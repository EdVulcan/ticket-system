<template>
  <div class="gate-simulator">
    <el-card class="simulator-card">
      <template #header>
        <div class="card-header">
          <span>虚拟闸机模拟器 (Virtual Gate Simulator)</span>
          <el-tag :type="deviceStatus === 'online' ? 'success' : 'info'">
            {{ deviceStatus === 'online' ? '在线 (Online)' : '离线 (Offline)' }}
          </el-tag>
        </div>
      </template>

      <el-form label-width="120px">
        <el-form-item label="系统编号 System">
          <el-input v-model="form.system_code" placeholder="例如: SYS001" />
        </el-form-item>
        <el-form-item label="设备序列号 S/N">
          <el-input v-model="form.serial_number" placeholder="例如: GT-8888" />
        </el-form-item>
        <el-form-item label="设备密钥 Key">
          <el-input v-model="form.device_key" type="password" show-password placeholder="设备创建或轮换时获得的密钥" />
        </el-form-item>
        
        <el-divider content-position="left">设备动作 (Actions)</el-divider>

        <el-form-item>
          <el-button type="primary" @click="sendHeartbeat" :loading="loading.heartbeat">
            发送心跳 (Send Heartbeat)
          </el-button>
          <div class="heartbeat-info" v-if="lastHeartbeat">
            上次心跳: {{ lastHeartbeat }}
          </div>
        </el-form-item>

        <el-divider content-position="left">验票操作 (Verification)</el-divider>

        <el-form-item label="票据号码 Ticket">
          <el-input v-model="form.ticket_code" placeholder="模拟扫码结果..." clearable />
        </el-form-item>
        
        <el-form-item>
          <el-button type="success" size="large" @click="verifyTicket" :loading="loading.verify">
            模拟刷票 (Simulate Scan)
          </el-button>
        </el-form-item>
      </el-form>

      <!-- Simulation Result Display -->
      <div v-if="result" class="simulation-screen" :class="result.result">
        <div class="screen-content">
          <el-icon v-if="result.result === 'allow'" :size="60"><CircleCheckFilled /></el-icon>
          <el-icon v-else :size="60"><CircleCloseFilled /></el-icon>
          
          <h2 class="display-text" style="white-space: pre-wrap;">{{ result.display_text }}</h2>
          
          <div class="gate-animation" v-if="result.result === 'allow'">
             <div class="gate-door left open"></div>
             <div class="gate-door right open"></div>
          </div>
          <div class="gate-animation" v-else>
             <div class="gate-door left closed"></div>
             <div class="gate-door right closed"></div>
          </div>

          <div class="audio-log" v-if="result.voice_file">
            <el-icon><Microphone /></el-icon> 播放语音: {{ result.voice_file }}
          </div>
        </div>
      </div>
    </el-card>

    <!-- Log Panel -->
    <el-card class="log-card">
      <template #header>设备日志 (Device Logs)</template>
      <div class="log-container">
        <div v-for="(log, index) in logs" :key="index" class="log-item">
          <span class="log-time">[{{ log.time }}]</span>
          <span :class="log.type">{{ log.message }}</span>
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { CircleCheckFilled, CircleCloseFilled, Microphone } from '@element-plus/icons-vue'
import axios from 'axios' // Directly use axios to bypass interceptors if needed, or use request instance

const form = reactive({
  system_code: 'SYS001',
  serial_number: 'GT-001',
  device_key: '',
  ticket_code: 'T202312240001'
})

const deviceStatus = ref('offline')
const lastHeartbeat = ref('')
const loading = reactive({
  heartbeat: false,
  verify: false
})

const result = ref<any>(null)
const logs = ref<any[]>([])

// Base URL handling - usually proxied or direct
const API_BASE = '/api/v1/hardware' 

const addLog = (message: string, type: 'info' | 'success' | 'error' = 'info') => {
  const now = new Date().toLocaleTimeString()
  logs.value.unshift({ time: now, message, type })
}

const sendHeartbeat = async () => {
  loading.heartbeat = true
  try {
    await axios.post(`${API_BASE}/heartbeat`, {
      system_code: form.system_code,
      serial_number: form.serial_number,
      device_key: form.device_key,
      ip: '127.0.0.1',
      status: 'ok'
    })
    deviceStatus.value = 'online'
    lastHeartbeat.value = new Date().toLocaleTimeString()
    addLog('心跳发送成功 (Heartbeat Success)', 'success')
  } catch (error: any) {
    deviceStatus.value = 'offline'
    addLog(`心跳失败: ${error.response?.data?.error || error.message}`, 'error')
    ElMessage.error('心跳失败: 设备未就绪或未注册')
  } finally {
    loading.heartbeat = false
  }
}

const verifyTicket = async () => {
  if (!form.ticket_code) return
  
  loading.verify = true
  result.value = null
  
  try {
    addLog(`正在验票: ${form.ticket_code}...`, 'info')
    const response = await axios.post(`${API_BASE}/verify`, {
      system_code: form.system_code,
      serial_number: form.serial_number,
      device_key: form.device_key,
      ticket_code: form.ticket_code,
      media_type: 'qr_code',
      scan_time: new Date().toISOString()
    })
    
    result.value = response.data
    
    if (response.data.result === 'allow') {
      addLog(`放行: ${response.data.display_text.replace('\n', ' ')}`, 'success')
      // Auto clear after 5s
      setTimeout(() => {
        if(result.value === response.data) result.value = null
      }, response.data.open_duration || 5000)
    } else {
      addLog(`拒绝: ${response.data.display_text.replace('\n', ' ')}`, 'error')
    }

  } catch (error: any) {
    addLog(`验票错误: ${error.response?.data?.error || error.message}`, 'error')
    ElMessage.error('验票请求失败')
  } finally {
    loading.verify = false
  }
}
</script>

<style scoped>
.gate-simulator {
  display: flex;
  gap: 20px;
  padding: 20px;
  max-width: 1200px;
  margin: 0 auto;
}

.simulator-card {
  flex: 1;
  position: relative;
}

.log-card {
  width: 350px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.heartbeat-info {
  margin-left: 10px;
  color: #909399;
  font-size: 12px;
}

.simulation-screen {
  margin-top: 20px;
  border-radius: 8px;
  padding: 20px;
  text-align: center;
  color: white;
  min-height: 200px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
}

.simulation-screen.allow {
  background: linear-gradient(135deg, #67c23a, #95d475);
}

.simulation-screen.deny {
  background: linear-gradient(135deg, #f56c6c, #fab6b6);
}

.display-text {
  margin: 15px 0;
  font-size: 24px;
  font-weight: bold;
  text-shadow: 0 2px 4px rgba(0,0,0,0.2);
}

.gate-animation {
  width: 120px;
  height: 60px;
  background: rgba(0,0,0,0.1);
  position: relative;
  margin: 10px auto;
  border-radius: 4px;
  display: flex;
  justify-content: space-between;
  overflow: hidden;
}

.gate-door {
  width: 45%;
  height: 100%;
  background: rgba(255,255,255,0.8);
  transition: transform 0.5s ease;
}

.gate-door.open.left { transform: translateX(-100%); }
.gate-door.open.right { transform: translateX(100%); }
.gate-door.closed { transform: translateX(0); }

.audio-log {
  margin-top: 10px;
  font-size: 12px;
  opacity: 0.8;
  display: flex;
  align-items: center;
  gap: 5px;
}

.log-container {
  height: 400px;
  overflow-y: auto;
  font-family: monospace;
  font-size: 12px;
}

.log-item {
  margin-bottom: 5px;
  border-bottom: 1px solid #f0f0f0;
  padding-bottom: 2px;
}

.log-time {
  color: #909399;
  margin-right: 8px;
}

.log-item .success { color: #67c23a; }
.log-item .error { color: #f56c6c; }
.log-item .info { color: #606266; }
</style>
