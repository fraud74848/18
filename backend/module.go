// Consolidated source; original file boundaries are marked below.
package main

import (
	bytes "bytes"
	context "context"
	json "encoding/json"
	gin "github.com/gin-gonic/gin"
	godotenv "github.com/joho/godotenv"
	io "io"
	log "log"
	http "net/http"
	os "os"
	signal "os/signal"
	filepath "path/filepath"
	strconv "strconv"
	strings "strings"
	syscall "syscall"
	handlers "tg-attendance/backend/handlers"
	standalone "tg-attendance/backend/internal/standalone"
	middleware "tg-attendance/backend/middleware"
	models "tg-attendance/backend/models"
	services "tg-attendance/backend/services"
	time "time"
)

// ---- Source: backend/main.go ----
func main() {
	_ = godotenv.Load()
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8080")
	}
	if len(os.Getenv("ADMIN_PASSWORD")) < 6 || len(os.Getenv("TELEGRAM_TOKEN_SECRET")) < 32 {
		log.Fatal("Set ADMIN_PASSWORD (6+ characters) and TELEGRAM_TOKEN_SECRET (32+ characters)")
	}
	os.Setenv("CAPTURE_LINKED_TO_CHECKIN", "false")
	if os.Getenv("ENV") == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	// Bound all legacy HTTP calls; no independent polling process or Redis required.
	http.DefaultClient.Timeout = 20 * time.Second
	db, err := services.OpenAttendanceDatabase()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	middleware.InitPermissionDB(db)
	middleware.InitEmployeeScopeDB(db)
	workforce := services.NewWorkforceService(db)
	tg := services.NewWorkforceTelegramService(db, workforce)
	settings, err := tg.GetSettings(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	changes := map[string]interface{}{"requirePCLogin": false, "captureLinkedToCheckin": false, "botMode": "webhook"}
	public := strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/")
	if public == "" {
		public = strings.TrimRight(os.Getenv("RENDER_EXTERNAL_URL"), "/")
	}
	if public != "" {
		changes["webhookUrl"] = public + "/telegram/webhook"
	}
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		changes["botToken"] = token
	}
	if !settings.HasWebhookSecret {
		changes["regenerateWebhookSecret"] = true
	}
	if _, err = tg.UpdateSettings(context.Background(), changes); err != nil {
		log.Fatal(err)
	}
	tgHandler := handlers.NewWorkforceTelegramHandler(db, tg, nil)
	adminHandler := handlers.NewAdminHandler(db, workforce, services.NewPerformancePushService(db), services.NewPenaltyPushService(db), nil, nil, nil)
	workforceHandler := handlers.NewWorkforceHandler(db, workforce, tg, services.NewUserRepository(db), services.NewDeviceRepository(db), nil)
	handlers.GlobalLogger = handlers.NewOperationLogHandler(db)
	inbox := &standalone.Inbox{DB: db, TG: tg, Wake: make(chan struct{}, 1)}
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 12<<20)
		c.Next()
	})
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.POST("/telegram/webhook", inbox.Accept)
	a := standalone.Auth{DB: db}
	api := r.Group("/api/v1")
	authAPI := api.Group("", standalone.SameOrigin())
	authAPI.GET("/auth/captcha", a.Captcha)
	authAPI.GET("/auth/login-mode", a.LoginMode)
	authAPI.POST("/auth/login", a.Login)
	authAPI.POST("/auth/verify", a.VerifyLogin)
	protected := authAPI.Group("", a.Require(), a.Access())
	protected.POST("/auth/password", a.ChangePassword)
	protected.GET("/standalone/roles", a.Roles)
	protected.POST("/standalone/roles", a.SaveRole)
	protected.PUT("/standalone/roles/:id", a.SaveRole)
	protected.DELETE("/standalone/roles/:id", a.DeleteRole)
	protected.PUT("/standalone/accounts/:id/roles", a.AssignRoles)
	protected.POST("/standalone/accounts/:id/password", a.ResetPassword)
	protected.GET("/standalone/memory", a.Memory)
	protected.POST("/standalone/memory/clean", a.Memory)
	protected.PUT("/standalone/memory/policy", a.Memory)
	protected.POST("/standalone/export-authorize", a.AuthorizeExport)
	protected.GET("/auth/me", a.Me)
	protected.GET("/standalone/accounts", a.Accounts)
	protected.GET("/standalone/login-policy", a.LoginPolicy)
	protected.PUT("/standalone/login-policy", a.LoginPolicy)
	protected.PUT("/standalone/accounts/:id/totp", a.LoginPolicy)
	protected.POST("/standalone/accounts", a.CreateAccount)
	protected.PUT("/standalone/accounts/:id", a.UpdateAccount)
	protected.POST("/standalone/accounts/:id/telegram", a.AccountAttendance)
	protected.POST("/standalone/accounts/:id/reset-daily", a.AccountAttendance)
	protected.POST("/standalone/accounts/:id/bind-code", a.BindCode)
	protected.POST("/standalone/bind-code", a.BindCode)
	protected.POST("/standalone/bind-telegram", a.BindOwnTelegram)
	protected.POST("/auth/logout", a.Logout)
	protected.POST("/standalone/webhook", inbox.RegisterWebhook)
	protected.GET("/standalone/status", inbox.Status)
	protected.GET("/standalone/exports", inbox.Exports)
	protected.GET("/standalone/exports/:filename", inbox.DownloadExport)
	protected.POST("/standalone/inbox/:id/resolve", inbox.Resolve)
	protected.GET("/meta/modules", func(c *gin.Context) {
		models.Send(c, 200, 0, "success", gin.H{"telegramAttendance": true, "telegram": true, "attendance": true, "modules": gin.H{"telegramAttendance": true}})
	})
	protected.GET("/devices", func(c *gin.Context) { models.Send(c, 200, 0, "success", gin.H{"items": []interface{}{}, "total": 0}) })
	admin := protected.Group("/admin")
	registerAdminRoutes(admin, tgHandler, adminHandler, workforceHandler)
	// This deployment always uses webhook and has no PC-agent prerequisite.

	worker := api.Group("/telegram/worker", func(c *gin.Context) {
		started := time.Now()
		c.Request = c.Request.WithContext(services.WithTelegramRequestCache(c.Request.Context()))
		c.Next()
		// auth_ms covers secret validation (which also loads the settings row once
		// for the whole request); handler_ms is everything after it.
		total := time.Since(started)
		auth := total
		if t, ok := c.Get("workerAuthDone"); ok {
			if done, ok := t.(time.Time); ok {
				auth = done.Sub(started)
			}
		}
		log.Printf("[worker] path=%s status=%d auth_ms=%d handler_ms=%d total_ms=%d",
			c.FullPath(), c.Writer.Status(), auth.Milliseconds(), (total - auth).Milliseconds(), total.Milliseconds())
	}, middleware.TelegramWorkerAuth(tg), func(c *gin.Context) {
		c.Set("workerAuthDone", time.Now())
		if raw := c.GetHeader("X-TG-Event-Time"); raw != "" {
			if n, e := strconv.ParseInt(raw, 10, 64); e == nil && n > 0 && n <= time.Now().Add(time.Minute).Unix() {
				c.Request = c.Request.WithContext(services.WithPunchTime(c.Request.Context(), time.Unix(n, 0)))
			}
		}
		c.Next()
	})
	registerWorkerRoutes(worker, tgHandler)
	dist := os.Getenv("FRONTEND_DIST")
	if dist == "" {
		dist = "../frontend/dist"
	}
	r.GET("/avatar", func(c *gin.Context) {
		for _, base := range []string{"image", "../image", dist} {
			for _, ext := range []string{"png", "jpg", "jpeg", "webp"} {
				path := filepath.Join(base, "avatar."+ext)
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					c.File(path)
					return
				}
			}
		}
		c.Status(404)
	})
	r.Static("/assets", filepath.Join(dist, "assets"))
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != "GET" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(404)
			return
		}
		c.File(filepath.Join(dist, "index.html"))
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: ":" + os.Getenv("PORT"), Handler: r, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Fatal(e)
		}
	}()
	go services.StartResourceMaintenance(ctx, db)
	go inbox.Run(ctx)
	inbox.Notify()
	// Timers are best effort while awake; recovery remains available after sleep.
	// Maintenance is serialized by the inbox owner, after queued updates.
	// Registration/reconfiguration is explicit in the admin UI, never reset webhook on each cold start.
	log.Printf("TG Attendance listening on %s", srv.Addr)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
func forceWebhookSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, e := io.ReadAll(c.Request.Body)
		if e != nil {
			c.AbortWithStatus(400)
			return
		}
		var in map[string]interface{}
		if json.Unmarshal(body, &in) != nil || in == nil {
			c.AbortWithStatus(400)
			return
		}
		in["botMode"] = "webhook"
		in["requirePCLogin"] = false
		in["captureLinkedToCheckin"] = false
		public := strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/")
		if public == "" {
			public = strings.TrimRight(os.Getenv("RENDER_EXTERNAL_URL"), "/")
		}
		if public != "" {
			in["webhookUrl"] = public + "/telegram/webhook"
		}
		body, _ = json.Marshal(in)
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}

// ---- Source: backend/standalone_routes.go ----
func registerAdminRoutes(admin *gin.RouterGroup, tg *handlers.WorkforceTelegramHandler, a *handlers.AdminHandler, w *handlers.WorkforceHandler) {
	admin.GET("/employees", middleware.RequireAnyPermission("attendance:view", "salary:view_others", "salary:upload"), a.GetEmployees)
	admin.POST("/employees", middleware.RequirePermission("attendance:edit"), a.CreateEmployee)
	admin.PUT("/employees/:id", middleware.RequirePermission("attendance:edit"), a.UpdateEmployee)
	admin.DELETE("/employees/:id", middleware.RequirePermission("attendance:edit"), a.DeleteEmployee)
	admin.POST("/employee-accounts", middleware.RequirePermission("attendance:edit"), w.CreateEmployeeAccount)
	admin.POST("/employees/:id/disable", middleware.RequirePermission("attendance:edit"), w.DisableEmployee)
	admin.GET("/invite-codes", middleware.RequirePermission("invite:view"), w.ListInviteCodes)
	admin.GET("/invite-codes/settings", middleware.RequirePermission("invite:view"), w.GetInviteCodeSettings)
	admin.PUT("/invite-codes/settings", middleware.RequirePermission("invite:manage"), w.UpdateInviteCodeSettings)
	admin.GET("/invite-codes/history", middleware.RequirePermission("invite:view"), w.ListInviteCodeHistory)
	admin.POST("/invite-codes/:id/revoke", middleware.RequirePermission("invite:manage"), w.RevokeInviteCode)
	admin.POST("/employees/:id/invite-code/regenerate", middleware.RequirePermission("invite:manage"), w.RegenerateInviteCode)
	admin.GET("/attendance/records", middleware.RequirePermission("attendance:view"), a.GetAttendanceRecords)
	admin.POST("/attendance/records", middleware.RequirePermission("attendance:edit"), a.SaveAttendanceRecords)
	admin.POST("/attendance/records/import", middleware.RequirePermission("attendance:edit"), w.ImportAttendanceRecords)
	admin.GET("/attendance/today-absentees", middleware.RequirePermission("attendance:view"), a.GetTodayAbsentees)
	admin.GET("/attendance/today-rate", middleware.RequirePermission("attendance:view"), a.GetTodayAttendanceRate)
	admin.GET("/workforce/telegram/settings", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.GetSettings)
	admin.PUT("/workforce/telegram/settings", middleware.RequirePermission("telegram:edit"), forceWebhookSettings(), tg.UpdateSettings)
	admin.POST("/workforce/telegram/settings/regenerate-webhook-secret", middleware.RequirePermission("telegram:edit"), tg.RegenerateWebhookSecret)
	admin.GET("/workforce/telegram/worker-connection", middleware.RequirePermission("telegram:view"), tg.GetWorkerConnection)
	admin.GET("/workforce/telegram/groups/discover", middleware.RequirePermission("telegram:edit"), tg.DiscoverGroups)
	admin.GET("/workforce/telegram/groups", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.ListGroups)
	admin.POST("/workforce/telegram/groups", middleware.RequirePermission("telegram:edit"), tg.UpsertGroup)
	admin.DELETE("/workforce/telegram/groups/:id", middleware.RequirePermission("telegram:edit"), tg.DeleteGroup)
	admin.GET("/workforce/telegram/bindings", middleware.RequirePermission("telegram:view"), tg.ListBindings)
	admin.POST("/workforce/telegram/bindings", middleware.RequirePermission("telegram:edit"), tg.CreateBinding)
	admin.DELETE("/workforce/telegram/bindings/:id", middleware.RequirePermission("telegram:edit"), tg.DeleteBinding)
	admin.POST("/workforce/telegram/bindings/:id/reset-daily", middleware.RequirePermission("telegram:edit"), tg.ResetBindingDaily)
	admin.POST("/workforce/telegram/reset-daily", middleware.RequirePermission("telegram:edit"), tg.ResetEmployeeDaily)
	admin.GET("/workforce/telegram/activity-types", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.ListActivityTypes)
	admin.PUT("/workforce/telegram/activity-types", middleware.RequirePermission("telegram:edit"), tg.UpsertActivityType)
	admin.DELETE("/workforce/telegram/activity-types/:code", middleware.RequirePermission("telegram:edit"), tg.DeleteActivityType)
	admin.GET("/workforce/telegram/activity-fines", middleware.RequirePermission("telegram:view"), tg.ListActivityFines)
	admin.POST("/workforce/telegram/activity-fines/batch", middleware.RequirePermission("telegram:edit"), tg.BatchUpsertActivityFines)
	admin.POST("/workforce/telegram/activity-fines", middleware.RequirePermission("telegram:edit"), tg.UpsertActivityFine)
	admin.DELETE("/workforce/telegram/activity-fines/:id", middleware.RequirePermission("telegram:edit"), tg.DeleteActivityFine)
	admin.GET("/workforce/telegram/activity-sessions", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.ListActivitySessions)
	admin.GET("/workforce/telegram/work-sessions", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.ListWorkSessions)
	admin.GET("/workforce/telegram/handovers", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.ListHandovers)
	admin.GET("/workforce/telegram/rankings", middleware.RequireAnyPermission("telegram:view", "attendance:view"), tg.ListRankings)
	admin.GET("/workforce/telegram/export", middleware.RequirePermission("telegram:view"), tg.ExportAttendance)
	admin.POST("/workforce/telegram/daily-reset", middleware.RequirePermission("telegram:edit"), tg.RunDailyReset)
	admin.GET("/workforce/telegram/handover-config", middleware.RequirePermission("telegram:view"), tg.GetHandoverConfig)
	admin.PUT("/workforce/telegram/handover-config", middleware.RequirePermission("telegram:edit"), tg.UpsertHandoverConfig)
	admin.GET("/workforce/telegram/push-settings", middleware.RequirePermission("telegram:view"), tg.GetPushSettings)
	admin.PUT("/workforce/telegram/push-settings", middleware.RequirePermission("telegram:edit"), tg.UpsertPushSettings)
	admin.POST("/workforce/telegram/push-settings/test", middleware.RequirePermission("telegram:edit"), tg.TestPushSettings)
	admin.GET("/workforce/telegram/attendance-review/settings", middleware.RequireAnyPermission("attendance_review:view", "telegram:view", "attendance:view"), tg.GetAttendanceReviewSettings)
	admin.PUT("/workforce/telegram/attendance-review/settings", middleware.RequireAnyPermission("attendance_review:edit", "telegram:edit"), tg.SaveAttendanceReviewSettings)
	admin.GET("/workforce/telegram/attendance-review/tasks", middleware.RequireAnyPermission("attendance_review:view", "telegram:view", "attendance:view"), tg.ListAttendanceReviewTasks)
	admin.POST("/workforce/telegram/attendance-review/tasks/:id/confirm", middleware.RequireAnyPermission("attendance_review:edit", "attendance:edit"), tg.ConfirmAttendanceReviewTask)
	admin.POST("/workforce/telegram/punch/checkin", middleware.RequirePermission("punch:edit"), tg.AdminPunchCheckIn)
	admin.POST("/workforce/telegram/punch/checkout", middleware.RequirePermission("punch:edit"), tg.AdminPunchCheckOut)
	admin.POST("/workforce/telegram/punch/activity/start", middleware.RequirePermission("punch:edit"), tg.AdminPunchActivityStart)
	admin.POST("/workforce/telegram/punch/activity/back", middleware.RequirePermission("punch:edit"), tg.AdminPunchActivityBack)
	admin.GET("/workforce/telegram/punch/usage", tg.PunchActivityUsage)
	admin.GET("/workforce/telegram/punch/me", middleware.RequireAnyPermission("attendance:view", "attendance:edit", "telegram:view"), tg.GetAdminPunchMe)
	admin.GET("/workforce/telegram/work-fines", middleware.RequirePermission("telegram:view"), tg.ListWorkFines)
	admin.POST("/workforce/telegram/work-fines/batch", middleware.RequirePermission("telegram:edit"), tg.BatchUpsertWorkFines)
	admin.POST("/workforce/telegram/work-fines", middleware.RequirePermission("telegram:edit"), tg.UpsertWorkFine)
	admin.DELETE("/workforce/telegram/work-fines/:id", middleware.RequirePermission("telegram:edit"), tg.DeleteWorkFine)
	admin.GET("/penalty/push-settings", middleware.RequirePermission("attendance:view"), a.GetPenaltyPushSettings)
	admin.PUT("/penalty/push-settings/enabled", middleware.RequirePermission("attendance:edit"), a.SavePenaltyPushEnabled)
	admin.PUT("/penalty/push-settings/token", middleware.RequirePermission("attendance:edit"), a.SavePenaltyPushToken)
	admin.POST("/penalty/push-settings/discover", middleware.RequirePermission("attendance:edit"), a.DiscoverPenaltyPushChats)
	admin.PUT("/penalty/push-settings/bind", middleware.RequirePermission("attendance:edit"), a.BindPenaltyPushChat)
	admin.DELETE("/penalty/push-settings/bind", middleware.RequirePermission("attendance:edit"), a.UnbindPenaltyPushChat)
	admin.POST("/penalty/push-settings/test", middleware.RequirePermission("attendance:edit"), a.TestPenaltyPush)
	admin.POST("/penalty/push-notify", middleware.RequirePermission("attendance:edit"), a.NotifyPenaltyPush)
	admin.GET("/penalty/records", middleware.RequirePermission("attendance:view"), a.GetPenaltyRecords)
	admin.POST("/penalty/record", middleware.RequirePermission("attendance:edit"), a.CreatePenaltyRecord)
	admin.PUT("/penalty/records/:id", middleware.RequirePermission("attendance:edit"), a.UpdatePenaltyRecord)
	admin.DELETE("/penalty/records/:id", middleware.RequirePermission("attendance:edit"), a.DeletePenaltyRecord)
	admin.GET("/performance", middleware.RequirePermission("attendance:view"), a.GetPerformance)
	admin.POST("/performance/batch", middleware.RequirePermission("attendance:edit"), a.BatchSavePerformance)
	admin.GET("/performance/rule-settings", middleware.RequirePermission("attendance:view"), a.GetPerformanceRuleSettings)
	admin.PUT("/performance/rule-settings", middleware.RequirePermission("attendance:edit"), a.SavePerformanceRuleSettings)
	admin.PUT("/performance/:employeeId/:month", middleware.RequirePermission("attendance:edit"), a.UpdatePerformance)
	admin.DELETE("/performance/:employeeId/:month", middleware.RequirePermission("attendance:edit"), a.DeletePerformance)
	admin.GET("/performance/push-settings", middleware.RequirePermission("attendance:view"), a.GetPerformancePushSettings)
	admin.PUT("/performance/push-settings/enabled", middleware.RequirePermission("attendance:edit"), a.SavePerformancePushEnabled)
	admin.PUT("/performance/push-settings/token", middleware.RequirePermission("attendance:edit"), a.SavePerformancePushToken)
	admin.POST("/performance/push-settings/discover", middleware.RequirePermission("attendance:edit"), a.DiscoverPerformancePushChats)
	admin.PUT("/performance/push-settings/bind", middleware.RequirePermission("attendance:edit"), a.BindPerformancePushChat)
	admin.DELETE("/performance/push-settings/bind", middleware.RequirePermission("attendance:edit"), a.UnbindPerformancePushChat)
	admin.POST("/performance/push-settings/test", middleware.RequirePermission("attendance:edit"), a.TestPerformancePush)
	admin.POST("/performance/push-notify", middleware.RequirePermission("attendance:edit"), a.NotifyPerformancePush)
}
func registerWorkerRoutes(worker *gin.RouterGroup, tg *handlers.WorkforceTelegramHandler) {
	worker.POST("/heartbeat", tg.WorkerHeartbeat)
	worker.GET("/config", tg.WorkerConfig)
	worker.GET("/status", tg.WorkerStatus)
	worker.GET("/punch-devices", tg.WorkerPunchDevices)
	worker.POST("/bind", tg.WorkerBind)
	worker.POST("/bind-workstation", tg.WorkerBindWorkstation)
	worker.POST("/bind-device", tg.WorkerBindDevice)
	worker.POST("/checkin", tg.WorkerCheckIn)
	worker.POST("/checkout", tg.WorkerCheckOut)
	worker.POST("/activity/start", tg.WorkerStartActivity)
	worker.POST("/activity/end", tg.WorkerEndActivity)
	worker.POST("/activity/callback-back", tg.WorkerCallbackQuickBack)
	worker.POST("/attendance-review/callback", tg.WorkerAttendanceReviewCallback)
	worker.POST("/activity/message-id", tg.WorkerActivityMessageID)
	worker.POST("/group/register", tg.WorkerRegisterGroup)
	worker.POST("/chat/seen", tg.WorkerRecordChatSeen)
	worker.GET("/myinfo", tg.WorkerMyInfo)
	worker.POST("/handover", tg.WorkerHandover)
	worker.GET("/rankings", tg.WorkerRankings)
	worker.GET("/export", tg.WorkerExport)
	worker.GET("/monthly-report", tg.WorkerMonthlyReport)
	worker.GET("/actstatus", tg.WorkerActStatus)
	worker.GET("/showsettings", tg.WorkerShowSettings)
	worker.GET("/checkdb", tg.WorkerCheckDB)
	worker.GET("/resolve-ci", tg.WorkerResolveCI)
	worker.POST("/lang", tg.WorkerSetLang)
	worker.POST("/admin/fixmessages", tg.WorkerFixMessages)
}
