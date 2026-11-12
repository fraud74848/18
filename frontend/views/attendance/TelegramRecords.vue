<template>
  <div class="telegram-records-page attendance-management">
    <div class="bg-decoration" aria-hidden="true">
      <div class="blob blob-1 tg-rec-blob-1"></div>
      <div class="blob blob-2 tg-rec-blob-2"></div>
      <div class="blob blob-3 tg-rec-blob-3"></div>
    </div>

    <div class="tg-rec-content">
      <el-alert
        v-if="!moduleEnabled"
        type="info"
        show-icon
        :closable="false"
        class="tg-alert"
        title="Telegram 考勤模块未启用"
      />

      <el-card class="tg-rec-main-card" shadow="never">

        <div class="tg-rec-stat-grid">
          <div class="tg-rec-stat-card sessions">
            <span class="tg-rec-stat-num">{{ sessionStats.count }}</span>
            <span class="tg-rec-stat-label">活动记录</span>
          </div>
          <div class="tg-rec-stat-card fine">
            <span class="tg-rec-stat-num">{{ formatFineAmount(sessionStats.totalFine, fineCurrency) }}</span>
            <span class="tg-rec-stat-label">{{ sessionTodayOnly ? "所选日罚款合计" : "所选月罚款合计" }}</span>
          </div>

          <div v-if="account.hasPermission('data:all')" class="tg-rec-stat-card rankings">
            <span class="tg-rec-stat-num">{{ rankings.length }}</span>
            <span class="tg-rec-stat-label">排行榜条目</span>
          </div>
        </div>

        <el-tabs v-model="activeTab" class="tg-tabs" @tab-change="onTabChange">
          <el-tab-pane name="work">
            <template #label>
              <span class="tg-tab-label"><el-icon><Clock /></el-icon>上下班记录</span>
            </template>

            <div class="activity-section">
              <div class="activity-section-head">
                <div>
                  <h4 class="sub-block-title">上班 / 下班 (TG 与后台考勤记录)</h4>
                
                </div>
              </div>


              <div class="tg-toolbar">
                <el-switch
                  v-model="workTodayOnly"
                  inline-prompt
                  active-text="按日"
                  inactive-text="按月"
                  @change="onWorkScopeChange"
                />
                <el-date-picker
                  v-if="workTodayOnly"
                  v-model="workDate"
                  type="date"
                  placeholder="选择日期"
                  value-format="YYYY-MM-DD"
                  style="width: 160px"
                  @change="loadWorkSessions"
                />
                <el-date-picker
                  v-else
                  v-model="workMonth"
                  type="month"
                  placeholder="选择月份"
                  format="YYYY年MM月"
                  value-format="YYYY-MM"
                  style="width: 160px"
                  @change="loadWorkSessions"
                />
                <el-input
                  v-model="workSearch"
                  placeholder="搜索员工或工号"
                  clearable
                  class="tg-search-input"
                  :prefix-icon="Search"
                  @input="debouncedLoadWork"
                />
                <el-button type="primary" @click="loadWorkSessions" :loading="loadingWork">
                  <el-icon><Refresh /></el-icon>
                  刷新
                </el-button>
              </div>

              <el-table
                :data="workSessions"
                v-loading="loadingWork"
                stripe
                class="tg-table"
                empty-text="暂无上下班记录"
              >
                <el-table-column prop="employeeName" label="员工" width="100" />
                <el-table-column prop="employeeCode" label="工号" width="100" />
                <el-table-column label="班次" width="80">
                  <template #default="{ row }">{{ formatShift(row.shiftType) }}</template>
                </el-table-column>
                <el-table-column label="来源" width="90">
                  <template #default="{ row }">{{ formatWorkSource(row.source) }}</template>
                </el-table-column>
                <el-table-column label="状态" width="90">
                  <template #default="{ row }">
                    <el-tag :type="row.status === 'open' ? 'warning' : 'success'" size="small">
                      {{ row.status === "open" ? "上班中" : "已下班" }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="attendanceDate" label="考勤日" width="110" />
                <el-table-column label="上班时间" min-width="150">
                  <template #default="{ row }">{{ formatDateTime(row.checkinTime) }}</template>
                </el-table-column>
                <el-table-column label="下班时间" min-width="150">
                  <template #default="{ row }">
                    {{ row.checkoutTime ? formatDateTime(row.checkoutTime) : "-" }}
                  </template>
                </el-table-column>
              </el-table>
            </div>
          </el-tab-pane>

          <el-tab-pane name="sessions">
            <template #label>
              <span class="tg-tab-label"><el-icon><Timer /></el-icon>活动记录</span>
            </template>

            <div class="activity-section">
              <div class="activity-section-head">
                <div>
                  <h4 class="sub-block-title">活动会话</h4>
                  <p class="section-desc inline-desc">
                    小厕、吃饭等活动打卡；日期按实际开始时间或考勤日匹配
                  </p>
                </div>
              </div>

            

              <div class="tg-toolbar">
                <el-switch
                  v-model="sessionTodayOnly"
                  inline-prompt
                  active-text="按日"
                  inactive-text="按月"
                  @change="onSessionScopeChange"
                />
                <el-date-picker
                  v-if="sessionTodayOnly"
                  v-model="sessionDate"
                  type="date"
                  placeholder="选择日期"
                  value-format="YYYY-MM-DD"
                  style="width: 160px"
                  @change="loadActivitySessions"
                />
                <el-date-picker
                  v-else
                  v-model="sessionMonth"
                  type="month"
                  placeholder="选择月份"
                  format="YYYY年MM月"
                  value-format="YYYY-MM"
                  style="width: 160px"
                  @change="loadActivitySessions"
                />
                <el-input
                  v-model="sessionSearch"
                  placeholder="搜索员工或活动"
                  clearable
                  class="tg-search-input"
                  :prefix-icon="Search"
                  @input="debouncedLoadSessions"
                />
                <el-button type="primary" @click="loadActivitySessions" :loading="loadingSessions">
                  <el-icon><Refresh /></el-icon>
                  刷新
                </el-button>
              </div>

              <el-table
                :data="activitySessions"
                v-loading="loadingSessions"
                stripe
                class="tg-table"
                empty-text="暂无活动记录"
              >
                <el-table-column prop="employeeName" label="员工" width="100" />
                <el-table-column prop="activityName" label="活动" width="120" />
                <el-table-column label="状态" width="90">
                  <template #default="{ row }">
                    <el-tag :type="row.status === 'open' ? 'warning' : 'success'" size="small">
                      {{ row.status === "open" ? "进行中" : "已结束" }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column label="时长" width="90">
                  <template #default="{ row }">{{ formatSeconds(row.elapsedSeconds) }}</template>
                </el-table-column>
                <el-table-column label="超时" width="90">
                  <template #default="{ row }">{{ formatSeconds(row.overtimeSeconds) }}</template>
                </el-table-column>
                <el-table-column label="罚款" width="90">
                  <template #default="{ row }">
                    <span v-if="row.fineAmount > 0" class="fine-text">{{ formatFineAmount(row.fineAmount, fineCurrency) }}</span>
                    <span v-else class="muted-cell">-</span>
                  </template>
                </el-table-column>
                <el-table-column label="开始时间" min-width="160">
                  <template #default="{ row }">{{ formatDateTime(row.startedAt) }}</template>
                </el-table-column>
                <el-table-column label="开始来源" width="80" align="center">
                  <template #default="{ row }">{{ formatActivitySourceShort(row.startSource) }}</template>
                </el-table-column>
                <el-table-column label="结束时间" min-width="160">
                  <template #default="{ row }">
                    {{
                      row.endedAt
                        ? formatDateTime(row.endedAt)
                        : row.status === "open"
                          ? "进行中"
                          : "-"
                    }}
                  </template>
                </el-table-column>
                <el-table-column label="结束来源" width="80" align="center">
                  <template #default="{ row }">{{ formatActivitySourceShort(row.endSource) }}</template>
                </el-table-column>
              </el-table>
            </div>
          </el-tab-pane>



          <el-tab-pane v-if="account.hasPermission('data:all')" name="rankings">
            <template #label>
              <span class="tg-tab-label"><el-icon><Trophy /></el-icon>排行榜</span>
            </template>

            <div class="activity-section">
      

              <div class="tg-toolbar">
                <el-button-group>
                  <el-button
                    :type="rankingDate === todayDateStr() ? 'primary' : 'default'"
                    @click="setRankingDate(todayDateStr())"
                  >
                    今天
                  </el-button>
                  <el-button
                    :type="rankingDate === yesterdayDateStr() ? 'primary' : 'default'"
                    @click="setRankingDate(yesterdayDateStr())"
                  >
                    昨天
                  </el-button>
                </el-button-group>
                <el-date-picker
                  v-model="rankingDate"
                  type="date"
                  placeholder="选择日期"
                  value-format="YYYY-MM-DD"
                  style="width: 160px"
                  @change="loadRankings"
                />
                <el-select v-model="rankingMetric" style="width: 140px" @change="loadRankings">
                  <el-option label="罚款排行" value="fine" />
                  <el-option label="活动次数" value="activity" />
                  <el-option label="超时时长" value="overtime" />
                </el-select>
                <el-select
                  v-model="rankingShift"
                  clearable
                  placeholder="全部班次"
                  style="width: 120px"
                  @change="loadRankings"
                >
                  <el-option label="白班" value="day" />
                  <el-option label="夜班" value="night" />
                </el-select>
                <el-select
                  v-model="rankingChatId"
                  clearable
                  filterable
                  placeholder="全部群组"
                  style="width: 220px"
                  @change="loadRankings"
                >
                  <el-option
                    v-for="g in groups"
                    :key="g.chatId"
                    :label="g.chatTitle ? `${g.chatTitle} (${g.chatId})` : g.chatId"
                    :value="g.chatId"
                  />
                </el-select>
                <el-button type="primary" @click="loadRankings" :loading="loadingRankings">
                  <el-icon><Refresh /></el-icon>
                  刷新
                </el-button>
              </div>

              <div v-if="topThreeRankings.length" class="ranking-podium">
                <div
                  v-for="row in topThreeRankings"
                  :key="row.rank"
                  class="ranking-podium-card"
                  :class="`rank-${row.rank}`"
                >
                  <span class="podium-rank">{{ podiumRankTitle(row.rank) }}</span>
                  <div class="podium-detail">
                    <span class="podium-name">{{ row.employeeName }}</span>
                    <span class="podium-metric">{{ rankingMetricLabel(row) }}</span>
                  </div>
                </div>
              </div>

              <el-table
                :data="rankings"
                v-loading="loadingRankings"
                stripe
                class="tg-table"
                empty-text="暂无排行数据"
              >
                <el-table-column label="名次" width="120" align="center">
                  <template #default="{ row }">{{ podiumRankTitle(row.rank) }}</template>
                </el-table-column>
                <el-table-column prop="employeeName" label="员工" min-width="100" />
                <el-table-column prop="employeeCode" label="工号" width="100" />
                <el-table-column prop="activityCount" label="活动次数" width="100" />
                <el-table-column label="超时" width="100">
                  <template #default="{ row }">{{ formatSeconds(row.overtimeSeconds) }}</template>
                </el-table-column>
                <el-table-column label="罚款" width="100">
                  <template #default="{ row }">
                    <span v-if="row.fineTotal > 0" class="fine-text">{{ formatFineAmount(row.fineTotal, fineCurrency) }}</span>
                    <span v-else class="muted-cell">-</span>
                  </template>
                </el-table-column>
              </el-table>
            </div>
          </el-tab-pane>
        </el-tabs>
      </el-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import {useUserStore} from "../../store/user";
const account = useUserStore();
import { ref, computed, onMounted } from "vue";
import { ElMessage } from "element-plus";
import { Timer, Switch, Trophy, Refresh, Search, Clock } from "@element-plus/icons-vue";
import workforceTelegramApi from "@api/workforce_telegram_api";
import { metaApi } from "@api/index";
import {
  formatSeconds,
  formatDateTime,
  todayDateStr,
  yesterdayDateStr,
  currentYearMonthStr,
  formatFineAmount,
  normalizeFineCurrency,
} from "./telegram_helpers";

const moduleEnabled = ref(false);
const fineCurrency = ref("CNY");
const activeTab = ref("work");

const groups = ref<any[]>([]);
const workSessions = ref<any[]>([]);
const loadingWork = ref(false);
const workTodayOnly = ref(true);
const workDate = ref(todayDateStr());
const workMonth = ref(currentYearMonthStr());
const workSearch = ref("");

const activitySessions = ref<any[]>([]);
const loadingSessions = ref(false);
const sessionTodayOnly = ref(true);
const sessionDate = ref(todayDateStr());
const sessionMonth = ref(currentYearMonthStr());
const sessionSearch = ref("");

const handovers = ref<any[]>([]);
const loadingHandovers = ref(false);
const handoverDate = ref("");
const handoverSearch = ref("");

const rankings = ref<any[]>([]);
const loadingRankings = ref(false);
const rankingDate = ref(todayDateStr());
const rankingMetric = ref("fine");
const rankingShift = ref("");
const rankingChatId = ref("");

let workSearchTimer: ReturnType<typeof setTimeout> | null = null;
let sessionSearchTimer: ReturnType<typeof setTimeout> | null = null;
let handoverSearchTimer: ReturnType<typeof setTimeout> | null = null;

const sessionStats = computed(() => {
  const items = activitySessions.value;
  const totalFine = items.reduce((sum, row) => sum + (Number(row.fineAmount) || 0), 0);
  return {
    count: items.length,
    totalFine: totalFine % 1 === 0 ? totalFine : totalFine.toFixed(2),
  };
});

const topThreeRankings = computed(() => rankings.value.filter((row) => row.rank <= 3));

function setRankingDate(date: string) {
  rankingDate.value = date;
  loadRankings();
}

/** 按当前排行类型生成友好名次文案，避免只显示 #1 */
const podiumRankTitle = (rank: number) => {
  const metric = rankingMetric.value;
  if (rank === 1) {
    if (metric === "fine") return "罚款最多";
    if (metric === "activity") return "活动次数最多";
    return "超时最多";
  }
  if (metric === "fine") return `罚款第${rank}`;
  if (metric === "activity") return `活动次数第${rank}`;
  return `超时第${rank}`;
};

const rankingMetricLabel = (row: any) => {
  if (rankingMetric.value === "fine") {
    return row.fineTotal > 0 ? formatFineAmount(row.fineTotal, fineCurrency.value) : formatFineAmount(0, fineCurrency.value);
  }
  if (rankingMetric.value === "activity") {
    return `${row.activityCount || 0} 次活动`;
  }
  return formatSeconds(row.overtimeSeconds);
};

async function loadModuleFlag() {
  try {
    const { data } = await metaApi.modules();
    moduleEnabled.value = !!data?.telegramAttendance;
  } catch {
    moduleEnabled.value = false;
  }
}

async function loadGroups() {
  try {
    const res = await workforceTelegramApi.listGroups();
    groups.value = res.data?.items || [];
  } catch {
    groups.value = [];
  }
}

function formatShift(shift?: string) {
  if (shift === "night") return "夜班";
  if (shift === "day") return "白班";
  return shift || "-";
}

function formatWorkSource(source?: string) {
  if (source === "telegram") return "Telegram";
  if (source === "agent") return "PC客户端";
  if (source === "web") return "web";
  return source || "-";
}

function formatActivitySourceShort(source?: string) {
  if (!source) return "-";
  if (source === "telegram") return "TG";
  if (source === "agent") return "PC";
  if (source === "web") return "web";
  return source;
}

function debouncedLoadWork() {
  if (workSearchTimer) clearTimeout(workSearchTimer);
  workSearchTimer = setTimeout(loadWorkSessions, 300);
}

function onWorkScopeChange() {
  if (workTodayOnly.value) {
    if (!workDate.value) workDate.value = todayDateStr();
  } else if (!workMonth.value) {
    workMonth.value = currentYearMonthStr();
  }
  loadWorkSessions();
}

async function loadWorkSessions() {
  loadingWork.value = true;
  try {
    const params: { date?: string; yearMonth?: string; search?: string } = {
      search: workSearch.value.trim() || undefined,
    };
    if (workTodayOnly.value) {
      params.date = workDate.value || todayDateStr();
    } else {
      params.yearMonth = workMonth.value || currentYearMonthStr();
    }
    const res = await workforceTelegramApi.listWorkSessions(params);
    workSessions.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载上下班记录失败");
  } finally {
    loadingWork.value = false;
  }
}

function debouncedLoadSessions() {
  if (sessionSearchTimer) clearTimeout(sessionSearchTimer);
  sessionSearchTimer = setTimeout(loadActivitySessions, 300);
}

function onSessionScopeChange() {
  if (sessionTodayOnly.value) {
    if (!sessionDate.value) sessionDate.value = todayDateStr();
  } else if (!sessionMonth.value) {
    sessionMonth.value = currentYearMonthStr();
  }
  loadActivitySessions();
}

async function loadActivitySessions() {
  loadingSessions.value = true;
  try {
    const params: { date?: string; yearMonth?: string; search?: string } = {
      search: sessionSearch.value.trim() || undefined,
    };
    if (sessionTodayOnly.value) {
      params.date = sessionDate.value || todayDateStr();
    } else {
      params.yearMonth = sessionMonth.value || currentYearMonthStr();
    }
    const res = await workforceTelegramApi.listActivitySessions(params);
    activitySessions.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载活动记录失败");
  } finally {
    loadingSessions.value = false;
  }
}

function debouncedLoadHandovers() {
  if (handoverSearchTimer) clearTimeout(handoverSearchTimer);
  handoverSearchTimer = setTimeout(loadHandovers, 300);
}

async function loadHandovers() {
  loadingHandovers.value = true;
  try {
    const res = await workforceTelegramApi.listHandovers({
      date: handoverDate.value || undefined,
      search: handoverSearch.value.trim() || undefined,
    });
    handovers.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载交接记录失败");
  } finally {
    loadingHandovers.value = false;
  }
}

async function loadRankings() {
  loadingRankings.value = true;
  try {
    const res = await workforceTelegramApi.listRankings({
      date: rankingDate.value || undefined,
      metric: rankingMetric.value,
      shift: rankingShift.value || undefined,
      chatId: rankingChatId.value || undefined,
    });
    rankings.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载排行榜失败");
  } finally {
    loadingRankings.value = false;
  }
}

function onTabChange(name: string | number) {
  if (name === "work" && workSessions.value.length === 0) loadWorkSessions();
  if (name === "sessions" && activitySessions.value.length === 0) loadActivitySessions();
  
  if (name === "rankings" && rankings.value.length === 0) loadRankings();
}

async function loadFineCurrency() {
  try {
    const res = await workforceTelegramApi.getSettings();
    fineCurrency.value = normalizeFineCurrency(res.data?.fineCurrency);
  } catch {
    fineCurrency.value = "CNY";
  }
}

onMounted(async () => {
  await loadModuleFlag();
  await loadFineCurrency();
  await loadGroups();
  await loadWorkSessions();
  await loadActivitySessions();
});
</script>

<style scoped>
@import "./attendance.css";

.telegram-records-page {
  position: relative;
  min-height: 100%;
}

.tg-rec-blob-1 {
  background: linear-gradient(135deg, #229ed9, #0088cc);
  opacity: 0.32;
}

.tg-rec-blob-2 {
  background: linear-gradient(135deg, #667eea, #764ba2);
  opacity: 0.26;
}

.tg-rec-blob-3 {
  background: linear-gradient(135deg, #fbbf24, #f59e0b);
  opacity: 0.2;
}

.tg-rec-content {
  position: relative;
  z-index: 1;
  width: 100%;
  padding: 8px 0 32px;
}

.tg-alert {
  margin-bottom: 16px;
  border-radius: 12px;
}

.tg-rec-main-card {
  border-radius: 18px;
  border: 1px solid rgba(255, 255, 255, 0.65);
  background: rgba(255, 255, 255, 0.92);
  backdrop-filter: blur(16px);
  box-shadow: 0 8px 32px rgba(15, 23, 42, 0.08);
}

.tg-rec-main-card :deep(.el-card__header) {
  padding: 20px 24px 12px;
  border-bottom: 1px solid rgba(0, 0, 0, 0.05);
}

.tg-rec-main-card :deep(.el-card__body) {
  padding: 8px 20px 24px;
}

.tg-rec-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.tg-rec-title {
  margin: 0 0 6px;
  font-size: 20px;
  font-weight: 700;
  color: #303133;
}

.tg-rec-subtitle {
  margin: 0;
  font-size: 13px;
  color: #909399;
  line-height: 1.5;
}

.tg-rec-stat-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 20px;
}

.tg-rec-stat-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 16px 12px;
  border-radius: 14px;
  background: linear-gradient(145deg, #f8fafc 0%, #f1f5f9 100%);
  border: 1px solid rgba(0, 0, 0, 0.06);
  transition: transform 0.2s ease, box-shadow 0.2s ease;
}

.tg-rec-stat-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 20px rgba(15, 23, 42, 0.08);
}

.tg-rec-stat-card.sessions {
  border-top: 3px solid #0088cc;
}

.tg-rec-stat-card.fine {
  border-top: 3px solid #f59e0b;
}

.tg-rec-stat-card.handovers {
  border-top: 3px solid #6366f1;
}

.tg-rec-stat-card.rankings {
  border-top: 3px solid #10b981;
}

.tg-rec-stat-num {
  font-size: 22px;
  font-weight: 700;
  color: #303133;
}

.tg-rec-stat-label {
  font-size: 12px;
  color: #909399;
}

.tg-tabs :deep(.el-tabs__header) {
  margin-bottom: 20px;
}

.tg-tabs :deep(.el-tabs__nav-wrap::after) {
  height: 1px;
  background: rgba(0, 0, 0, 0.06);
}

.tg-tabs :deep(.el-tabs__item) {
  height: 48px;
  font-size: 14px;
  font-weight: 500;
  color: #606266;
}

.tg-tabs :deep(.el-tabs__item.is-active) {
  color: #0088cc;
  font-weight: 600;
}

.tg-tabs :deep(.el-tabs__active-bar) {
  height: 3px;
  border-radius: 3px;
  background: linear-gradient(90deg, #229ed9, #667eea);
}

.tg-tab-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.activity-section {
  margin-bottom: 8px;
  padding: 20px;
  border-radius: 16px;
  background: linear-gradient(145deg, #f8fafc 0%, #f1f5f9 100%);
  border: 1px solid rgba(0, 0, 0, 0.06);
}

.activity-section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}

.sub-block-title {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}

.section-desc {
  margin: 0;
  padding: 10px 14px;
  font-size: 13px;
  color: #606266;
  background: rgba(0, 136, 204, 0.06);
  border-radius: 10px;
  border-left: 3px solid #0088cc;
}

.inline-desc {
  padding: 0;
  background: none;
  border-left: none;
  color: #909399;
  font-size: 12px;
}

.tg-toolbar {
  display: flex;
  gap: 12px;
  margin-bottom: 16px;
  flex-wrap: wrap;
  align-items: center;
}

.tg-search-input {
  width: 280px;
  max-width: 100%;
}

.tg-table {
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.04);
}

.tg-table :deep(.el-table__header th) {
  background: #f5f7fa !important;
  color: #606266;
  font-weight: 600;
}

.tg-table :deep(.el-table__row:hover > td) {
  background: rgba(0, 136, 204, 0.04) !important;
}

.fine-text {
  color: #e6a23c;
  font-weight: 600;
}

.muted-cell {
  color: #c0c4cc;
}

.ranking-podium {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.ranking-podium-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 10px 12px;
  border-radius: 12px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.06);
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.04);
  text-align: center;
}

.ranking-podium-card.rank-1 {
  border-top: 3px solid #fbbf24;
  order: 2;
}

.ranking-podium-card.rank-2 {
  border-top: 3px solid #94a3b8;
  order: 1;
}

.ranking-podium-card.rank-3 {
  border-top: 3px solid #d97706;
  order: 3;
}

.podium-rank {
  font-size: 14px;
  font-weight: 800;
  color: #303133;
  line-height: 1.2;
}

.podium-detail {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: center;
  gap: 6px 10px;
  line-height: 1.3;
}

.podium-name {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}

.podium-metric {
  font-size: 13px;
  color: #0088cc;
  font-weight: 600;
}

@media (max-width: 900px) {
  .tg-rec-stat-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .ranking-podium {
    grid-template-columns: 1fr;
  }

  .ranking-podium-card.rank-1,
  .ranking-podium-card.rank-2,
  .ranking-podium-card.rank-3 {
    order: unset;
  }
}
</style>
