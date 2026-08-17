<template>
  <div class="catalog-page bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <header class="page-heading">
      <div class="page-heading-copy">
        <h2 class="text-lg font-bold text-gray-900">线上门票管理</h2>
        <p class="text-xs text-gray-500 mt-1">管理外部销售渠道和微商城销售的票务产品</p>
      </div>
      <div class="page-actions">
        <el-button v-if="canWrite" type="primary" :icon="Plus" @click="handleAdd">发布新门票</el-button>
      </div>
    </header>

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
          <div class="flex gap-1 mt-1">
            <el-tag size="small" type="info" v-if="row.code_mode === 'order'">一单一码</el-tag>
            <el-tag size="small" type="warning" v-else>一票一码</el-tag>
            <el-tag size="small" v-if="row.real_name_required">实名</el-tag>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="所属景区" min-width="150">
        <template #default="{ row }">{{ scenicAreaName(row.scenic_area_id) }}</template>
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
            <div v-if="row.validity_type === 'date'">指定日期</div>
            <div v-else>购买后 {{ row.validity_days }} 天有效</div>
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
      <template #empty><el-empty description="没有匹配的线上票种" :image-size="72" /></template>
    </el-table>

    <div class="table-footer catalog-pagination">
      <span class="table-caption">共 {{ total }} 个线上票种</span>
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
      :title="isEdit ? '编辑门票' : '发布新门票'"
      width="min(980px, calc(100vw - 32px))"
      top="4vh"
      class="product-editor-dialog"
      :close-on-click-modal="false"
      destroy-on-close
    >
      <template #header>
        <div class="editor-dialog-header">
          <div>
            <div class="editor-dialog-title">{{ isEdit ? '编辑线上票种' : '发布线上票种' }}</div>
            <div class="editor-dialog-subtitle">先完善票种事实，再保存为当前租户的未上架产品。</div>
          </div>
          <el-tag size="small" effect="plain" type="info">线上票</el-tag>
        </div>
      </template>
      <el-tabs v-model="activeTab" class="product-editor-tabs">
        <!-- Tab 1: 基础信息 -->
        <el-tab-pane label="基础信息" name="basic">
          <el-form :model="form" label-width="100px" :rules="rules" ref="formRefBasic" class="editor-form">
            <div class="form-section form-grid">
              <el-form-item label="门票名称" prop="product.name" class="col-span-2">
                <el-input v-model="form.product.name" placeholder="例如：成人全天通票" />
              </el-form-item>
              <el-form-item label="所属景区" prop="product.scenic_area_id" class="col-span-2">
                <el-select v-model="form.product.scenic_area_id" aria-label="所属景区" placeholder="请选择门票所属景区" class="w-full" @change="handleScenicAreaChange">
                  <el-option v-for="area in scenicAreaOptions" :key="area.id" :label="area.status === 'active' ? area.name : `${area.name}（已停用）`" :value="area.id" />
                </el-select>
                <div class="text-xs text-gray-400 mt-1">切换景区会清空已选检票点；已售门票仍按售票时的景区核销。</div>
              </el-form-item>
              <el-form-item label="销售价格" prop="product.price" class="price-field">
                <el-input-number v-model="form.product.price" :precision="2" :step="1" :min="0" class="w-full" />
              </el-form-item>
              <el-form-item label="结算价格" prop="product.settlement_price" class="price-field">
                <el-input-number v-model="form.product.settlement_price" :precision="2" :step="1" :min="0" class="w-full" />
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
              
               <el-form-item label="供应商分销" prop="product.is_distributable" class="form-item-wide">
                  <el-switch v-model="form.product.is_distributable" active-text="允许分销商代理销售" />
                  <div class="text-xs text-gray-400 mt-1" v-if="form.product.is_distributable">
                    开启后，已建立合作关系的分销商可在其后台看到并代理此产品。结算价为 ¥{{ form.product.settlement_price }}。
                  </div>
               </el-form-item>

              <!-- Type is implicitly 'online' -->

                <el-form-item label="发码模式" prop="product.code_mode" class="form-item-wide">
                  <el-radio-group v-model="form.product.code_mode">
                    <el-radio label="order" border>一单一码 (全家一张)</el-radio>
                    <el-radio label="ticket" border>一票一码 (一人一张)</el-radio>
                  </el-radio-group>
                </el-form-item>

                <el-form-item label="闸机本地语音" prop="product.gate_voice_code" class="form-item-wide">
                  <el-select
                    v-model="form.product.gate_voice_code"
                    filterable
                    allow-create
                    default-first-option
                    placeholder="选择预置语音，或输入闸机已安装的音频编号"
                    class="w-full"
                  >
                    <el-option label="欢迎光临" value="welcome" />
                    <el-option label="成人票请通行" value="adult_ticket" />
                    <el-option label="儿童票请通行" value="child_ticket" />
                    <el-option label="团队票请通行" value="team_ticket" />
                  </el-select>
                  <div class="text-xs text-gray-400 mt-1">闸机程序从本机播放该编号对应的音频；已售门票保留售票时的编号。</div>
                </el-form-item>
              
            </div>
          </el-form>
        </el-tab-pane>

        <!-- Tab 2: 有效期与库存 -->
        <el-tab-pane label="有效期 & 库存" name="stock">
          <el-form :model="form" label-width="120px">
            <div class="editor-section-heading"><strong>有效期设置</strong><span>决定游客何时可以使用这张票</span></div>
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

            <div class="editor-section-heading"><strong>库存与分时</strong><span>控制每日库存和可选时段</span></div>
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
          <el-form :model="form" label-width="100px" class="editor-form">
            <div class="rule-editor-intro">
              <div class="rule-editor-intro-copy">
                <div class="rule-editor-intro-icon"><el-icon><InfoFilled /></el-icon></div>
                <div>
                  <strong>设置核销路径</strong>
                  <p>每个规则组描述一条可用的通行路径；组内可设置全部点位通行，或任选指定数量的点位。</p>
                </div>
              </div>
              <div class="rule-editor-stats" aria-label="规则统计">
                <span><strong>{{ ruleGroupCount }}</strong><small>规则组</small></span>
                <span><strong>{{ ruleCheckpointCount }}</strong><small>检票点</small></span>
              </div>
            </div>

            <div class="rule-group-stack">
              <div class="rule-group-card" v-for="(group, gIdx) in form.rule.groups" :key="gIdx">
                <div class="rule-group-header">
                  <div class="rule-group-title">
                    <span class="rule-group-index">{{ formatRuleIndex(gIdx) }}</span>
                    <div class="rule-group-title-copy">
                      <div class="rule-group-name">规则组 {{ gIdx + 1 }}</div>
                      <div class="rule-group-subtitle">{{ group.group_name || '未命名规则组' }}</div>
                    </div>
                    <el-tag size="small" effect="plain" type="info">
                      {{ group.max_total_check_in === 0 ? '全部点位' : `任选 ${group.max_total_check_in} 个` }}
                    </el-tag>
                  </div>
                  <el-button
                    v-if="form.rule.groups.length > 1"
                    type="danger"
                    link
                    size="small"
                    class="delete-group-button"
                    @click="removeGroup(gIdx)"
                  >
                    <el-icon><Delete /></el-icon>
                    删除组
                  </el-button>
                </div>

                <div class="rule-group-settings">
                  <div class="rule-setting">
                    <label>分组名称 <span>*</span></label>
                    <el-input v-model="group.group_name" placeholder="如：大门票、剧场票" />
                    <small>用于区分不同的核销路径。</small>
                  </div>
                  <div class="rule-setting rule-setting-mode">
                    <label>组内可选点位数</label>
                    <div class="rule-mode-control">
                      <el-input-number v-model="group.max_total_check_in" :min="0" controls-position="right" placeholder="0为全部" />
                      <span>{{ group.max_total_check_in === 0 ? '全部点位均可通行' : `任选 ${group.max_total_check_in} 个点位` }}</span>
                    </div>
                    <small>填 0 表示组内所有点位都可通行。</small>
                  </div>
                </div>

                <div class="rule-items-section">
                  <div class="rule-items-heading">
                    <div>
                      <strong>检票点配置</strong>
                      <span>设置每个点位可通行的次数</span>
                    </div>
                    <span class="rule-items-count">{{ group.items.length }} 个点位</span>
                  </div>
                  <div class="rule-items-list">
                    <div v-if="group.items.length === 0" class="rule-items-empty">暂未添加检票点，请先添加一个点位。</div>
                    <div v-for="(item, iIdx) in group.items" :key="iIdx" class="rule-item-row">
                      <span class="rule-item-index">{{ formatRuleIndex(iIdx) }}</span>
                      <el-select v-model="item.check_point_id" aria-label="检票点" :placeholder="form.product.scenic_area_id ? '选择检票点' : '请先选择所属景区'" :disabled="!form.product.scenic_area_id">
                        <el-option
                          v-for="cp in filteredCheckpoints"
                          :key="cp.id"
                          :label="cp.name"
                          :value="cp.id"
                          :disabled="isCheckPointDisabled(cp.id, item.check_point_id)"
                        />
                      </el-select>
                      <div class="rule-item-limit">
                        <el-input-number v-model="item.max_per_check_in" :min="1" controls-position="right" placeholder="次数" />
                        <span>次</span>
                      </div>
                      <el-button circle size="small" type="danger" plain aria-label="删除检票点" title="删除检票点" @click="removeItem(gIdx, iIdx)">
                        <el-icon><Minus /></el-icon>
                      </el-button>
                    </div>
                    <el-button type="primary" link size="small" class="add-rule-item" @click="addItem(gIdx)">
                      <el-icon><Plus /></el-icon>
                      添加检票点
                    </el-button>
                  </div>
                </div>
              </div>
            </div>

            <el-button type="primary" plain class="add-rule-group" @click="addGroup">
              <el-icon><Plus /></el-icon>
              添加新规则组
            </el-button>
          </el-form>
        </el-tab-pane>

        <!-- Tab 4: 购买限制 -->
        <el-tab-pane label="购买限制" name="limit">
          <el-form :model="form" label-width="120px" class="editor-form">
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
          <el-form :model="form" label-width="120px" class="editor-form">
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
import { Delete, InfoFilled, Minus, Plus, Refresh } from '@element-plus/icons-vue'
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
const productTags = ref<string[]>([])

// Complex Form Structure
const form = reactive({
  id: 0,
  product: {
    name: '',
    scenic_area_id: null as number | null,
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
    refund_rule: '', // JSON string
    tags: '', // JSON string
    is_distributable: false,
    gate_voice_code: 'welcome'
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
  'product.settlement_price': [{ required: true, message: '请输入结算价', trigger: 'blur' }],
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
  } catch (e) {
    console.error(e)
  }
}

const activeScenicAreas = computed(() => scenicAreas.value.filter(area => area.status === 'active'))
const scenicAreaOptions = computed(() => scenicAreas.value.filter(area => area.status === 'active' || area.id === form.product.scenic_area_id))
const filteredCheckpoints = computed(() => checkpoints.value.filter(cp => cp.scenic_area_id === form.product.scenic_area_id))
const ruleGroupCount = computed(() => form.rule.groups.length)
const ruleCheckpointCount = computed(() => form.rule.groups.reduce((total, group) => total + group.items.length, 0))
const scenicAreaName = (id: number) => scenicAreas.value.find(area => area.id === id)?.name || '未归属'

const formatRuleIndex = (index: number) => String(index + 1).padStart(2, '0')

const fetchData = async () => {
  loading.value = true
  try {
    const res = await request.get('/products', {
      params: {
        type: 'online',
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

const handleAdd = () => {
  isEdit.value = false
  activeTab.value = 'basic'
  
  // Reset Form
  form.id = 0
  form.product = { 
    name: '', scenic_area_id: activeScenicAreas.value.length === 1 ? activeScenicAreas.value[0].id : null,
    price: 0, settlement_price: 0, type: 'online', status: 'online', code_mode: 'order',
    validity_type: 'date', validity_days: 0, validity_start_date: null, validity_end_date: null,
    stock_type: 'unlimited', daily_stock: 0, time_slot_config: '',
    real_name_required: false, region_limit: '', limit_per_phone: 0, limit_per_id: 0,
    refund_type: 'no_refund', refund_rule: '', tags: '', is_distributable: false,
    gate_voice_code: 'welcome'
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
  productTags.value = []
  
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

  try {
    productTags.value = data.tags ? JSON.parse(data.tags) : []
  } catch(e) { productTags.value = [] }

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

const handleScenicAreaChange = () => {
  for (const group of form.rule.groups) {
    for (const item of group.items) item.check_point_id = null
  }
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确认删除该门票吗？', '警告', {
    confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning',
  }).then(async () => {
    try {
      await request.delete(`/products/${row.id}`)
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
      await request.patch(`/products/${row.id}/status`, { status: newStatus })
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
      form.product.tags = JSON.stringify(productTags.value)
      
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

.product-editor-tabs {
  min-height: 540px;
}

.product-editor-tabs :deep(.el-tabs__header) {
  background: #f7f9fc;
  border-bottom: 1px solid #e4eaf2;
  margin: 0;
  padding: 0 18px;
}

.product-editor-tabs :deep(.el-tabs__nav-wrap::after) {
  background-color: transparent;
}

.product-editor-tabs :deep(.el-tabs__item) {
  color: #667085;
  font-size: 13px;
  height: 48px;
  line-height: 48px;
  margin: 0 4px;
  padding: 0 14px;
  transition: color .2s ease, background-color .2s ease;
  white-space: nowrap;
}

.product-editor-tabs :deep(.el-tabs__item:hover) {
  background: #edf3ff;
  border-radius: 6px 6px 0 0;
  color: #2563eb;
}

.product-editor-tabs :deep(.el-tabs__item.is-active) {
  background: #edf3ff;
  border-radius: 6px 6px 0 0;
  color: #1d4ed8;
  font-weight: 600;
}

.product-editor-tabs :deep(.el-tabs__active-bar) {
  background-color: #2563eb;
  border-radius: 3px 3px 0 0;
  bottom: 0;
  height: 3px;
}

.product-editor-tabs :deep(.el-tabs__content) {
  box-sizing: border-box;
  height: 540px;
  overflow-y: auto;
  padding: 24px 32px 36px;
}

.editor-form :deep(.el-form-item) {
  margin-bottom: 20px;
}

.form-section,
.form-grid {
  display: grid;
  gap: 20px 24px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.form-section > .col-span-2,
.form-section > .form-item-wide,
.form-grid > .col-span-2,
.form-grid > .form-item-wide {
  grid-column: 1 / -1;
}

.price-field :deep(.el-input-number) {
  width: 100%;
}

.editor-section-heading {
  align-items: baseline;
  border-bottom: 1px solid #edf0f4;
  display: flex;
  gap: 12px;
  margin: 0 0 22px;
  padding-bottom: 10px;
}

.editor-section-heading strong {
  color: #172033;
  font-size: 14px;
}

.editor-section-heading span {
  color: #8a94a6;
  font-size: 12px;
}

.rule-editor-intro {
  align-items: center;
  background: #f7f9fc;
  border: 1px solid #e4eaf2;
  border-radius: 10px;
  display: flex;
  gap: 20px;
  justify-content: space-between;
  margin-bottom: 16px;
  padding: 14px 16px;
}

.rule-editor-intro-copy {
  align-items: flex-start;
  display: flex;
  gap: 10px;
  min-width: 0;
}

.rule-editor-intro-icon {
  align-items: center;
  background: #eaf1ff;
  border-radius: 7px;
  color: #2563eb;
  display: inline-flex;
  flex: 0 0 auto;
  height: 30px;
  justify-content: center;
  width: 30px;
}

.rule-editor-intro-copy strong {
  color: #172033;
  display: block;
  font-size: 13px;
  line-height: 20px;
}

.rule-editor-intro-copy p {
  color: #667085;
  font-size: 12px;
  line-height: 18px;
  margin: 2px 0 0;
}

.rule-editor-stats {
  background: #e4eaf2;
  border: 1px solid #e4eaf2;
  border-radius: 7px;
  display: flex;
  flex: 0 0 auto;
  gap: 1px;
  overflow: hidden;
}

.rule-editor-stats span {
  align-items: center;
  background: #fff;
  display: flex;
  flex-direction: column;
  min-width: 64px;
  padding: 6px 10px;
}

.rule-editor-stats strong {
  color: #172033;
  font-size: 16px;
  line-height: 20px;
}

.rule-editor-stats small {
  color: #8a94a6;
  font-size: 11px;
  line-height: 16px;
}

.rule-group-stack {
  display: grid;
  gap: 14px;
}

.rule-group-card {
  background: #fff;
  border: 1px solid #dfe5ee;
  border-radius: 10px;
  box-shadow: 0 2px 8px rgb(15 23 42 / 3%);
  padding: 16px;
}

.rule-group-header {
  align-items: center;
  border-bottom: 1px solid #edf0f4;
  display: flex;
  justify-content: space-between;
  margin-bottom: 14px;
  padding-bottom: 12px;
}

.rule-group-title {
  align-items: center;
  display: flex;
  gap: 10px;
  min-width: 0;
}

.rule-group-index,
.rule-item-index {
  align-items: center;
  background: #edf3ff;
  border-radius: 6px;
  color: #2563eb;
  display: inline-flex;
  flex: 0 0 auto;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  font-weight: 700;
  height: 28px;
  justify-content: center;
  width: 28px;
}

.rule-group-title-copy {
  min-width: 0;
}

.rule-group-name {
  color: #344054;
  font-size: 13px;
  font-weight: 700;
  line-height: 18px;
}

.rule-group-subtitle {
  color: #98a2b3;
  font-size: 11px;
  line-height: 16px;
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.delete-group-button {
  flex: 0 0 auto;
}

.rule-group-settings {
  display: grid;
  gap: 14px 24px;
  grid-template-columns: minmax(0, 1fr) minmax(280px, .9fr);
  margin-bottom: 16px;
}

.rule-setting label {
  color: #475467;
  display: block;
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
  margin-bottom: 6px;
}

.rule-setting label span {
  color: #ef4444;
  margin-left: 2px;
}

.rule-setting small {
  color: #98a2b3;
  display: block;
  font-size: 11px;
  line-height: 16px;
  margin-top: 5px;
}

.rule-setting-mode .rule-mode-control {
  align-items: center;
  display: flex;
  gap: 10px;
}

.rule-mode-control :deep(.el-input-number) {
  width: 142px;
}

.rule-mode-control span {
  color: #667085;
  font-size: 12px;
  white-space: nowrap;
}

.rule-items-section {
  background: #f8fafc;
  border: 1px solid #edf0f4;
  border-radius: 8px;
  padding: 12px;
}

.rule-items-heading {
  align-items: center;
  display: flex;
  justify-content: space-between;
  margin-bottom: 9px;
}

.rule-items-heading > div {
  align-items: baseline;
  display: flex;
  gap: 8px;
}

.rule-items-heading strong {
  color: #475467;
  font-size: 12px;
}

.rule-items-heading span {
  color: #98a2b3;
  font-size: 11px;
}

.rule-items-count {
  background: #fff;
  border: 1px solid #e4e7ec;
  border-radius: 5px;
  color: #667085 !important;
  padding: 2px 7px;
}

.rule-items-list {
  display: grid;
  gap: 8px;
}

.rule-item-row {
  align-items: center;
  display: grid;
  gap: 10px;
  grid-template-columns: 28px minmax(0, 1fr) 154px 32px;
  min-width: 0;
}

.rule-item-index {
  background: #fff;
  border: 1px solid #e4e7ec;
  color: #667085;
  height: 26px;
  width: 26px;
}

.rule-item-row :deep(.el-select) {
  min-width: 0;
  width: 100%;
}

.rule-item-limit {
  align-items: center;
  display: flex;
  gap: 7px;
}

.rule-item-limit :deep(.el-input-number) {
  width: 124px;
}

.rule-item-limit span {
  color: #667085;
  font-size: 12px;
}

.rule-items-empty {
  background: #fff;
  border: 1px dashed #d0d5dd;
  border-radius: 6px;
  color: #98a2b3;
  font-size: 12px;
  padding: 12px;
  text-align: center;
}

.add-rule-item {
  justify-self: start;
  margin-top: 2px;
}

.add-rule-group {
  border-style: dashed;
  height: 40px;
  margin-top: 14px;
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

  .product-editor-tabs :deep(.el-tabs__header) {
    overflow-x: auto;
    padding: 0 10px;
  }

  .product-editor-tabs :deep(.el-tabs__nav-prev),
  .product-editor-tabs :deep(.el-tabs__nav-next) {
    display: none;
  }

  .product-editor-tabs :deep(.el-tabs__nav-scroll) {
    overflow-x: auto;
    scrollbar-width: none;
  }

  .product-editor-tabs :deep(.el-tabs__nav-scroll::-webkit-scrollbar) {
    display: none;
  }

  .product-editor-tabs :deep(.el-tabs__nav) {
    min-width: max-content;
  }

  .product-editor-tabs :deep(.el-tabs__content) {
    padding: 22px 18px 28px;
  }

  .form-section,
  .form-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .form-section > .col-span-2,
  .form-section > .form-item-wide,
  .form-grid > .col-span-2,
  .form-grid > .form-item-wide {
    grid-column: auto;
  }

  .rule-editor-intro {
    align-items: flex-start;
    flex-direction: column;
  }

  .rule-editor-stats {
    width: 100%;
  }

  .rule-editor-stats span {
    flex: 1;
  }

  .rule-group-settings {
    grid-template-columns: minmax(0, 1fr);
  }

  .rule-item-row {
    grid-template-columns: 28px minmax(0, 1fr) 32px;
  }

  .rule-item-limit {
    grid-column: 2 / 3;
  }
}
</style>
