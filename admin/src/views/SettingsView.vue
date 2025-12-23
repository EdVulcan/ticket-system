<template>
  <div class="max-w-4xl mx-auto space-y-6">
    <div class="flex items-center justify-between">
      <h2 class="text-2xl font-bold text-gray-800">系统设置 (System Settings)</h2>
    </div>

    <!-- Basic Info -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
      <h3 class="text-lg font-bold text-gray-900 mb-4 pb-2 border-b border-gray-100">基本信息</h3>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="商户名称">{{ tenant.name }}</el-descriptions-item>
        <el-descriptions-item label="联系人">{{ tenant.contact }}</el-descriptions-item>
        <el-descriptions-item label="联系电话">{{ tenant.phone }}</el-descriptions-item>
        <el-descriptions-item label="联系地址">{{ tenant.address }}</el-descriptions-item>
      </el-descriptions>
    </div>

    <!-- Developer Config -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
      <h3 class="text-lg font-bold text-gray-900 mb-4 pb-2 border-b border-gray-100">开发配置 (Developer)</h3>
      <div class="bg-gray-50 rounded-lg p-6 border border-gray-200 space-y-6">
        <div>
          <div class="text-sm font-bold text-gray-500 mb-1">系统编号 (System Code)</div>
          <div class="flex gap-2">
            <el-input v-model="tenant.system_code" readonly class="font-mono" />
            <el-button @click="copy(tenant.system_code)">复制</el-button>
          </div>
          <div class="text-xs text-gray-400 mt-1">用于 B2B 分销时的身份标识。</div>
        </div>

        <div>
          <div class="text-sm font-bold text-gray-500 mb-1">API 密钥 (Secret Key)</div>
          <div class="flex gap-2">
             <el-input v-model="displayKey" readonly class="font-mono">
                <template #append>
                    <el-button @click="toggleKey">
                         <el-icon><View v-if="showKey"/><Hide v-else/></el-icon>
                    </el-button>
                </template>
             </el-input>
             <el-button type="primary" @click="copy(tenant.secret_key)">复制密钥</el-button>
          </div>
          <div class="text-xs text-red-400 mt-1">此密钥用于 OTA 接口签名，请妥善保管，切勿泄露给无关人员。</div>
        </div>
      </div>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { View, Hide } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { ElMessage } from 'element-plus'

const tenant = ref<any>({})
const showKey = ref(false)

const displayKey = computed(() => {
    return showKey.value ? tenant.value.secret_key : '********************************'
})

const fetchSelf = async () => {
    try {
        const res = await request.get('/tenants/me')
        tenant.value = res.data
    } catch (e) {
        ElMessage.error('获取信息失败')
    }
}

const toggleKey = () => {
    showKey.value = !showKey.value
}

const copy = (text: string) => {
    if(!text) return
    navigator.clipboard.writeText(text).then(() => {
        ElMessage.success('已复制')
    })
}

onMounted(() => {
    fetchSelf()
})
</script>
