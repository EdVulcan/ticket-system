<template>
  <div class="payment-config-page">
    <div class="page-heading">
      <div>
        <h2>支付参数配置</h2>
        <p>分别维护微信与支付宝商户参数，窗口收款时自动识别支付渠道。</p>
      </div>
      <el-tag type="info" effect="plain">普通商户直连</el-tag>
    </div>

    <el-card class="shadow-sm border-0">
      <el-tabs v-model="activeTab" class="demo-tabs">
        <!-- WeChat Pay Config -->
        <el-tab-pane label="微信支付" name="wechat">
          <div class="p-4">
            <el-alert title="请使用【微信支付商户平台】的参数进行配置" type="info" show-icon class="mb-6" />

            <div class="readiness-panel">
              <div class="readiness-head">
                <div>
                  <strong>接入状态</strong>
                  <span>保存完整参数后，系统会逐项检查可用能力</span>
                </div>
                <el-tag :type="readinessType(wechatReadiness)">{{ readinessText(wechatReadiness) }}</el-tag>
              </div>
              <div class="capability-list">
                <div v-for="item in wechatReadiness?.capabilities || []" :key="item.code" class="capability-item" :class="{ available: item.available }">
                  <div class="capability-copy">
                    <span>{{ item.name }}</span>
                    <small>{{ item.note || '配置完成后可用于窗口收款' }}</small>
                  </div>
                  <el-tag size="small" :type="item.available ? 'success' : 'info'">{{ item.available ? '已具备配置' : '暂不可用' }}</el-tag>
                </div>
              </div>
              <el-alert v-if="wechatReadiness?.issues?.length" :title="wechatReadiness.issues.join('；')" type="warning" :closable="false" show-icon />
            </div>
            
            <el-form label-position="top" :model="wechatForm">
              <el-form-item label="启用微信支付">
                <el-switch v-model="wechatForm.status" active-text="启用" inactive-text="停用" />
              </el-form-item>

              <div class="credential-upload-grid">
                <el-form-item label="商户接口证书">
                  <el-upload v-model:file-list="wechatMerchantCertFiles" :auto-upload="false" :limit="1" accept=".pem">
                    <el-button :icon="UploadFilled">选择 apiclient_cert.pem</el-button>
                    <template #tip><div class="upload-tip">自动提取商户号和证书序列号，并校验证书有效期</div></template>
                  </el-upload>
                </el-form-item>
                <el-form-item label="商户接口私钥">
                  <el-upload v-model:file-list="wechatMerchantKeyFiles" :auto-upload="false" :limit="1" accept=".pem">
                    <el-button :icon="UploadFilled">选择 apiclient_key.pem</el-button>
                    <template #tip><div class="upload-tip">仅在保存时上传，后端校验后加密入库</div></template>
                  </el-upload>
                </el-form-item>
              </div>
              <el-alert title="首次配置或更换证书时请同时选择证书和私钥；已经保存过的配置无需重复上传。" type="info" :closable="false" show-icon class="mb-4" />

              <div class="grid grid-cols-2 gap-4">
                <el-form-item label="应用编号">
                  <el-input v-model="wechatForm.app_id" placeholder="wx..." />
                </el-form-item>
                <el-form-item label="商户号">
                  <el-input v-model="wechatForm.mch_id" placeholder="16..." />
                </el-form-item>
              </div>

              <el-form-item label="第三版接口密钥（32位）">
                <el-input v-model="wechatForm.key" type="password" show-password placeholder="在微信支付接口安全设置中配置的32位密钥" />
              </el-form-item>

              <el-form-item label="证书序列号">
                <el-input v-model="wechatForm.serial_no" placeholder="商户接口证书序列号" />
              </el-form-item>

              <el-form-item label="商户接口私钥（PEM格式）">
                <el-input 
                    v-model="wechatForm.private_key" 
                    type="textarea" 
                    :rows="5" 
                    placeholder="-----BEGIN PRIVATE KEY----- ... " 
                    class="font-mono text-xs"
                />
                <div class="text-xs text-gray-400 mt-1">请复制 apiclient_key.pem 文件的全部内容</div>
              </el-form-item>

              <el-form-item label="微信支付平台公钥编号">
                <el-input v-model="wechatForm.platform_public_key_id" placeholder="PUB_KEY_ID_..." />
              </el-form-item>

              <el-form-item label="微信支付平台公钥（PEM格式）">
                <el-upload v-model:file-list="wechatPlatformKeyFiles" :auto-upload="false" :limit="1" accept=".pem" class="mb-2">
                  <el-button :icon="UploadFilled">上传微信支付平台公钥</el-button>
                  <template #tip><div class="upload-tip">也可以继续在下方手工粘贴 PEM 内容</div></template>
                </el-upload>
                <el-input v-model="wechatForm.platform_public_key" type="textarea" :rows="5" placeholder="-----BEGIN PUBLIC KEY----- ..." class="font-mono text-xs" />
              </el-form-item>

              <el-form-item label="支付结果通知地址">
                <el-input v-model="wechatForm.notify_url" placeholder="https://你的域名/api/v1/payments/notify/wechat/租户编号" />
              </el-form-item>

              <el-form-item>
                <el-button type="primary" @click="saveConfig('wechat')">保存微信配置</el-button>
              </el-form-item>
            </el-form>
          </div>
        </el-tab-pane>

        <!-- Alipay Config -->
        <el-tab-pane label="支付宝" name="alipay">
          <div class="p-4">
            <el-alert title="请使用【支付宝开放平台】的参数进行配置" type="warning" show-icon class="mb-6" />

            <div class="readiness-panel">
              <div class="readiness-head">
                <div>
                  <strong>接入状态</strong>
                  <span>保存完整参数后，系统会逐项检查可用能力</span>
                </div>
                <el-tag :type="readinessType(alipayReadiness)">{{ readinessText(alipayReadiness) }}</el-tag>
              </div>
              <div class="capability-list">
                <div v-for="item in alipayReadiness?.capabilities || []" :key="item.code" class="capability-item" :class="{ available: item.available }">
                  <div class="capability-copy">
                    <span>{{ item.name }}</span>
                    <small>{{ item.note || '配置完成后可用于窗口收款' }}</small>
                  </div>
                  <el-tag size="small" :type="item.available ? 'success' : 'info'">{{ item.available ? '已具备配置' : '暂不可用' }}</el-tag>
                </div>
              </div>
              <el-alert v-if="alipayReadiness?.issues?.length" :title="alipayReadiness.issues.join('；')" type="warning" :closable="false" show-icon />
            </div>

            <el-form label-position="top" :model="alipayForm">
              <el-form-item label="启用支付宝">
                <el-switch v-model="alipayForm.status" active-text="启用" inactive-text="停用" />
              </el-form-item>
              <el-form-item label="应用编号">
                <el-input v-model="alipayForm.app_id" placeholder="2021..." />
              </el-form-item>

              <el-form-item label="应用私钥（RSA2格式）">
                <el-input 
                    v-model="alipayForm.private_key" 
                    type="textarea" 
                    :rows="5" 
                    placeholder="MIIEpAIBAAKCAQEAt..." 
                    class="font-mono text-xs"
                />
              </el-form-item>

              <el-form-item label="支付宝公钥">
                 <el-input 
                    v-model="alipayForm.public_key" 
                    type="textarea" 
                    :rows="5" 
                    placeholder="MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA..." 
                    class="font-mono text-xs"
                />
              </el-form-item>

              <el-form-item label="支付结果通知地址">
                <el-input v-model="alipayForm.notify_url" placeholder="https://你的域名/api/v1/payments/notify/alipay/租户编号" />
              </el-form-item>

              <el-form-item>
                <el-button type="primary" @click="saveConfig('alipay')">保存支付宝配置</el-button>
              </el-form-item>
            </el-form>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { ElMessage, type UploadUserFile } from 'element-plus'
import { UploadFilled } from '@element-plus/icons-vue'
import request from '@/utils/request'

const activeTab = ref('wechat')

const wechatForm = ref({ provider: 'wechat', app_id: '', mch_id: '', key: '', serial_no: '', private_key: '', platform_public_key_id: '', platform_public_key: '', notify_url: '', status: false })
const alipayForm = ref({ provider: 'alipay', app_id: '', private_key: '', public_key: '', notify_url: '', status: false })
const wechatMerchantCertFiles = ref<UploadUserFile[]>([])
const wechatMerchantKeyFiles = ref<UploadUserFile[]>([])
const wechatPlatformKeyFiles = ref<UploadUserFile[]>([])
const readiness = ref<any[]>([])
const wechatReadiness = computed(() => readiness.value.find(item => item.provider === 'wechat'))
const alipayReadiness = computed(() => readiness.value.find(item => item.provider === 'alipay'))
const readinessText = (item: any) => item?.configuration_ready ? '配置已就绪' : item?.enabled ? '配置不完整' : '尚未启用'
const readinessType = (item: any) => item?.configuration_ready ? 'success' : item?.enabled ? 'warning' : 'info'

const fetchReadiness = async () => {
    const res = await request.get('/payments/configs/readiness')
    readiness.value = res.data.data || []
}

const fetchConfigs = async () => {
    try {
        const res = await request.get('/payments/configs')
        const configs = res.data.data || []
        
        const wc = configs.find((c: any) => c.provider === 'wechat')
        if (wc) wechatForm.value = { ...wc }

        const ali = configs.find((c: any) => c.provider === 'alipay')
        if (ali) alipayForm.value = { ...ali }
        await fetchReadiness()
    } catch (e) {
        ElMessage.error('加载配置失败')
    }
}

const saveConfig = async (provider: string) => {
    const data = provider === 'wechat' ? wechatForm.value : alipayForm.value
    // Simple validation
    if (!data.app_id) return ElMessage.warning('应用编号不能为空')

    try {
        if (provider === 'wechat') {
            const form = new FormData()
            Object.entries(wechatForm.value).forEach(([key, value]) => form.append(key, String(value ?? '')))
            const merchantCert = wechatMerchantCertFiles.value[0]?.raw
            const merchantKey = wechatMerchantKeyFiles.value[0]?.raw
            const platformKey = wechatPlatformKeyFiles.value[0]?.raw
            if (merchantCert) form.append('merchant_certificate', merchantCert)
            if (merchantKey) form.append('merchant_private_key', merchantKey)
            if (platformKey) form.append('platform_public_key_file', platformKey)
            await request.post('/payments/configs/wechat', form)
            wechatMerchantCertFiles.value = []
            wechatMerchantKeyFiles.value = []
            wechatPlatformKeyFiles.value = []
        } else {
            await request.post('/payments/configs', data)
        }
        ElMessage.success('保存成功')
        await fetchConfigs()
    } catch (e: any) {
        const issues = e.response?.data?.issues
        ElMessage.error(issues?.length ? issues.join('；') : (e.response?.data?.error || '保存失败'))
    }
}

onMounted(() => {
    fetchConfigs()
})
</script>

<style scoped>
.payment-config-page { width: min(1040px, 100%); margin: 0 auto; padding: 20px 24px 32px; }
.page-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; margin-bottom: 18px; }
.page-heading h2 { margin: 0; color: #1f2937; font-size: 24px; line-height: 32px; }
.page-heading p { margin: 5px 0 0; color: #6b7280; font-size: 13px; line-height: 20px; }
.readiness-panel { margin-bottom: 22px; padding: 18px; border: 1px solid #dfe5e2; border-radius: 6px; background: #f8faf9; }
.readiness-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 14px; }
.readiness-head > div { display: flex; flex-direction: column; gap: 3px; }
.readiness-head strong { color: #1f2937; font-size: 16px; line-height: 22px; }
.readiness-head span { color: #7b847e; font-size: 12px; line-height: 18px; }
.capability-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin-bottom: 14px; }
.capability-item { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 12px; min-height: 62px; padding: 10px 12px; border: 1px solid #e4e9e6; border-radius: 5px; background: #fff; }
.capability-item.available { border-color: #cae5d5; background: #f6fbf8; }
.capability-copy { min-width: 0; }
.capability-copy > span { display: block; color: #26312b; font-size: 14px; font-weight: 600; line-height: 20px; }
.capability-copy small { display: block; margin-top: 3px; color: #7b847e; font-size: 12px; line-height: 17px; }
.credential-upload-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; padding: 14px 16px 0; margin-bottom: 16px; border: 1px solid #e2e8e4; border-radius: 6px; background: #fafcfb; }
.upload-tip { margin-top: 5px; color: #7b847e; font-size: 12px; line-height: 18px; }
:deep(.el-card__body) { padding: 18px 20px 24px; }
:deep(.el-tabs__header) { margin-bottom: 18px; }
@media (max-width: 720px) {
  .payment-config-page { padding: 16px 12px 24px; }
  .page-heading { align-items: flex-start; gap: 12px; }
  .page-heading p { max-width: 250px; }
  .capability-list { grid-template-columns: 1fr; }
  .credential-upload-grid { grid-template-columns: 1fr; gap: 0; }
  .readiness-panel { padding: 14px; }
}
</style>
