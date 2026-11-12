// Consolidated source; original file boundaries are marked below.
package handlers

import (
	context "context"
	subtle "crypto/subtle"
	sql "database/sql"
	csvPackage2 "encoding/csv"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	gin "github.com/gin-gonic/gin"
	uuid "github.com/google/uuid"
	redisPackage2 "github.com/redis/go-redis/v9"
	excelize "github.com/xuri/excelize/v2"
	rate "golang.org/x/time/rate"
	io "io"
	logPackage2 "log"
	multipart "mime/multipart"
	os "os"
	filepath "path/filepath"
	regexp "regexp"
	strconv "strconv"
	strings "strings"
	sync "sync"
	middleware "tg-attendance/backend/middleware"
	models "tg-attendance/backend/models"
	services "tg-attendance/backend/services"
	logx "tg-attendance/backend/services/logx"
	time "time"
)

// ---- Source: backend/handlers/attendance_import.go ----
const maxAttendanceImportRows = 500

var attendanceStatusImportMap = map[string]string{
	"出勤":        "work",
	"半休":        "rest_half",
	"全休":        "rest_full",
	"半假":        "leave",
	"旷工":        "absent",
	"全假":        "off_post",
	"离职":        "resigned",
	"work":      "work",
	"rest_half": "rest_half",
	"rest_full": "rest_full",
	"leave":     "leave",
	"absent":    "absent",
	"off_post":  "off_post",
	"resigned":  "resigned",
	"pending":   "pending",
}

type attendanceImportColumnMap struct {
	HireDate     int
	Name         int
	Position     int
	WorkLocation int
	EmployeeID   int
	Username     int
	Password     int
	DayColumns   map[int]int
}
type attendanceImportRow struct {
	HireDate     string
	Name         string
	Position     string
	WorkLocation string
	EmployeeID   string
	Username     string
	Password     string
	DayStatus    map[int]string
}

func normalizeImportHeader(cell string) string {
	cell = strings.TrimSpace(cell)
	cell = strings.Trim(cell, "\ufeff")
	cell = strings.ReplaceAll(cell, " ", "")
	return strings.ToLower(cell)
}
func findImportColumn(header []string, keys ...string) int {
	for i, raw := range header {
		norm := normalizeImportHeader(raw)
		for _, key := range keys {
			if norm == normalizeImportHeader(key) {
				return i
			}
		}
	}
	return -1
}
func parseAttendanceImportHeader(header []string) (attendanceImportColumnMap, error) {
	if len(header) == 0 {
		return attendanceImportColumnMap{}, fmt.Errorf("表头为空")
	}

	m := attendanceImportColumnMap{
		HireDate:     findImportColumn(header, "入职日期", "hire_date"),
		Name:         findImportColumn(header, "姓名", "name", "员工姓名"),
		Position:     findImportColumn(header, "岗位", "position"),
		WorkLocation: findImportColumn(header, "办公地点", "公地点", "work_location", "工作地点"),
		EmployeeID:   findImportColumn(header, "工号", "employee_id", "员工工号"),
		Username:     findImportColumn(header, "登录账号", "username", "子账号"),
		Password:     findImportColumn(header, "初始密码", "password", "密码"),
		DayColumns:   make(map[int]int),
	}

	if m.Name < 0 {
		return m, fmt.Errorf("缺少「姓名」列")
	}

	for i, raw := range header {
		dayText := strings.TrimSpace(raw)
		if dayText == "" {
			continue
		}
		if day, err := strconv.Atoi(dayText); err == nil && day >= 1 && day <= 31 {
			m.DayColumns[day] = i
		}
	}

	return m, nil
}
func parseAttendanceStatus(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if status, ok := attendanceStatusImportMap[raw]; ok {
		return status
	}
	if status, ok := attendanceStatusImportMap[strings.ToLower(raw)]; ok {
		return status
	}
	return ""
}
func parseAttendanceImportRows(rows [][]string, cols attendanceImportColumnMap) ([]attendanceImportRow, error) {
	if len(rows) < 2 {
		return nil, fmt.Errorf("文件中没有数据行")
	}

	getCell := func(row []string, idx int) string {
		if idx < 0 || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	items := make([]attendanceImportRow, 0)
	for rowIdx, row := range rows[1:] {
		name := getCell(row, cols.Name)
		if name == "" {
			continue
		}

		item := attendanceImportRow{
			HireDate:     getCell(row, cols.HireDate),
			Name:         name,
			Position:     getCell(row, cols.Position),
			WorkLocation: getCell(row, cols.WorkLocation),
			EmployeeID:   getCell(row, cols.EmployeeID),
			Username:     getCell(row, cols.Username),
			Password:     getCell(row, cols.Password),
			DayStatus:    make(map[int]string),
		}

		if item.WorkLocation == "" {
			item.WorkLocation = "现场"
		}

		for day, colIdx := range cols.DayColumns {
			status := parseAttendanceStatus(getCell(row, colIdx))
			if status != "" {
				item.DayStatus[day] = status
			}
		}

		items = append(items, item)
		if len(items) > maxAttendanceImportRows {
			return nil, fmt.Errorf("单次最多导入 %d 名员工", maxAttendanceImportRows)
		}
		_ = rowIdx
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("未读取到有效员工数据")
	}
	return items, nil
}
func (h *WorkforceHandler) lookupEmployeeID(ctx *gin.Context, employeeID, name string) (uuid.UUID, bool, error) {
	if strings.TrimSpace(employeeID) != "" {
		var id uuid.UUID
		err := h.db.QueryRowContext(ctx.Request.Context(), `
			SELECT id FROM employees WHERE employee_id = $1 LIMIT 1
		`, strings.TrimSpace(employeeID)).Scan(&id)
		if err == nil {
			return id, true, nil
		}
	}

	var id uuid.UUID
	err := h.db.QueryRowContext(ctx.Request.Context(), `
		SELECT id FROM employees WHERE name = $1 ORDER BY created_at ASC LIMIT 1
	`, strings.TrimSpace(name)).Scan(&id)
	if err != nil {
		return uuid.Nil, false, nil
	}
	return id, true, nil
}
func (h *WorkforceHandler) upsertAttendanceForImport(ctx *gin.Context, employeeID uuid.UUID, yearMonth, position string, dayStatus map[int]string) (int, error) {
	if len(dayStatus) == 0 {
		return 0, nil
	}

	parts := strings.Split(yearMonth, "-")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid year_month")
	}

	updated := 0
	for day, status := range dayStatus {
		if status == "" {
			continue
		}
		dateKey := fmt.Sprintf("%s-%02d", yearMonth, day)
		_, err := h.db.ExecContext(ctx.Request.Context(), `
			INSERT INTO attendance_records (employee_id, year_month, date, status, position_snapshot, manually_edited, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, TRUE, NOW(), NOW())
			ON CONFLICT (employee_id, year_month, date) DO UPDATE SET
				status = EXCLUDED.status,
				position_snapshot = EXCLUDED.position_snapshot,
				manually_edited = TRUE,
				updated_at = NOW()
		`, employeeID, yearMonth, dateKey, status, position)
		if err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}
func (h *WorkforceHandler) ensurePerformanceForMonth(ctx *gin.Context, employeeID uuid.UUID, name, position, yearMonth string) error {
	_, err := h.db.ExecContext(ctx.Request.Context(), `
		INSERT INTO performance_records (employee_id, employee_name, position, score_records, total_score, grade, month, created_at, updated_at)
		VALUES ($1, $2, $3, '[]'::jsonb, 10, '合格', $4, NOW(), NOW())
		ON CONFLICT (employee_id, month) DO NOTHING
	`, employeeID, name, position, yearMonth)
	return err
}

// ImportAttendanceRecords 导入考勤表，新员工可自动创建子账号与邀请码
func (h *WorkforceHandler) ImportAttendanceRecords(c *gin.Context) {
	yearMonth := strings.TrimSpace(c.PostForm("year_month"))
	defaultPassword := strings.TrimSpace(c.PostForm("default_password"))

	if yearMonth == "" {
		models.SendError(c, 400, 1001, "year_month 不能为空")
		return
	}
	if len(defaultPassword) > 0 && len(defaultPassword) < 6 {
		models.SendError(c, 400, 1001, "默认密码至少 6 位")
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		models.SendError(c, 400, 1001, "请上传文件")
		return
	}

	rows, err := readSiteStatsRowsFromFileHeader(file)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	cols, err := parseAttendanceImportHeader(rows[0])
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	items, err := parseAttendanceImportRows(rows, cols)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}

	createdBy := parseContextUserUUID(c)
	createdEmployees := 0
	updatedEmployees := 0
	attendanceCells := 0
	newAccounts := make([]gin.H, 0)

	for _, item := range items {
		employeeUUID, found, err := h.lookupEmployeeID(c, item.EmployeeID, item.Name)
		if err != nil {
			models.SendError(c, 500, 5000, err.Error())
			return
		}

		if !found {
			username := strings.TrimSpace(item.Username)
			employeeCode := strings.TrimSpace(item.EmployeeID)
			if username == "" {
				username = employeeCode
			}
			if employeeCode == "" {
				employeeCode = username
			}
			if username == "" {
				models.SendError(c, 400, 1001, fmt.Sprintf("员工「%s」不存在，请填写登录账号或工号以自动创建子账号", item.Name))
				return
			}
			if username != employeeCode {
				employeeCode = username
			}

			password := strings.TrimSpace(item.Password)
			if password == "" {
				password = defaultPassword
			}
			if len(password) < 6 {
				models.SendError(c, 400, 1001, fmt.Sprintf("员工「%s」需填写初始密码，或在导入时设置默认密码（至少6位）", item.Name))
				return
			}

			result, err := h.workforce.CreateEmployeeAccount(c.Request.Context(), &services.CreateEmployeeAccountInput{
				Username:     username,
				Password:     password,
				EmployeeID:   employeeCode,
				Name:         item.Name,
				Position:     item.Position,
				HireDate:     item.HireDate,
				WorkLocation: item.WorkLocation,
				CreatedBy:    createdBy,
			})
			if err != nil {
				if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
					models.SendError(c, 400, 1001, fmt.Sprintf("员工「%s」创建失败：登录账号或工号已存在", item.Name))
					return
				}
				models.SendError(c, 500, 5000, fmt.Sprintf("创建员工「%s」失败: %v", item.Name, err))
				return
			}

			employeeUUID = result.EmployeeID
			createdEmployees++
			newAccounts = append(newAccounts, gin.H{
				"name":       item.Name,
				"username":   username,
				"employeeId": result.EmployeeID.String(),
				"inviteCode": result.InviteCode,
			})
		} else {
			updatedEmployees++
		}

		if err := h.ensurePerformanceForMonth(c, employeeUUID, item.Name, item.Position, yearMonth); err != nil {
			models.SendError(c, 500, 5000, err.Error())
			return
		}

		count, err := h.upsertAttendanceForImport(c, employeeUUID, yearMonth, item.Position, item.DayStatus)
		if err != nil {
			models.SendError(c, 500, 5000, fmt.Sprintf("导入员工「%s」考勤失败: %v", item.Name, err))
			return
		}
		attendanceCells += count
	}

	Log(c, "CREATE", "ATTENDANCE",
		fmt.Sprintf("导入 %s 月考勤：新增员工 %d，更新 %d，写入 %d 条考勤", yearMonth, createdEmployees, updatedEmployees, attendanceCells),
		"", yearMonth)

	models.Send(c, 200, 0, "Import completed", gin.H{
		"createdEmployees": createdEmployees,
		"updatedEmployees": updatedEmployees,
		"attendanceCells":  attendanceCells,
		"newAccounts":      newAccounts,
	})
}

func normalizeRoleName(roleName string) string {
	switch strings.TrimSpace(strings.ToLower(roleName)) {
	case "管理员", "admin", "administrator", "超级管理员", "superadmin":
		return "admin"
	case "审计员", "auditor":
		return "auditor"
	case "操作员", "operator", "ops":
		return "operator"
	case "查看员", "viewer":
		return "viewer"
	case "员工", "employee":
		return "employee"
	default:
		return "auditor"
	}
}
var _ = context.Background

// deviceSecretMatch compares provided vs stored device secrets in constant time.
func deviceSecretMatch(provided, stored string) bool {
	provided = strings.TrimSpace(provided)
	stored = strings.TrimSpace(stored)
	if provided == "" || stored == "" {
		return false
	}
	if len(provided) != len(stored) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(stored)) == 1
}

// ---- Source: backend/handlers/employee_account_import.go ----
const maxEmployeeAccountImportRows = 2000

// ---- Source: backend/handlers/health.go ----
const (
	healthCheckTimeout    = 2 * time.Second
	diskNotReadyPercent   = 95.0
	diskWritableProbeFile = ".health_probe"
)

var errMediaAccessDenied = errors.New("media access denied")

var (
	errInvalidMediaAction   = &mediaRequestError{"unsupported media action"}
	errInvalidMediaResource = &mediaRequestError{"unsupported resource type"}
)

type mediaRequestError struct {
	msg string
}

func (e *mediaRequestError) Error() string { return e.msg }
// ---- Source: backend/handlers/operation_log.go ----
type OperationLogHandler struct {
	db *sql.DB
}

func NewOperationLogHandler(db *sql.DB) *OperationLogHandler {
	return &OperationLogHandler{db: db}
}

type operationDescRule struct {
	pathKeyword string
	method      string
	description string
}

// 路径匹配顺序：越具体的规则越靠前
var operationDescRules = []operationDescRule{
	{"/media/access-token", "POST", "申请截图/录屏媒体访问令牌"},
	{"/ws-ticket", "POST", "申请 WebSocket 连接票据（远程查看等）"},
	{"/notifications/read-all", "POST", "将全部通知标记为已读"},
	{"/notifications/", "read", "标记单条通知为已读"},
	{"/user/totp/enroll/confirm", "POST", "确认绑定谷歌验证器"},
	{"/user/totp/enroll", "POST", "发起绑定谷歌验证器"},
	{"/user/change-password", "POST", "修改登录密码"},
	{"/diagnostics/ingest", "POST", "诊断中心前端上报"},
	{"/diagnostics/capture/export", "POST", "导出诊断抓取包"},
	{"/diagnostics/capture", "POST", "诊断中心一键抓取"},
	{"/diagnostics/reproduce", "POST", "诊断中心启动复现任务"},
	{"/diagnostics/templates/apply", "POST", "诊断中心应用模板"},
	{"/recordings/batch-delete", "POST", "批量删除录屏"},
	{"/remote-view/quality", "POST", "调整远程查看画质"},
	{"/remote-view/start", "POST", "发起远程查看"},
	{"/remote-view/stop", "POST", "结束远程查看"},
	{"/performance/push-settings/token", "PUT", "保存绩效 Telegram 推送 Bot Token"},
	{"/performance/push-settings/discover", "POST", "发现绩效推送频道或群组"},
	{"/performance/push-settings/bind", "PUT", "绑定绩效推送频道或群组"},
	{"/performance/push-settings/bind", "DELETE", "解绑绩效推送频道或群组"},
	{"/performance/push-settings/test", "POST", "测试绩效 Telegram 推送"},
	{"/performance/push-notify", "POST", "推送新增绩效记录到 Telegram"},
	{"/penalty/push-settings/token", "PUT", "保存罚款 Telegram 推送 Bot Token"},
	{"/penalty/push-settings/discover", "POST", "发现罚款推送频道或群组"},
	{"/penalty/push-settings/bind", "PUT", "绑定罚款推送频道或群组"},
	{"/penalty/push-settings/bind", "DELETE", "解绑罚款推送频道或群组"},
	{"/penalty/push-settings/test", "POST", "测试罚款 Telegram 推送"},
	{"/penalty/push-notify", "POST", "推送新增罚款记录到 Telegram"},
	{"/workforce/telegram/settings", "PUT", "更新 Telegram 考勤配置"},
	{"/workforce/telegram/groups", "POST", "绑定 Telegram 考勤群"},
	{"/workforce/telegram/groups", "DELETE", "删除 Telegram 考勤群"},
	{"/workforce/telegram/bindings", "POST", "创建 Telegram 员工绑定"},
	{"/workforce/telegram/bindings", "DELETE", "解除 Telegram 员工绑定"},
	{"/workforce/telegram/bindings/", "reset-daily", "重置员工当日 Telegram 打卡"},
	{"/workforce/telegram/daily-reset", "POST", "手动执行 Telegram 日重置"},
	{"/workforce/telegram/push-settings", "PUT", "更新 Telegram 推送配置"},
	{"/workforce/telegram/push-settings/test", "POST", "测试 Telegram 推送配置"},
	{"/workforce/telegram/work-fines/batch", "POST", "批量新增上下班罚款规则"},
	{"/workforce/telegram/work-fines", "POST", "新增上下班罚款规则"},
	{"/workforce/telegram/work-fines", "DELETE", "删除上下班罚款规则"},
	{"/workforce/telegram/activity-fines/batch", "POST", "批量新增活动超时罚款规则"},
	{"/workforce/telegram/activity-fines", "POST", "新增活动超时罚款规则"},
	{"/workforce/telegram/activity-fines", "DELETE", "删除活动超时罚款规则"},
	{"/workforce/telegram/handover-config", "PUT", "更新 Telegram 换班日配置"},
	{"/workforce/telegram/shared-workstations", "POST", "添加共享工位成员"},
	{"/workforce/telegram/shared-workstations", "DELETE", "移除共享工位成员"},
	{"/invite-codes/regenerate", "POST", "重新生成员工邀请码"},
	{"/invite-codes/settings", "PUT", "更新邀请码默认有效期"},
	{"/invite-codes/", "revoke", "作废邀请码"},
	{"/settings/cors-origins", "PUT", "更新 CORS 白名单"},
	{"/settings/auth-security", "PUT", "更新登录安全策略"},
	{"/settings/login-ip-whitelist", "PUT", "更新登录 IP 白名单"},
	{"/notifications", "PUT", "更新系统通知"},
	{"/notifications", "DELETE", "删除系统通知"},
	{"/devices/:id/remote-view/start", "POST", "发起远程查看"},
	{"/devices/:id/remote-view/stop", "POST", "结束远程查看"},
	{"/site-stats/upload/preview", "POST", "预览出款统计上传表头"},
	{"/site-stats/upload", "POST", "上传出款统计数据"},
	{"/site-stats/data/clear-by-date", "DELETE", "清除指定站点出款统计"},
	{"/site-stats/clear-by-date-only", "DELETE", "清除指定日期全部出款统计"},
	{"/site-stats/clear-by-date-shift", "DELETE", "清除指定日期班次出款统计"},
	{"/site-stats/data/clear", "DELETE", "清空全部出款统计数据"},
	{"/site-stats/sites", "POST", "新增出款站点"},
	{"/site-stats/sites", "PUT", "修改出款站点"},
	{"/site-stats/sites", "DELETE", "删除出款站点"},
	{"/site-stats/employee-accounts/import", "POST", "导入出款员工账号"},
	{"/site-stats/employee-accounts/batch-shift", "POST", "批量修改出款员工班次"},
	{"/site-stats/employee-accounts", "POST", "新增出款员工账号"},
	{"/site-stats/employee-accounts", "PUT", "修改出款员工账号"},
	{"/site-stats/employee-accounts", "DELETE", "删除出款员工账号"},
	{"/employees", "POST", "新增员工"},
	{"/employees", "PUT", "修改员工信息"},
	{"/employees", "DELETE", "删除员工"},
	{"/attendance/records/import", "POST", "导入考勤记录"},
	{"/attendance/records", "POST", "保存考勤记录"},
	{"/performance/batch", "POST", "批量保存绩效考核"},
	{"/performance/", "PUT", "修改员工绩效考核"},
	{"/performance/", "DELETE", "删除员工绩效考核"},
	{"/penalty/record", "POST", "添加罚款记录"},
	{"/penalty/records", "PUT", "修改罚款记录"},
	{"/penalty/records", "DELETE", "删除罚款记录"},
	{"/salary/upload", "POST", "上传工资表"},
	{"/salary/records", "PUT", "修改工资记录"},
	{"/salary/records", "DELETE", "删除工资记录"},
	{"/users", "POST", "创建系统用户"},
	{"/users", "PUT", "修改系统用户"},
	{"/users", "DELETE", "删除系统用户"},
	{"/roles", "POST", "创建角色"},
	{"/roles", "PUT", "修改角色"},
	{"/roles", "DELETE", "删除角色"},
	{"/policies/", "publish", "发布监控策略"},
	{"/policies", "POST", "创建监控策略"},
	{"/policies", "PUT", "修改监控策略"},
	{"/policies", "DELETE", "删除监控策略"},
	{"/policies/", "rollback", "回滚监控策略"},
	{"/devices", "POST", "绑定监控设备"},
	{"/devices", "PUT", "修改设备信息"},
	{"/devices", "DELETE", "解绑监控设备"},
	{"/devices/", "bind", "绑定设备到员工"},
	{"/devices/", "unbind", "解绑设备员工"},
	{"/devices/", "force-offline", "强制设备下线"},
	{"/devices/", "allow-reregister", "放行设备重新注册"},
	{"/devices/", "status", "更新设备审核状态"},
	{"/recordings", "DELETE", "删除录屏文件"},
	{"/screenshots/batch-delete", "POST", "批量删除截图"},
	{"/screenshots/purge", "POST", "批量清理截图"},
	{"/screenshots", "DELETE", "删除截图"},
	{"/cleanup/manual", "POST", "手动执行存储清理"},
	{"/cleanup/policy", "PUT", "修改存储清理策略"},
	{"/tasks/batch-status", "POST", "批量更新任务状态"},
	{"/tasks/", "cancel", "取消上传任务"},
	{"/tasks/", "retry", "重试上传任务"},
	{"/tasks", "POST", "创建上传任务"},
	{"/tasks", "DELETE", "删除上传任务"},
	{"/user/profile", "PUT", "修改个人资料"},
	{"/user/change-password", "POST", "修改登录密码"},
	{"/domain-monitor/settings/check", "PUT", "保存域名监控检测设置"},
	{"/domain-monitor/groups", "POST", "新增域名分组"},
	{"/domain-monitor/groups", "PUT", "修改域名分组"},
	{"/domain-monitor/groups", "DELETE", "删除域名分组"},
	{"/domain-monitor/domains", "POST", "新增监控域名"},
	{"/domain-monitor/domains/import", "POST", "导入监控域名"},
	{"/domain-monitor/domains/batch-update", "POST", "批量修改监控域名"},
	{"/domain-monitor/domains", "PUT", "修改监控域名"},
	{"/domain-monitor/domains", "DELETE", "删除监控域名"},
	{"/domain-monitor/domains/", "check", "立即检测监控域名"},
	{"/domain-monitor/domains/check-all", "POST", "立即检测全部域名"},
	{"/domain-monitor/alerts/", "ack", "确认域名监控告警"},
	{"/domain-monitor/alerts/", "resolve", "关闭域名监控告警"},
	{"/domain-monitor/notify/telegram/bots", "POST", "新增域名监控 Telegram Bot"},
	{"/domain-monitor/notify/telegram/bots", "PUT", "修改域名监控 Telegram Bot"},
	{"/domain-monitor/notify/telegram/bots", "DELETE", "删除域名监控 Telegram Bot"},
	{"/domain-monitor/notify/routes", "POST", "新增域名告警推送路由"},
	{"/domain-monitor/notify/routes", "PUT", "修改域名告警推送路由"},
	{"/domain-monitor/notify/routes", "DELETE", "删除域名告警推送路由"},
	{"/domain-monitor/nodes", "POST", "新增域名监控探针节点"},
	{"/domain-monitor/nodes", "PUT", "修改域名监控探针节点"},
	{"/domain-monitor/nodes", "DELETE", "删除域名监控探针节点"},
	{"/domain-monitor/reports/generate", "POST", "生成域名监控报表"},
	{"/callback/platforms", "POST", "新增回传平台"},
	{"/callback/platforms", "PUT", "修改回传平台"},
	{"/callback/platforms", "DELETE", "删除回传平台"},
	{"/callback/endpoints", "POST", "新增回传 API 端点"},
	{"/callback/endpoints", "PUT", "修改回传 API 端点"},
	{"/callback/endpoints", "DELETE", "删除回传 API 端点"},
	{"/callback/activity", "PUT", "更新回传活动配置"},
	{"/callback/tasks/generate", "POST", "生成回传任务"},
	{"/callback/manual/process", "POST", "处理运营工具手工导入"},
	{"/workforce/telegram/activity-types", "PUT", "保存 Telegram 活动类型"},
	{"/workforce/telegram/activity-types", "DELETE", "删除 Telegram 活动类型"},
	{"/workforce/telegram/device-bindings/", "reset-daily", "工位 TG 重置当日考勤"},
	{"/workforce/telegram/export", "GET", "导出 Telegram 考勤表"},
	{"/activities/export", "GET", "导出活动日志"},
	{"/activities", "DELETE", "批量删除活动日志"},
	{"/devices/", "remote-view/start", "发起远程查看"},
	{"/devices/", "remote-view/stop", "结束远程查看"},
	{"/devices/", "remote-view/quality", "调整远程查看画质"},
	{"/devices/", "employee", "更新设备员工信息"},
}

// saveOperationLog 保存操作日志到数据库（包含 IP 和耗时）
func saveOperationLog(db *sql.DB, log *models.OperationLog) {
	query := `
        INSERT INTO operation_logs (
            id, operator_id, operator_name, operator_role, operation_type,
            operation_module, operation_desc, target_id, target_name,
            ip_address, user_agent, request_method, request_path, execution_time_ms, created_at
        ) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11, NULLIF($12, ''), NULLIF($13, ''), $14, $15)
    `
	_, err := db.Exec(query,
		log.ID, log.OperatorID, log.OperatorName, log.OperatorRole,
		log.OperationType, log.OperationModule, log.OperationDesc,
		log.TargetID, log.TargetName, log.IPAddress, log.UserAgent,
		log.RequestMethod, log.RequestPath, log.ExecutionTimeMs, log.CreatedAt,
	)
	if err != nil {
		logx.Debug("saveOperationLog failed: %v", err)
	}
}

// SaveOperationLog 公共方法：供其他 handler 调用记录操作日志
func (h *OperationLogHandler) SaveOperationLog(c *gin.Context, operationType, operationModule, operationDesc, targetID, targetName string) {
	operatorID := c.GetString("userID")
	username := c.GetString("username")
	operatorName := resolveOperatorDisplayName(h.db, operatorID, username)
	operatorRole := c.GetString("role")

	if operatorID == "" && operatorName == "" {
		return
	}

	log := &models.OperationLog{
		ID:              uuid.New().String(),
		OperatorID:      operatorID,
		OperatorName:    operatorName,
		OperatorRole:    operatorRole,
		OperationType:   NormalizeOperationType(operationType),
		OperationModule: NormalizeOperationModule(operationModule),
		OperationDesc:   operationDesc,
		TargetID:        targetID,
		TargetName:      targetName,
		IPAddress:       c.ClientIP(),
		UserAgent:       c.Request.UserAgent(),
		RequestMethod:   c.Request.Method,
		RequestPath:     c.Request.URL.Path,
		ExecutionTimeMs: 0,
		CreatedAt:       time.Now(),
	}

	go saveOperationLog(h.db, log)
}

// GlobalLogger 全局操作日志处理器，在 main.go 中初始化
var GlobalLogger *OperationLogHandler

// Log 全局日志记录函数，方便在各个 handler 中调用
func Log(c *gin.Context, operationType, operationModule, operationDesc, targetID, targetName string) {
	if GlobalLogger != nil {
		GlobalLogger.SaveOperationLog(c, operationType, operationModule, operationDesc, targetID, targetName)
	}
}
func resolveOperatorDisplayName(db *sql.DB, operatorID, username string) string {
	var realName sql.NullString

	if operatorID != "" {
		if err := db.QueryRow(`SELECT real_name FROM users WHERE id::text = $1`, operatorID).Scan(&realName); err == nil {
			if realName.Valid && strings.TrimSpace(realName.String) != "" {
				return strings.TrimSpace(realName.String)
			}
		}
	}

	if username != "" {
		if err := db.QueryRow(`SELECT real_name FROM users WHERE username = $1`, username).Scan(&realName); err == nil {
			if realName.Valid && strings.TrimSpace(realName.String) != "" {
				return strings.TrimSpace(realName.String)
			}
		}
		return username
	}

	return ""
}

const operationLogUserJoin = `
LEFT JOIN users u ON (
	(ol.operator_id IS NOT NULL AND ol.operator_id <> '' AND u.id::text = ol.operator_id)
	OR u.username = ol.operator_name
)`
const operationLogOperatorDisplay = `COALESCE(NULLIF(TRIM(u.real_name), ''), ol.operator_name)`

// ---- Source: backend/handlers/operation_log_labels.go ----
var operationTypeLabels = map[string]string{
	"CREATE":   "创建",
	"UPDATE":   "修改",
	"DELETE":   "删除",
	"UPLOAD":   "上传",
	"EXPORT":   "导出",
	"VIEW":     "查看",
	"DOWNLOAD": "下载",
	"LOGIN":    "登录",
	"LOGOUT":   "登出",
	"QUERY":    "查询",
	"POST":     "创建",
	"PUT":      "修改",
	"PATCH":    "修改",
	"OTHER":    "其他",
}
var operationModuleLabels = map[string]string{
	"USER":                "用户管理",
	"ROLE":                "角色管理",
	"DEVICE":              "设备管理",
	"RECORDING":           "录屏管理",
	"SCREENSHOT":          "截图管理",
	"ACTIVITY":            "活动日志",
	"POLICY":              "策略管理",
	"EMPLOYEE":            "员工管理",
	"ATTENDANCE":          "考勤管理",
	"PERFORMANCE":         "绩效管理",
	"PENALTY":             "罚款管理",
	"SITE_STATS":          "出款统计",
	"SALARY":              "工资管理",
	"TASK":                "任务管理",
	"TELEGRAM_ATTENDANCE": "Telegram考勤",
	"INVITE_CODE":         "邀请码管理",
	"EMPLOYEE_ACCOUNT":    "员工账号",
	"SYSTEM":              "系统设置",
	"AUTH":                "登录认证",
	"NOTIFICATION":        "消息通知",
	"REMOTE_VIEW":         "远程查看",
	"MEDIA":               "媒体访问",
	"DIAGNOSTIC":          "诊断中心",
	"DOMAIN_MONITOR":      "域名监控",
	"CALLBACK":            "运营工具",
	"OTHER":               "其他",
}
var operatorRoleLabels = map[string]string{
	"admin":       "管理员",
	"super_admin": "超级管理员",
	"operator":    "操作员",
	"auditor":     "审计员",
	"hr":          "人事",
	"finance":     "财务",
	"attendance":  "考勤员",
}

// NormalizeOperationType 将操作类型转为中文展示
func NormalizeOperationType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "其他"
	}
	if cn, ok := operationTypeLabels[strings.ToUpper(value)]; ok {
		return cn
	}
	if cn, ok := operationTypeLabels[value]; ok {
		return cn
	}
	return value
}

// NormalizeOperationModule 将操作模块转为中文展示
func NormalizeOperationModule(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "其他"
	}
	if cn, ok := operationModuleLabels[strings.ToUpper(value)]; ok {
		return cn
	}
	if cn, ok := operationModuleLabels[value]; ok {
		return cn
	}
	return value
}

// ---- Source: backend/handlers/payment_address.go ----
const (
	paymentMethodBank  = "bank"
	paymentMethodUsdt  = "usdt"
	paymentMethodOther = "other"
)

var (
	errTotpRequired     = errors.New("totp required")
	errInvalidUser      = errors.New("invalid user")
	errTotpNotBound     = errors.New("totp not bound")
	errInvalidTotpCode  = errors.New("invalid totp code")
	errEmployeeNotFound = errors.New("employee not found")
)

const attendancePositionExpr = `
	COALESCE(
		NULLIF(TRIM(e.position), ''),
		NULLIF((
			SELECT ar.position_snapshot
			FROM attendance_records ar
			WHERE ar.employee_id = e.id
			  AND NULLIF(TRIM(ar.position_snapshot), '') IS NOT NULL
			ORDER BY ar.year_month DESC, ar.date DESC
			LIMIT 1
		), ''),
		''
	)`
const attendanceDepartmentExpr = `
	COALESCE(NULLIF(TRIM(e.department), ''), NULLIF(TRIM(e.work_location), ''), '')`

const paymentAddressSelectCols = `
	pa.id::text, pa.employee_id::text, pa.account_holder, pa.bank_name, pa.bank_account,
	pa.usdt_network, pa.usdt_address, pa.other_wallet_type, pa.other_wallet_address,
	COALESCE(pa.preferred_method, ''), pa.remark, COALESCE(pa.notification_email, ''), pa.created_at, pa.updated_at`

// ---- Source: backend/handlers/penalty_push.go ----
func (h *AdminHandler) GetPenaltyPushSettings(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	settings, err := h.penaltyPush.GetSettings(c.Request.Context())
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", settings)
}
func (h *AdminHandler) SavePenaltyPushToken(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	var req struct {
		BotToken string `json:"botToken"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	settings, err := h.penaltyPush.SaveBotToken(c.Request.Context(), req.BotToken)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "PENALTY", "保存罚款 Telegram 推送 Bot Token", "", settings.BotUsername)
	models.Send(c, 200, 0, "Token 已保存", settings)
}
func (h *AdminHandler) SavePenaltyPushEnabled(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	settings, err := h.penaltyPush.SetPushEnabled(c.Request.Context(), req.Enabled)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	action := "关闭罚款 Telegram 自动推送"
	if req.Enabled {
		action = "开启罚款 Telegram 自动推送"
	}
	Log(c, "UPDATE", "PENALTY", action, "", fmt.Sprintf("enabled=%v", req.Enabled))
	models.Send(c, 200, 0, "已保存", settings)
}
func (h *AdminHandler) DiscoverPenaltyPushChats(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	groups, err := h.penaltyPush.DiscoverChats(c.Request.Context())
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", groups)
}
func (h *AdminHandler) BindPenaltyPushChat(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	var req struct {
		ChatID    string `json:"chatId" binding:"required"`
		ChatTitle string `json:"chatTitle"`
		ChatType  string `json:"chatType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	settings, err := h.penaltyPush.BindChat(c.Request.Context(), req.ChatID, req.ChatTitle, req.ChatType)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "PENALTY",
		fmt.Sprintf("绑定罚款推送频道/群组：%s", settings.ChatTitle),
		settings.ChatID, settings.ChatTitle)
	models.Send(c, 200, 0, "绑定成功", settings)
}
func (h *AdminHandler) UnbindPenaltyPushChat(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	settings, err := h.penaltyPush.UnbindChat(c.Request.Context())
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "UPDATE", "PENALTY", "解绑罚款推送频道/群组", "", "")
	models.Send(c, 200, 0, "已解绑", settings)
}
func (h *AdminHandler) TestPenaltyPush(c *gin.Context) {
	if h.penaltyPush == nil {
		models.SendError(c, 500, 5000, "penalty push service unavailable")
		return
	}
	if err := h.penaltyPush.TestPush(c.Request.Context()); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "PENALTY", "测试罚款 Telegram 推送", "", "")
	models.Send(c, 200, 0, "测试消息已发送", nil)
}
func (h *AdminHandler) NotifyPenaltyPush(c *gin.Context) {
	if h.penaltyPush == nil {
		models.Send(c, 200, 0, "skipped", gin.H{"sent": false})
		return
	}
	var req services.PenaltyPushRecord
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	sent, err := h.penaltyPush.PushPenaltyRecord(c.Request.Context(), req)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	if sent {
		Log(c, "CREATE", "PENALTY",
			fmt.Sprintf("推送罚款：%s %s %s（%s）",
				req.Date, req.EmployeeName,
				services.FormatFineMoney(req.Amount, h.penaltyPush.FineCurrencyCode(c.Request.Context())),
				req.Reason),
			req.EmployeeName, req.EmployeeName)
		if h.agentNotify != nil {
			empID := uuid.Nil
			if parsed, err := uuid.Parse(req.EmployeeID); err == nil {
				empID = parsed
			}
			h.agentNotify.PushPenalty(empID, req.Amount, req.Category, req.Reason, req.Date, req.EmployeeName)
		}
	}
	models.Send(c, 200, 0, "success", gin.H{"sent": sent})
}

// ---- Source: backend/handlers/performance_push.go ----
func (h *AdminHandler) GetPerformancePushSettings(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	settings, err := h.perfPush.GetSettings(c.Request.Context())
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", settings)
}
func (h *AdminHandler) SavePerformancePushToken(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	var req struct {
		BotToken string `json:"botToken"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	settings, err := h.perfPush.SaveBotToken(c.Request.Context(), req.BotToken)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "PERFORMANCE", "保存绩效 Telegram 推送 Bot Token", "", settings.BotUsername)
	models.Send(c, 200, 0, "Token 已保存", settings)
}
func (h *AdminHandler) SavePerformancePushEnabled(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	settings, err := h.perfPush.SetPushEnabled(c.Request.Context(), req.Enabled)
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	action := "关闭绩效 Telegram 自动推送"
	if req.Enabled {
		action = "开启绩效 Telegram 自动推送"
	}
	Log(c, "UPDATE", "PERFORMANCE", action, "", fmt.Sprintf("enabled=%v", req.Enabled))
	models.Send(c, 200, 0, "已保存", settings)
}
func (h *AdminHandler) DiscoverPerformancePushChats(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	groups, err := h.perfPush.DiscoverChats(c.Request.Context())
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	models.Send(c, 200, 0, "success", groups)
}
func (h *AdminHandler) BindPerformancePushChat(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	var req struct {
		ChatID    string `json:"chatId" binding:"required"`
		ChatTitle string `json:"chatTitle"`
		ChatType  string `json:"chatType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	settings, err := h.perfPush.BindChat(c.Request.Context(), req.ChatID, req.ChatTitle, req.ChatType)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "PERFORMANCE",
		fmt.Sprintf("绑定绩效推送频道/群组：%s", settings.ChatTitle),
		settings.ChatID, settings.ChatTitle)
	models.Send(c, 200, 0, "绑定成功", settings)
}
func (h *AdminHandler) UnbindPerformancePushChat(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	settings, err := h.perfPush.UnbindChat(c.Request.Context())
	if err != nil {
		models.SendError(c, 500, 5000, err.Error())
		return
	}
	Log(c, "UPDATE", "PERFORMANCE", "解绑绩效推送频道/群组", "", "")
	models.Send(c, 200, 0, "已解绑", settings)
}
func (h *AdminHandler) TestPerformancePush(c *gin.Context) {
	if h.perfPush == nil {
		models.SendError(c, 500, 5000, "performance push service unavailable")
		return
	}
	if err := h.perfPush.TestPush(c.Request.Context()); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	Log(c, "UPDATE", "PERFORMANCE", "测试绩效 Telegram 推送", "", "")
	models.Send(c, 200, 0, "测试消息已发送", nil)
}
func (h *AdminHandler) NotifyPerformancePush(c *gin.Context) {
	if h.perfPush == nil {
		models.Send(c, 200, 0, "skipped", gin.H{"sent": false})
		return
	}
	var req services.PerformancePushRecord
	if err := c.ShouldBindJSON(&req); err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	sent, err := h.perfPush.PushScoreRecord(c.Request.Context(), req)
	if err != nil {
		models.SendError(c, 400, 1001, err.Error())
		return
	}
	if sent {
		change := "加分"
		if req.Score < 0 {
			change = "扣分"
		}
		Log(c, "CREATE", "PERFORMANCE",
			fmt.Sprintf("推送绩效%s：%s %s %d分（%s）", change, req.Date, req.EmployeeName, req.Score, req.Reason),
			req.EmployeeName, req.EmployeeName)
		if h.agentNotify != nil {
			empID := uuid.Nil
			if parsed, err := uuid.Parse(req.EmployeeID); err == nil {
				empID = parsed
			}
			h.agentNotify.PushPerformance(empID, req)
		}
	}
	models.Send(c, 200, 0, "success", gin.H{"sent": sent})
}

// ---- Source: backend/handlers/recording.go ----
// 批量删除最大数量限制
const maxRecordingBatchDelete = 50

// ---- Source: backend/handlers/remote_view_audit_labels.go ----
var remoteViewAuditActionLabels = map[string]string{
	"session_start": "开始远程查看",
	"viewer_join":   "加入查看",
	"viewer_leave":  "离开查看",
	"session_stop":  "结束远程查看",
}

// ---- Source: backend/handlers/salary_payment_proof.go ----
var yearMonthPattern = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

var allowedPaymentProofExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
}

// ---- Source: backend/handlers/salary_view_gate.go ----
const (
	salaryViewPasswordMinLen = 6
	salaryViewPasswordMaxLen = 64
)

// ---- Source: backend/handlers/screen_lock.go ----
const (
	screenLockPasswordMinLen = 6
	screenLockPasswordMaxLen = 64
)

// ---- Source: backend/handlers/screenshot.go ----
// 常量定义
const (
	maxBatchDeleteSize       = 100
	screenshotPurgeBatchSize = 500
	imageCacheMaxAge         = 3600 // 1小时
)

// ✅ 允许的图片扩展名
var allowedImageExts = []string{
	".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp",
}

// ✅ 安全的基础目录（在初始化时设置）
var (
	safeBaseDirs     []string
	safeBaseDirsLock sync.RWMutex
)

func readSiteStatsRowsFromFileHeader(file *multipart.FileHeader) ([][]string, error) {
	src, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return readSiteStatsRows(src, file.Filename)
}
func readSiteStatsRows(src io.Reader, filename string) ([][]string, error) {
	isCSV := strings.HasSuffix(strings.ToLower(filename), ".csv")
	if isCSV {
		reader := csvPackage2.NewReader(src)
		reader.Comma = ','
		reader.FieldsPerRecord = -1
		reader.TrimLeadingSpace = true
		reader.LazyQuotes = true
		rows, err := reader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("CSV文件解析失败: %w", err)
		}
		return rows, nil
	}

	f, err := excelize.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("无法解析 Excel 文件: %w", err)
	}
	defer f.Close()

	sheetName := f.GetSheetName(0)
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
// ---- Source: backend/handlers/task.go ----
// 任务状态常量
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"
)

// 有效任务状态列表
var validTaskStatuses = map[string]bool{
	TaskStatusPending:   true,
	TaskStatusRunning:   true,
	TaskStatusCompleted: true,
	TaskStatusFailed:    true,
	TaskStatusCancelled: true,
	"skipped":           true,
}

// ---- Source: backend/handlers/upload.go ----
// ========== 常量定义 ==========
const (
	maxChunkSize               = 5 * 1024 * 1024    // 5MB
	maxTotalFileSize           = 1024 * 1024 * 1024 // 1GB
	maxUploadSessions          = 10000              // 最大上传会话数
	sessionTTL                 = 1 * time.Hour      // 会话过期时间
	sessionCleanupInterval     = 10 * time.Minute
	nonceCacheTTL              = 10 * time.Minute
	timestampTolerance         = 5 * time.Minute
	maxConcurrentUploadsPerIP  = 5               // 每IP最大并发上传
	rateLimitRequestsPerSecond = 10              // 限流请求数/秒
	rateLimitBurst             = 20              // 限流突发容量
	rateLimiterCleanupInterval = 5 * time.Minute // 限流器清理间隔

	headerUploadID      = "X-Upload-Id"
	headerFileName      = "X-File-Name"
	headerFileType      = "X-File-Type"
	headerFileID        = "X-File-Id"
	headerChunkIndex    = "X-Chunk-Index"
	headerTotalChunks   = "X-Total-Chunks"
	headerChunkChecksum = "X-Chunk-Checksum"
)

// ========== 上传会话结构 ==========
type uploadSession struct {
	UploadID       string
	DeviceID       string
	FileName       string
	FileType       string
	FileID         string
	TotalChunks    int
	Received       map[int]bool
	ChunkChecksums map[int]string
	LastActive     time.Time
	TotalSize      int64
	mu             sync.RWMutex
	Completed      bool      // 是否已完成
	Completing     bool      // 是否正在完成中（防并发）
	CompletedAt    time.Time // 完成时间（响应返回）
	// CompletedOnce sync.Once     // 确保只执行一次（双重保险）
}

// ========== 会话管理器 ==========
type uploadSessionManager struct {
	sessions map[string]*uploadSession
	mu       sync.RWMutex
	stopCh   chan struct{}
	once     sync.Once
}

var sessionManager = &uploadSessionManager{
	sessions: make(map[string]*uploadSession),
	stopCh:   make(chan struct{}),
}

// startSessionManager 启动会话管理器（单个 init）
func startSessionManager() {
	go sessionManager.cleanupLoop()
}
func init() {
	startSessionManager()
	startNonceCache()
	startRateLimiterCleanup()
}
func (m *uploadSessionManager) cleanupLoop() {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.cleanup()
		}
	}
}
func (m *uploadSessionManager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, session := range m.sessions {
		// 安全读取状态
		session.mu.RLock()
		completed := session.Completed
		completing := session.Completing
		lastActive := session.LastActive
		completedAt := session.CompletedAt
		session.mu.RUnlock()

		// ⭐⭐⭐ 最关键：正在完成的不删
		if completing {
			continue
		}

		// ✅ 活跃 session 过期清理（默认 1 小时）
		if !completed && now.Sub(lastActive) > sessionTTL {
			logPackage2.Printf("[Cleanup] Removing expired session: %s (last active: %v)",
				id, lastActive.Format("2006-01-02 15:04:05"))
			delete(m.sessions, id)
			purgeUploadArtifacts(id)
			m.removeSessionFromRedis(id)
			continue
		}

		// ✅ 已完成 session 兜底清理（60分钟，远超 Complete 的 30 分钟）
		if completed && !completedAt.IsZero() && now.Sub(completedAt) > 60*time.Minute {
			logPackage2.Printf("[Cleanup] Removing stale completed session: %s (completed at %v)",
				id, completedAt.Format("2006-01-02 15:04:05"))
			delete(m.sessions, id)
			purgeUploadArtifacts(id)
			m.removeSessionFromRedis(id)
		}
	}
}
func (s *uploadSession) checkOwner(ownerDeviceID string) error {
	if ownerDeviceID == "" {
		return fmt.Errorf("device not authenticated")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.DeviceID == "" {
		return fmt.Errorf("upload session has no owner")
	}
	if !strings.EqualFold(s.DeviceID, ownerDeviceID) {
		return fmt.Errorf("upload session device mismatch")
	}
	return nil
}
func (m *uploadSessionManager) getOrCreate(req *chunkUpload, ownerDeviceID string) (*uploadSession, error) {
	if ownerDeviceID == "" {
		return nil, fmt.Errorf("device not authenticated")
	}

	m.mu.RLock()
	session, exists := m.sessions[req.UploadID]
	m.mu.RUnlock()

	if exists {
		if err := session.checkOwner(ownerDeviceID); err != nil {
			return nil, err
		}
		session.mu.Lock()
		session.LastActive = time.Now()
		session.mu.Unlock()
		m.persistSession(session)
		return session, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if session, exists = m.sessions[req.UploadID]; exists {
		if err := session.checkOwner(ownerDeviceID); err != nil {
			return nil, err
		}
		session.mu.Lock()
		session.LastActive = time.Now()
		session.mu.Unlock()
		m.persistSession(session)
		return session, nil
	}

	if session, ok := m.loadSessionFromRedis(req.UploadID); ok {
		if err := session.checkOwner(ownerDeviceID); err != nil {
			return nil, err
		}
		session.mu.Lock()
		session.LastActive = time.Now()
		session.mu.Unlock()
		m.sessions[req.UploadID] = session
		m.persistSession(session)
		return session, nil
	}

	// LRU 淘汰（跳过正在完成或活跃的会话）
	if len(m.sessions) >= maxUploadSessions {
		var oldestID string
		var oldestTime time.Time
		for id, s := range m.sessions {
			s.mu.RLock()
			completing := s.Completing
			completed := s.Completed
			lastActive := s.LastActive
			s.mu.RUnlock()

			if completing || (!completed && time.Since(lastActive) < 5*time.Minute) {
				continue
			}

			if oldestID == "" || lastActive.Before(oldestTime) {
				oldestID = id
				oldestTime = lastActive
			}
		}
		if oldestID != "" {
			delete(m.sessions, oldestID)
			os.RemoveAll(filepath.Join(os.TempDir(), "uploads", oldestID))
		}
	}

	session = &uploadSession{
		UploadID:       req.UploadID,
		DeviceID:       ownerDeviceID,
		FileName:       req.FileName,
		FileType:       req.FileType,
		FileID:         req.FileID,
		TotalChunks:    req.TotalChunks,
		Received:       make(map[int]bool),
		ChunkChecksums: make(map[int]string),
		LastActive:     time.Now(),
		TotalSize:      0,
	}
	m.sessions[req.UploadID] = session
	m.persistSession(session)
	if uploadManifestStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := uploadManifestStore.Init(ctx, services.UploadManifest{
			UploadID:    req.UploadID,
			DeviceID:    ownerDeviceID,
			FileName:    req.FileName,
			FileType:    req.FileType,
			FileID:      req.FileID,
			TotalChunks: req.TotalChunks,
		})
		cancel()
		if err != nil {
			delete(m.sessions, req.UploadID)
			m.removeSessionFromRedis(req.UploadID)
			return nil, fmt.Errorf("upload manifest init failed: %w", err)
		}
	}
	return session, nil
}
func (m *uploadSessionManager) get(uploadID string) (*uploadSession, bool) {
	m.mu.RLock()
	session, exists := m.sessions[uploadID]
	m.mu.RUnlock()
	if exists {
		return session, true
	}

	if session, ok := m.loadSessionFromRedis(uploadID); ok {
		m.mu.Lock()
		m.sessions[uploadID] = session
		m.mu.Unlock()
		return session, true
	}

	return nil, false
}
func (m *uploadSessionManager) stop() {
	m.once.Do(func() {
		close(m.stopCh)
	})
}

// ========== Nonce 缓存（防重放攻击） ==========
type nonceCache struct {
	items  map[string]time.Time
	mu     sync.RWMutex
	stopCh chan struct{}
	once   sync.Once
}

var nonceCacheInstance = &nonceCache{
	items:  make(map[string]time.Time),
	stopCh: make(chan struct{}),
}

func startNonceCache() {
	go nonceCacheInstance.cleanupLoop()
}
func (c *nonceCache) cleanupLoop() {
	ticker := time.NewTicker(nonceCacheTTL)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}
func (c *nonceCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for nonce, createdAt := range c.items {
		if now.Sub(createdAt) > nonceCacheTTL {
			delete(c.items, nonce)
		}
	}
}
func (c *nonceCache) stop() {
	c.once.Do(func() {
		close(c.stopCh)
	})
}

// ========== IP 限流器（带最后使用时间） ==========
type rateLimiterEntry struct {
	*rate.Limiter
	lastUsed time.Time
}
type ipRateLimiter struct {
	limiters map[string]*rateLimiterEntry
	mu       sync.RWMutex
	stopCh   chan struct{}
	once     sync.Once
}

var globalRateLimiter = &ipRateLimiter{
	limiters: make(map[string]*rateLimiterEntry),
	stopCh:   make(chan struct{}),
}

func startRateLimiterCleanup() {
	go globalRateLimiter.cleanupLoop()
}
func (l *ipRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rateLimiterCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.cleanup()
		}
	}
}
func (l *ipRateLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for ip, entry := range l.limiters {
		if now.Sub(entry.lastUsed) > 1*time.Hour {
			delete(l.limiters, ip)
		}
	}
}
func (l *ipRateLimiter) stop() {
	l.once.Do(func() {
		close(l.stopCh)
	})
}

// ========== 上传并发控制器 ==========
type uploadConcurrencyController struct {
	uploads map[string]int
	mu      sync.RWMutex
}

var concurrencyController = &uploadConcurrencyController{
	uploads: make(map[string]int),
}

func (c *uploadConcurrencyController) release(ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if count, exists := c.uploads[ip]; exists && count > 0 {
		c.uploads[ip]--
		if c.uploads[ip] == 0 {
			delete(c.uploads, ip)
		}
	}
}

// ========== 请求结构 ==========
type chunkUpload struct {
	UploadID      string `json:"uploadId"`
	FileName      string `json:"fileName"`
	FileType      string `json:"fileType"`
	FileID        string `json:"fileId"`
	ChunkIndex    int    `json:"chunkIndex"`
	TotalChunks   int    `json:"totalChunks"`
	Data          string `json:"data"`
	ChunkChecksum string `json:"chunkChecksum"`
	Timestamp     string `json:"timestamp"`
	Nonce         string `json:"nonce"`
	Signature     string `json:"signature"`
}
const uploadSessionRedisPrefix = "upload:session:"

// uploadSessionData 可序列化的会话元数据（分块文件仍存本地临时目录）。
type uploadSessionData struct {
	UploadID       string         `json:"uploadId"`
	DeviceID       string         `json:"deviceId"`
	FileName       string         `json:"fileName"`
	FileType       string         `json:"fileType"`
	FileID         string         `json:"fileId"`
	TotalChunks    int            `json:"totalChunks"`
	Received       map[int]bool   `json:"received"`
	ChunkChecksums map[int]string `json:"chunkChecksums"`
	LastActive     time.Time      `json:"lastActive"`
	TotalSize      int64          `json:"totalSize"`
	Completed      bool           `json:"completed"`
	Completing     bool           `json:"completing"`
	CompletedAt    time.Time      `json:"completedAt,omitempty"`
}

var uploadSessionRedis *redisPackage2.Client
var uploadChunkStore *services.UploadChunkStore
var uploadManifestStore *services.UploadManifestStore
var uploadCompleteLock *services.UploadCompleteLock

func purgeUploadArtifacts(uploadID string) {
	if uploadManifestStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = uploadManifestStore.Delete(ctx, uploadID)
		cancel()
	}
	if uploadChunkStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := uploadChunkStore.DeleteAll(ctx, uploadID); err != nil {
			logPackage2.Printf("[Upload] failed to delete chunks for %s: %v", uploadID, err)
		}
	}
	os.RemoveAll(filepath.Join(os.TempDir(), "uploads", uploadID))
}
func uploadSessionRedisKey(uploadID string) string {
	return uploadSessionRedisPrefix + uploadID
}
func (s *uploadSession) toData() *uploadSessionData {
	s.mu.RLock()
	defer s.mu.RUnlock()

	received := make(map[int]bool, len(s.Received))
	for k, v := range s.Received {
		received[k] = v
	}
	checksums := make(map[int]string, len(s.ChunkChecksums))
	for k, v := range s.ChunkChecksums {
		checksums[k] = v
	}

	return &uploadSessionData{
		UploadID:       s.UploadID,
		DeviceID:       s.DeviceID,
		FileName:       s.FileName,
		FileType:       s.FileType,
		FileID:         s.FileID,
		TotalChunks:    s.TotalChunks,
		Received:       received,
		ChunkChecksums: checksums,
		LastActive:     s.LastActive,
		TotalSize:      s.TotalSize,
		Completed:      s.Completed,
		Completing:     s.Completing,
		CompletedAt:    s.CompletedAt,
	}
}
func uploadSessionFromData(d *uploadSessionData) *uploadSession {
	if d == nil {
		return nil
	}
	received := d.Received
	if received == nil {
		received = make(map[int]bool)
	}
	checksums := d.ChunkChecksums
	if checksums == nil {
		checksums = make(map[int]string)
	}
	return &uploadSession{
		UploadID:       d.UploadID,
		DeviceID:       d.DeviceID,
		FileName:       d.FileName,
		FileType:       d.FileType,
		FileID:         d.FileID,
		TotalChunks:    d.TotalChunks,
		Received:       received,
		ChunkChecksums: checksums,
		LastActive:     d.LastActive,
		TotalSize:      d.TotalSize,
		Completed:      d.Completed,
		Completing:     d.Completing,
		CompletedAt:    d.CompletedAt,
	}
}
func (m *uploadSessionManager) persistSession(session *uploadSession) {
	if uploadSessionRedis == nil || session == nil {
		return
	}

	data := session.toData()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	key := uploadSessionRedisKey(session.UploadID)
	if existingRaw, err := uploadSessionRedis.Get(ctx, key).Bytes(); err == nil {
		var existing uploadSessionData
		if json.Unmarshal(existingRaw, &existing) == nil {
			if existing.DeviceID != "" && data.DeviceID != "" && !strings.EqualFold(existing.DeviceID, data.DeviceID) {
				logPackage2.Printf("[UploadSession] Refusing to overwrite session %s device binding", session.UploadID)
				return
			}
			if existing.Received != nil {
				if data.Received == nil {
					data.Received = make(map[int]bool, len(existing.Received))
				}
				for idx, ok := range existing.Received {
					if ok && !data.Received[idx] {
						data.Received[idx] = true
					}
				}
			}
			if existing.ChunkChecksums != nil {
				if data.ChunkChecksums == nil {
					data.ChunkChecksums = make(map[int]string, len(existing.ChunkChecksums))
				}
				for idx, sum := range existing.ChunkChecksums {
					if _, ok := data.ChunkChecksums[idx]; !ok {
						data.ChunkChecksums[idx] = sum
					}
				}
			}
		}
	}

	payload, err := json.Marshal(data)
	if err != nil {
		logPackage2.Printf("[UploadSession] Failed to marshal session %s: %v", session.UploadID, err)
		return
	}

	if err := uploadSessionRedis.Set(ctx, key, payload, sessionTTL).Err(); err != nil {
		logPackage2.Printf("[UploadSession] Failed to persist session %s to Redis: %v", session.UploadID, err)
	}
}
func (m *uploadSessionManager) loadSessionFromRedis(uploadID string) (*uploadSession, bool) {
	if uploadSessionRedis == nil {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	payload, err := uploadSessionRedis.Get(ctx, uploadSessionRedisKey(uploadID)).Bytes()
	if err != nil {
		return nil, false
	}

	var data uploadSessionData
	if err := json.Unmarshal(payload, &data); err != nil {
		logPackage2.Printf("[UploadSession] Failed to unmarshal session %s: %v", uploadID, err)
		return nil, false
	}

	return uploadSessionFromData(&data), true
}
func (m *uploadSessionManager) removeSessionFromRedis(uploadID string) {
	if uploadSessionRedis == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := uploadSessionRedis.Del(ctx, uploadSessionRedisKey(uploadID)).Err(); err != nil {
		logPackage2.Printf("[UploadSession] Failed to delete session %s from Redis: %v", uploadID, err)
	}
}

func revokeUserSessions(ctx context.Context, userID uuid.UUID, reason string) error {
	svc := middleware.GetSessionService()
	if svc == nil {
		return fmt.Errorf("session service unavailable")
	}
	return svc.RevokeSubjectRefreshTokens(ctx, services.SessionSubjectUser, userID, reason)
}

// ---- Source: backend/handlers/user_notification.go ----
const (
	NotificationTypeSalaryPaid       = "salary_paid"
	NotificationTypeAnnouncement     = "announcement"
	NotificationTypeAttendanceReview = "attendance_review_pending"
	virtualAttendanceReviewNotifyID  = "virtual:attendance_review"
)

