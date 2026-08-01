<template>
  <div class="payment-container">
    <div class="amount-due"><span>本次应收</span><strong>¥{{ amount.toFixed(2) }}</strong></div>

    <div class="method-grid">
      <button
        v-for="method in methods"
        :key="method.key"
        type="button"
        class="method-button"
        :class="{ active: selectedMethod === method.key }"
        :disabled="loading"
        @click="selectMethod(method.key)"
      >
        <el-icon :size="22"><component :is="method.icon" /></el-icon>
        <span>{{ method.label }}</span>
      </button>
    </div>

    <div v-if="selectedMethod === 'cash'" class="cash-panel">
      <label>顾客实付</label>
      <div class="cash-input-row">
        <span>¥</span>
        <el-input-number v-model="cashTendered" :min="0" :precision="2" :step="10" :controls="false" />
      </div>
      <div class="cash-presets">
        <button v-for="value in cashQuickAmounts" :key="value" type="button" @click="cashTendered = value">¥{{ value.toFixed(2) }}</button>
      </div>
      <div class="change-row" :class="{ insufficient: cashChange < 0 }">
        <span>{{ cashChange < 0 ? '还差' : '应找零' }}</span>
        <strong>¥{{ Math.abs(cashChange).toFixed(2) }}</strong>
      </div>
    </div>

    <div v-if="selectedMethod === 'alipay_scan'" class="scan-panel">
      <div>扫描顾客的支付宝付款码</div>
      <el-input
        ref="scanInputRef"
        v-model="authCode"
        size="large"
        placeholder="等待扫码"
        :disabled="loading"
        @keyup.enter="doPay"
      />
    </div>

    <div v-if="isQRMethod" class="w-full flex flex-col items-center">
      <div class="qr-frame">
        <img v-if="qrDataURL" :src="qrDataURL" alt="支付二维码" class="w-full h-full" />
        <el-icon v-else-if="loading" class="is-loading text-gray-500" :size="42"><Loading /></el-icon>
        <span v-else class="text-sm text-gray-500">点击下方按钮生成二维码</span>
      </div>
      <div class="qr-caption">请顾客扫码支付</div>
    </div>

    <el-button
      v-if="selectedMethod === 'cash' || selectedMethod === 'alipay_scan' || (isQRMethod && !paymentId)"
      type="success"
      class="pay-button"
      size="large"
      :loading="loading"
      :disabled="(selectedMethod === 'alipay_scan' && !authCode.trim()) || (selectedMethod === 'cash' && cashChange < 0)"
      @click="doPay"
    >
      {{ selectedMethod === 'cash' ? '确认现金收款' : selectedMethod === 'alipay_scan' ? '确认付款码收款' : '生成支付码' }}
    </el-button>

    <div v-if="loading || paymentId" class="mt-4 flex items-center gap-2 text-[#1890ff]">
      <el-icon v-if="loading || paymentId" class="is-loading"><Loading /></el-icon>
      <span>{{ paymentId ? '等待支付结果...' : '正在创建支付...' }}</span>
    </div>
    <div v-if="errorMsg" class="mt-4 text-red-500 text-sm">{{ errorMsg }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { Aim, ChatDotRound, Loading, Money, Wallet } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import QRCode from 'qrcode'
import axios from 'axios'

const props = defineProps<{ amount: number; orderNo: string; shiftId: number; deviceId: number }>()
const emit = defineEmits<{ success: [] }>()

const methods = [
  { key: 'cash', label: '现金', icon: Money },
  { key: 'alipay_scan', label: '支付宝付款码', icon: Aim },
  { key: 'wechat_qr', label: '微信扫码', icon: ChatDotRound },
  { key: 'alipay_qr', label: '支付宝扫码', icon: Wallet }
]

const selectedMethod = ref('cash')
const authCode = ref('')
const cashTendered = ref(props.amount)
const scanInputRef = ref()
const loading = ref(false)
const paymentId = ref<number | null>(null)
const qrDataURL = ref('')
const errorMsg = ref('')
const isQRMethod = computed(() => selectedMethod.value.endsWith('_qr'))
const cashChange = computed(() => cashTendered.value - props.amount)
const cashQuickAmounts = computed(() => {
  const roundedTen = Math.ceil(props.amount / 10) * 10
  return [...new Set([props.amount, roundedTen, 50, 100, 200, 500].filter(value => value >= props.amount))].slice(0, 5)
})

let pollTimer: ReturnType<typeof setTimeout> | undefined
let pollDeadline = 0

const stopPolling = () => {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = undefined
}

const resetAttempt = () => {
  stopPolling()
  paymentId.value = null
  qrDataURL.value = ''
  errorMsg.value = ''
  loading.value = false
}

const selectMethod = async (key: string) => {
  resetAttempt()
  selectedMethod.value = key
  if (key === 'cash') {
    cashTendered.value = props.amount
  } else if (key === 'alipay_scan') {
    await nextTick()
    scanInputRef.value?.focus()
  }
}

const doPay = async () => {
  if (loading.value || paymentId.value) return
  loading.value = true
  errorMsg.value = ''
  try {
    const isScan = selectedMethod.value === 'alipay_scan'
    const method = isScan ? 'alipay' : selectedMethod.value.replace('_qr', '')
    const response = await axios.post('/payments/pay', {
      order_no: props.orderNo,
      method,
      pay_type: isScan ? 'bscanc' : selectedMethod.value === 'cash' ? 'cash' : 'cscanb',
      auth_code: isScan ? authCode.value.trim() : '',
      shift_id: props.shiftId,
      device_id: props.deviceId,
      cash_tendered_cents: selectedMethod.value === 'cash' ? Math.round(cashTendered.value * 100) : 0
    })
    const payment = response.data
    if (payment.status === 'paid') {
      loading.value = false
      ElMessage.success('支付成功')
      emit('success')
      return
    }
    paymentId.value = payment.id
    if (isQRMethod.value) {
      if (!payment.code_url) throw new Error('支付平台未返回二维码地址')
      qrDataURL.value = await QRCode.toDataURL(payment.code_url, { width: 220, margin: 1, errorCorrectionLevel: 'M' })
    }
    loading.value = false
    pollDeadline = Date.now() + 5 * 60 * 1000
    schedulePoll()
  } catch (error: any) {
    resetAttempt()
    errorMsg.value = error.response?.data?.error || error.message || '支付请求失败'
  }
}

const schedulePoll = () => {
  stopPolling()
  pollTimer = setTimeout(pollStatus, 1500)
}

const pollStatus = async () => {
  if (!paymentId.value) return
  if (Date.now() > pollDeadline) {
    resetAttempt()
    errorMsg.value = '支付等待超时，请查询订单状态后重试'
    return
  }
  try {
    const response = await axios.get(`/payments/${paymentId.value}`)
    if (response.data.status === 'paid') {
      stopPolling()
      paymentId.value = null
      ElMessage.success('支付成功')
      emit('success')
      return
    }
    if (response.data.status === 'failed') {
      const message = response.data.error_message || '支付平台返回失败'
      resetAttempt()
      errorMsg.value = `支付失败: ${message}`
      return
    }
  } catch {
    // A temporary query failure should not discard an in-progress provider payment.
  }
  schedulePoll()
}

onMounted(() => { cashTendered.value = props.amount })
onUnmounted(stopPolling)
</script>

<style scoped>
.payment-container { color: #252a25; }
.amount-due { display: flex; align-items: flex-end; justify-content: space-between; padding: 4px 2px 16px; }
.amount-due span { color: #6c746c; font-size: 13px; }
.amount-due strong { color: #b65b0b; font-size: 30px; line-height: 34px; font-variant-numeric: tabular-nums; }
.method-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; margin-bottom: 18px; }
.method-button { min-height: 68px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; padding: 7px 4px; border: 1px solid #d9ded7; border-radius: 7px; background: #f7f8f6; color: #5e665e; cursor: pointer; }
.method-button span { font-size: 11px; line-height: 15px; }
.method-button:hover { border-color: #78a88d; }
.method-button.active { border-color: #16784a; background: #eaf5ee; color: #0f643c; font-weight: 700; }
.cash-panel, .scan-panel { padding: 14px; border: 1px solid #dde2db; border-radius: 7px; background: #fafbf9; }
.cash-panel > label, .scan-panel > div:first-child { display: block; margin-bottom: 8px; color: #616961; font-size: 13px; }
.cash-input-row { height: 52px; display: flex; align-items: center; gap: 8px; padding: 0 14px; border: 2px solid #8aac97; border-radius: 7px; background: #fff; }
.cash-input-row > span { color: #667066; font-size: 20px; }
.cash-input-row :deep(.el-input-number) { width: 100%; }
.cash-input-row :deep(.el-input__wrapper) { box-shadow: none; padding: 0; }
.cash-input-row :deep(.el-input__inner) { text-align: left; font-size: 24px; font-weight: 700; }
.cash-presets { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 10px; }
.cash-presets button { height: 30px; padding: 0 10px; border: 1px solid #d1d7cf; border-radius: 5px; background: #fff; color: #4c544c; cursor: pointer; }
.cash-presets button:hover { border-color: #16784a; color: #16784a; }
.change-row { display: flex; align-items: center; justify-content: space-between; margin-top: 13px; padding-top: 12px; border-top: 1px solid #e2e6e0; }
.change-row span { color: #6b736b; }
.change-row strong { color: #16784a; font-size: 23px; font-variant-numeric: tabular-nums; }
.change-row.insufficient strong { color: #c23f3f; }
.qr-frame { width: 220px; height: 220px; display: flex; align-items: center; justify-content: center; margin: 0 auto; padding: 8px; border: 1px solid #dfe3dc; border-radius: 7px; background: #fff; }
.qr-caption { margin-top: 8px; text-align: center; color: #727a72; font-size: 13px; }
.pay-button { width: 100%; height: 46px; margin-top: 16px; border-radius: 7px; font-weight: 700; --el-button-bg-color: #16784a; --el-button-border-color: #16784a; --el-button-hover-bg-color: #0d5d38; --el-button-hover-border-color: #0d5d38; }
</style>
