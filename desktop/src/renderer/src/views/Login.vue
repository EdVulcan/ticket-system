<template>
  <main class="login-shell">
    <section class="terminal-panel">
      <div class="terminal-brand"><span><el-icon :size="25"><Ticket /></el-icon></span><strong>景区票务</strong></div>
      <div class="terminal-message">
        <p>窗口工作台</p>
        <h1>快速售票，清楚交班</h1>
        <div class="terminal-points">
          <span>售票与收款集中处理</span>
          <span>班次和设备归属可追溯</span>
          <span>硬件异常不会伪报成功</span>
        </div>
      </div>
      <div class="terminal-foot">本机终端</div>
    </section>

    <section class="login-panel">
      <div class="login-form-wrap">
        <div class="login-heading"><span>员工登录</span><h2>开始当前班次</h2><p>使用景区分配的系统编号和员工工号登录</p></div>
        <el-form ref="formRef" :model="form" :rules="rules" size="large" label-position="top">
          <el-form-item label="商户系统编号" prop="system_code">
            <el-input v-model="form.system_code" placeholder="请输入系统编号" :prefix-icon="OfficeBuilding" autofocus />
          </el-form-item>
          <el-form-item label="员工工号" prop="job_number">
            <el-input v-model="form.job_number" placeholder="请输入工号" :prefix-icon="User" />
          </el-form-item>
          <el-form-item label="登录密码" prop="password">
            <el-input v-model="form.password" type="password" placeholder="请输入密码" :prefix-icon="Lock" show-password @keyup.enter="handleLogin" />
          </el-form-item>
          <el-form-item class="login-submit">
            <el-button type="success" :loading="loading" class="w-full" @click="handleLogin">登录窗口端</el-button>
          </el-form-item>
        </el-form>
      </div>
    </section>
  </main>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import axios from 'axios'
import { Ticket, OfficeBuilding, User, Lock } from '@element-plus/icons-vue'
import { localizeErrorMessage } from '../utils/localize'

const router = useRouter()
const formRef = ref()
const loading = ref(false)

const form = reactive({
  system_code: '',
  job_number: '',
  password: ''
})

const rules = {
  system_code: [{ required: true, message: '请输入商户系统编号', trigger: 'blur' }],
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
        const res = await axios.post(`${import.meta.env.VITE_API_URL || 'http://127.0.0.1:8080/api/v1'}/auth/staff/login`, form)
        const { token, staff } = res.data
        sessionStorage.setItem('token', token)
        sessionStorage.setItem('staff', JSON.stringify(staff))
        ElMessage.success('登录成功')
        router.push('/')
      } catch (error: any) {
        ElMessage.error(localizeErrorMessage(error.response?.data?.error || error.message, '登录失败'))
      } finally {
        loading.value = false
      }
    }
  })
}
</script>

<style scoped>
.login-shell {
  --green: #16784a;
  height: 100vh;
  min-width: 900px;
  display: grid;
  grid-template-columns: minmax(340px, 42%) minmax(500px, 1fr);
  color: #20231f;
  background: #f4f6f2;
}
.terminal-panel {
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 34px 40px;
  color: #fff;
  background: #252c27;
  border-right: 1px solid #121713;
}
.terminal-brand { display: flex; align-items: center; gap: 10px; font-size: 17px; }
.terminal-brand span { width: 40px; height: 40px; display: grid; place-items: center; border-radius: 7px; background: #e6b94d; color: #252c27; }
.terminal-message { margin: auto 0; }
.terminal-message > p { margin: 0 0 8px; color: #e6b94d; font-size: 14px; font-weight: 700; }
.terminal-message h1 { max-width: 420px; margin: 0; font-size: 34px; line-height: 44px; }
.terminal-points { display: flex; flex-direction: column; gap: 10px; margin-top: 30px; color: #c3cbc4; font-size: 14px; }
.terminal-points span { display: flex; align-items: center; gap: 10px; }
.terminal-points span::before { content: ''; width: 7px; height: 7px; border-radius: 50%; background: #55be83; }
.terminal-foot { color: #818b82; font-size: 12px; }
.login-panel { display: flex; align-items: center; justify-content: center; padding: 40px; }
.login-form-wrap { width: min(390px, 100%); }
.login-heading { margin-bottom: 28px; }
.login-heading span { color: var(--green); font-size: 13px; font-weight: 700; }
.login-heading h2 { margin: 6px 0 0; font-size: 28px; line-height: 36px; }
.login-heading p { margin: 7px 0 0; color: #707870; font-size: 13px; }
.login-form-wrap :deep(.el-form-item) { margin-bottom: 20px; }
.login-form-wrap :deep(.el-form-item__label) { color: #454b45; font-weight: 600; }
.login-form-wrap :deep(.el-input__wrapper) { min-height: 46px; border-radius: 7px; box-shadow: 0 0 0 1px #cdd3cb inset; }
.login-form-wrap :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 2px var(--green) inset; }
.login-submit { margin-top: 28px; }
.login-submit :deep(.el-button) { height: 48px; border-radius: 7px; font-weight: 700; --el-button-bg-color: var(--green); --el-button-border-color: var(--green); --el-button-hover-bg-color: #0d5d38; --el-button-hover-border-color: #0d5d38; }

@media (max-width: 1024px) {
  .terminal-panel { padding: 28px; }
  .terminal-message h1 { font-size: 29px; line-height: 38px; }
}
</style>
