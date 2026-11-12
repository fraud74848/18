// Consolidated source; original file boundaries are marked below.
package models

import (
	rand "crypto/rand"
	hex "encoding/hex"
	json "encoding/json"
	gin "github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
	uuid "github.com/google/uuid"
	log "log"
	math "math"
	os "os"
	strings "strings"
	sync "sync"
	time "time"
)

// ---- Source: backend/models/default_roles.go ----
const (
	PermInviteView   = "invite:view"
	PermInviteManage = "invite:manage"

	PermCallbackView     = "callback:view"
	PermCallbackManage   = "callback:manage"
	PermCallbackSettings = "callback:settings"

	PermCsToolView     = "cstool:view"
	PermCsToolSettings = "cstool:settings"

	PermDomainMonitorView     = "domain_monitor:view"
	PermDomainMonitorManage   = "domain_monitor:manage"
	PermDomainMonitorSettings = "domain_monitor:settings"

	PermProfileView     = "profile:view"
	PermProfileEdit     = "profile:edit"
	PermProfilePassword = "profile:password"
	PermProfileTotp     = "profile:totp"
	PermProfileInvite   = "profile:invite"

	PermEmployeePortalView        = "employee:portal:view"
	PermEmployeeAttendanceViewOwn = "employee:attendance:view_own"
	PermEmployeeRecordingViewOwn  = "employee:recording:view_own"
	PermEmployeeDeviceViewOwn     = "employee:device:view_own"
	PermEmployeeInviteViewOwn     = "employee:invite:view_own"
	PermEmployeeProfileEdit       = "employee:profile:edit"
	PermEmployeeClientDownload    = "employee:client:download"

	PermAgentNotificationView = "agent_notification:view" // 查看客户端通知配置
	PermAgentNotificationEdit = "agent_notification:edit" // 编辑客户端通知配置
)

// DefaultEmployeePortalPermissions 员工自助门户（Agent 登录）
var DefaultEmployeePortalPermissions = []string{
	PermEmployeePortalView,
	PermEmployeeAttendanceViewOwn,
	PermEmployeeRecordingViewOwn,
	PermEmployeeDeviceViewOwn,
	PermEmployeeInviteViewOwn,
	PermEmployeeProfileEdit,
	PermEmployeeClientDownload,
}

// AllAdminPermissions 管理员拥有全部后台权限
func AllAdminPermissions() []string {
	return []string{
		PermUserView, PermUserCreate, PermUserUpdate, PermUserDelete, PermUserManage,
		PermUserResetSalaryPassword,
		PermRoleView, PermRoleCreate, PermRoleUpdate, PermRoleDelete,
		PermDeviceView, PermDeviceRemote, PermDeviceManage, PermDeviceBind, PermDeviceUnbind,
		PermRecordingView, PermRecordingPlay, PermRecordingExport, PermRecordingDelete, PermRecordingUpload,
		PermScreenshotView, PermScreenshotDelete,
		PermActivityView, PermActivityExport, PermActivityDelete,
		PermPolicyView, PermPolicyCreate, PermPolicyUpdate, PermPolicyDelete,
		PermAuditView,
		PermAttendanceView, PermAttendanceEdit,
		PermAttendanceReviewView, PermAttendanceReviewEdit,
		PermTelegramView, PermTelegramEdit,
		PermAgentNotificationView, PermAgentNotificationEdit,
		PermInviteView, PermInviteManage,
		PermSiteView, PermSiteManage,
		PermStatsView, PermStatsEdit,
		PermSalaryView, PermSalaryViewOthers, PermSalaryEdit, PermSalaryUpload, PermSalaryDelete, PermSalaryPaymentAddressView,
		PermOperationLogView, PermOperationLogExport, PermOperationLogDelete,
		PermDiagnosticView, PermDiagnosticManage, PermDiagnosticExport,
		PermCallbackView, PermCallbackManage, PermCallbackSettings,
		PermCsToolView, PermCsToolSettings,
		PermDomainMonitorView, PermDomainMonitorManage, PermDomainMonitorSettings,
		PermProfileView, PermProfileEdit, PermProfilePassword, PermProfileTotp, PermProfileInvite,
	}
}

// DefaultViewerPermissions 查看员 — 子账号默认角色，仅查看：
// 打卡记录、后台打卡、考勤月历、罚款详情（修改自己的登录密码不依赖角色权限）。
var DefaultViewerPermissions = []string{
	"attendance:view", // 考勤月历
	"records:view",    // 打卡记录
	"punch:view",      // 后台打卡
	"penalty:view",    // 罚款详情
}

// DefaultRoleSeed 启动时自动创建/同步的默认角色
type DefaultRoleSeed struct {
	Name        string
	Description string
	Permissions []string
	ExactSync   bool // true=每次启动对齐权限列表；false=仅合并缺失权限
}

var DefaultRoleSeeds = []DefaultRoleSeed{
	{
		Name:        "管理员",
		Description: "拥有所有权限",
		Permissions: AllAdminPermissions(),
		ExactSync:   false,
	},
	{
		Name:        "查看员",
		Description: "只读：打卡记录、后台打卡、考勤月历、罚款详情",
		Permissions: DefaultViewerPermissions,
		ExactSync:   true,
	},
}

// ---- Source: backend/models/diagnostic.go ----
const (
	PermDiagnosticView   = "diagnostic:view"
	PermDiagnosticManage = "diagnostic:manage"
	PermDiagnosticExport = "diagnostic:export"
)

type DiagnosticLevel string

const (
	DiagnosticLevelOff   DiagnosticLevel = "OFF"
	DiagnosticLevelError DiagnosticLevel = "ERROR"
	DiagnosticLevelWarn  DiagnosticLevel = "WARN"
	DiagnosticLevelInfo  DiagnosticLevel = "INFO"
	DiagnosticLevelDebug DiagnosticLevel = "DEBUG"
	DiagnosticLevelTrace DiagnosticLevel = "TRACE"
)

var DiagnosticModules = []string{
	"RemoteView",
	"Recording",
	"Upload",
	"WebSocket",
	"LiveKit",
	"FFmpeg",
	"Policy",
	"Capture",
	"Lifecycle",
	"Health",
	"Backend",
}

type DiagnosticCenterConfig struct {
	Enabled               bool                       `json:"enabled"`
	ExpiresAt             *time.Time                 `json:"expiresAt,omitempty"`
	GlobalLevel           DiagnosticLevel            `json:"globalLevel"`
	ModuleLevels          map[string]DiagnosticLevel `json:"moduleLevels"`
	AgentUploadEnabled    bool                       `json:"agentUploadEnabled"`
	FrontendUploadEnabled bool                       `json:"frontendUploadEnabled"`
	RetentionHours        int                        `json:"retentionHours"`
	AppliedTemplateID     string                     `json:"appliedTemplateId,omitempty"`
	UpdatedAt             time.Time                  `json:"updatedAt"`
	UpdatedBy             string                     `json:"updatedBy,omitempty"`
}
type DiagnosticRuntimeConfig struct {
	Active                bool                       `json:"active"`
	ExpiresAt             *time.Time                 `json:"expiresAt,omitempty"`
	GlobalLevel           DiagnosticLevel            `json:"globalLevel"`
	ModuleLevels          map[string]DiagnosticLevel `json:"moduleLevels"`
	AgentUploadEnabled    bool                       `json:"agentUploadEnabled"`
	FrontendUploadEnabled bool                       `json:"frontendUploadEnabled"`
}
type DiagnosticEvent struct {
	ID            uuid.UUID       `json:"id"`
	Source        string          `json:"source"`
	DeviceID      *uuid.UUID      `json:"deviceId,omitempty"`
	Module        string          `json:"module"`
	Level         DiagnosticLevel `json:"level"`
	Message       string          `json:"message"`
	Context       json.RawMessage `json:"context,omitempty"`
	CorrelationID string          `json:"correlationId,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
}

func NormalizeDiagnosticLevel(raw string) DiagnosticLevel {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case string(DiagnosticLevelError):
		return DiagnosticLevelError
	case string(DiagnosticLevelWarn):
		return DiagnosticLevelWarn
	case string(DiagnosticLevelInfo):
		return DiagnosticLevelInfo
	case string(DiagnosticLevelDebug):
		return DiagnosticLevelDebug
	case string(DiagnosticLevelTrace):
		return DiagnosticLevelTrace
	default:
		return DiagnosticLevelOff
	}
}
func (c DiagnosticCenterConfig) Effective(now time.Time) DiagnosticRuntimeConfig {
	runtime := DiagnosticRuntimeConfig{
		Active:                false,
		GlobalLevel:           DiagnosticLevelOff,
		ModuleLevels:          map[string]DiagnosticLevel{},
		AgentUploadEnabled:    c.AgentUploadEnabled,
		FrontendUploadEnabled: c.FrontendUploadEnabled,
	}
	if !c.Enabled {
		return runtime
	}
	if c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
		return runtime
	}
	runtime.Active = true
	runtime.ExpiresAt = c.ExpiresAt
	runtime.GlobalLevel = NormalizeDiagnosticLevel(string(c.GlobalLevel))
	for _, module := range DiagnosticModules {
		level := runtime.GlobalLevel
		if c.ModuleLevels != nil {
			if moduleLevel, ok := c.ModuleLevels[module]; ok {
				level = NormalizeDiagnosticLevel(string(moduleLevel))
			}
		}
		runtime.ModuleLevels[module] = level
	}
	return runtime
}
// ---- Source: backend/models/diagnostic_extended.go ----
type DiagnosticSnapshot struct {
	GeneratedAt time.Time                `json:"generatedAt"`
	Runtime     DiagnosticRuntimeConfig  `json:"runtime"`
	WebSocket   DiagnosticWSSnapshot     `json:"websocket"`
	Events      DiagnosticEventStats     `json:"events"`
	RemoteView  DiagnosticRVSnapshot     `json:"remoteView"`
	Agents      []DiagnosticAgentSummary `json:"agents"`
	Health      DiagnosticHealthScore    `json:"health"`
	StatsToday  DiagnosticDailyStats     `json:"statsToday"`
}
type DiagnosticHealthScore struct {
	Score  int                     `json:"score"`
	Layers []DiagnosticHealthLayer `json:"layers"`
}
type DiagnosticHealthLayer struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}
type DiagnosticDailyStats struct {
	RemoteViewStarts   int     `json:"remoteViewStarts"`
	RemoteViewSuccess  int     `json:"remoteViewSuccess"`
	RemoteViewFailures int     `json:"remoteViewFailures"`
	SuccessRate        float64 `json:"successRate"`
}
type DiagnosticWSSnapshot struct {
	AgentsOnline     int `json:"agentsOnline"`
	AdminConnections int `json:"adminConnections"`
	TotalConnections int `json:"totalConnections"`
}
type DiagnosticEventStats struct {
	TotalStored int            `json:"totalStored"`
	Last5Min    map[string]int `json:"last5Min"`
	Last1Hour   map[string]int `json:"last1Hour"`
	ByModule5m  map[string]int `json:"byModule5m"`
}
type DiagnosticRVSnapshot struct {
	ActiveSessions  int                              `json:"activeSessions"`
	PublishingFresh int                              `json:"publishingFresh"`
	Sessions        []DiagnosticRemoteSessionSummary `json:"sessions"`
}
type DiagnosticRemoteSessionSummary struct {
	SessionID   string                  `json:"sessionId"`
	DeviceID    string                  `json:"deviceId"`
	Hostname    string                  `json:"hostname,omitempty"`
	Status      string                  `json:"status"`
	ViewerCount int                     `json:"viewerCount"`
	AgentOnline bool                    `json:"agentOnline"`
	StatsFresh  bool                    `json:"statsFresh"`
	IngressID   string                  `json:"ingressId,omitempty"`
	StartedAt   string                  `json:"startedAt,omitempty"`
	Stats       *RemoteViewStatsPayload `json:"stats,omitempty"`
}
type DiagnosticAgentSummary struct {
	DeviceID      string `json:"deviceId"`
	Hostname      string `json:"hostname,omitempty"`
	Online        bool   `json:"online"`
	StatsFresh    bool   `json:"statsFresh"`
	Publishing    bool   `json:"publishing"`
	SessionID     string `json:"sessionId,omitempty"`
	SessionStatus string `json:"sessionStatus,omitempty"`
}
type DiagnosticPipeline struct {
	DeviceID      string                   `json:"deviceId"`
	Hostname      string                   `json:"hostname,omitempty"`
	SessionID     string                   `json:"sessionId,omitempty"`
	GeneratedAt   time.Time                `json:"generatedAt"`
	OverallStatus string                   `json:"overallStatus"`
	Nodes         []DiagnosticPipelineNode `json:"nodes"`
	Edges         []DiagnosticPipelineEdge `json:"edges"`
}
type DiagnosticPipelineNode struct {
	ID           string                 `json:"id"`
	Label        string                 `json:"label"`
	Layer        string                 `json:"layer"`
	Status       string                 `json:"status"`
	Detail       string                 `json:"detail"`
	Metrics      map[string]interface{} `json:"metrics,omitempty"`
	RecentLogs   []DiagnosticEventBrief `json:"recentLogs,omitempty"`
	RecentErrors []DiagnosticEventBrief `json:"recentErrors,omitempty"`
}
type DiagnosticEventBrief struct {
	CreatedAt string `json:"createdAt"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Module    string `json:"module,omitempty"`
}
type DiagnosticPipelineEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type DiagnosticFinding struct {
	Severity        string   `json:"severity"`
	Code            string   `json:"code"`
	Title           string   `json:"title"`
	Detail          string   `json:"detail"`
	Module          string   `json:"module,omitempty"`
	DeviceID        string   `json:"deviceId,omitempty"`
	Evidence        []string `json:"evidence,omitempty"`
	SuggestedAction string   `json:"suggestedAction,omitempty"`
}
type DiagnosticAnalysis struct {
	GeneratedAt   time.Time             `json:"generatedAt"`
	WindowMinutes int                   `json:"windowMinutes"`
	Summary       string                `json:"summary"`
	Findings      []DiagnosticFinding   `json:"findings"`
	Counts        map[string]int        `json:"counts"`
	Conclusion    *DiagnosticConclusion `json:"conclusion,omitempty"`
}
type DiagnosticConclusion struct {
	Title         string                      `json:"title"`
	ProbableCause string                      `json:"probableCause"`
	Confidence    int                         `json:"confidence"`
	Checks        []DiagnosticConclusionCheck `json:"checks"`
}
type DiagnosticConclusionCheck struct {
	Label string `json:"label"`
	OK    bool   `json:"ok"`
}
type DiagnosticReproduceStatus struct {
	JobID        string    `json:"jobId"`
	Active       bool      `json:"active"`
	TemplateID   string    `json:"templateId"`
	DeviceIDs    []string  `json:"deviceIds"`
	StartedAt    time.Time `json:"startedAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	AutoCapture  bool      `json:"autoCapture"`
	CaptureReady bool      `json:"captureReady"`
	CaptureID    string    `json:"captureId,omitempty"`
	Message      string    `json:"message,omitempty"`
}
type DiagnosticCaptureRequest struct {
	DeviceIDs       []string `json:"deviceIds"`
	WindowMinutes   int      `json:"windowMinutes"`
	IncludeAgents   bool     `json:"includeAgents"`
	IncludeAnalysis bool     `json:"includeAnalysis"`
}
type DiagnosticCaptureResult struct {
	CaptureID   string                 `json:"captureId"`
	GeneratedAt time.Time              `json:"generatedAt"`
	Snapshot    DiagnosticSnapshot     `json:"snapshot"`
	Analysis    *DiagnosticAnalysis    `json:"analysis,omitempty"`
	Pipelines   []DiagnosticPipeline   `json:"pipelines,omitempty"`
	AgentSnaps  map[string]interface{} `json:"agentSnapshots,omitempty"`
	EventCount  int                    `json:"eventCount"`
}

// ---- Source: backend/models/diagnostic_template.go ----
// DiagnosticTemplate 诊断模板：集中定义场景所需模块级别，新增模块时只需改此处。
type DiagnosticTemplate struct {
	ID                     string                     `json:"id"`
	Name                   string                     `json:"name"`
	Description            string                     `json:"description"`
	GlobalLevel            DiagnosticLevel            `json:"globalLevel"`
	ModuleLevels           map[string]DiagnosticLevel `json:"moduleLevels"`
	DefaultDurationMinutes int                        `json:"defaultDurationMinutes"`
}

var diagnosticTemplateOrder = []string{
	"remote-view",
	"login",
	"upload",
	"recording",
	"full",
}
var diagnosticTemplates = map[string]DiagnosticTemplate{
	"remote-view": {
		ID:          "remote-view",
		Name:        "RemoteView",
		Description: "远程桌面推流、编码、媒体与链路生命周期",
		GlobalLevel: DiagnosticLevelOff,
		ModuleLevels: map[string]DiagnosticLevel{
			"RemoteView": DiagnosticLevelInfo,
			"FFmpeg":     DiagnosticLevelInfo,
			"LiveKit":    DiagnosticLevelInfo,
			"WebSocket":  DiagnosticLevelInfo,
			"Capture":    DiagnosticLevelInfo,
			"Lifecycle":  DiagnosticLevelInfo,
			"Health":     DiagnosticLevelWarn,
			"Backend":    DiagnosticLevelWarn,
		},
		DefaultDurationMinutes: 60,
	},
	"login": {
		ID:          "login",
		Name:        "登录异常",
		Description: "Agent 注册、WebSocket 连接与鉴权问题",
		GlobalLevel: DiagnosticLevelOff,
		ModuleLevels: map[string]DiagnosticLevel{
			"WebSocket": DiagnosticLevelInfo,
			"Lifecycle": DiagnosticLevelInfo,
			"Health":    DiagnosticLevelInfo,
			"Backend":   DiagnosticLevelInfo,
			"Policy":    DiagnosticLevelWarn,
		},
		DefaultDurationMinutes: 30,
	},
	"upload": {
		ID:          "upload",
		Name:        "上传异常",
		Description: "录制/截图上传队列与网络传输",
		GlobalLevel: DiagnosticLevelOff,
		ModuleLevels: map[string]DiagnosticLevel{
			"Upload":    DiagnosticLevelInfo,
			"Recording": DiagnosticLevelInfo,
			"WebSocket": DiagnosticLevelInfo,
			"Health":    DiagnosticLevelWarn,
			"Backend":   DiagnosticLevelWarn,
		},
		DefaultDurationMinutes: 60,
	},
	"recording": {
		ID:          "recording",
		Name:        "录屏异常",
		Description: "本地录屏、采集与编码链路",
		GlobalLevel: DiagnosticLevelOff,
		ModuleLevels: map[string]DiagnosticLevel{
			"Recording": DiagnosticLevelInfo,
			"Capture":   DiagnosticLevelInfo,
			"FFmpeg":    DiagnosticLevelInfo,
			"Lifecycle": DiagnosticLevelInfo,
			"Health":    DiagnosticLevelWarn,
		},
		DefaultDurationMinutes: 60,
	},
	"full": {
		ID:          "full",
		Name:        "全部诊断",
		Description: "所有模块开启 DEBUG，仅短时排查使用",
		GlobalLevel: DiagnosticLevelDebug,
		ModuleLevels: map[string]DiagnosticLevel{
			"RemoteView": DiagnosticLevelDebug,
			"Recording":  DiagnosticLevelDebug,
			"Upload":     DiagnosticLevelDebug,
			"WebSocket":  DiagnosticLevelDebug,
			"LiveKit":    DiagnosticLevelDebug,
			"FFmpeg":     DiagnosticLevelDebug,
			"Policy":     DiagnosticLevelInfo,
			"Capture":    DiagnosticLevelDebug,
			"Lifecycle":  DiagnosticLevelDebug,
			"Health":     DiagnosticLevelInfo,
			"Backend":    DiagnosticLevelInfo,
		},
		DefaultDurationMinutes: 30,
	},
}

// ---- Source: backend/models/models.go ----
// APIResponse API 统一响应结构
type APIResponse struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp string      `json:"timestamp"`
	RequestID string      `json:"requestId"`
}

// User 用户模型
type User struct {
	ID           uuid.UUID  `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Email        string     `json:"email"`
	RealName     string     `json:"realName"`
	DepartmentID *uuid.UUID `json:"departmentId,omitempty"`
	Status       int        `json:"status"` // 1:启用, 0:禁用
	TotpEnabled  bool       `json:"totpEnabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLogin    *time.Time `json:"lastLogin,omitempty"`
	Roles        []Role     `json:"roles,omitempty"`
}

// Role 角色模型
type Role struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Device 设备模型
type Device struct {
	ID                  uuid.UUID  `json:"id"`
	EmployeeID          string     `json:"employeeId"`   // 员工ID（系统内部）
	EmployeeName        string     `json:"employeeName"` // 员工姓名
	EmployeeNumber      string     `json:"employeeNumber"`
	Position            string     `json:"position"`
	DeviceFingerprint   string     `json:"deviceFingerprint"`
	Hostname            string     `json:"hostname"`
	OSVersion           string     `json:"osVersion"`
	AgentVersion        string     `json:"agentVersion"`
	Status              string     `json:"status"` // online, offline, pending
	OnlineStatus        string     `json:"onlineStatus" gorm:"-"`
	LastSeenAt          *time.Time `json:"lastSeenAt"`
	BoundAt             *time.Time `json:"boundAt"` // ✅ 添加：绑定时间
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           *time.Time `json:"updatedAt,omitempty"` // ✅ 添加：更新时间
	DeviceSecret        string     `json:"-"`
	ActiveInviteCode    string     `json:"activeInviteCode,omitempty"`
	EmployeeUUID        string     `json:"employeeUuid,omitempty"`
	IsActiveSession     bool       `json:"isActiveSession"`
	IsSharedWorkstation bool       `json:"isSharedWorkstation"`
	SharedWithNames     string     `json:"sharedWithNames,omitempty"`
	// ForceOfflined: admin forced the device offline; blocks token refresh/register until cleared.
	ForceOfflined bool `json:"forceOfflined"`
}

// Policy 策略模型
type Policy struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Content   string    `json:"content"`
	Signature string    `json:"signature"`
	Status    int       `json:"status"` // 1:已发布, 0:草稿
	IsCurrent bool      `json:"isCurrent"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Recording 录屏记录模型
type Recording struct {
	ID        uuid.UUID  `json:"id"`
	DeviceID  uuid.UUID  `json:"deviceId"`
	StartTime time.Time  `json:"startTime"`
	EndTime   time.Time  `json:"endTime"`
	FilePath  string     `json:"filePath"`
	FileSize  int64      `json:"fileSize"`
	SHA256    string     `json:"sha256"`
	Duration  int        `json:"duration"` // ✅ 添加：录制时长（秒）
	Uploaded  bool       `json:"uploaded"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"` // ✅ 添加：更新时间
}

// ActivityLog 活动日志模型
type ActivityLog struct {
	ID              string     `json:"id"`
	DeviceID        string     `json:"deviceId"`
	ProcessName     string     `json:"processName"`
	WindowTitle     string     `json:"windowTitle"`
	BrowserTitle    string     `json:"browserTitle"`
	StartedAt       time.Time  `json:"startedAt"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	IsIdle          bool       `json:"isIdle"`
	DurationSeconds int        `json:"durationSeconds"`
	CreatedAt       time.Time  `json:"createdAt"`
	Reported        bool       `json:"reported"`               // ✅ 添加：是否已报告到后端
	ReportedAt      *time.Time `json:"reportedAt,omitempty"`   // ✅ 添加：报告时间
	RetryCount      int        `json:"retryCount,omitempty"`   // ✅ 添加：重试次数
	ErrorMessage    string     `json:"errorMessage,omitempty"` // ✅ 添加：错误信息
}

const (
	// 用户管理权限
	PermUserView                    = "user:view"
	PermUserCreate                  = "user:create"
	PermUserUpdate                  = "user:update"
	PermUserDelete                  = "user:delete"
	PermUserManage                  = "user:manage"
	PermUserResetSalaryPassword     = "user:reset_salary_view_password"
	PermUserResetScreenLockPassword = "user:reset_screen_lock_password"

	// 角色管理权限
	PermRoleView   = "role:view"
	PermRoleCreate = "role:create"
	PermRoleUpdate = "role:update"
	PermRoleDelete = "role:delete"

	// 设备管理权限
	PermDeviceView   = "device:view"
	PermDeviceRemote = "device:remote"
	PermDeviceManage = "device:manage"
	PermDeviceBind   = "device:bind"
	PermDeviceUnbind = "device:unbind"

	// 录屏管理权限
	PermRecordingView   = "recording:view"
	PermRecordingPlay   = "recording:play"
	PermRecordingExport = "recording:export"
	PermRecordingDelete = "recording:delete"
	PermRecordingUpload = "recording:upload"

	// 截图管理权限
	PermScreenshotView   = "screenshot:view"
	PermScreenshotDelete = "screenshot:delete"

	// 活动日志权限
	PermActivityView   = "activity:view"
	PermActivityExport = "activity:export"
	PermActivityDelete = "activity:delete"

	// 策略管理权限
	PermPolicyView   = "policy:view"
	PermPolicyCreate = "policy:create"
	PermPolicyUpdate = "policy:update"
	PermPolicyDelete = "policy:delete"

	// 审计权限
	PermAuditView = "audit:view"

	// ========== 出款管理权限（新增） ==========
	PermAttendanceView       = "attendance:view"        // 查看考勤绩效
	PermAttendanceEdit       = "attendance:edit"        // 编辑考勤绩效
	PermAttendanceReviewView = "attendance_review:view" // 查看考勤待确认
	PermAttendanceReviewEdit = "attendance_review:edit" // 处理考勤待确认 / 改阈值
	PermTelegramView         = "telegram:view"          // 查看 Telegram 考勤配置与记录
	PermTelegramEdit         = "telegram:edit"          // 编辑 Telegram 考勤配置
	PermSiteView             = "site:view"              // 查看出款站点
	PermSiteManage           = "site:manage"            // 管理出款站点
	PermStatsView            = "stats:view"             // 查看出款统计
	PermStatsEdit            = "stats:edit"             // 编辑出款统计（上传数据）

	// ========== 工资管理权限（新增） ==========
	PermSalaryUpload             = "salary:upload"               // 上传工资表
	PermSalaryView               = "salary:view"                 // 查看工资（默认仅本人）
	PermSalaryViewOthers         = "salary:view_others"          // 工资查看：可下拉选择其他员工
	PermSalaryEdit               = "salary:edit"                 // 编辑工资
	PermSalaryDelete             = "salary:delete"               // 删除工资
	PermSalaryPaymentAddressView = "salary:payment_address:view" // 查看员工收款地址（管理员）
)

// OperationLog 操作日志模型（只记录修改操作）
type OperationLog struct {
	ID              string    `json:"id"`
	OperatorID      string    `json:"operatorId"`
	OperatorName    string    `json:"operatorName"`
	OperatorRole    string    `json:"operatorRole"`
	OperationType   string    `json:"operationType"`   // CREATE, UPDATE, DELETE, UPLOAD
	OperationModule string    `json:"operationModule"` // USER, ROLE, DEVICE, SALARY, ATTENDANCE, PERFORMANCE, PENALTY, SITE_STATS
	OperationDesc   string    `json:"operationDesc"`   // 详细描述，如：修改员工阿富的工资: 底薪18000→18500
	TargetID        string    `json:"targetId"`
	TargetName      string    `json:"targetName"`
	IPAddress       string    `json:"ipAddress"`
	UserAgent       string    `json:"userAgent"`
	RequestMethod   string    `json:"requestMethod"`
	RequestPath     string    `json:"requestPath"`
	OldValue        string    `json:"oldValue"` // 修改前的值（JSON格式）
	NewValue        string    `json:"newValue"`
	ExecutionTimeMs int       `json:"executionTimeMs"` // 修改后的值（JSON格式）
	CreatedAt       time.Time `json:"createdAt"`
}

// 操作类型常量
const (
	OperationTypeCreate = "CREATE" // 创建
	OperationTypeUpdate = "UPDATE" // 修改
	OperationTypeDelete = "DELETE" // 删除
	OperationTypeUpload = "UPLOAD" // 上传
)

// 操作模块常量
const (
	OperationModuleUser        = "USER"
	OperationModuleRole        = "ROLE"
	OperationModuleDevice      = "DEVICE"
	OperationModuleSalary      = "SALARY"
	OperationModuleAttendance  = "ATTENDANCE"
	OperationModulePerformance = "PERFORMANCE"
	OperationModulePenalty     = "PENALTY"
	OperationModuleSiteStats   = "SITE_STATS"
	OperationModulePolicy      = "POLICY"
	OperationModuleMedia       = "MEDIA"
	OperationModuleDiagnostic  = "DIAGNOSTIC"
)

// 添加权限常量
const (
	// 操作日志权限
	PermOperationLogView   = "operation_log:view"
	PermOperationLogExport = "operation_log:export"
	PermOperationLogDelete = "operation_log:delete"
)

// DefaultRoles 默认角色定义
var DefaultRoles = []Role{
	{
		Name:        "管理员",
		Description: "拥有所有权限",
		Permissions: []string{
			PermUserView, PermUserCreate, PermUserUpdate, PermUserDelete, PermUserManage,
			PermUserResetSalaryPassword, PermUserResetScreenLockPassword,
			PermRoleView, PermRoleCreate, PermRoleUpdate, PermRoleDelete,
			PermDeviceView, PermDeviceManage, PermDeviceBind, PermDeviceUnbind,
			PermRecordingView, PermRecordingPlay, PermRecordingExport, PermRecordingDelete, PermRecordingUpload,
			PermScreenshotView, PermScreenshotDelete,
			PermActivityView, PermActivityExport, PermActivityDelete,
			PermPolicyView, PermPolicyCreate, PermPolicyUpdate, PermPolicyDelete,
			PermAuditView,
			PermSalaryUpload, PermSalaryView, PermSalaryViewOthers, PermSalaryEdit, PermSalaryDelete, PermSalaryPaymentAddressView,
			PermOperationLogView, PermOperationLogExport, PermOperationLogDelete,
			PermDiagnosticView, PermDiagnosticManage, PermDiagnosticExport,
			PermAttendanceView, PermAttendanceEdit,
			PermAttendanceReviewView, PermAttendanceReviewEdit,
			PermTelegramView, PermTelegramEdit,
			PermAgentNotificationView, PermAgentNotificationEdit,
			PermSiteView, PermSiteManage,
			PermStatsView, PermStatsEdit,
		},
	},
}

// UTCNow 获取当前 UTC 时间
func UTCNow() time.Time {
	return time.Now().UTC()
}

// FormatUTC 格式化 UTC 时间为 RFC3339
func FormatUTC(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

var (
	jwtSecretValue []byte
	secretsOnce    sync.Once
)

// initSecrets 初始化内存中的密钥值（延迟加载）
func initSecrets() {
	jwtEnv := os.Getenv("JWT_SECRET")
	if strings.TrimSpace(jwtEnv) == "" {
		// ✅ 生产环境必须设置，开发环境给警告但使用随机值
		log.Println("⚠️ WARNING: JWT_SECRET not set, using random value (insecure for production)")
		jwtEnv = generateRandomSecret(32)
	}
	jwtSecretValue = []byte(jwtEnv)
}

// 添加辅助函数
func generateRandomSecret(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "fallback-secret-do-not-use"
	}
	return hex.EncodeToString(bytes)[:length]
}

// JWTSecret 获取 JWT 密钥
func JWTSecret() []byte {
	secretsOnce.Do(initSecrets)
	return jwtSecretValue
}

// Send 发送统一格式的响应
func Send(c *gin.Context, statusCode int, code int, message string, data interface{}) {
	requestID := c.GetString("requestID")
	c.JSON(statusCode, APIResponse{
		Code:      code,
		Message:   message,
		Data:      data,
		Timestamp: FormatUTC(UTCNow()),
		RequestID: requestID,
	})
}

// SendError 发送错误响应
func SendError(c *gin.Context, statusCode int, code int, message string) {
	log.Printf("[ERROR] SendError: status=%d, code=%d, message=%s, path=%s",
		statusCode, code, message, c.Request.URL.Path)

	Send(c, statusCode, code, message, nil)
}

// SendInternalError 记录内部错误并返回通用 500 消息（避免泄露实现细节）
func SendInternalError(c *gin.Context, err error) {
	if err != nil {
		log.Printf("[INTERNAL] path=%s err=%v", c.Request.URL.Path, err)
	}
	SendError(c, 500, 5000, "Internal server error")
}

// SessionJWTClaims JWT 仅携带 session_id（sid）
type SessionJWTClaims struct {
	SID string `json:"sid"`
	jwt.RegisteredClaims
}

const (
	RemoteViewStatusStarting = "starting"
	RemoteViewStatusActive   = "active"
	RemoteViewStatusStopping = "stopping"
	RemoteViewStatusStopped  = "stopped"
	RemoteViewStatusFailed   = "failed"
)

type RemoteViewSession struct {
	ID                uuid.UUID  `json:"id"`
	DeviceID          uuid.UUID  `json:"deviceId"`
	RoomName          string     `json:"roomName"`
	Status            string     `json:"status"`
	StartedBy         uuid.UUID  `json:"startedBy"`
	StartedByName     string     `json:"startedByName"`
	ViewerCount       int        `json:"viewerCount"`
	PublisherIdentity string     `json:"publisherIdentity"`
	LiveKitURL        string     `json:"livekitUrl"`
	IngressID         string     `json:"ingressId,omitempty"`
	RTMPURL           string     `json:"rtmpUrl,omitempty"`
	StartedAt         time.Time  `json:"startedAt"`
	EndedAt           *time.Time `json:"endedAt,omitempty"`
	EndReason         string     `json:"endReason,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}
type RemoteViewStatusResponse struct {
	IsActive    bool   `json:"isActive"`
	SessionID   string `json:"sessionId,omitempty"`
	Status      string `json:"status,omitempty"`
	ViewerCount int    `json:"viewerCount"`
	StartedAt   string `json:"startedAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	// StartingTimeoutSec mirrors backend StartingTimeout for FE warmup alignment.
	StartingTimeoutSec int                     `json:"startingTimeoutSec,omitempty"`
	Stats              *RemoteViewStatsPayload `json:"stats,omitempty"`
	// StatsFresh is true when agent stats were received within the publisher grace window.
	StatsFresh bool `json:"statsFresh"`
	// SfuChecked is true when LiveKit (Ingress/room) was queried for this status call.
	SfuChecked bool `json:"sfuChecked"`
	// SfuPublishing is true when SFU reports the publisher with an unmuted video track
	// or Ingress is in ENDPOINT_PUBLISHING. Independent of agent-reported fps.
	SfuPublishing bool `json:"sfuPublishing"`
}
type RemoteViewStatsPayload struct {
	FPS         int    `json:"fps"`
	BitrateKbps int    `json:"bitrateKbps"`
	Encoder     string `json:"encoder"`
}

// RequestID 从 Gin Context 中获取请求 ID
func RequestID(c *gin.Context) string {
	if value, ok := c.Get("requestID"); ok {
		if requestID, ok := value.(string); ok {
			return requestID
		}
	}
	return ""
}

// ---- Source: backend/models/policy_config.go ----
const (
	DefaultRemoteViewWidth  = 1280
	DefaultRemoteViewHeight = 720
	DefaultRemoteViewFps    = 15
)

type RemoteViewPolicyConfig struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	Fps    int `json:"fps"`
}
type UploadPolicyConfig struct {
	Enabled       bool `json:"enabled"`
	RateLimitKBps int  `json:"rateLimitKBps"`
	MaxConcurrent int  `json:"maxConcurrent"`
	ChunkSizeKB   int  `json:"chunkSizeKB"`
}

type PolicyConfigEnvelope struct {
	Config struct {
		RemoteView *RemoteViewPolicyConfig `json:"remoteView"`
		Upload     *UploadPolicyConfig     `json:"upload"`
	} `json:"config"`
}

func clampInt(value, min, max, fallback int) int {
	if value < min || value > max {
		return fallback
	}
	return value
}

// ParseRemoteViewFromPolicyContent reads config.remoteView from signed policy JSON.
func ParseRemoteViewFromPolicyContent(content string) RemoteViewPolicyConfig {
	cfg := RemoteViewPolicyConfig{
		Width:  DefaultRemoteViewWidth,
		Height: DefaultRemoteViewHeight,
		Fps:    DefaultRemoteViewFps,
	}
	if content == "" {
		return cfg
	}

	var envelope PolicyConfigEnvelope
	if err := json.Unmarshal([]byte(content), &envelope); err != nil || envelope.Config.RemoteView == nil {
		return cfg
	}

	rv := envelope.Config.RemoteView
	if rv.Width > 0 {
		cfg.Width = clampInt(rv.Width, 640, 3840, DefaultRemoteViewWidth)
	}
	if rv.Height > 0 {
		cfg.Height = clampInt(rv.Height, 480, 2160, DefaultRemoteViewHeight)
	}
	if rv.Fps > 0 {
		cfg.Fps = clampInt(rv.Fps, 1, 30, DefaultRemoteViewFps)
	}

	// Keep even dimensions for encoders.
	cfg.Width = int(math.Floor(float64(cfg.Width/2))) * 2
	cfg.Height = int(math.Floor(float64(cfg.Height/2))) * 2
	if cfg.Width <= 0 {
		cfg.Width = DefaultRemoteViewWidth
	}
	if cfg.Height <= 0 {
		cfg.Height = DefaultRemoteViewHeight
	}

	return cfg
}

