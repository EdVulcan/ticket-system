<template>
  <div class="user-view">
    <div class="header">
      <h2>系统员管理 (后台账号)</h2>
      <el-button type="primary" @click="dialogVisible = true">新增管理员</el-button>
    </div>

    <el-alert title="注意：此处管理的账号用于登录“本后台管理系统”，非窗口售票员。" type="info" show-icon class="mb-4" :closable="false" />

    <el-table :data="userList" style="width: 100%" v-loading="loading">
      <el-table-column prop="username" label="登录用户名" width="180" />
      <el-table-column prop="role" label="角色">
        <template #default="scope">
          <el-tag :type="scope.row.role === 'admin' ? 'danger' : 'success'">
            {{ roleMap[scope.row.role] || scope.row.role }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" />
      <el-table-column label="操作" width="200">
        <template #default="scope">
          <el-button size="small" type="warning" @click="handleResetPassword(scope.row)">重置密码</el-button>
          <el-button 
            size="small" 
            type="danger" 
            @click="handleDelete(scope.row)" 
            :disabled="scope.row.role === 'admin' || scope.row.id === currentUser.id || currentUser.role !== 'admin'"
            :title="scope.row.id === currentUser.id ? '无法删除自己' : (currentUser.role !== 'admin' ? '权限不足' : '')"
          >
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Create Dialog -->
    <el-dialog v-model="dialogVisible" title="新增系统管理员" width="30%">
      <el-form :model="form" label-width="80px">
        <el-form-item label="用户名">
          <el-input v-model="form.username" placeholder="例如: finance_01" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role" class="w-full">
            <el-option label="普通管理员 (Sub-Admin)" value="sub_admin" />
            <el-option label="财务 (Finance)" value="finance" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleCreate">确定</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import request from '@/utils/request'
import { ElMessage, ElMessageBox } from 'element-plus'

const userList = ref([])
const loading = ref(false)
const dialogVisible = ref(false)

const roleMap: Record<string, string> = {
  admin: '超级管理员 (主)',
  sub_admin: '普通管理员',
  finance: '财务专员'
}

const form = reactive({
  username: '',
  password: '',
  role: 'sub_admin'
})

const currentUser = ref<any>({})

const fetchUsers = async () => {
  loading.value = true
  try {
    const userStr = localStorage.getItem('user')
    if (userStr) currentUser.value = JSON.parse(userStr)

    const res = await request.get('/users') // Hits UserController.List
    userList.value = res.data
  } catch (error) {
    ElMessage.error('获取管理员列表失败')
  } finally {
    loading.value = false
  }
}

const handleCreate = async () => {
  if (!form.username || !form.password) {
    ElMessage.warning('请填写完整信息')
    return
  }
  
  try {
    await request.post('/users', form)
    ElMessage.success('创建成功')
    dialogVisible.value = false
    fetchUsers()
    // Reset form
    form.username = ''
    form.password = ''
    form.role = 'sub_admin'
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '创建失败')
  }
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确定删除该管理员吗?', '提示', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    type: 'warning'
  }).then(async () => {
    try {
      await request.delete(`/users/${row.id}`)
      ElMessage.success('删除成功')
      fetchUsers()
    } catch (error) {
      ElMessage.error('删除失败')
    }
  })
}

const handleResetPassword = (row: any) => {
  ElMessageBox.prompt('请输入新密码', '重置密码', {
    confirmButtonText: '确定',
    cancelButtonText: '取消',
    inputPattern: /.{6,}/,
    inputErrorMessage: '密码长度至少6位'
  }).then(async ({ value }) => {
    try {
      await request.put(`/users/${row.id}/password`, { password: value })
      ElMessage.success('密码重置成功')
    } catch (error) {
      ElMessage.error('重置失败')
    }
  })
}

onMounted(() => {
  fetchUsers()
})
</script>

<style scoped>
.header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
</style>
