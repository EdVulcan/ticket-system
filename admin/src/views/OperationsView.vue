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
          <el-table-column label="应收" width="120"><template #default="{ row }">¥{{ cents(row.expected_cents) }}</template></el-table-column>
          <el-table-column prop="status" label="状态" width="120" />
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
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, onMounted } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

const user = computed<any>(() => {
  try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} }
})
const capabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const hasCapability = (value: string) => capabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(hasCapability)
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
onMounted(loadActiveTab)
</script>
