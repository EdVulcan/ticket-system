<template>
  <el-container class="h-screen w-full bg-gray-50">
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
        default-active="/"
        text-color="#94a3b8"
        router
      >
        <div class="px-4 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">概览</div>
        <el-menu-item index="/" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
          <el-icon><Odometer /></el-icon>
          <span>控制台</span>
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
          <span>门票管理</span>
        </el-menu-item>

        <el-menu-item index="/order" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
          <el-icon><List /></el-icon>
          <span>订单管理</span>
        </el-menu-item>
        
        <div class="px-4 mt-6 mb-2 text-xs font-semibold text-slate-500 uppercase tracking-wider">系统设置</div>
        <el-menu-item index="/settings" class="mx-3 rounded-lg mb-1 hover:bg-slate-800 transition-colors">
          <el-icon><Setting /></el-icon>
          <span>系统设置</span>
        </el-menu-item>
      </el-menu>

      <!-- User Profile (Bottom) -->
      <div class="p-4 border-t border-slate-800 bg-slate-950/50">
        <div class="flex items-center gap-3 cursor-pointer hover:bg-slate-800 p-2 rounded-lg transition-colors group">
          <el-avatar :size="36" class="ring-2 ring-indigo-500/50" src="https://cube.elemecdn.com/0/88/03b0d39583f48206768a7534e55bcpng.png" />
          <div class="flex flex-col overflow-hidden">
            <span class="text-sm font-medium text-white truncate">演示商户</span>
            <div class="flex items-center gap-1 text-xs text-slate-400">
              <span>ID: TS-88492</span>
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
            <el-breadcrumb-item>控制台</el-breadcrumb-item>
          </el-breadcrumb>
        </div>
        <div class="flex items-center gap-4">
          <el-button circle plain>
            <el-icon><Bell /></el-icon>
          </el-button>
          <el-button circle plain>
            <el-icon><FullScreen /></el-icon>
          </el-button>
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
</template>

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
