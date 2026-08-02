<template>
  <main class="max-w-[1200px] mx-auto">
    <div class="flex items-end justify-between border-b border-gray-200 pb-5 mb-6">
      <div>
        <h1 class="text-2xl font-semibold text-gray-900">平台账号</h1>
        <p class="text-sm text-gray-500 mt-1">管理系统服务商内部账号。初始平台管理员受保护，不能删除、停用或降权。</p>
      </div>
      <el-button type="primary" :icon="Plus" @click="openCreate">新增平台账号</el-button>
    </div>

    <el-table :data="users" v-loading="loading" style="width: 100%">
      <el-table-column prop="username" label="用户名" min-width="180" />
      <el-table-column label="角色" width="150">
        <template #default="{ row }"><el-tag :type="row.role === 'platform_admin' ? 'danger' : 'info'">{{ roleText(row.role) }}</el-tag></template>
      </el-table-column>
      <el-table-column label="账号类型" width="130">
        <template #default="{ row }">{{ row.is_initial_admin ? '初始账号' : '子账号' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'warning'">{{ row.status === 'active' ? '正常' : '已停用' }}</el-tag></template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" min-width="180" />
      <el-table-column label="操作" width="260" align="right">
        <template #default="{ row }">
          <el-button link type="primary" :disabled="row.is_initial_admin || row.id === currentUser.id" @click="openEdit(row)">权限与状态</el-button>
          <el-button link type="warning" :disabled="row.is_initial_admin || row.id === currentUser.id" @click="resetPassword(row)">重置密码</el-button>
          <el-button link type="danger" :disabled="row.is_initial_admin || row.id === currentUser.id" @click="deleteUser(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editingID ? '调整平台账号' : '新增平台账号'" width="460px">
      <el-form label-position="top">
        <el-form-item v-if="!editingID" label="用户名"><el-input v-model="form.username" autocomplete="off" /></el-form-item>
        <el-form-item v-if="!editingID" label="初始密码"><el-input v-model="form.password" type="password" show-password autocomplete="new-password" /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role" class="w-full">
            <el-option label="平台管理员（完整治理权限）" value="platform_admin" />
            <el-option label="平台运营员（只读运营数据）" value="platform_operator" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="editingID" label="状态">
          <el-select v-model="form.status" class="w-full"><el-option label="正常" value="active" /><el-option label="停用" value="frozen" /></el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </main>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const users = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const editingID = ref(0)
const currentUser = (() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })()
const form = reactive({ username: '', password: '', role: 'platform_operator', status: 'active' })
const roleText = (role: string) => role === 'platform_admin' ? '平台管理员' : '平台运营员'

const loadUsers = async () => {
  loading.value = true
  try { users.value = (await request.get('/platform-users')).data || [] }
  finally { loading.value = false }
}

const openCreate = () => {
  editingID.value = 0
  Object.assign(form, { username: '', password: '', role: 'platform_operator', status: 'active' })
  dialogVisible.value = true
}

const openEdit = (row: any) => {
  editingID.value = row.id
  Object.assign(form, { username: row.username, password: '', role: row.role, status: row.status })
  dialogVisible.value = true
}

const save = async () => {
  if (!editingID.value && (!form.username.trim() || form.password.length < 8)) {
    ElMessage.warning('请填写用户名，并设置至少8位的初始密码')
    return
  }
  saving.value = true
  try {
    if (editingID.value) await request.put(`/platform-users/${editingID.value}`, { role: form.role, status: form.status })
    else await request.post('/platform-users', { username: form.username.trim(), password: form.password, role: form.role })
    ElMessage.success(editingID.value ? '账号权限已更新' : '平台账号已创建')
    dialogVisible.value = false
    await loadUsers()
  } finally { saving.value = false }
}

const resetPassword = async (row: any) => {
  try {
    const { value } = await ElMessageBox.prompt(`为“${row.username}”设置新密码，保存后其现有登录会立即失效。`, '重置密码', {
      confirmButtonText: '确认重置', cancelButtonText: '取消', inputType: 'password', inputPattern: /.{8,}/, inputErrorMessage: '密码长度至少8位'
    })
    await request.put(`/platform-users/${row.id}/password`, { password: value })
    ElMessage.success('密码已重置')
  } catch (error: any) {
    if (error === 'cancel' || error === 'close') return
    throw error
  }
}

const deleteUser = async (row: any) => {
  try {
    await ElMessageBox.confirm(`确定删除平台账号“${row.username}”吗？该账号会立即无法登录。`, '删除平台账号', {
      confirmButtonText: '确认删除', cancelButtonText: '取消', type: 'warning'
    })
    await request.delete(`/platform-users/${row.id}`)
    ElMessage.success('平台账号已删除')
    await loadUsers()
  } catch (error: any) {
    if (error === 'cancel' || error === 'close') return
    throw error
  }
}

onMounted(loadUsers)
</script>
