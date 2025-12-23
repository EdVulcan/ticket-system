<template>
  <div class="payment-container flex flex-col items-center">
    <div class="text-3xl font-bold font-mono text-[#faad14] mb-8">¥ {{ amount.toFixed(2) }}</div>
    
    <div class="flex gap-4 w-full mb-8">
      <div 
        v-for="m in methods" 
        :key="m.key"
        class="flex-1 h-[100px] bg-[#333] border border-[#444] rounded-xl flex flex-col items-center justify-center cursor-pointer hover:bg-[#444] hover:border-[#1890ff] active:scale-95 transition-all"
        :class="{'border-[#1890ff] bg-[#1890ff] text-white': selectedMethod === m.key}"
        @click="selectMethod(m.key)"
      >
         <el-icon :size="32" class="mb-2"><component :is="m.icon" /></el-icon>
         <span class="text-sm">{{ m.label }}</span>
      </div>
    </div>

    <!-- State: Scan Gun (BScanC) -->
    <div v-if="selectedMethod === 'scan'" class="w-full">
        <div class="text-center text-gray-400 mb-4 animate-pulse" v-if="!paymentId">请使用扫码枪扫描顾客付款码...</div>
        <el-input 
            v-if="!paymentId"
            ref="scanInputRef"
            v-model="authCode" 
            placeholder="等待扫码..." 
            class="mb-4"
            @keyup.enter="doPay"
        />
    </div>

    <!-- State: QR Code (CScanB) -->
    <div v-if="selectedMethod === 'qr'" class="w-full flex justify-center">
        <div class="w-[200px] h-[200px] bg-white p-2 rounded flex items-center justify-center">
            <!-- Mock QR -->
             <el-icon :size="150" class="text-black"><Picture /></el-icon>
        </div>
    </div>
    <div v-if="selectedMethod === 'qr'" class="text-center text-gray-400 mt-2">请顾客使用微信/支付宝扫码</div>

    <!-- Loading State -->
    <div v-if="loading" class="mt-4 flex flex-col items-center text-[#1890ff]">
        <el-icon class="is-loading text-2xl mb-2"><Loading /></el-icon>
        <div>支付处理中...</div>
    </div>
    
    <div v-if="errorMsg" class="mt-4 text-red-500 text-sm">{{ errorMsg }}</div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { Money, Aim, Picture, Loading } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import axios from 'axios'

const props = defineProps<{
    amount: number
    orderNo: string
}>()

const emit = defineEmits(['success', 'cancel'])

const methods = [
    { key: 'scan', label: '被扫 (BScanC)', icon: Aim },
    { key: 'qr', label: '主扫 (CScanB)', icon: Picture },
    { key: 'cash', label: '现金/其他', icon: Money }
]

const selectedMethod = ref('scan')
const authCode = ref('')
const scanInputRef = ref()
const loading = ref(false)
const paymentId = ref<number | null>(null)
const errorMsg = ref('')

const selectMethod = (key: string) => {
    selectedMethod.value = key
    errorMsg.value = ''
    if (key === 'scan') {
        setTimeout(() => scanInputRef.value?.focus(), 100)
    } else if (key === 'cash') {
        doPay()
    } else if (key === 'qr') {
        // Mock CScanB: Create payment immediately and poll
        doPay()
    }
}

const doPay = async () => {
    loading.value = true
    errorMsg.value = ''
    try {
        const payload: any = {
            order_no: props.orderNo,
            amount: props.amount,
            method: selectedMethod.value === 'scan' || selectedMethod.value === 'qr' ? 'wechat' : 'cash', // Mock wechat for scan/qr
            pay_type: selectedMethod.value === 'scan' ? 'bscanc' : 'cscanb',
            auth_code: authCode.value
        }

        const res = await axios.post('/payments/pay', payload)
        const payment = res.data
        paymentId.value = payment.id

        // Start Polling
        pollStatus()

    } catch (e: any) {
        errorMsg.value = e.response?.data?.error || '支付请求失败'
        loading.value = false
    }
}

const pollStatus = async () => {
    if (!paymentId.value) return
    const timer = setInterval(async () => {
        try {
            const res = await axios.get(`/payments/${paymentId.value}`)
            const status = res.data.status // pending, paid, failed
            
            if (status === 'paid') {
                clearInterval(timer)
                loading.value = false
                ElMessage.success('支付成功')
                emit('success')
            } else if (status === 'failed') {
                clearInterval(timer)
                loading.value = false
                errorMsg.value = '支付失败: ' + (res.data.error_message || 'Unknown')
            }
            // Continue polling if pending
        } catch (e) {
            // Ignore temporary network errors
        }
    }, 1000)
}

onMounted(() => {
    setTimeout(() => scanInputRef.value?.focus(), 100)
})
</script>
