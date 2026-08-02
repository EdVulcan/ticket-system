<template>
  <main class="max-w-[1400px] mx-auto">
    <div class="flex items-end justify-between border-b border-gray-200 pb-5 mb-6">
      <div>
        <h1 class="text-2xl font-semibold text-gray-900">{{ isPlatform ? '平台运行总览' : '经营控制台' }}</h1>
        <p class="text-sm text-gray-500 mt-1">{{ isPlatform ? '跨租户只读运行指标，访问会写入平台审计。' : '进入当前租户已授权的核心工作区。' }}</p>
      </div>
      <el-button v-if="isPlatform" :icon="Refresh" circle title="刷新" @click="loadOverview" />
    </div>

    <template v-if="isPlatform">
      <div class="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-5 border-y border-gray-200 bg-white" v-loading="loading">
        <div v-for="item in metrics" :key="item.key" class="px-5 py-6 border-r border-b border-gray-100 last:border-r-0">
          <div class="text-xs text-gray-500">{{ item.label }}</div>
          <div class="text-2xl font-semibold text-gray-900 mt-2">{{ overview[item.key] || 0 }}</div>
        </div>
      </div>
      <div class="mt-6 flex gap-3">
        <el-button type="primary" @click="$router.push('/tenant')">租户治理</el-button>
        <el-button @click="$router.push('/platform-operations?tab=orders')">平台运营工作台</el-button>
      </div>
    </template>

    <template v-else>
      <div class="divide-y divide-gray-200 border-y border-gray-200 bg-white">
        <button class="workspace-link" @click="$router.push('/online-order')"><span>订单管理</span><small>销售订单、支付与退款</small><el-icon><ArrowRight /></el-icon></button>
        <button v-if="hasCapability('supplier')" class="workspace-link" @click="$router.push('/product')"><span>产品与履约</span><small>产品、库存、景区与现场设备</small><el-icon><ArrowRight /></el-icon></button>
        <button v-if="hasAnyCapability('supplier', 'distributor')" class="workspace-link" @click="$router.push('/distribution')"><span>供应与分销</span><small>合作、报价和销售映射</small><el-icon><ArrowRight /></el-icon></button>
        <button class="workspace-link" @click="$router.push('/operations')"><span>运营工作台</span><small>渠道、团队、结算、班次、打印和告警</small><el-icon><ArrowRight /></el-icon></button>
      </div>
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ArrowRight, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

const user = computed<any>(() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })
const isPlatform = computed(() => user.value.scope === 'platform')
const capabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const hasCapability = (value: string) => capabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(hasCapability)
const loading = ref(false)
const overview = reactive<Record<string, number>>({})
const metrics = [
  { key: 'tenant_total', label: '租户总数' }, { key: 'tenant_active', label: '运行租户' }, { key: 'tenant_frozen', label: '冻结租户' },
  { key: 'orders_today', label: '今日订单' }, { key: 'pending_payments', label: '支付待确认' }, { key: 'pending_refunds', label: '退款待确认' },
  { key: 'open_device_alerts', label: '设备告警' }, { key: 'open_settlements', label: '待结算' }, { key: 'active_channel_links', label: '活动渠道' }
]
const loadOverview = async () => {
  if (!isPlatform.value) return
  loading.value = true
  try { Object.assign(overview, (await request.get('/platform/overview')).data) } finally { loading.value = false }
}
onMounted(loadOverview)
</script>

<style scoped>
.workspace-link { display: grid; grid-template-columns: 180px 1fr 24px; width: 100%; align-items: center; padding: 20px 4px; text-align: left; color: #111827; }
.workspace-link:hover { background: #f9fafb; }
.workspace-link span { font-weight: 600; }
.workspace-link small { color: #6b7280; }
</style>
