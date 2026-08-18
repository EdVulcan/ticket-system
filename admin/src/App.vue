<template>
  <div class="app-root">
    <el-container v-if="!isLoginPage" class="app-shell">
      <button
        v-if="mobileSidebarOpen"
        class="sidebar-backdrop"
        type="button"
        aria-label="关闭导航"
        @click="mobileSidebarOpen = false"
      />

      <el-aside
        :width="sidebarWidth"
        class="app-sidebar"
        :class="{ 'is-collapsed': sidebarCollapsed, 'is-mobile-open': mobileSidebarOpen }"
      >
        <div class="brand-bar">
          <div class="brand-mark" aria-hidden="true"><el-icon><Ticket /></el-icon></div>
          <div v-show="!sidebarCollapsed || mobileSidebarOpen" class="brand-copy">
            <strong>景区票务平台</strong>
            <span>{{ isSuperAdmin ? '平台管理中心' : '商户管理中心' }}</span>
          </div>
        </div>

        <div class="sidebar-scroll">
          <el-menu
            :default-active="route.path"
            :collapse="sidebarCollapsed && !mobileSidebarOpen"
            :collapse-transition="false"
            class="app-menu"
            router
            @select="mobileSidebarOpen = false"
          >
            <template v-for="group in navGroups" :key="group.label">
              <div v-if="!sidebarCollapsed || mobileSidebarOpen" class="menu-group-label">{{ group.label }}</div>
              <el-menu-item v-for="item in group.items" :key="item.path" :index="item.path">
                <el-icon><component :is="item.icon" /></el-icon>
                <template #title>{{ item.label }}</template>
              </el-menu-item>
            </template>
          </el-menu>
        </div>

        <div class="sidebar-footer">
          <button
            class="sidebar-collapse-button"
            type="button"
            :title="sidebarCollapsed ? '展开导航' : '收起导航'"
            @click="toggleSidebar"
          >
            <el-icon><component :is="sidebarCollapsed ? Expand : Fold" /></el-icon>
            <span v-if="!sidebarCollapsed">收起导航</span>
          </button>
        </div>
      </el-aside>

      <el-container class="app-workspace">
        <el-header class="app-topbar">
          <div class="topbar-leading">
            <button class="mobile-menu-button" type="button" title="打开导航" @click="mobileSidebarOpen = true">
              <el-icon><MenuIcon /></el-icon>
            </button>
            <div class="route-context">
              <span>{{ currentGroupLabel }}</span>
              <strong>{{ route.meta.title || '控制台' }}</strong>
            </div>
          </div>

          <div class="topbar-actions">
            <div v-if="isSuperAdmin" data-testid="account-context" class="account-context">
              <el-icon><Monitor /></el-icon>
              <div><span>当前主体</span><strong>系统服务商</strong></div>
            </div>
            <button v-else data-testid="account-context" class="account-context tenant-context" type="button" @click="copyCode">
              <el-icon><OfficeBuilding /></el-icon>
              <div>
                <span>{{ user.system_code || '商户编号' }}</span>
                <strong>{{ user.tenant_name || '当前商户' }}</strong>
              </div>
              <el-icon class="copy-hint"><CopyDocument /></el-icon>
            </button>

            <el-dropdown data-testid="profile-menu" trigger="click" @command="handleCommand">
              <button class="profile-trigger" type="button">
                <span class="profile-avatar">{{ userInitial }}</span>
                <span class="profile-copy">
                  <strong>{{ user.username || '未登录用户' }}</strong>
                  <small>{{ roleText(user.role) }}</small>
                </span>
                <el-icon class="profile-caret"><CaretBottom /></el-icon>
              </button>
              <template #dropdown>
                <el-dropdown-menu class="profile-dropdown">
                  <div class="dropdown-identity">
                    <span>当前身份</span>
                    <strong>{{ roleText(user.role) }}</strong>
                  </div>
                  <el-dropdown-item command="change-password">
                    <el-icon><Key /></el-icon>修改密码
                  </el-dropdown-item>
                  <el-dropdown-item command="logout" divided>
                    <el-icon><SwitchButton /></el-icon>退出登录
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </el-header>

        <el-main class="app-content">
          <div class="content-frame">
            <RouterView v-slot="{ Component }">
              <transition name="page-fade" mode="out-in">
                <component :is="Component" />
              </transition>
            </RouterView>
          </div>
        </el-main>
      </el-container>
    </el-container>

    <RouterView v-else />

    <AIAssistantBubble v-if="showAIAssistant" />

    <el-dialog v-model="passwordDialogVisible" title="修改登录密码" width="440px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="当前密码"><el-input v-model="passwordForm.currentPassword" type="password" show-password autocomplete="current-password" /></el-form-item>
        <el-form-item label="新密码"><el-input v-model="passwordForm.newPassword" type="password" show-password autocomplete="new-password" /></el-form-item>
        <el-form-item label="确认新密码"><el-input v-model="passwordForm.confirmPassword" type="password" show-password autocomplete="new-password" @keyup.enter="changePassword" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="passwordDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="passwordSaving" @click="changePassword">确认修改</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  CaretBottom, Connection, CopyDocument, CreditCard, Expand, Fold, Key, List, Location,
  Menu as MenuIcon, Money, Monitor, Odometer, OfficeBuilding, Operation, Reading, Setting,
  SwitchButton, Ticket, Tickets, TrendCharts, User, UserFilled, Warning
} from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'
import AIAssistantBubble from '@/components/AIAssistantBubble.vue'
import { hasPermission, tenantRoleLabel } from '@/utils/permissions'
import {
  activeCapabilitySet,
  activeSupplierBusinessTypeSet,
  configuredCapabilitySet,
  configuredSupplierBusinessTypeSet,
  readStoredUser,
  refreshStoredTenantIdentity,
} from '@/utils/tenantAccess'

type NavItem = { path: string; label: string; icon: any }
type NavGroup = { label: string; items: NavItem[] }

const route = useRoute()
const router = useRouter()
const isSuperAdmin = ref(false)
const user = ref<any>({})
const sidebarCollapsed = ref(localStorage.getItem('admin_sidebar_collapsed') === '1')
const mobileSidebarOpen = ref(false)
const passwordDialogVisible = ref(false)
const passwordSaving = ref(false)
const passwordForm = reactive({ currentPassword: '', newPassword: '', confirmPassword: '' })

const isLoginPage = computed(() => route.name === 'login' || route.name === 'platform-login')
const activeCapabilities = computed(() => activeCapabilitySet(user.value))
const configuredCapabilities = computed(() => configuredCapabilitySet(user.value))
const hasCapability = (value: string) => activeCapabilities.value.has(value)
const hasAnyCapability = (...values: string[]) => values.some(value => activeCapabilities.value.has(value))
const activeSupplierBusinessTypes = computed(() => activeSupplierBusinessTypeSet(user.value))
const configuredSupplierBusinessTypes = computed(() => configuredSupplierBusinessTypeSet(user.value))
const hasSupplierBusinessType = (value: string) => activeSupplierBusinessTypes.value.has(value)
const hasConfiguredSupplierBusinessType = (value: string) => configuredSupplierBusinessTypes.value.has(value)
const can = (permission: string) => hasPermission(user.value, permission)
// The server remains authoritative for every registered AI tool. This only
// controls whether a tenant user can discover the assistant: scenic suppliers
// retain the existing catalog preview entry, while distributors and travel
// agencies may enter for the read-only order/report tools they can already use.
const showAIAssistant = computed(() => {
  if (isLoginPage.value || isSuperAdmin.value || !can('agent.use')) return false
  if (hasCapability('supplier') && hasSupplierBusinessType('scenic')) return true
  const hasReadOnlyAgentSurface = can('orders.read') || can('reports.read') ||
    (hasCapability('distributor') && (can('distribution.read') || can('settlements.read'))) ||
    (hasCapability('travel_agency') && (can('teams.read') || can('settlements.read') || can('finance.read')))
  return hasReadOnlyAgentSurface && hasAnyCapability('distributor', 'travel_agency')
})

const navGroups = computed<NavGroup[]>(() => {
  const overview: NavGroup = { label: '概览', items: [{ path: '/', label: '控制台', icon: Odometer }] }
  if (isSuperAdmin.value) {
    const platformItems: NavItem[] = []
    if (user.value.role === 'platform_admin') {
      platformItems.push({ path: '/tenant', label: '商户开户管理', icon: OfficeBuilding })
      platformItems.push({ path: '/platform-users', label: '平台账号', icon: UserFilled })
      platformItems.push({ path: '/platform-ai', label: 'AI 助手配置', icon: Setting })
      platformItems.push({ path: '/platform-ai/quotas', label: 'AI 租户额度', icon: Operation })
    }
    platformItems.push({ path: '/platform-operations', label: '平台运营工作台', icon: Monitor })
    return [overview, { label: '平台管理', items: platformItems }]
  }

  const scenicSupplier = hasCapability('supplier') && hasSupplierBusinessType('scenic')
  const scenicHistorySupplier = hasCapability('supplier') && hasConfiguredSupplierBusinessType('scenic')
  const hotelHistorySupplier = configuredCapabilities.value.has('supplier') && hasConfiguredSupplierBusinessType('hotel')
  const currentHistoryTenant = scenicHistorySupplier || hasAnyCapability('distributor', 'travel_agency')
  const sales: NavItem[] = []
  if (scenicHistorySupplier && can('catalog.read')) {
    sales.push({ path: '/product', label: '线上门票', icon: Ticket })
    sales.push({ path: '/product/offline', label: '窗口门票', icon: Monitor })
  }
  if (currentHistoryTenant && can('orders.read')) sales.push({ path: '/online-order', label: '线上订单', icon: List })
  if (scenicHistorySupplier && can('onsite.read')) sales.push({ path: '/offline-order', label: '线下/窗口订单', icon: Tickets })

  const accommodation: NavItem[] = []
  if (hotelHistorySupplier && can('catalog.read')) accommodation.push({ path: '/hotel', label: '酒店经营', icon: OfficeBuilding })

  const distribution: NavItem[] = []
  if ((scenicHistorySupplier || hasCapability('distributor')) && can('distribution.read')) distribution.push({ path: '/distribution', label: '供销合作', icon: Connection })
  if ((scenicHistorySupplier || hasCapability('distributor')) && can('channels.read')) distribution.push({ path: '/channels', label: '渠道连接', icon: Connection })
  if ((scenicHistorySupplier || hasCapability('travel_agency')) && can('teams.read')) distribution.push({ path: '/teams', label: '旅行社团队', icon: Tickets })

  const operations: NavItem[] = []
  if ((scenicSupplier || hasCapability('distributor')) && can('operations.read')) operations.push({ path: '/operations', label: '运营工作台', icon: Operation })
  if (scenicSupplier && can('catalog.read')) operations.push({ path: '/policy', label: '政策知识库', icon: Reading })
  if (scenicSupplier && can('onsite.manage')) operations.push({ path: '/device', label: '终端设备', icon: Monitor })
  if (scenicSupplier && can('onsite.read')) operations.push({ path: '/checkpoint', label: '检票点位', icon: Location })
  if (scenicSupplier && can('catalog.read')) operations.push({ path: '/product/batch', label: '批量规则操作', icon: Operation })
  if (scenicSupplier && can('catalog.read')) operations.push({ path: '/agent-aliases', label: 'AI 业务别名', icon: Reading })

  const data: NavItem[] = []
  if ((scenicHistorySupplier || hasCapability('distributor')) && can('finance.read')) data.push({ path: '/finance', label: '财务报表', icon: Money })
  if (currentHistoryTenant && can('reports.read')) data.push({ path: '/report', label: '经营数据', icon: TrendCharts })
  if ((scenicHistorySupplier || hasCapability('distributor')) && can('refunds.read')) data.push({ path: '/refund-tasks', label: '退款待办', icon: Warning })
  if (currentHistoryTenant && can('after_sales.read')) data.push({ path: '/after-sales', label: '售后工作台', icon: Warning })

  const settings: NavItem[] = []
  if (scenicSupplier && can('onsite.manage')) settings.push({ path: '/staff', label: '员工管理', icon: User })
  if (can('tenant_accounts.manage')) settings.push({ path: '/system-user', label: '管理账号', icon: UserFilled })
  if ((scenicSupplier || hasCapability('distributor')) && can('payment_config.manage')) settings.push({ path: '/payment-config', label: '支付参数配置', icon: CreditCard })
  settings.push({ path: '/settings', label: '系统设置', icon: Setting })

  return [overview, { label: '销售中心', items: sales }, { label: '住宿经营', items: accommodation }, { label: '合作与渠道', items: distribution }, { label: '运营管理', items: operations }, { label: '数据与财务', items: data }, { label: '组织与设置', items: settings }]
    .filter(group => group.items.length)
})

const sidebarWidth = computed(() => sidebarCollapsed.value ? '76px' : '232px')
const currentGroupLabel = computed(() => navGroups.value.find(group => group.items.some(item => item.path === route.path))?.label || '工作台')
const userInitial = computed(() => String(user.value.username || '管').trim().slice(0, 1).toUpperCase())
const roleText = (role: string) => ({
  platform_admin: '平台管理员', platform_operator: '平台运营员', super_admin: '商户最高管理员', admin: '商户管理员',
  seller: '售票员', checker: '验票员'
} as Record<string, string>)[role] || tenantRoleLabel(role)

const toggleSidebar = () => {
  sidebarCollapsed.value = !sidebarCollapsed.value
  localStorage.setItem('admin_sidebar_collapsed', sidebarCollapsed.value ? '1' : '0')
}

const handleLogout = () => {
  const loginPath = user.value.scope === 'platform' ? '/platform/login' : '/login'
  localStorage.removeItem('token')
  localStorage.removeItem('user')
  ElMessage.success('退出登录成功')
  router.push(loginPath)
}

const handleCommand = (command: string) => {
  if (command === 'logout') handleLogout()
  if (command === 'change-password') {
    Object.assign(passwordForm, { currentPassword: '', newPassword: '', confirmPassword: '' })
    passwordDialogVisible.value = true
  }
}

const changePassword = async () => {
  if (!passwordForm.currentPassword || passwordForm.newPassword.length < 8) {
    ElMessage.warning('请填写当前密码，新密码长度至少8位')
    return
  }
  if (passwordForm.newPassword !== passwordForm.confirmPassword) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  passwordSaving.value = true
  try {
    const loginPath = user.value.scope === 'platform' ? '/platform/login' : '/login'
    await request.put('/auth/password', { current_password: passwordForm.currentPassword, new_password: passwordForm.newPassword })
    passwordDialogVisible.value = false
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    ElMessage.success('密码已修改，请重新登录')
    await router.push(loginPath)
  } finally {
    passwordSaving.value = false
  }
}

const copyCode = async () => {
  if (!user.value.system_code) return
  try {
    await navigator.clipboard.writeText(user.value.system_code)
    ElMessage.success('商户系统编号已复制')
  } catch {
    ElMessage.info(`商户系统编号：${user.value.system_code}`)
  }
}

const applyUser = (next: any) => {
  user.value = next || {}
  isSuperAdmin.value = user.value.scope === 'platform'
}

const loadUser = async (force = false) => {
  applyUser(readStoredUser())
  applyUser(await refreshStoredTenantIdentity(force))
}

const handleIdentityRefresh = (event: Event) => {
  applyUser((event as CustomEvent).detail)
}

const handleSessionExpired = () => {
  if (isLoginPage.value) return
  ElMessage.error('登录状态已失效，请重新登录')
  void router.push({ name: 'login' })
}

const handleWindowFocus = () => { void loadUser(true) }

watch(() => route.path, async () => {
  await loadUser()
  mobileSidebarOpen.value = false
})
onMounted(() => {
  window.addEventListener('tenant-identity-refreshed', handleIdentityRefresh)
  window.addEventListener('auth-session-expired', handleSessionExpired)
  window.addEventListener('focus', handleWindowFocus)
  void loadUser()
})
onBeforeUnmount(() => {
  window.removeEventListener('tenant-identity-refreshed', handleIdentityRefresh)
  window.removeEventListener('auth-session-expired', handleSessionExpired)
  window.removeEventListener('focus', handleWindowFocus)
})
</script>
