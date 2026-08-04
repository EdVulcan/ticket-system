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
        <div class="flex justify-end mb-3">
          <el-button v-if="canScenicWrite" type="primary" @click="openCreateScenic">新增景区</el-button>
        </div>
        <el-table :data="rows.scenic" v-loading="loading">
          <el-table-column prop="code" label="编码" width="150" />
          <el-table-column prop="name" label="景区名称" min-width="220" />
          <el-table-column label="状态" width="120"><template #default="{ row }">{{ scenicStatusText(row.status) }}</template></el-table-column>
          <el-table-column v-if="canScenicWrite" label="操作" width="100" fixed="right">
            <template #default="{ row }"><el-button link type="primary" @click="openEditScenic(row)">编辑</el-button></template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasAnyCapability('supplier', 'distributor')" label="渠道" name="channels">
        <el-table :data="rows.channels" v-loading="loading">
          <el-table-column prop="code" label="渠道编码" width="180" />
          <el-table-column prop="name" label="名称" min-width="200" />
          <el-table-column label="类型" width="140"><template #default="{ row }">{{ channelProviderText(row.provider) }}</template></el-table-column>
          <el-table-column label="状态" width="120"><template #default="{ row }">{{ commonStatusText(row.status) }}</template></el-table-column>
          <el-table-column label="权限" min-width="260"><template #default="{ row }">{{ channelPermissionsText(row.permissions_json) }}</template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasCapability('travel_agency')" label="团队" name="teams">
        <el-table :data="rows.teams" v-loading="loading">
          <el-table-column prop="group_no" label="团号" width="180" />
          <el-table-column prop="name" label="团队" min-width="200" />
          <el-table-column prop="visit_date" label="到园日期" width="180" />
          <el-table-column prop="planned_count" label="计划人数" width="110" />
          <el-table-column prop="entered_count" label="已入园" width="100" />
          <el-table-column label="状态" width="120"><template #default="{ row }">{{ teamStatusText(row.status) }}</template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasAnyCapability('supplier', 'distributor')" label="结算" name="settlements">
        <div class="flex items-center justify-between mb-3">
          <span class="text-sm text-gray-500">只处理供应商与分销商之间的履约对账和付款确认。</span>
          <el-button v-if="hasCapability('supplier') && canSettlementWrite" type="primary" @click="openGenerateSettlement">生成结算单</el-button>
        </div>
        <el-table :data="rows.settlements" v-loading="loading">
          <el-table-column prop="statement_no" label="结算单" width="210" />
          <el-table-column prop="period_start" label="开始" width="170" />
          <el-table-column prop="period_end" label="结束" width="170" />
          <el-table-column label="应结净额" width="130"><template #default="{ row }">¥{{ cents(Number(row.net_cents || 0) + Number(row.adjustment_cents || 0)) }}</template></el-table-column>
          <el-table-column label="状态" width="150"><template #default="{ row }"><el-tag :type="settlementStatusType(row.status)">{{ settlementStatusText(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="100" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openSettlement(row)">详情</el-button></template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasAnyCapability('supplier', 'distributor')" label="总账" name="ledger">
        <h3 class="detail-title">上下游账户</h3>
        <el-table :data="financialAccounts" v-loading="loading" size="small" class="mb-5">
          <el-table-column label="合作方" min-width="180"><template #default="{ row }"><div>{{ row.side === 'managed' ? row.owner_name : row.supplier_name }}</div><div class="text-xs text-gray-400">{{ row.side === 'managed' ? row.owner_code : row.supplier_code }}</div></template></el-table-column>
          <el-table-column label="预付余额"><template #default="{ row }">¥{{ cents(row.balance_cents) }}</template></el-table-column>
          <el-table-column label="授信额度"><template #default="{ row }">¥{{ cents(row.credit_line_cents) }}</template></el-table-column>
          <el-table-column label="已用授信"><template #default="{ row }">¥{{ cents(row.used_credit_cents) }}</template></el-table-column>
          <el-table-column label="可用授信"><template #default="{ row }">¥{{ cents(Math.max(0, Number(row.credit_line_cents || 0) - Number(row.used_credit_cents || 0))) }}</template></el-table-column>
          <el-table-column label="冻结"><template #default="{ row }">¥{{ cents(row.frozen_cents) }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }">{{ accountStatusText(row.status) }}</template></el-table-column>
        </el-table>
        <h3 class="detail-title">账户流水</h3>
        <el-table :data="rows.ledger" v-loading="loading">
          <el-table-column prop="created_at" label="时间" width="180" />
          <el-table-column label="流水类型" width="180"><template #default="{ row }">{{ ledgerTypeText(row.entry_type) }}</template></el-table-column>
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
          <el-table-column label="状态" width="120"><template #default="{ row }">{{ printStatusText(row.status) }}</template></el-table-column>
          <el-table-column prop="attempt_count" label="尝试" width="90" />
          <el-table-column label="最后错误" min-width="280"><template #default="{ row }">{{ localizeDisplayText(row.last_error, '暂无错误') }}</template></el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane v-if="hasCapability('supplier')" label="告警" name="alerts">
        <el-table :data="rows.alerts" v-loading="loading">
          <el-table-column prop="opened_at" label="发生时间" width="180" />
          <el-table-column prop="device_id" label="设备" width="100" />
          <el-table-column label="类型" width="120"><template #default="{ row }">{{ alertTypeText(row.type) }}</template></el-table-column>
          <el-table-column label="状态" width="120"><template #default="{ row }">{{ commonStatusText(row.status) }}</template></el-table-column>
          <el-table-column label="详情" min-width="300"><template #default="{ row }">{{ localizeDisplayText(row.message, '设备运行异常') }}</template></el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="scenicDialog" :title="scenicForm.id ? '编辑景区' : '新增景区'" width="520px" align-center>
      <el-form label-position="top">
        <el-form-item label="景区编码" required>
          <el-input v-model="scenicForm.code" :disabled="Boolean(scenicForm.id)" maxlength="50" placeholder="例如：MAIN_PARK" />
          <div class="text-xs text-gray-500 mt-1">编码用于设备和渠道识别，创建后不可修改。</div>
        </el-form-item>
        <el-form-item label="景区名称" required><el-input v-model="scenicForm.name" maxlength="100" placeholder="请输入游客和员工熟悉的名称" /></el-form-item>
        <el-form-item label="经营状态">
          <el-select v-model="scenicForm.status" class="w-full">
            <el-option label="正常营业" value="active" />
            <el-option label="暂停营业" value="frozen" />
            <el-option label="已关闭" value="closed" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="scenicDialog = false">取消</el-button>
        <el-button type="primary" :loading="scenicSaving" @click="saveScenic">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="generateSettlementDialog" title="生成结算单" width="520px" align-center>
      <el-form label-position="top">
        <el-form-item label="结算对象" required>
          <el-select v-model="settlementGenerate.distributor_tenant_id" filterable class="w-full" placeholder="选择分销商或旅行社">
            <el-option v-for="partner in settlementPartners" :key="partner.agent_tenant_id" :label="partner.agent_name" :value="partner.agent_tenant_id" />
          </el-select>
        </el-form-item>
        <el-form-item label="结算周期" required><el-date-picker v-model="settlementGenerate.period" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" class="w-full" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="generateSettlementDialog = false">取消</el-button><el-button type="primary" :loading="settlementActionLoading" @click="generateSettlement">生成</el-button></template>
    </el-dialog>

    <el-dialog v-model="settlementDialog" title="结算对账详情" width="980px" align-center :close-on-click-modal="false">
      <div v-loading="settlementDetailLoading" class="space-y-5">
        <el-descriptions v-if="settlementDetail" :column="3" border>
          <el-descriptions-item label="结算单">{{ settlementDetail.statement_no }}</el-descriptions-item>
          <el-descriptions-item label="供应商">租户 {{ settlementDetail.supplier_tenant_id }}</el-descriptions-item>
          <el-descriptions-item label="分销商">租户 {{ settlementDetail.distributor_tenant_id }}</el-descriptions-item>
          <el-descriptions-item label="履约总额">¥{{ cents(settlementDetail.gross_cents) }}</el-descriptions-item>
          <el-descriptions-item label="退款冲减">¥{{ cents(settlementDetail.refund_cents) }}</el-descriptions-item>
          <el-descriptions-item label="佣金">¥{{ cents(settlementDetail.commission_cents) }}</el-descriptions-item>
          <el-descriptions-item label="追加调整">{{ signedCents(settlementDetail.adjustment_cents) }}</el-descriptions-item>
          <el-descriptions-item label="最终应结"><strong>¥{{ cents(settlementPayable) }}</strong></el-descriptions-item>
          <el-descriptions-item label="状态"><el-tag :type="settlementStatusType(settlementDetail.status)">{{ settlementStatusText(settlementDetail.status) }}</el-tag></el-descriptions-item>
          <el-descriptions-item v-if="settlementDetail.dispute_reason" label="争议原因" :span="3">{{ settlementDetail.dispute_reason }}</el-descriptions-item>
          <el-descriptions-item v-if="settlementDetail.payment_proof" label="付款凭证" :span="3">{{ settlementDetail.payment_proof }}</el-descriptions-item>
        </el-descriptions>

        <section v-if="settlementDetail">
          <h3 class="detail-title">履约结算行</h3>
          <el-table :data="settlementDetail.lines || []" size="small" border>
            <el-table-column prop="fulfillment_order_id" label="履约单编号" width="130" />
            <el-table-column label="履约总额"><template #default="{ row }">¥{{ cents(row.gross_cents) }}</template></el-table-column>
            <el-table-column label="退款冲减"><template #default="{ row }">¥{{ cents(row.refund_cents) }}</template></el-table-column>
            <el-table-column label="佣金"><template #default="{ row }">¥{{ cents(row.commission_cents) }}</template></el-table-column>
            <el-table-column label="净额"><template #default="{ row }">¥{{ cents(row.net_cents) }}</template></el-table-column>
          </el-table>
        </section>

        <section v-if="settlementDetail?.adjustments?.length">
          <h3 class="detail-title">争议调整记录</h3>
          <el-table :data="settlementDetail.adjustments" size="small">
            <el-table-column prop="sequence" label="序号" width="70" />
            <el-table-column label="调整金额" width="130"><template #default="{ row }">{{ signedCents(row.amount_cents) }}</template></el-table-column>
            <el-table-column prop="actor_tenant_id" label="操作租户" width="100" />
            <el-table-column prop="reason" label="原因" min-width="260" />
            <el-table-column prop="created_at" label="时间" width="180" />
          </el-table>
        </section>

        <div v-if="settlementDetail" class="flex justify-end gap-2">
          <el-button icon="Download" @click="exportSettlement">导出对账单</el-button>
          <el-button v-if="canSupplierConfirm" type="primary" :loading="settlementActionLoading" @click="updateSettlementStatus('supplier_confirmed')">供应商确认</el-button>
          <el-button v-if="canDistributorConfirm" type="success" :loading="settlementActionLoading" @click="updateSettlementStatus('confirmed')">分销商确认</el-button>
          <el-button v-if="canDispute" type="warning" :loading="settlementActionLoading" @click="disputeSettlement">提出争议</el-button>
          <el-button v-if="canAdjust" type="warning" plain @click="settlementAdjustmentDialog = true">追加调整</el-button>
          <el-button v-if="canMarkPaid" type="success" :loading="settlementActionLoading" @click="markSettlementPaid">登记付款</el-button>
        </div>
      </div>
      <template #footer><el-button @click="settlementDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="settlementAdjustmentDialog" title="追加争议调整" width="520px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="调整金额"><el-input-number v-model="settlementAdjustment.amount" :precision="2" :step="1" :controls="false" class="w-full" /><div class="text-xs text-gray-500 mt-1">正数增加应结，负数减少应结。</div></el-form-item>
        <el-form-item label="调整原因" required><el-input v-model="settlementAdjustment.reason" type="textarea" :rows="3" maxlength="255" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="settlementAdjustmentDialog = false">取消</el-button><el-button type="primary" :loading="settlementActionLoading" @click="submitSettlementAdjustment">追加并重新确认</el-button></template>
    </el-dialog>

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
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { localizeDisplayText } from '@/utils/localize'
import { hasPermission } from '@/utils/permissions'

const user = computed<any>(() => {
  try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} }
})
const capabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const hasCapability = (value: string) => capabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(hasCapability)
const isSupervisor = computed(() => hasPermission(user.value, 'onsite.manage'))
const canScenicWrite = computed(() => hasPermission(user.value, 'onsite.manage'))
const canSettlementWrite = computed(() => hasPermission(user.value, 'settlements.write'))
const currentTenantID = computed(() => Number(user.value.tenant_id || 0))
const firstTab = () => hasCapability('supplier') ? 'scenic' : hasCapability('travel_agency') ? 'teams' : 'channels'
const activeTab = ref(firstTab())
const loading = ref(false)
const rows = reactive<Record<string, any[]>>({ scenic: [], channels: [], teams: [], settlements: [], ledger: [], shifts: [], prints: [], alerts: [] })
const financialAccounts = ref<any[]>([])
const endpoints: Record<string, string> = {
  scenic: '/scenic-areas', channels: '/channel-accounts', teams: '/teams', settlements: '/settlements', ledger: '/finance/ledger', shifts: '/operations/shifts', prints: '/operations/print-jobs', alerts: '/operations/alerts'
}
const loadActiveTab = async () => {
  loading.value = true
  try {
    if (activeTab.value === 'ledger') {
      const accountRequests: Promise<any>[] = []
      const ledgerRequests: Promise<any>[] = []
      if (hasCapability('supplier')) {
        accountRequests.push(request.get('/finance/managed-accounts'))
        ledgerRequests.push(request.get('/finance/managed-ledger', { params: { page: 1, page_size: 100 } }))
      }
      if (hasCapability('distributor')) {
        accountRequests.push(request.get('/finance/accounts'))
        ledgerRequests.push(request.get('/finance/ledger', { params: { page: 1, page_size: 100 } }))
      }
      const [accountResponses, ledgerResponses] = await Promise.all([Promise.all(accountRequests), Promise.all(ledgerRequests)])
      financialAccounts.value = accountResponses.flatMap((response, index) => (response.data.data || []).map((row: any) => ({ ...row, side: hasCapability('supplier') && index === 0 ? 'managed' : 'owned' })))
      rows.ledger = ledgerResponses.flatMap(response => response.data.data || []).sort((left, right) => String(right.created_at).localeCompare(String(left.created_at)))
      return
    }
    const response = await request.get(endpoints[activeTab.value], { params: { page: 1, page_size: 100 } })
    rows[activeTab.value] = response.data.data || []
  } finally { loading.value = false }
}
const cents = (value: number) => ((value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const varianceClass = (value: number) => Number(value || 0) === 0 ? 'variance-ok' : 'variance-alert'
const shiftStatusLabel = (status: string) => ({ open: '当班中', closed: '待复核', reconciled: '已复核' } as Record<string, string>)[status] || '未知状态'
const shiftStatusType = (status: string) => status === 'reconciled' ? 'success' : status === 'closed' ? 'warning' : 'primary'
const scenicStatusText = (status: string) => ({ active: '正常营业', inactive: '暂停营业', disabled: '已停用', closed: '已关闭' } as Record<string, string>)[status] || '未知状态'
const channelProviderText = (provider: string) => ({ core: '通用渠道', ctrip: '携程', meituan: '美团', distributor: '分销商', miniapp: '小程序', h5: '网页商城' } as Record<string, string>)[provider] || '其他渠道'
const commonStatusText = (status: string) => ({ active: '已启用', disabled: '已停用', pending: '待处理', processing: '处理中', completed: '已完成', failed: '处理失败', open: '待处理', closed: '已关闭', resolved: '已解决' } as Record<string, string>)[status] || '未知状态'
const teamStatusText = (status: string) => ({ draft: '草稿', confirmed: '待入园', partial_entry: '部分入园', entered: '已全部入园', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const accountStatusText = (status: string) => ({ active: '正常', frozen: '已冻结', disabled: '已停用' } as Record<string, string>)[status] || '未知状态'
const printStatusText = (status: string) => ({ pending: '待打印', processing: '打印中', completed: '已打印', failed: '打印失败', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const alertTypeText = (type: string) => ({ offline: '设备离线', heartbeat: '心跳异常', print: '打印异常', payment: '支付异常', gate: '闸机异常' } as Record<string, string>)[type] || '其他告警'
const ledgerTypeText = (type: string) => ({ recharge: '预付款充值', payment: '订单扣款', refund: '订单退款', settlement: '结算入账', credit_used: '使用授信', credit_repaid: '归还授信', freeze: '资金冻结', unfreeze: '资金解冻', adjustment: '人工调整' } as Record<string, string>)[type] || '其他流水'
const channelPermissionsText = (value: string) => {
  if (!value) return '未配置'
  try {
    const permissions = typeof value === 'string' ? JSON.parse(value) : value
    const names: Record<string, string> = { order: '下单', refund: '退款', query: '查询', verify: '核销', callback: '回调' }
    if (Array.isArray(permissions)) return permissions.map(item => names[item] || '其他权限').join('、') || '未配置'
    return Object.entries(permissions).filter(([, enabled]) => enabled).map(([key]) => names[key] || '其他权限').join('、') || '未配置'
  } catch { return '已配置' }
}

const scenicDialog = ref(false)
const scenicSaving = ref(false)
const scenicForm = reactive({ id: 0, code: '', name: '', status: 'active' })

const openCreateScenic = () => {
  Object.assign(scenicForm, { id: 0, code: '', name: '', status: 'active' })
  scenicDialog.value = true
}

const openEditScenic = (row: any) => {
  Object.assign(scenicForm, { id: row.id, code: row.code, name: row.name, status: row.status || 'active' })
  scenicDialog.value = true
}

const saveScenic = async () => {
  scenicForm.code = scenicForm.code.trim()
  scenicForm.name = scenicForm.name.trim()
  if (!scenicForm.code || !scenicForm.name) { ElMessage.warning('请填写景区编码和名称'); return }
  scenicSaving.value = true
  try {
    const payload = { code: scenicForm.code, name: scenicForm.name, status: scenicForm.status }
    if (scenicForm.id) await request.put(`/scenic-areas/${scenicForm.id}`, payload)
    else await request.post('/scenic-areas', payload)
    scenicDialog.value = false
    ElMessage.success(scenicForm.id ? '景区已更新' : '景区已创建')
    await loadActiveTab()
  } finally { scenicSaving.value = false }
}

const settlementDialog = ref(false)
const settlementDetailLoading = ref(false)
const settlementActionLoading = ref(false)
const settlementDetail = ref<any>(null)
const generateSettlementDialog = ref(false)
const settlementPartners = ref<any[]>([])
const settlementGenerate = reactive<{ distributor_tenant_id: number, period: string[] }>({ distributor_tenant_id: 0, period: [] })
const settlementAdjustmentDialog = ref(false)
const settlementAdjustment = reactive({ amount: 0, reason: '' })
const settlementPayable = computed(() => Number(settlementDetail.value?.net_cents || 0) + Number(settlementDetail.value?.adjustment_cents || 0))
const isSettlementSupplier = computed(() => currentTenantID.value > 0 && currentTenantID.value === Number(settlementDetail.value?.supplier_tenant_id))
const isSettlementDistributor = computed(() => currentTenantID.value > 0 && currentTenantID.value === Number(settlementDetail.value?.distributor_tenant_id))
const canSupplierConfirm = computed(() => canSettlementWrite.value && settlementDetail.value?.status === 'draft' && isSettlementSupplier.value)
const canDistributorConfirm = computed(() => canSettlementWrite.value && settlementDetail.value?.status === 'supplier_confirmed' && isSettlementDistributor.value)
const canDispute = computed(() => canSettlementWrite.value && ['supplier_confirmed', 'confirmed'].includes(settlementDetail.value?.status) && (isSettlementSupplier.value || isSettlementDistributor.value))
const canAdjust = computed(() => canSettlementWrite.value && settlementDetail.value?.status === 'disputed' && (isSettlementSupplier.value || isSettlementDistributor.value))
const canMarkPaid = computed(() => canSettlementWrite.value && settlementDetail.value?.status === 'confirmed' && isSettlementDistributor.value)
const settlementStatusText = (status: string) => ({ draft: '草稿', supplier_confirmed: '供应商已确认', confirmed: '双方已确认', disputed: '有争议', paid: '已付款' } as Record<string, string>)[status] || '未知状态'
const settlementStatusType = (status: string) => status === 'paid' ? 'success' : status === 'disputed' ? 'danger' : status === 'confirmed' ? 'success' : status === 'supplier_confirmed' ? 'warning' : 'info'

const loadSettlementDetail = async (id: number) => {
  settlementDetailLoading.value = true
  try { settlementDetail.value = (await request.get(`/settlements/${id}`)).data }
  finally { settlementDetailLoading.value = false }
}

const openSettlement = async (row: any) => {
  settlementDialog.value = true
  await loadSettlementDetail(row.id)
}

const exportSettlement = async () => {
  if (!settlementDetail.value?.id) return
  const response = await request.get(`/settlements/${settlementDetail.value.id}/export`, { responseType: 'blob' })
  const url = URL.createObjectURL(response.data)
  const link = document.createElement('a')
  link.href = url
  link.download = `${settlementDetail.value.statement_no}.csv`
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

const openGenerateSettlement = async () => {
  settlementGenerate.distributor_tenant_id = 0
  settlementGenerate.period = []
  generateSettlementDialog.value = true
  try { settlementPartners.value = ((await request.get('/distribution/agents')).data.data || []).filter((row: any) => row.status === 'active') }
  catch (e: any) { ElMessage.error(e.response?.data?.error || '合作方加载失败') }
}

const generateSettlement = async () => {
  if (!settlementGenerate.distributor_tenant_id || settlementGenerate.period.length !== 2) { ElMessage.warning('请选择分销商和结算周期'); return }
  settlementActionLoading.value = true
  try {
    const response = await request.post('/settlements/generate', { distributor_tenant_id: settlementGenerate.distributor_tenant_id, start_date: settlementGenerate.period[0], end_date: settlementGenerate.period[1] })
    generateSettlementDialog.value = false
    ElMessage.success('结算单已生成')
    await loadActiveTab()
    await openSettlement(response.data)
  } finally { settlementActionLoading.value = false }
}

const updateSettlementStatus = async (status: string, detail = '') => {
  if (!settlementDetail.value?.id) return
  settlementActionLoading.value = true
  try {
    await request.patch(`/settlements/${settlementDetail.value.id}/status`, { status, detail })
    ElMessage.success('结算状态已更新')
    await loadSettlementDetail(settlementDetail.value.id)
    await loadActiveTab()
  } finally { settlementActionLoading.value = false }
}

const disputeSettlement = async () => {
  const result = await ElMessageBox.prompt('请输入具体差异或争议原因', '提出结算争议', { inputType: 'textarea', inputValidator: value => value.trim() ? true : '争议原因必填' })
  await updateSettlementStatus('disputed', result.value.trim())
}

const submitSettlementAdjustment = async () => {
  if (!settlementDetail.value?.id || !settlementAdjustment.amount || !settlementAdjustment.reason.trim()) { ElMessage.warning('调整金额不能为 0，且必须填写原因'); return }
  settlementActionLoading.value = true
  try {
    await request.post(`/settlements/${settlementDetail.value.id}/adjustments`, { amount_cents: Math.round(settlementAdjustment.amount * 100), reason: settlementAdjustment.reason.trim() })
    settlementAdjustmentDialog.value = false
    settlementAdjustment.amount = 0
    settlementAdjustment.reason = ''
    ElMessage.success('调整已追加，结算单回到重新确认流程')
    await loadSettlementDetail(settlementDetail.value.id)
    await loadActiveTab()
  } finally { settlementActionLoading.value = false }
}

const markSettlementPaid = async () => {
  const result = await ElMessageBox.prompt('填写银行流水号、转账单号或付款凭证位置', '登记付款', { inputValidator: value => value.trim() ? true : '付款凭证必填' })
  await updateSettlementStatus('paid', result.value.trim())
}

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
