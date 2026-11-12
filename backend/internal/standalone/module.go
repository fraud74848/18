// Consolidated source; original file boundaries are marked below.
package standalone

import (
	"bytes"
	context "context"
	rand "crypto/rand"
	sha256 "crypto/sha256"
	subtle "crypto/subtle"
	sql "database/sql"
	"encoding/base64"
	hex "encoding/hex"
	json "encoding/json"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	uuid "github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	bcrypt "golang.org/x/crypto/bcrypt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/png"
	io "io"
	log "log"
	"math/big"
	http "net/http"
	url "net/url"
	os "os"
	filepath "path/filepath"
	regexp "regexp"
	"runtime"
	"strconv"
	strings "strings"
	"sync"
	tgbot "tg-attendance/backend/internal/tgbot"
	models "tg-attendance/backend/models"
	services "tg-attendance/backend/services"
	time "time"
)

// ---- Source: backend/internal/standalone/access.go ----
// Deny by default before legacy handlers/permissions run. A member cannot reach
// settings mutations, exports, account management or any unrelated admin API.
// Every protected endpoint is explicitly classified. No wildcard grants.
var routePermissions = map[string][]string{
	"PUT /api/v1/standalone/memory/policy":                                      {"maintenance:edit"},
	"GET /api/v1/admin/workforce/telegram/punch/usage":                          {"punch:view"},
	"GET /api/v1/devices":                                                       {"punch:view", "telegram:view"},
	"GET /api/v1/admin/employees":                                               {"attendance:view", "accounts:view", "punch:view", "records:view", "penalty:view", "performance:view", "attendance_review:view", "telegram:view"},
	"POST /api/v1/admin/employees":                                              {"attendance:edit"},
	"PUT /api/v1/admin/employees/:id":                                           {"attendance:edit"},
	"DELETE /api/v1/admin/employees/:id":                                        {"attendance:delete"},
	"POST /api/v1/admin/employee-accounts":                                      {"attendance:edit"},
	"POST /api/v1/admin/employees/:id/disable":                                  {"attendance:edit"},
	"GET /api/v1/admin/invite-codes":                                            {"attendance:view"},
	"GET /api/v1/admin/invite-codes/settings":                                   {"attendance:view"},
	"PUT /api/v1/admin/invite-codes/settings":                                   {"attendance:edit"},
	"GET /api/v1/admin/invite-codes/history":                                    {"attendance:view"},
	"POST /api/v1/admin/invite-codes/:id/revoke":                                {"attendance:edit"},
	"POST /api/v1/admin/employees/:id/invite-code/regenerate":                   {"attendance:edit"},
	"GET /api/v1/admin/attendance/records":                                      {"attendance:view"},
	"POST /api/v1/admin/attendance/records":                                     {"attendance:edit"},
	"POST /api/v1/admin/attendance/records/import":                              {"attendance:edit"},
	"GET /api/v1/admin/attendance/today-absentees":                              {"attendance:view"},
	"GET /api/v1/admin/attendance/today-rate":                                   {"attendance:view"},
	"GET /api/v1/admin/workforce/telegram/settings":                             {"telegram:view", "punch:view", "records:view"},
	"PUT /api/v1/admin/workforce/telegram/settings":                             {"telegram:edit"},
	"POST /api/v1/admin/workforce/telegram/settings/regenerate-webhook-secret":  {"telegram:edit"},
	"GET /api/v1/admin/workforce/telegram/worker-connection":                    {"telegram:edit"},
	"GET /api/v1/admin/workforce/telegram/groups/discover":                      {"telegram:edit"},
	"GET /api/v1/admin/workforce/telegram/groups":                               {"telegram:view", "punch:view", "records:view"},
	"POST /api/v1/admin/workforce/telegram/groups":                              {"telegram:edit"},
	"DELETE /api/v1/admin/workforce/telegram/groups/:id":                        {"telegram:delete"},
	"GET /api/v1/admin/workforce/telegram/bindings":                             {"telegram:view"},
	"POST /api/v1/admin/workforce/telegram/bindings":                            {"telegram:edit"},
	"DELETE /api/v1/admin/workforce/telegram/bindings/:id":                      {"telegram:delete"},
	"POST /api/v1/admin/workforce/telegram/bindings/:id/reset-daily":            {"telegram:reset"},
	"POST /api/v1/admin/workforce/telegram/reset-daily":                         {"telegram:reset"},
	"GET /api/v1/admin/workforce/telegram/activity-types":                       {"telegram:view", "punch:view", "records:view"},
	"PUT /api/v1/admin/workforce/telegram/activity-types":                       {"telegram:edit"},
	"DELETE /api/v1/admin/workforce/telegram/activity-types/:code":              {"telegram:delete"},
	"GET /api/v1/admin/workforce/telegram/activity-fines":                       {"telegram:view"},
	"POST /api/v1/admin/workforce/telegram/activity-fines/batch":                {"telegram:edit"},
	"POST /api/v1/admin/workforce/telegram/activity-fines":                      {"telegram:edit"},
	"DELETE /api/v1/admin/workforce/telegram/activity-fines/:id":                {"telegram:delete"},
	"GET /api/v1/admin/workforce/telegram/activity-sessions":                    {"records:view", "punch:view"},
	"GET /api/v1/admin/workforce/telegram/work-sessions":                        {"records:view"},
	"GET /api/v1/admin/workforce/telegram/handovers":                            {"records:view"},
	"GET /api/v1/admin/workforce/telegram/rankings":                             {"records:view"},
	"GET /api/v1/admin/workforce/telegram/export":                               {"records:export"},
	"POST /api/v1/admin/workforce/telegram/daily-reset":                         {"telegram:reset"},
	"GET /api/v1/admin/workforce/telegram/handover-config":                      {"telegram:view"},
	"PUT /api/v1/admin/workforce/telegram/handover-config":                      {"telegram:edit"},
	"GET /api/v1/admin/workforce/telegram/push-settings":                        {"telegram:view"},
	"PUT /api/v1/admin/workforce/telegram/push-settings":                        {"telegram:notify"},
	"POST /api/v1/admin/workforce/telegram/push-settings/test":                  {"telegram:notify"},
	"GET /api/v1/admin/workforce/telegram/attendance-review/settings":           {"attendance_review:view"},
	"PUT /api/v1/admin/workforce/telegram/attendance-review/settings":           {"attendance_review:edit"},
	"GET /api/v1/admin/workforce/telegram/attendance-review/tasks":              {"attendance_review:view"},
	"POST /api/v1/admin/workforce/telegram/attendance-review/tasks/:id/confirm": {"attendance_review:approve"},
	"POST /api/v1/admin/workforce/telegram/punch/checkin":                       {"punch:edit"},
	"POST /api/v1/admin/workforce/telegram/punch/checkout":                      {"punch:edit"},
	"POST /api/v1/admin/workforce/telegram/punch/activity/start":                {"punch:edit"},
	"POST /api/v1/admin/workforce/telegram/punch/activity/back":                 {"punch:edit"},
	"GET /api/v1/admin/workforce/telegram/punch/me":                             {"punch:view"},
	"GET /api/v1/admin/workforce/telegram/work-fines":                           {"telegram:view"},
	"POST /api/v1/admin/workforce/telegram/work-fines/batch":                    {"telegram:edit"},
	"POST /api/v1/admin/workforce/telegram/work-fines":                          {"telegram:edit"},
	"DELETE /api/v1/admin/workforce/telegram/work-fines/:id":                    {"telegram:delete"},
	"GET /api/v1/admin/penalty/push-settings":                                   {"penalty:view"},
	"PUT /api/v1/admin/penalty/push-settings/enabled":                           {"penalty:notify"},
	"PUT /api/v1/admin/penalty/push-settings/token":                             {"penalty:notify"},
	"POST /api/v1/admin/penalty/push-settings/discover":                         {"penalty:notify"},
	"PUT /api/v1/admin/penalty/push-settings/bind":                              {"penalty:notify"},
	"DELETE /api/v1/admin/penalty/push-settings/bind":                           {"penalty:notify"},
	"POST /api/v1/admin/penalty/push-settings/test":                             {"penalty:notify"},
	"POST /api/v1/admin/penalty/push-notify":                                    {"penalty:notify"},
	"GET /api/v1/admin/penalty/records":                                         {"penalty:view"},
	"POST /api/v1/admin/penalty/record":                                         {"penalty:edit"},
	"PUT /api/v1/admin/penalty/records/:id":                                     {"penalty:edit"},
	"DELETE /api/v1/admin/penalty/records/:id":                                  {"penalty:delete"},
	"GET /api/v1/admin/performance":                                             {"performance:view"},
	"POST /api/v1/admin/performance/batch":                                      {"performance:edit"},
	"GET /api/v1/admin/performance/rule-settings":                               {"performance:view"},
	"PUT /api/v1/admin/performance/rule-settings":                               {"performance:edit"},
	"PUT /api/v1/admin/performance/:employeeId/:month":                          {"performance:edit"},
	"DELETE /api/v1/admin/performance/:employeeId/:month":                       {"performance:delete"},
	"GET /api/v1/admin/performance/push-settings":                               {"performance:view"},
	"PUT /api/v1/admin/performance/push-settings/enabled":                       {"performance:notify"},
	"PUT /api/v1/admin/performance/push-settings/token":                         {"performance:notify"},
	"POST /api/v1/admin/performance/push-settings/discover":                     {"performance:notify"},
	"PUT /api/v1/admin/performance/push-settings/bind":                          {"performance:notify"},
	"DELETE /api/v1/admin/performance/push-settings/bind":                       {"performance:notify"},
	"POST /api/v1/admin/performance/push-settings/test":                         {"performance:notify"},
	"POST /api/v1/admin/performance/push-notify":                                {"performance:notify"},
	"GET /api/v1/standalone/login-policy":                                       {"accounts:view"},
	"PUT /api/v1/standalone/login-policy":                                       {"accounts:security"},
	"PUT /api/v1/standalone/accounts/:id/totp":                                  {"accounts:security"},
	"GET /api/v1/standalone/accounts":                                           {"accounts:view"},
	"POST /api/v1/standalone/accounts":                                          {"accounts:edit"},
	"PUT /api/v1/standalone/accounts/:id":                                       {"accounts:edit"},
	"POST /api/v1/standalone/accounts/:id/telegram":                             {"accounts:edit"},
	"POST /api/v1/standalone/accounts/:id/reset-daily":                          {"accounts:reset"},
	"POST /api/v1/standalone/accounts/:id/password":                             {"accounts:reset_password"},
	"PUT /api/v1/standalone/accounts/:id/roles":                                 {"roles:assign"},
	"POST /api/v1/standalone/accounts/:id/bind-code":                            {"accounts:edit"},
	"GET /api/v1/standalone/roles":                                              {"roles:view"},
	"POST /api/v1/standalone/roles":                                             {"roles:edit"},
	"PUT /api/v1/standalone/roles/:id":                                          {"roles:edit"},
	"DELETE /api/v1/standalone/roles/:id":                                       {"roles:delete"},
	"GET /api/v1/standalone/status":                                             {"overview:view"},
	"POST /api/v1/standalone/webhook":                                           {"telegram:edit"},
	"GET /api/v1/standalone/exports":                                            {"records:export"},
	"GET /api/v1/standalone/exports/:filename":                                  {"records:export"},
	"POST /api/v1/standalone/inbox/:id/resolve":                                 {"overview:approve"},
	"POST /api/v1/standalone/memory/clean":                                      {"maintenance:clean"},
	"GET /api/v1/standalone/memory":                                             {"maintenance:view"},
	"POST /api/v1/standalone/export-authorize":                                  {"attendance:export", "penalty:export", "performance:export"},
}

func hasGrant(c *gin.Context, permission string) bool {
	if c.GetString("standaloneRole") == "admin" {
		return true
	}
	raw, _ := c.Get("permissions")
	list, _ := raw.([]string)
	contains := func(key string) bool {
		for _, p := range list {
			if p == key {
				return true
			}
		}
		return false
	}
	if !contains(permission) {
		return false
	}
	parts := strings.SplitN(permission, ":", 2)
	if len(parts) == 2 && parts[0] != "data" && parts[1] != "view" {
		return contains(parts[0] + ":view")
	}
	return true
}
func (a Auth) Access() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		if path == "/api/v1/auth/me" || path == "/api/v1/auth/logout" || path == "/api/v1/auth/password" || path == "/api/v1/standalone/bind-code" || path == "/api/v1/standalone/bind-telegram" || path == "/api/v1/meta/modules" {
			c.Next()
			return
		}
		required, known := routePermissions[c.Request.Method+" "+path]
		allowed := false
		for _, p := range required {
			if hasGrant(c, p) {
				allowed = true
			}
		}
		if !known || !allowed {
			models.SendError(c, 403, 403, "没有此操作权限")
			c.Abort()
			return
		}
		// The legacy route checks must use this validated per-route policy, not cached broad permissions.
		if strings.HasSuffix(path, "/attendance-review/tasks/:id/confirm") && !hasGrant(c, "attendance_review:review") {
			models.SendError(c, 403, 403, "还需要审核权限")
			c.Abort()
			return
		}
		c.Set("standalonePolicyValidated", true)
		if c.GetString("standaloneRole") == "admin" || hasGrant(c, "data:all") {
			c.Next()
			return
		}
		dataRoute := strings.HasPrefix(path, "/api/v1/admin/employees") || strings.Contains(path, "/employee-accounts") || strings.Contains(path, "/attendance/") || strings.Contains(path, "/penalty/record") || path == "/api/v1/admin/performance" || strings.Contains(path, "/performance/batch") || strings.Contains(path, "/performance/:employeeId") || strings.Contains(path, "/activity-sessions") || strings.Contains(path, "/work-sessions") || strings.Contains(path, "/rankings") || strings.Contains(path, "/handovers") || strings.Contains(path, "/attendance-review/tasks") || strings.HasSuffix(path, "/export") || strings.Contains(path, "/punch/")
		if dataRoute {

			var emp string
			if err := a.DB.QueryRowContext(c.Request.Context(), `SELECT id FROM employees WHERE user_id=$1 AND status=1`, c.GetString("userID")).Scan(&emp); err != nil {
				models.SendError(c, 403, 403, "账号未关联有效员工")
				c.Abort()
				return
			}
			if c.Request.Method != "GET" && !strings.Contains(path, "/punch/") && !a.scopeOwnMutation(c, emp) {
				models.SendError(c, 403, 403, "只能修改本人数据，管理其他员工需要全员数据权限")
				c.Abort()
				return
			}
			c.Set("standaloneEmployeeID", emp)
			q := c.Request.URL.Query()
			for _, key := range []string{"employeeId", "employee_id", "employee_ids", "employee_ids[]"} {
				q.Del(key)
				q.Set(key, emp)
			}
			c.Request.URL.RawQuery = q.Encode()
			// Aggregate/group-wide records cannot safely be scoped by these legacy handlers.
			if strings.Contains(path, "/rankings") || strings.Contains(path, "/handovers") || strings.Contains(path, "/attendance-review/tasks") || strings.Contains(path, "/export") || strings.Contains(path, "/today-") {
				models.SendError(c, 403, 403, "此功能需要全员数据权限")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// ---- Source: backend/internal/standalone/accounts.go ----
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{2,63}$`)

func (a Auth) Accounts(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("search"))
	filter := ` FROM standalone_subaccounts sa JOIN users u ON u.id=sa.user_id JOIN employees e ON e.user_id=u.id WHERE ($1='' OR strpos(lower(u.username),lower($1))>0 OR strpos(lower(e.name),lower($1))>0 OR strpos(lower(COALESCE(e.employee_id,'')),lower($1))>0 OR EXISTS(SELECT 1 FROM employee_telegram_bindings b WHERE b.employee_id=e.id AND strpos(b.telegram_user_id::text,$1)>0))`
	page, size, total, err := listPage(c, a.DB, `SELECT COUNT(*)`+filter, keyword)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}

	rows, err := a.DB.QueryContext(c.Request.Context(), `SELECT u.id,u.username,u.status,e.id,COALESCE(e.name,''),COALESCE(e.employee_id,''),COALESCE((SELECT telegram_user_id::text FROM employee_telegram_bindings WHERE employee_id=e.id LIMIT 1),''),COALESCE((SELECT json_agg(role_id) FROM user_roles WHERE user_id=u.id),'[]'::json),u.last_login_at,(u.status=1 AND EXISTS(SELECT 1 FROM standalone_sessions ss WHERE ss.user_id=u.id AND ss.expires_at>NOW() AND ss.last_seen_at>NOW()-interval '2 minutes')),u.standalone_totp_required,(u.standalone_totp_secret!='')`+filter+` ORDER BY sa.created_at DESC,u.id DESC LIMIT $2 OFFSET $3`, keyword, size, (page-1)*size)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, name, emp, display, code, tg string
		var status int
		var rawRoles []byte
		var lastLogin *time.Time
		var online, totpRequired, totpBound bool
		if err = rows.Scan(&id, &name, &status, &emp, &display, &code, &tg, &rawRoles, &lastLogin, &online, &totpRequired, &totpBound); err != nil {
			models.SendInternalError(c, err)
			return
		}
		var roleIDs []string
		_ = json.Unmarshal(rawRoles, &roleIDs)
		out = append(out, gin.H{"totpRequired": totpRequired, "totpBound": totpBound, "lastLoginAt": lastLogin, "online": online, "roleIds": roleIDs, "id": id, "username": name, "status": status, "employeeId": emp, "name": display, "employeeCode": code, "telegramUserId": tg})
	}
	if err = rows.Err(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out, "page": page, "pageSize": size, "total": total})
}

// Self-service binding always uses the authenticated account; no employee/account selector is accepted.
func (a Auth) BindOwnTelegram(c *gin.Context) {
	var in struct {
		TelegramUserID string `json:"telegramUserId"`
	}
	if c.ShouldBindJSON(&in) != nil {
		models.SendError(c, 400, 400, "请输入数字 TG ID")
		return
	}
	tgID, err := strconv.ParseInt(strings.TrimSpace(in.TelegramUserID), 10, 64)
	if err != nil || tgID <= 0 || tgID > 4503599627370495 {
		models.SendError(c, 400, 400, "TG ID 格式无效")
		return
	}
	var username string
	err = a.DB.QueryRowContext(c.Request.Context(), `SELECT u.username FROM users u JOIN standalone_subaccounts sa ON sa.user_id=u.id WHERE u.id=$1 AND u.status=1`, c.GetString("userID")).Scan(&username)
	if err != nil {
		models.SendError(c, 403, 403, "仅有效员工子账号可以绑定")
		return
	}
	svc := services.NewWorkforceTelegramService(a.DB, services.NewWorkforceService(a.DB))
	result, err := svc.BindTelegramByUsername(c.Request.Context(), tgID, "", username, strconv.FormatInt(tgID, 10))
	if err != nil {
		models.SendError(c, 409, 409, "绑定失败：ID 已被使用、账号已有其他绑定或员工不可用，请联系管理员核对")
		return
	}
	models.Send(c, 200, 0, "success", result)
}

// Account actions resolve the employee from the account, never from a client-supplied employee ID.
func (a Auth) AccountAttendance(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 400, "账号无效")
		return
	}
	if c.GetString("standaloneRole") != "admin" && !hasGrant(c, "data:all") && id.String() != c.GetString("userID") {
		models.SendError(c, 403, 403, "管理其他员工需要全员数据权限")
		return
	}
	var employeeID uuid.UUID
	var chatID string
	err = a.DB.QueryRowContext(c.Request.Context(), `SELECT e.id,COALESCE(b.chat_id,'') FROM standalone_subaccounts sa JOIN employees e ON e.user_id=sa.user_id LEFT JOIN employee_telegram_bindings b ON b.employee_id=e.id WHERE sa.user_id=$1`, id).Scan(&employeeID, &chatID)
	if err == sql.ErrNoRows {
		models.SendError(c, 404, 404, "子账号不存在")
		return
	}
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	service := services.NewWorkforceTelegramService(a.DB, services.NewWorkforceService(a.DB))
	if strings.HasSuffix(c.FullPath(), "/telegram") {
		var in struct {
			TelegramUserID string `json:"telegramUserId"`
		}
		if c.ShouldBindJSON(&in) != nil {
			models.SendError(c, 400, 400, "请输入数字 TG ID")
			return
		}
		tgID, parseErr := strconv.ParseInt(strings.TrimSpace(in.TelegramUserID), 10, 64)
		if parseErr != nil || tgID <= 0 || tgID > 4503599627370495 {
			models.SendError(c, 400, 400, "TG ID 格式无效")
			return
		}
		// A previous private chat belongs to the previous Telegram user.
		if !strings.HasPrefix(chatID, "-") {
			chatID = strconv.FormatInt(tgID, 10)
		}
		result, bindErr := service.CreateBinding(c.Request.Context(), employeeID, tgID, "", chatID)
		if bindErr != nil {
			models.SendError(c, 409, 409, "绑定失败：TG ID 已被使用或员工账号已停用")
			return
		}
		models.Send(c, 200, 0, "success", result)
		return
	}
	result, resetErr := service.ResetEmployeeDailyAttendance(c.Request.Context(), employeeID, chatID)
	if resetErr != nil {
		models.SendInternalError(c, resetErr)
		return
	}
	models.Send(c, 200, 0, "success", result)
}

func (a Auth) CreateAccount(c *gin.Context) {
	var in struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		EmployeeID string `json:"employeeId"`
	}
	if c.ShouldBindJSON(&in) != nil || !usernamePattern.MatchString(in.Username) || len(in.Password) < 6 || len(in.Password) > 72 {
		models.SendError(c, 400, 400, "账号需为3–64位字母数字或_.-，密码需为6–72字节")
		return
	}
	emp, err := uuid.Parse(in.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 400, "请选择员工")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	tx, err := a.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	id := uuid.New()
	_, err = tx.ExecContext(c.Request.Context(), `INSERT INTO users(id,username,password_hash,status) VALUES($1,$2,$3,1)`, id, in.Username, string(hash))
	if err != nil {
		models.SendError(c, 409, 409, "账号已存在或无法创建")
		return
	}
	res, err := tx.ExecContext(c.Request.Context(), `UPDATE employees SET user_id=$1,attendance_mode='tg_only' WHERE id=$2 AND user_id IS NULL AND status=1`, id, emp)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		models.SendError(c, 409, 409, "员工已关联账号或已停用，请选择其他员工")
		return
	}
	if _, err = tx.ExecContext(c.Request.Context(), `INSERT INTO standalone_subaccounts(user_id) VALUES($1);`, id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if _, err = tx.ExecContext(c.Request.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE name='查看员'`, id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 201, 0, "success", gin.H{"id": id})
}
func (a Auth) UpdateAccount(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 400, "账号无效")
		return
	}
	var in struct {
		Status   *int   `json:"status"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&in) != nil || (in.Status != nil && *in.Status != 0 && *in.Status != 1) || (in.Password != "" && (len(in.Password) < 6 || len(in.Password) > 72)) {
		models.SendError(c, 400, 400, "状态或密码格式错误")
		return
	}
	if in.Password != "" && !hasGrant(c, "accounts:reset_password") {
		models.SendError(c, 403, 403, "没有重置密码权限")
		return
	}
	if strings.HasSuffix(c.FullPath(), "/password") && (in.Status != nil || in.Password == "") {
		models.SendError(c, 400, 400, "请仅提交新密码")
		return
	}
	tx, err := a.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	var found string
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT user_id FROM standalone_subaccounts WHERE user_id=$1 FOR UPDATE`, id).Scan(&found); err != nil {
		models.SendError(c, 404, 404, "子账号不存在")
		return
	}
	if c.GetString("standaloneRole") != "admin" {
		grants, e := a.permissions(c.Request.Context(), id.String())
		if e != nil {
			models.SendInternalError(c, e)
			return
		}
		for _, p := range grants {
			if !hasGrant(c, p) {
				models.SendError(c, 403, 403, "不能管理权限高于自己的账号")
				return
			}
		}
	}
	if in.Status != nil {
		_, err = tx.ExecContext(c.Request.Context(), `UPDATE users SET status=$2,updated_at=NOW() WHERE id=$1`, id, *in.Status)
		if err != nil {
			models.SendInternalError(c, err)
			return
		}
	}
	if in.Status != nil {
		if _, err = tx.ExecContext(c.Request.Context(), `UPDATE employee_telegram_bindings SET status=$2 WHERE employee_id IN (SELECT id FROM employees WHERE user_id=$1)`, id, *in.Status); err != nil {
			models.SendInternalError(c, err)
			return
		}
	}
	if in.Password != "" {
		hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if e != nil {
			models.SendInternalError(c, e)
			return
		}
		_, err = tx.ExecContext(c.Request.Context(), `UPDATE users SET password_hash=$2,updated_at=NOW() WHERE id=$1`, id, string(hash))
		if err != nil {
			models.SendInternalError(c, err)
			return
		}
	}
	if _, err = tx.ExecContext(c.Request.Context(), `DELETE FROM standalone_sessions WHERE user_id=$1`, id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if _, err = tx.ExecContext(c.Request.Context(), `DELETE FROM standalone_bind_codes WHERE user_id=$1`, id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", nil)
}
func (a Auth) BindCode(c *gin.Context) {
	id := c.GetString("userID")
	if c.Param("id") != "" {
		id = c.Param("id")
	}
	var name string
	if err := a.DB.QueryRowContext(c.Request.Context(), `SELECT u.username FROM standalone_subaccounts s JOIN users u ON u.id=s.user_id WHERE u.id=$1 AND u.status=1`, id).Scan(&name); err != nil {
		models.SendError(c, 404, 404, "有效子账号不存在")
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		models.SendInternalError(c, err)
		return
	}
	code := hex.EncodeToString(b)
	if _, err := a.DB.ExecContext(c.Request.Context(), `INSERT INTO standalone_bind_codes(user_id,code_hash,expires_at) VALUES($1,$2,NOW()+interval '15 minutes') ON CONFLICT(user_id) DO UPDATE SET code_hash=EXCLUDED.code_hash,expires_at=EXCLUDED.expires_at`, id, digest(code)); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, http.StatusOK, 0, "success", gin.H{"command": "/bind " + name + " " + code, "expiresMinutes": 15})
}

// ---- Source: backend/internal/standalone/auth.go ----
type Auth struct{ DB *sql.DB }

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
// Admin login stays valid for 7 days from sign-in (fixed, not extended by activity).
// Keep in sync with the `interval '7 days'` written to standalone_sessions at login.
const sessionMaxAgeSeconds = 7 * 24 * 60 * 60

func (a Auth) cookie(c *gin.Context, value string, age int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("tg_admin", value, age, "/", "", os.Getenv("ENV") == "production", true)
}
func SameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			origin := c.GetHeader("Origin")
			if origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Host != c.Request.Host {
					c.AbortWithStatusJSON(403, gin.H{"code": 403, "message": "跨站请求被拒绝"})
					return
				}
			}
			if c.GetHeader("X-Requested-With") != "TGAttendance" {
				c.AbortWithStatusJSON(403, gin.H{"code": 403, "message": "缺少请求校验标记"})
				return
			}
		}
		c.Next()
	}
}
func (a Auth) Login(c *gin.Context) {
	var in struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		CaptchaID string `json:"captchaId"`
		Code      string `json:"code"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.Username) > 100 || len(in.Password) > 256 || len(in.CaptchaID) > 64 || len(in.Code) > 16 {
		models.SendError(c, 400, 400, "请求格式错误")
		return
	}
	// Database-backed throttle survives restarts. Username limit also prevents proxy-IP spoofing bypass.
	for _, key := range []string{"user:" + strings.ToLower(in.Username), "global"} {
		var attempts int
		err := a.DB.QueryRowContext(c.Request.Context(), `INSERT INTO standalone_login_attempts(bucket,attempts,expires_at) VALUES($1,1,NOW()+interval '15 minutes') ON CONFLICT(bucket) DO UPDATE SET attempts=CASE WHEN standalone_login_attempts.expires_at<NOW() THEN 1 ELSE standalone_login_attempts.attempts+1 END,expires_at=CASE WHEN standalone_login_attempts.expires_at<NOW() THEN NOW()+interval '15 minutes' ELSE standalone_login_attempts.expires_at END RETURNING attempts`, digest(key)).Scan(&attempts)
		limit := 10
		if key == "global" {
			limit = 100
		}
		if err != nil {
			models.SendError(c, 503, 503, "数据库暂不可用")
			return
		}
		if attempts > limit {
			models.SendError(c, 429, 429, "登录尝试过多，请15分钟后重试")
			return
		}
	}
	var id, hash, name, role string
	err := a.DB.QueryRowContext(c.Request.Context(), `SELECT u.id,u.password_hash,u.username,CASE WHEN EXISTS(SELECT 1 FROM standalone_subaccounts sa WHERE sa.user_id=u.id) THEN 'member' ELSE 'admin' END FROM users u WHERE u.username=$1 AND u.status=1 AND (EXISTS(SELECT 1 FROM standalone_subaccounts sa WHERE sa.user_id=u.id) OR EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.name IN ('管理员','admin')))`, in.Username).Scan(&id, &hash, &name, &role)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		models.SendError(c, 401, 401, "用户名或密码错误")
		return
	}
	ctx := c.Request.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	// Lock the user row so one authenticator code cannot be replayed by two parallel requests.
	var required bool
	var enc string
	var lastStep int64
	err = tx.QueryRowContext(ctx, `SELECT standalone_totp_required OR COALESCE((SELECT value='true' FROM system_settings WHERE key='standalone_totp_global'),false),standalone_totp_secret,standalone_totp_step FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&required, &enc, &lastStep)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	if required && enc == "" {
		// First-time Google Authenticator binding still needs the QR-code step.
		_ = tx.Rollback()
		a.issueLoginChallenge(c, id, name, hash)
		return
	}
	code := strings.TrimSpace(in.Code)
	valid := false
	step := int64(-1)
	if required {
		// Accounts with Google Authenticator enabled: the single code field takes the 6-digit dynamic code.
		secret, decryptErr := services.DecryptSecret(enc)
		if decryptErr == nil {
			step = acceptedTOTPStep(secret, code, time.Now(), lastStep)
			valid = step >= 0
		}
	} else if len(in.CaptchaID) > 0 {
		// Other accounts: the image captcha shown on the same page. It is single-use, win or lose.
		var answer string
		if tx.QueryRowContext(ctx, `DELETE FROM standalone_login_captchas WHERE id_hash=$1 AND expires_at>NOW() RETURNING answer_hash`, digest(in.CaptchaID)).Scan(&answer) == nil {
			valid = subtle.ConstantTimeCompare([]byte(answer), []byte(digest(in.CaptchaID+strings.ToUpper(code)))) == 1
		}
	}
	if !valid {
		if err = tx.Commit(); err != nil {
			models.SendInternalError(c, err)
			return
		}
		models.SendError(c, 400, 400, "验证码错误、已过期或已使用，请重试")
		return
	}
	if required {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET standalone_totp_step=$2 WHERE id=$1`, id, step); err != nil {
			models.SendInternalError(c, err)
			return
		}
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		models.SendInternalError(c, err)
		return
	}
	session := hex.EncodeToString(token)
	if _, err = tx.ExecContext(ctx, `WITH session AS(INSERT INTO standalone_sessions(token_hash,user_id,expires_at,last_seen_at) VALUES($1,$2,NOW()+interval '7 days',NOW())) UPDATE users SET last_login_at=NOW() WHERE id=$2`, digest(session), id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	a.DB.ExecContext(ctx, `DELETE FROM standalone_login_attempts WHERE bucket=$1`, digest("user:"+strings.ToLower(name)))
	a.cookie(c, session, sessionMaxAgeSeconds)
	models.Send(c, 200, 0, "success", a.identity(c, id, name, role))
}

// LoginMode tells the login form which verification input to show ("totp" or "captcha").
// Unknown, disabled and non-Google accounts all answer "captcha", so it never reveals whether a username exists.
func (a Auth) LoginMode(c *gin.Context) {
	ctx := c.Request.Context()
	c.Header("Cache-Control", "no-store")
	var attempts int
	if err := a.DB.QueryRowContext(ctx, `INSERT INTO standalone_login_attempts(bucket,attempts,expires_at) VALUES($1,1,NOW()+interval '15 minutes') ON CONFLICT(bucket) DO UPDATE SET attempts=CASE WHEN standalone_login_attempts.expires_at<NOW() THEN 1 ELSE standalone_login_attempts.attempts+1 END,expires_at=CASE WHEN standalone_login_attempts.expires_at<NOW() THEN NOW()+interval '15 minutes' ELSE standalone_login_attempts.expires_at END RETURNING attempts`, digest("login-mode-global")).Scan(&attempts); err != nil {
		models.SendError(c, 503, 503, "数据库暂不可用")
		return
	}
	mode := "captcha"
	username := strings.TrimSpace(c.Query("username"))
	if attempts <= 600 && username != "" && len(username) <= 100 {
		var required bool
		err := a.DB.QueryRowContext(ctx, `SELECT u.standalone_totp_required OR COALESCE((SELECT value='true' FROM system_settings WHERE key='standalone_totp_global'),false) FROM users u WHERE u.username=$1 AND u.status=1 AND (EXISTS(SELECT 1 FROM standalone_subaccounts sa WHERE sa.user_id=u.id) OR EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.name IN ('管理员','admin')))`, username).Scan(&required)
		if err == nil && required {
			mode = "totp"
		}
	}
	models.Send(c, 200, 0, "success", gin.H{"mode": mode})
}

// Captcha is fetched together with the login form so account, password and code are submitted in one step.
func (a Auth) Captcha(c *gin.Context) {
	ctx := c.Request.Context()
	var attempts int
	if err := a.DB.QueryRowContext(ctx, `INSERT INTO standalone_login_attempts(bucket,attempts,expires_at) VALUES($1,1,NOW()+interval '15 minutes') ON CONFLICT(bucket) DO UPDATE SET attempts=CASE WHEN standalone_login_attempts.expires_at<NOW() THEN 1 ELSE standalone_login_attempts.attempts+1 END,expires_at=CASE WHEN standalone_login_attempts.expires_at<NOW() THEN NOW()+interval '15 minutes' ELSE standalone_login_attempts.expires_at END RETURNING attempts`, digest("captcha-global")).Scan(&attempts); err != nil {
		models.SendError(c, 503, 503, "数据库暂不可用")
		return
	}
	if attempts > 600 {
		models.SendError(c, 429, 429, "验证码获取过于频繁，请稍后再试")
		return
	}
	code, img, err := loginCaptcha()
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		models.SendInternalError(c, err)
		return
	}
	id := hex.EncodeToString(raw)
	if _, err = a.DB.ExecContext(ctx, `WITH expired AS(DELETE FROM standalone_login_captchas WHERE expires_at<NOW()) INSERT INTO standalone_login_captchas(id_hash,answer_hash,expires_at) VALUES($1,$2,NOW()+interval '5 minutes')`, digest(id), digest(id+strings.ToUpper(code))); err != nil {
		models.SendInternalError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	models.Send(c, 200, 0, "success", gin.H{"captchaId": id, "captchaImage": img})
}

// PNG data is generated locally; no external QR/captcha service receives credentials.
func loginCaptcha() (string, string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	code := make([]byte, 4)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", "", err
		}
		code[i] = alphabet[n.Int64()]
	}
	// Guarantee a mix of numbers and letters, without ambiguous 0/O or 1/I.
	n, err := rand.Int(rand.Reader, big.NewInt(8))
	if err != nil {
		return "", "", err
	}
	code[0] = alphabet[n.Int64()]
	n, err = rand.Int(rand.Reader, big.NewInt(int64(len(alphabet)-8)))
	if err != nil {
		return "", "", err
	}
	code[1] = alphabet[8+n.Int64()]
	for i := 3; i > 0; i-- {
		n, err = rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", "", err
		}
		code[i], code[n.Int64()] = code[n.Int64()], code[i]
	}
	img := image.NewRGBA(image.Rect(0, 0, 144, 52))
	for y := 0; y < 52; y++ {
		for x := 0; x < 144; x++ {
			img.Set(x, y, color.RGBA{242, 247, 250, 255})
		}
	}
	for i, ch := range code {
		glyph := image.NewRGBA(image.Rect(0, 0, 9, 16))
		d := font.Drawer{Dst: glyph, Src: image.NewUniform(color.RGBA{35, 70, 105, 255}), Face: basicfont.Face7x13, Dot: fixed.P(1, 13)}
		d.DrawString(string(ch))
		for y := 0; y < 16; y++ {
			for x := 0; x < 9; x++ {
				pixel := glyph.RGBAAt(x, y)
				if pixel.A != 0 {
					for dy := 0; dy < 2; dy++ {
						for dx := 0; dx < 2; dx++ {
							img.Set(13+i*32+x*2+dx, 9+y*2+dy, pixel)
						}
					}
				}
			}
		}
	}
	var buf bytes.Buffer
	if err = png.Encode(&buf, img); err != nil {
		return "", "", err
	}
	return string(code), "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
func (a Auth) issueLoginChallenge(c *gin.Context, id, name, passwordHash string) {
	var required bool
	var enc string
	err := a.DB.QueryRowContext(c.Request.Context(), `SELECT standalone_totp_required OR (SELECT value='true' FROM system_settings WHERE key='standalone_totp_global'),standalone_totp_secret FROM users WHERE id=$1`, id).Scan(&required, &enc)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		models.SendInternalError(c, err)
		return
	}
	challenge := hex.EncodeToString(raw)
	kind, answer := "captcha", ""
	data := gin.H{"challengeId": challenge, "expiresIn": 300}
	if required {
		kind = "totp"
		if enc == "" {
			kind = "setup"
			key, keyErr := totp.Generate(totp.GenerateOpts{Issuer: "TG Attendance", AccountName: name, SecretSize: 20})
			if keyErr != nil {
				models.SendInternalError(c, keyErr)
				return
			}
			enc, err = services.EncryptSecret(key.Secret())
			if err != nil {
				models.SendInternalError(c, err)
				return
			}
			qr, qrErr := key.Image(220, 220)
			if qrErr != nil {
				models.SendInternalError(c, qrErr)
				return
			}
			var buf bytes.Buffer
			if err = png.Encode(&buf, qr); err != nil {
				models.SendInternalError(c, err)
				return
			}
			data["secret"] = key.Secret()
			data["qrImage"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	} else {
		var code, img string
		code, img, err = loginCaptcha()
		if err != nil {
			models.SendInternalError(c, err)
			return
		}
		answer = digest(challenge + strings.ToUpper(code))
		data["captchaImage"] = img
		enc = ""
	}
	_, err = a.DB.ExecContext(c.Request.Context(), `WITH expired AS(DELETE FROM standalone_login_challenges WHERE expires_at<NOW()) INSERT INTO standalone_login_challenges(id_hash,user_id,kind,answer_hash,secret_enc,password_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6,NOW()+interval '5 minutes')`, digest(challenge), id, kind, answer, enc, passwordHash)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	data["verification"] = kind
	c.Header("Cache-Control", "no-store")
	models.Send(c, 200, 0, "verification required", data)
}

// Match a specific 30-second step so the same authenticator code cannot be replayed.
func acceptedTOTPStep(secret, code string, now time.Time, last int64) int64 {
	if len(code) != 6 {
		return -1
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		if step <= last {
			continue
		}
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(step*30, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return step
		}
	}
	return -1
}
func (a Auth) VerifyLogin(c *gin.Context) {
	var in struct {
		ChallengeID string `json:"challengeId"`
		Code        string `json:"code"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.ChallengeID) != 64 || len(in.Code) > 16 {
		models.SendError(c, 400, 400, "验证请求无效")
		return
	}
	ctx := c.Request.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	var global bool
	if err = tx.QueryRowContext(ctx, `SELECT value='true' FROM system_settings WHERE key='standalone_totp_global' FOR SHARE`).Scan(&global); err != nil {
		models.SendInternalError(c, err)
		return
	}
	var id, kind, answer, enc, savedHash string
	var attempts int
	err = tx.QueryRowContext(ctx, `UPDATE standalone_login_challenges SET attempts=attempts+1 WHERE id_hash=$1 AND expires_at>NOW() AND attempts<5 RETURNING user_id,kind,answer_hash,secret_enc,password_hash,attempts`, digest(in.ChallengeID)).Scan(&id, &kind, &answer, &enc, &savedHash, &attempts)
	if err != nil {
		models.SendError(c, 400, 400, "验证已过期或尝试次数过多，请重新登录")
		return
	}
	var name, passwordHash, currentSecret, role string
	var status int
	var individual bool
	var lastStep int64
	err = tx.QueryRowContext(ctx, `SELECT username,password_hash,status,standalone_totp_required,standalone_totp_secret,standalone_totp_step,CASE WHEN EXISTS(SELECT 1 FROM standalone_subaccounts WHERE user_id=users.id) THEN 'member' ELSE 'admin' END FROM users WHERE id=$1 AND (EXISTS(SELECT 1 FROM standalone_subaccounts WHERE user_id=users.id) OR EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=users.id AND r.name IN ('管理员','admin'))) FOR UPDATE`, id).Scan(&name, &passwordHash, &status, &individual, &currentSecret, &lastStep, &role)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	valid := status == 1 && passwordHash == savedHash
	required := global || individual
	valid = valid && ((kind == "captcha" && !required) || (kind == "totp" && required && enc == currentSecret && enc != "") || (kind == "setup" && required && currentSecret == ""))
	step := int64(-1)
	if kind == "captcha" {
		valid = valid && subtle.ConstantTimeCompare([]byte(answer), []byte(digest(in.ChallengeID+strings.ToUpper(strings.TrimSpace(in.Code))))) == 1
	} else {
		secret, decryptErr := services.DecryptSecret(enc)
		if decryptErr != nil {
			valid = false
		} else {
			step = acceptedTOTPStep(secret, strings.TrimSpace(in.Code), time.Now(), lastStep)
			valid = valid && step >= 0
		}
	}
	if !valid {
		if err = tx.Commit(); err != nil {
			models.SendInternalError(c, err)
			return
		}
		models.SendError(c, 400, 400, "验证码错误、已使用或登录设置已变化，请重试或重新登录")
		return
	}
	if kind != "captcha" {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET standalone_totp_secret=$2,standalone_totp_step=$3 WHERE id=$1`, id, enc, step); err != nil {
			models.SendInternalError(c, err)
			return
		}
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		models.SendInternalError(c, err)
		return
	}
	session := hex.EncodeToString(token)
	if _, err = tx.ExecContext(ctx, `WITH session AS(INSERT INTO standalone_sessions(token_hash,user_id,expires_at,last_seen_at) VALUES($1,$2,NOW()+interval '7 days',NOW())) UPDATE users SET last_login_at=NOW() WHERE id=$2`, digest(session), id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM standalone_login_challenges WHERE id_hash=$1`, digest(in.ChallengeID)); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	a.DB.ExecContext(ctx, `DELETE FROM standalone_login_attempts WHERE bucket=$1`, digest("user:"+strings.ToLower(name)))
	a.cookie(c, session, sessionMaxAgeSeconds)
	models.Send(c, 200, 0, "success", a.identity(c, id, name, role))
}
func (a Auth) LoginPolicy(c *gin.Context) {
	if c.Request.Method == "GET" {
		var enabled bool
		if err := a.DB.QueryRowContext(c.Request.Context(), `SELECT value='true' FROM system_settings WHERE key='standalone_totp_global'`).Scan(&enabled); err != nil {
			models.SendInternalError(c, err)
			return
		}
		models.Send(c, 200, 0, "success", gin.H{"global": enabled})
		return
	}
	var in struct {
		Enabled  bool   `json:"enabled"`
		Password string `json:"password"`
		Reset    bool   `json:"reset"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.Password) > 72 {
		models.SendError(c, 400, 400, "请求无效")
		return
	}
	// Resetting one account's Google binding needs no password (it only clears the binding and
	// keeps the on/off setting as it is). Switching verification on/off still needs the password.
	resetOnly := in.Reset && c.Param("id") != ""
	if !resetOnly {
		var hash string
		if err := a.DB.QueryRowContext(c.Request.Context(), `SELECT password_hash FROM users WHERE id=$1`, c.GetString("userID")).Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
			models.SendError(c, 403, 403, "当前登录密码不正确")
			return
		}
	}
	ctx := c.Request.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	var value string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key='standalone_totp_global' FOR UPDATE`).Scan(&value); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if c.Param("id") == "" {
		if _, err = tx.ExecContext(ctx, `UPDATE system_settings SET value=$1,updated_at=NOW() WHERE key='standalone_totp_global'`, strconv.FormatBool(in.Enabled)); err != nil {
			models.SendInternalError(c, err)
			return
		}
		token, _ := c.Cookie("tg_admin")
		_, err = tx.ExecContext(ctx, `DELETE FROM standalone_sessions WHERE token_hash!=$1`, digest(token))
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM standalone_login_challenges`)
		}
	} else {
		id, parseErr := uuid.Parse(c.Param("id"))
		if parseErr != nil {
			models.SendError(c, 400, 400, "账号无效")
			return
		}
		if c.GetString("standaloneRole") != "admin" && !hasGrant(c, "data:all") {
			models.SendError(c, 403, 403, "管理登录验证需要全员数据权限")
			return
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE users SET standalone_totp_required=CASE WHEN $4 THEN standalone_totp_required ELSE $2 END,standalone_totp_secret=CASE WHEN $3 THEN '' ELSE standalone_totp_secret END,standalone_totp_step=CASE WHEN $3 THEN -1 ELSE standalone_totp_step END WHERE id=$1 AND EXISTS(SELECT 1 FROM standalone_subaccounts WHERE user_id=$1)`, id, in.Enabled, in.Reset, resetOnly)
		if updateErr != nil {
			models.SendInternalError(c, updateErr)
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			models.SendError(c, 404, 404, "子账号不存在")
			return
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM standalone_sessions WHERE user_id=$1`, id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM standalone_login_challenges WHERE user_id=$1`, id)
		}
	}
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", nil)
}

func (a Auth) Require() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie("tg_admin")
		if token == "" {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "message": "请先登录"})
			return
		}
		var id, name, role string
		err := a.DB.QueryRowContext(c.Request.Context(), `SELECT u.id,u.username,CASE WHEN EXISTS(SELECT 1 FROM standalone_subaccounts sa WHERE sa.user_id=u.id) THEN 'member' ELSE 'admin' END FROM standalone_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>NOW() AND u.status=1 AND (EXISTS(SELECT 1 FROM standalone_subaccounts sa WHERE sa.user_id=u.id) OR EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.name IN ('管理员','admin')))`, digest(token)).Scan(&id, &name, &role)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "message": "登录已过期"})
			return
		}
		c.Set("userID", id)
		c.Set("username", name)
		c.Set("role", role)
		c.Set("standaloneRole", role)
		perms, permissionErr := a.permissions(c.Request.Context(), id)
		if permissionErr != nil {
			models.SendError(c, 503, 503, "无法验证角色权限")
			c.Abort()
			return
		}
		c.Set("permissions", perms)
		headerPerms := perms
		if role == "admin" {
			headerPerms = []string{"*"}
		}
		encoded, _ := json.Marshal(headerPerms)
		c.Header("X-TG-Permissions", string(encoded))
		c.Next()
	}
}
func (a Auth) Me(c *gin.Context) {
	token, _ := c.Cookie("tg_admin")
	if _, err := a.DB.ExecContext(c.Request.Context(), `UPDATE standalone_sessions SET last_seen_at=NOW() WHERE token_hash=$1 AND (last_seen_at IS NULL OR last_seen_at<NOW()-interval '20 seconds')`, digest(token)); err != nil {
		models.SendInternalError(c, err)
		return
	}

	models.Send(c, 200, 0, "success", a.identity(c, c.GetString("userID"), c.GetString("username"), c.GetString("standaloneRole")))
}
func (a Auth) Logout(c *gin.Context) {
	s, _ := c.Cookie("tg_admin")
	a.DB.ExecContext(c.Request.Context(), "DELETE FROM standalone_sessions WHERE token_hash=$1", digest(s))
	a.cookie(c, "", -1)
	models.Send(c, 200, 0, "success", nil)
}
func (a Auth) permissions(ctx context.Context, id string) ([]string, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT DISTINCT jsonb_array_elements_text(r.permissions) FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return normalizeGrants(out), rows.Err()
}
func (a Auth) identity(c *gin.Context, id, name, role string) gin.H {
	perms, err := a.permissions(c.Request.Context(), id)
	if err != nil {
		perms = []string{}
	}
	if role == "admin" {
		perms = []string{"*"}
	}
	return gin.H{"id": id, "username": name, "role": role, "permissions": perms}
}

// ---- Source: backend/internal/standalone/bot.go ----
func (i *Inbox) RegisterWebhook(c *gin.Context) {
	opts, e := i.TG.BuildTelegramBotOptionsContext(c.Request.Context(), os.Getenv("PORT"))
	if e == nil {
		e = tgbot.RegisterDurableWebhook(opts)
	}
	if e != nil {
		models.SendError(c, 400, 400, "Webhook 注册失败，请确认已保存 Token、启用 Bot、配置 HTTPS 域名及网络连接。")
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"webhookUrl": opts.WebhookURL})
}

// ---- Source: backend/internal/standalone/exports.go ----
// Clamp pages after retention cleanup so the last page cannot become stranded.
func listPage(c *gin.Context, db *sql.DB, countSQL string, args ...interface{}) (page, size, total int, err error) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	err = db.QueryRowContext(c.Request.Context(), countSQL, args...).Scan(&total)
	last := (total + size - 1) / size
	if last < 1 {
		last = 1
	}
	if page > last {
		page = last
	}
	return
}

func (i *Inbox) Exports(c *gin.Context) {
	page, size, total, e := listPage(c, i.DB, `SELECT COUNT(*) FROM standalone_exports`)
	if e != nil {
		models.SendInternalError(c, e)
		return
	}

	rows, e := i.DB.QueryContext(c.Request.Context(), "SELECT filename,octet_length(content),created_at FROM standalone_exports ORDER BY created_at DESC,filename DESC LIMIT $1 OFFSET $2", size, (page-1)*size)
	if e != nil {
		models.SendError(c, 503, 503, "备份暂不可用")
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var name string
		var size int
		var at time.Time
		if rows.Scan(&name, &size, &at) == nil {
			items = append(items, gin.H{"filename": name, "size": size, "createdAt": at})
		}
	}
	models.Send(c, 200, 0, "success", gin.H{"items": items, "page": page, "pageSize": size, "total": total})
}
func (i *Inbox) DownloadExport(c *gin.Context) {
	name := c.Param("filename")
	if name != filepath.Base(name) {
		c.Status(400)
		return
	}
	var body []byte
	e := i.DB.QueryRowContext(c.Request.Context(), "SELECT content FROM standalone_exports WHERE filename=$1", name).Scan(&body)
	if e == sql.ErrNoRows {
		c.Status(404)
		return
	}
	if e != nil {
		c.Status(503)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="attendance.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", body)
}

// ---- Source: backend/internal/standalone/inbox.go ----
type Inbox struct {
	DB                *sql.DB
	TG                *services.WorkforceTelegramService
	Wake              chan struct{}
	maintenanceMu     sync.Mutex
	maintenanceCancel context.CancelFunc
}

func (i *Inbox) Notify() {
	i.maintenanceMu.Lock()
	defer i.maintenanceMu.Unlock()
	if i.maintenanceCancel != nil {
		i.maintenanceCancel()
	}
	select {
	case i.Wake <- struct{}{}:
	default:
	}
}

// Keep maintenance serialized with punches, but yield database/network waits as
// soon as a durably accepted update arrives. The mutex closes the wake/start race.
func (i *Inbox) maintenanceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	i.maintenanceMu.Lock()
	defer i.maintenanceMu.Unlock()
	maintenance, cancel := services.StandaloneMaintenanceContext(ctx)
	i.maintenanceCancel = cancel
	if len(i.Wake) > 0 {
		cancel()
	}
	return maintenance, func() {
		i.maintenanceMu.Lock()
		defer i.maintenanceMu.Unlock()
		cancel()
		i.maintenanceCancel = nil
	}
}

func (i *Inbox) Accept(c *gin.Context) {
	secret, err := i.TG.GetWebhookSecret(c.Request.Context())
	if err != nil || secret == "" {
		c.Status(503)
		return
	}
	if subtle.ConstantTimeCompare([]byte(secret), []byte(c.GetHeader("X-Telegram-Bot-Api-Secret-Token"))) != 1 {
		c.Status(403)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		c.Status(413)
		return
	}
	var u struct {
		ID *int64 `json:"update_id"`
	}
	if json.Unmarshal(body, &u) != nil || u.ID == nil || *u.ID < 0 {
		c.Status(400)
		return
	}
	if _, err = i.DB.ExecContext(c.Request.Context(), `INSERT INTO standalone_inbox(update_id,body) VALUES($1,$2) ON CONFLICT DO NOTHING`, *u.ID, string(body)); err != nil {
		c.Status(503)
		return
	}
	// Acknowledge only after durable storage. Telegram retries safely on a database failure.
	i.Notify()
	c.Status(200)
}
func (i *Inbox) Run(ctx context.Context) {
	// One worker per database. Session advisory lock prevents duplicate processing on deploy overlap.
	conn, err := i.DB.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		var locked bool
		if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(84392702)").Scan(&locked); err != nil {
			return
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(84392702)")
	// Legacy business operations aren't one transaction with external Telegram sends.
	// Never silently replay a partially processed event and risk duplicate fines/exports.
	i.DB.ExecContext(ctx, `UPDATE standalone_inbox SET status='uncertain',note='进程在处理期间退出；请核对考勤后处理，系统不会自动重放' WHERE status='processing'`)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	lastMaintenance, lastFull, lastWarm := time.Time{}, time.Time{}, time.Time{}
	i.Notify() // Drain persisted updates immediately on startup.
	for {
		select {
		case <-ctx.Done():
			return
		case <-i.Wake:
		case <-ticker.C:
		}
		for ctx.Err() == nil {
			opts, e := i.TG.BuildTelegramBotOptionsContext(ctx, os.Getenv("PORT"))
			if e != nil || opts.BotToken == "" {
				break
			}
			var id int64
			var body []byte
			var received time.Time
			e = i.DB.QueryRowContext(ctx, `UPDATE standalone_inbox SET status='processing',started_at=NOW() WHERE update_id=(SELECT update_id FROM standalone_inbox WHERE status='pending' ORDER BY update_id LIMIT 1) RETURNING update_id,body,received_at`).Scan(&id, &body, &received)
			if e == sql.ErrNoRows {
				// Idle: keep the Telegram connection warm (the shared transport drops it after 90s).
				if time.Since(lastWarm) >= 45*time.Second {
					lastWarm = time.Now()
					go tgbot.WarmTelegramConnection(opts.BotToken)
					go warmDatabasePool(ctx, i.DB)
				}
				break
			}
			if e != nil {
				log.Print("Inbox database unavailable")
				break
			}
			started := time.Now()
			dbBefore := i.DB.Stats()
			opts.Context = ctx
			err = i.process(opts, body, received)
			dbAfter := i.DB.Stats()
			log.Printf("[inbox] update=%d queue_ms=%d processing_ms=%d db_wait_ms=%d db_idle_closed=%d db_open=%d", id, started.Sub(received).Milliseconds(), time.Since(started).Milliseconds(),
				(dbAfter.WaitDuration - dbBefore.WaitDuration).Milliseconds(), dbAfter.MaxIdleClosed-dbBefore.MaxIdleClosed, dbAfter.OpenConnections)
			status, note := "done", ""
			if err != nil {
				status = "uncertain"
				note = "处理异常，请核对业务记录后确认"
				log.Printf("Inbox update %d needs review: %v", id, err)
			}
			if _, e = i.DB.ExecContext(ctx, `UPDATE standalone_inbox SET status=$2,note=$3,finished_at=NOW(),body=CASE WHEN $2='done' THEN '{}'::jsonb ELSE body END WHERE update_id=$1`, id, status, note); e != nil {
				log.Print("Inbox completion could not be persisted; stopping to avoid unsafe replay")
				return
			}
		}
		if time.Since(lastMaintenance) >= 15*time.Second && ctx.Err() == nil {
			var pending int
			if e := i.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM standalone_inbox WHERE status IN ('pending','processing')").Scan(&pending); e == nil && pending == 0 {
				full := time.Since(lastFull) >= time.Hour
				maintenance, cancel := i.maintenanceContext(ctx)
				i.TG.StandaloneMaintenance(maintenance, full)
				completed := maintenance.Err() == nil
				cancel()
				lastMaintenance = time.Now()
				if full && completed {
					lastFull = time.Now()
				}
			}
		}
	}
}
func (i *Inbox) process(opts tgbot.Options, body []byte, received time.Time) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("handler panic")
		}
	}()
	parent := opts.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	opts.Context = ctx
	notices, handled, e := tgbot.MemberJoinNotices(body)
	if e != nil {
		return e
	}
	if handled {
		if i.TG != nil {
			active, err := i.TG.IsActive(ctx)
			if err != nil {
				return err
			}
			if !active {
				return nil
			}
		}
		for _, notice := range notices {
			if notice.EventUnix <= 0 {
				return fmt.Errorf("join event missing timestamp")
			}
			// Telegram may deliver the same join through two different update types.
			// A three-second window suppresses the duplicate across process restarts.
			res, e := i.DB.ExecContext(ctx, `INSERT INTO standalone_member_notices(chat_id,user_id,event_unix) VALUES($1,$2,$3)
 ON CONFLICT(chat_id,user_id) DO UPDATE SET event_unix=EXCLUDED.event_unix,created_at=NOW()
 WHERE standalone_member_notices.event_unix < EXCLUDED.event_unix-3`, notice.ChatID, notice.UserID, notice.EventUnix)
			if e != nil {
				return e
			}
			count, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if count == 0 {
				continue
			}
			if e = tgbot.SendMemberJoinNotice(opts, notice); e != nil {
				return e
			}
		}
		return nil
	}
	return tgbot.ProcessDurableUpdate(opts, body, received)
}
func (i *Inbox) Status(c *gin.Context) {
	page, size, total, e := listPage(c, i.DB, `SELECT COUNT(*) FROM standalone_inbox`)
	if e != nil {
		models.SendInternalError(c, e)
		return
	}

	rows, e := i.DB.QueryContext(c.Request.Context(), `SELECT update_id,status,received_at,note FROM standalone_inbox ORDER BY received_at DESC,update_id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if e != nil {
		models.SendError(c, 503, 503, "数据库暂不可用")
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id int64
		var status, note string
		var at time.Time
		if rows.Scan(&id, &status, &at, &note) == nil {
			items = append(items, gin.H{"updateId": id, "status": status, "receivedAt": at, "note": note})
		}
	}
	if err := rows.Err(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	rows.Close()
	stats, err := attendanceSummary(c, i.DB, time.Now().Format("2006-01-02"))
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"page": page, "pageSize": size, "total": total, "attendance": stats, "items": items, "database": func() string {
		if os.Getenv("DATABASE_TLS_MODE") == "internal" {
			return "服务器本地 PostgreSQL"
		}
		return "外部 PostgreSQL"
	}(), "localDatabase": os.Getenv("DATABASE_TLS_MODE") == "internal", "maxConnections": i.DB.Stats().MaxOpenConnections, "sleepNotice": "免费 Render 休眠期间不保证定时提醒准点；首次打卡需要唤醒。"})
}

// Missing punches are not automatically classified as absence: use the recorded daily status.
func attendanceSummary(c *gin.Context, db *sql.DB, date string) (gin.H, error) {
	scope := ""
	if c.GetString("standaloneRole") != "admin" && !hasGrant(c, "data:all") {
		scope = c.GetString("userID")
		if scope == "" {
			return nil, fmt.Errorf("missing account scope")
		}
	}

	lists := gin.H{"late": []gin.H{}, "early": []gin.H{}, "leave": []gin.H{}, "absent": []gin.H{}}
	rows, err := db.QueryContext(c.Request.Context(), `SELECT DISTINCT e.id,e.name,COALESCE(e.employee_id,''),ar.status,
 EXISTS(SELECT 1 FROM work_sessions w WHERE w.employee_id=e.id AND w.attendance_date=ar.date AND w.late_minutes>0),
 EXISTS(SELECT 1 FROM penalty_records p WHERE p.employee_id=e.id AND p.penalty_date=ar.date AND (p.category='早退' OR p.reason LIKE '%早退%')),
 EXISTS(SELECT 1 FROM wf_tg_attendance_review_tasks t WHERE t.employee_id=e.id AND t.attendance_date=ar.date AND t.trigger_type='early_checkout_review' AND t.status='pending')
 FROM employees e JOIN attendance_records ar ON ar.employee_id=e.id AND ar.date=$1::date WHERE e.status=1 AND ($2='' OR e.user_id::text=$2) ORDER BY e.name,e.id`, date, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	present := 0
	for rows.Next() {
		var id, name, code, status string
		var late, early, pending bool
		if err = rows.Scan(&id, &name, &code, &status, &late, &early, &pending); err != nil {
			return nil, err
		}
		person := gin.H{"id": id, "name": name, "employeeCode": code, "pending": pending}
		if status == "work" || status == "working" {
			present++
			if late {
				lists["late"] = append(lists["late"].([]gin.H), person)
			}
		}
		if early || pending {
			lists["early"] = append(lists["early"].([]gin.H), person)
		}
		if status == "leave" || status == "rest_full" || status == "rest_half" {
			lists["leave"] = append(lists["leave"].([]gin.H), person)
		}
		if status == "absent" {
			lists["absent"] = append(lists["absent"].([]gin.H), person)
		}
	}
	return gin.H{"date": date, "present": present, "late": len(lists["late"].([]gin.H)), "leave": len(lists["leave"].([]gin.H)), "absent": len(lists["absent"].([]gin.H)), "people": lists, "selfOnly": scope != ""}, rows.Err()

}

func (i *Inbox) Resolve(c *gin.Context) {
	var in struct {
		Note string `json:"note"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.Note) < 4 || len(in.Note) > 500 {
		models.SendError(c, 400, 400, "请填写核对结果（4至500字）")
		return
	}
	r, e := i.DB.ExecContext(c.Request.Context(), `UPDATE standalone_inbox SET status='resolved',note=$2,finished_at=NOW(),body='{}'::jsonb WHERE update_id=$1 AND status='uncertain'`, c.Param("id"), in.Note)
	if e != nil {
		models.SendError(c, 500, 500, "更新失败")
		return
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		models.SendError(c, 409, 409, "该消息不在待核对状态")
		return
	}
	models.Send(c, 200, 0, "success", nil)
}

// RBAC reuses roles and user_roles; protected system roles cannot be assigned or changed here.
var permissionModules = []struct {
	Key, Label string
	Actions    []string
}{
	{"overview", "首页概览", []string{"view", "approve"}},
	{"accounts", "子账号", []string{"view", "edit", "reset_password", "reset", "security"}},
	{"roles", "角色管理", []string{"view", "edit", "delete", "assign"}},
	{"attendance", "考勤月历", []string{"view", "edit", "delete", "export"}},
	{"records", "打卡记录", []string{"view", "export"}},
	{"punch", "后台打卡", []string{"view", "edit"}},
	{"telegram", "打卡规则", []string{"view", "edit", "delete", "reset", "notify"}},
	{"attendance_review", "审核审批", []string{"view", "edit", "review", "approve"}},
	{"penalty", "罚款详情", []string{"view", "edit", "delete", "export", "notify"}},
	{"performance", "员工绩效", []string{"view", "edit", "delete", "export", "notify"}},
	{"maintenance", "清理内存", []string{"view", "edit", "clean"}},
	{"data", "数据范围", []string{"all"}},
}

func normalizeGrants(in []string) []string {
	set := map[string]bool{}
	for _, p := range in {
		set[p] = true
		if strings.HasSuffix(p, ":edit") {
			set[strings.TrimSuffix(p, ":edit")+":view"] = true
		}
	}
	out := []string{}
	for _, m := range permissionModules {
		for _, action := range m.Actions {
			p := m.Key + ":" + action
			if set[p] {
				out = append(out, p)
			}
		}
	}
	return out
}
func systemRole(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "admin", "administrator", "superadmin", "管理员", "超级管理员":
		return true
	}
	return false
}

// 「查看员」是子账号默认角色，新增子账号依赖它，不可删除或重命名。
func isViewerRole(name string) bool { return strings.TrimSpace(name) == "查看员" }
func (a Auth) Roles(c *gin.Context) {
	rows, err := a.DB.QueryContext(c.Request.Context(), `SELECT r.id,r.name,r.permissions,COUNT(ur.user_id) FROM roles r LEFT JOIN user_roles ur ON ur.role_id=r.id GROUP BY r.id ORDER BY r.name`)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, name string
		var raw []byte
		var count int
		if err = rows.Scan(&id, &name, &raw, &count); err != nil {
			models.SendInternalError(c, err)
			return
		}
		var perms []string
		if json.Unmarshal(raw, &perms) != nil {
			models.SendError(c, 500, 500, "角色权限数据异常")
			return
		}
		items = append(items, gin.H{"id": id, "name": name, "permissions": normalizeGrants(perms), "protected": systemRole(name), "userCount": count})
	}
	catalog := []gin.H{}
	for _, m := range permissionModules {
		catalog = append(catalog, gin.H{"key": m.Key, "label": m.Label, "actions": m.Actions})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": items, "catalog": catalog})
}
func (a Auth) SaveRole(c *gin.Context) {
	var in struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if c.ShouldBindJSON(&in) != nil || len(strings.TrimSpace(in.Name)) == 0 || len(in.Name) > 100 || systemRole(in.Name) {
		models.SendError(c, 400, 400, "角色名称无效或为系统保留名称")
		return
	}
	valid := map[string]bool{}
	for _, m := range permissionModules {
		for _, action := range m.Actions {
			valid[m.Key+":"+action] = true
		}
	}
	for _, p := range in.Permissions {
		if !valid[p] || !hasGrant(c, p) {
			models.SendError(c, 403, 403, "不能授予未知权限或超出自身权限的授权")
			return
		}
	}
	perms := normalizeGrants(in.Permissions)
	raw, _ := json.Marshal(perms)
	tx, err := a.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	id := c.Param("id")
	if id != "" {
		var name string
		if err = tx.QueryRowContext(c.Request.Context(), `SELECT name FROM roles WHERE id=$1 FOR UPDATE`, id).Scan(&name); err != nil {
			models.SendError(c, 404, 404, "角色不存在")
			return
		}
		if systemRole(name) {
			models.SendError(c, 403, 403, "系统管理员角色不可修改")
			return
		}
		if isViewerRole(name) && strings.TrimSpace(in.Name) != name {
			models.SendError(c, 403, 403, "默认角色「查看员」不可重命名")
			return
		}
		// A delegated editor cannot modify roles that already exceed their own grants.
		var old []byte
		_ = tx.QueryRowContext(c.Request.Context(), `SELECT permissions FROM roles WHERE id=$1`, id).Scan(&old)
		var previous []string
		_ = json.Unmarshal(old, &previous)
		for _, p := range previous {
			if !hasGrant(c, p) {
				models.SendError(c, 403, 403, "不能修改权限高于自己的角色")
				return
			}
		}
		_, err = tx.ExecContext(c.Request.Context(), `UPDATE roles SET name=$2,permissions=$3::jsonb WHERE id=$1`, id, strings.TrimSpace(in.Name), string(raw))
	} else {
		id = uuid.NewString()
		_, err = tx.ExecContext(c.Request.Context(), `INSERT INTO roles(id,name,permissions) VALUES($1,$2,$3::jsonb)`, id, strings.TrimSpace(in.Name), string(raw))
	}
	if err != nil {
		models.SendError(c, 409, 409, "角色名称重复或保存失败")
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"id": id})
}
func (a Auth) DeleteRole(c *gin.Context) {
	tx, err := a.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	var name string
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT name FROM roles WHERE id=$1 FOR UPDATE`, c.Param("id")).Scan(&name); err != nil {
		models.SendError(c, 404, 404, "角色不存在")
		return
	}
	if systemRole(name) || isViewerRole(name) {
		models.SendError(c, 403, 403, "系统角色不可删除")
		return
	}
	var used int
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT COUNT(*) FROM user_roles WHERE role_id=$1`, c.Param("id")).Scan(&used); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if used > 0 {
		models.SendError(c, 409, 409, "请先解除账号的角色分配，再删除角色")
		return
	}
	if _, err = tx.ExecContext(c.Request.Context(), `DELETE FROM roles WHERE id=$1`, c.Param("id")); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", nil)
}
func (a Auth) AssignRoles(c *gin.Context) {
	var in struct {
		RoleIDs []string `json:"roleIds"`
	}
	if c.ShouldBindJSON(&in) != nil {
		models.SendError(c, 400, 400, "角色参数无效")
		return
	}
	tx, err := a.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	id := c.Param("id")
	var found string
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT user_id FROM standalone_subaccounts WHERE user_id=$1 FOR UPDATE`, id).Scan(&found); err != nil {
		models.SendError(c, 404, 404, "子账号不存在")
		return
	}
	if c.GetString("standaloneRole") != "admin" {
		grants, e := a.permissions(c.Request.Context(), id)
		if e != nil {
			models.SendInternalError(c, e)
			return
		}
		for _, p := range grants {
			if !hasGrant(c, p) {
				models.SendError(c, 403, 403, "不能管理权限高于自己的账号")
				return
			}
		}
	}
	for _, roleID := range in.RoleIDs {
		var name string
		var raw []byte
		if err = tx.QueryRowContext(c.Request.Context(), `SELECT name,permissions FROM roles WHERE id=$1 FOR SHARE`, roleID).Scan(&name, &raw); err != nil || systemRole(name) {
			models.SendError(c, 400, 400, "不能分配该角色")
			return
		}
		var perms []string
		if json.Unmarshal(raw, &perms) != nil {
			models.SendError(c, 400, 400, "角色权限无效")
			return
		}
		for _, p := range perms {
			if !hasGrant(c, p) {
				models.SendError(c, 403, 403, "不能分配超出自身权限的角色")
				return
			}
		}
	}
	if _, err = tx.ExecContext(c.Request.Context(), `DELETE FROM user_roles WHERE user_id=$1`, id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	for _, roleID := range in.RoleIDs {
		if _, err = tx.ExecContext(c.Request.Context(), `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, roleID); err != nil {
			models.SendInternalError(c, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", nil)
}
func (a Auth) ChangePassword(c *gin.Context) {
	var in struct {
		Current  string `json:"currentPassword"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&in) != nil || len(in.Password) < 6 || len(in.Password) > 72 {
		models.SendError(c, 400, 400, "新密码需要6–72字节")
		return
	}
	tx, err := a.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	defer tx.Rollback()
	id := c.GetString("userID")
	var hash string
	if err = tx.QueryRowContext(c.Request.Context(), `SELECT password_hash FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&hash); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Current)) != nil {
		models.SendError(c, 400, 400, "当前密码不正确")
		return
	}
	next, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	if _, err = tx.ExecContext(c.Request.Context(), `UPDATE users SET password_hash=$2,updated_at=NOW() WHERE id=$1`, id, string(next)); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if _, err = tx.ExecContext(c.Request.Context(), `DELETE FROM standalone_sessions WHERE user_id=$1`, id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	if err = tx.Commit(); err != nil {
		models.SendInternalError(c, err)
		return
	}
	a.cookie(c, "", -1)
	models.Send(c, 200, 0, "success", nil)
}
func (a Auth) ResetPassword(c *gin.Context) {
	// Reuse account validation/transaction; the dedicated route has its own grant.
	a.UpdateAccount(c)
}
func (a Auth) AuthorizeExport(c *gin.Context) {
	var in struct {
		Module string `json:"module"`
	}
	if c.ShouldBindJSON(&in) != nil || (in.Module != "attendance" && in.Module != "penalty" && in.Module != "performance") || !hasGrant(c, in.Module+":view") || !hasGrant(c, in.Module+":export") {
		models.SendError(c, 403, 403, "没有导出权限")
		return
	}
	models.Send(c, 200, 0, "success", nil)
}

func (a Auth) Memory(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 80*time.Second)
	defer cancel()
	if c.Request.Method == "PUT" {
		var policy services.ResourcePolicy
		if c.ShouldBindJSON(&policy) != nil {
			models.SendError(c, 400, 400, "清理策略格式错误")
			return
		}
		if err := services.SaveResourcePolicy(ctx, a.DB, policy); err != nil {
			models.SendError(c, 400, 400, err.Error())
			return
		}
	}
	if c.Request.Method == "POST" {
		if _, err := services.CleanResources(ctx, a.DB, "manual"); err != nil {
			models.SendError(c, 409, 409, err.Error())
			return
		}
	}
	policy, err := services.LoadResourcePolicy(ctx, a.DB)
	if err != nil {
		models.SendError(c, 500, 500, "读取清理策略失败")
		return
	}
	page, size, total, err := listPage(c, a.DB, `SELECT COUNT(*) FROM operation_logs WHERE operation_module='RESOURCE_MAINTENANCE'`)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	history, err := services.ResourceHistory(ctx, a.DB, size, (page-1)*size)
	if err != nil {
		models.SendError(c, 500, 500, "读取清理日志失败")
		return
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	models.Send(c, 200, 0, "success", gin.H{"heapBytes": stats.HeapAlloc, "systemBytes": stats.Sys, "policy": policy, "snapshot": services.InspectResources(ctx, a.DB, policy), "history": history, "page": page, "pageSize": size, "total": total})
}

// Scope mutation bodies as well as query strings; arbitrary client employee IDs
// must never bypass a member's data boundary.
func (a Auth) scopeOwnMutation(c *gin.Context, emp string) bool {
	path := c.FullPath()
	if c.Request.Method == "DELETE" {
		if strings.Contains(path, "/penalty/records/:id") {
			var owner string
			return a.DB.QueryRowContext(c.Request.Context(), `SELECT employee_id FROM penalty_records WHERE id=$1`, c.Param("id")).Scan(&owner) == nil && owner == emp
		}
		return c.Param("employeeId") == emp || (strings.Contains(path, "/employees/:id") && c.Param("id") == emp)
	}
	if strings.Contains(path, "/import") || strings.Contains(path, "/employee-accounts") || path == "/api/v1/admin/employees" || strings.Contains(path, "/attendance-review/tasks") {
		return false
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	body := map[string]interface{}{}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	ownID := func(m map[string]interface{}) bool {
		v, _ := m["employee_id"].(string)
		if v == "" {
			v, _ = m["employeeId"].(string)
		}
		return v == emp
	}
	if path == "/api/v1/admin/attendance/records" {
		for _, key := range []string{"data", "clears"} {
			if raw, exists := body[key]; exists {
				items, ok := raw.(map[string]interface{})
				if !ok {
					return false
				}
				for id := range items {
					if id != emp {
						return false
					}
				}
			}
		}
		if raw, exists := body["restoreEmployees"]; exists {
			ids, ok := raw.([]interface{})
			if !ok {
				return false
			}
			for _, id := range ids {
				if id != emp {
					return false
				}
			}
		}
		return true
	}
	if path == "/api/v1/admin/performance/batch" {
		items, ok := body["items"].([]interface{})
		if !ok {
			return false
		}
		for _, item := range items {
			m, ok := item.(map[string]interface{})
			if !ok || !ownID(m) {
				return false
			}
		}
		return true
	}
	if strings.Contains(path, "/penalty/records/:id") {
		var owner string
		return a.DB.QueryRowContext(c.Request.Context(), `SELECT employee_id FROM penalty_records WHERE id=$1`, c.Param("id")).Scan(&owner) == nil && owner == emp
	}
	if path == "/api/v1/admin/penalty/record" {
		return ownID(body)
	}
	if strings.Contains(path, "/performance/:employeeId") {
		return c.Param("employeeId") == emp && ownID(body)
	}
	if strings.Contains(path, "/employees/:id") {
		return c.Param("id") == emp
	}
	return false
}


// warmDatabasePool keeps a few pooled connections alive while idle. Connections
// idle for more than the pool's idle limit (or dropped by the database) cost a
// fresh TCP+TLS+auth handshake, several round trips, on the next punch.
// Best effort: bounded, never holds more than three connections, and skips
// quickly when the pool is busy so it cannot delay a real punch.
func warmDatabasePool(ctx context.Context, db *sql.DB) {
	n := db.Stats().MaxOpenConnections - 2 // one connection holds the inbox lock; keep one free
	if n > 3 {
		n = 3
	}
	if n < 1 {
		return
	}
	var wg sync.WaitGroup
	for k := 0; k < n; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			conn, err := db.Conn(c)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.PingContext(c)
			time.Sleep(150 * time.Millisecond) // hold briefly so the n pings use n distinct connections
		}()
	}
	wg.Wait()
}
