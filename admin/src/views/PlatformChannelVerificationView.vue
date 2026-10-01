<template>
  <main class="verification-page">
    <header class="page-heading">
      <div>
        <span class="eyebrow">PLATFORM CHANNELS</span>
        <h2>小程序校验文件</h2>
        <p>由 SaaS 平台管理员按租户和渠道账号上传。租户管理员没有上传或删除权限。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
    </header>

    <el-alert
      class="notice"
      type="info"
      :closable="false"
      show-icon
      title="文件会保存到服务器持久化上传目录，仍通过根路径公开给微信或小红书校验；不需要登录服务器，也不会写入源码 admin/public。"
    />

    <section class="upload-panel">
      <div class="panel-title">上传或替换</div>
      <el-form label-position="top" class="upload-form">
        <el-form-item label="租户和渠道账号" required>
          <el-select v-model="form.accountId" filterable class="account-select" placeholder="选择租户的小程序账号" @change="handleAccountChange">
            <el-option v-for="account in accounts" :key="account.channel_account_id" :value="account.channel_account_id" :label="`${account.tenant_name}（${account.system_code}） · ${channelTypeText(account.channel_type)} · ${account.channel_code}`" />
          </el-select>
          <div v-if="selectedAccount" class="field-note">AppID：{{ selectedAccount.app_id || '未配置' }}；文件类型：{{ channelTypeText(selectedAccount.channel_type) }}</div>
        </el-form-item>
        <el-form-item label="校验文件" required>
          <input ref="fileInput" class="file-input" type="file" accept=".txt,text/plain" @change="handleFileChange" />
          <div class="field-note">微信文件名必须为 <code>MP_verify_*.txt</code>；小红书文件名必须为十六进制 <code>.txt</code>。最大 64 KB。</div>
        </el-form-item>
        <div class="form-actions">
          <span v-if="selectedFile" class="selected-file">{{ selectedFile.name }}（{{ formatBytes(selectedFile.size) }}）</span>
          <el-button type="primary" :loading="saving" :disabled="!selectedAccount || !selectedFile" @click="upload">上传文件</el-button>
        </div>
      </el-form>
    </section>

    <section class="table-panel">
      <el-table :data="rows" v-loading="loading" stripe>
        <el-table-column label="租户" min-width="220">
          <template #default="{ row }"><div class="tenant-cell"><strong>{{ row.tenant_name }}</strong><span>{{ row.system_code }}</span></div></template>
        </el-table-column>
        <el-table-column label="渠道" min-width="190">
          <template #default="{ row }"><div class="tenant-cell"><strong>{{ channelTypeText(row.channel_type) }}</strong><span>{{ row.channel_code }} · {{ row.app_id }}</span></div></template>
        </el-table-column>
        <el-table-column prop="filename" label="公开文件名" min-width="220" />
        <el-table-column label="公开地址" min-width="300" show-overflow-tooltip>
          <template #default="{ row }"><el-link :href="row.public_url" target="_blank" type="primary">{{ row.public_url }}</el-link></template>
        </el-table-column>
        <el-table-column label="更新时间" width="180"><template #default="{ row }">{{ formatDate(row.updated_at) }}</template></el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }"><el-button link type="danger" @click="remove(row)">删除</el-button></template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && rows.length === 0" description="暂无平台托管校验文件" />
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

const loading = ref(false)
const saving = ref(false)
const accounts = ref<any[]>([])
const rows = ref<any[]>([])
const selectedFile = ref<File | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const form = reactive({ accountId: 0 })

const selectedAccount = computed(() => accounts.value.find(item => Number(item.channel_account_id) === Number(form.accountId)) || null)
const channelTypeText = (type: string) => type === 'wechat_miniapp' ? '微信小程序' : type === 'xiaohongshu' ? '小红书小程序' : type
const formatBytes = (value: number) => `${Math.max(1, Math.ceil(Number(value || 0) / 1024))} KB`
const formatDate = (value: string) => value ? new Date(value).toLocaleString('zh-CN') : '-'

const load = async () => {
  loading.value = true
  try {
    const [accountResponse, fileResponse] = await Promise.all([
      request.get('/platform/channel-verification-files/accounts'),
      request.get('/platform/channel-verification-files'),
    ])
    accounts.value = accountResponse.data?.data || []
    rows.value = fileResponse.data?.data || []
  } finally { loading.value = false }
}

const handleAccountChange = () => {
  selectedFile.value = null
  if (fileInput.value) fileInput.value.value = ''
}

const handleFileChange = (event: Event) => {
  const input = event.target as HTMLInputElement
  selectedFile.value = input.files?.[0] || null
}

const upload = async () => {
  if (!selectedAccount.value || !selectedFile.value) return
  const payload = new FormData()
  payload.append('tenant_id', String(selectedAccount.value.tenant_id))
  payload.append('channel_account_id', String(selectedAccount.value.channel_account_id))
  payload.append('kind', selectedAccount.value.channel_type)
  payload.append('file', selectedFile.value)
  saving.value = true
  try {
    await request.post('/platform/channel-verification-files', payload)
    ElMessage.success('校验文件已保存')
    selectedFile.value = null
    if (fileInput.value) fileInput.value.value = ''
    await load()
  } finally { saving.value = false }
}

const remove = async (row: any) => {
  await ElMessageBox.confirm(`确定删除 ${row.filename}？删除后对应平台将无法继续完成域名校验。`, '删除校验文件', { type: 'warning' })
  await request.delete(`/platform/channel-verification-files/${row.id}`)
  ElMessage.success('校验文件已删除')
  await load()
}

onMounted(load)
</script>

<style scoped>
.verification-page { min-height: 100%; }
.page-heading { display: flex; justify-content: space-between; gap: 24px; align-items: flex-start; margin-bottom: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.page-heading h2 { margin: 4px 0 6px; color: #18202b; font-size: 22px; line-height: 30px; }
.page-heading p { margin: 0; color: #667085; font-size: 13px; line-height: 20px; }
.notice { margin-bottom: 16px; }
.upload-panel, .table-panel { padding: 20px; background: #fff; border: 1px solid #e2e7ee; border-radius: 6px; }
.table-panel { margin-top: 16px; }
.panel-title { color: #18202b; font-weight: 650; }
.upload-form { margin-top: 18px; max-width: 720px; }
.account-select { width: 100%; }
.file-input { width: 100%; padding: 9px; border: 1px dashed #c9d2df; border-radius: 6px; background: #fafbfc; }
.field-note { margin-top: 6px; color: #929baa; font-size: 12px; line-height: 18px; }
.form-actions { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.selected-file { color: #475467; font-size: 13px; }
.tenant-cell { display: flex; flex-direction: column; gap: 3px; }
.tenant-cell span { color: #929baa; font-size: 12px; }
@media (max-width: 680px) { .page-heading, .form-actions { flex-direction: column; align-items: stretch; } .upload-panel, .table-panel { padding: 15px; } }
</style>
