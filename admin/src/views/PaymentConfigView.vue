<template>
  <div class="max-w-5xl mx-auto p-6">
    <div class="mb-6 flex justify-between items-center">
      <h2 class="text-2xl font-bold text-gray-800">支付参数配置</h2>
      <el-tag type="info">当前模式：普通商户直连</el-tag>
    </div>

    <el-card class="shadow-sm border-0">
      <el-tabs v-model="activeTab" class="demo-tabs">
        <!-- WeChat Pay Config -->
        <el-tab-pane label="微信支付" name="wechat">
          <div class="p-4">
            <el-alert title="请使用【微信支付商户平台】的参数进行配置" type="info" show-icon class="mb-6" />

            <div class="readiness-panel">
              <div class="readiness-head">
                <strong>接入状态</strong>
                <el-tag :type="readinessType(wechatReadiness)">{{ readinessText(wechatReadiness) }}</el-tag>
              </div>
              <div class="capability-list">
                <div v-for="item in wechatReadiness?.capabilities || []" :key="item.code" class="capability-item">
                  <span>{{ item.name }}</span>
                  <el-tag size="small" :type="item.available ? 'success' : 'info'">{{ item.available ? '已具备配置' : '暂不可用' }}</el-tag>
                  <small v-if="item.note">{{ item.note }}</small>
                </div>
              </div>
              <el-alert v-if="wechatReadiness?.issues?.length" :title="wechatReadiness.issues.join('；')" type="warning" :closable="false" show-icon />
            </div>
            
            <el-form label-position="top" :model="wechatForm">
              <el-form-item label="启用微信支付">
                <el-switch v-model="wechatForm.status" active-text="启用" inactive-text="停用" />
              </el-form-item>
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
                <strong>接入状态</strong>
                <el-tag :type="readinessType(alipayReadiness)">{{ readinessText(alipayReadiness) }}</el-tag>
              </div>
              <div class="capability-list">
                <div v-for="item in alipayReadiness?.capabilities || []" :key="item.code" class="capability-item">
                  <span>{{ item.name }}</span>
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
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const activeTab = ref('wechat')

const wechatForm = ref({ provider: 'wechat', app_id: '', mch_id: '', key: '', serial_no: '', private_key: '', platform_public_key_id: '', platform_public_key: '', notify_url: '', status: false })
const alipayForm = ref({ provider: 'alipay', app_id: '', private_key: '', public_key: '', notify_url: '', status: false })
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
        await request.post('/payments/configs', data)
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
.readiness-panel { margin-bottom: 20px; padding: 16px; border: 1px solid #e5e7eb; border-radius: 6px; background: #f8fafc; }
.readiness-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.capability-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px 16px; margin-bottom: 12px; }
.capability-item { display: grid; grid-template-columns: minmax(100px, 1fr) auto; align-items: center; min-height: 28px; gap: 8px; }
.capability-item small { grid-column: 1 / -1; color: #6b7280; }
@media (max-width: 720px) { .capability-list { grid-template-columns: 1fr; } }
</style>
