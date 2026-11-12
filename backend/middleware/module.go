// Consolidated source; original file boundaries are marked below.
package middleware

import (
	context "context"
	sql "database/sql"
	errors "errors"
	gin "github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
	uuid "github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	rate "golang.org/x/time/rate"
	log "log"
	http "net/http"
	os "os"
	strconv "strconv"
	strings "strings"
	sync "sync"
	config "tg-attendance/backend/config"
	models "tg-attendance/backend/models"
	security "tg-attendance/backend/security"
	services "tg-attendance/backend/services"
	time "time"
)

// ---- Source: backend/middleware/auth.go ----
// Auth requires an active user session (JWT sid → auth_sessions).
func Auth(_ *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := extractToken(c)
		if tokenString == "" {
			models.SendError(c, 401, 1002, "Missing authorization token")
			c.Abort()
			return
		}
		if !authenticateSessionJWT(c, tokenString, true, false) {
			return
		}
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != authHeader {
			return strings.TrimSpace(token)
		}
	}
	if allowsBrowserDownloadToken(c) {
		if token := strings.TrimSpace(c.Query("token")); token != "" {
			return token
		}
	}
	if services.IsProductionEnv() {
		return ""
	}
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}
	if token, err := c.Cookie("token"); err == nil && token != "" {
		return strings.TrimSpace(token)
	}
	return ""
}

// Browser-initiated installer downloads cannot set Authorization headers.
func allowsBrowserDownloadToken(c *gin.Context) bool {
	if c.Request == nil || c.Request.Method != http.MethodGet {
		return false
	}
	return strings.HasSuffix(c.Request.URL.Path, "/client-download/file")
}
func normalizeRoleName(roleName string) string {
	switch strings.TrimSpace(strings.ToLower(roleName)) {
	case "管理员", "admin", "administrator", "超级管理员", "superadmin":
		return "admin"
	case "审计员", "auditor":
		return "auditor"
	case "操作员", "operator", "ops":
		return "operator"
	case "员工", "employee":
		return "employee"
	case "查看员", "viewer":
		return "viewer"
	default:
		return "auditor"
	}
}

// RequirePermission checks permission using DB-backed role (JWT carries no permissions).
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetBool("standalonePolicyValidated") {
			c.Next()
			return
		}
		userID := c.GetString("userID")
		if userID == "" {
			models.SendError(c, 401, 1002, "User not authenticated")
			c.Abort()
			return
		}

		role := c.GetString("role")
		var perms []string
		if p, exists := c.Get("permissions"); exists {
			if list, ok := p.([]string); ok {
				perms = list
			}
		}
		if dbRole, dbPerms, ok := loadUserAuthFromDB(c.Request.Context(), userID); ok {
			c.Set("role", dbRole)
			c.Set("permissions", dbPerms)
			role = dbRole
			perms = dbPerms
		}

		if checkPermissionList(role, perms, permission) {
			c.Next()
			return
		}
		models.SendError(c, 403, 1003, "Permission denied: "+permission)
		c.Abort()
	}
}
func RequireAnyPermission(permissions ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetBool("standalonePolicyValidated") {
			c.Next()
			return
		}
		userID := c.GetString("userID")
		if userID == "" {
			models.SendError(c, 401, 1002, "User not authenticated")
			c.Abort()
			return
		}

		role := c.GetString("role")
		var perms []string
		if p, exists := c.Get("permissions"); exists {
			if list, ok := p.([]string); ok {
				perms = list
			}
		}
		if dbRole, dbPerms, ok := loadUserAuthFromDB(c.Request.Context(), userID); ok {
			c.Set("role", dbRole)
			c.Set("permissions", dbPerms)
			role = dbRole
			perms = dbPerms
		}

		if checkAnyPermissionList(role, perms, permissions...) {
			c.Next()
			return
		}
		models.SendError(c, 403, 1003, "Permission denied")
		c.Abort()
	}
}

// ---- Source: backend/middleware/employee_scope.go ----
var employeeScopeDB *sql.DB

func InitEmployeeScopeDB(db *sql.DB) {
	employeeScopeDB = db
}

// ResolveScopedEmployeeID loads employees.id for portal user.
func ResolveScopedEmployeeID(c *gin.Context) (uuid.UUID, bool) {
	if employeeScopeDB == nil {
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(c.GetString("userID"))
	if err != nil {
		return uuid.Nil, false
	}
	var empID uuid.UUID
	err = employeeScopeDB.QueryRowContext(c.Request.Context(),
		`SELECT id FROM employees WHERE user_id = $1 AND status = 1`, userID).Scan(&empID)
	if err != nil {
		return uuid.Nil, false
	}
	return empID, true
}

// IsAdminRole reports whether the authenticated user is an administrator.
func IsAdminRole(c *gin.Context) bool {
	return normalizeRoleName(c.GetString("role")) == "admin"
}

// ---- Source: backend/middleware/http.go ----
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	limit    rate.Limit
	burst    int
}

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic recovered: %v", err)
				models.SendError(c, 500, 5000, "Internal server error")
				c.Abort()
			}
		}()
		c.Next()
	}
}
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if config.IsOriginAllowed(origin) {
			if origin != "" {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
				c.Header("Access-Control-Allow-Credentials", "true")
			}
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Device-Id, X-Timestamp, X-Nonce, X-Signature, X-Signature-Version, X-Upload-Id, X-File-Name, X-File-Type, X-File-Id, X-Chunk-Index, X-Total-Chunks, X-Chunk-Checksum, X-Upload-Nonce")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// ---- Source: backend/middleware/metrics.go ----
// Metrics records request counts and latency-friendly trace headers.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		services.GlobalMetrics.ObserveHTTP(c.Writer.Status())
		traceID := c.GetString("requestID")
		c.Header("X-Trace-Id", traceID)
		if sessionID := c.GetString("sessionID"); sessionID != "" {
			c.Header("X-Session-Id", sessionID)
		}
		if uploadID := c.GetString("uploadId"); uploadID != "" {
			c.Header("X-Upload-Id", uploadID)
		}
		_ = start
	}
}

// ---- Source: backend/middleware/permissions.go ----
var (
	permissionDB  *sql.DB
	permissionTTL = 30 * time.Second
)

type permissionCacheEntry struct {
	role   string
	perms  []string
	expiry time.Time
}

var permissionCache sync.Map

// InitPermissionDB 启用 RequirePermission 的数据库实时权限校验（带 TTL 缓存）
func InitPermissionDB(db *sql.DB) {
	permissionDB = db
	if v := os.Getenv("PERMISSION_CACHE_TTL_SECONDS"); v != "" {
		if sec, err := strconv.Atoi(v); err == nil && sec >= 0 {
			permissionTTL = time.Duration(sec) * time.Second
		}
	}
}

func loadUserAuthFromDB(ctx context.Context, userID string) (role string, perms []string, ok bool) {
	if permissionDB == nil || userID == "" {
		return "", nil, false
	}
	if permissionTTL > 0 {
		if v, hit := permissionCache.Load(userID); hit {
			entry := v.(permissionCacheEntry)
			if time.Now().Before(entry.expiry) {
				return entry.role, entry.perms, true
			}
			permissionCache.Delete(userID)
		}
	}

	roleName := "auditor"
	err := permissionDB.QueryRowContext(ctx, `
		SELECT COALESCE(r.name, 'auditor')
		FROM user_roles ur
		JOIN roles r ON ur.role_id = r.id
		WHERE ur.user_id = $1
		LIMIT 1`, userID).Scan(&roleName)
	if err != nil && err != sql.ErrNoRows {
		return "", nil, false
	}

	rows, err := permissionDB.QueryContext(ctx, `
		SELECT DISTINCT jsonb_array_elements_text(r.permissions)
		FROM user_roles ur
		JOIN roles r ON ur.role_id = r.id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return "", nil, false
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var perm string
		if err := rows.Scan(&perm); err != nil {
			continue
		}
		seen[perm] = struct{}{}
	}
	perms = make([]string, 0, len(seen))
	for p := range seen {
		perms = append(perms, p)
	}

	normalizedRole := normalizeRoleName(roleName)
	if permissionTTL > 0 {
		permissionCache.Store(userID, permissionCacheEntry{
			role:   normalizedRole,
			perms:  perms,
			expiry: time.Now().Add(permissionTTL),
		})
	}
	return normalizedRole, perms, true
}
func checkPermissionList(role string, perms []string, required string) bool {
	if normalizeRoleName(role) == "admin" {
		return true
	}
	for _, p := range perms {
		if p == required {
			return true
		}
	}
	return false
}
func checkAnyPermissionList(role string, perms []string, required ...string) bool {
	if normalizeRoleName(role) == "admin" {
		return true
	}
	allowed := make(map[string]struct{}, len(required))
	for _, p := range required {
		allowed[p] = struct{}{}
	}
	for _, p := range perms {
		if _, ok := allowed[p]; ok {
			return true
		}
	}
	return false
}

// UserHasPermission checks whether the authenticated user holds a permission.
func UserHasPermission(c *gin.Context, permission string) bool {
	if c.GetString("standaloneRole") != "" {
		if c.GetString("standaloneRole") == "admin" {
			return true
		}
		raw, _ := c.Get("permissions")
		perms, _ := raw.([]string)
		for _, p := range perms {
			if p == permission {
				return true
			}
		}
		return false
	}
	userID := c.GetString("userID")
	if userID == "" {
		return false
	}
	role := c.GetString("role")
	var perms []string
	if p, ok := c.Get("permissions"); ok {
		if list, ok := p.([]string); ok {
			perms = list
		}
	}
	if dbRole, dbPerms, ok := loadUserAuthFromDB(c.Request.Context(), userID); ok {
		role = dbRole
		perms = dbPerms
	}
	return checkPermissionList(role, perms, permission)
}
func loadUsernameFromDB(ctx context.Context, userID string) (string, bool) {
	if permissionDB == nil || userID == "" {
		return "", false
	}
	var username string
	if err := permissionDB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&username); err != nil {
		return "", false
	}
	return username, true
}
func loadUserAccountActive(ctx context.Context, userID string) (bool, bool) {
	if permissionDB == nil || userID == "" {
		return false, false
	}
	var status int
	if err := permissionDB.QueryRowContext(ctx, `SELECT status FROM users WHERE id = $1`, userID).Scan(&status); err != nil {
		return false, false
	}
	return status == 1, true
}

// ---- Source: backend/middleware/salary_view_gate.go ----
const SalaryViewUnlockHeader = "X-Salary-View-Unlock"

// ---- Source: backend/middleware/session_auth.go ----
var sessionSvc *services.SessionService

func GetSessionService() *services.SessionService {
	return sessionSvc
}
func abortAuthServiceUnavailable(c *gin.Context) {
	models.SendError(c, 503, 5003, "Authentication service temporarily unavailable")
	c.Abort()
}
func abortAuthError(c *gin.Context, code int, message string) {
	models.SendError(c, code, 1002, message)
	c.Abort()
}

// authenticateSessionJWT parses sid-only JWT and loads active session into context.
func authenticateSessionJWT(c *gin.Context, tokenString string, requireUser, requireDevice bool) bool {
	if sessionSvc == nil {
		abortAuthServiceUnavailable(c)
		return false
	}

	sid, err := services.ParseSessionJWT(tokenString)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			abortAuthError(c, 401, "Token expired")
		} else {
			abortAuthError(c, 401, "Invalid token")
		}
		return false
	}

	sess, err := sessionSvc.ValidateActiveSession(c.Request.Context(), sid)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrSessionRevoked):
			abortAuthError(c, 401, "Session has been revoked")
		case errors.Is(err, services.ErrSessionExpired):
			abortAuthError(c, 401, "Session expired")
		case errors.Is(err, services.ErrSessionNotFound):
			abortAuthError(c, 401, "Session not found")
		default:
			abortAuthServiceUnavailable(c)
		}
		return false
	}

	c.Set("sessionID", sid)
	c.Set("sessionSubjectType", sess.SubjectType)
	c.Set("sessionSubjectID", sess.SubjectID.String())

	switch sess.SubjectType {
	case services.SessionSubjectUser:
		if requireDevice {
			abortAuthError(c, 403, "Device session required")
			return false
		}
		return populateUserContext(c, sess.SubjectID.String())
	case services.SessionSubjectDevice:
		if requireUser {
			abortAuthError(c, 403, "User session required")
			return false
		}
		deviceID := sess.SubjectID.String()
		c.Set("deviceID", deviceID)
		c.Set("deviceId", deviceID)
		c.Set("device_id", deviceID)
		return true
	default:
		abortAuthError(c, 401, "Invalid session subject")
		return false
	}
}
func populateUserContext(c *gin.Context, userID string) bool {
	if active, ok := loadUserAccountActive(c.Request.Context(), userID); !ok || !active {
		abortAuthError(c, 401, "User account is disabled or not found")
		return false
	}
	c.Set("userID", userID)
	if username, ok := loadUsernameFromDB(c.Request.Context(), userID); ok {
		c.Set("username", username)
	}
	if role, perms, ok := loadUserAuthFromDB(c.Request.Context(), userID); ok {
		c.Set("role", normalizeRoleName(role))
		c.Set("permissions", perms)
	} else {
		abortAuthError(c, 401, "User account is disabled or not found")
		return false
	}
	return true
}
// ---- Source: backend/middleware/signature.go ----
var signatureNonceStore *security.DistributedNonceStore

// SignatureMetrics tracks signature verification metrics
type SignatureMetrics struct {
	SuccessCount           int64     // 签名验证成功计数
	FailureCount           int64     // 签名验证失败计数
	MissingSecretCount     int64     // device_secret 缺失或为空计数
	TimestampRejectedCount int64     // 时间戳超过 ±5 分钟计数
	InvalidFormatCount     int64     // 无效格式计数
	LastUpdateTime         time.Time // 最后更新时间
	mu                     sync.RWMutex
}

var globalSignatureMetrics = &SignatureMetrics{
	LastUpdateTime: time.Now(),
}

// ---- Source: backend/middleware/telegram_worker.go ----
// AuthenticateTelegramWorker validates the Telegram Worker shared secret from
// X-Telegram-Worker-Secret or Authorization: Bearer. On failure it writes 401
// and returns false (caller should Abort).
func AuthenticateTelegramWorker(c *gin.Context, telegram *services.WorkforceTelegramService) bool {
	secret := strings.TrimSpace(c.GetHeader("X-Telegram-Worker-Secret"))
	if secret == "" {
		secret = strings.TrimSpace(c.GetHeader("Authorization"))
		secret = strings.TrimPrefix(secret, "Bearer ")
	}
	if err := telegram.ValidateWorkerSecret(c.Request.Context(), secret); err != nil {
		models.SendError(c, 401, 1002, "invalid worker secret")
		return false
	}
	return true
}

// TelegramWorkerAuth is a Gin middleware that requires a valid worker secret.
// Mount on the authenticated /telegram/worker subgroup only — do not mount on /pair.
func TelegramWorkerAuth(telegram *services.WorkforceTelegramService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if telegram == nil || !AuthenticateTelegramWorker(c, telegram) {
			if telegram == nil {
				models.SendError(c, 401, 1002, "invalid worker secret")
			}
			c.Abort()
			return
		}
		c.Next()
	}
}

// ---- Source: backend/middleware/ws_ticket_cookie.go ----
const wsTicketCookieName = "ws_ticket"
const wsTicketContextKey = "wsTicket"

