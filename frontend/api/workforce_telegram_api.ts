import request from "./request";

const workforceTelegramApi = {
  getSettings: () => request.get<any>("/admin/workforce/telegram/settings"),

  updateSettings: (data: {
    enabled?: boolean;
    botToken?: string;
    timezone?: string;
    dayStart?: string;
    dayEnd?: string;
    nightStart?: string;
    nightEnd?: string;
    graceMinutes?: number;
    graceMinutesAfter?: number;
    dualShiftEnabled?: boolean;
    dailyResetTime?: string;
    dailyResetExecutionOffsetMinutes?: number;
    dailyResetEnabled?: boolean;
    shiftWindowDisabled?: boolean;
    workEndGraceMinutes?: number;
    workEndGraceAfter?: number;
    requirePCLogin?: boolean;
    adminTelegramUserIds?: string;
    botUiLang?: string;
    fineCurrency?: string;
    botMode?: string;
    webhookUrl?: string;
  }) => request.put("/admin/workforce/telegram/settings", data),

  regenerateWorkerSecret: () =>
    request.post<any>("/admin/workforce/telegram/settings/regenerate-secret"),

  regenerateWebhookSecret: () =>
    request.post<any>("/admin/workforce/telegram/settings/regenerate-webhook-secret"),

  registerWebhook: () => request.post<any>("/standalone/webhook"),

  generateWorkerPairing: () =>
    request.post<any>("/admin/workforce/telegram/settings/generate-worker-pairing"),

  getWorkerConnection: () =>
    request.get<any>("/admin/workforce/telegram/worker-connection"),

  listGroups: () => request.get<any>("/admin/workforce/telegram/groups"),

  discoverGroups: () =>
    request.get<any>("/admin/workforce/telegram/groups/discover"),

  upsertGroup: (data: {
    chatId: string;
    chatTitle?: string;
    enabled?: boolean;
    shiftGraceBefore?: number;
    shiftGraceAfter?: number;
    workEndGraceBefore?: number;
    workEndGraceAfter?: number;
    dailyResetTime?: string;
    botUiLang?: string;
  }) => request.post("/admin/workforce/telegram/groups", data),

  deleteGroup: (id: string) => request.delete(`/admin/workforce/telegram/groups/${id}`),

  listBindings: (search?: string) =>
    request.get<any>("/admin/workforce/telegram/bindings", { params: { search } }),

  createBinding: (data: {
    employeeId: string;
    telegramUserId: number;
    telegramUsername?: string;
    chatId?: string;
  }) => request.post("/admin/workforce/telegram/bindings", data),

  deleteBinding: (id: string) => request.delete(`/admin/workforce/telegram/bindings/${id}`),

  resetBindingDaily: (id: string) =>
    request.post<any>(`/admin/workforce/telegram/bindings/${id}/reset-daily`),

  resetEmployeeDaily: (data: { employeeId: string; chatId?: string; deviceId?: string }) =>
    request.post<any>("/admin/workforce/telegram/reset-daily", data),

  resetDeviceBindingDaily: (id: string) =>
    request.post<any>(`/admin/workforce/telegram/device-bindings/${id}/reset-daily`),

  listActivityTypes: () => request.get<any>("/admin/workforce/telegram/activity-types"),

  upsertActivityType: (data: {
    code: string;
    name: string;
    maxTimesPerDay?: number;
    maxConcurrent?: number;
    timeLimitSeconds?: number;
    sortOrder?: number;
    enabled?: boolean;
  }) => request.put("/admin/workforce/telegram/activity-types", data),

  deleteActivityType: (code: string) =>
    request.delete(`/admin/workforce/telegram/activity-types/${encodeURIComponent(code)}`),

  listActivityFines: (activityCode?: string) =>
    request.get<any>("/admin/workforce/telegram/activity-fines", {
      params: activityCode ? { activityCode } : undefined,
    }),

  upsertActivityFine: (data: {
    activityCode: string;
    segmentMinutes: number;
    fineAmount: number;
  }) => request.post("/admin/workforce/telegram/activity-fines", data),

  batchUpsertActivityFines: (data: {
    activityCode?: string;
    activityCodes?: string[];
    items: Array<{ segmentMinutes: number; fineAmount: number }>;
  }) => request.post("/admin/workforce/telegram/activity-fines/batch", data),

  deleteActivityFine: (id: string) =>
    request.delete(`/admin/workforce/telegram/activity-fines/${id}`),

  punchActivityUsage: (employeeId: string) => request.get<any>("/admin/workforce/telegram/punch/usage", {params:{employeeId}}),

  listActivitySessions: (params?: {
    date?: string;
    yearMonth?: string;
    employeeId?: string;
    search?: string;
  }) => request.get<any>("/admin/workforce/telegram/activity-sessions", { params }),

  listWorkSessions: (params?: {
    date?: string;
    yearMonth?: string;
    employeeId?: string;
    search?: string;
  }) => request.get<any>("/admin/workforce/telegram/work-sessions", { params }),

  listHandovers: (params?: { date?: string; search?: string }) =>
    request.get<any>("/admin/workforce/telegram/handovers", { params }),

  listRankings: (params?: { date?: string; metric?: string; shift?: string; chatId?: string }) =>
    request.get<any>("/admin/workforce/telegram/rankings", { params }),

  getHandoverConfig: (chatId: string) =>
    request.get<any>("/admin/workforce/telegram/handover-config", { params: { chatId } }),

  upsertHandoverConfig: (data: { chatId: string; data: Record<string, unknown> }) =>
    request.put("/admin/workforce/telegram/handover-config", data),

  getPushSettings: (chatId: string) =>
    request.get<any>("/admin/workforce/telegram/push-settings", { params: { chatId } }),

  upsertPushSettings: (data: { chatId: string; data: Record<string, unknown> }) =>
    request.put("/admin/workforce/telegram/push-settings", data),

  testPushSettings: (chatId: string) =>
    request.post("/admin/workforce/telegram/push-settings/test", { chatId }),

  getAttendanceReviewSettings: () =>
    request.get<any>("/admin/workforce/telegram/attendance-review/settings"),

  saveAttendanceReviewSettings: (data: { workHours: number; earlyHours: number }) =>
    request.put("/admin/workforce/telegram/attendance-review/settings", data),

  listAttendanceReviewTasks: (params?: { month?: string; search?: string }) =>
    request.get<any>("/admin/workforce/telegram/attendance-review/tasks", { params }),

  confirmAttendanceReviewTask: (id: string, data: { decision: "absent" | "rest_half" | "leave" }) =>
    request.post<any>(`/admin/workforce/telegram/attendance-review/tasks/${id}/confirm`, data),

  punchCheckIn: (data: { employeeId?: string; shift?: "day" | "night" }) =>
    request.post<any>("/admin/workforce/telegram/punch/checkin", data),

  punchCheckOut: (data: { employeeId?: string; shift?: "day" | "night" }) =>
    request.post<any>("/admin/workforce/telegram/punch/checkout", data),

  punchActivityStart: (data: { employeeId?: string; activity: string }) =>
    request.post<any>("/admin/workforce/telegram/punch/activity/start", data),

  punchActivityBack: (data: { employeeId?: string }) =>
    request.post<any>("/admin/workforce/telegram/punch/activity/back", data),

  getPunchMe: () =>
    request.get<any>("/admin/workforce/telegram/punch/me"),

  listWorkFines: () => request.get<any>("/admin/workforce/telegram/work-fines"),

  upsertWorkFine: (data: { checkinType: string; segmentMinutes: number; fineAmount: number }) =>
    request.post("/admin/workforce/telegram/work-fines", data),

  batchUpsertWorkFines: (data: {
    checkinType?: string;
    checkinTypes?: string[];
    items: Array<{ segmentMinutes: number; fineAmount: number }>;
  }) => request.post("/admin/workforce/telegram/work-fines/batch", data),

  deleteWorkFine: (id: string) => request.delete(`/admin/workforce/telegram/work-fines/${id}`),

  listSharedWorkstations: (deviceId?: string) =>
    request.get<any>("/admin/workforce/telegram/shared-workstations", {
      params: deviceId ? { deviceId } : undefined,
    }),

  addSharedWorkstationMember: (data: { deviceId: string; employeeId: string }) =>
    request.post("/admin/workforce/telegram/shared-workstations", data),

  removeSharedWorkstationMember: (data: { deviceId: string; employeeId: string }) =>
    request.delete("/admin/workforce/telegram/shared-workstations", { data }),

  listDeviceBindings: (search?: string) =>
    request.get<any>("/admin/workforce/telegram/device-bindings", { params: { search } }),

  deleteDeviceBinding: (id: string) =>
    request.delete(`/admin/workforce/telegram/device-bindings/${id}`),

  exportAttendance: (params?: { dateFrom?: string; dateTo?: string; month?: string }) =>
    request.get("/admin/workforce/telegram/export", { params, responseType: "blob" }),

  runDailyReset: (data?: { exportBusinessToday?: boolean }) =>
    request.post<any>("/admin/workforce/telegram/daily-reset", data ?? {}),
};

export default workforceTelegramApi;
