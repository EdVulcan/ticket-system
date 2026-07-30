<template>
  <div class="payment-container flex flex-col items-center">
    <div class="mb-6 text-3xl font-bold font-mono text-[#faad14]">¥ {{ amount.toFixed(2) }}</div>

    <div class="grid grid-cols-4 gap-3 w-full mb-6">
      <button
        v-for="method in methods"
        :key="method.key"
        type="button"
        class="h-[88px] bg-[#333] border border-[#444] rounded-md flex flex-col items-center justify-center hover:bg-[#444] hover:border-[#1890ff] transition-colors"
        :class="{ 'border-[#1890ff] bg-[#15395b] text-white': selectedMethod === method.key }"
        :disabled="loading"
        @click="selectMethod(method.key)"
      >
        <el-icon :size="28" class="mb-2"><component :is="method.icon" /></el-icon>
        <span class="text-xs">{{ method.label }}</span>
      </button>
    </div>

    <div v-if="selectedMethod === 'scan'" class="w-full">
      <div class="text-center text-gray-400 mb-3">扫描顾客的微信或支付宝付款码</div>
      <el-input
        ref="scanInputRef"
        v-model="authCode"
        placeholder="等待扫码..."
        :disabled="loading"
        @keyup.enter="doPay"
      />
    </div>

    <div v-if="isQRMethod" class="w-full flex flex-col items-center">
      <div class="w-[220px] h-[220px] bg-white p-2 rounded-md flex items-center justify-center">
        <img v-if="qrDataURL" :src="qrDataURL" alt="支付二维码" class="w-full h-full" />
        <el-icon v-else-if="loading" class="is-loading text-gray-500" :size="42"><Loading /></el-icon>
        <span v-else class="text-sm text-gray-500">点击下方按钮生成二维码</span>
      </div>
      <div class="text-center text-gray-400 mt-2">请顾客扫码支付</div>
    </div>

    <el-button
      v-if="selectedMethod === 'scan' || (isQRMethod && !paymentId)"
      type="primary"
      class="w-full mt-5"
      size="large"
      :loading="loading"
      :disabled="selectedMethod === 'scan' && !authCode.trim()"
      @click="doPay"
    >
      {{ selectedMethod === 'scan' ? '确认收款' : '生成支付码' }}
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

const props = defineProps<{ amount: number; orderNo: string }>()
const emit = defineEmits<{ success: [] }>()

const methods = [
  { key: 'scan', label: '付款码', icon: Aim },
  { key: 'wechat_qr', label: '微信扫码', icon: ChatDotRound },
  { key: 'alipay_qr', label: '支付宝扫码', icon: Wallet },
  { key: 'cash', label: '现金', icon: Money }
]

const selectedMethod = ref('scan')
const authCode = ref('')
const scanInputRef = ref()
const loading = ref(false)
const paymentId = ref<number | null>(null)
const qrDataURL = ref('')
const errorMsg = ref('')
const isQRMethod = computed(() => selectedMethod.value.endsWith('_qr'))

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
    await doPay()
  } else if (key === 'scan') {
    await nextTick()
    scanInputRef.value?.focus()
  }
}

const doPay = async () => {
  if (loading.value || paymentId.value) return
  loading.value = true
  errorMsg.value = ''
  try {
    const isScan = selectedMethod.value === 'scan'
    const method = isScan ? 'auto' : selectedMethod.value.replace('_qr', '')
    const response = await axios.post('/payments/pay', {
      order_no: props.orderNo,
      method,
      pay_type: isScan ? 'bscanc' : selectedMethod.value === 'cash' ? 'cash' : 'cscanb',
      auth_code: isScan ? authCode.value.trim() : ''
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

onMounted(() => setTimeout(() => scanInputRef.value?.focus(), 100))
onUnmounted(stopPolling)
</script>
