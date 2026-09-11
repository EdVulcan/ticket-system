<template>
  <main class="mobile-verify-page" data-testid="mobile-verify-page">
    <div class="mobile-shell">
      <header class="mobile-app-header" :class="{ 'is-authenticated': isLoggedIn }">
        <div class="brand-block">
          <span class="brand-mark" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
              <path d="M6.5 8.5h11v9h-11z" />
              <path d="M8.5 8.5V6.8a3.5 3.5 0 0 1 7 0v1.7M9.5 13h5M12 10.8v4.4" />
            </svg>
          </span>
          <div class="brand-copy">
            <span>现场验票</span>
            <strong>{{ isLoggedIn ? (tenantName || loginForm.system_code || '手机核销') : '手机核销' }}</strong>
          </div>
        </div>

        <div v-if="isLoggedIn" class="header-actions">
          <span class="connection-status" :class="`is-${connectionState}`" data-testid="connection-status">
            <i aria-hidden="true"></i>{{ connectionLabel }}
          </span>
          <button class="icon-button" type="button" aria-label="退出登录" :disabled="busy || sessionRestoring || verifying || verificationPhase === 'uncertain'" @click="logout">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M10 5H5.8A1.8 1.8 0 0 0 4 6.8v10.4A1.8 1.8 0 0 0 5.8 19H10M14 8l4 4-4 4M18 12H9" />
            </svg>
          </button>
        </div>
      </header>

      <section v-if="!isLoggedIn" class="auth-view">
        <div class="screen-intro">
          <span class="screen-kicker">现场验票</span>
          <h1>手机核销</h1>
          <p>登录后选择工作点，打开相机即可连续核验。</p>
        </div>

        <form class="surface auth-surface" @submit.prevent="login">
          <label class="field">
            <span>系统编号</span>
            <input v-model.trim="loginForm.system_code" autocomplete="organization" placeholder="例如 SYS001" required />
          </label>
          <label class="field">
            <span>员工工号</span>
            <input v-model.trim="loginForm.job_number" autocomplete="username" placeholder="请输入工号" required />
          </label>
          <label class="field">
            <span>密码</span>
            <input v-model="loginForm.password" type="password" autocomplete="current-password" placeholder="请输入密码" required />
          </label>
          <button class="primary-button" type="submit" :disabled="busy">
            <span>{{ busy ? '登录中…' : '登录并开始' }}</span>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M5 12h13M13 6l6 6-6 6" />
            </svg>
          </button>
          <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
        </form>
      </section>

      <section v-else-if="sessionRestoring" class="restore-view" data-testid="session-restoring">
        <div class="restore-surface">
          <span class="restore-spinner" aria-hidden="true"></span>
          <strong>正在恢复工作台</strong>
          <p>正在确认点位和会话状态，请稍候。</p>
        </div>
      </section>

      <section v-else-if="!sessionToken" class="setup-view">
        <div class="screen-intro setup-intro">
          <span class="screen-kicker">开始前设置</span>
          <h1>选择核销点位</h1>
          <p>只显示当前账号已授权的检票点和移动终端。</p>
        </div>

        <div class="surface setup-surface">
          <label class="field">
            <span>检票点</span>
            <span class="select-wrap">
              <select v-model.number="selectedCheckpointID" data-testid="checkpoint-select" @change="selectDefaultDevice">
                <option :value="0">请选择检票点</option>
                <option v-for="checkpoint in checkpoints" :key="checkpoint.id" :value="checkpoint.id">
                  {{ checkpoint.name }}{{ checkpoint.location ? ` · ${checkpoint.location}` : '' }}
                </option>
              </select>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="m7 10 5 5 5-5" />
              </svg>
            </span>
          </label>

          <label class="field">
            <span>移动终端</span>
            <span class="select-wrap">
              <select v-model.number="selectedDeviceID" data-testid="device-select">
                <option :value="0">请选择移动终端</option>
                <option v-for="device in filteredDevices" :key="device.id" :value="device.id">
                  {{ device.name }} · {{ device.serial_number }}
                </option>
              </select>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="m7 10 5 5 5-5" />
              </svg>
            </span>
          </label>

          <div v-if="selectedCheckpointID" class="target-note" :class="{ 'is-empty': filteredDevices.length === 0 }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="9" /><path d="M12 10v5M12 7.5h.01" />
            </svg>
            <span v-if="filteredDevices.length">{{ filteredDevices.length }} 台可用移动终端</span>
            <span v-else>该点位还没有绑定可用的手持设备，请先在后台设备管理中完成绑定。</span>
          </div>

          <button class="primary-button" type="button" :disabled="busy || !selectedCheckpointID || !selectedDeviceID" @click="createSession">
            <span>{{ busy ? '连接中…' : '进入核销' }}</span>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M5 12h13M13 6l6 6-6 6" />
            </svg>
          </button>
          <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>
        </div>

        <button class="quiet-action" type="button" @click="loadTargets" :disabled="busy">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M20 11a8 8 0 0 0-14.8-4.2L4 9M4 5v4h4M4 13a8 8 0 0 0 14.8 4.2L20 15M20 19v-4h-4" />
          </svg>
          <span>{{ busy ? '正在刷新' : '刷新点位' }}</span>
        </button>
      </section>

      <section v-else class="verify-view">
        <div class="workspace-toolbar">
          <button class="location-button" type="button" :disabled="verifying || verificationPhase === 'uncertain'" @click="closeSession()">
            <span class="toolbar-icon" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 20s6-5.2 6-10a6 6 0 1 0-12 0c0 4.8 6 10 6 10Z" /><circle cx="12" cy="10" r="2" />
              </svg>
            </span>
            <span class="location-copy">
              <small>当前检票点</small>
              <strong>{{ activeCheckpoint?.name || '未选择' }}</strong>
            </span>
            <svg class="chevron-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="m9 18 6-6-6-6" />
            </svg>
          </button>
          <div class="device-copy">
            <span>{{ activeDevice?.name || '移动终端' }}</span>
            <small>{{ activeDevice?.serial_number || '' }}</small>
          </div>
          <button class="icon-button light" type="button" aria-label="刷新连接" :disabled="heartbeatBusy || verifying" @click="sendHeartbeat">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M20 11a8 8 0 0 0-14.8-4.2L4 9M4 5v4h4M4 13a8 8 0 0 0 14.8 4.2L20 15M20 19v-4h-4" />
            </svg>
          </button>
        </div>

        <div class="session-strip">
          <span class="session-state" :class="`is-${connectionState}`"><i aria-hidden="true"></i>{{ connectionLabel }}</span>
          <span>{{ lastHeartbeatText }}</span>
          <span>{{ sessionExpiryText }}</span>
        </div>

        <div class="scan-stage" :class="{ 'is-scanning': scanning, 'is-starting': scannerStarting, 'is-processing': verifying }" data-testid="scan-stage">
          <video ref="videoRef" playsinline muted></video>
          <div v-if="!scanning || scannerStarting" class="camera-empty">
            <span class="camera-mark" aria-hidden="true">
              <svg viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                <path d="M15 8h-3a4 4 0 0 0-4 4v3M33 8h3a4 4 0 0 1 4 4v3M15 40h-3a4 4 0 0 1-4-4v-3M33 40h3a4 4 0 0 0 4-4v-3" />
                <path d="M18 20h12v8H18zM21 17h6M21 31h6" />
              </svg>
            </span>
            <strong>{{ verifying ? '正在核验' : scannerStarting ? '正在开启相机' : scanResult ? '准备下一张票' : '对准票券二维码' }}</strong>
            <small>{{ verifying ? '请稍候，正在确认票券状态' : scannerStarting ? '请允许浏览器使用相机' : '保持二维码完整出现在取景框内' }}</small>
          </div>
          <div v-else class="scan-guide" aria-hidden="true"><span></span></div>
          <span class="camera-badge" :class="{ 'is-live': scanning }">
            <i aria-hidden="true"></i>{{ scannerStarting ? '正在开启' : scanning ? '相机已开启' : '相机待机' }}
          </span>
        </div>

        <div v-if="verifying" class="processing-strip" role="status" aria-live="polite">
          <span class="spinner" aria-hidden="true"></span>
          <span>正在核验，请不要关闭页面</span>
        </div>

        <div v-if="scanResult" class="result-card" :class="resultIsSuccess ? 'is-success' : 'is-deny'" role="status" aria-live="polite" data-testid="verification-result">
          <span class="result-icon" aria-hidden="true">
            <svg v-if="resultIsSuccess" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
              <path d="m5 12 4.5 4.5L19 7" />
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
              <path d="M6 6l12 12M18 6 6 18" />
            </svg>
          </span>
          <div class="result-copy">
            <span class="result-kicker">{{ resultIsSuccess ? '核验通过' : '核验结果' }}</span>
            <h2>{{ resultTitle }}</h2>
            <p>{{ resultDetail }}</p>
          </div>
          <span class="result-time">{{ resultTimeText }}</span>
        </div>

        <div v-if="verificationPhase === 'uncertain'" class="uncertain-card" role="alert">
          <span class="uncertain-icon" aria-hidden="true">?</span>
          <div>
            <strong>结果待确认</strong>
            <p>网络没有返回最终结果，请重试上一笔核验。系统会沿用同一个请求号，不会重复计次。</p>
          </div>
          <button type="button" :disabled="verifying" @click="retryPendingVerification">重试</button>
        </div>

        <div class="primary-action-block">
          <button class="primary-button scan-button" type="button" :disabled="verifying || verificationPhase === 'uncertain'" @click="toggleScanner">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M8 5H6a2 2 0 0 0-2 2v2M16 5h2a2 2 0 0 1 2 2v2M8 19H6a2 2 0 0 1-2-2v-2M16 19h2a2 2 0 0 0 2-2v-2" />
              <path d="M8 9h8v6H8z" />
            </svg>
            <span>{{ scanButtonLabel }}</span>
          </button>
          <p class="action-note">{{ scanning ? '扫描到二维码后会自动提交核验' : '相机无法使用时，可从下方选择备用方式' }}</p>
        </div>

        <div class="fallback-actions">
          <label class="secondary-action" :class="{ 'is-disabled': !canUseFallback }">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M4 7a2 2 0 0 1 2-2h2l1.2-2h5.6L16 5h2a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7Z" /><circle cx="12" cy="12" r="3.2" />
            </svg>
            <span>相册识别</span>
            <input type="file" accept="image/*" :disabled="!canUseFallback" @change="decodeImage" />
          </label>
          <button class="secondary-action" type="button" :disabled="!canUseFallback" @click="openManualEntry">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M4 5h16v14H4zM8 9h.01M12 9h.01M16 9h.01M8 13h.01M12 13h.01M16 13h.01M8 17h8" />
            </svg>
            <span>输入票码</span>
          </button>
        </div>

        <p v-if="cameraMessage" class="hint-text" role="status">{{ cameraMessage }}</p>
        <p v-if="errorMessage" class="error-text" role="alert">{{ errorMessage }}</p>

        <section v-if="recentScans.length" class="recent-panel" aria-labelledby="recent-title">
          <div class="recent-heading">
            <div>
              <span class="screen-kicker">本机记录</span>
              <h2 id="recent-title">最近核销</h2>
            </div>
            <span>仅保留本页</span>
          </div>
          <ul class="recent-list">
            <li v-for="item in recentScans" :key="item.id">
              <span class="recent-dot" :class="item.result === 'allow' ? 'is-success' : 'is-deny'" aria-hidden="true"></span>
              <span class="recent-info"><strong>{{ item.label }}</strong><small>{{ item.time }} · {{ item.pointName }}</small></span>
              <span class="recent-status" :class="item.result === 'allow' ? 'is-success' : 'is-deny'">{{ item.statusLabel }}</span>
            </li>
          </ul>
        </section>

        <footer class="session-footer">
          <span class="footer-lock" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="10" width="14" height="10" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3" /></svg>
          </span>
          <span>当前会话仅绑定此检票点和设备</span>
        </footer>
      </section>
    </div>

    <Transition name="sheet">
      <div v-if="manualEntryVisible" class="sheet-backdrop" @click.self="closeManualEntry">
        <section class="manual-sheet" role="dialog" aria-modal="true" aria-labelledby="manual-title">
          <div class="sheet-grabber" aria-hidden="true"></div>
          <div class="sheet-header">
            <div><span class="screen-kicker">备用方式</span><h2 id="manual-title">输入票码</h2></div>
            <button class="icon-button light" type="button" aria-label="关闭输入票码" @click="closeManualEntry">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg>
            </button>
          </div>
          <form class="manual-form" @submit.prevent="submitManual">
            <label class="field"><span>票码</span><input ref="manualInputRef" v-model.trim="manualCode" placeholder="输入票码" autocomplete="off" required /></label>
            <button class="primary-button" type="submit" :disabled="verifying || !manualCode.trim()">
              <span>{{ verifying ? '核验中…' : '核销' }}</span>
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12h13M13 6l6 6-6 6" /></svg>
            </button>
          </form>
        </section>
      </div>
    </Transition>
  </main>
</template>

<script setup lang="ts">
import axios from 'axios'
import { BrowserMultiFormatReader } from '@zxing/browser'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'

type VerificationPhase = 'idle' | 'scanning' | 'processing' | 'success' | 'denied' | 'uncertain' | 'session_expired'
type ConnectionState = 'checking' | 'online' | 'degraded' | 'offline'

interface PendingRequest {
  id: string
  ticketCode: string
}

interface RecentScan {
  id: string
  label: string
  result: 'allow' | 'deny'
  statusLabel: string
  pointName: string
  time: string
}

const api = axios.create({ baseURL: import.meta.env.VITE_API_URL || '/api/v1', timeout: 15000 })
const sessionToken = ref(sessionStorage.getItem('mobile_session') || '')
const apiSessionToken = () => sessionToken.value
api.interceptors.request.use((config) => {
  const token = sessionStorage.getItem('mobile_token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  const session = apiSessionToken()
  if (session) config.headers['X-Mobile-Session'] = session
  return config
})

const loginForm = reactive({ system_code: '', job_number: '', password: '' })
const isLoggedIn = ref(Boolean(sessionStorage.getItem('mobile_token')))
const tenantName = ref(sessionStorage.getItem('mobile_tenant_name') || '')
const busy = ref(false)
const verifying = ref(false)
const heartbeatBusy = ref(false)
const errorMessage = ref('')
const cameraMessage = ref('')
const sessionExpiresAt = ref(sessionStorage.getItem('mobile_session_expires_at') || '')
const lastHeartbeatAt = ref<number | null>(null)
const connectionState = ref<ConnectionState>(sessionToken.value ? 'checking' : 'offline')
const checkpoints = ref<any[]>([])
const devices = ref<any[]>([])
const selectedCheckpointID = ref(Number(sessionStorage.getItem('mobile_checkpoint_id') || 0))
const selectedDeviceID = ref(Number(sessionStorage.getItem('mobile_device_id') || 0))
const scanResult = ref<any>(null)
const readPendingRequest = (): PendingRequest | null => {
  try {
    const parsed = JSON.parse(sessionStorage.getItem('mobile_pending_verification') || 'null')
    if (parsed && typeof parsed.id === 'string' && parsed.id.trim() && typeof parsed.ticketCode === 'string' && parsed.ticketCode.trim()) {
      return { id: parsed.id.trim(), ticketCode: parsed.ticketCode.trim() }
    }
  } catch {
    // Ignore malformed session data and let the operator start a fresh scan.
  }
  return null
}
const pendingRequest = ref<PendingRequest | null>(readPendingRequest())
const verificationPhase = ref<VerificationPhase>(pendingRequest.value && sessionToken.value ? 'uncertain' : 'idle')
const sessionRestoring = ref(Boolean(sessionToken.value && isLoggedIn.value))
const recentScans = ref<RecentScan[]>([])
const manualEntryVisible = ref(false)
const manualCode = ref('')
const scanning = ref(false)
const videoRef = ref<HTMLVideoElement | null>(null)
const manualInputRef = ref<HTMLInputElement | null>(null)
const reader = new BrowserMultiFormatReader()
let scannerControls: { stop: () => void } | null = null
const scannerStarting = ref(false)
let heartbeatTimer: number | undefined
let lastScanCode = ''
let lastScanAt = 0
let scannerStartVersion = 0

const filteredDevices = computed(() => devices.value.filter((device) => Number(device.check_point_id || 0) === selectedCheckpointID.value))
const activeCheckpoint = computed(() => checkpoints.value.find((item) => Number(item.id) === selectedCheckpointID.value))
const activeDevice = computed(() => devices.value.find((item) => Number(item.id) === selectedDeviceID.value))
const resultIsSuccess = computed(() => scanResult.value?.result === 'allow')
const resultTitle = computed(() => {
  if (resultIsSuccess.value) return '核销成功'
  const titles: Record<string, string> = {
    refunded: '订单已退款',
    already_used: '票券已使用',
    expired: '票券已过期',
    not_started: '票券未生效',
    order_not_paid: '订单未支付',
    wrong_checkpoint: '不适用当前点位',
  }
  return titles[String(scanResult.value?.reason_code || '')] || '核销未通过'
})
const resultDetail = computed(() => String(scanResult.value?.display_text || '请查看票券状态'))
const resultTimeText = computed(() => scanResult.value?.checked_at ? formatClock(scanResult.value.checked_at) : formatClock(Date.now()))
const scanButtonLabel = computed(() => scannerStarting.value ? '取消启动' : scanning.value ? '停止扫码' : scanResult.value ? '继续扫码' : '打开相机扫码')
const canUseFallback = computed(() => Boolean(sessionToken.value) && !verifying.value && !scanning.value && !scannerStarting.value && verificationPhase.value !== 'uncertain')
const connectionLabel = computed(() => ({ checking: '连接中', online: '连接正常', degraded: '网络不稳', offline: '已断开' })[connectionState.value])
const lastHeartbeatText = computed(() => lastHeartbeatAt.value ? `同步于 ${formatClock(lastHeartbeatAt.value)}` : '等待同步')
const sessionExpiryText = computed(() => sessionExpiresAt.value ? `会话至 ${formatClock(sessionExpiresAt.value)}` : '会话有效')

const setError = (message: string) => { errorMessage.value = message }
const clearError = () => { errorMessage.value = '' }

const formatClock = (value: string | number | Date) => {
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return '--:--'
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

const formatResultLabel = (response: any) => {
  const lines = String(response?.display_text || '').split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  return lines[resultIsAllow(response) && lines.length > 1 ? 1 : 0] || (resultIsAllow(response) ? '核验通过' : '未通过')
}

const resultIsAllow = (response: any) => response?.result === 'allow'

const responseNeedsRetry = (response: any) => response?.reason_code === 'processing' || Number(response?.code) === 409

const resultStatusLabel = (response: any) => {
  if (resultIsAllow(response)) return '通过'
  const labels: Record<string, string> = {
    refunded: '已退款',
    already_used: '已使用',
    expired: '已过期',
    not_started: '未生效',
    invalid_ticket: '无效票',
    order_not_paid: '未支付',
    wrong_checkpoint: '点位不符',
    processing: '待确认',
  }
  return labels[String(response?.reason_code || '')] || '未通过'
}

const addRecentScan = (response: any, ticketCode: string) => {
  recentScans.value = [{
    id: `${Date.now()}-${ticketCode}`,
    label: formatResultLabel(response),
    result: resultIsAllow(response) ? ('allow' as const) : ('deny' as const),
    statusLabel: resultStatusLabel(response),
    pointName: String(activeCheckpoint.value?.name || '当前点位'),
    time: formatClock(Date.now()),
  }, ...recentScans.value].slice(0, 5)
}

const persistPendingRequest = () => {
  if (!pendingRequest.value) {
    sessionStorage.removeItem('mobile_pending_verification')
    return
  }
  sessionStorage.setItem('mobile_pending_verification', JSON.stringify(pendingRequest.value))
}

const clearPendingRequest = () => {
  pendingRequest.value = null
  sessionStorage.removeItem('mobile_pending_verification')
}

const vibrateForResult = (allowed: boolean) => {
  if (typeof navigator.vibrate !== 'function') return
  navigator.vibrate(allowed ? [24, 36, 24] : 80)
}

const login = async () => {
  busy.value = true
  clearError()
  try {
    const response = await api.post('/auth/staff/login', loginForm)
    sessionStorage.setItem('mobile_token', response.data.token)
    sessionStorage.setItem('mobile_staff', JSON.stringify(response.data.staff || {}))
    isLoggedIn.value = true
    loginForm.password = ''
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
    if (error.response?.status === 401) await logout()
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
    connectionState.value = 'online'
    lastHeartbeatAt.value = Date.now()
    sessionStorage.setItem('mobile_session', sessionToken.value)
    sessionStorage.setItem('mobile_session_expires_at', sessionExpiresAt.value)
    sessionStorage.setItem('mobile_checkpoint_id', String(selectedCheckpointID.value))
    sessionStorage.setItem('mobile_device_id', String(selectedDeviceID.value))
    clearPendingRequest()
    verificationPhase.value = 'idle'
    startHeartbeat(false)
  } catch (error: any) {
    setError(error.response?.data?.error || '无法建立移动核销会话')
  } finally {
    busy.value = false
  }
}

const sendHeartbeat = async () => {
  if (!sessionToken.value || heartbeatBusy.value) return
  heartbeatBusy.value = true
  try {
    await api.post('/mobile/session/heartbeat')
    connectionState.value = 'online'
    lastHeartbeatAt.value = Date.now()
  } catch (error: any) {
    if (error.response?.status === 401) {
      await closeSession(false, true)
      verificationPhase.value = 'session_expired'
      setError('会话已过期，请重新选择点位')
    } else {
      connectionState.value = 'degraded'
      cameraMessage.value = '网络连接不稳定，核销结果未受影响；请确认网络后再继续。'
    }
  } finally {
    heartbeatBusy.value = false
  }
}

const startHeartbeat = (checkNow = false) => {
  if (heartbeatTimer) window.clearInterval(heartbeatTimer)
  if (checkNow) void sendHeartbeat()
  heartbeatTimer = window.setInterval(() => { void sendHeartbeat() }, 60000)
}

const closeSession = async (revoke = true, force = false) => {
  if ((verifying.value || verificationPhase.value === 'uncertain') && !force) return
  stopScanner()
  closeManualEntry()
  if (revoke && sessionToken.value) {
    try { await api.post('/mobile/session/close') } catch { /* session may already have expired */ }
  }
  sessionToken.value = ''
  sessionExpiresAt.value = ''
  scanResult.value = null
  clearPendingRequest()
  recentScans.value = []
  verificationPhase.value = 'idle'
  connectionState.value = 'offline'
  lastHeartbeatAt.value = null
  if (heartbeatTimer) window.clearInterval(heartbeatTimer)
  heartbeatTimer = undefined
  sessionStorage.removeItem('mobile_session')
  sessionStorage.removeItem('mobile_session_expires_at')
}

const logout = async () => {
  if (busy.value || sessionRestoring.value || verifying.value) return
  // The header is disabled while a request is uncertain; this force flag is
  // for explicit logout and automatic auth-expiry cleanup so stale session
  // storage cannot survive into the next operator login.
  await closeSession(true, true)
  sessionStorage.removeItem('mobile_token')
  sessionStorage.removeItem('mobile_staff')
  sessionStorage.removeItem('mobile_tenant_name')
  isLoggedIn.value = false
  tenantName.value = ''
  loginForm.password = ''
}

const toggleScanner = async () => {
  if (scannerStarting.value) { stopScanner(); return }
  if (scanning.value) { stopScanner(); return }
  if (verificationPhase.value === 'uncertain') {
    setError('上一笔核验结果尚未确认，请先重试。')
    return
  }
  clearError()
  cameraMessage.value = '正在启动相机…'
  if (!navigator.mediaDevices?.getUserMedia) {
    cameraMessage.value = '当前浏览器不支持相机，请使用 HTTPS 的系统浏览器，或改用相册识别/输入票码。'
    return
  }
  const startVersion = ++scannerStartVersion
  try {
    scannerStarting.value = true
    verificationPhase.value = 'scanning'
    const controls = await reader.decodeFromVideoDevice(undefined, videoRef.value || undefined, (result, error) => {
      if (result && !verifying.value) {
        const value = result.getText().trim()
        const now = Date.now()
        if (value === lastScanCode && now - lastScanAt < 1500) return
        lastScanCode = value
        lastScanAt = now
        stopScanner()
        void submitCode(value)
      } else if (error && !String(error).includes('NotFoundException')) {
        cameraMessage.value = '相机已开启，请将二维码放入取景框。'
      }
    })
    if (!scannerStarting.value || startVersion !== scannerStartVersion) {
      controls.stop()
      return
    }
    scannerControls = controls
    scanning.value = true
    scannerStarting.value = false
    cameraMessage.value = '相机已开启，请将二维码放入取景框。'
  } catch (error: any) {
    scannerStarting.value = false
    scanning.value = false
    verificationPhase.value = scanResult.value ? (resultIsSuccess.value ? 'success' : 'denied') : 'idle'
    cameraMessage.value = error?.name === 'NotAllowedError' ? '相机权限被拒绝，请在浏览器设置中允许相机后重试。' : '相机启动失败，请检查 HTTPS、浏览器权限或改用相册识别。'
  }
}

const stopScanner = () => {
  scannerStartVersion++
  scannerControls?.stop()
  scannerControls = null
  scannerStarting.value = false
  scanning.value = false
  if (verificationPhase.value === 'scanning') verificationPhase.value = scanResult.value ? (resultIsSuccess.value ? 'success' : 'denied') : 'idle'
}

const decodeImage = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !canUseFallback.value) return
  const url = URL.createObjectURL(file)
  cameraMessage.value = '正在识别二维码…'
  clearError()
  try {
    const result = await reader.decodeFromImageUrl(url)
    await submitCode(result.getText())
  } catch {
    cameraMessage.value = '没有识别出二维码，请换一张清晰图片或输入票码。'
  } finally {
    URL.revokeObjectURL(url)
  }
}

const normalizeTicketCode = (value: string) => {
  const text = value.trim()
  try {
    const parsed = new URL(text)
    return parsed.searchParams.get('ticket_code') || parsed.searchParams.get('code') || text
  } catch { return text }
}

const createRequestID = () => typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `mobile-${Date.now()}-${Math.random().toString(36).slice(2)}`

const submitManual = async () => {
  const code = manualCode.value
  closeManualEntry()
  await submitCode(code)
}

const retryPendingVerification = async () => {
  if (!pendingRequest.value) return
  await submitCode(pendingRequest.value.ticketCode)
}

const submitCode = async (value: string) => {
  const ticketCode = normalizeTicketCode(value)
  if (!ticketCode || verifying.value || !sessionToken.value) return
  if (verificationPhase.value === 'uncertain' && pendingRequest.value && pendingRequest.value.ticketCode !== ticketCode) {
    setError('上一笔核验结果尚未确认，请先重试或等待结果返回。')
    return
  }
  clearError()
  cameraMessage.value = ''
  const request = verificationPhase.value === 'uncertain' && pendingRequest.value?.ticketCode === ticketCode
    ? pendingRequest.value
    : { id: createRequestID(), ticketCode }
  pendingRequest.value = request
  persistPendingRequest()
  verifying.value = true
  verificationPhase.value = 'processing'
  scanResult.value = null
  try {
    const response = await api.post('/mobile/session/verify', { ticket_code: ticketCode, request_id: request.id })
    if (responseNeedsRetry(response.data)) {
      verificationPhase.value = 'uncertain'
      connectionState.value = 'degraded'
      setError('核验请求仍在处理中，请重试上一笔核验。')
      cameraMessage.value = '结果未确认前不会允许提交下一张票。'
      return
    }
    scanResult.value = { ...response.data, ticket_code: ticketCode, request_id: request.id, checked_at: new Date().toISOString() }
    verificationPhase.value = resultIsAllow(response.data) ? 'success' : 'denied'
    clearPendingRequest()
    manualCode.value = ''
    addRecentScan(response.data, ticketCode)
    vibrateForResult(resultIsAllow(response.data))
  } catch (error: any) {
    if (error.response?.status === 401) {
      clearPendingRequest()
      await closeSession(false, true)
      verificationPhase.value = 'session_expired'
      setError('会话已过期，请重新选择点位')
    } else if (error.response?.status === 409 || !error.response || error.response.status >= 500) {
      verificationPhase.value = 'uncertain'
      connectionState.value = error.response ? 'degraded' : 'offline'
      setError('核验结果暂时未返回，请重试上一笔核验。')
      cameraMessage.value = '结果未确认前不会允许提交下一张票。'
    } else {
      clearPendingRequest()
      verificationPhase.value = 'denied'
      setError(error.response?.data?.error || '核验请求失败，请重试')
    }
  } finally {
    verifying.value = false
  }
}

const openManualEntry = async () => {
  if (!canUseFallback.value) return
  clearError()
  manualEntryVisible.value = true
  await nextTick()
  manualInputRef.value?.focus()
}

const closeManualEntry = () => { manualEntryVisible.value = false; manualCode.value = '' }

const restoreSession = async () => {
  if (!isLoggedIn.value || !sessionToken.value) {
    sessionRestoring.value = false
    return
  }
  sessionRestoring.value = true
  try {
    await loadTargets()
    if (sessionToken.value) {
      await sendHeartbeat()
      if (sessionToken.value) startHeartbeat(false)
    }
  } finally {
    sessionRestoring.value = false
  }
}

const handleVisibilityChange = () => {
  if (!document.hidden && sessionToken.value) void sendHeartbeat()
}

const handleOnline = () => {
  if (!sessionToken.value) return
  connectionState.value = 'checking'
  void sendHeartbeat()
}

const handleOffline = () => {
  if (sessionToken.value) connectionState.value = 'offline'
}

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && manualEntryVisible.value) closeManualEntry()
}

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
  document.addEventListener('visibilitychange', handleVisibilityChange)
  window.addEventListener('online', handleOnline)
  window.addEventListener('offline', handleOffline)
  if (isLoggedIn.value) {
    void restoreSession()
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', handleKeydown)
  document.removeEventListener('visibilitychange', handleVisibilityChange)
  window.removeEventListener('online', handleOnline)
  window.removeEventListener('offline', handleOffline)
  stopScanner()
  if (heartbeatTimer) window.clearInterval(heartbeatTimer)
})
</script>

<style scoped>
.mobile-verify-page {
  --mobile-ink: #17212b;
  --mobile-muted: #70808c;
  --mobile-border: #dce4e8;
  --mobile-surface: #ffffff;
  --mobile-primary: #155e75;
  --mobile-primary-dark: #104b5c;
  --mobile-success: #117b58;
  --mobile-success-soft: #e8f6ef;
  --mobile-danger: #bd3e48;
  --mobile-danger-soft: #fff0ef;
  --mobile-amber: #9d6814;
  min-height: 100dvh;
  height: 100dvh;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  padding: 0 16px max(28px, env(safe-area-inset-bottom));
  box-sizing: border-box;
  color: var(--mobile-ink);
  background: #edf1f4;
  font-family: "Microsoft YaHei UI", "PingFang SC", "Noto Sans CJK SC", Arial, sans-serif;
}

.mobile-shell { width: min(100%, 520px); min-height: 100%; margin: 0 auto; }
.mobile-app-header { display: flex; align-items: center; justify-content: space-between; min-height: 76px; padding-top: max(12px, env(safe-area-inset-top)); }
.brand-block, .header-actions, .workspace-toolbar, .location-button, .session-strip, .fallback-actions, .secondary-action, .session-footer, .quiet-action, .processing-strip { display: flex; align-items: center; }
.brand-block { min-width: 0; gap: 11px; }
.brand-mark { display: grid; place-items: center; flex: 0 0 40px; width: 40px; height: 40px; color: #f6fbfc; background: var(--mobile-primary); border-radius: 13px; box-shadow: 0 8px 16px rgba(21, 94, 117, .18); }
.brand-mark svg { width: 23px; height: 23px; }
.brand-copy { min-width: 0; display: grid; gap: 1px; }
.brand-copy span, .screen-kicker { color: var(--mobile-muted); font-size: 11px; font-weight: 650; letter-spacing: .04em; }
.brand-copy strong { overflow: hidden; font-size: 16px; font-weight: 750; text-overflow: ellipsis; white-space: nowrap; }
.header-actions { gap: 9px; }
.connection-status, .camera-badge { display: inline-flex; align-items: center; gap: 6px; color: var(--mobile-muted); font-size: 11px; font-weight: 650; white-space: nowrap; }
.connection-status i, .session-state i, .camera-badge i { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.connection-status.is-online, .session-state.is-online { color: var(--mobile-success); }
.connection-status.is-degraded { color: var(--mobile-amber); }
.connection-status.is-offline { color: var(--mobile-danger); }
.connection-status.is-checking { color: #7c8e98; }
.session-state.is-degraded { color: var(--mobile-amber); }
.session-state.is-offline { color: var(--mobile-danger); }
.session-state.is-checking { color: #7c8e98; }
.icon-button { display: inline-grid; place-items: center; width: 38px; height: 38px; padding: 0; color: var(--mobile-ink); background: transparent; border: 0; border-radius: 12px; cursor: pointer; }
.icon-button svg { width: 20px; height: 20px; }
.icon-button.light { color: #5e707b; background: #f2f5f6; }
.icon-button:hover:not(:disabled), .quiet-action:hover:not(:disabled) { color: var(--mobile-primary); }
button { font: inherit; -webkit-tap-highlight-color: transparent; }
button:disabled { cursor: not-allowed; opacity: .48; }

.auth-view, .setup-view { padding-top: clamp(28px, 10vh, 88px); }
.restore-view { display: grid; min-height: calc(100dvh - 110px); place-items: center; padding: 28px 0; }
.restore-surface { display: grid; justify-items: center; gap: 9px; width: min(100%, 360px); padding: 28px 22px; color: var(--mobile-ink); background: #fff; border: 1px solid rgba(214, 225, 229, .9); border-radius: 22px; box-shadow: 0 16px 35px rgba(35, 58, 68, .07); text-align: center; }
.restore-surface strong { font-size: 16px; font-weight: 760; }
.restore-surface p { margin: 0; color: var(--mobile-muted); font-size: 12px; line-height: 1.55; }
.restore-spinner { width: 26px; height: 26px; border: 3px solid #dcebed; border-top-color: var(--mobile-primary); border-radius: 50%; animation: spin .8s linear infinite; }
.screen-intro { margin-bottom: 24px; }
.screen-intro h1 { margin: 4px 0 8px; color: var(--mobile-ink); font-size: 30px; line-height: 1.18; font-weight: 780; letter-spacing: 0; }
.screen-intro p { margin: 0; color: var(--mobile-muted); font-size: 14px; line-height: 1.65; }
.surface { background: var(--mobile-surface); border: 1px solid rgba(214, 225, 229, .9); border-radius: 22px; box-shadow: 0 16px 35px rgba(35, 58, 68, .07); }
.auth-surface, .setup-surface { display: grid; gap: 16px; padding: 20px; }
.field { display: grid; gap: 8px; min-width: 0; color: #40515c; font-size: 12px; font-weight: 700; }
.field input, .field select { width: 100%; min-height: 50px; box-sizing: border-box; color: var(--mobile-ink); background: #f9fbfb; border: 1px solid var(--mobile-border); border-radius: 13px; outline: none; padding: 0 14px; font-size: 15px; }
.field input::placeholder { color: #9aa8af; }
.field input:focus, .field select:focus { border-color: #5c9bad; box-shadow: 0 0 0 3px rgba(92, 155, 173, .15); }
.select-wrap { position: relative; display: block; }
.field select { appearance: none; padding-right: 42px; }
.select-wrap > svg { position: absolute; top: 50%; right: 14px; width: 18px; height: 18px; color: #71838d; pointer-events: none; transform: translateY(-50%); }
.primary-button { display: inline-flex; align-items: center; justify-content: center; gap: 9px; width: 100%; min-height: 52px; padding: 0 18px; color: #f7fbfc; background: var(--mobile-primary); border: 0; border-radius: 14px; box-shadow: 0 10px 20px rgba(21, 94, 117, .18); cursor: pointer; font-size: 15px; font-weight: 750; transition: background-color 160ms ease, transform 160ms ease, box-shadow 160ms ease; }
.primary-button svg { width: 19px; height: 19px; }
.primary-button:hover:not(:disabled) { background: var(--mobile-primary-dark); box-shadow: 0 12px 22px rgba(21, 94, 117, .23); }
.primary-button:active:not(:disabled) { transform: translateY(1px); }
.error-text { margin: 0; color: var(--mobile-danger); font-size: 13px; line-height: 1.55; }
.target-note { display: flex; align-items: flex-start; gap: 8px; color: #5e7883; font-size: 12px; line-height: 1.5; }
.target-note.is-empty { color: var(--mobile-amber); }
.target-note svg { flex: 0 0 16px; width: 16px; height: 16px; margin-top: 1px; }
.quiet-action { justify-content: center; gap: 7px; width: fit-content; margin: 16px auto 0; padding: 8px 12px; color: var(--mobile-muted); background: transparent; border: 0; cursor: pointer; font-size: 12px; font-weight: 700; }
.quiet-action svg { width: 16px; height: 16px; }

.verify-view { padding-bottom: 8px; }
.workspace-toolbar { gap: 8px; min-height: 65px; }
.location-button { min-width: 0; flex: 1; gap: 9px; padding: 5px 4px; color: var(--mobile-ink); background: transparent; border: 0; text-align: left; cursor: pointer; }
.toolbar-icon { display: grid; place-items: center; flex: 0 0 34px; width: 34px; height: 34px; color: var(--mobile-primary); background: #dfeef1; border-radius: 11px; }
.toolbar-icon svg { width: 18px; height: 18px; }
.location-copy { min-width: 0; display: grid; gap: 2px; }
.location-copy small, .device-copy small { overflow: hidden; color: var(--mobile-muted); font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.location-copy strong { overflow: hidden; font-size: 15px; font-weight: 750; text-overflow: ellipsis; white-space: nowrap; }
.chevron-icon { flex: 0 0 16px; width: 16px; height: 16px; color: #82929b; }
.device-copy { display: grid; flex: 0 1 130px; gap: 2px; min-width: 0; text-align: right; }
.device-copy span { overflow: hidden; color: #556873; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.session-strip { gap: 11px; min-height: 31px; padding: 0 2px 10px; color: #8a989f; font-size: 10px; }
.session-strip > span:not(:first-child) { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.session-strip > span:last-child { margin-left: auto; }
.session-state { display: inline-flex; flex: 0 0 auto; align-items: center; gap: 5px; font-weight: 700; }
.scan-stage { position: relative; overflow: hidden; aspect-ratio: 1 / 1.08; min-height: 270px; max-height: 58vh; border-radius: 24px; background: #18262d; box-shadow: 0 17px 32px rgba(19, 33, 39, .15); }
.scan-stage video { display: block; width: 100%; height: 100%; object-fit: cover; }
.scan-stage::after { position: absolute; inset: 0; content: ''; pointer-events: none; background: linear-gradient(180deg, rgba(9, 19, 24, .14), transparent 28%, transparent 72%, rgba(9, 19, 24, .25)); }
.camera-empty { position: absolute; inset: 0; z-index: 1; display: grid; place-content: center; justify-items: center; gap: 9px; padding: 28px; color: #e9f1f2; text-align: center; }
.camera-empty strong { font-size: 17px; font-weight: 750; }
.camera-empty small { max-width: 230px; color: #b8c8cc; font-size: 12px; line-height: 1.5; }
.camera-mark { display: grid; place-items: center; width: 66px; height: 66px; margin-bottom: 3px; color: #9ac9ce; background: rgba(224, 245, 245, .09); border: 1px solid rgba(182, 224, 226, .26); border-radius: 20px; }
.camera-mark svg { width: 42px; height: 42px; }
.scan-guide { position: absolute; inset: 16% 12%; z-index: 2; border: 1.5px solid rgba(232, 250, 249, .9); border-radius: 22px; box-shadow: 0 0 0 999px rgba(7, 16, 20, .31); }
.scan-guide::before, .scan-guide::after { position: absolute; inset: -2px; content: ''; border-radius: inherit; border: 3px solid transparent; border-top-color: #8ad4d1; border-left-color: #8ad4d1; }
.scan-guide::after { transform: rotate(180deg); }
.scan-guide span { position: absolute; right: 8%; left: 8%; top: 50%; height: 2px; background: #a6e8d8; box-shadow: 0 0 13px #a6e8d8; animation: scan-line 2.1s ease-in-out infinite; }
.camera-badge { position: absolute; top: 14px; left: 15px; z-index: 3; padding: 7px 9px; color: #cbdadc; background: rgba(17, 31, 37, .62); border: 1px solid rgba(204, 227, 228, .16); border-radius: 99px; backdrop-filter: blur(8px); }
.camera-badge.is-live { color: #c3f4df; }
.camera-badge.is-live i { box-shadow: 0 0 0 3px rgba(174, 241, 215, .15); }
.processing-strip { justify-content: center; gap: 8px; min-height: 38px; margin-top: 10px; color: #52707a; background: #e5f0f2; border-radius: 12px; font-size: 12px; font-weight: 650; }
.spinner { width: 14px; height: 14px; border: 2px solid rgba(21, 94, 117, .2); border-top-color: var(--mobile-primary); border-radius: 50%; animation: spin .8s linear infinite; }
.result-card { position: relative; display: flex; align-items: flex-start; gap: 12px; min-height: 92px; margin-top: 10px; padding: 15px 44px 15px 14px; border-radius: 17px; }
.result-card.is-success { color: var(--mobile-success); background: var(--mobile-success-soft); }
.result-card.is-deny { color: var(--mobile-danger); background: var(--mobile-danger-soft); }
.result-icon { display: grid; place-items: center; flex: 0 0 34px; width: 34px; height: 34px; color: #fff; background: currentColor; border-radius: 50%; }
.result-icon svg { width: 19px; height: 19px; }
.result-copy { min-width: 0; }
.result-kicker { display: block; font-size: 11px; font-weight: 700; opacity: .76; }
.result-copy h2 { margin: 2px 0 5px; color: inherit; font-size: 19px; line-height: 1.2; font-weight: 780; }
.result-copy p { margin: 0; color: inherit; font-size: 13px; line-height: 1.5; opacity: .86; white-space: pre-line; }
.result-time { position: absolute; top: 16px; right: 14px; color: inherit; font-size: 10px; opacity: .68; }
.uncertain-card { display: flex; align-items: flex-start; gap: 10px; margin-top: 10px; padding: 13px; color: #855a1a; background: #fff6df; border: 1px solid #f2dfb3; border-radius: 16px; }
.uncertain-icon { display: grid; place-items: center; flex: 0 0 26px; width: 26px; height: 26px; color: #fff; background: #bb7b1c; border-radius: 50%; font-weight: 800; }
.uncertain-card div { min-width: 0; flex: 1; }
.uncertain-card strong { display: block; font-size: 13px; }
.uncertain-card p { margin: 3px 0 0; font-size: 11px; line-height: 1.5; }
.uncertain-card button { flex: 0 0 auto; padding: 5px 7px; color: #855a1a; background: transparent; border: 0; cursor: pointer; font-size: 12px; font-weight: 800; }
.primary-action-block { margin-top: 13px; }
.scan-button { min-height: 55px; }
.action-note { margin: 8px 0 0; color: #82919a; font-size: 11px; line-height: 1.5; text-align: center; }
.fallback-actions { gap: 10px; margin-top: 10px; }
.secondary-action { justify-content: center; gap: 8px; flex: 1; min-height: 47px; color: #49606b; background: #fff; border: 1px solid var(--mobile-border); border-radius: 13px; cursor: pointer; font-size: 13px; font-weight: 700; }
.secondary-action:hover:not(.is-disabled), .secondary-action:focus-within { color: var(--mobile-primary); border-color: #a2c5cc; }
.secondary-action svg { width: 18px; height: 18px; }
.secondary-action input { display: none; }
.secondary-action.is-disabled { cursor: not-allowed; opacity: .45; }
.hint-text { margin: 10px 2px 0; color: #71828c; font-size: 12px; line-height: 1.55; }
.recent-panel { margin-top: 28px; padding-top: 18px; border-top: 1px solid #dbe3e6; }
.recent-heading { display: flex; align-items: flex-end; justify-content: space-between; }
.recent-heading h2 { margin: 3px 0 0; color: var(--mobile-ink); font-size: 17px; font-weight: 780; }
.recent-heading > span { color: #94a0a5; font-size: 10px; }
.recent-list { display: grid; gap: 0; margin: 10px 0 0; padding: 0; list-style: none; }
.recent-list li { display: flex; align-items: center; gap: 10px; min-height: 52px; border-bottom: 1px solid #e3e9eb; }
.recent-list li:last-child { border-bottom: 0; }
.recent-dot { flex: 0 0 8px; width: 8px; height: 8px; border-radius: 50%; }
.recent-dot.is-success { background: var(--mobile-success); }
.recent-dot.is-deny { background: var(--mobile-danger); }
.recent-info { min-width: 0; flex: 1; display: grid; gap: 3px; }
.recent-info strong { overflow: hidden; color: #354953; font-size: 12px; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.recent-info small { color: #96a3a9; font-size: 10px; }
.recent-status { font-size: 11px; font-weight: 750; }
.recent-status.is-success { color: var(--mobile-success); }
.recent-status.is-deny { color: var(--mobile-danger); }
.session-footer { justify-content: center; gap: 5px; margin-top: 24px; color: #9aa8ad; font-size: 10px; }
.footer-lock svg { display: block; width: 13px; height: 13px; }

.sheet-backdrop { position: fixed; inset: 0; z-index: 20; display: flex; align-items: flex-end; justify-content: center; padding: 16px; background: rgba(20, 33, 41, .42); }
.manual-sheet { width: min(100%, 520px); padding: 9px 18px max(18px, env(safe-area-inset-bottom)); background: #fff; border-radius: 22px 22px 16px 16px; box-shadow: 0 -18px 45px rgba(25, 40, 47, .18); }
.sheet-grabber { width: 38px; height: 4px; margin: 0 auto 16px; background: #d5dfe2; border-radius: 99px; }
.sheet-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 18px; }
.sheet-header h2 { margin: 3px 0 0; font-size: 20px; }
.manual-form { display: grid; gap: 16px; }
.sheet-enter-active, .sheet-leave-active { transition: opacity 180ms ease; }
.sheet-enter-active .manual-sheet, .sheet-leave-active .manual-sheet { transition: transform 180ms ease; }
.sheet-enter-from, .sheet-leave-to { opacity: 0; }
.sheet-enter-from .manual-sheet, .sheet-leave-to .manual-sheet { transform: translateY(100%); }

@keyframes scan-line { 0%, 100% { transform: translateY(-46px); opacity: .35; } 50% { transform: translateY(46px); opacity: 1; } }
@keyframes spin { to { transform: rotate(360deg); } }

@media (min-width: 620px) {
  .mobile-verify-page { padding-right: 24px; padding-left: 24px; }
  .mobile-app-header { min-height: 86px; }
  .scan-stage { aspect-ratio: 1 / .86; }
}

@media (max-width: 370px) {
  .mobile-verify-page { padding-right: 12px; padding-left: 12px; }
  .mobile-app-header { min-height: 68px; }
  .device-copy { display: none; }
  .screen-intro h1 { font-size: 27px; }
  .auth-surface, .setup-surface { padding: 16px; }
}

@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { transition-duration: .01ms !important; animation-duration: .01ms !important; animation-iteration-count: 1 !important; }
}
</style>
