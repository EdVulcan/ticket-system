<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <h2 class="text-lg font-bold text-gray-900">设备管理</h2>
      <el-button type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> 新增设备
      </el-button>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading">
      <el-table-column prop="id" label="编号" width="80" />
      <el-table-column prop="name" label="设备名称" min-width="150" />
      <el-table-column prop="serial_number" label="序列号 (SN)" width="180">
        <template #default="{ row }">
          <el-tag type="info" effect="plain">{{ row.serial_number }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="type" label="类型" width="120">
        <template #default="{ row }">
          <el-tag :type="getDeviceTypeTag(row.type)">{{ getDeviceTypeName(row.type) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100">
        <template #default="{ row }">
          <el-badge is-dot :type="row.status === 'online' ? 'success' : 'danger'" class="mr-2" />
          <span>{{ row.status === 'online' ? '在线' : '离线' }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="check_point.name" label="所属检票点" min-width="150">
        <template #default="{ row }">
          <el-tag v-if="row.check_point" type="warning" effect="plain">{{ row.check_point.name }}</el-tag>
          <span v-else class="text-gray-400">未绑定</span>
        </template>
      </el-table-column>
      <el-table-column prop="ip_address" label="网络地址" width="140" />
      <el-table-column label="操作" width="330" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
          <el-button link type="warning" size="small" @click="handleRotateKey(row)">轮换密钥</el-button>
          <el-button v-if="row.type === 'gate' && canMaintenance" link type="success" size="small" @click="openMaintenance(row)">远程维护</el-button>
          <el-button link type="danger" size="small" @click="handleDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="mt-4 flex justify-end">
      <el-pagination
        v-model:current-page="currentPage"
        v-model:page-size="pageSize"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="fetchData"
      />
    </div>

    <!-- Dialog -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑设备' : '新增设备'"
      width="500px"
    >
      <el-form :model="form" label-width="100px" :rules="rules" ref="formRef">
        <el-form-item label="设备名称" prop="name">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="序列号" prop="serial_number">
          <el-input v-model="form.serial_number" placeholder="请输入唯一序列号" />
        </el-form-item>
        <el-form-item label="所属检票点" prop="check_point_id">
          <el-select v-model="form.check_point_id" placeholder="请选择检票点" class="w-full" clearable>
            <el-option
              v-for="item in checkPoints"
              :key="item.id"
              :label="item.name"
              :value="item.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="设备类型" prop="type">
          <el-select v-model="form.type" placeholder="请选择类型" class="w-full">
            <el-option label="闸机" value="gate" />
            <el-option label="手持机" value="handheld" />
            <el-option label="桌面售票终端" value="pos" />
          </el-select>
        </el-form-item>
        <el-form-item label="网络地址" prop="ip_address">
          <el-input v-model="form.ip_address" />
        </el-form-item>
        <el-form-item label="MAC地址" prop="mac_address">
          <el-input v-model="form.mac_address" />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleSubmit">确定</el-button>
        </span>
      </template>
    </el-dialog>

    <el-dialog v-model="credentialVisible" :title="credentialIsMaintenance ? '闸机维护凭据' : '设备接入密钥'" width="520px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" show-icon class="mb-4">
        此密钥只显示一次。<template v-if="credentialIsMaintenance">请立即配置到 Linux gate-client 的 GATE_MAINTENANCE_SECRET；它与设备核销密钥不同。</template><template v-else>请立即配置到设备 {{ credentialDeviceName }}，系统不会保存明文。</template>
      </el-alert>
      <el-input v-model="credentialKey" readonly>
        <template #append>
          <el-button @click="copyCredential">复制</el-button>
        </template>
      </el-input>
      <template #footer>
        <el-button type="primary" @click="credentialVisible = false">我已保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="maintenanceVisible" title="闸机远程维护" width="760px" :close-on-click-modal="false">
      <el-alert type="info" :closable="false" show-icon class="mb-4">
        维护通道只连接闸机本机 <code>127.0.0.1:22</code>，不提供任意内网转发；会话默认 15 分钟，到期自动关闭。请先把一次性维护密钥配置到 Linux gate-client。
      </el-alert>
      <el-descriptions v-if="maintenanceDevice" :column="2" border class="mb-4">
        <el-descriptions-item label="设备">{{ maintenanceDevice.name }}</el-descriptions-item>
        <el-descriptions-item label="序列号">{{ maintenanceDevice.serial_number }}</el-descriptions-item>
        <el-descriptions-item label="在线状态">
          <el-tag :type="maintenanceDevice.status === 'online' ? 'success' : 'danger'">{{ maintenanceDevice.status === 'online' ? '在线' : '离线' }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="维护凭据">
          <el-tag :type="maintenanceCredential ? 'success' : 'warning'">{{ maintenanceCredential ? '已配置' : '未配置' }}</el-tag>
        </el-descriptions-item>
      </el-descriptions>

      <div class="flex gap-2 mb-4">
        <el-button type="warning" :loading="maintenanceLoading" @click="rotateMaintenanceCredential">{{ maintenanceCredential ? '轮换维护凭据' : '生成维护凭据' }}</el-button>
        <el-button @click="loadMaintenance">刷新状态</el-button>
      </div>

      <el-form label-width="100px" class="mb-5">
        <el-form-item label="会话原因" required>
          <el-input v-model="maintenanceForm.reason" maxlength="255" show-word-limit placeholder="例如：现场排查三辊闸控制板连接" />
        </el-form-item>
        <el-form-item label="有效时长">
          <el-input-number v-model="maintenanceForm.ttl_seconds" :min="60" :max="1800" :step="60" />
          <span class="text-xs text-gray-500 ml-3">最多 30 分钟</span>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="maintenanceLoading" :disabled="!maintenanceCredential || maintenanceDevice?.status !== 'online'" @click="createMaintenanceSession">创建 SSH 会话</el-button>
        </el-form-item>
      </el-form>

      <el-alert v-if="maintenanceSession" type="success" :closable="false" class="mb-4">
        <template #title>会话已创建，令牌只显示本次</template>
        <div class="text-xs mt-2 break-all">WebSocket：{{ maintenanceSession.websocket_url }}</div>
        <div class="text-xs mt-1 break-all">令牌：{{ maintenanceSession.session_token }}</div>
        <div class="text-xs mt-1">可在管理员电脑执行：<code>ssh -o ProxyCommand="gate-ssh --url '{{ maintenanceSession.websocket_url }}' --token '{{ maintenanceSession.session_token }}'" root@127.0.0.1</code></div>
        <div class="mt-2 flex gap-2">
          <el-button size="small" @click="copyMaintenanceText(maintenanceSession.websocket_url, '连接地址')">复制连接地址</el-button>
          <el-button size="small" @click="copyMaintenanceText(maintenanceSession.session_token, '会话令牌')">复制令牌</el-button>
        </div>
      </el-alert>

      <el-table :data="maintenanceSessions" size="small" max-height="220" v-loading="maintenanceLoading">
        <el-table-column prop="id" label="会话" width="75" />
        <el-table-column prop="status" label="状态" width="100" />
        <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
        <el-table-column prop="expires_at" label="到期时间" width="180" />
        <el-table-column label="操作" width="90">
          <template #default="{ row }">
            <el-button v-if="['pending', 'active'].includes(row.status)" link type="danger" size="small" @click="closeMaintenanceSession(row)">关闭</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { hasPermission } from '@/utils/permissions'
import { readStoredUser } from '@/utils/tenantAccess'

const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const dialogVisible = ref(false)
const isEdit = ref(false)
const formRef = ref()
const credentialVisible = ref(false)
const credentialKey = ref('')
const credentialDeviceName = ref('')
const credentialIsMaintenance = ref(false)
const canMaintenance = computed(() => hasPermission(readStoredUser(), 'onsite.maintenance'))
const maintenanceVisible = ref(false)
const maintenanceLoading = ref(false)
const maintenanceDevice = ref<any | null>(null)
const maintenanceCredential = ref<any | null>(null)
const maintenanceSessions = ref<any[]>([])
const maintenanceSession = ref<any | null>(null)
const maintenanceForm = reactive({ reason: '', ttl_seconds: 900 })

const form = reactive({
  id: 0,
  name: '',
  serial_number: '',
  check_point_id: undefined,
  type: 'gate',
  status: 'offline',
  ip_address: '',
  mac_address: ''
})

const checkPoints = ref<any[]>([])

const fetchCheckPoints = async () => {
    try {
        const res = await request.get('/checkpoints', { params: { page_size: 100 } })
        checkPoints.value = res.data.data
    } catch (error) {
        console.error('Fetch CheckPoints Error', error)
    }
}

const rules = {
  name: [{ required: true, message: '请输入设备名称', trigger: 'blur' }],
  serial_number: [{ required: true, message: '请输入序列号', trigger: 'blur' }],
  type: [{ required: true, message: '请选择类型', trigger: 'change' }]
}

const getDeviceTypeName = (type: string) => {
  const map: Record<string, string> = {
    gate: '闸机',
    handheld: '手持机',
    pos: '桌面售票终端'
  }
  return map[type] || '其他设备'
}

const getDeviceTypeTag = (type: string) => {
  const map: Record<string, string> = {
    gate: 'primary',
    handheld: 'warning',
    pos: 'success'
  }
  return map[type] || 'info'
}

const fetchData = async () => {
  loading.value = true
  try {
    const res = await request.get('/devices', {
      params: { page: currentPage.value, page_size: pageSize.value }
    })
    tableData.value = res.data.data
    total.value = res.data.total
  } catch (error) {
    ElMessage.error('获取数据失败')
  } finally {
    loading.value = false
  }
}

const handleAdd = () => {
  isEdit.value = false
  Object.assign(form, { id: 0, name: '', serial_number: '', check_point_id: undefined, type: 'gate', status: 'offline', ip_address: '', mac_address: '' })
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  Object.assign(form, {
    id: row.id, name: row.name || '', serial_number: row.serial_number || '',
    check_point_id: row.check_point_id || undefined, type: row.type || 'gate',
    status: row.status || 'offline', ip_address: row.ip_address || '', mac_address: row.mac_address || '',
  })
  dialogVisible.value = true
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('删除后会保留设备历史记录，序列号可以重新用于新设备，但员工授权不会自动转移。确认删除吗？', '删除设备', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    type: 'warning',
  }).then(async () => {
    try {
      await request.delete(`/devices/${row.id}`)
      ElMessage.success('删除成功')
      fetchData()
    } catch (error) {
      ElMessage.error('删除失败')
    }
  })
}

const showCredential = (deviceName: string, key: string, maintenance = false) => {
  credentialDeviceName.value = deviceName
  credentialKey.value = key
  credentialIsMaintenance.value = maintenance
  credentialVisible.value = true
}

const copyCredential = async () => {
  try {
    await navigator.clipboard.writeText(credentialKey.value)
    ElMessage.success('密钥已复制')
  } catch {
    ElMessage.error('复制失败，请手动选择密钥')
  }
}

const handleRotateKey = async (row: any) => {
  try {
    await ElMessageBox.confirm('旧密钥会立即失效，确认轮换吗？', '轮换设备密钥', {
      confirmButtonText: '确认轮换',
      cancelButtonText: '取消',
      type: 'warning'
    })
    const response = await request.post(`/devices/${row.id}/rotate-key`)
    showCredential(row.name, response.data.auth_key)
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error('密钥轮换失败')
  }
}

const openMaintenance = async (row: any) => {
  maintenanceDevice.value = row
  maintenanceCredential.value = null
  maintenanceSessions.value = []
  maintenanceSession.value = null
  maintenanceForm.reason = ''
  maintenanceForm.ttl_seconds = 900
  maintenanceVisible.value = true
  await loadMaintenance()
}

const loadMaintenance = async () => {
  if (!maintenanceDevice.value) return
  maintenanceLoading.value = true
  try {
    try {
      const credential = await request.get(`/devices/${maintenanceDevice.value.id}/maintenance-credential`, { skipErrorToast: true } as any)
      maintenanceCredential.value = credential.data
    } catch (error: any) {
      if (error.response?.status !== 404) throw error
      maintenanceCredential.value = null
    }
    const sessions = await request.get(`/devices/${maintenanceDevice.value.id}/maintenance-sessions`, { params: { page: 1, page_size: 20 } })
    maintenanceSessions.value = sessions.data.data || []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '获取维护状态失败')
  } finally {
    maintenanceLoading.value = false
  }
}

const rotateMaintenanceCredential = async () => {
  if (!maintenanceDevice.value) return
  try {
    const reason = await ElMessageBox.prompt('请说明轮换维护凭据的原因', '维护凭据', {
      confirmButtonText: '生成', cancelButtonText: '取消', inputValidator: value => value.trim() ? true : '原因不能为空',
    })
    maintenanceLoading.value = true
    const response = await request.post(`/devices/${maintenanceDevice.value.id}/maintenance-credential`, { reason: reason.value })
    showCredential(maintenanceDevice.value.name + '（维护密钥）', response.data.secret, true)
    await loadMaintenance()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || '维护凭据生成失败')
  } finally {
    maintenanceLoading.value = false
  }
}

const createMaintenanceSession = async () => {
  if (!maintenanceDevice.value || !maintenanceForm.reason.trim()) {
    ElMessage.warning('请先填写会话原因')
    return
  }
  maintenanceLoading.value = true
  try {
    const response = await request.post(`/devices/${maintenanceDevice.value.id}/maintenance-sessions`, maintenanceForm)
    maintenanceSession.value = response.data
    await loadMaintenance()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || 'SSH 会话创建失败')
  } finally {
    maintenanceLoading.value = false
  }
}

const closeMaintenanceSession = async (row: any) => {
  try {
    const result = await ElMessageBox.prompt('请说明关闭维护会话的原因', '关闭会话', {
      confirmButtonText: '关闭', cancelButtonText: '取消', inputValidator: value => value.trim() ? true : '原因不能为空',
    })
    await request.post(`/devices/${maintenanceDevice.value.id}/maintenance-sessions/${row.id}/close`, { reason: result.value })
    ElMessage.success('维护会话已关闭')
    await loadMaintenance()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || '会话关闭失败')
  }
}

const copyMaintenanceText = async (value: string, label: string) => {
  try {
    await navigator.clipboard.writeText(value)
    ElMessage.success(`${label}已复制`)
  } catch {
    ElMessage.error(`复制${label}失败，请手动选择`)
  }
}

const handleSubmit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      try {
        const payload = {
          name: form.name.trim(), serial_number: form.serial_number.trim(), check_point_id: form.check_point_id,
          type: form.type, status: form.status, ip_address: form.ip_address.trim(), mac_address: form.mac_address.trim(),
        }
        if (isEdit.value) {
          await request.put(`/devices/${form.id}`, payload)
        } else {
          const response = await request.post('/devices', payload)
          showCredential(form.name, response.data.auth_key)
        }
        ElMessage.success(isEdit.value ? '更新成功' : '创建成功')
        dialogVisible.value = false
        fetchData()
      } catch (error) {
        ElMessage.error('操作失败')
      }
    }
  })
}

onMounted(() => {
  fetchData()
  fetchCheckPoints()
})
</script>
