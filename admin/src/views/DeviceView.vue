<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <h2 class="text-lg font-bold text-gray-900">设备管理</h2>
      <div class="flex items-center gap-2">
        <el-button :icon="Refresh" circle title="刷新设备列表" :loading="loading" @click="refreshDevices" />
        <el-button v-if="canManage" type="primary" @click="handleAdd">
          <el-icon class="mr-2"><Plus /></el-icon> 新增设备
        </el-button>
      </div>
    </div>

    <el-tabs v-model="activeDeviceType" class="mb-4" @tab-change="handleDeviceTypeChange">
      <el-tab-pane v-for="tab in deviceTypeTabs" :key="tab.value" :label="tab.label" :name="tab.value" />
    </el-tabs>

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
      <el-table-column label="操作" width="430" fixed="right">
        <template #default="{ row }">
          <el-button v-if="canManage" link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
          <el-button v-if="canManage" link type="warning" size="small" @click="handleRotateKey(row)">轮换密钥</el-button>
          <el-button v-if="isGateDevice(row) && canManage" link type="primary" size="small" @click="openProvisioning(row)">生成安装绑定</el-button>
          <el-button v-if="isGateDevice(row) && canMaintenance" link type="success" size="small" @click="openMaintenance(row)">远程维护</el-button>
           <el-button v-if="canManage" link type="danger" size="small" @click="handleDelete(row)">删除</el-button>
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
          <el-input v-model="form.serial_number" :disabled="isEdit" placeholder="请输入唯一序列号" />
        </el-form-item>
        <el-form-item label="所属景区" prop="scenic_area_id">
          <el-select v-model="form.scenic_area_id" placeholder="请选择所属景区" class="w-full" @change="handleScenicAreaChange">
            <el-option
              v-for="area in scenicAreas"
              :key="area.id"
              :label="scenicAreaLabel(area)"
              :value="area.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="所属检票点" prop="check_point_id">
          <el-select v-model="form.check_point_id" placeholder="请选择检票点" class="w-full" clearable>
            <el-option
              v-for="item in availableCheckPoints"
              :key="item.id"
              :label="checkpointLabel(item)"
              :value="item.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="设备类型" prop="type">
          <el-select v-model="form.type" :disabled="isEdit" placeholder="请选择类型" class="w-full">
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
          <el-button v-if="canManage" type="primary" @click="handleSubmit">确定</el-button>
        </span>
      </template>
    </el-dialog>

    <el-dialog v-model="credentialVisible" :title="credentialIsMaintenance ? '闸机维护凭据' : '设备接入密钥'" width="520px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" show-icon class="mb-4">
        此密钥只显示一次。<template v-if="credentialIsMaintenance">请立即配置到 Linux gate-client 的 GATE_MAINTENANCE_SECRET；它与设备核销密钥不同。</template><template v-else><template v-if="isGateDevice(credentialDevice) && canManage">这是创建时生成的初始密钥；使用下一步安装绑定后会自动轮换，无需手工配置。</template><template v-else>请立即配置到设备 {{ credentialDeviceName }}，系统不会保存明文。</template></template>
      </el-alert>
      <el-input v-model="credentialKey" readonly>
        <template #append>
          <el-button @click="copyCredential">复制</el-button>
        </template>
      </el-input>
      <el-alert v-if="!credentialIsMaintenance && isGateDevice(credentialDevice) && canManage" type="info" :closable="false" class="mt-3">
        新闸机推荐使用一次性安装绑定，避免手工填写设备密钥。确认已了解下方密钥后，可直接进入下一步生成绑定码。
      </el-alert>
      <template #footer>
        <el-button v-if="!credentialIsMaintenance && isGateDevice(credentialDevice) && canManage" type="primary" @click="openProvisioningFromCredential">下一步：生成安装绑定</el-button>
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
        <div class="text-xs mt-1">可在管理员电脑执行：<code>ssh -o ProxyCommand="gate-ssh --url '{{ maintenanceSession.websocket_url }}' --token '{{ maintenanceSession.session_token }}'" vmadmin@127.0.0.1</code></div>
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

    <el-dialog v-model="provisioningVisible" title="生成闸机安装绑定" width="620px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" show-icon class="mb-4">
        <template #title>绑定码只显示一次，且不会展示设备密钥</template>
        <div class="text-xs mt-1">请先停止在线闸机上的旧客户端。安装器会从服务端绑定当前设备、租户和景区，不能手工改写这些归属。</div>
      </el-alert>
      <el-descriptions v-if="provisioningDevice" :column="2" border class="mb-4">
        <el-descriptions-item label="设备">{{ provisioningDevice.name }}</el-descriptions-item>
        <el-descriptions-item label="序列号">{{ provisioningDevice.serial_number }}</el-descriptions-item>
        <el-descriptions-item label="当前状态">
          <el-tag :type="provisioningDevice.status === 'online' ? 'danger' : 'info'">{{ provisioningDevice.status === 'online' ? '在线（需先停止旧客户端）' : '离线' }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="有效期">默认 10 分钟，最长 15 分钟</el-descriptions-item>
      </el-descriptions>
      <el-form label-width="100px" class="mb-2">
        <el-form-item label="安装原因" required>
          <el-input v-model="provisioningForm.reason" maxlength="255" show-word-limit placeholder="例如：现场部署新版本 gate-client" />
        </el-form-item>
        <el-form-item label="有效时长">
          <el-input-number v-model="provisioningForm.ttl_seconds" :min="60" :max="900" :step="60" />
          <span class="text-xs text-gray-500 ml-3">绑定码过期后不能恢复</span>
        </el-form-item>
      </el-form>
      <el-alert v-if="provisioningResult" type="success" :closable="false" class="mt-4">
        <template #title>绑定码已生成，请立即在闸机安装器中输入</template>
        <div class="text-xs mt-2">过期时间：{{ provisioningResult.expires_at }}</div>
        <el-input class="mt-2" :model-value="provisioningResult.activation_code" readonly>
          <template #append><el-button @click="copyProvisioningCode">复制绑定码</el-button></template>
        </el-input>
        <div class="text-xs text-gray-500 mt-2">绑定码不会写入 URL、命令行、二维码或日志；安装确认后服务端会清除临时加密配置。</div>
        <el-button class="mt-3" size="small" type="danger" plain @click="revokeProvisioningLease">立即撤销绑定码</el-button>
      </el-alert>
      <template #footer>
        <el-button @click="provisioningVisible = false">关闭</el-button>
        <el-button v-if="!provisioningResult" type="primary" :loading="provisioningLoading" :disabled="provisioningDevice?.status === 'online'" @click="createProvisioningLease">生成绑定码</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { hasPermission } from '@/utils/permissions'
import { readStoredUser } from '@/utils/tenantAccess'

const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const activeDeviceType = ref('all')
const deviceTypeTabs = [
  { label: '全部设备', value: 'all' },
  { label: '闸机', value: 'gate' },
  { label: '手持机', value: 'handheld' },
  { label: '桌面终端', value: 'pos' },
]
const dialogVisible = ref(false)
const isEdit = ref(false)
const formRef = ref()
const credentialVisible = ref(false)
const credentialKey = ref('')
const credentialDeviceName = ref('')
const credentialDevice = ref<any | null>(null)
const credentialIsMaintenance = ref(false)
const canMaintenance = computed(() => hasPermission(readStoredUser(), 'onsite.maintenance'))
const canManage = computed(() => hasPermission(readStoredUser(), 'onsite.manage'))
const maintenanceVisible = ref(false)
const maintenanceLoading = ref(false)
const maintenanceDevice = ref<any | null>(null)
const maintenanceCredential = ref<any | null>(null)
const maintenanceSessions = ref<any[]>([])
const maintenanceSession = ref<any | null>(null)
const maintenanceForm = reactive({ reason: '', ttl_seconds: 900 })
const provisioningVisible = ref(false)
const provisioningLoading = ref(false)
const provisioningDevice = ref<any | null>(null)
const provisioningResult = ref<any | null>(null)
const provisioningForm = reactive({ reason: '', ttl_seconds: 600 })

const form = reactive({
  id: 0,
  name: '',
  serial_number: '',
  scenic_area_id: undefined as number | undefined,
  check_point_id: undefined,
  type: 'gate',
  status: 'offline',
  ip_address: '',
  mac_address: ''
})

const scenicAreas = ref<any[]>([])
const checkPoints = ref<any[]>([])
const availableCheckPoints = computed(() => {
  if (!form.scenic_area_id) return checkPoints.value
  return checkPoints.value.filter(item => Number(item.scenic_area_id) === Number(form.scenic_area_id))
})
let fetchSequence = 0

const fetchCheckPoints = async () => {
  try {
    const rows: any[] = []
    let page = 1
    let total = 0
    do {
      const res = await request.get('/checkpoints', { params: { page, page_size: 100 } })
      rows.push(...(res.data.data || []))
      total = Number(res.data.total || rows.length)
      page += 1
    } while (rows.length < total && page <= 100)
    checkPoints.value = rows
  } catch (error) {
    console.error('Fetch CheckPoints Error', error)
    ElMessage.error((error as any)?.response?.data?.error || '获取检票点失败')
  }
}

const fetchScenicAreas = async () => {
  const response = await request.get('/scenic-areas')
  scenicAreas.value = response.data.data || []
}

const checkpointLabel = (item: any) => {
  const area = scenicAreas.value.find(value => Number(value.id) === Number(item.scenic_area_id))
  return area ? `${area.name} / ${item.name}` : item.name
}

const scenicAreaLabel = (area: any) => area.status === 'active' ? area.name : `${area.name}（${area.status}）`

const handleScenicAreaChange = () => {
  if (form.check_point_id && !availableCheckPoints.value.some(item => Number(item.id) === Number(form.check_point_id))) {
    form.check_point_id = undefined
  }
}

const rules = {
  name: [{ required: true, message: '请输入设备名称', trigger: 'blur' }],
  serial_number: [{ required: true, message: '请输入序列号', trigger: 'blur' }],
  scenic_area_id: [{ required: true, message: '请选择所属景区', trigger: 'change' }],
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

const isGateDevice = (device: any) => String(device?.type || '').trim().toLowerCase() === 'gate'

const fetchData = async () => {
  const sequence = ++fetchSequence
  loading.value = true
  try {
    const type = activeDeviceType.value === 'all' ? undefined : activeDeviceType.value
    const res = await request.get('/devices', {
      params: { page: currentPage.value, page_size: pageSize.value, type }
    })
    if (sequence !== fetchSequence) return
    tableData.value = res.data.data
    total.value = res.data.total
  } catch (error: any) {
    if (sequence === fetchSequence) ElMessage.error(error.response?.data?.error || '获取数据失败')
  } finally {
    if (sequence === fetchSequence) loading.value = false
  }
}

const handleDeviceTypeChange = () => {
  currentPage.value = 1
  fetchData()
}

const refreshDevices = () => {
  fetchData()
}

const handleAdd = () => {
  isEdit.value = false
  Object.assign(form, { id: 0, name: '', serial_number: '', scenic_area_id: scenicAreas.value.length === 1 ? scenicAreas.value[0].id : undefined, check_point_id: undefined, type: 'gate', status: 'offline', ip_address: '', mac_address: '' })
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  Object.assign(form, {
    id: row.id, name: row.name || '', serial_number: row.serial_number || '',
    scenic_area_id: row.scenic_area_id || undefined,
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

const showCredential = (deviceName: string, key: string, maintenance = false, device: any | null = null) => {
  credentialDeviceName.value = deviceName
  credentialDevice.value = device
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

const openProvisioningFromCredential = () => {
  const device = credentialDevice.value
  credentialVisible.value = false
  if (!device?.id) {
    ElMessage.error('设备信息未准备好，请在列表操作列中生成安装绑定')
    return
  }
  openProvisioning(device)
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

const openProvisioning = (row: any) => {
  provisioningDevice.value = row
  provisioningResult.value = null
  provisioningForm.reason = ''
  provisioningForm.ttl_seconds = 600
  provisioningVisible.value = true
}

const createProvisioningLease = async () => {
  if (!provisioningDevice.value || !provisioningForm.reason.trim()) {
    ElMessage.warning('请先填写安装原因')
    return
  }
  provisioningLoading.value = true
  try {
    const response = await request.post(`/devices/${provisioningDevice.value.id}/provisioning-leases`, provisioningForm)
    provisioningResult.value = response.data
    ElMessage.success('安装绑定码已生成')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '安装绑定码生成失败')
  } finally {
    provisioningLoading.value = false
  }
}

const copyProvisioningCode = async () => {
  if (!provisioningResult.value?.activation_code) return
  try {
    await navigator.clipboard.writeText(provisioningResult.value.activation_code)
    ElMessage.success('绑定码已复制')
  } catch {
    ElMessage.error('复制失败，请手动选择绑定码')
  }
}

const revokeProvisioningLease = async () => {
  if (!provisioningDevice.value || !provisioningResult.value?.lease_id) return
  try {
    const result = await ElMessageBox.prompt('请输入撤销原因', '撤销安装绑定', {
      confirmButtonText: '撤销', cancelButtonText: '取消', inputValidator: value => value.trim() ? true : '原因不能为空',
    })
    await request.post(`/devices/${provisioningDevice.value.id}/provisioning-leases/${provisioningResult.value.lease_id}/revoke`, { reason: result.value })
    provisioningResult.value = null
    ElMessage.success('安装绑定已撤销')
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || '撤销安装绑定失败')
  }
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
        const payload: Record<string, any> = {
          name: form.name.trim(), scenic_area_id: form.scenic_area_id, check_point_id: form.check_point_id,
          ip_address: form.ip_address.trim(), mac_address: form.mac_address.trim(),
        }
        if (!isEdit.value) {
          payload.serial_number = form.serial_number.trim()
          payload.type = form.type
        }
        if (isEdit.value) {
          await request.put(`/devices/${form.id}`, payload)
        } else {
          const response = await request.post('/devices', payload)
          showCredential(form.name, response.data.auth_key, false, response.data)
        }
        ElMessage.success(isEdit.value ? '更新成功' : '创建成功')
        dialogVisible.value = false
        fetchData()
      } catch (error: any) {
        ElMessage.error(error.response?.data?.error || '操作失败')
      }
    }
  })
}

onMounted(() => {
  fetchData()
  Promise.all([fetchScenicAreas(), fetchCheckPoints()]).catch(error => {
    ElMessage.error(error.response?.data?.error || '加载设备归属选项失败')
  })
})
</script>
