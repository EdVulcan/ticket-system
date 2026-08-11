<template>
  <main class="dashboard-page">
    <header class="dashboard-heading">
      <div>
        <h1>{{ isPlatform ? '平台运行总览' : '经营控制台' }}</h1>
        <p>{{ isPlatform ? '查看全平台运行状态和需要跟进的事项' : `${user.tenant_name || '当前商户'}的日常业务入口` }}</p>
      </div>
      <el-button v-if="isPlatform" :icon="Refresh" title="刷新数据" @click="loadOverview">刷新</el-button>
    </header>

    <template v-if="isPlatform">
      <section class="metric-strip" v-loading="loading">
        <div v-for="item in metrics" :key="item.key" class="metric-item">
          <span>{{ item.label }}</span>
          <strong>{{ overview[item.key] || 0 }}</strong>
        </div>
      </section>

      <section class="dashboard-section">
        <div class="section-heading"><h2>平台工作</h2><span>按职责进入对应工作区</span></div>
        <div class="work-list">
          <button v-if="canManagePlatform" class="work-row" type="button" @click="$router.push('/tenant')">
            <span class="work-icon is-green"><el-icon><OfficeBuilding /></el-icon></span>
            <span class="work-copy"><strong>商户开户管理</strong><small>开户、资质、合同期限和租户状态</small></span>
            <el-icon class="work-arrow"><ArrowRight /></el-icon>
          </button>
          <button class="work-row" type="button" @click="$router.push('/platform-operations')">
            <span class="work-icon is-blue"><el-icon><Monitor /></el-icon></span>
            <span class="work-copy"><strong>平台运营工作台</strong><small>跨租户订单、资金、设备、结算和审计</small></span>
            <el-icon class="work-arrow"><ArrowRight /></el-icon>
          </button>
        </div>
      </section>
    </template>

    <template v-else>
      <div class="tenant-dashboard-grid">
        <section class="dashboard-section">
          <div class="section-heading"><h2>开始工作</h2><span>常用业务入口</span></div>
          <div class="work-list">
            <button v-for="item in workspaces" :key="item.path" class="work-row" type="button" @click="$router.push(item.path)">
              <span class="work-icon" :class="item.tone"><el-icon><component :is="item.icon" /></el-icon></span>
              <span class="work-copy"><strong>{{ item.label }}</strong><small>{{ item.description }}</small></span>
              <el-icon class="work-arrow"><ArrowRight /></el-icon>
            </button>
          </div>
        </section>

        <aside class="tenant-summary">
          <div class="section-heading"><h2>当前商户</h2><span>{{ user.system_code || '-' }}</span></div>
          <div class="tenant-name">{{ user.tenant_name || '商户信息' }}</div>
          <dl class="tenant-facts">
            <div><dt>登录账号</dt><dd>{{ user.username || '-' }}</dd></div>
            <div><dt>当前岗位</dt><dd>{{ tenantRoleLabel(user.role) }}</dd></div>
          </dl>
          <div class="capability-block">
            <span>已启用业务</span>
            <div class="capability-list">
              <el-tag v-for="item in capabilityLabels" :key="item" effect="plain" type="success">{{ item }}</el-tag>
              <span v-if="!capabilityLabels.length" class="empty-copy">暂无启用业务</span>
            </div>
          </div>
        </aside>
      </div>
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ArrowRight, Connection, List, Monitor, OfficeBuilding, Operation, Refresh, Tickets, TrendCharts } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { tenantRoleLabel } from '@/utils/permissions'

const user = computed<any>(() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })
const isPlatform = computed(() => user.value.scope === 'platform')
const canManagePlatform = computed(() => user.value.role === 'platform_admin')
const capabilities = computed(() => new Set((user.value.capabilities || [])
  .filter((item: any) => item.status === 'active' && (!item.expires_at || new Date(item.expires_at).getTime() > Date.now()))
  .map((item: any) => item.capability)))
const hasCapability = (value: string) => capabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(hasCapability)
const permissions = computed(() => new Set(user.value.permissions || []))
const can = (value: string) => user.value.role === 'super_admin' || permissions.value.has(value)

const workspaces = computed(() => [
  can('orders.read') ? { path: '/online-order', label: '订单管理', description: '查询订单、支付状态和售后进度', icon: List, tone: 'is-green' } : null,
  hasCapability('supplier') && can('catalog.read') ? { path: '/product', label: '门票与库存', description: '维护线上、窗口票种和可售库存', icon: Tickets, tone: 'is-blue' } : null,
  hasAnyCapability('supplier', 'distributor') && can('distribution.read') ? { path: '/distribution', label: '供销合作', description: '维护合作关系、产品授权和结算价格', icon: Connection, tone: 'is-amber' } : null,
  hasAnyCapability('supplier', 'travel_agency') && can('teams.read') ? { path: '/teams', label: '旅行社团队', description: '处理团队计划、合同、入园和结算', icon: Operation, tone: 'is-coral' } : null,
  can('reports.read') ? { path: '/report', label: '经营报表', description: '查看营业与核销收入数据', icon: TrendCharts, tone: 'is-blue' } : null,
  hasAnyCapability('supplier', 'distributor') && can('operations.read') ? { path: '/operations', label: '运营工作台', description: '集中处理景区、渠道、班次和设备事项', icon: Monitor, tone: 'is-green' } : null,
].filter(Boolean) as Array<{ path: string; label: string; description: string; icon: any; tone: string }>)

const capabilityLabels = computed(() => [
  hasCapability('supplier') ? '景区供应商' : '',
  hasCapability('distributor') ? '分销商' : '',
  hasCapability('travel_agency') ? '旅行社' : ''
].filter(Boolean))

const loading = ref(false)
const overview = reactive<Record<string, number>>({})
const metrics = [
  { key: 'tenant_total', label: '商户总数' }, { key: 'tenant_active', label: '运行商户' }, { key: 'tenant_frozen', label: '冻结商户' },
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
.dashboard-page { display: flex; flex-direction: column; gap: 22px; }
.dashboard-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; padding-bottom: 18px; border-bottom: 1px solid var(--ui-border); }
.dashboard-heading h1, .section-heading h2 { margin: 0; }
.dashboard-heading p { margin: 5px 0 0; font-size: 13px; }
.metric-strip { display: grid; grid-template-columns: repeat(5, minmax(120px, 1fr)); background: #fff; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); }
.metric-item { min-height: 88px; padding: 16px 18px; border-right: 1px solid var(--ui-border); border-bottom: 1px solid var(--ui-border); }
.metric-item:nth-child(5n) { border-right: 0; }
.metric-item:nth-last-child(-n+4) { border-bottom: 0; }
.metric-item span { display: block; color: var(--ui-text-secondary); font-size: 12px; }
.metric-item strong { display: block; margin-top: 7px; color: var(--ui-text); font-size: 24px; line-height: 30px; font-weight: 700; }
.tenant-dashboard-grid { display: grid; grid-template-columns: minmax(0, 1fr) 320px; gap: 20px; align-items: start; }
.dashboard-section, .tenant-summary { background: #fff; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); }
.section-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 16px; padding: 16px 18px; border-bottom: 1px solid var(--ui-border); }
.section-heading h2 { font-size: 15px !important; line-height: 22px; }
.section-heading span { color: var(--ui-text-muted); font-size: 12px; }
.work-list { display: flex; flex-direction: column; }
.work-row { display: grid; grid-template-columns: 38px minmax(0,1fr) 20px; gap: 13px; align-items: center; width: 100%; min-height: 68px; padding: 11px 18px; color: var(--ui-text); background: transparent; border: 0; border-bottom: 1px solid #edf0f4; cursor: pointer; text-align: left; transition: background-color 120ms ease; }
.work-row:last-child { border-bottom: 0; }
.work-row:hover { background: #f7f9fd; }
.work-row:hover .work-arrow { color: var(--ui-primary); transform: translateX(2px); }
.work-icon { display: inline-flex; align-items: center; justify-content: center; width: 36px; height: 36px; border-radius: 5px; font-size: 18px; }
.work-icon.is-green { color: #16875f; background: #edf8f3; }
.work-icon.is-blue { color: #2563eb; background: #eef4ff; }
.work-icon.is-amber { color: #c46d08; background: #fff6e5; }
.work-icon.is-coral { color: #cf4650; background: #fff0f1; }
.work-copy { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.work-copy strong { font-size: 14px; line-height: 20px; }
.work-copy small { overflow: hidden; color: var(--ui-text-secondary); font-size: 12px; line-height: 18px; text-overflow: ellipsis; white-space: nowrap; }
.work-arrow { color: #9aa4b3; transition: color 120ms ease, transform 120ms ease; }
.tenant-summary { overflow: hidden; }
.tenant-name { padding: 18px 18px 8px; font-size: 16px; font-weight: 700; line-height: 24px; }
.tenant-facts { margin: 0; padding: 6px 18px 15px; }
.tenant-facts > div { display: flex; justify-content: space-between; gap: 16px; padding: 9px 0; border-bottom: 1px solid #edf0f4; }
.tenant-facts dt { color: var(--ui-text-secondary); font-size: 12px; }
.tenant-facts dd { margin: 0; color: var(--ui-text); font-size: 12px; font-weight: 600; text-align: right; }
.capability-block { padding: 0 18px 18px; }
.capability-block > span { display: block; margin-bottom: 9px; color: var(--ui-text-secondary); font-size: 12px; }
.capability-list { display: flex; flex-wrap: wrap; gap: 7px; }
.empty-copy { color: var(--ui-text-muted); font-size: 12px; }
@media (max-width: 1050px) { .tenant-dashboard-grid { grid-template-columns: 1fr; } .tenant-summary { order: -1; } .metric-strip { grid-template-columns: repeat(3, minmax(120px, 1fr)); } .metric-item:nth-child(5n) { border-right: 1px solid var(--ui-border); } .metric-item:nth-child(3n) { border-right: 0; } }
@media (max-width: 640px) { .metric-strip { grid-template-columns: repeat(2, minmax(110px, 1fr)); } .metric-item:nth-child(3n) { border-right: 1px solid var(--ui-border); } .metric-item:nth-child(2n) { border-right: 0; } .dashboard-heading { align-items: center; } .work-copy small { white-space: normal; } }
</style>
