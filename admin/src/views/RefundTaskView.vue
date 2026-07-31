<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between"><div><h2 class="text-xl font-semibold text-gray-900">退款待办</h2><p class="text-sm text-gray-500 mt-1">查看数字退款执行、重试和人工复核状态。</p></div><el-button :icon="Refresh" circle title="刷新" @click="load" /></div>
    <el-alert v-if="manualCount" type="warning" :closable="false" :title="`有 ${manualCount} 条退款进入人工复核`" />
    <el-table :data="tasks" v-loading="loading" stripe>
      <el-table-column prop="id" label="任务" width="90"/><el-table-column prop="refund_id" label="退款单" width="100"/><el-table-column prop="provider" label="渠道" width="110"/><el-table-column prop="status" label="状态" width="140"><template #default="{row}"><el-tag :type="row.status === 'manual_review' ? 'danger' : row.status === 'succeeded' ? 'success' : 'warning'">{{ row.status }}</el-tag></template></el-table-column><el-table-column prop="attempt_count" label="尝试次数" width="110"/><el-table-column prop="failure_code" label="失败码" width="160"/><el-table-column prop="last_error" label="最后错误" min-width="260" show-overflow-tooltip/><el-table-column label="操作" width="150"><template #default="{row}"><el-button v-if="row.status === 'manual_review' || row.status === 'failed'" link type="primary" @click="retry(row)">人工重试</el-button></template></el-table-column>
    </el-table>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
const tasks = ref<any[]>([]); const loading = ref(false)
const manualCount = computed(() => tasks.value.filter(row => row.status === 'manual_review').length)
const load = async () => { loading.value = true; try { tasks.value = (await request.get('/payments/refund-tasks', { params: { page: 1, page_size: 100 } })).data.data || [] } finally { loading.value = false } }
const retry = async (row: any) => { const reason = await ElMessageBox.prompt('请输入重试原因', '人工重试', { inputPlaceholder: '例如：支付商户凭据已修复' }); await request.post(`/payments/refund-tasks/${row.id}/retry`, { reason: reason.value }); ElMessage.success('已重新排队'); await load() }
onMounted(load)
</script>
