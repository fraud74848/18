// Consolidated source; original file boundaries are marked below.
package services

import (
	bytesPackage2 "bytes"
	context "context"
	aes "crypto/aes"
	cipher "crypto/cipher"
	rand "crypto/rand"
	sha256 "crypto/sha256"
	tls "crypto/tls"
	sql "database/sql"
	base64 "encoding/base64"
	hex "encoding/hex"
	json "encoding/json"
	errorsPackage2 "errors"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
	uuid "github.com/google/uuid"
	websocket "github.com/gorilla/websocket"
	minioPackage2 "github.com/minio/minio-go/v7"
	redisPackage2 "github.com/redis/go-redis/v9"
	bcrypt "golang.org/x/crypto/bcrypt"
	_ "golang.org/x/image/webp"
	html "html"
	_ "image/gif"
	io "io"
	logPackage2 "log"
	net "net"
	httpPackage2 "net/http"
	smtp "net/smtp"
	urlPackage2 "net/url"
	os "os"
	filepath "path/filepath"
	regexp "regexp"
	strconv "strconv"
	strings "strings"
	sync "sync"
	atomic "sync/atomic"
	configPackage2 "tg-attendance/backend/config"
	models "tg-attendance/backend/models"
	time "time"
)

// ---- Source: backend/services/activity_repository.go ----
// maxCacheSize 缓存最大条目数
const maxCacheSize = 10000

// ActivityRepository 活动日志仓库
type ActivityRepository struct {
	db         *sql.DB
	mu         sync.RWMutex
	activities map[string]*models.ActivityLog
	// cacheKeys 用于 LRU 淘汰（简单 FIFO）
	cacheKeys []string
}

// addToCache 添加条目到缓存，并处理淘汰
func (r *ActivityRepository) addToCache(id string, log *models.ActivityLog) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 如果已存在，更新
	if _, exists := r.activities[id]; exists {
		r.activities[id] = log
		return
	}

	// 检查是否需要淘汰
	if len(r.activities) >= maxCacheSize {
		// 淘汰最早的条目（FIFO）
		if len(r.cacheKeys) > 0 {
			oldestID := r.cacheKeys[0]
			delete(r.activities, oldestID)
			r.cacheKeys = r.cacheKeys[1:]
		}
	}

	// 添加新条目
	r.activities[id] = log
	r.cacheKeys = append(r.cacheKeys, id)
}

// Add 添加单条活动日志
func (r *ActivityRepository) Add(ctx context.Context, log *models.ActivityLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}

	// 先写入数据库
	query := `
		INSERT INTO activity_logs (id, device_id, process_name, window_title, browser_title, 
		                           started_at, ended_at, is_idle, duration_seconds, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			ended_at = EXCLUDED.ended_at,
			duration_seconds = EXCLUDED.duration_seconds
	`
	_, err := r.db.ExecContext(ctx, query,
		log.ID, log.DeviceID, log.ProcessName, log.WindowTitle, log.BrowserTitle,
		log.StartedAt, log.EndedAt, log.IsIdle, log.DurationSeconds, log.CreatedAt)
	if err != nil {
		return err
	}

	// 更新内存缓存
	r.addToCache(log.ID, log)

	return nil
}
// Count 统计活动日志数量
func (r *ActivityRepository) Count(ctx context.Context, deviceID string, startDate, endDate string) (int, error) {
	args := []interface{}{}
	where := []string{"1=1"}
	argIdx := 1

	if deviceID != "" {
		where = append(where, fmt.Sprintf("device_id = $%d", argIdx))
		args = append(args, deviceID)
		argIdx++
	}
	if startDate != "" {
		where = append(where, fmt.Sprintf("started_at >= $%d", argIdx))
		args = append(args, startDate)
		argIdx++
	}
	if endDate != "" {
		where = append(where, fmt.Sprintf("started_at <= $%d", argIdx))
		args = append(args, endDate+" 23:59:59")
		argIdx++
	}

	whereClause := strings.Join(where, " AND ")
	query := `SELECT COUNT(*) FROM activity_logs WHERE ` + whereClause

	var count int
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

// ---- Source: backend/services/agent_notification.go ----
const (
	AgentNotifyCategorySalaryPaid  = "salary_paid"
	AgentNotifyCategoryPenalty     = "penalty"
	AgentNotifyCategoryPerformance = "performance"
	AgentNotifyCategoryAttendance  = "attendance"
	AgentNotifyCategoryActivity    = "activity"
)
const (
	AgentNotifyLevelInfo    = "info"
	AgentNotifyLevelWarning = "warning"
	AgentNotifyLevelSuccess = "success"
)

type AgentNotificationSettings struct {
	Enabled                bool   `json:"enabled"`
	SalaryPaidEnabled      bool   `json:"salaryPaidEnabled"`
	PenaltyEnabled         bool   `json:"penaltyEnabled"`
	PerformanceEnabled     bool   `json:"performanceEnabled"`
	AttendanceEnabled      bool   `json:"attendanceEnabled"`
	ActivityEnabled        bool   `json:"activityEnabled"`
	DefaultDurationSec     int    `json:"defaultDurationSec"`
	StartupEnabled         bool   `json:"startupEnabled"`
	LocalLoginEnabled      bool   `json:"localLoginEnabled"`
	LocalAttendanceEnabled bool   `json:"localAttendanceEnabled"`
	LocalBindEnabled       bool   `json:"localBindEnabled"`
	LocalDurationSec       int    `json:"localDurationSec"`
	UpdatedAt              string `json:"updatedAt,omitempty"`
}

type AgentNotificationService struct {
	db    *sql.DB
	wsHub *WebSocketHub
}

func defaultAgentNotificationSettings() AgentNotificationSettings {
	return AgentNotificationSettings{
		Enabled:                true,
		SalaryPaidEnabled:      true,
		PenaltyEnabled:         true,
		PerformanceEnabled:     true,
		AttendanceEnabled:      true,
		ActivityEnabled:        true,
		DefaultDurationSec:     300,
		StartupEnabled:         true,
		LocalLoginEnabled:      true,
		LocalAttendanceEnabled: true,
		LocalBindEnabled:       true,
		LocalDurationSec:       60,
	}
}
func clampPushDurationSec(dur int) int {
	if dur < 30 {
		return 30
	}
	if dur > 600 {
		return 600
	}
	return dur
}
func clampLocalDurationSec(dur int) int {
	if dur < 8 {
		return 8
	}
	if dur > 600 {
		return 600
	}
	return dur
}
func (s *AgentNotificationService) GetSettings(ctx context.Context) (*AgentNotificationSettings, error) {
	out := defaultAgentNotificationSettings()
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, salary_paid_enabled, penalty_enabled, performance_enabled,
		       attendance_enabled, activity_enabled, default_duration_sec,
		       startup_enabled, local_login_enabled, local_attendance_enabled,
		       local_bind_enabled, local_duration_sec, updated_at
		FROM agent_notification_settings WHERE id = 1`).
		Scan(&out.Enabled, &out.SalaryPaidEnabled, &out.PenaltyEnabled, &out.PerformanceEnabled,
			&out.AttendanceEnabled, &out.ActivityEnabled, &out.DefaultDurationSec,
			&out.StartupEnabled, &out.LocalLoginEnabled, &out.LocalAttendanceEnabled,
			&out.LocalBindEnabled, &out.LocalDurationSec, &updatedAt)
	if err == sql.ErrNoRows {
		return &out, nil
	}
	if err != nil {
		return nil, err
	}
	out.DefaultDurationSec = clampPushDurationSec(out.DefaultDurationSec)
	out.LocalDurationSec = clampLocalDurationSec(out.LocalDurationSec)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return &out, nil
}
func (s *AgentNotificationService) categoryEnabled(settings *AgentNotificationSettings, category string) bool {
	if settings == nil || !settings.Enabled {
		return false
	}
	switch category {
	case AgentNotifyCategorySalaryPaid:
		return settings.SalaryPaidEnabled
	case AgentNotifyCategoryPenalty:
		return settings.PenaltyEnabled
	case AgentNotifyCategoryPerformance:
		return settings.PerformanceEnabled
	case AgentNotifyCategoryAttendance:
		return settings.AttendanceEnabled
	case AgentNotifyCategoryActivity:
		return settings.ActivityEnabled
	default:
		return false
	}
}
type AgentNotifyExtras struct {
	SessionID uuid.UUID
	Action    string
}

func (s *AgentNotificationService) applyAgentNotifyExtras(params gin.H, extras *AgentNotifyExtras) {
	if extras == nil {
		return
	}
	if extras.SessionID != uuid.Nil {
		params["sessionId"] = extras.SessionID.String()
	}
	if action := strings.TrimSpace(extras.Action); action != "" {
		params["action"] = action
	}
}
func (s *AgentNotificationService) PushToEmployeeAsync(
	empID uuid.UUID,
	category, title, message, level string,
	durationSec int,
	extras ...*AgentNotifyExtras,
) {
	if s == nil || empID == uuid.Nil {
		return
	}
	var opt *AgentNotifyExtras
	if len(extras) > 0 {
		opt = extras[0]
	}
	go func() {
		ok := s.pushToEmployee(context.Background(), empID, category, title, message, level, durationSec, opt)
		if ok {
			logPackage2.Printf("[AgentNotify] pushed category=%s emp=%s", category, empID)
		}
	}()
}
func (s *AgentNotificationService) pushToEmployee(
	ctx context.Context,
	empID uuid.UUID,
	category, title, message, level string,
	durationSec int,
	extras *AgentNotifyExtras,
) bool {
	if s == nil || s.wsHub == nil || empID == uuid.Nil {
		return false
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !s.categoryEnabled(settings, category) {
		return false
	}
	title = strings.TrimSpace(title)
	message = strings.TrimSpace(message)
	if title == "" && message == "" {
		return false
	}
	if title == "" {
		title = "通知"
	}
	level = normalizeAgentNotifyLevel(level)
	if durationSec <= 0 {
		durationSec = settings.DefaultDurationSec
	}
	if durationSec < 30 {
		durationSec = 30
	}
	if durationSec > 600 {
		durationSec = 600
	}

	deviceID, online, err := s.findOnlineDeviceForEmployee(ctx, empID)
	if err != nil || deviceID == uuid.Nil || !online {
		return false
	}
	params := gin.H{
		"title":       title,
		"message":     message,
		"type":        category,
		"level":       level,
		"durationSec": durationSec,
	}
	s.applyAgentNotifyExtras(params, extras)
	s.wsHub.SendCommandToDevice(deviceID.String(), "agent_notification", params)
	return true
}
func (s *AgentNotificationService) ResolveEmployeeID(ctx context.Context, empID uuid.UUID, employeeName string) uuid.UUID {
	if empID != uuid.Nil {
		return empID
	}
	name := strings.TrimSpace(employeeName)
	if name == "" || s == nil || s.db == nil {
		return uuid.Nil
	}
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM employees WHERE name = $1
		ORDER BY updated_at DESC NULLS LAST LIMIT 1`, name).Scan(&id)
	if err != nil {
		return uuid.Nil
	}
	return id
}
func (s *AgentNotificationService) PushPenalty(
	empID uuid.UUID,
	amount float64,
	category, reason, penaltyDate string,
	employeeName string,
) {
	if amount <= 0 {
		return
	}
	empID = s.ResolveEmployeeID(context.Background(), empID, employeeName)
	if empID == uuid.Nil {
		return
	}
	title := "罚款通知"
	date := strings.TrimSpace(penaltyDate)
	msg := fmt.Sprintf("您有一笔 ¥%.2f 的罚款", amount)
	if cat := strings.TrimSpace(category); cat != "" {
		msg += "（" + cat + "）"
	}
	if r := strings.TrimSpace(reason); r != "" {
		msg += "\n原因：" + r
	}
	if date != "" {
		msg += "\n日期：" + date
	}
	s.PushToEmployeeAsync(empID, AgentNotifyCategoryPenalty, title, msg, AgentNotifyLevelWarning, 0)
}
func (s *AgentNotificationService) PushPerformance(
	empID uuid.UUID,
	rec PerformancePushRecord,
) {
	empID = s.ResolveEmployeeID(context.Background(), empID, rec.EmployeeName)
	if empID == uuid.Nil {
		return
	}
	changeType := "加分"
	level := AgentNotifyLevelSuccess
	scoreText := fmt.Sprintf("+%d分", rec.Score)
	if rec.Score < 0 {
		changeType = "扣分"
		level = AgentNotifyLevelWarning
		scoreText = fmt.Sprintf("%d分", rec.Score)
	}
	title := "绩效变动"
	reason := strings.TrimSpace(rec.Reason)
	if reason == "" {
		reason = "-"
	}
	message := fmt.Sprintf("%s %s（%s）\n原因：%s", strings.TrimSpace(rec.Date), scoreText, changeType, reason)
	s.PushToEmployeeAsync(empID, AgentNotifyCategoryPerformance, title, message, level, 0)
}
func (s *AgentNotificationService) PushAttendanceException(
	empID uuid.UUID,
	employeeName, shift, event string,
	workFine float64,
	exceptionMinutes int,
) {
	if workFine <= 0 && exceptionMinutes <= 0 {
		return
	}
	empID = s.ResolveEmployeeID(context.Background(), empID, employeeName)
	if empID == uuid.Nil {
		return
	}
	label := strings.TrimSpace(shift)
	if label == "" {
		label = "班次"
	}
	title := "考勤异常"
	message := fmt.Sprintf("%s · %s", event, label)
	if exceptionMinutes > 0 {
		sec := exceptionMinutes * 60
		if event == "上班" {
			message += fmt.Sprintf("\n迟到 %s", formatDurationZh(sec))
		} else {
			message += fmt.Sprintf("\n早退 %s", formatDurationZh(sec))
		}
	}
	if workFine > 0 {
		message += fmt.Sprintf("\n罚款 ¥%.2f", workFine)
	}
	level := AgentNotifyLevelWarning
	s.PushToEmployeeAsync(empID, AgentNotifyCategoryAttendance, title, message, level, 0)
}
func (s *AgentNotificationService) PushActivityEvent(
	empID uuid.UUID,
	employeeName, activityName, event, extra string,
	sessionID uuid.UUID,
) {
	empID = s.ResolveEmployeeID(context.Background(), empID, employeeName)
	if empID == uuid.Nil {
		return
	}
	title, message, level := formatActivityAgentNotify(activityName, event, extra)
	var notifyExtras *AgentNotifyExtras
	if sessionID != uuid.Nil && activityAgentNotifySupportsBack(event) {
		notifyExtras = &AgentNotifyExtras{
			SessionID: sessionID,
			Action:    "activity_back",
		}
	}
	s.PushToEmployeeAsync(empID, AgentNotifyCategoryActivity, title, message, level, 0, notifyExtras)
}
func activityAgentNotifySupportsBack(event string) bool {
	switch strings.TrimSpace(event) {
	case "即将超时", "超时警告":
		return true
	default:
		return false
	}
}
func formatActivityAgentNotify(activityName, event, extra string) (title, message, level string) {
	act := strings.TrimSpace(activityName)
	if act == "" {
		act = "活动"
	}
	level = AgentNotifyLevelWarning
	extra = strings.TrimSpace(extra)
	switch strings.TrimSpace(event) {
	case "即将超时":
		title = "即将超时"
		message = fmt.Sprintf("活动：%s\n剩余约 1 分钟，请及时回座", act)
		if extra != "" {
			message += "\n" + extra
		}
	case "超时警告":
		title = "活动超时"
		message = fmt.Sprintf("活动：%s", act)
		if extra != "" {
			message += "\n" + extra
		} else {
			message += "\n已超时，请立即回座"
		}
	case "自动安全回座":
		title = "自动安全回座"
		message = fmt.Sprintf("活动：%s", act)
		if extra != "" {
			message += "\n" + extra
		}
	case "强制回座":
		title = "强制回座"
		message = fmt.Sprintf("活动：%s", act)
		if extra != "" {
			message += "\n" + extra
		}
	case "超时自动结束":
		title = "活动超时结束"
		message = fmt.Sprintf("活动：%s\n超时自动结束", act)
		if extra != "" {
			message += "\n" + extra
		}
	case "超时回座":
		title = "超时回座"
		message = fmt.Sprintf("活动：%s", act)
		if extra != "" {
			message += "\n" + extra
		}
	default:
		title = "活动通知"
		message = fmt.Sprintf("活动：%s\n%s", act, strings.TrimSpace(event))
		if extra != "" {
			message += "\n" + extra
		}
	}
	return title, message, level
}
func (s *AgentNotificationService) findOnlineDeviceForEmployee(ctx context.Context, empID uuid.UUID) (uuid.UUID, bool, error) {
	if s == nil || s.db == nil {
		return uuid.Nil, false, nil
	}
	var deviceID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM devices
		WHERE employee_uuid = $1 AND LOWER(status) = 'approved' AND employee_session_at IS NOT NULL
		ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
	if err != nil && err != sql.ErrNoRows {
		return uuid.Nil, false, err
	}
	if deviceID == uuid.Nil {
		err = s.db.QueryRowContext(ctx, `
			SELECT m.device_id FROM wf_shared_workstation_members m
			JOIN devices d ON d.id = m.device_id AND LOWER(d.status) = 'approved'
			WHERE m.employee_id = $1
			ORDER BY d.last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
		if err != nil && err != sql.ErrNoRows {
			return uuid.Nil, false, err
		}
	}
	if deviceID == uuid.Nil {
		return uuid.Nil, false, nil
	}
	online := s.wsHub != nil && s.wsHub.IsAgentOnline(deviceID.String())
	return deviceID, online, nil
}
func normalizeAgentNotifyLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case AgentNotifyLevelWarning, AgentNotifyLevelSuccess:
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return AgentNotifyLevelInfo
	}
}
// ---- Source: backend/services/alert_service.go ----
const (
	alertHTTPTimeout     = 10 * time.Second
	alertDefaultCooldown = 30 * time.Minute
	alertMonitorInterval = 5 * time.Minute
	deviceOfflineAfter   = 10 * time.Minute
	diskAlertPercent     = 90.0
)

// AlertEvent 运维告警事件
type AlertEvent struct {
	Type     string
	Severity string // info, warning, critical
	Title    string
	Message  string
	Details  map[string]interface{}
}

// AlertService 通过 Webhook 发送运维告警（支持 generic / dingtalk）
type AlertService struct {
	enabled     bool
	webhookURL  string
	webhookType string
	client      *httpPackage2.Client

	cooldown   time.Duration
	sentAt     map[string]time.Time
	sentAtLock sync.Mutex

	deviceRepo *DeviceRepository
	taskRepo   *TaskRepository
	uploadsDir string
}

func (s *AlertService) Enabled() bool {
	return s != nil && s.enabled
}
func (s *AlertService) Notify(ctx context.Context, event AlertEvent) {
	if s == nil || !s.enabled {
		return
	}

	key := s.cooldownKey(event)
	if s.isCoolingDown(key) {
		return
	}

	body, contentType, err := s.buildPayload(event)
	if err != nil {
		logPackage2.Printf("[Alert] Failed to build payload (%s): %v", event.Type, err)
		return
	}

	req, err := httpPackage2.NewRequestWithContext(ctx, httpPackage2.MethodPost, s.webhookURL, bytesPackage2.NewReader(body))
	if err != nil {
		logPackage2.Printf("[Alert] Failed to create request (%s): %v", event.Type, err)
		return
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := s.client.Do(req)
	if err != nil {
		logPackage2.Printf("[Alert] Webhook request failed (%s): %v", event.Type, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		logPackage2.Printf("[Alert] Webhook returned %d (%s): %s", resp.StatusCode, event.Type, strings.TrimSpace(string(respBody)))
		return
	}

	s.markSent(key)
	logPackage2.Printf("[Alert] Sent %s: %s", event.Type, event.Title)
}
func (s *AlertService) buildPayload(event AlertEvent) ([]byte, string, error) {
	text := fmt.Sprintf("[%s] %s\n%s", strings.ToUpper(event.Severity), event.Title, event.Message)
	if len(event.Details) > 0 {
		if detailsJSON, err := json.Marshal(event.Details); err == nil {
			text += "\n" + string(detailsJSON)
		}
	}

	switch s.webhookType {
	case "dingtalk":
		payload := map[string]interface{}{
			"msgtype": "text",
			"text": map[string]string{
				"content": text,
			},
		}
		body, err := json.Marshal(payload)
		return body, "application/json", err
	default:
		payload := map[string]interface{}{
			"event":     event.Type,
			"severity":  event.Severity,
			"title":     event.Title,
			"message":   event.Message,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"details":   event.Details,
		}
		body, err := json.Marshal(payload)
		return body, "application/json", err
	}
}
func (s *AlertService) cooldownKey(event AlertEvent) string {
	key := event.Type
	if event.Details != nil {
		for _, field := range []string{"subjectKey", "deviceId", "taskId", "path", "ip", "username"} {
			if v, ok := event.Details[field].(string); ok && v != "" {
				key += ":" + v
				break
			}
		}
	}
	return key
}
func (s *AlertService) isCoolingDown(key string) bool {
	s.sentAtLock.Lock()
	defer s.sentAtLock.Unlock()

	last, ok := s.sentAt[key]
	return ok && time.Since(last) < s.cooldown
}
func (s *AlertService) markSent(key string) {
	s.sentAtLock.Lock()
	defer s.sentAtLock.Unlock()
	s.sentAt[key] = time.Now()
}
// ---- Source: backend/services/attendance_review_notify.go ----
const notificationTypeAttendanceReview = "attendance_review_pending"

// NotifyAttendanceReviewPendingUsers inserts popup notifications for users who can open 考勤待确认.
// Layout bell unread also reflects live pending count via the notifications API.
func NotifyAttendanceReviewPendingUsers(
	ctx context.Context,
	db *sql.DB,
	taskID uuid.UUID,
	employeeName, attendanceDate, triggerType string,
) {
	if db == nil || taskID == uuid.Nil {
		return
	}
	kind := "早退"
	if strings.Contains(strings.ToLower(triggerType), "late") {
		kind = "迟到"
	}
	title := "考勤待确认"
	content := fmt.Sprintf("%s 有一条%s考勤异常待确认（%s），请前往【考勤绩效 - 待确认】处理。",
		strings.TrimSpace(employeeName), kind, strings.TrimSpace(attendanceDate))
	payload, _ := json.Marshal(map[string]interface{}{
		"type":           notificationTypeAttendanceReview,
		"taskId":         taskID.String(),
		"employeeName":   employeeName,
		"attendanceDate": attendanceDate,
		"triggerType":    triggerType,
		"path":           "/attendance/review",
	})

	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT u.id::text
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.status = 1
		  AND (
			LOWER(TRIM(r.name)) IN ('admin', 'administrator', '管理员', '超级管理员', 'superadmin')
			OR EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(r.permissions) AS p(perm)
				WHERE p.perm = $1
			)
		  )`, models.PermAttendanceReviewView)
	if err != nil {
		return
	}
	defer rows.Close()

	taskIDStr := taskID.String()
	for rows.Next() {
		var userID string
		if rows.Scan(&userID) != nil || strings.TrimSpace(userID) == "" {
			continue
		}
		var exists bool
		_ = db.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM user_notifications
				WHERE user_id = $1 AND type = $2
				  AND COALESCE(payload->>'taskId','') = $3
			)`, userID, notificationTypeAttendanceReview, taskIDStr).Scan(&exists)
		if exists {
			continue
		}
		_, _ = db.ExecContext(ctx, `
			INSERT INTO user_notifications (id, user_id, type, title, content, payload, is_read, show_popup)
			VALUES ($1, $2, $3, $4, $5, $6, FALSE, TRUE)
		`, uuid.NewString(), userID, notificationTypeAttendanceReview, title, content, payload)
	}
}

// ---- Source: backend/services/captcha.go ----
const captchaRedisPrefix = "login:captcha:"
const captchaTTL = 5 * time.Minute

// ---- Source: backend/services/capture_settings.go ----
// CaptureLinkedToCheckinEnvOverride returns (enabled, explicit) when TELEGRAM_AGENT_CAPTURE_ENABLED is set.
func CaptureLinkedToCheckinEnvOverride() (bool, bool) {
	v := strings.TrimSpace(os.Getenv("TELEGRAM_AGENT_CAPTURE_ENABLED"))
	if v == "" {
		return false, false
	}
	lower := strings.ToLower(v)
	return lower == "true" || lower == "1" || lower == "yes", true
}

// CaptureLinkedToCheckin reports whether check-in (client or Telegram) should start/stop capture.
// Default false: follow policy auto capture. Env var overrides DB when explicitly set.
func CaptureLinkedToCheckin(ctx context.Context, db *sql.DB) bool {
	if enabled, explicit := CaptureLinkedToCheckinEnvOverride(); explicit {
		return enabled
	}
	if db == nil {
		return false
	}
	var linked bool
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(capture_linked_to_checkin, FALSE)
		FROM wf_telegram_settings ORDER BY updated_at DESC LIMIT 1`).Scan(&linked)
	if err != nil {
		return false
	}
	return linked
}

// ---- Source: backend/services/cleanup_repository.go ----
const defaultCleanupPolicyID = "default"

// 仅统计/展示有实际删除内容的清理记录
const cleanupLogsWithDataWhere = `WHERE (deleted_recordings + deleted_screenshots + deleted_logs + deleted_business_records) > 0 OR deleted_bytes > 0`

// ---- Source: backend/services/client_protection.go ----
const (
	systemSettingClientProtectionDefaults = "client_protection_defaults"
	defaultUnlockCodeUnusedExpireMinutes  = 30
	minUnlockCodeUnusedExpireMinutes      = 5
	maxUnlockCodeUnusedExpireMinutes      = 1440
)

type clientProtectionDefaultsStored struct {
	AllowUserExit                 bool   `json:"allowUserExit"`
	AllowAutoStartToggle          bool   `json:"allowAutoStartToggle"`
	RestartOnKill                 bool   `json:"restartOnKill"`
	UnlockCodeUnusedExpireMinutes int    `json:"unlockCodeUnusedExpireMinutes"`
	UnlockCodeHash                string `json:"unlockCodeHash,omitempty"` // legacy, ignored
}

// ClientProtectionEffective is sent to agents (no secrets).
type ClientProtectionEffective struct {
	AllowUserExit        bool       `json:"allowUserExit"`
	AllowAutoStartToggle bool       `json:"allowAutoStartToggle"`
	RestartOnKill        bool       `json:"restartOnKill"`
	UnlockCodeConfigured bool       `json:"unlockCodeConfigured"` // true when device has unused, unexpired code
	TemporaryExitUntil   *time.Time `json:"temporaryExitUntil,omitempty"`
}
type ClientProtectionService struct {
	db  *sql.DB
	hub *WebSocketHub
}

func defaultClientProtectionStored() clientProtectionDefaultsStored {
	return clientProtectionDefaultsStored{
		AllowUserExit:                 true,
		AllowAutoStartToggle:          true,
		RestartOnKill:                 false,
		UnlockCodeUnusedExpireMinutes: defaultUnlockCodeUnusedExpireMinutes,
	}
}
func (s *ClientProtectionService) loadStoredDefaults(ctx context.Context) (clientProtectionDefaultsStored, *time.Time, error) {
	stored := defaultClientProtectionStored()
	var raw sql.NullString
	var updatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_at FROM system_settings WHERE key = $1`, systemSettingClientProtectionDefaults,
	).Scan(&raw, &updatedAt)
	if err == sql.ErrNoRows {
		return stored, nil, nil
	}
	if err != nil {
		return stored, nil, err
	}
	if raw.Valid && strings.TrimSpace(raw.String) != "" {
		_ = json.Unmarshal([]byte(raw.String), &stored)
	}
	if stored.UnlockCodeUnusedExpireMinutes <= 0 {
		stored.UnlockCodeUnusedExpireMinutes = defaultUnlockCodeUnusedExpireMinutes
	}
	var updated *time.Time
	if updatedAt.Valid {
		t := updatedAt.Time.UTC()
		updated = &t
	}
	return stored, updated, nil
}
func (s *ClientProtectionService) deviceHasActiveUnlockCode(ctx context.Context, deviceID uuid.UUID) bool {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM device_exit_unlock_codes
		WHERE device_id = $1 AND used_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
		LIMIT 1
	`, deviceID).Scan(&id)
	return err == nil
}
func (s *ClientProtectionService) ResolveEffective(ctx context.Context, deviceID string) (*ClientProtectionEffective, error) {
	id, err := uuid.Parse(strings.TrimSpace(deviceID))
	if err != nil {
		return nil, err
	}
	stored, _, err := s.loadStoredDefaults(ctx)
	if err != nil {
		return nil, err
	}
	eff := &ClientProtectionEffective{
		AllowUserExit:        stored.AllowUserExit,
		AllowAutoStartToggle: stored.AllowAutoStartToggle,
		RestartOnKill:        stored.RestartOnKill,
		UnlockCodeConfigured: s.deviceHasActiveUnlockCode(ctx, id),
	}

	var useDefault bool
	var allowExit, allowAuto, restart sql.NullBool
	var tempUntil sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(client_protection_use_default, TRUE),
		       client_protection_allow_user_exit,
		       client_protection_allow_auto_start_toggle,
		       client_protection_restart_on_kill,
		       client_protection_temp_exit_until
		FROM devices WHERE id = $1
	`, id).Scan(&useDefault, &allowExit, &allowAuto, &restart, &tempUntil)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil && !useDefault {
		if allowExit.Valid {
			eff.AllowUserExit = allowExit.Bool
		}
		if allowAuto.Valid {
			eff.AllowAutoStartToggle = allowAuto.Bool
		}
		if restart.Valid {
			eff.RestartOnKill = restart.Bool
		}
	}
	if tempUntil.Valid {
		t := tempUntil.Time.UTC()
		if t.After(time.Now().UTC()) {
			eff.TemporaryExitUntil = &t
		} else {
			_, _ = s.db.ExecContext(ctx, `
				UPDATE devices SET client_protection_temp_exit_until = NULL, updated_at = NOW()
				WHERE id = $1 AND client_protection_temp_exit_until IS NOT NULL
			`, id)
		}
	}
	eff.UnlockCodeConfigured = s.deviceHasActiveUnlockCode(ctx, id)
	return eff, nil
}
func (s *ClientProtectionService) PushConfigToDevice(ctx context.Context, deviceID string) {
	if s.hub == nil {
		return
	}
	eff, err := s.ResolveEffective(ctx, deviceID)
	if err != nil {
		return
	}
	s.hub.BroadcastToAgent(deviceID, gin.H{
		"type":      "client_protection_update",
		"messageId": uuid.NewString(),
		"config":    eff,
	})
}
func (s *ClientProtectionService) OnAgentRegistered(deviceID string) {
	if s.hub == nil {
		return
	}
	go s.PushConfigToDevice(context.Background(), deviceID)
}

// ---- Source: backend/services/client_release.go ----
var (
	fileHashCacheMu sync.RWMutex
	fileHashCache   = map[string]cachedFileHash{}
)

type cachedFileHash struct {
	modTime int64
	size    int64
	hash    string
}

// ---- Source: backend/services/cs_tool_settings.go ----
const (
	DefaultCsToolUrl    = "https://dh1888.github.io/168/"
	DefaultCsScriptUrl  = "https://dh1888.github.io/render/"
	maxCsToolCustomMenu = 30
)

var csToolMenuIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

type CsToolMenuItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}
type CsToolSettings struct {
	ToolUrl     string           `json:"toolUrl"`
	ScriptUrl   string           `json:"scriptUrl"`
	CustomMenus []CsToolMenuItem `json:"customMenus"`
	UpdatedAt   string           `json:"updatedAt,omitempty"`
}
type CsToolSettingsService struct {
	db *sql.DB
}

func (s *CsToolSettingsService) GetSettings(ctx context.Context) (*CsToolSettings, error) {
	out := &CsToolSettings{
		ToolUrl:     DefaultCsToolUrl,
		ScriptUrl:   DefaultCsScriptUrl,
		CustomMenus: []CsToolMenuItem{},
	}
	var updatedAt time.Time
	var menusJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT tool_url, script_url, COALESCE(custom_menus, '[]'::jsonb), updated_at
		FROM cs_tool_settings WHERE id = 1`).
		Scan(&out.ToolUrl, &out.ScriptUrl, &menusJSON, &updatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.ToolUrl = normalizeCsToolURL(out.ToolUrl, DefaultCsToolUrl)
	out.ScriptUrl = normalizeCsToolURL(out.ScriptUrl, DefaultCsScriptUrl)
	out.CustomMenus = decodeCsToolCustomMenus(menusJSON)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return out, nil
}
func decodeCsToolCustomMenus(raw []byte) []CsToolMenuItem {
	if len(raw) == 0 {
		return []CsToolMenuItem{}
	}
	var items []CsToolMenuItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return []CsToolMenuItem{}
	}
	normalized, err := normalizeCsToolCustomMenus(items)
	if err != nil {
		return []CsToolMenuItem{}
	}
	return normalized
}
func normalizeCsToolCustomMenus(items []CsToolMenuItem) ([]CsToolMenuItem, error) {
	if len(items) > maxCsToolCustomMenu {
		return nil, fmt.Errorf("自定义菜单最多 %d 个", maxCsToolCustomMenu)
	}
	reserved := map[string]bool{"tool": true, "script": true, "custom": true}
	seen := map[string]bool{}
	out := make([]CsToolMenuItem, 0, len(items))
	for _, item := range items {
		id := strings.ToLower(strings.TrimSpace(item.ID))
		name := strings.TrimSpace(item.Name)
		link, err := validateCsToolURL(item.URL, "")
		if err != nil {
			return nil, fmt.Errorf("菜单「%s」跳转地址无效", nameOrID(name, id))
		}
		if id == "" || !csToolMenuIDPattern.MatchString(id) {
			return nil, fmt.Errorf("菜单「%s」标识无效", nameOrID(name, id))
		}
		if reserved[id] {
			return nil, fmt.Errorf("菜单标识 %q 为系统保留", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("菜单标识 %q 重复", id)
		}
		if name == "" {
			return nil, fmt.Errorf("菜单 %q 名称不能为空", id)
		}
		if len([]rune(name)) > 32 {
			return nil, fmt.Errorf("菜单「%s」名称过长", name)
		}
		seen[id] = true
		out = append(out, CsToolMenuItem{ID: id, Name: name, URL: link})
	}
	return out, nil
}
func nameOrID(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	if strings.TrimSpace(id) != "" {
		return id
	}
	return "未命名"
}
func validateCsToolURL(raw, fallback string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		if fallback == "" {
			return "", ErrInvalidCsToolURL
		}
		return fallback, nil
	}
	u, err := urlPackage2.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ErrInvalidCsToolURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrInvalidCsToolURL
	}
	return u.String(), nil
}
func normalizeCsToolURL(raw, fallback string) string {
	v, err := validateCsToolURL(raw, fallback)
	if err != nil || v == "" {
		return fallback
	}
	return v
}

var ErrInvalidCsToolURL = &csToolURLError{}

type csToolURLError struct{}

func (e *csToolURLError) Error() string { return "跳转地址必须是有效的 http 或 https URL" }

// ---- Source: backend/services/cs_tool_shift_roster.go ----
const maxShiftRosterJSONBytes = 256 * 1024

// ---- Source: backend/services/deployment_drift.go ----
const (
	deploymentRegistryKey   = "deploy:registry"
	deploymentDriftInterval = time.Minute
	deploymentInstanceTTL   = 3 * time.Minute
)

var deploymentInstanceID = fmt.Sprintf("%s:%d", hostname(), os.Getpid())

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// ---- Source: backend/services/diagnostic_analyzer.go ----
func (s *DiagnosticService) Analyze(ctx context.Context, deviceID string, windowMinutes int) (*models.DiagnosticAnalysis, error) {
	if windowMinutes <= 0 {
		windowMinutes = 30
	}
	if windowMinutes > 24*60 {
		windowMinutes = 24 * 60
	}
	since := models.UTCNow().Add(-time.Duration(windowMinutes) * time.Minute)

	q := DiagnosticEventQuery{Page: 1, PageSize: 500, DeviceID: deviceID}
	q.From = &since
	events, _, err := s.ListEvents(ctx, q)
	if err != nil {
		return nil, err
	}

	counts := map[string]int{"ERROR": 0, "WARN": 0, "INFO": 0, "DEBUG": 0, "TRACE": 0}
	findings := []models.DiagnosticFinding{}

	for _, ev := range events {
		counts[string(ev.Level)]++
	}

	// Rule: high error rate
	if counts["ERROR"] >= 3 {
		findings = append(findings, models.DiagnosticFinding{
			Severity:        "critical",
			Code:            "HIGH_ERROR_RATE",
			Title:           "错误事件集中出现",
			Detail:          fmt.Sprintf("近 %d 分钟内 ERROR 事件 %d 条", windowMinutes, counts["ERROR"]),
			SuggestedAction: "查看 Timeline / Logs 中 ERROR 详情，优先检查 FFmpeg / WHIP / Ingress",
		})
	}

	// Rule: dead frame / srcObject
	for _, ev := range events {
		msg := strings.ToLower(ev.Message)
		if ev.Module == "RemoteView" && (strings.Contains(msg, "dead frame") || strings.Contains(msg, "srcobject")) {
			findings = append(findings, models.DiagnosticFinding{
				Severity:        "warning",
				Code:            "REMOTE_VIEW_DEAD_FRAME",
				Title:           "前端可能存在 dead frame",
				Detail:          ev.Message,
				Module:          ev.Module,
				DeviceID:        deviceIDFromEvent(ev),
				SuggestedAction: "检查 trackSid 是否变化、LiveKit reconnect 是否成功",
				Evidence:        []string{ev.Message},
			})
			break
		}
	}

	// Rule: ingress lifecycle
	ingressDeletes := 0
	for _, ev := range events {
		if strings.Contains(strings.ToLower(ev.Message), "ingress") {
			ingressDeletes++
		}
	}
	if ingressDeletes >= 2 {
		findings = append(findings, models.DiagnosticFinding{
			Severity:        "info",
			Code:            "INGRESS_CHURN",
			Title:           "Ingress 生命周期频繁变化",
			Detail:          fmt.Sprintf("近 %d 分钟内 ingress 相关事件 %d 条", windowMinutes, ingressDeletes),
			SuggestedAction: "确认 stop/start 间隔是否小于 15s DeleteIngress 延迟",
		})
	}

	// Rule: ffmpeg teardown
	for _, ev := range events {
		if ev.Module == "FFmpeg" && strings.Contains(ev.Message, "killed=true") {
			findings = append(findings, models.DiagnosticFinding{
				Severity:        "warning",
				Code:            "FFMPEG_FORCE_KILL",
				Title:           "FFmpeg 被强制 Kill",
				Detail:          ev.Message,
				Module:          "FFmpeg",
				DeviceID:        deviceIDFromEvent(ev),
				SuggestedAction: "检查 WHIP 退出是否超时，或 quality switch 是否重叠",
			})
			break
		}
	}

	// Rule: stale remote stats from snapshot comparison
	if s.remoteView != nil && deviceID != "" {
		if st, err := s.remoteView.GetStatus(ctx, deviceID); err == nil && st != nil && st.IsActive {
			if st.SfuChecked && !st.SfuPublishing {
				findings = append(findings, models.DiagnosticFinding{
					Severity:        "warning",
					Code:            "SFU_NOT_PUBLISHING",
					Title:           "SFU 未确认推流",
					Detail:          fmt.Sprintf("session=%s status=%s sfuPublishing=false statsFresh=%v", st.SessionID, st.Status, st.StatsFresh),
					Module:          "RemoteView",
					DeviceID:        deviceID,
					SuggestedAction: "Agent 编码 fps 不能当作已推流；检查 WHIP/Ingress 与房间视频轨",
				})
			} else if !st.SfuChecked && !st.StatsFresh {
				findings = append(findings, models.DiagnosticFinding{
					Severity:        "warning",
					Code:            "STALE_PUBLISHER_STATS",
					Title:           "Agent 推流 stats 过期",
					Detail:          fmt.Sprintf("session=%s status=%s statsFresh=false", st.SessionID, st.Status),
					Module:          "RemoteView",
					DeviceID:        deviceID,
					SuggestedAction: "Agent 可能已停推或 WS stats 上报中断",
				})
			}
		}
	}

	summary := fmt.Sprintf("分析窗口 %d 分钟，共 %d 条事件", windowMinutes, len(events))
	if len(findings) == 0 {
		summary += "；未发现明显异常"
	} else {
		summary += fmt.Sprintf("；发现 %d 条提示", len(findings))
	}

	analysis := &models.DiagnosticAnalysis{
		GeneratedAt:   models.UTCNow(),
		WindowMinutes: windowMinutes,
		Summary:       summary,
		Findings:      findings,
		Counts:        counts,
	}
	analysis.Conclusion = s.buildConclusion(ctx, deviceID, findings)
	return analysis, nil
}
func (s *DiagnosticService) buildConclusion(ctx context.Context, deviceID string, findings []models.DiagnosticFinding) *models.DiagnosticConclusion {
	checks := []models.DiagnosticConclusionCheck{
		{Label: "Agent 在线", OK: true},
		{Label: "Capture 正常", OK: true},
		{Label: "FFmpeg 正常", OK: true},
		{Label: "WHIP 正常", OK: true},
		{Label: "Backend 正常", OK: true},
		{Label: "Browser Track 正常", OK: true},
	}

	probableCause := "未发现明显异常，链路整体正常"
	confidence := 75
	title := "诊断结论"

	if deviceID != "" && s.remoteView != nil {
		if pipe, err := s.BuildPipeline(ctx, deviceID); err == nil && pipe != nil {
			for i := range checks {
				for _, node := range pipe.Nodes {
					switch checks[i].Label {
					case "Agent 在线":
						if node.ID == "agent" {
							checks[i].OK = node.Status == statusHealthy
						}
					case "Capture 正常":
						if node.ID == "capture" {
							checks[i].OK = node.Status == statusHealthy
						}
					case "FFmpeg 正常":
						if node.ID == "ffmpeg" {
							checks[i].OK = node.Status == statusHealthy
						}
					case "WHIP 正常":
						if node.ID == "whip" {
							checks[i].OK = node.Status == statusHealthy
						}
					case "Backend 正常":
						if node.ID == "ingress" || node.ID == "livekit" {
							if node.Status != statusHealthy {
								checks[i].OK = false
							}
						}
					case "Browser Track 正常":
						if node.ID == "frontend" {
							checks[i].OK = node.Status == statusHealthy
						}
					}
				}
			}
		}
	}

	for _, f := range findings {
		switch f.Code {
		case "REMOTE_VIEW_DEAD_FRAME":
			checks[5].OK = false
			probableCause = "LiveKit Track 未更新或 Browser srcObject 未切换"
			confidence = 91
			title = "前端播放层异常"
		case "FFMPEG_FORCE_KILL":
			checks[2].OK = false
			probableCause = "FFmpeg 被强制终止，推流链路中断"
			confidence = 88
			title = "编码进程异常"
		case "STALE_PUBLISHER_STATS":
			checks[0].OK = false
			checks[2].OK = false
			probableCause = "Agent 推流 stats 过期，可能已停推或 WS 上报中断"
			confidence = 86
			title = "Agent 推流异常"
		case "INGRESS_CHURN":
			checks[4].OK = false
			if confidence < 80 {
				probableCause = "Ingress 生命周期频繁变化，可能存在 stop/start 竞态"
				confidence = 82
				title = "Ingress 不稳定"
			}
		case "HIGH_ERROR_RATE":
			confidence = 70
			title = "错误事件集中"
			probableCause = "近窗口内 ERROR 事件偏多，建议查看 Session Timeline"
		}
	}

	if len(findings) == 0 {
		okCount := 0
		for _, c := range checks {
			if c.OK {
				okCount++
			}
		}
		confidence = 60 + okCount*5
		if confidence > 95 {
			confidence = 95
		}
	}

	return &models.DiagnosticConclusion{
		Title:         title,
		ProbableCause: probableCause,
		Confidence:    confidence,
		Checks:        checks,
	}
}
func deviceIDFromEvent(ev models.DiagnosticEvent) string {
	if ev.DeviceID == nil {
		return ""
	}
	return ev.DeviceID.String()
}

func (s *DiagnosticService) GetAgentSnapshots(deviceIDs []string) map[string]interface{} {
	out := map[string]interface{}{}
	cutoff := time.Now().Add(-2 * time.Minute)
	s.agentSnapMu.RLock()
	defer s.agentSnapMu.RUnlock()
	if len(deviceIDs) == 0 {
		for id, entry := range s.agentSnaps {
			if entry.received.After(cutoff) {
				out[id] = entry.payload
			}
		}
		return out
	}
	for _, id := range deviceIDs {
		if entry, ok := s.agentSnaps[id]; ok && entry.received.After(cutoff) {
			out[id] = entry.payload
		}
	}
	return out
}
func (s *DiagnosticService) RequestAgentSnapshots(deviceIDs []string) {
	if s.wsHub == nil {
		return
	}
	for _, deviceID := range deviceIDs {
		if !s.wsHub.IsAgentOnline(deviceID) {
			continue
		}
		s.wsHub.SendCommandToDevice(deviceID, "collect_diagnostics", nil)
	}
}
func (s *DiagnosticService) Capture(ctx context.Context, req models.DiagnosticCaptureRequest) (*models.DiagnosticCaptureResult, error) {
	window := req.WindowMinutes
	if window <= 0 {
		window = 60
	}

	snapshot, err := s.BuildSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	if req.IncludeAgents && len(req.DeviceIDs) > 0 {
		s.RequestAgentSnapshots(req.DeviceIDs)
		time.Sleep(1200 * time.Millisecond)
	}

	result := &models.DiagnosticCaptureResult{
		CaptureID:   uuid.New().String(),
		GeneratedAt: models.UTCNow(),
		Snapshot:    *snapshot,
		AgentSnaps:  s.GetAgentSnapshots(req.DeviceIDs),
		Pipelines:   []models.DiagnosticPipeline{},
	}

	if req.IncludeAnalysis {
		analysis, err := s.Analyze(ctx, "", window)
		if err == nil {
			result.Analysis = analysis
		}
		for _, deviceID := range req.DeviceIDs {
			if pipe, err := s.BuildPipeline(ctx, deviceID); err == nil {
				result.Pipelines = append(result.Pipelines, *pipe)
			}
		}
	}

	q := DiagnosticEventQuery{Page: 1, PageSize: 5000}
	since := models.UTCNow().Add(-time.Duration(window) * time.Minute)
	q.From = &since
	if len(req.DeviceIDs) == 1 {
		q.DeviceID = req.DeviceIDs[0]
	}
	events, _, _ := s.ListEvents(ctx, q)
	result.EventCount = len(events)

	return result, nil
}
// ---- Source: backend/services/diagnostic_health.go ----
func (s *DiagnosticService) enrichSnapshot(ctx context.Context, snap *models.DiagnosticSnapshot) {
	if snap == nil {
		return
	}
	snap.StatsToday = s.buildDailyStats(ctx)
	snap.Health = s.buildSystemHealth(ctx, snap)
}
func (s *DiagnosticService) buildDailyStats(ctx context.Context) models.DiagnosticDailyStats {
	stats := models.DiagnosticDailyStats{}
	startOfDay := models.UTCNow().Truncate(24 * time.Hour)
	rows, err := s.db.QueryContext(ctx, `
		SELECT action, COUNT(*)
		FROM remote_view_audit_logs
		WHERE created_at >= $1
		GROUP BY action
	`, startOfDay)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		var count int
		if err := rows.Scan(&action, &count); err != nil {
			continue
		}
		lower := strings.ToLower(action)
		switch {
		case strings.Contains(lower, "start") || strings.Contains(action, "启动") || strings.Contains(action, "开始"):
			stats.RemoteViewStarts += count
		case strings.Contains(lower, "success") || strings.Contains(action, "成功"):
			stats.RemoteViewSuccess += count
		case strings.Contains(lower, "fail") || strings.Contains(lower, "error") ||
			strings.Contains(action, "失败") || strings.Contains(action, "断开"):
			stats.RemoteViewFailures += count
		}
	}
	if stats.RemoteViewStarts == 0 {
		stats.RemoteViewStarts = stats.RemoteViewSuccess + stats.RemoteViewFailures
	}
	if stats.RemoteViewStarts > 0 {
		success := stats.RemoteViewStarts - stats.RemoteViewFailures
		if success < 0 {
			success = stats.RemoteViewSuccess
		}
		stats.RemoteViewSuccess = success
		stats.SuccessRate = float64(success) / float64(stats.RemoteViewStarts) * 100
	}
	return stats
}
func (s *DiagnosticService) buildSystemHealth(ctx context.Context, snap *models.DiagnosticSnapshot) models.DiagnosticHealthScore {
	layers := []models.DiagnosticHealthLayer{}
	score := 100

	wsStatus := statusHealthy
	wsDetail := "连接正常"
	if snap.WebSocket.AgentsOnline == 0 {
		wsStatus = statusDegraded
		wsDetail = "无在线 Agent"
		score -= 15
	}
	layers = append(layers, models.DiagnosticHealthLayer{ID: "websocket", Label: "WebSocket", Status: wsStatus, Detail: wsDetail})

	agentStatus := statusUnknown
	agentDetail := "无 Agent"
	if len(snap.Agents) > 0 {
		online := 0
		for _, a := range snap.Agents {
			if a.Online {
				online++
			}
		}
		switch {
		case online == len(snap.Agents):
			agentStatus = statusHealthy
			agentDetail = "全部在线"
		case online > 0:
			agentStatus = statusDegraded
			agentDetail = "部分离线"
			score -= 10
		default:
			agentStatus = statusDown
			agentDetail = "全部离线"
			score -= 25
		}
	}
	layers = append(layers, models.DiagnosticHealthLayer{ID: "agent", Label: "Agent", Status: agentStatus, Detail: agentDetail})

	pipelineLayers := []string{"capture", "ffmpeg", "whip", "ingress", "livekit", "frontend"}
	pipelineLabels := map[string]string{
		"capture": "Capture", "ffmpeg": "FFmpeg", "whip": "WHIP",
		"ingress": "Ingress", "livekit": "LiveKit", "frontend": "Browser",
	}

	if snap.RemoteView.ActiveSessions > 0 && len(snap.RemoteView.Sessions) > 0 {
		session := snap.RemoteView.Sessions[0]
		if pipe, err := s.BuildPipeline(ctx, session.DeviceID); err == nil && pipe != nil {
			for _, nodeID := range pipelineLayers {
				for _, node := range pipe.Nodes {
					if node.ID != nodeID {
						continue
					}
					layers = append(layers, models.DiagnosticHealthLayer{
						ID: node.ID, Label: pipelineLabels[nodeID], Status: node.Status, Detail: node.Detail,
					})
					switch node.Status {
					case statusDegraded:
						score -= 8
					case statusDown:
						score -= 20
					case statusUnknown:
						score -= 3
					}
				}
			}
		}
	} else {
		for _, nodeID := range pipelineLayers {
			layers = append(layers, models.DiagnosticHealthLayer{
				ID: nodeID, Label: pipelineLabels[nodeID], Status: statusUnknown, Detail: "无活跃 RemoteView 会话",
			})
			score -= 2
		}
	}

	errCount := snap.Events.Last5Min["ERROR"]
	if errCount >= 3 {
		score -= 15
	} else if errCount >= 1 {
		score -= 5
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return models.DiagnosticHealthScore{Score: score, Layers: layers}
}

// ---- Source: backend/services/diagnostic_pipeline.go ----
func (s *DiagnosticService) BuildPipeline(ctx context.Context, deviceIDStr string) (*models.DiagnosticPipeline, error) {
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		return nil, err
	}

	now := models.UTCNow()
	pipeline := &models.DiagnosticPipeline{
		DeviceID:      deviceIDStr,
		GeneratedAt:   now,
		OverallStatus: "unknown",
		Nodes:         []models.DiagnosticPipelineNode{},
		Edges: []models.DiagnosticPipelineEdge{
			{From: "agent", To: "capture"},
			{From: "capture", To: "ffmpeg"},
			{From: "ffmpeg", To: "whip"},
			{From: "whip", To: "ingress"},
			{From: "ingress", To: "livekit"},
			{From: "livekit", To: "frontend"},
		},
	}

	if s.deviceRepo != nil {
		if dev, err := s.deviceRepo.Get(ctx, deviceID); err == nil && dev != nil {
			pipeline.Hostname = dev.Hostname
		}
	}

	agentOnline := s.wsHub != nil && s.wsHub.IsAgentOnline(deviceIDStr)
	agentStatus := statusDown
	agentDetail := "Agent 离线"
	if agentOnline {
		agentStatus = statusHealthy
		agentDetail = "WebSocket 已连接"
	}
	pipeline.Nodes = append(pipeline.Nodes, models.DiagnosticPipelineNode{
		ID: "agent", Label: "Agent", Layer: "edge", Status: agentStatus, Detail: agentDetail,
	})

	var session *models.RemoteViewSession
	var rvStatus *models.RemoteViewStatusResponse
	if s.remoteView != nil {
		rvStatus, _ = s.remoteView.GetStatus(ctx, deviceIDStr)
		session, _ = s.remoteView.GetActiveSession(ctx, deviceID)
	}

	captureStatus, captureDetail := statusUnknown, "无活跃远程会话"
	ffmpegStatus, ffmpegDetail := statusUnknown, "未推流"
	whipStatus, whipDetail := statusUnknown, "无 WHIP 会话"
	ingressStatus, ingressDetail := statusUnknown, "无 Ingress"
	lkStatus, lkDetail := statusUnknown, "LiveKit 未就绪"

	if session != nil {
		pipeline.SessionID = session.ID.String()
		captureStatus = statusHealthy
		captureDetail = "SharedBridge / Capture 编排中"
		if session.Status == models.RemoteViewStatusStarting {
			captureStatus = statusDegraded
			captureDetail = "会话 starting，等待首帧"
		}
		if session.IngressID != "" {
			ingressStatus = statusHealthy
			ingressDetail = "Ingress " + truncateMiddle(session.IngressID, 12)
		}
		if strings.Contains(strings.ToLower(session.RTMPURL), "whip") || strings.Contains(session.RTMPURL, "/w/") {
			whipStatus = statusHealthy
			whipDetail = "WHIP publish URL 已分配"
		} else if session.RTMPURL != "" {
			whipStatus = statusDegraded
			whipDetail = "RTMP 推流（非 WHIP）"
		}
		if session.LiveKitURL != "" {
			lkStatus = statusHealthy
			lkDetail = "Room " + session.RoomName
		}
	}
	if rvStatus != nil && rvStatus.SfuChecked {
		if rvStatus.SfuPublishing {
			whipStatus = statusHealthy
			whipDetail = "SFU 确认正在推流"
			ingressStatus = statusHealthy
			ingressDetail = "Ingress publishing / 未静音视频轨"
			lkStatus = statusHealthy
			if session != nil {
				lkDetail = "Room " + session.RoomName + " publisher live"
			} else {
				lkDetail = "publisher 视频轨活跃"
			}
		} else if session != nil {
			whipStatus = statusDown
			whipDetail = "WHIP 出口未在 SFU 上推流（不采信 agent fps）"
			ingressStatus = statusDegraded
			ingressDetail = "Ingress/房间无活跃视频轨"
			lkStatus = statusDegraded
			lkDetail = "publisher 未发布或已静音"
		}
	}

	statsFresh := false
	var stats *models.RemoteViewStatsPayload
	if s.remoteView != nil {
		statsFresh = s.remoteView.hasFreshRemoteStats(deviceIDStr)
		stats = s.remoteView.GetDeviceStats(deviceIDStr)
	}
	if statsFresh && stats != nil {
		ffmpegStatus = statusHealthy
		ffmpegDetail = "编码中 " + stats.Encoder
		if stats.FPS <= 2 {
			ffmpegStatus = statusDegraded
			ffmpegDetail = "编码 FPS 过低 (" + strconv.Itoa(stats.FPS) + ")"
		}
	} else if session != nil {
		ffmpegStatus = statusDegraded
		ffmpegDetail = "推流 stats 过期或未上报"
	}

	frontendStatus, frontendDetail := statusUnknown, "无观看者"
	if rvStatus != nil && rvStatus.IsActive {
		if rvStatus.ViewerCount > 0 {
			frontendStatus = statusHealthy
			frontendDetail = "观看者 " + strconv.Itoa(rvStatus.ViewerCount)
		} else {
			frontendStatus = statusDegraded
			frontendDetail = "会话 active 但无 viewer"
		}
	}

	pipeline.Nodes = append(pipeline.Nodes,
		nodeWithMetrics("capture", "SharedBridge", "capture", captureStatus, captureDetail, nil),
		nodeWithMetrics("ffmpeg", "FFmpeg", "encode", ffmpegStatus, ffmpegDetail, statsMap(stats)),
		nodeWithMetrics("whip", "WHIP", "transport", whipStatus, whipDetail, nil),
		nodeWithMetrics("ingress", "LiveKit Ingress", "ingress", ingressStatus, ingressDetail, map[string]interface{}{"ingressId": safeStr(session, "ingress")}),
		nodeWithMetrics("livekit", "LiveKit SFU", "media", lkStatus, lkDetail, nil),
		nodeWithMetrics("frontend", "Browser Player", "client", frontendStatus, frontendDetail, map[string]interface{}{"viewers": viewerCount(rvStatus)}),
	)

	pipeline.OverallStatus = aggregatePipelineStatus(pipeline.Nodes)
	pipeline.Nodes = s.attachPipelineNodeLogs(ctx, deviceIDStr, pipeline.Nodes)
	return pipeline, nil
}

const (
	statusHealthy  = "healthy"
	statusDegraded = "degraded"
	statusDown     = "down"
	statusUnknown  = "unknown"
)

func nodeWithMetrics(id, label, layer, status, detail string, metrics map[string]interface{}) models.DiagnosticPipelineNode {
	return models.DiagnosticPipelineNode{ID: id, Label: label, Layer: layer, Status: status, Detail: detail, Metrics: metrics}
}
func aggregatePipelineStatus(nodes []models.DiagnosticPipelineNode) string {
	hasDown := false
	hasDegraded := false
	for _, n := range nodes {
		switch n.Status {
		case statusDown:
			hasDown = true
		case statusDegraded:
			hasDegraded = true
		}
	}
	if hasDown {
		return statusDown
	}
	if hasDegraded {
		return statusDegraded
	}
	allHealthy := len(nodes) > 0
	for _, n := range nodes {
		if n.Status != statusHealthy {
			allHealthy = false
			break
		}
	}
	if allHealthy {
		return statusHealthy
	}
	return statusUnknown
}
func statsMap(stats *models.RemoteViewStatsPayload) map[string]interface{} {
	if stats == nil {
		return nil
	}
	return map[string]interface{}{
		"fps":         stats.FPS,
		"bitrateKbps": stats.BitrateKbps,
		"encoder":     stats.Encoder,
	}
}
func viewerCount(st *models.RemoteViewStatusResponse) int {
	if st == nil {
		return 0
	}
	return st.ViewerCount
}
func safeStr(session *models.RemoteViewSession, field string) string {
	if session == nil {
		return ""
	}
	if field == "ingress" {
		return session.IngressID
	}
	return ""
}
func truncateMiddle(s string, keep int) string {
	if len(s) <= keep*2 {
		return s
	}
	return s[:keep] + "…" + s[len(s)-keep:]
}

// ---- Source: backend/services/diagnostic_pipeline_detail.go ----
var pipelineNodeModules = map[string][]string{
	"agent":    {"WebSocket", "Health"},
	"capture":  {"Capture", "Lifecycle"},
	"ffmpeg":   {"FFmpeg", "RemoteView"},
	"whip":     {"RemoteView", "FFmpeg"},
	"ingress":  {"Backend", "RemoteView"},
	"livekit":  {"LiveKit", "Backend"},
	"frontend": {"RemoteView", "LiveKit"},
}

func (s *DiagnosticService) attachPipelineNodeLogs(ctx context.Context, deviceID string, nodes []models.DiagnosticPipelineNode) []models.DiagnosticPipelineNode {
	since := models.UTCNow().Add(-30 * time.Minute)
	for i := range nodes {
		modules := pipelineNodeModules[nodes[i].ID]
		if len(modules) == 0 {
			continue
		}
		logs, errors := s.fetchNodeEvents(ctx, deviceID, modules, since, 8)
		nodes[i].RecentLogs = logs
		nodes[i].RecentErrors = errors
	}
	return nodes
}
func (s *DiagnosticService) fetchNodeEvents(
	ctx context.Context,
	deviceID string,
	modules []string,
	since time.Time,
	limit int,
) ([]models.DiagnosticEventBrief, []models.DiagnosticEventBrief) {
	if len(modules) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(modules))
	args := []interface{}{since}
	for i, m := range modules {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, m)
	}
	deviceClause := ""
	if deviceID != "" {
		deviceClause = fmt.Sprintf(" AND device_id = $%d", len(args)+1)
		args = append(args, deviceID)
	}
	query := fmt.Sprintf(`
		SELECT created_at, level, module, message
		FROM diagnostic_events
		WHERE created_at >= $1 AND module IN (%s)%s
		ORDER BY created_at DESC
		LIMIT %d
	`, strings.Join(placeholders, ","), deviceClause, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	logs := []models.DiagnosticEventBrief{}
	errors := []models.DiagnosticEventBrief{}
	for rows.Next() {
		var createdAt time.Time
		var level, module, message string
		if err := rows.Scan(&createdAt, &level, &module, &message); err != nil {
			continue
		}
		item := models.DiagnosticEventBrief{
			CreatedAt: models.FormatUTC(createdAt),
			Level:     level,
			Module:    module,
			Message:   message,
		}
		logs = append(logs, item)
		if level == "ERROR" || level == "WARN" {
			errors = append(errors, item)
		}
	}
	return logs, errors
}
// ---- Source: backend/services/diagnostic_service.go ----
const systemSettingDiagnosticCenter = "diagnostic_center_config"

type DiagnosticService struct {
	db         *sql.DB
	wsHub      *WebSocketHub
	remoteView *RemoteViewService
	deviceRepo *DeviceRepository

	mu     sync.RWMutex
	config models.DiagnosticCenterConfig
	loaded bool

	agentSnapMu sync.RWMutex
	agentSnaps  map[string]agentSnapshotEntry

	reproduceMu   sync.RWMutex
	reproduceJobs map[string]*models.DiagnosticReproduceStatus
	reproduceCap  map[string]*models.DiagnosticCaptureResult
}
type agentSnapshotEntry struct {
	payload  map[string]interface{}
	received time.Time
}

func (s *DiagnosticService) GetRuntimeConfig() models.DiagnosticRuntimeConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Effective(time.Now().UTC())
}
func (s *DiagnosticService) PushConfigToDevice(deviceID string) {
	if s.wsHub == nil || deviceID == "" {
		return
	}
	runtime := s.GetRuntimeConfig()
	s.wsHub.SendCriticalToAgent(deviceID, gin.H{
		"type":   "diagnostic_config_update",
		"config": runtime,
	})
}
type DiagnosticEventQuery struct {
	Source   string
	Module   string
	Level    string
	DeviceID string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

func (s *DiagnosticService) ListEvents(ctx context.Context, q DiagnosticEventQuery) ([]models.DiagnosticEvent, int, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 50
	}
	if q.PageSize > 200 {
		q.PageSize = 200
	}

	where := "1=1"
	args := []interface{}{}
	idx := 1

	if q.Source != "" {
		where += fmt.Sprintf(" AND source = $%d", idx)
		args = append(args, q.Source)
		idx++
	}
	if q.Module != "" {
		where += fmt.Sprintf(" AND module = $%d", idx)
		args = append(args, q.Module)
		idx++
	}
	if q.Level != "" {
		where += fmt.Sprintf(" AND level = $%d", idx)
		args = append(args, strings.ToUpper(q.Level))
		idx++
	}
	if q.DeviceID != "" {
		where += fmt.Sprintf(" AND device_id = $%d", idx)
		args = append(args, q.DeviceID)
		idx++
	}
	if q.From != nil {
		where += fmt.Sprintf(" AND created_at >= $%d", idx)
		args = append(args, *q.From)
		idx++
	}
	if q.To != nil {
		where += fmt.Sprintf(" AND created_at <= $%d", idx)
		args = append(args, *q.To)
		idx++
	}

	var total int
	countSQL := "SELECT COUNT(*) FROM diagnostic_events WHERE " + where
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	offset := (q.Page - 1) * q.PageSize
	listSQL := fmt.Sprintf(`
		SELECT id, source, device_id, module, level, message, context, correlation_id, created_at
		FROM diagnostic_events
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, idx, idx+1)
	args = append(args, q.PageSize, offset)

	rows, err := s.db.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]models.DiagnosticEvent, 0, q.PageSize)
	for rows.Next() {
		var ev models.DiagnosticEvent
		var deviceID sql.NullString
		var ctxRaw sql.NullString
		var corr sql.NullString
		if err := rows.Scan(
			&ev.ID, &ev.Source, &deviceID, &ev.Module, &ev.Level, &ev.Message, &ctxRaw, &corr, &ev.CreatedAt,
		); err != nil {
			return nil, 0, err
		}
		if deviceID.Valid {
			if parsed, err := uuid.Parse(deviceID.String); err == nil {
				ev.DeviceID = &parsed
			}
		}
		if ctxRaw.Valid && strings.TrimSpace(ctxRaw.String) != "" {
			ev.Context = json.RawMessage(ctxRaw.String)
		}
		if corr.Valid {
			ev.CorrelationID = corr.String
		}
		out = append(out, ev)
	}
	return out, total, rows.Err()
}
// ---- Source: backend/services/diagnostic_snapshot.go ----
func (s *DiagnosticService) BuildSnapshot(ctx context.Context) (*models.DiagnosticSnapshot, error) {
	now := models.UTCNow()
	snap := &models.DiagnosticSnapshot{
		GeneratedAt: now,
		Runtime:     s.GetRuntimeConfig(),
		Events: models.DiagnosticEventStats{
			Last5Min:   map[string]int{},
			Last1Hour:  map[string]int{},
			ByModule5m: map[string]int{},
		},
		RemoteView: models.DiagnosticRVSnapshot{Sessions: []models.DiagnosticRemoteSessionSummary{}},
		Agents:     []models.DiagnosticAgentSummary{},
	}

	if s.wsHub != nil {
		adminCount, agentCount := s.wsHub.GetConnectionCounts()
		snap.WebSocket = models.DiagnosticWSSnapshot{
			AgentsOnline:     agentCount,
			AdminConnections: adminCount,
			TotalConnections: adminCount + agentCount,
		}
		online := s.wsHub.GetOnlineDevices()
		for _, deviceID := range online {
			agent := models.DiagnosticAgentSummary{DeviceID: deviceID, Online: true}
			if s.remoteView != nil {
				agent.StatsFresh = s.remoteView.hasFreshRemoteStats(deviceID)
				if st, err := s.remoteView.GetStatus(ctx, deviceID); err == nil && st != nil && st.IsActive {
					agent.SessionID = st.SessionID
					agent.SessionStatus = st.Status
					if st.SfuChecked {
						agent.Publishing = st.SfuPublishing && st.Status == models.RemoteViewStatusActive
					} else {
						agent.Publishing = st.StatsFresh && st.Status == models.RemoteViewStatusActive
					}
				}
			}
			if s.deviceRepo != nil {
				if id, err := uuid.Parse(deviceID); err == nil {
					if dev, err := s.deviceRepo.Get(ctx, id); err == nil && dev != nil {
						agent.Hostname = dev.Hostname
					}
				}
			}
			snap.Agents = append(snap.Agents, agent)
		}
	}

	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM diagnostic_events`).Scan(&snap.Events.TotalStored)
	s.fillEventStats(ctx, now.Add(-5*time.Minute), snap.Events.Last5Min, snap.Events.ByModule5m)
	s.fillEventStats(ctx, now.Add(-1*time.Hour), snap.Events.Last1Hour, nil)

	if s.remoteView != nil {
		sessions, err := s.remoteView.ListActivePublisherSessions(ctx)
		if err == nil {
			snap.RemoteView.ActiveSessions = len(sessions)
			for _, session := range sessions {
				if session == nil {
					continue
				}
				deviceID := session.DeviceID.String()
				item := models.DiagnosticRemoteSessionSummary{
					SessionID:   session.ID.String(),
					DeviceID:    deviceID,
					Status:      session.Status,
					ViewerCount: session.ViewerCount,
					IngressID:   session.IngressID,
					AgentOnline: s.wsHub != nil && s.wsHub.IsAgentOnline(deviceID),
					StatsFresh:  s.remoteView.hasFreshRemoteStats(deviceID),
					StartedAt:   models.FormatUTC(session.StartedAt),
					Stats:       s.remoteView.GetDeviceStats(deviceID),
				}
				if item.StatsFresh {
					snap.RemoteView.PublishingFresh++
				}
				if s.deviceRepo != nil {
					if dev, err := s.deviceRepo.Get(ctx, session.DeviceID); err == nil && dev != nil {
						item.Hostname = dev.Hostname
					}
				}
				snap.RemoteView.Sessions = append(snap.RemoteView.Sessions, item)
			}
		}
	}

	s.enrichSnapshot(ctx, snap)
	return snap, nil
}
func (s *DiagnosticService) fillEventStats(
	ctx context.Context,
	since time.Time,
	levelCounts map[string]int,
	moduleCounts map[string]int,
) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT level, module, COUNT(*)
		FROM diagnostic_events
		WHERE created_at >= $1
		GROUP BY level, module
	`, since)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var level, module string
		var count int
		if err := rows.Scan(&level, &module, &count); err != nil {
			continue
		}
		levelCounts[level] += count
		if moduleCounts != nil {
			moduleCounts[module] += count
		}
	}
}

// ---- Source: backend/services/mail.go ----
type MailService struct {
	enabled  bool
	host     string
	port     int
	user     string
	password string
	from     string
	fromName string
}

func (m *MailService) Enabled() bool {
	return m != nil && m.enabled
}
func (m *MailService) Send(_ context.Context, to, subject, body string) error {
	if m == nil || !m.enabled {
		return nil
	}
	to = strings.TrimSpace(to)
	if to == "" || !strings.Contains(to, "@") {
		return nil
	}

	fromHeader := m.from
	if m.fromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", encodeRFC2047(m.fromName), m.from)
	}

	msg := strings.Join([]string{
		fmt.Sprintf("From: %s", fromHeader),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", encodeRFC2047(subject)),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body,
	}, "\r\n")

	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	auth := smtp.PlainAuth("", m.user, m.password, m.host)

	if m.port == 465 {
		return m.sendTLS(addr, to, []byte(msg), auth)
	}
	return smtp.SendMail(addr, auth, m.from, []string{to}, []byte(msg))
}
func (m *MailService) sendTLS(addr, to string, msg []byte, auth smtp.Auth) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		ServerName: m.host,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return err
	}
	defer client.Close()

	if auth != nil && m.user != "" {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(m.from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
func encodeRFC2047(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		if r > 127 {
			return fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(value)))
		}
	}
	return value
}
// ---- Source: backend/services/media_token.go ----
const (
	mediaTokenKeyPrefix   = "media_access:"
	mediaTokenLeasePrefix = "media_access:lease:"
	mediaTokenTTL         = 15 * time.Minute
	mediaStreamLeaseTTL   = 15 * time.Minute
)

var (
	ErrMediaTokenInvalid = errorsPackage2.New("invalid media token")
	ErrMediaTokenExpired = errorsPackage2.New("media token expired")
)

// MediaAccessGrant 短期媒体访问授权（Session 签发的能力委托）
type MediaAccessGrant struct {
	SessionID        string   `json:"sessionId,omitempty"`
	UserID           string   `json:"userId"`
	Username         string   `json:"username"`
	Role             string   `json:"role,omitempty"`
	AllowedDeviceIDs []string `json:"allowedDeviceIds,omitempty"`
	ResourceType     string   `json:"resourceType"`
	ResourceID       string   `json:"resourceId"`
	Action           string   `json:"action"`
}
type MediaTokenService struct {
	redis    *redisPackage2.Client
	sessions *SessionService
}

func NewMediaTokenService(redisClient *redisPackage2.Client, sessions *SessionService) *MediaTokenService {
	return &MediaTokenService{redis: redisClient, sessions: sessions}
}
func (s *MediaTokenService) Issue(ctx context.Context, grant MediaAccessGrant) (string, time.Duration, error) {
	if grant.UserID == "" || grant.ResourceType == "" || grant.ResourceID == "" || grant.Action == "" {
		return "", 0, fmt.Errorf("invalid media grant")
	}
	if grant.SessionID == "" {
		return "", 0, fmt.Errorf("media grant requires session id")
	}

	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", 0, err
	}
	token := hex.EncodeToString(tokenBytes)

	payload, err := json.Marshal(grant)
	if err != nil {
		return "", 0, err
	}

	if s.redis != nil {
		if err := s.redis.Set(ctx, mediaTokenKeyPrefix+token, payload, mediaTokenTTL).Err(); err != nil {
			return "", 0, err
		}
		return token, mediaTokenTTL, nil
	}

	return "", 0, fmt.Errorf("media token store unavailable")
}
func (s *MediaTokenService) Validate(ctx context.Context, token, resourceType, resourceID, action string) (*MediaAccessGrant, error) {
	token = trimToken(token)
	if token == "" || s.redis == nil {
		return nil, ErrMediaTokenInvalid
	}

	// stream / view：优先走 lease（支持同一 token 多次请求，如缩略图+原图、浏览器重试）
	if action == "stream" || action == "view" {
		if grant, err := s.validateLease(ctx, token, resourceType, resourceID, action); err == nil {
			return grant, nil
		} else if err != ErrMediaTokenExpired {
			return nil, err
		}
	}

	// view：截图/缩略图可共用同一 token，在 TTL 内多次 GET，不一次性销毁
	if action == "view" {
		raw, err := s.redis.Get(ctx, mediaTokenKeyPrefix+token).Bytes()
		if err != nil {
			if err == redisPackage2.Nil {
				return nil, ErrMediaTokenExpired
			}
			return nil, err
		}
		grant, err := s.parseGrant(raw, resourceType, resourceID, action)
		if err != nil {
			return nil, err
		}
		if err := s.ensureActiveSession(ctx, grant); err != nil {
			return nil, err
		}
		_ = s.redis.Set(ctx, mediaTokenLeasePrefix+token, raw, mediaTokenTTL).Err()
		return grant, nil
	}

	raw, err := s.consumeToken(ctx, mediaTokenKeyPrefix+token)
	if err != nil {
		if err == redisPackage2.Nil {
			return nil, ErrMediaTokenExpired
		}
		return nil, err
	}

	grant, err := s.parseGrant(raw, resourceType, resourceID, action)
	if err != nil {
		return nil, err
	}

	if err := s.ensureActiveSession(ctx, grant); err != nil {
		return nil, err
	}

	if action == "stream" {
		_ = s.redis.Set(ctx, mediaTokenLeasePrefix+token, raw, mediaStreamLeaseTTL).Err()
	}

	return grant, nil
}
func (s *MediaTokenService) validateLease(ctx context.Context, token, resourceType, resourceID, action string) (*MediaAccessGrant, error) {
	raw, err := s.redis.Get(ctx, mediaTokenLeasePrefix+token).Bytes()
	if err != nil {
		if err == redisPackage2.Nil {
			return nil, ErrMediaTokenExpired
		}
		return nil, err
	}
	grant, err := s.parseGrant(raw, resourceType, resourceID, action)
	if err != nil {
		return nil, err
	}
	if err := s.ensureActiveSession(ctx, grant); err != nil {
		return nil, err
	}
	return grant, nil
}
func (s *MediaTokenService) parseGrant(raw []byte, resourceType, resourceID, action string) (*MediaAccessGrant, error) {
	var grant MediaAccessGrant
	if err := json.Unmarshal(raw, &grant); err != nil {
		return nil, ErrMediaTokenInvalid
	}
	if grant.ResourceType != resourceType || grant.ResourceID != resourceID || grant.Action != action {
		return nil, ErrMediaTokenInvalid
	}
	return &grant, nil
}
func (s *MediaTokenService) ensureActiveSession(ctx context.Context, grant *MediaAccessGrant) error {
	if grant.SessionID == "" || s.sessions == nil {
		return nil
	}
	if _, err := s.sessions.ValidateActiveSession(ctx, grant.SessionID); err != nil {
		return ErrMediaTokenExpired
	}
	return nil
}
func (s *MediaTokenService) consumeToken(ctx context.Context, key string) ([]byte, error) {
	raw, err := s.redis.GetDel(ctx, key).Bytes()
	if err == nil {
		return raw, nil
	}
	if err != redisPackage2.Nil && !isRedisUnknownCommand(err) {
		return nil, err
	}
	if err == redisPackage2.Nil {
		return nil, redisPackage2.Nil
	}
	raw, err = s.redis.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	if delErr := s.redis.Del(ctx, key).Err(); delErr != nil {
		return nil, delErr
	}
	return raw, nil
}
func isRedisUnknownCommand(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unknown command") || strings.Contains(msg, "ERR unknown")
}
func trimToken(token string) string {
	for len(token) > 0 && (token[0] == ' ' || token[0] == '\t') {
		token = token[1:]
	}
	for len(token) > 0 {
		last := token[len(token)-1]
		if last == ' ' || last == '\t' {
			token = token[:len(token)-1]
			continue
		}
		break
	}
	return token
}

// ---- Source: backend/services/metrics.go ----
// AppMetrics holds lightweight counters for /metrics exposition.
type AppMetrics struct {
	HTTPRequests            atomic.Int64
	HTTP5xx                 atomic.Int64
	UploadChunks            atomic.Int64
	UploadCompletes         atomic.Int64
	UploadFailures          atomic.Int64
	SessionRevokes          atomic.Int64
	WSAdminConns            atomic.Int64
	WSAgentConns            atomic.Int64
	WSDropsTotal            atomic.Int64
	WSDropsRevoke           atomic.Int64
	WSDropsGuard            atomic.Int64
	SessionValidationCount  atomic.Int64
	SessionValidationNanos  atomic.Int64
	DeploymentDriftDetected atomic.Int64
	startedAt               time.Time
}

var GlobalMetrics = &AppMetrics{startedAt: time.Now().UTC()}

func (m *AppMetrics) ObserveHTTP(status int) {
	m.HTTPRequests.Add(1)
	if status >= 500 {
		m.HTTP5xx.Add(1)
	}
}
func (m *AppMetrics) ObserveSessionValidation(d time.Duration) {
	m.SessionValidationCount.Add(1)
	m.SessionValidationNanos.Add(d.Nanoseconds())
}
func (m *AppMetrics) ObserveWSDrop(reason string) {
	m.WSDropsTotal.Add(1)
	switch reason {
	case "revoke", "logout", "session_revoked", "user_logout":
		m.WSDropsRevoke.Add(1)
	case "guard", "session_invalid":
		m.WSDropsGuard.Add(1)
	}
}
func (m *AppMetrics) Handler() httpPackage2.Handler {
	return httpPackage2.HandlerFunc(func(w httpPackage2.ResponseWriter, r *httpPackage2.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		uptime := int64(time.Since(m.startedAt).Seconds())
		writeMetric(w, "monitorwin_uptime_seconds", "Process uptime seconds", uptime)
		writeMetric(w, "monitorwin_http_requests_total", "Total HTTP requests", m.HTTPRequests.Load())
		writeMetric(w, "monitorwin_http_5xx_total", "Total HTTP 5xx responses", m.HTTP5xx.Load())
		writeMetric(w, "monitorwin_upload_chunks_total", "Upload chunks stored", m.UploadChunks.Load())
		writeMetric(w, "monitorwin_upload_complete_total", "Successful upload completions", m.UploadCompletes.Load())
		writeMetric(w, "monitorwin_upload_failures_total", "Failed upload completions", m.UploadFailures.Load())
		writeMetric(w, "monitorwin_upload_success_rate_percent", "Upload success rate percent", m.uploadSuccessRatePercent())
		writeMetric(w, "monitorwin_session_revokes_total", "Session revoke events handled", m.SessionRevokes.Load())
		writeMetric(w, "monitorwin_ws_admin_connections", "Current admin WS connections", m.WSAdminConns.Load())
		writeMetric(w, "monitorwin_ws_agent_connections", "Current agent WS connections", m.WSAgentConns.Load())
		writeMetric(w, "monitorwin_ws_connection_drops_total", "Total WS connections dropped", m.WSDropsTotal.Load())
		writeMetric(w, "monitorwin_ws_connection_drops_revoke_total", "WS drops due to session revoke", m.WSDropsRevoke.Load())
		writeMetric(w, "monitorwin_ws_connection_drops_guard_total", "WS drops due to session guard", m.WSDropsGuard.Load())
		writeMetric(w, "monitorwin_ws_connection_drop_rate_percent", "WS drop rate percent at scrape time", m.wsDropRatePercent())
		writeMetric(w, "monitorwin_session_validation_count_total", "Session validation calls", m.SessionValidationCount.Load())
		writeMetric(w, "monitorwin_session_validation_latency_ms_avg", "Average session validation latency ms", m.sessionValidationLatencyMsAvg())
		writeMetric(w, "monitorwin_deployment_drift_detected", "1 when cluster config drift detected", m.DeploymentDriftDetected.Load())
	})
}
func (m *AppMetrics) uploadSuccessRatePercent() int64 {
	completes := m.UploadCompletes.Load()
	failures := m.UploadFailures.Load()
	attempts := completes + failures
	if attempts == 0 {
		return 100
	}
	return completes * 100 / attempts
}
func (m *AppMetrics) wsDropRatePercent() int64 {
	drops := m.WSDropsTotal.Load()
	open := m.WSAdminConns.Load() + m.WSAgentConns.Load()
	denominator := drops + open
	if denominator == 0 {
		return 0
	}
	return drops * 100 / denominator
}
func (m *AppMetrics) sessionValidationLatencyMsAvg() int64 {
	count := m.SessionValidationCount.Load()
	if count == 0 {
		return 0
	}
	return m.SessionValidationNanos.Load() / count / int64(time.Millisecond)
}
func writeMetric(w httpPackage2.ResponseWriter, name, help string, value int64) {
	_, _ = w.Write([]byte("# HELP " + name + " " + help + "\n"))
	_, _ = w.Write([]byte("# TYPE " + name + " counter\n"))
	_, _ = w.Write([]byte(name + " " + strconv.FormatInt(value, 10) + "\n"))
}

// ---- Source: backend/services/penalty_push.go ----
type PenaltyPushService struct {
	db *sql.DB
}

func NewPenaltyPushService(db *sql.DB) *PenaltyPushService {
	return &PenaltyPushService{db: db}
}

type PenaltyPushSettings struct {
	HasBotToken bool   `json:"hasBotToken"`
	BotUsername string `json:"botUsername"`
	ChatID      string `json:"chatId"`
	ChatTitle   string `json:"chatTitle"`
	ChatType    string `json:"chatType"`
	Enabled     bool   `json:"enabled"`
	Ready       bool   `json:"ready"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

func penaltyPushReady(hasBotToken bool, chatID string, enabled bool) bool {
	return enabled && hasBotToken && strings.TrimSpace(chatID) != ""
}

type PenaltyPushRecord struct {
	Date         string  `json:"date"`
	EmployeeID   string  `json:"employeeId"`
	EmployeeName string  `json:"employeeName"`
	Position     string  `json:"position"`
	Amount       float64 `json:"amount"`
	Category     string  `json:"category"`
	Reason       string  `json:"reason"`
}

func (s *PenaltyPushService) GetSettings(ctx context.Context) (*PenaltyPushSettings, error) {
	out := &PenaltyPushSettings{}
	var tokenEnc, botUsername, chatID, chatTitle, chatType sql.NullString
	var botID sql.NullInt64
	var enabled bool
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT bot_token_enc, bot_username, bot_id, chat_id, chat_title, chat_type, enabled, updated_at
		FROM penalty_push_settings WHERE id = 1`).
		Scan(&tokenEnc, &botUsername, &botID, &chatID, &chatTitle, &chatType, &enabled, &updatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.HasBotToken = tokenEnc.Valid && strings.TrimSpace(tokenEnc.String) != ""
	if botUsername.Valid {
		out.BotUsername = botUsername.String
	}
	if chatID.Valid {
		out.ChatID = chatID.String
	}
	if chatTitle.Valid {
		out.ChatTitle = chatTitle.String
	}
	if chatType.Valid {
		out.ChatType = chatType.String
	}
	out.Enabled = enabled
	out.Ready = penaltyPushReady(out.HasBotToken, out.ChatID, enabled)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return out, nil
}
func (s *PenaltyPushService) SetPushEnabled(ctx context.Context, enabled bool) (*PenaltyPushSettings, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO penalty_push_settings (id, enabled, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			updated_at = NOW()`, enabled)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PenaltyPushService) SaveBotToken(ctx context.Context, token string) (*PenaltyPushSettings, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("Bot Token 不能为空")
	}
	username, botID, err := performanceTelegramGetMe(token)
	if err != nil {
		return nil, err
	}
	enc, err := EncryptSecret(token)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO penalty_push_settings (id, bot_token_enc, bot_username, bot_id, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			bot_token_enc = EXCLUDED.bot_token_enc,
			bot_username = EXCLUDED.bot_username,
			bot_id = EXCLUDED.bot_id,
			updated_at = NOW()`, enc, username, botID)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PenaltyPushService) DiscoverChats(ctx context.Context) ([]map[string]string, error) {
	token, err := s.getBotToken(ctx)
	if err != nil {
		return nil, err
	}
	return DiscoverTelegramChats(ctx, s.db, token)
}
func (s *PenaltyPushService) BindChat(ctx context.Context, chatID, chatTitle, chatType string) (*PenaltyPushSettings, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("chatId required")
	}
	if _, err := s.getBotToken(ctx); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(chatTitle)
	if title == "" {
		title = chatID
	}
	chatType = strings.TrimSpace(chatType)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO penalty_push_settings (id, chat_id, chat_title, chat_type, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			chat_id = EXCLUDED.chat_id,
			chat_title = EXCLUDED.chat_title,
			chat_type = EXCLUDED.chat_type,
			updated_at = NOW()`, chatID, title, chatType)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PenaltyPushService) UnbindChat(ctx context.Context) (*PenaltyPushSettings, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE penalty_push_settings
		SET chat_id = NULL, chat_title = NULL, chat_type = NULL, updated_at = NOW()
		WHERE id = 1`)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PenaltyPushService) PushPenaltyRecord(ctx context.Context, rec PenaltyPushRecord) (bool, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	if !settings.Ready {
		return false, nil
	}
	token, err := s.getBotToken(ctx)
	if err != nil {
		return false, err
	}
	tgUID, username := lookupEmployeeTelegramProfile(ctx, s.db, rec.EmployeeID, rec.EmployeeName)
	text := buildPenaltyPushHTML(rec, tgUID, username, s.FineCurrencyCode(ctx))
	if err := performanceTelegramSendHTML(token, settings.ChatID, text); err != nil {
		return false, err
	}
	return true, nil
}
func (s *PenaltyPushService) TestPush(ctx context.Context) error {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if settings.ChatID == "" {
		return fmt.Errorf("请先绑定频道或群组")
	}
	token, err := s.getBotToken(ctx)
	if err != nil {
		return err
	}
	text := "⚠️ <b>罚款推送测试</b>\n" + tgReplyDashedLine() + "\n配置成功，后续新增罚款将使用可点击姓名、可复制金额的模版推送到此频道/群组。"
	return performanceTelegramSendHTML(token, settings.ChatID, text)
}
func (s *PenaltyPushService) getBotToken(ctx context.Context) (string, error) {
	var enc sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT bot_token_enc FROM penalty_push_settings WHERE id = 1`).Scan(&enc)
	if err == sql.ErrNoRows || !enc.Valid || strings.TrimSpace(enc.String) == "" {
		return "", fmt.Errorf("请先配置 Bot Token")
	}
	if err != nil {
		return "", err
	}
	return DecryptSecret(enc.String)
}

// FineCurrencyCode reads TG settings fine currency (display only; default CNY).
func (s *PenaltyPushService) FineCurrencyCode(ctx context.Context) string {
	var code string
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(NULLIF(TRIM(fine_currency), ''), 'CNY') FROM wf_telegram_settings WHERE id = 1`,
	).Scan(&code)
	if err != nil {
		return "CNY"
	}
	return NormalizeFineCurrency(code)
}
func buildPenaltyPushHTML(rec PenaltyPushRecord, telegramUserID int64, telegramUsername, fineCurrency string) string {
	return fmt.Sprintf(
		"⚠️ <b>罚款通知</b>\n"+
			"%s\n"+
			"👤 员工：%s\n"+
			"📅 日期：%s\n"+
			"💼 岗位：%s\n"+
			"💰 金额：%s\n"+
			"📂 类别：%s\n"+
			"📝 原因：%s",
		tgReplyDashedLine(),
		formatEmployeeProfileHTML(telegramUserID, telegramUsername, rec.EmployeeName),
		tgCopyableText(rec.Date),
		tgCopyableText(rec.Position),
		tgCopyableText(FormatFineMoney(rec.Amount, fineCurrency)),
		tgCopyableText(rec.Category),
		tgCopyableText(formatPenaltyReasonDuration(rec.Reason)),
	)
}
func lookupEmployeeTelegramUserID(ctx context.Context, db *sql.DB, employeeID, employeeName string) int64 {
	userID, _ := lookupEmployeeTelegramProfile(ctx, db, employeeID, employeeName)
	return userID
}
func lookupEmployeeTelegramProfile(ctx context.Context, db *sql.DB, employeeID, employeeName string) (userID int64, username string) {
	if db == nil {
		return 0, ""
	}
	scan := func(query string, args ...interface{}) bool {
		var id int64
		var user sql.NullString
		if err := db.QueryRowContext(ctx, query, args...).Scan(&id, &user); err != nil || id <= 0 {
			return false
		}
		userID = id
		username = strings.TrimSpace(user.String)
		return true
	}
	if id, err := uuid.Parse(strings.TrimSpace(employeeID)); err == nil && id != uuid.Nil {
		if scan(`
			SELECT telegram_user_id, COALESCE(telegram_username, '')
			FROM employee_telegram_bindings
			WHERE employee_id = $1 AND status = 1
			ORDER BY bound_at DESC NULLS LAST LIMIT 1`, id) {
			return userID, username
		}
		if scan(`
			SELECT b.telegram_user_id, COALESCE(b.telegram_username, '')
			FROM device_telegram_bindings b
			JOIN devices d ON d.id = b.device_id
			WHERE d.employee_uuid = $1 AND b.status = 1
			ORDER BY b.bound_at DESC NULLS LAST LIMIT 1`, id) {
			return userID, username
		}
		if scan(`
			SELECT b.telegram_user_id, COALESCE(b.telegram_username, '')
			FROM device_telegram_bindings b
			JOIN wf_shared_workstation_members m ON m.device_id = b.device_id
			WHERE m.employee_id = $1 AND b.status = 1
			ORDER BY b.bound_at DESC NULLS LAST LIMIT 1`, id) {
			return userID, username
		}
		if scan(`
			SELECT b.telegram_user_id, COALESCE(b.telegram_username, '')
			FROM work_sessions ws
			JOIN device_telegram_bindings b ON b.device_id = ws.device_id AND b.status = 1
			WHERE ws.employee_id = $1 AND ws.status = 'open' AND ws.device_id IS NOT NULL
			ORDER BY ws.checkin_time DESC LIMIT 1`, id) {
			return userID, username
		}
	}
	name := strings.TrimSpace(employeeName)
	if name == "" {
		return 0, ""
	}
	if scan(`
		SELECT b.telegram_user_id, COALESCE(b.telegram_username, '')
		FROM employee_telegram_bindings b
		JOIN employees e ON e.id = b.employee_id
		WHERE e.name = $1 AND b.status = 1
		ORDER BY b.bound_at DESC NULLS LAST LIMIT 1`, name) {
		return userID, username
	}
	scan(`
		SELECT b.telegram_user_id, COALESCE(b.telegram_username, '')
		FROM device_telegram_bindings b
		JOIN devices d ON d.id = b.device_id
		WHERE d.employee_name = $1 AND b.status = 1
		ORDER BY b.bound_at DESC NULLS LAST LIMIT 1`, name)
	return userID, username
}

// formatPenaltyReasonDuration converts trailing "N 秒" in reason text to 分/秒 display.
func formatPenaltyReasonDuration(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return reason
	}
	const suffix = " 秒"
	if !strings.HasSuffix(reason, suffix) {
		return reason
	}
	body := strings.TrimSuffix(reason, suffix)
	idx := strings.LastIndex(body, " ")
	if idx < 0 {
		return reason
	}
	numPart := strings.TrimSpace(body[idx+1:])
	prefix := strings.TrimSpace(body[:idx])
	var sec int
	if _, err := fmt.Sscanf(numPart, "%d", &sec); err != nil || sec < 0 {
		return reason
	}
	if prefix == "" {
		return formatDurationZh(sec)
	}
	return prefix + " " + formatDurationZh(sec)
}

// ---- Source: backend/services/performance_push.go ----
type PerformancePushService struct {
	db *sql.DB
}

func NewPerformancePushService(db *sql.DB) *PerformancePushService {
	return &PerformancePushService{db: db}
}

type PerformancePushSettings struct {
	HasBotToken bool   `json:"hasBotToken"`
	BotUsername string `json:"botUsername"`
	ChatID      string `json:"chatId"`
	ChatTitle   string `json:"chatTitle"`
	ChatType    string `json:"chatType"`
	Enabled     bool   `json:"enabled"`
	Ready       bool   `json:"ready"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

func performancePushReady(hasBotToken bool, chatID string, enabled bool) bool {
	return enabled && hasBotToken && strings.TrimSpace(chatID) != ""
}

type PerformancePushRecord struct {
	Date         string `json:"date"`
	EmployeeID   string `json:"employeeId"`
	EmployeeName string `json:"employeeName"`
	Position     string `json:"position"`
	Score        int    `json:"score"`
	Reason       string `json:"reason"`
}

func (s *PerformancePushService) GetSettings(ctx context.Context) (*PerformancePushSettings, error) {
	out := &PerformancePushSettings{}
	var tokenEnc, botUsername, chatID, chatTitle, chatType sql.NullString
	var botID sql.NullInt64
	var enabled bool
	var updatedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT bot_token_enc, bot_username, bot_id, chat_id, chat_title, chat_type, enabled, updated_at
		FROM performance_push_settings WHERE id = 1`).
		Scan(&tokenEnc, &botUsername, &botID, &chatID, &chatTitle, &chatType, &enabled, &updatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.HasBotToken = tokenEnc.Valid && strings.TrimSpace(tokenEnc.String) != ""
	if botUsername.Valid {
		out.BotUsername = botUsername.String
	}
	if chatID.Valid {
		out.ChatID = chatID.String
	}
	if chatTitle.Valid {
		out.ChatTitle = chatTitle.String
	}
	if chatType.Valid {
		out.ChatType = chatType.String
	}
	out.Enabled = enabled
	out.Ready = performancePushReady(out.HasBotToken, out.ChatID, enabled)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	return out, nil
}
func (s *PerformancePushService) SetPushEnabled(ctx context.Context, enabled bool) (*PerformancePushSettings, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO performance_push_settings (id, enabled, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			updated_at = NOW()`, enabled)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PerformancePushService) SaveBotToken(ctx context.Context, token string) (*PerformancePushSettings, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("Bot Token 不能为空")
	}
	username, botID, err := performanceTelegramGetMe(token)
	if err != nil {
		return nil, err
	}
	enc, err := EncryptSecret(token)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO performance_push_settings (id, bot_token_enc, bot_username, bot_id, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			bot_token_enc = EXCLUDED.bot_token_enc,
			bot_username = EXCLUDED.bot_username,
			bot_id = EXCLUDED.bot_id,
			updated_at = NOW()`, enc, username, botID)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PerformancePushService) DiscoverChats(ctx context.Context) ([]map[string]string, error) {
	token, err := s.getBotToken(ctx)
	if err != nil {
		return nil, err
	}
	return DiscoverTelegramChats(ctx, s.db, token)
}
func (s *PerformancePushService) BindChat(ctx context.Context, chatID, chatTitle, chatType string) (*PerformancePushSettings, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("chatId required")
	}
	if _, err := s.getBotToken(ctx); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(chatTitle)
	if title == "" {
		title = chatID
	}
	chatType = strings.TrimSpace(chatType)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO performance_push_settings (id, chat_id, chat_title, chat_type, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			chat_id = EXCLUDED.chat_id,
			chat_title = EXCLUDED.chat_title,
			chat_type = EXCLUDED.chat_type,
			updated_at = NOW()`, chatID, title, chatType)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PerformancePushService) UnbindChat(ctx context.Context) (*PerformancePushSettings, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE performance_push_settings
		SET chat_id = NULL, chat_title = NULL, chat_type = NULL, updated_at = NOW()
		WHERE id = 1`)
	if err != nil {
		return nil, err
	}
	return s.GetSettings(ctx)
}
func (s *PerformancePushService) PushScoreRecord(ctx context.Context, rec PerformancePushRecord) (bool, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	if !settings.Ready {
		return false, nil
	}
	token, err := s.getBotToken(ctx)
	if err != nil {
		return false, err
	}
	tgUID, username := lookupEmployeeTelegramProfile(ctx, s.db, rec.EmployeeID, rec.EmployeeName)
	text := buildPerformancePushHTML(rec, tgUID, username)
	if err := performanceTelegramSendHTML(token, settings.ChatID, text); err != nil {
		return false, err
	}
	return true, nil
}
func (s *PerformancePushService) TestPush(ctx context.Context) error {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if settings.ChatID == "" {
		return fmt.Errorf("请先绑定频道或群组")
	}
	token, err := s.getBotToken(ctx)
	if err != nil {
		return err
	}
	text := "📋 <b>绩效推送测试</b>\n" + tgReplyDashedLine() + "\n配置成功，后续新增绩效将使用可点击姓名、可复制分数的模版推送到此频道/群组。"
	return performanceTelegramSendHTML(token, settings.ChatID, text)
}
func (s *PerformancePushService) getBotToken(ctx context.Context) (string, error) {
	var enc sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT bot_token_enc FROM performance_push_settings WHERE id = 1`).Scan(&enc)
	if err == sql.ErrNoRows || !enc.Valid || strings.TrimSpace(enc.String) == "" {
		return "", fmt.Errorf("请先配置 Bot Token")
	}
	if err != nil {
		return "", err
	}
	return DecryptSecret(enc.String)
}
func buildPerformancePushHTML(rec PerformancePushRecord, telegramUserID int64, telegramUsername string) string {
	changeType := "加分"
	scoreText := fmt.Sprintf("+%d分", rec.Score)
	if rec.Score < 0 {
		changeType = "扣分"
		scoreText = fmt.Sprintf("%d分", rec.Score)
	}
	return fmt.Sprintf(
		"📋 <b>绩效变动通知</b>\n"+
			"%s\n"+
			"👤 员工：%s\n"+
			"📅 日期：%s\n"+
			"💼 岗位：%s\n"+
			"📊 变动：%s（%s）\n"+
			"📝 原因：%s",
		tgReplyDashedLine(),
		formatEmployeeProfileHTML(telegramUserID, telegramUsername, rec.EmployeeName),
		tgCopyableText(rec.Date),
		tgCopyableText(rec.Position),
		tgCopyableText(scoreText),
		html.EscapeString(changeType),
		tgCopyableText(rec.Reason),
	)
}
func performanceTelegramGetMe(token string) (string, int64, error) {
	client := &httpPackage2.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token))
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, err
	}
	if !out.OK {
		if out.Description == "" {
			out.Description = "getMe failed"
		}
		return "", 0, fmt.Errorf("%s", out.Description)
	}
	return out.Result.Username, out.Result.ID, nil
}
func performanceTelegramSendHTML(token, chatID, htmlText string) error {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       htmlText,
		"parse_mode": "HTML",
	}
	body, _ := json.Marshal(payload)
	resp, err := httpPackage2.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token),
		"application/json",
		bytesPackage2.NewReader(body),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if !parsed.OK {
		if parsed.Description == "" {
			parsed.Description = string(raw)
		}
		return fmt.Errorf("telegram sendMessage: %s", parsed.Description)
	}
	return nil
}

// ---- Source: backend/services/policy_repository.go ----
// PolicyRepository 策略仓库
type PolicyRepository struct {
	db *sql.DB
}

// Get 根据ID获取策略
func (r *PolicyRepository) Get(ctx context.Context, id uuid.UUID) (*models.Policy, error) {
	var p models.Policy
	var updatedAt sql.NullTime
	query := `SELECT id, name, version, content, signature, status, is_current, created_at, updated_at FROM policies WHERE id = $1`
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Name, &p.Version, &p.Content, &p.Signature, &p.Status, &p.IsCurrent, &p.CreatedAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}
	if updatedAt.Valid {
		p.UpdatedAt = updatedAt.Time
	}
	return &p, nil
}

// Delete 删除策略
func (r *PolicyRepository) Delete(ctx context.Context, id uuid.UUID) error {
	// 检查是否还有策略
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM policies`).Scan(&count)
	if err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf("cannot delete the last policy")
	}

	// 如果要删除的是当前策略，先将其他策略设为当前
	var isCurrent bool
	err = r.db.QueryRowContext(ctx, `SELECT is_current FROM policies WHERE id = $1`, id).Scan(&isCurrent)
	if err != nil {
		return err
	}

	if isCurrent {
		// 找到另一个策略设为当前
		var anotherID uuid.UUID
		err = r.db.QueryRowContext(ctx, `SELECT id FROM policies WHERE id != $1 LIMIT 1`, id).Scan(&anotherID)
		if err != nil {
			return err
		}
		if err := r.SetCurrent(ctx, &models.Policy{ID: anotherID}); err != nil {
			return err
		}
	}

	_, err = r.db.ExecContext(ctx, `DELETE FROM policies WHERE id = $1`, id)
	return err
}

// SetCurrent 设置当前策略
func (r *PolicyRepository) SetCurrent(ctx context.Context, policy *models.Policy) error {
	logPackage2.Printf("🔵 SetCurrent called: policy.ID=%s, policy.Name=%s", policy.ID, policy.Name)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		logPackage2.Printf("❌ BeginTx error: %v", err)
		return err
	}
	defer tx.Rollback()

	// 清除当前策略标记
	result1, err := tx.ExecContext(ctx, `UPDATE policies SET is_current = false`)
	if err != nil {
		logPackage2.Printf("❌ Clear current error: %v", err)
		return err
	}
	rows1, _ := result1.RowsAffected()
	logPackage2.Printf("✅ Cleared is_current for %d policies", rows1)

	// 设置新策略为当前
	result2, err := tx.ExecContext(ctx, `UPDATE policies SET is_current = true, status = 1 WHERE id = $1`, policy.ID)
	if err != nil {
		logPackage2.Printf("❌ Set current error: %v", err)
		return err
	}
	rows2, _ := result2.RowsAffected()
	logPackage2.Printf("✅ Set is_current=true for %d rows (expected 1)", rows2)

	if err := tx.Commit(); err != nil {
		logPackage2.Printf("❌ Commit error: %v", err)
		return err
	}

	logPackage2.Printf("✅ SetCurrent completed successfully")
	return nil
}

// ---- Source: backend/services/remote_view_lifecycle_diag.go ----
func (s *RemoteViewService) lifecycleDiag(phase, sessionID, detail string) {
	if s == nil || !s.cfg.LifecycleDiagEnabled {
		return
	}
	logPackage2.Printf(
		"[RemoteViewLifecycleDiag] ts=%s phase=%s session=%s detail=%s",
		time.Now().UTC().Format(time.RFC3339Nano),
		phase,
		normalizeLifecycleDiagSession(sessionID),
		detail,
	)
}
func normalizeLifecycleDiagSession(sessionID string) string {
	if sessionID == "" {
		return "-"
	}
	return sessionID
}

func isIngressNotFound(statusCode int, body string) bool {
	if statusCode == httpStatusNotFound {
		return true
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "not_found") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "ingress not found")
}

const httpStatusNotFound = 404

// ---- Source: backend/services/remote_view_service.go ----
const (
	remoteViewStartAgentDelay  = 50 * time.Millisecond
	remoteViewIngressReadyMin  = 120 * time.Millisecond
	remoteViewIngressReadyStep = 80 * time.Millisecond
	remoteViewIngressReadyMax  = 420 * time.Millisecond
	remoteViewFreshStartSettle = 200 * time.Millisecond
	// Keep WHIP/RTMP ingress alive long enough for Agent reconnect / fallback;
	// 2s was deleting stream keys while FFmpeg still POSTed → "ingress does not exist".
	remoteViewIngressDeleteDelay = 15 * time.Second
	// Wipe SFU room shortly after last viewer leaves so the next start does not
	// subscribe to a ghost Ingress participant (frozen / non-realtime frame).
	remoteViewRoomDeleteDelay = 1500 * time.Millisecond
	// Reuse an active session when agent stats arrived recently (covers publisher grace window).
	remoteViewStatsStaleBuffer = 15 * time.Second
	sfuProbeCacheTTL           = 2 * time.Second
)

type publisherGraceStop struct {
	timer *time.Timer
	gen   uint64
}
type remoteViewStatsEntry struct {
	stats    *models.RemoteViewStatsPayload
	received time.Time
}
type RemoteViewService struct {
	cfg             configPackage2.LiveKitConfig
	repo            *RemoteViewRepository
	deviceRepo      *DeviceRepository
	policyRepo      *PolicyRepository
	wsHub           *WebSocketHub
	tokens          *LiveKitTokenService
	ingress         *liveKitIngressClient
	statsMu         sync.RWMutex
	deviceStats     map[string]*remoteViewStatsEntry
	startLocks      sync.Map
	graceMu         sync.Mutex
	graceStops      map[string]*publisherGraceStop
	graceGenCounter uint64 // atomic; invalidates in-flight AfterFunc callbacks
	sfuProbeMu      sync.Mutex
	sfuProbe        map[string]sfuProbeCache
}
type sfuProbeCache struct {
	checked    bool
	publishing bool
	at         time.Time
}

func (s *RemoteViewService) GetActiveSession(ctx context.Context, deviceID uuid.UUID) (*models.RemoteViewSession, error) {
	return s.repo.GetActiveByDevice(ctx, deviceID)
}
func (s *RemoteViewService) ListActivePublisherSessions(ctx context.Context) ([]*models.RemoteViewSession, error) {
	return s.repo.ListActivePublisherSessions(ctx)
}
func (s *RemoteViewService) GetDeviceStats(deviceID string) *models.RemoteViewStatsPayload {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	entry := s.deviceStats[deviceID]
	if entry == nil {
		return nil
	}
	return entry.stats
}
func (s *RemoteViewService) statsStaleAfter() time.Duration {
	grace := s.cfg.PublisherGrace
	if grace <= 0 {
		return 20 * time.Second
	}
	return grace + remoteViewStatsStaleBuffer
}
func (s *RemoteViewService) hasFreshRemoteStats(deviceID string) bool {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	entry := s.deviceStats[deviceID]
	if entry == nil || entry.stats == nil {
		return false
	}
	return time.Since(entry.received) <= s.statsStaleAfter()
}

func (s *RemoteViewService) probeSfuPublisher(ctx context.Context, session *models.RemoteViewSession) sfuProbeCache {
	if session == nil || s.ingress == nil {
		return sfuProbeCache{}
	}
	key := session.ID.String()
	s.sfuProbeMu.Lock()
	if cached, ok := s.sfuProbe[key]; ok && time.Since(cached.at) < sfuProbeCacheTTL {
		s.sfuProbeMu.Unlock()
		return cached
	}
	s.sfuProbeMu.Unlock()

	result := sfuProbeCache{checked: true, at: time.Now()}
	identity := strings.TrimSpace(session.PublisherIdentity)
	if identity == "" {
		identity = publisherIdentityForSession(session.DeviceID.String(), session.ID)
	}

	roomLive, roomErr := s.ingress.PublisherHasLiveVideo(ctx, session.RoomName, identity)
	ingressLive := false
	var ingressErr error
	if session.IngressID != "" {
		ingressLive, ingressErr = s.ingress.IngressIsPublishing(ctx, session.IngressID)
	}
	if roomErr != nil && (session.IngressID == "" || ingressErr != nil) {
		if roomErr != nil {
			logPackage2.Printf("[RemoteView] SFU ListParticipants probe failed session=%s: %v", session.ID, roomErr)
		}
		if ingressErr != nil {
			logPackage2.Printf("[RemoteView] SFU ListIngress probe failed session=%s: %v", session.ID, ingressErr)
		}
		result.checked = false
	} else {
		result.publishing = roomLive || ingressLive
	}

	s.sfuProbeMu.Lock()
	s.sfuProbe[key] = result
	s.sfuProbeMu.Unlock()
	return result
}
func (s *RemoteViewService) cancelPublisherGraceStop(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	s.graceMu.Lock()
	defer s.graceMu.Unlock()
	if entry, ok := s.graceStops[sessionID]; ok && entry != nil && entry.timer != nil {
		entry.timer.Stop()
	}
	delete(s.graceStops, sessionID)
}
func (s *RemoteViewService) GetStatus(ctx context.Context, deviceIDStr string) (*models.RemoteViewStatusResponse, error) {
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid device id")
	}

	s.expireStaleStartingIfNeeded(ctx, deviceID)

	session, err := s.repo.GetActiveByDevice(ctx, deviceID)
	if err != nil {
		return &models.RemoteViewStatusResponse{IsActive: false}, nil
	}

	resp := &models.RemoteViewStatusResponse{
		IsActive:           true,
		SessionID:          session.ID.String(),
		Status:             session.Status,
		ViewerCount:        session.ViewerCount,
		StartedAt:          models.FormatUTC(session.StartedAt),
		UpdatedAt:          models.FormatUTC(session.UpdatedAt),
		StartingTimeoutSec: int(s.cfg.StartingTimeout / time.Second),
		StatsFresh:         s.hasFreshRemoteStats(deviceIDStr),
	}
	if resp.StartingTimeoutSec <= 0 {
		resp.StartingTimeoutSec = 90
	}
	if resp.StatsFresh {
		resp.Stats = s.GetDeviceStats(deviceIDStr)
	}
	probe := s.probeSfuPublisher(ctx, session)
	resp.SfuChecked = probe.checked
	resp.SfuPublishing = probe.publishing
	return resp, nil
}
func (s *RemoteViewService) expireStaleStartingIfNeeded(ctx context.Context, deviceID uuid.UUID) {
	unlock := s.lockDeviceStart(deviceID.String())
	defer unlock()
	s.expireStaleStartingLocked(ctx, deviceID)
}

// expireStaleStartingLocked assumes lockDeviceStart(deviceID) is already held.
func (s *RemoteViewService) expireStaleStartingLocked(ctx context.Context, deviceID uuid.UUID) {
	session, err := s.repo.GetActiveByDevice(ctx, deviceID)
	if err != nil || session == nil {
		return
	}
	if session.Status != models.RemoteViewStatusStarting {
		return
	}
	if time.Since(session.UpdatedAt) < s.cfg.StartingTimeout {
		return
	}

	logPackage2.Printf("[RemoteView] expiring stale starting session %s for device %s", session.ID, deviceID)
	s.cancelPublisherGraceStop(session.ID.String())
	if s.ingress != nil && session.IngressID != "" {
		_ = s.ingress.DeleteIngress(ctx, session.IngressID)
	}
	s.scheduleRoomDelete(session.RoomName)
	_ = s.repo.MarkFailed(ctx, session.ID, "starting_timeout")
	s.statsMu.Lock()
	delete(s.deviceStats, deviceID.String())
	s.statsMu.Unlock()
	s.wsHub.SendCommandToDevice(deviceID.String(), "stop_remote_view", gin.H{
		"sessionId": session.ID.String(),
	})
}

// forceStopSessionLocked tears down an active/starting session while startLocks is held.
func (s *RemoteViewService) forceStopSessionLocked(ctx context.Context, session *models.RemoteViewSession, reason string) {
	if session == nil {
		return
	}
	s.lifecycleDiag(
		"session-force-stop",
		session.ID.String(),
		"reason="+reason+" ingressId="+session.IngressID+" room="+session.RoomName+" status="+session.Status,
	)
	s.cancelPublisherGraceStop(session.ID.String())
	deviceID := session.DeviceID.String()
	s.wsHub.SendCommandToDevice(deviceID, "stop_remote_view", gin.H{
		"sessionId": session.ID.String(),
	})
	if s.ingress != nil && session.IngressID != "" {
		s.scheduleIngressDelete(session.IngressID)
	}
	s.scheduleRoomDelete(session.RoomName)
	_ = s.repo.MarkStopped(ctx, session.ID, reason)
	s.statsMu.Lock()
	delete(s.deviceStats, deviceID)
	s.statsMu.Unlock()
}
func (s *RemoteViewService) lockDeviceStart(deviceID string) func() {
	v, _ := s.startLocks.LoadOrStore(deviceID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
func publisherIdentityForSession(deviceID string, sessionID uuid.UUID) string {
	short := strings.ReplaceAll(sessionID.String(), "-", "")
	if len(short) > 8 {
		short = short[:8]
	}
	return "agent-" + deviceID + "-" + short
}
// scheduleIngressDelete waits before tearing down Ingress so the agent can finish publish teardown.
func (s *RemoteViewService) scheduleIngressDelete(ingressID string) {
	if s.ingress == nil || ingressID == "" {
		return
	}
	s.lifecycleDiag("ingress-delete-scheduled", "-", "ingressId="+ingressID+" delay="+remoteViewIngressDeleteDelay.String())
	go func(id string) {
		time.Sleep(remoteViewIngressDeleteDelay)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.lifecycleDiag("ingress-delete-start", "-", "ingressId="+id)
		if err := s.ingress.DeleteIngress(ctx, id); err != nil {
			logPackage2.Printf("[RemoteView] delete ingress failed: %v", err)
			s.lifecycleDiag("ingress-delete-failed", "-", "ingressId="+id+" err="+err.Error())
			return
		}
		s.lifecycleDiag("ingress-delete-complete", "-", "ingressId="+id)
	}(ingressID)
}
func (s *RemoteViewService) scheduleRoomDelete(roomName string) {
	if s.ingress == nil || strings.TrimSpace(roomName) == "" {
		return
	}
	s.lifecycleDiag("room-delete-scheduled", "-", "room="+roomName+" delay="+remoteViewRoomDeleteDelay.String())
	go func(name string) {
		time.Sleep(remoteViewRoomDeleteDelay)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.lifecycleDiag("room-delete-start", "-", "room="+name)
		if err := s.ingress.DeleteRoomWithRetry(ctx, name, 3); err != nil {
			logPackage2.Printf("[RemoteView] delete room failed after retries: %v", err)
			s.lifecycleDiag("room-delete-failed", "-", "room="+name+" err="+err.Error())
			return
		}
		s.lifecycleDiag("room-delete-complete", "-", "room="+name)
	}(roomName)
}
func (s *RemoteViewService) OnAgentDisconnected(ctx context.Context, deviceIDStr string) {
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		return
	}

	unlock := s.lockDeviceStart(deviceIDStr)
	defer unlock()

	session, err := s.repo.GetActiveByDevice(ctx, deviceID)
	if err != nil || session == nil {
		return
	}
	s.lifecycleDiag(
		"session-agent-offline",
		session.ID.String(),
		"deviceId="+deviceIDStr+" ingressId="+session.IngressID,
	)
	s.forceStopSessionLocked(ctx, session, "agent_offline")
}

type LiveKitTokenService struct {
	apiKey    string
	apiSecret string
	ttl       time.Duration
}

type liveKitVideoGrant struct {
	RoomJoin     bool   `json:"roomJoin,omitempty"`
	Room         string `json:"room,omitempty"`
	RoomCreate   bool   `json:"roomCreate,omitempty"`
	RoomAdmin    bool   `json:"roomAdmin,omitempty"`
	CanPublish   bool   `json:"canPublish,omitempty"`
	CanSubscribe bool   `json:"canSubscribe,omitempty"`
	IngressAdmin bool   `json:"ingressAdmin,omitempty"`
}
type liveKitClaims struct {
	Video liveKitVideoGrant `json:"video"`
	jwt.RegisteredClaims
}

func (s *LiveKitTokenService) createAdminToken(ttl time.Duration) (string, error) {
	if s.apiKey == "" || s.apiSecret == "" {
		return "", fmt.Errorf("livekit api credentials not configured")
	}
	now := time.Now()
	claims := liveKitClaims{
		Video: liveKitVideoGrant{
			RoomCreate:   true,
			RoomAdmin:    true,
			IngressAdmin: true,
		},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.apiKey,
			Subject:   "remote-view-admin",
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.apiSecret))
}

type liveKitIngressClient struct {
	apiHost     string
	apiKey      string
	apiSecret   string
	rtmpBaseURL string
	whipBaseURL string
	http        *httpPackage2.Client
	tokens      *LiveKitTokenService
}

func (c *liveKitIngressClient) DeleteIngress(ctx context.Context, ingressID string) error {
	body, _ := json.Marshal(map[string]string{"ingress_id": ingressID})

	token, err := c.tokens.createAdminToken(5 * time.Minute)
	if err != nil {
		return err
	}

	req, err := httpPackage2.NewRequestWithContext(ctx, httpPackage2.MethodPost, c.apiHost+"/twirp/livekit.Ingress/DeleteIngress", bytesPackage2.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		if isIngressNotFound(resp.StatusCode, string(respBody)) {
			return nil
		}
		return fmt.Errorf("delete ingress http %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
func (c *liveKitIngressClient) DeleteRoom(ctx context.Context, roomName string) error {
	return c.deleteRoomOnce(ctx, roomName)
}
func (c *liveKitIngressClient) DeleteRoomWithRetry(ctx context.Context, roomName string, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := c.deleteRoomOnce(ctx, roomName); err == nil {
			return nil
		} else {
			lastErr = err
			if i < attempts-1 {
				time.Sleep(time.Duration(250*(i+1)) * time.Millisecond)
			}
		}
	}
	return lastErr
}
func (c *liveKitIngressClient) deleteRoomOnce(ctx context.Context, roomName string) error {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"room": roomName})

	token, err := c.tokens.createAdminToken(5 * time.Minute)
	if err != nil {
		return err
	}

	req, err := httpPackage2.NewRequestWithContext(ctx, httpPackage2.MethodPost, c.apiHost+"/twirp/livekit.RoomService/DeleteRoom", bytesPackage2.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		msg := string(respBody)
		// Room already gone is fine — next CreateIngress will recreate it.
		if resp.StatusCode == httpPackage2.StatusNotFound ||
			strings.Contains(strings.ToLower(msg), "not found") ||
			strings.Contains(strings.ToLower(msg), "does not exist") {
			return nil
		}
		return fmt.Errorf("delete room http %d: %s", resp.StatusCode, msg)
	}
	return nil
}
func (c *liveKitIngressClient) postTwirp(ctx context.Context, path string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	token, err := c.tokens.createAdminToken(5 * time.Minute)
	if err != nil {
		return nil, err
	}
	req, err := httpPackage2.NewRequestWithContext(ctx, httpPackage2.MethodPost, c.apiHost+path, bytesPackage2.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := c.http
	if client == nil {
		client = &httpPackage2.Client{Timeout: 3 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s http %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// PublisherHasLiveVideo is true when the SFU room has the publisher identity
// with at least one unmuted video track.
func (c *liveKitIngressClient) PublisherHasLiveVideo(ctx context.Context, roomName, identity string) (bool, error) {
	roomName = strings.TrimSpace(roomName)
	identity = strings.TrimSpace(identity)
	if roomName == "" || identity == "" {
		return false, nil
	}
	body, _ := json.Marshal(map[string]string{"room": roomName})
	raw, err := c.postTwirp(ctx, "/twirp/livekit.RoomService/ListParticipants", body)
	if err != nil {
		return false, err
	}
	participants := lkExtractObjectList(raw, "participants", "Participants")
	for _, p := range participants {
		id, _ := p["identity"].(string)
		if id == "" {
			id, _ = p["Identity"].(string)
		}
		if id != identity {
			continue
		}
		tracks, _ := p["tracks"].([]any)
		if tracks == nil {
			tracks, _ = p["Tracks"].([]any)
		}
		for _, item := range tracks {
			track, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if lkTrackIsUnmutedVideo(track) {
				return true, nil
			}
		}
	}
	return false, nil
}

// IngressIsPublishing is true when LiveKit Ingress reports ENDPOINT_PUBLISHING.
func (c *liveKitIngressClient) IngressIsPublishing(ctx context.Context, ingressID string) (bool, error) {
	ingressID = strings.TrimSpace(ingressID)
	if ingressID == "" {
		return false, nil
	}
	body, _ := json.Marshal(map[string]string{"ingress_id": ingressID})
	raw, err := c.postTwirp(ctx, "/twirp/livekit.Ingress/ListIngress", body)
	if err != nil {
		return false, err
	}
	items := lkExtractObjectList(raw, "items", "Items")
	for _, item := range items {
		id, _ := item["ingress_id"].(string)
		if id == "" {
			id, _ = item["ingressId"].(string)
		}
		if id != "" && id != ingressID {
			continue
		}
		state, _ := item["state"].(map[string]any)
		if lkIngressStatePublishing(state) {
			return true, nil
		}
	}
	return false, nil
}
func lkExtractObjectList(raw []byte, keys ...string) []map[string]any {
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	for _, key := range keys {
		arr, ok := generic[key].([]any)
		if !ok {
			continue
		}
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			if obj, ok := item.(map[string]any); ok {
				out = append(out, obj)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
func lkTrackIsUnmutedVideo(track map[string]any) bool {
	if muted, ok := track["muted"].(bool); ok && muted {
		return false
	}
	switch t := track["type"].(type) {
	case float64:
		if int(t) == 1 {
			return true
		}
	case string:
		if strings.EqualFold(t, "VIDEO") || t == "1" {
			return true
		}
	}
	mime, _ := track["mime_type"].(string)
	if mime == "" {
		mime, _ = track["mimeType"].(string)
	}
	return strings.Contains(strings.ToLower(mime), "video")
}
func lkIngressStatePublishing(state map[string]any) bool {
	if state == nil {
		return false
	}
	switch status := state["status"].(type) {
	case float64:
		return int(status) == 2 // ENDPOINT_PUBLISHING
	case string:
		s := strings.ToUpper(status)
		return s == "ENDPOINT_PUBLISHING" || s == "2"
	}
	return false
}
func (c *liveKitIngressClient) Ping(ctx context.Context) error {
	req, err := httpPackage2.NewRequestWithContext(ctx, httpPackage2.MethodGet, c.apiHost, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("livekit http %d", resp.StatusCode)
	}
	return nil
}

type RemoteViewRepository struct {
	db *sql.DB
}

func (r *RemoteViewRepository) GetActiveByDevice(ctx context.Context, deviceID uuid.UUID) (*models.RemoteViewSession, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, device_id, room_name, status, started_by, started_by_name, viewer_count,
		       publisher_identity, livekit_url, COALESCE(ingress_id, ''), COALESCE(rtmp_url, ''),
		       started_at, ended_at, COALESCE(end_reason, ''), created_at, updated_at
		FROM remote_view_sessions
		WHERE device_id = $1 AND status IN ('starting', 'active')
		ORDER BY created_at DESC
		LIMIT 1
	`, deviceID)

	return scanRemoteViewSession(row)
}
func (r *RemoteViewRepository) ListActivePublisherSessions(ctx context.Context) ([]*models.RemoteViewSession, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, device_id, room_name, status, started_by, started_by_name, viewer_count,
		       publisher_identity, livekit_url, COALESCE(ingress_id, ''), COALESCE(rtmp_url, ''),
		       started_at, ended_at, COALESCE(end_reason, ''), created_at, updated_at
		FROM remote_view_sessions
		WHERE status IN ('starting', 'active')
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*models.RemoteViewSession
	for rows.Next() {
		var session models.RemoteViewSession
		var endedAt sql.NullTime
		if err := rows.Scan(
			&session.ID, &session.DeviceID, &session.RoomName, &session.Status,
			&session.StartedBy, &session.StartedByName, &session.ViewerCount,
			&session.PublisherIdentity, &session.LiveKitURL, &session.IngressID, &session.RTMPURL,
			&session.StartedAt, &endedAt, &session.EndReason, &session.CreatedAt, &session.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if endedAt.Valid {
			session.EndedAt = &endedAt.Time
		}
		sessions = append(sessions, &session)
	}
	return sessions, rows.Err()
}
func (r *RemoteViewRepository) MarkStopped(ctx context.Context, sessionID uuid.UUID, reason string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE remote_view_sessions
		SET status = 'stopped', end_reason = $2, ended_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, sessionID, truncateEndReason(reason))
	return err
}
func (r *RemoteViewRepository) MarkFailed(ctx context.Context, sessionID uuid.UUID, reason string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE remote_view_sessions
		SET status = 'failed', end_reason = $2, ended_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status IN ('starting', 'active', 'stopping')
	`, sessionID, truncateEndReason(reason))
	return err
}
func truncateEndReason(reason string) string {
	reason = strings.TrimSpace(reason)
	const max = 500
	if len(reason) <= max {
		return reason
	}
	return reason[:max]
}
func scanRemoteViewSession(row *sql.Row) (*models.RemoteViewSession, error) {
	var session models.RemoteViewSession
	var endedAt sql.NullTime
	err := row.Scan(
		&session.ID, &session.DeviceID, &session.RoomName, &session.Status,
		&session.StartedBy, &session.StartedByName, &session.ViewerCount,
		&session.PublisherIdentity, &session.LiveKitURL, &session.IngressID, &session.RTMPURL,
		&session.StartedAt, &endedAt, &session.EndReason, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if endedAt.Valid {
		session.EndedAt = &endedAt.Time
	}
	return &session, nil
}
// ---- Source: backend/services/repositories.go ----
var ErrRecordingRemoteStorage = fmt.Errorf("recording stored in remote object storage")

func NewDatabase() *sql.DB {
	host := os.Getenv("DB_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("DB_USER")
	if user == "" {
		user = "agent"
	}
	password := os.Getenv("DB_PASSWORD")
	if password == "" {
		password = "agent123"
	}
	dbname := os.Getenv("DB_NAME")
	if dbname == "" {
		dbname = "agent"
	}
	sslmode := os.Getenv("DB_SSLMODE")
	if sslmode == "" {
		sslmode = "disable"
	}

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(fmt.Sprintf("failed to open database: %v", err))
	}

	db.SetMaxOpenConns(30)
	db.SetMaxIdleConns(15)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		panic(fmt.Sprintf("failed to ping database: %v", err))
	}

	// 初始化schema（如果表不存在）
	if err := initializeSchema(db); err != nil {
		panic(fmt.Sprintf("failed to initialize schema: %v", err))
	}
	if err := ensureAuthSessionsSchema(db); err != nil {
		panic(fmt.Sprintf("failed to ensure auth_sessions schema: %v", err))
	}
	if err := ensureWorkforceSchema(db); err != nil {
		panic(fmt.Sprintf("failed to ensure workforce schema: %v", err))
	}
	if err := ensureWorkforceTelegramSchema(db); err != nil {
		panic(fmt.Sprintf("failed to ensure workforce telegram schema: %v", err))
	}

	if err := seedDefaultAdmin(db); err != nil {
		panic(fmt.Sprintf("failed to seed default admin: %v", err))
	}

	if err := EnsureDefaultPolicy(db); err != nil {
		logPackage2.Printf("⚠️ 警告: 创建默认策略失败: %v", err)
	}

	fmt.Println("PostgreSQL connected successfully")
	return db

}
func initializeSchema(db *sql.DB) error {
	// 检查表是否存在
	var exists bool
	err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'users')`).Scan(&exists)
	if err != nil {
		return err
	}

	if exists {
		// 表已存在，检查并添加缺失的列
		return migrateSchema(db)
	}

	// 读取schema文件
	schemaPath := "schema_postgresql.sql"
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		// 如果文件不存在，使用内嵌SQL
		return initEmbeddedSchema(db)
	}

	_, err = db.Exec(string(schema))
	return err
}
func migrateSchema(db *sql.DB) error {
	migrations := []string{
		// 出款和考勤业务表（如果缺失则创建）
		`CREATE TABLE IF NOT EXISTS employees (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id VARCHAR(100),
            name VARCHAR(255),
            position VARCHAR(100),
            hire_date DATE,
            work_location VARCHAR(255),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employees_employee_id ON employees(employee_id)`,
		`CREATE TABLE IF NOT EXISTS attendance_records (
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            year_month VARCHAR(7) NOT NULL,
            date DATE NOT NULL,
            status VARCHAR(50) NOT NULL,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (employee_id, year_month, date)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_attendance_records_employee_id ON attendance_records(employee_id)`,
		`CREATE TABLE IF NOT EXISTS performance_records (
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            employee_name VARCHAR(255),
            position VARCHAR(100),
            score_records JSONB,
            total_score INTEGER DEFAULT 0,
            grade VARCHAR(50),
            month VARCHAR(7) NOT NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (employee_id, month)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_performance_records_month ON performance_records(month)`,
		`CREATE TABLE IF NOT EXISTS penalty_records (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
            employee_name VARCHAR(255),
            position VARCHAR(100),
            amount NUMERIC(12,2) NOT NULL DEFAULT 0,
            category VARCHAR(100),
            reason TEXT,
            penalty_date DATE NOT NULL,
            created_by VARCHAR(100),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_penalty_records_employee_id ON penalty_records(employee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_penalty_records_penalty_date ON penalty_records(penalty_date)`,
		`CREATE TABLE IF NOT EXISTS sites (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            code VARCHAR(100) NOT NULL UNIQUE,
            name VARCHAR(255) NOT NULL,
            sort_order INTEGER NOT NULL DEFAULT 0,
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE
        )`,
		`CREATE TABLE IF NOT EXISTS employee_accounts (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
            name VARCHAR(255),
            account_name VARCHAR(255) NOT NULL,
            shift VARCHAR(50),
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE,
            UNIQUE (site_id, account_name)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employee_accounts_site_id ON employee_accounts(site_id)`,
		`CREATE TABLE IF NOT EXISTS site_stats (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_account_id UUID NOT NULL REFERENCES employee_accounts(id) ON DELETE CASCADE,
            site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
            stat_date DATE NOT NULL,
            shift VARCHAR(50),
            order_count INTEGER NOT NULL DEFAULT 0,
            avg_time_seconds INTEGER NOT NULL DEFAULT 0,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE,
            UNIQUE (employee_account_id, stat_date, shift)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_site_stats_site_id ON site_stats(site_id)`,
		`CREATE INDEX IF NOT EXISTS idx_site_stats_employee_account_id ON site_stats(employee_account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_site_stats_stat_date ON site_stats(stat_date)`,

		// 用户表
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMP WITH TIME ZONE`,

		// 策略表
		`ALTER TABLE policies ADD COLUMN IF NOT EXISTS is_current BOOLEAN DEFAULT FALSE`,

		// 录屏表
		`ALTER TABLE recordings ADD COLUMN IF NOT EXISTS duration INTEGER DEFAULT 0`,
		`ALTER TABLE recordings ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE`,

		// 截图表
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS width INTEGER DEFAULT 0`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS height INTEGER DEFAULT 0`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS format VARCHAR(10) DEFAULT 'jpg'`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS uploaded_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS encrypted BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS employee_uuid UUID REFERENCES employees(id) ON DELETE SET NULL`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS employee_name VARCHAR(255)`,
		`ALTER TABLE screenshots ADD COLUMN IF NOT EXISTS employee_number VARCHAR(64)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshots_employee_uuid ON screenshots(employee_uuid)`,

		// 活动日志表
		`ALTER TABLE activity_logs ADD COLUMN IF NOT EXISTS reported BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE activity_logs ADD COLUMN IF NOT EXISTS reported_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE activity_logs ADD COLUMN IF NOT EXISTS retry_count INTEGER DEFAULT 0`,
		`ALTER TABLE activity_logs ADD COLUMN IF NOT EXISTS error_message TEXT DEFAULT ''`,

		`ALTER TABLE upload_tasks ADD COLUMN IF NOT EXISTS task_type VARCHAR(50) NOT NULL DEFAULT 'upload_recordings'`,
		`ALTER TABLE upload_tasks ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()`,

		// ========== ⭐ ==========
		`ALTER TABLE operation_logs ADD COLUMN IF NOT EXISTS execution_time_ms INTEGER DEFAULT 0`,
		`ALTER TABLE operation_logs ADD COLUMN IF NOT EXISTS target_name VARCHAR(255)`,

		`ALTER TABLE cleanup_policy ADD COLUMN IF NOT EXISTS attendance_retention_months INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cleanup_policy ADD COLUMN IF NOT EXISTS performance_retention_months INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cleanup_policy ADD COLUMN IF NOT EXISTS penalty_retention_months INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cleanup_policy ADD COLUMN IF NOT EXISTS salary_retention_months INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cleanup_policy ADD COLUMN IF NOT EXISTS site_stats_retention_months INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE cleanup_logs ADD COLUMN IF NOT EXISTS deleted_business_records INTEGER NOT NULL DEFAULT 0`,

		// 索引（建议先添加列，后添加索引）
		`CREATE INDEX IF NOT EXISTS idx_screenshots_device_captured ON screenshots(device_id, captured_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_recordings_uploaded ON recordings(uploaded)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_device_started ON activity_logs(device_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_reported ON activity_logs(reported)`,

		`ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_status_check`,
		`ALTER TABLE devices ADD CONSTRAINT devices_status_check CHECK (status IN ('pending', 'approved', 'rejected'))`,

		// 修复错误数据
		`UPDATE devices SET status = 'approved' WHERE status NOT IN ('pending', 'approved', 'rejected')`,

		// ========== 工资记录表 ==========
		`CREATE TABLE IF NOT EXISTS salary_records (
            id VARCHAR(36) PRIMARY KEY,
            employee_id VARCHAR(100) NOT NULL,
            employee_name VARCHAR(255) NOT NULL,
            position VARCHAR(100),
            hire_date_dept VARCHAR(255),
            months_employed INTEGER DEFAULT 0,
            last_month_base DECIMAL(12,2) DEFAULT 0,
            total_increase DECIMAL(12,2) DEFAULT 0,
            base_salary DECIMAL(12,2) DEFAULT 0,
            attendance_days DECIMAL(10,2) DEFAULT 0,
            actual_base_salary DECIMAL(12,2) DEFAULT 0,
            attendance_adjustment DECIMAL(12,2) DEFAULT 0,
            half_year_unpaid_leave DECIMAL(12,2) DEFAULT 0,
            exchange_rate DECIMAL(10,4) DEFAULT 0,
            performance_score INTEGER DEFAULT 0,
            performance_bonus_b DECIMAL(12,2) DEFAULT 0,
            reward DECIMAL(12,2) DEFAULT 0,
            penalty DECIMAL(12,2) DEFAULT 0,
            advance DECIMAL(12,2) DEFAULT 0,
            other_deduction DECIMAL(12,2) DEFAULT 0,
            protection_return DECIMAL(12,2) DEFAULT 0,
            protection_fee DECIMAL(12,2) DEFAULT 0,
            total_salary DECIMAL(12,2) DEFAULT 0,
            remark TEXT,
            year_month VARCHAR(7) NOT NULL,
            paid_status VARCHAR(16) NOT NULL DEFAULT 'pending',
            paid_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(employee_id, year_month)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_salary_records_employee_id ON salary_records(employee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_salary_records_year_month ON salary_records(year_month)`,
		`CREATE INDEX IF NOT EXISTS idx_salary_records_employee_name ON salary_records(employee_name)`,
		`ALTER TABLE salary_records ADD COLUMN IF NOT EXISTS paid_status VARCHAR(16) NOT NULL DEFAULT 'pending'`,
		`ALTER TABLE salary_records ADD COLUMN IF NOT EXISTS paid_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE salary_records ADD COLUMN IF NOT EXISTS sheet_values JSONB`,
		`CREATE TABLE IF NOT EXISTS salary_month_layouts (
            year_month VARCHAR(7) PRIMARY KEY,
            headers JSONB NOT NULL DEFAULT '[]'::jsonb,
            name_column_index INTEGER NOT NULL DEFAULT 1,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS salary_configs (
            config_key VARCHAR(64) PRIMARY KEY,
            config_name VARCHAR(64) NOT NULL DEFAULT '',
            total_increase DECIMAL(12,2) NOT NULL DEFAULT 0,
            full_attendance_days INTEGER NOT NULL DEFAULT 0,
            full_attendance_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
            full_attendance_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            exchange_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            cny_thb_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            cny_usd_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            thb_usd_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            performance_full_score_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
            performance_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            protection_fee DECIMAL(12,2) NOT NULL DEFAULT 0,
            protection_fee_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            payout_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            position_caps JSONB NOT NULL DEFAULT '[]'::jsonb,
            sort_order INTEGER NOT NULL DEFAULT 0,
            is_builtin BOOLEAN NOT NULL DEFAULT false,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`DROP TRIGGER IF EXISTS update_salary_records_updated_at ON salary_records;
        CREATE TRIGGER update_salary_records_updated_at
            BEFORE UPDATE ON salary_records
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column()`,

		// ========== 员工收款地址表 ==========
		`CREATE TABLE IF NOT EXISTS employee_payment_accounts (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL UNIQUE REFERENCES employees(id) ON DELETE CASCADE,
            account_holder VARCHAR(255) NOT NULL DEFAULT '',
            bank_name VARCHAR(255) NOT NULL DEFAULT '',
            bank_account VARCHAR(128) NOT NULL DEFAULT '',
            usdt_network VARCHAR(32) NOT NULL DEFAULT '',
            usdt_address VARCHAR(255) NOT NULL DEFAULT '',
            other_wallet_type VARCHAR(64) NOT NULL DEFAULT '',
            other_wallet_address VARCHAR(255) NOT NULL DEFAULT '',
            preferred_method VARCHAR(16) NOT NULL DEFAULT '',
            remark TEXT NOT NULL DEFAULT '',
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employee_payment_accounts_employee_id ON employee_payment_accounts(employee_id)`,
		`ALTER TABLE employee_payment_accounts ADD COLUMN IF NOT EXISTS preferred_method VARCHAR(16) NOT NULL DEFAULT ''`,
		`ALTER TABLE employee_payment_accounts ADD COLUMN IF NOT EXISTS notification_email VARCHAR(255) NOT NULL DEFAULT ''`,
		`DROP TRIGGER IF EXISTS update_employee_payment_accounts_updated_at ON employee_payment_accounts;
        CREATE TRIGGER update_employee_payment_accounts_updated_at
            BEFORE UPDATE ON employee_payment_accounts
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column()`,

		// ========== 工资打款截图 ==========
		`CREATE TABLE IF NOT EXISTS salary_payment_proofs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            year_month VARCHAR(7) NOT NULL,
            file_path VARCHAR(512) NOT NULL,
            file_name VARCHAR(255) NOT NULL,
            content_type VARCHAR(128) NOT NULL DEFAULT 'image/jpeg',
            uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL,
            uploaded_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(employee_id, year_month)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_salary_payment_proofs_employee_month ON salary_payment_proofs(employee_id, year_month)`,

		// ========== 用户通知（公告、工资发放等）==========
		`CREATE TABLE IF NOT EXISTS user_notifications (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            type VARCHAR(32) NOT NULL DEFAULT 'announcement',
            title VARCHAR(255) NOT NULL,
            content TEXT NOT NULL,
            payload JSONB,
            is_read BOOLEAN NOT NULL DEFAULT FALSE,
            show_popup BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_user_notifications_user_id ON user_notifications(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_notifications_unread ON user_notifications(user_id, is_read) WHERE is_read = FALSE`,

		// ========== 操作日志表 ==========
		`CREATE TABLE IF NOT EXISTS operation_logs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            operator_id VARCHAR(36),
            operator_name VARCHAR(100) NOT NULL,
            operator_role VARCHAR(50),
            operation_type VARCHAR(50) NOT NULL,
            operation_module VARCHAR(50) NOT NULL,
            operation_desc TEXT NOT NULL,
            target_id VARCHAR(100),
            target_name VARCHAR(255),
            ip_address VARCHAR(45),
            user_agent TEXT,
            request_method VARCHAR(10),
            request_path VARCHAR(255),
            request_params TEXT,
            response_status INTEGER,
            execution_time_ms INTEGER,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		`CREATE INDEX IF NOT EXISTS idx_operation_logs_operator_name ON operation_logs(operator_name)`,
		`CREATE INDEX IF NOT EXISTS idx_operation_logs_created_at ON operation_logs(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_operation_logs_operation_module ON operation_logs(operation_module)`,
		`CREATE INDEX IF NOT EXISTS idx_operation_logs_operation_type ON operation_logs(operation_type)`,

		// ========== 远程查看（LiveKit）==========
		`CREATE TABLE IF NOT EXISTS remote_view_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id),
            room_name VARCHAR(128) NOT NULL,
            status VARCHAR(32) NOT NULL,
            started_by UUID NOT NULL,
            started_by_name VARCHAR(128) NOT NULL,
            viewer_count INTEGER NOT NULL DEFAULT 0,
            publisher_identity VARCHAR(128) NOT NULL,
            livekit_url VARCHAR(512) NOT NULL,
            ingress_id VARCHAR(128),
            rtmp_url VARCHAR(512),
            started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            ended_at TIMESTAMPTZ,
            end_reason VARCHAR(512),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`ALTER TABLE remote_view_sessions ALTER COLUMN end_reason TYPE VARCHAR(512)`,
		`CREATE INDEX IF NOT EXISTS idx_rv_sessions_device_id ON remote_view_sessions(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_rv_sessions_status ON remote_view_sessions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_rv_sessions_started_at ON remote_view_sessions(started_at DESC)`,

		`CREATE TABLE IF NOT EXISTS remote_view_audit_logs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            session_id UUID REFERENCES remote_view_sessions(id),
            device_id UUID NOT NULL,
            operator_id UUID NOT NULL,
            operator_name VARCHAR(128) NOT NULL,
            action VARCHAR(64) NOT NULL,
            detail JSONB,
            client_ip VARCHAR(64),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_rv_audit_device_id ON remote_view_audit_logs(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_rv_audit_created_at ON remote_view_audit_logs(created_at DESC)`,

		// Session SSOT + login security settings (existing DB upgrade)
		`CREATE TABLE IF NOT EXISTS auth_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            subject_type VARCHAR(16) NOT NULL,
            subject_id UUID NOT NULL,
            status VARCHAR(16) NOT NULL DEFAULT 'active',
            version INT NOT NULL DEFAULT 1,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            revoked_at TIMESTAMP WITH TIME ZONE,
            revoke_reason VARCHAR(64),
            client_ip INET,
            user_agent TEXT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_subject ON auth_sessions(subject_type, subject_id)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_status ON auth_sessions(status) WHERE status = 'active'`,

		`CREATE TABLE IF NOT EXISTS system_settings (
            key VARCHAR(128) PRIMARY KEY,
            value TEXT NOT NULL DEFAULT '',
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS diagnostic_events (
            id UUID PRIMARY KEY,
            source VARCHAR(16) NOT NULL,
            device_id UUID NULL REFERENCES devices(id) ON DELETE SET NULL,
            module VARCHAR(64) NOT NULL,
            level VARCHAR(16) NOT NULL,
            message TEXT NOT NULL,
            context JSONB,
            correlation_id VARCHAR(128),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_diagnostic_events_created ON diagnostic_events(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_diagnostic_events_expires ON diagnostic_events(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_diagnostic_events_module ON diagnostic_events(module, level, created_at DESC)`,

		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS salary_view_password_hash TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS screen_lock_password_hash TEXT`,

		`CREATE INDEX IF NOT EXISTS idx_upload_tasks_device_status ON upload_tasks(device_id, status)`,

		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS force_offlined_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_use_default BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_allow_user_exit BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_allow_auto_start_toggle BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_restart_on_kill BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_temp_exit_until TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE`,
		`CREATE TABLE IF NOT EXISTS device_exit_unlock_codes (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            code_hash TEXT NOT NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            used_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            created_by UUID REFERENCES users(id) ON DELETE SET NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_device_exit_unlock_codes_device_active
            ON device_exit_unlock_codes(device_id, expires_at DESC)
            WHERE used_at IS NULL`,

		// ========== 安全事件（签名失败爆发 / 登录失败爆发等）==========
		`CREATE TABLE IF NOT EXISTS security_events (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            event_type VARCHAR(64) NOT NULL,
            subject_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
            subject_id VARCHAR(128) NOT NULL DEFAULT '',
            ip_address VARCHAR(64),
            reason VARCHAR(255),
            event_count INTEGER NOT NULL DEFAULT 1,
            details JSONB,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_created ON security_events(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_type_created ON security_events(event_type, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_security_events_subject ON security_events(subject_type, subject_id, created_at DESC)`,
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			// 记录警告但继续执行
			logPackage2.Printf("Migration warning (non-fatal): %v\nSQL: %s\n", err, migration)
		}
	}
	// Ensure devices table has device_secret column
	if _, err := db.Exec(`ALTER TABLE devices ADD COLUMN IF NOT EXISTS device_secret VARCHAR(128)`); err != nil {
		logPackage2.Printf("Warning: failed to add device_secret column: %v", err)
	}
	if err := ensureClientProtectionSchema(db); err != nil {
		logPackage2.Printf("Warning: failed to ensure client protection schema: %v", err)
	}

	// 为现有设备生成 device_secret（如果为空）
	if err := ensureDeviceSecrets(db); err != nil {
		logPackage2.Printf("Warning: failed to ensure device secrets: %v", err)
	}
	return nil
}
func ensureClientProtectionSchema(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS force_offlined_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_use_default BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_allow_user_exit BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_allow_auto_start_toggle BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_restart_on_kill BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_temp_exit_until TIMESTAMP WITH TIME ZONE`,
		`CREATE TABLE IF NOT EXISTS device_exit_unlock_codes (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            code_hash TEXT NOT NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            used_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            created_by UUID REFERENCES users(id) ON DELETE SET NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_device_exit_unlock_codes_device_active
            ON device_exit_unlock_codes(device_id, expires_at DESC)
            WHERE used_at IS NULL`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// ensureDeviceSecrets 为 devices 表中缺失 device_secret 的行生成随机 secret
func ensureDeviceSecrets(db *sql.DB) error {
	rows, err := db.Query(`SELECT id FROM devices WHERE device_secret IS NULL OR device_secret = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		// 生成 32 字节随机数，hex 编码
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			logPackage2.Printf("failed to generate device secret for %s: %v", id, err)
			continue
		}
		secret := hex.EncodeToString(b)
		if _, err := db.Exec(`UPDATE devices SET device_secret = $1 WHERE id = $2`, secret, id); err != nil {
			logPackage2.Printf("failed to store device secret for %s: %v", id, err)
			continue
		}
		logPackage2.Printf("Generated device_secret for device %s", id)
	}
	return nil
}

// ensureAuthSessionsSchema creates session/login-security tables required for SSOT auth.
func ensureAuthSessionsSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS auth_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            subject_type VARCHAR(16) NOT NULL,
            subject_id UUID NOT NULL,
            status VARCHAR(16) NOT NULL DEFAULT 'active',
            version INT NOT NULL DEFAULT 1,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            revoked_at TIMESTAMP WITH TIME ZONE,
            revoke_reason VARCHAR(64),
            client_ip INET,
            user_agent TEXT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_subject ON auth_sessions(subject_type, subject_id)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_status ON auth_sessions(status) WHERE status = 'active'`,
		`CREATE TABLE IF NOT EXISTS system_settings (
            key VARCHAR(128) PRIMARY KEY,
            value TEXT NOT NULL DEFAULT '',
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS salary_view_password_hash TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS screen_lock_password_hash TEXT`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec failed: %w\nSQL: %s", err, stmt)
		}
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'auth_sessions')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("auth_sessions table still missing after migration")
	}
	logPackage2.Println("auth_sessions schema ready")
	return nil
}

// closeDuplicateOpenWorkSessions keeps the newest open session per partition and closes the rest.
func closeDuplicateOpenWorkSessions(db *sql.DB, partitionBy string) error {
	_, err := db.Exec(fmt.Sprintf(`
		WITH ranked AS (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY %s ORDER BY checkin_time DESC) AS rn
			FROM work_sessions WHERE status = 'open'
		)
		UPDATE work_sessions ws
		SET status = 'closed', checkout_time = COALESCE(ws.checkout_time, NOW()), updated_at = NOW()
		FROM ranked r WHERE ws.id = r.id AND r.rn > 1`, partitionBy))
	return err
}
func closeDuplicateOpenActivitySessions(db *sql.DB) error {
	_, err := db.Exec(`
		WITH ranked AS (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY employee_id ORDER BY started_at DESC) AS rn
			FROM wf_tg_activity_sessions WHERE status = 'open'
		)
		UPDATE wf_tg_activity_sessions ws
		SET status = 'closed', ended_at = COALESCE(ws.ended_at, NOW())
		FROM ranked r WHERE ws.id = r.id AND r.rn > 1`)
	return err
}

// ensureWorkforceSchema creates employee invite / session tables for existing DB upgrades.
func ensureWorkforceSchema(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS department VARCHAR(100)`,
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS status SMALLINT NOT NULL DEFAULT 1`,
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS resigned_month VARCHAR(7)`,
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE SET NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_employees_user_id ON employees(user_id) WHERE user_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_employees_department ON employees(department)`,
		`CREATE INDEX IF NOT EXISTS idx_employees_status ON employees(status)`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS position_snapshot VARCHAR(100)`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS checkin_time TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS checkout_time TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS manually_edited BOOLEAN NOT NULL DEFAULT FALSE`,
		`UPDATE attendance_records SET status = 'work' WHERE status = 'working'`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS employee_uuid UUID REFERENCES employees(id) ON DELETE SET NULL`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS employee_session_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_ip VARCHAR(64)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_employee_uuid ON devices(employee_uuid)`,
		`CREATE TABLE IF NOT EXISTS device_invite_codes (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            code VARCHAR(32) NOT NULL UNIQUE,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            created_by UUID REFERENCES users(id) ON DELETE SET NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            used_at TIMESTAMP WITH TIME ZONE,
            used_by_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
            revoked_at TIMESTAMP WITH TIME ZONE,
            superseded_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_invite_codes_employee ON device_invite_codes(employee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_invite_codes_code ON device_invite_codes(code)`,
		`CREATE TABLE IF NOT EXISTS work_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            attendance_date DATE NOT NULL,
            device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
            checkin_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            checkout_time TIMESTAMP WITH TIME ZONE,
            status VARCHAR(20) NOT NULL DEFAULT 'open',
            recording_id UUID REFERENCES recordings(id) ON DELETE SET NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_work_sessions_employee_date ON work_sessions(employee_id, attendance_date DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec failed: %w\nSQL: %s", err, stmt)
		}
	}
	if err := closeDuplicateOpenWorkSessions(db, "employee_id"); err != nil {
		return fmt.Errorf("dedupe open work_sessions: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_open ON work_sessions(employee_id) WHERE status = 'open'`); err != nil {
		return fmt.Errorf("exec failed: %w\nSQL: CREATE UNIQUE INDEX idx_work_sessions_open", err)
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'device_invite_codes')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("device_invite_codes table still missing after migration")
	}
	logPackage2.Println("workforce schema ready")
	return nil
}

// ensureWorkforceTelegramSchema adds Telegram attendance integration tables (P0).
func ensureWorkforceTelegramSchema(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS late_minutes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS source VARCHAR(32) NOT NULL DEFAULT 'agent'`,

		`CREATE TABLE IF NOT EXISTS wf_telegram_settings (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            enabled BOOLEAN NOT NULL DEFAULT FALSE,
            bot_token_enc TEXT,
            bot_username VARCHAR(64),
            bot_id BIGINT,
            worker_secret VARCHAR(128),
            timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
            day_start VARCHAR(5) NOT NULL DEFAULT '09:00',
            day_end VARCHAR(5) NOT NULL DEFAULT '21:00',
            grace_minutes INT NOT NULL DEFAULT 5,
            dual_shift_enabled BOOLEAN NOT NULL DEFAULT FALSE,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS wf_telegram_groups (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            chat_id VARCHAR(64) NOT NULL UNIQUE,
            chat_title VARCHAR(256),
            enabled BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS employee_telegram_bindings (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            telegram_user_id BIGINT NOT NULL,
            telegram_username VARCHAR(128),
            chat_id VARCHAR(64),
            bound_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            status SMALLINT NOT NULL DEFAULT 1,
            UNIQUE(employee_id),
            UNIQUE(telegram_user_id)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employee_telegram_bindings_employee ON employee_telegram_bindings(employee_id)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec failed: %w\nSQL: %s", err, stmt)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wf_telegram_settings`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := db.Exec(`INSERT INTO wf_telegram_settings (enabled) VALUES (FALSE)`); err != nil {
			return err
		}
	}
	// Keep a single settings row. Extra rows + heartbeat bumps of updated_at can make
	// GetSettings (ORDER BY updated_at DESC) flip between configs (e.g. triple shift off).
	if count > 1 {
		if _, err := db.Exec(`
			DELETE FROM wf_telegram_settings
			WHERE id NOT IN (
				SELECT id FROM (
					SELECT id FROM wf_telegram_settings
					ORDER BY updated_at DESC NULLS LAST, id DESC
					LIMIT 1
				) keep_one
			)`); err != nil {
			return fmt.Errorf("dedupe wf_telegram_settings: %w", err)
		}
	}

	p1Stmts := []string{
		`CREATE TABLE IF NOT EXISTS wf_tg_activity_types (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            code VARCHAR(32) NOT NULL UNIQUE,
            name VARCHAR(64) NOT NULL,
            max_times_per_day INT NOT NULL DEFAULT 0,
            time_limit_seconds INT NOT NULL DEFAULT 300,
            sort_order INT NOT NULL DEFAULT 0,
            enabled BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_activity_fines (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            activity_code VARCHAR(32) NOT NULL REFERENCES wf_tg_activity_types(code) ON DELETE CASCADE,
            segment_minutes INT NOT NULL,
            fine_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
            UNIQUE(activity_code, segment_minutes)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_activity_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            activity_code VARCHAR(32) NOT NULL,
            activity_name VARCHAR(64) NOT NULL,
            attendance_date DATE NOT NULL,
            started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            ended_at TIMESTAMP WITH TIME ZONE,
            elapsed_seconds INT NOT NULL DEFAULT 0,
            limit_seconds INT NOT NULL DEFAULT 0,
            overtime_seconds INT NOT NULL DEFAULT 0,
            fine_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
            penalty_record_id UUID,
            status VARCHAR(16) NOT NULL DEFAULT 'open',
            source VARCHAR(32) NOT NULL DEFAULT 'telegram',
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_wf_tg_activity_sessions_employee ON wf_tg_activity_sessions(employee_id, attendance_date DESC)`,
	}
	for _, stmt := range p1Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p1 failed: %w\nSQL: %s", err, stmt)
		}
	}
	if err := closeDuplicateOpenActivitySessions(db); err != nil {
		return fmt.Errorf("dedupe open activity sessions: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_wf_tg_activity_sessions_open ON wf_tg_activity_sessions(employee_id) WHERE status = 'open'`); err != nil {
		return fmt.Errorf("exec p1 index failed: %w", err)
	}
	if err := seedDefaultTelegramActivityTypes(db); err != nil {
		return err
	}

	p2Stmts := []string{
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS night_start VARCHAR(5) NOT NULL DEFAULT '21:00'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS night_end VARCHAR(5) NOT NULL DEFAULT '09:00'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS daily_reset_time VARCHAR(5) NOT NULL DEFAULT '09:00'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS daily_reset_enabled BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS daily_reset_execution_offset_minutes INT NOT NULL DEFAULT 120`,
		`ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS shift_type VARCHAR(16) NOT NULL DEFAULT 'day'`,
		`CREATE TABLE IF NOT EXISTS wf_tg_group_shift_state (
            chat_id VARCHAR(64) PRIMARY KEY,
            current_shift VARCHAR(16) NOT NULL DEFAULT 'day',
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_handovers (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            employee_name VARCHAR(128) NOT NULL DEFAULT '',
            chat_id VARCHAR(64),
            from_shift VARCHAR(16) NOT NULL,
            to_shift VARCHAR(16) NOT NULL,
            note TEXT,
            handover_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_wf_tg_handovers_at ON wf_tg_handovers(handover_at DESC)`,
		`CREATE TABLE IF NOT EXISTS wf_tg_daily_reset_log (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            reset_date DATE NOT NULL,
            closed_work_sessions INT NOT NULL DEFAULT 0,
            closed_activities INT NOT NULL DEFAULT 0,
            run_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(reset_date)
        )`,
	}
	for _, stmt := range p2Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p2 failed: %w\nSQL: %s", err, stmt)
		}
	}

	p4Stmts := []string{
		`DROP INDEX IF EXISTS idx_work_sessions_open`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS shift_window_disabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS work_end_grace_minutes INT NOT NULL DEFAULT 5`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS require_pc_login BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS capture_linked_to_checkin BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS web_punch_only BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS invited_employee_uuid UUID REFERENCES employees(id) ON DELETE SET NULL`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS checkin_source VARCHAR(32)`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS checkout_source VARCHAR(32)`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS shift_type VARCHAR(16)`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS activity_seconds INT`,
		// Backfill from still-present work_sessions (before future daily purge removes them).
		`UPDATE attendance_records ar
			SET shift_type = ws.shift_type
			FROM (
				SELECT DISTINCT ON (employee_id, attendance_date)
					employee_id, attendance_date, COALESCE(NULLIF(TRIM(shift_type), ''), 'day') AS shift_type
				FROM work_sessions
				WHERE COALESCE(TRIM(shift_type), '') <> ''
				ORDER BY employee_id, attendance_date, checkin_time DESC
			) ws
			WHERE ar.employee_id = ws.employee_id
			  AND ar.date = ws.attendance_date
			  AND (ar.shift_type IS NULL OR TRIM(ar.shift_type) = '')`,
		`UPDATE attendance_records ar
			SET activity_seconds = COALESCE(act.total_sec, 0)
			FROM (
				SELECT employee_id, attendance_date, COALESCE(SUM(elapsed_seconds), 0)::int AS total_sec
				FROM wf_tg_activity_sessions
				WHERE status = 'closed'
				GROUP BY employee_id, attendance_date
			) act
			WHERE ar.employee_id = act.employee_id
			  AND ar.date = act.attendance_date
			  AND ar.activity_seconds IS NULL
			  AND ar.checkin_time IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS wf_tg_bind_tokens (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            token VARCHAR(32) NOT NULL UNIQUE,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            used_at TIMESTAMP WITH TIME ZONE,
            used_by_telegram_user_id BIGINT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_wf_tg_bind_tokens_employee ON wf_tg_bind_tokens(employee_id)`,
		`ALTER TABLE wf_tg_activity_types ADD COLUMN IF NOT EXISTS max_concurrent INT NOT NULL DEFAULT 0`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS shift_type VARCHAR(16) NOT NULL DEFAULT 'day'`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS is_shared_workstation BOOLEAN NOT NULL DEFAULT FALSE`,
		`CREATE TABLE IF NOT EXISTS wf_shared_workstation_members (
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (device_id, employee_id)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_work_fines (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            checkin_type VARCHAR(16) NOT NULL,
            segment_minutes INT NOT NULL,
            fine_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
            UNIQUE(checkin_type, segment_minutes)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_handover_configs (
            chat_id VARCHAR(64) PRIMARY KEY,
            handover_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            handover_day INT NOT NULL DEFAULT 0,
            handover_month INT NOT NULL DEFAULT 0,
            handover_day_start_time VARCHAR(5) NOT NULL DEFAULT '15:00',
            night_start_time VARCHAR(5) NOT NULL DEFAULT '21:00',
            day_start_time VARCHAR(5) NOT NULL DEFAULT '09:00',
            handover_night_hours INT NOT NULL DEFAULT 18,
            handover_day_hours INT NOT NULL DEFAULT 18,
            normal_night_hours INT NOT NULL DEFAULT 12,
            normal_day_hours INT NOT NULL DEFAULT 12,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_employee_shift_state (
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            chat_id VARCHAR(64) NOT NULL DEFAULT '',
            shift_type VARCHAR(16) NOT NULL,
            record_date DATE NOT NULL,
            session_id UUID,
            started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (employee_id, shift_type)
        )`,
		`DROP TABLE IF EXISTS wf_tg_group_shift_state`,
		`CREATE TABLE IF NOT EXISTS wf_tg_group_shift_state (
            chat_id VARCHAR(64) NOT NULL,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            shift_type VARCHAR(16) NOT NULL,
            record_date DATE NOT NULL,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (chat_id, employee_id, shift_type)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_handover_cycles (
            chat_id VARCHAR(64) NOT NULL,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            handover_date DATE NOT NULL,
            shift_type VARCHAR(16) NOT NULL,
            cycle_number INT NOT NULL,
            cycle_start_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            total_work_seconds INT NOT NULL DEFAULT 0,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (chat_id, employee_id, handover_date, shift_type, cycle_number)
        )`,
	}
	for _, stmt := range p4Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p4 failed: %w\nSQL: %s", err, stmt)
		}
	}
	if err := closeDuplicateOpenWorkSessions(db, "employee_id, shift_type"); err != nil {
		return fmt.Errorf("dedupe open work_sessions by shift: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_open_shift ON work_sessions(employee_id, shift_type) WHERE status = 'open'`); err != nil {
		return fmt.Errorf("exec p4 index failed: %w", err)
	}

	p5Stmts := []string{
		`ALTER TABLE wf_tg_handover_configs ADD COLUMN IF NOT EXISTS handover_reset_threshold_hours NUMERIC(6,2) NOT NULL DEFAULT 12`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS source_chat_id VARCHAR(64)`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS telegram_message_id BIGINT`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS limit_warning_sent BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS auto_ended BOOLEAN NOT NULL DEFAULT FALSE`,
		`CREATE TABLE IF NOT EXISTS wf_tg_push_settings (
            chat_id VARCHAR(64) PRIMARY KEY,
            channel_id VARCHAR(64),
            notification_group_id VARCHAR(64),
            extra_work_group_id VARCHAR(64),
            enable_channel_push BOOLEAN NOT NULL DEFAULT TRUE,
            enable_group_push BOOLEAN NOT NULL DEFAULT TRUE,
            enable_admin_push BOOLEAN NOT NULL DEFAULT FALSE,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_monthly_activity_stats (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            year_month VARCHAR(7) NOT NULL,
            chat_id VARCHAR(64) NOT NULL DEFAULT '',
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            shift_type VARCHAR(16) NOT NULL DEFAULT 'day',
            activity_code VARCHAR(64) NOT NULL,
            activity_name VARCHAR(128) NOT NULL DEFAULT '',
            activity_count INT NOT NULL DEFAULT 0,
            total_seconds INT NOT NULL DEFAULT 0,
            fine_total NUMERIC(12,2) NOT NULL DEFAULT 0,
            archived_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(year_month, chat_id, employee_id, shift_type, activity_code)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_monthly_work_stats (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            year_month VARCHAR(7) NOT NULL,
            chat_id VARCHAR(64) NOT NULL DEFAULT '',
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            shift_type VARCHAR(16) NOT NULL DEFAULT 'day',
            session_count INT NOT NULL DEFAULT 0,
            work_fine_total NUMERIC(12,2) NOT NULL DEFAULT 0,
            archived_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(year_month, chat_id, employee_id, shift_type)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_reset_export_log (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            reset_date DATE NOT NULL,
            export_path TEXT,
            archived_month VARCHAR(7),
            closed_work_sessions INT NOT NULL DEFAULT 0,
            closed_activities INT NOT NULL DEFAULT 0,
            run_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(reset_date)
        )`,
		`CREATE TABLE IF NOT EXISTS wf_tg_relay_states (
            chat_id VARCHAR(64) NOT NULL,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            relay_handover_date DATE NOT NULL,
            successor_record_date DATE NOT NULL,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (chat_id, employee_id)
        )`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS admin_telegram_user_ids TEXT`,
	}
	for _, stmt := range p5Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p5 failed: %w\nSQL: %s", err, stmt)
		}
	}
	p7Stmts := []string{
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS grace_minutes_after INT NOT NULL DEFAULT 360`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS work_end_grace_after INT NOT NULL DEFAULT 360`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS shift_grace_before INT`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS shift_grace_after INT`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS work_end_grace_before INT`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS work_end_grace_after INT`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS daily_reset_time VARCHAR(5)`,
		`ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS shift_detail VARCHAR(16)`,
		`ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS source_chat_id VARCHAR(64)`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS telegram_message_id BIGINT`,
		`DROP INDEX IF EXISTS idx_work_sessions_open_shift`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS overtime_immediate_sent BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS overtime_5min_sent BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS last_overtime_reminder_min INT NOT NULL DEFAULT -1`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS force_back_sent BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_tg_activity_sessions ADD COLUMN IF NOT EXISTS end_source VARCHAR(32)`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS bot_mode VARCHAR(16) NOT NULL DEFAULT 'polling'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS webhook_url TEXT`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS webhook_secret VARCHAR(64)`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS bot_ui_lang VARCHAR(8) NOT NULL DEFAULT 'both'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS fine_currency VARCHAR(16) NOT NULL DEFAULT 'CNY'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS triple_shift_enabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS middle_start VARCHAR(5) NOT NULL DEFAULT '13:00'`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS middle_end VARCHAR(5) NOT NULL DEFAULT '01:00'`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS bot_ui_lang VARCHAR(8)`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS worker_pairing_code VARCHAR(16)`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS worker_pairing_expires_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS worker_last_seen_at TIMESTAMP WITH TIME ZONE`,
		`CREATE TABLE IF NOT EXISTS wf_telegram_discovered_chats (
			chat_id VARCHAR(64) PRIMARY KEY,
			chat_title VARCHAR(256),
			chat_type VARCHAR(32),
			first_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wf_tg_discovered_last_seen ON wf_telegram_discovered_chats(last_seen_at DESC)`,
		`CREATE TABLE IF NOT EXISTS wf_tg_group_reset_log (
			chat_id VARCHAR(64) NOT NULL,
			reset_date DATE NOT NULL,
			closed_work_sessions INT NOT NULL DEFAULT 0,
			closed_activities INT NOT NULL DEFAULT 0,
			run_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			UNIQUE(chat_id, reset_date)
		)`,
		`ALTER TABLE employee_telegram_bindings ADD COLUMN IF NOT EXISTS last_group_bot_message_id BIGINT`,
		`CREATE TABLE IF NOT EXISTS wf_tg_activity_sessions_archive (
			id UUID PRIMARY KEY,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			activity_code VARCHAR(32) NOT NULL,
			activity_name VARCHAR(64) NOT NULL,
			attendance_date DATE NOT NULL,
			started_at TIMESTAMP WITH TIME ZONE NOT NULL,
			ended_at TIMESTAMP WITH TIME ZONE,
			elapsed_seconds INT NOT NULL DEFAULT 0,
			overtime_seconds INT NOT NULL DEFAULT 0,
			fine_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
			status VARCHAR(16) NOT NULL,
			source VARCHAR(32) NOT NULL DEFAULT 'telegram',
			end_source VARCHAR(32),
			shift_type VARCHAR(16) NOT NULL DEFAULT 'day',
			source_chat_id VARCHAR(64),
			archived_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wf_tg_activity_archive_emp ON wf_tg_activity_sessions_archive(employee_id, attendance_date DESC)`,
	}
	for _, stmt := range p7Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p7 failed: %w\nSQL: %s", err, stmt)
		}
	}
	if err := closeDuplicateOpenWorkSessions(db, "employee_id, shift_type, attendance_date"); err != nil {
		return fmt.Errorf("dedupe open work_sessions by shift/date: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_open_shift_date ON work_sessions(employee_id, shift_type, attendance_date) WHERE status = 'open'`); err != nil {
		return fmt.Errorf("exec p7 index failed: %w", err)
	}
	if err := seedDefaultTelegramWorkFines(db); err != nil {
		return err
	}
	p8Stmts := []string{
		`CREATE TABLE IF NOT EXISTS performance_push_settings (
            id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
            bot_token_enc TEXT NOT NULL DEFAULT '',
            bot_username VARCHAR(128),
            bot_id BIGINT,
            chat_id VARCHAR(64),
            chat_title VARCHAR(255),
            chat_type VARCHAR(32),
            enabled BOOLEAN NOT NULL DEFAULT FALSE,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS penalty_push_settings (
            id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
            bot_token_enc TEXT NOT NULL DEFAULT '',
            bot_username VARCHAR(128),
            bot_id BIGINT,
            chat_id VARCHAR(64),
            chat_title VARCHAR(255),
            chat_type VARCHAR(32),
            enabled BOOLEAN NOT NULL DEFAULT FALSE,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS agent_notification_settings (
            id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
            enabled BOOLEAN NOT NULL DEFAULT TRUE,
            salary_paid_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            penalty_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            performance_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            attendance_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            activity_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            default_duration_sec INT NOT NULL DEFAULT 300,
            startup_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            local_login_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            local_attendance_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            local_bind_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            local_duration_sec INT NOT NULL DEFAULT 60,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`INSERT INTO agent_notification_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING`,
		`ALTER TABLE agent_notification_settings ADD COLUMN IF NOT EXISTS startup_enabled BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE agent_notification_settings ADD COLUMN IF NOT EXISTS local_login_enabled BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE agent_notification_settings ADD COLUMN IF NOT EXISTS local_attendance_enabled BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE agent_notification_settings ADD COLUMN IF NOT EXISTS local_bind_enabled BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE agent_notification_settings ADD COLUMN IF NOT EXISTS local_duration_sec INT NOT NULL DEFAULT 60`,
		`CREATE TABLE IF NOT EXISTS cs_tool_settings (
            id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
            tool_url TEXT NOT NULL DEFAULT 'https://dh1888.github.io/168/',
            script_url TEXT NOT NULL DEFAULT 'https://dh1888.github.io/render/',
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`INSERT INTO cs_tool_settings (id, tool_url, script_url) VALUES (
            1,
            'https://dh1888.github.io/168/',
            'https://dh1888.github.io/render/'
        ) ON CONFLICT (id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS cs_tool_shift_roster (
            id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
            payload JSONB NOT NULL DEFAULT '{}'::jsonb,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`INSERT INTO cs_tool_shift_roster (id, payload) VALUES (1, '{}'::jsonb) ON CONFLICT (id) DO NOTHING`,
	}
	for _, stmt := range p8Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p8 failed: %w\nSQL: %s", err, stmt)
		}
	}
	p9Stmts := []string{
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS late_tolerance_minutes INT`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS early_leave_tolerance_minutes INT`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS check_out_grace_before INT`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS late_tolerance_minutes INT`,
		`ALTER TABLE wf_telegram_groups ADD COLUMN IF NOT EXISTS early_leave_tolerance_minutes INT`,
	}
	for _, stmt := range p9Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p9 failed: %w\nSQL: %s", err, stmt)
		}
	}
	if _, err := db.Exec(`
		UPDATE wf_telegram_settings SET
			late_tolerance_minutes = COALESCE(late_tolerance_minutes, grace_minutes_after, 360),
			early_leave_tolerance_minutes = COALESCE(early_leave_tolerance_minutes, work_end_grace_minutes, 120),
			check_out_grace_before = COALESCE(check_out_grace_before, 120)`); err != nil {
		return fmt.Errorf("backfill grace tolerance settings: %w", err)
	}
	if _, err := db.Exec(`
		UPDATE wf_telegram_groups SET
			early_leave_tolerance_minutes = COALESCE(early_leave_tolerance_minutes, work_end_grace_before)
		WHERE work_end_grace_before IS NOT NULL AND early_leave_tolerance_minutes IS NULL`); err != nil {
		return fmt.Errorf("backfill group early leave tolerance: %w", err)
	}
	if _, err := db.Exec(`
		ALTER TABLE wf_telegram_settings
			ALTER COLUMN late_tolerance_minutes SET DEFAULT 360,
			ALTER COLUMN early_leave_tolerance_minutes SET DEFAULT 120,
			ALTER COLUMN check_out_grace_before SET DEFAULT 120`); err != nil {
		return fmt.Errorf("set grace tolerance defaults: %w", err)
	}
	p10Stmts := []string{
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS attendance_mode VARCHAR(32) NOT NULL DEFAULT 'pc_tg'`,
		`CREATE TABLE IF NOT EXISTS device_telegram_bindings (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            telegram_user_id BIGINT NOT NULL,
            telegram_username VARCHAR(128),
            chat_id VARCHAR(64),
            bound_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            status SMALLINT NOT NULL DEFAULT 1,
            UNIQUE(device_id)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_device_telegram_bindings_device ON device_telegram_bindings(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_device_telegram_bindings_tg_user ON device_telegram_bindings(telegram_user_id)`,
	}
	for _, stmt := range p10Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p10 failed: %w\nSQL: %s", err, stmt)
		}
	}
	// Allow one Telegram account to bind multiple shared-workstation devices.
	// Existing installs may still have UNIQUE(telegram_user_id); drop it safely.
	if _, err := db.Exec(`ALTER TABLE device_telegram_bindings DROP CONSTRAINT IF EXISTS device_telegram_bindings_telegram_user_id_key`); err != nil {
		return fmt.Errorf("drop device_telegram_bindings telegram_user_id unique: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_device_telegram_bindings_tg_user ON device_telegram_bindings(telegram_user_id)`); err != nil {
		return fmt.Errorf("create idx_device_telegram_bindings_tg_user: %w", err)
	}
	p11Stmts := []string{
		`CREATE TABLE IF NOT EXISTS work_sessions_archive (
			id UUID PRIMARY KEY,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			attendance_date DATE NOT NULL,
			device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
			checkin_time TIMESTAMP WITH TIME ZONE NOT NULL,
			checkout_time TIMESTAMP WITH TIME ZONE,
			status VARCHAR(20) NOT NULL,
			recording_id UUID,
			source VARCHAR(32) NOT NULL DEFAULT 'agent',
			shift_type VARCHAR(16) NOT NULL DEFAULT 'day',
			shift_detail VARCHAR(16),
			source_chat_id VARCHAR(64),
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			archived_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_work_sessions_archive_emp_date ON work_sessions_archive(employee_id, attendance_date DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_work_sessions_archive_date ON work_sessions_archive(attendance_date DESC)`,
		`CREATE TABLE IF NOT EXISTS wf_tg_monthly_export_log (
			year_month VARCHAR(7) NOT NULL,
			chat_id VARCHAR(64) NOT NULL DEFAULT '',
			export_path TEXT,
			pushed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			PRIMARY KEY (year_month, chat_id)
		)`,
		`CREATE TABLE IF NOT EXISTS wf_tg_attendance_review_tasks (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			attendance_date DATE NOT NULL,
			year_month VARCHAR(7) NOT NULL,
			shift_type VARCHAR(16) NOT NULL DEFAULT 'day',
			chat_id VARCHAR(64) NOT NULL DEFAULT '',
			trigger_type VARCHAR(32) NOT NULL DEFAULT 'early_checkout_review',
			work_seconds INT NOT NULL DEFAULT 0,
			early_minutes INT NOT NULL DEFAULT 0,
			overtime_threshold_hours INT NOT NULL DEFAULT 5,
			early_threshold_hours INT NOT NULL DEFAULT 5,
			proposed_fine NUMERIC(12,2) NOT NULL DEFAULT 0,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			tg_message_id BIGINT,
			confirmed_by VARCHAR(128),
			confirm_source VARCHAR(32),
			confirmed_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			UNIQUE(employee_id, attendance_date, shift_type, trigger_type)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wf_tg_att_review_month ON wf_tg_attendance_review_tasks(year_month, status, created_at DESC)`,
	}
	for _, stmt := range p11Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p11 failed: %w\nSQL: %s", err, stmt)
		}
	}
	p12Stmts := []string{
		`ALTER TABLE wf_telegram_settings ALTER COLUMN late_tolerance_minutes SET DEFAULT 0`,
		`ALTER TABLE wf_telegram_settings ALTER COLUMN early_leave_tolerance_minutes SET DEFAULT 10`,
		`ALTER TABLE wf_tg_handover_configs ALTER COLUMN handover_day SET DEFAULT 0`,
		`UPDATE wf_telegram_settings SET late_tolerance_minutes = 0 WHERE late_tolerance_minutes = 360`,
		`UPDATE wf_telegram_settings SET early_leave_tolerance_minutes = 10 WHERE early_leave_tolerance_minutes = 120`,
		`UPDATE wf_telegram_groups SET late_tolerance_minutes = 0 WHERE late_tolerance_minutes = 360`,
		`UPDATE wf_telegram_groups SET early_leave_tolerance_minutes = 10 WHERE early_leave_tolerance_minutes = 120`,
		`ALTER TABLE wf_telegram_settings ADD COLUMN IF NOT EXISTS show_keyboard_info_buttons BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE wf_telegram_settings ALTER COLUMN show_keyboard_info_buttons SET DEFAULT FALSE`,
	}
	for _, stmt := range p12Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p12 failed: %w\nSQL: %s", err, stmt)
		}
	}
	p13Stmts := []string{
		`CREATE TABLE IF NOT EXISTS wf_tg_user_lang_prefs (
			telegram_user_id BIGINT PRIMARY KEY,
			preferred_lang VARCHAR(8) NOT NULL DEFAULT 'zh',
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
	}
	for _, stmt := range p13Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p13 failed: %w\nSQL: %s", err, stmt)
		}
	}
	p14Stmts := []string{
		`ALTER TABLE cs_tool_settings ADD COLUMN IF NOT EXISTS custom_menus JSONB NOT NULL DEFAULT '[]'::jsonb`,
	}
	for _, stmt := range p14Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p14 failed: %w\nSQL: %s", err, stmt)
		}
	}
	// Punch reply language is per employee (shared TG / multi-device must not inherit).
	p15Stmts := []string{
		`CREATE TABLE IF NOT EXISTS wf_tg_employee_lang_prefs (
			employee_id UUID PRIMARY KEY REFERENCES employees(id) ON DELETE CASCADE,
			preferred_lang VARCHAR(8) NOT NULL DEFAULT 'zh',
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`,
		// One-time: copy personal TG prefs only (exactly one employee binding per telegram user).
		`INSERT INTO wf_tg_employee_lang_prefs (employee_id, preferred_lang, updated_at)
		SELECT b.employee_id, p.preferred_lang, COALESCE(p.updated_at, NOW())
		FROM wf_tg_user_lang_prefs p
		INNER JOIN employee_telegram_bindings b
			ON b.telegram_user_id = p.telegram_user_id AND b.status = 1
		WHERE (
			SELECT COUNT(*) FROM employee_telegram_bindings b2
			WHERE b2.telegram_user_id = p.telegram_user_id AND b2.status = 1
		) = 1
		ON CONFLICT (employee_id) DO NOTHING`,
	}
	for _, stmt := range p15Stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec p15 failed: %w\nSQL: %s", err, stmt)
		}
	}
	logPackage2.Println("workforce telegram schema ready")
	return nil
}
func seedDefaultTelegramWorkFines(db *sql.DB) error {
	defaults := []struct {
		typ     string
		segment int
		amount  float64
	}{
		{"work_start", 5, 10},
		{"work_start", 15, 30},
		{"work_end", 5, 10},
		{"work_end", 15, 30},
	}
	for _, d := range defaults {
		_, err := db.Exec(`
			INSERT INTO wf_tg_work_fines (checkin_type, segment_minutes, fine_amount)
			VALUES ($1, $2, $3) ON CONFLICT (checkin_type, segment_minutes) DO NOTHING`,
			d.typ, d.segment, d.amount)
		if err != nil {
			return err
		}
	}
	return nil
}
func seedDefaultTelegramActivityTypes(db *sql.DB) error {
	defaults := []struct {
		code, name string
		limitSec   int
		maxTimes   int
		order      int
	}{
		{"wc_small", "小厕", 180, 0, 1},
		{"wc_large", "大厕", 600, 0, 2},
		{"smoke", "抽烟", 300, 0, 3},
		{"eat", "吃饭", 1800, 0, 4},
	}
	for _, d := range defaults {
		_, err := db.Exec(`
			INSERT INTO wf_tg_activity_types (code, name, max_times_per_day, time_limit_seconds, sort_order, enabled)
			VALUES ($1, $2, $3, $4, $5, TRUE)
			ON CONFLICT (code) DO NOTHING`,
			d.code, d.name, d.maxTimes, d.limitSec, d.order)
		if err != nil {
			return err
		}
	}
	return nil
}
func initEmbeddedSchema(db *sql.DB) error {
	statements := []string{
		// `CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,

		// ========== 用户表 ==========
		`CREATE TABLE IF NOT EXISTS users (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            username VARCHAR(100) NOT NULL UNIQUE,
            password_hash VARCHAR(255) NOT NULL,
            email VARCHAR(255),
            real_name VARCHAR(100),
            status SMALLINT NOT NULL DEFAULT 1,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE,
            last_login_at TIMESTAMP WITH TIME ZONE
        )`,

		// ========== 角色表 ==========
		`CREATE TABLE IF NOT EXISTS roles (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            name VARCHAR(100) NOT NULL UNIQUE,
            description TEXT,
            permissions JSONB NOT NULL DEFAULT '[]',
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		// ========== 用户角色关联表 ==========
		`CREATE TABLE IF NOT EXISTS user_roles (
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (user_id, role_id)
        )`,

		// ========== 设备表 ==========
		`CREATE TABLE IF NOT EXISTS devices (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id VARCHAR(100),
            employee_name VARCHAR(255),
            employee_number VARCHAR(100),
            position VARCHAR(100),
            device_fingerprint VARCHAR(255) NOT NULL UNIQUE,
            hostname VARCHAR(255) NOT NULL,
            os_version VARCHAR(255),
            agent_version VARCHAR(50),
            status VARCHAR(20) NOT NULL DEFAULT 'pending',
            bound_at TIMESTAMP WITH TIME ZONE,
            last_seen_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE,
            device_secret VARCHAR(128)
        )`,

		// ========== 设备表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_devices_device_fingerprint ON devices(device_fingerprint)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_status ON devices(status)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_last_seen ON devices(last_seen_at)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_employee_id ON devices(employee_id)`,

		// ========== 录屏记录表 ==========
		`CREATE TABLE IF NOT EXISTS recordings (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            start_time TIMESTAMP WITH TIME ZONE NOT NULL,
            end_time TIMESTAMP WITH TIME ZONE NOT NULL,
            file_path TEXT NOT NULL,
            file_size BIGINT NOT NULL DEFAULT 0,
            sha256 VARCHAR(64),
            uploaded BOOLEAN NOT NULL DEFAULT FALSE,
            duration INTEGER DEFAULT 0,
            updated_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		// ========== 录屏表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_recordings_device_id ON recordings(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_recordings_start_time ON recordings(start_time)`,
		`CREATE INDEX IF NOT EXISTS idx_recordings_uploaded ON recordings(uploaded)`,
		`CREATE INDEX IF NOT EXISTS idx_recordings_device_start ON recordings(device_id, start_time DESC)`,

		// ========== 截图表（✅ 添加新字段） ==========
		`CREATE TABLE IF NOT EXISTS screenshots (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            captured_at TIMESTAMP WITH TIME ZONE NOT NULL,
            file_path TEXT NOT NULL,
            file_size BIGINT NOT NULL DEFAULT 0,
            uploaded BOOLEAN NOT NULL DEFAULT FALSE,
            width INTEGER DEFAULT 0,
            height INTEGER DEFAULT 0,
            format VARCHAR(10) DEFAULT 'jpg',
            uploaded_at TIMESTAMP WITH TIME ZONE,
            encrypted BOOLEAN DEFAULT FALSE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		// ========== 截图表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_screenshots_device_id ON screenshots(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshots_captured_at ON screenshots(captured_at)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshots_device_captured ON screenshots(device_id, captured_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_screenshots_uploaded ON screenshots(uploaded)`,

		// ========== 活动日志表（✅ 添加新字段） ==========
		`CREATE TABLE IF NOT EXISTS activity_logs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            process_name VARCHAR(255),
            window_title TEXT,
            browser_title TEXT,
            started_at TIMESTAMP WITH TIME ZONE NOT NULL,
            ended_at TIMESTAMP WITH TIME ZONE,
            is_idle BOOLEAN NOT NULL DEFAULT FALSE,
            duration_seconds INTEGER NOT NULL DEFAULT 0,
            reported BOOLEAN DEFAULT FALSE,
            reported_at TIMESTAMP WITH TIME ZONE,
            retry_count INTEGER DEFAULT 0,
            error_message TEXT DEFAULT '',
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		// ========== 活动日志表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_device_id ON activity_logs(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_started_at ON activity_logs(started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_process_name ON activity_logs(process_name)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_device_started ON activity_logs(device_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_logs_reported ON activity_logs(reported)`,

		// ========== 策略表 ==========
		`CREATE TABLE IF NOT EXISTS policies (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            name VARCHAR(200) NOT NULL,
            version INTEGER NOT NULL DEFAULT 1,
            content JSONB NOT NULL,
            signature VARCHAR(255),
            is_current BOOLEAN NOT NULL DEFAULT FALSE,
            status SMALLINT NOT NULL DEFAULT 1,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE
        )`,

		// ========== 策略表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_policies_is_current ON policies(is_current) WHERE is_current = TRUE`,
		`CREATE INDEX IF NOT EXISTS idx_policies_version ON policies(version)`,

		// ========== 上传任务表 ==========
		`CREATE TABLE IF NOT EXISTS upload_tasks (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            task_type VARCHAR(50) NOT NULL DEFAULT 'upload_recordings',
            start_time TIMESTAMP WITH TIME ZONE NOT NULL,
            end_time TIMESTAMP WITH TIME ZONE,
            status VARCHAR(20) NOT NULL DEFAULT 'pending',
            progress INTEGER NOT NULL DEFAULT 0,
            result TEXT,
            completed_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		// ========== 上传任务表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_upload_tasks_device_id ON upload_tasks(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_tasks_status ON upload_tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_upload_tasks_created_at ON upload_tasks(created_at)`,

		// ========== 清理策略表 ==========
		`CREATE TABLE IF NOT EXISTS cleanup_policy (
            id VARCHAR(50) PRIMARY KEY DEFAULT 'default',
            auto_cleanup_enabled BOOLEAN NOT NULL DEFAULT TRUE,
            recording_retention_days INTEGER NOT NULL DEFAULT 30,
            screenshot_retention_days INTEGER NOT NULL DEFAULT 30,
            log_retention_days INTEGER NOT NULL DEFAULT 90,
            disk_usage_threshold_percent INTEGER NOT NULL DEFAULT 80,
            cleanup_interval_minutes INTEGER NOT NULL DEFAULT 60,
            last_run_at TIMESTAMP WITH TIME ZONE,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		// ========== 清理日志表 ==========
		`CREATE TABLE IF NOT EXISTS cleanup_logs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            trigger_type VARCHAR(20) NOT NULL,
            status VARCHAR(20) NOT NULL,
            deleted_recordings INTEGER NOT NULL DEFAULT 0,
            deleted_screenshots INTEGER NOT NULL DEFAULT 0,
            deleted_logs INTEGER NOT NULL DEFAULT 0,
            deleted_bytes BIGINT NOT NULL DEFAULT 0,
            message TEXT,
            started_at TIMESTAMP WITH TIME ZONE NOT NULL,
            completed_at TIMESTAMP WITH TIME ZONE NOT NULL
        )`,

		// ========== 清理日志表索引 ==========
		`CREATE INDEX IF NOT EXISTS idx_cleanup_logs_started_at ON cleanup_logs(started_at)`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_logs_trigger_type ON cleanup_logs(trigger_type)`,

		// ========== 出款与考勤业务表 ==========
		`CREATE TABLE IF NOT EXISTS employees (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id VARCHAR(100),
            name VARCHAR(255),
            position VARCHAR(100),
            hire_date DATE,
            work_location VARCHAR(255),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employees_employee_id ON employees(employee_id)`,
		`CREATE TABLE IF NOT EXISTS attendance_records (
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            year_month VARCHAR(7) NOT NULL,
            date DATE NOT NULL,
            status VARCHAR(50) NOT NULL,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (employee_id, year_month, date)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_attendance_records_employee_id ON attendance_records(employee_id)`,
		`CREATE TABLE IF NOT EXISTS performance_records (
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            employee_name VARCHAR(255),
            position VARCHAR(100),
            score_records JSONB,
            total_score INTEGER DEFAULT 0,
            grade VARCHAR(50),
            month VARCHAR(7) NOT NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            PRIMARY KEY (employee_id, month)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_performance_records_month ON performance_records(month)`,
		`CREATE TABLE IF NOT EXISTS penalty_records (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
            employee_name VARCHAR(255),
            position VARCHAR(100),
            amount NUMERIC(12,2) NOT NULL DEFAULT 0,
            category VARCHAR(100),
            reason TEXT,
            penalty_date DATE NOT NULL,
            created_by VARCHAR(100),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_penalty_records_employee_id ON penalty_records(employee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_penalty_records_penalty_date ON penalty_records(penalty_date)`,
		`CREATE TABLE IF NOT EXISTS sites (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            code VARCHAR(100) NOT NULL UNIQUE,
            name VARCHAR(255) NOT NULL,
            sort_order INTEGER NOT NULL DEFAULT 0,
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE
        )`,
		`CREATE TABLE IF NOT EXISTS employee_accounts (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
            name VARCHAR(255),
            account_name VARCHAR(255) NOT NULL,
            shift VARCHAR(50),
            is_active BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE,
            UNIQUE (site_id, account_name)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employee_accounts_site_id ON employee_accounts(site_id)`,
		`CREATE TABLE IF NOT EXISTS site_stats (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_account_id UUID NOT NULL REFERENCES employee_accounts(id) ON DELETE CASCADE,
            site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
            stat_date DATE NOT NULL,
            shift VARCHAR(50),
            order_count INTEGER NOT NULL DEFAULT 0,
            avg_time_seconds INTEGER NOT NULL DEFAULT 0,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE,
            UNIQUE (employee_account_id, stat_date, shift)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_site_stats_site_id ON site_stats(site_id)`,
		`CREATE INDEX IF NOT EXISTS idx_site_stats_employee_account_id ON site_stats(employee_account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_site_stats_stat_date ON site_stats(stat_date)`,

		// ========== 添加 updated_at 自动更新触发器 ==========
		`CREATE OR REPLACE FUNCTION update_updated_at_column()
        RETURNS TRIGGER AS $$
        BEGIN
            NEW.updated_at = NOW();
            RETURN NEW;
        END;
        $$ LANGUAGE plpgsql`,

		// ========== 为 users 表添加触发器 ==========
		`DROP TRIGGER IF EXISTS update_users_updated_at ON users;
        CREATE TRIGGER update_users_updated_at
            BEFORE UPDATE ON users
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column()`,

		// ========== 为 policies 表添加触发器 ==========
		`DROP TRIGGER IF EXISTS update_policies_updated_at ON policies;
        CREATE TRIGGER update_policies_updated_at
            BEFORE UPDATE ON policies
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column()`,

		`ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_status_check`,
		`ALTER TABLE devices ADD CONSTRAINT devices_status_check CHECK (status IN ('pending', 'approved', 'rejected'))`,

		// ========== 工资记录表 ==========
		`CREATE TABLE IF NOT EXISTS salary_records (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id VARCHAR(100) NOT NULL,
            employee_name VARCHAR(255) NOT NULL,
            position VARCHAR(100),
            hire_date_dept VARCHAR(255),
            months_employed INTEGER DEFAULT 0,
            last_month_base DECIMAL(12,2) DEFAULT 0,
            total_increase DECIMAL(12,2) DEFAULT 0,
            base_salary DECIMAL(12,2) DEFAULT 0,
            attendance_days DECIMAL(10,2) DEFAULT 0,
            actual_base_salary DECIMAL(12,2) DEFAULT 0,
            attendance_adjustment DECIMAL(12,2) DEFAULT 0,
            half_year_unpaid_leave DECIMAL(12,2) DEFAULT 0,
            exchange_rate DECIMAL(10,4) DEFAULT 0,
            performance_score INTEGER DEFAULT 0,
            performance_bonus_b DECIMAL(12,2) DEFAULT 0,
            reward DECIMAL(12,2) DEFAULT 0,
            penalty DECIMAL(12,2) DEFAULT 0,
            advance DECIMAL(12,2) DEFAULT 0,
            other_deduction DECIMAL(12,2) DEFAULT 0,
            protection_return DECIMAL(12,2) DEFAULT 0,
            protection_fee DECIMAL(12,2) DEFAULT 0,
            total_salary DECIMAL(12,2) DEFAULT 0,
            remark TEXT,
            year_month VARCHAR(7) NOT NULL,
            paid_status VARCHAR(16) NOT NULL DEFAULT 'pending',
            paid_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(employee_id, year_month)
        )`,

		`CREATE INDEX IF NOT EXISTS idx_salary_records_employee_id ON salary_records(employee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_salary_records_year_month ON salary_records(year_month)`,
		`CREATE INDEX IF NOT EXISTS idx_salary_records_employee_name ON salary_records(employee_name)`,
		`ALTER TABLE salary_records ADD COLUMN IF NOT EXISTS paid_status VARCHAR(16) NOT NULL DEFAULT 'pending'`,
		`ALTER TABLE salary_records ADD COLUMN IF NOT EXISTS paid_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE salary_records ADD COLUMN IF NOT EXISTS sheet_values JSONB`,
		`CREATE TABLE IF NOT EXISTS salary_month_layouts (
            year_month VARCHAR(7) PRIMARY KEY,
            headers JSONB NOT NULL DEFAULT '[]'::jsonb,
            name_column_index INTEGER NOT NULL DEFAULT 1,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS salary_configs (
            config_key VARCHAR(64) PRIMARY KEY,
            config_name VARCHAR(64) NOT NULL DEFAULT '',
            total_increase DECIMAL(12,2) NOT NULL DEFAULT 0,
            full_attendance_days INTEGER NOT NULL DEFAULT 0,
            full_attendance_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
            full_attendance_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            exchange_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            cny_thb_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            cny_usd_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            thb_usd_rate DECIMAL(10,4) NOT NULL DEFAULT 0,
            performance_full_score_amount DECIMAL(12,2) NOT NULL DEFAULT 0,
            performance_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            protection_fee DECIMAL(12,2) NOT NULL DEFAULT 0,
            protection_fee_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            payout_currency VARCHAR(8) NOT NULL DEFAULT 'CNY',
            position_caps JSONB NOT NULL DEFAULT '[]'::jsonb,
            sort_order INTEGER NOT NULL DEFAULT 0,
            is_builtin BOOLEAN NOT NULL DEFAULT false,
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		`DROP TRIGGER IF EXISTS update_salary_records_updated_at ON salary_records;
        CREATE TRIGGER update_salary_records_updated_at
            BEFORE UPDATE ON salary_records
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column()`,

		// ========== 员工收款地址表 ==========
		`CREATE TABLE IF NOT EXISTS employee_payment_accounts (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL UNIQUE REFERENCES employees(id) ON DELETE CASCADE,
            account_holder VARCHAR(255) NOT NULL DEFAULT '',
            bank_name VARCHAR(255) NOT NULL DEFAULT '',
            bank_account VARCHAR(128) NOT NULL DEFAULT '',
            usdt_network VARCHAR(32) NOT NULL DEFAULT '',
            usdt_address VARCHAR(255) NOT NULL DEFAULT '',
            other_wallet_type VARCHAR(64) NOT NULL DEFAULT '',
            other_wallet_address VARCHAR(255) NOT NULL DEFAULT '',
            preferred_method VARCHAR(16) NOT NULL DEFAULT '',
            remark TEXT NOT NULL DEFAULT '',
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_employee_payment_accounts_employee_id ON employee_payment_accounts(employee_id)`,
		`ALTER TABLE employee_payment_accounts ADD COLUMN IF NOT EXISTS preferred_method VARCHAR(16) NOT NULL DEFAULT ''`,
		`ALTER TABLE employee_payment_accounts ADD COLUMN IF NOT EXISTS notification_email VARCHAR(255) NOT NULL DEFAULT ''`,
		`DROP TRIGGER IF EXISTS update_employee_payment_accounts_updated_at ON employee_payment_accounts;
        CREATE TRIGGER update_employee_payment_accounts_updated_at
            BEFORE UPDATE ON employee_payment_accounts
            FOR EACH ROW
            EXECUTE FUNCTION update_updated_at_column()`,

		`CREATE TABLE IF NOT EXISTS salary_payment_proofs (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            year_month VARCHAR(7) NOT NULL,
            file_path VARCHAR(512) NOT NULL,
            file_name VARCHAR(255) NOT NULL,
            content_type VARCHAR(128) NOT NULL DEFAULT 'image/jpeg',
            uploaded_by UUID REFERENCES users(id) ON DELETE SET NULL,
            uploaded_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE(employee_id, year_month)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_salary_payment_proofs_employee_month ON salary_payment_proofs(employee_id, year_month)`,

		// ========== 用户通知（公告、工资发放等）==========
		`CREATE TABLE IF NOT EXISTS user_notifications (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
            type VARCHAR(32) NOT NULL DEFAULT 'announcement',
            title VARCHAR(255) NOT NULL,
            content TEXT NOT NULL,
            payload JSONB,
            is_read BOOLEAN NOT NULL DEFAULT FALSE,
            show_popup BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_user_notifications_user_id ON user_notifications(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_notifications_unread ON user_notifications(user_id, is_read) WHERE is_read = FALSE`,

		// ========== 员工主数据 / 邀请码 / 工作 Session（Workforce）==========
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS department VARCHAR(100)`,
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS status SMALLINT NOT NULL DEFAULT 1`,
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS resigned_month VARCHAR(7)`,
		`ALTER TABLE employees ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE SET NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_employees_user_id ON employees(user_id) WHERE user_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_employees_department ON employees(department)`,
		`CREATE INDEX IF NOT EXISTS idx_employees_status ON employees(status)`,

		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS position_snapshot VARCHAR(100)`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS checkin_time TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS checkout_time TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE attendance_records ADD COLUMN IF NOT EXISTS manually_edited BOOLEAN NOT NULL DEFAULT FALSE`,
		`UPDATE attendance_records SET status = 'work' WHERE status = 'working'`,

		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS employee_uuid UUID REFERENCES employees(id) ON DELETE SET NULL`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS employee_session_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_ip VARCHAR(64)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_employee_uuid ON devices(employee_uuid)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_one_employee ON devices(employee_uuid) WHERE employee_uuid IS NOT NULL AND status != 'rejected'`,

		`CREATE TABLE IF NOT EXISTS device_invite_codes (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            code VARCHAR(32) NOT NULL UNIQUE,
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            created_by UUID REFERENCES users(id) ON DELETE SET NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            used_at TIMESTAMP WITH TIME ZONE,
            used_by_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
            revoked_at TIMESTAMP WITH TIME ZONE,
            superseded_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_invite_codes_employee ON device_invite_codes(employee_id)`,
		`CREATE INDEX IF NOT EXISTS idx_invite_codes_code ON device_invite_codes(code)`,

		`CREATE TABLE IF NOT EXISTS work_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
            attendance_date DATE NOT NULL,
            device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
            checkin_time TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            checkout_time TIMESTAMP WITH TIME ZONE,
            status VARCHAR(20) NOT NULL DEFAULT 'open',
            recording_id UUID REFERENCES recordings(id) ON DELETE SET NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_work_sessions_employee_date ON work_sessions(employee_id, attendance_date DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_open ON work_sessions(employee_id) WHERE status = 'open'`,

		`CREATE TABLE IF NOT EXISTS auth_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            subject_type VARCHAR(16) NOT NULL,
            subject_id UUID NOT NULL,
            status VARCHAR(16) NOT NULL DEFAULT 'active',
            version INT NOT NULL DEFAULT 1,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            revoked_at TIMESTAMP WITH TIME ZONE,
            revoke_reason VARCHAR(64),
            client_ip INET,
            user_agent TEXT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_subject ON auth_sessions(subject_type, subject_id)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_status ON auth_sessions(status) WHERE status = 'active'`,

		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS force_offlined_at TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_use_default BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_allow_user_exit BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_allow_auto_start_toggle BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_restart_on_kill BOOLEAN`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS client_protection_temp_exit_until TIMESTAMP WITH TIME ZONE`,
		`ALTER TABLE devices ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE`,
		`CREATE TABLE IF NOT EXISTS device_exit_unlock_codes (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
            code_hash TEXT NOT NULL,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            used_at TIMESTAMP WITH TIME ZONE,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            created_by UUID REFERENCES users(id) ON DELETE SET NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_device_exit_unlock_codes_device_active
            ON device_exit_unlock_codes(device_id, expires_at DESC)
            WHERE used_at IS NULL`,

		// 共享工位：允许多员工轮流使用同一终端（取消一人一机唯一约束）
		`DROP INDEX IF EXISTS idx_devices_one_employee`,

		`CREATE TABLE IF NOT EXISTS system_settings (
            key VARCHAR(128) PRIMARY KEY,
            value TEXT NOT NULL DEFAULT '',
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS auth_sessions (
            id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            subject_type VARCHAR(16) NOT NULL,
            subject_id UUID NOT NULL,
            status VARCHAR(16) NOT NULL DEFAULT 'active',
            version INT NOT NULL DEFAULT 1,
            expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
            revoked_at TIMESTAMP WITH TIME ZONE,
            revoke_reason VARCHAR(64),
            client_ip INET,
            user_agent TEXT,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
        )`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_subject ON auth_sessions(subject_type, subject_id)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_sessions_status ON auth_sessions(status) WHERE status = 'active'`,

		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS salary_view_password_hash TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS screen_lock_password_hash TEXT`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			// 如果表已存在，忽略错误
			if !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("failed to execute statement: %s\nError: %v", stmt, err)
			}
		}
	}
	return nil
}

var defaultAdminPermissions = models.AllAdminPermissions()

func seedDefaultAdmin(db *sql.DB) error {
	// 插入/更新默认角色
	var adminRoleID string
	for _, role := range models.DefaultRoleSeeds {
		permissionsJSON, _ := json.Marshal(role.Permissions)

		var roleID string
		err := db.QueryRow(`
			INSERT INTO roles (id, name, description, permissions, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, $2, $3, NOW(), NOW())
			ON CONFLICT (name) DO UPDATE SET
				description = EXCLUDED.description,
				updated_at = NOW()
			RETURNING id`,
			role.Name, role.Description, string(permissionsJSON)).Scan(&roleID)
		if err != nil {
			return err
		}
		if role.Name == "管理员" {
			adminRoleID = roleID
		}
	}

	if adminRoleID == "" {
		return fmt.Errorf("admin role not found")
	}

	for _, role := range models.DefaultRoleSeeds {
		if role.ExactSync {
			if err := syncRolePermissionsExact(db, role.Name, role.Permissions); err != nil {
				return err
			}
		} else if err := mergeRolePermissions(db, role.Name, role.Permissions); err != nil {
			return err
		}
	}

	if err := retireLegacyEmployeeRole(db); err != nil {
		return err
	}
	if err := grantAttendanceReviewPermsFromAttendance(db); err != nil {
		logPackage2.Printf("grant attendance_review perms: %v", err)
	}

	// 创建默认管理员用户（已存在时不覆盖密码）
	username := os.Getenv("ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}
	password := strings.TrimSpace(os.Getenv("ADMIN_PASSWORD"))
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	isProduction := env == "production" || env == "prod"
	if password == "" {
		if isProduction {
			return fmt.Errorf("ADMIN_PASSWORD is required in production")
		}
		password = "admin123"
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	var userID string
	err = db.QueryRow(`
		INSERT INTO users (id, username, password_hash, email, real_name, status, created_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, 1, NOW())
		ON CONFLICT (username) DO NOTHING
		RETURNING id`,
		username, string(hash), "admin@example.com", "系统管理员").Scan(&userID)
	if err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		err = db.QueryRow(`SELECT id FROM users WHERE username = $1`, username).Scan(&userID)
		if err != nil {
			return err
		}
	}

	// 关联用户和角色
	_, err = db.Exec(`
		INSERT INTO user_roles (user_id, role_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, role_id) DO NOTHING`,
		userID, adminRoleID)

	// 初始化清理策略
	_, err = db.Exec(`
		INSERT INTO cleanup_policy (id, auto_cleanup_enabled, recording_retention_days, 
			screenshot_retention_days, log_retention_days, disk_usage_threshold_percent, 
			cleanup_interval_minutes, updated_at)
		VALUES (gen_random_uuid(), TRUE, 5, 5, 3, 70, 60, NOW())
		ON CONFLICT (id) DO NOTHING`)

	return err
}

// mergeRolePermissions 将缺失的权限合并进已有角色（不删除自定义权限）
func mergeRolePermissions(db *sql.DB, roleName string, required []string) error {
	var permissionsJSON string
	err := db.QueryRow(`SELECT permissions FROM roles WHERE name = $1`, roleName).Scan(&permissionsJSON)
	if err != nil {
		return err
	}

	var existing []string
	if permissionsJSON != "" && permissionsJSON != "null" {
		if err := json.Unmarshal([]byte(permissionsJSON), &existing); err != nil {
			return err
		}
	}

	seen := make(map[string]struct{}, len(existing)+len(required))
	for _, p := range existing {
		seen[p] = struct{}{}
	}
	merged := append([]string(nil), existing...)
	for _, p := range required {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		merged = append(merged, p)
	}

	if len(merged) == len(existing) {
		return nil
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE roles SET permissions = $1, updated_at = NOW() WHERE name = $2`, string(out), roleName)
	return err
}

// syncRolePermissionsExact 将角色权限对齐为指定列表（用于默认角色模板）
func syncRolePermissionsExact(db *sql.DB, roleName string, permissions []string) error {
	out, err := json.Marshal(permissions)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE roles SET permissions = $1, updated_at = NOW() WHERE name = $2`, string(out), roleName)
	return err
}

// grantAttendanceReviewPermsFromAttendance 给已有 attendance 权限的角色补齐待确认权限（自定义角色不丢菜单）
func grantAttendanceReviewPermsFromAttendance(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, COALESCE(permissions::text, '[]') FROM roles`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, permissionsJSON string
		if err := rows.Scan(&id, &permissionsJSON); err != nil {
			return err
		}
		var existing []string
		if permissionsJSON != "" && permissionsJSON != "null" {
			if err := json.Unmarshal([]byte(permissionsJSON), &existing); err != nil {
				continue
			}
		}
		seen := make(map[string]struct{}, len(existing)+2)
		for _, p := range existing {
			seen[p] = struct{}{}
		}
		changed := false
		if _, ok := seen[models.PermAttendanceView]; ok {
			if _, has := seen[models.PermAttendanceReviewView]; !has {
				existing = append(existing, models.PermAttendanceReviewView)
				seen[models.PermAttendanceReviewView] = struct{}{}
				changed = true
			}
		}
		if _, ok := seen[models.PermAttendanceEdit]; ok {
			if _, has := seen[models.PermAttendanceReviewEdit]; !has {
				existing = append(existing, models.PermAttendanceReviewEdit)
				seen[models.PermAttendanceReviewEdit] = struct{}{}
				changed = true
			}
		}
		if !changed {
			continue
		}
		out, err := json.Marshal(existing)
		if err != nil {
			return err
		}
		if _, err = db.Exec(`UPDATE roles SET permissions = $1, updated_at = NOW() WHERE id = $2`, string(out), id); err != nil {
			return err
		}
	}
	return rows.Err()
}

// retireLegacyEmployeeRole 迁移并删除已废弃的「员工」角色
func retireLegacyEmployeeRole(db *sql.DB) error {
	var employeeRoleID string
	err := db.QueryRow(`SELECT id FROM roles WHERE name = '员工'`).Scan(&employeeRoleID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	var viewerRoleID string
	if err := db.QueryRow(`SELECT id FROM roles WHERE name = '查看员'`).Scan(&viewerRoleID); err != nil {
		return err
	}

	if _, err = db.Exec(`
		INSERT INTO user_roles (user_id, role_id, created_at)
		SELECT ur.user_id, $1, NOW()
		FROM user_roles ur
		WHERE ur.role_id = $2
		ON CONFLICT (user_id, role_id) DO NOTHING`, viewerRoleID, employeeRoleID); err != nil {
		return err
	}
	if _, err = db.Exec(`DELETE FROM user_roles WHERE role_id = $1`, employeeRoleID); err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM roles WHERE id = $1`, employeeRoleID)
	return err
}
type MinIOClient struct {
	bucket    string
	enabled   bool
	rootDir   string
	endpoint  string
	accessKey string
	secretKey string
	useSSL    bool
	client    *minioPackage2.Client
}

func (m *MinIOClient) usesRemote() bool {
	return m != nil && m.client != nil
}
func (m *MinIOClient) cleanObjectName(objectName string) (string, error) {
	cleanObjectName := strings.TrimPrefix(filepath.ToSlash(objectName), "/")
	if cleanObjectName == "" {
		return "", fmt.Errorf("object name is required")
	}
	return cleanObjectName, nil
}
func (m *MinIOClient) Save(ctx context.Context, objectName string, data []byte) error {
	if m == nil || !m.enabled {
		return nil
	}
	cleanObjectName, err := m.cleanObjectName(objectName)
	if err != nil {
		return err
	}
	if m.usesRemote() {
		_, err := m.client.PutObject(ctx, m.bucket, cleanObjectName, bytesPackage2.NewReader(data), int64(len(data)), minioPackage2.PutObjectOptions{
			ContentType: "application/octet-stream",
		})
		if err != nil {
			return fmt.Errorf("failed to upload to minio: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		return err
	}
	outputPath := filepath.Join(m.rootDir, filepath.FromSlash(cleanObjectName))
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return err
	}
	return nil
}
// RemoveObject 删除 MinIO 对象或本地镜像文件
func (m *MinIOClient) RemoveObject(ctx context.Context, objectName string) error {
	if m == nil || !m.enabled {
		return nil
	}

	cleanObjectName, err := m.cleanObjectName(objectName)
	if err != nil {
		return err
	}

	if m.usesRemote() {
		return m.client.RemoveObject(ctx, m.bucket, cleanObjectName, minioPackage2.RemoveObjectOptions{})
	}

	filePath := filepath.Join(m.rootDir, filepath.FromSlash(cleanObjectName))
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type DeviceRepository struct {
	db *sql.DB
}

func NewDeviceRepository(db *sql.DB) *DeviceRepository {
	return &DeviceRepository{db: db}
}

// ✅ 修复：使用 sql.NullString 处理 NULL 字段
func (r *DeviceRepository) Get(ctx context.Context, id uuid.UUID) (*models.Device, error) {
	var d models.Device
	var boundAt, lastSeenAt sql.NullTime
	var employeeID, employeeName, employeeNumber, position sql.NullString

	query := `SELECT id, employee_id, employee_name, employee_number, position, device_fingerprint, hostname, os_version, agent_version, status, bound_at, last_seen_at, created_at, device_secret
              FROM devices WHERE id = $1`
	var deviceSecret sql.NullString
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&d.ID,
		&employeeID, &employeeName, &employeeNumber, &position,
		&d.DeviceFingerprint, &d.Hostname,
		&d.OSVersion, &d.AgentVersion,
		&d.Status, &boundAt, &lastSeenAt, &d.CreatedAt, &deviceSecret)
	if err == sql.ErrNoRows {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	// 处理 NULL 值
	if employeeID.Valid {
		d.EmployeeID = employeeID.String
	}
	if employeeName.Valid {
		d.EmployeeName = employeeName.String
	}
	if employeeNumber.Valid {
		d.EmployeeNumber = employeeNumber.String
	}
	if position.Valid {
		d.Position = position.String
	}
	if boundAt.Valid {
		d.BoundAt = &boundAt.Time
	}
	if lastSeenAt.Valid {
		d.LastSeenAt = &lastSeenAt.Time
	}
	if deviceSecret.Valid {
		d.DeviceSecret = deviceSecret.String
	}
	return &d, nil
}

func (r *DeviceRepository) Bind(ctx context.Context, id uuid.UUID, employeeID, employeeName, employeeNumber string) error {
	// ✅ 删除 status = 'online'，只更新员工信息
	query := `UPDATE devices SET employee_id = $1, employee_name = $2, employee_number = $3, bound_at = $4 WHERE id = $5`
	_, err := r.db.ExecContext(ctx, query, employeeID, nullString(employeeName), nullString(employeeNumber), models.UTCNow(), id)
	return err
}
func (r *DeviceRepository) UpdateLastSeen(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE devices SET last_seen_at = NOW() WHERE id = $1`, id)
	return err
}
type TaskRepository struct {
	db *sql.DB
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
// EnsureDefaultPolicy 确保数据库中存在默认策略
func EnsureDefaultPolicy(db *sql.DB) error {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM policies`).Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		logPackage2.Println("📋 策略表已有数据，跳过默认策略创建")
		return nil
	}

	logPackage2.Println("📋 未发现策略，正在创建默认策略...")

	// 默认策略内容（与客户端对齐）
	defaultPolicyContent := `{
        "version": 1,
        "config": {
            "recording": {
                "enabled": true,
                "fps": 5,
                "quality": 70,
                "enableGpu": true,
                "sliceMinutes": 1,
                "autoUploadOnIndex": false
            },
            "upload": {
                "enabled": true,
                "rateLimitKBps": 1024,
                "maxConcurrent": 3,
                "chunkSizeKB": 2048
            },
            "remoteView": {
                "width": 1280,
                "height": 720,
                "fps": 10
            },
            "screenshot": {
                "enabled": true,
                "intervalSeconds": 30,
                "quality": 70,
                "format": "webp"
            },
            "activity": {
                "enabled": true,
                "trackIdle": true,
                "idleThresholdSeconds": 300
            },
            "security": {
                "enableEncryption": true,
                "enableVPN": false
            },
            "cleanup": {
                "enabled": true,
                "recordingDays": 7,
                "screenshotDays": 15,
                "logDays": 3,
                "diskThresholdPercent": 80
            },
            "clientProtection": {
                "allowUserExit": true,
                "allowAutoStartToggle": true,
                "restartOnKill": false,
                "unlockCodeUnusedExpireMinutes": 30
            }
        }
    }`

	_, err = db.Exec(`
        INSERT INTO policies (id, name, version, content, signature, is_current, status, created_at)
        VALUES (
            gen_random_uuid(),
            '默认策略',
            1,
            $1,
            '',
            true,
            1,
            NOW()
        )
    `, defaultPolicyContent)

	if err != nil {
		return fmt.Errorf("插入默认策略失败: %w", err)
	}

	logPackage2.Println("✅ 默认策略创建成功")
	return nil
}

// ---- Source: backend/services/salary_config.go ----
const (
	SalaryWorkModeRemote = "remote"
	SalaryWorkModeOnsite = "onsite"
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// ---- Source: backend/services/salary_view_unlock.go ----
const salaryViewUnlockTTL = 30 * time.Minute

type salaryViewUnlockEntry struct {
	userID    string
	expiresAt time.Time
}

var (
	salaryViewUnlockMu sync.Mutex
	salaryViewUnlocks  = map[string]salaryViewUnlockEntry{}
)

const screenshotPurgeBatchSize = 500

// ---- Source: backend/services/screenshot_thumb.go ----
const (
	// 略大于网格展示尺寸，避免卡片发糊；体积仍远小于整屏原图
	screenshotThumbMaxEdge = 720
	screenshotThumbQuality = 78
)

var thumbGenLocks sync.Map // string -> *sync.Mutex

// ---- Source: backend/services/security_monitor.go ----
// Security event types persisted when burst thresholds are crossed.
const (
	SecEventSignatureFailBurst = "signature_fail_burst"
	SecEventAuthFailBurst      = "auth_fail_burst"
	SecEventLoginIPDenied      = "login_ip_denied"
	SecEventUploadRateLimited  = "upload_rate_limited"
	SecEventDeviceRegistered   = "device_registered"
)

type SecurityMonitor struct {
	db     *sql.DB
	redis  *redisPackage2.Client
	alerts *AlertService

	enabled bool
	// Persist + alert when Redis counter reaches threshold within window.
	sigFailThreshold  int
	sigFailWindow     time.Duration
	authFailThreshold int
	authFailWindow    time.Duration
	ipDeniedThreshold int
	ipDeniedWindow    time.Duration
	uploadRLThreshold int
	uploadRLWindow    time.Duration
	alertCooldown     time.Duration
}

var GlobalSecurityMonitor *SecurityMonitor

// ---- Source: backend/services/session_config.go ----
func envInt(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// IsProductionEnv returns true when ENV is production or prod.
func IsProductionEnv() bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	return env == "production" || env == "prod"
}

// WSTicketRequired when true, WebSocket handshakes must use ticket (no JWT fallback).
// Defaults to true when ENV=production|prod.
func WSTicketRequired() bool {
	v := strings.TrimSpace(os.Getenv("WS_TICKET_REQUIRED"))
	if v != "" {
		return v == "true" || v == "1"
	}
	return IsProductionEnv()
}

// WSSessionGuardInterval returns how often WS connections revalidate session state.
func WSSessionGuardInterval() time.Duration {
	seconds := envInt("WS_SESSION_GUARD_INTERVAL_SECONDS", 10)
	if seconds < 3 {
		seconds = 3
	}
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

// ---- Source: backend/services/session_jwt.go ----
const userAccessTokenTTL = 24 * time.Hour
const deviceAccessTokenTTL = 7 * 24 * time.Hour
const refreshTokenTTL = 7 * 24 * time.Hour

// Device refresh must outlive access JWT so agents can renew after 7 days
// without re-registering. Matches device session expiry in handlers/device.go.
const deviceRefreshTokenTTL = 30 * 24 * time.Hour

var (
	ErrInvalidSessionJWT = errorsPackage2.New("invalid session jwt")
	ErrMissingSessionID  = errorsPackage2.New("missing session id in jwt")
)

// ParseSessionJWT validates signature/expiry and returns sid.
func ParseSessionJWT(tokenString string) (string, error) {
	secret := models.JWTSecret()
	if len(secret) == 0 {
		return "", jwt.ErrInvalidKey
	}
	claims := &models.SessionJWTClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}))
	if err != nil {
		return "", err
	}
	if !token.Valid || claims.SID == "" {
		return "", ErrInvalidSessionJWT
	}
	return claims.SID, nil
}

// ---- Source: backend/services/session_revoke_bus.go ----
const sessionRevokeChannel = "session:revoke:v1"

// SessionRevokeEvent is published when a session becomes invalid.
type SessionRevokeEvent struct {
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason"`
}

func (s *SessionService) publishSessionRevoke(ctx context.Context, sessionID, reason string) {
	if s == nil || s.redis == nil || sessionID == "" {
		return
	}
	payload, err := json.Marshal(SessionRevokeEvent{
		SessionID: sessionID,
		Reason:    reason,
	})
	if err != nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.redis.Publish(pubCtx, sessionRevokeChannel, payload).Err(); err != nil {
		logPackage2.Printf("[SessionRevoke] publish failed session=%s: %v", sessionID, err)
	}
}

// ---- Source: backend/services/session_service.go ----
const (
	SessionSubjectUser   = "user"
	SessionSubjectDevice = "device"

	SessionStatusActive  = "active"
	SessionStatusRevoked = "revoked"
	SessionStatusExpired = "expired"
)

var (
	ErrSessionNotFound        = errorsPackage2.New("session not found")
	ErrSessionRevoked         = errorsPackage2.New("session revoked")
	ErrSessionExpired         = errorsPackage2.New("session expired")
	ErrSessionVersionConflict = errorsPackage2.New("session version conflict")
	ErrWSTicketInvalid        = errorsPackage2.New("ws ticket invalid")
	ErrSessionUnavailable     = errorsPackage2.New("session store unavailable")
)

type AuthSession struct {
	ID           uuid.UUID
	SubjectType  string
	SubjectID    uuid.UUID
	Status       string
	Version      int
	ExpiresAt    time.Time
	RevokedAt    *time.Time
	RevokeReason string
	ClientIP     string
	UserAgent    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
type sessionCachePayload struct {
	Status      string    `json:"status"`
	Version     int       `json:"version"`
	ExpiresAt   time.Time `json:"expiresAt"`
	SubjectType string    `json:"subjectType"`
	SubjectID   string    `json:"subjectId"`
}
type SessionService struct {
	db         *sql.DB
	redis      *redisPackage2.Client
	revokeHook func(SessionRevokeEvent)
}

func NewSessionService(db *sql.DB, redis *redisPackage2.Client) *SessionService {
	return &SessionService{db: db, redis: redis}
}

func (s *SessionService) emitSessionRevoke(sessionID, reason string) {
	if sessionID == "" {
		return
	}
	event := SessionRevokeEvent{SessionID: sessionID, Reason: reason}
	if s.revokeHook != nil {
		s.revokeHook(event)
	}
}
func sessionCacheKey(id string) string      { return "sess:" + id }
func sessionRefreshKey(token string) string { return "sess:refresh:" + token }
func (s *SessionService) CreateSession(
	ctx context.Context,
	subjectType string,
	subjectID uuid.UUID,
	expiresAt time.Time,
	clientIP, userAgent string,
) (*AuthSession, error) {
	if s.db == nil {
		return nil, ErrSessionUnavailable
	}

	id := uuid.New()
	now := models.UTCNow()
	sess := &AuthSession{
		ID:          id,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Status:      SessionStatusActive,
		Version:     1,
		ExpiresAt:   expiresAt,
		ClientIP:    clientIP,
		UserAgent:   userAgent,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_sessions (
			id, subject_type, subject_id, status, version, expires_at, client_ip, user_agent, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10)`,
		sess.ID, sess.SubjectType, sess.SubjectID, sess.Status, sess.Version,
		sess.ExpiresAt, normalizeSessionClientIP(clientIP), userAgent, sess.CreatedAt, sess.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	s.cacheSession(ctx, sess)
	return sess, nil
}
func (s *SessionService) GetSession(ctx context.Context, sessionID string) (*AuthSession, error) {
	if sessionID == "" {
		return nil, ErrSessionNotFound
	}

	if tomb, ok := s.loadSessionTombstone(ctx, sessionID); ok {
		return tomb, nil
	}

	if s.db == nil {
		return nil, ErrSessionUnavailable
	}

	if cached, err := s.loadSessionCache(ctx, sessionID); err == nil && cached != nil {
		if cached.Status != SessionStatusActive {
			return cached, nil
		}
		if tomb, ok := s.loadSessionTombstone(ctx, sessionID); ok {
			return tomb, nil
		}
		// Active cache must be confirmed against DB (fail-closed on revoke drift).
		sess, err := s.loadSessionFromDB(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		s.cacheSession(ctx, sess)
		return sess, nil
	}

	sess, err := s.loadSessionFromDB(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	s.cacheSession(ctx, sess)
	return sess, nil
}
func (s *SessionService) loadSessionFromDB(ctx context.Context, sessionID string) (*AuthSession, error) {
	parsedID, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, ErrSessionNotFound
	}

	sess := &AuthSession{}
	var revokedAt sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT id, subject_type, subject_id, status, version, expires_at,
		       revoked_at, COALESCE(revoke_reason, ''), COALESCE(client_ip::text, ''), COALESCE(user_agent, ''),
		       created_at, updated_at
		FROM auth_sessions WHERE id = $1`, parsedID,
	).Scan(
		&sess.ID, &sess.SubjectType, &sess.SubjectID, &sess.Status, &sess.Version, &sess.ExpiresAt,
		&revokedAt, &sess.RevokeReason, &sess.ClientIP, &sess.UserAgent,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		if errorsPackage2.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	if revokedAt.Valid {
		sess.RevokedAt = &revokedAt.Time
	}
	if sess.Status == SessionStatusActive && models.UTCNow().After(sess.ExpiresAt) {
		sess.Status = SessionStatusExpired
	}
	return sess, nil
}
func (s *SessionService) RevokeSession(ctx context.Context, sessionID, reason string) error {
	if sessionID == "" || s.db == nil {
		return nil
	}
	parsedID, err := uuid.Parse(sessionID)
	if err != nil {
		return nil
	}

	now := models.UTCNow()
	_, err = s.db.ExecContext(ctx, `
		UPDATE auth_sessions
		SET status = $2, revoked_at = $3, revoke_reason = $4, updated_at = $3
		WHERE id = $1 AND status = $5`,
		parsedID, SessionStatusRevoked, now, reason, SessionStatusActive,
	)
	if err != nil {
		return err
	}

	s.invalidateSessionCache(ctx, sessionID)
	s.writeSessionTombstone(ctx, sessionID, SessionStatusRevoked, reason)
	s.publishSessionRevoke(ctx, sessionID, reason)
	s.emitSessionRevoke(sessionID, reason)
	return nil
}
func (s *SessionService) RevokeSessionsBySubject(ctx context.Context, subjectType string, subjectID uuid.UUID, reason string) error {
	if s.db == nil {
		return ErrSessionUnavailable
	}

	now := models.UTCNow()
	rows, err := s.db.QueryContext(ctx, `
		UPDATE auth_sessions
		SET status = $4, revoked_at = $5, revoke_reason = $6, updated_at = $5
		WHERE subject_type = $1 AND subject_id = $2 AND status = $3
		RETURNING id`,
		subjectType, subjectID, SessionStatusActive, SessionStatusRevoked, now, reason,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		s.invalidateSessionCache(ctx, id.String())
		s.writeSessionTombstone(ctx, id.String(), SessionStatusRevoked, reason)
		s.publishSessionRevoke(ctx, id.String(), reason)
		s.emitSessionRevoke(id.String(), reason)
	}
	return rows.Err()
}
func (s *SessionService) ValidateActiveSession(ctx context.Context, sessionID string) (*AuthSession, error) {
	start := time.Now()
	defer func() {
		GlobalMetrics.ObserveSessionValidation(time.Since(start))
	}()

	sess, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	switch sess.Status {
	case SessionStatusRevoked:
		return nil, ErrSessionRevoked
	case SessionStatusExpired:
		return nil, ErrSessionExpired
	case SessionStatusActive:
		if models.UTCNow().After(sess.ExpiresAt) {
			return nil, ErrSessionExpired
		}
		return sess, nil
	default:
		return nil, ErrSessionRevoked
	}
}
func (s *SessionService) RevokeSubjectRefreshTokens(ctx context.Context, subjectType string, subjectID uuid.UUID, reason string) error {
	if err := s.RevokeSessionsBySubject(ctx, subjectType, subjectID, reason); err != nil {
		return err
	}
	if s.redis == nil {
		return nil
	}
	indexKey := sessionRefreshIndexKey(subjectType, subjectID.String())
	tokens, err := s.redis.SMembers(ctx, indexKey).Result()
	if err != nil && err != redisPackage2.Nil {
		return err
	}
	pipe := s.redis.Pipeline()
	for _, token := range tokens {
		pipe.Del(ctx, sessionRefreshKey(token))
	}
	pipe.Del(ctx, indexKey)
	_, err = pipe.Exec(ctx)
	return err
}
func sessionRefreshIndexKey(subjectType, subjectID string) string {
	return "sess:refresh_index:" + subjectType + ":" + subjectID
}
func (s *SessionService) cacheSession(ctx context.Context, sess *AuthSession) {
	if s.redis == nil || sess == nil {
		return
	}
	payload, err := json.Marshal(sessionCachePayload{
		Status:      sess.Status,
		Version:     sess.Version,
		ExpiresAt:   sess.ExpiresAt,
		SubjectType: sess.SubjectType,
		SubjectID:   sess.SubjectID.String(),
	})
	if err != nil {
		return
	}
	ttl := time.Until(sess.ExpiresAt)
	if ttl <= 0 {
		ttl = time.Minute
	}
	if err := s.redis.Set(ctx, sessionCacheKey(sess.ID.String()), payload, ttl).Err(); err != nil {
		logPackage2.Printf("[Session] cache set failed for %s: %v", sess.ID, err)
	}
}
func (s *SessionService) loadSessionCache(ctx context.Context, sessionID string) (*AuthSession, error) {
	if s.redis == nil {
		return nil, redisPackage2.Nil
	}
	raw, err := s.redis.Get(ctx, sessionCacheKey(sessionID)).Result()
	if err != nil {
		return nil, err
	}
	var payload sessionCachePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	subjectID, err := uuid.Parse(payload.SubjectID)
	if err != nil {
		return nil, err
	}
	parsedID, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, err
	}
	status := payload.Status
	if status == SessionStatusActive && models.UTCNow().After(payload.ExpiresAt) {
		status = SessionStatusExpired
	}
	return &AuthSession{
		ID:          parsedID,
		SubjectType: payload.SubjectType,
		SubjectID:   subjectID,
		Status:      status,
		Version:     payload.Version,
		ExpiresAt:   payload.ExpiresAt,
	}, nil
}
func (s *SessionService) invalidateSessionCache(ctx context.Context, sessionID string) {
	if s.redis == nil || sessionID == "" {
		return
	}
	_ = s.redis.Del(ctx, sessionCacheKey(sessionID)).Err()
}
// normalizeSessionClientIP returns a PostgreSQL INET-safe value or nil.
func normalizeSessionClientIP(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	return nil
}

// ---- Source: backend/services/session_tombstone.go ----
const sessionTombstoneTTL = 24 * time.Hour

type sessionTombstonePayload struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func sessionTombstoneKey(id string) string { return "sess:tomb:" + id }
func (s *SessionService) writeSessionTombstone(ctx context.Context, sessionID, status, reason string) {
	if s == nil || s.redis == nil || sessionID == "" {
		return
	}
	payload, err := json.Marshal(sessionTombstonePayload{
		Status: status,
		Reason: reason,
	})
	if err != nil {
		return
	}
	tombCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.redis.Set(tombCtx, sessionTombstoneKey(sessionID), payload, sessionTombstoneTTL).Err(); err != nil {
		logPackage2.Printf("[Session] tombstone write failed session=%s: %v", sessionID, err)
	}
}
func (s *SessionService) loadSessionTombstone(ctx context.Context, sessionID string) (*AuthSession, bool) {
	if s == nil || s.redis == nil || sessionID == "" {
		return nil, false
	}
	raw, err := s.redis.Get(ctx, sessionTombstoneKey(sessionID)).Bytes()
	if err != nil {
		return nil, false
	}
	var payload sessionTombstonePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	parsedID, err := uuid.Parse(sessionID)
	if err != nil {
		return nil, false
	}
	switch payload.Status {
	case SessionStatusRevoked, SessionStatusExpired:
		return &AuthSession{
			ID:           parsedID,
			Status:       payload.Status,
			RevokeReason: payload.Reason,
		}, true
	default:
		return nil, false
	}
}

// ---- Source: backend/services/system_settings.go ----
const (
	systemSettingCorsOrigins             = "cors_allowed_origins"
	systemSettingLoginCaptcha            = "login_captcha_enabled"
	systemSettingLoginTotp               = "login_totp_enabled"
	systemSettingLoginIPWhitelistEnabled = "login_ip_whitelist_enabled"
	systemSettingLoginIPWhitelist        = "login_ip_whitelist"
)

// ---- Source: backend/services/token_crypto.go ----
func deriveAppSecretKey() []byte {
	raw := strings.TrimSpace(os.Getenv("TELEGRAM_TOKEN_SECRET"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("JWT_SECRET"))
	}
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("DM_SECRET_KEY"))
	}
	if raw == "" {
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) >= 32 {
		return b[:32]
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
func EncryptSecret(plain string) (string, error) {
	key := deriveAppSecretKey()
	if len(key) != 32 {
		return "", errorsPackage2.New("secret key not configured (set JWT_SECRET or TELEGRAM_TOKEN_SECRET)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
func DecryptSecret(enc string) (string, error) {
	key := deriveAppSecretKey()
	if len(key) != 32 {
		return "", errorsPackage2.New("secret key not configured")
	}
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid ciphertext")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
func TelegramAttendanceEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("TELEGRAM_ATTENDANCE_ENABLED")))
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return true
}

// ---- Source: backend/services/totp_auth.go ----
const (
	loginChallengePrefix = "login:challenge:"
	loginChallengeTTL    = 5 * time.Minute
	totpIssuer           = "DHPG"
)

type LoginChallengePurpose string

const (
	LoginChallengeVerify LoginChallengePurpose = "verify"
	LoginChallengeSetup  LoginChallengePurpose = "setup"
	LoginChallengeBind   LoginChallengePurpose = "bind"
)

// ---- Source: backend/services/upload_chunk_store.go ----
const uploadChunkRoot = "upload-chunks"

// UploadChunkStore persists upload parts in shared object storage (MinIO/S3 or UPLOADS_DIR mirror).
type UploadChunkStore struct {
	minio *MinIOClient
}

func NewUploadChunkStore(m *MinIOClient) *UploadChunkStore {
	if m == nil {
		return nil
	}
	return &UploadChunkStore{minio: m}
}

// NewLocalMinIOForTests returns a file-backed storage client for integration tests.
func NewLocalMinIOForTests(rootDir string) *MinIOClient {
	return &MinIOClient{bucket: "test", enabled: true, rootDir: rootDir}
}

func (s *UploadChunkStore) chunkObjectName(uploadID string, index int) string {
	return filepath.ToSlash(filepath.Join(uploadChunkRoot, uploadID, fmt.Sprintf("chunk_%05d", index)))
}
func (s *UploadChunkStore) chunkPrefix(uploadID string) string {
	return filepath.ToSlash(filepath.Join(uploadChunkRoot, uploadID)) + "/"
}
func (s *UploadChunkStore) Save(ctx context.Context, uploadID string, index int, data []byte) error {
	if s == nil || s.minio == nil {
		return fmt.Errorf("upload chunk store unavailable")
	}
	return s.minio.Save(ctx, s.chunkObjectName(uploadID, index), data)
}
func (s *UploadChunkStore) Open(ctx context.Context, uploadID string, index int) (io.ReadCloser, error) {
	if s == nil || s.minio == nil {
		return nil, fmt.Errorf("upload chunk store unavailable")
	}
	return s.minio.Open(ctx, s.chunkObjectName(uploadID, index))
}
func (s *UploadChunkStore) DeleteAll(ctx context.Context, uploadID string) error {
	if s == nil || s.minio == nil {
		return nil
	}
	return s.minio.RemovePrefix(ctx, s.chunkPrefix(uploadID))
}

// MergeReader streams chunks in order as one io.Reader.
func (s *UploadChunkStore) MergeReader(ctx context.Context, uploadID string, totalChunks int) io.ReadCloser {
	return &orderedChunkReader{
		store:       s,
		ctx:         ctx,
		uploadID:    uploadID,
		totalChunks: totalChunks,
	}
}

type orderedChunkReader struct {
	store       *UploadChunkStore
	ctx         context.Context
	uploadID    string
	totalChunks int
	current     int
	cur         io.ReadCloser
	closed      bool
	readErr     error
}

func (r *orderedChunkReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.EOF
	}
	if r.readErr != nil {
		return 0, r.readErr
	}
	for {
		if r.cur == nil {
			if r.current >= r.totalChunks {
				return 0, io.EOF
			}
			rc, err := r.store.Open(r.ctx, r.uploadID, r.current)
			if err != nil {
				r.readErr = fmt.Errorf("open chunk %d: %w", r.current, err)
				return 0, r.readErr
			}
			r.cur = rc
		}
		n, err := r.cur.Read(p)
		if n > 0 {
			if err == io.EOF {
				_ = r.cur.Close()
				r.cur = nil
				r.current++
			}
			return n, nil
		}
		if err == io.EOF {
			_ = r.cur.Close()
			r.cur = nil
			r.current++
			continue
		}
		if err != nil {
			r.readErr = err
			return 0, err
		}
	}
}
func (r *orderedChunkReader) Close() error {
	r.closed = true
	if r.cur != nil {
		return r.cur.Close()
	}
	return nil
}

// Open returns a reader for an object in shared storage.
func (m *MinIOClient) Open(ctx context.Context, objectName string) (io.ReadCloser, error) {
	if m == nil || !m.enabled {
		return nil, fmt.Errorf("storage unavailable")
	}
	cleanObjectName, err := m.cleanObjectName(objectName)
	if err != nil {
		return nil, err
	}
	if m.usesRemote() {
		return m.client.GetObject(ctx, m.bucket, cleanObjectName, minioPackage2.GetObjectOptions{})
	}
	filePath := filepath.Join(m.rootDir, filepath.FromSlash(cleanObjectName))
	return os.Open(filePath)
}

// ListObjectNames lists object keys under prefix.
func (m *MinIOClient) ListObjectNames(ctx context.Context, prefix string) ([]string, error) {
	if m == nil || !m.enabled {
		return nil, fmt.Errorf("storage unavailable")
	}
	cleanPrefix, err := m.cleanObjectName(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return nil, err
	}
	cleanPrefix = strings.TrimSuffix(cleanPrefix, "/") + "/"

	if m.usesRemote() {
		var names []string
		for obj := range m.client.ListObjects(ctx, m.bucket, minioPackage2.ListObjectsOptions{
			Prefix:    cleanPrefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				return nil, obj.Err
			}
			names = append(names, obj.Key)
		}
		return names, nil
	}

	dir := filepath.Join(m.rootDir, filepath.FromSlash(cleanPrefix))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, filepath.ToSlash(filepath.Join(cleanPrefix, e.Name())))
	}
	return names, nil
}

// RemovePrefix deletes all objects under prefix.
func (m *MinIOClient) RemovePrefix(ctx context.Context, prefix string) error {
	if m == nil || !m.enabled {
		return nil
	}
	names, err := m.ListObjectNames(ctx, prefix)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := m.RemoveObject(ctx, name); err != nil {
			return err
		}
	}
	if m.usesRemote() {
		return nil
	}
	cleanPrefix, err := m.cleanObjectName(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return err
	}
	dir := filepath.Join(m.rootDir, filepath.FromSlash(strings.TrimSuffix(cleanPrefix, "/")))
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ---- Source: backend/services/upload_complete_lock.go ----
const (
	uploadCompleteLockPrefix = "upload:complete:lock:"
	uploadCompleteLockTTL    = 15 * time.Minute
)

// UploadCompleteLock provides cross-instance mutual exclusion for upload completion.
type UploadCompleteLock struct {
	redis *redisPackage2.Client
}

func (l *UploadCompleteLock) release(ctx context.Context, key, token string) error {
	const script = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`
	return l.redis.Eval(ctx, script, []string{key}, token).Err()
}

// ---- Source: backend/services/upload_manifest.go ----
const (
	uploadManifestPrefix      = "upload:manifest:"
	uploadManifestPartsSuffix = ":parts"
	uploadManifestTTL         = time.Hour
)

// UploadManifest is the cross-instance source of truth for received chunks.
type UploadManifest struct {
	UploadID    string    `json:"uploadId"`
	DeviceID    string    `json:"deviceId"`
	FileName    string    `json:"fileName"`
	FileType    string    `json:"fileType"`
	FileID      string    `json:"fileId,omitempty"`
	TotalChunks int       `json:"totalChunks"`
	TotalSize   int64     `json:"totalSize"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// UploadManifestStore tracks chunk receipt in Redis and mirrors manifest.json to object storage.
type UploadManifestStore struct {
	redis      *redisPackage2.Client
	chunkStore *UploadChunkStore
}

func NewUploadManifestStore(redisClient *redisPackage2.Client, chunks *UploadChunkStore) *UploadManifestStore {
	if redisClient == nil {
		return nil
	}
	return &UploadManifestStore{redis: redisClient, chunkStore: chunks}
}
func (s *UploadManifestStore) metaKey(uploadID string) string {
	return uploadManifestPrefix + uploadID
}
func (s *UploadManifestStore) partsKey(uploadID string) string {
	return uploadManifestPrefix + uploadID + uploadManifestPartsSuffix
}
func (s *UploadManifestStore) manifestObjectName(uploadID string) string {
	return filepath.ToSlash(filepath.Join(uploadChunkRoot, uploadID, "manifest.json"))
}

// Init creates or refreshes manifest metadata for an upload.
func (s *UploadManifestStore) Init(ctx context.Context, m UploadManifest) error {
	if s == nil || s.redis == nil {
		return fmt.Errorf("upload manifest store unavailable")
	}
	if m.UploadID == "" || m.DeviceID == "" || m.TotalChunks <= 0 {
		return fmt.Errorf("invalid upload manifest")
	}
	m.UpdatedAt = time.Now().UTC()
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}

	metaKey := s.metaKey(m.UploadID)
	ok, err := s.redis.SetNX(ctx, metaKey, payload, uploadManifestTTL).Result()
	if err != nil {
		return err
	}
	if !ok {
		raw, getErr := s.redis.Get(ctx, metaKey).Bytes()
		if getErr != nil {
			return getErr
		}
		var existing UploadManifest
		if err := json.Unmarshal(raw, &existing); err != nil {
			return err
		}
		if !stringsEqualFold(existing.DeviceID, m.DeviceID) {
			return fmt.Errorf("upload manifest device mismatch")
		}
		pipe := s.redis.TxPipeline()
		pipe.Expire(ctx, metaKey, uploadManifestTTL)
		pipe.Expire(ctx, s.partsKey(m.UploadID), uploadManifestTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		return s.persistObjectManifest(ctx, m.UploadID)
	}

	pipe := s.redis.TxPipeline()
	pipe.Expire(ctx, s.partsKey(m.UploadID), uploadManifestTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return s.persistObjectManifest(ctx, m.UploadID)
}

// RecordChunk marks a chunk as stored and returns received count.
func (s *UploadManifestStore) RecordChunk(ctx context.Context, uploadID string, chunkIndex int, chunkSize int64, checksum string) (int, error) {
	if s == nil || s.redis == nil {
		return 0, fmt.Errorf("upload manifest store unavailable")
	}
	part := strconv.Itoa(chunkIndex)
	pipe := s.redis.TxPipeline()
	pipe.SAdd(ctx, s.partsKey(uploadID), part)
	if checksum != "" {
		pipe.HSet(ctx, s.metaKey(uploadID)+":checksums", part, checksum)
	}
	if chunkSize > 0 {
		pipe.HIncrBy(ctx, s.metaKey(uploadID)+":meta", "totalSize", chunkSize)
	}
	pipe.Expire(ctx, s.partsKey(uploadID), uploadManifestTTL)
	pipe.Expire(ctx, s.metaKey(uploadID)+":checksums", uploadManifestTTL)
	pipe.Expire(ctx, s.metaKey(uploadID)+":meta", uploadManifestTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	count, err := s.redis.SCard(ctx, s.partsKey(uploadID)).Result()
	if err != nil {
		return 0, err
	}
	return int(count), s.persistObjectManifest(ctx, uploadID)
}

// Verify ensures manifest ownership, chunk completeness, index continuity, and checksums.
func (s *UploadManifestStore) Verify(ctx context.Context, uploadID, deviceID string, totalChunks int, chunkChecksums map[int]string) (*UploadManifest, error) {
	if s == nil || s.redis == nil {
		return nil, fmt.Errorf("upload manifest store unavailable")
	}
	raw, err := s.redis.Get(ctx, s.metaKey(uploadID)).Bytes()
	if err != nil {
		if err == redisPackage2.Nil {
			return nil, fmt.Errorf("upload manifest not found")
		}
		return nil, err
	}
	var manifest UploadManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if !stringsEqualFold(manifest.DeviceID, deviceID) {
		return nil, fmt.Errorf("upload manifest device mismatch")
	}
	if manifest.TotalChunks != totalChunks {
		return nil, fmt.Errorf("upload manifest total chunks mismatch")
	}

	parts, err := s.redis.SMembers(ctx, s.partsKey(uploadID)).Result()
	if err != nil {
		return nil, err
	}
	if len(parts) != totalChunks {
		return nil, fmt.Errorf("upload manifest missing chunks: expected %d got %d", totalChunks, len(parts))
	}

	seen := make(map[int]bool, totalChunks)
	for _, part := range parts {
		idx, convErr := strconv.Atoi(part)
		if convErr != nil || idx < 0 || idx >= totalChunks {
			return nil, fmt.Errorf("upload manifest invalid chunk index: %q", part)
		}
		if seen[idx] {
			return nil, fmt.Errorf("upload manifest duplicate chunk index: %d", idx)
		}
		seen[idx] = true
	}
	for i := 0; i < totalChunks; i++ {
		if !seen[i] {
			return nil, fmt.Errorf("upload manifest missing chunk index: %d", i)
		}
	}

	if len(chunkChecksums) > 0 {
		stored, err := s.redis.HGetAll(ctx, s.metaKey(uploadID)+":checksums").Result()
		if err != nil {
			return nil, err
		}
		for i := 0; i < totalChunks; i++ {
			expected := chunkChecksums[i]
			if expected == "" {
				continue
			}
			got := stored[strconv.Itoa(i)]
			if got == "" {
				return nil, fmt.Errorf("upload manifest missing checksum for chunk %d", i)
			}
			if got != expected {
				return nil, fmt.Errorf("upload manifest checksum mismatch for chunk %d", i)
			}
		}
	}

	return &manifest, nil
}

// Delete removes manifest keys and object storage manifest snapshot.
func (s *UploadManifestStore) Delete(ctx context.Context, uploadID string) error {
	if s == nil || s.redis == nil {
		return nil
	}
	pipe := s.redis.TxPipeline()
	pipe.Del(ctx, s.metaKey(uploadID))
	pipe.Del(ctx, s.partsKey(uploadID))
	pipe.Del(ctx, s.metaKey(uploadID)+":checksums")
	pipe.Del(ctx, s.metaKey(uploadID)+":meta")
	_, err := pipe.Exec(ctx)
	if s.chunkStore != nil && s.chunkStore.minio != nil {
		_ = s.chunkStore.minio.RemoveObject(ctx, s.manifestObjectName(uploadID))
	}
	return err
}
func (s *UploadManifestStore) persistObjectManifest(ctx context.Context, uploadID string) error {
	if s.chunkStore == nil || s.chunkStore.minio == nil {
		return nil
	}
	raw, err := s.redis.Get(ctx, s.metaKey(uploadID)).Bytes()
	if err != nil {
		return err
	}
	parts, err := s.redis.SMembers(ctx, s.partsKey(uploadID)).Result()
	if err != nil {
		return err
	}
	checksums, _ := s.redis.HGetAll(ctx, s.metaKey(uploadID)+":checksums").Result()
	totalSize, _ := s.redis.HGet(ctx, s.metaKey(uploadID)+":meta", "totalSize").Int64()

	var manifest UploadManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	snapshot := struct {
		UploadManifest
		ReceivedParts []string          `json:"receivedParts"`
		Checksums     map[string]string `json:"checksums,omitempty"`
		TotalSize     int64             `json:"totalSizeTracked"`
	}{
		UploadManifest: manifest,
		ReceivedParts:  parts,
		Checksums:      checksums,
		TotalSize:      totalSize,
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return s.chunkStore.minio.Save(ctx, s.manifestObjectName(uploadID), payload)
}
func stringsEqualFold(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// ---- Source: backend/services/user_repository.go ----
// UserRepository 用户仓库
// 主存储：PostgreSQL，内存作为一级缓存
type UserRepository struct {
	db    *sql.DB
	mu    sync.RWMutex
	users map[uuid.UUID]*models.User // 缓存
}

// NewUserRepository 创建用户仓库
func NewUserRepository(db *sql.DB) *UserRepository {
	repo := &UserRepository{
		db:    db,
		users: make(map[uuid.UUID]*models.User),
	}
	if db != nil {
		repo.loadUsers() // 加载缓存
	}
	return repo
}

// loadUsers 加载所有用户到缓存
func (r *UserRepository) loadUsers() {
	// PostgreSQL 兼容的用户+角色查询
	query := `
		SELECT 
			u.id, 
			u.username, 
			u.password_hash, 
			u.email, 
			u.real_name, 
			u.status, 
			COALESCE(u.totp_enabled, FALSE),
			u.created_at,
			u.last_login_at,
			COALESCE(
				(
					SELECT json_agg(
						json_build_object(
							'id', r.id,
							'name', r.name,
							'description', r.description,
							'permissions', r.permissions,
							'created_at', r.created_at,
							'updated_at', r.updated_at
						)
					)
					FROM user_roles ur
					JOIN roles r ON ur.role_id = r.id
					WHERE ur.user_id = u.id
				),
				'[]'::json
			) as roles
		FROM users u
	`

	rows, err := r.db.Query(query)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var user models.User
		var passwordHash string
		var createdAt time.Time
		var lastLogin sql.NullTime
		var rolesJSON string

		if err := rows.Scan(&user.ID, &user.Username, &passwordHash, &user.Email, &user.RealName, &user.Status, &user.TotpEnabled, &createdAt, &lastLogin, &rolesJSON); err != nil {
			continue
		}
		user.PasswordHash = passwordHash
		user.CreatedAt = createdAt
		if lastLogin.Valid {
			user.LastLogin = &lastLogin.Time
		}

		// 解析用户关联的角色
		var roles []models.Role
		if err := json.Unmarshal([]byte(rolesJSON), &roles); err == nil {
			user.Roles = roles
		}

		r.users[user.ID] = &user
	}
}

// Get 根据ID获取用户
func (r *UserRepository) Get(ctx context.Context, id uuid.UUID) (*models.User, error) {
	// 先查缓存
	r.mu.RLock()
	if user, ok := r.users[id]; ok {
		userCopy := *user
		userCopy.PasswordHash = ""
		r.mu.RUnlock()
		return &userCopy, nil
	}
	r.mu.RUnlock()

	// 缓存未命中，查询数据库
	query := `
		SELECT id, username, password_hash, email, real_name, status,
		       COALESCE(totp_enabled, FALSE), created_at, last_login_at
		FROM users WHERE id = $1
	`
	var user models.User
	var passwordHash string
	var createdAt time.Time
	var lastLogin sql.NullTime

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Username, &passwordHash, &user.Email, &user.RealName, &user.Status,
		&user.TotpEnabled, &createdAt, &lastLogin)
	if err == sql.ErrNoRows {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	user.PasswordHash = passwordHash
	user.CreatedAt = createdAt

	// 获取角色
	roles, _ := r.GetUserRoles(ctx, user.ID)
	user.Roles = roles

	// 更新缓存
	r.mu.Lock()
	r.users[user.ID] = &user
	r.mu.Unlock()

	userCopy := user
	userCopy.PasswordHash = ""
	return &userCopy, nil
}

// Delete 删除用户
func (r *UserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	// 不能删除管理员
	var username string
	err := r.db.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, id).Scan(&username)
	if err != nil {
		return err
	}
	if username == "admin" {
		return fmt.Errorf("cannot delete admin user")
	}

	// 使用事务
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除用户角色关联
	_, err = tx.Exec(`DELETE FROM user_roles WHERE user_id = $1`, id)
	if err != nil {
		return err
	}

	// 删除用户
	_, err = tx.Exec(`DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// 从缓存中删除
	r.mu.Lock()
	delete(r.users, id)
	r.mu.Unlock()

	return nil
}

// ChangePassword 修改用户密码（管理员操作）
func (r *UserRepository) ChangePassword(ctx context.Context, id uuid.UUID, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, string(hash), id)
	if err != nil {
		return err
	}

	// 更新缓存
	r.mu.Lock()
	if user, ok := r.users[id]; ok {
		user.PasswordHash = string(hash)
	}
	r.mu.Unlock()

	return nil
}
// DeleteRole 删除角色
func (r *UserRepository) DeleteRole(ctx context.Context, id uuid.UUID) error {
	// 检查是否有用户关联此角色
	var userCount int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_roles WHERE role_id = $1`, id).Scan(&userCount)
	if err != nil {
		return err
	}
	if userCount > 0 {
		return fmt.Errorf("cannot delete role: it is assigned to %d user(s)", userCount)
	}

	// 先删除角色关联
	_, err = r.db.ExecContext(ctx, `DELETE FROM user_roles WHERE role_id = $1`, id)
	if err != nil {
		return err
	}

	// 删除角色
	_, err = r.db.ExecContext(ctx, `DELETE FROM roles WHERE id = $1`, id)
	return err
}

// GetUserRoles 获取用户的所有角色
func (r *UserRepository) GetUserRoles(ctx context.Context, userID uuid.UUID) ([]models.Role, error) {
	rolesByUser, err := r.GetRolesForUserIDs(ctx, []uuid.UUID{userID})
	if err != nil {
		return nil, err
	}
	return rolesByUser[userID], nil
}

// GetRolesForUserIDs loads roles for many users in one query.
func (r *UserRepository) GetRolesForUserIDs(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]models.Role, error) {
	result := make(map[uuid.UUID][]models.Role, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(userIDs))
	args := make([]interface{}, len(userIDs))
	for i, id := range userIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT ur.user_id, r.id, r.name, r.description, r.permissions, r.created_at, r.updated_at
		FROM user_roles ur
		INNER JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id IN (%s)
		ORDER BY ur.user_id, r.name`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var userID uuid.UUID
		var role models.Role
		var permissionsJSON string
		if err := rows.Scan(&userID, &role.ID, &role.Name, &role.Description, &permissionsJSON, &role.CreatedAt, &role.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(permissionsJSON), &role.Permissions); err != nil {
			return nil, err
		}
		result[userID] = append(result[userID], role)
	}
	return result, rows.Err()
}

// ---- Source: backend/services/websocket.go ----
// WebSocket 配置常量
const (
	writeWait      = 10 * time.Second
	pongWait       = 90 * time.Second
	pingPeriod     = 54 * time.Second
	maxMessageSize = 8192
	sendBufferSize = 512
)

var wsSessionRecheckInterval = 10 * time.Second

func init() {
	wsSessionRecheckInterval = WSSessionGuardInterval()
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *httpPackage2.Request) bool {
		origin := r.Header.Get("Origin")
		if configPackage2.IsOriginAllowed(origin) {
			return true
		}
		configPackage2.LogOriginRejected(origin)
		return false
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// AgentConn 代表一个Agent的WebSocket连接
type AgentConn struct {
	ConnID      string
	DeviceID    string
	SessionID   string
	Conn        *websocket.Conn
	Send        chan []byte // 发送消息队列
	LastSeen    time.Time
	MissedPongs int32        // 最后活跃时间
	mu          sync.RWMutex // 保护 LastSeen
	sendMu      sync.Mutex
	closeOnce   sync.Once
	closed      atomic.Bool
}

// Close 安全关闭连接
func (ac *AgentConn) Close() {
	if ac == nil {
		return
	}

	ac.closeOnce.Do(func() {

		ac.closed.Store(true)

		// 先关 websocket
		ac.mu.Lock()
		if ac.Conn != nil {

			_ = ac.Conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(
					websocket.CloseNormalClosure,
					"",
				),
			)

			_ = ac.Conn.Close()
			ac.Conn = nil
		}
		ac.mu.Unlock()

		// 再关发送通道
		ac.sendMu.Lock()
		if ac.Send != nil {
			close(ac.Send)
			ac.Send = nil
		}
		ac.sendMu.Unlock()

		logPackage2.Printf(
			"Agent connection closed: device=%s conn=%p",
			ac.DeviceID,
			ac,
		)
	})
}
func (ac *AgentConn) SendMessage(message []byte) error {
	if ac.closed.Load() {
		return errorsPackage2.New("agent connection already closed")
	}

	ac.sendMu.Lock()
	defer ac.sendMu.Unlock()

	if ac.Send == nil {
		return errorsPackage2.New("agent connection closed")
	}

	for attempt := 0; attempt < 5; attempt++ {
		select {
		case ac.Send <- message:
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}

	logPackage2.Printf("Agent %s send buffer full after retries, dropping message (len=%d)",
		ac.DeviceID, len(message))
	return errorsPackage2.New("agent send buffer full")
}
func (ac *AgentConn) IsClosed() bool {
	return ac.closed.Load()
}

type AdminConn struct {
	Conn      *websocket.Conn
	Send      chan []byte
	SessionID string
	UserID    string
	closeOnce sync.Once
	sendMu    sync.Mutex
	closed    atomic.Bool
}

func NewAdminConn(conn *websocket.Conn) *AdminConn {
	return &AdminConn{
		Conn: conn,
		Send: make(chan []byte, sendBufferSize),
	}
}
func (ac *AdminConn) Close() {
	ac.closeOnce.Do(func() {
		ac.closed.Store(true)

		ac.sendMu.Lock()
		if ac.Send != nil {
			close(ac.Send)
			ac.Send = nil
		}
		ac.sendMu.Unlock()

		if ac.Conn != nil {
			ac.Conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			ac.Conn.Close()
			ac.Conn = nil
		}
	})
}
func (ac *AdminConn) IsClosed() bool {
	return ac.closed.Load()
}
func (ac *AdminConn) SendMessage(message []byte) error {

	ac.sendMu.Lock()
	defer ac.sendMu.Unlock()

	if ac.closed.Load() {
		return errorsPackage2.New("admin connection already closed")
	}

	if ac.Send == nil {
		return errorsPackage2.New("admin send channel is nil")
	}

	select {
	case ac.Send <- message:
		return nil

	default:
		return errorsPackage2.New("admin send buffer full")
	}
}
// UpdateLastSeen 更新最后活跃时间
func (ac *AgentConn) UpdateLastSeen() {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.LastSeen = time.Now()
}

// WebSocketHub WebSocket连接管理中心
type WebSocketHub struct {
	// Agent连接管理
	agents   map[string]*AgentConn
	agentsMu sync.RWMutex

	// Admin连接管理
	admins   map[*AdminConn]bool
	adminsMu sync.RWMutex

	// Redis 客户端（可选，Hub 扩展能力）
	redis *redisPackage2.Client

	// 活动日志仓储（处理 activity.real_time）
	activityRepo *ActivityRepository

	// 设备仓储（同步 WS 活跃时间到 DB）
	deviceRepo *DeviceRepository
	lastSeenMu sync.Mutex
	lastSeenAt map[string]time.Time

	// 通道
	register   chan *AgentConn
	unregister chan *AgentConn
	broadcast  chan []byte

	// 广播并发限制（避免 1000 Agent 时 goroutine 风暴）
	broadcastSem chan struct{}

	// 清理控制
	stopCleanup chan struct{}
	cleanupOnce sync.Once

	// 关键消息可靠投递（策略/命令 ACK + 重试）
	critical *criticalDelivery

	// 远程查看（LiveKit 会话编排）
	remoteViewSvc *RemoteViewService

	// 诊断中心
	diagnosticSvc    *DiagnosticService
	clientProtection *ClientProtectionService

	// Session SSOT
	sessionSvc *SessionService

	// Connection cap reservation (prevents upgrade TOCTOU).
	connSlotsMu   sync.Mutex
	reservedSlots int
}

const agentLastSeenDBInterval = 60 * time.Second

// SyncAgentLastSeen 将 Agent WS 活跃时间写入数据库（节流，避免频繁写库）
func (h *WebSocketHub) SyncAgentLastSeen(deviceID string, force bool) {
	if h.deviceRepo == nil || deviceID == "" {
		return
	}

	now := time.Now()
	if !force {
		h.lastSeenMu.Lock()
		if last, ok := h.lastSeenAt[deviceID]; ok && now.Sub(last) < agentLastSeenDBInterval {
			h.lastSeenMu.Unlock()
			return
		}
		h.lastSeenAt[deviceID] = now
		h.lastSeenMu.Unlock()
	} else {
		h.lastSeenMu.Lock()
		h.lastSeenAt[deviceID] = now
		h.lastSeenMu.Unlock()
	}

	deviceUUID, err := uuid.Parse(deviceID)
	if err != nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := h.deviceRepo.UpdateLastSeen(ctx, deviceUUID); err != nil {
			logPackage2.Printf("[WebSocketHub] Failed to update last_seen_at for device %s: %v", deviceID, err)
		}
	}()
}

// NewWebSocketHub 创建新的WebSocketHub
func NewWebSocketHub() *WebSocketHub {
	return NewWebSocketHubWithRedis(nil)
}

// NewWebSocketHubWithRedis 创建带有 Redis 的 WebSocketHub
func NewWebSocketHubWithRedis(redis *redisPackage2.Client) *WebSocketHub {
	hub := &WebSocketHub{
		agents:       make(map[string]*AgentConn),
		admins:       make(map[*AdminConn]bool),
		redis:        redis,
		lastSeenAt:   make(map[string]time.Time),
		register:     make(chan *AgentConn, 256),
		unregister:   make(chan *AgentConn, 256),
		broadcast:    make(chan []byte, 1024),
		broadcastSem: make(chan struct{}, 32),
		stopCleanup:  make(chan struct{}),
	}

	go hub.run()
	go hub.cleanupStaleConnections()
	hub.critical = newCriticalDelivery(hub)

	return hub
}

// run 主事件循环
func (h *WebSocketHub) run() {
	for {
		select {
		case agent := <-h.register:
			h.registerAgent(agent)

		case agent := <-h.unregister: // ✅ 接收 *AgentConn
			h.unregisterAgent(agent)

		case message := <-h.broadcast:
			h.broadcastToAll(message)
		}
	}
}

// registerAgent 注册Agent连接
func (h *WebSocketHub) registerAgent(agent *AgentConn) {
	var oldAgent *AgentConn

	h.agentsMu.Lock()
	if old, exists := h.agents[agent.DeviceID]; exists {
		oldAgent = old
	}
	h.agents[agent.DeviceID] = agent
	total := len(h.agents)
	GlobalMetrics.WSAgentConns.Store(int64(total))
	h.agentsMu.Unlock()

	if oldAgent != nil {
		logPackage2.Printf("Replacing old connection for device: %s", agent.DeviceID)
		h.enqueueUnregister(oldAgent)
	}

	logPackage2.Printf("Agent registered: %s, total agents: %d", agent.DeviceID, total)
	h.ReleaseReservedConnectionSlot()
	h.SyncAgentLastSeen(agent.DeviceID, true)

	if h.critical != nil {
		h.critical.onAgentRegistered(agent.DeviceID)
	}

	if h.diagnosticSvc != nil {
		h.diagnosticSvc.PushConfigToDevice(agent.DeviceID)
	}
	if h.clientProtection != nil {
		h.clientProtection.OnAgentRegistered(agent.DeviceID)
	}

	// 通知所有Admin设备上线
	h.broadcastToAdmins(gin.H{
		"type":     "device_status",
		"deviceId": agent.DeviceID,
		"status":   "online",
	})
}

// unregisterAgent 注销Agent连接
func (h *WebSocketHub) unregisterAgent(agent *AgentConn) {
	if agent == nil {
		return
	}

	deviceID := agent.DeviceID

	h.agentsMu.Lock()

	current, exists := h.agents[deviceID]
	if !exists || current != agent {
		h.agentsMu.Unlock()

		agent.Close()
		return
	}

	delete(h.agents, deviceID)

	remaining := len(h.agents)
	GlobalMetrics.WSAgentConns.Store(int64(remaining))

	h.agentsMu.Unlock()

	agent.Close()

	if h.remoteViewSvc != nil {
		h.remoteViewSvc.OnAgentDisconnected(context.Background(), deviceID)
	}

	h.broadcastToAdmins(gin.H{
		"type":     "device_status",
		"deviceId": deviceID,
		"status":   "offline",
	})

	logPackage2.Printf(
		"Agent unregistered: device=%s, conn=%p, remaining agents=%d",
		deviceID,
		agent,
		remaining,
	)
}

// broadcastToAll 广播消息给所有Agent和Admin（Hub 主循环快速返回，发送并发受限）
func (h *WebSocketHub) broadcastToAll(message []byte) {
	h.agentsMu.RLock()
	agents := make([]*AgentConn, 0, len(h.agents))
	for _, agent := range h.agents {
		agents = append(agents, agent)
	}
	h.agentsMu.RUnlock()

	for _, agent := range agents {
		h.dispatchAgentMessage(agent, message)
	}

	h.adminsMu.RLock()
	admins := make([]*AdminConn, 0, len(h.admins))
	for admin := range h.admins {
		admins = append(admins, admin)
	}
	h.adminsMu.RUnlock()

	for _, admin := range admins {
		target := admin
		go func() {
			if err := target.SendMessage(message); err != nil {
				logPackage2.Printf("Admin send buffer full or closed, skipping: %v", err)
			}
		}()
	}
}
func (h *WebSocketHub) dispatchAgentMessage(agent *AgentConn, message []byte) {
	target := agent
	h.broadcastSem <- struct{}{}
	go func() {
		defer func() { <-h.broadcastSem }()
		if err := target.SendMessage(message); err != nil {
			logPackage2.Printf("Agent %s send buffer full or closed, skipping: %v", target.DeviceID, err)
		}
	}()
}

// broadcastToAdmins 广播消息给所有Admin（不发送给Agent）
func (h *WebSocketHub) broadcastToAdmins(message interface{}) {
	payload, err := json.Marshal(message)
	if err != nil {
		logPackage2.Printf("Failed to marshal admin broadcast: %v", err)
		return
	}

	h.adminsMu.RLock()
	for admin := range h.admins {
		if err := admin.SendMessage(payload); err != nil {
			logPackage2.Printf("Admin send buffer full or closed: %v", err)
		}
	}
	h.adminsMu.RUnlock()
}

// cleanupStaleConnections 定期清理过期连接
func (h *WebSocketHub) cleanupStaleConnections() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCleanup:
			logPackage2.Println("Cleanup routine stopped")
			return
		case <-ticker.C:
			h.performCleanup()
		}
	}
}

// performCleanup 执行清理操作
func (h *WebSocketHub) performCleanup() {
	h.agentsMu.RLock()

	var toRemove []*AgentConn
	maxMissedPongs := 3

	for deviceID, agent := range h.agents {
		missed := atomic.LoadInt32(&agent.MissedPongs)

		if agent.closed.Load() {
			toRemove = append(toRemove, agent)
			logPackage2.Printf("Device %s (conn=%s) already closed, scheduling map cleanup", deviceID, agent.ConnID)
			continue
		}

		if missed >= int32(maxMissedPongs) {
			toRemove = append(toRemove, agent)
			logPackage2.Printf("Device %s (conn=%s) scheduled for cleanup after %d missed heartbeats",
				deviceID, agent.ConnID, missed)
		} else {
			atomic.AddInt32(&agent.MissedPongs, 1)
		}
	}

	h.agentsMu.RUnlock()

	for _, agent := range toRemove {
		h.enqueueUnregister(agent)
	}
}

// enqueueUnregister 将连接移出在线表并关闭，channel 满时同步清理
func (h *WebSocketHub) enqueueUnregister(agent *AgentConn) {
	if agent == nil {
		return
	}

	select {
	case h.unregister <- agent:
	default:
		h.removeAgentFromMap(agent)
	}
}

// removeAgentFromMap 同步从在线表移除并关闭连接
func (h *WebSocketHub) removeAgentFromMap(agent *AgentConn) {
	deviceID := agent.DeviceID

	h.agentsMu.Lock()
	current, exists := h.agents[deviceID]
	if exists && current == agent {
		delete(h.agents, deviceID)
	}
	h.agentsMu.Unlock()

	agent.Close()

	if exists && current == agent {
		if h.remoteViewSvc != nil {
			h.remoteViewSvc.OnAgentDisconnected(context.Background(), deviceID)
		}
		h.broadcastToAdmins(gin.H{
			"type":     "device_status",
			"deviceId": deviceID,
			"status":   "offline",
		})
		logPackage2.Printf("Agent removed synchronously: device=%s, conn=%s", deviceID, agent.ConnID)
	}
}

// Stop 停止WebSocketHub
func (h *WebSocketHub) Stop() {
	h.cleanupOnce.Do(func() {
		close(h.stopCleanup)
	})

	if h.critical != nil {
		h.critical.stop()
	}

	// 关闭所有Agent连接
	h.agentsMu.Lock()
	for _, agent := range h.agents {
		agent.Close()
	}
	h.agents = make(map[string]*AgentConn)
	h.agentsMu.Unlock()

	// 关闭所有Admin连接
	h.adminsMu.Lock()
	for admin := range h.admins {
		admin.Close()
	}
	h.admins = make(map[*AdminConn]bool)
	h.adminsMu.Unlock()

	logPackage2.Println("WebSocket hub stopped")
}

// SendCommandToDevice 向指定设备发送命令（可靠投递 + ACK）
func (h *WebSocketHub) SendCommandToDevice(deviceID string, command string, params gin.H) {
	if params == nil {
		params = gin.H{}
	}
	h.SendCriticalToAgent(deviceID, gin.H{
		"type":    "command",
		"command": command,
		"params":  params,
	})
}
// BroadcastToAgent 向指定Agent发送消息
func (h *WebSocketHub) BroadcastToAgent(deviceID string, message interface{}) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}

	h.agentsMu.RLock()
	agent, exists := h.agents[deviceID]
	h.agentsMu.RUnlock()

	if exists {
		if err := agent.SendMessage(payload); err != nil {
			logPackage2.Printf("Send buffer full for device %s: %v", deviceID, err)
		}
	}
}

// ForceDisconnectAgent 通知客户端会话失效并断开 WebSocket
func (h *WebSocketHub) ForceDisconnectAgent(deviceID, reason string) {
	h.BroadcastToAgent(deviceID, gin.H{
		"type":   "device_revoked",
		"reason": reason,
	})

	h.agentsMu.RLock()
	agent, exists := h.agents[deviceID]
	h.agentsMu.RUnlock()
	if exists {
		h.removeAgentFromMap(agent)
	}
}

// BroadcastToAdmin 广播到Admin（兼容旧接口名）
func (h *WebSocketHub) BroadcastToAdmin(message interface{}) {
	h.broadcastToAdmins(message)
}

// GetOnlineDevices 获取在线设备列表
func (h *WebSocketHub) GetOnlineDevices() []string {
	h.agentsMu.RLock()
	defer h.agentsMu.RUnlock()

	devices := make([]string, 0, len(h.agents))
	for id := range h.agents {
		devices = append(devices, id)
	}
	return devices
}

// ReleaseReservedConnectionSlot releases a reserved slot when upgrade/register fails.
func (h *WebSocketHub) ReleaseReservedConnectionSlot() {
	h.connSlotsMu.Lock()
	defer h.connSlotsMu.Unlock()
	if h.reservedSlots > 0 {
		h.reservedSlots--
	}
}
func (h *WebSocketHub) totalConnectionsLocked() int {
	h.adminsMu.RLock()
	adminCount := len(h.admins)
	h.adminsMu.RUnlock()
	h.agentsMu.RLock()
	agentCount := len(h.agents)
	h.agentsMu.RUnlock()
	return adminCount + agentCount
}

// GetConnectionCounts returns admin and agent websocket connection counts.
func (h *WebSocketHub) GetConnectionCounts() (adminCount, agentCount int) {
	h.adminsMu.RLock()
	adminCount = len(h.admins)
	h.adminsMu.RUnlock()
	h.agentsMu.RLock()
	agentCount = len(h.agents)
	h.agentsMu.RUnlock()
	return adminCount, agentCount
}

// TotalConnections returns admin + agent WebSocket connection count.
func (h *WebSocketHub) TotalConnections() int {
	return h.totalConnectionsLocked()
}

// HasAnyAgentOnline reports whether at least one Agent is connected. Callers use
// it to skip device lookups whose only purpose is to push a command to an Agent.
func (h *WebSocketHub) HasAnyAgentOnline() bool {
	h.agentsMu.RLock()
	defer h.agentsMu.RUnlock()
	return len(h.agents) > 0
}

// IsAgentOnline 检查Agent是否在线
func (h *WebSocketHub) IsAgentOnline(deviceID string) bool {
	h.agentsMu.RLock()
	defer h.agentsMu.RUnlock()
	_, exists := h.agents[deviceID]
	return exists
}

// DisconnectAdminsBySession closes admin WebSocket connections bound to a session.
func (h *WebSocketHub) DisconnectAdminsBySession(sessionID, reason string) {
	if sessionID == "" {
		return
	}
	h.adminsMu.RLock()
	targets := make([]*AdminConn, 0)
	for admin := range h.admins {
		if admin.SessionID == sessionID {
			targets = append(targets, admin)
		}
	}
	h.adminsMu.RUnlock()

	payload, _ := json.Marshal(gin.H{
		"type":   "session_revoked",
		"reason": reason,
	})
	for _, admin := range targets {
		_ = admin.SendMessage(payload)
		GlobalMetrics.ObserveWSDrop(reason)
		admin.Close()
		h.adminsMu.Lock()
		delete(h.admins, admin)
		GlobalMetrics.WSAdminConns.Store(int64(len(h.admins)))
		h.adminsMu.Unlock()
	}
}

// DisconnectAgentsBySession closes agent WebSocket connections bound to a session.
func (h *WebSocketHub) DisconnectAgentsBySession(sessionID, reason string) {
	if sessionID == "" {
		return
	}
	h.agentsMu.RLock()
	targets := make([]*AgentConn, 0)
	for _, agent := range h.agents {
		if agent.SessionID == sessionID {
			targets = append(targets, agent)
		}
	}
	h.agentsMu.RUnlock()

	for _, agent := range targets {
		GlobalMetrics.ObserveWSDrop(reason)
		h.ForceDisconnectAgent(agent.DeviceID, reason)
	}
}
const (
	maxAgentReadSize            = 1 << 20 // 1 MiB — 对齐 Agent MaxWebSocketMessageSize
	criticalDeliveryMaxAttempts = 8
	criticalDeliveryRetryEvery  = 15 * time.Second
	criticalDeliveryAckTimeout  = 45 * time.Second
	criticalSendRetryCount      = 12
	criticalSendRetryWait       = 200 * time.Millisecond
)

type criticalPendingMsg struct {
	DeviceID   string
	Payload    []byte
	Attempts   int
	CreatedAt  time.Time
	LastSentAt time.Time
}

// criticalDelivery 跟踪策略/命令等关键消息，直到收到 Agent ACK 或耗尽重试
type criticalDelivery struct {
	hub     *WebSocketHub
	pending map[string]*criticalPendingMsg
	mu      sync.Mutex
	stopCh  chan struct{}
}

func newCriticalDelivery(hub *WebSocketHub) *criticalDelivery {
	cd := &criticalDelivery{
		hub:     hub,
		pending: make(map[string]*criticalPendingMsg),
		stopCh:  make(chan struct{}),
	}
	go cd.retryLoop()
	return cd
}
func (cd *criticalDelivery) stop() {
	select {
	case <-cd.stopCh:
	default:
		close(cd.stopCh)
	}
}

// SendCriticalToAgent 向单设备发送需 ACK 的关键消息（policy_update / command 等）
func (h *WebSocketHub) SendCriticalToAgent(deviceID string, message gin.H) {
	if h == nil || h.critical == nil || deviceID == "" {
		return
	}
	h.critical.enqueue(deviceID, message)
}

func (cd *criticalDelivery) enqueue(deviceID string, message gin.H) {
	messageID := uuid.New().String()
	message["messageId"] = messageID

	payload, err := json.Marshal(message)
	if err != nil {
		logPackage2.Printf("critical delivery marshal failed for device %s: %v", deviceID, err)
		return
	}

	now := time.Now()
	cd.mu.Lock()
	cd.pending[messageID] = &criticalPendingMsg{
		DeviceID:   deviceID,
		Payload:    payload,
		CreatedAt:  now,
		LastSentAt: time.Time{},
	}
	cd.mu.Unlock()

	cd.tryDeliver(messageID)
}
func (cd *criticalDelivery) onAgentRegistered(deviceID string) {
	cd.mu.Lock()
	var ids []string
	for id, p := range cd.pending {
		if p.DeviceID == deviceID {
			ids = append(ids, id)
		}
	}
	cd.mu.Unlock()

	for _, id := range ids {
		cd.tryDeliver(id)
	}
}
func (cd *criticalDelivery) tryDeliver(messageID string) bool {
	cd.mu.Lock()
	p, ok := cd.pending[messageID]
	if !ok {
		cd.mu.Unlock()
		return true
	}
	if p.Attempts >= criticalDeliveryMaxAttempts {
		logPackage2.Printf("critical message %s to device %s dropped after %d attempts",
			messageID, p.DeviceID, p.Attempts)
		delete(cd.pending, messageID)
		cd.mu.Unlock()
		return false
	}
	deviceID := p.DeviceID
	payload := p.Payload
	cd.mu.Unlock()

	cd.hub.agentsMu.RLock()
	agent, exists := cd.hub.agents[deviceID]
	cd.hub.agentsMu.RUnlock()

	if !exists {
		return false
	}

	if err := sendCriticalMessage(agent, payload); err != nil {
		logPackage2.Printf("critical send failed device=%s messageId=%s: %v", deviceID, messageID, err)
		cd.mu.Lock()
		if pp, ok := cd.pending[messageID]; ok {
			pp.Attempts++
		}
		cd.mu.Unlock()
		return false
	}

	now := time.Now()
	cd.mu.Lock()
	if pp, ok := cd.pending[messageID]; ok {
		pp.Attempts++
		pp.LastSentAt = now
	}
	cd.mu.Unlock()
	return true
}
func sendCriticalMessage(agent *AgentConn, payload []byte) error {
	if agent == nil || agent.IsClosed() {
		return errAgentConnectionClosed
	}

	agent.sendMu.Lock()
	defer agent.sendMu.Unlock()

	if agent.Send == nil {
		return errAgentConnectionClosed
	}

	for attempt := 0; attempt < criticalSendRetryCount; attempt++ {
		select {
		case agent.Send <- payload:
			return nil
		case <-time.After(criticalSendRetryWait):
		}
	}
	return errAgentSendBufferFull
}

var (
	errAgentConnectionClosed = errorsPackage2.New("agent connection closed")
	errAgentSendBufferFull   = errorsPackage2.New("agent send buffer full")
)

func (cd *criticalDelivery) retryLoop() {
	ticker := time.NewTicker(criticalDeliveryRetryEvery)
	defer ticker.Stop()

	for {
		select {
		case <-cd.stopCh:
			return
		case <-ticker.C:
			cd.retryDue()
		}
	}
}
func (cd *criticalDelivery) retryDue() {
	now := time.Now()

	cd.mu.Lock()
	ids := make([]string, 0, len(cd.pending))
	for id, p := range cd.pending {
		if p.Attempts >= criticalDeliveryMaxAttempts {
			continue
		}
		if p.LastSentAt.IsZero() {
			if now.Sub(p.CreatedAt) >= criticalDeliveryRetryEvery {
				ids = append(ids, id)
			}
			continue
		}
		if now.Sub(p.LastSentAt) >= criticalDeliveryAckTimeout {
			ids = append(ids, id)
		}
	}
	cd.mu.Unlock()

	for _, id := range ids {
		cd.tryDeliver(id)
	}
}

// RegisterAgentForTest injects an agent connection (integration tests only).
func (h *WebSocketHub) RegisterAgentForTest(agent *AgentConn) {
	if h == nil || agent == nil {
		return
	}
	h.registerAgent(agent)
}

// RegisterAdminForTest injects an admin connection (integration tests only).
func (h *WebSocketHub) RegisterAdminForTest(admin *AdminConn) {
	if h == nil || admin == nil {
		return
	}
	h.adminsMu.Lock()
	h.admins[admin] = true
	GlobalMetrics.WSAdminConns.Store(int64(len(h.admins)))
	h.adminsMu.Unlock()
}
