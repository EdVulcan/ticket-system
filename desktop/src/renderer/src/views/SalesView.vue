<template>
  <div class="pos-shell">
    <header class="topbar">
      <div class="brand-block">
        <div class="brand-mark"><el-icon :size="22"><Tickets /></el-icon></div>
        <div>
          <div class="brand-title">窗口售票</div>
          <div class="brand-subtitle">{{ getPageTitle }}</div>
        </div>
      </div>

      <nav class="workspace-tabs" aria-label="窗口工作区">
        <button class="workspace-tab" :class="{ active: currentView === 'pos' }" @click="currentView = 'pos'">
          <el-icon><Monitor /></el-icon><span>售票</span>
        </button>
        <button class="workspace-tab" :class="{ active: currentView === 'orders' }" @click="currentView = 'orders'">
          <el-icon><List /></el-icon><span>订单</span>
        </button>
        <button class="workspace-tab" :class="{ active: currentView === 'verify' }" @click="currentView = 'verify'">
          <el-icon><Checked /></el-icon><span>核销</span>
        </button>
        <button class="workspace-tab" :class="{ active: currentView === 'settings' }" @click="currentView = 'settings'">
          <el-icon><Setting /></el-icon><span>终端</span>
        </button>
      </nav>

      <div class="operator-block">
        <div class="operator-meta">
          <span class="clock">{{ currentTime }}</span>
          <span>{{ currentStaff.name }} · {{ currentStaff.job_number }}</span>
        </div>
        <button class="shift-chip" :class="{ open: shiftState.isOpen }" @click="handleShiftAction">
          <span class="status-dot"></span>{{ shiftState.isOpen ? '当班中' : '未开班' }}
        </button>
        <el-tooltip content="退出登录" placement="bottom">
          <button class="icon-button danger" aria-label="退出登录" @click="handleLogout"><el-icon><SwitchButton /></el-icon></button>
        </el-tooltip>
      </div>
    </header>

    <main class="workspace">
      <section v-if="currentView === 'pos'" class="sales-workspace">
        <div class="catalog-pane">
          <div v-if="!shiftState.isOpen || !posDeviceId" class="readiness-banner">
            <el-icon><Warning /></el-icon>
            <span v-if="!posDeviceId">尚未配置 POS 设备，请先进入终端设置。</span>
            <span v-else>当前未开班，开班后才能创建窗口订单。</span>
            <button @click="!posDeviceId ? (currentView = 'settings') : handleShiftAction()">立即处理</button>
          </div>

          <div class="catalog-toolbar">
            <el-input ref="searchInput" v-model="searchQuery" size="large" clearable placeholder="搜索票种名称或标签" :prefix-icon="Search" />
            <div class="catalog-count">可售 {{ filteredProducts.length }} 种</div>
            <el-tooltip content="刷新商品与库存" placement="bottom">
              <el-button :icon="Refresh" circle aria-label="刷新商品与库存" @click="fetchProducts" />
            </el-tooltip>
          </div>

          <div class="product-grid custom-scrollbar">
            <button v-for="p in filteredProducts" :key="p.id" class="product-tile" @click="addToCart(p)">
              <div class="product-main">
                <div class="product-name">{{ p.name }}</div>
                <div class="product-tags">
                  <span v-for="tag in p.parsedTags?.slice(0, 2)" :key="tag">{{ tag }}</span>
                  <span class="stock-tag">库存 {{ p.stock_type === 'unlimited' ? '充足' : p.daily_stock }}</span>
                </div>
              </div>
              <div class="product-action">
                <strong>¥{{ Number(p.price).toFixed(2) }}</strong>
                <span class="add-icon"><el-icon><Plus /></el-icon></span>
              </div>
            </button>
            <div v-if="filteredProducts.length === 0" class="empty-state">
              <el-icon :size="36"><Search /></el-icon>
              <strong>没有匹配的票种</strong>
              <span>调整搜索内容后再试</span>
            </div>
          </div>

          <div class="quick-tools">
            <button @click="showPolicy = true"><el-icon><Reading /></el-icon><span>政策</span></button>
            <button @click="showCalc = true"><el-icon><Grid /></el-icon><span>计算器</span></button>
            <button @click="openHolds"><el-icon><Notebook /></el-icon><span>挂单列表</span></button>
            <button @click="handleReprint"><el-icon><Printer /></el-icon><span>失败重打</span></button>
            <button @click="showNote = true"><el-icon><EditPen /></el-icon><span>交班便签</span></button>
          </div>
        </div>

        <aside class="cart-pane">
          <div class="cart-heading">
            <div>
              <span class="eyebrow">本次交易</span>
              <h2>购票清单 <em>{{ cartItemCount }}</em></h2>
            </div>
            <el-button text type="danger" :disabled="cart.length === 0" @click="clearCart">清空</el-button>
          </div>

          <div class="cart-list custom-scrollbar">
            <div v-if="cart.length === 0" class="empty-cart">
              <div class="empty-cart-icon"><el-icon :size="32"><ShoppingCart /></el-icon></div>
              <strong>还没有选择票种</strong>
              <span>点击左侧票种即可加入</span>
            </div>
            <div v-for="(item, idx) in cart" :key="item.id" class="cart-item">
              <div class="cart-item-top">
                <div class="cart-item-name">{{ item.name }}</div>
                <strong>¥{{ (item.price * item.quantity).toFixed(2) }}</strong>
              </div>
              <div class="cart-item-bottom">
                <span>¥{{ Number(item.price).toFixed(2) }} / 张</span>
                <div class="quantity-stepper">
                  <button aria-label="减少数量" @click="updateQty(idx, -1)"><el-icon><Minus /></el-icon></button>
                  <span>{{ item.quantity }}</span>
                  <button aria-label="增加数量" @click="updateQty(idx, 1)"><el-icon><Plus /></el-icon></button>
                </div>
              </div>
            </div>
          </div>

          <div class="checkout-panel">
            <div class="total-line"><span>共 {{ cartItemCount }} 张</span><strong>¥{{ totalAmount.toFixed(2) }}</strong></div>
            <div class="checkout-actions">
              <el-button size="large" :disabled="cart.length === 0" @click="handleHold"><el-icon><Notebook /></el-icon>挂单</el-button>
              <el-button type="success" size="large" :disabled="cart.length === 0 || !shiftState.isOpen || !posDeviceId" @click="handleCheckout">
                <el-icon><Wallet /></el-icon>收款
              </el-button>
            </div>
          </div>
        </aside>
      </section>

      <section v-if="currentView === 'orders'" class="page-workspace">
        <div class="page-heading"><div><h1>窗口订单</h1><p>查询售票记录并处理后续操作</p></div></div>
        <div class="filter-bar">
          <el-input v-model="orderSearchQuery" placeholder="订单号或联系人" clearable :prefix-icon="Search" @keyup.enter="fetchOrders" />
          <el-date-picker v-model="orderDateRange" type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" value-format="YYYY-MM-DD" @change="fetchOrders" />
          <el-select v-model="orderStatus" placeholder="全部状态" clearable @change="fetchOrders">
            <el-option label="已支付" value="paid" />
            <el-option label="已退款" value="refund" />
          </el-select>
          <el-button type="primary" :icon="Search" @click="fetchOrders">查询</el-button>
        </div>
        <div class="data-panel">
          <el-table v-loading="ordersLoading" :data="orders" height="100%" stripe>
            <el-table-column prop="order_no" label="订单号" min-width="205" />
            <el-table-column prop="contact_name" label="联系人" width="120" />
            <el-table-column label="商品" min-width="240">
              <template #default="{ row }"><span v-for="item in row.items" :key="item.id" class="order-item-text">{{ item.product_name }} × {{ item.quantity }}</span></template>
            </el-table-column>
            <el-table-column label="金额" width="110"><template #default="{ row }"><strong class="money">¥{{ row.total_amount }}</strong></template></el-table-column>
            <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.status === 'paid' ? 'success' : row.status === 'refund' ? 'danger' : 'info'">{{ orderStatusLabel(row.status) }}</el-tag></template></el-table-column>
            <el-table-column label="下单时间" width="180"><template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template></el-table-column>
          </el-table>
        </div>
      </section>

      <section v-if="currentView === 'verify'" class="verify-workspace">
        <div class="verify-main">
          <div class="verify-heading"><div class="verify-icon"><el-icon><FullScreen /></el-icon></div><h1>票券核销</h1><p>扫描二维码，或输入完整票码</p></div>
          <div class="verify-entry">
            <el-input ref="verifyInputRef" v-model="verifyInput" size="large" clearable placeholder="等待扫码或输入票码" @keyup.enter="handleVerify" />
            <el-button type="success" size="large" :disabled="!verifyInput.trim() || !currentCheckPointId || !posDeviceId" @click="handleVerify">确认核销</el-button>
          </div>
          <div class="verify-context">
            <span><el-icon><Place /></el-icon>{{ currentCheckpointName }}</span>
            <span><el-icon><Monitor /></el-icon>设备 {{ posDeviceId || '未配置' }}</span>
          </div>
        </div>
        <aside class="history-pane">
          <div class="history-heading"><h2>最近核销</h2><span>{{ verifyHistory.length }} 条</span></div>
          <div class="history-list custom-scrollbar">
            <div v-if="verifyHistory.length === 0" class="history-empty">暂无核销记录</div>
            <div v-for="entry in verifyHistory" :key="`${entry.code}-${entry.time}`" class="history-item" :class="entry.status">
              <el-icon><CircleCheck v-if="entry.status === 'success'" /><CircleClose v-else /></el-icon>
              <div><strong>{{ entry.msg }}</strong><span>{{ entry.code }}</span><small>{{ entry.time }}</small></div>
            </div>
          </div>
        </aside>
      </section>

      <section v-if="currentView === 'settings'" class="page-workspace">
        <div class="page-heading"><div><h1>终端与班次</h1><p>配置当前窗口的设备归属和交接班状态</p></div></div>
        <div class="settings-grid">
          <section class="settings-section">
            <div class="section-heading"><el-icon><Place /></el-icon><div><h2>窗口归属</h2><p>核销与售票操作将记录到所选设备</p></div></div>
            <el-form label-position="top">
              <el-form-item label="当前检票点"><el-select v-model="currentCheckPointId" placeholder="请选择检票点" class="w-full" @change="saveSettings"><el-option v-for="cp in checkpoints" :key="cp.id" :label="cp.name" :value="cp.id" /></el-select></el-form-item>
              <el-form-item label="POS 设备编号"><el-input-number v-model="posDeviceId" :min="1" class="w-full" controls-position="right" @change="saveSettings" /></el-form-item>
            </el-form>
          </section>
          <section class="settings-section">
            <div class="section-heading"><el-icon><Printer /></el-icon><div><h2>本机硬件</h2><p>硬件适配器未配置时不会伪报成功</p></div></div>
            <div class="hardware-row"><span>小票打印机</span><el-tag type="warning">待配置</el-tag></div>
            <div class="hardware-row"><span>证件阅读器</span><el-tag type="info">待配置</el-tag></div>
          </section>
          <section class="settings-section">
            <div class="section-heading"><el-icon><Notebook /></el-icon><div><h2>当前班次</h2><p>{{ shiftState.isOpen ? `开始于 ${new Date(shiftState.startTime!).toLocaleString()}` : '开班后才能进行窗口收款' }}</p></div></div>
            <div class="shift-summary"><span>状态</span><el-tag :type="shiftState.isOpen ? 'success' : 'info'">{{ shiftState.isOpen ? '当班中' : '未开班' }}</el-tag></div>
            <div v-if="shiftState.isOpen" class="shift-summary"><span>开班备用金</span><strong>¥{{ cents(shiftState.openingCents) }}</strong></div>
            <el-button :type="shiftState.isOpen ? 'danger' : 'success'" size="large" class="w-full" @click="handleShiftAction">{{ shiftState.isOpen ? '结束当班并交班' : '开始当班' }}</el-button>
          </section>
        </div>
      </section>

      <el-dialog v-model="showCalc" title="计算器" width="320px" :modal="false" draggable align-center><Calculator /></el-dialog>
      <el-dialog v-model="showPayment" title="收款" width="500px" align-center :close-on-click-modal="false">
        <PaymentModal v-if="showPayment" :amount="currentOrder?.total_amount || 0" :order-no="currentOrder?.order_no || ''" :shift-id="shiftState.shiftId || 0" :device-id="posDeviceId || 0" @success="handlePaymentSuccess" />
      </el-dialog>
      <el-dialog v-model="showOpenShift" title="开始当班" width="420px" align-center :close-on-click-modal="false">
        <div class="shift-dialog-intro">请清点钱箱内用于找零的备用金。该金额会计入本班应交现金。</div>
        <el-form label-position="top">
          <el-form-item label="开班备用金（元）">
            <el-input-number v-model="openingAmount" :min="0" :precision="2" :step="10" :controls="false" class="money-input" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="showOpenShift = false">取消</el-button>
          <el-button type="success" :loading="shiftSubmitting" @click="submitOpenShift">确认开班</el-button>
        </template>
      </el-dialog>
      <el-dialog v-model="showCloseShift" title="交班清点" width="720px" align-center :close-on-click-modal="false">
        <div v-loading="shiftSummaryLoading">
          <div class="shift-summary-grid">
            <div v-for="method in shiftMethods" :key="method.key" class="shift-method-panel">
              <div class="shift-method-title"><span>{{ method.label }}</span><strong>净收 ¥{{ cents(methodSummary(method.key).net_cents) }}</strong></div>
              <div><span>实收</span><b>¥{{ cents(methodSummary(method.key).gross_cents) }}</b></div>
              <div><span>退款</span><b>¥{{ cents(methodSummary(method.key).refund_cents) }}</b></div>
            </div>
          </div>
          <div class="cash-count-panel">
            <div class="cash-fact"><span>开班备用金</span><strong>¥{{ cents(closeSummary?.shift?.opening_cents) }}</strong></div>
            <div class="cash-fact"><span>应交现金</span><strong>¥{{ cents(closeSummary?.cash_expected_cents) }}</strong></div>
            <div class="cash-count-input">
              <label>钱箱实盘（元）</label>
              <el-input-number v-model="closingAmount" :min="0" :precision="2" :step="10" :controls="false" />
            </div>
            <div class="cash-difference" :class="{ balanced: closeDifferenceCents === 0 }"><span>差异</span><strong>{{ signedCents(closeDifferenceCents) }}</strong></div>
          </div>
          <el-form label-position="top" class="mt-4"><el-form-item label="交班说明"><el-input v-model="closingNotes" type="textarea" :rows="3" maxlength="255" show-word-limit placeholder="有差异或需交接的事项请在此说明" /></el-form-item></el-form>
        </div>
        <template #footer>
          <el-button @click="showCloseShift = false">取消</el-button>
          <el-button type="danger" :loading="shiftSubmitting" :disabled="shiftSummaryLoading" @click="submitCloseShift">确认关班</el-button>
        </template>
      </el-dialog>
      <el-dialog v-model="showHolds" title="挂单列表" width="760px" align-center>
        <div class="flex justify-between items-center mb-3">
          <span class="text-sm text-gray-400">挂单只保存商品选择，恢复时会重新校验价格、上下架和库存。</span>
          <el-button :icon="Refresh" circle title="刷新挂单" @click="loadHolds" />
        </div>
        <el-table :data="holds" stripe max-height="360" v-loading="holdsLoading">
          <el-table-column prop="hold_no" label="挂单号" width="220" />
          <el-table-column label="商品" min-width="220">
            <template #default="{ row }">{{ formatHoldItems(row) }}</template>
          </el-table-column>
          <el-table-column label="金额" width="110">
            <template #default="{ row }">¥{{ (row.total_cents / 100).toFixed(2) }}</template>
          </el-table-column>
          <el-table-column prop="expires_at" label="有效期" width="180" />
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="resumeHold(row)">恢复</el-button>
              <el-button link type="danger" @click="cancelHold(row)">取消</el-button>
            </template>
          </el-table-column>
        </el-table>
        <template #footer><el-button @click="showHolds = false">关闭</el-button></template>
      </el-dialog>

      <el-dialog v-model="showPolicy" title="票务政策" width="600px" align-center>
        <PolicyModal />
      </el-dialog>

      <el-dialog v-model="showNote" title="交班便签" width="400px" align-center>
        <el-input v-model="noteContent" type="textarea" rows="5" placeholder="请记录需要传达给下一班次的事项..." />
        <template #footer>
          <el-button @click="showNote = false">取消</el-button>
          <el-button type="primary" @click="saveNote">保存</el-button>
        </template>
      </el-dialog>

    </main>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { 
  Monitor, List, Checked, Setting, SwitchButton, 
  Reading, Grid, Printer, Notebook, Refresh,
  ShoppingCart, FullScreen, Search, Plus, Minus,
  Tickets, Wallet, Warning, EditPen, Place,
  CircleCheck, CircleClose
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
import { printTicket } from '../services/hardwareBridge'

// --- State ---
const currentView = ref('pos')
const searchQuery = ref('')
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
const showHolds = ref(false)
const holds = ref<any[]>([])
const holdsLoading = ref(false)

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
const shiftState = ref({
  isOpen: false,
  shiftId: null as number | null,
  startTime: null as string | null,
  operator: '未登录员工',
  openingCents: 0
})
const showOpenShift = ref(false)
const showCloseShift = ref(false)
const openingAmount = ref(0)
const closingAmount = ref(0)
const closingNotes = ref('')
const closeSummary = ref<any>(null)
const shiftSummaryLoading = ref(false)
const shiftSubmitting = ref(false)
const shiftMethods = [
  { key: 'cash', label: '现金' },
  { key: 'wechat', label: '微信' },
  { key: 'alipay', label: '支付宝' }
]

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
  ElMessage.success('设置已保存')
}

const loadSettings = () => {
  const savedId = localStorage.getItem('pos_checkpoint_id')
  if (savedId) {
    currentCheckPointId.value = parseInt(savedId)
  }
  const savedDeviceId = localStorage.getItem('pos_device_id')
  if (savedDeviceId) posDeviceId.value = parseInt(savedDeviceId)
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
    openingAmount.value = 0
    showOpenShift.value = true
  } else {
    await prepareCloseShift()
  }
}

const submitOpenShift = async () => {
  if (!posDeviceId.value) return
  shiftSubmitting.value = true
  try {
    const res = await axios.post('/operations/shifts', { device_id: posDeviceId.value, opening_cents: Math.round(openingAmount.value * 100) })
    const shift = res.data
    shiftState.value = { isOpen: true, shiftId: shift.id, startTime: shift.opened_at, operator: currentStaff.value.name || '当前操作员', openingCents: shift.opening_cents || 0 }
    localStorage.setItem('pos_shift_state', JSON.stringify(shiftState.value))
    showOpenShift.value = false
    ElMessage.success('已开始当班')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '开班失败')
  } finally {
    shiftSubmitting.value = false
  }
}

const prepareCloseShift = async () => {
  if (!shiftState.value.shiftId) { ElMessage.error('当前班次缺少服务端编号，请重新登录恢复'); return }
  showCloseShift.value = true
  shiftSummaryLoading.value = true
  try {
    const { data } = await axios.get(`/operations/shifts/${shiftState.value.shiftId}/summary`)
    closeSummary.value = data
    closingAmount.value = (data.cash_expected_cents || 0) / 100
    closingNotes.value = noteContent.value
  } catch (error: any) {
    showCloseShift.value = false
    ElMessage.error(error.response?.data?.error || '获取班次汇总失败')
  } finally {
    shiftSummaryLoading.value = false
  }
}

const submitCloseShift = async () => {
  if (!shiftState.value.shiftId) return
  shiftSubmitting.value = true
  try {
    await axios.post(`/operations/shifts/${shiftState.value.shiftId}/close`, { closing_cents: Math.round(closingAmount.value * 100), notes: closingNotes.value })
    const difference = closeDifferenceCents.value
    showCloseShift.value = false
    shiftState.value = { isOpen: false, shiftId: null, startTime: null, operator: currentStaff.value.name || '当前员工', openingCents: 0 }
    localStorage.removeItem('pos_shift_state')
    noteContent.value = ''
    localStorage.removeItem('pos_shift_note')
    ElMessage.success(difference === 0 ? '交班完成，现金账实相符' : `交班完成，现金差异 ${signedCents(difference)}`)
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '交班失败')
  } finally {
    shiftSubmitting.value = false
  }
}

const restoreOpenShift = async () => {
  const deviceId = Number(localStorage.getItem('pos_device_id') || 0)
  if (!deviceId) return
  try {
    const { data: shift } = await axios.get('/operations/shifts/open', { params: { device_id: deviceId } })
    shiftState.value = { isOpen: true, shiftId: shift.id, startTime: shift.opened_at, operator: currentStaff.value.name || '当前员工', openingCents: shift.opening_cents || 0 }
    localStorage.setItem('pos_shift_state', JSON.stringify(shiftState.value))
  } catch (error: any) {
    if (error.response?.status === 404) {
      shiftState.value = { isOpen: false, shiftId: null, startTime: null, operator: currentStaff.value.name || '当前员工', openingCents: 0 }
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
    ElMessage.warning('请先配置当前 POS 设备')
    return
  }
  try {
    const { data } = await axios.get('/operations/print-jobs', { params: { device_id: posDeviceId.value, status: 'failed' } })
    const job = data.data?.[0]
    if (!job) {
      ElMessage.info('当前没有等待重打的失败任务')
      return
    }
    await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printing' })
    const result = await printTicket({ order_no: job.order_no, ticket_code: job.ticket_code })
    if (!result?.success) throw new Error(result?.message || '打印失败')
    await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printed' })
    ElMessage.success('重打完成')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || error.message || '重打失败')
  }
}

const handleHold = async () => {
  if (cart.value.length === 0) {
    await loadHolds()
    showHolds.value = true
    return
  }
  if (!shiftState.value.isOpen || !shiftState.value.shiftId || !posDeviceId.value) {
    ElMessage.warning('请先开班并配置 POS 设备')
    return
  }
  try {
    await axios.post('/operations/holds', {
      device_id: posDeviceId.value,
      shift_id: shiftState.value.shiftId,
      items: cart.value.map(item => ({ product_id: item.id, quantity: item.quantity })),
      contact_name: '窗口散客'
    })
    cart.value = []
    ElMessage.success('挂单已保存')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '挂单失败')
  }
}

const openHolds = async () => {
  await loadHolds()
  showHolds.value = true
}

const loadHolds = async () => {
  holdsLoading.value = true
  try {
    const { data } = await axios.get('/operations/holds', { params: { status: 'held', page_size: 50 } })
    holds.value = data.data || []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '获取挂单失败')
  } finally {
    holdsLoading.value = false
  }
}

const formatHoldItems = (hold: any) => {
  try {
    return (hold.items || []).map((item: any) => `商品 #${item.product_id} x${item.quantity}`).join('，')
  } catch (_) {
    return '商品明细不可读'
  }
}

const resumeHold = async (hold: any) => {
  try {
    const { data } = await axios.post(`/operations/holds/${hold.id}/resume`)
    const restored = (data.items || []).map((line: any) => {
      const product = products.value.find(item => item.id === line.product_id)
      if (!product) throw new Error(`商品 #${line.product_id} 已不再可售`)
      return { ...product, quantity: line.quantity }
    })
    cart.value = restored
    showHolds.value = false
    ElMessage.success('挂单已恢复，请核对后结账')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || error.message || '恢复挂单失败')
    await loadHolds()
  }
}

const cancelHold = async (hold: any) => {
  try {
    await ElMessageBox.confirm(`确认取消挂单 ${hold.hold_no}？`, '取消挂单', { type: 'warning' })
    await axios.post(`/operations/holds/${hold.id}/cancel`, { reason: '收银员取消挂单' })
    ElMessage.success('挂单已取消')
    await loadHolds()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || '取消挂单失败')
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

const cartItemCount = computed(() => cart.value.reduce((sum, item) => sum + item.quantity, 0))

const currentCheckpointName = computed(() => {
  if (!currentCheckPointId.value) return '未配置检票点'
  return checkpoints.value.find(item => item.id === currentCheckPointId.value)?.name || `检票点 ${currentCheckPointId.value}`
})

const orderStatusLabel = (status: string) => {
  const labels: Record<string, string> = { unpaid: '待支付', paid: '已支付', cancelled: '已取消', refund: '已退款', refunded: '已退款' }
  return labels[status] || status
}

const filteredProducts = computed(() => {
  let res = products.value
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
const closeDifferenceCents = computed(() => Math.round(closingAmount.value * 100) - Number(closeSummary.value?.cash_expected_cents || 0))
const cents = (value: number | undefined) => ((Number(value) || 0) / 100).toFixed(2)
const signedCents = (value: number | undefined) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const methodSummary = (method: string) => closeSummary.value?.payments?.find((item: any) => item.method === method) || { gross_cents: 0, refund_cents: 0, net_cents: 0 }

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
    ElMessage.warning('请先在当前 POS 设备上开班')
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
      const result = await printTicket(currentOrder.value)
      if (!result?.success) throw new Error(result?.message || '打印失败')
      await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printed' })
      cart.value = []
      currentOrder.value = null
    } catch (error: any) {
      if (job) {
        await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'failed', error: error.message || '打印失败' }).catch(() => undefined)
      }
      ElMessage.error('支付已成功，但打印失败。订单和打印任务已保留，可稍后重打。')
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
      check_point_id: checkPointId,
      device_id: posDeviceId.value
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
    if (e.key === 'F4') { e.preventDefault(); handleHold() }
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
.pos-shell {
  --ink: #20231f;
  --muted: #697168;
  --line: #dfe3dc;
  --surface: #ffffff;
  --canvas: #f2f4f0;
  --green: #16784a;
  --green-dark: #0d5d38;
  --amber: #b96212;
  height: 100vh;
  min-width: 1024px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: var(--ink);
  background: var(--canvas);
}

.topbar {
  height: 64px;
  flex: 0 0 64px;
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 0 18px;
  background: #232925;
  color: #fff;
  border-bottom: 1px solid #131713;
}

.brand-block, .operator-block, .workspace-tabs, .workspace-tab, .shift-chip,
.catalog-toolbar, .product-tile, .product-action, .quick-tools, .cart-heading,
.cart-item-top, .cart-item-bottom, .quantity-stepper, .total-line, .checkout-actions,
.filter-bar, .verify-entry, .verify-context, .history-heading, .history-item,
.section-heading, .hardware-row, .shift-summary {
  display: flex;
  align-items: center;
}

.brand-block { width: 176px; gap: 10px; flex: 0 0 176px; }
.brand-mark { width: 36px; height: 36px; display: grid; place-items: center; border-radius: 6px; background: #e7b84a; color: #232925; }
.brand-title { font-size: 16px; line-height: 20px; font-weight: 700; }
.brand-subtitle { margin-top: 1px; font-size: 11px; line-height: 14px; color: #aeb7ae; }

.workspace-tabs { height: 40px; padding: 3px; gap: 2px; border: 1px solid #424a43; border-radius: 7px; background: #1a1f1b; }
.workspace-tab { height: 32px; min-width: 74px; justify-content: center; gap: 6px; border: 0; border-radius: 5px; background: transparent; color: #bcc4bd; cursor: pointer; font-size: 14px; }
.workspace-tab:hover { color: #fff; background: #303732; }
.workspace-tab.active { color: #20231f; background: #fff; font-weight: 700; }

.operator-block { min-width: 0; margin-left: auto; justify-content: flex-end; gap: 10px; }
.operator-meta { text-align: right; font-size: 12px; line-height: 17px; color: #d5dbd5; white-space: nowrap; }
.operator-meta .clock { display: block; color: #96a097; font-variant-numeric: tabular-nums; }
.shift-chip { height: 32px; gap: 7px; padding: 0 10px; border: 1px solid #59615a; border-radius: 6px; background: #303632; color: #d7ddd7; cursor: pointer; }
.shift-chip.open { border-color: #48a374; background: #163d2a; color: #dff5e8; }
.status-dot { width: 7px; height: 7px; border-radius: 50%; background: #9da49e; }
.shift-chip.open .status-dot { background: #57d28e; }
.icon-button { width: 32px; height: 32px; display: grid; place-items: center; border: 1px solid #4a514b; border-radius: 6px; background: transparent; color: #d7ddd7; cursor: pointer; }
.icon-button:hover { background: #363d37; color: #fff; }
.icon-button.danger:hover { border-color: #a84949; background: #582525; }

.workspace { flex: 1; min-height: 0; overflow: hidden; }
.sales-workspace { height: 100%; display: grid; grid-template-columns: minmax(0, 1fr) minmax(370px, 38%); }
.catalog-pane { min-width: 0; min-height: 0; display: flex; flex-direction: column; padding: 14px 16px 0; border-right: 1px solid var(--line); }
.readiness-banner { min-height: 40px; display: flex; align-items: center; gap: 8px; margin-bottom: 10px; padding: 8px 10px; border: 1px solid #e8c787; border-radius: 6px; background: #fff8e9; color: #7a4a0b; font-size: 13px; }
.readiness-banner span { min-width: 0; flex: 1; }
.readiness-banner button { border: 0; background: transparent; color: #8e4b08; font-weight: 700; cursor: pointer; }
.catalog-toolbar { gap: 10px; margin-bottom: 12px; }
.catalog-toolbar :deep(.el-input) { flex: 1; }
.catalog-toolbar :deep(.el-input__wrapper) { min-height: 42px; border-radius: 7px; box-shadow: 0 0 0 1px #ccd2ca inset; }
.catalog-toolbar :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 2px #278157 inset; }
.catalog-count { white-space: nowrap; color: var(--muted); font-size: 13px; }

.product-grid { min-height: 0; flex: 1; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-content: start; gap: 10px; overflow-y: auto; padding: 1px 5px 12px 1px; }
.product-tile { width: 100%; min-height: 88px; justify-content: space-between; gap: 12px; padding: 13px 14px; text-align: left; border: 1px solid #d9ded7; border-radius: 7px; background: var(--surface); color: var(--ink); cursor: pointer; }
.product-tile:hover { border-color: #74a98b; background: #f9fffb; }
.product-tile:active { border-color: var(--green); background: #eef9f2; }
.product-main { min-width: 0; }
.product-name { min-width: 0; font-size: 15px; line-height: 21px; font-weight: 700; word-break: break-word; }
.product-tags { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 8px; }
.product-tags span { max-width: 110px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 2px 6px; border-radius: 4px; background: #edf0eb; color: #667066; font-size: 11px; }
.product-tags .stock-tag { background: #fff4dc; color: #8a570f; }
.product-action { flex: 0 0 auto; align-self: stretch; flex-direction: column; justify-content: space-between; align-items: flex-end; }
.product-action strong { color: var(--amber); font-size: 18px; font-variant-numeric: tabular-nums; }
.add-icon { width: 24px; height: 24px; display: grid; place-items: center; border-radius: 5px; background: #e8f4ed; color: var(--green); }
.empty-state { grid-column: 1 / -1; min-height: 260px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; color: #9aa199; }
.empty-state strong { color: #5e665e; }
.empty-state span { font-size: 13px; }

.quick-tools { height: 54px; flex: 0 0 54px; gap: 4px; border-top: 1px solid var(--line); }
.quick-tools button { height: 34px; display: flex; align-items: center; gap: 5px; padding: 0 10px; border: 0; border-radius: 5px; background: transparent; color: #596159; cursor: pointer; }
.quick-tools button:hover { background: #e4e8e2; color: var(--ink); }

.cart-pane { min-width: 0; min-height: 0; display: flex; flex-direction: column; background: #fff; }
.cart-heading { height: 72px; flex: 0 0 72px; justify-content: space-between; padding: 0 18px; border-bottom: 1px solid var(--line); }
.eyebrow { color: var(--muted); font-size: 11px; }
.cart-heading h2 { margin: 2px 0 0; font-size: 18px; line-height: 24px; }
.cart-heading h2 em { display: inline-flex; min-width: 22px; height: 22px; align-items: center; justify-content: center; margin-left: 5px; border-radius: 5px; background: #edf0eb; color: #586058; font-size: 12px; font-style: normal; }
.cart-list { flex: 1; min-height: 0; overflow-y: auto; padding: 12px 14px; background: #f8f9f7; }
.empty-cart { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 7px; color: #8b938b; }
.empty-cart-icon { width: 58px; height: 58px; display: grid; place-items: center; margin-bottom: 4px; border-radius: 8px; background: #edf0eb; color: #8a928a; }
.empty-cart strong { color: #586058; }
.empty-cart span { font-size: 13px; }
.cart-item { margin-bottom: 9px; padding: 12px; border: 1px solid #dfe3dc; border-radius: 7px; background: #fff; }
.cart-item-top { justify-content: space-between; gap: 12px; }
.cart-item-name { min-width: 0; font-size: 14px; line-height: 20px; font-weight: 700; word-break: break-word; }
.cart-item-top strong { flex: 0 0 auto; color: var(--amber); font-size: 16px; }
.cart-item-bottom { justify-content: space-between; margin-top: 10px; color: var(--muted); font-size: 12px; }
.quantity-stepper { height: 30px; overflow: hidden; border: 1px solid #cfd5cd; border-radius: 6px; background: #fff; }
.quantity-stepper button { width: 30px; height: 28px; display: grid; place-items: center; border: 0; background: #f0f2ee; color: #3d453e; cursor: pointer; }
.quantity-stepper button:hover { background: #dfe5dd; }
.quantity-stepper span { width: 34px; text-align: center; color: var(--ink); font-size: 14px; font-weight: 700; }
.checkout-panel { flex: 0 0 auto; padding: 16px 18px 18px; border-top: 1px solid var(--line); background: #fff; }
.total-line { justify-content: space-between; margin-bottom: 14px; color: var(--muted); }
.total-line strong { color: var(--ink); font-size: 30px; line-height: 36px; font-variant-numeric: tabular-nums; }
.checkout-actions { gap: 10px; }
.checkout-actions :deep(.el-button) { height: 46px; margin: 0; border-radius: 7px; font-weight: 700; }
.checkout-actions :deep(.el-button:first-child) { width: 110px; }
.checkout-actions :deep(.el-button:last-child) { flex: 1; }

.page-workspace { height: 100%; min-height: 0; display: flex; flex-direction: column; padding: 20px; }
.page-heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-heading h1 { margin: 0; font-size: 22px; line-height: 28px; }
.page-heading p { margin: 3px 0 0; color: var(--muted); font-size: 13px; }
.filter-bar { gap: 10px; margin-bottom: 12px; padding: 12px; border: 1px solid var(--line); border-radius: 7px; background: #fff; }
.filter-bar :deep(.el-input) { width: 240px; }
.filter-bar :deep(.el-date-editor) { width: 260px; }
.filter-bar :deep(.el-select) { width: 140px; }
.data-panel { min-height: 0; flex: 1; overflow: hidden; border: 1px solid var(--line); border-radius: 7px; background: #fff; }
.order-item-text { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.money { color: var(--amber); font-variant-numeric: tabular-nums; }

.verify-workspace { height: 100%; display: grid; grid-template-columns: minmax(0, 1fr) 360px; background: #f7f8f5; }
.verify-main { display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 30px; }
.verify-heading { text-align: center; }
.verify-icon { width: 54px; height: 54px; display: grid; place-items: center; margin: 0 auto 14px; border-radius: 8px; background: #e2f1e8; color: var(--green); font-size: 27px; }
.verify-heading h1 { margin: 0; font-size: 26px; }
.verify-heading p { margin: 7px 0 0; color: var(--muted); }
.verify-entry { width: min(680px, 92%); gap: 10px; margin-top: 28px; }
.verify-entry :deep(.el-input__wrapper) { min-height: 58px; border-radius: 7px; box-shadow: 0 0 0 2px #cbd2c9 inset; }
.verify-entry :deep(.el-input__inner) { text-align: center; font-size: 21px; font-variant-numeric: tabular-nums; }
.verify-entry :deep(.el-button) { height: 58px; min-width: 120px; border-radius: 7px; font-weight: 700; }
.verify-context { gap: 18px; margin-top: 16px; color: var(--muted); font-size: 13px; }
.verify-context span { display: flex; align-items: center; gap: 5px; }
.history-pane { min-height: 0; display: flex; flex-direction: column; border-left: 1px solid var(--line); background: #fff; }
.history-heading { height: 62px; justify-content: space-between; padding: 0 18px; border-bottom: 1px solid var(--line); }
.history-heading h2 { margin: 0; font-size: 17px; }
.history-heading span { color: var(--muted); font-size: 12px; }
.history-list { min-height: 0; flex: 1; overflow-y: auto; padding: 12px; }
.history-empty { padding-top: 80px; text-align: center; color: #9aa19a; }
.history-item { align-items: flex-start; gap: 10px; margin-bottom: 8px; padding: 12px; border: 1px solid #dfe3dc; border-left: 4px solid #23915b; border-radius: 6px; }
.history-item.fail { border-left-color: #c74646; }
.history-item > .el-icon { margin-top: 2px; color: #23915b; }
.history-item.fail > .el-icon { color: #c74646; }
.history-item div { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.history-item strong { font-size: 14px; }
.history-item span { overflow: hidden; text-overflow: ellipsis; color: #596159; font-size: 12px; white-space: nowrap; }
.history-item small { color: #969d96; }

.settings-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.settings-section { padding: 18px; border: 1px solid var(--line); border-radius: 7px; background: #fff; }
.section-heading { align-items: flex-start; gap: 10px; margin-bottom: 18px; }
.section-heading > .el-icon { margin-top: 2px; color: var(--green); font-size: 20px; }
.section-heading h2 { margin: 0; font-size: 17px; }
.section-heading p { margin: 4px 0 0; color: var(--muted); font-size: 12px; line-height: 18px; }
.hardware-row, .shift-summary { justify-content: space-between; min-height: 46px; border-top: 1px solid #ecefeb; }
.hardware-row:last-child { border-bottom: 1px solid #ecefeb; }
.shift-summary:last-of-type { margin-bottom: 16px; }
.shift-dialog-intro { margin-bottom: 18px; padding: 10px 12px; border: 1px solid #dfe4dc; border-radius: 6px; background: #f6f8f5; color: #626a62; font-size: 13px; line-height: 20px; }
.money-input { width: 100%; }
.money-input :deep(.el-input__wrapper) { min-height: 48px; }
.money-input :deep(.el-input__inner) { text-align: left; font-size: 22px; font-weight: 700; }
.shift-summary-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.shift-method-panel { padding: 12px; border: 1px solid #dfe3dc; border-radius: 7px; background: #fafbf9; }
.shift-method-panel > div { display: flex; align-items: center; justify-content: space-between; min-height: 27px; color: #697169; font-size: 12px; }
.shift-method-panel .shift-method-title { min-height: 34px; margin-bottom: 5px; padding-bottom: 7px; border-bottom: 1px solid #e5e9e3; color: #303630; }
.shift-method-title span { font-size: 15px; font-weight: 700; }
.shift-method-title strong { color: #16784a; }
.shift-method-panel b { color: #343a34; font-weight: 600; }
.cash-count-panel { display: grid; grid-template-columns: 1fr 1fr 1.4fr 1fr; align-items: end; gap: 10px; margin-top: 12px; padding: 14px; border: 1px solid #d9ded7; border-radius: 7px; background: #fff; }
.cash-fact, .cash-difference { min-height: 52px; display: flex; flex-direction: column; justify-content: center; gap: 4px; }
.cash-fact span, .cash-difference span, .cash-count-input label { color: #717971; font-size: 12px; }
.cash-fact strong, .cash-difference strong { font-size: 18px; font-variant-numeric: tabular-nums; }
.cash-count-input :deep(.el-input-number) { width: 100%; margin-top: 5px; }
.cash-count-input :deep(.el-input__inner) { text-align: left; font-weight: 700; }
.cash-difference strong { color: #bf3f3f; }
.cash-difference.balanced strong { color: #16784a; }

.custom-scrollbar::-webkit-scrollbar { width: 6px; }
.custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
.custom-scrollbar::-webkit-scrollbar-thumb { border-radius: 3px; background: #c4cbc3; }
.custom-scrollbar::-webkit-scrollbar-thumb:hover { background: #a8b0a7; }

:deep(.el-dialog) { border-radius: 8px; }
:deep(.el-button--success) { --el-button-bg-color: var(--green); --el-button-border-color: var(--green); --el-button-hover-bg-color: var(--green-dark); --el-button-hover-border-color: var(--green-dark); }

@media (max-width: 1120px) {
  .topbar { gap: 12px; padding: 0 12px; }
  .brand-block { width: 150px; flex-basis: 150px; }
  .workspace-tab { min-width: 66px; }
  .operator-meta .clock { display: none; }
  .product-grid { grid-template-columns: 1fr; }
  .quick-tools button { padding: 0 7px; }
}
</style>
