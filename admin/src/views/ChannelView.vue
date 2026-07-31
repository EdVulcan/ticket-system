<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">渠道连接</h2>
        <p class="text-sm text-gray-500 mt-1">管理独立渠道凭据、权限、商品映射和账单导入。</p>
      </div>
      <div class="flex gap-2">
        <el-button :icon="Refresh" circle title="刷新" @click="load" />
        <el-button type="primary" :icon="Plus" @click="createDialog = true">新增渠道</el-button>
      </div>
    </div>

    <el-table :data="accounts" v-loading="loading" stripe>
      <el-table-column prop="code" label="渠道编码" width="180" />
      <el-table-column prop="type" label="适配器类型" width="140" />
      <el-table-column prop="status" label="状态" width="120"><template #default="{row}"><el-tag :type="row.status === 'active' ? 'success' : row.status === 'sandbox' ? 'warning' : 'info'">{{ row.status }}</el-tag></template></el-table-column>
      <el-table-column prop="rate_limit_per_min" label="限流/分钟" width="120" />
      <el-table-column prop="permissions_json" label="权限" min-width="220" show-overflow-tooltip />
      <el-table-column label="操作" width="300" fixed="right">
        <template #default="{row}">
          <el-button link type="primary" @click="openMapping(row)">商品映射</el-button>
          <el-button link type="warning" @click="toggleStatus(row)">{{ row.status === 'disabled' ? '启用' : '停用' }}</el-button>
          <el-button link type="danger" @click="rotate(row)">轮换密钥</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialog" title="新增渠道账号" width="520px">
      <el-form :model="form" label-position="top">
        <el-form-item label="渠道编码"><el-input v-model="form.code" placeholder="例如 ctrip-prod" /></el-form-item>
        <el-form-item label="适配器类型"><el-input v-model="form.type" placeholder="core / ctrip / meituan / zyb" /></el-form-item>
        <el-form-item label="初始密钥"><el-input v-model="form.secret" type="password" show-password /></el-form-item>
        <el-form-item label="权限 JSON"><el-input v-model="form.permissions_json" /></el-form-item>
        <el-form-item label="每分钟请求上限"><el-input-number v-model="form.rate_limit_per_min" :min="1" :max="100000" /></el-form-item>
        <el-form-item label="允许 IP JSON"><el-input v-model="form.allowed_ips_json" placeholder='例如 ["203.0.113.5"]' /></el-form-item>
      </el-form>
      <template #footer><el-button @click="createDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="mappingDialog" title="商品映射" width="780px">
      <div class="flex gap-2 mb-4">
        <el-input v-model="mapping.external_code" placeholder="外部商品编码" />
        <el-input-number v-model="mapping.product_id" :min="1" placeholder="本租户商品 ID" />
        <el-button type="primary" @click="addMapping">添加</el-button>
      </div>
      <el-table :data="mappings" stripe><el-table-column prop="external_code" label="外部编码"/><el-table-column prop="product_id" label="本地商品 ID"/><el-table-column prop="status" label="状态"/></el-table>
    </el-dialog>

    <el-dialog v-model="secretDialog" title="新渠道密钥" width="460px"><el-alert type="warning" :closable="false" title="密钥只在本次显示，请立即交给渠道方并安全保存。"/><el-input class="mt-4" :model-value="newSecret" readonly /></el-dialog>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const accounts = ref<any[]>([])
const mappings = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const createDialog = ref(false)
const mappingDialog = ref(false)
const secretDialog = ref(false)
const selectedID = ref(0)
const newSecret = ref('')
const form = reactive({ code: '', type: 'core', secret: '', permissions_json: '["products:read","inventory:reserve","orders:create","orders:query","orders:cancel"]', rate_limit_per_min: 600, allowed_ips_json: '' })
const mapping = reactive({ external_code: '', product_id: 0 })

const load = async () => { loading.value = true; try { accounts.value = (await request.get('/channel-accounts')).data.data || [] } finally { loading.value = false } }
const create = async () => { saving.value = true; try { const response = await request.post('/channel-accounts', { ...form }); accounts.value.unshift(response.data); createDialog.value = false; ElMessage.success('渠道已创建'); form.code = ''; form.secret = '' } finally { saving.value = false } }
const toggleStatus = async (row: any) => { const status = row.status === 'disabled' ? 'active' : 'disabled'; await request.patch(`/channel-accounts/${row.id}/status`, { status }); row.status = status; ElMessage.success('状态已更新') }
const rotate = async (row: any) => { await ElMessageBox.confirm('轮换后旧密钥立即失效，确认继续？', '确认轮换', { type: 'warning' }); const response = await request.post(`/channel-accounts/${row.id}/rotate-secret`); newSecret.value = response.data.secret; secretDialog.value = true }
const openMapping = async (row: any) => { selectedID.value = row.id; mapping.external_code = ''; mapping.product_id = 0; mappings.value = (await request.get('/channel-accounts/mappings', { params: { channel_account_id: row.id } })).data.data || []; mappingDialog.value = true }
const addMapping = async () => { if (!mapping.external_code || !mapping.product_id) return; const response = await request.post('/channel-accounts/mappings', { channel_account_id: selectedID.value, external_code: mapping.external_code, product_id: mapping.product_id }); mappings.value.unshift(response.data); mapping.external_code = ''; mapping.product_id = 0 }
onMounted(load)
</script>
