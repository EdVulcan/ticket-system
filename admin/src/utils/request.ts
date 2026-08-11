import axios from 'axios'
import router from '@/router'
import { ElMessage } from 'element-plus'
import { localizeErrorMessage } from '@/utils/localize'

// Create axios instance
const service = axios.create({
  // Use relative path so proxy works in dev, and nginx works in prod
  // Alternatively can use env var: import.meta.env.VITE_API_URL
  baseURL: import.meta.env.VITE_API_URL || '/api/v1', 
  timeout: 10000 // Request timeout
})

// Request interceptor
service.interceptors.request.use(
  (config) => {
    // Add Authorization header
    const token = localStorage.getItem('token')
    if (token) {
      config.headers['Authorization'] = `Bearer ${token}`
    }
    return config
  },
  (error) => {
    return Promise.reject(error)
  }
)

// Response interceptor
service.interceptors.response.use(
  (response) => {
    return response
  },
  (error) => {
    const skipErrorToast = Boolean((error.config as any)?.skipErrorToast)
    if (error.response) {
      const status = error.response.status
      if (status === 401) {
        let loginPath = router.currentRoute.value.path.startsWith('/platform') ? '/platform/login' : '/login'
        try {
          const user = JSON.parse(localStorage.getItem('user') || '{}')
          if (user.scope === 'platform') loginPath = '/platform/login'
        } catch { /* invalid session */ }
        // Clear token
        localStorage.removeItem('token')
        localStorage.removeItem('user')
        
        const currentPath = router.currentRoute.value.path
        if (currentPath !== '/login' && currentPath !== '/platform/login') {
            ElMessage.error('登录状态已失效，请重新登录')
            router.push(loginPath)
        }
      } else {
        const message = localizeErrorMessage(error.response.data?.error, '请求失败，请稍后重试')
        if (error.response.data && typeof error.response.data === 'object') error.response.data.error = message
        error.message = message
        if (!skipErrorToast) ElMessage.error(message)
      }
    } else {
      const message = localizeErrorMessage(error.message, '网络连接异常')
      error.message = message
      if (!skipErrorToast) ElMessage.error(message)
    }
    return Promise.reject(error)
  }
)

export default service
