<template>
  <main class="login-shell">
    <section class="terminal-panel">
      <div class="terminal-brand">
        <span><el-icon :size="24"><Ticket /></el-icon></span>
        <div><strong>景区票务</strong><small>窗口端</small></div>
      </div>
      <div class="terminal-message">
        <p>当前岗位</p>
        <h1>售票员<br />工作台</h1>
        <div class="terminal-rule"></div>
        <span>窗口收银 · 订单查询 · 交接班</span>
      </div>
      <div class="terminal-foot"><i></i> 本机终端</div>
    </section>

    <section class="login-panel">
      <div class="login-form-wrap">
        <div class="login-heading"><span>员工登录</span><h2>开始当前班次</h2><p>验证当前窗口的操作人员身份</p></div>
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
const rememberedSystemCodeKey = 'pos_login_system_code'
const rememberedJobNumberKey = 'pos_login_job_number'

const form = reactive({
  system_code: localStorage.getItem(rememberedSystemCodeKey) || '',
  job_number: localStorage.getItem(rememberedJobNumberKey) || '',
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
        const systemCode = form.system_code.trim()
        const jobNumber = form.job_number.trim()
        const res = await axios.post(`${import.meta.env.VITE_API_URL || 'http://127.0.0.1:8080/api/v1'}/auth/staff/login`, {
          system_code: systemCode,
          job_number: jobNumber,
          password: form.password
        })
        const { token, staff } = res.data
        localStorage.setItem(rememberedSystemCodeKey, systemCode)
        localStorage.setItem(rememberedJobNumberKey, jobNumber)
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
  --green: #14734a;
  height: 100vh;
  min-width: 900px;
  display: grid;
  grid-template-columns: minmax(300px, 34%) minmax(500px, 1fr);
  color: #1d2420;
  background: #eef1ed;
}
.terminal-panel {
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 30px 34px;
  color: #fff;
  background: #18231d;
  border-right: 1px solid #121713;
}
.terminal-brand { display: flex; align-items: center; gap: 11px; }
.terminal-brand > span { width: 40px; height: 40px; display: grid; place-items: center; border-radius: 7px; background: #f3c95d; color: #18231d; }
.terminal-brand div { display: flex; flex-direction: column; gap: 1px; }
.terminal-brand strong { font-size: 16px; }
.terminal-brand small { color: #91a098; font-size: 11px; }
.terminal-message { margin: auto 0; }
.terminal-message > p { margin: 0 0 12px; color: #f3c95d; font-size: 13px; font-weight: 700; }
.terminal-message h1 { margin: 0; font-size: 38px; line-height: 48px; }
.terminal-message > span { color: #aeb9b1; font-size: 13px; }
.terminal-rule { width: 44px; height: 3px; margin: 24px 0 18px; border-radius: 2px; background: #25865a; }
.terminal-foot { display: flex; align-items: center; gap: 7px; color: #829087; font-size: 12px; }
.terminal-foot i { width: 7px; height: 7px; border-radius: 50%; background: #49be7d; box-shadow: 0 0 0 4px rgba(73, 190, 125, .1); }
.login-panel { display: flex; align-items: center; justify-content: center; padding: 40px; }
.login-form-wrap { width: min(400px, 100%); padding: 4px; }
.login-heading { margin-bottom: 28px; }
.login-heading span { color: var(--green); font-size: 13px; font-weight: 700; }
.login-heading h2 { margin: 6px 0 0; font-size: 29px; line-height: 38px; }
.login-heading p { margin: 7px 0 0; color: #707870; font-size: 13px; }
.login-form-wrap :deep(.el-form-item) { margin-bottom: 20px; }
.login-form-wrap :deep(.el-form-item__label) { color: #454b45; font-weight: 600; }
.login-form-wrap :deep(.el-input__wrapper) { min-height: 48px; border-radius: 7px; background: #fff; box-shadow: 0 0 0 1px #cbd2cb inset; }
.login-form-wrap :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 2px var(--green) inset; }
.login-submit { margin-top: 28px; }
.login-submit :deep(.el-button) { height: 50px; border-radius: 7px; font-weight: 700; --el-button-bg-color: var(--green); --el-button-border-color: var(--green); --el-button-hover-bg-color: #0d5d38; --el-button-hover-border-color: #0d5d38; }

@media (max-width: 1024px) {
  .terminal-panel { padding: 28px; }
  .terminal-message h1 { font-size: 29px; line-height: 38px; }
}
</style>
