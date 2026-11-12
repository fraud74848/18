// Consolidated source; original file boundaries are marked below.
package integration_test

import (
	bytes "bytes"
	context "context"
	errors "errors"
	uuid "github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	io "io"
	os "os"
	testing "testing"
	services "tg-attendance/backend/services"
	time "time"
)

// ---- Source: backend/integration/attack_chain_test.go ----
func testAttackChainRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       11,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable: %v", err)
	}
	return client
}
func TestAttackChainRevokeBlocksSessionUploadAndMedia(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("database unavailable: %v", r)
		}
	}()

	db := services.NewDatabase()
	if db == nil {
		t.Fatal("database required")
	}
	defer db.Close()

	redisClient := testAttackChainRedis(t)
	defer redisClient.Close()

	ctx := context.Background()
	svc := services.NewSessionService(db, redisClient)
	mediaSvc := services.NewMediaTokenService(redisClient, svc)
	expires := time.Now().UTC().Add(time.Hour)

	deviceSubject := uuid.New()
	deviceSess, err := svc.CreateSession(ctx, services.SessionSubjectDevice, deviceSubject, expires, "127.0.0.1", "attack-test")
	if err != nil {
		t.Fatalf("create device session: %v", err)
	}

	userSubject := uuid.New()
	userSess, err := svc.CreateSession(ctx, services.SessionSubjectUser, userSubject, expires, "127.0.0.1", "attack-test")
	if err != nil {
		t.Fatalf("create user session: %v", err)
	}

	recID := "rec-attack-" + uuid.NewString()
	token, _, err := mediaSvc.Issue(ctx, services.MediaAccessGrant{
		SessionID:    userSess.ID.String(),
		UserID:       userSubject.String(),
		Username:     "attack-user",
		ResourceType: "recording",
		ResourceID:   recID,
		Action:       "download",
	})
	if err != nil {
		t.Fatalf("issue media token: %v", err)
	}

	if _, err := svc.ValidateActiveSession(ctx, deviceSess.ID.String()); err != nil {
		t.Fatalf("device session should be active: %v", err)
	}

	if err := svc.RevokeSession(ctx, deviceSess.ID.String(), "attack_test"); err != nil {
		t.Fatalf("revoke device session: %v", err)
	}
	if err := svc.RevokeSession(ctx, userSess.ID.String(), "attack_test"); err != nil {
		t.Fatalf("revoke user session: %v", err)
	}

	if _, err := svc.ValidateActiveSession(ctx, deviceSess.ID.String()); !errors.Is(err, services.ErrSessionRevoked) {
		t.Fatalf("device session should be revoked, got %v", err)
	}
	if _, err := svc.ValidateActiveSession(ctx, userSess.ID.String()); !errors.Is(err, services.ErrSessionRevoked) {
		t.Fatalf("user session should be revoked, got %v", err)
	}

	if _, err := mediaSvc.Validate(ctx, token, "recording", recID, "download"); !errors.Is(err, services.ErrMediaTokenExpired) {
		t.Fatalf("media token should fail after user session revoke, got %v", err)
	}
}
func TestAttackChainWSRevokeDisconnectsBoundConnections(t *testing.T) {
	hub := services.NewWebSocketHub()
	sessionID := uuid.NewString()

	agent := &services.AgentConn{
		DeviceID:  "device-ws-attack",
		SessionID: sessionID,
		Send:      make(chan []byte, 4),
	}
	hub.RegisterAgentForTest(agent)

	admin := services.NewAdminConn(nil)
	admin.SessionID = sessionID
	admin.UserID = "user-ws-attack"
	hub.RegisterAdminForTest(admin)

	hub.DisconnectAgentsBySession(sessionID, "session_revoked")
	hub.DisconnectAdminsBySession(sessionID, "session_revoked")

	if !agent.IsClosed() {
		t.Fatal("agent connection should be closed after session revoke")
	}
	if !admin.IsClosed() {
		t.Fatal("admin connection should be closed after session revoke")
	}
}

// ---- Source: backend/integration/auth_session_test.go ----
func TestValidateActiveSessionAfterRevoke(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("Skipping test because database is unavailable: %v", r)
		}
	}()

	db := services.NewDatabase()
	if db == nil {
		t.Fatal("expected database handle")
	}
	defer db.Close()

	svc := services.NewSessionService(db, nil)
	ctx := context.Background()
	subjectID := uuid.New()
	expiresAt := time.Now().UTC().Add(time.Hour)

	sess, err := svc.CreateSession(ctx, services.SessionSubjectDevice, subjectID, expiresAt, "127.0.0.1", "integration-test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := svc.ValidateActiveSession(ctx, sess.ID.String()); err != nil {
		t.Fatalf("expected active session: %v", err)
	}

	if err := svc.RevokeSession(ctx, sess.ID.String(), "logout"); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	_, err = svc.ValidateActiveSession(ctx, sess.ID.String())
	if !errors.Is(err, services.ErrSessionRevoked) {
		t.Fatalf("expected ErrSessionRevoked after revoke, got %v", err)
	}
}
func TestIsProductionEnv(t *testing.T) {
	t.Setenv("ENV", "production")
	if !services.IsProductionEnv() {
		t.Fatal("expected production env")
	}
	t.Setenv("ENV", "development")
	if services.IsProductionEnv() {
		t.Fatal("expected non-production env")
	}
}
func TestWSTicketRequiredProductionDefault(t *testing.T) {
	t.Setenv("WS_TICKET_REQUIRED", "")
	t.Setenv("ENV", "production")

	if !services.WSTicketRequired() {
		t.Fatal("expected WS ticket required when ENV=production and WS_TICKET_REQUIRED unset")
	}
}
func TestWSTicketRequiredExplicitOverride(t *testing.T) {
	t.Setenv("ENV", "production")
	t.Setenv("WS_TICKET_REQUIRED", "false")

	if services.WSTicketRequired() {
		t.Fatal("expected explicit WS_TICKET_REQUIRED=false to disable requirement")
	}
}
func TestWSTicketRequiredExplicitTrue(t *testing.T) {
	t.Setenv("ENV", "development")
	t.Setenv("WS_TICKET_REQUIRED", "true")

	if !services.WSTicketRequired() {
		t.Fatal("expected WS_TICKET_REQUIRED=true to enable requirement in development")
	}
}

// ---- Source: backend/integration/upload_e2e_test.go ----
func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       13,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("redis unavailable at %s: %v", addr, err)
	}
	return client
}
func TestUploadManifestChunkMergeE2E(t *testing.T) {
	client := testRedis(t)
	defer client.Close()

	root := t.TempDir()
	minio := services.NewLocalMinIOForTests(root)
	chunks := services.NewUploadChunkStore(minio)
	manifest := services.NewUploadManifestStore(client, chunks)
	ctx := context.Background()

	uploadID := "integration-upload-" + time.Now().Format("150405.000")
	deviceID := "device-integration"

	t.Cleanup(func() {
		_ = manifest.Delete(context.Background(), uploadID)
		_ = chunks.DeleteAll(context.Background(), uploadID)
	})

	if err := manifest.Init(ctx, services.UploadManifest{
		UploadID:    uploadID,
		DeviceID:    deviceID,
		FileName:    "sample.webp",
		FileType:    "screenshot",
		TotalChunks: 3,
	}); err != nil {
		t.Fatalf("init manifest: %v", err)
	}

	parts := [][]byte{[]byte("part-a-"), []byte("part-b-"), []byte("part-c")}
	for i, part := range parts {
		if err := chunks.Save(ctx, uploadID, i, part); err != nil {
			t.Fatalf("save chunk %d: %v", i, err)
		}
		if count, err := manifest.RecordChunk(ctx, uploadID, i, int64(len(part)), "chk"); err != nil || count != i+1 {
			t.Fatalf("record chunk %d: count=%d err=%v", i, count, err)
		}
	}

	if _, err := manifest.Verify(ctx, uploadID, deviceID, 3, map[int]string{0: "chk", 1: "chk", 2: "chk"}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	merge := chunks.MergeReader(ctx, uploadID, 3)
	defer merge.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, merge); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if buf.String() != "part-a-part-b-part-c" {
		t.Fatalf("unexpected merged data: %q", buf.String())
	}
}
