// Consolidated source; original file boundaries are marked below.
package services

import (
	"bytes"
	context "context"
	sha256 "crypto/sha256"
	sql "database/sql"
	"database/sql/driver"
	hex "encoding/hex"
	"encoding/json"
	fmt "fmt"
	uuid "github.com/google/uuid"
	"io"
	"log"
	url "net/url"
	os "os"
	"path/filepath"
	"runtime/debug"
	strconv "strconv"
	"strings"
	time "time"
)

// ---- Source: backend/services/punch_clock.go ----
type punchClockKey struct{}

func WithPunchTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, punchClockKey{}, t)
}
func punchNow(ctx context.Context) time.Time {
	if t, ok := ctx.Value(punchClockKey{}).(time.Time); ok {
		return t
	}
	return time.Now()
}

// ---- Source: backend/services/standalone_bind.go ----
func (s *WorkforceTelegramService) bindStandaloneAccount(ctx context.Context, tgID int64, tgName, username, code, chatID string) (*TelegramBindingRow, error) {
	if chatID != fmt.Sprint(tgID) {
		return nil, fmt.Errorf("请私聊机器人发送绑定命令")
	}
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	var id string
	// Atomic consumption prevents concurrent reuse; failed binding requires a new code.
	err := s.db.QueryRowContext(ctx, `DELETE FROM standalone_bind_codes b USING users u, standalone_subaccounts sa WHERE b.user_id=u.id AND sa.user_id=u.id AND u.username=$1 AND u.status=1 AND b.code_hash=$2 AND b.expires_at>NOW() RETURNING u.id`, username, hash).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("绑定码无效或已过期，请登录后台重新生成")
	}
	return s.BindTelegramByUsername(ctx, tgID, tgName, username, chatID)
}

// Existing TG ownership is immutable via self-service, including concurrent claims.
func (s *WorkforceTelegramService) createSelfBinding(ctx context.Context, employeeID uuid.UUID, tgID int64, username, chatID string) (*TelegramBindingRow, error) {
	var id uuid.UUID
	err := s.db.QueryRowContext(ctx, `INSERT INTO employee_telegram_bindings(employee_id,telegram_user_id,telegram_username,chat_id,status)
 SELECT e.id,$2,$3,$4,1 FROM employees e JOIN users u ON u.id=e.user_id WHERE e.id=$1 AND e.status=1 AND u.status=1
 ON CONFLICT(employee_id) DO UPDATE SET telegram_username=EXCLUDED.telegram_username,chat_id=EXCLUDED.chat_id,status=1
 WHERE employee_telegram_bindings.telegram_user_id=EXCLUDED.telegram_user_id RETURNING id`, employeeID, tgID, username, chatID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("账号不可用或已绑定其他 Telegram，请联系管理员")
	}
	if err != nil {
		return nil, err
	}
	return s.GetBindingByID(ctx, id)
}

// ---- Source: backend/services/standalone_database.go ----
// OpenAttendanceDatabase is the only database bootstrap used by the standalone binary.
// A dedicated empty database is required; monitor databases cannot be upgraded accidentally.
func OpenAttendanceDatabase() (*sql.DB, error) {
	raw, err := attendanceDatabaseURL(os.Getenv("DATABASE_URL"), os.Getenv("ENV"), os.Getenv("DATABASE_TLS_MODE"), os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", raw)
	if err != nil {
		return nil, err
	}
	configureAttendanceDatabasePool(db)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fail := func(err error) (*sql.DB, error) { db.Close(); return nil, err }
	if err = db.PingContext(ctx); err != nil {
		return fail(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock(84392701)"); err != nil {
		return fail(err)
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(84392701)")
	var marker, users bool
	if err = conn.QueryRowContext(ctx, "SELECT to_regclass('public.standalone_schema') IS NOT NULL, to_regclass('public.users') IS NOT NULL").Scan(&marker, &users); err != nil {
		return fail(err)
	}
	if users && !marker {
		return fail(fmt.Errorf("refusing an existing non-standalone database; create a dedicated empty Aiven database"))
	}
	if _, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS standalone_schema(version integer PRIMARY KEY, installed_at timestamptz DEFAULT NOW());`); err != nil {
		return fail(err)
	}
	var version int
	_ = conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM standalone_schema").Scan(&version)
	if version < 1 {
		for _, migrate := range []func(*sql.DB) error{initEmbeddedSchema, migrateSchema, ensureAuthSessionsSchema, ensureWorkforceSchema, ensureWorkforceTelegramSchema, seedDefaultAdmin} {
			if err = migrate(db); err != nil {
				return fail(err)
			}
		}
		if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS standalone_sessions(token_hash text PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL);
   CREATE TABLE IF NOT EXISTS standalone_login_attempts(bucket text PRIMARY KEY,attempts integer NOT NULL DEFAULT 0,expires_at timestamptz NOT NULL);
   CREATE TABLE IF NOT EXISTS standalone_inbox(update_id bigint PRIMARY KEY,body jsonb NOT NULL,status text NOT NULL DEFAULT 'pending',received_at timestamptz NOT NULL DEFAULT NOW(),started_at timestamptz,finished_at timestamptz,note text NOT NULL DEFAULT '');
   CREATE INDEX IF NOT EXISTS standalone_inbox_pending ON standalone_inbox(status,update_id);
   INSERT INTO standalone_schema(version) VALUES(1) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 2 {
		if _, err = db.Exec(`ALTER TABLE employees ALTER COLUMN attendance_mode SET DEFAULT 'tg_only'; UPDATE employees SET attendance_mode='tg_only' WHERE attendance_mode='pc_tg'; INSERT INTO standalone_schema(version) VALUES(2) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 3 {
		if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS standalone_exports(filename text PRIMARY KEY,content bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT NOW()); INSERT INTO standalone_schema(version) VALUES(3) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 4 {
		if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS standalone_subaccounts(user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, created_at timestamptz NOT NULL DEFAULT NOW());
CREATE TABLE IF NOT EXISTS standalone_bind_codes(user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,code_hash text NOT NULL,expires_at timestamptz NOT NULL);
INSERT INTO roles(name,permissions) VALUES('attendance_member','["attendance:view","telegram:view"]') ON CONFLICT(name) DO NOTHING;
INSERT INTO standalone_schema(version) VALUES(4) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 5 {
		if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS standalone_member_notices(chat_id bigint NOT NULL,user_id bigint NOT NULL,event_unix bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT NOW(),PRIMARY KEY(chat_id,user_id)); INSERT INTO standalone_schema(version) VALUES(5) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 6 {
		if _, err = db.Exec(`
ALTER TABLE work_sessions ADD COLUMN IF NOT EXISTS late_minutes INTEGER NOT NULL DEFAULT 0;
INSERT INTO standalone_subaccounts(user_id)
SELECT DISTINCT e.user_id FROM employees e JOIN users u ON u.id=e.user_id
WHERE NOT EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.name IN ('管理员','admin'))
ON CONFLICT DO NOTHING;
INSERT INTO user_roles(user_id,role_id)
SELECT sa.user_id,r.id FROM standalone_subaccounts sa CROSS JOIN roles r WHERE r.name='attendance_member'
ON CONFLICT DO NOTHING;
INSERT INTO standalone_schema(version) VALUES(6) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 7 {
		if _, err = db.Exec(`UPDATE roles SET permissions='["attendance:view","records:view","punch:view","punch:edit","penalty:view"]'::jsonb WHERE name='attendance_member'; INSERT INTO standalone_schema(version) VALUES(7) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 8 {
		// 角色精简：移除 attendance_member / 审计员 / 操作员，子账号统一使用「查看员」（只读：
		// 考勤月历、打卡记录、后台打卡、罚款详情）。原持有旧角色的子账号迁移为查看员，再删除旧角色。
		if _, err = db.Exec(`
INSERT INTO roles(name,description,permissions) VALUES('查看员','只读：打卡记录、后台打卡、考勤月历、罚款详情','["attendance:view","records:view","punch:view","penalty:view"]'::jsonb)
ON CONFLICT(name) DO UPDATE SET permissions=EXCLUDED.permissions, description=EXCLUDED.description, updated_at=NOW();
INSERT INTO user_roles(user_id,role_id)
SELECT DISTINCT ur.user_id,v.id FROM user_roles ur
JOIN roles old ON old.id=ur.role_id AND old.name IN ('attendance_member','审计员','操作员')
JOIN standalone_subaccounts sa ON sa.user_id=ur.user_id
CROSS JOIN roles v WHERE v.name='查看员'
ON CONFLICT DO NOTHING;
DELETE FROM user_roles WHERE role_id IN (SELECT id FROM roles WHERE name IN ('attendance_member','审计员','操作员'));
DELETE FROM roles WHERE name IN ('attendance_member','审计员','操作员');
INSERT INTO standalone_schema(version) VALUES(8) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}
	if version < 9 {
		if _, err = db.Exec(`ALTER TABLE standalone_sessions ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;
INSERT INTO standalone_schema(version) VALUES(9) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}

	if version < 10 {
		if _, err = db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS standalone_totp_required boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS standalone_totp_secret text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS standalone_totp_step bigint NOT NULL DEFAULT -1;
CREATE TABLE IF NOT EXISTS standalone_login_challenges(id_hash text PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,kind text NOT NULL,answer_hash text NOT NULL DEFAULT '',secret_enc text NOT NULL DEFAULT '',password_hash text NOT NULL,expires_at timestamptz NOT NULL,attempts integer NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS standalone_login_challenges_expiry ON standalone_login_challenges(expires_at);
INSERT INTO system_settings(key,value) VALUES('standalone_totp_global','false') ON CONFLICT DO NOTHING;
INSERT INTO standalone_schema(version) VALUES(10) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}

	if version < 11 {
		if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS standalone_login_captchas(id_hash text PRIMARY KEY,answer_hash text NOT NULL,expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS standalone_login_captchas_expiry ON standalone_login_captchas(expires_at);
INSERT INTO standalone_schema(version) VALUES(11) ON CONFLICT DO NOTHING;`); err != nil {
			return fail(err)
		}
	}

	return db, nil
}

// Retain the bounded pool across punches. Keeping only one idle connection
// discarded parallel config/lock connections and forced repeated TLS handshakes.
func configureAttendanceDatabasePool(db *sql.DB) {
	max := 5
	if n, e := strconv.Atoi(os.Getenv("DB_MAX_CONNECTIONS")); e == nil && n >= 3 && n <= 10 {
		max = n
	}
	db.SetMaxOpenConns(max)
	db.SetMaxIdleConns(max)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
}

// ---- Source: backend/services/standalone_maintenance.go ----
// Called by the single inbox owner AFTER queued punches are drained. No competing
// scheduler can auto-close an activity before a delayed return-to-seat is handled.
func (s *WorkforceTelegramService) StandaloneMaintenance(ctx context.Context, full bool) {
	active, err := s.IsActive(ctx)
	if err != nil || !active {
		return
	}
	s.tickActivityTimers(ctx)
	s.MaybeRunDailyReset(ctx)
	if full {
		s.RecoverMissedDailyResets(ctx)
		s.RecoverExpiredActivities(ctx)
		s.CloseExpiredWorkSessionsGlobal(ctx)
		s.MaybePushMonthlyArchive(ctx)
		_ = s.CleanupTelegramRetention(ctx)

	}
}

// A maintenance deadline bounds database waits on a constrained free instance.
func StandaloneMaintenanceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 45*time.Second)
}

// Public databases always use verified TLS in production. Plaintext is limited
// to the explicitly selected db service on an isolated Compose network.
func attendanceDatabaseURL(raw, environment, mode, ca string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("DATABASE_URL must be a PostgreSQL URI")
	}
	q := u.Query()
	if mode == "internal" {
		if u.Hostname() != "db" || (u.Port() != "" && u.Port() != "5432") {
			return "", fmt.Errorf("internal database mode only allows the Compose db:5432 service")
		}
		u.Host = "db:5432"
		// Do not allow query options to override host/port or select a libpq service.
		q = url.Values{"sslmode": []string{"disable"}}
	} else {
		if mode != "" && mode != "verify-full" {
			return "", fmt.Errorf("DATABASE_TLS_MODE must be internal or verify-full")
		}
		if environment == "production" || mode == "verify-full" {
			if ca == "" {
				return "", fmt.Errorf("PGSSLROOTCERT must point to the external database CA certificate")
			}
			q.Set("sslmode", "verify-full")
			q.Set("sslrootcert", ca)
		}
	}
	q.Set("connect_timeout", "10")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Resource housekeeping only touches explicitly disposable application data.
// PostgreSQL files, uploads, core attendance and durable exports are never targets.
type ResourcePolicy struct {
	Auto               bool  `json:"auto"`
	DatabaseQuotaBytes int64 `json:"databaseQuotaBytes"`
	HistoryDays        int   `json:"historyDays"`
	LogDays            int   `json:"logDays"`
	TempHours          int   `json:"tempHours"`
}
type ResourceSnapshot struct {
	At              time.Time `json:"at"`
	DiskPath        string    `json:"diskPath"`
	DiskTotal       int64     `json:"diskTotal"`
	DiskAvailable   int64     `json:"diskAvailable"`
	DiskPercent     float64   `json:"diskPercent"`
	DatabaseBytes   int64     `json:"databaseBytes"`
	DatabasePercent *float64  `json:"databasePercent"`
	Alerts          []string  `json:"alerts"`
}
type ResourceRun struct {
	ID                       string           `json:"id"`
	Trigger                  string           `json:"trigger"`
	StartedAt                time.Time        `json:"startedAt"`
	FinishedAt               time.Time        `json:"finishedAt"`
	Status                   string           `json:"status"`
	Before                   ResourceSnapshot `json:"before"`
	After                    ResourceSnapshot `json:"after"`
	DeletedRows              int64            `json:"deletedRows"`
	DeletedTempBytes         int64            `json:"deletedTempBytes"`
	ObservedDiskReleased     int64            `json:"observedDiskReleased"`
	ObservedDatabaseReleased int64            `json:"observedDatabaseReleased"`
	Steps                    []string         `json:"steps"`
	Errors                   []string         `json:"errors"`
}

func LoadResourcePolicy(ctx context.Context, db *sql.DB) (ResourcePolicy, error) {
	p := ResourcePolicy{Auto: true, HistoryDays: 30, LogDays: 180, TempHours: 72}
	if n, e := strconv.ParseInt(os.Getenv("DATABASE_QUOTA_BYTES"), 10, 64); e == nil && n > 0 {
		p.DatabaseQuotaBytes = n
	}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key='resource_cleanup_policy'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	return p, validateResourcePolicy(p)
}
func validateResourcePolicy(p ResourcePolicy) error {
	if p.DatabaseQuotaBytes < 0 || p.HistoryDays < 30 || p.HistoryDays > 3650 || p.LogDays < 90 || p.LogDays > 3650 || p.TempHours < 24 || p.TempHours > 8760 {
		return fmt.Errorf("保留策略无效：历史日志至少30天、操作日志至少90天、临时副本至少24小时，数据库配额不能为负")
	}
	return nil
}
func SaveResourcePolicy(ctx context.Context, db *sql.DB, p ResourcePolicy) error {
	if err := validateResourcePolicy(p); err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	_, err := db.ExecContext(ctx, `INSERT INTO system_settings(key,value) VALUES('resource_cleanup_policy',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, string(raw))
	return err
}
func InspectResources(ctx context.Context, db *sql.DB, p ResourcePolicy) ResourceSnapshot {
	snapshot := ResourceSnapshot{At: time.Now(), DiskPath: os.Getenv("RESOURCE_DISK_PATH"), Alerts: []string{}}
	if snapshot.DiskPath == "" {
		snapshot.DiskPath = "."
	}
	total, available, err := getDiskStats(snapshot.DiskPath)
	if err != nil {
		snapshot.Alerts = append(snapshot.Alerts, "服务器磁盘容量无法读取")
	} else if total > 0 {
		snapshot.DiskTotal = total
		snapshot.DiskAvailable = available
		snapshot.DiskPercent = float64(total-available) / float64(total) * 100
		if snapshot.DiskPercent > 80 {
			snapshot.Alerts = append(snapshot.Alerts, "服务器磁盘使用率超过80%")
		}
	}
	if err = db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&snapshot.DatabaseBytes); err != nil {
		snapshot.Alerts = append(snapshot.Alerts, "数据库容量无法读取")
	} else if p.DatabaseQuotaBytes > 0 {
		ratio := float64(snapshot.DatabaseBytes) / float64(p.DatabaseQuotaBytes) * 100
		snapshot.DatabasePercent = &ratio
		if ratio > 80 {
			snapshot.Alerts = append(snapshot.Alerts, "当前数据库占配置配额超过80%")
		}
	}
	if p.DatabaseQuotaBytes == 0 {
		snapshot.Alerts = append(snapshot.Alerts, "未设置外部数据库配额，无法判断其80%阈值；不会猜测供应商磁盘容量")
	}
	return snapshot
}
func resourcePressure(s ResourceSnapshot) bool {
	return s.DiskPercent > 80 || s.DatabasePercent != nil && *s.DatabasePercent > 80
}
func resourceLog(ctx context.Context, db *sql.DB, kind string, run ResourceRun) error {
	raw, _ := json.Marshal(run)
	_, err := db.ExecContext(ctx, `INSERT INTO operation_logs(operator_name,operator_role,operation_type,operation_module,operation_desc,target_id) VALUES('system','system',$1,'RESOURCE_MAINTENANCE',$2,$3)`, kind, string(raw), run.ID)
	return err
}
func ResourceHistory(ctx context.Context, db *sql.DB, limit, offset int) ([]ResourceRun, error) {
	rows, err := db.QueryContext(ctx, `SELECT operation_desc FROM operation_logs WHERE operation_module='RESOURCE_MAINTENANCE' ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResourceRun{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var run ResourceRun
		if json.Unmarshal([]byte(raw), &run) == nil {
			out = append(out, run)
		}
	}
	return out, rows.Err()
}

// Delete only old transport copies whose bytes still match an existing durable
// database backup. Never recurse, follow symlinks, or accept arbitrary filenames.
func cleanupExportScratch(ctx context.Context, db *sql.DB, p ResourcePolicy) (int64, error) {
	dir := telegramResetExportDir()
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("临时导出目录不是安全的普通目录")
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return 0, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return 0, err
	}
	if filepath.Clean(real) != filepath.Clean(abs) {
		return 0, fmt.Errorf("临时导出路径含符号链接，跳过文件清理")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var released int64
	checked := 0
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return released, err
		}
		if checked >= 100 {
			break
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "telegram_attendance_") || !strings.HasSuffix(name, ".xlsx") || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, name)
		stat, err := os.Lstat(path)
		if err != nil {
			return released, err
		}
		if !stat.Mode().IsRegular() || stat.Size() > 67108864 || stat.ModTime().After(time.Now().Add(-time.Duration(p.TempHours)*time.Hour)) {
			continue
		}
		checked++
		// A byte-for-byte hash comparison avoids treating a stale filename as a backup.
		var backup []byte
		if err = db.QueryRowContext(ctx, `SELECT content FROM standalone_exports WHERE filename=$1 AND octet_length(content)<=67108864 LIMIT 1`, name).Scan(&backup); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return released, err
		}
		f, err := os.Open(path)
		if err != nil {
			return released, err
		}
		opened, err := f.Stat()
		if err != nil || !os.SameFile(stat, opened) {
			f.Close()
			continue
		}
		hash := sha256.New()
		_, err = io.Copy(hash, f)
		f.Close()
		if err != nil {
			return released, err
		}
		sum := sha256.Sum256(backup)
		if !bytes.Equal(hash.Sum(nil), sum[:]) {
			continue
		}
		again, err := os.Lstat(path)
		if err != nil || !os.SameFile(stat, again) || again.Size() != stat.Size() || !again.ModTime().Equal(stat.ModTime()) {
			continue
		}
		if err = os.Remove(path); err != nil {
			return released, err
		}
		released += stat.Size()
	}
	return released, nil
}
func CleanResources(ctx context.Context, db *sql.DB, trigger string) (ResourceRun, error) {
	run := ResourceRun{ID: uuid.NewString(), Trigger: trigger, StartedAt: time.Now(), Status: "running", Steps: []string{}, Errors: []string{}}
	policy, err := LoadResourcePolicy(ctx, db)
	if err != nil {
		return run, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return run, err
	}
	defer conn.Close()
	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(84392704)`).Scan(&acquired); err != nil {
		return run, err
	}
	if !acquired {
		return run, fmt.Errorf("已有清理任务正在执行")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, e := conn.ExecContext(releaseCtx, `SELECT pg_advisory_unlock(84392704)`); e != nil {
			_ = conn.Raw(func(interface{}) error { return driver.ErrBadConn })
		}
	}()
	var previous string
	err = conn.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key='resource_cleanup_last_run'`).Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return run, err
	}
	if at, e := time.Parse(time.RFC3339Nano, previous); e == nil && time.Since(at) < 30*time.Minute {
		return run, fmt.Errorf("清理冷却中：距离上次执行需满30分钟")
	}
	// Persist the attempt before touching data; even interrupted jobs remain visible.
	run.Before = InspectResources(ctx, db, policy)
	if err = resourceLog(ctx, db, "CLEANUP", run); err != nil {
		return run, fmt.Errorf("无法保存执行日志，已取消清理: %w", err)
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO system_settings(key,value) VALUES('resource_cleanup_last_run',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, run.StartedAt.Format(time.RFC3339Nano)); err != nil {
		return run, err
	}
	bytesRemoved, e := cleanupExportScratch(ctx, db, policy)
	run.DeletedTempBytes = bytesRemoved
	if e != nil {
		run.Errors = append(run.Errors, e.Error())
	}
	run.Steps = append(run.Steps, "临时文件：仅清理已有完整数据库副本的过期导出传输文件；保留所有有效备份")
	targets := []struct {
		table, condition string
		args             []interface{}
	}{
		{"standalone_sessions", "expires_at<NOW()", nil},
		{"standalone_login_challenges", "expires_at<NOW()", nil},
		{"standalone_login_captchas", "expires_at<NOW()", nil},
		{"standalone_login_attempts", "expires_at<NOW()", nil},
		{"standalone_bind_codes", "expires_at<NOW()", nil},
		{"standalone_member_notices", "created_at < NOW()-($1 * interval '1 day')", []interface{}{policy.HistoryDays}},
		{"standalone_inbox", "status IN ('done','resolved') AND received_at < NOW()-($1 * interval '1 day')", []interface{}{policy.HistoryDays}},
		{"operation_logs", "operation_module='RESOURCE_MAINTENANCE' AND created_at < NOW()-($1 * interval '1 day')", []interface{}{policy.LogDays}},
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			run.Errors = append(run.Errors, "清理达到时间上限，剩余项目保留至下次")
			break
		}
		// At most 1000 rows per table per run: avoid long transactions and WAL bursts.
		query := `WITH doomed AS (SELECT ctid FROM ` + target.table + ` WHERE ` + target.condition + ` LIMIT 1000 FOR UPDATE SKIP LOCKED) DELETE FROM ` + target.table + ` t USING doomed d WHERE t.ctid=d.ctid`
		stepCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, e := conn.ExecContext(stepCtx, query, target.args...)
		cancel()
		if e != nil {
			run.Errors = append(run.Errors, target.table+": 过期数据清理失败（请检查数据库权限或锁等待）")
			continue
		}
		n, _ := res.RowsAffected()
		run.DeletedRows += n
		run.Steps = append(run.Steps, fmt.Sprintf("%s：删除%d条过期非业务记录", target.table, n))
		// Not VACUUM FULL: no table rewrite or extra full-table disk allocation.
		stepCtx, cancel = context.WithTimeout(ctx, 8*time.Second)
		_, e = conn.ExecContext(stepCtx, `VACUUM (ANALYZE, SKIP_LOCKED, TRUNCATE TRUE) `+target.table)
		cancel()
		if e != nil {
			run.Errors = append(run.Errors, target.table+": VACUUM未完成，请检查维护权限、锁或超时")
		} else {
			run.Steps = append(run.Steps, target.table+"：VACUUM执行结束；内部空闲页可复用，尾部空页仅在可安全截断时归还磁盘")
		}
	}
	debug.FreeOSMemory()
	finalCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	run.After = InspectResources(finalCtx, db, policy)
	run.FinishedAt = time.Now()
	run.Status = "completed"
	if len(run.Errors) > 0 {
		run.Status = "partial"
	}
	if run.Before.DiskTotal > 0 && run.After.DiskTotal > 0 {
		run.ObservedDiskReleased = max(0, run.After.DiskAvailable-run.Before.DiskAvailable)
	}
	if run.Before.DatabaseBytes > 0 && run.After.DatabaseBytes > 0 {
		run.ObservedDatabaseReleased = max(0, run.Before.DatabaseBytes-run.After.DatabaseBytes)
	}
	if resourcePressure(run.After) {
		run.After.Alerts = append(run.After.Alerts, "安全清理后仍超过80%，需要扩容或由数据库管理员检查膨胀、WAL及备份；不会扩大删除范围")
	}
	if run.ObservedDatabaseReleased == 0 {
		run.Steps = append(run.Steps, "当前数据库文件大小未减少；不能把删除行数或VACUUM成功视作物理磁盘空间已释放")
	}
	raw, _ := json.Marshal(run)
	_, err = db.ExecContext(finalCtx, `UPDATE operation_logs SET operation_desc=$2 WHERE operation_module='RESOURCE_MAINTENANCE' AND target_id=$1`, run.ID, string(raw))
	if err != nil {
		return run, fmt.Errorf("清理已执行，但最终日志保存失败，请检查服务日志")
	}
	return run, nil
}
func StartResourceMaintenance(ctx context.Context, db *sql.DB) {
	tick := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		policy, err := LoadResourcePolicy(checkCtx, db)
		if err != nil {
			cancel()
			log.Printf("[resources] policy unavailable: %v", err)
			return
		}
		snapshot := InspectResources(checkCtx, db, policy)
		raw, _ := json.Marshal(snapshot)
		_, err = db.ExecContext(checkCtx, `INSERT INTO system_settings(key,value) VALUES('resource_monitor',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, string(raw))
		cancel()
		if err != nil {
			log.Printf("[resources] monitor persistence failed: %v", err)
		}
		if len(snapshot.Alerts) > 0 {
			log.Printf("[resources] alerts: %s", strings.Join(snapshot.Alerts, "; "))
		}
		if !policy.Auto || !resourcePressure(snapshot) {
			return
		}
		runCtx, done := context.WithTimeout(ctx, 75*time.Second)
		defer done()
		if _, err = CleanResources(runCtx, db, "automatic"); err != nil {
			log.Printf("[resources] cleanup: %v", err)
		}
	}
	tick()
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			tick()
		}
	}
}
