<template>
  <el-dialog v-model="visible" title="上游供应商配置" width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" destroy-on-close>
    <div v-loading="loading">
      <p class="supply-product">{{ productName }}</p>
      <el-alert title="只影响之后的新订单" description="外部供应商出票后，新旧设备可识别同一个二维码。两套系统各自核销；这里的设置不会改变已售门票。" type="info" :closable="false" show-icon />
      <el-form label-position="top" class="supply-form" @submit.prevent>
        <el-form-item label="出票方式">
          <el-radio-group v-model="form.enabled" :disabled="loading || saving"><el-radio-button :value="false">本系统出票</el-radio-button><el-radio-button :value="true">外部供应商出票</el-radio-button></el-radio-group>
        </el-form-item>
        <template v-if="form.enabled">
        <el-form-item label="上游供应连接">
          <el-select v-model="form.upstream_connection_id" placeholder="选择供应连接" style="width:100%" :disabled="loading || saving">
            <el-option v-for="item in connections" :key="item.id" :value="item.id" :label="`${item.name}${item.status === 'active' ? '' : '（未启用）'}`" />
          </el-select>
          <div class="field-hint">选择已经建立的供应商接口连接。一个连接可以供多个票种复用。</div>
          <el-button link type="primary" @click="editConnection()" :disabled="loading || saving">新建供应连接</el-button>
          <el-button v-if="form.upstream_connection_id" link type="primary" @click="editConnection(form.upstream_connection_id)" :disabled="loading || saving">管理此共用连接</el-button>
        </el-form-item>
        <el-form-item label="供应商商品编码">
          <el-input v-model="form.external_product_code" placeholder="填写供应商提供的商品编码" maxlength="200" :disabled="loading || saving" />
          <div class="field-hint">这是供应商系统中对应商品的唯一编码，不是本系统票种 ID，也不是小红书商品编码。</div>
        </el-form-item>
        </template>
      </el-form>
      <el-alert v-if="loadFailed" title="配置加载失败，请关闭后重试" type="error" :closable="false" />
    </div>
    <template #footer>
      <el-button @click="visible = false" :disabled="saving">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="loading || loadFailed || (form.enabled && (!form.upstream_connection_id || !form.external_product_code.trim()))" @click="save">保存出票设置</el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="creating" title="智游宝共用连接" width="min(520px, calc(100vw - 24px))" append-to-body :close-on-click-modal="false">
    <p class="field-hint">本商家的多个产品可复用此连接。停用只停止新订单，已售订单仍保留查单与售后处理。</p>
    <el-form label-position="top" @submit.prevent>
      <el-form-item label="连接名称"><el-input v-model="connectionForm.name" maxlength="100" /></el-form-item>
      <el-form-item label="接口地址"><el-input v-model="connectionForm.endpoint" placeholder="智游宝提供的接口地址" /></el-form-item>
      <el-form-item label="企业编码"><el-input v-model="connectionForm.corp_code" /></el-form-item>
      <el-form-item label="接口用户名"><el-input v-model="connectionForm.username" /></el-form-item>
      <el-form-item label="接口密钥"><el-input v-model="connectionForm.private_key" type="password" show-password autocomplete="new-password" :placeholder="connectionID ? '留空保留已保存密钥' : '填写供应商提供的密钥'" /></el-form-item>
      <el-form-item label="环境"><el-select v-model="connectionForm.environment" :disabled="!!connectionID"><el-option label="正式" value="production" /><el-option label="测试" value="sandbox" /></el-select></el-form-item>
      <el-form-item label="允许新订单使用"><el-switch v-model="connectionActive" /></el-form-item>
    </el-form>
    <template #footer><el-button @click="creating = false" :disabled="creatingBusy">取消</el-button><el-button type="primary" @click="createConnection" :loading="creatingBusy" :disabled="!connectionForm.name.trim()">保存共用连接</el-button></template>
  </el-dialog>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const visible = ref(false)
const productName = ref('')
const productID = ref(0)
const loading = ref(false)
const loadFailed = ref(false)
const saving = ref(false)
const creating = ref(false)
const creatingBusy = ref(false)
const connectionID = ref(0)
const connectionActive = ref(false)
const connectionForm = reactive({ name: '', provider: 'zhiyoubao', endpoint: '', corp_code: '', username: '', private_key: '', environment: 'production' })
const connections = ref<Array<any>>([])
const form = reactive({ enabled: false, upstream_connection_id: 0, external_product_code: '' })
let generation = 0

async function open(product: { id: number; name: string }) {
  const current = ++generation
  productID.value = product.id
  productName.value = product.name
  form.upstream_connection_id = 0
  form.enabled = false
  form.external_product_code = ''
  connections.value = []
  creating.value = false
  visible.value = true
  loading.value = true
  loadFailed.value = false
  try {
    const [config, list] = await Promise.all([request.get(`/products/${product.id}/supply`), request.get('/upstream-connections')])
    if (current !== generation) return
    Object.assign(form, { enabled: config.data.enabled, upstream_connection_id: config.data.upstream_connection_id, external_product_code: config.data.external_product_code })
    connections.value = list.data.data || []
  } catch {
    if (current === generation) loadFailed.value = true
  } finally {
    if (current === generation) loading.value = false
  }
}

function editConnection(id = 0) {
  const row = connections.value.find(item => item.id === id)
  connectionID.value = id
  connectionActive.value = row?.status === 'active'
  Object.assign(connectionForm, { name: row?.name || '', provider: 'zhiyoubao', endpoint: row?.endpoint || '', corp_code: row?.corp_code || '', username: row?.username || '', private_key: '', environment: row?.environment || 'production' })
  creating.value = true
}

async function createConnection() {
  if (creatingBusy.value) return
  const current = generation
  creatingBusy.value = true
  try {
    const response = connectionID.value
      ? await request.put(`/upstream-connections/${connectionID.value}`, { ...connectionForm })
      : await request.post('/upstream-connections', { ...connectionForm })
    connectionID.value = response.data.id
    await request.patch(`/upstream-connections/${connectionID.value}/status`, { status: connectionActive.value ? 'active' : 'disabled' })
    const list = await request.get('/upstream-connections')
    if (current !== generation) return
    connections.value = list.data.data || []
    form.upstream_connection_id = response.data.id
    creating.value = false
    connectionForm.private_key = ''
  } catch { /* The shared request handler shows the error; keep the draft. */ }
  finally { creatingBusy.value = false }
}

async function save() {
  if (saving.value || loading.value || loadFailed.value) return
  const current = generation
  saving.value = true
  try {
    await request.put(`/products/${productID.value}/supply`, { ...form })
    if (current !== generation) return
    ElMessage.success(form.enabled ? '已启用外部供应商出票，仅对新订单生效' : '新订单使用本系统出票')
    visible.value = false
  } catch { /* Keep the editable draft when saving fails. */ }
  finally { saving.value = false }
}

defineExpose({ open })
</script>

<style scoped>
.supply-product { margin: 0 0 16px; font-weight: 600; }
.supply-form { margin-top: 20px; }
.connection-form { display: flex; gap: 8px; margin-bottom: 18px; }
.supply-hint { margin-left: 12px; color: #909399; font-size: 13px; }
.field-hint { margin-top: 5px; color: #909399; font-size: 12px; line-height: 1.5; }
</style>
