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

    <div class="flex gap-3">
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
          <el-button v-if="row.status === 'pending' && canApprove" link type="primary" @click="approve(row)">批准</el-button>
          <el-button v-if="row.status === 'pending' && canApprove" link type="danger" @click="reject(row)">拒绝</el-button>
          <el-button v-if="row.status === 'approved'" link type="primary" @click="execute(row)">执行</el-button>
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
          <el-form-item v-if="form.type === 'exchange'" label="目标商品 ID"><el-input-number v-model="form.target_product_id" :min="1" class="w-full" /></el-form-item>
          <template v-if="form.type === 'reissue'">
            <el-form-item label="POS 设备 ID"><el-input-number v-model="form.device_id" :min="1" class="w-full" /></el-form-item>
            <el-form-item label="POS 班次 ID"><el-input-number v-model="form.shift_id" :min="1" class="w-full" /></el-form-item>
          </template>
          <el-form-item class="col-span-2" label="原因"><el-input v-model="form.reason" type="textarea" :rows="2" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="createVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">提交申请</el-button></template>
    </el-dialog>

    <el-dialog v-model="detailVisible" title="售后详情" width="560px">
      <el-descriptions v-if="selected" :column="2" border>
        <el-descriptions-item label="申请号">{{ selected.request_no }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ statusText(selected.status) }}</el-descriptions-item>
        <el-descriptions-item label="订单号">{{ selected.order_no }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ typeText(selected.type) }}</el-descriptions-item>
        <el-descriptions-item label="票码" :span="2">{{ parseCodes(selected.ticket_codes) || '整单' }}</el-descriptions-item>
        <el-descriptions-item label="原因" :span="2">{{ selected.reason || '-' }}</el-descriptions-item>
        <el-descriptions-item label="错误" :span="2">{{ selected.error_message || '-' }}</el-descriptions-item>
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
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

const user = ref<any>({})
try { user.value = JSON.parse(localStorage.getItem('user') || '{}') } catch (_) { /* invalid session */ }
const canApprove = computed(() => user.value.role === 'admin' || user.value.role === 'super_admin')
const rows = ref<any[]>([]); const loading = ref(false); const saving = ref(false); const total = ref(0); const page = ref(1); const pageSize = ref(20); const status = ref('')
const createVisible = ref(false); const detailVisible = ref(false); const selected = ref<any>(null)
const refundDetail = ref<any>(null)
const types = [{ value: 'refund', label: '退票' }, { value: 'reschedule', label: '改期' }, { value: 'exchange', label: '换票' }, { value: 'void', label: '作废' }, { value: 'reissue', label: '补打' }]
const emptyForm = () => ({ order_no: '', type: 'refund', ticket_codes: '', amount_cents: 0, payment_method: 'auto', target_date: '', target_slot: '', target_product_id: 0, device_id: 0, shift_id: 0, reason: '' })
const form = reactive(emptyForm())

const load = async () => { loading.value = true; try { const res = await request.get('/after-sales', { params: { page: page.value, page_size: pageSize.value, status: status.value } }); rows.value = res.data.data || []; total.value = res.data.total || 0 } finally { loading.value = false } }
const openCreate = () => { Object.assign(form, emptyForm()); createVisible.value = true }
const create = async () => { if (!form.order_no.trim() || !form.reason.trim()) { ElMessage.warning('订单号和原因必填'); return }; saving.value = true; try { await request.post('/after-sales', { ...form, ticket_codes: form.ticket_codes.split(/[,，\s]+/).map(value => value.trim()).filter(Boolean), idempotency_key: `admin-${Date.now()}-${Math.random().toString(36).slice(2)}` }); ElMessage.success('售后申请已提交'); createVisible.value = false; await load() } finally { saving.value = false } }
const approve = async (row: any) => { const reason = await ElMessageBox.prompt('请输入批准说明', '批准售后', { inputPlaceholder: '可选' }); await request.post(`/after-sales/${row.id}/approve`, { reason: reason.value }); ElMessage.success('已批准'); await load() }
const reject = async (row: any) => { const reason = await ElMessageBox.prompt('请输入拒绝原因', '拒绝售后', { inputValidator: value => value.trim() ? true : '拒绝原因必填' }); await request.post(`/after-sales/${row.id}/reject`, { reason: reason.value }); ElMessage.success('已拒绝'); await load() }
const execute = async (row: any) => { await ElMessageBox.confirm(`确认执行 ${typeText(row.type)} ${row.request_no}？`, '执行售后', { type: 'warning' }); await request.post(`/after-sales/${row.id}/execute`); ElMessage.success('已进入执行流程'); await load() }
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
const typeText = (value: string) => types.find(item => item.value === value)?.label || value
const statusText = (value: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '失败' } as any)[value] || value
const statusType = (value: string) => ({ completed: 'success', rejected: 'info', failed: 'danger', processing: 'warning', approved: 'warning' } as any)[value] || 'info'
const paymentMethodText = (value: string) => ({ cash: '现金', wechat: '微信', alipay: '支付宝', mixed: '混合支付' } as any)[value] || value
const refundStatusText = (value: string) => ({ group_pending: '等待全部退款', group_succeeded: '退款完成', pending: '等待渠道', processing: '处理中', submitted: '渠道处理中', succeeded: '已退款', failed: '失败', manual_review: '待人工复核' } as any)[value] || value
const refundStatusType = (value: string) => ({ group_succeeded: 'success', succeeded: 'success', failed: 'danger', manual_review: 'danger', group_pending: 'warning', pending: 'warning', processing: 'warning', submitted: 'warning' } as any)[value] || 'info'
const eventActionText = (value: string) => ({ created: '提交申请', approved: '审核批准', rejected: '审核拒绝', execution_started: '开始执行', execution_completed: '执行完成', execution_failed: '执行失败', print_queued: '打印任务已排队', proxy_print_queued: '主管代补打', print_started: '开始打印', print_succeeded: '打印完成', print_failed: '打印失败' } as any)[value] || value
const formatTime = (value: string) => value ? new Date(value).toLocaleString() : '-'
onMounted(load)
</script>
