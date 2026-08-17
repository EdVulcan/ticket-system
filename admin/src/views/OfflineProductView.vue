<template>
  <div class="catalog-page bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <div>
        <h2 class="text-lg font-bold text-gray-900">窗口门票管理</h2>
        <p class="text-xs text-gray-500 mt-1">管理线下窗口销售的票务产品（仅限窗口/自助机使用）</p>
      </div>
      <el-button v-if="canWrite" type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> 发布窗口票
      </el-button>
    </div>

    <!-- Filter -->
    <div class="filter-toolbar catalog-filter-bar">
      <el-input v-model="searchQuery" placeholder="搜索门票名称..." class="search-filter" prefix-icon="Search" clearable @keyup.enter="applyFilters" @clear="applyFilters" />
      <el-select v-model="filterStatus" placeholder="全部状态" class="status-filter" clearable @change="applyFilters">
        <el-option label="全部" value="" />
        <el-option label="上架中" value="online" />
        <el-option label="已下架" value="offline" />
      </el-select>
      <el-button :icon="Refresh" @click="resetFilters">重置</el-button>
    </div>

    <el-table :data="tableData" class="catalog-table" style="width: 100%" v-loading="loading" border>
      <el-table-column prop="id" label="编号" width="70" align="center" />
      <el-table-column prop="name" label="门票名称" min-width="180">
        <template #default="{ row }">
          <div class="font-medium">{{ row.name }}</div>
          <el-tag size="small" type="info" class="mt-1">窗口专用</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="所属景区" min-width="150">
        <template #default="{ row }">{{ scenicAreaName(row.scenic_area_id) }}</template>
      </el-table-column>
      <el-table-column label="价格管理" width="150">
        <template #default="{ row }">
          <div class="text-sm">售价: <span class="font-bold text-orange-500">¥{{ row.price }}</span></div>
        </template>
      </el-table-column>
      <el-table-column label="有效期" width="180">
        <template #default="{ row }">
          <div class="text-xs">
            <div v-if="row.validity_type === 'date'">📅 指定日期</div>
            <div v-else-if="row.validity_days === 0">📅 当日有效</div>
            <div v-else>⏳ 购买后 {{ row.validity_days }} 天有效</div>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100" align="center">
        <template #default="{ row }">
          <el-tag :type="row.status === 'online' ? 'success' : 'danger'" effect="dark">
            {{ row.status === 'online' ? '上架中' : '已下架' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column v-if="canHistoryWrite" label="操作" width="200" fixed="right" align="center">
        <template #default="{ row }">
          <el-button v-if="canWrite" link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
          <el-button 
            v-if="canWrite || row.status === 'online'"
            link 
            :type="row.status === 'online' ? 'danger' : 'success'" 
            size="small" 
            @click="handleToggleStatus(row)"
          >
            {{ row.status === 'online' ? '下架' : '上架' }}
          </el-button>
           <el-button v-if="canWrite" link type="danger" size="small" @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
      <template #empty><el-empty description="没有匹配的窗口票种" :image-size="72" /></template>
    </el-table>

    <div class="table-footer catalog-pagination">
      <span class="table-caption">共 {{ total }} 个窗口票种</span>
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="sizes, prev, pager, next"
        @current-change="fetchData"
        @size-change="handlePageSizeChange"
      />
    </div>

    <!-- Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑窗口票' : '发布窗口票'"
      width="min(920px, calc(100vw - 32px))"
      top="4vh"
      class="product-editor-dialog"
      :close-on-click-modal="false"
      destroy-on-close
    >
      <template #header>
        <div class="editor-dialog-header">
          <div>
            <div class="editor-dialog-title">{{ isEdit ? '编辑窗口票种' : '发布窗口票种' }}</div>
            <div class="editor-dialog-subtitle">完善窗口销售与检票规则后保存，票种默认仅供现场使用。</div>
          </div>
          <el-tag size="small" effect="plain" type="warning">窗口票</el-tag>
        </div>
      </template>
      <el-form :model="form" label-width="100px" :rules="rules" ref="formRef" class="editor-form">
        
        <el-divider content-position="left">基础信息</el-divider>
        <div class="form-grid">
          <el-form-item label="门票名称" prop="product.name" class="col-span-2">
            <el-input v-model="form.product.name" placeholder="例如：成人全天通票(窗口)" />
          </el-form-item>
          <el-form-item label="所属景区" prop="product.scenic_area_id" class="col-span-2">
            <el-select v-model="form.product.scenic_area_id" aria-label="所属景区" placeholder="请选择门票所属景区" class="w-full" @change="handleScenicAreaChange">
              <el-option v-for="area in scenicAreaOptions" :key="area.id" :label="area.status === 'active' ? area.name : `${area.name}（已停用）`" :value="area.id" />
            </el-select>
            <div class="text-xs text-gray-400 mt-1">切换景区会清空已选检票点；已售门票仍按售票时的景区核销。</div>
          </el-form-item>
          <el-form-item label="销售价格" prop="product.price">
            <el-input-number v-model="form.product.price" :precision="2" :step="1" :min="0" class="w-full" />
          </el-form-item>

          <el-form-item label="退票规则">
            <el-radio-group v-model="form.product.refund_type">
              <el-radio value="free">允许退票</el-radio>
              <el-radio value="no_refund">不可退票</el-radio>
            </el-radio-group>
            <div class="text-xs text-gray-400 mt-1">规则会在售票时保存；后续修改不会改变已经售出的门票。</div>
          </el-form-item>
          
          <el-form-item label="标签预设" class="col-span-2">
             <el-select
               v-model="productTags"
               multiple
               filterable
               allow-create
               default-first-option
               :reserve-keyword="false"
               placeholder="输入标签后按回车 (如: 热销, 特惠)"
               class="w-full"
             >
               <el-option label="热销" value="热销" />
               <el-option label="特惠" value="特惠" />
               <el-option label="推荐" value="推荐" />
               <el-option label="新品" value="新品" />
             </el-select>
          </el-form-item>
        </div>

        <el-divider content-position="left">规则设置</el-divider>
        <el-form-item label="有效期类型">
           <el-radio-group v-model="form.product.validity_type">
             <el-radio label="days">购买后N天有效</el-radio>
             <el-radio label="date">指定日期范围</el-radio>
           </el-radio-group>
        </el-form-item>

        <div v-if="form.product.validity_type === 'days'">
          <el-form-item label="有效天数">
             <el-radio-group v-model="form.product.validity_days">
               <el-radio :label="0" border>当日有效</el-radio>
               <el-radio :label="1" border>次日有效</el-radio>
             </el-radio-group>
             <div class="flex items-center mt-2 gap-2">
               <span class="text-sm text-gray-500">或自定义天数:</span>
               <el-input-number v-model="form.product.validity_days" :min="0" size="small" style="width: 100px" />
               <span class="text-xs text-gray-400">(0=当日, N=购买后N天内有效)</span>
             </div>
          </el-form-item>
        </div>

        <div v-else>
          <el-form-item label="有效日期">
            <el-date-picker
              v-model="validityDateRange"
              type="daterange"
              range-separator="至"
              start-placeholder="开始日期"
              end-placeholder="结束日期"
              value-format="YYYY-MM-DD"
              class="w-full"
            />
          </el-form-item>
        </div>

        <el-form-item label="核销规则">
          <el-alert title="设置门票可通行的检票点及通行次数" type="info" show-icon :closable="false" class="mb-4" />
          
          <div class="rule-group-card" v-for="(group, gIdx) in form.rule.groups" :key="gIdx">
            <div class="flex justify-between items-center mb-2">
              <div class="flex items-center gap-2">
                <span class="font-bold text-sm text-slate-700">规则组 #{{ gIdx + 1 }}</span>
                <el-tag size="small" effect="plain">{{ group.max_total_check_in === 0 ? '全选模式' : `M选${group.max_total_check_in}` }}</el-tag>
              </div>
              <el-button type="danger" link size="small" @click="removeGroup(gIdx)" v-if="form.rule.groups.length > 1">删除组</el-button>
            </div>
            
            <div class="rule-group-fields form-grid">
              <el-form-item label="分组名称" label-width="80px" :required="true" class="mb-0">
                <el-input v-model="group.group_name" placeholder="如：大门票、剧场票" />
              </el-form-item>
              <el-form-item label="可选点位数(M)" label-width="120px" class="mb-0">
                <el-input-number v-model="group.max_total_check_in" :min="0" placeholder="0为不限" />
                <div class="text-xs text-gray-400 mt-1">0 = 全选, N = M选N</div>
              </el-form-item>
            </div>

            <div class="rule-items-list">
              <div v-for="(item, iIdx) in group.items" :key="iIdx" class="flex items-center gap-2">
                <el-select v-model="item.check_point_id" aria-label="检票点" :placeholder="form.product.scenic_area_id ? '选择检票点' : '请先选择所属景区'" class="flex-1" :disabled="!form.product.scenic_area_id">
                  <el-option 
                    v-for="cp in filteredCheckpoints"
                    :key="cp.id" 
                    :label="cp.name" 
                    :value="cp.id" 
                    :disabled="isCheckPointDisabled(cp.id, item.check_point_id)"
                  />
                </el-select>
                <el-input-number v-model="item.max_per_check_in" :min="1" controls-position="right" style="width: 120px" placeholder="单点次数" />
                <span class="text-xs text-gray-400">次</span>
                <el-button circle size="small" type="danger" @click="removeItem(gIdx, iIdx)">
                  <el-icon><Minus /></el-icon>
                </el-button>
              </div>
              <el-button type="primary" link size="small" @click="addItem(gIdx)">+ 添加检票点</el-button>
            </div>
          </div>
          
          <el-button type="primary" plain class="w-full border-dashed" @click="addGroup">+ 添加新规则组</el-button>
        </el-form-item>

      </el-form>

      <template #footer>
        <span class="dialog-footer editor-dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleSubmit" :loading="submitting">保存并发布</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { Plus, Minus, Refresh } from '@element-plus/icons-vue'
import { hasPermission } from '@/utils/permissions'
import { isActiveScenicSupplier, isScenicHistorySupplier, readStoredUser } from '@/utils/tenantAccess'

const currentUser = readStoredUser()
const hasCatalogWritePermission = hasPermission(currentUser, 'catalog.write')
const canWrite = hasCatalogWritePermission && isActiveScenicSupplier(currentUser)
const canHistoryWrite = hasCatalogWritePermission && isScenicHistorySupplier(currentUser)

const loading = ref(false)
const submitting = ref(false)
const tableData = ref<any[]>([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(10)
const checkpoints = ref<any[]>([])
const scenicAreas = ref<any[]>([])
const dialogVisible = ref(false)
const isEdit = ref(false)
const searchQuery = ref('')
const filterStatus = ref('')
const formRef = ref()

const form = reactive({
  id: 0,
  product: {
    name: '',
    scenic_area_id: null as number | null,
    price: 0,
    settlement_price: 0,
    type: 'offline', // Force Offline
    status: 'online',
    code_mode: 'ticket', // Force Ticket Mode
    validity_type: 'days', // Force Days
    validity_days: 0,
    stock_type: 'unlimited', // Force Unlimited
    refund_type: 'free',
    // Unused fields
    validity_start_date: null as string | null, validity_end_date: null as string | null, daily_stock: 0, time_slot_config: '',
    real_name_required: false, region_limit: '', limit_per_phone: 0, limit_per_id: 0, refund_rule: '',
    tags: '' // JSON string
  },
  rule: {
    name: '',
    validity_type: 'date',
    groups: [
      { group_name: '默认分组', max_total_check_in: 1, items: [{ check_point_id: null, max_per_check_in: 1 }] }
    ]
  }
})

const rules = {
  'product.name': [{ required: true, message: '请输入门票名称', trigger: 'blur' }],
  'product.price': [{ required: true, message: '请输入售价', trigger: 'blur' }],
  'product.scenic_area_id': [{ required: true, type: 'number', min: 1, message: '请选择所属景区', trigger: 'change' }]
}

const fetchReferences = async () => {
  try {
    const [checkpointRes, scenicRes] = await Promise.all([
      request.get('/checkpoints', { params: { page_size: 100 } }),
      request.get('/scenic-areas')
    ])
    checkpoints.value = checkpointRes.data.data || []
    scenicAreas.value = scenicRes.data.data || []
  } catch (e) { console.error(e) }
}

const activeScenicAreas = computed(() => scenicAreas.value.filter(area => area.status === 'active'))
const scenicAreaOptions = computed(() => scenicAreas.value.filter(area => area.status === 'active' || area.id === form.product.scenic_area_id))
const filteredCheckpoints = computed(() => checkpoints.value.filter(cp => cp.scenic_area_id === form.product.scenic_area_id))
const scenicAreaName = (id: number) => scenicAreas.value.find(area => area.id === id)?.name || '未归属'

const fetchData = async () => {
  loading.value = true
  try {
    const res = await request.get('/products', {
      params: {
        type: 'offline',
        product_kind: 'ticket',
        page: currentPage.value,
        page_size: pageSize.value,
        status: filterStatus.value || undefined,
        search: searchQuery.value.trim() || undefined,
      }
    })
    tableData.value = res.data.data || []
    total.value = res.data.total || 0
  } catch (error) {
    ElMessage.error('获取数据失败')
  } finally {
    loading.value = false
  }
}

const applyFilters = () => {
  currentPage.value = 1
  fetchData()
}

const resetFilters = () => {
  searchQuery.value = ''
  filterStatus.value = ''
  applyFilters()
}

const handlePageSizeChange = () => {
  currentPage.value = 1
  fetchData()
}

const validityDateRange = ref<[string, string] | null>(null)
const productTags = ref<string[]>([])

const handleAdd = () => {
  isEdit.value = false
  form.id = 0
  form.product.name = ''
  form.product.scenic_area_id = activeScenicAreas.value.length === 1 ? activeScenicAreas.value[0].id : null
  form.product.price = 0
  form.product.settlement_price = 0
  form.product.refund_type = 'free'
  form.product.validity_type = 'days'
  form.product.validity_days = 0
  form.product.validity_days = 0
  validityDateRange.value = null
  productTags.value = []
  form.rule.groups = [{ group_name: '默认分组', max_total_check_in: 1, items: [{ check_point_id: null, max_per_check_in: 1 }] }]
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  const data = JSON.parse(JSON.stringify(row))
  form.id = data.id
  Object.assign(form.product, data)
  form.rule = data.rule || { name: data.name, validity_type: 'date', groups: [] }
  
  if (data.validity_start_date && data.validity_end_date) {
    validityDateRange.value = [data.validity_start_date.split('T')[0], data.validity_end_date.split('T')[0]]
  } else {
    form.product.validity_days = 0
  validityDateRange.value = null
  productTags.value = []
  }

  try {
    productTags.value = data.tags ? JSON.parse(data.tags) : []
  } catch(e) { productTags.value = [] }

  // Ensure groups structure
  if (!form.rule.groups || form.rule.groups.length === 0) {
     form.rule.groups = [{ group_name: '默认分组', max_total_check_in: 1, items: [{ check_point_id: null, max_per_check_in: 1 }] }]
  } else {
     form.rule.groups.forEach((g: any) => { if (!g.items) g.items = [] })
  }
  
  dialogVisible.value = true
}

const addGroup = () => form.rule.groups.push({ group_name: '', max_total_check_in: 1, items: [] })
const removeGroup = (idx: number) => form.rule.groups.splice(idx, 1)
const addItem = (gIdx: number) => form.rule.groups[gIdx].items.push({ check_point_id: null, max_per_check_in: 1 })
const removeItem = (gIdx: number, iIdx: number) => form.rule.groups[gIdx].items.splice(iIdx, 1)

const handleScenicAreaChange = () => {
  for (const group of form.rule.groups) {
    for (const item of group.items) item.check_point_id = null
  }
}

const handleSubmit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      submitting.value = true
      
      // Handle Validity Date Range
      if (form.product.validity_type === 'date' && validityDateRange.value) {
        form.product.validity_start_date = validityDateRange.value[0] + 'T00:00:00Z'
        form.product.validity_end_date = validityDateRange.value[1] + 'T23:59:59Z'
      } else {
        form.product.validity_start_date = null
        form.product.validity_end_date = null
      }
      
      form.product.tags = JSON.stringify(productTags.value)

      // Sync rule name
      if (!form.rule.name) form.rule.name = form.product.name

      // Validation for M-choose-N
      for (const group of form.rule.groups) {
        if (group.max_total_check_in > 0) {
          if (group.max_total_check_in > group.items.length) {
             ElMessage.error(`规则组 "${group.group_name}" 的可选点位数(M)不能大于该组包含的检票点总数(N)`)
             submitting.value = false
             return
          }
        }
      }
      
      try {
        if (isEdit.value) {
           await request.put(`/products/${form.id}`, form)
           ElMessage.success('更新成功')
        } else {
           await request.post('/products', form)
           ElMessage.success('发布成功')
        }
        dialogVisible.value = false
        fetchData()
      } catch (error: any) {
        ElMessage.error(error.response?.data?.error || '操作失败')
      } finally {
        submitting.value = false
      }
    }
  })
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确认删除该门票吗？', '警告', { type: 'warning' }).then(async () => {
    await request.delete(`/products/${row.id}`)
    fetchData()
  })
}

const handleToggleStatus = (row: any) => {
  const newStatus = row.status === 'online' ? 'offline' : 'online'
  ElMessageBox.confirm(`确认${newStatus === 'online' ? '上架' : '下架'}吗？`, '提示').then(async () => {
    await request.patch(`/products/${row.id}/status`, { status: newStatus })
    fetchData()
  })
}

const isCheckPointDisabled = (cpId: number, currentVal: number | null) => {
  if (cpId === currentVal) return false
  for (const group of form.rule.groups) {
    for (const item of group.items) {
      if (item.check_point_id === cpId) return true
    }
  }
  return false
}

const handleAgentTaskCompleted = () => {
  void fetchData()
}

onMounted(() => {
  fetchData()
  fetchReferences()
  window.addEventListener('agent-task-completed', handleAgentTaskCompleted)
})

onBeforeUnmount(() => window.removeEventListener('agent-task-completed', handleAgentTaskCompleted))
</script>

<style scoped>
.catalog-filter-bar {
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin: 20px 0 16px;
}

.search-filter {
  width: min(360px, 100%);
}

.status-filter {
  width: 144px;
}

.catalog-table {
  --el-table-header-bg-color: #f8fafc;
  --el-table-border-color: #e6eaf0;
  border-radius: 8px;
  overflow: hidden;
}

.catalog-pagination {
  align-items: center;
  border-top: 1px solid #eef1f5;
  display: flex;
  justify-content: space-between;
  margin-top: 16px;
  padding-top: 16px;
}

.table-caption {
  color: #64748b;
  font-size: 13px;
}

.editor-dialog-header {
  align-items: flex-start;
  display: flex;
  justify-content: space-between;
  padding-right: 28px;
}

.editor-dialog-title {
  color: #172033;
  font-size: 18px;
  font-weight: 700;
  line-height: 1.3;
}

.editor-dialog-subtitle {
  color: #7a8699;
  font-size: 12px;
  margin-top: 6px;
}

.product-editor-dialog :deep(.el-dialog__body) {
  max-height: 76vh;
  overflow-y: auto;
  padding: 8px 32px 20px;
}

.editor-form :deep(.el-form-item) {
  margin-bottom: 20px;
}

.form-grid {
  display: grid;
  gap: 20px 24px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.form-grid > .col-span-2 {
  grid-column: 1 / -1;
}

.rule-group-card {
  background: #f8fafc;
  border: 1px solid #e5eaf1;
  border-radius: 8px;
  margin-bottom: 16px;
  padding: 18px 18px 16px;
}

.rule-group-fields {
  gap: 16px 20px;
}

.rule-items-list {
  border-left: 2px solid #dbe5f2;
  display: grid;
  gap: 10px;
  margin: 4px 0 0 4px;
  padding-left: 16px;
}

.rule-items-list > div {
  align-items: center;
  display: flex;
  gap: 10px;
  min-width: 0;
}

.rule-items-list :deep(.el-select) {
  flex: 1;
  min-width: 0;
}

.editor-dialog-footer {
  display: flex;
  gap: 10px;
  justify-content: flex-end;
}

@media (max-width: 760px) {
  .catalog-page {
    padding: 16px;
  }

  .catalog-pagination {
    align-items: flex-start;
    flex-direction: column;
    gap: 12px;
  }

  .product-editor-dialog :deep(.el-dialog__body) {
    padding: 8px 18px 20px;
  }

  .form-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .form-grid > .col-span-2 {
    grid-column: auto;
  }

  .rule-items-list > div {
    align-items: stretch;
    flex-wrap: wrap;
  }
}
</style>
