<!-- frontend/views/attendance/AttendanceRecords.vue -->
<template>
  <div class="attendance-management">
    <!-- 背景装饰 -->
    <div class="bg-decoration">
      <div class="blob blob-1"></div>
      <div class="blob blob-2"></div>
      <div class="blob blob-3"></div>
    </div>
    <div class="records-content">
      <el-card class="filter-card" shadow="hover">
        <el-row :gutter="20" align="middle" class="records-filter-row">
          <el-col :span="4">
            <el-date-picker
              v-model="filters.yearMonth"
              type="month"
              placeholder="选择月份"
              format="YYYY年MM月"
              value-format="YYYY-MM"
              @change="handleMonthChange"
              style="width: 100%"
            />
          </el-col>
          <el-col :span="4">
            <el-input
              v-model="filters.searchKeyword"
              placeholder="搜索员工姓名"
              clearable
              :prefix-icon="Search"
              @input="handleSearch"
            />
          </el-col>
          <el-col :span="2">
            <el-switch
              v-model="autoSaveEnabled"
              active-text="自动"
              inactive-text="手动"
              @change="handleAutoSaveToggle"
            />
          </el-col>
          <el-col :span="8">
            <div class="batch-actions">
              <el-button type="primary" size="small" @click="toggleSelectAll"
                ><el-icon><Select /></el-icon
                >{{ isAllSelected ? "取消全选" : "全选" }}</el-button
              >
              <el-button
                type="success"
                size="small"
                v-permission="'attendance:edit'" @click="openBatchDialog"
                :disabled="selectedEmployees.length === 0"
                ><el-icon><Edit /></el-icon>批量操作 ({{
                  selectedEmployees.length
                }})</el-button
              >
              <el-button v-permission="'attendance:delete'"
                type="danger"
                size="small" @click="batchDeleteEmployees"
                :disabled="selectedEmployees.length === 0"
                ><el-icon><Delete /></el-icon>批量删除</el-button
              >
            </div>
          </el-col>
          <el-col :span="6" class="text-right">
            <el-button @click="refreshAttendanceData" :loading="refreshing">
              <el-icon><Refresh /></el-icon>刷新
            </el-button>
            <el-button
              v-permission="'attendance:edit'"
              type="primary"
              @click="showAddEmployeeDialog"
              ><el-icon><Plus /></el-icon>添加员工</el-button
            >
            <el-button
              v-permission="'attendance:edit'"
              @click="showImportDialog"
            >
              <el-icon><Upload /></el-icon>导入
            </el-button>
            <el-button
              v-if="!autoSaveEnabled"
              v-permission="'attendance:edit'"
              type="success"
              @click="saveAllAttendance"
              :loading="saving"
              ><el-icon><Check /></el-icon>保存全部</el-button
            >
            <el-button v-permission="'attendance:export'" @click="exportAttendanceTable"
              ><el-icon><Download /></el-icon>导出</el-button
            >
          </el-col>
        </el-row>
      </el-card>

      <el-card class="attendance-table-card" shadow="hover" v-loading="loading">
        <template #header>
          <div class="card-header">
            <div class="card-header-left">
              <span>
                <el-icon><Calendar /></el-icon>考勤表
                {{ filters.yearMonth || "请选择月份" }}
              </span>
              <div class="header-stats">
                <el-tag type="primary" size="small" effect="plain"
                  >总员工: {{ summaryStats.totalEmployees }}</el-tag
                >
                <el-tag
                  v-if="isCurrentMonthView"
                  :type="getAttendanceRateType(todayAttendanceRate)"
                  size="small"
                  effect="plain"
                >
                  当天出勤率: {{ todayAttendanceRate }}%
                </el-tag>
                <el-tag
                  v-if="selectedEmployees.length"
                  type="warning"
                  size="small"
                  effect="plain"
                >
                  已选择 {{ selectedEmployees.length }} 名员工
                </el-tag>
              </div>
            </div>
            <div class="legend">
              <span class="legend-item"><span class="dot late"></span>迟到</span>
              <span class="legend-item"
                ><span class="dot work"></span>出勤</span
              >
              <span class="legend-item"
                ><span class="dot rest-half"></span>休假半天(半休)</span
              >
              <span class="legend-item"
                ><span class="dot rest-full"></span>休假一天(全休)</span
              >
              <span class="legend-item"
                ><span class="dot leave"></span>请假半天(半假)</span
              >
              <span class="legend-item"
                ><span class="dot off-post"></span>请假一天(全假)</span
              >
              <span class="legend-item"
                ><span class="dot absent"></span>旷工</span
              >
              <span class="legend-item"
                ><span class="dot source-agent"></span>客户端打卡</span
              >
              <span class="legend-item"
                ><span class="dot source-telegram"></span>Telegram 打卡</span
              >
              <span class="legend-item"
                ><span class="dot source-web"></span>Web 后台打卡</span
              >
            </div>
          </div>
        </template>

        <div class="table-container-fixed-left">
          <table class="unified-table" cellspacing="0" cellpadding="0">
            <thead>
              <tr>
                <th class="checkbox-col fixed-col">
                  <el-checkbox
                    :model-value="isAllSelected"
                    :indeterminate="isIndeterminate"
                    @change="toggleSelectAll"
                  />
                </th>
                <th class="date-col fixed-col">入职日期</th>
                <th class="name-col fixed-col">姓名</th>
                <th class="position-col fixed-col">岗位</th>
                <th class="location-col fixed-col">办公地点</th>
                <th class="stat-col fixed-col">实际上班</th>
                <th class="stat-col fixed-col">请/休/旷工</th>
                <th
                  v-for="day in actualDays"
                  :key="day"
                  class="day-col scroll-col"
                  :style="{ minWidth: dayWidth }"
                >
                  {{ day }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="employee in filteredEmployees" :key="employee.id">
                <td class="checkbox-col fixed-col">
                  <el-checkbox
                    :model-value="selectedEmployees.includes(employee.id)"
                    @change="toggleSelectEmployee(employee.id)"
                  />
                </td>
                <td class="date-col fixed-col">
                  {{ formatDate(employee.hireDate) }}
                </td>
                <td class="name-col fixed-col">
                  <span
                    class="employee-name-link"
                    @click="showEmployeeDetail(employee)"
                    >{{ employee.name }}</span
                  >
                </td>
                <td class="position-col fixed-col">
                  {{ employee.position || "-" }}
                </td>
                <td class="location-col fixed-col">
                  <span class="location-text">{{
                    employee.workLocation || "现场"
                  }}</span>
                </td>
                <td class="stat-col fixed-col">
                  <span class="stat-value work-days">{{
                    getWorkDays(employee)
                  }}</span>
                </td>
                <td class="stat-col fixed-col">
                  <span class="stat-value rest-days">{{
                    getLeaveRestDays(employee)
                  }}</span>
                </td>
                <td
                  v-for="day in actualDays"
                  :key="day"
                  class="day-cell scroll-col"
                  :class="{ weekend: isWeekend(day) }"
                  :style="{ minWidth: dayWidth }"
                  @click.stop="openStatusDialog(employee, day)"
                >
                  <div class="day-cell-inner">
                    <el-tooltip
                      :disabled="!hasDayAttendanceTooltip(employee, day)"
                      placement="top"
                      :show-after="150"
                      :hide-after="0"
                      popper-class="attendance-day-tooltip"
                    >
                      <template #content>
                        <template
                          v-for="tip in [getDayAttendanceTooltip(employee, day)]"
                          :key="'tip'"
                        >
                          <div v-if="tip" class="attendance-tip-panel">
                            <div class="attendance-tip-header">
                              <span class="attendance-tip-title">
                                打卡详情
                                <span
                                  v-if="tip.shiftLabel"
                                  class="attendance-tip-shift"
                                  :class="`attendance-tip-shift--${tip.shiftType}`"
                                >
                                  {{ tip.shiftLabel }}
                                </span>
                              </span>
                              <span class="attendance-tip-date">{{
                                tip.dateLabel
                              }}</span>
                            </div>
                            <div
                              v-if="tip.checkinSource || tip.checkoutSource"
                              class="attendance-tip-row attendance-tip-row--source"
                            >
                              <span class="attendance-tip-label">打卡方式</span>
                              <span class="attendance-tip-badges">
                                <span
                                  v-if="tip.checkinSource"
                                  class="attendance-tip-badge"
                                  :class="`attendance-tip-badge--${tip.checkinSource.toLowerCase()}`"
                                >
                                  上班 {{ tip.checkinSource }}
                                </span>
                                <span
                                  v-if="tip.checkoutSource"
                                  class="attendance-tip-badge"
                                  :class="`attendance-tip-badge--${tip.checkoutSource.toLowerCase()}`"
                                >
                                  下班 {{ tip.checkoutSource }}
                                </span>
                              </span>
                            </div>
                            <div class="attendance-tip-row">
                              <span class="attendance-tip-label">上班时间</span>
                              <span class="attendance-tip-row-meta">
                                <span class="attendance-tip-value">{{
                                  tip.checkinTime
                                }}</span>
                                <span
                                  v-if="tip.checkinFlag"
                                  class="attendance-tip-flag"
                                  :class="`attendance-tip-flag--${tip.checkinFlagKind}`"
                                >{{ tip.checkinFlag }}</span>
                              </span>
                            </div>
                            <div class="attendance-tip-row">
                              <span class="attendance-tip-label">下班时间</span>
                              <span class="attendance-tip-row-meta">
                                <span class="attendance-tip-value">{{
                                  tip.checkoutTime
                                }}</span>
                                <span
                                  v-if="tip.checkoutFlag"
                                  class="attendance-tip-flag"
                                  :class="`attendance-tip-flag--${tip.checkoutFlagKind}`"
                                >{{ tip.checkoutFlag }}</span>
                              </span>
                            </div>
                            <div
                              v-if="tip.duration"
                              class="attendance-tip-duration"
                            >
                              <span class="attendance-tip-label">工作时长</span>
                              <span
                                class="attendance-tip-duration-value"
                                :class="{ 'is-active': tip.inProgress }"
                              >
                                {{ tip.duration }}
                              </span>
                            </div>
                            <div
                              v-if="tip.pureDuration"
                              class="attendance-tip-duration attendance-tip-duration--pure"
                            >
                              <span class="attendance-tip-label">纯工作时长</span>
                              <span class="attendance-tip-duration-value is-pure">
                                {{ tip.pureDuration }}
                              </span>
                            </div>
                          </div>
                        </template>
                      </template>
                      <span
                        :class="[
                          'status-badge',
                          getStatusClass(getDayDisplayStatus(employee, day)),
                        ]"
                      >
                        {{ getStatusText(getDayDisplayStatus(employee, day)) }}
                      </span>
                    </el-tooltip>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-if="actualDays.length > 15" class="scroll-hint">
          <el-icon><ArrowRight /></el-icon> 横向滚动查看更多日期
        </div>

        <div class="employee-pagination">
          <el-pagination
            v-model:current-page="employeePage"
            v-model:page-size="employeePageSize"
            :page-sizes="[10, 20, 50, 100]"
            :total="employeeTotal"
            layout="total, sizes, prev, pager, next, jumper"
            @size-change="handleEmployeeSizeChange"
            @current-change="handleEmployeeCurrentChange"
          />
        </div>
      </el-card>

      <!-- 状态设置对话框 -->
      <el-dialog
        v-model="statusDialogVisible"
        title="设置考勤状态"
        width="300px"
        @close="resetStatusForm"
      >
        <el-form label-width="80px">
          <el-form-item label="员工">
            <span>{{ currentStatusEmployee?.name }}</span>
          </el-form-item>
          <el-form-item label="日期">
            <span>{{ currentStatusDate }}</span>
          </el-form-item>
          <el-form-item label="考勤状态">
            <el-radio-group v-model="tempStatus">
              <el-radio value="work">✅ 出勤</el-radio>
              <el-radio value="rest_half">🌙 半休</el-radio>
              <el-radio value="rest_full">🌙🌙 全休</el-radio>
              <el-radio value="leave">📝 半假</el-radio>
              <el-radio value="off_post">📝📝 全假</el-radio>
              <el-radio value="absent">❌ 旷工</el-radio>
              <el-radio value="resigned">📄 离职</el-radio>
              <el-radio value="-">➖ 清空（恢复离职）</el-radio>
            </el-radio-group>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="statusDialogVisible = false">取消</el-button>
          <el-button
            type="primary"
            v-permission="'attendance:edit'" @click="confirmStatusChange"
            :loading="statusChanging"
          >
            确定
          </el-button>
        </template>
      </el-dialog>

      <!-- 员工添加/编辑对话框 -->
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
          <el-form-item
            v-if="!employeeForm.id"
            label="子账号"
            prop="username"
          >
            <el-input
              v-model="employeeForm.username"
              placeholder="例如 test，TG 发送 /bind test 绑定"
            />
          </el-form-item>
          <el-form-item
            v-if="!employeeForm.id"
            label="初始密码"
            prop="password"
          >
            <el-input
              v-model="employeeForm.password"
              type="password"
              show-password
              placeholder="至少6位"
            />
          </el-form-item>
          <el-form-item label="姓名" prop="name">
            <el-input v-model="employeeForm.name" placeholder="请输入姓名" />
          </el-form-item>
          <el-form-item v-if="employeeForm.id" label="工号" prop="employee_id">
            <el-input
              v-model="employeeForm.employee_id"
              placeholder="与登录账号一致"
            />
          </el-form-item>
          <el-form-item label="部门" prop="department">
            <el-input
              v-model="employeeForm.department"
              placeholder="请输入部门"
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
          <el-button
            type="primary"
            v-permission="'attendance:edit'" @click="submitEmployee"
            :loading="submittingEmployee"
            >确定</el-button
          >
        </template>
      </el-dialog>

      <!-- 导入考勤对话框 -->
      <el-dialog
        v-model="importDialogVisible"
        title="导入考勤"
        width="560px"
        @close="resetImportForm"
      >
        <el-form label-width="100px">
          <el-form-item label="导入月份" required>
            <el-date-picker
              v-model="importForm.yearMonth"
              type="month"
              placeholder="选择月份"
              format="YYYY年MM月"
              value-format="YYYY-MM"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item label="默认密码">
            <el-input
              v-model="importForm.defaultPassword"
              type="password"
              show-password
              placeholder="新员工未填密码时使用（至少6位）"
            />
          </el-form-item>
          <el-form-item label="导入模板">
            <div class="import-template-actions">
              <el-button @click="downloadEmployeeImportTemplate">
                <el-icon><Download /></el-icon>员工模板
              </el-button>
              <el-button @click="downloadImportTemplate">
                <el-icon><Download /></el-icon>考勤模板
              </el-button>
            </div>
          </el-form-item>
          <el-form-item label="导入文件" required>
            <el-upload
              ref="importUploadRef"
              :auto-upload="false"
              :limit="1"
              accept=".xlsx,.xls,.csv"
              :on-change="handleImportFileChange"
              :on-remove="handleImportFileRemove"
            >
              <el-button type="primary">
                <el-icon><Upload /></el-icon>选择文件
              </el-button>
              <template #tip>
                <div class="import-tip">
                  支持仅导入员工：入职日期、姓名、岗位、办公地点、登录账号、密码（新员工自动创建子账号与邀请码）。
                  也可在模板中增加日期列（1、2、3…）一并导入考勤，状态填出勤/半休/全休/半假/旷工/全假/离职。
                </div>
              </template>
            </el-upload>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="importDialogVisible = false">取消</el-button>
          <el-button
            type="primary"
            :loading="importSubmitting"
            v-permission="'attendance:edit'" @click="submitImportAttendance"
          >
            开始导入
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
        <div class="detail-container">
          <!-- 头部信息 -->
          <div class="detail-header">
            <div class="avatar-section">
              <el-avatar :size="64" :icon="User" class="employee-avatar" />
              <div class="employee-title">
                <h3 class="employee-name">{{ currentEmployee?.name }}</h3>
                <div class="employee-badges">
                  <el-tag size="small" type="primary">{{
                    currentEmployee?.position || "员工"
                  }}</el-tag>
                  <el-tag
                    size="small"
                    :type="
                      currentEmployee?.workLocation === '现场'
                        ? 'success'
                        : 'warning'
                    "
                  >
                    {{ currentEmployee?.workLocation || "现场" }}
                  </el-tag>
                </div>
              </div>
            </div>
            <div class="employee-id">
              <el-icon><OfficeBuilding /></el-icon>
              <span>工号: {{ currentEmployee?.employeeId || currentEmployee?.username || "-" }}</span>
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
                  <span class="stat-work"
                    >{{ getWorkDays(currentEmployee) }}天</span
                  >
                  <span class="stat-divider">/</span>
                  <span class="stat-leave"
                    >{{ getLeaveRestDays(currentEmployee) }}天</span
                  >
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
                      circle @click="showPenaltyDialogFromDetail"
                    >
                      <el-icon><Plus /></el-icon>
                    </el-button>
                  </el-tooltip>
                </div>
                <div class="stat-card-value">
                  <span
                    :class="currentPenaltyTotal > 0 ? 'penalty-amount' : ''"
                  >
                    ¥{{ currentPenaltyTotal }}
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
              <span class="divider-text">
                <el-icon><TrendCharts /></el-icon>
                绩效记录
              </span>
            </el-divider>
            <div class="records-list">
              <div
                v-for="(record, idx) in currentPerformance.score_records.slice(
                  -6,
                )"
                :key="idx"
                class="record-item score-item"
              >
                <div class="record-date-badge">
                  <span class="record-day">{{
                    formatDateShort(record.date)
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
              v-if="(currentPerformance?.score_records?.length || 0) > 6"
              class="view-all-link"
            >
              <el-button link type="primary" @click="jumpToPerformance">
                查看全部 {{ currentPerformance?.score_records?.length }} 条记录
                <el-icon><ArrowRight /></el-icon>
              </el-button>
            </div>
          </div>

          <!-- 罚款记录区域 -->
          <div v-if="currentPenaltyRecords.length > 0" class="records-section">
            <el-divider style="margin: 12px 0">
              <span class="divider-text">
                <el-icon><Money /></el-icon>
                罚款记录
              </span>
            </el-divider>
            <div class="records-list">
              <div
                v-for="(record, idx) in currentPenaltyRecords.slice(-5)"
                :key="idx"
                class="record-item penalty-item"
              >
                <div class="record-date-badge penalty-date">
                  <span>{{
                    record.penaltyDate || record.penalty_date || "-"
                  }}</span>
                </div>
                <div class="record-content">
                  <span class="penalty-amount-badge">¥{{ record.amount }}</span>
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
            <div v-if="currentPenaltyRecords.length > 5" class="view-all-link">
              <el-button link type="primary" @click="jumpToPenalty">
                查看全部 {{ currentPenaltyRecords.length }} 条记录
                <el-icon><ArrowRight /></el-icon>
              </el-button>
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
            <el-button type="primary" v-permission="'attendance:edit'" @click="editFromDetail">
              <el-icon><Edit /></el-icon>编辑信息
            </el-button>
            <el-button v-permission="'attendance:delete'" type="danger" plain @click="deleteFromDetail">
              <el-icon><Delete /></el-icon>删除员工
            </el-button>
          </div>
        </template>
      </el-dialog>

      <!-- 添加绩效分对话框 -->
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
          <el-form-item label="员工岗位">
            <div class="info-field">
              <el-icon><Briefcase /></el-icon>
              <span class="info-text">{{
                currentEmployee?.position || "-"
              }}</span>
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
              <span class="info-text highlight">
                {{ scoreForm.current_score + (scoreForm.score || 0) }}分
              </span>
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
              type="primary" @click="confirmAddScore"
              :loading="scoreSubmitting"
              class="submit-button"
            >
              <el-icon><Check /></el-icon>确定添加
            </el-button>
          </div>
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
            />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="penaltyDialogVisible = false">取消</el-button>
          <el-button v-permission="'penalty:edit'"
            type="primary" @click="confirmAddPenalty"
            :loading="penaltySubmitting"
          >
            确定
          </el-button>
        </template>
      </el-dialog>

      <!-- 批量设置对话框 -->
      <el-dialog
        v-model="batchDialogVisible"
        title="批量设置考勤"
        width="450px"
      >
        <el-form label-width="100px">
          <el-form-item label="选择员工">
            <div class="batch-employees">
              <el-tag
                v-for="emp in batchEmployees"
                :key="emp.id"
                size="small"
                style="margin: 2px"
                >{{ emp.name }}</el-tag
              >
            </div>
          </el-form-item>
          <el-form-item label="选择日期" required>
            <el-date-picker
              v-model="batchDate"
              type="date"
              placeholder="选择日期"
              format="YYYY-MM-DD"
              value-format="YYYY-MM-DD"
              style="width: 100%"
            />
          </el-form-item>
          <el-form-item label="考勤状态" required>
            <el-radio-group v-model="batchStatus">
              <el-radio value="-">➖ 清空（恢复离职）</el-radio>
              <el-radio value="work"
                ><span class="work-option">✅ 出勤</span></el-radio
              >
              <el-radio value="rest_half"
                ><span class="rest-half-option">🌙 休假半天</span></el-radio
              >
              <el-radio value="rest_full"
                ><span class="rest-full-option">🌙🌙 休假一天</span></el-radio
              >
              <el-radio value="leave"
                ><span class="leave-option">📝 请假半天</span></el-radio
              >
              <el-radio value="off_post"
                ><span class="off-post-option">📝📝 请假一天</span></el-radio
              >
              <el-radio value="absent"
                ><span class="absent-option">❌ 旷工</span></el-radio
              >
              <el-radio value="resigned"
                ><span class="resigned-option">📄 离职</span></el-radio
              >
            </el-radio-group>
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="batchDialogVisible = false">取消</el-button>
          <el-button
            type="primary"
            v-permission="'attendance:edit'" @click="confirmBatchSet"
            :loading="batchSetting"
            >确定</el-button
          >
        </template>
      </el-dialog>
    </div>
  </div>
</template>

<script setup lang="ts">
import request from "@api/request";
import { ref, computed, onMounted, watch, nextTick } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  Check,
  Download,
  Plus,
  Edit,
  Delete,
  Calendar,
  User,
  DataLine,
  Search,
  Select,
  ArrowRight,
  Money,
  TrendCharts,
  OfficeBuilding,
  Ticket,
  Refresh,
  Briefcase,
  Star,
  Upload,
} from "@element-plus/icons-vue";
import adminApi from "@api/admin_api";
import { useUserStore } from "@store/user";
import {
  getCurrentYearMonth,
  getToday,
  formatDate,
  getAttendanceRateType,
  getStatusClass,
  getStatusText,
  normalizeAttendanceStatus,
  getScoreType,
  getGradeType,
  formatDateShort,
  getPenaltyCategoryType,
  formatAttendanceClockTime,
  formatWorkDuration,
  formatDurationSeconds,
  workDurationSeconds,
} from "./helpers";

const route = useRoute();
const router = useRouter();

// ========== 响应式数据 ==========
const filters = ref({ yearMonth: getCurrentYearMonth(), searchKeyword: "" });
const employees = ref<any[]>([]);
const attendanceData = ref<Record<string, any>>({});
const selectedEmployees = ref<any[]>([]);
const resignationDates = ref<Record<string, string>>({});
const attendanceClears = ref<Record<string, string[]>>({});
const restoreEmployees = ref(new Set<string>());
const employeePage = ref(1);
const todayRate = ref<number | null>(null);
const employeePageSize = ref(100);
const employeeTotal = ref(0);
const loading = ref(false);
const refreshing = ref(false);
const saving = ref(false);
const submittingEmployee = ref(false);
const batchSetting = ref(false);
const autoSaveEnabled = ref(true);
const batchDialogVisible = ref(false);
const batchDate = ref("");
const batchStatus = ref("work");
const batchEmployees = ref<any[]>([]);
const statusDialogVisible = ref(false);
const statusChanging = ref(false);
const currentStatusEmployee = ref<any>(null);
const currentStatusDate = ref("");
const currentStatusDay = ref<number | null>(null);
const tempStatus = ref("work");
const employeeDialogVisible = ref(false);
const importDialogVisible = ref(false);
const importSubmitting = ref(false);
const importUploadRef = ref<any>(null);
const importForm = ref({
  yearMonth: getCurrentYearMonth(),
  defaultPassword: "",
  file: null as File | null,
});
const employeeDialogTitle = ref("添加员工");
const employeeFormRef = ref<any>(null);
const employeeForm = ref({
  id: null,
  username: "",
  password: "",
  name: "",
  employee_id: "",
  department: "",
  position: "",
  hire_date: "",
  work_location: "现场",
});
const employeeRules = computed(() => {
  const rules: Record<string, any[]> = {
    name: [{ required: true, message: "请输入姓名", trigger: "blur" }],
    hire_date: [{ required: true, message: "请选择入职日期", trigger: "change" }],
  };
  if (employeeForm.value.id) {
    rules.employee_id = [{ required: true, message: "请输入工号", trigger: "blur" }];
  } else {
    rules.username = [{ required: true, message: "请输入登录账号（工号）", trigger: "blur" }];
  }
  return rules;
});
const detailDialogVisible = ref(false);
const currentEmployee = ref<any>(null);
const currentPerformance = ref<any>(null);
const currentPenaltyTotal = ref(0);
const currentPenaltyRecords = ref<any[]>([]);

// 绩效分对话框相关
const scoreDialogVisible = ref(false);
const scoreFormRef = ref<any>(null);
const scoreSubmitting = ref(false);
const scoreForm = ref({
  current_score: 10,
  score: 0,
  reason: "",
  date: "",
});
const scoreRules = {
  score: [{ required: true, message: "请输入加减分数", trigger: "blur" }],
  reason: [{ required: true, message: "请输入原因", trigger: "blur" }],
  date: [{ required: true, message: "请选择日期", trigger: "change" }],
};

// 罚款对话框相关
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

// ========== 计算属性 ==========
const dayWidth = computed(() => {
  const days = actualDays.value.length;
  if (days <= 20) return "90px";
  if (days <= 25) return "76px";
  return "64px";
});

const actualDays = computed(() => {
  if (!filters.value.yearMonth)
    return Array.from({ length: 31 }, (_, i) => i + 1);
  const [year, month] = filters.value.yearMonth.split("-");
  const lastDay = new Date(parseInt(year), parseInt(month), 0).getDate();
  return Array.from({ length: lastDay }, (_, i) => i + 1);
});

const isWeekend = (day: number) => {
  if (!filters.value.yearMonth) return false;
  const [year, month] = filters.value.yearMonth.split("-");
  const date = new Date(parseInt(year), parseInt(month) - 1, day);
  const dayOfWeek = date.getDay();
  return dayOfWeek === 0 || dayOfWeek === 6;
};

const filteredEmployees = computed(() => {
  const base = employees.value.filter((employee) => {
    const resignMonth = resignationDates.value[String(employee.id)]?.slice(
      0,
      7,
    );
    return !(
      resignMonth &&
      filters.value.yearMonth &&
      filters.value.yearMonth > resignMonth
    );
  });
  if (!filters.value.searchKeyword) return base;
  const keyword = filters.value.searchKeyword.toLowerCase();
  return base.filter((e) => e.name.toLowerCase().includes(keyword));
});

const isAllSelected = computed(
  () =>
    filteredEmployees.value.length > 0 &&
    selectedEmployees.value.length === filteredEmployees.value.length,
);
const isIndeterminate = computed(
  () =>
    selectedEmployees.value.length > 0 &&
    selectedEmployees.value.length < filteredEmployees.value.length,
);

const summaryStats = computed(() => {
  const totalEmps = employeeTotal.value;
  if (totalEmps === 0 || actualDays.value.length === 0) {
    return {
      totalEmployees: 0,
      totalWorkDays: 0,
      totalLeaveRestDays: 0,
      avgAttendanceRate: 0,
    };
  }
  let totalWork = 0;
  let totalLeaveRest = 0;
  for (const emp of filteredEmployees.value) {
    totalWork += getWorkDays(emp);
    totalLeaveRest += getLeaveRestDays(emp);
  }
  const totalPossibleWorkDays = totalEmps * actualDays.value.length;
  const attendanceRate =
    totalPossibleWorkDays > 0
      ? Math.round((totalWork / totalPossibleWorkDays) * 100)
      : 0;
  return {
    totalEmployees: totalEmps,
    totalWorkDays: totalWork,
    totalLeaveRestDays: totalLeaveRest,
    avgAttendanceRate: attendanceRate,
  };
});

const isCurrentMonthView = computed(
  () => filters.value.yearMonth === getCurrentYearMonth(),
);

const todayAttendanceRate = computed(() => {
  if (!isCurrentMonthView.value) return 0;
  return todayRate.value ?? 0;
});

// ========== 辅助函数 ==========
const handleAutoSaveToggle = (value: boolean) => {
  if (value) {
    ElMessage.success("自动保存已开启，修改后将自动保存");
  } else {
    ElMessage.info("自动保存已关闭，请记得手动点击「保存全部」按钮");
  }
};

// 加载员工列表（使用 skip/limit 与后端对齐）
const loadEmployees = async (): Promise<boolean> => {
  try {
    const userStore = useUserStore();
    const sessionOk = await userStore.ensureSession();
    if (!sessionOk) {
      ElMessage.error("登录已失效，请重新登录");
      return false;
    }

    // 计算 skip 值：skip = (当前页 - 1) * 每页条数
    const skip = (employeePage.value - 1) * employeePageSize.value;
    const response = await adminApi.getEmployees({
      skip: skip,
      limit: employeePageSize.value,
      search: filters.value.searchKeyword?.trim() || undefined,
      employment_scope: "month_visible",
      year_month: filters.value.yearMonth || getCurrentYearMonth(),
    });

    employees.value = response.data?.items || [];
    employeeTotal.value = response.data?.total ?? employees.value.length;

    if (employeeTotal.value > 0 && employees.value.length === 0) {
      ElMessage.warning("员工列表加载异常，请重启后端服务后重试");
    }

    // 确保每个员工都有考勤数据对象
    for (const emp of employees.value) {
      if (!attendanceData.value[String(emp.id)]) {
        attendanceData.value[String(emp.id)] = {};
      }
    }

    if (employees.value.length === 0 && employeeTotal.value === 0) {
      if (filters.value.searchKeyword?.trim()) {
        ElMessage.info("没有符合搜索条件的员工");
      } else {
        ElMessage.info("暂无员工数据，请点击「添加员工」按钮添加");
      }
    }
    return true;
  } catch (error: any) {
    employees.value = [];
    employeeTotal.value = 0;
    ElMessage.error("加载员工列表失败: " + (error.message || "未知错误"));
    return false;
  }
};

// 加载考勤数据
const loadAttendanceData = async () => {
  if (!filters.value.yearMonth) {
    ElMessage.warning("请选择月份");
    return;
  }

  if (employees.value.length === 0) {
    attendanceData.value = {};
    return;
  }

  loading.value = true;
  try {
    const employeeUUIDs = employees.value.map((emp) => emp.id);
    const response = await adminApi.getRecordsByEmployees(
      filters.value.yearMonth,
      employeeUUIDs,
    );
    const rawData = response.data || {};

    // 规范化数据：直接使用 YYYY-MM-DD 作为 key
    const normalizedData: Record<string, any> = {};
    for (const emp of employees.value) {
      const empKey = String(emp.id);
      const records = rawData[emp.id] || rawData[emp.employee_id] || {};

      const normalizedRecords: Record<string, any> = {};
      for (const [dateKey, record] of Object.entries(records)) {
        // 提取 YYYY-MM-DD 格式
        let normalizedKey = dateKey;
        if (dateKey.includes("T")) {
          normalizedKey = dateKey.split("T")[0];
        }
        normalizedRecords[normalizedKey] = record;
      }
      normalizedData[empKey] = normalizedRecords;
    }
    attendanceData.value = normalizedData;

    for (const emp of employees.value) {
      refreshEmployeeResignation(emp);
    }

    // 强制刷新视图
    await nextTick();
    await loadTodayAttendanceRate();
  } catch (error) {
    for (const emp of employees.value) {
      if (!attendanceData.value[String(emp.id)]) {
        attendanceData.value[String(emp.id)] = {};
      }
    }
  } finally {
    loading.value = false;
  }
};

// 刷新员工离职状态
const refreshEmployeeResignation = (employee: any) => {
  const empKey = String(employee.id);
  const records = attendanceData.value[empKey] || {};

  // 找出所有状态为 "resigned" 的日期
  const resignedDates = Object.keys(records)
    .filter((dateKey) => records[dateKey]?.status === "resigned")
    .sort();

  if (resignedDates.length > 0) {
    resignationDates.value[empKey] = resignedDates[resignedDates.length - 1];
  } else if (resignationDates.value[empKey]) {
    delete resignationDates.value[empKey];
  }
};

// 刷新所有数据
const refreshAttendanceData = async () => {
  refreshing.value = true;
  try {
    const ok = await loadEmployees();
    if (!ok) {
      return;
    }
    if (filters.value.yearMonth) {
      await loadAttendanceData();
    }
    ElMessage.success("数据已刷新");
  } catch {
    ElMessage.error("刷新失败");
  } finally {
    refreshing.value = false;
  }
};

// 分页处理
const handleEmployeeSizeChange = async (val: number) => {
  employeePageSize.value = val;
  employeePage.value = 1;
  await loadEmployees();
  if (filters.value.yearMonth) {
    await loadAttendanceData();
  }
};

const handleEmployeeCurrentChange = async (val: number) => {
  employeePage.value = val;
  await loadEmployees();
  if (filters.value.yearMonth) {
    await loadAttendanceData();
  }
};

// 月份变化
const handleMonthChange = async () => {
  employeePage.value = 1;
  selectedEmployees.value = [];
  await loadEmployees();
  await loadAttendanceData();
  await loadTodayAttendanceRate();
};

// 搜索
const handleSearch = () => {
  window.clearTimeout((handleSearch as any).timer);
  (handleSearch as any).timer = window.setTimeout(async () => {
    selectedEmployees.value = [];
    employeePage.value = 1;
    await loadEmployees();
    if (filters.value.yearMonth) {
      await loadAttendanceData();
    }
  }, 300);
};

// 批量选择
const toggleSelectAll = () => {
  if (isAllSelected.value) {
    selectedEmployees.value = [];
  } else {
    selectedEmployees.value = filteredEmployees.value.map((e) => e.id);
  }
};

const toggleSelectEmployee = (id: any) => {
  const index = selectedEmployees.value.indexOf(id);
  if (index === -1) selectedEmployees.value.push(id);
  else selectedEmployees.value.splice(index, 1);
};

// 获取考勤状态
const getDayStatus = (employee: any, day: number) => {
  const dateKey = `${filters.value.yearMonth}-${String(day).padStart(2, "0")}`;
  const empKey = String(employee.id);
  const empData = attendanceData.value[empKey] || {};

  // 直接通过 dateKey 获取状态（因为我们已经规范化为 YYYY-MM-DD 格式）
  const record = empData[dateKey];

  if (record && record.status) {
    return normalizeAttendanceStatus(record.status) || record.status;
  }

  // 如果没有找到，返回空字符串（表示未设置）
  return "";
};

const getDayDisplayStatus = (employee: any, day: number) => {
  const status = getDayStatus(employee, day);
  const record = getDayRecord(employee, day);
  return status === "work" && (Number(record?.lateMinutes) > 0 || !!record?.lateLabel) ? "late" : status;
};

const getDayRecord = (employee: any, day: number) => {
  const dateKey = `${filters.value.yearMonth}-${String(day).padStart(2, "0")}`;
  const empKey = String(employee.id);
  return attendanceData.value[empKey]?.[dateKey] || null;
};

const formatSourceShort = (source?: string) => {
  if (!source) return "";
  if (source === "telegram") return "TG";
  if (source === "agent") return "PC";
  if (source === "web") return "web";
  if (source === "system") return "系统";
  return source.slice(0, 3).toUpperCase();
};

const formatRecordSourceLabel = (record: any) => {
  if (!record?.checkinSource && !record?.checkin_source) return "";
  const checkin = formatSourceShort(
    record.checkinSource || record.checkin_source,
  );
  const checkout = formatSourceShort(
    record.checkoutSource || record.checkout_source,
  );
  if (checkout && checkout !== checkin) return `${checkin}/${checkout}`;
  return checkin;
};

const getRecordCheckinTime = (record: any) =>
  record?.checkinTime || record?.checkin_time || record?.checkInTime || "";

const getRecordCheckoutTime = (record: any) =>
  record?.checkoutTime || record?.checkout_time || record?.checkOutTime || "";

const hasDayAttendanceTooltip = (employee: any, day: number) => {
  const record = getDayRecord(employee, day);
  if (!record) return false;
  return !!(
    formatRecordSourceLabel(record) ||
    getRecordCheckinTime(record) ||
    getRecordCheckoutTime(record)
  );
};

type DayAttendanceTooltip = {
  dateLabel: string;
  shiftType: string;
  shiftLabel: string;
  checkinSource: string;
  checkoutSource: string;
  checkinTime: string;
  checkoutTime: string;
  duration: string;
  pureDuration: string;
  inProgress: boolean;
  checkinFlag: string;
  checkinFlagKind: "late" | "ontime" | "";
  checkoutFlag: string;
  checkoutFlagKind: "early" | "ontime" | "";
};

const getDayAttendanceTooltip = (
  employee: any,
  day: number,
): DayAttendanceTooltip | null => {
  const record = getDayRecord(employee, day);
  if (!record) return null;

  const checkinRaw = getRecordCheckinTime(record);
  const checkoutRaw = getRecordCheckoutTime(record);
  const checkinSource = formatSourceShort(
    record.checkinSource || record.checkin_source,
  );
  const checkoutSource = formatSourceShort(
    record.checkoutSource || record.checkout_source,
  );

  if (!checkinSource && !checkoutSource && !checkinRaw && !checkoutRaw) {
    return null;
  }

  const dateKey = `${filters.value.yearMonth}-${String(day).padStart(2, "0")}`;
  let duration = "";
  let pureDuration = "";
  let inProgress = false;

  if (checkinRaw && checkoutRaw) {
    duration = formatWorkDuration(checkinRaw, checkoutRaw);
    const workSec = workDurationSeconds(checkinRaw, checkoutRaw);
    const activitySec = Math.max(
      0,
      Math.floor(
        Number(
          record.activitySeconds ??
            record.activity_seconds ??
            0,
        ) || 0,
      ),
    );
    pureDuration = formatDurationSeconds(Math.max(0, workSec - activitySec));
  } else if (checkinRaw) {
    duration = "进行中";
    inProgress = true;
  }

  const shiftTypeRaw = String(record.shiftType || record.shift_type || "")
    .trim()
    .toLowerCase();
  const shiftType = shiftTypeRaw === "night" ? "night" : shiftTypeRaw === "day" ? "day" : "";
  const shiftLabel = shiftType === "night" ? "夜班" : shiftType === "day" ? "白班" : "";

  const lateLabel = normalizeExceptionLabel(
    "迟到",
    typeof record.lateLabel === "string" ? record.lateLabel : "",
    Number(record.lateMinutes),
  );
  const earlyLabel = normalizeExceptionLabel(
    "早退",
    typeof record.earlyLabel === "string" ? record.earlyLabel : "",
    Number(record.earlyMinutes),
  );

  let checkinFlag = lateLabel;
  let checkinFlagKind: DayAttendanceTooltip["checkinFlagKind"] = lateLabel
    ? "late"
    : "";
  if (checkinRaw && !lateLabel) {
    checkinFlag = "准时上班";
    checkinFlagKind = "ontime";
  }

  let checkoutFlag = earlyLabel;
  let checkoutFlagKind: DayAttendanceTooltip["checkoutFlagKind"] = earlyLabel
    ? "early"
    : "";
  if (checkoutRaw && !earlyLabel) {
    checkoutFlag = "准时下班";
    checkoutFlagKind = "ontime";
  }

  return {
    dateLabel: dateKey,
    shiftType,
    shiftLabel,
    checkinSource,
    checkoutSource,
    checkinTime: checkinRaw ? formatAttendanceClockTime(checkinRaw) : "-",
    checkoutTime: checkoutRaw ? formatAttendanceClockTime(checkoutRaw) : "-",
    duration,
    pureDuration,
    inProgress,
    checkinFlag,
    checkinFlagKind,
    checkoutFlag,
    checkoutFlagKind,
  };
};

const formatExceptionDuration = (secondsRaw: number) => {
  const totalSeconds = Math.max(0, Math.floor(secondsRaw || 0));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  if (hours > 0) return `${hours}小时${minutes}分${seconds}秒`;
  if (minutes > 0) return `${minutes}分${seconds}秒`;
  return `${seconds}秒`;
};

const normalizeExceptionLabel = (
  prefix: "迟到" | "早退",
  backendLabel: string,
  minutesRaw: number,
) => {
  const text = (backendLabel || "").trim();
  const match = text.match(/(\d+)\s*分钟?/);
  if (match) {
    const min = Number(match[1]);
    return `${prefix}${formatExceptionDuration(min * 60)}`;
  }
  if (Number.isFinite(minutesRaw) && minutesRaw > 0) {
    return `${prefix}${formatExceptionDuration(minutesRaw * 60)}`;
  }
  if (text === prefix) return prefix;
  return "";
};

const queueAttendanceClears = (empKey: string, dates: string[]) => {
  if (dates.length === 0) return;
  const existing = attendanceClears.value[empKey] || [];
  const merged = new Set([...existing, ...dates]);
  attendanceClears.value[empKey] = [...merged];
};

// 更新考勤状态
const updateDayStatus = (employee: any, day: number, status: string) => {
  if (!useUserStore().hasPermission("attendance:edit")) return;
  const empKey = String(employee.id);
  const dateKey = `${filters.value.yearMonth}-${String(day).padStart(2, "0")}`;

  if (!attendanceData.value[empKey]) {
    attendanceData.value[empKey] = {};
  }

  if (status === "-" && !useUserStore().hasPermission("attendance:delete")) { ElMessage.warning("清除考勤需要删除权限"); return; }
  if (status === "-") {
    const datesToClear: string[] = [];
    const currentStatus = attendanceData.value[empKey][dateKey]?.status;
    const hasResignedInMonth = Object.entries(
      attendanceData.value[empKey] || {},
    ).some(
      ([dk, rec]) =>
        dk.startsWith(filters.value.yearMonth) &&
        (rec as any)?.status === "resigned",
    );

    if (currentStatus === "resigned" || hasResignedInMonth) {
      for (const [dk, rec] of Object.entries(attendanceData.value[empKey])) {
        if (
          dk.startsWith(filters.value.yearMonth) &&
          (rec as any)?.status === "resigned"
        ) {
          datesToClear.push(dk);
          delete attendanceData.value[empKey][dk];
        }
      }
      restoreEmployees.value.add(empKey);
    } else if (attendanceData.value[empKey][dateKey]) {
      datesToClear.push(dateKey);
      delete attendanceData.value[empKey][dateKey];
    }

    queueAttendanceClears(empKey, datesToClear);
    refreshEmployeeResignation(employee);
    triggerAutoSave(employee.id);

    if (dateKey === getToday()) {
      void loadTodayAttendanceRate();
    }
    return;
  }

  attendanceData.value[empKey][dateKey] = {
    ...(attendanceData.value[empKey][dateKey] || {}),
    status,
  };

  if (status === "resigned") {
    const totalDays = actualDays.value.length;
    for (let nextDay = day + 1; nextDay <= totalDays; nextDay += 1) {
      const nextKey = `${filters.value.yearMonth}-${String(nextDay).padStart(2, "0")}`;
      attendanceData.value[empKey][nextKey] = { status: "resigned" };
    }
  }

  refreshEmployeeResignation(employee);
  triggerAutoSave(employee.id);

  if (dateKey === getToday()) {
    void loadTodayAttendanceRate();
  }
};

// 计算实际上班天数（只统计完整出勤）
// 计算实际上班天数
const getWorkDays = (employee: any) => {
  if (!employee) return 0;

  // 检查员工是否在当月已离职
  const resignDate = resignationDates.value[String(employee.id)];
  const currentYearMonth = filters.value.yearMonth;

  let count = 0;
  const empKey = String(employee.id);
  const records = attendanceData.value[empKey] || {};

  for (const [dateKey, value] of Object.entries(records)) {
    const status = normalizeAttendanceStatus((value as any).status);
    if (!status) continue;

    // 离职状态：跳过该日期及之后的所有日期
    if (status === "resigned") {
      // 如果离职日期小于等于当前日期，停止计算
      if (resignDate && resignDate <= dateKey) {
        continue;
      }
      // 否则继续（但离职状态本身不计入）
      continue;
    }

    // 只统计当月的数据
    if (dateKey.startsWith(currentYearMonth)) {
      if (status === "work") {
        count += 1;
      } else if (status === "rest_half" || status === "leave") {
        count += 0.5;
      }
      // rest_full, off_post, absent 都不计入
    }
  }

  return count;
};

// 计算请假/休假天数（统计所有非出勤状态，不含离职）
const getLeaveRestDays = (employee: any) => {
  if (!employee) return 0;

  const resignDate = resignationDates.value[String(employee.id)];
  const currentYearMonth = filters.value.yearMonth;
  const empKey = String(employee.id);
  const records = attendanceData.value[empKey] || {};
  let count = 0;

  for (const [dateKey, value] of Object.entries(records)) {
    const status = (value as any).status;
    if (!status) continue;

    // 离职状态：跳过该日期及之后的所有日期
    if (status === "resigned") {
      if (resignDate && resignDate <= dateKey) {
        continue;
      }
      continue;
    }

    // 只统计当月的数据
    if (dateKey.startsWith(currentYearMonth)) {
      if (status === "rest_half") {
        count += 0.5;
      } else if (status === "rest_full") {
        count += 1;
      } else if (status === "leave") {
        count += 0.5;
      } else if (status === "off_post") {
        count += 1;
      } else if (status === "absent") {
        count += 1; // 旷工也算缺勤
      }
      // work 不计入
    }
  }
  return count;
};

// 获取单日出勤率（当前页 fallback，全量统计请用 loadTodayAttendanceRate）
const getDailyAttendanceRate = (day: number) => {
  if (!filters.value.yearMonth) return 0;

  const currentDate = `${filters.value.yearMonth}-${String(day).padStart(2, "0")}`;

  let totalAttendance = 0;
  let validEmployeeCount = 0;

  for (const emp of filteredEmployees.value) {
    const resignDate = resignationDates.value[String(emp.id)];
    if (resignDate && resignDate <= currentDate) {
      continue;
    }

    validEmployeeCount++;
    const status = normalizeAttendanceStatus(getDayStatus(emp, day));

    if (status === "work") {
      totalAttendance += 1;
    } else if (status === "rest_half" || status === "leave") {
      totalAttendance += 0.5;
    }
  }

  if (validEmployeeCount === 0) return 0;

  return Math.round((totalAttendance / validEmployeeCount) * 100);
};

const loadTodayAttendanceRate = async () => {
  if(useUserStore().role !== "admin") { const day=Number(getToday().slice(8,10)); todayRate.value=actualDays.value.includes(day)?getDailyAttendanceRate(day):0; return; }
  if (!isCurrentMonthView.value) {
    todayRate.value = null;
    return;
  }

  try {
    const response = await adminApi.getTodayAttendanceRate();
    todayRate.value = response.data?.rate ?? 0;
  } catch {
    const day = Number(getToday().slice(8, 10));
    todayRate.value = actualDays.value.includes(day)
      ? getDailyAttendanceRate(day)
      : 0;
  }
};

// 状态对话框
const openStatusDialog = (employee: any, day: number) => {
  if (!useUserStore().hasPermission("attendance:edit")) return;
  currentStatusEmployee.value = employee;
  currentStatusDate.value = `${filters.value.yearMonth}-${String(day).padStart(2, "0")}`;
  currentStatusDay.value = day;
  tempStatus.value = getDayStatus(employee, day) || "work";
  statusDialogVisible.value = true;
};

const confirmStatusChange = async () => {
  statusChanging.value = true;
  try {
    if (currentStatusEmployee.value && currentStatusDay.value !== null) {
      updateDayStatus(
        currentStatusEmployee.value,
        currentStatusDay.value,
        tempStatus.value,
      );
    }
    if (!autoSaveEnabled.value) {
      ElMessage.success("考勤状态已更新（请记得手动保存）");
    }
    statusDialogVisible.value = false;
  } catch (error) {
    ElMessage.error("更新失败");
  } finally {
    statusChanging.value = false;
  }
};

const resetStatusForm = () => {
  currentStatusEmployee.value = null;
  currentStatusDate.value = "";
  currentStatusDay.value = null;
  tempStatus.value = "work";
};

// 自动保存
let autoSaveTimer: number | null = null;
const pendingSaves = ref(new Set<any>());
const AUTO_SAVE_DELAY = 500;

const triggerAutoSave = (employeeIds: any | any[]) => {
  if (!autoSaveEnabled.value) {
    return;
  }

  // 支持传入单个ID或ID数组
  const ids = Array.isArray(employeeIds) ? employeeIds : [employeeIds];

  ids.forEach((id) => {
    pendingSaves.value.add(id);
  });

  if (autoSaveTimer) {
    window.clearTimeout(autoSaveTimer);
  }

  autoSaveTimer = window.setTimeout(async () => {
    const employeesToSave = employees.value.filter((e) =>
      pendingSaves.value.has(e.id),
    );

    if (employeesToSave.length > 0) {
      try {
        await saveMultipleEmployeesAttendance(employeesToSave, false);
        pendingSaves.value.clear();
      } catch (error) {
        console.error("自动保存失败:", error);
      }
    }
  }, AUTO_SAVE_DELAY);
};

// 保存多个员工考勤
const saveMultipleEmployeesAttendance = async (
  employeeList: any[],
  showMessage = false,
) => {
  try {
    const saveData: Record<string, any> = {};
    const clearsPayload: Record<string, string[]> = {};
    const employeeIds = new Set(employeeList.map((e) => String(e.id)));

    for (const employee of employeeList) {
      const empKey = String(employee.id);
      const empRecords = attendanceData.value[empKey] || {};

      const recordsToSave: Record<string, any> = {};
      for (const [date, record] of Object.entries(empRecords)) {
        const status = (record as any)?.status;
        if (status && status !== "" && status !== "-") {
          recordsToSave[date] = { status: status };
        }
      }
      if (Object.keys(recordsToSave).length > 0) {
        saveData[employee.id] = recordsToSave;
      }

      const pendingClears = attendanceClears.value[empKey];
      if (pendingClears?.length) {
        clearsPayload[empKey] = [...pendingClears];
      }
    }

    for (const [empKey, dates] of Object.entries(attendanceClears.value)) {
      if (!employeeIds.has(empKey) && dates.length > 0) {
        clearsPayload[empKey] = [...dates];
      }
    }

    const restoreList = [...restoreEmployees.value].filter(
      (id) => employeeIds.has(id) || clearsPayload[id]?.length,
    );

    if (
      Object.keys(saveData).length === 0 &&
      Object.keys(clearsPayload).length === 0 &&
      restoreList.length === 0
    ) {
      return { success: true, savedCount: 0 };
    }

    await adminApi.saveRecords({
      year_month: filters.value.yearMonth,
      data: saveData,
      clears: clearsPayload,
      restoreEmployees: restoreList,
    });

    for (const empKey of Object.keys(clearsPayload)) {
      delete attendanceClears.value[empKey];
    }
    for (const empKey of restoreList) {
      restoreEmployees.value.delete(empKey);
    }

    if (showMessage) {
      ElMessage.success(`已保存 ${employeeList.length} 名员工的考勤数据`);
    }
    return { success: true, savedCount: Object.keys(saveData).length };
  } catch (error: any) {
    if (showMessage) {
      ElMessage.error(`保存失败: ${error.message}`);
    }
    throw error;
  }
};

// 保存所有考勤
const saveAllAttendance = async () => {
  saving.value = true;
  try {
    const saveData: Record<string, any> = {};
    const clearsPayload: Record<string, string[]> = {};

    for (const emp of employees.value) {
      const empKey = String(emp.id);
      const empRecords = attendanceData.value[empKey] || {};
      const recordsToSave: Record<string, any> = {};
      for (const [date, record] of Object.entries(empRecords)) {
        const status = (record as any).status;
        if (status && status !== "" && status !== "-") {
          recordsToSave[date] = { status };
        }
      }
      if (Object.keys(recordsToSave).length > 0) {
        saveData[emp.id] = recordsToSave;
      }
    }

    for (const [empKey, dates] of Object.entries(attendanceClears.value)) {
      if (dates.length > 0) {
        clearsPayload[empKey] = [...dates];
      }
    }

    const restoreList = [...restoreEmployees.value];

    if (
      Object.keys(saveData).length === 0 &&
      Object.keys(clearsPayload).length === 0 &&
      restoreList.length === 0
    ) {
      ElMessage.warning("没有数据需要保存");
      return;
    }

    await adminApi.saveRecords({
      year_month: filters.value.yearMonth,
      data: saveData,
      clears: clearsPayload,
      restoreEmployees: restoreList,
    });

    attendanceClears.value = {};
    restoreEmployees.value.clear();

    ElMessage.success("考勤数据已保存");
  } catch (error: any) {
    ElMessage.error(error.response?.data?.detail || "保存失败");
  } finally {
    saving.value = false;
  }
};

// 批量设置
const openBatchDialog = () => {
  if (selectedEmployees.value.length === 0) return;
  batchEmployees.value = employees.value.filter((e) =>
    selectedEmployees.value.includes(e.id),
  );
  batchDate.value = "";
  batchStatus.value = "work";
  batchDialogVisible.value = true;
};

const confirmBatchSet = async () => {
  if (!batchDate.value) {
    ElMessage.warning("请选择日期");
    return;
  }
  batchSetting.value = true;
  try {
    const day = parseInt(batchDate.value.split("-")[2]);
    const affectedEmployees: any[] = [];

    // 判断是否全选（选中了所有员工）
    const isSelectAll =
      selectedEmployees.value.length === filteredEmployees.value.length;

    for (const emp of batchEmployees.value) {
      const currentStatus = getDayStatus(emp, day);

      if (isSelectAll) {
        // 全选模式：只对空单元格操作
        if (!currentStatus || currentStatus === "") {
          updateDayStatus(emp, day, batchStatus.value);
          affectedEmployees.push(emp);
        }
      } else {
        // 部分选择模式：对所有选中的员工生效（覆盖）
        updateDayStatus(emp, day, batchStatus.value);
        affectedEmployees.push(emp);
      }
    }

    // 强制刷新视图
    attendanceData.value = { ...attendanceData.value };

    if (affectedEmployees.length === 0) {
      ElMessage.warning(
        "所选日期没有可操作的单元格（全选模式下只修改空单元格）",
      );
      batchDialogVisible.value = false;
      return;
    }

    if (autoSaveEnabled.value) {
      await saveMultipleEmployeesAttendance(affectedEmployees, true);
      ElMessage.success(
        `已为 ${affectedEmployees.length} 名员工设置 ${batchDate.value} 的考勤`,
      );
    } else {
      ElMessage.success(
        `已为 ${affectedEmployees.length} 名员工设置 ${batchDate.value} 的考勤（请记得手动保存）`,
      );
    }
    batchDialogVisible.value = false;
  } catch (error) {
    ElMessage.error("操作失败");
  } finally {
    batchSetting.value = false;
  }
};

// 员工管理
const showAddEmployeeDialog = () => {
  employeeDialogTitle.value = "添加员工";
  employeeForm.value = {
    id: null,
    username: "",
    password: "",
    name: "",
    employee_id: "",
    department: "",
    position: "",
    hire_date: "",
    work_location: "现场",
  };
  employeeDialogVisible.value = true;
};

const showImportDialog = () => {
  importForm.value = {
    yearMonth: filters.value.yearMonth || getCurrentYearMonth(),
    defaultPassword: "",
    file: null,
  };
  importUploadRef.value?.clearFiles();
  importDialogVisible.value = true;
};

const resetImportForm = () => {
  importForm.value = {
    yearMonth: getCurrentYearMonth(),
    defaultPassword: "",
    file: null,
  };
  importUploadRef.value?.clearFiles();
};

const handleImportFileChange = (file: { raw: File }) => {
  importForm.value.file = file.raw;
};

const handleImportFileRemove = () => {
  importForm.value.file = null;
};

const getImportTemplateDays = (yearMonth: string) => {
  if (!yearMonth) {
    return Array.from({ length: 31 }, (_, i) => i + 1);
  }
  const [year, month] = yearMonth.split("-");
  const lastDay = new Date(
    parseInt(year, 10),
    parseInt(month, 10),
    0,
  ).getDate();
  return Array.from({ length: lastDay }, (_, i) => i + 1);
};

const downloadCsv = (filename: string, headers: string[], rows: string[][]) => {
  const csvContent = [
    headers.join(","),
    ...rows.map((row) =>
      row.map((cell) => `"${String(cell).replace(/"/g, '""')}"`).join(","),
    ),
  ].join("\n");
  const blob = new Blob(["\uFEFF" + csvContent], {
    type: "text/csv;charset=utf-8;",
  });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = filename;
  link.click();
  URL.revokeObjectURL(link.href);
};

const downloadEmployeeImportTemplate = () => {
  const headers = ["入职日期", "姓名", "岗位", "办公地点", "登录账号", "密码"];
  const exampleRows = [
    ["2026-01-01", "张三", "客服", "现场", "zhangsan", "123456"],
    ["2026-01-15", "李四", "运营", "越南/居家", "lisi", "123456"],
  ];
  downloadCsv("考勤员工导入模板.csv", headers, exampleRows);
};

const downloadImportTemplate = () => {
  const yearMonth = importForm.value.yearMonth || getCurrentYearMonth();
  const days = getImportTemplateDays(yearMonth);
  const headers = [
    "入职日期",
    "姓名",
    "岗位",
    "办公地点",
    "工号",
    "登录账号",
    "初始密码",
    "实际上班",
    "请假/休假天数",
    ...days,
  ];
  const exampleRows = [
    [
      `${yearMonth}-01`,
      "张三",
      "客服",
      "现场",
      "E001",
      "zhangsan",
      "",
      "",
      "",
      ...days.map((day) => (day <= 5 ? "出勤" : "")),
    ],
    [
      `${yearMonth}-15`,
      "李四",
      "运营",
      "越南/居家",
      "E002",
      "lisi",
      "",
      "",
      "",
      ...days.map((day) => (day === 1 ? "出勤" : day === 2 ? "半休" : "")),
    ],
  ];
  downloadCsv(`考勤导入模板_${yearMonth}.csv`, headers, exampleRows);
};

const submitImportAttendance = async () => {
  if (!importForm.value.yearMonth) {
    ElMessage.warning("请选择导入月份");
    return;
  }
  if (!importForm.value.file) {
    ElMessage.warning("请选择导入文件");
    return;
  }
  if (
    importForm.value.defaultPassword &&
    importForm.value.defaultPassword.length < 6
  ) {
    ElMessage.warning("默认密码至少 6 位");
    return;
  }

  importSubmitting.value = true;
  try {
    const formData = new FormData();
    formData.append("year_month", importForm.value.yearMonth);
    if (importForm.value.defaultPassword) {
      formData.append("default_password", importForm.value.defaultPassword);
    }
    formData.append("file", importForm.value.file);

    const response = await adminApi.importAttendanceRecords(formData);
    const data = response.data || {};
    const created = data.createdEmployees ?? 0;
    const updated = data.updatedEmployees ?? 0;
    const cells = data.attendanceCells ?? 0;
    const newAccounts = data.newAccounts || [];

    let message = `导入完成：写入 ${cells} 条考勤`;
    if (created > 0) message += `，新增员工 ${created} 人`;
    if (updated > 0) message += `，更新已有员工 ${updated} 人`;
    ElMessage.success(message);

    if (newAccounts.length > 0) {
      const lines = newAccounts
        .slice(0, 8)
        .map(
          (item: any) =>
            `${item.name}（${item.username}）邀请码：${item.inviteCode}`,
        )
        .join("\n");
      const suffix =
        newAccounts.length > 8 ? `\n…等 ${newAccounts.length} 名新员工` : "";
      await ElMessageBox.alert(`${lines}${suffix}`, "新员工邀请码", {
        confirmButtonText: "知道了",
      });
    }

    importDialogVisible.value = false;
    filters.value.yearMonth = importForm.value.yearMonth;
    await loadEmployees();
    await loadAttendanceData();
  } catch (error: any) {
    ElMessage.error(
      error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        "导入失败",
    );
  } finally {
    importSubmitting.value = false;
  }
};

// 编辑员工 - 确保表单正确填充
const editEmployee = (row: any) => {
  employeeDialogTitle.value = "编辑员工";
  employeeForm.value = {
    id: row.id,
    username: row.username || "",
    name: row.name || "",
    employee_id: row.employeeId || row.username || "",
    department: row.department || "",
    position: row.position || "",
    hire_date: row.hireDate || "",
    work_location: row.workLocation || "现场",
  };
  employeeDialogVisible.value = true;
};

// 提交员工（创建/更新）
// 提交员工（创建/更新）
const submitEmployee = async () => {
  if (!employeeFormRef.value) return;

  try {
    await employeeFormRef.value.validate();
  } catch {
    return;
  }

  submittingEmployee.value = true;
  try {
    if (employeeForm.value.id) {
      // ========== 编辑员工 ==========
      const updateData = {
        name: employeeForm.value.name,
        employee_id: employeeForm.value.employee_id,
        department: employeeForm.value.department,
        position: employeeForm.value.position,
        hire_date: employeeForm.value.hire_date,
        work_location: employeeForm.value.work_location,
      };

      await adminApi.updateEmployee(employeeForm.value.id, updateData);
      ElMessage.success("员工信息已更新");

      // ✅ 重新加载员工列表
      await loadEmployees();

      // ✅ 刷新考勤数据
      if (filters.value.yearMonth) {
        await loadAttendanceData();
      }

      // ✅ 从最新数据中更新当前员工对象
      if (detailDialogVisible.value && currentEmployee.value) {
        const updated = employees.value.find(
          (e) => e.id === employeeForm.value.id,
        );
        if (updated) {
          currentEmployee.value = updated;
        }
      }

      employeeDialogVisible.value = false;
      return;
    }

    // ========== 创建新员工（子账号 + 邀请码 + 当天考勤）==========
    if (
      !employeeForm.value.username?.trim() ||
      !employeeForm.value.password ||
      employeeForm.value.password.length < 6
    ) {
      ElMessage.warning("请填写登录账号和至少6位初始密码");
      submittingEmployee.value = false;
      return;
    }
    const loginAccount = employeeForm.value.username.trim();
    const createData = {
      username: loginAccount,
      password: employeeForm.value.password,
      name: employeeForm.value.name,
      employee_id: loginAccount,
      department: employeeForm.value.department,
      position: employeeForm.value.position,
      hire_date: employeeForm.value.hire_date,
      work_location: employeeForm.value.work_location,
    };

    const response = await adminApi.createEmployeeAccount(createData);
    const payload = response.data || response;

    const newEmployeeObj = {
      id: payload.employeeId,
      name: createData.name,
      employeeId: createData.employee_id,
      position: createData.position || "-",
      department: createData.department || "",
      hireDate: createData.hire_date,
      workLocation: createData.work_location || "现场",
      activeInviteCode: payload.inviteCode || "",
    };

    employees.value.push(newEmployeeObj);
    ElMessage.success(`员工及子账号已创建，TG 发送 /bind ${loginAccount} 即可绑定`);
    attendanceData.value[newEmployeeObj.id] = {};
    employeeTotal.value = employees.value.length;

    if (filters.value.yearMonth) {
      await loadAttendanceData();
    }

    employeeDialogVisible.value = false;

    const totalPages = Math.ceil(employeeTotal.value / employeePageSize.value);
    if (employeePage.value !== totalPages && totalPages > 0) {
      employeePage.value = totalPages;
      await loadEmployees();
      if (filters.value.yearMonth) {
        await loadAttendanceData();
      }
    }
  } catch (error: any) {
    ElMessage.error(
      error.response?.data?.detail || error.message || "操作失败",
    );
  } finally {
    submittingEmployee.value = false;
  }
};

const batchDeleteEmployees = async () => {
  if (selectedEmployees.value.length === 0) return;
  try {
    await ElMessageBox.confirm(
      `确定要删除选中的 ${selectedEmployees.value.length} 名员工吗？`,
      "警告",
      {
        type: "warning",
      },
    );
    for (const empId of selectedEmployees.value) {
      await adminApi.deleteEmployee(empId);
      delete attendanceData.value[String(empId)];
    }
    employees.value = employees.value.filter(
      (e) => !selectedEmployees.value.includes(e.id),
    );
    selectedEmployees.value = [];
    ElMessage.success("删除成功");
    await refreshAttendanceData();
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error("删除失败");
  }
};

const deleteEmployee = async (row: any) => {
  try {
    await ElMessageBox.confirm(`确定要删除员工 "${row.name}" 吗？`, "警告", {
      type: "warning",
    });
    await adminApi.deleteEmployee(row.id);
    employees.value = employees.value.filter((e) => e.id !== row.id);
    delete attendanceData.value[row.id];
    selectedEmployees.value = selectedEmployees.value.filter(
      (id) => id !== row.id,
    );
    ElMessage.success("删除成功");
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error("删除失败");
  }
};

// 员工详情 - 加载绩效数据
const loadCurrentEmployeePerformance = async (employeeId: any) => {
  try {
    const response = await adminApi.getPerformance({
      month: filters.value.yearMonth,
      employee_id: employeeId,
    });
    const data = response.data || {};

    if (data.items && data.items.length > 0) {
      const item = data.items[0];
      // 适配驼峰字段名
      currentPerformance.value = {
        employee_id: item.employeeId || item.employee_id || employeeId,
        employee_name: item.employeeName || item.employee_name || "",
        total_score: item.totalScore || item.total_score || 10,
        grade: item.grade || "合格",
        score_records: item.scoreRecords || item.score_records || [],
      };
    } else {
      currentPerformance.value = {
        employee_id: employeeId,
        total_score: 10,
        grade: "合格",
        score_records: [],
      };
    }
  } catch (error) {
    console.error("加载绩效失败:", error);
    currentPerformance.value = {
      employee_id: employeeId,
      total_score: 10,
      grade: "合格",
      score_records: [],
    };
  }
};

// 员工详情 - 加载罚款数据
const loadCurrentEmployeePenalty = async (employeeId: any) => {
  try {
    const response = await adminApi.getPenaltyRecords({
      month: filters.value.yearMonth,
      employee_id: employeeId,
      page: 1,
      page_size: 1000,
    });
    const data = response.data || {};

    // 处理 items 为 null 的情况
    const items = data.items || [];
    currentPenaltyRecords.value = items;
    currentPenaltyTotal.value = items.reduce(
      (sum: number, r: any) => sum + (r.amount || 0),
      0,
    );
  } catch (error) {
    currentPenaltyRecords.value = [];
    currentPenaltyTotal.value = 0;
  }
};

// 员工详情 - 确保 currentEmployee 包含完整数据
const showEmployeeDetail = async (employee: any) => {
  // 直接使用传入的 employee 对象，它已经包含了所有字段
  currentEmployee.value = employee;

  await loadCurrentEmployeePerformance(employee.id);
  await loadCurrentEmployeePenalty(employee.id);
  detailDialogVisible.value = true;
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

const editFromDetail = () => {
  if (currentEmployee.value) {
    editEmployee(currentEmployee.value);
  }
};

const deleteFromDetail = async () => {
  if (currentEmployee.value) {
    detailDialogVisible.value = false;
    await deleteEmployee(currentEmployee.value);
  }
};

// ========== 绩效分相关函数 ==========
const showScoreDialogFromDetail = () => {
  if (currentEmployee.value) {
    scoreForm.value = {
      current_score: currentPerformance.value?.total_score || 10,
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
      ...(currentPerformance.value?.score_records || []),
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
      month: filters.value.yearMonth,
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
    await loadCurrentEmployeePerformance(currentEmployee.value.id);
  } catch (error: any) {
    ElMessage.error(error.message || "添加失败");
  } finally {
    scoreSubmitting.value = false;
  }
};

// ========== 罚款相关函数 ==========
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

    // 刷新当前员工的罚款数据
    await loadCurrentEmployeePenalty(currentEmployee.value.id);
  } catch (error: any) {
    ElMessage.error(error.message || "添加失败");
  } finally {
    penaltySubmitting.value = false;
  }
};

// 导出
const exportAttendanceTable = async () => {
  try { await request.post("/standalone/export-authorize", {module:"attendance"}); } catch(e:any) { ElMessage.error(e.message); return; }
  if (!employees.value.length || !filters.value.yearMonth) {
    ElMessage.warning("没有数据可导出");
    return;
  }
  const headers = [
    "入职日期",
    "姓名",
    "岗位",
    "办公地点",
    "实际上班",
    "请假/休假天数",
    ...actualDays.value,
  ];
  const rows = employees.value.map((emp) => {
    const row: any[] = [
      emp.hireDate || "",
      emp.name,
      emp.position || "-",
      emp.workLocation,
      getWorkDays(emp),
      getLeaveRestDays(emp),
    ];
    for (const day of actualDays.value) {
      const status = getDayStatus(emp, day);
      const map: Record<string, string> = {
        work: "出勤",
        rest_half: "半休",
        rest_full: "全休",
        leave: "半假",
        absent: "旷工",
        off_post: "全假",
        resigned: "离职",
      };
      row.push(map[status] || "");
    }
    return row;
  });
  const csvContent = [
    headers.join(","),
    ...rows.map((row) => row.map((cell) => `"${cell}"`).join(",")),
  ].join("\n");
  const blob = new Blob(["\uFEFF" + csvContent], {
    type: "text/csv;charset=utf-8;",
  });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `考勤表_${filters.value.yearMonth}.csv`;
  link.click();
  URL.revokeObjectURL(link.href);
};

// 监听路由参数
watch(
  () => route.query.employee,
  (value) => {
    if (value) {
      filters.value.searchKeyword = String(value);
    }
  },
  { immediate: true },
);

// 生命周期
onMounted(async () => {
  if (route.query.employee) {
    filters.value.searchKeyword = String(route.query.employee);
  }
  await loadEmployees();
  await loadAttendanceData();
});
</script>

<style scoped>
@import "./attendance.css";

.day-cell {
  vertical-align: middle;
  text-align: center;
}

.day-cell-inner {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  min-height: 28px;
  cursor: pointer;
}

.records-content {
  position: relative;
  z-index: 1;
  width: 100%;
}

.records-page-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.records-title {
  margin: 0 0 6px;
  font-size: 20px;
  font-weight: 700;
  color: #303133;
}

.records-subtitle {
  margin: 0;
  font-size: 13px;
  color: #909399;
  line-height: 1.5;
}

.records-filter-row {
  padding-top: 4px;
}

.import-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #909399;
  line-height: 1.6;
  max-width: 420px;
}

.import-template-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}
</style>

<style>
.attendance-day-tooltip.el-popper {
  padding: 0 !important;
  border: none !important;
  background: transparent !important;
  box-shadow: none !important;
}

.attendance-day-tooltip .el-popper__arrow::before {
  background: #1a2332 !important;
  border: 1px solid rgba(255, 255, 255, 0.08) !important;
}

.attendance-tip-panel {
  min-width: 220px;
  padding: 12px 14px;
  background: linear-gradient(145deg, #243044 0%, #151c28 100%);
  border-radius: 12px;
  box-shadow:
    0 12px 32px rgba(0, 0, 0, 0.28),
    inset 0 1px 0 rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.08);
}

.attendance-tip-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}

.attendance-tip-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 700;
  color: #ffffff;
  letter-spacing: 0.02em;
}

.attendance-tip-shift {
  display: inline-flex;
  align-items: center;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
  line-height: 1.4;
  letter-spacing: 0.02em;
}

/* 贴近截图里「上班 PC / 迟到」胶囊风格：浅底 + 描边 */
.attendance-tip-shift--day {
  color: #ffd7a8;
  background: rgba(232, 140, 48, 0.18);
  border: 1px solid rgba(232, 140, 48, 0.35);
}

.attendance-tip-shift--night {
  color: #9ec5ff;
  background: rgba(64, 128, 220, 0.18);
  border: 1px solid rgba(105, 177, 255, 0.35);
}

.attendance-tip-date {
  font-size: 11px;
  color: rgba(255, 255, 255, 0.45);
  font-variant-numeric: tabular-nums;
}

.attendance-tip-row {
  display: grid;
  grid-template-columns: 64px 1fr;
  align-items: center;
  gap: 8px;
  padding: 5px 0;
}

.attendance-tip-row--source {
  grid-template-columns: 64px 1fr;
}

.attendance-tip-label {
  flex-shrink: 0;
  font-size: 12px;
  color: rgba(255, 255, 255, 0.55);
}

.attendance-tip-value {
  width: 7.2ch;
  font-size: 13px;
  font-weight: 600;
  color: #ffffff;
  font-variant-numeric: tabular-nums;
  text-align: left;
  white-space: nowrap;
}

.attendance-tip-row-meta {
  display: grid;
  grid-template-columns: 7.2ch minmax(0, 1fr);
  align-items: center;
  justify-items: start;
  column-gap: 8px;
  width: 100%;
  min-width: 0;
}

.attendance-tip-flag {
  display: inline-flex;
  align-items: center;
  justify-self: end;
  max-width: 100%;
  padding: 1px 7px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
  line-height: 1.4;
  white-space: nowrap;
}

.attendance-tip-flag--late {
  color: #ffd7a8;
  background: rgba(232, 140, 48, 0.22);
  border: 1px solid rgba(232, 140, 48, 0.35);
}

.attendance-tip-flag--early {
  color: #ffc9c2;
  background: rgba(220, 88, 72, 0.2);
  border: 1px solid rgba(220, 88, 72, 0.35);
}

.attendance-tip-flag--ontime {
  color: #95de64;
  background: rgba(82, 196, 26, 0.16);
  border: 1px solid rgba(82, 196, 26, 0.32);
}

.attendance-tip-badges {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 6px;
}

.attendance-tip-badge {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.03em;
  white-space: nowrap;
}

.attendance-tip-badge--tg {
  background: rgba(41, 169, 227, 0.18);
  color: #7dd3fc;
  border: 1px solid rgba(41, 169, 227, 0.35);
}

.attendance-tip-badge--pc {
  background: rgba(82, 196, 26, 0.16);
  color: #95de64;
  border: 1px solid rgba(82, 196, 26, 0.32);
}

.attendance-tip-badge--web {
  background: rgba(250, 173, 20, 0.18);
  color: #ffd666;
  border: 1px solid rgba(250, 173, 20, 0.35);
}

.attendance-tip-duration {
  display: grid;
  grid-template-columns: 64px 1fr;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  padding-top: 10px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}

.attendance-tip-duration-value {
  font-size: 14px;
  font-weight: 700;
  color: #ffd666;
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.attendance-tip-duration-value.is-active {
  color: #69b1ff;
}

.attendance-tip-duration--pure {
  margin-top: 6px;
  padding-top: 0;
  border-top: none;
}

.attendance-tip-duration-value.is-pure {
  color: #91caff;
}
</style>
