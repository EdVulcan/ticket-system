<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">渠道连接</h2>
        <p class="text-sm text-gray-500 mt-1">管理独立渠道凭据、权限、商品映射和账单导入。</p>
      </div>
      <div class="flex gap-2">
        <el-button :icon="Refresh" circle title="刷新" @click="load" />
        <el-button type="primary" :icon="Plus" @click="createDialog = true">新增渠道</el-button>
      </div>
    </div>

    <el-table :data="accounts" v-loading="loading" stripe>
      <el-table-column prop="code" label="渠道编码" width="180" />
      <el-table-column prop="type" label="适配器类型" width="140" />
      <el-table-column prop="status" label="状态" width="120"><template #default="{row}"><el-tag :type="row.status === 'active' ? 'success' : row.status === 'sandbox' ? 'warning' : 'info'">{{ row.status }}</el-tag></template></el-table-column>
      <el-table-column prop="rate_limit_per_min" label="限流/分钟" width="120" />
      <el-table-column prop="permissions_json" label="权限" min-width="220" show-overflow-tooltip />
      <el-table-column label="操作" width="460" fixed="right">
        <template #default="{row}">
          <el-button link type="primary" @click="openMapping(row)">商品映射</el-button>
          <el-button link type="primary" @click="openRequests(row)">请求日志</el-button>
          <el-button link type="primary" @click="openReconciliations(row)">账单对账</el-button>
          <el-button link type="warning" @click="toggleStatus(row)">{{ row.status === 'disabled' ? '启用' : '停用' }}</el-button>
          <el-button link type="danger" @click="rotate(row)">轮换密钥</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialog" title="新增渠道账号" width="520px">
      <el-form :model="form" label-position="top">
        <el-form-item label="渠道编码"><el-input v-model="form.code" placeholder="例如 ctrip-prod" /></el-form-item>
        <el-form-item label="适配器类型"><el-input v-model="form.type" placeholder="core / ctrip / meituan" /></el-form-item>
        <el-form-item label="初始密钥"><el-input v-model="form.secret" type="password" show-password /></el-form-item>
        <el-form-item label="权限 JSON"><el-input v-model="form.permissions_json" /></el-form-item>
        <el-form-item label="每分钟请求上限"><el-input-number v-model="form.rate_limit_per_min" :min="1" :max="100000" /></el-form-item>
        <el-form-item label="允许 IP JSON"><el-input v-model="form.allowed_ips_json" placeholder='例如 ["203.0.113.5"]' /></el-form-item>
      </el-form>
      <template #footer><el-button @click="createDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="mappingDialog" title="商品映射" width="780px">
      <div class="flex gap-2 mb-4">
        <el-input v-model="mapping.external_code" placeholder="外部商品编码" />
        <el-input-number v-model="mapping.product_id" :min="1" placeholder="本租户商品 ID" />
        <el-button type="primary" @click="addMapping">添加</el-button>
      </div>
      <el-table :data="mappings" stripe><el-table-column prop="external_code" label="外部编码"/><el-table-column prop="product_id" label="本地商品 ID"/><el-table-column prop="status" label="状态"/></el-table>
    </el-dialog>

    <el-dialog v-model="secretDialog" title="新渠道密钥" width="460px"><el-alert type="warning" :closable="false" title="密钥只在本次显示，请立即交给渠道方并安全保存。"/><el-input class="mt-4" :model-value="newSecret" readonly /></el-dialog>

    <el-dialog v-model="requestsDialog" :title="`渠道请求日志：${selectedAccount?.code || ''}`" width="1060px" :close-on-click-modal="false">
      <div class="mb-3 flex items-center gap-2">
        <el-select v-model="requestStatus" clearable placeholder="全部状态" style="width: 180px" @change="loadRequests">
          <el-option label="处理中" value="processing" />
          <el-option label="已完成" value="completed" />
          <el-option label="失败待处理" value="failed" />
          <el-option label="已授权重试" value="retryable" />
        </el-select>
        <el-button :icon="Refresh" @click="loadRequests">刷新</el-button>
        <span class="text-sm text-gray-500">共 {{ requestTotal }} 条</span>
      </div>
      <el-table :data="channelRequests" v-loading="requestsLoading" stripe height="480" empty-text="暂无请求记录">
        <el-table-column prop="request_id" label="请求 ID" min-width="180" show-overflow-tooltip />
        <el-table-column prop="endpoint" label="接口" min-width="210" show-overflow-tooltip />
        <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="requestStatusType(row.status)">{{ requestStatusText(row.status) }}</el-tag></template></el-table-column>
        <el-table-column prop="response_status" label="响应码" width="90" />
        <el-table-column prop="attempt_count" label="尝试" width="80" />
        <el-table-column prop="remote_ip" label="来源 IP" width="130" />
        <el-table-column label="最后尝试" width="180"><template #default="{ row }">{{ dateTime(row.last_attempt_at || row.created_at) }}</template></el-table-column>
        <el-table-column prop="response_json" label="响应摘要" min-width="220" show-overflow-tooltip />
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }"><el-button v-if="row.status === 'failed'" link type="warning" @click="authorizeRetry(row)">授权重试</el-button></template>
        </el-table-column>
      </el-table>
      <template #footer><el-button @click="requestsDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="reconciliationsDialog" :title="`渠道账单对账：${selectedAccount?.code || ''}`" width="1000px" :close-on-click-modal="false">
      <div class="mb-4 flex justify-between">
        <div class="text-sm text-gray-500">导入渠道销售、支付、取消或退款账单，与本系统订单资金事实逐笔核对。</div>
        <el-button type="primary" :icon="Plus" @click="billImportDialog = true">导入账单</el-button>
      </div>
      <el-table :data="reconciliations" v-loading="reconciliationsLoading" stripe height="430" empty-text="暂无对账批次">
        <el-table-column prop="idempotency_key" label="批次号" min-width="180" />
        <el-table-column prop="record_count" label="记录" width="80" />
        <el-table-column prop="matched_count" label="匹配" width="80" />
        <el-table-column label="差异" width="120"><template #default="{ row }">{{ signedCents(row.difference_cents) }}</template></el-table-column>
        <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="row.status === 'completed' ? 'success' : 'warning'">{{ row.status === 'completed' ? '一致' : '待复核' }}</el-tag></template></el-table-column>
        <el-table-column label="导入时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openReconciliationDetail(row)">明细</el-button></template></el-table-column>
      </el-table>
      <template #footer><el-button @click="reconciliationsDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="billImportDialog" title="导入渠道账单" width="680px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="批次号" required><el-input v-model="billBatchKey" maxlength="120" /></el-form-item>
        <el-form-item label="账单内容" required>
          <el-input v-model="billText" type="textarea" :rows="9" placeholder="每行：外部单号,类型,金额(元),发生时间(可选)&#10;EXT-1001,payment,299.00,2026-08-01 10:30:00" />
          <div class="mt-1 text-xs text-gray-500">类型支持 sale、payment、cancel、refund；金额使用元。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="billImportDialog = false">取消</el-button><el-button type="primary" :loading="billImporting" @click="importBill">导入并核对</el-button></template>
    </el-dialog>

    <el-dialog v-model="reconciliationDetailDialog" title="渠道对账明细" width="1080px" append-to-body>
      <el-table :data="reconciliationDetail?.lines || []" v-loading="reconciliationDetailLoading" stripe height="500">
        <el-table-column prop="external_no" label="外部单号" min-width="170" />
        <el-table-column prop="operation" label="类型" width="90" />
        <el-table-column label="渠道金额" width="120"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column>
        <el-table-column prop="matched_order_no" label="内部订单" min-width="160" />
        <el-table-column prop="matched_payment_no" label="支付单" min-width="150" />
        <el-table-column prop="matched_refund_no" label="退款单" min-width="150" />
        <el-table-column label="差异" width="120"><template #default="{ row }">{{ signedCents(row.difference_cents) }}</template></el-table-column>
        <el-table-column label="结果" width="100"><template #default="{ row }"><el-tag :type="row.status === 'matched' ? 'success' : 'danger'" effect="plain">{{ row.status === 'matched' ? '一致' : row.status === 'mismatch' ? '金额差异' : '未匹配' }}</el-tag></template></el-table-column>
      </el-table>
      <template #footer><el-button @click="reconciliationDetailDialog = false">关闭</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const accounts = ref<any[]>([])
const mappings = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const createDialog = ref(false)
const mappingDialog = ref(false)
const secretDialog = ref(false)
const selectedID = ref(0)
const newSecret = ref('')
const requestsDialog = ref(false)
const requestsLoading = ref(false)
const selectedAccount = ref<any>(null)
const channelRequests = ref<any[]>([])
const requestStatus = ref('')
const requestTotal = ref(0)
const reconciliationsDialog = ref(false)
const reconciliationsLoading = ref(false)
const reconciliations = ref<any[]>([])
const billImportDialog = ref(false)
const billImporting = ref(false)
const billBatchKey = ref('')
const billText = ref('')
const reconciliationDetailDialog = ref(false)
const reconciliationDetailLoading = ref(false)
const reconciliationDetail = ref<any>(null)
const form = reactive({ code: '', type: 'core', secret: '', permissions_json: '["products:read","inventory:reserve","orders:create","orders:query","orders:cancel"]', rate_limit_per_min: 600, allowed_ips_json: '' })
const mapping = reactive({ external_code: '', product_id: 0 })

const load = async () => { loading.value = true; try { accounts.value = (await request.get('/channel-accounts')).data.data || [] } finally { loading.value = false } }
const create = async () => { saving.value = true; try { const response = await request.post('/channel-accounts', { ...form }); accounts.value.unshift(response.data); createDialog.value = false; ElMessage.success('渠道已创建'); form.code = ''; form.secret = '' } finally { saving.value = false } }
const toggleStatus = async (row: any) => { const status = row.status === 'disabled' ? 'active' : 'disabled'; await request.patch(`/channel-accounts/${row.id}/status`, { status }); row.status = status; ElMessage.success('状态已更新') }
const rotate = async (row: any) => { await ElMessageBox.confirm('轮换后旧密钥立即失效，确认继续？', '确认轮换', { type: 'warning' }); const response = await request.post(`/channel-accounts/${row.id}/rotate-secret`); newSecret.value = response.data.secret; secretDialog.value = true }
const openMapping = async (row: any) => { selectedID.value = row.id; mapping.external_code = ''; mapping.product_id = 0; mappings.value = (await request.get('/channel-accounts/mappings', { params: { channel_account_id: row.id } })).data.data || []; mappingDialog.value = true }
const addMapping = async () => { if (!mapping.external_code || !mapping.product_id) return; const response = await request.post('/channel-accounts/mappings', { channel_account_id: selectedID.value, external_code: mapping.external_code, product_id: mapping.product_id }); mappings.value.unshift(response.data); mapping.external_code = ''; mapping.product_id = 0 }
const dateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const requestStatusText = (status: string) => ({ processing: '处理中', completed: '已完成', failed: '失败待处理', retryable: '已授权重试' } as Record<string, string>)[status] || status
const requestStatusType = (status: string) => status === 'completed' ? 'success' : status === 'failed' ? 'danger' : status === 'retryable' ? 'warning' : 'primary'
const loadRequests = async () => {
  if (!selectedAccount.value) return
  requestsLoading.value = true
  try {
    const response = await request.get(`/channel-accounts/${selectedAccount.value.id}/requests`, { params: { status: requestStatus.value, page: 1, page_size: 100 } })
    channelRequests.value = response.data.data || []
    requestTotal.value = Number(response.data.total || 0)
  } finally { requestsLoading.value = false }
}
const openRequests = async (row: any) => { selectedAccount.value = row; requestStatus.value = ''; requestsDialog.value = true; await loadRequests() }
const authorizeRetry = async (row: any) => {
  if (!selectedAccount.value) return
  try {
    const result = await ElMessageBox.prompt('确认故障原因已排除，并填写授权重试原因。渠道方仍需使用相同请求 ID 和相同正文重新发送。', '授权渠道重试', { inputType: 'textarea', inputValidator: value => value.trim() ? true : '授权原因必填' })
    await request.post(`/channel-accounts/${selectedAccount.value.id}/requests/${row.id}/authorize-retry`, { reason: result.value.trim() })
    ElMessage.success('已授权，等待渠道方重发原请求')
    await loadRequests()
  } catch (action: any) {
    if (action !== 'cancel' && action !== 'close') ElMessage.error(action.response?.data?.error || '授权重试失败')
  }
}
const loadReconciliations = async () => {
  if (!selectedAccount.value) return
  reconciliationsLoading.value = true
  try { reconciliations.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/reconciliations`, { params: { page: 1, page_size: 100 } })).data.data || [] }
  finally { reconciliationsLoading.value = false }
}
const openReconciliations = async (row: any) => { selectedAccount.value = row; reconciliationsDialog.value = true; await loadReconciliations() }
const parseBill = () => billText.value.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map((line, index) => {
  const cells = line.split(/[\t,，]/).map(value => value.trim())
  if (index === 0 && /外部单号|external/i.test(cells[0] || '')) return null
  const amount = Number(cells[2])
  if (!cells[0] || !['sale', 'payment', 'cancel', 'refund'].includes(cells[1]) || !Number.isFinite(amount) || amount < 0) throw new Error(`第 ${index + 1} 行格式不正确`)
  const occurred = cells[3] ? new Date(cells[3].replace(' ', 'T')) : null
  if (occurred && Number.isNaN(occurred.getTime())) throw new Error(`第 ${index + 1} 行时间格式不正确`)
  return { external_no: cells[0], operation: cells[1], amount_cents: Math.round(amount * 100), currency: 'CNY', external_occurred_at: occurred?.toISOString() }
}).filter(Boolean)
const importBill = async () => {
  if (!selectedAccount.value || !billBatchKey.value.trim() || !billText.value.trim()) { ElMessage.warning('批次号和账单内容必填'); return }
  billImporting.value = true
  try {
    const records = parseBill()
    await request.post(`/channel-accounts/${selectedAccount.value.id}/bills/import`, { idempotency_key: billBatchKey.value.trim(), records })
    billImportDialog.value = false
    billBatchKey.value = ''
    billText.value = ''
    ElMessage.success('账单已导入并完成核对')
    await loadReconciliations()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || e.message || '账单导入失败') }
  finally { billImporting.value = false }
}
const openReconciliationDetail = async (row: any) => {
  if (!selectedAccount.value) return
  reconciliationDetailDialog.value = true
  reconciliationDetailLoading.value = true
  try { reconciliationDetail.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/reconciliations/${row.id}`)).data }
  finally { reconciliationDetailLoading.value = false }
}
onMounted(load)
</script>
