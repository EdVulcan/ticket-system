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

    <el-tabs v-model="activeTab" @tab-change="loadTab">
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
      <el-tab-pane label="资金总览" name="finance">
        <div v-loading="financeLoading" class="grid grid-cols-2 md:grid-cols-4 gap-4">
          <el-card v-for="item in financeCards" :key="item.key" shadow="never" class="border border-gray-100">
            <el-statistic :title="item.label" :value="item.cents ? centsToYuan(finance[item.key] || 0) : (finance[item.key] || 0)" :precision="item.cents ? 2 : 0" />
          </el-card>
        </div>
      </el-tab-pane>
      <el-tab-pane label="设备总览" name="devices">
        <el-table :data="devices" v-loading="devicesLoading" stripe>
          <el-table-column prop="id" label="设备 ID" width="90" />
          <el-table-column prop="tenant_name" label="租户" min-width="150" />
          <el-table-column prop="scenic_area_name" label="履约景区" min-width="150" />
          <el-table-column prop="name" label="设备" min-width="150" />
          <el-table-column prop="type" label="类型" width="100" />
          <el-table-column prop="status" label="状态" width="110" />
          <el-table-column prop="last_heartbeat" label="最后心跳" min-width="180" />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="devicesPage" v-model:page-size="pageSize" :total="devicesTotal" layout="total, prev, pager, next" @current-change="loadDevices" /></div>
      </el-tab-pane>
      <el-tab-pane label="结算总览" name="settlements">
        <el-table :data="settlements" v-loading="settlementsLoading" stripe>
          <el-table-column prop="statement_no" label="结算单" min-width="180" />
          <el-table-column prop="supplier_name" label="供应商" min-width="150" />
          <el-table-column prop="distributor_name" label="分销商" min-width="150" />
          <el-table-column prop="net_cents" label="应结(分)" width="110" />
          <el-table-column prop="status" label="状态" width="130" />
          <el-table-column prop="created_at" label="创建时间" min-width="180" />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="settlementsPage" v-model:page-size="pageSize" :total="settlementsTotal" layout="total, prev, pager, next" @current-change="loadSettlements" /></div>
      </el-tab-pane>
      <el-tab-pane label="审计日志" name="audit">
        <el-table :data="auditLogs" v-loading="auditLoading" stripe>
          <el-table-column prop="created_at" label="时间" min-width="180" />
          <el-table-column prop="action" label="动作" min-width="220" />
          <el-table-column prop="tenant_name" label="租户" min-width="150" />
          <el-table-column prop="actor_role" label="操作者角色" width="130" />
          <el-table-column prop="reason" label="原因" min-width="260" show-overflow-tooltip />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="auditPage" v-model:page-size="pageSize" :total="auditTotal" layout="total, prev, pager, next" @current-change="loadAudit" /></div>
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
const financeLoading = ref(false)
const devicesLoading = ref(false)
const settlementsLoading = ref(false)
const auditLoading = ref(false)
const finance = ref<Record<string, number>>({})
const devices = ref<any[]>([])
const settlements = ref<any[]>([])
const auditLogs = ref<any[]>([])
const devicesPage = ref(1)
const settlementsPage = ref(1)
const auditPage = ref(1)
const devicesTotal = ref(0)
const settlementsTotal = ref(0)
const auditTotal = ref(0)
const financeCards = [
  { key: 'capital_balance_cents', label: '资金余额(元)', cents: true },
  { key: 'credit_used_cents', label: '已用授信(元)', cents: true },
  { key: 'frozen_cents', label: '冻结资金(元)', cents: true },
  { key: 'pending_document_count', label: '待处理财务凭证', cents: false },
  { key: 'pending_payment_cents', label: '待确认支付(元)', cents: true },
  { key: 'pending_refund_cents', label: '待确认退款(元)', cents: true }
]

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

const centsToYuan = (value: number) => Number(value || 0) / 100

const loadFinance = async () => {
  financeLoading.value = true
  try { finance.value = (await request.get('/platform/finance', { params: { tenant_id: tenantParam() } })).data || {} }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '资金总览加载失败') }
  finally { financeLoading.value = false }
}

const loadDevices = async () => {
  devicesLoading.value = true
  try {
    const res = await request.get('/platform/devices', { params: { tenant_id: tenantParam(), page: devicesPage.value, page_size: pageSize.value } })
    devices.value = res.data.data || []; devicesTotal.value = res.data.total || 0
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '设备总览加载失败') } finally { devicesLoading.value = false }
}

const loadSettlements = async () => {
  settlementsLoading.value = true
  try {
    const res = await request.get('/platform/settlements', { params: { tenant_id: tenantParam(), page: settlementsPage.value, page_size: pageSize.value } })
    settlements.value = res.data.data || []; settlementsTotal.value = res.data.total || 0
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '结算总览加载失败') } finally { settlementsLoading.value = false }
}

const loadAudit = async () => {
  auditLoading.value = true
  try {
    const res = await request.get('/platform/audit-logs', { params: { tenant_id: tenantParam(), page: auditPage.value, page_size: pageSize.value } })
    auditLogs.value = res.data.data || []; auditTotal.value = res.data.total || 0
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '审计日志加载失败') } finally { auditLoading.value = false }
}

const loadTab = (name: string | number) => {
  switch (name) {
    case 'finance': return loadFinance()
    case 'devices': return loadDevices()
    case 'settlements': return loadSettlements()
    case 'audit': return loadAudit()
  }
}

const loadAll = async () => {
  ordersPage.value = 1
  issuesPage.value = 1
  await Promise.all([loadOrders(), loadIssues(), loadFinance(), loadDevices(), loadSettlements(), loadAudit()])
}

onMounted(loadAll)
</script>
