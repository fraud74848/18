// Consolidated source; original file boundaries are marked below.
package services

import (
	context "context"
	rand "crypto/rand"
	sql "database/sql"
	hex "encoding/hex"
	errors "errors"
	fmt "fmt"
	uuid "github.com/google/uuid"
	bcrypt "golang.org/x/crypto/bcrypt"
	sort "sort"
	strconv "strconv"
	strings "strings"
	time "time"
)

// ---- Source: backend/services/workforce.go ----
var (
	ErrInviteNotFound              = errors.New("invite code not found")
	ErrInviteExpired               = errors.New("invite code expired")
	ErrInviteUsed                  = errors.New("invite code already used")
	ErrInviteRevoked               = errors.New("invite code revoked")
	ErrInviteEmployeeMismatch      = errors.New("login employee does not match invite-bound employee")
	ErrEmployeeBound               = errors.New("employee already has a bound device")
	ErrEmployeeOnOtherDevice       = errors.New("employee active on another device")
	ErrNoOpenSession               = errors.New("no open work session")
	ErrSessionExists               = errors.New("work session already open")
	ErrWorkSessionOpenOnPC         = errors.New("work session open on pc")
	ErrWorkSessionOpenOnTG         = errors.New("work session open on telegram")
	ErrEmployeeNotFound            = errors.New("employee not found")
	ErrInvalidCredentials          = errors.New("invalid credentials")
	ErrNotEmployeeRole             = errors.New("not an employee account")
	ErrEmployeeLoginRequired       = errors.New("employee login required")
	ErrEmployeeSessionExpired      = errors.New("employee session expired")
	ErrTelegramDisabled            = errors.New("telegram attendance disabled")
	ErrWebPunchOnlyMode            = errors.New("当前为仅后台打卡模式，请使用网页端打卡")
	ErrTelegramNotBound            = errors.New("telegram user not bound to employee")
	ErrTelegramAlreadyBound        = errors.New("telegram user already bound to another employee")
	ErrOutsideWorkWindow           = errors.New("outside work shift window")
	ErrShiftAlreadyCompleted       = errors.New("shift already completed today")
	ErrOtherWorkSessionOpen        = errors.New("other work session still open")
	ErrOpenActivityBlocksCheckout  = errors.New("open activity blocks checkout")
	ErrDualShiftDisabled           = errors.New("dual shift mode disabled")
	ErrPCLoginRequired             = errors.New("pc agent login required")
	ErrPCLoginMismatch             = errors.New("pc logged in as different employee")
	ErrUnsupportedReplyLang        = errors.New("unsupported lang: use zh, vi, or en")
	ErrCredentialsEmployeeMismatch = errors.New("credentials do not match invite employee")
	ErrWorkstationNotConfigured    = errors.New("shared workstation not configured for employee")
	ErrTelegramBoundToWorkstation  = errors.New("telegram account bound to shared workstation")
	ErrTelegramBoundToPersonal     = errors.New("telegram account bound to personal employee")
	ErrDeviceTelegramAlreadyBound  = errors.New("device already has telegram binding")
	ErrMultipleWorkstationLogins   = errors.New("multiple workstation devices logged in for this telegram")
	ErrWorkstationDeviceRequired   = errors.New("workstation device selection required for punch")
	ErrWorkstationDeviceNotBound   = errors.New("telegram not bound to the selected workstation device")
	ErrWorkstationInviteMismatch   = errors.New("invite employee is not a member of the target device")
	ErrActivityConcurrentLimit     = errors.New("activity concurrent limit reached")
)

const (
	defaultInviteValidDays   = 7
	systemSettingInviteValid = "invite_code_valid_days"
	minInviteValidDays       = 1
	maxInviteValidDays       = 365
)

// InviteCodeSettings 新生成邀请码使用的默认有效期（已发放的码不受影响）。
type InviteCodeSettings struct {
	ValidDays int        `json:"validDays"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

func clampInviteValidDays(days int) int {
	if days < minInviteValidDays {
		return minInviteValidDays
	}
	if days > maxInviteValidDays {
		return maxInviteValidDays
	}
	return days
}
func (s *WorkforceService) InviteValidDays(ctx context.Context) int {
	if s.db == nil {
		return defaultInviteValidDays
	}
	var val sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM system_settings WHERE key = $1`, systemSettingInviteValid).Scan(&val)
	if err != nil || !val.Valid {
		return defaultInviteValidDays
	}
	days, err := strconv.Atoi(strings.TrimSpace(val.String))
	if err != nil {
		return defaultInviteValidDays
	}
	return clampInviteValidDays(days)
}
func (s *WorkforceService) GetInviteCodeSettings(ctx context.Context) (*InviteCodeSettings, error) {
	if s.db == nil {
		d := defaultInviteValidDays
		return &InviteCodeSettings{ValidDays: d}, nil
	}
	var val sql.NullString
	var updatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT value, updated_at FROM system_settings WHERE key = $1`, systemSettingInviteValid,
	).Scan(&val, &updatedAt)
	if err == sql.ErrNoRows {
		d := defaultInviteValidDays
		return &InviteCodeSettings{ValidDays: d}, nil
	}
	if err != nil {
		return nil, err
	}
	days := defaultInviteValidDays
	if val.Valid {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(val.String)); parseErr == nil {
			days = clampInviteValidDays(parsed)
		}
	}
	out := &InviteCodeSettings{ValidDays: days}
	if updatedAt.Valid {
		t := updatedAt.Time
		out.UpdatedAt = &t
	}
	return out, nil
}
func (s *WorkforceService) SaveInviteCodeSettings(ctx context.Context, validDays int) (*InviteCodeSettings, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	validDays = clampInviteValidDays(validDays)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		systemSettingInviteValid, strconv.Itoa(validDays),
	)
	if err != nil {
		return nil, err
	}
	return s.GetInviteCodeSettings(ctx)
}

// WorkforceService HR + invite + attendance orchestration.
type WorkforceService struct {
	db *sql.DB
}

func NewWorkforceService(db *sql.DB) *WorkforceService {
	return &WorkforceService{db: db}
}

type CreateEmployeeAccountInput struct {
	Username     string
	Password     string
	EmployeeID   string
	Name         string
	Position     string
	Department   string
	HireDate     string
	WorkLocation string
	CreatedBy    uuid.UUID
}
type CreateEmployeeAccountResult struct {
	UserID     uuid.UUID
	EmployeeID uuid.UUID
	InviteCode string
	InviteID   uuid.UUID
}

// normalizeLinkedEmployeeCode unifies login username and business employee_id (工号).
// Login account is canonical when both are provided but differ.
func normalizeLinkedEmployeeCode(username, employeeID string) (string, error) {
	username = strings.TrimSpace(username)
	employeeID = strings.TrimSpace(employeeID)
	switch {
	case username == "" && employeeID == "":
		return "", fmt.Errorf("username or employee id required")
	case username == "":
		return employeeID, nil
	case employeeID == "":
		return username, nil
	case username != employeeID:
		return username, nil
	default:
		return username, nil
	}
}
func (s *WorkforceService) CreateEmployeeAccount(ctx context.Context, in *CreateEmployeeAccountInput) (*CreateEmployeeAccountResult, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	loginCode, err := normalizeLinkedEmployeeCode(in.Username, in.EmployeeID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Password) == "" || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("username, password and name are required")
	}
	username := loginCode

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var userID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (id, username, password_hash, email, real_name, status, created_at)
		VALUES (gen_random_uuid(), $1, $2, '', $3, 1, NOW())
		RETURNING id`,
		username, string(hash), in.Name,
	).Scan(&userID)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	var memberRoleID uuid.UUID
	// 考勤月历新增的子账号默认角色：查看员（只读）
	err = tx.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = '查看员' LIMIT 1`).Scan(&memberRoleID)
	if err != nil {
		return nil, fmt.Errorf("viewer role not found: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT (user_id, role_id) DO NOTHING`, userID, memberRoleID); err != nil {
		return nil, err
	}

	if _, err = tx.ExecContext(ctx, `INSERT INTO standalone_subaccounts(user_id) VALUES($1)`, userID); err != nil {
		return nil, err
	}

	empUUID := uuid.New()
	var hireDate interface{}
	if strings.TrimSpace(in.HireDate) != "" {
		hireDate = in.HireDate
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO employees (id, employee_id, name, position, department, hire_date, work_location, user_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, NOW())`,
		empUUID, loginCode, in.Name, nullString(in.Position),
		nullString(in.Department), hireDate, nullString(in.WorkLocation), userID,
	); err != nil {
		return nil, err
	}

	currentMonth := time.Now().Format("2006-01")
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO performance_records (employee_id, employee_name, position, score_records, total_score, grade, month, created_at, updated_at)
		VALUES ($1, $2, $3, '[]'::jsonb, 10, '合格', $4, NOW(), NOW())
		ON CONFLICT (employee_id, month) DO NOTHING`,
		empUUID, in.Name, in.Position, currentMonth,
	); err != nil {
		return nil, err
	}

	if err = s.ensureTodayAttendanceTx(ctx, tx, empUUID, in.Position); err != nil {
		return nil, err
	}

	validDays := s.InviteValidDays(ctx)
	code, inviteID, err := s.createInviteCodeTx(ctx, tx, empUUID, uuidPtrOrNil(in.CreatedBy), validDays)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &CreateEmployeeAccountResult{
		UserID:     userID,
		EmployeeID: empUUID,
		InviteCode: code,
		InviteID:   inviteID,
	}, nil
}
func (s *WorkforceService) ensureTodayAttendanceTx(ctx context.Context, tx *sql.Tx, employeeID uuid.UUID, position string) error {
	today := time.Now().Format("2006-01-02")
	yearMonth := time.Now().Format("2006-01")
	_, err := tx.ExecContext(ctx, `
		INSERT INTO attendance_records (employee_id, year_month, date, status, position_snapshot, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, NOW(), NOW())
		ON CONFLICT (employee_id, year_month, date) DO NOTHING`,
		employeeID, yearMonth, today, nullString(position),
	)
	return err
}
func generateInviteCodeString() (string, error) {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := strings.ToUpper(hex.EncodeToString(b))
	return fmt.Sprintf("%s-%s", raw[:4], raw[4:]), nil
}
func (s *WorkforceService) createInviteCodeTx(ctx context.Context, tx *sql.Tx, employeeID uuid.UUID, createdBy *uuid.UUID, validDays int) (string, uuid.UUID, error) {
	if _, err := tx.ExecContext(ctx, `
		UPDATE device_invite_codes SET revoked_at = NOW(), superseded_at = NOW()
		WHERE employee_id = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()`,
		employeeID); err != nil {
		return "", uuid.Nil, err
	}

	code, err := generateInviteCodeString()
	if err != nil {
		return "", uuid.Nil, err
	}
	id := uuid.New()
	expires := time.Now().Add(time.Duration(validDays) * 24 * time.Hour)
	if createdBy != nil {
		if err = tx.QueryRowContext(ctx, `
			INSERT INTO device_invite_codes (id, code, employee_id, created_by, expires_at, created_at)
			VALUES ($1, $2, $3, $4, $5, NOW()) RETURNING id`,
			id, code, employeeID, *createdBy, expires,
		).Scan(&id); err != nil {
			return "", uuid.Nil, err
		}
	} else if err = tx.QueryRowContext(ctx, `
		INSERT INTO device_invite_codes (id, code, employee_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4, NOW()) RETURNING id`,
		id, code, employeeID, expires,
	).Scan(&id); err != nil {
		return "", uuid.Nil, err
	}
	return code, id, nil
}
func uuidPtrOrNil(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

type InviteCodeRow struct {
	ID           uuid.UUID
	Code         string
	EmployeeID   uuid.UUID
	EmployeeName string
	EmployeeCode string
	ExpiresAt    time.Time
	UsedAt       *time.Time
	RevokedAt    *time.Time
	SupersededAt *time.Time
	CreatedAt    time.Time
	DeviceID     *uuid.UUID
}

func scanInviteCodeRow(rows *sql.Rows) (InviteCodeRow, error) {
	var row InviteCodeRow
	var usedAt, revokedAt, supersededAt sql.NullTime
	var deviceID sql.NullString
	if err := rows.Scan(
		&row.ID, &row.Code, &row.EmployeeID, &row.EmployeeName, &row.EmployeeCode,
		&row.ExpiresAt, &usedAt, &revokedAt, &supersededAt, &row.CreatedAt, &deviceID,
	); err != nil {
		return row, err
	}
	if usedAt.Valid {
		row.UsedAt = &usedAt.Time
	}
	if revokedAt.Valid {
		row.RevokedAt = &revokedAt.Time
	}
	if supersededAt.Valid {
		row.SupersededAt = &supersededAt.Time
	}
	if deviceID.Valid {
		if did, err := uuid.Parse(deviceID.String); err == nil {
			row.DeviceID = &did
		}
	}
	return row, nil
}

const inviteCodeSelectCols = `
	ic.id, ic.code, ic.employee_id, COALESCE(e.name,''), COALESCE(e.employee_id,''),
	ic.expires_at, ic.used_at, ic.revoked_at, ic.superseded_at, ic.created_at, ic.used_by_device_id`

func (s *WorkforceService) ListCurrentInviteCodes(ctx context.Context, employeeID *uuid.UUID) ([]InviteCodeRow, error) {
	query := `
		SELECT DISTINCT ON (ic.employee_id) ` + inviteCodeSelectCols + `
		FROM device_invite_codes ic
		JOIN employees e ON e.id = ic.employee_id`
	args := []interface{}{}
	if employeeID != nil {
		query += ` WHERE ic.employee_id = $1`
		args = append(args, *employeeID)
	}
	query += `
		ORDER BY ic.employee_id,
			(CASE WHEN ic.used_at IS NULL AND ic.revoked_at IS NULL AND ic.expires_at > NOW() THEN 0 ELSE 1 END),
			ic.created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []InviteCodeRow
	for rows.Next() {
		row, err := scanInviteCodeRow(rows)
		if err != nil {
			continue
		}
		list = append(list, row)
	}
	sortInviteRowsByCreatedDesc(list)
	return list, nil
}
func (s *WorkforceService) ListInviteCodeHistory(ctx context.Context, employeeID *uuid.UUID, limit int) ([]InviteCodeRow, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `
		SELECT ` + inviteCodeSelectCols + `
		FROM device_invite_codes ic
		JOIN employees e ON e.id = ic.employee_id`
	args := []interface{}{}
	argN := 1
	if employeeID != nil {
		query += fmt.Sprintf(` WHERE ic.employee_id = $%d`, argN)
		args = append(args, *employeeID)
		argN++
	}
	query += fmt.Sprintf(` ORDER BY ic.created_at DESC LIMIT %d`, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []InviteCodeRow
	for rows.Next() {
		row, err := scanInviteCodeRow(rows)
		if err != nil {
			continue
		}
		list = append(list, row)
	}
	return list, nil
}
func sortInviteRowsByCreatedDesc(list []InviteCodeRow) {
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
}
func (s *WorkforceService) ListInviteCodes(ctx context.Context, employeeID *uuid.UUID) ([]InviteCodeRow, error) {
	return s.ListCurrentInviteCodes(ctx, employeeID)
}
func (s *WorkforceService) GetInviteCodeByID(ctx context.Context, inviteID uuid.UUID) (*InviteCodeRow, error) {
	row := InviteCodeRow{}
	var usedAt, revokedAt, supersededAt sql.NullTime
	var deviceID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT `+inviteCodeSelectCols+`
		FROM device_invite_codes ic
		JOIN employees e ON e.id = ic.employee_id
		WHERE ic.id = $1`, inviteID).Scan(
		&row.ID, &row.Code, &row.EmployeeID, &row.EmployeeName, &row.EmployeeCode,
		&row.ExpiresAt, &usedAt, &revokedAt, &supersededAt, &row.CreatedAt, &deviceID,
	)
	if err == sql.ErrNoRows {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	if usedAt.Valid {
		row.UsedAt = &usedAt.Time
	}
	if revokedAt.Valid {
		row.RevokedAt = &revokedAt.Time
	}
	if supersededAt.Valid {
		row.SupersededAt = &supersededAt.Time
	}
	if deviceID.Valid {
		if did, err := uuid.Parse(deviceID.String); err == nil {
			row.DeviceID = &did
		}
	}
	return &row, nil
}
func InviteDisposition(row InviteCodeRow, now time.Time) string {
	if row.SupersededAt != nil {
		return "superseded"
	}
	if row.RevokedAt != nil {
		return "revoked"
	}
	if row.UsedAt != nil {
		return "used"
	}
	if now.After(row.ExpiresAt) {
		return "expired"
	}
	return "active"
}
func InviteDispositionLabel(disposition string) string {
	switch disposition {
	case "active":
		return "有效"
	case "used":
		return "已使用"
	case "expired":
		return "已过期"
	case "revoked":
		return "手动作废"
	case "superseded":
		return "重新生成作废"
	default:
		return disposition
	}
}
func (s *WorkforceService) RevokeInviteCode(ctx context.Context, inviteID uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE device_invite_codes SET revoked_at = NOW()
		WHERE id = $1 AND used_at IS NULL AND revoked_at IS NULL`, inviteID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrInviteNotFound
	}
	return nil
}
func (s *WorkforceService) RegenerateInviteCode(ctx context.Context, employeeID, createdBy uuid.UUID) (*InviteCodeRow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	validDays := s.InviteValidDays(ctx)
	code, id, err := s.createInviteCodeTx(ctx, tx, employeeID, uuidPtrOrNil(createdBy), validDays)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	row, err := s.GetInviteCodeByID(ctx, id)
	if err != nil {
		return &InviteCodeRow{ID: id, Code: code, EmployeeID: employeeID}, nil
	}
	return row, nil
}

type InviteValidationResult struct {
	EmployeeID     uuid.UUID
	EmployeeName   string
	EmployeeNumber string
	Position       string
	Department     string
	InviteID       uuid.UUID
	UserID         *uuid.UUID
}

// ValidateEmployeeUsername verifies username maps to expectedEmployeeID (no password).
func (s *WorkforceService) ValidateEmployeeUsername(ctx context.Context, username string, expectedEmployeeID uuid.UUID) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return ErrInvalidCredentials
	}
	var userID uuid.UUID
	var status int
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.status FROM users u WHERE u.username = $1`, username).Scan(&userID, &status)
	if err == sql.ErrNoRows {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if status != 1 {
		return ErrInvalidCredentials
	}
	empID, err := s.ResolveEmployeeByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrEmployeeNotFound) {
			return ErrNotEmployeeRole
		}
		return err
	}
	if empID != expectedEmployeeID {
		return ErrCredentialsEmployeeMismatch
	}
	return nil
}

// ValidateEmployeeCredentials verifies username/password and ensures it maps to expectedEmployeeID.
func (s *WorkforceService) ValidateEmployeeCredentials(ctx context.Context, username, password string, expectedEmployeeID uuid.UUID) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return ErrInvalidCredentials
	}
	var userID uuid.UUID
	var passwordHash string
	var status int
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.password_hash, u.status
		FROM users u WHERE u.username = $1`, username).Scan(&userID, &passwordHash, &status)
	if err == sql.ErrNoRows {
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if status != 1 {
		return ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	empID, err := s.ResolveEmployeeByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrEmployeeNotFound) {
			return ErrNotEmployeeRole
		}
		return err
	}
	if empID != expectedEmployeeID {
		return ErrCredentialsEmployeeMismatch
	}
	return nil
}

// ResolveInviteCodeEmployee resolves invite code for Telegram bind (used codes allowed).
func (s *WorkforceService) ResolveInviteCodeEmployee(ctx context.Context, code string) (*InviteValidationResult, error) {
	code = strings.TrimSpace(strings.ToUpper(code))
	var row InviteValidationResult
	var usedAt, revokedAt sql.NullTime
	var expiresAt time.Time
	var userID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT ic.id, ic.employee_id, e.name, COALESCE(e.employee_id,''), COALESCE(e.position,''), COALESCE(e.department,''),
			ic.expires_at, ic.used_at, ic.revoked_at, e.user_id::text
		FROM device_invite_codes ic
		JOIN employees e ON e.id = ic.employee_id
		WHERE ic.code = $1`, code,
	).Scan(&row.InviteID, &row.EmployeeID, &row.EmployeeName, &row.EmployeeNumber, &row.Position, &row.Department,
		&expiresAt, &usedAt, &revokedAt, &userID)
	if err == sql.ErrNoRows {
		return nil, ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	if revokedAt.Valid {
		return nil, ErrInviteRevoked
	}
	if time.Now().After(expiresAt) {
		return nil, ErrInviteExpired
	}
	if userID.Valid {
		if uid, err := uuid.Parse(userID.String); err == nil {
			row.UserID = &uid
		}
	}
	return &row, nil
}
// EmployeeSessionWorkDuration is how long an employee may stay logged in after check-in.
const EmployeeSessionWorkDuration = 20 * time.Hour

func (s *WorkforceService) ResolveEmployeeByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var empID uuid.UUID
	err := s.db.QueryRowContext(ctx, `SELECT id FROM employees WHERE user_id = $1 AND status = 1`, userID).Scan(&empID)
	if err == sql.ErrNoRows {
		return uuid.Nil, ErrEmployeeNotFound
	}
	return empID, err
}
func (s *WorkforceService) ResolveEmployeeByDeviceID(ctx context.Context, deviceID uuid.UUID) (uuid.UUID, error) {
	var empID uuid.UUID
	err := s.db.QueryRowContext(ctx, `SELECT employee_uuid FROM devices WHERE id = $1`, deviceID).Scan(&empID)
	if err != nil || empID == uuid.Nil {
		return uuid.Nil, ErrEmployeeNotFound
	}
	return empID, err
}

type CheckInResult struct {
	SessionID uuid.UUID
	Date      string
	ShiftType string
}

type CheckInOptions struct {
	LateMinutes int // Snapshot at punch time, independent of fines.
	ShiftType   string
	RecordDate  string // YYYY-MM-DD, optional override for night shift
	ShiftDetail string // night_last, night_tonight
}

func (s *WorkforceService) CheckInForEmployee(ctx context.Context, empID uuid.UUID, source string, deviceID *uuid.UUID, opts *CheckInOptions) (*CheckInResult, error) {
	if empID == uuid.Nil {
		return nil, ErrEmployeeNotFound
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "agent"
	}

	shiftType := "day"
	if opts != nil && strings.TrimSpace(opts.ShiftType) != "" {
		shiftType = strings.TrimSpace(opts.ShiftType)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Serialize all channels for this employee before reading attendance state.
	var lockedEmployee uuid.UUID
	if err = tx.QueryRowContext(ctx, `SELECT id FROM employees WHERE id=$1 FOR UPDATE`, empID).Scan(&lockedEmployee); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrEmployeeNotFound
		}
		return nil, err
	}

	today := punchNow(ctx).Format("2006-01-02")
	if opts != nil && strings.TrimSpace(opts.RecordDate) != "" {
		today = strings.TrimSpace(opts.RecordDate)
	}

	var openSameShift int
	var openSource sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(source) FROM work_sessions
		WHERE employee_id = $1 AND status = 'open' AND shift_type = $2 AND attendance_date = $3::date`,
		empID, shiftType, today).Scan(&openSameShift, &openSource); err != nil {
		return nil, err
	}
	if openSameShift > 0 {
		src := strings.TrimSpace(openSource.String)
		if src == "telegram" {
			return nil, ErrWorkSessionOpenOnTG
		}
		if src == "agent" {
			return nil, ErrWorkSessionOpenOnPC
		}
		return nil, ErrSessionExists
	}
	var completed int
	if err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM work_sessions
		WHERE employee_id = $1 AND attendance_date = $2::date AND shift_type = $3 AND status = 'closed'`,
		empID, today, shiftType).Scan(&completed); err != nil {
		return nil, err
	}
	if completed > 0 {
		return nil, ErrShiftAlreadyCompleted
	}
	yearMonth := today
	if len(today) >= 7 {
		yearMonth = today[:7]
	}
	var position string
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(position,'') FROM employees WHERE id = $1`, empID).Scan(&position)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO attendance_records (employee_id, year_month, date, status, position_snapshot, checkin_time, checkin_source, shift_type, created_at, updated_at)
		VALUES ($1, $2, $3, 'work', $4, $7, $5, $6, NOW(), NOW())
		ON CONFLICT (employee_id, year_month, date) DO UPDATE SET
			status = CASE
				WHEN attendance_records.manually_edited THEN attendance_records.status
				WHEN attendance_records.status IN ('leave','absent','rest_full','rest_half','off_post','resigned')
					THEN attendance_records.status
				ELSE 'work' END,
			checkin_time = CASE
				WHEN attendance_records.manually_edited THEN attendance_records.checkin_time
				ELSE COALESCE(attendance_records.checkin_time, EXCLUDED.checkin_time) END,
			checkin_source = CASE
				WHEN attendance_records.manually_edited THEN attendance_records.checkin_source
				WHEN attendance_records.checkin_source IS NOT NULL AND attendance_records.checkin_source != '' THEN attendance_records.checkin_source
				ELSE EXCLUDED.checkin_source END,
			shift_type = CASE
				WHEN attendance_records.manually_edited THEN attendance_records.shift_type
				ELSE EXCLUDED.shift_type END,
			position_snapshot = CASE
				WHEN attendance_records.manually_edited THEN attendance_records.position_snapshot
				ELSE COALESCE(attendance_records.position_snapshot, EXCLUDED.position_snapshot) END,
			updated_at = NOW()`,
		empID, yearMonth, today, position, source, shiftType, punchNow(ctx))
	if err != nil {
		return nil, err
	}

	sessionID := uuid.New()
	var dev interface{}
	if deviceID != nil && *deviceID != uuid.Nil {
		dev = *deviceID
	}
	lateMinutes := 0
	shiftDetail := ""
	if opts != nil {
		lateMinutes = max(0, opts.LateMinutes)
		shiftDetail = strings.TrimSpace(opts.ShiftDetail)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO work_sessions (id, employee_id, attendance_date, device_id, checkin_time, status, source, shift_type, shift_detail, late_minutes, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $8, 'open', $5, $6, NULLIF($7,''), $9, NOW(), NOW())`,
		sessionID, empID, today, dev, source, shiftType, shiftDetail, punchNow(ctx), lateMinutes); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	res := &CheckInResult{SessionID: sessionID, Date: today, ShiftType: shiftType}
	return res, nil
}

type CheckOutResult struct {
	SessionID uuid.UUID
	ShiftType string
}
type CheckOutOptions struct {
	ShiftType string // optional: close specific shift; empty = FIFO oldest open
}

func (s *WorkforceService) hasOpenActivitySession(ctx context.Context, empID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM wf_tg_activity_sessions WHERE employee_id = $1 AND status = 'open'`, empID).Scan(&count)
	return count > 0, err
}
func (s *WorkforceService) CheckOutForEmployee(ctx context.Context, empID uuid.UUID, source string, deviceID *uuid.UUID, sessionID *uuid.UUID, opts *CheckOutOptions) (*CheckOutResult, error) {
	if empID == uuid.Nil {
		return nil, ErrEmployeeNotFound
	}
	if open, err := s.hasOpenActivitySession(ctx, empID); err != nil {
		return nil, err
	} else if open {
		return nil, ErrOpenActivityBlocksCheckout
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "agent"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Serialize all channels for this employee before reading attendance state.
	var lockedEmployee uuid.UUID
	if err = tx.QueryRowContext(ctx, `SELECT id FROM employees WHERE id=$1 FOR UPDATE`, empID).Scan(&lockedEmployee); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrEmployeeNotFound
		}
		return nil, err
	}

	var sid uuid.UUID
	shiftFilter := ""
	if opts != nil {
		shiftFilter = strings.TrimSpace(opts.ShiftType)
	}
	if sessionID != nil && *sessionID != uuid.Nil {
		sid = *sessionID
	} else if shiftFilter != "" {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM work_sessions
			WHERE employee_id = $1 AND status = 'open' AND shift_type = $2
			ORDER BY checkin_time ASC LIMIT 1`, empID, shiftFilter).Scan(&sid)
	} else if deviceID != nil && *deviceID != uuid.Nil {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM work_sessions WHERE employee_id = $1 AND device_id = $2 AND status = 'open'
			ORDER BY checkin_time ASC LIMIT 1`, empID, *deviceID).Scan(&sid)
	} else {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM work_sessions WHERE employee_id = $1 AND status = 'open'
			ORDER BY checkin_time ASC LIMIT 1`, empID).Scan(&sid)
	}
	if err == sql.ErrNoRows {
		return nil, ErrNoOpenSession
	}
	if err != nil {
		return nil, err
	}

	// 上下班渠道互通：Telegram / PC 均可结束对方开启的 open 会话；
	// attendance_records.checkout_source 仍记录实际下班渠道（本函数的 source）。

	var closedShift string
	var attendanceDate time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(shift_type,'day'), attendance_date FROM work_sessions
		WHERE id = $1 AND employee_id = $2 AND status = 'open' FOR UPDATE`, sid, empID).
		Scan(&closedShift, &attendanceDate)
	if err == sql.ErrNoRows {
		return nil, ErrNoOpenSession
	}
	if err != nil {
		return nil, err
	}

	recordDate := attendanceDate.Format("2006-01-02")
	yearMonth := attendanceDate.Format("2006-01")

	if _, err = tx.ExecContext(ctx, `
		UPDATE work_sessions SET status = 'closed', checkout_time = $2, updated_at = NOW()
		WHERE id = $1 AND status = 'open'`, sid, punchNow(ctx)); err != nil {
		return nil, err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE attendance_records SET
			status = CASE
				WHEN manually_edited THEN status
				WHEN status IN ('leave','absent','rest_full','rest_half','off_post','resigned') THEN status
				ELSE 'work' END,
			checkout_time = CASE WHEN manually_edited THEN checkout_time ELSE $6 END,
			checkout_source = CASE
				WHEN manually_edited THEN checkout_source
				ELSE $4 END,
			shift_type = CASE
				WHEN manually_edited THEN shift_type
				WHEN COALESCE(NULLIF(TRIM($5), ''), '') = '' THEN shift_type
				ELSE $5 END,
			activity_seconds = CASE
				WHEN manually_edited THEN activity_seconds
				ELSE COALESCE((
					SELECT SUM(a.elapsed_seconds)::int
					FROM wf_tg_activity_sessions a
					WHERE a.employee_id = $1 AND a.attendance_date = $3::date AND a.status = 'closed'
				), 0) END,
			updated_at = NOW()
		WHERE employee_id = $1 AND year_month = $2 AND date = $3`,
		empID, yearMonth, recordDate, source, closedShift, punchNow(ctx))
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &CheckOutResult{SessionID: sid, ShiftType: closedShift}, nil
}

const CheckoutSourceSystem = "system"

// syncAttendanceCheckout writes checkout_time into attendance_records for the given business day.
func (s *WorkforceService) syncAttendanceCheckout(ctx context.Context, empID uuid.UUID, recordDate string, checkoutAt time.Time, source string) error {
	if empID == uuid.Nil || strings.TrimSpace(recordDate) == "" {
		return nil
	}
	yearMonth := recordDate
	if len(recordDate) >= 7 {
		yearMonth = recordDate[:7]
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = CheckoutSourceSystem
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE attendance_records SET
			status = CASE
				WHEN manually_edited THEN status
				WHEN status IN ('leave','absent','rest_full','rest_half','off_post','resigned') THEN status
				ELSE 'work' END,
			checkout_time = CASE WHEN manually_edited THEN checkout_time ELSE $4 END,
			checkout_source = CASE
				WHEN manually_edited THEN checkout_source
				ELSE $5 END,
			activity_seconds = CASE
				WHEN manually_edited THEN activity_seconds
				ELSE COALESCE((
					SELECT SUM(a.elapsed_seconds)::int
					FROM wf_tg_activity_sessions a
					WHERE a.employee_id = $1 AND a.attendance_date = $3::date AND a.status = 'closed'
				), 0) END,
			updated_at = NOW()
		WHERE employee_id = $1 AND year_month = $2 AND date = $3
		  AND checkin_time IS NOT NULL`,
		empID, yearMonth, recordDate, checkoutAt, source)
	return err
}

func (s *WorkforceService) CheckOut(ctx context.Context, deviceID uuid.UUID, sessionID *uuid.UUID) (*CheckOutResult, error) {
	empID, err := s.ResolveEmployeeByDeviceID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return s.CheckOutForEmployee(ctx, empID, "agent", &deviceID, sessionID, nil)
}
func (s *WorkforceService) DeleteEmployeeCompletely(ctx context.Context, employeeID uuid.UUID) error {
	if employeeID == uuid.Nil {
		return ErrEmployeeNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var userID sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT user_id::text FROM employees WHERE id = $1`, employeeID).Scan(&userID); err == sql.ErrNoRows {
		return ErrEmployeeNotFound
	} else if err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE devices SET employee_uuid = NULL, user_id = NULL, employee_id = NULL, employee_name = NULL, employee_number = NULL, position = NULL, bound_at = NULL
		WHERE employee_uuid = $1`, employeeID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE devices SET invited_employee_uuid = NULL WHERE invited_employee_uuid = $1`, employeeID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM employees WHERE id = $1`, employeeID); err != nil {
		return err
	}
	if userID.Valid && strings.TrimSpace(userID.String) != "" {
		if uid, parseErr := uuid.Parse(strings.TrimSpace(userID.String)); parseErr == nil {
			_, _ = tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = $1`, uid)
			if _, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, uid); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (s *WorkforceService) ReactivateEmployee(ctx context.Context, employeeID uuid.UUID) error {
	if employeeID == uuid.Nil {
		return ErrEmployeeNotFound
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE employees
		SET status = 1, resigned_month = NULL, updated_at = NOW()
		WHERE id = $1`, employeeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEmployeeNotFound
	}
	var userID sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT user_id::text FROM employees WHERE id = $1`, employeeID).Scan(&userID)
	if userID.Valid && strings.TrimSpace(userID.String) != "" {
		if uid, parseErr := uuid.Parse(strings.TrimSpace(userID.String)); parseErr == nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE users SET status = 1, updated_at = NOW() WHERE id = $1`, uid)
		}
	}
	return nil
}
func (s *WorkforceService) DisableEmployee(ctx context.Context, employeeID uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var userID sql.NullString
	_ = tx.QueryRowContext(ctx, `SELECT user_id::text FROM employees WHERE id = $1`, employeeID).Scan(&userID)

	if _, err = tx.ExecContext(ctx, `
		UPDATE employees
		SET status = 0, resigned_month = TO_CHAR(CURRENT_DATE, 'YYYY-MM'), updated_at = NOW()
		WHERE id = $1`, employeeID); err != nil {
		return err
	}
	if userID.Valid && strings.TrimSpace(userID.String) != "" {
		if uid, parseErr := uuid.Parse(strings.TrimSpace(userID.String)); parseErr == nil {
			if _, err = tx.ExecContext(ctx, `UPDATE users SET status = 0, updated_at = NOW() WHERE id = $1`, uid); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE device_invite_codes SET revoked_at = NOW()
		WHERE employee_id = $1 AND used_at IS NULL AND revoked_at IS NULL`, employeeID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE devices SET employee_uuid = NULL, user_id = NULL, employee_id = NULL, employee_name = NULL, employee_number = NULL, position = NULL, bound_at = NULL
		WHERE employee_uuid = $1`, employeeID); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncEmployeeProfile 同步员工展示姓名/岗位到用户管理、绩效、罚款、工资、设备等关联表
func (s *WorkforceService) SyncEmployeeProfile(ctx context.Context, employeeID uuid.UUID, displayName, position string) error {
	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return fmt.Errorf("display name required")
	}
	position = strings.TrimSpace(position)
	empIDStr := employeeID.String()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(ctx, `
		UPDATE employees SET name = $1, updated_at = NOW() WHERE id = $2`,
		displayName, employeeID); err != nil {
		return err
	}
	if position != "" {
		if _, err = tx.ExecContext(ctx, `
			UPDATE employees SET position = $1, updated_at = NOW() WHERE id = $2`,
			position, employeeID); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE users SET real_name = $1, updated_at = NOW()
		WHERE id = (SELECT user_id FROM employees WHERE id = $2 AND user_id IS NOT NULL)`,
		displayName, employeeID); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE performance_records
		SET employee_name = $1, updated_at = NOW()
		WHERE employee_id = $2`,
		displayName, employeeID); err != nil {
		return err
	}
	if position != "" {
		if _, err = tx.ExecContext(ctx, `
			UPDATE performance_records SET position = $1, updated_at = NOW() WHERE employee_id = $2`,
			position, employeeID); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE penalty_records SET employee_name = $1 WHERE employee_id = $2`,
		displayName, employeeID); err != nil {
		return err
	}
	if position != "" {
		if _, err = tx.ExecContext(ctx, `
			UPDATE penalty_records SET position = $1 WHERE employee_id = $2`,
			position, employeeID); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE salary_records SET employee_name = $1, updated_at = NOW() WHERE employee_id = $2`,
		displayName, empIDStr); err != nil {
		return err
	}
	if position != "" {
		if _, err = tx.ExecContext(ctx, `
			UPDATE salary_records SET position = $1, updated_at = NOW() WHERE employee_id = $2`,
			position, empIDStr); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE devices SET employee_name = $1 WHERE employee_uuid = $2`,
		displayName, employeeID); err != nil {
		return err
	}
	if position != "" {
		if _, err = tx.ExecContext(ctx, `
			UPDATE devices SET position = $1 WHERE employee_uuid = $2`,
			position, employeeID); err != nil {
			return err
		}
	}

	_, _ = tx.ExecContext(ctx, `
		UPDATE wf_tg_handovers SET employee_name = $1 WHERE employee_id = $2`,
		displayName, employeeID)

	return tx.Commit()
}

// SyncEmployeeLoginIdentity keeps employees.employee_id, users.username, and device 工号 in sync.
func (s *WorkforceService) SyncEmployeeLoginIdentity(ctx context.Context, employeeID uuid.UUID, loginCode string) error {
	if s.db == nil {
		return fmt.Errorf("database not initialized")
	}
	loginCode = strings.TrimSpace(loginCode)
	if loginCode == "" {
		return fmt.Errorf("login code required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var userID sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT user_id::text FROM employees WHERE id = $1`, employeeID).Scan(&userID); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE employees SET employee_id = $1, updated_at = NOW() WHERE id = $2`,
		loginCode, employeeID); err != nil {
		return err
	}

	if userID.Valid && userID.String != "" {
		uid, parseErr := uuid.Parse(userID.String)
		if parseErr != nil {
			return parseErr
		}
		var conflict int
		if err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM users WHERE username = $1 AND id != $2`,
			loginCode, uid).Scan(&conflict); err != nil {
			return err
		}
		if conflict > 0 {
			return fmt.Errorf("login account already exists")
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE users SET username = $1, updated_at = NOW() WHERE id = $2`,
			loginCode, uid); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE devices
		SET employee_id = $1, employee_number = $1, updated_at = NOW()
		WHERE employee_uuid = $2`,
		loginCode, employeeID); err != nil {
		return err
	}

	return tx.Commit()
}

