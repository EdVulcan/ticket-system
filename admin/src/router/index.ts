import { createRouter, createWebHistory } from 'vue-router'

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
            path: '/tenant',
            name: 'tenant',
            component: () => import('../views/TenantView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin'], title: '商户管理 (平台)' }
        },
        {
            path: '/platform-operations',
            name: 'platform-operations',
            component: () => import('../views/PlatformOperationsView.vue'),
            meta: { scope: 'platform', roles: ['platform_admin'], title: '平台运营工作台' }
        },
        {
            path: '/distribution',
            name: 'distribution',
            component: () => import('../views/DistributionView.vue'),
            meta: { scope: 'tenant', roles: ['admin', 'super_admin'], title: '分销中心' }
        },
        {
            path: '/channels',
            name: 'channels',
            component: () => import('../views/ChannelView.vue'),
            meta: { scope: 'tenant', roles: ['admin', 'super_admin'], title: '渠道连接' }
        },
        {
            path: '/teams',
            name: 'teams',
            component: () => import('../views/TeamView.vue'),
            meta: { scope: 'tenant', roles: ['seller', 'admin', 'super_admin'], capabilities: ['supplier', 'travel_agency'], title: '旅行社团队' }
        },
        {
            path: '/refund-tasks',
            name: 'refund-tasks',
            component: () => import('../views/RefundTaskView.vue'),
            meta: { scope: 'tenant', roles: ['admin', 'super_admin'], title: '退款待办' }
        },
        {
            path: '/after-sales',
            name: 'after-sales',
            component: () => import('../views/AfterSaleView.vue'),
            meta: { scope: 'tenant', roles: ['seller', 'admin', 'super_admin'], title: '售后工作台' }
        },
        {
            path: '/finance',
            name: 'finance',
            component: () => import('../views/FinanceView.vue'),
            meta: { title: '财务中心' }
        },
        {
            path: '/device',
            name: 'device',
            component: () => import('../views/DeviceView.vue'),
            meta: { scope: 'tenant', capability: 'supplier', title: '设备管理' }
        },
        {
            path: '/checkpoint',
            name: 'checkpoint',
            component: () => import('../views/CheckPointView.vue'),
            meta: { scope: 'tenant', capability: 'supplier', title: '检票点管理' }
        },
        {
            path: '/product',
            name: 'product',
            component: () => import('../views/ProductView.vue'),
            meta: { scope: 'tenant', capability: 'supplier', title: '产品管理' }
        },
        {
            path: '/product/offline',
            name: 'offline-product',
            component: () => import('../views/OfflineProductView.vue'),
            meta: { scope: 'tenant', capability: 'supplier', title: '窗口产品' }
        },
        {
            path: '/online-order',
            name: 'online-order',
            component: () => import('../views/OrderView.vue'),
            meta: { title: '线上订单' }
        },
        {
            path: '/offline-order',
            name: 'offline-order',
            component: () => import('../views/OfflineOrderView.vue'),
            meta: { title: '线下/窗口订单' }
        },
        {
            path: '/operations',
            name: 'operations',
            component: () => import('../views/OperationsView.vue'),
            meta: { scope: 'tenant', roles: ['admin', 'super_admin'], title: '运营工作台' }
        },
        {
            path: '/login',
            name: 'login',
            component: () => import('../views/LoginView.vue'),
            meta: { title: '登录' }
        },
        {
            path: '/staff',
            name: 'staff',
            component: () => import('../views/StaffView.vue'),
            meta: { title: '员工管理' }
        },
        {
            path: '/system-user',
            name: 'system-user',
            component: () => import('../views/UserView.vue'),
            meta: { title: '系统员管理' }
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
            meta: { scope: 'tenant', capability: 'supplier', title: '政策知识库' }
        },
        {
            path: '/report',
            name: 'report',
            component: () => import('../views/ReportView.vue'),
            meta: { title: '经营数据报表' }
        },
        {
            path: '/payment-config',
            name: 'payment-config',
            component: () => import('../views/PaymentConfigView.vue'),
            meta: { title: '支付参数配置' }
        },
        {
            path: '/gate-simulator',
            name: 'gate-simulator',
            component: () => import('../views/GateSimulator.vue'),
            meta: { title: '虚拟闸机模拟' }
        }
    ]
})

router.beforeEach((to, _from, next) => {
    const token = localStorage.getItem('token')
    if (to.name !== 'login' && !token) {
        next({ name: 'login' })
    } else if (to.name === 'login') {
        next()
    } else {
        let user: any = {}
        try { user = JSON.parse(localStorage.getItem('user') || '{}') } catch { /* invalid session */ }
        const requiredScope = to.meta.scope as string | undefined
        const roles = to.meta.roles as string[] | undefined
        const capability = to.meta.capability as string | undefined
        const capabilities = to.meta.capabilities as string[] | undefined
        const activeCapabilities = new Set((user.capabilities || []).filter((item: any) => item.status === 'active').map((item: any) => item.capability))
        const platformOnTenantRoute = user.scope === 'platform' && !requiredScope && to.name !== 'home'
        if (platformOnTenantRoute || (requiredScope && user.scope !== requiredScope) || (roles && !roles.includes(user.role)) || (capability && !activeCapabilities.has(capability)) || (capabilities && !capabilities.some(value => activeCapabilities.has(value)))) {
            next({ name: 'home' })
            return
        }
        next()
    }
})

export default router
