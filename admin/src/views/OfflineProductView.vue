<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
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
    <div class="mb-4 flex gap-4">
      <el-input v-model="searchQuery" placeholder="搜索门票名称..." class="w-64" prefix-icon="Search" />
      <el-select v-model="filterStatus" placeholder="状态" class="w-32">
        <el-option label="全部" value="" />
        <el-option label="上架中" value="online" />
        <el-option label="已下架" value="offline" />
      </el-select>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading" border>
      <el-table-column prop="id" label="编号" width="70" align="center" />
      <el-table-column prop="name" label="门票名称" min-width="180">
        <template #default="{ row }">
          <div class="font-medium">{{ row.name }}</div>
          <el-tag size="small" type="info" class="mt-1">窗口专用</el-tag>
        </template>
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
      <el-table-column v-if="canWrite" label="操作" width="200" fixed="right" align="center">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
          <el-button 
            link 
            :type="row.status === 'online' ? 'danger' : 'success'" 
            size="small" 
            @click="handleToggleStatus(row)"
          >
            {{ row.status === 'online' ? '下架' : '上架' }}
          </el-button>
           <el-button link type="danger" size="small" @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑窗口票' : '发布窗口票'"
      width="800px"
      top="5vh"
      destroy-on-close
    >
      <el-form :model="form" label-width="100px" :rules="rules" ref="formRef">
        
        <el-divider content-position="left">基础信息</el-divider>
        <div class="grid grid-cols-2 gap-4">
          <el-form-item label="门票名称" prop="product.name" class="col-span-2">
            <el-input v-model="form.product.name" placeholder="例如：成人全天通票(窗口)" />
          </el-form-item>
          <el-form-item label="销售价格" prop="product.price">
            <el-input-number v-model="form.product.price" :precision="2" :step="1" :min="0" class="w-full" />
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
          
          <div class="bg-slate-50 p-4 rounded-lg mb-4 border border-slate-200" v-for="(group, gIdx) in form.rule.groups" :key="gIdx">
            <div class="flex justify-between items-center mb-2">
              <div class="flex items-center gap-2">
                <span class="font-bold text-sm text-slate-700">规则组 #{{ gIdx + 1 }}</span>
                <el-tag size="small" effect="plain">{{ group.max_total_check_in === 0 ? '全选模式' : `M选${group.max_total_check_in}` }}</el-tag>
              </div>
              <el-button type="danger" link size="small" @click="removeGroup(gIdx)" v-if="form.rule.groups.length > 1">删除组</el-button>
            </div>
            
            <div class="grid grid-cols-2 gap-4 mb-2">
              <el-form-item label="分组名称" label-width="80px" :required="true" class="mb-0">
                <el-input v-model="group.group_name" placeholder="如：大门票、剧场票" />
              </el-form-item>
              <el-form-item label="可选点位数(M)" label-width="120px" class="mb-0">
                <el-input-number v-model="group.max_total_check_in" :min="0" placeholder="0为不限" />
                <div class="text-xs text-gray-400 mt-1">0 = 全选, N = M选N</div>
              </el-form-item>
            </div>

            <div class="space-y-2 pl-4 border-l-2 border-slate-200 mt-2">
              <div v-for="(item, iIdx) in group.items" :key="iIdx" class="flex items-center gap-2">
                <el-select v-model="item.check_point_id" placeholder="选择检票点" class="flex-1">
                  <el-option 
                    v-for="cp in checkpoints" 
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
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleSubmit" :loading="submitting">保存并发布</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { Plus, Minus } from '@element-plus/icons-vue'
import { hasPermission } from '@/utils/permissions'

const currentUser = (() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })()
const canWrite = hasPermission(currentUser, 'catalog.write')

const loading = ref(false)
const submitting = ref(false)
const tableData = ref([])
const checkpoints = ref<any[]>([])
const dialogVisible = ref(false)
const isEdit = ref(false)
const searchQuery = ref('')
const filterStatus = ref('')
const formRef = ref()

const form = reactive({
  id: 0,
  product: {
    name: '',
    price: 0,
    settlement_price: 0,
    type: 'offline', // Force Offline
    status: 'online',
    code_mode: 'ticket', // Force Ticket Mode
    validity_type: 'days', // Force Days
    validity_days: 0,
    stock_type: 'unlimited', // Force Unlimited
    refund_type: 'no_refund', // Force No Refund
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
  'product.price': [{ required: true, message: '请输入售价', trigger: 'blur' }]
}

const fetchCheckPoints = async () => {
  try {
    const res = await request.get('/checkpoints', { params: { page_size: 100 } })
    checkpoints.value = res.data.data
  } catch (e) { console.error(e) }
}

const fetchData = async () => {
  loading.value = true
  try {
    // Filter by type=offline
    const res = await request.get('/products', { params: { type: 'offline', page_size: 100 } })
    tableData.value = res.data.data
  } catch (error) {
    ElMessage.error('获取数据失败')
  } finally {
    loading.value = false
  }
}

const validityDateRange = ref<[string, string] | null>(null)
const productTags = ref<string[]>([])

const handleAdd = () => {
  isEdit.value = false
  form.id = 0
  form.product.name = ''
  form.product.price = 0
  form.product.settlement_price = 0
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
      } catch (error) {
        ElMessage.error('操作失败')
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

onMounted(() => {
  fetchData()
  fetchCheckPoints()
})
</script>
