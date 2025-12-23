<template>
  <div class="h-full flex flex-col p-6 space-y-4">
    <div class="flex justify-between items-center">
      <h2 class="text-2xl font-bold text-gray-800">政策知识库 (Policy Knowledge Base)</h2>
      <el-button type="primary" @click="handleAdd">
        <el-icon class="mr-2"><Plus /></el-icon> 发布新政策
      </el-button>
    </div>

    <!-- Category Filter -->
    <div class="flex gap-2">
      <el-radio-group v-model="filterCategory" @change="fetchPolicies">
        <el-radio-button label="">全部</el-radio-button>
        <el-radio-button label="Admission">入园/免票</el-radio-button>
        <el-radio-button label="Discount">优惠政策</el-radio-button>
        <el-radio-button label="Refund">退改说明</el-radio-button>
        <el-radio-button label="Pet">宠物政策</el-radio-button>
        <el-radio-button label="Other">其他</el-radio-button>
      </el-radio-group>
    </div>

    <!-- Policy List -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6 overflow-y-auto pb-4">
       <div v-for="policy in policies" :key="policy.id" class="bg-white rounded-xl shadow-sm border border-gray-100 hover:shadow-md transition-shadow flex flex-col">
          <div class="p-5 flex-1">
             <div class="flex justify-between items-start mb-3">
                 <el-tag :type="getCategoryType(policy.category)" effect="light">{{ getCategoryLabel(policy.category) }}</el-tag>
                 <el-switch v-model="policy.is_active" size="small" @change="updateStatus(policy)" />
             </div>
             <h3 class="text-lg font-bold text-gray-800 mb-2 truncate">{{ policy.title }}</h3>
             <p class="text-gray-500 text-sm line-clamp-4 whitespace-pre-wrap">{{ policy.content }}</p>
          </div>
          <div class="p-4 border-t border-gray-50 flex justify-end gap-2 bg-gray-50 rounded-b-xl">
             <el-button link type="primary" @click="handleEdit(policy)">编辑</el-button>
             <el-button link type="danger" @click="handleDelete(policy)">删除</el-button>
          </div>
       </div>

       <!-- Empty State -->
       <div v-if="policies.length === 0" class="col-span-full flex flex-col items-center justify-center py-20 text-gray-400">
          <el-icon class="text-4xl mb-2"><Document /></el-icon>
          <p>暂无相关政策文档</p>
       </div>
    </div>

    <!-- Dialog -->
    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑政策' : '发布新政策'" width="600px">
        <el-form :model="form" label-position="top" :rules="rules" ref="formRef">
            <el-form-item label="标题" prop="title">
                <el-input v-model="form.title" placeholder="如：2024年入园免票规定" />
            </el-form-item>
            <el-form-item label="分类" prop="category">
                <el-select v-model="form.category" placeholder="请选择分类" class="w-full">
                    <el-option label="入园/免票" value="Admission" />
                    <el-option label="优惠政策" value="Discount" />
                    <el-option label="退改说明" value="Refund" />
                    <el-option label="宠物政策" value="Pet" />
                    <el-option label="其他" value="Other" />
                </el-select>
            </el-form-item>
            <el-form-item label="正文内容" prop="content">
                <el-input v-model="form.content" type="textarea" rows="8" placeholder="支持纯文本输入..." />
            </el-form-item>
        </el-form>
        <template #footer>
            <el-button @click="dialogVisible = false">取消</el-button>
            <el-button type="primary" @click="handleSubmit">保存</el-button>
        </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { Plus, Document } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { ElMessage, ElMessageBox } from 'element-plus'

const policies = ref<any[]>([])
const filterCategory = ref('')
const dialogVisible = ref(false)
const isEdit = ref(false)
const formRef = ref()

const form = reactive({
    id: 0,
    title: '',
    category: '',
    content: '',
    is_active: true
})

const rules = {
    title: [{ required: true, message: '请输入标题', trigger: 'blur' }],
    category: [{ required: true, message: '请选择分类', trigger: 'change' }],
    content: [{ required: true, message: '请输入内容', trigger: 'blur' }]
}

const fetchPolicies = async () => {
    try {
        const res = await request.get('/policies', { params: { category: filterCategory.value } })
        policies.value = res.data.data
    } catch (e) {
        ElMessage.error('加载失败')
    }
}

const handleAdd = () => {
    isEdit.value = false
    Object.assign(form, { id: 0, title: '', category: 'Admission', content: '', is_active: true })
    dialogVisible.value = true
}

const handleEdit = (row: any) => {
    isEdit.value = true
    Object.assign(form, row)
    dialogVisible.value = true
}

const handleDelete = (row: any) => {
    ElMessageBox.confirm('确定要删除此政策吗？', '提示', { type: 'warning' })
        .then(async () => {
            await request.delete(`/policies/${row.id}`)
            fetchPolicies()
            ElMessage.success('已删除')
        })
}

const handleSubmit = async () => {
    if (!formRef.value) return
    await formRef.value.validate(async (valid: boolean) => {
        if (valid) {
            try {
                if (isEdit.value) {
                    await request.put(`/policies/${form.id}`, form)
                } else {
                    await request.post('/policies', form)
                }
                ElMessage.success('保存成功')
                dialogVisible.value = false
                fetchPolicies()
            } catch (e) {
                ElMessage.error('操作失败')
            }
        }
    })
}

const updateStatus = async (row: any) => {
    try {
        await request.put(`/policies/${row.id}`, row)
        ElMessage.success('状态已更新')
    } catch (e) {
        ElMessage.error('更新失败')
    }
}

const getCategoryLabel = (val: string) => {
    const map: any = { 'Admission': '入园/免票', 'Discount': '优惠政策', 'Refund': '退改说明', 'Pet': '宠物政策', 'Other': '其他' }
    return map[val] || val
}

const getCategoryType = (val: string) => {
    const map: any = { 'Admission': 'primary', 'Discount': 'success', 'Refund': 'danger', 'Pet': 'warning', 'Other': 'info' }
    return map[val] || 'info'
}

onMounted(() => {
    fetchPolicies()
})
</script>
