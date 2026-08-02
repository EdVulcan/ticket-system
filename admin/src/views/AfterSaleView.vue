<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">售后工作台</h2>
        <p class="text-sm text-gray-500 mt-1">统一处理退票、改期、换票、作废和补打，所有状态都保留操作记录。</p>
      </div>
      <div class="flex gap-2">
        <el-button :icon="Refresh" circle title="刷新" @click="load" />
        <el-button type="primary" :icon="Plus" @click="openCreate">新建售后</el-button>
      </div>
    </div>

    <div class="flex flex-wrap gap-3">
      <el-input v-model="orderNo" clearable placeholder="按订单号查询" class="w-64" @keyup.enter="applyFilters" @clear="applyFilters" />
      <el-select v-model="status" clearable placeholder="全部状态" class="w-40" @change="load">
        <el-option label="待审核" value="pending" />
        <el-option label="已批准" value="approved" />
        <el-option label="处理中" value="processing" />
        <el-option label="已完成" value="completed" />
        <el-option label="已拒绝" value="rejected" />
        <el-option label="失败" value="failed" />
      </el-select>
    </div>

    <el-table :data="rows" v-loading="loading" stripe border>
      <el-table-column prop="request_no" label="申请号" width="190" />
      <el-table-column prop="order_no" label="订单号" width="190" />
      <el-table-column prop="type" label="类型" width="90">
        <template #default="{ row }">{{ typeText(row.type) }}</template>
      </el-table-column>
      <el-table-column prop="ticket_codes" label="票码" min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ parseCodes(row.ticket_codes) }}</template>
      </el-table-column>
      <el-table-column prop="amount_cents" label="金额" width="100">
        <template #default="{ row }">{{ row.amount_cents ? `¥${(row.amount_cents / 100).toFixed(2)}` : '-' }}</template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="110">
        <template #default="{ row }"><el-tag :type="statusType(row.status)">{{ statusText(row.status) }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="created_at" label="申请时间" width="180">
        <template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button v-if="row.status === 'pending' && canApprove" link type="primary" @click="openApprove(row)">批准</el-button>
          <el-button v-if="row.status === 'pending' && canApprove" link type="danger" @click="reject(row)">拒绝</el-button>
          <el-button v-if="row.status === 'approved'" link type="primary" @click="execute(row)">执行</el-button>
          <el-button v-if="row.status === 'processing' && row.difference_cents > 0 && row.difference_status !== 'settled'" link type="warning" @click="openDifferencePayment(row)">收取差价</el-button>
          <el-button link @click="showDetail(row)">详情</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="flex justify-end"><el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="total" layout="total, prev, pager, next" @current-change="load" /></div>

    <el-dialog v-model="createVisible" title="新建售后申请" width="620px">
      <el-form :model="form" label-position="top">
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="订单号"><el-input v-model="form.order_no" /></el-form-item>
          <el-form-item label="类型"><el-select v-model="form.type" class="w-full"><el-option v-for="item in types" :key="item.value" :label="item.label" :value="item.value" /></el-select></el-form-item>
          <el-form-item class="col-span-2" :label="form.type === 'void' ? '票码（整单作废可留空）' : '票码（可填写一个或多个游客票码）'"><el-input v-model="form.ticket_codes" type="textarea" :rows="2" /></el-form-item>
          <template v-if="form.type === 'refund'">
            <el-form-item label="退款金额（分）"><el-input-number v-model="form.amount_cents" :min="0" class="w-full" /></el-form-item>
            <el-form-item label="退款方式"><el-select v-model="form.payment_method" class="w-full"><el-option label="按原支付方式自动分摊" value="auto" /><el-option label="现金" value="cash" /><el-option label="微信" value="wechat" /><el-option label="支付宝" value="alipay" /></el-select></el-form-item>
          </template>
          <template v-if="form.type === 'reschedule' || form.type === 'exchange'">
            <el-form-item label="目标日期"><el-date-picker v-model="form.target_date" value-format="YYYY-MM-DD" class="w-full" /></el-form-item>
            <el-form-item label="目标时段"><el-input v-model="form.target_slot" placeholder="不填则保留原时段" /></el-form-item>
          </template>
          <el-form-item v-if="form.type === 'exchange'" label="目标产品"><el-select v-model="form.target_product_id" filterable class="w-full" placeholder="选择换票后的产品"><el-option v-for="product in availableProducts" :key="product.id" :label="product.name" :value="product.id" /></el-select></el-form-item>
          <template v-if="form.type === 'reissue'">
            <el-form-item label="售票终端"><el-select v-model="form.device_id" filterable class="w-full" placeholder="选择售票终端" @change="applyFormDevice"><el-option v-for="device in posDevices" :key="device.id" :label="deviceLabel(device)" :value="device.id" /></el-select></el-form-item>
            <el-form-item label="当前班次"><el-select v-model="form.shift_id" class="w-full" placeholder="选择当班班次"><el-option v-for="shift in shiftsForDevice(form.device_id)" :key="shift.id" :label="shiftLabel(shift)" :value="shift.id" /></el-select></el-form-item>
          </template>
          <el-form-item class="col-span-2" label="原因"><el-input v-model="form.reason" type="textarea" :rows="2" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="createVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">提交申请</el-button></template>
    </el-dialog>

    <el-dialog v-model="approveVisible" title="批准售后" width="520px">
      <el-form :model="approveForm" label-position="top">
        <el-form-item label="批准说明"><el-input v-model="approveForm.reason" type="textarea" :rows="2" /></el-form-item>
        <el-form-item v-if="approveTarget?.type === 'exchange'" label="允许供应结算价例外">
          <el-switch v-model="approveForm.settlement_exception" />
        </el-form-item>
        <el-form-item v-if="approveForm.settlement_exception" label="例外原因" required>
          <el-input v-model="approveForm.settlement_exception_reason" type="textarea" :rows="2" placeholder="说明与供应商的确认情况" />
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="approveVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="confirmApprove">确认批准</el-button></template>
    </el-dialog>

    <el-dialog v-model="differenceVisible" title="收取换票差价" width="520px">
      <el-alert v-if="differenceTarget" type="warning" :closable="false" class="mb-4" :title="`应收 ¥${((differenceTarget.difference_cents || 0) / 100).toFixed(2)}`" />
      <el-form :model="differenceForm" label-position="top">
        <el-form-item label="收款方式"><el-select v-model="differenceForm.method" class="w-full"><el-option label="现金" value="cash" /><el-option label="微信" value="wechat" /><el-option label="支付宝" value="alipay" /></el-select></el-form-item>
        <template v-if="differenceForm.method === 'cash'">
          <el-form-item label="顾客实付（分）"><el-input-number v-model="differenceForm.cash_tendered_cents" :min="differenceTarget?.difference_cents || 0" class="w-full" /></el-form-item>
          <div class="text-sm text-gray-600 mb-4">找零：¥{{ differenceChange }}</div>
        </template>
        <template v-else>
          <el-form-item label="扫码方式"><el-segmented v-model="differenceForm.pay_type" :options="[{ label: '扫顾客付款码', value: 'bscanc' }, { label: '顾客扫码', value: 'cscanb' }]" /></el-form-item>
          <el-form-item v-if="differenceForm.pay_type === 'bscanc'" label="顾客付款码"><el-input v-model="differenceForm.auth_code" autocomplete="off" /></el-form-item>
        </template>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="售票终端"><el-select v-model="differenceForm.device_id" filterable class="w-full" placeholder="选择售票终端" @change="applyDifferenceDevice"><el-option v-for="device in posDevices" :key="device.id" :label="deviceLabel(device)" :value="device.id" /></el-select></el-form-item>
          <el-form-item label="当前班次"><el-select v-model="differenceForm.shift_id" class="w-full" placeholder="选择当班班次"><el-option v-for="shift in shiftsForDevice(differenceForm.device_id)" :key="shift.id" :label="shiftLabel(shift)" :value="shift.id" /></el-select></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="differenceVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="collectDifference">确认收款</el-button></template>
    </el-dialog>

    <el-dialog v-model="detailVisible" title="售后详情" width="560px">
      <el-descriptions v-if="selected" :column="2" border>
        <el-descriptions-item label="申请号">{{ selected.request_no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ statusText(selected.status) }}</el-descriptions-item>
        <el-descriptions-item label="订单号">{{ selected.order_no }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ typeText(selected.type) }}</el-descriptions-item>
        <el-descriptions-item label="票码" :span="2">{{ parseCodes(selected.ticket_codes) || '整单' }}</el-descriptions-item>
        <el-descriptions-item label="原因" :span="2">{{ selected.reason || '-' }}</el-descriptions-item>
        <el-descriptions-item v-if="selected.difference_cents" label="换票价差">{{ selected.difference_cents > 0 ? '应补' : '应退' }} ¥{{ (Math.abs(selected.difference_cents) / 100).toFixed(2) }}</el-descriptions-item>
        <el-descriptions-item v-if="selected.difference_cents" label="价差状态">{{ differenceStatusText(selected.difference_status) }}</el-descriptions-item>
        <el-descriptions-item v-if="selected.settlement_exception_approved" label="结算例外" :span="2">{{ selected.settlement_exception_reason }}</el-descriptions-item>
        <el-descriptions-item label="错误" :span="2">{{ selected.error_message ? localizeDisplayText(selected.error_message) : '-' }}</el-descriptions-item>
      </el-descriptions>
      <div v-if="refundDetail" class="mt-4">
        <div class="flex items-center justify-between mb-2"><strong>退款资金进度</strong><el-tag :type="refundStatusType(refundDetail.root.status)">{{ refundStatusText(refundDetail.root.status) }}</el-tag></div>
        <el-table :data="refundDetail.allocations" size="small" border>
          <el-table-column label="原支付方式" width="120"><template #default="{ row }">{{ paymentMethodText(row.method) }}</template></el-table-column>
          <el-table-column label="退款金额" width="120"><template #default="{ row }">¥{{ ((row.amount_cents || 0) / 100).toFixed(2) }}</template></el-table-column>
          <el-table-column label="状态"><template #default="{ row }"><el-tag :type="refundStatusType(allocationStatus(row))">{{ refundStatusText(allocationStatus(row)) }}</el-tag></template></el-table-column>
        </el-table>
      </div>
      <div v-if="selected?.events?.length" class="mt-5">
        <div class="font-medium text-gray-900 mb-3">处理记录</div>
        <el-timeline>
          <el-timeline-item v-for="event in selected.events" :key="event.id" :timestamp="formatTime(event.created_at)" placement="top">
            <div class="flex items-center gap-2">
              <span class="font-medium">{{ eventActionText(event.action) }}</span>
              <el-tag v-if="event.to_status" size="small" :type="statusType(event.to_status)">{{ statusText(event.to_status) }}</el-tag>
            </div>
            <div class="text-xs text-gray-500 mt-1">操作人 #{{ event.actor_id || '-' }}<span v-if="event.reason"> · {{ event.reason }}</span></div>
          </el-timeline-item>
        </el-timeline>
      </div>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { localizeDisplayText } from '@/utils/localize'

const route = useRoute()

const user = ref<any>({})
try { user.value = JSON.parse(localStorage.getItem('user') || '{}') } catch (_) { /* invalid session */ }
const canApprove = computed(() => user.value.role === 'admin' || user.value.role === 'super_admin')
const rows = ref<any[]>([]); const loading = ref(false); const saving = ref(false); const total = ref(0); const page = ref(1); const pageSize = ref(20); const status = ref(''); const orderNo = ref(String(route.query.order_no || ''))
const createVisible = ref(false); const detailVisible = ref(false); const approveVisible = ref(false); const differenceVisible = ref(false); const selected = ref<any>(null)
const approveTarget = ref<any>(null); const differenceTarget = ref<any>(null)
const refundDetail = ref<any>(null)
const availableProducts = ref<any[]>([])
const devices = ref<any[]>([])
const openShifts = ref<any[]>([])
const posDevices = computed(() => devices.value.filter((device: any) => device.type === 'pos' && device.status === 'online'))
const shiftsForDevice = (deviceID: number) => openShifts.value.filter((shift: any) => shift.status === 'open' && Number(shift.device_id) === Number(deviceID))
const deviceLabel = (device: any) => `${device.name}${device.serial_number ? `（${device.serial_number}）` : ''}`
const shiftLabel = (shift: any) => `${shift.shift_no || `班次 ${shift.id}`} · ${shift.operator_name || '当前收银员'}`
const types = [{ value: 'refund', label: '退票' }, { value: 'reschedule', label: '改期' }, { value: 'exchange', label: '换票' }, { value: 'void', label: '作废' }, { value: 'reissue', label: '补打' }]
const emptyForm = () => ({ order_no: '', type: 'refund', ticket_codes: '', amount_cents: 0, payment_method: 'auto', target_date: '', target_slot: '', target_product_id: 0, device_id: 0, shift_id: 0, reason: '' })
const form = reactive(emptyForm())
const approveForm = reactive({ reason: '', settlement_exception: false, settlement_exception_reason: '' })
const differenceForm = reactive({ method: 'cash', pay_type: 'cscanb', auth_code: '', shift_id: 0, device_id: 0, cash_tendered_cents: 0 })
const differenceChange = computed(() => ((Math.max(0, differenceForm.cash_tendered_cents - (differenceTarget.value?.difference_cents || 0))) / 100).toFixed(2))

const load = async () => { loading.value = true; try { const res = await request.get('/after-sales', { params: { page: page.value, page_size: pageSize.value, status: status.value, order_no: orderNo.value.trim() } }); rows.value = res.data.data || []; total.value = res.data.total || 0 } finally { loading.value = false } }
const applyFilters = () => { page.value = 1; load() }
const loadOperationOptions = async () => {
	const [productResult, deviceResult, shiftResult] = await Promise.allSettled([
		request.get('/products', { params: { page: 1, page_size: 100 } }),
		request.get('/devices', { params: { page: 1, page_size: 100 } }),
		request.get('/operations/shifts', { params: { page: 1, page_size: 100 } }),
	])
	availableProducts.value = productResult.status === 'fulfilled' ? (productResult.value.data.data || []).filter((product: any) => product.status === 'online') : []
	devices.value = deviceResult.status === 'fulfilled' ? deviceResult.value.data.data || [] : []
	openShifts.value = shiftResult.status === 'fulfilled' ? shiftResult.value.data.data || [] : []
}
const applyFormDevice = () => { form.shift_id = shiftsForDevice(form.device_id)[0]?.id || 0 }
const applyDifferenceDevice = () => { differenceForm.shift_id = shiftsForDevice(differenceForm.device_id)[0]?.id || 0 }
const openCreate = async () => {
  Object.assign(form, { ...emptyForm(), order_no: orderNo.value.trim() })
  createVisible.value = true
  try { await loadOperationOptions() } catch (e: any) { ElMessage.error(e.response?.data?.error || '售后可选项加载失败') }
}
const create = async () => { if (!form.order_no.trim() || !form.reason.trim()) { ElMessage.warning('订单号和原因必填'); return }; saving.value = true; try { await request.post('/after-sales', { ...form, ticket_codes: form.ticket_codes.split(/[,，\s]+/).map(value => value.trim()).filter(Boolean), idempotency_key: `admin-${Date.now()}-${Math.random().toString(36).slice(2)}` }); ElMessage.success('售后申请已提交'); createVisible.value = false; await load() } finally { saving.value = false } }
const openApprove = (row: any) => { approveTarget.value = row; Object.assign(approveForm, { reason: '', settlement_exception: false, settlement_exception_reason: '' }); approveVisible.value = true }
const confirmApprove = async () => { if (approveForm.settlement_exception && !approveForm.settlement_exception_reason.trim()) { ElMessage.warning('结算价例外必须填写原因'); return }; saving.value = true; try { await request.post(`/after-sales/${approveTarget.value.id}/approve`, approveForm); ElMessage.success('已批准'); approveVisible.value = false; await load() } finally { saving.value = false } }
const reject = async (row: any) => { const reason = await ElMessageBox.prompt('请输入拒绝原因', '拒绝售后', { inputValidator: value => value.trim() ? true : '拒绝原因必填' }); await request.post(`/after-sales/${row.id}/reject`, { reason: reason.value }); ElMessage.success('已拒绝'); await load() }
const execute = async (row: any) => { await ElMessageBox.confirm(`确认执行 ${typeText(row.type)} ${row.request_no}？`, '执行售后', { type: 'warning' }); const result = (await request.post(`/after-sales/${row.id}/execute`)).data; ElMessage.success(result.difference_status === 'payment_required' ? '请继续收取换票差价' : '已进入执行流程'); await load() }
const openDifferencePayment = async (row: any) => {
  differenceTarget.value = row
  Object.assign(differenceForm, { method: 'cash', pay_type: 'cscanb', auth_code: '', shift_id: row.shift_id || 0, device_id: row.device_id || 0, cash_tendered_cents: row.difference_cents || 0 })
  differenceVisible.value = true
  try {
    await loadOperationOptions()
    if (!differenceForm.device_id) differenceForm.device_id = posDevices.value[0]?.id || 0
    if (!differenceForm.shift_id) applyDifferenceDevice()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || '收银上下文加载失败') }
}
const collectDifference = async () => { if (!differenceForm.device_id || !differenceForm.shift_id) { ElMessage.warning('请选择当前售票终端和班次'); return }; if (differenceForm.pay_type === 'bscanc' && differenceForm.method !== 'cash' && !differenceForm.auth_code.trim()) { ElMessage.warning('请扫描顾客付款码'); return }; saving.value = true; try { const response = await request.post(`/after-sales/${differenceTarget.value.id}/difference-payment`, { ...differenceForm, idempotency_key: `difference-${differenceTarget.value.request_no}-${Date.now()}` }); differenceVisible.value = false; ElMessage.success(response.data.status === 'paid' ? '差价收取完成，换票已生效' : '支付请求已提交，等待渠道确认'); await load() } finally { saving.value = false } }
const showDetail = async (row: any) => {
  selected.value = (await request.get(`/after-sales/${row.id}`)).data
  refundDetail.value = null
  if (selected.value.refund_id) {
    try { refundDetail.value = (await request.get(`/payments/refunds/${selected.value.refund_id}`)).data }
    catch (_) { ElMessage.warning('退款资金进度暂时无法加载') }
  }
  detailVisible.value = true
}
const allocationStatus = (row: any) => refundDetail.value?.tasks?.find((task: any) => task.refund_id === row.id)?.status || row.status
const parseCodes = (value: string) => { try { return (JSON.parse(value || '[]') || []).join('，') } catch (_) { return value || '' } }
const typeText = (value: string) => types.find(item => item.value === value)?.label || '其他售后'
const statusText = (value: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '失败' } as any)[value] || '未知状态'
const statusType = (value: string) => ({ completed: 'success', rejected: 'info', failed: 'danger', processing: 'warning', approved: 'warning' } as any)[value] || 'info'
const paymentMethodText = (value: string) => ({ cash: '现金', wechat: '微信', alipay: '支付宝', mixed: '混合支付' } as any)[value] || '其他方式'
const refundStatusText = (value: string) => ({ group_pending: '等待全部退款', group_succeeded: '退款完成', pending: '等待渠道', processing: '处理中', submitted: '渠道处理中', succeeded: '已退款', failed: '失败', manual_review: '待人工复核' } as any)[value] || '未知状态'
const refundStatusType = (value: string) => ({ group_succeeded: 'success', succeeded: 'success', failed: 'danger', manual_review: 'danger', group_pending: 'warning', pending: 'warning', processing: 'warning', submitted: 'warning' } as any)[value] || 'info'
const differenceStatusText = (value: string) => ({ payment_required: '待收取差价', payment_pending: '等待支付确认', refund_pending: '等待退款完成', settled: '已结清' } as any)[value] || '无需处理'
const eventActionText = (value: string) => ({ created: '提交申请', approved: '审核批准', rejected: '审核拒绝', settlement_exception: '批准结算例外', execution_started: '开始执行', execution_completed: '执行完成', execution_failed: '执行失败', difference_payment_required: '等待补收差价', difference_payment_started: '发起差价收款', difference_payment_completed: '差价收款完成', difference_payment_failed: '差价收款失败', difference_refund_pending: '差价退款处理中', difference_refund_completed: '差价退款完成', print_queued: '打印任务已排队', proxy_print_queued: '主管代补打', print_started: '开始打印', print_succeeded: '打印完成', print_failed: '打印失败' } as any)[value] || '其他操作'
const formatTime = (value: string) => value ? new Date(value).toLocaleString() : '-'
watch(() => route.query.order_no, value => { orderNo.value = String(value || ''); applyFilters() })
onMounted(load)
</script>
