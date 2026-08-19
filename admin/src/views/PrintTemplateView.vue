<template>
  <section class="print-template-page">
    <header class="page-heading">
      <div class="page-heading-copy">
        <span class="eyebrow">PRINT CONTROL</span>
        <h2>门票打印模板</h2>
        <p>按景区和票种维护结构化打印内容。草稿预览后发布，已生成的打印任务永远使用当时的版本快照。</p>
      </div>
      <div class="page-actions">
        <el-button :icon="Refresh" circle title="刷新" :loading="loading" @click="loadWorkspace" />
        <el-button type="primary" :icon="Plus" @click="openCreate">新建模板</el-button>
      </div>
    </header>

    <section class="template-toolbar">
      <div class="toolbar-field">
        <span>景区</span>
        <el-select v-model="filters.scenicAreaID" clearable filterable placeholder="全部景区" @change="loadTemplates">
          <el-option v-for="area in scenicAreas" :key="area.id" :label="`${area.name} · ${area.code}`" :value="area.id" />
        </el-select>
      </div>
      <div class="toolbar-field">
        <span>票种覆盖</span>
        <el-select v-model="filters.productID" clearable filterable placeholder="全部覆盖范围" @change="loadTemplates">
          <el-option label="仅景区默认模板" :value="0" />
          <el-option v-for="product in products" :key="product.id" :label="product.name" :value="product.id" />
        </el-select>
      </div>
      <div class="toolbar-note"><el-icon><InfoFilled /></el-icon>未配置时系统会创建景区默认模板；未配置真实打印机时仍会明确失败。</div>
    </section>

    <el-table :data="templates" v-loading="loading" class="template-table" empty-text="当前还没有打印模板">
      <el-table-column label="模板" min-width="230">
        <template #default="{ row }"><div class="template-name"><strong>{{ row.name }}</strong><span>{{ scopeText(row) }}</span></div></template>
      </el-table-column>
      <el-table-column label="景区" min-width="160"><template #default="{ row }">{{ scenicName(row.scenic_area_id) }}</template></el-table-column>
      <el-table-column label="纸张" width="130"><template #default="{ row }">{{ row.paper_width_mm }} mm · {{ orientationText(row.orientation) }}</template></el-table-column>
      <el-table-column label="版本" width="110"><template #default="{ row }">{{ row.current_revision?.version ? `v${row.current_revision.version}` : '未发布' }}</template></el-table-column>
      <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ row.status === 'active' ? '已启用' : '已停用' }}</el-tag></template></el-table-column>
      <el-table-column label="草稿" width="100"><template #default="{ row }"><el-tag v-if="row.draft_revision" type="warning" effect="plain">待发布</el-tag><span v-else class="muted">—</span></template></el-table-column>
      <el-table-column label="操作" width="260" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="row.draft_revision" link type="warning" @click="publish(row)">发布</el-button>
          <el-button link type="primary" @click="showRevisions(row)">版本</el-button>
          <el-button link :type="row.status === 'active' ? 'danger' : 'success'" @click="toggleStatus(row)">{{ row.status === 'active' ? '停用' : '启用' }}</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="editor.visible" :title="editor.id ? '编辑打印模板' : '新建打印模板'" width="1180px" top="5vh" destroy-on-close :close-on-click-modal="false">
      <div class="template-editor">
        <div class="editor-form">
          <el-form label-position="top">
            <div class="form-grid">
              <el-form-item label="模板名称" required><el-input v-model="editor.name" maxlength="100" placeholder="例如：窗口标准票据" /></el-form-item>
              <el-form-item label="纸张宽度"><el-radio-group v-model="editor.paperWidthMM"><el-radio-button :label="58">58 mm</el-radio-button><el-radio-button :label="80">80 mm</el-radio-button></el-radio-group><div class="field-hint">按实际热敏纸宽度选择；横版只改变票面的方向，不改变 58/80 mm 纸宽。</div></el-form-item>
            </div>
            <div class="form-grid">
              <el-form-item label="打印方向"><el-radio-group v-model="editor.orientation"><el-radio-button label="portrait">竖版</el-radio-button><el-radio-button label="landscape">横版</el-radio-button></el-radio-group><div class="field-hint">横版会将票面宽度作为短边，服务端快照和后续打印适配器都会保留该方向。</div></el-form-item>
              <el-form-item label="适用景区" required><el-select v-model="editor.scenicAreaID" filterable class="w-full" :disabled="Boolean(editor.id)"><el-option v-for="area in scenicAreas" :key="area.id" :label="area.name" :value="area.id" /></el-select></el-form-item>
            </div>
            <div class="form-grid">
              <el-form-item label="票种覆盖"><el-select v-model="editor.productID" filterable clearable class="w-full" :disabled="Boolean(editor.id && editor.currentRevisionID)" placeholder="不选则作为景区默认模板"><el-option label="景区默认模板" :value="0" /><el-option v-for="product in productsForArea(editor.scenicAreaID)" :key="product.id" :label="product.name" :value="product.id" /></el-select></el-form-item>
              <el-form-item v-if="editor.productID" label="版本范围"><el-radio-group v-model="editor.revisionMode"><el-radio-button label="product">产品全部版本</el-radio-button><el-radio-button label="current">仅当前版本</el-radio-button></el-radio-group><div class="field-hint">产品全部版本的覆盖更稳定；指定当前版本适合临时版式，产品生成新 revision 后会回退到产品级覆盖。</div></el-form-item>
            </div>
          </el-form>

          <div class="block-heading"><div><span class="panel-kicker">CONTENT BLOCKS</span><h3>打印内容</h3></div><span class="block-count">{{ editor.definition.blocks.length }} / 64</span></div>
          <div class="block-list">
            <article v-for="(block, index) in editor.definition.blocks" :key="`${index}-${block.kind}`" class="block-row">
              <div class="block-index">{{ String(Number(index) + 1).padStart(2, '0') }}</div>
              <div class="block-main">
                <div class="block-line">
                  <el-select v-model="block.kind" class="kind-select" @change="normalizeBlock(block)"><el-option v-for="option in blockOptions" :key="option.value" :label="option.label" :value="option.value" /></el-select>
                  <el-select v-model="block.align" class="align-select"><el-option label="左对齐" value="left" /><el-option label="居中" value="center" /><el-option label="右对齐" value="right" /></el-select>
                  <el-input-number v-model="block.font_size" :min="8" :max="32" controls-position="right" class="font-size-input" />
                  <el-checkbox v-model="block.bold">粗体</el-checkbox>
                </div>
                <el-input v-if="isTextBlock(block.kind)" v-model="block.text" maxlength="500" placeholder="输入固定说明文字" />
                <div v-else class="block-description">{{ blockDescription(block.kind) }}</div>
              </div>
              <div class="block-actions"><el-button link :disabled="Number(index) === 0" @click="moveBlock(Number(index), -1)">上移</el-button><el-button link :disabled="Number(index) === editor.definition.blocks.length - 1" @click="moveBlock(Number(index), 1)">下移</el-button><el-button link type="danger" @click="removeBlock(Number(index))">删除</el-button></div>
            </article>
          </div>
          <el-button class="add-block-button" :icon="Plus" plain @click="addBlock">增加打印区块</el-button>
          <div class="editor-safety"><el-icon><Lock /></el-icon><span>只允许结构化字段；不支持任意 HTML、JavaScript、SQL 或渠道密钥。售价仅打印游客售价，不打印结算价。</span></div>
        </div>

        <aside class="preview-card">
          <div class="preview-header"><div><span class="panel-kicker">LIVE PREVIEW</span><h3>样例票据</h3></div><el-tag size="small" effect="plain">{{ editor.paperWidthMM }} mm · {{ orientationText(editor.orientation) }}</el-tag></div>
          <div class="paper-wrap"><div class="paper" :class="[editor.paperWidthMM === 80 ? 'paper-wide' : 'paper-narrow', editor.orientation === 'landscape' ? 'paper-landscape' : 'paper-portrait']">
            <div v-for="(block, index) in previewDocument.blocks" :key="`${index}-${block.kind}`" class="paper-block" :class="[`align-${block.align}`, { bold: block.bold, separator: block.separator }]" :style="{ fontSize: `${numberValue(block.font_size, 12) > 22 ? 22 : numberValue(block.font_size, 12) < 9 ? 9 : numberValue(block.font_size, 12)}px`, marginBottom: `${Math.max(2, numberValue(block.spacing, 0) * 3)}px` }">
              <template v-if="block.kind === 'qr_code' || block.kind === 'barcode'"><div class="code-placeholder"><span>{{ block.kind === 'qr_code' ? 'QR' : 'BAR' }}</span><small>{{ block.text }}</small></div></template>
              <template v-else>{{ block.text || ' ' }}</template>
            </div>
          </div></div>
          <el-alert type="info" :closable="false" show-icon title="预览使用样例游客和票码；正式打印内容由服务端按订单票据快照生成。" />
          <div v-if="editor.previewHash" class="preview-hash">样例内容哈希：{{ editor.previewHash.slice(0, 16) }}…</div>
        </aside>
      </div>
      <template #footer><el-button @click="editor.visible = false">取消</el-button><el-button :loading="editor.previewing" @click="refreshPreview">更新预览</el-button><el-button type="primary" :loading="editor.saving" @click="saveDraft">保存草稿</el-button></template>
    </el-dialog>

    <el-dialog v-model="revisionDialog.visible" title="模板版本历史" width="760px">
      <el-timeline v-loading="revisionDialog.loading">
        <el-timeline-item v-for="revision in revisionDialog.rows" :key="revision.id" :timestamp="formatTime(revision.created_at)" :type="revision.status === 'published' ? 'success' : revision.status === 'draft' ? 'warning' : 'info'">
          <div class="revision-row"><div><strong>v{{ revision.version }}</strong><el-tag size="small" effect="plain" class="ml-2">{{ revisionStatus(revision.status) }}</el-tag><p>{{ revision.definition?.blocks?.length || 0 }} 个区块 · {{ revision.definition?.paper_width_mm || 58 }} mm · {{ orientationText(revision.definition?.orientation) }} · {{ revision.definition_hash?.slice(0, 12) }}…</p></div><span>{{ revision.published_at ? `发布于 ${formatTime(revision.published_at)}` : '未发布' }}</span></div>
        </el-timeline-item>
      </el-timeline>
      <el-empty v-if="!revisionDialog.loading && !revisionDialog.rows.length" description="暂无版本" />
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { InfoFilled, Lock, Plus, Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'

type PrintBlock = { kind: string, text?: string, align: string, font_size: number, bold: boolean, spacing: number, separator?: boolean }
type Definition = { schema_version: number, paper_width_mm: number, orientation: 'portrait' | 'landscape', blocks: PrintBlock[] }

const blockOptions = [
  { value: 'scenic_name', label: '景区名称' }, { value: 'logo', label: 'Logo（预留）' }, { value: 'product_name', label: '票种名称' },
  { value: 'use_date', label: '使用日期' }, { value: 'validity', label: '有效期' }, { value: 'visitor_name', label: '游客姓名' },
  { value: 'visitor_phone_suffix', label: '手机号后四位' }, { value: 'order_no', label: '订单号' }, { value: 'ticket_code', label: '票码' },
  { value: 'qr_code', label: '二维码' }, { value: 'barcode', label: '条形码' }, { value: 'ticket_sequence', label: '票张序号' },
  { value: 'checkpoint_summary', label: '检票规则摘要' }, { value: 'price', label: '售价' }, { value: 'custom_text', label: '自定义说明' }, { value: 'footer_text', label: '底部说明' },
]
const blockDescriptions: Record<string, string> = { scenic_name: '读取订单履约景区名称', logo: '等待景区 Logo 资源配置', product_name: '读取售票时的票种名称快照', use_date: '读取订单明细使用日期', validity: '读取售票时的有效期快照', visitor_name: '读取票据游客姓名（若有）', visitor_phone_suffix: '仅显示手机号后四位', order_no: '读取销售订单号', ticket_code: '读取服务端生成的票码', qr_code: '由未来硬件适配器编码票码', barcode: '由未来硬件适配器编码票码', ticket_sequence: '显示本订单票张序号', checkpoint_summary: '读取售票时检票规则摘要', price: '读取游客售价，不包含结算价', custom_text: '打印固定说明', footer_text: '打印底部固定说明' }
const scenicAreas = ref<any[]>([])
const products = ref<any[]>([])
const templates = ref<any[]>([])
const loading = ref(false)
const filters = reactive<{ scenicAreaID: number | undefined, productID: number | undefined }>({ scenicAreaID: undefined, productID: undefined })
const editor = reactive<any>({ visible: false, id: 0, name: '', scenicAreaID: 0, productID: 0, productRevisionID: 0, currentRevisionID: 0, revisionMode: 'product', paperWidthMM: 58, orientation: 'portrait', printerProfile: 'escpos', definition: emptyDefinition(58, 'portrait'), previewDocument: { blocks: [] }, previewHash: '', saving: false, previewing: false })
const revisionDialog = reactive({ visible: false, loading: false, rows: [] as any[] })
const previewDocument = computed(() => editor.previewDocument || { blocks: [] })

function emptyDefinition(width: number, orientation: 'portrait' | 'landscape' = 'portrait'): Definition { return { schema_version: 1, paper_width_mm: width, orientation, blocks: [{ kind: 'scenic_name', align: 'center', font_size: 18, bold: true, spacing: 1 }, { kind: 'product_name', align: 'center', font_size: 15, bold: true, spacing: 1 }, { kind: 'ticket_code', align: 'left', font_size: 11, bold: true, spacing: 0 }, { kind: 'qr_code', align: 'center', font_size: 12, bold: false, spacing: 1 }, { kind: 'footer_text', align: 'center', font_size: 9, bold: false, spacing: 1, text: '请妥善保管票据，入园时出示二维码' }] } }
function orientationText(value: unknown) { return value === 'landscape' ? '横版' : '竖版' }
function cloneDefinition(value: any, width: number, orientation: 'portrait' | 'landscape' = 'portrait'): Definition { const source = value || emptyDefinition(width, orientation); const resolvedOrientation = source.orientation === 'landscape' ? 'landscape' : orientation; return { schema_version: 1, paper_width_mm: Number(source.paper_width_mm || width), orientation: resolvedOrientation, blocks: (source.blocks || []).map((block: any) => ({ kind: block.kind, text: block.text || '', align: block.align || 'left', font_size: Number(block.font_size || 12), bold: Boolean(block.bold), spacing: Number(block.spacing || 0), separator: Boolean(block.separator) })) } }
function normalizeBlock(block: PrintBlock) { block.text = isTextBlock(block.kind) ? block.text || '' : ''; block.align = block.align || 'left'; block.font_size = block.font_size || 12; block.spacing = block.spacing || 0 }
function isTextBlock(kind: string) { return kind === 'custom_text' || kind === 'footer_text' }
function blockDescription(kind: string) { return blockDescriptions[kind] || '服务端字段' }
function productsForArea(areaID: number) { return products.value.filter(product => !areaID || Number(product.scenic_area_id) === Number(areaID)) }
function scenicName(areaID: number) { return scenicAreas.value.find(area => Number(area.id) === Number(areaID))?.name || `景区 #${areaID}` }
function scopeText(row: any) { if (!row.product_id) return '景区默认模板'; return `${row.product_name || `票种 #${row.product_id}`}${row.product_revision_id ? ' · 指定版本' : ' · 全版本'}` }
function revisionStatus(status: string) { return ({ draft: '草稿', published: '已发布', retired: '已归档' } as Record<string, string>)[status] || status }
function formatTime(value: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '' }
function numberValue(value: unknown, fallback: number) { const parsed = Number(value); return Number.isFinite(parsed) ? parsed : fallback }

const loadWorkspace = async () => {
  loading.value = true
  try {
    const [areasResponse, productsResponse] = await Promise.all([request.get('/scenic-areas'), request.get('/products', { params: { type: 'online', product_kind: 'ticket', page: 1, page_size: 100 } })])
    scenicAreas.value = areasResponse.data.data || []
    products.value = productsResponse.data.data || []
    await loadTemplates()
  } finally { loading.value = false }
}
const loadTemplates = async () => {
  const response = await request.get('/print-templates', { params: { scenic_area_id: filters.scenicAreaID || undefined, product_id: filters.productID || undefined } })
  templates.value = response.data.data || []
}

const openCreate = () => {
  const areaID = Number(filters.scenicAreaID || scenicAreas.value[0]?.id || 0)
  Object.assign(editor, { visible: true, id: 0, name: '', scenicAreaID: areaID, productID: 0, productRevisionID: 0, currentRevisionID: 0, revisionMode: 'product', paperWidthMM: 58, orientation: 'portrait', printerProfile: 'escpos', definition: emptyDefinition(58, 'portrait'), previewDocument: { blocks: [] }, previewHash: '', saving: false })
  void refreshPreview()
}
const openEdit = (row: any) => {
  const definition = row.draft_definition || row.definition
  const orientation = row.orientation === 'landscape' || definition?.orientation === 'landscape' ? 'landscape' : 'portrait'
  Object.assign(editor, { visible: true, id: row.id, name: row.name, scenicAreaID: row.scenic_area_id, productID: row.product_id || 0, productRevisionID: row.product_revision_id || 0, currentRevisionID: row.current_revision_id || 0, revisionMode: row.product_revision_id ? 'current' : 'product', paperWidthMM: row.paper_width_mm || definition?.paper_width_mm || 58, orientation, printerProfile: row.printer_profile || 'escpos', definition: cloneDefinition(definition, row.paper_width_mm || 58, orientation), previewDocument: { blocks: [] }, previewHash: '', saving: false })
  void refreshPreview()
}
const addBlock = () => { if (editor.definition.blocks.length >= 64) return; editor.definition.blocks.push({ kind: 'custom_text', text: '请输入说明', align: 'left', font_size: 12, bold: false, spacing: 0 }) }
const removeBlock = (index: number) => { if (editor.definition.blocks.length <= 1) { ElMessage.warning('至少保留一个打印区块'); return } editor.definition.blocks.splice(index, 1) }
const moveBlock = (index: number, direction: number) => { const target = index + direction; if (target < 0 || target >= editor.definition.blocks.length) return; const [block] = editor.definition.blocks.splice(index, 1); editor.definition.blocks.splice(target, 0, block) }
const requestPayload = () => ({ id: editor.id || undefined, scenic_area_id: Number(editor.scenicAreaID), product_id: Number(editor.productID || 0), product_revision_id: editor.productID && editor.revisionMode === 'current' ? Number(products.value.find(product => Number(product.id) === Number(editor.productID))?.current_revision_id || 0) : 0, name: editor.name.trim(), paper_width_mm: Number(editor.paperWidthMM), orientation: editor.orientation, printer_profile: editor.printerProfile, definition: { ...editor.definition, schema_version: 1, paper_width_mm: Number(editor.paperWidthMM), orientation: editor.orientation } })
const refreshPreview = async () => {
  if (!editor.scenicAreaID || !editor.definition.blocks.length) return
  editor.previewing = true
  try {
    const response = await request.post('/print-templates/preview', requestPayload())
    editor.orientation = response.data.definition?.orientation === 'landscape' ? 'landscape' : 'portrait'
    editor.definition = cloneDefinition(response.data.definition, editor.paperWidthMM, editor.orientation)
    editor.previewDocument = response.data.document
    editor.previewHash = response.data.content_hash || ''
  } finally { editor.previewing = false }
}
const saveDraft = async () => {
  if (!editor.name.trim() || !editor.scenicAreaID) { ElMessage.warning('请填写模板名称并选择景区'); return }
  editor.saving = true
  try {
    const response = editor.id ? await request.put(`/print-templates/${editor.id}`, requestPayload()) : await request.post('/print-templates', requestPayload())
    editor.id = response.data.id
    editor.currentRevisionID = response.data.current_revision_id || 0
    editor.visible = false
    ElMessage.success('打印模板草稿已保存')
    await loadTemplates()
  } finally { editor.saving = false }
}
const publish = async (row: any) => {
  await ElMessageBox.confirm(`发布“${row.name}”的草稿版本？已生成的历史打印任务不会改变。`, '发布打印模板', { type: 'warning' })
  await request.post(`/print-templates/${row.id}/publish`)
  ElMessage.success('打印模板已发布')
  await loadTemplates()
}
const toggleStatus = async (row: any) => {
  const next = row.status === 'active' ? 'disabled' : 'active'
  await request.patch(`/print-templates/${row.id}/status`, { status: next })
  ElMessage.success(next === 'active' ? '模板已启用' : '模板已停用')
  await loadTemplates()
}
const showRevisions = async (row: any) => {
  revisionDialog.visible = true
  revisionDialog.loading = true
  try { revisionDialog.rows = (await request.get(`/print-templates/${row.id}/revisions`)).data.data || [] } finally { revisionDialog.loading = false }
}

onMounted(() => { void loadWorkspace() })
</script>

<style scoped>
.print-template-page { max-width: 1500px; margin: 0 auto; }
.template-toolbar { display: flex; align-items: end; gap: 18px; padding: 16px 18px; margin-bottom: 18px; border: 1px solid #e8edf5; border-radius: 14px; background: linear-gradient(120deg, #fbfdff, #f5f8fc); }
.toolbar-field { display: grid; gap: 6px; min-width: 210px; color: #64748b; font-size: 12px; font-weight: 600; }
.toolbar-note { display: flex; align-items: center; gap: 6px; margin-left: auto; color: #64748b; font-size: 12px; }
.template-name { display: grid; gap: 4px; }.template-name span, .muted, .field-hint, .block-description { color: #94a3b8; font-size: 12px; }
.template-editor { display: grid; grid-template-columns: minmax(0, 1.35fr) minmax(310px, .65fr); gap: 24px; }
.editor-form { min-width: 0; }.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }.w-full { width: 100%; }
.block-heading, .preview-header { display: flex; align-items: center; justify-content: space-between; margin: 8px 0 12px; }.block-heading h3, .preview-header h3 { margin: 3px 0 0; font-size: 16px; color: #0f172a; }.panel-kicker { color: #94a3b8; font-size: 10px; letter-spacing: .12em; font-weight: 700; }.block-count { color: #64748b; font-size: 12px; }
.block-list { display: grid; gap: 8px; max-height: 440px; overflow: auto; padding-right: 4px; }.block-row { display: grid; grid-template-columns: 34px minmax(0, 1fr) auto; gap: 10px; align-items: center; padding: 10px 12px; border: 1px solid #e6ebf2; border-radius: 10px; background: #fff; }.block-index { color: #94a3b8; font-family: ui-monospace, monospace; font-size: 11px; }.block-main { min-width: 0; display: grid; gap: 7px; }.block-line { display: flex; align-items: center; gap: 8px; }.kind-select { min-width: 152px; flex: 1; }.align-select { width: 92px; }.font-size-input { width: 82px; }.block-actions { white-space: nowrap; }.add-block-button { width: 100%; margin-top: 10px; border-style: dashed; }.editor-safety { display: flex; gap: 8px; align-items: flex-start; margin-top: 14px; padding: 10px 12px; color: #64748b; font-size: 12px; line-height: 1.5; background: #f8fafc; border-radius: 9px; }
.preview-card { min-width: 0; padding: 16px; border: 1px solid #e6ebf2; border-radius: 14px; background: #f8fafc; }.paper-wrap { display: flex; justify-content: center; padding: 18px 0; overflow-x: auto; }.paper { padding: 22px 15px; color: #111827; background: #fff; box-shadow: 0 10px 28px rgba(15, 23, 42, .12); transition: width .2s ease, min-height .2s ease; }.paper-narrow { width: 238px; }.paper-wide { width: 310px; }.paper-portrait { min-height: 470px; }.paper-landscape.paper-narrow { width: 470px; min-height: 238px; }.paper-landscape.paper-wide { width: 560px; min-height: 310px; }.paper-block { line-height: 1.3; word-break: break-all; }.align-left { text-align: left; }.align-center { text-align: center; }.align-right { text-align: right; }.paper-block.bold { font-weight: 700; }.paper-block.separator { border-top: 1px dashed #94a3b8; padding-top: 5px; }.code-placeholder { display: grid; justify-items: center; gap: 4px; padding: 10px; border: 1px dashed #cbd5e1; color: #0f172a; }.code-placeholder span { font-size: 25px; font-weight: 800; letter-spacing: .12em; }.code-placeholder small { color: #64748b; font-family: ui-monospace, monospace; font-size: 10px; }.preview-hash { margin-top: 12px; color: #94a3b8; font-size: 10px; font-family: ui-monospace, monospace; }.revision-row { display: flex; justify-content: space-between; gap: 16px; }.revision-row p { margin: 6px 0 0; color: #94a3b8; font-size: 12px; }
@media (max-width: 1000px) { .template-editor { grid-template-columns: 1fr; }.preview-card { order: -1; }.template-toolbar { flex-wrap: wrap; }.toolbar-note { width: 100%; margin-left: 0; } }
@media (max-width: 620px) { .form-grid { grid-template-columns: 1fr; }.block-row { grid-template-columns: 26px minmax(0, 1fr); }.block-actions { grid-column: 2; }.block-line { flex-wrap: wrap; }.kind-select { width: 100%; flex-basis: 100%; } }
</style>
