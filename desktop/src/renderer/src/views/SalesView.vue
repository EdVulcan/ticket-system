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

      <div class="mt-auto mb-6 w-[50px] h-[50px] rounded-xl flex flex-col items-center justify-center text-red-400 cursor-pointer hover:bg-red-900/20">
        <el-icon :size="24"><SwitchButton /></el-icon>
      </div>
    </div>

    <!-- Main Content -->
    <div class="flex-1 flex flex-col overflow-hidden relative">
      <!-- Status Bar -->
      <div class="h-10 bg-[#1f1f1f] border-b border-[#303030] flex items-center justify-between px-4 text-xs text-gray-500">
        <div>当前位置: {{ getPageTitle }}</div>
        <div>{{ currentTime }} | 操作员: 李明 (007)</div>
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
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]">
                <el-icon class="text-xl mb-1 text-blue-400"><Reading /></el-icon>
                <span class="text-xs">政策查询</span>
              </div>
            </el-tooltip>
            <el-tooltip content="打开简易计算器" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]">
                <el-icon class="text-xl mb-1"><Grid /></el-icon>
                <span class="text-xs">计算器</span>
              </div>
            </el-tooltip>
            <el-tooltip content="重新打印上一笔订单" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]">
                <el-icon class="text-xl mb-1"><Printer /></el-icon>
                <span class="text-xs">重打</span>
              </div>
            </el-tooltip>
            <el-tooltip content="交班注意事项记录" placement="top" :show-after="500">
              <div class="bg-[#2b2b2b] border border-[#3d3d3d] rounded-md h-[60px] flex flex-col items-center justify-center text-[#ccc] cursor-pointer hover:bg-[#3d3d3d] hover:text-white hover:border-[#555] hover:-translate-y-0.5 transition-all active:translate-y-0 active:bg-[#222]">
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
          <el-input placeholder="输入订单号" style="width: 200px" prefix-icon="Search" class="dark-input" />
          <el-date-picker type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" style="width: 300px" class="dark-date-picker" />
          <el-select placeholder="状态" style="width: 120px" class="dark-select">
            <el-option label="已支付" value="paid" />
            <el-option label="已退款" value="refund" />
          </el-select>
          <el-button type="primary">查询</el-button>
        </div>
        <div class="bg-[#1f1f1f] rounded-lg p-4 flex-1 overflow-hidden">
           <el-empty description="暂无订单数据" />
        </div>
      </div>

      <!-- View C: Verify -->
      <div v-if="currentView === 'verify'" class="h-full flex gap-6 bg-[radial-gradient(circle_at_50%_30%,#1f2a36_0%,#141414_70%)]">
         <div class="flex-1 flex flex-col items-center justify-center">
            <div class="text-2xl font-bold text-gray-400 mb-8">请扫描票据二维码或输入票号</div>
            <div class="w-[80%] max-w-[600px] relative">
               <input class="w-full h-[80px] text-[32px] text-center tracking-[4px] bg-black/30 border-2 border-[#303030] text-[#1890ff] rounded-xl outline-none focus:border-[#1890ff] focus:shadow-[0_0_20px_rgba(24,144,255,0.2)] transition-all" placeholder="Waiting for scan..." autofocus />
               <el-icon class="absolute right-4 top-1/2 -translate-y-1/2 text-gray-500" :size="30"><FullScreen /></el-icon>
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
                  <el-form-item label="小票打印机"><el-select class="w-full" model-value="EPSON TM-T88V"><el-option label="EPSON TM-T88V" value="1"/></el-select></el-form-item>
                  <el-button class="w-full">打印测试页</el-button>
               </el-form>
            </div>
         </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { 
  Monitor, List, Checked, Setting, SwitchButton, 
  Search, Reading, Grid, Printer, Notebook, Refresh, 
  ShoppingCart, FullScreen 
} from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'

// --- State ---
const currentView = ref('pos')
const searchQuery = ref('')
const filterCategory = ref('all')
const products = ref<any[]>([])
const cart = ref<any[]>([])
const searchInput = ref()
const currentTime = ref('')

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
    const res = await axios.get('http://127.0.0.1:8080/api/v1/products', { 
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
  try {
    const orderData = {
      contact_name: '窗口散客',
      contact_phone: '',
      channel: 'window',
      total_amount: totalAmount.value,
      items: cart.value.map(item => ({ product_id: item.id, quantity: item.quantity }))
    }
    const res = await axios.post('http://127.0.0.1:8080/api/v1/orders', orderData)
    ElMessage.success('下单成功！正在打印...')
    // @ts-ignore
    if (window.api && window.api.printTicket) await window.api.printTicket(res.data)
    cart.value = []
  } catch (e) {
    ElMessage.error('下单失败')
  }
}

// --- Lifecycle ---
let timer: any
onMounted(() => {
  fetchProducts()
  timer = setInterval(updateTime, 1000)
  updateTime()
  
  window.addEventListener('keydown', (e) => {
    if (e.key === 'F2') { e.preventDefault(); searchInput.value?.focus() }
    if (e.key === 'F5') { e.preventDefault(); fetchProducts() }
    if (e.code === 'Space' && currentView.value === 'pos') { e.preventDefault(); handleCheckout() }
  })
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
