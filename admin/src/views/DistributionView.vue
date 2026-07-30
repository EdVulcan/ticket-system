<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100 flex justify-between items-center">
      <div>
        <h2 class="text-lg font-bold text-gray-900">分销中心 (B2B)</h2>
        <p class="text-xs text-gray-500 mt-1">连接产业上下游，拓展业务边界</p>
      </div>
      <div>
         <el-button v-if="activeTab === 'suppliers'" type="primary" size="large" @click="dialogVisible = true">
            <el-icon class="mr-2"><Connection /></el-icon> 寻找供应商
         </el-button>
      </div>
    </div>

    <!-- Main Content -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
      <el-tabs v-model="activeTab" @tab-change="handleTabChange">
        
        <!-- Tab 1: My Suppliers -->
        <el-tab-pane label="我的供应商 (我是分销商)" name="suppliers">
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
        <el-tab-pane label="我的分销商 (我是供应商)" name="agents">
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
                         <el-button type="warning" size="small" @click="handleRecharge(row)">充值</el-button>
                    </div>
                </template>
                </el-table-column>
            </el-table>
        </el-tab-pane>

      </el-tabs>
    </div>

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
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Connection, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const activeTab = ref('suppliers')

// Suppliers State
const loadingSuppliers = ref(false)
const suppliers = ref<any[]>([])

// Agents State
const loadingAgents = ref(false)
const agents = ref<any[]>([])

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
    } else {
        fetchAgents()
    }
}

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
            amount: rechargeForm.amount
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
    fetchSuppliers()
})
</script>
