<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between"><div><h2 class="text-xl font-semibold text-gray-900">旅行社团队</h2><p class="text-sm text-gray-500 mt-1">合同、团队计划、名单和供应商履约状态。</p></div><el-button :icon="Refresh" circle title="刷新" @click="loadGroups" /></div>
    <div class="flex gap-2"><el-button type="primary" :icon="Plus" @click="groupDialog = true">新建团队</el-button><el-button @click="activeTab = 'contracts'; loadContracts()">合同管理</el-button><el-button @click="activeTab = 'settlements'; loadSettlements()">团队结算</el-button></div>
    <el-tabs v-model="activeTab">
      <el-tab-pane label="团队计划" name="groups"><el-table :data="groups" v-loading="loading" stripe><el-table-column prop="group_no" label="团号" width="180"/><el-table-column prop="name" label="团队名称"/><el-table-column prop="visit_date" label="游玩日期" width="160"/><el-table-column prop="expected_count" label="计划人数" width="100"/><el-table-column prop="status" label="状态" width="130"/><el-table-column prop="sales_order_id" label="订单" width="90"/></el-table></el-tab-pane>
      <el-tab-pane label="合同" name="contracts"><el-table :data="contracts" stripe><el-table-column prop="contract_no" label="合同号" width="180"/><el-table-column prop="supplier_tenant_id" label="供应商"/><el-table-column prop="credit_limit_cents" label="授信(分)"/><el-table-column prop="status" label="状态"/></el-table></el-tab-pane>
      <el-tab-pane label="结算" name="settlements"><el-table :data="settlements" stripe><el-table-column prop="statement_no" label="结算单"/><el-table-column prop="gross_cents" label="总额(分)"/><el-table-column prop="net_cents" label="应付(分)"/><el-table-column prop="status" label="状态"/></el-table></el-tab-pane>
    </el-tabs>
    <el-dialog v-model="groupDialog" title="新建团队计划" width="520px"><el-form :model="form" label-position="top"><el-form-item label="团队名称"><el-input v-model="form.name" /></el-form-item><el-form-item label="供应商租户 ID"><el-input-number v-model="form.supplier_tenant_id" :min="1" /></el-form-item><el-form-item label="供应商景区 ID"><el-input-number v-model="form.scenic_area_id" :min="1" /></el-form-item><el-form-item label="游玩日期"><el-date-picker v-model="form.visit_date" type="date" value-format="YYYY-MM-DD" /></el-form-item><el-form-item label="计划人数"><el-input-number v-model="form.expected_count" :min="1" /></el-form-item></el-form><template #footer><el-button @click="groupDialog = false">取消</el-button><el-button type="primary" @click="createGroup">创建</el-button></template></el-dialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'
const activeTab = ref('groups'); const groups = ref<any[]>([]); const contracts = ref<any[]>([]); const settlements = ref<any[]>([]); const loading = ref(false); const groupDialog = ref(false)
const form = reactive({ name: '', supplier_tenant_id: 0, scenic_area_id: 0, visit_date: '', expected_count: 1 })
const loadGroups = async () => { loading.value = true; try { groups.value = (await request.get('/teams', { params: { page: 1, page_size: 100 } })).data.data || [] } finally { loading.value = false } }
const loadContracts = async () => { contracts.value = (await request.get('/teams/contracts')).data.data || [] }
const loadSettlements = async () => { settlements.value = (await request.get('/teams/settlements', { params: { page: 1, page_size: 100 } })).data.data || [] }
const createGroup = async () => { await request.post('/teams', { ...form }); groupDialog.value = false; ElMessage.success('团队已创建'); await loadGroups() }
onMounted(loadGroups)
</script>
