<template>
  <div class="h-full flex flex-col p-6 space-y-6">
    <div class="flex justify-between items-center">
      <h2 class="text-2xl font-bold text-gray-800">经营数据报表 (Advanced Reporting)</h2>
      <el-date-picker
        v-model="dateRange"
        type="daterange"
        range-separator="至"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
        value-format="YYYY-MM-DD"
        :clearable="false"
        @change="fetchAll"
      />
    </div>

    <!-- Cards Row -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
       <!-- Sales Trend -->
       <div class="bg-white p-6 rounded-xl shadow-sm border border-gray-100 flex flex-col">
          <h3 class="text-lg font-bold text-gray-800 mb-4">销售趋势 (近30天)</h3>
          <div class="flex-1 h-64">
              <v-chart class="chart" :option="salesOption" autoresize />
          </div>
       </div>

       <!-- Channel Share -->
       <div class="bg-white p-6 rounded-xl shadow-sm border border-gray-100 flex flex-col">
          <h3 class="text-lg font-bold text-gray-800 mb-4">渠道占比 (GMV)</h3>
          <div class="flex-1 h-64">
              <v-chart class="chart" :option="channelOption" autoresize />
          </div>
       </div>
    </div>

    <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <div class="bg-white p-4 rounded-xl shadow-sm border border-gray-100">
        <div class="text-xs text-gray-500">渠道差异</div>
        <div class="text-2xl font-semibold text-amber-600 mt-1">{{ operations.channel_mismatch_count || 0 }}</div>
        <div class="text-xs text-gray-400 mt-1">差异 ¥{{ centsToYuan(operations.channel_difference_cents) }}</div>
      </div>
      <div class="bg-white p-4 rounded-xl shadow-sm border border-gray-100">
        <div class="text-xs text-gray-500">退款待办</div>
        <div class="text-2xl font-semibold text-blue-600 mt-1">{{ operations.pending_refund_count || 0 }}</div>
        <div class="text-xs text-red-500 mt-1">人工复核 {{ operations.manual_review_refund_count || 0 }}</div>
      </div>
      <div class="bg-white p-4 rounded-xl shadow-sm border border-gray-100">
        <div class="text-xs text-gray-500">库存余量</div>
        <div class="text-2xl font-semibold text-emerald-600 mt-1">{{ operations.inventory_remaining || 0 }}</div>
        <div class="text-xs text-gray-400 mt-1">已售 {{ operations.inventory_sold || 0 }} / {{ operations.inventory_capacity || 0 }}</div>
      </div>
      <div class="bg-white p-4 rounded-xl shadow-sm border border-gray-100">
        <div class="text-xs text-gray-500">供应商待收</div>
        <div class="text-2xl font-semibold text-indigo-600 mt-1">¥{{ centsToYuan(operations.supplier_receivable_cents) }}</div>
        <div class="text-xs text-gray-400 mt-1">按履约与结算事实计算</div>
      </div>
    </div>

    <div class="bg-white p-6 rounded-xl shadow-sm border border-gray-100">
      <div class="flex justify-between items-center mb-4">
        <h3 class="text-lg font-bold text-gray-800">业务日期口径</h3>
        <span class="text-xs text-gray-400">金额以整数分汇总</span>
      </div>
      <el-tabs v-model="activeFact">
        <el-tab-pane label="销售日" name="sales">
          <el-table :data="daily.sales" stripe><el-table-column prop="date" label="日期"/><el-table-column prop="order_count" label="订单数"/><el-table-column label="毛额"><template #default="{row}">¥{{ centsToYuan(row.gross_cents) }}</template></el-table-column><el-table-column label="退款"><template #default="{row}">¥{{ centsToYuan(row.refund_cents) }}</template></el-table-column><el-table-column label="净额"><template #default="{row}">¥{{ centsToYuan(row.net_cents) }}</template></el-table-column></el-table>
        </el-tab-pane>
        <el-tab-pane label="支付日" name="payments">
          <el-table :data="daily.payments" stripe><el-table-column prop="date" label="日期"/><el-table-column prop="payment_count" label="支付笔数"/><el-table-column label="支付金额"><template #default="{row}">¥{{ centsToYuan(row.paid_cents) }}</template></el-table-column><el-table-column label="已退款"><template #default="{row}">¥{{ centsToYuan(row.refund_cents) }}</template></el-table-column></el-table>
        </el-tab-pane>
        <el-tab-pane label="游玩日" name="visits">
          <el-table :data="daily.visits" stripe><el-table-column prop="date" label="日期"/><el-table-column prop="ticket_count" label="票数"/><el-table-column label="订单金额"><template #default="{row}">¥{{ centsToYuan(row.gross_cents) }}</template></el-table-column></el-table>
        </el-tab-pane>
        <el-tab-pane label="核销日" name="check_ins">
          <el-table :data="daily.check_ins" stripe><el-table-column prop="date" label="日期"/><el-table-column prop="success_count" label="成功核销"/><el-table-column prop="failure_count" label="失败记录"/></el-table>
        </el-tab-pane>
        <el-tab-pane label="结算日" name="settlements">
          <el-table :data="daily.settlements" stripe><el-table-column prop="date" label="日期"/><el-table-column prop="statement_count" label="结算单数"/><el-table-column label="应结金额"><template #default="{row}">¥{{ centsToYuan(row.net_cents) }}</template></el-table-column><el-table-column label="退款冲减"><template #default="{row}">¥{{ centsToYuan(row.refund_cents) }}</template></el-table-column></el-table>
        </el-tab-pane>
      </el-tabs>
    </div>

    <!-- Product Table -->
    <div class="bg-white p-6 rounded-xl shadow-sm border border-gray-100 flex-1 flex flex-col">
        <h3 class="text-lg font-bold text-gray-800 mb-4">热销商品排行 (Top 10)</h3>
        <el-table :data="productStats" stripe style="width: 100%">
            <el-table-column type="index" label="排名" width="80" />
            <el-table-column prop="product_name" label="商品名称" />
            <el-table-column prop="total_sold" label="销量" width="120" sortable />
            <el-table-column prop="total_amount" label="销售额" width="150" sortable>
                <template #default="{ row }">
                   ¥ {{ row.total_amount.toFixed(2) }}
                </template>
            </el-table-column>
             <el-table-column label="占比" width="200">
                <template #default="{ row }">
                    <el-progress :percentage="calcPercent(row.total_amount)" />
                </template>
            </el-table-column>
        </el-table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed, provide } from 'vue'
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { LineChart, PieChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent, TitleComponent } from 'echarts/components'
import VChart, { THEME_KEY } from 'vue-echarts'
import request from '@/utils/request'
import dayjs from 'dayjs'

use([CanvasRenderer, LineChart, PieChart, GridComponent, TooltipComponent, LegendComponent, TitleComponent])
provide(THEME_KEY, 'light')

const dateRange = ref([
    dayjs().subtract(29, 'day').format('YYYY-MM-DD'),
    dayjs().add(1, 'day').format('YYYY-MM-DD')
])

const salesStats = ref<any[]>([])
const channelStats = ref<any[]>([])
const productStats = ref<any[]>([])
const daily = ref<any>({ sales: [], payments: [], visits: [], check_ins: [], settlements: [] })
const operations = ref<any>({})
const activeFact = ref('sales')

const totalProductAmount = computed(() => {
    return productStats.value.reduce((sum, item) => sum + item.total_amount, 0) || 1
})

const calcPercent = (val: number) => {
    return Math.round((val / totalProductAmount.value) * 100)
}

const centsToYuan = (value: number | undefined) => ((Number(value || 0) / 100).toFixed(2))

const salesOption = computed(() => ({
    tooltip: { trigger: 'axis' },
    xAxis: {
        type: 'category',
        data: salesStats.value.map(i => i.date),
        boundaryGap: false
    },
    yAxis: { type: 'value' },
    series: [
        {
            name: '销售额',
            type: 'line',
            data: salesStats.value.map(i => i.total_amount),
            smooth: true,
            areaStyle: { opacity: 0.1 },
            itemStyle: { color: '#4f46e5' }
        }
    ],
    grid: { left: '3%', right: '4%', bottom: '3%', containLabel: true }
}))

const channelOption = computed(() => ({
    tooltip: { trigger: 'item' },
    legend: { bottom: '0%' },
    series: [
        {
            name: '销售来源',
            type: 'pie',
            radius: ['40%', '70%'],
            avoidLabelOverlap: false,
            itemStyle: { borderRadius: 10, borderColor: '#fff', borderWidth: 2 },
            label: { show: false, position: 'center' },
            emphasis: { label: { show: true, fontSize: 20, fontWeight: 'bold' } },
            data: channelStats.value.map(i => ({ value: i.total_amount, name: mapChannel(i.channel) }))
        }
    ]
}))

const mapChannel = (c: string) => {
    const map: any = { 'miniapp': '小程序', 'window': '窗口直销', 'ota': 'OTA分销', 'distributor': 'B2B分销' }
    return map[c] || c
}

const fetchAll = async () => {
    const [start, end] = dateRange.value
    const params = { start_date: start, end_date: end }
    
    const [res1, res2, res3, res4, res5] = await Promise.all([
      request.get('/reports/sales', { params }),
      request.get('/reports/channels', { params }),
      request.get('/reports/products', { params }),
      request.get('/reports/daily', { params }),
      request.get('/reports/operations', { params })
    ])
    salesStats.value = res1.data.data || []
    channelStats.value = res2.data.data || []
    productStats.value = res3.data.data || []
    daily.value = res4.data.data || daily.value
    operations.value = res5.data.data || {}
}

onMounted(() => {
    fetchAll()
})
</script>

<style scoped>
.chart {
  height: 100%;
  width: 100%;
}
</style>
