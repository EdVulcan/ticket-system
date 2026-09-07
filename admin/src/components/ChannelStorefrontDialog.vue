<template>
  <el-dialog v-model="visible" title="商城图片" width="560px" :close-on-click-modal="false">
    <div v-loading="loading" class="min-h-32">
      <el-alert type="info" :closable="false" title="支持 JPG、PNG，文件不超过 5 MB。可选用约 2:1 的横幅图；留空将隐藏商城头图。" />
      <el-alert v-if="loadError" class="mt-3" type="error" :closable="false" :title="loadError">
        <template #default><el-button link type="primary" :loading="loading" @click="load">重新加载</el-button></template>
      </el-alert>
      <div class="mt-4 rounded border border-gray-200 bg-gray-50 p-3">
        <el-image
          v-if="draftImageURL"
          :src="draftImageURL"
          :preview-src-list="[draftImageURL]"
          fit="cover"
          class="h-44 w-full rounded border border-gray-200 bg-white"
          preview-teleported
        />
        <div v-else class="flex h-44 items-center justify-center rounded border border-dashed border-gray-300 bg-white text-sm text-gray-500">未设置商城图片</div>
        <template v-if="canWrite">
          <div class="mt-3 flex flex-wrap items-center gap-2">
            <el-upload
              ref="uploadRef"
              :auto-upload="false"
              :show-file-list="false"
              accept="image/jpeg,image/png"
              :disabled="!loaded || uploading || saving"
              :on-change="uploadImage"
            >
              <el-button :icon="UploadFilled" :loading="uploading">{{ draftImageURL ? '替换图片' : '选择图片' }}</el-button>
            </el-upload>
            <el-button v-if="draftImageURL" type="danger" plain :disabled="!loaded || uploading || saving" @click="draftImageURL = ''">移除</el-button>
          </div>
          <p class="mt-2 text-xs leading-5 text-gray-500">上传仅更新当前草稿；请点击保存后才会应用到商城。</p>
        </template>
      </div>
    </div>
    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
      <el-button v-if="canWrite" type="primary" :loading="saving" :disabled="!loaded || loading || uploading" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { UploadFilled } from '@element-plus/icons-vue'
import { ElMessage, type UploadFile, type UploadInstance } from 'element-plus'
import request from '@/utils/request'

const props = defineProps<{ modelValue: boolean; accountId: number | null; canWrite: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const visible = computed({ get: () => props.modelValue, set: (value: boolean) => emit('update:modelValue', value) })
const loading = ref(false)
const uploading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const loadError = ref('')
const savedImageURL = ref('')
const draftImageURL = ref('')
const uploadRef = ref<UploadInstance>()
let session = 0

const isCurrent = (requestSession: number, accountID: number) => visible.value && session === requestSession && props.accountId === accountID

const load = async () => {
  const accountID = props.accountId
  if (!visible.value || !accountID) return
  const requestSession = ++session
  loading.value = true
  uploading.value = false
  saving.value = false
  loaded.value = false
  loadError.value = ''
  savedImageURL.value = ''
  draftImageURL.value = ''
  try {
    const response = await request.get(`/channel-accounts/${accountID}/storefront`)
    if (!isCurrent(requestSession, accountID)) return
    savedImageURL.value = response.data.image_url || ''
    draftImageURL.value = savedImageURL.value
    loaded.value = true
  } catch {
    if (!isCurrent(requestSession, accountID)) return
    loadError.value = '商城图片暂时无法加载，请重试'
  } finally {
    if (isCurrent(requestSession, accountID)) loading.value = false
  }
}

watch(() => [visible.value, props.accountId] as const, ([open]) => {
  if (open) void load()
  else ++session
}, { immediate: true })

const uploadImage = async (file: UploadFile) => {
  const accountID = props.accountId
  if (!props.canWrite || !loaded.value || uploading.value || saving.value || !accountID || !file.raw || !visible.value) return
  if (!['image/jpeg', 'image/png'].includes(file.raw.type)) {
    ElMessage.warning('商城图片仅支持 JPG 或 PNG 格式')
    uploadRef.value?.clearFiles()
    return
  }
  if (file.raw.size > 5 * 1024 * 1024) {
    ElMessage.warning('商城图片不能超过 5 MB')
    uploadRef.value?.clearFiles()
    return
  }
  const requestSession = session
  uploading.value = true
  try {
    const form = new FormData()
    form.append('image', file.raw)
    const response = await request.post(`/channel-accounts/${accountID}/storefront-image`, form, { timeout: 30_000 })
    if (!isCurrent(requestSession, accountID)) return
    draftImageURL.value = response.data.image_url || draftImageURL.value
    ElMessage.success('商城图片已上传，请保存后应用')
  } catch {
    // The request utility reports the upload error. Leave the saved draft intact.
  } finally {
    if (isCurrent(requestSession, accountID)) uploading.value = false
    uploadRef.value?.clearFiles()
  }
}

const save = async () => {
  const accountID = props.accountId
  if (!props.canWrite || !loaded.value || uploading.value || saving.value || !accountID || !visible.value) return
  const requestSession = session
  saving.value = true
  try {
    await request.put(`/channel-accounts/${accountID}/storefront`, { image_url: draftImageURL.value })
    if (!isCurrent(requestSession, accountID)) return
    savedImageURL.value = draftImageURL.value
    ElMessage.success(draftImageURL.value ? '商城图片已保存' : '商城图片已清除')
  } catch {
    // The request utility reports the save error. Keep the draft for an explicit retry.
  } finally {
    if (isCurrent(requestSession, accountID)) saving.value = false
  }
}
</script>
