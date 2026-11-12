// Consolidated source; original file boundaries are marked below.
package handlers

import (
	context "context"
	sql "database/sql"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	uuid "github.com/google/uuid"
	log "log"
	math "math"
	http "net/http"
	regexp "regexp"
	strconv "strconv"
	strings "strings"
	"sync"
	middleware "tg-attendance/backend/middleware"
	models "tg-attendance/backend/models"
	services "tg-attendance/backend/services"
	time "time"
)

// ---- Source: backend/handlers/admin.go ----
type AdminHandler struct {
	db          *sql.DB
	workforce   *services.WorkforceService
	perfPush    *services.PerformancePushService
	penaltyPush *services.PenaltyPushService
	agentNotify *services.AgentNotificationService
	mail        *services.MailService
	csTool      *services.CsToolSettingsService
}

func NewAdminHandler(
	db *sql.DB,
	workforce *services.WorkforceService,
	perfPush *services.PerformancePushService,
	penaltyPush *services.PenaltyPushService,
	agentNotify *services.AgentNotificationService,
	mail *services.MailService,
	csTool *services.CsToolSettingsService,
) *AdminHandler {
	return &AdminHandler{
		db:          db,
		workforce:   workforce,
		perfPush:    perfPush,
		penaltyPush: penaltyPush,
		agentNotify: agentNotify,
		mail:        mail,
		csTool:      csTool,
	}
}

// employeeDeviceActivatedSQL is true when the employee has bound a device, joined a shared
// workstation, or consumed an invite (shared-station members are not 1:1 on devices.employee_uuid).
const employeeDeviceActivatedSQL = `(
	EXISTS (
		SELECT 1 FROM devices d
		WHERE d.employee_uuid = e.id AND d.status != 'rejected'
	)
	OR EXISTS (
		SELECT 1 FROM wf_shared_workstation_members m
		INNER JOIN devices d ON d.id = m.device_id
		WHERE m.employee_id = e.id AND d.status != 'rejected'
	)
	OR EXISTS (
		SELECT 1 FROM device_invite_codes ic
		WHERE ic.employee_id = e.id AND ic.used_at IS NOT NULL
	)
)`

func parseEmployeeEmploymentFilter(c *gin.Context, fallbackMonth string) (scope string, yearMonth string) {
	scope = strings.TrimSpace(c.Query("employment_scope"))
	if scope == "" {
		scope = "all"
	}
	yearMonth = strings.TrimSpace(c.Query("year_month"))
	if yearMonth == "" {
		yearMonth = fallbackMonth
	}
	if _, err := time.Parse("2006-01", yearMonth); err != nil {
		yearMonth = fallbackMonth
	}
	return scope, yearMonth
}

const systemSettingPerformanceHireCutoffDay = "performance_hire_cutoff_day"

func clampPerformanceHireCutoffDay(day int) int {
	if day < 1 {
		return 1
	}
	if day > 31 {
		return 31
	}
	return day
}
func (h *AdminHandler) getPerformanceHireCutoffDay(ctx context.Context) int {
	if h.db == nil {
		return 15
	}
	var val sql.NullString
	if err := h.db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = $1`, systemSettingPerformanceHireCutoffDay).Scan(&val); err != nil || !val.Valid {
		return 15
	}
	n, err := strconv.Atoi(strings.TrimSpace(val.String))
	if err != nil {
		return 15
	}
	return clampPerformanceHireCutoffDay(n)
}
func parsePerformanceHireCutoffDay(c *gin.Context, fallback int) int {
	raw := strings.TrimSpace(c.Query("performance_hire_cutoff_day"))
	if raw == "" {
		return clampPerformanceHireCutoffDay(fallback)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return clampPerformanceHireCutoffDay(fallback)
	}
	return clampPerformanceHireCutoffDay(n)
}
func performanceStartMonthByHireDate(hireDate time.Time, cutoffDay int) string {
	monthStart := time.Date(hireDate.Year(), hireDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	if hireDate.Day() <= cutoffDay {
		return monthStart.AddDate(0, 1, 0).Format("2006-01")
	}
	return monthStart.AddDate(0, 2, 0).Format("2006-01")
}

// GetEmployees 获取员工列表
func (h *AdminHandler) GetEmployees(c *gin.Context) {
	skip, _ := strconv.Atoi(c.DefaultQuery("skip", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	search := c.Query("search")
	scope, yearMonth := parseEmployeeEmploymentFilter(c, time.Now().Format("2006-01"))
	perfEligibility := strings.EqualFold(strings.TrimSpace(c.Query("performance_eligibility")), "true")
	cutoffDay := parsePerformanceHireCutoffDay(c, h.getPerformanceHireCutoffDay(c.Request.Context()))

	where := "1=1"
	args := []interface{}{}
	argIdx := 1
	if emp := c.GetString("standaloneEmployeeID"); emp != "" {
		where += " AND e.id=$1"
		args = append(args, emp)
		argIdx++
	}

	if search != "" {
		where += fmt.Sprintf(" AND name ILIKE $%d", argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}

	switch strings.TrimSpace(c.Query("device_bound")) {
	case "true":
		where += ` AND ` + employeeDeviceActivatedSQL
	case "false":
		where += ` AND e.status = 1 AND NOT ` + employeeDeviceActivatedSQL
	}

	if scope == "month_visible" {
		where += fmt.Sprintf(` AND (
			(e.status = 1 OR e.effective_resigned_month = $%d)
			AND (e.effective_resigned_month IS NULL OR e.effective_resigned_month >= $%d)
		)`, argIdx, argIdx)
		args = append(args, yearMonth)
		argIdx++
	}
	if perfEligibility && yearMonth != "" {
		where += fmt.Sprintf(` AND (
			e.hire_date IS NULL
			OR TO_CHAR(
				DATE_TRUNC('month', e.hire_date) +
				CASE WHEN EXTRACT(DAY FROM e.hire_date)::int <= $%d THEN INTERVAL '1 month' ELSE INTERVAL '2 month' END,
				'YYYY-MM'
			) <= $%d
		)`, argIdx, argIdx+1)
		args = append(args, cutoffDay, yearMonth)
		argIdx += 2
	}

	countQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM (
			SELECT e0.*,
				COALESCE(
					NULLIF(TRIM(e0.resigned_month), ''),
					(
						SELECT MIN(ar.year_month)
						FROM attendance_records ar
						WHERE ar.employee_id = e0.id AND ar.status = 'resigned'
					)
				) AS effective_resigned_month
			FROM employees e0
		) e
		WHERE %s`, where)
	var total int
	if err := h.db.QueryRowContext(c.Request.Context(), countQuery, args...).Scan(&total); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	query := fmt.Sprintf(`
		SELECT e.id, e.employee_id, e.name, e.position, e.department, e.status, e.user_id,
			e.hire_date, e.work_location, e.created_at, e.updated_at,
			e.effective_resigned_month,
			(SELECT u.username FROM users u WHERE u.id = e.user_id) AS username,
			(SELECT ic.code FROM device_invite_codes ic
			 WHERE ic.employee_id = e.id AND ic.used_at IS NULL AND ic.revoked_at IS NULL AND ic.expires_at > NOW()
			 ORDER BY ic.created_at DESC LIMIT 1) AS active_invite,
			COALESCE(
				(SELECT d.id::text FROM devices d WHERE d.employee_uuid = e.id AND d.status != 'rejected' LIMIT 1),
				(SELECT d.id::text FROM wf_shared_workstation_members m
				 INNER JOIN devices d ON d.id = m.device_id
				 WHERE m.employee_id = e.id AND d.status != 'rejected' LIMIT 1)
			) AS device_id,
			COALESCE(
				(SELECT d.last_seen_at FROM devices d WHERE d.employee_uuid = e.id AND d.status != 'rejected' LIMIT 1),
				(SELECT d.last_seen_at FROM wf_shared_workstation_members m
				 INNER JOIN devices d ON d.id = m.device_id
				 WHERE m.employee_id = e.id AND d.status != 'rejected' LIMIT 1)
			) AS last_seen_at,
			`+employeeDeviceActivatedSQL+` AS device_activated
		FROM (
			SELECT e0.*,
				COALESCE(
					NULLIF(TRIM(e0.resigned_month), ''),
					(
						SELECT MIN(ar.year_month)
						FROM attendance_records ar
						WHERE ar.employee_id = e0.id AND ar.status = 'resigned'
					)
				) AS effective_resigned_month
			FROM employees e0
		) e
		WHERE %s
		ORDER BY e.created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argIdx, argIdx+1)
	args = append(args, limit, skip)

	rows, err := h.db.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer rows.Close()

	var employees []map[string]interface{}
	for rows.Next() {
		var id string
		var employeeID, name, position, department, workLocation, resignedMonth sql.NullString
		var userID, activeInvite, deviceID, username sql.NullString
		var empStatus int
		var deviceActivated bool
		var hireDate, createdAt, updatedAt, lastSeen sql.NullTime
		if err := rows.Scan(&id, &employeeID, &name, &position, &department, &empStatus, &userID,
			&hireDate, &workLocation, &createdAt, &updatedAt, &resignedMonth, &username, &activeInvite, &deviceID, &lastSeen, &deviceActivated); err != nil {
			log.Printf("GetEmployees scan error: %v", err)
			continue
		}
		bound := deviceActivated
		onlineStatus := "offline"
		if lastSeen.Valid && time.Since(lastSeen.Time) < 3*time.Minute {
			onlineStatus = "online"
		}
		activationStatus := "pending_activation"
		if bound {
			activationStatus = "active"
		}
		if empStatus == 0 {
			activationStatus = "disabled"
		}
		createdAtVal := interface{}(nil)
		if createdAt.Valid {
			createdAtVal = createdAt.Time
		}
		emp := map[string]interface{}{
			"id":                 id,
			"employee_id":        nullStringToString(employeeID),
			"username":           nullStringToString(username),
			"name":               nullStringToString(name),
			"position":           nullStringToString(position),
			"department":         nullStringToString(department),
			"status":             empStatus,
			"user_id":            nullStringToInterface(userID),
			"hire_date":          nullTimeToString(hireDate),
			"work_location":      nullStringToString(workLocation),
			"created_at":         createdAtVal,
			"updated_at":         nullTimeToString(updatedAt),
			"resigned_month":     nullStringToString(resignedMonth),
			"device_bound":       bound,
			"online_status":      onlineStatus,
			"activation_status":  activationStatus,
			"active_invite_code": nullStringToInterface(activeInvite),
		}
		employees = append(employees, emp)
	}

	models.Send(c, 200, 0, "success", gin.H{
		"items": employees,
		"total": total,
	})
}

// CreateEmployee 创建员工
func (h *AdminHandler) CreateEmployee(c *gin.Context) {
	var req struct {
		EmployeeID   string `json:"employee_id"`
		Name         string `json:"name"`
		Position     string `json:"position"`
		HireDate     string `json:"hire_date"`
		WorkLocation string `json:"work_location"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	id := uuid.New().String()
	var hireDate interface{}
	if req.HireDate != "" {
		hireDate = req.HireDate
	}

	tx, err := h.db.Begin()
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer tx.Rollback()

	// 1. 插入员工
	_, err = tx.Exec(`
        INSERT INTO employees (id, employee_id, name, position, hire_date, work_location, created_at)
        VALUES ($1, $2, $3, $4, $5, $6, NOW())
    `, id, req.EmployeeID, req.Name, req.Position, hireDate, req.WorkLocation)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// 2. ✅ 自动初始化当前月份的绩效记录
	currentMonth := time.Now().Format("2006-01")
	_, err = tx.Exec(`
        INSERT INTO performance_records (employee_id, employee_name, position, score_records, total_score, grade, month, created_at, updated_at)
        VALUES ($1, $2, $3, '[]'::jsonb, 10, '合格', $4, NOW(), NOW())
        ON CONFLICT (employee_id, month) DO NOTHING
    `, id, req.Name, req.Position, currentMonth)
	if err != nil {
		log.Printf("初始化绩效记录失败: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	yearMonth := time.Now().Format("2006-01")
	_, err = tx.Exec(`
        INSERT INTO attendance_records (employee_id, year_month, date, status, position_snapshot, created_at, updated_at)
        VALUES ($1, $2, $3, 'pending', $4, NOW(), NOW())
        ON CONFLICT (employee_id, year_month, date) DO NOTHING
    `, id, yearMonth, today, req.Position)
	if err != nil {
		log.Printf("初始化考勤记录失败: %v", err)
	}

	if err := tx.Commit(); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	Log(c, "CREATE", "EMPLOYEE",
		fmt.Sprintf("新增员工：%s（工号 %s，岗位 %s）", req.Name, req.EmployeeID, req.Position),
		id, req.Name)

	models.Send(c, 200, 0, "Employee created", gin.H{"id": id})
}

// UpdateEmployee 更新员工
func (h *AdminHandler) UpdateEmployee(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name         string `json:"name"`
		EmployeeID   string `json:"employee_id"`
		Position     string `json:"position"`
		Department   string `json:"department"`
		HireDate     string `json:"hire_date"`
		WorkLocation string `json:"work_location"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	var hireDate interface{}
	if req.HireDate != "" {
		hireDate = req.HireDate
	}

	_, err := h.db.Exec(`
		UPDATE employees SET name=$1, employee_id=$2, position=$3, department=$4, hire_date=$5, work_location=$6, updated_at=NOW()
		WHERE id=$7
	`, req.Name, req.EmployeeID, req.Position, req.Department, hireDate, req.WorkLocation, id)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	if h.workforce != nil {
		if empUUID, parseErr := uuid.Parse(id); parseErr == nil {
			if syncErr := h.workforce.SyncEmployeeLoginIdentity(c.Request.Context(), empUUID, req.EmployeeID); syncErr != nil {
				models.SendError(c, 400, 1001, syncErr.Error())
				return
			}
			if syncErr := h.workforce.SyncEmployeeProfile(c.Request.Context(), empUUID, req.Name, req.Position); syncErr != nil {
				log.Printf("SyncEmployeeProfile failed for %s: %v", id, syncErr)
			}
		}
	}

	currentMonth := time.Now().Format("2006-01")
	_, _ = h.db.Exec(`
		UPDATE attendance_records SET position_snapshot = $1, updated_at = NOW()
		WHERE employee_id = $2 AND year_month = $3
	`, req.Position, id, currentMonth)

	Log(c, "UPDATE", "EMPLOYEE",
		fmt.Sprintf("修改员工：%s（工号 %s，岗位 %s）", req.Name, req.EmployeeID, req.Position),
		id, req.Name)

	models.Send(c, 200, 0, "Employee updated", nil)
}

// DeleteEmployee 删除员工及其子账号、设备绑定等关联数据
func (h *AdminHandler) DeleteEmployee(c *gin.Context) {
	id := c.Param("id")
	empUUID, err := uuid.Parse(id)
	if err != nil {
		models.SendError(c, 400, 1001, "invalid employee id")
		return
	}
	var employeeName string
	_ = h.db.QueryRow(`SELECT name FROM employees WHERE id=$1`, id).Scan(&employeeName)

	if h.workforce == nil {
		models.SendError(c, 500, 5000, "workforce service unavailable")
		return
	}
	if err := h.workforce.DeleteEmployeeCompletely(c.Request.Context(), empUUID); err != nil {
		if errors.Is(err, services.ErrEmployeeNotFound) {
			models.SendError(c, 404, 2001, "employee not found")
			return
		}
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	if employeeName != "" {
		Log(c, "DELETE", "EMPLOYEE",
			fmt.Sprintf("删除员工：%s（含子账号与绑定）", employeeName), id, employeeName)
	}

	models.Send(c, 200, 0, "Employee deleted", nil)
}

// GetAttendanceRecords 批量获取考勤记录
func (h *AdminHandler) GetAttendanceRecords(c *gin.Context) {
	yearMonth := c.Query("year_month")

	employeeIDs := c.QueryArray("employee_ids[]")
	if len(employeeIDs) == 0 {
		employeeIDs = c.QueryArray("employee_ids")
	}
	if len(employeeIDs) == 0 {
		if ids := c.Query("employee_ids"); ids != "" {
			employeeIDs = strings.Split(ids, ",")
		}
	}

	if yearMonth == "" || len(employeeIDs) == 0 {
		models.Send(c, 200, 0, "success", gin.H{})
		return
	}

	// ✅ 验证并过滤有效的 UUID
	validUUIDs := make([]string, 0)
	for _, id := range employeeIDs {
		if _, err := uuid.Parse(id); err == nil {
			validUUIDs = append(validUUIDs, id)
		}
	}

	if len(validUUIDs) == 0 {
		models.Send(c, 200, 0, "success", gin.H{})
		return
	}

	// ✅ 构建占位符，将字符串 UUID 转换为 UUID 类型
	placeholders := make([]string, len(validUUIDs))
	args := []interface{}{yearMonth}
	for i, id := range validUUIDs {
		placeholders[i] = fmt.Sprintf("$%d::uuid", i+2) // ✅ 关键：::uuid 转换
		args = append(args, id)
	}

	query := fmt.Sprintf(`
        SELECT ar.employee_id, ar.date::text, ar.status, COALESCE(ar.checkin_source,''), COALESCE(ar.checkout_source,''),
               ar.checkin_time, ar.checkout_time,
               COALESCE(NULLIF(TRIM(ar.shift_type), ''), ws.shift_type, ''),
               CASE
                   WHEN ar.activity_seconds IS NOT NULL THEN ar.activity_seconds
                   ELSE COALESCE(act.total_sec, 0)
               END,
               COALESCE((SELECT MAX(late_minutes) FROM work_sessions ls
                 WHERE ls.employee_id=ar.employee_id AND ls.attendance_date=ar.date),0)
        FROM attendance_records ar
        LEFT JOIN LATERAL (
            SELECT COALESCE(shift_type, '') AS shift_type
            FROM work_sessions
            WHERE employee_id = ar.employee_id AND attendance_date = ar.date
            ORDER BY checkin_time DESC
            LIMIT 1
        ) ws ON TRUE
        LEFT JOIN LATERAL (
            SELECT COALESCE(SUM(elapsed_seconds), 0)::int AS total_sec
            FROM wf_tg_activity_sessions
            WHERE employee_id = ar.employee_id AND attendance_date = ar.date AND status = 'closed'
        ) act ON TRUE
        WHERE ar.year_month = $1 AND ar.employee_id IN (%s)
    `, strings.Join(placeholders, ","))

	rows, err := h.db.Query(query, args...)
	if err != nil {
		log.Printf("SQL错误: %v, query: %s", err, query)
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer rows.Close()

	// 构建返回结果
	result := make(map[string]map[string]interface{})
	for _, id := range employeeIDs {
		result[id] = make(map[string]interface{})
	}

	for rows.Next() {
		var employeeUUID, date, status, checkinSource, checkoutSource, shiftType string
		var checkinTime, checkoutTime sql.NullTime
		var activitySeconds, lateMinutes int
		if err := rows.Scan(&employeeUUID, &date, &status, &checkinSource, &checkoutSource, &checkinTime, &checkoutTime, &shiftType, &activitySeconds, &lateMinutes); err != nil {
			continue
		}
		// 匹配原始输入的 ID
		for _, inputID := range employeeIDs {
			if inputID == employeeUUID {
				if result[inputID] == nil {
					result[inputID] = make(map[string]interface{})
				}
				record := map[string]interface{}{
					"status":          status,
					"checkinSource":   checkinSource,
					"checkoutSource":  checkoutSource,
					"activitySeconds": activitySeconds,
					"lateMinutes":     lateMinutes,
				}
				if checkinTime.Valid {
					record["checkinTime"] = checkinTime.Time.UTC().Format(time.RFC3339)
				}
				if checkoutTime.Valid {
					record["checkoutTime"] = checkoutTime.Time.UTC().Format(time.RFC3339)
				}
				if strings.TrimSpace(shiftType) != "" {
					record["shiftType"] = strings.TrimSpace(shiftType)
				}
				result[inputID][date] = record
				break
			}
		}
	}

	h.attachAttendanceLateEarlyFlags(result, yearMonth, validUUIDs)

	models.Send(c, 200, 0, "success", result)
}

var (
	attendanceLateMinutesRe  = regexp.MustCompile(`迟到\s*(\d+)\s*分钟`)
	attendanceEarlyMinutesRe = regexp.MustCompile(`早退\s*(\d+)\s*分钟`)
)

// attachAttendanceLateEarlyFlags merges late/early minutes from penalty_records into daily attendance rows.
func (h *AdminHandler) attachAttendanceLateEarlyFlags(
	result map[string]map[string]interface{},
	yearMonth string,
	employeeIDs []string,
) {
	if h.db == nil || len(result) == 0 || len(employeeIDs) == 0 || strings.TrimSpace(yearMonth) == "" {
		return
	}
	ym, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return
	}
	dateFrom := ym.Format("2006-01-02")
	dateTo := ym.AddDate(0, 1, -1).Format("2006-01-02")

	placeholders := make([]string, len(employeeIDs))
	args := []interface{}{dateFrom, dateTo}
	for i, id := range employeeIDs {
		placeholders[i] = fmt.Sprintf("$%d::uuid", i+3)
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT employee_id::text, penalty_date::text, COALESCE(category,''), COALESCE(reason,'')
		FROM penalty_records
		WHERE penalty_date BETWEEN $1::date AND $2::date
		  AND employee_id IN (%s)
		  AND (
			category IN ('迟到', '早退', 'TG考勤')
			OR reason LIKE '%%迟到%%'
			OR reason LIKE '%%早退%%'
		  )`, strings.Join(placeholders, ","))

	rows, err := h.db.Query(query, args...)
	if err != nil {
		log.Printf("[Attendance] late/early flags query failed: %v", err)
		return
	}
	defer rows.Close()

	type dayFlags struct {
		lateMin  int
		earlyMin int
		lateHit  bool
		earlyHit bool
	}
	flags := make(map[string]*dayFlags) // empID|date

	for rows.Next() {
		var empID, penaltyDate, category, reason string
		if rows.Scan(&empID, &penaltyDate, &category, &reason) != nil {
			continue
		}
		if i := strings.IndexByte(penaltyDate, 'T'); i > 0 {
			penaltyDate = penaltyDate[:i]
		}
		key := empID + "|" + penaltyDate
		f := flags[key]
		if f == nil {
			f = &dayFlags{}
			flags[key] = f
		}
		cat := strings.TrimSpace(category)
		reason = strings.TrimSpace(reason)
		isLate := cat == "迟到" || strings.Contains(reason, "迟到")
		isEarly := cat == "早退" || strings.Contains(reason, "早退")
		if isLate {
			f.lateHit = true
			if m := attendanceLateMinutesRe.FindStringSubmatch(reason); len(m) == 2 {
				if n, err := strconv.Atoi(m[1]); err == nil && n > f.lateMin {
					f.lateMin = n
				}
			}
		}
		if isEarly {
			f.earlyHit = true
			if m := attendanceEarlyMinutesRe.FindStringSubmatch(reason); len(m) == 2 {
				if n, err := strconv.Atoi(m[1]); err == nil && n > f.earlyMin {
					f.earlyMin = n
				}
			}
		}
	}

	for empID, days := range result {
		for date, raw := range days {
			rec, ok := raw.(map[string]interface{})
			if !ok || rec == nil {
				continue
			}
			f := flags[empID+"|"+date]
			if f == nil {
				continue
			}
			if f.lateHit || f.lateMin > 0 {
				if f.lateMin > 0 {
					rec["lateMinutes"] = f.lateMin
					rec["lateLabel"] = fmt.Sprintf("迟到%d分钟", f.lateMin)
				} else {
					rec["lateLabel"] = "迟到"
				}
			}
			if f.earlyHit || f.earlyMin > 0 {
				if f.earlyMin > 0 {
					rec["earlyMinutes"] = f.earlyMin
					rec["earlyLabel"] = fmt.Sprintf("早退%d分钟", f.earlyMin)
				} else {
					rec["earlyLabel"] = "早退"
				}
			}
		}
	}
}

// SaveAttendanceRecords 保存考勤记录
func (h *AdminHandler) SaveAttendanceRecords(c *gin.Context) {
	var req struct {
		YearMonth        string                 `json:"year_month"`
		Data             map[string]interface{} `json:"data"`
		Clears           map[string]interface{} `json:"clears"`
		RestoreEmployees []string               `json:"restoreEmployees"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	if len(req.Clears) > 0 && !middleware.UserHasPermission(c, "attendance:delete") {
		models.SendError(c, 403, 403, "清除考勤需要删除权限")
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer tx.Rollback()

	for empID, records := range req.Data {
		var exists bool
		err := tx.QueryRow(`
            SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)
        `, empID).Scan(&exists)
		if err != nil || !exists {
			// 如果不是有效的 UUID，尝试通过 employee_id 查找
			var realUUID string
			err = tx.QueryRow(`
                SELECT id FROM employees WHERE employee_id = $1
            `, empID).Scan(&realUUID)
			if err != nil {
				// 如果还是找不到，跳过这条记录
				continue
			}
			empID = realUUID // 使用找到的 UUID
		}

		recordsMap, ok := records.(map[string]interface{})
		if !ok {
			continue
		}
		for date, record := range recordsMap {
			recordMap, ok := record.(map[string]interface{})
			if !ok {
				continue
			}
			status, _ := recordMap["status"].(string)
			if status == "" {
				continue
			}
			_, err = tx.Exec(`
                INSERT INTO attendance_records (employee_id, year_month, date, status, manually_edited, updated_at)
                VALUES ($1, $2, $3, $4, TRUE, NOW())
                ON CONFLICT (employee_id, year_month, date) DO UPDATE SET
                    status = $4,
                    manually_edited = TRUE,
                    updated_at = NOW()
            `, empID, req.YearMonth, date, status)
			if err != nil {
				models.SendError(c, 500, 5000, err.Error())
				return
			}
		}
	}

	for empID, datesRaw := range req.Clears {
		resolvedEmpID := empID
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1)`, empID).Scan(&exists); err != nil || !exists {
			var realUUID string
			if err = tx.QueryRow(`SELECT id FROM employees WHERE employee_id = $1`, empID).Scan(&realUUID); err != nil {
				continue
			}
			resolvedEmpID = realUUID
		}
		dateList, ok := datesRaw.([]interface{})
		if !ok {
			continue
		}
		for _, d := range dateList {
			dateStr, _ := d.(string)
			dateStr = strings.TrimSpace(dateStr)
			if dateStr == "" {
				continue
			}
			if _, err = tx.Exec(`
                DELETE FROM attendance_records
                WHERE employee_id = $1 AND year_month = $2 AND date = $3::date`,
				resolvedEmpID, req.YearMonth, dateStr); err != nil {
				models.SendError(c, 500, 5000, err.Error())
				return
			}
		}
	}

	if err := tx.Commit(); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	if h.workforce != nil {
		for _, empID := range req.RestoreEmployees {
			empID = strings.TrimSpace(empID)
			if empID == "" {
				continue
			}
			if uid, err := uuid.Parse(empID); err == nil {
				_ = h.workforce.ReactivateEmployee(c.Request.Context(), uid)
			}
		}
	}

	// ✅ 记录操作日志
	Log(c, "UPDATE", "ATTENDANCE",
		fmt.Sprintf("保存 %s 月考勤记录", req.YearMonth), "", req.YearMonth)

	models.Send(c, 200, 0, "Attendance saved", nil)
}
func (h *AdminHandler) GetPerformanceRuleSettings(c *gin.Context) {
	cutoff := h.getPerformanceHireCutoffDay(c.Request.Context())
	models.Send(c, 200, 0, "success", gin.H{
		"hireCutoffDay": cutoff,
	})
}
func (h *AdminHandler) SavePerformanceRuleSettings(c *gin.Context) {
	var req struct {
		HireCutoffDay int `json:"hireCutoffDay"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	day := clampPerformanceHireCutoffDay(req.HireCutoffDay)
	if _, err := h.db.ExecContext(c.Request.Context(), `
		INSERT INTO system_settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		systemSettingPerformanceHireCutoffDay, strconv.Itoa(day)); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "UPDATE", "PERFORMANCE", fmt.Sprintf("更新绩效起算规则：入职截止日=%d", day), "", strconv.Itoa(day))
	models.Send(c, 200, 0, "saved", gin.H{
		"hireCutoffDay": day,
	})
}

// GetPerformance 获取绩效数据
func (h *AdminHandler) GetPerformance(c *gin.Context) {
	month := c.Query("month")
	employeeID := c.Query("employee_id")
	scope, scopeMonth := parseEmployeeEmploymentFilter(c, time.Now().Format("2006-01"))
	cutoffDay := parsePerformanceHireCutoffDay(c, h.getPerformanceHireCutoffDay(c.Request.Context()))
	if month == "" {
		month = scopeMonth
	}

	var where string
	var args []interface{}
	argIdx := 1

	if month != "" {
		where += fmt.Sprintf(" AND month = $%d", argIdx)
		args = append(args, month)
		argIdx++
	}
	if employeeID != "" {
		where += fmt.Sprintf(" AND employee_id = $%d", argIdx)
		args = append(args, employeeID)
		argIdx++
	}
	if scope == "month_visible" && employeeID == "" {
		where += fmt.Sprintf(` AND (
			(e.status = 1 OR e.effective_resigned_month = $%d)
			AND (e.effective_resigned_month IS NULL OR e.effective_resigned_month >= $%d)
		)`, argIdx, argIdx)
		args = append(args, month)
		argIdx++
	}
	if month != "" && employeeID == "" {
		where += fmt.Sprintf(` AND (
			e.hire_date IS NULL
			OR TO_CHAR(
				DATE_TRUNC('month', e.hire_date) +
				CASE WHEN EXTRACT(DAY FROM e.hire_date)::int <= $%d THEN INTERVAL '1 month' ELSE INTERVAL '2 month' END,
				'YYYY-MM'
			) <= $%d
		)`, argIdx, argIdx+1)
		args = append(args, cutoffDay, month)
		argIdx += 2
	}

	query := fmt.Sprintf(`
		SELECT pr.employee_id, pr.employee_name, pr.position, pr.score_records, pr.total_score, pr.grade, pr.month
		FROM performance_records pr
		LEFT JOIN (
			SELECT e0.id, e0.status, e0.hire_date,
				COALESCE(
					NULLIF(TRIM(e0.resigned_month), ''),
					(
						SELECT MIN(ar.year_month)
						FROM attendance_records ar
						WHERE ar.employee_id = e0.id AND ar.status = 'resigned'
					)
				) AS effective_resigned_month
			FROM employees e0
		) e ON e.id = pr.employee_id
		WHERE 1=1 %s
	`, where)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var employeeID, employeeName, position, month string
		var scoreRecordsJSON string
		var totalScore int
		var grade string
		if err := rows.Scan(&employeeID, &employeeName, &position, &scoreRecordsJSON, &totalScore, &grade, &month); err != nil {
			continue
		}
		var scoreRecords []interface{}
		json.Unmarshal([]byte(scoreRecordsJSON), &scoreRecords)
		items = append(items, map[string]interface{}{
			"employee_id":   employeeID,
			"employee_name": employeeName,
			"position":      position,
			"score_records": scoreRecords,
			"total_score":   totalScore,
			"grade":         grade,
			"month":         month,
		})
	}

	models.Send(c, 200, 0, "success", gin.H{"items": items})
}

// BatchSavePerformance 批量保存绩效
func (h *AdminHandler) BatchSavePerformance(c *gin.Context) {
	var req struct {
		Month string                   `json:"month"`
		Items []map[string]interface{} `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	tx, err := h.db.Begin()
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer tx.Rollback()
	cutoffDay := h.getPerformanceHireCutoffDay(c.Request.Context())

	for _, item := range req.Items {
		employeeID, _ := item["employee_id"].(string)
		employeeName, _ := item["employee_name"].(string)
		position, _ := item["position"].(string)
		totalScore, _ := item["total_score"].(float64)
		grade, _ := item["grade"].(string)
		if !h.allowPerformanceReplacement(c, employeeID, req.Month, item["score_records"]) {
			return
		}
		scoreRecords, _ := json.Marshal(item["score_records"])

		var hireDate sql.NullTime
		_ = tx.QueryRowContext(c.Request.Context(), `
			SELECT hire_date FROM employees WHERE id = $1`, employeeID).Scan(&hireDate)
		if hireDate.Valid && strings.TrimSpace(req.Month) != "" {
			startMonth := performanceStartMonthByHireDate(hireDate.Time, cutoffDay)
			if req.Month < startMonth {
				models.SendError(c, 400, 1001, fmt.Sprintf("员工 %s 绩效起算月为 %s，当前月份不可保存", employeeName, startMonth))
				return
			}
		}

		_, err = tx.Exec(`
			INSERT INTO performance_records (employee_id, employee_name, position, score_records, total_score, grade, month, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT (employee_id, month) DO UPDATE SET
				employee_name=$2, position=$3, score_records=$4, total_score=$5, grade=$6, updated_at=NOW()
            WHERE $8 OR jsonb_array_length(COALESCE(performance_records.score_records,'[]'::jsonb)) <= jsonb_array_length($4::jsonb)
		`, employeeID, employeeName, position, string(scoreRecords), int(totalScore), grade, req.Month, middleware.UserHasPermission(c, "performance:delete"))
		if err != nil {
			models.SendError(c, 500, 5000, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	Log(c, "UPDATE", "PERFORMANCE",
		fmt.Sprintf("批量保存 %s 月绩效考核（共 %d 人）", req.Month, len(req.Items)),
		"", req.Month)

	models.Send(c, 200, 0, "Performance saved", nil)
}

// GetPenaltyRecords 获取罚款记录
func (h *AdminHandler) GetPenaltyRecords(c *gin.Context) {
	month := c.Query("month")
	employeeID := c.Query("employee_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	offset := (page - 1) * pageSize
	where := "1=1"
	args := []interface{}{}
	argIdx := 1

	if month != "" {
		where += fmt.Sprintf(" AND DATE_TRUNC('month', penalty_date) = DATE_TRUNC('month', $%d::date)", argIdx)
		args = append(args, month+"-01")
		argIdx++
	}
	if employeeID != "" {
		where += fmt.Sprintf(" AND employee_id = $%d", argIdx)
		args = append(args, employeeID)
		argIdx++
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM penalty_records WHERE %s", where)
	var total int
	h.db.QueryRow(countQuery, args...).Scan(&total)

	query := fmt.Sprintf(`
		SELECT pr.id, pr.employee_id, pr.employee_name, pr.position, pr.amount, pr.category, pr.reason, pr.penalty_date,
			pr.created_by,
			COALESCE(NULLIF(TRIM(u.real_name), ''), pr.created_by) AS created_by_name,
			pr.created_at
		FROM penalty_records pr
		LEFT JOIN users u ON u.username = pr.created_by
		WHERE %s
		ORDER BY pr.penalty_date DESC
		LIMIT $%d OFFSET $%d
	`, where, argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var id, employeeID, employeeName, position, category, reason, createdBy, createdByName string
		var amount float64
		var penaltyDate, createdAt time.Time
		rows.Scan(&id, &employeeID, &employeeName, &position, &amount, &category, &reason, &penaltyDate, &createdBy, &createdByName, &createdAt)
		items = append(items, map[string]interface{}{
			"id":              id,
			"employee_id":     employeeID,
			"employee_name":   employeeName,
			"position":        position,
			"amount":          amount,
			"category":        category,
			"reason":          reason,
			"penalty_date":    penaltyDate.Format("2006-01-02"),
			"created_by":      createdBy,
			"created_by_name": createdByName,
		})
	}

	// 统计汇总
	var totalAmount float64
	var employeeCount int
	h.db.QueryRow(fmt.Sprintf(`
		SELECT COALESCE(SUM(amount), 0), COUNT(DISTINCT employee_id)
		FROM penalty_records WHERE %s
	`, where), args[:len(args)-2]...).Scan(&totalAmount, &employeeCount)

	models.Send(c, 200, 0, "success", gin.H{
		"items": items,
		"total": total,
		"stats": gin.H{
			"total_amount":   totalAmount,
			"employee_count": employeeCount,
			"record_count":   total,
			"avg_amount": func() float64 {
				if employeeCount > 0 {
					return totalAmount / float64(employeeCount)
				}
				return 0
			}(),
		},
	})
}

// CreatePenaltyRecord 创建罚款记录
func (h *AdminHandler) CreatePenaltyRecord(c *gin.Context) {
	var req struct {
		EmployeeID  string  `json:"employee_id"`
		PenaltyDate string  `json:"penalty_date"`
		Amount      float64 `json:"amount"`
		Category    string  `json:"category"`
		Reason      string  `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	username := c.GetString("username")
	id := uuid.New().String()

	_, err := h.db.Exec(`
		INSERT INTO penalty_records (id, employee_id, employee_name, position, amount, category, reason, penalty_date, created_by, created_at)
		SELECT $1, $2, e.name, e.position, $3, $4, $5, $6, $7, NOW()
		FROM employees e WHERE e.id = $2
	`, id, req.EmployeeID, req.Amount, req.Category, req.Reason, req.PenaltyDate, username)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	var employeeName string
	h.db.QueryRow(`SELECT name FROM employees WHERE id = $1`, req.EmployeeID).Scan(&employeeName)
	Log(c, "CREATE", "PENALTY",
		fmt.Sprintf("添加罚款：员工 %s，金额 ¥%.2f，类型 %s", employeeName, req.Amount, req.Category),
		id, employeeName)

	models.Send(c, 200, 0, "Penalty record created", nil)
}

// DeletePenaltyRecord 删除罚款记录
func (h *AdminHandler) DeletePenaltyRecord(c *gin.Context) {
	id := c.Param("id")

	// ✅ 获取删除的记录信息用于日志
	var employeeName string
	var amount float64
	h.db.QueryRow(`SELECT employee_name, amount FROM penalty_records WHERE id = $1`, id).Scan(&employeeName, &amount)

	_, err := h.db.Exec("DELETE FROM penalty_records WHERE id=$1", id)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// ✅ 修复: 使用全局 Log 函数
	Log(c, "DELETE", "PENALTY",
		fmt.Sprintf("删除罚款：员工 %s，金额 ¥%.2f", employeeName, amount),
		id, employeeName)

	models.Send(c, 200, 0, "Penalty record deleted", nil)
}

// CreateEmployeeAccount 创建出款站点员工账号
func (h *AdminHandler) CreateEmployeeAccount(c *gin.Context) {
	var req struct {
		SiteID      string `json:"site_id" binding:"required"`
		Name        string `json:"name" binding:"required"`
		AccountName string `json:"account_name" binding:"required"`
		Shift       string `json:"shift" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	siteID, err := uuid.Parse(strings.TrimSpace(req.SiteID))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid site_id")
		return
	}
	id := uuid.New()
	_, err = h.db.Exec(`
		INSERT INTO employee_accounts (id, site_id, name, account_name, shift, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, true, NOW())
	`, id, siteID, req.Name, req.AccountName, req.Shift)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "CREATE", "SITE_STATS",
		fmt.Sprintf("新增出款员工账号：%s（账号 %s，%s班）", req.Name, req.AccountName, req.Shift),
		id.String(), req.Name)
	models.Send(c, 200, 0, "Employee account created", nil)
}

const maxEmployeeAccountBatchShift = 200

func nullTimeToString(t sql.NullTime) string {
	if t.Valid {
		return t.Time.Format("2006-01-02")
	}
	return ""
}
func nullStringToString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}
func nullStringToInterface(s sql.NullString) interface{} {
	if s.Valid {
		return s.String
	}
	return nil
}

// ==================== 绩效管理（增强版：添加修改删除日志） ====================
// UpdatePerformance 更新单条绩效记录
func (h *AdminHandler) UpdatePerformance(c *gin.Context) {
	var req struct {
		EmployeeID   string      `json:"employee_id"`
		Month        string      `json:"month"`
		TotalScore   int         `json:"total_score"`
		Grade        string      `json:"grade"`
		ScoreRecords interface{} `json:"score_records"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	if !h.allowPerformanceReplacement(c, req.EmployeeID, req.Month, req.ScoreRecords) {
		return
	}
	// 获取旧值用于日志（同时获取员工ID用于后续查询）
	var oldTotalScore int
	var oldGrade string
	var employeeID, employeeName string
	err := h.db.QueryRow(`
        SELECT employee_id, employee_name, total_score, grade 
        FROM performance_records 
        WHERE employee_id = $1 AND month = $2
    `, req.EmployeeID, req.Month).Scan(&employeeID, &employeeName, &oldTotalScore, &oldGrade)
	if err != nil && err != sql.ErrNoRows {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// 序列化 score_records
	scoreRecordsJSON, _ := json.Marshal(req.ScoreRecords)

	// 执行更新
	_, err = h.db.Exec(`
        UPDATE performance_records 
        SET total_score = $1, grade = $2, score_records = $3, updated_at = NOW()
        WHERE employee_id = $4 AND month = $5
          AND ($6 OR jsonb_array_length(COALESCE(score_records,'[]'::jsonb)) <= jsonb_array_length($3::jsonb))
    `, req.TotalScore, req.Grade, string(scoreRecordsJSON), req.EmployeeID, req.Month, middleware.UserHasPermission(c, "performance:delete"))
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// ✅ 修复：更新后重新查询最新的员工姓名
	var latestEmployeeName string
	err = h.db.QueryRow(`
        SELECT e.name 
        FROM performance_records p
        JOIN employees e ON p.employee_id = e.id
        WHERE p.employee_id = $1 AND p.month = $2
    `, req.EmployeeID, req.Month).Scan(&latestEmployeeName)
	if err == nil && latestEmployeeName != "" {
		employeeName = latestEmployeeName
	} else {
		// 降级方案：使用绩效表中存储的姓名
		_ = h.db.QueryRow(`
            SELECT employee_name FROM performance_records 
            WHERE employee_id = $1 AND month = $2
        `, req.EmployeeID, req.Month).Scan(&employeeName)
	}

	month := c.Param("month")
	if month == "" {
		month = req.Month
	}

	// 记录操作日志（现在使用最新数据）
	Log(c, "UPDATE", "PERFORMANCE",
		fmt.Sprintf("修改绩效考核：员工 %s，%s 月，分数 %d→%d，等级 %s→%s",
			employeeName, month, oldTotalScore, req.TotalScore, oldGrade, req.Grade),
		req.EmployeeID, employeeName)

	models.Send(c, 200, 0, "Performance updated", nil)
}

// DeletePerformance 删除绩效记录
func (h *AdminHandler) DeletePerformance(c *gin.Context) {
	employeeID := c.Param("employeeId")
	month := c.Param("month")

	if employeeID == "" || month == "" {
		models.SendError(c, 400, 1001, "employeeId and month are required")
		return
	}

	// 获取员工姓名用于日志
	var employeeName string
	var oldTotalScore int
	h.db.QueryRow(`
        SELECT employee_name, total_score 
        FROM performance_records 
        WHERE employee_id = $1 AND month = $2
    `, employeeID, month).Scan(&employeeName, &oldTotalScore)

	_, err := h.db.Exec(`
        DELETE FROM performance_records 
        WHERE employee_id = $1 AND month = $2
    `, employeeID, month)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// ✅ 记录操作日志
	Log(c, "DELETE", "PERFORMANCE",
		fmt.Sprintf("删除绩效考核：员工 %s，%s 月（原分数 %d）", employeeName, month, oldTotalScore),
		employeeID, employeeName)

	models.Send(c, 200, 0, "Performance deleted", nil)
}

// UpdatePenaltyRecord 更新罚款记录
func (h *AdminHandler) UpdatePenaltyRecord(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		Amount      float64 `json:"amount"`
		Category    string  `json:"category"`
		Reason      string  `json:"reason"`
		PenaltyDate string  `json:"penalty_date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	// 获取旧值用于日志
	var oldAmount float64
	var oldCategory, oldReason, employeeName string
	err := h.db.QueryRow(`
        SELECT employee_name, amount, category, reason 
        FROM penalty_records WHERE id = $1
    `, id).Scan(&employeeName, &oldAmount, &oldCategory, &oldReason)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// 执行更新
	_, err = h.db.Exec(`
        UPDATE penalty_records 
        SET amount = $1, category = $2, reason = $3, penalty_date = $4
        WHERE id = $5
    `, req.Amount, req.Category, req.Reason, req.PenaltyDate, id)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	// ✅ 修复：更新后重新查询最新的员工姓名
	var latestEmployeeName string
	err = h.db.QueryRow(`
        SELECT employee_name FROM penalty_records WHERE id = $1
    `, id).Scan(&latestEmployeeName)
	if err == nil && latestEmployeeName != "" {
		employeeName = latestEmployeeName // 使用最新的员工姓名
	}

	// 记录操作日志（现在使用最新数据）
	Log(c, "UPDATE", "PENALTY",
		fmt.Sprintf("修改罚款：员工 %s，金额 ¥%.2f→¥%.2f，类型 %s",
			employeeName, oldAmount, req.Amount, req.Category),
		id, employeeName)

	models.Send(c, 200, 0, "Penalty record updated", nil)
}

// TodayAbsenteeItem 今日异常考勤人员项
type TodayAbsenteeItem struct {
	EmployeeID   string `json:"employeeId"`
	EmployeeName string `json:"employeeName"`
	Position     string `json:"position"`
	Status       string `json:"status"`
	StatusLabel  string `json:"statusLabel"`
	Date         string `json:"date"`
}

// TodayAbsenteesResponse 今日异常考勤响应
type TodayAbsenteesResponse struct {
	Vacation []TodayAbsenteeItem `json:"vacation"` // 休假（全天+半天）
	Leave    []TodayAbsenteeItem `json:"leave"`    // 请假
	Absent   []TodayAbsenteeItem `json:"absent"`   // 旷工
	Total    int                 `json:"total"`
}

// GetTodayAbsentees 获取今日休假/请假/旷工人员列表
func (h *AdminHandler) GetTodayAbsentees(c *gin.Context) {
	today := time.Now().Format("2006-01-02")
	yearMonth := time.Now().Format("2006-01")

	// 查询今日有考勤记录且状态为休假/请假/旷工的员工
	query := `
		SELECT 
			e.id,
			e.employee_id,
			e.name,
			COALESCE(e.position, '') as position,
			ar.status,
			ar.date
		FROM employees e
		INNER JOIN attendance_records ar ON e.id = ar.employee_id
		WHERE ar.year_month = $1 
			AND ar.date = $2
			AND ar.status IN ('rest_full', 'rest_half', 'leave', 'absent')
		ORDER BY 
			CASE ar.status
				WHEN 'absent' THEN 1
				WHEN 'leave' THEN 2
				WHEN 'rest_full' THEN 3
				WHEN 'rest_half' THEN 4
				ELSE 5
			END,
			e.name
	`

	rows, err := h.db.QueryContext(c.Request.Context(), query, yearMonth, today)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	defer rows.Close()

	var vacationList []TodayAbsenteeItem
	var leaveList []TodayAbsenteeItem
	var absentList []TodayAbsenteeItem

	for rows.Next() {
		var id, employeeID, name, position, status, date string
		if err := rows.Scan(&id, &employeeID, &name, &position, &status, &date); err != nil {
			continue
		}

		item := TodayAbsenteeItem{
			EmployeeID:   employeeID,
			EmployeeName: name,
			Position:     position,
			Status:       status,
			StatusLabel:  getStatusLabel(status),
			Date:         date,
		}

		switch status {
		case "rest_full", "rest_half":
			vacationList = append(vacationList, item)
		case "leave":
			leaveList = append(leaveList, item)
		case "absent":
			absentList = append(absentList, item)
		}
	}

	response := TodayAbsenteesResponse{
		Vacation: vacationList,
		Leave:    leaveList,
		Absent:   absentList,
		Total:    len(vacationList) + len(leaveList) + len(absentList),
	}

	models.Send(c, 200, 0, "success", response)
}

// GetTodayAttendanceRate 统计当天出勤率（全员，不受分页影响）
func (h *AdminHandler) GetTodayAttendanceRate(c *gin.Context) {
	today := time.Now().Format("2006-01-02")
	yearMonth := time.Now().Format("2006-01")

	query := `
		WITH eligible AS (
			SELECT e.id
			FROM employees e
			WHERE e.status = 1
			  AND NOT EXISTS (
				SELECT 1 FROM attendance_records r
				WHERE r.employee_id = e.id
				  AND r.status = 'resigned'
				  AND r.year_month < $1
			  )
			  AND NOT EXISTS (
				SELECT 1 FROM attendance_records r
				WHERE r.employee_id = e.id
				  AND r.status = 'resigned'
				  AND r.year_month = $1
				  AND r.date <= $2
			  )
		)
		SELECT
			COUNT(eligible.id) AS total,
			COALESCE(SUM(
				CASE
					WHEN ar.status IN ('work', 'working') THEN 1.0
					WHEN ar.status IN ('rest_half', 'leave') THEN 0.5
					ELSE 0.0
				END
			), 0) AS points
		FROM eligible
		LEFT JOIN attendance_records ar
			ON ar.employee_id = eligible.id
			AND ar.year_month = $1
			AND ar.date = $2
	`

	var total int
	var points float64
	if err := h.db.QueryRowContext(c.Request.Context(), query, yearMonth, today).Scan(&total, &points); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	rate := 0
	if total > 0 {
		rate = int(math.Round(points / float64(total) * 100))
	}

	models.Send(c, 200, 0, "success", gin.H{
		"date":   today,
		"rate":   rate,
		"total":  total,
		"points": points,
	})
}

// getStatusLabel 获取状态的中文标签
func getStatusLabel(status string) string {
	switch status {
	case "rest_full":
		return "全天休假"
	case "rest_half":
		return "半天休假"
	case "leave":
		return "请假"
	case "absent":
		return "旷工"
	default:
		return status
	}
}

// ---- Source: backend/handlers/workforce.go ----
type WorkforceHandler struct {
	db         *sql.DB
	workforce  *services.WorkforceService
	telegram   *services.WorkforceTelegramService
	userRepo   *services.UserRepository
	deviceRepo *services.DeviceRepository
	wsHub      *services.WebSocketHub
}

func NewWorkforceHandler(db *sql.DB, workforce *services.WorkforceService, telegram *services.WorkforceTelegramService, userRepo *services.UserRepository, deviceRepo *services.DeviceRepository, wsHub *services.WebSocketHub) *WorkforceHandler {
	return &WorkforceHandler{db: db, workforce: workforce, telegram: telegram, userRepo: userRepo, deviceRepo: deviceRepo, wsHub: wsHub}
}

// CreateEmployeeAccount orchestrates user + employee + invite + today attendance.
func (h *WorkforceHandler) CreateEmployeeAccount(c *gin.Context) {
	var req struct {
		Username     string `json:"username" binding:"required"`
		Password     string `json:"password" binding:"required,min=6"`
		EmployeeID   string `json:"employee_id"`
		Name         string `json:"name" binding:"required"`
		Position     string `json:"position"`
		Department   string `json:"department"`
		HireDate     string `json:"hire_date"`
		WorkLocation string `json:"work_location"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	createdBy := parseContextUserUUID(c)

	result, err := h.workforce.CreateEmployeeAccount(c.Request.Context(), &services.CreateEmployeeAccountInput{
		Username:     req.Username,
		Password:     req.Password,
		EmployeeID:   req.EmployeeID,
		Name:         req.Name,
		Position:     req.Position,
		Department:   req.Department,
		HireDate:     req.HireDate,
		WorkLocation: req.WorkLocation,
		CreatedBy:    createdBy,
	})
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			models.SendError(c, 400, 1001, "username or employee id already exists")
			return
		}
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	Log(c, "CREATE", "EMPLOYEE_ACCOUNT",
		fmt.Sprintf("创建员工子账号：%s（登录名 %s）", req.Name, req.Username),
		result.EmployeeID.String(), req.Name)

	models.Send(c, 200, 0, "Employee account created", gin.H{
		"userId":     result.UserID,
		"employeeId": result.EmployeeID,
		"inviteCode": result.InviteCode,
		"inviteId":   result.InviteID,
	})
}
func inviteCodeRowJSON(row services.InviteCodeRow) gin.H {
	disposition := services.InviteDisposition(row, time.Now())
	// status 必须是机器可读枚举（active/used/...），供前端筛选与统计；
	// 中文文案放在 dispositionLabel，避免 status==="active" 永远匹配失败导致统计全 0。
	status := disposition
	if status == "superseded" {
		status = "revoked"
	}
	return gin.H{
		"id":               row.ID,
		"code":             row.Code,
		"employeeId":       row.EmployeeID,
		"employeeName":     row.EmployeeName,
		"employeeCode":     row.EmployeeCode,
		"status":           status,
		"disposition":      disposition,
		"dispositionLabel": services.InviteDispositionLabel(disposition),
		"expiresAt":        row.ExpiresAt,
		"usedAt":           row.UsedAt,
		"revokedAt":        row.RevokedAt,
		"supersededAt":     row.SupersededAt,
		"createdAt":        row.CreatedAt,
		"deviceId":         row.DeviceID,
	}
}
func (h *WorkforceHandler) ListInviteCodes(c *gin.Context) {
	var employeeFilter *uuid.UUID
	if eid := c.Query("employee_id"); eid != "" {
		if id, err := uuid.Parse(eid); err == nil {
			employeeFilter = &id
		}
	}

	rows, err := h.workforce.ListCurrentInviteCodes(c.Request.Context(), employeeFilter)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, inviteCodeRowJSON(row))
	}
	models.Send(c, 200, 0, "success", gin.H{"items": items})
}
func (h *WorkforceHandler) GetInviteCodeSettings(c *gin.Context) {
	cfg, err := h.workforce.GetInviteCodeSettings(c.Request.Context())
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", cfg)
}
func (h *WorkforceHandler) UpdateInviteCodeSettings(c *gin.Context) {
	var req struct {
		ValidDays int `json:"validDays" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, "Invalid request")
		return
	}
	if req.ValidDays < 1 || req.ValidDays > 365 {
		models.SendError(c, 400, 1001, "validDays must be between 1 and 365")
		return
	}
	cfg, err := h.workforce.SaveInviteCodeSettings(c.Request.Context(), req.ValidDays)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "UPDATE", "INVITE_CODE",
		fmt.Sprintf("更新邀请码默认有效期为 %d 天", cfg.ValidDays), "", "")
	models.Send(c, 200, 0, "success", cfg)
}
func (h *WorkforceHandler) ListInviteCodeHistory(c *gin.Context) {
	var employeeFilter *uuid.UUID
	if eid := c.Query("employee_id"); eid != "" {
		if id, err := uuid.Parse(eid); err == nil {
			employeeFilter = &id
		}
	}

	rows, err := h.workforce.ListInviteCodeHistory(c.Request.Context(), employeeFilter, 500)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}

	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		item := inviteCodeRowJSON(row)
		item["eventLabel"] = inviteCodeEventLabel(row)
		items = append(items, item)
	}
	models.Send(c, 200, 0, "success", gin.H{"items": items})
}
func inviteCodeEventLabel(row services.InviteCodeRow) string {
	now := time.Now()
	disposition := services.InviteDisposition(row, now)
	switch disposition {
	case "active":
		return "新增"
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
func (h *WorkforceHandler) RevokeInviteCode(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid invite id")
		return
	}
	if err := h.workforce.RevokeInviteCode(c.Request.Context(), id); err != nil {
		if errors.Is(err, services.ErrInviteNotFound) {
			models.SendError(c, 404, 2001, "invite not found or already used")
			return
		}
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "UPDATE", "INVITE_CODE", "作废邀请码", id.String(), id.String())
	models.Send(c, 200, 0, "Invite revoked", nil)
}
func (h *WorkforceHandler) RegenerateInviteCode(c *gin.Context) {
	employeeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid employee id")
		return
	}
	createdBy := parseContextUserUUID(c)
	row, err := h.workforce.RegenerateInviteCode(c.Request.Context(), employeeID, createdBy)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "CREATE", "INVITE_CODE", fmt.Sprintf("重新生成邀请码（员工 %s）", employeeID), row.ID.String(), employeeID.String())
	item := inviteCodeRowJSON(*row)
	models.Send(c, 200, 0, "Invite regenerated", gin.H{
		"code":     row.Code,
		"inviteId": row.ID,
		"item":     item,
	})
}
func (h *WorkforceHandler) CheckOut(c *gin.Context) {
	deviceID, err := uuid.Parse(c.GetString("deviceID"))
	if err != nil {
		models.SendError(c, 401, 1002, "device unauthorized")
		return
	}
	if h.telegram != nil {
		if err := h.telegram.RejectIfWebPunchOnly(c.Request.Context()); err != nil {
			models.SendError(c, 403, 1003, err.Error())
			return
		}
	}
	var req struct {
		SessionID string `json:"sessionId"`
	}
	_ = c.ShouldBindJSON(&req)
	var sid *uuid.UUID
	if req.SessionID != "" {
		if id, err := uuid.Parse(req.SessionID); err == nil {
			sid = &id
		}
	}
	result, err := h.workforce.CheckOut(c.Request.Context(), deviceID, sid)
	if err != nil {
		if errors.Is(err, services.ErrNoOpenSession) {
			models.SendError(c, 404, 2001, "no open work session")
			return
		}
		if errors.Is(err, services.ErrOpenActivityBlocksCheckout) {
			models.SendError(c, 409, 1004, "请先回座结束活动后再下班")
			return
		}
		if errors.Is(err, services.ErrWorkSessionOpenOnTG) {
			models.SendError(c, 409, 1004, "Telegram 已上班，请先在群内下班")
			return
		}
		if errors.Is(err, services.ErrWorkSessionOpenOnPC) {
			models.SendError(c, 409, 1004, "PC 客户端已上班，请先在客户端下班")
			return
		}
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	if h.wsHub != nil {
		h.wsHub.BroadcastToAdmin(gin.H{
			"type": "employee.checkout", "deviceId": deviceID.String(),
			"sessionId": result.SessionID.String(),
		})
	}
	if h.telegram != nil {
		if empID, err := h.workforce.ResolveEmployeeByDeviceID(c.Request.Context(), deviceID); err == nil {
			shift := result.ShiftType
			go h.telegram.NotifyAgentWorkEvent(context.Background(), empID, deviceID, shift, "下班", result.SessionID)
		}
	}
	models.Send(c, 200, 0, "checked out", gin.H{"sessionId": result.SessionID})
}
func (h *WorkforceHandler) DisableEmployee(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid employee id")
		return
	}
	if err := h.workforce.DisableEmployee(c.Request.Context(), id); err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	var userIDStr sql.NullString
	_ = h.db.QueryRowContext(c.Request.Context(), `SELECT user_id::text FROM employees WHERE id = $1`, id).Scan(&userIDStr)
	if userIDStr.Valid && strings.TrimSpace(userIDStr.String) != "" {
		if uid, parseErr := uuid.Parse(strings.TrimSpace(userIDStr.String)); parseErr == nil {
			_ = revokeUserSessions(c.Request.Context(), uid, "user_disabled")
		}
	}
	Log(c, "UPDATE", "EMPLOYEE", "禁用员工（软删除）", id.String(), id.String())
	models.Send(c, 200, 0, "Employee disabled", nil)
}
func parseContextUserUUID(c *gin.Context) uuid.UUID {
	raw := strings.TrimSpace(c.GetString("userID"))
	if raw == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// ---- Source: backend/handlers/workforce_telegram.go ----
type WorkforceTelegramHandler struct {
	db       *sql.DB
	telegram *services.WorkforceTelegramService
	wsHub    *services.WebSocketHub
}

func NewWorkforceTelegramHandler(db *sql.DB, telegram *services.WorkforceTelegramService, wsHub *services.WebSocketHub) *WorkforceTelegramHandler {
	return &WorkforceTelegramHandler{db: db, telegram: telegram, wsHub: wsHub}
}

func (h *WorkforceTelegramHandler) GetSettings(c *gin.Context) {
	settings, err := h.telegram.GetSettings(c.Request.Context())
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{
		"id":                               settings.ID,
		"enabled":                          settings.Enabled,
		"botUsername":                      settings.BotUsername,
		"botId":                            settings.BotID,
		"hasBotToken":                      settings.HasBotToken,
		"timezone":                         settings.Timezone,
		"dayStart":                         settings.DayStart,
		"dayEnd":                           settings.DayEnd,
		"graceMinutes":                     settings.GraceMinutes,
		"graceMinutesAfter":                settings.GraceMinutesAfter,
		"checkInGraceBefore":               settings.GraceMinutes,
		"checkInGraceAfter":                settings.GraceMinutesAfter,
		"checkOutGraceBefore":              settings.CheckOutGraceBefore,
		"checkOutGraceAfter":               settings.WorkEndGraceAfter,
		"lateToleranceMinutes":             settings.LateToleranceMinutes,
		"earlyLeaveToleranceMinutes":       settings.EarlyLeaveToleranceMinutes,
		"workEndGraceAfter":                settings.WorkEndGraceAfter,
		"dualShiftEnabled":                 settings.DualShiftEnabled,
		"tripleShiftEnabled":               settings.TripleShiftEnabled,
		"nightStart":                       settings.NightStart,
		"nightEnd":                         settings.NightEnd,
		"middleStart":                      settings.MiddleStart,
		"middleEnd":                        settings.MiddleEnd,
		"dailyResetTime":                   settings.DailyResetTime,
		"dailyResetEnabled":                settings.DailyResetEnabled,
		"dailyResetExecutionOffsetMinutes": settings.DailyResetExecutionOffsetMinutes,
		"shiftWindowDisabled":              settings.ShiftWindowDisabled,
		"workEndGraceMinutes":              settings.EarlyLeaveToleranceMinutes,
		"requirePCLogin":                   settings.RequirePCLogin,
		"webPunchOnly":                     settings.WebPunchOnly,
		"adminTelegramUserIds":             settings.AdminTelegramUserIDs,
		"botMode":                          settings.BotMode,
		"webhookUrl":                       settings.WebhookURL,
		"hasWebhookSecret":                 settings.HasWebhookSecret,
		"botUiLang":                        settings.BotUILang,
		"fineCurrency":                     settings.FineCurrency,
		"showKeyboardInfoButtons":          settings.ShowKeyboardInfoButtons,
		"updatedAt":                        models.FormatUTC(settings.UpdatedAt),
		"moduleEnabled":                    services.TelegramAttendanceEnabled(),
		"captureLinkedToCheckin":           services.CaptureLinkedToCheckin(c.Request.Context(), h.db),
		"captureLinkedEnvOverride": func() bool {
			_, explicit := services.CaptureLinkedToCheckinEnvOverride()
			return explicit
		}(),
	})
}
func (h *WorkforceTelegramHandler) UpdateSettings(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, "Invalid request")
		return
	}
	settings, err := h.telegram.UpdateSettings(c.Request.Context(), req)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "更新 Telegram 考勤配置", settings.ID.String(), "Telegram")
	models.Send(c, 200, 0, "success", gin.H{
		"enabled":                          settings.Enabled,
		"botUsername":                      settings.BotUsername,
		"botId":                            settings.BotID,
		"hasBotToken":                      settings.HasBotToken,
		"timezone":                         settings.Timezone,
		"dayStart":                         settings.DayStart,
		"dayEnd":                           settings.DayEnd,
		"graceMinutes":                     settings.GraceMinutes,
		"graceMinutesAfter":                settings.GraceMinutesAfter,
		"checkInGraceBefore":               settings.GraceMinutes,
		"checkInGraceAfter":                settings.GraceMinutesAfter,
		"checkOutGraceBefore":              settings.CheckOutGraceBefore,
		"checkOutGraceAfter":               settings.WorkEndGraceAfter,
		"lateToleranceMinutes":             settings.LateToleranceMinutes,
		"earlyLeaveToleranceMinutes":       settings.EarlyLeaveToleranceMinutes,
		"workEndGraceAfter":                settings.WorkEndGraceAfter,
		"dualShiftEnabled":                 settings.DualShiftEnabled,
		"tripleShiftEnabled":               settings.TripleShiftEnabled,
		"nightStart":                       settings.NightStart,
		"nightEnd":                         settings.NightEnd,
		"middleStart":                      settings.MiddleStart,
		"middleEnd":                        settings.MiddleEnd,
		"dailyResetTime":                   settings.DailyResetTime,
		"dailyResetEnabled":                settings.DailyResetEnabled,
		"dailyResetExecutionOffsetMinutes": settings.DailyResetExecutionOffsetMinutes,
		"shiftWindowDisabled":              settings.ShiftWindowDisabled,
		"workEndGraceMinutes":              settings.EarlyLeaveToleranceMinutes,
		"requirePCLogin":                   settings.RequirePCLogin,
		"webPunchOnly":                     settings.WebPunchOnly,
		"captureLinkedToCheckin":           settings.CaptureLinkedToCheckin,
		"adminTelegramUserIds":             settings.AdminTelegramUserIDs,
		"botMode":                          settings.BotMode,
		"webhookUrl":                       settings.WebhookURL,
		"hasWebhookSecret":                 settings.HasWebhookSecret,
		"botUiLang":                        settings.BotUILang,
		"fineCurrency":                     settings.FineCurrency,
		"showKeyboardInfoButtons":          settings.ShowKeyboardInfoButtons,
		"updatedAt":                        models.FormatUTC(settings.UpdatedAt),
	})
}
func (h *WorkforceTelegramHandler) RegenerateWebhookSecret(c *gin.Context) {
	settings, err := h.telegram.UpdateSettings(c.Request.Context(), map[string]interface{}{
		"regenerateWebhookSecret": true,
	})
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	secret, err := h.telegram.GetWebhookSecret(c.Request.Context())
	if err != nil || secret == "" {
		models.SendError(c, 500, 5000, "failed to generate webhook secret")
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "重新生成 Telegram Webhook Secret", settings.ID.String(), "Telegram")
	models.Send(c, 200, 0, "success", gin.H{"webhookSecret": secret})
}
func (h *WorkforceTelegramHandler) GetWorkerConnection(c *gin.Context) {
	info, err := h.telegram.GetWorkerConnectionInfo(c.Request.Context())
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	resp := gin.H{
		"online":           info.Online,
		"hasWorkerSecret":  info.HasWorkerSecret,
		"hasActivePairing": info.HasActivePairing,
		"embedded":         services.TelegramBotEmbedded(),
	}
	if info.LastSeenAt != nil {
		resp["lastSeenAt"] = models.FormatUTC(*info.LastSeenAt)
	}
	if info.PairingExpiresAt != nil {
		resp["pairingExpiresAt"] = models.FormatUTC(*info.PairingExpiresAt)
	}
	models.Send(c, 200, 0, "success", resp)
}
func (h *WorkforceTelegramHandler) WorkerHeartbeat(c *gin.Context) {
	if err := h.telegram.TouchWorkerHeartbeat(c.Request.Context()); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) DiscoverGroups(c *gin.Context) {
	items, err := h.telegram.DiscoverGroups(c.Request.Context())
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, gin.H{
			"chatId":    item.ChatID,
			"groupName": item.ChatTitle,
			"chatTitle": item.ChatTitle,
			"chatType":  item.ChatType,
			"bound":     item.Bound,
			"source":    item.Source,
		})
	}
	models.Send(c, 200, 0, "success", out)
}
func (h *WorkforceTelegramHandler) WorkerRecordChatSeen(c *gin.Context) {
	var req struct {
		ChatID    string `json:"chatId" binding:"required"`
		ChatTitle string `json:"chatTitle"`
		ChatType  string `json:"chatType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	firstSeen, err := h.telegram.RecordDiscoveredChat(c.Request.Context(), req.ChatID, req.ChatTitle, req.ChatType)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"firstSeen": firstSeen})
}
func (h *WorkforceTelegramHandler) ListGroups(c *gin.Context) {
	items, err := h.telegram.ListGroups(c.Request.Context())
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		row := gin.H{
			"id":        item.ID,
			"chatId":    item.ChatID,
			"chatTitle": item.ChatTitle,
			"enabled":   item.Enabled,
			"createdAt": models.FormatUTC(item.CreatedAt),
		}
		if item.ShiftGraceBefore != nil {
			row["shiftGraceBefore"] = *item.ShiftGraceBefore
		}
		if item.ShiftGraceAfter != nil {
			row["shiftGraceAfter"] = *item.ShiftGraceAfter
		}
		if item.CheckOutGraceBefore != nil {
			row["checkOutGraceBefore"] = *item.CheckOutGraceBefore
			row["workEndGraceBefore"] = *item.CheckOutGraceBefore
		}
		if item.CheckOutGraceAfter != nil {
			row["checkOutGraceAfter"] = *item.CheckOutGraceAfter
			row["workEndGraceAfter"] = *item.CheckOutGraceAfter
		}
		if item.LateToleranceMinutes != nil {
			row["lateToleranceMinutes"] = *item.LateToleranceMinutes
		}
		if item.EarlyLeaveToleranceMinutes != nil {
			row["earlyLeaveToleranceMinutes"] = *item.EarlyLeaveToleranceMinutes
		}
		if item.DailyResetTime != "" {
			row["dailyResetTime"] = item.DailyResetTime
		}
		if item.BotUILang != "" {
			row["botUiLang"] = item.BotUILang
		}
		out = append(out, row)
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) UpsertGroup(c *gin.Context) {
	var req struct {
		ChatID                     string `json:"chatId" binding:"required"`
		ChatTitle                  string `json:"chatTitle"`
		Enabled                    *bool  `json:"enabled"`
		ShiftGraceBefore           *int   `json:"shiftGraceBefore"`
		ShiftGraceAfter            *int   `json:"shiftGraceAfter"`
		CheckInGraceBefore         *int   `json:"checkInGraceBefore"`
		CheckInGraceAfter          *int   `json:"checkInGraceAfter"`
		CheckOutGraceBefore        *int   `json:"checkOutGraceBefore"`
		CheckOutGraceAfter         *int   `json:"checkOutGraceAfter"`
		WorkEndGraceBefore         *int   `json:"workEndGraceBefore"`
		WorkEndGraceAfter          *int   `json:"workEndGraceAfter"`
		LateToleranceMinutes       *int   `json:"lateToleranceMinutes"`
		EarlyLeaveToleranceMinutes *int   `json:"earlyLeaveToleranceMinutes"`
		DailyResetTime             string `json:"dailyResetTime"`
		BotUILang                  string `json:"botUiLang"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	overrides := map[string]interface{}{}
	if req.ShiftGraceBefore != nil {
		overrides["shiftGraceBefore"] = *req.ShiftGraceBefore
	}
	if req.CheckInGraceBefore != nil {
		overrides["checkInGraceBefore"] = *req.CheckInGraceBefore
	}
	if req.ShiftGraceAfter != nil {
		overrides["shiftGraceAfter"] = *req.ShiftGraceAfter
	}
	if req.CheckInGraceAfter != nil {
		overrides["checkInGraceAfter"] = *req.CheckInGraceAfter
	}
	if req.CheckOutGraceBefore != nil {
		overrides["checkOutGraceBefore"] = *req.CheckOutGraceBefore
	}
	if req.WorkEndGraceBefore != nil {
		overrides["workEndGraceBefore"] = *req.WorkEndGraceBefore
	}
	if req.CheckOutGraceAfter != nil {
		overrides["checkOutGraceAfter"] = *req.CheckOutGraceAfter
	}
	if req.WorkEndGraceAfter != nil {
		overrides["workEndGraceAfter"] = *req.WorkEndGraceAfter
	}
	if req.LateToleranceMinutes != nil {
		overrides["lateToleranceMinutes"] = *req.LateToleranceMinutes
	}
	if req.EarlyLeaveToleranceMinutes != nil {
		overrides["earlyLeaveToleranceMinutes"] = *req.EarlyLeaveToleranceMinutes
	}
	if strings.TrimSpace(req.DailyResetTime) != "" {
		overrides["dailyResetTime"] = strings.TrimSpace(req.DailyResetTime)
	}
	if strings.TrimSpace(req.BotUILang) != "" {
		overrides["botUiLang"] = strings.TrimSpace(req.BotUILang)
	}
	row, err := h.telegram.UpsertGroup(c.Request.Context(), req.ChatID, req.ChatTitle, enabled, overrides)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", fmt.Sprintf("绑定 Telegram 群 %s", req.ChatID), row.ID.String(), req.ChatTitle)
	resp := gin.H{
		"id": row.ID, "chatId": row.ChatID, "chatTitle": row.ChatTitle, "enabled": row.Enabled,
	}
	if row.ShiftGraceBefore != nil {
		resp["shiftGraceBefore"] = *row.ShiftGraceBefore
	}
	if row.ShiftGraceAfter != nil {
		resp["shiftGraceAfter"] = *row.ShiftGraceAfter
	}
	if row.CheckOutGraceBefore != nil {
		resp["checkOutGraceBefore"] = *row.CheckOutGraceBefore
		resp["workEndGraceBefore"] = *row.CheckOutGraceBefore
	}
	if row.CheckOutGraceAfter != nil {
		resp["checkOutGraceAfter"] = *row.CheckOutGraceAfter
		resp["workEndGraceAfter"] = *row.CheckOutGraceAfter
	}
	if row.LateToleranceMinutes != nil {
		resp["lateToleranceMinutes"] = *row.LateToleranceMinutes
	}
	if row.EarlyLeaveToleranceMinutes != nil {
		resp["earlyLeaveToleranceMinutes"] = *row.EarlyLeaveToleranceMinutes
	}
	if row.DailyResetTime != "" {
		resp["dailyResetTime"] = row.DailyResetTime
	}
	if row.BotUILang != "" {
		resp["botUiLang"] = row.BotUILang
	}
	models.Send(c, 200, 0, "success", resp)
}
func (h *WorkforceTelegramHandler) DeleteGroup(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid id")
		return
	}
	if err := h.telegram.DeleteGroup(c.Request.Context(), id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "DELETE", "TELEGRAM_ATTENDANCE", "删除 Telegram 群绑定", id.String(), id.String())
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) ListBindings(c *gin.Context) {
	items, err := h.telegram.ListBindings(c.Request.Context(), c.Query("search"))
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, gin.H{
			"id":               item.ID,
			"employeeId":       item.EmployeeID,
			"employeeName":     item.EmployeeName,
			"employeeCode":     item.EmployeeCode,
			"attendanceMode":   item.AttendanceMode,
			"channel":          services.TelegramChannelPersonal,
			"telegramUserId":   item.TelegramUserID,
			"telegramUsername": item.TelegramUsername,
			"chatId":           item.ChatID,
			"boundAt":          models.FormatUTC(item.BoundAt),
		})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) CreateBinding(c *gin.Context) {
	var req struct {
		EmployeeID       string `json:"employeeId" binding:"required"`
		TelegramUserID   int64  `json:"telegramUserId" binding:"required"`
		TelegramUsername string `json:"telegramUsername"`
		ChatID           string `json:"chatId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	empID, err := uuid.Parse(req.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 1001, "invalid employee id")
		return
	}
	row, err := h.telegram.CreateBinding(c.Request.Context(), empID, req.TelegramUserID, req.TelegramUsername, req.ChatID)
	if err != nil {
		if errors.Is(err, services.ErrEmployeeNotFound) {
			models.SendError(c, 404, 2001, "员工不存在")
			return
		}
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			models.SendError(c, 400, 1001, "该 Telegram 账号或员工已绑定")
			return
		}
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("绑定 Telegram：%s ↔ %d", row.EmployeeName, row.TelegramUserID),
		row.ID.String(), row.EmployeeName)
	models.Send(c, 200, 0, "success", gin.H{
		"id": row.ID, "employeeId": row.EmployeeID, "employeeName": row.EmployeeName,
		"telegramUserId": row.TelegramUserID, "telegramUsername": row.TelegramUsername,
	})
}
func (h *WorkforceTelegramHandler) DeleteBinding(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid id")
		return
	}
	if err := h.telegram.DeleteBinding(c.Request.Context(), id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "DELETE", "TELEGRAM_ATTENDANCE", "解除 Telegram 绑定", id.String(), id.String())
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) ResetBindingDaily(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid id")
		return
	}
	binding, err := h.telegram.GetBindingByID(c.Request.Context(), id)
	if err != nil {
		models.SendError(c, 404, 2001, "binding not found")
		return
	}
	result, err := h.telegram.ResetEmployeeDailyAttendance(c.Request.Context(), binding.EmployeeID, binding.ChatID)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("重置 %s 当日考勤（业务日 %s）", binding.EmployeeName, result.BusinessDate),
		id.String(), binding.EmployeeName)
	models.Send(c, 200, 0, "success", result)
}
func (h *WorkforceTelegramHandler) ResetEmployeeDaily(c *gin.Context) {
	var req struct {
		EmployeeID string `json:"employeeId" binding:"required"`
		ChatID     string `json:"chatId"`
		DeviceID   string `json:"deviceId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	empID, err := uuid.Parse(req.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 1001, "invalid employee id")
		return
	}
	chatID := strings.TrimSpace(req.ChatID)
	if chatID == "" && strings.TrimSpace(req.DeviceID) != "" {
		if deviceID, parseErr := uuid.Parse(req.DeviceID); parseErr == nil {
			_, _, chatID, _ = h.telegram.GetDeviceTelegramBindingByDevice(c.Request.Context(), deviceID)
		}
	}
	var empName string
	_ = h.db.QueryRowContext(c.Request.Context(), `SELECT name FROM employees WHERE id = $1`, empID).Scan(&empName)
	result, err := h.telegram.ResetEmployeeDailyAttendance(c.Request.Context(), empID, chatID)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("重置 %s 当日考勤（业务日 %s）", empName, result.BusinessDate),
		empID.String(), empName)
	models.Send(c, 200, 0, "success", result)
}
func (h *WorkforceTelegramHandler) WorkerBind(c *gin.Context) {
	var req struct {
		TelegramUserID   int64  `json:"telegramUserId" binding:"required"`
		TelegramUsername string `json:"telegramUsername"`
		InviteCode       string `json:"inviteCode"`
		BindToken        string `json:"bindToken"`
		Identifier       string `json:"identifier"`
		Username         string `json:"username"`
		Password         string `json:"password"`
		ChatID           string `json:"chatId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	if req.Identifier != "" {
		var account string
		err := h.db.QueryRowContext(c.Request.Context(), `SELECT username FROM users WHERE username=$1`, req.Identifier).Scan(&account)
		if err == nil {
			req.Username = account
			req.InviteCode = ""
			req.BindToken = ""
		} else if err != sql.ErrNoRows {
			models.SendInternalError(c, err)
			return
		}
	}
	row, err := h.telegram.BindTelegramSelf(c.Request.Context(), req.TelegramUserID, req.TelegramUsername, req.InviteCode, req.BindToken, req.Username, req.Password, req.ChatID)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("自助绑定 Telegram（个人）：%s ↔ %d", row.EmployeeName, row.TelegramUserID),
		row.ID.String(), row.EmployeeName)
	models.Send(c, 200, 0, "success", gin.H{
		"channel":          services.TelegramChannelPersonal,
		"employeeId":       row.EmployeeID,
		"employeeName":     row.EmployeeName,
		"attendanceMode":   row.AttendanceMode,
		"telegramUserId":   row.TelegramUserID,
		"telegramUsername": row.TelegramUsername,
	})
}
func (h *WorkforceTelegramHandler) WorkerBindWorkstation(c *gin.Context) {
	var req struct {
		TelegramUserID   int64  `json:"telegramUserId" binding:"required"`
		TelegramUsername string `json:"telegramUsername"`
		InviteCode       string `json:"inviteCode" binding:"required"`
		ChatID           string `json:"chatId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	row, err := h.telegram.BindWorkstationTelegramByInviteCode(c.Request.Context(), req.TelegramUserID, req.TelegramUsername, req.InviteCode, req.ChatID)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("工位 TG 绑定：%s ↔ %d", row.DeviceName, row.TelegramUserID),
		row.ID.String(), row.DeviceName)
	models.Send(c, 200, 0, "success", gin.H{
		"channel":          services.TelegramChannelWorkstation,
		"deviceId":         row.DeviceID,
		"deviceName":       row.DeviceName,
		"telegramUserId":   row.TelegramUserID,
		"telegramUsername": row.TelegramUsername,
	})
}

// WorkerBindDevice binds multi-device shared-workstation TG (/bind_multi 账号 or /bind_multi 邀请码).
// Same as old repeated /bind 设备ID 邀请码: one Telegram covers every shared PC the employee belongs to.
func (h *WorkforceTelegramHandler) WorkerBindDevice(c *gin.Context) {
	var req struct {
		TelegramUserID   int64  `json:"telegramUserId" binding:"required"`
		TelegramUsername string `json:"telegramUsername"`
		Username         string `json:"username"`
		InviteCode       string `json:"inviteCode"`
		ChatID           string `json:"chatId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	username := strings.TrimSpace(req.Username)
	inviteCode := strings.TrimSpace(req.InviteCode)
	if username == "" && inviteCode == "" {
		models.SendError(c, 400, 1001, "用法：/bind_multi 账号 或 /bind_multi 邀请码")
		return
	}
	rows, err := h.telegram.BindWorkstationTelegramMulti(
		c.Request.Context(),
		req.TelegramUserID,
		req.TelegramUsername,
		inviteCode,
		username,
		req.ChatID,
	)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	if len(rows) == 0 {
		models.SendError(c, 400, 1001, "未绑定到任何共享工位设备")
		return
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.DeviceName)
		if name == "" {
			name = row.DeviceID.String()
		}
		names = append(names, name)
	}
	last := rows[len(rows)-1]
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("工位 TG 多设备绑定：%d 台 ↔ %d（%s）", len(rows), last.TelegramUserID, strings.Join(names, ", ")),
		last.ID.String(), last.DeviceName)
	models.Send(c, 200, 0, "success", gin.H{
		"channel":          services.TelegramChannelWorkstation,
		"deviceId":         last.DeviceID,
		"deviceName":       last.DeviceName,
		"deviceNames":      names,
		"deviceCount":      len(rows),
		"telegramUserId":   last.TelegramUserID,
		"telegramUsername": last.TelegramUsername,
		"replyKeyboard":    h.telegram.BuildWorkerReplyKeyboardForUser(c.Request.Context(), req.ChatID, req.TelegramUserID),
		"deviceTagLegend":  h.telegram.DeviceTagLegendForTelegram(c.Request.Context(), req.TelegramUserID),
	})
}
func (h *WorkforceTelegramHandler) WorkerStatus(c *gin.Context) {
	tgID, err := services.ParseTelegramUserID(c.Query("telegramUserId"))
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	binding, err := h.telegram.ResolveTelegramBinding(c.Request.Context(), tgID)
	if err != nil {
		if errors.Is(err, services.ErrTelegramNotBound) {
			models.Send(c, 200, 0, "success", gin.H{"bound": false})
			return
		}
		models.SendInternalError(c, err)
		return
	}
	if binding.Channel == services.TelegramChannelWorkstation {
		resp := gin.H{
			"bound": true, "channel": services.TelegramChannelWorkstation,
			"deviceId": binding.DeviceID, "deviceName": binding.DeviceName,
			"pcLoginRequired": binding.RequiresPCLogin || binding.EmployeeID == uuid.Nil,
			"workSessionOpen": false, "openShifts": []interface{}{},
		}
		if binding.EmployeeID != uuid.Nil {
			var name, empCode string
			_ = h.db.QueryRowContext(c.Request.Context(), `
				SELECT name, COALESCE(employee_id,'') FROM employees WHERE id = $1`, binding.EmployeeID).Scan(&name, &empCode)
			resp["employeeId"] = binding.EmployeeID
			resp["employeeName"] = name
			resp["employeeCode"] = empCode
			open, sessionID, _ := h.telegram.GetEmployeeSessionStatus(c.Request.Context(), binding.EmployeeID)
			resp["workSessionOpen"] = open
			if sessionID != nil {
				resp["workSessionId"] = sessionID.String()
			}
			openShifts, _ := h.telegram.GetEmployeeOpenShifts(c.Request.Context(), binding.EmployeeID)
			resp["openShifts"] = openShifts
		}
		models.Send(c, 200, 0, "success", resp)
		return
	}
	empID := binding.EmployeeID
	open, sessionID, err := h.telegram.GetEmployeeSessionStatus(c.Request.Context(), empID)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	openShifts, _ := h.telegram.GetEmployeeOpenShifts(c.Request.Context(), empID)
	var name, empCode, attendanceMode string
	_ = h.db.QueryRowContext(c.Request.Context(), `
		SELECT name, COALESCE(employee_id,''), COALESCE(NULLIF(TRIM(attendance_mode), ''), 'pc_tg')
		FROM employees WHERE id = $1`, empID).Scan(&name, &empCode, &attendanceMode)
	resp := gin.H{
		"bound": true, "channel": services.TelegramChannelPersonal,
		"employeeId": empID, "employeeName": name, "employeeCode": empCode,
		"attendanceMode":  attendanceMode,
		"workSessionOpen": open, "openShifts": openShifts,
	}
	if sessionID != nil {
		resp["workSessionId"] = sessionID.String()
	}
	if act, err := h.telegram.GetOpenActivitySession(c.Request.Context(), empID); err == nil {
		resp["currentActivity"] = act.ActivityName
		resp["activityStartedAt"] = models.FormatUTC(act.StartedAt)
	}
	var shiftType string
	_ = h.db.QueryRowContext(c.Request.Context(), `
		SELECT COALESCE(shift_type,'day') FROM work_sessions
		WHERE employee_id = $1 AND status = 'open' ORDER BY checkin_time DESC LIMIT 1`, empID).Scan(&shiftType)
	if shiftType != "" && open {
		resp["shiftType"] = shiftType
		resp["shiftLabel"] = services.ShiftLabelPublic(shiftType)
	}
	models.Send(c, 200, 0, "success", resp)
}

// WorkerPunchDevices lists the bound PCs that currently have an employee
// logged in. The bot shows an inline device picker only when more than one is
// returned.
func (h *WorkforceTelegramHandler) WorkerPunchDevices(c *gin.Context) {
	tgID, err := services.ParseTelegramUserID(c.Query("telegramUserId"))
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	items, err := h.telegram.ListTelegramPunchDeviceOptions(c.Request.Context(), tgID)
	if err != nil {
		h.sendTelegramWorkerError(c, err, tgID)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{
		"requiresSelection": len(items) > 1,
		"items":             items,
	})
}
func (h *WorkforceTelegramHandler) WorkerCheckIn(c *gin.Context) {
	var req struct {
		TelegramUserID int64  `json:"telegramUserId" binding:"required"`
		ChatID         string `json:"chatId"`
		Shift          string `json:"shift"`
		DeviceID       string `json:"deviceId"`
		DeviceHint     string `json:"deviceHint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	deviceID, err := services.ParseOptionalDeviceID(req.DeviceID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	result, empID, name, workFine, handoverHint, groupReplyHTML, err := h.telegram.TelegramCheckInWithDevice(
		c.Request.Context(), req.TelegramUserID, req.ChatID, req.Shift, deviceID, req.DeviceHint)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	if h.wsHub != nil {
		h.wsHub.BroadcastToAdmin(gin.H{
			"type":       "employee.telegram.checkin",
			"employeeId": empID.String(), "employeeName": name,
			"sessionId": result.SessionID.String(), "date": result.Date,
		})
	}
	models.Send(c, 200, 0, "checked in", gin.H{
		"sessionId": result.SessionID, "date": result.Date,
		"employeeId": empID, "employeeName": name, "shiftType": result.ShiftType,
		"shiftLabel": services.ShiftLabelPublic(result.ShiftType),
		"workFine":   workFine, "handoverHint": handoverHint,
		"groupReplyHtml": groupReplyHTML,
	})
}
func (h *WorkforceTelegramHandler) WorkerCheckOut(c *gin.Context) {
	var req struct {
		TelegramUserID int64  `json:"telegramUserId" binding:"required"`
		ChatID         string `json:"chatId"`
		DeviceID       string `json:"deviceId"`
		DeviceHint     string `json:"deviceHint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	deviceID, err := services.ParseOptionalDeviceID(req.DeviceID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	result, empID, name, workFine, handoverHint, activityWarn, groupReplyHTML, err := h.telegram.TelegramCheckOutWithDevice(
		c.Request.Context(), req.TelegramUserID, req.ChatID, deviceID, req.DeviceHint)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	if h.wsHub != nil {
		h.wsHub.BroadcastToAdmin(gin.H{
			"type":       "employee.telegram.checkout",
			"employeeId": empID.String(), "employeeName": name,
			"sessionId": result.SessionID.String(),
		})
	}
	models.Send(c, 200, 0, "checked out", gin.H{
		"sessionId": result.SessionID, "employeeId": empID, "employeeName": name,
		"shiftType": result.ShiftType, "shiftLabel": services.ShiftLabelPublic(result.ShiftType),
		"workFine": workFine, "handoverHint": handoverHint, "activityWarn": activityWarn,
		"groupReplyHtml": groupReplyHTML,
	})
}
func (h *WorkforceTelegramHandler) sendTelegramWorkerError(c *gin.Context, err error, telegramUserID int64) {
	mode := services.TgLangZh
	if h.telegram != nil && telegramUserID > 0 {
		mode = h.telegram.GetReplyLangForUser(c.Request.Context(), telegramUserID)
	}
	status, code := services.TelegramWorkerErrorHTTP(err)
	models.SendError(c, status, code, services.FormatTelegramWorkerError(err, mode))
}
func (h *WorkforceTelegramHandler) WorkerConfig(c *gin.Context) {
	ctx := services.WithTelegramRequestCache(c.Request.Context())
	settings, err := h.telegram.GetSettings(ctx)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	active, _ := h.telegram.IsActive(ctx)
	chatID := strings.TrimSpace(c.Query("chatId"))
	tgUID, _ := services.ParseTelegramUserID(c.Query("telegramUserId"))
	// Independent metadata reads overlap network latency; mutations remain serial.
	var types []services.TgActivityType
	var typeErr error
	var webhookSecret, token string
	var reads sync.WaitGroup
	reads.Add(3)
	go func() { defer reads.Done(); types, typeErr = h.telegram.ListEnabledActivityTypesForWorker(ctx) }()
	go func() {
		defer reads.Done()
		webhookSecret, token, _ = h.telegram.GetWorkerReplyCredentials(ctx)
	}()
	// The group's own language is needed after the identity lookup when the user
	// has no personal preference; read it alongside instead of after.
	go func() {
		defer reads.Done()
		if chatID != "" {
			_ = h.telegram.GetLangMode(ctx, chatID)
		}
	}()
	empID, _ := h.telegram.ResolveEmployeeByTelegramUserID(ctx, tgUID)
	empIDStr := ""
	replyLang := services.TgLangZh
	if empID != uuid.Nil {
		empIDStr = empID.String()
		replyLang = h.telegram.GetReplyLangForEmployee(ctx, empID)
	}
	keyboard := h.telegram.BuildWorkerReplyKeyboardForUser(ctx, chatID, tgUID)
	preferredLang := h.telegram.DisplayLangForTelegram(ctx, chatID, tgUID)
	reads.Wait()
	if typeErr != nil {
		models.SendInternalError(c, typeErr)
		return
	}
	actItems := make([]gin.H, 0, len(types))
	actNames := make([]string, 0, len(types))
	for _, t := range types {
		actItems = append(actItems, gin.H{"code": t.Code, "name": t.Name, "timeLimitSeconds": t.TimeLimitSeconds})
		if strings.TrimSpace(t.Name) != "" {
			actNames = append(actNames, t.Name)
		}
	}
	resp := gin.H{
		"active":               active,
		"dayStart":             settings.DayStart,
		"dayEnd":               settings.DayEnd,
		"nightStart":           settings.NightStart,
		"nightEnd":             settings.NightEnd,
		"graceMinutes":         settings.GraceMinutes,
		"dualShiftEnabled":     settings.DualShiftEnabled,
		"shiftWindowDisabled":  settings.ShiftWindowDisabled,
		"dailyResetTime":       settings.DailyResetTime,
		"botMode":              settings.BotMode,
		"webhookUrl":           settings.WebhookURL,
		"webhookSecret":        webhookSecret,
		"webhookPath":          "/telegram/webhook",
		"langMode":             settings.BotUILang,
		"buttonLookup":         h.telegram.BuildButtonLookup(ctx, actNames),
		"botCommands":          h.telegram.BuildActivityBotCommands(ctx),
		"adminTelegramUserIds": settings.AdminTelegramUserIDs,
		"activities":           actItems,
		"replyKeyboard":        keyboard,
		"deviceTagLegend":      h.telegram.DeviceTagLegendForTelegram(ctx, tgUID),
		"preferredLang":        string(preferredLang),
		"replyLang":            string(replyLang),
		"employeeId":           empIDStr,
	}
	if token != "" {
		resp["botToken"] = token
	}
	models.Send(c, http.StatusOK, 0, "success", resp)
}
func sessionToJSON(s *services.TgActivitySession) gin.H {
	if s == nil {
		return nil
	}
	elapsed := s.ElapsedSeconds
	// Open sessions keep elapsed_seconds=0 in DB until close; expose live elapsed for UI countdown.
	if strings.EqualFold(strings.TrimSpace(s.Status), "open") && !s.StartedAt.IsZero() {
		live := int(time.Since(s.StartedAt).Seconds())
		if live < 0 {
			live = 0
		}
		elapsed = live
	}
	item := gin.H{
		"id": s.ID, "sessionId": s.ID.String(), "employeeId": s.EmployeeID, "employeeName": s.EmployeeName,
		"activityCode": s.ActivityCode, "activityName": s.ActivityName,
		"attendanceDate": s.AttendanceDate, "startedAt": models.FormatUTC(s.StartedAt),
		"startSource": s.StartSource, "endSource": s.EndSource,
		"elapsedSeconds": elapsed, "limitSeconds": s.LimitSeconds,
		"overtimeSeconds": s.OvertimeSeconds, "fineAmount": s.FineAmount, "status": s.Status,
	}
	if s.EndedAt != nil {
		item["endedAt"] = models.FormatUTC(*s.EndedAt)
	}
	if s.GroupReplyHTML != "" {
		item["groupReplyHtml"] = s.GroupReplyHTML
		item["groupReplyChatId"] = s.GroupReplyChatID
	}
	return item
}
func (h *WorkforceTelegramHandler) ListActivityTypes(c *gin.Context) {
	items, err := h.telegram.ListActivityTypes(c.Request.Context())
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, t := range items {
		out = append(out, gin.H{
			"id": t.ID, "code": t.Code, "name": t.Name,
			"maxTimesPerDay": t.MaxTimesPerDay, "maxConcurrent": t.MaxConcurrent,
			"timeLimitSeconds": t.TimeLimitSeconds,
			"sortOrder":        t.SortOrder, "enabled": t.Enabled,
		})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) UpsertActivityType(c *gin.Context) {
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, "Invalid request")
		return
	}
	row, err := h.telegram.UpsertActivityType(c.Request.Context(), req)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "保存 Telegram 活动类型："+row.Name, row.Code, row.Name)
	models.Send(c, 200, 0, "success", gin.H{
		"code": row.Code, "name": row.Name, "maxTimesPerDay": row.MaxTimesPerDay,
		"maxConcurrent":    row.MaxConcurrent,
		"timeLimitSeconds": row.TimeLimitSeconds, "enabled": row.Enabled,
	})
}
func (h *WorkforceTelegramHandler) DeleteActivityType(c *gin.Context) {
	code := c.Param("code")
	if err := h.telegram.DeleteActivityType(c.Request.Context(), code); err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "DELETE", "TELEGRAM_ATTENDANCE", "删除 Telegram 活动类型："+code, code, code)
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) ListActivityFines(c *gin.Context) {
	items, err := h.telegram.ListActivityFines(c.Request.Context(), c.Query("activityCode"))
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, f := range items {
		out = append(out, gin.H{
			"id": f.ID, "activityCode": f.ActivityCode,
			"segmentMinutes": f.SegmentMinutes, "fineAmount": f.FineAmount,
		})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) UpsertActivityFine(c *gin.Context) {
	var req struct {
		ActivityCode   string  `json:"activityCode" binding:"required"`
		SegmentMinutes int     `json:"segmentMinutes" binding:"required"`
		FineAmount     float64 `json:"fineAmount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	row, err := h.telegram.UpsertActivityFine(c.Request.Context(), req.ActivityCode, req.SegmentMinutes, req.FineAmount)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("新增活动超时罚款规则：%s / %d 分钟", req.ActivityCode, req.SegmentMinutes),
		row.ID.String(), req.ActivityCode)
	models.Send(c, 200, 0, "success", gin.H{
		"id": row.ID, "activityCode": row.ActivityCode,
		"segmentMinutes": row.SegmentMinutes, "fineAmount": row.FineAmount,
	})
}
func (h *WorkforceTelegramHandler) BatchUpsertActivityFines(c *gin.Context) {
	var req struct {
		ActivityCode  string   `json:"activityCode"`
		ActivityCodes []string `json:"activityCodes"`
		Items         []struct {
			SegmentMinutes int     `json:"segmentMinutes" binding:"required"`
			FineAmount     float64 `json:"fineAmount"`
		} `json:"items" binding:"required,min=1,dive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	codes := append([]string{}, req.ActivityCodes...)
	if strings.TrimSpace(req.ActivityCode) != "" {
		codes = append(codes, req.ActivityCode)
	}
	batchItems := make([]services.TgActivityFineBatchItem, 0, len(req.Items))
	for _, item := range req.Items {
		batchItems = append(batchItems, services.TgActivityFineBatchItem{
			SegmentMinutes: item.SegmentMinutes,
			FineAmount:     item.FineAmount,
		})
	}
	rows, err := h.telegram.BatchUpsertActivityFines(c.Request.Context(), codes, batchItems)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "activityCode": row.ActivityCode,
			"segmentMinutes": row.SegmentMinutes, "fineAmount": row.FineAmount,
		})
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("批量新增活动超时罚款规则：%d 个活动 × %d 档（共 %d 条）", len(codes), len(req.Items), len(out)),
		"", "")
	models.Send(c, 200, 0, "success", gin.H{"items": out, "count": len(out)})
}
func (h *WorkforceTelegramHandler) DeleteActivityFine(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid id")
		return
	}
	if err := h.telegram.DeleteActivityFine(c.Request.Context(), id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "DELETE", "TELEGRAM_ATTENDANCE", "删除活动超时罚款规则", id.String(), id.String())
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) ListActivitySessions(c *gin.Context) {
	limit := 200
	if strings.TrimSpace(c.Query("yearMonth")) != "" && strings.TrimSpace(c.Query("date")) == "" {
		limit = 500
	}
	items, err := h.telegram.ListActivitySessions(c.Request.Context(),
		c.Query("date"), c.Query("yearMonth"), c.Query("employeeId"), c.Query("search"), limit)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for i := range items {
		out = append(out, sessionToJSON(&items[i]))
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) ListWorkSessions(c *gin.Context) {
	limit := 200
	if strings.TrimSpace(c.Query("yearMonth")) != "" && strings.TrimSpace(c.Query("date")) == "" {
		limit = 500
	}
	items, err := h.telegram.ListWorkSessions(c.Request.Context(),
		c.Query("date"), c.Query("yearMonth"), c.Query("employeeId"), c.Query("search"), limit)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for i := range items {
		row := &items[i]
		item := gin.H{
			"id": row.ID, "employeeId": row.EmployeeID, "employeeName": row.EmployeeName,
			"employeeCode": row.EmployeeCode, "attendanceDate": row.AttendanceDate,
			"shiftType": row.ShiftType, "checkinTime": models.FormatUTC(row.CheckinTime),
			"source": row.Source, "status": row.Status,
		}
		if row.CheckoutTime != nil {
			item["checkoutTime"] = models.FormatUTC(*row.CheckoutTime)
		}
		out = append(out, item)
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) WorkerStartActivity(c *gin.Context) {
	var req struct {
		TelegramUserID   int64  `json:"telegramUserId" binding:"required"`
		ChatID           string `json:"chatId"`
		Activity         string `json:"activity" binding:"required"`
		ReplyToMessageID int64  `json:"replyToMessageId"`
		DeviceID         string `json:"deviceId"`
		DeviceHint       string `json:"deviceHint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	deviceID, err := services.ParseOptionalDeviceID(req.DeviceID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	result, err := h.telegram.TelegramStartActivityWithDevice(
		services.WithWorkerTelegramReply(c.Request.Context()), req.TelegramUserID, req.ChatID, req.Activity, req.ReplyToMessageID, deviceID, req.DeviceHint)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	resp := sessionToJSON(result.Session)
	resp["messageText"] = result.MessageText
	resp["groupReplyHtml"] = result.GroupReplyHTML
	resp["groupReplyChatId"] = result.Session.GroupReplyChatID
	resp["inlineKeyboard"] = result.InlineKeyboard
	resp["groupReplySent"] = result.GroupReplySent
	models.Send(c, 200, 0, "activity started", resp)
}
func (h *WorkforceTelegramHandler) WorkerEndActivity(c *gin.Context) {
	var req struct {
		TelegramUserID int64  `json:"telegramUserId" binding:"required"`
		DeviceID       string `json:"deviceId"`
		DeviceHint     string `json:"deviceHint"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	deviceID, err := services.ParseOptionalDeviceID(req.DeviceID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	sess, err := h.telegram.TelegramEndActivityWithDevice(services.WithWorkerTelegramReply(c.Request.Context()), req.TelegramUserID, deviceID, req.DeviceHint)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	resp := sessionToJSON(sess)
	if sess != nil && strings.TrimSpace(sess.GroupReplyHTML) != "" {
		resp["groupReplySent"] = false
		if anchorID := sess.TelegramMessageID; anchorID > 0 {
			resp["activityAnchorMessageId"] = anchorID
		}
	}
	models.Send(c, 200, 0, "activity ended", resp)
}
func (h *WorkforceTelegramHandler) WorkerMyInfo(c *gin.Context) {
	tgID, err := services.ParseTelegramUserID(c.Query("telegramUserId"))
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	info, err := h.telegram.GetMyInfoForShift(c.Request.Context(), tgID, c.Query("shift"), c.Query("chatId"))
	if err != nil {
		if errors.Is(err, services.ErrTelegramNotBound) {
			models.Send(c, 200, 0, "success", gin.H{"bound": false})
			return
		}
		h.sendTelegramWorkerError(c, err, tgID)
		return
	}
	displayText, _ := h.telegram.BuildRichMyInfo(c.Request.Context(), tgID, c.Query("shift"), c.Query("chatId"))
	resp := gin.H{
		"bound": true, "employeeName": info.EmployeeName,
		"workSessionOpen": info.WorkSessionOpen, "currentActivity": info.CurrentActivity,
		"todayActivities": info.TodayActivities, "todayFineTotal": info.TodayFineTotal,
		"shiftType": info.ShiftFilter, "shiftLabel": info.ShiftLabel,
		"displayText": displayText,
	}
	if info.ActivityStartedAt != nil {
		resp["activityStartedAt"] = models.FormatUTC(*info.ActivityStartedAt)
	}
	models.Send(c, 200, 0, "success", resp)
}
func (h *WorkforceTelegramHandler) ListHandovers(c *gin.Context) {
	items, err := h.telegram.ListHandovers(c.Request.Context(), c.Query("date"), c.Query("search"), 200)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, row := range items {
		out = append(out, gin.H{
			"id": row.ID, "employeeId": row.EmployeeID, "employeeName": row.EmployeeName,
			"chatId": row.ChatID, "fromShift": row.FromShift, "toShift": row.ToShift,
			"fromShiftLabel": services.ShiftLabelPublic(row.FromShift),
			"toShiftLabel":   services.ShiftLabelPublic(row.ToShift),
			"note":           row.Note, "handoverAt": models.FormatUTC(row.HandoverAt),
		})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) ListRankings(c *gin.Context) {
	items, err := h.telegram.ListRankingsForShift(c.Request.Context(), c.Query("date"), c.Query("metric"), c.Query("shift"), c.Query("chatId"), 30)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, row := range items {
		out = append(out, gin.H{
			"rank": row.Rank, "employeeId": row.EmployeeID, "employeeName": row.EmployeeName,
			"employeeCode": row.EmployeeCode, "activityCount": row.ActivityCount,
			"overtimeSeconds": row.OvertimeSeconds, "fineTotal": row.FineTotal,
		})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out, "date": c.Query("date"), "metric": c.Query("metric")})
}
func (h *WorkforceTelegramHandler) ExportAttendance(c *gin.Context) {
	dateFrom := strings.TrimSpace(c.Query("dateFrom"))
	dateTo := strings.TrimSpace(c.Query("dateTo"))
	month := strings.TrimSpace(c.Query("month"))
	if month != "" && dateFrom == "" {
		dateFrom = month + "-01"
		if t, err := time.Parse("2006-01-02", dateFrom); err == nil {
			dateTo = t.AddDate(0, 1, -1).Format("2006-01-02")
		}
	}
	data, filename, err := h.telegram.ExportAttendanceXLSX(c.Request.Context(), dateFrom, dateTo, "")
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "EXPORT", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("导出 Telegram 考勤表（%s 至 %s）", dateFrom, dateTo), month, filename)
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", data)
}
func (h *WorkforceTelegramHandler) RunDailyReset(c *gin.Context) {
	var req struct {
		ExportBusinessToday bool `json:"exportBusinessToday"`
	}
	_ = c.ShouldBindJSON(&req)

	runOpts := &services.DailyResetRunOptions{ManualTrigger: true}
	if req.ExportBusinessToday {
		runOpts.ExportBusinessToday = true
	}

	result, err := h.telegram.RunDailyReset(c.Request.Context(), runOpts)
	if err != nil {
		if errors.Is(err, services.ErrTelegramDisabled) {
			models.SendError(c, 403, 1003, "Telegram 考勤未启用")
			return
		}
		models.SendInternalError(c, err)
		return
	}
	if result == nil {
		models.Send(c, 200, 0, "already reset today or disabled", gin.H{"skipped": true})
		return
	}
	if result.SkippedAllGroups {
		models.Send(c, 200, 0, "all groups already reset today", gin.H{
			"skipped":          true,
			"skippedAllGroups": true,
			"resetDate":        result.ResetDate,
		})
		return
	}
	Log(c, "POST", "TELEGRAM_ATTENDANCE", "手动日重置", result.ResetDate, "Telegram")
	models.Send(c, 200, 0, "daily reset completed", gin.H{
		"resetDate":           result.ResetDate,
		"exportDate":          result.ExportDate,
		"exportDateNote":      result.ExportDateNote,
		"closedWorkSessions":  result.ClosedWorkSessions,
		"closedAgentSessions": result.ClosedAgentSessions,
		"closedActivities":    result.ClosedActivities,
		"exportPath":          result.ExportPath,
		"archivedMonth":       result.ArchivedMonth,
		"bindingWarnings":     result.BindingWarnings,
		"groupsProcessed":     result.GroupsProcessed,
	})
}
func (h *WorkforceTelegramHandler) WorkerHandover(c *gin.Context) {
	var req struct {
		TelegramUserID int64  `json:"telegramUserId" binding:"required"`
		Note           string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	result, handover, err := h.telegram.TelegramHandover(c.Request.Context(), req.TelegramUserID, req.Note)
	if err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	models.Send(c, 200, 0, "handover completed", gin.H{
		"sessionId": result.SessionID, "shiftType": result.ShiftType,
		"shiftLabel": services.ShiftLabelPublic(result.ShiftType),
		"fromShift":  handover.FromShift, "toShift": handover.ToShift,
	})
}
func (h *WorkforceTelegramHandler) WorkerRankings(c *gin.Context) {
	chatID := c.Query("chatId")
	shift := c.Query("shift")
	groups, header, err := h.telegram.ListActivityRankGroups(c.Request.Context(), chatID, shift)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	displayText := h.telegram.FormatActivityRankingsText(groups, header, shift)
	models.Send(c, 200, 0, "success", gin.H{"displayText": displayText, "groups": groups})
}
func (h *WorkforceTelegramHandler) WorkerCallbackQuickBack(c *gin.Context) {
	var req struct {
		SessionID      string `json:"sessionId" binding:"required"`
		TelegramUserID int64  `json:"telegramUserId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	sid, err := uuid.Parse(req.SessionID)
	if err != nil {
		models.SendError(c, 400, 1001, "invalid sessionId")
		return
	}
	sess, err := h.telegram.TelegramEndActivityBySession(services.WithWorkerTelegramReply(c.Request.Context()), sid, req.TelegramUserID)
	if err != nil {
		if errors.Is(err, services.ErrActivityBackNotOwner) {
			name := h.telegram.EmployeeNameByTelegramUserID(c.Request.Context(), req.TelegramUserID)
			mode := h.telegram.GetReplyLangForUser(c.Request.Context(), req.TelegramUserID)
			models.SendError(c, 403, 1006, services.FormatTelegramActivityBackNotOwner(name, mode))
			return
		}
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	item := sessionToJSON(sess)
	msg := fmt.Sprintf("✅ 「%s」已结束，用时 %s", sess.ActivityName, services.FormatDurationZh(sess.ElapsedSeconds))
	if sess.FineAmount > 0 {
		msg += fmt.Sprintf("\n⚠️ 超时罚款 ¥%.2f", sess.FineAmount)
	}
	item["messageText"] = msg
	if strings.TrimSpace(sess.GroupReplyHTML) != "" {
		item["groupReplySent"] = false
		if anchorID := sess.TelegramMessageID; anchorID > 0 {
			item["activityAnchorMessageId"] = anchorID
		}
	}
	models.Send(c, 200, 0, "activity ended", item)
}
func (h *WorkforceTelegramHandler) WorkerAttendanceReviewCallback(c *gin.Context) {
	var req struct {
		TaskID         string `json:"taskId" binding:"required"`
		Decision       string `json:"decision" binding:"required"`
		TelegramUserID int64  `json:"telegramUserId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	taskID, err := uuid.Parse(strings.TrimSpace(req.TaskID))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid taskId")
		return
	}
	decision := strings.TrimSpace(strings.ToLower(req.Decision))
	switch decision {
	case "absent", "rest_half", "leave":
	default:
		models.SendError(c, 400, 1001, "invalid decision")
		return
	}
	msg, err := h.telegram.ConfirmAttendanceReviewTaskByTelegram(c.Request.Context(), taskID, decision, req.TelegramUserID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "admin only") {
			models.SendError(c, 403, 1003, "admin only")
			return
		}
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"messageText": msg})
}
func (h *WorkforceTelegramHandler) WorkerActivityMessageID(c *gin.Context) {
	var req struct {
		Ended     bool   `json:"ended"`
		SessionID string `json:"sessionId" binding:"required"`
		MessageID int64  `json:"messageId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	sid, err := uuid.Parse(req.SessionID)
	if err != nil {
		models.SendError(c, 400, 1001, "invalid sessionId")
		return
	}
	if err := h.telegram.RecordWorkerActivityReply(c.Request.Context(), sid, req.MessageID, req.Ended); err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) GetAttendanceReviewSettings(c *gin.Context) {
	cfg := h.telegram.GetAttendanceReviewSettings(c.Request.Context())
	models.Send(c, 200, 0, "success", gin.H{
		"workHours":  cfg.WorkHours,
		"earlyHours": cfg.EarlyHours,
	})
}
func (h *WorkforceTelegramHandler) SaveAttendanceReviewSettings(c *gin.Context) {
	var req struct {
		WorkHours  int `json:"workHours"`
		EarlyHours int `json:"earlyHours"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	cfg, err := h.telegram.SaveAttendanceReviewSettings(c.Request.Context(), services.AttendanceReviewSettings{
		WorkHours:  req.WorkHours,
		EarlyHours: req.EarlyHours,
	})
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "更新考勤待确认阈值", "", "")
	models.Send(c, 200, 0, "success", gin.H{"workHours": cfg.WorkHours, "earlyHours": cfg.EarlyHours})
}
func (h *WorkforceTelegramHandler) ListAttendanceReviewTasks(c *gin.Context) {
	month := strings.TrimSpace(c.Query("month"))
	search := strings.TrimSpace(c.Query("search"))
	items, err := h.telegram.ListAttendanceReviewTasks(c.Request.Context(), month, search)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"items": items, "count": len(items)})
}
func (h *WorkforceTelegramHandler) ConfirmAttendanceReviewTask(c *gin.Context) {
	taskID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid id")
		return
	}
	var req struct {
		Decision string `json:"decision" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	adminName := strings.TrimSpace(c.GetString("username"))
	msg, err := h.telegram.ConfirmAttendanceReviewTaskByAdmin(c.Request.Context(), taskID, req.Decision, adminName)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "确认考勤待处理任务", taskID.String(), taskID.String())
	models.Send(c, 200, 0, "success", gin.H{"messageText": msg})
}
func (h *WorkforceTelegramHandler) GetAdminPunchMe(c *gin.Context) {
	// Only admin may 代打 others; everyone else is locked to linked self.
	forceSelf := !middleware.IsAdminRole(c) && !middleware.UserHasPermission(c, "data:all")
	empID, ok := middleware.ResolveScopedEmployeeID(c)
	if !ok {
		models.Send(c, 200, 0, "success", gin.H{
			"linked":    false,
			"forceSelf": forceSelf,
		})
		return
	}
	var name, empCode, position string
	_ = h.db.QueryRowContext(c.Request.Context(), `
		SELECT COALESCE(name,''), COALESCE(employee_id,''), COALESCE(position,'')
		FROM employees WHERE id = $1`, empID).Scan(&name, &empCode, &position)
	models.Send(c, 200, 0, "success", gin.H{
		"linked":       true,
		"forceSelf":    forceSelf,
		"employeeId":   empID.String(),
		"employeeName": name,
		"employeeCode": empCode,
		"position":     position,
	})
}
func (h *WorkforceTelegramHandler) resolveAdminPunchEmployeeID(c *gin.Context, requested string) (uuid.UUID, error) {
	if !middleware.IsAdminRole(c) && !middleware.UserHasPermission(c, "data:all") {
		empID, ok := middleware.ResolveScopedEmployeeID(c)
		if !ok {
			return uuid.Nil, fmt.Errorf("当前账号未关联员工，无法打卡")
		}
		if strings.TrimSpace(requested) != "" && strings.TrimSpace(requested) != empID.String() {
			return uuid.Nil, fmt.Errorf("子账号只能为本人打卡")
		}
		return empID, nil
	}
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if empID, ok := middleware.ResolveScopedEmployeeID(c); ok {
			return empID, nil
		}
		return uuid.Nil, fmt.Errorf("请选择员工")
	}
	empID, err := uuid.Parse(requested)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid employeeId")
	}
	return empID, nil
}
func (h *WorkforceTelegramHandler) AdminPunchCheckIn(c *gin.Context) {
	var req struct {
		EmployeeID string `json:"employeeId"`
		Shift      string `json:"shift"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	empID, err := h.resolveAdminPunchEmployeeID(c, req.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	res, err := h.telegram.AdminPunchCheckIn(c.Request.Context(), empID, req.Shift)
	if err != nil {
		h.sendTelegramWorkerError(c, err, 0)
		return
	}
	out := gin.H{
		"employeeId":   empID,
		"employeeName": res.EmployeeName,
		"workFine":     res.WorkFine,
		"handoverHint": res.HandoverHint,
		"messageText":  res.MessageText,
	}
	if res.Result != nil {
		out["sessionId"] = res.Result.SessionID
		out["shiftType"] = res.Result.ShiftType
		out["date"] = res.Result.Date
		out["shiftLabel"] = services.ShiftLabelPublic(res.Result.ShiftType)
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE", "后台上班打卡", empID.String(), empID.String())
	models.Send(c, 200, 0, "success", out)
}
func (h *WorkforceTelegramHandler) AdminPunchCheckOut(c *gin.Context) {
	var req struct {
		EmployeeID string `json:"employeeId"`
		Shift      string `json:"shift"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	empID, err := h.resolveAdminPunchEmployeeID(c, req.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	res, err := h.telegram.AdminPunchCheckOut(c.Request.Context(), empID, req.Shift)
	if err != nil {
		h.sendTelegramWorkerError(c, err, 0)
		return
	}
	out := gin.H{
		"employeeId":   empID,
		"employeeName": res.EmployeeName,
		"workFine":     res.WorkFine,
		"handoverHint": res.HandoverHint,
		"activityWarn": res.ActivityWarn,
		"messageText":  res.MessageText,
	}
	if res.CheckOut != nil {
		out["sessionId"] = res.CheckOut.SessionID
		out["shiftType"] = res.CheckOut.ShiftType
		out["shiftLabel"] = services.ShiftLabelPublic(res.CheckOut.ShiftType)
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "后台下班打卡", empID.String(), empID.String())
	models.Send(c, 200, 0, "success", out)
}
func (h *WorkforceTelegramHandler) AdminPunchActivityStart(c *gin.Context) {
	var req struct {
		EmployeeID string `json:"employeeId"`
		Activity   string `json:"activity" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	empID, err := h.resolveAdminPunchEmployeeID(c, req.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	res, err := h.telegram.AdminPunchStartActivity(c.Request.Context(), empID, req.Activity)
	if err != nil {
		h.sendTelegramWorkerError(c, err, 0)
		return
	}
	item := sessionToJSON(res.Session)
	item["messageText"] = res.MessageText
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE", "后台活动打卡", empID.String(), empID.String())
	models.Send(c, 200, 0, "success", item)
}
func (h *WorkforceTelegramHandler) AdminPunchActivityBack(c *gin.Context) {
	var req struct {
		EmployeeID string `json:"employeeId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	empID, err := h.resolveAdminPunchEmployeeID(c, req.EmployeeID)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	sess, err := h.telegram.AdminPunchBackActivity(c.Request.Context(), empID)
	if err != nil {
		h.sendTelegramWorkerError(c, err, 0)
		return
	}
	item := sessionToJSON(sess)
	msg := fmt.Sprintf("✅ 「%s」已结束，用时 %s", sess.ActivityName, services.FormatDurationZh(sess.ElapsedSeconds))
	if sess.FineAmount > 0 {
		msg += fmt.Sprintf("\n⚠️ 超时罚款 ¥%.2f", sess.FineAmount)
	}
	item["messageText"] = msg
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "后台活动回座", empID.String(), empID.String())
	models.Send(c, 200, 0, "success", item)
}
func (h *WorkforceTelegramHandler) WorkerRegisterGroup(c *gin.Context) {
	var req struct {
		ChatID    string `json:"chatId" binding:"required"`
		ChatTitle string `json:"chatTitle"`
		ChatType  string `json:"chatType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	chatType := strings.TrimSpace(req.ChatType)
	if chatType == "" {
		chatType = "supergroup"
	}
	firstSeen, err := h.telegram.RecordDiscoveredChat(c.Request.Context(), req.ChatID, req.ChatTitle, chatType)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{
		"chatId": req.ChatID, "chatTitle": req.ChatTitle, "chatType": chatType, "firstSeen": firstSeen,
	})
}
func (h *WorkforceTelegramHandler) GetPushSettings(c *gin.Context) {
	chatID := c.Query("chatId")
	if strings.TrimSpace(chatID) == "" {
		models.SendError(c, 400, 1001, "chatId required")
		return
	}
	ps, err := h.telegram.GetPushSettings(c.Request.Context(), chatID)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{
		"chatId": ps.ChatID, "channelId": ps.ChannelID,
		"notificationGroupId": ps.NotificationGroupID, "extraWorkGroupId": ps.ExtraWorkGroupID,
		"enableChannelPush": ps.EnableChannelPush, "enableGroupPush": ps.EnableGroupPush,
		"enableAdminPush": ps.EnableAdminPush, "updatedAt": models.FormatUTC(ps.UpdatedAt),
	})
}
func (h *WorkforceTelegramHandler) UpsertPushSettings(c *gin.Context) {
	var req struct {
		ChatID string                 `json:"chatId" binding:"required"`
		Data   map[string]interface{} `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	in := req.Data
	if in == nil {
		in = map[string]interface{}{}
	}
	ps, err := h.telegram.UpsertPushSettings(c.Request.Context(), req.ChatID, in)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "更新 Telegram 推送配置", ps.ChatID, ps.ChatID)
	models.Send(c, 200, 0, "success", gin.H{"chatId": ps.ChatID})
}
func (h *WorkforceTelegramHandler) TestPushSettings(c *gin.Context) {
	chatID := strings.TrimSpace(c.Query("chatId"))
	if chatID == "" {
		var req struct {
			ChatID string `json:"chatId"`
		}
		if c.ShouldBindJSON(&req) == nil {
			chatID = strings.TrimSpace(req.ChatID)
		}
	}
	if chatID == "" {
		models.SendError(c, 400, 1001, "chatId required")
		return
	}
	if err := h.telegram.TestPushSettings(c.Request.Context(), chatID); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "测试 Telegram 推送配置", chatID, chatID)
	models.Send(c, 200, 0, "测试消息已发送", nil)
}
func (h *WorkforceTelegramHandler) GetHandoverConfig(c *gin.Context) {
	chatID := c.Query("chatId")
	if strings.TrimSpace(chatID) == "" {
		models.SendError(c, 400, 1001, "chatId required")
		return
	}
	cfg, err := h.telegram.GetHandoverConfig(c.Request.Context(), chatID)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{
		"chatId": cfg.ChatID, "handoverEnabled": cfg.HandoverEnabled,
		"handoverDay": cfg.HandoverDay, "handoverMonth": cfg.HandoverMonth,
		"handoverDayStartTime": cfg.HandoverDayStartTime,
		"nightStartTime":       cfg.NightStartTime, "dayStartTime": cfg.DayStartTime,
		"handoverNightHours": cfg.HandoverNightHours, "handoverDayHours": cfg.HandoverDayHours,
		"normalNightHours": cfg.NormalNightHours, "normalDayHours": cfg.NormalDayHours,
		"handoverResetThresholdHours": cfg.HandoverResetThresholdHours,
		"updatedAt":                   models.FormatUTC(cfg.UpdatedAt),
	})
}
func (h *WorkforceTelegramHandler) UpsertHandoverConfig(c *gin.Context) {
	var req struct {
		ChatID string                 `json:"chatId" binding:"required"`
		Data   map[string]interface{} `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	in := req.Data
	if in == nil {
		in = map[string]interface{}{}
	}
	cfg, err := h.telegram.UpsertHandoverConfig(c.Request.Context(), req.ChatID, in)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "TELEGRAM_ATTENDANCE", "更新换班日配置", cfg.ChatID, cfg.ChatID)
	models.Send(c, 200, 0, "success", gin.H{"chatId": cfg.ChatID, "handoverEnabled": cfg.HandoverEnabled})
}
func (h *WorkforceTelegramHandler) ListWorkFines(c *gin.Context) {
	items, err := h.telegram.ListWorkFines(c.Request.Context())
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, f := range items {
		out = append(out, gin.H{
			"id": f.ID, "checkinType": f.CheckinType,
			"segmentMinutes": f.SegmentMinutes, "fineAmount": f.FineAmount,
		})
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) UpsertWorkFine(c *gin.Context) {
	var req struct {
		CheckinType    string  `json:"checkinType" binding:"required"`
		SegmentMinutes int     `json:"segmentMinutes" binding:"required"`
		FineAmount     float64 `json:"fineAmount"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	row, err := h.telegram.UpsertWorkFine(c.Request.Context(), req.CheckinType, req.SegmentMinutes, req.FineAmount)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("新增上下班罚款规则：%s / %d 分钟", req.CheckinType, req.SegmentMinutes),
		row.ID.String(), req.CheckinType)
	models.Send(c, 200, 0, "success", gin.H{
		"id": row.ID, "checkinType": row.CheckinType,
		"segmentMinutes": row.SegmentMinutes, "fineAmount": row.FineAmount,
	})
}
func (h *WorkforceTelegramHandler) BatchUpsertWorkFines(c *gin.Context) {
	var req struct {
		CheckinType  string   `json:"checkinType"`
		CheckinTypes []string `json:"checkinTypes"`
		Items        []struct {
			SegmentMinutes int     `json:"segmentMinutes" binding:"required"`
			FineAmount     float64 `json:"fineAmount"`
		} `json:"items" binding:"required,min=1,dive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	types := append([]string{}, req.CheckinTypes...)
	if strings.TrimSpace(req.CheckinType) != "" {
		types = append(types, req.CheckinType)
	}
	batchItems := make([]services.TgWorkFineBatchItem, 0, len(req.Items))
	for _, item := range req.Items {
		batchItems = append(batchItems, services.TgWorkFineBatchItem{
			SegmentMinutes: item.SegmentMinutes,
			FineAmount:     item.FineAmount,
		})
	}
	rows, err := h.telegram.BatchUpsertWorkFines(c.Request.Context(), types, batchItems)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		out = append(out, gin.H{
			"id": row.ID, "checkinType": row.CheckinType,
			"segmentMinutes": row.SegmentMinutes, "fineAmount": row.FineAmount,
		})
	}
	Log(c, "CREATE", "TELEGRAM_ATTENDANCE",
		fmt.Sprintf("批量新增上下班罚款规则：%d 种类型 × %d 档（共 %d 条）", len(types), len(req.Items), len(out)),
		"", "")
	models.Send(c, 200, 0, "success", gin.H{"items": out, "count": len(out)})
}
func (h *WorkforceTelegramHandler) DeleteWorkFine(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		models.SendError(c, 400, 1001, "invalid id")
		return
	}
	if err := h.telegram.DeleteWorkFine(c.Request.Context(), id); err != nil {
		models.SendInternalError(c, err)
		return
	}
	Log(c, "DELETE", "TELEGRAM_ATTENDANCE", "删除上下班罚款规则", id.String(), id.String())
	models.Send(c, 200, 0, "success", nil)
}
func (h *WorkforceTelegramHandler) ListDeviceTelegramBindings(c *gin.Context) {
	items, err := h.telegram.ListDeviceTelegramBindings(c.Request.Context(), c.Query("search"))
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		row := gin.H{
			"id":               item.ID,
			"deviceId":         item.DeviceID,
			"deviceName":       item.DeviceName,
			"channel":          services.TelegramChannelWorkstation,
			"telegramUserId":   item.TelegramUserID,
			"telegramUsername": item.TelegramUsername,
			"chatId":           item.ChatID,
			"boundAt":          models.FormatUTC(item.BoundAt),
		}
		if item.EmployeeID != uuid.Nil {
			row["employeeId"] = item.EmployeeID
			row["employeeName"] = item.EmployeeName
			row["employeeCode"] = item.EmployeeCode
		}
		out = append(out, row)
	}
	models.Send(c, 200, 0, "success", gin.H{"items": out})
}
func (h *WorkforceTelegramHandler) WorkerExport(c *gin.Context) {
	chatID := strings.TrimSpace(c.Query("chatId"))
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	if month := strings.TrimSpace(c.Query("month")); month != "" && dateFrom == "" {
		if t, err := time.Parse("2006-01", month); err == nil {
			dateFrom = t.Format("2006-01-02")
			dateTo = t.AddDate(0, 1, -1).Format("2006-01-02")
		}
	}
	caption, err := h.telegram.BotExportAndPush(c.Request.Context(), chatID, dateFrom, dateTo)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"caption": caption})
}
func (h *WorkforceTelegramHandler) WorkerMonthlyReport(c *gin.Context) {
	chatID := strings.TrimSpace(c.Query("chatId"))
	month := c.Query("month")
	text, err := h.telegram.FormatMonthlyReport(c.Request.Context(), chatID, month)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"displayText": text, "parseMode": "HTML"})
}
func (h *WorkforceTelegramHandler) WorkerActStatus(c *gin.Context) {
	chatID := strings.TrimSpace(c.Query("chatId"))
	models.Send(c, 200, 0, "success", gin.H{"displayText": h.telegram.FormatActivityOccupancy(c.Request.Context(), chatID)})
}
func (h *WorkforceTelegramHandler) WorkerShowSettings(c *gin.Context) {
	tgID, err := services.ParseTelegramUserID(c.Query("telegramUserId"))
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	if !h.telegram.IsAdminTelegramUser(c.Request.Context(), tgID) {
		models.SendError(c, 403, 1003, "admin only")
		return
	}
	chatID := strings.TrimSpace(c.Query("chatId"))
	models.Send(c, 200, 0, "success", gin.H{
		"displayText": h.telegram.FormatShowSettings(c.Request.Context(), chatID),
		"parseMode":   "HTML",
	})
}
func (h *WorkforceTelegramHandler) WorkerCheckDB(c *gin.Context) {
	tgID, err := services.ParseTelegramUserID(c.Query("telegramUserId"))
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	if !h.telegram.IsAdminTelegramUser(c.Request.Context(), tgID) {
		models.SendError(c, 403, 1003, "admin only")
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"displayText": h.telegram.FormatCheckDB(c.Request.Context())})
}
func (h *WorkforceTelegramHandler) WorkerFixMessages(c *gin.Context) {
	var req struct {
		TelegramUserID int64 `json:"telegramUserId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	if !h.telegram.IsAdminTelegramUser(c.Request.Context(), req.TelegramUserID) {
		models.SendError(c, 403, 1003, "admin only")
		return
	}
	n := h.telegram.FixActivityMessageIDs(c.Request.Context())
	models.Send(c, 200, 0, "success", gin.H{"cleared": n})
}
func (h *WorkforceTelegramHandler) WorkerResolveCI(c *gin.Context) {
	text := strings.TrimSpace(c.Query("text"))
	act := h.telegram.ResolveActivityFromCI(c.Request.Context(), text)
	if act == "" {
		models.SendError(c, 404, 1004, "activity not found")
		return
	}
	models.Send(c, 200, 0, "success", gin.H{"activityName": act})
}
func (h *WorkforceTelegramHandler) WorkerSetLang(c *gin.Context) {
	var req struct {
		TelegramUserID int64  `json:"telegramUserId"`
		Lang           string `json:"lang"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	if err := h.telegram.SetUserPreferredLang(c.Request.Context(), req.TelegramUserID, req.Lang); err != nil {
		h.sendTelegramWorkerError(c, err, req.TelegramUserID)
		return
	}
	empID, _ := h.telegram.ResolveEmployeeByTelegramUserID(c.Request.Context(), req.TelegramUserID)
	models.Send(c, 200, 0, "success", gin.H{
		"employeeId":    empID.String(),
		"preferredLang": string(h.telegram.GetReplyLangForEmployee(c.Request.Context(), empID)),
	})
}

// Reject destructive score-list replacement through ordinary edit endpoints.
func (h *AdminHandler) allowPerformanceReplacement(c *gin.Context, employeeID, month string, value interface{}) bool {
	list, ok := value.([]interface{})
	if !ok {
		models.SendError(c, 400, 400, "绩效记录必须是数组")
		return false
	}
	if middleware.UserHasPermission(c, "performance:delete") {
		return true
	}
	var count int
	err := h.db.QueryRowContext(c.Request.Context(), `SELECT jsonb_array_length(COALESCE(score_records,'[]'::jsonb)) FROM performance_records WHERE employee_id=$1 AND month=$2`, employeeID, month).Scan(&count)
	if err != nil && err != sql.ErrNoRows {
		models.SendInternalError(c, err)
		return false
	}
	if len(list) < count {
		models.SendError(c, 403, 403, "删除绩效明细需要独立删除权限")
		return false
	}
	return true
}

func (h *WorkforceTelegramHandler) PunchActivityUsage(c *gin.Context) {
	emp, err := h.resolveAdminPunchEmployeeID(c, c.Query("employeeId"))
	if err != nil {
		models.SendError(c, 403, 403, err.Error())
		return
	}
	result, err := h.telegram.PunchActivityUsage(c.Request.Context(), emp)
	if err != nil {
		models.SendInternalError(c, err)
		return
	}
	if open, ok := result["openSession"].(*services.TgActivitySession); ok {
		result["openSession"] = sessionToJSON(open)
	}
	models.Send(c, 200, 0, "success", result)
}
