import { createRouter, createWebHashHistory } from 'vue-router'
import Login from '../views/Login.vue'
import Home from '../views/SalesView.vue'

const routes = [
    {
        path: '/login',
        name: 'login',
        component: Login
    },
    {
        path: '/',
        name: 'home',
        component: Home
    }
]

const router = createRouter({
    history: createWebHashHistory(),
    routes
})

router.beforeEach((to, _from, next) => {
    const token = sessionStorage.getItem('token')
    if (to.name !== 'login' && !token) {
        next({ name: 'login' })
    } else {
        next()
    }
})

export default router
