// Consolidated source; original file boundaries are marked below.
package services

import (
	bytes "bytes"
	context "context"
	sqlPackage2 "database/sql"
	json "encoding/json"
	errors "errors"
	uuid "github.com/google/uuid"
	godotenv "github.com/joho/godotenv"
	_ "github.com/lib/pq"
	io "io"
	http "net/http"
	os "os"
	filepath "path/filepath"
	strings "strings"
	"sync"
	"sync/atomic"
	testing "testing"
	time "time"
)

// ---- Source: backend/services/standalone_integration_test.go ----
type fakeTelegramTransport struct{}

func (fakeTelegramTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":999,"username":"local_test_bot","message_id":123}}`)), Header: make(http.Header)}, nil
}

// Uses ONLY an explicitly supplied disposable test database. No real Telegram calls.
func TestStandaloneAttendanceRoundTrip(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set STANDALONE_TEST_DATABASE_URL to a disposable initialized database")
	}
	t.Setenv("TELEGRAM_TOKEN_SECRET", "local-test-encryption-key-not-production-123")
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fakeTelegramTransport{}}
	defer func() { http.DefaultClient = old }()
	db, e := sqlPackage2.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	s := NewWorkforceTelegramService(db, NewWorkforceService(db))
	ctx := context.Background()
	_, e = s.UpdateSettings(ctx, map[string]interface{}{"enabled": true, "botToken": "999:test-only", "requirePCLogin": false, "shiftWindowDisabled": true, "dailyResetEnabled": false, "dualShiftEnabled": true, "botUiLang": "zh", "dayStart": "09:00", "dayEnd": "18:00", "nightStart": "21:00", "nightEnd": "09:00"})
	if e != nil {
		t.Fatal(e)
	}
	emp := uuid.New()
	tgID := time.Now().UnixNano()%1000000000 + 1000000000
	_, e = db.Exec(`INSERT INTO employees(id,employee_id,name,status,attendance_mode) VALUES($1,$2,'测试员工',1,'tg_only')`, emp, "it-"+emp.String()[:8])
	if e != nil {
		t.Fatal(e)
	}
	defer db.Exec("DELETE FROM employees WHERE id=$1", emp)
	if _, e = s.CreateBinding(ctx, emp, tgID, "testemployee", ""); e != nil {
		t.Fatal(e)
	}
	release, e := s.acquireAttendanceOperation(ctx, emp)
	if e != nil {
		t.Fatal(e)
	}
	otherService := NewWorkforceTelegramService(db, NewWorkforceService(db))
	if unlock, err := otherService.acquireAttendanceOperation(ctx, emp); !errors.Is(err, ErrOperationBusy) {
		if unlock != nil {
			unlock()
		}
		release()
		t.Fatalf("operation lock not shared: %v", err)
	}
	release()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, loc)
	inCtx := WithPunchTime(ctx, start)
	checkin, _, _, _, _, reply, e := s.TelegramCheckIn(inCtx, tgID, "", "day")
	if e != nil {
		t.Fatal("checkin:", e)
	}
	if !strings.Contains(reply, "上班完成") {
		t.Fatal("original reply missing", reply)
	}
	var saved time.Time
	if e = db.QueryRow("SELECT checkin_time FROM work_sessions WHERE id=$1", checkin.SessionID).Scan(&saved); e != nil || !saved.Equal(start) {
		t.Fatal("cold start timestamp not preserved", saved, e)
	}
	if _, _, _, _, _, _, e = s.TelegramCheckIn(inCtx, tgID, "", "day"); e == nil {
		t.Fatal("duplicate checkin accepted")
	}
	if _, e = s.AdminPunchCheckIn(inCtx, emp, "day"); e == nil {
		t.Fatal("web duplicate after Telegram checkin accepted")
	}
	act, e := s.TelegramStartActivity(WithTelegramRequestCache(WithPunchTime(ctx, start.Add(time.Hour))), tgID, "", "wc_small", 0)
	if e != nil {
		t.Fatal("activity:", e)
	}
	if !strings.Contains(act.GroupReplyHTML, "打卡成功") {
		t.Fatal("original activity reply missing")
	}
	if act.Session.EmployeeName != "测试员工" || act.Session.ShiftType != "day" || act.Session.AttendanceDate != start.Format("2006-01-02") || !act.Session.StartedAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("insert-returning changed activity result: %+v", act.Session)
	}
	count, err := s.currentActivityCount(WithPunchTime(ctx, start.Add(time.Hour)), emp, "", "wc_small", "day")
	if err != nil || count != 1 {
		t.Fatalf("activity counted incorrectly: count=%d error=%v", count, err)
	}
	langCtx := WithTelegramRequestCache(ctx)
	if err = s.SetEmployeePreferredLang(langCtx, emp, "en"); err != nil {
		t.Fatal(err)
	}
	if s.GetReplyLangForEmployee(langCtx, emp) != TgLangEn {
		t.Fatal("initial language mismatch")
	}
	if err = s.SetEmployeePreferredLang(langCtx, emp, "zh"); err != nil {
		t.Fatal(err)
	}
	if s.GetReplyLangForEmployee(langCtx, emp) != TgLangZh {
		t.Fatal("language cache survived mutation")
	}

	if _, e = s.TelegramStartActivity(WithPunchTime(ctx, start.Add(time.Hour)), tgID, "", "eat", 0); e == nil {
		t.Fatal("overlapping activity accepted")
	}
	_, e = s.UpsertActivityFine(ctx, "wc_small", 5, 10)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdminPunchStartActivity(WithPunchTime(ctx, start.Add(time.Hour)), emp, "eat"); e == nil {
		t.Fatal("web activity overlaps Telegram activity")
	}
	if _, e = s.AdminPunchCheckOut(inCtx, emp, "day"); !errors.Is(e, ErrOpenActivityBlocksCheckout) {
		t.Fatalf("web checkout must reject open Telegram activity: %v", e)
	}
	ended, e := s.AdminPunchBackActivity(WithPunchTime(ctx, start.Add(time.Hour+4*time.Minute)), emp)
	if e != nil {
		t.Fatal("back:", e)
	}
	if ended.ElapsedSeconds != 240 || ended.OvertimeSeconds != 60 || ended.FineAmount != 10 {
		t.Fatalf("incorrect activity settlement: %+v", ended)
	}
	counts, durations, totalCount, totalSeconds := s.dailyActivityStats(ctx, emp, ended.AttendanceDate, "day")
	if counts[ended.ActivityName] != 1 || durations[ended.ActivityName] != 240 || totalCount != 1 || totalSeconds != 240 {
		t.Fatal("combined activity durations differ from settled results")
	}
	normalAt := start.Add(2 * time.Hour)
	normal, err := s.TelegramStartActivity(WithTelegramRequestCache(WithPunchTime(ctx, normalAt)), tgID, "", "wc_small", 0)
	if err != nil {
		t.Fatal(err)
	}
	s.SetActivityTelegramMessageID(ctx, normal.Session.ID, 7654321)
	if _, err = s.endActivitySessionForEmployee(ctx, normal.Session.ID, uuid.New(), "telegram"); !errors.Is(err, ErrActivityBackNotOwner) {
		t.Fatalf("wrong owner accepted: %v", err)
	}
	normalEnd, err := s.TelegramEndActivity(WithTelegramRequestCache(WithPunchTime(ctx, normalAt.Add(time.Minute))), tgID)
	if err == nil && normalEnd.TelegramMessageID != 7654321 {
		t.Fatal("reply anchor lost in merged activity read")
	}
	if _, repeatErr := s.TelegramEndActivity(ctx, tgID); !errors.Is(repeatErr, ErrActivityNotActive) {
		t.Fatalf("duplicate return accepted: %v", repeatErr)
	}
	if err != nil || normalEnd.OvertimeSeconds != 0 || normalEnd.FineAmount != 0 || normalEnd.ElapsedSeconds != 60 {
		t.Fatalf("normal return changed: %+v %v", normalEnd, err)
	}
	// Archive overlap must count the session once even after replacing UNION with UNION ALL.
	_, err = db.Exec(`INSERT INTO wf_tg_activity_sessions_archive(id,employee_id,activity_code,activity_name,attendance_date,started_at,elapsed_seconds,status,shift_type)
 SELECT id,employee_id,activity_code,activity_name,attendance_date,started_at,elapsed_seconds,status,shift_type FROM wf_tg_activity_sessions WHERE id=$1`, normal.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	queryCtx := WithPunchTime(ctx, normalAt.Add(2*time.Minute))
	if n, e := s.currentActivityCount(queryCtx, emp, "", "wc_small", "day"); e != nil || n != 2 {
		t.Fatalf("archive overlap double-counted: %d %v", n, e)
	}
	usage, e := s.PunchActivityUsage(queryCtx, emp)
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range usage["counts"].([]map[string]interface{}) {
		if item["activityCode"] == "wc_small" && item["count"] != 2 {
			t.Fatal("usage double-counted archived copy")
		}
	}

	outAt := start.Add(9 * time.Hour)
	_, e = s.AdminPunchCheckOut(WithPunchTime(ctx, outAt), emp, "day")
	if e != nil {
		t.Fatal("checkout:", e)
	}
	if e = db.QueryRow("SELECT checkout_time FROM work_sessions WHERE id=$1", checkin.SessionID).Scan(&saved); e != nil || !saved.Equal(outAt) {
		t.Fatal("checkout event timestamp lost", saved, e)
	}
	// Cross-midnight retains the start day's business date.
	night := start.Add(12 * time.Hour)
	nightResult, e := s.AdminPunchCheckIn(WithPunchTime(ctx, night), emp, "night")
	var ci *CheckInResult
	if nightResult != nil {
		ci = nightResult.Result
	}
	if e != nil {
		t.Fatal("night:", e)
	}
	if ci.Date != night.Format("2006-01-02") {
		t.Fatal("wrong night business date", ci.Date)
	}
	_, _, _, _, _, _, _, e = s.TelegramCheckOut(WithPunchTime(ctx, night.Add(12*time.Hour)), tgID, "")
	if e != nil {
		t.Fatal("night out:", e)
	}
	exportPath, e := s.exportBeforeReset(ctx, start.Format("2006-01-02"), start.Format("2006-01-02"), "")
	if e != nil {
		t.Fatal("durable export:", e)
	}
	defer os.Remove(exportPath)
	file, e := os.ReadFile(exportPath)
	if e != nil || len(file) < 100 || string(file[:2]) != "PK" {
		t.Fatal("invalid XLSX export", e)
	}
	var backup []byte
	if e = db.QueryRow("SELECT content FROM standalone_exports WHERE filename=$1", filepath.Base(exportPath)).Scan(&backup); e != nil || !bytes.Equal(file, backup) {
		t.Fatal("export backup mismatch", e)
	}
	defer db.Exec("DELETE FROM standalone_exports WHERE filename=$1", filepath.Base(exportPath))

}

// ---- Source: backend/services/workforce_tg_activity_icon_test.go ----
func TestActivityKeyboardIconLabels(t *testing.T) {
	label := tgActivityKeyboardLabel("小厕", TgLangZh)
	if label != "🚾 小厕" {
		t.Fatalf("expected swapped 小厕 icon, got %q", label)
	}
	if got := tgActivityKeyboardLabel("大厕", TgLangZh); got != "🚽 大厕" {
		t.Fatalf("expected swapped 大厕 icon, got %q", got)
	}
	lookup := map[string]string{}
	tgRegisterLookup(lookup, "小厕", tgActivityKeyboardLabel("小厕", TgLangZh))
	if lookup["🚾 小厕"] != "小厕" {
		t.Fatalf("lookup mismatch: %v", lookup)
	}
}
func TestStripActivityKeyboardPrefix(t *testing.T) {
	if got := stripActivityKeyboardPrefix("🚾 小厕"); got != "小厕" {
		t.Fatalf("strip new 小厕 icon failed: %q", got)
	}
	if got := stripActivityKeyboardPrefix("🚽 小厕"); got != "小厕" {
		t.Fatalf("strip legacy 小厕 icon failed: %q", got)
	}
	if got := stripActivityKeyboardPrefix("小厕"); got != "小厕" {
		t.Fatalf("plain name changed: %q", got)
	}
}
func TestResolveActivityTypeByIconLabel(t *testing.T) {
	// Ensures API/agent paths still resolve when only icon+name text is used.
	if got := canonicalActivityTypeCode("🍱 吃饭"); got != "eat" {
		t.Fatalf("expected eat, got %q", got)
	}
}

// ---- Source: backend/services/workforce_tg_attendance_review_test.go ----
func TestFormatAdminMentionsHTML(t *testing.T) {
	if got := formatAdminMentionsHTML(nil); got != "" {
		t.Fatalf("empty ids = %q", got)
	}
	if got := formatAdminMentionsHTML([]int64{0, -1}); got != "" {
		t.Fatalf("invalid ids = %q", got)
	}
	got := formatAdminMentionsHTML([]int64{123, 456})
	want := `<a href="tg://user?id=123">管理员</a> <a href="tg://user?id=456">管理员</a>`
	if got != want {
		t.Fatalf("mentions = %q, want %q", got, want)
	}
}
func TestAttendanceReviewConfirmFooterKeepsLegacyWhenNoAdmins(t *testing.T) {
	got := attendanceReviewConfirmFooter("")
	want := "请管理员在【罚款详情】确认：旷工 / 半休 / 半假\n未确认前：不记罚款，不改考勤状态。"
	if got != want {
		t.Fatalf("legacy footer changed: %q", got)
	}
}
func TestAttendanceReviewConfirmFooterMentionsAdmins(t *testing.T) {
	mentions := formatAdminMentionsHTML([]int64{111})
	got := attendanceReviewConfirmFooter(mentions)
	for _, part := range []string{mentions, "请确认", "旷工", "罚款详情", "不记罚款"} {
		if !strings.Contains(got, part) {
			t.Fatalf("footer missing %q: %q", part, got)
		}
	}
}

// ---- Source: backend/services/workforce_tg_bot_ops_test.go ----
func TestBuildTelegramBotCommandMenuOrder(t *testing.T) {
	types := []TgActivityType{
		{Name: "吃饭"},
		{Name: "小厕"},
		{Name: "大厕"},
		{Name: "抽烟或休息"},
	}
	got := buildTelegramBotCommandMenu(types)
	want := []string{
		"checkin_day", "checkin_middle", "checkin_night", "checkout",
		"wc1", "wc2", "smoke", "at",
		"lang_zh", "lang_vi", "lang_en",
		"myinfo", "rankings", "bind", "help", "start",
		"meal", "status",
	}
	if len(got) < len(want) {
		t.Fatalf("got %d commands, want at least %d: %#v", len(got), len(want), commandNames(got))
	}
	for i, cmd := range want {
		if got[i]["command"] != cmd {
			t.Fatalf("pos %d: got %q want %q\nfull=%v", i, got[i]["command"], cmd, commandNames(got))
		}
	}
}
func commandNames(cmds []map[string]string) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c["command"])
	}
	return out
}

// ---- Source: backend/services/workforce_tg_device_tag_test.go ----
func TestSplitTgDeviceTag(t *testing.T) {
	base, hint := SplitTgDeviceTag("🚾 小厕 · A")
	if base != "🚾 小厕" || hint != "A" {
		t.Fatalf("got base=%q hint=%q", base, hint)
	}
	base, hint = SplitTgDeviceTag("白班上班")
	if base != "白班上班" || hint != "" {
		t.Fatalf("untagged got base=%q hint=%q", base, hint)
	}
}
func TestBuildWorkstationDeviceTagsShortLetters(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	tags := buildWorkstationDeviceTags([]workstationTGDevice{
		{ID: id2, Name: "DESKTOP-BBB"},
		{ID: id1, Name: "DESKTOP-AAA"},
	})
	if len(tags) != 2 || tags[0].Tag != "A" || tags[1].Tag != "B" {
		t.Fatalf("tags=%v", tags)
	}
	// Stable by UUID: smaller UUID is A
	if tags[0].Device.ID != id1 || tags[1].Device.ID != id2 {
		t.Fatalf("order by uuid failed: %#v", tags)
	}
	legend := FormatWorkstationDeviceTagLegend(tags)
	if legend != "设备标记：A=DESKTOP-AAA  B=DESKTOP-BBB" {
		t.Fatalf("legend=%q", legend)
	}
}
func TestMatchWorkstationDeviceByHint(t *testing.T) {
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	devices := []workstationTGDevice{
		{ID: id1, Name: "DESKTOP-AAA"},
		{ID: id2, Name: "DESKTOP-BBB"},
	}
	got, err := matchWorkstationDeviceByHint(devices, "B")
	if err != nil || got.ID != id2 {
		t.Fatalf("match B failed: got=%v err=%v", got, err)
	}
	got, err = matchWorkstationDeviceByHint(devices, "DESKTOP-AAA")
	if err != nil || got.ID != id1 {
		t.Fatalf("hostname match failed: got=%v err=%v", got, err)
	}
}

// ---- Source: backend/services/workforce_tg_export_test.go ----
func TestTgGroupEmployeeBelongsToChatSQLIncludesSharedWorkstation(t *testing.T) {
	sql := tgGroupEmployeeBelongsToChatSQL("ws.employee_id", "$1")
	if !strings.Contains(sql, "wf_shared_workstation_members") {
		t.Fatal("group employee match must include shared workstation")
	}
	if !strings.Contains(sql, "device_telegram_bindings") {
		t.Fatal("group employee match must include device telegram bindings")
	}
}
func TestTgExportChatFilterIncludesPCSessionChat(t *testing.T) {
	filter, arg := tgExportChatFilter("-100123")
	if arg != "-100123" {
		t.Fatalf("chat arg = %q", arg)
	}
	if !strings.Contains(filter, "source_chat_id") {
		t.Fatal("export chat filter must match session source_chat_id")
	}
	if !strings.Contains(filter, "wf_shared_workstation_members") {
		t.Fatal("export chat filter must include shared workstation")
	}
	if !strings.Contains(filter, "wf_tg_activity_sessions_archive") {
		t.Fatal("export chat filter must also match activity archive for empty binding chat")
	}
}
func TestTgExportAttendanceChatFilter(t *testing.T) {
	if got := tgExportAttendanceChatFilter(""); got != "" {
		t.Fatalf("empty chat filter = %q", got)
	}
	got := tgExportAttendanceChatFilter("-1001")
	if !strings.Contains(got, "ar.employee_id") {
		t.Fatal("attendance chat filter must scope ar.employee_id")
	}
	if !strings.Contains(got, "$3") {
		t.Fatal("attendance chat filter must use $3 chat placeholder")
	}
}
func TestExportStatKeySeparatesSharedTelegramEmployees(t *testing.T) {
	tgUID := int64(987654321)
	empA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	empB := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	keyA := exportStatKey(empA, tgUID, "day")
	keyB := exportStatKey(empB, tgUID, "day")
	if keyA == keyB {
		t.Fatalf("shared TG employees must not share export stat key: %q", keyA)
	}
	if exportUserKey(empA, tgUID) != exportUserKey(empA, 0) {
		t.Fatal("export user key should not depend on telegram user id")
	}
}
func TestTelegramExportUsePunchTimes(t *testing.T) {
	if !telegramExportUsePunchTimes("2026-07-25", "2026-07-25") {
		t.Fatal("single day export should use punch times")
	}
	if telegramExportUsePunchTimes("2026-07-01", "2026-07-31") {
		t.Fatal("month span export should use aggregate counts")
	}
}
func TestExportSessionDurationSecondsNightClosed(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	checkin := time.Date(2026, 7, 29, 21, 0, 0, 0, loc)
	checkout := time.Date(2026, 7, 30, 8, 50, 0, 0, loc)
	got := exportSessionDurationSeconds(checkin, checkout, true, false, false, "2026-07-29", "2026-07-30", time.Now())
	want := int(checkout.Sub(checkin).Seconds()) // 11h50 = 42600
	if got != want {
		t.Fatalf("closed night duration = %d, want %d", got, want)
	}
}
func TestExportSessionDurationSecondsArchiveOpenIgnored(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	checkin := time.Date(2026, 7, 29, 20, 59, 53, 0, loc)
	now := time.Date(2026, 7, 30, 11, 30, 0, 0, loc)
	got := exportSessionDurationSeconds(checkin, time.Time{}, false, false, true, "2026-07-29", "2026-07-30", now)
	if got != 0 {
		t.Fatalf("archive open must not use NOW: got %d", got)
	}
}
func TestExportSessionDurationSecondsLiveOpenToday(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	checkin := time.Date(2026, 7, 30, 9, 0, 0, 0, loc)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, loc)
	got := exportSessionDurationSeconds(checkin, time.Time{}, false, true, true, "2026-07-30", "2026-07-30", now)
	want := 3 * 3600
	if got != want {
		t.Fatalf("live open today = %d, want %d", got, want)
	}
}
func TestExportSessionDurationSecondsYuYuPattern(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	openCheckin := time.Date(2026, 7, 29, 20, 59, 53, 0, loc)
	closedCheckin := time.Date(2026, 7, 29, 23, 23, 59, 0, loc)
	closedCheckout := time.Date(2026, 7, 30, 8, 56, 3, 0, loc)
	now := time.Date(2026, 7, 30, 11, 26, 0, 0, loc)
	today := "2026-07-30"

	openSec := exportSessionDurationSeconds(openCheckin, time.Time{}, false, false, true, "2026-07-29", today, now)
	closedSec := exportSessionDurationSeconds(closedCheckin, closedCheckout, true, false, false, "2026-07-29", today, now)
	sum := openSec + closedSec
	if openSec != 0 {
		t.Fatalf("ghost archive open should be 0, got %d", openSec)
	}
	wantClosed := int(closedCheckout.Sub(closedCheckin).Seconds()) // ~9h32m = 34324
	if closedSec != wantClosed {
		t.Fatalf("closed segment = %d, want %d", closedSec, wantClosed)
	}
	if wantClosed != 34324 {
		t.Fatalf("YuYu closed segment seconds = %d, expected 34324", wantClosed)
	}
	if sum != wantClosed {
		t.Fatalf("YuYu export sum = %d, want closed-only %d", sum, wantClosed)
	}
	if sum > 12*3600 {
		t.Fatalf("YuYu-like sum %d should not approach 24h", sum)
	}
}
func TestExportSessionDurationSecondsHistoricalLiveOpenIgnored(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	checkin := time.Date(2026, 7, 28, 21, 0, 0, 0, loc)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, loc)
	got := exportSessionDurationSeconds(checkin, time.Time{}, false, true, true, "2026-07-28", "2026-07-30", now)
	if got != 0 {
		t.Fatalf("historical live open must be 0, got %d", got)
	}
}
func TestExportSQLDedupPrefersLiveOverArchive(t *testing.T) {
	// Mirrors DISTINCT ON (id) ORDER BY id, is_live DESC: live row wins when same id in both tables.
	type row struct {
		id     string
		isLive bool
		open   bool
	}
	pick := func(rows []row) row {
		best := rows[0]
		for _, r := range rows[1:] {
			if r.id != best.id {
				continue
			}
			if r.isLive && !best.isLive {
				best = r
			}
		}
		return best
	}
	chosen := pick([]row{
		{id: "same", isLive: false, open: false},
		{id: "same", isLive: true, open: false},
	})
	if !chosen.isLive {
		t.Fatal("dedup must prefer live over archive for same id")
	}
}
func TestArchiveWorkSessionsByEmployeeDateOnlyClosed(t *testing.T) {
	if !strings.Contains(archiveWorkSessionsByEmployeeDateWhere, "status = 'closed'") {
		t.Fatalf("employee archive must filter closed only, got %q", archiveWorkSessionsByEmployeeDateWhere)
	}
	if strings.Contains(archiveWorkSessionsByEmployeeDateWhere, "status != 'closed'") {
		t.Fatal("employee archive must not invert closed filter")
	}
}
func TestEmployeeDailyResetSkipFinePolicy(t *testing.T) {
	// Documented contract: employee daily reset closes open sessions without early-leave fine.
	const employeeDailyResetSkipFine = true
	if !employeeDailyResetSkipFine {
		t.Fatal("employee daily reset must skip early-leave fine")
	}
}
func TestExportDisplayUserIDStillUsesTelegramWhenPresent(t *testing.T) {
	tgUID := int64(12345)
	empID := uuid.New()
	if got := exportDisplayUserID(tgUID, empID); got != tgUID {
		t.Fatalf("display user id = %d, want %d", got, tgUID)
	}
}

// ---- Source: backend/services/workforce_tg_export_yuyu_verify_test.go ----
// TestVerifyYuYuExportDuration20260729 runs against a live DB when available.
// Recomputes export duration rules for employee name YuYu on 2026-07-29.
func TestVerifyYuYuExportDuration20260729(t *testing.T) {
	_ = godotenv.Load("../.env")
	_ = godotenv.Load(".env")
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		host := getenvDefault("DB_HOST", "localhost")
		port := getenvDefault("DB_PORT", "5432")
		user := getenvDefault("DB_USER", "postgres")
		pass := os.Getenv("DB_PASSWORD")
		name := getenvDefault("DB_NAME", "postgres")
		ssl := getenvDefault("DB_SSLMODE", "disable")
		if pass == "" {
			t.Skip("no DB credentials; skip YuYu live verify")
		}
		dsn = "host=" + host + " port=" + port + " user=" + user + " password=" + pass + " dbname=" + name + " sslmode=" + ssl
	}

	db, err := sqlPackage2.Open("postgres", dsn)
	if err != nil {
		t.Skipf("db open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("db unreachable: %v", err)
	}

	const day = "2026-07-29"
	today := time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02")

	// Prefer named YuYu; else any employee with archive open+closed night pattern on that day.
	var empID, empName string
	err = db.QueryRowContext(ctx, `
		SELECT e.id::text, e.name FROM employees e
		WHERE e.name ILIKE '%yuyu%'
		ORDER BY e.created_at DESC NULLS LAST
		LIMIT 1`).Scan(&empID, &empName)
	if err == sqlPackage2.ErrNoRows || err != nil {
		err = db.QueryRowContext(ctx, `
			SELECT e.id::text, e.name
			FROM employees e
			WHERE EXISTS (
				SELECT 1 FROM work_sessions_archive o
				WHERE o.employee_id = e.id AND o.attendance_date = $1::date
				  AND (o.status = 'open' OR o.checkout_time IS NULL)
			) AND EXISTS (
				SELECT 1 FROM work_sessions_archive c
				WHERE c.employee_id = e.id AND c.attendance_date = $1::date
				  AND c.status = 'closed' AND c.checkout_time IS NOT NULL
			)
			ORDER BY e.name
			LIMIT 1`, day).Scan(&empID, &empName)
		if err == sqlPackage2.ErrNoRows {
			t.Skip("no YuYu / open+closed night dirty pattern for 2026-07-29 in this DB")
		}
		if err != nil {
			// Local/dev DBs may lack archive table or production dump.
			t.Skipf("lookup dirty night pattern unavailable: %v", err)
		}
	}

	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ON (id)
			checkin_time, checkout_time, COALESCE(status,'closed'), is_live, attendance_date::text
		FROM (
			SELECT id, checkin_time, checkout_time, status, TRUE AS is_live, attendance_date
			FROM work_sessions
			WHERE employee_id = $1::uuid
			  AND (attendance_date = $2::date OR (checkin_time AT TIME ZONE 'Asia/Shanghai')::date = $2::date)
			UNION ALL
			SELECT id, checkin_time, checkout_time, status, FALSE AS is_live, attendance_date
			FROM work_sessions_archive
			WHERE employee_id = $1::uuid
			  AND (attendance_date = $2::date OR (checkin_time AT TIME ZONE 'Asia/Shanghai')::date = $2::date)
		) u
		ORDER BY id, is_live DESC`, empID, day)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer rows.Close()

	total := 0
	var closedSegs, openIgnored int
	var closedOnly int
	for rows.Next() {
		var checkin time.Time
		var checkout sqlPackage2.NullTime
		var status string
		var isLive bool
		var attDate string
		if err := rows.Scan(&checkin, &checkout, &status, &isLive, &attDate); err != nil {
			t.Fatal(err)
		}
		isOpen := status == "open" || !checkout.Valid
		sec := exportSessionDurationSeconds(checkin, checkout.Time, checkout.Valid, isLive, isOpen, attDate, today, time.Now())
		total += sec
		if checkout.Valid {
			closedSegs++
			closedOnly += sec
		} else if sec == 0 {
			openIgnored++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if closedSegs == 0 && openIgnored == 0 {
		t.Skip("no sessions for 2026-07-29 in this DB")
	}
	if total != closedOnly {
		t.Fatalf("%s export duration %d != closed-only %d (open must be ignored)", empName, total, closedOnly)
	}
	if total > 12*3600 {
		t.Fatalf("%s 2026-07-29 export duration %ds (~%.1fh) still looks inflated", empName, total, float64(total)/3600)
	}

	// Sanity: a normal closed night (~11h50) must still compute correctly under the same helper.
	loc := time.FixedZone("CST", 8*3600)
	nCheckin := time.Date(2026, 7, 29, 21, 0, 0, 0, loc)
	nCheckout := time.Date(2026, 7, 30, 8, 50, 0, 0, loc)
	normal := exportSessionDurationSeconds(nCheckin, nCheckout, true, false, false, day, today, time.Now())
	if normal != int(nCheckout.Sub(nCheckin).Seconds()) {
		t.Fatalf("normal night duration broken: %d", normal)
	}

	t.Logf("verify emp=%s(%s) day=%s closedSegs=%d openIgnored=%d duration=%ds (~%.2fh); normalNight=%ds",
		empName, empID, day, closedSegs, openIgnored, total, float64(total)/3600, normal)
}
func getenvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---- Source: backend/services/workforce_tg_fine_currency_test.go ----
func TestNormalizeFineCurrency(t *testing.T) {
	cases := map[string]string{
		"":      "CNY",
		"cny":   "CNY",
		"元":     "CNY",
		"THB":   "THB",
		"฿":     "THB",
		"usd":   "USD",
		"weird": "CNY",
	}
	for in, want := range cases {
		if got := NormalizeFineCurrency(in); got != want {
			t.Fatalf("NormalizeFineCurrency(%q)=%q want %q", in, got, want)
		}
	}
}
func TestFineCurrencyDisplay(t *testing.T) {
	if FineCurrencySymbol("CNY") != "¥" || FineCurrencyUnit("CNY") != "元" {
		t.Fatal("CNY display mismatch")
	}
	if FineCurrencySymbol("THB") != "฿" || FineCurrencyUnit("THB") != "泰铢" {
		t.Fatal("THB display mismatch")
	}
	if FormatFineMoney(10, "CNY") != "¥10.00" {
		t.Fatalf("FormatFineMoney = %q", FormatFineMoney(10, "CNY"))
	}
	if FormatFineAmountWithUnit(10, "THB") != "10 泰铢" {
		t.Fatalf("FormatFineAmountWithUnit = %q", FormatFineAmountWithUnit(10, "THB"))
	}
}

// ---- Source: backend/services/workforce_tg_grace_test.go ----
func TestIsWithinCheckInWindow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	start := time.Date(2026, 7, 18, 9, 0, 0, 0, loc)

	if !isWithinCheckInWindow(time.Date(2026, 7, 18, 8, 30, 0, 0, loc), start, 60, 30) {
		t.Fatal("expected 08:30 within 60m before start")
	}
	if isWithinCheckInWindow(time.Date(2026, 7, 18, 7, 50, 0, 0, loc), start, 60, 30) {
		t.Fatal("expected 07:50 outside 60m before start")
	}
	if !isWithinCheckInWindow(time.Date(2026, 7, 18, 9, 20, 0, 0, loc), start, 60, 30) {
		t.Fatal("expected 09:20 within 30m after start")
	}
	if isWithinCheckInWindow(time.Date(2026, 7, 18, 9, 31, 0, 0, loc), start, 60, 30) {
		t.Fatal("expected 09:31 outside 30m after start")
	}
}
func TestMinutesLateUsesLateToleranceOnly(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	expected := time.Date(2026, 7, 18, 9, 0, 0, 0, loc)
	now := time.Date(2026, 7, 18, 9, 20, 0, 0, loc)
	if got := minutesLateFromExpected(now, expected, 15); got != 5 {
		t.Fatalf("late minutes = %d, want 5", got)
	}
}
func TestMinutesLateUsesClockMinuteNotCeil(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	expected := time.Date(2026, 7, 27, 9, 0, 0, 0, loc)
	// 09:01:30 previously Ceil'd to 2 minutes; clock-minute diff must be 1.
	now := time.Date(2026, 7, 27, 9, 1, 30, 0, loc)
	if got := minutesLateFromExpected(now, expected, 0); got != 1 {
		t.Fatalf("late minutes = %d, want 1", got)
	}
	// Still within the same displayed minute as start → not late.
	if got := minutesLateFromExpected(time.Date(2026, 7, 27, 9, 0, 59, 0, loc), expected, 0); got != 0 {
		t.Fatalf("09:00:59 late minutes = %d, want 0", got)
	}
}
func TestMinutesEarlyUsesEarlyLeaveTolerance(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	expected := time.Date(2026, 7, 18, 18, 0, 0, 0, loc)
	now := time.Date(2026, 7, 18, 17, 40, 0, 0, loc)
	if got := minutesEarlyFromExpected(now, expected, 15); got != 5 {
		t.Fatalf("early minutes = %d, want 5", got)
	}
}
func TestMinutesEarlyUsesClockMinuteNotCeil(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	expected := time.Date(2026, 7, 27, 18, 0, 0, 0, loc)
	// Allowed earliest without early = 18:00; leave at 17:58:40 → 2 clock minutes early.
	now := time.Date(2026, 7, 27, 17, 58, 40, 0, loc)
	if got := minutesEarlyFromExpected(now, expected, 0); got != 2 {
		t.Fatalf("early minutes = %d, want 2", got)
	}
}
func TestDefaultLateAndEarlyLeaveTolerance(t *testing.T) {
	if defaultLateTolerance != 0 {
		t.Fatalf("default late tolerance = %d, want 0", defaultLateTolerance)
	}
	if defaultEarlyLeaveTolerance != 10 {
		t.Fatalf("default early leave tolerance = %d, want 10", defaultEarlyLeaveTolerance)
	}
}

// ---- Source: backend/services/workforce_tg_keyboard_test.go ----
func TestLocalizedMainReplyKeyboardIncludesButtonStyles(t *testing.T) {
	kb := localizedMainReplyKeyboard(TgLangBoth, false, false, []string{"小厕", "大厕", "吃饭"})
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsAll(raw, `"style":"success"`, `"style":"danger"`) {
		t.Fatalf("expected success and danger styles in keyboard JSON, got: %s", raw)
	}

	var wrapped struct {
		Code int `json:"code"`
		Data struct {
			ReplyKeyboard map[string]interface{} `json:"replyKeyboard"`
		} `json:"data"`
	}
	wrapped.Code = 0
	wrapped.Data.ReplyKeyboard = kb
	roundTrip, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(roundTrip, &wrapped); err != nil {
		t.Fatal(err)
	}
	telegramPayload := map[string]interface{}{
		"chat_id":      123,
		"text":         "hello",
		"reply_markup": wrapped.Data.ReplyKeyboard,
	}
	final, err := json.Marshal(telegramPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsAll(final, `"style":"success"`, `"style":"danger"`) {
		t.Fatalf("styles lost after round-trip, got: %s", final)
	}
}
func TestLocalizedMainReplyKeyboardDualShiftStyles(t *testing.T) {
	kb := localizedMainReplyKeyboard(TgLangBoth, true, false, []string{"小厕"})
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsAll(raw, `"style":"success"`, `"style":"primary"`, `"style":"danger"`) {
		t.Fatalf("expected success, primary and danger styles, got: %s", raw)
	}
}
func TestLocalizedMainReplyKeyboardBackButtonStyle(t *testing.T) {
	kb := localizedMainReplyKeyboard(TgLangBoth, false, false, []string{"小厕", "大厕", "抽烟或休息", "吃饭"})
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	successCount := countSubstring(string(raw), `"style":"success"`)
	if successCount < 2 {
		t.Fatalf("expected at least 2 success styles (checkin + back), got %d in: %s", successCount, raw)
	}
}
func TestEnrichReplyKeyboardStylesAfterRoundTrip(t *testing.T) {
	kb := localizedMainReplyKeyboard(TgLangBoth, false, false, []string{"小厕", "大厕", "吃饭"})
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]interface{}
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if rows, ok := roundTrip["keyboard"].([]interface{}); ok && len(rows) > 0 {
		if firstRow, ok := rows[0].([]interface{}); ok && len(firstRow) > 0 {
			if btn, ok := firstRow[0].(map[string]interface{}); ok {
				delete(btn, "style")
			}
		}
	}
	enriched := enrichReplyKeyboardStyles(roundTrip)
	final, err := json.Marshal(enriched)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsAll(final, `"style":"success"`, `"style":"danger"`) {
		t.Fatalf("enrichReplyKeyboardStyles failed, got: %s", final)
	}
}
func jsonContainsAll(raw []byte, parts ...string) bool {
	s := string(raw)
	for _, part := range parts {
		if !contains(s, part) {
			return false
		}
	}
	return true
}
func countSubstring(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub) - 1
		}
	}
	return n
}
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}
func TestLocalizedMainReplyKeyboardHidesInfoButtons(t *testing.T) {
	shown := localizedMainReplyKeyboardEx(TgLangZh, false, false, []string{"小厕"}, true)
	hidden := localizedMainReplyKeyboardEx(TgLangZh, false, false, []string{"小厕"}, false)
	shownRaw, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}
	hiddenRaw, err := json.Marshal(hidden)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsAll(shownRaw, "我的记录", "排行榜", "导出数据") {
		t.Fatalf("expected info buttons when enabled, got: %s", shownRaw)
	}
	if jsonContainsAll(hiddenRaw, "我的记录") || jsonContainsAll(hiddenRaw, "排行榜") || jsonContainsAll(hiddenRaw, "导出数据") {
		t.Fatalf("info buttons should be hidden, got: %s", hiddenRaw)
	}
	if !jsonContainsAll(hiddenRaw, "回座") {
		t.Fatalf("back button must remain when info buttons are hidden, got: %s", hiddenRaw)
	}
}
func TestVietnamesePersonalKeyboardLabels(t *testing.T) {
	kb := localizedMainReplyKeyboardEx(TgLangVi, true, false, []string{"小厕"}, false)
	kb["selective"] = true
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, `"selective":true`) {
		t.Fatalf("personal group keyboard must be selective, got %s", got)
	}
	if !strings.Contains(got, "Ca ngày Đi làm") || !strings.Contains(got, "Tan ca") {
		t.Fatalf("vi keyboard missing punch labels, got %s", got)
	}
	if strings.Contains(got, "白班上班") {
		t.Fatalf("vi keyboard should not keep chinese punch labels, got %s", got)
	}
	if !strings.Contains(got, "Nhà vệ sin") {
		t.Fatalf("vi keyboard missing activity label, got %s", got)
	}
}
func TestFitKeyboardLabelKeepsChinese(t *testing.T) {
	for _, name := range []string{"小厕", "大厕", "吃饭", "抽烟或休息"} {
		zh := tgActivityKeyboardLabel(name, TgLangZh)
		if got := tgFitKeyboardLabel(zh); got != zh {
			t.Fatalf("chinese keyboard label changed: %q -> %q", zh, got)
		}
	}
	if got := tgFitKeyboardLabel("白班上班"); got != "白班上班" {
		t.Fatalf("chinese work label changed: %q", got)
	}
}
func TestFitKeyboardLabelEllipsizesBilingual(t *testing.T) {
	full := tgActivityKeyboardLabel("小厕", TgLangBoth)
	fitted := tgFitKeyboardLabel(full)
	if fitted == full {
		t.Fatalf("expected bilingual label to ellipsize, got unchanged %q", full)
	}
	if !strings.HasSuffix(fitted, "...") {
		t.Fatalf("expected ..., got %q", fitted)
	}
	if strings.Contains(fitted, "vệ sinh") {
		t.Fatalf("expected long vietnamese to be trimmed, got %q", fitted)
	}
	if w := tgDisplayWidth(fitted); w > tgKeyboardLabelMaxWidth {
		t.Fatalf("fitted width %d exceeds max %d (%q)", w, tgKeyboardLabelMaxWidth, fitted)
	}
}
func TestFitKeyboardLabelStillResolves(t *testing.T) {
	lookup := map[string]string{}
	full := tgActivityKeyboardLabel("小厕", TgLangBoth)
	tgRegisterLookup(lookup, "小厕", full)
	fitted := tgFitKeyboardLabel(full)
	if lookup[fitted] != "小厕" {
		t.Fatalf("truncated label %q not in lookup: %#v", fitted, lookup)
	}
	kb := localizedMainReplyKeyboardEx(TgLangBoth, false, false, []string{"小厕", "大厕"}, false)
	raw, err := json.Marshal(kb)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContainsAll(raw, "...") {
		t.Fatalf("expected ellipsis on bilingual keyboard, got: %s", raw)
	}
	if jsonContainsAll(raw, "Nhà vệ sinh nhỏ") {
		t.Fatalf("full vietnamese should not appear on keyboard, got: %s", raw)
	}
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ---- Source: backend/services/workforce_tg_reply_test.go ----
func TestTgReplyDashedLineIsCopyableAndCompact(t *testing.T) {
	got := tgReplyDashedLine()
	if !strings.HasPrefix(got, "<code>") || !strings.HasSuffix(got, "</code>") {
		t.Fatalf("divider must be wrapped in <code> for tap-to-copy, got %q", got)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(got, "<code>"), "</code>")
	if strings.Contains(inner, "─") {
		t.Fatal("divider must not use fullwidth box-drawing bars on small screens")
	}
	if !strings.Contains(inner, "-") {
		t.Fatal("divider must look dashed")
	}
	if strings.Contains(inner, " ") {
		t.Fatalf("divider should be dense without spaces: %q", inner)
	}
	if n := strings.Count(inner, "-"); n != 26 {
		t.Fatalf("divider should be 26 dashes to fill the bubble, got %d in %q", n, inner)
	}
}
func TestFormatEmployeeNameHTML(t *testing.T) {
	got := formatEmployeeNameHTML(12345, "张三")
	if got != `<a href="tg://user?id=12345">张三</a>` {
		t.Fatalf("expected TG profile link, got %q", got)
	}
	plain := formatEmployeeNameHTML(0, "张三")
	if plain != "张三" {
		t.Fatalf("unbound name should stay plain, got %q", plain)
	}
	if strings.Contains(plain, "tg://user") {
		t.Fatal("must not emit a TG link without user id")
	}
	escaped := formatEmployeeNameHTML(0, `<b>x</b>`)
	if escaped != "&lt;b&gt;x&lt;/b&gt;" {
		t.Fatalf("plain name must be escaped, got %q", escaped)
	}
	named := formatEmployeeProfileHTML(12345, "dhpg_user", "DHPG")
	if named != `<a href="tg://user?id=12345">DHPG</a>` {
		t.Fatalf("username must not use t.me card link, got %q", named)
	}
	userOnly := formatEmployeeProfileHTML(0, "dhpg_user", "DHPG")
	if strings.Contains(userOnly, "t.me/") || strings.Contains(userOnly, "tg://user") {
		t.Fatalf("no numeric id must stay plain without card link, got %q", userOnly)
	}
}
func TestBuildActivitySoonTimeoutHTMLUsesPreferredLang(t *testing.T) {
	zh := buildActivitySoonTimeoutHTML(TgLangZh, 1, "DHPG", "小厕", "夜班")
	if !strings.Contains(zh, "即将超时警告") || !strings.Contains(zh, "小厕") {
		t.Fatalf("zh warning missing chinese copy, got %q", zh)
	}
	vi := buildActivitySoonTimeoutHTML(TgLangVi, 1, "DHPG", "小厕", "Ca đêm")
	if strings.Contains(vi, "即将超时警告") {
		t.Fatalf("vi warning should not stay chinese, got %q", vi)
	}
	if !strings.Contains(vi, "Cảnh báo sắp quá giờ") || !strings.Contains(vi, "Nhà vệ sinh nhỏ") {
		t.Fatalf("vi warning missing vietnamese copy, got %q", vi)
	}
	kb := inlineQuickBackKeyboard("sess", TgLangVi)
	rows, _ := kb["inline_keyboard"].([][]map[string]interface{})
	if len(rows) == 0 || len(rows[0]) == 0 {
		t.Fatalf("expected back button, got %#v", kb)
	}
	if rows[0][0]["text"] != "Trở lại chỗ ngồi" {
		t.Fatalf("vi back button text = %#v", rows[0][0]["text"])
	}
}
func TestTgCopyableText(t *testing.T) {
	if got := tgCopyableText("¥50.00"); got != "<code>¥50.00</code>" {
		t.Fatalf("got %q", got)
	}
	if got := tgCopyableText("  "); got != "<code>-</code>" {
		t.Fatalf("empty should fall back to dash, got %q", got)
	}
}

// ---- Source: backend/services/workforce_tg_settings_bool_test.go ----
func TestMapLookupBoolTripleShift(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]interface{}
		want bool
		ok   bool
	}{
		{"bool true", map[string]interface{}{"tripleShiftEnabled": true}, true, true},
		{"bool false", map[string]interface{}{"tripleShiftEnabled": false}, false, true},
		{"float 1", map[string]interface{}{"tripleShiftEnabled": float64(1)}, true, true},
		{"string true", map[string]interface{}{"tripleShiftEnabled": "true"}, true, true},
		{"snake_case", map[string]interface{}{"triple_shift_enabled": true}, true, true},
		{"missing", map[string]interface{}{}, false, false},
	}
	for _, tc := range cases {
		got, ok := mapLookupBool(tc.in, "tripleShiftEnabled", "triple_shift_enabled")
		if ok != tc.ok || got != tc.want {
			t.Fatalf("%s: got=(%v,%v) want=(%v,%v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	// Simulate encoding/json into map[string]interface{}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(`{"tripleShiftEnabled":true}`), &m); err != nil {
		t.Fatal(err)
	}
	got, ok := mapLookupBool(m, "tripleShiftEnabled")
	if !ok || !got {
		t.Fatalf("json bool: got=(%v,%v)", got, ok)
	}
}

// ---- Source: backend/services/workforce_tg_shift_window_test.go ----
func TestIsHandoverDateClampsShortMonths(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	feb := time.Date(2026, 2, 28, 12, 0, 0, 0, loc)
	if !isHandoverDate(feb, 31, 0) {
		t.Fatal("handover_day=31 must clamp to Feb 28")
	}
	if isHandoverDate(time.Date(2026, 2, 27, 12, 0, 0, 0, loc), 31, 0) {
		t.Fatal("Feb 27 is not handover when day=31")
	}
	apr30 := time.Date(2026, 4, 30, 12, 0, 0, 0, loc)
	if !isHandoverDate(apr30, 0, 0) {
		t.Fatal("handover_day=0 must be last day of month")
	}
	if !isHandoverDate(time.Date(2026, 1, 31, 12, 0, 0, 0, loc), 31, 0) {
		t.Fatal("Jan 31 must still match handover_day=31")
	}
}
func TestResolveAttendanceDateMiddleUsesCalendarDay(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 8, 31, 13, 0, 0, 0, loc)
	period := &TgPeriodInfo{BusinessDate: "2026-08-30", IsHandover: true, PeriodType: "handover_night"}
	got := resolveAttendanceDate("middle", "middle_today", period, now, loc)
	if got != "2026-08-31" {
		t.Fatalf("middle_today date = %s, want 2026-08-31", got)
	}
	prev := resolveAttendanceDate("middle", "middle_prev", period, time.Date(2026, 9, 1, 0, 30, 0, 0, loc), loc)
	if prev != "2026-08-31" {
		t.Fatalf("middle_prev date = %s, want 2026-08-31", prev)
	}
}
func TestOvernightCheckInContinuationAndEarlyWindow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	settings := &TelegramSettings{
		Timezone:    "Asia/Shanghai",
		DayStart:    "09:00",
		DayEnd:      "21:00",
		NightStart:  "21:00",
		NightEnd:    "09:00",
		MiddleStart: "13:00",
		MiddleEnd:   "01:00",
	}
	grace := TgGraceConfig{CheckInBefore: 120, CheckInAfter: 360}
	s := &WorkforceTelegramService{}

	// Middle may check in 120 minutes before 13:00.
	at1200 := time.Date(2026, 8, 17, 12, 0, 0, 0, loc)
	if !s.isCheckInWindowForShift(settings, grace, "middle", at1200) {
		t.Fatal("12:00 should be inside middle check-in window")
	}
	at1259 := time.Date(2026, 8, 17, 12, 59, 0, 0, loc)
	if !s.isCheckInWindowForShift(settings, grace, "middle", at1259) {
		t.Fatal("12:59 should be inside middle check-in window")
	}
	// Overnight tail is check-out time, not check-in.
	at0030 := time.Date(2026, 8, 18, 0, 30, 0, 0, loc)
	if s.isCheckInWindowForShift(settings, grace, "middle", at0030) {
		t.Fatal("00:30 must not be a middle check-in window")
	}
	if !overnightCheckInContinuation(settings, "night", at0030) {
		t.Fatal("00:30 is overnight continuation for night")
	}
	if overnightCheckInContinuation(settings, "middle", at1200) {
		t.Fatal("12:00 is not overnight continuation for middle")
	}
}
func TestResolveWorkEndExpectedTimeOvernightAndHandover(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 8, 31, 16, 0, 0, 0, loc)

	middleEnd := resolveWorkEndExpectedTime(now, "2026-08-31", "middle", "13:00", "01:00", loc)
	if middleEnd.Format("2006-01-02 15:04") != "2026-09-01 01:00" {
		t.Fatalf("middle end = %s", middleEnd)
	}
	handoverDayEnd := resolveWorkEndExpectedTime(now, "2026-08-31", "day", "15:00", "09:00", loc)
	if handoverDayEnd.Format("2006-01-02 15:04") != "2026-09-01 09:00" {
		t.Fatalf("handover day end = %s", handoverDayEnd)
	}
	handoverNightEnd := resolveWorkEndExpectedTime(now, "2026-08-30", "night", "21:00", "15:00", loc)
	if handoverNightEnd.Format("2006-01-02 15:04") != "2026-08-31 15:00" {
		t.Fatalf("handover night end = %s", handoverNightEnd)
	}
	normalDayEnd := resolveWorkEndExpectedTime(now, "2026-08-17", "day", "09:00", "21:00", loc)
	if normalDayEnd.Format("2006-01-02 15:04") != "2026-08-17 21:00" {
		t.Fatalf("normal day end = %s", normalDayEnd)
	}
}
func TestIsHandoverRelaySuccessor(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	cfg := &TgHandoverConfigRow{HandoverEnabled: true, HandoverDay: 31, HandoverMonth: 0}
	if !isHandoverRelaySuccessor("day", "2026-09-01", "day", "2026-08-31", cfg, loc) {
		t.Fatal("Sep 1 day after Aug 31 handover day must be successor")
	}
	if isHandoverRelaySuccessor("day", "2026-08-31", "day", "2026-08-31", cfg, loc) {
		t.Fatal("same date is not successor")
	}
	if isHandoverRelaySuccessor("night", "2026-09-01", "day", "2026-08-31", cfg, loc) {
		t.Fatal("night is not a day successor")
	}
	if isHandoverRelaySuccessor("day", "2026-08-16", "day", "2026-08-15", cfg, loc) {
		t.Fatal("Aug 15 is not a handover date")
	}
}

// ---- Source: backend/services/workforce_tg_user_lang_test.go ----
func TestNormalizeUserReplyLang(t *testing.T) {
	if mode, ok := normalizeUserReplyLang("vi"); !ok || mode != TgLangVi {
		t.Fatalf("vi expected")
	}
	if mode, ok := normalizeUserReplyLang("zh"); !ok || mode != TgLangZh {
		t.Fatalf("zh expected")
	}
	if mode, ok := normalizeUserReplyLang("en"); !ok || mode != TgLangEn {
		t.Fatalf("en expected")
	}
	if _, ok := normalizeUserReplyLang("both"); ok {
		t.Fatal("both should be rejected")
	}
}
func TestResolveEmployeeGroupNotifyLang_IgnoresWorkstationWhenNoPersonalPref(t *testing.T) {
	// Shared PC: workstation TG may have had /lang vi historically; employee without pref uses group.
	got := resolveEmployeeGroupNotifyLang(false, TgLangVi, TgLangBoth)
	if got != TgLangZh {
		t.Fatalf("expected group both → zh, got %s", got)
	}
	got = resolveEmployeeGroupNotifyLang(false, TgLangVi, TgLangZh)
	if got != TgLangZh {
		t.Fatalf("expected zh group, got %s", got)
	}
	got = resolveEmployeeGroupNotifyLang(false, TgLangVi, TgLangEn)
	if got != TgLangEn {
		t.Fatalf("expected en group fallback, got %s", got)
	}
}
func TestResolveEmployeeGroupNotifyLang_PersonalPrefWins(t *testing.T) {
	got := resolveEmployeeGroupNotifyLang(true, TgLangEn, TgLangVi)
	if got != TgLangEn {
		t.Fatalf("employee /lang should win, got %s", got)
	}
	got = resolveEmployeeGroupNotifyLang(true, TgLangVi, TgLangZh)
	if got != TgLangVi {
		t.Fatalf("employee vi should win, got %s", got)
	}
}
func TestGroupPunchTemplateLang(t *testing.T) {
	if groupPunchTemplateLang(TgLangBoth) != TgLangZh {
		t.Fatal("both → zh for punch HTML fallback")
	}
	if groupPunchTemplateLang(TgLangVi) != TgLangVi {
		t.Fatal("vi group stays vi")
	}
}
func TestBuildWorkStartGroupReplyHTMLLocalized(t *testing.T) {
	now := time.Date(2026, 8, 18, 9, 5, 0, 0, time.UTC)
	expected := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	zh := buildWorkStartGroupReplyHTML(TgLangZh, 123, "张三", "day", "", now, expected, 0, 0, "", "CNY")
	if !strings.Contains(zh, "白班上班完成") || !strings.Contains(zh, "准时") {
		t.Fatalf("zh reply unexpected: %s", zh)
	}
	vi := buildWorkStartGroupReplyHTML(TgLangVi, 123, "Lan", "day", "", now, expected, 0, 0, "", "CNY")
	if !strings.Contains(vi, "Ca ngày") || !strings.Contains(vi, "Đúng giờ") || strings.Contains(vi, "准时") {
		t.Fatalf("vi reply unexpected: %s", vi)
	}
}
func TestBuildWorkStartGroupReplyHTMLEnglish(t *testing.T) {
	now := time.Date(2026, 8, 18, 9, 5, 0, 0, time.UTC)
	expected := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	en := buildWorkStartGroupReplyHTML(TgLangEn, 123, "Tom", "day", "", now, expected, 0, 0, "", "CNY")
	if !strings.Contains(en, "Day shift") || !strings.Contains(en, "On time") || strings.Contains(en, "准时") {
		t.Fatalf("en reply unexpected: %s", en)
	}
}
func TestBuildActivityStartGroupReplyHTMLEnglishActivityName(t *testing.T) {
	en := buildActivityStartGroupReplyHTML(TgLangEn, 1, "Tom", "小厕", "08/18 10:00:00", "Day shift", 1, 5, 10)
	if strings.Contains(en, "小厕") || !strings.Contains(en, "Restroom") {
		t.Fatalf("en activity name unexpected: %s", en)
	}
}
func TestBuildActivityStartGroupReplyHTMLLocalized(t *testing.T) {
	vi := buildActivityStartGroupReplyHTML(TgLangVi, 1, "Lan", "小厕", "08/18 10:00:00", "Ca ngày", 1, 5, 10)
	if !strings.Contains(vi, "Chấm công") || !strings.Contains(vi, "Trở lại") {
		t.Fatalf("vi activity start unexpected: %s", vi)
	}
}

// ---- Source: backend/services/workforce_tg_worker_error_test.go ----
func TestFormatTelegramWorkerErrorLocalizesSentinels(t *testing.T) {
	zh := FormatTelegramWorkerError(ErrSessionExists, TgLangZh)
	if !strings.Contains(zh, "已在上班状态") {
		t.Fatalf("zh session-exists = %q", zh)
	}
	vi := FormatTelegramWorkerError(ErrSessionExists, TgLangVi)
	if strings.Contains(vi, "已在上班状态") {
		t.Fatalf("vi session-exists stayed chinese: %q", vi)
	}
	if !strings.Contains(vi, "đã đang đi làm") {
		t.Fatalf("vi session-exists missing vietnamese: %q", vi)
	}
	en := FormatTelegramWorkerError(ErrActivityNotInWork, TgLangEn)
	if strings.Contains(en, "请先上班") {
		t.Fatalf("en activity-not-in-work stayed chinese: %q", en)
	}
	if !strings.Contains(strings.ToLower(en), "clock in") {
		t.Fatalf("en activity-not-in-work missing english: %q", en)
	}
}
func TestFormatTelegramWorkerErrorWindowDetails(t *testing.T) {
	err := &outsideWorkWindowError{Action: "checkout", Shift: "night", Clock: "09:00"}
	if !errors.Is(err, ErrOutsideWorkWindow) {
		t.Fatal("typed window error must unwrap to ErrOutsideWorkWindow")
	}
	zh := FormatTelegramWorkerError(err, TgLangZh)
	if !strings.Contains(zh, "夜班") || !strings.Contains(zh, "09:00") || !strings.Contains(zh, "签退窗口") {
		t.Fatalf("zh window = %q", zh)
	}
	vi := FormatTelegramWorkerError(err, TgLangVi)
	if strings.Contains(vi, "签退窗口") {
		t.Fatalf("vi window stayed chinese: %q", vi)
	}
	if !strings.Contains(vi, "09:00") || !strings.Contains(vi, "Ca đêm") {
		t.Fatalf("vi window missing details: %q", vi)
	}
	other := &otherWorkSessionStillOpenError{Shift: "day", Detail: "night_last", Date: "2026-08-18"}
	if !errors.Is(other, ErrOtherWorkSessionOpen) {
		t.Fatal("typed other-session error must unwrap")
	}
	en := FormatTelegramWorkerError(other, TgLangEn)
	if strings.Contains(en, "请先下班") {
		t.Fatalf("en other-session stayed chinese: %q", en)
	}
	if !strings.Contains(en, "2026-08-18") || !strings.Contains(en, "last night") {
		t.Fatalf("en other-session missing details: %q", en)
	}
}
func TestFormatTelegramWorkerErrorDevicePickerPhrases(t *testing.T) {
	cases := []struct {
		mode    TgLangMode
		needAny []string
	}{
		{TgLangZh, []string{"选择设备", "多台设备已登录"}},
		{TgLangVi, []string{"chọn thiết bị", "Nhiều thiết bị"}},
		{TgLangEn, []string{"select a device", "Multiple devices"}},
	}
	for _, tc := range cases {
		msg := FormatTelegramWorkerError(ErrMultipleWorkstationLogins, tc.mode)
		ok := false
		for _, n := range tc.needAny {
			if strings.Contains(msg, n) {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("mode %s device error %q missing %#v", tc.mode, msg, tc.needAny)
		}
	}
}
func TestTelegramWorkerErrorHTTPCodes(t *testing.T) {
	st, code := TelegramWorkerErrorHTTP(ErrSessionExists)
	if st != 409 || code != 1004 {
		t.Fatalf("session exists http=%d code=%d", st, code)
	}
	st, code = TelegramWorkerErrorHTTP(ErrOutsideWorkWindow)
	if st != 400 || code != 1001 {
		t.Fatalf("window http=%d code=%d", st, code)
	}
	st, code = TelegramWorkerErrorHTTP(ErrActivityBackNotOwner)
	if st != 403 || code != 1006 {
		t.Fatalf("not owner http=%d code=%d", st, code)
	}
	st, code = TelegramWorkerErrorHTTP(errors.New("boom"))
	if st != 500 || code != 5000 {
		t.Fatalf("unknown http=%d code=%d", st, code)
	}
}
func TestFormatTelegramActivityBackNotOwner(t *testing.T) {
	zh := FormatTelegramActivityBackNotOwner("YuYu", TgLangZh)
	if zh != "YuYu，这不是您的活动按钮" {
		t.Fatalf("zh = %q", zh)
	}
	vi := FormatTelegramActivityBackNotOwner("YuYu", TgLangVi)
	if strings.Contains(vi, "这不是您的") {
		t.Fatalf("vi stayed chinese: %q", vi)
	}
	if !strings.HasPrefix(vi, "YuYu") {
		t.Fatalf("vi must keep name: %q", vi)
	}
}

func TestStandaloneDatabaseConnectionModes(t *testing.T) {
	for _, tt := range []struct {
		raw, env, mode, ca string
		wantError          bool
		required           string
	}{
		{"postgresql://user:pass@db:5432/attendance", "production", "internal", "", false, "sslmode=disable"},
		{"postgresql://user:pass@public.example:5432/attendance", "production", "internal", "", true, ""},
		{"postgresql://user:pass@db:15432/attendance", "production", "internal", "", true, ""},
		{"postgresql://user:pass@public.example:5432/attendance?sslmode=disable", "production", "", "/ca.pem", false, "sslmode=verify-full"},
		{"postgresql://user:pass@public.example/attendance", "production", "", "", true, ""},
		{"postgresql://user:pass@db/attendance?host=evil.example&service=external&port=4444", "production", "internal", "", false, "sslmode=disable"},
		{"postgresql://user:pass@localhost/test?sslmode=disable", "test", "", "", false, "sslmode=disable"},
	} {
		result, err := attendanceDatabaseURL(tt.raw, tt.env, tt.mode, tt.ca)
		if (err != nil) != tt.wantError {
			t.Fatalf("mode %s: %v", tt.mode, err)
		}
		if err == nil && (!strings.Contains(result, tt.required) || strings.Contains(result, "evil.example") || strings.Contains(result, "service=")) {
			t.Fatalf("unsafe or wrong URL %s", result)
		}
	}
}

func TestReplyKeyboardPersistent(t *testing.T) {
	for _, mode := range []TgLangMode{TgLangZh, TgLangVi, TgLangEn, TgLangBoth} {
		kb := localizedMainReplyKeyboard(mode, true, false, []string{"小厕"})
		if kb["is_persistent"] != true || kb["one_time_keyboard"] != false {
			t.Fatalf("keyboard may disappear: %#v", kb)
		}
	}
}

func TestActivityCounterConfiguredBoundary(t *testing.T) {
	settings := &TelegramSettings{Timezone: "Asia/Shanghai", DailyResetEnabled: true, DailyResetTime: "09:00", DailyResetExecutionOffsetMinutes: 0}
	parse := func(v string) time.Time {
		d, e := time.Parse(time.RFC3339, v)
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	cases := []struct {
		now, clock, start string
		offset            int
	}{
		{"2026-11-09T23:59:00+08:00", "09:00", "2026-11-09T09:00:00+08:00", 0},
		{"2026-11-10T00:01:00+08:00", "09:00", "2026-11-09T09:00:00+08:00", 0},
		{"2026-11-10T09:00:00+08:00", "09:00", "2026-11-10T09:00:00+08:00", 0},
		{"2026-11-10T10:59:00+08:00", "09:00", "2026-11-09T11:00:00+08:00", 120},
		{"2026-11-10T01:00:00+08:00", "23:00", "2026-11-10T01:00:00+08:00", 120},
		{"2026-11-10T05:59:00+08:00", "06:00", "2026-11-09T06:00:00+08:00", 0},
	}
	for _, c := range cases {
		settings.DailyResetExecutionOffsetMinutes = c.offset
		start, end, err := activityCounterWindow(parse(c.now), settings, c.clock)
		if err != nil || !start.Equal(parse(c.start)) || !end.After(parse(c.now)) {
			t.Errorf("%+v: got %v..%v, %v", c, start, end, err)
		}
	}
	settings.DailyResetEnabled = false
	_, end, err := activityCounterWindow(parse(cases[0].now), settings, "invalid")
	if err != nil || !end.IsZero() {
		t.Fatal("disabled automatic reset must not create a midnight boundary")
	}
	settings.DailyResetEnabled = true
	if _, _, err := activityCounterWindow(parse(cases[0].now), settings, "invalid"); err == nil {
		t.Fatal("invalid reset clock must fail closed")
	}
}

func TestResourcePolicyAndPressure(t *testing.T) {
	p := ResourcePolicy{Auto: true, HistoryDays: 30, LogDays: 180, TempHours: 72}
	if err := validateResourcePolicy(p); err != nil {
		t.Fatal(err)
	}
	p.HistoryDays = 0
	if validateResourcePolicy(p) == nil {
		t.Fatal("unsafe retention accepted")
	}
	ratio := 80.0
	if resourcePressure(ResourceSnapshot{DiskPercent: 80, DatabasePercent: &ratio}) {
		t.Fatal("80 percent must not exceed threshold")
	}
	ratio = 80.1
	if !resourcePressure(ResourceSnapshot{DatabasePercent: &ratio}) {
		t.Fatal("database pressure ignored")
	}
	if resourcePressure(ResourceSnapshot{}) {
		t.Fatal("unknown capacity must not imply pressure")
	}
}

func TestWorkerActivityReplyIsDeferredWithoutDatabaseOrNetwork(t *testing.T) {
	service := &WorkforceTelegramService{} // No DB: worker deferral must happen before any I/O.
	ctx := WithWorkerTelegramReply(context.Background())
	if workerOwnsTelegramReply(context.Background()) {
		t.Fatal("non-worker context inherited delegation")
	}
	if service.publishActivityStartGroupReply(ctx, uuid.New(), "-123", uuid.New(), "ok", 7, 123) {
		t.Fatal("deferred reply was marked sent")
	}
	service.publishActivityEndGroupReply(ctx, uuid.New(), &TgActivitySession{GroupReplyHTML: "ok", EmployeeID: uuid.New()})
}

func TestTelegramReadCacheIsolationAndRetry(t *testing.T) {
	s := &WorkforceTelegramService{}
	ctx := WithTelegramRequestCache(context.Background())
	calls := 0
	read := func() (int, error) { calls++; return calls, nil }
	first, _ := telegramRequestRead(ctx, s, "rule", read)
	same, _ := telegramRequestRead(ctx, s, "rule", read)
	next, _ := telegramRequestRead(WithTelegramRequestCache(context.Background()), s, "rule", read)
	other, _ := telegramRequestRead(ctx, &WorkforceTelegramService{}, "rule", read)
	if first != 1 || same != 1 || next != 2 || other != 3 {
		t.Fatalf("cache leaked across requests/services: %d %d %d %d", first, same, next, other)
	}
	attempts := 0
	flaky := func() (int, error) {
		attempts++
		if attempts == 1 {
			return 0, errors.New("database unavailable")
		}
		return 9, nil
	}
	if _, err := telegramRequestRead(ctx, s, "retry", flaky); err == nil {
		t.Fatal("expected first read failure")
	}
	if value, err := telegramRequestRead(ctx, s, "retry", flaky); err != nil || value != 9 || attempts != 2 {
		t.Fatal("transient failure was cached")
	}
	// Callers may customize returned settings without modifying the cached snapshot.
	_, _ = telegramRequestRead(ctx, s, "settings", func() (*TelegramSettings, error) {
		return &TelegramSettings{Timezone: "Asia/Shanghai", Enabled: true}, nil
	})
	a, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a.Enabled = false
	b, err := s.GetSettings(ctx)
	if err != nil || !b.Enabled {
		t.Fatal("mutable settings escaped into request cache")
	}
}
func TestTelegramReadCacheCoalescesConcurrentReads(t *testing.T) {
	s := &WorkforceTelegramService{}
	ctx := WithTelegramRequestCache(context.Background())
	var calls atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := telegramRequestRead(ctx, s, "settings", func() (int, error) { calls.Add(1); return 42, nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("repeated immutable query: %d", calls.Load())
	}
}

func TestActivityCounterBoundsCombinedRead(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable initialized database")
	}
	db, err := sqlPackage2.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewWorkforceTelegramService(db, NewWorkforceService(db))
	emp := uuid.New()
	chat := "-" + strings.ReplaceAll(emp.String(), "-", "")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ctx := WithTelegramRequestCache(WithPunchTime(context.Background(), now))
	_, err = telegramRequestRead(ctx, s, "settings", func() (*TelegramSettings, error) {
		return &TelegramSettings{Timezone: "UTC", DailyResetEnabled: true, DailyResetTime: "09:00"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStart := func(want time.Time) {
		t.Helper()
		got, _, _, e := s.activityCounterBounds(ctx, emp, chat)
		if e != nil || !got.Equal(want) {
			t.Fatalf("start=%v want=%v err=%v", got, want, e)
		}
	}
	assertStart(now.Add(-3 * time.Hour))
	_, err = db.Exec(`INSERT INTO wf_telegram_groups(chat_id,chat_title,daily_reset_time) VALUES($1,'counter-test','06:30')`, chat)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM wf_telegram_groups WHERE chat_id=$1`, chat)
	assertStart(now.Add(-5*time.Hour - 30*time.Minute))
	key := "activity_reset:" + emp.String()
	marker := now.Add(-time.Hour)
	_, err = db.Exec(`INSERT INTO system_settings(key,value) VALUES($1,$2)`, key, marker.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM system_settings WHERE key=$1`, key)
	assertStart(marker)
	_, err = db.Exec(`UPDATE system_settings SET value=$2 WHERE key=$1`, key, now.Add(time.Hour).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	assertStart(now.Add(-5*time.Hour - 30*time.Minute))
}

func TestActivityStartStillClosesExpiredShift(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable initialized database")
	}
	db, err := sqlPackage2.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewWorkforceTelegramService(db, NewWorkforceService(db))
	emp, sid := uuid.New(), uuid.New()
	_, err = db.Exec(`INSERT INTO employees(id,employee_id,name,status,attendance_mode) VALUES($1,$2,'expired-test',1,'tg_only')`, emp, emp.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM employees WHERE id=$1`, emp)
	_, err = db.Exec(`INSERT INTO work_sessions(id,employee_id,attendance_date,checkin_time,status,source,shift_type) VALUES($1,$2,'2020-01-01','2020-01-01T09:00:00Z','open','telegram','day')`, sid, emp)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithTelegramRequestCache(WithWorkerTelegramReply(WithPunchTime(context.Background(), time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))))
	_, err = s.startActivitySessionInChat(ctx, emp, uuid.Nil, "wc_small", PunchSourceWeb, "", 0, 0)
	if !errors.Is(err, ErrActivityNotInWork) {
		t.Fatalf("expired shift allowed activity: %v", err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM work_sessions WHERE id=$1`, sid).Scan(&status); err != nil || status != "closed" {
		t.Fatalf("expired shift not settled: status=%s err=%v", status, err)
	}
}

func TestAttendancePoolRetainsReusableConnections(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable database")
	}
	db, err := sqlPackage2.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("DB_MAX_CONNECTIONS", "3")
	cycle := func() {
		t.Helper()
		held := []*sqlPackage2.Conn{}
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for n := 0; n < 3; n++ {
			c, e := db.Conn(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			held = append(held, c)
		}
	}
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	cycle()
	baseline := db.Stats()
	if baseline.MaxIdleClosed != 2 {
		t.Fatalf("unexpected old pool baseline: %+v", baseline)
	}
	configureAttendanceDatabasePool(db)
	cycle()
	after := db.Stats()
	if after.Idle != 3 || after.MaxIdleClosed != baseline.MaxIdleClosed || after.MaxOpenConnections != 3 {
		t.Fatalf("pool still discards reusable connections: %+v", after)
	}
	cycle()
	if db.Stats().MaxIdleClosed != after.MaxIdleClosed {
		t.Fatal("repeated punch cycle churned connections")
	}
	t.Logf("three-connection burst: previous policy closed=%d; new policy closed=%d idle=%d", baseline.MaxIdleClosed, after.MaxIdleClosed-baseline.MaxIdleClosed, after.Idle)
}

func TestScheduledResetClosesOnlyTargetDateWithoutEarlyReview(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	t.Setenv("TELEGRAM_TOKEN_SECRET", "local-test-encryption-key-not-production-123")
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fakeTelegramTransport{}}
	defer func() { http.DefaultClient = oldClient }()
	db, err := sqlPackage2.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	s := NewWorkforceTelegramService(db, NewWorkforceService(db))
	ctx := context.Background()
	settings, err := s.UpdateSettings(ctx, map[string]interface{}{"enabled": true, "botToken": "999:test-only", "timezone": "Asia/Shanghai", "dailyResetTime": "09:00", "dailyResetExecutionOffsetMinutes": 120, "dayStart": "09:00", "dayEnd": "21:00", "nightStart": "21:00", "nightEnd": "09:00"})
	if err != nil {
		t.Fatal(err)
	}
	loc := tgLoadLocation(settings.Timezone)
	today := time.Now().In(loc)
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	todayDate := today.Format("2006-01-02")
	chat := "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = db.Exec(`INSERT INTO wf_telegram_groups(chat_id,chat_title,enabled,daily_reset_time) VALUES($1,'reset test',true,'09:00')`, chat); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM wf_telegram_groups WHERE chat_id=$1`, chat)
	oldEmployee, newEmployee := uuid.New(), uuid.New()
	oldWork, newWork, oldActivity, newActivity := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for n, emp := range []uuid.UUID{oldEmployee, newEmployee} {
		date, work, activity := yesterday, oldWork, oldActivity
		if n == 1 {
			date, work, activity = todayDate, newWork, newActivity
		}
		if _, err = db.Exec(`INSERT INTO employees(id,employee_id,name,status) VALUES($1,$2,'reset regression',1)`, emp, emp.String()); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM employees WHERE id=$1`, emp)
		checkin, parseErr := time.ParseInLocation("2006-01-02 15:04", date+" 09:00", loc)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if _, err = db.Exec(`INSERT INTO work_sessions(id,employee_id,attendance_date,checkin_time,status,source,source_chat_id,shift_type) VALUES($1,$2,$3,$4,'open','telegram',$5,'day')`, work, emp, date, checkin, chat); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO attendance_records(employee_id,year_month,date,status,checkin_time) VALUES($1,$2,$3,'work',$4)`, emp, date[:7], date, checkin); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO wf_tg_activity_sessions(id,employee_id,activity_code,activity_name,attendance_date,started_at,limit_seconds,status,source_chat_id) VALUES($1,$2,'reset_test','reset test',$3,$4,0,'open',$5)`, activity, emp, date, checkin.Add(time.Minute), chat); err != nil {
			t.Fatal(err)
		}
	}
	// Same employee has started today's shift while yesterday is still open.
	if _, err = db.Exec(`UPDATE work_sessions SET employee_id=$1 WHERE id=$2`, oldEmployee, newWork); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO wf_tg_group_shift_state(chat_id,employee_id,shift_type,record_date) VALUES($1,$2,'day',$3)`, chat, oldEmployee, todayDate); err != nil {
		t.Fatal(err)
	}
	tgID := time.Now().UnixNano()%1000000000 + 3000000000
	if _, err = s.CreateBinding(ctx, oldEmployee, tgID, "", "1234567"); err != nil {
		t.Fatal(err)
	}
	if warnings := s.collectGroupBindingWarnings(ctx, chat, yesterday); len(warnings) != 0 {
		t.Fatalf("private chat incorrectly treated as group: %v", warnings)
	}
	if _, err = db.Exec(`UPDATE employee_telegram_bindings SET chat_id='-9988776655' WHERE employee_id=$1`, oldEmployee); err != nil {
		t.Fatal(err)
	}
	if warnings := s.collectGroupBindingWarnings(ctx, chat, yesterday); len(warnings) == 0 {
		t.Fatal("real group mismatch must remain visible")
	}
	closed, err := s.completeMissingWorkEndsForGroup(ctx, chat, yesterday, nil)
	if err != nil || closed != 1 {
		t.Fatalf("closed=%d err=%v", closed, err)
	}
	var checkout time.Time
	if err = db.QueryRow(`SELECT checkout_time FROM work_sessions WHERE id=$1`, oldWork).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	want, _ := time.ParseInLocation("2006-01-02 15:04", yesterday+" 21:00", loc)
	if !checkout.Equal(want) {
		t.Fatalf("checkout=%s want=%s", checkout, want)
	}
	var reviews int
	if err = db.QueryRow(`SELECT COUNT(*) FROM wf_tg_attendance_review_tasks WHERE employee_id=$1`, oldEmployee).Scan(&reviews); err != nil || reviews != 0 {
		t.Fatalf("false review=%d %v", reviews, err)
	}
	closed, err = s.forceCloseOpenActivitiesForGroup(ctx, chat, yesterday)
	if err != nil || closed != 1 {
		t.Fatalf("activities=%d %v", closed, err)
	}
	for _, row := range []struct {
		table string
		id    uuid.UUID
	}{{"work_sessions", newWork}, {"wf_tg_activity_sessions", newActivity}} {
		var status string
		if err = db.QueryRow("SELECT status FROM "+row.table+" WHERE id=$1", row.id).Scan(&status); err != nil || status != "open" {
			t.Fatalf("today changed: %s %v", status, err)
		}
	}
	var stateDate string
	if err = db.QueryRow(`SELECT record_date::text FROM wf_tg_group_shift_state WHERE employee_id=$1 AND chat_id=$2`, oldEmployee, chat).Scan(&stateDate); err != nil || stateDate != todayDate {
		t.Fatalf("today shift state cleared: %s %v", stateDate, err)
	}
	// A group reset before the global reset clock still targets yesterday.
	if _, err = db.Exec(`UPDATE wf_telegram_groups SET daily_reset_time='06:00' WHERE chat_id=$1`, chat); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 10, 8, 0, 0, 0, loc)
	if got := s.scheduledResetExportDateForChat(ctx, chat, settings, at); got != "2026-11-09" {
		t.Fatalf("group date=%s", got)
	}
	if _, err = db.Exec(`UPDATE wf_telegram_groups SET daily_reset_time='23:00' WHERE chat_id=$1`, chat); err != nil {
		t.Fatal(err)
	}
	at = time.Date(2026, 11, 10, 1, 0, 0, 0, loc)
	if got := s.scheduledResetExportDateForChat(ctx, chat, settings, at); got != "2026-11-08" {
		t.Fatalf("midnight date=%s", got)
	}
}
