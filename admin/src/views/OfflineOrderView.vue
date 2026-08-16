<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <div>
        <h2 class="text-lg font-bold text-gray-900">线下/窗口订单</h2>
        <p class="text-xs text-gray-500 mt-1">查看和管理通过售票窗口产生的交易记录</p>
      </div>
      <div class="flex gap-2">
        <el-button @click="fetchData" icon="Refresh">刷新</el-button>
      </div>
    </div>

    <!-- Filter -->
    <div class="mb-4 flex gap-4">
      <el-input v-model="searchQuery" placeholder="搜索订单号/手机号..." class="w-64" prefix-icon="Search" @keyup.enter="applyFilters" />
      <el-date-picker
        v-model="dateRange"
        type="daterange"
        range-separator="至"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
        value-format="YYYY-MM-DD"
        class="w-64"
        @change="applyFilters"
      />
      <el-select v-model="filterStatus" placeholder="订单状态" class="w-32" clearable @change="applyFilters">
        <el-option label="已支付" value="paid" />
        <el-option label="已完成" value="completed" />
        <el-option label="已退款" value="refunded" />
      </el-select>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading" border>
      <el-table-column prop="order_no" label="订单号" width="180" />
      <el-table-column label="售票归属" width="190">
        <template #default="{ row }">
          <div class="font-medium">{{ saleOperatorText(row) }}</div>
          <div class="text-xs text-gray-400 mt-1">{{ saleDeviceText(row) }}</div>
        </template>
      </el-table-column>
      <el-table-column label="订单内容" min-width="200">
        <template #default="{ row }">
          <div v-for="item in row.items" :key="item.id" class="text-sm mb-1">
            <span class="font-medium">{{ item.product_name }}</span>
            <span class="text-gray-500 mx-1">×</span>
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
    <el-dialog v-model="detailVisible" title="订单详情" width="700px">
      <div v-if="currentOrder">
        <el-descriptions title="基本信息" :column="2" border>
          <el-descriptions-item label="订单号">{{ currentOrder.order_no }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag :type="getStatusType(currentOrder.status)">{{ getStatusText(currentOrder.status) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="售票员">{{ saleOperatorText(currentOrder) }}</el-descriptions-item>
          <el-descriptions-item label="出票设备">{{ saleDeviceText(currentOrder) }}</el-descriptions-item>
          <el-descriptions-item label="下单时间">{{ new Date(currentOrder.created_at).toLocaleString() }}</el-descriptions-item>
          <el-descriptions-item label="总金额">¥{{ currentOrder.total_amount }}</el-descriptions-item>
          <el-descriptions-item label="所属班次" :span="2">{{ currentOrder.sale_shift_no || '暂无班次记录' }}</el-descriptions-item>
        </el-descriptions>

        <el-divider content-position="left">票据明细</el-divider>
        <div v-for="item in currentOrder.items" :key="item.id" class="mb-4">
          <div class="font-bold mb-2">{{ item.product_name }} ({{ item.quantity }}张)</div>
          <el-table :data="item.tickets" border size="small">
            <el-table-column prop="ticket_code" label="核销码" width="150" />
            <el-table-column prop="visitor_name" label="游客姓名" />
            <el-table-column prop="status" label="状态" width="100">
              <template #default="{ row }">
                <el-tag size="small" :type="row.status === 'used' ? 'info' : 'success'">
                  {{ row.status === 'used' ? '已核销' : '未使用' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="check_in_count" label="核销次数" width="80" align="center" />
            <el-table-column label="操作" width="100" align="center">
              <template #default="{ row }">
                <!-- Offline tickets usually printed, but allow verify for testing -->
                <el-button link type="primary" size="small" @click="handleVerify(row)">手动核销</el-button>
              </template>
            </el-table-column>
          </el-table>
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

const currentUser = (() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })()

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

const verifyDialogVisible = ref(false)
const verifying = ref(false)
const checkpoints = ref<any[]>([])
const verifyForm = reactive({ code: '', check_point_id: null })

const fetchCheckPoints = async () => {
    try {
        const res = await request.get('/checkpoints', { params: { page_size: 100 } })
        checkpoints.value = res.data.data
    } catch (error) {
        console.error('Fetch CheckPoints Error', error)
    }
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
      channel: 'window' // Force window channel
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

const handleDetail = (row: any) => {
  currentOrder.value = row
  detailVisible.value = true
}

const handleRefund = async (row: any) => {
  const policyOverride = (row.items || []).some((item: any) => item.refund_type === 'no_refund')
  if (policyOverride && !currentUser.is_initial_admin) {
    ElMessage.warning('该订单包含不可退票，仅景区初始管理员可以执行例外退款')
    return
  }
  const title = policyOverride ? '初始管理员例外退款' : '退款确认'
  const message = policyOverride
    ? '该订单售出时设置为不可退。请输入本次例外退款原因，操作将保留审计记录。'
    : '请输入退款原因。退款将按原支付方式退回。'
  ElMessageBox.prompt(message, title, {
    confirmButtonText: policyOverride ? '确认例外退款' : '确认退款',
    cancelButtonText: '取消',
    inputType: 'textarea',
    inputPlaceholder: '请填写具体退款原因',
    inputValidator: value => value.trim() ? true : '退款原因不能为空',
    type: 'warning'
  }).then(async ({ value }) => {
    const ticketCodes = (row.items || []).flatMap((item: any) => (item.tickets || []).map((ticket: any) => ticket.ticket_code)).filter(Boolean)
    if (!ticketCodes.length) { ElMessage.warning('订单没有可退款的未使用票'); return }
    const response = await request.post('/payments/refunds/mixed', {
      order_no: row.order_no,
      idempotency_key: `admin-${row.order_no}-${Date.now()}`,
      amount: row.total_amount,
      ticket_codes: ticketCodes,
      reason: value.trim(),
      override_refund_policy: policyOverride
    })
    if (response.data.status === 'group_pending') ElMessage.info('退款已按原支付方式分摊，等待支付渠道确认')
    else ElMessage.success('退款已完成')
    fetchData()
  }).catch(() => undefined)
}

const applyFilters = () => {
  currentPage.value = 1
  fetchData()
}

const saleOperatorText = (row: any) => {
  if (!row.sale_operator_id) return '暂无售票员记录'
  if (row.sale_operator_name && row.sale_operator_job_number) return `${row.sale_operator_name}（${row.sale_operator_job_number}）`
  return row.sale_operator_name || row.sale_operator_job_number || `员工 #${row.sale_operator_id}`
}

const saleDeviceText = (row: any) => {
  if (!row.sale_device_id) return '暂无出票设备记录'
  if (row.sale_device_name && row.sale_device_serial) return `${row.sale_device_name}（${row.sale_device_serial}）`
  return row.sale_device_name || row.sale_device_serial || `设备 #${row.sale_device_id}`
}

const getStatusType = (status: string) => {
  const map: any = { paid: 'success', unpaid: 'warning', refunded: 'info', completed: 'success' }
  return map[status] || 'info'
}

const getStatusText = (status: string) => {
  const map: any = { paid: '已支付', unpaid: '待支付', refunded: '已退款', completed: '已完成' }
  return map[status] || '未知状态'
}

onMounted(() => {
  fetchData()
  fetchCheckPoints()
})
</script>
