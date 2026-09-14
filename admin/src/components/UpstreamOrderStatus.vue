<template>
  <section v-if="rows.length || error" class="upstream-status">
    <div class="heading"><strong>智游宝供票</strong><el-button link type="primary" :loading="busy" @click="load(true)">查询供应商最新状态</el-button></div>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <el-descriptions v-for="row in rows" :key="row.external_product_code" :column="2" border>
      <el-descriptions-item label="商品">{{ row.product_name }}</el-descriptions-item>
      <el-descriptions-item label="供应商订单">{{ row.provider_order_code || '等待出票' }}</el-descriptions-item>
      <el-descriptions-item label="出票进度">{{ text(row.issue_status) }}</el-descriptions-item>
      <el-descriptions-item label="供应商状态">{{ text(row.provider_status) }}</el-descriptions-item>
      <el-descriptions-item label="上游取消">{{ text(row.cancel_status) }}</el-descriptions-item>
      <el-descriptions-item label="最近查询">{{ date(row.last_synced_at) }}</el-descriptions-item>
      <el-descriptions-item v-if="row.sync_pending" label="同步进度">等待同步{{ row.next_sync_at ? ' · 最早 ' + date(row.next_sync_at) : '' }}</el-descriptions-item>
      <el-descriptions-item label="本系统首次核销">{{ date(row.local_first_used_at) }}</el-descriptions-item>
      <el-descriptions-item label="供应商首次核销">{{ date(row.provider_first_used_at) }}</el-descriptions-item>
      <el-descriptions-item label="最早使用时间">{{ date(row.first_used_at) }}</el-descriptions-item>
      <el-descriptions-item v-if="row.last_error" label="处理提示">{{ row.last_error }}</el-descriptions-item>
      <el-descriptions-item v-if="row.requires_confirmation && canConfirm" label="退款处理"><el-button type="warning" size="small" :disabled="busy" @click="confirm(row.refund_id)">管理员确认继续退款</el-button></el-descriptions-item>
      <el-descriptions-item v-if="row.can_recover_funding && canConfirm" label="款项恢复"><el-button type="warning" size="small" :disabled="busy" @click="recoverFunding(row.refund_id)">恢复款项退款</el-button></el-descriptions-item>
      <el-descriptions-item v-if="row.can_recover_issuance && canRecoverIssuance" label="出票恢复"><el-button type="primary" size="small" :disabled="busy" @click="recoverIssuance">查单并恢复出票</el-button></el-descriptions-item>
    </el-descriptions>
    <p>两边独立核销。供应商状态仅供订单管理参考，不限制本系统剩余检票点的使用。</p>
  </section>
</template>
<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import request from '@/utils/request'
import { ElMessageBox, ElMessage } from 'element-plus'
import { readStoredUser, isScenicHistorySupplier } from '@/utils/tenantAccess'
import { hasPermission } from '@/utils/permissions'
const props = defineProps<{ orderNo: string; refreshKey?: number }>()
const rows = ref<any[]>([])
const busy = ref(false)
const error = ref('')
let generation = 0
let pollTimer: ReturnType<typeof setTimeout> | undefined
let pollCount = 0
function stopPolling() { if (pollTimer) clearTimeout(pollTimer); pollTimer = undefined }
const user = readStoredUser()
const canConfirm = user.is_initial_admin === true && isScenicHistorySupplier(user) && hasPermission(user, 'refunds.write')
const canRecoverIssuance = user.is_initial_admin === true && isScenicHistorySupplier(user) && hasPermission(user, 'after_sales.write')
async function recoverFunding(refundID: number) {
  try {
    const { value } = await ElMessageBox.prompt('原款项退款失败，票券将继续锁定。系统会恢复原资金任务；小红书明确失败的售后会以新申请重提，并保留原失败记录。请填写原因。', '恢复款项退款', { inputValidator: value => !!value?.trim() || '请填写原因', confirmButtonText: '确认恢复退款', type: 'warning' })
    busy.value = true
    await request.post('/payments/refunds/upstream-recover', { refund_id: refundID, reason: value.trim() })
    ElMessage.info('退款任务已恢复，等待渠道确认到账')
    await load()
  } catch { /* cancelled or shared error */ } finally { busy.value = false }
}
async function recoverIssuance() {
  const orderNo = props.orderNo
  try {
    const { value } = await ElMessageBox.prompt('请先核对供应商后台。系统会查询原订单：已成单只恢复取码；明确未成单时，才使用原订单号重新发码。请填写核对说明。', '确认恢复出票', { inputValidator: value => !!value?.trim() || '请填写核对说明', confirmButtonText: '确认查单并恢复', type: 'warning' })
    busy.value = true
    await request.post(`/orders/${encodeURIComponent(orderNo)}/upstream/recover-issuance`, { reason: value.trim(), confirmed_no_order: true })
    ElMessage.info('已提交恢复任务，请稍后查询出票进度')
    await load()
  } catch { /* cancelled or shared error */ } finally { busy.value = false }
}
async function confirm(refundID: number) {
  try {
    const { value } = await ElMessageBox.prompt('此操作继续本系统退款，不保证供应商旧票码已失效。请填写处理原因。', '确认特殊退款', { inputValidator: value => !!value?.trim() || '请填写原因', confirmButtonText: '确认继续退款', type: 'warning' })
    busy.value = true
    await request.post('/payments/refunds/upstream-confirm', { refund_id: refundID, reason: value.trim() })
    ElMessage.info('已确认，原退款任务继续处理')
    await load()
  } catch { /* cancellation or shared request error */ } finally { busy.value = false }
}
const date = (value?: string) => value ? new Date(value).toLocaleString() : '暂无记录'
const text = (value: string) => ({ pending: '等待出票', ready: '出票成功', local_ready: '本系统出票', un_check: '未使用', checked: '已使用', checking: '部分使用', refunded: '已退票', partial_refunded: '部分退票', unknown: '状态待核实', submitted: '取消处理中', succeeded: '已取消', failed: '取消失败', override: '管理员特殊退款' } as Record<string,string>)[value] || value || '暂无'
async function load(refresh = false) {
  stopPolling()
  if (refresh) pollCount = 0
  const current = ++generation
  const orderNo = props.orderNo
  if (!orderNo) { rows.value = []; return }
  busy.value = true; error.value = ''
  try {
    const url = `/orders/${encodeURIComponent(orderNo)}/upstream`
    if (refresh) {
      try { await request.post(`${url}/refresh`, {}, { skipErrorToast: true } as any) }
      catch (cause: any) { if (current === generation) error.value = cause.response?.data?.error || '供应商查询失败，以下仍显示本地处理进度' }
    }
    const result = await request.get(url, { skipErrorToast: true } as any)
    if (current === generation) {
      rows.value = result.data.data || []
      // Poll our saved projection only; this never enqueues another request.
      if (rows.value.some(row => row.sync_pending) && pollCount++ < 20) {
        pollTimer = setTimeout(() => { void load() }, 3000)
      }
    }
  } catch (cause: any) {
    if (current === generation) error.value = cause.response?.data?.error || '供应商状态读取失败'
  } finally { if (current === generation) busy.value = false }
}
watch(() => [props.orderNo, props.refreshKey], () => { rows.value = []; pollCount = 0; void load() }, { immediate: true })
onBeforeUnmount(() => { generation++; stopPolling() })
</script>
<style scoped>
.upstream-status { margin: 20px 0; }
.heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }
p { color: #64748b; font-size: 12px; line-height: 1.6; }
</style>
