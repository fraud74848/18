<template>
  <div class="telegram-attendance-page attendance-management">
    <div class="bg-decoration" aria-hidden="true">
      <div class="blob blob-1 tg-blob-1"></div>
      <div class="blob blob-2 tg-blob-2"></div>
      <div class="blob blob-3 tg-blob-3"></div>
    </div>

    <div class="tg-content">
      <el-alert
        v-if="!moduleEnabled"
        type="warning"
        show-icon
        :closable="false"
        class="tg-alert"
        title="Telegram 考勤模块已在服务端禁用"
        description="后端 .env 设置了 TELEGRAM_ATTENDANCE_ENABLED=false。删除或改为 true 并重启后端后可恢复。"
      />

      <el-alert
        v-if="moduleEnabled && captureLinkedEnvOverride"
        type="warning"
        show-icon
        :closable="false"
        class="tg-alert"
        title="环境变量覆盖了后台配置"
        description="TELEGRAM_AGENT_CAPTURE_ENABLED 已在 .env 中设置，将覆盖下方「考勤联动录屏」开关。删除该环境变量后可完全由后台控制。"
      />

      <el-card class="tg-main-card" shadow="never">
        <el-tabs v-model="activeTab" class="tg-tabs">
        <el-tab-pane name="settings">
          <template #label>
            <span class="tg-tab-label"><el-icon><Setting /></el-icon>基础配置</span>
          </template>
          <el-form v-loading="loadingSettings" label-width="140px" class="settings-form">
            <el-collapse v-model="settingsPanels" class="tg-collapse tg-collapse-grid">
              <el-collapse-item name="bot" class="panel-half">
                <template #title>
                  <span class="tg-panel-title">
                    <span class="tg-panel-num">01</span>Bot 连接与状态
                    <el-tag type="info" size="small" effect="plain" class="tg-title-tag">Webhook</el-tag>
                  </span>
                </template>
                <el-form-item label="启用 Telegram">
                  <el-switch v-model="settingsForm.enabled" :disabled="!canEdit" />
                  <span class="field-hint">关闭后 Bot 不再响应打卡</span>
                </el-form-item>
                <el-form-item label="Bot Token">
                  <el-input
                    v-model="settingsForm.botToken"
                    type="password"
                    show-password
                    placeholder="留空则不修改；填写后将验证并保存"
                    :disabled="!canEdit"
                    style="max-width: 420px"
                  />
                  <div class="field-hint block-hint" v-if="settings.hasBotToken">
                    已配置 Bot{{ settings.botUsername ? ` @${settings.botUsername}` : "" }}
                  </div>
                </el-form-item>
                <el-form-item label="Bot 模式">
                  <el-select v-model="settingsForm.botMode" :disabled="!canEdit" style="width: 200px">
                    
                    <el-option label="Webhook" value="webhook" />
                  </el-select>
                  <span class="field-hint">本项目固定使用 Webhook，保存后到「界面与权限」卡片点击注册。</span>
                </el-form-item>
                <template v-if="settingsForm.botMode === 'webhook'">
                  <el-form-item label="Webhook URL">
                    <el-input v-model="settingsForm.webhookUrl" :disabled="!canEdit" placeholder="https://your-domain/telegram/webhook" style="max-width: 520px" />
                    <div class="field-hint block-hint">须为 HTTPS，指向本项目的 /telegram/webhook</div>
                  </el-form-item>
                  <el-form-item label="Webhook Secret">
                    <el-tag v-if="settings.hasWebhookSecret" type="success" size="small">已配置</el-tag>
                    <el-tag v-else type="info" size="small">未配置</el-tag>
                    <el-button v-if="canEdit" style="margin-left: 8px" @click="regenerateWebhookSecret" :loading="regeneratingWebhookSecret">
                      生成 Secret
                    </el-button>
                    <div v-if="webhookSecret" class="field-hint block-hint">Webhook Secret 已生成；请到「界面与权限」卡片重新注册 Webhook</div>
                  </el-form-item>
                </template>
                <el-alert title="无需单独 Worker。Webhook 注册请到「界面与权限」卡片操作，运行状态请到首页概览查看。" type="info" :closable="false" />
                <el-form-item v-if="canEdit">
                  <el-button v-permission="'telegram:edit'" type="primary" :loading="savingSettings" @click="saveSettings">保存本节</el-button>
                </el-form-item>
              </el-collapse-item>

              <el-collapse-item name="ui" class="panel-half">
                <template #title>
                  <span class="tg-panel-title"><span class="tg-panel-num">02</span>界面与权限</span>
                </template>
                <div v-if="false" class="feature-grid feature-grid-pair">
                  <div class="feature-card" :class="{ 'is-on': settingsForm.requirePCLogin }">
                    <div class="feature-card-top">
                      <div class="feature-icon pc"><el-icon><User /></el-icon></div>
                      <el-switch v-model="settingsForm.requirePCLogin" :disabled="!canEdit" />
                    </div>
                    <h4 class="feature-title">要求 PC 登录</h4>
                    <p class="feature-desc">TG 上班前须在本机 Agent 登录对应员工</p>
                  </div>
                  <div
                    class="feature-card capture"
                    :class="{ 'is-on': settingsForm.captureLinkedToCheckin, 'is-locked': captureLinkedEnvOverride }"
                  >
                    <div class="feature-card-top">
                      <div class="feature-icon capture"><el-icon><VideoCamera /></el-icon></div>
                      <el-switch
                        v-model="settingsForm.captureLinkedToCheckin"
                        :disabled="!canEdit || captureLinkedEnvOverride"
                      />
                    </div>
                    <h4 class="feature-title">考勤联动录屏</h4>
                    <p class="feature-desc">
                      关：按「策略管理」自动录屏；开：上班开始、下班停止（客户端或 TG 均可触发）
                    </p>
                  </div>
                  <div class="feature-card" :class="{ 'is-on': settingsForm.webPunchOnly }">
                    <div class="feature-card-top">
                      <div class="feature-icon web"><el-icon><Monitor /></el-icon></div>
                      <el-switch v-model="settingsForm.webPunchOnly" :disabled="!canEdit" />
                    </div>
                    <h4 class="feature-title">仅后台打卡</h4>
                    <p class="feature-desc">
                      开：只允许网页打卡（不依赖 Bot/PC 登录），禁用 TG/PC 打卡；导出含未绑定员工。关：保持现有 TG/PC/后台行为
                    </p>
                  </div>
                </div>
                <el-form-item label="界面语言">
                  <el-select v-model="settingsForm.botUiLang" :disabled="!canEdit" style="width: 200px">
                    <el-option label="中越双语" value="both" />
                    <el-option label="仅中文" value="zh" />
                    <el-option label="仅越南语" value="vi" />
                  </el-select>
                  <span class="field-hint">Bot 键盘与帮助文案；群组可单独覆盖</span>
                </el-form-item>
                <el-form-item label="管理员 TG ID">
                  <el-input v-model="settingsForm.adminTelegramUserIds" :disabled="!canEdit" placeholder="逗号分隔，如 123456789" style="max-width: 420px" />
                  <div class="field-hint block-hint">可使用 Bot 管理命令；迟到/早退待确认会在群里 @ 这些账号；推送失败时兜底私信</div>
                </el-form-item>
                <div class="tg-setup-guide">
                  <ol>
                    <li>在「考勤月历」添加员工，同时设置子账号和密码。</li>
                    <li>在「打卡规则」保存 Bot Token，启用 Telegram。</li>
                    <li>把机器人加入打卡群并启用群组，员工发送 /bind 子账号完成绑定。</li>
                    <li>点击下面按钮注册 Webhook；更换 Token 或 Secret 后再注册。</li>
                  </ol>
                  <el-button type="primary" :loading="registeringWebhook" v-permission="'telegram:edit'" @click="registerWebhook">注册 / 更新 Webhook</el-button>
                  <p class="field-hint block-hint">此操作连接当前配置的机器人，不清空待处理消息。请使用独立机器人。</p>
                </div>
              </el-collapse-item>

              <el-collapse-item name="shift" class="panel-half">
                <template #title>
                  <span class="tg-panel-title"><span class="tg-panel-num">03</span>班次与日终重置</span>
                </template>
                <el-divider content-position="left">班次与时间</el-divider>
                <el-form-item label="时区">
                  <el-input v-model="settingsForm.timezone" :disabled="!canEdit" style="width: 200px" />
                </el-form-item>
                <el-form-item label="白班时段">
                  <div class="inline-pair">
                    <el-input v-model="settingsForm.dayStart" placeholder="开始 09:00" :disabled="!canEdit" style="width: 120px" />
                    <span>至</span>
                    <el-input v-model="settingsForm.dayEnd" placeholder="结束 21:00" :disabled="!canEdit" style="width: 120px" />
                  </div>
                </el-form-item>
                <el-form-item label="双班模式">
                  <el-switch v-model="settingsForm.dualShiftEnabled" :disabled="!canEdit || settingsForm.tripleShiftEnabled" />
                  <span class="field-hint">启用白班/夜班分开打卡与交接班</span>
                </el-form-item>
                <el-form-item label="三班模式">
                  <el-switch v-model="settingsForm.tripleShiftEnabled" :disabled="!canEdit" @change="onTripleShiftChange" />
                  <span class="field-hint">启用白班/中班/夜班；交接班按 白→中→夜→白 轮转（开启后自动含双班能力）</span>
                </el-form-item>
                <template v-if="settingsForm.dualShiftEnabled || settingsForm.tripleShiftEnabled">
                  <el-form-item label="夜班时段">
                    <div class="inline-pair">
                      <el-input v-model="settingsForm.nightStart" placeholder="开始 21:00" :disabled="!canEdit" style="width: 120px" />
                      <span>至</span>
                      <el-input v-model="settingsForm.nightEnd" placeholder="结束 09:00" :disabled="!canEdit" style="width: 120px" />
                    </div>
                  </el-form-item>
                </template>
                <template v-if="settingsForm.tripleShiftEnabled">
                  <el-form-item label="中班时段">
                    <div class="inline-pair">
                      <el-input v-model="settingsForm.middleStart" placeholder="开始 13:00" :disabled="!canEdit" style="width: 120px" />
                      <span>至</span>
                      <el-input v-model="settingsForm.middleEnd" placeholder="结束 01:00" :disabled="!canEdit" style="width: 120px" />
                    </div>
                    <span class="field-hint">可跨午夜，默认 13:00–次日 01:00</span>
                  </el-form-item>
                </template>
                <el-form-item label="不限打卡时段">
                  <el-switch v-model="settingsForm.shiftWindowDisabled" :disabled="!canEdit" />
                  <span class="field-hint">开启后各班次按钮任意时间可用</span>
                </el-form-item>
                <el-divider content-position="left">日终重置</el-divider>
                <el-form-item label="重置基准时间">
                  <el-input v-model="settingsForm.dailyResetTime" placeholder="09:00" :disabled="!canEdit" style="width: 120px" />
                </el-form-item>
                <el-form-item label="执行延迟（分钟）">
                  <el-input-number
                    v-model="settingsForm.dailyResetExecutionOffsetMinutes"
                    :min="0"
                    :max="1440"
                    :disabled="!canEdit"
                  />
                  <span class="field-hint">
                    实际执行 {{ dailyResetExecutionPreview }}；到点自动执行，仅处理昨日数据
                  </span>
                </el-form-item>
                <el-form-item label="自动日重置">
                  <el-switch v-model="settingsForm.dailyResetEnabled" :disabled="!canEdit" />
                  <span class="field-hint">到点按群导出昨日明细、补全昨日未下班、清理昨日缓存</span>
                </el-form-item>
              </el-collapse-item>

              <el-collapse-item name="grace" class="panel-half">
                <template #title>
                  <span class="tg-panel-title"><span class="tg-panel-num">04</span>打卡宽容时间</span>
                </template>
                <p class="section-desc">打卡时间窗口决定何时允许打上班/下班卡；迟到/早退宽限决定超出标准时间后是否计异常并可能罚款。</p>
                <el-divider content-position="left">打卡时间窗口</el-divider>
                <el-form-item label="上班卡 · 可提前">
                  <el-input-number v-model="settingsForm.checkInGraceBefore" :min="0" :max="480" :disabled="!canEdit" />
                  <span class="field-hint">分钟（默认 120）</span>
                </el-form-item>
                <el-form-item label="上班卡 · 可延迟">
                  <el-input-number v-model="settingsForm.checkInGraceAfter" :min="0" :max="480" :disabled="!canEdit" />
                  <span class="field-hint">分钟（默认 360）</span>
                </el-form-item>
                <el-form-item label="下班卡 · 可提前">
                  <el-input-number v-model="settingsForm.checkOutGraceBefore" :min="0" :max="480" :disabled="!canEdit" />
                  <span class="field-hint">分钟（默认 120）</span>
                </el-form-item>
                <el-form-item label="下班卡 · 可延迟">
                  <el-input-number v-model="settingsForm.checkOutGraceAfter" :min="0" :max="480" :disabled="!canEdit" />
                  <span class="field-hint">分钟（默认 360）</span>
                </el-form-item>
                <el-divider content-position="left">考勤宽限（不计迟到/早退）</el-divider>
                <el-form-item label="上班 · 允许迟到">
                  <el-input-number v-model="settingsForm.lateToleranceMinutes" :min="0" :max="480" :disabled="!canEdit" />
                  <span class="field-hint">分钟（默认 360）</span>
                </el-form-item>
                <el-form-item label="下班 · 允许早退">
                  <el-input-number v-model="settingsForm.earlyLeaveToleranceMinutes" :min="0" :max="480" :disabled="!canEdit" />
                  <span class="field-hint">分钟（默认 10）</span>
                </el-form-item>
                <el-divider content-position="left">Telegram 键盘按钮</el-divider>
                <el-form-item label="底部信息按钮">
                  <el-switch v-model="settingsForm.showKeyboardInfoButtons" :disabled="!canEdit" />
                  <span class="field-hint">默认关闭。打开后显示「我的记录 / 排行榜 / 导出数据」</span>
                </el-form-item>
              </el-collapse-item>
            </el-collapse>

            <el-form-item v-if="canEdit" class="save-all-row">
              <el-button v-permission="'telegram:edit'" type="primary" size="large" :loading="savingSettings" @click="saveSettings">
                保存全部配置
              </el-button>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <el-tab-pane name="groups">
          <template #label>
            <span class="tg-tab-label"><el-icon><ChatDotRound /></el-icon>群组绑定</span>
          </template>
          <div class="toolbar tg-toolbar">
            <el-button v-if="canEdit" type="success" @click="discoverGroups" :loading="discoveringGroups">发现群组</el-button>
            <el-button type="primary" v-if="canEdit" @click="openGroupDialog()">手动添加</el-button>
            <el-button @click="loadGroups" :loading="loadingGroups">刷新</el-button>
          </div>
          <el-alert type="info" show-icon :closable="false" class="tg-tip-alert">
            <template #title>打卡交互群</template>
            <template #default>
              绑定后，用户在此群内点击<strong>上班 / 下班 / 活动 / 回座</strong>，或从<strong>PC 客户端</strong>打卡，机器人/系统都会在此群回复。
              客户端与 Telegram 共用同一套考勤会话，数据互通。迟到、活动超时等异常另推送到「换班与推送」配置的频道/通知群。
            </template>
          </el-alert>
          <el-table :data="groups" v-loading="loadingGroups" stripe class="tg-table">
            <el-table-column prop="chatId" label="Chat ID" min-width="140" />
            <el-table-column prop="chatTitle" label="群名称" min-width="160" />
            <el-table-column label="上班窗口" width="110">
              <template #default="{ row }">
                {{ formatGroupGrace(row.shiftGraceBefore, row.shiftGraceAfter) }}
              </template>
            </el-table-column>
            <el-table-column label="下班窗口" width="110">
              <template #default="{ row }">
                {{ formatGroupGrace(row.checkOutGraceBefore ?? row.workEndGraceBefore, row.checkOutGraceAfter ?? row.workEndGraceAfter) }}
              </template>
            </el-table-column>
            <el-table-column label="迟到/早退" width="110">
              <template #default="{ row }">
                {{ formatToleranceGrace(row.lateToleranceMinutes, row.earlyLeaveToleranceMinutes) }}
              </template>
            </el-table-column>
            <el-table-column label="日重置" width="80">
              <template #default="{ row }">{{ row.dailyResetTime || "全局" }}</template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'" size="small">
                  {{ row.enabled ? "启用" : "停用" }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="140" v-if="canEdit">
              <template #default="{ row }">
                <el-button link type="primary" @click="openGroupDialog(row)">编辑</el-button>
                <el-button link type="danger" @click="removeGroup(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>



        

        <el-tab-pane name="activities">
          <template #label>
            <span class="tg-tab-label"><el-icon><Timer /></el-icon>活动与罚款</span>
          </template>

          <div class="activity-section">
            <div class="activity-section-head">
              <div>
                <h4 class="sub-block-title">活动类型</h4>
                <p class="section-desc inline-desc">员工在 TG 群发送活动名称开始计时，超时可能触发罚款。</p>
              </div>
              <div class="toolbar tg-toolbar compact-toolbar">
                <el-button type="primary" v-if="canEdit" @click="openActivityDialog()">新增活动</el-button>
                <el-button @click="loadActivityTypes" :loading="loadingActivities">刷新</el-button>
              </div>
            </div>

            <div v-loading="loadingActivities" class="activity-card-grid">
              <div
                v-for="act in activityTypes"
                :key="act.code"
                class="activity-card"
                :class="{ 'is-disabled': !act.enabled }"
              >
                <div class="activity-card-header">
                  <div class="activity-card-icon">
                    <el-icon><CoffeeCup /></el-icon>
                  </div>
                  <div class="activity-card-title">
                    <h5>{{ act.name }}</h5>
                    <code>{{ act.code }}</code>
                  </div>
                  <el-tag :type="act.enabled ? 'success' : 'info'" size="small" effect="light">
                    {{ act.enabled ? "启用" : "停用" }}
                  </el-tag>
                </div>
                <div class="activity-card-stats">
                  <div class="activity-stat">
                    <span>时限</span>
                    <strong>{{ formatSeconds(act.timeLimitSeconds) }}</strong>
                  </div>
                  <div class="activity-stat">
                    <span>日上限</span>
                    <strong>{{ act.maxTimesPerDay || "不限" }}</strong>
                  </div>
                  <div class="activity-stat">
                    <span>群并发</span>
                    <strong>{{ act.maxConcurrent || "不限" }}</strong>
                  </div>
                </div>
                <div v-if="canEdit" class="activity-card-actions">
                  <el-button link type="primary" @click="openActivityDialog(act)">编辑</el-button>
                  <el-button link type="danger" @click="removeActivityType(act)">删除</el-button>
                </div>
              </div>
              <div v-if="!loadingActivities && !activityTypes.length" class="activity-empty">
                暂无活动类型，点击「新增活动」创建
              </div>
            </div>
          </div>

          <div class="activity-fine-section">
            <div class="activity-section-head">
              <div>
                <h4 class="sub-block-title">超时罚款规则</h4>
                <p class="section-desc inline-desc">超时落在某档位内收取对应罚款；超出最大档按最大档计。金额计算不变，仅切换显示币种。</p>
              </div>
              <div class="toolbar tg-toolbar compact-toolbar">
                <el-select
                  v-model="settingsForm.fineCurrency"
                  :disabled="!canEdit"
                  style="width: 168px"
                  @change="saveFineCurrency"
                >
                  <el-option label="人民币 ¥" value="CNY" />
                  <el-option label="泰铢 ฿" value="THB" />
                  <el-option label="美元 $" value="USD" />
                </el-select>
                <el-select
                  v-model="fineFilterCode"
                  clearable
                  placeholder="筛选活动"
                  class="activity-fine-filter"
                  @change="loadActivityFines"
                >
                  <el-option v-for="a in activityTypes" :key="a.code" :label="a.name" :value="a.code" />
                </el-select>
                <el-button type="primary" v-if="canEdit" @click="openFineDialog()">新增规则</el-button>
                <el-button v-if="canEdit" @click="openFineBatchDialog()">批量新增</el-button>
                <el-button @click="loadActivityFines" :loading="loadingFines">刷新</el-button>
              </div>
            </div>

            <div v-loading="loadingFines" class="activity-fine-grid">
              <div
                v-for="group in activityFineGroups"
                :key="group.activity.code"
                class="activity-fine-group-card"
              >
                <div class="activity-fine-group-header">
                  <div class="activity-fine-group-icon">
                    <el-icon><Warning /></el-icon>
                  </div>
                  <div>
                    <h5>{{ group.activity.name }}</h5>
                    <span>{{ group.fines.length }} 条规则 · {{ group.activity.code }}</span>
                  </div>
                </div>
                <div class="work-fine-cards">
                  <div
                    v-for="fine in group.fines"
                    :key="fine.id || `${fine.activityCode}-${fine.segmentMinutes}`"
                    class="work-fine-card activity-fine-item"
                  >
                    <div class="work-fine-card-main">
                      <span class="work-fine-tier">≤ {{ fine.segmentMinutes }} 分钟</span>
                      <span class="work-fine-amount activity-amount">{{ formatFineAmount(fine.fineAmount, settingsForm.fineCurrency) }}</span>
                    </div>
                    <el-button
                      v-if="canEdit"
                      link
                      type="danger"
                      class="work-fine-delete"
                      @click="removeActivityFine(fine)"
                    >
                      删除
                    </el-button>
                  </div>
                  <div v-if="!group.fines.length" class="work-fine-empty">暂无超时罚款规则</div>
                </div>
              </div>
              <div v-if="!loadingFines && !activityFineGroups.length" class="activity-empty wide">
                {{ fineFilterCode ? "该活动暂无罚款规则" : "请先创建活动类型，再添加罚款规则" }}
              </div>
            </div>
          </div>

          <div class="work-fine-section">
            <div class="work-fine-section-head">
              <div>
                <h4 class="sub-block-title">上下班罚款</h4>
                <p class="section-desc inline-desc">
                  迟到/早退落在对应档位内收取罚款；超出最大档按最大档计。币种：{{ fineCurrencySymbol(settingsForm.fineCurrency) }}
                  （与上方「超时罚款」共用）
                </p>
              </div>
              <div class="toolbar tg-toolbar compact-toolbar">
                <el-button type="primary" v-if="canEdit" @click="openWorkFineDialog()">新增规则</el-button>
                <el-button v-if="canEdit" @click="openWorkFineBatchDialog()">批量新增</el-button>
                <el-button @click="loadWorkFines" :loading="loadingWorkFines">刷新</el-button>
              </div>
            </div>

            <div v-loading="loadingWorkFines" class="work-fine-columns">
              <div class="work-fine-column checkin">
                <div class="work-fine-column-header">
                  <div class="work-fine-column-icon">
                    <el-icon><Sunrise /></el-icon>
                  </div>
                  <div>
                    <h5>上班迟到</h5>
                    <span>{{ workStartFines.length }} 条规则</span>
                  </div>
                </div>
                <div class="work-fine-cards">
                  <div
                    v-for="row in workStartFines"
                    :key="row.id || `${row.checkinType}-${row.segmentMinutes}`"
                    class="work-fine-card"
                  >
                    <div class="work-fine-card-main">
                      <span class="work-fine-tier">≤ {{ row.segmentMinutes }} 分钟</span>
                      <span class="work-fine-amount">{{ formatFineAmount(row.fineAmount, settingsForm.fineCurrency) }}</span>
                    </div>
                    <el-button
                      v-if="canEdit"
                      link
                      type="danger"
                      class="work-fine-delete"
                      @click="removeWorkFine(row)"
                    >
                      删除
                    </el-button>
                  </div>
                  <div v-if="!workStartFines.length" class="work-fine-empty">暂无上班迟到罚款规则</div>
                </div>
              </div>

              <div class="work-fine-column checkout">
                <div class="work-fine-column-header">
                  <div class="work-fine-column-icon">
                    <el-icon><Sunset /></el-icon>
                  </div>
                  <div>
                    <h5>下班早退</h5>
                    <span>{{ workEndFines.length }} 条规则</span>
                  </div>
                </div>
                <div class="work-fine-cards">
                  <div
                    v-for="row in workEndFines"
                    :key="row.id || `${row.checkinType}-${row.segmentMinutes}`"
                    class="work-fine-card"
                  >
                    <div class="work-fine-card-main">
                      <span class="work-fine-tier">≤ {{ row.segmentMinutes }} 分钟</span>
                      <span class="work-fine-amount">{{ formatFineAmount(row.fineAmount, settingsForm.fineCurrency) }}</span>
                    </div>
                    <el-button
                      v-if="canEdit"
                      link
                      type="danger"
                      class="work-fine-delete"
                      @click="removeWorkFine(row)"
                    >
                      删除
                    </el-button>
                  </div>
                  <div v-if="!workEndFines.length" class="work-fine-empty">暂无下班早退罚款规则</div>
                </div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <el-tab-pane name="handover">
          <template #label>
            <span class="tg-tab-label"><el-icon><Bell /></el-icon>换班与推送</span>
          </template>

          <div class="activity-section handover-picker-section">
            <div class="activity-section-head">
              <div>
                <h4 class="sub-block-title">关联打卡群</h4>
                <p class="section-desc inline-desc">
                  为「群组绑定」中的打卡群配置换班规则与<strong>异常推送</strong>（迟到 / 早退 / 活动超时）。
                  正常打卡与活动回座由机器人在打卡群内直接回复，不会推送到下方频道/通知群。
                </p>
              </div>
            </div>
            <div class="handover-picker-row">
              <el-select
                v-model="handoverChatId"
                filterable
                clearable
                placeholder="从已绑定群组选择"
                class="handover-group-select"
                @change="onHandoverGroupSelected"
              >
                <el-option
                  v-for="g in groups"
                  :key="g.chatId"
                  :label="groupOptionLabel(g)"
                  :value="g.chatId"
                />
              </el-select>
              <el-button @click="loadGroups" :loading="loadingGroups">刷新列表</el-button>
              <el-button v-if="canEdit" type="success" @click="discoverGroups" :loading="discoveringGroups">
                发现群组
              </el-button>
            </div>
            <div v-if="handoverChatId" class="handover-selected-banner">
              <el-icon><ChatDotRound /></el-icon>
              <span>当前打卡群：<strong>{{ groupOptionLabel(selectedHandoverGroup) }}</strong></span>
            </div>
            <p v-if="!groups.length && !loadingGroups" class="field-hint block-hint">
              暂无已绑定群，请前往「群组绑定」或使用「发现群组」
            </p>
          </div>

          <div v-if="handoverChatId" class="handover-config-grid">
            <div class="activity-section handover-panel" v-loading="loadingHandoverCfg">
              <div class="activity-section-head">
                <div class="panel-title-with-icon">
                  <div class="panel-icon-badge calendar"><el-icon><Calendar /></el-icon></div>
                  <div>
                    <h4 class="sub-block-title">换班配置</h4>
                    <p class="section-desc inline-desc">换班日、班次时间与工时阈值</p>
                  </div>
                </div>
                <el-button
                  v-if="canEdit && handoverCfg.chatId"
                  type="primary"
                  :loading="savingHandoverCfg"
                  @click="saveHandoverConfig"
                >
                  保存换班配置
                </el-button>
              </div>

              <template v-if="handoverCfg.chatId">
                <div class="config-field-grid">
                  <div class="config-field-card switch-card" :class="{ 'is-on': handoverCfg.handoverEnabled }">
                    <label>启用换班日</label>
                    <el-switch v-model="handoverCfg.handoverEnabled" :disabled="!canEdit" />
                  </div>
                  <div class="config-field-card">
                    <label>换班日</label>
                    <el-input-number v-model="handoverCfg.handoverDay" :min="0" :max="31" :disabled="!canEdit" controls-position="right" />
                    <span class="config-hint">0 = 每月最后一天</span>
                  </div>
                  <div class="config-field-card">
                    <label>指定月份</label>
                    <el-input-number v-model="handoverCfg.handoverMonth" :min="0" :max="12" :disabled="!canEdit" controls-position="right" />
                    <span class="config-hint">0 = 每月；1–12 = 仅该月</span>
                  </div>
                  <div class="config-field-card">
                    <label>换班白班开始</label>
                    <el-input v-model="handoverCfg.handoverDayStartTime" :disabled="!canEdit" placeholder="09:00" />
                  </div>
                  <div class="config-field-card">
                    <label>夜班开始</label>
                    <el-input v-model="handoverCfg.nightStartTime" :disabled="!canEdit" placeholder="21:00" />
                  </div>
                  <div class="config-field-card">
                    <label>白班开始</label>
                    <el-input v-model="handoverCfg.dayStartTime" :disabled="!canEdit" placeholder="09:00" />
                  </div>
                  <div class="config-field-card">
                    <label>换班夜班时长 (h)</label>
                    <el-input-number v-model="handoverCfg.handoverNightHours" :min="1" :max="24" :disabled="!canEdit" controls-position="right" />
                  </div>
                  <div class="config-field-card">
                    <label>换班白班时长 (h)</label>
                    <el-input-number v-model="handoverCfg.handoverDayHours" :min="1" :max="24" :disabled="!canEdit" controls-position="right" />
                  </div>
                  <div class="config-field-card">
                    <label>正常夜班时长 (h)</label>
                    <el-input-number v-model="handoverCfg.normalNightHours" :min="1" :max="24" :disabled="!canEdit" controls-position="right" />
                  </div>
                  <div class="config-field-card">
                    <label>正常白班时长 (h)</label>
                    <el-input-number v-model="handoverCfg.normalDayHours" :min="1" :max="24" :disabled="!canEdit" controls-position="right" />
                  </div>
                  <div class="config-field-card span-2">
                    <label>周期 2 阈值 (小时)</label>
                    <el-input-number v-model="handoverCfg.handoverResetThresholdHours" :min="1" :max="24" :step="0.5" :disabled="!canEdit" controls-position="right" />
                    <span class="config-hint">累计工时达此值后活动次数进入第 2 段</span>
                  </div>
                </div>
              </template>
              <div v-else-if="!loadingHandoverCfg" class="panel-empty">加载换班配置中或该群暂无配置</div>
            </div>

            <div class="activity-section push-panel" v-loading="loadingPushCfg">
              <div class="activity-section-head">
                <div class="panel-title-with-icon">
                  <div class="panel-icon-badge push"><el-icon><Promotion /></el-icon></div>
                  <div>
                    <h4 class="sub-block-title">推送通道</h4>
                    <p class="section-desc inline-desc">额外推送：活动已超时、上下班迟到/早退（不含「即将超时」预警）</p>
                  </div>
                </div>
                <div class="compact-toolbar toolbar tg-toolbar">
                  <el-button v-if="handoverChatId" link type="primary" @click="loadPushSettings">重新加载</el-button>
                  <el-button
 v-permission="'telegram:notify'"                    v-if="canEdit && pushCfg.chatId"
                    :loading="testingPushCfg"
                    @click="testPushSettings"
                  >
                    测试推送
                  </el-button>
                  <el-button
 v-permission="'telegram:notify'"                    v-if="canEdit && pushCfg.chatId"
                    type="primary"
                    :loading="savingPushCfg"
                    @click="savePushSettings"
                  >
                    保存推送配置
                  </el-button>
                </div>
              </div>

              <template v-if="pushCfg.chatId">
                <el-alert
                  type="info"
                  :closable="false"
                  show-icon
                  class="push-id-hint"
                />
                <div class="push-target-cards">
                  <div class="push-target-card">
                    <div class="push-target-label">① 频道 ID</div>
                    <p class="field-hint block-hint">Telegram 频道；推送活动已超时、迟到/早退等异常</p>
                    <div class="target-id-row">
                      <el-input v-model="pushCfg.channelId" :disabled="!canEdit" placeholder="手动输入 @channel 或 -100..." />
                      <el-button v-permission="'telegram:notify'" v-if="canEdit" plain @click="openPushTargetDiscover('channelId')">
                        <el-icon><Search /></el-icon>
                        发现绑定
                      </el-button>
                    </div>
                  </div>
                  <div class="push-target-card">
                    <div class="push-target-label">② 通知群 ID</div>
                    <p class="field-hint block-hint">Telegram 群组；推送活动已超时、强制回座等（不含即将超时）</p>
                    <div class="target-id-row">
                      <el-input v-model="pushCfg.notificationGroupId" :disabled="!canEdit" placeholder="手动输入 -100..." />
                      <el-button v-permission="'telegram:notify'" v-if="canEdit" plain @click="openPushTargetDiscover('notificationGroupId')">
                        <el-icon><Search /></el-icon>
                        发现绑定
                      </el-button>
                    </div>
                  </div>
                  <div class="push-target-card">
                    <div class="push-target-label">③ 迟到/早退通知群（可选）</div>
                    <p class="field-hint block-hint">仅额外接收上下班迟到、早退、罚款；留空则迟到/早退只走上方频道+通知群</p>
                    <div class="target-id-row">
                      <el-input v-model="pushCfg.extraWorkGroupId" :disabled="!canEdit" placeholder="手动输入 -100..." />
                      <el-button v-permission="'telegram:notify'" v-if="canEdit" plain @click="openPushTargetDiscover('extraWorkGroupId')">
                        <el-icon><Search /></el-icon>
                        发现绑定
                      </el-button>
                    </div>
                  </div>
                </div>

                <div class="push-toggle-grid">
                  <div class="feature-card compact-feature" :class="{ 'is-on': pushCfg.enableChannelPush }">
                    <div class="feature-card-top">
                      <span class="feature-title">推送至频道</span>
                      <el-switch v-model="pushCfg.enableChannelPush" :disabled="!canEdit" />
                    </div>
                  </div>
                  <div class="feature-card compact-feature" :class="{ 'is-on': pushCfg.enableGroupPush }">
                    <div class="feature-card-top">
                      <span class="feature-title">推送至通知群</span>
                      <el-switch v-model="pushCfg.enableGroupPush" :disabled="!canEdit" />
                    </div>
                  </div>
                  <div class="feature-card compact-feature" :class="{ 'is-on': pushCfg.enableAdminPush }">
                    <div class="feature-card-top">
                      <span class="feature-title">管理员兜底推送</span>
                      <el-switch v-model="pushCfg.enableAdminPush" :disabled="!canEdit" />
                    </div>
                    <p class="feature-desc">频道与通知群均失败时，私信管理员 TG 账号（非下方三个 ID 字段）</p>
                  </div>
                </div>
              </template>
              <div v-else-if="!handoverChatId" class="panel-empty">请先在上方选择打卡群</div>
              <div v-else-if="!loadingPushCfg" class="panel-empty">该群暂无推送配置，保存后将自动创建</div>
            </div>
          </div>

          <div v-else class="handover-empty-state">
            <el-empty description="请选择打卡群以配置换班与推送" :image-size="80" />
          </div>
        </el-tab-pane>

        <el-tab-pane name="export">
          <template #label>
            <span class="tg-tab-label"><el-icon><Download /></el-icon>导出与日重置</span>
          </template>
          <div class="export-panel">
            <el-alert
              type="info"
              show-icon
              :closable="false"
              class="tg-tip-alert"
              title="数据导出说明"
            >
              <template #default>
                <p>管理端导出为<strong>全库</strong> Telegram 考勤 XLSX；各群 Bot 内 <code>/export</code> 仅导出该群。</p>
                <p>自动日重置推送的文件标题含<strong>统计日期</strong>（一般为昨日业务日）；重置后同一天明细会被清理，请保存群文件；首页概览可下载最近 7 天的日重置备份。</p>
                <p>手动日重置可勾选「导出当前业务日」，用于当天中午补导今日打卡数据。</p>
              </template>
            </el-alert>
            <h4>导出 XLSX</h4>
            <div class="toolbar tg-toolbar">
              <el-date-picker
                v-model="exportDateFrom"
                type="date"
                placeholder="开始日期"
                value-format="YYYY-MM-DD"
                style="width: 160px"
              />
              <el-date-picker
                v-model="exportDateTo"
                type="date"
                placeholder="结束日期"
                value-format="YYYY-MM-DD"
                style="width: 160px"
              />
              <el-button v-permission="'records:export'" type="primary" :loading="exporting" @click="exportDaily">导出所选日期</el-button>
            </div>
            <div class="toolbar tg-toolbar">
              <el-date-picker
                v-model="exportMonth"
                type="month"
                placeholder="选择月份"
                value-format="YYYY-MM"
                style="width: 160px"
              />
              <el-button v-permission="'records:export'" type="success" :loading="exporting" @click="exportMonthly">导出整月</el-button>
            </div>
            <div class="export-reset-block">
              <h4>日重置</h4>
              <p class="field-hint block-hint">
                自动日重置在「基础配置 → 日终重置」中设置；下方为管理员手动触发（按群执行，同一天每群仅一次）。
                默认导出<strong>上一统计日</strong>；若需导出<strong>今日业务日</strong>请勾选下方选项。
              </p>
              <el-checkbox v-model="exportBusinessToday" class="reset-export-today">
                导出当前业务日（手动重置时）
              </el-checkbox>
              <el-button v-permission="'telegram:reset'" v-if="canEdit" type="warning" :loading="runningReset" @click="triggerDailyReset">
                立即执行日重置
              </el-button>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <Transition name="tg-save-slide">
      <div v-if="canEdit && activeTab === 'settings'" class="tg-save-bar">
        <span class="tg-save-hint">修改后请保存；更换 Token 或 Secret 后，请到「界面与权限」卡片更新 Webhook。</span>
        <el-button v-permission="'telegram:edit'" type="primary" size="large" :loading="savingSettings" @click="saveSettings">
          保存全部配置
        </el-button>
      </div>
    </Transition>

    <el-dialog v-model="groupVisible" :title="groupForm._edit ? '编辑 Telegram 群' : '添加 Telegram 群'" width="520px" class="tg-dialog">
      <el-form label-width="120px">
        <el-form-item label="Chat ID" required>
          <el-input v-model="groupForm.chatId" placeholder="例如 -1001234567890" :disabled="!!groupForm._edit" />
        </el-form-item>
        <el-form-item label="群名称">
          <el-input v-model="groupForm.chatTitle" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="groupForm.enabled" />
        </el-form-item>
        <el-divider content-position="left">本群单独设置（留空则继承全局）</el-divider>
        <el-form-item label="上班卡窗口">
          <div class="inline-pair">
            <el-input-number v-model="groupForm.shiftGraceBefore" :min="0" :max="480" placeholder="可提前" />
            <el-input-number v-model="groupForm.shiftGraceAfter" :min="0" :max="480" placeholder="可延迟" />
          </div>
        </el-form-item>
        <el-form-item label="下班卡窗口">
          <div class="inline-pair">
            <el-input-number v-model="groupForm.checkOutGraceBefore" :min="0" :max="480" placeholder="可提前" />
            <el-input-number v-model="groupForm.checkOutGraceAfter" :min="0" :max="480" placeholder="可延迟" />
          </div>
        </el-form-item>
        <el-form-item label="迟到/早退宽限">
          <div class="inline-pair">
            <el-input-number v-model="groupForm.lateToleranceMinutes" :min="0" :max="480" placeholder="允许迟到" />
            <el-input-number v-model="groupForm.earlyLeaveToleranceMinutes" :min="0" :max="480" placeholder="允许早退" />
          </div>
        </el-form-item>
        <el-form-item label="日重置时间">
          <el-input v-model="groupForm.dailyResetTime" placeholder="09:00，留空用全局" style="width: 120px" />
        </el-form-item>
        <el-form-item label="界面语言">
          <el-select v-model="groupForm.botUiLang" clearable placeholder="继承全局" style="width: 200px">
            <el-option label="中越双语" value="both" />
            <el-option label="仅中文" value="zh" />
            <el-option label="仅越南语" value="vi" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="groupVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingGroup" @click="saveGroup">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="discoverVisible" title="发现群组" width="640px">
      <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px">
        <template #title>将 Bot 拉入群即可（入群事件会自动记录）；若列表为空可在群内发一条消息后重试</template>
      </el-alert>
      <el-table :data="discoveredGroups" stripe border max-height="360" v-loading="discoveringGroups">
        <el-table-column prop="groupName" label="群名称" min-width="160" />
        <el-table-column prop="chatId" label="Chat ID" min-width="140" />
        <el-table-column label="类型" width="100">
          <template #default="{ row }">
            <el-tag size="small" effect="light">
              {{ row.chatType === "supergroup" ? "超级群" : row.chatType === "channel" ? "频道" : row.chatType === "group" ? "群组" : row.chatType || "-" }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120">
          <template #default="{ row }">
            <el-tag v-if="row.bound" type="success" size="small">已绑定</el-tag>
            <el-button v-else link type="primary" @click="bindDiscoveredGroup(row)">绑定</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!discoveringGroups && !discoveredGroups.length" description="暂未发现群组" />
      <template #footer>
        <el-button @click="discoverVisible = false">关闭</el-button>
        <el-button type="primary" :loading="discoveringGroups" @click="discoverGroups">重新扫描</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="pushTargetDiscoverVisible"
      :title="pushTargetDiscoverTitle"
      width="640px"
      class="modern-dialog"
    >
      <el-alert
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 12px"
          title="将 Bot 拉入目标频道/群即可发现（勿选打卡群）。若列表为空，可发一条消息后重试。"
      />
      <el-table
        :data="pushDiscoverTargets"
        stripe
        border
        max-height="360"
        v-loading="pushTargetDiscovering"
      >
        <el-table-column label="名称" min-width="180">
          <template #default="{ row }">
            {{ row.chatTitle || row.chatId }}
          </template>
        </el-table-column>
        <el-table-column prop="chatId" label="Chat ID" min-width="150" />
        <el-table-column label="类型" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="light">{{ formatPushChatType(row.chatType) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" align="center">
          <template #default="{ row }">
            <el-button link type="primary" @click="selectPushTarget(row)">选择</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty
        v-if="!pushTargetDiscovering && !pushDiscoverTargets.length"
        description="未发现频道或群组"
        :image-size="64"
      />
      <template #footer>
        <el-button v-permission="'telegram:notify'" @click="pushTargetDiscoverVisible = false">关闭</el-button>
        <el-button v-permission="'telegram:notify'" type="primary" :loading="pushTargetDiscovering" @click="refreshPushTargetDiscover">
          重新发现
        </el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="bindingVisible" title="绑定员工 Telegram" width="520px">
      <el-alert type="warning" :closable="false" show-icon style="margin-bottom: 16px">
        推荐让员工自行绑定：客户端托盘「绑定 Telegram」，或私聊 Bot 发送 <code>/bind 邀请码</code>。此处为管理员代绑。
      </el-alert>
      <el-form label-width="120px">
        <el-form-item label="员工" required>
          <el-select
            v-model="bindingForm.employeeId"
            filterable
            placeholder="选择员工"
            style="width: 100%"
          >
            <el-option
              v-for="emp in employees"
              :key="emp.id"
              :label="`${emp.name}${emp.employee_id ? ` (${emp.employee_id})` : ''}`"
              :value="emp.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="Telegram User ID" required>
          <el-input v-model="bindingForm.telegramUserId" placeholder="纯数字；自助绑定时无需填写" />
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="bindingForm.telegramUsername" placeholder="不含 @" />
        </el-form-item>
        <el-form-item label="Chat ID">
          <el-input v-model="bindingForm.chatId" placeholder="可选，打卡群 ID" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="bindingVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingBinding" @click="saveBinding">绑定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="activityVisible" :title="activityForm.code ? '编辑活动' : '新增活动'" width="480px">
      <el-form label-width="110px">
        <el-form-item label="代码" required>
          <el-input v-model="activityForm.code" :disabled="!!activityForm._edit" placeholder="如 wc_small" />
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="activityForm.name" placeholder="如 小厕" />
        </el-form-item>
        <el-form-item label="时限(秒)">
          <el-input-number v-model="activityForm.timeLimitSeconds" :min="30" :max="7200" />
        </el-form-item>
        <el-form-item label="日次数上限">
          <el-input-number v-model="activityForm.maxTimesPerDay" :min="0" :max="99" />
          <span class="field-hint">0 表示不限</span>
        </el-form-item>
        <el-form-item label="群并发上限">
          <el-input-number v-model="activityForm.maxConcurrent" :min="0" :max="99" />
          <span class="field-hint">同群同时进行中人数，0 表示不限</span>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="activityForm.sortOrder" :min="0" :max="99" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="activityForm.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="activityVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingActivity" @click="saveActivityType">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="fineVisible" title="超时罚款规则" width="460px">
      <el-form label-width="100px">
        <el-form-item label="活动" required>
          <el-select v-model="fineForm.activityCode" style="width: 100%">
            <el-option v-for="a in activityTypes" :key="a.code" :label="a.name" :value="a.code" />
          </el-select>
        </el-form-item>
        <el-form-item label="超时档位" required>
          <el-input-number v-model="fineForm.segmentMinutes" :min="1" :max="120" />
          <span class="field-hint">分钟</span>
        </el-form-item>
        <el-form-item :label="`罚款金额 (${fineCurrencySymbol(settingsForm.fineCurrency)})`" required>
          <el-input-number v-model="fineForm.fineAmount" :min="0" :precision="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="fineVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingFine" @click="saveActivityFine">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="fineBatchVisible" title="批量新增超时罚款规则" width="560px">
      <el-form label-width="88px">
        <el-form-item label="活动" required>
          <div class="fine-batch-select-wrap">
            <el-select
              v-model="fineBatchForm.activityCodes"
              multiple
              filterable
              collapse-tags
              collapse-tags-tooltip
              placeholder="可多选活动，统一套用下方档位"
              style="width: 100%"
            >
              <el-option v-for="a in activityTypes" :key="a.code" :label="a.name" :value="a.code" />
            </el-select>
            <div class="fine-batch-select-actions">
              <el-button link type="primary" @click="selectAllFineBatchActivities">全选活动</el-button>
              <el-button link @click="fineBatchForm.activityCodes = []">清空</el-button>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="罚款档位">
          <div class="fine-batch-rows">
            <div
              v-for="(row, index) in fineBatchForm.items"
              :key="index"
              class="fine-batch-row"
            >
              <span class="fine-batch-label">≤</span>
              <el-input-number
                v-model="row.segmentMinutes"
                :min="1"
                :max="120"
                controls-position="right"
              />
              <span class="fine-batch-label">分钟，罚款</span>
              <el-input-number
                v-model="row.fineAmount"
                :min="0"
                :precision="2"
                controls-position="right"
              />
              <span class="fine-batch-label">元</span>
              <el-button
                link
                type="danger"
                :disabled="fineBatchForm.items.length <= 1"
                @click="removeFineBatchRow(index)"
              >
                删除
              </el-button>
            </div>
            <el-button link type="primary" @click="addFineBatchRow">+ 添加一行</el-button>
          </div>
          <p class="field-hint">所选活动将共用同一套超时档位；相同档位会更新罚款金额。</p>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="fineBatchVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingFineBatch" @click="saveActivityFineBatch">
          批量保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="workFineVisible" title="上下班罚款规则" width="460px">
      <el-form label-width="100px">
        <el-form-item label="类型" required>
          <el-select v-model="workFineForm.checkinType" style="width: 100%">
            <el-option label="上班迟到" value="work_start" />
            <el-option label="下班早退" value="work_end" />
          </el-select>
        </el-form-item>
        <el-form-item label="档位" required>
          <el-input-number v-model="workFineForm.segmentMinutes" :min="1" :max="120" />
        </el-form-item>
        <el-form-item :label="`罚款 (${fineCurrencySymbol(settingsForm.fineCurrency)})`" required>
          <el-input-number v-model="workFineForm.fineAmount" :min="0" :precision="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="workFineVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingWorkFine" @click="saveWorkFine">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="workFineBatchVisible" title="批量新增上下班罚款规则" width="560px">
      <el-form label-width="88px">
        <el-form-item label="类型" required>
          <div class="fine-batch-select-wrap">
            <el-select
              v-model="workFineBatchForm.checkinTypes"
              multiple
              collapse-tags
              collapse-tags-tooltip
              placeholder="可多选类型，统一套用下方档位"
              style="width: 100%"
            >
              <el-option label="上班迟到" value="work_start" />
              <el-option label="下班早退" value="work_end" />
            </el-select>
            <div class="fine-batch-select-actions">
              <el-button link type="primary" @click="selectAllWorkFineBatchTypes">全选类型</el-button>
              <el-button link @click="workFineBatchForm.checkinTypes = []">清空</el-button>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="罚款档位">
          <div class="fine-batch-rows">
            <div
              v-for="(row, index) in workFineBatchForm.items"
              :key="index"
              class="fine-batch-row"
            >
              <span class="fine-batch-label">≤</span>
              <el-input-number
                v-model="row.segmentMinutes"
                :min="1"
                :max="120"
                controls-position="right"
              />
              <span class="fine-batch-label">分钟，罚款</span>
              <el-input-number
                v-model="row.fineAmount"
                :min="0"
                :precision="2"
                controls-position="right"
              />
              <span class="fine-batch-label">元</span>
              <el-button
                link
                type="danger"
                :disabled="workFineBatchForm.items.length <= 1"
                @click="removeWorkFineBatchRow(index)"
              >
                删除
              </el-button>
            </div>
            <el-button link type="primary" @click="addWorkFineBatchRow">+ 添加一行</el-button>
          </div>
          <p class="field-hint">所选类型将共用同一套档位；相同档位会更新罚款金额。</p>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="workFineBatchVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" :loading="savingWorkFineBatch" @click="saveWorkFineBatch">
          批量保存
        </el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="sharedWsVisible" title="添加共享工位成员" width="520px" @open="loadBindableDevices">
      <el-form label-width="100px">
        <el-form-item label="设备" required>
          <el-select
            v-model="sharedWsForm.deviceId"
            filterable
            clearable
            placeholder="请选择已注册设备"
            style="width: 100%"
            :loading="loadingBindableDevices"
          >
            <el-option
              v-for="device in bindableDevices"
              :key="device.id"
              :label="formatSharedWsDeviceLabel(device)"
              :value="device.id"
            >
              <div class="shared-ws-device-option">
                <span class="shared-ws-device-option-main">{{ formatSharedWsDeviceLabel(device) }}</span>
                <span class="shared-ws-device-option-id">{{ device.id }}</span>
              </div>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="员工" required>
          <el-select v-model="sharedWsForm.employeeId" filterable style="width: 100%">
            <el-option v-for="e in employees" :key="e.id" :label="`${e.name} (${e.employeeId || e.id})`" :value="e.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="sharedWsVisible = false">取消</el-button>
        <el-button v-permission="'telegram:edit'" type="primary" @click="saveSharedWs">保存</el-button>
      </template>
    </el-dialog>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import {
  VideoCamera,
  ChatDotRound,
  Setting,
  Link,
  Timer,
  Bell,
  Download,
  User,
  Sunrise,
  Sunset,
  CoffeeCup,
  Warning,
  Calendar,
  Promotion,
  Search,
  Monitor,
} from "@element-plus/icons-vue";
import workforceTelegramApi from "@api/workforce_telegram_api";
import adminApi from "@api/admin_api";
import { metaApi, deviceApi } from "@api/index";
import { useRoute } from "vue-router";
import type { Device } from "@api/types";
import { useUserStore } from "@store/user";
import { formatDateTime } from "@api/format";
import {
  formatSeconds,
  formatGroupGrace,
  formatToleranceGrace,
  groupOptionLabel,
  formatPushChatType,
  fineCurrencySymbol,
  formatFineAmount,
  normalizeFineCurrency,
} from "./telegram_helpers";

const userStore = useUserStore();
const route = useRoute();
const canEdit = computed(() => userStore.hasPermission("telegram:edit"));

function addMinutesToClock(clock: string, minutes: number) {
  const match = /^(\d{1,2}):(\d{2})$/.exec((clock || "").trim());
  if (!match) return clock || "-";
  const total = ((parseInt(match[1], 10) * 60 + parseInt(match[2], 10) + minutes) % (24 * 60) + 24 * 60) % (24 * 60);
  const h = Math.floor(total / 60);
  const m = total % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

const dailyResetExecutionPreview = computed(() => {
  const base = settingsForm.value.dailyResetTime || "09:00";
  const offset = settingsForm.value.dailyResetExecutionOffsetMinutes ?? 120;
  return `${addMinutesToClock(base, offset)}（${base} + ${offset} 分钟）`;
});
const moduleEnabled = ref(false);
const captureLinkedEnvOverride = ref(false);
const activeTab = ref("settings");
const settingsPanels = ref(["bot", "ui", "shift", "grace"]);

const loadingSettings = ref(false);
const savingSettings = ref(false);
const regeneratingSecret = ref(false);
const regeneratingWebhookSecret = ref(false);
const registeringWebhook = ref(false);
const workerSecret = ref("");
const webhookSecret = ref("");
const generatingPairing = ref(false);
const pairingInfo = ref({ code: "", expiresAt: "" });
const pairingCommand = ref("");
const workerConn = ref<{ online?: boolean; lastSeenAt?: string; embedded?: boolean }>({});
const settings = ref<any>({});
const settingsForm = ref({
  enabled: false,
  botToken: "",
  timezone: "Asia/Shanghai",
  dayStart: "09:00",
  dayEnd: "21:00",
  checkInGraceBefore: 120,
  checkInGraceAfter: 360,
  checkOutGraceBefore: 120,
  checkOutGraceAfter: 360,
  lateToleranceMinutes: 0,
  earlyLeaveToleranceMinutes: 10,
  dualShiftEnabled: false,
  tripleShiftEnabled: false,
  nightStart: "21:00",
  nightEnd: "09:00",
  middleStart: "13:00",
  middleEnd: "01:00",
  dailyResetTime: "09:00",
  dailyResetExecutionOffsetMinutes: 120,
  dailyResetEnabled: true,
  shiftWindowDisabled: false,
  requirePCLogin: false,
  captureLinkedToCheckin: false,
  webPunchOnly: false,
  adminTelegramUserIds: "",
  botUiLang: "both",
  fineCurrency: "CNY" as "CNY" | "THB" | "USD",
  botMode: "webhook",
  webhookUrl: "",
  showKeyboardInfoButtons: false,
});

const groups = ref<any[]>([]);
const loadingGroups = ref(false);
const discoveringGroups = ref(false);
const discoverVisible = ref(false);
const discoveredGroups = ref<any[]>([]);
const groupVisible = ref(false);
const savingGroup = ref(false);
const groupForm = ref({
  chatId: "",
  chatTitle: "",
  enabled: true,
  shiftGraceBefore: undefined as number | undefined,
  shiftGraceAfter: undefined as number | undefined,
  checkOutGraceBefore: undefined as number | undefined,
  checkOutGraceAfter: undefined as number | undefined,
  lateToleranceMinutes: undefined as number | undefined,
  earlyLeaveToleranceMinutes: undefined as number | undefined,
  dailyResetTime: "",
  botUiLang: "",
  _edit: false as boolean,
});

const bindings = ref<any[]>([]);
const loadingBindings = ref(false);
const bindingSearch = ref("");
const bindingVisible = ref(false);
const savingBinding = ref(false);
const bindingForm = ref({
  employeeId: "",
  telegramUserId: "",
  telegramUsername: "",
  chatId: "",
});
const employees = ref<any[]>([]);

const activityTypes = ref<any[]>([]);
const loadingActivities = ref(false);
const activityVisible = ref(false);
const savingActivity = ref(false);
const activityForm = ref({
  code: "",
  name: "",
  timeLimitSeconds: 300,
  maxTimesPerDay: 0,
  maxConcurrent: 0,
  sortOrder: 0,
  enabled: true,
  _edit: false as boolean | string,
});

const activityFines = ref<any[]>([]);
const loadingFines = ref(false);
const fineFilterCode = ref("");

const activityFineGroups = computed(() => {
  const finesByCode = new Map<string, any[]>();
  for (const fine of activityFines.value) {
    const code = fine.activityCode || "";
    if (!finesByCode.has(code)) finesByCode.set(code, []);
    finesByCode.get(code)!.push(fine);
  }
  for (const list of finesByCode.values()) {
    list.sort((a, b) => (a.segmentMinutes ?? 0) - (b.segmentMinutes ?? 0));
  }

  let types = activityTypes.value;
  if (fineFilterCode.value) {
    types = types.filter((t) => t.code === fineFilterCode.value);
  }

  return types.map((activity) => ({
    activity,
    fines: finesByCode.get(activity.code) || [],
  }));
});

const fineVisible = ref(false);
const savingFine = ref(false);
const fineForm = ref({ activityCode: "", segmentMinutes: 5, fineAmount: 10 });
const fineBatchVisible = ref(false);
const savingFineBatch = ref(false);
const fineBatchForm = ref({
  activityCodes: [] as string[],
  items: [
    { segmentMinutes: 5, fineAmount: 10 },
    { segmentMinutes: 15, fineAmount: 30 },
  ],
});

const handoverChatId = ref("");
const loadingHandoverCfg = ref(false);
const savingHandoverCfg = ref(false);
const handoverCfg = ref<any>({});
const pushChatId = ref("");
const loadingPushCfg = ref(false);
const savingPushCfg = ref(false);
const testingPushCfg = ref(false);
const pushCfg = ref<any>({});
type PushTargetField = "channelId" | "notificationGroupId" | "extraWorkGroupId";
const pushTargetPickField = ref<PushTargetField | "">("");
const pushTargetDiscoverVisible = ref(false);
const pushTargetDiscovering = ref(false);
const pushTargetDiscoverList = ref<any[]>([]);

const selectedHandoverGroup = computed(() =>
  groups.value.find((g) => g.chatId === handoverChatId.value) || { chatId: handoverChatId.value, chatTitle: "" },
);

const punchGroupChatIds = computed(() => new Set(groups.value.map((g) => g.chatId).filter(Boolean)));

const pushDiscoverTargets = computed(() =>
  pushTargetDiscoverList.value.filter((item) => !punchGroupChatIds.value.has(item.chatId)),
);

const pushTargetDiscoverTitle = computed(() => {
  switch (pushTargetPickField.value) {
    case "channelId":
      return "发现绑定 · 频道";
    case "notificationGroupId":
      return "发现绑定 · 通知群";
    case "extraWorkGroupId":
      return "发现绑定 · 迟到/早退通知群";
    default:
      return "发现绑定推送目标";
  }
});

async function onHandoverGroupSelected(chatId: string) {
  if (!chatId) {
    handoverCfg.value = {};
    pushCfg.value = {};
    return;
  }
  pushChatId.value = chatId;
  await loadHandoverConfig();
  await loadPushSettings();
}

const workFines = ref<any[]>([]);
const loadingWorkFines = ref(false);

const workStartFines = computed(() =>
  workFines.value
    .filter((f) => f.checkinType === "work_start")
    .sort((a, b) => (a.segmentMinutes ?? 0) - (b.segmentMinutes ?? 0))
);
const workEndFines = computed(() =>
  workFines.value
    .filter((f) => f.checkinType === "work_end")
    .sort((a, b) => (a.segmentMinutes ?? 0) - (b.segmentMinutes ?? 0))
);

const workFineVisible = ref(false);
const savingWorkFine = ref(false);
const workFineForm = ref({ checkinType: "work_start", segmentMinutes: 5, fineAmount: 10 });
const workFineBatchVisible = ref(false);
const savingWorkFineBatch = ref(false);
const workFineBatchForm = ref({
  checkinTypes: ["work_start"] as string[],
  items: [
    { segmentMinutes: 5, fineAmount: 10 },
    { segmentMinutes: 15, fineAmount: 30 },
  ],
});
const sharedWorkstations = ref<any[]>([]);
const loadingSharedWs = ref(false);
const deviceBindings = ref<any[]>([]);
const loadingDeviceBindings = ref(false);
const sharedWsVisible = ref(false);
const sharedWsForm = ref({ deviceId: "", employeeId: "" });
const bindableDevices = ref<Device[]>([]);
const loadingBindableDevices = ref(false);

function formatSharedWsDeviceLabel(device: Device) {
  const hostname = device.hostname || "未命名设备";
  const employee = device.employeeName || device.employeeNumber;
  return employee ? `${hostname}（${employee}）` : hostname;
}

async function loadBindableDevices() {
  loadingBindableDevices.value = true;
  try {
    const { data } = await deviceApi.listAll();
    const items: Device[] = data.items || [];
    const byId = new Map<string, Device>();
    for (const device of items) {
      if (device.status === "rejected") continue;
      const existing = byId.get(device.id);
      if (!existing || device.isActiveSession) {
        byId.set(device.id, device);
      }
    }
    bindableDevices.value = Array.from(byId.values()).sort((a, b) =>
      formatSharedWsDeviceLabel(a).localeCompare(formatSharedWsDeviceLabel(b), "zh-CN"),
    );
  } catch (error: any) {
    bindableDevices.value = [];
    ElMessage.error(error.message || "加载设备列表失败");
  } finally {
    loadingBindableDevices.value = false;
  }
}

const exportDateFrom = ref("");
const exportDateTo = ref("");
const exportMonth = ref("");
const exporting = ref(false);
const runningReset = ref(false);
const exportBusinessToday = ref(false);

let bindingSearchTimer: ReturnType<typeof setTimeout> | null = null;

async function loadModuleFlag() {
  try {
    const { data } = await metaApi.modules();
    moduleEnabled.value = !!data?.telegramAttendance;
    captureLinkedEnvOverride.value = false;
  } catch {
    moduleEnabled.value = false;
    captureLinkedEnvOverride.value = false;
  }
}

async function loadSettings() {
  loadingSettings.value = true;
  try {
    const res = await workforceTelegramApi.getSettings();
    settings.value = res.data || {};
    settingsForm.value = {
      enabled: !!settings.value.enabled,
      botToken: "",
      timezone: settings.value.timezone || "Asia/Shanghai",
      dayStart: settings.value.dayStart || "09:00",
      dayEnd: settings.value.dayEnd || "21:00",
      checkInGraceBefore: settings.value.checkInGraceBefore ?? settings.value.graceMinutes ?? 120,
      checkInGraceAfter: settings.value.checkInGraceAfter ?? settings.value.graceMinutesAfter ?? 360,
      checkOutGraceBefore: settings.value.checkOutGraceBefore ?? 120,
      checkOutGraceAfter: settings.value.checkOutGraceAfter ?? settings.value.workEndGraceAfter ?? 360,
      lateToleranceMinutes: settings.value.lateToleranceMinutes ?? 0,
      earlyLeaveToleranceMinutes: settings.value.earlyLeaveToleranceMinutes ?? 10,
      dualShiftEnabled: !!settings.value.dualShiftEnabled || !!settings.value.tripleShiftEnabled,
      tripleShiftEnabled: !!settings.value.tripleShiftEnabled,
      nightStart: settings.value.nightStart || "21:00",
      nightEnd: settings.value.nightEnd || "09:00",
      middleStart: settings.value.middleStart || "13:00",
      middleEnd: settings.value.middleEnd || "01:00",
      dailyResetTime: settings.value.dailyResetTime || "09:00",
      dailyResetExecutionOffsetMinutes: settings.value.dailyResetExecutionOffsetMinutes ?? 120,
      dailyResetEnabled: settings.value.dailyResetEnabled !== false,
      shiftWindowDisabled: !!settings.value.shiftWindowDisabled,
      requirePCLogin: settings.value.requirePCLogin !== false,
      captureLinkedToCheckin: !!settings.value.captureLinkedToCheckin,
      webPunchOnly: !!settings.value.webPunchOnly,
      adminTelegramUserIds: settings.value.adminTelegramUserIds || "",
      botUiLang: settings.value.botUiLang || "both",
      fineCurrency: normalizeFineCurrency(settings.value.fineCurrency),
      botMode: settings.value.botMode || "polling",
      webhookUrl: settings.value.webhookUrl || "",
      showKeyboardInfoButtons: !!settings.value.showKeyboardInfoButtons,
    };
    moduleEnabled.value = !!settings.value.moduleEnabled;
    captureLinkedEnvOverride.value = !!settings.value.captureLinkedEnvOverride;
  } catch (error: any) {
    ElMessage.error(error.message || "加载配置失败");
  } finally {
    loadingSettings.value = false;
  }
}

function onTripleShiftChange(enabled: boolean) {
  if (enabled) {
    settingsForm.value.dualShiftEnabled = true;
    if (!settingsForm.value.middleStart) settingsForm.value.middleStart = "13:00";
    if (!settingsForm.value.middleEnd) settingsForm.value.middleEnd = "01:00";
  }
}

async function saveFineCurrency() {
  if (!canEdit.value) return;
  try {
    await workforceTelegramApi.updateSettings({
      fineCurrency: normalizeFineCurrency(settingsForm.value.fineCurrency),
    });
    ElMessage.success("罚款币种已保存");
    await loadSettings();
  } catch (error: any) {
    ElMessage.error(error.message || "保存币种失败");
  }
}

async function saveSettings() {
  savingSettings.value = true;
  try {
    const payload: any = {
      enabled: settingsForm.value.enabled,
      timezone: settingsForm.value.timezone,
      dayStart: settingsForm.value.dayStart,
      dayEnd: settingsForm.value.dayEnd,
      checkInGraceBefore: settingsForm.value.checkInGraceBefore,
      checkInGraceAfter: settingsForm.value.checkInGraceAfter,
      checkOutGraceBefore: settingsForm.value.checkOutGraceBefore,
      checkOutGraceAfter: settingsForm.value.checkOutGraceAfter,
      lateToleranceMinutes: settingsForm.value.lateToleranceMinutes,
      earlyLeaveToleranceMinutes: settingsForm.value.earlyLeaveToleranceMinutes,
      graceMinutes: settingsForm.value.checkInGraceBefore,
      graceMinutesAfter: settingsForm.value.checkInGraceAfter,
      workEndGraceMinutes: settingsForm.value.earlyLeaveToleranceMinutes,
      workEndGraceAfter: settingsForm.value.checkOutGraceAfter,
      dualShiftEnabled: !!(settingsForm.value.dualShiftEnabled || settingsForm.value.tripleShiftEnabled),
      tripleShiftEnabled: !!settingsForm.value.tripleShiftEnabled,
      nightStart: settingsForm.value.nightStart,
      nightEnd: settingsForm.value.nightEnd,
      middleStart: settingsForm.value.middleStart,
      middleEnd: settingsForm.value.middleEnd,
      dailyResetTime: settingsForm.value.dailyResetTime,
      dailyResetExecutionOffsetMinutes: settingsForm.value.dailyResetExecutionOffsetMinutes,
      dailyResetEnabled: settingsForm.value.dailyResetEnabled,
      shiftWindowDisabled: settingsForm.value.shiftWindowDisabled,
      requirePCLogin: settingsForm.value.requirePCLogin,
      captureLinkedToCheckin: settingsForm.value.captureLinkedToCheckin,
      webPunchOnly: settingsForm.value.webPunchOnly,
      adminTelegramUserIds: settingsForm.value.adminTelegramUserIds,
      botUiLang: settingsForm.value.botUiLang,
      fineCurrency: normalizeFineCurrency(settingsForm.value.fineCurrency),
      botMode: settingsForm.value.botMode,
      webhookUrl: settingsForm.value.webhookUrl,
      showKeyboardInfoButtons: !!settingsForm.value.showKeyboardInfoButtons,
    };
    if (settingsForm.value.botToken.trim()) {
      payload.botToken = settingsForm.value.botToken.trim();
    }
    const res = await workforceTelegramApi.updateSettings(payload);
    if (payload.tripleShiftEnabled && !res.data?.tripleShiftEnabled) {
      ElMessage.error("三班模式未保存成功：后端返回仍为关闭。请确认已部署含三班字段的后端并重启服务后再保存。");
      await loadSettings();
      return;
    }
    ElMessage.success("配置已保存");
    settingsForm.value.botToken = "";
    await loadSettings();
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingSettings.value = false;
  }
}

async function loadWorkerConnection() {
  try {
    const res = await workforceTelegramApi.getWorkerConnection();
    workerConn.value = res.data || {};
  } catch {
    workerConn.value = {};
  }
}

function buildPairingCommand(code: string) {
  const backendUrl = import.meta.env.DEV
    ? "http://127.0.0.1:8080/api/v1"
    : `${window.location.origin}/api/v1`;
  return [
    "cd telegram-worker",
    `# PowerShell:`,
    `$env:BACKEND_URL="${backendUrl}"; $env:TELEGRAM_PAIRING_CODE="${code}"; go run .`,
    `# Linux/macOS:`,
    `BACKEND_URL=${backendUrl} TELEGRAM_PAIRING_CODE=${code} go run .`,
  ].join("\n");
}

async function generatePairing() {
  if (!settings.value.hasBotToken) {
    ElMessage.warning("请先保存 Bot Token 并启用 Telegram");
    return;
  }
  generatingPairing.value = true;
  try {
    const res = await workforceTelegramApi.generateWorkerPairing();
    const code = res.data?.pairingCode || "";
    pairingInfo.value = {
      code,
      expiresAt: res.data?.expiresAt || "",
    };
    pairingCommand.value = code ? buildPairingCommand(code) : "";
    ElMessage.success("配对码已生成，请在 15 分钟内启动 Worker");

  } catch (error: any) {
    ElMessage.error(error.message || "生成失败");
  } finally {
    generatingPairing.value = false;
  }
}

function copyPairingCommand() {
  if (!pairingCommand.value) return;
  navigator.clipboard.writeText(pairingCommand.value).then(
    () => ElMessage.success("已复制"),
    () => ElMessage.error("复制失败"),
  );
}

async function registerWebhook() {
  try {
    await ElMessageBox.confirm(
      "将当前机器人连接到此项目。请确认该机器人没有被旧 monitor 服务使用。",
      "注册 Webhook",
    );
  } catch {
    return;
  }
  registeringWebhook.value = true;
  try {
    await workforceTelegramApi.registerWebhook();
    ElMessage.success("Webhook 已注册");
  } catch (error: any) {
    ElMessage.error(error.message || "Webhook 注册失败");
  } finally {
    registeringWebhook.value = false;
  }
}

async function regenerateWebhookSecret() {
  try {
    await ElMessageBox.confirm("重新生成后 Webhook 模式 Worker 会自动同步新 Secret，是否继续？", "确认", {
      type: "warning",
    });
  } catch {
    return;
  }
  regeneratingWebhookSecret.value = true;
  try {
    const res = await workforceTelegramApi.regenerateWebhookSecret();
    webhookSecret.value = res.data?.webhookSecret || "";
    ElMessage.success("Webhook Secret 已生成");
    await loadSettings();
  } catch (error: any) {
    ElMessage.error(error.message || "生成失败");
  } finally {
    regeneratingWebhookSecret.value = false;
  }
}

async function regenerateSecret() {
  try {
    await ElMessageBox.confirm("重新生成后已配对的 Worker 将失效，需重新生成配对码并启动，是否继续？", "确认", {
      type: "warning",
    });
  } catch {
    return;
  }
  regeneratingSecret.value = true;
  try {
    const res = await workforceTelegramApi.regenerateWorkerSecret();
    workerSecret.value = res.data?.workerSecret || "";
    ElMessage.success("Worker Secret 已生成");
  } catch (error: any) {
    ElMessage.error(error.message || "生成失败");
  } finally {
    regeneratingSecret.value = false;
  }
}

function copySecret() {
  if (!workerSecret.value) return;
  navigator.clipboard.writeText(workerSecret.value).then(
    () => ElMessage.success("已复制"),
    () => ElMessage.error("复制失败"),
  );
}

async function loadGroups() {
  loadingGroups.value = true;
  try {
    const res = await workforceTelegramApi.listGroups();
    groups.value = res.data?.items || [];
    await ensureDefaultHandoverGroup();
  } catch (error: any) {
    ElMessage.error(error.message || "加载群组失败");
  } finally {
    loadingGroups.value = false;
  }
}

async function ensureDefaultHandoverGroup() {
  if (!groups.value.length) return;
  if (!handoverChatId.value) {
    handoverChatId.value = groups.value[0].chatId;
    pushChatId.value = groups.value[0].chatId;
  }
  if (activeTab.value === "handover" && handoverChatId.value) {
    await onHandoverGroupSelected(handoverChatId.value);
  }
}

async function discoverGroups() {
  discoveringGroups.value = true;
  try {
    const res = await workforceTelegramApi.discoverGroups();
    discoveredGroups.value = res.data || [];
    discoverVisible.value = true;
    if (!discoveredGroups.value.length) {
      ElMessage.info("未发现群组：请确认 Bot 已入群且 Worker 在线；仍没有时可在群内发一条消息后重试");
    }
  } catch (error: any) {
    ElMessage.error(error.message || "发现群组失败");
  } finally {
    discoveringGroups.value = false;
  }
}

async function bindDiscoveredGroup(row: { chatId: string; groupName?: string; chatTitle?: string; bound?: boolean }) {
  try {
    await workforceTelegramApi.upsertGroup({
      chatId: row.chatId,
      chatTitle: row.groupName || row.chatTitle || row.chatId,
      enabled: true,
    });
    ElMessage.success("绑定成功");
    row.bound = true;
    await loadGroups();
    handoverChatId.value = row.chatId;
    pushChatId.value = row.chatId;
    if (activeTab.value === "handover") {
      await onHandoverGroupSelected(row.chatId);
    }
    const res = await workforceTelegramApi.discoverGroups();
    discoveredGroups.value = res.data || [];
  } catch (error: any) {
    ElMessage.error(error.message || "绑定失败");
  }
}

function openGroupDialog(row?: any) {
  if (row) {
    groupForm.value = {
      chatId: row.chatId,
      chatTitle: row.chatTitle || "",
      enabled: row.enabled !== false,
      shiftGraceBefore: row.shiftGraceBefore,
      shiftGraceAfter: row.shiftGraceAfter,
      checkOutGraceBefore: row.checkOutGraceBefore ?? row.workEndGraceBefore,
      checkOutGraceAfter: row.checkOutGraceAfter ?? row.workEndGraceAfter,
      lateToleranceMinutes: row.lateToleranceMinutes,
      earlyLeaveToleranceMinutes: row.earlyLeaveToleranceMinutes,
      dailyResetTime: row.dailyResetTime || "",
      botUiLang: row.botUiLang || "",
      _edit: true,
    };
  } else {
    groupForm.value = {
      chatId: "",
      chatTitle: "",
      enabled: true,
      shiftGraceBefore: undefined,
      shiftGraceAfter: undefined,
      checkOutGraceBefore: undefined,
      checkOutGraceAfter: undefined,
      lateToleranceMinutes: undefined,
      earlyLeaveToleranceMinutes: undefined,
      dailyResetTime: "",
      botUiLang: "",
      _edit: false,
    };
  }
  groupVisible.value = true;
}

async function saveGroup() {
  if (!groupForm.value.chatId.trim()) {
    ElMessage.warning("请填写 Chat ID");
    return;
  }
  savingGroup.value = true;
  try {
    const payload: any = {
      chatId: groupForm.value.chatId.trim(),
      chatTitle: groupForm.value.chatTitle.trim(),
      enabled: groupForm.value.enabled,
    };
    if (groupForm.value.shiftGraceBefore != null) payload.shiftGraceBefore = groupForm.value.shiftGraceBefore;
    if (groupForm.value.shiftGraceAfter != null) payload.shiftGraceAfter = groupForm.value.shiftGraceAfter;
    if (groupForm.value.checkOutGraceBefore != null) payload.checkOutGraceBefore = groupForm.value.checkOutGraceBefore;
    if (groupForm.value.checkOutGraceAfter != null) payload.checkOutGraceAfter = groupForm.value.checkOutGraceAfter;
    if (groupForm.value.lateToleranceMinutes != null) payload.lateToleranceMinutes = groupForm.value.lateToleranceMinutes;
    if (groupForm.value.earlyLeaveToleranceMinutes != null) payload.earlyLeaveToleranceMinutes = groupForm.value.earlyLeaveToleranceMinutes;
    if (groupForm.value.dailyResetTime.trim()) payload.dailyResetTime = groupForm.value.dailyResetTime.trim();
    if (groupForm.value.botUiLang) payload.botUiLang = groupForm.value.botUiLang;
    await workforceTelegramApi.upsertGroup(payload);
    ElMessage.success("群组已保存");
    groupVisible.value = false;
    await loadGroups();
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingGroup.value = false;
  }
}

async function removeGroup(row: any) {
  try {
    await ElMessageBox.confirm(`确定删除群 ${row.chatId}？`, "确认", { type: "warning" });
    await workforceTelegramApi.deleteGroup(row.id);
    ElMessage.success("已删除");
    await loadGroups();
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "删除失败");
  }
}

function debouncedLoadBindings() {
  if (bindingSearchTimer) clearTimeout(bindingSearchTimer);
  bindingSearchTimer = setTimeout(loadBindings, 300);
}

async function loadBindings() {
  loadingBindings.value = true;
  try {
    const res = await workforceTelegramApi.listBindings(bindingSearch.value.trim() || undefined);
    bindings.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载绑定失败");
  } finally {
    loadingBindings.value = false;
  }
}

async function loadEmployees() {
  try {
    const res = await adminApi.getEmployees({ limit: 500 });
    employees.value = (res.data?.items || []).filter((e: any) => e.status === 1);
  } catch {
    employees.value = [];
  }
}

function openBindingDialog() {
  bindingForm.value = { employeeId: "", telegramUserId: "", telegramUsername: "", chatId: "" };
  bindingVisible.value = true;
}

async function saveBinding() {
  if (!bindingForm.value.employeeId) {
    ElMessage.warning("请选择员工");
    return;
  }
  const tgId = Number(bindingForm.value.telegramUserId);
  if (!tgId || Number.isNaN(tgId)) {
    ElMessage.warning("Telegram User ID 必须为数字");
    return;
  }
  savingBinding.value = true;
  try {
    await workforceTelegramApi.createBinding({
      employeeId: bindingForm.value.employeeId,
      telegramUserId: tgId,
      telegramUsername: bindingForm.value.telegramUsername.trim() || undefined,
      chatId: bindingForm.value.chatId.trim() || undefined,
    });
    ElMessage.success("绑定成功");
    bindingVisible.value = false;
    await loadBindings();
  } catch (error: any) {
    ElMessage.error(error.message || "绑定失败");
  } finally {
    savingBinding.value = false;
  }
}

async function removeBinding(row: any) {
  try {
    await ElMessageBox.confirm(`确定解除 ${row.employeeName} 的 Telegram 绑定？`, "确认", {
      type: "warning",
    });
    await workforceTelegramApi.deleteBinding(row.id);
    ElMessage.success("已解除绑定");
    await loadBindings();
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "操作失败");
  }
}

async function resetEmployeeDaily(row: { employeeName: string; employeeId: string; deviceId?: string }, label: string) {
  try {
    await ElMessageBox.confirm(
      `确定重置 ${row.employeeName} 当日的上下班打卡、活动会话与考勤记录？此操作不可撤销。`,
      `重置当日考勤 · ${label}`,
      { type: "warning" },
    );
    const res = await workforceTelegramApi.resetEmployeeDaily({
      employeeId: row.employeeId,
      deviceId: row.deviceId,
    });
    showResetResult(row.employeeName, res.data);
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "重置失败");
  }
}

function showResetResult(employeeName: string, data?: any) {
  const date = data?.businessDate || "今日";
  const parts = [
    `已重置 ${employeeName} 的 ${date} 考勤`,
    data?.deletedWorkSessions ? `删除 ${data.deletedWorkSessions} 条上班会话` : "",
    data?.deletedActivitySessions ? `删除 ${data.deletedActivitySessions} 条活动会话` : "",
    data?.agentNotified ? "已通知客户端下班" : "",
  ].filter(Boolean);
  ElMessage.success(parts.join("，"));
}

async function resetBindingDaily(row: any) {
  try {
    await ElMessageBox.confirm(
      `确定重置 ${row.employeeName} 当日的上下班打卡、活动会话与考勤记录？此操作不可撤销。`,
      "重置当日考勤",
      { type: "warning" },
    );
    const res = await workforceTelegramApi.resetBindingDaily(row.id);
    showResetResult(row.employeeName, res.data);
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "重置失败");
  }
}

async function resetSharedMemberDaily(row: any) {
  await resetEmployeeDaily(row, "共享工位成员");
}

async function resetDeviceBindingDaily(row: any) {
  try {
    await ElMessageBox.confirm(
      `确定重置工位 ${row.deviceName} 上「当前 PC 已登录员工」的当日考勤？若 PC 未登录请从成员列表按员工重置。`,
      "重置工位 TG 当日考勤",
      { type: "warning" },
    );
    const res = await workforceTelegramApi.resetDeviceBindingDaily(row.id);
    const name = res.data?.employeeName || "员工";
    showResetResult(name, res.data);
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "重置失败");
  }
}

async function loadActivityTypes() {
  loadingActivities.value = true;
  try {
    const res = await workforceTelegramApi.listActivityTypes();
    activityTypes.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载活动类型失败");
  } finally {
    loadingActivities.value = false;
  }
}

function openActivityDialog(row?: any) {
  if (row) {
    activityForm.value = {
      code: row.code,
      name: row.name,
      timeLimitSeconds: row.timeLimitSeconds,
      maxTimesPerDay: row.maxTimesPerDay,
      maxConcurrent: row.maxConcurrent || 0,
      sortOrder: row.sortOrder,
      enabled: row.enabled,
      _edit: row.code,
    };
  } else {
    activityForm.value = {
      code: "",
      name: "",
      timeLimitSeconds: 300,
      maxTimesPerDay: 0,
      maxConcurrent: 0,
      sortOrder: activityTypes.value.length + 1,
      enabled: true,
      _edit: false,
    };
  }
  activityVisible.value = true;
}

async function saveActivityType() {
  if (!activityForm.value.code.trim() || !activityForm.value.name.trim()) {
    ElMessage.warning("请填写代码和名称");
    return;
  }
  savingActivity.value = true;
  try {
    await workforceTelegramApi.upsertActivityType({
      code: activityForm.value.code.trim(),
      name: activityForm.value.name.trim(),
      timeLimitSeconds: activityForm.value.timeLimitSeconds,
      maxTimesPerDay: activityForm.value.maxTimesPerDay,
      maxConcurrent: activityForm.value.maxConcurrent,
      sortOrder: activityForm.value.sortOrder,
      enabled: activityForm.value.enabled,
    });
    ElMessage.success("已保存");
    activityVisible.value = false;
    await loadActivityTypes();
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingActivity.value = false;
  }
}

async function removeActivityType(row: any) {
  try {
    await ElMessageBox.confirm(`确定删除活动「${row.name}」？`, "确认", { type: "warning" });
    await workforceTelegramApi.deleteActivityType(row.code);
    ElMessage.success("已删除");
    await loadActivityTypes();
    await loadActivityFines();
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "删除失败");
  }
}

async function loadActivityFines() {
  loadingFines.value = true;
  try {
    const res = await workforceTelegramApi.listActivityFines(fineFilterCode.value || undefined);
    activityFines.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载罚款规则失败");
  } finally {
    loadingFines.value = false;
  }
}

function openFineDialog() {
  fineForm.value = {
    activityCode: fineFilterCode.value || activityTypes.value[0]?.code || "",
    segmentMinutes: 5,
    fineAmount: 10,
  };
  fineVisible.value = true;
}

async function saveActivityFine() {
  if (!fineForm.value.activityCode) {
    ElMessage.warning("请选择活动");
    return;
  }
  savingFine.value = true;
  try {
    await workforceTelegramApi.upsertActivityFine({ ...fineForm.value });
    ElMessage.success("已保存");
    fineVisible.value = false;
    await loadActivityFines();
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingFine.value = false;
  }
}

async function removeActivityFine(row: any) {
  try {
    await ElMessageBox.confirm("确定删除该罚款规则？", "确认", { type: "warning" });
    await workforceTelegramApi.deleteActivityFine(row.id);
    ElMessage.success("已删除");
    await loadActivityFines();
  } catch (error: any) {
    if (error !== "cancel") ElMessage.error(error.message || "删除失败");
  }
}

function createDefaultFineBatchItems() {
  return [
    { segmentMinutes: 5, fineAmount: 10 },
    { segmentMinutes: 15, fineAmount: 30 },
  ];
}

function openFineBatchDialog() {
  const preset = fineFilterCode.value
    ? [fineFilterCode.value]
    : activityTypes.value.map((a: any) => a.code);
  fineBatchForm.value = {
    activityCodes: [...preset],
    items: createDefaultFineBatchItems(),
  };
  fineBatchVisible.value = true;
}

function selectAllFineBatchActivities() {
  fineBatchForm.value.activityCodes = activityTypes.value.map((a: any) => a.code);
}

function addFineBatchRow() {
  fineBatchForm.value.items.push({ segmentMinutes: 5, fineAmount: 10 });
}

function removeFineBatchRow(index: number) {
  if (fineBatchForm.value.items.length <= 1) return;
  fineBatchForm.value.items.splice(index, 1);
}

async function saveActivityFineBatch() {
  if (!fineBatchForm.value.activityCodes.length) {
    ElMessage.warning("请至少选择一个活动");
    return;
  }
  const items = fineBatchForm.value.items.filter(
    (row) => row.segmentMinutes > 0 && row.fineAmount >= 0,
  );
  if (!items.length) {
    ElMessage.warning("请至少填写一条有效规则");
    return;
  }
  const segments = items.map((row) => row.segmentMinutes);
  if (new Set(segments).size !== segments.length) {
    ElMessage.warning("超时档位不能重复");
    return;
  }

  savingFineBatch.value = true;
  try {
    const res = await workforceTelegramApi.batchUpsertActivityFines({
      activityCodes: fineBatchForm.value.activityCodes,
      items,
    });
    const count = res.data?.count ?? items.length * fineBatchForm.value.activityCodes.length;
    ElMessage.success(
      `已批量保存 ${count} 条规则（${fineBatchForm.value.activityCodes.length} 个活动）`,
    );
    fineBatchVisible.value = false;
    await loadActivityFines();
  } catch (error: any) {
    ElMessage.error(error.message || "批量保存失败");
  } finally {
    savingFineBatch.value = false;
  }
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

async function exportDaily() {
  exporting.value = true;
  try {
    const res = await workforceTelegramApi.exportAttendance({
      dateFrom: exportDateFrom.value || undefined,
      dateTo: exportDateTo.value || exportDateFrom.value || undefined,
    });
    downloadBlob(res.data, `telegram_attendance_${exportDateFrom.value || "today"}.xlsx`);
  } catch (error: any) {
    ElMessage.error(error.message || "导出失败");
  } finally {
    exporting.value = false;
  }
}

async function exportMonthly() {
  if (!exportMonth.value) {
    ElMessage.warning("请选择月份");
    return;
  }
  exporting.value = true;
  try {
    const res = await workforceTelegramApi.exportAttendance({ month: exportMonth.value });
    downloadBlob(res.data, `telegram_attendance_${exportMonth.value}.xlsx`);
  } catch (error: any) {
    ElMessage.error(error.message || "导出失败");
  } finally {
    exporting.value = false;
  }
}

async function triggerDailyReset() {
  const exportTodayHint = exportBusinessToday.value
    ? "将导出各群<strong>当前业务日</strong>的数据。"
    : "将导出各群<strong>默认统计日</strong>（一般为昨日）的数据。";
  try {
    await ElMessageBox.confirm(
      `将自动结束未关闭的 Telegram/PC 上班会话与活动，推送 XLSX/CSV 后清理明细。${exportTodayHint}确定执行？`,
      "日重置",
      { type: "warning", dangerouslyUseHTMLString: true },
    );
  } catch {
    return;
  }
  runningReset.value = true;
  try {
    const res = await workforceTelegramApi.runDailyReset({
      exportBusinessToday: exportBusinessToday.value,
    });
    if (res.data?.skipped) {
      if (res.data?.skippedAllGroups) {
        ElMessage.info(`今日各群已重置过（重置日 ${res.data.resetDate || "今天"}），无需重复执行`);
      } else {
        ElMessage.info("今日已重置或未启用自动日重置");
      }
      return;
    }
    const warnings = (res.data?.bindingWarnings as string[] | undefined) || [];
    const summary = [
      `日重置完成（处理 ${res.data?.groupsProcessed ?? 0} 个群）`,
      res.data?.exportDate ? `统计日期 ${res.data.exportDate}` : "",
      res.data?.exportDateNote ? `（${res.data.exportDateNote}）` : "",
      `关闭 TG 上班 ${res.data?.closedWorkSessions ?? 0}、PC 上班 ${res.data?.closedAgentSessions ?? 0}、活动 ${res.data?.closedActivities ?? 0}`,
    ].filter(Boolean).join("；");
    if (warnings.length > 0) {
      ElMessage.warning(`${summary}。绑定告警：${warnings.slice(0, 3).join("；")}${warnings.length > 3 ? "…" : ""}`);
    } else {
      ElMessage.success(summary);
    }
  } catch (error: any) {
    ElMessage.error(error.message || "日重置失败");
  } finally {
    runningReset.value = false;
  }
}

async function loadHandoverConfig() {
  if (!handoverChatId.value.trim()) {
    ElMessage.warning("请先选择打卡群");
    return;
  }
  loadingHandoverCfg.value = true;
  try {
    const res = await workforceTelegramApi.getHandoverConfig(handoverChatId.value.trim());
    handoverCfg.value = res.data || {};
  } catch (error: any) {
    ElMessage.error(error.message || "加载换班配置失败");
  } finally {
    loadingHandoverCfg.value = false;
  }
}

async function saveHandoverConfig() {
  savingHandoverCfg.value = true;
  try {
    await workforceTelegramApi.upsertHandoverConfig({
      chatId: handoverCfg.value.chatId,
      data: {
        handoverEnabled: handoverCfg.value.handoverEnabled,
        handoverDay: handoverCfg.value.handoverDay,
        handoverMonth: handoverCfg.value.handoverMonth,
        handoverDayStartTime: handoverCfg.value.handoverDayStartTime,
        nightStartTime: handoverCfg.value.nightStartTime,
        dayStartTime: handoverCfg.value.dayStartTime,
        handoverNightHours: handoverCfg.value.handoverNightHours,
        handoverDayHours: handoverCfg.value.handoverDayHours,
        normalNightHours: handoverCfg.value.normalNightHours,
        normalDayHours: handoverCfg.value.normalDayHours,
        handoverResetThresholdHours: handoverCfg.value.handoverResetThresholdHours,
      },
    });
    ElMessage.success("换班配置已保存");
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingHandoverCfg.value = false;
  }
}

async function loadPushSettings() {
  const chatId = handoverChatId.value.trim() || pushChatId.value.trim();
  if (!chatId) {
    ElMessage.warning("请先选择打卡群");
    return;
  }
  loadingPushCfg.value = true;
  try {
    const res = await workforceTelegramApi.getPushSettings(chatId);
    pushCfg.value = res.data || {};
    pushChatId.value = chatId;
  } catch (error: any) {
    ElMessage.error(error.message || "加载推送配置失败");
  } finally {
    loadingPushCfg.value = false;
  }
}

async function refreshPushTargetDiscover() {
  pushTargetDiscovering.value = true;
  try {
    const res = await workforceTelegramApi.discoverGroups();
    pushTargetDiscoverList.value = res.data || [];
    discoveredGroups.value = pushTargetDiscoverList.value;
    if (!pushDiscoverTargets.value.length) {
      ElMessage.info("未发现可用频道/群：请确认 Bot 已入群；仍没有时可发一条消息后重试");
    }
  } catch (error: any) {
    ElMessage.error(error.message || "发现失败");
  } finally {
    pushTargetDiscovering.value = false;
  }
}

async function openPushTargetDiscover(field: PushTargetField) {
  pushTargetPickField.value = field;
  pushTargetDiscoverVisible.value = true;
  if (!pushTargetDiscoverList.value.length) {
    await refreshPushTargetDiscover();
  }
}

function selectPushTarget(row: { chatId: string; chatTitle?: string }) {
  const field = pushTargetPickField.value;
  if (!field || !row.chatId) return;
  pushCfg.value[field] = row.chatId;
  pushTargetDiscoverVisible.value = false;
  ElMessage.success(`已填入 ${row.chatTitle || row.chatId}`);
}

async function testPushSettings() {
  const chatId = pushCfg.value.chatId || pushChatId.value.trim();
  if (!chatId) {
    ElMessage.warning("请先选择打卡群并保存推送配置");
    return;
  }
  testingPushCfg.value = true;
  try {
    await workforceTelegramApi.testPushSettings(chatId);
    ElMessage.success("测试消息已发送，请检查频道/通知群");
  } catch (error: any) {
    ElMessage.error(error.message || "测试推送失败");
  } finally {
    testingPushCfg.value = false;
  }
}

async function savePushSettings() {
  savingPushCfg.value = true;
  try {
    await workforceTelegramApi.upsertPushSettings({
      chatId: pushCfg.value.chatId || pushChatId.value.trim(),
      data: {
        channelId: pushCfg.value.channelId,
        notificationGroupId: pushCfg.value.notificationGroupId,
        extraWorkGroupId: pushCfg.value.extraWorkGroupId,
        enableChannelPush: pushCfg.value.enableChannelPush,
        enableGroupPush: pushCfg.value.enableGroupPush,
        enableAdminPush: pushCfg.value.enableAdminPush,
      },
    });
    ElMessage.success("推送配置已保存");
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingPushCfg.value = false;
  }
}

async function loadWorkFines() {
  loadingWorkFines.value = true;
  try {
    const res = await workforceTelegramApi.listWorkFines();
    workFines.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载上下班罚款失败");
  } finally {
    loadingWorkFines.value = false;
  }
}

function openWorkFineDialog() {
  workFineForm.value = { checkinType: "work_start", segmentMinutes: 5, fineAmount: 10 };
  workFineVisible.value = true;
}

async function saveWorkFine() {
  savingWorkFine.value = true;
  try {
    await workforceTelegramApi.upsertWorkFine(workFineForm.value);
    workFineVisible.value = false;
    await loadWorkFines();
    ElMessage.success("规则已保存");
  } catch (error: any) {
    ElMessage.error(error.message || "保存失败");
  } finally {
    savingWorkFine.value = false;
  }
}

async function removeWorkFine(row: any) {
  try {
    await ElMessageBox.confirm("确定删除该规则？", "确认", { type: "warning" });
    await workforceTelegramApi.deleteWorkFine(row.id);
    await loadWorkFines();
  } catch {
    /* cancelled */
  }
}

function openWorkFineBatchDialog() {
  workFineBatchForm.value = {
    checkinTypes: ["work_start", "work_end"],
    items: createDefaultFineBatchItems(),
  };
  workFineBatchVisible.value = true;
}

function selectAllWorkFineBatchTypes() {
  workFineBatchForm.value.checkinTypes = ["work_start", "work_end"];
}

function addWorkFineBatchRow() {
  workFineBatchForm.value.items.push({ segmentMinutes: 5, fineAmount: 10 });
}

function removeWorkFineBatchRow(index: number) {
  if (workFineBatchForm.value.items.length <= 1) return;
  workFineBatchForm.value.items.splice(index, 1);
}

async function saveWorkFineBatch() {
  if (!workFineBatchForm.value.checkinTypes.length) {
    ElMessage.warning("请至少选择一个类型");
    return;
  }
  const items = workFineBatchForm.value.items.filter(
    (row) => row.segmentMinutes > 0 && row.fineAmount >= 0,
  );
  if (!items.length) {
    ElMessage.warning("请至少填写一条有效规则");
    return;
  }
  const segments = items.map((row) => row.segmentMinutes);
  if (new Set(segments).size !== segments.length) {
    ElMessage.warning("档位分钟数不能重复");
    return;
  }

  savingWorkFineBatch.value = true;
  try {
    const res = await workforceTelegramApi.batchUpsertWorkFines({
      checkinTypes: workFineBatchForm.value.checkinTypes,
      items,
    });
    const count = res.data?.count ?? items.length * workFineBatchForm.value.checkinTypes.length;
    ElMessage.success(
      `已批量保存 ${count} 条规则（${workFineBatchForm.value.checkinTypes.length} 种类型）`,
    );
    workFineBatchVisible.value = false;
    await loadWorkFines();
  } catch (error: any) {
    ElMessage.error(error.message || "批量保存失败");
  } finally {
    savingWorkFineBatch.value = false;
  }
}

function attendanceModeLabel(mode?: string) {
  switch (mode) {
    case "tg_only":
      return "仅 TG";
    case "shared_station":
      return "共享工位";
    case "pc_tg":
    default:
      return "PC+TG";
  }
}

async function loadDeviceBindings() {
  loadingDeviceBindings.value = true;
  try {
    const res = await workforceTelegramApi.listDeviceBindings();
    deviceBindings.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载工位绑定失败");
  } finally {
    loadingDeviceBindings.value = false;
  }
}

function isFirstDeviceBindingRow(row: any) {
  const idx = deviceBindings.value.findIndex((item) => item.id === row.id);
  return idx >= 0 && deviceBindings.value[idx] === row;
}

async function removeDeviceBinding(row: any) {
  try {
    await ElMessageBox.confirm(`解除设备 ${row.deviceName} 的工位 Telegram 绑定？`, "确认", { type: "warning" });
    await workforceTelegramApi.deleteDeviceBinding(row.id);
    await loadDeviceBindings();
    ElMessage.success("已解除");
  } catch {
    /* cancelled */
  }
}

async function loadSharedWorkstations() {
  loadingSharedWs.value = true;
  try {
    const res = await workforceTelegramApi.listSharedWorkstations();
    sharedWorkstations.value = res.data?.items || [];
  } catch (error: any) {
    ElMessage.error(error.message || "加载共享工位失败");
  } finally {
    loadingSharedWs.value = false;
  }
}

function openSharedWsDialog() {
  sharedWsForm.value = { deviceId: "", employeeId: "" };
  sharedWsVisible.value = true;
}

async function saveSharedWs() {
  if (!sharedWsForm.value.deviceId || !sharedWsForm.value.employeeId) {
    ElMessage.warning("请选择设备与员工");
    return;
  }
  try {
    await workforceTelegramApi.addSharedWorkstationMember(sharedWsForm.value);
    sharedWsVisible.value = false;
    await loadSharedWorkstations();
    ElMessage.success("已添加");
  } catch (error: any) {
    ElMessage.error(error.message || "添加失败");
  }
}

async function removeSharedWs(row: any) {
  try {
    await ElMessageBox.confirm(`移除 ${row.employeeName} 与该设备的共享关系？`, "确认", { type: "warning" });
    await workforceTelegramApi.removeSharedWorkstationMember({
      deviceId: row.deviceId,
      employeeId: row.employeeId,
    });
    await loadSharedWorkstations();
  } catch {
    /* cancelled */
  }
}

onMounted(async () => {
  const tab = String(route.query.tab || "");
  if (
    tab === "activities" ||
    tab === "settings" ||
    tab === "groups" ||
    tab === "shared" ||
    tab === "handover" ||
    tab === "export"
  ) {
    activeTab.value = tab;
  }
  await loadModuleFlag();
  await Promise.all([
    loadSettings(),

    loadGroups(),
    loadEmployees(),
    loadActivityTypes(),
    loadActivityFines(),
    loadWorkFines(),


  ]);
});

watch(activeTab, (tab) => {
  if (tab === "handover") {
    if (!groups.value.length) {
      loadGroups();
    } else {
      void ensureDefaultHandoverGroup();
    }
  }
  if (tab === "bindings") {
    void loadBindings();
  }
  if (tab === "shared") {
    void loadSharedWorkstations();
    void loadDeviceBindings();
  }
});
</script>

<style scoped>
.tg-setup-guide { margin: 4px 0 8px; padding: 14px 16px; background: #fff; border: 1px solid #e3e9eb; border-radius: 8px; }
.tg-setup-guide ol { margin: 0 0 14px; padding-left: 18px; font-size: 13px; line-height: 2.2; color: #5c706b; }
@import "./attendance.css";

.telegram-attendance-page {
  position: relative;
  min-height: 100%;
}

.tg-blob-1 {
  background: linear-gradient(135deg, #229ed9, #0088cc);
  opacity: 0.35;
}

.tg-blob-2 {
  background: linear-gradient(135deg, #667eea, #764ba2);
  opacity: 0.28;
}

.tg-blob-3 {
  background: linear-gradient(135deg, #43e97b, #38f9d7);
  opacity: 0.22;
}

.tg-content {
  position: relative;
  z-index: 1;
  width: 100%;
  padding: 8px 0 88px;
}

.tg-alert {
  margin-bottom: 16px;
  border-radius: 12px;
  backdrop-filter: blur(8px);
}

.tg-main-card {
  border-radius: 18px;
  border: 1px solid rgba(255, 255, 255, 0.65);
  background: rgba(255, 255, 255, 0.92);
  backdrop-filter: blur(16px);
  box-shadow: 0 8px 32px rgba(15, 23, 42, 0.08);
}

.tg-main-card :deep(.el-card__body) {
  padding: 8px 20px 24px;
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
  transition: color 0.2s ease;
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

.tg-collapse {
  border: none;
}

.tg-collapse-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 12px;
}

.tg-collapse-grid :deep(.el-collapse-item) {
  margin-bottom: 0;
  min-width: 0;
}

.tg-collapse-grid :deep(.panel-full) {
  grid-column: 1 / -1;
}

@media (min-width: 768px) {
  .tg-collapse-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (min-width: 1100px) {
  .tg-collapse-grid {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }

  .tg-collapse-grid :deep(.panel-half) {
    grid-column: span 3;
  }

  .tg-collapse-grid :deep(.panel-full) {
    grid-column: span 6;
  }

  .tg-collapse-grid :deep(.panel-third) {
    grid-column: span 2;
  }
}

.tg-collapse-grid :deep(.panel-third .el-form-item) {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  margin-bottom: 14px;
}

.tg-collapse-grid :deep(.panel-third .el-form-item__label) {
  width: 108px !important;
  min-width: 108px;
  padding: 0 12px 0 0;
  line-height: 32px;
  text-align: left;
  flex-shrink: 0;
}

.tg-collapse-grid :deep(.panel-third .el-form-item__content) {
  margin-left: 0 !important;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.tg-collapse-grid :deep(.panel-half .el-form-item__label),
.tg-collapse-grid :deep(.panel-full .el-form-item__label) {
  width: 120px !important;
}

.tg-collapse :deep(.el-collapse-item) {
  margin-bottom: 12px;
  border: 1px solid rgba(0, 0, 0, 0.06);
  border-radius: 14px;
  overflow: hidden;
  background: #fafbfc;
  transition: box-shadow 0.2s ease;
}

.tg-collapse :deep(.el-collapse-item.is-active) {
  box-shadow: 0 4px 16px rgba(0, 136, 204, 0.08);
  background: #fff;
}

.tg-collapse :deep(.el-collapse-item__header) {
  height: 52px;
  padding: 0 18px;
  border: none;
  background: transparent;
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}

.tg-collapse :deep(.el-collapse-item__wrap) {
  border: none;
  background: transparent;
}

.tg-collapse :deep(.el-collapse-item__content) {
  padding: 4px 18px 18px;
}

.tg-panel-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.tg-panel-num {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 700;
  color: #0088cc;
  background: rgba(0, 136, 204, 0.1);
}

.tg-title-tag {
  margin-left: 2px;
  vertical-align: middle;
}

.feature-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 16px;
  margin-bottom: 20px;
}

.feature-grid-pair {
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.feature-grid-pair .feature-card {
  padding: 14px;
  min-width: 0;
}

@media (max-width: 980px) {
  .feature-grid-pair {
    grid-template-columns: 1fr;
  }
}

.feature-card {
  padding: 18px;
  border-radius: 14px;
  border: 1px solid rgba(0, 0, 0, 0.06);
  background: linear-gradient(145deg, #f8fafc 0%, #f1f5f9 100%);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.feature-card.is-on {
  border-color: rgba(0, 136, 204, 0.35);
  background: linear-gradient(145deg, #f0f9ff 0%, #e0f2fe 100%);
  box-shadow: 0 6px 20px rgba(0, 136, 204, 0.1);
}

.feature-card.is-locked {
  opacity: 0.72;
}

.feature-card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.feature-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  border-radius: 12px;
  font-size: 22px;
  color: #fff;
}

.feature-icon.pc {
  background: linear-gradient(135deg, #667eea, #764ba2);
}

.feature-icon.capture {
  background: linear-gradient(135deg, #229ed9, #0088cc);
}

.feature-icon.web {
  background: linear-gradient(135deg, #0d9488, #0f766e);
}

.feature-title {
  margin: 0 0 6px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}

.feature-desc {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: #909399;
}

.section-desc {
  margin: 0 0 12px;
  padding: 10px 14px;
  font-size: 13px;
  color: #606266;
  background: rgba(0, 136, 204, 0.06);
  border-radius: 10px;
  border-left: 3px solid #0088cc;
}

.sub-block-title {
  margin: 28px 0 14px;
  padding-left: 12px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
  border-left: 3px solid #667eea;
}

.block-hint {
  display: block;
  margin-left: 0 !important;
  margin-top: 4px;
}

.save-all-row {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px dashed rgba(0, 0, 0, 0.08);
}

.settings-form {
  width: 100%;
  padding-top: 4px;
}

.field-hint {
  margin-left: 8px;
  font-size: 12px;
  color: #909399;
}

.fine-batch-rows {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.fine-batch-select-wrap {
  width: 100%;
}

.fine-batch-select-actions {
  display: flex;
  gap: 8px;
  margin-top: 6px;
}

.fine-batch-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.fine-batch-label {
  font-size: 13px;
  color: #606266;
  white-space: nowrap;
}

.fine-batch-rows > .field-hint,
.fine-batch-rows .field-hint {
  margin-left: 0;
  margin-top: 4px;
}

.inline-pair {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  align-items: center;
}

.toolbar,
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

.tg-tip-alert {
  margin-bottom: 16px;
  border-radius: 12px;
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

.export-panel h4 {
  margin: 0 0 12px;
  font-size: 15px;
  font-weight: 600;
}

.export-panel p {
  margin: 0 0 12px;
}

.export-reset-block {
  margin-top: 28px;
  padding: 20px;
  border-radius: 14px;
  background: linear-gradient(145deg, #fffbeb 0%, #fef3c7 100%);
  border: 1px solid rgba(245, 158, 11, 0.2);
}

.reset-export-today {
  display: block;
  margin: 8px 0 12px;
}

.target-id-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  align-items: center;
  max-width: 640px;
}

.target-id-row .el-input {
  flex: 1;
  min-width: 200px;
}

.target-id-row .el-button {
  flex-shrink: 0;
}

.binding-hint {
  margin-bottom: 12px;
  border-radius: 12px;
}

.binding-hint-list {
  margin: 8px 0 0;
  padding-left: 18px;
  font-size: 13px;
  line-height: 1.6;
}

.binding-hint-list code {
  font-size: 12px;
  background: rgba(0, 136, 204, 0.1);
  color: #0088cc;
  padding: 2px 6px;
  border-radius: 4px;
}

.work-fine-section {
  margin-top: 32px;
  padding-top: 24px;
  border-top: 1px dashed rgba(0, 0, 0, 0.08);
}

.work-fine-section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}

.work-fine-section-head .sub-block-title {
  margin-top: 0;
  border-left: none;
  padding-left: 0;
}

.inline-desc {
  margin: 6px 0 0;
  padding: 0;
  background: none;
  border-left: none;
}

.compact-toolbar {
  margin-bottom: 0;
}

.work-fine-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.work-fine-column {
  padding: 16px;
  border-radius: 14px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.06);
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.04);
}

.work-fine-column.checkin {
  border-top: 3px solid #f59e0b;
}

.work-fine-column.checkout {
  border-top: 3px solid #6366f1;
}

.work-fine-column-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
  padding-bottom: 12px;
  border-bottom: 1px dashed rgba(0, 0, 0, 0.08);
}

.work-fine-column-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  border-radius: 12px;
  font-size: 22px;
  color: #fff;
}

.work-fine-column.checkin .work-fine-column-icon {
  background: linear-gradient(135deg, #fbbf24, #f59e0b);
}

.work-fine-column.checkout .work-fine-column-icon {
  background: linear-gradient(135deg, #818cf8, #6366f1);
}

.work-fine-column-header h5 {
  margin: 0 0 2px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}

.work-fine-column-header span {
  font-size: 12px;
  color: #909399;
}

.work-fine-cards {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.work-fine-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-radius: 10px;
  background: #f9fafb;
  border: 1px solid rgba(0, 0, 0, 0.05);
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.work-fine-card:hover {
  border-color: rgba(0, 136, 204, 0.25);
  box-shadow: 0 4px 12px rgba(0, 136, 204, 0.08);
}

.work-fine-card-main {
  display: flex;
  align-items: baseline;
  gap: 16px;
  flex-wrap: wrap;
}

.work-fine-tier {
  font-size: 13px;
  color: #606266;
}

.work-fine-amount {
  font-size: 18px;
  font-weight: 700;
  color: #e6a23c;
}

.work-fine-column.checkout .work-fine-amount {
  color: #6366f1;
}

.work-fine-delete {
  flex-shrink: 0;
}

.work-fine-empty {
  padding: 20px 12px;
  text-align: center;
  font-size: 13px;
  color: #909399;
  border-radius: 10px;
  background: rgba(0, 0, 0, 0.02);
  border: 1px dashed rgba(0, 0, 0, 0.08);
}

.activity-section,
.activity-fine-section {
  margin-bottom: 24px;
  padding: 20px;
  border-radius: 16px;
  background: linear-gradient(145deg, #f8fafc 0%, #f1f5f9 100%);
  border: 1px solid rgba(0, 0, 0, 0.06);
}

.activity-fine-section {
  margin-bottom: 0;
}

.activity-section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}

.activity-section-head .sub-block-title {
  margin-top: 0;
  border-left: none;
  padding-left: 0;
}

.activity-fine-filter {
  width: 160px;
}

.activity-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 14px;
  min-height: 60px;
}

.activity-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  border-radius: 14px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.06);
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.04);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.activity-card:hover {
  border-color: rgba(0, 136, 204, 0.25);
  box-shadow: 0 8px 24px rgba(0, 136, 204, 0.1);
  transform: translateY(-1px);
}

.activity-card.is-disabled {
  opacity: 0.72;
}

.activity-card-header {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.activity-card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 42px;
  height: 42px;
  border-radius: 12px;
  font-size: 20px;
  color: #fff;
  background: linear-gradient(135deg, #34d399, #10b981);
  flex-shrink: 0;
}

.activity-card-title {
  flex: 1;
  min-width: 0;
}

.activity-card-title h5 {
  margin: 0 0 4px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}

.activity-card-title code {
  font-size: 11px;
  color: #0088cc;
  background: rgba(0, 136, 204, 0.08);
  padding: 2px 6px;
  border-radius: 4px;
}

.activity-card-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #f9fafb;
}

.activity-stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.activity-stat span {
  font-size: 11px;
  color: #909399;
}

.activity-stat strong {
  font-size: 13px;
  color: #303133;
  font-weight: 600;
}

.activity-card-actions {
  display: flex;
  gap: 8px;
  padding-top: 4px;
  border-top: 1px dashed rgba(0, 0, 0, 0.06);
}

.activity-fine-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 14px;
  min-height: 60px;
}

.activity-fine-group-card {
  padding: 16px;
  border-radius: 14px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.06);
  border-top: 3px solid #f97316;
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.04);
}

.activity-fine-group-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  padding-bottom: 12px;
  border-bottom: 1px dashed rgba(0, 0, 0, 0.08);
}

.activity-fine-group-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 10px;
  font-size: 20px;
  color: #fff;
  background: linear-gradient(135deg, #fb923c, #f97316);
}

.activity-fine-group-header h5 {
  margin: 0 0 2px;
  font-size: 15px;
  font-weight: 600;
  color: #303133;
}

.activity-fine-group-header span {
  font-size: 12px;
  color: #909399;
}

.activity-amount {
  color: #f97316 !important;
}

.activity-empty {
  grid-column: 1 / -1;
  padding: 32px 16px;
  text-align: center;
  font-size: 13px;
  color: #909399;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.6);
  border: 1px dashed rgba(0, 0, 0, 0.08);
}

.activity-empty.wide {
  margin-top: 4px;
}

.handover-picker-section {
  margin-bottom: 16px;
}

.handover-picker-row {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
}

.handover-group-select {
  flex: 1;
  min-width: 280px;
  max-width: 480px;
}

.handover-selected-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 14px;
  padding: 10px 14px;
  border-radius: 10px;
  background: rgba(0, 136, 204, 0.08);
  border: 1px solid rgba(0, 136, 204, 0.15);
  font-size: 13px;
  color: #606266;
}

.handover-selected-banner strong {
  color: #0088cc;
}

.handover-config-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  margin-bottom: 20px;
}

.handover-panel,
.push-panel {
  margin-bottom: 0;
  height: fit-content;
}

.panel-title-with-icon {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.panel-icon-badge {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  font-size: 20px;
  color: #fff;
  flex-shrink: 0;
}

.panel-icon-badge.calendar {
  background: linear-gradient(135deg, #818cf8, #6366f1);
}

.panel-icon-badge.push {
  background: linear-gradient(135deg, #38bdf8, #0ea5e9);
}

.config-field-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.config-field-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px;
  border-radius: 12px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.06);
}

.config-field-card.switch-card {
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
}

.config-field-card.switch-card.is-on {
  border-color: rgba(99, 102, 241, 0.35);
  background: linear-gradient(145deg, #f5f3ff 0%, #ede9fe 100%);
}

.config-field-card.span-2 {
  grid-column: span 2;
}

.config-field-card label {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
}

.config-hint {
  font-size: 11px;
  color: #909399;
  line-height: 1.4;
}

.config-field-card :deep(.el-input),
.config-field-card :deep(.el-input-number) {
  width: 100%;
}

.push-target-cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 16px;
}

.push-id-hint {
  margin-bottom: 12px;
}

.push-target-card {
  padding: 14px;
  border-radius: 12px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.06);
}

.push-target-label {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
  margin-bottom: 8px;
}

.push-toggle-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.feature-card.compact-feature {
  padding: 14px;
  margin: 0;
}

.feature-card.compact-feature .feature-card-top {
  margin-bottom: 0;
}

.feature-card.compact-feature .feature-title {
  font-size: 14px;
}

.panel-empty {
  padding: 24px;
  text-align: center;
  font-size: 13px;
  color: #909399;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.5);
  border: 1px dashed rgba(0, 0, 0, 0.08);
}

.handover-empty-state {
  margin-bottom: 20px;
  padding: 20px;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.6);
  border: 1px dashed rgba(0, 0, 0, 0.08);
}

.tg-save-bar {
  position: fixed;
  left: 50%;
  bottom: 24px;
  transform: translateX(-50%);
  z-index: 100;
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 14px 22px;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.96);
  backdrop-filter: blur(16px);
  border: 1px solid rgba(0, 136, 204, 0.2);
  box-shadow: 0 12px 40px rgba(15, 23, 42, 0.15);
  max-width: calc(100vw - 32px);
}

.tg-save-hint {
  font-size: 13px;
  color: #606266;
  white-space: nowrap;
}

.tg-save-slide-enter-active,
.tg-save-slide-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}

.tg-save-slide-enter-from,
.tg-save-slide-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(16px);
}

.tg-dialog :deep(.el-dialog) {
  border-radius: 16px;
  overflow: hidden;
}

.tg-dialog :deep(.el-dialog__header) {
  padding: 18px 20px 12px;
  margin-right: 0;
  background: linear-gradient(135deg, rgba(34, 158, 217, 0.08), rgba(102, 126, 234, 0.08));
}

@media (max-width: 768px) {
  .tg-save-bar {
    flex-direction: column;
    align-items: stretch;
    text-align: center;
    bottom: 12px;
    padding: 12px 16px;
  }

  .tg-save-hint {
    white-space: normal;
  }

  .work-fine-columns {
    grid-template-columns: 1fr;
  }

  .activity-card-stats {
    grid-template-columns: 1fr;
  }

  .handover-config-grid {
    grid-template-columns: 1fr;
  }

  .config-field-grid {
    grid-template-columns: 1fr;
  }

  .config-field-card.span-2 {
    grid-column: span 1;
  }

  .push-toggle-grid {
    grid-template-columns: 1fr;
  }
}

.shared-ws-device-option {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;
  padding: 2px 0;
}

.shared-ws-device-option-main {
  font-size: 13px;
  color: #303133;
}

.shared-ws-device-option-id {
  font-size: 11px;
  color: #909399;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
</style>
