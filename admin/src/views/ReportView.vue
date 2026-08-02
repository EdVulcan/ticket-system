<template>
  <div class="report-page">
    <header class="page-header">
      <div>
        <h2>经营报表</h2>
        <p>按实际收退款和有效核销事实统计</p>
      </div>
      <div class="header-actions">
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          value-format="YYYY-MM-DD"
          :clearable="false"
        />
        <el-button type="primary" :icon="Search" :loading="loading" @click="refresh">查询</el-button>
        <el-button :icon="Download" :loading="exporting" @click="exportCurrent">导出 CSV</el-button>
      </div>
    </header>

    <el-tabs v-model="activeTab" class="report-tabs" @tab-change="handleTabChange">
      <el-tab-pane label="营业汇总表" name="business-summary" />
      <el-tab-pane label="营业明细表" name="business-details" />
      <el-tab-pane label="核销汇总表" name="verification-summary" />
      <el-tab-pane label="核销明细表" name="verification-details" />
    </el-tabs>

    <section class="filter-bar">
      <template v-if="isBusiness">
        <el-select v-model="filters.channel" clearable placeholder="全部销售渠道" class="filter-control">
          <el-option v-for="item in channelOptions" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
        <el-select v-model="filters.method" clearable placeholder="全部支付方式" class="filter-control">
          <el-option v-for="item in methodOptions" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
        <el-input v-if="isDetail" v-model="filters.orderNo" clearable placeholder="订单号" class="wide-filter" @keyup.enter="refresh" />
      </template>
      <template v-else>
        <el-select v-model="filters.scenicAreaId" clearable placeholder="全部景区" class="filter-control">
          <el-option v-for="area in scenicAreas" :key="area.id" :label="area.name" :value="area.id" />
        </el-select>
        <el-select v-model="filters.channel" clearable placeholder="全部销售渠道" class="filter-control">
          <el-option v-for="item in channelOptions" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
        <el-input v-model="filters.productName" clearable placeholder="票种名称" class="wide-filter" @keyup.enter="refresh" />
      </template>
    </section>

    <section v-if="!isDetail" class="summary-strip">
      <template v-if="isBusiness">
        <div><span>收款笔数</span><strong>{{ businessTotals.paymentCount }}</strong></div>
        <div><span>收款金额</span><strong>¥{{ yuan(businessTotals.grossCents) }}</strong></div>
        <div><span>退款金额</span><strong class="negative">-¥{{ yuan(businessTotals.refundCents) }}</strong></div>
        <div><span>营业净额</span><strong>¥{{ yuan(businessTotals.netCents) }}</strong></div>
      </template>
      <template v-else>
        <div><span>有效核销</span><strong>{{ verificationTotals.count }} 人次</strong></div>
        <div><span>确认收入</span><strong>¥{{ yuan(verificationTotals.incomeCents) }}</strong></div>
        <div class="summary-note">已退票的核销不会计入核销数量和收入</div>
      </template>
    </section>

    <section class="table-section" v-loading="loading">
      <el-table v-if="activeTab === 'business-summary'" :data="rows" stripe height="100%">
        <el-table-column prop="date" label="营业日期" width="120" />
        <el-table-column label="销售渠道" min-width="130"><template #default="{ row }">{{ channelName(row.channel) }}</template></el-table-column>
        <el-table-column label="支付方式" min-width="130"><template #default="{ row }">{{ methodName(row.method) }}</template></el-table-column>
        <el-table-column prop="payment_count" label="收款笔数" width="110" align="right" />
        <el-table-column prop="refund_count" label="退款笔数" width="110" align="right" />
        <el-table-column label="收款金额" width="140" align="right"><template #default="{ row }">¥{{ yuan(row.gross_cents) }}</template></el-table-column>
        <el-table-column label="退款金额" width="140" align="right"><template #default="{ row }"><span class="negative">-¥{{ yuan(row.refund_cents) }}</span></template></el-table-column>
        <el-table-column label="营业净额" width="140" align="right"><template #default="{ row }"><strong>¥{{ yuan(row.net_cents) }}</strong></template></el-table-column>
      </el-table>

      <el-table v-else-if="activeTab === 'business-details'" :data="rows" stripe height="100%">
        <el-table-column label="发生时间" width="180"><template #default="{ row }">{{ formatTime(row.occurred_at) }}</template></el-table-column>
        <el-table-column label="业务类型" width="100"><template #default="{ row }"><el-tag :type="row.fact_type === 'refund' ? 'danger' : 'success'" effect="plain">{{ row.fact_type === 'refund' ? '退款' : '收款' }}</el-tag></template></el-table-column>
        <el-table-column prop="order_no" label="订单号" min-width="190" show-overflow-tooltip />
        <el-table-column prop="product_names" label="票种" min-width="180" show-overflow-tooltip />
        <el-table-column label="渠道 / 支付" min-width="150"><template #default="{ row }">{{ channelName(row.channel) }} / {{ methodName(row.method) }}</template></el-table-column>
        <el-table-column label="金额" width="130" align="right"><template #default="{ row }"><strong :class="{ negative: row.fact_type === 'refund' }">{{ row.fact_type === 'refund' ? '-' : '' }}¥{{ yuan(row.amount_cents) }}</strong></template></el-table-column>
        <el-table-column prop="contact_name" label="联系人" width="110" />
        <el-table-column prop="contact_phone" label="手机号" width="130" />
        <el-table-column prop="reason" label="备注" min-width="150" show-overflow-tooltip />
      </el-table>

      <el-table v-else-if="activeTab === 'verification-summary'" :data="rows" stripe height="100%">
        <el-table-column prop="date" label="核销日期" width="120" />
        <el-table-column prop="scenic_area_name" label="景区" min-width="140" />
        <el-table-column prop="product_name" label="票种" min-width="220" show-overflow-tooltip />
        <el-table-column prop="seller_name" label="销售方" min-width="150" />
        <el-table-column label="销售渠道" width="130"><template #default="{ row }">{{ channelName(row.channel) }}</template></el-table-column>
        <el-table-column prop="verified_count" label="核销人次" width="120" align="right" />
        <el-table-column label="确认收入" width="150" align="right"><template #default="{ row }"><strong>¥{{ yuan(row.income_cents) }}</strong></template></el-table-column>
      </el-table>

      <el-table v-else :data="rows" stripe height="100%">
        <el-table-column label="核销时间" width="180"><template #default="{ row }">{{ formatTime(row.check_in_time) }}</template></el-table-column>
        <el-table-column prop="scenic_area_name" label="景区" min-width="130" />
        <el-table-column prop="product_name" label="票种" min-width="210" show-overflow-tooltip />
        <el-table-column prop="ticket_code" label="票码" min-width="170" show-overflow-tooltip />
        <el-table-column prop="order_no" label="订单号" min-width="180" show-overflow-tooltip />
        <el-table-column prop="seller_name" label="销售方" min-width="130" />
        <el-table-column prop="verified_count" label="核销人次" width="110" align="right" />
        <el-table-column label="确认收入" width="130" align="right"><template #default="{ row }"><strong>¥{{ yuan(row.income_cents) }}</strong></template></el-table-column>
        <el-table-column prop="visitor_name" label="游客" width="100" />
        <el-table-column prop="check_point_name" label="核销点" width="130" />
      </el-table>
    </section>

    <footer v-if="isDetail" class="pagination-bar">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :page-sizes="[10, 20, 40]"
        :total="total"
        layout="total, sizes, prev, pager, next"
        @current-change="loadRows"
        @size-change="handlePageSizeChange"
      />
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Download, Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import dayjs from 'dayjs'
import request from '@/utils/request'

type ReportTab = 'business-summary' | 'business-details' | 'verification-summary' | 'verification-details'

const activeTab = ref<ReportTab>('business-summary')
const dateRange = ref([dayjs().subtract(29, 'day').format('YYYY-MM-DD'), dayjs().format('YYYY-MM-DD')])
const filters = reactive({ channel: '', method: '', orderNo: '', productName: '', scenicAreaId: '' as string | number })
const rows = ref<any[]>([])
const scenicAreas = ref<any[]>([])
const loading = ref(false)
const exporting = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const channelOptions = [
  { value: 'window', label: '窗口直销' },
  { value: 'distributor', label: '分销商' },
  { value: 'ota', label: 'OTA 渠道' },
  { value: 'miniapp', label: '小程序 / H5' },
  { value: 'team', label: '旅行社团队' }
]
const methodOptions = [
  { value: 'cash', label: '现金' },
  { value: 'wechat', label: '微信' },
  { value: 'alipay', label: '支付宝' },
  { value: 'mixed', label: '组合支付' }
]

const isBusiness = computed(() => activeTab.value.startsWith('business'))
const isDetail = computed(() => activeTab.value.endsWith('details'))
const businessTotals = computed(() => rows.value.reduce((sum, row) => ({
  paymentCount: sum.paymentCount + Number(row.payment_count || 0),
  grossCents: sum.grossCents + Number(row.gross_cents || 0),
  refundCents: sum.refundCents + Number(row.refund_cents || 0),
  netCents: sum.netCents + Number(row.net_cents || 0)
}), { paymentCount: 0, grossCents: 0, refundCents: 0, netCents: 0 }))
const verificationTotals = computed(() => rows.value.reduce((sum, row) => ({
  count: sum.count + Number(row.verified_count || 0),
  incomeCents: sum.incomeCents + Number(row.income_cents || 0)
}), { count: 0, incomeCents: 0 }))

const channelName = (value: string) => channelOptions.find(item => item.value === value)?.label || value || '-'
const methodName = (value: string) => methodOptions.find(item => item.value === value)?.label || value || '-'
const yuan = (value: number) => (Number(value || 0) / 100).toFixed(2)
const formatTime = (value: string) => value ? dayjs(value).format('YYYY-MM-DD HH:mm:ss') : '-'

const queryParams = (exportAll = false) => ({
  start_date: dateRange.value[0],
  end_date: dateRange.value[1],
  channel: filters.channel || undefined,
  method: isBusiness.value ? filters.method || undefined : undefined,
  order_no: isBusiness.value && isDetail.value ? filters.orderNo || undefined : undefined,
  product_name: !isBusiness.value ? filters.productName || undefined : undefined,
  scenic_area_id: !isBusiness.value ? filters.scenicAreaId || undefined : undefined,
  page: exportAll ? 1 : page.value,
  page_size: exportAll ? 10000 : pageSize.value
})

const loadRows = async () => {
  loading.value = true
  try {
    const response = await request.get(`/reports/${activeTab.value}`, { params: queryParams() })
    rows.value = response.data.data || []
    total.value = Number(response.data.total ?? rows.value.length)
  } finally {
    loading.value = false
  }
}

const refresh = async () => {
  page.value = 1
  await loadRows()
}

const handleTabChange = async () => {
  page.value = 1
  rows.value = []
  await loadRows()
}

const handlePageSizeChange = async () => {
  page.value = 1
  await loadRows()
}

const csvCell = (value: unknown) => `"${String(value ?? '').replace(/"/g, '""')}"`
const exportCurrent = async () => {
  exporting.value = true
  try {
    let exportRows = rows.value
    if (isDetail.value) {
      const response = await request.get(`/reports/${activeTab.value}`, { params: queryParams(true) })
      exportRows = response.data.data || []
    }
    if (!exportRows.length) {
      ElMessage.warning('当前筛选条件没有可导出的数据')
      return
    }
    const fields: Record<ReportTab, Array<[string, string]>> = {
      'business-summary': [['date', '营业日期'], ['channel', '销售渠道'], ['method', '支付方式'], ['payment_count', '收款笔数'], ['refund_count', '退款笔数'], ['gross_cents', '收款金额（分）'], ['refund_cents', '退款金额（分）'], ['net_cents', '营业净额（分）']],
      'business-details': [['occurred_at', '发生时间'], ['fact_type', '业务类型'], ['order_no', '订单号'], ['transaction_no', '交易号'], ['product_names', '票种'], ['channel', '销售渠道'], ['method', '支付方式'], ['amount_cents', '金额（分）'], ['contact_name', '联系人'], ['contact_phone', '手机号'], ['reason', '备注']],
      'verification-summary': [['date', '核销日期'], ['scenic_area_name', '景区'], ['product_name', '票种'], ['seller_name', '销售方'], ['channel', '销售渠道'], ['verified_count', '核销人次'], ['income_cents', '确认收入（分）']],
      'verification-details': [['check_in_time', '核销时间'], ['scenic_area_name', '景区'], ['product_name', '票种'], ['ticket_code', '票码'], ['order_no', '订单号'], ['seller_name', '销售方'], ['channel', '销售渠道'], ['verified_count', '核销人次'], ['income_cents', '确认收入（分）'], ['visitor_name', '游客'], ['visitor_phone', '游客手机号'], ['check_point_name', '核销点']]
    }
    const columns = fields[activeTab.value]
    const csv = [columns.map(item => csvCell(item[1])).join(','), ...exportRows.map(row => columns.map(item => csvCell(row[item[0]])).join(','))].join('\r\n')
    const url = URL.createObjectURL(new Blob(['\ufeff' + csv], { type: 'text/csv;charset=utf-8' }))
    const link = document.createElement('a')
    link.href = url
    link.download = `${activeTab.value}-${dateRange.value[0]}-${dateRange.value[1]}.csv`
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
  } finally {
    exporting.value = false
  }
}

onMounted(async () => {
  const [areas] = await Promise.all([request.get('/scenic-areas'), loadRows()])
  scenicAreas.value = areas.data.data || []
})
</script>

<style scoped>
.report-page { height: 100%; min-height: 0; padding: 20px 24px; display: flex; flex-direction: column; background: #f5f7fa; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 12px; }
.page-header h2 { margin: 0; color: #1f2937; font-size: 24px; font-weight: 700; }
.page-header p { margin: 5px 0 0; color: #6b7280; font-size: 13px; }
.header-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
.report-tabs { flex: 0 0 auto; }
.filter-bar { display: flex; flex-wrap: wrap; gap: 10px; padding: 12px 0 16px; }
.filter-control { width: 160px; }
.wide-filter { width: 240px; }
.summary-strip { display: grid; grid-template-columns: repeat(4, minmax(140px, 1fr)); border: 1px solid #e5e7eb; border-radius: 6px; background: #fff; margin-bottom: 14px; }
.summary-strip > div { min-height: 76px; padding: 14px 18px; border-right: 1px solid #e5e7eb; display: flex; flex-direction: column; justify-content: center; }
.summary-strip > div:last-child { border-right: 0; }
.summary-strip span { color: #6b7280; font-size: 12px; }
.summary-strip strong { margin-top: 5px; color: #111827; font-size: 22px; font-weight: 650; }
.summary-strip .summary-note { grid-column: span 2; color: #6b7280; font-size: 13px; }
.negative { color: #c2413b !important; }
.table-section { flex: 1; min-height: 320px; padding: 1px; border: 1px solid #e5e7eb; border-radius: 6px; background: #fff; overflow: hidden; }
.pagination-bar { display: flex; justify-content: flex-end; padding-top: 14px; }
@media (max-width: 900px) {
  .report-page { padding: 16px; }
  .page-header { flex-direction: column; }
  .header-actions { width: 100%; justify-content: flex-start; }
  .summary-strip { grid-template-columns: repeat(2, minmax(130px, 1fr)); }
  .summary-strip > div:nth-child(2) { border-right: 0; }
  .filter-control, .wide-filter { width: min(100%, 220px); }
}
</style>
