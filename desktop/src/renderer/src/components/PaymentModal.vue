<template>
  <div class="payment-container">
    <div class="amount-due"><span>本次应收</span><strong>¥{{ amount.toFixed(2) }}</strong></div>

    <div class="mode-switch">
      <button type="button" :class="{ active: paymentMode === 'single' }" :disabled="partialCashRecorded || loading || !!paymentId" @click="setMode('single')">单一支付</button>
      <button type="button" :class="{ active: paymentMode === 'combined' }" :disabled="partialCashRecorded || loading || !!paymentId" @click="setMode('combined')">现金 + 付款码</button>
    </div>

    <div v-if="paymentMode === 'combined'" class="split-panel">
      <div class="split-input">
        <label>现金部分</label>
        <el-input-number v-model="cashPortion" :min="0.01" :max="Math.max(0.01, amount - 0.01)" :precision="2" :step="10" :controls="false" :disabled="partialCashRecorded" />
      </div>
      <div class="split-remainder"><span>付款码余款</span><strong>¥{{ digitalAmount.toFixed(2) }}</strong></div>
    </div>

    <div class="method-grid" :class="{ compact: paymentMode === 'combined' }">
      <button
        v-for="method in availableMethods"
        :key="method.key"
        type="button"
        class="method-button"
        :class="{ active: selectedMethod === method.key }"
        :disabled="loading || !!paymentId"
        @click="selectMethod(method.key)"
      >
        <el-icon :size="22"><component :is="method.icon" /></el-icon>
        <span>{{ method.label }}</span>
      </button>
    </div>

    <div v-if="showCashPanel" class="cash-panel">
      <label>顾客交付现金</label>
      <div class="cash-input-row">
        <span>¥</span>
        <el-input-number v-model="cashTendered" :min="0" :precision="2" :step="10" :controls="false" :disabled="partialCashRecorded" />
      </div>
      <div class="cash-presets">
        <button v-for="value in cashQuickAmounts" :key="value" type="button" :disabled="partialCashRecorded" @click="cashTendered = value">¥{{ value.toFixed(2) }}</button>
      </div>
      <div class="change-row" :class="{ insufficient: cashChange < 0 }">
        <span>{{ cashChange < 0 ? '还差' : '应找零' }}</span>
        <strong>¥{{ Math.abs(cashChange).toFixed(2) }}</strong>
      </div>
    </div>

    <div v-if="partialCashRecorded" class="cash-recorded">
      <div><span>现金已收</span><strong>¥{{ cashPortion.toFixed(2) }}</strong></div>
      <p>请完成数字余款；如顾客放弃，必须退回现金并登记原因。</p>
    </div>

    <div v-if="selectedMethod === 'payment_code'" class="scan-panel">
      <div>扫描顾客的微信或支付宝付款码</div>
      <el-input ref="scanInputRef" v-model="authCode" size="large" placeholder="等待扫码" :disabled="loading || !!paymentId" @keyup.enter="doPay" />
      <small>系统自动识别支付渠道；尚未完成协议联调的渠道会明确拒绝，不会记录为支付成功。</small>
    </div>

    <div v-if="selectedMethod === 'pos'" class="pos-panel">
      <strong>备用 POS 机收款</strong>
      <p>请先在外部 POS 机上完成收款，确认设备显示支付成功后，再在这里登记入账。</p>
    </div>

    <el-button
      v-if="canStartPayment"
      type="success"
      class="pay-button"
      size="large"
      :loading="loading"
      :disabled="payDisabled"
      @click="doPay"
    >
      {{ payButtonLabel }}
    </el-button>

    <el-button v-if="partialCashRecorded && !paymentId" class="return-button" :loading="cancelling" @click="returnCashAndCancel">退回现金并取消订单</el-button>

    <div v-if="loading || paymentId" class="payment-waiting">
      <el-icon class="is-loading"><Loading /></el-icon>
      <span>{{ paymentId ? '等待支付结果...' : '正在创建支付...' }}</span>
    </div>
    <el-button v-if="paymentId && errorMsg" class="query-button" @click="retryQuery">重新查询支付结果</el-button>
    <div v-if="errorMsg" class="error-message">{{ errorMsg }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { Aim, CreditCard, Loading, Money } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'

const props = defineProps<{ amount: number; orderNo: string; shiftId: number; deviceId: number }>()
const emit = defineEmits<{ success: []; cancelled: []; lockChange: [locked: boolean] }>()

const methods = [
  { key: 'payment_code', label: '付款码收款', icon: Aim },
  { key: 'cash', label: '现金', icon: Money },
  { key: 'pos', label: 'POS机收款', icon: CreditCard }
]
const digitalMethods = methods.filter(method => method.key === 'payment_code')
const paymentMode = ref<'single' | 'combined'>('single')
const selectedMethod = ref('payment_code')
const authCode = ref('')
const cashPortion = ref(Math.max(0.01, Math.floor((props.amount / 2) * 100) / 100))
const cashTendered = ref(props.amount)
const partialCashRecorded = ref(false)
const scanInputRef = ref()
const loading = ref(false)
const cancelling = ref(false)
const paymentId = ref<number | null>(null)
const errorMsg = ref('')
const cashRequestKey = ref('')
const digitalRequestKey = ref('')

const availableMethods = computed(() => paymentMode.value === 'combined' ? digitalMethods : methods)
const digitalAmount = computed(() => Math.max(0, Math.round((props.amount - cashPortion.value) * 100) / 100))
const cashDue = computed(() => paymentMode.value === 'combined' ? cashPortion.value : props.amount)
const cashChange = computed(() => cashTendered.value - cashDue.value)
const showCashPanel = computed(() => selectedMethod.value === 'cash' || (paymentMode.value === 'combined' && !partialCashRecorded.value))
const canStartPayment = computed(() => ['cash', 'payment_code', 'pos'].includes(selectedMethod.value))
const payDisabled = computed(() => {
  if (cashChange.value < 0 && showCashPanel.value) return true
  if (paymentMode.value === 'combined' && (cashPortion.value <= 0 || digitalAmount.value <= 0)) return true
  return selectedMethod.value === 'payment_code' && !authCode.value.trim()
})
const payButtonLabel = computed(() => {
  if (paymentMode.value === 'combined') return partialCashRecorded.value ? `收取付款码余款 ¥${digitalAmount.value.toFixed(2)}` : `收取现金并继续付款码 ¥${digitalAmount.value.toFixed(2)}`
  if (selectedMethod.value === 'cash') return '确认现金收款'
  if (selectedMethod.value === 'pos') return '确认POS机已收款'
  return '确认付款码收款'
})
const cashQuickAmounts = computed(() => {
  const due = cashDue.value
  const roundedTen = Math.ceil(due / 10) * 10
  return [...new Set([due, roundedTen, 50, 100, 200, 500].filter(value => value >= due))].slice(0, 5)
})

let pollTimer: ReturnType<typeof setTimeout> | undefined
let pollDeadline = 0

const requestKey = (prefix: string) => `${prefix}-${props.orderNo}-${globalThis.crypto?.randomUUID?.() || Date.now()}`
const stopPolling = () => { if (pollTimer) clearTimeout(pollTimer); pollTimer = undefined }
const resetProviderAttempt = () => { stopPolling(); paymentId.value = null; loading.value = false }

const setMode = (mode: 'single' | 'combined') => {
  if (partialCashRecorded.value) return
  resetProviderAttempt()
  paymentMode.value = mode
  selectedMethod.value = 'payment_code'
  cashTendered.value = mode === 'single' ? props.amount : cashPortion.value
  errorMsg.value = ''
}

const selectMethod = async (key: string) => {
  resetProviderAttempt()
  selectedMethod.value = key
  authCode.value = ''
  if (key === 'cash') cashTendered.value = props.amount
  if (key === 'payment_code') await nextTick(() => scanInputRef.value?.focus())
}

const createPayment = async (methodKey: string, amountCents: number, idempotencyKey: string, tenderedCents = 0) => {
  const isScan = methodKey === 'payment_code'
  const method = isScan ? 'auto' : methodKey
  const response = await axios.post('/payments/pay', {
    order_no: props.orderNo,
    method,
    pay_type: isScan ? 'bscanc' : methodKey,
    auth_code: isScan ? authCode.value.trim() : '',
    shift_id: props.shiftId,
    device_id: props.deviceId,
    amount_cents: amountCents,
    cash_tendered_cents: tenderedCents,
    idempotency_key: idempotencyKey
  })
  return response.data
}

const beginProviderWait = async (payment: any) => {
  if (payment.status === 'paid') { finishSuccess(); return }
  paymentId.value = payment.id
  loading.value = false
  pollDeadline = Date.now() + 5 * 60 * 1000
  schedulePoll()
}

const restorePendingAttempt = async () => {
  try {
    const { data } = await axios.get(`/payments/orders/${encodeURIComponent(props.orderNo)}`)
    partialCashRecorded.value = Boolean(data.has_partial_cash)
    emit('lockChange', partialCashRecorded.value)
    const pending = data.payments?.find((payment: any) => payment.status === 'pending')
    if (partialCashRecorded.value) {
      paymentMode.value = 'combined'
      cashPortion.value = Number(data.payments?.filter((payment: any) => payment.method === 'cash' && ['paid', 'partial_refunded'].includes(payment.status)).reduce((sum: number, payment: any) => sum + Number(payment.amount_cents || 0), 0) || 0) / 100
      cashTendered.value = cashPortion.value
      selectedMethod.value = 'payment_code'
    }
    if (pending) {
      paymentId.value = pending.id
      pollDeadline = Date.now() + 5 * 60 * 1000
      schedulePoll()
      return true
    }
  } catch {
    // Keep the original provider error visible when progress lookup also fails.
  }
  return false
}

const doPay = async () => {
  if (loading.value || paymentId.value || payDisabled.value) return
  loading.value = true
  errorMsg.value = ''
  try {
    if (paymentMode.value === 'combined' && !partialCashRecorded.value) {
      if (!cashRequestKey.value) cashRequestKey.value = requestKey('cash')
      await createPayment('cash', Math.round(cashPortion.value * 100), cashRequestKey.value, Math.round(cashTendered.value * 100))
      partialCashRecorded.value = true
      emit('lockChange', true)
    }
    const amountCents = paymentMode.value === 'combined' ? Math.round(digitalAmount.value * 100) : Math.round(props.amount * 100)
    if (selectedMethod.value === 'cash' || selectedMethod.value === 'pos') {
      const method = selectedMethod.value
      const tenderedCents = method === 'cash' ? Math.round(cashTendered.value * 100) : 0
      const payment = await createPayment(method, amountCents, requestKey(`${method}-full`), tenderedCents)
      if (payment.status === 'paid') finishSuccess()
      return
    }
    digitalRequestKey.value = requestKey('digital')
    const payment = await createPayment(selectedMethod.value, amountCents, digitalRequestKey.value)
    await beginProviderWait(payment)
  } catch (error: any) {
    resetProviderAttempt()
    const message = error.response?.data?.error || error.message || '支付请求失败'
    const restored = paymentMode.value === 'combined' ? await restorePendingAttempt() : false
    errorMsg.value = restored ? '数字支付结果待确认，系统正在持续查询，请勿重复收款。' : message
  }
}

const schedulePoll = () => { stopPolling(); pollTimer = setTimeout(pollStatus, 1500) }
const retryQuery = () => { errorMsg.value = ''; pollDeadline = Date.now() + 5 * 60 * 1000; void pollStatus() }
const pollStatus = async () => {
  if (!paymentId.value) return
  if (Date.now() > pollDeadline) {
    stopPolling()
    errorMsg.value = '支付等待超时，结果仍待确认；请勿退回现金或重复收款。'
    return
  }
  try {
    const response = await axios.get(`/payments/${paymentId.value}`)
    if (response.data.status === 'paid') { finishSuccess(); return }
    if (response.data.status === 'failed') {
      const message = response.data.error_message || '支付平台返回失败'
      resetProviderAttempt()
      errorMsg.value = `数字支付失败，可重试余款或退回现金：${message}`
      return
    }
  } catch {
    // Temporary query errors retain the provider attempt and continue polling.
  }
  schedulePoll()
}

const finishSuccess = () => {
  stopPolling()
  loading.value = false
  paymentId.value = null
  partialCashRecorded.value = false
  emit('lockChange', false)
  ElMessage.success('支付成功')
  emit('success')
}

const returnCashAndCancel = async () => {
  try {
    const input = await ElMessageBox.prompt('请填写退回现金并取消订单的原因', '退回现金', { confirmButtonText: '确认已退回', cancelButtonText: '继续收款', inputPattern: /\S+/, inputErrorMessage: '必须填写原因', type: 'warning' })
    cancelling.value = true
    await axios.post(`/payments/orders/${encodeURIComponent(props.orderNo)}/cancel-partial-cash`, { shift_id: props.shiftId, device_id: props.deviceId, reason: input.value.trim() })
    partialCashRecorded.value = false
    emit('lockChange', false)
    ElMessage.success('现金已登记退回，订单已取消')
    emit('cancelled')
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') errorMsg.value = error.response?.data?.error || '退回现金登记失败'
  } finally {
    cancelling.value = false
  }
}

watch(cashPortion, value => { if (!partialCashRecorded.value && paymentMode.value === 'combined') cashTendered.value = value })
onMounted(async () => {
  cashTendered.value = props.amount
  const restored = await restorePendingAttempt()
  if (!restored && selectedMethod.value === 'payment_code') await nextTick(() => scanInputRef.value?.focus())
})
onUnmounted(stopPolling)
</script>

<style scoped>
.payment-container { color: #252a25; }
.amount-due { display: flex; align-items: flex-end; justify-content: space-between; padding: 4px 2px 14px; }
.amount-due span { color: #6c746c; font-size: 13px; }
.amount-due strong { color: #b65b0b; font-size: 30px; line-height: 34px; font-variant-numeric: tabular-nums; }
.mode-switch { display: grid; grid-template-columns: 1fr 1fr; padding: 3px; margin-bottom: 14px; border: 1px solid #d8ddd6; border-radius: 7px; background: #f2f4f1; }
.mode-switch button { height: 34px; border: 0; border-radius: 5px; background: transparent; color: #667066; cursor: pointer; }
.mode-switch button.active { background: #fff; color: #12683f; font-weight: 700; box-shadow: 0 1px 3px rgba(24, 50, 33, .12); }
.split-panel { display: grid; grid-template-columns: 1.2fr 1fr; gap: 10px; margin-bottom: 12px; }
.split-input, .split-remainder { min-height: 68px; padding: 10px 12px; border: 1px solid #dce1da; border-radius: 7px; background: #fafbf9; }
.split-input label, .split-remainder span { display: block; margin-bottom: 6px; color: #707870; font-size: 12px; }
.split-input :deep(.el-input-number) { width: 100%; }
.split-input :deep(.el-input__inner) { text-align: left; font-weight: 700; }
.split-remainder strong { color: #12683f; font-size: 21px; font-variant-numeric: tabular-nums; }
.method-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin-bottom: 14px; }
.method-grid.compact { grid-template-columns: 1fr; }
.method-button { min-height: 64px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 5px; padding: 6px 4px; border: 1px solid #d9ded7; border-radius: 7px; background: #f7f8f6; color: #5e665e; cursor: pointer; }
.method-button span { font-size: 11px; line-height: 15px; }
.method-button:hover { border-color: #78a88d; }
.method-button.active { border-color: #16784a; background: #eaf5ee; color: #0f643c; font-weight: 700; }
.cash-panel, .scan-panel { padding: 13px; border: 1px solid #dde2db; border-radius: 7px; background: #fafbf9; }
.cash-panel > label, .scan-panel > div:first-child { display: block; margin-bottom: 8px; color: #616961; font-size: 13px; }
.cash-input-row { height: 48px; display: flex; align-items: center; gap: 8px; padding: 0 14px; border: 2px solid #8aac97; border-radius: 7px; background: #fff; }
.cash-input-row > span { color: #667066; font-size: 20px; }
.cash-input-row :deep(.el-input-number) { width: 100%; }
.cash-input-row :deep(.el-input__wrapper) { box-shadow: none; padding: 0; }
.cash-input-row :deep(.el-input__inner) { text-align: left; font-size: 22px; font-weight: 700; }
.cash-presets { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 9px; }
.cash-presets button { height: 29px; padding: 0 10px; border: 1px solid #d1d7cf; border-radius: 5px; background: #fff; color: #4c544c; cursor: pointer; }
.change-row { display: flex; align-items: center; justify-content: space-between; margin-top: 11px; padding-top: 10px; border-top: 1px solid #e2e6e0; }
.change-row span { color: #6b736b; }
.change-row strong { color: #16784a; font-size: 21px; font-variant-numeric: tabular-nums; }
.change-row.insufficient strong { color: #c23f3f; }
.cash-recorded { margin-bottom: 12px; padding: 10px 12px; border: 1px solid #e1c887; border-radius: 7px; background: #fff9e8; }
.cash-recorded div { display: flex; justify-content: space-between; }
.cash-recorded strong { color: #9b5a06; font-size: 18px; }
.cash-recorded p { margin: 5px 0 0; color: #7b6846; font-size: 12px; }
.pos-panel { padding: 13px 14px; border: 1px solid #d8dfd7; border-radius: 7px; background: #f7f9f6; }
.pos-panel strong { color: #303730; font-size: 14px; }
.pos-panel p { margin: 6px 0 0; color: #687168; font-size: 12px; line-height: 18px; }
.pay-button { width: 100%; height: 46px; margin-top: 14px; border-radius: 7px; font-weight: 700; --el-button-bg-color: #16784a; --el-button-border-color: #16784a; --el-button-hover-bg-color: #0d5d38; --el-button-hover-border-color: #0d5d38; }
.return-button { width: 100%; margin-top: 8px; color: #a53d3d; }
.query-button { width: 100%; margin-top: 8px; }
.payment-waiting { display: flex; align-items: center; gap: 8px; margin-top: 12px; color: #176a8a; }
.error-message { margin-top: 12px; color: #c23f3f; font-size: 13px; line-height: 19px; }
</style>
