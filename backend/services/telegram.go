// Consolidated source; original file boundaries are marked below.
package services

import (
	bytes "bytes"
	context "context"
	rand "crypto/rand"
	sql "database/sql"
	hex "encoding/hex"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	uuid "github.com/google/uuid"
	pq "github.com/lib/pq"
	excelize "github.com/xuri/excelize/v2"
	htmlPackage2 "html"
	io "io"
	log "log"
	math "math"
	multipart "mime/multipart"
	http "net/http"
	os "os"
	filepath "path/filepath"
	sort "sort"
	strconv "strconv"
	strings "strings"
	sync "sync"
	tgbot "tg-attendance/backend/internal/tgbot"
	security "tg-attendance/backend/security"
	time "time"
	unicode "unicode"
)

// ---- Source: backend/services/workforce_telegram.go ----
type WorkforceTelegramService struct {
	db          *sql.DB
	workforce   *WorkforceService
	wsHub       *WebSocketHub
	penaltyPush *PenaltyPushService
	agentNotify *AgentNotificationService

	// Short-lived copy of the settings row shared across worker requests
	// (see loadTelegramSettingsCached).
	settingsMu       sync.RWMutex
	settingsCached   *TelegramSettings
	settingsCachedAt time.Time
}

func NewWorkforceTelegramService(db *sql.DB, workforce *WorkforceService) *WorkforceTelegramService {
	return &WorkforceTelegramService{
		db:          db,
		workforce:   workforce,
		penaltyPush: NewPenaltyPushService(db),
	}
}

// mapLookupBool reads a boolean from a JSON map under any of the given keys.
// Accepts bool, 0/1 numbers, and common string forms so settings toggles are not
// silently ignored when the JSON type is not a strict bool.
func mapLookupBool(in map[string]interface{}, keys ...string) (bool, bool) {
	if in == nil {
		return false, false
	}
	for _, key := range keys {
		raw, exists := in[key]
		if !exists || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case bool:
			return v, true
		case float64:
			return v != 0, true
		case json.Number:
			n, err := v.Int64()
			if err != nil {
				f, fErr := v.Float64()
				if fErr != nil {
					continue
				}
				return f != 0, true
			}
			return n != 0, true
		case int:
			return v != 0, true
		case int64:
			return v != 0, true
		case string:
			s := strings.TrimSpace(strings.ToLower(v))
			switch s {
			case "1", "true", "yes", "on":
				return true, true
			case "0", "false", "no", "off", "":
				return false, true
			}
		}
	}
	return false, false
}
// pushAutoPenaltyNotify sends TG-generated fines to the 罚款推送频道 when enabled.
// Non-blocking; failures are logged and do not affect attendance flow.
func (s *WorkforceTelegramService) pushAutoPenaltyNotify(
	ctx context.Context,
	empID uuid.UUID,
	amount float64,
	category, reason, penaltyDate string,
) {
	if s == nil || s.penaltyPush == nil || amount <= 0 {
		return
	}
	var name, position string
	_ = s.db.QueryRowContext(ctx, `
		SELECT name, COALESCE(position,'') FROM employees WHERE id = $1`, empID).
		Scan(&name, &position)
	if strings.TrimSpace(name) == "" {
		return
	}
	rec := PenaltyPushRecord{
		Date:         strings.TrimSpace(penaltyDate),
		EmployeeID:   empID.String(),
		EmployeeName: name,
		Position:     position,
		Amount:       amount,
		Category:     category,
		Reason:       reason,
	}
	go func(r PenaltyPushRecord, eid uuid.UUID) {
		ok, err := s.penaltyPush.PushPenaltyRecord(context.Background(), r)
		if err != nil {
			log.Printf("[Telegram] auto penalty push failed category=%s emp=%s: %v", r.Category, r.EmployeeName, err)
			return
		}
		if ok {
			log.Printf("[Telegram] auto penalty push sent category=%s amount=%.2f emp=%s", r.Category, r.Amount, r.EmployeeName)
		}
		if s.agentNotify != nil {
			s.agentNotify.PushPenalty(eid, r.Amount, r.Category, r.Reason, r.Date, r.EmployeeName)
		}
	}(rec, empID)
}

type TelegramSettings struct {
	ID                               uuid.UUID
	Enabled                          bool
	BotUsername                      string
	BotID                            int64
	HasBotToken                      bool
	Timezone                         string
	DayStart                         string
	DayEnd                           string
	NightStart                       string
	NightEnd                         string
	MiddleStart                      string
	MiddleEnd                        string
	GraceMinutes                     int
	GraceMinutesAfter                int
	CheckOutGraceBefore              int
	LateToleranceMinutes             int
	EarlyLeaveToleranceMinutes       int
	DualShiftEnabled                 bool
	TripleShiftEnabled               bool
	DailyResetTime                   string
	DailyResetEnabled                bool
	DailyResetExecutionOffsetMinutes int
	ShiftWindowDisabled              bool
	WorkEndGraceMinutes              int
	WorkEndGraceAfter                int
	RequirePCLogin                   bool
	CaptureLinkedToCheckin           bool
	// WebPunchOnly: when true, attendance is driven by web admin punch; TG/PC punch is rejected.
	WebPunchOnly         bool
	AdminTelegramUserIDs string
	BotMode              string
	WebhookURL           string
	HasWebhookSecret     bool
	BotUILang            string
	// FineCurrency: ISO-like code for TG fine display (CNY/THB/USD). Amounts stay numeric.
	FineCurrency string
	// ShowKeyboardInfoButtons: 我的记录 / 排行榜 / 导出数据 on the TG reply keyboard.
	ShowKeyboardInfoButtons bool
	UpdatedAt               time.Time
	// Credentials ride the same row read so worker auth and /config reuse one
	// round trip. Unexported: never serialised and never exposed by GetSettings callers.
	workerSecret  string
	webhookSecret string
	botTokenEnc   string
}
type TelegramGroupRow struct {
	ID                         uuid.UUID
	ChatID                     string
	ChatTitle                  string
	Enabled                    bool
	ShiftGraceBefore           *int
	ShiftGraceAfter            *int
	CheckOutGraceBefore        *int
	CheckOutGraceAfter         *int
	LateToleranceMinutes       *int
	EarlyLeaveToleranceMinutes *int
	DailyResetTime             string
	BotUILang                  string
	CreatedAt                  time.Time
}
type TelegramBindingRow struct {
	ID               uuid.UUID
	EmployeeID       uuid.UUID
	EmployeeName     string
	EmployeeCode     string
	AttendanceMode   string
	TelegramUserID   int64
	TelegramUsername string
	ChatID           string
	BoundAt          time.Time
	Status           int
}

func (s *WorkforceTelegramService) IsActive(ctx context.Context) (bool, error) {
	if !TelegramAttendanceEnabled() {
		return false, nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	return settings.Enabled && settings.HasBotToken, nil
}

// IsWebPunchOnly reports whether TG/PC punch channels are disabled in favor of web admin punch.
func (s *WorkforceTelegramService) IsWebPunchOnly(ctx context.Context) bool {
	settings, err := s.GetSettings(ctx)
	if err != nil || settings == nil {
		return false
	}
	return settings.WebPunchOnly
}

// ensureAttendanceModuleAvailable allows web punch when web_punch_only is on even without bot token.
func (s *WorkforceTelegramService) ensureAttendanceModuleAvailable(ctx context.Context) error {
	if !TelegramAttendanceEnabled() {
		return ErrTelegramDisabled
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if settings.WebPunchOnly {
		return nil
	}
	if !settings.Enabled || !settings.HasBotToken {
		return ErrTelegramDisabled
	}
	return nil
}

// rejectIfWebPunchOnly blocks TG Bot / PC agent punch when web-only mode is enabled.
func (s *WorkforceTelegramService) rejectIfWebPunchOnly(ctx context.Context) error {
	if s.IsWebPunchOnly(ctx) {
		return ErrWebPunchOnlyMode
	}
	return nil
}

// RejectIfWebPunchOnly is the exported form for HTTP handlers (PC agent checkout etc.).
func (s *WorkforceTelegramService) RejectIfWebPunchOnly(ctx context.Context) error {
	return s.rejectIfWebPunchOnly(ctx)
}

// Cache only immutable rule/identity reads within ONE worker HTTP request.
// Never cache open sessions, limits, balances or results across punches.
type telegramRequestCacheKey struct{}
type telegramReadKey struct {
	service *WorkforceTelegramService
	name    string
}
type telegramReadEntry struct {
	mu     sync.Mutex
	value  interface{}
	loaded bool
}

func WithTelegramRequestCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(telegramRequestCacheKey{}).(*sync.Map); ok {
		return ctx
	}
	return context.WithValue(ctx, telegramRequestCacheKey{}, &sync.Map{})
}
func telegramRequestRead[T any](ctx context.Context, s *WorkforceTelegramService, key string, read func() (T, error)) (T, error) {
	cache, ok := ctx.Value(telegramRequestCacheKey{}).(*sync.Map)
	if !ok {
		return read()
	}
	raw, _ := cache.LoadOrStore(telegramReadKey{s, key}, &telegramReadEntry{})
	entry := raw.(*telegramReadEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.loaded {
		return entry.value.(T), nil
	}
	value, err := read()
	if err == nil {
		entry.value = value
		entry.loaded = true
	}
	return value, err
}

func (s *WorkforceTelegramService) GetSettings(ctx context.Context) (*TelegramSettings, error) {
	value, err := telegramRequestRead(ctx, s, "settings", func() (*TelegramSettings, error) { return s.loadTelegramSettingsCached(ctx) })
	if err != nil {
		return nil, err
	}
	copy := *value
	return &copy, nil
}

// The settings row (including the worker secret) is identical for every worker
// request, yet each request paid a database round trip for it (~40ms x 3-4 per
// punch). Keep it for a few seconds; every writer in this service clears it, so
// the only staleness is a direct database edit or a read racing a save, both
// bounded by telegramSettingsTTL.
const telegramSettingsTTL = 3 * time.Second

func (s *WorkforceTelegramService) loadTelegramSettingsCached(ctx context.Context) (*TelegramSettings, error) {
	s.settingsMu.RLock()
	cached, at := s.settingsCached, s.settingsCachedAt
	s.settingsMu.RUnlock()
	if cached != nil && time.Since(at) < telegramSettingsTTL {
		return cached, nil
	}
	value, err := s.loadTelegramSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.settingsMu.Lock()
	s.settingsCached, s.settingsCachedAt = value, time.Now()
	s.settingsMu.Unlock()
	return value, nil
}
func (s *WorkforceTelegramService) invalidateTelegramSettingsCache() {
	s.settingsMu.Lock()
	s.settingsCached = nil
	s.settingsMu.Unlock()
}

func (s *WorkforceTelegramService) loadTelegramSettings(ctx context.Context) (*TelegramSettings, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, enabled, COALESCE(bot_username,''), COALESCE(bot_id,0),
			CASE WHEN bot_token_enc IS NOT NULL AND bot_token_enc != '' THEN TRUE ELSE FALSE END,
			timezone, day_start, day_end, COALESCE(night_start,'21:00'), COALESCE(night_end,'09:00'),
			COALESCE(middle_start,'13:00'), COALESCE(middle_end,'01:00'),
			grace_minutes, COALESCE(grace_minutes_after, 360),
			COALESCE(check_out_grace_before, 120),
			COALESCE(late_tolerance_minutes, 0),
			COALESCE(early_leave_tolerance_minutes, 10),
			dual_shift_enabled, COALESCE(triple_shift_enabled, FALSE),
			COALESCE(daily_reset_time,'09:00'), COALESCE(daily_reset_enabled, TRUE),
			COALESCE(daily_reset_execution_offset_minutes, 120),
			COALESCE(shift_window_disabled, FALSE), COALESCE(work_end_grace_minutes, 5), COALESCE(work_end_grace_after, 360), COALESCE(require_pc_login, TRUE),
			COALESCE(capture_linked_to_checkin, FALSE),
			COALESCE(web_punch_only, FALSE),
			COALESCE(admin_telegram_user_ids,''),
			COALESCE(bot_mode,'polling'), COALESCE(webhook_url,''),
			CASE WHEN webhook_secret IS NOT NULL AND webhook_secret != '' THEN TRUE ELSE FALSE END,
			COALESCE(bot_ui_lang,'both'), COALESCE(NULLIF(TRIM(fine_currency), ''), 'CNY'),
			COALESCE(show_keyboard_info_buttons, FALSE), updated_at,
			COALESCE(worker_secret,''), COALESCE(webhook_secret,''), COALESCE(bot_token_enc,'')
		FROM wf_telegram_settings ORDER BY updated_at DESC, id DESC LIMIT 1`)
	var out TelegramSettings
	if err := row.Scan(&out.ID, &out.Enabled, &out.BotUsername, &out.BotID, &out.HasBotToken,
		&out.Timezone, &out.DayStart, &out.DayEnd, &out.NightStart, &out.NightEnd,
		&out.MiddleStart, &out.MiddleEnd,
		&out.GraceMinutes, &out.GraceMinutesAfter,
		&out.CheckOutGraceBefore, &out.LateToleranceMinutes, &out.EarlyLeaveToleranceMinutes,
		&out.DualShiftEnabled, &out.TripleShiftEnabled, &out.DailyResetTime, &out.DailyResetEnabled,
		&out.DailyResetExecutionOffsetMinutes,
		&out.ShiftWindowDisabled, &out.WorkEndGraceMinutes, &out.WorkEndGraceAfter, &out.RequirePCLogin, &out.CaptureLinkedToCheckin,
		&out.WebPunchOnly, &out.AdminTelegramUserIDs,
		&out.BotMode, &out.WebhookURL, &out.HasWebhookSecret, &out.BotUILang, &out.FineCurrency,
		&out.ShowKeyboardInfoButtons, &out.UpdatedAt,
		&out.workerSecret, &out.webhookSecret, &out.botTokenEnc); err != nil {
		return nil, err
	}
	out.FineCurrency = NormalizeFineCurrency(out.FineCurrency)
	if strings.TrimSpace(out.MiddleStart) == "" {
		out.MiddleStart = "13:00"
	}
	if strings.TrimSpace(out.MiddleEnd) == "" {
		out.MiddleEnd = "01:00"
	}
	return &out, nil
}

// MultiShiftEnabled reports whether day/night (and optionally middle) punch modes are on.
func (s *TelegramSettings) MultiShiftEnabled() bool {
	return s != nil && (s.DualShiftEnabled || s.TripleShiftEnabled)
}

// EnabledShiftTypes returns configured shift codes for export/filter/handover.
func (s *TelegramSettings) EnabledShiftTypes() []string {
	if s != nil && s.TripleShiftEnabled {
		return []string{"day", "middle", "night"}
	}
	if s != nil && s.DualShiftEnabled {
		return []string{"day", "night"}
	}
	return []string{"day"}
}
func (s *WorkforceTelegramService) UpdateSettings(ctx context.Context, in map[string]interface{}) (*TelegramSettings, error) {
	s.invalidateTelegramSettingsCache() // merge the edit against the stored row, never a cached copy
	current, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}

	enabled := current.Enabled
	if v, ok := mapLookupBool(in, "enabled"); ok {
		enabled = v
	}
	timezone := current.Timezone
	if v, ok := in["timezone"].(string); ok && strings.TrimSpace(v) != "" {
		timezone = strings.TrimSpace(v)
	}
	dayStart := current.DayStart
	if v, ok := in["dayStart"].(string); ok && strings.TrimSpace(v) != "" {
		dayStart = strings.TrimSpace(v)
	}
	dayEnd := current.DayEnd
	if v, ok := in["dayEnd"].(string); ok && strings.TrimSpace(v) != "" {
		dayEnd = strings.TrimSpace(v)
	}
	grace := current.GraceMinutes
	if v, ok := in["graceMinutes"].(float64); ok {
		grace = int(v)
	}
	if v, ok := in["checkInGraceBefore"].(float64); ok {
		grace = int(v)
	}
	graceAfter := current.GraceMinutesAfter
	if v, ok := in["graceMinutesAfter"].(float64); ok {
		graceAfter = int(v)
	}
	if v, ok := in["checkInGraceAfter"].(float64); ok {
		graceAfter = int(v)
	}
	checkOutBefore := current.CheckOutGraceBefore
	if v, ok := in["checkOutGraceBefore"].(float64); ok {
		checkOutBefore = int(v)
	}
	lateTolerance := current.LateToleranceMinutes
	if v, ok := in["lateToleranceMinutes"].(float64); ok {
		lateTolerance = int(v)
	}
	if v, ok := in["graceMinutesAfter"].(float64); ok {
		if _, hasLate := in["lateToleranceMinutes"]; !hasLate {
			if _, hasCheckIn := in["checkInGraceAfter"]; !hasCheckIn {
				// legacy-only payload: graceMinutesAfter still updates both window and tolerance
				lateTolerance = int(v)
			}
		}
	}
	earlyLeave := current.EarlyLeaveToleranceMinutes
	if v, ok := in["earlyLeaveToleranceMinutes"].(float64); ok {
		earlyLeave = int(v)
	}
	if v, ok := in["workEndGraceMinutes"].(float64); ok {
		earlyLeave = int(v)
	}
	dual := current.DualShiftEnabled
	if v, ok := mapLookupBool(in, "dualShiftEnabled", "dual_shift_enabled"); ok {
		dual = v
	}
	triple := current.TripleShiftEnabled
	if v, ok := mapLookupBool(in, "tripleShiftEnabled", "triple_shift_enabled"); ok {
		triple = v
	}
	if triple {
		// Triple mode builds on dual UI/windows; keep dual on when triple is enabled.
		dual = true
	}
	nightStart := current.NightStart
	if v, ok := in["nightStart"].(string); ok && strings.TrimSpace(v) != "" {
		nightStart = strings.TrimSpace(v)
	}
	nightEnd := current.NightEnd
	if v, ok := in["nightEnd"].(string); ok && strings.TrimSpace(v) != "" {
		nightEnd = strings.TrimSpace(v)
	}
	middleStart := current.MiddleStart
	if strings.TrimSpace(middleStart) == "" {
		middleStart = "13:00"
	}
	if v, ok := in["middleStart"].(string); ok && strings.TrimSpace(v) != "" {
		middleStart = strings.TrimSpace(v)
	}
	middleEnd := current.MiddleEnd
	if strings.TrimSpace(middleEnd) == "" {
		middleEnd = "01:00"
	}
	if v, ok := in["middleEnd"].(string); ok && strings.TrimSpace(v) != "" {
		middleEnd = strings.TrimSpace(v)
	}
	dailyResetTime := current.DailyResetTime
	if v, ok := in["dailyResetTime"].(string); ok && strings.TrimSpace(v) != "" {
		dailyResetTime = strings.TrimSpace(v)
	}
	dailyResetEnabled := current.DailyResetEnabled
	if v, ok := mapLookupBool(in, "dailyResetEnabled", "daily_reset_enabled"); ok {
		dailyResetEnabled = v
	}
	dailyResetExecutionOffset := current.DailyResetExecutionOffsetMinutes
	if v, ok := in["dailyResetExecutionOffsetMinutes"].(float64); ok {
		dailyResetExecutionOffset = int(v)
	}
	if dailyResetExecutionOffset < 0 {
		dailyResetExecutionOffset = 0
	}
	if dailyResetExecutionOffset > 24*60 {
		dailyResetExecutionOffset = 24 * 60
	}
	shiftWindowDisabled := current.ShiftWindowDisabled
	if v, ok := mapLookupBool(in, "shiftWindowDisabled", "shift_window_disabled"); ok {
		shiftWindowDisabled = v
	}
	workEndGraceAfter := current.WorkEndGraceAfter
	if v, ok := in["workEndGraceAfter"].(float64); ok {
		workEndGraceAfter = int(v)
	}
	if v, ok := in["checkOutGraceAfter"].(float64); ok {
		workEndGraceAfter = int(v)
	}
	if lateTolerance < 0 {
		lateTolerance = 0
	}
	if earlyLeave < 0 {
		earlyLeave = 0
	}
	requirePCLogin := current.RequirePCLogin
	if v, ok := mapLookupBool(in, "requirePCLogin", "require_pc_login"); ok {
		requirePCLogin = v
	}
	captureLinkedToCheckin := current.CaptureLinkedToCheckin
	if v, ok := mapLookupBool(in, "captureLinkedToCheckin", "capture_linked_to_checkin"); ok {
		captureLinkedToCheckin = v
	}
	webPunchOnly := current.WebPunchOnly
	if v, ok := mapLookupBool(in, "webPunchOnly", "web_punch_only"); ok {
		webPunchOnly = v
	}
	adminTelegramUserIDs := current.AdminTelegramUserIDs
	if v, ok := in["adminTelegramUserIds"].(string); ok {
		adminTelegramUserIDs = strings.TrimSpace(v)
	}
	botMode := current.BotMode
	if v, ok := in["botMode"].(string); ok && strings.TrimSpace(v) != "" {
		botMode = strings.ToLower(strings.TrimSpace(v))
	}
	webhookURL := current.WebhookURL
	if v, ok := in["webhookUrl"].(string); ok {
		webhookURL = strings.TrimSpace(v)
	}
	botUILang := current.BotUILang
	if v, ok := in["botUiLang"].(string); ok && strings.TrimSpace(v) != "" {
		botUILang = strings.ToLower(strings.TrimSpace(v))
	}
	fineCurrency := NormalizeFineCurrency(current.FineCurrency)
	if v, ok := in["fineCurrency"].(string); ok {
		fineCurrency = NormalizeFineCurrency(v)
	}
	showKeyboardInfoButtons := current.ShowKeyboardInfoButtons
	if v, ok := mapLookupBool(in, "showKeyboardInfoButtons", "show_keyboard_info_buttons"); ok {
		showKeyboardInfoButtons = v
	}

	botTokenEnc := sql.NullString{}
	botUsername := current.BotUsername
	botID := current.BotID
	if token, ok := in["botToken"].(string); ok && strings.TrimSpace(token) != "" {
		enc, err := EncryptSecret(strings.TrimSpace(token))
		if err != nil {
			return nil, err
		}
		botTokenEnc = sql.NullString{String: enc, Valid: true}
		me, err := telegramGetMe(strings.TrimSpace(token))
		if err != nil {
			return nil, fmt.Errorf("verify bot token: %w", err)
		}
		botUsername = me.Username
		botID = me.ID
	}

	workerSecret := sql.NullString{}
	if v, ok := in["regenerateWorkerSecret"].(bool); ok && v {
		secret, err := generateWorkerSecret()
		if err != nil {
			return nil, err
		}
		workerSecret = sql.NullString{String: secret, Valid: true}
	}
	webhookSecret := sql.NullString{}
	if v, ok := in["regenerateWebhookSecret"].(bool); ok && v {
		secret, err := generateWorkerSecret()
		if err != nil {
			return nil, err
		}
		webhookSecret = sql.NullString{String: secret, Valid: true}
	}

	query := `
		UPDATE wf_telegram_settings SET
			enabled = $1, timezone = $2, day_start = $3, day_end = $4,
			night_start = $5, night_end = $6, middle_start = $7, middle_end = $8,
			grace_minutes = $9, dual_shift_enabled = $10, triple_shift_enabled = $11,
			daily_reset_time = $12, daily_reset_enabled = $13,
			daily_reset_execution_offset_minutes = $14,
			shift_window_disabled = $15, work_end_grace_minutes = $16, work_end_grace_after = $17, require_pc_login = $18,
			capture_linked_to_checkin = $19, web_punch_only = $20,
			admin_telegram_user_ids = $21, grace_minutes_after = $22,
			check_out_grace_before = $23, late_tolerance_minutes = $24, early_leave_tolerance_minutes = $25,
			bot_mode = $26, webhook_url = $27, bot_ui_lang = $28, fine_currency = $29,
			show_keyboard_info_buttons = $30, updated_at = NOW()`
	args := []interface{}{
		enabled, timezone, dayStart, dayEnd, nightStart, nightEnd, middleStart, middleEnd,
		grace, dual, triple, dailyResetTime, dailyResetEnabled, dailyResetExecutionOffset,
		shiftWindowDisabled, earlyLeave, workEndGraceAfter, requirePCLogin, captureLinkedToCheckin,
		webPunchOnly, adminTelegramUserIDs, graceAfter, checkOutBefore, lateTolerance, earlyLeave,
		botMode, webhookURL, botUILang, fineCurrency, showKeyboardInfoButtons,
	}
	argN := 31
	if botTokenEnc.Valid {
		query += fmt.Sprintf(", bot_token_enc = $%d, bot_username = $%d, bot_id = $%d", argN, argN+1, argN+2)
		args = append(args, botTokenEnc.String, botUsername, botID)
		argN += 3
	}
	if workerSecret.Valid {
		query += fmt.Sprintf(", worker_secret = $%d", argN)
		args = append(args, workerSecret.String)
		argN++
	}
	if webhookSecret.Valid {
		query += fmt.Sprintf(", webhook_secret = $%d", argN)
		args = append(args, webhookSecret.String)
		argN++
	}
	query += fmt.Sprintf(" WHERE id = $%d", argN)
	args = append(args, current.ID)

	_, execErr := s.db.ExecContext(ctx, query, args...)
	s.invalidateTelegramSettingsCache()
	if execErr != nil {
		return nil, execErr
	}
	if botTokenEnc.Valid {
		if _, err := s.EnsureWorkerSecret(ctx); err != nil {
			return nil, err
		}
	}
	return s.GetSettings(ctx)
}
func (s *WorkforceTelegramService) GetWebhookSecret(ctx context.Context) (string, error) {
	var secret sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT webhook_secret FROM wf_telegram_settings ORDER BY updated_at DESC LIMIT 1`).Scan(&secret)
	if err != nil {
		return "", err
	}
	if !secret.Valid {
		return "", nil
	}
	return strings.TrimSpace(secret.String), nil
}
func generateWorkerSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *WorkforceTelegramService) ValidateWorkerSecret(ctx context.Context, secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return errors.New("missing worker secret")
	}
	envSecret := strings.TrimSpace(os.Getenv("TELEGRAM_WORKER_SECRET"))
	if envSecret != "" && security.SecureCompare(secret, envSecret) {
		return nil
	}
	if s == nil || s.db == nil {
		return errors.New("invalid worker secret")
	}
	// Same row GetSettings needs moments later: load it once per worker request
	// (the request cache is installed before this middleware runs).
	settings, err := telegramRequestRead(ctx, s, "settings", func() (*TelegramSettings, error) { return s.loadTelegramSettingsCached(ctx) })
	if err != nil {
		return err
	}
	if security.SecureCompare(settings.workerSecret, secret) {
		return nil
	}
	return errors.New("invalid worker secret")
}
func (s *WorkforceTelegramService) GetBotToken(ctx context.Context) (string, error) {
	var enc sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT bot_token_enc FROM wf_telegram_settings WHERE enabled = TRUE ORDER BY updated_at DESC LIMIT 1`).Scan(&enc)
	if err != nil || !enc.Valid {
		return "", errors.New("bot token not configured")
	}
	return DecryptSecret(enc.String)
}

type telegramMeResult struct {
	ID        int64
	Username  string
	FirstName string
}

func telegramGetMe(token string) (*telegramMeResult, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.telegram.org/bot"+token+"/getMe", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK     bool `json:"ok"`
		Result struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		if parsed.Description != "" {
			return nil, errors.New(parsed.Description)
		}
		return nil, errors.New("telegram getMe failed")
	}
	return &telegramMeResult{
		ID: parsed.Result.ID, Username: parsed.Result.Username, FirstName: parsed.Result.FirstName,
	}, nil
}
func scanTelegramGroupRow(row *TelegramGroupRow, sb, sa, cb, ca, lt, el sql.NullInt32, reset, lang sql.NullString) {
	if sb.Valid && int(sb.Int32) > 0 {
		v := int(sb.Int32)
		row.ShiftGraceBefore = &v
	}
	if sa.Valid && int(sa.Int32) > 0 {
		v := int(sa.Int32)
		row.ShiftGraceAfter = &v
	}
	if cb.Valid && int(cb.Int32) > 0 {
		v := int(cb.Int32)
		row.CheckOutGraceBefore = &v
	}
	if ca.Valid && int(ca.Int32) > 0 {
		v := int(ca.Int32)
		row.CheckOutGraceAfter = &v
	}
	if lt.Valid && int(lt.Int32) >= 0 {
		v := int(lt.Int32)
		row.LateToleranceMinutes = &v
	}
	if el.Valid && int(el.Int32) >= 0 {
		v := int(el.Int32)
		row.EarlyLeaveToleranceMinutes = &v
	}
	if reset.Valid {
		row.DailyResetTime = strings.TrimSpace(reset.String)
	}
	if lang.Valid {
		row.BotUILang = strings.TrimSpace(lang.String)
	}
}
func (s *WorkforceTelegramService) ListGroups(ctx context.Context) ([]TelegramGroupRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chat_id, COALESCE(chat_title,''), enabled,
			shift_grace_before, shift_grace_after, work_end_grace_before, work_end_grace_after,
			late_tolerance_minutes, early_leave_tolerance_minutes,
			daily_reset_time, bot_ui_lang, created_at
		FROM wf_telegram_groups ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TelegramGroupRow, 0)
	for rows.Next() {
		var row TelegramGroupRow
		var sb, sa, cb, ca, lt, el sql.NullInt32
		var reset, lang sql.NullString
		if err := rows.Scan(&row.ID, &row.ChatID, &row.ChatTitle, &row.Enabled,
			&sb, &sa, &cb, &ca, &lt, &el, &reset, &lang, &row.CreatedAt); err != nil {
			continue
		}
		scanTelegramGroupRow(&row, sb, sa, cb, ca, lt, el, reset, lang)
		items = append(items, row)
	}
	return items, nil
}
func (s *WorkforceTelegramService) UpsertGroup(ctx context.Context, chatID, chatTitle string, enabled bool, overrides map[string]interface{}) (*TelegramGroupRow, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("chat_id required")
	}
	shiftBefore := nullableIntFromAnyKeys(overrides, "shiftGraceBefore", "checkInGraceBefore")
	shiftAfter := nullableIntFromAnyKeys(overrides, "shiftGraceAfter", "checkInGraceAfter")
	checkOutBefore := nullableIntFromAnyKeys(overrides, "workEndGraceBefore", "checkOutGraceBefore")
	checkOutAfter := nullableIntFromAnyKeys(overrides, "workEndGraceAfter", "checkOutGraceAfter")
	lateTolerance := nullableIntFromAnyAllowZero(overrides["lateToleranceMinutes"])
	earlyLeave := nullableIntFromAnyAllowZero(overrides["earlyLeaveToleranceMinutes"])
	dailyReset := nullableStringFromAny(overrides["dailyResetTime"])
	groupLang := nullableStringFromAny(overrides["botUiLang"])

	var row TelegramGroupRow
	var sb, sa, cb, ca, lt, el sql.NullInt32
	var reset, lang sql.NullString
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO wf_telegram_groups (
			chat_id, chat_title, enabled,
			shift_grace_before, shift_grace_after, work_end_grace_before, work_end_grace_after,
			late_tolerance_minutes, early_leave_tolerance_minutes,
			daily_reset_time, bot_ui_lang
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (chat_id) DO UPDATE SET
			chat_title = CASE WHEN EXCLUDED.chat_title <> '' THEN EXCLUDED.chat_title ELSE wf_telegram_groups.chat_title END,
			enabled = EXCLUDED.enabled,
			shift_grace_before = COALESCE(EXCLUDED.shift_grace_before, wf_telegram_groups.shift_grace_before),
			shift_grace_after = COALESCE(EXCLUDED.shift_grace_after, wf_telegram_groups.shift_grace_after),
			work_end_grace_before = COALESCE(EXCLUDED.work_end_grace_before, wf_telegram_groups.work_end_grace_before),
			work_end_grace_after = COALESCE(EXCLUDED.work_end_grace_after, wf_telegram_groups.work_end_grace_after),
			late_tolerance_minutes = COALESCE(EXCLUDED.late_tolerance_minutes, wf_telegram_groups.late_tolerance_minutes),
			early_leave_tolerance_minutes = COALESCE(EXCLUDED.early_leave_tolerance_minutes, wf_telegram_groups.early_leave_tolerance_minutes),
			daily_reset_time = COALESCE(EXCLUDED.daily_reset_time, wf_telegram_groups.daily_reset_time),
			bot_ui_lang = COALESCE(EXCLUDED.bot_ui_lang, wf_telegram_groups.bot_ui_lang)
		RETURNING id, chat_id, COALESCE(chat_title,''), enabled,
			shift_grace_before, shift_grace_after, work_end_grace_before, work_end_grace_after,
			late_tolerance_minutes, early_leave_tolerance_minutes,
			daily_reset_time, bot_ui_lang, created_at`,
		chatID, strings.TrimSpace(chatTitle), enabled,
		shiftBefore, shiftAfter, checkOutBefore, checkOutAfter, lateTolerance, earlyLeave, dailyReset, groupLang).Scan(
		&row.ID, &row.ChatID, &row.ChatTitle, &row.Enabled,
		&sb, &sa, &cb, &ca, &lt, &el, &reset, &lang, &row.CreatedAt)
	if err != nil {
		return nil, err
	}
	scanTelegramGroupRow(&row, sb, sa, cb, ca, lt, el, reset, lang)
	return &row, nil
}
func nullableIntFromAny(v interface{}) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	n := intFromAny(v, 0)
	if n <= 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(n), Valid: true}
}
func nullableIntFromAnyAllowZero(v interface{}) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	n := intFromAny(v, 0)
	if n < 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(n), Valid: true}
}
func nullableIntFromAnyKeys(overrides map[string]interface{}, keys ...string) sql.NullInt32 {
	for _, key := range keys {
		if v, ok := overrides[key]; ok {
			if out := nullableIntFromAny(v); out.Valid {
				return out
			}
		}
	}
	return sql.NullInt32{}
}
func nullableStringFromAny(v interface{}) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
func (s *WorkforceTelegramService) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM wf_telegram_groups WHERE id = $1`, id)
	return err
}
func (s *WorkforceTelegramService) ListBindings(ctx context.Context, search string) ([]TelegramBindingRow, error) {
	search = strings.TrimSpace(search)
	query := `
		SELECT b.id, b.employee_id, e.name, COALESCE(e.employee_id,''),
			COALESCE(NULLIF(TRIM(e.attendance_mode), ''), 'pc_tg'),
			b.telegram_user_id, COALESCE(b.telegram_username,''), COALESCE(b.chat_id,''),
			b.bound_at, b.status
		FROM employee_telegram_bindings b
		JOIN employees e ON e.id = b.employee_id
		WHERE b.status = 1`
	args := []interface{}{}
	if search != "" {
		query += ` AND (e.name ILIKE $1 OR e.employee_id ILIKE $1 OR b.telegram_username ILIKE $1 OR b.telegram_user_id::text ILIKE $1)`
		args = append(args, "%"+search+"%")
	}
	query += ` ORDER BY b.bound_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TelegramBindingRow, 0)
	for rows.Next() {
		var row TelegramBindingRow
		if err := rows.Scan(&row.ID, &row.EmployeeID, &row.EmployeeName, &row.EmployeeCode, &row.AttendanceMode,
			&row.TelegramUserID, &row.TelegramUsername, &row.ChatID, &row.BoundAt, &row.Status); err != nil {
			continue
		}
		items = append(items, row)
	}
	return items, nil
}
func (s *WorkforceTelegramService) CreateBinding(ctx context.Context, employeeID uuid.UUID, telegramUserID int64, telegramUsername, chatID string) (*TelegramBindingRow, error) {
	if telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid telegram user id")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM employees e WHERE e.id = $1 AND e.status = 1 AND (e.user_id IS NULL OR EXISTS(SELECT 1 FROM users u WHERE u.id=e.user_id AND u.status=1))`, employeeID).Scan(&exists); err != nil || exists == 0 {
		return nil, ErrEmployeeNotFound
	}
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO employee_telegram_bindings (employee_id, telegram_user_id, telegram_username, chat_id, status)
		VALUES ($1, $2, $3, $4, 1)
		ON CONFLICT (employee_id) DO UPDATE SET
			telegram_user_id = EXCLUDED.telegram_user_id,
			telegram_username = EXCLUDED.telegram_username,
			chat_id = CASE
				WHEN COALESCE(NULLIF(TRIM(EXCLUDED.chat_id), ''), '') <> '' THEN EXCLUDED.chat_id
				ELSE employee_telegram_bindings.chat_id
			END,
			status = 1,
			bound_at = NOW()
		RETURNING id`, employeeID, telegramUserID, nullString(strings.TrimSpace(telegramUsername)), nullString(strings.TrimSpace(chatID))).Scan(&id)
	if err != nil {
		return nil, err
	}
	items, err := s.ListBindings(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("binding not found after create")
}
func (s *WorkforceTelegramService) DeleteBinding(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM employee_telegram_bindings WHERE id = $1`, id)
	return err
}
func (s *WorkforceTelegramService) GetBindingByID(ctx context.Context, id uuid.UUID) (*TelegramBindingRow, error) {
	var row TelegramBindingRow
	err := s.db.QueryRowContext(ctx, `
		SELECT b.id, b.employee_id, e.name, COALESCE(e.employee_id,''),
			COALESCE(NULLIF(TRIM(e.attendance_mode), ''), 'pc_tg'),
			b.telegram_user_id, COALESCE(b.telegram_username,''), COALESCE(b.chat_id,''), b.bound_at, b.status
		FROM employee_telegram_bindings b
		JOIN employees e ON e.id = b.employee_id
		WHERE b.id = $1 AND b.status = 1`, id).
		Scan(&row.ID, &row.EmployeeID, &row.EmployeeName, &row.EmployeeCode, &row.AttendanceMode,
			&row.TelegramUserID, &row.TelegramUsername, &row.ChatID, &row.BoundAt, &row.Status)
	if err == sql.ErrNoRows {
		return nil, ErrTelegramNotBound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
func (s *WorkforceTelegramService) ResolveEmployeeByTelegramUserID(ctx context.Context, telegramUserID int64) (uuid.UUID, error) {
	return s.ResolveEmployeeByTelegramUserIDWithDevice(ctx, telegramUserID, uuid.Nil, "")
}
func (s *WorkforceTelegramService) ResolveEmployeeByTelegramUserIDWithDevice(
	ctx context.Context, telegramUserID int64, deviceID uuid.UUID, deviceHint string,
) (uuid.UUID, error) {
	actor, err := s.ResolveTelegramActorWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	if err != nil {
		return uuid.Nil, err
	}
	return actor.EmployeeID, nil
}
func (s *WorkforceTelegramService) EmployeeNameByTelegramUserID(ctx context.Context, telegramUserID int64) string {
	if actor, err := s.ResolveTelegramBinding(ctx, telegramUserID); err == nil && actor.EmployeeID != uuid.Nil {
		var name string
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, actor.EmployeeID).Scan(&name)
		return strings.TrimSpace(name)
	}
	return ""
}
func (s *WorkforceTelegramService) GetEmployeeSessionStatus(ctx context.Context, empID uuid.UUID) (bool, *uuid.UUID, error) {
	var sid uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM work_sessions WHERE employee_id = $1 AND status = 'open'
		ORDER BY checkin_time DESC LIMIT 1`, empID).Scan(&sid)
	if err == sql.ErrNoRows {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, &sid, nil
}
func (s *WorkforceTelegramService) TelegramCheckIn(ctx context.Context, telegramUserID int64, chatID, forcedShift string) (*CheckInResult, uuid.UUID, string, float64, string, string, error) {
	return s.TelegramCheckInWithDevice(ctx, telegramUserID, chatID, forcedShift, uuid.Nil, "")
}
func (s *WorkforceTelegramService) TelegramCheckInWithDevice(
	ctx context.Context, telegramUserID int64, chatID, forcedShift string, deviceID uuid.UUID, deviceHint string,
) (*CheckInResult, uuid.UUID, string, float64, string, string, error) {
	if err := s.rejectIfWebPunchOnly(ctx); err != nil {
		return nil, uuid.Nil, "", 0, "", "", err
	}
	active, err := s.IsActive(ctx)
	if err != nil {
		return nil, uuid.Nil, "", 0, "", "", err
	}
	if !active {
		return nil, uuid.Nil, "", 0, "", "", ErrTelegramDisabled
	}
	actor, err := s.ResolveTelegramActorWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	if err != nil {
		return nil, uuid.Nil, "", 0, "", "", err
	}
	empID := actor.EmployeeID
	if chatID != "" {
		_, _ = s.UpsertGroup(ctx, chatID, "", true, nil)
		s.updateTelegramChatID(ctx, actor, chatID)
	}
	var name string
	_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&name)
	outcome, err := s.telegramCheckInWithShift(ctx, empID, telegramUserID, chatID, forcedShift, actor)
	if err != nil {
		return nil, empID, name, 0, "", "", err
	}
	return outcome.Result, empID, name, outcome.WorkFine, outcome.HandoverHint, outcome.GroupReplyHTML, nil
}
func (s *WorkforceTelegramService) TelegramCheckOut(ctx context.Context, telegramUserID int64, chatID string) (*CheckOutResult, uuid.UUID, string, float64, string, string, string, error) {
	return s.TelegramCheckOutWithDevice(ctx, telegramUserID, chatID, uuid.Nil, "")
}
func (s *WorkforceTelegramService) TelegramCheckOutWithDevice(
	ctx context.Context, telegramUserID int64, chatID string, deviceID uuid.UUID, deviceHint string,
) (*CheckOutResult, uuid.UUID, string, float64, string, string, string, error) {
	if err := s.rejectIfWebPunchOnly(ctx); err != nil {
		return nil, uuid.Nil, "", 0, "", "", "", err
	}
	active, err := s.IsActive(ctx)
	if err != nil {
		return nil, uuid.Nil, "", 0, "", "", "", err
	}
	if !active {
		return nil, uuid.Nil, "", 0, "", "", "", ErrTelegramDisabled
	}
	actor, err := s.ResolveTelegramActorWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	if err != nil {
		return nil, uuid.Nil, "", 0, "", "", "", err
	}
	empID := actor.EmployeeID
	var name string
	_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&name)
	if chatID == "" {
		chatID = s.getChatIDForActor(ctx, actor)
	}
	outcome, err := s.telegramCheckOutFIFO(ctx, empID, telegramUserID, chatID, actor)
	if err != nil {
		return nil, empID, name, 0, "", "", "", err
	}
	return outcome.Result, empID, name, outcome.WorkFine, outcome.HandoverHint, outcome.ActivityWarn, outcome.GroupReplyHTML, nil
}
func ParseTelegramUserID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty telegram user id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

// ---- Source: backend/services/workforce_tg_activity.go ----
var (
	ErrActivityNotInWork     = errors.New("must check in to work before activity")
	ErrActivityAlreadyActive = errors.New("activity already in progress")
	ErrActivityNotActive     = errors.New("no activity in progress")
	ErrActivityLimitReached  = errors.New("daily activity limit reached")
	ErrActivityTypeDisabled  = errors.New("activity type disabled")
	ErrActivityTypeNotFound  = errors.New("activity type not found")
	ErrBackProcessing        = errors.New("back request already processing")
	ErrOperationBusy         = errors.New("activity operation already processing")
	ErrActivityBackNotOwner  = errors.New("activity back button not owned by clicker")
)

type TgActivityType struct {
	ID               uuid.UUID
	Code             string
	Name             string
	MaxTimesPerDay   int
	MaxConcurrent    int
	TimeLimitSeconds int
	SortOrder        int
	Enabled          bool
}
type TgActivityFine struct {
	ID             uuid.UUID
	ActivityCode   string
	SegmentMinutes int
	FineAmount     float64
}
type TgActivitySession struct {
	ID                uuid.UUID
	EmployeeID        uuid.UUID
	EmployeeName      string
	ActivityCode      string
	ActivityName      string
	AttendanceDate    string
	StartedAt         time.Time
	EndedAt           *time.Time
	ElapsedSeconds    int
	LimitSeconds      int
	OvertimeSeconds   int
	FineAmount        float64
	Status            string
	StartSource       string
	EndSource         string
	ShiftType         string
	GroupReplyHTML    string
	GroupReplyChatID  string
	TelegramMessageID int64
}
type TgActivityStartResult struct {
	Session        *TgActivitySession
	MessageText    string
	GroupReplyHTML string
	InlineKeyboard map[string]interface{}
	GroupReplySent bool
}

func computeActivityOvertimeFine(segments map[int]float64, overtimeMinutes float64) float64 {
	if len(segments) == 0 || overtimeMinutes <= 0 {
		return 0
	}
	keys := make([]int, 0, len(segments))
	for k := range segments {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, seg := range keys {
		if overtimeMinutes <= float64(seg) {
			return segments[seg]
		}
	}
	return segments[keys[len(keys)-1]]
}
func (s *WorkforceTelegramService) ListActivityTypes(ctx context.Context) ([]TgActivityType, error) {
	items, err := telegramRequestRead(ctx, s, "activity_types", func() ([]TgActivityType, error) { return s.loadActivityTypes(ctx) })
	return append([]TgActivityType(nil), items...), err
}
func (s *WorkforceTelegramService) loadActivityTypes(ctx context.Context) ([]TgActivityType, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, name, max_times_per_day, COALESCE(max_concurrent,0), time_limit_seconds, sort_order, enabled
		FROM wf_tg_activity_types ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgActivityType, 0)
	for rows.Next() {
		var row TgActivityType
		if err := rows.Scan(&row.ID, &row.Code, &row.Name, &row.MaxTimesPerDay, &row.MaxConcurrent, &row.TimeLimitSeconds, &row.SortOrder, &row.Enabled); err != nil {
			continue
		}
		items = append(items, row)
	}
	return items, nil
}
func (s *WorkforceTelegramService) UpsertActivityType(ctx context.Context, in map[string]interface{}) (*TgActivityType, error) {
	code := strings.TrimSpace(fmt.Sprint(in["code"]))
	name := strings.TrimSpace(fmt.Sprint(in["name"]))
	if code == "" || name == "" {
		return nil, fmt.Errorf("code and name required")
	}
	maxTimes := intFromAny(in["maxTimesPerDay"], 0)
	maxConcurrent := intFromAny(in["maxConcurrent"], 0)
	limitSec := intFromAny(in["timeLimitSeconds"], 300)
	sortOrder := intFromAny(in["sortOrder"], 0)
	enabled := boolFromAny(in["enabled"], true)

	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO wf_tg_activity_types (code, name, max_times_per_day, max_concurrent, time_limit_seconds, sort_order, enabled, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (code) DO UPDATE SET
			name = EXCLUDED.name,
			max_times_per_day = EXCLUDED.max_times_per_day,
			max_concurrent = EXCLUDED.max_concurrent,
			time_limit_seconds = EXCLUDED.time_limit_seconds,
			sort_order = EXCLUDED.sort_order,
			enabled = EXCLUDED.enabled,
			updated_at = NOW()
		RETURNING id`, code, name, maxTimes, maxConcurrent, limitSec, sortOrder, enabled).Scan(&id)
	if err != nil {
		return nil, err
	}
	types, err := s.ListActivityTypes(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range types {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("activity type not found after upsert")
}
func (s *WorkforceTelegramService) DeleteActivityType(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM wf_tg_activity_types WHERE code = $1`, strings.TrimSpace(code))
	return err
}
func (s *WorkforceTelegramService) ListActivityFines(ctx context.Context, activityCode string) ([]TgActivityFine, error) {
	query := `SELECT id, activity_code, segment_minutes, fine_amount FROM wf_tg_activity_fines`
	args := []interface{}{}
	if strings.TrimSpace(activityCode) != "" {
		query += ` WHERE activity_code = $1`
		args = append(args, activityCode)
	}
	query += ` ORDER BY activity_code, segment_minutes ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgActivityFine, 0)
	for rows.Next() {
		var row TgActivityFine
		if err := rows.Scan(&row.ID, &row.ActivityCode, &row.SegmentMinutes, &row.FineAmount); err != nil {
			continue
		}
		items = append(items, row)
	}
	return items, nil
}
func (s *WorkforceTelegramService) UpsertActivityFine(ctx context.Context, activityCode string, segmentMinutes int, fineAmount float64) (*TgActivityFine, error) {
	activityCode = strings.TrimSpace(activityCode)
	if activityCode == "" || segmentMinutes <= 0 {
		return nil, fmt.Errorf("invalid fine rule")
	}
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO wf_tg_activity_fines (activity_code, segment_minutes, fine_amount)
		VALUES ($1, $2, $3)
		ON CONFLICT (activity_code, segment_minutes) DO UPDATE SET fine_amount = EXCLUDED.fine_amount
		RETURNING id`, activityCode, segmentMinutes, fineAmount).Scan(&id)
	if err != nil {
		return nil, err
	}
	fines, err := s.ListActivityFines(ctx, activityCode)
	if err != nil {
		return nil, err
	}
	for _, f := range fines {
		if f.ID == id {
			return &f, nil
		}
	}
	return nil, fmt.Errorf("fine rule not found")
}

type TgActivityFineBatchItem struct {
	SegmentMinutes int
	FineAmount     float64
}

func (s *WorkforceTelegramService) BatchUpsertActivityFines(ctx context.Context, activityCodes []string, items []TgActivityFineBatchItem) ([]TgActivityFine, error) {
	codes := make([]string, 0, len(activityCodes))
	seenCode := map[string]struct{}{}
	for _, code := range activityCodes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, ok := seenCode[code]; ok {
			continue
		}
		seenCode[code] = struct{}{}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("activity code required")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one rule required")
	}

	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.SegmentMinutes <= 0 {
			return nil, fmt.Errorf("segment minutes must be positive")
		}
		if _, dup := seen[item.SegmentMinutes]; dup {
			return nil, fmt.Errorf("duplicate segment minutes in batch: %d", item.SegmentMinutes)
		}
		seen[item.SegmentMinutes] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	out := make([]TgActivityFine, 0, len(codes)*len(items))
	for _, activityCode := range codes {
		for _, item := range items {
			var id uuid.UUID
			err := tx.QueryRowContext(ctx, `
				INSERT INTO wf_tg_activity_fines (activity_code, segment_minutes, fine_amount)
				VALUES ($1, $2, $3)
				ON CONFLICT (activity_code, segment_minutes) DO UPDATE SET fine_amount = EXCLUDED.fine_amount
				RETURNING id`, activityCode, item.SegmentMinutes, item.FineAmount).Scan(&id)
			if err != nil {
				return nil, err
			}
			out = append(out, TgActivityFine{
				ID: id, ActivityCode: activityCode,
				SegmentMinutes: item.SegmentMinutes, FineAmount: item.FineAmount,
			})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *WorkforceTelegramService) DeleteActivityFine(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM wf_tg_activity_fines WHERE id = $1`, id)
	return err
}
func (s *WorkforceTelegramService) getActivityType(ctx context.Context, code string) (*TgActivityType, error) {
	code = strings.TrimSpace(code)
	var row TgActivityType
	err := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, max_times_per_day, COALESCE(max_concurrent,0), time_limit_seconds, sort_order, enabled
		FROM wf_tg_activity_types WHERE code = $1`, code).
		Scan(&row.ID, &row.Code, &row.Name, &row.MaxTimesPerDay, &row.MaxConcurrent, &row.TimeLimitSeconds, &row.SortOrder, &row.Enabled)
	if err == sql.ErrNoRows {
		return nil, ErrActivityTypeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
func canonicalActivityTypeCode(key string) string {
	key = stripActivityKeyboardPrefix(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	aliases := map[string]string{
		"小厕": "wc_small", "大厕": "wc_large", "吃饭": "eat",
		"抽烟": "smoke", "抽烟或休息": "smoke",
		"wc1": "wc_small", "wc2": "wc_large", "meal": "eat",
	}
	if code, ok := aliases[key]; ok {
		return code
	}
	if code, ok := aliases[strings.ToLower(key)]; ok {
		return code
	}
	return strings.ToLower(key)
}
func expandActivityLookupKeys(key string) []string {
	key = strings.TrimSpace(key)
	seen := make(map[string]struct{})
	out := make([]string, 0, 6)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	add(key)
	add(strings.ToLower(key))
	if code := canonicalActivityTypeCode(key); code != "" {
		add(code)
	}
	if slug := activityCommandSlug(key); slug != "" {
		add(slug)
	}
	return out
}
func activityTypeMatchesCandidate(t TgActivityType, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return false
	}
	if strings.EqualFold(t.Code, candidate) || t.Name == candidate {
		return true
	}
	nameSlug := activityCommandSlug(t.Name)
	candSlug := activityCommandSlug(candidate)
	if nameSlug != "" && candSlug != "" && nameSlug == candSlug {
		return true
	}
	return nameSlug != "" && strings.EqualFold(nameSlug, candidate)
}
func (s *WorkforceTelegramService) resolveActivityTypeByNameOrCode(ctx context.Context, key string) (*TgActivityType, error) {
	key = stripActivityKeyboardPrefix(strings.TrimSpace(key))
	if key == "" {
		return nil, ErrActivityTypeNotFound
	}

	if code := canonicalActivityTypeCode(key); code != "" {
		if t, err := s.getActivityType(ctx, code); err == nil {
			if !t.Enabled {
				return nil, ErrActivityTypeDisabled
			}
			return t, nil
		} else if !errors.Is(err, ErrActivityTypeNotFound) {
			return nil, err
		}
	}

	types, err := s.ListActivityTypes(ctx)
	if err != nil {
		return nil, err
	}
	for _, candidate := range expandActivityLookupKeys(key) {
		for i := range types {
			t := types[i]
			if !activityTypeMatchesCandidate(t, candidate) {
				continue
			}
			if !t.Enabled {
				return nil, ErrActivityTypeDisabled
			}
			return &t, nil
		}
	}
	return nil, ErrActivityTypeNotFound
}
func (s *WorkforceTelegramService) GetOpenActivitySession(ctx context.Context, empID uuid.UUID) (*TgActivitySession, error) {
	return s.getOpenActivitySession(ctx, empID)
}
func (s *WorkforceTelegramService) getOpenActivitySession(ctx context.Context, empID uuid.UUID) (*TgActivitySession, error) {
	var row TgActivitySession
	var endedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.employee_id, e.name, s.activity_code, s.activity_name, s.attendance_date::text,
			s.started_at, s.ended_at, s.elapsed_seconds, s.limit_seconds, s.overtime_seconds, s.fine_amount, s.status
		FROM wf_tg_activity_sessions s
		JOIN employees e ON e.id = s.employee_id
		WHERE s.employee_id = $1 AND s.status = 'open'`, empID).
		Scan(&row.ID, &row.EmployeeID, &row.EmployeeName, &row.ActivityCode, &row.ActivityName, &row.AttendanceDate,
			&row.StartedAt, &endedAt, &row.ElapsedSeconds, &row.LimitSeconds, &row.OvertimeSeconds, &row.FineAmount, &row.Status)
	if err == sql.ErrNoRows {
		return nil, ErrActivityNotActive
	}
	if err != nil {
		return nil, err
	}
	if endedAt.Valid {
		row.EndedAt = &endedAt.Time
	}
	return &row, nil
}
func (s *WorkforceTelegramService) loadActivityFineMap(ctx context.Context, activityCode string) map[int]float64 {
	fines, err := s.ListActivityFines(ctx, activityCode)
	if err != nil {
		return nil
	}
	out := make(map[int]float64)
	for _, f := range fines {
		out[f.SegmentMinutes] = f.FineAmount
	}
	return out
}
func (s *WorkforceTelegramService) autoEndActivityIfCrossShift(ctx context.Context, empID uuid.UUID, newShift, recordDate string) error {
	sess, err := s.getOpenActivitySession(ctx, empID)
	if errors.Is(err, ErrActivityNotActive) {
		return nil
	}
	if err != nil {
		return err
	}
	var actShift string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day') FROM wf_tg_activity_sessions WHERE id = $1`, sess.ID).Scan(&actShift)
	if sess.AttendanceDate == recordDate && actShift == newShift {
		return nil
	}
	_, err = s.closeActivitySession(ctx, sess.ID, false, "system")
	return err
}
func (s *WorkforceTelegramService) endActivityForEmployee(ctx context.Context, empID uuid.UUID) (*TgActivitySession, error) {
	release, lockErr := s.acquireAttendanceOperation(ctx, empID)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	stageMark(ctx, "lock")
	if !s.tryAcquireBackLock(empID) {
		return nil, ErrBackProcessing
	}
	defer releaseUserLock(empID)

	sess, err := s.closeActivitySessionForOwner(ctx, uuid.Nil, empID, false, "telegram")
	stageMark(ctx, "close")
	return sess, err
}
func (s *WorkforceTelegramService) TelegramStartActivity(ctx context.Context, telegramUserID int64, chatID, activityKey string, replyToMessageID int64) (*TgActivityStartResult, error) {
	return s.TelegramStartActivityWithDevice(ctx, telegramUserID, chatID, activityKey, replyToMessageID, uuid.Nil, "")
}
func (s *WorkforceTelegramService) TelegramStartActivityWithDevice(
	ctx context.Context, telegramUserID int64, chatID, activityKey string, replyToMessageID int64, deviceID uuid.UUID, deviceHint string,
) (*TgActivityStartResult, error) {
	ctx, stages := withPunchStages(ctx, "activity_start")
	var spec *speculativeLock
	if deviceID == uuid.Nil && strings.TrimSpace(deviceHint) == "" {
		ctx, spec = s.beginSpeculativeLock(ctx, telegramUserID)
	}
	defer spec.discard()
	res, err := s.telegramStartActivityWithDevice(ctx, telegramUserID, chatID, activityKey, replyToMessageID, deviceID, deviceHint)
	stages.finish(err)
	return res, err
}
func (s *WorkforceTelegramService) telegramStartActivityWithDevice(
	ctx context.Context, telegramUserID int64, chatID, activityKey string, replyToMessageID int64, deviceID uuid.UUID, deviceHint string,
) (*TgActivityStartResult, error) {
	if err := s.rejectIfWebPunchOnly(ctx); err != nil {
		return nil, err
	}
	active, err := s.IsActive(ctx)
	if err != nil || !active {
		return nil, ErrTelegramDisabled
	}
	stageMark(ctx, "settings")
	empID, err := s.ResolveEmployeeByTelegramUserIDWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	s.noteResolvedEmployee(ctx, telegramUserID, empID, err, deviceID != uuid.Nil || strings.TrimSpace(deviceHint) != "")
	if err != nil {
		return nil, err
	}
	stageMark(ctx, "identity")
	return s.startActivitySessionInChat(ctx, empID, deviceID, activityKey, PunchSourceTelegram, chatID, replyToMessageID, telegramUserID)
}

func (s *WorkforceTelegramService) TelegramEndActivity(ctx context.Context, telegramUserID int64) (*TgActivitySession, error) {
	return s.TelegramEndActivityWithDevice(ctx, telegramUserID, uuid.Nil, "")
}
func (s *WorkforceTelegramService) TelegramEndActivityWithDevice(
	ctx context.Context, telegramUserID int64, deviceID uuid.UUID, deviceHint string,
) (*TgActivitySession, error) {
	ctx, stages := withPunchStages(ctx, "activity_end")
	var spec *speculativeLock
	if deviceID == uuid.Nil && strings.TrimSpace(deviceHint) == "" {
		ctx, spec = s.beginSpeculativeLock(ctx, telegramUserID)
	}
	defer spec.discard()
	sess, err := s.telegramEndActivityWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	stages.finish(err)
	return sess, err
}
func (s *WorkforceTelegramService) telegramEndActivityWithDevice(
	ctx context.Context, telegramUserID int64, deviceID uuid.UUID, deviceHint string,
) (*TgActivitySession, error) {
	if err := s.rejectIfWebPunchOnly(ctx); err != nil {
		return nil, err
	}
	active, err := s.IsActive(ctx)
	if err != nil || !active {
		return nil, ErrTelegramDisabled
	}
	stageMark(ctx, "settings")
	empID, err := s.ResolveEmployeeByTelegramUserIDWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	s.noteResolvedEmployee(ctx, telegramUserID, empID, err, deviceID != uuid.Nil || strings.TrimSpace(deviceHint) != "")
	if err != nil {
		return nil, err
	}
	stageMark(ctx, "identity")
	return s.endActivityForEmployee(ctx, empID)
}
func (s *WorkforceTelegramService) TelegramEndActivityBySession(ctx context.Context, sessionID uuid.UUID, telegramUserID int64) (*TgActivitySession, error) {
	if err := s.rejectIfWebPunchOnly(ctx); err != nil {
		return nil, err
	}
	if telegramUserID <= 0 {
		return nil, ErrTelegramNotBound
	}
	clickerEmpID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	return s.endActivitySessionForEmployee(ctx, sessionID, clickerEmpID, "telegram")
}

// startActivitySessionWithSource starts an activity and records the given channel source.
func (s *WorkforceTelegramService) startActivitySessionWithSource(ctx context.Context, empID, deviceID uuid.UUID, activityKey, source string) (*TgActivityStartResult, error) {
	return s.startActivitySessionInChat(ctx, empID, deviceID, activityKey, source, "", 0, 0)
}

// All channels use the same state, limits and settlement rules. Chat/message
// arguments only retain the originating Telegram reply and group configuration.
func (s *WorkforceTelegramService) startActivitySessionInChat(ctx context.Context, empID, deviceID uuid.UUID, activityKey, source, chatID string, replyToMessageID, telegramUserID int64) (*TgActivityStartResult, error) {
	release, lockErr := s.acquireAttendanceOperation(ctx, empID)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	stageMark(ctx, "lock")
	startedAt := punchNow(ctx).UTC()
	if empID == uuid.Nil {
		return nil, ErrEmployeeNotFound
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = PunchSourceAgent
	}
	if source == PunchSourceWeb {
		if err := s.ensureAttendanceModuleAvailable(ctx); err != nil {
			return nil, err
		}
	} else {
		if err := s.rejectIfWebPunchOnly(ctx); err != nil {
			return nil, err
		}
		active, err := s.IsActive(ctx)
		if err != nil || !active {
			return nil, ErrTelegramDisabled
		}
	}
	effectiveChat := strings.TrimSpace(chatID)
	if effectiveChat == "" {
		effectiveChat = s.resolveNotifyChatID(ctx, empID, deviceID)
	}
	if effectiveChat != "" {
		s.MaybeLazyResetForChat(ctx, effectiveChat)
	}
	if !tryAcquireUserLock(empID, 30*time.Second) {
		return nil, ErrOperationBusy
	}
	defer releaseUserLock(empID)
	stageMark(ctx, "pre")

	// Usually no shift has expired. Read that fact with the active shift and
	// activity guard, and run the existing settlement path only when necessary.
	var workShift, businessDate string
	var activityOpen, expiredWork bool
	// The counter window inputs (group reset override, manual reset marker) ride
	// the same round trip as the shift state; they are not needed until the count.
	var resetOverride, groupLang sql.NullString
	var resetMarker string
	loadState := func() error {
		return s.db.QueryRowContext(ctx, `SELECT COALESCE(shift_type,'day'),
 COALESCE((SELECT successor_record_date FROM wf_tg_relay_states WHERE chat_id=$2 AND employee_id=$1),attendance_date)::text,
 EXISTS(SELECT 1 FROM wf_tg_activity_sessions WHERE employee_id=$1 AND status='open'),
 EXISTS(SELECT 1 FROM work_sessions WHERE employee_id=$1 AND status='open' AND source IN ('telegram','agent','web') AND checkin_time<$3),
 (SELECT daily_reset_time FROM wf_telegram_groups WHERE chat_id=$2),
 COALESCE((SELECT value FROM system_settings WHERE key=$4),''),
 (SELECT bot_ui_lang FROM wf_telegram_groups WHERE chat_id=$2)
 FROM work_sessions WHERE employee_id=$1 AND status='open' ORDER BY checkin_time DESC LIMIT 1`, empID, effectiveChat,
			punchNow(ctx).Add(-time.Duration(telegramShiftStateMaxHours())*time.Hour), "activity_reset:"+empID.String()).
			Scan(&workShift, &businessDate, &activityOpen, &expiredWork, &resetOverride, &resetMarker, &groupLang)
	}
	var actType *TgActivityType
	var typeErr, stateErr error
	var reads sync.WaitGroup
	reads.Add(1)
	go func() { defer reads.Done(); actType, typeErr = s.resolveActivityTypeByNameOrCode(ctx, activityKey) }()
	stateErr = loadState()
	reads.Wait()
	if stateErr != nil {
		return nil, ErrActivityNotInWork
	}
	if expiredWork {
		if _, err := s.closeExpiredWorkSessionsForEmployee(ctx, empID, effectiveChat); err != nil {
			log.Printf("[Telegram] close expired shifts emp=%s: %v", empID, err)
		}
		if err := loadState(); err != nil {
			return nil, ErrActivityNotInWork
		}
	}

	if effectiveChat != "" {
		telegramRequestStore(ctx, s, "group_ui_lang:"+effectiveChat, groupLang) // reply language needs no extra query
	}
	stageMark(ctx, "state")
	if activityOpen {
		return nil, ErrActivityAlreadyActive
	}

	if typeErr != nil {
		return nil, typeErr
	}

	var openTeam int
	var teamDone sync.WaitGroup
	if actType.MaxConcurrent > 0 {
		teamDone.Add(1)
		go func() {
			defer teamDone.Done()
			_ = s.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM wf_tg_activity_sessions
				WHERE activity_code = $1 AND status = 'open'
				  AND ($2 = '' OR COALESCE(source_chat_id,'') = $2)`,
				actType.Code, effectiveChat).Scan(&openTeam)
		}()
	}
	var count int
	var err error
	if effectiveChat != "" {
		settings, serr := s.GetSettings(ctx)
		if serr != nil {
			return nil, serr
		}
		cStart, cEnd, _, berr := activityCounterBoundsFrom(ctx, settings, effectiveChat, resetOverride, resetMarker)
		if berr != nil {
			return nil, berr
		}
		stageMark(ctx, "bounds")
		if count, err = s.countActivitiesInWindow(ctx, empID, actType.Code, workShift, cStart, cEnd); err != nil {
			return nil, err
		}
	} else if count, err = s.currentActivityCount(ctx, empID, effectiveChat, actType.Code, workShift); err != nil {
		return nil, err
	}
	stageMark(ctx, "count")

	if actType.MaxTimesPerDay > 0 {
		if count >= actType.MaxTimesPerDay {
			return nil, ErrActivityLimitReached
		}
	}
	if actType.MaxConcurrent > 0 {
		teamDone.Wait()
		if openTeam >= actType.MaxConcurrent {
			return nil, ErrActivityConcurrentLimit
		}
	}

	stageMark(ctx, "rules")
	sessionID := uuid.New()
	var employeeName string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO wf_tg_activity_sessions (
			id, employee_id, activity_code, activity_name, attendance_date, limit_seconds, status, source, shift_type, source_chat_id, started_at
		) VALUES ($1, $2, $3, $4, $5::date, $6, 'open', $7, $8, NULLIF($9,''), $10) RETURNING (SELECT name FROM employees WHERE id=$2)`,
		sessionID, empID, actType.Code, actType.Name, businessDate, actType.TimeLimitSeconds, source, workShift, effectiveChat, startedAt).Scan(&employeeName)
	if err != nil {
		return nil, err
	}
	stageMark(ctx, "insert")
	sess := &TgActivitySession{ID: sessionID, EmployeeID: empID, EmployeeName: employeeName,
		ActivityCode: actType.Code, ActivityName: actType.Name, AttendanceDate: businessDate,
		StartedAt: startedAt, LimitSeconds: actType.TimeLimitSeconds, Status: "open", StartSource: source, ShiftType: workShift}
	count++ // The new session is inside the exact window counted above, under the employee lock.

	limitMin := actType.TimeLimitSeconds / 60
	if actType.TimeLimitSeconds%60 != 0 && actType.TimeLimitSeconds > 0 {
		limitMin++
	}

	var tgUID int64
	tgUID = telegramUserID
	if tgUID == 0 {
		tgUID = s.resolveNotifyTelegramUID(ctx, empID, deviceID)
	}

	replyLang := s.replyLangForEmployeeGroupNotify(ctx, empID, effectiveChat)
	msg := buildActivityStartMessageText(replyLang, actType.Name, workShift, limitMin, count, actType.MaxTimesPerDay)
	groupHTML := buildActivityStartGroupReplyHTML(
		replyLang, tgUID, sess.EmployeeName, actType.Name,
		punchNow(ctx).Format("01/02 15:04:05"), tgReplyShiftLabel(workShift, replyLang),
		count, actType.MaxTimesPerDay, limitMin,
	)
	if prefix := activityGroupNotifyPrefix(source, replyLang); prefix != "" {
		groupHTML = prefix + groupHTML
	}
	sess.GroupReplyChatID = effectiveChat
	groupReplySent := s.publishActivityStartGroupReply(ctx, sessionID, effectiveChat, empID, groupHTML, replyToMessageID, telegramUserID)
	if !groupReplySent && strings.TrimSpace(effectiveChat) == "" {
		log.Printf("[Telegram] activity start skipped: no notify chat emp=%s device=%s activity=%s source=%s", empID, deviceID, actType.Name, source)
	}
	s.syncAgentActivityStateForDevice(ctx, empID, deviceID, true, sess, source)
	stageMark(ctx, "reply")

	return &TgActivityStartResult{
		Session:        sess,
		MessageText:    msg,
		GroupReplyHTML: groupHTML,
		InlineKeyboard: inlineQuickBackKeyboard(sessionID.String(), replyLang),
		GroupReplySent: groupReplySent,
	}, nil
}

// publishActivityStartGroupReply sends activity start ① (打卡成功) to the group.
// replyToMessageID > 0: TG 用户点击活动按钮（如「小厕」）时，引用该用户消息；
// replyToMessageID = 0: PC 客户端发起，独立发送。
// Stores telegram_message_id as anchor for timeout/back reminders.
type workerReplyContextKey struct{}

// Only worker endpoints delegate delivery. Web/agent and timer notifications
// retain their existing sender and ordering.
func WithWorkerTelegramReply(ctx context.Context) context.Context {
	return context.WithValue(ctx, workerReplyContextKey{}, true)
}
func workerOwnsTelegramReply(ctx context.Context) bool {
	owns, _ := ctx.Value(workerReplyContextKey{}).(bool)
	return owns
}

func (s *WorkforceTelegramService) publishActivityStartGroupReply(ctx context.Context, sessionID uuid.UUID, chatID string, empID uuid.UUID, groupHTML string, replyToMessageID int64, telegramUserID int64) bool {
	if workerOwnsTelegramReply(ctx) {
		return false
	}
	if strings.TrimSpace(groupHTML) == "" || strings.TrimSpace(chatID) == "" {
		return false
	}
	var markup interface{}
	if telegramUserID > 0 && replyToMessageID > 0 {
		markup = s.BuildWorkerReplyKeyboardForUser(ctx, chatID, telegramUserID)
	}
	replyTo := replyToMessageID
	if replyTo < 0 {
		replyTo = 0
	}
	msgID, err := s.SendTelegramHTMLReplyReturnID(ctx, chatID, groupHTML, replyTo, markup)
	if err != nil {
		log.Printf("[Telegram] activity start group notify failed chat=%s session=%s: %v", chatID, sessionID, err)
		return false
	}
	s.SetActivityTelegramMessageID(ctx, sessionID, msgID)
	s.setEmployeeLastGroupBotMessageID(ctx, chatID, empID, msgID)
	return true
}
func (s *WorkforceTelegramService) endActivitySessionForEmployee(ctx context.Context, sessionID, empID uuid.UUID, endSource string) (*TgActivitySession, error) {
	release, lockErr := s.acquireAttendanceOperation(ctx, empID)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	if !s.tryAcquireBackLock(empID) {
		return nil, ErrBackProcessing
	}
	defer releaseUserLock(empID)

	return s.closeActivitySessionForOwner(ctx, sessionID, empID, false, endSource)
}
func (s *WorkforceTelegramService) publishActivityEndGroupReply(ctx context.Context, sessionID uuid.UUID, sess *TgActivitySession) {
	if workerOwnsTelegramReply(ctx) {
		return
	}
	if sess == nil || strings.TrimSpace(sess.GroupReplyHTML) == "" {
		return
	}
	var chatID string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(source_chat_id,'') FROM wf_tg_activity_sessions WHERE id = $1`, sessionID).Scan(&chatID)
	if strings.TrimSpace(chatID) == "" {
		chatID = s.resolveNotifyChatID(ctx, sess.EmployeeID, uuid.Nil)
	}
	replyTo := s.activitySessionTelegramMessageID(ctx, sessionID)
	groupHTML := sess.GroupReplyHTML
	prefixLang := s.replyLangForEmployeeGroupNotify(ctx, sess.EmployeeID, chatID)
	if sess.EndSource != PunchSourceAgent && sess.EndSource != PunchSourceWeb {
		prefixLang = s.GetReplyLangForEmployee(ctx, sess.EmployeeID)
	}
	if prefix := activityGroupNotifyPrefix(sess.EndSource, prefixLang); prefix != "" {
		groupHTML = prefix + groupHTML
	}
	if strings.TrimSpace(chatID) != "" {
		msgID, err := s.SendTelegramHTMLReplyReturnID(ctx, chatID, groupHTML, replyTo, nil)
		if err != nil {
			log.Printf("[Telegram] activity back group notify failed chat=%s emp=%s: %v", chatID, sess.EmployeeID, err)
			return
		}
		s.setEmployeeLastGroupBotMessageID(ctx, chatID, sess.EmployeeID, msgID)
	}
}
func (s *WorkforceTelegramService) closeActivitySession(ctx context.Context, sessionID uuid.UUID, autoEnded bool, endSource string) (*TgActivitySession, error) {
	return s.closeActivitySessionForOwner(ctx, sessionID, uuid.Nil, autoEnded, endSource)
}

// Read the session once, and validate ownership before any write.
func (s *WorkforceTelegramService) closeActivitySessionForOwner(ctx context.Context, sessionID, expectedEmployee uuid.UUID, autoEnded bool, endSource string) (*TgActivitySession, error) {
	var empID uuid.UUID
	var session TgActivitySession
	var endedAt sql.NullTime
	var sessionSource, chatID string
	var personalTGUID int64 // latest personal binding, same rule as lookupEmployeeTelegramUserID's first step
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.employee_id, e.name, s.activity_code, s.activity_name, s.attendance_date::text,
			s.started_at, s.ended_at, s.elapsed_seconds, s.limit_seconds, s.overtime_seconds, s.fine_amount, s.status,
			COALESCE(s.shift_type,'day'), COALESCE(s.source_chat_id,''), COALESCE(s.source,''), COALESCE(s.telegram_message_id,0),
			COALESCE((SELECT b.telegram_user_id FROM employee_telegram_bindings b
				WHERE b.employee_id = s.employee_id AND b.status = 1
				ORDER BY b.bound_at DESC NULLS LAST LIMIT 1), 0)
		FROM wf_tg_activity_sessions s
		JOIN employees e ON e.id = s.employee_id
		WHERE (s.id=$1 OR ($1='00000000-0000-0000-0000-000000000000'::uuid AND s.employee_id=$2)) AND s.status='open'`, sessionID, expectedEmployee).
		Scan(&session.ID, &session.EmployeeID, &session.EmployeeName, &session.ActivityCode, &session.ActivityName, &session.AttendanceDate,
			&session.StartedAt, &endedAt, &session.ElapsedSeconds, &session.LimitSeconds, &session.OvertimeSeconds, &session.FineAmount, &session.Status,
			&session.ShiftType, &chatID, &sessionSource, &session.TelegramMessageID, &personalTGUID)
	if err == sql.ErrNoRows {
		return nil, ErrActivityNotActive
	}
	if err != nil {
		return nil, err
	}
	if expectedEmployee != uuid.Nil && session.EmployeeID != expectedEmployee {
		return nil, ErrActivityBackNotOwner
	}
	sessionID = session.ID
	empID = session.EmployeeID

	now := punchNow(ctx)
	elapsed := int(math.Max(0, now.Sub(session.StartedAt).Seconds()))
	overtime := 0
	if session.LimitSeconds > 0 && elapsed > session.LimitSeconds {
		overtime = elapsed - session.LimitSeconds
	}
	fine := 0.0
	if overtime > 0 {
		fine = computeActivityOvertimeFine(s.loadActivityFineMap(ctx, session.ActivityCode), float64(overtime)/60.0)
	}
	resolvedEndSource := strings.TrimSpace(endSource)
	if resolvedEndSource == "" {
		if autoEnded {
			resolvedEndSource = "system"
		} else {
			resolvedEndSource = "telegram"
		}
	}

	stageMark(ctx, "close_read")
	var penaltyID sql.NullString
	if fine <= 0 {
		// A single UPDATE is already atomic: skip BEGIN/COMMIT (2 round trips).
		if _, err = s.db.ExecContext(ctx, `
			UPDATE wf_tg_activity_sessions SET
				status = 'closed', ended_at = NOW(), elapsed_seconds = $1,
				overtime_seconds = $2, fine_amount = $3, penalty_record_id = NULL,
				auto_ended = $4, end_source = $5
			WHERE id = $6 AND status = 'open'`,
			elapsed, overtime, fine, autoEnded, resolvedEndSource, sessionID); err != nil {
			return nil, err
		}
	} else {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		{
			pid := uuid.NewString()
			_, err = tx.ExecContext(ctx, `
				INSERT INTO penalty_records (id, employee_id, employee_name, position, amount, category, reason, penalty_date, created_by, created_at)
				SELECT $1, e.id, e.name, COALESCE(e.position,''), $2, 'TG活动', $3, $4::date, 'telegram', NOW()
				FROM employees e WHERE e.id = $5`,
				pid, fine, fmt.Sprintf("%s 超时 %s", session.ActivityName, formatDurationZh(overtime)), session.AttendanceDate, empID)
			if err != nil {
				return nil, err
			}
			penaltyID = sql.NullString{String: pid, Valid: true}
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE wf_tg_activity_sessions SET
				status = 'closed', ended_at = NOW(), elapsed_seconds = $1,
				overtime_seconds = $2, fine_amount = $3, penalty_record_id = NULLIF($4, '')::uuid,
				auto_ended = $5, end_source = $6
			WHERE id = $7 AND status = 'open'`,
			elapsed, overtime, fine, nullStringFromNull(penaltyID), autoEnded, resolvedEndSource, sessionID)
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	stageMark(ctx, "close_write")
	if fine > 0 {
		s.pushAutoPenaltyNotify(ctx, empID, fine, "TG活动",
			fmt.Sprintf("%s 超时 %s", session.ActivityName, formatDurationZh(overtime)), session.AttendanceDate)
	}

	if strings.TrimSpace(chatID) == "" {
		chatID = s.resolveNotifyChatID(ctx, empID, uuid.Nil)
	}
	session.GroupReplyChatID = chatID
	// Two independent read/write chains run side by side (2 connections at most,
	// the pool is small): handover bookkeeping vs. the reply's statistics.
	var tgUID int64
	var activityCounts map[string]int
	var activityDurations map[string]int
	var totalDailyCount, totalDailySec int
	var statsDone sync.WaitGroup
	statsDone.Add(1)
	go func() {
		defer statsDone.Done()
		if tgUID = personalTGUID; tgUID <= 0 {
			tgUID = lookupEmployeeTelegramUserID(ctx, s.db, empID.String(), session.EmployeeName)
		}
		activityCounts, activityDurations, totalDailyCount, totalDailySec = s.dailyActivityStats(ctx, empID, session.AttendanceDate, session.ShiftType)
	}()
	if period, err := s.DetermineCurrentPeriod(ctx, chatID, now); err == nil {
		s.bumpHandoverCycleWork(ctx, chatID, empID, period, elapsed)
	}
	statsDone.Wait()
	stageMark(ctx, "close_post")
	todayActivityCount := activityCounts[session.ActivityName]
	totalActivitySec := activityDurations[session.ActivityName]

	replyLang := s.GetReplyLangForEmployee(ctx, empID)
	if resolvedEndSource == PunchSourceAgent || resolvedEndSource == PunchSourceWeb {
		replyLang = s.replyLangForEmployeeGroupNotify(ctx, empID, chatID)
	}
	session.GroupReplyHTML = buildActivityEndGroupReplyHTML(
		replyLang, tgUID, session.EmployeeName, session.ActivityName,
		now.Format("01/02 15:04:05"),
		elapsed, todayActivityCount, totalActivitySec, totalDailySec, totalDailyCount,
		activityCounts,
		overtime > 0, overtime, fine,
		s.FineCurrencyCode(ctx),
	)

	if overtime > 0 {
		s.NotifyActivityOvertimeBack(ctx, chatID, empID, session.EmployeeName, session.ActivityName, tgUID, overtime, fine, now)
	} else if autoEnded {
		s.NotifyActivityEvent(ctx, chatID, empID, session.EmployeeName, session.ActivityName, "超时自动结束",
			fmt.Sprintf("用时 %s", formatDurationZh(elapsed)))
	}

	session.ElapsedSeconds = elapsed
	session.OvertimeSeconds = overtime
	session.FineAmount = fine
	session.Status = "closed"
	session.EndedAt = &now
	session.StartSource = sessionSource
	session.EndSource = resolvedEndSource
	syncSource := strings.TrimSpace(resolvedEndSource)
	if autoEnded {
		syncSource = "system"
	}
	if syncSource == "" {
		syncSource = "telegram"
	}
	s.SyncAgentActivityState(ctx, empID, false, &session, syncSource)
	if !autoEnded {
		s.publishActivityEndGroupReply(ctx, sessionID, &session)
	}
	stageMark(ctx, "close_reply")
	return &session, nil
}
func nullStringFromNull(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}
func (s *WorkforceTelegramService) dailyActivityStats(ctx context.Context, empID uuid.UUID, attendanceDate, shift string) (map[string]int, map[string]int, int, int) {
	counts := make(map[string]int)
	durations := make(map[string]int)
	totalCount := 0
	totalSec := 0
	rows, err := s.db.QueryContext(ctx, `
		SELECT activity_name, COUNT(*), COALESCE(SUM(elapsed_seconds),0)::int
		FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date AND shift_type = $3 AND status = 'closed'
		GROUP BY activity_name ORDER BY activity_name`, empID, attendanceDate, shift)
	if err != nil {
		return counts, durations, 0, 0
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var count, sec int
		if rows.Scan(&name, &count, &sec) != nil {
			continue
		}
		counts[name] = count
		durations[name] = sec
		totalCount += count
		totalSec += sec
	}
	return counts, durations, totalCount, totalSec
}
func (s *WorkforceTelegramService) ListActivitySessions(ctx context.Context, date, yearMonth, employeeID, search string, limit int) ([]TgActivitySession, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	tz := "Asia/Shanghai"
	if settings, err := s.GetSettings(ctx); err == nil && strings.TrimSpace(settings.Timezone) != "" {
		tz = strings.TrimSpace(settings.Timezone)
	}
	where := "s.status IN ('open', 'closed')"
	args := []interface{}{}
	argN := 1
	date = strings.TrimSpace(date)
	yearMonth = strings.TrimSpace(yearMonth)
	if date != "" {
		where += fmt.Sprintf(` AND (
			s.attendance_date = $%d::date
			OR ((s.started_at AT TIME ZONE $%d)::date = $%d::date)
		)`, argN, argN+1, argN)
		args = append(args, date, tz)
		argN += 2
	} else if yearMonth != "" {
		if _, err := time.Parse("2006-01", yearMonth); err == nil {
			where += fmt.Sprintf(` AND (
				to_char(s.attendance_date, 'YYYY-MM') = $%d
				OR to_char((s.started_at AT TIME ZONE $%d), 'YYYY-MM') = $%d
			)`, argN, argN+1, argN)
			args = append(args, yearMonth, tz)
			argN += 2
		}
	}
	if strings.TrimSpace(employeeID) != "" {
		where += fmt.Sprintf(" AND s.employee_id = $%d::uuid", argN)
		args = append(args, employeeID)
		argN++
	}
	if strings.TrimSpace(search) != "" {
		where += fmt.Sprintf(" AND (e.name ILIKE $%d OR s.activity_name ILIKE $%d)", argN, argN)
		args = append(args, "%"+search+"%")
		argN++
	}
	// Live ∪ archive：日切/员工日重置会把已关闭行归档并删除活表，列表必须能查历史日。
	query := fmt.Sprintf(`
		SELECT id, employee_id, employee_name, activity_code, activity_name, attendance_date,
			started_at, ended_at, elapsed_seconds, limit_seconds, overtime_seconds, fine_amount, status,
			start_source, end_source
		FROM (
			SELECT DISTINCT ON (s.id)
				s.id, s.employee_id, e.name AS employee_name, s.activity_code, s.activity_name, s.attendance_date::text,
				s.started_at, s.ended_at, s.elapsed_seconds, s.limit_seconds, s.overtime_seconds, s.fine_amount, s.status,
				s.start_source, s.end_source
			FROM (
				SELECT id, employee_id, activity_code, activity_name, attendance_date,
					started_at, ended_at, elapsed_seconds, limit_seconds, overtime_seconds, fine_amount, status,
					COALESCE(source, '') AS start_source, COALESCE(end_source, '') AS end_source, TRUE AS is_live
				FROM wf_tg_activity_sessions
				UNION ALL
				SELECT id, employee_id, activity_code, activity_name, attendance_date,
					started_at, ended_at, elapsed_seconds, 0 AS limit_seconds, overtime_seconds, fine_amount, status,
					COALESCE(source, '') AS start_source, COALESCE(end_source, '') AS end_source, FALSE AS is_live
				FROM wf_tg_activity_sessions_archive
			) s
			JOIN employees e ON e.id = s.employee_id
			WHERE %s
			ORDER BY s.id, s.is_live DESC
		) listed
		ORDER BY started_at DESC
		LIMIT %d`, where, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgActivitySession, 0)
	for rows.Next() {
		var row TgActivitySession
		var endedAt sql.NullTime
		if err := rows.Scan(&row.ID, &row.EmployeeID, &row.EmployeeName, &row.ActivityCode, &row.ActivityName, &row.AttendanceDate,
			&row.StartedAt, &endedAt, &row.ElapsedSeconds, &row.LimitSeconds, &row.OvertimeSeconds, &row.FineAmount, &row.Status,
			&row.StartSource, &row.EndSource); err != nil {
			continue
		}
		if endedAt.Valid {
			row.EndedAt = &endedAt.Time
		}
		items = append(items, row)
	}
	return items, nil
}

type TgWorkSessionRow struct {
	ID             uuid.UUID
	EmployeeID     uuid.UUID
	EmployeeName   string
	EmployeeCode   string
	AttendanceDate string
	ShiftType      string
	CheckinTime    time.Time
	CheckoutTime   *time.Time
	Source         string
	Status         string
}

func (s *WorkforceTelegramService) ListWorkSessions(ctx context.Context, date, yearMonth, employeeID, search string, limit int) ([]TgWorkSessionRow, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	tz := "Asia/Shanghai"
	if settings, err := s.GetSettings(ctx); err == nil && strings.TrimSpace(settings.Timezone) != "" {
		tz = strings.TrimSpace(settings.Timezone)
	}
	where := "ws.source " + punchAttendanceSourcesSQL
	args := []interface{}{}
	argN := 1
	date = strings.TrimSpace(date)
	yearMonth = strings.TrimSpace(yearMonth)
	if date != "" {
		where += fmt.Sprintf(` AND (
			ws.attendance_date = $%d::date
			OR ((ws.checkin_time AT TIME ZONE $%d)::date = $%d::date)
		)`, argN, argN+1, argN)
		args = append(args, date, tz)
		argN += 2
	} else if yearMonth != "" {
		if _, err := time.Parse("2006-01", yearMonth); err == nil {
			where += fmt.Sprintf(` AND (
				to_char(ws.attendance_date, 'YYYY-MM') = $%d
				OR to_char((ws.checkin_time AT TIME ZONE $%d), 'YYYY-MM') = $%d
			)`, argN, argN+1, argN)
			args = append(args, yearMonth, tz)
			argN += 2
		}
	}
	if strings.TrimSpace(employeeID) != "" {
		where += fmt.Sprintf(" AND ws.employee_id = $%d::uuid", argN)
		args = append(args, employeeID)
		argN++
	}
	if strings.TrimSpace(search) != "" {
		where += fmt.Sprintf(" AND (e.name ILIKE $%d OR COALESCE(e.employee_id,'') ILIKE $%d)", argN, argN)
		args = append(args, "%"+search+"%")
		argN++
	}
	// Live ∪ archive：与导出一致；日切后昨日已关闭上下班行只在 archive。
	query := fmt.Sprintf(`
		SELECT id, employee_id, employee_name, employee_code, attendance_date, shift_type,
			checkin_time, checkout_time, source, status
		FROM (
			SELECT DISTINCT ON (ws.id)
				ws.id, ws.employee_id, e.name AS employee_name, COALESCE(e.employee_id,'') AS employee_code,
				ws.attendance_date::text AS attendance_date, COALESCE(ws.shift_type,'day') AS shift_type,
				ws.checkin_time, ws.checkout_time, ws.source, ws.status
			FROM (
				SELECT id, employee_id, attendance_date, shift_type, checkin_time, checkout_time, source, status, TRUE AS is_live
				FROM work_sessions
				UNION ALL
				SELECT id, employee_id, attendance_date, shift_type, checkin_time, checkout_time, source, status, FALSE AS is_live
				FROM work_sessions_archive
			) ws
			JOIN employees e ON e.id = ws.employee_id
			WHERE %s
			ORDER BY ws.id, ws.is_live DESC
		) listed
		ORDER BY checkin_time DESC
		LIMIT %d`, where, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgWorkSessionRow, 0)
	for rows.Next() {
		var row TgWorkSessionRow
		var checkout sql.NullTime
		if err := rows.Scan(&row.ID, &row.EmployeeID, &row.EmployeeName, &row.EmployeeCode,
			&row.AttendanceDate, &row.ShiftType, &row.CheckinTime, &checkout, &row.Source, &row.Status); err != nil {
			continue
		}
		if checkout.Valid {
			row.CheckoutTime = &checkout.Time
		}
		items = append(items, row)
	}
	return items, nil
}

type TgMyInfoSummary struct {
	EmployeeName      string
	WorkSessionOpen   bool
	CurrentActivity   string
	ActivityStartedAt *time.Time
	TodayActivities   []map[string]interface{}
	TodayFineTotal    float64
	ShiftFilter       string
	ShiftLabel        string
}

func (s *WorkforceTelegramService) GetMyInfo(ctx context.Context, telegramUserID int64) (*TgMyInfoSummary, error) {
	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	var name string
	_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&name)

	openWork, _, _ := s.GetEmployeeSessionStatus(ctx, empID)
	out := &TgMyInfoSummary{EmployeeName: name, WorkSessionOpen: openWork}

	if sess, err := s.getOpenActivitySession(ctx, empID); err == nil {
		out.CurrentActivity = sess.ActivityName
		t := sess.StartedAt
		out.ActivityStartedAt = &t
	}

	today := punchNow(ctx).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `
		SELECT activity_name, COUNT(*), COALESCE(SUM(elapsed_seconds),0), COALESCE(SUM(fine_amount),0)
		FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date AND status = 'closed'
		GROUP BY activity_name ORDER BY activity_name`, empID, today)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var actName string
			var count, totalSec int
			var fineTotal float64
			if rows.Scan(&actName, &count, &totalSec, &fineTotal) == nil {
				out.TodayActivities = append(out.TodayActivities, map[string]interface{}{
					"name": actName, "count": count, "totalSeconds": totalSec, "fineAmount": fineTotal,
				})
				out.TodayFineTotal += fineTotal
			}
		}
	}
	return out, nil
}
func intFromAny(v interface{}, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return def
	}
}
func boolFromAny(v interface{}, def bool) bool {
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}
func (s *WorkforceTelegramService) ListEnabledActivityTypesForWorker(ctx context.Context) ([]TgActivityType, error) {
	all, err := s.ListActivityTypes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TgActivityType, 0)
	for _, t := range all {
		if t.Enabled {
			out = append(out, t)
		}
	}
	return out, nil
}

// ---- Source: backend/services/workforce_tg_activity_archive.go ----
const archiveActivitySessionsInsertSQL = `
		INSERT INTO wf_tg_activity_sessions_archive (
			id, employee_id, activity_code, activity_name, attendance_date,
			started_at, ended_at, elapsed_seconds, overtime_seconds, fine_amount,
			status, source, end_source, shift_type, source_chat_id, archived_at
		)
		SELECT
			s.id, s.employee_id, s.activity_code, s.activity_name, s.attendance_date,
			s.started_at, s.ended_at, s.elapsed_seconds, s.overtime_seconds, s.fine_amount,
			s.status, s.source, s.end_source, COALESCE(s.shift_type,'day'), s.source_chat_id, NOW()
		FROM wf_tg_activity_sessions s
		WHERE %s
		ON CONFLICT (id) DO NOTHING`

func (s *WorkforceTelegramService) archiveClosedActivitySessionsByGroupDate(ctx context.Context, tx execContext, chatID, exportDate string) error {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" {
		return nil
	}
	where := `s.status = 'closed' AND s.attendance_date = $2::date AND COALESCE(s.source_chat_id,'') = $1`
	_, err := tx.ExecContext(ctx, fmt.Sprintf(archiveActivitySessionsInsertSQL, where), chatID, exportDate)
	return err
}
func (s *WorkforceTelegramService) archiveActivitySessionsByEmployeeDate(ctx context.Context, tx execContext, employeeID uuid.UUID, attendanceDate string) error {
	if employeeID == uuid.Nil || strings.TrimSpace(attendanceDate) == "" {
		return nil
	}
	where := `s.attendance_date = $2::date AND s.employee_id = $1 AND s.status = 'closed'`
	_, err := tx.ExecContext(ctx, fmt.Sprintf(archiveActivitySessionsInsertSQL, where), employeeID, attendanceDate)
	return err
}
func (s *WorkforceTelegramService) mergeActivityStatsIntoMonthlyForEmployeeDateTx(ctx context.Context, tx execContext, employeeID uuid.UUID, exportDate string) error {
	if employeeID == uuid.Nil {
		return nil
	}
	exportDate = strings.TrimSpace(exportDate)
	if exportDate == "" || len(exportDate) < 7 {
		return nil
	}
	yearMonth := exportDate[:7]
	_, err := tx.ExecContext(ctx, `
		INSERT INTO wf_tg_monthly_activity_stats (
			year_month, chat_id, employee_id, shift_type, activity_code, activity_name,
			activity_count, total_seconds, fine_total, archived_at
		)
		SELECT $1, COALESCE(s.source_chat_id,''), s.employee_id, COALESCE(s.shift_type,'day'),
			s.activity_code, s.activity_name,
			COUNT(*)::int, COALESCE(SUM(s.elapsed_seconds),0)::int, COALESCE(SUM(s.fine_amount),0), NOW()
		FROM wf_tg_activity_sessions s
		WHERE s.status = 'closed' AND s.attendance_date = $2::date AND s.employee_id = $3
		GROUP BY COALESCE(s.source_chat_id,''), s.employee_id, COALESCE(s.shift_type,'day'),
			s.activity_code, s.activity_name
		ON CONFLICT (year_month, chat_id, employee_id, shift_type, activity_code) DO UPDATE SET
			activity_name = EXCLUDED.activity_name,
			activity_count = wf_tg_monthly_activity_stats.activity_count + EXCLUDED.activity_count,
			total_seconds = wf_tg_monthly_activity_stats.total_seconds + EXCLUDED.total_seconds,
			fine_total = wf_tg_monthly_activity_stats.fine_total + EXCLUDED.fine_total,
			archived_at = NOW()`, yearMonth, exportDate, employeeID)
	return err
}

// ---- Source: backend/services/workforce_tg_admin_punch.go ----
// AdminPunchResult is the admin console punch outcome (same core path as TG).
type AdminPunchResult struct {
	Result         *CheckInResult
	CheckOut       *CheckOutResult
	EmployeeName   string
	WorkFine       float64
	HandoverHint   string
	ActivityWarn   string
	GroupReplyHTML string
	MessageText    string
}

func (s *WorkforceTelegramService) resolveAdminPunchContext(ctx context.Context, empID uuid.UUID) (int64, string, *TelegramActor, string, error) {
	if empID == uuid.Nil {
		return 0, "", nil, "", ErrEmployeeNotFound
	}
	if err := s.ensureAttendanceModuleAvailable(ctx); err != nil {
		return 0, "", nil, "", err
	}
	var empName string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(name,'') FROM employees WHERE id = $1`, empID).Scan(&empName); err != nil {
		return 0, "", nil, "", ErrEmployeeNotFound
	}
	chatID := s.resolveNotifyChatID(ctx, empID, uuid.Nil)
	tgUID := s.employeeTelegramUserID(ctx, empID)
	actor := &TelegramActor{
		Channel:     TelegramChannelPersonal,
		EmployeeID:  empID,
		PunchSource: PunchSourceWeb,
	}
	if deviceID, err := s.findDeviceForEmployee(ctx, empID); err == nil && deviceID != uuid.Nil {
		actor.DeviceID = deviceID
	}
	return tgUID, chatID, actor, empName, nil
}
func (s *WorkforceTelegramService) publishAdminPunchGroupReply(ctx context.Context, chatID string, empID uuid.UUID, groupHTML string) {
	chatID = strings.TrimSpace(chatID)
	groupHTML = strings.TrimSpace(groupHTML)
	if chatID == "" || groupHTML == "" {
		return
	}
	html := "🖥 <b>后台打卡</b>\n" + groupHTML
	replyTo := s.getEmployeeLastGroupBotMessageID(ctx, chatID, empID)
	msgID, err := s.SendTelegramHTMLReplyReturnID(ctx, chatID, html, replyTo, nil)
	if err != nil {
		log.Printf("[Telegram] admin punch group notify failed chat=%s emp=%s: %v", chatID, empID, err)
		return
	}
	if msgID > 0 {
		s.setEmployeeLastGroupBotMessageID(ctx, chatID, empID, msgID)
	}
}
func (s *WorkforceTelegramService) AdminPunchCheckIn(ctx context.Context, empID uuid.UUID, shift string) (*AdminPunchResult, error) {
	tgUID, chatID, actor, empName, err := s.resolveAdminPunchContext(ctx, empID)
	if err != nil {
		return nil, err
	}
	shift = strings.TrimSpace(strings.ToLower(shift))
	if shift == "" {
		shift = "day"
	}
	if _, err = normalizeShiftType(shift); err != nil {
		return nil, err
	}
	outcome, err := s.telegramCheckInWithShift(ctx, empID, tgUID, chatID, shift, actor)
	if err != nil {
		return nil, err
	}
	s.publishAdminPunchGroupReply(ctx, chatID, empID, outcome.GroupReplyHTML)

	msg := fmt.Sprintf("上班打卡成功（%s）", ShiftLabelPublic(outcome.Result.ShiftType))
	if outcome.WorkFine > 0 {
		msg += fmt.Sprintf("，迟到罚款 %s", FormatFineMoney(outcome.WorkFine, s.FineCurrencyCode(ctx)))
	}
	if hint := strings.TrimSpace(outcome.HandoverHint); hint != "" {
		msg += "\n" + hint
	}
	return &AdminPunchResult{
		Result:         outcome.Result,
		EmployeeName:   empName,
		WorkFine:       outcome.WorkFine,
		HandoverHint:   outcome.HandoverHint,
		GroupReplyHTML: outcome.GroupReplyHTML,
		MessageText:    msg,
	}, nil
}
func (s *WorkforceTelegramService) AdminPunchCheckOut(ctx context.Context, empID uuid.UUID, shift string) (*AdminPunchResult, error) {
	tgUID, chatID, actor, empName, err := s.resolveAdminPunchContext(ctx, empID)
	if err != nil {
		return nil, err
	}
	_ = shift // FIFO checkout matches TG bot behavior
	outcome, err := s.telegramCheckOutFIFO(ctx, empID, tgUID, chatID, actor)
	if err != nil {
		return nil, err
	}
	s.publishAdminPunchGroupReply(ctx, chatID, empID, outcome.GroupReplyHTML)

	msg := fmt.Sprintf("下班打卡成功（%s）", ShiftLabelPublic(outcome.ShiftType))
	if outcome.WorkFine > 0 {
		msg += fmt.Sprintf("，早退相关金额 %s", FormatFineMoney(outcome.WorkFine, s.FineCurrencyCode(ctx)))
	}
	if warn := strings.TrimSpace(outcome.ActivityWarn); warn != "" {
		msg += "\n" + warn
	}
	if hint := strings.TrimSpace(outcome.HandoverHint); hint != "" {
		msg += "\n" + hint
	}
	return &AdminPunchResult{
		CheckOut:       outcome.Result,
		EmployeeName:   empName,
		WorkFine:       outcome.WorkFine,
		HandoverHint:   outcome.HandoverHint,
		ActivityWarn:   outcome.ActivityWarn,
		GroupReplyHTML: outcome.GroupReplyHTML,
		MessageText:    msg,
	}, nil
}
func (s *WorkforceTelegramService) AdminPunchStartActivity(ctx context.Context, empID uuid.UUID, activity string) (*TgActivityStartResult, error) {
	if _, _, _, _, err := s.resolveAdminPunchContext(ctx, empID); err != nil {
		return nil, err
	}
	return s.startActivitySessionWithSource(ctx, empID, uuid.Nil, activity, PunchSourceWeb)
}
func (s *WorkforceTelegramService) AdminPunchBackActivity(ctx context.Context, empID uuid.UUID) (*TgActivitySession, error) {
	if _, _, _, _, err := s.resolveAdminPunchContext(ctx, empID); err != nil {
		return nil, err
	}
	session, err := s.getOpenActivitySession(ctx, empID)
	if err != nil {
		return nil, err
	}
	return s.endActivitySessionForEmployee(ctx, session.ID, empID, PunchSourceWeb)
}

// ---- Source: backend/services/workforce_tg_admin_reset.go ----
type EmployeeDailyResetResult struct {
	BusinessDate            string `json:"businessDate"`
	ClosedActivities        int    `json:"closedActivities"`
	ClosedWorkSessions      int    `json:"closedWorkSessions"`
	DeletedWorkSessions     int    `json:"deletedWorkSessions"`
	DeletedActivitySessions int    `json:"deletedActivitySessions"`
	ClearedShiftState       int    `json:"clearedShiftState"`
	AgentNotified           bool   `json:"agentNotified"`
}

// resolveEmployeeResetBusinessDate 计算「单人日重置」要清理的业务日。
// 跨夜班（如 21:00–次日 09:00）在 0 点后、下班时间前，业务日仍是前一天，不能直接取日历日。
// 优先级：员工当前未关闭的班次日期（与打卡/活动一致）> 周期规则(DetermineCurrentPeriod，
// 未绑定群时使用默认夜班/白班分界) > 按系统时区的日历日。
func (s *WorkforceTelegramService) resolveEmployeeResetBusinessDate(ctx context.Context, empID uuid.UUID, chatID string, now time.Time) string {
	loc := time.Local
	if settings, err := s.GetSettings(ctx); err == nil && settings != nil {
		loc = tgLoadLocation(settings.Timezone)
	}
	now = now.In(loc)
	if _, _, openDate, ok := s.getOpenWorkSessionAttendance(ctx, empID); ok && strings.TrimSpace(openDate) != "" {
		if len(openDate) > 10 {
			openDate = openDate[:10]
		}
		return openDate
	}
	if period, err := s.DetermineCurrentPeriod(ctx, strings.TrimSpace(chatID), now); err == nil && period != nil && strings.TrimSpace(period.BusinessDate) != "" {
		return period.BusinessDate
	}
	return now.Format("2006-01-02")
}

func (s *WorkforceTelegramService) ResetEmployeeDailyAttendance(ctx context.Context, employeeID uuid.UUID, chatID string) (*EmployeeDailyResetResult, error) {
	if employeeID == uuid.Nil {
		return nil, fmt.Errorf("employeeId required")
	}
	now := time.Now()
	businessDate := s.resolveEmployeeResetBusinessDate(ctx, employeeID, chatID, now)
	out := &EmployeeDailyResetResult{BusinessDate: businessDate}

	openRows, err := s.db.QueryContext(ctx, `
		SELECT id FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND status = 'open'`, employeeID)
	if err == nil {
		defer openRows.Close()
		for openRows.Next() {
			var sid uuid.UUID
			if openRows.Scan(&sid) != nil {
				continue
			}
			if _, err := s.closeActivitySession(ctx, sid, false, "system"); err == nil {
				out.ClosedActivities++
			}
		}
	}

	// Close open work sessions for this business date before archive (no early-leave fine).
	// Prevents archiving status=open ghosts that later inflate export duration via NOW().
	const employeeDailyResetSkipFine = true
	wsOpenRows, err := s.db.QueryContext(ctx, `
		SELECT id FROM work_sessions
		WHERE employee_id = $1 AND status = 'open' AND attendance_date = $2::date
		ORDER BY checkin_time ASC`, employeeID, businessDate)
	if err == nil {
		defer wsOpenRows.Close()
		for wsOpenRows.Next() {
			var sid uuid.UUID
			if wsOpenRows.Scan(&sid) != nil {
				continue
			}
			if err := s.forceCheckoutWorkSessionAtWithOptions(ctx, sid, chatID, time.Now(), "单人日重置", employeeDailyResetSkipFine); err != nil {
				log.Printf("[Telegram] employee daily reset close work session=%s: %v", sid, err)
				continue
			}
			out.ClosedWorkSessions++
			out.AgentNotified = true
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := s.mergeActivityStatsIntoMonthlyForEmployeeDateTx(ctx, tx, employeeID, businessDate); err != nil {
		return nil, err
	}
	if err := s.archiveActivitySessionsByEmployeeDate(ctx, tx, employeeID, businessDate); err != nil {
		return nil, err
	}
	if err := s.archiveWorkSessionsByEmployeeDate(ctx, tx, employeeID, businessDate); err != nil {
		return nil, err
	}

	actRes, err := tx.ExecContext(ctx, `
		DELETE FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date`,
		employeeID, businessDate)
	if err != nil {
		return nil, err
	}
	if n, _ := actRes.RowsAffected(); n > 0 {
		out.DeletedActivitySessions = int(n)
	}

	res, err := tx.ExecContext(ctx, `
		DELETE FROM work_sessions WHERE employee_id = $1 AND attendance_date = $2::date`,
		employeeID, businessDate)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		out.DeletedWorkSessions = int(n)
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO system_settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, "activity_reset:"+employeeID.String(), now.UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	yearMonth := businessDate
	if len(businessDate) >= 7 {
		yearMonth = businessDate[:7]
	}
	_, _ = s.db.ExecContext(ctx, `
		UPDATE attendance_records SET
			checkin_time = NULL, checkout_time = NULL,
			checkin_source = NULL, checkout_source = NULL,
			shift_type = NULL,
			activity_seconds = NULL,
			status = CASE WHEN manually_edited THEN status ELSE 'absent' END,
			updated_at = NOW()
		WHERE employee_id = $1 AND year_month = $2 AND date = $3::date AND manually_edited = FALSE`,
		employeeID, yearMonth, businessDate)

	res2, _ := s.db.ExecContext(ctx, `
		DELETE FROM wf_tg_group_shift_state WHERE employee_id = $1 AND record_date = $2::date`,
		employeeID, businessDate)
	if n, _ := res2.RowsAffected(); n > 0 {
		out.ClearedShiftState = int(n)
	}

	return out, nil
}

// ---- Source: backend/services/workforce_tg_agent.go ----
type AgentCaptureSyncResult struct {
	DeviceID uuid.UUID
	Synced   bool
	Online   bool
}

func (s *WorkforceTelegramService) resolveAgentSyncTarget(ctx context.Context, empID, preferredDeviceID uuid.UUID) (uuid.UUID, bool, error) {
	if preferredDeviceID != uuid.Nil {
		var status string
		err := s.db.QueryRowContext(ctx, `
			SELECT LOWER(COALESCE(status,'')) FROM devices WHERE id = $1`, preferredDeviceID).Scan(&status)
		if err == nil && status == "approved" {
			online := s.wsHub != nil && s.wsHub.IsAgentOnline(preferredDeviceID.String())
			return preferredDeviceID, online, nil
		}
	}
	deviceID, err := s.findDeviceForEmployee(ctx, empID)
	if err != nil || deviceID == uuid.Nil {
		return uuid.Nil, false, err
	}
	online := s.wsHub != nil && s.wsHub.IsAgentOnline(deviceID.String())
	return deviceID, online, nil
}
func (s *WorkforceTelegramService) syncAgentWorkInFromTelegram(ctx context.Context, empID, sessionID uuid.UUID, actor *TelegramActor) {
	pref := preferredDeviceFromActor(actor)
	s.syncAgentCaptureStart(ctx, empID, sessionID, pref)
	s.syncAgentTelegramCheckInNotify(ctx, empID, sessionID, pref)
	s.syncAgentWorkSessionState(ctx, empID, true, sessionID, resolvePunchSource(actor, PunchSourceTelegram), pref)
}
func (s *WorkforceTelegramService) syncAgentWorkOutFromTelegram(ctx context.Context, empID, sessionID uuid.UUID, actor *TelegramActor, source string) {
	pref := preferredDeviceFromActor(actor)
	s.syncAgentCaptureStop(ctx, empID, sessionID, pref)
	s.syncAgentTelegramCheckOutNotify(ctx, empID, sessionID, pref)
	src := strings.TrimSpace(source)
	if src == "" {
		src = "telegram"
	}
	s.syncAgentWorkSessionState(ctx, empID, false, sessionID, src, pref)
}
func (s *WorkforceTelegramService) linkSessionToDevice(ctx context.Context, sessionID, deviceID uuid.UUID) {
	_, _ = s.db.ExecContext(ctx, `
		UPDATE work_sessions SET device_id = $1, updated_at = NOW()
		WHERE id = $2 AND device_id IS NULL`, deviceID, sessionID)
}
func (s *WorkforceTelegramService) SyncAgentCaptureStart(ctx context.Context, empID, sessionID uuid.UUID) AgentCaptureSyncResult {
	return s.syncAgentCaptureStart(ctx, empID, sessionID, uuid.Nil)
}
func (s *WorkforceTelegramService) syncAgentCaptureStart(ctx context.Context, empID, sessionID, preferredDeviceID uuid.UUID) AgentCaptureSyncResult {
	out := AgentCaptureSyncResult{}
	if !CaptureLinkedToCheckin(ctx, s.db) || s.wsHub == nil {
		return out
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, preferredDeviceID)
	if err != nil || deviceID == uuid.Nil {
		return out
	}
	out.DeviceID = deviceID
	out.Online = online
	s.linkSessionToDevice(ctx, sessionID, deviceID)
	if !online {
		log.Printf("[Telegram] agent capture start skipped: device %s offline (employee %s)", deviceID, empID)
		return out
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "start_work_capture", gin.H{
		"sessionId": sessionID.String(),
		"source":    "telegram",
	})
	out.Synced = true
	return out
}
func (s *WorkforceTelegramService) SyncAgentCaptureStop(ctx context.Context, empID, sessionID uuid.UUID) AgentCaptureSyncResult {
	return s.syncAgentCaptureStop(ctx, empID, sessionID, uuid.Nil)
}
func (s *WorkforceTelegramService) syncAgentCaptureStop(ctx context.Context, empID, sessionID, preferredDeviceID uuid.UUID) AgentCaptureSyncResult {
	out := AgentCaptureSyncResult{}
	if !CaptureLinkedToCheckin(ctx, s.db) || s.wsHub == nil {
		return out
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, preferredDeviceID)
	if err != nil || deviceID == uuid.Nil {
		return out
	}
	out.DeviceID = deviceID
	out.Online = online
	if !online {
		return out
	}
	params := gin.H{"source": "telegram"}
	if sessionID != uuid.Nil {
		params["sessionId"] = sessionID.String()
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "stop_work_capture", params)
	out.Synced = true
	return out
}
func (s *WorkforceTelegramService) SyncAgentTelegramCheckInNotify(ctx context.Context, empID, sessionID uuid.UUID) {
	s.syncAgentTelegramCheckInNotify(ctx, empID, sessionID, uuid.Nil)
}
func (s *WorkforceTelegramService) syncAgentTelegramCheckInNotify(ctx context.Context, empID, sessionID, preferredDeviceID uuid.UUID) {
	if s.wsHub == nil {
		return
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, preferredDeviceID)
	if err != nil || deviceID == uuid.Nil || !online {
		return
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "telegram_checkin_notify", gin.H{
		"sessionId": sessionID.String(),
	})
}
func (s *WorkforceTelegramService) SyncAgentTelegramCheckOutNotify(ctx context.Context, empID, sessionID uuid.UUID) {
	s.syncAgentTelegramCheckOutNotify(ctx, empID, sessionID, uuid.Nil)
}
func (s *WorkforceTelegramService) syncAgentTelegramCheckOutNotify(ctx context.Context, empID, sessionID, preferredDeviceID uuid.UUID) {
	if s.wsHub == nil {
		return
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, preferredDeviceID)
	if err != nil || deviceID == uuid.Nil || !online {
		return
	}
	params := gin.H{}
	if sessionID != uuid.Nil {
		params["sessionId"] = sessionID.String()
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "telegram_checkout_notify", params)
}

// SyncAgentWorkSessionState pushes tray work-session state without requiring capture-linked check-in.
func (s *WorkforceTelegramService) SyncAgentWorkSessionState(ctx context.Context, empID uuid.UUID, open bool, sessionID uuid.UUID, source string) {
	s.syncAgentWorkSessionState(ctx, empID, open, sessionID, source, uuid.Nil)
}
func (s *WorkforceTelegramService) syncAgentWorkSessionState(ctx context.Context, empID uuid.UUID, open bool, sessionID uuid.UUID, source string, preferredDeviceID uuid.UUID) {
	if s.wsHub == nil {
		return
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, preferredDeviceID)
	if err != nil || deviceID == uuid.Nil || !online {
		return
	}
	params := gin.H{"open": open}
	if sessionID != uuid.Nil {
		params["sessionId"] = sessionID.String()
	}
	if strings.TrimSpace(source) != "" {
		params["source"] = strings.TrimSpace(source)
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "work_session_state_sync", params)
}

// SyncAgentActivityState pushes TG/PC activity (活动/回座) changes to the online agent punch panel.
func (s *WorkforceTelegramService) SyncAgentActivityState(
	ctx context.Context,
	empID uuid.UUID,
	open bool,
	session *TgActivitySession,
	source string,
) {
	if s.wsHub == nil || !s.wsHub.HasAnyAgentOnline() {
		return
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, uuid.Nil)
	if err != nil || deviceID == uuid.Nil || !online {
		return
	}
	params := gin.H{"open": open}
	if strings.TrimSpace(source) != "" {
		params["source"] = strings.TrimSpace(source)
	}
	if session != nil {
		if session.ID != uuid.Nil {
			params["sessionId"] = session.ID.String()
		}
		if strings.TrimSpace(session.ActivityName) != "" {
			params["activityName"] = strings.TrimSpace(session.ActivityName)
		}
		if strings.TrimSpace(session.ActivityCode) != "" {
			params["activityCode"] = strings.TrimSpace(session.ActivityCode)
		}
		if session.LimitSeconds > 0 {
			params["limitSeconds"] = session.LimitSeconds
		}
		if !session.StartedAt.IsZero() {
			params["startedAt"] = session.StartedAt.UTC().Format(time.RFC3339)
		}
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "activity_state_sync", params)
}
func (s *WorkforceTelegramService) syncAgentActivityStateForDevice(ctx context.Context, empID, preferredDeviceID uuid.UUID, open bool, session *TgActivitySession, source string) {
	if s.wsHub == nil || !s.wsHub.HasAnyAgentOnline() {
		return // no Agent connected: the device lookup below could only end in "offline"
	}
	deviceID, online, err := s.resolveAgentSyncTarget(ctx, empID, preferredDeviceID)
	if err != nil || deviceID == uuid.Nil || !online {
		return
	}
	params := gin.H{"open": open}
	if strings.TrimSpace(source) != "" {
		params["source"] = strings.TrimSpace(source)
	}
	if session != nil {
		if session.ID != uuid.Nil {
			params["sessionId"] = session.ID.String()
		}
		if strings.TrimSpace(session.ActivityName) != "" {
			params["activityName"] = strings.TrimSpace(session.ActivityName)
		}
		if strings.TrimSpace(session.ActivityCode) != "" {
			params["activityCode"] = strings.TrimSpace(session.ActivityCode)
		}
		if session.LimitSeconds > 0 {
			params["limitSeconds"] = session.LimitSeconds
		}
		if !session.StartedAt.IsZero() {
			params["startedAt"] = session.StartedAt.UTC().Format(time.RFC3339)
		}
	}
	s.wsHub.SendCommandToDevice(deviceID.String(), "activity_state_sync", params)
}
// NotifyAgentWorkEvent posts PC client check-in/out to the bound Telegram check-in group
// using the same HTML templates as in-group TG check-in. Exception alerts go to push targets.
func (s *WorkforceTelegramService) NotifyAgentWorkEvent(ctx context.Context, empID, deviceID uuid.UUID, shift, event string, sessionID uuid.UUID) {
	if empID == uuid.Nil || sessionID == uuid.Nil {
		return
	}
	active, err := s.IsActive(ctx)
	if err != nil || !active {
		return
	}
	var empName string
	var tgUID int64
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(name,'') FROM employees WHERE id = $1`, empID).Scan(&empName)
	if err != nil {
		return
	}
	chatID := s.resolveNotifyChatID(ctx, empID, deviceID)
	if strings.TrimSpace(chatID) == "" {
		log.Printf("[Telegram] agent work event skipped: no notify chat emp=%s device=%s event=%s", empID, deviceID, event)
		return
	}
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(telegram_user_id,0) FROM employee_telegram_bindings
		WHERE employee_id = $1 AND status = 1 ORDER BY updated_at DESC NULLS LAST LIMIT 1`, empID).Scan(&tgUID)
	if tgUID == 0 && deviceID != uuid.Nil {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(telegram_user_id,0) FROM device_telegram_bindings
			WHERE device_id = $1 AND status = 1`, deviceID).Scan(&tgUID)
	}
	settings, _, _, _, err := s.loadExtendedSettings(ctx)
	if err != nil {
		return
	}
	grace, _ := s.GetGraceConfig(ctx, chatID)
	loc := tgLoadLocation(settings.Timezone)

	switch event {
	case "上班":
		s.notifyAgentCheckInGroup(ctx, empID, tgUID, chatID, empName, sessionID, shift, settings, grace, loc)
	case "下班":
		s.notifyAgentCheckOutGroup(ctx, empID, tgUID, chatID, empName, sessionID, shift, settings, grace, loc)
	}
}
func (s *WorkforceTelegramService) notifyAgentCheckInGroup(
	ctx context.Context, empID uuid.UUID, tgUID int64, chatID, empName string, sessionID uuid.UUID,
	shift string, settings *TelegramSettings, grace TgGraceConfig, loc *time.Location,
) {
	var shiftType, shiftDetail, recordDate string
	var checkin time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day'), COALESCE(shift_detail,''), attendance_date::text, checkin_time
		FROM work_sessions WHERE id = $1`, sessionID).
		Scan(&shiftType, &shiftDetail, &recordDate, &checkin)
	if err != nil {
		return
	}
	if strings.TrimSpace(shift) == "" {
		shift = shiftType
	}
	checkin = checkin.In(loc)
	period, _ := s.DetermineCurrentPeriod(ctx, chatID, checkin)
	startClock := s.resolveWorkStartClock(ctx, chatID, settings, shiftType, recordDate)
	expectedStart := resolveWorkStartExpectedTime(checkin, recordDate, shiftType, shiftDetail, startClock, loc)
	lateMin := minutesLateFromExpected(checkin, expectedStart, grace.LateTolerance)
	fineReason := fmt.Sprintf("%s上班迟到 %d 分钟（PC客户端）", shiftLabel(shiftType)+shiftDetailLabel(shiftDetail), lateMin)
	workFine := 0.0
	createdReview, _ := s.maybeCreateLateCheckinReviewTask(ctx, empID, shiftType, recordDate, chatID, lateMin, empName)
	if !createdReview {
		workFine, _ = s.applyWorkFine(ctx, empID, "work_start", lateMin, recordDate, fineReason)
	}

	if chatID != "" {
		_ = s.setGroupShiftState(ctx, chatID, empID, shiftType, recordDate)
	}

	hint := ""
	replyLang := s.replyLangForEmployeeGroupNotify(ctx, empID, chatID)
	if period != nil {
		hint = s.handoverStatusHint(ctx, chatID, period, replyLang)
		hint += s.buildRelaySuccessorHintAfterCheckIn(ctx, empID, chatID, shiftType, recordDate, checkin, replyLang)
	}
	bodyHTML := buildWorkStartGroupReplyHTML(
		replyLang, tgUID, empName, shiftType, shiftDetail, checkin, expectedStart, lateMin, workFine, strings.TrimSpace(hint),
		s.FineCurrencyCode(ctx),
	)
	if createdReview {
		bodyHTML += "\n\n" + attendanceReviewPendingHint("late", replyLang)
	}
	pcPrefix := activityGroupNotifyPrefix(PunchSourceAgent, replyLang)
	groupHTML := pcPrefix + bodyHTML
	replyTo := s.getEmployeeLastGroupBotMessageID(ctx, chatID, empID)
	if msgID, err := s.SendTelegramHTMLReplyReturnID(ctx, chatID, groupHTML, replyTo, nil); err != nil {
		log.Printf("[Telegram] agent check-in group notify failed chat=%s emp=%s: %v", chatID, empID, err)
	} else {
		s.setEmployeeLastGroupBotMessageID(ctx, chatID, empID, msgID)
	}
	activitySec := s.sumTodayActivitySeconds(ctx, empID, recordDate, shiftType)
	s.NotifyWorkExceptionPushHTML(ctx, chatID, empID, empName, shiftLabel(shiftType)+shiftDetailLabel(shiftDetail), "上班", workFine, lateMin, 0, activitySec, bodyHTML, pcPrefix)
}
func (s *WorkforceTelegramService) notifyAgentCheckOutGroup(
	ctx context.Context, empID uuid.UUID, tgUID int64, chatID, empName string, sessionID uuid.UUID,
	shift string, settings *TelegramSettings, grace TgGraceConfig, loc *time.Location,
) {
	var shiftType, shiftDetail, recordDate string
	var checkin, checkout time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day'), COALESCE(shift_detail,''), attendance_date::text, checkin_time, checkout_time
		FROM work_sessions WHERE id = $1 AND status = 'closed'`, sessionID).
		Scan(&shiftType, &shiftDetail, &recordDate, &checkin, &checkout)
	if err != nil {
		return
	}
	if strings.TrimSpace(shift) == "" {
		shift = shiftType
	}
	checkin = checkin.In(loc)
	checkout = checkout.In(loc)
	now := checkout

	expectedEnd := s.expectedWorkEnd(ctx, chatID, settings, shiftType, recordDate, now)
	earlyMin := minutesEarlyFromExpected(now, expectedEnd, grace.EarlyLeaveTolerance)
	fineReason := fmt.Sprintf("%s下班早退 %d 分钟（PC客户端）", shiftLabel(shiftType), earlyMin)
	workFine := 0.0
	createdReview, _ := s.maybeCreateAttendanceReviewTask(ctx, empID, shiftType, recordDate, chatID, checkin, now, earlyMin, empName)
	if !createdReview {
		workFine, _ = s.applyWorkFine(ctx, empID, "work_end", earlyMin, recordDate, fineReason)
	}

	replyLang := s.replyLangForEmployeeGroupNotify(ctx, empID, chatID)
	activityWarn := s.buildCrossShiftActivityWarn(ctx, empID, shiftType, recordDate, replyLang)
	if chatID != "" {
		s.clearGroupShiftState(ctx, chatID, empID, shiftType)
		s.maybeBroadcastShiftEnd(ctx, chatID, shiftType, recordDate)
	}

	workSec := int(math.Max(0, now.Sub(checkin).Seconds()))
	activitySec := s.sumTodayActivitySeconds(ctx, empID, recordDate, shiftType)
	handoverHint := s.buildRelayCheckoutHint(ctx, chatID, empID, shiftType, recordDate, now, replyLang)
	bodyHTML := buildWorkEndGroupReplyHTML(
		replyLang, tgUID, empName, shiftType, now, expectedEnd, earlyMin, workFine, handoverHint, activityWarn,
		s.FineCurrencyCode(ctx),
	)
	if createdReview {
		bodyHTML += "\n\n" + attendanceReviewPendingHint("early", replyLang)
	}
	pcPrefix := activityGroupNotifyPrefix(PunchSourceAgent, replyLang)
	groupHTML := pcPrefix + bodyHTML
	replyTo := s.getEmployeeLastGroupBotMessageID(ctx, chatID, empID)
	if msgID, err := s.SendTelegramHTMLReplyReturnID(ctx, chatID, groupHTML, replyTo, nil); err != nil {
		log.Printf("[Telegram] agent check-out group notify failed chat=%s emp=%s: %v", chatID, empID, err)
	} else {
		s.setEmployeeLastGroupBotMessageID(ctx, chatID, empID, msgID)
	}
	s.NotifyWorkExceptionPushHTML(ctx, chatID, empID, empName, shiftLabel(shiftType), "下班", workFine, earlyMin, workSec, activitySec, bodyHTML, pcPrefix)
}
// ---- Source: backend/services/workforce_tg_attendance_review.go ----
const (
	perfReviewDefaultWorkHours  = 5 // 迟到阈值（小时）；JSON 字段仍为 workHours，兼容前端
	perfReviewDefaultEarlyHours = 5 // 早退提前阈值（小时）
	triggerEarlyCheckoutReview  = "early_checkout_review"
	triggerLateCheckinReview    = "late_checkin_review"
)

type AttendanceReviewSettings struct {
	WorkHours  int `json:"workHours"`  // 迟到超过该小时数 → 待确认
	EarlyHours int `json:"earlyHours"` // 早退提前超过该小时数 → 待确认
}
type AttendanceReviewTask struct {
	ID             uuid.UUID `json:"id"`
	EmployeeID     uuid.UUID `json:"employeeId"`
	EmployeeName   string    `json:"employeeName"`
	Position       string    `json:"position"`
	AttendanceDate string    `json:"attendanceDate"`
	ShiftType      string    `json:"shiftType"`
	ChatID         string    `json:"chatId"`
	TriggerType    string    `json:"triggerType"`
	WorkSeconds    int       `json:"workSeconds"`
	EarlyMinutes   int       `json:"earlyMinutes"` // 早退分钟；迟到场景复用为迟到分钟
	ProposedFine   float64   `json:"proposedFine"`
	Status         string    `json:"status"`
	CreatedAt      string    `json:"createdAt"`
}

func clampReviewHours(v int) int {
	if v < 1 {
		return 1
	}
	if v > 24 {
		return 24
	}
	return v
}
func (s *WorkforceTelegramService) GetAttendanceReviewSettings(ctx context.Context) AttendanceReviewSettings {
	out := AttendanceReviewSettings{
		WorkHours:  perfReviewDefaultWorkHours,
		EarlyHours: perfReviewDefaultEarlyHours,
	}
	var val sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = 'tg_review_work_hours'`).Scan(&val); err == nil && val.Valid {
		if n, err := atoiSafe(strings.TrimSpace(val.String)); err == nil {
			out.WorkHours = clampReviewHours(n)
		}
	}
	val = sql.NullString{}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = 'tg_review_early_hours'`).Scan(&val); err == nil && val.Valid {
		if n, err := atoiSafe(strings.TrimSpace(val.String)); err == nil {
			out.EarlyHours = clampReviewHours(n)
		}
	}
	return out
}
func atoiSafe(v string) (int, error) {
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	return n, err
}
func (s *WorkforceTelegramService) SaveAttendanceReviewSettings(ctx context.Context, in AttendanceReviewSettings) (AttendanceReviewSettings, error) {
	in.WorkHours = clampReviewHours(in.WorkHours)
	in.EarlyHours = clampReviewHours(in.EarlyHours)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_settings (key, value, updated_at)
		VALUES ('tg_review_work_hours', $1, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, fmt.Sprintf("%d", in.WorkHours))
	if err != nil {
		return in, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO system_settings (key, value, updated_at)
		VALUES ('tg_review_early_hours', $1, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, fmt.Sprintf("%d", in.EarlyHours))
	return in, err
}
func attendanceReviewConfirmFooter(adminMentions string) string {
	adminMentions = strings.TrimSpace(adminMentions)
	if adminMentions == "" {
		return "请管理员在【罚款详情】确认：旷工 / 半休 / 半假\n未确认前：不记罚款，不改考勤状态。"
	}
	return adminMentions + " 请确认：旷工 / 半休 / 半假（亦可在后台【罚款详情】处理）\n未确认前：不记罚款，不改考勤状态。"
}
func (s *WorkforceTelegramService) attendanceReviewConfirmFooter(ctx context.Context) string {
	return attendanceReviewConfirmFooter(formatAdminMentionsHTML(s.listAdminTelegramUserIDs(ctx)))
}
func attendanceReviewKeyboard(taskID uuid.UUID) map[string]interface{} {
	id := taskID.String()
	return map[string]interface{}{
		"inline_keyboard": [][]map[string]interface{}{
			{
				{"text": "旷工", "callback_data": "rv:" + id + ":abs"},
				{"text": "半休", "callback_data": "rv:" + id + ":rest"},
				{"text": "半假", "callback_data": "rv:" + id + ":leave"},
			},
		},
	}
}
func (s *WorkforceTelegramService) resolveEmployeeChatID(ctx context.Context, empID uuid.UUID, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	var chatID sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(chat_id,'') FROM employee_telegram_bindings
		WHERE employee_id = $1 AND status = 1
		ORDER BY updated_at DESC NULLS LAST LIMIT 1`, empID).Scan(&chatID)
	return strings.TrimSpace(chatID.String)
}
func (s *WorkforceTelegramService) employeeNameByID(ctx context.Context, empID uuid.UUID) string {
	var name string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(name,'') FROM employees WHERE id = $1`, empID).Scan(&name)
	return name
}

// maybeCreateAttendanceReviewTask：早退提前超过阈值时进入【罚款详情】待确认，未确认前不记罚款。
func (s *WorkforceTelegramService) maybeCreateAttendanceReviewTask(
	ctx context.Context,
	empID uuid.UUID,
	shift, recordDate, chatID string,
	checkinAt, checkoutAt time.Time,
	earlyMin int,
	employeeName string,
) (bool, error) {
	cfg := s.GetAttendanceReviewSettings(ctx)
	workSec := int(checkoutAt.Sub(checkinAt).Seconds())
	if workSec < 0 {
		workSec = 0
	}
	// 仅按「提前下班 ≥ 阈值小时」进入待确认（与迟到阈值相互独立）
	if earlyMin < cfg.EarlyHours*60 {
		return false, nil
	}
	if len(recordDate) < 7 {
		return false, nil
	}
	yearMonth := recordDate[:7]
	chatID = s.resolveEmployeeChatID(ctx, empID, chatID)
	fineMap := s.loadWorkFineMap(ctx, "work_end")
	proposedFine := computeActivityOvertimeFine(fineMap, float64(earlyMin))

	var taskID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO wf_tg_attendance_review_tasks (
			employee_id, attendance_date, year_month, shift_type, chat_id, trigger_type,
			work_seconds, early_minutes, overtime_threshold_hours, early_threshold_hours, proposed_fine, status, updated_at
		) VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'pending', NOW())
		ON CONFLICT (employee_id, attendance_date, shift_type, trigger_type) DO NOTHING
		RETURNING id`,
		empID, recordDate, yearMonth, shift, chatID, triggerEarlyCheckoutReview,
		workSec, earlyMin, cfg.WorkHours, cfg.EarlyHours, proposedFine,
	).Scan(&taskID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if strings.TrimSpace(chatID) != "" {
		msg := fmt.Sprintf(
			"⚠️ <b>考勤待确认</b>（早退）\n👤 %s\n📅 %s（%s）\n⏱ 上班时长：%s\n🕐 提前下班：%d 分钟\n\n%s",
			htmlPackage2.EscapeString(employeeName),
			recordDate, shiftLabel(shift),
			formatDurationZh(workSec), earlyMin,
			s.attendanceReviewConfirmFooter(ctx),
		)
		msgID, sendErr := s.SendTelegramHTMLReplyReturnID(ctx, chatID, msg, 0, attendanceReviewKeyboard(taskID))
		if sendErr == nil && msgID > 0 {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_attendance_review_tasks SET tg_message_id = $2, updated_at = NOW() WHERE id = $1`, taskID, msgID)
		}
	}
	NotifyAttendanceReviewPendingUsers(ctx, s.db, taskID, employeeName, recordDate, triggerEarlyCheckoutReview)
	return true, nil
}

// maybeCreateLateCheckinReviewTask：上班迟到超过阈值时进入【罚款详情】待确认，未确认前不记罚款、群消息不显示罚款金额。
func (s *WorkforceTelegramService) maybeCreateLateCheckinReviewTask(
	ctx context.Context,
	empID uuid.UUID,
	shift, recordDate, chatID string,
	lateMin int,
	employeeName string,
) (bool, error) {
	cfg := s.GetAttendanceReviewSettings(ctx)
	if lateMin < cfg.WorkHours*60 {
		return false, nil
	}
	if len(recordDate) < 7 {
		return false, nil
	}
	yearMonth := recordDate[:7]
	chatID = s.resolveEmployeeChatID(ctx, empID, chatID)
	fineMap := s.loadWorkFineMap(ctx, "work_start")
	proposedFine := computeActivityOvertimeFine(fineMap, float64(lateMin))

	var taskID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO wf_tg_attendance_review_tasks (
			employee_id, attendance_date, year_month, shift_type, chat_id, trigger_type,
			work_seconds, early_minutes, overtime_threshold_hours, early_threshold_hours, proposed_fine, status, updated_at
		) VALUES ($1, $2::date, $3, $4, $5, $6, 0, $7, $8, $9, $10, 'pending', NOW())
		ON CONFLICT (employee_id, attendance_date, shift_type, trigger_type) DO NOTHING
		RETURNING id`,
		empID, recordDate, yearMonth, shift, chatID, triggerLateCheckinReview,
		lateMin, cfg.WorkHours, cfg.EarlyHours, proposedFine,
	).Scan(&taskID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if strings.TrimSpace(chatID) != "" {
		msg := fmt.Sprintf(
			"⚠️ <b>考勤待确认</b>（迟到）\n👤 %s\n📅 %s（%s）\n🕐 迟到：%d 分钟（超过 %d 小时阈值）\n💰 预计旷工罚款：%s\n\n%s",
			htmlPackage2.EscapeString(employeeName),
			recordDate, shiftLabel(shift),
			lateMin, cfg.WorkHours,
			FormatFineMoney(proposedFine, s.FineCurrencyCode(ctx)),
			s.attendanceReviewConfirmFooter(ctx),
		)
		msgID, sendErr := s.SendTelegramHTMLReplyReturnID(ctx, chatID, msg, 0, attendanceReviewKeyboard(taskID))
		if sendErr == nil && msgID > 0 {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_attendance_review_tasks SET tg_message_id = $2, updated_at = NOW() WHERE id = $1`, taskID, msgID)
		}
	}
	NotifyAttendanceReviewPendingUsers(ctx, s.db, taskID, employeeName, recordDate, triggerLateCheckinReview)
	return true, nil
}
func (s *WorkforceTelegramService) scanAndCreateAttendanceReviewTasks(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return
	}
	loc := tgLoadLocation(settings.Timezone)
	rows, err := s.db.QueryContext(ctx, `
		SELECT ws.employee_id, COALESCE(ws.shift_type,'day'), ws.attendance_date::text,
		       ws.checkin_time, ws.checkout_time, COALESCE(ws.source_chat_id,''), e.name
		FROM work_sessions ws
		JOIN employees e ON e.id = ws.employee_id
		WHERE ws.status = 'closed'
		  AND ws.source IN ('telegram','agent','web')
		  AND ws.checkout_time IS NOT NULL
		  AND ws.checkout_time >= NOW() - INTERVAL '12 hours'`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var empID uuid.UUID
		var shift, recordDate, chatID, empName string
		var checkinAt, checkoutAt time.Time
		if rows.Scan(&empID, &shift, &recordDate, &checkinAt, &checkoutAt, &chatID, &empName) != nil {
			continue
		}
		expectedEnd := s.expectedWorkEnd(ctx, chatID, settings, shift, recordDate, checkoutAt.In(loc))
		earlyMin := minutesEarlyFromExpected(checkoutAt.In(loc), expectedEnd, 0)
		_, _ = s.maybeCreateAttendanceReviewTask(ctx, empID, shift, recordDate, chatID, checkinAt.In(loc), checkoutAt.In(loc), earlyMin, empName)
	}
}
func (s *WorkforceTelegramService) ListAttendanceReviewTasks(ctx context.Context, month, search string) ([]AttendanceReviewTask, error) {
	month = strings.TrimSpace(month)
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	search = strings.TrimSpace(search)
	args := []interface{}{month}
	where := "t.year_month = $1 AND t.status = 'pending'"
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(" AND e.name ILIKE $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT t.id, t.employee_id, e.name, COALESCE(e.position,''), t.attendance_date::text,
		       t.shift_type, t.chat_id, t.trigger_type, t.work_seconds, t.early_minutes,
		       COALESCE(t.proposed_fine,0), t.status, t.created_at
		FROM wf_tg_attendance_review_tasks t
		JOIN employees e ON e.id = t.employee_id
		WHERE %s
		ORDER BY t.created_at DESC`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AttendanceReviewTask, 0)
	for rows.Next() {
		var item AttendanceReviewTask
		var createdAt time.Time
		if rows.Scan(&item.ID, &item.EmployeeID, &item.EmployeeName, &item.Position, &item.AttendanceDate,
			&item.ShiftType, &item.ChatID, &item.TriggerType, &item.WorkSeconds, &item.EarlyMinutes,
			&item.ProposedFine, &item.Status, &createdAt) != nil {
			continue
		}
		item.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		out = append(out, item)
	}
	return out, nil
}
func (s *WorkforceTelegramService) confirmAttendanceReviewTask(
	ctx context.Context,
	taskID uuid.UUID,
	decision, actor, source string,
) (string, error) {
	decision = strings.TrimSpace(strings.ToLower(decision))
	if decision != "absent" && decision != "rest_half" && decision != "leave" {
		return "", fmt.Errorf("invalid decision")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var empID uuid.UUID
	var dateStr, yearMonth, shift, status, triggerType string
	var proposedFine float64
	var reviewChatID string
	var reviewMessageID sql.NullInt64
	if err = tx.QueryRowContext(ctx, `
		SELECT employee_id, attendance_date::text, year_month, COALESCE(shift_type,'day'),
		       status, COALESCE(proposed_fine,0), COALESCE(trigger_type,''),
		       COALESCE(chat_id,''), tg_message_id
		FROM wf_tg_attendance_review_tasks
		WHERE id = $1
		FOR UPDATE`, taskID).Scan(
		&empID, &dateStr, &yearMonth, &shift, &status, &proposedFine, &triggerType,
		&reviewChatID, &reviewMessageID,
	); err != nil {
		return "", err
	}
	if status != "pending" {
		return "该待确认任务已处理", nil
	}
	targetStatus := "work"
	taskStatus := "confirmed"
	switch decision {
	case "absent":
		targetStatus = "absent"
		taskStatus = "confirmed_absent"
	case "rest_half":
		targetStatus = "rest_half"
		taskStatus = "confirmed_rest_half"
	case "leave":
		targetStatus = "leave"
		taskStatus = "confirmed_leave_half"
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE attendance_records
		SET status = $4, manually_edited = TRUE, updated_at = NOW()
		WHERE employee_id = $1 AND year_month = $2 AND date = $3::date`,
		empID, yearMonth, dateStr, targetStatus); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE wf_tg_attendance_review_tasks
		SET status = $2, confirmed_by = NULLIF($3,''), confirm_source = NULLIF($4,''),
			confirmed_at = NOW(), updated_at = NOW()
		WHERE id = $1`, taskID, taskStatus, actor, source); err != nil {
		return "", err
	}

	msg := "已确认"
	if decision == "absent" {
		var empName, position string
		_ = tx.QueryRowContext(ctx, `SELECT name, COALESCE(position,'') FROM employees WHERE id = $1`, empID).Scan(&empName, &position)
		reason := fmt.Sprintf("%s早退待确认，管理员确认为旷工", shiftLabel(shift))
		if triggerType == triggerLateCheckinReview {
			reason = fmt.Sprintf("%s上班迟到待确认，管理员确认为旷工", shiftLabel(shift))
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO penalty_records (id, employee_id, employee_name, position, amount, category, reason, penalty_date, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, 'TG考勤', $6, $7::date, 'telegram', NOW())`,
			uuid.NewString(), empID, empName, position, proposedFine, reason, dateStr); err != nil {
			return "", err
		}
		s.pushAutoPenaltyNotify(ctx, empID, proposedFine, "TG考勤", reason, dateStr)
		msg = fmt.Sprintf("已确认旷工，并记录罚款 %s", FormatFineMoney(proposedFine, s.FineCurrencyCode(ctx)))
	} else if decision == "rest_half" {
		msg = "已确认半休"
	} else if decision == "leave" {
		msg = "已确认半假"
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	// Confirmation is already committed; Telegram cleanup is best-effort and
	// must not roll back attendance if the Bot API is temporarily unavailable.
	if reviewMessageID.Valid && reviewMessageID.Int64 > 0 && strings.TrimSpace(reviewChatID) != "" {
		_ = s.RemoveTelegramInlineKeyboard(ctx, reviewChatID, reviewMessageID.Int64)
	}
	return msg, nil
}
func (s *WorkforceTelegramService) ConfirmAttendanceReviewTaskByAdmin(ctx context.Context, taskID uuid.UUID, decision, adminName string) (string, error) {
	return s.confirmAttendanceReviewTask(ctx, taskID, decision, adminName, "admin")
}
func (s *WorkforceTelegramService) ConfirmAttendanceReviewTaskByTelegram(ctx context.Context, taskID uuid.UUID, decision string, telegramUserID int64) (string, error) {
	if telegramUserID <= 0 || !s.IsAdminTelegramUser(ctx, telegramUserID) {
		return "", fmt.Errorf("admin only")
	}
	actor := fmt.Sprintf("tg:%d", telegramUserID)
	return s.confirmAttendanceReviewTask(ctx, taskID, decision, actor, "telegram")
}

// ---- Source: backend/services/workforce_tg_bind.go ----
func (s *WorkforceTelegramService) telegramUserBoundEmployee(ctx context.Context, telegramUserID int64) (uuid.UUID, error) {
	var empID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT employee_id FROM employee_telegram_bindings
		WHERE telegram_user_id = $1 AND status = 1`, telegramUserID).Scan(&empID)
	if err == sql.ErrNoRows {
		return uuid.Nil, nil
	}
	return empID, err
}
func (s *WorkforceTelegramService) employeeTelegramBound(ctx context.Context, employeeID uuid.UUID) (int64, error) {
	var tgUID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT telegram_user_id FROM employee_telegram_bindings
		WHERE employee_id = $1 AND status = 1`, employeeID).Scan(&tgUID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return tgUID, err
}

// BindTelegramByInviteAndCredentials binds personal TG with invite + username (password optional).
func (s *WorkforceTelegramService) BindTelegramByInviteAndCredentials(ctx context.Context, telegramUserID int64, telegramUsername, inviteCode, username, password, chatID string) (*TelegramBindingRow, error) {
	if telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid telegram user id")
	}
	if s.workforce == nil {
		return nil, fmt.Errorf("workforce service unavailable")
	}
	inviteCode = strings.TrimSpace(inviteCode)
	if inviteCode == "" {
		return nil, fmt.Errorf("invite code required")
	}
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if err := s.ensureTelegramNotCrossBound(ctx, telegramUserID, TelegramChannelPersonal); err != nil {
		return nil, err
	}
	invite, err := s.workforce.ResolveInviteCodeEmployee(ctx, inviteCode)
	if err != nil {
		return nil, err
	}
	if username != "" {
		if password != "" {
			if err := s.workforce.ValidateEmployeeCredentials(ctx, username, password, invite.EmployeeID); err != nil {
				return nil, err
			}
		} else if err := s.workforce.ValidateEmployeeUsername(ctx, username, invite.EmployeeID); err != nil {
			return nil, err
		}
	}
	if boundEmp, err := s.telegramUserBoundEmployee(ctx, telegramUserID); err != nil {
		return nil, err
	} else if boundEmp != uuid.Nil && boundEmp != invite.EmployeeID {
		return nil, ErrTelegramAlreadyBound
	}
	if otherTG, err := s.employeeTelegramBound(ctx, invite.EmployeeID); err != nil {
		return nil, err
	} else if otherTG > 0 && otherTG != telegramUserID {
		return nil, fmt.Errorf("该员工已绑定其他 Telegram 账号")
	}
	row, err := s.CreateBinding(ctx, invite.EmployeeID, telegramUserID, telegramUsername, chatID)
	if err != nil {
		return nil, err
	}
	_ = s.setEmployeeAttendanceMode(ctx, invite.EmployeeID, AttendanceModeTGOnly)
	return row, nil
}

// BindTelegramByUsername binds personal TG with login username only (no invite).
func (s *WorkforceTelegramService) BindTelegramByUsername(ctx context.Context, telegramUserID int64, telegramUsername, username, chatID string) (*TelegramBindingRow, error) {

	if telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid telegram user id")
	}
	if s.workforce == nil {
		return nil, fmt.Errorf("workforce service unavailable")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("账号不能为空，用法：/bind 账号")
	}
	if err := s.ensureTelegramNotCrossBound(ctx, telegramUserID, TelegramChannelPersonal); err != nil {
		return nil, err
	}
	var userID uuid.UUID
	var status int
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.status FROM users u WHERE u.username = $1`, username).Scan(&userID, &status)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if status != 1 {
		return nil, ErrInvalidCredentials
	}
	empID, err := s.workforce.ResolveEmployeeByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrEmployeeNotFound) {
			return nil, ErrNotEmployeeRole
		}
		return nil, err
	}
	if boundEmp, err := s.telegramUserBoundEmployee(ctx, telegramUserID); err != nil {
		return nil, err
	} else if boundEmp != uuid.Nil && boundEmp != empID {
		return nil, ErrTelegramAlreadyBound
	}
	if otherTG, err := s.employeeTelegramBound(ctx, empID); err != nil {
		return nil, err
	} else if otherTG > 0 && otherTG != telegramUserID {
		return nil, fmt.Errorf("该员工已绑定其他 Telegram 账号")
	}
	row, err := s.createSelfBinding(ctx, empID, telegramUserID, telegramUsername, chatID)
	if err != nil {
		return nil, err
	}
	_ = s.setEmployeeAttendanceMode(ctx, empID, AttendanceModeTGOnly)
	return row, nil
}

// BindWorkstationTelegramByInviteCode binds a shared workstation TG account to a device.
// Legacy one-TG-one-device path: rejects if this Telegram is already bound to a different device.
func (s *WorkforceTelegramService) BindWorkstationTelegramByInviteCode(ctx context.Context, telegramUserID int64, telegramUsername, inviteCode, chatID string) (*DeviceTelegramBindingRow, error) {
	if telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid telegram user id")
	}
	if s.workforce == nil {
		return nil, fmt.Errorf("workforce service unavailable")
	}
	inviteCode = strings.TrimSpace(inviteCode)
	if inviteCode == "" {
		return nil, fmt.Errorf("invite code required")
	}
	if err := s.ensureTelegramNotCrossBound(ctx, telegramUserID, TelegramChannelWorkstation); err != nil {
		return nil, err
	}
	invite, err := s.workforce.ResolveInviteCodeEmployee(ctx, inviteCode)
	if err != nil {
		return nil, err
	}
	deviceID, err := s.findSharedWorkstationDeviceForEmployee(ctx, invite.EmployeeID)
	if err != nil {
		return nil, err
	}
	ok, err := s.deviceIsSharedWorkstation(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrWorkstationNotConfigured
	}
	var existingDevice uuid.UUID
	err = s.db.QueryRowContext(ctx, `
		SELECT device_id FROM device_telegram_bindings
		WHERE telegram_user_id = $1 AND status = 1`, telegramUserID).Scan(&existingDevice)
	if err == nil && existingDevice != uuid.Nil && existingDevice != deviceID {
		return nil, ErrTelegramAlreadyBound
	}
	var otherTG int64
	err = s.db.QueryRowContext(ctx, `
		SELECT telegram_user_id FROM device_telegram_bindings
		WHERE device_id = $1 AND status = 1`, deviceID).Scan(&otherTG)
	if err == nil && otherTG > 0 && otherTG != telegramUserID {
		return nil, ErrDeviceTelegramAlreadyBound
	}
	var id uuid.UUID
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO device_telegram_bindings (device_id, telegram_user_id, telegram_username, chat_id, status)
		VALUES ($1, $2, $3, $4, 1)
		ON CONFLICT (device_id) DO UPDATE SET
			telegram_user_id = EXCLUDED.telegram_user_id,
			telegram_username = EXCLUDED.telegram_username,
			chat_id = CASE
				WHEN COALESCE(NULLIF(TRIM(EXCLUDED.chat_id), ''), '') <> '' THEN EXCLUDED.chat_id
				ELSE device_telegram_bindings.chat_id
			END,
			status = 1,
			bound_at = NOW()
		RETURNING id`, deviceID, telegramUserID, nullString(strings.TrimSpace(telegramUsername)), nullString(strings.TrimSpace(chatID))).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE devices SET is_shared_workstation = TRUE WHERE id = $1`, deviceID)
	items, err := s.ListDeviceTelegramBindings(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("workstation binding not found after create")
}

// BindWorkstationTelegramMulti binds workstation TG like the old multi-device flow
// (/bind 设备ID 邀请码 repeated for each PC): same Telegram may cover multiple devices.
// Resolves the employee by account or invite, then binds every shared-workstation device
// that employee belongs to (equivalent to issuing one old device-ID bind per PC).
func (s *WorkforceTelegramService) BindWorkstationTelegramMulti(
	ctx context.Context,
	telegramUserID int64,
	telegramUsername, inviteCode, username, chatID string,
) ([]DeviceTelegramBindingRow, error) {
	if telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid telegram user id")
	}
	if s.workforce == nil {
		return nil, fmt.Errorf("workforce service unavailable")
	}
	inviteCode = strings.TrimSpace(inviteCode)
	username = strings.TrimSpace(username)
	if inviteCode == "" && username == "" {
		return nil, fmt.Errorf("用法：/bind_multi 账号 或 /bind_multi 邀请码")
	}
	if err := s.ensureTelegramNotCrossBound(ctx, telegramUserID, TelegramChannelWorkstation); err != nil {
		return nil, err
	}
	empID, err := s.resolveEmployeeForMultiBind(ctx, inviteCode, username)
	if err != nil {
		return nil, err
	}
	deviceIDs, err := s.listSharedWorkstationDevicesForEmployee(ctx, empID)
	if err != nil {
		return nil, err
	}
	if len(deviceIDs) == 0 {
		return nil, ErrWorkstationNotConfigured
	}
	bound := make([]DeviceTelegramBindingRow, 0, len(deviceIDs))
	for _, deviceID := range deviceIDs {
		row, bindErr := s.bindWorkstationTelegramToDevice(ctx, telegramUserID, telegramUsername, deviceID, empID, chatID)
		if bindErr != nil {
			// Same as old per-device bind: skip PCs already taken by another TG; keep going for the rest.
			if errors.Is(bindErr, ErrDeviceTelegramAlreadyBound) {
				continue
			}
			return nil, bindErr
		}
		bound = append(bound, *row)
	}
	if len(bound) == 0 {
		return nil, ErrDeviceTelegramAlreadyBound
	}
	return bound, nil
}
func (s *WorkforceTelegramService) resolveEmployeeForMultiBind(ctx context.Context, inviteCode, username string) (uuid.UUID, error) {
	switch {
	case inviteCode != "":
		invite, err := s.workforce.ResolveInviteCodeEmployee(ctx, inviteCode)
		if err != nil {
			return uuid.Nil, err
		}
		if username != "" {
			if err := s.workforce.ValidateEmployeeUsername(ctx, username, invite.EmployeeID); err != nil {
				return uuid.Nil, err
			}
		}
		return invite.EmployeeID, nil
	default:
		var userID uuid.UUID
		var status int
		err := s.db.QueryRowContext(ctx, `
			SELECT u.id, u.status FROM users u WHERE u.username = $1`, username).Scan(&userID, &status)
		if err == sql.ErrNoRows {
			return uuid.Nil, ErrInvalidCredentials
		}
		if err != nil {
			return uuid.Nil, err
		}
		if status != 1 {
			return uuid.Nil, ErrInvalidCredentials
		}
		empID, err := s.workforce.ResolveEmployeeByUserID(ctx, userID)
		if err != nil {
			if errors.Is(err, ErrEmployeeNotFound) {
				return uuid.Nil, ErrNotEmployeeRole
			}
			return uuid.Nil, err
		}
		return empID, nil
	}
}
func (s *WorkforceTelegramService) listSharedWorkstationDevicesForEmployee(ctx context.Context, empID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.device_id FROM wf_shared_workstation_members m
		JOIN devices d ON d.id = m.device_id AND LOWER(d.status) = 'approved'
		WHERE m.employee_id = $1
		ORDER BY d.last_seen_at DESC NULLS LAST`, empID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[uuid.UUID]struct{}{}
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	fallbackRows, err := s.db.QueryContext(ctx, `
		SELECT id FROM devices
		WHERE invited_employee_uuid = $1 AND COALESCE(is_shared_workstation, FALSE) = TRUE
			AND LOWER(status) = 'approved'
		ORDER BY last_seen_at DESC NULLS LAST`, empID)
	if err != nil {
		return nil, err
	}
	defer fallbackRows.Close()
	for fallbackRows.Next() {
		var id uuid.UUID
		if err := fallbackRows.Scan(&id); err != nil {
			return nil, err
		}
		if id == uuid.Nil {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, fallbackRows.Err()
}

// bindWorkstationTelegramToDevice binds TG to one shared-workstation device (multi-device TG allowed).
func (s *WorkforceTelegramService) bindWorkstationTelegramToDevice(
	ctx context.Context,
	telegramUserID int64,
	telegramUsername string,
	deviceID, employeeID uuid.UUID,
	chatID string,
) (*DeviceTelegramBindingRow, error) {
	if deviceID == uuid.Nil {
		return nil, fmt.Errorf("invalid device id")
	}
	ok, err := s.deviceIsSharedWorkstation(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("device not found")
		}
		return nil, err
	}
	if !ok {
		return nil, ErrWorkstationNotConfigured
	}
	member, err := s.employeeIsSharedWorkstationMember(ctx, deviceID, employeeID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrWorkstationInviteMismatch
	}
	var otherTG int64
	err = s.db.QueryRowContext(ctx, `
		SELECT telegram_user_id FROM device_telegram_bindings
		WHERE device_id = $1 AND status = 1`, deviceID).Scan(&otherTG)
	if err == nil && otherTG > 0 && otherTG != telegramUserID {
		return nil, ErrDeviceTelegramAlreadyBound
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var id uuid.UUID
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO device_telegram_bindings (device_id, telegram_user_id, telegram_username, chat_id, status)
		VALUES ($1, $2, $3, $4, 1)
		ON CONFLICT (device_id) DO UPDATE SET
			telegram_user_id = EXCLUDED.telegram_user_id,
			telegram_username = EXCLUDED.telegram_username,
			chat_id = CASE
				WHEN COALESCE(NULLIF(TRIM(EXCLUDED.chat_id), ''), '') <> '' THEN EXCLUDED.chat_id
				ELSE device_telegram_bindings.chat_id
			END,
			status = 1,
			bound_at = NOW()
		RETURNING id`, deviceID, telegramUserID, nullString(strings.TrimSpace(telegramUsername)), nullString(strings.TrimSpace(chatID))).Scan(&id)
	if err != nil {
		return nil, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE devices SET is_shared_workstation = TRUE WHERE id = $1`, deviceID)
	items, err := s.ListDeviceTelegramBindings(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.ID == id {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("workstation binding not found after create")
}
// BindTelegramByToken consumes a deep-link bind token.
func (s *WorkforceTelegramService) BindTelegramByToken(ctx context.Context, telegramUserID int64, telegramUsername, token, chatID string) (*TelegramBindingRow, error) {
	token = strings.TrimSpace(strings.TrimPrefix(token, "bind_"))
	if token == "" || telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid bind token")
	}
	if err := s.ensureTelegramNotCrossBound(ctx, telegramUserID, TelegramChannelPersonal); err != nil {
		return nil, err
	}
	var employeeID uuid.UUID
	var expiresAt time.Time
	var usedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT employee_id, expires_at, used_at FROM wf_tg_bind_tokens
		WHERE token = $1`, token).Scan(&employeeID, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("绑定链接无效或已过期")
	}
	if err != nil {
		return nil, err
	}
	if usedAt.Valid {
		return nil, fmt.Errorf("绑定链接已使用")
	}
	if time.Now().After(expiresAt) {
		return nil, fmt.Errorf("绑定链接已过期，请在客户端重新获取")
	}
	if boundEmp, err := s.telegramUserBoundEmployee(ctx, telegramUserID); err != nil {
		return nil, err
	} else if boundEmp != uuid.Nil && boundEmp != employeeID {
		return nil, ErrTelegramAlreadyBound
	}
	if otherTG, err := s.employeeTelegramBound(ctx, employeeID); err != nil {
		return nil, err
	} else if otherTG > 0 && otherTG != telegramUserID {
		return nil, fmt.Errorf("该员工已绑定其他 Telegram 账号")
	}
	row, err := s.CreateBinding(ctx, employeeID, telegramUserID, telegramUsername, chatID)
	if err != nil {
		return nil, err
	}
	res, execErr := s.db.ExecContext(ctx, `
		UPDATE wf_tg_bind_tokens SET used_at = NOW(), used_by_telegram_user_id = $2
		WHERE token = $1 AND used_at IS NULL`, token, telegramUserID)
	if execErr != nil {
		return row, execErr
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errors.New("bind token already used")
	}
	return row, nil
}

// BindTelegramSelf accepts invite code (+ credentials) or bind token from worker API.
func (s *WorkforceTelegramService) BindTelegramSelf(ctx context.Context, telegramUserID int64, telegramUsername, inviteCode, bindToken, username, password, chatID string) (*TelegramBindingRow, error) {
	inviteCode = strings.TrimSpace(inviteCode)
	bindToken = strings.TrimSpace(bindToken)
	username = strings.TrimSpace(username)
	switch {
	case bindToken != "":
		return s.BindTelegramByToken(ctx, telegramUserID, telegramUsername, bindToken, chatID)
	case inviteCode != "":
		return s.BindTelegramByInviteAndCredentials(ctx, telegramUserID, telegramUsername, inviteCode, username, password, chatID)
	case username != "" && password != "":
		return s.bindStandaloneAccount(ctx, telegramUserID, telegramUsername, username, password, chatID)
	case username != "":
		return s.BindTelegramByUsername(ctx, telegramUserID, telegramUsername, username, chatID)
	default:
		return nil, fmt.Errorf("invite code, account, or bind token required")
	}
}

// ---- Source: backend/services/workforce_tg_bot_ops.go ----
const defaultDailyResetExecutionOffsetMinutes = 120

func (s *WorkforceTelegramService) dailyResetExecutionOffset(settings *TelegramSettings) time.Duration {
	if settings == nil {
		return time.Duration(defaultDailyResetExecutionOffsetMinutes) * time.Minute
	}
	return time.Duration(max(0, settings.DailyResetExecutionOffsetMinutes)) * time.Minute
}
func (s *WorkforceTelegramService) dailyResetExecutionTimeForChat(ctx context.Context, settings *TelegramSettings, chatID string, now time.Time) time.Time {
	resetTime := settings.DailyResetTime
	if chatID != "" {
		if t := s.GetGroupDailyResetTime(ctx, chatID); t != "" {
			resetTime = t
		}
	}
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	resetH, resetM, ok := parseClock(resetTime)
	if !ok {
		return now
	}
	execution := time.Date(now.Year(), now.Month(), now.Day(), resetH, resetM, 0, 0, loc).Add(s.dailyResetExecutionOffset(settings))
	// Normalize an offset crossing midnight to today's execution clock.
	return time.Date(now.Year(), now.Month(), now.Day(), execution.Hour(), execution.Minute(), 0, 0, loc)
}

// scheduledResetExportDate 自动日重置的日历锚点（按 DailyResetTime 区分「昨日」）。
func (s *WorkforceTelegramService) scheduledResetExportDate(settings *TelegramSettings, now time.Time) string {
	return s.resetExportDate(settings, now)
}

// scheduledResetExportDateForChat 将日历锚点映射为该群业务日（DetermineCurrentPeriod）。
func (s *WorkforceTelegramService) scheduledResetExportDateForChat(ctx context.Context, chatID string, settings *TelegramSettings, now time.Time) string {
	chatID = strings.TrimSpace(chatID)
	// Use the group's reset boundary, not the global clock or delayed execution date.
	effective := *settings
	if chatID != "" {
		if clock := s.GetGroupDailyResetTime(ctx, chatID); clock != "" {
			effective.DailyResetTime = clock
		}
	}
	boundary := s.dailyResetExecutionTimeForChat(ctx, &effective, chatID, now).Add(-s.dailyResetExecutionOffset(&effective))
	calDate := s.resetExportDate(&effective, boundary)
	if chatID == "" {
		return calDate
	}
	loc := tgLoadLocation(settings.Timezone)
	parsed, err := time.ParseInLocation("2006-01-02", calDate, loc)
	if err != nil {
		return calDate
	}
	anchor := parsed.Add(12 * time.Hour)
	if period, err := s.DetermineCurrentPeriod(ctx, chatID, anchor); err == nil && period != nil {
		if bd := strings.TrimSpace(period.BusinessDate); bd != "" {
			return bd
		}
	}
	return calDate
}
func (s *WorkforceTelegramService) resetExportDateForChat(ctx context.Context, settings *TelegramSettings, chatID string, now time.Time) string {
	return s.scheduledResetExportDateForChat(ctx, chatID, settings, now)
}
func (s *WorkforceTelegramService) shouldRunScheduledDailyReset(ctx context.Context, settings *TelegramSettings, chatID string, now time.Time) bool {
	execAt := s.dailyResetExecutionTimeForChat(ctx, settings, chatID, now)
	if now.Before(execAt) {
		return false
	}
	resetDate := now.Format("2006-01-02")
	return !s.groupResetAlreadyDone(ctx, chatID, resetDate)
}

// resetExportDateSpan 自动日重置仅导出单日（业务日）；手动重置仍可使用较宽区间。
func (s *WorkforceTelegramService) resetExportDateSpan(ctx context.Context, settings *TelegramSettings, chatID, exportDate string, now time.Time, runOpts *DailyResetRunOptions) (dateFrom, dateTo string) {
	exportDate = strings.TrimSpace(exportDate)
	if exportDate == "" {
		exportDate = s.resetExportDateForChat(ctx, settings, chatID, now)
	}
	if runOpts != nil && runOpts.ManualTrigger {
		loc := tgLoadLocation(settings.Timezone)
		calendarYesterday := now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		dateFrom, dateTo = exportDate, exportDate
		if calendarYesterday < dateFrom {
			dateFrom = calendarYesterday
		} else if calendarYesterday > dateTo {
			dateTo = calendarYesterday
		}
		return dateFrom, dateTo
	}
	return exportDate, exportDate
}
func (s *WorkforceTelegramService) IsAdminTelegramUser(ctx context.Context, telegramUserID int64) bool {
	for _, id := range s.listAdminTelegramUserIDs(ctx) {
		if id == telegramUserID {
			return true
		}
	}
	return false
}
func (s *WorkforceTelegramService) groupResetAlreadyDone(ctx context.Context, chatID, resetDate string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM wf_tg_group_reset_log WHERE chat_id = $1 AND reset_date = $2::date`, chatID, resetDate).Scan(&n)
	return n > 0
}
func (s *WorkforceTelegramService) MaybeRunDailyReset(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.DailyResetEnabled {
		return
	}
	groups, err := s.ListGroups(ctx)
	if err != nil {
		return
	}
	loc := tgLoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	resetDate := now.Format("2006-01-02")
	for _, g := range groups {
		if !g.Enabled || strings.TrimSpace(g.ChatID) == "" {
			continue
		}
		if !s.shouldRunScheduledDailyReset(ctx, settings, g.ChatID, now) {
			continue
		}
		if _, err := s.runDailyResetForGroup(ctx, g.ChatID, resetDate, nil); err != nil {
			log.Printf("[Telegram] group reset %s: %v", g.ChatID, err)
		}
	}
}

// MaybeLazyResetForChat 已废弃：日重置仅由定时任务/补偿任务触发，不在用户点击 TG 时执行。
func (s *WorkforceTelegramService) MaybeLazyResetForChat(ctx context.Context, chatID string) {
	_ = ctx
	_ = chatID
}
func (s *WorkforceTelegramService) RunDailyReset(ctx context.Context, runOpts *DailyResetRunOptions) (*TgDailyResetResult, error) {
	if !TelegramAttendanceEnabled() {
		return nil, ErrTelegramDisabled
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.DailyResetEnabled {
		return nil, nil
	}
	groups, err := s.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	loc := tgLoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	resetDate := now.Format("2006-01-02")
	total := &TgDailyResetResult{ResetDate: resetDate}
	processed := 0
	for _, g := range groups {
		if !g.Enabled || strings.TrimSpace(g.ChatID) == "" {
			continue
		}
		if s.groupResetAlreadyDone(ctx, g.ChatID, resetDate) {
			continue
		}
		r, err := s.runDailyResetForGroup(ctx, g.ChatID, resetDate, runOpts)
		if err != nil {
			return total, err
		}
		if r != nil {
			processed++
			total.ClosedWorkSessions += r.ClosedWorkSessions
			total.ClosedAgentSessions += r.ClosedAgentSessions
			total.ClosedActivities += r.ClosedActivities
			if r.ExportDate != "" {
				total.ExportDate = r.ExportDate
			}
			if r.ExportDateNote != "" {
				total.ExportDateNote = r.ExportDateNote
			}
			if len(r.BindingWarnings) > 0 {
				total.BindingWarnings = append(total.BindingWarnings, r.BindingWarnings...)
			}
			if r.ExportPath != "" {
				total.ExportPath = r.ExportPath
			}
			if r.ArchivedMonth != "" {
				total.ArchivedMonth = r.ArchivedMonth
			}
		}
	}
	total.GroupsProcessed = processed
	if processed == 0 {
		total.SkippedAllGroups = true
	}
	var exists int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wf_tg_daily_reset_log WHERE reset_date = $1::date`, resetDate).Scan(&exists)
	if exists == 0 && (total.ClosedWorkSessions > 0 || total.ClosedActivities > 0) {
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO wf_tg_daily_reset_log (reset_date, closed_work_sessions, closed_activities)
			VALUES ($1::date, $2, $3) ON CONFLICT (reset_date) DO NOTHING`,
			resetDate, total.ClosedWorkSessions, total.ClosedActivities)
	}
	return total, nil
}
func (s *WorkforceTelegramService) runDailyResetForGroup(ctx context.Context, chatID, resetDate string, runOpts *DailyResetRunOptions) (*TgDailyResetResult, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	loc := tgLoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	exportDate, exportNote := s.resolveGroupResetExportDate(ctx, settings, chatID, now, runOpts)
	result := &TgDailyResetResult{
		ResetDate:      resetDate,
		ExportDate:     exportDate,
		ExportDateNote: exportNote,
	}

	log.Printf("[Telegram] group reset begin chat=%s reset_date=%s attendance_date=%s", chatID, resetDate, exportDate)
	bindingWarnings := s.collectGroupBindingWarnings(ctx, chatID, exportDate)
	if len(bindingWarnings) > 0 {
		result.BindingWarnings = bindingWarnings
		for _, w := range bindingWarnings {
			log.Printf("[Telegram] group reset binding warning chat=%s: %s", chatID, w)
		}
	}

	completed, err := s.completeMissingWorkEndsForGroup(ctx, chatID, exportDate, runOpts)
	if err != nil {
		return result, fmt.Errorf("complete work ends failed: %w", err)
	}
	result.ClosedWorkSessions += completed

	agentCompleted, err := s.completeMissingAgentWorkEndsForGroup(ctx, chatID, exportDate, runOpts)
	if err != nil {
		return result, fmt.Errorf("complete agent work ends failed: %w", err)
	}
	result.ClosedAgentSessions += agentCompleted

	closedAct, err := s.forceCloseOpenActivitiesForGroup(ctx, chatID, exportDate)
	if err != nil {
		return result, fmt.Errorf("close activities failed: %w", err)
	}
	result.ClosedActivities += closedAct

	if backfilled, err := s.backfillAttendanceCheckoutsForGroup(ctx, chatID, exportDate); err != nil {
		log.Printf("[Telegram] backfill attendance checkouts chat=%s date=%s: %v", chatID, exportDate, err)
	} else if backfilled > 0 {
		log.Printf("[Telegram] backfilled %d attendance checkout(s) chat=%s date=%s", backfilled, chatID, exportDate)
	}

	if attCompleted, err := s.completeMissingAttendanceRecordCheckoutsForGroup(ctx, chatID, exportDate, runOpts); err != nil {
		return result, fmt.Errorf("complete attendance checkouts failed: %w", err)
	} else if attCompleted > 0 {
		log.Printf("[Telegram] completed %d attendance checkout(s) chat=%s date=%s", attCompleted, chatID, exportDate)
	}

	if parsed, err := time.Parse("2006-01-02", exportDate); err == nil {
		lastOfMonth := parsed.AddDate(0, 1, -parsed.Day()).Day()
		if parsed.Day() == lastOfMonth {
			month := parsed.Format("2006-01")
			if err := s.archiveMonthlyStats(ctx, month); err == nil {
				result.ArchivedMonth = month
			}
		}
	}

	exportDateFrom, exportDateTo := s.resetExportDateSpan(ctx, settings, chatID, exportDate, now, runOpts)
	exportPath, exportErr := s.exportBeforeReset(ctx, exportDateFrom, exportDateTo, chatID)
	if exportErr != nil {
		return result, fmt.Errorf("reset export failed: %w", exportErr)
	}
	result.ExportPath = exportPath
	if exportPath != "" {
		defer os.Remove(exportPath)
	}

	spanNote := ""
	if exportDateFrom != exportDateTo {
		spanNote = fmt.Sprintf("（导出区间 %s ~ %s）", exportDateFrom, exportDateTo)
	}
	caption := fmt.Sprintf(
		"📊 日重置数据导出\n📅 统计日期：%s%s\n📝 %s\n💬 群组：%s",
		exportDate, spanNote, exportNote, chatID,
	)
	if len(bindingWarnings) > 0 {
		caption += "\n⚠️ 绑定告警：\n• " + strings.Join(bindingWarnings, "\n• ")
	}
	if exportPath != "" {
		if data, err := os.ReadFile(exportPath); err == nil {
			s.PushDocumentToChatTargets(ctx, chatID, filepath.Base(exportPath), data, caption, false)
		}
	} else if data, name, err := s.ExportAttendanceXLSX(ctx, exportDateFrom, exportDateTo, chatID); err == nil {
		s.PushDocumentToChatTargets(ctx, chatID, name, data, caption, false)
	}

	for d := exportDateFrom; ; {
		if err := s.purgeDailyDetailForGroup(ctx, chatID, d); err != nil {
			log.Printf("[Telegram] purge daily detail chat=%s date=%s: %v", chatID, d, err)
		}
		if d == exportDateTo {
			break
		}
		parsed, err := time.Parse("2006-01-02", d)
		if err != nil {
			break
		}
		d = parsed.AddDate(0, 0, 1).Format("2006-01-02")
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_group_reset_log (chat_id, reset_date, closed_work_sessions, closed_activities)
		VALUES ($1, $2::date, $3, $4)
		ON CONFLICT (chat_id, reset_date) DO UPDATE SET
			closed_work_sessions = EXCLUDED.closed_work_sessions,
			closed_activities = EXCLUDED.closed_activities,
			run_at = NOW()`,
		chatID, resetDate, result.ClosedWorkSessions, result.ClosedActivities)
	if err != nil {
		return result, err
	}
	return result, nil
}
func (s *WorkforceTelegramService) FormatActivityOccupancy(ctx context.Context, chatID string) string {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.name, t.max_concurrent,
			(SELECT COUNT(*) FROM wf_tg_activity_sessions s
			 WHERE s.status = 'open' AND s.activity_code = t.code
			   AND ($1 = '' OR COALESCE(s.source_chat_id,'') = $1))
		FROM wf_tg_activity_types t WHERE t.enabled = TRUE ORDER BY t.sort_order, t.name`, chatID)
	if err != nil {
		return "查询失败"
	}
	defer rows.Close()
	var b strings.Builder
	b.WriteString("📊 活动实时占用\n")
	has := false
	for rows.Next() {
		has = true
		var name string
		var maxC, open int
		if rows.Scan(&name, &maxC, &open) != nil {
			continue
		}
		limit := "不限"
		if maxC > 0 {
			limit = fmt.Sprintf("%d/%d", open, maxC)
		} else {
			limit = fmt.Sprintf("%d", open)
		}
		b.WriteString(fmt.Sprintf("• %s：%s\n", name, limit))
	}
	if !has {
		b.WriteString("暂无活动配置")
	}
	return strings.TrimSpace(b.String())
}
func (s *WorkforceTelegramService) BotExportAndPush(ctx context.Context, chatID, dateFrom, dateTo string) (string, error) {
	if strings.TrimSpace(dateFrom) == "" {
		dateFrom = time.Now().Format("2006-01-02")
	}
	if strings.TrimSpace(dateTo) == "" {
		dateTo = dateFrom
	}
	data, filename, err := s.ExportAttendanceXLSX(ctx, dateFrom, dateTo, chatID)
	if err != nil {
		return "", err
	}
	caption := fmt.Sprintf("📤 考勤导出 %s ~ %s", dateFrom, dateTo)
	s.PushDocumentToChatTargets(ctx, chatID, filename, data, caption, false)
	return caption, nil
}
func (s *WorkforceTelegramService) FormatMonthlyReport(ctx context.Context, chatID, yearMonth string) (string, error) {
	if strings.TrimSpace(yearMonth) == "" {
		now := time.Now()
		now = now.AddDate(0, -1, 0)
		yearMonth = now.Format("2006-01")
	}
	t, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return "", err
	}
	dateFrom := t.Format("2006-01-02")
	dateTo := t.AddDate(0, 1, -1).Format("2006-01-02")

	var fineTotal float64
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(fine_amount),0) FROM wf_tg_activity_sessions
		WHERE attendance_date BETWEEN $1::date AND $2::date
		  AND ($3 = '' OR COALESCE(source_chat_id,'') = $3)`, dateFrom, dateTo, chatID).Scan(&fineTotal)

	var topName string
	var topSec int
	_ = s.db.QueryRowContext(ctx, `
		SELECT e.name, COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ws.checkout_time, NOW()) - ws.checkin_time))),0)::int
		FROM work_sessions ws JOIN employees e ON e.id = ws.employee_id
		LEFT JOIN employee_telegram_bindings b ON b.employee_id = ws.employee_id AND b.status = 1
		WHERE ws.source = 'telegram' AND ws.attendance_date BETWEEN $1::date AND $2::date
		  AND ($3 = '' OR b.chat_id = $3)
		GROUP BY e.name ORDER BY 2 DESC LIMIT 1`, dateFrom, dateTo, chatID).Scan(&topName, &topSec)

	text := fmt.Sprintf("📅 <b>月报 %s</b>\n\n", yearMonth)
	text += fmt.Sprintf("💰 活动罚款合计：%s\n", FormatFineMoney(fineTotal, s.FineCurrencyCode(ctx)))
	if topName != "" {
		text += fmt.Sprintf("⏱ 工时最长：%s（%s）\n", topName, formatDurationZh(topSec))
	}
	text += "\n完整数据请使用 /exportmonthly 或后台导出 XLSX。"
	return text, nil
}
func (s *WorkforceTelegramService) FormatShowSettings(ctx context.Context, chatID string) string {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return "读取配置失败"
	}
	mode := s.GetLangMode(ctx, chatID)
	grace, _ := s.GetGraceConfig(ctx, chatID)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("⚙️ <b>群组配置</b> %s\n\n", chatID))
	b.WriteString(fmt.Sprintf("• 界面语言：%s\n", mode))
	b.WriteString(fmt.Sprintf("• 双班：%v | 三班：%v | 班次窗口禁用：%v\n", settings.DualShiftEnabled, settings.TripleShiftEnabled, settings.ShiftWindowDisabled))
	b.WriteString(fmt.Sprintf("• 白班 %s-%s | 中班 %s-%s | 夜班 %s-%s\n",
		settings.DayStart, settings.DayEnd, settings.MiddleStart, settings.MiddleEnd, settings.NightStart, settings.NightEnd))
	b.WriteString(fmt.Sprintf("• 上班打卡窗口 前%d/后%d 分钟\n", grace.CheckInBefore, grace.CheckInAfter))
	b.WriteString(fmt.Sprintf("• 下班打卡窗口 前%d/后%d 分钟\n", grace.CheckOutBefore, grace.CheckOutAfter))
	b.WriteString(fmt.Sprintf("• 迟到/早退宽限 迟到%d 早退%d 分钟\n", grace.LateTolerance, grace.EarlyLeaveTolerance))
	b.WriteString(fmt.Sprintf("• 日重置：%s +%dm（群覆盖：%s）\n", settings.DailyResetTime, settings.DailyResetExecutionOffsetMinutes, s.GetGroupDailyResetTime(ctx, chatID)))
	ps, _ := s.GetPushSettings(ctx, chatID)
	if ps != nil {
		b.WriteString(fmt.Sprintf("• 推送 频道:%s 通知群:%s 额外上班群:%s\n", ps.ChannelID, ps.NotificationGroupID, ps.ExtraWorkGroupID))
		b.WriteString(fmt.Sprintf("• 推送开关 ch=%v gr=%v admin=%v\n", ps.EnableChannelPush, ps.EnableGroupPush, ps.EnableAdminPush))
	}
	return b.String()
}
func (s *WorkforceTelegramService) FixActivityMessageIDs(ctx context.Context) int {
	res, err := s.db.ExecContext(ctx, `UPDATE wf_tg_activity_sessions SET telegram_message_id = NULL WHERE telegram_message_id IS NOT NULL`)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}
func (s *WorkforceTelegramService) FormatCheckDB(ctx context.Context) string {
	var settingsOK, groups, bindings, openAct, openWork int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wf_telegram_settings WHERE enabled = TRUE`).Scan(&settingsOK)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wf_telegram_groups WHERE enabled = TRUE`).Scan(&groups)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM employee_telegram_bindings WHERE status = 1`).Scan(&bindings)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wf_tg_activity_sessions WHERE status = 'open'`).Scan(&openAct)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_sessions WHERE status = 'open' AND source = 'telegram'`).Scan(&openWork)
	return fmt.Sprintf("🗄 DB 状态\n• 启用配置:%d 群组:%d 绑定:%d\n• 进行中活动:%d 上班中:%d",
		settingsOK, groups, bindings, openAct, openWork)
}
func (s *WorkforceTelegramService) BuildActivityBotCommands(ctx context.Context) []map[string]string {
	types, _ := s.ListEnabledActivityTypesForWorker(ctx)
	return buildTelegramBotCommandMenu(types)
}
func tgBotCmd(command, description string) map[string]string {
	return map[string]string{"command": command, "description": description}
}
func telegramBotCommandReserved() map[string]bool {
	return map[string]bool{
		"start": true, "lang": true, "language": true, "help": true,
		"bind": true, "bind_multi": true, "bind_workstation": true,
		"checkin": true, "checkout": true, "at": true,
		"checkin_day": true, "checkin_middle": true, "checkin_night": true,
		"lang_zh": true, "lang_vi": true, "lang_en": true,
		"status": true, "myinfo": true, "rankings": true,
		"export": true, "exportmonthly": true, "monthlyreport": true,
		"actstatus": true, "chatid": true, "ci": true,
	}
}
func buildTelegramBotCommandMenu(types []TgActivityType) []map[string]string {
	reserved := telegramBotCommandReserved()
	activityBySlug := make(map[string]string, len(types))
	for _, t := range types {
		slug := activityCommandSlug(t.Name)
		if slug == "" || reserved[slug] || activityBySlug[slug] != "" || !isTelegramBotCommandName(slug) {
			continue
		}
		activityBySlug[slug] = t.Name
	}

	out := []map[string]string{
		tgBotCmd("checkin_day", "白班上班 / Day shift"),
		tgBotCmd("checkin_middle", "中班上班 / Middle shift"),
		tgBotCmd("checkin_night", "夜班上班 / Night shift"),
		tgBotCmd("checkout", "下班 / Check out"),
	}
	pinned := []string{"wc1", "wc2", "smoke"}
	pinnedSeen := map[string]bool{}
	for _, slug := range pinned {
		name, ok := activityBySlug[slug]
		if !ok {
			continue
		}
		out = append(out, tgBotCmd(slug, name))
		pinnedSeen[slug] = true
	}
	out = append(out,
		tgBotCmd("at", "回座 / Back"),
		tgBotCmd("lang_zh", "设为中文 / Set Chinese"),
		tgBotCmd("lang_vi", "设为越南语 / Set Vietnamese"),
		tgBotCmd("lang_en", "设为英语 / Set English"),
		tgBotCmd("myinfo", "我的记录 / My record"),
		tgBotCmd("rankings", "排行榜 / Rankings"),
		tgBotCmd("bind", "绑定员工 / Bind"),
		tgBotCmd("help", "帮助 / Help"),
		tgBotCmd("start", "开始 / Start"),
	)
	for _, t := range types {
		if len(out) >= 100 {
			return out[:100]
		}
		slug := activityCommandSlug(t.Name)
		if slug == "" || reserved[slug] || pinnedSeen[slug] || !isTelegramBotCommandName(slug) {
			continue
		}
		reserved[slug] = true
		out = append(out, tgBotCmd(slug, t.Name))
	}
	for _, c := range []struct{ cmd, desc string }{
		{"status", "状态 / Status"},
		{"export", "导出今日 / Export today"},
		{"exportmonthly", "导出上月 / Export month"},
		{"monthlyreport", "月报摘要 / Monthly report"},
		{"actstatus", "活动占用 / Activity status"},
		{"chatid", "群组/频道 ID / Chat ID"},
		{"ci", "快捷开始活动 / Quick activity"},
		{"bind_multi", "多设备绑定 / Bind multi"},
		{"bind_workstation", "工位绑定 / Bind workstation"},
	} {
		if len(out) >= 100 {
			return out[:100]
		}
		out = append(out, tgBotCmd(c.cmd, c.desc))
	}
	if len(out) > 100 {
		return out[:100]
	}
	return out
}
func isTelegramBotCommandName(cmd string) bool {
	if cmd == "" || len(cmd) > 32 {
		return false
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '_' {
			continue
		}
		return false
	}
	return true
}
func activityCommandSlug(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	aliases := map[string]string{
		"小厕": "wc1", "大厕": "wc2", "吃饭": "meal", "抽烟或休息": "smoke", "抽烟": "smoke",
	}
	if slug, ok := aliases[name]; ok {
		return slug
	}
	r := strings.NewReplacer(" ", "", "或", "")
	s := r.Replace(name)
	if len([]rune(s)) > 32 {
		s = string([]rune(s)[:32])
	}
	return s
}
func (s *WorkforceTelegramService) ResolveActivityFromCI(ctx context.Context, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	types, _ := s.ListEnabledActivityTypesForWorker(ctx)
	lookup := s.BuildButtonLookup(ctx, activityNamesFromTypes(types))
	if act := lookup[text]; act != "" {
		for _, t := range types {
			if t.Name == act {
				return t.Name
			}
		}
	}
	lower := strings.ToLower(text)
	for _, t := range types {
		if strings.ToLower(t.Name) == lower || activityCommandSlug(t.Name) == lower {
			return t.Name
		}
	}
	return ""
}
func activityNamesFromTypes(types []TgActivityType) []string {
	names := make([]string, 0, len(types))
	for _, t := range types {
		if strings.TrimSpace(t.Name) != "" {
			names = append(names, t.Name)
		}
	}
	return names
}

// ---- Source: backend/services/workforce_tg_bot_runner.go ----
func envTelegramBotEmbedded() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("TELEGRAM_BOT_EMBEDDED")))
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return true
}

// TelegramBotEmbedded reports whether the bot runs inside the backend process.
func TelegramBotEmbedded() bool {
	return envTelegramBotEmbedded()
}

func (s *WorkforceTelegramService) BuildTelegramBotOptionsContext(parent context.Context, listenPort string) (tgbot.Options, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	// Same settings row every update needs; served from the short-lived cache
	// (saving settings clears it) instead of one round trip per queued update.
	settings, err := s.loadTelegramSettingsCached(ctx)
	if err != nil {
		return tgbot.Options{}, err
	}
	secret, encrypted, enabled := settings.workerSecret, settings.botTokenEnc, settings.Enabled
	mode, webhookURL, webhookSecret := settings.BotMode, settings.WebhookURL, settings.webhookSecret
	if !enabled || strings.TrimSpace(encrypted) == "" {
		return tgbot.Options{}, errors.New("bot token not configured")
	}
	token, err := DecryptSecret(encrypted)
	if err != nil {
		return tgbot.Options{}, err
	}
	if strings.TrimSpace(secret) == "" {
		secret, err = s.EnsureWorkerSecret(ctx)
		if err != nil {
			return tgbot.Options{}, err
		}
	}
	return tgbot.Options{BackendURL: fmt.Sprintf("http://127.0.0.1:%s/api/v1", listenPort),
		WorkerSecret: strings.TrimSpace(secret), BotToken: token, BotMode: mode, WebhookURL: webhookURL,
		WebhookSecret: strings.TrimSpace(webhookSecret), Embedded: true}, nil
}

// ---- Source: backend/services/workforce_tg_device_tag.go ----
// TgDeviceTagSep separates action label and short device tag on reply-keyboard buttons.
// Example: "🚾 小厕 · A"
const TgDeviceTagSep = " · "

type workstationDeviceTag struct {
	Device workstationTGDevice
	Tag    string
}
type TelegramPunchDeviceOption struct {
	DeviceID     uuid.UUID `json:"deviceId"`
	DeviceName   string    `json:"deviceName"`
	EmployeeID   uuid.UUID `json:"employeeId"`
	EmployeeName string    `json:"employeeName"`
}

// ListTelegramPunchDeviceOptions returns bound workstations that currently have
// an employee logged in. The bot uses this to ask for a device only when the
// action would otherwise be ambiguous.
func (s *WorkforceTelegramService) ListTelegramPunchDeviceOptions(
	ctx context.Context, telegramUserID int64,
) ([]TelegramPunchDeviceOption, error) {
	devices, err := s.listWorkstationDevicesForTelegram(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	out := make([]TelegramPunchDeviceOption, 0, len(devices))
	for _, device := range devices {
		empID, loginErr := s.resolveWorkstationLoggedInEmployee(ctx, device.ID)
		if loginErr == ErrPCLoginRequired {
			continue
		}
		if loginErr != nil {
			return nil, loginErr
		}
		var employeeName string
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(name, '') FROM employees WHERE id = $1`, empID,
		).Scan(&employeeName); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		out = append(out, TelegramPunchDeviceOption{
			DeviceID: device.ID, DeviceName: device.Name,
			EmployeeID: empID, EmployeeName: strings.TrimSpace(employeeName),
		})
	}
	return out, nil
}

// SplitTgDeviceTag splits "动作 · 标记" into base label and device hint.
func SplitTgDeviceTag(text string) (base, deviceHint string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	idx := strings.LastIndex(text, TgDeviceTagSep)
	if idx <= 0 {
		return text, ""
	}
	base = strings.TrimSpace(text[:idx])
	deviceHint = strings.TrimSpace(text[idx+len(TgDeviceTagSep):])
	if base == "" || deviceHint == "" {
		return text, ""
	}
	return base, deviceHint
}
func JoinTgDeviceTag(label, tag string) string {
	label = strings.TrimSpace(label)
	tag = strings.TrimSpace(tag)
	if label == "" {
		return tag
	}
	if tag == "" {
		return label
	}
	return label + TgDeviceTagSep + tag
}

// buildWorkstationDeviceTags assigns stable short tags A, B, C… (then A1, B1…).
// Order is by device UUID so the same PC keeps the same letter across refreshes.
func buildWorkstationDeviceTags(devices []workstationTGDevice) []workstationDeviceTag {
	sorted := append([]workstationTGDevice(nil), devices...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID.String() < sorted[j].ID.String()
	})
	out := make([]workstationDeviceTag, 0, len(sorted))
	for i, d := range sorted {
		out = append(out, workstationDeviceTag{
			Device: d,
			Tag:    shortDeviceLetterTag(i),
		})
	}
	return out
}
func shortDeviceLetterTag(index int) string {
	if index < 0 {
		index = 0
	}
	if index < 26 {
		return string(rune('A' + index))
	}
	// 27th+ → A1, B1, … then A2…
	cycle := index / 26
	letter := index % 26
	return fmt.Sprintf("%c%d", 'A'+letter, cycle)
}

// FormatWorkstationDeviceTagLegend explains short tags, e.g. "A=DESKTOP-1  B=DESKTOP-2".
func FormatWorkstationDeviceTagLegend(tags []workstationDeviceTag) string {
	if len(tags) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tags))
	for _, t := range tags {
		name := strings.TrimSpace(t.Device.Name)
		if name == "" {
			name = t.Device.ID.String()
			if len(name) > 8 {
				name = name[:8]
			}
		}
		parts = append(parts, t.Tag+"="+name)
	}
	return "设备标记：" + strings.Join(parts, "  ")
}

func matchWorkstationDeviceByHint(devices []workstationTGDevice, hint string) (workstationTGDevice, error) {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return workstationTGDevice{}, ErrWorkstationDeviceRequired
	}
	tags := buildWorkstationDeviceTags(devices)
	hintLower := strings.ToLower(hint)

	var exact []workstationTGDevice
	for _, t := range tags {
		if strings.EqualFold(t.Tag, hint) {
			exact = append(exact, t.Device)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return workstationTGDevice{}, fmt.Errorf("%w: ambiguous device tag", ErrWorkstationDeviceRequired)
	}

	if id, err := uuid.Parse(hint); err == nil {
		for _, d := range devices {
			if d.ID == id {
				return d, nil
			}
		}
	}
	// Hostname / UUID prefix when unique (legacy / manual input).
	var prefixHits []workstationTGDevice
	for _, d := range devices {
		idRaw := strings.ReplaceAll(d.ID.String(), "-", "")
		nameLower := strings.ToLower(strings.TrimSpace(d.Name))
		if strings.HasPrefix(strings.ToLower(idRaw), hintLower) ||
			(nameLower != "" && (nameLower == hintLower || strings.HasPrefix(nameLower, hintLower))) {
			prefixHits = append(prefixHits, d)
		}
	}
	if len(prefixHits) == 1 {
		return prefixHits[0], nil
	}
	return workstationTGDevice{}, ErrWorkstationDeviceNotBound
}

// ---- Source: backend/services/workforce_tg_discover.go ----
type TelegramDiscoveredChat struct {
	ChatID    string `json:"chatId"`
	ChatTitle string `json:"chatTitle"`
	ChatType  string `json:"chatType"`
	Bound     bool   `json:"bound"`
	Source    string `json:"source,omitempty"`
}

func (s *WorkforceTelegramService) RecordDiscoveredChat(ctx context.Context, chatID, chatTitle, chatType string) (bool, error) {
	return RecordTelegramDiscoveredChat(ctx, s.db, chatID, chatTitle, chatType)
}

// RecordTelegramDiscoveredChat persists a chat seen via join/message updates.
// Used by attendance worker and push discover so joining a group is enough.
// The returned bool is true when this chat_id was seen for the first time.
func RecordTelegramDiscoveredChat(ctx context.Context, db *sql.DB, chatID, chatTitle, chatType string) (bool, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || db == nil {
		return false, nil
	}
	chatType = strings.TrimSpace(chatType)
	if chatType != "group" && chatType != "supergroup" && chatType != "channel" {
		return false, nil
	}
	title := strings.TrimSpace(chatTitle)
	var isFirst bool
	err := db.QueryRowContext(ctx, `
		INSERT INTO wf_telegram_discovered_chats (chat_id, chat_title, chat_type, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (chat_id) DO UPDATE SET
			chat_title = CASE WHEN EXCLUDED.chat_title <> '' THEN EXCLUDED.chat_title ELSE wf_telegram_discovered_chats.chat_title END,
			chat_type = CASE WHEN EXCLUDED.chat_type <> '' THEN EXCLUDED.chat_type ELSE wf_telegram_discovered_chats.chat_type END,
			last_seen_at = NOW()
		RETURNING (first_seen_at = last_seen_at)`, chatID, title, chatType).Scan(&isFirst)
	if err != nil {
		return false, err
	}
	return isFirst, nil
}
func (s *WorkforceTelegramService) DiscoverGroups(ctx context.Context) ([]TelegramDiscoveredChat, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.HasBotToken {
		return nil, fmt.Errorf("请先保存 Bot Token")
	}
	if !settings.Enabled {
		return nil, fmt.Errorf("请先启用 Telegram")
	}

	bound := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id FROM wf_telegram_groups`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			bound[strings.TrimSpace(id)] = true
		}
	}
	rows.Close()

	token, _ := s.GetBotTokenForWorker(ctx)
	raw, err := DiscoverTelegramChats(ctx, s.db, token)
	if err != nil {
		return nil, err
	}

	out := make([]TelegramDiscoveredChat, 0, len(raw))
	seen := map[string]bool{}
	for _, g := range raw {
		id := strings.TrimSpace(g["chatId"])
		if id == "" {
			continue
		}
		seen[id] = true
		item := TelegramDiscoveredChat{
			ChatID:    id,
			ChatTitle: g["groupName"],
			ChatType:  g["chatType"],
			Bound:     bound[id],
			Source:    g["source"],
		}
		if item.ChatTitle == "" {
			item.ChatTitle = id
		}
		out = append(out, item)
	}

	for chatID := range bound {
		if seen[chatID] {
			continue
		}
		item := TelegramDiscoveredChat{ChatID: chatID, Bound: true, Source: "bound"}
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(chat_title,'') FROM wf_telegram_groups WHERE chat_id = $1`, chatID).Scan(&item.ChatTitle)
		if item.ChatTitle == "" {
			item.ChatTitle = chatID
		}
		out = append(out, item)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Bound != out[j].Bound {
			return !out[i].Bound
		}
		return out[i].ChatTitle < out[j].ChatTitle
	})
	return out, nil
}

type tgUpdatesResponse struct {
	OK     bool `json:"ok"`
	Result []struct {
		Message *struct {
			Chat struct {
				ID    int64  `json:"id"`
				Type  string `json:"type"`
				Title string `json:"title"`
			} `json:"chat"`
		} `json:"message"`
		ChannelPost *struct {
			Chat struct {
				ID    int64  `json:"id"`
				Type  string `json:"type"`
				Title string `json:"title"`
			} `json:"chat"`
		} `json:"channel_post"`
		MyChatMember *struct {
			Chat struct {
				ID    int64  `json:"id"`
				Type  string `json:"type"`
				Title string `json:"title"`
			} `json:"chat"`
		} `json:"my_chat_member"`
	} `json:"result"`
}

// DiscoverTelegramChats merges persisted join/message history with a live getUpdates scan.
// Telegram Bot API cannot list chats; joining a group (my_chat_member) is enough when
// recorded. Live scan may 409 if another poller owns the token — history still returns.
func DiscoverTelegramChats(ctx context.Context, db *sql.DB, token string) ([]map[string]string, error) {
	merged := map[string]map[string]string{}

	if db != nil {
		discRows, err := db.QueryContext(ctx, `
			SELECT chat_id, COALESCE(chat_title,''), COALESCE(chat_type,'')
			FROM wf_telegram_discovered_chats
			ORDER BY last_seen_at DESC`)
		if err == nil {
			for discRows.Next() {
				var id, title, chatType string
				if discRows.Scan(&id, &title, &chatType) != nil {
					continue
				}
				id = strings.TrimSpace(id)
				if id == "" {
					continue
				}
				name := strings.TrimSpace(title)
				if name == "" {
					name = id
				}
				merged[id] = map[string]string{
					"chatId":    id,
					"groupName": name,
					"chatType":  chatType,
					"source":    "history",
				}
			}
			discRows.Close()
		}
	}

	token = strings.TrimSpace(token)
	if token != "" {
		live, scanErr := telegramScanRecentChats(token)
		if scanErr == nil {
			for _, g := range live {
				id := strings.TrimSpace(g["chatId"])
				if id == "" {
					continue
				}
				existing, ok := merged[id]
				if ok {
					if existing["groupName"] == "" || existing["groupName"] == id {
						existing["groupName"] = g["groupName"]
					}
					if existing["chatType"] == "" {
						existing["chatType"] = g["chatType"]
					}
					if existing["source"] == "history" {
						existing["source"] = "history,live"
					}
					merged[id] = existing
				} else {
					g["source"] = "live"
					merged[id] = g
				}
				_, _ = RecordTelegramDiscoveredChat(ctx, db, id, g["groupName"], g["chatType"])
			}
		}
	}

	out := make([]map[string]string, 0, len(merged))
	for _, v := range merged {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["groupName"] < out[j]["groupName"]
	})
	return out, nil
}
func telegramScanRecentChats(token string) ([]map[string]string, error) {
	client := &http.Client{Timeout: 12 * time.Second}
	// Explicitly request join events and channel posts — a plain group message is not required.
	url := fmt.Sprintf(
		"https://api.telegram.org/bot%s/getUpdates?limit=100&timeout=0&allowed_updates=%s",
		token,
		"%5B%22message%22%2C%22channel_post%22%2C%22my_chat_member%22%5D",
	)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("bot is polling updates")
	}
	var out tgUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("getUpdates failed")
	}
	seen := map[string]map[string]string{}
	addChat := func(id int64, chatType, title string) {
		if chatType != "group" && chatType != "supergroup" && chatType != "channel" {
			return
		}
		key := strconv.FormatInt(id, 10)
		if _, ok := seen[key]; ok {
			return
		}
		name := strings.TrimSpace(title)
		if name == "" {
			name = key
		}
		seen[key] = map[string]string{
			"chatId":    key,
			"groupName": name,
			"chatType":  chatType,
		}
	}
	for _, u := range out.Result {
		if u.Message != nil {
			c := u.Message.Chat
			addChat(c.ID, c.Type, c.Title)
		}
		if u.ChannelPost != nil {
			c := u.ChannelPost.Chat
			addChat(c.ID, c.Type, c.Title)
		}
		if u.MyChatMember != nil {
			c := u.MyChatMember.Chat
			addChat(c.ID, c.Type, c.Title)
		}
	}
	list := make([]map[string]string, 0, len(seen))
	for _, v := range seen {
		list = append(list, v)
	}
	return list, nil
}

// ---- Source: backend/services/workforce_tg_dual_channel.go ----
const (
	AttendanceModeTGOnly        = "tg_only"
	AttendanceModePCTG          = "pc_tg"
	AttendanceModeSharedStation = "shared_station"
	TelegramChannelPersonal     = "personal"
	TelegramChannelWorkstation  = "workstation"

	PunchSourceTelegram = "telegram"
	PunchSourceAgent    = "agent"
	PunchSourceWeb      = "web"

	// punchAttendanceSourcesSQL matches all channels that create workforce punch sessions.
	punchAttendanceSourcesSQL = `IN ('telegram','agent','web')`
)

// TelegramActor is the resolved identity for a Telegram user action.
type TelegramActor struct {
	Channel         string
	EmployeeID      uuid.UUID
	DeviceID        uuid.UUID
	DeviceName      string
	RequiresPCLogin bool
	// PunchSource overrides DB/sync channel when set (e.g. web admin punch → "web").
	PunchSource string
}

func resolvePunchSource(actor *TelegramActor, fallback string) string {
	if actor != nil {
		if src := strings.TrimSpace(actor.PunchSource); src != "" {
			return src
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return PunchSourceTelegram
	}
	return fallback
}

type DeviceTelegramBindingRow struct {
	ID               uuid.UUID
	DeviceID         uuid.UUID
	DeviceName       string
	TelegramUserID   int64
	TelegramUsername string
	ChatID           string
	BoundAt          time.Time
	Status           int
	EmployeeID       uuid.UUID
	EmployeeName     string
	EmployeeCode     string
}

func (s *WorkforceTelegramService) getEmployeeAttendanceMode(ctx context.Context, empID uuid.UUID) (string, error) {
	var mode string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(attendance_mode), ''), 'pc_tg')
		FROM employees WHERE id = $1`, empID).Scan(&mode)
	if err == sql.ErrNoRows {
		return AttendanceModePCTG, ErrEmployeeNotFound
	}
	if err != nil {
		return AttendanceModePCTG, err
	}
	switch mode {
	case AttendanceModeTGOnly, AttendanceModePCTG, AttendanceModeSharedStation:
		return mode, nil
	default:
		return AttendanceModePCTG, nil
	}
}
func (s *WorkforceTelegramService) setEmployeeAttendanceMode(ctx context.Context, empID uuid.UUID, mode string) error {
	switch mode {
	case AttendanceModeTGOnly, AttendanceModePCTG, AttendanceModeSharedStation:
	default:
		return fmt.Errorf("invalid attendance mode")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE employees SET attendance_mode = $2, updated_at = NOW() WHERE id = $1`, empID, mode)
	return err
}

type workstationTGDevice struct {
	ID   uuid.UUID
	Name string
}

func (s *WorkforceTelegramService) telegramUserHasWorkstationBinding(ctx context.Context, telegramUserID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM device_telegram_bindings
		WHERE telegram_user_id = $1 AND status = 1`, telegramUserID).Scan(&n)
	return n > 0, err
}
func (s *WorkforceTelegramService) listWorkstationDevicesForTelegram(ctx context.Context, telegramUserID int64) ([]workstationTGDevice, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.device_id, COALESCE(d.hostname, '')
		FROM device_telegram_bindings b
		JOIN devices d ON d.id = b.device_id
		WHERE b.telegram_user_id = $1 AND b.status = 1
		ORDER BY d.last_seen_at DESC NULLS LAST, b.bound_at DESC`, telegramUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]workstationTGDevice, 0)
	for rows.Next() {
		var item workstationTGDevice
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *WorkforceTelegramService) employeeIsSharedWorkstationMember(ctx context.Context, deviceID, employeeID uuid.UUID) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM wf_shared_workstation_members
		WHERE device_id = $1 AND employee_id = $2`, deviceID, employeeID).Scan(&n)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	var invited uuid.NullUUID
	err = s.db.QueryRowContext(ctx, `
		SELECT invited_employee_uuid FROM devices
		WHERE id = $1 AND COALESCE(is_shared_workstation, FALSE) = TRUE`, deviceID).Scan(&invited)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return invited.Valid && invited.UUID == employeeID, nil
}
func (s *WorkforceTelegramService) resolveWorkstationActorForTelegram(
	ctx context.Context, telegramUserID int64, preferredDeviceID uuid.UUID, deviceHint string,
) (*TelegramActor, error) {
	devices, err := s.listWorkstationDevicesForTelegram(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	return s.resolveWorkstationActorFromDevices(ctx, devices, preferredDeviceID, deviceHint)
}
func (s *WorkforceTelegramService) resolveWorkstationActorFromDevices(
	ctx context.Context, devices []workstationTGDevice, preferredDeviceID uuid.UUID, deviceHint string,
) (*TelegramActor, error) {
	if len(devices) == 0 {
		return nil, nil
	}

	if preferredDeviceID == uuid.Nil && strings.TrimSpace(deviceHint) != "" {
		matched, matchErr := matchWorkstationDeviceByHint(devices, deviceHint)
		if matchErr != nil {
			return nil, matchErr
		}
		preferredDeviceID = matched.ID
	}
	if preferredDeviceID != uuid.Nil {
		var target *workstationTGDevice
		for i := range devices {
			if devices[i].ID == preferredDeviceID {
				target = &devices[i]
				break
			}
		}
		if target == nil {
			return nil, ErrWorkstationDeviceNotBound
		}
		empID, loginErr := s.resolveWorkstationLoggedInEmployee(ctx, target.ID)
		if loginErr != nil {
			return nil, loginErr
		}
		return &TelegramActor{
			Channel:    TelegramChannelWorkstation,
			DeviceID:   target.ID,
			DeviceName: target.Name,
			EmployeeID: empID,
		}, nil
	}

	type loggedInDevice struct {
		device    workstationTGDevice
		empID     uuid.UUID
		sessionAt time.Time
		lastSeen  time.Time
	}
	loggedIn := make([]loggedInDevice, 0, len(devices))
	for _, device := range devices {
		empID, sessionAt, lastSeen, loginErr := s.resolveWorkstationLoggedInEmployeeDetail(ctx, device.ID)
		if loginErr == nil {
			loggedIn = append(loggedIn, loggedInDevice{
				device: device, empID: empID, sessionAt: sessionAt, lastSeen: lastSeen,
			})
			continue
		}
		if loginErr != ErrPCLoginRequired {
			return nil, loginErr
		}
	}
	if len(loggedIn) > 1 {
		// Ambiguous without a device-tagged button.
		return nil, ErrWorkstationDeviceRequired
	}
	if len(loggedIn) == 1 {
		return &TelegramActor{
			Channel:    TelegramChannelWorkstation,
			DeviceID:   loggedIn[0].device.ID,
			DeviceName: loggedIn[0].device.Name,
			EmployeeID: loggedIn[0].empID,
		}, nil
	}

	actor := &TelegramActor{
		Channel:         TelegramChannelWorkstation,
		RequiresPCLogin: true,
	}
	if len(devices) == 1 {
		actor.DeviceID = devices[0].ID
		actor.DeviceName = devices[0].Name
	}
	return actor, nil
}
func (s *WorkforceTelegramService) resolveWorkstationLoggedInEmployee(ctx context.Context, deviceID uuid.UUID) (uuid.UUID, error) {
	empID, _, _, err := s.resolveWorkstationLoggedInEmployeeDetail(ctx, deviceID)
	return empID, err
}
func (s *WorkforceTelegramService) resolveWorkstationLoggedInEmployeeDetail(
	ctx context.Context, deviceID uuid.UUID,
) (empID uuid.UUID, sessionAt, lastSeen time.Time, err error) {
	if deviceID == uuid.Nil {
		return uuid.Nil, time.Time{}, time.Time{}, ErrPCLoginRequired
	}
	var loggedIn uuid.UUID
	var sessionAtNull, lastSeenNull sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT employee_uuid, employee_session_at, last_seen_at FROM devices
		WHERE id = $1 AND LOWER(status) = 'approved'`, deviceID).Scan(&loggedIn, &sessionAtNull, &lastSeenNull)
	if err == sql.ErrNoRows {
		return uuid.Nil, time.Time{}, time.Time{}, ErrPCLoginRequired
	}
	if err != nil {
		return uuid.Nil, time.Time{}, time.Time{}, err
	}
	if !sessionAtNull.Valid || loggedIn == uuid.Nil {
		return uuid.Nil, time.Time{}, time.Time{}, ErrPCLoginRequired
	}
	if lastSeenNull.Valid {
		lastSeen = lastSeenNull.Time
	}
	return loggedIn, sessionAtNull.Time, lastSeen, nil
}
func (s *WorkforceTelegramService) findSharedWorkstationDeviceForEmployee(ctx context.Context, empID uuid.UUID) (uuid.UUID, error) {
	var deviceID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT m.device_id FROM wf_shared_workstation_members m
		JOIN devices d ON d.id = m.device_id AND LOWER(d.status) = 'approved'
		WHERE m.employee_id = $1
		ORDER BY d.last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
	if err == nil && deviceID != uuid.Nil {
		return deviceID, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return uuid.Nil, err
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM devices
		WHERE invited_employee_uuid = $1 AND COALESCE(is_shared_workstation, FALSE) = TRUE
			AND LOWER(status) = 'approved'
		ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
	if err == sql.ErrNoRows {
		return uuid.Nil, ErrWorkstationNotConfigured
	}
	return deviceID, err
}
func (s *WorkforceTelegramService) deviceIsSharedWorkstation(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	var isShared bool
	var memberCount int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(is_shared_workstation, FALSE),
			(SELECT COUNT(*) FROM wf_shared_workstation_members WHERE device_id = $1)
		FROM devices WHERE id = $1`, deviceID).Scan(&isShared, &memberCount)
	if err != nil {
		return false, err
	}
	return isShared || memberCount > 0, nil
}

// ResolveTelegramBinding returns how a TG account is bound without requiring PC login.
// Optional deviceID / deviceHint pins multi-device workstation TG to one PC.
func (s *WorkforceTelegramService) ResolveTelegramBinding(ctx context.Context, telegramUserID int64) (*TelegramActor, error) {
	return s.ResolveTelegramBindingWithDevice(ctx, telegramUserID, uuid.Nil, "")
}
func (s *WorkforceTelegramService) ResolveTelegramBindingWithDevice(
	ctx context.Context, telegramUserID int64, deviceID uuid.UUID, deviceHint string,
) (*TelegramActor, error) {
	if telegramUserID <= 0 {
		return nil, fmt.Errorf("invalid telegram user id")
	}
	if deviceID == uuid.Nil && strings.TrimSpace(deviceHint) == "" {
		// Common case (no device pin): one round trip covers workstation bindings,
		// the personal binding and the employee's language preference.
		return s.resolveTelegramBindingOneTrip(ctx, telegramUserID)
	}
	if actor, err := s.resolveWorkstationActorForTelegram(ctx, telegramUserID, deviceID, deviceHint); err != nil {
		return nil, err
	} else if actor != nil {
		return actor, nil
	}
	if deviceID != uuid.Nil || strings.TrimSpace(deviceHint) != "" {
		// Device pin only applies to workstation bindings.
		return nil, ErrWorkstationDeviceNotBound
	}
	empID, err := s.resolvePersonalTelegramEmployee(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	return &TelegramActor{Channel: TelegramChannelPersonal, EmployeeID: empID}, nil
}

func (s *WorkforceTelegramService) ResolveTelegramActorWithDevice(ctx context.Context, telegramUserID int64, deviceID uuid.UUID, deviceHint string) (*TelegramActor, error) {
	key := fmt.Sprintf("actor:%d:%s:%s", telegramUserID, deviceID, deviceHint)
	actor, err := telegramRequestRead(ctx, s, key, func() (*TelegramActor, error) {
		return s.loadTelegramActorWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	})
	if err != nil {
		return actor, err
	}
	copy := *actor
	return &copy, nil
}
func (s *WorkforceTelegramService) loadTelegramActorWithDevice(
	ctx context.Context, telegramUserID int64, deviceID uuid.UUID, deviceHint string,
) (*TelegramActor, error) {
	actor, err := s.ResolveTelegramBindingWithDevice(ctx, telegramUserID, deviceID, deviceHint)
	if err != nil {
		return nil, err
	}
	if actor.Channel == TelegramChannelWorkstation {
		if actor.EmployeeID == uuid.Nil {
			return actor, ErrPCLoginRequired
		}
		return actor, nil
	}
	return actor, nil
}
func ParseOptionalDeviceID(raw string) (uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid device id")
	}
	return id, nil
}
// telegramRequestStore primes the per-request cache with a value that was read
// as a by-product of another query. A value already loaded is never replaced.
func telegramRequestStore[T any](ctx context.Context, s *WorkforceTelegramService, key string, value T) {
	cache, ok := ctx.Value(telegramRequestCacheKey{}).(*sync.Map)
	if !ok {
		return
	}
	raw, _ := cache.LoadOrStore(telegramReadKey{s, key}, &telegramReadEntry{})
	entry := raw.(*telegramReadEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.loaded {
		entry.value = value
		entry.loaded = true
	}
}

// resolveTelegramBindingOneTrip returns exactly what the two-step lookup did
// (workstation bindings take precedence, then the personal binding) but reads
// both tables, plus the personal employee's /lang preference, in one query.
// The binding is still read from the database on every punch request.
func (s *WorkforceTelegramService) resolveTelegramBindingOneTrip(ctx context.Context, telegramUserID int64) (*TelegramActor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ord, kind, id, name, ls, ba FROM (
			SELECT 0 AS ord, 'w' AS kind, b.device_id::text AS id, COALESCE(d.hostname, '') AS name,
				d.last_seen_at AS ls, b.bound_at AS ba
			FROM device_telegram_bindings b
			JOIN devices d ON d.id = b.device_id
			WHERE b.telegram_user_id = $1 AND b.status = 1
			UNION ALL
			SELECT 1, 'p', t.employee_id::text, COALESCE(l.preferred_lang, ''), NULL, NULL
			FROM employee_telegram_bindings t
			LEFT JOIN wf_tg_employee_lang_prefs l ON l.employee_id = t.employee_id
			WHERE t.telegram_user_id = $1 AND t.status = 1
		) u
		ORDER BY ord, ls DESC NULLS LAST, ba DESC`, telegramUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := make([]workstationTGDevice, 0)
	var personalID uuid.UUID
	var personalLang string
	var havePersonal bool
	for rows.Next() {
		var ord int
		var kind, id, name string
		var ls, ba sql.NullTime
		if err := rows.Scan(&ord, &kind, &id, &name, &ls, &ba); err != nil {
			return nil, err
		}
		parsed, perr := uuid.Parse(id)
		if perr != nil {
			return nil, perr
		}
		if kind == "w" {
			devices = append(devices, workstationTGDevice{ID: parsed, Name: name})
		} else if !havePersonal {
			personalID, personalLang, havePersonal = parsed, name, true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(devices) > 0 {
		return s.resolveWorkstationActorFromDevices(ctx, devices, uuid.Nil, "")
	}
	if !havePersonal {
		return nil, ErrTelegramNotBound
	}
	telegramRequestStore(ctx, s, "employee_lang:"+personalID.String(),
		sql.NullString{String: personalLang, Valid: strings.TrimSpace(personalLang) != ""})
	return &TelegramActor{Channel: TelegramChannelPersonal, EmployeeID: personalID}, nil
}
func (s *WorkforceTelegramService) resolvePersonalTelegramEmployee(ctx context.Context, telegramUserID int64) (uuid.UUID, error) {
	var empID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT employee_id FROM employee_telegram_bindings
		WHERE telegram_user_id = $1 AND status = 1`, telegramUserID).Scan(&empID)
	if err == sql.ErrNoRows {
		return uuid.Nil, ErrTelegramNotBound
	}
	return empID, err
}
func (s *WorkforceTelegramService) employeeRequiresPCLogin(ctx context.Context, empID uuid.UUID, globalRequire bool) (bool, error) {
	mode, err := s.getEmployeeAttendanceMode(ctx, empID)
	if err != nil {
		return globalRequire, err
	}
	switch mode {
	case AttendanceModeTGOnly:
		return false, nil
	case AttendanceModeSharedStation:
		return true, nil
	default:
		return globalRequire, nil
	}
}
func (s *WorkforceTelegramService) updateTelegramChatID(ctx context.Context, actor *TelegramActor, chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || actor == nil {
		return
	}
	if actor.Channel == TelegramChannelWorkstation && actor.DeviceID != uuid.Nil {
		if isTelegramGroupChatID(chatID) {
			_, _ = s.db.ExecContext(ctx, `
				UPDATE device_telegram_bindings SET chat_id = $1
				WHERE device_id = $2 AND status = 1`, chatID, actor.DeviceID)
		}
		return
	}
	if actor.EmployeeID != uuid.Nil {
		if isTelegramGroupChatID(chatID) {
			_, _ = s.db.ExecContext(ctx, `
				UPDATE employee_telegram_bindings SET chat_id = $1
				WHERE employee_id = $2 AND status = 1`, chatID, actor.EmployeeID)
			return
		}
		_, _ = s.db.ExecContext(ctx, `
			UPDATE employee_telegram_bindings SET chat_id = $1
			WHERE employee_id = $2 AND status = 1
			  AND COALESCE(NULLIF(TRIM(chat_id), ''), '') = ''`, chatID, actor.EmployeeID)
	}
}

// getChatIDForEmployee resolves the attendance group chat for PC→TG notifications and grace config.
func (s *WorkforceTelegramService) getChatIDForEmployee(ctx context.Context, empID uuid.UUID) string {
	return s.resolveNotifyChatID(ctx, empID, uuid.Nil)
}
func isTelegramGroupChatID(chatID string) bool {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return false
	}
	if strings.HasPrefix(chatID, "-") {
		return true
	}
	if n, err := strconv.ParseInt(chatID, 10, 64); err == nil && n < 0 {
		return true
	}
	return false
}
func (s *WorkforceTelegramService) chatIDFromDeviceBinding(ctx context.Context, deviceID uuid.UUID) string {
	if deviceID == uuid.Nil {
		return ""
	}
	var chatID string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(chat_id,'') FROM device_telegram_bindings
		WHERE device_id = $1 AND status = 1`, deviceID).Scan(&chatID)
	return strings.TrimSpace(chatID)
}

// resolveNotifyChatID picks the best Telegram chat for PC→TG group notifications.
func (s *WorkforceTelegramService) resolveNotifyChatID(ctx context.Context, empID, deviceID uuid.UUID) string {
	if empID == uuid.Nil && deviceID == uuid.Nil {
		return ""
	}

	if deviceID != uuid.Nil {
		if chatID := s.chatIDFromDeviceBinding(ctx, deviceID); chatID != "" && isTelegramGroupChatID(chatID) {
			return chatID
		}
	}

	var personalChat string
	if empID != uuid.Nil {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(chat_id,'') FROM employee_telegram_bindings
			WHERE employee_id = $1 AND status = 1 ORDER BY updated_at DESC NULLS LAST LIMIT 1`, empID).Scan(&personalChat)
		personalChat = strings.TrimSpace(personalChat)
		if personalChat != "" && isTelegramGroupChatID(personalChat) {
			return personalChat
		}
	}

	if empID != uuid.Nil {
		var wsChat string
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(dtb.chat_id,'')
			FROM wf_shared_workstation_members m
			JOIN device_telegram_bindings dtb ON dtb.device_id = m.device_id AND dtb.status = 1
			WHERE m.employee_id = $1
			ORDER BY dtb.bound_at DESC NULLS LAST LIMIT 1`, empID).Scan(&wsChat)
		wsChat = strings.TrimSpace(wsChat)
		if wsChat != "" && isTelegramGroupChatID(wsChat) {
			return wsChat
		}
	}

	if deviceID != uuid.Nil {
		if chatID := s.chatIDFromDeviceBinding(ctx, deviceID); chatID != "" {
			return chatID
		}
	}

	if empID != uuid.Nil {
		var loggedInDevice uuid.UUID
		if err := s.db.QueryRowContext(ctx, `
			SELECT id FROM devices
			WHERE employee_uuid = $1 AND LOWER(status) = 'approved' AND employee_session_at IS NOT NULL
			ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&loggedInDevice); err == nil && loggedInDevice != uuid.Nil {
			if chatID := s.chatIDFromDeviceBinding(ctx, loggedInDevice); chatID != "" {
				return chatID
			}
		}
	}

	if personalChat != "" {
		return personalChat
	}

	if empID != uuid.Nil {
		var sessionChat string
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(source_chat_id,'') FROM work_sessions
			WHERE employee_id = $1 AND COALESCE(source_chat_id,'') != ''
			ORDER BY checkin_time DESC LIMIT 1`, empID).Scan(&sessionChat)
		if strings.TrimSpace(sessionChat) != "" {
			return strings.TrimSpace(sessionChat)
		}
	}

	if empID != uuid.Nil {
		var groupChat string
		_ = s.db.QueryRowContext(ctx, `
			SELECT g.chat_id FROM wf_telegram_groups g
			WHERE g.enabled = TRUE
			  AND EXISTS (
			    SELECT 1 FROM employee_telegram_bindings b
			    WHERE b.employee_id = $1 AND b.status = 1
			  )
			ORDER BY g.created_at DESC LIMIT 1`, empID).Scan(&groupChat)
		if strings.TrimSpace(groupChat) != "" {
			return strings.TrimSpace(groupChat)
		}
	}

	var groupCount int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM wf_telegram_groups WHERE enabled = TRUE`).Scan(&groupCount)
	if groupCount == 1 {
		var onlyChat string
		_ = s.db.QueryRowContext(ctx, `SELECT chat_id FROM wf_telegram_groups WHERE enabled = TRUE LIMIT 1`).Scan(&onlyChat)
		return strings.TrimSpace(onlyChat)
	}

	return ""
}
func (s *WorkforceTelegramService) resolveNotifyTelegramUID(ctx context.Context, empID, deviceID uuid.UUID) int64 {
	var tgUID int64
	if empID != uuid.Nil {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(telegram_user_id,0) FROM employee_telegram_bindings
			WHERE employee_id = $1 AND status = 1 ORDER BY updated_at DESC NULLS LAST LIMIT 1`, empID).Scan(&tgUID)
	}
	if tgUID == 0 && deviceID != uuid.Nil {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(telegram_user_id,0) FROM device_telegram_bindings
			WHERE device_id = $1 AND status = 1`, deviceID).Scan(&tgUID)
	}
	return tgUID
}
func preferredDeviceFromActor(actor *TelegramActor) uuid.UUID {
	if actor != nil && actor.Channel == TelegramChannelWorkstation && actor.DeviceID != uuid.Nil {
		return actor.DeviceID
	}
	return uuid.Nil
}
func (s *WorkforceTelegramService) getChatIDForActor(ctx context.Context, actor *TelegramActor) string {
	if actor == nil {
		return ""
	}
	var chatID string
	if actor.Channel == TelegramChannelWorkstation && actor.DeviceID != uuid.Nil {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(chat_id,'') FROM device_telegram_bindings
			WHERE device_id = $1 AND status = 1`, actor.DeviceID).Scan(&chatID)
		return chatID
	}
	if actor.EmployeeID != uuid.Nil {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(chat_id,'') FROM employee_telegram_bindings
			WHERE employee_id = $1 AND status = 1`, actor.EmployeeID).Scan(&chatID)
	}
	return chatID
}
func (s *WorkforceTelegramService) ensureTelegramNotCrossBound(ctx context.Context, telegramUserID int64, targetChannel string) error {
	hasWorkstation, err := s.telegramUserHasWorkstationBinding(ctx, telegramUserID)
	if err != nil {
		return err
	}
	if hasWorkstation && targetChannel == TelegramChannelPersonal {
		return ErrTelegramBoundToWorkstation
	}
	if empID, err := s.resolvePersonalTelegramEmployee(ctx, telegramUserID); err != nil && err != ErrTelegramNotBound {
		return err
	} else if empID != uuid.Nil && targetChannel == TelegramChannelWorkstation {
		return ErrTelegramBoundToPersonal
	}
	return nil
}
func (s *WorkforceTelegramService) ListDeviceTelegramBindings(ctx context.Context, search string) ([]DeviceTelegramBindingRow, error) {
	search = strings.TrimSpace(search)
	query := `
		SELECT b.id, b.device_id, COALESCE(d.hostname,''), b.telegram_user_id,
			COALESCE(b.telegram_username,''), COALESCE(b.chat_id,''), b.bound_at, b.status,
			m.employee_id, COALESCE(e.name,''), COALESCE(e.employee_id,'')
		FROM device_telegram_bindings b
		JOIN devices d ON d.id = b.device_id
		LEFT JOIN wf_shared_workstation_members m ON m.device_id = b.device_id
		LEFT JOIN employees e ON e.id = m.employee_id
		WHERE b.status = 1`
	args := []interface{}{}
	if search != "" {
		query += ` AND (d.hostname ILIKE $1 OR b.telegram_username ILIKE $1 OR b.telegram_user_id::text ILIKE $1
			OR e.name ILIKE $1 OR e.employee_id ILIKE $1)`
		args = append(args, "%"+search+"%")
	}
	query += ` ORDER BY b.bound_at DESC, e.name NULLS LAST`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DeviceTelegramBindingRow, 0)
	for rows.Next() {
		var row DeviceTelegramBindingRow
		var empID uuid.NullUUID
		if err := rows.Scan(&row.ID, &row.DeviceID, &row.DeviceName, &row.TelegramUserID,
			&row.TelegramUsername, &row.ChatID, &row.BoundAt, &row.Status,
			&empID, &row.EmployeeName, &row.EmployeeCode); err != nil {
			continue
		}
		if empID.Valid {
			row.EmployeeID = empID.UUID
		}
		items = append(items, row)
	}
	return items, nil
}
func (s *WorkforceTelegramService) GetDeviceTelegramBindingByDevice(ctx context.Context, deviceID uuid.UUID) (int64, string, string, bool) {
	if deviceID == uuid.Nil {
		return 0, "", "", false
	}
	var tgUID int64
	var tgUser string
	var chatID string
	err := s.db.QueryRowContext(ctx, `
		SELECT telegram_user_id, COALESCE(telegram_username,''), COALESCE(chat_id,'')
		FROM device_telegram_bindings
		WHERE device_id = $1 AND status = 1`, deviceID).Scan(&tgUID, &tgUser, &chatID)
	if err != nil {
		return 0, "", "", false
	}
	return tgUID, tgUser, chatID, tgUID > 0
}
// ---- Source: backend/services/workforce_tg_export.go ----
type tgExportActivityStat struct {
	Count int
	Time  int
}
type tgExportUserStat struct {
	UserID               int64
	Nickname             string
	Shift                string
	WorkDays             int
	WorkStartCount       int
	WorkEndCount         int
	WorkStartAt          *time.Time
	WorkEndAt            *time.Time
	WorkHours            int
	Activities           map[string]tgExportActivityStat
	TotalActivityCount   int
	TotalAccumulatedTime int
	OvertimeCount        int
	TotalOvertimeTime    int
	EarlyCount           int
	LateCount            int
	WorkEndFines         int
	WorkStartFines       int
	TotalFines           int
}

func (s *WorkforceTelegramService) ExportAttendanceXLSX(ctx context.Context, dateFrom, dateTo, chatID string) ([]byte, string, error) {
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	if dateFrom == "" {
		dateFrom = time.Now().Format("2006-01-02")
	}
	if dateTo == "" {
		dateTo = dateFrom
	}

	if err := s.ensureExportAttendanceComplete(ctx, dateFrom, dateTo, chatID); err != nil {
		log.Printf("[Telegram] export attendance prep failed: %v", err)
	}

	stats, activityNames, err := s.buildTelegramExportStats(ctx, dateFrom, dateTo, chatID)
	if err != nil {
		return nil, "", err
	}

	f := excelize.NewFile()
	defer f.Close()
	if err := s.writeTelegramStatsWorkbook(f, stats, activityNames, telegramExportUsePunchTimes(dateFrom, dateTo)); err != nil {
		return nil, "", err
	}

	buf := bytes.NewBuffer(nil)
	if err := f.Write(buf); err != nil {
		return nil, "", err
	}
	filename := exportFilenameSuffix(chatID, dateFrom, dateTo) + ".xlsx"
	return buf.Bytes(), filename, nil
}
func (s *WorkforceTelegramService) buildTelegramExportStats(ctx context.Context, dateFrom, dateTo, chatID string) ([]tgExportUserStat, []string, error) {
	settings, _ := s.GetSettings(ctx)
	shifts := []string{"day"}
	if settings != nil {
		shifts = settings.EnabledShiftTypes()
	} else {
		shifts = []string{"day", "night"}
	}

	type userKey struct {
		userID int64
		empID  uuid.UUID
		name   string
	}
	users := make(map[string]userKey)
	chatID = strings.TrimSpace(chatID)
	userQuery := `
		SELECT DISTINCT b.telegram_user_id, b.employee_id, e.name
		FROM employee_telegram_bindings b
		JOIN employees e ON e.id = b.employee_id
		WHERE b.status = 1
		  AND ($1 = '' OR b.chat_id = $1 OR EXISTS (
			SELECT 1 FROM work_sessions ws
			WHERE ws.employee_id = b.employee_id
			  AND ws.source IN ('telegram','agent','web')
			  AND (
				ws.attendance_date BETWEEN $2::date AND $3::date
				OR (ws.checkin_time AT TIME ZONE $4)::date BETWEEN $2::date AND $3::date
			  )
		  ) OR EXISTS (
			SELECT 1 FROM work_sessions_archive wsa
			WHERE wsa.employee_id = b.employee_id
			  AND wsa.source IN ('telegram','agent','web')
			  AND (
				wsa.attendance_date BETWEEN $2::date AND $3::date
				OR (wsa.checkin_time AT TIME ZONE $4)::date BETWEEN $2::date AND $3::date
			  )
		  ) OR EXISTS (
			SELECT 1 FROM wf_tg_activity_sessions s
			WHERE s.employee_id = b.employee_id
			  AND COALESCE(s.source_chat_id,'') = $1
			  AND s.attendance_date BETWEEN $2::date AND $3::date
		  ) OR EXISTS (
			SELECT 1 FROM wf_tg_activity_sessions_archive sa
			WHERE sa.employee_id = b.employee_id
			  AND COALESCE(sa.source_chat_id,'') = $1
			  AND sa.attendance_date BETWEEN $2::date AND $3::date
		  ) OR EXISTS (
			SELECT 1 FROM attendance_records ar
			WHERE ar.employee_id = b.employee_id
			  AND ar.date BETWEEN $2::date AND $3::date
			  AND ar.checkin_time IS NOT NULL
		  ) OR EXISTS (
			SELECT 1 FROM wf_shared_workstation_members m
			JOIN device_telegram_bindings dtb ON dtb.device_id = m.device_id AND dtb.status = 1
			WHERE m.employee_id = b.employee_id AND dtb.chat_id = $1
		  ))
		ORDER BY e.name`
	tz := "Asia/Shanghai"
	if settings != nil && strings.TrimSpace(settings.Timezone) != "" {
		tz = strings.TrimSpace(settings.Timezone)
	}
	loc := tgLoadLocation(tz)
	usePunchTimes := telegramExportUsePunchTimes(dateFrom, dateTo)
	userRows, err := s.db.QueryContext(ctx, userQuery, chatID, dateFrom, dateTo, tz)
	if err != nil {
		return nil, nil, err
	}
	defer userRows.Close()
	for userRows.Next() {
		var tgUID int64
		var empID uuid.UUID
		var name string
		if userRows.Scan(&tgUID, &empID, &name) != nil {
			continue
		}
		users[exportUserKey(empID, tgUID)] = userKey{userID: tgUID, empID: empID, name: name}
	}

	// Weak mode: include employees who punched via web without Telegram binding (admin full export).
	if settings != nil && settings.WebPunchOnly && chatID == "" {
		unboundQuery := `
			SELECT DISTINCT e.id, e.name
			FROM employees e
			WHERE (
				EXISTS (
					SELECT 1 FROM work_sessions ws
					WHERE ws.employee_id = e.id
					  AND ws.source = 'web'
					  AND (
						ws.attendance_date BETWEEN $1::date AND $2::date
						OR (ws.checkin_time AT TIME ZONE $3)::date BETWEEN $1::date AND $2::date
					  )
				) OR EXISTS (
					SELECT 1 FROM work_sessions_archive wsa
					WHERE wsa.employee_id = e.id
					  AND wsa.source = 'web'
					  AND (
						wsa.attendance_date BETWEEN $1::date AND $2::date
						OR (wsa.checkin_time AT TIME ZONE $3)::date BETWEEN $1::date AND $2::date
					  )
				) OR EXISTS (
					SELECT 1 FROM wf_tg_activity_sessions s
					WHERE s.employee_id = e.id
					  AND COALESCE(s.source,'') = 'web'
					  AND s.attendance_date BETWEEN $1::date AND $2::date
				) OR EXISTS (
					SELECT 1 FROM wf_tg_activity_sessions_archive sa
					WHERE sa.employee_id = e.id
					  AND COALESCE(sa.source,'') = 'web'
					  AND sa.attendance_date BETWEEN $1::date AND $2::date
				) OR EXISTS (
					SELECT 1 FROM attendance_records ar
					WHERE ar.employee_id = e.id
					  AND ar.date BETWEEN $1::date AND $2::date
					  AND ar.checkin_time IS NOT NULL
					  AND COALESCE(ar.checkin_source,'') = 'web'
				)
			)
			  AND NOT EXISTS (
				SELECT 1 FROM employee_telegram_bindings b
				WHERE b.employee_id = e.id AND b.status = 1
			)`
		if rows, err := s.db.QueryContext(ctx, unboundQuery, dateFrom, dateTo, tz); err == nil {
			for rows.Next() {
				var empID uuid.UUID
				var name string
				if rows.Scan(&empID, &name) != nil {
					continue
				}
				key := exportUserKey(empID, 0)
				if _, exists := users[key]; exists {
					continue
				}
				users[key] = userKey{userID: 0, empID: empID, name: name}
			}
			rows.Close()
		}
	}

	statMap := make(map[string]*tgExportUserStat)
	for _, u := range users {
		for _, shift := range shifts {
			key := exportStatKey(u.empID, u.userID, shift)
			statMap[key] = &tgExportUserStat{
				UserID:     exportDisplayUserID(u.userID, u.empID),
				Nickname:   u.name,
				Shift:      shift,
				Activities: make(map[string]tgExportActivityStat),
			}
		}
	}

	chatFilter, chatArg := tgExportChatFilter(chatID)
	tzParam := 3
	todayParam := 4
	if chatArg != "" {
		tzParam = 4
		todayParam = 5
	}
	todayDate := time.Now().In(loc).Format("2006-01-02")
	// Live ∪ archive (deduped by id); attendance_records fills days purged before work archive existed.
	// Open sessions: only live + attendance_date=today may use NOW(); archive/historical open contribute 0.
	workQuery := fmt.Sprintf(`
		WITH unioned AS (
			SELECT ws.id,
				ws.employee_id,
				COALESCE(ws.shift_type,'day') AS shift_type,
				ws.attendance_date,
				ws.checkin_time,
				ws.checkout_time,
				COALESCE(ws.status,'closed') AS status,
				ws.source_chat_id,
				ws.is_live
			FROM (
				SELECT id, employee_id, shift_type, attendance_date, checkin_time, checkout_time, status, source_chat_id, source, TRUE AS is_live
				FROM work_sessions
				UNION ALL
				SELECT id, employee_id, shift_type, attendance_date, checkin_time, checkout_time, status, source_chat_id, source, FALSE AS is_live
				FROM work_sessions_archive
			) ws
			WHERE ws.source IN ('telegram','agent','web')
			  AND (
				ws.attendance_date BETWEEN $1::date AND $2::date
				OR (ws.checkin_time AT TIME ZONE $%d)::date BETWEEN $1::date AND $2::date
			  )%s
		),
		sess_raw AS (
			SELECT DISTINCT ON (id)
				id, employee_id, shift_type, attendance_date, checkin_time, checkout_time, status, source_chat_id, is_live
			FROM unioned
			ORDER BY id, is_live DESC
		),
		sess AS (
			SELECT employee_id, shift_type, attendance_date, checkin_time, checkout_time, source_chat_id,
				CASE
					WHEN checkout_time IS NOT NULL THEN checkout_time
					WHEN is_live AND status = 'open' AND attendance_date = $%d::date THEN NOW()
					ELSE NULL
				END AS duration_end
			FROM sess_raw
		),
		ar_fill AS (
			SELECT ar.employee_id,
				COALESCE(NULLIF(TRIM(ar.shift_type), ''), 'day') AS shift_type,
				ar.date AS attendance_date,
				ar.checkin_time,
				ar.checkout_time,
				NULL::varchar AS source_chat_id,
				CASE
					WHEN ar.checkout_time IS NOT NULL THEN ar.checkout_time
					WHEN ar.date = $%d::date THEN NOW()
					ELSE NULL
				END AS duration_end
			FROM attendance_records ar
			WHERE ar.date BETWEEN $1::date AND $2::date
			  AND ar.checkin_time IS NOT NULL
			  AND NOT EXISTS (
				SELECT 1 FROM sess s
				WHERE s.employee_id = ar.employee_id
				  AND s.attendance_date = ar.date
				  AND s.shift_type = COALESCE(NULLIF(TRIM(ar.shift_type), ''), 'day')
			  )%s
		),
		combined AS (
			SELECT employee_id, shift_type, attendance_date, checkin_time, checkout_time, source_chat_id, duration_end FROM sess
			UNION ALL
			SELECT employee_id, shift_type, attendance_date, checkin_time, checkout_time, source_chat_id, duration_end FROM ar_fill
		)
		SELECT c.employee_id, c.shift_type,
			COUNT(DISTINCT CASE WHEN c.duration_end IS NOT NULL THEN c.attendance_date END)::int,
			COUNT(*) FILTER (WHERE c.checkin_time IS NOT NULL AND c.duration_end IS NOT NULL)::int,
			COUNT(*) FILTER (WHERE c.checkout_time IS NOT NULL)::int,
			MIN(c.checkin_time) FILTER (WHERE c.duration_end IS NOT NULL),
			MAX(c.checkout_time),
			COALESCE(SUM(
				CASE
					WHEN c.duration_end IS NOT NULL AND c.duration_end > c.checkin_time
					THEN EXTRACT(EPOCH FROM (c.duration_end - c.checkin_time))
					ELSE 0
				END
			),0)::int
		FROM combined c
		GROUP BY c.employee_id, c.shift_type`, tzParam, chatFilter, todayParam, todayParam, tgExportAttendanceChatFilter(chatID))
	workArgs := []interface{}{dateFrom, dateTo}
	if chatArg != "" {
		workArgs = append(workArgs, chatArg)
	}
	workArgs = append(workArgs, tz, todayDate)
	if rows, err := s.db.QueryContext(ctx, workQuery, workArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var empID uuid.UUID
			var shift string
			var workDays, startCnt, endCnt, workHours int
			var minCheckin sql.NullTime
			var maxCheckout sql.NullTime
			if rows.Scan(&empID, &shift, &workDays, &startCnt, &endCnt, &minCheckin, &maxCheckout, &workHours) != nil {
				continue
			}
			tgUID := s.telegramUserIDForEmployee(ctx, empID)
			st := statMap[exportStatKey(empID, tgUID, shift)]
			if st == nil {
				var name string
				_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&name)
				st = &tgExportUserStat{
					UserID:     exportDisplayUserID(tgUID, empID),
					Nickname:   name,
					Shift:      shift,
					Activities: make(map[string]tgExportActivityStat),
				}
				statMap[exportStatKey(empID, tgUID, shift)] = st
			}
			st.WorkDays = workDays
			st.WorkStartCount = startCnt
			st.WorkEndCount = endCnt
			st.WorkHours = workHours
			if usePunchTimes {
				if minCheckin.Valid {
					t := minCheckin.Time.In(loc)
					st.WorkStartAt = &t
				}
				if maxCheckout.Valid {
					t := maxCheckout.Time.In(loc)
					st.WorkEndAt = &t
				}
			}
		}
	}

	actFilter, actArg := tgExportActivityChatFilter(chatID)
	actQuery := fmt.Sprintf(`
		SELECT s.employee_id, COALESCE(s.shift_type,'day'), s.activity_name,
			COUNT(*)::int,
			COALESCE(SUM(s.elapsed_seconds),0)::int,
			COUNT(*) FILTER (WHERE s.overtime_seconds > 0)::int,
			COALESCE(SUM(s.overtime_seconds),0)::int,
			COALESCE(SUM(s.fine_amount),0)::int
		FROM (
			SELECT employee_id, shift_type, activity_name, elapsed_seconds, overtime_seconds, fine_amount,
				status, attendance_date, source_chat_id
			FROM wf_tg_activity_sessions
			UNION ALL
			SELECT employee_id, shift_type, activity_name, elapsed_seconds, overtime_seconds, fine_amount,
				status, attendance_date, source_chat_id
			FROM wf_tg_activity_sessions_archive
		) s
		WHERE s.status = 'closed'
		  AND s.attendance_date BETWEEN $1::date AND $2::date%s
		GROUP BY s.employee_id, COALESCE(s.shift_type,'day'), s.activity_name`, actFilter)
	actArgs := []interface{}{dateFrom, dateTo}
	if actArg != "" {
		actArgs = append(actArgs, actArg)
	}
	activityNameSet := make(map[string]struct{})
	if rows, err := s.db.QueryContext(ctx, actQuery, actArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var empID uuid.UUID
			var shift, actName string
			var count, totalSec, otCount, otSec, actFine int
			if rows.Scan(&empID, &shift, &actName, &count, &totalSec, &otCount, &otSec, &actFine) != nil {
				continue
			}
			tgUID := s.telegramUserIDForEmployee(ctx, empID)
			st := statMap[exportStatKey(empID, tgUID, shift)]
			if st == nil {
				var name string
				_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&name)
				st = &tgExportUserStat{
					UserID:     exportDisplayUserID(tgUID, empID),
					Nickname:   name,
					Shift:      shift,
					Activities: make(map[string]tgExportActivityStat),
				}
				statMap[exportStatKey(empID, tgUID, shift)] = st
			}
			actName = strings.TrimSpace(actName)
			if actName != "" {
				activityNameSet[actName] = struct{}{}
				st.Activities[actName] = tgExportActivityStat{Count: count, Time: totalSec}
			}
			st.TotalActivityCount += count
			st.TotalAccumulatedTime += totalSec
			st.OvertimeCount += otCount
			st.TotalOvertimeTime += otSec
			st.TotalFines += actFine
		}
	}

	penaltyQuery := `
		SELECT p.employee_id, p.reason, p.amount
		FROM penalty_records p
		WHERE p.category = 'TG考勤'
		  AND p.penalty_date BETWEEN $1::date AND $2::date`
	penaltyArgs := []interface{}{dateFrom, dateTo}
	if strings.TrimSpace(chatID) != "" {
		penaltyQuery += `
		  AND (` + tgGroupEmployeeBelongsToChatSQL("p.employee_id", "$3") + `)`
		penaltyArgs = append(penaltyArgs, chatID)
	}
	if rows, err := s.db.QueryContext(ctx, penaltyQuery, penaltyArgs...); err == nil {
		defer rows.Close()
		for rows.Next() {
			var empID uuid.UUID
			var reason string
			var amount float64
			if rows.Scan(&empID, &reason, &amount) != nil {
				continue
			}
			tgUID := s.telegramUserIDForEmployee(ctx, empID)
			shift := exportPenaltyShift(reason)
			st := statMap[exportStatKey(empID, tgUID, shift)]
			if st == nil {
				continue
			}
			amt := int(amount + 0.5)
			st.TotalFines += amt
			if strings.Contains(reason, "迟到") {
				st.LateCount++
				st.WorkStartFines += amt
			} else if strings.Contains(reason, "早退") {
				st.EarlyCount++
				st.WorkEndFines += amt
			} else if strings.Contains(reason, "上班") {
				st.WorkStartFines += amt
			} else if strings.Contains(reason, "下班") {
				st.WorkEndFines += amt
			}
		}
	}

	types, _ := s.ListActivityTypes(ctx)
	for _, t := range types {
		name := strings.TrimSpace(t.Name)
		if name != "" {
			activityNameSet[name] = struct{}{}
		}
	}
	activityNames := make([]string, 0, len(activityNameSet))
	for name := range activityNameSet {
		activityNames = append(activityNames, name)
	}
	sort.Strings(activityNames)

	out := make([]tgExportUserStat, 0, len(statMap))
	for _, st := range statMap {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Nickname != out[j].Nickname {
			return out[i].Nickname < out[j].Nickname
		}
		return out[i].Shift < out[j].Shift
	})
	return out, activityNames, nil
}

// exportUserKey identifies one employee in export stats. Always keyed by employee_id so
// shared-workstation members with the same Telegram account each get their own row.
func exportUserKey(empID uuid.UUID, _ int64) string {
	return "emp:" + empID.String()
}
func exportStatKey(empID uuid.UUID, tgUID int64, shift string) string {
	return exportUserKey(empID, tgUID) + ":" + shift
}
func exportDisplayUserID(tgUID int64, empID uuid.UUID) int64 {
	if tgUID > 0 {
		return tgUID
	}
	var n uint64
	for _, b := range empID {
		n = n*31 + uint64(b)
	}
	if n == 0 {
		return -1
	}
	return -int64(n % 1_000_000_000)
}
func exportPenaltyShift(reason string) string {
	if strings.Contains(reason, "夜班") {
		return "night"
	}
	return "day"
}
func (s *WorkforceTelegramService) telegramUserIDForEmployee(ctx context.Context, empID uuid.UUID) int64 {
	var tgUID int64
	_ = s.db.QueryRowContext(ctx, `
		SELECT telegram_user_id FROM employee_telegram_bindings
		WHERE employee_id = $1 AND status = 1
		ORDER BY updated_at DESC NULLS LAST LIMIT 1`, empID).Scan(&tgUID)
	return tgUID
}
func formatShiftForExport(shift string) string {
	switch strings.ToLower(strings.TrimSpace(shift)) {
	case "night", "night_last", "night_tonight":
		return "夜班"
	case "middle", "middle_today", "middle_prev", "mid":
		return "中班"
	default:
		return "白班"
	}
}
func formatExportValue(value int, isTime bool) string {
	if value <= 0 {
		return "-"
	}
	if isTime {
		return formatDurationForExport(value)
	}
	return strconv.Itoa(value)
}
func formatDurationForExport(seconds int) string {
	if seconds <= 0 {
		return "0分0秒"
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	sec := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d时%d分%d秒", h, m, sec)
	}
	if m > 0 {
		return fmt.Sprintf("%d分%d秒", m, sec)
	}
	return fmt.Sprintf("%d秒", sec)
}

// exportSessionDurationSeconds mirrors export SQL duration rules for unit tests.
// Archive / historical open sessions contribute 0; only live open on today uses now.
func exportSessionDurationSeconds(checkin, checkout time.Time, hasCheckout, isLive, isOpen bool, attendanceDate, today string, now time.Time) int {
	if hasCheckout {
		if !checkout.After(checkin) {
			return 0
		}
		return int(checkout.Sub(checkin).Seconds())
	}
	if isLive && isOpen && strings.TrimSpace(attendanceDate) == strings.TrimSpace(today) {
		if !now.After(checkin) {
			return 0
		}
		return int(now.Sub(checkin).Seconds())
	}
	return 0
}

// telegramExportUsePunchTimes：单日导出（含 TG 日重置前导出）展示打卡时刻；跨日/月汇总仍用次数。
func telegramExportUsePunchTimes(dateFrom, dateTo string) bool {
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	if dateFrom == "" || dateTo == "" {
		return true
	}
	return dateFrom == dateTo
}
func formatExportPunchClock(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Format("15:04:05")
}
func telegramExportWorkColumnHeaders(usePunchTimes bool) (startLabel, endLabel string) {
	if usePunchTimes {
		return "上班打卡时间", "下班打卡时间"
	}
	return "上班次数", "下班次数"
}
func telegramExportWorkColumnValues(st tgExportUserStat, usePunchTimes bool) (startVal, endVal string) {
	if usePunchTimes {
		return formatExportPunchClock(st.WorkStartAt), formatExportPunchClock(st.WorkEndAt)
	}
	return formatExportValue(st.WorkStartCount, false), formatExportValue(st.WorkEndCount, false)
}
func exportRowEmpty(row []string) bool {
	for i, v := range row {
		if i <= 2 {
			continue
		}
		if v != "-" {
			return false
		}
	}
	return true
}
func (s *WorkforceTelegramService) writeTelegramStatsWorkbook(f *excelize.File, stats []tgExportUserStat, activityNames []string, usePunchTimes bool) error {
	sheet := "打卡统计"
	_ = f.SetSheetName("Sheet1", sheet)

	startLabel, endLabel := telegramExportWorkColumnHeaders(usePunchTimes)
	headers := []string{"用户ID", "用户昵称", "班次", "工作天数", startLabel, endLabel, "工作时长"}
	for _, act := range activityNames {
		headers = append(headers, act+"次数", act+"总时长")
	}
	headers = append(headers,
		"活动次数总计", "活动用时总计", "超时次数", "超时时长",
		"早退次数", "迟到次数", "下班罚款", "上班罚款", "罚款总金额",
	)

	headerStyle, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"4472C4"}, Pattern: 1},
		Font:      &excelize.Font{Color: "FFFFFF", Bold: true, Size: 11},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	})
	dataStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	})
	fineStyle, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"FFEB9C"}, Pattern: 1},
		Font:      &excelize.Font{Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	})
	noFineStyle, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"C6EFCE"}, Pattern: 1},
		Font:      &excelize.Font{Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	})
	emptyStyle, _ := f.NewStyle(&excelize.Style{
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"FFC7CE"}, Pattern: 1},
		Font:      &excelize.Font{Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	})

	for col, header := range headers {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		_ = f.SetCellValue(sheet, cell, header)
		_ = f.SetCellStyle(sheet, cell, cell, headerStyle)
	}
	_ = f.SetRowHeight(sheet, 1, 21)

	for rowIdx, st := range stats {
		rowNum := rowIdx + 2
		totalFines := formatExportValue(st.TotalFines, false)
		hasFine := totalFines != "-" && totalFines != "0"
		startVal, endVal := telegramExportWorkColumnValues(st, usePunchTimes)

		row := []string{
			strconv.FormatInt(st.UserID, 10),
			st.Nickname,
			formatShiftForExport(st.Shift),
			formatExportValue(st.WorkDays, false),
			startVal,
			endVal,
			formatExportValue(st.WorkHours, true),
		}
		for _, act := range activityNames {
			info := st.Activities[act]
			row = append(row,
				formatExportValue(info.Count, false),
				formatExportValue(info.Time, true),
			)
		}
		row = append(row,
			formatExportValue(st.TotalActivityCount, false),
			formatExportValue(st.TotalAccumulatedTime, true),
			formatExportValue(st.OvertimeCount, false),
			formatExportValue(st.TotalOvertimeTime, true),
			formatExportValue(st.EarlyCount, false),
			formatExportValue(st.LateCount, false),
			formatExportValue(st.WorkEndFines, false),
			formatExportValue(st.WorkStartFines, false),
			totalFines,
		)

		rowStyle := dataStyle
		switch {
		case exportRowEmpty(row):
			rowStyle = emptyStyle
		case hasFine:
			rowStyle = fineStyle
		default:
			rowStyle = noFineStyle
		}

		for col, value := range row {
			cell, _ := excelize.CoordinatesToCellName(col+1, rowNum)
			_ = f.SetCellValue(sheet, cell, value)
			_ = f.SetCellStyle(sheet, cell, cell, rowStyle)
		}
		_ = f.SetRowHeight(sheet, rowNum, 21)
	}

	for col := 1; col <= len(headers); col++ {
		colName, _ := excelize.ColumnNumberToName(col)
		_ = f.SetColWidth(sheet, colName, colName, 12)
	}
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	return nil
}

// ---- Source: backend/services/workforce_tg_fine_currency.go ----
// NormalizeFineCurrency maps settings / API values to a stable ISO-like code.
// Empty or unknown → CNY (keeps existing ¥ / 元 behavior).
func NormalizeFineCurrency(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "THB", "฿", "BAHT":
		return "THB"
	case "USD", "$", "US$":
		return "USD"
	case "CNY", "RMB", "¥", "元", "":
		return "CNY"
	default:
		return "CNY"
	}
}

// FineCurrencySymbol is the prefix used in Web / bot money displays.
func FineCurrencySymbol(code string) string {
	switch NormalizeFineCurrency(code) {
	case "THB":
		return "฿"
	case "USD":
		return "$"
	default:
		return "¥"
	}
}

// FineCurrencyUnit is the Chinese unit suffix (replaces hard-coded「元」).
func FineCurrencyUnit(code string) string {
	switch NormalizeFineCurrency(code) {
	case "THB":
		return "泰铢"
	case "USD":
		return "美元"
	default:
		return "元"
	}
}

// FormatFineMoney formats amount with currency symbol prefix (e.g. ¥12.50 / ฿12.50).
func FormatFineMoney(amount float64, code string) string {
	return fmt.Sprintf("%s%.2f", FineCurrencySymbol(code), amount)
}

// FormatFineAmountWithUnit e.g. "12 元" / "12 泰铢" for Chinese bot sentences.
func FormatFineAmountWithUnit(amount float64, code string) string {
	if amount == float64(int64(amount)) {
		return fmt.Sprintf("%.0f %s", amount, FineCurrencyUnit(code))
	}
	return fmt.Sprintf("%.2f %s", amount, FineCurrencyUnit(code))
}
func (s *WorkforceTelegramService) FineCurrencyCode(ctx context.Context) string {
	if s == nil {
		return "CNY"
	}
	st, err := s.GetSettings(ctx)
	if err != nil || st == nil {
		return "CNY"
	}
	return NormalizeFineCurrency(st.FineCurrency)
}

// ---- Source: backend/services/workforce_tg_group_config.go ----
const (
	defaultCheckInBefore       = 120
	defaultCheckInAfter        = 360
	defaultCheckOutBefore      = 120
	defaultCheckOutAfter       = 360
	defaultLateTolerance       = 0
	defaultEarlyLeaveTolerance = 10
)

// TgGraceConfig separates punch windows from late/early-leave penalty tolerances.
type TgGraceConfig struct {
	CheckInBefore       int
	CheckInAfter        int
	CheckOutBefore      int
	CheckOutAfter       int
	LateTolerance       int
	EarlyLeaveTolerance int
}

func normalizeGraceConfig(cfg TgGraceConfig) TgGraceConfig {
	if cfg.CheckInBefore < 30 {
		cfg.CheckInBefore = defaultCheckInBefore
	}
	if cfg.CheckInAfter < 30 {
		cfg.CheckInAfter = defaultCheckInAfter
	}
	if cfg.CheckOutBefore < 30 {
		cfg.CheckOutBefore = defaultCheckOutBefore
	}
	if cfg.CheckOutAfter < 30 {
		cfg.CheckOutAfter = defaultCheckOutAfter
	}
	if cfg.LateTolerance < 0 {
		cfg.LateTolerance = 0
	}
	if cfg.EarlyLeaveTolerance < 0 {
		cfg.EarlyLeaveTolerance = 0
	}
	return cfg
}
func (s *WorkforceTelegramService) graceFromSettings(settings *TelegramSettings) TgGraceConfig {
	cfg := TgGraceConfig{
		CheckInBefore:       settings.GraceMinutes,
		CheckInAfter:        settings.GraceMinutesAfter,
		CheckOutBefore:      settings.CheckOutGraceBefore,
		CheckOutAfter:       settings.WorkEndGraceAfter,
		LateTolerance:       settings.LateToleranceMinutes,
		EarlyLeaveTolerance: settings.EarlyLeaveToleranceMinutes,
	}
	return normalizeGraceConfig(cfg)
}
func (s *WorkforceTelegramService) GetGraceConfig(ctx context.Context, chatID string) (TgGraceConfig, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return TgGraceConfig{}, err
	}
	cfg := s.graceFromSettings(settings)
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return cfg, nil
	}
	var sb, sa, cb, ca, lt, el sql.NullInt32
	err = s.db.QueryRowContext(ctx, `
		SELECT shift_grace_before, shift_grace_after, work_end_grace_before, work_end_grace_after,
			late_tolerance_minutes, early_leave_tolerance_minutes
		FROM wf_telegram_groups WHERE chat_id = $1`, chatID).
		Scan(&sb, &sa, &cb, &ca, &lt, &el)
	if err == sql.ErrNoRows {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if sb.Valid && int(sb.Int32) > 0 {
		cfg.CheckInBefore = int(sb.Int32)
	}
	if sa.Valid && int(sa.Int32) > 0 {
		cfg.CheckInAfter = int(sa.Int32)
	}
	if cb.Valid && int(cb.Int32) > 0 {
		cfg.CheckOutBefore = int(cb.Int32)
	}
	if ca.Valid && int(ca.Int32) > 0 {
		cfg.CheckOutAfter = int(ca.Int32)
	}
	if lt.Valid && int(lt.Int32) >= 0 {
		cfg.LateTolerance = int(lt.Int32)
	}
	if el.Valid && int(el.Int32) >= 0 {
		cfg.EarlyLeaveTolerance = int(el.Int32)
	}
	return normalizeGraceConfig(cfg), nil
}
func (s *WorkforceTelegramService) GetGroupDailyResetTime(ctx context.Context, chatID string) string {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return "09:00"
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return settings.DailyResetTime
	}
	var t sql.NullString
	if s.db.QueryRowContext(ctx, `
		SELECT daily_reset_time FROM wf_telegram_groups WHERE chat_id = $1`, chatID).Scan(&t) != nil || !t.Valid || strings.TrimSpace(t.String) == "" {
		return settings.DailyResetTime
	}
	return strings.TrimSpace(t.String)
}

// ---- Source: backend/services/workforce_tg_handover_relay.go ----
type pendingWorkShift struct {
	Shift      string
	RecordDate string
	SessionID  uuid.UUID
}

func (s *WorkforceTelegramService) getPendingWorkEndShift(ctx context.Context, chatID string, empID uuid.UUID) (*pendingWorkShift, error) {
	var sid uuid.UUID
	var shift, recordDate string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(shift_type,'day'), attendance_date::text
		FROM work_sessions
		WHERE employee_id = $1 AND status = 'open' AND source = 'telegram'
		ORDER BY checkin_time ASC LIMIT 1`, empID).Scan(&sid, &shift, &recordDate)
	if err == nil {
		return &pendingWorkShift{Shift: shift, RecordDate: recordDate, SessionID: sid}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	err = s.db.QueryRowContext(ctx, `
		SELECT gs.shift_type, gs.record_date::text
		FROM wf_tg_group_shift_state gs
		WHERE gs.chat_id = $1 AND gs.employee_id = $2
		  AND NOT EXISTS (
			SELECT 1 FROM work_sessions ws
			WHERE ws.employee_id = gs.employee_id AND ws.shift_type = gs.shift_type
			  AND ws.attendance_date = gs.record_date AND ws.status = 'closed' AND ws.source = 'telegram'
		  )
		ORDER BY gs.updated_at ASC LIMIT 1`, chatID, empID).Scan(&shift, &recordDate)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pendingWorkShift{Shift: shift, RecordDate: recordDate}, nil
}
func (s *WorkforceTelegramService) upsertRelaySuccessor(ctx context.Context, chatID string, empID uuid.UUID, relayDate, successorDate string) {
	if chatID == "" || empID == uuid.Nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_relay_states (chat_id, employee_id, relay_handover_date, successor_record_date, updated_at)
		VALUES ($1, $2, $3::date, $4::date, NOW())
		ON CONFLICT (chat_id, employee_id) DO UPDATE SET
			relay_handover_date = EXCLUDED.relay_handover_date,
			successor_record_date = EXCLUDED.successor_record_date,
			updated_at = NOW()`,
		chatID, empID, relayDate, successorDate)
}
func (s *WorkforceTelegramService) clearRelaySuccessor(ctx context.Context, chatID string, empID uuid.UUID) {
	if chatID == "" || empID == uuid.Nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM wf_tg_relay_states WHERE chat_id = $1 AND employee_id = $2`, chatID, empID)
}
func isHandoverRelaySuccessor(shift, recordDate, openShift, openDate string, cfg *TgHandoverConfigRow, loc *time.Location) bool {
	if cfg == nil || !cfg.HandoverEnabled {
		return false
	}
	if shift != "day" || openShift != "day" || strings.TrimSpace(recordDate) == "" || strings.TrimSpace(openDate) == "" {
		return false
	}
	if recordDate <= openDate {
		return false
	}
	if loc == nil {
		loc = time.Local
	}
	od, err := time.ParseInLocation("2006-01-02", openDate, loc)
	if err != nil {
		return false
	}
	return isHandoverDate(od, cfg.HandoverDay, cfg.HandoverMonth)
}
func (s *WorkforceTelegramService) getRelaySuccessorDate(ctx context.Context, chatID string, empID uuid.UUID) string {
	var d sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT successor_record_date::text FROM wf_tg_relay_states
		WHERE chat_id = $1 AND employee_id = $2`, chatID, empID).Scan(&d)
	if d.Valid {
		return d.String
	}
	return ""
}
func (s *WorkforceTelegramService) validateRelayNightBlock(ctx context.Context, chatID, shift string, now time.Time) error {
	if shift != "night" {
		return nil
	}
	relay := s.getHandoverDayRelayHandoverDate(ctx, chatID, now)
	if relay == "" {
		return nil
	}
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	dh, _, ok := parseClock(cfg.DayStartTime)
	if !ok {
		dh = 9
	}
	if now.Hour() < dh {
		return fmt.Errorf("清晨不能打夜班卡，请使用「白班上班」进行换班接班")
	}
	return nil
}

// ---- Source: backend/services/workforce_tg_i18n.go ----
type TgLangMode string

const (
	TgLangBoth TgLangMode = "both"
	TgLangZh   TgLangMode = "zh"
	TgLangVi   TgLangMode = "vi"
	TgLangEn   TgLangMode = "en"
)

var tgActivityVI = map[string]string{
	"吃饭":    "Đi ăn",
	"大厕":    "Nhà vệ sinh lớn",
	"小厕":    "Nhà vệ sinh nhỏ",
	"抽烟或休息": "Nghỉ hoặc hút thuốc",
	"抽烟":    "Hút thuốc",
}

// tgDefaultActivityIcons maps canonical activity names to keyboard display icons.
// DB name / API resolution stay unchanged; icons are display-only on buttons.
var tgDefaultActivityIcons = map[string]string{
	"小厕":    "🚾",
	"大厕":    "🚽",
	"吃饭":    "🍱",
	"抽烟":    "🚬",
	"抽烟或休息": "🚬",
}

type tgButtonMeta struct {
	Zh     string
	Vi     string
	En     string
	Style  string
	Legacy []string
}

var tgWorkButtons = map[string]tgButtonMeta{
	"work_start_day":    {Zh: "白班上班", Vi: "Ca ngày Đi làm", En: "Day in", Style: "success"},
	"work_start_middle": {Zh: "中班上班", Vi: "Ca giữa Đi làm", En: "Mid in", Style: "primary"},
	"work_start_night":  {Zh: "夜班上班", Vi: "Ca đêm Đi làm", En: "Night in", Style: "primary"},
	"work_end":          {Zh: "下班", Vi: "Tan ca", En: "Clock out", Style: "danger", Legacy: []string{"🔴 下班"}},
}
var tgUIButtons = map[string]tgButtonMeta{
	"back":        {Zh: "回座", Vi: "Trở lại chỗ ngồi", En: "Back", Style: "success", Legacy: []string{"✅ 回座", "回来", "结束活动", "结束", "回座"}},
	"my_record":   {Zh: "我的记录", Vi: "Lịch sử của tôi", En: "My log", Legacy: []string{"我的信息", "我的记录"}},
	"rank":        {Zh: "排行榜", Vi: "Bảng xếp hạng", En: "Rank", Legacy: []string{"排名"}},
	"export_data": {Zh: "导出数据", Vi: "Xuất dữ liệu", En: "Export", Legacy: []string{"/export"}},
	"checkin":     {Zh: "上班", Vi: "Đi làm", En: "Clock in", Style: "success"},
	"status":      {Zh: "状态", Vi: "Trạng thái", En: "Status"},
	"handover":    {Zh: "交接班", Vi: "Giao ca", En: "Handover", Legacy: []string{"换班"}},
	"help":        {Zh: "帮助", Vi: "Trợ giúp", En: "Help"},
	"chatid":      {Zh: "chatid", Vi: "chatid", En: "chatid"},
}

func normalizeTgLangMode(mode string) TgLangMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "zh":
		return TgLangZh
	case "vi":
		return TgLangVi
	case "en":
		return TgLangEn
	default:
		return TgLangBoth
	}
}
func (s *WorkforceTelegramService) GetLangMode(ctx context.Context, chatID string) TgLangMode {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return TgLangBoth
	}
	mode := normalizeTgLangMode(settings.BotUILang)
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return mode
	}
	if groupLang := s.groupUILang(ctx, chatID); groupLang.Valid && strings.TrimSpace(groupLang.String) != "" {
		return normalizeTgLangMode(groupLang.String)
	}
	return mode
}

// groupUILang reads the group's own bot language once per request. Callers that
// already fetched it as part of another query prime the cache (telegramRequestStore).
func (s *WorkforceTelegramService) groupUILang(ctx context.Context, chatID string) sql.NullString {
	v, _ := telegramRequestRead(ctx, s, "group_ui_lang:"+chatID, func() (sql.NullString, error) {
		var g sql.NullString
		if err := s.db.QueryRowContext(ctx, `
			SELECT bot_ui_lang FROM wf_telegram_groups WHERE chat_id = $1`, chatID).Scan(&g); err != nil {
			return sql.NullString{}, nil
		}
		return g, nil
	})
	return v
}
func tgFormatLabel(zh, vi string, mode TgLangMode) string {
	switch mode {
	case TgLangZh:
		return zh
	case TgLangVi:
		if vi != "" {
			return vi
		}
		return zh
	default:
		if vi != "" && vi != zh {
			return zh + " / " + vi
		}
		return zh
	}
}
func tgActivityLabel(activity string, mode TgLangMode) string {
	return tgFormatLabel(activity, tgActivityVI[activity], mode)
}
func tgDefaultActivityIcon(name string) string {
	return tgDefaultActivityIcons[strings.TrimSpace(name)]
}
func tgActivityKeyboardLabel(activity string, mode TgLangMode) string {
	var base string
	switch mode {
	case TgLangZh, TgLangVi, TgLangEn:
		base = tgActivityDisplayName(activity, mode)
	default:
		base = tgActivityLabel(activity, mode)
	}
	if icon := tgDefaultActivityIcon(activity); icon != "" {
		return icon + " " + base
	}
	return base
}

// stripActivityKeyboardPrefix removes a leading default activity icon from button text.
func stripActivityKeyboardPrefix(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	seen := make(map[string]struct{}, len(tgDefaultActivityIcons))
	for _, icon := range tgDefaultActivityIcons {
		if icon == "" {
			continue
		}
		if _, ok := seen[icon]; ok {
			continue
		}
		seen[icon] = struct{}{}
		if strings.HasPrefix(text, icon+" ") {
			return strings.TrimSpace(text[len(icon)+1:])
		}
	}
	return text
}
func tgButtonLabel(meta tgButtonMeta, mode TgLangMode) string {
	switch mode {
	case TgLangVi:
		if strings.TrimSpace(meta.Vi) != "" {
			return meta.Vi
		}
	case TgLangEn:
		if strings.TrimSpace(meta.En) != "" {
			return meta.En
		}
	case TgLangBoth:
		return tgFormatLabel(meta.Zh, meta.Vi, mode)
	}
	return meta.Zh
}

// Telegram reply-keyboard rows share width by button count, not by text.
// Long bilingual labels wrap and make buttons taller; keep the original
// 3-across layout and ellipsize overflow instead.
const tgKeyboardLabelMaxWidth = 16
const tgKeyboardLabelEllipsis = "..."

func tgRuneDisplayWidth(r rune) int {
	switch {
	case r == 0 || r == '\u200d' || r == '\ufe0e' || r == '\ufe0f':
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
		return 0
	case r < 0x1100:
		return 1
	case r >= 0x2E80 && r <= 0xA4CF:
		return 2
	case r >= 0xAC00 && r <= 0xD7AF:
		return 2
	case r >= 0xF900 && r <= 0xFAFF:
		return 2
	case r >= 0xFE10 && r <= 0xFE19:
		return 2
	case r >= 0xFF00 && r <= 0xFF60:
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	case r >= 0x1F000 && r <= 0x1FFFF:
		return 2
	case r >= 0x2600 && r <= 0x27BF:
		return 2
	default:
		return 1
	}
}
func tgDisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += tgRuneDisplayWidth(r)
	}
	return w
}
func tgFitKeyboardLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || tgDisplayWidth(s) <= tgKeyboardLabelMaxWidth {
		return s
	}
	ellipsisW := tgDisplayWidth(tgKeyboardLabelEllipsis)
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := tgRuneDisplayWidth(r)
		if w+rw+ellipsisW > tgKeyboardLabelMaxWidth {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimRight(out, "/ ")
	if out == "" {
		return tgKeyboardLabelEllipsis
	}
	return out + tgKeyboardLabelEllipsis
}
func tgKeyboardBtn(label, style string) map[string]interface{} {
	btn := map[string]interface{}{"text": tgFitKeyboardLabel(label)}
	if strings.TrimSpace(style) != "" {
		btn["style"] = style
	}
	return btn
}
func tgCollectStyledLabels(meta tgButtonMeta) []string {
	labels := []string{
		strings.TrimSpace(meta.Zh),
		strings.TrimSpace(meta.Vi),
		strings.TrimSpace(meta.En),
		strings.TrimSpace(tgFormatLabel(meta.Zh, meta.Vi, TgLangBoth)),
		strings.TrimSpace(tgButtonLabel(meta, TgLangZh)),
		strings.TrimSpace(tgButtonLabel(meta, TgLangVi)),
		strings.TrimSpace(tgButtonLabel(meta, TgLangEn)),
	}
	for _, legacy := range meta.Legacy {
		if legacy = strings.TrimSpace(legacy); legacy != "" {
			labels = append(labels, legacy)
		}
	}
	extra := make([]string, 0, len(labels))
	for _, label := range labels {
		if fitted := tgFitKeyboardLabel(label); fitted != "" && fitted != label {
			extra = append(extra, fitted)
		}
	}
	return append(labels, extra...)
}
func buildStyledButtonTextLookup() map[string]string {
	lookup := map[string]string{}
	addMeta := func(meta tgButtonMeta) {
		style := strings.TrimSpace(meta.Style)
		if style == "" {
			return
		}
		for _, label := range tgCollectStyledLabels(meta) {
			if label != "" {
				lookup[label] = style
			}
		}
	}
	for _, meta := range tgWorkButtons {
		addMeta(meta)
	}
	for _, meta := range tgUIButtons {
		addMeta(meta)
	}
	return lookup
}
func enrichReplyKeyboardButton(btn map[string]interface{}, lookup map[string]string) {
	text, _ := btn["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if style, ok := lookup[text]; ok {
		btn["style"] = style
	}
}
func enrichReplyKeyboardStyles(kb map[string]interface{}) map[string]interface{} {
	if kb == nil {
		return nil
	}
	lookup := buildStyledButtonTextLookup()
	rawRows, ok := kb["keyboard"]
	if !ok || rawRows == nil {
		return kb
	}
	switch rows := rawRows.(type) {
	case [][]map[string]interface{}:
		for i := range rows {
			for j := range rows[i] {
				enrichReplyKeyboardButton(rows[i][j], lookup)
			}
		}
		kb["keyboard"] = rows
	case []interface{}:
		for _, row := range rows {
			btnRow, ok := row.([]interface{})
			if !ok {
				continue
			}
			for i, btn := range btnRow {
				switch b := btn.(type) {
				case string:
					if style, ok := lookup[strings.TrimSpace(b)]; ok {
						btnRow[i] = map[string]interface{}{"text": b, "style": style}
					}
				case map[string]interface{}:
					enrichReplyKeyboardButton(b, lookup)
				}
			}
		}
	}
	return kb
}
func tgRegisterLookup(lookup map[string]string, canonical, text string) {
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	lookup[text] = canonical
	if fitted := tgFitKeyboardLabel(text); fitted != text {
		lookup[fitted] = canonical
	}
}
func tgRegisterMeta(lookup map[string]string, key string, meta tgButtonMeta, mode TgLangMode) {
	tgRegisterLookup(lookup, key, meta.Zh)
	tgRegisterLookup(lookup, key, meta.Vi)
	tgRegisterLookup(lookup, key, meta.En)
	tgRegisterLookup(lookup, key, tgButtonLabel(meta, TgLangBoth))
	tgRegisterLookup(lookup, key, tgButtonLabel(meta, TgLangEn))
	for _, legacy := range meta.Legacy {
		tgRegisterLookup(lookup, key, legacy)
	}
}
func (s *WorkforceTelegramService) BuildButtonLookup(ctx context.Context, activityNames []string) map[string]string {
	settings, _ := s.GetSettings(ctx)
	mode := normalizeTgLangMode("")
	if settings != nil {
		mode = normalizeTgLangMode(settings.BotUILang)
	}
	lookup := map[string]string{}
	for key, meta := range tgWorkButtons {
		tgRegisterMeta(lookup, key, meta, mode)
	}
	for key, meta := range tgUIButtons {
		tgRegisterMeta(lookup, key, meta, mode)
	}
	for _, act := range activityNames {
		name := strings.TrimSpace(act)
		if name == "" {
			continue
		}
		tgRegisterLookup(lookup, name, name)
		tgRegisterLookup(lookup, name, tgActivityLabel(name, TgLangZh))
		tgRegisterLookup(lookup, name, tgActivityLabel(name, TgLangVi))
		tgRegisterLookup(lookup, name, tgActivityLabel(name, TgLangBoth))
		tgRegisterLookup(lookup, name, tgActivityKeyboardLabel(name, TgLangZh))
		tgRegisterLookup(lookup, name, tgActivityKeyboardLabel(name, TgLangVi))
		tgRegisterLookup(lookup, name, tgActivityKeyboardLabel(name, TgLangEn))
		tgRegisterLookup(lookup, name, tgActivityKeyboardLabel(name, TgLangBoth))
		tgRegisterLookup(lookup, name, tgActivityDisplayName(name, TgLangEn))
		// Keep previous 小厕/大厕 icons clickable until clients refresh the keyboard.
		if name == "小厕" {
			tgRegisterLookup(lookup, name, "🚽 小厕")
		}
		if name == "大厕" {
			tgRegisterLookup(lookup, name, "🚾 大厕")
		}
	}
	// Legacy slash commands
	tgRegisterLookup(lookup, "back", "/at")
	tgRegisterLookup(lookup, "checkin", "/checkin")
	tgRegisterLookup(lookup, "work_end", "/checkout")
	tgRegisterLookup(lookup, "status", "/status")
	tgRegisterLookup(lookup, "my_record", "/myinfo")
	tgRegisterLookup(lookup, "rank", "/rankings")
	tgRegisterLookup(lookup, "handover", "/handover")
	tgRegisterLookup(lookup, "help", "/start")
	tgRegisterLookup(lookup, "help", "/help")
	tgRegisterLookup(lookup, "my_record_day", "/myinfoday")
	tgRegisterLookup(lookup, "my_record_day", "白班信息")
	tgRegisterLookup(lookup, "my_record_night", "/myinfonight")
	tgRegisterLookup(lookup, "my_record_night", "夜班信息")
	tgRegisterLookup(lookup, "rank_day", "/rankingsday")
	tgRegisterLookup(lookup, "rank_day", "白班排行")
	tgRegisterLookup(lookup, "rank_night", "/rankingsnight")
	tgRegisterLookup(lookup, "rank_night", "夜班排行")
	tgRegisterLookup(lookup, "work_start_day", "白班")
	tgRegisterLookup(lookup, "work_start_night", "夜班")
	tgRegisterLookup(lookup, "export_data", "/export")
	tgRegisterLookup(lookup, "export_data", "/exportmonthly")
	tgRegisterLookup(lookup, "export_data", "导出数据")
	tgRegisterLookup(lookup, "actstatus", "/actstatus")
	tgRegisterLookup(lookup, "showsettings", "/showsettings")
	return lookup
}
func localizedMainReplyKeyboard(mode TgLangMode, dualShift, tripleShift bool, activityNames []string) map[string]interface{} {
	return localizedMainReplyKeyboardEx(mode, dualShift, tripleShift, activityNames, true)
}
func localizedMainReplyKeyboardEx(mode TgLangMode, dualShift, tripleShift bool, activityNames []string, showInfoButtons bool) map[string]interface{} {
	return localizedMainReplyKeyboardWithDevices(mode, dualShift, tripleShift, activityNames, nil, showInfoButtons)
}
func localizedMainReplyKeyboardWithDevices(
	mode TgLangMode, dualShift, tripleShift bool, activityNames []string, deviceTags []workstationDeviceTag, showInfoButtons bool,
) map[string]interface{} {
	if len(deviceTags) >= 2 {
		return localizedMultiDeviceReplyKeyboard(mode, dualShift, tripleShift, activityNames, deviceTags, showInfoButtons)
	}
	var rows [][]map[string]interface{}
	if tripleShift {
		// 白班 / 中班 / 夜班 / 下班 — four buttons on one row
		rows = append(rows, []map[string]interface{}{
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_start_day"], mode), tgWorkButtons["work_start_day"].Style),
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_start_middle"], mode), tgWorkButtons["work_start_middle"].Style),
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_start_night"], mode), tgWorkButtons["work_start_night"].Style),
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_end"], mode), tgWorkButtons["work_end"].Style),
		})
	} else if dualShift {
		rows = append(rows, []map[string]interface{}{
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_start_day"], mode), tgWorkButtons["work_start_day"].Style),
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_start_night"], mode), tgWorkButtons["work_start_night"].Style),
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_end"], mode), tgWorkButtons["work_end"].Style),
		})
	} else {
		rows = append(rows, []map[string]interface{}{
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["checkin"], mode), tgUIButtons["checkin"].Style),
			tgKeyboardBtn(tgButtonLabel(tgWorkButtons["work_end"], mode), tgWorkButtons["work_end"].Style),
		})
	}
	displayNames := make([]string, 0, len(activityNames))
	for _, name := range activityNames {
		displayNames = append(displayNames, tgActivityKeyboardLabel(strings.TrimSpace(name), mode))
	}
	rows = append(rows, buildActivityRowsWithBack(mode, displayNames)...)
	if showInfoButtons {
		rows = append(rows, []map[string]interface{}{
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["my_record"], mode), ""),
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["rank"], mode), ""),
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["export_data"], mode), ""),
		})
	}
	return enrichReplyKeyboardStyles(map[string]interface{}{
		"keyboard":          rows,
		"resize_keyboard":   true,
		"is_persistent":     true,
		"one_time_keyboard": false,
	})
}
func localizedMultiDeviceReplyKeyboard(
	mode TgLangMode, dualShift, tripleShift bool, activityNames []string, deviceTags []workstationDeviceTag, showInfoButtons bool,
) map[string]interface{} {
	var rows [][]map[string]interface{}
	appendTaggedRow := func(label, style string) {
		row := make([]map[string]interface{}, 0, len(deviceTags))
		for _, d := range deviceTags {
			row = append(row, tgKeyboardBtn(JoinTgDeviceTag(label, d.Tag), style))
		}
		// Keep rows readable on mobile: wrap every 2 device buttons.
		for i := 0; i < len(row); i += 2 {
			end := i + 2
			if end > len(row) {
				end = len(row)
			}
			rows = append(rows, row[i:end])
		}
	}

	if tripleShift {
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_start_day"], mode), tgWorkButtons["work_start_day"].Style)
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_start_middle"], mode), tgWorkButtons["work_start_middle"].Style)
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_start_night"], mode), tgWorkButtons["work_start_night"].Style)
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_end"], mode), tgWorkButtons["work_end"].Style)
	} else if dualShift {
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_start_day"], mode), tgWorkButtons["work_start_day"].Style)
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_start_night"], mode), tgWorkButtons["work_start_night"].Style)
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_end"], mode), tgWorkButtons["work_end"].Style)
	} else {
		appendTaggedRow(tgButtonLabel(tgUIButtons["checkin"], mode), tgUIButtons["checkin"].Style)
		appendTaggedRow(tgButtonLabel(tgWorkButtons["work_end"], mode), tgWorkButtons["work_end"].Style)
	}

	for _, name := range activityNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		appendTaggedRow(tgActivityKeyboardLabel(name, mode), "")
	}
	appendTaggedRow(tgButtonLabel(tgUIButtons["back"], mode), tgUIButtons["back"].Style)

	if showInfoButtons {
		rows = append(rows, []map[string]interface{}{
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["my_record"], mode), ""),
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["rank"], mode), ""),
			tgKeyboardBtn(tgButtonLabel(tgUIButtons["export_data"], mode), ""),
		})
	}
	// selective: in groups only the replied-to / mentioned user receives this keyboard.
	return enrichReplyKeyboardStyles(map[string]interface{}{
		"keyboard":          rows,
		"resize_keyboard":   true,
		"is_persistent":     true,
		"one_time_keyboard": false,
		"selective":         true,
	})
}

// ---- Source: backend/services/workforce_tg_myinfo.go ----
type TgActivityRankEntry struct {
	Rank         int
	EmployeeName string
	TotalSeconds int
	TotalCount   int
	IsActive     bool
}
type TgActivityRankGroup struct {
	ActivityName string
	ActivityCode string
	Entries      []TgActivityRankEntry
}

func formatDurationZh(seconds int) string {
	return FormatDurationZh(seconds)
}

// FormatDurationZh 将秒数格式化为中文「时分秒」，供 handlers 等包外调用。
func FormatDurationZh(seconds int) string {
	if seconds <= 0 {
		return "0秒"
	}
	m, s := seconds/60, seconds%60
	h := m / 60
	m = m % 60
	if h > 0 {
		return fmt.Sprintf("%d小时%d分%d秒", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%d分%d秒", m, s)
	}
	return fmt.Sprintf("%d秒", s)
}
func (s *WorkforceTelegramService) resolveStatsQuery(ctx context.Context, chatID, shift string, now time.Time) (businessDate string, queryDate string, splitDual bool, nightDate, dayDate string, err error) {
	settings, _ := s.GetSettings(ctx)
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	period, err := s.DetermineCurrentPeriod(ctx, chatID, now)
	if err != nil {
		fallback := now.Format("2006-01-02")
		return fallback, fallback, false, "", "", err
	}
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	dec := float64(now.Hour()) + float64(now.Minute())/60.0
	nh, nm, _ := parseClock(cfg.NightStartTime)
	dh, dm, _ := parseClock(cfg.DayStartTime)
	nightDec := float64(nh) + float64(nm)/60.0
	dayDec := float64(dh) + float64(dm)/60.0
	isNight := dec >= nightDec || dec < dayDec

	if shift == "day" {
		businessDate = resolveAttendanceDate("day", "", period, now, loc)
		return businessDate, businessDate, false, "", "", nil
	}
	if shift == "night" {
		detail := resolveNightShiftDetail(settings, now)
		businessDate = resolveAttendanceDate("night", detail, period, now, loc)
		queryDate = businessDate
		if !isNight {
			t, _ := time.Parse("2006-01-02", businessDate)
			queryDate = t.AddDate(0, 0, -1).Format("2006-01-02")
		}
		return businessDate, queryDate, false, "", "", nil
	}
	if isNight {
		nightBD := resolveAttendanceDate("night", resolveNightShiftDetail(settings, now), period, now, loc)
		dayBD := resolveAttendanceDate("day", "", period, now, loc)
		if dayBD != nightBD {
			return nightBD, nightBD, true, nightBD, dayBD, nil
		}
		return nightBD, nightBD, false, "", "", nil
	}
	businessDate = resolveAttendanceDate("day", "", period, now, loc)
	return businessDate, businessDate, false, "", "", nil
}
func (s *WorkforceTelegramService) ListActivityRankGroups(ctx context.Context, chatID, shift string) ([]TgActivityRankGroup, string, error) {
	types, err := s.ListEnabledActivityTypesForWorker(ctx)
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	businessDate, _, _, nightDate, dayDate, err := s.resolveStatsQuery(ctx, chatID, shift, now)
	if err != nil {
		businessDate = now.Format("2006-01-02")
	}
	settings, _ := s.GetSettings(ctx)
	header := fmt.Sprintf("📅 统计周期：%s\n⏰ 重置：%s", businessDate, settings.DailyResetTime)

	groups := make([]TgActivityRankGroup, 0)
	for _, act := range types {
		dates := []string{businessDate}
		if shift == "" && nightDate != "" && dayDate != "" {
			dates = []string{dayDate, nightDate}
		} else if shift == "night" && nightDate != "" {
			dates = []string{nightDate}
		} else if shift == "day" && dayDate != "" {
			dates = []string{dayDate}
		}

		chatFilter := strings.TrimSpace(chatID)
		rows, err := s.db.QueryContext(ctx, `
			SELECT e.name,
				COALESCE(SUM(CASE WHEN s.status = 'closed' THEN s.elapsed_seconds ELSE EXTRACT(EPOCH FROM (NOW()-s.started_at))::int END),0)::int,
				COUNT(CASE WHEN s.status = 'closed' THEN 1 END)::int,
				BOOL_OR(s.status = 'open') AS is_active
			FROM wf_tg_activity_sessions s
			JOIN employees e ON e.id = s.employee_id
			WHERE s.activity_code = $1 AND s.attendance_date = ANY($2::date[])
			  AND ($3 = '' OR COALESCE(s.source_chat_id,'') = $3
			       OR e.id IN (SELECT employee_id FROM employee_telegram_bindings WHERE chat_id = $3 AND status = 1))
			GROUP BY e.id, e.name
			HAVING BOOL_OR(s.status = 'open') OR COUNT(CASE WHEN s.status = 'closed' THEN 1 END) > 0
			ORDER BY BOOL_OR(s.status = 'open') DESC, 2 DESC, 3 DESC
			LIMIT 15`, act.Code, pq.Array(dates), chatFilter)
		if err != nil {
			continue
		}
		entries := make([]TgActivityRankEntry, 0)
		rank := 0
		for rows.Next() {
			var name string
			var totalSec, count int
			var active bool
			if rows.Scan(&name, &totalSec, &count, &active) != nil {
				continue
			}
			rank++
			entries = append(entries, TgActivityRankEntry{
				Rank: rank, EmployeeName: name, TotalSeconds: totalSec, TotalCount: count, IsActive: active,
			})
		}
		rows.Close()
		if len(entries) > 0 {
			groups = append(groups, TgActivityRankGroup{ActivityName: act.Name, ActivityCode: act.Code, Entries: entries})
		}
	}
	return groups, header, nil
}
func (s *WorkforceTelegramService) FormatActivityRankingsText(groups []TgActivityRankGroup, header, shift string) string {
	if shift == "day" {
		header = "🏆 【白班】活动排行榜\n" + header
	} else if shift == "night" {
		header = "🏆 【夜班】活动排行榜\n" + header
	} else {
		header = "🏆 活动排行榜\n" + header
	}
	if len(groups) == 0 {
		return header + "\n\n暂无排行数据"
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\n")
	for _, g := range groups {
		b.WriteString(fmt.Sprintf("📈 %s：\n", g.ActivityName))
		for _, e := range g.Entries {
			if e.IsActive && e.TotalSeconds == 0 {
				b.WriteString(fmt.Sprintf("  %d. 🟡 %s - 进行中\n", e.Rank, e.EmployeeName))
			} else if e.TotalSeconds > 0 {
				b.WriteString(fmt.Sprintf("  %d. 🟢 %s - %s (%d次)\n", e.Rank, e.EmployeeName, formatDurationZh(e.TotalSeconds), e.TotalCount))
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
func (s *WorkforceTelegramService) BuildRichMyInfo(ctx context.Context, telegramUserID int64, shiftFilter, chatID string) (string, error) {
	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil {
		return "", err
	}
	var empName string
	if strings.TrimSpace(chatID) == "" {
		_ = s.db.QueryRowContext(ctx, `
			SELECT e.name, COALESCE(b.chat_id,'')
			FROM employees e
			LEFT JOIN employee_telegram_bindings b ON b.employee_id = e.id AND b.status = 1
			WHERE e.id = $1`, empID).Scan(&empName, &chatID)
	} else {
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&empName)
	}

	settings, _ := s.GetSettings(ctx)
	now := time.Now()
	businessDate, queryDate, splitDual, nightDate, dayDate, _ := s.resolveStatsQuery(ctx, chatID, shiftFilter, now)
	period, _ := s.DetermineCurrentPeriod(ctx, chatID, now)

	var b strings.Builder
	if shiftFilter == "day" {
		b.WriteString(fmt.Sprintf("👤 %s\n📊 【白班】记录统计\n", empName))
	} else if shiftFilter == "night" {
		b.WriteString(fmt.Sprintf("👤 %s\n📊 【夜班】记录统计\n", empName))
	} else {
		b.WriteString(fmt.Sprintf("👤 %s\n📊 当前周期记录\n", empName))
	}
	b.WriteString(fmt.Sprintf("📅 统计周期：%s\n⏰ 重置时间：%s\n", businessDate, settings.DailyResetTime))
	if period != nil && period.IsHandover {
		b.WriteString(s.handoverStatusHint(ctx, chatID, period, s.GetLangMode(ctx, chatID)) + "\n")
	}
	b.WriteString("\n")

	// work sessions
	wsQuery := `
		SELECT shift_type, checkin_time, checkout_time, status
		FROM work_sessions WHERE employee_id = $1 AND attendance_date = $2::date`
	wsArgs := []interface{}{empID, queryDate}
	if shiftFilter != "" {
		wsQuery += ` AND shift_type = $3`
		wsArgs = append(wsArgs, shiftFilter)
	}
	wsQuery += ` ORDER BY checkin_time ASC`
	wsRows, _ := s.db.QueryContext(ctx, wsQuery, wsArgs...)
	if wsRows != nil {
		defer wsRows.Close()
		hasWork := false
		for wsRows.Next() {
			var sh, st string
			var in time.Time
			var out sql.NullTime
			if wsRows.Scan(&sh, &in, &out, &st) != nil {
				continue
			}
			if !hasWork {
				b.WriteString("🕒 上下班记录\n")
				hasWork = true
			}
			outStr := "进行中"
			if out.Valid {
				outStr = out.Time.Format("15:04:05")
			}
			b.WriteString(fmt.Sprintf("• %s 上班 %s → %s\n", shiftLabel(sh), in.Format("15:04:05"), outStr))
		}
		if hasWork {
			b.WriteString("\n")
		}
	}

	types, _ := s.ListActivityTypes(ctx)
	cycle := 1
	if period != nil {
		cycle = s.effectiveActivityCycle(ctx, chatID, empID, period)
	}

	dates := []string{queryDate}
	if splitDual && shiftFilter == "" {
		dates = []string{dayDate, nightDate}
	}

	b.WriteString("📋 活动统计\n")
	for _, act := range types {
		if !act.Enabled {
			continue
		}
		for _, d := range dates {
			shifts := []string{shiftFilter}
			if shiftFilter == "" {
				shifts = []string{"day", "middle", "night"}
			}
			for _, sh := range shifts {
				if shiftFilter == "" && splitDual {
					if d == dayDate && sh == "night" {
						continue
					}
					if d == nightDate && sh == "day" {
						continue
					}
				}
				count, _ := s.countActivitiesOnDate(ctx, empID, act.Code, d, sh, cycle)
				var totalSec int
				_ = s.db.QueryRowContext(ctx, `
					SELECT COALESCE(SUM(elapsed_seconds),0) FROM wf_tg_activity_sessions
					WHERE employee_id = $1 AND activity_code = $2 AND attendance_date = $3::date AND shift_type = $4 AND status = 'closed'`,
					empID, act.Code, d, sh).Scan(&totalSec)
				limitStr := "不限"
				if act.MaxTimesPerDay > 0 {
					limitStr = fmt.Sprintf("%d", act.MaxTimesPerDay)
				}
				if count > 0 || totalSec > 0 {
					b.WriteString(fmt.Sprintf("• %s(%s) %d/%s 次 · %s\n",
						act.Name, shiftLabel(sh), count, limitStr, formatDurationZh(totalSec)))
				}
			}
		}
	}

	if sess, err := s.getOpenActivitySession(ctx, empID); err == nil {
		b.WriteString(fmt.Sprintf("\n🟡 进行中：%s（%s）\n", sess.ActivityName, formatDurationZh(int(time.Since(sess.StartedAt).Seconds()))))
	}

	var fineTotal float64
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(fine_amount),0) FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND attendance_date = ANY($2::date[]) AND status = 'closed'`,
		empID, pq.Array(dates)).Scan(&fineTotal)
	if fineTotal > 0 {
		b.WriteString(fmt.Sprintf("\n💰 今日活动罚款：%s", FormatFineMoney(fineTotal, s.FineCurrencyCode(ctx))))
	}
	out := strings.TrimSpace(b.String())
	if !strings.Contains(out, "•") && !strings.Contains(out, "🟡") && !strings.Contains(out, "🕒") {
		out += "\n\n暂无记录"
	}
	return out, nil
}

// ---- Source: backend/services/workforce_tg_ops.go ----
const (
	telegramDailyRetentionDays = 30
)

func (s *WorkforceTelegramService) CleanupTelegramRetention(ctx context.Context) error {
	if !TelegramAttendanceEnabled() {
		return nil
	}
	// Live detail rows are normally archived then deleted by daily reset.
	// Only remove orphan live closed activities that are already archived,
	// so export completeness is preserved. Archive / monthly table retention
	// is owned by System Settings → attendanceRetentionMonths.
	cutoff := time.Now().AddDate(0, 0, -telegramDailyRetentionDays).Format("2006-01-02")
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM wf_tg_activity_sessions s
		WHERE s.status = 'closed'
		  AND s.attendance_date < $1::date
		  AND EXISTS (
			SELECT 1 FROM wf_tg_activity_sessions_archive a WHERE a.id = s.id
		  )`, cutoff)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		log.Printf("[Telegram] retention cleanup: removed %d already-archived live activities before %s", n, cutoff)
	}
	return nil
}
func (s *WorkforceTelegramService) MaybePushMonthlyArchive(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return
	}
	loc := tgLoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	// 次月 1 日 17:00 起推送上月月表；前 3 天内可补推，避免服务宕机漏掉。
	if now.Day() > 3 {
		return
	}
	if now.Day() == 1 && now.Hour() < 17 {
		return
	}
	prevMonth := now.AddDate(0, -1, 0)
	yearMonth := prevMonth.Format("2006-01")
	dateFrom := time.Date(prevMonth.Year(), prevMonth.Month(), 1, 0, 0, 0, 0, loc).Format("2006-01-02")
	dateTo := time.Date(prevMonth.Year(), prevMonth.Month()+1, 0, 0, 0, 0, 0, loc).Format("2006-01-02")

	groups, err := s.ListGroups(ctx)
	if err != nil {
		return
	}
	caption := fmt.Sprintf("📊 月度考勤报表\n📅 统计月份：%s\n📆 区间：%s ~ %s\n⏰ 导出时间：%s",
		yearMonth, dateFrom, dateTo, now.Format("2006-01-02 15:04:05"))
	for _, g := range groups {
		if !g.Enabled || strings.TrimSpace(g.ChatID) == "" {
			continue
		}
		chatID := strings.TrimSpace(g.ChatID)
		var exists int
		_ = s.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM wf_tg_monthly_export_log
			WHERE year_month = $1 AND chat_id = $2`, yearMonth, chatID).Scan(&exists)
		if exists > 0 {
			continue
		}
		data, filename, err := s.ExportAttendanceXLSX(ctx, dateFrom, dateTo, chatID)
		if err != nil || len(data) == 0 {
			log.Printf("[Telegram] monthly archive export %s chat=%s: %v", yearMonth, chatID, err)
			continue
		}
		s.PushDocumentToChatTargets(ctx, chatID, filename, data, caption, false)
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO wf_tg_monthly_export_log (year_month, chat_id, export_path, pushed_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (year_month, chat_id) DO NOTHING`, yearMonth, chatID, filename)
		log.Printf("[Telegram] monthly archive pushed %s -> %s", yearMonth, chatID)
	}
}
// RecordWorkerActivityReply keeps the start anchor and latest group reply distinct.
func (s *WorkforceTelegramService) RecordWorkerActivityReply(ctx context.Context, sessionID uuid.UUID, messageID int64, ended bool) error {
	if messageID <= 0 {
		return fmt.Errorf("invalid Telegram message ID")
	}
	// One atomic statement (previously BEGIN + 2 UPDATEs + COMMIT = 4 round trips).
	// The data-modifying CTE always runs; the main UPDATE only reads columns the
	// CTE does not change, so both writes see the same session row.
	_, err := s.db.ExecContext(ctx, `
		WITH anchor AS (
			UPDATE wf_tg_activity_sessions SET telegram_message_id=$2::bigint
			WHERE id=$1 AND NOT $3::boolean RETURNING id
		)
		UPDATE employee_telegram_bindings b SET last_group_bot_message_id=$2::bigint
		FROM wf_tg_activity_sessions s WHERE s.id=$1 AND b.employee_id=s.employee_id AND b.status=1
		AND COALESCE(b.chat_id,'')=s.source_chat_id`, sessionID, messageID, ended)
	return err
}

func (s *WorkforceTelegramService) SetActivityTelegramMessageID(ctx context.Context, sessionID uuid.UUID, messageID int64) {
	if sessionID == uuid.Nil || messageID <= 0 {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		UPDATE wf_tg_activity_sessions SET telegram_message_id = $2 WHERE id = $1`, sessionID, messageID)
}
func (s *WorkforceTelegramService) getEmployeeLastGroupBotMessageID(ctx context.Context, chatID string, empID uuid.UUID) int64 {
	if empID == uuid.Nil || strings.TrimSpace(chatID) == "" {
		return 0
	}
	var msgID sql.NullInt64
	_ = s.db.QueryRowContext(ctx, `
		SELECT last_group_bot_message_id FROM employee_telegram_bindings
		WHERE employee_id = $1 AND status = 1 AND COALESCE(chat_id,'') = $2`,
		empID, strings.TrimSpace(chatID)).Scan(&msgID)
	if msgID.Valid && msgID.Int64 > 0 {
		return msgID.Int64
	}
	return 0
}
func (s *WorkforceTelegramService) setEmployeeLastGroupBotMessageID(ctx context.Context, chatID string, empID uuid.UUID, messageID int64) {
	if empID == uuid.Nil || messageID <= 0 || strings.TrimSpace(chatID) == "" {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		UPDATE employee_telegram_bindings
		SET last_group_bot_message_id = $3
		WHERE employee_id = $1 AND status = 1 AND COALESCE(chat_id,'') = $2`,
		empID, strings.TrimSpace(chatID), messageID)
}
func (s *WorkforceTelegramService) hasOpenNightContinuation(ctx context.Context, empID uuid.UUID, recordDate string) bool {
	t, err := time.Parse("2006-01-02", recordDate)
	if err != nil {
		return false
	}
	next := t.AddDate(0, 0, 1).Format("2006-01-02")
	var count int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM work_sessions
		WHERE employee_id = $1 AND shift_type = 'night' AND status = 'open'
		  AND attendance_date IN ($2::date, $3::date)`, empID, recordDate, next).Scan(&count)
	return count > 0 && time.Now().Hour() < 12
}
func (s *WorkforceTelegramService) hasOpenMiddleContinuation(ctx context.Context, empID uuid.UUID, recordDate string) bool {
	settings, err := s.GetSettings(ctx)
	if err != nil || settings == nil {
		return false
	}
	loc := tgLoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	expectedEnd := resolveWorkEndExpectedTime(now, recordDate, "middle", shiftStartClock(settings, "middle"), shiftEndClock(settings, "middle"), loc)
	if !now.Before(expectedEnd.Add(2 * time.Hour)) {
		return false
	}
	var count int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM work_sessions
		WHERE employee_id = $1 AND shift_type = 'middle' AND status = 'open'
		  AND attendance_date = $2::date`, empID, recordDate).Scan(&count)
	return count > 0
}
func (s *WorkforceTelegramService) shouldPreserveOpenShiftDuringReset(ctx context.Context, empID uuid.UUID, recordDate, shift string) bool {
	switch shift {
	case "night":
		return s.hasOpenNightContinuation(ctx, empID, recordDate)
	case "middle":
		return s.hasOpenMiddleContinuation(ctx, empID, recordDate)
	default:
		return false
	}
}

// ---- Source: backend/services/workforce_tg_p2.go ----
type TgHandoverRow struct {
	ID           uuid.UUID
	EmployeeID   uuid.UUID
	EmployeeName string
	ChatID       string
	FromShift    string
	ToShift      string
	Note         string
	HandoverAt   time.Time
}
type TgRankingRow struct {
	Rank            int
	EmployeeID      uuid.UUID
	EmployeeName    string
	EmployeeCode    string
	ActivityCount   int
	OvertimeSeconds int
	FineTotal       float64
}
type TgDailyResetResult struct {
	ResetDate           string   `json:"resetDate"`
	ExportDate          string   `json:"exportDate"`
	ExportDateNote      string   `json:"exportDateNote,omitempty"`
	ClosedWorkSessions  int      `json:"closedWorkSessions"`
	ClosedAgentSessions int      `json:"closedAgentSessions"`
	ClosedActivities    int      `json:"closedActivities"`
	ExportPath          string   `json:"exportPath,omitempty"`
	ArchivedMonth       string   `json:"archivedMonth,omitempty"`
	BindingWarnings     []string `json:"bindingWarnings,omitempty"`
	SkippedAllGroups    bool     `json:"skippedAllGroups,omitempty"`
	GroupsProcessed     int      `json:"groupsProcessed"`
}

// DailyResetRunOptions 手动日重置可选参数（定时/懒重置不传）。
type DailyResetRunOptions struct {
	// ExportBusinessToday 为 true 时导出各群当前业务日数据，而非默认的「上一统计日」。
	ExportBusinessToday bool
	// ManualTrigger 为 true 表示管理端手动触发；补全下班会保留日期保护，避免白天误关当日在班会话。
	ManualTrigger bool
	// ForcedExportDate 非空时强制使用该统计日导出/清库（补偿重置用）。
	ForcedExportDate string
}

func tgLoadLocation(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}
func parseClock(s string) (hour, minute int, ok bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var h, m int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return 0, 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}
func minutesOfDay(t time.Time) int {
	return t.Hour()*60 + t.Minute()
}
func clockToMinutes(h, m int) int {
	return h*60 + m
}
func oppositeShift(shift string) string {
	if shift == "night" {
		return "day"
	}
	return "night"
}

// nextHandoverShift returns the next shift in dual (day↔night) or triple (day→middle→night→day) rotation.
func nextHandoverShift(fromShift string, triple bool) string {
	from, _ := normalizeShiftType(fromShift)
	if triple {
		switch from {
		case "day":
			return "middle"
		case "middle":
			return "night"
		default:
			return "day"
		}
	}
	return oppositeShift(from)
}
func shiftLabel(shift string) string {
	switch shift {
	case "night":
		return "夜班"
	case "middle":
		return "中班"
	default:
		return "白班"
	}
}

// ShiftLabelPublic returns localized shift label for API responses.
func ShiftLabelPublic(shift string) string {
	return shiftLabel(shift)
}
func (s *WorkforceTelegramService) telegramCheckInWithShift(ctx context.Context, empID uuid.UUID, telegramUserID int64, chatID, forcedShift string, actor *TelegramActor) (*CheckInOutcome, error) {
	release, lockErr := s.acquireAttendanceOperation(ctx, empID)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	return s.telegramCheckInExplicit(ctx, empID, telegramUserID, chatID, forcedShift, actor)
}
func (s *WorkforceTelegramService) TelegramHandover(ctx context.Context, telegramUserID int64, note string) (*CheckInResult, *TgHandoverRow, error) {
	if err := s.rejectIfWebPunchOnly(ctx); err != nil {
		return nil, nil, err
	}
	active, err := s.IsActive(ctx)
	if err != nil || !active {
		return nil, nil, ErrTelegramDisabled
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !settings.MultiShiftEnabled() {
		return nil, nil, ErrDualShiftDisabled
	}

	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil {
		return nil, nil, err
	}
	open, _, err := s.GetEmployeeSessionStatus(ctx, empID)
	if err != nil {
		return nil, nil, err
	}
	if !open {
		return nil, nil, ErrNoOpenSession
	}

	if _, err = s.getOpenActivitySession(ctx, empID); err == nil {
		if _, err = s.TelegramEndActivity(ctx, telegramUserID); err != nil {
			return nil, nil, err
		}
	} else if !errors.Is(err, ErrActivityNotActive) {
		return nil, nil, err
	}

	var fromShift string
	var fromSID uuid.UUID
	_ = s.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(shift_type,'day') FROM work_sessions
		WHERE employee_id = $1 AND status = 'open' ORDER BY checkin_time ASC LIMIT 1`, empID).Scan(&fromSID, &fromShift)

	toShift := nextHandoverShift(fromShift, settings.TripleShiftEnabled)
	loc := tgLoadLocation(settings.Timezone)
	now := punchNow(ctx).In(loc)

	var empName, chatID string
	_ = s.db.QueryRowContext(ctx, `
		SELECT e.name, COALESCE(b.chat_id,'')
		FROM employees e
		LEFT JOIN employee_telegram_bindings b ON b.employee_id = e.id AND b.status = 1
		WHERE e.id = $1`, empID).Scan(&empName, &chatID)

	period, _ := s.DetermineCurrentPeriod(ctx, chatID, now)
	shiftDetail := ""
	if toShift == "night" {
		shiftDetail = resolveNightShiftDetail(settings, now)
	} else if toShift == "middle" {
		shiftDetail = resolveMiddleShiftDetail(settings, now)
	}
	recordDate := resolveAttendanceDate(toShift, shiftDetail, period, now, loc)
	done, err := s.hasCompletedShiftOnDate(ctx, empID, recordDate, toShift)
	if err != nil {
		return nil, nil, err
	}
	if done {
		return nil, nil, ErrShiftAlreadyCompleted
	}

	s.SyncAgentCaptureStop(ctx, empID, fromSID)
	if _, err = s.workforce.CheckOutForEmployee(ctx, empID, "telegram", nil, nil, &CheckOutOptions{ShiftType: fromShift}); err != nil {
		return nil, nil, err
	}

	handoverID := uuid.New()
	row := &TgHandoverRow{
		ID: handoverID, EmployeeID: empID, EmployeeName: empName, ChatID: chatID,
		FromShift: fromShift, ToShift: toShift, Note: strings.TrimSpace(note), HandoverAt: punchNow(ctx),
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_handovers (id, employee_id, employee_name, chat_id, from_shift, to_shift, note)
		VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, NULLIF($7,''))`,
		row.ID, row.EmployeeID, row.EmployeeName, row.ChatID, row.FromShift, row.ToShift, row.Note)
	if err != nil {
		return nil, nil, err
	}

	result, err := s.workforce.CheckInForEmployee(ctx, empID, "telegram", nil, &CheckInOptions{
		ShiftType:   toShift,
		RecordDate:  recordDate,
		ShiftDetail: shiftDetail,
	})
	if err != nil {
		return nil, row, err
	}
	if chatID != "" {
		_ = s.setGroupShiftState(ctx, chatID, empID, toShift, recordDate)
	}
	result.ShiftType = toShift
	s.SyncAgentCaptureStart(ctx, empID, result.SessionID)
	s.SyncAgentTelegramCheckInNotify(ctx, empID, result.SessionID)
	return result, row, nil
}
func (s *WorkforceTelegramService) ListHandovers(ctx context.Context, date, search string, limit int) ([]TgHandoverRow, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	where := "1=1"
	args := []interface{}{}
	argN := 1
	if strings.TrimSpace(date) != "" {
		where += fmt.Sprintf(" AND handover_at::date = $%d::date", argN)
		args = append(args, date)
		argN++
	}
	if strings.TrimSpace(search) != "" {
		where += fmt.Sprintf(" AND (employee_name ILIKE $%d OR note ILIKE $%d)", argN, argN)
		args = append(args, "%"+search+"%")
		argN++
	}
	query := fmt.Sprintf(`
		SELECT id, employee_id, employee_name, COALESCE(chat_id,''), from_shift, to_shift, COALESCE(note,''), handover_at
		FROM wf_tg_handovers WHERE %s ORDER BY handover_at DESC LIMIT %d`, where, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgHandoverRow, 0)
	for rows.Next() {
		var row TgHandoverRow
		if err := rows.Scan(&row.ID, &row.EmployeeID, &row.EmployeeName, &row.ChatID,
			&row.FromShift, &row.ToShift, &row.Note, &row.HandoverAt); err != nil {
			continue
		}
		items = append(items, row)
	}
	return items, nil
}
func (s *WorkforceTelegramService) ListRankings(ctx context.Context, date, metric string, limit int) ([]TgRankingRow, error) {
	return s.ListRankingsForShift(ctx, date, metric, "", "", limit)
}
func (s *WorkforceTelegramService) forceCloseActivityForReset(ctx context.Context, session *TgActivitySession) error {
	empID := session.EmployeeID
	now := punchNow(ctx)
	elapsed := int(now.Sub(session.StartedAt).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}
	overtime := 0
	if session.LimitSeconds > 0 && elapsed > session.LimitSeconds {
		overtime = elapsed - session.LimitSeconds
	}
	fine := computeActivityOvertimeFine(s.loadActivityFineMap(ctx, session.ActivityCode), float64(overtime)/60.0)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM wf_tg_activity_sessions WHERE id=$1 FOR UPDATE`, session.ID).Scan(&status); err != nil {
		return err
	}
	if status != "open" {
		return nil
	}
	var penaltyID sql.NullString
	if fine > 0 {
		pid := uuid.NewString()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO penalty_records (id, employee_id, employee_name, position, amount, category, reason, penalty_date, created_by, created_at)
			SELECT $1, e.id, e.name, COALESCE(e.position,''), $2, 'TG活动', $3, $4::date, 'telegram-reset', NOW()
			FROM employees e WHERE e.id = $5`,
			pid, fine, fmt.Sprintf("%s 日重置超时 %s", session.ActivityName, formatDurationZh(overtime)), session.AttendanceDate, empID)
		if err != nil {
			return err
		}
		penaltyID = sql.NullString{String: pid, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE wf_tg_activity_sessions SET status='closed', auto_ended=TRUE, end_source='system', ended_at=$6, elapsed_seconds=$1,
			overtime_seconds=$2, fine_amount=$3, penalty_record_id=NULLIF($4,'')::uuid
		WHERE id=$5 AND status='open'`,
		elapsed, overtime, fine, nullStringFromNull(penaltyID), session.ID, now)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if fine > 0 {
		s.pushAutoPenaltyNotify(ctx, empID, fine, "TG活动",
			fmt.Sprintf("%s 日重置超时 %s", session.ActivityName, formatDurationZh(overtime)), session.AttendanceDate)
	}
	return nil
}
// ---- Source: backend/services/workforce_tg_p4.go ----
const defaultHandoverResetThresholdHours = 12.0

func handoverThreshold(cfg *TgHandoverConfigRow) float64 {
	if cfg == nil || cfg.HandoverResetThresholdHours <= 0 {
		return defaultHandoverResetThresholdHours
	}
	return cfg.HandoverResetThresholdHours
}

type TgHandoverConfigRow struct {
	ChatID                      string
	HandoverEnabled             bool
	HandoverDay                 int
	HandoverMonth               int
	HandoverDayStartTime        string
	NightStartTime              string
	DayStartTime                string
	HandoverNightHours          int
	HandoverDayHours            int
	NormalNightHours            int
	NormalDayHours              int
	HandoverResetThresholdHours float64
	UpdatedAt                   time.Time
}
type TgWorkFine struct {
	ID             uuid.UUID
	CheckinType    string
	SegmentMinutes int
	FineAmount     float64
}
type TgPeriodInfo struct {
	PeriodType   string
	BusinessDate string
	HandoverDate string
	IsHandover   bool
	Cycle        int
	TotalHours   float64
	HoursElapsed float64
}
type CheckInOutcome struct {
	Result         *CheckInResult
	WorkFine       float64
	HandoverHint   string
	GroupReplyHTML string
}
type CheckOutOutcome struct {
	Result         *CheckOutResult
	WorkFine       float64
	ShiftType      string
	HandoverHint   string
	ActivityWarn   string
	GroupReplyHTML string
}

func normalizeShiftType(shift string) (string, error) {
	raw := strings.TrimSpace(shift)
	shift = strings.ToLower(raw)
	switch shift {
	case "", "day", "白班", "白班上班":
		return "day", nil
	case "middle", "mid", "中班", "中班上班":
		return "middle", nil
	case "night", "夜班", "夜班上班":
		return "night", nil
	default:
		// Chinese labels are unchanged by ToLower; accept raw forms too.
		switch raw {
		case "白班", "白班上班":
			return "day", nil
		case "中班", "中班上班":
			return "middle", nil
		case "夜班", "夜班上班":
			return "night", nil
		}
		return "", fmt.Errorf("invalid shift: %s", shift)
	}
}
func (s *WorkforceTelegramService) loadExtendedSettings(ctx context.Context) (*TelegramSettings, bool, int, bool, error) {
	base, err := s.GetSettings(ctx)
	if err != nil {
		return nil, false, 0, false, err
	}
	return base, base.ShiftWindowDisabled, base.WorkEndGraceMinutes, base.RequirePCLogin, nil
}
func (s *WorkforceTelegramService) defaultHandoverConfig(chatID string) *TgHandoverConfigRow {
	return &TgHandoverConfigRow{
		ChatID: chatID, HandoverEnabled: true, HandoverDay: 0, HandoverMonth: 0,
		HandoverDayStartTime: "15:00", NightStartTime: "21:00", DayStartTime: "09:00",
		HandoverNightHours: 18, HandoverDayHours: 18, NormalNightHours: 12, NormalDayHours: 12,
		HandoverResetThresholdHours: defaultHandoverResetThresholdHours,
	}
}
func (s *WorkforceTelegramService) GetHandoverConfig(ctx context.Context, chatID string) (*TgHandoverConfigRow, error) {
	value, err := telegramRequestRead(ctx, s, "handover:"+chatID, func() (*TgHandoverConfigRow, error) { return s.loadHandoverConfig(ctx, chatID) })
	if err != nil {
		return nil, err
	}
	copy := *value
	return &copy, nil
}
func (s *WorkforceTelegramService) loadHandoverConfig(ctx context.Context, chatID string) (*TgHandoverConfigRow, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return s.defaultHandoverConfig(""), nil
	}
	row := s.defaultHandoverConfig(chatID)
	err := s.db.QueryRowContext(ctx, `
		SELECT chat_id, handover_enabled, handover_day, handover_month,
			handover_day_start_time, night_start_time, day_start_time,
			handover_night_hours, handover_day_hours, normal_night_hours, normal_day_hours,
			handover_reset_threshold_hours, updated_at
		FROM wf_tg_handover_configs WHERE chat_id = $1`, chatID).
		Scan(&row.ChatID, &row.HandoverEnabled, &row.HandoverDay, &row.HandoverMonth,
			&row.HandoverDayStartTime, &row.NightStartTime, &row.DayStartTime,
			&row.HandoverNightHours, &row.HandoverDayHours, &row.NormalNightHours, &row.NormalDayHours,
			&row.HandoverResetThresholdHours, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO wf_tg_handover_configs (chat_id) VALUES ($1) ON CONFLICT (chat_id) DO NOTHING`, chatID)
		return s.defaultHandoverConfig(chatID), nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}
func (s *WorkforceTelegramService) UpsertHandoverConfig(ctx context.Context, chatID string, in map[string]interface{}) (*TgHandoverConfigRow, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("chatId required")
	}
	current, err := s.GetHandoverConfig(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if v, ok := in["handoverEnabled"].(bool); ok {
		current.HandoverEnabled = v
	}
	if v, ok := in["handoverDay"].(float64); ok {
		current.HandoverDay = int(v)
	}
	if v, ok := in["handoverMonth"].(float64); ok {
		current.HandoverMonth = int(v)
	}
	if v, ok := in["handoverDayStartTime"].(string); ok && strings.TrimSpace(v) != "" {
		current.HandoverDayStartTime = strings.TrimSpace(v)
	}
	if v, ok := in["nightStartTime"].(string); ok && strings.TrimSpace(v) != "" {
		current.NightStartTime = strings.TrimSpace(v)
	}
	if v, ok := in["dayStartTime"].(string); ok && strings.TrimSpace(v) != "" {
		current.DayStartTime = strings.TrimSpace(v)
	}
	for _, pair := range []struct {
		key string
		ptr *int
	}{
		{"handoverNightHours", &current.HandoverNightHours},
		{"handoverDayHours", &current.HandoverDayHours},
		{"normalNightHours", &current.NormalNightHours},
		{"normalDayHours", &current.NormalDayHours},
	} {
		if v, ok := in[pair.key].(float64); ok {
			*pair.ptr = int(v)
		}
	}
	if v, ok := in["handoverResetThresholdHours"].(float64); ok && v > 0 {
		current.HandoverResetThresholdHours = v
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_handover_configs (
			chat_id, handover_enabled, handover_day, handover_month,
			handover_day_start_time, night_start_time, day_start_time,
			handover_night_hours, handover_day_hours, normal_night_hours, normal_day_hours,
			handover_reset_threshold_hours, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW())
		ON CONFLICT (chat_id) DO UPDATE SET
			handover_enabled = EXCLUDED.handover_enabled,
			handover_day = EXCLUDED.handover_day,
			handover_month = EXCLUDED.handover_month,
			handover_day_start_time = EXCLUDED.handover_day_start_time,
			night_start_time = EXCLUDED.night_start_time,
			day_start_time = EXCLUDED.day_start_time,
			handover_night_hours = EXCLUDED.handover_night_hours,
			handover_day_hours = EXCLUDED.handover_day_hours,
			normal_night_hours = EXCLUDED.normal_night_hours,
			normal_day_hours = EXCLUDED.normal_day_hours,
			handover_reset_threshold_hours = EXCLUDED.handover_reset_threshold_hours,
			updated_at = NOW()`,
		chatID, current.HandoverEnabled, current.HandoverDay, current.HandoverMonth,
		current.HandoverDayStartTime, current.NightStartTime, current.DayStartTime,
		current.HandoverNightHours, current.HandoverDayHours, current.NormalNightHours, current.NormalDayHours,
		current.HandoverResetThresholdHours)
	if err != nil {
		return nil, err
	}
	return s.GetHandoverConfig(ctx, chatID)
}
func lastDayOfMonth(d time.Time) int {
	return time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, d.Location()).Day()
}
func isHandoverDate(d time.Time, handoverDay, handoverMonth int) bool {
	if handoverMonth != 0 && int(d.Month()) != handoverMonth {
		return false
	}
	last := lastDayOfMonth(d)
	if handoverDay == 0 {
		return d.Day() == last
	}
	day := handoverDay
	if day > last {
		day = last
	}
	return d.Day() == day
}
func combineDateTime(d time.Time, clock string, loc *time.Location) time.Time {
	h, m, ok := parseClock(clock)
	if !ok {
		h, m = 0, 0
	}
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, loc)
}
func (s *WorkforceTelegramService) DetermineCurrentPeriod(ctx context.Context, chatID string, now time.Time) (*TgPeriodInfo, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	cfg, err := s.GetHandoverConfig(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if !cfg.HandoverEnabled {
		return s.buildNormalPeriod(now, cfg, loc)
	}
	currentDate := now
	for _, offset := range []int{0, -1, 1} {
		checkDate := currentDate.AddDate(0, 0, offset)
		if !isHandoverDate(checkDate, cfg.HandoverDay, cfg.HandoverMonth) {
			continue
		}
		if p := s.resolveHandoverPeriodForDate(now, checkDate, cfg, loc); p != nil {
			return p, nil
		}
	}
	return s.buildNormalPeriod(now, cfg, loc)
}
func (s *WorkforceTelegramService) resolveHandoverPeriodForDate(now, handoverDate time.Time, cfg *TgHandoverConfigRow, loc *time.Location) *TgPeriodInfo {
	nightStart := combineDateTime(handoverDate.AddDate(0, 0, -1), cfg.NightStartTime, loc)
	nightEnd := combineDateTime(handoverDate, cfg.HandoverDayStartTime, loc)
	dayStart := nightEnd
	dayEnd := combineDateTime(handoverDate.AddDate(0, 0, 1), cfg.DayStartTime, loc)

	if !now.Before(nightStart) && now.Before(nightEnd) {
		return s.buildHandoverNightPeriod(now, nightStart, handoverDate, cfg)
	}
	if !now.Before(dayStart) && now.Before(dayEnd) {
		return s.buildHandoverDayPeriod(now, dayStart, handoverDate, cfg)
	}
	return nil
}
func (s *WorkforceTelegramService) buildHandoverNightPeriod(now, start time.Time, handoverDate time.Time, cfg *TgHandoverConfigRow) *TgPeriodInfo {
	elapsed := now.Sub(start).Hours()
	total := float64(cfg.HandoverNightHours)
	cycle := 1
	if elapsed >= handoverThreshold(cfg) {
		cycle = 2
	}
	return &TgPeriodInfo{
		PeriodType:   "handover_night",
		BusinessDate: handoverDate.AddDate(0, 0, -1).Format("2006-01-02"),
		HandoverDate: handoverDate.Format("2006-01-02"),
		IsHandover:   true,
		Cycle:        cycle,
		TotalHours:   total,
		HoursElapsed: elapsed,
	}
}
func (s *WorkforceTelegramService) buildHandoverDayPeriod(now, start time.Time, handoverDate time.Time, cfg *TgHandoverConfigRow) *TgPeriodInfo {
	elapsed := now.Sub(start).Hours()
	total := float64(cfg.HandoverDayHours)
	cycle := 1
	if elapsed >= handoverThreshold(cfg) {
		cycle = 2
	}
	return &TgPeriodInfo{
		PeriodType:   "handover_day",
		BusinessDate: handoverDate.Format("2006-01-02"),
		HandoverDate: handoverDate.Format("2006-01-02"),
		IsHandover:   true,
		Cycle:        cycle,
		TotalHours:   total,
		HoursElapsed: elapsed,
	}
}
func (s *WorkforceTelegramService) buildNormalPeriod(now time.Time, cfg *TgHandoverConfigRow, loc *time.Location) (*TgPeriodInfo, error) {
	dec := float64(now.Hour()) + float64(now.Minute())/60.0
	nh, nm, _ := parseClock(cfg.NightStartTime)
	dh, dm, _ := parseClock(cfg.DayStartTime)
	nightDec := float64(nh) + float64(nm)/60.0
	dayDec := float64(dh) + float64(dm)/60.0

	if dec >= nightDec || dec < dayDec {
		var start time.Time
		businessDate := now
		if dec >= nightDec {
			start = combineDateTime(now, cfg.NightStartTime, loc)
		} else {
			businessDate = now.AddDate(0, 0, -1)
			start = combineDateTime(businessDate, cfg.NightStartTime, loc)
		}
		elapsed := now.Sub(start).Hours()
		return &TgPeriodInfo{
			PeriodType:   "normal_night",
			BusinessDate: businessDate.Format("2006-01-02"),
			IsHandover:   false,
			Cycle:        1,
			TotalHours:   float64(cfg.NormalNightHours),
			HoursElapsed: elapsed,
		}, nil
	}
	start := combineDateTime(now, cfg.DayStartTime, loc)
	return &TgPeriodInfo{
		PeriodType:   "normal_day",
		BusinessDate: now.Format("2006-01-02"),
		IsHandover:   false,
		Cycle:        1,
		TotalHours:   float64(cfg.NormalDayHours),
		HoursElapsed: now.Sub(start).Hours(),
	}, nil
}
func (s *WorkforceTelegramService) telegramCheckInExplicit(ctx context.Context, empID uuid.UUID, telegramUserID int64, chatID, forcedShift string, actor *TelegramActor) (*CheckInOutcome, error) {
	settings, shiftWindowDisabled, _, requirePCLogin, err := s.loadExtendedSettings(ctx)
	if err != nil {
		return nil, err
	}
	grace, _ := s.GetGraceConfig(ctx, chatID)
	loc := tgLoadLocation(settings.Timezone)
	now := punchNow(ctx).In(loc)

	if err := s.validatePCLoginForEmployee(ctx, empID, requirePCLogin, actor); err != nil {
		return nil, err
	}
	if _, err := s.closeExpiredWorkSessionsForEmployee(ctx, empID, chatID); err != nil {
		log.Printf("[Telegram] close expired shifts emp=%s: %v", empID, err)
	}

	period, err := s.DetermineCurrentPeriod(ctx, chatID, now)
	if err != nil {
		return nil, err
	}

	shift, shiftDetail, err := s.resolveShiftForCheckIn(settings, grace, shiftWindowDisabled, forcedShift, now)
	if err != nil {
		return nil, err
	}
	if err := s.validateRelayNightBlock(ctx, chatID, shift, now); err != nil {
		return nil, err
	}

	recordDate := resolveAttendanceDate(shift, shiftDetail, period, now, loc)

	if err := s.validateNoConflictingOpenWorkSession(ctx, chatID, empID, shift, shiftDetail, recordDate); err != nil {
		return nil, err
	}

	done, err := s.hasCompletedShiftOnDate(ctx, empID, recordDate, shift)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, ErrShiftAlreadyCompleted
	}

	if err := s.autoEndActivityIfCrossShift(ctx, empID, shift, recordDate); err != nil {
		return nil, err
	}

	startClock := s.resolveWorkStartClock(ctx, chatID, settings, shift, recordDate)
	lateMin := minutesLateFromExpected(now, resolveWorkStartExpectedTime(now, recordDate, shift, shiftDetail, startClock, loc), grace.LateTolerance)
	fineReason := fmt.Sprintf("%s上班迟到 %d 分钟", shiftLabel(shift)+shiftDetailLabel(shiftDetail), lateMin)

	result, err := s.workforce.CheckInForEmployee(ctx, empID, resolvePunchSource(actor, PunchSourceTelegram), nil, &CheckInOptions{
		ShiftType:   shift,
		RecordDate:  recordDate,
		ShiftDetail: shiftDetail,
		LateMinutes: lateMin,
	})
	if err != nil {
		return nil, err
	}

	workFine := 0.0
	empName := ""
	_ = s.db.QueryRowContext(ctx, `SELECT name FROM employees WHERE id = $1`, empID).Scan(&empName)
	createdReview, _ := s.maybeCreateLateCheckinReviewTask(ctx, empID, shift, recordDate, chatID, lateMin, empName)
	if !createdReview {
		workFine, _ = s.applyWorkFine(ctx, empID, "work_start", lateMin, recordDate, fineReason)
	}
	if chatID != "" {
		_ = s.setGroupShiftState(ctx, chatID, empID, shift, recordDate)
	}
	result.ShiftType = shift
	s.syncAgentWorkInFromTelegram(ctx, empID, result.SessionID, actor)

	activitySec := s.sumTodayActivitySeconds(ctx, empID, recordDate, shift)

	replyLang := s.GetReplyLangForEmployee(ctx, empID)
	hint := s.handoverStatusHint(ctx, chatID, period, replyLang)
	hint += s.buildRelaySuccessorHintAfterCheckIn(ctx, empID, chatID, shift, recordDate, now, replyLang)
	expected := resolveWorkStartExpectedTime(now, recordDate, shift, shiftDetail, startClock, loc)
	groupReply := buildWorkStartGroupReplyHTML(
		replyLang, telegramUserID, empName, shift, shiftDetail, now, expected, lateMin, workFine, strings.TrimSpace(hint),
		s.FineCurrencyCode(ctx),
	)
	if createdReview {
		groupReply += "\n\n" + attendanceReviewPendingHint("late", replyLang)
	}
	s.NotifyWorkExceptionPushHTML(ctx, chatID, empID, empName, shiftLabel(shift)+shiftDetailLabel(shiftDetail), "上班", workFine, lateMin, 0, activitySec, groupReply, "")
	return &CheckInOutcome{
		Result:         result,
		WorkFine:       workFine,
		HandoverHint:   strings.TrimSpace(hint),
		GroupReplyHTML: groupReply,
	}, nil
}
func (s *WorkforceTelegramService) validatePCLoginForEmployee(ctx context.Context, empID uuid.UUID, requirePCLogin bool, actor *TelegramActor) error {
	// Web-only mode: admin console punch must not require PC Agent login.
	if actor != nil && actor.PunchSource == PunchSourceWeb && s.IsWebPunchOnly(ctx) {
		return nil
	}
	if actor != nil && actor.Channel == TelegramChannelWorkstation {
		if actor.DeviceID == uuid.Nil {
			return ErrPCLoginRequired
		}
		loggedIn, err := s.resolveWorkstationLoggedInEmployee(ctx, actor.DeviceID)
		if err != nil {
			return err
		}
		if loggedIn != empID {
			return ErrPCLoginMismatch
		}
		return nil
	}
	needPC, err := s.employeeRequiresPCLogin(ctx, empID, requirePCLogin)
	if err != nil {
		return err
	}
	if !needPC {
		return nil
	}
	deviceID, err := s.findDeviceForEmployee(ctx, empID)
	if err != nil || deviceID == uuid.Nil {
		return ErrPCLoginRequired
	}
	var loggedIn uuid.UUID
	var sessionAt sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT employee_uuid, employee_session_at FROM devices WHERE id = $1`, deviceID).
		Scan(&loggedIn, &sessionAt)
	if err != nil || !sessionAt.Valid || loggedIn == uuid.Nil {
		return ErrPCLoginRequired
	}
	if loggedIn != empID {
		return ErrPCLoginMismatch
	}
	return nil
}
func (s *WorkforceTelegramService) findDeviceForEmployee(ctx context.Context, empID uuid.UUID) (uuid.UUID, error) {
	var deviceID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM devices
		WHERE employee_uuid = $1 AND LOWER(status) = 'approved' AND employee_session_at IS NOT NULL
		ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
	if err == nil && deviceID != uuid.Nil {
		return deviceID, nil
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT m.device_id FROM wf_shared_workstation_members m
		JOIN devices d ON d.id = m.device_id AND LOWER(d.status) = 'approved'
		WHERE m.employee_id = $1
		ORDER BY d.last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
	if err == nil && deviceID != uuid.Nil {
		return deviceID, nil
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM devices
		WHERE employee_uuid = $1 AND LOWER(status) = 'approved'
		ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`, empID).Scan(&deviceID)
	if err == sql.ErrNoRows {
		return uuid.Nil, ErrWorkstationNotConfigured
	}
	return deviceID, err
}
func (s *WorkforceTelegramService) setGroupShiftState(ctx context.Context, chatID string, empID uuid.UUID, shift, recordDate string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || empID == uuid.Nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_group_shift_state (chat_id, employee_id, shift_type, record_date, updated_at)
		VALUES ($1, $2, $3, $4::date, NOW())
		ON CONFLICT (chat_id, employee_id, shift_type) DO UPDATE SET
			record_date = EXCLUDED.record_date, updated_at = NOW()`,
		chatID, empID, shift, recordDate)
	return err
}
func (s *WorkforceTelegramService) clearGroupShiftState(ctx context.Context, chatID string, empID uuid.UUID, shift string) {
	if chatID == "" || empID == uuid.Nil {
		return
	}
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM wf_tg_group_shift_state WHERE chat_id = $1 AND employee_id = $2 AND shift_type = $3`,
		chatID, empID, shift)
}
func (s *WorkforceTelegramService) loadWorkFineMap(ctx context.Context, checkinType string) map[int]float64 {
	rows, err := s.db.QueryContext(ctx, `
		SELECT segment_minutes, fine_amount FROM wf_tg_work_fines WHERE checkin_type = $1 ORDER BY segment_minutes`, checkinType)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make(map[int]float64)
	for rows.Next() {
		var seg int
		var amt float64
		if rows.Scan(&seg, &amt) == nil {
			out[seg] = amt
		}
	}
	return out
}
// truncateToClockMinute drops seconds/nanos so late/early matches displayed HH:mm clock difference.
func truncateToClockMinute(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
}
func minutesLateFromExpected(now, expected time.Time, grace int) int {
	now = now.In(expected.Location())
	deadline := expected.Add(time.Duration(grace) * time.Minute)
	nowMin := truncateToClockMinute(now)
	deadlineMin := truncateToClockMinute(deadline)
	if !nowMin.After(deadlineMin) {
		return 0
	}
	late := int(nowMin.Sub(deadlineMin) / time.Minute)
	if late < 0 {
		return 0
	}
	return late
}
func minutesEarlyFromExpected(now, expectedEnd time.Time, grace int) int {
	now = now.In(expectedEnd.Location())
	allowed := expectedEnd.Add(-time.Duration(grace) * time.Minute)
	nowMin := truncateToClockMinute(now)
	allowedMin := truncateToClockMinute(allowed)
	if !nowMin.Before(allowedMin) {
		return 0
	}
	early := int(allowedMin.Sub(nowMin) / time.Minute)
	if early < 0 {
		return 0
	}
	return early
}
func (s *WorkforceTelegramService) applyWorkFine(ctx context.Context, empID uuid.UUID, checkinType string, lateMinutes int, penaltyDate, reason string) (float64, error) {
	if lateMinutes <= 0 {
		return 0, nil
	}
	fineMap := s.loadWorkFineMap(ctx, checkinType)
	amount := computeActivityOvertimeFine(fineMap, float64(lateMinutes))
	if amount <= 0 {
		return 0, nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO penalty_records (id, employee_id, employee_name, position, amount, category, reason, penalty_date, created_by, created_at)
		SELECT $1, e.id, e.name, COALESCE(e.position,''), $2, 'TG考勤', $3, $4::date, 'telegram', NOW()
		FROM employees e WHERE e.id = $5`,
		uuid.NewString(), amount, reason, penaltyDate, empID)
	if err != nil {
		return amount, err
	}
	s.pushAutoPenaltyNotify(ctx, empID, amount, "TG考勤", reason, penaltyDate)
	return amount, nil
}
func (s *WorkforceTelegramService) hasCompletedShiftOnDate(ctx context.Context, empID uuid.UUID, date, shift string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM work_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date AND shift_type = $3 AND status = 'closed'`,
		empID, date, shift).Scan(&count)
	return count > 0, err
}
func (s *WorkforceTelegramService) getHandoverDayRelayHandoverDate(ctx context.Context, chatID string, now time.Time) string {
	period, err := s.DetermineCurrentPeriod(ctx, chatID, now)
	if err != nil || period == nil || period.PeriodType != "handover_day" || period.HandoverDate == "" {
		return ""
	}
	settings, _ := s.GetSettings(ctx)
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	if now.Format("2006-01-02") <= period.HandoverDate {
		return ""
	}
	return period.HandoverDate
}
func checkoutSourceForForce(sessionSource, finePrefix string) string {
	if strings.TrimSpace(finePrefix) != "" {
		return CheckoutSourceSystem
	}
	source := strings.TrimSpace(sessionSource)
	if source == "" {
		return CheckoutSourceSystem
	}
	return source
}
func (s *WorkforceTelegramService) forceCheckoutWorkSessionAt(ctx context.Context, sessionID uuid.UUID, chatID string, checkoutAt time.Time, finePrefix string) error {
	return s.forceCheckoutWorkSessionAtWithOptions(ctx, sessionID, chatID, checkoutAt, finePrefix, false)
}

// forceCheckoutWorkSessionAtWithOptions closes an open work session at checkoutAt.
// When skipFine is true (e.g. employee daily reset), no early-leave fine or review task is created.
func (s *WorkforceTelegramService) forceCheckoutWorkSessionAtWithOptions(ctx context.Context, sessionID uuid.UUID, chatID string, checkoutAt time.Time, finePrefix string, skipFine bool) error {
	var empID uuid.UUID
	var shift, recordDate, sessionSource string
	var checkin time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT employee_id, COALESCE(shift_type,'day'), attendance_date::text, checkin_time, COALESCE(source,'')
		FROM work_sessions WHERE id = $1 AND status = 'open'`, sessionID).
		Scan(&empID, &shift, &recordDate, &checkin, &sessionSource)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	settings, _, _, _, err := s.loadExtendedSettings(ctx)
	if err != nil {
		return err
	}
	grace, _ := s.GetGraceConfig(ctx, chatID)
	loc := tgLoadLocation(settings.Timezone)
	checkoutAt = checkoutAt.In(loc)
	if skipFine {
		// Never write a checkout before the actual check-in or in the future.
		now := punchNow(ctx).In(loc)
		if checkoutAt.After(now) {
			checkoutAt = now
		}
		if checkoutAt.Before(checkin) {
			checkoutAt = checkin
		}
	}

	expectedEnd := s.expectedWorkEnd(ctx, chatID, settings, shift, recordDate, checkoutAt)
	earlyMin := minutesEarlyFromExpected(checkoutAt, expectedEnd, grace.EarlyLeaveTolerance)
	fineReason := fmt.Sprintf("%s下班早退 %d 分钟", shiftLabel(shift), earlyMin)
	if p := strings.TrimSpace(finePrefix); p != "" {
		fineReason = p + " " + fineReason
	}

	yearMonth := recordDate[:7]
	if len(recordDate) < 7 {
		yearMonth = checkoutAt.Format("2006-01")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, `
		UPDATE work_sessions SET status = 'closed', checkout_time = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'open'`, sessionID, checkoutAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE attendance_records SET
			status = CASE
				WHEN manually_edited THEN status
				WHEN status IN ('leave','absent','rest_full','rest_half','off_post','resigned') THEN status
				ELSE 'work' END,
			checkout_time = CASE WHEN manually_edited THEN checkout_time ELSE $4 END,
			checkout_source = CASE
				WHEN manually_edited THEN checkout_source
				WHEN $5 != '' THEN $5
				ELSE checkout_source END,
			shift_type = CASE
				WHEN manually_edited THEN shift_type
				WHEN COALESCE(NULLIF(TRIM($6), ''), '') = '' THEN shift_type
				ELSE $6 END,
			activity_seconds = CASE
				WHEN manually_edited THEN activity_seconds
				ELSE COALESCE((
					SELECT SUM(a.elapsed_seconds)::int
					FROM wf_tg_activity_sessions a
					WHERE a.employee_id = $1 AND a.attendance_date = $3::date AND a.status = 'closed'
				), 0) END,
			updated_at = NOW()
		WHERE employee_id = $1 AND year_month = $2 AND date = $3`,
		empID, yearMonth, recordDate, checkoutAt, checkoutSourceForForce(sessionSource, finePrefix), shift); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}

	workFine := 0.0
	if !skipFine {
		createdReview, _ := s.maybeCreateAttendanceReviewTask(ctx, empID, shift, recordDate, chatID, checkin.In(loc), checkoutAt, earlyMin, s.employeeNameByID(ctx, empID))
		if !createdReview {
			workFine, _ = s.applyWorkFine(ctx, empID, "work_end", earlyMin, recordDate, fineReason)
		}
	}
	s.SyncAgentCaptureStop(ctx, empID, sessionID)
	s.SyncAgentTelegramCheckOutNotify(ctx, empID, sessionID)
	s.SyncAgentWorkSessionState(ctx, empID, false, sessionID, sessionSource)
	if chatID != "" {
		// The same employee may already have started this shift on a newer business date.
		_, _ = s.db.ExecContext(ctx, `DELETE FROM wf_tg_group_shift_state WHERE chat_id=$1 AND employee_id=$2 AND shift_type=$3 AND record_date=$4::date`, chatID, empID, shift, recordDate)
		s.maybeBroadcastShiftEnd(ctx, chatID, shift, recordDate)
	}
	if workFine > 0 {
		log.Printf("[Telegram] force checkout session=%s fine=%.2f", sessionID, workFine)
	}
	return nil
}
func (s *WorkforceTelegramService) telegramCheckOutFIFO(ctx context.Context, empID uuid.UUID, telegramUserID int64, chatID string, actor *TelegramActor) (*CheckOutOutcome, error) {
	release, lockErr := s.acquireAttendanceOperation(ctx, empID)
	if lockErr != nil {
		return nil, lockErr
	}
	defer release()
	settings, shiftWindowDisabled, _, _, err := s.loadExtendedSettings(ctx)
	if err != nil {
		return nil, err
	}
	grace, _ := s.GetGraceConfig(ctx, chatID)
	loc := tgLoadLocation(settings.Timezone)
	now := punchNow(ctx).In(loc)

	openSID := s.openWorkSessionIDFIFO(ctx, empID)
	if openSID == uuid.Nil {
		return nil, ErrNoOpenSession
	}

	var shift, recordDate string
	var checkin time.Time
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day'), attendance_date::text, checkin_time FROM work_sessions WHERE id = $1`, openSID).
		Scan(&shift, &recordDate, &checkin)

	if err := s.validateCheckOutWindow(ctx, chatID, settings, grace, shiftWindowDisabled, shift, recordDate, now); err != nil {
		return nil, err
	}

	replyLang := s.GetReplyLangForEmployee(ctx, empID)
	activityWarn := s.buildCrossShiftActivityWarn(ctx, empID, shift, recordDate, replyLang)

	expectedEnd := s.expectedWorkEnd(ctx, chatID, settings, shift, recordDate, now)
	earlyMin := minutesEarlyFromExpected(now, expectedEnd, grace.EarlyLeaveTolerance)
	fineReason := fmt.Sprintf("%s下班早退 %d 分钟", shiftLabel(shift), earlyMin)

	result, err := s.workforce.CheckOutForEmployee(ctx, empID, resolvePunchSource(actor, PunchSourceTelegram), nil, &openSID, &CheckOutOptions{ShiftType: shift})
	if err != nil {
		return nil, err
	}
	empName := s.employeeNameByID(ctx, empID)
	workFine := 0.0
	workSec := int(math.Max(0, now.Sub(checkin).Seconds()))
	createdReview, _ := s.maybeCreateAttendanceReviewTask(ctx, empID, shift, recordDate, chatID, checkin, now, earlyMin, empName)
	if !createdReview {
		workFine, _ = s.applyWorkFine(ctx, empID, "work_end", earlyMin, recordDate, fineReason)
	}

	s.syncAgentWorkOutFromTelegram(ctx, empID, openSID, actor, resolvePunchSource(actor, PunchSourceTelegram))
	if chatID != "" {
		s.clearGroupShiftState(ctx, chatID, empID, shift)
		s.maybeBroadcastShiftEnd(ctx, chatID, shift, recordDate)
	}
	if result != nil {
		result.ShiftType = shift
	}

	activitySec := s.sumTodayActivitySeconds(ctx, empID, recordDate, shift)
	if period, err := s.DetermineCurrentPeriod(ctx, chatID, now); err == nil {
		s.bumpHandoverCycleWork(ctx, chatID, empID, period, workSec)
	}
	handoverHint := s.buildRelayCheckoutHint(ctx, chatID, empID, shift, recordDate, now, replyLang)
	groupReply := buildWorkEndGroupReplyHTML(
		replyLang, telegramUserID, empName, shift, now, expectedEnd, earlyMin, workFine, handoverHint, activityWarn,
		s.FineCurrencyCode(ctx),
	)
	if createdReview {
		groupReply += "\n\n" + attendanceReviewPendingHint("early", replyLang)
	}
	s.NotifyWorkExceptionPushHTML(ctx, chatID, empID, empName, shiftLabel(shift), "下班", workFine, earlyMin, workSec, activitySec, groupReply, "")
	return &CheckOutOutcome{
		Result:         result,
		WorkFine:       workFine,
		ShiftType:      shift,
		HandoverHint:   handoverHint,
		ActivityWarn:   activityWarn,
		GroupReplyHTML: groupReply,
	}, nil
}
func (s *WorkforceTelegramService) openWorkSessionIDFIFO(ctx context.Context, empID uuid.UUID) uuid.UUID {
	var sid uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM work_sessions WHERE employee_id = $1 AND status = 'open'
		ORDER BY checkin_time ASC LIMIT 1`, empID).Scan(&sid)
	if err != nil {
		return uuid.Nil
	}
	return sid
}
func (s *WorkforceTelegramService) GetEmployeeOpenShifts(ctx context.Context, empID uuid.UUID) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT shift_type FROM work_sessions WHERE employee_id = $1 AND status = 'open' ORDER BY shift_type`, empID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shifts := make([]string, 0)
	for rows.Next() {
		var sh string
		if rows.Scan(&sh) == nil {
			shifts = append(shifts, sh)
		}
	}
	return shifts, nil
}
func (s *WorkforceTelegramService) ListWorkFines(ctx context.Context) ([]TgWorkFine, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, checkin_type, segment_minutes, fine_amount FROM wf_tg_work_fines ORDER BY checkin_type, segment_minutes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgWorkFine, 0)
	for rows.Next() {
		var row TgWorkFine
		if rows.Scan(&row.ID, &row.CheckinType, &row.SegmentMinutes, &row.FineAmount) == nil {
			items = append(items, row)
		}
	}
	return items, nil
}
func (s *WorkforceTelegramService) UpsertWorkFine(ctx context.Context, checkinType string, segmentMinutes int, fineAmount float64) (*TgWorkFine, error) {
	checkinType = strings.TrimSpace(checkinType)
	if checkinType == "" || segmentMinutes <= 0 {
		return nil, fmt.Errorf("invalid work fine rule")
	}
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO wf_tg_work_fines (checkin_type, segment_minutes, fine_amount)
		VALUES ($1, $2, $3)
		ON CONFLICT (checkin_type, segment_minutes) DO UPDATE SET fine_amount = EXCLUDED.fine_amount
		RETURNING id`, checkinType, segmentMinutes, fineAmount).Scan(&id)
	if err != nil {
		return nil, err
	}
	items, err := s.ListWorkFines(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range items {
		if f.ID == id {
			return &f, nil
		}
	}
	return nil, fmt.Errorf("work fine not found")
}

type TgWorkFineBatchItem struct {
	SegmentMinutes int
	FineAmount     float64
}

func (s *WorkforceTelegramService) BatchUpsertWorkFines(ctx context.Context, checkinTypes []string, items []TgWorkFineBatchItem) ([]TgWorkFine, error) {
	types := make([]string, 0, len(checkinTypes))
	seenType := map[string]struct{}{}
	for _, t := range checkinTypes {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t != "work_start" && t != "work_end" {
			return nil, fmt.Errorf("invalid checkin type: %s", t)
		}
		if _, ok := seenType[t]; ok {
			continue
		}
		seenType[t] = struct{}{}
		types = append(types, t)
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("checkin type required")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("at least one rule required")
	}

	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.SegmentMinutes <= 0 {
			return nil, fmt.Errorf("segment minutes must be positive")
		}
		if _, dup := seen[item.SegmentMinutes]; dup {
			return nil, fmt.Errorf("duplicate segment minutes in batch: %d", item.SegmentMinutes)
		}
		seen[item.SegmentMinutes] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	out := make([]TgWorkFine, 0, len(types)*len(items))
	for _, checkinType := range types {
		for _, item := range items {
			var id uuid.UUID
			err := tx.QueryRowContext(ctx, `
				INSERT INTO wf_tg_work_fines (checkin_type, segment_minutes, fine_amount)
				VALUES ($1, $2, $3)
				ON CONFLICT (checkin_type, segment_minutes) DO UPDATE SET fine_amount = EXCLUDED.fine_amount
				RETURNING id`, checkinType, item.SegmentMinutes, item.FineAmount).Scan(&id)
			if err != nil {
				return nil, err
			}
			out = append(out, TgWorkFine{
				ID: id, CheckinType: checkinType,
				SegmentMinutes: item.SegmentMinutes, FineAmount: item.FineAmount,
			})
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *WorkforceTelegramService) DeleteWorkFine(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM wf_tg_work_fines WHERE id = $1`, id)
	return err
}
func (s *WorkforceTelegramService) effectiveActivityCycle(ctx context.Context, chatID string, empID uuid.UUID, period *TgPeriodInfo) int {
	if period == nil || !period.IsHandover {
		return 1
	}
	if period.Cycle >= 2 {
		return 2
	}
	var totalSec int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(total_work_seconds,0) FROM wf_tg_handover_cycles
		WHERE chat_id = $1 AND employee_id = $2 AND handover_date = $3::date
		  AND shift_type = $4 AND cycle_number = 1`,
		chatID, empID, period.HandoverDate, shiftFromPeriod(period.PeriodType)).Scan(&totalSec)
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	if totalSec >= int(handoverThreshold(cfg)*3600) {
		return 2
	}
	return 1
}
func shiftFromPeriod(periodType string) string {
	if strings.Contains(periodType, "night") {
		return "night"
	}
	return "day"
}
func (s *WorkforceTelegramService) bumpHandoverCycleWork(ctx context.Context, chatID string, empID uuid.UUID, period *TgPeriodInfo, elapsedSec int) {
	if period == nil || !period.IsHandover || elapsedSec <= 0 || chatID == "" {
		return
	}
	shift := shiftFromPeriod(period.PeriodType)
	cycle := s.effectiveActivityCycle(ctx, chatID, empID, period)
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_handover_cycles (chat_id, employee_id, handover_date, shift_type, cycle_number, total_work_seconds)
		VALUES ($1,$2,$3::date,$4,$5,$6)
		ON CONFLICT (chat_id, employee_id, handover_date, shift_type, cycle_number)
		DO UPDATE SET total_work_seconds = wf_tg_handover_cycles.total_work_seconds + EXCLUDED.total_work_seconds, updated_at = NOW()`,
		chatID, empID, period.HandoverDate, shift, cycle, elapsedSec)
}
func (s *WorkforceTelegramService) getOpenWorkSessionAttendance(ctx context.Context, empID uuid.UUID) (shift, shiftDetail, recordDate string, ok bool) {
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day'), COALESCE(shift_detail,''), attendance_date::text
		FROM work_sessions
		WHERE employee_id = $1 AND status = 'open'
		ORDER BY checkin_time DESC LIMIT 1`, empID).Scan(&shift, &shiftDetail, &recordDate)
	if err != nil {
		return "", "", "", false
	}
	return shift, shiftDetail, recordDate, true
}
func (s *WorkforceTelegramService) validateNoConflictingOpenWorkSession(ctx context.Context, chatID string, empID uuid.UUID, shift, shiftDetail, recordDate string) error {
	openShift, openDetail, openDate, hasOpen := s.getOpenWorkSessionAttendance(ctx, empID)
	if !hasOpen {
		return nil
	}
	if openShift == shift && openDate == recordDate {
		return nil
	}
	if strings.TrimSpace(chatID) != "" {
		cfg, _ := s.GetHandoverConfig(ctx, chatID)
		loc := time.Local
		if settings, err := s.GetSettings(ctx); err == nil {
			loc = tgLoadLocation(settings.Timezone)
		}
		if isHandoverRelaySuccessor(shift, recordDate, openShift, openDate, cfg, loc) {
			return nil
		}
	}
	return &otherWorkSessionStillOpenError{Shift: openShift, Detail: openDetail, Date: openDate}
}
func (s *WorkforceTelegramService) resolveActivityBusinessDate(ctx context.Context, chatID string, empID uuid.UUID, shift string) (string, *TgPeriodInfo, int, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		settings = &TelegramSettings{Timezone: "Asia/Shanghai"}
	}
	loc := tgLoadLocation(settings.Timezone)
	now := punchNow(ctx).In(loc)

	if successor := s.getRelaySuccessorDate(ctx, chatID, empID); successor != "" {
		period, perr := s.DetermineCurrentPeriod(ctx, chatID, now)
		cycle := 1
		if period != nil {
			cycle = s.effectiveActivityCycle(ctx, chatID, empID, period)
		}
		if perr != nil {
			return successor, period, cycle, perr
		}
		return successor, period, cycle, nil
	}

	if openShift, openDetail, openDate, hasOpen := s.getOpenWorkSessionAttendance(ctx, empID); hasOpen {
		period, _ := s.DetermineCurrentPeriod(ctx, chatID, now)
		cycle := 1
		if period != nil {
			cycle = s.effectiveActivityCycle(ctx, chatID, empID, period)
		}
		if strings.TrimSpace(shift) == "" {
			shift = openShift
		}
		_ = openDetail
		return openDate, period, cycle, nil
	}

	period, err := s.DetermineCurrentPeriod(ctx, chatID, now)
	if err != nil {
		return now.Format("2006-01-02"), nil, 1, err
	}
	if strings.TrimSpace(shift) == "" {
		shift = "day"
	}
	shiftDetail := ""
	if shift == "night" {
		shiftDetail = resolveNightShiftDetail(settings, now)
	}
	cycle := s.effectiveActivityCycle(ctx, chatID, empID, period)
	return resolveAttendanceDate(shift, shiftDetail, period, now, loc), period, cycle, nil
}
func (s *WorkforceTelegramService) countActivitiesOnDate(ctx context.Context, empID uuid.UUID, activityCode, date, shift string, cycle int) (int, error) {
	var count int
	query := `
		SELECT COUNT(*) FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND activity_code = $2 AND attendance_date = $3::date AND status = 'closed'`
	args := []interface{}{empID, activityCode, date}
	if shift != "" {
		query += ` AND shift_type = $4`
		args = append(args, shift)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, err
	}
	if cycle >= 2 {
		return 0, nil
	}
	return count, nil
}
func (s *WorkforceTelegramService) getOpenWorkShift(ctx context.Context, empID uuid.UUID) (string, error) {
	var shift string
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day') FROM work_sessions
		WHERE employee_id = $1 AND status = 'open' ORDER BY checkin_time DESC LIMIT 1`, empID).Scan(&shift)
	if err == sql.ErrNoRows {
		return "", ErrNoOpenSession
	}
	return shift, err
}
func (s *WorkforceTelegramService) GetMyInfoForShift(ctx context.Context, telegramUserID int64, shiftFilter, chatID string) (*TgMyInfoSummary, error) {
	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(chatID) == "" {
		_ = s.db.QueryRowContext(ctx, `
			SELECT COALESCE(chat_id,'') FROM employee_telegram_bindings WHERE employee_id = $1 AND status = 1`, empID).Scan(&chatID)
	}

	shiftFilter = strings.TrimSpace(strings.ToLower(shiftFilter))
	info, err := s.GetMyInfo(ctx, telegramUserID)
	if err != nil {
		return nil, err
	}
	if shiftFilter == "" {
		return info, nil
	}

	openShifts, _ := s.GetEmployeeOpenShifts(ctx, empID)
	info.WorkSessionOpen = false
	for _, sh := range openShifts {
		if sh == shiftFilter {
			info.WorkSessionOpen = true
			break
		}
	}

	businessDate, _, cycle, _ := s.resolveActivityBusinessDate(ctx, chatID, empID, shiftFilter)
	rows, err := s.db.QueryContext(ctx, `
		SELECT activity_name, COUNT(*), COALESCE(SUM(elapsed_seconds),0), COALESCE(SUM(fine_amount),0)
		FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date AND status = 'closed' AND shift_type = $3
		GROUP BY activity_name ORDER BY activity_name`, empID, businessDate, shiftFilter)
	if err == nil {
		defer rows.Close()
		info.TodayActivities = nil
		info.TodayFineTotal = 0
		for rows.Next() {
			var actName string
			var count, totalSec int
			var fineTotal float64
			if rows.Scan(&actName, &count, &totalSec, &fineTotal) == nil {
				if cycle >= 2 {
					count = 0
				}
				info.TodayActivities = append(info.TodayActivities, map[string]interface{}{
					"name": actName, "count": count, "totalSeconds": totalSec, "fineAmount": fineTotal,
					"shift": shiftFilter, "shiftLabel": shiftLabel(shiftFilter),
				})
				info.TodayFineTotal += fineTotal
			}
		}
	}
	info.ShiftFilter = shiftFilter
	info.ShiftLabel = shiftLabel(shiftFilter)
	return info, nil
}
func (s *WorkforceTelegramService) sumTodayActivitySeconds(ctx context.Context, empID uuid.UUID, recordDate, shift string) int {
	var total int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(elapsed_seconds),0)::int FROM wf_tg_activity_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date AND shift_type = $3 AND status = 'closed'`,
		empID, recordDate, shift).Scan(&total)
	return total
}
func (s *WorkforceTelegramService) maybeBroadcastShiftEnd(ctx context.Context, chatID, shift, recordDate string) {
	if chatID == "" {
		return
	}
	var remain int
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM wf_tg_group_shift_state
		WHERE chat_id = $1 AND shift_type = $2 AND record_date = $3::date`, chatID, shift, recordDate).Scan(&remain)
	if remain > 0 {
		return
	}
	msg := fmt.Sprintf("📢 %s %s 全员已下班", recordDate, shiftLabel(shift))
	_ = s.SendTelegramText(ctx, chatID, msg, nil)
}
func (s *WorkforceTelegramService) ListRankingsForShift(ctx context.Context, date, metric, shift, chatID string, limit int) ([]TgRankingRow, error) {
	if limit <= 0 {
		limit = 20
	}
	if strings.TrimSpace(date) == "" {
		date = punchNow(ctx).Format("2006-01-02")
	}
	metric = strings.TrimSpace(metric)
	if metric == "" {
		metric = "fine"
	}
	orderBy := "fine_total DESC, activity_count DESC"
	switch metric {
	case "activity":
		orderBy = "activity_count DESC, fine_total DESC"
	case "overtime":
		orderBy = "overtime_seconds DESC, fine_total DESC"
	}
	where := "s.attendance_date = $1::date AND s.status = 'closed'"
	args := []interface{}{date}
	argN := 2
	if strings.TrimSpace(chatID) != "" {
		where += fmt.Sprintf(" AND COALESCE(s.source_chat_id,'') = $%d", argN)
		args = append(args, chatID)
		argN++
	}
	if strings.TrimSpace(shift) != "" {
		where += fmt.Sprintf(" AND s.shift_type = $%d", argN)
		args = append(args, shift)
		argN++
	}
	// Live ∪ archive：日切会把已关闭会话归档并删活表，历史日排行必须读归档。
	query := fmt.Sprintf(`
		SELECT e.id, e.name, COALESCE(e.employee_id,''),
			COUNT(s.id)::int AS activity_count,
			COALESCE(SUM(s.overtime_seconds),0)::int AS overtime_seconds,
			COALESCE(SUM(s.fine_amount),0) AS fine_total
		FROM (
			SELECT DISTINCT ON (id)
				id, employee_id, attendance_date, overtime_seconds, fine_amount, status,
				COALESCE(source_chat_id,'') AS source_chat_id, shift_type, is_live
			FROM (
				SELECT id, employee_id, attendance_date, overtime_seconds, fine_amount, status,
					source_chat_id, shift_type, TRUE AS is_live
				FROM wf_tg_activity_sessions
				UNION ALL
				SELECT id, employee_id, attendance_date, overtime_seconds, fine_amount, status,
					source_chat_id, shift_type, FALSE AS is_live
				FROM wf_tg_activity_sessions_archive
			) u
			ORDER BY id, is_live DESC
		) s
		JOIN employees e ON e.id = s.employee_id
		WHERE %s
		GROUP BY e.id, e.name, e.employee_id
		ORDER BY %s
		LIMIT %d`, where, orderBy, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TgRankingRow, 0)
	rank := 0
	for rows.Next() {
		var row TgRankingRow
		if rows.Scan(&row.EmployeeID, &row.EmployeeName, &row.EmployeeCode,
			&row.ActivityCount, &row.OvertimeSeconds, &row.FineTotal) != nil {
			continue
		}
		rank++
		row.Rank = rank
		items = append(items, row)
	}
	return items, nil
}

// ---- Source: backend/services/workforce_tg_push.go ----
var (
	pushDedupMu    sync.Mutex
	pushDedupCache = map[string]time.Time{}
)

type TgPushSettings struct {
	ChatID              string
	ChannelID           string
	NotificationGroupID string
	ExtraWorkGroupID    string
	EnableChannelPush   bool
	EnableGroupPush     bool
	EnableAdminPush     bool
	UpdatedAt           time.Time
}

func (s *WorkforceTelegramService) GetPushSettings(ctx context.Context, chatID string) (*TgPushSettings, error) {
	chatID = strings.TrimSpace(chatID)
	out := &TgPushSettings{ChatID: chatID, EnableChannelPush: true, EnableGroupPush: true}
	if chatID == "" {
		return out, nil
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT chat_id, COALESCE(channel_id,''), COALESCE(notification_group_id,''), COALESCE(extra_work_group_id,''),
			enable_channel_push, enable_group_push, enable_admin_push, updated_at
		FROM wf_tg_push_settings WHERE chat_id = $1`, chatID).
		Scan(&out.ChatID, &out.ChannelID, &out.NotificationGroupID, &out.ExtraWorkGroupID,
			&out.EnableChannelPush, &out.EnableGroupPush, &out.EnableAdminPush, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	return out, err
}
func (s *WorkforceTelegramService) UpsertPushSettings(ctx context.Context, chatID string, in map[string]interface{}) (*TgPushSettings, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("chatId required")
	}
	cur, _ := s.GetPushSettings(ctx, chatID)
	if v, ok := in["channelId"].(string); ok {
		cur.ChannelID = strings.TrimSpace(v)
	}
	if v, ok := in["notificationGroupId"].(string); ok {
		cur.NotificationGroupID = strings.TrimSpace(v)
	}
	if v, ok := in["extraWorkGroupId"].(string); ok {
		cur.ExtraWorkGroupID = strings.TrimSpace(v)
	}
	if v, ok := in["enableChannelPush"].(bool); ok {
		cur.EnableChannelPush = v
	}
	if v, ok := in["enableGroupPush"].(bool); ok {
		cur.EnableGroupPush = v
	}
	if v, ok := in["enableAdminPush"].(bool); ok {
		cur.EnableAdminPush = v
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_push_settings (chat_id, channel_id, notification_group_id, extra_work_group_id,
			enable_channel_push, enable_group_push, enable_admin_push, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		ON CONFLICT (chat_id) DO UPDATE SET
			channel_id = EXCLUDED.channel_id,
			notification_group_id = EXCLUDED.notification_group_id,
			extra_work_group_id = EXCLUDED.extra_work_group_id,
			enable_channel_push = EXCLUDED.enable_channel_push,
			enable_group_push = EXCLUDED.enable_group_push,
			enable_admin_push = EXCLUDED.enable_admin_push,
			updated_at = NOW()`,
		chatID, nullIfEmpty(cur.ChannelID), nullIfEmpty(cur.NotificationGroupID), nullIfEmpty(cur.ExtraWorkGroupID),
		cur.EnableChannelPush, cur.EnableGroupPush, cur.EnableAdminPush)
	if err != nil {
		return nil, err
	}
	return s.GetPushSettings(ctx, chatID)
}
func nullIfEmpty(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
func (s *WorkforceTelegramService) listAdminTelegramUserIDs(ctx context.Context) []int64 {
	ids := make([]int64, 0)
	var raw sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(admin_telegram_user_ids,'') FROM wf_telegram_settings ORDER BY updated_at DESC LIMIT 1`).Scan(&raw)
	if raw.Valid {
		ids = append(ids, parseTelegramIDList(raw.String)...)
	}
	if env := strings.TrimSpace(os.Getenv("TELEGRAM_ADMIN_USER_IDS")); env != "" {
		ids = append(ids, parseTelegramIDList(env)...)
	}
	return dedupeInt64(ids)
}
func parseTelegramIDList(raw string) []int64 {
	out := make([]int64, 0)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.ParseInt(part, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}
func dedupeInt64(items []int64) []int64 {
	seen := make(map[int64]struct{}, len(items))
	out := make([]int64, 0, len(items))
	for _, v := range items {
		if v == 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

type telegramSendMessageResult struct {
	MessageID int64 `json:"message_id"`
}

func (s *WorkforceTelegramService) telegramAPI(ctx context.Context, method string, payload map[string]interface{}) error {
	_, err := s.telegramAPIWithResult(ctx, method, payload)
	return err
}
func (s *WorkforceTelegramService) telegramAPIWithResult(ctx context.Context, method string, payload map[string]interface{}) (*telegramSendMessageResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	token, err := s.GetBotToken(ctx)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("bot token not configured")
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK          bool                       `json:"ok"`
		Description string                     `json:"description"`
		Result      *telegramSendMessageResult `json:"result"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if !parsed.OK {
		if parsed.Description == "" {
			parsed.Description = string(raw)
		}
		return nil, fmt.Errorf("telegram %s: %s", method, parsed.Description)
	}
	return parsed.Result, nil
}
func (s *WorkforceTelegramService) SendTelegramHTML(ctx context.Context, chatID, htmlText string, replyMarkup interface{}) error {
	return s.SendTelegramHTMLReply(ctx, chatID, htmlText, 0, replyMarkup)
}
func (s *WorkforceTelegramService) SendTelegramHTMLReply(ctx context.Context, chatID, htmlText string, replyTo int64, replyMarkup interface{}) error {
	_, err := s.SendTelegramHTMLReplyReturnID(ctx, chatID, htmlText, replyTo, replyMarkup)
	return err
}
func (s *WorkforceTelegramService) SendTelegramHTMLReplyReturnID(ctx context.Context, chatID, htmlText string, replyTo int64, replyMarkup interface{}) (int64, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || strings.TrimSpace(htmlText) == "" {
		return 0, nil
	}
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       htmlText,
		"parse_mode": "HTML",
	}
	if replyTo > 0 {
		payload["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	result, err := s.telegramAPIWithResult(ctx, "sendMessage", payload)
	if err != nil {
		return 0, err
	}
	if result == nil {
		return 0, nil
	}
	return result.MessageID, nil
}

// RemoveTelegramInlineKeyboard removes buttons from an existing Telegram
// message without changing its text.
func (s *WorkforceTelegramService) RemoveTelegramInlineKeyboard(
	ctx context.Context, chatID string, messageID int64,
) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || messageID <= 0 {
		return nil
	}
	return s.telegramAPI(ctx, "editMessageReplyMarkup", map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"reply_markup": map[string]interface{}{
			"inline_keyboard": [][]interface{}{},
		},
	})
}
func (s *WorkforceTelegramService) SendTelegramText(ctx context.Context, chatID string, text string, replyMarkup interface{}) error {
	return s.SendTelegramTextReply(ctx, chatID, text, 0, replyMarkup)
}
func (s *WorkforceTelegramService) SendTelegramTextReply(ctx context.Context, chatID string, text string, replyTo int64, replyMarkup interface{}) error {
	return s.sendTelegramTextDedup(ctx, chatID, text, replyTo, replyMarkup, "")
}
func (s *WorkforceTelegramService) sendTelegramTextDedup(ctx context.Context, chatID, text string, replyTo int64, replyMarkup interface{}, dedupKey string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	if dedupKey == "" {
		dedupKey = chatID + "|" + text
	}
	pushDedupMu.Lock()
	if exp, ok := pushDedupCache[dedupKey]; ok && exp.After(time.Now()) {
		pushDedupMu.Unlock()
		return nil
	}
	pushDedupCache[dedupKey] = time.Now().Add(60 * time.Second)
	pushDedupMu.Unlock()
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if replyTo > 0 {
		payload["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	return s.telegramAPI(ctx, "sendMessage", payload)
}
func (s *WorkforceTelegramService) SendTelegramDocument(ctx context.Context, chatID, filename string, data []byte, caption string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" || len(data) == 0 {
		return nil
	}
	token, err := s.GetBotToken(ctx)
	if err != nil || strings.TrimSpace(token) == "" {
		return fmt.Errorf("bot token not configured")
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", chatID)
	if strings.TrimSpace(caption) != "" {
		_ = w.WriteField("caption", caption)
	}
	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err = part.Write(data); err != nil {
		return err
	}
	_ = w.Close()

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
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
		return fmt.Errorf("telegram sendDocument: %s", parsed.Description)
	}
	return nil
}
func (s *WorkforceTelegramService) pushWithAdminFallback(ctx context.Context, ps *TgPushSettings, sourceChatID string, send func(target string) error) {
	if ps == nil {
		return
	}
	sourceChatID = strings.TrimSpace(sourceChatID)
	shouldSkip := func(target string) bool {
		return sourceChatID != "" && strings.TrimSpace(target) == sourceChatID
	}
	sent := false
	if ps.EnableChannelPush && ps.ChannelID != "" && !shouldSkip(ps.ChannelID) {
		if err := send(ps.ChannelID); err == nil {
			sent = true
		} else {
			log.Printf("[TelegramPush] channel %s failed: %v", ps.ChannelID, err)
		}
	}
	if ps.EnableGroupPush && ps.NotificationGroupID != "" && !shouldSkip(ps.NotificationGroupID) {
		if err := send(ps.NotificationGroupID); err == nil {
			sent = true
		} else {
			log.Printf("[TelegramPush] notification group %s failed: %v", ps.NotificationGroupID, err)
		}
	}
	if !sent && ps.EnableAdminPush {
		for _, adminID := range s.listAdminTelegramUserIDs(ctx) {
			target := fmt.Sprintf("%d", adminID)
			if shouldSkip(target) {
				continue
			}
			if err := send(target); err == nil {
				break
			} else {
				log.Printf("[TelegramPush] admin %s failed: %v", target, err)
			}
		}
	}
}
func (s *WorkforceTelegramService) PushToChatTargets(ctx context.Context, chatID, text string, includeExtraWork bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	sourceChat := strings.TrimSpace(chatID)
	ps, err := s.GetPushSettings(ctx, sourceChat)
	if err != nil {
		return
	}
	s.pushWithAdminFallback(ctx, ps, sourceChat, func(target string) error {
		return s.SendTelegramText(ctx, target, text, nil)
	})
	if includeExtraWork && ps.ExtraWorkGroupID != "" && strings.TrimSpace(ps.ExtraWorkGroupID) != sourceChat {
		_ = s.SendTelegramText(ctx, ps.ExtraWorkGroupID, text, nil)
	}
}
func (s *WorkforceTelegramService) pushHTMLToChatTargets(ctx context.Context, chatID, htmlText string, includeExtraWork bool) {
	if strings.TrimSpace(htmlText) == "" {
		return
	}
	sourceChat := strings.TrimSpace(chatID)
	ps, err := s.GetPushSettings(ctx, sourceChat)
	if err != nil {
		return
	}
	s.pushWithAdminFallback(ctx, ps, sourceChat, func(target string) error {
		return s.SendTelegramHTML(ctx, target, htmlText, nil)
	})
	if includeExtraWork && ps.ExtraWorkGroupID != "" && strings.TrimSpace(ps.ExtraWorkGroupID) != sourceChat {
		_ = s.SendTelegramHTML(ctx, ps.ExtraWorkGroupID, htmlText, nil)
	}
}
func (s *WorkforceTelegramService) PushDocumentToChatTargets(ctx context.Context, chatID, filename string, data []byte, caption string, includeExtraWork bool) {
	if len(data) == 0 {
		return
	}
	ps, err := s.GetPushSettings(ctx, chatID)
	if err != nil {
		return
	}
	s.pushWithAdminFallback(ctx, ps, chatID, func(target string) error {
		return s.SendTelegramDocument(ctx, target, filename, data, caption)
	})
	if includeExtraWork && ps.ExtraWorkGroupID != "" && strings.TrimSpace(ps.ExtraWorkGroupID) != strings.TrimSpace(chatID) {
		_ = s.SendTelegramDocument(ctx, ps.ExtraWorkGroupID, filename, data, caption)
	}
}
// NotifyWorkExceptionPushHTML sends late/early/fine alerts to channel & notification group
// using the same HTML template as the check-in group reply.
func (s *WorkforceTelegramService) NotifyWorkExceptionPushHTML(
	ctx context.Context, chatID string, empID uuid.UUID, employeeName, shiftLabel, event string,
	workFine float64, exceptionMinutes int,
	workSeconds, activitySeconds int,
	htmlBody, sourcePrefix string,
) {
	if chatID == "" || (workFine <= 0 && exceptionMinutes <= 0) {
		return
	}
	msg := strings.TrimSpace(htmlBody)
	if msg == "" {
		s.NotifyWorkExceptionPush(ctx, chatID, empID, employeeName, shiftLabel, event, workFine, exceptionMinutes, workSeconds, activitySeconds, "")
		return
	}
	if prefix := strings.TrimSpace(sourcePrefix); prefix != "" {
		msg = prefix + "\n" + msg
	}
	s.pushHTMLToChatTargets(ctx, chatID, msg, true)
	if s.agentNotify != nil {
		s.agentNotify.PushAttendanceException(empID, employeeName, shiftLabel, event, workFine, exceptionMinutes)
	}
}

// NotifyWorkExceptionPush sends late/early/fine alerts to channel & notification group.
// Normal on-time check-in/out is answered in the check-in group by the bot.
func (s *WorkforceTelegramService) NotifyWorkExceptionPush(
	ctx context.Context, chatID string, empID uuid.UUID, employeeName, shift, event string,
	workFine float64, exceptionMinutes int,
	workSeconds, activitySeconds int, shiftDetail string,
) {
	if chatID == "" || (workFine <= 0 && exceptionMinutes <= 0) {
		return
	}
	label := shiftLabel(shift) + shiftDetailLabel(shiftDetail)
	msg := fmt.Sprintf("🚨 %s %s · %s", employeeName, event, label)
	if exceptionMinutes > 0 {
		if event == "上班" {
			msg += fmt.Sprintf("\n⏰ 迟到 %d 分钟", exceptionMinutes)
		} else {
			msg += fmt.Sprintf("\n⏰ 早退 %d 分钟", exceptionMinutes)
		}
	}
	if workSeconds > 0 {
		msg += fmt.Sprintf("\n⏱ 在岗时长：%s", formatDurationZh(workSeconds))
		if activitySeconds > 0 {
			net := workSeconds - activitySeconds
			if net < 0 {
				net = 0
			}
			msg += fmt.Sprintf("\n📊 活动 %s · 净工时 %s", formatDurationZh(activitySeconds), formatDurationZh(net))
		}
	}
	if workFine > 0 {
		msg += fmt.Sprintf("\n⚠️ 罚款 %s", FormatFineMoney(workFine, s.FineCurrencyCode(ctx)))
	}
	s.PushToChatTargets(ctx, chatID, msg, true)
	if s.agentNotify != nil {
		s.agentNotify.PushAttendanceException(empID, employeeName, label, event, workFine, exceptionMinutes)
	}
}
func (s *WorkforceTelegramService) NotifyActivityOvertimeBack(
	ctx context.Context, chatID string, empID uuid.UUID, employeeName, activity string, telegramUserID int64,
	overtimeSec int, fine float64, backAt time.Time,
) {
	if chatID == "" || overtimeSec <= 0 {
		return
	}
	userID, username := lookupEmployeeTelegramProfile(ctx, s.db, empID.String(), employeeName)
	if telegramUserID > 0 {
		if userID != telegramUserID {
			username = ""
		}
		userID = telegramUserID
	}
	cur := s.FineCurrencyCode(ctx)
	msg := buildActivityOvertimeBackPushHTML(s.getChatTitle(ctx, chatID), userID, username, employeeName, activity, backAt, overtimeSec, fine, cur)
	s.pushHTMLToChatTargets(ctx, chatID, msg, false)
	if s.agentNotify != nil {
		extra := fmt.Sprintf("超时时长：%s", formatDurationZh(overtimeSec))
		if fine > 0 {
			extra += fmt.Sprintf("\n罚款 %s", FormatFineMoney(fine, cur))
		}
		s.agentNotify.PushActivityEvent(empID, employeeName, activity, "超时回座", extra, uuid.Nil)
	}
}
func (s *WorkforceTelegramService) NotifyActivityEvent(ctx context.Context, chatID string, empID uuid.UUID, employeeName, activityName, event string, extra string) {
	if chatID == "" && empID == uuid.Nil {
		return
	}
	// 即将超时仅在打卡群/私信提醒，不推送到额外频道或通知群
	if event == "即将超时" {
		s.syncAgentActivityNotify(empID, employeeName, activityName, event, extra, uuid.Nil)
		return
	}
	switch event {
	case "强制回座", "超时自动结束":
	default:
		return
	}
	msg := fmt.Sprintf(
		"🚨 <b>活动超时通知</b>\n🏢 群组：<code>%s</code>\n%s\n👤 %s\n📝 活动：<code>%s</code>\n📌 %s",
		htmlPackage2.EscapeString(defaultChatTitle(s.getChatTitle(ctx, chatID))),
		tgReplyDashedLine(),
		htmlPackage2.EscapeString(employeeName),
		htmlPackage2.EscapeString(activityName),
		htmlPackage2.EscapeString(strings.TrimSpace(extra)),
	)
	s.pushHTMLToChatTargets(ctx, chatID, msg, false)
	if s.agentNotify != nil {
		s.agentNotify.PushActivityEvent(empID, employeeName, activityName, event, extra, uuid.Nil)
	}
}

// TestPushSettings sends a probe message to configured channel / notification targets.
func (s *WorkforceTelegramService) TestPushSettings(ctx context.Context, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return fmt.Errorf("请先选择打卡群")
	}
	ps, err := s.GetPushSettings(ctx, chatID)
	if err != nil {
		return err
	}
	text := "🧪 <b>推送通道测试</b>\n\n若收到此消息，说明频道/通知群配置正确，迟到与活动超时告警将推送到此处。"
	var failures []string
	sent := 0
	try := func(label, target string, enabled bool) {
		target = strings.TrimSpace(target)
		if !enabled || target == "" {
			return
		}
		if err := s.SendTelegramHTML(ctx, target, text, nil); err != nil {
			failures = append(failures, fmt.Sprintf("%s(%s): %v", label, target, err))
		} else {
			sent++
		}
	}
	try("频道", ps.ChannelID, ps.EnableChannelPush)
	try("通知群", ps.NotificationGroupID, ps.EnableGroupPush)
	try("迟到/早退群", ps.ExtraWorkGroupID, ps.ExtraWorkGroupID != "")
	if sent == 0 {
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "；"))
		}
		return fmt.Errorf("请先填写频道 ID 或通知群 ID，并开启对应推送开关")
	}
	if len(failures) > 0 {
		return fmt.Errorf("部分目标发送失败：%s", strings.Join(failures, "；"))
	}
	return nil
}
func defaultChatTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return "打卡群"
	}
	return title
}
func inlineQuickBackKeyboard(sessionID string, mode TgLangMode) map[string]interface{} {
	return map[string]interface{}{
		"inline_keyboard": [][]map[string]interface{}{
			{{
				"text":          tgReplyLabel("回座", "Trở lại chỗ ngồi", "Back", mode),
				"callback_data": "qb:" + sessionID,
				"style":         tgUIButtons["back"].Style,
			}},
		},
	}
}

const activityKeyboardCols = 3

func buildActivityRowsWithBack(mode TgLangMode, activityNames []string) [][]map[string]interface{} {
	backBtn := tgKeyboardBtn(tgButtonLabel(tgUIButtons["back"], mode), tgUIButtons["back"].Style)
	if len(activityNames) == 0 {
		return [][]map[string]interface{}{{backBtn}}
	}
	actBtns := make([]map[string]interface{}, 0, len(activityNames))
	for _, name := range activityNames {
		if strings.TrimSpace(name) != "" {
			actBtns = append(actBtns, tgKeyboardBtn(name, ""))
		}
	}
	if len(actBtns) == 0 {
		return [][]map[string]interface{}{{backBtn}}
	}
	if len(actBtns) <= 1 {
		return [][]map[string]interface{}{append(actBtns, backBtn)}
	}
	main := actBtns[:len(actBtns)-1]
	last := actBtns[len(actBtns)-1:]
	rows := make([][]map[string]interface{}, 0)
	for i := 0; i < len(main); i += activityKeyboardCols {
		end := i + activityKeyboardCols
		if end > len(main) {
			end = len(main)
		}
		rows = append(rows, main[i:end])
	}
	rows = append(rows, append(last, backBtn))
	return rows
}
// ---- Source: backend/services/workforce_tg_recovery.go ----
const defaultForceBackOvertimeMinutes = 120

var userOperationLocks sync.Map
var userOperationMu sync.Mutex

func tryAcquireUserLock(empID uuid.UUID, ttl time.Duration) bool {
	userOperationMu.Lock()
	defer userOperationMu.Unlock()
	now := time.Now()
	if v, ok := userOperationLocks.Load(empID); ok {
		if expiry, ok := v.(time.Time); ok && expiry.After(now) {
			return false
		}
	}
	userOperationLocks.Store(empID, now.Add(ttl))
	return true
}
func releaseUserLock(empID uuid.UUID) {
	userOperationLocks.Delete(empID)
}
func (s *WorkforceTelegramService) tryAcquireBackLock(empID uuid.UUID) bool {
	return tryAcquireUserLock(empID, 30*time.Second)
}
func telegramForceBackOvertimeSeconds() int {
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_FORCE_BACK_OVERTIME_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n * 60
		}
	}
	return defaultForceBackOvertimeMinutes * 60
}
func (s *WorkforceTelegramService) RecoverExpiredActivities(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	forceBackSec := telegramForceBackOvertimeSeconds()
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.employee_id, s.activity_name, s.started_at, s.limit_seconds,
			COALESCE(s.source_chat_id,''), e.name
		FROM wf_tg_activity_sessions s
		JOIN employees e ON e.id = s.employee_id
		WHERE s.status = 'open' AND s.limit_seconds > 0`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var sid, empID uuid.UUID
		var actName, chatID, empName string
		var started time.Time
		var limitSec int
		if rows.Scan(&sid, &empID, &actName, &started, &limitSec, &chatID, &empName) != nil {
			continue
		}
		elapsed := int(time.Since(started).Seconds())
		if elapsed-limitSec < forceBackSec {
			continue
		}
		sess, err := s.closeActivitySession(ctx, sid, true, "system")
		if err != nil {
			log.Printf("[Telegram] recover expired activity %s: %v", sid, err)
			continue
		}
		msg := fmt.Sprintf("🚨 %s「%s」超时超过 %d 分钟，系统已强制回座", empName, actName, forceBackSec/60)
		if sess.FineAmount > 0 {
			msg += fmt.Sprintf("\n⚠️ 罚款 %s", FormatFineMoney(sess.FineAmount, s.FineCurrencyCode(ctx)))
		}
		_ = s.SendTelegramText(ctx, chatID, msg, nil)
		s.NotifyActivityEvent(ctx, chatID, empID, empName, actName, "强制回座", fmt.Sprintf("超时 %d 分钟", forceBackSec/60))
		log.Printf("[Telegram] recovered expired activity %s for employee %s", sid, empID)
	}
}

// ---- Source: backend/services/workforce_tg_reply.go ----
func formatTelegramUserLink(telegramUserID int64, displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = fmt.Sprintf("用户%d", telegramUserID)
	}
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, telegramUserID, htmlPackage2.EscapeString(name))
}
func formatEmployeeNameHTML(telegramUserID int64, displayName string) string {
	return formatEmployeeProfileHTML(telegramUserID, "", displayName)
}
func formatEmployeeProfileHTML(telegramUserID int64, telegramUsername, displayName string) string {
	name := strings.TrimSpace(displayName)
	user := strings.TrimPrefix(strings.TrimSpace(telegramUsername), "@")
	if name == "" && looksLikeTelegramUsername(user) {
		name = user
	}
	if telegramUserID > 0 {
		return formatTelegramUserLink(telegramUserID, name)
	}
	if name == "" {
		return "-"
	}
	return htmlPackage2.EscapeString(name)
}
func looksLikeTelegramUsername(s string) bool {
	if len(s) < 5 || len(s) > 32 {
		return false
	}
	for i, r := range s {
		ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if i == 0 {
			ok = (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		}
		if !ok {
			return false
		}
	}
	return true
}
func tgCopyableText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "-"
	}
	return "<code>" + htmlPackage2.EscapeString(text) + "</code>"
}
func formatAdminMentionsHTML(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		parts = append(parts, formatTelegramUserLink(id, "管理员"))
	}
	return strings.Join(parts, " ")
}
func resolveWorkStartExpectedTime(now time.Time, recordDate, shift, shiftDetail, startClock string, loc *time.Location) time.Time {
	rd, err := time.ParseInLocation("2006-01-02", recordDate, loc)
	if err != nil {
		rd = now.In(loc)
	}
	_ = shift
	_ = shiftDetail
	// 夜班业务日 = 开班日（含跨午夜）；统一用 recordDate + 上班时刻。
	return combineDateTime(rd, startClock, loc)
}
func resolveWorkEndExpectedTime(now time.Time, recordDate, shift, startClock, endClock string, loc *time.Location) time.Time {
	rd, err := time.ParseInLocation("2006-01-02", recordDate, loc)
	if err != nil {
		rd = now.In(loc)
	}
	sh, sm, okS := parseClock(startClock)
	eh, em, okE := parseClock(endClock)
	nextDay := false
	if okS && okE && clockToMinutes(sh, sm) > clockToMinutes(eh, em) {
		nextDay = true
	} else if (shift == "night" || shift == "middle") && okE && clockToMinutes(eh, em) <= 12*60 {
		nextDay = true
	}
	if nextDay {
		rd = rd.AddDate(0, 0, 1)
	}
	return combineDateTime(rd, endClock, loc)
}
func buildWorkStartGroupReplyHTML(
	mode TgLangMode,
	telegramUserID int64,
	employeeName, shift, shiftDetail string,
	now, expected time.Time,
	lateMin int,
	workFine float64,
	handoverHint string,
	fineCurrency string,
) string {
	_ = shiftDetail
	shiftText := tgReplyShiftLabel(shift, mode)
	actionText := tgReplyLabel("上班", "Đi làm", "Clock in", mode)
	emoji := "👍"
	status := tgReplyLabel("✅ 准时", "✅ Đúng giờ", "✅ On time", mode)

	diffSec := int(now.Sub(expected).Seconds())
	if lateMin > 0 {
		emoji = "😅"
		status = fmt.Sprintf(tgReplyLabel("🚨 迟到 %s", "🚨 Đi muộn %s", "🚨 Late %s", mode), formatDurationForLang(lateMin*60, mode))
		if workFine > 0 {
			status += fmt.Sprintf(tgReplyLabel("\n💰罚款金额: %s", "\n💰 Tiền phạt: %s", "\n💰 Fine: %s", mode), FormatFineAmountWithUnit(workFine, fineCurrency))
		}
	} else if diffSec < 0 {
		status = fmt.Sprintf(tgReplyLabel("✅ 早到 %s", "✅ Đến sớm %s", "✅ Early %s", mode), formatDurationForLang(-diffSec, mode))
	}

	title := fmt.Sprintf(tgReplyLabel("%s%s完成", "%s %s hoàn thành", "%s %s completed", mode), shiftText, actionText)
	msg := fmt.Sprintf(
		"%s <b>%s</b>\n"+
			tgReplyLabel("👤 用户：%s\n", "👤 Người dùng: %s\n", "👤 User: %s\n", mode)+
			tgReplyLabel("⏰ 打卡时间：<code>%s</code>\n", "⏰ Thời gian chấm công: <code>%s</code>\n", "⏰ Clock-in: <code>%s</code>\n", mode)+
			tgReplyLabel("📅 %s时间：<code>%s</code>\n", "📅 Thời gian %s: <code>%s</code>\n", "📅 Expected %s: <code>%s</code>\n", mode)+
			tgReplyLabel("📊 状态：%s", "📊 Trạng thái: %s", "📊 Status: %s", mode),
		emoji, title,
		formatTelegramUserLink(telegramUserID, employeeName),
		now.Format("15:04"),
		actionText, expected.Format("01/02 15:04"),
		status,
	)
	if hint := strings.TrimSpace(handoverHint); hint != "" {
		msg += "\n\n" + hint
	}
	return msg
}
func buildWorkEndGroupReplyHTML(
	mode TgLangMode,
	telegramUserID int64,
	employeeName, shift string,
	now, expected time.Time,
	earlyMin int,
	workFine float64,
	handoverHint, activityWarn string,
	fineCurrency string,
) string {
	shiftText := tgReplyShiftLabel(shift, mode)
	actionText := tgReplyLabel("下班", "Tan ca", "Clock out", mode)
	emoji := "👍"
	status := tgReplyLabel("✅ 准时", "✅ Đúng giờ", "✅ On time", mode)

	diffSec := int(now.Sub(expected).Seconds())
	if earlyMin > 0 {
		emoji = "🏃"
		sec := earlyMin * 60
		if diffSec < 0 {
			sec = -diffSec
		}
		status = fmt.Sprintf(tgReplyLabel("🚨 早退 %s", "🚨 Về sớm %s", "🚨 Early leave %s", mode), formatDurationForLang(sec, mode))
		if workFine > 0 {
			status += fmt.Sprintf(tgReplyLabel("\n💰罚款金额: %s", "\n💰 Tiền phạt: %s", "\n💰 Fine: %s", mode), FormatFineAmountWithUnit(workFine, fineCurrency))
		}
	} else if diffSec > 60 {
		emoji = "⏰"
		status = fmt.Sprintf(tgReplyLabel("✅ 加班 %s", "✅ Làm thêm %s", "✅ Overtime %s", mode), formatDurationForLang(diffSec, mode))
	}

	title := fmt.Sprintf(tgReplyLabel("%s%s完成", "%s %s hoàn thành", "%s %s completed", mode), shiftText, actionText)
	msg := fmt.Sprintf(
		"%s <b>%s</b>\n"+
			tgReplyLabel("👤 用户：%s\n", "👤 Người dùng: %s\n", "👤 User: %s\n", mode)+
			tgReplyLabel("⏰ 打卡时间：<code>%s</code>\n", "⏰ Thời gian chấm công: <code>%s</code>\n", "⏰ Clock-out: <code>%s</code>\n", mode)+
			tgReplyLabel("📅 %s时间：<code>%s</code>\n", "📅 Thời gian %s: <code>%s</code>\n", "📅 Expected %s: <code>%s</code>\n", mode)+
			tgReplyLabel("📊 状态：%s", "📊 Trạng thái: %s", "📊 Status: %s", mode),
		emoji, title,
		formatTelegramUserLink(telegramUserID, employeeName),
		now.Format("15:04"),
		actionText, expected.Format("01/02 15:04"),
		status,
	)
	if hint := strings.TrimSpace(handoverHint); hint != "" {
		msg += "\n\n" + hint
	}
	if warn := strings.TrimSpace(activityWarn); warn != "" {
		msg = appendLocalizedActivityWarnHTML(msg, warn, mode)
	}
	return msg
}
func tgReplyDashedLine() string {
	// Dense ASCII dashes inside <code>: tap-to-copy on mobile, width tuned to
	// a typical phone bubble (26 hyphens). Avoid fullwidth ──── bars.
	return "<code>--------------------------</code>"
}
func buildActivityStartGroupReplyHTML(
	mode TgLangMode,
	telegramUserID int64,
	employeeName, activity, timeStr, shiftText string,
	count, maxTimes, timeLimitMin int,
) string {
	actDisplay := tgActivityDisplayName(activity, mode)
	actEsc := htmlPackage2.EscapeString(actDisplay)
	maxStr := tgReplyUnlimitedLabel(mode)
	if maxTimes > 0 {
		maxStr = fmt.Sprintf("%d", maxTimes)
	}
	shiftEsc := htmlPackage2.EscapeString(shiftText)
	msg := fmt.Sprintf(
		tgReplyLabel("👤 用户：%s\n", "👤 Người dùng: %s\n", "👤 User: %s\n", mode)+
			tgReplyLabel("✅ 打卡成功：<code>%s</code> - <code>%s</code>\n", "✅ Chấm công: <code>%s</code> - <code>%s</code>\n", "✅ Activity started: <code>%s</code> - <code>%s</code>\n", mode),
		formatTelegramUserLink(telegramUserID, employeeName),
		actEsc, htmlPackage2.EscapeString(timeStr),
	)
	if shiftText != "" {
		msg += fmt.Sprintf(tgReplyLabel("📊 班次：<code>%s</code>\n", "📊 Ca: <code>%s</code>\n", "📊 Shift: <code>%s</code>\n", mode), shiftEsc)
	}
	msg += fmt.Sprintf(
		tgReplyLabel(
			"▫️ 本次活动类型：<code>%s</code>\n⏰ 单次时长限制：<code>%d</code>分钟 \n📈 今日<code>%s</code>次数：第 <code>%d</code> 次（上限 <code>%s</code> 次）\n",
			"▫️ Loại hoạt động: <code>%s</code>\n⏰ Giới hạn: <code>%d</code> phút\n📈 Hôm nay <code>%s</code>: lần <code>%d</code> (tối đa <code>%s</code>)\n",
			"▫️ Activity: <code>%s</code>\n⏰ Time limit: <code>%d</code> min\n📈 Today <code>%s</code>: #<code>%d</code> (max <code>%s</code>)\n",
			mode,
		),
		actEsc, timeLimitMin, actEsc, count, htmlPackage2.EscapeString(maxStr),
	)
	if maxTimes > 0 && count >= maxTimes {
		msg += fmt.Sprintf(
			tgReplyLabel(
				"🚨 警告：本次结束后，您今日的<code>%s</code>次数将达到上限，请留意！\n",
				"🚨 Cảnh báo: sau lần này <code>%s</code> sẽ đạt giới hạn hôm nay!\n",
				"🚨 Warning: this will reach today's limit for <code>%s</code>!\n",
				mode,
			),
			actEsc,
		)
	}
	msg += fmt.Sprintf(
		"%s\n%s\n%s",
		tgReplyDashedLine(),
		tgReplyLabel("💡 操作提示", "💡 Gợi ý", "💡 Tip", mode),
		tgReplyLabel(
			"活动结束后请点击下方「回座」按钮，或使用 /at。",
			"Sau khi xong, bấm «Trở lại chỗ ngồi» hoặc dùng /at.",
			"When done, tap Back to seat or use /at.",
			mode,
		),
	)
	return msg
}
func buildActivityEndGroupReplyHTML(
	mode TgLangMode,
	telegramUserID int64,
	employeeName, activity, timeStr string,
	elapsedSec, todayActivityCount, totalActivitySec, totalDailySec, totalDailyCount int,
	activityCounts map[string]int,
	isOvertime bool,
	overtimeSec int,
	fine float64,
	fineCurrency string,
) string {
	actDisplay := tgActivityDisplayName(activity, mode)
	actEsc := htmlPackage2.EscapeString(actDisplay)
	msg := fmt.Sprintf(
		tgReplyLabel("👤 用户：%s\n", "👤 Người dùng: %s\n", "👤 User: %s\n", mode)+
			tgReplyLabel("✅ 回座打卡：<code>%s</code>\n", "✅ Trở lại: <code>%s</code>\n", "✅ Back to seat: <code>%s</code>\n", mode)+
			"%s\n"+
			tgReplyLabel("📍 活动记录\n", "📍 Lịch sử hoạt động\n", "📍 Activity log\n", mode)+
			tgReplyLabel("▫️ 活动类型：<code>%s</code>\n", "▫️ Loại: <code>%s</code>\n", "▫️ Type: <code>%s</code>\n", mode)+
			tgReplyLabel("▫️ 本次耗时：<code>%s</code> ⏰\n", "▫️ Lần này: <code>%s</code> ⏰\n", "▫️ This session: <code>%s</code> ⏰\n", mode)+
			tgReplyLabel("▫️ 累计时长：<code>%s</code>\n", "▫️ Tổng loại: <code>%s</code>\n", "▫️ Type total: <code>%s</code>\n", mode)+
			tgReplyLabel("▫️ 今日次数：<code>%d</code>次\n", "▫️ Số lần hôm nay: <code>%d</code>\n", "▫️ Today count: <code>%d</code>\n", mode),
		formatTelegramUserLink(telegramUserID, employeeName),
		htmlPackage2.EscapeString(timeStr),
		tgReplyDashedLine(),
		actEsc,
		formatDurationForLang(elapsedSec, mode),
		formatDurationForLang(totalActivitySec, mode),
		todayActivityCount,
	)
	if isOvertime && overtimeSec > 0 {
		msg += "\n" + tgReplyLabel("⚠️ 超时提醒\n", "⚠️ Quá giờ\n", "⚠️ Overtime\n", mode)
		msg += fmt.Sprintf(
			tgReplyLabel("▫️ 超时时长：<code>%s</code> 🚨\n", "▫️ Quá giờ: <code>%s</code> 🚨\n", "▫️ Overtime: <code>%s</code> 🚨\n", mode),
			formatDurationForLang(overtimeSec, mode),
		)
		if fine > 0 {
			msg += fmt.Sprintf(
				tgReplyLabel("▫️ 罚款金额：<code>%s</code> 💸\n", "▫️ Tiền phạt: <code>%s</code> 💸\n", "▫️ Fine: <code>%s</code> 💸\n", mode),
				FormatFineAmountWithUnit(fine, fineCurrency),
			)
		}
	}
	msg += tgReplyDashedLine() + "\n"
	msg += tgReplyLabel("📊 今日总计\n▫️ 活动详情\n", "📊 Tổng hôm nay\n▫️ Chi tiết\n", "📊 Today total\n▫️ Details\n", mode)
	for name, count := range activityCounts {
		if count <= 0 {
			continue
		}
		displayName := tgActivityDisplayName(name, mode)
		msg += fmt.Sprintf(
			tgReplyLabel("   ➤ <code>%s</code>：<code>%d</code> 次 📝\n", "   ➤ <code>%s</code>: <code>%d</code> lần 📝\n", "   ➤ <code>%s</code>: <code>%d</code> 📝\n", mode),
			htmlPackage2.EscapeString(displayName), count,
		)
	}
	msg += fmt.Sprintf(
		tgReplyLabel("▫️ 总活动次数：<code>%d</code>次\n", "▫️ Tổng số lần: <code>%d</code>\n", "▫️ Total sessions: <code>%d</code>\n", mode),
		totalDailyCount,
	)
	msg += fmt.Sprintf(
		tgReplyLabel("▫️ 总活动时长：<code>%s</code>", "▫️ Tổng thời gian: <code>%s</code>", "▫️ Total duration: <code>%s</code>", mode),
		formatDurationForLang(totalDailySec, mode),
	)
	return msg
}
func buildActivitySoonTimeoutHTML(mode TgLangMode, telegramUserID int64, employeeName, activity, shiftText string) string {
	actEsc := htmlPackage2.EscapeString(tgActivityDisplayName(activity, mode))
	shiftEsc := htmlPackage2.EscapeString(shiftText)
	return fmt.Sprintf(
		tgReplyLabel(
			"⏳ <b>即将超时警告</b>\n👤 %s\n📊 班次：<code>%s</code>\n🕓 本次 <code>%s</code> 还有 <code>1</code> 分钟！\n💡 请及时回座，避免超时罚款",
			"⏳ <b>Cảnh báo sắp quá giờ</b>\n👤 %s\n📊 Ca: <code>%s</code>\n🕓 <code>%s</code> lần này còn <code>1</code> phút!\n💡 Vui lòng trở lại chỗ ngồi để tránh phạt quá giờ",
			"⏳ <b>Timeout warning</b>\n👤 %s\n📊 Shift: <code>%s</code>\n🕓 Current <code>%s</code> has <code>1</code> minute left!\n💡 Please return to your seat to avoid overtime fines",
			mode,
		),
		formatTelegramUserLink(telegramUserID, employeeName),
		shiftEsc,
		actEsc,
	)
}
func buildActivityOvertimeWarnHTML(mode TgLangMode, telegramUserID int64, employeeName, activity, shiftText string, overtimeMin, variant int) string {
	actEsc := htmlPackage2.EscapeString(tgActivityDisplayName(activity, mode))
	shiftEsc := htmlPackage2.EscapeString(shiftText)
	user := formatTelegramUserLink(telegramUserID, employeeName)
	switch variant {
	case 0:
		return fmt.Sprintf(
			tgReplyLabel(
				"⚠️ <b>超时警告</b>\n👤 %s\n📊 班次：<code>%s</code>\n🕓 本次 <code>%s</code> 已超时\n🏃‍♂️ 请立即回座，避免产生更多罚款！",
				"⚠️ <b>Cảnh báo quá giờ</b>\n👤 %s\n📊 Ca: <code>%s</code>\n🕓 <code>%s</code> lần này đã quá giờ\n🏃‍♂️ Hãy trở lại ngay để tránh phạt thêm!",
				"⚠️ <b>Overtime warning</b>\n👤 %s\n📊 Shift: <code>%s</code>\n🕓 Current <code>%s</code> is overtime\n🏃‍♂️ Please return now to avoid more fines!",
				mode,
			),
			user, shiftEsc, actEsc)
	case 5:
		return fmt.Sprintf(
			tgReplyLabel(
				"🔔 <b>超时警告</b>\n👤 %s\n📊 班次：<code>%s</code>\n🕓 本次 <code>%s</code> 已超时 <code>%d</code> 分钟！\n😤 罚款正在累积，请立即回座！",
				"🔔 <b>Cảnh báo quá giờ</b>\n👤 %s\n📊 Ca: <code>%s</code>\n🕓 <code>%s</code> lần này đã quá <code>%d</code> phút!\n😤 Tiền phạt đang cộng dồn, hãy trở lại ngay!",
				"🔔 <b>Overtime warning</b>\n👤 %s\n📊 Shift: <code>%s</code>\n🕓 Current <code>%s</code> is <code>%d</code> min overtime!\n😤 Fines are accumulating — return now!",
				mode,
			),
			user, shiftEsc, actEsc, overtimeMin)
	default:
		return fmt.Sprintf(
			tgReplyLabel(
				"🚨 <b>超时警告</b>\n👤 %s\n📊 班次：<code>%s</code>\n🕓 本次 <code>%s</code> 已超时 <code>%d</code> 分钟！\n💢 请立刻回座，避免产生更多罚款！",
				"🚨 <b>Cảnh báo quá giờ</b>\n👤 %s\n📊 Ca: <code>%s</code>\n🕓 <code>%s</code> lần này đã quá <code>%d</code> phút!\n💢 Hãy trở lại ngay để tránh phạt thêm!",
				"🚨 <b>Overtime warning</b>\n👤 %s\n📊 Shift: <code>%s</code>\n🕓 Current <code>%s</code> is <code>%d</code> min overtime!\n💢 Return immediately to avoid more fines!",
				mode,
			),
			user, shiftEsc, actEsc, overtimeMin)
	}
}
func buildActivityForceBackHTML(mode TgLangMode, telegramUserID int64, employeeName, activity, shiftText string, overtimeMin int, fineText string) string {
	msg := fmt.Sprintf(
		tgReplyLabel(
			"🛑 <b>自动安全回座</b>\n👤 %s\n📝 活动：<code>%s</code>\n📊 班次：<code>%s</code>\n⚠️ 超时超过%d分钟，系统已自动回座",
			"🛑 <b>Tự động trở lại an toàn</b>\n👤 %s\n📝 Hoạt động: <code>%s</code>\n📊 Ca: <code>%s</code>\n⚠️ Đã quá hơn %d phút, hệ thống đã tự động trở lại",
			"🛑 <b>Automatic return to seat</b>\n👤 %s\n📝 Activity: <code>%s</code>\n📊 Shift: <code>%s</code>\n⚠️ Overtime exceeded %d minutes; the system returned you automatically",
			mode,
		),
		formatTelegramUserLink(telegramUserID, employeeName),
		htmlPackage2.EscapeString(tgActivityDisplayName(activity, mode)),
		htmlPackage2.EscapeString(shiftText),
		overtimeMin,
	)
	if strings.TrimSpace(fineText) != "" {
		msg += fmt.Sprintf(
			tgReplyLabel("\n💰 本次罚款金额：<code>%s</code>", "\n💰 Tiền phạt lần này: <code>%s</code>", "\n💰 Fine this time: <code>%s</code>", mode),
			htmlPackage2.EscapeString(fineText),
		)
	}
	return msg
}
func buildActivityOvertimeBackPushHTML(chatTitle string, telegramUserID int64, telegramUsername, employeeName, activity string, backAt time.Time, overtimeSec int, fine float64, fineCurrency string) string {
	title := htmlPackage2.EscapeString(strings.TrimSpace(chatTitle))
	if title == "" {
		title = "打卡群"
	}
	msg := fmt.Sprintf(
		"🚨 <b>超时回座通知</b>\n🏢 群组：<code>%s</code>\n%s\n"+
			"👤 用户：%s\n📝 活动：<code>%s</code>\n⏰ 回座时间：<code>%s</code>\n⏱️ 超时时长：<code>%s</code>\n",
		title, tgReplyDashedLine(),
		formatEmployeeProfileHTML(telegramUserID, telegramUsername, employeeName),
		htmlPackage2.EscapeString(activity),
		backAt.Format("01/02 15:04:05"),
		formatDurationZh(overtimeSec),
	)
	if fine > 0 {
		msg += fmt.Sprintf("💰 罚款金额：<code>%s</code>", FormatFineAmountWithUnit(fine, fineCurrency))
	}
	return msg
}

// ---- Source: backend/services/workforce_tg_reply_hints.go ----
var tgActivityEN = map[string]string{
	"吃饭":    "Meal break",
	"大厕":    "Restroom (long)",
	"小厕":    "Restroom (short)",
	"抽烟或休息": "Smoke / break",
	"抽烟":    "Smoke break",
}

func tgActivityDisplayName(name string, mode TgLangMode) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	switch mode {
	case TgLangVi:
		if v := strings.TrimSpace(tgActivityVI[name]); v != "" {
			return v
		}
	case TgLangEn:
		if v := strings.TrimSpace(tgActivityEN[name]); v != "" {
			return v
		}
	}
	return name
}
func activityGroupNotifyPrefix(source string, mode TgLangMode) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case PunchSourceAgent:
		return tgReplyLabel("💻 <b>PC客户端打卡</b>\n", "💻 <b>Chấm công PC</b>\n", "💻 <b>PC clock-in</b>\n", mode)
	case PunchSourceWeb:
		return tgReplyLabel("🖥 <b>后台打卡</b>\n", "🖥 <b>Chấm công admin</b>\n", "🖥 <b>Admin clock-in</b>\n", mode)
	default:
		return ""
	}
}
func buildActivityStartMessageText(mode TgLangMode, activity, workShift string, limitMin, count, maxTimes int) string {
	maxStr := tgReplyUnlimitedLabel(mode)
	if maxTimes > 0 {
		maxStr = fmt.Sprintf("%d", maxTimes)
	}
	act := tgActivityDisplayName(activity, mode)
	shiftText := tgReplyShiftLabel(workShift, mode)
	return fmt.Sprintf(
		tgReplyLabel(
			"✅ 打卡成功：%s\n📊 班次：%s\n⏰ 时限：%d 分钟\n📈 今日次数：第 %d 次（上限 %s 次）\n\n💡 结束后请点击「回座」按钮",
			"✅ Chấm công: %s\n📊 Ca: %s\n⏰ Giới hạn: %d phút\n📈 Hôm nay: lần %d (tối đa %s)\n\n💡 Xong bấm «Trở lại chỗ ngồi»",
			"✅ Activity: %s\n📊 Shift: %s\n⏰ Limit: %d min\n📈 Today: #%d (max %s)\n\n💡 Tap Back to seat when done",
			mode,
		), act, shiftText, limitMin, count, maxStr)
}
func attendanceReviewPendingHint(kind string, mode TgLangMode) string {
	switch kind {
	case "late":
		return tgReplyLabel(
			"⚠️ 迟到超过阈值，已进入【罚款详情-待确认】，未确认前不记罚款。",
			"⚠️ Đi muộn vượt ngưỡng, đã vào [Phạt-Chờ xác nhận], chưa ghi phạt trước khi xác nhận.",
			"⚠️ Late beyond threshold — pending fine review; no fine until confirmed.",
			mode,
		)
	case "early":
		return tgReplyLabel(
			"⚠️ 早退超过阈值，已进入【罚款详情-待确认】，未确认前不记罚款。",
			"⚠️ Về sớm vượt ngưỡng, đã vào [Phạt-Chờ xác nhận], chưa ghi phạt trước khi xác nhận.",
			"⚠️ Early leave beyond threshold — pending fine review; no fine until confirmed.",
			mode,
		)
	default:
		return tgReplyLabel(
			"⚠️ 已进入【罚款详情-待确认】，未确认前不记罚款。",
			"⚠️ Đã vào [Phạt-Chờ xác nhận], chưa ghi phạt trước khi xác nhận.",
			"⚠️ Pending fine review — no fine until confirmed.",
			mode,
		)
	}
}
func (s *WorkforceTelegramService) handoverStatusHint(ctx context.Context, chatID string, period *TgPeriodInfo, mode TgLangMode) string {
	if period == nil || !period.IsHandover {
		return ""
	}
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	threshold := handoverThreshold(cfg)
	if period.Cycle == 1 {
		remaining := math.Max(0, threshold-period.HoursElapsed)
		return fmt.Sprintf(
			tgReplyLabel(
				"🔄 换班日（%.0f小时制）· 第1段 · 约 %.1fh 后活动次数重置",
				"🔄 Ngày giao ca (%.0fh) · Đoạn 1 · Reset số lần sau ~%.1fh",
				"🔄 Handover day (%.0fh) · Segment 1 · Activity count resets in ~%.1fh",
				mode,
			), period.TotalHours, remaining)
	}
	return fmt.Sprintf(
		tgReplyLabel(
			"🔄 换班日（%.0f小时制）· 第2段 · 活动次数已重置",
			"🔄 Ngày giao ca (%.0fh) · Đoạn 2 · Đã reset số lần hoạt động",
			"🔄 Handover day (%.0fh) · Segment 2 · Activity count reset",
			mode,
		), period.TotalHours)
}
func (s *WorkforceTelegramService) buildRelaySuccessorHintAfterCheckIn(
	ctx context.Context, empID uuid.UUID, chatID, shift, recordDate string, now time.Time, mode TgLangMode,
) string {
	if shift != "day" {
		return ""
	}
	pending, err := s.getPendingWorkEndShift(ctx, chatID, empID)
	if err != nil || pending == nil || pending.Shift != "day" || pending.RecordDate >= recordDate {
		return ""
	}
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	settings, _ := s.GetSettings(ctx)
	loc := tgLoadLocation("")
	if settings != nil {
		loc = tgLoadLocation(settings.Timezone)
	}
	if !isHandoverRelaySuccessor(shift, recordDate, pending.Shift, pending.RecordDate, cfg, loc) {
		return ""
	}
	s.upsertRelaySuccessor(ctx, chatID, empID, pending.RecordDate, recordDate)
	dayStart := "15:00"
	if cfg != nil && strings.TrimSpace(cfg.HandoverDayStartTime) != "" {
		dayStart = cfg.HandoverDayStartTime
	}
	return fmt.Sprintf(
		tgReplyLabel(
			"\n🔄 换班接班\n• 已记录 %s 白班上班\n• 此后活动归属本班次\n• 下次「下班」将结束换班白班（%s %s 起）",
			"\n🔄 Nhận ca giao\n• Đã ghi %s ca ngày\n• Hoạt động sau thuộc ca này\n• «Tan ca» lần tới kết thúc ca giao (%s từ %s)",
			"\n🔄 Handover takeover\n• Recorded %s day shift clock-in\n• Later activities belong to this shift\n• Next clock-out ends handover day shift (from %s %s)",
			mode,
		), recordDate, pending.RecordDate, dayStart)
}
func (s *WorkforceTelegramService) buildRelayCheckoutHint(
	ctx context.Context, chatID string, empID uuid.UUID, shift, recordDate string, now time.Time, mode TgLangMode,
) string {
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	settings, _ := s.GetSettings(ctx)
	loc := tgLoadLocation("")
	if settings != nil {
		loc = tgLoadLocation(settings.Timezone)
	}
	if shift != "day" || !s.recordDateIsHandoverDay(ctx, chatID, recordDate, loc) {
		return ""
	}
	s.clearRelaySuccessor(ctx, chatID, empID)
	remaining, _ := s.getPendingWorkEndShift(ctx, chatID, empID)
	remainText := ""
	if remaining != nil {
		remainText = fmt.Sprintf(
			tgReplyLabel(
				"\n• 当前在岗：%s %s",
				"\n• Đang làm: %s %s",
				"\n• On shift: %s %s",
				mode,
			), remaining.RecordDate, tgReplyShiftLabel(remaining.Shift, mode))
	}
	startTime := "15:00"
	if cfg != nil && strings.TrimSpace(cfg.HandoverDayStartTime) != "" {
		startTime = cfg.HandoverDayStartTime
	}
	return fmt.Sprintf(
		tgReplyLabel(
			"🔄 换班白班已结束（%s %s 起）%s\n• 之后按正常流程打卡即可",
			"🔄 Ca giao ngày đã kết thúc (từ %s %s)%s\n• Tiếp tục chấm công bình thường",
			"🔄 Handover day shift ended (from %s %s)%s\n• Continue with normal punch flow",
			mode,
		), recordDate, startTime, remainText)
}
func (s *WorkforceTelegramService) buildCrossShiftActivityWarn(
	ctx context.Context, empID uuid.UUID, closingShift, recordDate string, mode TgLangMode,
) string {
	sess, err := s.getOpenActivitySession(ctx, empID)
	if errors.Is(err, ErrActivityNotActive) || err != nil {
		return ""
	}
	var actShift string
	_ = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day') FROM wf_tg_activity_sessions WHERE id = $1`, sess.ID).Scan(&actShift)
	if actShift == closingShift && sess.AttendanceDate == recordDate {
		return ""
	}
	actName := tgActivityDisplayName(sess.ActivityName, mode)
	shiftName := tgReplyShiftLabel(actShift, mode)
	return fmt.Sprintf(
		tgReplyLabel(
			"⚠️ 活动「%s」仍在进行（%s），请手动回座",
			"⚠️ Hoạt động «%s» vẫn đang diễn ra (%s), vui lòng trở lại",
			"⚠️ Activity \"%s\" still open (%s) — please return manually",
			mode,
		), actName, shiftName)
}
func appendLocalizedActivityWarnHTML(msg, activityWarn string, mode TgLangMode) string {
	warn := strings.TrimSpace(activityWarn)
	if warn == "" {
		return msg
	}
	prefix := tgReplyLabel("🔄 ", "🔄 ", "🔄 ", mode)
	return msg + "\n\n" + prefix + htmlPackage2.EscapeString(warn)
}

// ---- Source: backend/services/workforce_tg_reset.go ----
func telegramResetExportDir() string {
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_RESET_EXPORT_DIR")); v != "" {
		return v
	}
	return filepath.Join(os.TempDir(), "tg-attendance-exports")
}
func (s *WorkforceTelegramService) exportBeforeReset(ctx context.Context, dateFrom, dateTo, chatID string) (string, error) {
	data, filename, err := s.ExportAttendanceXLSX(ctx, dateFrom, dateTo, chatID)
	if err != nil {
		return "", err
	}

	// Durable report backup comes before any reset/purge. Local files are only transport scratch space.
	if _, err = s.db.ExecContext(ctx, `INSERT INTO standalone_exports(filename,content) VALUES($1,$2) ON CONFLICT(filename) DO UPDATE SET content=EXCLUDED.content,created_at=NOW()`, filename, data); err != nil {
		return "", fmt.Errorf("persist export backup: %w", err)
	}
	dir := telegramResetExportDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
func (s *WorkforceTelegramService) archiveMonthlyStats(ctx context.Context, yearMonth string) error {
	if strings.TrimSpace(yearMonth) == "" {
		return nil
	}
	t, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return err
	}
	dateFrom := t.Format("2006-01-02")
	dateTo := t.AddDate(0, 1, -1).Format("2006-01-02")

	// Activity totals are merged into wf_tg_monthly_activity_stats on each group daily reset (before purge).

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_monthly_work_stats (
			year_month, chat_id, employee_id, shift_type, session_count, work_fine_total, archived_at
		)
		SELECT $1, COALESCE(b.chat_id,''), ws.employee_id, COALESCE(ws.shift_type,'day'),
			COUNT(*)::int,
			COALESCE((
				SELECT SUM(p.amount) FROM penalty_records p
				WHERE p.employee_id = ws.employee_id AND p.category = 'TG考勤'
				  AND p.penalty_date BETWEEN $2::date AND $3::date
			), 0),
			NOW()
		FROM work_sessions ws
		LEFT JOIN employee_telegram_bindings b ON b.employee_id = ws.employee_id AND b.status = 1
		WHERE ws.source IN ('telegram','web') AND ws.attendance_date BETWEEN $2::date AND $3::date
		GROUP BY COALESCE(b.chat_id,''), ws.employee_id, COALESCE(ws.shift_type,'day')
		ON CONFLICT (year_month, chat_id, employee_id, shift_type) DO UPDATE SET
			session_count = EXCLUDED.session_count,
			work_fine_total = EXCLUDED.work_fine_total,
			archived_at = NOW()`, yearMonth, dateFrom, dateTo)
	return err
}

// mergeActivityStatsIntoMonthlyForGroupTx rolls closed activity sessions for one export day into the monthly table (before purge).
func (s *WorkforceTelegramService) mergeActivityStatsIntoMonthlyForGroupTx(ctx context.Context, tx execContext, chatID, exportDate string) error {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" || len(exportDate) < 7 {
		return nil
	}
	yearMonth := exportDate[:7]
	_, err := tx.ExecContext(ctx, `
		INSERT INTO wf_tg_monthly_activity_stats (
			year_month, chat_id, employee_id, shift_type, activity_code, activity_name,
			activity_count, total_seconds, fine_total, archived_at
		)
		SELECT $1, COALESCE(s.source_chat_id,''), s.employee_id, COALESCE(s.shift_type,'day'),
			s.activity_code, s.activity_name,
			COUNT(*)::int, COALESCE(SUM(s.elapsed_seconds),0)::int, COALESCE(SUM(s.fine_amount),0), NOW()
		FROM wf_tg_activity_sessions s
		WHERE s.status = 'closed' AND s.attendance_date = $2::date
		  AND COALESCE(s.source_chat_id,'') = $3
		GROUP BY COALESCE(s.source_chat_id,''), s.employee_id, COALESCE(s.shift_type,'day'),
			s.activity_code, s.activity_name
		ON CONFLICT (year_month, chat_id, employee_id, shift_type, activity_code) DO UPDATE SET
			activity_name = EXCLUDED.activity_name,
			activity_count = wf_tg_monthly_activity_stats.activity_count + EXCLUDED.activity_count,
			total_seconds = wf_tg_monthly_activity_stats.total_seconds + EXCLUDED.total_seconds,
			fine_total = wf_tg_monthly_activity_stats.fine_total + EXCLUDED.fine_total,
			archived_at = NOW()`, yearMonth, exportDate, chatID)
	return err
}

type execContext interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func (s *WorkforceTelegramService) resetExportDate(settings *TelegramSettings, now time.Time) string {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	resetH, resetM, ok := parseClock(settings.DailyResetTime)
	if !ok {
		return now.AddDate(0, 0, -1).Format("2006-01-02")
	}
	resetToday := time.Date(now.Year(), now.Month(), now.Day(), resetH, resetM, 0, 0, loc)
	if now.Before(resetToday) {
		return now.AddDate(0, 0, -2).Format("2006-01-02")
	}
	return now.AddDate(0, 0, -1).Format("2006-01-02")
}
func (s *WorkforceTelegramService) RecoverMissedDailyResets(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	settings, err := s.GetSettings(ctx)
	if err != nil || !settings.DailyResetEnabled {
		return
	}
	groups, err := s.ListGroups(ctx)
	if err != nil {
		return
	}
	loc := tgLoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	resetDate := now.Format("2006-01-02")

	for _, g := range groups {
		if !g.Enabled || strings.TrimSpace(g.ChatID) == "" {
			continue
		}
		if !s.shouldRunScheduledDailyReset(ctx, settings, g.ChatID, now) {
			continue
		}
		exportDate := s.scheduledResetExportDateForChat(ctx, g.ChatID, settings, now)
		opts := &DailyResetRunOptions{ForcedExportDate: exportDate}
		if _, err := s.runDailyResetForGroup(ctx, g.ChatID, resetDate, opts); err != nil {
			log.Printf("[Telegram] recovered missed group reset chat=%s export=%s reset=%s: %v", g.ChatID, exportDate, resetDate, err)
		} else {
			log.Printf("[Telegram] recovered missed group reset chat=%s export=%s reset=%s", g.ChatID, exportDate, resetDate)
		}
	}
}
// BuildWorkerReplyKeyboardForUser builds the normal bottom reply keyboard.
// Device selection is intentionally not encoded in reply-keyboard labels:
// ambiguous multi-device actions are resolved by a second-step inline picker.
func (s *WorkforceTelegramService) BuildWorkerReplyKeyboardForUser(ctx context.Context, chatID string, telegramUserID int64) map[string]interface{} {
	settings, _ := s.GetSettings(ctx)
	types, _ := s.ListEnabledActivityTypesForWorker(ctx)
	names := make([]string, 0, len(types))
	for _, t := range types {
		if strings.TrimSpace(t.Name) != "" {
			names = append(names, t.Name)
		}
	}
	dual := settings != nil && settings.DualShiftEnabled
	triple := settings != nil && settings.TripleShiftEnabled
	mode := s.keyboardLangForUser(ctx, chatID, telegramUserID)
	showInfo := settings != nil && settings.ShowKeyboardInfoButtons
	kb := enrichReplyKeyboardStyles(localizedMainReplyKeyboardEx(mode, dual, triple, names, showInfo))
	if telegramUserID > 0 && kb != nil {
		// Group chats: only the replied-to user receives this keyboard.
		kb["selective"] = true
	}
	return kb
}

// DeviceTagLegendForTelegram is retained for response compatibility. The
// two-step picker no longer exposes A/B tags in the reply keyboard.
func (s *WorkforceTelegramService) DeviceTagLegendForTelegram(ctx context.Context, telegramUserID int64) string {
	return ""
}

// ---- Source: backend/services/workforce_tg_reset_p0.go ----
func telegramShiftStateMaxHours() int {
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_SHIFT_STATE_MAX_HOURS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 24
}

// tgGroupEmployeeBelongsToChatSQL matches employees tied to a TG export group (binding, shared PC, or session chat).
func tgGroupEmployeeBelongsToChatSQL(employeeIDColumn, chatPlaceholder string) string {
	return fmt.Sprintf(`(
		EXISTS (
			SELECT 1 FROM employee_telegram_bindings b
			WHERE b.employee_id = %s AND b.status = 1 AND b.chat_id = %s
		)
		OR EXISTS (
			SELECT 1 FROM wf_shared_workstation_members m
			JOIN device_telegram_bindings dtb ON dtb.device_id = m.device_id AND dtb.status = 1
			WHERE m.employee_id = %s AND dtb.chat_id = %s
		)
	)`, employeeIDColumn, chatPlaceholder, employeeIDColumn, chatPlaceholder)
}
func tgExportChatFilter(chatID string) (string, string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "", ""
	}
	return ` AND (
			EXISTS (
				SELECT 1 FROM employee_telegram_bindings b
				WHERE b.employee_id = ws.employee_id AND b.status = 1
				  AND (
					b.chat_id = $3
					OR (
						COALESCE(NULLIF(TRIM(b.chat_id), ''), '') = ''
						AND (
							EXISTS (
								SELECT 1 FROM wf_tg_activity_sessions s
								WHERE s.employee_id = ws.employee_id
								  AND COALESCE(s.source_chat_id,'') = $3
								  AND s.attendance_date BETWEEN $1::date AND $2::date
							)
							OR EXISTS (
								SELECT 1 FROM wf_tg_activity_sessions_archive sa
								WHERE sa.employee_id = ws.employee_id
								  AND COALESCE(sa.source_chat_id,'') = $3
								  AND sa.attendance_date BETWEEN $1::date AND $2::date
							)
						)
					)
				  )
			)
			OR EXISTS (
				SELECT 1 FROM wf_shared_workstation_members m
				JOIN device_telegram_bindings dtb ON dtb.device_id = m.device_id AND dtb.status = 1
				WHERE m.employee_id = ws.employee_id AND dtb.chat_id = $3
			)
			OR COALESCE(ws.source_chat_id,'') = $3
		)`, chatID
}
func tgExportActivityChatFilter(chatID string) (string, string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "", ""
	}
	return ` AND COALESCE(s.source_chat_id,'') = $3`, chatID
}

// tgExportAttendanceChatFilter scopes attendance_records fallback rows to a TG group ($3).
func tgExportAttendanceChatFilter(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ""
	}
	return ` AND (` + tgGroupEmployeeBelongsToChatSQL("ar.employee_id", "$3") + `)`
}
func exportFilenameSuffix(chatID, dateFrom, dateTo string) string {
	if strings.TrimSpace(chatID) == "" {
		return fmt.Sprintf("telegram_attendance_%s_%s", dateFrom, dateTo)
	}
	return fmt.Sprintf("telegram_attendance_%s_%s_%s", chatID, dateFrom, dateTo)
}
func (s *WorkforceTelegramService) resetAutoCheckoutTime(ctx context.Context, settings *TelegramSettings, chatID, exportDate, shift string) time.Time {
	loc := tgLoadLocation(settings.Timezone)
	parsed, err := time.ParseInLocation("2006-01-02", exportDate, loc)
	if err != nil {
		return time.Now().In(loc)
	}
	// The execution delay schedules maintenance; it is never an employee's checkout clock.
	return s.expectedWorkEnd(ctx, chatID, settings, shift, exportDate, parsed.Add(12*time.Hour))
}

// resetWorkEndDateFilter 定时/懒重置只处理统计日（昨日）；手动重置保留日期保护。
func resetWorkEndDateFilter(runOpts *DailyResetRunOptions, exportDate string, todayDate string, argN int) (string, []interface{}) {
	if runOpts != nil && runOpts.ManualTrigger {
		if runOpts.ExportBusinessToday {
			return fmt.Sprintf(" AND ws.attendance_date < $%d::date", argN), []interface{}{todayDate}
		}
		return fmt.Sprintf(" AND ws.attendance_date <= $%d::date", argN), []interface{}{exportDate}
	}
	if strings.TrimSpace(exportDate) == "" {
		return "", nil
	}
	return fmt.Sprintf(" AND ws.attendance_date = $%d::date", argN), []interface{}{exportDate}
}
func (s *WorkforceTelegramService) completeMissingWorkEndsForGroup(ctx context.Context, chatID, exportDate string, runOpts *DailyResetRunOptions) (int, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return 0, nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	loc := tgLoadLocation(settings.Timezone)
	todayDate := time.Now().In(loc).Format("2006-01-02")
	dateFilter, dateArgs := resetWorkEndDateFilter(runOpts, exportDate, todayDate, 2)
	groupFilter := tgGroupEmployeeBelongsToChatSQL("ws.employee_id", "$1")
	query := fmt.Sprintf(`
		SELECT ws.id, ws.employee_id, COALESCE(ws.shift_type,'day'), ws.attendance_date::text
		FROM work_sessions ws
		WHERE ws.source IN ('telegram','web') AND ws.status = 'open'
		  AND (%s OR COALESCE(ws.source_chat_id,'') = $1)%s`, groupFilter, dateFilter)
	args := append([]interface{}{chatID}, dateArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	closed := 0
	for rows.Next() {
		var sid, empID uuid.UUID
		var shift, recordDate string
		if rows.Scan(&sid, &empID, &shift, &recordDate) != nil {
			continue
		}
		if s.shouldPreserveOpenShiftDuringReset(ctx, empID, recordDate, shift) {
			continue
		}
		checkoutAt := s.resetAutoCheckoutTime(ctx, settings, chatID, exportDate, shift)
		if recordDate != exportDate {
			checkoutAt = s.resetAutoCheckoutTime(ctx, settings, chatID, recordDate, shift)
		}
		if err := s.forceCheckoutWorkSessionAtWithOptions(ctx, sid, chatID, checkoutAt, "日重置补全下班", true); err != nil {
			return closed, fmt.Errorf("complete work end %s: %w", sid, err)
		}
		closed++
	}
	return closed, rows.Err()
}
func (s *WorkforceTelegramService) completeMissingAgentWorkEndsForGroup(ctx context.Context, chatID, exportDate string, runOpts *DailyResetRunOptions) (int, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return 0, nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	loc := tgLoadLocation(settings.Timezone)
	todayDate := time.Now().In(loc).Format("2006-01-02")
	dateFilter, dateArgs := resetWorkEndDateFilter(runOpts, exportDate, todayDate, 2)
	groupFilter := tgGroupEmployeeBelongsToChatSQL("ws.employee_id", "$1")
	query := fmt.Sprintf(`
		SELECT ws.id, ws.employee_id, COALESCE(ws.shift_type,'day'), ws.attendance_date::text
		FROM work_sessions ws
		WHERE ws.source = 'agent' AND ws.status = 'open'
		  AND (%s OR COALESCE(ws.source_chat_id,'') = $1)%s`, groupFilter, dateFilter)
	args := append([]interface{}{chatID}, dateArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	closed := 0
	for rows.Next() {
		var sid, empID uuid.UUID
		var shift, recordDate string
		if rows.Scan(&sid, &empID, &shift, &recordDate) != nil {
			continue
		}
		checkoutAt := s.resetAutoCheckoutTime(ctx, settings, chatID, exportDate, shift)
		if recordDate != exportDate {
			checkoutAt = s.resetAutoCheckoutTime(ctx, settings, chatID, recordDate, shift)
		}
		if err := s.forceCheckoutWorkSessionAtWithOptions(ctx, sid, chatID, checkoutAt, "日重置补全PC下班", true); err != nil {
			return closed, fmt.Errorf("complete agent work end %s: %w", sid, err)
		}
		closed++
	}
	return closed, rows.Err()
}

// backfillAttendanceCheckoutsForGroup copies checkout_time from closed work_sessions into
// attendance_records when the session was closed without syncing attendance (e.g. PC logout).
func (s *WorkforceTelegramService) backfillAttendanceCheckoutsForGroup(ctx context.Context, chatID, exportDate string) (int, error) {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE attendance_records ar
		SET checkout_time = ws.checkout_time,
			checkout_source = CASE
				WHEN ar.manually_edited THEN ar.checkout_source
				ELSE $3 END,
			shift_type = CASE
				WHEN ar.manually_edited THEN ar.shift_type
				ELSE COALESCE(NULLIF(TRIM(ar.shift_type), ''), NULLIF(TRIM(ws.shift_type), ''), ar.shift_type)
			END,
			updated_at = NOW()
		FROM work_sessions ws
		WHERE ar.employee_id = ws.employee_id
		  AND (`+tgGroupEmployeeBelongsToChatSQL("ws.employee_id", "$1")+` OR COALESCE(ws.source_chat_id,'') = $1)
		  AND ar.date = ws.attendance_date
		  AND ar.date = $2::date
		  AND ar.checkin_time IS NOT NULL
		  AND ar.checkout_time IS NULL
		  AND NOT ar.manually_edited
		  AND ws.status = 'closed'
		  AND ws.checkout_time IS NOT NULL
		  AND ws.source IN ('telegram','agent','web')`,
		chatID, exportDate, CheckoutSourceSystem)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// completeMissingAttendanceRecordCheckoutsForGroup fills attendance_records.checkout_time when
// check-in exists but checkout is still missing after session-level补全 (e.g. deleted/mismatched session).
func (s *WorkforceTelegramService) completeMissingAttendanceRecordCheckoutsForGroup(
	ctx context.Context,
	chatID, exportDate string,
	runOpts *DailyResetRunOptions,
) (int, error) {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" {
		return 0, nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return 0, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT ar.employee_id, ar.date::text, COALESCE(ws.shift_type,'day')
		FROM attendance_records ar
		LEFT JOIN LATERAL (
			SELECT shift_type FROM work_sessions ws
			WHERE ws.employee_id = ar.employee_id
			  AND ws.attendance_date = ar.date
			  AND ws.source IN ('telegram','agent','web')
			ORDER BY ws.checkin_time DESC
			LIMIT 1
		) ws ON TRUE
		WHERE ar.date = $2::date
		  AND (`+tgGroupEmployeeBelongsToChatSQL("ar.employee_id", "$1")+`)
		  AND ar.checkin_time IS NOT NULL
		  AND ar.checkout_time IS NULL
		  AND NOT ar.manually_edited
		  AND ar.status NOT IN ('leave','absent','rest_full','rest_half','off_post','resigned')`,
		chatID, exportDate)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	completed := 0
	for rows.Next() {
		var empID uuid.UUID
		var recordDate, shift string
		if rows.Scan(&empID, &recordDate, &shift) != nil {
			continue
		}
		if s.shouldPreserveOpenShiftDuringReset(ctx, empID, recordDate, shift) {
			continue
		}

		var openSID uuid.UUID
		err = s.db.QueryRowContext(ctx, `
			SELECT id FROM work_sessions
			WHERE employee_id = $1 AND attendance_date = $2::date AND status = 'open'
			ORDER BY checkin_time ASC LIMIT 1`, empID, recordDate).Scan(&openSID)
		if err == nil && openSID != uuid.Nil {
			checkoutAt := s.resetAutoCheckoutTime(ctx, settings, chatID, exportDate, shift)
			if recordDate != exportDate {
				checkoutAt = s.resetAutoCheckoutTime(ctx, settings, chatID, recordDate, shift)
			}
			if err := s.forceCheckoutWorkSessionAtWithOptions(ctx, openSID, chatID, checkoutAt, "日重置补全下班", true); err != nil {
				log.Printf("[Telegram] complete attendance via open session emp=%s date=%s: %v", empID, recordDate, err)
				continue
			}
			completed++
			continue
		}

		checkoutAt := s.resetAutoCheckoutTime(ctx, settings, chatID, exportDate, shift)
		if recordDate != exportDate {
			checkoutAt = s.resetAutoCheckoutTime(ctx, settings, chatID, recordDate, shift)
		}
		if err := s.workforce.syncAttendanceCheckout(ctx, empID, recordDate, checkoutAt, CheckoutSourceSystem); err != nil {
			log.Printf("[Telegram] complete attendance record emp=%s date=%s: %v", empID, recordDate, err)
			continue
		}
		completed++
	}
	return completed, nil
}
func (s *WorkforceTelegramService) ensureExportAttendanceComplete(ctx context.Context, dateFrom, dateTo, chatID string) error {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	loc := tgLoadLocation(settings.Timezone)
	today := time.Now().In(loc).Format("2006-01-02")
	runOpts := &DailyResetRunOptions{ManualTrigger: true}

	for d := dateFrom; ; {
		if d >= today {
			if d == dateTo {
				break
			}
		} else {
			trimmedChat := strings.TrimSpace(chatID)
			if trimmedChat != "" {
				if _, err := s.completeMissingWorkEndsForGroup(ctx, trimmedChat, d, runOpts); err != nil {
					return err
				}
				if _, err := s.completeMissingAgentWorkEndsForGroup(ctx, trimmedChat, d, runOpts); err != nil {
					return err
				}
				if _, err := s.backfillAttendanceCheckoutsForGroup(ctx, trimmedChat, d); err != nil {
					return err
				}
				if _, err := s.completeMissingAttendanceRecordCheckoutsForGroup(ctx, trimmedChat, d, runOpts); err != nil {
					return err
				}
			} else {
				if _, err := s.completeMissingWorkEndsForExportDate(ctx, d); err != nil {
					return err
				}
				if _, err := s.backfillAttendanceCheckoutsGlobal(ctx, d); err != nil {
					return err
				}
				if _, err := s.completeMissingAttendanceRecordCheckoutsGlobal(ctx, d); err != nil {
					return err
				}
			}
		}
		if d == dateTo {
			break
		}
		parsed, err := time.Parse("2006-01-02", d)
		if err != nil {
			break
		}
		d = parsed.AddDate(0, 0, 1).Format("2006-01-02")
	}
	return nil
}
func (s *WorkforceTelegramService) completeMissingWorkEndsForExportDate(ctx context.Context, exportDate string) (int, error) {
	exportDate = strings.TrimSpace(exportDate)
	if exportDate == "" {
		return 0, nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ws.id, ws.employee_id, COALESCE(ws.shift_type,'day'), ws.attendance_date::text, COALESCE(b.chat_id,'')
		FROM work_sessions ws
		LEFT JOIN employee_telegram_bindings b ON b.employee_id = ws.employee_id AND b.status = 1
		WHERE ws.source IN ('telegram','agent','web') AND ws.status = 'open'
		  AND ws.attendance_date = $1::date`, exportDate)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	closed := 0
	for rows.Next() {
		var sid, empID uuid.UUID
		var shift, recordDate, chatID string
		if rows.Scan(&sid, &empID, &shift, &recordDate, &chatID) != nil {
			continue
		}
		if s.shouldPreserveOpenShiftDuringReset(ctx, empID, recordDate, shift) {
			continue
		}
		checkoutAt := s.resetAutoCheckoutTime(ctx, settings, chatID, exportDate, shift)
		if err := s.forceCheckoutWorkSessionAtWithOptions(ctx, sid, chatID, checkoutAt, "导出补全下班", true); err != nil {
			log.Printf("[Telegram] export complete work end session=%s: %v", sid, err)
			continue
		}
		closed++
	}
	return closed, nil
}
func (s *WorkforceTelegramService) backfillAttendanceCheckoutsGlobal(ctx context.Context, exportDate string) (int, error) {
	exportDate = strings.TrimSpace(exportDate)
	if exportDate == "" {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE attendance_records ar
		SET checkout_time = ws.checkout_time,
			checkout_source = CASE
				WHEN ar.manually_edited THEN ar.checkout_source
				ELSE $2 END,
			shift_type = CASE
				WHEN ar.manually_edited THEN ar.shift_type
				ELSE COALESCE(NULLIF(TRIM(ar.shift_type), ''), NULLIF(TRIM(ws.shift_type), ''), ar.shift_type)
			END,
			updated_at = NOW()
		FROM work_sessions ws
		WHERE ar.employee_id = ws.employee_id
		  AND ar.date = ws.attendance_date
		  AND ar.date = $1::date
		  AND ar.checkin_time IS NOT NULL
		  AND ar.checkout_time IS NULL
		  AND NOT ar.manually_edited
		  AND ws.status = 'closed'
		  AND ws.checkout_time IS NOT NULL
		  AND ws.source IN ('telegram','agent','web')`,
		exportDate, CheckoutSourceSystem)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
func (s *WorkforceTelegramService) completeMissingAttendanceRecordCheckoutsGlobal(ctx context.Context, exportDate string) (int, error) {
	exportDate = strings.TrimSpace(exportDate)
	if exportDate == "" {
		return 0, nil
	}
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ar.employee_id, ar.date::text, COALESCE(ws.shift_type,'day')
		FROM attendance_records ar
		LEFT JOIN LATERAL (
			SELECT shift_type FROM work_sessions ws
			WHERE ws.employee_id = ar.employee_id
			  AND ws.attendance_date = ar.date
			  AND ws.source IN ('telegram','agent','web')
			ORDER BY ws.checkin_time DESC
			LIMIT 1
		) ws ON TRUE
		WHERE ar.date = $1::date
		  AND ar.checkin_time IS NOT NULL
		  AND ar.checkout_time IS NULL
		  AND NOT ar.manually_edited
		  AND ar.status NOT IN ('leave','absent','rest_full','rest_half','off_post','resigned')`,
		exportDate)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	completed := 0
	for rows.Next() {
		var empID uuid.UUID
		var recordDate, shift string
		if rows.Scan(&empID, &recordDate, &shift) != nil {
			continue
		}
		if s.shouldPreserveOpenShiftDuringReset(ctx, empID, recordDate, shift) {
			continue
		}
		chatID := s.getChatIDForEmployee(ctx, empID)
		checkoutAt := s.resetAutoCheckoutTime(ctx, settings, chatID, exportDate, shift)
		if err := s.workforce.syncAttendanceCheckout(ctx, empID, recordDate, checkoutAt, CheckoutSourceSystem); err != nil {
			log.Printf("[Telegram] export complete attendance emp=%s date=%s: %v", empID, recordDate, err)
			continue
		}
		completed++
	}
	return completed, nil
}
func (s *WorkforceTelegramService) collectGroupBindingWarnings(ctx context.Context, chatID, exportDate string) []string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	warnings := make([]string, 0)

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.name
		FROM employee_telegram_bindings b
		JOIN employees e ON e.id = b.employee_id
		WHERE b.status = 1 AND COALESCE(NULLIF(TRIM(b.chat_id), ''), '') = ''
		ORDER BY e.name`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil && strings.TrimSpace(name) != "" {
				warnings = append(warnings, fmt.Sprintf("员工 %s 未设置打卡群 chat_id", name))
			}
		}
	}

	rows2, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT e.name
		FROM wf_tg_activity_sessions s
		JOIN employees e ON e.id = s.employee_id
		JOIN employee_telegram_bindings b ON b.employee_id = s.employee_id AND b.status = 1
		WHERE s.attendance_date = $2::date
		  AND COALESCE(s.source_chat_id,'') = $1
		  AND TRIM(COALESCE(b.chat_id,'')) LIKE '-%'
		  AND TRIM(b.chat_id) != $1
		ORDER BY e.name`, chatID, exportDate)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var name string
			if rows2.Scan(&name) == nil && strings.TrimSpace(name) != "" {
				warnings = append(warnings, fmt.Sprintf("员工 %s 员工绑定打卡群与活动记录群不一致，请在 TG 配置中统一 chat_id", name))
			}
		}
	}

	rows3, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT e.name
		FROM work_sessions ws
		JOIN employees e ON e.id = ws.employee_id
		LEFT JOIN employee_telegram_bindings b ON b.employee_id = ws.employee_id AND b.status = 1
		WHERE ws.attendance_date = $2::date
		  AND ws.source IN ('telegram','agent','web')
		  AND EXISTS (
			SELECT 1 FROM employee_telegram_bindings bx
			WHERE bx.employee_id = ws.employee_id AND bx.status = 1 AND bx.chat_id = $1
		  )
		  AND COALESCE(NULLIF(TRIM(b.chat_id), ''), '') != ''
		  AND COALESCE(NULLIF(TRIM(b.chat_id), ''), '') != $1
		ORDER BY e.name`, chatID, exportDate)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var name string
			if rows3.Scan(&name) == nil && strings.TrimSpace(name) != "" {
				warnings = append(warnings, fmt.Sprintf("员工 %s 绑定群与导出群不匹配", name))
			}
		}
	}

	if len(warnings) > 12 {
		extra := len(warnings) - 12
		warnings = append(warnings[:12], fmt.Sprintf("…另有 %d 条绑定告警", extra))
	}
	return warnings
}
func (s *WorkforceTelegramService) resolveGroupResetExportDate(
	ctx context.Context,
	settings *TelegramSettings,
	chatID string,
	now time.Time,
	runOpts *DailyResetRunOptions,
) (exportDate, note string) {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	if runOpts != nil && strings.TrimSpace(runOpts.ForcedExportDate) != "" {
		return strings.TrimSpace(runOpts.ForcedExportDate), "补偿重置：指定统计日"
	}
	if runOpts != nil && runOpts.ManualTrigger {
		if runOpts.ExportBusinessToday {
			if period, err := s.DetermineCurrentPeriod(ctx, chatID, now); err == nil && period != nil && strings.TrimSpace(period.BusinessDate) != "" {
				return period.BusinessDate, "手动重置：导出当前业务日"
			}
			return now.Format("2006-01-02"), "手动重置：导出今日（日历日）"
		}
		return s.scheduledResetExportDate(settings, now), "手动重置：导出昨日"
	}
	exportDate = s.scheduledResetExportDateForChat(ctx, chatID, settings, now)
	return exportDate, "自动统计日（业务日）"
}
func (s *WorkforceTelegramService) forceCloseOpenActivitiesForGroup(ctx context.Context, chatID, exportDate string) (int, error) {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,employee_id,activity_code,activity_name,attendance_date::text,started_at,limit_seconds FROM wf_tg_activity_sessions WHERE status='open' AND source_chat_id=$1 AND attendance_date=$2::date`, chatID, exportDate)
	if err != nil {
		return 0, err
	}
	sessions := []TgActivitySession{}
	for rows.Next() {
		var session TgActivitySession
		if err = rows.Scan(&session.ID, &session.EmployeeID, &session.ActivityCode, &session.ActivityName, &session.AttendanceDate, &session.StartedAt, &session.LimitSeconds); err != nil {
			rows.Close()
			return 0, err
		}
		sessions = append(sessions, session)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, session := range sessions {
		if err = s.forceCloseActivityForReset(ctx, &session); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}
func (s *WorkforceTelegramService) purgeDailyDetailForGroup(ctx context.Context, chatID, exportDate string) error {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.mergeActivityStatsIntoMonthlyForGroupTx(ctx, tx, chatID, exportDate); err != nil {
		return err
	}
	if err := s.archiveClosedActivitySessionsByGroupDate(ctx, tx, chatID, exportDate); err != nil {
		return err
	}
	if err := s.archiveClosedWorkSessionsByGroupDate(ctx, tx, chatID, exportDate); err != nil {
		return err
	}

	// Persist shift + activity seconds onto attendance_records before deleting detail tables,
	// so 考勤查看 can still show 白班/夜班 and 纯工作时长 after daily reset purge.
	if _, err = tx.ExecContext(ctx, `
		UPDATE attendance_records ar
		SET shift_type = COALESCE(NULLIF(TRIM(ar.shift_type), ''), ws.shift_type),
			updated_at = NOW()
		FROM (
			SELECT DISTINCT ON (employee_id, attendance_date)
				employee_id, attendance_date,
				COALESCE(NULLIF(TRIM(shift_type), ''), 'day') AS shift_type
			FROM work_sessions
			WHERE attendance_date = $2::date
			  AND source IN ('telegram','agent','web')
			  AND (`+tgGroupEmployeeBelongsToChatSQL("employee_id", "$1")+` OR COALESCE(source_chat_id,'') = $1)
			  AND COALESCE(TRIM(shift_type), '') <> ''
			ORDER BY employee_id, attendance_date, checkin_time DESC
		) ws
		WHERE ar.employee_id = ws.employee_id
		  AND ar.date = ws.attendance_date
		  AND (ar.shift_type IS NULL OR TRIM(ar.shift_type) = '')
		  AND NOT ar.manually_edited`, chatID, exportDate); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE attendance_records ar
		SET activity_seconds = COALESCE(act.total_sec, 0),
			updated_at = NOW()
		FROM (
			SELECT a.employee_id, a.attendance_date, COALESCE(SUM(a.elapsed_seconds), 0)::int AS total_sec
			FROM wf_tg_activity_sessions a
			WHERE a.status = 'closed'
			  AND a.attendance_date = $2::date
			  AND (`+tgGroupEmployeeBelongsToChatSQL("a.employee_id", "$1")+` OR COALESCE(a.source_chat_id,'') = $1)
			GROUP BY a.employee_id, a.attendance_date
		) act
		WHERE ar.employee_id = act.employee_id
		  AND ar.date = act.attendance_date
		  AND NOT ar.manually_edited`, chatID, exportDate); err != nil {
		return err
	}

	// Days with check-in but zero closed activities still get a solidified 0.
	if _, err = tx.ExecContext(ctx, `
		UPDATE attendance_records ar
		SET activity_seconds = 0,
			updated_at = NOW()
		WHERE ar.date = $2::date
		  AND ar.checkin_time IS NOT NULL
		  AND ar.activity_seconds IS NULL
		  AND NOT ar.manually_edited
		  AND (`+tgGroupEmployeeBelongsToChatSQL("ar.employee_id", "$1")+`)`, chatID, exportDate); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		DELETE FROM wf_tg_activity_sessions
		WHERE status = 'closed' AND attendance_date = $2::date
		  AND COALESCE(source_chat_id,'') = $1`, chatID, exportDate)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM work_sessions ws
		WHERE ws.source IN ('telegram','agent','web') AND ws.status = 'closed'
		  AND ws.attendance_date = $2::date
		  AND (`+tgGroupEmployeeBelongsToChatSQL("ws.employee_id", "$1")+` OR COALESCE(ws.source_chat_id,'') = $1)`, chatID, exportDate)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM wf_tg_group_shift_state
		WHERE chat_id = $1 AND record_date <= $2::date`, chatID, exportDate)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM wf_tg_handover_cycles
		WHERE chat_id = $1 AND handover_date <= $2::date`, chatID, exportDate)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *WorkforceTelegramService) closeExpiredWorkSessionsForEmployee(ctx context.Context, empID uuid.UUID, chatID string) (int, error) {
	maxHours := telegramShiftStateMaxHours()
	cutoff := punchNow(ctx).Add(-time.Duration(maxHours) * time.Hour)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(shift_type,'day'), attendance_date::text
		FROM work_sessions
		WHERE employee_id = $1 AND source IN ('telegram','agent','web') AND status = 'open'
		  AND checkin_time < $2`, empID, cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	closed := 0
	for rows.Next() {
		var sid uuid.UUID
		var shift, recordDate string
		if rows.Scan(&sid, &shift, &recordDate) != nil {
			continue
		}
		if s.shouldPreserveOpenShiftDuringReset(ctx, empID, recordDate, shift) {
			continue
		}
		checkoutAt := punchNow(ctx)
		if err := s.forceCheckoutWorkSessionAt(ctx, sid, chatID, checkoutAt, fmt.Sprintf("班次超时自动下班（>%dh）", maxHours)); err != nil {
			log.Printf("[Telegram] expired shift close session=%s: %v", sid, err)
			continue
		}
		closed++
	}
	return closed, nil
}
func (s *WorkforceTelegramService) CloseExpiredWorkSessionsGlobal(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	maxHours := telegramShiftStateMaxHours()
	cutoff := punchNow(ctx).Add(-time.Duration(maxHours) * time.Hour)
	rows, err := s.db.QueryContext(ctx, `
		SELECT ws.id, ws.employee_id, COALESCE(b.chat_id,''), COALESCE(ws.shift_type,'day'), ws.attendance_date::text
		FROM work_sessions ws
		LEFT JOIN employee_telegram_bindings b ON b.employee_id = ws.employee_id AND b.status = 1
		WHERE ws.source IN ('telegram','agent','web') AND ws.status = 'open' AND ws.checkin_time < $1`, cutoff)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var sid, empID uuid.UUID
		var chatID, shift, recordDate string
		if rows.Scan(&sid, &empID, &chatID, &shift, &recordDate) != nil {
			continue
		}
		if s.shouldPreserveOpenShiftDuringReset(ctx, empID, recordDate, shift) {
			continue
		}
		if err := s.forceCheckoutWorkSessionAt(ctx, sid, chatID, punchNow(ctx), fmt.Sprintf("班次超时自动下班（>%dh）", maxHours)); err != nil {
			log.Printf("[Telegram] global expired shift close session=%s: %v", sid, err)
		}
	}
}

func resolveExpectedStartForWindow(now time.Time, startClock string, loc *time.Location, overnightContinuation bool) time.Time {
	expectedStart := combineDateTime(now, startClock, loc)
	if now.Before(expectedStart) && overnightContinuation {
		expectedStart = expectedStart.AddDate(0, 0, -1)
	}
	return expectedStart
}

// overnightCheckInContinuation is true only on the post-midnight tail of an overnight
// shift (e.g. night 00:30 still belongs to yesterday's 21:00 start). Day/middle before
// today's start must keep today's expected start so CheckInBefore works.
func overnightCheckInContinuation(settings *TelegramSettings, shift string, now time.Time) bool {
	if shift != "night" && shift != "middle" {
		return false
	}
	startClock := shiftStartClock(settings, shift)
	endClock := shiftEndClock(settings, shift)
	sh, sm, ok1 := parseClock(startClock)
	eh, em, ok2 := parseClock(endClock)
	if !ok1 || !ok2 {
		return shift == "night" && now.Hour() < 12
	}
	startMin := clockToMinutes(sh, sm)
	endMin := clockToMinutes(eh, em)
	if startMin <= endMin {
		return false
	}
	return minutesOfDay(now) <= endMin
}
func isWithinCheckInWindow(now, expectedStart time.Time, beforeMin, afterMin int) bool {
	earliest := expectedStart.Add(-time.Duration(beforeMin) * time.Minute)
	latest := expectedStart.Add(time.Duration(afterMin) * time.Minute)
	return !now.Before(earliest) && !now.After(latest)
}
func isWithinCheckOutWindow(now, expectedEnd time.Time, beforeMin, afterMin int) bool {
	earliest := expectedEnd.Add(-time.Duration(beforeMin) * time.Minute)
	latest := expectedEnd.Add(time.Duration(afterMin) * time.Minute)
	return !now.Before(earliest) && !now.After(latest)
}
func shiftStartClock(settings *TelegramSettings, shift string) string {
	switch shift {
	case "night":
		return settings.NightStart
	case "middle":
		if strings.TrimSpace(settings.MiddleStart) != "" {
			return settings.MiddleStart
		}
		return "13:00"
	default:
		return settings.DayStart
	}
}
func shiftEndClock(settings *TelegramSettings, shift string) string {
	switch shift {
	case "night":
		return settings.NightEnd
	case "middle":
		if strings.TrimSpace(settings.MiddleEnd) != "" {
			return settings.MiddleEnd
		}
		return "01:00"
	default:
		return settings.DayEnd
	}
}
func (s *WorkforceTelegramService) isCheckInWindowForShift(settings *TelegramSettings, grace TgGraceConfig, shift string, now time.Time) bool {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	startClock := shiftStartClock(settings, shift)
	expectedStart := resolveExpectedStartForWindow(now, startClock, loc, overnightCheckInContinuation(settings, shift, now))
	return isWithinCheckInWindow(now, expectedStart, grace.CheckInBefore, grace.CheckInAfter)
}
func (s *WorkforceTelegramService) validateCheckOutWindow(ctx context.Context, chatID string, settings *TelegramSettings, grace TgGraceConfig, shiftWindowDisabled bool, shift, recordDate string, now time.Time) error {
	if shiftWindowDisabled {
		return nil
	}
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	endClock := s.resolveWorkEndClock(ctx, chatID, settings, shift, recordDate)
	expectedEnd := s.expectedWorkEnd(ctx, chatID, settings, shift, recordDate, now)
	if isWithinCheckOutWindow(now, expectedEnd, grace.CheckOutBefore, grace.CheckOutAfter) {
		return nil
	}
	return &outsideWorkWindowError{Action: "checkout", Shift: shift, Clock: endClock}
}
func resolveNightShiftDetail(settings *TelegramSettings, now time.Time) string {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	nowMin := minutesOfDay(now)
	dayEndH, dayEndM, ok1 := parseClock(settings.DayEnd)
	nightStartH, nightStartM, ok2 := parseClock(settings.NightStart)
	if !ok1 || !ok2 {
		return "night_tonight"
	}
	dayEnd := clockToMinutes(dayEndH, dayEndM)
	nightStart := clockToMinutes(nightStartH, nightStartM)
	if nowMin >= nightStart {
		return "night_tonight"
	}
	if nowMin < dayEnd {
		return "night_last"
	}
	return "night_tonight"
}
func resolveMiddleShiftDetail(settings *TelegramSettings, now time.Time) string {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	nowMin := minutesOfDay(now)
	startH, startM, ok1 := parseClock(shiftStartClock(settings, "middle"))
	endH, endM, ok2 := parseClock(shiftEndClock(settings, "middle"))
	if !ok1 || !ok2 {
		return "middle_today"
	}
	startMin := clockToMinutes(startH, startM)
	endMin := clockToMinutes(endH, endM)
	// Overnight middle (e.g. 13:00–01:00): early-morning hours belong to previous business day.
	if startMin > endMin && nowMin <= endMin {
		return "middle_prev"
	}
	if nowMin >= startMin {
		return "middle_today"
	}
	return "middle_today"
}
func nightRecordDateForDetail(businessDate, detail string) string {
	_ = detail
	return businessDate
}

// resolveAttendanceDate 返回按班次解释的统一业务日（attendance_date）。
func resolveAttendanceDate(shift, shiftDetail string, period *TgPeriodInfo, now time.Time, loc *time.Location) string {
	now = now.In(loc)
	if shift == "day" {
		return now.Format("2006-01-02")
	}
	if shift == "middle" {
		if shiftDetail == "middle_prev" {
			return now.AddDate(0, 0, -1).Format("2006-01-02")
		}
		return now.Format("2006-01-02")
	}
	bd := now.Format("2006-01-02")
	if period != nil && strings.TrimSpace(period.BusinessDate) != "" {
		bd = period.BusinessDate
	}
	return nightRecordDateForDetail(bd, shiftDetail)
}
func (s *WorkforceTelegramService) resolveCurrentShiftWithGrace(settings *TelegramSettings, grace TgGraceConfig, now time.Time) (shift, shiftDetail string, err error) {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)

	if !settings.MultiShiftEnabled() {
		if s.isCheckInWindowForShift(settings, grace, "day", now) {
			return "day", "", nil
		}
		return "", "", ErrOutsideWorkWindow
	}

	if s.isCheckInWindowForShift(settings, grace, "day", now) {
		return "day", "", nil
	}
	if settings.TripleShiftEnabled && s.isCheckInWindowForShift(settings, grace, "middle", now) {
		return "middle", resolveMiddleShiftDetail(settings, now), nil
	}
	if s.isCheckInWindowForShift(settings, grace, "night", now) {
		return "night", resolveNightShiftDetail(settings, now), nil
	}
	return "", "", ErrOutsideWorkWindow
}
func (s *WorkforceTelegramService) isForcedShiftAllowed(settings *TelegramSettings, grace TgGraceConfig, forcedShift string, now time.Time) bool {
	forced, _ := normalizeShiftType(forcedShift)
	return s.isCheckInWindowForShift(settings, grace, forced, now)
}
func shiftDetailForCheckIn(settings *TelegramSettings, shift string, now time.Time) string {
	switch shift {
	case "night":
		return resolveNightShiftDetail(settings, now)
	case "middle":
		return resolveMiddleShiftDetail(settings, now)
	default:
		return ""
	}
}
func (s *WorkforceTelegramService) resolveShiftForCheckIn(settings *TelegramSettings, grace TgGraceConfig, shiftWindowDisabled bool, forcedShift string, now time.Time) (shift, shiftDetail string, err error) {
	if forcedShift != "" {
		shift, err = normalizeShiftType(forcedShift)
		if err != nil {
			return "", "", err
		}
		if shift == "middle" && !settings.TripleShiftEnabled {
			return "", "", fmt.Errorf("三班模式未启用")
		}
		if shiftWindowDisabled {
			return shift, shiftDetailForCheckIn(settings, shift, now), nil
		}
		if !s.isForcedShiftAllowed(settings, grace, forcedShift, now) {
			return "", "", &outsideWorkWindowError{
				Action: "checkin",
				Shift:  shift,
				Clock:  shiftStartClock(settings, shift),
			}
		}
		return shift, shiftDetailForCheckIn(settings, shift, now), nil
	}
	if shiftWindowDisabled {
		if settings.MultiShiftEnabled() {
			if shift, detail, err := s.resolveCurrentShiftWithGrace(settings, grace, now); err == nil {
				return shift, detail, nil
			}
		}
		return "day", "", nil
	}
	return s.resolveCurrentShiftWithGrace(settings, grace, now)
}
func shiftDetailLabel(detail string) string {
	switch strings.TrimSpace(detail) {
	case "night_last":
		return "（昨晚夜班）"
	case "night_tonight":
		return "（今晚夜班）"
	case "middle_prev":
		return "（昨日中班）"
	case "middle_today":
		return "（今日中班）"
	default:
		return ""
	}
}
func (s *WorkforceTelegramService) resolveWorkStartClock(ctx context.Context, chatID string, settings *TelegramSettings, shift, recordDate string) string {
	if settings == nil {
		settings = &TelegramSettings{}
	}
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	loc := tgLoadLocation(settings.Timezone)
	rd, rdErr := time.ParseInLocation("2006-01-02", recordDate, loc)

	switch shift {
	case "middle":
		return shiftStartClock(settings, "middle")
	case "night":
		start := settings.NightStart
		if cfg != nil && cfg.HandoverEnabled && rdErr == nil {
			if isHandoverDate(rd.AddDate(0, 0, 1), cfg.HandoverDay, cfg.HandoverMonth) && strings.TrimSpace(cfg.NightStartTime) != "" {
				start = cfg.NightStartTime
			}
		}
		if strings.TrimSpace(start) == "" {
			start = shiftStartClock(settings, "night")
		}
		return start
	default:
		start := settings.DayStart
		if cfg != nil && cfg.HandoverEnabled && rdErr == nil && isHandoverDate(rd, cfg.HandoverDay, cfg.HandoverMonth) && strings.TrimSpace(cfg.HandoverDayStartTime) != "" {
			start = cfg.HandoverDayStartTime
		}
		if strings.TrimSpace(start) == "" {
			start = shiftStartClock(settings, "day")
		}
		return start
	}
}
func (s *WorkforceTelegramService) resolveWorkEndClock(ctx context.Context, chatID string, settings *TelegramSettings, shift, recordDate string) string {
	if settings == nil {
		settings = &TelegramSettings{}
	}
	cfg, _ := s.GetHandoverConfig(ctx, chatID)
	loc := tgLoadLocation(settings.Timezone)
	rd, rdErr := time.ParseInLocation("2006-01-02", recordDate, loc)
	if cfg != nil && cfg.HandoverEnabled && rdErr == nil {
		if shift == "day" && isHandoverDate(rd, cfg.HandoverDay, cfg.HandoverMonth) && strings.TrimSpace(cfg.DayStartTime) != "" {
			return cfg.DayStartTime
		}
		if shift == "night" && isHandoverDate(rd.AddDate(0, 0, 1), cfg.HandoverDay, cfg.HandoverMonth) && strings.TrimSpace(cfg.HandoverDayStartTime) != "" {
			return cfg.HandoverDayStartTime
		}
	}
	return shiftEndClock(settings, shift)
}
func (s *WorkforceTelegramService) expectedWorkEnd(ctx context.Context, chatID string, settings *TelegramSettings, shift, recordDate string, now time.Time) time.Time {
	loc := tgLoadLocation(settings.Timezone)
	startClock := s.resolveWorkStartClock(ctx, chatID, settings, shift, recordDate)
	endClock := s.resolveWorkEndClock(ctx, chatID, settings, shift, recordDate)
	return resolveWorkEndExpectedTime(now, recordDate, shift, startClock, endClock, loc)
}
func (s *WorkforceTelegramService) recordDateIsHandoverDay(ctx context.Context, chatID, recordDate string, loc *time.Location) bool {
	cfg, err := s.GetHandoverConfig(ctx, chatID)
	if err != nil || cfg == nil || !cfg.HandoverEnabled {
		return false
	}
	rd, err := time.ParseInLocation("2006-01-02", recordDate, loc)
	if err != nil {
		return false
	}
	return isHandoverDate(rd, cfg.HandoverDay, cfg.HandoverMonth)
}

func (s *WorkforceTelegramService) tickActivityTimers(ctx context.Context) {
	if !TelegramAttendanceEnabled() {
		return
	}
	s.scanAndCreateAttendanceReviewTasks(ctx)
	forceBackSec := telegramForceBackOvertimeSeconds()
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.employee_id, s.activity_code, s.activity_name, s.started_at, s.limit_seconds,
			COALESCE(s.source_chat_id,''), e.name, COALESCE(s.shift_type,'day'),
			COALESCE(s.limit_warning_sent, FALSE), COALESCE(s.overtime_immediate_sent, FALSE),
			COALESCE(s.overtime_5min_sent, FALSE), COALESCE(s.last_overtime_reminder_min, -1),
			COALESCE(s.force_back_sent, FALSE)
		FROM wf_tg_activity_sessions s
		JOIN employees e ON e.id = s.employee_id
		WHERE s.status = 'open' AND s.limit_seconds > 0`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var sid, empID uuid.UUID
		var code, name, chatID, empName, shiftType string
		var started time.Time
		var limitSec int
		var warned, ot0, ot5, forceBackSent bool
		var lastReminderMin int
		if rows.Scan(&sid, &empID, &code, &name, &started, &limitSec, &chatID, &empName, &shiftType,
			&warned, &ot0, &ot5, &lastReminderMin, &forceBackSent) != nil {
			continue
		}
		elapsed := int(time.Since(started).Seconds())
		warnAt := limitSec - 60
		if warnAt < 0 {
			warnAt = 0
		}

		if !warned && elapsed >= warnAt && elapsed < limitSec {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_activity_sessions SET limit_warning_sent = TRUE WHERE id = $1`, sid)
			tgUID := s.employeeTelegramUserID(ctx, empID)
			mode := s.GetReplyLangForEmployee(ctx, empID)
			groupHTML := buildActivitySoonTimeoutHTML(mode, tgUID, empName, name, tgReplyShiftLabel(shiftType, mode))
			// 即将超时：打卡群 + 员工私信 + PC 客户端
			s.sendActivityTimerHTML(ctx, chatID, empID, sid, groupHTML, true, mode)
			s.syncAgentActivityNotify(empID, empName, name, "即将超时",
				fmt.Sprintf("班次：%s", shiftLabel(shiftType)), sid)
			continue
		}

		overtimeSec := elapsed - limitSec
		if overtimeSec < 0 {
			continue
		}

		if overtimeSec >= forceBackSec && !forceBackSent {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_activity_sessions SET force_back_sent = TRUE WHERE id = $1`, sid)
			sess, err := s.closeActivitySession(ctx, sid, true, "system")
			if err != nil {
				log.Printf("[Telegram] force back activity %s: %v", sid, err)
				continue
			}
			tgUID := s.employeeTelegramUserID(ctx, empID)
			mode := s.GetReplyLangForEmployee(ctx, empID)
			cur := s.FineCurrencyCode(ctx)
			fineText := ""
			if sess.FineAmount > 0 {
				fineText = FormatFineAmountWithUnit(sess.FineAmount, cur)
			}
			msg := buildActivityForceBackHTML(mode, tgUID, empName, name, tgReplyShiftLabel(shiftType, mode), forceBackSec/60, fineText)
			s.sendActivityTimerHTML(ctx, chatID, empID, sid, msg, false, mode)
			userID, username := lookupEmployeeTelegramProfile(ctx, s.db, empID.String(), empName)
			if tgUID > 0 {
				if userID != tgUID {
					username = ""
				}
				userID = tgUID
			}
			pushHTML := buildActivityOvertimeBackPushHTML(s.getChatTitle(ctx, chatID), userID, username, empName, name, time.Now(), overtimeSec, sess.FineAmount, cur)
			s.pushHTMLToChatTargets(ctx, chatID, pushHTML, false)
			extra := fmt.Sprintf("班次：%s\n超时超过 %d 分钟，系统已自动回座", shiftLabel(shiftType), forceBackSec/60)
			if sess.FineAmount > 0 {
				extra += fmt.Sprintf("\n罚款 %s", FormatFineMoney(sess.FineAmount, cur))
			}
			s.syncAgentActivityNotify(empID, empName, name, "自动安全回座", extra, sid)
			continue
		}

		overtimeMin := overtimeSec / 60
		variant := -1
		updateOT0, updateOT5 := false, false
		updateLastMin := lastReminderMin

		if !ot0 && overtimeMin >= 0 {
			variant = 0
			updateOT0 = true
			updateLastMin = 0
		} else if !ot5 && overtimeMin >= 5 {
			variant = 5
			updateOT5 = true
			updateLastMin = 5
		} else if overtimeMin >= 10 && overtimeMin%10 == 0 && overtimeMin != lastReminderMin {
			variant = overtimeMin
			updateLastMin = overtimeMin
		}

		if variant < 0 {
			continue
		}
		tgUID := s.employeeTelegramUserID(ctx, empID)
		mode := s.GetReplyLangForEmployee(ctx, empID)
		pendingHTML := buildActivityOvertimeWarnHTML(mode, tgUID, empName, name, tgReplyShiftLabel(shiftType, mode), overtimeMin, variant)

		if updateOT0 {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_activity_sessions SET overtime_immediate_sent = TRUE, last_overtime_reminder_min = $2 WHERE id = $1`, sid, updateLastMin)
		} else if updateOT5 {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_activity_sessions SET overtime_5min_sent = TRUE, last_overtime_reminder_min = $2 WHERE id = $1`, sid, updateLastMin)
		} else {
			_, _ = s.db.ExecContext(ctx, `UPDATE wf_tg_activity_sessions SET last_overtime_reminder_min = $2 WHERE id = $1`, sid, updateLastMin)
		}
		s.sendActivityTimerHTML(ctx, chatID, empID, sid, pendingHTML, true, mode)
		extra := fmt.Sprintf("班次：%s\n已超时 %d 分钟，请立即回座", shiftLabel(shiftType), overtimeMin)
		s.syncAgentActivityNotify(empID, empName, name, "超时警告", extra, sid)
	}
}
func (s *WorkforceTelegramService) employeeTelegramUserID(ctx context.Context, empID uuid.UUID) int64 {
	return lookupEmployeeTelegramUserID(ctx, s.db, empID.String(), "")
}
func (s *WorkforceTelegramService) syncAgentActivityNotify(
	empID uuid.UUID,
	employeeName, activityName, event, extra string,
	sessionID uuid.UUID,
) {
	if s == nil || s.agentNotify == nil {
		return
	}
	s.agentNotify.PushActivityEvent(empID, employeeName, activityName, event, extra, sessionID)
}
func (s *WorkforceTelegramService) sendActivityTimerHTML(ctx context.Context, chatID string, empID, sessionID uuid.UUID, htmlText string, withInlineBack bool, mode TgLangMode) {
	markup := interface{}(nil)
	if withInlineBack {
		markup = inlineQuickBackKeyboard(sessionID.String(), mode)
	}
	replyTo := s.activitySessionTelegramMessageID(ctx, sessionID)
	if strings.TrimSpace(chatID) != "" {
		_ = s.SendTelegramHTMLReply(ctx, chatID, htmlText, replyTo, markup)
	}
	var tgUID int64
	if s.db.QueryRowContext(ctx, `
		SELECT telegram_user_id FROM employee_telegram_bindings WHERE employee_id = $1 AND status = 1 LIMIT 1`, empID).Scan(&tgUID) == nil && tgUID > 0 {
		_ = s.SendTelegramHTML(ctx, fmt.Sprintf("%d", tgUID), htmlText, markup)
	}
}
func (s *WorkforceTelegramService) activitySessionTelegramMessageID(ctx context.Context, sessionID uuid.UUID) int64 {
	var msgID sql.NullInt64
	_ = s.db.QueryRowContext(ctx, `
		SELECT telegram_message_id FROM wf_tg_activity_sessions WHERE id = $1`, sessionID).Scan(&msgID)
	if msgID.Valid && msgID.Int64 > 0 {
		return msgID.Int64
	}
	return 0
}
func (s *WorkforceTelegramService) getChatTitle(ctx context.Context, chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ""
	}
	var title string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(chat_title,'') FROM wf_tg_groups WHERE chat_id = $1`, chatID).Scan(&title)
	if strings.TrimSpace(title) != "" {
		return title
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(chat_title,'') FROM wf_telegram_discovered_chats WHERE chat_id = $1`, chatID).Scan(&title)
	return title
}

func groupPunchTemplateLang(mode TgLangMode) TgLangMode {
	switch mode {
	case TgLangVi:
		return TgLangVi
	case TgLangEn:
		return TgLangEn
	default:
		return TgLangZh
	}
}

// resolveEmployeeGroupNotifyLang picks language for PC/admin group punch cards.
// Employee /lang wins; otherwise group botUiLang (both → zh).
func resolveEmployeeGroupNotifyLang(hasPersonalPref bool, personal, group TgLangMode) TgLangMode {
	if hasPersonalPref {
		return personal
	}
	return groupPunchTemplateLang(group)
}

// replyLangForEmployeeGroupNotify is for PC/后台 group announcements of this employee.
func (s *WorkforceTelegramService) employeeLanguagePreference(ctx context.Context, empID uuid.UUID) (sql.NullString, error) {
	if empID == uuid.Nil {
		return sql.NullString{}, nil
	}
	return telegramRequestRead(ctx, s, "employee_lang:"+empID.String(), func() (sql.NullString, error) {
		var lang sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT preferred_lang FROM wf_tg_employee_lang_prefs WHERE employee_id=$1`, empID).Scan(&lang)
		if err == sql.ErrNoRows {
			err = nil
		}
		return lang, err
	})
}

func (s *WorkforceTelegramService) replyLangForEmployeeGroupNotify(ctx context.Context, empID uuid.UUID, chatID string) TgLangMode {
	if pref, err := s.employeeLanguagePreference(ctx, empID); err == nil && pref.Valid && strings.TrimSpace(pref.String) != "" {
		return groupPunchTemplateLang(normalizeTgLangMode(pref.String))
	}
	return groupPunchTemplateLang(s.GetLangMode(ctx, chatID))
}

// GetReplyLangForEmployee returns zh/vi/en for punch reply HTML for this employee.
// Employees without /lang default to Chinese.
func (s *WorkforceTelegramService) GetReplyLangForEmployee(ctx context.Context, empID uuid.UUID) TgLangMode {
	if empID == uuid.Nil {
		return TgLangZh
	}
	lang, err := s.employeeLanguagePreference(ctx, empID)
	if err != nil || !lang.Valid {
		return TgLangZh
	}
	switch normalizeTgLangMode(lang.String) {
	case TgLangVi:
		return TgLangVi
	case TgLangEn:
		return TgLangEn
	default:
		return TgLangZh
	}
}

// GetReplyLangForUser resolves the current employee for this Telegram account, then returns
// that employee's preferred lang. Unbound / PC-not-logged-in workstation → Chinese default.
func (s *WorkforceTelegramService) GetReplyLangForUser(ctx context.Context, telegramUserID int64) TgLangMode {
	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil || empID == uuid.Nil {
		return TgLangZh
	}
	return s.GetReplyLangForEmployee(ctx, empID)
}

// keyboardLangForUser uses the resolved employee's /lang when set; otherwise group/bot UI language.
func (s *WorkforceTelegramService) keyboardLangForUser(ctx context.Context, chatID string, telegramUserID int64) TgLangMode {
	lang, _ := telegramRequestRead(ctx, s, fmt.Sprintf("keyboard_lang:%s:%d", chatID, telegramUserID), func() (TgLangMode, error) { return s.loadKeyboardLangForUser(ctx, chatID, telegramUserID), nil })
	return lang
}
func (s *WorkforceTelegramService) loadKeyboardLangForUser(ctx context.Context, chatID string, telegramUserID int64) TgLangMode {
	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil || empID == uuid.Nil {
		return s.GetLangMode(ctx, chatID)
	}
	pref, err := s.employeeLanguagePreference(ctx, empID)
	if err != nil || !pref.Valid || strings.TrimSpace(pref.String) == "" {
		return s.GetLangMode(ctx, chatID)
	}
	return groupPunchTemplateLang(normalizeTgLangMode(pref.String))
}

// DisplayLangForTelegram is the language for bot keyboards / local bot cache.
// Employee /lang wins; otherwise group botUiLang (same as keyboard). Not used for punch HTML
// (punch replies without a personal pref still default to Chinese).
func (s *WorkforceTelegramService) DisplayLangForTelegram(ctx context.Context, chatID string, telegramUserID int64) TgLangMode {
	return s.keyboardLangForUser(ctx, chatID, telegramUserID)
}
func normalizeUserReplyLang(raw string) (TgLangMode, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "zh", "cn", "chinese", "中文":
		return TgLangZh, true
	case "vi", "vn", "vietnamese", "越南语", "越":
		return TgLangVi, true
	case "en", "english", "英语", "英文", "英":
		return TgLangEn, true
	default:
		return TgLangZh, false
	}
}

// SetEmployeePreferredLang stores /lang preference for one employee (zh, vi, or en).
func (s *WorkforceTelegramService) SetEmployeePreferredLang(ctx context.Context, empID uuid.UUID, lang string) error {
	if empID == uuid.Nil {
		return fmt.Errorf("invalid employee id")
	}
	mode, ok := normalizeUserReplyLang(lang)
	if !ok {
		return ErrUnsupportedReplyLang
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wf_tg_employee_lang_prefs (employee_id, preferred_lang, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (employee_id) DO UPDATE SET
			preferred_lang = EXCLUDED.preferred_lang,
			updated_at = NOW()`, empID, string(mode))
	if err == nil {
		if cache, ok := ctx.Value(telegramRequestCacheKey{}).(*sync.Map); ok {
			cache.Clear()
		}
	}
	return err
}

// SetUserPreferredLang resolves the employee currently associated with this Telegram account
// (personal bind, or workstation with PC login) and stores language for that employee.
func (s *WorkforceTelegramService) SetUserPreferredLang(ctx context.Context, telegramUserID int64, lang string) error {
	if telegramUserID <= 0 {
		return fmt.Errorf("invalid telegram user id")
	}
	if _, ok := normalizeUserReplyLang(lang); !ok {
		return ErrUnsupportedReplyLang
	}
	empID, err := s.ResolveEmployeeByTelegramUserID(ctx, telegramUserID)
	if err != nil {
		return err
	}
	if empID == uuid.Nil {
		return ErrPCLoginRequired
	}
	return s.SetEmployeePreferredLang(ctx, empID, lang)
}
func tgReplyLabel(zh, vi, en string, mode TgLangMode) string {
	switch mode {
	case TgLangVi:
		if strings.TrimSpace(vi) != "" {
			return vi
		}
	case TgLangEn:
		if strings.TrimSpace(en) != "" {
			return en
		}
	}
	return zh
}
func tgReplyShiftLabel(shift string, mode TgLangMode) string {
	switch shift {
	case "night":
		return tgReplyLabel("夜班", "Ca đêm", "Night shift", mode)
	case "middle":
		return tgReplyLabel("中班", "Ca giữa", "Middle shift", mode)
	default:
		return tgReplyLabel("白班", "Ca ngày", "Day shift", mode)
	}
}
func tgReplyUnlimitedLabel(mode TgLangMode) string {
	return tgReplyLabel("不限", "Không giới hạn", "Unlimited", mode)
}
func formatDurationForLang(seconds int, mode TgLangMode) string {
	if seconds <= 0 {
		switch mode {
		case TgLangVi:
			return "0 giây"
		case TgLangEn:
			return "0s"
		default:
			return "0秒"
		}
	}
	m, sec := seconds/60, seconds%60
	h := m / 60
	m = m % 60
	switch mode {
	case TgLangVi:
		if h > 0 {
			return fmt.Sprintf("%d giờ %d phút %d giây", h, m, sec)
		}
		if m > 0 {
			return fmt.Sprintf("%d phút %d giây", m, sec)
		}
		return fmt.Sprintf("%d giây", sec)
	case TgLangEn:
		if h > 0 {
			return fmt.Sprintf("%dh %dm %ds", h, m, sec)
		}
		if m > 0 {
			return fmt.Sprintf("%dm %ds", m, sec)
		}
		return fmt.Sprintf("%ds", sec)
	default:
		if h > 0 {
			return fmt.Sprintf("%d小时%d分%d秒", h, m, sec)
		}
		if m > 0 {
			return fmt.Sprintf("%d分%d秒", m, sec)
		}
		return fmt.Sprintf("%d秒", sec)
	}
}

// ---- Source: backend/services/workforce_tg_work_archive.go ----
const archiveWorkSessionsInsertSQL = `
		INSERT INTO work_sessions_archive (
			id, employee_id, attendance_date, device_id,
			checkin_time, checkout_time, status, recording_id,
			source, shift_type, shift_detail, source_chat_id,
			created_at, updated_at, archived_at
		)
		SELECT
			ws.id, ws.employee_id, ws.attendance_date, ws.device_id,
			ws.checkin_time, ws.checkout_time, ws.status, ws.recording_id,
			ws.source, COALESCE(ws.shift_type,'day'), ws.shift_detail, ws.source_chat_id,
			ws.created_at, ws.updated_at, NOW()
		FROM work_sessions ws
		WHERE %s
		ON CONFLICT (id) DO NOTHING`

func (s *WorkforceTelegramService) archiveClosedWorkSessionsByGroupDate(ctx context.Context, tx execContext, chatID, exportDate string) error {
	chatID = strings.TrimSpace(chatID)
	exportDate = strings.TrimSpace(exportDate)
	if chatID == "" || exportDate == "" {
		return nil
	}
	where := `ws.source IN ('telegram','agent','web') AND ws.status = 'closed'
		AND ws.attendance_date = $2::date
		AND (` + tgGroupEmployeeBelongsToChatSQL("ws.employee_id", "$1") + ` OR COALESCE(ws.source_chat_id,'') = $1)`
	_, err := tx.ExecContext(ctx, fmt.Sprintf(archiveWorkSessionsInsertSQL, where), chatID, exportDate)
	return err
}

// archiveWorkSessionsByEmployeeDateWhere only closed rows — open ghosts must never enter archive.
const archiveWorkSessionsByEmployeeDateWhere = `ws.attendance_date = $2::date AND ws.employee_id = $1 AND ws.status = 'closed'`

func (s *WorkforceTelegramService) archiveWorkSessionsByEmployeeDate(ctx context.Context, tx execContext, employeeID uuid.UUID, attendanceDate string) error {
	if employeeID == uuid.Nil || strings.TrimSpace(attendanceDate) == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(archiveWorkSessionsInsertSQL, archiveWorkSessionsByEmployeeDateWhere), employeeID, attendanceDate)
	return err
}

// ---- Source: backend/services/workforce_tg_worker_conn.go ----
const workerOnlineThreshold = 2 * time.Minute
const pairingCodeTTL = 15 * time.Minute
const pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type WorkerConnectionInfo struct {
	Online           bool
	LastSeenAt       *time.Time
	HasWorkerSecret  bool
	HasActivePairing bool
	PairingExpiresAt *time.Time
}

func (s *WorkforceTelegramService) EnsureWorkerSecret(ctx context.Context) (string, error) {
	var secret sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT worker_secret FROM wf_telegram_settings ORDER BY updated_at DESC LIMIT 1`).Scan(&secret)
	if err != nil {
		return "", err
	}
	if secret.Valid && strings.TrimSpace(secret.String) != "" {
		return strings.TrimSpace(secret.String), nil
	}
	newSecret, err := generateWorkerSecret()
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE wf_telegram_settings SET worker_secret = $1, updated_at = NOW()`, newSecret)
	s.invalidateTelegramSettingsCache()
	return newSecret, err
}
func (s *WorkforceTelegramService) TouchWorkerHeartbeat(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE wf_telegram_settings SET worker_last_seen_at = NOW(), updated_at = NOW()
		WHERE id = (SELECT id FROM wf_telegram_settings ORDER BY updated_at DESC, id DESC LIMIT 1)`)
	return err
}
func (s *WorkforceTelegramService) GetWorkerConnectionInfo(ctx context.Context) (*WorkerConnectionInfo, error) {
	var lastSeen sql.NullTime
	var secret sql.NullString
	var pairCode sql.NullString
	var pairExp sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT worker_last_seen_at, worker_secret, worker_pairing_code, worker_pairing_expires_at
		FROM wf_telegram_settings ORDER BY updated_at DESC LIMIT 1`).Scan(&lastSeen, &secret, &pairCode, &pairExp)
	if err != nil {
		return nil, err
	}
	info := &WorkerConnectionInfo{
		HasWorkerSecret: secret.Valid && strings.TrimSpace(secret.String) != "",
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		info.LastSeenAt = &t
		info.Online = time.Since(t) < workerOnlineThreshold
	}
	if pairCode.Valid && pairExp.Valid && time.Now().UTC().Before(pairExp.Time) {
		info.HasActivePairing = true
		t := pairExp.Time
		info.PairingExpiresAt = &t
	}
	return info, nil
}

// Read credentials together; these remain fresh on every worker configuration request.
func (s *WorkforceTelegramService) GetWorkerReplyCredentials(ctx context.Context) (webhookSecret, token string, err error) {
	settings, err := telegramRequestRead(ctx, s, "settings", func() (*TelegramSettings, error) { return s.loadTelegramSettingsCached(ctx) })
	if err != nil {
		return "", "", err
	}
	webhookSecret = strings.TrimSpace(settings.webhookSecret)
	if settings.Enabled && settings.botTokenEnc != "" {
		token, err = DecryptSecret(settings.botTokenEnc)
	}
	return
}
func (s *WorkforceTelegramService) GetBotTokenForWorker(ctx context.Context) (string, error) {
	var enc sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT bot_token_enc FROM wf_telegram_settings
		WHERE enabled = TRUE AND bot_token_enc IS NOT NULL AND bot_token_enc != ''
		ORDER BY updated_at DESC LIMIT 1`).Scan(&enc)
	if err != nil || !enc.Valid {
		return "", errors.New("bot token not configured")
	}
	return DecryptSecret(enc.String)
}

// ---- Source: backend/services/workforce_tg_worker_error.go ----
type outsideWorkWindowError struct {
	Action string
	Shift  string
	Clock  string
}

func (e *outsideWorkWindowError) Error() string {
	if e == nil {
		return ErrOutsideWorkWindow.Error()
	}
	shift := shiftLabel(e.Shift)
	if e.Action == "checkout" {
		return fmt.Sprintf("当前不在%s签退窗口，请在 %s 前后（含宽容）内打下班卡", shift, e.Clock)
	}
	return fmt.Sprintf("当前不在%s上班窗口，请在 %s 前后（含宽容）内打%s卡", shift, e.Clock, shift)
}
func (e *outsideWorkWindowError) Unwrap() error { return ErrOutsideWorkWindow }

type otherWorkSessionStillOpenError struct {
	Shift  string
	Detail string
	Date   string
}

func (e *otherWorkSessionStillOpenError) Error() string {
	if e == nil {
		return ErrOtherWorkSessionOpen.Error()
	}
	return fmt.Sprintf("%s：请先下班当前%s%s（%s）", ErrOtherWorkSessionOpen.Error(), shiftLabel(e.Shift), shiftDetailLabel(e.Detail), e.Date)
}
func (e *otherWorkSessionStillOpenError) Unwrap() error { return ErrOtherWorkSessionOpen }
func warnLabel(zh, vi, en string, mode TgLangMode) string {
	return "⚠️ " + tgReplyLabel(zh, vi, en, mode)
}
func failLabel(zh, vi, en string, mode TgLangMode) string {
	return "❌ " + tgReplyLabel(zh, vi, en, mode)
}
func shiftDetailLabelForLang(detail string, mode TgLangMode) string {
	switch strings.TrimSpace(detail) {
	case "night_last":
		return tgReplyLabel("（昨晚夜班）", "（ca đêm hôm qua）", " (last night)", mode)
	case "night_tonight":
		return tgReplyLabel("（今晚夜班）", "（ca đêm nay）", " (tonight)", mode)
	case "middle_prev":
		return tgReplyLabel("（昨日中班）", "（ca giữa hôm qua）", " (yesterday middle)", mode)
	case "middle_today":
		return tgReplyLabel("（今日中班）", "（ca giữa hôm nay）", " (today middle)", mode)
	default:
		return ""
	}
}

// TelegramWorkerErrorHTTP maps punch/activity errors to API status and business code.
func TelegramWorkerErrorHTTP(err error) (status, code int) {
	switch {
	case errors.Is(err, ErrTelegramDisabled), errors.Is(err, ErrWebPunchOnlyMode),
		errors.Is(err, ErrActivityTypeDisabled):
		return 403, 1003
	case errors.Is(err, ErrTelegramNotBound), errors.Is(err, ErrNoOpenSession),
		errors.Is(err, ErrActivityNotActive), errors.Is(err, ErrActivityTypeNotFound):
		return 404, 2001
	case errors.Is(err, ErrSessionExists), errors.Is(err, ErrWorkSessionOpenOnPC),
		errors.Is(err, ErrWorkSessionOpenOnTG), errors.Is(err, ErrActivityAlreadyActive),
		errors.Is(err, ErrOpenActivityBlocksCheckout), errors.Is(err, ErrActivityLimitReached),
		errors.Is(err, ErrShiftAlreadyCompleted), errors.Is(err, ErrTelegramBoundToWorkstation),
		errors.Is(err, ErrTelegramBoundToPersonal), errors.Is(err, ErrDeviceTelegramAlreadyBound),
		errors.Is(err, ErrTelegramAlreadyBound), errors.Is(err, ErrActivityConcurrentLimit),
		errors.Is(err, ErrOtherWorkSessionOpen):
		return 409, 1004
	case errors.Is(err, ErrBackProcessing), errors.Is(err, ErrOperationBusy):
		return 429, 1005
	case errors.Is(err, ErrActivityBackNotOwner):
		return 403, 1006
	case errors.Is(err, ErrActivityNotInWork), errors.Is(err, ErrOutsideWorkWindow),
		errors.Is(err, ErrDualShiftDisabled), errors.Is(err, ErrPCLoginRequired),
		errors.Is(err, ErrWorkstationDeviceRequired), errors.Is(err, ErrWorkstationDeviceNotBound),
		errors.Is(err, ErrMultipleWorkstationLogins), errors.Is(err, ErrPCLoginMismatch),
		errors.Is(err, ErrCredentialsEmployeeMismatch), errors.Is(err, ErrWorkstationNotConfigured),
		errors.Is(err, ErrWorkstationInviteMismatch), errors.Is(err, ErrInvalidCredentials),
		errors.Is(err, ErrInviteNotFound), errors.Is(err, ErrInviteExpired),
		errors.Is(err, ErrInviteRevoked), errors.Is(err, ErrUnsupportedReplyLang):
		return 400, 1001
	default:
		return 500, 5000
	}
}
func FormatTelegramActivityBackNotOwner(name string, mode TgLangMode) string {
	if strings.TrimSpace(name) == "" {
		name = tgReplyLabel("该用户", "Người dùng này", "This user", mode)
	}
	return fmt.Sprintf(
		tgReplyLabel("%s，这不是您的活动按钮", "%s, đây không phải nút hoạt động của bạn", "%s, this is not your activity button", mode),
		name,
	)
}

// FormatTelegramWorkerError returns the user-facing punch/activity error in zh/vi/en.
func FormatTelegramWorkerError(err error, mode TgLangMode) string {
	if err == nil {
		return ""
	}
	var window *outsideWorkWindowError
	if errors.As(err, &window) && window != nil {
		shift := tgReplyShiftLabel(window.Shift, mode)
		clock := strings.TrimSpace(window.Clock)
		if window.Action == "checkout" {
			return fmt.Sprintf(
				tgReplyLabel(
					"⚠️ 当前不在%s签退窗口，请在 %s 前后（含宽容）内打下班卡",
					"⚠️ Hiện không trong khung tan ca %s, hãy chấm tan ca quanh %s (đã gồm dung sai)",
					"⚠️ Not in the %s clock-out window. Clock out around %s (including grace)",
					mode,
				), shift, clock)
		}
		return fmt.Sprintf(
			tgReplyLabel(
				"⚠️ 当前不在%s上班窗口，请在 %s 前后（含宽容）内打%s卡",
				"⚠️ Hiện không trong khung đi làm %s, hãy chấm công quanh %s",
				"⚠️ Not in the %s clock-in window. Clock in around %s",
				mode,
			), shift, clock, shift)
	}
	var other *otherWorkSessionStillOpenError
	if errors.As(err, &other) && other != nil {
		return fmt.Sprintf(
			tgReplyLabel(
				"⚠️ 请先下班当前%s%s（%s）",
				"⚠️ Hãy tan ca ca hiện tại %s%s (%s) trước",
				"⚠️ Please clock out the current %s%s (%s) first",
				mode,
			), tgReplyShiftLabel(other.Shift, mode), shiftDetailLabelForLang(other.Detail, mode), other.Date)
	}

	switch {
	case errors.Is(err, ErrTelegramDisabled):
		return warnLabel("Telegram 考勤未启用", "Chấm công Telegram chưa bật", "Telegram attendance is disabled", mode)
	case errors.Is(err, ErrWebPunchOnlyMode):
		return warnLabel("当前为仅后台打卡模式，请使用网页端打卡", "Hiện chỉ cho phép chấm công trên web", "Web-only punch mode — use the web console", mode)
	case errors.Is(err, ErrTelegramNotBound):
		return warnLabel(
			"未绑定。个人 TG：/bind 邀请码 或 /bind 账号；多设备工位：/bind_multi 账号 或 /bind_multi 邀请码；单设备工位：/bind_workstation 邀请码",
			"Chưa gắn. Cá nhân: /bind mã mời hoặc /bind tài khoản; nhiều máy: /bind_multi; một máy: /bind_workstation",
			"Not bound. Personal: /bind invite or /bind account; multi-PC: /bind_multi; single PC: /bind_workstation",
			mode,
		)
	case errors.Is(err, ErrSessionExists):
		return warnLabel("已在上班状态", "Bạn đã đang đi làm", "You are already clocked in", mode)
	case errors.Is(err, ErrWorkSessionOpenOnPC):
		return warnLabel("PC 客户端已上班，请先在客户端下班", "PC đã đi làm, hãy tan ca trên máy trước", "PC client is clocked in — clock out there first", mode)
	case errors.Is(err, ErrWorkSessionOpenOnTG):
		return warnLabel("Telegram 已上班，请先在群内下班", "Telegram đã đi làm, hãy tan ca trong nhóm trước", "Telegram is clocked in — clock out in the group first", mode)
	case errors.Is(err, ErrNoOpenSession):
		return warnLabel("当前没有进行中的上班会话", "Hiện không có ca đang làm", "No open work session", mode)
	case errors.Is(err, ErrActivityNotInWork):
		return warnLabel("请先上班后再开始活动", "Hãy đi làm trước khi bắt đầu hoạt động", "Please clock in before starting an activity", mode)
	case errors.Is(err, ErrActivityAlreadyActive):
		return warnLabel("已有进行中的活动，请先结束", "Đã có hoạt động đang diễn ra, hãy kết thúc trước", "An activity is already in progress — end it first", mode)
	case errors.Is(err, ErrOpenActivityBlocksCheckout):
		return warnLabel("请先回座结束活动后再下班", "Hãy trở lại chỗ ngồi rồi mới tan ca", "Return to seat before clocking out", mode)
	case errors.Is(err, ErrActivityNotActive):
		return warnLabel("当前没有进行中的活动", "Hiện không có hoạt động đang diễn ra", "No activity in progress", mode)
	case errors.Is(err, ErrActivityLimitReached):
		return warnLabel("今日该活动次数已达上限", "Hôm nay hoạt động này đã đạt giới hạn", "Today's limit for this activity is reached", mode)
	case errors.Is(err, ErrActivityTypeNotFound):
		return failLabel("未知活动类型", "Loại hoạt động không xác định", "Unknown activity type", mode)
	case errors.Is(err, ErrActivityTypeDisabled):
		return warnLabel("该活动已停用", "Hoạt động này đã tắt", "This activity is disabled", mode)
	case errors.Is(err, ErrOutsideWorkWindow):
		return warnLabel("当前不在上班时间段内", "Hiện không trong khung giờ làm việc", "Outside the allowed work window", mode)
	case errors.Is(err, ErrShiftAlreadyCompleted):
		return warnLabel("今天该班次已下班，请明天再上班", "Ca này hôm nay đã tan, hãy đi làm vào ngày mai", "This shift already clocked out today — try tomorrow", mode)
	case errors.Is(err, ErrDualShiftDisabled):
		return warnLabel("双班模式未启用", "Chế độ hai ca chưa bật", "Dual-shift mode is disabled", mode)
	case errors.Is(err, ErrPCLoginRequired):
		return warnLabel("请先在 PC Agent 员工登录后再操作（打卡或 /lang）", "Hãy đăng nhập nhân viên trên PC Agent trước (chấm công hoặc /lang)", "Please sign in on the PC agent first (punch or /lang)", mode)
	case errors.Is(err, ErrUnsupportedReplyLang):
		return warnLabel("不支持的语言，请使用 zh、vi 或 en", "Ngôn ngữ không hỗ trợ, dùng zh, vi hoặc en", "Unsupported language — use zh, vi, or en", mode)
	case errors.Is(err, ErrWorkstationDeviceRequired), errors.Is(err, ErrMultipleWorkstationLogins):
		return warnLabel("多台设备已登录，请重新点击普通打卡按钮并选择设备", "Nhiều thiết bị đã đăng nhập, hãy bấm lại nút chấm công và chọn thiết bị", "Multiple devices are signed in — tap the punch button again and select a device", mode)
	case errors.Is(err, ErrWorkstationDeviceNotBound):
		return warnLabel("未找到该设备绑定，请重新绑定或使用正确的设备按钮", "Không thấy gắn thiết bị này, hãy gắn lại hoặc chọn đúng máy", "Device binding not found — rebind or use the correct device button", mode)
	case errors.Is(err, ErrPCLoginMismatch):
		return warnLabel("PC 当前登录员工与 Telegram 绑定不一致", "Nhân viên đang đăng nhập PC khác với Telegram", "PC signed-in employee does not match this Telegram account", mode)
	case errors.Is(err, ErrCredentialsEmployeeMismatch):
		return warnLabel("账号与邀请码所属员工不一致", "Tài khoản không khớp nhân viên của mã mời", "Account does not match the invite employee", mode)
	case errors.Is(err, ErrWorkstationNotConfigured):
		return warnLabel("该员工未配置共享工位，请先在后台添加工位成员", "Nhân viên chưa có bàn chia sẻ, hãy thêm thành viên ở trang quản trị", "Shared workstation is not configured for this employee", mode)
	case errors.Is(err, ErrTelegramBoundToWorkstation):
		return warnLabel("该 Telegram 已绑定共享工位，请使用 /bind_workstation", "Telegram này đã gắn bàn chia sẻ, dùng /bind_workstation", "This Telegram is bound to a shared workstation — use /bind_workstation", mode)
	case errors.Is(err, ErrTelegramBoundToPersonal):
		return warnLabel("该 Telegram 已绑定个人账号，请使用 /bind", "Telegram này đã gắn tài khoản cá nhân, dùng /bind", "This Telegram is bound to a personal account — use /bind", mode)
	case errors.Is(err, ErrDeviceTelegramAlreadyBound):
		return warnLabel("该工位已绑定其他 Telegram 账号", "Bàn này đã gắn Telegram khác", "This workstation is already bound to another Telegram account", mode)
	case errors.Is(err, ErrWorkstationInviteMismatch):
		return warnLabel("邀请码员工不是该设备的共享工位成员", "Nhân viên mã mời không thuộc bàn chia sẻ của máy này", "Invite employee is not a member of this workstation", mode)
	case errors.Is(err, ErrInvalidCredentials):
		return warnLabel("账号不存在、已停用或密码错误", "Tài khoản không tồn tại, đã khóa hoặc sai mật khẩu", "Account missing, disabled, or password is wrong", mode)
	case errors.Is(err, ErrInviteNotFound):
		return failLabel("邀请码无效", "Mã mời không hợp lệ", "Invalid invite code", mode)
	case errors.Is(err, ErrInviteExpired):
		return warnLabel("邀请码已过期", "Mã mời đã hết hạn", "Invite code expired", mode)
	case errors.Is(err, ErrInviteRevoked):
		return warnLabel("邀请码已撤销", "Mã mời đã bị hủy", "Invite code revoked", mode)
	case errors.Is(err, ErrTelegramAlreadyBound):
		return warnLabel("该 Telegram 账号已绑定其他员工", "Telegram này đã gắn nhân viên khác", "This Telegram account is already bound to another employee", mode)
	case errors.Is(err, ErrActivityConcurrentLimit):
		return warnLabel("该活动并发人数已达上限", "Số người đang làm hoạt động này đã đầy", "This activity's concurrent limit is reached", mode)
	case errors.Is(err, ErrBackProcessing):
		return warnLabel("回座请求处理中，请稍候", "Đang xử lý trở lại, vui lòng đợi", "Return request is already processing", mode)
	case errors.Is(err, ErrOperationBusy):
		return warnLabel("请求处理中，请稍候", "Đang xử lý yêu cầu, vui lòng đợi", "Request is already processing", mode)
	case errors.Is(err, ErrActivityBackNotOwner):
		return warnLabel("这不是您的活动，无法回座", "Đây không phải hoạt động của bạn", "This is not your activity", mode)
	case errors.Is(err, ErrEmployeeNotFound):
		return warnLabel("未找到员工", "Không tìm thấy nhân viên", "Employee not found", mode)
	default:
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			return failLabel("操作失败", "Thao tác thất bại", "Operation failed", mode)
		}
		if mode == TgLangZh || mode == TgLangBoth {
			return "❌ " + msg
		}
		return failLabel("操作失败", "Thao tác thất bại", "Operation failed", mode) + "\n" + msg
	}
}

// Activity counters share the configured reset boundary across TG and web.
// A calendar midnight is not a reset unless explicitly configured as one.
func activityCounterWindow(now time.Time, settings *TelegramSettings, resetClock string) (time.Time, time.Time, error) {
	loc := tgLoadLocation(settings.Timezone)
	now = now.In(loc)
	if !settings.DailyResetEnabled {
		return time.Unix(0, 0).UTC(), time.Time{}, nil
	}
	h, m, ok := parseClock(resetClock)
	if !ok {
		return time.Time{}, time.Time{}, fmt.Errorf("无效的重置时间")
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc).Add(time.Duration(max(0, settings.DailyResetExecutionOffsetMinutes)) * time.Minute)
	for start.After(now) {
		start = start.AddDate(0, 0, -1)
	}
	for !start.AddDate(0, 0, 1).After(now) {
		start = start.AddDate(0, 0, 1)
	}
	return start, start.AddDate(0, 0, 1), nil
}
func (s *WorkforceTelegramService) activityCounterBounds(ctx context.Context, empID uuid.UUID, chatID string) (time.Time, time.Time, string, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	if chatID == "" {
		chatID = s.resolveNotifyChatID(ctx, empID, uuid.Nil)
	}
	// Group override and manual reset marker are independent scalar reads.
	// Fetch both in one round trip; missing rows retain the global defaults.
	var override sql.NullString
	var marker string
	err = s.db.QueryRowContext(ctx, `SELECT
 (SELECT daily_reset_time FROM wf_telegram_groups WHERE chat_id=$1),
 COALESCE((SELECT value FROM system_settings WHERE key=$2),'')`, chatID, "activity_reset:"+empID.String()).Scan(&override, &marker)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	return activityCounterBoundsFrom(ctx, settings, chatID, override, marker)
}

// activityCounterBoundsFrom turns the already-read group override and manual
// reset marker into the counting window (pure computation, no database access).
func activityCounterBoundsFrom(ctx context.Context, settings *TelegramSettings, chatID string, override sql.NullString, marker string) (time.Time, time.Time, string, error) {
	clock := settings.DailyResetTime
	if chatID != "" && override.Valid && strings.TrimSpace(override.String) != "" {
		clock = strings.TrimSpace(override.String)
	}
	start, end, err := activityCounterWindow(punchNow(ctx), settings, clock)
	if err != nil {
		return start, end, "", err
	}

	if at, e := time.Parse(time.RFC3339Nano, marker); e == nil && at.After(start) && !at.After(punchNow(ctx)) {
		start = at
	}
	return start, end, settings.Timezone, nil
}

// Both consumers count DISTINCT id; avoid a redundant full-row UNION deduplication.
const currentActivityUnion = `SELECT id,activity_code,activity_name,started_at,shift_type FROM wf_tg_activity_sessions WHERE employee_id=$1 AND status IN ('open','closed')
UNION ALL SELECT id,activity_code,activity_name,started_at,shift_type FROM wf_tg_activity_sessions_archive WHERE employee_id=$1 AND status='closed'`

func (s *WorkforceTelegramService) currentActivityCount(ctx context.Context, empID uuid.UUID, chatID, code, shift string) (int, error) {
	start, end, _, err := s.activityCounterBounds(ctx, empID, chatID)
	if err != nil {
		return 0, err
	}
	return s.countActivitiesInWindow(ctx, empID, code, shift, start, end)
}
func (s *WorkforceTelegramService) countActivitiesInWindow(ctx context.Context, empID uuid.UUID, code, shift string, start, end time.Time) (int, error) {
	var err error
	if end.IsZero() {
		end = punchNow(ctx).Add(time.Second)
	}
	var count int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT id) FROM (`+currentActivityUnion+`) a WHERE started_at >= $2 AND started_at < $3 AND activity_code=$4 AND ($5='' OR shift_type=$5)`, empID, start, end, code, shift).Scan(&count)
	return count, err
}
func (s *WorkforceTelegramService) PunchActivityUsage(ctx context.Context, empID uuid.UUID) (map[string]interface{}, error) {
	start, end, zone, err := s.activityCounterBounds(ctx, empID, "")
	if err != nil {
		return nil, err
	}
	shift, _ := s.getOpenWorkShift(ctx, empID)
	queryEnd := end
	if queryEnd.IsZero() {
		queryEnd = punchNow(ctx).Add(time.Second)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT activity_code,MAX(activity_name),COUNT(DISTINCT id) FROM (`+currentActivityUnion+`) a WHERE started_at >= $2 AND started_at < $3 AND ($4='' OR shift_type=$4) GROUP BY activity_code`, empID, start, queryEnd, shift)
	if err != nil {
		return nil, err
	}
	counts := []map[string]interface{}{}
	for rows.Next() {
		var code, name string
		var n int
		if err = rows.Scan(&code, &name, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts = append(counts, map[string]interface{}{"code": code, "name": name, "count": n})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	open, err := s.getOpenActivitySession(ctx, empID)
	if err != nil && !errors.Is(err, ErrActivityNotActive) {
		return nil, err
	}
	return map[string]interface{}{"counts": counts, "openSession": open, "periodStart": start, "periodEnd": end, "timezone": zone, "shift": shift, "automaticReset": !end.IsZero()}, nil
}
