// frontend/api/index.ts
import request from "./request";
import type {
  LoginRequest,
  LoginResponse,
  LoginConfig,
  CaptchaResponse,
  AuthSecurityConfig,
  TotpEnrollResponse,
  TotpStatus,
  Device,
  DeviceListResponse,
  OnlineStatsResponse,
  RecordingListResponse,
  Recording,
  Policy,
  PolicyContent,
  CleanupPolicy,
  StorageStats,
  CleanupLog,
  PageResponse,
  DeviceTask,
  User,
  Role,
  RemoteViewStartSession,
  RemoteViewStatus,
  RemoteViewAuditLogList,
  Screenshot,
  ScreenshotListResponse,
  ScreenshotOverviewResponse,
  ActivityLog,
  ActivityStats,
  DailyActivitySummary,
} from "./types";

// ✅ 从 format.ts 导入格式化函数（避免重复）
import {
  formatBytes,
  formatDateTime,
  formatDate,
  formatTime,
  getRelativeTime,
  getStatusType,
  getStatusText,
  formatDuration,
  truncateText,
  formatDeviceId,
  formatPercent,
  formatNumber,
  formatISOString,
} from "./format";
import {
  fetchAllDevicesCached,
  invalidateDeviceListCache,
} from "./deviceListCache";

export { invalidateDeviceListCache };
export { fetchMetaModules, invalidateMetaModulesCache } from "./metaModulesCache";
export type { MetaModules } from "./metaModulesCache";

// ========== 重新导出格式化函数（保持向后兼容） ==========
export {
  formatBytes,
  formatDateTime,
  formatDate,
  formatTime,
  getRelativeTime,
  getStatusType,
  getStatusText,
  formatDuration,
  truncateText,
  formatDeviceId,
  formatPercent,
  formatNumber,
  formatISOString,
};

// ========== 时间工具（与后端 UTC 保持一致） ==========
export const formatUTC = (date: Date = new Date()): string => {
  return date.toISOString();
};

export const parseUTC = (dateStr: string): Date => {
  return new Date(dateStr);
};

// ========== 认证 API ==========
export const authApi = {
  getLoginConfig: async (): Promise<{ data: LoginConfig }> => {
    const response = await request.get<any>("/auth/login-config");
    if (response.data?.data) return { data: response.data.data as LoginConfig };
    return { data: response.data as LoginConfig };
  },

  getCaptcha: async (): Promise<{ data: CaptchaResponse }> => {
    const response = await request.get<any>("/auth/captcha");
    const payload = (response.data?.data ?? response.data) as CaptchaResponse;
    return { data: payload };
  },

  login: async (data: LoginRequest): Promise<{ data: LoginResponse; message?: string }> => {
    const response = await request.post<any>("/auth/login", data);
    const raw = response.data;
    const body = (raw?.data ?? raw) as LoginResponse;
    const message =
      raw && typeof raw === "object" && "message" in raw
        ? String((raw as { message?: string }).message || "")
        : undefined;
    return { data: body, message };
  },

  refresh: async (
    refreshToken: string,
  ): Promise<{ data: LoginResponse }> => {
    const response = await request.post<any>("/auth/refresh", { refreshToken });
    if (
      response.data &&
      typeof response.data === "object" &&
      "data" in response.data
    ) {
      return { data: response.data.data as LoginResponse };
    }
    return { data: response.data as LoginResponse };
  },

  logout: (refreshToken?: string) =>
    request.post("/auth/logout", refreshToken ? { refreshToken } : {}),

  wsTicket: async (): Promise<{ ticket: string; expiresIn: number }> => {
    const response = await request.post<any>("/ws-ticket", {});
    const payload = (response.data?.data ?? response.data) as {
      ticket?: string;
      expiresIn?: number;
    };
    return {
      ticket: payload?.ticket || "",
      expiresIn: payload?.expiresIn || 0,
    };
  },
};

export const metaApi = {
  modules: () =>
    request.get<{ domainMonitor: boolean; callback: boolean; wsTicketRequired?: boolean }>(
      "/meta/modules",
    ),
};

export interface ClientProtectionDefaults {
  allowUserExit: boolean;
  allowAutoStartToggle: boolean;
  restartOnKill: boolean;
  unlockCodeUnusedExpireMinutes: number;
  updatedAt?: string;
}

export interface ClientProtectionDeviceOverride {
  useDefault: boolean;
  allowUserExit?: boolean;
  allowAutoStartToggle?: boolean;
  restartOnKill?: boolean;
}

export interface ClientProtectionEffective {
  allowUserExit: boolean;
  allowAutoStartToggle: boolean;
  restartOnKill: boolean;
  unlockCodeConfigured: boolean;
  temporaryExitUntil?: string;
}

// ========== 设备 API ==========
export const deviceApi = {
  list: (params: {
    page?: number;
    pageSize?: number;
    status?: string;
    employeeSearch?: string; // ✅ 新增
    deviceStatus?: string; // ✅ 新增
    keyword?: string; // ✅ 新增
  }) => request.get<DeviceListResponse>("/devices", { params }),

  /** 分页拉取全部设备（后端单页最大 100 条）；30s 内存缓存 + 并发 dedup，force:true 强制刷新 */
  listAll: async (
    params?: Omit<
      {
        page?: number;
        pageSize?: number;
        status?: string;
        employeeSearch?: string;
        deviceStatus?: string;
        keyword?: string;
        force?: boolean;
      },
      "page" | "pageSize"
    >,
  ) => {
    const { force, ...query } = params ?? {};

    const fetchAll = async (listParams?: typeof query) => {
      const all: Device[] = [];
      let page = 1;
      const pageSize = 100;
      let total = 0;
      while (true) {
        const { data } = await request.get<DeviceListResponse>("/devices", {
          params: { ...listParams, page, pageSize },
        });
        all.push(...(data.items || []));
        total = data.total ?? all.length;
        if (all.length >= total || !(data.items?.length)) break;
        page++;
      }
      return {
        data: {
          items: all,
          total: all.length,
          page: 1,
          pageSize: all.length,
        } satisfies DeviceListResponse,
      };
    };

    return fetchAllDevicesCached(fetchAll, query, { force: force === true });
  },

  get: (id: string) => request.get<Device>(`/devices/${id}`),

  bind: (
    id: string,
    data: {
      employeeId: string;
      employeeName?: string;
      employeeNumber?: string;
    },
  ) => request.post(`/devices/${id}/bind`, data),

  unbind: (id: string) => request.post(`/devices/${id}/unbind`),

  forceOffline: (id: string) => request.post(`/devices/${id}/force-offline`),

  allowReregister: (id: string) =>
    request.post(`/devices/${id}/allow-reregister`),

  updateStatus: (id: string, status: "pending" | "approved" | "rejected") =>
    request.put(`/devices/${id}/status`, { status }),

  onlineStats: () => request.get<OnlineStatsResponse>("/devices/stats/online"),

  reportInfo: (data: {
    deviceId: string;
    hostname: string;
    osVersion: string;
    agentVersion: string;
  }) => request.post("/devices/info", data),

  register: (data: {
    deviceId: string;
    hostname: string;
    osVersion: string;
    agentVersion: string;
    fingerprint: Record<string, string>;
    publicKey: string;
  }) => request.post<LoginResponse>("/devices/register", data),
  updateEmployee: (
    id: string,
    data: {
      employeeId?: string;
      employeeName?: string;
      employeeNumber?: string;
      position?: string;
    },
  ) => request.put(`/devices/${id}/employee`, data),

  getClientProtection: (id: string) =>
    request.get<{
      override: ClientProtectionDeviceOverride;
      effective: ClientProtectionEffective;
    }>(`/devices/${id}/client-protection`),

  updateClientProtection: (id: string, data: ClientProtectionDeviceOverride) =>
    request.put(`/devices/${id}/client-protection`, data),

  grantTemporaryExit: (id: string, minutes: number) =>
    request.post<{ temporaryExitUntil: string }>(
      `/devices/${id}/temporary-exit-grant`,
      { minutes },
    ),

  issueExitUnlockCode: (id: string) =>
    request.post<{ code: string; expiresAt: string }>(
      `/devices/${id}/exit-unlock-code`,
      {},
    ),
};

// ========== 录制 API ==========
const recordingApiBase =
  (import.meta.env.VITE_API_BASE_URL as string) || "/api/v1";

type MediaResourceType = "recording" | "screenshot";
type MediaAction = "stream" | "download" | "view";

/** 与 Redis mediaToken TTL 对齐：到期前 60s 视为需刷新 */
export const MEDIA_TOKEN_REFRESH_BUFFER_MS = 60_000;

export type MediaAccessResult = {
  url: string;
  expiresAt: number;
};

const mediaTokenCache = new Map<
  string,
  { token: string; expiresAt: number }
>();

/** mediaToken 是否仍可安全复用（提前刷新缓冲内视为过期） */
export const isMediaAccessFresh = (
  expiresAt: number,
  now = Date.now(),
): boolean => expiresAt > now + MEDIA_TOKEN_REFRESH_BUFFER_MS;

/** 清除已缓存的 mediaToken（图片 401 时强制重新签发） */
export const invalidateMediaAccessToken = (
  resourceType: MediaResourceType,
  resourceId: string,
  action: MediaAction,
) => {
  const prefix = `${resourceType}:${resourceId}:${action}`;
  for (const key of mediaTokenCache.keys()) {
    if (key === prefix || key.startsWith(`${prefix}:`)) {
      mediaTokenCache.delete(key);
    }
  }
};

const getMediaAccess = async (
  resourceType: MediaResourceType,
  resourceId: string,
  action: MediaAction,
  path: string,
  cacheSuffix?: string,
): Promise<MediaAccessResult> => {
  const cacheKey = cacheSuffix
    ? `${resourceType}:${resourceId}:${action}:${cacheSuffix}`
    : `${resourceType}:${resourceId}:${action}`;
  const cached = mediaTokenCache.get(cacheKey);
  if (cached && isMediaAccessFresh(cached.expiresAt)) {
    return {
      url: `${recordingApiBase}${path}?mediaToken=${encodeURIComponent(cached.token)}`,
      expiresAt: cached.expiresAt,
    };
  }

  const { data } = await request.post<{
    mediaToken: string;
    expiresIn: number;
  }>("/media/access-token", {
    resourceType,
    resourceId,
    action,
  });

  const expiresAt = Date.now() + data.expiresIn * 1000;
  mediaTokenCache.set(cacheKey, {
    token: data.mediaToken,
    expiresAt,
  });

  return {
    url: `${recordingApiBase}${path}?mediaToken=${encodeURIComponent(data.mediaToken)}`,
    expiresAt,
  };
};

const getMediaAccessUrl = async (
  resourceType: MediaResourceType,
  resourceId: string,
  action: MediaAction,
  path: string,
  cacheSuffix?: string,
): Promise<string> =>
  (await getMediaAccess(resourceType, resourceId, action, path, cacheSuffix)).url;

/** 短期令牌流式播放地址（供 <video> 使用，不在 URL 中传递 JWT） */
export const getRecordingStreamUrl = (id: string): Promise<string> =>
  getMediaAccessUrl("recording", id, "stream", `/recordings/${id}/stream`);

/** 短期令牌下载地址 */
export const getRecordingDownloadUrl = (id: string): Promise<string> =>
  getMediaAccessUrl("recording", id, "download", `/recordings/${id}/download`);

/** 截图原图直链（供 <img> 使用，启用浏览器缓存与渐进加载） */
export const getScreenshotImageUrl = (id: string): Promise<string> =>
  getMediaAccessUrl("screenshot", id, "view", `/screenshots/${id}/image`, "image");

/** 截图缩略图直链 */
export const getScreenshotThumbnailUrl = (id: string): Promise<string> =>
  getMediaAccessUrl("screenshot", id, "view", `/screenshots/${id}/thumbnail`, "thumb");

/** 截图原图访问信息（含过期时间，供页面级缓存对齐 token 生命周期） */
export const getScreenshotImageAccess = (id: string): Promise<MediaAccessResult> =>
  getMediaAccess("screenshot", id, "view", `/screenshots/${id}/image`, "image");

/** 截图缩略图访问信息（含过期时间） */
export const getScreenshotThumbnailAccess = (
  id: string,
): Promise<MediaAccessResult> =>
  getMediaAccess("screenshot", id, "view", `/screenshots/${id}/thumbnail`, "thumb");

export const recordingApi = {
  list: (params: {
    deviceId?: string;
    startTime?: string;
    endTime?: string;
    page?: number;
    pageSize?: number;
  }) => request.get<RecordingListResponse>("/recordings", { params }),

  /** 分页拉取全部录制（后端单页最大 100 条） */
  listAll: async (
    params?: Omit<
      {
        deviceId?: string;
        startTime?: string;
        endTime?: string;
        page?: number;
        pageSize?: number;
      },
      "page" | "pageSize"
    >,
  ) => {
    const all: Recording[] = [];
    let page = 1;
    const pageSize = 100;
    let total = 0;
    while (true) {
      const { data } = await request.get<RecordingListResponse>("/recordings", {
        params: { ...params, page, pageSize },
      });
      all.push(...(data.items || []));
      total = data.total ?? all.length;
      if (all.length >= total || !(data.items?.length)) break;
      page++;
    }
    return { data: { items: all, total: all.length } };
  },

  getStats: (params: {
    deviceId?: string;
    startTime?: string;
    endTime?: string;
  }) =>
    request.get<{
      total: number;
      totalSize: number;
      avgSize: number;
      todayCount: number;
    }>("/recordings/stats", { params }),

  getPlayUrl: (id: string) =>
    request.get<{ url: string; streamUrl?: string }>(
      `/recordings/${id}/play`,
    ),

  delete: (id: string) => request.delete(`/recordings/${id}`),

  batchDelete: (ids: string[]) =>
    request.post("/recordings/batch-delete", { ids }),
};

// ========== 策略 API ==========
export const policyApi = {
  list: () => request.get<Policy[]>("/policies"),

  get: (id: string) => request.get<Policy>(`/policies/${id}`),

  create: (name: string, content: PolicyContent) =>
    request.post<Policy>("/policies", {
      name,
      content: content, // ✅ 直接传对象
    }),

  update: (id: string, name: string, content: PolicyContent) =>
    request.put<Policy>(`/policies/${id}`, {
      name,
      content: content,
    }),

  delete: (id: string) => request.delete(`/policies/${id}`),

  publish: (id: string) => request.post(`/policies/${id}/publish`),

  rollback: (id: string) => request.post(`/policies/${id}/rollback`),
};

// ========== 清理 API ==========
export const cleanupApi = {
  getPolicy: () => request.get<CleanupPolicy>("/cleanup/policy"),

  updatePolicy: (policy: CleanupPolicy) =>
    request.put<CleanupPolicy>("/cleanup/policy", policy),

  getStats: () => request.get<StorageStats>("/cleanup/stats"),

  manualCleanup: () => request.post<CleanupLog>("/cleanup/manual"),

  getLogs: (params?: { page?: number; pageSize?: number }) =>
    request.get<PageResponse<CleanupLog>>("/cleanup/logs", { params }),
};

export interface CorsOriginsConfig {
  origins: string[];
  effectiveOrigins: string[];
  source: "database" | "environment" | "default" | "none";
  updatedAt?: string;
}

export interface LoginIPWhitelistConfig {
  enabled: boolean;
  ips: string[];
  updatedAt?: string;
}

export const settingsApi = {
  getCorsOrigins: () => request.get<CorsOriginsConfig>("/settings/cors-origins"),

  updateCorsOrigins: (origins: string[]) =>
    request.put<CorsOriginsConfig>("/settings/cors-origins", { origins }),

  getAuthSecurity: () => request.get<AuthSecurityConfig>("/settings/auth-security"),

  updateAuthSecurity: (data: {
    loginCaptchaEnabled: boolean;
    loginTotpEnabled: boolean;
  }) => request.put<AuthSecurityConfig>("/settings/auth-security", data),

  getLoginIPWhitelist: () =>
    request.get<LoginIPWhitelistConfig>("/settings/login-ip-whitelist"),

  updateLoginIPWhitelist: (data: { enabled: boolean; ips: string[] }) =>
    request.put<LoginIPWhitelistConfig>("/settings/login-ip-whitelist", data),

  getClientProtectionDefaults: () =>
    request.get<ClientProtectionDefaults>("/settings/client-protection"),

  updateClientProtectionDefaults: (data: {
    allowUserExit: boolean;
    allowAutoStartToggle: boolean;
    restartOnKill: boolean;
    unlockCodeUnusedExpireMinutes: number;
  }) => request.put<ClientProtectionDefaults>("/settings/client-protection", data),
};

export type { AuthSecurityConfig } from "./types";

// ========== 任务 API ==========
export const taskApi = {
  create: (data: {
    deviceId: string;
    type: string;
    startTime?: string;
    endTime?: string;
  }) => request.post<DeviceTask>("/tasks", data),

  get: (id: string) => request.get<DeviceTask>(`/tasks/${id}`),

  list: () => request.get<{ items: DeviceTask[]; total: number }>("/tasks"),

  cancel: (id: string) => request.post(`/tasks/${id}/cancel`),

  retry: (id: string) => request.post(`/tasks/${id}/retry`),
};

// ========== 用户 API ==========
export const userApi = {
  list: (params?: { page?: number; pageSize?: number; keyword?: string }) =>
    request.get<{ items: User[]; total: number }>("/users", { params }),

  create: (data: {
    username: string;
    password: string;
    realName?: string;
    email?: string;
    roleIds?: string[];
  }) => request.post<User>("/users", data),

  get: (id: string) => request.get<User>(`/users/${id}`),

  update: (
    id: string,
    data: {
      realName?: string;
      email?: string;
      status?: number;
      roleIds?: string[];
    },
  ) => request.put<User>(`/users/${id}`, data),

  delete: (id: string) => request.delete(`/users/${id}`),

  resetPassword: (id: string, password: string) =>
    request.post(`/users/${id}/reset-password`, { password }),

  getSalaryViewPasswordStatus: (id: string) =>
    request.get<{ configured: boolean }>(`/users/${id}/salary-view-password`),

  resetSalaryViewPassword: (
    id: string,
    data: { password?: string; clear?: boolean },
  ) => request.post(`/users/${id}/reset-salary-view-password`, data),

  getScreenLockPasswordStatus: (id: string) =>
    request.get<{ configured: boolean }>(`/users/${id}/screen-lock-password`),

  resetScreenLockPassword: (
    id: string,
    data: { password?: string; clear?: boolean },
  ) => request.post(`/users/${id}/reset-screen-lock-password`, data),

  resetTotp: (id: string) => request.post(`/users/${id}/reset-totp`),

  startTotpEnroll: async (id: string): Promise<TotpEnrollResponse> => {
    const response = await request.post<any>(`/users/${id}/totp-enroll`);
    return (response.data?.data ?? response.data) as TotpEnrollResponse;
  },

  confirmTotpEnroll: (id: string, data: { setupChallenge: string; totpCode: string }) =>
    request.post(`/users/${id}/totp-enroll/confirm`, data),

  getTotpStatus: async (): Promise<TotpStatus> => {
    const response = await request.get<any>("/user/totp");
    return (response.data?.data ?? response.data) as TotpStatus;
  },

  startOwnTotpEnroll: async (password: string): Promise<TotpEnrollResponse> => {
    const response = await request.post<any>("/user/totp/enroll", { password });
    return (response.data?.data ?? response.data) as TotpEnrollResponse;
  },

  confirmOwnTotpEnroll: (data: { setupChallenge: string; totpCode: string }) =>
    request.post("/user/totp/enroll/confirm", data),

  getProfile: () => request.get<User>("/user/profile"),

  updateProfile: (data: { realName?: string; email?: string }) =>
    request.put<User>("/user/profile", data),

  changeOwnPassword: (data: { oldPassword: string; newPassword: string }) =>
    request.post("/user/change-password", data),

  getScreenLockStatus: () =>
    request.get<{ configured: boolean }>("/user/screen-lock/status"),

  setupScreenLockPassword: (data: { password: string }) =>
    request.post("/user/screen-lock/setup", data),

  verifyScreenLockPassword: (data: { password: string }) =>
    request.post("/user/screen-lock/verify", data),

  changeScreenLockPassword: (data: {
    oldPassword: string;
    newPassword: string;
  }) => request.post("/user/screen-lock/change-password", data),
};

// ========== 角色 API ==========
export const roleApi = {
  list: () => request.get<Role[]>("/roles"),

  create: (data: {
    name: string;
    description?: string;
    permissions: string[];
  }) => request.post<Role>("/roles", data),

  update: (
    id: string,
    data: { name?: string; description?: string; permissions?: string[] },
  ) => request.put<Role>(`/roles/${id}`, data),

  delete: (id: string) => request.delete(`/roles/${id}`),
};

// ========== 截图 API ==========
export const screenshotApi = {
  list: (params: {
    deviceId?: string;
    employeeUuid?: string;
    startDate?: string;
    endDate?: string;
    page?: number;
    pageSize?: number;
  }) => request.get<ScreenshotListResponse>("/screenshots", { params }),

  get: (id: string) => request.get<Screenshot>(`/screenshots/${id}`),

  delete: (id: string) => request.delete(`/screenshots/${id}`),

  batchDelete: (ids: string[]) =>
    request.post("/screenshots/batch-delete", { ids }),

  purge: (data: {
    deleteAll?: boolean;
    startDate?: string;
    endDate?: string;
  }) => request.post<{ deleted: number }>("/screenshots/purge", data),

  getEmployeeStats: (deviceId: string) =>
    request.get<{ todayCount: number; totalCount: number }>(
      `/screenshots/stats/employee/${deviceId}`,
    ),

  getBatchEmployeeStats: (deviceIds: string[]) =>
    request.get<Record<string, { todayCount: number; totalCount: number }>>(
      "/screenshots/stats/batch",
      { params: { deviceIds: deviceIds.join(",") } },
    ),

  getOverview: (params?: {
    limit?: number;
    startDate?: string;
    endDate?: string;
    startTime?: string;
    endTime?: string;
  }) => request.get<ScreenshotOverviewResponse>("/screenshots/overview", { params }),
};

// ========== 活动日志 API ==========
export const activityApi = {
  list: (params: {
    deviceId?: string;
    startDate?: string;
    endDate?: string;
    processName?: string;
    page?: number;
    pageSize?: number;
  }) =>
    request.get<{
      items: ActivityLog[];
      total: number;
      page: number;
      pageSize: number;
    }>("/activities", { params }),

  getStats: (deviceId?: string, startDate?: string, endDate?: string) => {
    const params: any = {};
    if (deviceId) params.deviceId = deviceId;
    if (startDate) params.startDate = startDate;
    if (endDate) params.endDate = endDate;
    return request.get<ActivityStats>("/activities/stats", { params });
  },

  getDailySummary: (deviceId?: string, days?: number) =>
    request.get<DailyActivitySummary[]>("/activities/daily-summary", {
      params: { deviceId, days },
    }),

  export: (params: {
    deviceId?: string;
    startDate?: string;
    endDate?: string;
  }) => request.get("/activities/export", { params, responseType: "blob" }),

  delete: (data: { ids?: string[]; daysOld?: number }) =>
    request.delete("/activities", { data }),

  // ✅ Agent 上报活动日志（客户端使用）
  report: (deviceId: string, activities: ActivityLog[]) =>
    request.post("/activities/report", { deviceId, activities }),

  // ✅ 获取进程列表（用于活动日志筛选）
  getProcessNames: (deviceId?: string) =>
    request.get<string[]>("/activities/processes", { params: { deviceId } }),
};

// ========== 远程查看 API（LiveKit）==========
export type RemoteViewQuality = "weak" | "auto" | "strong";

export const remoteViewApi = {
  start: (deviceId: string, opts?: { quality?: RemoteViewQuality; networkProfile?: RemoteViewQuality; freshStart?: boolean }) =>
    request.post<RemoteViewStartSession>(
      `/devices/${deviceId}/remote-view/start`,
      {
        quality: opts?.quality ?? opts?.networkProfile,
        networkProfile: opts?.networkProfile ?? opts?.quality,
        freshStart: opts?.freshStart ?? false,
      },
    ),

  stop: (deviceId: string, sessionId?: string, opts?: { hard?: boolean }) =>
    request.post<{ deviceId: string; stopped: boolean; publisherStopped: boolean }>(
      `/devices/${deviceId}/remote-view/stop`,
      {
        ...(sessionId ? { sessionId } : {}),
        ...(opts?.hard ? { hard: true } : {}),
      },
    ),

  setQuality: (
    deviceId: string,
    quality: RemoteViewQuality,
    sessionId?: string,
  ) =>
    request.post<{ deviceId: string; networkProfile: string; updated: boolean }>(
      `/devices/${deviceId}/remote-view/quality`,
      {
        quality,
        networkProfile: quality,
        ...(sessionId ? { sessionId } : {}),
      },
    ),

  refreshViewerToken: (deviceId: string, sessionId: string) =>
    request.post<{
      sessionId: string;
      deviceId: string;
      roomName: string;
      livekitUrl: string;
      viewerToken: string;
      expiresAt: string;
    }>(`/devices/${deviceId}/remote-view/viewer-token`, { sessionId }),

  getStatus: (deviceId: string) =>
    request.get<RemoteViewStatus>(`/devices/${deviceId}/remote-view/status`),

  listAuditLogs: (params?: { page?: number; pageSize?: number; deviceId?: string }) =>
    request.get<RemoteViewAuditLogList>("/remote-view/audit-logs", { params }),
};

export type DiagnosticLevelName =
  | "OFF"
  | "ERROR"
  | "WARN"
  | "INFO"
  | "DEBUG"
  | "TRACE";

export const diagnosticApi = {
  getConfig: () =>
    request.get<{
      config: {
        enabled: boolean;
        expiresAt?: string | null;
        globalLevel: DiagnosticLevelName;
        moduleLevels: Record<string, DiagnosticLevelName>;
        agentUploadEnabled: boolean;
        frontendUploadEnabled: boolean;
        retentionHours: number;
        appliedTemplateId?: string;
        updatedAt?: string;
        updatedBy?: string;
      };
      runtime: {
        active: boolean;
        expiresAt?: string | null;
        globalLevel: DiagnosticLevelName;
        moduleLevels: Record<string, DiagnosticLevelName>;
      };
      modules: string[];
      levels: DiagnosticLevelName[];
    }>("/diagnostics/config"),

  updateConfig: (data: {
    enabled: boolean;
    durationMinutes?: number;
    globalLevel: DiagnosticLevelName;
    moduleLevels: Record<string, DiagnosticLevelName>;
    agentUploadEnabled: boolean;
    frontendUploadEnabled: boolean;
    retentionHours: number;
  }) => request.put("/diagnostics/config", data),

  listTemplates: () =>
    request.get<{
      items: Array<{
        id: string;
        name: string;
        description: string;
        globalLevel: DiagnosticLevelName;
        moduleLevels: Record<string, DiagnosticLevelName>;
        defaultDurationMinutes: number;
      }>;
    }>("/diagnostics/templates"),

  applyTemplate: (data: { templateId: string; durationMinutes?: number }) =>
    request.post<{
      config: {
        enabled: boolean;
        expiresAt?: string | null;
        globalLevel: DiagnosticLevelName;
        moduleLevels: Record<string, DiagnosticLevelName>;
        appliedTemplateId?: string;
        agentUploadEnabled: boolean;
        frontendUploadEnabled: boolean;
        retentionHours: number;
      };
      runtime: {
        active: boolean;
        expiresAt?: string | null;
        globalLevel: DiagnosticLevelName;
        moduleLevels: Record<string, DiagnosticLevelName>;
      };
    }>("/diagnostics/templates/apply", data),

  listEvents: (params?: {
    page?: number;
    pageSize?: number;
    source?: string;
    module?: string;
    level?: string;
    deviceId?: string;
    from?: string;
    to?: string;
  }) =>
    request.get<{
      items: Array<{
        id: string;
        source: string;
        deviceId?: string;
        module: string;
        level: string;
        message: string;
        context?: Record<string, unknown>;
        correlationId?: string;
        createdAt: string;
      }>;
      total: number;
      page: number;
      pageSize: number;
    }>("/diagnostics/events", { params }),

  exportBundle: (params?: {
    source?: string;
    module?: string;
    level?: string;
    deviceId?: string;
    from?: string;
    to?: string;
  }) =>
    request.get<Blob>("/diagnostics/export", {
      params,
      responseType: "blob",
    }),

  getSnapshot: () =>
    request.get<DiagnosticSnapshot>("/diagnostics/snapshot"),

  analyze: (params?: { windowMinutes?: number; deviceId?: string }) =>
    request.get<DiagnosticAnalysis>("/diagnostics/analyze", { params }),

  getPipeline: (deviceId: string) =>
    request.get<DiagnosticPipeline>("/diagnostics/pipeline", {
      params: { deviceId },
    }),

  capture: (data: DiagnosticCaptureRequest) =>
    request.post<DiagnosticCaptureResult>("/diagnostics/capture", data),

  captureExport: (data: DiagnosticCaptureRequest) =>
    request.post<Blob>("/diagnostics/capture/export", data, {
      responseType: "blob",
    }),

  getPipelineNode: (deviceId: string, nodeId: string) =>
    request.get<DiagnosticPipelineNode>("/diagnostics/pipeline/node", {
      params: { deviceId, nodeId },
    }),

  listSessions: (params?: { deviceId?: string; limit?: number }) =>
    request.get<{ items: DiagnosticSessionGroup[] }>("/diagnostics/sessions", { params }),

  getSession: (sessionId: string) =>
    request.get<DiagnosticSessionGroup>(`/diagnostics/sessions/${sessionId}`),

  startReproduce: (data: DiagnosticReproduceRequest) =>
    request.post<DiagnosticReproduceStatus>("/diagnostics/reproduce", data),

  getReproduceStatus: (jobId: string) =>
    request.get<DiagnosticReproduceStatus>(`/diagnostics/reproduce/${jobId}`),
};

export type DiagnosticEventItem = {
  id: string;
  source: string;
  deviceId?: string;
  module: string;
  level: string;
  message: string;
  context?: Record<string, unknown>;
  correlationId?: string;
  createdAt: string;
};

export type DiagnosticSnapshot = {
  generatedAt: string;
  runtime: {
    active: boolean;
    expiresAt?: string | null;
    globalLevel: DiagnosticLevelName;
    moduleLevels: Record<string, DiagnosticLevelName>;
  };
  websocket: {
    agentsOnline: number;
    adminConnections: number;
    totalConnections: number;
  };
  events: {
    totalStored: number;
    last5Min: Record<string, number>;
    last1Hour: Record<string, number>;
    byModule5m: Record<string, number>;
  };
  remoteView: {
    activeSessions: number;
    publishingFresh: number;
    sessions: Array<{
      sessionId: string;
      deviceId: string;
      hostname?: string;
      status: string;
      viewerCount: number;
      agentOnline: boolean;
      statsFresh: boolean;
      ingressId?: string;
      startedAt?: string;
    }>;
  };
  agents: Array<{
    deviceId: string;
    hostname?: string;
    online: boolean;
    statsFresh: boolean;
    publishing: boolean;
    sessionId?: string;
    sessionStatus?: string;
  }>;
  health: DiagnosticHealthScore;
  statsToday: DiagnosticDailyStats;
};

export type DiagnosticHealthScore = {
  score: number;
  layers: Array<{ id: string; label: string; status: string; detail?: string }>;
};

export type DiagnosticDailyStats = {
  remoteViewStarts: number;
  remoteViewSuccess: number;
  remoteViewFailures: number;
  successRate: number;
};

export type DiagnosticFinding = {
  severity: "critical" | "warning" | "info";
  code: string;
  title: string;
  detail: string;
  module?: string;
  deviceId?: string;
  evidence?: string[];
  suggestedAction?: string;
};

export type DiagnosticAnalysis = {
  generatedAt: string;
  windowMinutes: number;
  summary: string;
  findings: DiagnosticFinding[];
  counts: Record<string, number>;
  conclusion?: DiagnosticConclusion;
};

export type DiagnosticConclusion = {
  title: string;
  probableCause: string;
  confidence: number;
  checks: Array<{ label: string; ok: boolean }>;
};

export type DiagnosticPipelineNode = {
  id: string;
  label: string;
  layer: string;
  status: "healthy" | "degraded" | "down" | "unknown";
  detail: string;
  metrics?: Record<string, unknown>;
  recentLogs?: DiagnosticEventBrief[];
  recentErrors?: DiagnosticEventBrief[];
};

export type DiagnosticEventBrief = {
  createdAt: string;
  level: string;
  message: string;
  module?: string;
};

export type DiagnosticSessionGroup = {
  sessionId: string;
  deviceId?: string;
  hostname?: string;
  startedAt?: string;
  endedAt?: string;
  durationSec: number;
  status: string;
  reason?: string;
  eventCount: number;
  events?: DiagnosticEventBrief[];
};

export type DiagnosticReproduceRequest = {
  templateId?: string;
  deviceIds: string[];
  durationMinutes?: number;
  autoCapture?: boolean;
};

export type DiagnosticReproduceStatus = {
  jobId: string;
  active: boolean;
  templateId: string;
  deviceIds: string[];
  startedAt: string;
  expiresAt: string;
  autoCapture: boolean;
  captureReady: boolean;
  captureId?: string;
  message?: string;
};

export type DiagnosticPipeline = {
  deviceId: string;
  hostname?: string;
  sessionId?: string;
  generatedAt: string;
  overallStatus: string;
  nodes: DiagnosticPipelineNode[];
  edges: Array<{ from: string; to: string }>;
};

export type DiagnosticCaptureRequest = {
  deviceIds: string[];
  windowMinutes?: number;
  includeAgents?: boolean;
  includeAnalysis?: boolean;
};

export type DiagnosticCaptureResult = {
  captureId: string;
  generatedAt: string;
  snapshot: DiagnosticSnapshot;
  analysis?: DiagnosticAnalysis;
  pipelines?: DiagnosticPipeline[];
  agentSnapshots?: Record<string, unknown>;
  eventCount: number;
};
