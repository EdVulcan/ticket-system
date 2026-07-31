<template>
  <div class="h-screen flex bg-[#141414] text-white font-sans overflow-hidden">
    <!-- Sidebar -->
    <div class="w-[80px] bg-[#001529] flex flex-col items-center pt-5 border-r border-[#333] z-20">
      <div class="mb-6 w-10 h-10 bg-blue-600 rounded-lg flex items-center justify-center font-bold text-xl">P</div>
      
      <div 
        class="w-[50px] h-[50px] mb-5 rounded-xl flex flex-col items-center justify-center text-[#8c8c8c] cursor-pointer transition-all hover:text-white hover:bg-white/10"
        :class="{ 'bg-[#1890ff] text-white shadow-lg shadow-blue-500/40': currentView === 'pos' }"
        @click="currentView = 'pos'"
      >
        <el-icon :size="24"><Monitor /></el-icon>
        <span class="text-[10px] mt-0.5">收银</span>
      </div>

      <div 
        class="w-[50px] h-[50px] mb-5 rounded-xl flex flex-col items-center justify-center text-[#8c8c8c] cursor-pointer transition-all hover:text-white hover:bg-white/10"
        :class="{ 'bg-[#1890ff] text-white shadow-lg shadow-blue-500/40': currentView === 'orders' }"
        @click="currentView = 'orders'"
      >
        <el-icon :size="24"><List /></el-icon>
        <span class="text-[10px] mt-0.5">订单</span>
      </div>

      <div 
        class="w-[50px] h-[50px] mb-5 rounded-xl flex flex-col items-center justify-center text-[#8c8c8c] cursor-pointer transition-all hover:text-white hover:bg-white/10"
        :class="{ 'bg-[#1890ff] text-white shadow-lg shadow-blue-500/40': currentView === 'verify' }"
        @click="currentView = 'verify'"
      >
        <el-icon :size="24"><Checked /></el-icon>
        <span class="text-[10px] mt-0.5">核销</span>
      </div>

      <div 
        class="w-[50px] h-[50px] mb-5 rounded-xl flex flex-col items-center justify-center text-[#8c8c8c] cursor-pointer transition-all hover:text-white hover:bg-white/10"
        :class="{ 'bg-[#1890ff] text-white shadow-lg shadow-blue-500/40': currentView === 'settings' }"
        @click="currentView = 'settings'"
      >
        <el-icon :size="24"><Setting /></el-icon>
        <span class="text-[10px] mt-0.5">设置</span>
      </div>

      <div class="mt-auto mb-6 w-[50px] h-[50px] rounded-xl flex flex-col items-center justify-center text-red-400 cursor-pointer hover:bg-red-900/20" @click="handleLogout">
        <el-icon :size="24"><SwitchButton /></el-icon>
      </div>
    </div>

    <!-- Main Content -->
    <div class="flex-1 flex flex-col overflow-hidden relative">
      <!-- Status Bar -->
      <div class="h-10 bg-[#1f1f1f] border-b border-[#303030] flex items-center justify-between px-4 text-xs text-gray-500">
        <div>当前位置: {{ getPageTitle }}</div>
        <div>{{ currentTime }} | 操作员: {{ currentStaff.name }} ({{ currentStaff.job_number }})</div>
      </div>

      <!-- View A: POS -->
      <div v-if="currentView === 'pos'" class="flex h-full">
        <!-- Left Panel -->
        <div class="w-[65%] p-4 border-r border-[#303030] flex flex-col bg-[#141414]">
          <!-- Search -->
          <div class="flex gap-2 mb-3">
            <el-input v-model="searchQuery" placeholder="输入拼音/名称搜索... (F2)" prefix-icon="Search" class="flex-1 dark-input" ref="searchInput" />
            <el-select v-model="filterCategory" placeholder="分类" style="width: 120px" class="dark-select">
              <el-option label="全部" value="all" />
              <el-option label="门票" value="ticket" />
              <el-option label="套票" value="package" />
            </el-select>
          </div>

          <!-- Product List -->
          <div class="flex-1 flex flex-col gap-2 overflow-y-auto pr-1 custom-scrollbar">
            <div 
              v-for="p in filteredProducts" 
              :key="p.id" 
              class="flex justify-between items-center p-3 bg-[#1f1f1f] rounded-md cursor-pointer border border-transparent hover:bg-[#333] hover:border-[#444] active:bg-[#444] active:border-[#1890ff] transition-all"
              @click="addToCart(p)"
            >
              <div>
                <div class="text-white font-medium text-base">{{ p.name }}</div>
                <div class="flex gap-2 mt-1">
                  <span class="text-xs px-1.5 py-0.5 rounded bg-gray-700 text-gray-400">门票</span>
                  <span v-for="tag in p.parsedTags" :key="tag" class="text-xs px-1.5 py-0.5 rounded bg-blue-900 text-blue-300">{{ tag }}</span>
                  <span class="text-xs px-1.5 py-0.5 rounded bg-gray-800 text-gray-500">库存: {{ p.stock_type === 'unlimited' ? '∞' : p.daily_stock }}</span>
                </div>
              </div>
              <div class="text-xl font-bold text-[#faad14] font-mono">¥{{ p.price }}</div>
            </div>
          </div>

          <!-- Toolbar -->
          <div class="mt-3 pt-3 border-t border-[#333] grid grid-cols-5 gap-2.5">
            <el-tooltip content="查询优惠政策/入园规则 (Ctrl+F)" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]" @click="showPolicy = true">
                <el-icon class="text-xl mb-1 text-blue-400"><Reading /></el-icon>
                <span class="text-xs">政策查询</span>
              </div>
            </el-tooltip>
            <el-tooltip content="打开简易计算器" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]" @click="showCalc = true">
                <el-icon class="text-xl mb-1"><Grid /></el-icon>
                <span class="text-xs">计算器</span>
              </div>
            </el-tooltip>
            <el-tooltip content="重新打印上一笔订单" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]" @click="handleReprint">
                <el-icon class="text-xl mb-1"><Printer /></el-icon>
                <span class="text-xs">重打</span>
              </div>
            </el-tooltip>
            <el-tooltip content="交班注意事项记录" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]" @click="showNote = true">
                <el-icon class="text-xl mb-1"><Notebook /></el-icon>
                <span class="text-xs">便签</span>
              </div>
            </el-tooltip>
            <el-tooltip content="刷新商品与库存 (F5)" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]" @click="fetchProducts">
                <el-icon class="text-xl mb-1"><Refresh /></el-icon>
                <span class="text-xs">刷新</span>
              </div>
            </el-tooltip>
          </div>
        </div>

        <!-- Right Panel (Cart) -->
        <div class="w-[35%] bg-[#1f1f1f] flex flex-col shadow-[-4px_0_10px_rgba(0,0,0,0.2)]">
          <div class="p-3 border-b border-[#303030] flex justify-between font-bold">
            <span>购物清单</span>
            <span class="cursor-pointer text-gray-500 hover:text-red-500" @click="clearCart">清空</span>
          </div>
          
          <div class="flex-1 overflow-y-auto p-2 custom-scrollbar">
            <div v-if="cart.length===0" class="h-full flex flex-col items-center justify-center text-gray-600">
              <el-icon :size="40" class="mb-2 opacity-30"><ShoppingCart /></el-icon>
              <span class="text-sm">暂无商品</span>
            </div>
            <div v-for="(item, idx) in cart" :key="idx" class="bg-[#2b2b2b] p-3 mb-2 rounded flex justify-between items-center border border-transparent hover:border-gray-600">
              <div>
                <div class="font-medium">{{ item.name }}</div>
                <div class="text-xs text-gray-500">单价: {{ item.price }}</div>
              </div>
              <div class="flex items-center gap-3">
                <div class="text-lg font-bold font-mono text-[#faad14]">¥{{ (item.price * item.quantity).toFixed(2) }}</div>
                <div class="flex bg-[#333] rounded overflow-hidden border border-[#444]">
                  <button class="w-7 h-7 text-white hover:bg-[#444] flex items-center justify-center" @click="updateQty(idx, -1)">-</button>
                  <span class="w-8 text-center text-sm leading-7 bg-[#262626]">{{ item.quantity }}</span>
                  <button class="w-7 h-7 text-white hover:bg-[#444] flex items-center justify-center" @click="updateQty(idx, 1)">+</button>
                </div>
              </div>
            </div>
          </div>

          <div class="p-4 bg-[#141414] border-t border-[#303030]">
            <div class="flex justify-between text-2xl font-bold mb-4 text-[#faad14] font-mono">
              <span>合计</span>
              <span>¥{{ totalAmount.toFixed(2) }}</span>
            </div>
            <div class="flex gap-2">
              <el-button class="w-1/3" color="#333" size="large">挂单 (F4)</el-button>
              <el-button type="primary" class="flex-1 !font-bold" size="large" @click="handleCheckout" :disabled="cart.length===0">结账 (Space)</el-button>
            </div>
          </div>
        </div>
      </div>

      <!-- View B: Orders -->
      <div v-if="currentView === 'orders'" class="p-6 h-full flex flex-col">
        <div class="bg-[#1f1f1f] p-4 rounded-lg mb-4 flex gap-3">
          <el-input v-model="orderSearchQuery" placeholder="输入订单号" style="width: 200px" prefix-icon="Search" class="dark-input" @keyup.enter="fetchOrders" />
          <el-date-picker
            v-model="orderDateRange"
            type="daterange"
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            value-format="YYYY-MM-DD"
            style="width: 240px"
            class="dark-input"
            @change="fetchOrders"
          />
          <el-select v-model="orderStatus" placeholder="状态" style="width: 120px" class="dark-select" @change="fetchOrders">
            <el-option label="全部" value="" />
            <el-option label="已支付" value="paid" />
            <el-option label="已退款" value="refund" />
          </el-select>
          <el-button type="primary" @click="fetchOrders">查询</el-button>
        </div>
        <div class="bg-[#1f1f1f] rounded-lg p-4 flex-1 overflow-hidden flex flex-col">
           <div class="flex justify-between text-gray-400 text-xs px-4 py-2 border-b border-[#333]">
             <span class="w-[180px]">订单号</span>
             <span class="w-[100px]">联系人</span>
             <span class="w-[80px]">金额</span>
             <span class="w-[80px]">状态</span>
             <span class="w-[150px]">时间</span>
             <span class="flex-1">商品</span>
           </div>
           <div class="flex-1 overflow-y-auto custom-scrollbar">
             <div v-if="orders.length === 0" class="flex justify-center items-center h-full text-gray-500">暂无数据</div>
             <div v-for="order in orders" :key="order.id" class="flex justify-between items-center px-4 py-3 border-b border-[#333] hover:bg-[#2b2b2b] text-sm">
               <span class="w-[180px] font-mono text-[#1890ff]">{{ order.order_no }}</span>
               <span class="w-[100px]">{{ order.contact_name || '-' }}</span>
               <span class="w-[80px] font-bold text-[#faad14]">¥{{ order.total_amount }}</span>
               <span class="w-[80px]">
                 <span v-if="order.status==='paid'" class="text-green-500">已支付</span>
                 <span v-else-if="order.status==='refund'" class="text-red-500">已退款</span>
                 <span v-else>{{ order.status }}</span>
               </span>
               <span class="w-[150px] text-gray-400 text-xs">{{ new Date(order.created_at).toLocaleString() }}</span>
               <span class="flex-1 text-gray-400 truncate">
                 <span v-for="item in order.items" :key="item.id" class="mr-2">{{ item.product_name }} x{{ item.quantity }}</span>
               </span>
             </div>
           </div>
        </div>
      </div>

      <!-- View C: Verify -->
      <div v-if="currentView === 'verify'" class="h-full flex gap-6 bg-[radial-gradient(circle_at_50%_30%,#1f2a36_0%,#141414_70%)]">
         <div class="flex-1 flex flex-col items-center justify-center">
            <div class="text-2xl font-bold text-gray-400 mb-8">请扫描票据二维码或输入票号</div>
            <div class="w-[80%] max-w-[600px] relative flex gap-2">
               <div class="relative flex-1">
                 <input 
                   v-model="verifyInput"
                   ref="verifyInputRef"
                   class="w-full h-[80px] text-[32px] text-center tracking-[4px] bg-black/30 border-2 border-[#303030] text-[#1890ff] rounded-xl outline-none focus:border-[#1890ff] focus:shadow-[0_0_20px_rgba(24,144,255,0.2)] transition-all" 
                   placeholder="Waiting for scan..." 
                   autofocus 
                   @keyup.enter="handleVerify"
                 />
                 <el-icon class="absolute right-4 top-1/2 -translate-y-1/2 text-gray-500" :size="30"><FullScreen /></el-icon>
               </div>
               <button 
                 class="w-[100px] h-[80px] bg-[#1890ff] rounded-xl text-white font-bold text-xl hover:bg-[#40a9ff] active:scale-95 transition-all shadow-lg shadow-blue-500/30"
                 @click="handleVerify"
               >
                 核销
               </button>
            </div>
         </div>
         <div class="w-[350px] bg-[#1f1f1f] border-l border-[#303030] p-5 flex flex-col">
            <h3 class="text-lg font-bold mb-4 border-b border-[#333] pb-2">最近核销</h3>
            <div class="overflow-y-auto flex-1 space-y-3">
               <!-- History Items Placeholder -->
            </div>
         </div>
      </div>

      <!-- View D: Settings -->
      <div v-if="currentView === 'settings'" class="p-6 h-full">
         <div class="grid grid-cols-3 gap-5">
            <div class="bg-[#1f1f1f] rounded-lg p-6 border border-[#303030]">
               <div class="flex items-center gap-2 mb-5 text-lg font-bold text-[#ddd]"><el-icon><Printer /></el-icon> 设备管理</div>
               <el-form label-position="top">
                  <el-form-item label="小票打印机">
                    <el-select v-model="selectedPrinter" class="w-full" @change="saveSettings">
                      <el-option label="EPSON TM-T88V" value="EPSON TM-T88V"/>
                      <el-option label="Microsoft Print to PDF" value="Microsoft Print to PDF"/>
                    </el-select>
                  </el-form-item>
                  <el-button class="w-full">打印测试页</el-button>
               </el-form>
            </div>
            
            <div class="bg-[#1f1f1f] rounded-lg p-6 border border-[#303030]">
               <div class="flex items-center gap-2 mb-5 text-lg font-bold text-[#ddd]"><el-icon><Place /></el-icon> 终端设置</div>
               <el-form label-position="top">
                  <el-form-item label="当前检票点">
                    <el-select v-model="currentCheckPointId" placeholder="请选择检票点" class="w-full" @change="saveSettings">
                      <el-option v-for="cp in checkpoints" :key="cp.id" :label="cp.name" :value="cp.id" />
                    </el-select>
                  </el-form-item>
                  <el-form-item label="POS 设备编号">
                    <el-input-number v-model="posDeviceId" :min="1" class="w-full" controls-position="right" @change="saveSettings" />
                  </el-form-item>
                  <div class="text-xs text-gray-500 mt-2">设置后将用于核销验证记录</div>
               </el-form>
            </div>

            <div class="bg-[#1f1f1f] rounded-lg p-6 border border-[#303030]">
               <div class="flex items-center gap-2 mb-5 text-lg font-bold text-[#ddd]"><el-icon><Notebook /></el-icon> 交接班管理</div>
               <div class="flex flex-col gap-4">
                 <div class="flex justify-between items-center bg-[#2b2b2b] p-3 rounded">
                   <span class="text-gray-400">当前状态</span>
                   <el-tag :type="shiftState.isOpen ? 'success' : 'info'">{{ shiftState.isOpen ? '当班中' : '未当班' }}</el-tag>
                 </div>
                 <div v-if="shiftState.isOpen" class="text-sm text-gray-500">
                   <div>开始时间: {{ new Date(shiftState.startTime!).toLocaleString() }}</div>
                   <div>操作员: {{ shiftState.operator }}</div>
                 </div>
                 <el-button 
                   :type="shiftState.isOpen ? 'danger' : 'primary'" 
                   class="w-full !h-[40px]" 
                   @click="handleShiftAction"
                 >
                   {{ shiftState.isOpen ? '结束当班 / 交班' : '开始当班' }}
                 </el-button>
               </div>
            </div>
         </div>
      </div>

      <!-- Modals -->
      <el-dialog v-model="showCalc" title="计算器" width="300px" :modal="false" draggable align-center class="dark-dialog">
        <Calculator />
      </el-dialog>

      <el-dialog v-model="showPayment" title="收银台" width="500px" align-center class="dark-dialog" :close-on-click-modal="false">
        <PaymentModal v-if="showPayment" :amount="currentOrder?.total_amount || 0" :order-no="currentOrder?.order_no || ''" :shift-id="shiftState.shiftId || 0" :device-id="posDeviceId || 0" @success="handlePaymentSuccess" />
      </el-dialog>

      <el-dialog v-model="showPolicy" title="百事通 (F3)" width="600px" align-center class="dark-dialog">
        <PolicyModal />
      </el-dialog>

      <el-dialog v-model="showNote" title="交班便签" width="400px" align-center class="dark-dialog">
        <el-input v-model="noteContent" type="textarea" rows="5" placeholder="请记录需要传达给下一班次的事项..." />
        <template #footer>
          <el-button @click="showNote = false">取消</el-button>
          <el-button type="primary" @click="saveNote">保存</el-button>
        </template>
      </el-dialog>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { 
  Monitor, List, Checked, Setting, SwitchButton, 
  Reading, Grid, Printer, Notebook, Refresh,
  ShoppingCart, FullScreen 
} from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'

const router = useRouter()

// Configure Axios
axios.defaults.baseURL = import.meta.env.VITE_API_URL || 'http://127.0.0.1:8080/api/v1'
axios.interceptors.request.use(config => {
  const token = sessionStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

axios.interceptors.response.use(res => res, err => {
  if (err.response && err.response.status === 401) {
    ElMessage.error('登录失效，请重新登录')
    router.push('/login')
  }
  return Promise.reject(err)
})

import Calculator from '../components/Calculator.vue'
import PolicyModal from '../components/PolicyModal.vue'
import PaymentModal from '../components/PaymentModal.vue'

// --- State ---
const currentView = ref('pos')
const searchQuery = ref('')
const filterCategory = ref('all')
const products = ref<any[]>([])
const cart = ref<any[]>([])
const searchInput = ref()
const currentTime = ref('')
const currentStaff = ref({ name: '?', job_number: '?' })


// --- Modals State ---
const showCalc = ref(false)
const showPolicy = ref(false)
const showNote = ref(false)
const noteContent = ref('')
const showPayment = ref(false)
const currentOrder = ref<any>(null)

// --- Orders State ---
const orders = ref<any[]>([])
const orderSearchQuery = ref('')
const orderDateRange = ref<[string, string] | null>(null)
const orderStatus = ref('')
const ordersLoading = ref(false)

// --- Verify State ---
const verifyInput = ref('')
const verifyHistory = ref<any[]>([])
const verifyInputRef = ref()
const checkpoints = ref<any[]>([])
const currentCheckPointId = ref<number | null>(null)
const posDeviceId = ref<number | null>(null)

// --- Settings Logic ---
const selectedPrinter = ref('EPSON TM-T88V')
const shiftState = ref({
  isOpen: false,
  shiftId: null as number | null,
  startTime: null as string | null,
  operator: '李明 (007)'
})

const fetchCheckPoints = async () => {
  try {
    const res = await axios.get('/checkpoints', { params: { page_size: 100 } })
    checkpoints.value = res.data.data
  } catch (e) {
    console.error('Failed to fetch checkpoints', e)
  }
}

const saveSettings = () => {
  if (currentCheckPointId.value) {
    localStorage.setItem('pos_checkpoint_id', currentCheckPointId.value.toString())
  }
  if (posDeviceId.value) {
    localStorage.setItem('pos_device_id', posDeviceId.value.toString())
  }
  localStorage.setItem('pos_printer', selectedPrinter.value)
  ElMessage.success('设置已保存')
}

const loadSettings = () => {
  const savedId = localStorage.getItem('pos_checkpoint_id')
  if (savedId) {
    currentCheckPointId.value = parseInt(savedId)
  }
  const savedDeviceId = localStorage.getItem('pos_device_id')
  if (savedDeviceId) posDeviceId.value = parseInt(savedDeviceId)
  const savedPrinter = localStorage.getItem('pos_printer')
  if (savedPrinter) {
    selectedPrinter.value = savedPrinter
  }
  
  const savedShift = localStorage.getItem('pos_shift_state')
  if (savedShift) {
    try {
      shiftState.value = JSON.parse(savedShift)
    } catch (e) {}
  }

  // Load note
  const savedNote = localStorage.getItem('pos_shift_note')
  if (savedNote) noteContent.value = savedNote
}

const handleShiftAction = async () => {
  const deviceId = Number(localStorage.getItem('pos_device_id') || 0)
  if (!deviceId) {
    ElMessage.warning('请先在终端配置中设置 POS 设备编号')
    return
  }
  if (!shiftState.value.isOpen) {
    try {
      const res = await axios.post('/operations/shifts', { device_id: deviceId, opening_cents: 0 })
      const shift = res.data
      shiftState.value = { isOpen: true, shiftId: shift.id, startTime: shift.opened_at, operator: currentStaff.value.name || '当前操作员' }
      localStorage.setItem('pos_shift_state', JSON.stringify(shiftState.value))
      ElMessage.success('已开始当班')
    } catch (error: any) {
      ElMessage.error(error.response?.data?.error || '开班失败')
    }
  } else {
    if (!shiftState.value.shiftId) { ElMessage.error('当前班次缺少服务端编号，请重新开班'); return }
    try {
      await ElMessageBox.confirm('确定要结束当前班次吗？', '交班确认', { confirmButtonText: '确认交班', cancelButtonText: '取消', type: 'warning' })
      const input = await ElMessageBox.prompt('请输入钱箱实收金额（元）', '交班金额', { inputPattern: /^\d+(\.\d{1,2})?$/, inputErrorMessage: '请输入有效金额', confirmButtonText: '提交', cancelButtonText: '取消' })
      const closingCents = Math.round(Number(input.value) * 100)
      const res = await axios.post(`/operations/shifts/${shiftState.value.shiftId}/close`, { closing_cents: closingCents, notes: noteContent.value })
      const shift = res.data
      const endTime = new Date()
      const duration = shiftState.value.startTime ? 
        ((endTime.getTime() - new Date(shiftState.value.startTime).getTime()) / 1000 / 60 / 60).toFixed(1) : '0'
      
      const report = [
        `操作员：${shiftState.value.operator}`,
        `当班时长：${duration} 小时`,
        `开始时间：${new Date(shiftState.value.startTime!).toLocaleString()}`,
        `结束时间：${endTime.toLocaleString()}`,
        noteContent.value ? `交班便签：${noteContent.value}` : '',
        '请在后台查看详细销售报表。'
      ].filter(Boolean).join('\n')
      ElMessageBox.alert(`${report}\n\n应收：¥${(shift.expected_cents / 100).toFixed(2)}\n实收：¥${(shift.closing_cents / 100).toFixed(2)}`, '交班报告')
      shiftState.value.isOpen = false
      shiftState.value.shiftId = null
      shiftState.value.startTime = null
      localStorage.removeItem('pos_shift_state')
      // Clear note
      noteContent.value = ''
      localStorage.removeItem('pos_shift_note')
    } catch (error: any) {
      if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || '交班失败')
    }
  }
}

const restoreOpenShift = async () => {
  const deviceId = Number(localStorage.getItem('pos_device_id') || 0)
  if (!deviceId) return
  try {
    const { data: shift } = await axios.get('/operations/shifts/open', { params: { device_id: deviceId } })
    shiftState.value = { isOpen: true, shiftId: shift.id, startTime: shift.opened_at, operator: currentStaff.value.name || 'Current operator' }
    localStorage.setItem('pos_shift_state', JSON.stringify(shiftState.value))
  } catch (error: any) {
    if (error.response?.status === 404) {
      shiftState.value = { isOpen: false, shiftId: null, startTime: null, operator: currentStaff.value.name || 'Current operator' }
      localStorage.removeItem('pos_shift_state')
    }
  }
}

// Actions
const saveNote = () => {
  localStorage.setItem('pos_shift_note', noteContent.value)
  showNote.value = false
  ElMessage.success('便签已保存')
}

const handleReprint = async () => {
  if (!posDeviceId.value) {
    ElMessage.warning('Please configure the POS device first')
    return
  }
  try {
    const { data } = await axios.get('/operations/print-jobs', { params: { device_id: posDeviceId.value, status: 'failed' } })
    const job = data.data?.[0]
    if (!job) {
      ElMessage.info('No failed print job is waiting')
      return
    }
    await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printing' })
    // @ts-ignore
    const result = window.api?.printTicket ? await window.api.printTicket({ order_no: job.order_no, ticket_code: job.ticket_code }) : { success: false, message: 'printer bridge is unavailable' }
    if (!result?.success) throw new Error(result?.message || 'printer failed')
    await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printed' })
    ElMessage.success('Reprint completed')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || error.message || 'Reprint failed')
  }
}

const handleLogout = () => {
    ElMessageBox.confirm('确定要退出登录吗?', '提示', { type: 'warning' })
        .then(() => {
            sessionStorage.clear()
            router.push('/login')
        })
}

// --- Computed ---
const getPageTitle = computed(() => {
  const map: any = { pos: '收银台', orders: '订单管理', verify: '核销终端', settings: '系统设置' }
  return map[currentView.value]
})

const filteredProducts = computed(() => {
  let res = products.value
  if (filterCategory.value !== 'all') {
    // res = res.filter(p => p.category === filterCategory.value) // Mock category
  }
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    res = res.filter(p => {
      const nameMatch = p.name.toLowerCase().includes(query)
      const tagMatch = p.parsedTags && p.parsedTags.some((t: string) => t.toLowerCase().includes(query))
      return nameMatch || tagMatch
    })
  }
  return res
})

const totalAmount = computed(() => cart.value.reduce((sum, item) => sum + item.price * item.quantity, 0))

// --- Methods ---
const updateTime = () => {
  const now = new Date()
  currentTime.value = now.toLocaleString()
}

const fetchProducts = async () => {
  try {
    const res = await axios.get('/products', { 
      params: { page_size: 100, type: 'offline' } 
    })
    products.value = res.data.data.map((p: any) => {
      try {
        p.parsedTags = p.tags ? JSON.parse(p.tags) : []
      } catch (e) {
        p.parsedTags = []
      }
      return p
    })
  } catch (e) {
    ElMessage.error('获取产品失败')
  }
}


const addToCart = (product: any) => {
  const existing = cart.value.find(item => item.id === product.id)
  if (existing) {
    existing.quantity++
  } else {
    cart.value.push({ ...product, quantity: 1 })
  }
}

const updateQty = (index: number, delta: number) => {
  const item = cart.value[index]
  item.quantity += delta
  if (item.quantity <= 0) {
    cart.value.splice(index, 1)
  }
}

const clearCart = () => {
  if (cart.value.length === 0) return
  ElMessageBox.confirm('确定清空当前购物车吗？', '提示', { type: 'warning' })
    .then(() => cart.value = [])
    .catch(() => {})
}

const handleCheckout = async () => {
  if (cart.value.length === 0) return
  if (!shiftState.value.isOpen || !shiftState.value.shiftId || !posDeviceId.value) {
    ElMessage.warning('Please open a shift on this POS terminal before selling tickets')
    return
  }
  try {
    const orderData = {
      contact_name: '窗口散客',
      contact_phone: '',
      channel: 'window',
      total_amount: totalAmount.value,
      items: cart.value.map(item => ({ product_id: item.id, quantity: item.quantity }))
    }
    const res = await axios.post('/orders', orderData)
    // ElMessage.success('下单成功！正在打印...') 
    // OLD: Direct Success. NEW: Open Payment.
    currentOrder.value = res.data
    showPayment.value = true
  } catch (e) {
    ElMessage.error('下单失败')
  }
}

const handlePaymentSuccess = async () => {
    showPayment.value = false
    ElMessage.success('支付成功！正在打印...')
    if (!currentOrder.value || !posDeviceId.value || !shiftState.value.shiftId) return
    let job: any
    try {
      const queued = await axios.post('/operations/print-jobs', { device_id: posDeviceId.value, shift_id: shiftState.value.shiftId, order_no: currentOrder.value.order_no })
      job = queued.data
      await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printing' })
      // @ts-ignore
      const result = window.api?.printTicket ? await window.api.printTicket(currentOrder.value) : { success: false, message: 'printer bridge is unavailable' }
      if (!result?.success) throw new Error(result?.message || 'printer failed')
      await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printed' })
      cart.value = []
      currentOrder.value = null
    } catch (error: any) {
      if (job) {
        await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'failed', error: error.message || 'printer failed' }).catch(() => undefined)
      }
      ElMessage.error('Payment succeeded but printing failed. The order and print task were retained for retry.')
    }
}

const handleVerify = async () => {
  const code = verifyInput.value.trim()
  if (!code) return
  
  try {
    // Determine if it's a ticket code or order no (simple heuristic or backend handles both)
    // For now assume ticket code or order no.
    // We need a checkpoint ID. For desktop POS, we might need to select a checkpoint or use a default one.
    const checkPointId = currentCheckPointId.value
    if (!checkPointId) {
      ElMessage.warning('请先在设置中配置当前检票点')
      return
    }
    
    await axios.post('/tickets/verify', {
      code: code,
      check_point_id: checkPointId
    })
    
    ElMessage.success('核销成功')
    verifyHistory.value.unshift({
      code: code,
      status: 'success',
      time: new Date().toLocaleString(),
      msg: '核销成功'
    })
    verifyInput.value = ''
  } catch (e: any) {
    const msg = e.response?.data?.error || '核销失败'
    ElMessage.error(msg)
    verifyHistory.value.unshift({
      code: code,
      status: 'fail',
      time: new Date().toLocaleString(),
      msg: msg
    })
    verifyInput.value = ''
  }
}

const fetchOrders = async () => {
  ordersLoading.value = true
  try {
    const params: any = { page_size: 50, channel: 'window' }
    if (orderSearchQuery.value) params.search = orderSearchQuery.value
    if (orderStatus.value) params.status = orderStatus.value
    if (orderDateRange.value && orderDateRange.value.length === 2) {
      params.start_date = orderDateRange.value[0]
      params.end_date = orderDateRange.value[1]
    }
    
    const res = await axios.get('/orders', { params })
    orders.value = res.data.data
  } catch (e) {
    ElMessage.error('获取订单失败')
  } finally {
    ordersLoading.value = false
  }
}

// --- Lifecycle ---
let timer: any
onMounted(async () => {
  fetchProducts()
  fetchCheckPoints()
  loadSettings()
  timer = setInterval(updateTime, 1000)
  updateTime()

  // Load Staff
  const staffStr = sessionStorage.getItem('staff')
  if (staffStr) {
      try {
          currentStaff.value = JSON.parse(staffStr)
      } catch(e) {}
  }
  await restoreOpenShift()
  
  window.addEventListener('keydown', (e) => {
    if (e.key === 'F2') { e.preventDefault(); searchInput.value?.focus() }
    if (e.key === 'F3' || (e.ctrlKey && e.key === 'f')) { e.preventDefault(); showPolicy.value = true } // Policy
    if (e.key === 'F5') { e.preventDefault(); fetchProducts() }
    if (e.key === 'Delete') { clearCart() } // Clear
    if (e.code === 'Space' && currentView.value === 'pos') { e.preventDefault(); handleCheckout() }
  })
})

import { watch } from 'vue'
watch(currentView, (val) => {
  if (val === 'orders') fetchOrders()
  if (val === 'verify') {
    setTimeout(() => verifyInputRef.value?.focus(), 100)
  }
})

onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
/* Custom Scrollbar */
.custom-scrollbar::-webkit-scrollbar { width: 6px; }
.custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
.custom-scrollbar::-webkit-scrollbar-thumb { background: #444; border-radius: 3px; }
.custom-scrollbar::-webkit-scrollbar-thumb:hover { background: #555; }

/* Element Plus Dark Overrides */
:deep(.dark-input .el-input__wrapper) { background-color: #333; box-shadow: none; border: 1px solid #444; }
:deep(.dark-input .el-input__inner) { color: #fff; }
:deep(.dark-select .el-input__wrapper) { background-color: #333; box-shadow: none; border: 1px solid #444; }
</style>
