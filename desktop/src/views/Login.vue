<template>
  <div class="login-container">
    <div class="login-box">
      <div class="logo">
        <el-icon :size="48" class="text-primary"><Ticket /></el-icon>
        <h1>TicketPro POS</h1>
      </div>
      <el-form :model="form" :rules="rules" ref="formRef" size="large">
        <el-form-item prop="job_number">
          <el-input v-model="form.job_number" placeholder="工号" prefix-icon="User" />
        </el-form-item>
        <el-form-item prop="password">
          <el-input v-model="form.password" type="password" placeholder="密码" prefix-icon="Lock" show-password @keyup.enter="handleLogin" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" class="w-full" @click="handleLogin">登录</el-button>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import axios from 'axios'

const router = useRouter()
const formRef = ref()
const loading = ref(false)

const form = reactive({
  job_number: '',
  password: ''
})

const rules = {
  job_number: [{ required: true, message: '请输入工号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }]
}

const handleLogin = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      loading.value = true
      try {
        // Use full URL for Electron dev or configure proxy
        const res = await axios.post('http://127.0.0.1:8080/api/v1/auth/staff/login', form)
        const { token, staff } = res.data
        localStorage.setItem('token', token)
        localStorage.setItem('staff', JSON.stringify(staff))
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
  height: 100vh;
  display: flex;
  justify-content: center;
  align-items: center;
  background-color: #1e293b; /* Slate 800 */
  color: white;
}
.login-box {
  width: 400px;
  padding: 40px;
  background: #0f172a; /* Slate 950 */
  border-radius: 16px;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
}
.logo {
  text-align: center;
  margin-bottom: 40px;
}
.logo h1 {
  margin-top: 10px;
  font-size: 24px;
  font-weight: bold;
}
</style>
