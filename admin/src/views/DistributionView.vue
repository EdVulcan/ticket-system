<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100 flex justify-between items-center">
      <div>
        <h2 class="text-lg font-bold text-gray-900">分销中心 (B2B)</h2>
        <p class="text-xs text-gray-500 mt-1">连接其他主体，获取更多优质旅游产品</p>
      </div>
      <el-button type="primary" size="large" @click="dialogVisible = true">
        <el-icon class="mr-2"><Connection /></el-icon> 寻找供应商
      </el-button>
    </div>

    <!-- My Suppliers List -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
      <div class="flex items-center justify-between mb-4">
        <h3 class="font-bold text-gray-700">我的供应商</h3>
        <el-input v-model="searchQuery" placeholder="搜索供应商名称..." class="w-64" prefix-icon="Search" />
      </div>

      <el-table :data="suppliers" style="width: 100%" v-loading="loading">
        <el-table-column prop="supplier_name" label="供应商名称" min-width="180">
          <template #default="{ row }">
            <div class="font-medium">{{ row.supplier_name }}</div>
            <div class="text-xs text-gray-400">系统编号: {{ row.supplier_code }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="合作状态" width="120">
          <template #default="{ row }">
            <el-tag :type="getStatusType(row.status)">{{ getStatusText(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="agent_level" label="分销等级" width="120">
          <template #default="{ row }">
            <el-tag effect="plain">{{ getLevelText(row.agent_level) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="balance" label="预付余额" width="150" align="right">
          <template #default="{ row }">
            <span class="font-mono font-bold text-orange-500">¥{{ row.balance || '0.00' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right" align="center">
          <template #default="{ row }">
            <el-button link type="primary" size="small">查看商品</el-button>
            <el-button link type="warning" size="small">充值</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- Apply Dialog -->
    <el-dialog v-model="dialogVisible" title="申请代理权益" width="500px">
      <el-form label-position="top">
        <el-form-item label="请输入目标供应商的系统编号 (System Code)">
          <div class="flex gap-2">
            <el-input v-model="targetSystemCode" placeholder="例如: SYS001" class="flex-1" />
            <el-button @click="handleSearch" :loading="searching">查询</el-button>
          </div>
        </el-form-item>

        <div v-if="foundSupplier" class="bg-gray-50 p-4 rounded-lg mb-4 border border-gray-200">
           <div class="flex items-center gap-3 mb-2">
             <el-avatar :size="40" class="bg-indigo-100 text-indigo-500 font-bold">{{ foundSupplier.name.charAt(0) }}</el-avatar>
             <div>
               <div class="font-bold text-gray-800">{{ foundSupplier.name }}</div>
               <div class="text-xs text-gray-500">联系人: {{ foundSupplier.contact || '暂无' }}</div>
               <div class="text-xs text-gray-400 font-mono">CODE: {{ foundSupplier.code }}</div>
             </div>
           </div>
           <el-alert title="确认申请后，需等待对方审核通过才可代理其产品。" type="info" :closable="false" />
        </div>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleApply" :disabled="!foundSupplier" :loading="applying">确认申请</el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Connection, Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const loading = ref(false)
const suppliers = ref<any[]>([])
const searchQuery = ref('')

const dialogVisible = ref(false)
const targetSystemCode = ref('')
const searching = ref(false)
const applying = ref(false)
const foundSupplier = ref<any>(null)

const fetchSuppliers = async () => {
  loading.value = true
  try {
     const res = await request.get('/distribution/suppliers')
     if (res.data.data) {
        suppliers.value = res.data.data
     } else {
        suppliers.value = []
     }
  } catch (e) {
     ElMessage.error('获取供应商列表失败')
  } finally {
     loading.value = false
  }
}

const handleSearch = async () => {
  if (!targetSystemCode.value) return
  searching.value = true
  foundSupplier.value = null
  try {
    const res = await request.get('/distribution/search', { params: { code: targetSystemCode.value }})
    foundSupplier.value = res.data.data
  } catch (e: any) {
    ElMessage.warning(e.response?.data?.error || '未找到该供应商')
    foundSupplier.value = null
  } finally {
    searching.value = false
  }
}

const handleApply = async () => {
    if (!foundSupplier.value) return
    applying.value = true
    try {
        await request.post('/distribution/apply', { system_code: foundSupplier.value.code })
        ElMessage.success('申请已提交')
        dialogVisible.value = false
        foundSupplier.value = null
        targetSystemCode.value = ''
        fetchSuppliers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '申请失败')
    } finally {
        applying.value = false
    }
}

const getStatusType = (status: string) => {
    const map: any = { active: 'success', pending: 'warning', rejected: 'danger' }
    return map[status] || 'info'
}

const getStatusText = (status: string) => {
    const map: any = { active: '合作中', pending: '审核中', rejected: '已拒绝' }
    return map[status] || status
}

const getLevelText = (level: string) => {
    const map: any = { standard: '普通代理', core: '核心代理', diamond: '金牌代理' }
    return map[level] || level
}

onMounted(() => {
    fetchSuppliers()
})
</script>
