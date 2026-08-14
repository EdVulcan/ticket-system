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

        <template v-if="!preview">
          <el-input
            v-model="inputText"
            type="textarea"
            :rows="5"
            maxlength="2000"
            show-word-limit
            placeholder="例如：给成人票和儿童票增加北门检票点，每个点最多核销 2 次"
            :disabled="loading || (availability && !availability.enabled)"
            @keyup.ctrl.enter="generatePreview"
          />
          <div class="assistant-hint">AI 只生成批量票规预览，确认前不会修改数据。</div>
          <div class="assistant-actions">
            <span v-if="availability" class="quota-copy">本月剩余 {{ availability.requests_remaining }} 次请求</span>
            <el-button type="primary" :icon="Promotion" :loading="loading" :disabled="!inputText.trim() || (availability && !availability.enabled)" @click="generatePreview">生成预览</el-button>
          </div>
        </template>

        <template v-else>
          <div class="preview-meta">
            <span>{{ providerLabel }} · 计划 #{{ preview.plan_id }}</span>
            <el-tag size="small" :type="preview.can_confirm ? 'warning' : 'info'" effect="plain">{{ statusText(preview.status) }}</el-tag>
          </div>
          <p class="original-request">“{{ preview.input_text }}”</p>
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
          <div class="assistant-hint">确认后由服务端重新加锁并校验计划哈希；过期或有变化会拒绝执行。</div>
          <div class="assistant-actions preview-actions">
            <el-button :icon="Refresh" @click="resetPlan">重新描述</el-button>
            <el-button type="primary" :icon="CircleCheck" :loading="confirming" :disabled="!preview.can_confirm || preview.status === 'completed'" @click="confirmPlan">确认执行</el-button>
          </div>
        </template>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ChatDotRound, CircleCheck, Close, Promotion, Refresh, Right } from '@element-plus/icons-vue'
import request from '@/utils/request'

const open = ref(false)
const inputText = ref('')
const loading = ref(false)
const confirming = ref(false)
const availability = ref<any | null>(null)
const preview = ref<any | null>(null)
const errorMessage = ref('')
const idempotencyKey = ref(newKey())

const statusLine = computed(() => {
  if (!availability.value) return '正在读取平台额度'
  if (!availability.value.enabled) return '当前不可用'
  return `${availability.value.provider || '平台模型'} · 剩余 ${availability.value.requests_remaining} 次`
})
const providerLabel = computed(() => `${preview.value?.provider || availability.value?.provider || '平台模型'}${preview.value?.model ? ` / ${preview.value.model}` : ''}`)

function newKey() { return `catalog-ai-${Date.now()}-${Math.random().toString(36).slice(2, 9)}` }
const statusText = (status: string) => ({ previewed: '待确认', completed: '已完成', expired: '已过期', failed: '执行失败' } as Record<string, string>)[status] || status
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
}

const generatePreview = async () => {
  if (!inputText.value.trim()) return
  loading.value = true
  errorMessage.value = ''
  try {
    const response = await request.post('/catalog/batch-changes/ai-preview', { input_text: inputText.value.trim(), idempotency_key: idempotencyKey.value })
    preview.value = response.data.preview
    availability.value = response.data.availability || availability.value
    ElMessage.success('AI 预览已生成，请核对变更范围')
  } catch (error: any) {
    errorMessage.value = error.response?.data?.error || error.message || 'AI 预览生成失败'
  } finally { loading.value = false }
}

const confirmPlan = async () => {
  if (!preview.value?.can_confirm) return
  try {
    await ElMessageBox.confirm('确认后会一次性更新预览中的票种规则，并记录审计。是否继续？', '确认执行', { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '返回检查' })
  } catch { return }
  confirming.value = true
  errorMessage.value = ''
  try {
    const response = await request.post(`/catalog/batch-changes/${preview.value.plan_id}/confirm`, { plan_hash: preview.value.plan_hash })
    preview.value = response.data
    await loadStatus()
    ElMessage.success('批量规则操作已完成')
  } catch (error: any) {
    errorMessage.value = error.response?.data?.error || error.message || '确认执行失败'
  } finally { confirming.value = false }
}

const resetPlan = () => {
  preview.value = null
  inputText.value = ''
  errorMessage.value = ''
  idempotencyKey.value = newKey()
}

watch(open, value => { if (value) void loadStatus() })
onMounted(() => { void loadStatus() })
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
