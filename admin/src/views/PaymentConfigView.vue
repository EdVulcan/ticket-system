<template>
  <div class="max-w-4xl mx-auto p-6">
    <div class="mb-6 flex justify-between items-center">
      <h2 class="text-2xl font-bold text-gray-800">支付参数配置</h2>
      <el-tag type="info">当前模式：普通商户直连</el-tag>
    </div>

    <el-card class="shadow-sm rounded-xl border-0">
      <el-tabs v-model="activeTab" class="demo-tabs">
        <!-- WeChat Pay Config -->
        <el-tab-pane label="微信支付" name="wechat">
          <div class="p-4">
            <el-alert title="请使用【微信支付商户平台】的参数进行配置" type="info" show-icon class="mb-6" />
            
            <el-form label-position="top" :model="wechatForm">
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

            <el-form label-position="top" :model="alipayForm">
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
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const activeTab = ref('wechat')

const wechatForm = ref({ provider: 'wechat', app_id: '', mch_id: '', key: '', serial_no: '', private_key: '' })
const alipayForm = ref({ provider: 'alipay', app_id: '', private_key: '', public_key: '' })

const fetchConfigs = async () => {
    try {
        const res = await request.get('/payments/configs')
        const configs = res.data.data || []
        
        const wc = configs.find((c: any) => c.provider === 'wechat')
        if (wc) wechatForm.value = { ...wc }

        const ali = configs.find((c: any) => c.provider === 'alipay')
        if (ali) alipayForm.value = { ...ali }
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
    } catch (e) {
        ElMessage.error('保存失败')
    }
}

onMounted(() => {
    fetchConfigs()
})
</script>
