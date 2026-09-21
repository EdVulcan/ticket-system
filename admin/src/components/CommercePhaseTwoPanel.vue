<template>
  <section class="phase-two-panel">
    <el-alert
      v-if="errorMessage"
      type="error"
      :title="errorMessage"
      :closable="false"
      class="panel-alert"
    />

    <el-tabs v-model="panelTab" class="phase-two-tabs">
      <el-tab-pane label="履约配置" name="fulfillment">
        <div class="panel-heading">
          <div>
            <h3>{{ businessType === 'restaurant' ? '门店服务配置' : '仓库发货配置' }}</h3>
            <p class="muted">配置只作用于当前业务，不会修改景区票务或另一种商业业务。</p>
          </div>
          <el-button :icon="Refresh" :loading="loading" @click="loadFulfillment">刷新</el-button>
        </div>

        <el-form label-position="top" class="phase-form">
          <el-form-item :label="businessType === 'restaurant' ? '履约地点' : '发货地点'" required>
            <el-select v-model="selectedLocationID" class="full-width" filterable>
              <el-option v-for="location in activeLocations" :key="location.id" :label="location.name" :value="location.id" />
            </el-select>
          </el-form-item>

          <div class="phase-grid">
            <el-form-item v-if="businessType === 'restaurant'" label="支持自取">
              <el-switch v-model="config.pickup_enabled" :disabled="!canWrite" />
            </el-form-item>
            <el-form-item v-if="businessType === 'restaurant'" label="支持配送">
              <el-switch v-model="config.delivery_enabled" :disabled="!canWrite" />
            </el-form-item>
            <el-form-item v-if="businessType === 'retail'" label="支持快递发货">
              <el-switch v-model="config.shipping_enabled" :disabled="!canWrite" />
            </el-form-item>
            <el-form-item :label="businessType === 'restaurant' ? '起送金额（元）' : '包邮门槛（元）'">
              <el-input-number v-model="configThresholdYuan" class="full-width" :min="0" :precision="2" :controls="false" :disabled="!canWrite" />
            </el-form-item>
            <el-form-item v-if="businessType === 'restaurant'" label="包装费（元）">
              <el-input-number v-model="configPackagingYuan" class="full-width" :min="0" :precision="2" :controls="false" :disabled="!canWrite" />
            </el-form-item>
            <el-form-item v-if="businessType === 'retail'" label="默认运费（元）">
              <el-input-number v-model="configShippingYuan" class="full-width" :min="0" :precision="2" :controls="false" :disabled="!canWrite" />
            </el-form-item>
            <el-form-item label="预计处理时长（分钟）">
              <el-input-number v-model="config.estimated_minutes" class="full-width" :min="0" :max="10080" :precision="0" :controls="false" :disabled="!canWrite" />
            </el-form-item>
          </div>
          <div class="phase-grid">
            <el-form-item label="联系人"><el-input v-model="config.contact_name" maxlength="120" :disabled="!canWrite" /></el-form-item>
            <el-form-item label="联系电话"><el-input v-model="config.contact_phone" maxlength="40" :disabled="!canWrite" /></el-form-item>
          </div>
          <el-form-item :label="businessType === 'restaurant' ? '门店地址' : '发货地址'"><el-input v-model="config.address" maxlength="500" :disabled="!canWrite" /></el-form-item>
          <el-form-item label="配置状态"><el-select v-model="config.status" :disabled="!canWrite"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
          <div class="form-actions"><el-button v-if="canWrite" type="primary" :loading="saving" @click="saveConfig">保存服务配置</el-button></div>
        </el-form>

        <div v-if="businessType === 'restaurant'" class="subsection">
          <div class="subsection-heading"><div><h4>配送区域</h4><p class="muted">按行政区维护运费和预计时长，不自动推断地图范围。</p></div><el-button v-if="canWrite" type="primary" plain :icon="Plus" @click="openZoneDialog()">新增区域</el-button></div>
          <el-table :data="zones" border stripe size="small" class="phase-table">
            <el-table-column label="区域" min-width="180"><template #default="{ row }">{{ row.name }}<div class="secondary-cell">{{ row.province }} {{ row.city }} {{ row.district }}</div></template></el-table-column>
            <el-table-column label="运费" width="100" align="right"><template #default="{ row }">¥{{ yuan(row.fee_cents) }}</template></el-table-column>
            <el-table-column label="预计" width="100" align="right"><template #default="{ row }">{{ row.estimated_minutes || '-' }} 分钟</template></el-table-column>
            <el-table-column label="状态" width="90" align="center"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag></template></el-table-column>
            <el-table-column label="操作" width="100" fixed="right" align="right"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openZoneDialog(row)">编辑</el-button></template></el-table-column>
            <template #empty><el-empty description="暂未配置配送区域" :image-size="60" /></template>
          </el-table>

          <div class="subsection-heading slots-heading"><div><h4>配送时段</h4><p class="muted">按每周重复时段设置容量和下单截止时间。</p></div><el-button v-if="canWrite" plain type="primary" :icon="Plus" @click="openSlotDialog()">新增时段</el-button></div>
          <el-table :data="slots" border stripe size="small" class="phase-table">
            <el-table-column label="星期" width="90"><template #default="{ row }">{{ weekdayLabel(row.day_of_week) }}</template></el-table-column>
            <el-table-column label="时间" min-width="140"><template #default="{ row }">{{ minuteLabel(row.start_minute) }} - {{ minuteLabel(row.end_minute) }}</template></el-table-column>
            <el-table-column label="容量" width="90" align="right"><template #default="{ row }">{{ row.capacity || '不限' }}</template></el-table-column>
            <el-table-column label="状态" width="90" align="center"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag></template></el-table-column>
            <el-table-column label="操作" width="100" fixed="right" align="right"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openSlotDialog(row)">编辑</el-button></template></el-table-column>
            <template #empty><el-empty description="暂未配置配送时段" :image-size="60" /></template>
          </el-table>
        </div>
      </el-tab-pane>

      <el-tab-pane label="营销活动" name="promotions">
        <div class="panel-heading"><div><h3>优惠券模板</h3><p class="muted">优惠券和分享助力均绑定当前业务及真实微信小程序渠道账号。</p></div><div class="heading-actions"><el-select v-model="promotionChannelID" :disabled="promotionLoading" filterable placeholder="选择微信渠道" @change="loadPromotions"><el-option v-for="channel in channels" :key="channel.id" :label="`${channel.code} · ${channel.app_id || '未填写 AppID'}`" :value="channel.id" /></el-select><el-button :icon="Refresh" :loading="promotionLoading" @click="loadPromotions">刷新</el-button></div></div>
        <div class="subsection-heading"><div><strong>优惠券模板</strong><span class="muted"> {{ couponTemplates.length }} 个</span></div><el-button v-if="canWrite" type="primary" plain :icon="Plus" @click="openCouponDialog()">新增模板</el-button></div>
        <el-table :data="couponTemplates" border stripe size="small" class="phase-table">
          <el-table-column prop="name" label="名称" min-width="180" />
          <el-table-column label="优惠" width="110" align="right"><template #default="{ row }">减 ¥{{ yuan(row.discount_cents) }}</template></el-table-column>
          <el-table-column label="门槛" width="110" align="right"><template #default="{ row }">¥{{ yuan(row.min_goods_subtotal_cents) }}</template></el-table-column>
          <el-table-column label="状态" width="90" align="center"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ row.status === 'active' ? '启用' : row.status === 'draft' ? '草稿' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="100" fixed="right" align="right"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openCouponDialog(row)">编辑</el-button></template></el-table-column>
          <template #empty><el-empty description="暂无优惠券模板" :image-size="60" /></template>
        </el-table>

        <div class="subsection-heading campaign-heading"><div><strong>分享助力活动</strong><span class="muted"> {{ campaigns.length }} 个</span></div><el-button v-if="canWrite" type="primary" plain :icon="Plus" :disabled="couponTemplates.length === 0" @click="openCampaignDialog()">新增活动</el-button></div>
        <el-table :data="campaigns" border stripe size="small" class="phase-table">
          <el-table-column prop="title" label="活动名称" min-width="200" />
          <el-table-column label="所需助力" width="100" align="right"><template #default="{ row }">{{ row.required_unique_helpers }}</template></el-table-column>
          <el-table-column label="状态" width="90" align="center"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ row.status === 'active' ? '启用' : row.status === 'draft' ? '草稿' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="100" fixed="right" align="right"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openCampaignDialog(row)">编辑</el-button></template></el-table-column>
          <template #empty><el-empty description="暂无分享助力活动" :image-size="60" /></template>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="zoneDialogVisible" :title="zoneForm.id ? '编辑配送区域' : '新增配送区域'" width="min(560px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="zoneForm" label-position="top" class="phase-form"><el-form-item label="区域名称" required><el-input v-model="zoneForm.name" maxlength="120" /></el-form-item><div class="phase-grid"><el-form-item label="省/自治区" required><el-input v-model="zoneForm.province" maxlength="80" /></el-form-item><el-form-item label="城市" required><el-input v-model="zoneForm.city" maxlength="80" /></el-form-item><el-form-item label="区/县" required><el-input v-model="zoneForm.district" maxlength="80" /></el-form-item><el-form-item label="运费（元）"><el-input-number v-model="zoneForm.fee_yuan" class="full-width" :min="0" :precision="2" :controls="false" /></el-form-item><el-form-item label="预计时长（分钟）"><el-input-number v-model="zoneForm.estimated_minutes" class="full-width" :min="0" :max="10080" :precision="0" :controls="false" /></el-form-item><el-form-item label="状态"><el-select v-model="zoneForm.status" class="full-width"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item></div></el-form>
      <template #footer><el-button @click="zoneDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveZone">保存区域</el-button></template>
    </el-dialog>

    <el-dialog v-model="slotDialogVisible" :title="slotForm.id ? '编辑配送时段' : '新增配送时段'" width="min(520px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="slotForm" label-position="top" class="phase-form"><div class="phase-grid"><el-form-item label="星期" required><el-select v-model="slotForm.day_of_week" class="full-width"><el-option v-for="(label, index) in weekdays" :key="index" :label="label" :value="index" /></el-select></el-form-item><el-form-item label="容量（0 为不限）"><el-input-number v-model="slotForm.capacity" class="full-width" :min="0" :controls="false" /></el-form-item><el-form-item label="开始时间" required><el-time-select v-model="slotForm.start_time" start="00:00" step="00:30" end="23:30" class="full-width" /></el-form-item><el-form-item label="结束时间" required><el-time-select v-model="slotForm.end_time" start="00:30" step="00:30" end="24:00" class="full-width" /></el-form-item><el-form-item label="提前截止（分钟）"><el-input-number v-model="slotForm.order_cutoff_minutes" class="full-width" :min="0" :max="1440" :controls="false" /></el-form-item><el-form-item label="状态"><el-select v-model="slotForm.status" class="full-width"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item></div></el-form>
      <template #footer><el-button @click="slotDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveSlot">保存时段</el-button></template>
    </el-dialog>

    <el-dialog v-model="couponDialogVisible" :title="couponForm.id ? '编辑优惠券模板' : '新增优惠券模板'" width="min(620px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="couponForm" label-position="top" class="phase-form"><el-form-item label="微信小程序渠道" required><el-select v-model="couponForm.channel_account_id" class="full-width" filterable :disabled="Boolean(couponForm.id)"><el-option v-for="channel in channels" :key="channel.id" :label="`${channel.code} · ${channel.app_id || '未填写 AppID'}`" :value="channel.id" /></el-select></el-form-item><el-form-item label="模板名称" required><el-input v-model="couponForm.name" maxlength="120" /></el-form-item><div class="phase-grid"><el-form-item label="优惠金额（元）" required><el-input-number v-model="couponForm.discount_yuan" class="full-width" :min="0.01" :precision="2" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item><el-form-item label="使用门槛（元）"><el-input-number v-model="couponForm.min_yuan" class="full-width" :min="0" :precision="2" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item><el-form-item label="有效天数"><el-input-number v-model="couponForm.valid_days" class="full-width" :min="0" :max="3650" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item><el-form-item label="开始时间"><el-date-picker v-model="couponForm.starts_at" type="datetime" class="full-width" /></el-form-item><el-form-item label="结束时间"><el-date-picker v-model="couponForm.ends_at" type="datetime" class="full-width" /></el-form-item><el-form-item label="发放上限（0 为不限）"><el-input-number v-model="couponForm.issuance_cap" class="full-width" :min="0" :controls="false" /></el-form-item><el-form-item label="每人上限"><el-input-number v-model="couponForm.per_customer_cap" class="full-width" :min="1" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item><el-form-item label="状态"><el-select v-model="couponForm.status" class="full-width"><el-option label="草稿" value="draft" /><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item></div></el-form>
      <template #footer><el-button @click="couponDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveCoupon">保存模板</el-button></template>
    </el-dialog>

    <el-dialog v-model="campaignDialogVisible" :title="campaignForm.id ? '编辑分享助力活动' : '新增分享助力活动'" width="min(620px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="campaignForm" label-position="top" class="phase-form"><el-form-item label="活动名称" required><el-input v-model="campaignForm.title" maxlength="120" /></el-form-item><div class="phase-grid"><el-form-item label="发起人奖励" required><el-select v-model="campaignForm.starter_coupon_template_id" class="full-width" :disabled="Boolean(campaignForm.id)"><el-option v-for="coupon in couponTemplates" :key="coupon.id" :label="coupon.name" :value="coupon.id" /></el-select></el-form-item><el-form-item label="助力人奖励" required><el-select v-model="campaignForm.helper_coupon_template_id" class="full-width" :disabled="Boolean(campaignForm.id)"><el-option v-for="coupon in couponTemplates" :key="coupon.id" :label="coupon.name" :value="coupon.id" /></el-select></el-form-item><el-form-item label="所需独立助力人数"><el-input-number v-model="campaignForm.required_unique_helpers" class="full-width" :min="1" :controls="false" /></el-form-item><el-form-item label="每人可发起次数"><el-input-number v-model="campaignForm.per_starter_session_limit" class="full-width" :min="1" :controls="false" /></el-form-item><el-form-item label="开始时间" required><el-date-picker v-model="campaignForm.starts_at" type="datetime" class="full-width" /></el-form-item><el-form-item label="结束时间" required><el-date-picker v-model="campaignForm.ends_at" type="datetime" class="full-width" /></el-form-item><el-form-item label="状态"><el-select v-model="campaignForm.status" class="full-width"><el-option label="草稿" value="draft" /><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item></div></el-form>
      <template #footer><el-button @click="campaignDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveCampaign">保存活动</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

type BusinessType = 'restaurant' | 'retail'
const props = defineProps<{ businessType: BusinessType; activeLocations: any[]; canWrite: boolean }>()

const panelTab = ref('fulfillment')
const loading = ref(false)
const promotionLoading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const fulfillmentRequestID = ref(0)
const promotionRequestID = ref(0)
const selectedLocationID = ref<number>(Number(props.activeLocations[0]?.id || 0))
const zones = ref<any[]>([])
const slots = ref<any[]>([])
const channels = ref<any[]>([])
const promotionChannelID = ref<number>(0)
const couponTemplates = ref<any[]>([])
const campaigns = ref<any[]>([])
const defaultConfig = () => ({ pickup_enabled: false, delivery_enabled: false, shipping_enabled: false, min_goods_cents: 0, packaging_fee_cents: 0, shipping_fee_cents: 0, free_shipping_threshold_cents: 0, estimated_minutes: 0, status: 'active', contact_name: '', contact_phone: '', address: '' })
const config = reactive<any>(defaultConfig())
const configThresholdYuan = computed({ get: () => (props.businessType === 'restaurant' ? config.min_goods_cents : config.free_shipping_threshold_cents) / 100, set: value => { if (props.businessType === 'restaurant') config.min_goods_cents = Math.round(Number(value || 0) * 100); else config.free_shipping_threshold_cents = Math.round(Number(value || 0) * 100) } })
const configPackagingYuan = computed({ get: () => config.packaging_fee_cents / 100, set: value => { config.packaging_fee_cents = Math.round(Number(value || 0) * 100) } })
const configShippingYuan = computed({ get: () => config.shipping_fee_cents / 100, set: value => { config.shipping_fee_cents = Math.round(Number(value || 0) * 100) } })
const weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
const zoneDialogVisible = ref(false)
const slotDialogVisible = ref(false)
const couponDialogVisible = ref(false)
const campaignDialogVisible = ref(false)
const zoneForm = reactive<any>({ id: 0, name: '', province: '', city: '', district: '', fee_yuan: 0, estimated_minutes: 0, status: 'active' })
const slotForm = reactive<any>({ id: 0, day_of_week: 1, start_time: '09:00', end_time: '18:00', order_cutoff_minutes: 0, capacity: 0, status: 'active' })
const couponForm = reactive<any>({ id: 0, channel_account_id: 0, name: '', discount_yuan: 1, min_yuan: 0, valid_days: 7, starts_at: null, ends_at: null, issuance_cap: 0, per_customer_cap: 1, status: 'draft' })
const campaignForm = reactive<any>({ id: 0, title: '', starter_coupon_template_id: 0, helper_coupon_template_id: 0, required_unique_helpers: 1, per_starter_session_limit: 1, starts_at: '', ends_at: '', status: 'draft' })

const activeLocations = computed(() => props.activeLocations)
function apiError(error: any, fallback: string) { return String(error?.response?.data?.error || error?.response?.data?.message || fallback) }
function params(extra: Record<string, unknown> = {}) { return { params: { business_type: props.businessType, ...extra }, skipErrorToast: true } as any }
function yuan(cents: unknown) { return (Number(cents || 0) / 100).toFixed(2) }
function minuteLabel(value: unknown) { const n = Number(value || 0); return `${String(Math.floor(n / 60)).padStart(2, '0')}:${String(n % 60).padStart(2, '0')}` }
function minuteValue(value: string) { const [hour, minute] = String(value || '').split(':').map(Number); return (hour || 0) * 60 + (minute || 0) }
function weekdayLabel(value: unknown) { return weekdays[Number(value)] || '-' }
function resetError() { errorMessage.value = '' }
function clearFulfillmentState() { Object.assign(config, defaultConfig()); zones.value = []; slots.value = [] }
function clearPromotionState(resetChannel = true) { channels.value = []; if (resetChannel) promotionChannelID.value = 0; couponTemplates.value = []; campaigns.value = [] }

async function loadFulfillment() {
  const requestID = ++fulfillmentRequestID.value
  const locationID = selectedLocationID.value
  const businessType = props.businessType
  clearFulfillmentState()
  if (!locationID) { loading.value = false; return }
  loading.value = true; resetError()
  try {
    const requests = [request.get(`/commerce/locations/${locationID}/service-config`, params())]
    if (businessType === 'restaurant') {
      requests.push(
        request.get(`/commerce/locations/${locationID}/delivery-zones`, params()),
        request.get(`/commerce/locations/${locationID}/delivery-slots`, params()),
      )
    }
    const [configResponse, zonesResponse, slotsResponse] = await Promise.all(requests)
    if (requestID !== fulfillmentRequestID.value || locationID !== selectedLocationID.value || businessType !== props.businessType) return
    Object.assign(config, configResponse.data?.data || configResponse.data || {})
    zones.value = zonesResponse?.data?.data || []
    slots.value = slotsResponse?.data?.data || []
  } catch (error) {
    if (requestID === fulfillmentRequestID.value) { clearFulfillmentState(); errorMessage.value = apiError(error, '履约配置暂时无法加载') }
  } finally {
    if (requestID === fulfillmentRequestID.value) loading.value = false
  }
}

async function saveConfig() {
  if (!props.canWrite || !selectedLocationID.value) return
  saving.value = true; resetError()
  try { await request.put(`/commerce/locations/${selectedLocationID.value}/service-config`, { ...config, business_type: props.businessType }); ElMessage.success('服务配置已保存'); await loadFulfillment() } catch (error) { errorMessage.value = apiError(error, '服务配置保存失败') } finally { saving.value = false }
}

function openZoneDialog(row?: any) { Object.assign(zoneForm, { id: Number(row?.id || 0), name: row?.name || '', province: row?.province || '', city: row?.city || '', district: row?.district || '', fee_yuan: Number(row?.fee_cents || 0) / 100, estimated_minutes: Number(row?.estimated_minutes || 0), status: row?.status || 'active' }); zoneDialogVisible.value = true }
async function saveZone() {
  if (!zoneForm.name.trim() || !zoneForm.province.trim() || !zoneForm.city.trim() || !zoneForm.district.trim()) { ElMessage.warning('请完整填写配送区域'); return }
  saving.value = true; resetError()
  try { const payload = { name: zoneForm.name.trim(), province: zoneForm.province.trim(), city: zoneForm.city.trim(), district: zoneForm.district.trim(), fee_cents: Math.round(Number(zoneForm.fee_yuan || 0) * 100), estimated_minutes: Number(zoneForm.estimated_minutes || 0), status: zoneForm.status, sort_order: 0 }; if (zoneForm.id) await request.put(`/commerce/locations/${selectedLocationID.value}/delivery-zones/${zoneForm.id}`, payload, params()); else await request.post(`/commerce/locations/${selectedLocationID.value}/delivery-zones`, payload, params()); zoneDialogVisible.value = false; ElMessage.success('配送区域已保存'); await loadFulfillment() } catch (error) { errorMessage.value = apiError(error, '配送区域保存失败') } finally { saving.value = false }
}
function openSlotDialog(row?: any) { Object.assign(slotForm, { id: Number(row?.id || 0), day_of_week: Number(row?.day_of_week ?? 1), start_time: minuteLabel(row?.start_minute ?? 540), end_time: minuteLabel(row?.end_minute ?? 1080), order_cutoff_minutes: Number(row?.order_cutoff_minutes || 0), capacity: Number(row?.capacity || 0), status: row?.status || 'active' }); slotDialogVisible.value = true }
async function saveSlot() {
  const start = minuteValue(slotForm.start_time); const end = minuteValue(slotForm.end_time)
  if (end <= start) { ElMessage.warning('结束时间必须晚于开始时间'); return }
  saving.value = true; resetError()
  try { const payload = { day_of_week: Number(slotForm.day_of_week), start_minute: start, end_minute: end, order_cutoff_minutes: Number(slotForm.order_cutoff_minutes || 0), capacity: Number(slotForm.capacity || 0), status: slotForm.status }; if (slotForm.id) await request.put(`/commerce/locations/${selectedLocationID.value}/delivery-slots/${slotForm.id}`, payload, params()); else await request.post(`/commerce/locations/${selectedLocationID.value}/delivery-slots`, payload, params()); slotDialogVisible.value = false; ElMessage.success('配送时段已保存'); await loadFulfillment() } catch (error) { errorMessage.value = apiError(error, '配送时段保存失败') } finally { saving.value = false }
}

async function loadPromotions() {
  const requestID = ++promotionRequestID.value
  const businessType = props.businessType
  clearPromotionState(false)
  promotionLoading.value = true; resetError()
  try {
    const channelsResponse = await request.get('/commerce/storefront-channels', { skipErrorToast: true } as any)
    if (requestID !== promotionRequestID.value || businessType !== props.businessType) return
    channels.value = (channelsResponse.data?.data || []).filter((row: any) => row.status !== 'disabled')
    if (!promotionChannelID.value || !channels.value.some(row => Number(row.id) === promotionChannelID.value)) promotionChannelID.value = Number(channels.value[0]?.id || 0)
    if (!promotionChannelID.value) { couponTemplates.value = []; campaigns.value = []; return }
    const listParams = params({ channel_account_id: promotionChannelID.value })
    const [couponsResponse, campaignsResponse] = await Promise.all([
      request.get('/commerce/promotions/coupon-templates', listParams),
      request.get('/commerce/promotions/assist-campaigns', listParams),
    ])
    if (requestID !== promotionRequestID.value || businessType !== props.businessType) return
    couponTemplates.value = couponsResponse.data?.data || []
    campaigns.value = campaignsResponse.data?.data || []
  } catch (error) {
    if (requestID === promotionRequestID.value) { clearPromotionState(false); errorMessage.value = apiError(error, '营销活动暂时无法加载') }
  } finally {
    if (requestID === promotionRequestID.value) promotionLoading.value = false
  }
}
function optionalISOString(value: unknown) {
  if (!value) return null
  const date = new Date(String(value))
  return Number.isNaN(date.getTime()) ? value : date.toISOString()
}
function openCouponDialog(row?: any) { Object.assign(couponForm, { id: Number(row?.id || 0), channel_account_id: Number(row?.channel_account_id || promotionChannelID.value || channels.value[0]?.id || 0), name: row?.name || '', discount_yuan: Number(row?.discount_cents ?? 100) / 100, min_yuan: Number(row?.min_goods_subtotal_cents ?? 0) / 100, valid_days: Number(row?.valid_days ?? 7), starts_at: row?.starts_at || null, ends_at: row?.ends_at || null, issuance_cap: Number(row?.issuance_cap ?? 0), per_customer_cap: Number(row?.per_customer_cap ?? 1), status: row?.status || 'draft' }); couponDialogVisible.value = true }
async function saveCoupon() {
  if (!couponForm.channel_account_id || !couponForm.name.trim()) { ElMessage.warning('请选择渠道账号并填写模板名称'); return }
  saving.value = true; resetError()
  try { if (couponForm.id) await request.put(`/commerce/promotions/coupon-templates/${couponForm.id}`, { name: couponForm.name.trim(), starts_at: optionalISOString(couponForm.starts_at), ends_at: optionalISOString(couponForm.ends_at), status: couponForm.status, issuance_cap: Number(couponForm.issuance_cap || 0), per_customer_cap: Number(couponForm.per_customer_cap || 1) }, { params: { channel_account_id: couponForm.channel_account_id, business_type: props.businessType }, skipErrorToast: true } as any); else await request.post('/commerce/promotions/coupon-templates', { channel_account_id: couponForm.channel_account_id, business_type: props.businessType, name: couponForm.name.trim(), discount_cents: Math.round(Number(couponForm.discount_yuan || 0) * 100), min_goods_subtotal_cents: Math.round(Number(couponForm.min_yuan || 0) * 100), valid_days: Number(couponForm.valid_days || 0), starts_at: optionalISOString(couponForm.starts_at), ends_at: optionalISOString(couponForm.ends_at), status: couponForm.status, issuance_cap: Number(couponForm.issuance_cap || 0), per_customer_cap: Number(couponForm.per_customer_cap || 1), refund_return_policy: 'unfulfilled_full_refund_if_valid' }, { skipErrorToast: true } as any); couponDialogVisible.value = false; ElMessage.success('优惠券模板已保存'); await loadPromotions() } catch (error) { errorMessage.value = apiError(error, '优惠券模板保存失败') } finally { saving.value = false }
}
function openCampaignDialog(row?: any) { Object.assign(campaignForm, { id: Number(row?.id || 0), title: row?.title || '', starter_coupon_template_id: Number(row?.starter_coupon_template_id || couponTemplates.value[0]?.id || 0), helper_coupon_template_id: Number(row?.helper_coupon_template_id || couponTemplates.value[0]?.id || 0), required_unique_helpers: Number(row?.required_unique_helpers || 1), per_starter_session_limit: Number(row?.per_starter_session_limit || 1), starts_at: row?.starts_at || new Date(), ends_at: row?.ends_at || new Date(Date.now() + 7 * 86400000), status: row?.status || 'draft' }); campaignDialogVisible.value = true }
async function saveCampaign() {
  if (!campaignForm.title.trim() || !campaignForm.starter_coupon_template_id || !campaignForm.helper_coupon_template_id) { ElMessage.warning('请完整填写活动和奖励模板'); return }
  saving.value = true; resetError()
  try { const payload = { title: campaignForm.title.trim(), required_unique_helpers: Number(campaignForm.required_unique_helpers || 1), per_starter_session_limit: Number(campaignForm.per_starter_session_limit || 1), starts_at: new Date(campaignForm.starts_at).toISOString(), ends_at: new Date(campaignForm.ends_at).toISOString(), status: campaignForm.status }; if (campaignForm.id) await request.put(`/commerce/promotions/assist-campaigns/${campaignForm.id}`, payload, { params: { channel_account_id: promotionChannelID.value, business_type: props.businessType }, skipErrorToast: true } as any); else await request.post('/commerce/promotions/assist-campaigns', { ...payload, channel_account_id: promotionChannelID.value, business_type: props.businessType, starter_coupon_template_id: campaignForm.starter_coupon_template_id, helper_coupon_template_id: campaignForm.helper_coupon_template_id }, { skipErrorToast: true } as any); campaignDialogVisible.value = false; ElMessage.success('助力活动已保存'); await loadPromotions() } catch (error) { errorMessage.value = apiError(error, '助力活动保存失败') } finally { saving.value = false }
}

watch(() => props.businessType, () => {
  selectedLocationID.value = 0
  clearFulfillmentState()
  clearPromotionState()
  resetError()
  if (panelTab.value === 'promotions') void loadPromotions()
})
watch(() => props.activeLocations, value => {
  const selected = Number(value.find(location => Number(location.id) === selectedLocationID.value)?.id || value[0]?.id || 0)
  if (selected !== selectedLocationID.value) selectedLocationID.value = selected
}, { deep: true })
watch(selectedLocationID, (value, previous) => {
  if (value !== previous && panelTab.value === 'fulfillment') void loadFulfillment()
})
watch(panelTab, tab => {
  if (tab === 'fulfillment') void loadFulfillment()
  if (tab === 'promotions') void loadPromotions()
})
onMounted(() => { void loadFulfillment() })
</script>

<style scoped>
.phase-two-panel { display: flex; flex-direction: column; gap: 16px; }
.panel-alert { margin-bottom: 4px; }
.panel-heading, .subsection-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 14px; }
.panel-heading h3, .subsection-heading h4 { margin: 0 0 4px; }
.subsection-heading h4 { font-size: 15px; }
.subsection-heading strong { font-size: 15px; }
.muted { color: var(--el-text-color-secondary); margin: 0; }
.phase-form { max-width: 900px; }
.phase-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px 16px; }
.form-actions { display: flex; justify-content: flex-end; margin-top: 4px; }
.subsection { margin-top: 24px; }
.slots-heading, .campaign-heading { margin-top: 26px; }
.phase-table { width: 100%; }
.secondary-cell { color: var(--el-text-color-secondary); font-size: 12px; }
.full-width { width: 100%; }
@media (max-width: 760px) { .panel-heading, .subsection-heading { flex-direction: column; align-items: stretch; } .phase-grid { grid-template-columns: 1fr; } .phase-table { overflow-x: auto; } }
</style>
