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
          <div v-if="task?.missing_fields?.length" class="missing-list">
            <div v-for="field in task.missing_fields" :key="field.field" class="missing-item">
              <strong>{{ field.label }}</strong><span>{{ field.question }}</span>
              <small v-if="field.options?.length">可选：{{ field.options.join('、') }}</small>
            </div>
          </div>
          <div class="assistant-hint">确认前不会修改票种、规则或分销数据。</div>
          <div class="assistant-actions">
            <div class="action-meta">
              <span v-if="availability" class="quota-copy">本月剩余 {{ availability.requests_remaining }} 次请求</span>
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
          <template v-if="isProductPreview">
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
          <div class="assistant-hint">确认后服务端会重新校验租户归属和当前数据；过期或有变化会拒绝执行。</div>
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
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ChatDotRound, CircleCheck, Close, Loading, Promotion, Refresh, Right, WarningFilled } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import request from '@/utils/request'
import { localizeErrorMessage } from '@/utils/localize'
import { parseRuleSnapshot, ruleGroupMode } from '@/utils/ruleSnapshot'

type ErrorKind = 'auth' | 'timeout' | 'provider' | 'conflict' | 'generic' | ''
type AgentAction = 'submit' | 'confirm' | 'cancel' | ''

const AI_STATUS_TIMEOUT_MS = 15_000
// Keep a client-side buffer above the platform's 120-second provider timeout.
const AI_TASK_TIMEOUT_MS = 180_000

const open = ref(false)
const inputText = ref('')
const loading = ref(false)
const confirming = ref(false)
const statusLoading = ref(false)
const taskLoading = ref(false)
const availability = ref<any | null>(null)
const task = ref<any | null>(null)
const errorMessage = ref('')
const errorKind = ref<ErrorKind>('')
const lastAction = ref<AgentAction>('')
const idempotencyKey = ref(newKey())
const router = useRouter()

const statusLine = computed(() => {
  if (loading.value) return '正在整理操作计划'
  if (confirming.value) return '正在执行已确认计划'
  if (statusLoading.value) return '正在读取平台额度'
  if (!availability.value) return '正在读取平台额度'
  if (!availability.value.enabled) return '当前不可用'
  if (task.value?.state === 'awaiting_confirmation') return '等待确认执行'
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
  if (errorKind.value === 'conflict') return '任务状态已变化'
  return '这次请求没有完成'
})
const errorActionLabel = computed(() => errorKind.value === 'auth' ? '重新登录' : '重试')
const providerLabel = computed(() => `${task.value?.provider || availability.value?.provider || '平台模型'}${task.value?.model ? ` / ${task.value.model}` : ''}`)
const preview = computed(() => task.value?.preview || {})
const isProductUpdatePreview = computed(() => preview.value?.operation_type === 'ticket_product_update')
const productPreview = computed(() => {
  if (preview.value?.operation_type === 'ticket_product_create') return preview.value
  if (isProductUpdatePreview.value) return { ...preview.value, product: preview.value.after }
  return {}
})
const isProductPreview = computed(() => preview.value?.operation_type === 'ticket_product_create' || isProductUpdatePreview.value)
const productUpdateRows = computed(() => {
  if (!isProductUpdatePreview.value) return []
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

function newKey() { return `catalog-ai-${Date.now()}-${Math.random().toString(36).slice(2, 9)}` }
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
const refundText = (product: any) => product?.refund_type || '未设置'
const ruleItems = (group: any) => {
  const total = group?.max_total_check_in > 0 ? `总计最多 ${group.max_total_check_in} 次` : '总次数不限'
  const items = (group?.items || []).map((item: any) => `${item.checkpoint_name || `#${item.checkpoint_id}`} ×${item.max_per_check_in}`).join('、') || '-'
  return `${total}；${items}`
}
const loadStatus = async () => {
  statusLoading.value = true
  try {
    availability.value = (await request.get('/catalog/batch-changes/ai-status', { timeout: AI_STATUS_TIMEOUT_MS, skipErrorToast: true } as any)).data
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
  const savedTaskID = localStorage.getItem('ticket-agent-task-id')
  if (savedTaskID && !task.value) {
    taskLoading.value = true
    try {
      task.value = (await request.get(`/agent/tasks/${savedTaskID}`, { timeout: AI_STATUS_TIMEOUT_MS, skipErrorToast: true } as any)).data
    } catch {
      localStorage.removeItem('ticket-agent-task-id')
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
  else if (status === 409 || code === 'task_conflict') errorKind.value = 'conflict'
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
  if (lastAction.value === 'confirm') confirmTask()
  else if (lastAction.value === 'submit') inputText.value.trim() ? submitInput() : startNewTask()
  else if (lastAction.value === 'cancel') startNewTask()
}

const submitInput = async () => {
  if (loading.value || confirming.value) return
  const content = inputText.value.trim()
  if (!content) return
  loading.value = true
  errorMessage.value = ''
  errorKind.value = ''
  try {
    const response = await request.post('/agent/tasks', {
      task_id: task.value?.task_id || undefined,
      input_text: content,
      idempotency_key: task.value ? undefined : idempotencyKey.value,
      turn_key: newKey(),
    }, { timeout: AI_TASK_TIMEOUT_MS, skipErrorToast: true } as any)
    task.value = response.data
    localStorage.setItem('ticket-agent-task-id', String(response.data.task_id))
    availability.value = response.data.availability || availability.value
    inputText.value = ''
    if (response.data.can_confirm) ElMessage.success('计划已生成，请核对后确认执行')
    else ElMessage.info('还需要补充信息')
  } catch (error: any) {
    // The server no longer accepts this local task context. Detach it while
    // preserving the typed request so the next submit starts a fresh task.
    if (error?.response?.status === 409) {
      task.value = null
      localStorage.removeItem('ticket-agent-task-id')
      idempotencyKey.value = newKey()
    }
    setError(error, 'AI 任务处理失败，请检查输入或稍后重试', 'submit')
  } finally { loading.value = false }
}

const confirmTask = async () => {
  if (confirming.value || loading.value || !task.value?.can_confirm) return
  try {
    await ElMessageBox.confirm('确认后会按预览内容执行，并记录审计。是否继续？', '确认执行', { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '返回检查' })
  } catch { return }
  confirming.value = true
  errorMessage.value = ''
  errorKind.value = ''
  try {
    const response = await request.post(`/agent/tasks/${task.value.task_id}/confirm`, undefined, { timeout: AI_TASK_TIMEOUT_MS, skipErrorToast: true } as any)
    task.value = response.data
    await loadStatus()
    ElMessage.success('操作已完成')
  } catch (error: any) {
    setError(error, '确认执行失败，请稍后重试', 'confirm')
  } finally { confirming.value = false }
}

const clearInput = () => { inputText.value = '' }

const resetTask = () => {
  task.value = null
  localStorage.removeItem('ticket-agent-task-id')
  inputText.value = ''
  errorMessage.value = ''
  errorKind.value = ''
  lastAction.value = ''
  idempotencyKey.value = newKey()
}

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
.product-update-diff { margin-top: 10px; padding: 10px; border: 1px solid #e2e7ee; border-radius: 5px; }
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
@media (max-width: 560px) { .ai-assistant { right: 14px; bottom: 14px; } .assistant-panel { width: calc(100vw - 28px); max-height: calc(100vh - 28px); } .assistant-body { max-height: calc(100vh - 89px); padding: 12px; } .assistant-actions { align-items: flex-end; } .diff-values, .product-update-values { grid-template-columns: minmax(0, 1fr); } .diff-values > .el-icon, .product-update-values > .el-icon { justify-self: center; transform: rotate(90deg); } }
</style>
