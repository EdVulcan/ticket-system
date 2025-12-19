<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <div>
        <h2 class="text-lg font-bold text-gray-900">线上门票管理</h2>
        <p class="text-xs text-gray-500 mt-1">管理线上销售渠道（OTA/微官网）的票务产品</p>
      </div>
      <el-button type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> 发布新门票
      </el-button>
    </div>

    <!-- Filter / Search (Placeholder) -->
    <div class="mb-4 flex gap-4">
      <el-input v-model="searchQuery" placeholder="搜索门票名称..." class="w-64" prefix-icon="Search" />
      <el-select v-model="filterStatus" placeholder="状态" class="w-32">
        <el-option label="全部" value="" />
        <el-option label="上架中" value="online" />
        <el-option label="已下架" value="offline" />
      </el-select>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading" border>
      <el-table-column prop="id" label="ID" width="60" align="center" />
      <el-table-column prop="name" label="门票名称" min-width="180">
        <template #default="{ row }">
          <div class="font-medium">{{ row.name }}</div>
          <div class="flex gap-1 mt-1">
            <el-tag size="small" type="info" v-if="row.code_mode === 'order'">一单一码</el-tag>
            <el-tag size="small" type="warning" v-else>一票一码</el-tag>
            <el-tag size="small" v-if="row.real_name_required">实名</el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="价格管理" width="150">
        <template #default="{ row }">
          <div class="text-sm">售价: <span class="font-bold text-orange-500">¥{{ row.price }}</span></div>
          <div class="text-xs text-gray-400">结算: ¥{{ row.settlement_price }}</div>
        </template>
      </el-table-column>
      <el-table-column label="有效期 & 库存" width="180">
        <template #default="{ row }">
          <div class="text-xs">
            <div v-if="row.validity_type === 'date'">📅 指定日期</div>
            <div v-else>⏳ 购买后 {{ row.validity_days }} 天有效</div>
            <div class="mt-1 text-gray-500">
              库存: {{ row.stock_type === 'unlimited' ? '不限' : (row.stock_type === 'daily' ? '日限 ' + row.daily_stock : '总限 ' + row.daily_stock) }}
            </div>
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
      <el-table-column label="操作" width="200" fixed="right" align="center">
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
      :title="isEdit ? '编辑门票' : '发布新门票'"
      width="900px"
      top="5vh"
      destroy-on-close
    >
      <el-tabs v-model="activeTab" class="demo-tabs">
        <!-- Tab 1: 基础信息 -->
        <el-tab-pane label="基础信息" name="basic">
          <el-form :model="form" label-width="100px" :rules="rules" ref="formRefBasic">
            <div class="grid grid-cols-2 gap-4">
              <el-form-item label="门票名称" prop="product.name" class="col-span-2">
                <el-input v-model="form.product.name" placeholder="例如：成人全天通票" />
              </el-form-item>
              <el-form-item label="销售价格" prop="product.price">
                <el-input-number v-model="form.product.price" :precision="2" :step="1" :min="0" class="w-full" />
              </el-form-item>
              <el-form-item label="结算价格" prop="product.settlement_price">
                <el-input-number v-model="form.product.settlement_price" :precision="2" :step="1" :min="0" class="w-full" />
              </el-form-item>
              <el-form-item label="产品类型" prop="product.type">
                 <el-radio-group v-model="form.product.type" @change="handleTypeChange">
                   <el-radio label="online" border>线上票 (小程序/OTA)</el-radio>
                   <el-radio label="offline" border>窗口票 (线下售卖)</el-radio>
                 </el-radio-group>
              </el-form-item>

              <!-- Online Only Fields -->
              <template v-if="form.product.type === 'online'">
                <el-form-item label="发码模式" prop="product.code_mode">
                  <el-radio-group v-model="form.product.code_mode">
                    <el-radio label="order" border>一单一码 (全家一张)</el-radio>
                    <el-radio label="ticket" border>一票一码 (一人一张)</el-radio>
                  </el-radio-group>
                </el-form-item>
              </template>
              
              <!-- Offline Hint -->
              <div v-else class="ml-[100px] text-gray-400 text-sm mb-4">
                * 窗口票默认强制为“一票一码”模式，且仅支持“当日有效”或“现场激活”。
              </div>
            </div>
          </el-form>
        </el-tab-pane>

        <!-- Tab 2: 有效期与库存 -->
        <el-tab-pane label="有效期 & 库存" name="stock">
          <el-form :model="form" label-width="120px">
            <el-divider content-position="left">有效期设置</el-divider>
            <el-form-item label="有效期类型">
              <el-radio-group v-model="form.product.validity_type">
                <el-radio label="date">指定日期范围</el-radio>
                <el-radio label="days">购买后N天有效</el-radio>
              </el-radio-group>
            </el-form-item>
            
            <div v-if="form.product.validity_type === 'date'">
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
            <div v-else>
              <el-form-item label="有效天数">
                <el-input-number v-model="form.product.validity_days" :min="0" />
                <span class="ml-2 text-gray-400">0表示当天有效，1表示次日有效，以此类推</span>
              </el-form-item>
            </div>

            <el-divider content-position="left">库存与分时</el-divider>
            <el-form-item label="库存模式">
              <el-radio-group v-model="form.product.stock_type">
                <el-radio label="unlimited">不限库存</el-radio>
                <el-radio label="daily">每日库存</el-radio>
                <el-radio label="total">总库存</el-radio>
              </el-radio-group>
            </el-form-item>
            
            <div v-if="form.product.stock_type !== 'unlimited'">
              <el-form-item label="库存数量">
                <el-input-number v-model="form.product.daily_stock" :min="0" />
              </el-form-item>
            </div>

            <el-form-item label="分时预约">
               <el-switch v-model="enableTimeSlot" active-text="开启分时段库存控制" />
            </el-form-item>
            
            <div v-if="enableTimeSlot" class="bg-gray-50 p-4 rounded mb-4 ml-10">
               <div v-for="(slot, idx) in timeSlots" :key="idx" class="flex gap-2 mb-2 items-center">
                 <el-time-picker v-model="slot.start" placeholder="开始" value-format="HH:mm" style="width: 120px"/>
                 <span>-</span>
                 <el-time-picker v-model="slot.end" placeholder="结束" value-format="HH:mm" style="width: 120px"/>
                 <el-input-number v-model="slot.stock" placeholder="库存" :min="0" style="width: 120px" />
                 <el-button circle type="danger" icon="Minus" @click="timeSlots.splice(idx, 1)" />
               </div>
               <el-button type="primary" link icon="Plus" @click="timeSlots.push({start: '08:00', end: '12:00', stock: 100})">添加时段</el-button>
            </div>
          </el-form>
        </el-tab-pane>

        <!-- Tab 3: 核销规则 (M选N) -->
        <el-tab-pane label="核销规则 (M选N)" name="rule">
          <el-form :model="form" label-width="100px">
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
                <el-form-item label="分组名称" label-width="80px" :required="true">
                  <el-input v-model="group.group_name" placeholder="如：大门票、剧场票" />
                </el-form-item>
                <el-form-item label="可选点位数(M)" label-width="120px">
                  <el-input-number v-model="group.max_total_check_in" :min="0" placeholder="0为不限" />
                  <div class="text-xs text-gray-400 mt-1">0 = 组内所有点位都可通行 (全选)<br>N = 组内任选 N 个点位通行 (M选N)</div>
                </el-form-item>
              </div>

              <div class="space-y-2 pl-4 border-l-2 border-slate-200">
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
          </el-form>
        </el-tab-pane>

        <!-- Tab 4: 购买限制 -->
        <el-tab-pane label="购买限制" name="limit">
          <el-form :model="form" label-width="120px">
            <el-form-item label="实名制要求">
              <el-switch v-model="form.product.real_name_required" active-text="强制实名 (需输入姓名+身份证)" />
            </el-form-item>
            
            <el-form-item label="限购数量">
              <div class="flex gap-4">
                <el-input-number v-model="form.product.limit_per_phone" :min="0" placeholder="0不限" />
                <span class="text-gray-500">张/手机号</span>
              </div>
              <div class="flex gap-4 mt-2">
                <el-input-number v-model="form.product.limit_per_id" :min="0" placeholder="0不限" />
                <span class="text-gray-500">张/身份证</span>
              </div>
            </el-form-item>

            <el-form-item label="地区限制">
               <el-select v-model="regionLimits" multiple placeholder="选择限制地区 (身份证前6位)" allow-create filterable default-first-option class="w-full">
                 <el-option label="杭州 (3301)" value="3301" />
                 <el-option label="北京 (1101)" value="1101" />
                 <el-option label="上海 (3101)" value="3101" />
               </el-select>
               <span class="text-xs text-gray-400">输入身份证前6位行政区划代码，限制仅这些地区可购买</span>
            </el-form-item>
          </el-form>
        </el-tab-pane>

        <!-- Tab 5: 退改规则 -->
        <el-tab-pane label="退改规则" name="refund">
          <el-form :model="form" label-width="120px">
            <el-form-item label="退票类型">
              <el-radio-group v-model="form.product.refund_type">
                <el-radio label="no_refund" border>不可退</el-radio>
                <el-radio label="free" border>未核销可退 (随时退)</el-radio>
              </el-radio-group>
            </el-form-item>
            
            <div class="text-gray-400 text-xs ml-[120px]">
              <p v-if="form.product.refund_type === 'no_refund'">订单支付后概不退款。</p>
              <p v-else>游客在未使用（未核销）的情况下可随时申请退款，系统自动全额退款。</p>
            </div>
          </el-form>
        </el-tab-pane>
      </el-tabs>

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
import { ref, reactive, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'
import { Plus, Minus, Search, Monitor, Location, Ticket } from '@element-plus/icons-vue'

const API_URL = 'http://localhost:8080/api/v1/products'
const CP_API_URL = 'http://localhost:8080/api/v1/checkpoints'

const loading = ref(false)
const submitting = ref(false)
const tableData = ref([])
const checkpoints = ref<any[]>([])
const dialogVisible = ref(false)
const isEdit = ref(false)
const activeTab = ref('basic')
const searchQuery = ref('')
const filterStatus = ref('')
const formRefBasic = ref()

// UI Helper States
const validityDateRange = ref<[string, string] | null>(null)
const enableTimeSlot = ref(false)
const timeSlots = ref<any[]>([])
const regionLimits = ref<string[]>([])
const refundRules = ref<any[]>([])

// Complex Form Structure
const form = reactive({
  id: 0,
  product: {
    name: '',
    price: 0,
    settlement_price: 0,
    type: 'online',
    status: 'online',
    code_mode: 'order',
    validity_type: 'date',
    validity_days: 0,
    validity_start_date: null as string | null,
    validity_end_date: null as string | null,
    stock_type: 'unlimited',
    daily_stock: 0,
    time_slot_config: '', // JSON string
    real_name_required: false,
    region_limit: '', // JSON string
    limit_per_phone: 0,
    limit_per_id: 0,
    refund_type: 'no_refund',
    refund_rule: '' // JSON string
  },
  rule: {
    name: '',
    validity_type: 'date',
    groups: [
      {
        group_name: '默认分组',
        max_total_check_in: 1,
        items: [
          { check_point_id: null, max_per_check_in: 1 }
        ]
      }
    ]
  }
})

const rules = {
  'product.name': [{ required: true, message: '请输入门票名称', trigger: 'blur' }],
  'product.price': [{ required: true, message: '请输入售价', trigger: 'blur' }],
  'product.settlement_price': [{ required: true, message: '请输入结算价', trigger: 'blur' }]
}

const fetchCheckPoints = async () => {
  try {
    const res = await axios.get(CP_API_URL, { params: { page_size: 100 } })
    checkpoints.value = res.data.data
  } catch (e) {
    console.error(e)
  }
}

const fetchData = async () => {
  loading.value = true
  try {
    const res = await axios.get(API_URL)
    tableData.value = res.data.data
  } catch (error) {
    ElMessage.error('获取数据失败')
  } finally {
    loading.value = false
  }
}

const handleAdd = () => {
  isEdit.value = false
  activeTab.value = 'basic'
  
  // Reset Form
  form.id = 0
  form.product = { 
    name: '', price: 0, settlement_price: 0, type: 'online', status: 'online', code_mode: 'order',
    validity_type: 'date', validity_days: 0, validity_start_date: null, validity_end_date: null,
    stock_type: 'unlimited', daily_stock: 0, time_slot_config: '',
    real_name_required: false, region_limit: '', limit_per_phone: 0, limit_per_id: 0,
    refund_type: 'no_refund', refund_rule: ''
  }
  form.rule = {
    name: '', validity_type: 'date',
    groups: [{ group_name: '默认分组', max_total_check_in: 1, items: [{ check_point_id: null, max_per_check_in: 1 }] }]
  }
  
  // Reset UI Helpers
  validityDateRange.value = null
  enableTimeSlot.value = false
  timeSlots.value = []
  regionLimits.value = []
  refundRules.value = []
  
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  activeTab.value = 'basic'
  
  // Deep copy
  const data = JSON.parse(JSON.stringify(row))
  form.id = data.id
  form.product = { ...data } // Copy all fields
  form.rule = data.rule || {
    name: data.name, validity_type: 'date',
    groups: [{ group_name: '默认分组', max_total_check_in: 1, items: [{ check_point_id: null, max_per_check_in: 1 }] }]
  }
  
  // Restore UI Helpers
  if (data.validity_start_date && data.validity_end_date) {
    validityDateRange.value = [data.validity_start_date.split('T')[0], data.validity_end_date.split('T')[0]]
  } else {
    validityDateRange.value = null
  }
  
  try {
    const tsConfig = data.time_slot_config ? JSON.parse(data.time_slot_config) : []
    timeSlots.value = tsConfig
    enableTimeSlot.value = tsConfig.length > 0
  } catch(e) { timeSlots.value = [] }

  try {
    regionLimits.value = data.region_limit ? JSON.parse(data.region_limit) : []
  } catch(e) { regionLimits.value = [] }

  try {
    refundRules.value = data.refund_rule ? JSON.parse(data.refund_rule) : []
  } catch(e) { refundRules.value = [] }

  // Ensure groups and items are arrays
  if (form.rule.groups) {
    form.rule.groups.forEach((g: any) => {
      if (!g.items) g.items = []
    })
  } else {
    form.rule.groups = []
  }

  dialogVisible.value = true
}

const addGroup = () => {
  form.rule.groups.push({
    group_name: '',
    max_total_check_in: 1,
    items: [{ check_point_id: null, max_per_check_in: 1 }]
  })
}

const removeGroup = (idx: number) => {
  form.rule.groups.splice(idx, 1)
}

const addItem = (gIdx: number) => {
  form.rule.groups[gIdx].items.push({ check_point_id: null, max_per_check_in: 1 })
}

const removeItem = (gIdx: number, iIdx: number) => {
  form.rule.groups[gIdx].items.splice(iIdx, 1)
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确认删除该门票吗？', '警告', {
    confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning',
  }).then(async () => {
    try {
      await axios.delete(`${API_URL}/${row.id}`)
      ElMessage.success('删除成功')
      fetchData()
    } catch (error) {
      ElMessage.error('操作失败')
    }
  })
}

const handleToggleStatus = (row: any) => {
  const newStatus = row.status === 'online' ? 'offline' : 'online'
  const actionText = newStatus === 'online' ? '上架' : '下架'
  ElMessageBox.confirm(`确认${actionText}该门票吗？`, '提示', {
    confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning',
  }).then(async () => {
    try {
      await axios.patch(`${API_URL}/${row.id}/status`, { status: newStatus })
      ElMessage.success(`${actionText}成功`)
      fetchData()
    } catch (error) {
      ElMessage.error('操作失败')
    }
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

const handleTypeChange = (val: string) => {
  if (val === 'offline') {
    // Force defaults for Offline Ticket
    form.product.code_mode = 'ticket'
    form.product.validity_type = 'days' // Simplified to N days (usually 0 or 1)
    form.product.validity_days = 0 // Default today
    form.product.stock_type = 'unlimited'
    form.product.refund_type = 'no_refund'
  }
}

const handleSubmit = async () => {
  if (!formRefBasic.value) return
  await formRefBasic.value.validate(async (valid: boolean) => {
    if (valid) {
      submitting.value = true
      
      // 1. Prepare Data
      // Validity
      if (form.product.validity_type === 'date' && validityDateRange.value) {
        form.product.validity_start_date = validityDateRange.value[0] + 'T00:00:00Z'
        form.product.validity_end_date = validityDateRange.value[1] + 'T23:59:59Z'
      } else {
        form.product.validity_start_date = null
        form.product.validity_end_date = null
      }
      
      // JSON Fields
      form.product.time_slot_config = enableTimeSlot.value ? JSON.stringify(timeSlots.value) : ''
      form.product.region_limit = JSON.stringify(regionLimits.value)
      form.product.refund_rule = JSON.stringify(refundRules.value)
      
      // Sync rule name
      if (!form.rule.name) form.rule.name = form.product.name

      // 2. Validation
      for (const group of form.rule.groups) {
        if (group.max_total_check_in > 0) {
          if (group.max_total_check_in > group.items.length) {
             ElMessage.error(`规则组 "${group.group_name}" 的可选点位数(M)不能大于该组包含的检票点总数(N)`)
             submitting.value = false
             return
          }
        }
      }

      // 3. Submit
      try {
        if (isEdit.value) {
           await axios.put(`${API_URL}/${form.id}`, form)
           ElMessage.success('更新成功')
        } else {
           await axios.post(API_URL, form)
           ElMessage.success('发布成功')
        }
        dialogVisible.value = false
        fetchData()
      } catch (error) {
        ElMessage.error('操作失败: ' + (error as any).response?.data?.error || '未知错误')
      } finally {
        submitting.value = false
      }
    }
  })
}

onMounted(() => {
  fetchData()
  fetchCheckPoints()
})
</script>
