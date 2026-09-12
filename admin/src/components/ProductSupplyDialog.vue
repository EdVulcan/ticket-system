<template>
  <el-dialog v-model="visible" title="上游供应商配置" width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" destroy-on-close>
    <div v-loading="loading">
      <p class="supply-product">{{ productName }}</p>
      <el-alert title="当前仍由本系统出票" description="这里用于把本景区自有票种映射到上游供应商。保存的是配置草稿，不会改变现有销售、已出票门票或核销方式；完成接口联调和验收后，才会单独开放上游出票。" type="info" :closable="false" show-icon />
      <el-form label-position="top" class="supply-form" @submit.prevent>
        <el-form-item label="上游供应连接">
          <el-select v-model="form.upstream_connection_id" placeholder="选择供应连接" style="width:100%" :disabled="loading || saving">
            <el-option v-for="item in connections" :key="item.id" :value="item.id" :label="item.name" />
          </el-select>
          <div class="field-hint">选择已经建立的供应商接口连接。一个连接可以供多个票种复用。</div>
          <el-button link type="primary" @click="creating = !creating" :disabled="loading || saving">新建供应连接</el-button>
        </el-form-item>
        <div v-if="creating" class="connection-form">
          <el-input v-model="connectionName" placeholder="连接名称，例如：景区合作供应商接口" maxlength="100" />
          <el-button @click="createConnection" :loading="creatingBusy" :disabled="!connectionName.trim()">保存连接</el-button>
        </div>
        <el-form-item label="供应商商品编码">
          <el-input v-model="form.external_product_code" placeholder="填写供应商提供的商品编码" maxlength="200" :disabled="loading || saving" />
          <div class="field-hint">这是供应商系统中对应商品的唯一编码，不是本系统票种 ID，也不是小红书商品编码。</div>
        </el-form-item>
        <el-form-item label="出票方式">
          <el-switch :model-value="false" disabled />
          <span class="supply-hint">暂未开放上游出票</span>
          <div class="field-hint">联调完成后会在这里明确显示启用条件和切换影响；当前保存后仍由本系统出票。</div>
        </el-form-item>
      </el-form>
      <el-alert v-if="loadFailed" title="配置加载失败，请关闭后重试" type="error" :closable="false" />
    </div>
    <template #footer>
      <el-button @click="visible = false" :disabled="saving">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="loading || loadFailed || !form.upstream_connection_id || !form.external_product_code.trim()" @click="save">保存配置草稿</el-button>
    </template>
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
const connectionName = ref('')
const connections = ref<Array<{ id: number; name: string }>>([])
const form = reactive({ upstream_connection_id: 0, external_product_code: '' })
let generation = 0

async function open(product: { id: number; name: string }) {
  const current = ++generation
  productID.value = product.id
  productName.value = product.name
  form.upstream_connection_id = 0
  form.external_product_code = ''
  connections.value = []
  creating.value = false
  connectionName.value = ''
  visible.value = true
  loading.value = true
  loadFailed.value = false
  try {
    const [config, list] = await Promise.all([request.get(`/products/${product.id}/supply`), request.get('/upstream-connections')])
    if (current !== generation) return
    Object.assign(form, { upstream_connection_id: config.data.upstream_connection_id, external_product_code: config.data.external_product_code })
    connections.value = list.data.data || []
  } catch {
    if (current === generation) loadFailed.value = true
  } finally {
    if (current === generation) loading.value = false
  }
}

async function createConnection() {
  if (creatingBusy.value) return
  const current = generation
  creatingBusy.value = true
  try {
    const response = await request.post('/upstream-connections', { name: connectionName.value.trim(), provider: 'zhiyoubao' })
    if (current !== generation) return
    connections.value.push(response.data)
    form.upstream_connection_id = response.data.id
    creating.value = false
    connectionName.value = ''
  } catch { /* The shared request handler shows the error; keep the draft. */ }
  finally { creatingBusy.value = false }
}

async function save() {
  if (saving.value || loading.value || loadFailed.value) return
  const current = generation
  saving.value = true
  try {
    await request.put(`/products/${productID.value}/supply`, { ...form, enabled: false })
    if (current !== generation) return
    ElMessage.success('待联调配置已保存，仍使用本系统出票')
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
