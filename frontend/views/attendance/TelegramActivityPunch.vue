<template>
  <div
    class="telegram-punch-page attendance-management"
    :class="{ 'is-popup': isPopupMode }"
  >
    <div v-if="!isPopupMode" class="bg-decoration" aria-hidden="true">
      <div class="blob blob-1 punch-blob-1"></div>
      <div class="blob blob-2 punch-blob-2"></div>
      <div class="blob blob-3 punch-blob-3"></div>
    </div>

    <div class="punch-content">
      <el-alert
        v-if="!moduleEnabled"
        type="info"
        show-icon
        :closable="false"
        class="punch-alert"
        title="Telegram 考勤模块未启用"
      />

      <el-card class="punch-main-card" shadow="never">
        <template #header>
          <div class="punch-header">
            <div>
              <h2 class="punch-title">活动打卡</h2>
              <p v-if="!isPopupMode" class="punch-subtitle">
                与 Telegram走同一套上下班与活动链路（含罚款、群通知、会话互斥）；配置管理新增活动后此处同步显示
              </p>
            </div>
            <div class="punch-header-actions">
              <div v-if="!canSelectEmployee" class="self-punch-badge">
                {{
                  linkedEmployeeName
                    ? `当前打卡：${linkedEmployeeName}`
                    : "当前账号未关联员工，无法代打他人"
                }}
              </div>
              <el-select
                v-else
                v-model="selectedEmployeeId"
                filterable
                clearable
                placeholder="请选择员工"
                :style="{ width: isPopupMode ? '160px' : '240px' }"
              >
                <el-option
                  v-for="emp in employees"
                  :key="emp.id"
                  :label="`${emp.name}${emp.employee_id ? ` (${emp.employee_id})` : ''}`"
                  :value="emp.id"
                />
              </el-select>
              <el-button class="action-button" @click="refreshAll" :loading="refreshing">
                <el-icon><Refresh /></el-icon>刷新
              </el-button>
              <el-dropdown
                v-if="!isPopupMode"
                split-button
                class="action-button punch-popup-dropdown"
                @click="openPunchPopup"
                @command="onPunchPopupCommand"
              >
                <el-icon><CopyDocument /></el-icon>
                <span>小窗口</span>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="open">打开小窗口</el-dropdown-item>
                    <el-dropdown-item command="size">设置大小…</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <el-button
                v-if="canEdit && !isPopupMode"
                type="primary"
                class="action-button"
                @click="goActivityConfig"
              >
                <el-icon><Setting /></el-icon>配置活动
              </el-button>
            </div>
          </div>
        </template>

        <div v-if="isPopupMode" class="popup-status" :class="{ 'is-active': !!ongoingActivity }">
          <template v-if="ongoingActivity">
            <span class="popup-status-name">{{ ongoingActivity.activityName || "活动" }}</span>
            <span class="popup-status-label">{{
              ongoingCountdown.overtime ? "已超时" : "剩余"
            }}</span>
            <span
              class="popup-status-countdown"
              :class="{ 'is-overtime': ongoingCountdown.overtime }"
              >{{ ongoingCountdown.text }}</span
            >
          </template>
          <template v-else>暂无进行中活动</template>
        </div>

        <el-alert v-if="!canPunch" title="当前账号只有查看权限，请联系管理员授予后台打卡权限。" type="info" :closable="false" />
        <div class="punch-card-grid">
          <!-- 卡片1：上下班 -->
          <section class="punch-panel work-panel">
            <div class="panel-head">
              <div class="panel-icon work-icon">
                <el-icon><Sunrise /></el-icon>
              </div>
              <div>
                <h3 class="panel-title">上下班打卡</h3>
                <p class="panel-desc">
                  {{
                    tripleShiftEnabled
                      ? "三班模式：白班 / 中班 / 夜班 / 下班"
                      : dualShiftEnabled
                        ? "双班模式：白班 / 夜班 / 下班"
                        : "单班模式：上班 / 下班"
                  }}
                </p>
              </div>
            </div>
            <div class="punch-btn-row" :class="{ 'is-triple': tripleShiftEnabled, 'is-dual': dualShiftEnabled && !tripleShiftEnabled }">
              <template v-if="dualShiftEnabled || tripleShiftEnabled">
                <button
                  type="button"
                  class="punch-btn punch-btn-day"
                  :disabled="!canPunch || punching"
                  @click="onWorkPunch('day')"
                >
                  <span class="punch-btn-label">白班上班</span>
                  <span class="punch-btn-hint">Day In</span>
                </button>
                <button
                  v-if="tripleShiftEnabled"
                  type="button"
                  class="punch-btn punch-btn-day"
                  :disabled="!canPunch || punching"
                  @click="onWorkPunch('middle')"
                >
                  <span class="punch-btn-label">中班上班</span>
                  <span class="punch-btn-hint">Middle In</span>
                </button>
                <button
                  type="button"
                  class="punch-btn punch-btn-night"
                  :disabled="!canPunch || punching"
                  @click="onWorkPunch('night')"
                >
                  <span class="punch-btn-label">夜班上班</span>
                  <span class="punch-btn-hint">Night In</span>
                </button>
              </template>
              <button
                v-else
                type="button"
                class="punch-btn punch-btn-day"
                :disabled="!canPunch || punching"
                @click="onWorkPunch('day')"
              >
                <span class="punch-btn-label">上班</span>
                <span class="punch-btn-hint">Check In</span>
              </button>
              <button
                type="button"
                class="punch-btn punch-btn-end"
                :disabled="!canPunch || punching"
                @click="onCheckoutPunch"
              >
                <span class="punch-btn-label">下班</span>
                <span class="punch-btn-hint">Check Out</span>
              </button>
            </div>
          </section>

          <!-- 卡片3：活动 + 回座 -->
          <section class="punch-panel activity-panel">
            <div class="panel-head">
              <div class="panel-icon activity-icon">
                <el-icon><Timer /></el-icon>
              </div>
              <div class="panel-head-text">
                <h3 class="panel-title">活动与回座</h3>
                <p class="panel-desc">
                  启用中的活动与 TG 同步；可在配置管理新增
                </p>
              </div>
              <el-button
                v-if="canEdit && !isPopupMode"
                link
                type="primary"
                @click="goActivityConfig"
              >
                新增活动
              </el-button>
            </div>

            <div v-loading="loadingActivities" class="punch-btn-wrap">
              <button
                v-for="(act, idx) in enabledActivities"
                :key="act.code"
                type="button"
                class="punch-btn punch-btn-activity"
                :style="activityBtnStyle(idx)"
                :disabled="!canPunch || punching"
                @click="onActivityStart(act.name)"
              >
                <span class="punch-btn-label">{{ act.name }}</span>
                <span class="punch-btn-hint">{{ formatActivityBtnHint(act) }}</span>
                <span class="punch-btn-count">{{ formatActivityCount(act) }}</span>
              </button>
              <button
                type="button"
                class="punch-btn punch-btn-back"
                :disabled="!canPunch || punching"
                @click="onActivityBack"
              >
                <span class="punch-btn-label">回座</span>
                <span class="punch-btn-hint">End Activity</span>
              </button>
              <div
                v-if="!loadingActivities && !enabledActivities.length"
                class="activity-empty"
              >
                暂无启用活动，请到「配置管理 → 活动与罚款」新增并启用
              </div>
            </div>
          </section>
        </div>

        <!-- 活动记录详情 -->
        <section v-if="!isPopupMode" class="records-panel">
          <div class="records-head">
            <div>
              <h3 class="panel-title">活动记录详情</h3>
              <p class="panel-desc">Telegram 与后台产生的活动会话</p>
            </div>
            <div class="records-toolbar">
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
                style="width: 150px"
                @change="loadActivitySessions"
              />
              <el-date-picker
                v-else
                v-model="sessionMonth"
                type="month"
                placeholder="选择月份"
                format="YYYY年MM月"
                value-format="YYYY-MM"
                style="width: 150px"
                @change="loadActivitySessions"
              />
              <el-input
                v-model="sessionSearch"
                placeholder="搜索员工或活动"
                clearable
                style="width: 180px"
                :prefix-icon="Search"
                @input="debouncedLoadSessions"
              />
              <el-button type="primary" :loading="loadingSessions" @click="loadActivitySessions">
                <el-icon><Refresh /></el-icon>刷新
              </el-button>
            </div>
          </div>

          <el-row :gutter="16" class="records-stats">
            <el-col :span="8">
              <div class="mini-stat mini-stat-count">
                <div class="mini-stat-value">{{ sessionStats.count }}</div>
                <div class="mini-stat-label">活动记录</div>
              </div>
            </el-col>
            <el-col :span="8">
              <div class="mini-stat mini-stat-open">
                <div class="mini-stat-value mini-stat-open-line">
                  <template v-if="ongoingActivity">
                    <span class="mini-stat-act-name">{{
                      ongoingActivity.activityName || "活动"
                    }}</span>
                    <span class="mini-stat-remain-label">剩余时间</span>
                    <span
                      class="mini-stat-countdown"
                      :class="{ 'is-overtime': ongoingCountdown.overtime }"
                      >{{ ongoingCountdown.text }}</span
                    >
                  </template>
                  <template v-else>0</template>
                </div>
                <div class="mini-stat-label">
                  {{
                    ongoingActivity
                      ? ongoingCountdown.overtime
                        ? "进行中 · 已超时"
                        : "进行中"
                      : "进行中"
                  }}
                </div>
              </div>
            </el-col>
            <el-col :span="8">
              <div class="mini-stat mini-stat-fine">
                <div class="mini-stat-value">{{ formatFineAmount(sessionStats.totalFine, fineCurrency) }}</div>
                <div class="mini-stat-label">罚款合计</div>
              </div>
            </el-col>
          </el-row>

          <div class="records-table-wrap">
          <el-table
            :data="activitySessions"
            v-loading="loadingSessions"
            stripe
            border
            height="100%"
            class="modern-table"
            empty-text="暂无活动记录"
            :header-cell-style="{
              background: '#f8fafc',
              color: '#1e293b',
              fontWeight: '600',
            }"
          >
            <el-table-column type="index" width="50" label="#" />
            <el-table-column prop="employeeName" label="员工" width="100" align="center" />
            <el-table-column prop="activityName" label="活动" width="120" align="center">
              <template #default="{ row }">
                <el-tag size="small" effect="light" type="warning">{{
                  row.activityName || "-"
                }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="90" align="center">
              <template #default="{ row }">
                <el-tag
                  :type="row.status === 'open' ? 'warning' : 'success'"
                  size="small"
                  effect="light"
                >
                  {{ row.status === "open" ? "进行中" : "已结束" }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="时长" width="90" align="center">
              <template #default="{ row }">{{ formatSeconds(row.elapsedSeconds) }}</template>
            </el-table-column>
            <el-table-column label="超时" width="90" align="center">
              <template #default="{ row }">{{ formatSeconds(row.overtimeSeconds) }}</template>
            </el-table-column>
            <el-table-column label="罚款" width="90" align="center">
              <template #default="{ row }">
                <span v-if="row.fineAmount > 0" class="fine-text">{{ formatFineAmount(row.fineAmount, fineCurrency) }}</span>
                <span v-else class="muted-cell">-</span>
              </template>
            </el-table-column>
            <el-table-column label="开始时间" min-width="160">
              <template #default="{ row }">{{ formatDateTime(row.startedAt) }}</template>
            </el-table-column>
            <el-table-column label="开始来源" width="90" align="center">
              <template #default="{ row }">{{ formatSource(row.startSource) }}</template>
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
            <el-table-column label="结束来源" width="90" align="center">
              <template #default="{ row }">{{ formatSource(row.endSource) }}</template>
            </el-table-column>
          </el-table>
          </div>
        </section>
      </el-card>
    </div>

    <el-dialog
      v-model="popupSizeVisible"
      title="小窗口大小"
      width="360px"
      append-to-body
      destroy-on-close
    >
      <el-form label-width="72px" @submit.prevent>
        <el-form-item label="宽度">
          <el-input-number
            v-model="popupSizeForm.width"
            :min="360"
            :max="1200"
            :step="20"
            controls-position="right"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="高度">
          <el-input-number
            v-model="popupSizeForm.height"
            :min="480"
            :max="1400"
            :step="20"
            controls-position="right"
            style="width: 100%"
          />
        </el-form-item>
        <p class="popup-size-hint">单位：像素。下次打开小窗口将按此尺寸弹出；在小窗口内拖拽调整后也会自动记住。</p>
      </el-form>
      <template #footer>
        <el-button @click="popupSizeVisible = false">取消</el-button>
        <el-button @click="resetPopupSize">恢复默认</el-button>
        <el-button type="primary" @click="confirmPopupSize">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated, watch, reactive } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import {
  Refresh,
  Setting,
  Timer,
  Sunrise,
  Search,
  CopyDocument,
} from "@element-plus/icons-vue";
import workforceTelegramApi from "@api/workforce_telegram_api";
import { metaApi } from "@api/index";
import { useUserStore } from "@store/user";
import {
  formatSeconds,
  formatDateTime,
  todayDateStr,
  currentYearMonthStr,
  formatFineAmount,
  normalizeFineCurrency,
} from "./telegram_helpers";
import adminApi from "@api/admin_api";
import dayjs from "dayjs";
import utc from "dayjs/plugin/utc";

dayjs.extend(utc);
const route = useRoute();
const router = useRouter();
const userStore = useUserStore();
const isPopupMode = computed(() => route.meta.popup === true);
const PUNCH_POPUP_NAME = "tg-activity-punch-popup";
const PUNCH_POPUP_SIZE_KEY = "tg-punch-popup-size";
const DEFAULT_POPUP_WIDTH = 400;
const DEFAULT_POPUP_HEIGHT = 500;
let punchPopupWin: Window | null = null;
let popupResizeTimer: ReturnType<typeof setTimeout> | null = null;

const popupSizeVisible = ref(false);
const popupSizeForm = reactive({
  width: DEFAULT_POPUP_WIDTH,
  height: DEFAULT_POPUP_HEIGHT,
});

function clampPopupSize(width: number, height: number) {
  return {
    width: Math.min(1200, Math.max(360, Math.round(Number(width) || DEFAULT_POPUP_WIDTH))),
    height: Math.min(1400, Math.max(480, Math.round(Number(height) || DEFAULT_POPUP_HEIGHT))),
  };
}

function loadPopupSize() {
  try {
    const raw = localStorage.getItem(PUNCH_POPUP_SIZE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw);
      return clampPopupSize(parsed?.width, parsed?.height);
    }
  } catch {
    // ignore
  }
  return { width: DEFAULT_POPUP_WIDTH, height: DEFAULT_POPUP_HEIGHT };
}

function savePopupSize(width: number, height: number) {
  const size = clampPopupSize(width, height);
  try {
    localStorage.setItem(PUNCH_POPUP_SIZE_KEY, JSON.stringify(size));
  } catch {
    // ignore
  }
  return size;
}

function onPunchPopupCommand(command: string) {
  if (command === "open") openPunchPopup();
  else if (command === "size") openPopupSizeDialog();
}

function openPopupSizeDialog() {
  const size = loadPopupSize();
  popupSizeForm.width = size.width;
  popupSizeForm.height = size.height;
  popupSizeVisible.value = true;
}

function confirmPopupSize() {
  const size = savePopupSize(popupSizeForm.width, popupSizeForm.height);
  popupSizeVisible.value = false;
  ElMessage.success(`已保存 ${size.width}×${size.height}，下次打开将使用此尺寸`);
  if (punchPopupWin && !punchPopupWin.closed) {
    try {
      punchPopupWin.resizeTo(size.width, size.height);
    } catch {
      // browsers may block resizeTo cross-origin / some cases
    }
  }
}

function resetPopupSize() {
  popupSizeForm.width = DEFAULT_POPUP_WIDTH;
  popupSizeForm.height = DEFAULT_POPUP_HEIGHT;
}
const canPunch = computed(() => userStore.hasPermission("punch:edit"));
const canEdit = computed(() => userStore.hasPermission("telegram:edit"));
/** Matches backend IsAdminRole: only admin may punch for others. */
const lockToSelf = computed(() => !userStore.hasPermission("data:all"));

const moduleEnabled = ref(false);
const fineCurrency = ref("CNY");
const refreshing = ref(false);
const dualShiftEnabled = ref(true);
const tripleShiftEnabled = ref(false);
const activityTypes = ref<any[]>([]);
const loadingActivities = ref(false);
const employees = ref<any[]>([]);
const selectedEmployeeId = ref("");
const punching = ref(false);
const forceSelf = ref(false);
const linkedEmployeeId = ref("");
const linkedEmployeeName = ref("");
const canSelectEmployee = computed(() => !lockToSelf.value && !forceSelf.value);

const activitySessions = ref<any[]>([]);
const loadingSessions = ref(false);
const sessionTodayOnly = ref(true);
const sessionDate = ref(todayDateStr());
const sessionMonth = ref(currentYearMonthStr());
const sessionSearch = ref("");
let sessionSearchTimer: ReturnType<typeof setTimeout> | null = null;

/** Today's sessions for punch target employee (button counts + open activity). */
const punchUsageSessions = ref<any[]>([]);
const serverUsageCounts = ref<any[]>([]);
const nowTick = ref(Date.now());
let countdownTimer: ReturnType<typeof setInterval> | null = null;
/** Poll PC/TG activity open↔closed so countdown starts/stops without manual refresh. */
let remoteSyncTimer: ReturnType<typeof setInterval> | null = null;
const REMOTE_SYNC_MS = 15000;
let tabActive = true;
let bootReady = false;
/** Local wall-clock start for the current open activity (sync countdown, ignore server clock skew / RTT). */
const countdownLocalStartMs = ref<number | null>(null);
const countdownBoundSessionId = ref<string>("");

const ACTIVITY_PALETTE = [
  { bg: "linear-gradient(135deg, #f59e0b, #ea580c)", shadow: "rgba(245, 158, 11, 0.35)" },
  { bg: "linear-gradient(135deg, #8b5cf6, #6d28d9)", shadow: "rgba(139, 92, 246, 0.35)" },
  { bg: "linear-gradient(135deg, #06b6d4, #0891b2)", shadow: "rgba(6, 182, 212, 0.35)" },
  { bg: "linear-gradient(135deg, #ec4899, #db2777)", shadow: "rgba(236, 72, 153, 0.35)" },
  { bg: "linear-gradient(135deg, #14b8a6, #0d9488)", shadow: "rgba(20, 184, 166, 0.35)" },
  { bg: "linear-gradient(135deg, #6366f1, #4f46e5)", shadow: "rgba(99, 102, 241, 0.35)" },
];

const enabledActivities = computed(() =>
  (activityTypes.value || [])
    .filter((a) => a.enabled !== false)
    .slice()
    .sort((a, b) => (a.sortOrder || 0) - (b.sortOrder || 0)),
);

const sessionStats = computed(() => {
  const items = activitySessions.value;
  const totalFine = items.reduce((sum, row) => sum + (Number(row.fineAmount) || 0), 0);
  return {
    count: items.length,
    openCount: items.filter((r) => r.status === "open").length,
    totalFine: totalFine % 1 === 0 ? totalFine : Number(totalFine.toFixed(2)),
  };
});

const ongoingActivity = computed(() => {
  const fromUsage = punchUsageSessions.value.find((r) => r.status === "open");
  if (fromUsage) return fromUsage;
  return activitySessions.value.find((r) => r.status === "open") || null;
});

const ongoingCountdown = computed(() => {
  void nowTick.value;
  const sess = ongoingActivity.value;
  if (!sess) {
    return { text: "00:00", overtime: false };
  }
  const limitSec = Math.max(0, Number(sess.limitSeconds || 0));
  const started = resolveCountdownStartMs(sess);
  if (started == null) {
    return { text: "00:00", overtime: false };
  }
  const elapsed = Math.floor(Math.max(0, nowTick.value - started) / 1000);
  if (limitSec <= 0) {
    return { text: formatCountdownClock(elapsed), overtime: false };
  }
  const remain = limitSec - elapsed;
  if (remain >= 0) {
    return { text: formatCountdownClock(remain), overtime: false };
  }
  return { text: `+${formatCountdownClock(-remain)}`, overtime: true };
});

function formatCountdownClock(totalSec: number) {
  const sec = Math.max(0, Math.floor(Number(totalSec) || 0));
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

function bumpCountdownNow() {
  nowTick.value = Date.now();
}

/** Tick only while an activity is open and this tab is visible; otherwise no timer runs. */
function syncCountdownTimer() {
  const need = tabActive && !!ongoingActivity.value;
  if (need && !countdownTimer) {
    bumpCountdownNow();
    countdownTimer = setInterval(bumpCountdownNow, 250);
  } else if (!need && countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
}
watch(ongoingActivity, syncCountdownTimer);

function parseSessionStartedMs(sess: any): number | null {
  const raw = String(sess?.startedAt || "").trim();
  if (!raw) return null;
  const utc = dayjs.utc(raw);
  if (utc.isValid()) return utc.valueOf();
  const native = Date.parse(raw);
  return Number.isNaN(native) ? null : native;
}

function clearCountdownAnchor() {
  countdownLocalStartMs.value = null;
  countdownBoundSessionId.value = "";
}

function bindCountdownAnchor(sessionId: string, startMs: number) {
  countdownBoundSessionId.value = String(sessionId);
  countdownLocalStartMs.value = startMs;
  bumpCountdownNow();
}

/** Resolve absolute start time for countdown. Prefer server startedAt for existing sessions. */
function resolveCountdownStartMs(sess: any): number | null {
  const id = String(sess?.id || sess?.sessionId || "");
  const now = nowTick.value || Date.now();
  const parsed = parseSessionStartedMs(sess);

  // Reconstruct from live elapsedSeconds when opened late (backend fills this for open sessions).
  const elapsedSec = Number(sess?.elapsedSeconds);
  if (
    String(sess?.status || "") === "open" &&
    Number.isFinite(elapsedSec) &&
    elapsedSec > 0 &&
    (parsed == null || now - parsed < elapsedSec * 1000 - 1500)
  ) {
    // startedAt missing or too recent vs server elapsed → use now - elapsed.
    return now - Math.floor(elapsedSec) * 1000;
  }

  if (
    countdownLocalStartMs.value != null &&
    countdownBoundSessionId.value &&
    id &&
    (countdownBoundSessionId.value === id ||
      (countdownBoundSessionId.value.startsWith("local-") &&
        String(id).startsWith("local-")))
  ) {
    const local = countdownLocalStartMs.value;
    // Optimistic local punch: trust local start.
    if (countdownBoundSessionId.value.startsWith("local-")) {
      return Math.min(local, now);
    }
    // If local anchor is much later than server startedAt, it was wrongly rebound on page open.
    if (parsed != null && local > parsed + 2000) {
      return Math.min(parsed, now);
    }
    return Math.min(local, now);
  }

  if (parsed != null) {
    return Math.min(parsed, now);
  }
  if (
    String(sess?.status || "") === "open" &&
    Number.isFinite(elapsedSec) &&
    elapsedSec >= 0
  ) {
    return now - Math.floor(elapsedSec) * 1000;
  }
  return null;
}

function ensureCountdownAnchorForSession(sess: any) {
  if (!sess || sess.status !== "open") return;
  const id = String(sess.id || sess.sessionId || "");
  if (!id) return;
  if (countdownBoundSessionId.value === id && countdownLocalStartMs.value != null) {
    const parsedExisting = parseSessionStartedMs(sess);
    // Repair bad anchors that were set to "page open time".
    if (
      parsedExisting != null &&
      countdownLocalStartMs.value > parsedExisting + 2000
    ) {
      bindCountdownAnchor(id, parsedExisting);
    }
    return;
  }
  const parsed = parseSessionStartedMs(sess);
  const now = Date.now();
  if (parsed != null) {
    // Tiny future skew only (server clock ahead); never replace a past start with page-open now.
    const SKEW_MS = 3000;
    bindCountdownAnchor(id, parsed > now + SKEW_MS ? now : Math.min(parsed, now));
    return;
  }
  const elapsedSec = Number(sess?.elapsedSeconds);
  if (Number.isFinite(elapsedSec) && elapsedSec > 0) {
    bindCountdownAnchor(id, now - Math.floor(elapsedSec) * 1000);
    return;
  }
  // Brand-new session without startedAt (shouldn't happen for server rows).
  bindCountdownAnchor(id, now);
}

function applyOpenSessionFromResponse(data: any, localStartMs?: number) {
  if (!data) return;
  const id = data.id || data.sessionId;
  if (!id) return;
  const startMs = localStartMs ?? Date.now();
  const sess = {
    ...data,
    id,
    status: "open",
    startedAt: data.startedAt || new Date(startMs).toISOString(),
    limitSeconds: data.limitSeconds,
    activityName: data.activityName,
    activityCode: data.activityCode,
    employeeName: data.employeeName,
    employeeId: data.employeeId,
  };
  punchUsageSessions.value = [
    sess,
    ...punchUsageSessions.value.filter(
      (s) =>
        String(s.id) !== String(id) &&
        s.status !== "open" &&
        !String(s.id).startsWith("local-"),
    ),
  ];
  bindCountdownAnchor(String(id), startMs);
}

function beginOptimisticActivity(act: any, name: string) {
  const startMs = Date.now();
  const tempId = `local-${startMs}`;
  const empId = punchEmployeePayload();
  const emp =
    employees.value.find((e) => e.id === empId) ||
    (linkedEmployeeId.value === empId
      ? { id: empId, name: linkedEmployeeName.value }
      : null);
  const sess = {
    id: tempId,
    status: "open",
    startedAt: new Date(startMs).toISOString(),
    limitSeconds: Number(act?.timeLimitSeconds || 0),
    activityName: name,
    activityCode: act?.code || "",
    employeeName: emp?.name || linkedEmployeeName.value || "",
    employeeId: empId,
  };
  punchUsageSessions.value = [
    sess,
    ...punchUsageSessions.value.filter(
      (s) => s.status !== "open" && !String(s.id).startsWith("local-"),
    ),
  ];
  bindCountdownAnchor(tempId, startMs);
  return { tempId, startMs };
}

const usageCountByCode = computed(() => {
 const map:Record<string,number>={};for(const item of serverUsageCounts.value){map[item.code]=Number(item.count)||0;map[item.name]=Number(item.count)||0}return map;
});

function activityBtnStyle(idx: number) {
  const tone = ACTIVITY_PALETTE[idx % ACTIVITY_PALETTE.length];
  return {
    background: tone.bg,
    boxShadow: `0 8px 20px ${tone.shadow}`,
  };
}

function formatLimitHint(act: any) {
  const sec = Number(act.timeLimitSeconds || 0);
  if (sec <= 0) return "无时限";
  if (sec < 60) return `${sec}秒`;
  const m = Math.round(sec / 60);
  return `限时 ${m} 分钟`;
}

function formatActivityBtnHint(act: any) {
  return formatLimitHint(act);
}

function formatActivityCount(act: any) {
  const max = Number(act.maxTimesPerDay || 0);
  const used =
    usageCountByCode.value[String(act.code || "")] ||
    usageCountByCode.value[String(act.name || "")] ||
    0;
  if (max > 0) return `${used}/${max}`;
  return `${used}/∞`;
}

function formatSource(source?: string) {
  if (!source) return "-";
  if (source === "telegram") return "TG";
  if (source === "agent") return "PC";
  if (source === "web") return "web";
  if (source === "system") return "系统";
  return source;
}

function ensureEmployeeSelected() {
  if (lockToSelf.value) {
    if (!linkedEmployeeId.value) {
      ElMessage.warning("当前账号未关联员工，无法打卡");
      return false;
    }
    selectedEmployeeId.value = linkedEmployeeId.value;
    return true;
  }
  if (!selectedEmployeeId.value) {
    ElMessage.warning("请先选择员工");
    return false;
  }
  return true;
}

function punchEmployeePayload() {
  // forceSelf / linked: backend also resolves; still send for clarity
  return selectedEmployeeId.value || linkedEmployeeId.value || undefined;
}

async function onWorkPunch(shift: "day" | "middle" | "night") {
  if (!canPunch.value || punching.value || !ensureEmployeeSelected()) return;
  punching.value = true;
  try {
    const res = await workforceTelegramApi.punchCheckIn({
      employeeId: punchEmployeePayload(),
      shift,
    });
    const fallbackLabel =
      shift === "night" ? "夜班" : shift === "middle" ? "中班" : "白班";
    ElMessage.success(
      res.data?.messageText ||
        `上班打卡成功（${res.data?.shiftLabel || fallbackLabel}）`,
    );
    await loadActivitySessions();
    await loadPunchUsage();
  } catch (e: any) {
    ElMessage.error(e.message || "上班打卡失败");
  } finally {
    punching.value = false;
  }
}

async function onCheckoutPunch() {
  if (!canPunch.value || punching.value || !ensureEmployeeSelected()) return;
  punching.value = true;
  try {
    const res = await workforceTelegramApi.punchCheckOut({
      employeeId: punchEmployeePayload(),
    });
    ElMessage.success(res.data?.messageText || `下班打卡成功（${res.data?.shiftLabel || "-"}）`);
    await loadActivitySessions();
    await loadPunchUsage();
  } catch (e: any) {
    ElMessage.error(e.message || "下班打卡失败");
  } finally {
    punching.value = false;
  }
}

async function onActivityStart(name: string) {
  if (!canPunch.value || punching.value || !ensureEmployeeSelected()) return;
  punching.value = true;
  const act = enabledActivities.value.find((a) => a.name === name);
  const optimistic = beginOptimisticActivity(act, name);
  try {
    const res = await workforceTelegramApi.punchActivityStart({
      employeeId: punchEmployeePayload(),
      activity: name,
    });
    ElMessage.success(res.data?.messageText || `已开始活动：${name}`);
    // Keep counting from click time so network latency doesn't stall the clock.
    applyOpenSessionFromResponse(res.data, optimistic.startMs);
    await Promise.all([loadActivitySessions(), loadPunchUsage()]);
    // Preserve local start across reload (same activity still open).
    const open = punchUsageSessions.value.find((s) => s.status === "open");
    if (open) {
      bindCountdownAnchor(String(open.id), optimistic.startMs);
    }
    bumpCountdownNow();
  } catch (e: any) {
    punchUsageSessions.value = punchUsageSessions.value.filter(
      (s) => String(s.id) !== optimistic.tempId,
    );
    if (countdownBoundSessionId.value === optimistic.tempId) {
      clearCountdownAnchor();
    }
    ElMessage.error(e.message || "活动打卡失败");
  } finally {
    punching.value = false;
  }
}

async function onActivityBack() {
  if (!canPunch.value || punching.value || !ensureEmployeeSelected()) return;
  punching.value = true;
  try {
    const res = await workforceTelegramApi.punchActivityBack({
      employeeId: punchEmployeePayload(),
    });
    ElMessage.success(res.data?.messageText || "回座完成");
    punchUsageSessions.value = punchUsageSessions.value.filter((s) => s.status !== "open");
    clearCountdownAnchor();
    bumpCountdownNow();
    await Promise.all([loadActivitySessions(), loadPunchUsage()]);
  } catch (e: any) {
    ElMessage.error(e.message || "回座失败");
  } finally {
    punching.value = false;
  }
}

function goActivityConfig() {
  router.push({ path: "/attendance/telegram", query: { tab: "activities" } });
}

function openPunchPopup() {
  const href = router.resolve({ name: "TelegramActivityPunchPopup" }).href;
  const size = loadPopupSize();
  const features = [
    "popup=yes",
    `width=${size.width}`,
    `height=${size.height}`,
    "left=80",
    "top=60",
    "menubar=no",
    "toolbar=no",
    "location=no",
    "status=no",
    "resizable=yes",
    "scrollbars=yes",
  ].join(",");
  if (punchPopupWin && !punchPopupWin.closed) {
    try {
      punchPopupWin.resizeTo(size.width, size.height);
    } catch {
      // ignore
    }
    punchPopupWin.focus();
    return;
  }
  punchPopupWin = window.open(href, PUNCH_POPUP_NAME, features);
  if (!punchPopupWin) {
    ElMessage.warning("无法打开小窗口，请允许浏览器弹窗后重试");
    return;
  }
  try {
    punchPopupWin.focus();
  } catch {
    // ignore focus failures
  }
}

function persistPopupWindowSize() {
  if (!isPopupMode.value) return;
  savePopupSize(window.outerWidth, window.outerHeight);
}

function onPopupWindowResize() {
  if (popupResizeTimer) clearTimeout(popupResizeTimer);
  popupResizeTimer = setTimeout(persistPopupWindowSize, 400);
}

async function loadModuleFlag() {
  try {
    const { data } = await metaApi.modules();
    moduleEnabled.value = !!data?.telegramAttendance;
  } catch {
    moduleEnabled.value = false;
  }
}

async function loadSettings() {
  try {
    const res = await workforceTelegramApi.getSettings();
    dualShiftEnabled.value = !!res.data?.dualShiftEnabled || !!res.data?.tripleShiftEnabled;
    tripleShiftEnabled.value = !!res.data?.tripleShiftEnabled;
    fineCurrency.value = normalizeFineCurrency(res.data?.fineCurrency);
  } catch {
    dualShiftEnabled.value = true;
    tripleShiftEnabled.value = false;
    fineCurrency.value = "CNY";
  }
}

async function loadActivityTypes() {
  loadingActivities.value = true;
  try {
    const res = await workforceTelegramApi.listActivityTypes();
    activityTypes.value = res.data?.items || [];
  } catch (e: any) {
    activityTypes.value = [];
    ElMessage.error(e.message || "加载活动失败");
  } finally {
    loadingActivities.value = false;
  }
}

async function loadEmployees() {
  try {
    const res = await adminApi.getEmployees({ skip: 0, limit: 1000, search: "" });
    employees.value = (res.data?.items || []).map((emp: any) => ({
      id: emp.id,
      name: emp.name,
      employee_id: emp.employeeId || emp.employee_id || "",
    }));
  } catch {
    employees.value = [];
  }
}

async function loadPunchMe() {
  try {
    const res = await workforceTelegramApi.getPunchMe();
    const data = res.data || {};
    // Prefer server flag; also lock UI for any non-admin role.
    forceSelf.value = !!data.forceSelf || lockToSelf.value;
    if (data.linked && data.employeeId) {
      linkedEmployeeId.value = String(data.employeeId);
      linkedEmployeeName.value = data.employeeName || "";
      if (forceSelf.value || !selectedEmployeeId.value) {
        selectedEmployeeId.value = linkedEmployeeId.value;
      }
      // Ensure linked employee appears in options for admin dropdown.
      if (
        linkedEmployeeId.value &&
        !employees.value.some((e) => e.id === linkedEmployeeId.value)
      ) {
        employees.value.unshift({
          id: linkedEmployeeId.value,
          name: linkedEmployeeName.value || "本人",
          employee_id: data.employeeCode || "",
        });
      }
    } else {
      linkedEmployeeId.value = "";
      linkedEmployeeName.value = "";
      if (forceSelf.value) {
        selectedEmployeeId.value = "";
      }
    }
  } catch {
    forceSelf.value = lockToSelf.value;
    linkedEmployeeId.value = "";
    linkedEmployeeName.value = "";
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

async function loadActivitySessions(opts?: { silent?: boolean }) {
  const silent = !!opts?.silent;
  if (!silent) loadingSessions.value = true;
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
  } catch (e: any) {
    if (!silent) {
      activitySessions.value = [];
      ElMessage.error(e.message || "加载活动记录失败");
    }
  } finally {
    if (!silent) loadingSessions.value = false;
  }
}

async function loadPunchUsage() {
  const empId = punchEmployeePayload();
  if (!empId) {
    punchUsageSessions.value = [];
    serverUsageCounts.value = [];
    clearCountdownAnchor();
    return;
  }
  try {
    const res = await workforceTelegramApi.punchActivityUsage(empId);
    serverUsageCounts.value = res.data?.counts || [];
    const items = res.data?.openSession ? [res.data.openSession] : [];
    // Keep optimistic local open session until server open arrives.
    const localOpen = punchUsageSessions.value.find(
      (s) => s.status === "open" && String(s.id).startsWith("local-"),
    );
    const prevOpenId = String(
      punchUsageSessions.value.find((s) => s.status === "open")?.id ||
        countdownBoundSessionId.value ||
        "",
    );
    const serverOpen = items.find((s: any) => s.status === "open");
    punchUsageSessions.value = items;
    if (serverOpen) {
      const serverId = String(serverOpen.id || "");
      if (
        countdownLocalStartMs.value != null &&
        countdownBoundSessionId.value.startsWith("local-")
      ) {
        // Keep optimistic local start; remap id to server session.
        bindCountdownAnchor(serverId, countdownLocalStartMs.value);
      } else {
        // Existing / PC/TG open session → always align to server startedAt (not page-open time).
        ensureCountdownAnchorForSession(serverOpen);
      }
    } else if (localOpen && punching.value) {
      punchUsageSessions.value = [localOpen, ...items];
    } else {
      // PC/TG 回座 or closed → stop countdown.
      clearCountdownAnchor();
    }
  } catch {
    // keep current optimistic state on transient errors
  }
}

/** Quiet poll: pick up PC/TG activity start & 回座 for the selected employee. */
async function syncRemoteActivityState() {
  if (punching.value) return;
  if (typeof document !== "undefined" && document.hidden) return;
  await loadPunchUsage();
  // Keep the detail table roughly in sync when browsing today.
  if (
    !isPopupMode.value &&
    sessionTodayOnly.value &&
    (sessionDate.value || todayDateStr()) === todayDateStr()
  ) {
    await loadActivitySessions({ silent: true });
  }
}

function startRemoteSyncPolling() {
  stopRemoteSyncPolling();
  remoteSyncTimer = setInterval(() => {
    if (tabActive && !document.hidden) void syncRemoteActivityState();
  }, REMOTE_SYNC_MS);
}

function stopRemoteSyncPolling() {
  if (remoteSyncTimer) {
    clearInterval(remoteSyncTimer);
    remoteSyncTimer = null;
  }
}

function onVisibilityChange() {
  if (typeof document === "undefined") return;
  if (tabActive && !document.hidden) {
    void syncRemoteActivityState();
  }
}

async function refreshAll() {
  refreshing.value = true;
  try {
    const tasks: Promise<unknown>[] = [
      loadSettings(),
      loadActivityTypes(),
      loadEmployees(),
    ];
    if (!isPopupMode.value) tasks.push(loadActivitySessions());
    await Promise.all(tasks);
    await loadPunchMe();
    await loadPunchUsage();
    ElMessage.success("已刷新");
  } finally {
    refreshing.value = false;
  }
}

watch(
  () => [selectedEmployeeId.value, linkedEmployeeId.value, forceSelf.value],
  () => {
    clearCountdownAnchor();
    loadPunchUsage();
  },
);

onMounted(async () => {
  bumpCountdownNow();
  if (typeof document !== "undefined") {
    document.addEventListener("visibilitychange", onVisibilityChange);
  }
  await loadModuleFlag();
  const bootTasks: Promise<unknown>[] = [
    loadSettings(),
    loadActivityTypes(),
    loadEmployees(),
  ];
  if (!isPopupMode.value) bootTasks.push(loadActivitySessions());
  await Promise.all(bootTasks);
  await loadPunchMe();
  await loadPunchUsage();
  const open = ongoingActivity.value;
  if (open) ensureCountdownAnchorForSession(open);
  bumpCountdownNow();
  bootReady = true;
  if (tabActive) startRemoteSyncPolling();
  if (isPopupMode.value && typeof window !== "undefined") {
    window.addEventListener("resize", onPopupWindowResize);
  }
});

onActivated(() => {
  tabActive = true;
  syncCountdownTimer();
  if (bootReady) { bumpCountdownNow(); startRemoteSyncPolling(); void syncRemoteActivityState(); }
});
onDeactivated(() => {
  tabActive = false;
  stopRemoteSyncPolling();
  syncCountdownTimer();
});

onUnmounted(() => {
  stopRemoteSyncPolling();
  if (typeof document !== "undefined") {
    document.removeEventListener("visibilitychange", onVisibilityChange);
  }
  if (typeof window !== "undefined") {
    window.removeEventListener("resize", onPopupWindowResize);
  }
  if (popupResizeTimer) {
    clearTimeout(popupResizeTimer);
    popupResizeTimer = null;
  }
  if (countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
  if (sessionSearchTimer) {
    clearTimeout(sessionSearchTimer);
    sessionSearchTimer = null;
  }
});
</script>

<style scoped>
@import "./attendance.css";

.telegram-punch-page {
  position: relative;
  min-height: 100%;
}

.telegram-punch-page:not(.is-popup) {
  /* 页面允许向下滚动，保证记录表至少能看到 8–10 条 */
  min-height: calc(100vh - 164px);
  display: flex;
  flex-direction: column;
}

.telegram-punch-page:not(.is-popup) .punch-content {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 0;
}

.telegram-punch-page:not(.is-popup) .punch-main-card {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.telegram-punch-page:not(.is-popup) .punch-main-card :deep(.el-card__header) {
  flex-shrink: 0;
}

.telegram-punch-page:not(.is-popup) .punch-main-card :deep(.el-card__body) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding-bottom: 12px;
}

.telegram-punch-page:not(.is-popup) .punch-card-grid {
  flex-shrink: 0;
  margin-bottom: 12px;
}

.telegram-punch-page.is-popup {
  min-height: 100vh;
  background: linear-gradient(165deg, #ecfdf5 0%, #eff6ff 42%, #fff7ed 100%);
}

.telegram-punch-page.is-popup .punch-content {
  padding: 10px 10px 18px;
}

.telegram-punch-page.is-popup .punch-main-card {
  border-radius: 14px;
  box-shadow: 0 6px 20px rgba(15, 23, 42, 0.08);
}

.telegram-punch-page.is-popup .punch-title {
  font-size: 17px;
}

.telegram-punch-page.is-popup .punch-card-grid {
  grid-template-columns: 1fr;
  gap: 12px;
  margin-bottom: 0;
}

.telegram-punch-page.is-popup .punch-panel {
  padding: 14px;
}

.popup-status {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 14px;
  padding: 10px 14px;
  border-radius: 12px;
  background: linear-gradient(135deg, #f8fafc, #eff6ff);
  border: 1px solid rgba(15, 23, 42, 0.06);
  color: #64748b;
  font-size: 13px;
  font-weight: 600;
}

.popup-status.is-active {
  background: linear-gradient(135deg, #fff7ed, #fef3c7);
  border-color: rgba(245, 158, 11, 0.28);
  color: #9a3412;
}

.popup-status-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 700;
  color: #9a3412;
}

.popup-status-label {
  flex-shrink: 0;
  color: #78716c;
  font-weight: 600;
}

.popup-status-countdown {
  flex-shrink: 0;
  margin-left: auto;
  font-size: 20px;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.03em;
  color: #ea580c;
  font-weight: 800;
}

.popup-status-countdown.is-overtime {
  color: #dc2626;
}

.punch-blob-1 {
  background: linear-gradient(135deg, #10b981, #059669);
  opacity: 0.28;
}

.punch-blob-2 {
  background: linear-gradient(135deg, #3b82f6, #1d4ed8);
  opacity: 0.22;
}

.punch-blob-3 {
  background: linear-gradient(135deg, #f59e0b, #ef4444);
  opacity: 0.18;
}

.punch-content {
  position: relative;
  z-index: 1;
  width: 100%;
  padding: 8px 0 40px;
}

.punch-alert {
  margin-bottom: 16px;
  border-radius: 12px;
}

.punch-main-card {
  border-radius: 18px;
  border: 1px solid rgba(255, 255, 255, 0.65);
  background: rgba(255, 255, 255, 0.92);
  backdrop-filter: blur(16px);
  box-shadow: 0 8px 32px rgba(15, 23, 42, 0.08);
}

.punch-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.punch-title {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
  color: #0f172a;
}

.punch-subtitle {
  margin: 6px 0 0;
  font-size: 13px;
  color: #64748b;
  line-height: 1.5;
}

.punch-header-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}

.punch-popup-dropdown :deep(.el-button-group) {
  display: inline-flex;
  vertical-align: middle;
}

.popup-size-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: #64748b;
}

.self-punch-badge {
  display: inline-flex;
  align-items: center;
  min-height: 32px;
  padding: 0 14px;
  border-radius: 999px;
  background: linear-gradient(135deg, #ecfdf5, #eff6ff);
  border: 1px solid rgba(16, 185, 129, 0.25);
  color: #047857;
  font-size: 13px;
  font-weight: 600;
}

.punch-card-grid {
  display: grid;
  grid-template-columns: 1fr 1.2fr;
  gap: 18px;
  margin-bottom: 22px;
}

@media (max-width: 1100px) {
  .punch-card-grid {
    grid-template-columns: 1fr;
  }
}

.punch-panel {
  border-radius: 16px;
  padding: 18px;
  border: 1px solid rgba(15, 23, 42, 0.06);
  background: linear-gradient(180deg, #ffffff 0%, #f8fafc 100%);
}

.work-panel {
  background: linear-gradient(160deg, #f0fdf4 0%, #eff6ff 55%, #fff 100%);
}

.activity-panel {
  background: linear-gradient(160deg, #fffbeb 0%, #fdf4ff 50%, #fff 100%);
}

.panel-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.panel-head-text {
  flex: 1;
  min-width: 0;
}

.panel-icon {
  width: 42px;
  height: 42px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 20px;
  flex-shrink: 0;
}

.work-icon {
  background: linear-gradient(135deg, #10b981, #059669);
  box-shadow: 0 6px 14px rgba(16, 185, 129, 0.3);
}

.activity-icon {
  background: linear-gradient(135deg, #f59e0b, #ea580c);
  box-shadow: 0 6px 14px rgba(245, 158, 11, 0.3);
}

.panel-title {
  margin: 0;
  font-size: 16px;
  font-weight: 700;
  color: #0f172a;
}

.panel-desc {
  margin: 4px 0 0;
  font-size: 12px;
  color: #64748b;
}

.punch-btn-row,
.punch-btn-wrap {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.punch-btn-row.is-triple {
  flex-wrap: nowrap;
}

.punch-btn-row.is-triple .punch-btn {
  flex: 1 1 0;
  min-width: 0;
}

@media (max-width: 720px) {
  .punch-btn-row.is-triple {
    flex-wrap: wrap;
  }
  .punch-btn-row.is-triple .punch-btn {
    flex: 1 1 calc(50% - 6px);
    min-width: 118px;
  }
}

.punch-btn {
  min-width: 118px;
  min-height: 72px;
  padding: 12px 16px;
  border: none;
  border-radius: 14px;
  color: #fff;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  gap: 4px;
  transition: transform 0.15s ease, filter 0.15s ease;
}

.punch-btn:hover {
  transform: translateY(-2px);
  filter: brightness(1.05);
}

.punch-btn:active {
  transform: translateY(0);
}

.punch-btn-label {
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.punch-btn-hint {
  font-size: 11px;
  opacity: 0.88;
  font-weight: 500;
}

.punch-btn-count {
  margin-top: 2px;
  font-size: 12px;
  font-weight: 800;
  letter-spacing: 0.04em;
  opacity: 0.95;
  font-variant-numeric: tabular-nums;
}

.punch-btn-day {
  background: linear-gradient(135deg, #22c55e, #16a34a);
  box-shadow: 0 8px 20px rgba(34, 197, 94, 0.35);
}

.punch-btn-night {
  background: linear-gradient(135deg, #3b82f6, #1d4ed8);
  box-shadow: 0 8px 20px rgba(59, 130, 246, 0.38);
}

.punch-btn-end {
  background: linear-gradient(135deg, #f43f5e, #e11d48);
  box-shadow: 0 8px 20px rgba(244, 63, 94, 0.35);
}

.punch-btn-back {
  background: linear-gradient(135deg, #0ea5e9, #0284c7);
  box-shadow: 0 8px 20px rgba(14, 165, 233, 0.35);
}

.activity-empty {
  width: 100%;
  padding: 18px;
  text-align: center;
  color: #94a3b8;
  font-size: 13px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.7);
  border: 1px dashed #cbd5e1;
}

.records-panel {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #e2e8f0;
}

.telegram-punch-page:not(.is-popup) .records-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.telegram-punch-page:not(.is-popup) .records-head,
.telegram-punch-page:not(.is-popup) .records-stats {
  flex-shrink: 0;
}

.records-table-wrap {
  flex: 1;
  min-height: 180px;
  overflow: hidden;
}

/* 表头 + 约 10 行；屏幕更高时自动加高 */
.telegram-punch-page:not(.is-popup) .records-table-wrap {
  flex: none;
  height: max(520px, calc(100vh - 460px));
}

.records-table-wrap :deep(.el-table) {
  height: 100%;
}

.records-table-wrap :deep(.el-table__header-wrapper th) {
  background: #f8fafc !important;
}

.records-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 14px;
}

.records-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.records-stats {
  margin-bottom: 14px;
}

.mini-stat {
  border-radius: 12px;
  padding: 14px 16px;
  border: 1px solid rgba(15, 23, 42, 0.05);
  min-height: 72px;
  box-sizing: border-box;
}

.mini-stat-count {
  background: linear-gradient(135deg, #eff6ff, #f8fafc);
}

.mini-stat-open {
  background: linear-gradient(135deg, #fff7ed, #f8fafc);
}

.mini-stat-open-line {
  display: flex;
  align-items: baseline;
  gap: 10px;
  white-space: nowrap;
  overflow: hidden;
  min-width: 0;
}

.mini-stat-act-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mini-stat-remain-label {
  flex-shrink: 0;
  font-size: 13px;
  font-weight: 600;
  color: #64748b;
}

.mini-stat-countdown {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
  letter-spacing: 0.02em;
  color: #ea580c;
}

.mini-stat-countdown.is-overtime {
  color: #dc2626;
}

.mini-stat-fine {
  background: linear-gradient(135deg, #fef2f2, #f8fafc);
}

.mini-stat-value {
  font-size: 22px;
  font-weight: 800;
  color: #0f172a;
  line-height: 1.2;
}

.mini-stat-label {
  margin-top: 4px;
  font-size: 12px;
  color: #64748b;
}

.fine-text {
  color: #e11d48;
  font-weight: 700;
}

.muted-cell {
  color: #94a3b8;
}
.punch-btn:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
