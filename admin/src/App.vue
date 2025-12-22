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
          <span class="text-lg font-bold tracking-wide bg-gradient-to-r from-white to-gray-400 bg-clip-text text-transparent">TicketPro</span>
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
          </template>

          <!-- Tenant Only -->
          <template v-else>
             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">B2B 分销</div>
             <el-menu-item index="/distribution" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Connection /></el-icon>
                <span>分销中心 (供应商)</span>
             </el-menu-item>

             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">运营管理</div>
             <el-menu-item index="/device" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Monitor /></el-icon>
                <span>终端设备管理</span>
             </el-menu-item>
             <el-menu-item index="/checkpoint" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Location /></el-icon>
                <span>检票点位设置</span>
             </el-menu-item>

             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">线上销售</div>
             <el-menu-item index="/product" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Ticket /></el-icon>
                <span>线上门票管理</span>
             </el-menu-item>
             <el-menu-item index="/order" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><List /></el-icon>
                <span>线上订单管理</span>
             </el-menu-item>

             <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">线下销售</div>
             <el-menu-item index="/product/offline" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
                <el-icon><Monitor /></el-icon>
                <span>窗口门票管理</span>
             </el-menu-item>
          </template>
          
          <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">系统设置</div>
          <el-menu-item index="/settings" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><Setting /></el-icon>
            <span>系统设置</span>
          </el-menu-item>

          <el-menu-item index="/staff" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
            <el-icon><User /></el-icon>
            <span>员工管理</span>
          </el-menu-item>
        </el-menu>

        <!-- User Profile (Bottom) -->
        <div class="p-4 border-t border-slate-800 bg-slate-950/50">
          <div class="flex items-center gap-3 cursor-pointer hover:bg-slate-800 p-2 rounded-lg transition-colors group">
            <el-avatar :size="36" class="ring-2 ring-indigo-500/50" src="https://cube.elemecdn.com/0/88/03b0d39583f48206768a7534e55bcpng.png" />
            <div class="flex flex-col overflow-hidden">
              <span class="text-sm font-medium text-white truncate">{{ user.username || '商户' }}</span>
              <div class="flex items-center gap-1 text-xs text-slate-400">
                <span @click.stop="toggleDevRole" class="cursor-pointer hover:text-indigo-400 select-none" title="[开发调试] 点击切换角色">
                    {{ isSuperAdmin ? '平台管理员 (Dev)' : '商户管理员' }}
                </span>
                <el-icon class="opacity-0 group-hover:opacity-100 transition-opacity hover:text-white" title="复制编号"><CopyDocument /></el-icon>
              </div>
            </div>
          </div>
        </div>
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
          <div class="flex items-center gap-4">
            <div class="flex items-center gap-4 transition-all duration-300">
                <el-tooltip content="退出登录" placement="bottom">
                  <el-button circle plain type="danger" @click="handleLogout">
                    <el-icon><SwitchButton /></el-icon>
                  </el-button>
                </el-tooltip>
                <el-button circle plain>
                  <el-icon><Bell /></el-icon>
                </el-button>
                <el-button circle plain>
                  <el-icon><FullScreen /></el-icon>
                </el-button>
            </div>
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
import { computed, ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { 
  Odometer, Monitor, Location, Ticket, List, Setting, User, 
  SwitchButton, Bell, FullScreen, CopyDocument, OfficeBuilding, Connection 
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

const route = useRoute()
const router = useRouter()
const isSuperAdmin = ref(false)
const user = ref<any>({})

const isLoginPage = computed(() => route.path === '/login' || route.name === 'login')

const handleLogout = () => {
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    ElMessage.success('退出登录成功')
    router.push('/login')
}

const toggleDevRole = () => {
    const newUser = { ...user.value }
    if (isSuperAdmin.value) {
        newUser.role = 'admin'
        ElMessage.info('已切换为：商户视角')
    } else {
        newUser.role = 'super_admin'
        ElMessage.success('已切换为：平台上帝视角')
    }
    user.value = newUser
    isSuperAdmin.value = newUser.role === 'super_admin'
    localStorage.setItem('user', JSON.stringify(newUser))
    // reload to apply menu changes ensuring router/App state sync
    setTimeout(() => location.reload(), 500)
}

onMounted(() => {
    const userStr = localStorage.getItem('user')
    if (userStr) {
        try {
            user.value = JSON.parse(userStr)
            // Logic: standard tenants have role="admin" or "staff".
            // Platform Operator must have role="super_admin".
            // For testing: manually set localStorage user.role = 'super_admin' to see effect.
            isSuperAdmin.value = user.value.role === 'super_admin'
        } catch (e) {
            console.error('Failed to parse user info')
        }
    }
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
