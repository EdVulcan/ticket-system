<template>
  <main class="mobile-verify-page">
    <section v-if="!isLoggedIn" class="mobile-auth-panel">
      <div class="mobile-brand">
        <span class="brand-dot" aria-hidden="true">✓</span>
        <div>
          <p class="eyebrow">悦聚游 · 现场工具</p>
          <h1>手机核销</h1>
        </div>
      </div>
      <p class="muted">使用验票员账号登录，在不方便安装闸机的点位完成扫码核销。</p>
      <form class="mobile-form" @submit.prevent="login">
        <label>系统编号<input v-model.trim="loginForm.system_code" autocomplete="organization" placeholder="例如 SYS001" required /></label>
        <label>员工工号<input v-model.trim="loginForm.job_number" autocomplete="username" placeholder="请输入工号" required /></label>
        <label>密码<input v-model="loginForm.password" type="password" autocomplete="current-password" placeholder="请输入密码" required /></label>
        <button class="primary-button" type="submit" :disabled="busy">{{ busy ? '登录中…' : '登录并开始' }}</button>
      </form>
      <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
    </section>

    <template v-else>
      <header class="mobile-topbar">
        <div>
          <p class="eyebrow">{{ tenantName || loginForm.system_code }}</p>
          <h1>手机核销</h1>
        </div>
        <button class="text-button" type="button" @click="logout">退出</button>
      </header>

      <section v-if="!sessionToken" class="setup-panel">
        <div class="section-heading">
          <div><p class="eyebrow">准备工作</p><h2>选择核销点位</h2></div>
          <button class="icon-button" type="button" title="刷新点位" @click="loadTargets">↻</button>
        </div>
        <label>检票点
          <select v-model.number="selectedCheckpointID" @change="selectDefaultDevice">
            <option :value="0">请选择检票点</option>
            <option v-for="checkpoint in checkpoints" :key="checkpoint.id" :value="checkpoint.id">{{ checkpoint.name }}{{ checkpoint.location ? ` · ${checkpoint.location}` : '' }}</option>
          </select>
        </label>
        <label>移动终端
          <select v-model.number="selectedDeviceID">
            <option :value="0">请选择移动终端</option>
            <option v-for="device in filteredDevices" :key="device.id" :value="device.id">{{ device.name }} · {{ device.serial_number }}</option>
          </select>
        </label>
        <p v-if="selectedCheckpointID && filteredDevices.length === 0" class="hint-text">该点位还没有绑定可用的手持设备，请先在后台设备管理中新增“handheld”设备并绑定点位。</p>
        <button class="primary-button" type="button" :disabled="busy || !selectedCheckpointID || !selectedDeviceID" @click="createSession">进入核销</button>
        <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
      </section>

      <section v-else class="scanner-panel">
        <div class="session-context">
          <div><span>当前点位</span><strong>{{ activeCheckpoint?.name || '未选择' }}</strong></div>
          <div><span>终端</span><strong>{{ activeDevice?.name || '移动终端' }}</strong></div>
          <button class="text-button" type="button" @click="closeSession()">切换</button>
        </div>

        <div v-if="scanResult" class="result-banner" :class="scanResult.result === 'allow' ? 'is-success' : 'is-deny'" role="status">
          <span class="result-icon" aria-hidden="true">{{ scanResult.result === 'allow' ? '✓' : '!' }}</span>
          <div><strong>{{ scanResult.result === 'allow' ? '核销成功' : '核销未通过' }}</strong><p>{{ scanResult.display_text || '请查看票券状态' }}</p></div>
        </div>

        <div class="camera-frame" :class="{ 'is-active': scanning }">
          <video ref="videoRef" playsinline muted></video>
          <div v-if="!scanning" class="camera-empty"><span class="camera-mark">⌁</span><strong>准备扫码</strong><small>允许浏览器使用相机后，将票券二维码放入框内</small></div>
          <div v-else class="scan-guide" aria-hidden="true"><span></span></div>
        </div>
        <p v-if="cameraMessage" class="hint-text">{{ cameraMessage }}</p>
        <button class="primary-button scan-button" type="button" :disabled="verifying" @click="toggleScanner">{{ scanning ? '停止相机' : '打开相机扫码' }}</button>
        <div class="fallback-row">
          <label class="secondary-button">上传二维码<input type="file" accept="image/*" @change="decodeImage" /></label>
          <button class="secondary-button" type="button" @click="manualEntryVisible = !manualEntryVisible">手动输入</button>
        </div>
        <form v-if="manualEntryVisible" class="manual-form" @submit.prevent="submitManual">
          <input v-model.trim="manualCode" placeholder="输入票码" autocomplete="off" required />
          <button class="secondary-button" type="submit" :disabled="verifying">核销</button>
        </form>
        <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
      </section>
    </template>
  </main>
</template>

<script setup lang="ts">
import axios from 'axios'
import { BrowserMultiFormatReader } from '@zxing/browser'
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'

const api = axios.create({ baseURL: import.meta.env.VITE_API_URL || '/api/v1', timeout: 15000 })
api.interceptors.request.use((config) => {
  const token = sessionStorage.getItem('mobile_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  const session = sessionToken.value
  if (session) config.headers['X-Mobile-Session'] = session
  return config
})

const loginForm = reactive({ system_code: '', job_number: '', password: '' })
const isLoggedIn = ref(Boolean(sessionStorage.getItem('mobile_token')))
const tenantName = ref(sessionStorage.getItem('mobile_tenant_name') || '')
const busy = ref(false)
const verifying = ref(false)
const errorMessage = ref('')
const cameraMessage = ref('')
const sessionToken = ref(sessionStorage.getItem('mobile_session') || '')
const sessionExpiresAt = ref('')
const checkpoints = ref<any[]>([])
const devices = ref<any[]>([])
const selectedCheckpointID = ref(Number(sessionStorage.getItem('mobile_checkpoint_id') || 0))
const selectedDeviceID = ref(Number(sessionStorage.getItem('mobile_device_id') || 0))
const scanResult = ref<any>(null)
const manualEntryVisible = ref(false)
const manualCode = ref('')
const scanning = ref(false)
const videoRef = ref<HTMLVideoElement | null>(null)
const reader = new BrowserMultiFormatReader()
let scannerControls: { stop: () => void } | null = null
let heartbeatTimer: number | undefined

const filteredDevices = computed(() => devices.value.filter((device) => Number(device.check_point_id || 0) === selectedCheckpointID.value))
const activeCheckpoint = computed(() => checkpoints.value.find((item) => Number(item.id) === selectedCheckpointID.value))
const activeDevice = computed(() => devices.value.find((item) => Number(item.id) === selectedDeviceID.value))

const setError = (message: string) => { errorMessage.value = message }
const clearError = () => { errorMessage.value = '' }

const login = async () => {
  busy.value = true
  clearError()
  try {
    const response = await api.post('/auth/staff/login', loginForm)
    sessionStorage.setItem('mobile_token', response.data.token)
    sessionStorage.setItem('mobile_staff', JSON.stringify(response.data.staff || {}))
    isLoggedIn.value = true
    const tenant = await api.get('/tenants/me')
    tenantName.value = tenant.data.name || ''
    sessionStorage.setItem('mobile_tenant_name', tenantName.value)
    await loadTargets()
  } catch (error: any) {
    setError(error.response?.data?.error || '登录失败，请检查系统编号、工号和密码')
  } finally {
    busy.value = false
  }
}

const loadTargets = async () => {
  if (!isLoggedIn.value) return
  busy.value = true
  clearError()
  try {
    const response = await api.get('/mobile/targets')
    checkpoints.value = response.data.checkpoints || []
    devices.value = response.data.devices || []
    if (!checkpoints.value.some((item) => Number(item.id) === selectedCheckpointID.value)) selectedCheckpointID.value = checkpoints.value.length === 1 ? checkpoints.value[0].id : 0
    selectDefaultDevice()
  } catch (error: any) {
    if (error.response?.status === 401) logout()
    else setError(error.response?.data?.error || '点位加载失败')
  } finally {
    busy.value = false
  }
}

const selectDefaultDevice = () => {
  if (!filteredDevices.value.some((device) => Number(device.id) === selectedDeviceID.value)) selectedDeviceID.value = filteredDevices.value.length === 1 ? filteredDevices.value[0].id : 0
}

const createSession = async () => {
  busy.value = true
  clearError()
  try {
    const response = await api.post('/mobile/sessions', { check_point_id: selectedCheckpointID.value, device_id: selectedDeviceID.value })
    sessionToken.value = response.data.session_token
    sessionExpiresAt.value = response.data.expires_at
    sessionStorage.setItem('mobile_session', sessionToken.value)
    sessionStorage.setItem('mobile_checkpoint_id', String(selectedCheckpointID.value))
    sessionStorage.setItem('mobile_device_id', String(selectedDeviceID.value))
    startHeartbeat()
  } catch (error: any) {
    setError(error.response?.data?.error || '无法建立移动核销会话')
  } finally {
    busy.value = false
  }
}

const startHeartbeat = () => {
  if (heartbeatTimer) window.clearInterval(heartbeatTimer)
  heartbeatTimer = window.setInterval(async () => {
    try { await api.post('/mobile/session/heartbeat') } catch (error: any) { if (error.response?.status === 401) closeSession(false) }
  }, 60000)
}

const closeSession = async (revoke = true) => {
  stopScanner()
  if (revoke && sessionToken.value) { try { await api.post('/mobile/session/close') } catch { /* session may already have expired */ } }
  sessionToken.value = ''
  sessionExpiresAt.value = ''
  scanResult.value = null
  if (heartbeatTimer) window.clearInterval(heartbeatTimer)
  heartbeatTimer = undefined
  sessionStorage.removeItem('mobile_session')
}

const logout = async () => {
  await closeSession()
  sessionStorage.removeItem('mobile_token')
  sessionStorage.removeItem('mobile_staff')
  sessionStorage.removeItem('mobile_tenant_name')
  isLoggedIn.value = false
  tenantName.value = ''
}

const toggleScanner = async () => {
  if (scanning.value) { stopScanner(); return }
  clearError(); cameraMessage.value = ''
  if (!navigator.mediaDevices?.getUserMedia) { cameraMessage.value = '当前浏览器不支持相机，请使用 HTTPS 的系统浏览器或改用上传/手动输入。'; return }
  try {
    scanning.value = true
    scannerControls = await reader.decodeFromVideoDevice(undefined, videoRef.value || undefined, (result, error) => {
      if (result && !verifying.value) {
        stopScanner()
        submitCode(result.getText())
      } else if (error && !String(error).includes('NotFoundException')) {
        cameraMessage.value = '相机已开启，请将二维码放入取景框。'
      }
    })
  } catch (error: any) {
    scanning.value = false
    cameraMessage.value = error?.name === 'NotAllowedError' ? '相机权限被拒绝，请在浏览器设置中允许相机后重试。' : '相机启动失败，请检查 HTTPS、浏览器权限或改用上传图片。'
  }
}

const stopScanner = () => {
  scannerControls?.stop()
  scannerControls = null
  scanning.value = false
}

const decodeImage = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  try {
    const url = URL.createObjectURL(file)
    const result = await reader.decodeFromImageUrl(url)
    URL.revokeObjectURL(url)
    submitCode(result.getText())
  } catch {
    cameraMessage.value = '没有识别出二维码，请换一张清晰图片或手动输入。'
  }
}

const normalizeTicketCode = (value: string) => {
  const text = value.trim()
  try {
    const parsed = new URL(text)
    return parsed.searchParams.get('ticket_code') || parsed.searchParams.get('code') || text
  } catch { return text }
}

const submitManual = () => submitCode(manualCode.value)

const submitCode = async (value: string) => {
  const ticketCode = normalizeTicketCode(value)
  if (!ticketCode || verifying.value || !sessionToken.value) return
  verifying.value = true
  clearError()
  try {
    const requestID = typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `mobile-${Date.now()}-${Math.random().toString(36).slice(2)}`
    const response = await api.post('/mobile/session/verify', { ticket_code: ticketCode, request_id: requestID })
    scanResult.value = response.data
    manualCode.value = ''
  } catch (error: any) {
    if (error.response?.status === 401) await closeSession(false)
    setError(error.response?.data?.error || '核销请求失败，请重试')
  } finally {
    verifying.value = false
  }
}

onMounted(() => {
  if (isLoggedIn.value) {
    loadTargets()
    if (sessionToken.value) startHeartbeat()
  }
})

onBeforeUnmount(() => {
  stopScanner()
  if (heartbeatTimer) window.clearInterval(heartbeatTimer)
})
</script>

<style scoped>
:global(body) { margin: 0; background: #f4f6f8; color: #16202a; }
.mobile-verify-page { min-height: 100dvh; padding: 22px 16px 34px; box-sizing: border-box; background: linear-gradient(180deg, #f4f6f8 0%, #eef2f4 100%); }
.mobile-auth-panel, .setup-panel, .scanner-panel { width: min(100%, 480px); margin: 0 auto; }
.mobile-auth-panel { padding-top: 10vh; }
.mobile-brand, .mobile-topbar, .section-heading, .session-context, .fallback-row, .manual-form { display: flex; align-items: center; }
.mobile-brand { gap: 12px; margin-bottom: 26px; }
.brand-dot { display: grid; place-items: center; width: 40px; height: 40px; border-radius: 13px; background: #14212b; color: #fff; font-weight: 800; }
.eyebrow { margin: 0 0 4px; color: #72808b; font-size: 12px; letter-spacing: .02em; }
h1, h2, p { margin-top: 0; }
h1 { margin-bottom: 0; font-size: 26px; letter-spacing: -.02em; }
h2 { margin-bottom: 0; font-size: 20px; }
.muted, .hint-text { color: #6a7781; font-size: 14px; line-height: 1.6; }
.mobile-form, .setup-panel { display: grid; gap: 14px; }
label { display: grid; gap: 7px; color: #3e4c57; font-size: 13px; font-weight: 650; }
input, select { width: 100%; box-sizing: border-box; min-height: 48px; border: 1px solid #d5dce1; border-radius: 12px; padding: 0 13px; background: #fff; color: #16202a; font: inherit; outline: none; }
input:focus, select:focus { border-color: #437d8c; box-shadow: 0 0 0 3px rgba(67, 125, 140, .13); }
button { font: inherit; cursor: pointer; }
.primary-button, .secondary-button { min-height: 48px; border-radius: 12px; border: 0; padding: 0 16px; font-weight: 700; }
.primary-button { background: #14212b; color: #fff; }
.primary-button:disabled { opacity: .45; cursor: not-allowed; }
.secondary-button { display: inline-flex; align-items: center; justify-content: center; background: #fff; color: #344651; border: 1px solid #d5dce1; }
.secondary-button input { display: none; }
.text-button, .icon-button { border: 0; background: transparent; color: #477985; font-weight: 700; padding: 6px; }
.icon-button { font-size: 24px; line-height: 1; }
.mobile-topbar { justify-content: space-between; margin: 4px auto 24px; width: min(100%, 480px); }
.section-heading { justify-content: space-between; margin-bottom: 10px; }
.setup-panel, .scanner-panel { background: rgba(255,255,255,.72); border: 1px solid rgba(211,220,225,.9); border-radius: 20px; padding: 18px; box-shadow: 0 12px 34px rgba(29, 49, 61, .07); }
.setup-panel label + label { margin-top: 2px; }
.hint-text { margin: 0; font-size: 12px; }
.error-text { margin: 0; color: #b83a3a; font-size: 13px; line-height: 1.5; }
.session-context { gap: 12px; padding-bottom: 16px; border-bottom: 1px solid #e1e6e9; }
.session-context > div { min-width: 0; flex: 1; }
.session-context span { display: block; color: #82909a; font-size: 11px; }
.session-context strong { display: block; margin-top: 3px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 14px; }
.result-banner { display: flex; align-items: center; gap: 11px; margin: 16px 0; padding: 13px; border-radius: 14px; }
.result-banner.is-success { background: #e7f5ef; color: #176546; }
.result-banner.is-deny { background: #fff0ee; color: #a63d3d; }
.result-icon { display: grid; place-items: center; width: 30px; height: 30px; border-radius: 50%; background: currentColor; color: #fff; font-weight: 800; }
.result-banner strong { display: block; font-size: 15px; }
.result-banner p { margin: 4px 0 0; color: inherit; opacity: .86; font-size: 13px; white-space: pre-line; }
.camera-frame { position: relative; overflow: hidden; aspect-ratio: 1 / 1; margin: 16px 0 10px; border-radius: 18px; background: #1d2931; }
.camera-frame video { width: 100%; height: 100%; object-fit: cover; display: block; }
.camera-empty { position: absolute; inset: 0; display: grid; place-content: center; justify-items: center; gap: 8px; color: #e6eef2; text-align: center; padding: 28px; }
.camera-empty small { color: #afc0c8; line-height: 1.5; }
.camera-mark { font-size: 42px; transform: rotate(-45deg); }
.scan-guide { position: absolute; inset: 14% 14%; border: 2px solid rgba(255,255,255,.8); border-radius: 18px; box-shadow: 0 0 0 999px rgba(15, 24, 29, .27); }
.scan-guide span { position: absolute; left: 8%; right: 8%; top: 50%; height: 2px; background: #78d4c1; box-shadow: 0 0 12px #78d4c1; }
.scan-button { width: 100%; }
.fallback-row { gap: 10px; margin-top: 10px; }
.fallback-row > * { flex: 1; }
.manual-form { gap: 8px; margin-top: 10px; }
.manual-form input { flex: 1; }
.manual-form button { min-width: 78px; }
@media (min-width: 600px) { .mobile-verify-page { padding-top: 7vh; } }
</style>
