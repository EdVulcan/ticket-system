<template>
  <section class="marketing-page" data-testid="commerce-marketing-workspace">
    <header class="page-heading marketing-heading">
      <div class="page-heading-copy">
        <div class="section-kicker">商业经营</div>
        <h1>营销中心</h1>
        <p>统一管理店铺优惠券和分享助力活动，并选择它们适用的业务板块。</p>
      </div>
      <div class="page-actions">
        <el-tag type="success" effect="plain">店铺级营销</el-tag>
        <el-button :icon="Refresh" :loading="loading" @click="loadPromotions">刷新</el-button>
      </div>
    </header>

    <el-alert
      v-if="errorMessage"
      class="marketing-alert"
      type="error"
      :closable="false"
      :title="errorMessage"
    />

    <section class="marketing-scope-bar">
      <div>
        <strong>当前店铺渠道</strong>
        <p>同一微信小程序渠道下，优惠券可以同时用于餐饮和电商订单。</p>
      </div>
      <div class="scope-actions">
        <el-select
          v-model="channelID"
          class="channel-select"
          filterable
          :disabled="loading || !channels.length"
          placeholder="选择微信小程序渠道"
          @change="loadPromotions"
        >
          <el-option
            v-for="channel in channels"
            :key="channel.id"
            :label="`${channel.code} · ${channel.app_id || '未填写 AppID'}`"
            :value="channel.id"
          />
        </el-select>
      </div>
    </section>

    <el-tabs v-model="activeTab" class="marketing-tabs">
      <el-tab-pane label="优惠券模板" name="coupons">
        <section class="marketing-section">
          <div class="section-toolbar">
            <div>
              <h2>优惠券模板</h2>
              <p class="muted">已配置 {{ couponTemplates.length }} 个模板；适用板块决定用户在哪类订单中可以使用。</p>
            </div>
            <el-button v-if="canWrite" type="primary" plain :icon="Plus" @click="openCouponDialog()">新增优惠券模板</el-button>
          </div>
          <el-table v-loading="loading" :data="couponTemplates" class="marketing-table" border stripe>
            <el-table-column prop="name" label="名称" min-width="190" />
            <el-table-column label="适用板块" min-width="180">
              <template #default="{ row }">
                <div class="scope-tags">
                  <el-tag v-for="type in normalizedBusinessTypes(row)" :key="type" size="small" effect="plain">{{ businessTypeLabel(type) }}</el-tag>
                  <span v-if="!normalizedBusinessTypes(row).length" class="muted">未配置</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="优惠" width="110" align="right"><template #default="{ row }">减 ¥{{ yuan(row.discount_cents) }}</template></el-table-column>
            <el-table-column label="使用门槛" width="120" align="right"><template #default="{ row }">¥{{ yuan(row.min_goods_subtotal_cents) }}</template></el-table-column>
            <el-table-column label="有效期" min-width="150"><template #default="{ row }">{{ validityLabel(row) }}</template></el-table-column>
            <el-table-column label="状态" width="90" align="center"><template #default="{ row }"><el-tag :type="statusType(row.status)" effect="plain">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
            <el-table-column label="操作" width="100" fixed="right" align="right"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openCouponDialog(row)">编辑</el-button><span v-else class="muted">只读</span></template></el-table-column>
            <template #empty>
              <el-empty description="暂无优惠券模板" :image-size="64">
                <el-button v-if="canWrite" type="primary" :icon="Plus" @click="openCouponDialog()">新增优惠券模板</el-button>
              </el-empty>
            </template>
          </el-table>
        </section>
      </el-tab-pane>

      <el-tab-pane label="分享助力" name="campaigns">
        <section class="marketing-section">
          <div class="section-toolbar">
            <div>
              <h2>分享助力活动</h2>
              <p class="muted">活动归属店铺，参与者获得的优惠券可按模板适用板块使用。</p>
            </div>
            <el-button v-if="canWrite && couponTemplates.length" type="primary" plain :icon="Plus" @click="openCampaignDialog()">新增分享助力活动</el-button>
          </div>
          <el-table v-loading="loading" :data="campaigns" class="marketing-table" border stripe>
            <el-table-column prop="title" label="活动名称" min-width="200" />
            <el-table-column label="适用板块" min-width="180">
              <template #default="{ row }"><div class="scope-tags"><el-tag v-for="type in normalizedBusinessTypes(row)" :key="type" size="small" effect="plain">{{ businessTypeLabel(type) }}</el-tag><span v-if="!normalizedBusinessTypes(row).length" class="muted">未配置</span></div></template>
            </el-table-column>
            <el-table-column label="所需助力" width="110" align="right"><template #default="{ row }">{{ row.required_unique_helpers }}</template></el-table-column>
            <el-table-column label="活动时间" min-width="220"><template #default="{ row }">{{ dateLabel(row.starts_at) }} 至 {{ dateLabel(row.ends_at) }}</template></el-table-column>
            <el-table-column label="状态" width="90" align="center"><template #default="{ row }"><el-tag :type="statusType(row.status)" effect="plain">{{ statusLabel(row.status) }}</el-tag></template></el-table-column>
            <el-table-column label="操作" width="100" fixed="right" align="right"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openCampaignDialog(row)">编辑</el-button><span v-else class="muted">只读</span></template></el-table-column>
            <template #empty>
              <el-empty description="暂无分享助力活动" :image-size="64">
                <el-button v-if="canWrite && couponTemplates.length" type="primary" :icon="Plus" @click="openCampaignDialog()">新增分享助力活动</el-button>
                <el-button v-else-if="canWrite" type="primary" plain :icon="Plus" @click="activeTab = 'coupons'; openCouponDialog()">先创建优惠券模板</el-button>
              </el-empty>
            </template>
          </el-table>
        </section>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="couponDialogVisible" :title="couponForm.id ? '编辑优惠券模板' : '新增优惠券模板'" width="min(660px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="couponForm" label-position="top" class="marketing-form">
        <el-form-item label="微信小程序渠道" required>
          <el-select v-model="couponForm.channel_account_id" class="full-width" filterable :disabled="Boolean(couponForm.id)">
            <el-option v-for="channel in channels" :key="channel.id" :label="`${channel.code} · ${channel.app_id || '未填写 AppID'}`" :value="channel.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="适用板块" required>
          <el-checkbox-group v-model="couponForm.business_types" class="business-checkboxes">
            <el-checkbox v-for="type in availableBusinessTypes" :key="type" :label="type">{{ businessTypeLabel(type) }}</el-checkbox>
          </el-checkbox-group>
          <div class="field-help">优惠券属于店铺营销，可同时选择多个板块；订单结算时由服务端按实际业务校验。</div>
        </el-form-item>
        <el-form-item label="模板名称" required><el-input v-model="couponForm.name" maxlength="120" /></el-form-item>
        <div class="marketing-grid">
          <el-form-item label="优惠金额（元）" required><el-input-number v-model="couponForm.discount_yuan" class="full-width" :min="0.01" :precision="2" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item>
          <el-form-item label="使用门槛（元）"><el-input-number v-model="couponForm.min_yuan" class="full-width" :min="0" :precision="2" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item>
          <el-form-item label="有效天数"><el-input-number v-model="couponForm.valid_days" class="full-width" :min="0" :max="3650" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item>
          <el-form-item label="发放上限（0 为不限）"><el-input-number v-model="couponForm.issuance_cap" class="full-width" :min="0" :controls="false" /></el-form-item>
          <el-form-item label="每人上限"><el-input-number v-model="couponForm.per_customer_cap" class="full-width" :min="1" :controls="false" :disabled="Boolean(couponForm.id)" /></el-form-item>
          <el-form-item label="状态"><el-select v-model="couponForm.status" class="full-width"><el-option label="草稿" value="draft" /><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
          <el-form-item label="开始时间"><el-date-picker v-model="couponForm.starts_at" type="datetime" class="full-width" /></el-form-item>
          <el-form-item label="结束时间"><el-date-picker v-model="couponForm.ends_at" type="datetime" class="full-width" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="couponDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveCoupon">保存模板</el-button></template>
    </el-dialog>

    <el-dialog v-model="campaignDialogVisible" :title="campaignForm.id ? '编辑分享助力活动' : '新增分享助力活动'" width="min(660px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="campaignForm" label-position="top" class="marketing-form">
        <el-form-item label="活动名称" required><el-input v-model="campaignForm.title" maxlength="160" /></el-form-item>
        <el-form-item label="适用板块" required>
          <el-checkbox-group v-model="campaignForm.business_types" class="business-checkboxes">
            <el-checkbox v-for="type in availableBusinessTypes" :key="type" :label="type">{{ businessTypeLabel(type) }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <div class="marketing-grid">
          <el-form-item label="发起人奖励" required><el-select v-model="campaignForm.starter_coupon_template_id" class="full-width" :disabled="Boolean(campaignForm.id)"><el-option v-for="coupon in couponTemplates" :key="coupon.id" :label="coupon.name" :value="coupon.id" /></el-select></el-form-item>
          <el-form-item label="助力人奖励" required><el-select v-model="campaignForm.helper_coupon_template_id" class="full-width" :disabled="Boolean(campaignForm.id)"><el-option v-for="coupon in couponTemplates" :key="coupon.id" :label="coupon.name" :value="coupon.id" /></el-select></el-form-item>
          <el-form-item label="所需独立助力人数"><el-input-number v-model="campaignForm.required_unique_helpers" class="full-width" :min="1" :controls="false" /></el-form-item>
          <el-form-item label="每人可发起次数"><el-input-number v-model="campaignForm.per_starter_session_limit" class="full-width" :min="1" :controls="false" /></el-form-item>
          <el-form-item label="开始时间" required><el-date-picker v-model="campaignForm.starts_at" type="datetime" class="full-width" /></el-form-item>
          <el-form-item label="结束时间" required><el-date-picker v-model="campaignForm.ends_at" type="datetime" class="full-width" /></el-form-item>
          <el-form-item label="状态"><el-select v-model="campaignForm.status" class="full-width"><el-option label="草稿" value="draft" /><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="campaignDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveCampaign">保存活动</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { activeBusinessCapabilitySet, configuredBusinessCapabilitySet, readStoredUser } from '@/utils/tenantAccess'
import { hasPermission } from '@/utils/permissions'

type BusinessType = 'restaurant' | 'retail'

// An omitted Boolean prop is false in Vue; true means the parent has not overridden the page's own permission check.
const props = withDefaults(defineProps<{ canWrite?: boolean }>(), { canWrite: true })
const activeTab = ref('coupons')
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const channels = ref<any[]>([])
const channelID = ref(0)
const couponTemplates = ref<any[]>([])
const campaigns = ref<any[]>([])
const couponDialogVisible = ref(false)
const campaignDialogVisible = ref(false)

const user = readStoredUser()
const configuredTypes = configuredBusinessCapabilitySet(user)
const activeTypes = activeBusinessCapabilitySet(user)
const canWrite = computed(() => props.canWrite !== false && hasPermission(user, 'catalog.write') && [...activeTypes].some(type => type === 'restaurant' || type === 'retail'))
const availableBusinessTypes = computed<BusinessType[]>(() => {
  const values = (['restaurant', 'retail'] as BusinessType[]).filter(type => configuredTypes.has(type))
  return values.length ? values : ['restaurant', 'retail']
})

const couponForm = reactive<any>({ id: 0, channel_account_id: 0, business_types: [] as BusinessType[], name: '', discount_yuan: 1, min_yuan: 0, valid_days: 7, starts_at: null, ends_at: null, issuance_cap: 0, per_customer_cap: 1, status: 'draft' })
const campaignForm = reactive<any>({ id: 0, title: '', business_types: [] as BusinessType[], starter_coupon_template_id: 0, helper_coupon_template_id: 0, required_unique_helpers: 1, per_starter_session_limit: 1, starts_at: '', ends_at: '', status: 'draft' })

function apiError(error: any, fallback: string) { return String(error?.response?.data?.error || error?.response?.data?.message || fallback) }
function businessTypeLabel(value: string) { return value === 'restaurant' ? '餐饮' : value === 'retail' ? '电商' : value }
function normalizedBusinessTypes(row: any): BusinessType[] {
  const values = Array.isArray(row?.business_types) ? row.business_types : row?.business_type ? [row.business_type] : []
  return values.filter((value: string): value is BusinessType => value === 'restaurant' || value === 'retail')
}
function statusLabel(value: string) { return value === 'active' ? '启用' : value === 'inactive' ? '停用' : '草稿' }
function statusType(value: string) { return value === 'active' ? 'success' : value === 'inactive' ? 'info' : 'warning' }
function yuan(cents: unknown) { return (Number(cents || 0) / 100).toFixed(2) }
function dateLabel(value: unknown) { if (!value) return '未设置'; const date = new Date(String(value)); return Number.isNaN(date.getTime()) ? '未设置' : date.toLocaleString('zh-CN', { hour12: false }) }
function validityLabel(row: any) { if (row.valid_days > 0) return `领取后 ${row.valid_days} 天`; if (row.ends_at) return `至 ${dateLabel(row.ends_at)}`; return '未设置' }
function optionalISOString(value: unknown) { if (!value) return null; const date = new Date(String(value)); return Number.isNaN(date.getTime()) ? value : date.toISOString() }
function resetError() { errorMessage.value = '' }

async function loadPromotions() {
  loading.value = true
  resetError()
  try {
    const channelsResponse = await request.get('/commerce/storefront-channels', { skipErrorToast: true } as any)
    channels.value = (channelsResponse.data?.data || []).filter((row: any) => row.status !== 'disabled')
    if (!channelID.value || !channels.value.some(row => Number(row.id) === channelID.value)) channelID.value = Number(channels.value[0]?.id || 0)
    if (!channelID.value) { couponTemplates.value = []; campaigns.value = []; return }
    const params = { params: { channel_account_id: channelID.value }, skipErrorToast: true } as any
    const [couponsResponse, campaignsResponse] = await Promise.all([
      request.get('/commerce/promotions/coupon-templates', params),
      request.get('/commerce/promotions/assist-campaigns', params),
    ])
    couponTemplates.value = couponsResponse.data?.data || []
    campaigns.value = campaignsResponse.data?.data || []
  } catch (error) {
    couponTemplates.value = []
    campaigns.value = []
    errorMessage.value = apiError(error, '营销配置暂时无法加载')
  } finally {
    loading.value = false
  }
}

function openCouponDialog(row?: any) {
  Object.assign(couponForm, {
    id: Number(row?.id || 0), channel_account_id: Number(row?.channel_account_id || channelID.value || channels.value[0]?.id || 0),
    business_types: normalizedBusinessTypes(row).length ? normalizedBusinessTypes(row) : [...availableBusinessTypes.value],
    name: row?.name || '', discount_yuan: Number(row?.discount_cents ?? 100) / 100, min_yuan: Number(row?.min_goods_subtotal_cents ?? 0) / 100,
    valid_days: Number(row?.valid_days ?? 7), starts_at: row?.starts_at || null, ends_at: row?.ends_at || null,
    issuance_cap: Number(row?.issuance_cap ?? 0), per_customer_cap: Number(row?.per_customer_cap ?? 1), status: row?.status || 'draft',
  })
  couponDialogVisible.value = true
}

async function saveCoupon() {
  if (!couponForm.channel_account_id || !couponForm.name.trim() || !couponForm.business_types.length) { ElMessage.warning('请选择渠道、适用板块并填写模板名称'); return }
  saving.value = true; resetError()
  try {
    if (couponForm.id) {
      await request.put(`/commerce/promotions/coupon-templates/${couponForm.id}`, { business_types: couponForm.business_types, name: couponForm.name.trim(), starts_at: optionalISOString(couponForm.starts_at), ends_at: optionalISOString(couponForm.ends_at), status: couponForm.status, issuance_cap: Number(couponForm.issuance_cap || 0), per_customer_cap: Number(couponForm.per_customer_cap || 1) }, { params: { channel_account_id: couponForm.channel_account_id }, skipErrorToast: true } as any)
    } else {
      await request.post('/commerce/promotions/coupon-templates', { channel_account_id: couponForm.channel_account_id, business_types: couponForm.business_types, name: couponForm.name.trim(), discount_cents: Math.round(Number(couponForm.discount_yuan || 0) * 100), min_goods_subtotal_cents: Math.round(Number(couponForm.min_yuan || 0) * 100), valid_days: Number(couponForm.valid_days || 0), starts_at: optionalISOString(couponForm.starts_at), ends_at: optionalISOString(couponForm.ends_at), status: couponForm.status, issuance_cap: Number(couponForm.issuance_cap || 0), per_customer_cap: Number(couponForm.per_customer_cap || 1), refund_return_policy: 'unfulfilled_full_refund_if_valid' }, { skipErrorToast: true } as any)
    }
    couponDialogVisible.value = false; ElMessage.success('优惠券模板已保存'); await loadPromotions()
  } catch (error) { errorMessage.value = apiError(error, '优惠券模板保存失败') } finally { saving.value = false }
}

function openCampaignDialog(row?: any) {
  Object.assign(campaignForm, {
    id: Number(row?.id || 0), title: row?.title || '', business_types: normalizedBusinessTypes(row).length ? normalizedBusinessTypes(row) : [...availableBusinessTypes.value],
    starter_coupon_template_id: Number(row?.starter_coupon_template_id || couponTemplates.value[0]?.id || 0), helper_coupon_template_id: Number(row?.helper_coupon_template_id || couponTemplates.value[0]?.id || 0),
    required_unique_helpers: Number(row?.required_unique_helpers || 1), per_starter_session_limit: Number(row?.per_starter_session_limit || 1), starts_at: row?.starts_at || new Date(), ends_at: row?.ends_at || new Date(Date.now() + 7 * 86400000), status: row?.status || 'draft',
  })
  campaignDialogVisible.value = true
}

async function saveCampaign() {
  if (!campaignForm.title.trim() || !campaignForm.business_types.length || !campaignForm.starter_coupon_template_id || !campaignForm.helper_coupon_template_id) { ElMessage.warning('请完整填写活动、适用板块和奖励模板'); return }
  saving.value = true; resetError()
  try {
    const payload = { title: campaignForm.title.trim(), business_types: campaignForm.business_types, required_unique_helpers: Number(campaignForm.required_unique_helpers || 1), per_starter_session_limit: Number(campaignForm.per_starter_session_limit || 1), starts_at: new Date(campaignForm.starts_at).toISOString(), ends_at: new Date(campaignForm.ends_at).toISOString(), status: campaignForm.status }
    if (campaignForm.id) await request.put(`/commerce/promotions/assist-campaigns/${campaignForm.id}`, payload, { params: { channel_account_id: channelID.value }, skipErrorToast: true } as any)
    else await request.post('/commerce/promotions/assist-campaigns', { ...payload, channel_account_id: channelID.value, starter_coupon_template_id: campaignForm.starter_coupon_template_id, helper_coupon_template_id: campaignForm.helper_coupon_template_id }, { skipErrorToast: true } as any)
    campaignDialogVisible.value = false; ElMessage.success('助力活动已保存'); await loadPromotions()
  } catch (error) { errorMessage.value = apiError(error, '助力活动保存失败') } finally { saving.value = false }
}

onMounted(() => { void loadPromotions() })
</script>

<style scoped>
.marketing-page { display: flex; flex-direction: column; gap: 16px; }
.marketing-heading { margin-bottom: 0; }
.marketing-alert { margin-bottom: 0; }
.marketing-scope-bar { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding: 16px 18px; border: 1px solid var(--el-border-color-light); border-radius: 8px; background: var(--el-bg-color); }
.marketing-scope-bar p { margin: 4px 0 0; color: var(--el-text-color-secondary); font-size: 13px; }
.scope-actions { min-width: min(360px, 100%); }
.channel-select, .full-width { width: 100%; }
.marketing-tabs, .marketing-section { min-width: 0; }
.marketing-section { padding: 4px 0 0; }
.section-toolbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 14px; }
.section-toolbar h2 { margin: 0 0 4px; font-size: 18px; }
.muted { color: var(--el-text-color-secondary); margin: 0; }
.marketing-table { width: 100%; }
.scope-tags { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
.marketing-form { max-width: 900px; }
.marketing-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 4px 16px; }
.business-checkboxes { display: flex; flex-wrap: wrap; gap: 8px 18px; }
.field-help { margin-top: 5px; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.5; }
@media (max-width: 760px) { .marketing-scope-bar, .section-toolbar { flex-direction: column; align-items: stretch; } .scope-actions { min-width: 0; } .marketing-grid { grid-template-columns: 1fr; } .marketing-table { overflow-x: auto; } }
</style>
