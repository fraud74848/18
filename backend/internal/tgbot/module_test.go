// Consolidated source; original file boundaries are marked below.
package tgbot

import (
	io "io"
	http "net/http"
	strings "strings"
	testing "testing"
	time "time"
)

// ---- Source: backend/internal/tgbot/bot_commands_sync_test.go ----
func TestNormalizeBotCommandsKeepsMenuOrder(t *testing.T) {
	raw := []map[string]interface{}{
		{"command": "checkin_day", "description": "白班上班 / Day shift"},
		{"command": "CHECKIN_DAY", "description": "dup"},
		{"command": "checkin_middle", "description": "中班上班 / Middle shift"},
		{"command": "坏命令", "description": "invalid"},
		{"command": "lang_zh", "description": "设为中文 / Set Chinese"},
	}
	got := normalizeBotCommands(raw)
	if len(got) != 3 {
		t.Fatalf("got %d commands: %#v", len(got), got)
	}
	if got[0]["command"] != "checkin_day" || got[1]["command"] != "checkin_middle" || got[2]["command"] != "lang_zh" {
		t.Fatalf("order mismatch: %#v", got)
	}
}

// ---- Source: backend/internal/tgbot/command_sync_queue_test.go ----
type menuTransport func(*http.Request) (*http.Response, error)

func (f menuTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMenuSyncDoesNotBlockPunchPath(t *testing.T) {
	old := http.DefaultClient
	release := make(chan struct{})
	started := make(chan struct{}, 32)
	completed := make(chan struct{}, 32)
	http.DefaultClient = &http.Client{Transport: menuTransport(func(r *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-release
		completed <- struct{}{}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Header: make(http.Header)}, nil
	})}
	returned := make(chan struct{})
	go func() {
		scheduleBotCommands(config{BotToken: "fake-menu-sync-token"}, []map[string]interface{}{{"command": "help", "description": "Help"}})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("menu sync blocks caller")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("background sync never started")
	}
	close(release)
	for n := 0; n < 18; n++ {
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatal("menu sync did not finish")
		}
	}
	// The worker has finished all network calls before restoring the client.
	http.DefaultClient = old
}

// ---- Source: backend/internal/tgbot/device_picker_test.go ----
func TestPackInlineButtonsWrapsByNameWidth(t *testing.T) {
	buttons := []map[string]interface{}{
		{"text": "张三"},
		{"text": "李四"},
		{"text": "王五"},
		{"text": "一个很长的员工姓名"},
		{"text": "赵六"},
	}
	rows := packInlineButtons(buttons)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %#v", len(rows), rows)
	}
	if len(rows[0]) != 3 {
		t.Fatalf("expected three short names in first row, got %d", len(rows[0]))
	}
	if len(rows[1]) != 1 || len(rows[2]) != 1 {
		t.Fatalf("expected long name and final name to wrap, got %#v", rows)
	}
}
func TestInlineLabelVisualWidth(t *testing.T) {
	if got := inlineLabelVisualWidth("张三"); got != 4 {
		t.Fatalf("Chinese width=%d", got)
	}
	if got := inlineLabelVisualWidth("Alice"); got != 5 {
		t.Fatalf("ASCII width=%d", got)
	}
}

// ---- Source: backend/internal/tgbot/member_notice_test.go ----
func TestMemberJoinMessages(t *testing.T) {
	body := []byte(`{"message":{"date":100,"chat":{"id":-123,"type":"supergroup"},"from":{"id":999},"new_chat_members":[{"id":42,"first_name":"<b>A</b>"},{"id":43,"username":"bob"},{"id":44,"is_bot":true}]}}`)
	notices, handled, err := MemberJoinNotices(body)
	if err != nil || !handled || len(notices) != 2 {
		t.Fatalf("join batch: %#v %v", notices, err)
	}
	if notices[0].UserID != 42 || !strings.Contains(notices[0].Text, "&lt;b&gt;A&lt;/b&gt;") || strings.Contains(notices[0].Text, "999") {
		t.Fatal("wrong member or unescaped name", notices[0])
	}
	if notices[1].UserID != 43 || notices[0].ChatID != -123 {
		t.Fatal("wrong destination")
	}
}
func TestMemberTransitionNotices(t *testing.T) {
	for _, tt := range []struct {
		old, new string
		want     int
	}{
		{`{"status":"left"}`, `{"status":"member","user":{"id":42}}`, 1},
		{`{"status":"restricted","is_member":false}`, `{"status":"restricted","is_member":true,"user":{"id":42}}`, 1},
		{`{"status":"member"}`, `{"status":"administrator","user":{"id":42}}`, 0},
		{`{"status":"member"}`, `{"status":"left","user":{"id":42}}`, 0},
	} {
		body := []byte(`{"chat_member":{"date":100,"chat":{"id":-123,"type":"supergroup"},"old_chat_member":` + tt.old + `,"new_chat_member":` + tt.new + `}}`)
		notices, handled, err := MemberJoinNotices(body)
		if err != nil || !handled || len(notices) != tt.want {
			t.Fatalf("transition %s -> %s: %v", tt.old, tt.new, notices)
		}
	}
	if _, handled, _ := MemberJoinNotices([]byte(`{"message":{"text":"/bind user"}}`)); handled {
		t.Fatal("normal command swallowed")
	}
}

// ---- Source: backend/internal/tgbot/user_display_test.go ----
func TestTGUserRefDisplayName(t *testing.T) {
	u := tgUserRef{ID: 99, FirstName: "Lan", LastName: "Nguyen", Username: "lan_vn"}
	if got := u.displayName(); got != "Lan Nguyen" {
		t.Fatalf("expected full name, got %q", got)
	}
	u = tgUserRef{ID: 99, Username: "lan_vn"}
	if got := u.displayName(); got != "@lan_vn" {
		t.Fatalf("expected @username, got %q", got)
	}
	u = tgUserRef{ID: 99}
	if got := u.displayName(); got != "用户99" {
		t.Fatalf("expected fallback id, got %q", got)
	}
}
func TestEnrichUnboundAlertHTMLPlainName(t *testing.T) {
	u := tgUserRef{ID: 12345, FirstName: "梁上听珂", Username: "testuser"}
	got := enrichUnboundAlertHTML(u, "⚠️ 未绑定")
	if strings.Contains(got, "t.me/") {
		t.Fatalf("unbound alert must not use t.me card: %q", got)
	}
	if !strings.Contains(got, `<a href="tg://user?id=12345">梁上听珂</a>`) {
		t.Fatalf("unbound name should open that TG user: %q", got)
	}
	if !strings.Contains(got, "您还未绑定Telegram打卡群") {
		t.Fatalf("expected new unbound format: %q", got)
	}
	if !strings.Contains(got, "/bind_workstation 邀请码") {
		t.Fatalf("expected bind instructions: %q", got)
	}
}
func TestTGUserRefHTMLProfileLink(t *testing.T) {
	u := tgUserRef{ID: 12345, FirstName: "Tom", Username: "tom_user"}
	got := u.htmlProfileLink()
	want := `<a href="tg://user?id=12345">Tom</a>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	u = tgUserRef{ID: 12345, FirstName: "张三"}
	got = u.htmlProfileLink()
	want = `<a href="tg://user?id=12345">张三</a>`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// ---- Source: backend/internal/tgbot/webhook_test.go ----
func TestHandleWebhookBody_SecretMatch(t *testing.T) {
	cfg := config{WebhookSecret: "wh-secret-token"}
	if err := HandleWebhookBody(cfg, "wh-secret-token", []byte(`{"update_id":1}`)); err != nil {
		t.Fatalf("expected valid secret to pass: %v", err)
	}
}
func TestHandleWebhookBody_SecretMismatch(t *testing.T) {
	cfg := config{WebhookSecret: "wh-secret-token"}
	if err := HandleWebhookBody(cfg, "wrong", []byte(`{"update_id":1}`)); err == nil {
		t.Fatal("expected wrong secret to be rejected")
	}
}

// ---- Source: backend/internal/tgbot/worker_error_i18n_test.go ----
func TestNeedsDeviceSelectionRetryLocalized(t *testing.T) {
	msgs := []string{
		"⚠️ 多台设备已登录，请重新点击普通打卡按钮并选择设备",
		"⚠️ Nhiều thiết bị đã đăng nhập, hãy bấm lại nút chấm công và chọn thiết bị",
		"⚠️ Multiple devices are signed in — tap the punch button again and select a device",
	}
	for _, msg := range msgs {
		if !needsDeviceSelectionRetry(msg) {
			t.Fatalf("device picker must still trigger for %q", msg)
		}
	}
	if needsDeviceSelectionRetry("⚠️ 已在上班状态") {
		t.Fatal("session-exists should not open device picker")
	}
}
func TestIsUnboundAlertLocalized(t *testing.T) {
	if !isUnboundAlert("⚠️ 未绑定。个人 TG：/bind") {
		t.Fatal("zh unbound")
	}
	if !isUnboundAlert("⚠️ Chưa gắn. Cá nhân: /bind") {
		t.Fatal("vi unbound")
	}
	if !isUnboundAlert("⚠️ Not bound. Personal: /bind") {
		t.Fatal("en unbound")
	}
}
func TestPunchFailPrefixFollowsUserLang(t *testing.T) {
	const uid int64 = 424242
	rememberUserLang(uid, "emp-a", "vi")
	if got := punchFailPrefix(uid); got != "Chấm công thất bại: " {
		t.Fatalf("vi prefix = %q", got)
	}
	rememberUserLang(uid, "emp-a", "en")
	if got := startActivityFailPrefix(uid); got != "Start activity failed: " {
		t.Fatalf("en prefix = %q", got)
	}
	rememberUserLang(uid, "emp-a", "zh")
	if got := checkoutFailPrefix(uid); got != "签退失败：" {
		t.Fatalf("zh prefix = %q", got)
	}
	clearUserLang(uid)
}
func TestSharedTGHandoffClearsPriorEmployeeLang(t *testing.T) {
	const uid int64 = 525252
	// A set Vietnamese
	rememberUserLang(uid, "employee-a", "vi")
	if userLang(uid) != "vi" || cachedEmpID(uid) != "employee-a" {
		t.Fatalf("A cache: lang=%s emp=%s", userLang(uid), cachedEmpID(uid))
	}
	// B logs in with no personal pref → backend replyLang zh
	applyBackendLangCache(uid, "employee-b", "zh", "vi")
	if got := userLang(uid); got != "zh" {
		t.Fatalf("B must get default zh, not inherit A vi; got %q", got)
	}
	if cachedEmpID(uid) != "employee-b" {
		t.Fatalf("emp id should be B, got %s", cachedEmpID(uid))
	}
	clearUserLang(uid)
}
func TestApplyBackendLangCachePrefersReplyLang(t *testing.T) {
	const uid int64 = 626262
	rememberUserLang(uid, "a", "vi")
	// preferredLang=vi (group) must not win over replyLang=zh (employee default)
	applyBackendLangCache(uid, "b", "zh", "vi")
	if userLang(uid) != "zh" {
		t.Fatal("replyLang zh must override preferredLang vi")
	}
	clearUserLang(uid)
}
