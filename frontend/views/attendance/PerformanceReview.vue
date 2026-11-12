<template>
  <div class="attendance-management">
    <!-- 背景装饰 -->
    <div class="bg-decoration">
      <div class="blob blob-1"></div>
      <div class="blob blob-2"></div>
      <div class="blob blob-3"></div>
    </div>
    <div class="attendance-management">
      <el-card class="performance-card" shadow="never">
        <template #header>
          <div class="card-header">
            <div class="header-title">
              <div class="title-icon">
                <el-icon><TrendCharts /></el-icon>
              </div>
              <span class="title-text">绩效考核</span>
              <div class="info-badge" v-if="performanceMonth">
                <el-icon><Calendar /></el-icon>
                {{ formatMonthDisplay(performanceMonth) }}
              </div>
            </div>
            <div class="header-actions">
              <el-date-picker
                v-model="performanceMonth"
                type="month"
                placeholder="选择考核月份"
                format="YYYY年MM月"
                value-format="YYYY-MM"
                @change="loadPerformanceData"
                style="width: 160px"
                class="month-picker"
              />
              <el-input
                v-model="performanceSearch"
                placeholder="搜索员工"
                clearable
                style="width: 180px"
                :prefix-icon="Search"
                class="search-input"
              />
              <el-button v-permission="'performance:edit'"
                type="primary"
                @click="showAddScoreDialog"
                class="action-button"
              >
                <el-icon><Plus /></el-icon>添加绩效分
              </el-button>
              <el-button
                type="success"
                @click="handleRefresh"
                :loading="refreshing"
              >
                <el-icon><Refresh /></el-icon>刷新
              </el-button>
              <el-button v-permission="'performance:export'"
                @click="exportPerformance"
                class="action-button ghost-button"
              >
                <el-icon><Download /></el-icon>导出
              </el-button>
              <el-button
                v-permission="'performance:edit'"
                @click="openRuleSettingsDialog"
                class="action-button ghost-button"
              >
                <el-icon><Setting /></el-icon>设置
              </el-button>
              <el-button
                v-permission="'performance:edit'"
                @click="openPushConfigDrawer"
                class="action-button ghost-button"
              >
                <el-icon><Promotion /></el-icon>配置推送
              </el-button>
            </div>
          </div>
        </template>

        <!-- 考核说明 -->
        <div class="info-alert-wrapper">
          <el-alert
            type="info"
            :closable="false"
            show-icon
            class="modern-alert"
          >
            <template #default>
              <div class="alert-content">
                <div class="alert-item">
                  <span class="alert-badge">基础分</span>
                  <span>10分</span>
                </div>
                <div class="alert-item">
                  <span class="alert-badge success">加分项</span>
                  <span>表现优秀突出可加分</span>
                </div>
                <div class="alert-item">
                  <span class="alert-badge danger">扣分项</span>
                  <span>工作失误则扣分</span>
                </div>
              </div>
            </template>
          </el-alert>
        </div>

        <!-- 绩效考核表格 -->
        <el-table
          :data="filteredPerformanceData"
          stripe
          border
          style="width: 100%"
          class="modern-table"
          :header-cell-style="{
            background: 'linear-gradient(135deg, #f8fafc 0%, #f1f5f9 100%)',
            color: '#1e293b',
            fontWeight: '600',
          }"
        >
          <el-table-column type="index" label="#" width="60" align="center">
            <template #default="{ $index }">
              <span class="index-number">{{ $index + 1 }}</span>
            </template>
          </el-table-column>
          <el-table-column
            prop="employee_name"
            label="姓名"
            min-width="100"
            align="center"
          >
            <template #default="{ row }">
              <div class="employee-cell" @click="showEmployeeDetail(row)">
                <el-avatar
                  :size="32"
                  class="employee-avatar"
                  :style="{ background: getAvatarColor(row.employee_name) }"
                >
                  {{ row.employee_name?.charAt(0)?.toUpperCase() || "?" }}
                </el-avatar>
                <div class="employee-info">
                  <span class="employee-name-link">{{
                    row.employee_name
                  }}</span>
                </div>
              </div>
            </template>
          </el-table-column>
          <el-table-column
            prop="position"
            label="岗位"
            min-width="70"
            align="center"
          >
            <template #default="{ row }">
              <div class="position-wrapper">
                <span class="position-text">{{
                  row.position || "未分配"
                }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column
            prop="base_score"
            label="基础分"
            width="100"
            align="center"
          >
            <template #default>
              <div class="base-score-wrapper">
                <span class="base-score-value">10</span>
                <span class="base-score-unit">分</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="绩效记录" min-width="320">
            <template #default="{ row }">
              <div class="score-records">
                <div
                  v-for="(record, idx) in row.score_records"
                  :key="idx"
                  class="score-record-item"
                >
                  <div class="record-date-wrapper">
                    <el-icon><Calendar /></el-icon>
                    <span class="record-date">{{
                      formatShortDate(record.date)
                    }}</span>
                  </div>
                  <div
                    class="record-score"
                    :class="record.score >= 0 ? 'score-plus' : 'score-minus'"
                  >
                    {{
                      record.score >= 0
                        ? `+${record.score}`
                        : `${record.score}`
                    }}分
                  </div>
                  <div class="record-reason">
                    <el-icon><ChatLineSquare /></el-icon>
                    <span>{{ record.reason }}</span>
                  </div>
                  <div class="record-operator">
                    <el-icon><User /></el-icon>
                    <span>{{ getScoreRecordOperator(record) }}</span>
                  </div>
                  <el-button
                    v-permission="'performance:edit'"
                    link
                    type="primary"
                    size="small"
                    @click="editScoreRecord(row, idx)"
                    class="edit-record-btn"
                  >
                    <el-icon><Edit /></el-icon>
                  </el-button>
                  <el-button v-permission="'performance:delete'"
                    link
                    type="danger"
                    size="small"
                    @click="deleteScoreRecord(row, idx)"
                    class="delete-record-btn"
                  >
                    <el-icon><Delete /></el-icon>
                  </el-button>
                </div>
                <div v-if="!row.score_records?.length" class="empty-records">
                  <el-icon><Document /></el-icon>
                  <span>没有扣分，nice</span>
                </div>
              </div>
            </template>
          </el-table-column>

          <el-table-column
            prop="total_score"
            label="结余分数"
            width="120"
            align="center"
          >
            <template #default="{ row }">
              <div class="total-score-wrapper">
                <el-tag
                  :type="getScoreType(row.total_score)"
                  effect="dark"
                  class="score-tag"
                >
                  {{ row.total_score }}分
                </el-tag>
              </div>
            </template>
          </el-table-column>

          <el-table-column label="评级" width="100" align="center">
            <template #default="{ row }">
              <el-tag
                :type="getGradeType(row.grade)"
                effect="light"
                class="grade-tag"
                size="large"
              >
                {{ row.grade }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column
            label="操作"
            width="150"
            align="center"
            fixed="right"
          >
            <template #default="{ row }">
              <div class="action-buttons">
                <el-button v-permission="'performance:edit'"
                  link
                  type="primary"
                  size="small"
                  @click="showAddScoreDialogForEmployee(row)"
                  class="action-link"
                >
                  <el-icon><Edit /></el-icon>加减分
                </el-button>
                <!-- ✅ 改为跳转到罚款页面 -->
                <el-button
                  link
                  size="small"
                  @click="goToPenaltyPage(row.employee_name)"
                  class="action-link penalty-link"
                >
                  <el-icon><Warning /></el-icon>罚款
                </el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- 添加绩效分对话框 -->
      <el-dialog
        v-model="scoreDialogVisible"
        title="添加绩效分"
        width="540px"
        class="modern-dialog"
        :close-on-click-modal="false"
      >
        <el-form
          :model="scoreForm"
          :rules="scoreRules"
          ref="scoreFormRef"
          label-width="100px"
          label-position="right"
        >
          <el-form-item label="选择员工" prop="employee_id">
            <el-select
              v-model="scoreForm.employee_id"
              placeholder="选择员工"
              filterable
              style="width: 100%"
              @change="onScoreEmployeeChange"
              class="modern-select"
              popper-class="modern-select-popper"
            >
              <el-option
                v-for="emp in employees"
                :key="emp.id"
                :label="`${emp.name} (${emp.position || '未分配'})`"
                :value="emp.id"
              >
                <div class="select-option">
                  <el-avatar :size="24" class="option-avatar">
                    {{ emp.name?.charAt(0) }}
                  </el-avatar>
                  <span>{{ emp.name }}</span>
                  <span class="option-position">{{ emp.position }}</span>
                </div>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item label="员工岗位">
            <div class="info-field">
              <el-icon><Briefcase /></el-icon>
              <span class="info-text">{{ scoreForm.position || "-" }}</span>
            </div>
          </el-form-item>
          <el-form-item label="当前分数">
            <div class="info-field">
              <el-icon><TrendCharts /></el-icon>
              <span class="info-text current-score"
                >{{ scoreForm.current_score }}分</span
              >
            </div>
          </el-form-item>
          <el-form-item label="加减分数" prop="score">
            <div class="score-input-wrapper">
              <el-input-number
                v-model="scoreForm.score"
                :min="-50"
                :max="50"
                :step="1"
                placeholder="正数为加分，负数为扣分"
                style="width: 100%"
                class="modern-input-number"
                controls-position="right"
              />
              <span class="help-text">
                <span class="help-positive">正数表示加分</span>，
                <span class="help-negative">负数表示扣分</span>
              </span>
            </div>
          </el-form-item>
          <el-form-item label="结余分数">
            <div class="info-field highlight-field">
              <el-icon><Star /></el-icon>
              <span class="info-text highlight"
                >{{ scoreForm.current_score + (scoreForm.score || 0) }}分</span
              >
            </div>
          </el-form-item>
          <el-form-item label="原因" prop="reason">
            <el-input
              v-model="scoreForm.reason"
              type="textarea"
              :rows="3"
              placeholder="请输入加减分原因"
              class="modern-textarea"
              maxlength="200"
              show-word-limit
            />
          </el-form-item>
          <el-form-item label="日期" prop="date">
            <el-date-picker
              v-model="scoreForm.date"
              type="date"
              placeholder="选择日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
              class="modern-datepicker"
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <div class="dialog-footer">
            <el-button @click="scoreDialogVisible = false" class="cancel-button"
              >取消</el-button
            >
            <el-button v-permission="'performance:edit'"
              type="primary"
              @click="confirmAddScore"
              :loading="scoreSubmitting"
              class="submit-button"
            >
              <el-icon><Check /></el-icon>确定添加
            </el-button>
          </div>
        </template>
      </el-dialog>

      <!-- 绩效推送配置抽屉 -->
      <el-drawer
        v-model="pushConfigVisible"
        title="绩效推送配置"
        size="480px"
        class="performance-push-drawer"
        @open="loadPushSettings"
      >
        <div class="push-config-body">
          <section class="push-config-section push-enable-section">
            <div class="push-enable-row">
              <span class="push-section-title">启用自动推送</span>
              <el-switch
 v-permission="'performance:notify'"                v-model="pushSettings.enabled"
                :loading="pushEnabledSaving"
                @change="savePushEnabled"
              />
            </div>
            <p class="push-help-text">
              <template v-if="pushSettings.enabled && pushSettings.ready">
                已开启，新增绩效记录将自动推送到 Telegram
              </template>
              <template v-else-if="pushSettings.enabled">
                已开启，请先配置 Bot Token 并绑定频道/群组
              </template>
              <template v-else>
                已关闭，新增绩效记录不会推送到 Telegram
              </template>
            </p>
          </section>

          <el-alert
            type="info"
            :closable="false"
            show-icon
            class="push-config-tip"
            title="请先配置 Bot Token 与推送目标，再开启自动推送"
          />

          <section class="push-config-section">
            <div class="push-section-title">1. Bot Token</div>
            <el-input
              v-model="pushTokenInput"
              type="password"
              show-password
              placeholder="请输入 Telegram Bot Token"
              class="push-token-input"
            />
            <div class="push-section-actions">
              <el-button
 v-permission="'performance:notify'"                type="primary"
                :loading="pushTokenSaving"
                @click="savePushToken"
              >
                保存 Token
              </el-button>
              <el-tag v-if="pushSettings.hasBotToken" type="success" size="small">
                @{{ pushSettings.botUsername || "已配置" }}
              </el-tag>
            </div>
          </section>

          <section class="push-config-section">
            <div class="push-section-title">2. 绑定频道 / 群组</div>
            <el-button
 v-permission="'performance:notify'"              type="primary"
              plain
              :loading="pushDiscovering"
              :disabled="!pushSettings.hasBotToken"
              @click="discoverPushChats"
            >
              <el-icon><Search /></el-icon>
              发现频道或群组
            </el-button>
            <p class="push-help-text">
              将机器人拉入目标群/频道即可发现；若列表为空可发一条消息后重试
            </p>
            <div v-if="pushSettings.chatId" class="push-bound-card">
              <div class="push-bound-main">
                <el-icon><ChatLineSquare /></el-icon>
                <div>
                  <div class="push-bound-title">
                    {{ pushSettings.chatTitle || pushSettings.chatId }}
                  </div>
                  <div class="push-bound-meta">
                    Chat ID: {{ pushSettings.chatId }}
                    <span v-if="pushSettings.chatType">
                      · {{ formatPushChatType(pushSettings.chatType) }}
                    </span>
                  </div>
                </div>
              </div>
              <div class="push-bound-actions">
                <el-button
 v-permission="'performance:notify'"                  size="small"
                  :loading="pushTesting"
                  @click="testPushConfig"
                >
                  测试推送
                </el-button>
                <el-button
 v-permission="'performance:notify'"                  size="small"
                  type="danger"
                  plain
                  @click="unbindPushChat"
                >
                  解绑
                </el-button>
              </div>
            </div>
            <el-empty
              v-else
              description="尚未绑定推送目标"
              :image-size="72"
            />
          </section>

          <section class="push-config-section">
            <div class="push-section-title">推送内容示例</div>
            <div class="push-preview-card">
              <div><b>日期</b>：2025-06-04</div>
              <div><b>员工</b>：张三</div>
              <div><b>岗位</b>：客服</div>
              <div><b>变动</b>：+2分（加分）</div>
              <div><b>原因</b>：表现优秀</div>
            </div>
          </section>
        </div>
      </el-drawer>

      <el-dialog
        v-model="ruleSettingsVisible"
        title="绩效起算设置"
        width="480px"
        class="modern-dialog"
      >
        <el-form label-width="180px">
          <el-form-item label="入职截止日（当月）">
            <el-input-number
              v-model="ruleSettingsForm.hireCutoffDay"
              :min="1"
              :max="31"
              style="width: 160px"
            />
          </el-form-item>
        </el-form>
        <div class="push-help-text">
          规则：入职当月不计绩效；入职日小于等于截止日，则下个月开始计绩效；否则下下个月开始计绩效。
        </div>
        <template #footer>
          <el-button @click="ruleSettingsVisible = false">取消</el-button>
          <el-button v-permission="'performance:edit'" type="primary" :loading="ruleSettingsSaving" @click="saveRuleSettings">
            保存
          </el-button>
        </template>
      </el-dialog>

      <!-- 发现群组对话框 -->
      <el-dialog
        v-model="pushDiscoverVisible"
        title="发现频道 / 群组"
        width="620px"
        class="modern-dialog"
      >
        <el-alert
          type="info"
          :closable="false"
          show-icon
          title="选择要绑定的频道或群组，绑定后新增绩效将推送到该目标"
          style="margin-bottom: 12px"
        />
        <el-table
          :data="pushDiscoveredChats"
          stripe
          max-height="360"
          v-loading="pushDiscovering"
        >
          <el-table-column prop="groupName" label="名称" min-width="180" />
          <el-table-column prop="chatId" label="Chat ID" width="160" />
          <el-table-column prop="chatType" label="类型" width="110" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="light">
                {{ formatPushChatType(row.chatType) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100" align="center">
            <template #default="{ row }">
              <el-button
 v-permission="'performance:notify'"                link
                type="primary"
                :loading="pushBindingChatId === row.chatId"
                @click="bindPushChat(row)"
              >
                绑定
              </el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-empty
          v-if="!pushDiscovering && !pushDiscoveredChats.length"
          description="未发现频道或群组"
          :image-size="64"
        />
        <template #footer>
          <el-button v-permission="'performance:notify'" @click="pushDiscoverVisible = false">关闭</el-button>
          <el-button v-permission="'performance:notify'" type="primary" :loading="pushDiscovering" @click="discoverPushChats">
            重新发现
          </el-button>
        </template>
      </el-dialog>

      <!-- ✅ 编辑绩效分对话框 -->
      <el-dialog
        v-model="editScoreDialogVisible"
        title="编辑绩效分"
        width="540px"
        class="modern-dialog"
        :close-on-click-modal="false"
      >
        <el-form
          :model="editScoreForm"
          :rules="editScoreRules"
          ref="editScoreFormRef"
          label-width="100px"
          label-position="right"
        >
          <el-form-item label="员工姓名">
            <div class="info-field">
              <el-icon><User /></el-icon>
              <span class="info-text">{{ editScoreForm.employee_name }}</span>
            </div>
          </el-form-item>

          <el-form-item label="当前分数">
            <div class="info-field">
              <span class="info-text current-score"
                >{{ editScoreForm.oldScore }}分</span
              >
            </div>
          </el-form-item>

          <el-form-item label="修改分数" prop="score">
            <div class="score-input-wrapper">
              <el-input-number
                v-model="editScoreForm.score"
                :min="-50"
                :max="50"
                :step="1"
                placeholder="正数为加分，负数为扣分"
                style="width: 100%"
                controls-position="right"
              />
              <span class="help-text">
                <span class="help-positive"
                  >原分: {{ editScoreForm.oldScore }}</span
                >
                <span class="help-separator">→</span>
                <span class="help-new">新分: {{ editScoreForm.score }}</span>
                <span class="help-diff">
                  ({{
                    editScoreForm.score - editScoreForm.oldScore >= 0
                      ? "+"
                      : ""
                  }}{{ editScoreForm.score - editScoreForm.oldScore }})
                </span>
              </span>
            </div>
          </el-form-item>

          <el-form-item label="原因" prop="reason">
            <el-input
              v-model="editScoreForm.reason"
              type="textarea"
              :rows="3"
              placeholder="请输入加减分原因"
              maxlength="200"
              show-word-limit
            />
          </el-form-item>

          <el-form-item label="日期" prop="date">
            <el-date-picker
              v-model="editScoreForm.date"
              type="date"
              placeholder="选择日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
            />
          </el-form-item>
        </el-form>

        <template #footer>
          <div class="dialog-footer">
            <el-button @click="editScoreDialogVisible = false">取消</el-button>
            <el-button v-permission="'performance:edit'"
              type="primary"
              @click="confirmEditScore"
              :loading="editScoreSubmitting"
            >
              <el-icon><Check /></el-icon>保存修改
            </el-button>
          </div>
        </template>
      </el-dialog>

      <!-- 员工详情对话框 -->
      <el-dialog
        v-model="detailDialogVisible"
        title="员工详细信息"
        width="600px"
        class="employee-detail-dialog"
        :close-on-click-modal="false"
      >
        <div class="detail-container" v-if="currentEmployee">
          <!-- 头部信息 -->
          <div class="detail-header">
            <div class="avatar-section">
              <el-avatar :size="64" :icon="User" class="employee-avatar" />
              <div class="employee-title">
                <h3 class="employee-name">{{ currentEmployee.name }}</h3>
                <div class="employee-badges">
                  <el-tag size="small" type="primary">{{
                    currentEmployee.position || "员工"
                  }}</el-tag>
                  <el-tag
                    size="small"
                    :type="
                      currentEmployee.workLocation === '现场'
                        ? 'success'
                        : 'warning'
                    "
                  >
                    {{ currentEmployee.workLocation || "现场" }}
                  </el-tag>
                </div>
              </div>
            </div>
            <div class="employee-id">
              <el-icon><OfficeBuilding /></el-icon>
              <span>工号: {{ currentEmployee.employee_id || "-" }}</span>
            </div>
          </div>

          <el-divider style="margin: 12px 0" />

          <!-- 统计卡片区域 -->
          <div class="detail-stats">
            <!-- 入职日期卡片 -->
            <div class="stat-card-item">
              <div class="stat-card-icon" style="background: #e6f7ff">
                <el-icon><Calendar /></el-icon>
              </div>
              <div class="stat-card-info">
                <div class="stat-card-label">入职日期</div>
                <div class="stat-card-value">
                  {{ formatDate2(currentEmployee.hireDate) }}
                </div>
              </div>
            </div>

            <!-- 本月考勤卡片 -->
            <div class="stat-card-item">
              <div class="stat-card-icon" style="background: #f6ffed">
                <el-icon><DataLine /></el-icon>
              </div>
              <div class="stat-card-info">
                <div class="stat-card-label">本月考勤</div>
                <div class="stat-card-value">
                  <span class="stat-work">{{ currentWorkDays }}天</span>
                  <span class="stat-divider">/</span>
                  <span class="stat-leave">{{ currentLeaveRestDays }}天</span>
                </div>
              </div>
            </div>

            <!-- 本月绩效卡片（带添加按钮） -->
            <div class="stat-card-item">
              <div class="stat-card-icon" style="background: #f9f0ff">
                <el-icon><TrendCharts /></el-icon>
              </div>
              <div class="stat-card-info">
                <div class="stat-card-label">
                  本月绩效
                  <el-tooltip content="添加绩效分" placement="top">
                    <el-button v-permission="'performance:edit'"
                      class="add-card-btn"
                      size="small"
                      circle
                      @click="showScoreDialogFromDetail"
                    >
                      <el-icon><Plus /></el-icon>
                    </el-button>
                  </el-tooltip>
                </div>
                <div class="stat-card-value">
                  <el-tag
                    :type="getScoreType(currentPerformance?.total_score || 10)"
                    size="small"
                    effect="dark"
                  >
                    {{ currentPerformance?.total_score || 10 }}分
                  </el-tag>
                  <el-tag
                    :type="getGradeType(currentPerformance?.grade || '合格')"
                    size="small"
                  >
                    {{ currentPerformance?.grade || "合格" }}
                  </el-tag>
                </div>
              </div>
            </div>

            <!-- 本月罚款卡片（带添加按钮） -->
            <div class="stat-card-item">
              <div class="stat-card-icon" style="background: #fff7e6">
                <el-icon><Money /></el-icon>
              </div>
              <div class="stat-card-info">
                <div class="stat-card-label">
                  本月罚款
                  <el-tooltip content="添加罚款" placement="top">
                    <el-button v-permission="'penalty:edit'"
                      class="add-card-btn"
                      size="small"
                      circle
                      @click="showPenaltyDialogFromDetail"
                    >
                      <el-icon><Plus /></el-icon>
                    </el-button>
                  </el-tooltip>
                </div>
                <div class="stat-card-value">
                  <span
                    :class="currentPenaltyTotal > 0 ? 'penalty-amount' : ''"
                  >
                    {{ formatFineAmount(currentPenaltyTotal, fineCurrency) }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- 绩效记录区域 -->
          <div
            v-if="currentPerformance?.score_records?.length > 0"
            class="records-section"
          >
            <el-divider style="margin: 12px 0">
              <span class="divider-text">绩效记录</span>
            </el-divider>
            <div class="records-list">
              <div
                v-for="(record, idx) in currentPerformance.score_records.slice(
                  -5,
                )"
                :key="idx"
                class="record-item"
              >
                <div class="record-date-badge">
                  <span class="record-day">{{
                    formatShortDate(record.date)
                  }}</span>
                </div>
                <div class="record-content">
                  <span
                    :class="[
                      'record-score',
                      record.score >= 0 ? 'score-plus' : 'score-minus',
                    ]"
                  >
                    {{
                      record.score >= 0
                        ? `+${record.score}`
                        : `${record.score}`
                    }}分
                  </span>
                  <span class="record-reason">{{ record.reason }}</span>
                  <span class="record-operator-inline">{{
                    getScoreRecordOperator(record)
                  }}</span>
                </div>
              </div>
            </div>
          </div>

          <!-- 罚款记录区域 -->
          <div v-if="currentPenaltyRecords.length > 0" class="records-section">
            <el-divider style="margin: 12px 0">
              <span class="divider-text">罚款记录</span>
            </el-divider>
            <div class="records-list">
              <div
                v-for="(record, idx) in currentPenaltyRecords.slice(-5)"
                :key="idx"
                class="record-item"
              >
                <div class="record-date-badge penalty-date">
                  <span>{{
                    record.penaltyDate || record.penalty_date || "-"
                  }}</span>
                </div>
                <div class="record-content">
                  <span class="penalty-amount-badge">{{ formatFineAmount(record.amount, fineCurrency) }}</span>
                  <span class="record-reason">{{ record.reason }}</span>
                  <span class="record-operator-inline">{{
                    getPenaltyCreatorName(record)
                  }}</span>
                  <span class="record-category" v-if="record.category">
                    <el-tag
                      size="small"
                      :type="getPenaltyCategoryType(record.category)"
                      effect="plain"
                    >
                      {{ record.category }}
                    </el-tag>
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- 空状态 -->
          <div
            v-if="
              (!currentPerformance?.score_records?.length ||
                currentPerformance.score_records.length === 0) &&
              currentPenaltyRecords.length === 0
            "
            class="empty-state"
          >
            <el-empty description="暂无绩效和罚款记录" :image-size="80" />
          </div>
        </div>

        <template #footer>
          <div class="dialog-footer">
            <el-button @click="detailDialogVisible = false">关闭</el-button>
            <el-button type="primary" @click="editEmployeeFromDetail">
              <el-icon><Edit /></el-icon>编辑员工
            </el-button>
            <el-button v-permission="'attendance:delete'" type="danger" plain @click="deleteEmployeeFromDetail">
              <el-icon><Delete /></el-icon>删除员工
            </el-button>
          </div>
        </template>
      </el-dialog>

      <!-- 编辑员工对话框 -->
      <el-dialog
        v-model="employeeDialogVisible"
        :title="employeeDialogTitle"
        width="500px"
      >
        <el-form
          :model="employeeForm"
          :rules="employeeRules"
          ref="employeeFormRef"
          label-width="100px"
        >
          <el-form-item label="姓名" prop="name">
            <el-input v-model="employeeForm.name" placeholder="请输入姓名" />
          </el-form-item>
          <el-form-item label="工号" prop="employee_id">
            <el-input
              v-model="employeeForm.employee_id"
              placeholder="与登录账号一致"
            />
          </el-form-item>
          <el-form-item label="岗位" prop="position">
            <el-input
              v-model="employeeForm.position"
              placeholder="请输入岗位"
            />
          </el-form-item>
          <el-form-item label="入职日期" prop="hire_date">
            <el-date-picker
              v-model="employeeForm.hire_date"
              type="date"
              placeholder="选择入职日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item label="办公地点" prop="work_location">
            <el-select
              v-model="employeeForm.work_location"
              filterable
              allow-create
              default-first-option
              placeholder="请选择或输入办公地点"
              style="width: 100%"
            >
              <el-option label="现场" value="现场" />
              <el-option label="越南/居家" value="越南/居家" />
              <el-option label="缅甸/居家" value="缅甸/居家" />
            </el-select>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="employeeDialogVisible = false">取消</el-button>
          <el-button v-permission="'attendance:edit'"
            type="primary"
            @click="submitEmployeeEdit"
            :loading="submittingEmployee"
          >
            确定
          </el-button>
        </template>
      </el-dialog>

      <!-- 添加罚款对话框 -->
      <el-dialog
        v-model="penaltyDialogVisible"
        title="添加罚款记录"
        width="500px"
        :append-to-body="true"
        @close="resetPenaltyForm"
      >
        <el-form
          :model="penaltyForm"
          :rules="penaltyRules"
          ref="penaltyFormRef"
          label-width="100px"
        >
          <el-form-item label="员工">
            <span>{{ currentEmployee?.name }}</span>
          </el-form-item>
          <el-form-item label="罚款日期" prop="penalty_date">
            <el-date-picker
              v-model="penaltyForm.penalty_date"
              type="date"
              placeholder="选择罚款日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item label="罚款金额" prop="amount">
            <el-input-number
              v-model="penaltyForm.amount"
              :min="0"
              :step="10"
              style="width: 100%"
              controls-position="right"
            />
          </el-form-item>
          <el-form-item label="罚款类型" prop="category">
            <el-select
              v-model="penaltyForm.category"
              filterable
              allow-create
              default-first-option
              placeholder="请选择或输入罚款类型"
              style="width: 100%"
            >
              <el-option label="迟到" value="迟到" />
              <el-option label="早退" value="早退" />
              <el-option label="旷工" value="旷工" />
              <el-option label="小厕超时" value="小厕超时" />
              <el-option label="大厕超时" value="大厕超时" />
              <el-option label="吃饭超时" value="吃饭超时" />
              <el-option label="抽烟或休息超时" value="抽烟或休息超时" />
              <el-option label="其他" value="其他" />
            </el-select>
          </el-form-item>
          <el-form-item label="罚款原因" prop="reason">
            <el-input
              v-model="penaltyForm.reason"
              type="textarea"
              :rows="3"
              placeholder="请输入罚款原因"
              maxlength="200"
              show-word-limit
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="penaltyDialogVisible = false">取消</el-button>
          <el-button v-permission="'penalty:edit'"
            type="primary"
            @click="confirmAddPenalty"
            :loading="penaltySubmitting"
          >
            确定添加
          </el-button>
        </template>
      </el-dialog>
    </div>
  </div>
</template>

<script setup lang="ts">
import request from "@api/request";
import { ref, computed, onMounted, onUnmounted, watch } from "vue";
import { useRouter, useRoute } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Check,
  Download,
  Plus,
  Edit,
  Delete,
  TrendCharts,
  Search,
  Calendar,
  ChatLineSquare,
  Document,
  Warning,
  Briefcase,
  Star,
  User,
  Money,
  DataLine,
  OfficeBuilding,
  Refresh,
  Promotion,
  Setting,
} from "@element-plus/icons-vue";
import adminApi from "@api/admin_api";
import { userApi } from "@api/index";
import workforceTelegramApi from "@api/workforce_telegram_api";
import {
  getCurrentYearMonth,
  getToday,
  getScoreType,
  getGradeType,
  getPenaltyCategoryType,
  getScoreRecordOperator,
  getPenaltyCreatorName,
} from "./helpers";
import {
  formatFineAmount,
  normalizeFineCurrency,
  type FineCurrencyCode,
} from "./telegram_helpers";

const router = useRouter();
const route = useRoute();

const fineCurrency = ref<FineCurrencyCode>("CNY");

async function loadFineCurrency() {
  try {
    const res = await workforceTelegramApi.getSettings();
    fineCurrency.value = normalizeFineCurrency(res.data?.fineCurrency);
  } catch {
    fineCurrency.value = "CNY";
  }
}

const refreshing = ref(false);
const currentOperatorName = ref("");
const performanceHireCutoffDay = ref(15);
const ruleSettingsVisible = ref(false);
const ruleSettingsSaving = ref(false);
const ruleSettingsForm = ref({
  hireCutoffDay: 15,
});

const getOperatorName = () => {
  return currentOperatorName.value.trim() || "-";
};

const loadCurrentOperatorName = async () => {
  try {
    const { data } = await userApi.getProfile();
    const realName = data.realName?.trim();
    currentOperatorName.value = realName || data.username || "";
  } catch (error) {
    console.error("加载操作者信息失败:", error);
    currentOperatorName.value = "";
  }
};

// ==================== 员工详情相关 ====================
const detailDialogVisible = ref(false);
const currentEmployee = ref<any>(null);
const currentPerformance = ref<any>(null);
const currentPenaltyTotal = ref(0);
const currentPenaltyRecords = ref<any[]>([]);
const currentWorkDays = ref(0);
const currentLeaveRestDays = ref(0);

// ==================== 绩效考核相关 ====================
const performanceMonth = ref(getCurrentYearMonth());
const performanceSearch = ref("");
const performanceData = ref<any[]>([]);
const savingPerformance = ref(false);
const scoreDialogVisible = ref(false);
const scoreFormRef = ref<any>(null);
const employees = ref<any[]>([]);
const scoreForm = ref({
  employee_id: "",
  position: "",
  current_score: 10,
  score: 0,
  reason: "",
  date: getToday(),
});
const scoreRules = {
  employee_id: [{ required: true, message: "请选择员工", trigger: "change" }],
  score: [{ required: true, message: "请输入加减分数", trigger: "blur" }],
  reason: [{ required: true, message: "请输入原因", trigger: "blur" }],
  date: [{ required: true, message: "请选择日期", trigger: "change" }],
};

// ==================== 员工编辑对话框相关 ====================
const employeeDialogVisible = ref(false);
const employeeDialogTitle = ref("编辑员工");
const employeeFormRef = ref<any>(null);
const submittingEmployee = ref(false);
const employeeForm = ref({
  id: null,
  name: "",
  employee_id: "",
  position: "",
  hire_date: "",
  work_location: "现场",
});
const employeeRules = {
  name: [{ required: true, message: "请输入姓名", trigger: "blur" }],
  employee_id: [{ required: true, message: "请输入工号", trigger: "blur" }],
  hire_date: [{ required: true, message: "请选择入职日期", trigger: "change" }],
};

// ==================== 罚款对话框相关 ====================
const penaltyDialogVisible = ref(false);
const penaltyFormRef = ref<any>(null);
const penaltySubmitting = ref(false);
const penaltyForm = ref({
  employee_id: "",
  penalty_date: "",
  amount: 0,
  category: "迟到",
  reason: "",
});
const penaltyRules = {
  penalty_date: [
    { required: true, message: "请选择罚款日期", trigger: "change" },
  ],
  amount: [{ required: true, message: "请输入罚款金额", trigger: "blur" }],
  reason: [{ required: true, message: "请输入罚款原因", trigger: "blur" }],
};

// ==================== 辅助函数 ====================
const formatMonthDisplay = (month: string) => {
  if (!month) return "";
  const [year, monthNum] = month.split("-");
  return `${year}年${parseInt(monthNum)}月`;
};

const formatShortDate = (date: string) => {
  if (!date) return "";
  const parts = date.split("-");
  if (parts.length === 3) {
    return `${parts[1]}/${parts[2]}`;
  }
  return date;
};

const formatDate2 = (date: string | null | undefined) => {
  if (!date) return "-";
  const str = String(date);
  if (str.includes("-")) return str.substring(0, 10);
  const d = new Date(date);
  if (isNaN(d.getTime())) return "-";
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
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

// ==================== 刷新数据 ====================
const handleRefresh = async () => {
  refreshing.value = true;
  try {
    await loadEmployees();
    await loadPerformanceData();
    ElMessage.success("数据已刷新");
  } catch (error) {
    ElMessage.error("刷新失败");
  } finally {
    refreshing.value = false;
  }
};

// ==================== 计算属性 ====================
const filteredPerformanceData = computed(() => {
  if (!performanceSearch.value) return performanceData.value;
  const keyword = performanceSearch.value.toLowerCase();
  return performanceData.value.filter((p) =>
    p.employee_name?.toLowerCase().includes(keyword),
  );
});

// ==================== 加载员工考勤数据 ====================
const loadEmployeeAttendance = async (employeeId: string) => {
  // 添加参数校验
  if (!employeeId) {
    console.warn("employeeId is required");
    currentWorkDays.value = 0;
    currentLeaveRestDays.value = 0;
    return;
  }

  try {
    let month = performanceMonth.value;
    if (!month) {
      month = getCurrentYearMonth();
    }

    const response = await adminApi.getRecordsByEmployees(month, [employeeId]);
    const data = response.data || {};
    const empKey = String(employeeId);
    const records = data[empKey] || {};

    let workDays = 0;
    let leaveRestDays = 0;

    for (const record of Object.values(records)) {
      const status = (record as any)?.status;
      if (status === "work") {
        workDays += 1;
      } else if (status === "rest_half" || status === "leave") {
        workDays += 0.5;
        leaveRestDays += 0.5;
      } else if (
        status === "rest_full" ||
        status === "off_post" ||
        status === "absent"
      ) {
        leaveRestDays += 1;
      }
    }

    currentWorkDays.value = workDays;
    currentLeaveRestDays.value = leaveRestDays;
  } catch (error) {
    console.error("加载考勤数据失败:", error);
    currentWorkDays.value = 0;
    currentLeaveRestDays.value = 0;
  }
};

// ==================== 监听路由参数 ====================
watch(
  () => route.query.employee,
  async (value) => {
    if (value) {
      performanceSearch.value = String(value);
      await loadEmployees();
      await loadPerformanceData();
    }
  },
  { immediate: true },
);

watch(performanceSearch, async () => {
  await loadEmployees();
  await loadPerformanceData();
});

watch(performanceMonth, async () => {
  await loadPerformanceData();
});

// ==================== 加载员工列表 ====================
const loadEmployees = async () => {
  try {
    const response = await adminApi.getEmployees({
      skip: 0,
      limit: 1000,
      search: performanceSearch.value,
      employment_scope: "month_visible",
      year_month: performanceMonth.value || getCurrentYearMonth(),
      performance_eligibility: "true",
      performance_hire_cutoff_day: performanceHireCutoffDay.value,
    });

    const items = response.data?.items || [];
    employees.value = items.map((emp: any) => ({
      id: emp.id,
      name: emp.name,
      position: emp.position || "-",
      employeeId: emp.employeeId || emp.employee_id,
      workLocation: emp.workLocation || emp.work_location || "现场", // ✅
      hireDate: emp.hireDate || emp.hire_date,
    }));
  } catch (error: any) {
    console.error("加载员工失败:", error);
    employees.value = [];
  }
};

// ==================== 加载绩效数据 ====================
const loadPerformanceData = async () => {
  if (employees.value.length === 0) {
    await loadEmployees();
  }

  try {
    const response = await adminApi.getPerformance({
      month: performanceMonth.value,
      employment_scope: "month_visible",
      year_month: performanceMonth.value || getCurrentYearMonth(),
      performance_hire_cutoff_day: performanceHireCutoffDay.value,
    });

    const data = response.data || {};
    const items = data.items || [];

    // ✅ 创建员工映射表，方便快速查找最新姓名
    const employeeMap = new Map();
    employees.value.forEach((emp) => {
      employeeMap.set(emp.id, emp);
      if (emp.employeeId) employeeMap.set(emp.employeeId, emp);
    });

    if (items.length > 0) {
      performanceData.value = items.map((item: any) => {
        let scoreRecords = item.scoreRecords || [];
        if (typeof scoreRecords === "string") {
          try {
            scoreRecords = JSON.parse(scoreRecords);
          } catch (e) {
            scoreRecords = [];
          }
        }

        let total = 10;
        for (const record of scoreRecords) {
          total += record.score;
        }

        let grade = "";
        if (total > 10) {
          grade = "优秀";
        } else if (total === 10) {
          grade = "满分";
        } else if (total >= 7 && total <= 9) {
          grade = "合格";
        } else if (total === 6) {
          grade = "待提升";
        } else if (total <= 5) {
          grade = "不合格";
        }

        // 获取员工ID（兼容多种字段名）
        const employeeId = item.employeeId || item.employee_id;
        // ✅ 从 employees 中获取最新员工信息
        const latestEmployee = employeeMap.get(employeeId);

        return {
          employee_id: employeeId,
          // ✅ 关键修复：优先使用 employees 表中的最新姓名
          employee_name: latestEmployee?.name || item.employeeName || "未知",
          position: latestEmployee?.position || item.position || "-",
          base_score: 10,
          score_records: scoreRecords,
          total_score: total,
          grade: grade,
        };
      });
    } else {
      performanceData.value = employees.value.map((emp) => ({
        employee_id: emp.id,
        employee_name: emp.name,
        position: emp.position || "-",
        base_score: 10,
        score_records: [],
        total_score: 10,
        grade: "满分",
      }));
    }
  } catch (error) {
    console.error("加载绩效考核失败:", error);
    performanceData.value = [];
  }
};

// ==================== 加载员工绩效详情 ====================
const loadEmployeePerformance = async (employeeId: string) => {
  // 添加参数校验
  if (!employeeId) {
    console.warn("employeeId is required");
    currentPerformance.value = {
      employee_id: "",
      total_score: 10,
      grade: "合格",
      score_records: [],
    };
    return;
  }

  try {
    // 修复：确保月份不为 null
    let month = performanceMonth.value;
    if (!month) {
      month = getCurrentYearMonth();
    }

    const response = await adminApi.getPerformance({
      month: month,
      employee_id: employeeId,
    });
    const data = response.data || {};
    if (data.items && data.items.length > 0) {
      currentPerformance.value = data.items[0];
    } else {
      currentPerformance.value = {
        employee_id: employeeId,
        total_score: 10,
        grade: "合格",
        score_records: [],
      };
    }
  } catch (error) {
    console.error("加载绩效详情失败:", error);
    currentPerformance.value = {
      employee_id: employeeId,
      total_score: 10,
      grade: "合格",
      score_records: [],
    };
  }
};

// ==================== 加载员工罚款详情 ====================
const loadEmployeePenalty = async (employeeId: string) => {
  // 添加参数校验
  if (!employeeId) {
    console.warn("employeeId is required");
    currentPenaltyRecords.value = [];
    currentPenaltyTotal.value = 0;
    return;
  }

  try {
    // 修复：确保月份不为 null
    let month = performanceMonth.value;
    if (!month) {
      month = getCurrentYearMonth();
    }

    const response = await adminApi.getPenaltyRecords({
      month: month,
      employee_id: employeeId,
      page: 1,
      page_size: 100,
    });
    const data = response.data || {};
    currentPenaltyRecords.value = data.items || [];
    currentPenaltyTotal.value = currentPenaltyRecords.value.reduce(
      (sum, r) => sum + r.amount,
      0,
    );
  } catch (error) {
    console.error("加载罚款详情失败:", error);
    currentPenaltyRecords.value = [];
    currentPenaltyTotal.value = 0;
  }
};

// ==================== 显示员工详情弹窗 ====================
const showEmployeeDetail = async (row: any) => {
  console.log("=== 打开员工详情 ===");
  console.log("员工数据:", row);
  console.log("当前月份:", performanceMonth.value);

  const fullEmployee = employees.value.find((e) => e.id === row.employee_id);

  currentEmployee.value = {
    id: row.employee_id,
    name: row.employee_name,
    position: row.position,
    employee_id: row.employee_id,
    employeeId: row.employee_id,
    workLocation:
      fullEmployee?.workLocation || fullEmployee?.work_location || "现场",
    hireDate: fullEmployee?.hireDate || fullEmployee?.hire_date || null,
    // 保留下划线版本以便兼容
    hire_date: fullEmployee?.hireDate || fullEmployee?.hire_date || null,
  };

  let month = performanceMonth.value;
  if (!month) {
    month = getCurrentYearMonth();
  }

  // 加载绩效数据
  try {
    const perfResponse = await adminApi.getPerformance({
      month: month,
      employee_id: row.employee_id,
    });
    console.log("绩效API返回:", perfResponse.data);

    const perfData = perfResponse.data || {};
    if (perfData.items && perfData.items.length > 0) {
      const item = perfData.items[0];
      // 修复：适配字段名（可能是驼峰或下划线）
      currentPerformance.value = {
        employee_id: item.employeeId || item.employee_id,
        employee_name: item.employeeName || item.employee_name,
        total_score: item.totalScore || item.total_score || 10,
        grade: item.grade || "合格",
        score_records: item.scoreRecords || item.score_records || [],
      };
    } else {
      currentPerformance.value = {
        employee_id: row.employee_id,
        total_score: 10,
        grade: "合格",
        score_records: [],
      };
    }
    console.log("处理后的绩效数据:", currentPerformance.value);
  } catch (error) {
    console.error("加载绩效详情失败:", error);
    currentPerformance.value = {
      employee_id: row.employee_id,
      total_score: 10,
      grade: "合格",
      score_records: [],
    };
  }

  // 加载罚款数据
  try {
    const penaltyResponse = await adminApi.getPenaltyRecords({
      month: month,
      employee_id: row.employee_id,
      page: 1,
      page_size: 100,
    });
    console.log("罚款API返回:", penaltyResponse.data);

    const penaltyData = penaltyResponse.data || {};
    currentPenaltyRecords.value = penaltyData.items || [];
    currentPenaltyTotal.value = currentPenaltyRecords.value.reduce(
      (sum, r) => sum + r.amount,
      0,
    );
    console.log("罚款数据:", currentPenaltyRecords.value);
    console.log("罚款总额:", currentPenaltyTotal.value);
  } catch (error) {
    console.error("加载罚款详情失败:", error);
    currentPenaltyRecords.value = [];
    currentPenaltyTotal.value = 0;
  }

  // 加载考勤数据
  await loadEmployeeAttendance(row.employee_id);

  console.log("最终数据 - 绩效:", currentPerformance.value);
  console.log("最终数据 - 罚款总额:", currentPenaltyTotal.value);

  detailDialogVisible.value = true;
};

// ==================== 从详情页打开添加绩效分对话框 ====================
const showScoreDialogFromDetail = () => {
  if (currentEmployee.value) {
    // 构造绩效数据
    const perfData = {
      employee_id: currentEmployee.value.id,
      employee_name: currentEmployee.value.name,
      position: currentEmployee.value.position,
      total_score: currentPerformance.value?.total_score || 10,
      score_records: currentPerformance.value?.score_records || [],
    };
    showAddScoreDialogForEmployee(perfData);
  }
};

// ==================== 添加绩效分对话框相关 ====================
const showAddScoreDialog = () => {
  scoreForm.value = {
    employee_id: "",
    position: "",
    current_score: 10,
    score: 0,
    reason: "",
    date: getToday(),
  };
  scoreDialogVisible.value = true;
};

const showAddScoreDialogForEmployee = (row: any) => {
  scoreForm.value = {
    employee_id: row.employee_id,
    position: row.position,
    current_score: row.total_score,
    score: 0,
    reason: "",
    date: getToday(),
  };
  scoreDialogVisible.value = true;
};

const onScoreEmployeeChange = (employeeId: any) => {
  const emp = employees.value.find((e) => e.id === employeeId);
  if (emp) {
    scoreForm.value.position = emp.position || "-";
  }
  const perf = performanceData.value.find((p) => p.employee_id === employeeId);
  scoreForm.value.current_score = perf ? perf.total_score : 10;
};

const scoreSubmitting = ref(false);

const confirmAddScore = async () => {
  if (!scoreFormRef.value) return;
  await scoreFormRef.value.validate(async (valid: boolean) => {
    if (valid) {
      scoreSubmitting.value = true;
      try {
        let perf = performanceData.value.find(
          (p) => p.employee_id === scoreForm.value.employee_id,
        );
        if (!perf) {
          const emp = employees.value.find(
            (e) => e.id === scoreForm.value.employee_id,
          );
          perf = {
            employee_id: emp.id,
            employee_name: emp.name,
            position: emp.position || "-",
            base_score: 10,
            score_records: [],
            total_score: 10,
            grade: "满分",
          };
          performanceData.value.push(perf);
        }
        const newRecord = {
          date: scoreForm.value.date,
          score: scoreForm.value.score,
          reason: scoreForm.value.reason,
          operator: getOperatorName(),
        };
        if (!perf.score_records) perf.score_records = [];
        perf.score_records.push(newRecord);

        let total = 10;
        for (const record of perf.score_records) {
          total += record.score;
        }
        perf.total_score = total;

        if (total > 10) {
          perf.grade = "优秀";
        } else if (total === 10) {
          perf.grade = "满分";
        } else if (total >= 7 && total <= 9) {
          perf.grade = "合格";
        } else if (total === 6) {
          perf.grade = "待提升";
        } else if (total <= 5) {
          perf.grade = "不合格";
        }

        await savePerformance();
        scoreDialogVisible.value = false;
        ElMessage.success("绩效分已添加");

        try {
          const pushRes = await adminApi.pushPerformanceNotify({
            date: newRecord.date,
            employeeId: perf.employee_id || scoreForm.value.employee_id,
            employeeName: perf.employee_name,
            position: perf.position,
            score: newRecord.score,
            reason: newRecord.reason,
          });
          if (pushRes.data?.sent) {
            ElMessage.success("已推送到 Telegram");
          }
        } catch (pushErr: any) {
          console.warn("绩效推送失败:", pushErr);
        }

        // 关闭员工详情对话框
        detailDialogVisible.value = false;

        // 刷新绩效页面数据
        await loadPerformanceData();
      } catch (error) {
        ElMessage.error("操作失败");
      } finally {
        scoreSubmitting.value = false;
      }
    }
  });
};

// ==================== 删除绩效记录 ====================
const deleteScoreRecord = async (row: any, index: number) => {
  try {
    await ElMessageBox.confirm("确定要删除这条绩效记录吗？", "提示", {
      type: "warning",
      confirmButtonText: "确认删除",
      cancelButtonText: "取消",
    });
    row.score_records.splice(index, 1);
    let total = 10;
    for (const record of row.score_records) {
      total += record.score;
    }
    row.total_score = total;

    if (total > 10) {
      row.grade = "优秀";
    } else if (total === 10) {
      row.grade = "满分";
    } else if (total >= 7 && total <= 9) {
      row.grade = "合格";
    } else if (total === 6) {
      row.grade = "待提升";
    } else if (total <= 5) {
      row.grade = "不合格";
    }

    await savePerformance();
    ElMessage.success("删除成功");
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error("删除失败");
  }
};

// ==================== 编辑绩效记录 ====================
const editScoreRecord = (row: any, index: number) => {
  const record = row.score_records[index];

  // 填充编辑表单
  editScoreForm.value = {
    rowIndex: index,
    employee_id: row.employee_id,
    employee_name: row.employee_name,
    oldScore: record.score,
    score: record.score,
    reason: record.reason,
    date: record.date,
  };

  editScoreDialogVisible.value = true;
};

// 编辑绩效对话框相关
const editScoreDialogVisible = ref(false);
const editScoreSubmitting = ref(false);
const editScoreFormRef = ref<any>(null);
const editScoreForm = ref({
  rowIndex: -1,
  employee_id: "",
  employee_name: "",
  oldScore: 0,
  score: 0,
  reason: "",
  date: "",
});

// 编辑表单验证规则
const editScoreRules = {
  score: [{ required: true, message: "请输入加减分数", trigger: "blur" }],
  reason: [{ required: true, message: "请输入原因", trigger: "blur" }],
  date: [{ required: true, message: "请选择日期", trigger: "change" }],
};

// 确认编辑绩效记录
const confirmEditScore = async () => {
  if (!editScoreFormRef.value) return;

  try {
    await editScoreFormRef.value.validate();
  } catch {
    return;
  }

  editScoreSubmitting.value = true;
  try {
    const row = performanceData.value.find(
      (p) => p.employee_id === editScoreForm.value.employee_id,
    );

    if (row && editScoreForm.value.rowIndex >= 0) {
      // 更新记录
      row.score_records[editScoreForm.value.rowIndex] = {
        date: editScoreForm.value.date,
        score: editScoreForm.value.score,
        reason: editScoreForm.value.reason,
        operator: getOperatorName(),
      };

      // 重新计算总分
      let total = 10;
      for (const record of row.score_records) {
        total += record.score;
      }
      row.total_score = total;

      // 重新计算等级
      if (total > 10) {
        row.grade = "优秀";
      } else if (total === 10) {
        row.grade = "满分";
      } else if (total >= 7 && total <= 9) {
        row.grade = "合格";
      } else if (total === 6) {
        row.grade = "待提升";
      } else if (total <= 5) {
        row.grade = "不合格";
      }

      // 保存到后端
      await savePerformance();

      ElMessage.success("绩效记录已更新");
      editScoreDialogVisible.value = false;

      // 刷新数据
      await loadPerformanceData();
    }
  } catch (error: any) {
    ElMessage.error(error.message || "更新失败");
  } finally {
    editScoreSubmitting.value = false;
  }
};

// ==================== 保存绩效考核 ====================
const savePerformance = async () => {
  savingPerformance.value = true;
  try {
    const itemsToSave = performanceData.value.map((item) => ({
      employee_id: item.employee_id,
      employee_name: item.employee_name,
      position: item.position,
      base_score: item.base_score || 10,
      score_records: item.score_records || [],
      total_score: item.total_score,
      grade: item.grade,
    }));
    await adminApi.batchSavePerformance({
      month: performanceMonth.value,
      items: itemsToSave,
    });
    ElMessage.success("绩效考核已保存");
  } catch (error: any) {
    console.error("保存失败:", error);
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingPerformance.value = false;
  }
};

// ==================== 导出绩效数据 ====================
const exportPerformance = async () => {
  try { await request.post("/standalone/export-authorize", {module:"performance"}); } catch(e:any) { ElMessage.error(e.message); return; }
  if (!performanceData.value.length) {
    ElMessage.warning("没有数据可导出");
    return;
  }
  const headers = ["姓名", "岗位", "基础分", "绩效记录", "结余分数", "评级"];
  const rows = performanceData.value.map((p) => {
    const recordsStr = (p.score_records || [])
      .map(
        (r: any) =>
          `${r.date} ${r.score >= 0 ? `+${r.score}` : r.score}分 ${r.reason} [操作者:${getScoreRecordOperator(r)}]`,
      )
      .join("; ");
    return [
      p.employee_name,
      p.position,
      "10",
      recordsStr || "-",
      p.total_score,
      p.grade,
    ];
  });
  const csvContent = [
    headers.join(","),
    ...rows.map((row) => row.map((cell) => `"${cell || ""}"`).join(",")),
  ].join("\n");
  const blob = new Blob(["\uFEFF" + csvContent], {
    type: "text/csv;charset=utf-8;",
  });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `绩效考核_${performanceMonth.value}.csv`;
  link.click();
  URL.revokeObjectURL(link.href);
  ElMessage.success("导出成功");
};

// ==================== 绩效 Telegram 推送配置 ====================
const pushConfigVisible = ref(false);
const pushDiscoverVisible = ref(false);
const pushTokenInput = ref("");
const pushTokenSaving = ref(false);
const pushEnabledSaving = ref(false);
const pushDiscovering = ref(false);
const pushTesting = ref(false);
const pushBindingChatId = ref("");
const pushDiscoveredChats = ref<
  Array<{ chatId: string; groupName: string; chatType?: string }>
>([]);
const pushSettings = ref({
  hasBotToken: false,
  botUsername: "",
  chatId: "",
  chatTitle: "",
  chatType: "",
  enabled: false,
  ready: false,
});

const applyPushSettingsFromApi = (data: any, keep?: Partial<typeof pushSettings.value>) => {
  pushSettings.value = {
    hasBotToken: !!data?.hasBotToken,
    botUsername: data?.botUsername || keep?.botUsername || "",
    chatId: data?.chatId ?? keep?.chatId ?? "",
    chatTitle: data?.chatTitle ?? keep?.chatTitle ?? "",
    chatType: data?.chatType ?? keep?.chatType ?? "",
    enabled: !!data?.enabled,
    ready: !!data?.ready,
  };
};

const formatPushChatType = (type?: string) => {
  if (type === "channel") return "频道";
  if (type === "supergroup") return "超级群组";
  if (type === "group") return "群组";
  return type || "-";
};

const openPushConfigDrawer = () => {
  pushConfigVisible.value = true;
};

const loadPerformanceRuleSettings = async () => {
  try {
    const res = await adminApi.getPerformanceRuleSettings();
    const day = Math.max(1, Math.min(31, Number(res.data?.hireCutoffDay || 15)));
    performanceHireCutoffDay.value = day;
    ruleSettingsForm.value.hireCutoffDay = day;
  } catch {
    performanceHireCutoffDay.value = 15;
    ruleSettingsForm.value.hireCutoffDay = 15;
  }
};

const openRuleSettingsDialog = async () => {
  await loadPerformanceRuleSettings();
  ruleSettingsVisible.value = true;
};

const saveRuleSettings = async () => {
  ruleSettingsSaving.value = true;
  try {
    const day = Math.max(1, Math.min(31, Number(ruleSettingsForm.value.hireCutoffDay || 15)));
    const res = await adminApi.savePerformanceRuleSettings({ hireCutoffDay: day });
    performanceHireCutoffDay.value = Number(res.data?.hireCutoffDay || day);
    ruleSettingsVisible.value = false;
    ElMessage.success("绩效起算设置已保存");
    await loadEmployees();
    await loadPerformanceData();
  } catch (e: any) {
    ElMessage.error(e.message || "保存设置失败");
  } finally {
    ruleSettingsSaving.value = false;
  }
};

const loadPushSettings = async () => {
  try {
    const res = await adminApi.getPerformancePushSettings();
    applyPushSettingsFromApi(res.data);
  } catch (e: any) {
    ElMessage.error(e.message || "加载推送配置失败");
  }
};

const savePushEnabled = async (enabled: boolean) => {
  pushEnabledSaving.value = true;
  try {
    const res = await adminApi.savePerformancePushEnabled(enabled);
    applyPushSettingsFromApi(res.data, pushSettings.value);
    ElMessage.success(enabled ? "已开启自动推送" : "已关闭自动推送");
  } catch (e: any) {
    pushSettings.value.enabled = !enabled;
    ElMessage.error(e.message || "保存推送开关失败");
  } finally {
    pushEnabledSaving.value = false;
  }
};

const savePushToken = async () => {
  if (!pushTokenInput.value.trim()) {
    ElMessage.warning("请输入 Bot Token");
    return;
  }
  pushTokenSaving.value = true;
  try {
    const res = await adminApi.savePerformancePushToken(pushTokenInput.value.trim());
    applyPushSettingsFromApi(res.data, pushSettings.value);
    pushTokenInput.value = "";
    ElMessage.success("Token 已保存并验证");
  } catch (e: any) {
    ElMessage.error(e.message || "保存 Token 失败");
  } finally {
    pushTokenSaving.value = false;
  }
};

const discoverPushChats = async () => {
  pushDiscovering.value = true;
  try {
    const res = await adminApi.discoverPerformancePushChats();
    pushDiscoveredChats.value = (res.data || []).map((item: any) => ({
      chatId: item.chatId,
      groupName: item.groupName || item.chatTitle || item.chatId,
      chatType: item.chatType,
    }));
    pushDiscoverVisible.value = true;
    if (!pushDiscoveredChats.value.length) {
      ElMessage.info("未发现频道或群组：请确认机器人已入群；仍没有时可发一条消息后重试");
    }
  } catch (e: any) {
    ElMessage.error(e.message || "发现失败");
  } finally {
    pushDiscovering.value = false;
  }
};

const bindPushChat = async (row: {
  chatId: string;
  groupName?: string;
  chatType?: string;
}) => {
  pushBindingChatId.value = row.chatId;
  try {
    const res = await adminApi.bindPerformancePushChat({
      chatId: row.chatId,
      chatTitle: row.groupName,
      chatType: row.chatType,
    });
    applyPushSettingsFromApi(res.data, {
      ...pushSettings.value,
      chatId: row.chatId,
      chatTitle: row.groupName || "",
      chatType: row.chatType || "",
    });
    pushDiscoverVisible.value = false;
    ElMessage.success("绑定成功");
  } catch (e: any) {
    ElMessage.error(e.message || "绑定失败");
  } finally {
    pushBindingChatId.value = "";
  }
};

const unbindPushChat = async () => {
  try {
    await ElMessageBox.confirm("确定解绑当前推送目标？", "确认", {
      type: "warning",
    });
    const res = await adminApi.unbindPerformancePushChat();
    applyPushSettingsFromApi(res.data, {
      ...pushSettings.value,
      chatId: "",
      chatTitle: "",
      chatType: "",
    });
    ElMessage.success("已解绑");
  } catch (e: any) {
    if (e !== "cancel") ElMessage.error(e.message || "解绑失败");
  }
};

const testPushConfig = async () => {
  pushTesting.value = true;
  try {
    await adminApi.testPerformancePush();
    ElMessage.success("测试消息已发送");
  } catch (e: any) {
    ElMessage.error(e.message || "测试推送失败");
  } finally {
    pushTesting.value = false;
  }
};

// ==================== 提交员工编辑 ====================
const submitEmployeeEdit = async () => {
  if (!employeeFormRef.value) return;

  try {
    await employeeFormRef.value.validate();
  } catch {
    return;
  }

  // 修复：检查 id 是否存在
  if (!employeeForm.value.id) {
    ElMessage.error("员工ID不存在");
    return;
  }

  submittingEmployee.value = true;
  try {
    const updateData = {
      name: employeeForm.value.name,
      employee_id: employeeForm.value.employee_id,
      position: employeeForm.value.position,
      hire_date: employeeForm.value.hire_date,
      work_location: employeeForm.value.work_location,
    };

    await adminApi.updateEmployee(String(employeeForm.value.id), updateData);
    ElMessage.success("员工信息已更新");

    await loadEmployees();
    await loadPerformanceData();

    // 检查 id 存在后再调用
    if (
      detailDialogVisible.value &&
      currentEmployee.value &&
      currentEmployee.value.id
    ) {
      currentEmployee.value = {
        ...currentEmployee.value,
        ...updateData,
      };
      await loadEmployeePerformance(String(currentEmployee.value.id));
      await loadEmployeePenalty(String(currentEmployee.value.id));
      await loadEmployeeAttendance(String(currentEmployee.value.id));
    }

    employeeDialogVisible.value = false;
  } catch (error: any) {
    ElMessage.error(error.message || "更新失败");
  } finally {
    submittingEmployee.value = false;
  }
};

// ==================== 提交罚款 ====================
const confirmAddPenalty = async () => {
  if (!penaltyFormRef.value) return;

  try {
    await penaltyFormRef.value.validate();
  } catch {
    return;
  }

  penaltySubmitting.value = true;
  try {
    await adminApi.createPenaltyRecord({
      employee_id: penaltyForm.value.employee_id,
      penalty_date: penaltyForm.value.penalty_date,
      amount: penaltyForm.value.amount,
      category: penaltyForm.value.category,
      reason: penaltyForm.value.reason,
    });

    ElMessage.success("罚款记录已添加");
    penaltyDialogVisible.value = false;

    // 关闭员工详情对话框
    detailDialogVisible.value = false;
  } catch (error: any) {
    ElMessage.error(error.message || "添加失败");
  } finally {
    penaltySubmitting.value = false;
  }
};

// ==================== 重置罚款表单 ====================
const resetPenaltyForm = () => {
  penaltyForm.value = {
    employee_id: "",
    penalty_date: "",
    amount: 0,
    category: "迟到",
    reason: "",
  };
  penaltyFormRef.value?.clearValidate();
};

// ==================== 从详情页打开添加罚款对话框 ====================
const showPenaltyDialogFromDetail = () => {
  if (currentEmployee.value) {
    // 添加验证
    if (!currentEmployee.value.id) {
      ElMessage.error("员工ID无效，无法添加罚款");
      console.error("currentEmployee为空:", currentEmployee.value);
      return;
    }

    penaltyForm.value = {
      employee_id: currentEmployee.value.id,
      penalty_date: getToday(),
      amount: 0,
      category: "迟到",
      reason: "",
    };
    penaltyDialogVisible.value = true;
  }
};

// ==================== 编辑员工（从详情页） ====================
const editEmployeeFromDetail = () => {
  if (currentEmployee.value) {
    detailDialogVisible.value = false;
    employeeDialogTitle.value = "编辑员工";
    employeeForm.value = {
      id: currentEmployee.value.id,
      name: currentEmployee.value.name,
      employee_id: currentEmployee.value.employee_id,
      position: currentEmployee.value.position,
      hire_date:
        currentEmployee.value.hireDate || currentEmployee.value.hire_date,
      work_location:
        currentEmployee.value.workLocation ||
        currentEmployee.value.work_location ||
        "现场",
    };
    employeeDialogVisible.value = true;
  }
};

// ==================== 删除员工（从详情页） ====================
const deleteEmployeeFromDetail = async () => {
  if (!currentEmployee.value) return;

  try {
    await ElMessageBox.confirm(
      `确定要删除员工 "${currentEmployee.value.name}" 吗？`,
      "警告",
      { type: "warning", confirmButtonText: "确定", cancelButtonText: "取消" },
    );

    await adminApi.deleteEmployee(currentEmployee.value.id);
    ElMessage.success("删除成功");

    detailDialogVisible.value = false;
    await loadEmployees();
    await loadPerformanceData();
  } catch (error: any) {
    if (error !== "cancel") {
      ElMessage.error(error.message || "删除失败");
    }
  }
};

// ==================== 跳转到罚款页面 ====================
const goToPenaltyPage = (employeeName: string) => {
  router.push({
    path: "/attendance/penalty",
    query: { employee: employeeName },
  });
};

// ==================== 生命周期 ====================
// ==================== 员工更新事件监听 ====================
const handleEmployeeUpdated = (event: any) => {
  const { id, data } = event.detail;

  // 更新本地员工列表
  const index = employees.value.findIndex((e) => e.id === id);
  if (index !== -1) {
    employees.value[index] = { ...employees.value[index], ...data };
  }

  // 刷新绩效数据（让表格中的员工姓名显示最新值）
  loadPerformanceData();
};

onMounted(async () => {
  if (route.query.employee) {
    performanceSearch.value = String(route.query.employee);
  }
  await loadFineCurrency();
  await loadPerformanceRuleSettings();
  await loadCurrentOperatorName();
  await loadEmployees();
  await loadPerformanceData();

  // 监听员工更新事件
  window.addEventListener("employee-updated", handleEmployeeUpdated);
});

onUnmounted(() => {
  window.removeEventListener("employee-updated", handleEmployeeUpdated);
});
</script>

<style scoped>
@import "./attendance.css";

.push-config-body {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.push-config-tip {
  margin-bottom: 4px;
}

.push-enable-section {
  background: linear-gradient(135deg, #f0f9ff 0%, #eef2ff 100%);
  border-color: rgba(64, 158, 255, 0.18);
}

.push-enable-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.push-enable-row .push-section-title {
  margin-bottom: 0;
}

.push-config-section {
  padding: 16px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.72);
  border: 1px solid rgba(0, 0, 0, 0.06);
}

.push-section-title {
  font-size: 14px;
  font-weight: 700;
  color: #303133;
  margin-bottom: 12px;
}

.push-section-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
}

.push-help-text {
  margin: 10px 0 0;
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}

.push-bound-card {
  margin-top: 14px;
  padding: 14px;
  border-radius: 10px;
  background: linear-gradient(135deg, #f0f9ff 0%, #eef2ff 100%);
  border: 1px solid rgba(64, 158, 255, 0.18);
}

.push-bound-main {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  color: #409eff;
}

.push-bound-title {
  font-size: 14px;
  font-weight: 700;
  color: #303133;
}

.push-bound-meta {
  margin-top: 4px;
  font-size: 12px;
  color: #909399;
}

.push-bound-actions {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.push-preview-card {
  padding: 12px 14px;
  border-radius: 10px;
  background: #1e293b;
  color: #fff;
  font-size: 13px;
  line-height: 1.8;
}
</style>
