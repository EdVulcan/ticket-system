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
      <el-input v-model="tenantID" clearable placeholder="目标租户编号（可选）" style="width: 210px" @keyup.enter="loadAll" />
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
          <el-table-column prop="tenant_id" label="租户编号" width="100" />
          <el-table-column prop="tenant_name" label="租户名称" min-width="150" />
          <el-table-column label="渠道" width="120"><template #default="{ row }">{{ channelText(row.channel) }}</template></el-table-column>
          <el-table-column prop="total_amount" label="金额" width="110" />
          <el-table-column label="状态" width="120"><template #default="{ row }">{{ orderStatusText(row.status) }}</template></el-table-column>
          <el-table-column prop="created_at" label="创建时间" min-width="180" />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="ordersPage" v-model:page-size="pageSize" :total="ordersTotal" layout="total, prev, pager, next" @current-change="loadOrders" /></div>
      </el-tab-pane>
      <el-tab-pane label="异常工作台" name="issues">
        <el-table :data="issues" v-loading="issuesLoading" stripe>
          <el-table-column label="类型" width="160"><template #default="{ row }">{{ issueKindText(row.kind) }}</template></el-table-column>
          <el-table-column prop="id" label="记录编号" width="100" />
          <el-table-column prop="tenant_id" label="租户编号" width="100" />
          <el-table-column label="状态" width="130"><template #default="{ row }">{{ commonStatusText(row.status) }}</template></el-table-column>
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
          <el-table-column prop="id" label="设备编号" width="100" />
          <el-table-column prop="tenant_name" label="租户" min-width="150" />
          <el-table-column prop="scenic_area_name" label="履约景区" min-width="150" />
          <el-table-column prop="name" label="设备" min-width="150" />
          <el-table-column label="类型" width="110"><template #default="{ row }">{{ deviceTypeText(row.type) }}</template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="{ row }">{{ deviceStatusText(row.status) }}</template></el-table-column>
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
          <el-table-column label="状态" width="130"><template #default="{ row }">{{ settlementStatusText(row.status) }}</template></el-table-column>
          <el-table-column prop="created_at" label="创建时间" min-width="180" />
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="settlementsPage" v-model:page-size="pageSize" :total="settlementsTotal" layout="total, prev, pager, next" @current-change="loadSettlements" /></div>
      </el-tab-pane>
      <el-tab-pane label="审计日志" name="audit">
        <el-table :data="auditLogs" v-loading="auditLoading" stripe>
          <el-table-column prop="created_at" label="时间" min-width="180" />
          <el-table-column label="动作" min-width="220"><template #default="{ row }">{{ auditActionText(row.action) }}</template></el-table-column>
          <el-table-column prop="tenant_name" label="租户" min-width="150" />
          <el-table-column label="操作者角色" width="130"><template #default="{ row }">{{ roleText(row.actor_role) }}</template></el-table-column>
          <el-table-column label="原因" min-width="260" show-overflow-tooltip><template #default="{ row }">{{ localizeDisplayText(row.reason, '系统操作记录') }}</template></el-table-column>
        </el-table>
        <div class="mt-4 flex justify-end"><el-pagination v-model:current-page="auditPage" v-model:page-size="pageSize" :total="auditTotal" layout="total, prev, pager, next" @current-change="loadAudit" /></div>
      </el-tab-pane>
    </el-tabs>
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'
import { localizeDisplayText } from '@/utils/localize'

const route = useRoute()
const router = useRouter()
const platformTabs = new Set(['orders', 'issues', 'finance', 'devices', 'settlements', 'audit'])
const routeTab = () => typeof route.query.tab === 'string' && platformTabs.has(route.query.tab) ? route.query.tab : 'orders'
const activeTab = ref(routeTab())
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
const orderStatusText = (status: string) => ({ unpaid: '待支付', paid: '已支付', completed: '已完成', partial_refunded: '部分退款', refunded: '已退款', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const channelText = (channel: string) => ({ window: '窗口售票', h5: '网页商城', miniapp: '小程序', ota: '外部渠道', distributor: '分销商', team: '旅行社团队' } as Record<string, string>)[channel] || '其他渠道'
const issueKindText = (kind: string) => ({ refund: '退款异常', payment: '支付异常', print: '打印异常', device: '设备异常', settlement: '结算异常', callback: '回调异常', after_sale: '售后异常' } as Record<string, string>)[kind] || '其他异常'
const commonStatusText = (status: string) => ({ pending: '待处理', processing: '处理中', failed: '处理失败', retryable: '可重试', completed: '已完成', succeeded: '已成功', closed: '已关闭', open: '待处理' } as Record<string, string>)[status] || '未知状态'
const deviceTypeText = (type: string) => ({ gate: '闸机', pos: '售票终端', printer: '打印机', id_reader: '身份证阅读器', scanner: '扫码设备' } as Record<string, string>)[type] || '其他设备'
const deviceStatusText = (status: string) => ({ online: '在线', offline: '离线', active: '已启用', disabled: '已停用', maintenance: '维护中' } as Record<string, string>)[status] || '未知状态'
const settlementStatusText = (status: string) => ({ draft: '待确认', supplier_confirmed: '供应商已确认', confirmed: '双方已确认', disputed: '有争议', paid: '已付款', settled: '已结清' } as Record<string, string>)[status] || '未知状态'
const roleText = (role: string) => ({ super_admin: '平台管理员', admin: '租户管理员', seller: '售票员', checker: '验票员', finance: '结算人员', operator: '运营人员' } as Record<string, string>)[role] || '其他角色'
const auditActionText = (action: string) => ({
  'platform.overview.read': '查看平台总览',
  'platform.orders.read': '查看全局订单',
  'platform.issues.read': '查看异常工作台',
  'platform.finance.read': '查看资金总览',
  'platform.devices.read': '查看设备总览',
  'platform.settlements.read': '查看结算总览',
  'tenant.sessions.revoke': '撤销租户全部会话',
  'tenant.status.update': '更新租户状态',
  'tenant.lifecycle.update': '更新租户资质与合同',
  'tenant.capability.update': '更新租户业务能力',
  'settlement.adjust': '调整结算单',
  'settlement.status': '更新结算单状态',
  'pos.shift.correct': '更正收银班次',
  'payment.refund.retry': '重试退款任务',
  'payment.refund.cash': '确认现金退款',
  'distribution.offer.status': '更新供货报价状态',
  'distribution.listing.sync': '同步分销商品',
  'distribution.bundle.create': '创建组合商品',
  'distribution.bundle.revise': '修订组合商品',
  'distribution.bundle.status': '更新组合商品状态',
  'channel.request.retry_authorized': '授权重试渠道请求',
  'team.settlement.generate': '生成团队结算单',
  'team.settlement.status': '更新团队结算状态',
  'team.settlement.adjust': '调整团队结算单',
  'team.entry_batch': '登记团队分批入园',
  'team.confirmation.submit': '提交团队现场确认',
  'team.confirmation.acknowledge': '确认团队现场记录',
  'team.member.change': '调整团队人数',
} as Record<string, string>)[action] || '其他系统操作'

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
  const tab = String(name)
  if (route.query.tab !== tab) router.replace({ query: { ...route.query, tab } })
  switch (name) {
    case 'orders': return loadOrders()
    case 'issues': return loadIssues()
    case 'finance': return loadFinance()
    case 'devices': return loadDevices()
    case 'settlements': return loadSettlements()
    case 'audit': return loadAudit()
  }
}

watch(() => route.query.tab, () => {
  const tab = routeTab()
  if (activeTab.value !== tab) {
    activeTab.value = tab
    loadTab(tab)
  }
})

const loadAll = async () => {
  ordersPage.value = 1
  issuesPage.value = 1
  await Promise.all([loadOrders(), loadIssues(), loadFinance(), loadDevices(), loadSettlements(), loadAudit()])
}

onMounted(loadAll)
</script>
