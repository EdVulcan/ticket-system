<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <div>
        <h2 class="text-lg font-bold text-gray-900">订单管理</h2>
        <p class="text-xs text-gray-500 mt-1">查看线上销售订单、处理退款及手动核销</p>
      </div>
      <div class="flex gap-2">
        <el-button @click="fetchData" icon="Refresh">刷新</el-button>
      </div>
    </div>

    <!-- Filter -->
    <div class="mb-4 flex gap-4">
      <el-input v-model="searchQuery" placeholder="搜索订单号/手机号..." class="w-64" prefix-icon="Search" @keyup.enter="fetchData" />
      <el-date-picker
        v-model="dateRange"
        type="daterange"
        range-separator="至"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
        value-format="YYYY-MM-DD"
        class="w-64"
        @change="fetchData"
      />
      <el-select v-model="filterStatus" placeholder="订单状态" class="w-32" clearable @change="fetchData">
        <el-option label="已支付" value="paid" />
        <el-option label="已完成" value="completed" />
        <el-option label="已退款" value="refunded" />
      </el-select>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading" border>
      <el-table-column prop="order_no" label="订单号" width="180" />
      <el-table-column label="联系人" width="150">
        <template #default="{ row }">
          <div>{{ row.contact_name }}</div>
          <div class="text-xs text-gray-400">{{ row.contact_phone }}</div>
        </template>
      </el-table-column>
      <el-table-column label="订单内容" min-width="200">
        <template #default="{ row }">
          <div v-for="item in row.items" :key="item.id" class="text-sm mb-1">
            <div><el-tag v-if="item.bundle_name" size="small" effect="plain" class="mr-2">{{ item.bundle_name }}</el-tag><span class="font-medium">{{ item.product_name }}</span></div>
            <span class="text-gray-500 mx-1">x</span>
            <span>{{ item.quantity }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="total_amount" label="金额" width="100">
        <template #default="{ row }">
          <span class="font-bold text-orange-500">¥{{ row.total_amount }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100" align="center">
        <template #default="{ row }">
          <el-tag :type="getStatusType(row.status)">{{ getStatusText(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="下单时间" width="180">
        <template #default="{ row }">
          {{ new Date(row.created_at).toLocaleString() }}
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right" align="center">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleDetail(row)">详情</el-button>
          <el-button link type="danger" size="small" v-if="row.status === 'paid'" @click="handleRefund(row)">退款</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Pagination -->
    <div class="flex justify-end mt-4">
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="fetchData"
      />
    </div>

    <!-- Detail Dialog -->
    <el-dialog v-model="detailVisible" title="订单详情" width="980px">
      <div v-if="currentOrder" v-loading="detailLoading">
        <el-descriptions title="基本信息" :column="2" border>
          <el-descriptions-item label="订单号">{{ currentOrder.order_no }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="getStatusType(currentOrder.status)">{{ getStatusText(currentOrder.status) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="联系人">{{ currentOrder.contact_name }}</el-descriptions-item>
          <el-descriptions-item label="手机号">{{ currentOrder.contact_phone }}</el-descriptions-item>
          <el-descriptions-item label="下单时间">{{ new Date(currentOrder.created_at).toLocaleString() }}</el-descriptions-item>
          <el-descriptions-item label="总金额">¥{{ currentOrder.total_amount }}</el-descriptions-item>
        </el-descriptions>

        <el-divider content-position="left">供应履约责任</el-divider>
        <el-empty v-if="!detailLoading && responsibilities.length === 0" description="暂无履约信息" :image-size="72" />
        <div v-for="fulfillment in responsibilities" :key="fulfillment.id" class="responsibility-section">
          <div class="responsibility-heading">
            <div>
              <div class="font-semibold text-gray-900">{{ fulfillment.supplier_name }}</div>
              <div class="text-xs text-gray-500 mt-1">{{ fulfillment.scenic_area_name }} · {{ fulfillment.fulfillment_no }}</div>
            </div>
            <div class="flex gap-2">
              <el-tag size="small" effect="plain">{{ fulfillmentStatusText(fulfillment.status) }}</el-tag>
              <el-tag size="small" :type="fulfillment.statement_status === 'paid' ? 'success' : 'info'">
                {{ settlementStatusText(fulfillment.statement_status || fulfillment.settlement_status) }}
              </el-tag>
            </div>
          </div>

          <div class="responsibility-summary">
            <div><span>票数</span><strong>{{ fulfillment.ticket_count }}</strong></div>
            <div><span>已核销</span><strong>{{ fulfillment.used_count }}</strong></div>
            <div><span>已退款</span><strong>{{ fulfillment.refunded_count }}</strong></div>
            <div><span>履约总额</span><strong>¥{{ centsToYuan(fulfillment.gross_cents) }}</strong></div>
            <div><span>退款冲减</span><strong>¥{{ centsToYuan(fulfillment.refund_cents) }}</strong></div>
            <div><span>分销佣金</span><strong>¥{{ centsToYuan(fulfillment.commission_cents) }}</strong></div>
            <div><span>应结净额</span><strong>¥{{ centsToYuan(fulfillment.net_cents) }}</strong></div>
          </div>

          <div v-for="item in fulfillment.items" :key="item.id" class="mt-4">
            <div class="font-medium text-sm mb-2"><span v-if="item.bundle_name">{{ item.bundle_name }} · </span>{{ item.product_name }}（{{ item.quantity }}张）</div>
            <el-table :data="item.tickets" border size="small" empty-text="暂无票据">
              <el-table-column prop="ticket_code" label="核销码" min-width="170" />
              <el-table-column prop="visitor_name" label="游客姓名" min-width="110" />
              <el-table-column prop="status" label="状态" width="100">
                <template #default="{ row }">
                  <el-tag size="small" :type="ticketStatusType(row.status)">{{ ticketStatusText(row.status) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column prop="check_in_count" label="核销次数" width="90" align="center" />
              <el-table-column label="操作" width="100" align="center">
                <template #default="{ row }">
                  <el-button v-if="fulfillment.can_verify && row.status === 'unused'" link type="primary" size="small" @click="handleVerify(row)">手动核销</el-button>
                  <span v-else class="text-xs text-gray-400">不可核销</span>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </div>
    </el-dialog>

    <!-- Verify Dialog -->
    <el-dialog v-model="verifyDialogVisible" title="手动核销" width="400px">
      <el-form :model="verifyForm" label-width="80px">
        <el-form-item label="核销码">
          <el-input v-model="verifyForm.code" disabled />
        </el-form-item>
        <el-form-item label="检票点">
          <el-select v-model="verifyForm.check_point_id" placeholder="选择检票点" class="w-full">
            <el-option v-for="cp in checkpoints" :key="cp.id" :label="cp.name" :value="cp.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="verifyDialogVisible = false">取消</el-button>
          <el-button type="primary" @click="submitVerify" :loading="verifying">确认核销</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const loading = ref(false)
const tableData = ref([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(10)
const searchQuery = ref('')
const filterStatus = ref('')
const dateRange = ref<[string, string] | null>(null)

const detailVisible = ref(false)
const currentOrder = ref<any>(null)
const detailLoading = ref(false)
const responsibilities = ref<any[]>([])

// Verify Logic
const verifyDialogVisible = ref(false)
const verifying = ref(false)
const checkpoints = ref<any[]>([])
const verifyForm = reactive({ code: '', check_point_id: null })

const fetchCheckPoints = async () => {
  try {
    const res = await request.get('/checkpoints', { params: { page_size: 100 } })
    checkpoints.value = res.data.data
  } catch (e) { console.error(e) }
}

const handleVerify = (row: any) => {
  verifyForm.code = row.ticket_code
  verifyForm.check_point_id = null
  verifyDialogVisible.value = true
}

const submitVerify = async () => {
  if (!verifyForm.check_point_id) {
    ElMessage.warning('请选择检票点')
    return
  }
  verifying.value = true
  try {
    await request.post('/tickets/verify', {
      code: verifyForm.code,
      check_point_id: verifyForm.check_point_id
    })
    ElMessage.success('核销成功')
    verifyDialogVisible.value = false
    // Refresh detail? Need to reload order
    // For simplicity, just close detail and refresh list
    detailVisible.value = false
    fetchData()
  } catch (error) {
    ElMessage.error('核销失败: ' + (error as any).response?.data?.error || '未知错误')
  } finally {
    verifying.value = false
  }
}

const fetchData = async () => {
  loading.value = true
  try {
    const params: any = {
      page: currentPage.value,
      page_size: pageSize.value,
      channel: 'online'
    }
    if (filterStatus.value) params.status = filterStatus.value
    if (searchQuery.value) params.search = searchQuery.value
    
    if (dateRange.value && dateRange.value.length === 2) {
      params.start_date = dateRange.value[0]
      params.end_date = dateRange.value[1]
    }

    const res = await request.get('/orders', { params })
    tableData.value = res.data.data
    total.value = res.data.total
  } catch (error) {
    ElMessage.error('获取订单失败')
  } finally {
    loading.value = false
  }
}

const handleDetail = async (row: any) => {
  currentOrder.value = row
  responsibilities.value = []
  detailVisible.value = true
  detailLoading.value = true
  try {
    const res = await request.get(`/orders/${encodeURIComponent(row.order_no)}`)
    currentOrder.value = res.data.order
    responsibilities.value = res.data.fulfillments || []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '订单详情加载失败')
  } finally {
    detailLoading.value = false
  }
}

const handleRefund = async (row: any) => {
  ElMessageBox.confirm('确认全额退款吗？此操作不可逆。', '退款确认', {
    confirmButtonText: '确定退款',
    cancelButtonText: '取消',
    type: 'warning'
  }).then(async () => {
    const idempotencyKey = `admin-${row.order_no}-${Date.now()}`
    const ticketCodes = (row.items || []).flatMap((item: any) => (item.tickets || []).map((ticket: any) => ticket.ticket_code)).filter(Boolean)
    if (!ticketCodes.length) { ElMessage.warning('订单没有可退款的未使用票'); return }
    const response = await request.post('/payments/refunds/mixed', { order_no: row.order_no, idempotency_key: idempotencyKey, amount: row.total_amount, ticket_codes: ticketCodes, reason: '管理端全额退款' })
    if (response.data.status === 'group_pending') ElMessage.info('退款已按原支付方式分摊，等待支付渠道确认')
    else ElMessage.success('退款已完成')
    fetchData()
  }).catch(() => undefined)
}

const getStatusType = (status: string) => {
  const map: any = { paid: 'success', unpaid: 'warning', refunded: 'info', completed: 'success' }
  return map[status] || 'info'
}

const getStatusText = (status: string) => {
  const map: any = { paid: '已支付', unpaid: '待支付', cancelled: '已取消', partial_refunded: '部分退款', refunded: '已退款', completed: '已完成' }
  return map[status] || status
}

const centsToYuan = (value: number) => ((value || 0) / 100).toFixed(2)
const fulfillmentStatusText = (status: string) => ({ reserved: '已预占', paid: '待履约', fulfilled: '已履约', cancelled: '已取消' } as Record<string, string>)[status] || status || '-'
const settlementStatusText = (status: string) => ({ open: '待结算', draft: '待供应商确认', supplier_confirmed: '待分销商确认', confirmed: '待付款', disputed: '有争议', paid: '已结清' } as Record<string, string>)[status] || status || '待结算'
const ticketStatusText = (status: string) => ({ unused: '未使用', used: '已核销', refunded: '已退款', expired: '已过期', void: '已作废' } as Record<string, string>)[status] || status
const ticketStatusType = (status: string) => ({ unused: 'success', used: 'info', refunded: 'warning', expired: 'info', void: 'danger' } as Record<string, string>)[status] || 'info'

onMounted(() => {
  fetchData()
  fetchCheckPoints()
})
</script>

<style scoped>
.responsibility-section {
  padding: 18px 0;
  border-bottom: 1px solid #e5e7eb;
}

.responsibility-section:last-child {
  border-bottom: 0;
}

.responsibility-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.responsibility-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(96px, 1fr));
  gap: 12px;
  margin-top: 14px;
  padding: 12px 0;
  border-top: 1px solid #f1f5f9;
  border-bottom: 1px solid #f1f5f9;
}

.responsibility-summary div {
  min-width: 0;
}

.responsibility-summary span,
.responsibility-summary strong {
  display: block;
}

.responsibility-summary span {
  color: #6b7280;
  font-size: 12px;
}

.responsibility-summary strong {
  margin-top: 4px;
  color: #111827;
  font-size: 14px;
}

</style>
