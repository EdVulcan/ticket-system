<template>
  <div class="catalog-batch-page">
    <header class="page-heading">
      <div class="page-heading-copy">
        <span class="eyebrow">CATALOG OPERATIONS</span>
        <h2>批量规则操作</h2>
        <p>把多个票种的检票规则变更集中成一份可预览、可确认、可追溯的操作计划。</p>
      </div>
      <div class="page-actions">
        <el-tag v-if="preview" :type="preview.status === 'completed' ? 'success' : preview.can_confirm ? 'warning' : 'info'" effect="plain">
          {{ statusText(preview.status) }}
        </el-tag>
        <el-button v-if="preview" :icon="Refresh" @click="resetWorkspace">新建计划</el-button>
      </div>
    </header>

    <div class="batch-grid">
      <section class="batch-panel input-panel">
        <div class="panel-title">
          <div>
            <span class="panel-kicker">01 / INPUT</span>
            <h3>描述要做什么</h3>
          </div>
          <el-tag size="small" type="info" effect="plain">服务端解析</el-tag>
        </div>
        <el-input
          v-model="inputText"
          type="textarea"
          :rows="5"
          maxlength="2000"
          show-word-limit
          placeholder="例如：给成人票、儿童票增加北门检票点，每个点最多核销 2 次"
          :disabled="Boolean(preview && preview.status === 'completed')"
        />
        <div class="input-hint">支持增加、移除检票点，或设置单点核销次数。名称必须与当前租户的票种和检票点一致。</div>

        <el-divider content-position="left">结构化操作（可选）</el-divider>
        <div class="structured-fields">
          <el-form label-position="top">
            <el-form-item label="操作类型">
              <el-select v-model="draft.kind" class="w-full">
                <el-option label="增加检票点" value="add_checkpoints" />
                <el-option label="移除检票点" value="remove_checkpoints" />
                <el-option label="设置单点核销次数" value="set_checkpoint_limit" />
              </el-select>
            </el-form-item>
            <el-form-item label="票种">
              <el-select v-model="draft.productIds" class="w-full" multiple filterable collapse-tags placeholder="选择一个或多个票种">
                <el-option v-for="product in products" :key="product.id" :label="product.name" :value="product.id" />
              </el-select>
            </el-form-item>
            <el-form-item label="检票点">
              <el-select v-model="draft.checkpointIds" class="w-full" multiple filterable collapse-tags placeholder="选择一个或多个检票点">
                <el-option v-for="checkpoint in checkpoints" :key="checkpoint.id" :label="checkpoint.name" :value="checkpoint.id" />
              </el-select>
            </el-form-item>
            <div class="field-row">
              <el-form-item label="规则组（多组票种必填）">
                <el-input v-model="draft.groupName" placeholder="例如：入园组" />
              </el-form-item>
              <el-form-item v-if="draft.kind !== 'remove_checkpoints'" label="单点次数">
                <el-input-number v-model="draft.maxPerCheckIn" :min="1" :max="1000" class="w-full" />
              </el-form-item>
            </div>
          </el-form>
          <el-button :icon="Plus" plain @click="addStructuredOperation">加入操作清单</el-button>
        </div>

        <div v-if="structuredOperations.length" class="operation-list">
          <div class="operation-list-header">
            <span>待预览的结构化操作</span>
            <el-button link type="danger" @click="structuredOperations = []">清空</el-button>
          </div>
          <div v-for="(operation, index) in structuredOperations" :key="`${operation.kind}-${index}`" class="operation-row">
            <div>
              <strong>{{ operationText(operation) }}</strong>
              <span>{{ operation.product_ids.length }} 个票种 · {{ operation.checkpoint_ids.length }} 个检票点</span>
            </div>
            <el-button :icon="Delete" circle text type="danger" title="移除操作" @click="structuredOperations.splice(index, 1)" />
          </div>
        </div>

        <div class="panel-footer">
          <span class="idempotency-copy">本次计划编号：{{ idempotencyKey }}</span>
          <el-button type="primary" :icon="View" :loading="loading" :disabled="Boolean(preview && preview.status === 'completed')" @click="previewPlan">生成预览</el-button>
        </div>
      </section>

      <section class="batch-panel preview-panel">
        <div class="panel-title">
          <div>
            <span class="panel-kicker">02 / REVIEW</span>
            <h3>预览并确认</h3>
          </div>
          <span v-if="preview" class="plan-meta">计划 #{{ preview.plan_id }} · {{ formatTime(preview.expires_at) }} 失效</span>
        </div>

        <div v-if="!preview" class="empty-preview">
          <el-icon><Document /></el-icon>
          <strong>还没有可执行计划</strong>
          <span>填写左侧描述并生成预览，系统会在这里展示每个票种的规则差异。</span>
        </div>

        <template v-else>
          <div class="preview-summary">
            <div><span>规范化操作</span><strong>{{ preview.operations.length }} 项</strong></div>
            <div><span>影响票种</span><strong>{{ preview.lines.length }} 个</strong></div>
            <div><span>影响授权</span><strong>{{ affectedOffers }} 条</strong></div>
            <div><span>状态</span><strong>{{ statusText(preview.status) }}</strong></div>
          </div>

          <el-alert v-if="preview.warnings?.length" type="warning" :closable="false" show-icon class="mb-4">
            <template #title>
              <div v-for="warning in preview.warnings" :key="warning">{{ warning }}</div>
            </template>
          </el-alert>

          <div class="diff-list">
            <article v-for="line in preview.lines" :key="line.line_id" class="diff-row">
              <div class="diff-heading">
                <div>
                  <strong>{{ line.product_name }}</strong>
                  <span>规则版本 {{ line.before_revision_id }}<template v-if="line.after_revision_id"> → {{ line.after_revision_id }}</template></span>
                </div>
                <el-tag size="small" :type="line.status === 'applied' ? 'success' : line.status === 'no_change' ? 'info' : line.error_message ? 'danger' : 'warning'" effect="plain">
                  {{ lineStatusText(line) }}
                </el-tag>
              </div>
              <div class="diff-stats">
                <span>active Offer {{ line.affected_offer_count }}</span>
                <span v-if="line.affected_bundle_count">当前套餐 {{ line.affected_bundle_count }} 个</span>
              </div>
                <div class="rule-diff">
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
              <p v-if="line.error_message" class="diff-error">{{ line.error_message }}</p>
            </article>
          </div>

          <div class="confirm-bar">
            <span v-if="preview.status === 'completed'">该计划已经执行完成，重复确认不会重复写入。</span>
            <span v-else-if="!preview.can_confirm">当前计划存在阻断原因，需要重新预览。</span>
            <span v-else>确认后将一次性更新所有待执行票种，并记录审计。</span>
            <el-button type="primary" :icon="CircleCheck" :disabled="!preview.can_confirm || preview.status === 'completed'" :loading="confirming" @click="confirmPlan">确认执行</el-button>
          </div>
        </template>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CircleCheck, Delete, Document, Plus, Refresh, Right, View } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { parseRuleSnapshot, ruleGroupMode } from '@/utils/ruleSnapshot'

type Operation = {
  kind: string
  product_ids: number[]
  checkpoint_ids: number[]
  group_name?: string
  max_per_check_in?: number
}

const inputText = ref('')
const products = ref<any[]>([])
const checkpoints = ref<any[]>([])
const structuredOperations = ref<Operation[]>([])
const preview = ref<any | null>(null)
const loading = ref(false)
const confirming = ref(false)
const idempotencyKey = ref(newKey())
const draft = reactive({ kind: 'add_checkpoints', productIds: [] as number[], checkpointIds: [] as number[], groupName: '', maxPerCheckIn: 1 })

function newKey() { return `catalog-batch-${Date.now()}-${Math.random().toString(36).slice(2, 8)}` }

const affectedOffers = computed(() => (preview.value?.lines || []).reduce((total: number, line: any) => total + Number(line.affected_offer_count || 0), 0))

const statusText = (status: string) => ({ previewed: '待确认', completed: '已完成', expired: '已过期', failed: '执行失败' } as Record<string, string>)[status] || status
const lineStatusText = (line: any) => line.status === 'applied' ? '已执行' : line.status === 'no_change' ? '无需变更' : line.error_message ? '无法执行' : '待执行'
const formatTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : ''
const operationText = (operation: Operation) => {
  const names = (ids: number[], source: any[]) => ids.map(id => source.find(item => item.id === id)?.name || `#${id}`).join('、')
  const action = operation.kind === 'add_checkpoints' ? '增加' : operation.kind === 'remove_checkpoints' ? '移除' : '设置次数'
  const suffix = operation.kind === 'set_checkpoint_limit' ? `，每点 ${operation.max_per_check_in} 次` : ''
  return `${action} ${names(operation.checkpoint_ids, checkpoints.value)}${suffix}`
}

const addStructuredOperation = () => {
  if (!draft.productIds.length || !draft.checkpointIds.length) {
    ElMessage.warning('请先选择票种和检票点')
    return
  }
  const operation: Operation = {
    kind: draft.kind,
    product_ids: [...draft.productIds],
    checkpoint_ids: [...draft.checkpointIds],
    group_name: draft.groupName.trim() || undefined,
    max_per_check_in: draft.kind === 'remove_checkpoints' ? undefined : draft.maxPerCheckIn,
  }
  structuredOperations.value.push(operation)
  draft.productIds = []
  draft.checkpointIds = []
  draft.groupName = ''
}

const previewPlan = async () => {
  if (!inputText.value.trim() && !structuredOperations.value.length) {
    ElMessage.warning('请填写自然语言描述或加入一条结构化操作')
    return
  }
  loading.value = true
  try {
    const response = await request.post('/catalog/batch-changes/preview', {
      input_text: inputText.value.trim(),
      idempotency_key: idempotencyKey.value,
      operations: structuredOperations.value,
    })
    preview.value = response.data
    ElMessage.success('预览已生成，请核对变更范围')
  } finally {
    loading.value = false
  }
}

const confirmPlan = async () => {
  if (!preview.value?.can_confirm) return
  await ElMessageBox.confirm('确认后会一次性更新预览中的票种规则，并写入审计记录。是否继续？', '确认执行', { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '返回检查' })
  confirming.value = true
  try {
    const response = await request.post(`/catalog/batch-changes/${preview.value.plan_id}/confirm`, { plan_hash: preview.value.plan_hash })
    preview.value = response.data
    ElMessage.success('批量规则操作已完成')
  } finally {
    confirming.value = false
  }
}

const resetWorkspace = () => {
  inputText.value = ''
  structuredOperations.value = []
  preview.value = null
  idempotencyKey.value = newKey()
}

const loadOptions = async () => {
  const [productResponse, checkpointResponse] = await Promise.all([
    request.get('/products', { params: { page: 1, page_size: 100, type: 'online' } }),
    request.get('/checkpoints', { params: { page: 1, page_size: 100 } }),
  ])
  products.value = productResponse.data.data || []
  checkpoints.value = checkpointResponse.data.data || []
}

onMounted(loadOptions)
</script>

<style scoped>
.catalog-batch-page { min-height: 100%; }
.page-heading { display: flex; justify-content: space-between; gap: 24px; align-items: flex-start; margin-bottom: 20px; }
.page-heading-copy h2 { margin: 4px 0 6px; color: #18202b; font-size: 22px; line-height: 30px; }
.page-heading-copy p { margin: 0; color: #667085; font-size: 13px; }
.eyebrow, .panel-kicker { color: #2563eb; font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.page-actions { display: flex; align-items: center; gap: 10px; }
.batch-grid { display: grid; grid-template-columns: minmax(320px, .8fr) minmax(480px, 1.2fr); gap: 16px; align-items: start; }
.batch-panel { min-width: 0; padding: 20px; background: #fff; border: 1px solid #e2e7ee; border-radius: 6px; box-shadow: 0 5px 16px rgba(24, 32, 43, .04); }
.panel-title { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 16px; }
.panel-title h3 { margin: 3px 0 0; font-size: 17px; line-height: 24px; }
.plan-meta, .idempotency-copy { color: #929baa; font-size: 11px; }
.input-hint { margin-top: 8px; color: #929baa; font-size: 12px; line-height: 18px; }
.structured-fields :deep(.el-form-item) { margin-bottom: 12px; }
.field-row { display: grid; grid-template-columns: minmax(0, 1fr) 132px; gap: 12px; }
.operation-list { margin-top: 16px; border-top: 1px solid #edf0f4; }
.operation-list-header { display: flex; justify-content: space-between; align-items: center; padding: 12px 0 6px; color: #667085; font-size: 12px; }
.operation-row { display: flex; justify-content: space-between; gap: 12px; padding: 9px 0; border-bottom: 1px solid #f1f3f6; }
.operation-row strong, .operation-row span { display: block; }
.operation-row strong { color: #18202b; font-size: 12px; font-weight: 600; }
.operation-row span { margin-top: 3px; color: #929baa; font-size: 11px; }
.panel-footer, .confirm-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 18px; padding-top: 14px; border-top: 1px solid #edf0f4; }
.empty-preview { min-height: 420px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; color: #929baa; text-align: center; }
.empty-preview .el-icon { color: #b9cdf9; font-size: 40px; }
.empty-preview strong { color: #667085; font-size: 14px; }
.empty-preview span { max-width: 260px; font-size: 12px; line-height: 18px; }
.preview-summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; margin-bottom: 16px; }
.preview-summary div { min-width: 0; padding: 10px 12px; background: #f8fafc; border: 1px solid #edf0f4; border-radius: 5px; }
.preview-summary span, .preview-summary strong { display: block; }
.preview-summary span { color: #929baa; font-size: 11px; }
.preview-summary strong { margin-top: 4px; color: #18202b; font-size: 16px; }
.diff-list { display: flex; flex-direction: column; gap: 10px; max-height: 570px; overflow: auto; padding-right: 2px; }
.diff-row { padding: 13px; border: 1px solid #e2e7ee; border-radius: 5px; }
.diff-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.diff-heading strong, .diff-heading span { display: block; }
.diff-heading strong { color: #18202b; font-size: 13px; }
.diff-heading span { margin-top: 3px; color: #929baa; font-size: 11px; }
.diff-stats { display: flex; gap: 12px; margin-top: 8px; color: #667085; font-size: 11px; }
.rule-diff { display: grid; grid-template-columns: minmax(0, 1fr) 18px minmax(0, 1fr); align-items: center; gap: 8px; margin-top: 11px; }
.rule-diff > div { min-width: 0; padding: 8px; background: #f8fafc; border-radius: 4px; }
.rule-diff small { display: block; margin-bottom: 4px; color: #929baa; font-size: 10px; }
.diff-side { min-width: 0; }
.rule-diff code { display: block; max-height: 168px; overflow: auto; color: #344054; font-family: inherit; font-size: 11px; line-height: 17px; overflow-wrap: anywhere; white-space: pre-wrap; }
.rule-snapshot { max-height: 168px; overflow: auto; color: #344054; font-size: 11px; line-height: 17px; }
.rule-snapshot-group + .rule-snapshot-group { margin-top: 8px; padding-top: 8px; border-top: 1px solid #e5eaf1; }
.rule-snapshot-heading, .rule-snapshot-item { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
.rule-snapshot-heading strong { min-width: 0; overflow-wrap: anywhere; color: #18202b; font-size: 11px; }
.rule-snapshot-heading span { flex: 0 0 auto; color: #667085; font-size: 10px; }
.rule-snapshot-item { padding-top: 2px; }
.rule-snapshot-item span:first-child { min-width: 0; overflow-wrap: anywhere; }
.rule-snapshot-item span:last-child { flex: 0 0 auto; color: #667085; }
.rule-snapshot-empty { padding-top: 2px; color: #929baa; }
.rule-diff .el-icon { color: #929baa; }
.diff-error { margin: 9px 0 0; color: #d94b54; font-size: 11px; line-height: 17px; }
.confirm-bar { color: #667085; font-size: 12px; }
@media (max-width: 980px) {
  .batch-grid { grid-template-columns: 1fr; }
  .preview-panel { order: -1; }
}
@media (max-width: 560px) {
  .batch-panel { padding: 15px; }
  .page-heading { flex-direction: column; gap: 12px; }
  .page-actions { width: 100%; justify-content: space-between; }
  .preview-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .field-row, .rule-diff { grid-template-columns: 1fr; }
  .rule-diff > .el-icon { display: block; justify-self: center; transform: rotate(90deg); }
  .panel-footer, .confirm-bar { align-items: flex-start; flex-direction: column; }
}
</style>
