<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <h2 class="text-lg font-bold text-gray-900">商户开户管理 (Tenant Management)</h2>
      <el-button type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> 新增商户主体
      </el-button>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading">
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column prop="name" label="商户名称" min-width="150" />
      <el-table-column prop="system_code" label="系统编号 (System Code)" width="180">
        <template #default="{ row }">
          <el-tag effect="dark" type="warning" class="font-mono text-base font-bold">{{ row.system_code }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="140">
        <template #default="{ row }">
          <el-select :model-value="row.status" size="small" @change="(value: string) => updateStatus(row, value)">
            <el-option label="启用" value="active" />
            <el-option label="冻结" value="frozen" />
            <el-option label="关闭" value="closed" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="资质" width="130">
        <template #default="{ row }">
          <el-tag :type="row.qualification_status === 'approved' ? 'success' : 'warning'">{{ row.qualification_status || 'legacy' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="业务能力" min-width="260">
        <template #default="{ row }">
          <el-button v-for="capability in capabilityOptions" :key="capability" size="small" :type="capabilityStatus(row, capability) === 'active' ? 'success' : 'info'" @click="toggleCapability(row, capability)">
            {{ capability }} · {{ capabilityStatus(row, capability) }}
          </el-button>
        </template>
      </el-table-column>
      <el-table-column prop="contact" label="联系人" width="120" />
      <el-table-column prop="phone" label="联系电话" width="150" />
      <el-table-column prop="address" label="地址" min-width="200" show-overflow-tooltip />
      <el-table-column label="操作" width="170" fixed="right" align="center">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
          <el-button link type="warning" size="small" @click="revokeSessions(row)">撤销会话</el-button>
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
      :title="isEdit ? '编辑商户信息' : '创建新商户主体'"
      width="500px"
    >
      <el-form :model="form" label-width="100px" :rules="rules" ref="formRef" label-position="top">
        <el-form-item label="商户主体名称" prop="name">
          <el-input v-model="form.name" placeholder="请输入公司或景区名称" />
        </el-form-item>
        <el-form-item label="分配系统编号 (System Code)" prop="system_code">
          <el-input v-model="form.system_code" placeholder="用于跨系统对接的唯一ID，如：SH001" />
          <div class="text-xs text-gray-400 mt-1">此编号将用于 B2B 分销对接，创建后建议不要修改。</div>
        </el-form-item>
        <div class="grid grid-cols-2 gap-4">
            <el-form-item label="联系人" prop="contact">
            <el-input v-model="form.contact" />
            </el-form-item>
            <el-form-item label="联系电话" prop="phone">
            <el-input v-model="form.phone" />
            </el-form-item>
        </div>
        <el-form-item label="联系地址" prop="address">
          <el-input v-model="form.address" type="textarea" />
        </el-form-item>

        <template v-if="isEdit">
          <div class="grid grid-cols-2 gap-4 border-t border-gray-100 pt-4 mt-2">
            <el-form-item label="资质状态">
              <el-select v-model="form.qualification_status" class="w-full">
                <el-option label="待审核" value="pending" /><el-option label="已通过" value="approved" />
                <el-option label="已驳回" value="rejected" /><el-option label="已过期" value="expired" />
              </el-select>
            </el-form-item>
            <el-form-item label="资质编号"><el-input v-model="form.qualification_no" /></el-form-item>
          </div>
          <div class="grid grid-cols-2 gap-4">
            <el-form-item label="资质到期">
              <el-date-picker v-model="form.qualification_expires_at" type="date" value-format="YYYY-MM-DDT00:00:00Z" class="w-full" />
            </el-form-item>
            <el-form-item label="合同到期">
              <el-date-picker v-model="form.contract_expires_at" type="date" value-format="YYYY-MM-DDT00:00:00Z" class="w-full" />
            </el-form-item>
          </div>
          <el-form-item label="关闭/变更原因"><el-input v-model="form.lifecycle_reason" type="textarea" /></el-form-item>
        </template>

        <div v-if="!isEdit" class="grid grid-cols-2 gap-4 border-t border-gray-100 pt-4 mt-2">
            <el-form-item label="管理员账号" prop="admin_username">
              <el-input v-model="form.admin_username" placeholder="默认: admin" />
            </el-form-item>
            <el-form-item label="初始密码" prop="admin_password">
              <el-input v-model="form.admin_password" type="password" show-password placeholder="必填" />
            </el-form-item>
        </div>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleSubmit">确认提交</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { Plus } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const dialogVisible = ref(false)
const isEdit = ref(false)
const formRef = ref()

const form = reactive({
  id: 0,
  name: '',
  system_code: '',
  contact: '',
  phone: '',
  address: '',
  qualification_status: 'pending',
  qualification_no: '',
  qualification_expires_at: '',
  contract_expires_at: '',
  lifecycle_reason: '',
  admin_username: '',
  admin_password: ''
})

const rules = computed(() => {
    const base = {
        name: [{ required: true, message: '请输入商户名称', trigger: 'blur' }],
        system_code: [{ required: true, message: '请输入系统编号', trigger: 'blur' }]
    }
    if (!isEdit.value) {
        return {
            ...base,
            admin_password: [{ required: true, message: '请设置初始密码', trigger: 'blur' }]
        }
    }
    return base
})

const fetchData = async () => {
  loading.value = true
  try {
    const res = await request.get('/tenants', {
      params: { page: currentPage.value, page_size: pageSize.value }
    })
    tableData.value = res.data.data
    total.value = res.data.total
  } catch (error) {
    ElMessage.error('数据获取失败')
  } finally {
    loading.value = false
  }
}

const handleAdd = () => {
  isEdit.value = false
  Object.assign(form, { 
    id: 0, name: '', system_code: '', contact: '', phone: '', address: '',
    qualification_status: 'pending', qualification_no: '', qualification_expires_at: '', contract_expires_at: '', lifecycle_reason: '',
    admin_username: '', admin_password: '' 
  })
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  Object.assign(form, row)
  dialogVisible.value = true
}

const handleSubmit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      try {
        if (isEdit.value) {
          await request.put(`/tenants/${form.id}`, form)
          await request.patch(`/tenants/${form.id}/lifecycle`, {
            qualification_status: form.qualification_status,
            qualification_no: form.qualification_no,
            qualification_expires_at: form.qualification_expires_at || undefined,
            contract_expires_at: form.contract_expires_at || undefined,
            reason: form.lifecycle_reason || '平台管理端更新租户生命周期'
          })
        } else {
          await request.post('/tenants', form)
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

const capabilityOptions = ['supplier', 'distributor', 'travel_agency']
const capabilityStatus = (row: any, capability: string) => row.capabilities?.find((item: any) => item.capability === capability)?.status || 'disabled'
const updateStatus = async (row: any, status: string) => {
  try { await request.patch(`/tenants/${row.id}/status`, { status }); await fetchData() }
  catch (error: any) { ElMessage.error(error.response?.data?.error || '状态更新失败') }
}
const toggleCapability = async (row: any, capability: string) => {
  const status = capabilityStatus(row, capability) === 'active' ? 'suspended' : 'active'
  try { await request.put(`/tenants/${row.id}/capabilities/${capability}`, { status, reason: '平台管理端调整' }); await fetchData() }
  catch (error: any) { ElMessage.error(error.response?.data?.error || '能力更新失败') }
}

const revokeSessions = async (row: any) => {
  try { await request.post(`/tenants/${row.id}/revoke-sessions`); ElMessage.success('已撤销该租户全部会话') }
  catch (error: any) { ElMessage.error(error.response?.data?.error || '会话撤销失败') }
}

onMounted(() => {
  fetchData()
})
</script>
