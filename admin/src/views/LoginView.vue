<template>
  <div class="login-container">
    <el-card class="login-card">
      <template #header>
        <div class="login-header">
          <h2>景区票务管理系统</h2>
          <p class="text-xs text-gray-400 mt-2">Ticket SaaS Platform</p>
        </div>
      </template>
      <el-form :model="form" :rules="rules" ref="formRef" label-width="0">
        <el-form-item>
          <el-segmented v-model="mode" :options="[{ label: 'Tenant', value: 'tenant' }, { label: 'Platform', value: 'platform' }]" class="w-full" />
        </el-form-item>
        <el-form-item v-if="mode === 'tenant'" prop="system_code">
           <el-input v-model="form.system_code" placeholder="系统编号 (System Code)" prefix-icon="OfficeBuilding" />
        </el-form-item>
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="用户名 (Username)" prefix-icon="User" />
        </el-form-item>
        <el-form-item prop="password">
          <el-input v-model="form.password" type="password" placeholder="密码 (Password)" prefix-icon="Lock" show-password @keyup.enter="handleLogin" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" class="login-button" @click="handleLogin">登 录</el-button>
        </el-form-item>
      </el-form>
      <div class="text-center text-xs text-gray-400 mt-4">
        <p>未获得授权请联系平台管理员</p>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const router = useRouter()
const formRef = ref()
const loading = ref(false)
const mode = ref<'tenant' | 'platform'>('tenant')

const form = reactive({
  system_code: '',
  username: '',
  password: ''
})

const rules = {
  system_code: [{ required: true, message: '请输入系统编号', trigger: 'blur' }],
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }]
}

const handleLogin = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      loading.value = true
      try {
        const endpoint = mode.value === 'platform' ? '/auth/platform/login' : '/auth/login'
        const payload = mode.value === 'platform' ? { username: form.username, password: form.password } : form
        const res = await request.post(endpoint, payload)
        const { token, user } = res.data
        localStorage.setItem('token', token)
        localStorage.setItem('user', JSON.stringify(user))
        ElMessage.success('登录成功')
        router.push('/')
      } catch (error: any) {
        ElMessage.error(error.response?.data?.error || '登录失败')
      } finally {
        loading.value = false
      }
    }
  })
}
</script>

<style scoped>
.login-container {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 100vh;
  background-color: #f0f2f5;
  background-image: linear-gradient(135deg, #f5f7fa 0%, #c3cfe2 100%);
}
.login-card {
  width: 400px;
  border-radius: 12px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
}
.login-header {
  text-align: center;
  padding: 10px 0;
}
.login-header h2 {
    font-size: 24px;
    font-weight: 600;
    color: #333;
}
.login-button {
  width: 100%;
  padding: 12px 0;
  font-size: 16px;
  border-radius: 6px;
}
</style>
