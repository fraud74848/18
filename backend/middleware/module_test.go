// Consolidated source; original file boundaries are marked below.
package middleware

import (
	json "encoding/json"
	gin "github.com/gin-gonic/gin"
	http "net/http"
	httptest "net/http/httptest"
	testing "testing"
	models "tg-attendance/backend/models"
	services "tg-attendance/backend/services"
)

// ---- Source: backend/middleware/telegram_worker_test.go ----
func setupTelegramWorkerTestRouter(t *testing.T, secret string) *gin.Engine {
	t.Helper()
	t.Setenv("TELEGRAM_WORKER_SECRET", secret)
	gin.SetMode(gin.TestMode)

	svc := services.NewWorkforceTelegramService(nil, nil)
	r := gin.New()
	api := r.Group("/api/v1")

	// Mirror routes.go: /pair is public; everything else requires TelegramWorkerAuth.
	tw := api.Group("/telegram/worker")
	tw.POST("/pair", func(c *gin.Context) {
		models.Send(c, 200, 0, "success", gin.H{"route": "pair"})
	})

	auth := tw.Group("")
	auth.Use(TelegramWorkerAuth(svc))
	auth.POST("/heartbeat", func(c *gin.Context) {
		models.Send(c, 200, 0, "success", gin.H{"route": "heartbeat"})
	})

	return r
}
func TestTelegramWorkerAuth_ValidSecret(t *testing.T) {
	const secret = "test-worker-secret-ok"
	r := setupTelegramWorkerTestRouter(t, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/worker/heartbeat", nil)
	req.Header.Set("X-Telegram-Worker-Secret", secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}
func TestTelegramWorkerAuth_MissingSecret(t *testing.T) {
	r := setupTelegramWorkerTestRouter(t, "test-worker-secret-ok")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/worker/heartbeat", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	assertAPICode(t, w, 1002)
}
func TestTelegramWorkerAuth_WrongSecret(t *testing.T) {
	r := setupTelegramWorkerTestRouter(t, "test-worker-secret-ok")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/worker/heartbeat", nil)
	req.Header.Set("X-Telegram-Worker-Secret", "wrong-secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	assertAPICode(t, w, 1002)
}
func TestTelegramWorkerAuth_BearerHeader(t *testing.T) {
	const secret = "test-worker-secret-bearer"
	r := setupTelegramWorkerTestRouter(t, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/worker/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with Bearer secret, got %d body=%s", w.Code, w.Body.String())
	}
}
func TestTelegramWorkerPair_NoSecretRequired(t *testing.T) {
	r := setupTelegramWorkerTestRouter(t, "test-worker-secret-ok")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/telegram/worker/pair", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected /pair without secret to remain reachable (200), got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Route string `json:"route"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Route != "pair" {
		t.Fatalf("expected pair stub, got %+v", body)
	}
}
func assertAPICode(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, w.Body.String())
	}
	if body.Code != want {
		t.Fatalf("expected api code %d, got %d body=%s", want, body.Code, w.Body.String())
	}
}
