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
      <el-alert v-if="uncertain" title="退款结果暂未确认，请先查询退款结果，勿重复创建申请。" type="info" :closable="false" show-icon class="refund-error" />
      <div v-if="needsPolicyOverride && canOverridePolicy && !unavailable" class="refund-error">
        <el-alert title="该订单购买时设置为不可退。例外退款不会修改原销售规则，仍须通过票券及支付渠道校验，并保留操作人和原因。" type="warning" :closable="false" show-icon />
        <el-checkbox v-model="overridePolicy" :disabled="attempted || submitting">以初始管理员身份申请例外退款</el-checkbox>
      </div>
      <el-form label-position="top" class="refund-form">
        <el-form-item label="退款原因" required>
          <el-input v-model="reason" type="textarea" :rows="3" maxlength="255" show-word-limit :disabled="attempted || submitting" :placeholder="needsPolicyOverride ? '请填写本次例外退款的具体原因' : '请填写游客申请退票的原因'" />
        </el-form-item>
      </el-form>
    </div>
    <template #footer>
      <el-button :disabled="submitting" @click="visible = false">{{ attempted ? '关闭' : '取消' }}</el-button>
      <el-button v-if="uncertain" type="primary" :loading="checking" :disabled="submitting" @click="queryResult">查询退款结果</el-button>
      <el-button v-if="!submitted" :type="uncertain ? 'default' : 'primary'" :loading="submitting" :disabled="checking || loading || !!unavailable || !order || !reason.trim() || (needsPolicyOverride && (!canOverridePolicy || !overridePolicy))" @click="submit">{{ uncertain ? '重试同一申请' : needsPolicyOverride ? '确认例外退款' : '确认申请退款' }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { hasPermission } from '@/utils/permissions'
import { isScenicHistorySupplier, readStoredUser } from '@/utils/tenantAccess'

const emit = defineEmits<{ changed: [] }>()
const visible = ref(false)
const loading = ref(false)
const submitting = ref(false)
const submitted = ref(false)
const attempted = ref(false)
const uncertain = ref(false)
const checking = ref(false)
const order = ref<any>(null)
const reason = ref('')
const error = ref('')
const unavailable = ref('')
const currentUser = ref(readStoredUser())
const overridePolicy = ref(false)
const needsPolicyOverride = computed(() => (order.value?.items || []).some((item: any) => item.refund_type === 'no_refund'))
// Visibility mirrors existing authority; RefundService reauthorizes every request.
const canOverridePolicy = computed(() => currentUser.value.is_initial_admin === true &&
  hasPermission(currentUser.value, 'refunds.write') && isScenicHistorySupplier(currentUser.value) &&
  Number(currentUser.value.tenant_id) > 0 && Number(order.value?.tenant_id) === Number(currentUser.value.tenant_id))
const tickets = computed<any[]>(() => (order.value?.items || []).flatMap((item: any) => item.tickets || []))
let requestKey = ''
let payload: Record<string, unknown> | null = null
let loadVersion = 0
let orderDetailURL = ''

// Read fresh scoped facts before confirming. All authorization, sale-time
// policy, amount and ticket reservation checks remain in RefundService.
const open = async (orderNo: string, detailURL?: string) => {
  if (submitting.value || checking.value) return
  const version = ++loadVersion
  visible.value = true
  loading.value = true
  submitted.value = false
  attempted.value = false
  uncertain.value = false
  overridePolicy.value = false
  currentUser.value = readStoredUser()
  order.value = null
  reason.value = ''
  error.value = ''
  unavailable.value = ''
  requestKey = `admin-refund-${orderNo}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
  payload = null
  orderDetailURL = detailURL || `/orders/${encodeURIComponent(orderNo)}`
  try {
    const { data } = await request.get(orderDetailURL)
    if (version !== loadVersion) return
    order.value = data.order
    if (!order.value || order.value.order_no !== orderNo) throw new Error('订单详情不匹配，请重新打开')
    if (order.value.environment === 'sandbox') unavailable.value = '沙箱订单不发起真实资金退款'
    else if (order.value.status !== 'paid') unavailable.value = '此入口只办理已支付、未使用订单的整单退款；其他售后请在售后工作台处理'
    else if (!tickets.value.length) unavailable.value = '票券尚未完整签发，请先刷新订单或核查出票状态'
    else if (tickets.value.some(ticket => typeof ticket.ticket_code !== 'string' || !ticket.ticket_code.trim() || !Number.isFinite(ticket.check_in_count))) unavailable.value = '票码或核销信息不完整，请先刷新订单或核查出票状态'
    else if (tickets.value.some(ticket => ticket.status !== 'unused' || Number(ticket.check_in_count || 0) > 0)) unavailable.value = '订单包含已使用或不可退票券，请在售后工作台核查'
    else if (tickets.value.some(ticket => Number(ticket.pending_refund_id || 0) || Number(ticket.pending_xiaohongshu_verification_id || 0)) ||
      (data.refunds || []).some((refund: any) => ['pending', 'group_pending', 'processing', 'submitted', 'manual_review'].includes(refund.status))) unavailable.value = '订单正在退款或核销处理中，请勿重复申请'
    else if (needsPolicyOverride.value && order.value.environment !== 'production') unavailable.value = '订单环境信息不完整，不能申请例外退款'
    else if (needsPolicyOverride.value && !canOverridePolicy.value) unavailable.value = '该订单购买时不可退，仅本商户景区初始管理员可申请例外退款'
  } catch (cause: any) {
    if (version === loadVersion) {
      order.value = null
      error.value = cause.response?.data?.error || cause.message || '订单详情加载失败，请重新打开'
    }
  } finally {
    if (version === loadVersion) loading.value = false
  }
}

// Only an authoritative refund result can confirm receipt or completion.
const showResult = (status: string) => {
  const completed = ['succeeded', 'group_succeeded'].includes(status)
  const pending = ['pending', 'group_pending', 'processing', 'submitted'].includes(status)
  if (!completed && !pending) return false
  uncertain.value = false
  submitted.value = true
  error.value = ''
  if (completed) ElMessage.success('退款已完成')
  else ElMessage.info('退款申请已提交，等待原支付渠道确认，请刷新查看进度')
  visible.value = false
  emit('changed')
  return true
}

const queryResult = async () => {
  if (checking.value || !payload) return
  const version = loadVersion
  checking.value = true
  try {
    const { data } = await request.get(orderDetailURL, { skipErrorToast: true } as any)
    if (version !== loadVersion || !visible.value) return
    if (data?.order?.order_no !== payload.order_no) return
    const refund = (Array.isArray(data.refunds) ? data.refunds : []).find((item: any) =>
      item.order_no === payload!.order_no && item.idempotency_key === requestKey)
    if (refund && showResult(refund.status)) return
    if (refund && ['failed', 'manual_review', 'group_failed'].includes(refund.status)) {
      uncertain.value = false
      submitted.value = true
      error.value = '该退款申请未完成或需要人工复核，请在退款任务中查看处理，勿重复创建申请'
    }
  } catch {
    // A read failure says nothing about whether the original refund committed.
  } finally { checking.value = false }
}

const submit = async () => {
  if (checking.value || loading.value || submitting.value || submitted.value || unavailable.value || !order.value || !reason.value.trim() ||
    (needsPolicyOverride.value && (!canOverridePolicy.value || !overridePolicy.value))) return
  submitting.value = true
  if (!payload && needsPolicyOverride.value) {
    try {
      await ElMessageBox.confirm(`订单 ${order.value.order_no} 将申请整单原路退款 ¥${Number(order.value.total_amount || 0).toFixed(2)}。例外原因：${reason.value.trim()}。本操作将记录审计，渠道确认成功后才算退款完成。`, '确认管理员例外退款', {
        confirmButtonText: '确认提交例外退款', cancelButtonText: '返回检查', type: 'warning',
      })
    } catch {
      submitting.value = false
      return
    }
  }
  // A retry within this dialog replays the same request, even when its response
  // was lost. Do not turn a timeout into a second refund application.
  if (!payload) payload = { order_no: order.value.order_no, idempotency_key: requestKey,
    amount: order.value.total_amount, ticket_codes: tickets.value.map(ticket => ticket.ticket_code), reason: reason.value.trim(),
    ...(needsPolicyOverride.value ? { override_refund_policy: true } : {}) }
  attempted.value = true
  error.value = ''
  try {
    const { data } = await request.post('/payments/refunds/mixed', payload, { skipErrorToast: true } as any)
    if (showResult(data?.status)) return
    uncertain.value = true
    await queryResult()
  } catch (cause: any) {
    const status = cause.response?.status
    const message = cause.response?.data?.error
    const genericFailure = ['请求失败，请稍后重试', '操作失败，请稍后重试', '请求超时，请稍后重试', '网络连接失败，请检查网络后重试'].includes(message)
    if ([400, 401, 403, 404, 409, 422].includes(status) && typeof message === 'string' && message.trim() && !genericFailure) {
      uncertain.value = false
      error.value = cause.response.data.error
    } else {
      uncertain.value = true
      await queryResult()
    }
  } finally { submitting.value = false }
}

defineExpose({ open })
</script>

<style scoped>
.refund-note { margin: 16px 0; color: #64748b; line-height: 1.7; }
.refund-error, .refund-form { margin-top: 16px; }
</style>
