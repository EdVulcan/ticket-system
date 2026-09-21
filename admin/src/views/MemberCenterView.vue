<template>
  <section class="member-page">
    <header class="page-heading">
      <div>
        <span class="eyebrow">CUSTOMER CENTER</span>
        <h1>客户与会员</h1>
        <p>统一查看自营小程序、App 和官网产生的客户。第三方渠道订单不会自动归入会员。</p>
      </div>
      <el-button :icon="Refresh" :loading="loading" @click="loadMembers">刷新</el-button>
    </header>

    <section class="filter-panel">
      <el-input v-model="filters.keyword" clearable placeholder="搜索姓名或会员编号" @keyup.enter="loadMembers" />
      <el-input v-model="filters.phone" clearable placeholder="验证手机号" @keyup.enter="loadMembers" />
      <el-select v-model="filters.status" clearable placeholder="全部状态" @change="loadMembers">
        <el-option label="正式会员" value="active" />
        <el-option label="待认证客户" value="provisional" />
        <el-option label="冻结" value="frozen" />
        <el-option label="已合并" value="merged" />
        <el-option label="已匿名化" value="anonymized" />
      </el-select>
      <el-button type="primary" :loading="loading" @click="loadMembers">查询</el-button>
    </section>

    <section class="table-panel">
      <el-table v-loading="loading" :data="rows" stripe>
        <el-table-column prop="display_name" label="客户" min-width="180">
          <template #default="{ row }">
            <div class="member-name">{{ row.display_name || '未命名客户' }}</div>
            <small class="muted">会员 #{{ row.id }}</small>
          </template>
        </el-table-column>
        <el-table-column prop="phone" label="验证手机号" width="150" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTag(row)" effect="plain">{{ statusText(row) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="source_count" label="入口身份" width="100" />
        <el-table-column label="最近访问" min-width="180">
          <template #default="{ row }">{{ formatTime(row.last_seen_at || row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row)">查看详情</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && !rows.length" description="暂无自营客户" />
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[20, 50, 100]"
        layout="total, sizes, prev, pager, next"
        @current-change="loadMembers"
        @size-change="handlePageSizeChange"
      />
    </section>

    <el-drawer v-model="detailVisible" title="客户详情" size="min(520px, 100vw)">
      <template v-if="detail">
        <div class="detail-heading">
          <div>
            <h2>{{ detail.display_name || '未命名客户' }}</h2>
            <p>会员 #{{ detail.id }} · {{ statusText(detail) }}</p>
          </div>
          <el-button
            v-if="detail.status === 'active' || detail.status === 'frozen'"
            :type="detail.status === 'frozen' ? 'success' : 'warning'"
            :loading="statusSaving"
            @click="toggleStatus"
          >{{ detail.status === 'frozen' ? '解冻会员' : '冻结会员' }}</el-button>
        </div>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="手机号">{{ detail.phone || '未验证' }}</el-descriptions-item>
          <el-descriptions-item label="会员授权">{{ detail.authorization?.consented ? '已授权' : '未授权' }}</el-descriptions-item>
          <el-descriptions-item label="身份入口">{{ detail.identities?.length || 0 }} 个</el-descriptions-item>
          <el-descriptions-item label="票务订单">{{ detail.orders?.ticket_count || 0 }} 张票</el-descriptions-item>
          <el-descriptions-item label="住宿订单">{{ detail.orders?.hotel_count || 0 }} 笔</el-descriptions-item>
          <el-descriptions-item label="餐饮/电商订单">{{ detail.orders?.commerce_count || 0 }} 笔</el-descriptions-item>
        </el-descriptions>
        <div class="identity-list">
          <div class="section-label">已关联入口</div>
          <el-tag v-for="(identity, index) in detail.identities || []" :key="identity.channel + '-' + identity.status + '-' + index" effect="plain">
            {{ identity.channel }} · {{ identity.status === 'active' ? '正常' : '已撤销' }}
          </el-tag>
          <span v-if="!detail.identities?.length" class="muted">暂无入口身份</span>
        </div>
      </template>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

const loading = ref(false)
const statusSaving = ref(false)
const rows = ref<any[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const detailVisible = ref(false)
const detail = ref<any | null>(null)
const filters = reactive({ keyword: '', phone: '', status: '' })

const statusText = (row: any) => {
  if (row?.status === 'frozen') return '冻结'
  if (row?.status === 'merged') return '已合并'
  if (row?.status === 'anonymized') return '已匿名化'
  return row?.membership_status === 'active' ? '正式会员' : '待认证'
}
const statusTag = (row: any) => ({ frozen: 'warning', merged: 'info', anonymized: 'danger' } as Record<string, any>)[row?.status] || (row?.membership_status === 'active' ? 'success' : 'info')
const formatTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'

async function loadMembers() {
  loading.value = true
  try {
    const response = await request.get('/members', { params: { page: page.value, page_size: pageSize.value, keyword: filters.keyword || undefined, phone: filters.phone || undefined, status: filters.status || undefined }, skipErrorToast: true } as any)
    rows.value = response.data.items || []
    total.value = Number(response.data.total || 0)
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '客户列表加载失败')
  } finally {
    loading.value = false
  }
}

function handlePageSizeChange() {
  page.value = 1
  loadMembers()
}

async function openDetail(row: any) {
  try {
    const response = await request.get('/members/' + row.id, { skipErrorToast: true } as any)
    detail.value = response.data
    detailVisible.value = true
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '客户详情加载失败')
  }
}

async function toggleStatus() {
  if (!detail.value) return
  const nextStatus = detail.value.status === 'frozen' ? 'unfreeze' : 'freeze'
  statusSaving.value = true
  try {
    const response = await request.post('/members/' + detail.value.id + '/' + nextStatus, {}, { skipErrorToast: true } as any)
    detail.value = response.data
    await loadMembers()
    ElMessage.success(nextStatus === 'freeze' ? '会员已冻结' : '会员已解冻')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '会员状态更新失败')
  } finally {
    statusSaving.value = false
  }
}

onMounted(loadMembers)
</script>

<style scoped>
.member-page { display: flex; flex-direction: column; gap: 16px; }
.page-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.page-heading h1 { margin: 4px 0 0; font-size: 26px; }
.page-heading p { margin: 8px 0 0; color: #64748b; }
.eyebrow { color: #0f766e; font-size: 11px; font-weight: 700; letter-spacing: .08em; }
.filter-panel, .table-panel { background: #fff; border: 1px solid #e5e7eb; border-radius: 8px; padding: 16px; }
.filter-panel { display: flex; gap: 10px; flex-wrap: wrap; }
.filter-panel .el-input { width: 220px; }
.filter-panel .el-select { width: 150px; }
.member-name { font-weight: 600; color: #0f172a; }
.muted { color: #94a3b8; font-size: 12px; }
.table-panel .el-pagination { justify-content: flex-end; margin-top: 16px; }
.detail-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 18px; }
.detail-heading h2 { margin: 0; font-size: 20px; }
.detail-heading p { color: #64748b; margin: 6px 0 0; }
.identity-list { margin-top: 20px; display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.section-label { flex-basis: 100%; color: #475569; font-size: 13px; font-weight: 600; }
@media (max-width: 700px) {
  .page-heading { flex-direction: column; }
  .filter-panel { align-items: stretch; flex-direction: column; }
  .filter-panel .el-input, .filter-panel .el-select { width: 100%; }
}
</style>
