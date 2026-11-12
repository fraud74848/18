// Consolidated source; original file boundaries are marked below.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/gin-gonic/gin"
	uuid "github.com/google/uuid"
	"net/http/httptest"
	"os"
	"strconv"
	strings "strings"
	testing "testing"
	"tg-attendance/backend/services"
	"time"
)

// ---- Source: backend/handlers/device_register_secret_test.go ----
func TestDeviceSecretMatch(t *testing.T) {
	secret := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	if !deviceSecretMatch(secret, secret) {
		t.Fatal("expected match for identical secrets")
	}
	if deviceSecretMatch("", secret) {
		t.Fatal("empty provided must not match")
	}
	if deviceSecretMatch(secret, "") {
		t.Fatal("empty stored must not match")
	}
	if deviceSecretMatch(secret, "bbbbccddeeff00112233445566778899aabbccddeeff00112233445566778899") {
		t.Fatal("different secrets must not match")
	}
	if deviceSecretMatch(secret+"x", secret) {
		t.Fatal("length mismatch must not match")
	}
	if !deviceSecretMatch("  "+secret+"  ", secret) {
		t.Fatal("trim whitespace should still match")
	}
}

// ---- Source: backend/handlers/upload_session_test.go ----
func TestUploadSessionCheckOwner(t *testing.T) {
	t.Parallel()

	sess := &uploadSession{DeviceID: "device-owner-abc"}

	if err := sess.checkOwner("device-owner-abc"); err != nil {
		t.Fatalf("expected same device to pass: %v", err)
	}
	if err := sess.checkOwner("DEVICE-OWNER-ABC"); err != nil {
		t.Fatalf("expected case-insensitive match: %v", err)
	}
	if err := sess.checkOwner("other-device"); err == nil {
		t.Fatal("expected mismatch error")
	} else if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := sess.checkOwner(""); err == nil {
		t.Fatal("expected empty owner to fail")
	}

	unbound := &uploadSession{}
	if err := unbound.checkOwner("device-a"); err == nil {
		t.Fatal("expected unbound session to fail")
	} else if !strings.Contains(err.Error(), "no owner") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestGetOrCreateBindsDeviceAndRejectsHijack(t *testing.T) {
	t.Parallel()

	mgr := &uploadSessionManager{sessions: make(map[string]*uploadSession)}
	uploadID := "test-" + uuid.NewString()
	req := &chunkUpload{
		UploadID:    uploadID,
		FileName:    "clip.mp4",
		FileType:    "recording",
		FileID:      "file-1",
		TotalChunks: 2,
	}
	owner := "device-owner-111"
	hijacker := "device-hijacker-222"

	created, err := mgr.getOrCreate(req, owner)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.DeviceID != owner {
		t.Fatalf("expected DeviceID %q, got %q", owner, created.DeviceID)
	}

	reused, err := mgr.getOrCreate(req, owner)
	if err != nil {
		t.Fatalf("reuse session: %v", err)
	}
	if reused != created {
		t.Fatal("expected same session instance for same owner")
	}

	if _, err := mgr.getOrCreate(req, hijacker); err == nil {
		t.Fatal("expected hijacker to be rejected")
	} else if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestGetOrCreateRequiresAuthenticatedDevice(t *testing.T) {
	t.Parallel()

	mgr := &uploadSessionManager{sessions: make(map[string]*uploadSession)}
	req := &chunkUpload{UploadID: "test-" + uuid.NewString(), TotalChunks: 1}

	if _, err := mgr.getOrCreate(req, ""); err == nil {
		t.Fatal("expected empty device ID to fail")
	} else if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkerConfigFreshIdentityAndLanguage(t *testing.T) {
	dsn := os.Getenv("STANDALONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable initialized database")
	}
	t.Setenv("TELEGRAM_TOKEN_SECRET", "local-test-encryption-key-not-production-123")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(3)
	service := services.NewWorkforceTelegramService(db, services.NewWorkforceService(db))
	emp := uuid.New()
	tgID := time.Now().UnixNano()%1000000000 + 3000000000
	_, err = db.Exec(`INSERT INTO employees(id,employee_id,name,status,attendance_mode) VALUES($1,$2,'配置测试',1,'tg_only')`, emp, "config-"+emp.String()[:8])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DELETE FROM employees WHERE id=$1", emp)
	if _, err = service.CreateBinding(context.Background(), emp, tgID, "configtest", "-123"); err != nil {
		t.Fatal(err)
	}
	h := NewWorkforceTelegramHandler(db, service, nil)
	r := gin.New()
	r.GET("/config", h.WorkerConfig)
	for _, lang := range []string{"en", "vi", "zh"} {
		if err = service.SetEmployeePreferredLang(context.Background(), emp, lang); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		started := time.Now()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/config?chatId=-123&telegramUserId="+strconv.FormatInt(tgID, 10), nil))
		if w.Code != 200 {
			t.Fatalf("config status=%d", w.Code)
		}
		var result struct {
			Code int `json:"code"`
			Data struct {
				EmployeeID    string                 `json:"employeeId"`
				ReplyLang     string                 `json:"replyLang"`
				PreferredLang string                 `json:"preferredLang"`
				ReplyKeyboard map[string]interface{} `json:"replyKeyboard"`
			} `json:"data"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Code != 0 || result.Data.EmployeeID != emp.String() || result.Data.ReplyLang != lang || result.Data.PreferredLang != lang || result.Data.ReplyKeyboard["keyboard"] == nil {
			t.Fatalf("configuration stale/incomplete: %+v", result)
		}
		t.Logf("configuration lang=%s local_elapsed_ms=%d", lang, time.Since(started).Milliseconds())
	}
}
