<template>
  <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
    <div class="flex justify-between items-center mb-6">
      <h2 class="text-lg font-bold text-gray-900">Tenant Management</h2>
      <el-button type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> Add Tenant
      </el-button>
    </div>

    <el-table :data="tableData" style="width: 100%" v-loading="loading">
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column prop="name" label="Name" min-width="150" />
      <el-table-column prop="system_code" label="System Code" width="150">
        <template #default="{ row }">
          <el-tag>{{ row.system_code }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="contact" label="Contact" width="120" />
      <el-table-column prop="phone" label="Phone" width="150" />
      <el-table-column prop="address" label="Address" min-width="200" show-overflow-tooltip />
      <el-table-column label="Actions" width="150" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="handleEdit(row)">Edit</el-button>
          <el-button link type="danger" size="small" @click="handleDelete(row)">Delete</el-button>
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
      :title="isEdit ? 'Edit Tenant' : 'Add Tenant'"
      width="500px"
    >
      <el-form :model="form" label-width="100px" :rules="rules" ref="formRef">
        <el-form-item label="Name" prop="name">
          <el-input v-model="form.name" />
        </el-form-item>
        <el-form-item label="System Code" prop="system_code">
          <el-input v-model="form.system_code" placeholder="Unique ID" />
        </el-form-item>
        <el-form-item label="Contact" prop="contact">
          <el-input v-model="form.contact" />
        </el-form-item>
        <el-form-item label="Phone" prop="phone">
          <el-input v-model="form.phone" />
        </el-form-item>
        <el-form-item label="Address" prop="address">
          <el-input v-model="form.address" type="textarea" />
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">Cancel</el-button>
          <el-button type="primary" @click="handleSubmit">Confirm</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'

// API Base URL (Should be in env)
const API_URL = 'http://localhost:8080/api/v1/tenants'

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
  address: ''
})

const rules = {
  name: [{ required: true, message: 'Please input name', trigger: 'blur' }],
  system_code: [{ required: true, message: 'Please input system code', trigger: 'blur' }]
}

const fetchData = async () => {
  loading.value = true
  try {
    const res = await axios.get(API_URL, {
      params: { page: currentPage.value, page_size: pageSize.value }
    })
    tableData.value = res.data.data
    total.value = res.data.total
  } catch (error) {
    ElMessage.error('Failed to fetch data')
  } finally {
    loading.value = false
  }
}

const handleAdd = () => {
  isEdit.value = false
  Object.assign(form, { id: 0, name: '', system_code: '', contact: '', phone: '', address: '' })
  dialogVisible.value = true
}

const handleEdit = (row: any) => {
  isEdit.value = true
  Object.assign(form, row)
  dialogVisible.value = true
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm('Are you sure to delete this tenant?', 'Warning', {
    confirmButtonText: 'OK',
    cancelButtonText: 'Cancel',
    type: 'warning',
  }).then(async () => {
    try {
      await axios.delete(`${API_URL}/${row.id}`)
      ElMessage.success('Delete completed')
      fetchData()
    } catch (error) {
      ElMessage.error('Delete failed')
    }
  })
}

const handleSubmit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (valid) {
      try {
        if (isEdit.value) {
          await axios.put(`${API_URL}/${form.id}`, form)
        } else {
          await axios.post(API_URL, form)
        }
        ElMessage.success(isEdit.value ? 'Update completed' : 'Create completed')
        dialogVisible.value = false
        fetchData()
      } catch (error) {
        ElMessage.error('Operation failed')
      }
    }
  })
}

onMounted(() => {
  fetchData()
})
</script>
