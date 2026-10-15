<template>
  <el-alert v-if="canRecover && pending && !modelValue" type="warning" :closable="false" title="有一笔发券结果待确认，请恢复原记录后查询或重试。">
    <el-button @click="emit('update:modelValue', true)">恢复待确认发券</el-button>
  </el-alert>
  <el-dialog :model-value="modelValue" title="发优惠券给会员" width="min(820px, calc(100vw - 32px))" :close-on-click-modal="false" @update:model-value="emit('update:modelValue', $event)">
    <el-alert v-if="!canRecover" type="warning" :closable="false" title="当前账号没有会员发券权限，或会员与商业能力未启用。" />
    <template v-else>
      <el-alert v-if="message" :type="pending ? 'warning' : 'error'" :closable="false" :title="message" />
      <template v-if="pending">
        <p>本次向 {{ pending.member_ids.length }} 位会员各发一张券。原发放内容已锁定，关闭或刷新页面后仍可恢复。</p>
        <p>优惠券：{{ pending.template_name || '原优惠券模板' }} · 用途：{{ pending.reason || '会员优惠券发放' }}</p>
        <p>{{ pending.recipients?.map(item => item.display_name || item.member_no).join('、') }}</p>
        <el-button :loading="recovering" :disabled="issuing" @click="recover">查询发放结果</el-button>
        <el-button type="primary" :loading="issuing" :disabled="recovering || !allowed" @click="issue(true)">重试原发放</el-button>
        <p v-if="!allowed">商业业务已暂停，可查询原发放结果，恢复业务后才能重试发放。</p>
      </template>
      <template v-else-if="result">
        <el-result icon="success" title="优惠券已发放" :sub-title="`已向 ${result.member_count} 位会员各发一张券`" />
        <el-table :data="result.items || []"><el-table-column prop="member_no" label="会员编号" /><el-table-column prop="display_name" label="会员" /></el-table>
      </template>
      <el-alert v-else-if="!allowed" type="warning" :closable="false" title="商业业务已暂停，暂不能发放新的优惠券。" />
      <template v-else>
        <el-form label-position="top" class="grant-form">
          <el-form-item label="店铺渠道" required>
            <el-select data-testid="grant-channel-select" v-model="channelID" placeholder="选择店铺渠道" :disabled="Boolean(preselectedTemplate) || busy" @change="changeChannel">
              <el-option v-for="channel in channels" :key="channel.id" :label="channel.code || channel.name" :value="Number(channel.id)" />
            </el-select>
          </el-form-item>
          <el-form-item label="优惠券模板" required>
            <el-select data-testid="grant-template-select" v-model="templateID" placeholder="选择启用中的优惠券模板" :disabled="Boolean(preselectedTemplate) || busy" @change="invalidatePreview">
              <el-option v-for="template in templates" :key="template.id" :label="template.name" :value="Number(template.id)" />
            </el-select>
          </el-form-item>
          <div v-if="selectedTemplate" class="template-summary">
            <strong>{{ selectedTemplate.name }}</strong>
            <p>减 ¥{{ yuan(selectedTemplate.discount_cents) }} · 商品金额满 ¥{{ yuan(selectedTemplate.min_goods_subtotal_cents) }} 可用</p>
            <p>{{ selectedTemplate.valid_days > 0 ? `发放后 ${selectedTemplate.valid_days} 天有效${selectedTemplate.ends_at ? '，最晚至 ' + dateLabel(selectedTemplate.ends_at) : ''}` : `有效至 ${dateLabel(selectedTemplate.ends_at)}` }} · {{ (selectedTemplate.business_types || [selectedTemplate.business_type]).map(businessLabel).join('、') }}</p>
          </div>
          <el-form-item label="发放用途（选填）"><el-input v-model="reason" maxlength="255" placeholder="例如：会员回馈活动" :disabled="busy" @input="invalidatePreview" /></el-form-item>
        </el-form>
        <p>已选 {{ selected.length }}/100 位正式会员，每位一张；本次全部符合条件才会发放。</p>
        <div class="selected-members"><el-tag v-for="member in selected" :key="member.id" closable :disable-transitions="true" @close="removeMember(member.id)">{{ member.display_name || member.member_no }}</el-tag></div>
        <template v-if="!initialMembers.length">
          <div class="member-search"><el-input v-model="keyword" placeholder="搜索姓名或会员编号" :disabled="busy" @keyup.enter="searchMembers" /><el-button :disabled="busy" @click="searchMembers">查找会员</el-button></div>
          <el-table v-loading="membersLoading" :data="members">
            <el-table-column prop="member_no" label="会员编号" />
            <el-table-column prop="display_name" label="会员" />
            <el-table-column label="选择" width="100"><template #default="{ row }"><el-checkbox :model-value="selected.some(item => item.id === row.id)" :disabled="busy || !formal(row) || (!selected.some(item => item.id === row.id) && selected.length >= 100)" :aria-label="`选择会员${row.member_no}`" @change="toggleMember(row)" /></template></el-table-column>
          </el-table>
          <el-pagination :current-page="memberPage" :total="memberTotal" :page-size="20" layout="total, prev, pager, next" @current-change="loadMembers" />
        </template>
        <div v-if="preview" class="preview-result">
          <el-alert :type="preview.eligible ? 'success' : 'warning'" :closable="false" :title="preview.eligible ? `可向 ${preview.member_count} 位会员各发一张券，请确认后发放。` : (preview.reason || '本次有会员不符合发放条件，请调整后重试。')" />
          <el-table :data="preview.recipients || []"><el-table-column prop="member_no" label="会员编号" /><el-table-column prop="display_name" label="会员" /><el-table-column label="发放检查"><template #default="{ row }">{{ row.eligible ? '符合条件' : row.reason || '不符合发放条件' }}</template></el-table-column></el-table>
        </div>
      </template>
    </template>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">关闭</el-button>
      <template v-if="allowed && !pending && !result">
        <el-button :loading="previewLoading" :disabled="busy || !canPreview" @click="previewGrant">检查发放条件</el-button>
        <el-button type="primary" :loading="issuing" :disabled="busy || !preview?.eligible" @click="issue(false)">确认发放</el-button>
      </template>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import request from '@/utils/request'
import { activeBusinessCapabilitySet, configuredBusinessCapabilitySet } from '@/utils/tenantAccess'
import { hasPermission } from '@/utils/permissions'

const props = withDefaults(defineProps<{ modelValue: boolean; user: any; initialMembers?: any[]; preselectedTemplate?: any; preselectedChannel?: number }>(), { initialMembers: () => [] })
const emit = defineEmits(['update:modelValue', 'completed'])
type Attempt = { template_id: number; member_ids: number[]; idempotency_key: string; reason: string; recipients?: any[]; template_name?: string }
const base = '/commerce/promotions/member-coupon-grants'
const config = { skipErrorToast: true } as any
const permitted = computed(() => Number(props.user?.tenant_id) > 0 && Number(props.user?.id) > 0 && props.user?.customer_membership_enabled === true
  && ['members.read', 'members.coupons.write', 'catalog.write'].every(key => hasPermission(props.user, key))
)
const canRecover = computed(() => permitted.value && [...configuredBusinessCapabilitySet(props.user)].some(type => ['restaurant', 'retail'].includes(type)))
const allowed = computed(() => permitted.value && [...activeBusinessCapabilitySet(props.user)].some(type => ['restaurant', 'retail'].includes(type)))
const scope = computed(() => `${props.user?.tenant_id || ''}:${props.user?.id || ''}`)
const storageKey = computed(() => `member-coupon-grant:${scope.value}`)
const pending = ref<Attempt | null>(null)
const channels = ref<any[]>([]), templates = ref<any[]>([]), members = ref<any[]>([]), selected = ref<any[]>([])
const channelID = ref<number>(), templateID = ref<number>(), keyword = ref(''), reason = ref('')
const memberPage = ref(1), memberTotal = ref(0), message = ref(''), result = ref<any>(null), preview = ref<any>(null)
const loading = ref(false), membersLoading = ref(false), previewLoading = ref(false), issuing = ref(false), recovering = ref(false)
let context = 0, channelVersion = 0, memberVersion = 0, previewVersion = 0
const busy = computed(() => loading.value || previewLoading.value || issuing.value || recovering.value)
const selectedTemplate = computed(() => templates.value.find(item => Number(item.id) === templateID.value))
const canPreview = computed(() => selectedTemplate.value && selected.value.length > 0 && selected.value.length <= 100 && selected.value.every(formal))
const formal = (member: any) => member.status === 'active' && member.membership_status === 'active'
const yuan = (value: any) => (Number(value || 0) / 100).toFixed(2)
const dateLabel = (value: any) => value ? new Date(value).toLocaleString('zh-CN') : '模板未设置'
const businessLabel = (value: string) => ({ restaurant: '餐饮', retail: '电商' } as Record<string, string>)[value] || ''
const errorText = (error: any, fallback: string) => error?.response?.data?.error || fallback
const valid = (version: number) => version === context && allowed.value && props.modelValue
function invalidatePreview() { previewVersion++; preview.value = null }
function removeMember(id: number) { if (busy.value) return; selected.value = selected.value.filter(item => item.id !== id); invalidatePreview() }
function toggleMember(row: any) {
  if (busy.value || !formal(row)) return
  if (selected.value.some(item => item.id === row.id)) removeMember(row.id)
  else if (selected.value.length < 100) { selected.value.push(row); invalidatePreview() }
}
function readPending() {
  try {
    const data = JSON.parse(sessionStorage.getItem(storageKey.value) || 'null')
    pending.value = data && typeof data.idempotency_key === 'string' && Number.isInteger(data.template_id) && Array.isArray(data.member_ids) && data.member_ids.length > 0 && data.member_ids.length <= 100 && typeof data.reason === 'string' ? data : null
  } catch { pending.value = null }
}
watch(scope, (_scope, previous) => {
  context++; channelVersion++; memberVersion++; previewVersion++
  preview.value = null; result.value = null; selected.value = []; templates.value = []; channels.value = []; members.value = []
  pending.value = null; readPending()
  if (previous !== undefined) emit('update:modelValue', false)
}, { immediate: true })
watch(allowed, value => { context++; preview.value = null; if (!value && !pending.value) emit('update:modelValue', false) })
watch(canRecover, value => { if (!value) emit('update:modelValue', false) })
watch(() => props.modelValue, async open => {
  context++; channelVersion++; memberVersion++; previewVersion++
  previewLoading.value = false; loading.value = false; membersLoading.value = false
  if (!open || !canRecover.value) return
  readPending()
  if (pending.value) { message.value = '上次发放结果尚未确认。请先查询结果；需要重试时将沿用原发放记录。'; return }
  if (!allowed.value) return
  result.value = null; message.value = ''; preview.value = null; reason.value = ''; keyword.value = ''
  selected.value = props.initialMembers.filter(formal).slice(0, 100)
  channelID.value = props.preselectedChannel || Number(props.preselectedTemplate?.channel_account_id) || undefined
  templateID.value = undefined; channels.value = []; templates.value = []; members.value = []
  const version = context
  loading.value = true
  try {
    const response = await request.get('/commerce/storefront-channels', config)
    if (!valid(version)) return
    channels.value = (response.data?.data || []).filter((item: any) => item.status === 'active')
    if (!channelID.value) channelID.value = Number(channels.value[0]?.id) || undefined
    if (!channels.value.some(item => Number(item.id) === channelID.value)) { message.value = '所选店铺渠道不可用，请检查渠道配置。'; return }
    await loadTemplates()
    if (!props.initialMembers.length) await loadMembers(1)
  } catch (error) { if (valid(version)) message.value = errorText(error, '店铺渠道加载失败，请关闭后重试') }
  finally { if (version === context) loading.value = false }
})
async function changeChannel() { invalidatePreview(); templateID.value = undefined; templates.value = []; await loadTemplates() }
async function loadTemplates() {
  const version = ++channelVersion, versionContext = context, requestedChannel = channelID.value
  invalidatePreview(); templates.value = []; templateID.value = undefined
  if (!requestedChannel) return
  try {
    const response = await request.get('/commerce/promotions/coupon-templates', { ...config, params: { channel_account_id: requestedChannel } })
    if (!valid(versionContext) || version !== channelVersion || requestedChannel !== channelID.value) return
    templates.value = (response.data?.data || []).filter((item: any) => item.status === 'active' && Number(item.channel_account_id) === requestedChannel)
    if (props.preselectedTemplate) {
      templateID.value = templates.value.some(item => Number(item.id) === Number(props.preselectedTemplate.id)) ? Number(props.preselectedTemplate.id) : undefined
      if (!templateID.value) message.value = '该优惠券模板已不可发放，请关闭后检查模板。'
    }
  } catch (error) { if (valid(versionContext) && version === channelVersion) message.value = errorText(error, '优惠券模板加载失败') }
}
function searchMembers() { void loadMembers(1) }
async function loadMembers(page = 1) {
  const version = ++memberVersion, versionContext = context, search = keyword.value
  membersLoading.value = true
  try {
    const response = await request.get('/members', { ...config, params: { status: 'active', keyword: search, page, page_size: 20 } })
    if (!valid(versionContext) || version !== memberVersion || search !== keyword.value) return
    members.value = response.data?.items || []; memberTotal.value = Number(response.data?.total || 0); memberPage.value = page
  } catch (error) { if (valid(versionContext) && version === memberVersion) message.value = errorText(error, '会员列表加载失败') }
  finally { if (version === memberVersion && versionContext === context) membersLoading.value = false }
}
async function previewGrant() {
  if (busy.value || !allowed.value || !canPreview.value || pending.value) return
  const versionContext = context, version = ++previewVersion
  const payload = { template_id: templateID.value, member_ids: selected.value.map(item => Number(item.id)) }
  previewLoading.value = true; message.value = ''; preview.value = null
  try {
    const response = await request.post(`${base}/preview`, payload, config)
    if (valid(versionContext) && version === previewVersion) preview.value = response.data
  } catch (error) { if (valid(versionContext) && version === previewVersion) message.value = errorText(error, '发放条件检查失败，请重试') }
  finally { if (versionContext === context && version === previewVersion) previewLoading.value = false }
}
function matchesAttempt(data: any, record: Attempt) {
  return data?.status === 'completed' && data.idempotency_key === record.idempotency_key
    && Number(data.template_id) === record.template_id
}
function complete(data: any, key: string, record: Attempt) {
  // Clear the durable attempt before notifying the parent; list refresh failure cannot undo issuance.
  sessionStorage.removeItem(key)
  if (pending.value?.idempotency_key === record.idempotency_key) {
    pending.value = null; result.value = data; message.value = ''; preview.value = null
    emit('completed', data)
  }
}
async function issue(retry: boolean) {
  if (!allowed.value || issuing.value || recovering.value) return
  if (!retry && (busy.value || !preview.value?.eligible || !canPreview.value || pending.value)) return
  if (!retry) {
    readPending()
    if (pending.value) { message.value = '已有一笔发放待确认，请先查询原记录。'; return }
  }
  const record: Attempt | null = retry ? pending.value : { template_id: Number(templateID.value), member_ids: selected.value.map(item => Number(item.id)), idempotency_key: crypto.randomUUID(), reason: reason.value.trim(), recipients: selected.value.map(item => ({ member_no: item.member_no, display_name: item.display_name })), template_name: selectedTemplate.value?.name }
  if (!record) return
  const key = storageKey.value, versionScope = scope.value
  try { sessionStorage.setItem(key, JSON.stringify(record)) } catch { message.value = '浏览器无法保存本次发放记录，请检查浏览器存储后再操作。'; return }
  pending.value = record; issuing.value = true; message.value = '正在提交，请稍候。'
  try {
    const response = await request.post(base, { template_id: record.template_id, member_ids: record.member_ids, idempotency_key: record.idempotency_key, reason: record.reason }, config)
    if (!matchesAttempt(response.data, record)) throw new Error('unconfirmed')
    if (versionScope === scope.value) complete(response.data, key, record)
    else sessionStorage.removeItem(key)
  } catch (error: any) {
    if (versionScope !== scope.value) return
    const status = error?.response?.status, code = error?.response?.data?.reason_code
    const definitelyRejected = !retry && ((status === 400 && ['MEMBER_COUPON_GRANT_INVALID', 'MEMBER_COUPON_GRANT_INELIGIBLE'].includes(code))
      || (status === 403 && code === 'MEMBER_COUPON_GRANT_FORBIDDEN'))
    if (definitelyRejected) {
      sessionStorage.removeItem(key); pending.value = null; invalidatePreview()
      message.value = errorText(error, '本次发放未受理，请调整后重新检查。')
    } else message.value = '发放结果暂未确认，请查询结果或重试原发放。重复操作原记录不会重复发券。'
  } finally { issuing.value = false }
}
async function recover() {
  if (!canRecover.value || !pending.value || issuing.value || recovering.value) return
  const record = pending.value, key = storageKey.value, versionScope = scope.value
  recovering.value = true
  try {
    const response = await request.get(`${base}/${encodeURIComponent(record.idempotency_key)}`, config)
    if (versionScope !== scope.value) return
    if (matchesAttempt(response.data, record)) complete(response.data, key, record)
    else message.value = '尚未确认发放结果，请稍后查询或重试原发放。'
  } catch (error: any) {
    if (versionScope === scope.value) message.value = error?.response?.status === 404 ? '暂未查到已完成记录，可继续查询或重试原发放。' : '暂时无法查询，请稍后查询；原发放记录已保留。'
  } finally { recovering.value = false }
}
onBeforeUnmount(() => { context++; channelVersion++; memberVersion++; previewVersion++ })
</script>

<style scoped>
.grant-form { display: grid; grid-template-columns: 1fr 1fr; gap: 8px 16px; margin-top: 16px; }
.grant-form .el-select { width: 100%; }
.template-summary { grid-column: 1 / -1; padding: 12px; background: var(--el-fill-color-light); border-radius: 8px; }
.template-summary p { margin: 6px 0; }
.selected-members, .member-search { display: flex; gap: 8px; flex-wrap: wrap; margin: 12px 0; }
.member-search .el-input { width: 280px; }
.preview-result { margin-top: 16px; }
@media (max-width: 640px) { .grant-form { grid-template-columns: 1fr; } }
</style>
