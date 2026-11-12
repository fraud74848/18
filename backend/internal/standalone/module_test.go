// Consolidated source; original file boundaries are marked below.
package standalone

import (
	context "context"
	sql "database/sql"
	"encoding/json"
	"fmt"
	gin "github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	io "io"
	http "net/http"
	httptest "net/http/httptest"
	os "os"
	strings "strings"
	testing "testing"
	tgbot "tg-attendance/backend/internal/tgbot"
	services "tg-attendance/backend/services"
	time "time"
)

// ---- Source: backend/internal/standalone/auth_test.go ----
func TestSameOriginRejectsCrossSite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		origin, header string
		want           int
	}{{"https://evil.example", "TGAttendance", 403}, {"", "", 403}, {"https://attendance.example", "TGAttendance", 200}, {"", "TGAttendance", 200}} {
		r := gin.New()
		r.Use(SameOrigin())
		r.POST("/", func(c *gin.Context) { c.Status(200) })
		req := httptest.NewRequest(http.MethodPost, "https://attendance.example/", strings.NewReader("{}"))
		req.Header.Set("Origin", tt.origin)
		req.Header.Set("X-Requested-With", tt.header)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Fatalf("origin=%q: got %d want %d", tt.origin, w.Code, tt.want)
		}
	}
}

// ---- Source: backend/internal/standalone/inbox_test.go ----
func TestInboxDurableDedupeAndSecret(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable test database")
	}
	t.Setenv("TELEGRAM_TOKEN_SECRET", "local-test-encryption-key-not-production-123")
	db, e := sql.Open("postgres", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	tg := services.NewWorkforceTelegramService(db, services.NewWorkforceService(db))
	secret, e := tg.GetWebhookSecret(context.Background())
	if e != nil || secret == "" {
		t.Fatal("test database needs initialized secret", e)
	}
	inbox := &Inbox{DB: db, TG: tg, Wake: make(chan struct{}, 1)}
	r := gin.New()
	r.POST("/telegram/webhook", inbox.Accept)
	const id = 8888888888999
	db.Exec("DELETE FROM standalone_inbox WHERE update_id=$1", id)
	defer db.Exec("DELETE FROM standalone_inbox WHERE update_id=$1", id)
	for _, h := range []string{"wrong", secret, secret} {
		req := httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader(`{"update_id":8888888888999}`))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", h)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		want := 200
		if h == "wrong" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("status %d want %d", w.Code, want)
		}
	}
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM standalone_inbox WHERE update_id=$1", id).Scan(&n); e != nil || n != 1 {
		t.Fatal("duplicate message persisted", n, e)
	}
}

// ---- Source: backend/internal/standalone/member_notice_test.go ----
type joinTransport func(*http.Request) (*http.Response, error)

func (f joinTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMemberNoticePersistentDeduplication(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable database")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENV", "test")
	t.Setenv("ADMIN_PASSWORD", "local-test-password-only")
	db, err := services.OpenAttendanceDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const chat int64 = -889991234567
	db.ExecContext(context.Background(), `DELETE FROM standalone_member_notices WHERE chat_id=$1`, chat)
	defer db.Exec(`DELETE FROM standalone_member_notices WHERE chat_id=$1`, chat)
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	calls := 0
	http.DefaultClient = &http.Client{Transport: joinTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/sendMessage") {
			t.Fatal("unexpected Telegram request")
		}
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}
	inbox := &Inbox{DB: db}
	opts := tgbot.Options{BotToken: "fake-join-token"}
	first := []byte(`{"message":{"date":100,"chat":{"id":-889991234567,"type":"supergroup"},"new_chat_members":[{"id":42,"first_name":"A"},{"id":43,"first_name":"B"}]}}`)
	for i := 0; i < 2; i++ {
		if err = inbox.process(opts, first, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	otherType := []byte(`{"chat_member":{"date":101,"chat":{"id":-889991234567,"type":"supergroup"},"old_chat_member":{"status":"left"},"new_chat_member":{"status":"member","user":{"id":42}}}}`)
	// New Inbox instance simulates restart, preserving deduplication in PostgreSQL.
	inbox = &Inbox{DB: db}
	if err = inbox.process(opts, otherType, time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("duplicate notices: got %d", calls)
	}
	rejoin := []byte(strings.Replace(string(otherType), `"date":101`, `"date":110`, 1))
	if err = inbox.process(opts, rejoin, time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("later rejoin missing")
	}
}

func TestRBACEditImpliesViewButNotHighRisk(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("standaloneRole", "member")
	c.Set("permissions", normalizeGrants([]string{"attendance:edit", "unknown:edit"}))
	if !hasGrant(c, "attendance:view") || !hasGrant(c, "attendance:edit") {
		t.Fatal("edit must imply view")
	}
	for _, p := range []string{"attendance:delete", "attendance:export", "roles:assign", "unknown:edit"} {
		if hasGrant(c, p) {
			t.Fatalf("unexpected permission %s", p)
		}
	}
	c.Set("permissions", []string{"attendance:export"})
	if hasGrant(c, "attendance:export") {
		t.Fatal("high-risk action without view must be rejected")
	}
}
func TestRBACProtectedRoutesDenyByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		method, path, role string
		permissions        []string
		want               int
	}{
		{"GET", "/api/v1/standalone/roles", "member", nil, 403},
		{"GET", "/api/v1/standalone/roles", "member", []string{"roles:view"}, 204},
		{"POST", "/api/v1/standalone/roles", "member", []string{"roles:view"}, 403},
		{"POST", "/api/v1/standalone/roles", "member", normalizeGrants([]string{"roles:edit"}), 204},
		{"DELETE", "/api/v1/standalone/roles/:id", "member", normalizeGrants([]string{"roles:edit"}), 403},
		{"DELETE", "/api/v1/standalone/roles/:id", "member", []string{"roles:view", "roles:delete"}, 204},
		{"POST", "/api/v1/standalone/memory/clean", "member", []string{"maintenance:view"}, 403},
		{"POST", "/api/v1/standalone/accounts/:id/password", "member", []string{"accounts:view", "accounts:edit"}, 403},
		{"POST", "/api/v1/standalone/accounts/:id/reset-daily", "member", []string{"accounts:view", "accounts:edit"}, 403},
		{"POST", "/api/v1/standalone/accounts/:id/reset-daily", "member", []string{"accounts:view", "accounts:reset"}, 204},
		{"POST", "/api/v1/standalone/accounts/:id/telegram", "member", []string{"accounts:view"}, 403},
		{"PUT", "/api/v1/standalone/login-policy", "member", []string{"accounts:view", "accounts:edit"}, 403},
		{"PUT", "/api/v1/standalone/login-policy", "member", []string{"accounts:view", "accounts:security"}, 204},
		{"PUT", "/api/v1/standalone/accounts/:id/totp", "member", []string{"accounts:view"}, 403},
		{"GET", "/api/v1/unclassified", "admin", []string{"*"}, 403},
	}
	for _, tt := range cases {
		t.Run(tt.method+tt.path+strings.Join(tt.permissions, ","), func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set("standaloneRole", tt.role); c.Set("permissions", tt.permissions) })
			r.Handle(tt.method, tt.path, (Auth{}).Access(), func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tt.method, strings.ReplaceAll(tt.path, ":id", "test-id"), nil))
			if w.Code != tt.want {
				t.Fatalf("got %d want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestInboxMaintenanceYieldsToNewUpdate(t *testing.T) {
	for _, pendingFirst := range []bool{false, true} {
		inbox := &Inbox{Wake: make(chan struct{}, 1)}
		if pendingFirst {
			inbox.Notify()
		}
		ctx, cancel := inbox.maintenanceContext(context.Background())
		if !pendingFirst {
			inbox.Notify()
		}
		select {
		case <-ctx.Done():
		default:
			t.Fatal("maintenance did not yield to accepted update")
		}
		cancel()
		select {
		case <-inbox.Wake:
		default:
			t.Fatal("update wake was lost")
		}
		next, finish := inbox.maintenanceContext(context.Background())
		if next.Err() != nil {
			t.Fatal("later idle maintenance was cancelled")
		}
		finish()
	}
}
func TestBackendPunchRequiresExplicitGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"checkin", "checkout", "activity/start", "activity/back"} {
		for _, tc := range []struct {
			name   string
			grants []string
			want   int
		}{
			{"viewer", []string{"punch:view"}, 403},
			{"rule_editor", []string{"telegram:view", "telegram:edit", "punch:view"}, 403},
			{"attendance_editor", []string{"attendance:view", "attendance:edit", "punch:view"}, 403},
			{"missing_view", []string{"punch:edit"}, 403},
			{"punch_operator", []string{"punch:view", "punch:edit"}, 204},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				r := gin.New()
				r.Use(func(c *gin.Context) {
					c.Set("standaloneRole", "member")
					c.Set("permissions", append(tc.grants, "data:all"))
					c.Next()
				})
				path := "/api/v1/admin/workforce/telegram/punch/" + endpoint
				r.POST(path, (Auth{}).Access(), func(c *gin.Context) { c.Status(204) })
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
				if w.Code != tc.want {
					t.Fatalf("got %d want %d", w.Code, tc.want)
				}
			})
		}
	}
}

func TestAttendanceSummaryScopeAndCategories(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/", nil)
	ctx.Set("standaloneRole", "admin")
	const date = "2040-02-03"
	before, err := attendanceSummary(ctx, db, date)
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	if _, err = db.Exec(`INSERT INTO users(id,username,password_hash,status) VALUES($1,$2,'test-only',1)`, userID, userID.String()); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, userID)
	for n, status := range []string{"work", "working", "leave", "rest_full", "rest_half", "absent", "pending"} {
		employee := uuid.New()
		if _, err = db.Exec(`INSERT INTO employees(id,employee_id,name,status,user_id) VALUES($1,$2,'summary test',1,$3)`, employee, employee.String(), func() any {
			if n == 0 {
				return userID
			}
			return nil
		}()); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM employees WHERE id=$1`, employee)
		if _, err = db.Exec(`INSERT INTO attendance_records(employee_id,year_month,date,status) VALUES($1,'2040-02',$2,$3)`, employee, date, status); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			if _, err = db.Exec(`INSERT INTO penalty_records(employee_id,employee_name,amount,category,reason,penalty_date) VALUES($1,'summary test',0,'早退','早退测试',$2)`, employee, date); err != nil {
				t.Fatal(err)
			}
		}
		if n == 0 {
			if _, err = db.Exec(`INSERT INTO wf_tg_attendance_review_tasks(employee_id,attendance_date,year_month,trigger_type,status) VALUES($1,$2,'2040-02','early_checkout_review','pending')`, employee, date); err != nil {
				t.Fatal(err)
			}
		}
		if n == 0 {
			for j := 0; j < 2; j++ {
				if _, err = db.Exec(`INSERT INTO work_sessions(id,employee_id,attendance_date,checkin_time,status,source,late_minutes) VALUES($1,$2,$3,NOW(),'closed','web',7)`, uuid.New(), employee, date); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	after, err := attendanceSummary(ctx, db, date)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{"present": 2, "late": 1, "leave": 3, "absent": 1} {
		if after[key].(int)-before[key].(int) != want {
			t.Fatalf("%s: before=%v after=%v", key, before, after)
		}
	}
	if len(after["people"].(gin.H)["early"].([]gin.H))-len(before["people"].(gin.H)["early"].([]gin.H)) != 2 {
		t.Fatal("early roster missing recorded/pending people")
	}
	ctx.Set("standaloneRole", "member")
	ctx.Set("userID", userID.String())
	own, err := attendanceSummary(ctx, db, date)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{"present": 1, "late": 1, "leave": 0, "absent": 0} {
		if own[key].(int) != want {
			t.Fatalf("unscoped %s: %v", key, own)
		}
	}
	people := own["people"].(gin.H)
	if len(people["late"].([]gin.H)) != 1 || len(people["early"].([]gin.H)) != 1 || people["early"].([]gin.H)[0]["pending"] != true || len(people["leave"].([]gin.H)) != 0 {
		t.Fatal("roster scope/pending flags", people)
	}
	ctx.Set("userID", "")
	if _, err = attendanceSummary(ctx, db, date); err == nil {
		t.Fatal("missing identity accepted")
	}
}

func TestAccountAttendanceRejectsOtherEmployeeWithoutDataGrant(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	c.Params = gin.Params{{Key: "id", Value: uuid.NewString()}}
	c.Set("standaloneRole", "member")
	c.Set("userID", uuid.NewString())
	c.Set("permissions", []string{"accounts:view", "accounts:edit", "accounts:reset"})
	// A nil database ensures rejection happens before reading or modifying another account.
	(Auth{}).AccountAttendance(c)
	if c.Writer.Status() != 403 {
		t.Fatalf("cross-account action status=%d", c.Writer.Status())
	}
}

func TestAdminListsDatabasePagination(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prefix := uuid.NewString()
	base := -time.Now().UnixMilli() * 1000
	if _, err = db.Exec(`INSERT INTO standalone_inbox(update_id,body,received_at) SELECT $1::bigint+n,'{}','2099-01-01' FROM generate_series(1,21) n`, base); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM standalone_inbox WHERE update_id BETWEEN $1 AND $1+21`, base)
	if _, err = db.Exec(`INSERT INTO standalone_exports(filename,content,created_at) SELECT $1||n::text,'x'::bytea,'2099-01-01' FROM generate_series(1,21) n`, prefix); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM standalone_exports WHERE filename LIKE $1`, prefix+"%")
	if _, err = db.Exec(`INSERT INTO operation_logs(operator_name,operation_type,operation_module,operation_desc,target_id,created_at) SELECT 'test','test','RESOURCE_MAINTENANCE',json_build_object('id',$1||n::text)::text,$1,'2099-01-01' FROM generate_series(1,21) n`, prefix); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM operation_logs WHERE target_id=$1`, prefix)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("standaloneRole", "admin") })
	inbox := Inbox{DB: db}
	r.GET("/status", inbox.Status)
	r.GET("/exports", inbox.Exports)
	r.GET("/memory", (Auth{DB: db}).Memory)
	for _, tc := range []struct{ path, list, key string }{{"/status", "items", "updateId"}, {"/exports", "items", "filename"}, {"/memory", "history", "id"}} {
		seen := map[string]bool{}
		for page := 1; page <= 2; page++ {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("%s?page=%d", tc.path, page), nil))
			var result struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatalf("%s: %s", tc.path, w.Body)
			}
			var items []map[string]interface{}
			json.Unmarshal(result.Data[tc.list], &items)
			if len(items) != 10 {
				t.Fatalf("%s page %d rows=%d", tc.path, page, len(items))
			}
			for _, item := range items {
				key := fmt.Sprint(item[tc.key])
				if seen[key] {
					t.Fatalf("overlapping pages: %s %s", tc.path, key)
				}
				seen[key] = true
			}
		}
	}
	for _, query := range []string{"", "?page=-1&pageSize=invalid", "?page=999&pageSize=10"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/"+query, nil)
		page, size, total, err := listPage(c, db, `SELECT 21`)
		want := 1
		if strings.Contains(query, "999") {
			want = 3
		}
		if err != nil || page != want || size != 10 || total != 21 {
			t.Fatalf("pagination %s: %d %d %d %v", query, page, size, total, err)
		}
	}
}

func TestStandaloneTwoStepLogin(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ENV", "test")
	t.Setenv("ADMIN_PASSWORD", "local-test-password-only")
	t.Setenv("TELEGRAM_TOKEN_SECRET", "local-test-encryption-key-not-production-123")
	db, err := services.OpenAttendanceDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE system_settings SET value='false' WHERE key='standalone_totp_global'`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`UPDATE system_settings SET value='false' WHERE key='standalone_totp_global'`)
	admin, member := uuid.New(), uuid.New()
	password := "login-security-test-password"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	for _, id := range []uuid.UUID{admin, member} {
		if _, err = db.Exec(`INSERT INTO users(id,username,password_hash,status) VALUES($1,$2,$3,1)`, id, id.String(), string(hash)); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM users WHERE id=$1`, id)
	}
	if _, err = db.Exec(`INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE name IN ('admin','管理员') LIMIT 1`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO standalone_subaccounts(user_id) VALUES($1)`, member); err != nil {
		t.Fatal(err)
	}
	a := Auth{DB: db}
	r := gin.New()
	r.POST("/auth/login", a.Login)
	r.POST("/auth/verify", a.VerifyLogin)
	p := r.Group("/api/v1", a.Require(), a.Access())
	p.GET("/auth/me", a.Me)
	p.PUT("/standalone/login-policy", a.LoginPolicy)
	p.PUT("/standalone/accounts/:id/totp", a.LoginPolicy)
	call := func(path string, body any, cookie string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		method := "POST"
		if strings.HasPrefix(path, "/api/") {
			method = "PUT"
		}
		if strings.HasSuffix(path, "/me") {
			method = "GET"
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	begin := func(id uuid.UUID, want string) map[string]interface{} {
		t.Helper()
		w := call("/auth/login", gin.H{"username": id.String(), "password": password}, "")
		var response struct{ Data map[string]interface{} }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Data["verification"] != want || len(w.Result().Cookies()) != 0 {
			t.Fatalf("challenge %s: %s", want, w.Body)
		}
		return response.Data
	}
	verify := func(ch map[string]interface{}, code string) *httptest.ResponseRecorder {
		return call("/auth/verify", gin.H{"challengeId": ch["challengeId"], "code": code}, "")
	}
	captcha := func(id uuid.UUID) string {
		t.Helper()
		ch := begin(id, "captcha")
		key := ch["challengeId"].(string)
		if !strings.HasPrefix(ch["captchaImage"].(string), "data:image/png;base64,") {
			t.Fatal("captcha not local PNG")
		}
		if _, err = db.Exec(`UPDATE standalone_login_challenges SET answer_hash=$2 WHERE id_hash=$1`, digest(key), digest(key+"A2B3")); err != nil {
			t.Fatal(err)
		}
		if w := verify(ch, "wrong"); w.Code != 400 {
			t.Fatal("bad captcha accepted")
		}
		w := verify(ch, "a2b3")
		if w.Code != 200 || len(w.Result().Cookies()) != 1 {
			t.Fatal("captcha login failed", w.Body)
		}
		if replay := verify(ch, "A2B3"); replay.Code != 400 {
			t.Fatal("challenge replay accepted")
		}
		return w.Result().Cookies()[0].String()
	}
	adminCookie := captcha(admin)
	memberCookie := captcha(member)
	policy := func(path string, enabled, reset bool) {
		t.Helper()
		w := call(path, gin.H{"enabled": enabled, "reset": reset, "password": password}, adminCookie)
		if w.Code != 200 {
			t.Fatal("policy change", w.Body)
		}
	}
	individualPath := "/api/v1/standalone/accounts/" + member.String() + "/totp"
	if w := call(individualPath, gin.H{"enabled": true, "password": password}, memberCookie); w.Code != 403 {
		t.Fatal("member changed security policy")
	}
	policy(individualPath, true, false)
	if w := call("/api/v1/auth/me", nil, memberCookie); w.Code != 401 {
		t.Fatal("enabling MFA left existing session active")
	}
	setup := begin(member, "setup")
	secret := setup["secret"].(string)
	code, _ := totp.GenerateCode(secret, time.Now())
	if w := verify(setup, code); w.Code != 200 {
		t.Fatal("enrollment failed", w.Body)
	}
	var stored string
	if err = db.QueryRow(`SELECT standalone_totp_secret FROM users WHERE id=$1`, member).Scan(&stored); err != nil || stored == "" || stored == secret {
		t.Fatal("secret not encrypted", err)
	}
	ch := begin(member, "totp")
	if _, ok := ch["secret"]; ok {
		t.Fatal("enrolled secret disclosed")
	}
	if w := verify(ch, code); w.Code != 400 {
		t.Fatal("TOTP timestep replay accepted")
	}
	next, _ := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if w := verify(ch, next); w.Code != 200 {
		t.Fatal("valid next TOTP rejected", w.Body)
	}
	stale := begin(admin, "captcha")
	policy("/api/v1/standalone/login-policy", true, false)
	if _, err = db.Exec(`UPDATE standalone_login_challenges SET answer_hash=$2 WHERE id_hash=$1`, digest(stale["challengeId"].(string)), digest(stale["challengeId"].(string)+"A2B3")); err != nil {
		t.Fatal(err)
	}
	if w := verify(stale, "A2B3"); w.Code != 400 {
		t.Fatal("old captcha bypassed global TOTP activation")
	}
	begin(admin, "setup")
	policy(individualPath, false, false)
	begin(member, "totp") // Global requirement wins.
	policy("/api/v1/standalone/login-policy", false, false)
	captcha(member)
	policy(individualPath, true, true)
	begin(member, "setup")
	policy(individualPath, false, false)
	expired := begin(member, "captcha")
	db.Exec(`UPDATE standalone_login_challenges SET expires_at=NOW()-interval '1 second' WHERE id_hash=$1`, digest(expired["challengeId"].(string)))
	if w := verify(expired, "A2B3"); w.Code != 400 {
		t.Fatal("expired challenge accepted")
	}
	limited := begin(member, "captcha")
	for i := 0; i < 6; i++ {
		if w := verify(limited, "bad"); w.Code != 400 {
			t.Fatal("invalid attempts accepted")
		}
	}
	var attempts int
	db.QueryRow(`SELECT attempts FROM standalone_login_challenges WHERE id_hash=$1`, digest(limited["challengeId"].(string))).Scan(&attempts)
	if attempts != 5 {
		t.Fatalf("challenge attempts not bounded: %d", attempts)
	}
}
func TestLoginCaptchaMixedAlphabet(t *testing.T) {
	for i := 0; i < 20; i++ {
		code, img, err := loginCaptcha()
		if err != nil || len(code) != 4 || !strings.HasPrefix(img, "data:image/png;base64,") {
			t.Fatal("invalid captcha", err)
		}
		digit, letter := false, false
		for _, ch := range code {
			digit = digit || (ch >= '0' && ch <= '9')
			letter = letter || (ch >= 'A' && ch <= 'Z')
		}
		if !digit || !letter {
			t.Fatal("captcha must mix digits and letters")
		}
	}
}
