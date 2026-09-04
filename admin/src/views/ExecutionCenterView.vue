<template>
  <section class="execution-page">
    <header class="page-header">
      <div>
        <h1>可信执行中心</h1>
        <p>把现有支付、退款、渠道、预约、打印和设备任务集中呈现，处理仍回到各自业务工作台。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新" :loading="loading" @click="load" />
    </header>

    <div class="summary-grid" aria-label="待处理摘要">
      <article class="summary-card summary-card-total">
        <span>当前待关注</span>
        <strong>{{ summary.total }}</strong>
        <small>只读聚合，不改变业务状态</small>
      </article>
      <article class="summary-card summary-card-critical">
        <span>需要立即处理</span>
        <strong>{{ summary.critical }}</strong>
        <small>失败、人工复核或异常</small>
      </article>
      <article class="summary-card summary-card-warning">
        <span>等待收敛</span>
        <strong>{{ summary.warning }}</strong>
        <small>排队、处理中或待同步</small>
      </article>
      <article class="summary-card summary-card-info">
        <span>其他待办</span>
        <strong>{{ summary.info }}</strong>
        <small>按业务工作台继续处理</small>
      </article>
    </div>

    <section class="worklist-panel">
      <div class="worklist-toolbar">
        <div>
          <h2>待处理事项</h2>
          <span v-if="generatedAt">更新于 {{ formatTime(generatedAt) }}</span>
        </div>
        <div class="filters">
          <el-select v-model="filters.category" clearable placeholder="全部业务" @change="load">
            <el-option v-for="item in categoryOptions" :key="item" :label="item" :value="item" />
          </el-select>
          <el-select v-model="filters.severity" clearable placeholder="全部优先级" @change="load">
            <el-option label="需要立即处理" value="critical" />
            <el-option label="等待收敛" value="warning" />
            <el-option label="其他待办" value="info" />
          </el-select>
        </div>
      </div>

      <el-table :data="items" v-loading="loading" stripe class="worklist-table" empty-text="当前没有需要关注的事项">
        <el-table-column label="优先级" width="120">
          <template #default="{ row }"><el-tag :type="severityType(row.severity)">{{ severityLabel(row.severity) }}</el-tag></template>
        </el-table-column>
        <el-table-column prop="category" label="业务" width="120" />
        <el-table-column label="事项" min-width="230">
          <template #default="{ row }">
            <div class="item-title">{{ row.title }}</div>
            <div class="item-source">{{ sourceLabel(row.source) }} · #{{ row.id }}</div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="130"><template #default="{ row }"><el-tag effect="plain">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="说明" min-width="300" show-overflow-tooltip><template #default="{ row }">{{ row.description || '暂无补充说明' }}</template></el-table-column>
        <el-table-column label="更新时间" width="180"><template #default="{ row }">{{ formatTime(row.updated_at || row.created_at) }}</template></el-table-column>
        <el-table-column label="处理" width="150" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.source === 'xiaohongshu_voucher_verification'" link type="primary" @click="openResolution(row)">人工处置</el-button>
            <el-button v-else-if="row.source === 'xiaohongshu_refund_coordination'" link type="primary" @click="openRefundResolution(row)">人工处置</el-button>
            <el-button v-else-if="row.action_route" link type="primary" @click="goTo(row.action_route)">{{ row.action_label || '进入处理' }}</el-button>
            <span v-else class="muted">-</span>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <el-dialog v-model="resolution.visible" :title="resolution.kind === 'refund' ? '小红书售后人工处置' : '小红书核销人工处置'" width="540px" :close-on-click-modal="false">
      <div v-if="resolution.item" class="resolution-context">
        <div class="resolution-context-title">{{ resolution.item.title }}</div>
        <div class="resolution-context-detail">{{ resolution.item.description || '渠道返回结果未知，票权已暂时锁定' }}</div>
      </div>
      <el-form label-position="top" @submit.prevent>
        <el-form-item label="处置决定">
          <el-radio-group v-model="resolution.decision">
            <template v-if="resolution.kind === 'refund'">
              <el-radio-button value="dismiss_no_refund">确认无退款并解除隔离</el-radio-button>
              <el-radio-button value="bind_order_hold">绑定外部订单并保持整单隔离</el-radio-button>
              <el-radio-button value="confirm_external_refund">确认外部退款并继续锁定</el-radio-button>
            </template>
            <template v-else>
              <el-radio-button value="confirm_external">确认渠道已核销</el-radio-button>
              <el-radio-button v-if="resolution.item?.status === 'external_unknown'" value="release_external">确认未核销并释放</el-radio-button>
            </template>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="处理原因" required>
          <el-input v-model="resolution.reason" type="textarea" :rows="3" maxlength="500" show-word-limit placeholder="填写核对依据和处理原因" />
        </el-form-item>
        <el-form-item label="证据引用" required>
          <el-input v-model="resolution.evidence" type="textarea" :rows="2" maxlength="2000" show-word-limit placeholder="填写渠道后台记录、工单号或截图编号" />
        </el-form-item>
        <el-form-item v-if="resolution.decision === 'confirm_external'" label="渠道核销编号" required>
          <el-input v-model="resolution.externalVerifyID" maxlength="100" placeholder="填写小红书返回或后台显示的核销编号" />
        </el-form-item>
        <el-form-item v-if="resolution.kind === 'refund' && resolution.decision === 'bind_order_hold'" label="小红书外部订单号" required>
          <el-input v-model="resolution.externalOrderID" maxlength="100" placeholder="仅用于当前渠道账号内查找订单" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="resolution.visible = false">取消</el-button>
        <el-button type="primary" :loading="resolution.loading" @click="submitResolution">提交处置</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

type ExecutionItem = {
  source: string
  category: string
  id: number
  title: string
  description?: string
  status: string
  severity: string
  action_route?: string
  action_label?: string
  created_at: string
  updated_at: string
}

const router = useRouter()
const loading = ref(false)
const items = ref<ExecutionItem[]>([])
const generatedAt = ref('')
const summary = reactive({ total: 0, critical: 0, warning: 0, info: 0 })
const filters = reactive({ category: '', severity: '' })
const resolution = reactive({
  visible: false,
  loading: false,
  item: null as ExecutionItem | null,
  kind: 'voucher' as 'voucher' | 'refund',
  decision: 'confirm_external',
  reason: '',
  evidence: '',
  externalVerifyID: '',
  externalOrderID: '',
  idempotencyKey: '',
})
const categoryOptions = ['现场设备', '打印', '退款', '支付', '渠道', '渠道核销', '渠道售后', '对账', '住宿预约', '售后', '结算']

const load = async () => {
  loading.value = true
  try {
    const response = await request.get('/execution-center', { params: { category: filters.category || undefined, severity: filters.severity || undefined, limit: 100 } })
    items.value = response.data?.items || []
    generatedAt.value = response.data?.generated_at || ''
    Object.assign(summary, response.data?.summary || { total: 0, critical: 0, warning: 0, info: 0 })
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '执行中心加载失败')
  } finally {
    loading.value = false
  }
}

const goTo = (path: string) => router.push(path)
const openResolution = (item: ExecutionItem) => {
  resolution.item = item
  resolution.kind = 'voucher'
  resolution.visible = true
  resolution.loading = false
  resolution.decision = 'confirm_external'
  resolution.reason = ''
  resolution.evidence = ''
  resolution.externalVerifyID = ''
  resolution.externalOrderID = ''
  resolution.idempotencyKey = ''
}
const openRefundResolution = (item: ExecutionItem) => {
  resolution.item = item
  resolution.kind = 'refund'
  resolution.visible = true
  resolution.loading = false
  resolution.decision = 'dismiss_no_refund'
  resolution.reason = ''
  resolution.evidence = ''
  resolution.externalVerifyID = ''
  resolution.externalOrderID = ''
  resolution.idempotencyKey = `xhs-refund-resolution-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
const submitResolution = async () => {
  if (!resolution.item) return
  if (!resolution.reason.trim() || !resolution.evidence.trim() || (resolution.kind === 'voucher' && resolution.decision === 'confirm_external' && !resolution.externalVerifyID.trim()) || (resolution.kind === 'refund' && resolution.decision === 'bind_order_hold' && !resolution.externalOrderID.trim())) {
    ElMessage.warning(resolution.kind === 'refund' ? '请完整填写处置决定、原因和证据' : '请完整填写处置决定、原因、证据和渠道核销编号')
    return
  }
  resolution.loading = true
  try {
    if (resolution.kind === 'voucher') {
      await request.post(`/xiaohongshu-voucher-verifications/${resolution.item.id}/resolve`, {
        decision: resolution.decision,
        reason: resolution.reason.trim(),
        evidence: resolution.evidence.trim(),
        external_verify_id: resolution.decision === 'confirm_external' ? resolution.externalVerifyID.trim() : undefined,
      })
    } else {
      await request.post(`/xiaohongshu-refund-coordinations/${resolution.item.id}/resolve`, {
        action: resolution.decision,
        external_order_id: resolution.decision === 'bind_order_hold' ? resolution.externalOrderID.trim() : undefined,
        reason: resolution.reason.trim(),
        evidence: resolution.evidence.trim(),
        idempotency_key: resolution.idempotencyKey,
      })
    }
    ElMessage.success('人工处置已提交')
    resolution.visible = false
    await load()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '人工处置失败，请稍后重试')
  } finally {
    resolution.loading = false
  }
}
const formatTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const severityLabel = (value: string) => ({ critical: '立即处理', warning: '等待收敛', info: '待关注' } as Record<string, string>)[value] || value || '未知'
const severityType = (value: string) => value === 'critical' ? 'danger' : value === 'warning' ? 'warning' : 'info'
const statusLabel = (value: string) => ({ open: '待处理', queued: '排队中', printing: '打印中·待确认', pending: '待执行', processing: '处理中', submitted: '渠道处理中', failed: '失败', unknown: '物理结果未知·需人工确认', external_unknown: '渠道结果未知·需人工确认', external_confirmed: '渠道已确认·待本地收尾', external_rejected: '渠道未核销·已释放', received_unmapped: '售后待匹配', order_held: '整单隔离', external_refund_confirmed: '外部退款已确认·继续锁定', local_completed: '已完成', manual_review: '人工复核', retryable: '可重试', needs_review: '待复核', remote_succeeded: '平台已成功', confirm_pending: '本地待收尾', compensation_pending: '补偿待处理', disputed: '存在争议' } as Record<string, string>)[value] || value || '未知'
const sourceLabel = (value: string) => ({ device_alert: '设备告警', device_verification: '闸机核验', print_job: '打印任务', digital_refund: '退款任务', payment_reconciliation: '支付查单', ctrip_outbound: '携程出站', channel_request: '渠道请求', channel_reconciliation: '渠道对账', xiaohongshu_voucher_verification: '小红书核销', xiaohongshu_refund_coordination: '小红书售后', xiaohongshu_booking: '小红书预约', xiaohongshu_order: '小红书订单', after_sale: '售后请求', settlement: '结算单' } as Record<string, string>)[value] || value

onMounted(load)
</script>

<style scoped>
.execution-page { max-width: 1500px; margin: 0 auto; color: #172033; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 24px; margin-bottom: 22px; }
.page-header h1 { margin: 0; font-size: 24px; line-height: 1.25; font-weight: 700; letter-spacing: -.02em; }
.page-header p { margin: 8px 0 0; color: #6b7280; font-size: 14px; }
.summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin-bottom: 18px; }
.summary-card { padding: 18px 20px; border: 1px solid #e6eaf0; border-radius: 14px; background: #fff; box-shadow: 0 5px 18px rgba(28, 39, 61, .04); }
.summary-card span, .summary-card small { display: block; color: #6b7280; font-size: 13px; }
.summary-card strong { display: block; margin: 7px 0 3px; color: #172033; font-size: 30px; line-height: 1; }
.summary-card-total { border-top: 3px solid #3b82f6; }
.summary-card-critical { border-top: 3px solid #ef4444; }
.summary-card-warning { border-top: 3px solid #f59e0b; }
.summary-card-info { border-top: 3px solid #94a3b8; }
.worklist-panel { padding: 20px; border: 1px solid #e6eaf0; border-radius: 14px; background: #fff; box-shadow: 0 5px 18px rgba(28, 39, 61, .04); }
.worklist-toolbar { display: flex; justify-content: space-between; align-items: center; gap: 16px; margin-bottom: 16px; }
.worklist-toolbar h2 { margin: 0; font-size: 17px; font-weight: 650; }
.worklist-toolbar span { color: #9aa3b2; font-size: 12px; }
.filters { display: flex; gap: 10px; }
.filters .el-select { width: 145px; }
.item-title { color: #172033; font-weight: 600; }
.item-source { margin-top: 4px; color: #9aa3b2; font-size: 12px; }
.resolution-context { margin-bottom: 18px; padding: 12px 14px; border: 1px solid #e6eaf0; border-radius: 8px; background: #f8fafc; }
.resolution-context-title { color: #172033; font-weight: 600; }
.resolution-context-detail { margin-top: 5px; color: #6b7280; font-size: 13px; line-height: 1.5; }
.muted { color: #a0a8b5; }
@media (max-width: 900px) {
  .summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .worklist-toolbar { align-items: stretch; flex-direction: column; }
  .filters { width: 100%; }
  .filters .el-select { flex: 1; width: auto; }
}
@media (max-width: 520px) {
  .page-header h1 { font-size: 21px; }
  .summary-grid { gap: 10px; }
  .summary-card { padding: 14px; }
  .summary-card strong { font-size: 25px; }
  .worklist-panel { padding: 12px; }
}
</style>
