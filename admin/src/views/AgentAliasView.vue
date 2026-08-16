<template>
  <main class="alias-page">
    <header class="page-heading">
      <div>
        <span class="eyebrow">ASSISTANT VOCABULARY</span>
        <h2>AI 业务别名</h2>
        <p>给当前租户的景区、检票点和自有票种维护常用叫法。别名只用于理解输入，不会创建新对象或扩大权限。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新别名" :loading="loading" @click="load" />
    </header>

    <section class="alias-panel">
      <el-alert title="保存时系统会校验目标名称仍属于当前租户；目标被删除或重名后，AI 会拒绝使用该别名。" type="info" :closable="false" show-icon />
      <el-form class="alias-form" label-position="top" @submit.prevent="save">
        <el-form-item label="对象类型">
          <el-select v-model="form.kind" class="kind-select">
            <el-option label="景区" value="scenic_area" />
            <el-option label="检票点" value="checkpoint" />
            <el-option label="票种" value="product" />
          </el-select>
        </el-form-item>
        <el-form-item label="常用叫法">
          <el-input v-model="form.alias" maxlength="100" placeholder="例如：8号点" />
        </el-form-item>
        <el-form-item label="系统名称">
          <el-input v-model="form.canonical_name" maxlength="100" placeholder="例如：水上乐园" />
        </el-form-item>
        <el-button type="primary" :loading="saving" :disabled="!form.alias.trim() || !form.canonical_name.trim()" @click="save">保存别名</el-button>
      </el-form>

      <el-table :data="rows" v-loading="loading" border class="alias-table">
        <el-table-column label="类型" width="120">
          <template #default="{ row }">{{ kindLabel(row.kind) }}</template>
        </el-table-column>
        <el-table-column prop="alias" label="常用叫法" min-width="180" />
        <el-table-column prop="canonical_name" label="系统名称" min-width="220" />
        <el-table-column label="操作" width="100" align="right">
          <template #default="{ row }">
            <el-button text type="danger" :loading="deleting === row.id" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂未设置业务别名" />
        </template>
      </el-table>
    </section>
  </main>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

const rows = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const deleting = ref(0)
const form = reactive({ kind: 'checkpoint', alias: '', canonical_name: '' })

const kindLabel = (kind: string) => ({ scenic_area: '景区', checkpoint: '检票点', product: '票种' } as Record<string, string>)[kind] || kind

const load = async () => {
  loading.value = true
  try {
    rows.value = (await request.get('/agent/aliases')).data?.data || []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || 'AI 业务别名加载失败')
  } finally { loading.value = false }
}

const save = async () => {
  if (!form.alias.trim() || !form.canonical_name.trim()) return
  saving.value = true
  try {
    await request.post('/agent/aliases', { kind: form.kind, alias: form.alias.trim(), canonical_name: form.canonical_name.trim() })
    form.alias = ''
    form.canonical_name = ''
    ElMessage.success('业务别名已保存')
    await load()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '业务别名保存失败')
  } finally { saving.value = false }
}

const remove = async (row: any) => {
  try {
    await ElMessageBox.confirm(`删除“${row.alias}”这个别名？`, '删除业务别名', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  deleting.value = row.id
  try {
    await request.delete(`/agent/aliases/${row.id}`)
    ElMessage.success('业务别名已删除')
    await load()
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '业务别名删除失败')
  } finally { deleting.value = 0 }
}

onMounted(load)
</script>

<style scoped>
.alias-page { min-height: 100%; }
.page-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; margin-bottom: 20px; }
.page-heading h2 { margin: 4px 0 6px; color: #18202b; font-size: 22px; line-height: 30px; }
.page-heading p { max-width: 760px; margin: 0; color: #667085; font-size: 13px; line-height: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.alias-panel { max-width: 980px; padding: 20px; background: #fff; border: 1px solid #e2e7ee; border-radius: 6px; }
.alias-form { display: grid; grid-template-columns: 150px minmax(0, 1fr) minmax(0, 1fr) auto; align-items: end; gap: 14px; margin: 20px 0; }
.alias-form :deep(.el-form-item) { margin-bottom: 0; }
.kind-select { width: 100%; }
.alias-table { margin-top: 8px; }
@media (max-width: 760px) {
  .page-heading { align-items: stretch; flex-direction: column; }
  .alias-panel { padding: 15px; }
  .alias-form { grid-template-columns: 1fr; gap: 8px; }
}
</style>
