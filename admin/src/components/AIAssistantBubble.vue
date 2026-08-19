<template>
  <div class="ai-assistant" data-testid="ai-assistant">
    <button v-if="!open" class="assistant-trigger" type="button" title="打开 AI 助手" aria-label="打开 AI 助手" @click="openAssistant">
      <el-icon><ChatDotRound /></el-icon>
    </button>

    <section v-else class="assistant-panel" role="dialog" aria-label="AI 助手">
      <header class="assistant-header">
        <div>
          <div class="assistant-title-row">
            <strong>AI 助手</strong>
            <span class="status-dot" :class="{ active: availability?.enabled, busy: loading || confirming }" aria-hidden="true"></span>
          </div>
          <span>{{ statusLine }}</span>
        </div>
        <button class="icon-button" type="button" title="关闭" aria-label="关闭" @click="open = false">
          <el-icon><Close /></el-icon>
        </button>
      </header>

      <div class="assistant-body">
        <el-alert v-if="availability && !availability.enabled" class="availability-alert" type="warning" :closable="false" show-icon>
          {{ availability.reason || '平台 AI 尚未启用' }}
        </el-alert>
        <div v-if="errorMessage" class="assistant-error" role="alert">
          <el-icon class="error-icon"><WarningFilled /></el-icon>
          <div class="error-copy">
            <strong>{{ errorTitle }}</strong>
            <span>{{ errorMessage }}</span>
          </div>
          <el-button text size="small" :icon="errorKind === 'auth' ? undefined : Refresh" @click="retryLastAction">{{ errorActionLabel }}</el-button>
        </div>

        <template v-if="!task?.preview">
          <div v-if="!task && !errorMessage" class="assistant-intro">
            <strong>描述你要完成的操作</strong>
            <span>我会先整理成计划，确认后才执行。</span>
          </div>
          <label class="field-label" for="ai-assistant-input">操作描述</label>
          <el-input
            id="ai-assistant-input"
            v-model="inputText"
            type="textarea"
            :rows="5"
            maxlength="2000"
            show-word-limit
            :placeholder="task ? '继续补充缺失信息，例如：窗口票，售价 120 元，结算价 80 元' : '例如：创建一个线上成人票，售价 120 元，结算价 80 元，使用北门检票点'"
            :disabled="loading || taskLoading || (availability && !availability.enabled)"
            @keyup.ctrl.enter="submitInput"
            @keyup.meta.enter="submitInput"
          />
          <div v-if="taskLoading" class="assistant-progress"><el-icon class="is-loading"><Loading /></el-icon>正在恢复未完成任务</div>
          <div v-if="task?.message" class="assistant-message" role="status">
            <strong>AI 返回</strong>
            <p>{{ task.message }}</p>
          </div>
          <section v-if="queryResults.length" class="query-result-panel" aria-label="服务器查询结果">
            <div class="query-result-heading">
              <div>
                <strong>服务器查询结果</strong>
                <span>以下内容由系统直接生成</span>
              </div>
              <el-tag size="small" type="success" effect="plain">只读</el-tag>
            </div>
            <article v-for="result in queryResults" :key="queryResultKey(result)" class="query-result-card">
              <div class="query-result-card-heading">
                <strong>{{ queryToolLabel(result.tool) }}</strong>
                <span>{{ queryAsOfText(result.as_of) }}</span>
              </div>
              <div class="query-result-meta">
                <span>返回 {{ result.returned ?? 0 }} 条</span>
                <span>匹配 {{ result.total ?? result.returned ?? 0 }} 条</span>
                <span>{{ result.has_more ? '结果已截断，请缩小范围' : '已展示当前结果' }}</span>
              </div>
              <dl v-if="queryFilterEntries(result).length" class="query-filter-list">
                <template v-for="filter in queryFilterEntries(result)" :key="filter.key">
                  <dt>{{ queryFieldLabel(filter.key) }}</dt>
                  <dd>{{ formatQueryValue(filter.value) }}</dd>
                </template>
              </dl>
              <div v-if="queryRows(result).length" class="query-row-list">
                <div v-for="(row, rowIndex) in queryRows(result)" :key="rowIndex" class="query-row">
                  <template v-for="field in queryRowEntries(row)" :key="field.key">
                    <span>{{ queryFieldLabel(field.key) }}</span>
                    <strong>{{ formatQueryValue(field.value) }}</strong>
                  </template>
                </div>
              </div>
              <p v-else class="query-empty">没有符合当前筛选条件的数据。</p>
            </article>
          </section>
          <div v-if="task?.missing_fields?.length" class="missing-list">
            <div v-for="field in task.missing_fields" :key="field.field" class="missing-item">
              <strong>{{ field.label }}</strong><span>{{ field.question }}</span>
              <small v-if="field.options?.length">可选：{{ field.options.join('、') }}</small>
            </div>
          </div>
          <div class="assistant-hint">{{ queryResults.length ? '查询只读取当前租户的服务器事实，不会修改业务数据。' : '确认前不会修改票种、规则或分销数据。' }}</div>
          <div class="assistant-actions">
            <div class="action-meta">
              <span v-if="availability" class="quota-copy">本月剩余 {{ availability.requests_remaining }} 次请求</span>
              <el-button v-if="loading && loadingSeconds >= 20" text size="small" @click="stopWaiting">停止等待</el-button>
              <el-button v-if="task || errorMessage" text size="small" :disabled="loading || confirming || taskLoading" @click="startNewTask">新建任务</el-button>
              <el-button v-if="inputText.trim()" text size="small" @click="clearInput">清空</el-button>
            </div>
            <el-button type="primary" :icon="Promotion" :loading="loading" :disabled="!inputText.trim() || taskLoading || (availability && !availability.enabled)" @click="submitInput">{{ task ? '继续处理' : '生成计划' }}</el-button>
          </div>
        </template>

        <template v-else>
          <div class="preview-meta">
            <span>{{ providerLabel }} · 任务 #{{ task.task_id }}</span>
            <el-tag size="small" :type="task.can_confirm ? 'warning' : 'info'" effect="plain">{{ statusText(task.state) }}</el-tag>
          </div>
          <p class="original-request">“{{ task.input_text }}”</p>
          <template v-if="isCompoundPreview">
            <div class="operation-summary compound-summary">
              <div><span>顺序步骤</span><strong>{{ preview.step_count || preview.steps?.length || 0 }} 步</strong></div>
              <div><span>执行方式</span><strong>一次确认后顺序执行</strong></div>
            </div>
            <div class="compound-step-list">
              <article v-for="step in preview.steps || []" :key="step.index" class="compound-step">
                <div class="diff-heading">
                  <strong>步骤 {{ step.index }} · {{ compoundOperationLabel(step.operation_type) }}</strong>
                  <el-tag size="small" effect="plain" type="info">{{ statusText(step.status) }}</el-tag>
                </div>
                <div class="compound-step-summary">{{ compoundStepSummary(step.preview) }}</div>
              </article>
            </div>
            <ul v-if="preview.safety?.length" class="assumption-list">
              <li v-for="item in preview.safety" :key="item">{{ item }}</li>
            </ul>
            <p class="compound-execution-note">步骤之间不是同一事务。若后续步骤失败，已完成的步骤会保留，并只恢复未完成步骤。</p>
          </template>
          <template v-else-if="isProductBatchUpdatePreview">
            <div class="operation-summary product-batch-summary">
              <div><span>批量票种</span><strong>{{ preview.product_count || preview.lines?.length || 0 }} 个</strong></div>
              <div><span>共同变更</span><strong>{{ preview.changes?.length || 0 }} 项</strong></div>
            </div>
            <div class="batch-update-list">
              <article v-for="line in preview.lines || []" :key="line.product_name" class="diff-row">
                <div class="diff-heading"><strong>{{ line.product_name }}</strong><span>{{ line.scenic_area_name || '-' }}</span></div>
                <div v-for="row in productUpdateRows" :key="`${line.product_name}-${row.key}`" class="product-update-row">
                  <span class="product-update-label">{{ row.label }}</span>
                  <div class="product-update-values">
                    <div><small>变更前</small><strong>{{ row.format(line.before) }}</strong></div>
                    <el-icon><Right /></el-icon>
                    <div><small>变更后</small><strong>{{ row.format(line.after) }}</strong></div>
                  </div>
                </div>
              </article>
            </div>
            <ul v-if="preview.safety?.length" class="assumption-list">
              <li v-for="item in preview.safety" :key="item">{{ item }}</li>
            </ul>
          </template>
          <template v-else-if="isProductPreview">
            <div class="product-summary">
              <div><span>票种名称</span><strong>{{ productPreview.product?.name }}</strong></div>
              <div><span>所属景区</span><strong>{{ productPreview.scenic_area_name || '-' }}</strong></div>
              <div><span>售价 / 结算价</span><strong>{{ money(productPreview.product?.price) }} / {{ money(productPreview.product?.settlement_price) }}</strong></div>
              <div><span>上架与分销</span><strong>{{ productPreview.product?.status_label || '未上架' }} · {{ productPreview.product?.is_distributable ? '允许分销' : '不分销' }}</strong></div>
              <div><span>票种类型</span><strong>{{ productPreview.product?.type_label || productPreview.product?.type || '-' }}</strong></div>
            </div>
            <div class="product-detail-grid">
              <div><span>有效期</span><strong>{{ validityText(productPreview.product) }}</strong></div>
              <div><span>库存</span><strong>{{ stockText(productPreview.product) }}</strong></div>
              <div><span>实名与限购</span><strong>{{ identityLimitText(productPreview.product) }}</strong></div>
              <div><span>退款规则</span><strong>{{ refundText(productPreview.product) }}</strong></div>
              <div><span>出票方式</span><strong>{{ productPreview.product?.code_mode || '-' }}</strong></div>
              <div><span>闸机语音</span><strong>{{ productPreview.product?.gate_voice_code || '-' }}</strong></div>
              <div class="wide"><span>标签</span><strong>{{ productPreview.product?.tags || '-' }}</strong></div>
              <div class="wide"><span>其他退款说明</span><strong>{{ productPreview.product?.refund_rule || '-' }}</strong></div>
            </div>
            <div v-if="!isProductUpdatePreview" class="rule-preview">
              <div class="diff-heading"><strong>{{ productPreview.rule?.name || '检票规则' }}</strong><span>{{ productPreview.rule?.validity_type || 'date' }}</span></div>
              <div v-for="group in productPreview.rule_groups || []" :key="group.group_name" class="rule-group">
                <span>{{ group.group_name || '默认分组' }}</span>
                <code>{{ ruleItems(group) }}</code>
              </div>
            </div>
            <ul v-if="productPreview.assumptions?.length" class="assumption-list">
              <li v-for="assumption in productPreview.assumptions" :key="assumption">{{ assumption }}</li>
            </ul>
            <div v-if="isProductUpdatePreview" class="product-update-diff">
              <div class="diff-heading"><strong>基础信息变更</strong><span>确认后生成新版本</span></div>
              <div v-for="row in productUpdateRows" :key="row.key" class="product-update-row">
                <span class="product-update-label">{{ row.label }}</span>
                <div class="product-update-values">
                  <div><small>变更前</small><strong>{{ row.format(preview.before) }}</strong></div>
                  <el-icon><Right /></el-icon>
                  <div><small>变更后</small><strong>{{ row.format(preview.after) }}</strong></div>
                </div>
              </div>
              <ul v-if="preview.safety?.length" class="assumption-list">
                <li v-for="item in preview.safety" :key="item">{{ item }}</li>
              </ul>
            </div>
          </template>
          <template v-else-if="isHotelPreview">
            <div class="hotel-preview-heading">
              <div>
                <strong>{{ hotelOperationLabel }}</strong>
                <span>{{ hotelPreviewScope }}</span>
              </div>
              <el-tag size="small" type="info" effect="plain">酒店业务</el-tag>
            </div>
            <div v-if="hotelPreviewLines.length" class="hotel-preview-list">
              <article v-for="line in hotelPreviewLines" :key="line.stay_date" class="hotel-preview-row">
                <div class="diff-heading">
                  <strong>{{ line.stay_date }}</strong>
                  <span>{{ hotelLineChangeText(line) }}</span>
                </div>
                <div class="hotel-preview-values">
                  <div><small>变更前</small><strong>{{ hotelLineBeforeText(line) }}</strong></div>
                  <el-icon><Right /></el-icon>
                  <div><small>变更后</small><strong>{{ hotelLineAfterText(line) }}</strong></div>
                </div>
              </article>
            </div>
            <div v-else-if="isHotelReservationStatusPreview" class="hotel-status-diff">
              <div><span>住宿预订号</span><strong>{{ preview.reservation_no || '-' }}</strong></div>
              <div><span>酒店 / 房型</span><strong>{{ preview.hotel_name || '-' }} · {{ preview.room_type_name || '-' }}</strong></div>
              <div><span>入住日期</span><strong>{{ preview.check_in_date || '-' }} 至 {{ preview.check_out_date || '-' }}</strong></div>
              <div><span>当前状态</span><strong>{{ hotelStatusLabel(preview.before_status) }}</strong></div>
              <div><span>登记为</span><strong>{{ hotelStatusLabel(preview.after_status) }}</strong></div>
              <div v-if="preview.reason" class="wide"><span>原因</span><strong>{{ preview.reason }}</strong></div>
            </div>
            <ul v-if="preview.safety?.length" class="assumption-list">
              <li v-for="item in preview.safety" :key="item">{{ item }}</li>
            </ul>
          </template>
          <template v-else>
            <div class="operation-summary">
              <div><span>规范化操作</span><strong>{{ preview.operations?.length || 0 }} 项</strong></div>
              <div><span>影响票种</span><strong>{{ preview.lines?.length || 0 }} 个</strong></div>
            </div>
            <div class="diff-list">
              <article v-for="line in preview.lines" :key="line.line_id" class="diff-row">
                <div class="diff-heading">
                  <strong>{{ line.product_name }}</strong>
                  <span>版本 {{ line.before_revision_id }}<template v-if="line.after_revision_id"> → {{ line.after_revision_id }}</template></span>
                </div>
                <div class="diff-values">
                  <div class="diff-side"><small>变更前</small><div v-if="parseRuleSnapshot(line.before_json)" class="rule-snapshot">
                    <div v-for="group in parseRuleSnapshot(line.before_json) || []" :key="group.key" class="rule-snapshot-group">
                      <div class="rule-snapshot-heading"><strong>{{ group.group_name }}</strong><span>{{ ruleGroupMode(group) }}</span></div>
                      <div v-for="item in group.items" :key="`${group.key}-${item.checkpoint_id || item.checkpoint_name}`" class="rule-snapshot-item"><span>{{ item.checkpoint_name }}</span><span>×{{ item.max_per_check_in }}</span></div>
                      <div v-if="!group.items.length" class="rule-snapshot-empty">暂无检票点</div>
                    </div>
                  </div><code v-else>{{ line.before_json || '-' }}</code></div>
                  <el-icon><Right /></el-icon>
                  <div class="diff-side"><small>变更后</small><div v-if="parseRuleSnapshot(line.after_json)" class="rule-snapshot">
                    <div v-for="group in parseRuleSnapshot(line.after_json) || []" :key="group.key" class="rule-snapshot-group">
                      <div class="rule-snapshot-heading"><strong>{{ group.group_name }}</strong><span>{{ ruleGroupMode(group) }}</span></div>
                      <div v-for="item in group.items" :key="`${group.key}-${item.checkpoint_id || item.checkpoint_name}`" class="rule-snapshot-item"><span>{{ item.checkpoint_name }}</span><span>×{{ item.max_per_check_in }}</span></div>
                      <div v-if="!group.items.length" class="rule-snapshot-empty">暂无检票点</div>
                    </div>
                  </div><code v-else>{{ line.after_json || '-' }}</code></div>
                </div>
                <p v-if="line.error_message" class="line-error">{{ line.error_message }}</p>
              </article>
            </div>
          </template>
          <div class="assistant-hint">确认后服务端会重新校验租户归属和当前数据；预览过期或数据变化时会拒绝执行，需重新生成预览。</div>
          <div class="assistant-actions preview-actions">
            <el-button :icon="Refresh" @click="startNewTask">新建任务</el-button>
            <el-button type="primary" :icon="CircleCheck" :loading="confirming" :disabled="!task.can_confirm" @click="confirmTask">确认执行</el-button>
          </div>
        </template>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ChatDotRound, CircleCheck, Close, Loading, Promotion, Refresh, Right, WarningFilled } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import request from '@/utils/request'
import { localizeErrorMessage } from '@/utils/localize'
import { parseRuleSnapshot, ruleGroupMode } from '@/utils/ruleSnapshot'

type ErrorKind = 'auth' | 'timeout' | 'provider' | 'conflict' | 'in_progress' | 'generic' | ''
type AgentAction = 'submit' | 'confirm' | 'cancel' | ''

const AI_STATUS_TIMEOUT_MS = 15_000
// Keep a small client-side buffer above the server's bounded 115-second
// planning lifetime. The user can stop waiting earlier without confirming or
// changing any domain record.
const AI_TASK_TIMEOUT_MS = 135_000

const agentSessionID = (() => {
  const storageKey = 'ticket-agent-session-id'
  try {
    const existing = sessionStorage.getItem(storageKey)
    if (existing) return existing
    const created = newKey()
    sessionStorage.setItem(storageKey, created)
    return created
  } catch {
    return newKey()
  }
})()
const taskStorageKey = `ticket-agent-task-id:${agentSessionID}`
const legacyTaskStorageKey = 'ticket-agent-task-id'

const open = ref(false)
const inputText = ref('')
const loading = ref(false)
const loadingSeconds = ref(0)
const confirming = ref(false)
const statusLoading = ref(false)
const taskLoading = ref(false)
const availability = ref<any | null>(null)
const task = ref<any | null>(null)
const errorMessage = ref('')
const errorKind = ref<ErrorKind>('')
const lastAction = ref<AgentAction>('')
const idempotencyKey = ref(newKey())
const lastSubmittedInput = ref('')
let loadingTimer: ReturnType<typeof setInterval> | undefined
let activeRequestController: AbortController | undefined
let stoppingRequest = false
const router = useRouter()

const statusLine = computed(() => {
  if (loading.value) {
    if (loadingSeconds.value >= 45) return `模型响应较慢，已等待 ${loadingSeconds.value} 秒`
    if (loadingSeconds.value >= 15) return `正在等待模型响应（${loadingSeconds.value} 秒）`
    return '正在整理操作计划'
  }
  if (confirming.value) return '正在执行已确认计划'
  if (statusLoading.value) return '正在读取平台额度'
  if (!availability.value) return '正在读取平台额度'
  if (!availability.value.enabled) return '当前不可用'
  if (task.value?.state === 'awaiting_confirmation') return '等待确认执行'
  if (task.value?.state === 'executing') return '正在恢复已确认操作'
  if (task.value?.message && !task.value?.missing_fields?.length) return '已返回查询结果'
  if (task.value?.state === 'collecting') return '等待补充信息'
  if (task.value?.state === 'expired') return '任务已过期，可新建任务'
  if (task.value?.state === 'cancelled') return '任务已放弃'
  if (task.value?.state === 'failed') return '任务失败，可重试或新建任务'
  return `${availability.value.provider || '平台模型'} · 剩余 ${availability.value.requests_remaining} 次`
})
const errorTitle = computed(() => {
  if (errorKind.value === 'auth') return '登录状态已失效'
  if (errorKind.value === 'timeout') return '模型响应超时'
  if (errorKind.value === 'provider') return '模型服务暂时不可用'
  if (errorKind.value === 'in_progress') return '任务正在处理中'
  if (errorKind.value === 'conflict') return '预览已失效'
  return '这次请求没有完成'
})
const errorActionLabel = computed(() => {
  if (errorKind.value === 'auth') return '重新登录'
  if (errorKind.value === 'conflict') return '重新预览'
  return '重试'
})
const providerLabel = computed(() => `${task.value?.provider || availability.value?.provider || '平台模型'}${task.value?.model ? ` / ${task.value.model}` : ''}`)
const preview = computed(() => task.value?.preview || {})
const queryResults = computed(() => {
  const result = parseJSONValue(task.value?.result)
  if (Array.isArray(result?.query_results)) return result.query_results.filter(isQueryResult)
  return isQueryResult(result) ? [result] : []
})
const isCompoundPreview = computed(() => preview.value?.operation_type === 'compound_preview')
const isProductUpdatePreview = computed(() => preview.value?.operation_type === 'ticket_product_update')
const isProductBatchUpdatePreview = computed(() => preview.value?.operation_type === 'ticket_product_batch_update')
const hotelOperationTypes = new Set(['hotel_inventory_change', 'hotel_rate_calendar_change', 'hotel_product_calendar_change', 'hotel_reservation_status_change'])
const isHotelPreview = computed(() => hotelOperationTypes.has(String(preview.value?.operation_type || '')))
const isHotelReservationStatusPreview = computed(() => preview.value?.operation_type === 'hotel_reservation_status_change')
const hotelOperationLabel = computed(() => {
  switch (preview.value?.operation_type) {
    case 'hotel_inventory_change': return '设置酒店房量'
    case 'hotel_rate_calendar_change': return '设置房型价格日历'
    case 'hotel_product_calendar_change': return '设置酒店产品价格日历'
    case 'hotel_reservation_status_change': return '登记住宿履约状态'
    default: return '酒店操作预览'
  }
})
const hotelPreviewScope = computed(() => {
  const current = preview.value || {}
  if (current.operation_type === 'hotel_inventory_change') return `${current.hotel_name || '-'} · ${current.room_type_name || '-'}`
  if (current.operation_type === 'hotel_rate_calendar_change') return `${current.hotel_name || '-'} · ${current.scope_name || '-'}（价格计划）`
  if (current.operation_type === 'hotel_product_calendar_change') return `${current.hotel_name || '-'} · ${current.scope_name || '-'}（日历房产品）`
  return `${current.hotel_name || '-'} · ${current.room_type_name || '-'}`
})
const hotelPreviewLines = computed(() => Array.isArray(preview.value?.lines) ? preview.value.lines : [])
const hotelMoney = (value: any) => typeof value === 'number' ? `¥${value.toFixed(2)}` : '-'
const hotelStatusLabel = (value: any) => ({ reserved: '已预约', confirmed: '已确认', checked_in: '已入住', checked_out: '已离店', no_show: '未到店', cancelled: '已取消', refunded: '已退款' } as Record<string, string>)[String(value || '')] || String(value || '-')
const hotelLineBeforeText = (line: any) => {
  const before = line?.before || {}
  if (preview.value?.operation_type === 'hotel_inventory_change') return `房量 ${before.capacity ?? 0} · 可售 ${before.available ?? 0} · ${before.closed ? '已关房' : '营业'}`
  return `${hotelMoney(before.retail_price)} / 结算 ${hotelMoney(before.settlement_price)} · ${before.source === 'override' ? '日期覆盖' : '基础价'}`
}
const hotelLineAfterText = (line: any) => {
  const after = line?.after || {}
  if (preview.value?.operation_type === 'hotel_inventory_change') return `房量 ${after.capacity ?? 0} · 可售 ${after.available ?? 0} · ${after.closed ? '已关房' : '营业'}`
  return `${hotelMoney(after.retail_price)} / 结算 ${hotelMoney(after.settlement_price)} · ${after.source === 'override' ? '日期覆盖' : '基础价'}`
}
const hotelLineChangeText = (line: any) => {
  if (preview.value?.operation_type === 'hotel_inventory_change') {
    const changes = Object.entries(line?.change || {}).filter(([, changed]) => changed).map(([key]) => key === 'capacity' ? '房量' : '关房')
    return changes.join('、') || '无变化'
  }
  return '入住日价格'
}
const isAnyProductUpdatePreview = computed(() => isProductUpdatePreview.value || isProductBatchUpdatePreview.value)
const productPreview = computed(() => {
  if (preview.value?.operation_type === 'ticket_product_create') return preview.value
  if (isProductUpdatePreview.value) return { ...preview.value, product: preview.value.after }
  return {}
})
const isProductPreview = computed(() => preview.value?.operation_type === 'ticket_product_create' || isProductUpdatePreview.value)
const productUpdateRows = computed(() => {
  if (!isAnyProductUpdatePreview.value) return []
  const labels: Array<{ key: string, label: string, format: (product: any) => string }> = [
    { key: 'name', label: '票种名称', format: (product) => product?.name || '-' },
    { key: 'price', label: '售价', format: (product) => money(product?.price) },
    { key: 'settlement_price', label: '结算价', format: (product) => money(product?.settlement_price) },
    { key: 'validity', label: '有效期', format: (product) => validityText(product) },
    { key: 'stock', label: '库存', format: (product) => stockText(product) },
    { key: 'identity', label: '实名与限购', format: (product) => identityLimitText(product) },
    { key: 'refund', label: '退款规则', format: (product) => refundText(product) },
    { key: 'code_mode', label: '出票方式', format: (product) => product?.code_mode || '-' },
    { key: 'gate_voice_code', label: '闸机语音', format: (product) => product?.gate_voice_code || '-' },
    { key: 'tags', label: '标签', format: (product) => product?.tags || '-' },
  ]
  const changed = new Set(preview.value?.changes || [])
  return labels.filter((row) => changed.has(row.label))
})

function newKey() {
  try {
    if (globalThis.crypto?.randomUUID) return `catalog-ai-${globalThis.crypto.randomUUID()}`
  } catch { /* older browsers may not expose crypto.randomUUID */ }
  return `catalog-ai-${Date.now()}-${Math.random().toString(36).slice(2, 11)}`
}
const queryToolLabel = (tool: unknown) => ({
  search_scenic_areas: '景区查询',
  search_checkpoints: '检票点查询',
  search_ticket_products: '票种查询',
  get_ticket_product_rules: '票种规则查询',
  search_orders: '订单查询',
  query_ticket_inventory: '票种库存查询',
  query_sales_summary: '销售汇总查询',
  query_verification_summary: '核销汇总查询',
  query_distribution_partners: '合作供应商查询',
  query_distribution_products: '授权商品查询',
  query_distribution_fulfillments: '供应商履约查询',
  query_distribution_settlements: '分销结算查询',
  query_team_contracts: '团队合同查询',
  query_team_groups: '团队计划查询',
  query_team_settlement_summary: '团队结算查询',
  query_team_account_summary: '团队账户查询',
  search_hotel_catalog: '酒店目录查询',
  query_hotel_inventory: '酒店房量查询',
  query_hotel_rate_calendar: '价格计划日历查询',
  query_hotel_product_calendar: '酒店产品售价日历查询',
  query_hotel_reservations: '住宿预订查询',
  query_hotel_booking_entitlements: '住宿预约权益查询',
  query_hotel_business_summary: '酒店经营汇总查询',
  query_compound_readonly: '复合只读查询',
} as Record<string, string>)[String(tool || '')] || '业务查询'
const queryFieldLabel = (key: string) => ({
  query: '关键词', search: '关键词', product_name: '票种', status: '状态', channel: '渠道',
  start_date: '开始日期', end_date: '结束日期', stock_slot: '时段', period_rule: '统计口径',
  order_no: '订单号', order_status: '订单状态', order_date: '下单时间', paid_at: '支付时间',
  amount: '金额', paid_amount: '实付金额', refund_amount: '退款金额', net_amount: '净额',
  product_type: '票种类型', product_status: '票种状态', type: '类型',
  price: '售价', quantity: '数量', items: '订单明细', ticket_count: '票数',
  stock_date: '日期', slot: '时段', capacity: '库存', sold: '已售', remaining: '剩余',
  date: '日期', sales_count: '售券数', refund_count: '退款数', verification_count: '核销数',
  income_cents: '核销收入', scenic_area_name: '景区', checkpoint_name: '检票点',
  location: '位置', code: '编号', is_distributable: '允许分销',
  supplier_name: '供应商', counterparty_name: '合作方', relationship_status: '合作状态', agent_level: '代理等级', applied_at: '申请时间',
  listing_status: '铺货状态', offer_status: '授权状态', retail_price: '零售价', sales_start_date: '销售开始', sales_end_date: '销售结束',
  allowed_channels: '允许渠道', currently_sellable: '当前可售', sales_order_no: '销售订单号', fulfillment_no: '履约单号',
  settlement_state: '结算状态', used_count: '已核销', created_at: '创建时间', statement_no: '结算单号',
  contract_no: '合同号', group_no: '团号', group_name: '团队名称', visit_date: '到园日期', expected_count: '预计人数',
  settlement_days: '结算账期', credit_limit_cents: '授信额度', price_rule_count: '价目条数', starts_at: '生效日期', ends_at: '截止日期',
  settlement_status: '结算状态', admission_batch_count: '入园批次', admitted_count: '已入园', confirmation_count: '确认次数',
  latest_confirmed_count: '最近确认人数', supplier_acknowledged: '供应商已确认', kind: '结算类型', gross_cents: '应收金额',
  refund_cents: '退款冲减', deposit_cents: '预付款', due_date: '到期日期', completed_at: '完成时间',
  hotel_name: '酒店', room_type_name: '房型', rate_plan_name: '价格计划', reservation_no: '预订号',
  check_in_date: '入住日期', check_out_date: '离店日期', sale_mode: '销售模式', name: '名称',
  source: '价格来源', base_retail_price: '基础售价', base_settlement_price: '基础结算价',
  has_override: '有入住日覆盖价', reserved: '已预留', available_after_reserved: '扣除预留后可售', closed: '关房',
  business_type: '业务类型', sales_amount: '售券金额', booking_amount: '预约金额', stay_amount: '入住金额',
} as Record<string, string>)[key] || key.replace(/_/g, ' ')
const isSafeQueryField = (key: string) => !/(^id$|_id$|tenant|user|password|token|secret|api[_-]?key|identity|id_number|phone|mobile)/i.test(key)
const queryEntries = (value: unknown) => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return [] as Array<{ key: string, value: unknown }>
  return Object.entries(value as Record<string, unknown>)
    .filter(([key]) => isSafeQueryField(key))
    .map(([key, fieldValue]) => ({ key, value: fieldValue }))
}
const queryFilterEntries = (result: any) => queryEntries(result?.filters)
const queryRowEntries = (row: unknown) => queryEntries(row)
const queryRows = (result: any) => {
  const data = result?.data
  if (Array.isArray(data)) return data
  if (data && typeof data === 'object' && Array.isArray((data as any).rows)) return (data as any).rows
  return data && typeof data === 'object' ? [data] : []
}
const formatQueryValue = (value: unknown): string => {
  if (value === undefined || value === null || value === '') return '-'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return Number.isFinite(value) ? String(value) : '-'
  if (typeof value === 'string') return value
  if (Array.isArray(value)) return value.slice(0, 6).map(formatQueryValue).join('、') || '-'
  if (typeof value === 'object') return queryEntries(value).slice(0, 6).map(field => `${queryFieldLabel(field.key)}：${formatQueryValue(field.value)}`).join('；') || '-'
  return String(value)
}
const queryResultKey = (result: any) => `${result?.tool || 'result'}-${result?.as_of || ''}`
const queryAsOfText = (value: unknown) => {
  const time = new Date(String(value || ''))
  if (!Number.isFinite(time.getTime())) return '生成时间未知'
  return `生成于 ${time.toLocaleString('zh-CN', { hour12: false })}`
}
const parseJSONValue = (value: unknown): any => {
  if (typeof value !== 'string') return value
  try { return JSON.parse(value) } catch { return null }
}
const isQueryResult = (value: any) => Boolean(value && typeof value === 'object' && value.schema_version && value.tool && Object.prototype.hasOwnProperty.call(value, 'data'))
const statusText = (status: string) => ({ awaiting_confirmation: '待确认', completed: '已完成', collecting: '补充信息', expired: '已过期', failed: '执行失败', cancelled: '已放弃' } as Record<string, string>)[status] || status
const money = (value: any) => value === undefined || value === null ? '-' : `¥${Number(value).toFixed(2)}`
const validityText = (product: any) => {
  if (!product) return '-'
  if (product.validity_type === 'days') return `购买后 ${product.validity_days || 0} 天`
  if (product.validity_start_date || product.validity_end_date) return `${product.validity_start_date || '不限'} 至 ${product.validity_end_date || '不限'}`
  return '按日期（未设置固定日期）'
}
const stockText = (product: any) => {
  if (!product) return '-'
  if (product.stock_type === 'unlimited') return '不限库存'
  return `${product.stock_type || '-'} · ${product.daily_stock || 0}`
}
const identityLimitText = (product: any) => {
  if (!product) return '-'
  const identity = product.real_name_required ? '实名' : '非实名'
  const phone = product.limit_per_phone > 0 ? `手机号 ${product.limit_per_phone}` : '手机号不限'
  const id = product.limit_per_id > 0 ? `证件 ${product.limit_per_id}` : '证件不限'
  return `${identity} · ${phone} · ${id}`
}
const refundText = (product: any) => ({ no_refund: '不可退', free: '未核销随时退', ladder: '阶梯退款' } as Record<string, string>)[product?.refund_type] || product?.refund_type || '未设置'
const compoundOperationLabel = (operationType: string) => ({
  catalog_batch_change: '检票规则调整',
  ticket_product_create: '创建未上线票种',
  ticket_product_update: '修改票种基础信息',
  ticket_product_batch_update: '批量修改票种基础信息',
} as Record<string, string>)[operationType] || operationType
const compoundStepSummary = (step: any) => {
  if (!step) return '预览内容不可用'
  if (step.product_count) return `影响 ${step.product_count} 个票种，变更 ${step.changes?.length || 0} 项`
  if (step.lines?.length) return `影响 ${step.lines.length} 个票种规则`
  if (step.product?.name) return `票种：${step.product.name} · ${step.product?.type_label || step.product?.type || '未上架'}`
  return '已生成服务器预览，确认时会再次校验当前数据'
}
const ruleItems = (group: any) => {
  const total = group?.max_total_check_in > 0 ? `总计最多 ${group.max_total_check_in} 次` : '总次数不限'
  const items = (group?.items || []).map((item: any) => `${item.checkpoint_name || `#${item.checkpoint_id}`} ×${item.max_per_check_in}`).join('、') || '-'
  return `${total}；${items}`
}
const loadStatus = async () => {
  statusLoading.value = true
  try {
    // Availability is scoped under the durable agent task resource. Keep the
    // UI endpoint aligned with the protected backend route so a healthy
    // tenant session does not look unavailable because of a 404.
    availability.value = (await request.get('/agent/tasks/availability', { timeout: AI_STATUS_TIMEOUT_MS, skipErrorToast: true } as any)).data
  } catch {
    availability.value = { enabled: false, reason: '无法读取平台 AI 状态，请稍后重试' }
  } finally {
    statusLoading.value = false
  }
}

const openAssistant = async () => {
  if (open.value) return
  open.value = true
  await loadStatus()
  let savedTaskID = sessionStorage.getItem(taskStorageKey)
  // Migrate the pointer written by pre-sessionStorage releases once. The
  // server still rechecks tenant and actor ownership before returning a task;
  // after migration, each tab keeps its own current-task pointer.
  if (!savedTaskID) {
    try {
      savedTaskID = localStorage.getItem(legacyTaskStorageKey)
      if (savedTaskID) {
        sessionStorage.setItem(taskStorageKey, savedTaskID)
        localStorage.removeItem(legacyTaskStorageKey)
      }
    } catch {
      savedTaskID = null
    }
  }
  if (savedTaskID && !task.value) {
    taskLoading.value = true
    try {
      task.value = (await request.get(`/agent/tasks/${savedTaskID}`, { timeout: AI_STATUS_TIMEOUT_MS, skipErrorToast: true } as any)).data
    } catch {
      sessionStorage.removeItem(taskStorageKey)
    } finally {
      taskLoading.value = false
    }
  }
}

const setError = (error: any, fallback: string, action: AgentAction) => {
  const status = error?.response?.status
  const code = String(error?.response?.data?.code || '')
  const message = String(error?.response?.data?.error || error?.message || '')
  const isTimeout = status === 408 || status === 504 || /timeout|deadline exceeded|超时|ECONNABORTED/i.test(message)
  if (status === 401 || /unauthorized|invalid token|登录状态已失效/i.test(message)) errorKind.value = 'auth'
  else if (isTimeout) errorKind.value = 'timeout'
  else if (code === 'task_in_progress') errorKind.value = 'in_progress'
  else if (status === 409 || code === 'task_conflict') {
    errorKind.value = 'conflict'
    if (action === 'confirm') {
      errorMessage.value = '当前预览依据的数据已变化，不能直接重复确认。请重新生成预览后再确认。'
      lastAction.value = action
      return
    }
  }
  else if (status === 502 || status === 503 || code === 'ai_unavailable' || /AI provider|AI 服务/i.test(message)) errorKind.value = 'provider'
  else errorKind.value = 'generic'
  lastAction.value = action
  errorMessage.value = localizeErrorMessage(message, fallback)
}

const retryLastAction = () => {
  if (errorKind.value === 'auth') {
    const loginPath = (() => {
      try { return JSON.parse(localStorage.getItem('user') || '{}').scope === 'platform' ? '/platform/login' : '/login' } catch { return '/login' }
    })()
    router.push(loginPath)
    return
  }
  if (errorKind.value === 'conflict') {
    void restartPreviewAfterConflict()
    return
  }
  if (lastAction.value === 'confirm') confirmTask()
  else if (lastAction.value === 'submit') inputText.value.trim() ? submitInput() : startNewTask()
  else if (lastAction.value === 'cancel') startNewTask()
}

const submitInput = async () => {
  if (loading.value || confirming.value) return
  const content = inputText.value.trim()
  if (!content) return
  // A creation key is bound to the first request body. Reuse it for an
  // unknown-outcome retry of the same input, but never attach a changed user
  // request to the old server-side task.
  if (!task.value && lastSubmittedInput.value && lastSubmittedInput.value !== content) {
    idempotencyKey.value = newKey()
  }
  lastSubmittedInput.value = content
  loading.value = true
  loadingSeconds.value = 0
  const loadingStartedAt = Date.now()
  loadingTimer = setInterval(() => {
    loadingSeconds.value = Math.floor((Date.now() - loadingStartedAt) / 1000)
  }, 1000)
  errorMessage.value = ''
  errorKind.value = ''
  stoppingRequest = false
  activeRequestController?.abort()
  activeRequestController = new AbortController()
  try {
    const response = await request.post('/agent/tasks', {
      task_id: task.value?.task_id || undefined,
      input_text: content,
      idempotency_key: task.value ? undefined : idempotencyKey.value,
      turn_key: newKey(),
    }, { timeout: AI_TASK_TIMEOUT_MS, signal: activeRequestController.signal, skipErrorToast: true } as any)
    task.value = response.data
    sessionStorage.setItem(taskStorageKey, String(response.data.task_id))
    availability.value = response.data.availability || availability.value
    inputText.value = ''
    if (response.data.can_confirm) ElMessage.success('计划已生成，请核对后确认执行')
    else ElMessage.info('还需要补充信息')
  } catch (error: any) {
    if (stoppingRequest) {
      errorKind.value = 'timeout'
      errorMessage.value = '已停止等待；确认前不会修改业务数据，可以稍后用相同内容重试。'
      lastAction.value = 'submit'
    } else {
      setError(error, 'AI 任务处理失败，请检查输入或稍后重试', 'submit')
    }
    // A deterministic rejection did not yield a usable task to this tab. Do
    // not let the next explicit retry reuse its creation key; timeout/5xx
    // keeps the key because the provider outcome may be unknown.
    const status = error?.response?.status
    if (!task.value && status >= 400 && status < 500) idempotencyKey.value = newKey()
  } finally {
    activeRequestController = undefined
    stoppingRequest = false
    loading.value = false
    if (loadingTimer) clearInterval(loadingTimer)
    loadingTimer = undefined
  }
}

const stopWaiting = () => {
  if (!loading.value || !activeRequestController) return
  stoppingRequest = true
  activeRequestController.abort()
}

const confirmTask = async () => {
  if (confirming.value || loading.value || !task.value?.can_confirm) return
  try {
    await ElMessageBox.confirm(
      isCompoundPreview.value
        ? '确认后会一次性确认全部步骤，并按顺序执行。步骤之间不是同一事务；若后续步骤失败，已完成的步骤会保留。是否继续？'
        : '确认后会按预览内容执行，并记录审计。是否继续？',
      '确认执行',
      { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '返回检查' },
    )
  } catch { return }
  confirming.value = true
  errorMessage.value = ''
  errorKind.value = ''
  try {
    const response = await request.post(`/agent/tasks/${task.value.task_id}/confirm`, undefined, { timeout: AI_TASK_TIMEOUT_MS, skipErrorToast: true } as any)
    task.value = response.data
    if (response.data?.state === 'completed') {
      window.dispatchEvent(new CustomEvent('agent-task-completed', { detail: response.data }))
    }
    await loadStatus()
    ElMessage.success(isCompoundPreview.value ? '复合操作已按顺序执行' : '操作已完成')
  } catch (error: any) {
    setError(error, '确认执行失败，请稍后重试', 'confirm')
  } finally { confirming.value = false }
}

const clearInput = () => {
  inputText.value = ''
  lastSubmittedInput.value = ''
  if (!task.value) idempotencyKey.value = newKey()
}

const resetTask = () => {
  task.value = null
  sessionStorage.removeItem(taskStorageKey)
  inputText.value = ''
  errorMessage.value = ''
  errorKind.value = ''
  lastAction.value = ''
  idempotencyKey.value = newKey()
  lastSubmittedInput.value = ''
}

const restartPreviewAfterConflict = async () => {
  const currentTask = task.value
  const originalInput = inputText.value.trim() || String(currentTask?.input_text || '')
  task.value = null
  sessionStorage.removeItem(taskStorageKey)
  inputText.value = originalInput
  errorMessage.value = ''
  errorKind.value = ''
  lastAction.value = ''
  idempotencyKey.value = newKey()
  lastSubmittedInput.value = ''
  if (currentTask?.task_id) {
    try {
      await request.post(`/agent/tasks/${currentTask.task_id}/cancel`, undefined, { timeout: AI_STATUS_TIMEOUT_MS, skipErrorToast: true } as any)
    } catch {
      // The server already rejected confirmation because the preview is stale;
      // starting a fresh, version-checked task remains safe if cancellation is unavailable.
    }
  }
  ElMessage.info('原预览已失效，请重新生成预览后再确认。')
}

onUnmounted(() => {
  if (loadingTimer) clearInterval(loadingTimer)
  activeRequestController?.abort()
})

const startNewTask = async () => {
  if (loading.value || confirming.value || taskLoading.value) return
  if (!task.value) {
    resetTask()
    return
  }
  const currentTask = task.value
  const terminal = currentTask.state === 'completed' || currentTask.state === 'cancelled'
  try {
    await ElMessageBox.confirm(
      terminal
        ? '当前任务已经结束，是否清理当前会话并新建一个任务？历史记录会保留。'
        : '放弃当前 AI 任务并新建一个任务？当前任务不会执行，已保存内容仍保留在任务记录中。',
      '新建任务',
      { type: 'warning', confirmButtonText: '放弃并新建', cancelButtonText: '继续当前任务' },
    )
  } catch {
    return
  }
  errorMessage.value = ''
  errorKind.value = ''
  lastAction.value = 'cancel'
  if (terminal) {
    resetTask()
    return
  }
  try {
    await request.post(`/agent/tasks/${currentTask.task_id}/cancel`, undefined, { timeout: AI_STATUS_TIMEOUT_MS, skipErrorToast: true } as any)
    resetTask()
  } catch (error: any) {
    setError(error, '无法放弃当前任务，请稍后重试', 'cancel')
  }
}

</script>

<style scoped>
.ai-assistant { position: fixed; right: 24px; bottom: 24px; z-index: 1000; }
.assistant-trigger { display: grid; place-items: center; width: 52px; height: 52px; border: 0; border-radius: 50%; color: #fff; background: #2563eb; box-shadow: 0 8px 24px rgba(37, 99, 235, .3); cursor: pointer; transition: transform .18s ease, background .18s ease; }
.assistant-trigger:hover { background: #1d4ed8; transform: translateY(-2px); }
.assistant-trigger .el-icon { font-size: 23px; }
.assistant-panel { width: min(460px, calc(100vw - 28px)); max-height: min(720px, calc(100vh - 40px)); overflow: hidden; background: #fff; border: 1px solid #dfe5ee; border-radius: 8px; box-shadow: 0 18px 50px rgba(15, 23, 42, .2); }
.assistant-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 16px; color: #18202b; border-bottom: 1px solid #edf0f4; }
.assistant-header strong, .assistant-header span { display: block; }
.assistant-header strong { font-size: 14px; }
.assistant-header span { margin-top: 3px; color: #667085; font-size: 11px; }
.assistant-title-row { display: flex; align-items: center; gap: 7px; }
.status-dot { width: 7px; height: 7px; border-radius: 50%; background: #c5ccd6; }
.status-dot.active { background: #35a36b; }
.status-dot.busy { background: #3b82f6; box-shadow: 0 0 0 3px rgba(59, 130, 246, .13); }
.icon-button { display: grid; place-items: center; width: 30px; height: 30px; border: 0; color: #667085; background: transparent; cursor: pointer; }
.assistant-body { max-height: calc(min(720px, 100vh - 40px) - 61px); overflow: auto; padding: 14px 16px 16px; }
.assistant-body > .el-alert { margin-bottom: 10px; }
.availability-alert { --el-alert-padding: 8px 10px; font-size: 12px; }
.assistant-error { display: flex; align-items: flex-start; gap: 9px; margin-bottom: 12px; padding: 9px 10px; border: 1px solid #f4c6c8; border-radius: 6px; background: #fff6f6; color: #9f2d35; }
.error-icon { flex: 0 0 auto; margin-top: 2px; font-size: 16px; }
.error-copy { display: flex; flex: 1; min-width: 0; flex-direction: column; gap: 2px; font-size: 11px; line-height: 16px; }
.error-copy strong { color: #842029; font-size: 12px; }
.error-copy span { overflow-wrap: anywhere; }
.assistant-error .el-button { flex: 0 0 auto; margin: -3px -5px 0 0; color: #8d2730; }
.assistant-intro { display: flex; flex-direction: column; gap: 3px; margin-bottom: 12px; }
.assistant-intro strong { color: #18202b; font-size: 14px; }
.assistant-intro span { color: #667085; font-size: 11px; }
.field-label { display: block; margin-bottom: 6px; color: #344054; font-size: 11px; font-weight: 600; }
.assistant-progress { display: flex; align-items: center; gap: 5px; margin-top: 7px; color: #667085; font-size: 11px; }
.assistant-message { margin-top: 10px; padding: 9px 10px; border-left: 3px solid #3b82f6; background: #f5f8ff; color: #344054; font-size: 12px; line-height: 18px; }
.assistant-message strong, .assistant-message p { display: block; }
.assistant-message strong { color: #1d4ed8; font-size: 11px; }
.assistant-message p { margin: 3px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.query-result-panel { display: flex; flex-direction: column; gap: 9px; margin-top: 10px; padding: 10px; border: 1px solid #dce8df; border-radius: 6px; background: #fbfefb; }
.query-result-heading, .query-result-card-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; }
.query-result-heading strong, .query-result-heading span { display: block; }
.query-result-heading strong { color: #17623a; font-size: 12px; }
.query-result-heading span { margin-top: 2px; color: #667085; font-size: 10px; }
.query-result-card { padding-top: 9px; border-top: 1px solid #e7efe9; }
.query-result-card-heading strong { color: #18202b; font-size: 12px; }
.query-result-card-heading span { color: #7a8698; font-size: 10px; line-height: 16px; text-align: right; }
.query-result-meta { display: flex; flex-wrap: wrap; gap: 5px 10px; margin-top: 5px; color: #667085; font-size: 10px; line-height: 15px; }
.query-filter-list { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 4px 8px; margin: 8px 0 0; padding: 7px 8px; border-radius: 4px; background: #f2f7f3; color: #596579; font-size: 10px; line-height: 15px; }
.query-filter-list dt { color: #667085; }
.query-filter-list dd { min-width: 0; margin: 0; overflow-wrap: anywhere; color: #344054; }
.query-row-list { display: flex; flex-direction: column; gap: 7px; margin-top: 8px; }
.query-row { display: grid; grid-template-columns: minmax(70px, .7fr) minmax(0, 1.3fr); gap: 4px 8px; padding: 8px; border: 1px solid #edf0f4; border-radius: 4px; background: #fff; font-size: 10px; line-height: 15px; }
.query-row > span { color: #7a8698; }
.query-row > strong { min-width: 0; overflow-wrap: anywhere; color: #344054; font-weight: 500; }
.query-empty { margin: 8px 0 0; color: #667085; font-size: 11px; }
.assistant-hint { margin-top: 8px; color: #929baa; font-size: 11px; line-height: 17px; }
.missing-list { display: flex; flex-direction: column; gap: 7px; margin-top: 10px; }
.missing-item { padding: 8px 10px; border-left: 3px solid #f0a64b; background: #fffaf1; color: #684d1a; font-size: 11px; line-height: 16px; }
.missing-item strong, .missing-item span, .missing-item small { display: block; }
.missing-item span { margin-top: 2px; }
.missing-item small { margin-top: 2px; color: #8b6b2e; }
.assistant-actions { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-top: 14px; }
.action-meta { display: flex; min-width: 0; align-items: center; gap: 4px; }
.quota-copy { color: #667085; font-size: 11px; }
.preview-meta, .diff-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.preview-meta { color: #667085; font-size: 11px; }
.original-request { margin: 12px 0; padding: 9px 10px; color: #344054; background: #f8fafc; border-left: 3px solid #93b4f4; font-size: 12px; line-height: 18px; }
.operation-summary { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin-bottom: 12px; }
.operation-summary div { padding: 9px 10px; border: 1px solid #edf0f4; border-radius: 5px; background: #fbfcfe; }
.operation-summary span, .operation-summary strong { display: block; }
.operation-summary span { color: #929baa; font-size: 10px; }
.operation-summary strong { margin-top: 3px; color: #18202b; font-size: 14px; }
.product-summary { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin-bottom: 12px; }
.product-summary div { padding: 9px 10px; border: 1px solid #edf0f4; border-radius: 5px; background: #fbfcfe; }
.product-summary span, .product-summary strong { display: block; }
.product-summary span { color: #929baa; font-size: 10px; }
.product-summary strong { margin-top: 3px; color: #18202b; font-size: 13px; }
.product-detail-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin-bottom: 12px; }
.product-detail-grid div { min-width: 0; padding: 8px 10px; border: 1px solid #edf0f4; border-radius: 5px; background: #fbfcfe; }
.product-detail-grid .wide { grid-column: 1 / -1; }
.product-detail-grid span, .product-detail-grid strong { display: block; }
.product-detail-grid span { color: #929baa; font-size: 10px; }
.product-detail-grid strong { margin-top: 3px; overflow-wrap: anywhere; color: #344054; font-size: 11px; line-height: 16px; }
.rule-preview { padding: 10px; border: 1px solid #e2e7ee; border-radius: 5px; }
.hotel-preview-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 10px; padding: 10px 11px; border: 1px solid #dbe7f3; border-radius: 7px; background: #f7fbff; }
.hotel-preview-heading > div { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.hotel-preview-heading strong { color: #1d4ed8; font-size: 13px; }
.hotel-preview-heading span { overflow-wrap: anywhere; color: #667085; font-size: 11px; }
.hotel-preview-list { display: flex; flex-direction: column; gap: 8px; }
.hotel-preview-row { padding: 10px; border: 1px solid #e2e7ee; border-radius: 7px; background: #fff; }
.hotel-preview-values { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); align-items: center; gap: 8px; margin-top: 8px; }
.hotel-preview-values > div { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.hotel-preview-values small, .hotel-status-diff span { color: #667085; font-size: 10px; }
.hotel-preview-values strong { overflow-wrap: anywhere; color: #344054; font-size: 11px; line-height: 16px; }
.hotel-status-diff { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 9px 12px; padding: 11px; border: 1px solid #e2e7ee; border-radius: 7px; }
.hotel-status-diff > div { display: flex; min-width: 0; flex-direction: column; gap: 3px; }
.hotel-status-diff strong { overflow-wrap: anywhere; color: #344054; font-size: 12px; line-height: 17px; }
.hotel-status-diff .wide { grid-column: 1 / -1; }
.product-update-diff { margin-top: 10px; padding: 10px; border: 1px solid #e2e7ee; border-radius: 5px; }
.product-batch-summary { margin-bottom: 10px; }
.compound-step-list { display: flex; flex-direction: column; gap: 8px; }
.compound-step { padding: 10px; border: 1px solid #e2e7ee; border-radius: 5px; background: #fbfcfe; }
.compound-step .diff-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.compound-step-summary { margin-top: 7px; color: #667085; font-size: 11px; line-height: 17px; }
.compound-execution-note { margin: 10px 0 0; padding: 8px 9px; border-left: 3px solid #d29b2d; background: #fffaf0; color: #6c521a; font-size: 11px; line-height: 17px; }
.batch-update-list { display: flex; flex-direction: column; gap: 9px; }
.product-update-row { padding-top: 8px; }
.product-update-row + .product-update-row { margin-top: 8px; border-top: 1px solid #edf0f4; }
.product-update-label { display: block; color: #667085; font-size: 10px; }
.product-update-values { display: grid; grid-template-columns: minmax(0, 1fr) 16px minmax(0, 1fr); align-items: center; gap: 6px; margin-top: 5px; }
.product-update-values > div { min-width: 0; padding: 7px; background: #f8fafc; border-radius: 4px; }
.product-update-values small, .product-update-values strong { display: block; }
.product-update-values small { margin-bottom: 2px; color: #929baa; font-size: 9px; }
.product-update-values strong { overflow-wrap: anywhere; color: #344054; font-size: 10px; line-height: 15px; }
.product-update-values .el-icon { color: #929baa; }
.rule-group { display: flex; justify-content: space-between; gap: 10px; padding-top: 8px; color: #344054; font-size: 11px; }
.rule-group code { color: #667085; font-family: inherit; text-align: right; }
.assumption-list { margin: 10px 0 0; padding-left: 18px; color: #667085; font-size: 11px; line-height: 17px; }
.diff-list { display: flex; flex-direction: column; gap: 9px; }
.diff-row { padding: 10px; border: 1px solid #e2e7ee; border-radius: 5px; }
.diff-heading strong { color: #18202b; font-size: 12px; }
.diff-heading span { color: #929baa; font-size: 10px; }
.diff-values { display: grid; grid-template-columns: minmax(0, 1fr) 16px minmax(0, 1fr); align-items: center; gap: 6px; margin-top: 9px; }
.diff-values > div { min-width: 0; padding: 7px; background: #f8fafc; border-radius: 4px; }
.diff-values small { display: block; margin-bottom: 3px; color: #929baa; font-size: 9px; }
.diff-side { min-width: 0; }
.diff-values code { display: block; max-height: 150px; overflow: auto; color: #344054; font-family: inherit; font-size: 10px; line-height: 15px; overflow-wrap: anywhere; white-space: pre-wrap; }
.rule-snapshot { max-height: 156px; overflow: auto; color: #344054; font-size: 10px; line-height: 15px; }
.rule-snapshot-group + .rule-snapshot-group { margin-top: 7px; padding-top: 7px; border-top: 1px solid #e5eaf1; }
.rule-snapshot-heading, .rule-snapshot-item { display: flex; align-items: baseline; justify-content: space-between; gap: 6px; }
.rule-snapshot-heading strong { min-width: 0; overflow-wrap: anywhere; color: #18202b; font-size: 10px; }
.rule-snapshot-heading span { flex: 0 0 auto; color: #667085; font-size: 9px; }
.rule-snapshot-item { padding-top: 2px; }
.rule-snapshot-item span:first-child { min-width: 0; overflow-wrap: anywhere; }
.rule-snapshot-item span:last-child { flex: 0 0 auto; color: #667085; }
.rule-snapshot-empty { padding-top: 2px; color: #929baa; }
.diff-values .el-icon { color: #929baa; }
.line-error { margin: 8px 0 0; color: #d94b54; font-size: 11px; }
.preview-actions { justify-content: flex-end; }
@media (max-width: 560px) { .ai-assistant { right: 14px; bottom: 14px; } .assistant-panel { width: calc(100vw - 28px); max-height: calc(100vh - 28px); } .assistant-body { max-height: calc(100vh - 89px); padding: 12px; } .assistant-actions { align-items: flex-end; } .diff-values, .product-update-values, .hotel-preview-values { grid-template-columns: minmax(0, 1fr); } .diff-values > .el-icon, .product-update-values > .el-icon, .hotel-preview-values > .el-icon { justify-self: center; transform: rotate(90deg); } .hotel-status-diff { grid-template-columns: minmax(0, 1fr); } .hotel-status-diff .wide { grid-column: auto; } .query-row { grid-template-columns: minmax(0, 1fr); gap: 2px; } .query-row > span { margin-top: 4px; } .query-result-card-heading { display: block; } .query-result-card-heading span { text-align: left; } }
</style>
