<template>
  <main class="max-w-[1500px] mx-auto space-y-6">
    <div class="flex items-center justify-between border-b border-gray-200 pb-5">
      <div>
        <h1 class="text-2xl font-semibold text-gray-900">平台运营工作台</h1>
        <p class="text-sm text-gray-500 mt-1">跨租户只读监控，所有查询都会写入平台审计。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新" @click="loadAll" />
    </div>

    <div class="flex flex-wrap gap-3 items-center">
      <el-input v-model="tenantID" clearable placeholder="目标租户 ID（可选）" style="width: 210px" @keyup.enter="loadAll" />
      <el-select v-model="orderStatus" clearable placeholder="订单状态" style="width: 160px" @change="loadOrders">
        <el-option label="未支付" value="unpaid" />
        <el-option label="已支付" value="paid" />
        <el-option label="部分退款" value="partial_refunded" />
        <el-option label="已退款" value="refunded" />
        <el-option label="已取消" value="cancelled" />
      </el-select>
      <el-button type="primary" @click="loadAll">查询</el-button>
    </div>

    <el-tabs v-model="activeTab">
      <el-tab-pane label="全局订单" name="orders">
        <el-table :data="orders" v-loading="ordersLoading" stripe>
          <el-table-column prop="order_no" label="订单号" min-width="190" />
          <el-table-column prop="tenant_id" label="租户 ID" width="90" />
          <el-table-column prop="tenant_name" label="租户名称" min-width="150" />
          <el-table-column prop="channel" label="渠道" width="100" />
          <el-table-column prop="total_amount" label="金额" width="110" />
          <el-table-column prop="status" label="状态" width="120" />
          <el-table-column prop="created_at" label="创建时间" min-width="180" />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="ordersPage" v-model:page-size="pageSize" :total="ordersTotal" layout="total, prev, pager, next" @current-change="loadOrders" /></div>
      </el-tab-pane>
      <el-tab-pane label="异常工作台" name="issues">
        <el-table :data="issues" v-loading="issuesLoading" stripe>
          <el-table-column prop="kind" label="类型" width="160" />
          <el-table-column prop="id" label="记录 ID" width="90" />
          <el-table-column prop="tenant_id" label="租户 ID" width="90" />
          <el-table-column prop="status" label="状态" width="130" />
          <el-table-column prop="description" label="说明" min-width="260" show-overflow-tooltip />
          <el-table-column prop="created_at" label="创建时间" min-width="180" />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="issuesPage" v-model:page-size="pageSize" :total="issuesTotal" layout="total, prev, pager, next" @current-change="loadIssues" /></div>
      </el-tab-pane>
    </el-tabs>
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const activeTab = ref('orders')
const tenantID = ref('')
const orderStatus = ref('')
const pageSize = ref(20)
const ordersPage = ref(1)
const issuesPage = ref(1)
const ordersTotal = ref(0)
const issuesTotal = ref(0)
const orders = ref<any[]>([])
const issues = ref<any[]>([])
const ordersLoading = ref(false)
const issuesLoading = ref(false)

const tenantParam = () => tenantID.value.trim() ? Number(tenantID.value) : undefined

const loadOrders = async () => {
  ordersLoading.value = true
  try {
    const res = await request.get('/platform/orders', { params: { tenant_id: tenantParam(), status: orderStatus.value, page: ordersPage.value, page_size: pageSize.value } })
    orders.value = res.data.data || []
    ordersTotal.value = res.data.total || 0
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '订单加载失败') } finally { ordersLoading.value = false }
}

const loadIssues = async () => {
  issuesLoading.value = true
  try {
    const res = await request.get('/platform/issues', { params: { tenant_id: tenantParam(), page: issuesPage.value, page_size: pageSize.value } })
    issues.value = res.data.data || []
    issuesTotal.value = res.data.total || 0
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '异常加载失败') } finally { issuesLoading.value = false }
}

const loadAll = async () => {
  ordersPage.value = 1
  issuesPage.value = 1
  await Promise.all([loadOrders(), loadIssues()])
}

onMounted(loadAll)
</script>
