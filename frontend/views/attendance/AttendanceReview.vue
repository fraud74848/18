<template>
  <div class="attendance-management">
    <div class="bg-decoration">
      <div class="blob blob-1"></div>
      <div class="blob blob-2"></div>
      <div class="blob blob-3"></div>
    </div>
    <div class="attendance-management">
      <el-card class="penalty-card" shadow="never">
        <template #header>
          <div class="card-header">
            <div class="header-title">
              <div class="title-icon review-title-icon">
                <el-icon><Clock /></el-icon>
              </div>
              <span class="title-text">待确认</span>
              <span
                class="pending-count-capsule"
                :class="pendingCount > 0 ? 'has-pending' : 'all-done'"
              >
                {{ pendingCount > 0 ? `${pendingCount} 条待处理` : "已全部处理" }}
              </span>
            </div>
            <div class="header-actions">
              <div class="search-wrapper">
                <el-input
                  v-model="searchEmployee"
                  placeholder="搜索员工姓名"
                  clearable
                  style="width: 200px"
                  :prefix-icon="Search"
                  class="search-input"
                />
              </div>
              <div class="date-wrapper">
                <el-date-picker
                  v-model="reviewMonth"
                  type="month"
                  placeholder="选择月份"
                  format="YYYY年MM月"
                  value-format="YYYY-MM"
                  @change="loadAttendanceReviewTasks"
                  style="width: 170px"
                  class="month-picker"
                  :clearable="false"
                />
              </div>
              <el-button 
                @click="handleRefresh" 
                :loading="refreshing" 
                class="action-btn action-btn-ghost"
              >
                <el-icon><Refresh /></el-icon>
                <span>刷新</span>
              </el-button>
              <el-button
                v-if="canEditSettings"
                @click="openReviewSettingsDialog"
                class="action-btn action-btn-primary"
              >
                <el-icon><Setting /></el-icon>
                <span>设置</span>
              </el-button>
            </div>
          </div>
        </template>

        <!-- 统计卡片 - 优化样式 -->
        <el-row :gutter="16" class="penalty-stats-row">
          <el-col :span="8" v-for="stat in reviewStatCards" :key="stat.key">
            <div class="stat-card-modern" :class="`stat-${stat.key}`">
              <div class="stat-content-modern">
                <div class="stat-icon-modern" :style="{ background: stat.iconBg, color: stat.iconColor }">
                  <el-icon><component :is="stat.icon" /></el-icon>
                </div>
                <div class="stat-info-modern">
                  <div class="stat-value-modern">{{ stat.value }}</div>
                  <div class="stat-label-modern">{{ stat.label }}</div>
                </div>
              </div>
            </div>
          </el-col>
        </el-row>

        <!-- 提示条 - 优化样式 -->
        <div class="review-tip-modern">
          <div class="tip-icon-wrap">
            <el-icon class="tip-icon"><Warning /></el-icon>
          </div>
          <div class="tip-text-wrap">
            <span class="tip-label">规则：</span>
            <span class="tip-content">
              上班迟到 ≥ <strong>{{ reviewSettings.workHours }}</strong> 小时 或 提前下班 ≥
              <strong>{{ reviewSettings.earlyHours }}</strong> 小时 进入待确认；
              未确认前不记罚款、不改考勤状态。
            </span>
          </div>
          <el-button
            v-if="canEditSettings"
            link
            type="primary"
            size="small"
            class="tip-edit-btn"
            @click="openReviewSettingsDialog"
          >
            调整阈值
          </el-button>
        </div>

        <!-- 表格 - 优化样式 -->
        <el-table
          :data="filteredReviewTasks"
          v-loading="reviewTasksLoading"
          stripe
          border
          style="width: 100%"
          class="modern-table-enhanced"
          empty-text="本月暂无待处理异常"
        >
          <el-table-column type="index" width="56" label="#" align="center">
            <template #default="{ $index }">
              <span class="row-index">{{ $index + 1 }}</span>
            </template>
          </el-table-column>
          <el-table-column label="员工姓名" min-width="150" align="center">
            <template #default="{ row }">
              <div class="employee-cell-modern">
                <el-avatar
                  :size="28"
                  class="employee-avatar-modern"
                  :style="{
                    background: getAvatarColor(row.employeeName || row.employee_name),
                  }"
                >
                  {{
                    (row.employeeName || row.employee_name)?.charAt(0)?.toUpperCase() || "?"
                  }}
                </el-avatar>
                <span class="employee-name-capsule">{{
                  row.employeeName || row.employee_name || "-"
                }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="岗位" width="120" align="center">
            <template #default="{ row }">
              <span class="position-capsule">{{ row.position || "-" }}</span>
            </template>
          </el-table-column>
          <el-table-column label="日期" width="150" align="center">
            <template #default="{ row }">
              <span class="date-capsule">{{ row.attendanceDate || "-" }}</span>
            </template>
          </el-table-column>
          <el-table-column label="班次" width="100" align="center">
            <template #default="{ row }">
              <span
                class="shift-capsule"
                :class="row.shiftType === 'night' ? 'shift-night' : 'shift-day'"
              >
                {{ formatShiftLabel(row.shiftType) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="类型" width="100" align="center">
            <template #default="{ row }">
              <span
                class="type-capsule"
                :class="row.triggerType === 'late_checkin_review' ? 'type-late' : 'type-early'"
              >
                {{ row.triggerType === "late_checkin_review" ? "迟到" : "早退" }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="上班时长" width="140" align="center">
            <template #default="{ row }">
              <span class="meta-capsule">
                {{ row.triggerType === "late_checkin_review" ? "-" : formatDuration(row.workSeconds || 0) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="异常分钟" width="130" align="center">
            <template #default="{ row }">
              <span class="exception-capsule">{{ row.earlyMinutes || 0 }} min</span>
            </template>
          </el-table-column>
          <el-table-column label="预计罚款" width="130" align="center">
            <template #default="{ row }">
              <span class="fine-capsule"
                >¥{{ Number(row.proposedFine || 0).toFixed(2) }}</span
              >
            </template>
          </el-table-column>
          <el-table-column label="操作" width="280" fixed="right" align="center">
            <template #default="{ row }">
              <div class="action-group-modern">
                <el-button
                  size="small"
                  class="action-btn-confirm absent"
                  :disabled="!canConfirm"
                  @click="confirmAttendanceReviewTask(row, 'absent')"
                >
                  旷工
                </el-button>
                <el-button
                  size="small"
                  class="action-btn-confirm rest"
                  :disabled="!canConfirm"
                  @click="confirmAttendanceReviewTask(row, 'rest_half')"
                >
                  半休
                </el-button>
                <el-button
                  size="small"
                  class="action-btn-confirm leave"
                  :disabled="!canConfirm"
                  @click="confirmAttendanceReviewTask(row, 'leave')"
                >
                  半假
                </el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- 设置对话框 - 优化样式 -->
      <el-dialog 
        v-model="reviewSettingsVisible" 
        title="待确认异常阈值" 
        width="440px" 
        class="settings-dialog-modern"
        :close-on-click-modal="false"
      >
        <div class="dialog-header-modern">
          <div class="dialog-icon-modern">
            <el-icon><Setting /></el-icon>
          </div>
          <div>
            <h3>异常触发阈值</h3>
            <p>设置迟到和早退的触发小时数，超过此值将进入待确认列表</p>
          </div>
        </div>

        <el-form label-width="140px" class="settings-form-modern">
          <el-form-item label="迟到阈值">
            <div class="threshold-input-group">
              <el-input-number 
                v-model="reviewSettings.workHours" 
                :min="1" 
                :max="24" 
                :step="1"
                controls-position="right"
              />
              <span class="threshold-unit">小时</span>
            </div>
          </el-form-item>
          <el-form-item label="早退阈值">
            <div class="threshold-input-group">
              <el-input-number 
                v-model="reviewSettings.earlyHours" 
                :min="1" 
                :max="24" 
                :step="1"
                controls-position="right"
              />
              <span class="threshold-unit">小时</span>
            </div>
          </el-form-item>
          <div class="settings-note-modern">
            <el-icon><InfoFilled /></el-icon>
            <span>当前：迟到 ≥ {{ reviewSettings.workHours }}h 或 早退 ≥ {{ reviewSettings.earlyHours }}h</span>
          </div>
        </el-form>

        <template #footer>
          <div class="dialog-footer-modern">
            <el-button @click="reviewSettingsVisible = false" class="btn-cancel-modern">
              取消
            </el-button>
            <el-button v-permission="'attendance_review:edit'" 
              type="primary" 
              :loading="reviewSettingsSaving" 
              @click="saveAttendanceReviewSettings"
              class="btn-save-modern"
            >
              <el-icon><Check /></el-icon> 保存设置
            </el-button>
          </div>
        </template>
      </el-dialog>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from "vue";
import { useRoute } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Warning,
  Setting,
  Search,
  Money,
  User,
  Refresh,
  Clock,
  Check,
  InfoFilled,
} from "@element-plus/icons-vue";
import workforceTelegramApi from "@api/workforce_telegram_api";
import { getCurrentYearMonth } from "./helpers";
import { hasAnyPermission } from "@store/permission";

const route = useRoute();

const searchEmployee = ref(String(route.query.employee || ""));
const reviewMonth = ref(getCurrentYearMonth());
const refreshing = ref(false);
const reviewSettingsVisible = ref(false);
const reviewSettingsSaving = ref(false);
const reviewSettings = ref({
  workHours: 5,
  earlyHours: 5,
});
const reviewTasks = ref<any[]>([]);
const reviewTasksLoading = ref(false);

const canConfirm = computed(() =>
  hasAnyPermission(["attendance_review:approve"]) && hasAnyPermission(["attendance_review:review"]),
);
const canEditSettings = computed(() =>
  hasAnyPermission(["attendance_review:edit"]),
);

const pendingCount = computed(() => filteredReviewTasks.value.length);

const filteredReviewTasks = computed(() => {
  if (!searchEmployee.value) return reviewTasks.value;
  const keyword = searchEmployee.value.toLowerCase();
  return reviewTasks.value.filter((r) =>
    String(r.employeeName || r.employee_name || "")
      .toLowerCase()
      .includes(keyword),
  );
});

const reviewStatCards = computed(() => {
  const list = filteredReviewTasks.value;
  const count = list.length;
  const fineSum = list.reduce((sum, row) => sum + Number(row.proposedFine || 0), 0);
  const empSet = new Set(
    list.map((row) => row.employeeId || row.employee_id || row.employeeName),
  );
  return [
    {
      key: "pending",
      icon: Warning,
      iconBg: "linear-gradient(135deg, #fffbeb, #fef3c7)",
      iconColor: "#d97706",
      label: "待处理异常",
      value: count.toLocaleString(),
    },
    {
      key: "employee",
      icon: User,
      iconBg: "linear-gradient(135deg, #ecfdf5, #d1fae5)",
      iconColor: "#059669",
      label: "涉及员工",
      value: empSet.size.toLocaleString(),
    },
    {
      key: "total",
      icon: Money,
      iconBg: "linear-gradient(135deg, #fef2f2, #fee2e2)",
      iconColor: "#dc2626",
      label: "预计罚款合计",
      value: `¥${fineSum.toLocaleString(undefined, { maximumFractionDigits: 2 })}`,
    },
  ];
});

const formatShiftLabel = (shift?: string) => {
  if (shift === "night") return "夜班";
  if (shift === "day") return "白班";
  return shift || "-";
};

const formatDuration = (sec: number) => {
  const total = Math.max(0, Number(sec || 0));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  return `${h}h ${m}m`;
};

const getAvatarColor = (name: string) => {
  const colors = [
    "linear-gradient(135deg, #667eea 0%, #764ba2 100%)",
    "linear-gradient(135deg, #f093fb 0%, #f5576c 100%)",
    "linear-gradient(135deg, #4facfe 0%, #00f2fe 100%)",
    "linear-gradient(135deg, #43e97b 0%, #38f9d7 100%)",
    "linear-gradient(135deg, #fa709a 0%, #fee140 100%)",
    "linear-gradient(135deg, #a8edea 0%, #fed6e3 100%)",
  ];
  const index = name?.charCodeAt(0) % colors.length || 0;
  return colors[index];
};

const loadAttendanceReviewSettings = async () => {
  try {
    const res = await workforceTelegramApi.getAttendanceReviewSettings();
    reviewSettings.value.workHours = Number(res.data?.workHours || 5);
    reviewSettings.value.earlyHours = Number(res.data?.earlyHours || 5);
  } catch {
    // keep defaults
  }
};

const saveAttendanceReviewSettings = async () => {
  reviewSettingsSaving.value = true;
  try {
    await workforceTelegramApi.saveAttendanceReviewSettings({
      workHours: Number(reviewSettings.value.workHours || 5),
      earlyHours: Number(reviewSettings.value.earlyHours || 5),
    });
    ElMessage.success("设置已保存");
    reviewSettingsVisible.value = false;
    await loadAttendanceReviewTasks();
  } catch (e: any) {
    ElMessage.error(e.message || "保存失败");
  } finally {
    reviewSettingsSaving.value = false;
  }
};

const loadAttendanceReviewTasks = async () => {
  reviewTasksLoading.value = true;
  try {
    const res = await workforceTelegramApi.listAttendanceReviewTasks({
      month: reviewMonth.value,
    });
    reviewTasks.value = res.data?.items || [];
  } catch {
    reviewTasks.value = [];
  } finally {
    reviewTasksLoading.value = false;
  }
};

const confirmAttendanceReviewTask = async (
  row: any,
  decision: "absent" | "rest_half" | "leave",
) => {
  try {
    const label =
      decision === "absent" ? "旷工" : decision === "rest_half" ? "半休" : "半假";
    await ElMessageBox.confirm(
      `确认将 ${row.employeeName || row.employee_name} 标记为「${label}」？`,
      "确认处理",
      { 
        type: "warning",
        confirmButtonText: "确认",
        cancelButtonText: "取消",
        customClass: "confirm-dialog-modern",
      },
    );
    const res = await workforceTelegramApi.confirmAttendanceReviewTask(row.id, {
      decision,
    });
    ElMessage.success(res.data?.messageText || "已处理");
    await loadAttendanceReviewTasks();
  } catch (e: any) {
    if (e !== "cancel") {
      ElMessage.error(e.message || "处理失败");
    }
  }
};

const openReviewSettingsDialog = async () => {
  await loadAttendanceReviewSettings();
  reviewSettingsVisible.value = true;
};

const handleRefresh = async () => {
  refreshing.value = true;
  try {
    await loadAttendanceReviewTasks();
    ElMessage.success("数据已刷新");
  } catch {
    ElMessage.error("刷新失败");
  } finally {
    refreshing.value = false;
  }
};

onMounted(async () => {
  await loadAttendanceReviewSettings();
  await loadAttendanceReviewTasks();
});
</script>

<style scoped>
.attendance-management {
  position: relative;
  padding: 0;
  min-height: 100%;
}

.bg-decoration {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  z-index: 0;
}

.blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(60px);
  opacity: 0.35;
}

.blob-1 {
  width: 280px;
  height: 280px;
  background: #fde68a;
  top: -80px;
  right: 10%;
}

.blob-2 {
  width: 220px;
  height: 220px;
  background: #fecaca;
  bottom: 10%;
  left: 5%;
}

.blob-3 {
  width: 180px;
  height: 180px;
  background: #bfdbfe;
  top: 40%;
  right: 0;
}

/* ========== 卡片 ========== */
.penalty-card {
  position: relative;
  z-index: 1;
  border: none;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.92);
  backdrop-filter: blur(8px);
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.04);
}

.penalty-card :deep(.el-card__header) {
  border-bottom: 1px solid rgba(0, 0, 0, 0.05);
  padding: 18px 24px;
}

.penalty-card :deep(.el-card__body) {
  padding: 20px 24px 24px;
}

/* ========== 卡片头部 ========== */
.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.header-title {
  display: flex;
  align-items: center;
  gap: 12px;
}

.title-icon {
  width: 38px;
  height: 38px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 18px;
  flex-shrink: 0;
}

.review-title-icon {
  background: linear-gradient(135deg, #f59e0b, #f97316);
  box-shadow: 0 4px 14px rgba(245, 158, 11, 0.35);
}

.title-text {
  font-size: 18px;
  font-weight: 700;
  color: #0f172a;
  letter-spacing: -0.3px;
}

.pending-count-capsule {
  display: inline-flex;
  align-items: center;
  padding: 2px 12px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 700;
  line-height: 1.5;
}

.pending-count-capsule.has-pending {
  color: #b45309;
  background: linear-gradient(180deg, #fffbeb 0%, #fde68a 100%);
  border: 1px solid #fcd34d;
}

.pending-count-capsule.all-done {
  color: #0f766e;
  background: linear-gradient(180deg, #f0fdfa 0%, #ccfbf1 100%);
  border: 1px solid #99f6e4;
}

/* ========== 头部操作区 ========== */
.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.search-wrapper {
  position: relative;
}

.search-input :deep(.el-input__wrapper) {
  border-radius: 10px;
  box-shadow: none;
  border: 1.5px solid #e2e8f0;
  background: #ffffff;
  transition: all 0.25s ease;
  padding: 2px 12px;
}

.search-input :deep(.el-input__wrapper):hover {
  border-color: #94a3b8;
}

.search-input :deep(.el-input__wrapper.is-focus) {
  border-color: #f59e0b;
  box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.12);
}

.search-input :deep(.el-input__prefix) {
  color: #94a3b8;
}

.date-wrapper .month-picker :deep(.el-input__wrapper) {
  border-radius: 10px;
  box-shadow: none;
  border: 1.5px solid #e2e8f0;
  background: #ffffff;
  transition: all 0.25s ease;
  padding: 2px 12px;
}

.date-wrapper .month-picker :deep(.el-input__wrapper):hover {
  border-color: #94a3b8;
}

.date-wrapper .month-picker :deep(.el-input__wrapper.is-focus) {
  border-color: #f59e0b;
  box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.12);
}

/* ========== 操作按钮 ========== */
.action-btn {
  border-radius: 10px;
  padding: 8px 18px;
  font-weight: 500;
  font-size: 13px;
  transition: all 0.25s ease;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: none;
}

.action-btn .el-icon {
  font-size: 16px;
}

.action-btn-ghost {
  background: #f1f5f9;
  color: #475569;
}

.action-btn-ghost:hover:not(:disabled) {
  background: #e2e8f0;
  color: #0f172a;
  transform: translateY(-1px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.06);
}

.action-btn-ghost:active {
  transform: translateY(0);
}

.action-btn-primary {
  background: linear-gradient(135deg, #f59e0b, #f97316);
  color: #fff;
  box-shadow: 0 4px 14px rgba(245, 158, 11, 0.3);
}

.action-btn-primary:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(245, 158, 11, 0.4);
}

.action-btn-primary:active {
  transform: translateY(0);
}

.action-btn-primary .el-icon {
  color: #fff;
}

/* ========== 统计卡片 ========== */
.penalty-stats-row {
  margin-bottom: 16px;
}

.stat-card-modern {
  height: 100%;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}

.stat-card-modern:hover {
  transform: translateY(-3px);
}

.stat-content-modern {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px 20px;
  background: rgba(255, 255, 255, 0.85);
  border-radius: 14px;
  border: 1px solid #e2e8f0;
  transition: all 0.3s ease;
  height: 100%;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.02);
}

.stat-card-modern:hover .stat-content-modern {
  border-color: #cbd5e1;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.06);
}

.stat-pending .stat-content-modern {
  border-left: 4px solid #f59e0b;
}

.stat-employee .stat-content-modern {
  border-left: 4px solid #10b981;
}

.stat-total .stat-content-modern {
  border-left: 4px solid #ef4444;
}

.stat-icon-modern {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  flex-shrink: 0;
}

.stat-info-modern {
  flex: 1;
  min-width: 0;
}

.stat-value-modern {
  font-size: 24px;
  font-weight: 800;
  color: #0f172a;
  line-height: 1.2;
  letter-spacing: -0.5px;
}

.stat-label-modern {
  font-size: 13px;
  color: #64748b;
  margin-top: 2px;
}

/* ========== 提示条 ========== */
.review-tip-modern {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 18px;
  margin-bottom: 16px;
  border-radius: 12px;
  background: linear-gradient(135deg, #fffbeb 0%, #fef3c7 100%);
  border: 1px solid rgba(245, 158, 11, 0.15);
  flex-wrap: wrap;
}

.tip-icon-wrap {
  width: 32px;
  height: 32px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(245, 158, 11, 0.12);
  flex-shrink: 0;
}

.tip-icon {
  color: #d97706;
  font-size: 16px;
}

.tip-text-wrap {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  font-size: 13px;
  color: #78350f;
  line-height: 1.5;
  min-width: 0;
}

.tip-label {
  font-weight: 600;
  color: #92400e;
}

.tip-content strong {
  color: #d97706;
  font-weight: 700;
}

.tip-edit-btn {
  color: #d97706;
  font-weight: 600;
  padding: 4px 12px;
  border-radius: 6px;
  background: rgba(245, 158, 11, 0.08);
  flex-shrink: 0;
}

.tip-edit-btn:hover {
  background: rgba(245, 158, 11, 0.16);
  color: #b45309;
}

/* ========== 表格 ========== */
.modern-table-enhanced {
  border-radius: 14px;
  overflow: hidden;
  border: 1px solid #fde68a !important;
}

.modern-table-enhanced :deep(.el-table__header-wrapper th.el-table__cell),
.modern-table-enhanced :deep(.el-table__header th) {
  background: linear-gradient(180deg, #fffbeb 0%, #fde68a 55%, #fcd34d 100%) !important;
  color: #92400e !important;
  font-weight: 700 !important;
  font-size: 13px !important;
  border-bottom: 1px solid #f59e0b !important;
  height: 48px;
}

.modern-table-enhanced :deep(.el-table__header .cell) {
  color: #92400e;
  font-weight: 700;
}

.modern-table-enhanced :deep(.el-table__inner-wrapper::before) {
  display: none;
}

.modern-table-enhanced :deep(.el-table__row) {
  transition: background 0.2s ease;
}

.modern-table-enhanced :deep(.el-table__row:hover td) {
  background: rgba(245, 158, 11, 0.06) !important;
}

.modern-table-enhanced :deep(.el-table__body td) {
  padding: 12px 0;
}

.modern-table-enhanced :deep(.el-table__fixed-right .el-table__header th),
.modern-table-enhanced :deep(.el-table__fixed .el-table__header th) {
  background: linear-gradient(180deg, #fffbeb 0%, #fde68a 55%, #fcd34d 100%) !important;
}

/* 行索引 */
.row-index {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  color: #b45309;
  background: linear-gradient(180deg, #fffbeb 0%, #fde68a 100%);
  border: 1px solid #fcd34d;
}

/* 员工单元格 */
.employee-cell-modern {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
}

.employee-avatar-modern {
  font-weight: 600;
  font-size: 12px;
  flex-shrink: 0;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
}

.employee-name-capsule,
.position-capsule,
.date-capsule,
.shift-capsule,
.type-capsule,
.meta-capsule,
.exception-capsule,
.fine-capsule {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  max-width: 100%;
  padding: 2px 11px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  line-height: 1.5;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
}

.employee-name-capsule {
  color: #0f766e;
  background: linear-gradient(180deg, #f0fdfa 0%, #ccfbf1 100%);
  border: 1px solid #99f6e4;
}

.position-capsule {
  color: #475569;
  background: linear-gradient(180deg, #f8fafc 0%, #e2e8f0 100%);
  border: 1px solid #cbd5e1;
}

.date-capsule {
  color: #64748b;
  background: linear-gradient(180deg, #f8fafc 0%, #eef2f7 100%);
  border: 1px solid #e2e8f0;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}

.shift-capsule.shift-day {
  color: #1d4ed8;
  background: linear-gradient(180deg, #eff6ff 0%, #bfdbfe 100%);
  border: 1px solid #93c5fd;
}

.shift-capsule.shift-night {
  color: #b45309;
  background: linear-gradient(180deg, #fffbeb 0%, #fde68a 100%);
  border: 1px solid #fcd34d;
}

.type-capsule.type-late {
  color: #b91c1c;
  background: linear-gradient(180deg, #fef2f2 0%, #fecaca 100%);
  border: 1px solid #fca5a5;
}

.type-capsule.type-early {
  color: #c2410c;
  background: linear-gradient(180deg, #fff7ed 0%, #fed7aa 100%);
  border: 1px solid #fdba74;
}

.meta-capsule {
  color: #64748b;
  background: linear-gradient(180deg, #f8fafc 0%, #eef2f7 100%);
  border: 1px solid #e2e8f0;
  font-weight: 500;
}

.exception-capsule {
  color: #b45309;
  background: linear-gradient(180deg, #fffbeb 0%, #fde68a 100%);
  border: 1px solid #fcd34d;
}

.fine-capsule {
  color: #b91c1c;
  background: linear-gradient(180deg, #fef2f2 0%, #fecaca 100%);
  border: 1px solid #fca5a5;
  font-variant-numeric: tabular-nums;
}

/* ========== 操作按钮组 ========== */
.action-group-modern {
  display: flex;
  gap: 6px;
  justify-content: center;
  flex-wrap: wrap;
}

.action-btn-confirm {
  border-radius: 999px !important;
  padding: 5px 14px !important;
  font-weight: 600 !important;
  font-size: 12px !important;
  border: 1px solid transparent !important;
  transition: all 0.25s ease;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
}

.action-btn-confirm.absent {
  background: linear-gradient(180deg, #fef2f2 0%, #fecaca 100%);
  color: #b91c1c;
  border-color: #fca5a5 !important;
}

.action-btn-confirm.absent:hover:not(:disabled) {
  background: #dc2626;
  color: #fff;
  border-color: #dc2626 !important;
  box-shadow: 0 4px 12px rgba(220, 38, 38, 0.3);
}

.action-btn-confirm.rest {
  background: linear-gradient(180deg, #ecfdf5 0%, #a7f3d0 100%);
  color: #047857;
  border-color: #6ee7b7 !important;
}

.action-btn-confirm.rest:hover:not(:disabled) {
  background: #059669;
  color: #fff;
  border-color: #059669 !important;
  box-shadow: 0 4px 12px rgba(5, 150, 105, 0.3);
}

.action-btn-confirm.leave {
  background: linear-gradient(180deg, #eff6ff 0%, #bfdbfe 100%);
  color: #1d4ed8;
  border-color: #93c5fd !important;
}

.action-btn-confirm.leave:hover:not(:disabled) {
  background: #2563eb;
  color: #fff;
  border-color: #2563eb !important;
  box-shadow: 0 4px 12px rgba(37, 99, 235, 0.3);
}

.action-btn-confirm:disabled {
  opacity: 0.5;
  cursor: not-allowed;
  transform: none !important;
  box-shadow: none !important;
}

/* ========== 设置对话框 ========== */
.settings-dialog-modern :deep(.el-dialog) {
  border-radius: 20px;
  overflow: hidden;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.15);
}

.settings-dialog-modern :deep(.el-dialog__header) {
  padding: 20px 24px 0;
  border-bottom: none;
}

.settings-dialog-modern :deep(.el-dialog__title) {
  font-weight: 700;
  font-size: 18px;
  color: #0f172a;
}

.settings-dialog-modern :deep(.el-dialog__body) {
  padding: 20px 24px;
}

.settings-dialog-modern :deep(.el-dialog__footer) {
  padding: 16px 24px 24px;
  border-top: 1px solid #f1f5f9;
}

.dialog-header-modern {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 20px;
  padding: 16px 20px;
  background: linear-gradient(135deg, #f8fafc, #f1f5f9);
  border-radius: 12px;
}

.dialog-icon-modern {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #f59e0b, #f97316);
  color: #fff;
  font-size: 20px;
  flex-shrink: 0;
}

.dialog-header-modern h3 {
  margin: 0 0 4px;
  font-size: 15px;
  color: #0f172a;
}

.dialog-header-modern p {
  margin: 0;
  font-size: 13px;
  color: #64748b;
}

.settings-form-modern .el-form-item {
  margin-bottom: 20px;
}

.settings-form-modern .el-form-item__label {
  font-weight: 500;
  color: #475569;
}

.threshold-input-group {
  display: flex;
  align-items: center;
  gap: 10px;
}

.threshold-unit {
  font-size: 13px;
  color: #64748b;
  font-weight: 500;
}

.settings-note-modern {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  border-radius: 10px;
  background: #f8fafc;
  color: #475569;
  font-size: 13px;
}

.settings-note-modern .el-icon {
  color: #f59e0b;
  font-size: 16px;
  flex-shrink: 0;
}

.dialog-footer-modern {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.btn-cancel-modern {
  border-radius: 10px;
  padding: 8px 20px;
  border: 1.5px solid #e2e8f0;
  color: #64748b;
  font-weight: 500;
  transition: all 0.2s ease;
}

.btn-cancel-modern:hover {
  border-color: #94a3b8;
  color: #0f172a;
  background: #f8fafc;
}

.btn-save-modern {
  border-radius: 10px;
  padding: 8px 24px;
  background: linear-gradient(135deg, #f59e0b, #f97316);
  border: none;
  color: #fff;
  font-weight: 600;
  transition: all 0.25s ease;
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.btn-save-modern:hover:not(:disabled) {
  background: linear-gradient(135deg, #d97706, #ea580c);
  transform: translateY(-1px);
  box-shadow: 0 4px 16px rgba(245, 158, 11, 0.35);
}

.btn-save-modern:disabled {
  opacity: 0.7;
}

.btn-save-modern .el-icon {
  color: #fff;
}

/* ========== 响应式 ========== */
@media (max-width: 768px) {
  .penalty-card :deep(.el-card__header) {
    padding: 14px 16px;
  }

  .penalty-card :deep(.el-card__body) {
    padding: 14px 16px;
  }

  .card-header {
    flex-direction: column;
    align-items: stretch;
  }

  .header-actions {
    flex-wrap: wrap;
    gap: 8px;
  }

  .search-wrapper {
    flex: 1;
    min-width: 140px;
  }

  .search-input {
    width: 100% !important;
  }

  .date-wrapper {
    flex: 1;
    min-width: 140px;
  }

  .date-wrapper .month-picker {
    width: 100% !important;
  }

  .action-btn {
    padding: 6px 14px;
    font-size: 12px;
  }

  .action-btn span {
    display: none;
  }

  .stat-content-modern {
    padding: 14px 16px;
  }

  .stat-value-modern {
    font-size: 20px;
  }

  .stat-icon-modern {
    width: 40px;
    height: 40px;
    font-size: 18px;
  }

  .review-tip-modern {
    flex-direction: column;
    align-items: flex-start;
    padding: 12px 14px;
  }

  .tip-edit-btn {
    align-self: flex-start;
  }

  .action-group-modern {
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }

  .action-btn-confirm {
    width: 100%;
    max-width: 80px;
    justify-content: center;
    padding: 4px 10px;
    font-size: 11px;
  }

  .settings-dialog-modern :deep(.el-dialog) {
    width: 95% !important;
    margin: 16px auto !important;
  }

  .dialog-header-modern {
    flex-direction: column;
    align-items: stretch;
  }

  .threshold-input-group {
    flex-wrap: wrap;
  }
}

@media (max-width: 480px) {
  .header-title .title-text {
    font-size: 16px;
  }

  .title-icon {
    width: 32px;
    height: 32px;
    font-size: 15px;
  }

  .stat-content-modern {
    flex-direction: column;
    text-align: center;
    padding: 12px 14px;
  }

  .stat-icon-modern {
    width: 36px;
    height: 36px;
    font-size: 16px;
  }

  .stat-value-modern {
    font-size: 18px;
  }
}
</style>