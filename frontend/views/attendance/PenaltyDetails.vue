<template>
  <div class="attendance-management">
    <!-- 背景装饰 -->
    <div class="bg-decoration">
      <div class="blob blob-1"></div>
      <div class="blob blob-2"></div>
      <div class="blob blob-3"></div>
    </div>
    <div class="attendance-management">
      <!-- 罚款卡片 - 现代化设计 -->
      <el-card class="penalty-card" shadow="never">
        <template #header>
          <div class="card-header">
            <div class="header-title">
              <div class="title-icon">
                <el-icon><Warning /></el-icon>
              </div>
              <span class="title-text">罚款详情</span>
              <el-tag
                type="danger"
                size="small"
                effect="light"
                class="title-tag"
                >实时更新</el-tag
              >
            </div>
            <div class="header-actions">
              <el-input
                v-model="penaltySearchEmployee"
                placeholder="搜索员工"
                clearable
                style="width: 180px"
                :prefix-icon="Search"
                class="search-input"
              />
              <el-date-picker
                v-model="penaltyMonth"
                type="month"
                placeholder="选择月份"
                format="YYYY年MM月"
                value-format="YYYY-MM"
                @change="onMonthChange"
                style="width: 160px"
                class="month-picker"
              />
              <el-button v-permission="'penalty:edit'"
                type="primary" @click="showAddPenaltyDialog"
                class="action-button"
              >
                <el-icon><Plus /></el-icon>添加罚款
              </el-button>
              <el-button
                @click="handleRefresh"
                :loading="refreshing"
                class="action-button"
              >
                <el-icon><Refresh /></el-icon>刷新
              </el-button>
              <el-button v-permission="'penalty:export'" @click="exportPenalty" class="action-button">
                <el-icon><Download /></el-icon>导出
              </el-button>
              <el-button
                v-permission="'penalty:edit'"
                @click="openPushConfigDrawer"
                class="action-button ghost-button"
              >
                <el-icon><Promotion /></el-icon>配置推送
              </el-button>
            </div>
          </div>
        </template>

            <!-- 统计卡片 - 现代化设计 -->
            <el-row :gutter="20" class="penalty-stats-row">
              <el-col :span="6" v-for="stat in statCards" :key="stat.key">
                <el-card
                  class="penalty-stat-card"
                  shadow="hover"
                  :class="`stat-card-${stat.key}`"
                >
                  <div class="stat-content">
                    <div
                      class="stat-icon"
                      :style="{ background: stat.iconBg, color: stat.iconColor }"
                    >
                      <el-icon><component :is="stat.icon" /></el-icon>
                    </div>
                    <div class="stat-info">
                      <div class="stat-value">{{ stat.value }}</div>
                      <div class="stat-label">{{ stat.label }}</div>
                    </div>
                  </div>
                </el-card>
              </el-col>
            </el-row>

            <!-- 表格 - 现代化设计 -->
            <el-table
              :data="filteredPenaltyRecords"
              stripe
              border
              style="width: 100%"
              class="modern-table"
              :header-cell-style="{
                background: '#f8fafc',
                color: '#1e293b',
                fontWeight: '600',
              }"
            >
          <el-table-column type="index" width="50" label="#" />
          <el-table-column
            prop="employee_name"
            label="员工姓名"
            min-width="80"
            align="center"
          >
            <template #default="{ row }">
              <div class="employee-cell" @click="showEmployeeDetail(row)">
                <el-avatar
                  :size="28"
                  class="employee-avatar"
                  :style="{
                    background: getAvatarColor(
                      row.employee_name || row.employeeName,
                    ),
                  }"
                >
                  {{
                    (row.employee_name || row.employeeName)
                      ?.charAt(0)
                      ?.toUpperCase() || "?"
                  }}
                </el-avatar>
                <span class="employee-name-link">{{
                  row.employee_name || row.employeeName
                }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column
            prop="position"
            label="岗位"
            width="120"
            align="center"
          >
            <template #default="{ row }">
              <span class="position-text">{{ row.position || "-" }}</span>
            </template>
          </el-table-column>
          <el-table-column label="罚款日期" width="120" align="center">
            <template #default="{ row }">
              <span class="date-text">{{
                row.penaltyDate || row.penalty_date || "-"
              }}</span>
            </template>
          </el-table-column>
          <el-table-column
            prop="amount"
            :label="`金额(${fineCurrencySymbol(fineCurrency)})`"
            width="110"
            align="center"
          >
            <template #default="{ row }">
              <span class="penalty-amount">{{ formatFineAmount(row.amount, fineCurrency) }}</span>
            </template>
          </el-table-column>
          <el-table-column
            prop="category"
            label="罚款类型"
            width="130"
            align="center"
          >
            <template #default="{ row }">
              <el-tag
                :type="getPenaltyCategoryType(row.category)"
                size="small"
                effect="light"
                class="category-tag"
                >{{ row.category }}</el-tag
              >
            </template>
          </el-table-column>
          <el-table-column
            prop="reason"
            label="罚款原因"
            min-width="200"
            show-overflow-tooltip
          >
            <template #default="{ row }">
              <span class="reason-text">{{ row.reason }}</span>
            </template>
          </el-table-column>
          <el-table-column
            prop="created_by"
            label="记录人"
            width="100"
            align="center"
          >
            <template #default="{ row }">
              <span class="creator-text">{{
                getPenaltyCreatorName(row)
              }}</span>
            </template>
          </el-table-column>
          <el-table-column
            label="操作"
            width="140"
            fixed="right"
            align="center"
          >
            <template #default="{ row }">
              <el-button
                link
                type="primary"
                size="small"
                v-permission="'penalty:edit'" @click="openEditDialog(row)"
                :disabled="!canEdit"
                class="action-btn edit-btn"
              >
                <el-icon><Edit /></el-icon>编辑
              </el-button>
              <el-button v-permission="'penalty:delete'"
                link
                type="danger" @click="deletePenalty(row)"
                class="delete-button"
              >
                <el-icon><Delete /></el-icon>删除
              </el-button>
            </template>
          </el-table-column>
        </el-table>

        <div class="pagination" v-if="penaltyTotal > 0">
          <el-pagination
            v-model:current-page="penaltyPage"
            v-model:page-size="penaltyPageSize"
            :total="penaltyTotal"
            layout="total, sizes, prev, pager, next"
            @size-change="loadPenaltyData"
            @current-change="loadPenaltyData"
            background
          />
        </div>
      </el-card>

      <!-- 编辑罚款对话框 -->
      <el-dialog
        v-model="editDialogVisible"
        title="编辑罚款记录"
        width="500px"
        class="modern-dialog"
      >
        <el-form
          :model="editForm"
          :rules="editRules"
          ref="editFormRef"
          label-width="100px"
        >
          <el-form-item label="员工姓名">
            <span>{{ editForm.employee_name }}</span>
          </el-form-item>
          <el-form-item label="罚款日期" prop="penalty_date">
            <el-date-picker
              v-model="editForm.penalty_date"
              type="date"
              placeholder="选择日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item :label="`罚款金额 (${fineCurrencySymbol(fineCurrency)})`" prop="amount">
            <el-input-number
              v-model="editForm.amount"
              :min="0"
              :step="50"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item label="罚款类别" prop="category">
            <el-select v-model="editForm.category" style="width: 100%">
              <el-option label="迟到" value="迟到" />
              <el-option label="早退" value="早退" />
              <el-option label="旷工" value="旷工" />
              <el-option label="离岗" value="离岗" />
              <el-option label="吃饭超时" value="吃饭超时" />
              <el-option label="大厕超时" value="大厕超时" />
              <el-option label="小厕超时" value="小厕超时" />
              <el-option label="抽烟或休息超时" value="抽烟或休息超时" />
              <el-option label="其他" value="其他" />
            </el-select>
          </el-form-item>
          <el-form-item label="罚款原因" prop="reason">
            <el-input
              v-model="editForm.reason"
              type="textarea"
              :rows="3"
              placeholder="请输入罚款原因"
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="editDialogVisible = false">取消</el-button>
          <el-button v-permission="'penalty:edit'" type="primary" @click="saveEdit" :loading="saving"
            >保存修改</el-button
          >
        </template>
      </el-dialog>

      <!-- 添加罚款对话框 - 现代化设计 -->
      <el-dialog
        v-model="penaltyDialogVisible"
        title="添加罚款记录"
        width="520px"
        class="modern-dialog"
        @close="resetPenaltyForm"
      >
        <el-form
          :model="penaltyForm"
          :rules="penaltyRules"
          ref="penaltyFormRef"
          label-width="90px"
          label-position="right"
        >
          <el-form-item label="员工" prop="employee_id">
            <el-select
              v-model="penaltyForm.employee_id"
              placeholder="选择员工"
              filterable
              style="width: 100%"
              class="modern-select"
            >
              <el-option
                v-for="emp in employees"
                :key="emp.id"
                :label="`${emp.name} (${emp.position || '-'})`"
                :value="emp.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="罚款日期" prop="penalty_date">
            <el-date-picker
              v-model="penaltyForm.penalty_date"
              type="date"
              placeholder="选择罚款日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
              class="modern-datepicker"
            />
          </el-form-item>
          <el-form-item :label="`罚款金额 (${fineCurrencySymbol(fineCurrency)})`" prop="amount">
            <el-input-number
              v-model="penaltyForm.amount"
              :min="0"
              :step="10"
              style="width: 100%"
              class="modern-input-number"
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
              class="modern-select"
            >
              <el-option label="迟到" value="迟到" />
              <el-option label="早退" value="早退" />
              <el-option label="旷工" value="旷工" />
              <el-option label="小厕超时" value="小厕超时" />
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
              class="modern-textarea"
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <div class="dialog-footer">
            <el-button
              @click="penaltyDialogVisible = false"
              class="cancel-button"
              >取消</el-button
            >
            <el-button v-permission="'penalty:edit'"
              type="primary"
              @click="submitPenalty"
              :loading="submittingPenalty"
              class="submit-button"
              >确定添加</el-button
            >
          </div>
        </template>
      </el-dialog>

      <!-- 罚款推送配置抽屉 -->
      <el-drawer
        v-model="pushConfigVisible"
        title="罚款推送配置"
        size="480px"
        class="penalty-push-drawer"
        @open="loadPushSettings"
      >
        <div class="push-config-body">
          <section class="push-config-section push-enable-section">
            <div class="push-enable-row">
              <span class="push-section-title">启用自动推送</span>
              <el-switch
 v-permission="'penalty:notify'"                v-model="pushSettings.enabled"
                :loading="pushEnabledSaving"
                @change="savePushEnabled"
              />
            </div>
            <p class="push-help-text">
              <template v-if="pushSettings.enabled && pushSettings.ready">
                已开启，新增罚款记录将自动推送到 Telegram
              </template>
              <template v-else-if="pushSettings.enabled">
                已开启，请先配置 Bot Token 并绑定频道/群组
              </template>
              <template v-else>
                已关闭，新增罚款记录不会推送到 Telegram
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
 v-permission="'penalty:notify'"                type="primary"
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
 v-permission="'penalty:notify'"              type="primary"
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
 v-permission="'penalty:notify'"                  size="small"
                  :loading="pushTesting"
                  @click="testPushConfig"
                >
                  测试推送
                </el-button>
                <el-button
 v-permission="'penalty:notify'"                  size="small"
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
              <div><b>金额</b>：{{ formatFineAmount(100, fineCurrency) }}</div>
              <div><b>类别</b>：迟到</div>
              <div><b>原因</b>：上班迟到30分钟</div>
            </div>
          </section>
        </div>
      </el-drawer>

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
          title="选择要绑定的频道或群组，绑定后新增罚款将推送到该目标"
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
 v-permission="'penalty:notify'"                link
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
          <el-button v-permission="'penalty:notify'" @click="pushDiscoverVisible = false">关闭</el-button>
          <el-button v-permission="'penalty:notify'" type="primary" :loading="pushDiscovering" @click="discoverPushChats">
            重新发现
          </el-button>
        </template>
      </el-dialog>

      <!-- 员工详情对话框 -->
      <el-dialog
        v-model="detailDialogVisible"
        title="员工详细信息"
        width="600px"
        class="employee-detail-dialog"
        :close-on-click-modal="false"
        destroy-on-close
      >
        <div class="detail-container" v-if="currentEmployee">
          <!-- 头部信息 -->
          <div class="detail-header">
            <div class="avatar-section">
              <el-avatar
                :size="64"
                class="employee-avatar"
                :style="{ background: getAvatarColor(currentEmployee.name) }"
              >
                {{ currentEmployee.name?.charAt(0)?.toUpperCase() || "?" }}
              </el-avatar>
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
                  {{ formatDate(currentEmployee?.hireDate) }}
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
                  <span class="stat-work">{{ employeeWorkDays }}天</span>
                  <span class="stat-divider">/</span>
                  <span class="stat-leave">{{ employeeLeaveRestDays }}天</span>
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
                      circle @click="showScoreDialogFromDetail"
                    >
                      <el-icon><Plus /></el-icon>
                    </el-button>
                  </el-tooltip>
                </div>
                <div class="stat-card-value">
                  <el-tag
                    :type="getScoreType(employeePerformance?.total_score || 10)"
                    size="small"
                    effect="dark"
                  >
                    {{ employeePerformance?.total_score || 10 }}分
                  </el-tag>
                  <el-tag
                    :type="getGradeType(employeePerformance?.grade || '合格')"
                    size="small"
                  >
                    {{ employeePerformance?.grade || "合格" }}
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
                      circle @click="showPenaltyDialogFromDetail"
                    >
                      <el-icon><Plus /></el-icon>
                    </el-button>
                  </el-tooltip>
                </div>
                <div class="stat-card-value">
                  <span
                    :class="employeePenaltyTotal > 0 ? 'penalty-amount' : ''"
                  >
                    {{ formatFineAmount(employeePenaltyTotal, fineCurrency) }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- 绩效记录列表 -->
          <div
            v-if="employeePerformance?.score_records?.length > 0"
            class="records-section"
          >
            <el-divider style="margin: 12px 0">
              <span class="divider-text">
                <el-icon><TrendCharts /></el-icon>
                绩效记录
              </span>
            </el-divider>
            <div class="records-list">
              <div
                v-for="(record, idx) in employeePerformance.score_records.slice(
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
                </div>
              </div>
            </div>
            <div
              v-if="employeePerformance.score_records.length > 5"
              class="view-all-link"
            >
              <el-button
                link
                type="primary"
                size="small"
                @click="jumpToPerformance"
              >
                查看全部 {{ employeePerformance.score_records.length }} 条记录
                <el-icon><ArrowRight /></el-icon>
              </el-button>
            </div>
          </div>

          <!-- 罚款记录列表 -->
          <div v-if="employeePenaltyRecords.length > 0" class="records-section">
            <el-divider style="margin: 12px 0">
              <span class="divider-text">
                <el-icon><Money /></el-icon>
                罚款记录
              </span>
            </el-divider>
            <div class="records-list">
              <div
                v-for="(record, idx) in employeePenaltyRecords.slice(-5)"
                :key="idx"
                class="record-item"
              >
                <div class="record-date-badge penalty-date">
                  <span>{{
                    record.penaltyDate ||
                    record.penalty_date ||
                    record.date ||
                    "-"
                  }}</span>
                </div>
                <div class="record-content">
                  <span class="penalty-amount-badge">{{ formatFineAmount(record.amount, fineCurrency) }}</span>
                  <span class="record-reason">{{ record.reason }}</span>
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
            <div v-if="employeePenaltyRecords.length > 5" class="view-all-link">
              <el-button
                link
                type="primary"
                size="small"
                @click="jumpToPenalty"
              >
                查看全部 {{ employeePenaltyRecords.length }} 条记录
                <el-icon><ArrowRight /></el-icon>
              </el-button>
            </div>
          </div>
        </div>

        <template #footer>
          <div class="dialog-footer">
            <el-button @click="detailDialogVisible = false">关闭</el-button>
            <el-button type="primary" v-permission="'penalty:edit'" @click="editEmployeeFromDetail">
              <el-icon><Edit /></el-icon>编辑员工
            </el-button>
            <el-button v-permission="'attendance:delete'" type="danger" plain @click="deleteEmployeeFromDetail">
              <el-icon><Delete /></el-icon>删除员工
            </el-button>
          </div>
        </template>
      </el-dialog>

      <!-- 添加绩效分对话框 -->
      <el-dialog
        v-model="scoreDialogVisible"
        title="添加绩效分"
        width="540px"
        class="modern-dialog"
        :close-on-click-modal="false"
        @close="resetScoreForm"
      >
        <el-form
          :model="scoreForm"
          :rules="scoreRules"
          ref="scoreFormRef"
          label-width="100px"
          label-position="right"
        >
          <el-form-item label="员工">
            <span>{{ currentEmployee?.name }}</span>
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

      <!-- 编辑员工对话框 -->
      <el-dialog
        v-model="employeeEditDialogVisible"
        title="编辑员工"
        width="500px"
        @close="resetEmployeeForm"
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
          <el-button @click="employeeEditDialogVisible = false">取消</el-button>
          <el-button v-permission="'attendance:edit'"
            type="primary"
            @click="confirmEditEmployee"
            :loading="submittingEdit"
          >
            确定
          </el-button>
        </template>
      </el-dialog>

    </div>
  </div>
</template>

<script setup lang="ts">
import request from "@api/request";
import { ref, reactive, computed, onMounted, onUnmounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Download,
  Plus,
  Delete,
  Warning,
  Search,
  Money,
  Document,
  TrendCharts,
  User,
  OfficeBuilding, // ✅ 新增
  Calendar, // ✅ 新增
  DataLine, // ✅ 新增
  Edit,
  ArrowRight,
  Refresh,
  Check,
  Star,
  Promotion,
  ChatLineSquare,
} from "@element-plus/icons-vue";
import adminApi from "@api/admin_api";
import workforceTelegramApi from "@api/workforce_telegram_api";
import {
  getCurrentYearMonth,
  getToday,
  getPenaltyCategoryType,
  getPenaltyCreatorName,
} from "./helpers";
import {
  formatFineAmount,
  fineCurrencySymbol,
  normalizeFineCurrency,
  type FineCurrencyCode,
} from "./telegram_helpers";
import { useUserStore } from "@store/user";

const route = useRoute();
const router = useRouter();

/** 与 TG 设定 fineCurrency 同步；加载失败时保持 CNY（原硬编码行为） */
const fineCurrency = ref<FineCurrencyCode>("CNY");

const penaltyMonth = ref(getCurrentYearMonth());
const penaltySearchEmployee = ref("");
const penaltyRecords = ref<any[]>([]);
const penaltyTotal = ref(0);
const penaltyPage = ref(1);
const penaltyPageSize = ref(20);
const penaltyStats = ref({
  totalAmount: 0,
  employeeCount: 0,
  recordCount: 0,
  avgAmount: 0,
});
const penaltyDialogVisible = ref(false);
const penaltyFormRef = ref<any>(null);
const submittingPenalty = ref(false);
const employees = ref<any[]>([]);
const penaltyForm = ref({
  employee_id: "",
  penalty_date: getToday(),
  amount: 0,
  category: "迟到",
  reason: "",
});
const penaltyRules = {
  employee_id: [{ required: true, message: "请选择员工", trigger: "change" }],
  penalty_date: [
    { required: true, message: "请选择罚款日期", trigger: "change" },
  ],
  amount: [{ required: true, message: "请输入罚款金额", trigger: "blur" }],
  reason: [{ required: true, message: "请输入罚款原因", trigger: "blur" }],
};

// ========== 权限检查 ==========
const userStore = useUserStore();
const canEdit = computed(() => userStore.hasPermission("penalty:edit"));

// ========== 刷新相关 ==========
const refreshing = ref(false);

const handleRefresh = async () => {
  refreshing.value = true;
  try {
    await loadEmployees();
    await loadPenaltyData();
    ElMessage.success("数据已刷新");
  } catch (error) {
    ElMessage.error("刷新失败");
  } finally {
    refreshing.value = false;
  }
};

const onMonthChange = async () => {
  await loadPenaltyData();
};

// ========== 员工详情相关 ==========
const detailDialogVisible = ref(false);
const currentEmployee = ref<any>(null);
const employeePerformance = ref<any>(null);
const employeePenaltyTotal = ref(0);
const employeeWorkDays = ref(0);
const employeeLeaveRestDays = ref(0);
const employeePenaltyRecords = ref<any[]>([]);

// 编辑员工相关
const employeeEditDialogVisible = ref(false);
const employeeFormRef = ref<any>(null);
const submittingEdit = ref(false);
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

// ========== 添加绩效分相关 ==========
const scoreDialogVisible = ref(false);
const scoreFormRef = ref<any>(null);
const scoreSubmitting = ref(false);
const scoreForm = ref({
  current_score: 10,
  score: 0,
  reason: "",
  date: getToday(),
});
const scoreRules = {
  score: [{ required: true, message: "请输入加减分数", trigger: "blur" }],
  reason: [{ required: true, message: "请输入原因", trigger: "blur" }],
  date: [{ required: true, message: "请选择日期", trigger: "change" }],
};

// 统计卡片数据 - 保持原有变量名不变
const statCards = computed(() => [
  {
    key: "total",
    icon: Money,
    iconBg: "#eff6ff",
    iconColor: "#3b82f6",
    label: "总罚款金额",
    value: formatFineAmount(penaltyStats.value.totalAmount || 0, fineCurrency.value),
  },
  {
    key: "employee",
    icon: User,
    iconBg: "#ecfdf5",
    iconColor: "#10b981",
    label: "涉及员工",
    value: Number(penaltyStats.value.employeeCount || 0).toLocaleString(),
  },
  {
    key: "record",
    icon: Document,
    iconBg: "#fff7ed",
    iconColor: "#f59e0b",
    label: "罚款记录数",
    value: Number(penaltyStats.value.recordCount || 0).toLocaleString(),
  },
  {
    key: "avg",
    icon: TrendCharts,
    iconBg: "#f5f3ff",
    iconColor: "#8b5cf6",
    label: "人均罚款",
    value: formatFineAmount(penaltyStats.value.avgAmount || 0, fineCurrency.value),
  },
]);

const filteredPenaltyRecords = computed(() => {
  if (!penaltySearchEmployee.value) return penaltyRecords.value;
  const keyword = penaltySearchEmployee.value.toLowerCase();
  return penaltyRecords.value.filter((r) =>
    r.employee_name?.toLowerCase().includes(keyword),
  );
});

watch(
  () => route.query.employee,
  (value) => {
    penaltySearchEmployee.value = value ? String(value) : "";
  },
  { immediate: true },
);

const loadEmployees = async () => {
  try {
    const response = await adminApi.getEmployees({
      skip: 0,
      limit: 1000,
      search: "",
    });
    const items = response.data?.items || [];
    employees.value = items.map((emp: any) => ({
      id: emp.id,
      name: emp.name,
      employee_name: emp.name,
      position: emp.position || "-",
      employee_id: emp.employeeId || emp.employee_id || "",
      hire_date: emp.hireDate || emp.hire_date || null,
      work_location: emp.workLocation || emp.work_location || "现场",
    }));
  } catch (error: any) {
    employees.value = [];
  }
};

const loadPenaltyData = async () => {
  try {
    const response = await adminApi.getPenaltyRecords({
      month: penaltyMonth.value,
      page: penaltyPage.value,
      page_size: penaltyPageSize.value,
    });
    const data = response.data || {};
    let records = data.items || [];

    // 确保员工列表已加载
    if (employees.value.length === 0) {
      await loadEmployees();
    }

    // 创建员工映射（按 ID）
    const employeeMap = new Map();
    employees.value.forEach((emp: any) => {
      employeeMap.set(emp.id, emp);
      if (emp.employee_id) employeeMap.set(emp.employee_id, emp);
    });

    for (const record of records) {
      // 兼容多种字段名
      const empId =
        record.employee_id || record.employeeId || record.employeeID;
      const empName = record.employee_name || record.employeeName;
      const empPosition = record.position;

      let emp = null;
      if (empId) {
        emp = employeeMap.get(empId);
      }

      // 如果通过 ID 匹配不到，尝试通过姓名匹配
      if (!emp && empName) {
        emp = employees.value.find((e) => e.name === empName);
      }

      if (emp) {
        record.employee_name = emp.name;
        record.employee_id = emp.id;
        record.position = emp.position || "-";
      } else if (empName) {
        record.employee_name = empName;
        record.position = empPosition || "-";
      } else {
        record.employee_name = "未知";
        record.position = "-";
      }

      // 确保 amount 是数字
      record.amount = record.amount || 0;

      // 修复日期字段
      const penaltyDate = record.penalty_date || record.penaltyDate;
      if (penaltyDate && penaltyDate !== "0001-01-01") {
        record.penalty_date = penaltyDate;
      } else {
        record.penalty_date = "";
      }

      // 记录人：优先显示姓名
      record.created_by = getPenaltyCreatorName(record);
    }

    penaltyRecords.value = records;
    penaltyTotal.value = data.total || 0;
    penaltyStats.value = {
      totalAmount: Number(data.stats?.totalAmount ?? 0),
      employeeCount: Number(data.stats?.employeeCount ?? 0),
      recordCount: Number(data.stats?.recordCount ?? data.total ?? 0),
      avgAmount: Number(data.stats?.avgAmount ?? 0),
    };
  } catch (error: any) {
    penaltyRecords.value = [];
    penaltyTotal.value = 0;
  }
};

const showAddPenaltyDialog = () => {
  penaltyForm.value = {
    employee_id: "",
    penalty_date: getToday(),
    amount: 0,
    category: "迟到",
    reason: "",
  };
  penaltyDialogVisible.value = true;
};

const submitPenalty = async () => {
  if (!penaltyFormRef.value) return;
  await penaltyFormRef.value.validate(async (valid: boolean) => {
    if (valid) {
      submittingPenalty.value = true;
      try {
        // 查找选中的员工
        const selectedEmp = employees.value.find(
          (e) => e.id === penaltyForm.value.employee_id,
        );

        const submitData = {
          employee_id: penaltyForm.value.employee_id,
          employee_name: selectedEmp?.name || "", // ✅ 同时保存员工姓名
          penalty_date: penaltyForm.value.penalty_date,
          amount: penaltyForm.value.amount,
          category: penaltyForm.value.category,
          reason: penaltyForm.value.reason,
        };

        await adminApi.createPenaltyRecord(submitData);
        ElMessage.success("罚款记录已添加");
        penaltyDialogVisible.value = false;
        await loadPenaltyData();

        try {
          const pushRes = await adminApi.pushPenaltyNotify({
            date: submitData.penalty_date,
            employeeId: selectedEmp?.id || submitData.employee_id,
            employeeName: selectedEmp?.name || submitData.employee_name || "",
            position: selectedEmp?.position || "",
            amount: submitData.amount,
            category: submitData.category,
            reason: submitData.reason,
          });
          if (pushRes.data?.sent) {
            ElMessage.success("已推送到 Telegram");
          }
        } catch (pushErr: any) {
          console.warn("罚款推送失败:", pushErr);
        }
      } catch (error: any) {
        ElMessage.error(error.message || "添加失败");
      } finally {
        submittingPenalty.value = false;
      }
    }
  });
};

// ========== 编辑相关状态 ==========
const editDialogVisible = ref(false);
const saving = ref(false);
const editFormRef = ref();
const editForm = reactive({
  id: "",
  employee_id: "",
  employee_name: "",
  penalty_date: "",
  amount: 0,
  category: "",
  reason: "",
});

// 编辑表单验证规则
const editRules = {
  penalty_date: [
    { required: true, message: "请选择罚款日期", trigger: "change" },
  ],
  amount: [
    { required: true, message: "请输入罚款金额", trigger: "blur" },
    { type: "number", min: 0, message: "罚款金额不能小于0", trigger: "blur" },
  ],
  category: [{ required: true, message: "请选择罚款类别", trigger: "change" }],
  reason: [{ required: true, message: "请输入罚款原因", trigger: "blur" }],
};

// ========== 打开编辑对话框 ==========
function openEditDialog(row: any) {
  editForm.id = row.id;
  editForm.employee_id = row.employee_id;
  editForm.employee_name = row.employee_name;
  editForm.penalty_date = row.penalty_date;
  editForm.amount = row.amount;
  editForm.category = row.category;
  editForm.reason = row.reason;
  editDialogVisible.value = true;
}

// ========== 保存编辑 ==========
async function saveEdit() {
  if (!editFormRef.value) return;

  try {
    await editFormRef.value.validate();
  } catch {
    return;
  }

  saving.value = true;
  try {
    await adminApi.updatePenaltyRecord(editForm.id, {
      amount: editForm.amount,
      category: editForm.category,
      reason: editForm.reason,
      penalty_date: editForm.penalty_date,
    });
    ElMessage.success("修改成功");
    editDialogVisible.value = false;
    await loadPenaltyData(); // ← 这里已经是 loadPenaltyData，不需要改
  } catch (error: any) {
    ElMessage.error(error.message || "修改失败");
  } finally {
    saving.value = false;
  }
}

const deletePenalty = async (row: any) => {
  try {
    await ElMessageBox.confirm("确定要删除这条罚款记录吗？", "警告", {
      type: "warning",
      confirmButtonText: "确认删除",
      cancelButtonText: "取消",
      customClass: "modern-message-box",
    });
    await adminApi.deletePenaltyRecord(row.id);
    ElMessage.success("删除成功");
    await loadPenaltyData();
  } catch (error: any) {
    if (error !== "cancel") {
      ElMessage.error(error.message || "删除失败");
    }
  }
};

const exportPenalty = async () => {
  try { await request.post("/standalone/export-authorize", {module:"penalty"}); } catch(e:any) { ElMessage.error(e.message); return; }
  if (!penaltyRecords.value.length) {
    ElMessage.warning("没有数据可导出");
    return;
  }
  const headers = [
    "员工姓名",
    "岗位",
    "罚款日期",
    "金额",
    "罚款类型",
    "罚款原因",
    "记录人",
  ];
  const rows = penaltyRecords.value.map((p) => [
    p.employee_name,
    p.position,
    p.penalty_date,
    p.amount,
    p.category,
    p.reason,
    p.created_by,
  ]);
  const csvContent = [
    headers.join(","),
    ...rows.map((row) => row.map((cell) => `"${cell || ""}"`).join(",")),
  ].join("\n");
  const blob = new Blob(["\uFEFF" + csvContent], {
    type: "text/csv;charset=utf-8;",
  });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `罚款记录_${penaltyMonth.value}.csv`;
  link.click();
  URL.revokeObjectURL(link.href);
};

// 加载员工绩效详情
const loadEmployeePerformance = async (employeeId: string) => {
  // ✅ 添加参数校验
  if (!employeeId) {
    employeePerformance.value = {
      employee_id: "",
      total_score: 10,
      grade: "合格",
      score_records: [],
    };
    return;
  }

  try {
    // ✅ 修复：确保月份不为 null
    let month = penaltyMonth.value;
    if (!month) {
      month = getCurrentYearMonth();
    }

    const response = await adminApi.getPerformance({
      month: month,
      employee_id: employeeId,
    });

    const data = response.data || {};
    if (data.items && data.items.length > 0) {
      const item = data.items[0];
      // ✅ 修复：适配字段名（可能是驼峰或下划线）
      let scoreRecords = item.scoreRecords || item.score_records || [];

      // ✅ 确保 score_records 是数组
      if (typeof scoreRecords === "string") {
        try {
          scoreRecords = JSON.parse(scoreRecords);
        } catch (e) {
          scoreRecords = [];
        }
      }

      employeePerformance.value = {
        employee_id: item.employeeId || item.employee_id || employeeId,
        employee_name: item.employeeName || item.employee_name || "",
        total_score: item.totalScore || item.total_score || 10,
        grade: item.grade || "合格",
        score_records: scoreRecords,
      };
    } else {
      employeePerformance.value = {
        employee_id: employeeId,
        total_score: 10,
        grade: "合格",
        score_records: [],
      };
    }
  } catch (error) {
    employeePerformance.value = {
      employee_id: employeeId,
      total_score: 10,
      grade: "合格",
      score_records: [],
    };
  }
};

// 加载员工罚款详情
const loadEmployeePenalty = async (employeeId: string) => {
  // ✅ 添加参数校验
  if (!employeeId) {
    employeePenaltyRecords.value = [];
    employeePenaltyTotal.value = 0;
    return;
  }

  try {
    // ✅ 修复：确保月份不为 null
    let month = penaltyMonth.value;
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
    const records = data.items || [];

    // ✅ 保存记录列表
    employeePenaltyRecords.value = records;
    // ✅ 计算总额
    employeePenaltyTotal.value = records.reduce(
      (sum: number, r: any) => sum + (r.amount || 0),
      0,
    );
  } catch (error) {
    employeePenaltyRecords.value = [];
    employeePenaltyTotal.value = 0;
  }
};

// 加载员工考勤数据
// 加载员工考勤数据
const loadEmployeeAttendance = async (employeeId: string) => {
  if (!employeeId) {
    employeeWorkDays.value = 0;
    employeeLeaveRestDays.value = 0;
    return;
  }

  try {
    let month = penaltyMonth.value;
    if (!month) {
      month = getCurrentYearMonth();
    }

    const response = await adminApi.getRecordsByEmployees(month, [employeeId]);
    const data = response.data || {};
    const empKey = String(employeeId);
    const records = data[empKey] || {};

    let workDays = 0;
    let leaveRestDays = 0;

    // ✅ 修复：正确遍历考勤记录
    for (const [dateKey, record] of Object.entries(records)) {
      const status = (record as any)?.status;

      // 解析日期，提取月份和日期用于判断是否在当前月份
      // 日期格式可能是 "2026-06-01T00:00:00Z" 或 "2026-06-01"
      let dateStr = dateKey;
      if (dateStr.includes("T")) {
        dateStr = dateStr.split("T")[0];
      }

      // 检查是否属于当前选择的月份
      const recordMonth = dateStr.substring(0, 7);
      if (recordMonth !== month) {
        continue; // 跳过不在当前月份的记录
      }

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

    employeeWorkDays.value = workDays;
    employeeLeaveRestDays.value = leaveRestDays;
  } catch (error) {
    employeeWorkDays.value = 0;
    employeeLeaveRestDays.value = 0;
  }
};

// 显示员工详情
const showEmployeeDetail = async (row: any) => {
  // 查找完整的员工信息
  const fullEmployee = employees.value.find((e) => e.id === row.employee_id);

  let hireDateValue = null;
  if (fullEmployee) {
    hireDateValue =
      fullEmployee.hire_date ||
      fullEmployee.hireDate ||
      fullEmployee.hiredate ||
      null;
  }

  if (!hireDateValue) {
    hireDateValue = row.hire_date || row.hireDate || null;
  }

  currentEmployee.value = {
    id: row.employee_id,
    name: row.employee_name,
    position: row.position,
    employee_id: row.employee_id,
    employeeId: row.employee_id,
    workLocation:
      fullEmployee?.workLocation || fullEmployee?.work_location || "现场",
    hireDate: hireDateValue,
    hire_date: hireDateValue,
  };

  await loadEmployeePerformance(row.employee_id);
  await loadEmployeePenalty(row.employee_id);
  await loadEmployeeAttendance(row.employee_id);

  detailDialogVisible.value = true;
};

const editEmployeeFromDetail = () => {
  if (currentEmployee.value) {
    detailDialogVisible.value = false;
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
    employeeEditDialogVisible.value = true;
  }
};

const confirmEditEmployee = async () => {
  if (!employeeFormRef.value) return;

  try {
    await employeeFormRef.value.validate();
  } catch {
    return;
  }

  if (!employeeForm.value.id) {
    ElMessage.error("员工ID不存在");
    return;
  }

  submittingEdit.value = true;
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

    // 刷新员工列表和罚款数据
    await loadEmployees();
    await loadPenaltyData();

    // ✅ 发送全局事件通知其他页面
    window.dispatchEvent(
      new CustomEvent("employee-updated", {
        detail: {
          id: employeeForm.value.id,
          data: {
            name: updateData.name,
            employeeId: updateData.employee_id,
            position: updateData.position,
            hireDate: updateData.hire_date,
            work_location: updateData.work_location,
          },
        },
      }),
    );

    employeeEditDialogVisible.value = false;
  } catch (error: any) {
    ElMessage.error(error.message || "更新失败");
  } finally {
    submittingEdit.value = false;
  }
};

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

    // ✅ 发送全局事件通知其他页面
    window.dispatchEvent(
      new CustomEvent("employee-deleted", {
        detail: { id: currentEmployee.value.id },
      }),
    );

    detailDialogVisible.value = false;
    await loadEmployees();
    await loadPenaltyData();
  } catch (error: any) {
    if (error !== "cancel") {
      ElMessage.error(error.message || "删除失败");
    }
  }
};

const jumpToPerformance = () => {
  if (currentEmployee.value) {
    detailDialogVisible.value = false;
    router.push({
      path: "/attendance/performance",
      query: { employee: currentEmployee.value.name },
    });
  }
};

const jumpToPenalty = () => {
  if (currentEmployee.value) {
    detailDialogVisible.value = false;
    router.push({
      path: "/attendance/penalty",
      query: { employee: currentEmployee.value.name },
    });
  }
};

// ========== 添加绩效分相关函数 ==========
// 从详情页打开添加绩效分对话框
const showScoreDialogFromDetail = () => {
  if (currentEmployee.value) {
    scoreForm.value = {
      current_score: employeePerformance.value?.total_score || 10,
      score: 0,
      reason: "",
      date: getToday(),
    };
    scoreDialogVisible.value = true;
  }
};

// 重置绩效分表单
const resetScoreForm = () => {
  scoreForm.value = {
    current_score: 10,
    score: 0,
    reason: "",
    date: "",
  };
  scoreFormRef.value?.clearValidate();
};

// 确认添加绩效分
const confirmAddScore = async () => {
  if (!scoreFormRef.value) return;
  try {
    await scoreFormRef.value.validate();
  } catch {
    return;
  }

  scoreSubmitting.value = true;
  try {
    const newRecord = {
      date: scoreForm.value.date,
      score: scoreForm.value.score,
      reason: scoreForm.value.reason,
    };

    const records = [
      ...(employeePerformance.value?.score_records || []),
      newRecord,
    ];
    let total = 10;
    for (const record of records) {
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

    await adminApi.batchSavePerformance({
      month: penaltyMonth.value,
      items: [
        {
          employee_id: currentEmployee.value.id,
          employee_name: currentEmployee.value.name,
          position: currentEmployee.value.position,
          base_score: 10,
          score_records: records,
          total_score: total,
          grade: grade,
        },
      ],
    });

    ElMessage.success("绩效分已添加");
    scoreDialogVisible.value = false;

    // 刷新当前员工的绩效数据
    await loadEmployeePerformance(currentEmployee.value.id);
  } catch (error: any) {
    ElMessage.error(error.message || "添加失败");
  } finally {
    scoreSubmitting.value = false;
  }
};

// 重置员工编辑表单
const resetEmployeeForm = () => {
  employeeForm.value = {
    id: null,
    name: "",
    employee_id: "",
    position: "",
    hire_date: "",
    work_location: "现场",
  };
  employeeFormRef.value?.clearValidate();
};

// 重置罚款表单（如果还没有的话）
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

// 从详情页打开添加罚款对话框
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

// ========== 辅助函数 ==========
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

const formatDate = (date: string | null | undefined) => {
  if (!date) return "-";
  const str = String(date);
  if (str.includes("-")) return str.substring(0, 10);
  const d = new Date(date);
  if (isNaN(d.getTime())) return "-";
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};

const formatShortDate = (dateStr: string) => {
  if (!dateStr) return "";
  if (typeof dateStr === "string" && dateStr.includes("-")) {
    return dateStr.slice(5); // 返回 "MM-DD"
  }
  return dateStr;
};

const getScoreType = (score: number) => {
  if (score >= 9) return "success";
  if (score >= 7) return "primary";
  if (score >= 5) return "warning";
  return "danger";
};

const getGradeType = (grade: string) => {
  const map: Record<string, string> = {
    优秀: "success",
    良好: "primary",
    合格: "warning",
    待提升: "danger",
    满分: "success",
    不合格: "danger",
  };
  return map[grade] || "info";
};

// ==================== 罚款 Telegram 推送配置 ====================
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

const loadPushSettings = async () => {
  try {
    const res = await adminApi.getPenaltyPushSettings();
    applyPushSettingsFromApi(res.data);
  } catch (e: any) {
    ElMessage.error(e.message || "加载推送配置失败");
  }
};

const savePushEnabled = async (enabled: boolean) => {
  pushEnabledSaving.value = true;
  try {
    const res = await adminApi.savePenaltyPushEnabled(enabled);
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
    const res = await adminApi.savePenaltyPushToken(pushTokenInput.value.trim());
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
    const res = await adminApi.discoverPenaltyPushChats();
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
    const res = await adminApi.bindPenaltyPushChat({
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
    const res = await adminApi.unbindPenaltyPushChat();
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
    await adminApi.testPenaltyPush();
    ElMessage.success("测试消息已发送");
  } catch (e: any) {
    ElMessage.error(e.message || "测试推送失败");
  } finally {
    pushTesting.value = false;
  }
};

// ==================== 事件监听 ====================
const handleEmployeeUpdated = async (event: any) => {
  const { id, data } = event.detail;
  const index = employees.value.findIndex((e) => e.id === id);
  if (index !== -1) {
    employees.value[index] = {
      ...employees.value[index],
      name: data.name,
      employee_name: data.name, // ✅ 添加这一行
      employee_id: data.employeeId,
      position: data.position,
      hire_date: data.hireDate,
      work_location: data.work_location,
    };
  }

  await loadPenaltyData();
};

const handleEmployeeDeleted = (event: any) => {
  const { id } = event.detail;
  // 从本地员工列表中移除
  employees.value = employees.value.filter((e) => e.id !== id);

  // 刷新罚款数据
  loadPenaltyData();
};

async function loadFineCurrency() {
  try {
    const res = await workforceTelegramApi.getSettings();
    fineCurrency.value = normalizeFineCurrency(res.data?.fineCurrency);
  } catch {
    fineCurrency.value = "CNY";
  }
}

onMounted(async () => {
  try {
    // 兼容旧链接：罚款详情「待处理」页签已独立为「待确认」菜单
    if (String(route.query.tab || "") === "pending") {
      router.replace({
        path: "/attendance/review",
        query: route.query.employee
          ? { employee: String(route.query.employee) }
          : undefined,
      });
      return;
    }
    await loadFineCurrency();
    await loadEmployees();
    await loadPenaltyData();
  } catch (e) {
    console.error("PenaltyDetails mount failed:", e);
  }

  // ✅ 添加这个：监听员工更新和删除事件
  window.addEventListener("employee-updated", handleEmployeeUpdated);
  window.addEventListener("employee-deleted", handleEmployeeDeleted);
});

// ✅ 添加这个：组件销毁时移除事件监听
onUnmounted(() => {
  window.removeEventListener("employee-updated", handleEmployeeUpdated);
  window.removeEventListener("employee-deleted", handleEmployeeDeleted);
});
</script>
<style scoped>
@import "./attendance.css";

.penalty-tabs {
  margin-top: 4px;
}

.penalty-tabs :deep(.el-tabs__header) {
  margin-bottom: 18px;
}

.penalty-tabs :deep(.el-tabs__nav-wrap::after) {
  height: 1px;
  background: #e2e8f0;
}

.penalty-tabs :deep(.el-tabs__item) {
  font-size: 15px;
  font-weight: 600;
  color: #64748b;
  padding: 0 22px;
  height: 42px;
  line-height: 42px;
}

.penalty-tabs :deep(.el-tabs__item.is-active) {
  color: #1e293b;
}

.penalty-tabs :deep(.el-tabs__active-bar) {
  height: 3px;
  border-radius: 3px 3px 0 0;
  background: linear-gradient(90deg, #f59e0b, #ef4444);
}

.tab-label {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.tab-badge :deep(.el-badge__content) {
  transform: translateY(-1px);
}

.review-tip {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 16px;
  padding: 12px 14px;
  border-radius: 12px;
  background: linear-gradient(135deg, #fff7ed 0%, #fef2f2 100%);
  border: 1px solid rgba(245, 158, 11, 0.22);
}

.review-tip-icon {
  color: #f59e0b;
  margin-top: 2px;
  font-size: 16px;
}

.review-tip-text {
  color: #9a3412;
  font-size: 13px;
  line-height: 1.55;
}

.review-action-btn {
  border-radius: 8px;
}

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
  background: linear-gradient(135deg, #fff7ed 0%, #fef2f2 100%);
  border: 1px solid rgba(245, 108, 108, 0.18);
}

.push-bound-main {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  color: #f56c6c;
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
