<template>
  <el-dialog v-model="visible" title="随机立减" width="720px" :close-on-click-modal="false">
    <div v-loading="loading" class="min-h-48">
      <el-alert
        type="info"
        :closable="false"
        title="游客进店自动获得限时随机立减，下一笔购买参与商品的订单自动抵扣一次。"
      />
      <el-alert v-if="loadError" class="mt-3" type="error" :closable="false" :title="loadError">
        <template #default><el-button link type="primary" :loading="loading" @click="load">重新加载</el-button></template>
      </el-alert>

      <template v-if="loaded">
        <el-form class="mt-4" label-position="top">
          <el-form-item label="活动状态">
            <el-switch v-model="form.enabled" active-text="启用随机立减" inactive-text="关闭活动" :disabled="!canWrite || saving" />
          </el-form-item>
          <div class="grid grid-cols-1 gap-x-4 md:grid-cols-2">
            <el-form-item label="随机立减最低金额（元）">
              <el-input-number v-model="form.minDiscountYuan" :min="form.enabled ? 0.01 : 0" :precision="2" :step="1" controls-position="right" class="w-full" :disabled="!canWrite || saving" />
            </el-form-item>
            <el-form-item label="随机立减最高金额（元）">
              <el-input-number v-model="form.maxDiscountYuan" :min="form.enabled ? 0.01 : 0" :precision="2" :step="1" controls-position="right" class="w-full" :disabled="!canWrite || saving" />
            </el-form-item>
            <el-form-item label="立减有效期（分钟）">
              <el-input-number v-model="form.validityMinutes" :min="form.enabled ? 1 : 0" :step="1" controls-position="right" class="w-full" :disabled="!canWrite || saving" />
            </el-form-item>
            <el-form-item label="再次获得间隔（天）">
              <el-input-number v-model="form.cooldownDays" :min="0" :step="1" controls-position="right" class="w-full" :disabled="!canWrite || saving" />
            </el-form-item>
          </div>
          <el-form-item label="参与商品">
            <el-checkbox-group v-model="form.mappingIDs" class="w-full">
              <div v-for="product in products" :key="product.id" class="mb-2 flex items-center justify-between gap-3 rounded border border-gray-200 px-3 py-2">
                <el-checkbox
                  :label="product.id"
                  :disabled="!canWrite || saving || (!product.eligible && !form.mappingIDs.includes(product.id))"
                >
                  {{ product.name }}
                </el-checkbox>
                <div class="flex shrink-0 items-center gap-2">
                  <span class="text-sm text-gray-500">¥{{ cents(product.price_cents) }}</span>
                  <el-tag :type="product.eligible ? 'success' : 'warning'" effect="plain">{{ product.eligible ? '可参与' : '当前不可参与' }}</el-tag>
                </div>
              </div>
            </el-checkbox-group>
            <p class="mt-2 text-xs leading-5 text-gray-500">启用前请取消勾选当前不可参与的商品；关闭活动时可保留原选择。</p>
          </el-form-item>
        </el-form>
        <el-alert class="mt-2" type="warning" :closable="false" title="关闭活动或移除商品后，已提交订单仍保留优惠价。再次获得间隔从上次获得时起算，未使用而过期也不重置。成功支付后退款不恢复立减，优惠成本由商家承担。" />
      </template>
    </div>
    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
      <el-button v-if="canWrite" type="primary" :loading="saving" :disabled="!loaded || loading || !canSave" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

type Product = {
  id: number
  name: string
  price_cents: number
  eligible: boolean
}

const props = defineProps<{ modelValue: boolean; accountId: number | null; canWrite: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const visible = computed({ get: () => props.modelValue, set: (value: boolean) => emit('update:modelValue', value) })
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const loadError = ref('')
const products = ref<Product[]>([])
const form = reactive({ enabled: false, minDiscountYuan: 0, maxDiscountYuan: 0, validityMinutes: 0, cooldownDays: 0, mappingIDs: [] as number[] })
let session = 0

const isCurrent = (requestSession: number, accountID: number) => visible.value && session === requestSession && props.accountId === accountID
const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const hasEnabledValidValues = computed(() =>
  Number.isFinite(form.minDiscountYuan) &&
  Number.isFinite(form.maxDiscountYuan) &&
  Number.isInteger(form.validityMinutes) &&
  Number.isInteger(form.cooldownDays) &&
  form.minDiscountYuan > 0 &&
  form.maxDiscountYuan >= form.minDiscountYuan &&
  form.validityMinutes > 0 &&
  form.cooldownDays >= 0 &&
  form.mappingIDs.length > 0 &&
  form.mappingIDs.every(id => products.value.some(product => product.id === id && product.eligible)),
)
const canSave = computed(() => !form.enabled || hasEnabledValidValues.value)

const load = async () => {
  const accountID = props.accountId
  if (!visible.value || !accountID) return
  const requestSession = ++session
  loading.value = true
  saving.value = false
  loaded.value = false
  loadError.value = ''
  products.value = []
  Object.assign(form, { enabled: false, minDiscountYuan: 0, maxDiscountYuan: 0, validityMinutes: 0, cooldownDays: 0, mappingIDs: [] })
  try {
    const response = await request.get(`/channel-accounts/${accountID}/instant-discount`, { skipErrorToast: true } as any)
    if (!isCurrent(requestSession, accountID)) return
    const config = response.data || {}
    Object.assign(form, {
      enabled: Boolean(config.enabled),
      minDiscountYuan: Number(config.min_discount_cents || 0) / 100,
      maxDiscountYuan: Number(config.max_discount_cents || 0) / 100,
      validityMinutes: Number(config.validity_minutes || 0),
      cooldownDays: Number(config.cooldown_days || 0),
      mappingIDs: Array.isArray(config.mapping_ids) ? config.mapping_ids.map(Number) : [],
    })
    products.value = Array.isArray(config.products) ? config.products.map((product: Product) => ({
      id: Number(product.id),
      name: product.name,
      price_cents: Number(product.price_cents || 0),
      eligible: Boolean(product.eligible),
    })) : []
    loaded.value = true
  } catch {
    if (!isCurrent(requestSession, accountID)) return
    loadError.value = '随机立减配置暂时无法加载，请重试'
  } finally {
    if (isCurrent(requestSession, accountID)) loading.value = false
  }
}

const save = async () => {
  const accountID = props.accountId
  if (!props.canWrite || !loaded.value || saving.value || !accountID || !visible.value || !canSave.value) return
  const requestSession = session
  saving.value = true
  try {
    await request.put(`/channel-accounts/${accountID}/instant-discount`, {
      enabled: form.enabled,
      min_discount_cents: Math.round(form.minDiscountYuan * 100),
      max_discount_cents: Math.round(form.maxDiscountYuan * 100),
      validity_minutes: form.validityMinutes,
      cooldown_days: form.cooldownDays,
      mapping_ids: form.mappingIDs,
    })
    if (!isCurrent(requestSession, accountID)) return
    ElMessage.success(form.enabled ? '随机立减活动已保存' : '随机立减活动已关闭并保存')
    visible.value = false
  } finally {
    if (isCurrent(requestSession, accountID)) saving.value = false
  }
}

watch(() => [visible.value, props.accountId] as const, ([open]) => {
  if (open) void load()
  else ++session
}, { immediate: true })
</script>
