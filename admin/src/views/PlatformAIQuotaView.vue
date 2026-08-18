<template>
  <main class="quota-page">
    <header class="page-heading">
      <div>
        <span class="eyebrow">PLATFORM AI</span>
        <h2>AI 租户额度</h2>
        <p>按租户控制平台 AI 的月度请求与 Token 预算。没有单独策略的租户自动继承平台默认值。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新" :loading="loading" @click="load" />
    </header>

    <section class="toolbar">
      <el-date-picker v-model="period" type="month" value-format="YYYY-MM" placeholder="选择账期" clearable @change="load" />
      <el-input v-model="search" clearable placeholder="搜索租户名称或企业码" class="search-input" @keyup.enter="load" @clear="load">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <el-button type="primary" @click="load">查询</el-button>
    </section>

    <el-alert
      title="额度调整只影响后续请求；历史 AI 用量账本不会被清零或重述。暂停后，租户的新请求会 fail-closed。"
      type="info"
      :closable="false"
      show-icon
      class="quota-note"
    />

    <section class="table-panel">
      <el-table :data="rows" v-loading="loading" stripe>
        <el-table-column label="租户" min-width="220">
          <template #default="{ row }">
            <div class="tenant-cell"><strong>{{ row.tenant_name }}</strong><span>{{ row.system_code }}</span></div>
          </template>
        </el-table-column>
        <el-table-column label="租户状态" width="110">
          <template #default="{ row }"><el-tag :type="tenantStatusType(row.tenant_status)" effect="plain">{{ tenantStatusText(row.tenant_status) }}</el-tag></template>
        </el-table-column>
        <el-table-column label="AI 状态" width="110">
          <template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'warning'" effect="plain">{{ row.enabled ? '可用' : '已暂停' }}</el-tag></template>
        </el-table-column>
        <el-table-column label="本月用量" min-width="180">
          <template #default="{ row }">
            <div class="usage-cell"><span>{{ row.request_count }} / {{ row.monthly_request_limit }} 次</span><span>{{ formatNumber(row.token_count) }} / {{ formatNumber(row.monthly_token_limit) }} Token</span></div>
          </template>
        </el-table-column>
        <el-table-column label="剩余" min-width="160">
          <template #default="{ row }"><span>{{ row.requests_remaining }} 次 · {{ formatNumber(row.tokens_remaining) }} Token</span></template>
        </el-table-column>
        <el-table-column label="额度来源" width="150">
          <template #default="{ row }">
            <div>{{ row.request_limit_inherited && row.token_limit_inherited ? '全部继承平台默认' : '包含租户覆盖' }}</div>
            <small v-if="row.policy_configured">{{ row.last_updated_reason || '已配置租户策略' }}</small>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right" align="center">
          <template #default="{ row }"><el-button link type="primary" :icon="Edit" @click="openEdit(row)">调整</el-button></template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && rows.length === 0" description="暂无租户" />
      <div class="pagination-row">
        <span>账期：{{ period || '本月' }}</span>
        <el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="total" layout="total, prev, pager, next" @current-change="load" />
      </div>
    </section>

    <el-dialog v-model="dialogVisible" title="调整租户 AI 额度" width="520px" destroy-on-close>
      <div v-if="editingRow" class="dialog-context">
        <strong>{{ editingRow.tenant_name }}</strong><span>{{ editingRow.system_code }} · {{ editingRow.period }} 已使用 {{ editingRow.request_count }} 次 / {{ formatNumber(editingRow.token_count) }} Token</span>
      </div>
      <el-form label-position="top">
        <el-form-item label="AI 状态">
          <el-switch v-model="form.enabled" active-text="允许使用" inactive-text="暂停使用" />
        </el-form-item>
        <div class="limit-grid">
          <el-form-item label="月请求上限">
            <el-checkbox v-model="form.inheritRequest">继承平台默认</el-checkbox>
            <el-input-number v-model="form.monthlyRequestLimit" :min="1" :max="1000000" :disabled="form.inheritRequest" class="full-width" />
          </el-form-item>
          <el-form-item label="月 Token 上限">
            <el-checkbox v-model="form.inheritToken">继承平台默认</el-checkbox>
            <el-input-number v-model="form.monthlyTokenLimit" :min="1000" :max="1000000000" :disabled="form.inheritToken" class="full-width" />
          </el-form-item>
        </div>
        <el-form-item label="调整原因" required>
          <el-input v-model="form.reason" type="textarea" :rows="3" maxlength="255" show-word-limit placeholder="说明本次额度或状态变更原因" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存策略</el-button>
      </template>
    </el-dialog>
  </main>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Edit, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const now = new Date()
const currentPeriod = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
const loading = ref(false)
const saving = ref(false)
const rows = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const period = ref(currentPeriod)
const search = ref('')
const dialogVisible = ref(false)
const editingRow = ref<any>(null)
const form = reactive({
  enabled: true,
  inheritRequest: true,
  inheritToken: true,
  monthlyRequestLimit: 100,
  monthlyTokenLimit: 200000,
  reason: '',
})

const formatNumber = (value: number) => new Intl.NumberFormat('zh-CN').format(Number(value || 0))
const tenantStatusText = (status: string) => ({ active: '正常', frozen: '已冻结', closed: '已关闭' } as Record<string, string>)[status] || status || '未知'
const tenantStatusType = (status: string) => status === 'active' ? 'success' : status === 'frozen' ? 'warning' : 'info'

const load = async () => {
  loading.value = true
  try {
    const response = await request.get('/platform/ai-quotas', { params: { period: period.value, search: search.value.trim(), page: page.value, page_size: pageSize.value } })
    const data = response.data || {}
    rows.value = data.data || []
    total.value = Number(data.total || 0)
  } finally {
    loading.value = false
  }
}

const openEdit = (row: any) => {
  editingRow.value = row
  Object.assign(form, {
    enabled: Boolean(row.enabled),
    inheritRequest: Boolean(row.request_limit_inherited),
    inheritToken: Boolean(row.token_limit_inherited),
    monthlyRequestLimit: Number(row.monthly_request_limit || 100),
    monthlyTokenLimit: Number(row.monthly_token_limit || 200000),
    reason: '',
  })
  dialogVisible.value = true
}

const save = async () => {
  if (!editingRow.value) return
  if (!form.reason.trim()) {
    ElMessage.warning('请填写调整原因')
    return
  }
  saving.value = true
  try {
    await request.put(`/platform/ai-quotas/${editingRow.value.tenant_id}`, {
      enabled: Boolean(form.enabled),
      monthly_request_limit: form.inheritRequest ? null : Number(form.monthlyRequestLimit),
      monthly_token_limit: form.inheritToken ? null : Number(form.monthlyTokenLimit),
      reason: form.reason.trim(),
    })
    ElMessage.success('租户 AI 策略已更新')
    dialogVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.quota-page { min-height: 100%; }
.page-heading { display: flex; justify-content: space-between; gap: 24px; align-items: flex-start; margin-bottom: 20px; }
.page-heading h2 { margin: 4px 0 6px; color: #18202b; font-size: 22px; line-height: 30px; }
.page-heading p { margin: 0; color: #667085; font-size: 13px; line-height: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.toolbar { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.search-input { width: 260px; }
.quota-note { margin-bottom: 14px; }
.table-panel { padding: 16px; background: #fff; border: 1px solid #e2e7ee; border-radius: 6px; }
.tenant-cell, .usage-cell { display: flex; flex-direction: column; gap: 3px; }
.tenant-cell span, .usage-cell span, .pagination-row, .dialog-context span, .quota-page small { color: #7b8492; font-size: 12px; }
.pagination-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-top: 16px; }
.dialog-context { display: flex; flex-direction: column; gap: 4px; padding: 12px; margin-bottom: 18px; background: #f6f8fb; border: 1px solid #e6eaf0; border-radius: 5px; }
.limit-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.full-width { width: 100%; margin-top: 8px; }
@media (max-width: 720px) {
  .page-heading, .pagination-row { flex-direction: column; align-items: stretch; }
  .search-input { width: 100%; }
  .limit-grid { grid-template-columns: 1fr; gap: 0; }
  .table-panel { padding: 10px; overflow-x: auto; }
}
</style>
