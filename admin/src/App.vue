<template>
  <div class="h-screen w-full">
    <!-- Admin Layout -->
    <el-container v-if="!isLoginPage" class="h-full w-full bg-gray-50">
      <!-- Sidebar -->
      <el-aside width="240px" class="bg-slate-900 text-white flex flex-col transition-all duration-300 shadow-xl z-20">
        <!-- Logo Area -->
        <div class="h-16 flex items-center px-6 border-b border-slate-800 bg-slate-950">
          <div class="w-8 h-8 rounded-lg bg-indigo-500 flex items-center justify-center mr-3 shadow-lg shadow-indigo-500/30">
            <el-icon :size="20" class="text-white"><Ticket /></el-icon>
          </div>
          <span class="text-lg font-bold tracking-wide bg-gradient-to-r from-white to-gray-400 bg-clip-text text-transparent">景区票务平台</span>
        </div>

        <!-- Menu -->
        <el-menu
          active-text-color="#fff"
          background-color="transparent"
          class="el-menu-vertical-demo border-none flex-1 py-4"
          :default-active="route.path"
          text-color="#94a3b8"
          router
        >
          <div class="px-4 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">概览</div>
          <el-menu-item index="/" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><Odometer /></el-icon>
            <span>控制台</span>
          </el-menu-item>

          <!-- Super Admin Only -->
          <template v-if="isSuperAdmin">
             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">平台管理</div>
             <el-menu-item index="/tenant" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><OfficeBuilding /></el-icon>
                <span>商户开户管理</span>
             </el-menu-item>
             <el-menu-item index="/platform-operations" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Monitor /></el-icon>
                <span>平台运营工作台</span>
             </el-menu-item>
          </template>

          <!-- Tenant Only -->
          <template v-else>
             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">销售中心</div>
             <el-menu-item v-if="hasCapability('supplier')" index="/product" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Ticket /></el-icon>
                <span>线上门票</span>
             </el-menu-item>
             <el-menu-item v-if="hasCapability('supplier')" index="/product/offline" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Monitor /></el-icon>
                <span>窗口门票</span>
             </el-menu-item>
             <el-menu-item index="/online-order" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><List /></el-icon>
                <span>线上订单</span>
             </el-menu-item>
             <el-menu-item index="/offline-order" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Tickets /></el-icon>
                <span>线下/窗口订单</span>
             </el-menu-item>

             <div v-if="hasAnyCapability('supplier', 'distributor')" class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">分销中心</div>
             <el-menu-item v-if="hasAnyCapability('supplier', 'distributor')" index="/distribution" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Connection /></el-icon>
                <span>分销商管理</span>
             </el-menu-item>
             <el-menu-item v-if="hasAnyCapability('supplier', 'distributor')" index="/channels" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Connection /></el-icon>
                <span>渠道连接</span>
             </el-menu-item>
             <el-menu-item v-if="hasAnyCapability('supplier', 'travel_agency')" index="/teams" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Tickets /></el-icon>
                <span>旅行社团队</span>
             </el-menu-item>

             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">现场运营</div>
             <el-menu-item index="/operations" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Operation /></el-icon>
                <span>运营工作台</span>
             </el-menu-item>
             <el-menu-item v-if="hasCapability('supplier')" index="/policy" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Reading /></el-icon>
                <span>政策知识库</span>
             </el-menu-item>
             <el-menu-item v-if="hasCapability('supplier')" index="/device" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Monitor /></el-icon>
                <span>终端设备</span>
             </el-menu-item>
             <el-menu-item v-if="hasCapability('supplier')" index="/checkpoint" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Location /></el-icon>
                <span>检票点位</span>
             </el-menu-item>

             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">数据与财务</div>
             <el-menu-item index="/finance" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Money /></el-icon>
                <span>财务报表</span>
             </el-menu-item>
             <el-menu-item index="/report" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors text-indigo-400">
                <el-icon><TrendCharts /></el-icon>
                <span>经营数据</span>
             </el-menu-item>
             <el-menu-item v-if="user.role === 'admin' || user.role === 'super_admin'" index="/refund-tasks" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Warning /></el-icon>
                <span>退款待办</span>
             </el-menu-item>
             <el-menu-item v-if="user.role === 'seller' || user.role === 'admin' || user.role === 'super_admin'" index="/after-sales" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Warning /></el-icon>
                <span>售后工作台</span>
             </el-menu-item>
          </template>
          
          <template v-if="!isSuperAdmin">
          <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">组织与设置</div>
           <el-menu-item index="/staff" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><User /></el-icon>
            <span>员工管理</span>
          </el-menu-item>
          <el-menu-item index="/system-user" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><UserFilled /></el-icon>
            <span>系统员管理</span>
          </el-menu-item>
           <el-menu-item index="/payment-config" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><CreditCard /></el-icon>
            <span>支付参数配置</span>
          </el-menu-item>
          <el-menu-item index="/settings" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><Setting /></el-icon>
            <span>系统设置</span>
          </el-menu-item>
          </template>
        </el-menu>

        <!-- User Profile (Bottom) -->
        <!-- User Profile (Bottom) Removed -->
      </el-aside>

      <!-- Main Content -->
      <el-container class="flex flex-col h-full overflow-hidden">
        <!-- Header -->
        <el-header class="h-16 bg-white border-b border-gray-200 flex items-center justify-between px-6 shadow-sm z-10">
          <div class="flex items-center gap-4">
            <el-breadcrumb separator="/">
              <el-breadcrumb-item :to="{ path: '/' }">首页</el-breadcrumb-item>
              <el-breadcrumb-item>{{ route.meta.title || '控制台' }}</el-breadcrumb-item>
            </el-breadcrumb>
          </div>
          <!-- Header Right Actions -->
          <div class="flex items-center gap-6">
            <!-- Account Context -->
            <div v-if="isSuperAdmin" data-testid="account-context" class="hidden md:flex items-center gap-2 bg-indigo-50 border border-indigo-100 rounded-full px-4 py-1.5 cursor-default">
                <el-icon class="text-indigo-600"><Monitor /></el-icon>
                <span class="text-sm font-semibold text-indigo-900">系统服务商</span>
            </div>
            <div v-else data-testid="account-context" class="hidden md:flex items-center gap-2 bg-indigo-50 border border-indigo-100 rounded-full px-4 py-1.5 transition-all hover:shadow-sm hover:border-indigo-200 group cursor-default">
                <el-icon class="text-indigo-600"><OfficeBuilding /></el-icon>
                <span class="text-sm font-semibold text-indigo-900">{{ user.tenant_name || '商户' }}</span>
                <div class="w-px h-3 bg-indigo-200 mx-1"></div>
                <el-tooltip content="点击复制商户编号" placement="bottom">
                    <span 
                        class="text-xs font-mono font-medium text-indigo-500 hover:text-indigo-700 cursor-pointer select-none bg-white px-2 py-0.5 rounded border border-indigo-100"
                        @click="copyCode"
                    >
                        {{ user.system_code || '---' }}
                    </span>
                </el-tooltip>
            </div>

            <!-- Vertical Divider -->
            <div class="w-px h-6 bg-gray-200 hidden md:block"></div>

            <!-- User Profile Dropdown -->
            <el-dropdown trigger="click" @command="handleCommand">
                <div class="flex items-center gap-3 cursor-pointer outline-none select-none transition-opacity hover:opacity-80">
                    <div class="flex flex-col items-end">
                        <span class="text-sm font-bold text-gray-800 leading-tight">{{ user.username || '未登录用户' }}</span>
                        <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wide bg-gray-100 px-1.5 py-px rounded mt-0.5">
                            {{ roleText(user.role) }}
                        </span>
                    </div>
                    <el-avatar :size="40" class="ring-2 ring-gray-100 shadow-sm" src="https://cube.elemecdn.com/0/88/03b0d39583f48206768a7534e55bcpng.png" />
                    <el-icon class="text-gray-400"><CaretBottom /></el-icon>
                </div>
                <template #dropdown>
                    <el-dropdown-menu class="w-48">
                        <div class="px-4 py-3 border-b border-gray-100 mb-1">
                            <p class="text-xs text-gray-400 mb-1">当前身份</p>
                            <p class="text-sm font-bold text-gray-900">{{ roleText(user.role) }}</p>
                        </div>
                        <el-dropdown-item command="logout" class="text-red-500 focus:text-red-600">
                            <el-icon><SwitchButton /></el-icon>
                            退出登录
                        </el-dropdown-item>
                    </el-dropdown-menu>
                </template>
            </el-dropdown>
          </div>
        </el-header>

        <!-- Content Area -->
        <el-main class="flex-1 overflow-auto bg-gray-50 p-6">
          <RouterView v-slot="{ Component }">
            <transition name="fade" mode="out-in">
              <component :is="Component" />
            </transition>
          </RouterView>
        </el-main>
      </el-container>
    </el-container>
    
    <!-- Login Route (Full Screen, No Layout) -->
    <RouterView v-else />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { 
  Odometer, Monitor, Location, Ticket, List, Setting, User, UserFilled,
  SwitchButton, OfficeBuilding, Connection, Money,
  CaretBottom, Reading, TrendCharts, CreditCard, Tickets, Operation, Warning
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

const route = useRoute()
const router = useRouter()
const isSuperAdmin = ref(false)
const user = ref<any>({})

const isLoginPage = computed(() => route.name === 'login' || route.name === 'platform-login')
const activeCapabilities = computed(() => new Set((user.value.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability)))
const hasCapability = (value: string) => activeCapabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(value => activeCapabilities.value.has(value))
const roleText = (role: string) => ({
    platform_admin: '平台管理员', super_admin: '商户最高管理员', admin: '商户管理员',
    sub_admin: '普通管理员', seller: '售票员', checker: '检票员', finance: '结算人员'
} as Record<string, string>)[role] || '系统用户'

const handleLogout = () => {
    const loginPath = user.value.scope === 'platform' ? '/platform/login' : '/login'
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    ElMessage.success('退出登录成功')
    router.push(loginPath)
}

const handleCommand = (command: string) => {
    if (command === 'logout') {
        handleLogout()
    }
}

const copyCode = () => {
    if (user.value.system_code) {
        navigator.clipboard.writeText(user.value.system_code)
        ElMessage.success('系统编号已复制')
    }
}


const loadUser = () => {
    const userStr = localStorage.getItem('user')
    if (userStr) {
        try {
            user.value = JSON.parse(userStr)
            isSuperAdmin.value = user.value.scope === 'platform'
        } catch (e) {
            console.error('Failed to parse user info')
        }
    }
}

watch(() => route.path, () => {
    loadUser()
})

onMounted(() => {
    loadUser()
})
</script>

<style>
/* Custom Menu Active State */
.el-menu-item.is-active {
  background-color: #4f46e5 !important; /* Indigo 600 */
  box-shadow: 0 4px 12px rgba(79, 70, 229, 0.3);
}

.el-menu-item:hover {
  background-color: #1e293b !important; /* Slate 800 */
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
