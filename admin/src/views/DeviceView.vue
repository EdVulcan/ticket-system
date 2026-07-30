<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <h2 class="text-lg font-bold text-gray-900">设备管理</h2>
      <el-button type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> 新增设备
      </el-button>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading">
      <el-table-column prop="id" label="ID" width="80" />
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
      <el-table-column prop="ip_address" label="IP地址" width="140" />
      <el-table-column label="操作" width="230" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
          <el-button link type="warning" size="small" @click="handleRotateKey(row)">轮换密钥</el-button>
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
            <el-option label="桌面POS" value="pos" />
          </el-select>
        </el-form-item>
        <el-form-item label="IP地址" prop="ip_address">
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

    <el-dialog v-model="credentialVisible" title="设备接入密钥" width="520px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" show-icon class="mb-4">
        此密钥只显示一次。请立即配置到设备 {{ credentialDeviceName }}，系统不会保存明文。
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
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

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
    pos: '桌面POS'
  }
  return map[type] || type
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
  Object.assign(form, row)
  dialogVisible.value = true
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确认删除该设备吗？', '警告', {
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

const showCredential = (deviceName: string, key: string) => {
  credentialDeviceName.value = deviceName
  credentialKey.value = key
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

const handleSubmit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      try {
        if (isEdit.value) {
          await request.put(`/devices/${form.id}`, form)
        } else {
          const response = await request.post('/devices', form)
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
