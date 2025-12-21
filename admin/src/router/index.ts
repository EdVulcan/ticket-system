import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
    history: createWebHistory(import.meta.env.BASE_URL),
    routes: [
        {
            path: '/',
            name: 'home',
            component: () => import('../views/HomeView.vue')
        },
        // Tenant management removed as per user request
        // {
        //   path: '/tenant',
        //   name: 'tenant',
        //   component: () => import('../views/TenantView.vue')
        // },
        {
            path: '/device',
            name: 'device',
            component: () => import('../views/DeviceView.vue')
        },
        {
            path: '/checkpoint',
            name: 'checkpoint',
            component: () => import('../views/CheckPointView.vue')
        },
        {
            path: '/product',
            name: 'product',
            component: () => import('../views/ProductView.vue')
        },
        {
            path: '/product/offline',
            name: 'offline-product',
            component: () => import('../views/OfflineProductView.vue')
        },
        {
            path: '/order',
            name: 'order',
            component: () => import('../views/OrderView.vue')
        }
        {
            path: '/login',
            name: 'login',
            component: () => import('../views/LoginView.vue')
        },
        {
            path: '/staff',
            name: 'staff',
            component: () => import('../views/StaffView.vue')
        }
    ]
})

router.beforeEach((to, from, next) => {
    const token = localStorage.getItem('token')
    if (to.name !== 'login' && !token) {
        next({ name: 'login' })
    } else {
        next()
    }
})

export default router
