import { createRouter, createWebHistory } from 'vue-router'
import { hasPermission } from '@/utils/permissions'
import {
    activeCapabilitySet,
    activeSupplierBusinessTypeSet,
    configuredCapabilitySet,
    configuredSupplierBusinessTypeSet,
    readStoredUser,
    refreshStoredTenantIdentity,
} from '@/utils/tenantAccess'

const router = createRouter({
    history: createWebHistory(import.meta.env.BASE_URL),
    routes: [
        {
            path: '/',
            name: 'home',
            component: () => import('../views/HomeView.vue'),
            meta: { title: '控制台' }
        },
        {
            path: '/execution-center',
            name: 'execution-center',
            component: () => import('../views/ExecutionCenterView.vue'),
            meta: { scope: 'tenant', roles: ['admin', 'super_admin', 'product_operator', 'team_operator', 'settlement_operator', 'viewer'], permission: 'operations.read', capabilities: ['supplier', 'distributor', 'travel_agency'], title: '可信执行中心' }
        },
        {
            path: '/tenant',
            name: 'tenant',
            component: () => import('../views/TenantView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin'], title: '商户管理 (平台)' }
        },
        {
            path: '/platform-operations',
            name: 'platform-operations',
            component: () => import('../views/PlatformOperationsView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin', 'platform_operator'], title: '平台运营工作台' }
        },
        {
            path: '/platform-users',
            name: 'platform-users',
            component: () => import('../views/PlatformUserView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin'], title: '平台账号' }
        },
        {
            path: '/platform-ai',
            name: 'platform-ai',
            component: () => import('../views/PlatformAIConfigView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin'], title: 'AI 助手配置' }
        },
        {
            path: '/platform-ai/quotas',
            name: 'platform-ai-quotas',
            component: () => import('../views/PlatformAIQuotaView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin'], title: 'AI 租户额度' }
        },
        {
            path: '/distribution',
            name: 'distribution',
            component: () => import('../views/DistributionView.vue'),
            meta: { scope: 'tenant', permission: 'distribution.read', capabilities: ['supplier', 'distributor'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor'], title: '供销合作' }
        },
        {
            path: '/channels',
            name: 'channels',
            component: () => import('../views/ChannelView.vue'),
            meta: { scope: 'tenant', permission: 'channels.read', capabilities: ['supplier', 'distributor'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor'], title: '渠道连接' }
        },
        {
            path: '/teams',
            name: 'teams',
            component: () => import('../views/TeamView.vue'),
            meta: { scope: 'tenant', permission: 'teams.read', capabilities: ['supplier', 'travel_agency'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['travel_agency'], title: '旅行社团队' }
        },
        {
            path: '/refund-tasks',
            name: 'refund-tasks',
            component: () => import('../views/RefundTaskView.vue'),
            meta: { scope: 'tenant', permission: 'refunds.read', capabilities: ['supplier', 'distributor'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor'], title: '退款待办' }
        },
        {
            path: '/after-sales',
            name: 'after-sales',
            component: () => import('../views/AfterSaleView.vue'),
            meta: { scope: 'tenant', permission: 'after_sales.read', capabilities: ['supplier', 'distributor', 'travel_agency'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor', 'travel_agency'], title: '售后工作台' }
        },
        {
            path: '/finance',
            name: 'finance',
            component: () => import('../views/FinanceView.vue'),
            meta: { scope: 'tenant', permission: 'finance.read', capabilities: ['supplier', 'distributor'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor'], title: '财务报表' }
        },
        {
            path: '/device',
            name: 'device',
            component: () => import('../views/DeviceView.vue'),
            meta: { scope: 'tenant', permissions: ['onsite.read', 'onsite.maintenance', 'onsite.manage'], capability: 'supplier', supplierBusinessType: 'scenic', title: '设备管理' }
        },
        {
            path: '/checkpoint',
            name: 'checkpoint',
            component: () => import('../views/CheckPointView.vue'),
            meta: { scope: 'tenant', permission: 'onsite.read', capability: 'supplier', supplierBusinessType: 'scenic', title: '检票点管理' }
        },
        {
            path: '/product',
            name: 'product',
            component: () => import('../views/ProductView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, title: '线上门票' }
        },
        {
            path: '/product/batch',
            name: 'product-batch',
            component: () => import('../views/CatalogBatchChangeView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, title: '批量规则操作' }
        },
        {
            path: '/print-templates',
            name: 'print-templates',
            component: () => import('../views/PrintTemplateView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, title: '门票打印模板' }
        },
        {
            path: '/agent-aliases',
            name: 'agent-aliases',
            component: () => import('../views/AgentAliasView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, title: 'AI 业务别名' }
        },
        {
            path: '/product/offline',
            name: 'offline-product',
            component: () => import('../views/OfflineProductView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, title: '窗口门票' }
        },
        {
            path: '/online-order',
            name: 'online-order',
            component: () => import('../views/OrderView.vue'),
            meta: { scope: 'tenant', permission: 'orders.read', capabilities: ['supplier', 'distributor', 'travel_agency'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor', 'travel_agency'], title: '线上订单' }
        },
        {
            path: '/offline-order',
            name: 'offline-order',
            component: () => import('../views/OfflineOrderView.vue'),
            meta: { scope: 'tenant', permission: 'onsite.read', capability: 'supplier', supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, title: '线下/窗口订单' }
        },
        {
            path: '/operations',
            name: 'operations',
            component: () => import('../views/OperationsView.vue'),
            meta: { scope: 'tenant', permission: 'operations.read', capabilities: ['supplier', 'distributor'], supplierBusinessType: 'scenic', supplierBusinessTypeAlternativeCapabilities: ['distributor'], title: '运营工作台' }
        },
        {
            path: '/login',
            name: 'login',
            component: () => import('../views/LoginView.vue'),
            meta: { title: '商户登录' }
        },
        {
            path: '/hotel',
            name: 'hotel',
            component: () => import('../views/HotelView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', capabilityAllowSuspended: true, supplierBusinessType: 'hotel', supplierBusinessTypeAllowSuspended: true, title: '酒店经营' }
        },
        {
            path: '/platform/login',
            name: 'platform-login',
            component: () => import('../views/PlatformLoginView.vue'),
            meta: { title: '平台登录', scope: 'platform' }
        },
        {
            path: '/staff',
            name: 'staff',
            component: () => import('../views/StaffView.vue'),
            meta: { scope: 'tenant', permission: 'onsite.manage', capability: 'supplier', supplierBusinessType: 'scenic', title: '员工管理' }
        },
        {
            path: '/system-user',
            name: 'system-user',
            component: () => import('../views/UserView.vue'),
            meta: { scope: 'tenant', permission: 'tenant_accounts.manage', title: '管理账号' }
        },
        {
            path: '/settings',
            name: 'settings',
            component: () => import('../views/SettingsView.vue'),
            meta: { title: '系统设置' }
        },
        {
            path: '/policy',
            name: 'policy',
            component: () => import('../views/PolicyView.vue'),
            meta: { scope: 'tenant', permission: 'catalog.read', capability: 'supplier', supplierBusinessType: 'scenic', title: '政策知识库' }
        },
        {
            path: '/report',
            name: 'report',
            component: () => import('../views/ReportView.vue'),
            meta: { scope: 'tenant', permission: 'reports.read', capabilities: ['supplier', 'distributor', 'travel_agency'], supplierBusinessType: 'scenic', supplierBusinessTypeAllowSuspended: true, supplierBusinessTypeAlternativeCapabilities: ['distributor', 'travel_agency'], title: '经营数据报表' }
        },
        {
            path: '/payment-config',
            name: 'payment-config',
            component: () => import('../views/PaymentConfigView.vue'),
            meta: { scope: 'tenant', permission: 'payment_config.manage', capabilities: ['supplier', 'distributor'], supplierBusinessType: 'scenic', supplierBusinessTypeAlternativeCapabilities: ['distributor'], title: '支付参数配置' }
        },
        {
            path: '/gate-simulator',
            name: 'gate-simulator',
            component: () => import('../views/GateSimulator.vue'),
            meta: { scope: 'tenant', permission: 'onsite.manage', capability: 'supplier', supplierBusinessType: 'scenic', title: '闸机联调说明' }
        }
    ]
})

router.beforeEach(async (to, _from, next) => {
    const token = localStorage.getItem('token')
    const isLoginRoute = to.name === 'login' || to.name === 'platform-login'
    if (!isLoginRoute && !token) {
        next({ name: to.meta.scope === 'platform' ? 'platform-login' : 'login' })
    } else if (isLoginRoute) {
        next()
    } else {
        let user: any = readStoredUser()
        user = await refreshStoredTenantIdentity()
        if (!localStorage.getItem('token')) {
            next({ name: to.meta.scope === 'platform' ? 'platform-login' : 'login' })
            return
        }
        const requiredScope = to.meta.scope as string | undefined
        const roles = to.meta.roles as string[] | undefined
        const capability = to.meta.capability as string | undefined
        const capabilityAllowSuspended = Boolean(to.meta.capabilityAllowSuspended)
        const capabilities = to.meta.capabilities as string[] | undefined
        const supplierBusinessType = to.meta.supplierBusinessType as string | undefined
        const supplierBusinessTypeAllowSuspended = Boolean(to.meta.supplierBusinessTypeAllowSuspended)
        const supplierBusinessTypeAlternativeCapabilities = to.meta.supplierBusinessTypeAlternativeCapabilities as string[] | undefined
        const permission = to.meta.permission as string | undefined
        const permissions = to.meta.permissions as string[] | undefined
        const activeCapabilities = activeCapabilitySet(user)
        const configuredCapabilities = configuredCapabilitySet(user)
        const activeSupplierBusinessTypes = activeSupplierBusinessTypeSet(user)
        const configuredSupplierBusinessTypes = configuredSupplierBusinessTypeSet(user)
        const platformOnTenantRoute = user.scope === 'platform' && !requiredScope && to.name !== 'home'
        const allowedSupplierBusinessTypes = supplierBusinessTypeAllowSuspended ? configuredSupplierBusinessTypes : activeSupplierBusinessTypes
        const missingSupplierBusinessType = supplierBusinessType && !allowedSupplierBusinessTypes.has(supplierBusinessType) && !supplierBusinessTypeAlternativeCapabilities?.some(value => activeCapabilities.has(value))
        const allowedCapabilities = capabilityAllowSuspended ? configuredCapabilities : activeCapabilities
        const missingPermission = permission && !hasPermission(user, permission)
        const missingAnyPermission = permissions && !permissions.some(value => hasPermission(user, value))
        if (platformOnTenantRoute || (requiredScope && user.scope !== requiredScope) || (roles && !roles.includes(user.role)) || missingPermission || missingAnyPermission || (capability && !allowedCapabilities.has(capability)) || (capabilities && !capabilities.some(value => activeCapabilities.has(value))) || missingSupplierBusinessType) {
            next({ name: 'home' })
            return
        }
        next()
    }
})

export default router
