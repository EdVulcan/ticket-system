<template>
  <el-dialog v-model="visible" title="申请原路退款" width="520px" append-to-body :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting">
    <div v-loading="loading">
      <el-descriptions v-if="order" :column="1" border>
        <el-descriptions-item label="订单号">{{ order.order_no }}</el-descriptions-item>
        <el-descriptions-item label="退款范围">整单 {{ tickets.length }} 张票</el-descriptions-item>
        <el-descriptions-item label="申请金额">¥{{ Number(order.total_amount || 0).toFixed(2) }}</el-descriptions-item>
      </el-descriptions>
      <p class="refund-note">款项退回原支付渠道。提交后需要等待渠道确认，不代表已经到账；退票规则及可退金额以服务端校验为准。</p>
      <el-alert v-if="unavailable" :title="unavailable" type="warning" :closable="false" show-icon />
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="refund-error" />
      <el-form label-position="top" class="refund-form">
        <el-form-item label="退款原因" required>
          <el-input v-model="reason" type="textarea" :rows="3" maxlength="255" show-word-limit :disabled="attempted" placeholder="请填写游客申请退票的原因" />
        </el-form-item>
      </el-form>
    </div>
    <template #footer>
      <el-button :disabled="submitting" @click="visible = false">{{ submitted ? '关闭' : '取消' }}</el-button>
      <el-button v-if="!submitted" type="primary" :loading="submitting" :disabled="loading || !!unavailable || !order || !reason.trim()" @click="submit">确认申请退款</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const emit = defineEmits<{ changed: [] }>()
const visible = ref(false)
const loading = ref(false)
const submitting = ref(false)
const submitted = ref(false)
const attempted = ref(false)
const order = ref<any>(null)
const reason = ref('')
const error = ref('')
const unavailable = ref('')
const tickets = computed<any[]>(() => (order.value?.items || []).flatMap((item: any) => item.tickets || []))
let requestKey = ''
let payload: Record<string, unknown> | null = null
let loadVersion = 0

// Read fresh scoped facts before confirming. All authorization, sale-time
// policy, amount and ticket reservation checks remain in RefundService.
const open = async (orderNo: string, detailURL?: string) => {
  if (submitting.value) return
  const version = ++loadVersion
  visible.value = true
  loading.value = true
  submitted.value = false
  attempted.value = false
  order.value = null
  reason.value = ''
  error.value = ''
  unavailable.value = ''
  requestKey = `admin-refund-${orderNo}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  payload = null
  try {
    const { data } = await request.get(detailURL || `/orders/${encodeURIComponent(orderNo)}`)
    if (version !== loadVersion) return
    order.value = data.order
    if (!order.value || order.value.order_no !== orderNo) throw new Error('订单详情不匹配，请重新打开')
    if (order.value.environment === 'sandbox') unavailable.value = '沙箱订单不发起真实资金退款'
    else if (order.value.status !== 'paid') unavailable.value = '此入口只办理已支付、未使用订单的整单退款；其他售后请在售后工作台处理'
    else if (!tickets.value.length) unavailable.value = '票券尚未完整签发，请先刷新订单或核查出票状态'
    else if (tickets.value.some(ticket => ticket.status !== 'unused' || Number(ticket.check_in_count || 0) > 0)) unavailable.value = '订单包含已使用或不可退票券，请在售后工作台核查'
    else if (tickets.value.some(ticket => Number(ticket.pending_refund_id || 0) || Number(ticket.pending_xiaohongshu_verification_id || 0)) ||
      (data.refunds || []).some((refund: any) => ['pending', 'group_pending', 'processing', 'submitted', 'manual_review'].includes(refund.status))) unavailable.value = '订单正在退款或核销处理中，请勿重复申请'
  } catch (cause: any) {
    if (version === loadVersion) {
      order.value = null
      error.value = cause.response?.data?.error || cause.message || '订单详情加载失败，请重新打开'
    }
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

const submit = async () => {
  if (loading.value || submitting.value || submitted.value || unavailable.value || !order.value || !reason.value.trim()) return
  // A retry within this dialog replays the same request, even when its response
  // was lost. Do not turn a timeout into a second refund application.
  if (!payload) payload = { order_no: order.value.order_no, idempotency_key: requestKey,
    amount: order.value.total_amount, ticket_codes: tickets.value.map(ticket => ticket.ticket_code), reason: reason.value.trim() }
  submitting.value = true
  attempted.value = true
  error.value = ''
  try {
    const { data } = await request.post('/payments/refunds/mixed', payload)
    submitted.value = true
    if (['succeeded', 'group_succeeded'].includes(data.status)) ElMessage.success('退款已完成')
    else ElMessage.info('退款申请已提交，等待原支付渠道确认，请刷新查看进度')
    visible.value = false
    emit('changed')
  } catch (cause: any) {
    error.value = cause.response?.data?.error || '退款结果暂未确认，可在此重试同一申请，或关闭后先查询订单退款记录'
  } finally { submitting.value = false }
}

defineExpose({ open })
</script>

<style scoped>
.refund-note { margin: 16px 0; color: #64748b; line-height: 1.7; }
.refund-error, .refund-form { margin-top: 16px; }
</style>
