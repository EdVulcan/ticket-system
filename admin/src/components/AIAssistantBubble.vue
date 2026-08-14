<template>
  <div class="ai-assistant" data-testid="ai-assistant">
    <button v-if="!open" class="assistant-trigger" type="button" title="打开 AI 助手" aria-label="打开 AI 助手" @click="openAssistant">
      <el-icon><ChatDotRound /></el-icon>
    </button>

    <section v-else class="assistant-panel" role="dialog" aria-label="AI 助手">
      <header class="assistant-header">
        <div>
          <strong>AI 助手</strong>
          <span>{{ statusLine }}</span>
        </div>
        <button class="icon-button" type="button" title="关闭" aria-label="关闭" @click="open = false">
          <el-icon><Close /></el-icon>
        </button>
      </header>

      <div class="assistant-body">
        <el-alert v-if="availability && !availability.enabled" type="warning" :closable="false" show-icon>
          {{ availability.reason || '平台 AI 尚未启用' }}
        </el-alert>
        <el-alert v-if="errorMessage" type="error" :closable="false" show-icon>{{ errorMessage }}</el-alert>

        <template v-if="!task?.preview">
          <el-input
            v-model="inputText"
            type="textarea"
            :rows="5"
            maxlength="2000"
            show-word-limit
            :placeholder="task ? '继续补充缺失信息，例如：售价 120 元，结算价 80 元' : '例如：创建一个成人票，售价 120 元，结算价 80 元，使用北门检票点'"
            :disabled="loading || (availability && !availability.enabled)"
            @keyup.ctrl.enter="submitInput"
          />
          <div v-if="task?.missing_fields?.length" class="missing-list">
            <div v-for="field in task.missing_fields" :key="field.field" class="missing-item">
              <strong>{{ field.label }}</strong><span>{{ field.question }}</span>
              <small v-if="field.options?.length">可选：{{ field.options.join('、') }}</small>
            </div>
          </div>
          <div class="assistant-hint">确认前不会修改票种、规则或分销数据。</div>
          <div class="assistant-actions">
            <span v-if="availability" class="quota-copy">本月剩余 {{ availability.requests_remaining }} 次请求</span>
            <el-button type="primary" :icon="Promotion" :loading="loading" :disabled="!inputText.trim() || (availability && !availability.enabled)" @click="submitInput">{{ task ? '继续处理' : '生成计划' }}</el-button>
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
              <div><span>上线与分销</span><strong>离线 · 不分销</strong></div>
            </div>
            <div class="rule-preview">
              <div class="diff-heading"><strong>检票规则</strong><span>{{ productPreview.rule?.validity_type || 'date' }}</span></div>
              <div v-for="group in productPreview.rule_groups || []" :key="group.group_name" class="rule-group">
                <span>{{ group.group_name || '默认分组' }}</span>
                <code>{{ ruleItems(group) }}</code>
              </div>
            </div>
            <ul v-if="productPreview.assumptions?.length" class="assumption-list">
              <li v-for="assumption in productPreview.assumptions" :key="assumption">{{ assumption }}</li>
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
                  <div><small>变更前</small><code>{{ compactRule(line.before_json) }}</code></div>
                  <el-icon><Right /></el-icon>
                  <div><small>变更后</small><code>{{ compactRule(line.after_json) }}</code></div>
                </div>
                <p v-if="line.error_message" class="line-error">{{ line.error_message }}</p>
              </article>
            </div>
          </template>
          <div class="assistant-hint">确认后服务端会重新校验租户归属和当前数据；过期或有变化会拒绝执行。</div>
          <div class="assistant-actions preview-actions">
            <el-button :icon="Refresh" @click="resetTask">重新开始</el-button>
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
import { ChatDotRound, CircleCheck, Close, Promotion, Refresh, Right } from '@element-plus/icons-vue'
import request from '@/utils/request'

const open = ref(false)
const inputText = ref('')
const loading = ref(false)
const confirming = ref(false)
const availability = ref<any | null>(null)
const task = ref<any | null>(null)
const errorMessage = ref('')
const idempotencyKey = ref(newKey())

const statusLine = computed(() => {
  if (!availability.value) return '正在读取平台额度'
  if (!availability.value.enabled) return '当前不可用'
  if (task.value?.state === 'awaiting_confirmation') return '等待确认执行'
  if (task.value?.state === 'collecting') return '等待补充信息'
  return `${availability.value.provider || '平台模型'} · 剩余 ${availability.value.requests_remaining} 次`
})
const providerLabel = computed(() => `${task.value?.provider || availability.value?.provider || '平台模型'}${task.value?.model ? ` / ${task.value.model}` : ''}`)
const preview = computed(() => task.value?.preview || {})
const productPreview = computed(() => preview.value?.operation_type === 'ticket_product_create' ? preview.value : {})
const isProductPreview = computed(() => preview.value?.operation_type === 'ticket_product_create')

function newKey() { return `catalog-ai-${Date.now()}-${Math.random().toString(36).slice(2, 9)}` }
const statusText = (status: string) => ({ awaiting_confirmation: '待确认', completed: '已完成', collecting: '补充信息', expired: '已过期', failed: '执行失败' } as Record<string, string>)[status] || status
const money = (value: any) => value === undefined || value === null ? '-' : `¥${Number(value).toFixed(2)}`
const ruleItems = (group: any) => (group.items || []).map((item: any) => `${item.checkpoint_name || `#${item.checkpoint_id}`} ×${item.max_per_check_in}`).join('、') || '-'
const compactRule = (value: string) => {
  try {
    const rule = JSON.parse(value)
    return (rule.groups || []).map((group: any) => `${group.group_name || '未命名'}：${(group.items || []).map((item: any) => `${item.checkpoint_name || `#${item.checkpoint_id}`} ×${item.max_per_check_in}`).join('、')}`).join('；') || '-'
  } catch { return value || '-' }
}

const loadStatus = async () => {
  try {
    availability.value = (await request.get('/catalog/batch-changes/ai-status', { skipErrorToast: true } as any)).data
  } catch { availability.value = { enabled: false, reason: '无法读取平台 AI 状态' } }
}

const openAssistant = async () => {
  open.value = true
  await loadStatus()
  const savedTaskID = localStorage.getItem('ticket-agent-task-id')
  if (savedTaskID && !task.value) {
    try {
      task.value = (await request.get(`/agent/tasks/${savedTaskID}`, { skipErrorToast: true } as any)).data
    } catch {
      localStorage.removeItem('ticket-agent-task-id')
    }
  }
}

const submitInput = async () => {
  if (!inputText.value.trim()) return
  loading.value = true
  errorMessage.value = ''
  try {
    const response = await request.post('/agent/tasks', {
      task_id: task.value?.task_id || undefined,
      input_text: inputText.value.trim(),
      idempotency_key: task.value ? undefined : idempotencyKey.value,
      turn_key: newKey(),
    })
    task.value = response.data
    localStorage.setItem('ticket-agent-task-id', String(response.data.task_id))
    availability.value = response.data.availability || availability.value
    inputText.value = ''
    if (response.data.can_confirm) ElMessage.success('计划已生成，请核对后确认执行')
    else ElMessage.info('还需要补充信息')
  } catch (error: any) {
    errorMessage.value = error.response?.data?.error || error.message || 'AI 任务处理失败'
  } finally { loading.value = false }
}

const confirmTask = async () => {
  if (!task.value?.can_confirm) return
  try {
    await ElMessageBox.confirm('确认后会按预览内容执行，并记录审计。是否继续？', '确认执行', { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '返回检查' })
  } catch { return }
  confirming.value = true
  errorMessage.value = ''
  try {
    const response = await request.post(`/agent/tasks/${task.value.task_id}/confirm`)
    task.value = response.data
    await loadStatus()
    ElMessage.success('操作已完成')
  } catch (error: any) {
    errorMessage.value = error.response?.data?.error || error.message || '确认执行失败'
  } finally { confirming.value = false }
}

const resetTask = () => {
  task.value = null
  localStorage.removeItem('ticket-agent-task-id')
  inputText.value = ''
  errorMessage.value = ''
  idempotencyKey.value = newKey()
}

</script>

<style scoped>
.ai-assistant { position: fixed; right: 24px; bottom: 24px; z-index: 1000; }
.assistant-trigger { display: grid; place-items: center; width: 52px; height: 52px; border: 0; border-radius: 50%; color: #fff; background: #2563eb; box-shadow: 0 8px 24px rgba(37, 99, 235, .3); cursor: pointer; transition: transform .18s ease, background .18s ease; }
.assistant-trigger:hover { background: #1d4ed8; transform: translateY(-2px); }
.assistant-trigger .el-icon { font-size: 23px; }
.assistant-panel { width: min(430px, calc(100vw - 28px)); max-height: min(720px, calc(100vh - 40px)); overflow: hidden; background: #fff; border: 1px solid #dfe5ee; border-radius: 8px; box-shadow: 0 18px 50px rgba(15, 23, 42, .2); }
.assistant-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 16px; color: #18202b; border-bottom: 1px solid #edf0f4; }
.assistant-header strong, .assistant-header span { display: block; }
.assistant-header strong { font-size: 14px; }
.assistant-header span { margin-top: 3px; color: #667085; font-size: 11px; }
.icon-button { display: grid; place-items: center; width: 30px; height: 30px; border: 0; color: #667085; background: transparent; cursor: pointer; }
.assistant-body { max-height: calc(min(720px, 100vh - 40px) - 61px); overflow: auto; padding: 14px 16px 16px; }
.assistant-body > .el-alert + .el-alert { margin-top: 8px; }
.assistant-hint { margin-top: 8px; color: #929baa; font-size: 11px; line-height: 17px; }
.missing-list { display: flex; flex-direction: column; gap: 7px; margin-top: 10px; }
.missing-item { padding: 8px 10px; border-left: 3px solid #f0a64b; background: #fffaf1; color: #684d1a; font-size: 11px; line-height: 16px; }
.missing-item strong, .missing-item span, .missing-item small { display: block; }
.missing-item span { margin-top: 2px; }
.missing-item small { margin-top: 2px; color: #8b6b2e; }
.assistant-actions { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-top: 14px; }
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
.rule-preview { padding: 10px; border: 1px solid #e2e7ee; border-radius: 5px; }
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
.diff-values code { display: block; overflow: hidden; color: #344054; font-family: inherit; font-size: 10px; line-height: 15px; text-overflow: ellipsis; white-space: nowrap; }
.diff-values .el-icon { color: #929baa; }
.line-error { margin: 8px 0 0; color: #d94b54; font-size: 11px; }
.preview-actions { justify-content: flex-end; }
@media (max-width: 560px) { .ai-assistant { right: 14px; bottom: 14px; } .assistant-panel { width: calc(100vw - 28px); } }
</style>
