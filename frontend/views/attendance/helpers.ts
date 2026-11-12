export function getCurrentYearMonth() {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
}

export function getToday() {
  const now = new Date();
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

export function formatDate(date: string | Date | null | undefined) {
  if (!date) return "-";
  const str = String(date);
  if (str.includes("-")) return str.substring(0, 10);
  const d = new Date(date);
  if (isNaN(d.getTime())) return "-";
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(
    d.getDate(),
  ).padStart(2, "0")}`;
}

export function formatDateShort(dateStr: string | Date | null | undefined) {
  if (!dateStr) return "";
  if (typeof dateStr === "string" && dateStr.includes("-")) {
    return dateStr.slice(5);
  }
  if (dateStr instanceof Date) {
    const month = String(dateStr.getMonth() + 1).padStart(2, "0");
    const day = String(dateStr.getDate()).padStart(2, "0");
    return `${month}-${day}`;
  }
  return String(dateStr);
}

export function getAttendanceRateType(rate: number) {
  if (rate >= 90) return "success";
  if (rate >= 70) return "warning";
  return "danger";
}

export function normalizeAttendanceStatus(status: string) {
  if (!status) return "";
  if (status === "working") return "work";
  if (status === "pending") return "";
  return status;
}

export function formatAttendanceTime(v: string | Date | null | undefined) {
  if (!v) return "-";
  const d = v instanceof Date ? v : new Date(v);
  if (Number.isNaN(d.getTime())) return String(v);
  return d.toLocaleString("zh-CN", {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatAttendanceClockTime(v: string | Date | null | undefined) {
  if (!v) return "-";
  const d = v instanceof Date ? v : new Date(v);
  if (Number.isNaN(d.getTime())) return String(v);
  return d.toLocaleTimeString("zh-CN", {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatWorkDuration(
  start: string | Date | null | undefined,
  end: string | Date | null | undefined,
) {
  if (!start || !end) return "-";
  const a = start instanceof Date ? start : new Date(start);
  const b = end instanceof Date ? end : new Date(end);
  if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return "-";
  const sec = Math.max(0, Math.floor((b.getTime() - a.getTime()) / 1000));
  return formatDurationSeconds(sec);
}

/** Format elapsed seconds as 工作时长 style (小时/分钟/秒). */
export function formatDurationSeconds(secondsRaw: number) {
  const sec = Math.max(0, Math.floor(secondsRaw || 0));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (h > 0 && m > 0) return `${h}小时${m}分钟`;
  if (h > 0) return `${h}小时`;
  if (m > 0) return `${m}分钟`;
  return `${sec}秒`;
}

export function workDurationSeconds(
  start: string | Date | null | undefined,
  end: string | Date | null | undefined,
) {
  if (!start || !end) return 0;
  const a = start instanceof Date ? start : new Date(start);
  const b = end instanceof Date ? end : new Date(end);
  if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return 0;
  return Math.max(0, Math.floor((b.getTime() - a.getTime()) / 1000));
}

export function getStatusClass(status: string) {
  const normalized = normalizeAttendanceStatus(status);
  const map: Record<string, string> = {
    work: "status-work",
    late: "status-late",
    rest_half: "status-rest-half",
    rest_full: "status-rest-full",
    leave: "status-leave",
    absent: "status-absent",
    off_post: "status-off-post",
    resigned: "status-resigned",
  };
  return map[normalized] || "";
}

export function getStatusText(status: string) {
  const normalized = normalizeAttendanceStatus(status);
  const map: Record<string, string> = {
    work: "出勤",
    late: "迟到",
    rest_half: "半休",
    rest_full: "全休",
    leave: "半假",
    absent: "旷工",
    off_post: "全假",
    resigned: "离职",
    "": "-",
  };
  return map[normalized] || "-";
}

export function getPenaltyCategoryType(category: string) {
  const map: Record<string, string> = {
    迟到: "warning",
    早退: "warning",
    小厕超时: "danger",
    大厕超时: "danger",
    吃饭超时: "danger",
    抽烟或休息超时: "danger",
    旷工: "danger",
    其他: "info",
  };
  return map[category] || "info";
}

export function getScoreType(score: number) {
  if (score >= 15) return "success"; // 15分以上 - 卓越（绿色）
  if (score >= 12) return "primary"; // 12-14.9分 - 优秀（蓝色）
  if (score === 10) return "success"; // 10分 - 良好（绿色）
  if (score >= 7) return "warning"; // 7-9.9分 - 合格（橙色）
  if (score >= 6) return "danger"; // 6-6.9分 - 待提升（红色）
  return "danger"; // 6分以下 - 不合格（深红色）
}

export function getGradeType(grade: string) {
  const map: Record<string, string> = {
    优秀: "success", // 绿色
    满分: "success", // 绿色（也可以改为 primary 蓝色）
    良好: "primary", // 蓝色
    合格: "warning", // 橙色
    待提升: "danger", // 红色
    不合格: "danger", // 红色
  };
  return map[grade] || "info";
}

export function getScoreRecordOperator(record: any): string {
  const name = record?.operator || record?.operatorName;
  return name && String(name).trim() ? String(name).trim() : "-";
}

export function getPenaltyCreatorName(record: any): string {
  const name =
    record?.createdByName ||
    record?.created_by_name ||
    record?.created_by ||
    record?.createdBy;
  return name && String(name).trim() ? String(name).trim() : "-";
}
