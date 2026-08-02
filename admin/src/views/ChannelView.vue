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
      <el-table-column label="适配器类型" width="140"><template #default="{row}">{{ adapterTypeText(row.type) }}</template></el-table-column>
      <el-table-column prop="status" label="状态" width="120"><template #default="{row}"><el-tag :type="row.status === 'active' ? 'success' : row.status === 'sandbox' ? 'warning' : 'info'">{{ accountStatusText(row.status) }}</el-tag></template></el-table-column>
      <el-table-column prop="rate_limit_per_min" label="限流/分钟" width="120" />
      <el-table-column prop="permissions_json" label="权限" min-width="220" show-overflow-tooltip />
      <el-table-column label="操作" width="530" fixed="right">
        <template #default="{row}">
          <el-button link type="primary" @click="openMapping(row)">商品映射</el-button>
          <el-button link type="primary" @click="openOrders(row)">渠道订单</el-button>
          <el-button link type="primary" @click="openRequests(row)">请求日志</el-button>
          <el-button link type="primary" @click="openReconciliations(row)">账单对账</el-button>
          <el-button link type="warning" @click="toggleStatus(row)">{{ row.status === 'disabled' ? '启用' : '停用' }}</el-button>
          <el-button link type="danger" @click="rotate(row)">轮换密钥</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialog" title="新增渠道账号" width="520px">
      <el-form :model="form" label-position="top">
        <el-form-item label="渠道编码"><el-input v-model="form.code" placeholder="例如：携程正式渠道" /></el-form-item>
        <el-form-item label="适配器类型"><el-input v-model="form.type" placeholder="例如：通用渠道、携程或美团" /></el-form-item>
        <el-form-item label="初始密钥"><el-input v-model="form.secret" type="password" show-password /></el-form-item>
        <el-form-item label="接口权限配置"><el-input v-model="form.permissions_json" /></el-form-item>
        <el-form-item label="每分钟请求上限"><el-input-number v-model="form.rate_limit_per_min" :min="1" :max="100000" /></el-form-item>
        <el-form-item label="允许访问的网络地址"><el-input v-model="form.allowed_ips_json" placeholder='例如 ["203.0.113.5"]' /></el-form-item>
      </el-form>
      <template #footer><el-button @click="createDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="mappingDialog" title="商品映射" width="780px">
      <div class="flex gap-2 mb-4">
        <el-input v-model="mapping.external_code" placeholder="外部商品编码" />
        <el-select v-model="mapping.product_id" filterable placeholder="选择本商户产品" style="min-width: 260px">
          <el-option v-for="product in products" :key="product.id" :label="product.name" :value="product.id" />
        </el-select>
        <el-button type="primary" @click="addMapping">添加</el-button>
      </div>
      <el-table :data="mappings" stripe><el-table-column prop="external_code" label="外部编码"/><el-table-column label="本地产品"><template #default="{ row }">{{ productName(row.product_id) }}</template></el-table-column><el-table-column label="状态"><template #default="{ row }">{{ mappingStatusText(row.status) }}</template></el-table-column></el-table>
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
        <el-table-column prop="request_id" label="请求编号" min-width="180" show-overflow-tooltip />
        <el-table-column prop="endpoint" label="接口" min-width="210" show-overflow-tooltip />
        <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="requestStatusType(row.status)">{{ requestStatusText(row.status) }}</el-tag></template></el-table-column>
        <el-table-column prop="response_status" label="响应码" width="90" />
        <el-table-column prop="attempt_count" label="尝试" width="80" />
        <el-table-column prop="remote_ip" label="来源网络地址" width="150" />
        <el-table-column label="最后尝试" width="180"><template #default="{ row }">{{ dateTime(row.last_attempt_at || row.created_at) }}</template></el-table-column>
        <el-table-column prop="response_json" label="响应摘要" min-width="220" show-overflow-tooltip />
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }"><el-button v-if="row.status === 'failed'" link type="warning" @click="authorizeRetry(row)">授权重试</el-button></template>
        </el-table-column>
      </el-table>
      <template #footer><el-button @click="requestsDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="ordersDialog" :title="`渠道订单：${selectedAccount?.code || ''}`" width="1120px" :close-on-click-modal="false">
      <div class="mb-3 flex items-center gap-2">
        <el-input v-model="orderSearch" clearable placeholder="订单号、外部单号、姓名或手机号" style="width: 300px" @keyup.enter="loadOrders(1)" />
        <el-select v-model="orderStatus" clearable placeholder="全部状态" style="width: 150px" @change="loadOrders(1)">
          <el-option label="待支付" value="unpaid" />
          <el-option label="已支付" value="paid" />
          <el-option label="已完成" value="completed" />
          <el-option label="部分退款" value="partial_refunded" />
          <el-option label="已退款" value="refunded" />
          <el-option label="已取消" value="cancelled" />
        </el-select>
        <el-button type="primary" @click="loadOrders(1)">查询</el-button>
        <el-button :icon="Refresh" @click="loadOrders(orderPage)">刷新</el-button>
      </div>
      <el-table :data="channelOrders" v-loading="ordersLoading" stripe height="470" empty-text="暂无渠道订单">
        <el-table-column prop="external_no" label="外部单号" min-width="160" show-overflow-tooltip />
        <el-table-column prop="order_no" label="内部订单" min-width="170" show-overflow-tooltip />
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag effect="plain">{{ orderStatusText(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="游客" min-width="150"><template #default="{ row }"><div>{{ row.contact_name || '-' }}</div><div class="text-xs text-gray-500">{{ row.contact_phone || '-' }}</div></template></el-table-column>
        <el-table-column label="票况" width="125"><template #default="{ row }"><div>{{ row.ticket_count }} 张</div><div class="text-xs text-gray-500">已核销 {{ row.used_ticket_count }} / 已退 {{ row.refunded_ticket_count }}</div></template></el-table-column>
        <el-table-column label="订单金额" width="110"><template #default="{ row }">¥{{ Number(row.total_amount || 0).toFixed(2) }}</template></el-table-column>
        <el-table-column label="实收/退款" width="130"><template #default="{ row }"><div>收 ¥{{ cents(row.paid_cents) }}</div><div class="text-xs text-gray-500">退 ¥{{ cents(row.refunded_cents) }}</div></template></el-table-column>
        <el-table-column label="下单时间" width="165"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" width="80" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openOrderDetail(row)">详情</el-button></template></el-table-column>
      </el-table>
      <div class="mt-3 flex justify-end"><el-pagination v-model:current-page="orderPage" :page-size="20" :total="orderTotal" layout="prev, pager, next, total" @current-change="loadOrders" /></div>
      <template #footer><el-button @click="ordersDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="orderDetailDialog" title="渠道订单详情" width="1080px" append-to-body>
      <div v-loading="orderDetailLoading">
        <el-descriptions v-if="orderDetail" :column="4" border>
          <el-descriptions-item label="外部单号">{{ orderDetail.order.external_no || '-' }}</el-descriptions-item>
          <el-descriptions-item label="内部订单">{{ orderDetail.order.order_no }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ orderStatusText(orderDetail.order.status) }}</el-descriptions-item>
          <el-descriptions-item label="金额">¥{{ Number(orderDetail.order.total_amount || 0).toFixed(2) }}</el-descriptions-item>
          <el-descriptions-item label="联系人">{{ orderDetail.order.contact_name || '-' }}</el-descriptions-item>
          <el-descriptions-item label="手机号">{{ orderDetail.order.contact_phone || '-' }}</el-descriptions-item>
          <el-descriptions-item label="创建时间" :span="2">{{ dateTime(orderDetail.order.created_at) }}</el-descriptions-item>
        </el-descriptions>
        <el-tabs v-if="orderDetail" class="mt-4">
          <el-tab-pane label="门票">
            <el-table :data="orderTickets(orderDetail.order)" stripe max-height="380" empty-text="暂无门票">
              <el-table-column prop="ticket_code" label="票码" min-width="180" />
              <el-table-column prop="product_name" label="产品" min-width="180" />
              <el-table-column prop="visitor_name" label="游客" width="120" />
              <el-table-column prop="visitor_phone" label="手机号" width="140" />
              <el-table-column label="状态" width="100"><template #default="{ row }">{{ ticketStatusText(row.status) }}</template></el-table-column>
              <el-table-column prop="check_in_count" label="核销次数" width="100" />
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`支付与退款 (${orderDetail.payments.length}/${orderDetail.refunds.length})`">
            <el-table :data="orderDetail.payments" stripe max-height="180" empty-text="暂无支付">
              <el-table-column prop="payment_no" label="支付单" min-width="160" /><el-table-column label="方式" width="100"><template #default="{ row }">{{ paymentMethodText(row.method) }}</template></el-table-column><el-table-column label="金额" width="120"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ paymentStatusText(row.status) }}</template></el-table-column><el-table-column prop="transaction_id" label="渠道流水" min-width="160" />
            </el-table>
            <el-table :data="orderDetail.refunds" stripe max-height="180" class="mt-3" empty-text="暂无退款">
              <el-table-column prop="refund_no" label="退款单" min-width="160" /><el-table-column label="方式" width="100"><template #default="{ row }">{{ paymentMethodText(row.method) }}</template></el-table-column><el-table-column label="金额" width="120"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ refundStatusText(row.status) }}</template></el-table-column><el-table-column prop="reason" label="原因" min-width="180" />
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`核销 (${orderDetail.check_ins.length})`">
            <el-table :data="orderDetail.check_ins" stripe max-height="380" empty-text="暂无核销">
              <el-table-column prop="ticket_code" label="票码" min-width="180" /><el-table-column label="结果" width="100"><template #default="{ row }">{{ checkInResultText(row.result) }}</template></el-table-column><el-table-column label="说明" min-width="180"><template #default="{ row }">{{ localizeDisplayText(row.message, '-') }}</template></el-table-column><el-table-column label="时间" width="180"><template #default="{ row }">{{ dateTime(row.check_in_time) }}</template></el-table-column>
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`售后 (${orderDetail.after_sales.length})`">
            <el-table :data="orderDetail.after_sales" stripe max-height="380" empty-text="暂无售后">
              <el-table-column prop="request_no" label="售后单" min-width="170" /><el-table-column label="类型" width="100"><template #default="{ row }">{{ afterSaleTypeText(row.type) }}</template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ afterSaleStatusText(row.status) }}</template></el-table-column><el-table-column prop="reason" label="原因" min-width="200" /><el-table-column label="申请时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </div>
      <template #footer><el-button @click="orderDetailDialog = false">关闭</el-button></template>
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
          <el-input v-model="billText" type="textarea" :rows="9" placeholder="每行：外部单号,类型,金额(元),发生时间(可选)&#10;示例订单,收款,299.00,2026-08-01 10:30:00" />
          <div class="mt-1 text-xs text-gray-500">类型支持销售、收款、取消、退款；也兼容渠道提供的英文类型值。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="billImportDialog = false">取消</el-button><el-button type="primary" :loading="billImporting" @click="importBill">导入并核对</el-button></template>
    </el-dialog>

    <el-dialog v-model="reconciliationDetailDialog" title="渠道对账明细" width="1080px" append-to-body>
      <el-table :data="reconciliationDetail?.lines || []" v-loading="reconciliationDetailLoading" stripe height="500">
        <el-table-column prop="external_no" label="外部单号" min-width="170" />
        <el-table-column label="类型" width="90"><template #default="{ row }">{{ operationText(row.operation) }}</template></el-table-column>
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
import { localizeDisplayText } from '@/utils/localize'

const accounts = ref<any[]>([])
const mappings = ref<any[]>([])
const products = ref<any[]>([])
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
const ordersDialog = ref(false)
const ordersLoading = ref(false)
const channelOrders = ref<any[]>([])
const orderSearch = ref('')
const orderStatus = ref('')
const orderPage = ref(1)
const orderTotal = ref(0)
const orderDetailDialog = ref(false)
const orderDetailLoading = ref(false)
const orderDetail = ref<any>(null)
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
const productName = (id: number) => products.value.find((product: any) => Number(product.id) === Number(id))?.name || '已下架或不可见产品'
const openMapping = async (row: any) => {
  selectedID.value = row.id; mapping.external_code = ''; mapping.product_id = 0
  const [mappingResponse, productResponse] = await Promise.all([
    request.get('/channel-accounts/mappings', { params: { channel_account_id: row.id } }),
    request.get('/products', { params: { page: 1, page_size: 100 } }),
  ])
  mappings.value = mappingResponse.data.data || []
  products.value = productResponse.data.data || []
  mappingDialog.value = true
}
const addMapping = async () => { if (!mapping.external_code || !mapping.product_id) return; const response = await request.post('/channel-accounts/mappings', { channel_account_id: selectedID.value, external_code: mapping.external_code, product_id: mapping.product_id }); mappings.value.unshift(response.data); mapping.external_code = ''; mapping.product_id = 0 }
const dateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const accountStatusText = (status: string) => ({ active: '正式启用', sandbox: '测试中', disabled: '已停用' } as Record<string, string>)[status] || '未知状态'
const adapterTypeText = (type: string) => ({ core: '通用渠道', ctrip: '携程', meituan: '美团', zyb: '智游宝上游' } as Record<string, string>)[type] || '自定义渠道'
const mappingStatusText = (status: string) => ({ active: '已启用', disabled: '已停用' } as Record<string, string>)[status] || '未知状态'
const requestStatusText = (status: string) => ({ processing: '处理中', completed: '已完成', failed: '失败待处理', retryable: '已授权重试' } as Record<string, string>)[status] || '未知状态'
const requestStatusType = (status: string) => status === 'completed' ? 'success' : status === 'failed' ? 'danger' : status === 'retryable' ? 'warning' : 'primary'
const orderStatusText = (status: string) => ({ unpaid: '待支付', paid: '已支付', completed: '已完成', partial_refunded: '部分退款', refunded: '已退款', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const ticketStatusText = (status: string) => ({ unused: '未使用', active: '可使用', issued: '已出票', used: '已核销', refunded: '已退款', expired: '已过期', void: '已作废' } as Record<string, string>)[status] || '未知状态'
const paymentMethodText = (method: string) => ({ cash: '现金', wechat: '微信支付', alipay: '支付宝', touch: '碰一碰支付', balance: '账户余额', credit: '授信挂账' } as Record<string, string>)[method] || '其他方式'
const paymentStatusText = (status: string) => ({ pending: '等待支付', processing: '支付处理中', paid: '支付成功', succeeded: '支付成功', failed: '支付失败', cancelled: '已取消', refunded: '已退款', partial_refunded: '部分退款' } as Record<string, string>)[status] || '未知状态'
const refundStatusText = (status: string) => ({ pending: '等待退款', processing: '退款处理中', succeeded: '退款成功', completed: '退款成功', failed: '退款失败', manual_review: '人工复核' } as Record<string, string>)[status] || '未知状态'
const checkInResultText = (result: string) => ({ success: '核销成功', failed: '核销失败', rejected: '已拒绝' } as Record<string, string>)[result] || '未知结果'
const afterSaleTypeText = (type: string) => ({ refund: '退票', reschedule: '改期', exchange: '换票', void: '作废', reissue: '补打' } as Record<string, string>)[type] || '其他售后'
const afterSaleStatusText = (status: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '处理失败' } as Record<string, string>)[status] || '未知状态'
const operationText = (operation: string) => ({ sale: '销售', payment: '收款', cancel: '取消', refund: '退款' } as Record<string, string>)[operation] || '其他'
const orderTickets = (order: any) => (order?.items || []).flatMap((item: any) => (item.tickets || []).map((ticket: any) => ({ ...ticket, product_name: item.product_name })))
const loadOrders = async (page = 1) => {
  if (!selectedAccount.value) return
  orderPage.value = page
  ordersLoading.value = true
  try {
    const response = await request.get(`/channel-accounts/${selectedAccount.value.id}/orders`, { params: { search: orderSearch.value.trim(), status: orderStatus.value, page, page_size: 20 } })
    channelOrders.value = response.data.data || []
    orderTotal.value = Number(response.data.total || 0)
  } finally { ordersLoading.value = false }
}
const openOrders = async (row: any) => {
  selectedAccount.value = row
  orderSearch.value = ''
  orderStatus.value = ''
  ordersDialog.value = true
  await loadOrders(1)
}
const openOrderDetail = async (row: any) => {
  if (!selectedAccount.value) return
  orderDetail.value = null
  orderDetailDialog.value = true
  orderDetailLoading.value = true
  try { orderDetail.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/orders/${encodeURIComponent(row.order_no)}`)).data }
  finally { orderDetailLoading.value = false }
}
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
    const result = await ElMessageBox.prompt('确认故障原因已排除，并填写授权重试原因。渠道方仍需使用相同请求编号和相同正文重新发送。', '授权渠道重试', { inputType: 'textarea', inputValidator: value => value.trim() ? true : '授权原因必填' })
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
  const operation = ({ 销售: 'sale', 收款: 'payment', 取消: 'cancel', 退款: 'refund' } as Record<string, string>)[cells[1]] || cells[1]
  const amount = Number(cells[2])
  if (!cells[0] || !['sale', 'payment', 'cancel', 'refund'].includes(operation) || !Number.isFinite(amount) || amount < 0) throw new Error(`第 ${index + 1} 行格式不正确`)
  const occurred = cells[3] ? new Date(cells[3].replace(' ', 'T')) : null
  if (occurred && Number.isNaN(occurred.getTime())) throw new Error(`第 ${index + 1} 行时间格式不正确`)
  return { external_no: cells[0], operation, amount_cents: Math.round(amount * 100), currency: 'CNY', external_occurred_at: occurred?.toISOString() }
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
