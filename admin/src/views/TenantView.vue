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
      <el-table-column label="API 密钥 (Secret Key)" width="200">
        <template #default="{ row }">
           <div class="flex items-center gap-2">
             <span class="font-mono text-xs text-gray-500 truncate w-[100px]">{{ row.secret_key }}</span>
             <el-button link type="primary" size="small" @click="copyKey(row.secret_key)">复制</el-button>
           </div>
        </template>
      </el-table-column>
      <el-table-column prop="contact" label="联系人" width="120" />
      <el-table-column prop="phone" label="联系电话" width="150" />
      <el-table-column prop="address" label="地址" min-width="200" show-overflow-tooltip />
      <el-table-column label="操作" width="150" fixed="right" align="center">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleEdit(row)">编辑</el-button>
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

const form = reactive({
  id: 0,
  name: '',
  system_code: '',
  contact: '',
  phone: '',
  address: '',
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
    admin_username: '', admin_password: '' 
  })
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  Object.assign(form, row)
  dialogVisible.value = true
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('确定要删除该商户主体吗？删除后其下所有数据将不可恢复！', '高风险操作警告', {
    confirmButtonText: '确定删除',
    cancelButtonText: '取消',
    type: 'warning',
  }).then(async () => {
    try {
      await request.delete(`/tenants/${row.id}`)
      ElMessage.success('已删除')
      fetchData()
    } catch (error) {
      ElMessage.error('删除失败')
    }
  })
}

const handleSubmit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      try {
        if (isEdit.value) {
          await request.put(`/tenants/${form.id}`, form)
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

const copyKey = (key: string) => {
  navigator.clipboard.writeText(key).then(() => {
    ElMessage.success('密钥已复制')
  })
}

onMounted(() => {
  fetchData()
})
</script>
