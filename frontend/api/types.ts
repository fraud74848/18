// frontend/api/types.ts - 完整的类型定义（与后端 Go 模型对齐）

// ========== 认证相关 ==========
export interface LoginRequest {
  username: string;
  password: string;
  captchaId?: string;
  captchaCode?: string;
  totpCode?: string;
  loginChallenge?: string;
}

export interface LoginConfig {
  captchaEnabled: boolean;
  totpEnabled: boolean;
}

export interface TotpEnrollResponse {
  setupChallenge: string;
  totpProvisioningUri: string;
}

export interface TotpStatus {
  totpEnabled: boolean;
  loginTotpEnabled: boolean;
}

export interface CaptchaResponse {
  captchaId: string;
  imageBase64: string;
}

export interface AuthSecurityConfig {
  loginCaptchaEnabled: boolean;
  loginTotpEnabled: boolean;
  updatedAt?: string;
}

export interface AuthUserInfo {
  id: string;
  username: string;
  role: string;
  permissions: string[];
}

export interface LoginResponse {
  token?: string;
  refreshToken?: string;
  expiresAt?: string;
  user?: AuthUserInfo;
  requiresTotp?: boolean;
  requiresTotpSetup?: boolean;
  loginChallenge?: string;
  totpProvisioningUri?: string;
}

// ========== 设备相关（camelCase） ==========
export interface Device {
  id: string;
  /** 设备ID（可能与 id 相同或不同，用于兼容旧版本） */
  deviceId?: string;
  /** 设备指纹，用于唯一标识设备 */
  deviceFingerprint: string;
  /** 员工ID（系统内部） */
  employeeId: string;
  /** 员工姓名 */
  employeeName: string;
  /** 员工工号 */
  employeeNumber: string;
  /** 职位 */
  position?: string;
  /** 设备名称/主机名 */
  hostname: string;
  /** 操作系统版本 */
  osVersion: string;
  /** Agent 版本号 */
  agentVersion: string;
  /** 审核状态: pending(待审核), approved(已审核), rejected(已拒绝) */
  status: "pending" | "approved" | "rejected";
  /** 在线状态: online(在线), offline(离线) - 前端动态计算，后端不存储 */
  onlineStatus: "online" | "offline";
  /** 最后在线时间 (UTC) */
  lastSeenAt: string | null;
  /** 绑定时间 (UTC) */
  boundAt: string | null;
  /** 创建时间 (UTC) */
  createdAt: string;
  /** 更新时间 (UTC) */
  updatedAt?: string;
  /** 截图统计（前端扩展字段，从 screenshotApi.getEmployeeStats 获取） */
  screenshotStats?: {
    /** 今日截图数量 */
    todayCount: number;
    /** 总截图数量 */
    totalCount: number;
  };
  /** 员工 UUID（共享工位展开行） */
  employeeUuid?: string;
  /** 是否为当前登录会话 */
  isActiveSession?: boolean;
  /** 是否为共享工位设备 */
  isSharedWorkstation?: boolean;
  /** 同设备其他共享成员姓名（顿号分隔） */
  sharedWithNames?: string;
  /** 是否处于强制下线锁（禁止重新注册，直至后台放行） */
  forceOfflined?: boolean;
}

export interface DeviceListResponse {
  items: Device[];
  total: number;
  page: number;
  pageSize: number;
}

export interface OnlineStatsResponse {
  total: number;
  online: number;
  offline: number;
  timestamp: string;
}

// ========== 活动日志相关 ==========
export interface ActivityLog {
  id: string;
  deviceId: string;
  processName: string;
  windowTitle?: string;
  browserTitle?: string;
  startedAt: string;
  endedAt?: string;
  durationSeconds: number;
  isIdle: boolean;
  createdAt: string;
  reported?: boolean; // ✅ 添加：是否已报告
  reportedAt?: string; // ✅ 添加：报告时间
  retryCount?: number; // ✅ 添加：重试次数
  errorMessage?: string; // ✅ 添加：错误信息
}

export interface ActivityStats {
  totalDuration: number;
  idleDuration: number;
  activeDuration: number;
  productivityRate: number;
  topApps: Array<{
    processName: string;
    duration: number;
    percentage?: number;
  }>;
  dailyActivity: Array<{ date: string; duration: number; idle: number }>;
  hourlyActivity: Record<number, number>;
}

export interface AppUsage {
  processName: string;
  duration: number;
  percentage?: number;
}

export interface DailyActivitySummary {
  date: string;
  totalDuration: number;
  idleDuration: number;
  activeDuration: number;
  productivityRate: number;
  topApps: Array<{ processName: string; duration: number; percentage?: number }>;
}

// ========== 录制相关 ==========
export interface Recording {
  id: string;
  deviceId: string;
  fileName?: string;
  fileSize: number;
  duration: number; // ✅ 添加：时长（秒）
  startTime: string;
  endTime: string;
  uploaded: boolean; // ✅ 改为 uploaded (布尔值)
  uploadedAt?: string; // ✅ 可选，上传完成时间（如果有）
  status?: string; // ✅ 可选，前端计算使用
  format?: string;
  filePath?: string; // ✅ 添加：文件路径
  sha256?: string; // ✅ 添加：文件哈希
  createdAt?: string; // ✅ 添加：创建时间
}

export interface RecordingListResponse {
  items: Recording[];
  total: number;
  page: number;
  pageSize: number;
}

// ========== 策略相关 ==========
export interface PolicyRecordingConfig {
  enabled: boolean;
  fps: number;
  quality: number;
  enableGpu: boolean;
  sliceMinutes: number;
  autoUploadOnIndex?: boolean;
}

export interface PolicyUploadConfig {
  enabled: boolean;
  rateLimitKBps: number;
  maxConcurrent: number;
  chunkSizeKB: number;
}

export interface PolicyRemoteViewConfig {
  width: number;
  height: number;
  fps: number;
}

export interface PolicyClientProtectionConfig {
  allowUserExit: boolean;
  allowAutoStartToggle: boolean;
  restartOnKill: boolean;
  unlockCodeUnusedExpireMinutes: number;
}

export interface PolicyContent {
  version: number;
  rules?: Array<{
    id?: string;
    name?: string;
    condition?: string;
    action: string;
    match?: string;
  }>;
  config?: {
    recording?: PolicyRecordingConfig;
    screenshot?: {
      enabled: boolean;
      intervalSeconds: number;
      quality: number;
      format: string;
    };
    upload?: PolicyUploadConfig;
    remoteView?: PolicyRemoteViewConfig;
    activity?: {
      enabled: boolean;
      trackIdle: boolean;
      idleThresholdSeconds: number;
    };
    security?: {
      enableEncryption: boolean;
      enableVPN: boolean;
    };
    cleanup?: {
      enabled: boolean;
      recordingDays: number;
      screenshotDays: number;
      logDays: number;
      diskThresholdPercent: number;
    };
    clientProtection?: PolicyClientProtectionConfig;
  };
}

export interface RemoteViewAuditLog {
  id: string;
  sessionId?: string;
  deviceId: string;
  operatorId: string;
  operatorName: string;
  action: string;
  detail?: string;
  clientIp?: string;
  employeeName?: string;
  employeeNumber?: string;
  hostname?: string;
  createdAt: string;
}

export interface RemoteViewAuditLogList {
  items: RemoteViewAuditLog[];
  total: number;
  page: number;
  pageSize: number;
}

export interface Policy {
  id: string;
  name: string;
  version: number;
  content: string;
  signature?: string;
  status: number;
  createdAt: string;
  updatedAt: string;
  isCurrent?: boolean;
}

export interface CleanupPolicy {
  autoCleanupEnabled: boolean;
  recordingRetentionDays: number;
  screenshotRetentionDays: number;
  logRetentionDays: number;
  attendanceRetentionMonths: number;
  performanceRetentionMonths: number;
  penaltyRetentionMonths: number;
  salaryRetentionMonths: number;
  siteStatsRetentionMonths: number;
  diskUsageThresholdPercent: number;
  cleanupIntervalMinutes: number;
  lastRunAt: string | null;
  updatedAt: string;
}

export interface BusinessRecordStats {
  attendanceCount: number;
  performanceCount: number;
  penaltyCount: number;
  salaryCount: number;
  siteStatsCount: number;
  totalCount: number;
}

// ========== 清理相关 ==========
export interface StorageStats {
  totalBytes: number;
  usedBytes: number;
  freeBytes: number;
  diskUsagePercent: number;
  uploadsBytes?: number;
  recordingCount: number;
  screenshotCount: number;
  logCount: number;
  businessRecords?: BusinessRecordStats;
  lastCleanupAt: string | null;
}

export interface CleanupLog {
  id: string;
  triggerType: "auto" | "manual" | "auto_disk_threshold" | "auto_disk_critical";
  status: "success" | "partial" | "failed";
  deletedRecordings: number;
  deletedScreenshots: number;
  deletedLogs: number;
  deletedBusinessRecords?: number;
  deletedBytes: number;
  message: string;
  startedAt: string;
  completedAt: string;
}

// 添加分页响应类型
export interface PageResponse<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
}

/** 设备端任务（录屏按需上传等） */
export interface DeviceTask {
  id: string;
  deviceId: string;
  type: string;
  startTime?: string;
  endTime?: string;
  status: string;
  progress: number;
  result?: string;
  completedAt?: string;
  createdAt?: string;
}

// ========== 用户相关 ==========
export interface User {
  id: string;
  username: string;
  realName?: string;
  email?: string;
  status: number;
  totpEnabled?: boolean;
  roleIds?: string[];
  roles?: Role[];
  createdAt: string;
  updatedAt: string;
  lastLogin?: string | null;
}

export interface Role {
  id: string;
  name: string;
  description?: string;
  permissions: string[];
  createdAt: string;
  updatedAt: string;
}

// ========== 截图相关 ==========
export interface Screenshot {
  id: string;
  deviceId: string;
  fileName: string;
  fileSize: number;
  format: string;
  width: number; // ✅ 添加：图片宽度
  height: number; // ✅ 添加：图片高度
  capturedAt: string;
  uploadedAt: string;
  thumbnailUrl?: string;
  imageUrl?: string;
  filePath: string;
  uploaded: boolean;
  createdAt: string;
  encrypted?: boolean;
  employeeUuid?: string;
  employeeName?: string;
  employeeNumber?: string;
}

export interface ScreenshotListResponse {
  items: Screenshot[];
  total: number;
  page: number;
  pageSize: number;
}

export interface ScreenshotOverviewItem {
  deviceId: string;
  employeeUuid?: string;
  employeeName: string;
  employeeNumber: string;
  hostname: string;
  totalCount: number;
  sharedWithNames?: string;
  isSharedWorkstation?: boolean;
  screenshots: Screenshot[];
}

export interface ScreenshotOverviewResponse {
  limit: number;
  items: ScreenshotOverviewItem[];
}

// ========== 远程查看相关 ==========
export interface RemoteViewStartSession {
  sessionId: string;
  deviceId: string;
  deviceName: string;
  roomName: string;
  livekitUrl: string;
  viewerToken: string;
  expiresAt: string;
}

export interface RemoteViewStatus {
  isActive: boolean;
  sessionId?: string;
  status?: string;
  viewerCount: number;
  startedAt?: string;
  updatedAt?: string;
  /** Backend StartingTimeout in seconds — FE warmup must use the same window. */
  startingTimeoutSec?: number;
  statsFresh?: boolean;
  sfuChecked?: boolean;
  sfuPublishing?: boolean;
  stats?: {
    fps: number;
    bitrateKbps: number;
    encoder: string;
  };
}

// ========== 响应类型 ==========
export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

export interface ListResponse<T> {
  items: T[];
  total: number;
  page?: number;
  pageSize?: number;
}

// ========== WebSocket 消息类型 ==========
export interface WSMessage {
  type:
    | "online_devices"
    | "device_status"
    | "policy_update"
    | "device_info_update"
    | "task_notification"
    | "session_revoked"
    | "ping"
    | "pong";
  reason?: string;
  devices?: string[];
  deviceId?: string;
  status?: "online" | "offline";
  timestamp?: number;
  data?: any;
  [key: string]: any;
}

// 在文件末尾添加

// ========== 今日考勤异常人员 ==========
export interface TodayAbsenteeItem {
  employeeId: string;
  employeeName: string;
  position: string;
  status: string;
  statusLabel: string;
  date: string;
}

export interface TodayAbsenteesResponse {
  vacation: TodayAbsenteeItem[]; // 休假（全天+半天）
  leave: TodayAbsenteeItem[]; // 请假
  absent: TodayAbsenteeItem[]; // 旷工
  total: number;
}
