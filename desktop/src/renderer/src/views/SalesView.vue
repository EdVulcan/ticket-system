<template>
  <div class="h-screen flex bg-[#1e1e1e] text-gray-200 overflow-hidden font-sans">
    <!-- Left: Product Selection (65%) -->
    <div class="w-[65%] flex flex-col border-r border-gray-700 bg-[#1e1e1e]">
      <!-- Header -->
      <div class="p-4 border-b border-gray-700 flex gap-4 items-center bg-[#252526]">
        <h2 class="text-xl font-bold text-white mr-4">Ticket POS</h2>
        <el-input
          v-model="searchQuery"
          placeholder="搜索产品 (F2)"
          prefix-icon="Search"
          class="flex-1 dark-input"
          ref="searchInput"
        />
        <el-button type="primary" color="#007acc" @click="fetchProducts">刷新 (F5)</el-button>
      </div>

      <!-- Product Grid -->
      <div class="flex-1 p-4 overflow-y-auto custom-scrollbar">
        <div class="grid grid-cols-3 xl:grid-cols-4 gap-4">
          <div
            v-for="product in filteredProducts"
            :key="product.id"
            class="cursor-pointer transition-all hover:scale-105 active:scale-95"
            @click="addToCart(product)"
          >
            <div class="bg-[#2d2d2d] rounded-lg p-4 border border-gray-700 hover:border-[#007acc] hover:bg-[#333]">
              <div class="h-20 bg-[#3e3e42] rounded mb-3 flex items-center justify-center text-[#007acc]">
                <el-icon :size="36"><Ticket /></el-icon>
              </div>
              <h3 class="font-bold text-gray-100 truncate text-lg">{{ product.name }}</h3>
              <div class="flex justify-between items-center mt-3">
                <span class="text-[#ff9f43] font-bold text-xl">¥{{ product.price }}</span>
                <span class="text-xs text-gray-500">库存: {{ product.stock_type === 'unlimited' ? '∞' : product.daily_stock }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Right: Cart & Checkout (35%) -->
    <div class="w-[35%] flex flex-col bg-[#252526] border-l border-gray-800">
      <!-- Cart Header -->
      <div class="p-4 border-b border-gray-700 bg-[#2d2d2d] flex justify-between items-center">
        <h2 class="text-lg font-bold text-white">当前订单</h2>
        <el-tag type="info" effect="dark" size="small">窗口直营</el-tag>
      </div>

      <!-- Cart Items -->
      <div class="flex-1 overflow-y-auto p-2 custom-scrollbar">
        <el-table 
          :data="cart" 
          style="width: 100%; --el-table-bg-color: transparent; --el-table-tr-bg-color: transparent; --el-table-header-bg-color: transparent; --el-table-text-color: #ccc; --el-table-header-text-color: #fff; --el-table-row-hover-bg-color: #333; --el-table-border-color: #444" 
          empty-text="暂无商品"
          :header-cell-style="{ background: '#252526' }"
        >
          <el-table-column prop="name" label="商品" />
          <el-table-column label="数量" width="120">
            <template #default="{ row }">
              <el-input-number v-model="row.quantity" :min="1" size="small" style="width: 90px" class="dark-input-number" />
            </template>
          </el-table-column>
          <el-table-column label="金额" width="80" align="right">
            <template #default="{ row }">
              <span class="text-[#ff9f43]">¥{{ (row.price * row.quantity).toFixed(2) }}</span>
            </template>
          </el-table-column>
          <el-table-column width="50" align="center">
            <template #default="{ row, $index }">
              <el-button type="danger" link icon="Delete" @click="removeFromCart($index)" />
            </template>
          </el-table-column>
        </el-table>
      </div>

      <!-- Footer Actions -->
      <div class="p-4 bg-[#2d2d2d] border-t border-gray-700 shadow-2xl">
        <div class="flex justify-between items-end mb-4">
          <span class="text-gray-400">数量: <span class="text-white font-bold">{{ totalQuantity }}</span></span>
          <div class="text-right">
            <div class="text-xs text-gray-500 mb-1">合计金额</div>
            <div class="text-4xl font-bold text-[#ff9f43] font-mono">¥{{ totalAmount.toFixed(2) }}</div>
          </div>
        </div>
        
        <div class="grid grid-cols-2 gap-3">
          <el-button size="large" color="#444" @click="clearCart" class="!text-white border-none hover:!bg-[#555]">
            清空 (Esc)
          </el-button>
          <el-button type="primary" size="large" color="#28a745" @click="handleCheckout" :disabled="cart.length === 0" class="!text-white font-bold text-lg h-12">
            收款出票 (F12)
          </el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Custom Scrollbar for Dark Mode */
.custom-scrollbar::-webkit-scrollbar {
  width: 8px;
}
.custom-scrollbar::-webkit-scrollbar-track {
  background: #1e1e1e; 
}
.custom-scrollbar::-webkit-scrollbar-thumb {
  background: #444; 
  border-radius: 4px;
}
.custom-scrollbar::-webkit-scrollbar-thumb:hover {
  background: #555; 
}

/* Element Plus Dark Overrides */
:deep(.el-input__wrapper) {
  background-color: #333;
  box-shadow: none;
  border: 1px solid #444;
}
:deep(.el-input__inner) {
  color: #fff;
}
:deep(.el-input-number__decrease), :deep(.el-input-number__increase) {
  background-color: #444;
  color: #fff;
  border-color: #555;
}
</style>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Search, Ticket, Delete } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'

// --- State ---
const products = ref<any[]>([])
const searchQuery = ref('')
const cart = ref<any[]>([])
const searchInput = ref()

// --- Computed ---
const filteredProducts = computed(() => {
  if (!searchQuery.value) return products.value
  return products.value.filter(p => p.name.includes(searchQuery.value))
})

const totalQuantity = computed(() => cart.value.reduce((sum, item) => sum + item.quantity, 0))
const totalAmount = computed(() => cart.value.reduce((sum, item) => sum + item.price * item.quantity, 0))

// --- Methods ---
const fetchProducts = async () => {
  try {
    // Assuming backend is at localhost:8080
    // Filter by type=offline
    const res = await axios.get('http://localhost:8080/api/v1/products', { 
      params: { 
        page_size: 100,
        type: 'offline' 
      } 
    })
    products.value = res.data.data
  } catch (e) {
    console.error(e)
    ElMessage.error('获取产品失败，请检查网络')
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

const removeFromCart = (index: number) => {
  cart.value.splice(index, 1)
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
    // 1. Create Order
    const orderData = {
      contact_name: '窗口散客', // Default for window sales
      contact_phone: '',
      total_amount: totalAmount.value,
      items: cart.value.map(item => ({
        product_id: item.id,
        quantity: item.quantity
      }))
    }
    
    const res = await axios.post('http://localhost:8080/api/v1/orders', orderData)
    const order = res.data
    
    ElMessage.success('下单成功！正在打印...')
    
    // 2. Print Ticket (Call Hardware Layer)
    // @ts-ignore
    if (window.api && window.api.printTicket) {
       // @ts-ignore
       await window.api.printTicket(order)
    }
    
    cart.value = []
  } catch (e) {
    console.error(e)
    ElMessage.error('下单失败')
  }
}

// --- Keyboard Shortcuts ---
const handleKeydown = (e: KeyboardEvent) => {
  if (e.key === 'F2') {
    e.preventDefault()
    searchInput.value?.focus()
  } else if (e.key === 'F5') {
    e.preventDefault()
    fetchProducts()
  } else if (e.key === 'F12') {
    e.preventDefault()
    handleCheckout()
  } else if (e.key === 'Escape') {
    e.preventDefault()
    clearCart()
  }
}

onMounted(() => {
  fetchProducts()
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
})
</script>
