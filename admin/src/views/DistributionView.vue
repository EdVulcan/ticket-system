<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100 flex justify-between items-center">
      <div>
        <h2 class="text-lg font-bold text-gray-900">分销中心 (B2B)</h2>
        <p class="text-xs text-gray-500 mt-1">连接产业上下游，拓展业务边界</p>
      </div>
      <div>
         <el-button v-if="canDistribute && activeTab === 'suppliers'" type="primary" size="large" @click="dialogVisible = true">
            <el-icon class="mr-2"><Connection /></el-icon> 寻找供应商
         </el-button>
      </div>
    </div>

    <!-- Main Content -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
      <el-tabs v-model="activeTab" @tab-change="handleTabChange">
        
        <!-- Tab 1: My Suppliers -->
        <el-tab-pane v-if="canDistribute" label="我的供应商 (我是分销商)" name="suppliers">
            <div class="flex items-center justify-between mb-4 mt-2">
                <h3 class="font-bold text-gray-700">已合作的供应商</h3>
                <el-button link type="primary" @click="fetchSuppliers"><el-icon><Refresh /></el-icon></el-button>
            </div>
            <el-table :data="suppliers" style="width: 100%" v-loading="loadingSuppliers">
                <el-table-column prop="supplier_name" label="供应商名称" min-width="180">
                <template #default="{ row }">
                    <div class="font-medium">{{ row.supplier_name }}</div>
                    <div class="text-xs text-gray-400">系统编号: {{ row.supplier_code }}</div>
                </template>
                </el-table-column>
                <el-table-column prop="status" label="合作状态" width="120">
                <template #default="{ row }">
                    <el-tag :type="getStatusType(row.status)">{{ getStatusText(row.status) }}</el-tag>
                </template>
                </el-table-column>
                <el-table-column prop="agent_level" label="分销等级" width="120">
                <template #default="{ row }">
                    <el-tag effect="plain">{{ getLevelText(row.agent_level) }}</el-tag>
                </template>
                </el-table-column>
                <el-table-column prop="balance" label="预付余额" width="150" align="right">
                <template #default="{ row }">
                    <span class="font-mono font-bold text-orange-500">¥{{ row.balance || '0.00' }}</span>
                </template>
                </el-table-column>
                <el-table-column label="操作" width="200" fixed="right" align="center">
                <template #default="{ row }">
                    <el-button type="primary" size="small" @click="handleSourcing(row)">采购/上架</el-button>
                    <el-button link type="warning" size="small">充值</el-button>
                </template>
                </el-table-column>
            </el-table>
        </el-tab-pane>

        <!-- Tab 2: My Agents -->
        <el-tab-pane v-if="canSupply" label="我的分销商 (我是供应商)" name="agents">
            <div class="flex items-center justify-between mb-4 mt-2">
                <h3 class="font-bold text-gray-700">代理申请列表</h3>
                <el-button link type="primary" @click="fetchAgents"><el-icon><Refresh /></el-icon></el-button>
            </div>
             <el-table :data="agents" style="width: 100%" v-loading="loadingAgents">
                <el-table-column prop="agent_name" label="分销商名称" min-width="180">
                <template #default="{ row }">
                    <div class="font-medium">{{ row.agent_name }}</div>
                    <div class="text-xs text-gray-400">联系人: {{ row.agent_contact || '暂无' }}</div>
                </template>
                </el-table-column>
                <el-table-column prop="agent_code" label="系统编号" width="150">
                    <template #default="{ row }">
                       <span class="font-mono">{{ row.agent_code }}</span>
                    </template>
                </el-table-column>
                <el-table-column prop="created_at" label="申请时间" width="160" />
                <el-table-column prop="status" label="状态" width="120">
                <template #default="{ row }">
                    <el-tag :type="getStatusType(row.status)">{{ getStatusText(row.status) }}</el-tag>
                </template>
                </el-table-column>
                <el-table-column label="操作" width="200" fixed="right" align="center">
                <template #default="{ row }">
                    <div v-if="row.status === 'pending'">
                        <el-button type="success" size="small" @click="handleAudit(row, 'active')">通过</el-button>
                        <el-button type="danger" size="small" @click="handleAudit(row, 'rejected')">拒绝</el-button>
                    </div>
                    <div v-else>
                         <el-button type="primary" size="small" @click="handleOffers(row)">Offers</el-button>
                         <el-button type="warning" size="small" @click="handleRecharge(row)">充值</el-button>
                    </div>
                </template>
                </el-table-column>
            </el-table>
        </el-tab-pane>

        <el-tab-pane v-if="canSupply" label="供应履约" name="fulfillments">
            <div class="flex items-center justify-between mb-4 mt-2">
                <div class="flex gap-2 items-center">
                    <el-select v-model="fulfillmentStatus" clearable placeholder="全部状态" style="width: 160px" @change="fetchFulfillments">
                        <el-option label="已预占" value="reserved" />
                        <el-option label="已支付" value="paid" />
                        <el-option label="已履约" value="fulfilled" />
                        <el-option label="已取消" value="cancelled" />
                    </el-select>
                    <el-input v-model="fulfillmentDistributorId" placeholder="分销商租户 ID" style="width: 180px" @keyup.enter="fetchFulfillments" />
                </div>
                <el-button link type="primary" @click="fetchFulfillments"><el-icon><Refresh /></el-icon></el-button>
            </div>
            <el-table :data="fulfillments" style="width: 100%" v-loading="loadingFulfillments" stripe>
                <el-table-column prop="fulfillment_no" label="履约单" min-width="190" />
                <el-table-column prop="sales_order_no" label="销售订单" min-width="190" />
                <el-table-column prop="sales_tenant_id" label="分销商" width="100" />
                <el-table-column prop="scenic_area_id" label="景区" width="90" />
                <el-table-column label="应结" width="110"><template #default="{ row }">¥{{ Number(row.settlement_amount || 0).toFixed(2) }}</template></el-table-column>
                <el-table-column label="状态" width="110"><template #default="{ row }">{{ fulfillmentStatusText(row.status) }}</template></el-table-column>
                <el-table-column label="票数" width="120">
                    <template #default="{ row }">{{ row.used_count }}/{{ row.ticket_count }} 已核销</template>
                </el-table-column>
                <el-table-column label="操作" width="90" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openFulfillment(row)">详情</el-button></template></el-table-column>
            </el-table>
        </el-tab-pane>

      </el-tabs>
    </div>

    <el-drawer v-model="fulfillmentDrawer" title="供应履约详情" size="78%" destroy-on-close>
      <div v-loading="loadingFulfillmentDetail" class="space-y-6">
        <el-descriptions v-if="fulfillmentDetail" :column="3" border>
          <el-descriptions-item label="履约单">{{ fulfillmentDetail.fulfillment.fulfillment_no }}</el-descriptions-item>
          <el-descriptions-item label="销售订单">{{ fulfillmentDetail.fulfillment.sales_order_no }}</el-descriptions-item>
          <el-descriptions-item label="履约状态">{{ fulfillmentStatusText(fulfillmentDetail.fulfillment.status) }}</el-descriptions-item>
          <el-descriptions-item label="分销商租户">{{ fulfillmentDetail.fulfillment.sales_tenant_id }}</el-descriptions-item>
          <el-descriptions-item label="履约景区">{{ fulfillmentDetail.fulfillment.scenic_area_id }}</el-descriptions-item>
          <el-descriptions-item label="结算状态">{{ fulfillmentDetail.settlement.statement_status ? settlementStatusText(fulfillmentDetail.settlement.statement_status) : '尚未生成结算单' }}</el-descriptions-item>
        </el-descriptions>

        <div v-if="fulfillmentDetail" class="grid grid-cols-4 gap-3">
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">履约总额</div><strong>¥{{ centsToYuan(fulfillmentDetail.settlement.gross_cents) }}</strong></div>
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">退款冲减</div><strong>¥{{ centsToYuan(fulfillmentDetail.settlement.refund_cents) }}</strong></div>
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">佣金</div><strong>¥{{ centsToYuan(fulfillmentDetail.settlement.commission_cents) }}</strong></div>
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">应结净额</div><strong class="text-green-700">¥{{ centsToYuan(fulfillmentDetail.settlement.net_cents) }}</strong></div>
        </div>

        <section v-for="item in fulfillmentDetail?.items || []" :key="item.id">
          <div class="flex items-center justify-between mb-2">
            <strong>{{ item.product_name }}</strong>
            <span class="text-sm text-gray-500">{{ formatDate(item.use_date) }} · {{ item.quantity }} 张 · 结算价 ¥{{ centsToYuan(item.settlement_price_cents) }}</span>
          </div>
          <el-table :data="item.tickets" size="small" border>
            <el-table-column prop="ticket_code" label="票码" min-width="180" />
            <el-table-column label="游客" min-width="160"><template #default="{ row }"><div>{{ row.visitor_name || '-' }}</div><div class="text-xs text-gray-400">{{ row.visitor_phone || '-' }}</div></template></el-table-column>
            <el-table-column prop="visitor_id" label="证件号" min-width="180" show-overflow-tooltip />
            <el-table-column label="票状态" width="110"><template #default="{ row }">{{ ticketStatusText(row.entitlement_status || row.status) }}</template></el-table-column>
            <el-table-column label="核销记录" min-width="240"><template #default="{ row }"><span v-if="!row.check_in_records?.length">暂无</span><div v-for="record in row.check_in_records || []" :key="record.id" class="text-xs">{{ formatDateTime(record.check_in_time) }} · {{ record.check_point?.name || `检票点 ${record.check_point_id}` }} · {{ record.result === 'success' ? '成功' : '失败' }}</div></template></el-table-column>
          </el-table>
        </section>

        <section v-if="fulfillmentDetail">
          <h3 class="font-semibold mb-2">退款与售后责任</h3>
          <el-table :data="fulfillmentDetail.after_sales" size="small" empty-text="该履约单暂无售后记录">
            <el-table-column prop="request_no" label="售后单" min-width="180" />
            <el-table-column label="类型" width="100"><template #default="{ row }">{{ afterSaleTypeText(row.type) }}</template></el-table-column>
            <el-table-column label="状态" width="110"><template #default="{ row }">{{ afterSaleStatusText(row.status) }}</template></el-table-column>
            <el-table-column label="退款金额" width="120"><template #default="{ row }">¥{{ centsToYuan(row.amount_cents) }}</template></el-table-column>
            <el-table-column prop="reason" label="原因" min-width="220" />
          </el-table>
        </section>
      </div>
    </el-drawer>

    <!-- Apply Dialog -->
    <el-dialog v-model="dialogVisible" title="申请代理权益" width="500px">
      <!-- ... (Same as before) ... -->
      <el-form label-position="top">
        <el-form-item label="请输入目标供应商的系统编号 (System Code)">
          <div class="flex gap-2">
            <el-input v-model="targetSystemCode" placeholder="例如: SYS001" class="flex-1" />
            <el-button @click="handleSearch" :loading="searching">查询</el-button>
          </div>
        </el-form-item>

        <div v-if="foundSupplier" class="bg-gray-50 p-4 rounded-lg mb-4 border border-gray-200">
           <div class="flex items-center gap-3 mb-2">
             <el-avatar :size="40" class="bg-indigo-100 text-indigo-500 font-bold">{{ foundSupplier.name.charAt(0) }}</el-avatar>
             <div>
               <div class="font-bold text-gray-800">{{ foundSupplier.name }}</div>
               <div class="text-xs text-gray-500">联系人: {{ foundSupplier.contact || '暂无' }}</div>
               <div class="text-xs text-gray-400 font-mono">CODE: {{ foundSupplier.code }}</div>
             </div>
           </div>
           <el-alert title="确认申请后，需等待对方审核通过才可代理其产品。" type="info" :closable="false" />
        </div>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleApply" :disabled="!foundSupplier" :loading="applying">确认申请</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- Sourcing Dialog (Product List) -->
    <el-dialog v-model="sourcingDialogVisible" title="可转销产品列表" width="800px">
        <el-table :data="supplierProducts" v-loading="loadingProducts" height="400">
            <el-table-column prop="name" label="产品名称" min-width="150" />
            <el-table-column prop="settlement_price" label="结算价" width="120">
                <template #default="{ row }">
                   <span class="font-bold text-orange-500">¥{{ row.settlement_price }}</span>
                </template>
            </el-table-column>
            <el-table-column prop="validity_type" label="有效期" width="150">
                 <template #default="{ row }">
                    <span v-if="row.validity_type === 'date'">指定日期</span>
                    <span v-else>有效期{{ row.validity_days }}天</span>
                 </template>
            </el-table-column>
            <el-table-column label="操作" width="120" fixed="right">
                <template #default="{ row }">
                    <el-button type="primary" size="small" @click="handleImportConfig(row)">一键上架</el-button>
                </template>
            </el-table-column>
        </el-table>
    </el-dialog>

    <!-- Import Config Dialog -->
    <el-dialog v-model="importDialogVisible" title="上架配置" width="500px">
        <el-form label-position="top">
            <el-alert title="将供应商产品映射到您的本地票务库，对接成功后可直接售卖。" type="success" :closable="false" class="mb-4"/>
            <el-form-item label="产品名称 (本地重命名)">
                <el-input v-model="importForm.name" />
            </el-form-item>
            <el-form-item label="您的售价 (Display Price)">
                <el-input-number v-model="importForm.price" :min="0" :precision="2" class="w-full" />
                <div class="text-xs text-gray-400 mt-1">结算成本: ¥{{ importForm.settlement_price }}</div>
            </el-form-item>
            <el-form-item label="上架渠道 (可多选)">
                <el-checkbox-group v-model="importForm.channels">
                    <el-checkbox label="online">线上微官网 (MiniApp)</el-checkbox>
                    <el-checkbox label="offline">线下售票窗口 (Window)</el-checkbox>
                </el-checkbox-group>
            </el-form-item>
        </el-form>
        <template #footer>
            <span class="dialog-footer">
                <el-button @click="importDialogVisible = false">取消</el-button>
                <el-button type="primary" @click="confirmImport" :loading="importing">确认上架</el-button>
            </span>
        </template>
    </el-dialog>

    <!-- Recharge Dialog -->
    <el-dialog v-model="rechargeDialogVisible" title="资金充值" width="400px">
        <el-form label-position="top">
            <el-alert type="warning" :closable="false" class="mb-4">
                <template #title>
                    正在给 <b>{{ rechargeForm.agent_name }}</b> 充值
                </template>
            </el-alert>
            <el-form-item label="充值金额 (CNY)">
                <el-input-number v-model="rechargeForm.amount" :min="0" :step="100" class="w-full" />
            </el-form-item>
        </el-form>
        <template #footer>
            <span class="dialog-footer">
                <el-button @click="rechargeDialogVisible = false">取消</el-button>
                <el-button type="primary" @click="confirmRecharge" :loading="recharging">确认充值</el-button>
            </span>
        </template>
    </el-dialog>

    <el-dialog v-model="offersDialogVisible" title="Supplier offers" width="980px">
        <div class="flex justify-between items-center mb-3">
            <span class="text-sm text-gray-500">Manage the supplier-authorized revision, price floor, quota and channels.</span>
            <div class="flex gap-2">
                <el-button size="small" @click="loadOffers">Refresh</el-button>
                <el-button type="primary" size="small" @click="openOfferForm">Create offer</el-button>
            </div>
        </div>
        <el-table :data="offers" v-loading="loadingOffers" height="360" stripe>
            <el-table-column prop="source_product_id" label="Product" width="90" />
            <el-table-column prop="product_revision_id" label="Revision" width="90" />
            <el-table-column prop="settlement_price" label="Settlement" width="110" />
            <el-table-column prop="minimum_retail_price_cents" label="Retail floor (cents)" width="140" />
            <el-table-column prop="quota" label="Quota" width="80" />
            <el-table-column prop="allowed_channels" label="Channels" min-width="150" />
            <el-table-column prop="status" label="Status" width="100" />
            <el-table-column label="Actions" width="180" fixed="right">
                <template #default="{ row }">
                    <el-button v-if="row.status === 'active'" link type="warning" @click="handleOfferStatus(row, 'suspended')">Suspend</el-button>
                    <el-button v-else-if="row.status === 'suspended'" link type="success" @click="handleOfferStatus(row, 'active')">Resume</el-button>
                    <el-button v-if="row.status !== 'expired'" link type="danger" @click="handleOfferStatus(row, 'expired')">Expire</el-button>
                </template>
            </el-table-column>
        </el-table>
    </el-dialog>

    <el-dialog v-model="offerFormVisible" title="Create supplier offer" width="560px">
        <el-form :model="offerForm" label-position="top">
            <el-form-item label="Source product">
                <el-select v-model="offerForm.source_product_id" filterable class="w-full" placeholder="Select an online distributable product">
                    <el-option v-for="product in sourceProducts" :key="product.id" :label="`${product.name} (#${product.id})`" :value="product.id" />
                </el-select>
            </el-form-item>
            <div class="grid grid-cols-2 gap-3">
                <el-form-item label="Settlement price">
                    <el-input-number v-model="offerForm.settlement_price" :min="0.01" :precision="2" class="w-full" />
                </el-form-item>
                <el-form-item label="Minimum retail price">
                    <el-input-number v-model="offerForm.minimum_retail_price" :min="0" :precision="2" class="w-full" />
                </el-form-item>
                <el-form-item label="Quota (0 = unlimited)">
                    <el-input-number v-model="offerForm.quota" :min="0" :precision="0" class="w-full" />
                </el-form-item>
                <el-form-item label="Commission (BPS)">
                    <el-input-number v-model="offerForm.commission_bps" :min="0" :max="10000" :precision="0" class="w-full" />
                </el-form-item>
            </div>
            <el-form-item label="Allowed channels (comma-separated)">
                <el-input v-model="offerForm.allowed_channels" placeholder="window,online,ota" />
            </el-form-item>
        </el-form>
        <template #footer>
            <el-button @click="offerFormVisible = false">Cancel</el-button>
            <el-button type="primary" :loading="savingOffer" @click="createOffer">Create</el-button>
        </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { Connection, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const currentUser = (() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })()
const activeCapabilities = new Set((currentUser.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability))
const canSupply = computed(() => activeCapabilities.has('supplier'))
const canDistribute = computed(() => activeCapabilities.has('distributor'))
const activeTab = ref(canDistribute.value ? 'suppliers' : 'agents')

// Suppliers State
const loadingSuppliers = ref(false)
const suppliers = ref<any[]>([])

// Agents State
const loadingAgents = ref(false)
const agents = ref<any[]>([])
const loadingFulfillments = ref(false)
const fulfillments = ref<any[]>([])
const fulfillmentStatus = ref('')
const fulfillmentDistributorId = ref('')
const fulfillmentDrawer = ref(false)
const loadingFulfillmentDetail = ref(false)
const fulfillmentDetail = ref<any>(null)

// Apply Dialog State
const dialogVisible = ref(false)
const targetSystemCode = ref('')
const searching = ref(false)
const applying = ref(false)
const foundSupplier = ref<any>(null)

// Sourcing State
const sourcingDialogVisible = ref(false)
const loadingProducts = ref(false)
const supplierProducts = ref<any[]>([])
const currentSupplierId = ref(0)

// Import State
const importDialogVisible = ref(false)
const importing = ref(false)
const importForm = reactive({
    source_product_id: 0,
    name: '',
    price: 0,
    settlement_price: 0,
    channels: ['online']
})

const offersDialogVisible = ref(false)
const offerFormVisible = ref(false)
const loadingOffers = ref(false)
const savingOffer = ref(false)
const offers = ref<any[]>([])
const sourceProducts = ref<any[]>([])
const selectedDistributorId = ref(0)
const offerForm = reactive({
    source_product_id: 0,
    settlement_price: 0,
    minimum_retail_price: 0,
    quota: 0,
    commission_bps: 0,
    allowed_channels: 'window,online,ota'
})

// Methods
const fetchSuppliers = async () => {
  loadingSuppliers.value = true
  try {
     const res = await request.get('/distribution/suppliers')
     suppliers.value = res.data.data || []
  } catch (e) {
     ElMessage.error('获取供应商列表失败')
  } finally {
     loadingSuppliers.value = false
  }
}

const fetchAgents = async () => {
  loadingAgents.value = true
  try {
     const res = await request.get('/distribution/agents')
     agents.value = res.data.data || []
  } catch (e) {
     ElMessage.error('获取分销商列表失败')
  } finally {
     loadingAgents.value = false
  }
}

const handleTabChange = (tabName: string) => {
    if (tabName === 'suppliers') {
        fetchSuppliers()
    } else if (tabName === 'agents') {
        fetchAgents()
    } else if (tabName === 'fulfillments') {
        fetchFulfillments()
    }
}

const fetchFulfillments = async () => {
    loadingFulfillments.value = true
    try {
        const params: Record<string, string | number> = { page: 1, page_size: 100 }
        if (fulfillmentStatus.value) params.status = fulfillmentStatus.value
        if (fulfillmentDistributorId.value.trim()) params.distributor_tenant_id = Number(fulfillmentDistributorId.value)
        const res = await request.get('/distribution/fulfillments', { params })
        fulfillments.value = res.data.data || []
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || 'Failed to load fulfillment worklist')
    } finally {
        loadingFulfillments.value = false
    }
}

const openFulfillment = async (row: any) => {
    fulfillmentDrawer.value = true
    loadingFulfillmentDetail.value = true
    fulfillmentDetail.value = null
    try {
        fulfillmentDetail.value = (await request.get(`/distribution/fulfillments/${row.id}`)).data
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '履约详情加载失败')
    } finally {
        loadingFulfillmentDetail.value = false
    }
}

const centsToYuan = (value: number) => (Number(value || 0) / 100).toFixed(2)
const formatDate = (value: string) => value ? new Date(value).toLocaleDateString('zh-CN') : '未指定日期'
const formatDateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const fulfillmentStatusText = (value: string) => ({ reserved: '已预占', paid: '已支付', fulfilled: '已履约', cancelled: '已取消' } as any)[value] || value
const settlementStatusText = (value: string) => ({ draft: '草稿', supplier_confirmed: '供应商已确认', confirmed: '双方已确认', disputed: '有争议', paid: '已付款' } as any)[value] || value
const ticketStatusText = (value: string) => ({ issued: '已出票', active: '可使用', unused: '未使用', used: '已核销', refunded: '已退款', void: '已作废', expired: '已过期' } as any)[value] || value
const afterSaleTypeText = (value: string) => ({ refund: '退票', reschedule: '改期', exchange: '换票', void: '作废', reissue: '补打' } as any)[value] || value
const afterSaleStatusText = (value: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '失败' } as any)[value] || value

const handleSearch = async () => {
  if (!targetSystemCode.value) return
  searching.value = true
  foundSupplier.value = null
  try {
    const res = await request.get('/distribution/search', { params: { code: targetSystemCode.value }})
    foundSupplier.value = res.data.data
  } catch (e: any) {
    ElMessage.warning(e.response?.data?.error || '未找到该供应商')
    foundSupplier.value = null
  } finally {
    searching.value = false
  }
}

const handleApply = async () => {
    if (!foundSupplier.value) return
    applying.value = true
    try {
        await request.post('/distribution/apply', { system_code: foundSupplier.value.code })
        ElMessage.success('申请已提交')
        dialogVisible.value = false
        foundSupplier.value = null
        targetSystemCode.value = ''
        fetchSuppliers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '申请失败')
    } finally {
        applying.value = false
    }
}

// Recharge Logic
const rechargeDialogVisible = ref(false)
const recharging = ref(false)
const rechargeForm = reactive({
    agent_id: 0, // Relationship ID
    agent_name: '',
    amount: 1000
})

const handleRecharge = (row: any) => {
    rechargeForm.agent_id = row.id // Relationship ID
    rechargeForm.agent_name = row.agent_name || row.supplier_name // Handle both views if needed, but usually Supplier recharges Agent
    rechargeForm.amount = 1000
    rechargeDialogVisible.value = true
}

const confirmRecharge = async () => {
    if (rechargeForm.amount <= 0) {
        ElMessage.warning('金额必须大于0')
        return
    }
    recharging.value = true
    try {
        await request.post(`/distribution/agents/${rechargeForm.agent_id}/recharge`, {
            amount: rechargeForm.amount,
            idempotency_key: `admin-recharge-${rechargeForm.agent_id}-${Date.now()}-${Math.random().toString(36).slice(2)}`
        })
        ElMessage.success('充值成功')
        rechargeDialogVisible.value = false
        // Refresh lists
        if (activeTab.value === 'suppliers') fetchSuppliers()
        else fetchAgents()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '充值失败')
    } finally {
        recharging.value = false
    }
}

const handleOffers = async (row: any) => {
    selectedDistributorId.value = row.agent_tenant_id
    offersDialogVisible.value = true
    await Promise.all([loadOffers(), loadSourceProducts()])
}

const loadOffers = async () => {
    if (!selectedDistributorId.value) return
    loadingOffers.value = true
    try {
        const res = await request.get('/distribution/offers', { params: { distributor_tenant_id: selectedDistributorId.value, page: 1, page_size: 100 } })
        offers.value = res.data.data || []
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || 'Failed to load offers')
    } finally {
        loadingOffers.value = false
    }
}

const loadSourceProducts = async () => {
    try {
        const res = await request.get('/products', { params: { page: 1, page_size: 100 } })
        sourceProducts.value = (res.data.data || []).filter((product: any) => product.status === 'online' && product.is_distributable)
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || 'Failed to load source products')
    }
}

const openOfferForm = () => {
    offerForm.source_product_id = sourceProducts.value[0]?.id || 0
    offerForm.settlement_price = 0
    offerForm.minimum_retail_price = 0
    offerForm.quota = 0
    offerForm.commission_bps = 0
    offerForm.allowed_channels = 'window,online,ota'
    offerFormVisible.value = true
}

const createOffer = async () => {
    if (!selectedDistributorId.value || !offerForm.source_product_id || offerForm.settlement_price <= 0 || !offerForm.allowed_channels.trim()) {
        ElMessage.warning('Product, settlement price and channels are required')
        return
    }
    savingOffer.value = true
    try {
        await request.post('/distribution/offers', { distributor_tenant_id: selectedDistributorId.value, ...offerForm })
        ElMessage.success('Offer created')
        offerFormVisible.value = false
        await loadOffers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || 'Failed to create offer')
    } finally {
        savingOffer.value = false
    }
}

const handleOfferStatus = async (row: any, status: string) => {
    try {
        await request.patch(`/distribution/offers/${row.id}/status`, { status, reason: `Changed from supplier console to ${status}` })
        ElMessage.success('Offer status updated')
        await loadOffers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || 'Failed to update offer')
    }
}

const handleAudit = async (row: any, status: string) => {
    const actionText = status === 'active' ? '通过' : '拒绝'
    try {
        await ElMessageBox.confirm(`确定要${actionText}该分销商的申请吗？`, '提示', {
            type: status === 'active' ? 'success' : 'warning'
        })
        await request.post(`/distribution/agents/${row.id}/audit`, { status })
        ElMessage.success('操作成功')
        fetchAgents()
    } catch (e) {
        // cancelled or error
    }
}

const handleSourcing = async (row: any) => {
    currentSupplierId.value = row.supplier_tenant_id // Note: row structure depends on API
    sourcingDialogVisible.value = true
    loadingProducts.value = true
    try {
        // We need supplier_id, from DB struct it is SupplierTenantID
        const res = await request.get('/distribution/products', { params: { supplier_id: row.supplier_tenant_id }})
        supplierProducts.value = res.data.data || []
    } catch (e) {
        ElMessage.error('获取商品列表失败')
    } finally {
        loadingProducts.value = false
    }
}

const handleImportConfig = (product: any) => {
    importForm.source_product_id = product.id
    importForm.name = product.name
    importForm.price = product.price // Default to retail price
    importForm.settlement_price = product.settlement_price
    importForm.channels = ['online']
    importDialogVisible.value = true
}

const confirmImport = async () => {
    if (importForm.channels.length === 0) {
        ElMessage.warning('请至少选择一个上架渠道')
        return
    }
    importing.value = true
    try {
        for (const channel of importForm.channels) {
            await request.post('/distribution/products/import', {
                source_product_id: importForm.source_product_id,
                name: importForm.name + (channel === 'offline' && importForm.channels.length > 1 ? ' (线下)' : ''),
                price: importForm.price,
                type: channel
            })
        }
        ElMessage.success('对接成功！请前往“门票管理”查看')
        importDialogVisible.value = false
        sourcingDialogVisible.value = false // Optionally close parent
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '对接失败')
    } finally {
        importing.value = false
    }
}


const getStatusType = (status: string) => {
    const map: any = { active: 'success', pending: 'warning', rejected: 'danger' }
    return map[status] || 'info'
}

const getStatusText = (status: string) => {
    const map: any = { active: '合作中', pending: '待审核', rejected: '已拒绝' }
    return map[status] || status
}

const getLevelText = (level: string) => {
    const map: any = { standard: '普通代理', core: '核心代理', diamond: '金牌代理' }
    return map[level] || level
}

onMounted(() => {
    if (canDistribute.value) fetchSuppliers()
    if (canSupply.value) fetchAgents()
})
</script>
