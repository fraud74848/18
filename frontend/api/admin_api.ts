// frontend/api/admin_api.ts
import request from "./request";

export interface SalaryPositionCap {
  position: string;
  capAmount: number;
}

export interface SalaryWorkConfig {
  configKey: string;
  configName: string;
  workMode?: string;
  totalIncrease: number;
  fullAttendanceDays: number;
  fullAttendanceAmount: number;
  fullAttendanceCurrency?: string;
  exchangeRate: number;
  cnyThbRate?: number;
  cnyUsdRate?: number;
  thbUsdRate?: number;
  performanceFullScoreAmount: number;
  performanceCurrency?: string;
  protectionFee: number;
  protectionFeeCurrency?: string;
  payoutCurrency?: string;
  positionCaps?: SalaryPositionCap[];
  isBuiltin?: boolean;
  sortOrder?: number;
  updatedAt?: string;
}

export interface AgentNotificationSettings {
  enabled: boolean;
  salaryPaidEnabled: boolean;
  penaltyEnabled: boolean;
  performanceEnabled: boolean;
  attendanceEnabled: boolean;
  activityEnabled: boolean;
  defaultDurationSec: number;
  startupEnabled: boolean;
  localLoginEnabled: boolean;
  localAttendanceEnabled: boolean;
  localBindEnabled: boolean;
  localDurationSec: number;
  updatedAt?: string;
}

export interface SalaryCalculateRow {
  id?: string;
  employeeId: string;
  employeeName: string;
  position?: string;
  hireDateDept?: string;
  workLocation?: string;
  workMode?: string;
  monthsEmployed?: number;
  lastMonthBase?: number;
  totalIncrease?: number;
  baseSalary?: number;
  attendanceDays?: number;
  actualBaseSalary?: number;
  attendanceAdjustment?: number;
  halfYearUnpaidLeave?: number;
  exchangeRate?: number;
  exchangeRateLabel?: string;
  performanceScore?: number;
  performanceBonusB?: number;
  reward?: number;
  penalty?: number;
  advance?: number;
  otherDeduction?: number;
  protectionReturn?: number;
  protectionFee?: number;
  totalSalary?: number;
  payoutCurrency?: string;
  remark?: string;
  yearMonth?: string;
  manualFields?: string[];
}

const adminApi = {
  // ========== 考勤管理 ==========
  getEmployees: (params?: {
    skip?: number;
    limit?: number;
    search?: string;
    device_bound?: "true" | "false";
    employment_scope?: "all" | "month_visible";
    year_month?: string;
    performance_eligibility?: "true" | "false";
    performance_hire_cutoff_day?: number;
  }) => request.get<any>("/admin/employees", { params }),

  createEmployee: (data: any) => request.post("/admin/employees", data),

  createEmployeeAccount: (data: any) => request.post("/admin/employee-accounts", data),

  /** 出款统计模块的员工账号（与考勤子账号 createEmployeeAccount 不同） */
  createSiteEmployeeAccount: (data: {
    site_id: string;
    name: string;
    account_name: string;
    shift: string;
  }) => request.post("/admin/site-stats/employee-accounts", data),

  listInviteCodes: (employeeId?: string) =>
    request.get("/admin/invite-codes", { params: { employee_id: employeeId } }),

  regenerateInviteCode: (employeeId: string) =>
    request.post(`/admin/employees/${employeeId}/invite-code/regenerate`),

  updateEmployee: (id: string, data: any) =>
    request.put(`/admin/employees/${id}`, data),

  deleteEmployee: (id: string) => request.delete(`/admin/employees/${id}`),

  getAttendanceRecords: (yearMonth: string, employeeIds: string[]) =>
    request.get("/admin/attendance/records", {
      params: { year_month: yearMonth, employee_ids: employeeIds },
    }),

  saveAttendanceRecords: (data: { year_month: string; data: any }) =>
    request.post("/admin/attendance/records", data),

  getRecordsByEmployees: (yearMonth: string, employeeIds: string[]) =>
    request.get("/admin/attendance/records", {
      params: { year_month: yearMonth, employee_ids: employeeIds },
    }),

  saveRecords: (data: {
    year_month: string;
    data: any;
    clears?: Record<string, string[]>;
    restoreEmployees?: string[];
  }) => request.post("/admin/attendance/records", data),

  importAttendanceRecords: (formData: FormData) =>
    request.post("/admin/attendance/records/import", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  getTodayAbsentees: () => request.get("/admin/attendance/today-absentees"),

  getTodayAttendanceRate: () => request.get("/admin/attendance/today-rate"),

  // ========== 绩效管理 ==========
  getPerformance: (params: {
    month: string;
    employee_id?: string;
    employment_scope?: "all" | "month_visible";
    year_month?: string;
    performance_hire_cutoff_day?: number;
  }) =>
    request.get("/admin/performance", { params }),

  getPerformanceRuleSettings: () =>
    request.get<{ hireCutoffDay: number }>("/admin/performance/rule-settings"),

  savePerformanceRuleSettings: (data: { hireCutoffDay: number }) =>
    request.put<{ hireCutoffDay: number }>("/admin/performance/rule-settings", data),

  batchSavePerformance: (data: { month: string; items: any[] }) =>
    request.post("/admin/performance/batch", data),

  updatePerformance: (
    employeeId: string,
    month: string,
    data: {
      total_score?: number;
      grade?: string;
      score_records?: any[];
    },
  ) => request.put(`/admin/performance/${employeeId}/${month}`, data),

  // ✅ 新增：单条绩效删除
  deletePerformance: (employeeId: string, month: string) =>
    request.delete(`/admin/performance/${employeeId}/${month}`),

  getPerformancePushSettings: () =>
    request.get("/admin/performance/push-settings"),

  savePerformancePushEnabled: (enabled: boolean) =>
    request.put("/admin/performance/push-settings/enabled", { enabled }),

  savePerformancePushToken: (botToken: string) =>
    request.put("/admin/performance/push-settings/token", { botToken }),

  discoverPerformancePushChats: () =>
    request.post("/admin/performance/push-settings/discover"),

  bindPerformancePushChat: (data: {
    chatId: string;
    chatTitle?: string;
    chatType?: string;
  }) => request.put("/admin/performance/push-settings/bind", data),

  unbindPerformancePushChat: () =>
    request.delete("/admin/performance/push-settings/bind"),

  testPerformancePush: () =>
    request.post("/admin/performance/push-settings/test"),

  pushPerformanceNotify: (data: {
    date: string;
    employeeId?: string;
    employeeName: string;
    position?: string;
    score: number;
    reason: string;
  }) => request.post("/admin/performance/push-notify", data),

  getPenaltyPushSettings: () =>
    request.get("/admin/penalty/push-settings"),

  savePenaltyPushEnabled: (enabled: boolean) =>
    request.put("/admin/penalty/push-settings/enabled", { enabled }),

  savePenaltyPushToken: (botToken: string) =>
    request.put("/admin/penalty/push-settings/token", { botToken }),

  discoverPenaltyPushChats: () =>
    request.post("/admin/penalty/push-settings/discover"),

  bindPenaltyPushChat: (data: {
    chatId: string;
    chatTitle?: string;
    chatType?: string;
  }) => request.put("/admin/penalty/push-settings/bind", data),

  unbindPenaltyPushChat: () =>
    request.delete("/admin/penalty/push-settings/bind"),

  testPenaltyPush: () =>
    request.post("/admin/penalty/push-settings/test"),

  pushPenaltyNotify: (data: {
    date: string;
    employeeId?: string;
    employeeName: string;
    position?: string;
    amount: number;
    category: string;
    reason: string;
  }) => request.post("/admin/penalty/push-notify", data),

  getAgentNotificationSettings: () =>
    request.get<AgentNotificationSettings>("/admin/agent-notification/settings"),

  saveAgentNotificationSettings: (data: AgentNotificationSettings) =>
    request.put<AgentNotificationSettings>("/admin/agent-notification/settings", data),

  // ========== 罚款管理 ==========
  getPenaltyRecords: (params: {
    month?: string;
    employee_id?: string;
    page?: number;
    page_size?: number;
  }) => request.get("/admin/penalty/records", { params }),

  createPenaltyRecord: (data: any) =>
    request.post("/admin/penalty/record", data),

  deletePenaltyRecord: (id: string) =>
    request.delete(`/admin/penalty/records/${id}`),

  updatePenaltyRecord: (
    id: string,
    data: {
      amount: number;
      category: string;
      reason: string;
      penalty_date: string;
    },
  ) => request.put(`/admin/penalty/records/${id}`, data),

  // ========== 出款站点管理 ==========
  getSites: (params?: { is_active?: boolean }) =>
    request.get("/admin/site-stats/sites", { params }),

  createSite: (data: any) => request.post("/admin/site-stats/sites", data),

  updateSite: (id: string, data: any) =>
    request.put(`/admin/site-stats/sites/${id}`, data),

  deleteSite: (id: string) => request.delete(`/admin/site-stats/sites/${id}`),

  // ========== 员工账号管理 ==========
  getSiteEmployeeAccounts: (params?: {
    site_id?: string;
    name?: string;
    account_name?: string;
    skip?: number;
    limit?: number;
    shift?: string;
  }) => request.get("/admin/site-stats/employee-accounts", { params }),

  updateSiteEmployeeAccount: (id: string, data: any) =>
    request.put(`/admin/site-stats/employee-accounts/${id}`, data),

  batchUpdateSiteEmployeeAccountShift: (data: {
    ids?: string[];
    name?: string;
    shift: "day" | "night";
  }) => request.post("/admin/site-stats/employee-accounts/batch-shift", data),

  deleteSiteEmployeeAccount: (id: string) =>
    request.delete(`/admin/site-stats/employee-accounts/${id}`),

  importSiteEmployeeAccounts: (formData: FormData) =>
    request.post("/admin/site-stats/employee-accounts/import", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  // ========== 出款统计 ==========
  previewSiteStatsUpload: (formData: FormData) =>
    request.post("/admin/site-stats/upload/preview", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  uploadSiteStats: (formData: FormData) =>
    request.post("/admin/site-stats/upload", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  getSiteStatsSummary: (params: {
    site_id?: string;
    employee_account_id?: string;
    start_date?: string;
    end_date?: string;
    shift?: string;
  }) => request.get("/admin/site-stats/summary", { params }),

  getSiteStatsStacked: (params: {
    site_id?: string;
    employee_account_id?: string;
    start_date?: string;
    end_date?: string;
    shift?: string;
  }) => request.get("/admin/site-stats/stacked-summary", { params }),
  clearSiteStatsByDate: (siteId: string, shift: string, date: string) =>
    request.delete("/admin/site-stats/data/clear-by-date", {
      params: { site_id: siteId, shift, date },
    }),
  clearAllSiteStats: () => request.delete("/admin/site-stats/data/clear"),
  // 删除指定日期的所有数据（不限站点、不限班次）
  clearSiteStatsByDateOnly: (date: string) =>
    request.delete("/admin/site-stats/clear-by-date-only", {
      params: { date },
    }),
  clearSiteStatsByDateAndShift: (date: string, shift: string) =>
    request.delete("/admin/site-stats/clear-by-date-shift", {
      params: { date, shift },
    }),

  // ========== 工资管理 ==========
  getSalaryViewGateStatus: () =>
    request.get<{ configured: boolean }>("/salary/view-gate/status"),

  setupSalaryViewPassword: (data: { password: string }) =>
    request.post<{ unlockToken: string }>("/salary/view-gate/setup", data),

  verifySalaryViewPassword: (data: { password: string }) =>
    request.post<{ unlockToken: string }>("/salary/view-gate/verify", data),

  changeSalaryViewPassword: (data: {
    oldPassword: string;
    newPassword: string;
  }) =>
    request.post<{ unlockToken: string; expiresAt?: string }>(
      "/salary/view-gate/change-password",
      data,
    ),

  uploadSalary: (formData: FormData) =>
    request.post("/salary/upload", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  previewSalaryUpload: (formData: FormData) =>
    request.post("/salary/upload/preview", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  getSalaryRecords: (params: {
    yearMonth?: string;
    employeeId?: string;
    employeeName?: string;
  }) => request.get("/salary/records", { params }),

  updateSalaryRecord: (id: string, data: any) =>
    request.put(`/salary/records/${id}`, data),

  deleteSalaryRecord: (id: string) => request.delete(`/salary/records/${id}`),

  markSalaryPaid: (data: { id?: string; employeeId?: string; yearMonth?: string }) =>
    request.post("/salary/records/mark-paid", data),

  markAllSalaryPaid: (data: { yearMonth: string }) =>
    request.post("/salary/records/mark-all-paid", data),

  uploadSalaryPaymentProof: (formData: FormData) =>
    request.post("/salary/payment-proofs", formData, {
      headers: { "Content-Type": "multipart/form-data" },
    }),

  getSalaryPaymentProof: (params: { employeeId: string; yearMonth: string }) =>
    request.get("/salary/payment-proofs", { params }),

  fetchSalaryPaymentProofFile: (id: string) =>
    request.get<Blob>(`/salary/payment-proofs/${id}/file`, {
      responseType: "blob",
    }),

  getSalaryMonths: (params?: { employeeId?: string }) =>
    request.get<string[]>("/salary/months", { params }),

  getMyPaymentAddress: () => request.get("/salary/payment-address/me"),

  upsertMyPaymentAddress: (data: {
    accountHolder?: string;
    bankName?: string;
    bankAccount?: string;
    usdtNetwork?: string;
    usdtAddress?: string;
    otherWalletType?: string;
    otherWalletAddress?: string;
    preferredMethod?: string;
    remark?: string;
    notificationEmail?: string;
    totpCode: string;
  }) => request.put("/salary/payment-address/me", data),

  listPaymentAddresses: (params: {
    skip?: number;
    limit?: number;
    search?: string;
  }) => request.get("/salary/payment-addresses", { params }),

  getPaymentAddressSalaryYear: (employeeUuid: string, params?: { year?: number }) =>
    request.get(`/salary/payment-addresses/${employeeUuid}/salary-year`, { params }),

  getEmployeePaymentAddress: (employeeUuid: string) =>
    request.get(`/salary/payment-addresses/${employeeUuid}`),

  getSalaryConfigs: () => request.get<{ items: SalaryWorkConfig[] }>("/salary/config"),

  createSalaryConfig: (data: { configName: string }) =>
    request.post<SalaryWorkConfig>("/salary/config", data),

  upsertSalaryConfig: (data: SalaryWorkConfig) =>
    request.put("/salary/config", data),

  deleteSalaryConfig: (configKey: string) =>
    request.delete(`/salary/config/${encodeURIComponent(configKey)}`),

  getSalaryCalculateDraft: (yearMonth: string) =>
    request.get<{ items: SalaryCalculateRow[]; configs: SalaryWorkConfig[] }>(
      "/salary/calculate/draft",
      { params: { yearMonth } },
    ),

  recalculateSalaryRows: (data: { yearMonth: string; items: SalaryCalculateRow[] }) =>
    request.post<{ items: SalaryCalculateRow[] }>("/salary/calculate/recalculate", data),

  saveSalaryCalculateRows: (data: { yearMonth: string; items: SalaryCalculateRow[] }) =>
    request.post<{ saved: number }>("/salary/calculate/save", data),

  // ========== 操作日志管理 ==========
  getOperationLogs: (params: {
    page?: number;
    pageSize?: number;
    operatorName?: string;
    operationModule?: string;
    operationType?: string;
    startDate?: string;
    endDate?: string;
    includeTechnical?: string;
  }) => request.get("/operation-logs", { params }),

  getOperationModules: () => request.get<string[]>("/operation-logs/modules"),

  getOperationTypes: () => request.get<string[]>("/operation-logs/types"),

  exportOperationLogs: (params: {
    operatorName?: string;
    operationModule?: string;
    startDate?: string;
    endDate?: string;
    includeTechnical?: string;
  }) => request.get("/operation-logs/export", { params, responseType: "blob" }),

  deleteOperationLogs: (data: {
    ids?: string[];
    daysOld?: number;
    deleteAll?: boolean;
  }) => request.delete("/operation-logs", { data }),
};

export default adminApi;
