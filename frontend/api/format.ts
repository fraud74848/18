// frontend/api/format.ts
import dayjs from "dayjs";
import utc from "dayjs/plugin/utc";
import timezone from "dayjs/plugin/timezone";

// 扩展 dayjs 插件
dayjs.extend(utc);
dayjs.extend(timezone);

// 设置默认时区为北京时间
dayjs.tz.setDefault("Asia/Shanghai");

/**
 * 清洗活动标题中的控制字符与乱码
 */
const looksGarbled = (text: string): boolean => {
  if (text.length < 2) return false;
  let suspicious = 0;
  for (const ch of text) {
    const code = ch.charCodeAt(0);
    if (
      code === 0xfffd ||
      code < 0x20 ||
      (code >= 0xe000 && code <= 0xf8ff)
    ) {
      suspicious++;
    }
  }
  return suspicious * 3 >= text.length;
};

export const sanitizeDisplayText = (value?: string | null): string => {
  if (!value) return "";
  const cleaned = value
    .replace(/[\u0000-\u001F\u007F-\u009F\uFFFD]/g, "")
    .replace(/\s+/g, " ")
    .trim();
  if (!cleaned || looksGarbled(cleaned)) return "";
  return cleaned;
};

export const formatActivityTitle = (activity: {
  windowTitle?: string | null;
  browserTitle?: string | null;
}): string => {
  const browser = sanitizeDisplayText(activity.browserTitle);
  const window = sanitizeDisplayText(activity.windowTitle);
  if (browser && window && browser !== window) {
    return `${browser} · ${window}`;
  }
  return browser || window || "无标题";
};

/**
 * 格式化文件大小
 * @param bytes 字节数
 * @param decimals 小数位数
 * @returns 格式化后的文件大小字符串
 */
export const formatBytes = (bytes: number, decimals: number = 2): string => {
  if (bytes === 0) return "0 B";
  if (!isFinite(bytes) || bytes < 0) return "0 B";

  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const value = bytes / Math.pow(k, i);
  const formattedValue = value.toFixed(Math.min(decimals, 2));

  // 移除末尾的 .00
  const finalValue = formattedValue.endsWith(".00")
    ? formattedValue.slice(0, -3)
    : formattedValue;

  return `${finalValue} ${sizes[i]}`;
};

/**
 * 格式化日期时间（完整）- 使用北京时间
 * @param time ISO 8601 时间字符串
 * @returns 格式化的日期时间，如 "2024-01-15 14:30:25"
 */
export const formatDateTime = (time: string | null | undefined): string => {
  if (!time) return "-";

  const parsed = dayjs.utc(time);
  if (!parsed.isValid()) return "-";

  return parsed.tz("Asia/Shanghai").format("YYYY-MM-DD HH:mm:ss");
};

/**
 * 格式化日期（仅日期）- 使用北京时间
 * @param time ISO 8601 时间字符串
 * @returns 格式化的日期，如 "2024-01-15"
 */
export const formatDate = (time: string | null | undefined): string => {
  if (!time) return "-";

  const parsed = dayjs.utc(time);
  if (!parsed.isValid()) return "-";

  return parsed.tz("Asia/Shanghai").format("YYYY-MM-DD");
};

/**
 * 格式化时间（仅时间）- 使用北京时间
 * @param time ISO 8601 时间字符串
 * @returns 格式化的时间，如 "14:30:25"
 */
export const formatTime = (time: string | null | undefined): string => {
  if (!time) return "-";

  const parsed = dayjs.utc(time);
  if (!parsed.isValid()) return "-";

  return parsed.tz("Asia/Shanghai").format("HH:mm:ss");
};

/**
 * 获取当前北京时间字符串
 * @returns 当前北京时间，格式为 "YYYY-MM-DD HH:mm:ss"
 */
export const getCurrentBeijingTime = (): string => {
  return dayjs().tz("Asia/Shanghai").format("YYYY-MM-DD HH:mm:ss");
};

/**
 * 获取相对时间描述
 * @param dateStr ISO 8601 时间字符串
 * @returns 相对时间描述，如 "5分钟前"、"2小时前"
 */
export const getRelativeTime = (dateStr: string): string => {
  if (!dateStr) return "-";

  const parsed = dayjs.utc(dateStr);
  if (!parsed.isValid()) return "-";

  const now = dayjs().tz("Asia/Shanghai");
  const target = parsed.tz("Asia/Shanghai");

  const diffSec = now.diff(target, "second");
  const diffMin = now.diff(target, "minute");
  const diffHour = now.diff(target, "hour");
  const diffDay = now.diff(target, "day");
  const diffMonth = now.diff(target, "month");
  const diffYear = now.diff(target, "year");

  if (diffSec < 10) return "刚刚";
  if (diffSec < 60) return `${diffSec}秒前`;
  if (diffMin < 60) return `${diffMin}分钟前`;
  if (diffHour < 24) return `${diffHour}小时前`;
  if (diffDay < 7) return `${diffDay}天前`;
  if (diffDay < 30) return `${Math.floor(diffDay / 7)}周前`;
  if (diffDay < 365) return `${diffMonth}个月前`;
  return `${diffYear}年前`;
};

/**
 * 获取状态标签类型（用于 Element Plus el-tag）
 * @param status 状态值
 * @returns Element Plus 标签类型
 */
export const getStatusType = (
  status: string,
): "success" | "warning" | "danger" | "info" | "primary" => {
  const map: Record<
    string,
    "success" | "warning" | "danger" | "info" | "primary"
  > = {
    // 设备状态
    online: "success",
    offline: "info",
    pending: "warning",
    // 用户状态
    active: "success",
    inactive: "danger",
    disabled: "danger",
    // 任务状态
    completed: "success",
    running: "warning",
    failed: "danger",
    // 上传状态
    uploaded: "success",
    uploading: "warning",
    // 通用
    success: "success",
    error: "danger",
    warning: "warning",
  };
  return map[status] || "info";
};

/**
 * 获取状态文本
 * @param status 状态值
 * @returns 中文状态文本
 */
export const getStatusText = (status: string): string => {
  const map: Record<string, string> = {
    // 设备状态
    online: "在线",
    offline: "离线",
    pending: "待审核",
    // 用户状态
    active: "启用",
    inactive: "禁用",
    disabled: "禁用",
    // 任务状态
    completed: "已完成",
    running: "运行中",
    failed: "失败",
    // 上传状态
    uploaded: "已上传",
    uploading: "上传中",
    // 通用
    success: "成功",
    error: "失败",
    warning: "警告",
  };
  return map[status] || status;
};

/**
 * 格式化时长（秒数转可读格式）
 * @param seconds 秒数
 * @returns 格式化的时长，如 "2小时30分钟"
 */
export const formatDuration = (seconds: number): string => {
  if (seconds <= 0) return "0秒";
  if (!isFinite(seconds)) return "0秒";

  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;

  const parts: string[] = [];
  if (hours > 0) parts.push(`${hours}小时`);
  if (minutes > 0) parts.push(`${minutes}分钟`);
  if (secs > 0 || parts.length === 0) parts.push(`${secs}秒`);

  return parts.join("");
};

/**
 * 截断文本
 * @param text 原始文本
 * @param maxLength 最大长度
 * @param suffix 后缀
 * @returns 截断后的文本
 */
export const truncateText = (
  text: string,
  maxLength: number = 50,
  suffix: string = "...",
): string => {
  if (!text) return "";
  if (text.length <= maxLength) return text;
  return text.slice(0, maxLength) + suffix;
};

/**
 * 格式化设备ID（显示前8位）
 * @param deviceId 完整设备ID
 * @returns 格式化的设备ID
 */
export const formatDeviceId = (deviceId: string): string => {
  if (!deviceId) return "-";
  if (deviceId.length <= 8) return deviceId;
  return deviceId.slice(0, 8) + "...";
};

/**
 * 格式化百分比
 * @param value 数值（0-1 或 0-100）
 * @param isDecimal 是否为小数（0-1），默认 false
 * @param decimals 小数位数
 * @returns 格式化的百分比
 */
export const formatPercent = (
  value: number,
  isDecimal: boolean = false,
  decimals: number = 1,
): string => {
  if (!isFinite(value)) return "0%";
  let percent: number;
  if (isDecimal) {
    percent = value * 100;
  } else {
    percent = value;
  }
  percent = Math.min(100, Math.max(0, percent));
  return percent.toFixed(decimals) + "%";
};

/**
 * 格式化数字（添加千分位分隔符）
 * @param num 数字
 * @returns 格式化的数字，如 "1,234,567"
 */
export const formatNumber = (num: number): string => {
  if (!isFinite(num)) return "0";
  return num.toLocaleString("zh-CN");
};

/**
 * 格式化 ISO 8601 时间为指定格式（使用北京时间）
 * @param isoString ISO 8601 字符串
 * @param format 格式类型
 * @returns 格式化后的时间字符串
 */
export const formatISOString = (
  isoString: string | null | undefined,
  format: "datetime" | "date" | "time" | "relative" = "datetime",
): string => {
  if (!isoString) return "-";

  switch (format) {
    case "datetime":
      return formatDateTime(isoString);
    case "date":
      return formatDate(isoString);
    case "time":
      return formatTime(isoString);
    case "relative":
      return getRelativeTime(isoString);
    default:
      return formatDateTime(isoString);
  }
};

/**
 * 调试：打印时间转换信息
 */
export const debugTimeConversion = (
  localTime: string,
  utcTime: string,
): void => {
  if (import.meta.env.DEV) {
    console.log(`[Time] Local: ${localTime} -> UTC: ${utcTime}`);
  }
};

/**
 * 将本地时间转换为UTC（用于API请求）
 */
export const localToUTC = (localTime: string): string => {
  if (!localTime) return "";
  return dayjs.tz(localTime, "Asia/Shanghai").utc().format();
};

/**
 * 将UTC时间转换为本地显示（已存在formatDateTime）
 */
// formatDateTime 已存在，无需重复
