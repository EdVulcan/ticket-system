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
            meta: { role: 'super_admin', title: '商户管理 (平台)' }
        },
        {
            path: '/distribution',
            name: 'distribution',
            component: () => import('../views/DistributionView.vue'),
            meta: { role: 'admin', title: '分销中心' }
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
            meta: { title: '设备管理' }
        },
        {
            path: '/checkpoint',
            name: 'checkpoint',
            component: () => import('../views/CheckPointView.vue'),
            meta: { title: '检票点管理' }
        },
        {
            path: '/product',
            name: 'product',
            component: () => import('../views/ProductView.vue'),
            meta: { title: '产品管理' }
        },
        {
            path: '/product/offline',
            name: 'offline-product',
            component: () => import('../views/OfflineProductView.vue'),
            meta: { title: '窗口产品' }
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
            meta: { title: '政策知识库' }
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
    } else {
        next()
    }
})

export default router
