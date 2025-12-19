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
            path: '/order',
            name: 'order',
            component: () => import('../views/OrderView.vue')
        }
    ]
})

export default router
