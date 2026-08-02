<template>
  <div class="staff-view">
    <div class="header">
      <h2>员工管理</h2>
      <el-button type="primary" @click="dialogVisible = true">新增员工</el-button>
    </div>

    <el-table :data="staffList" style="width: 100%" v-loading="loading">
      <el-table-column prop="job_number" label="工号" width="120" />
      <el-table-column prop="name" label="姓名" width="120" />
      <el-table-column prop="roles" label="角色">
        <template #default="scope">
          <el-tag v-for="role in scope.row.roles.split(',')" :key="role" class="mr-1">
            {{ roleMap[role] || '其他角色' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="100">
        <template #default="scope">
          <el-tag :type="scope.row.status === 'active' ? 'success' : 'danger'">
            {{ scope.row.status === 'active' ? '正常' : '冻结' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" />
      <el-table-column label="操作" width="280">
        <template #default="scope">
          <el-button size="small" type="warning" @click="handleResetPassword(scope.row)">重置密码</el-button>
          <el-button size="small" @click="openScopes(scope.row)">资源范围</el-button>
          <el-button size="small" type="danger" @click="handleDelete(scope.row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Create Dialog -->
    <el-dialog v-model="dialogVisible" title="新增员工" width="30%">
      <el-form :model="form" label-width="80px">
        <el-form-item label="工号">
          <el-input v-model="form.job_number" placeholder="例如: 1001" />
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="form.name" placeholder="员工真实姓名" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password />
        </el-form-item>
        <el-form-item label="角色">
          <el-checkbox-group v-model="form.roles">
            <el-checkbox label="seller">售票员</el-checkbox>
            <el-checkbox label="checker">验票员</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleCreate">确定</el-button>
        </span>
      </template>
    </el-dialog>
    <el-dialog v-model="scopeDialogVisible" title="员工资源范围" width="560px">
      <el-form label-position="top">
        <el-form-item label="景区">
          <el-select v-model="scopeForm.scenic_area" multiple class="w-full"><el-option v-for="item in resources.scenic_area" :key="item.id" :label="item.name" :value="item.id" /></el-select>
        </el-form-item>
        <el-form-item label="检票点">
          <el-select v-model="scopeForm.checkpoint" multiple class="w-full"><el-option v-for="item in resources.checkpoint" :key="item.id" :label="item.name" :value="item.id" /></el-select>
        </el-form-item>
        <el-form-item label="设备">
          <el-select v-model="scopeForm.device" multiple class="w-full"><el-option v-for="item in resources.device" :key="item.id" :label="item.name" :value="item.id" /></el-select>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="scopeDialogVisible = false">取消</el-button><el-button type="primary" @click="saveScopes">保存</el-button></template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import request from '@/utils/request'
import { ElMessage, ElMessageBox } from 'element-plus'

const staffList = ref([])
const loading = ref(false)
const dialogVisible = ref(false)
const scopeDialogVisible = ref(false)
const selectedStaff = ref<any>()
const resources = reactive<Record<string, any[]>>({ scenic_area: [], checkpoint: [], device: [] })
const scopeForm = reactive<Record<string, number[]>>({ scenic_area: [], checkpoint: [], device: [] })

const roleMap: Record<string, string> = {
  seller: '售票员',
  checker: '验票员'
}

const form = reactive({
  job_number: '',
  name: '',
  password: '',
  roles: [] as string[]
})

const fetchStaff = async () => {
  loading.value = true
  try {
    const res = await request.get('/staff')
    staffList.value = res.data
  } catch (error) {
    ElMessage.error('获取员工列表失败')
  } finally {
    loading.value = false
  }
}

const handleCreate = async () => {
  if (!form.job_number || !form.name || !form.password || form.roles.length === 0) {
    ElMessage.warning('请填写完整信息')
    return
  }
  
  const payload = {
    ...form,
    roles: form.roles.join(',')
  }

  try {
    await request.post('/staff', payload)
    ElMessage.success('创建成功')
    dialogVisible.value = false
    fetchStaff()
    // Reset form
    form.job_number = ''
    form.name = ''
    form.password = ''
    form.roles = []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '创建失败')
  }
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确定删除该员工吗?', '提示', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    type: 'warning'
  }).then(async () => {
    try {
      await request.delete(`/staff/${row.id}`)
      ElMessage.success('删除成功')
      fetchStaff()
    } catch (error) {
      ElMessage.error('删除失败')
    }
  })
}

const handleResetPassword = (row: any) => {
  ElMessageBox.prompt('请输入新密码', '重置密码', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    inputPattern: /.{8,}/,
    inputErrorMessage: '密码长度至少8位'
  }).then(async ({ value }) => {
    try {
      await request.put(`/staff/${row.id}/password`, { password: value })
      ElMessage.success('密码重置成功')
    } catch (error) {
      ElMessage.error('重置失败')
    }
  })
}

const openScopes = async (row: any) => {
  selectedStaff.value = row
  const [areas, checkpoints, devices] = await Promise.all([
    request.get('/scenic-areas'), request.get('/checkpoints', { params: { page_size: 100 } }), request.get('/devices', { params: { page_size: 100 } })
  ])
  resources.scenic_area = areas.data.data || []
  resources.checkpoint = checkpoints.data.data || checkpoints.data || []
  resources.device = devices.data.data || devices.data || []
  for (const type of Object.keys(scopeForm)) {
    scopeForm[type] = (row.resource_scopes || []).filter((item: any) => item.resource_type === type).map((item: any) => item.resource_id)
  }
  scopeDialogVisible.value = true
}

const saveScopes = async () => {
  const scopes = Object.entries(scopeForm).flatMap(([resource_type, ids]) => ids.map(resource_id => ({ resource_type, resource_id })))
  await request.put(`/staff/${selectedStaff.value.id}/resource-scopes`, { scopes })
  scopeDialogVisible.value = false
  ElMessage.success('资源范围已更新')
  await fetchStaff()
}

onMounted(() => {
  fetchStaff()
})
</script>

<style scoped>
.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
.mr-1 {
  margin-right: 4px;
}
</style>
