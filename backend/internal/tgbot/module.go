// Consolidated source; original file boundaries are marked below.
package tgbot

import (
	bytes "bytes"
	context "context"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	uuid "github.com/google/uuid"
	html "html"
	io "io"
	log "log"
	http "net/http"
	urlPackage2 "net/url"
	os "os"
	strconv "strconv"
	strings "strings"
	sync "sync"
	atomic "sync/atomic"
	security "tg-attendance/backend/security"
	time "time"
)

// ---- Source: backend/internal/tgbot/alert.go ----
// tgAlertPrefix adds a warning icon to user-facing alert text when missing.
func tgAlertPrefix(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "⚠️ 操作失败"
	}
	for _, p := range []string{"⚠️", "⚠", "❌", "🚨", "✅", "👍", "😅", "🏃", "⏰"} {
		if strings.HasPrefix(text, p) {
			return text
		}
	}
	if strings.Contains(text, "失败") {
		return "❌ " + text
	}
	return "⚠️ " + text
}

// ---- Source: backend/internal/tgbot/bot_commands.go ----
var (
	processedUpdates = map[int64]time.Time{}
	cachedAdminIDs   = map[int64]bool{}
	actionRateMu     sync.Mutex
	actionRateCalls  = map[string][]time.Time{}
	botCommandsMu    sync.Mutex
	lastBotCommands  string
)

func isDuplicateUpdate(updateID int64) bool {
	if updateID <= 0 {
		return false
	}
	now := time.Now()
	if exp, ok := processedUpdates[updateID]; ok && exp.After(now) {
		return true
	}
	processedUpdates[updateID] = now.Add(10 * time.Minute)
	if len(processedUpdates) > 5000 {
		for k, v := range processedUpdates {
			if v.Before(now) {
				delete(processedUpdates, k)
			}
		}
	}
	return false
}
func allowActionRate(userID int64, action string, limit int, window time.Duration) bool {
	key := fmt.Sprintf("%d:%s", userID, action)
	actionRateMu.Lock()
	defer actionRateMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	calls := actionRateCalls[key]
	filtered := calls[:0]
	for _, t := range calls {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= limit {
		actionRateCalls[key] = filtered
		return false
	}
	actionRateCalls[key] = append(filtered, now)
	return true
}
func syncBotCommands(cfg config, commands []map[string]interface{}) {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return
	}
	normalized := normalizeBotCommands(commands)
	if len(normalized) == 0 {
		return
	}
	fp := cfg.BotToken + ":" + botCommandsFingerprint(normalized)
	botCommandsMu.Lock()
	if fp == lastBotCommands {
		botCommandsMu.Unlock()
		return
	}
	botCommandsMu.Unlock()

	names := make([]string, 0, len(normalized))
	for _, c := range normalized {
		names = append(names, c["command"])
	}
	log.Printf("setMyCommands: applying %d commands: %s", len(normalized), strings.Join(names, ", "))

	scopes := []map[string]interface{}{
		{"type": "default"},
		{"type": "all_private_chats"},
		{"type": "all_group_chats"},
	}
	staleLangs := []string{"zh", "zh-hans", "zh-hant", "vi", "en"}
	// Independent cleanup calls run in parallel; this is background work and
	// must finish quickly so it does not compete with punch replies.
	var cleanup sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, scope := range scopes {
		for _, lang := range staleLangs {
			cleanup.Add(1)
			sem <- struct{}{}
			go func(scope map[string]interface{}, lang string) {
				defer cleanup.Done()
				defer func() { <-sem }()
				deleteBotCommandsForScope(cfg, scope, lang)
			}(scope, lang)
		}
	}
	cleanup.Wait()
	ok := true
	for _, scope := range scopes {
		if !setBotCommandsForScope(cfg, normalized, scope, "") {
			ok = false
		}
	}
	if !ok {
		return
	}
	botCommandsMu.Lock()
	lastBotCommands = fp
	botCommandsMu.Unlock()
}
func normalizeBotCommands(raw []map[string]interface{}) []map[string]string {
	seen := map[string]bool{}
	out := make([]map[string]string, 0, len(raw))
	for _, item := range raw {
		if item == nil {
			continue
		}
		cmd := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["command"])))
		desc := strings.TrimSpace(fmt.Sprint(item["description"]))
		if cmd == "<nil>" {
			cmd = ""
		}
		if desc == "<nil>" {
			desc = ""
		}
		if cmd == "" || desc == "" || seen[cmd] || !isTelegramCommandName(cmd) {
			continue
		}
		seen[cmd] = true
		out = append(out, map[string]string{"command": cmd, "description": desc})
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}
func isTelegramCommandName(cmd string) bool {
	if cmd == "" || len(cmd) > 32 {
		return false
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return true
}
func botCommandsFingerprint(commands []map[string]string) string {
	parts := make([]string, 0, len(commands))
	for _, c := range commands {
		parts = append(parts, c["command"]+"="+c["description"])
	}
	return strings.Join(parts, "\n")
}
func deleteBotCommandsForScope(cfg config, scope map[string]interface{}, languageCode string) {
	payload := map[string]interface{}{"scope": scope}
	if languageCode != "" {
		payload["language_code"] = languageCode
	}
	postTelegramAPI(cfg, "deleteMyCommands", payload)
}
func setBotCommandsForScope(cfg config, commands []map[string]string, scope map[string]interface{}, languageCode string) bool {
	payload := map[string]interface{}{
		"commands": commands,
		"scope":    scope,
	}
	if languageCode != "" {
		payload["language_code"] = languageCode
	}
	raw, err := postTelegramAPI(cfg, "setMyCommands", payload)
	if err != nil {
		log.Printf("setMyCommands scope=%v lang=%q: %v", scope["type"], languageCode, err)
		return false
	}
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || !parsed.OK {
		log.Printf("setMyCommands scope=%v lang=%q failed: %s", scope["type"], languageCode, strings.TrimSpace(string(raw)))
		return false
	}
	return true
}
func postTelegramAPI(cfg config, method string, payload map[string]interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/%s", cfg.BotToken, method)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer drainResponseBody(resp.Body)
	return io.ReadAll(resp.Body)
}
func isAdminUser(userID int64) bool {
	return cachedAdminIDs[userID]
}
func parseAdminIDs(raw string) {
	cachedAdminIDs = map[int64]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var id int64
		if _, err := fmt.Sscanf(part, "%d", &id); err == nil {
			cachedAdminIDs[id] = true
		}
	}
}
func notifyAdminsStartup(cfg config, mode string) {
	for id := range cachedAdminIDs {
		msg := fmt.Sprintf("✅ monitorWin Telegram Worker 已启动\n模式：%s", mode)
		_ = sendTelegramWithMarkup(cfg, id, msg, nil)
	}
}
func handleSlashCommand(cfg config, msg *tgMessage) bool {
	if msg == nil {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(text, "/") {
		return false
	}
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	userID := msg.From.ID
	user := tgUserFromMessage(msg)
	parts := strings.Fields(text)
	cmd := strings.TrimPrefix(strings.ToLower(parts[0]), "/")
	if i := strings.Index(cmd, "@"); i > 0 {
		cmd = cmd[:i]
	}
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}

	switch cmd {
	case "start":
		if args != "" {
			payload := strings.TrimSpace(args)
			if strings.HasPrefix(strings.ToLower(payload), "bind_") {
				payload = strings.TrimPrefix(payload, "bind_")
				payload = strings.TrimPrefix(payload, "BIND_")
			}
			if payload != "" {
				replyBind(cfg, userID, msg.From.Username, chatID, payload, msg.Chat.ID, msg.MessageID)
				return true
			}
		}
		replyHelp(cfg, msg)
		return true
	case "bind":
		replyBind(cfg, userID, msg.From.Username, chatID, args, msg.Chat.ID, msg.MessageID)
		return true
	case "bind_multi":
		replyBindMulti(cfg, userID, msg.From.Username, chatID, args, msg.Chat.ID, msg.MessageID)
		return true
	case "bind_workstation":
		replyBindWorkstation(cfg, userID, msg.From.Username, chatID, args, msg.Chat.ID, msg.MessageID)
		return true
	case "help":
		replyHelp(cfg, msg)
		return true
	case "lang", "language", "lang_zh", "lang_vi", "lang_en":
		langArg := args
		switch cmd {
		case "lang_zh":
			langArg = "zh"
		case "lang_vi":
			langArg = "vi"
		case "lang_en":
			langArg = "en"
		}
		replyLangSwitch(cfg, msg, langArg)
		return true
	case "chatid", "groupid", "channelid", "id", "群id", "频道id":
		replyChatID(cfg, msg.Chat.ID, msg.Chat.Type, msg.Chat.Title, msg.MessageID)
		return true
	case "checkin", "checkin_day", "checkin_middle", "checkin_night":
		shift := args
		switch cmd {
		case "checkin_day":
			shift = "day"
		case "checkin_middle":
			shift = "middle"
		case "checkin_night":
			shift = "night"
		default:
			if shift != "day" && shift != "night" && shift != "middle" {
				shift = ""
			}
		}
		arg := shift
		if arg == "" {
			arg = "-"
		}
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "ci", arg,
			"请选择上班设备", func() {
				replyCheckIn(cfg, user, chatID, shift, msg.MessageID, "")
			})
		return true
	case "checkout":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "co", "-",
			"请选择下班设备", func() {
				replyCheckOut(cfg, user, chatID, msg.MessageID, "")
			})
		return true
	case "at":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "bk", "-",
			"请选择回座设备", func() {
				replyEndActivity(cfg, user, chatID, msg.MessageID, "")
			})
		return true
	case "status":
		replyStatus(cfg, userID)
		return true
	case "myinfo", "myinfoday", "myinfomiddle", "myinfonight":
		shift := ""
		switch cmd {
		case "myinfoday":
			shift = "day"
		case "myinfomiddle":
			shift = "middle"
		case "myinfonight":
			shift = "night"
		}
		replyMyInfo(cfg, user, chatID, shift, msg.MessageID)
		return true
	case "rankings", "rankingsday", "rankingsmiddle", "rankingsnight":
		shift := ""
		switch cmd {
		case "rankingsday":
			shift = "day"
		case "rankingsmiddle":
			shift = "middle"
		case "rankingsnight":
			shift = "night"
		}
		replyRankings(cfg, user, chatID, shift, msg.MessageID)
		return true
	case "handover":
		replyHandover(cfg, userID, args)
		return true
	case "export":
		if !allowActionRate(userID, "export", 2, time.Minute) {
			_ = sendTelegramByUser(cfg, userID, tRateLimited())
			return true
		}
		path := fmt.Sprintf("/telegram/worker/export?chatId=%s", urlPackage2.QueryEscape(chatID))
		var res struct {
			Code int `json:"code"`
			Data struct {
				Caption string `json:"caption"`
			} `json:"data"`
		}
		if err := backendGet(cfg, path, &res); err != nil {
			_ = sendTelegramByUser(cfg, userID, "导出失败："+err.Error())
			return true
		}
		msg := res.Data.Caption
		if msg == "" {
			msg = "导出已推送到群组"
		}
		_ = sendTelegramByUser(cfg, userID, msg)
		return true
	case "exportmonthly":
		if !allowActionRate(userID, "export", 2, time.Minute) {
			_ = sendTelegramByUser(cfg, userID, tRateLimited())
			return true
		}
		month := args
		if month == "" {
			month = time.Now().AddDate(0, -1, 0).Format("2006-01")
		}
		path := fmt.Sprintf("/telegram/worker/export?chatId=%s&month=%s", urlPackage2.QueryEscape(chatID), urlPackage2.QueryEscape(month))
		var res struct {
			Code int `json:"code"`
			Data struct {
				Caption string `json:"caption"`
			} `json:"data"`
		}
		if err := backendGet(cfg, path, &res); err != nil {
			_ = sendTelegramByUser(cfg, userID, "导出失败："+err.Error())
			return true
		}
		msg := res.Data.Caption
		if msg == "" {
			msg = "月导出已推送到群组"
		}
		_ = sendTelegramByUser(cfg, userID, msg)
		return true
	case "monthlyreport":
		path := fmt.Sprintf("/telegram/worker/monthly-report?chatId=%s", urlPackage2.QueryEscape(chatID))
		if args != "" {
			path += "&month=" + urlPackage2.QueryEscape(args)
		}
		var res struct {
			Code int `json:"code"`
			Data struct {
				DisplayText string `json:"displayText"`
			} `json:"data"`
		}
		if err := backendGet(cfg, path, &res); err != nil || res.Data.DisplayText == "" {
			_ = sendTelegramByUser(cfg, userID, "月报生成失败")
			return true
		}
		_ = sendTelegramHTML(cfg, userID, res.Data.DisplayText)
		return true
	case "actstatus":
		path := fmt.Sprintf("/telegram/worker/actstatus?chatId=%s", urlPackage2.QueryEscape(chatID))
		var res struct {
			Code int `json:"code"`
			Data struct {
				DisplayText string `json:"displayText"`
			} `json:"data"`
		}
		if err := backendGet(cfg, path, &res); err == nil && res.Data.DisplayText != "" {
			_ = sendTelegramByUser(cfg, userID, res.Data.DisplayText)
		}
		return true
	case "ci":
		if args == "" {
			_ = sendTelegramByUser(cfg, userID, "用法：/ci 活动名")
			return true
		}
		replyStartActivityByCI(cfg, user, chatID, args, msg.MessageID)
		return true
	case "showsettings", "checkdb", "fixmessages":
		if !isAdminUser(userID) {
			_ = sendTelegramByUser(cfg, userID, "需要管理员权限")
			return true
		}
		switch cmd {
		case "showsettings":
			path := fmt.Sprintf("/telegram/worker/showsettings?telegramUserId=%d&chatId=%s", userID, urlPackage2.QueryEscape(chatID))
			var res struct {
				Code int `json:"code"`
				Data struct {
					DisplayText string `json:"displayText"`
				} `json:"data"`
			}
			if err := backendGet(cfg, path, &res); err == nil {
				_ = sendTelegramHTML(cfg, userID, res.Data.DisplayText)
			}
		case "checkdb":
			path := fmt.Sprintf("/telegram/worker/checkdb?telegramUserId=%d", userID)
			var res struct {
				Code int `json:"code"`
				Data struct {
					DisplayText string `json:"displayText"`
				} `json:"data"`
			}
			if err := backendGet(cfg, path, &res); err == nil {
				_ = sendTelegramByUser(cfg, userID, res.Data.DisplayText)
			}
		case "fixmessages":
			var res apiResult
			res.Data = map[string]interface{}{}
			_ = backendPost(cfg, "/telegram/worker/admin/fixmessages", map[string]interface{}{
				"telegramUserId": userID,
			}, &res)
			_ = sendTelegramByUser(cfg, userID, fmt.Sprintf("已清理 %v 条 message_id", res.Data["cleared"]))
		}
		return true
	default:
		path := fmt.Sprintf("/telegram/worker/resolve-ci?text=%s", urlPackage2.QueryEscape(cmd))
		var res struct {
			Code int `json:"code"`
			Data struct {
				ActivityName string `json:"activityName"`
			} `json:"data"`
		}
		if err := backendGet(cfg, path, &res); err == nil && res.Data.ActivityName != "" {
			replyStartActivityWithDeviceSelection(cfg, user, chatID, res.Data.ActivityName, msg.MessageID)
			return true
		}
	}
	return false
}
func replyStartActivityByCI(cfg config, user tgUserRef, chatID, text string, replyTo int64) {
	path := fmt.Sprintf("/telegram/worker/resolve-ci?text=%s", urlPackage2.QueryEscape(text))
	var res struct {
		Code int `json:"code"`
		Data struct {
			ActivityName string `json:"activityName"`
		} `json:"data"`
	}
	if err := backendGet(cfg, path, &res); err != nil || res.Data.ActivityName == "" {
		refreshUserLangFromBackend(cfg, chatID, user.ID)
		replyWorkHTMLError(cfg, user, chatID, activityNotFoundPrefix(user.ID)+text, replyTo)
		return
	}
	replyStartActivityWithDeviceSelection(cfg, user, chatID, res.Data.ActivityName, replyTo)
}
func replyLangSwitch(cfg config, msg *tgMessage, langArg string) {
	if msg == nil {
		return
	}
	langArg = strings.TrimSpace(strings.ToLower(langArg))
	if langArg == "" {
		usage := "用法 / Usage: /lang zh | vi | en"
		_ = sendTelegramHTMLReply(cfg, msg.Chat.ID, html.EscapeString(usage), msg.MessageID, nil)
		return
	}
	var res struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			PreferredLang string `json:"preferredLang"`
			EmployeeID    string `json:"employeeId"`
		} `json:"data"`
	}
	err := backendPost(cfg, "/telegram/worker/lang", map[string]interface{}{
		"telegramUserId": msg.From.ID,
		"lang":           langArg,
	}, &res)
	if err != nil {
		log.Printf("lang switch failed user=%d lang=%s: %v", msg.From.ID, langArg, err)
		fail := "语言设置失败 / Language update failed: " + err.Error()
		_ = sendTelegramHTMLReply(cfg, msg.Chat.ID, html.EscapeString(fail), msg.MessageID, nil)
		return
	}
	if res.Code != 0 {
		errMsg := strings.TrimSpace(res.Message)
		if errMsg == "" {
			errMsg = "unsupported lang"
		}
		log.Printf("lang switch rejected user=%d lang=%s code=%d msg=%s", msg.From.ID, langArg, res.Code, errMsg)
		_ = sendTelegramHTMLReply(cfg, msg.Chat.ID, html.EscapeString("❌ "+errMsg+"\n/lang zh | vi | en"), msg.MessageID, nil)
		return
	}
	rememberLang := strings.TrimSpace(res.Data.PreferredLang)
	if rememberLang == "" {
		rememberLang = langArg
	}
	var okMsg string
	switch strings.ToLower(rememberLang) {
	case "vi", "vn", "vietnamese", "越南语", "越":
		okMsg = "✅ Ngôn ngữ phản hồi chấm công: Tiếng Việt"
	case "en", "english", "英语", "英文", "英":
		okMsg = "✅ Punch reply language: English"
	default:
		okMsg = "✅ 打卡回复语言已设为中文"
	}
	rememberUserLang(msg.From.ID, res.Data.EmployeeID, rememberLang)
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	kb := replyKeyboardForUser(cfg, chatID, msg.From.ID)
	_ = sendTelegramHTMLReply(cfg, msg.Chat.ID, html.EscapeString(okMsg), msg.MessageID, kb)
}
func sendTelegramHTML(cfg config, chatID int64, text string) error {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	kb := currentReplyKeyboard()
	if kb != nil {
		payload["reply_markup"] = kb
	}
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err == nil {
		drainResponseBody(resp.Body)
	}
	return err
}

// ---- Source: backend/internal/tgbot/command_sync_queue.go ----
type commandSyncJob struct {
	cfg      config
	commands []map[string]interface{}
}

var commandQueue = make(chan commandSyncJob, 1)
var commandQueueOnce sync.Once
var commandQueueMu sync.Mutex

// Menu publication is administrative work, never part of punch response latency.
// One worker and one latest queued job bound resources during frequent refreshes.
func scheduleBotCommands(cfg config, commands []map[string]interface{}) {
	commandQueueOnce.Do(func() {
		go func() {
			for job := range commandQueue {
				syncBotCommands(job.cfg, job.commands)
			}
		}()
	})
	cfg.Failure = nil
	cfg.configResponses = nil
	cfg.ReceivedAt = time.Time{}
	cfg.UpdateID = 0
	cfg.Context = nil // Administrative work must not inherit an update deadline.
	commandQueueMu.Lock()
	defer commandQueueMu.Unlock()
	select {
	case <-commandQueue:
	default:
	}
	commandQueue <- commandSyncJob{cfg, commands}
}

// ---- Source: backend/internal/tgbot/credentials.go ----
const workerCredentialsFile = ".worker-credentials.json"

func startWorkerHeartbeat(cfg config) {
	sendHeartbeat := func() {
		var result apiResult
		_ = backendPost(cfg, "/telegram/worker/heartbeat", map[string]interface{}{}, &result)
	}
	sendHeartbeat()
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sendHeartbeat()
		}
	}()
}

// ---- Source: backend/internal/tgbot/durable.go ----
// ProcessDurableUpdate is synchronous, so the owner can persist completion after processing.
func ProcessDurableUpdate(opts Options, body []byte, received time.Time) error {
	var u tgUpdate
	if err := json.Unmarshal(body, &u); err != nil {
		return err
	}
	cfg := opts.toConfig()
	ctx, cancel := context.WithTimeout(cfg.requestContext(), 35*time.Second)
	defer cancel()
	cfg.Context = ctx
	cfg.Failure = &atomic.Bool{}
	cfg.EventUnix = received.Unix()
	cfg.ReceivedAt = received
	cfg.UpdateID = u.UpdateID
	if u.Message != nil && u.Message.Date > 0 {
		event := time.Unix(u.Message.Date, 0)
		if event.After(received.Add(time.Minute)) || event.Before(received.Add(-24*time.Hour)) {
			return fmt.Errorf("message timestamp outside Telegram delivery window")
		}
		cfg.EventUnix = u.Message.Date
	}
	// Callback messages carry the ORIGINAL bot message time, not the button click time.
	// Use receipt time for callbacks. Telegram does not provide a callback click timestamp.
	setActiveConfig(cfg)
	cfg.configResponses = make(map[string][]byte)
	// One user-scoped response supplies runtime settings, language and keyboard.
	// Shared workstations still resolve the employee anew for every update.
	chatID, userID := "", int64(0)
	if u.Message != nil {
		chatID, userID = strconv.FormatInt(u.Message.Chat.ID, 10), u.Message.From.ID
	} else if u.CallbackQuery != nil && u.CallbackQuery.Message != nil {
		chatID, userID = strconv.FormatInt(u.CallbackQuery.Message.Chat.ID, 10), u.CallbackQuery.From.ID
	}
	refreshRuntimeConfigForUser(&cfg, chatID, userID)
	if cfg.Failure.Load() {
		return fmt.Errorf("runtime configuration unavailable; update requires review")
	}
	ProcessUpdate(cfg, u)
	if cfg.Failure.Load() {
		return fmt.Errorf("update or reply delivery failed; verify records before retry")
	}
	return nil
}

// RegisterDurableWebhook never discards Telegram's pending delivery queue.
func RegisterDurableWebhook(opts Options) error {
	cfg := opts.toConfig()
	if cfg.BotToken == "" || cfg.WebhookSecret == "" {
		return fmt.Errorf("请先保存 Bot Token 并生成 Webhook Secret")
	}
	if len(cfg.WebhookURL) < 8 || cfg.WebhookURL[:8] != "https://" {
		return fmt.Errorf("请配置 PUBLIC_BASE_URL 或使用 Render 的 HTTPS 域名")
	}
	return setWebhook(cfg)
}

// ---- Source: backend/internal/tgbot/i18n.go ----
type userLangState struct {
	EmpID string
	Lang  string // zh | vi | en
}

var cachedUserLang sync.Map // telegramUserID -> userLangState

func normalizeCacheLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "vi", "vn", "vietnamese", "越南语", "越":
		return "vi"
	case "en", "english", "英语", "英文", "英":
		return "en"
	default:
		// both / empty / unknown → zh (punch default; clears prior shift's vi)
		return "zh"
	}
}

// rememberUserLang stores bot-local prefix language for a Telegram account.
// empID tracks which employee the lang belongs to (shared-TG shift handoff).
func rememberUserLang(userID int64, empID, lang string) {
	if userID <= 0 {
		return
	}
	cachedUserLang.Store(userID, userLangState{
		EmpID: strings.TrimSpace(empID),
		Lang:  normalizeCacheLang(lang),
	})
}

// applyBackendLangCache prefers replyLang (per-employee punch lang, default zh).
// preferredLang is display/keyboard fallback only when replyLang is absent.
func applyBackendLangCache(userID int64, empID, replyLang, preferredLang string) {
	lang := strings.TrimSpace(replyLang)
	if lang == "" {
		lang = preferredLang
	}
	rememberUserLang(userID, empID, lang)
}
func clearUserLang(userID int64) {
	if userID > 0 {
		cachedUserLang.Delete(userID)
	}
}
func cachedEmpID(userID int64) string {
	if userID <= 0 {
		return ""
	}
	if v, ok := cachedUserLang.Load(userID); ok {
		if st, ok := v.(userLangState); ok {
			return st.EmpID
		}
	}
	return ""
}
func userLang(userID int64) string {
	if userID <= 0 {
		return "zh"
	}
	if v, ok := cachedUserLang.Load(userID); ok {
		if st, ok := v.(userLangState); ok {
			if st.Lang == "vi" || st.Lang == "en" || st.Lang == "zh" {
				return st.Lang
			}
		}
		// Legacy string entries from older tests / hot reload
		if s, ok := v.(string); ok && (s == "vi" || s == "en" || s == "zh") {
			return s
		}
	}
	return "zh"
}
func tUser(userID int64, zh, vi, en string) string {
	switch userLang(userID) {
	case "vi":
		return vi
	case "en":
		return en
	default:
		return zh
	}
}
func punchFailPrefix(userID int64) string {
	return tUser(userID, "打卡失败：", "Chấm công thất bại: ", "Clock-in failed: ")
}
func checkoutFailPrefix(userID int64) string {
	return tUser(userID, "签退失败：", "Tan ca thất bại: ", "Clock-out failed: ")
}
func startActivityFailPrefix(userID int64) string {
	return tUser(userID, "开始活动失败：", "Bắt đầu hoạt động thất bại: ", "Start activity failed: ")
}
func endActivityFailPrefix(userID int64) string {
	return tUser(userID, "结束活动失败：", "Kết thúc hoạt động thất bại: ", "End activity failed: ")
}
func activityNotFoundPrefix(userID int64) string {
	return tUser(userID, "未找到活动：", "Không tìm thấy hoạt động: ", "Activity not found: ")
}
func currentLangMode() string {
	activitiesMu.RLock()
	defer activitiesMu.RUnlock()
	if cachedLangMode == "" {
		return "both"
	}
	return cachedLangMode
}

// Built-in labels so button presses still work before /config is loaded.
var builtinButtonLookup = map[string]string{
	"上班": "checkin", "Đi làm": "checkin", "上班 / Đi làm": "checkin", "Clock in": "checkin",
	"下班": "work_end", "Tan ca": "work_end", "下班 / Tan ca": "work_end", "🔴 下班": "work_end", "Clock out": "work_end",
	"白班上班": "work_start_day", "Ca ngày Đi làm": "work_start_day", "Day in": "work_start_day",
	"中班上班": "work_start_middle", "Ca giữa Đi làm": "work_start_middle", "Mid in": "work_start_middle",
	"夜班上班": "work_start_night", "Ca đêm Đi làm": "work_start_night", "Night in": "work_start_night",
	"回座": "back", "Trở lại chỗ ngồi": "back", "✅ 回座": "back", "Back": "back",
	"我的记录": "my_record", "Lịch sử của tôi": "my_record", "我的信息": "my_record", "My log": "my_record",
	"排行榜": "rank", "Bảng xếp hạng": "rank", "排名": "rank", "Rank": "rank",
	"状态": "status", "Trạng thái": "status", "Status": "status",
	"交接班": "handover", "Giao ca": "handover", "换班": "handover", "Handover": "handover",
	"帮助": "help", "Trợ giúp": "help", "Help": "help",
	"chatid": "chatid",
	"导出数据":   "export_data", "Xuất dữ liệu": "export_data", "Export": "export_data",
	"🚾 小厕": "小厕", "🚽 大厕": "大厕",
	"🚽 小厕": "小厕", "🚾 大厕": "大厕", "🍱 吃饭": "吃饭", "🚬 抽烟": "抽烟", "🚬 抽烟或休息": "抽烟或休息",
}

const deviceTagSep = " · "

func splitDeviceTag(text string) (base, deviceHint string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	idx := strings.LastIndex(text, deviceTagSep)
	if idx <= 0 {
		return text, ""
	}
	base = strings.TrimSpace(text[:idx])
	deviceHint = strings.TrimSpace(text[idx+len(deviceTagSep):])
	if base == "" || deviceHint == "" {
		return text, ""
	}
	return base, deviceHint
}
func resolveButton(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	activitiesMu.RLock()
	defer activitiesMu.RUnlock()
	if canonical, ok := cachedButtonLookup[text]; ok {
		return canonical
	}
	if canonical, ok := builtinButtonLookup[text]; ok {
		return canonical
	}
	return text
}
func resolveActivityName(text string) string {
	canonical := resolveButton(text)
	activitiesMu.RLock()
	defer activitiesMu.RUnlock()
	for _, name := range cachedActivities {
		if canonical == name {
			return name
		}
	}
	return ""
}
func tRateLimited() string {
	switch currentLangMode() {
	case "vi":
		return "Thao tác quá nhanh, vui lòng thử lại sau."
	case "zh":
		return "操作过于频繁，请稍后再试。"
	default:
		return "操作过于频繁，请稍后再试。 / Thao tác quá nhanh, vui lòng thử lại sau."
	}
}
func helpText() string {
	activitiesMu.RLock()
	acts := strings.Join(cachedActivities, "、")
	mode := cachedLangMode
	activitiesMu.RUnlock()
	if mode == "" {
		mode = "both"
	}
	switch mode {
	case "vi":
		return fmt.Sprintf(`Bot chấm công Telegram

Ca ngày / Ca đêm (chế độ hai ca)
Đi làm: gửi nút tương ứng
Tan ca: gửi nút Tan ca
Hoạt động: gửi tên hoạt động (%s)
Trở lại: gửi nút Trở lại chỗ ngồi
Giao ca: gửi Giao ca
Trạng thái / Lịch sử / Bảng xếp hạng: dùng nút bàn phím
Ngôn ngữ phản hồi chấm công theo nhân viên: /lang_zh /lang_vi /lang_en (bàn chia sẻ: đăng nhập PC trước)`, acts)
	case "zh":
		return fmt.Sprintf(`Telegram 考勤 Bot

绑定个人 TG（无需客户端）：/bind 邀请码 或 /bind 账号
多设备共用 TG（共享工位）：/bind_multi 账号 或 /bind_multi 邀请码（同一 TG 可绑多台；点普通打卡按钮后选择设备）
单设备工位 TG：/bind_workstation 邀请码（打卡需 PC 登录）
客户端：登录后托盘「绑定 Telegram」获取链接
群内键盘按钮无反应：请在 @BotFather 对该 Bot 执行 /setprivacy → Disable
或在群内使用命令：/checkin_day /checkin_middle /checkin_night /checkout
白班 / 中班 / 夜班上班（双班或三班模式）
上班：发送对应按钮，或 /checkin_day /checkin_middle /checkin_night
下班：发送「下班」或 /checkout
活动：发送活动名称开始（%s）
结束活动：发送「回座」或 /at
交接班：发送「交接班」（双班 day↔night；三班 day→middle→night→day）
状态 / 我的记录 / 排行榜：使用键盘按钮
查询本群/频道 ID：/chatid（或 /groupid、/channelid）
打卡回复语言按员工保存：/lang_zh /lang_vi /lang_en（也可 /lang zh|vi|en；共享工位需先 PC 登录；未设置默认中文）`, acts)
	default:
		return fmt.Sprintf(`Telegram 考勤 Bot / Bot chấm công

个人 /bind 邀请码 或 /bind 账号 | 多设备 /bind_multi 账号 或 /bind_multi 邀请码 | 工位 /bind_workstation 邀请码
白班 / 中班 / 夜班 | Ca ngày / giữa / đêm
上班 / 下班 | Đi làm / Tan ca
活动 (%s) | Hoạt động
回座 / 状态 / 我的记录 / 排行榜
Trở lại / Trạng thái / Lịch sử / Bảng xếp hạng
/chatid 查询群或频道 ID
/lang_zh /lang_vi /lang_en 打卡回复语言按员工（共享工位先 PC 登录；默认 zh）`, acts)
	}
}
func tGroupWelcome(chatType, title string, chatID int64) string {
	name := title
	if name == "" {
		name = "本群"
	}
	mode := currentLangMode()
	if chatType == "channel" {
		switch mode {
		case "vi":
			return fmt.Sprintf("🤖 Bot đã tham gia kênh\n\n📢 %s\n🆔 ID: %d\n\nThêm ID này trong trang quản trị monitorWin.", name, chatID)
		case "zh":
			return fmt.Sprintf("🤖 机器人已加入频道\n\n📢 %s\n🆔 频道 ID：%d\n\n请在后台「Telegram 考勤 → 推送配置」绑定此 ID。", name, chatID)
		default:
			return fmt.Sprintf("🤖 机器人已加入频道 / Bot đã tham gia kênh\n\n📢 %s\n🆔 ID：%d\n\n后台绑定此 ID。", name, chatID)
		}
	}
	switch mode {
	case "vi":
		return fmt.Sprintf("🤖 Bot đã tham gia nhóm\n\n💬 %s\n🆔 ID: %d\n\nThêm Chat ID trong trang quản trị hoặc gửi /chatid.", name, chatID)
	case "zh":
		return fmt.Sprintf("🤖 机器人已加入群组\n\n💬 %s\n🆔 群组 ID：%d\n\n请在后台「群组绑定」添加此 Chat ID；也可发送 /chatid 查看。", name, chatID)
	default:
		return fmt.Sprintf("🤖 机器人已加入群组 / Bot đã tham gia nhóm\n\n💬 %s\n🆔 ID：%d\n\n后台「群组绑定」或 /chatid", name, chatID)
	}
}
func tChatIDHint(chatType, title string, chatID int64) string {
	name := title
	if name == "" {
		name = fmt.Sprintf("%d", chatID)
	}
	mode := currentLangMode()
	switch mode {
	case "vi":
		return fmt.Sprintf("📋 %s\n🆔 Chat ID: %d", name, chatID)
	case "zh":
		return fmt.Sprintf("📋 %s\n🆔 Chat ID：%d", name, chatID)
	default:
		return fmt.Sprintf("📋 %s\n🆔 Chat ID：%d", name, chatID)
	}
}

// needsDeviceSelectionRetry detects multi-device login errors (zh/vi/en) that should
// re-offer the device picker keyboard instead of a plain error.
func needsDeviceSelectionRetry(msg string) bool {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "选择设备") ||
		strings.Contains(msg, "多台设备已登录") ||
		strings.Contains(msg, "chọn thiết bị") ||
		strings.Contains(msg, "Nhiều thiết bị đã đăng nhập") ||
		strings.Contains(msg, "select a device") ||
		strings.Contains(msg, "Multiple devices are signed in")
}

// ---- Source: backend/internal/tgbot/member_notice.go ----
type joinUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	IsBot     bool   `json:"is_bot"`
}
type joinState struct {
	Status   string   `json:"status"`
	IsMember bool     `json:"is_member"`
	User     joinUser `json:"user"`
}
type joinChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type MemberJoinNotice struct {
	ChatID, UserID, EventUnix int64
	Text                      string
}

func joined(s joinState) bool {
	return s.Status == "member" || s.Status == "administrator" || s.Status == "creator" || (s.Status == "restricted" && s.IsMember)
}

// Handles both service messages and membership updates; caller persistently deduplicates them.
func MemberJoinNotices(body []byte) ([]MemberJoinNotice, bool, error) {
	var u struct {
		Message *struct {
			Date    int64      `json:"date"`
			Chat    joinChat   `json:"chat"`
			Members []joinUser `json:"new_chat_members"`
		} `json:"message"`
		Member *struct {
			Date int64     `json:"date"`
			Chat joinChat  `json:"chat"`
			Old  joinState `json:"old_chat_member"`
			New  joinState `json:"new_chat_member"`
		} `json:"chat_member"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, false, err
	}
	var users []joinUser
	var chat joinChat
	var date int64
	if u.Member != nil {
		chat = u.Member.Chat
		date = u.Member.Date
		if !joined(u.Member.Old) && joined(u.Member.New) {
			users = []joinUser{u.Member.New.User}
		}
	} else if u.Message != nil && len(u.Message.Members) > 0 {
		chat = u.Message.Chat
		date = u.Message.Date
		users = u.Message.Members
	} else {
		return nil, false, nil
	}
	notices := []MemberJoinNotice{}
	if chat.Type != "group" && chat.Type != "supergroup" {
		return notices, true, nil
	}
	for _, member := range users {
		if member.IsBot || member.ID <= 0 {
			continue
		}
		name := strings.TrimSpace(member.FirstName + " " + member.LastName)
		if name == "" {
			name = member.Username
		}
		if name == "" {
			name = "新成员"
		}
		text := fmt.Sprintf("👋 欢迎 %s 加入群组！\nTelegram ID：<code>%d</code>\n发送 /bind 后台子账号 即可绑定打卡。", html.EscapeString(name), member.ID)
		notices = append(notices, MemberJoinNotice{chat.ID, member.ID, date, text})
	}
	return notices, true, nil
}
func SendMemberJoinNotice(opts Options, n MemberJoinNotice) error {
	body, _ := json.Marshal(map[string]interface{}{"chat_id": n.ChatID, "text": n.Text, "parse_mode": "HTML"})
	response, err := telegramPost(opts.toConfig(), "https://api.telegram.org/bot"+opts.BotToken+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Telegram入群通知网络请求失败")
	}
	defer response.Body.Close()
	var result struct {
		OK bool `json:"ok"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil || response.StatusCode != 200 || !result.OK {
		return fmt.Errorf("Telegram入群通知未被接受（HTTP %d），请检查机器人群权限", response.StatusCode)
	}
	return nil
}

// ---- Source: backend/internal/tgbot/options.go ----
// Options configures the Telegram bot runner (embedded in backend or standalone).
type Options struct {
	Context       context.Context
	BackendURL    string
	WorkerSecret  string
	BotToken      string
	BotMode       string
	WebhookURL    string
	WebhookSecret string
	WebhookPath   string
	WebhookListen string
	PollTimeout   int
	Embedded      bool
}

func (o Options) toConfig() config {
	cfg := config{
		Context:       o.Context,
		BackendURL:    strings.TrimRight(strings.TrimSpace(o.BackendURL), "/"),
		WorkerSecret:  strings.TrimSpace(o.WorkerSecret),
		BotToken:      strings.TrimSpace(o.BotToken),
		BotMode:       strings.TrimSpace(o.BotMode),
		WebhookURL:    strings.TrimSpace(o.WebhookURL),
		WebhookSecret: strings.TrimSpace(o.WebhookSecret),
		WebhookPath:   strings.TrimSpace(o.WebhookPath),
		WebhookListen: strings.TrimSpace(o.WebhookListen),
		PollTimeout:   o.PollTimeout,
	}
	if cfg.BotMode == "" {
		cfg.BotMode = "polling"
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 25
	}
	if cfg.WebhookPath == "" {
		cfg.WebhookPath = "/telegram/webhook"
	}
	if cfg.WebhookListen == "" {
		cfg.WebhookListen = ":8081"
	}
	return cfg
}

// Run starts polling or webhook mode until ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	cfg := opts.toConfig()
	if cfg.WorkerSecret == "" {
		return errMissingWorkerSecret
	}
	setActiveConfig(cfg)
	refreshRuntimeConfig(&cfg, "")
	if cfg.BotToken == "" {
		return errMissingBotToken
	}
	if !opts.Embedded {
		startWorkerHeartbeat(cfg)
	}
	logMode := "polling"
	if cfg.useWebhook() {
		logMode = "webhook"
	}
	log.Printf("[tgbot] started backend=%s mode=%s embedded=%v", cfg.BackendURL, logMode, opts.Embedded)
	refreshRuntimeConfig(&cfg, "")
	if len(cachedAdminIDs) > 0 {
		notifyAdminsStartup(cfg, logMode)
	}
	if cfg.useWebhook() {
		if opts.Embedded {
			// Webhook updates are delivered via backend HTTP route; keep config fresh.
			return runEmbeddedWebhook(ctx, cfg)
		}
		runWebhook(cfg)
		return nil
	}
	return runPollingCtx(ctx, cfg)
}

// ---- Source: backend/internal/tgbot/runner.go ----
type config struct {
	ReceivedAt time.Time
	UpdateID   int64
	Context    context.Context
	// Configuration is cached only within one update; never reuse employee state across punches.
	configResponses map[string][]byte
	EventUnix       int64
	Failure         *atomic.Bool
	BotToken        string
	BackendURL      string
	WorkerSecret    string
	PollTimeout     int
	BotMode         string
	WebhookURL      string
	WebhookSecret   string
	WebhookPath     string
	WebhookListen   string
}
type tgMessage struct {
	Date      int64 `json:"date"`
	MessageID int64 `json:"message_id"`
	Chat      struct {
		ID    int64  `json:"id"`
		Type  string `json:"type"`
		Title string `json:"title"`
	} `json:"chat"`
	From struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"from"`
	Text string `json:"text"`
}
type tgCallbackQuery struct {
	ID   string `json:"id"`
	From struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}
type tgUpdate struct {
	UpdateID      int64            `json:"update_id"`
	CallbackQuery *tgCallbackQuery `json:"callback_query"`
	MyChatMember  *struct {
		Chat struct {
			ID    int64  `json:"id"`
			Type  string `json:"type"`
			Title string `json:"title"`
		} `json:"chat"`
		OldChatMember struct {
			Status string `json:"status"`
		} `json:"old_chat_member"`
		NewChatMember struct {
			Status string `json:"status"`
			User   struct {
				IsBot bool `json:"is_bot"`
			} `json:"user"`
		} `json:"new_chat_member"`
	} `json:"my_chat_member"`
	Message     *tgMessage `json:"message"`
	ChannelPost *tgMessage `json:"channel_post"`
}
type workerActivity struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var (
	cachedActivities    = []string{"小厕", "大厕", "抽烟", "吃饭"}
	cachedReplyKeyboard map[string]interface{}
	cachedButtonLookup  = map[string]string{}
	cachedLangMode      = "both"
	activitiesMu        sync.RWMutex
	rateMu              sync.Mutex
	userRateCalls       = map[int64][]time.Time{}
)

const (
	userRateLimitCount  = 10
	userRateLimitWindow = time.Minute
)

func allowUserRate(userID int64) bool {
	rateMu.Lock()
	defer rateMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-userRateLimitWindow)
	calls := userRateCalls[userID]
	filtered := calls[:0]
	for _, t := range calls {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= userRateLimitCount {
		userRateCalls[userID] = filtered
		return false
	}
	userRateCalls[userID] = append(filtered, now)
	return true
}
func refreshRuntimeConfig(cfg *config, chatID string) {
	refreshRuntimeConfigForUser(cfg, chatID, 0)
}
func refreshRuntimeConfigForUser(cfg *config, chatID string, userID int64) {
	path := "/telegram/worker/config"
	if chatID = strings.TrimSpace(chatID); chatID != "" {
		path += "?chatId=" + urlPackage2.QueryEscape(chatID)
	}
	if userID > 0 {
		path = fmt.Sprintf("/telegram/worker/config?chatId=%s&telegramUserId=%d", urlPackage2.QueryEscape(chatID), userID)
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			BotMode              string                   `json:"botMode"`
			BotToken             string                   `json:"botToken"`
			WebhookURL           string                   `json:"webhookUrl"`
			WebhookSecret        string                   `json:"webhookSecret"`
			WebhookPath          string                   `json:"webhookPath"`
			LangMode             string                   `json:"langMode"`
			ButtonLookup         map[string]string        `json:"buttonLookup"`
			BotCommands          []map[string]interface{} `json:"botCommands"`
			AdminTelegramUserIds string                   `json:"adminTelegramUserIds"`
			Activities           []workerActivity         `json:"activities"`
			ReplyKeyboard        map[string]interface{}   `json:"replyKeyboard"`
		} `json:"data"`
	}
	if err := backendGet(*cfg, path, &result); err != nil || result.Code != 0 {
		if cfg.Failure != nil {
			cfg.Failure.Store(true)
		}
		return
	}
	names := make([]string, 0, len(result.Data.Activities))
	for _, a := range result.Data.Activities {
		if strings.TrimSpace(a.Name) != "" {
			names = append(names, a.Name)
		}
	}
	activitiesMu.Lock()
	if len(names) > 0 {
		cachedActivities = names
	}
	if userID == 0 && result.Data.ReplyKeyboard != nil {
		cachedReplyKeyboard = result.Data.ReplyKeyboard
	}
	if len(result.Data.ButtonLookup) > 0 {
		cachedButtonLookup = result.Data.ButtonLookup
	}
	if strings.TrimSpace(result.Data.LangMode) != "" {
		cachedLangMode = strings.TrimSpace(result.Data.LangMode)
	}
	parseAdminIDs(result.Data.AdminTelegramUserIds)
	if token := strings.TrimSpace(result.Data.BotToken); token != "" {
		cfg.BotToken = token
	}
	cmds := result.Data.BotCommands
	token := strings.TrimSpace(cfg.BotToken)
	activitiesMu.Unlock()
	if len(cmds) > 0 && token != "" {
		scheduleBotCommands(*cfg, cmds)
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_MODE")); v != "" {
		cfg.BotMode = v
	} else if m := strings.TrimSpace(result.Data.BotMode); m != "" {
		cfg.BotMode = m
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_URL")); v != "" {
		cfg.WebhookURL = v
	} else if u := strings.TrimSpace(result.Data.WebhookURL); u != "" {
		cfg.WebhookURL = u
	}
	if v := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET")); v != "" {
		cfg.WebhookSecret = v
	} else if s := strings.TrimSpace(result.Data.WebhookSecret); s != "" {
		cfg.WebhookSecret = s
	}
	if p := strings.TrimSpace(result.Data.WebhookPath); p != "" {
		cfg.WebhookPath = p
	}
}
// Discovery metadata (chat id/title/last seen) only matters to the admin group
// picker, so an unchanged group is written at most once per chatSeenRefresh
// instead of on every message. A title change or a restart writes immediately.
const chatSeenRefresh = 30 * time.Minute

type chatSeenEntry struct {
	title string
	at    time.Time
}

var chatSeenCache sync.Map // int64 chat id -> chatSeenEntry

func recordChatSeen(cfg config, chatID int64, chatType, title string) bool {
	chatType = strings.TrimSpace(chatType)
	if chatType != "group" && chatType != "supergroup" && chatType != "channel" {
		return false
	}
	if v, ok := chatSeenCache.Load(chatID); ok {
		if e, ok := v.(chatSeenEntry); ok && e.title == title && time.Since(e.at) < chatSeenRefresh {
			return false
		}
	}
	var result apiResult
	err := backendPost(cfg, "/telegram/worker/chat/seen", map[string]interface{}{
		"chatId":    strconv.FormatInt(chatID, 10),
		"chatTitle": title,
		"chatType":  chatType,
	}, &result)
	if err != nil || result.Code != 0 {
		return false
	}
	chatSeenCache.Store(chatID, chatSeenEntry{title: title, at: time.Now()})
	if v, ok := result.Data["firstSeen"].(bool); ok {
		return v
	}
	return false
}
func pollUpdates(cfg config, offset int64) ([]tgUpdate, error) {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=%d&offset=%d&allowed_updates=%s",
		cfg.BotToken, cfg.PollTimeout, offset, urlPackage2.QueryEscape(`["message","callback_query","my_chat_member","channel_post"]`))
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer drainResponseBody(resp.Body)
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if !parsed.OK {
		return nil, fmt.Errorf("getUpdates failed: %s", string(body))
	}
	return parsed.Result, nil
}
func handleCallback(cfg config, u tgUpdate) {
	cb := u.CallbackQuery
	if cb == nil {
		return
	}
	// Sync lang for current PC-logged employee before any local tUser text (shared TG handoff).
	if cb.From.ID > 0 {
		chatID := strconv.FormatInt(cb.From.ID, 10)
		if cb.Message != nil {
			chatID = strconv.FormatInt(cb.Message.Chat.ID, 10)
		}
		refreshUserLangFromBackend(cfg, chatID, cb.From.ID)
	}
	data := strings.TrimSpace(cb.Data)
	if strings.HasPrefix(data, "ds:") {
		handleDeviceSelectionCallback(cfg, cb, data)
		return
	}
	if strings.HasPrefix(data, "rv:") {
		handleAttendanceReviewCallback(cfg, cb, data)
		return
	}
	if !strings.HasPrefix(data, "qb:") {
		return
	}
	sessionID := strings.TrimPrefix(data, "qb:")
	userID := cb.From.ID
	var result apiResult
	result.Data = map[string]interface{}{}
	err := backendPost(cfg, "/telegram/worker/activity/callback-back", map[string]interface{}{
		"sessionId":      sessionID,
		"telegramUserId": userID,
	}, &result)
	chatID := strconv.FormatInt(userID, 10)
	replyTo := int64(0)
	if cb.Message != nil {
		chatID = strconv.FormatInt(cb.Message.Chat.ID, 10)
		replyTo = cb.Message.MessageID
	}
	if err == nil && result.Code == 0 {
		if groupReplySent(result.Data) {
			answerCallback(cfg, cb.ID, "")
			return
		}
		if groupHTML, ok := result.Data["groupReplyHtml"].(string); ok && strings.TrimSpace(groupHTML) != "" {
			replyActivityEndHTML(cfg, chatID, groupHTML, activityAnchorReplyTo(result.Data, replyTo), result.Data)
			answerCallback(cfg, cb.ID, "")
			return
		}
		refreshUserLangFromBackend(cfg, chatID, userID)
		text := tUser(userID, "回座完成", "Đã trở lại chỗ ngồi", "Returned to seat")
		if msg, ok := result.Data["messageText"].(string); ok && msg != "" {
			text = msg
		}
		replyWorkHTMLInChatMarkup(cfg, chatID, html.EscapeString(tgAlertPrefix(text)), replyTo, nil)
	} else {
		refreshUserLangFromBackend(cfg, chatID, userID)
		text := tUser(userID, "回座失败", "Trở lại thất bại", "Return failed")
		if err != nil {
			text = err.Error()
		} else if result.Message != "" {
			text = result.Message
		}
		if result.Code == 1006 {
			answerCallbackAlert(cfg, cb.ID, text)
			return
		}
		replyWorkHTMLInChatMarkup(cfg, chatID, html.EscapeString(tgAlertPrefix(text)), replyTo, nil)
	}
	answerCallback(cfg, cb.ID, "")
}

type punchDeviceOption struct {
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	EmployeeName string `json:"employeeName"`
}

func activitySelectionIndex(name string) int {
	activitiesMu.RLock()
	defer activitiesMu.RUnlock()
	for i, item := range cachedActivities {
		if item == name {
			return i
		}
	}
	return -1
}
func activitySelectionName(index int) string {
	activitiesMu.RLock()
	defer activitiesMu.RUnlock()
	if index < 0 || index >= len(cachedActivities) {
		return ""
	}
	return cachedActivities[index]
}
func replyStartActivityWithDeviceSelection(
	cfg config, user tgUserRef, chatID, activity string, replyTo int64,
) {
	index := activitySelectionIndex(activity)
	if index < 0 {
		replyStartActivity(cfg, user, chatID, activity, replyTo, "")
		return
	}
	withDeviceSelection(cfg, user.ID, chatID, replyTo, "ac",
		strconv.FormatInt(int64(index), 36),
		"请选择「"+activity+"」打卡设备", func() {
			replyStartActivity(cfg, user, chatID, activity, replyTo, "")
		})
}

// withDeviceSelection executes immediately for personal/single-device users.
// If several bound PCs have active employee logins it sends an inline picker
// and defers execution until the user selects a device.
func withDeviceSelection(
	cfg config, userID int64, chatID string, replyTo int64,
	action, arg, prompt string, execute func(),
) {
	// The fresh per-update config already resolved an unambiguous employee.
	// Skip only the picker preflight; the mutation endpoint still resolves and
	// validates the binding/device again. Ambiguous or missing identities fall
	// through to the existing device-selection endpoint.
	configPath := fmt.Sprintf("/telegram/worker/config?chatId=%s&telegramUserId=%d", urlPackage2.QueryEscape(strings.TrimSpace(chatID)), userID)
	if raw, ok := cfg.configResponses[configPath]; ok {
		var snapshot struct {
			Code int `json:"code"`
			Data struct {
				EmployeeID string `json:"employeeId"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &snapshot) == nil && snapshot.Code == 0 {
			if id, err := uuid.Parse(snapshot.Data.EmployeeID); err == nil && id != uuid.Nil {
				execute()
				return
			}
		}
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			RequiresSelection bool                `json:"requiresSelection"`
			Items             []punchDeviceOption `json:"items"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/telegram/worker/punch-devices?telegramUserId=%d", userID)
	if err := backendGet(cfg, path, &result); err != nil || result.Code != 0 {
		replyWorkHTMLError(cfg, tgUserRef{ID: userID}, chatID, tUser(userID,
			"暂时无法确认打卡设备，请稍后重试。", "Chưa xác định được thiết bị, vui lòng thử lại sau.", "Unable to check punch devices. Please try again later."), replyTo)
		return
	}
	if !result.Data.RequiresSelection || len(result.Data.Items) < 2 {
		execute()
		return
	}

	buttons := make([]map[string]interface{}, 0, len(result.Data.Items))
	nameCounts := make(map[string]int, len(result.Data.Items))
	for _, item := range result.Data.Items {
		name := strings.TrimSpace(item.EmployeeName)
		if name == "" {
			name = "未命名员工"
		}
		nameCounts[name]++
	}
	nameSeen := make(map[string]int, len(nameCounts))
	for _, item := range result.Data.Items {
		deviceID := strings.TrimSpace(item.DeviceID)
		if deviceID == "" {
			continue
		}
		label := strings.TrimSpace(item.EmployeeName)
		if label == "" {
			label = "未命名员工"
		}
		if nameCounts[label] > 1 {
			nameSeen[label]++
			label += fmt.Sprintf(" %d", nameSeen[label])
		}
		callbackData := fmt.Sprintf("ds:%s:%s:%s", action, arg, deviceID)
		// Telegram limits callback_data to 64 bytes.
		if len([]byte(callbackData)) > 64 {
			execute()
			return
		}
		buttons = append(buttons, map[string]interface{}{
			"text": label, "callback_data": callbackData,
		})
	}
	if len(buttons) < 2 {
		execute()
		return
	}
	rows := packInlineButtons(buttons)
	markup := map[string]interface{}{"inline_keyboard": rows}
	groupChatID, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	if err != nil {
		groupChatID = userID
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "请选择本次打卡设备"
	}
	if err := sendTelegramHTMLReply(cfg, groupChatID, html.EscapeString(prompt), replyTo, markup); err != nil {
		execute()
	}
}

// packInlineButtons keeps short employee names side-by-side and wraps longer
// labels. Telegram does not auto-wrap inline-keyboard rows, so row packing must
// be expressed explicitly in the reply_markup.
func packInlineButtons(buttons []map[string]interface{}) [][]map[string]interface{} {
	const (
		maxButtonsPerRow = 3
		maxVisualWidth   = 18
	)
	rows := make([][]map[string]interface{}, 0, (len(buttons)+1)/2)
	row := make([]map[string]interface{}, 0, maxButtonsPerRow)
	rowWidth := 0
	for _, button := range buttons {
		label, _ := button["text"].(string)
		width := inlineLabelVisualWidth(label)
		extra := width
		if len(row) > 0 {
			extra += 2
		}
		if len(row) >= maxButtonsPerRow || (len(row) > 0 && rowWidth+extra > maxVisualWidth) {
			rows = append(rows, row)
			row = make([]map[string]interface{}, 0, maxButtonsPerRow)
			rowWidth = 0
			extra = width
		}
		row = append(row, button)
		rowWidth += extra
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}
func inlineLabelVisualWidth(text string) int {
	width := 0
	for _, r := range strings.TrimSpace(text) {
		if r <= 0x7f {
			width++
		} else {
			width += 2
		}
	}
	if width < 1 {
		return 1
	}
	return width
}
func handleDeviceSelectionCallback(cfg config, cb *tgCallbackQuery, data string) {
	parts := strings.SplitN(data, ":", 4)
	if len(parts) != 4 || cb.Message == nil {
		answerCallbackAlert(cfg, cb.ID, "无效或已过期的设备选择")
		return
	}
	action, arg, deviceID := parts[1], parts[2], strings.TrimSpace(parts[3])
	if _, err := uuid.Parse(deviceID); err != nil {
		answerCallbackAlert(cfg, cb.ID, "无效设备")
		return
	}
	chatID := strconv.FormatInt(cb.Message.Chat.ID, 10)
	replyTo := cb.Message.MessageID
	user := tgUserFromCallback(cb)
	answerCallback(cfg, cb.ID, "已选择设备")
	clearInlineKeyboard(cfg, cb.Message.Chat.ID, cb.Message.MessageID)

	switch action {
	case "ci":
		shift := arg
		if shift == "-" {
			shift = ""
		}
		replyCheckIn(cfg, user, chatID, shift, replyTo, deviceID)
	case "co":
		replyCheckOut(cfg, user, chatID, replyTo, deviceID)
	case "bk":
		replyEndActivity(cfg, user, chatID, replyTo, deviceID)
	case "ac":
		index, err := strconv.ParseInt(arg, 36, 32)
		if err != nil {
			answerCallbackAlert(cfg, cb.ID, "活动选择已过期，请重新点击")
			return
		}
		activity := activitySelectionName(int(index))
		if activity == "" {
			answerCallbackAlert(cfg, cb.ID, "活动选择已过期，请重新点击")
			return
		}
		replyStartActivity(cfg, user, chatID, activity, replyTo, deviceID)
	default:
		answerCallbackAlert(cfg, cb.ID, "未知操作")
	}
}
func clearInlineKeyboard(cfg config, chatID, messageID int64) {
	payload := map[string]interface{}{
		"chat_id": chatID, "message_id": messageID,
		"reply_markup": map[string]interface{}{"inline_keyboard": [][]interface{}{}},
	}
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageReplyMarkup", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err == nil && resp != nil {
		drainResponseBody(resp.Body)
	}
}
func handleAttendanceReviewCallback(cfg config, cb *tgCallbackQuery, data string) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		answerCallbackAlert(cfg, cb.ID, "无效操作")
		return
	}
	taskID := strings.TrimSpace(parts[1])
	choice := strings.TrimSpace(parts[2])
	decision := ""
	switch choice {
	case "abs":
		decision = "absent"
	case "rest":
		decision = "rest_half"
	case "leave":
		decision = "leave"
	default:
		answerCallbackAlert(cfg, cb.ID, "无效操作")
		return
	}
	var result apiResult
	result.Data = map[string]interface{}{}
	err := backendPost(cfg, "/telegram/worker/attendance-review/callback", map[string]interface{}{
		"taskId":         taskID,
		"decision":       decision,
		"telegramUserId": cb.From.ID,
	}, &result)
	if err != nil {
		answerCallbackAlert(cfg, cb.ID, err.Error())
		return
	}
	if result.Code != 0 {
		text := result.Message
		if strings.TrimSpace(text) == "" {
			text = "处理失败"
		}
		if result.Code == 1003 {
			answerCallbackAlert(cfg, cb.ID, text)
			return
		}
		answerCallback(cfg, cb.ID, text)
		return
	}
	text := "已确认"
	if m, ok := result.Data["messageText"].(string); ok && strings.TrimSpace(m) != "" {
		text = m
	}
	answerCallback(cfg, cb.ID, text)
}
func currentReplyKeyboard() map[string]interface{} {
	activitiesMu.RLock()
	defer activitiesMu.RUnlock()
	return cachedReplyKeyboard
}
func replyKeyboardForChat(cfg config, chatID string) map[string]interface{} {
	if kb := currentReplyKeyboard(); kb != nil {
		return kb
	}
	refreshRuntimeConfig(&cfg, chatID)
	return currentReplyKeyboard()
}
func replyKeyboardForUser(cfg config, chatID string, userID int64) map[string]interface{} {
	kb, _ := replyKeyboardAndLegendForUser(cfg, chatID, userID)
	return kb
}
func replyKeyboardAndLegendForUser(cfg config, chatID string, userID int64) (map[string]interface{}, string) {
	if userID <= 0 {
		return replyKeyboardForChat(cfg, chatID), ""
	}
	path := fmt.Sprintf("/telegram/worker/config?chatId=%s&telegramUserId=%d",
		urlPackage2.QueryEscape(strings.TrimSpace(chatID)), userID)
	var result struct {
		Code int `json:"code"`
		Data struct {
			ReplyKeyboard   map[string]interface{} `json:"replyKeyboard"`
			DeviceTagLegend string                 `json:"deviceTagLegend"`
			PreferredLang   string                 `json:"preferredLang"`
			ReplyLang       string                 `json:"replyLang"`
			EmployeeID      string                 `json:"employeeId"`
		} `json:"data"`
	}
	if err := backendGet(cfg, path, &result); err == nil && result.Code == 0 {
		// replyLang = per-employee punch lang (default zh). Clears prior shift cache on shared TG.
		applyBackendLangCache(userID, result.Data.EmployeeID, result.Data.ReplyLang, result.Data.PreferredLang)
		kb := result.Data.ReplyKeyboard
		if kb == nil {
			kb = replyKeyboardForChat(cfg, chatID)
		}
		return kb, strings.TrimSpace(result.Data.DeviceTagLegend)
	}
	return replyKeyboardForChat(cfg, chatID), ""
}

// refreshUserLangFromBackend overwrites the bot-local lang cache from the current employee
// resolved for this Telegram account (PC login on shared workstation).
// The per-message language lookup costs one backend/database round trip (~0.4s on a
// remote database) in front of every punch. Reuse a recent result for the same
// user and chat; /bind and /lang calls invalidate it (see backendPost).
const userLangRefreshTTL = 2 * time.Minute

var userLangRefreshed sync.Map // "userID|chatID" -> time.Time

func refreshUserLangFromBackend(cfg config, chatID string, userID int64) {
	if userID <= 0 {
		return
	}
	cacheKey := fmt.Sprintf("%d|%s", userID, strings.TrimSpace(chatID))
	if at, ok := userLangRefreshed.Load(cacheKey); ok {
		if t, ok := at.(time.Time); ok && time.Since(t) < userLangRefreshTTL {
			return
		}
	}
	path := fmt.Sprintf("/telegram/worker/config?chatId=%s&telegramUserId=%d",
		urlPackage2.QueryEscape(strings.TrimSpace(chatID)), userID)
	var result struct {
		Code int `json:"code"`
		Data struct {
			PreferredLang string `json:"preferredLang"`
			ReplyLang     string `json:"replyLang"`
			EmployeeID    string `json:"employeeId"`
		} `json:"data"`
	}
	if err := backendGet(cfg, path, &result); err != nil || result.Code != 0 {
		return
	}
	applyBackendLangCache(userID, result.Data.EmployeeID, result.Data.ReplyLang, result.Data.PreferredLang)
	userLangRefreshed.Store(cacheKey, time.Now())
}

// replyHelp sends help + per-user keyboard. Must reply_to the user message so
// selective multi-device keyboards actually appear in groups.
func replyHelp(cfg config, msg *tgMessage) {
	if msg == nil {
		return
	}
	refreshRuntimeConfigForUser(&cfg, strconv.FormatInt(msg.Chat.ID, 10), msg.From.ID)
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	help := helpText()
	kb, legend := replyKeyboardAndLegendForUser(cfg, chatID, msg.From.ID)
	if legend != "" {
		help += "\n\n" + legend
	}
	if err := sendTelegramHTMLReply(cfg, msg.Chat.ID, html.EscapeString(help), msg.MessageID, kb); err != nil {
		log.Printf("[Telegram] help keyboard delivery failed chat=%d: %v", msg.Chat.ID, err)
		if cfg.Failure != nil {
			cfg.Failure.Store(true)
		}
	}
}
func replyPunchError(cfg config, user tgUserRef, chatID, text string, replyTo int64) {
	refreshUserLangFromBackend(cfg, chatID, user.ID)
	deliverPunchError(cfg, user, chatID, text, replyTo)
}

// replyPunchTransportError localizes the failure prefix using a fresh backend lang sync first
// (shared TG must not reuse the previous employee's cached language).
// A mutation timeout does not prove that the transaction failed. Never invite
// immediate re-punching after losing the response to a committed operation.
func replyPunchTransportError(cfg config, user tgUserRef, chatID string, _ func(int64) string, _ error, replyTo int64) {
	text := tUser(user.ID,
		"打卡结果暂未确认，请先查看打卡记录或联系管理员核对，勿重复打卡。",
		"Chưa xác nhận được kết quả. Vui lòng kiểm tra lịch sử hoặc liên hệ quản trị viên; không chấm công lại.",
		"Punch result is unconfirmed. Check your records or contact an administrator before punching again.")
	deliverPunchError(cfg, user, chatID, text, replyTo)
}

func deliverPunchError(cfg config, user tgUserRef, chatID, text string, replyTo int64) {
	if needsDeviceSelectionRetry(text) {
		kb, legend := replyKeyboardAndLegendForUser(cfg, chatID, user.ID)
		body := strings.TrimSpace(text)
		if legend != "" {
			body += "\n" + legend
		}
		groupChatID, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
		if err != nil {
			_ = sendTelegramWithMarkup(cfg, user.ID, body, kb)
			return
		}
		_ = sendTelegramHTMLReply(cfg, groupChatID, html.EscapeString(tgAlertPrefix(body)), replyTo, kb)
		return
	}
	if isUnboundAlert(text) {
		kb := replyKeyboardForUser(cfg, chatID, user.ID)
		replyWorkHTMLInChatMarkup(cfg, chatID, enrichUnboundAlertHTML(user, text), replyTo, kb)
		return
	}
	replyWorkHTMLError(cfg, user, chatID, text, replyTo)
}
func answerCallback(cfg config, callbackID, text string) {
	answerCallbackQuery(cfg, callbackID, text, false)
}
func answerCallbackAlert(cfg config, callbackID, text string) {
	answerCallbackQuery(cfg, callbackID, text, true)
}
func answerCallbackQuery(cfg config, callbackID, text string, showAlert bool) {
	payload := map[string]interface{}{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	if showAlert {
		payload["show_alert"] = true
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", cfg.BotToken)
	resp, err := telegramPost(cfg, url, bytes.NewReader(body))
	if err == nil {
		drainResponseBody(resp.Body)
	}
}
func handleChannelPost(cfg config, u tgUpdate) {
	msg := u.ChannelPost
	if msg == nil || isDuplicateUpdate(u.UpdateID) {
		return
	}
	recordChatSeen(cfg, msg.Chat.ID, msg.Chat.Type, msg.Chat.Title)
	text := strings.TrimSpace(msg.Text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return
	}
	if handleChatIDSlash(cfg, msg) {
		return
	}
}
func handleChatIDSlash(cfg config, msg *tgMessage) bool {
	if msg == nil {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if !strings.HasPrefix(text, "/") {
		return false
	}
	parts := strings.Fields(text)
	cmd := strings.TrimPrefix(strings.ToLower(parts[0]), "/")
	if i := strings.Index(cmd, "@"); i > 0 {
		cmd = cmd[:i]
	}
	if !isChatIDCommand(cmd) {
		return false
	}
	replyChatID(cfg, msg.Chat.ID, msg.Chat.Type, msg.Chat.Title, msg.MessageID)
	return true
}
func isChatIDCommand(cmd string) bool {
	switch cmd {
	case "chatid", "groupid", "channelid", "id", "群id", "频道id":
		return true
	default:
		return false
	}
}
func handleMessage(cfg config, u tgUpdate) {
	msg := u.Message
	if isDuplicateUpdate(u.UpdateID) {
		return
	}
	if msg.From.ID == 0 {
		return
	}
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	// B logs in after A: refresh before any local prefix so cache is current employee (default zh).
	refreshUserLangFromBackend(cfg, chatID, msg.From.ID)
	activitiesMu.RLock()
	lookupEmpty := len(cachedButtonLookup) == 0
	activitiesMu.RUnlock()
	if lookupEmpty {
		refreshRuntimeConfig(&cfg, chatID)
	}
	// Discovery metadata is persisted after the business reply, without background goroutines.
	defer recordChatSeen(cfg, msg.Chat.ID, msg.Chat.Type, msg.Chat.Title)
	text := strings.TrimSpace(msg.Text)
	user := tgUserFromMessage(msg)
	userID := msg.From.ID

	if strings.HasPrefix(text, "/") {
		if handleSlashCommand(cfg, msg) {
			return
		}
	}

	if !allowUserRate(userID) {
		_ = sendTelegramByUser(cfg, userID, tRateLimited())
		return
	}

	baseText, deviceHint := splitDeviceTag(text)
	action := resolveButton(baseText)
	switch action {
	case "chatid":
		replyChatID(cfg, msg.Chat.ID, msg.Chat.Type, msg.Chat.Title, msg.MessageID)
	case "work_start_day":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "ci", "day",
			"请选择白班上班设备", func() {
				replyCheckIn(cfg, user, chatID, "day", msg.MessageID, deviceHint)
			})
	case "work_start_middle":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "ci", "middle",
			"请选择中班上班设备", func() {
				replyCheckIn(cfg, user, chatID, "middle", msg.MessageID, deviceHint)
			})
	case "work_start_night":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "ci", "night",
			"请选择夜班上班设备", func() {
				replyCheckIn(cfg, user, chatID, "night", msg.MessageID, deviceHint)
			})
	case "checkin":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "ci", "-",
			"请选择上班设备", func() {
				replyCheckIn(cfg, user, chatID, "", msg.MessageID, deviceHint)
			})
	case "work_end":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "co", "-",
			"请选择下班设备", func() {
				replyCheckOut(cfg, user, chatID, msg.MessageID, deviceHint)
			})
	case "status":
		replyStatus(cfg, userID)
	case "my_record":
		replyMyInfo(cfg, user, chatID, "", msg.MessageID)
	case "my_record_day":
		replyMyInfo(cfg, user, chatID, "day", msg.MessageID)
	case "my_record_middle":
		replyMyInfo(cfg, user, chatID, "middle", msg.MessageID)
	case "my_record_night":
		replyMyInfo(cfg, user, chatID, "night", msg.MessageID)
	case "rank":
		replyRankings(cfg, user, chatID, "", msg.MessageID)
	case "rank_day":
		replyRankings(cfg, user, chatID, "day", msg.MessageID)
	case "rank_middle":
		replyRankings(cfg, user, chatID, "middle", msg.MessageID)
	case "rank_night":
		replyRankings(cfg, user, chatID, "night", msg.MessageID)
	case "export_data":
		exportMsg := *msg
		exportMsg.Text = "/export"
		handleSlashCommand(cfg, &exportMsg)
	case "handover":
		replyHandover(cfg, userID, "")
	case "back":
		withDeviceSelection(cfg, userID, chatID, msg.MessageID, "bk", "-",
			"请选择回座设备", func() {
				replyEndActivity(cfg, user, chatID, msg.MessageID, deviceHint)
			})
	case "help":
		replyHelp(cfg, msg)
	default:
		if strings.HasPrefix(text, "交接班 ") {
			replyHandover(cfg, userID, strings.TrimSpace(strings.TrimPrefix(text, "交接班")))
			return
		}
		if strings.HasPrefix(text, "绑定工位 ") || strings.HasPrefix(text, "绑定工位") {
			code := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "绑定工位 "), "绑定工位"))
			if code != "" {
				replyBindWorkstation(cfg, userID, msg.From.Username, chatID, code, msg.Chat.ID, msg.MessageID)
				return
			}
		}
		if strings.HasPrefix(text, "绑定 ") || strings.HasPrefix(text, "绑定") {
			code := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "绑定 "), "绑定"))
			if code != "" {
				replyBind(cfg, userID, msg.From.Username, chatID, code, msg.Chat.ID, msg.MessageID)
				return
			}
		}
		if looksLikeInviteCode(text) {
			replyBind(cfg, userID, msg.From.Username, chatID, text, msg.Chat.ID, msg.MessageID)
			return
		}
		if act := resolveActivityName(baseText); act != "" {
			if deviceHint != "" {
				replyStartActivity(cfg, user, chatID, act, msg.MessageID, deviceHint)
			} else {
				replyStartActivityWithDeviceSelection(cfg, user, chatID, act, msg.MessageID)
			}
			return
		}
		if act := resolveActivityName(text); act != "" {
			replyStartActivityWithDeviceSelection(cfg, user, chatID, act, msg.MessageID)
			return
		}
		if strings.HasPrefix(text, "/") {
			return
		}
		if text != "" && (msg.Chat.Type == "group" || msg.Chat.Type == "supergroup") {
			_ = sendTelegramHTMLReply(cfg, msg.Chat.ID,
				"未识别操作。群内键盘无反应时请在 @BotFather 关闭 Privacy Mode，或使用 /checkin /checkout /bind 邀请码",
				msg.MessageID, replyKeyboardForUser(cfg, chatID, userID))
		}
	}
}
func handleMyChatMember(cfg config, u tgUpdate) {
	m := u.MyChatMember
	if m == nil || !m.NewChatMember.User.IsBot {
		return
	}
	oldStatus := m.OldChatMember.Status
	newStatus := m.NewChatMember.Status
	if !isBotJoinTransition(oldStatus, newStatus) {
		return
	}
	chatSeenCache.Delete(m.Chat.ID) // joining a group always refreshes discovery data
	recordChatSeen(cfg, m.Chat.ID, m.Chat.Type, m.Chat.Title)
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	text := buildGroupWelcome(m.Chat.Type, m.Chat.Title, m.Chat.ID)
	_ = sendTelegramWithMarkup(cfg, m.Chat.ID, text, replyKeyboardForChat(cfg, chatID))
}
func isBotJoinTransition(oldStatus, newStatus string) bool {
	if !isActiveMemberStatus(newStatus) {
		return false
	}
	return isInactiveMemberStatus(oldStatus)
}
func buildGroupWelcome(chatType, title string, chatID int64) string {
	return tGroupWelcome(chatType, title, chatID)
}
func isInactiveMemberStatus(status string) bool {
	switch status {
	case "left", "kicked", "":
		return true
	default:
		return false
	}
}
func isActiveMemberStatus(status string) bool {
	switch status {
	case "member", "administrator", "creator", "restricted":
		return true
	default:
		return false
	}
}
func replyChatID(cfg config, chatID int64, chatType, title string, replyTo int64) {
	text := tChatIDHint(chatType, title, chatID)
	if chatType == "channel" {
		switch currentLangMode() {
		case "vi":
			text += "\n\nThêm ID kênh trong cấu hình push."
		case "zh":
			text += "\n\n在后台推送配置中绑定此频道 ID。"
		default:
			text += "\n\n后台推送配置绑定 / Cấu hình push"
		}
	} else if chatType == "group" || chatType == "supergroup" {
		switch currentLangMode() {
		case "vi":
			text += "\n\nThêm Chat ID trong trang quản trị."
		case "zh":
			text += "\n\n在后台「群组绑定」中添加此 Chat ID。"
		default:
			text += "\n\n后台「群组绑定」/ Trang quản trị"
		}
	}
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if replyTo > 0 {
		payload["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	kb := currentReplyKeyboard()
	if kb != nil {
		payload["reply_markup"] = kb
	}
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err == nil {
		drainResponseBody(resp.Body)
	}
}
func looksLikeBindToken(text string) bool {
	text = strings.TrimSpace(text)
	if len(text) != 32 {
		return false
	}
	for _, ch := range text {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return false
		}
	}
	return true
}
func looksLikeInviteCode(text string) bool {
	text = strings.ToUpper(strings.TrimSpace(text))
	if !strings.Contains(text, "-") {
		return false
	}
	parts := strings.Split(text, "-")
	if len(parts) != 2 {
		return false
	}
	if len(parts[0]) < 4 || len(parts[1]) < 4 {
		return false
	}
	for _, p := range parts {
		for _, ch := range p {
			if (ch < '0' || ch > '9') && (ch < 'A' || ch > 'F') {
				return false
			}
		}
	}
	return true
}
func replyBind(cfg config, userID int64, username, chatID, codeOrToken string, sourceChatID int64, replyTo int64) {
	codeOrToken = strings.TrimSpace(codeOrToken)
	if codeOrToken == "" {
		hint := "个人 TG：\n/bind 邀请码\n或 /bind 账号\n\n多设备共用 TG（共享工位）：\n/bind_multi 账号\n或 /bind_multi 邀请码\n\n单设备工位 TG：\n/bind_workstation 邀请码\n\n客户端：托盘「绑定 Telegram」"
		_ = sendTelegramByUser(cfg, userID, hint)
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "请私聊机器人发送绑定命令。", replyTo)
		}
		return
	}
	parts := strings.Fields(codeOrToken)

	payload := map[string]interface{}{
		"telegramUserId":   userID,
		"telegramUsername": strings.TrimPrefix(strings.TrimSpace(username), "@"),
		"chatId":           chatID,
	}
	if len(parts) == 2 && looksLikeBindToken(parts[1]) {
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "请私聊机器人发送绑定命令。", replyTo)
			return
		}
		payload["username"] = parts[0]
		payload["password"] = parts[1]
	} else if len(parts) >= 2 && looksLikeInviteCode(parts[0]) {
		payload["inviteCode"] = strings.ToUpper(parts[0])
		payload["username"] = parts[1]
		if len(parts) >= 3 {
			payload["password"] = strings.Join(parts[2:], " ")
		}
	} else if len(parts) == 1 && looksLikeInviteCode(parts[0]) {
		payload["inviteCode"] = strings.ToUpper(parts[0])
	} else if len(parts) == 1 && looksLikeBindToken(parts[0]) {
		payload["bindToken"] = parts[0]
	} else if len(parts) == 2 {
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "请私聊机器人发送绑定命令。", replyTo)
			return
		}
		payload["username"] = parts[0]
		payload["password"] = parts[1]
	} else if len(parts) == 1 {
		payload["username"] = parts[0]
	} else {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("用法：\n个人：/bind 邀请码 或 /bind 账号\n多设备工位：/bind_multi 账号 或 /bind_multi 邀请码"))
		return
	}
	if len(parts) == 1 {
		payload["identifier"] = parts[0]
	}
	var result apiResult
	result.Data = map[string]interface{}{}
	err := backendPost(cfg, "/telegram/worker/bind", payload, &result)
	if err != nil {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("绑定失败："+err.Error()))
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "绑定失败，详细原因已私聊发送。", replyTo)
		}
		return
	}
	if result.Code != 0 {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix(result.Message))
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "绑定失败，详细原因已私聊发送。", replyTo)
		}
		return
	}
	name, _ := result.Data["employeeName"].(string)
	if name == "" {
		name = "员工"
	}
	successMsg := fmt.Sprintf("✅ 已绑定员工：%s（个人 TG，无需客户端）\n现在可以在考勤群打卡。", name)
	_ = sendTelegramByUser(cfg, userID, successMsg)
	if sourceChatID != userID {
		_ = sendTelegramChatReply(cfg, sourceChatID, fmt.Sprintf("✅ %s 已绑定个人 Telegram，可以打卡了。", name), replyTo)
	}
}
func replyBindMulti(cfg config, userID int64, username, chatID, args string, sourceChatID int64, replyTo int64) {
	args = strings.TrimSpace(args)
	parts := strings.Fields(args)
	if len(parts) != 1 {
		hint := "多设备共用 TG：\n/bind_multi 账号\n或 /bind_multi 邀请码\n\n与旧版「每台 /bind 设备ID 邀请码」相同：同一 TG 绑定该员工名下全部共享工位设备。"
		_ = sendTelegramByUser(cfg, userID, hint)
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "请私聊机器人发送 /bind_multi 账号 或 /bind_multi 邀请码。", replyTo)
		}
		return
	}
	arg := parts[0]
	payload := map[string]interface{}{
		"telegramUserId":   userID,
		"telegramUsername": strings.TrimPrefix(strings.TrimSpace(username), "@"),
		"chatId":           chatID,
	}
	if looksLikeInviteCode(arg) {
		payload["inviteCode"] = strings.ToUpper(arg)
	} else {
		payload["username"] = arg
	}
	var result apiResult
	result.Data = map[string]interface{}{}
	err := backendPost(cfg, "/telegram/worker/bind-device", payload, &result)
	if err != nil {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("绑定失败："+err.Error()))
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "绑定失败，详细原因已私聊发送。", replyTo)
		}
		return
	}
	if result.Code != 0 {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix(result.Message))
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "绑定失败，详细原因已私聊发送。", replyTo)
		}
		return
	}
	deviceName, _ := result.Data["deviceName"].(string)
	if deviceName == "" {
		deviceName = "共享工位"
	}
	deviceCount := 0
	if n, ok := result.Data["deviceCount"].(float64); ok {
		deviceCount = int(n)
	}
	successMsg := fmt.Sprintf("✅ 工位 TG 已绑定 %d 台设备（多设备同 TG）", deviceCount)
	if deviceCount <= 0 {
		successMsg = fmt.Sprintf("✅ 工位 TG 已绑定：%s（多设备同 TG）", deviceName)
	} else if names, ok := result.Data["deviceNames"].([]interface{}); ok && len(names) > 0 {
		parts := make([]string, 0, len(names))
		for _, n := range names {
			if s, ok := n.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		if len(parts) > 0 {
			successMsg += "：\n- " + strings.Join(parts, "\n- ")
		}
	} else {
		successMsg += "：" + deviceName
	}
	successMsg += "\n打卡前请在对应 PC Agent 员工登录；多台同时登录时，点普通打卡按钮后再选择设备。"
	if legend, _ := result.Data["deviceTagLegend"].(string); strings.TrimSpace(legend) != "" {
		successMsg += "\n" + strings.TrimSpace(legend)
	}
	kb := replyKeyboardForUser(cfg, chatID, userID)
	if rawKB, ok := result.Data["replyKeyboard"].(map[string]interface{}); ok && rawKB != nil {
		kb = rawKB
	}
	_ = sendTelegramWithMarkup(cfg, userID, successMsg, kb)
	if sourceChatID != userID {
		groupMsg := fmt.Sprintf("✅ 工位 Telegram 已绑定 %d 台设备。多台同时登录时，点普通打卡按钮后再选择设备。", deviceCount)
		if deviceCount <= 0 {
			groupMsg = fmt.Sprintf("✅ 工位 Telegram 已绑定 %s。多台同时登录时，点普通打卡按钮后再选择设备。", deviceName)
		}
		if legend, _ := result.Data["deviceTagLegend"].(string); strings.TrimSpace(legend) != "" {
			groupMsg += "\n" + strings.TrimSpace(legend)
		}
		_ = sendTelegramHTMLReply(cfg, sourceChatID, html.EscapeString(groupMsg), replyTo, kb)
	}
}
func replyBindWorkstation(cfg config, userID int64, username, chatID, inviteCode string, sourceChatID int64, replyTo int64) {
	inviteCode = strings.TrimSpace(strings.ToUpper(strings.Fields(inviteCode)[0]))
	if inviteCode == "" || !looksLikeInviteCode(inviteCode) {
		hint := "工位共用 TG 绑定：\n/bind_workstation 邀请码\n\n绑定后打卡需先在 PC 上员工登录。"
		_ = sendTelegramByUser(cfg, userID, hint)
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "请私聊机器人发送 /bind_workstation 邀请码。", replyTo)
		}
		return
	}
	payload := map[string]interface{}{
		"telegramUserId":   userID,
		"telegramUsername": strings.TrimPrefix(strings.TrimSpace(username), "@"),
		"inviteCode":       inviteCode,
		"chatId":           chatID,
	}
	var result apiResult
	result.Data = map[string]interface{}{}
	err := backendPost(cfg, "/telegram/worker/bind-workstation", payload, &result)
	if err != nil {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("绑定失败："+err.Error()))
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "绑定失败，详细原因已私聊发送。", replyTo)
		}
		return
	}
	if result.Code != 0 {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix(result.Message))
		if sourceChatID != userID {
			_ = sendTelegramChatReply(cfg, sourceChatID, "绑定失败，详细原因已私聊发送。", replyTo)
		}
		return
	}
	deviceName, _ := result.Data["deviceName"].(string)
	if deviceName == "" {
		deviceName = "共享工位"
	}
	successMsg := fmt.Sprintf("✅ 工位 TG 已绑定设备：%s\n只需绑定一次；打卡前请先在 PC Agent 员工登录。", deviceName)
	_ = sendTelegramByUser(cfg, userID, successMsg)
	if sourceChatID != userID {
		_ = sendTelegramChatReply(cfg, sourceChatID, fmt.Sprintf("✅ 工位 Telegram 已绑定 %s。", deviceName), replyTo)
	}
}
func replyCheckIn(cfg config, user tgUserRef, chatID, shift string, replyTo int64, deviceHint string) {
	var result apiResult
	result.Data = map[string]interface{}{}
	payload := map[string]interface{}{
		"telegramUserId": user.ID,
		"chatId":         chatID,
	}
	if shift != "" {
		payload["shift"] = shift
	}
	if strings.TrimSpace(deviceHint) != "" {
		payload["deviceHint"] = strings.TrimSpace(deviceHint)
	}
	err := backendPost(cfg, "/telegram/worker/checkin", payload, &result)
	if err != nil {
		replyPunchTransportError(cfg, user, chatID, punchFailPrefix, err, replyTo)
		return
	}
	if result.Code != 0 {
		replyPunchError(cfg, user, chatID, result.Message, replyTo)
		return
	}
	if groupHTML, ok := result.Data["groupReplyHtml"].(string); ok && strings.TrimSpace(groupHTML) != "" {
		replyWorkHTMLInChat(cfg, user, chatID, groupHTML, replyTo)
		return
	}
	name, _ := result.Data["employeeName"].(string)
	date, _ := result.Data["date"].(string)
	shiftLabel, _ := result.Data["shiftLabel"].(string)
	if name == "" {
		name = "员工"
	}
	msg := fmt.Sprintf("✅ %s 已上班（%s）", name, date)
	if shiftLabel != "" {
		msg += fmt.Sprintf(" · %s", shiftLabel)
	}
	if fine, ok := result.Data["workFine"].(float64); ok && fine > 0 {
		msg += fmt.Sprintf("\n⚠️ 迟到罚款 ¥%.2f", fine)
	}
	if hint, ok := result.Data["handoverHint"].(string); ok && hint != "" {
		msg += "\n" + hint
	}
	replyWorkHTMLError(cfg, user, chatID, msg, replyTo)
}
func replyCheckOut(cfg config, user tgUserRef, chatID string, replyTo int64, deviceHint string) {
	var result apiResult
	result.Data = map[string]interface{}{}
	payload := map[string]interface{}{
		"telegramUserId": user.ID,
		"chatId":         chatID,
	}
	if strings.TrimSpace(deviceHint) != "" {
		payload["deviceHint"] = strings.TrimSpace(deviceHint)
	}
	err := backendPost(cfg, "/telegram/worker/checkout", payload, &result)
	if err != nil {
		replyPunchTransportError(cfg, user, chatID, checkoutFailPrefix, err, replyTo)
		return
	}
	if result.Code != 0 {
		replyPunchError(cfg, user, chatID, result.Message, replyTo)
		return
	}
	if groupHTML, ok := result.Data["groupReplyHtml"].(string); ok && strings.TrimSpace(groupHTML) != "" {
		replyWorkHTMLInChat(cfg, user, chatID, groupHTML, replyTo)
		return
	}
	name, _ := result.Data["employeeName"].(string)
	if name == "" {
		name = "员工"
	}
	msg := fmt.Sprintf("✅ %s 已下班", name)
	if shiftLabel, ok := result.Data["shiftLabel"].(string); ok && shiftLabel != "" {
		msg += fmt.Sprintf("（%s）", shiftLabel)
	}
	replyWorkHTMLError(cfg, user, chatID, msg, replyTo)
}
func replyWorkHTMLError(cfg config, user tgUserRef, chatID, text string, replyTo int64) {
	replyWorkHTMLInChat(cfg, user, chatID, html.EscapeString(tgAlertPrefix(text)), replyTo)
}
func replyWorkHTMLInChat(cfg config, user tgUserRef, chatID, htmlText string, replyTo int64) {
	replyWorkHTMLInChatMarkup(cfg, chatID, htmlText, replyTo, replyKeyboardForUser(cfg, chatID, user.ID))
}
func replyWorkHTMLInChatMarkup(cfg config, chatID, htmlText string, replyTo int64, replyMarkup map[string]interface{}) {
	groupChatID, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	if err != nil {
		return
	}
	_ = sendTelegramHTMLReply(cfg, groupChatID, htmlText, replyTo, replyMarkup)
}
func replyActivityEndHTML(cfg config, chatID, htmlText string, replyTo int64, data map[string]interface{}) {
	if target, _ := data["groupReplyChatId"].(string); strings.TrimSpace(target) != "" && target != chatID {
		chatID = target
		replyTo = activityAnchorReplyTo(data, 0)
	}
	groupID, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return
	}
	msgID, err := sendTelegramHTMLReplyReturnID(cfg, groupID, htmlText, replyTo, nil)
	sessionID, _ := data["sessionId"].(string)
	if err == nil && msgID > 0 && sessionID != "" {
		_ = backendPost(cfg, "/telegram/worker/activity/message-id", map[string]interface{}{
			"sessionId": sessionID, "messageId": msgID, "ended": true,
		}, &apiResult{})
	}
}

func replyStatus(cfg config, userID int64) {
	var result struct {
		Code int `json:"code"`
		Data struct {
			Bound             bool     `json:"bound"`
			EmployeeName      string   `json:"employeeName"`
			EmployeeCode      string   `json:"employeeCode"`
			WorkSessionOpen   bool     `json:"workSessionOpen"`
			CurrentActivity   string   `json:"currentActivity"`
			ActivityStartedAt string   `json:"activityStartedAt"`
			ShiftLabel        string   `json:"shiftLabel"`
			ShiftType         string   `json:"shiftType"`
			OpenShifts        []string `json:"openShifts"`
		} `json:"data"`
	}
	err := backendGet(cfg, fmt.Sprintf("/telegram/worker/status?telegramUserId=%d", userID), &result)
	if err != nil {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("查询失败："+err.Error()))
		return
	}
	if result.Code != 0 || !result.Data.Bound {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("尚未绑定员工账号，请联系管理员在后台绑定。"))
		return
	}
	status := "未上班"
	if result.Data.WorkSessionOpen {
		status = "上班中"
	}
	msg := fmt.Sprintf("%s", result.Data.EmployeeName)
	if result.Data.EmployeeCode != "" {
		msg += fmt.Sprintf("（%s）", result.Data.EmployeeCode)
	}
	msg += " · " + status
	if result.Data.ShiftLabel != "" && result.Data.WorkSessionOpen {
		msg += fmt.Sprintf("（%s）", result.Data.ShiftLabel)
	}
	if len(result.Data.OpenShifts) > 1 {
		msg += fmt.Sprintf("\n进行中班次：%s", strings.Join(result.Data.OpenShifts, "、"))
	}
	if result.Data.CurrentActivity != "" {
		msg += fmt.Sprintf("\n进行中活动：%s", result.Data.CurrentActivity)
		if result.Data.ActivityStartedAt != "" {
			msg += fmt.Sprintf("（自 %s）", result.Data.ActivityStartedAt)
		}
	}
	_ = sendTelegramByUser(cfg, userID, msg)
}
func replyStartActivity(cfg config, user tgUserRef, chatID, activity string, replyTo int64, deviceHint string) {
	var result apiResult
	result.Data = map[string]interface{}{}
	payload := map[string]interface{}{
		"telegramUserId":   user.ID,
		"chatId":           chatID,
		"activity":         activity,
		"replyToMessageId": replyTo,
	}
	if strings.TrimSpace(deviceHint) != "" {
		payload["deviceHint"] = strings.TrimSpace(deviceHint)
	}
	err := backendPost(cfg, "/telegram/worker/activity/start", payload, &result)
	if err != nil {
		replyPunchTransportError(cfg, user, chatID, startActivityFailPrefix, err, replyTo)
		return
	}
	if result.Code != 0 {
		replyPunchError(cfg, user, chatID, result.Message, replyTo)
		return
	}
	if groupReplySent(result.Data) {
		return
	}
	if target, _ := result.Data["groupReplyChatId"].(string); strings.TrimSpace(target) != "" && target != chatID {
		chatID, replyTo = target, 0
	}

	groupHTML, _ := result.Data["groupReplyHtml"].(string)
	if strings.TrimSpace(groupHTML) == "" {
		msg, _ := result.Data["messageText"].(string)
		if msg == "" {
			name, _ := result.Data["activityName"].(string)
			limit, _ := result.Data["limitSeconds"].(float64)
			msg = fmt.Sprintf("⏱ 已开始「%s」，时限 %d 秒", name, int(limit))
		}
		groupHTML = html.EscapeString(msg)
	}
	inline, _ := result.Data["inlineKeyboard"].(map[string]interface{})
	sessionID, _ := result.Data["sessionId"].(string)
	groupChatID, parseErr := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64)
	if parseErr == nil && groupChatID < 0 {
		kb := replyKeyboardForUser(cfg, chatID, user.ID)
		msgID, sendErr := sendTelegramHTMLReplyReturnID(cfg, groupChatID, groupHTML, replyTo, kb)
		if sendErr == nil && sessionID != "" && msgID > 0 {
			_ = backendPost(cfg, "/telegram/worker/activity/message-id", map[string]interface{}{
				"sessionId": sessionID, "messageId": msgID,
			}, &apiResult{})
		}
		return
	}
	msgID, err := sendTelegramWithMarkupReturnID(cfg, user.ID, groupHTML, inline)
	if err == nil && sessionID != "" && msgID > 0 {
		_ = backendPost(cfg, "/telegram/worker/activity/message-id", map[string]interface{}{
			"sessionId": sessionID, "messageId": msgID,
		}, &apiResult{})
	}
}
func replyEndActivity(cfg config, user tgUserRef, chatID string, replyTo int64, deviceHint string) {
	var result apiResult
	result.Data = map[string]interface{}{}
	payload := map[string]interface{}{
		"telegramUserId": user.ID,
	}
	if strings.TrimSpace(deviceHint) != "" {
		payload["deviceHint"] = strings.TrimSpace(deviceHint)
	}
	err := backendPost(cfg, "/telegram/worker/activity/end", payload, &result)
	if err != nil {
		replyPunchTransportError(cfg, user, chatID, endActivityFailPrefix, err, replyTo)
		return
	}
	if result.Code != 0 {
		replyPunchError(cfg, user, chatID, result.Message, replyTo)
		return
	}
	if groupReplySent(result.Data) {
		return
	}
	if groupHTML, ok := result.Data["groupReplyHtml"].(string); ok && strings.TrimSpace(groupHTML) != "" {
		replyActivityEndHTML(cfg, chatID, groupHTML, activityAnchorReplyTo(result.Data, replyTo), result.Data)
		return
	}
	name, _ := result.Data["activityName"].(string)
	elapsed, _ := result.Data["elapsedSeconds"].(float64)
	fine, _ := result.Data["fineAmount"].(float64)
	msg := fmt.Sprintf("✅ 「%s」已结束，用时 %s", name, formatDurationZh(int(elapsed)))
	if fine > 0 {
		msg += fmt.Sprintf("\n⚠️ 超时罚款 ¥%.2f（已写入罚款明细）", fine)
	}
	replyWorkHTMLInChatMarkup(cfg, chatID, html.EscapeString(tgAlertPrefix(msg)), activityAnchorReplyTo(result.Data, replyTo), nil)
}
func groupReplySent(data map[string]interface{}) bool {
	if data == nil {
		return false
	}
	switch v := data["groupReplySent"].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}
func activityAnchorReplyTo(data map[string]interface{}, fallback int64) int64 {
	if data == nil {
		return fallback
	}
	switch v := data["activityAnchorMessageId"].(type) {
	case float64:
		if int64(v) > 0 {
			return int64(v)
		}
	case int64:
		if v > 0 {
			return v
		}
	case int:
		if int64(v) > 0 {
			return int64(v)
		}
	}
	return fallback
}
func replyMyInfo(cfg config, user tgUserRef, chatID, shift string, replyTo int64) {
	path := fmt.Sprintf("/telegram/worker/myinfo?telegramUserId=%d", user.ID)
	if strings.TrimSpace(chatID) != "" {
		path += "&chatId=" + urlPackage2.QueryEscape(chatID)
	}
	if shift != "" {
		path += "&shift=" + urlPackage2.QueryEscape(shift)
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			Bound       bool   `json:"bound"`
			DisplayText string `json:"displayText"`
		} `json:"data"`
	}
	err := backendGet(cfg, path, &result)
	if err != nil {
		replyStatsPlain(cfg, user, chatID, tgAlertPrefix("查询失败："+err.Error()), replyTo)
		return
	}
	if result.Code != 0 || !result.Data.Bound {
		replyStatsPlain(cfg, user, chatID, tgAlertPrefix("尚未绑定员工账号。"), replyTo)
		return
	}
	text := result.Data.DisplayText
	if text == "" {
		text = "暂无记录"
	}
	replyStatsPlain(cfg, user, chatID, text, replyTo)
}
func replyRankings(cfg config, user tgUserRef, chatID, shift string, replyTo int64) {
	path := fmt.Sprintf("/telegram/worker/rankings?telegramUserId=%d", user.ID)
	if strings.TrimSpace(chatID) != "" {
		path += "&chatId=" + urlPackage2.QueryEscape(chatID)
	}
	if shift != "" {
		path += "&shift=" + urlPackage2.QueryEscape(shift)
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			DisplayText string `json:"displayText"`
		} `json:"data"`
	}
	err := backendGet(cfg, path, &result)
	if err != nil {
		replyStatsPlain(cfg, user, chatID, tgAlertPrefix("查询排行榜失败："+err.Error()), replyTo)
		return
	}
	text := result.Data.DisplayText
	if result.Code != 0 || text == "" {
		text = "今日暂无排行数据"
	}
	replyStatsPlain(cfg, user, chatID, text, replyTo)
}
func replyStatsPlain(cfg config, user tgUserRef, chatID, text string, replyTo int64) {
	if groupChatID, err := strconv.ParseInt(strings.TrimSpace(chatID), 10, 64); err == nil && groupChatID < 0 {
		kb := replyKeyboardForUser(cfg, chatID, user.ID)
		if isUnboundAlert(text) {
			_ = sendTelegramHTMLReply(cfg, groupChatID, enrichUnboundAlertHTML(user, text), replyTo, kb)
			return
		}
		_ = sendTelegramPlainReply(cfg, groupChatID, text, replyTo, kb)
		return
	}
	_ = sendTelegramByUser(cfg, user.ID, text)
}
func sendTelegramPlainReply(cfg config, chatID int64, text string, replyTo int64, replyMarkup map[string]interface{}) error {
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
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	drainResponseBody(resp.Body)
	return nil
}
func replyHandover(cfg config, userID int64, note string) {
	var result apiResult
	result.Data = map[string]interface{}{}
	payload := map[string]interface{}{"telegramUserId": userID}
	if note != "" {
		payload["note"] = note
	}
	err := backendPost(cfg, "/telegram/worker/handover", payload, &result)
	if err != nil {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix("交接班失败："+err.Error()))
		return
	}
	if result.Code != 0 {
		_ = sendTelegramByUser(cfg, userID, tgAlertPrefix(result.Message))
		return
	}
	shiftLabel, _ := result.Data["shiftLabel"].(string)
	_ = sendTelegramByUser(cfg, userID, fmt.Sprintf("✅ 交接班完成，当前班次：%s", shiftLabel))
}

type apiResult struct {
	Code    int                    `json:"code"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data"`
}

func (cfg config) requestContext() context.Context {
	if cfg.Context != nil {
		return cfg.Context
	}
	return context.Background()
}

const backendReadTimeout = 5 * time.Second
const backendWriteTimeout = 15 * time.Second
const telegramSendTimeout = 8 * time.Second

func boundedHTTPClient(timeout time.Duration) http.Client {
	client := *http.DefaultClient
	client.Timeout = timeout
	return client
}

// Keep each Telegram call within the update's remaining budget. Never log URLs:
// Telegram embeds the bot token in the URL path.
// An unread successful response prevents net/http from reusing its connection.
// Drain small Telegram replies (also safe after JSON was already read), keeping
// the configured client deadline and a size cap for unexpected response bodies.
func drainResponseBody(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	_ = body.Close()
}

// WarmTelegramConnection keeps a live TLS connection to Telegram in the shared
// HTTP transport so the next reply does not pay for a fresh TCP+TLS handshake
// (production logs: ~900ms for the first send after idle, ~380ms on a warm one).
// Best effort: failures are ignored and the token is never logged.
func WarmTelegramConnection(botToken string) {
	if strings.TrimSpace(botToken) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.telegram.org/bot"+botToken+"/getMe", nil)
	if err != nil {
		return
	}
	client := boundedHTTPClient(5 * time.Second)
	if resp, err := client.Do(req); err == nil {
		drainResponseBody(resp.Body)
	}
}

func telegramPost(cfg config, apiURL string, body io.Reader) (*http.Response, error) {
	started := time.Now()
	req, err := http.NewRequestWithContext(cfg.requestContext(), http.MethodPost, apiURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := boundedHTTPClient(telegramSendTimeout)
	resp, err := client.Do(req)
	log.Printf("[tgbot] stage=telegram_http elapsed_ms=%d failed=%t", time.Since(started).Milliseconds(), err != nil)
	if err != nil && cfg.Failure != nil {
		cfg.Failure.Store(true)
	}
	return resp, err
}

func backendPost(cfg config, path string, payload interface{}, out interface{}) (err error) {
	started := time.Now()
	defer func() {
		log.Printf("[tgbot] stage=backend path=%s elapsed_ms=%d failed=%t", strings.SplitN(path, "?", 2)[0], time.Since(started).Milliseconds(), err != nil)
	}()
	defer func() {
		if err != nil && cfg.Failure != nil {
			cfg.Failure.Store(true)
		}
	}()
	if strings.Contains(path, "/bind") || strings.Contains(path, "/lang") {
		clear(cfg.configResponses)
		userLangRefreshed.Range(func(k, _ interface{}) bool { userLangRefreshed.Delete(k); return true })
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(cfg.requestContext(), http.MethodPost, cfg.BackendURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Worker-Secret", cfg.WorkerSecret)
	if cfg.EventUnix > 0 {
		req.Header.Set("X-TG-Event-Time", strconv.FormatInt(cfg.EventUnix, 10))
	}
	client := boundedHTTPClient(backendWriteTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer drainResponseBody(resp.Body)
	if resp.StatusCode >= 500 && cfg.Failure != nil {
		cfg.Failure.Store(true)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s", string(raw))
	}
	return nil
}
func backendGet(cfg config, path string, out interface{}) (err error) {
	if raw, ok := cfg.configResponses[path]; ok {
		return json.Unmarshal(raw, out)
	}
	started := time.Now()
	defer func() {
		log.Printf("[tgbot] stage=backend path=%s elapsed_ms=%d failed=%t", strings.SplitN(path, "?", 2)[0], time.Since(started).Milliseconds(), err != nil)
	}()
	defer func() {
		if err != nil && cfg.Failure != nil {
			cfg.Failure.Store(true)
		}
	}()
	req, err := http.NewRequestWithContext(cfg.requestContext(), http.MethodGet, cfg.BackendURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Telegram-Worker-Secret", cfg.WorkerSecret)
	if cfg.EventUnix > 0 {
		req.Header.Set("X-TG-Event-Time", strconv.FormatInt(cfg.EventUnix, 10))
	}
	client := boundedHTTPClient(backendReadTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer drainResponseBody(resp.Body)
	if resp.StatusCode >= 500 && cfg.Failure != nil {
		cfg.Failure.Store(true)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return err
	}
	if cfg.configResponses != nil && resp.StatusCode == http.StatusOK && strings.HasPrefix(path, "/telegram/worker/config?") {
		var result struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(raw, &result) == nil && result.Code == 0 {
			cfg.configResponses[path] = raw
		}
	}
	return nil
}
func sendTelegramByUser(cfg config, userID int64, text string) error {
	return sendTelegramWithMarkup(cfg, userID, text, replyKeyboardForChat(cfg, strconv.FormatInt(userID, 10)))
}
func sendTelegramChatReply(cfg config, chatID int64, text string, replyTo int64) error {
	return sendTelegramHTMLReply(cfg, chatID, text, replyTo, nil)
}
func sendTelegramHTMLReply(cfg config, chatID int64, text string, replyTo int64, replyMarkup map[string]interface{}) error {
	_, err := sendTelegramHTMLReplyReturnID(cfg, chatID, text, replyTo, replyMarkup)
	return err
}
func sendTelegramHTMLReplyReturnID(cfg config, chatID int64, text string, replyTo int64, replyMarkup map[string]interface{}) (messageID int64, err error) {
	started := time.Now()
	defer func() {
		log.Printf("[tgbot] stage=telegram_reply elapsed_ms=%d failed=%t", time.Since(started).Milliseconds(), err != nil)
		if !cfg.ReceivedAt.IsZero() {
			log.Printf("[tgbot] stage=reply_delivery update=%d since_received_ms=%d failed=%t", cfg.UpdateID, time.Since(cfg.ReceivedAt).Milliseconds(), err != nil)
		}
	}()
	defer func() {
		if err != nil && cfg.Failure != nil {
			cfg.Failure.Store(true)
		}
	}()
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if replyTo > 0 {
		payload["reply_parameters"] = map[string]interface{}{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer drainResponseBody(resp.Body)
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if !parsed.OK {
		return 0, fmt.Errorf("telegram send failed (HTTP %d): %s", resp.StatusCode, strings.ReplaceAll(parsed.Description, cfg.BotToken, "[redacted]"))
	}
	return parsed.Result.MessageID, nil
}
func sendTelegramWithMarkupReturnID(cfg config, chatID int64, text string, replyMarkup map[string]interface{}) (messageID int64, err error) {
	started := time.Now()
	defer func() {
		log.Printf("[tgbot] stage=telegram_reply elapsed_ms=%d failed=%t", time.Since(started).Milliseconds(), err != nil)
		if !cfg.ReceivedAt.IsZero() {
			log.Printf("[tgbot] stage=reply_delivery update=%d since_received_ms=%d failed=%t", cfg.UpdateID, time.Since(cfg.ReceivedAt).Milliseconds(), err != nil)
		}
	}()
	defer func() {
		if err != nil && cfg.Failure != nil {
			cfg.Failure.Store(true)
		}
	}()
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer drainResponseBody(resp.Body)
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &parsed) != nil || !parsed.OK {
		return 0, fmt.Errorf("sendMessage failed")
	}
	return parsed.Result.MessageID, nil
}
func sendTelegramWithMarkup(cfg config, chatID int64, text string, replyMarkup map[string]interface{}) error {
	payload := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.BotToken)
	resp, err := telegramPost(cfg, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer drainResponseBody(resp.Body)
	return nil
}
func formatDurationZh(seconds int) string {
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

// ---- Source: backend/internal/tgbot/runtime.go ----
var (
	errMissingWorkerSecret = errors.New("missing worker secret")
	errMissingBotToken     = errors.New("bot token not configured")

	runtimeMu sync.RWMutex
	activeCfg config
)

func setActiveConfig(cfg config) {
	cfg.ReceivedAt = time.Time{}
	cfg.UpdateID = 0
	cfg.Context = nil
	cfg.Failure = nil
	cfg.configResponses = nil
	runtimeMu.Lock()
	activeCfg = cfg
	runtimeMu.Unlock()
}

// ---- Source: backend/internal/tgbot/user_display.go ----
type tgUserRef struct {
	ID        int64
	Username  string
	FirstName string
	LastName  string
}

func tgUserFromMessage(msg *tgMessage) tgUserRef {
	if msg == nil {
		return tgUserRef{}
	}
	return tgUserRef{
		ID:        msg.From.ID,
		Username:  msg.From.Username,
		FirstName: msg.From.FirstName,
		LastName:  msg.From.LastName,
	}
}
func tgUserFromCallback(cb *tgCallbackQuery) tgUserRef {
	if cb == nil {
		return tgUserRef{}
	}
	return tgUserRef{
		ID:        cb.From.ID,
		Username:  cb.From.Username,
		FirstName: cb.From.FirstName,
		LastName:  cb.From.LastName,
	}
}
func (u tgUserRef) displayName() string {
	var parts []string
	if s := strings.TrimSpace(u.FirstName); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(u.LastName); s != "" {
		parts = append(parts, s)
	}
	if name := strings.TrimSpace(strings.Join(parts, " ")); name != "" {
		return name
	}
	user := strings.TrimPrefix(strings.TrimSpace(u.Username), "@")
	if user != "" {
		return "@" + user
	}
	if u.ID > 0 {
		return fmt.Sprintf("用户%d", u.ID)
	}
	return "用户"
}
func (u tgUserRef) htmlProfileLink() string {
	name := u.displayName()
	if u.ID > 0 {
		return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, u.ID, html.EscapeString(name))
	}
	return html.EscapeString(name)
}
func isUnboundAlert(text string) bool {
	return strings.Contains(text, "未绑定") ||
		strings.Contains(text, "尚未绑定") ||
		strings.Contains(text, "Chưa gắn") ||
		strings.Contains(text, "Not bound")
}
func buildUnboundAlertHTML(user tgUserRef) string {
	link := user.htmlProfileLink()
	switch userLang(user.ID) {
	case "vi":
		return fmt.Sprintf(
			"⚠️ %s Bạn chưa gắn Telegram chấm công. Hãy gắn theo cách sau rồi mới chấm công:\n"+
				"Cá nhân: /bind mã mời hoặc /bind tài khoản;\n"+
				"Nhiều máy: /bind_multi tài khoản hoặc /bind_multi mã mời;\n"+
				"Một máy: /bind_workstation mã mời",
			link,
		)
	case "en":
		return fmt.Sprintf(
			"⚠️ %s You have not bound Telegram attendance. Bind first, then punch:\n"+
				"Personal: /bind invite or /bind account;\n"+
				"Multi-PC: /bind_multi account or /bind_multi invite;\n"+
				"Single PC: /bind_workstation invite",
			link,
		)
	default:
		return fmt.Sprintf(
			"⚠️ %s 您还未绑定Telegram打卡群，请按照以下方式绑定后再进行打卡活动：\n"+
				"个人 TG：/bind 邀请码 或 /bind 账号；\n"+
				"多设备工位：/bind_multi 账号 或 /bind_multi 邀请码；\n"+
				"单设备工位：/bind_workstation 邀请码",
			link,
		)
	}
}
func enrichUnboundAlertHTML(user tgUserRef, plainText string) string {
	_ = plainText
	return buildUnboundAlertHTML(user)
}

// ---- Source: backend/internal/tgbot/webhook.go ----
func (cfg config) useWebhook() bool {
	return strings.ToLower(strings.TrimSpace(cfg.BotMode)) == "webhook" && strings.TrimSpace(cfg.WebhookURL) != ""
}
func runPollingCtx(ctx context.Context, cfg config) error {
	if err := deleteWebhook(cfg); err != nil {
		log.Printf("delete webhook: %v", err)
	}
	offset := int64(0)
	lastRefresh := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if time.Since(lastRefresh) > 5*time.Minute {
			refreshRuntimeConfig(&cfg, "")
			setActiveConfig(cfg)
			lastRefresh = time.Now()
		}
		updates, err := pollUpdates(cfg, offset)
		if err != nil {
			log.Printf("poll error: %v", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			ProcessUpdate(cfg, u)
		}
	}
}
func runEmbeddedWebhook(ctx context.Context, cfg config) error {
	if err := setWebhook(cfg); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			refreshRuntimeConfig(&cfg, "")
			setActiveConfig(cfg)
		}
	}
}
func runWebhook(cfg config) {
	if err := setWebhook(cfg); err != nil {
		log.Fatalf("set webhook: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	path := cfg.WebhookPath
	if path == "" {
		path = "/telegram/webhook"
	}
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if secret := strings.TrimSpace(cfg.WebhookSecret); secret != "" {
			if !security.SecureCompare(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), secret) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var update tgUpdate
		if err := json.Unmarshal(body, &update); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		go ProcessUpdate(cfg, update)
		w.WriteHeader(http.StatusOK)
	})
	go func() {
		localCfg := cfg
		for {
			time.Sleep(5 * time.Minute)
			refreshRuntimeConfig(&localCfg, "")
		}
	}()
	addr := cfg.WebhookListen
	if addr == "" {
		addr = ":8081"
	}
	log.Printf("telegram-worker webhook mode listening on %s%s (public URL=%s)", addr, path, cfg.WebhookURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// ProcessUpdate handles a single Telegram update.
func ProcessUpdate(cfg config, u tgUpdate) {
	if cfg.configResponses == nil {
		cfg.configResponses = make(map[string][]byte)
	}
	if u.MyChatMember != nil {
		handleMyChatMember(cfg, u)
		return
	}
	if u.CallbackQuery != nil {
		handleCallback(cfg, u)
		return
	}
	if u.ChannelPost != nil {
		handleChannelPost(cfg, u)
		return
	}
	if u.Message == nil {
		return
	}
	if text := strings.TrimSpace(u.Message.Text); text != "" {
		log.Printf("[tgbot] update=%d chat=%d user=%d", u.UpdateID, u.Message.Chat.ID, u.Message.From.ID)
	}
	handleMessage(cfg, u)
}
func deleteWebhook(cfg config) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook?drop_pending_updates=true", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	defer drainResponseBody(resp.Body)
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return err
	}
	if !parsed.OK {
		if parsed.Description != "" {
			return fmt.Errorf("%s", parsed.Description)
		}
		return fmt.Errorf("deleteWebhook failed")
	}
	return nil
}
func setWebhook(cfg config) error {
	payload := map[string]interface{}{
		"url":                  strings.TrimSpace(cfg.WebhookURL),
		"max_connections":      1,
		"allowed_updates":      []string{"message", "callback_query", "my_chat_member", "chat_member"},
		"drop_pending_updates": false,
	}
	if secret := strings.TrimSpace(cfg.WebhookSecret); secret != "" {
		payload["secret_token"] = secret
	}
	body, _ := json.Marshal(payload)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", cfg.BotToken)
	resp, err := telegramPost(cfg, apiURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer drainResponseBody(resp.Body)
	raw, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	if !parsed.OK {
		if parsed.Description != "" {
			return fmt.Errorf("%s", parsed.Description)
		}
		return fmt.Errorf("setWebhook failed: %s", string(raw))
	}
	log.Printf("webhook registered: %s", cfg.WebhookURL)
	return nil
}

// HandleWebhookBody requires a configured webhook secret and processes a webhook payload.
func HandleWebhookBody(cfg config, secretHeader string, body []byte) error {
	secret := strings.TrimSpace(cfg.WebhookSecret)
	if secret == "" {
		return fmt.Errorf("webhook secret not configured")
	}
	if !security.SecureCompare(secretHeader, secret) {
		return fmt.Errorf("forbidden")
	}
	var update tgUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		return err
	}
	go ProcessUpdate(cfg, update)
	return nil
}
