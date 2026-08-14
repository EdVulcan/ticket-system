<template>
  <main class="platform-ai-page">
    <header class="page-heading">
      <div class="page-heading-copy">
        <span class="eyebrow">PLATFORM AI</span>
        <h2>AI 助手配置</h2>
        <p>统一配置供租户使用的模型连接与月度额度。密钥只写入加密存储，不会回显。</p>
      </div>
      <div class="page-actions">
        <el-tag :type="form.enabled && form.api_key_configured ? 'success' : 'info'" effect="plain">
          {{ form.enabled && form.api_key_configured ? '已启用' : '未启用' }}
        </el-tag>
        <el-button :icon="Refresh" circle title="刷新配置" :loading="loading" @click="load" />
      </div>
    </header>

    <section class="config-panel">
      <el-alert
        title="AI 只生成受限的批量票规预览，不直接修改业务数据。租户仍需在预览页确认后才会执行。"
        type="info"
        :closable="false"
        show-icon
      />
      <el-form label-position="top" class="config-form">
        <div class="form-grid two-columns">
          <el-form-item label="提供商">
            <el-select v-model="form.provider" class="w-full">
              <el-option label="DeepSeek" value="deepseek" />
              <el-option label="OpenAI 兼容接口" value="openai_compatible" />
            </el-select>
          </el-form-item>
          <el-form-item label="模型名称">
            <el-input v-model="form.model" placeholder="deepseek-chat" />
          </el-form-item>
        </div>
        <el-form-item label="接口地址">
          <el-input v-model="form.base_url" placeholder="https://api.deepseek.com" />
        </el-form-item>
        <el-form-item label="API Key">
          <el-input v-model="form.api_key" type="password" show-password autocomplete="new-password" placeholder="留空表示保留已配置密钥" />
          <div class="field-note">当前状态：{{ form.api_key_configured ? '已配置' : '未配置' }}。页面不会读取或展示原始密钥。</div>
        </el-form-item>
        <div class="form-grid three-columns">
          <el-form-item label="月请求上限">
            <el-input-number v-model="form.default_monthly_request_limit" :min="1" :max="1000000" class="w-full" />
          </el-form-item>
          <el-form-item label="月 Token 上限">
            <el-input-number v-model="form.default_monthly_token_limit" :min="1000" :max="1000000000" class="w-full" />
          </el-form-item>
          <el-form-item label="单次超时（秒）">
            <el-input-number v-model="form.request_timeout_seconds" :min="5" :max="120" class="w-full" />
          </el-form-item>
        </div>
        <div class="form-grid three-columns">
          <el-form-item label="最大输出 Token">
            <el-input-number v-model="form.max_output_tokens" :min="128" :max="8192" class="w-full" />
          </el-form-item>
          <el-form-item label="温度">
            <el-input-number v-model="form.temperature" :min="0" :max="2" :step="0.1" :precision="1" class="w-full" />
          </el-form-item>
          <el-form-item label="运行状态">
            <el-switch v-model="form.enabled" inline-prompt active-text="启用" inactive-text="停用" />
          </el-form-item>
        </div>
      </el-form>

      <div class="config-footer">
        <span class="version-copy">配置版本 {{ form.config_version || 1 }}</span>
        <div class="footer-actions">
          <el-button :loading="testing" :icon="Connection" @click="testConnection">测试连接</el-button>
          <el-button type="primary" :loading="saving" :icon="Select" @click="save">保存配置</el-button>
        </div>
      </div>
      <el-alert v-if="testResult" :title="testResult" :type="testSuccess ? 'success' : 'error'" :closable="false" class="test-result" />
    </section>
  </main>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Connection, Refresh, Select } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const testResult = ref('')
const testSuccess = ref(false)
const form = reactive<any>({
  provider: 'deepseek', base_url: 'https://api.deepseek.com', model: 'deepseek-chat', api_key: '', api_key_configured: false,
  enabled: false, default_monthly_request_limit: 100, default_monthly_token_limit: 200000,
  request_timeout_seconds: 30, max_output_tokens: 1200, temperature: 0.1, config_version: 1,
})

const load = async () => {
  loading.value = true
  try {
    Object.assign(form, (await request.get('/platform/ai-config')).data || {})
    form.api_key = ''
    testResult.value = ''
  } finally { loading.value = false }
}

const payload = () => ({
  provider: form.provider, base_url: form.base_url.trim(), model: form.model.trim(), api_key: form.api_key,
  enabled: Boolean(form.enabled), default_monthly_request_limit: Number(form.default_monthly_request_limit),
  default_monthly_token_limit: Number(form.default_monthly_token_limit), request_timeout_seconds: Number(form.request_timeout_seconds),
  max_output_tokens: Number(form.max_output_tokens), temperature: Number(form.temperature),
})

const testConnection = async () => {
  testing.value = true
  testResult.value = ''
  try {
    await request.post('/platform/ai-config/test', payload())
    testSuccess.value = true
    testResult.value = '连接测试成功'
  } catch (error: any) {
    testSuccess.value = false
    testResult.value = error.response?.data?.error || '连接测试失败'
  } finally { testing.value = false }
}

const save = async () => {
  saving.value = true
  try {
    Object.assign(form, (await request.put('/platform/ai-config', payload())).data || {})
    form.api_key = ''
    ElMessage.success('AI 配置已保存')
  } finally { saving.value = false }
}

onMounted(load)
</script>

<style scoped>
.platform-ai-page { min-height: 100%; }
.page-heading { display: flex; justify-content: space-between; gap: 24px; align-items: flex-start; margin-bottom: 20px; }
.page-heading-copy h2 { margin: 4px 0 6px; color: #18202b; font-size: 22px; line-height: 30px; }
.page-heading-copy p { margin: 0; color: #667085; font-size: 13px; line-height: 20px; }
.eyebrow { color: #2563eb; font-size: 10px; font-weight: 700; letter-spacing: .08em; }
.page-actions, .footer-actions { display: flex; align-items: center; gap: 10px; }
.config-panel { max-width: 920px; padding: 20px; background: #fff; border: 1px solid #e2e7ee; border-radius: 6px; }
.config-form { margin-top: 22px; }
.form-grid { display: grid; gap: 14px; }
.two-columns { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.three-columns { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.config-form :deep(.el-form-item) { margin-bottom: 16px; }
.field-note, .version-copy { color: #929baa; font-size: 12px; line-height: 18px; }
.config-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 6px; padding-top: 16px; border-top: 1px solid #edf0f4; }
.test-result { margin-top: 16px; }
@media (max-width: 680px) {
  .page-heading, .config-footer { flex-direction: column; align-items: stretch; }
  .two-columns, .three-columns { grid-template-columns: 1fr; gap: 0; }
  .footer-actions { justify-content: flex-end; }
  .config-panel { padding: 15px; }
}
</style>
