<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">旅行社团队</h2>
        <p class="text-sm text-gray-500 mt-1">管理合同、团队计划、游客名单和供应商履约状态。</p>
      </div>
      <el-button :icon="Refresh" circle title="刷新" @click="loadGroups" />
    </div>

    <div class="flex gap-2">
      <el-button type="primary" :icon="Plus" @click="openGroupDialog">新建团队</el-button>
      <el-button :icon="DocumentAdd" @click="openContractDialog">新建合同</el-button>
      <el-button @click="activeTab = 'contracts'; loadContracts()">合同管理</el-button>
      <el-button @click="activeTab = 'settlements'; loadSettlements()">团队结算</el-button>
    </div>

    <el-tabs v-model="activeTab" @tab-change="handleTabChange">
      <el-tab-pane label="团队计划" name="groups">
        <el-table :data="groups" v-loading="loading" stripe>
          <el-table-column prop="group_no" label="团号" width="180" />
          <el-table-column prop="name" label="团队名称" min-width="160" />
          <el-table-column prop="visit_date" label="游玩日期" width="140" />
          <el-table-column prop="expected_count" label="计划人数" width="100" />
          <el-table-column prop="status" label="状态" width="120" />
          <el-table-column prop="sales_order_id" label="订单" width="90" />
          <el-table-column label="操作" width="260" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openRoster(row)">名单</el-button>
              <el-button link type="primary" :disabled="row.status !== 'draft'" @click="openAttachOrder(row)">绑定订单</el-button>
              <el-button link type="success" @click="refreshGroup(row)">刷新</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="合同" name="contracts">
        <el-table :data="contracts" v-loading="contractsLoading" stripe>
          <el-table-column prop="contract_no" label="合同号" width="180" />
          <el-table-column prop="supplier_tenant_id" label="供应商" width="100" />
          <el-table-column prop="credit_limit_cents" label="授信(分)" width="110" />
          <el-table-column prop="settlement_days" label="账期(天)" width="100" />
          <el-table-column prop="status" label="状态" width="120" />
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="结算" name="settlements">
        <el-table :data="settlements" v-loading="settlementsLoading" stripe>
          <el-table-column prop="statement_no" label="结算单" min-width="180" />
          <el-table-column prop="gross_cents" label="总额(分)" width="110" />
          <el-table-column prop="refund_cents" label="退款(分)" width="110" />
          <el-table-column prop="net_cents" label="应付(分)" width="110" />
          <el-table-column prop="status" label="状态" width="130" />
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="groupDialog" title="新建团队计划" width="560px">
      <el-form :model="groupForm" label-position="top">
        <el-form-item label="团队名称" required><el-input v-model="groupForm.name" /></el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="供应商租户 ID" required><el-input-number v-model="groupForm.supplier_tenant_id" :min="1" class="w-full" /></el-form-item>
          <el-form-item label="供应商景区 ID" required><el-input-number v-model="groupForm.scenic_area_id" :min="1" class="w-full" /></el-form-item>
          <el-form-item label="合同 ID" required><el-input-number v-model="groupForm.contract_id" :min="1" class="w-full" /></el-form-item>
          <el-form-item label="计划人数" required><el-input-number v-model="groupForm.expected_count" :min="1" class="w-full" /></el-form-item>
        </div>
        <el-form-item label="游玩日期" required><el-date-picker v-model="groupForm.visit_date" type="date" value-format="YYYY-MM-DD" class="w-full" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="groupDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="createGroup">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="contractDialog" title="新建旅行社合同" width="560px">
      <el-form :model="contractForm" label-position="top">
        <el-form-item label="供应商租户 ID" required><el-input-number v-model="contractForm.supplier_tenant_id" :min="1" class="w-full" /></el-form-item>
        <el-form-item label="合同号" required><el-input v-model="contractForm.contract_no" /></el-form-item>
        <div class="grid grid-cols-2 gap-3">
          <el-form-item label="账期(天)"><el-input-number v-model="contractForm.settlement_days" :min="0" class="w-full" /></el-form-item>
          <el-form-item label="授信额度(分)"><el-input-number v-model="contractForm.credit_limit_cents" :min="0" class="w-full" /></el-form-item>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="contractDialog = false">取消</el-button>
        <el-button type="primary" :loading="savingContract" @click="createContract">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="rosterDialog" :title="`团队名单：${selectedGroup?.name || ''}`" width="900px">
      <div class="flex items-center justify-between mb-3">
        <div class="text-sm text-gray-500">共 {{ members.length }} 人，只有草稿且未绑定订单的团队可以替换名单。</div>
        <el-button :icon="Refresh" circle title="刷新名单" @click="loadMembers" />
      </div>
      <el-table :data="members" height="260" stripe>
        <el-table-column type="index" width="55" />
        <el-table-column prop="name" label="姓名" min-width="140" />
        <el-table-column prop="identity_no" label="证件号" min-width="180" />
        <el-table-column prop="phone" label="手机号" width="140" />
        <el-table-column prop="ticket_code" label="票码" min-width="180" />
        <el-table-column prop="status" label="状态" width="100" />
      </el-table>
      <el-divider />
      <el-form label-position="top">
        <el-form-item label="名单导入（每行：姓名,证件号,手机号；支持 CSV/Tab）">
          <el-input v-model="rosterText" type="textarea" :rows="6" placeholder="张三,110101...,13800000000" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="rosterDialog = false">关闭</el-button>
        <el-button type="primary" :loading="savingRoster" :disabled="!canReplaceRoster" @click="replaceRoster">替换名单</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="attachOrderDialog" title="绑定已支付团队订单" width="440px">
      <el-form label-position="top">
        <el-form-item label="订单 ID" required><el-input-number v-model="attachOrderId" :min="1" class="w-full" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="attachOrderDialog = false">取消</el-button>
        <el-button type="primary" :loading="attachingOrder" @click="attachOrder">绑定</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { DocumentAdd, Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const activeTab = ref('groups')
const groups = ref<any[]>([])
const contracts = ref<any[]>([])
const settlements = ref<any[]>([])
const loading = ref(false)
const contractsLoading = ref(false)
const settlementsLoading = ref(false)
const saving = ref(false)
const savingContract = ref(false)
const groupDialog = ref(false)
const contractDialog = ref(false)
const groupForm = reactive({ name: '', supplier_tenant_id: 0, scenic_area_id: 0, contract_id: 0, visit_date: '', expected_count: 1 })
const contractForm = reactive({ supplier_tenant_id: 0, contract_no: '', settlement_days: 0, credit_limit_cents: 0, status: 'active' })

const rosterDialog = ref(false)
const selectedGroup = ref<any>(null)
const members = ref<any[]>([])
const rosterText = ref('')
const savingRoster = ref(false)
const attachOrderDialog = ref(false)
const attachOrderId = ref(0)
const attachingOrder = ref(false)
const canReplaceRoster = computed(() => selectedGroup.value?.status === 'draft' && !selectedGroup.value?.sales_order_id && rosterText.value.trim().length > 0)

const loadGroups = async () => {
  loading.value = true
  try {
    groups.value = (await request.get('/teams', { params: { page: 1, page_size: 100 } })).data.data || []
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '团队加载失败')
  } finally {
    loading.value = false
  }
}

const loadContracts = async () => {
  contractsLoading.value = true
  try { contracts.value = (await request.get('/teams/contracts')).data.data || [] } finally { contractsLoading.value = false }
}

const loadSettlements = async () => {
  settlementsLoading.value = true
  try { settlements.value = (await request.get('/teams/settlements', { params: { page: 1, page_size: 100 } })).data.data || [] } finally { settlementsLoading.value = false }
}

const handleTabChange = (tab: string) => {
  if (tab === 'contracts') loadContracts()
  if (tab === 'settlements') loadSettlements()
}

const openGroupDialog = () => {
  Object.assign(groupForm, { name: '', supplier_tenant_id: 0, scenic_area_id: 0, contract_id: 0, visit_date: '', expected_count: 1 })
  groupDialog.value = true
}

const createGroup = async () => {
  if (!groupForm.name.trim() || !groupForm.supplier_tenant_id || !groupForm.scenic_area_id || !groupForm.contract_id || !groupForm.visit_date) {
    ElMessage.warning('团队名称、供应商、景区、合同和日期均必填')
    return
  }
  saving.value = true
  try {
    await request.post('/teams', { ...groupForm })
    groupDialog.value = false
    ElMessage.success('团队已创建')
    await loadGroups()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '团队创建失败')
  } finally { saving.value = false }
}

const openContractDialog = () => {
  Object.assign(contractForm, { supplier_tenant_id: 0, contract_no: '', settlement_days: 0, credit_limit_cents: 0, status: 'active' })
  contractDialog.value = true
}

const createContract = async () => {
  if (!contractForm.supplier_tenant_id || !contractForm.contract_no.trim()) {
    ElMessage.warning('供应商和合同号必填')
    return
  }
  savingContract.value = true
  try {
    await request.post('/teams/contracts', { ...contractForm })
    contractDialog.value = false
    ElMessage.success('合同已创建')
    await loadContracts()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '合同创建失败')
  } finally { savingContract.value = false }
}

const openRoster = async (row: any) => {
  selectedGroup.value = row
  rosterText.value = ''
  rosterDialog.value = true
  await loadMembers()
}

const loadMembers = async () => {
  if (!selectedGroup.value) return
  try { members.value = (await request.get(`/teams/${selectedGroup.value.id}/members`)).data.data || [] } catch (e: any) { ElMessage.error(e.response?.data?.error || '名单加载失败') }
}

const parseRoster = () => {
  const rows: any[] = []
  const lines = rosterText.value.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
  for (const [index, line] of lines.entries()) {
    const cells = line.split(/[\t,，]/).map(cell => cell.trim())
    if (index === 0 && /姓名|name/i.test(cells[0] || '')) continue
    if (!cells[0]) throw new Error(`第 ${index + 1} 行姓名为空`)
    rows.push({ name: cells[0], identity_no: cells[1] || '', phone: cells[2] || '' })
  }
  if (!rows.length) throw new Error('名单不能为空')
  return rows
}

const replaceRoster = async () => {
  if (!selectedGroup.value) return
  try {
    const roster = parseRoster()
    savingRoster.value = true
    await request.put(`/teams/${selectedGroup.value.id}/members`, { members: roster })
    ElMessage.success(`已替换 ${roster.length} 名成员`)
    await loadMembers()
    await loadGroups()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || e.message || '名单替换失败')
  } finally { savingRoster.value = false }
}

const openAttachOrder = (row: any) => {
  selectedGroup.value = row
  attachOrderId.value = 0
  attachOrderDialog.value = true
}

const attachOrder = async () => {
  if (!selectedGroup.value || !attachOrderId.value) {
    ElMessage.warning('请输入订单 ID')
    return
  }
  attachingOrder.value = true
  try {
    await request.post(`/teams/${selectedGroup.value.id}/attach-order`, { order_id: attachOrderId.value })
    attachOrderDialog.value = false
    ElMessage.success('订单已绑定，成员票权益已匹配')
    await loadGroups()
    if (rosterDialog.value) await loadMembers()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '订单绑定失败')
  } finally { attachingOrder.value = false }
}

const refreshGroup = async (row: any) => {
  await loadGroups()
  if (selectedGroup.value?.id === row.id && rosterDialog.value) await loadMembers()
}

onMounted(loadGroups)
</script>
