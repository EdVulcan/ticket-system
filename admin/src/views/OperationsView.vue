<template>
  <section class="max-w-[1500px] mx-auto">
    <div class="flex items-center justify-between mb-5">
      <div>
        <h1 class="text-xl font-semibold text-gray-900">运营工作台</h1>
        <p class="text-sm text-gray-500 mt-1">按租户能力展示景区履约、渠道、团队、结算和现场任务。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新" @click="loadActiveTab" />
    </div>

    <el-tabs v-model="activeTab" @tab-change="loadActiveTab">
      <el-tab-pane v-if="hasCapability('supplier')" label="景区" name="scenic">
        <el-table :data="rows.scenic" v-loading="loading">
          <el-table-column prop="code" label="编码" width="150" />
          <el-table-column prop="name" label="景区名称" min-width="220" />
          <el-table-column prop="status" label="状态" width="120" />
          <el-table-column prop="address" label="地址" min-width="260" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasAnyCapability('supplier', 'distributor')" label="渠道" name="channels">
        <el-table :data="rows.channels" v-loading="loading">
          <el-table-column prop="code" label="渠道编码" width="180" />
          <el-table-column prop="name" label="名称" min-width="200" />
          <el-table-column prop="provider" label="类型" width="140" />
          <el-table-column prop="status" label="状态" width="120" />
          <el-table-column prop="permissions_json" label="权限" min-width="260" show-overflow-tooltip />
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasCapability('travel_agency')" label="团队" name="teams">
        <el-table :data="rows.teams" v-loading="loading">
          <el-table-column prop="group_no" label="团号" width="180" />
          <el-table-column prop="name" label="团队" min-width="200" />
          <el-table-column prop="visit_date" label="到园日期" width="180" />
          <el-table-column prop="planned_count" label="计划人数" width="110" />
          <el-table-column prop="entered_count" label="已入园" width="100" />
          <el-table-column prop="status" label="状态" width="120" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasAnyCapability('supplier', 'distributor')" label="结算" name="settlements">
        <el-table :data="rows.settlements" v-loading="loading">
          <el-table-column prop="statement_no" label="结算单" width="210" />
          <el-table-column prop="period_start" label="开始" width="170" />
          <el-table-column prop="period_end" label="结束" width="170" />
          <el-table-column label="净额" width="130"><template #default="{ row }">¥{{ cents(row.net_cents) }}</template></el-table-column>
          <el-table-column prop="status" label="状态" width="150" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasAnyCapability('supplier', 'distributor')" label="总账" name="ledger">
        <el-table :data="rows.ledger" v-loading="loading">
          <el-table-column prop="created_at" label="时间" width="180" />
          <el-table-column prop="entry_type" label="事实类型" width="180" />
          <el-table-column prop="related_order_no" label="订单" width="210" />
          <el-table-column label="金额" width="130"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column>
          <el-table-column prop="memo" label="说明" min-width="260" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasCapability('supplier')" label="班次" name="shifts">
        <el-table :data="rows.shifts" v-loading="loading">
          <el-table-column prop="shift_no" label="班次" width="220" />
          <el-table-column prop="device_id" label="设备" width="100" />
          <el-table-column prop="operator_id" label="操作员" width="110" />
          <el-table-column prop="opened_at" label="开班" width="180" />
          <el-table-column label="备用金" width="110"><template #default="{ row }">¥{{ cents(row.opening_cents) }}</template></el-table-column>
          <el-table-column label="应交" width="110"><template #default="{ row }">¥{{ cents(row.expected_cents) }}</template></el-table-column>
          <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="shiftStatusType(row.status)">{{ shiftStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="120" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openShiftDetail(row)">查看与复核</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasCapability('supplier')" label="打印" name="prints">
        <el-table :data="rows.prints" v-loading="loading">
          <el-table-column prop="order_no" label="订单" width="220" />
          <el-table-column prop="device_id" label="设备" width="100" />
          <el-table-column prop="status" label="状态" width="120" />
          <el-table-column prop="attempt_count" label="尝试" width="90" />
          <el-table-column prop="last_error" label="最后错误" min-width="280" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasCapability('supplier')" label="告警" name="alerts">
        <el-table :data="rows.alerts" v-loading="loading">
          <el-table-column prop="opened_at" label="发生时间" width="180" />
          <el-table-column prop="device_id" label="设备" width="100" />
          <el-table-column prop="type" label="类型" width="120" />
          <el-table-column prop="status" label="状态" width="120" />
          <el-table-column prop="message" label="详情" min-width="300" />
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="shiftDialog" title="班次对账与复核" width="860px" align-center :close-on-click-modal="false">
      <div v-loading="shiftDetailLoading" class="shift-detail">
        <div class="shift-overview">
          <div><span>班次</span><strong>{{ shiftDetail?.shift?.shift_no || '-' }}</strong></div>
          <div><span>开班备用金</span><strong>¥{{ cents(shiftDetail?.shift?.opening_cents) }}</strong></div>
          <div><span>应交现金</span><strong>¥{{ cents(shiftDetail?.cash_expected_cents) }}</strong></div>
          <div><span>有效实盘</span><strong>¥{{ cents(shiftDetail?.effective_closing_cents) }}</strong></div>
          <div><span>当前差异</span><strong :class="varianceClass(shiftDetail?.effective_variance_cents)">{{ signedCents(shiftDetail?.effective_variance_cents) }}</strong></div>
        </div>

        <h3 class="detail-title">支付方式汇总</h3>
        <el-table :data="shiftPaymentRows" size="small" border>
          <el-table-column prop="label" label="方式" min-width="120" />
          <el-table-column label="实收" min-width="130"><template #default="{ row }">¥{{ cents(row.gross_cents) }}</template></el-table-column>
          <el-table-column label="退款" min-width="130"><template #default="{ row }">¥{{ cents(row.refund_cents) }}</template></el-table-column>
          <el-table-column label="净收" min-width="130"><template #default="{ row }"><strong>¥{{ cents(row.net_cents) }}</strong></template></el-table-column>
          <el-table-column prop="payment_count" label="支付笔数" width="110" />
        </el-table>

        <h3 class="detail-title">更正记录</h3>
        <el-table :data="shiftDetail?.corrections || []" size="small" empty-text="没有更正记录">
          <el-table-column prop="sequence" label="序号" width="70" />
          <el-table-column label="更正前" width="120"><template #default="{ row }">¥{{ cents(row.previous_closing_cents) }}</template></el-table-column>
          <el-table-column label="更正后" width="120"><template #default="{ row }">¥{{ cents(row.corrected_closing_cents) }}</template></el-table-column>
          <el-table-column prop="operator_id" label="主管" width="90" />
          <el-table-column prop="reason" label="原因" min-width="220" />
          <el-table-column prop="created_at" label="时间" width="180" />
        </el-table>

        <div v-if="isSupervisor && shiftDetail?.shift && shiftDetail.shift.status !== 'open'" class="supervisor-actions">
          <section>
            <h3>追加实盘更正</h3>
            <p>只追加新记录，不修改收银员原始关班金额。</p>
            <el-input-number v-model="correctionForm.amount" :min="0" :precision="2" :controls="false" placeholder="更正后实盘金额" />
            <el-input v-model="correctionForm.reason" maxlength="255" placeholder="必须填写更正原因" />
            <el-button type="warning" :loading="shiftActionLoading" @click="submitCorrection">追加更正</el-button>
          </section>
          <section v-if="shiftDetail.shift.status === 'closed'">
            <h3>主管复核</h3>
            <p>确认支付、退款、实盘和差异均已核对。</p>
            <el-input v-model="reconcileNotes" maxlength="255" placeholder="复核说明" />
            <el-button type="success" :loading="shiftActionLoading" @click="submitReconcile">确认复核</el-button>
          </section>
          <section v-else class="reconciled-state">
            <h3>已完成复核</h3>
            <p>{{ shiftDetail.shift.reconcile_notes || '主管已确认本班次。' }}</p>
          </section>
        </div>
      </div>
      <template #footer><el-button @click="shiftDialog = false">关闭</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const user = computed<any>(() => {
  try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} }
})
const capabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const hasCapability = (value: string) => capabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(hasCapability)
const isSupervisor = computed(() => ['admin', 'super_admin'].includes(user.value.role))
const firstTab = () => hasCapability('supplier') ? 'scenic' : hasCapability('travel_agency') ? 'teams' : 'channels'
const activeTab = ref(firstTab())
const loading = ref(false)
const rows = reactive<Record<string, any[]>>({ scenic: [], channels: [], teams: [], settlements: [], ledger: [], shifts: [], prints: [], alerts: [] })
const endpoints: Record<string, string> = {
  scenic: '/scenic-areas', channels: '/channel-accounts', teams: '/teams', settlements: '/settlements', ledger: '/finance/ledger', shifts: '/operations/shifts', prints: '/operations/print-jobs', alerts: '/operations/alerts'
}
const loadActiveTab = async () => {
  loading.value = true
  try {
    const response = await request.get(endpoints[activeTab.value], { params: { page: 1, page_size: 100 } })
    rows[activeTab.value] = response.data.data || []
  } finally { loading.value = false }
}
const cents = (value: number) => ((value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const varianceClass = (value: number) => Number(value || 0) === 0 ? 'variance-ok' : 'variance-alert'
const shiftStatusLabel = (status: string) => ({ open: '当班中', closed: '待复核', reconciled: '已复核' } as Record<string, string>)[status] || status
const shiftStatusType = (status: string) => status === 'reconciled' ? 'success' : status === 'closed' ? 'warning' : 'primary'

const shiftDialog = ref(false)
const shiftDetailLoading = ref(false)
const shiftActionLoading = ref(false)
const shiftDetail = ref<any>(null)
const correctionForm = reactive({ amount: 0, reason: '' })
const reconcileNotes = ref('')
const methodLabels: Record<string, string> = { cash: '现金', wechat: '微信', alipay: '支付宝' }
const shiftPaymentRows = computed(() => ['cash', 'wechat', 'alipay'].map(method => {
  const row = shiftDetail.value?.payments?.find((item: any) => item.method === method) || {}
  return { method, label: methodLabels[method], payment_count: 0, gross_cents: 0, refund_cents: 0, net_cents: 0, ...row }
}))

const loadShiftDetail = async (id: number) => {
  shiftDetailLoading.value = true
  try {
    const response = await request.get(`/operations/shifts/${id}/summary`)
    shiftDetail.value = response.data
    correctionForm.amount = Number(response.data.effective_closing_cents || 0) / 100
    correctionForm.reason = ''
    reconcileNotes.value = response.data.shift?.reconcile_notes || ''
  } finally { shiftDetailLoading.value = false }
}

const openShiftDetail = async (row: any) => {
  shiftDialog.value = true
  await loadShiftDetail(row.id)
}

const submitCorrection = async () => {
  if (!shiftDetail.value?.shift?.id || !correctionForm.reason.trim()) { ElMessage.warning('请填写更正金额和原因'); return }
  shiftActionLoading.value = true
  try {
    await request.post(`/operations/shifts/${shiftDetail.value.shift.id}/corrections`, { corrected_cents: Math.round(correctionForm.amount * 100), reason: correctionForm.reason.trim() })
    ElMessage.success('更正记录已追加')
    await loadShiftDetail(shiftDetail.value.shift.id)
    await loadActiveTab()
  } finally { shiftActionLoading.value = false }
}

const submitReconcile = async () => {
  if (!shiftDetail.value?.shift?.id) return
  shiftActionLoading.value = true
  try {
    await request.post(`/operations/shifts/${shiftDetail.value.shift.id}/reconcile`, { notes: reconcileNotes.value.trim() })
    ElMessage.success('班次复核完成')
    await loadShiftDetail(shiftDetail.value.shift.id)
    await loadActiveTab()
  } finally { shiftActionLoading.value = false }
}
onMounted(loadActiveTab)
</script>

<style scoped>
.shift-detail { min-height: 280px; }
.shift-overview { display: grid; grid-template-columns: 1.5fr repeat(4, 1fr); gap: 10px; padding: 14px; border: 1px solid #dfe3dc; border-radius: 7px; background: #f8faf7; }
.shift-overview > div { min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.shift-overview span { color: #727a72; font-size: 12px; }
.shift-overview strong { overflow: hidden; text-overflow: ellipsis; font-size: 16px; white-space: nowrap; }
.detail-title { margin: 20px 0 9px; font-size: 15px; }
.variance-ok { color: #16814e; }
.variance-alert { color: #c24141; }
.supervisor-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 20px; }
.supervisor-actions section { padding: 14px; border: 1px solid #dfe3dc; border-radius: 7px; background: #fff; }
.supervisor-actions h3 { margin: 0; font-size: 15px; }
.supervisor-actions p { min-height: 36px; margin: 5px 0 12px; color: #737b73; font-size: 12px; line-height: 18px; }
.supervisor-actions :deep(.el-input-number), .supervisor-actions :deep(.el-input) { width: 100%; margin-bottom: 9px; }
.supervisor-actions :deep(.el-button) { width: 100%; }
.reconciled-state { background: #f3f8f4 !important; }
</style>
