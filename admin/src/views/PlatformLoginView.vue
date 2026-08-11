<template>
  <div class="login-container">
    <el-card class="login-card">
      <template #header>
        <div class="login-header">
          <h2>票务平台管理中心</h2>
          <p class="text-xs text-gray-400 mt-2">系统服务商登录</p>
        </div>
      </template>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="0">
        <el-form-item prop="username">
          <el-input v-model="form.username" placeholder="平台用户名" prefix-icon="User" />
        </el-form-item>
        <el-form-item prop="password">
          <el-input v-model="form.password" type="password" placeholder="密码" prefix-icon="Lock" show-password @keyup.enter="handleLogin" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" class="login-button" @click="handleLogin">登 录</el-button>
        </el-form-item>
      </el-form>
      <div class="text-center text-xs text-gray-400 mt-4">
        <p>仅限平台管理人员使用</p>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const router = useRouter()
const formRef = ref()
const loading = ref(false)
const form = reactive({ username: '', password: '' })
const rules = {
  username: [{ required: true, message: '请输入平台用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

const handleLogin = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (!valid) return
    loading.value = true
    try {
      const response = await request.post('/auth/platform/login', form)
      const { token, user } = response.data
      localStorage.setItem('token', token)
      localStorage.setItem('user', JSON.stringify(user))
      ElMessage.success('登录成功')
      router.push('/')
    } catch (error: any) {
      ElMessage.error(error.response?.data?.error || '登录失败')
    } finally {
      loading.value = false
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
  background: #f3f6fb;
}
.login-card {
  width: 400px;
  border: 1px solid #dfe6f0;
  border-radius: 8px;
  box-shadow: 0 18px 48px rgba(31, 47, 76, 0.12);
}
.login-header {
  text-align: center;
  padding: 10px 0;
}
.login-header h2 {
  font-size: 24px;
  font-weight: 600;
  color: #18202b;
}
.login-button {
  width: 100%;
  padding: 12px 0;
  font-size: 16px;
  border-radius: 6px;
}
</style>
