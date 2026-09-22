<template>
  <section class="stats-panel" data-testid="commerce-stats-panel">
    <div class="section-toolbar">
      <div>
        <h2>经营统计</h2>
        <p class="muted">按付款与退款事实查看当前业态的经营概况，金额以服务端统计为准。</p>
      </div>
      <div class="stats-actions">
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          :clearable="false"
          :disabled="loading || !capabilityActive"
          @change="loadStats"
        />
        <el-button :icon="Refresh" :loading="loading" :disabled="!capabilityActive" @click="loadStats">刷新</el-button>
      </div>
    </div>

    <el-alert
      v-if="!capabilityActive"
      type="warning"
      :closable="false"
      title="该商业能力已暂停，暂不加载经营统计。"
    />
    <el-alert v-else-if="errorMessage" type="error" :closable="false" :title="errorMessage">
      <template #default>
        <el-button link type="primary" @click="loadStats">重试</el-button>
      </template>
    </el-alert>

    <template v-if="capabilityActive && !errorMessage">
      <div v-loading="loading" class="stats-content">
        <div class="summary-grid">
          <article v-for="metric in summaryMetrics" :key="metric.key" class="summary-item">
            <span>{{ metric.label }}</span>
            <strong>{{ metric.value }}</strong>
          </article>
        </div>

        <div class="stats-grid">
          <section class="stats-section">
            <div class="section-heading">
              <div>
                <h3>履约方式分布</h3>
                <p class="muted">按订单履约方式汇总</p>
              </div>
            </div>
            <div v-if="fulfillmentRows.length" class="fulfillment-list">
              <div v-for="row in fulfillmentRows" :key="row.key" class="fulfillment-row">
                <span>{{ row.label }}</span>
                <strong>{{ row.countLabel }}</strong>
              </div>
            </div>
            <el-empty v-else description="暂无履约分布数据" :image-size="64" />
          </section>

          <section class="stats-section">
            <div class="section-heading">
              <div>
                <h3>分享助力</h3>
                <p class="muted">统计服务端记录的成功助力数</p>
              </div>
            </div>
            <div class="assist-count">{{ assistSuccessLabel }}</div>
          </section>
        </div>

        <section class="stats-section top-products-section">
          <div class="section-heading">
            <div>
              <h3>热销商品</h3>
              <p class="muted">按服务端统计的销量与成交额排序</p>
            </div>
          </div>
          <div class="table-scroll">
            <el-table v-if="topProductRows.length" :data="topProductRows" border stripe>
              <el-table-column prop="productName" label="商品" min-width="220" />
              <el-table-column label="销量" width="120" align="right">
                <template #default="{ row }">{{ row.quantityLabel }}</template>
              </el-table-column>
              <el-table-column label="成交额" width="160" align="right">
                <template #default="{ row }">{{ row.salesLabel }}</template>
              </el-table-column>
            </el-table>
            <el-empty v-else description="暂无热销商品数据" :image-size="64" />
          </div>
        </section>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

type BusinessType = 'restaurant' | 'retail'

const props = withDefaults(defineProps<{
  businessType: BusinessType
  capabilityActive?: boolean
}>(), {
  capabilityActive: true,
})

type Summary = {
  orderCount?: number | null
  grossSalesCents?: number | null
  refundedAmountCents?: number | null
  netSalesCents?: number | null
}

type FulfillmentRow = { key: string; label: string; countLabel: string; count: number | null }
type TopProductRow = { productName: string; quantityLabel: string; salesLabel: string }

const loading = ref(false)
const errorMessage = ref('')
const dateRange = ref<[Date, Date]>(defaultDateRange())
const summary = ref<Summary>({})
const fulfillmentRows = ref<FulfillmentRow[]>([])
const topProductRows = ref<TopProductRow[]>([])
const assistSuccessCount = ref<number | null>(null)
let requestSequence = 0

function defaultDateRange(): [Date, Date] {
  const end = new Date()
  const start = new Date(end)
  start.setDate(end.getDate() - 6)
  start.setHours(0, 0, 0, 0)
  end.setHours(23, 59, 59, 999)
  return [start, end]
}

function dateParam(value: Date) {
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, '0')
  const day = String(value.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function numberOrNull(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

function money(value: number | null | undefined) {
  return value === null || value === undefined ? '-' : `¥${(value / 100).toFixed(2)}`
}

function countLabel(value: number | null | undefined) {
  return value === null || value === undefined ? '-' : String(value)
}

function firstValue(source: any, keys: string[]) {
  for (const key of keys) {
    if (source?.[key] !== undefined && source?.[key] !== null) return source[key]
  }
  return undefined
}

const summaryMetrics = computed(() => [
  { key: 'orders', label: '订单数', value: countLabel(summary.value.orderCount) },
  { key: 'gross', label: '成交额', value: money(summary.value.grossSalesCents) },
  { key: 'refunds', label: '退款金额', value: money(summary.value.refundedAmountCents) },
  { key: 'net', label: '净收入', value: money(summary.value.netSalesCents) },
])

const assistSuccessLabel = computed(() => countLabel(assistSuccessCount.value))

function statusLabel(value: unknown) {
  const labels: Record<string, string> = {
    pickup: '到店自取',
    self_pickup: '到店自取',
    delivery: '配送',
    local_delivery: '本地配送',
    shipping: '快递发货',
    express: '快递发货',
  }
  const text = String(value || '')
  return labels[text] || text || '未标注'
}

function clearStats() {
  summary.value = {}
  fulfillmentRows.value = []
  topProductRows.value = []
  assistSuccessCount.value = null
}

function normalizeResponse(payload: any) {
  const data = payload?.data || payload || {}
  const rawSummary = data.summary || data
  summary.value = {
    orderCount: numberOrNull(firstValue(rawSummary, ['order_count', 'orders_count', 'orders', 'total_orders'])),
    grossSalesCents: numberOrNull(firstValue(rawSummary, ['gross_sales_cents', 'gross_amount_cents', 'sales_cents', 'total_amount_cents'])),
    refundedAmountCents: numberOrNull(firstValue(rawSummary, ['refunded_amount_cents', 'refund_amount_cents'])),
    netSalesCents: numberOrNull(firstValue(rawSummary, ['net_sales_cents', 'net_amount_cents'])),
  }

  const distribution = firstValue(data, ['fulfillment_distribution', 'fulfillment_breakdown', 'fulfillment_methods'])
  fulfillmentRows.value = Array.isArray(distribution)
    ? distribution.map((item: any, index: number) => {
        const count = numberOrNull(firstValue(item, ['count', 'order_count', 'orders']))
        const label = statusLabel(firstValue(item, ['label', 'name', 'method', 'fulfillment_type', 'status']))
        return { key: `${label}-${index}`, label, count, countLabel: countLabel(count) }
      })
    : []

  const products = firstValue(data, ['top_products', 'best_sellers'])
  topProductRows.value = Array.isArray(products)
    ? products.map((item: any) => {
        const quantity = numberOrNull(firstValue(item, ['quantity', 'sold_quantity', 'units']))
        const sales = numberOrNull(firstValue(item, ['sales_cents', 'amount_cents', 'gross_sales_cents']))
        return {
          productName: String(firstValue(item, ['product_name', 'name', 'title']) || '未命名商品'),
          quantityLabel: countLabel(quantity),
          salesLabel: money(sales),
        }
      })
    : []

  assistSuccessCount.value = numberOrNull(firstValue(data, ['assist_success_count', 'successful_assists']))
}

async function loadStats() {
  if (!props.capabilityActive) return
  const range = dateRange.value
  if (!range?.[0] || !range?.[1]) return
  const sequence = ++requestSequence
  loading.value = true
  errorMessage.value = ''
  try {
    const response = await request.get('/commerce/stats', {
      params: {
        business_type: props.businessType,
        start_date: dateParam(range[0]),
        end_date: dateParam(range[1]),
      },
      skipErrorToast: true,
    } as any)
    if (sequence !== requestSequence) return
    normalizeResponse(response.data)
  } catch (error: any) {
    if (sequence !== requestSequence) return
    clearStats()
    errorMessage.value = Number(error?.response?.status || 0) === 403
      ? '当前账号暂无经营统计权限，或该商业能力已暂停。'
      : String(error?.response?.data?.error || error?.response?.data?.message || '经营统计暂时无法加载，请稍后重试。')
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

watch(() => [props.businessType, props.capabilityActive], () => {
  dateRange.value = defaultDateRange()
  clearStats()
  errorMessage.value = ''
  if (props.capabilityActive) void loadStats()
})

onMounted(() => { if (props.capabilityActive) void loadStats() })
</script>

<style scoped>
.stats-panel { display: flex; flex-direction: column; gap: 16px; }
.section-toolbar, .section-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.section-toolbar h2, .section-heading h3 { margin: 0; }
.section-toolbar h2 { font-size: 19px; }
.section-heading h3 { font-size: 16px; }
.section-toolbar p, .section-heading p { margin: 4px 0 0; }
.muted { color: var(--ui-text-secondary); font-size: 12px; }
.stats-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; justify-content: flex-end; }
.stats-content { min-width: 0; display: flex; flex-direction: column; gap: 16px; }
.summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
.summary-item { min-width: 0; padding: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); background: var(--ui-surface-soft); }
.summary-item span { display: block; color: var(--ui-text-secondary); font-size: 13px; }
.summary-item strong { display: block; margin-top: 8px; color: var(--ui-text); font-size: 22px; line-height: 1.2; }
.stats-grid { display: grid; grid-template-columns: minmax(0, 1.4fr) minmax(260px, 1fr); gap: 16px; }
.stats-section { min-width: 0; padding: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); background: var(--ui-surface); }
.fulfillment-list { display: flex; flex-direction: column; gap: 10px; margin-top: 18px; }
.fulfillment-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 12px; background: var(--ui-surface-soft); border-radius: var(--ui-radius); }
.assist-count { display: flex; align-items: center; min-height: 98px; margin-top: 14px; color: var(--ui-primary); font-size: 32px; font-weight: 700; }
.top-products-section { overflow: hidden; }
.table-scroll { min-width: 0; margin-top: 14px; overflow-x: auto; }
.table-scroll :deep(.el-table) { min-width: 560px; }
@media (max-width: 900px) {
  .summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .stats-grid { grid-template-columns: 1fr; }
}
@media (max-width: 620px) {
  .section-toolbar, .section-heading { flex-direction: column; align-items: stretch; }
  .stats-actions { justify-content: flex-start; }
  .stats-actions :deep(.el-date-editor) { width: 100%; }
  .summary-grid { grid-template-columns: 1fr; }
}
</style>
