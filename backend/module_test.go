// Consolidated source; original file boundaries are marked below.
package main

import (
	context "context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	uuid "github.com/google/uuid"
	httptest "net/http/httptest"
	os "os"
	strings "strings"
	testing "testing"
	handlers "tg-attendance/backend/handlers"
	standalone "tg-attendance/backend/internal/standalone"
	middleware "tg-attendance/backend/middleware"
	services "tg-attendance/backend/services"
	"time"
)

// ---- Source: backend/standalone_accounts_test.go ----
func TestStandaloneSubaccounts(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
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
	middleware.InitPermissionDB(db)
	middleware.InitEmployeeScopeDB(db)
	wf := services.NewWorkforceService(db)
	tg := services.NewWorkforceTelegramService(db, wf)
	a := standalone.Auth{DB: db}
	r := gin.New()
	p := r.Group("/api/v1", standalone.SameOrigin())
	p.GET("/auth/captcha", a.Captcha)
	p.POST("/auth/login", a.Login)
	p.POST("/auth/verify", a.VerifyLogin)
	protected := p.Group("", a.Require(), a.Access())
	protected.GET("/auth/me", a.Me)
	protected.GET("/standalone/accounts", a.Accounts)
	protected.POST("/standalone/accounts", a.CreateAccount)
	protected.PUT("/standalone/accounts/:id", a.UpdateAccount)
	protected.POST("/standalone/accounts/:id/telegram", a.AccountAttendance)
	protected.POST("/standalone/accounts/:id/reset-daily", a.AccountAttendance)
	protected.POST("/standalone/bind-code", a.BindCode)
	protected.POST("/standalone/bind-telegram", a.BindOwnTelegram)
	protected.GET("/standalone/status", (&standalone.Inbox{DB: db}).Status)
	ah := handlers.NewAdminHandler(db, wf, services.NewPerformancePushService(db), services.NewPenaltyPushService(db), nil, nil, nil)
	th := handlers.NewWorkforceTelegramHandler(db, tg, nil)
	wh := handlers.NewWorkforceHandler(db, wf, tg, services.NewUserRepository(db), services.NewDeviceRepository(db), nil)
	registerAdminRoutes(protected.Group("/admin"), th, ah, wh)
	request := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "TGAttendance")
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	login := func(name, pass string) string {
		w := request("GET", "/auth/captcha", "", "")
		if w.Code != 200 {
			t.Fatalf("captcha %d %s", w.Code, w.Body)
		}
		var captcha struct {
			Data struct {
				CaptchaID    string `json:"captchaId"`
				CaptchaImage string `json:"captchaImage"`
			}
		}
		if json.Unmarshal(w.Body.Bytes(), &captcha) != nil || captcha.Data.CaptchaID == "" || captcha.Data.CaptchaImage == "" {
			t.Fatal("missing captcha", w.Body.String())
		}
		// Password alone must never create a session.
		w = request("POST", "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, name, pass), "")
		if w.Code == 200 || len(w.Result().Cookies()) != 0 {
			t.Fatal("login bypassed captcha", w.Body.String())
		}
		sum := sha256.Sum256([]byte(captcha.Data.CaptchaID + "A2B3"))
		key := sha256.Sum256([]byte(captcha.Data.CaptchaID))
		if _, err = db.Exec(`UPDATE standalone_login_captchas SET answer_hash=$2 WHERE id_hash=$1`, hex.EncodeToString(key[:]), hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
		w = request("POST", "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"captchaId":%q,"code":"a2b3"}`, name, pass, captcha.Data.CaptchaID), "")
		if w.Code != 200 || len(w.Result().Cookies()) != 1 {
			t.Fatal("captcha login", w.Body.String())
		}
		// The captcha is single-use.
		if w2 := request("POST", "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q,"captchaId":%q,"code":"a2b3"}`, name, pass, captcha.Data.CaptchaID), ""); w2.Code == 200 {
			t.Fatal("captcha reused")
		}
		return w.Result().Cookies()[0].String()
	}
	admin := login("admin", "local-test-password-only")
	own, other := uuid.New(), uuid.New()
	username := "test_" + strings.ReplaceAll(own.String(), "-", "")[:12]
	for _, id := range []uuid.UUID{own, other} {
		if _, err = db.Exec(`INSERT INTO employees(id,employee_id,name,status,attendance_mode) VALUES($1,$2,$2,1,'tg_only')`, id, id.String()); err != nil {
			t.Fatal(err)
		}
	}
	defer db.Exec(`DELETE FROM employees WHERE id IN ($1,$2)`, own, other)
	defer db.Exec(`DELETE FROM users WHERE username=$1`, username)
	w := request("POST", "/standalone/accounts", fmt.Sprintf(`{"username":%q,"password":"member-test-password","employeeId":%q}`, username, own), admin)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body)
	}
	var resp struct {
		Data struct {
			ID string `json:"id"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	id := resp.Data.ID

	for _, eid := range []uuid.UUID{own, other} {
		if _, err = db.Exec(`INSERT INTO attendance_records(employee_id,year_month,date,status) VALUES($1,'2026-11','2026-11-08','work')`, eid); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO penalty_records(employee_id,employee_name,amount,category,reason,penalty_date) VALUES($1,$2,10,'test','scope test','2026-11-08')`, eid, eid.String()); err != nil {
			t.Fatal(err)
		}
	}
	member := login(username, "member-test-password")
	for _, term := range []string{username, own.String()} {
		response := request("GET", "/standalone/accounts?search="+term+"&page=999", "", admin)
		var result struct {
			Data struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Total, Page, PageSize int
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Total != 1 || result.Data.Page != 1 || result.Data.PageSize != 10 || len(result.Data.Items) != 1 || result.Data.Items[0].ID != id {
			t.Fatal("account search/page", response.Body.String())
		}
	}
	if response := request("POST", "/standalone/bind-telegram", `{"telegramUserId":"888881234567","employeeId":"`+other.String()+`"}`, member); response.Code != 200 {
		t.Fatal("self bind", response.Body.String())
	}
	var boundEmployee string
	if err = db.QueryRow(`SELECT employee_id::text FROM employee_telegram_bindings WHERE telegram_user_id=888881234567`).Scan(&boundEmployee); err != nil || boundEmployee != own.String() {
		t.Fatal("self binding crossed account", boundEmployee, err)
	}
	if response := request("POST", "/standalone/bind-telegram", `{"telegramUserId":"888881234566"}`, member); response.Code != 409 {
		t.Fatal("self-service overwrote existing binding", response.Body.String())
	}
	if response := request("POST", "/standalone/bind-telegram", `{"telegramUserId":"123"}`, ""); response.Code != 401 {
		t.Fatal("anonymous binding allowed")
	}

	// Login time and recent activity are independently observable.
	assertOnline := func(want bool) {
		t.Helper()
		response := request("GET", "/standalone/accounts", "", admin)
		var result struct {
			Data struct {
				Items []struct {
					ID        string     `json:"id"`
					LastLogin *time.Time `json:"lastLoginAt"`
					Online    bool       `json:"online"`
				} `json:"items"`
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal(response.Body.String())
		}
		for _, row := range result.Data.Items {
			if row.ID == id {
				if row.LastLogin == nil || row.Online != want {
					t.Fatalf("account activity: %+v", row)
				}
				return
			}
		}
		t.Fatal("missing account")
	}
	assertOnline(true)
	if _, err = db.Exec(`UPDATE standalone_sessions SET last_seen_at=NOW()-interval '3 minutes' WHERE user_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	assertOnline(false)
	if response := request("GET", "/auth/me", "", member); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	assertOnline(true)
	for _, action := range []string{"telegram", "reset-daily"} {
		if response := request("POST", "/standalone/accounts/"+id+"/"+action, `{"telegramUserId":"123"}`, member); response.Code != 403 {
			t.Fatalf("ungranted account action: %s", response.Body.String())
		}
	}
	if response := request("POST", "/standalone/accounts/"+id+"/telegram", `{"telegramUserId":"invalid"}`, admin); response.Code != 400 {
		t.Fatal(response.Body.String())
	}
	if response := request("POST", "/standalone/accounts/"+id+"/telegram", `{"telegramUserId":"888881234567"}`, admin); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	// This fixture tests calendar-day reset without a group business-day override.
	if _, err = db.Exec(`UPDATE employee_telegram_bindings SET chat_id='' WHERE employee_id=$1`, own); err != nil {
		t.Fatal(err)
	}
	// Daily reset keeps the old record archived and frees the work punch slot.
	today := time.Now().Format("2006-01-02")
	resetPunch, resetErr := wf.CheckInForEmployee(context.Background(), own, "web", nil, &services.CheckInOptions{RecordDate: today, ShiftType: "day", LateMinutes: 7})
	if resetErr != nil {
		t.Fatal(resetErr)
	}
	if _, err = wf.CheckOutForEmployee(context.Background(), own, "web", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	statsResponse := request("GET", "/standalone/status", "", admin)
	var stats struct {
		Data struct {
			Attendance struct{ Present, Late int } `json:"attendance"`
		} `json:"data"`
	}
	if statsResponse.Code != 200 || json.Unmarshal(statsResponse.Body.Bytes(), &stats) != nil || stats.Data.Attendance.Present < 1 || stats.Data.Attendance.Late < 1 {
		t.Fatal("attendance summary", statsResponse.Body.String())
	}
	if response := request("POST", "/standalone/accounts/"+id+"/reset-daily", `{}`, admin); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var archived bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM work_sessions_archive WHERE id=$1)`, resetPunch.SessionID).Scan(&archived); err != nil || !archived {
		t.Fatal("reset lost archive", err)
	}
	if _, err = wf.CheckInForEmployee(context.Background(), own, "web", nil, &services.CheckInOptions{RecordDate: today, ShiftType: "day"}); err != nil {
		t.Fatal("reset did not free punch", err)
	}

	for _, path := range []string{"/standalone/accounts", "/standalone/status", "/admin/workforce/telegram/worker-connection", "/admin/workforce/telegram/export"} {
		if w = request("GET", path, "", member); w.Code != 403 {
			t.Fatalf("unprotected %s %d", path, w.Code)
		}
	}
	if w = request("POST", "/standalone/accounts", `{}`, member); w.Code != 403 {
		t.Fatal("member can create accounts")
	}
	if w = request("PUT", "/admin/workforce/telegram/settings", `{}`, member); w.Code != 403 {
		t.Fatal("member can edit settings")
	}
	for _, path := range []string{"/admin/employees?employeeId=" + other.String(), "/admin/workforce/telegram/work-sessions?employeeId=" + other.String(), "/admin/workforce/telegram/activity-sessions?employeeId=" + other.String(), "/admin/penalty/records?employee_id=" + other.String(), "/admin/attendance/records?year_month=2026-11&employee_ids[]=" + other.String(), "/admin/workforce/telegram/punch/me", "/admin/workforce/telegram/settings", "/admin/workforce/telegram/activity-types", "/admin/workforce/telegram/groups"} {
		w = request("GET", path, "", member)
		if w.Code != 200 {
			t.Fatalf("member read %s %d %s", path, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), other.String()) {
			t.Fatalf("cross employee data %s", path)
		}
	}
	// Existing punch handler must reject attempts to act for another employee.
	w = request("POST", "/admin/workforce/telegram/punch/checkin", fmt.Sprintf(`{"employeeId":%q,"shiftType":"day"}`, other), member)
	if w.Code == 200 {
		t.Fatal("cross employee punch accepted")
	}
	calendarUsername := username + "_calendar"
	created, createErr := wf.CreateEmployeeAccount(context.Background(), &services.CreateEmployeeAccountInput{
		Username: calendarUsername, Password: "calendar-test-password", Name: "月历子账号测试",
	})
	if createErr != nil {
		t.Fatal("calendar employee account", createErr)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, created.UserID)
	defer db.Exec(`DELETE FROM employees WHERE id=$1`, created.EmployeeID)
	calendarCookie := login(calendarUsername, "calendar-test-password")
	if response := request("GET", "/standalone/accounts", "", calendarCookie); response.Code != 403 {
		t.Fatal("calendar account must have member permissions")
	}
	if _, err = tg.BindTelegramByUsername(context.Background(), 888881234569, "calendar_test", calendarUsername, "-10099"); err != nil {
		t.Fatal("calendar account username binding", err)
	}
	if response := request("POST", "/standalone/accounts/"+id+"/telegram", `{"telegramUserId":"888881234569"}`, admin); response.Code != 409 {
		t.Fatal("duplicate TG ownership accepted", response.Body.String())
	}
	// A late punch remains visible even with no fine configured.
	punch, punchErr := wf.CheckInForEmployee(context.Background(), created.EmployeeID, "web", nil,
		&services.CheckInOptions{RecordDate: "2026-11-08", ShiftType: "day", LateMinutes: 7})
	if punchErr != nil {
		t.Fatal(punchErr)
	}
	var savedLate int
	if err = db.QueryRow(`SELECT late_minutes FROM work_sessions WHERE id=$1`, punch.SessionID).Scan(&savedLate); err != nil || savedLate != 7 {
		t.Fatalf("late snapshot missing: %d %v", savedLate, err)
	}
	calendarResponse := request("GET", "/admin/attendance/records?year_month=2026-11&employee_ids[]="+created.EmployeeID.String(), "", calendarCookie)
	if calendarResponse.Code != 200 || !strings.Contains(calendarResponse.Body.String(), `"lateMinutes":7`) {
		t.Fatalf("calendar late flag missing: %s", calendarResponse.Body.String())
	}
	const tgID int64 = 888881234567
	if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, "", "-10099"); err != nil {
		t.Fatal("username-only binding failed", err)
	}

	if _, err = tg.BindTelegramSelf(context.Background(), tgID+1, "other", "", "", username, "", "-10099"); err == nil {
		t.Fatal("existing binding overwritten")
	}
	w = request("POST", "/standalone/bind-code", `{}`, member)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var codeResponse struct {
		Data struct {
			Command string `json:"command"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &codeResponse)
	parts := strings.Fields(codeResponse.Data.Command)

	db.Exec(`UPDATE standalone_bind_codes SET expires_at=NOW()-interval '1 second' WHERE user_id=$1`, id)
	if len(parts) == 3 {
		if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, parts[2], fmt.Sprint(tgID)); err == nil {
			t.Fatal("expired code accepted")
		}
	}
	w = request("POST", "/standalone/bind-code", `{}`, member)
	json.Unmarshal(w.Body.Bytes(), &codeResponse)
	parts = strings.Fields(codeResponse.Data.Command)
	if len(parts) != 3 {
		t.Fatal("invalid bind command")
	}
	if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, "wrong", fmt.Sprint(tgID)); err == nil {
		t.Fatal("bad code accepted")
	}
	if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, parts[2], "-1001"); err == nil {
		t.Fatal("group binding accepted")
	}
	if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, parts[2], fmt.Sprint(tgID)); err != nil {
		t.Fatal("bind failed", err)
	}
	if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, parts[2], fmt.Sprint(tgID)); err == nil {
		t.Fatal("code reused")
	}

	raceName := username + "_race"
	defer db.Exec(`DELETE FROM users WHERE username=$1`, raceName)
	w = request("POST", "/standalone/accounts", fmt.Sprintf(`{"username":%q,"password":"race-test-password","employeeId":%q}`, raceName, other), admin)
	if w.Code != 201 {
		t.Fatal("race fixture", w.Body.String())
	}
	results := make(chan error, 2)
	for n := int64(10); n < 12; n++ {
		go func(n int64) {
			_, e := tg.BindTelegramSelf(context.Background(), tgID+n, "race", "", "", raceName, "", "-10099")
			results <- e
		}(n)
	}
	successes := 0
	for n := 0; n < 2; n++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent username claim: %d winners", successes)
	}
	w = request("PUT", "/standalone/accounts/"+id, `{"password":"replacement-password"}`, admin)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if request("GET", "/auth/me", "", member).Code != 401 {
		t.Fatal("reset did not revoke session")
	}
	member = login(username, "replacement-password")
	w = request("PUT", "/standalone/accounts/"+id, `{"status":0}`, admin)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if request("GET", "/auth/me", "", member).Code != 401 {
		t.Fatal("disabled session accepted")
	}

	if _, err = tg.BindTelegramSelf(context.Background(), tgID, "test", "", "", username, "", "-10099"); err == nil {
		t.Fatal("disabled account can bind")
	}
	assertOnline(false)
	var bindingStatus int
	db.QueryRow(`SELECT status FROM employee_telegram_bindings WHERE employee_id=$1`, own).Scan(&bindingStatus)
	if bindingStatus != 0 {
		t.Fatal("disabled TG binding active")
	}
	if request("POST", "/auth/login", fmt.Sprintf(`{"username":%q,"password":"replacement-password"}`, username), "").Code != 401 {
		t.Fatal("disabled login accepted")
	}
}
