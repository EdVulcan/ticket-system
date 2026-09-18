<template>
  <section class="commerce-page" data-testid="commerce-workspace">
    <header class="page-heading commerce-heading">
      <div class="page-heading-copy">
        <div class="section-kicker">商业经营</div>
        <h1>{{ currentDomainLabel }}工作台</h1>
        <p>独立管理商品、库存与履约地点，不影响景区票务产品。</p>
      </div>
      <div class="page-actions">
        <el-tag :type="isCurrentDomainActive ? 'success' : 'warning'" effect="plain">
          {{ capabilityStatusLabel }}
        </el-tag>
        <el-button :icon="Refresh" :loading="loading" @click="refreshWorkspace">刷新</el-button>
        <el-button v-if="canWrite" type="primary" :icon="Plus" @click="openProductDialog()">新增商品</el-button>
      </div>
    </header>

    <div v-if="configuredDomains.length > 1" class="domain-switcher" aria-label="商业能力">
      <el-radio-group :model-value="currentDomain" @change="switchDomain">
        <el-radio-button v-for="domain in configuredDomains" :key="domain" :label="domain">
          {{ businessTypeLabel(domain) }}
        </el-radio-button>
      </el-radio-group>
    </div>

    <el-alert
      v-if="isCurrentDomainConfigured && !isCurrentDomainActive"
      class="capability-alert"
      type="warning"
      :closable="false"
      title="该商业能力已暂停，仅保留历史查看。"
    />
    <el-alert
      v-else-if="!canWrite && isCurrentDomainActive"
      class="capability-alert"
      type="info"
      :closable="false"
      title="当前账号为只读权限。"
    />
    <el-alert v-if="loadError" class="capability-alert" type="error" :closable="false" :title="loadError" />

    <div v-if="isCurrentDomainConfigured" class="commerce-workspace">
      <el-tabs v-model="activeTab" class="workspace-tabs" @tab-change="handleTabChange">
        <el-tab-pane label="商品与 SKU" name="products">
          <section class="workspace-section">
            <div class="section-toolbar">
              <div>
                <h2>商品目录</h2>
                <p class="muted">{{ products.length }} 个{{ currentDomainLabel }}商品</p>
              </div>
              <el-button v-if="canWrite" type="primary" plain :icon="Plus" @click="openProductDialog()">新增商品</el-button>
            </div>

            <div class="filter-toolbar commerce-filter-bar">
              <el-input
                v-model="productSearch"
                class="commerce-search"
                clearable
                placeholder="搜索商品名称或副标题"
                :prefix-icon="Search"
                @keyup.enter="loadProducts"
                @clear="loadProducts"
              />
              <el-select v-model="productStatus" class="status-filter" clearable placeholder="全部状态" @change="loadProducts">
                <el-option label="全部状态" value="" />
                <el-option label="草稿" value="draft" />
                <el-option label="上架中" value="online" />
                <el-option label="已下架" value="offline" />
              </el-select>
              <el-button :icon="Refresh" @click="resetProductFilters">重置</el-button>
            </div>

            <el-table v-loading="loading" :data="products" class="commerce-table" border stripe>
              <el-table-column label="商品" min-width="240">
                <template #default="{ row }">
                  <div class="primary-cell">{{ row.name }}</div>
                  <div v-if="row.short_title" class="secondary-cell">{{ row.short_title }}</div>
                </template>
              </el-table-column>
              <el-table-column prop="category_name" label="分类" min-width="120">
                <template #default="{ row }">{{ row.category_name || '未分类' }}</template>
              </el-table-column>
              <el-table-column label="SKU" min-width="210">
                <template #default="{ row }">
                  <div>{{ (row.skus || []).length }} 个 SKU</div>
                  <div class="secondary-cell">{{ priceSummary(row) }}</div>
                </template>
              </el-table-column>
              <el-table-column label="状态" width="110" align="center">
                <template #default="{ row }">
                  <el-tag :type="productStatusType(row.status)" effect="plain">{{ productStatusLabel(row.status) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="250" fixed="right" align="right">
                <template #default="{ row }">
                  <el-button link type="primary" @click="openProductDetail(row)">管理 SKU / 规格</el-button>
                  <el-button v-if="canWrite" link :type="row.status === 'online' ? 'danger' : 'success'" @click="toggleProductStatus(row)">
                    {{ row.status === 'online' ? '下架' : '上架' }}
                  </el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无商品" :image-size="72" /></template>
            </el-table>
          </section>
        </el-tab-pane>

        <el-tab-pane label="履约地点" name="locations">
          <section class="workspace-section">
            <div class="section-toolbar">
              <div>
                <h2>履约地点</h2>
                <p class="muted">{{ locations.length }} 个{{ currentDomainLabel }}地点</p>
              </div>
              <el-button v-if="canWrite" type="primary" :icon="Plus" @click="openLocationDialog()">新增地点</el-button>
            </div>
            <el-table v-loading="loading" :data="locations" class="commerce-table" border>
              <el-table-column prop="name" label="名称" min-width="240" />
              <el-table-column label="类型" width="150">
                <template #default="{ row }">{{ locationTypeLabel(row.location_type) }}</template>
              </el-table-column>
              <el-table-column label="状态" width="110" align="center">
                <template #default="{ row }">
                  <el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ locationStatusLabel(row.status) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="130" fixed="right" align="right">
                <template #default="{ row }">
                  <el-button v-if="canWrite" link :type="row.status === 'active' ? 'danger' : 'success'" @click="toggleLocationStatus(row)">
                    {{ row.status === 'active' ? '停用' : '启用' }}
                  </el-button>
                  <span v-else class="secondary-cell">只读</span>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无履约地点" :image-size="72" /></template>
            </el-table>
          </section>
        </el-tab-pane>

        <el-tab-pane label="库存" name="inventory">
          <section class="workspace-section">
            <div class="section-toolbar">
              <div>
                <h2>库存台账</h2>
                <p class="muted">按 SKU 与履约地点查看库存事实</p>
              </div>
              <el-button v-if="canWrite" type="primary" :icon="Plus" @click="openInventoryDialog()">设置库存</el-button>
            </div>
            <el-table v-loading="loading" :data="inventoryRows" class="commerce-table" border>
              <el-table-column label="SKU" min-width="250">
                <template #default="{ row }">
                  <div class="primary-cell">{{ skuDisplayName(row.sku_id) }}</div>
                  <div class="secondary-cell">{{ skuDisplayCode(row.sku_id) }}</div>
                </template>
              </el-table-column>
              <el-table-column label="履约地点" min-width="180">
                <template #default="{ row }">{{ locationDisplayName(row.location_id) }}</template>
              </el-table-column>
              <el-table-column prop="available_qty" label="可用" width="100" align="right" />
              <el-table-column prop="reserved_qty" label="预留" width="100" align="right" />
              <el-table-column prop="sold_qty" label="已售" width="100" align="right" />
              <el-table-column prop="released_qty" label="已释放" width="100" align="right" />
              <el-table-column prop="version" label="版本" width="90" align="right" />
              <el-table-column label="操作" width="190" fixed="right" align="right">
                <template #default="{ row }">
                  <el-button v-if="canWrite" link type="primary" @click="openInventoryDialog(row)">设置可用量</el-button>
                  <el-button v-if="canWrite" link type="warning" @click="openAdjustmentDialog(row)">调整</el-button>
                  <span v-if="!canWrite" class="secondary-cell">只读</span>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无库存记录" :image-size="72" /></template>
            </el-table>
          </section>
        </el-tab-pane>

        <el-tab-pane v-if="canOrdersRead" label="订单" name="orders">
          <section class="workspace-section">
            <div class="section-toolbar">
              <div>
                <h2>订单管理</h2>
                <p class="muted">查看{{ currentDomainLabel }}订单，处理售后与履约状态</p>
              </div>
              <el-button :icon="Refresh" :loading="orderLoading" @click="loadOrders">刷新</el-button>
            </div>
            <div class="filter-toolbar commerce-filter-bar">
              <el-input v-model="orderSearch" class="commerce-search" clearable placeholder="搜索订单号、联系人或手机号" :prefix-icon="Search" @keyup.enter="loadOrders" @clear="loadOrders" />
              <el-select v-model="orderPaymentStatus" class="status-filter" clearable placeholder="支付状态" @change="loadOrders">
                <el-option label="全部支付状态" value="" />
                <el-option label="待支付" value="unpaid" />
                <el-option label="支付中" value="pending" />
                <el-option label="已支付" value="paid" />
                <el-option label="支付失败" value="failed" />
                <el-option label="已退款" value="refunded" />
              </el-select>
              <el-select v-model="orderFulfillmentStatus" class="status-filter" clearable placeholder="履约状态" @change="loadOrders">
                <el-option label="全部履约状态" value="" />
                <el-option v-for="option in fulfillmentStatusOptions" :key="option.value" :label="option.label" :value="option.value" />
              </el-select>
              <el-select v-model="orderRefundStatus" class="status-filter" clearable placeholder="售后状态" @change="loadOrders">
                <el-option label="全部售后状态" value="" />
                <el-option label="无售后" value="none" />
                <el-option label="退款申请中" value="requested" />
                <el-option label="退款处理中" value="processing" />
                <el-option label="已退款" value="refunded" />
                <el-option label="已拒绝" value="rejected" />
              </el-select>
              <el-button :icon="Refresh" @click="resetOrderFilters">重置</el-button>
            </div>
            <el-table v-loading="orderLoading" :data="orders" class="commerce-table" border stripe>
              <el-table-column label="订单" min-width="220">
                <template #default="{ row }">
                  <div class="primary-cell">{{ row.order_no }}</div>
                  <div class="secondary-cell">{{ formatDate(row.created_at) }}</div>
                </template>
              </el-table-column>
              <el-table-column label="商品" min-width="240">
                <template #default="{ row }">
                  <div v-for="item in row.items || []" :key="item.id" class="order-item-line">
                    <span>{{ item.product_name }}</span><span class="secondary-cell">× {{ item.quantity }}</span>
                  </div>
                </template>
              </el-table-column>
              <el-table-column label="联系人" min-width="150">
                <template #default="{ row }"><div>{{ row.contact_name || '未填写' }}</div><div class="secondary-cell">{{ row.contact_phone || '-' }}</div></template>
              </el-table-column>
              <el-table-column label="金额" width="110" align="right"><template #default="{ row }">¥{{ money(row.total_amount_cents) }}</template></el-table-column>
              <el-table-column label="支付" width="100" align="center"><template #default="{ row }"><el-tag :type="paymentStatusType(row.payment_status)" effect="plain">{{ paymentStatusLabel(row.payment_status) }}</el-tag></template></el-table-column>
              <el-table-column label="履约" width="120" align="center"><template #default="{ row }"><el-tag :type="fulfillmentStatusType(row.fulfillment_status)" effect="plain">{{ fulfillmentStatusLabel(row.fulfillment_status) }}</el-tag></template></el-table-column>
              <el-table-column label="售后" width="120" align="center"><template #default="{ row }"><el-tag :type="refundStatusType(row.refund_status)" effect="plain">{{ refundStatusLabel(row.refund_status) }}</el-tag></template></el-table-column>
              <el-table-column label="操作" width="250" fixed="right" align="right">
                <template #default="{ row }">
                  <el-button link type="primary" @click="openOrderDetail(row)">详情</el-button>
                  <el-button v-if="canRequestRefund(row)" link type="warning" :loading="orderActionID === row.id" @click="requestOrderRefund(row)">申请退款</el-button>
                  <el-tag v-if="orderRefundRequest(row)" type="warning" effect="plain">等待支付渠道确认</el-tag>
                  <el-button v-if="canAdvanceFulfillment(row)" link type="primary" :loading="orderActionID === row.id" @click="advanceFulfillment(row)">推进履约</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无商业订单" :image-size="72" /></template>
            </el-table>
          </section>
        </el-tab-pane>
      </el-tabs>
    </div>

    <el-empty v-else description="当前商户未配置商业能力" :image-size="96" />

    <el-dialog
      v-model="productDialogVisible"
      :title="`新增${currentDomainLabel}商品`"
      width="min(900px, calc(100vw - 32px))"
      top="5vh"
      :close-on-click-modal="false"
      destroy-on-close
    >
      <el-form :model="productForm" label-position="top" class="commerce-form">
        <div class="form-grid">
          <el-form-item label="商品名称" required>
            <el-input v-model="productForm.name" maxlength="160" placeholder="请输入商品名称" />
          </el-form-item>
          <el-form-item label="分类">
            <el-input v-model="productForm.category_name" maxlength="80" placeholder="例如：套餐、饮品、日用品" />
          </el-form-item>
          <el-form-item label="副标题">
            <el-input v-model="productForm.short_title" maxlength="80" placeholder="可选" />
          </el-form-item>
          <el-form-item label="初始状态">
            <el-select v-model="productForm.status" class="full-width">
              <el-option label="草稿" value="draft" />
              <el-option label="上架中" value="online" />
              <el-option label="已下架" value="offline" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item label="商品描述">
          <el-input v-model="productForm.description" type="textarea" :rows="3" maxlength="2000" show-word-limit placeholder="可选" />
        </el-form-item>

        <div class="form-section-heading">
          <div><strong>初始 SKU</strong><span>至少添加一个 SKU</span></div>
          <el-button plain type="primary" :icon="Plus" @click="addProductSku">新增 SKU</el-button>
        </div>
        <div class="sku-form-list">
          <div v-for="(sku, index) in productForm.skus" :key="sku.key" class="sku-form-row">
            <el-input v-model="sku.sku_code" class="sku-code-input" maxlength="80" placeholder="SKU 编码" />
            <el-input v-model="sku.name" class="sku-name-input" maxlength="160" placeholder="SKU 名称" />
            <el-input-number v-model="sku.original_price" class="sku-price-input" :min="0" :precision="2" :controls="false" placeholder="原价" />
            <el-input-number v-model="sku.price" class="sku-price-input" :min="0" :precision="2" :controls="false" placeholder="售价" />
            <el-button
              circle
              text
              type="danger"
              :icon="Delete"
              :disabled="productForm.skus.length === 1"
              title="移除 SKU"
              @click="removeProductSku(index)"
            />
          </div>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="productDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveProduct">保存商品</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="productDetailVisible" title="商品详情" width="min(980px, calc(100vw - 32px))" top="4vh" destroy-on-close>
      <div v-if="detailProduct" v-loading="detailLoading" class="detail-workspace">
        <div class="detail-header">
          <div>
            <h2>{{ detailProduct.name }}</h2>
            <p class="muted">{{ detailProduct.category_name || '未分类' }} · {{ productStatusLabel(detailProduct.status) }}</p>
          </div>
          <el-tag :type="productStatusType(detailProduct.status)" effect="plain">{{ productStatusLabel(detailProduct.status) }}</el-tag>
        </div>

        <section class="detail-section">
          <div class="detail-section-heading">
            <div><h3>SKU</h3><span>{{ (detailProduct.skus || []).length }} 个</span></div>
            <el-button v-if="canWrite" plain type="primary" :icon="Plus" @click="openSkuDialog()">新增 SKU</el-button>
          </div>
          <el-table :data="detailProduct.skus || []" size="small" border>
            <el-table-column prop="sku_code" label="编码" min-width="150" />
            <el-table-column prop="name" label="名称" min-width="180" />
            <el-table-column label="价格" width="180" align="right">
              <template #default="{ row }">¥{{ money(row.price_cents) }} <span class="secondary-cell">/ ¥{{ money(row.original_price_cents) }}</span></template>
            </el-table-column>
            <el-table-column label="状态" width="100" align="center">
              <template #default="{ row }"><el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="90" fixed="right" align="right">
              <template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openSkuDialog(row)">编辑</el-button><span v-else class="secondary-cell">只读</span></template>
            </el-table-column>
          </el-table>
        </section>

        <section class="detail-section">
          <div class="detail-section-heading">
            <div><h3>规格组</h3><span>{{ optionGroups.length }} 个</span></div>
            <el-button v-if="canWrite" plain type="primary" :icon="Plus" @click="openOptionGroupDialog">新增规格组</el-button>
          </div>
          <div v-if="optionGroups.length" class="option-group-list">
            <div v-for="group in optionGroups" :key="group.id" class="option-group-row">
              <div class="option-group-heading">
                <div><strong>{{ group.name }}</strong><span>{{ group.required ? '必选' : '可选' }} · {{ group.min_selections }}-{{ group.max_selections }} 项</span></div>
                <el-button v-if="canWrite" link type="primary" :icon="Plus" @click="openOptionDialog(group)">新增规格</el-button>
              </div>
              <div v-if="group.options?.length" class="option-list">
                <div v-for="option in group.options" :key="option.id" class="option-row">
                  <span>{{ option.name }}</span>
                  <span class="option-price">{{ signedMoney(option.price_delta_cents) }}</span>
                  <el-tag size="small" :type="option.status === 'active' ? 'success' : 'info'" effect="plain">{{ option.status === 'active' ? '启用' : '停用' }}</el-tag>
                </div>
              </div>
              <div v-else class="secondary-cell empty-options">暂无规格</div>
            </div>
          </div>
          <el-empty v-else description="暂无规格组" :image-size="64" />
        </section>
      </div>
      <template #footer><el-button @click="productDetailVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="skuDialogVisible" :title="skuForm.id ? '编辑 SKU' : '新增 SKU'" width="min(620px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="skuForm" label-position="top" class="commerce-form">
        <div class="form-grid">
          <el-form-item label="SKU 编码" required><el-input v-model="skuForm.sku_code" maxlength="80" /></el-form-item>
          <el-form-item label="SKU 名称" required><el-input v-model="skuForm.name" maxlength="160" /></el-form-item>
          <el-form-item label="原价" required><el-input-number v-model="skuForm.original_price" class="full-width" :min="0" :precision="2" :controls="false" /></el-form-item>
          <el-form-item label="售价" required><el-input-number v-model="skuForm.price" class="full-width" :min="0" :precision="2" :controls="false" /></el-form-item>
          <el-form-item label="状态"><el-select v-model="skuForm.status" class="full-width"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
        </div>
        <el-form-item label="属性 JSON"><el-input v-model="skuForm.attributes" type="textarea" :rows="3" placeholder="可选，例如：{&quot;size&quot;:&quot;大杯&quot;}" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="skuDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveSku">保存 SKU</el-button></template>
    </el-dialog>

    <el-dialog v-model="optionGroupDialogVisible" title="新增规格组" width="min(520px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="optionGroupForm" label-position="top" class="commerce-form">
        <el-form-item label="规格组名称" required><el-input v-model="optionGroupForm.name" maxlength="80" placeholder="例如：口味、尺寸、颜色" /></el-form-item>
        <el-form-item label="选择规则">
          <el-checkbox v-model="optionGroupForm.required">必选</el-checkbox>
          <div class="selection-range"><el-input-number v-model="optionGroupForm.min_selections" :min="0" :max="99" :controls="false" /><span>至</span><el-input-number v-model="optionGroupForm.max_selections" :min="0" :max="99" :controls="false" /></div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="optionGroupDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveOptionGroup">保存规格组</el-button></template>
    </el-dialog>

    <el-dialog v-model="optionDialogVisible" title="新增规格" width="min(520px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="optionForm" label-position="top" class="commerce-form">
        <el-form-item label="规格名称" required><el-input v-model="optionForm.name" maxlength="80" placeholder="例如：微辣、加冰、黑色" /></el-form-item>
        <el-form-item label="价格调整"><el-input-number v-model="optionForm.price_delta" :precision="2" :controls="false" /><span class="form-suffix">元</span></el-form-item>
        <el-form-item label="状态"><el-select v-model="optionForm.status" class="full-width"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="optionDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveOption">保存规格</el-button></template>
    </el-dialog>

    <el-dialog v-model="locationDialogVisible" title="新增履约地点" width="min(520px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="locationForm" label-position="top" class="commerce-form">
        <el-form-item label="地点名称" required><el-input v-model="locationForm.name" maxlength="120" placeholder="例如：景区东门取货点" /></el-form-item>
        <el-form-item label="地点类型"><el-select v-model="locationForm.location_type" class="full-width"><el-option label="门店" value="store" /><el-option label="仓库" value="warehouse" /><el-option label="自取点" value="pickup" /></el-select></el-form-item>
        <el-form-item label="状态"><el-select v-model="locationForm.status" class="full-width"><el-option label="启用" value="active" /><el-option label="停用" value="inactive" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button @click="locationDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveLocation">保存地点</el-button></template>
    </el-dialog>

    <el-dialog v-model="inventoryDialogVisible" :title="inventoryForm.id ? '设置库存' : '设置库存'" width="min(560px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="inventoryForm" label-position="top" class="commerce-form">
        <el-form-item label="SKU" required>
          <el-select v-model="inventoryForm.sku_id" class="full-width" filterable :disabled="Boolean(inventoryForm.id)" placeholder="选择 SKU">
            <el-option v-for="sku in skuOptions" :key="sku.id" :label="`${sku.name} · ${sku.sku_code}`" :value="sku.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="履约地点" required>
          <el-select v-model="inventoryForm.location_id" class="full-width" filterable :disabled="Boolean(inventoryForm.id)" placeholder="选择履约地点">
            <el-option v-for="location in activeLocations" :key="location.id" :label="location.name" :value="location.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="可用数量" required><el-input-number v-model="inventoryForm.available_qty" class="full-width" :min="0" :precision="0" :controls="false" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="inventoryDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveInventory">保存库存</el-button></template>
    </el-dialog>

    <el-dialog v-model="adjustmentDialogVisible" title="调整库存" width="min(480px, calc(100vw - 32px))" destroy-on-close>
      <div v-if="adjustmentRow" class="adjustment-context"><strong>{{ skuDisplayName(adjustmentRow.sku_id) }}</strong><span>{{ locationDisplayName(adjustmentRow.location_id) }} · 当前可用 {{ adjustmentRow.available_qty }}</span></div>
      <el-form :model="adjustmentForm" label-position="top" class="commerce-form">
        <el-form-item label="调整数量" required><el-input-number v-model="adjustmentForm.delta" :precision="0" :controls="false" placeholder="正数增加，负数减少" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="adjustmentDialogVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveAdjustment">确认调整</el-button></template>
    </el-dialog>

    <el-dialog v-model="orderDetailVisible" title="订单详情" width="min(760px, calc(100vw - 32px))" destroy-on-close>
      <div v-if="selectedOrder" v-loading="orderDetailLoading" class="order-detail">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="订单号">{{ selectedOrder.order_no }}</el-descriptions-item>
          <el-descriptions-item label="下单时间">{{ formatDate(selectedOrder.created_at) }}</el-descriptions-item>
          <el-descriptions-item label="联系人">{{ selectedOrder.contact_name || '未填写' }}</el-descriptions-item>
          <el-descriptions-item label="手机号">{{ selectedOrder.contact_phone || '未填写' }}</el-descriptions-item>
          <el-descriptions-item label="支付状态"><el-tag :type="paymentStatusType(selectedOrder.payment_status)" effect="plain">{{ paymentStatusLabel(selectedOrder.payment_status) }}</el-tag></el-descriptions-item>
          <el-descriptions-item label="履约状态"><el-tag :type="fulfillmentStatusType(selectedOrder.fulfillment_status)" effect="plain">{{ fulfillmentStatusLabel(selectedOrder.fulfillment_status) }}</el-tag></el-descriptions-item>
          <el-descriptions-item label="售后状态"><el-tag :type="refundStatusType(selectedOrder.refund_status)" effect="plain">{{ refundStatusLabel(selectedOrder.refund_status) }}</el-tag></el-descriptions-item>
          <el-descriptions-item label="订单金额">¥{{ money(selectedOrder.total_amount_cents) }}</el-descriptions-item>
          <el-descriptions-item v-if="selectedOrder.shipping_address" label="收货地址" :span="2">{{ selectedOrder.shipping_address }}</el-descriptions-item>
        </el-descriptions>
        <el-divider content-position="left">商品快照</el-divider>
        <el-table :data="selectedOrder.items || []" border size="small">
          <el-table-column prop="product_name" label="商品" min-width="180" />
          <el-table-column prop="sku_name" label="规格" min-width="150" />
          <el-table-column label="数量" width="80" align="right"><template #default="{ row }">{{ row.quantity }}</template></el-table-column>
          <el-table-column label="单价" width="100" align="right"><template #default="{ row }">¥{{ money(row.unit_price_cents) }}</template></el-table-column>
          <el-table-column label="小计" width="110" align="right"><template #default="{ row }">¥{{ money(row.line_amount_cents) }}</template></el-table-column>
          <el-table-column label="库存状态" width="100" align="center"><template #default="{ row }">{{ reservationStatusLabel(row.reservation_status) }}</template></el-table-column>
        </el-table>
        <div v-if="selectedOrder.restaurant_fulfillment || selectedOrder.retail_fulfillment" class="order-fulfillment-summary">
          <el-divider content-position="left">履约信息</el-divider>
          <div v-if="selectedOrder.restaurant_fulfillment">{{ selectedOrder.restaurant_fulfillment.method === 'delivery' ? '配送' : '自取' }} · {{ fulfillmentStatusLabel(selectedOrder.restaurant_fulfillment.status) }}</div>
          <div v-if="selectedOrder.retail_fulfillment">{{ selectedOrder.retail_fulfillment.carrier || '待填写物流公司' }} · {{ selectedOrder.retail_fulfillment.tracking_no || '待填写运单号' }}</div>
        </div>
        <div v-if="selectedOrder.after_sales?.length" class="order-after-sale-list">
          <el-divider content-position="left">售后记录</el-divider>
          <div v-for="afterSale in selectedOrder.after_sales" :key="afterSale.id" class="after-sale-row"><span>{{ afterSale.request_no }}</span><el-tag size="small" :type="refundStatusType(afterSale.status)" effect="plain">{{ afterSaleStatusLabel(afterSale.status) }}</el-tag><span class="secondary-cell">¥{{ money(afterSale.amount_cents) }}</span></div>
        </div>
      </div>
      <template #footer><el-button @click="orderDetailVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="shippingDialogVisible" title="填写发货信息" width="min(480px, calc(100vw - 32px))" destroy-on-close>
      <el-form :model="shippingForm" label-position="top" class="commerce-form">
        <el-form-item label="物流公司" required><el-input v-model="shippingForm.carrier" maxlength="80" placeholder="请输入物流公司" /></el-form-item>
        <el-form-item label="运单号" required><el-input v-model="shippingForm.tracking_no" maxlength="120" placeholder="请输入运单号" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="shippingDialogVisible = false">取消</el-button><el-button type="primary" :loading="orderActionID !== 0" @click="submitShipping">确认发货</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Plus, Refresh, Search } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { hasPermission } from '@/utils/permissions'
import {
  activeBusinessCapabilitySet,
  configuredBusinessCapabilitySet,
  readStoredUser,
  refreshStoredTenantIdentity,
} from '@/utils/tenantAccess'

type CommerceDomain = 'restaurant' | 'retail'
type ProductStatus = 'draft' | 'online' | 'offline'

type ProductSKU = {
  id?: number
  key?: number
  sku_code: string
  name: string
  original_price_cents?: number
  price_cents?: number
  original_price: number
  price: number
  status: 'active' | 'inactive'
  attributes: string
}

const route = useRoute()
const router = useRouter()
const user = ref(readStoredUser())
const loading = ref(false)
const saving = ref(false)
const detailLoading = ref(false)
const loadError = ref('')
const activeTab = ref('products')
const productSearch = ref('')
const productStatus = ref<ProductStatus | ''>('')
const products = ref<any[]>([])
const locations = ref<any[]>([])
const inventoryRows = ref<any[]>([])
const optionGroups = ref<any[]>([])
const detailProduct = ref<any | null>(null)
const orders = ref<any[]>([])
const orderSearch = ref('')
const orderPaymentStatus = ref('')
const orderFulfillmentStatus = ref('')
const orderRefundStatus = ref('')
const orderLoading = ref(false)
const orderDetailLoading = ref(false)
const orderDetailVisible = ref(false)
const selectedOrder = ref<any | null>(null)
const orderActionID = ref(0)
const shippingDialogVisible = ref(false)
const shippingOrder = ref<any | null>(null)
const shippingForm = reactive({ carrier: '', tracking_no: '' })

const productDialogVisible = ref(false)
const productDetailVisible = ref(false)
const skuDialogVisible = ref(false)
const optionGroupDialogVisible = ref(false)
const optionDialogVisible = ref(false)
const locationDialogVisible = ref(false)
const inventoryDialogVisible = ref(false)
const adjustmentDialogVisible = ref(false)

const productForm = reactive({
  name: '',
  short_title: '',
  description: '',
  category_name: '',
  status: 'draft' as ProductStatus,
  skus: [] as ProductSKU[],
})
const skuForm = reactive<ProductSKU>(newSkuForm())
const optionGroupForm = reactive({ name: '', required: false, min_selections: 0, max_selections: 1 })
const optionForm = reactive({ group_id: 0, name: '', price_delta: 0, status: 'active' })
const locationForm = reactive({ name: '', location_type: 'store', status: 'active' })
const inventoryForm = reactive({ id: 0, sku_id: 0, location_id: 0, available_qty: 0 })
const adjustmentForm = reactive({ delta: 0 })
const adjustmentRow = ref<any | null>(null)

const configuredDomains = computed<CommerceDomain[]>(() => {
  const set = configuredBusinessCapabilitySet(user.value)
  return (['restaurant', 'retail'] as CommerceDomain[]).filter(domain => set.has(domain))
})
const currentDomain = computed<CommerceDomain>(() => {
  const value = String(route.params.businessType || '')
  if (value === 'retail' || value === 'restaurant') return value
  return configuredDomains.value[0] || 'restaurant'
})
const activeDomains = computed(() => activeBusinessCapabilitySet(user.value))
const isCurrentDomainConfigured = computed(() => configuredDomains.value.includes(currentDomain.value))
const isCurrentDomainActive = computed(() => activeDomains.value.has(currentDomain.value))
const canWrite = computed(() => isCurrentDomainActive.value && hasPermission(user.value, 'catalog.write'))
const canOrdersRead = computed(() => hasPermission(user.value, 'orders.read'))
const canAfterSalesWrite = computed(() => hasPermission(user.value, 'after_sales.write'))
const canOperationsWrite = computed(() => isCurrentDomainActive.value && hasPermission(user.value, 'operations.write'))
const currentDomainLabel = computed(() => businessTypeLabel(currentDomain.value))
const capabilityStatusLabel = computed(() => isCurrentDomainActive.value ? '能力正常' : '能力已暂停')
const activeLocations = computed(() => locations.value.filter(row => row.status === 'active'))
const skuOptions = computed(() => products.value.flatMap(product => (product.skus || []).map((sku: any) => ({ ...sku, product_name: product.name }))))
const fulfillmentStatusOptions = computed(() => currentDomain.value === 'restaurant'
  ? [
      { value: 'pending_acceptance', label: '待接单' },
      { value: 'accepted', label: '已接单' },
      { value: 'preparing', label: '制作中' },
      { value: 'ready', label: '待取/待配送' },
      { value: 'delivering', label: '配送中' },
      { value: 'completed', label: '已完成' },
      { value: 'cancelled', label: '已取消' },
    ]
  : [
      { value: 'pending_shipment', label: '待发货' },
      { value: 'shipped', label: '已发货' },
      { value: 'in_transit', label: '运输中' },
      { value: 'delivered', label: '待收货' },
      { value: 'completed', label: '已完成' },
      { value: 'cancelled', label: '已取消' },
    ])

function newSkuForm(): ProductSKU {
  return { key: Date.now() + Math.random(), sku_code: '', name: '', original_price: 0, price: 0, status: 'active', attributes: '' }
}

function businessTypeLabel(value: string) {
  return value === 'retail' ? '电商' : '餐饮'
}

function productStatusLabel(value: string) {
  return ({ draft: '草稿', online: '上架中', offline: '已下架' } as Record<string, string>)[value] || value || '-'
}

function productStatusType(value: string) {
  return ({ draft: 'warning', online: 'success', offline: 'info' } as Record<string, string>)[value] || 'info'
}

function locationTypeLabel(value: string) {
  return ({ store: '门店', warehouse: '仓库', pickup: '自取点' } as Record<string, string>)[value] || value || '-'
}

function locationStatusLabel(value: string) {
  return value === 'active' ? '启用' : '停用'
}

function money(cents: number) {
  return (Number(cents || 0) / 100).toFixed(2)
}

function signedMoney(cents: number) {
  const amount = Number(cents || 0)
  if (amount === 0) return '不加价'
  return `${amount > 0 ? '+' : '-'}¥${money(Math.abs(amount))}`
}

function priceSummary(product: any) {
  const prices = (product.skus || []).map((sku: any) => Number(sku.price_cents || 0)).filter((value: number) => Number.isFinite(value))
  if (!prices.length) return '暂无售价'
  const min = Math.min(...prices)
  const max = Math.max(...prices)
  return min === max ? `售价 ¥${money(min)}` : `售价 ¥${money(min)} - ¥${money(max)}`
}

function skuRecord(skuID: number) {
  return skuOptions.value.find(sku => Number(sku.id) === Number(skuID))
}

function skuDisplayName(skuID: number) {
  const sku = skuRecord(skuID)
  return sku ? `${sku.product_name} · ${sku.name}` : '商品已归档'
}

function skuDisplayCode(skuID: number) {
  const sku = skuRecord(skuID)
  return sku?.sku_code || 'SKU'
}

function locationDisplayName(locationID: number) {
  return locations.value.find(row => Number(row.id) === Number(locationID))?.name || '地点已归档'
}

function silentConfig(params: Record<string, string>) {
  return { params, skipErrorToast: true } as any
}

function statusCode(error: any) {
  return Number(error?.response?.status || 0)
}

async function loadProducts() {
  if (!isCurrentDomainConfigured.value) return
  try {
    const response = await request.get('/commerce/products', silentConfig({
      business_type: currentDomain.value,
      status: productStatus.value,
      search: productSearch.value.trim(),
    }))
    products.value = response.data?.data || []
  } catch (error) {
    if (statusCode(error) !== 403) loadError.value = '商品目录暂时无法加载'
  }
}

async function loadLocations() {
  if (!isCurrentDomainConfigured.value) return
  try {
    const response = await request.get('/commerce/locations', silentConfig({ business_type: currentDomain.value }))
    locations.value = response.data?.data || []
  } catch (error) {
    if (statusCode(error) !== 403) loadError.value = '履约地点暂时无法加载'
  }
}

async function loadInventory() {
  if (!isCurrentDomainConfigured.value) return
  try {
    const response = await request.get('/commerce/inventory', silentConfig({ business_type: currentDomain.value }))
    inventoryRows.value = response.data?.data || []
  } catch (error) {
    if (statusCode(error) !== 403) loadError.value = '库存台账暂时无法加载'
  }
}

async function loadWorkspace() {
  if (!isCurrentDomainConfigured.value) return
  products.value = []
  locations.value = []
  inventoryRows.value = []
  loading.value = true
  loadError.value = ''
  await Promise.all([loadProducts(), loadLocations(), loadInventory()])
  loading.value = false
}

async function refreshWorkspace() {
  user.value = await refreshStoredTenantIdentity()
  await loadWorkspace()
}

function resetProductFilters() {
  productSearch.value = ''
  productStatus.value = ''
  void loadProducts()
}

function formatDate(value: unknown) {
  if (!value) return '-'
  const date = new Date(String(value))
  if (Number.isNaN(date.getTime())) return String(value)
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

function paymentStatusLabel(value: string) {
  return ({ unpaid: '待支付', pending: '支付中', paid: '已支付', failed: '支付失败', refunded: '已退款' } as Record<string, string>)[value] || value || '-'
}

function paymentStatusType(value: string) {
  return ({ unpaid: 'warning', pending: 'warning', paid: 'success', failed: 'danger', refunded: 'info' } as Record<string, string>)[value] || 'info'
}

function fulfillmentStatusLabel(value: string) {
  return ({
    pending_acceptance: '待接单', accepted: '已接单', preparing: '制作中', ready: '待取/待配送',
    delivering: '配送中', pending_shipment: '待发货', shipped: '已发货', in_transit: '运输中',
    delivered: '待收货', completed: '已完成', cancelled: '已取消',
  } as Record<string, string>)[value] || value || '-'
}

function fulfillmentStatusType(value: string) {
  return ({ pending_acceptance: 'warning', pending_shipment: 'warning', accepted: 'primary', preparing: 'primary', ready: 'success', delivering: 'primary', shipped: 'primary', in_transit: 'primary', delivered: 'success', completed: 'success', cancelled: 'danger' } as Record<string, string>)[value] || 'info'
}

function refundStatusLabel(value: string) {
  return ({ none: '无售后', requested: '退款申请中', processing: '退款处理中', partial: '部分退款', refunded: '已退款', rejected: '已拒绝' } as Record<string, string>)[value] || value || '-'
}

function refundStatusType(value: string) {
  return ({ none: 'info', requested: 'warning', processing: 'warning', partial: 'warning', refunded: 'success', rejected: 'danger' } as Record<string, string>)[value] || 'info'
}

function afterSaleStatusLabel(value: string) {
  return ({ requested: '申请中', approved: '已同意', processing: '处理中', completed: '已完成', failed: '失败', rejected: '已拒绝', cancelled: '已取消' } as Record<string, string>)[value] || value || '-'
}

function reservationStatusLabel(value: string) {
  return ({ reserved: '已预留', sold: '已售出', released: '已释放', refunded: '已退款' } as Record<string, string>)[value] || value || '-'
}

function orderRefundRequest(row: any) {
  return (row.after_sales || []).find((request: any) => ['requested', 'approved', 'processing'].includes(request.status))
}

async function loadOrders() {
  if (!isCurrentDomainConfigured.value) return
  orderLoading.value = true
  try {
    const response = await request.get('/commerce/orders', silentConfig({
      business_type: currentDomain.value,
      search: orderSearch.value.trim(),
      payment_status: orderPaymentStatus.value,
      fulfillment_status: orderFulfillmentStatus.value,
      refund_status: orderRefundStatus.value,
    }))
    orders.value = response.data?.data || []
  } catch (error) {
    if (statusCode(error) !== 403) loadError.value = '订单暂时无法加载'
  } finally {
    orderLoading.value = false
  }
}

function resetOrderFilters() {
  orderSearch.value = ''
  orderPaymentStatus.value = ''
  orderFulfillmentStatus.value = ''
  orderRefundStatus.value = ''
  void loadOrders()
}

function handleTabChange(name: string | number) {
  if (String(name) === 'orders') void loadOrders()
}

async function openOrderDetail(row: any) {
  if (!row?.id) return
  selectedOrder.value = row
  orderDetailVisible.value = true
  orderDetailLoading.value = true
  try {
    const response = await request.get(`/commerce/orders/${row.id}`, { skipErrorToast: true } as any)
    selectedOrder.value = response.data
  } catch (error) {
    if (statusCode(error) !== 403) ElMessage.error('订单详情暂时无法加载')
  } finally {
    orderDetailLoading.value = false
  }
}

function nextFulfillmentStatus(row: any) {
  const current = String(row?.fulfillment_status || '')
  if (row?.business_type === 'restaurant') {
    const next: Record<string, string> = {
      pending_acceptance: 'accepted', accepted: 'preparing', preparing: 'ready', delivering: 'completed',
    }
    if (current === 'ready') return row.restaurant_fulfillment?.method === 'delivery' ? 'delivering' : 'completed'
    return next[current] || ''
  }
  return ({ pending_shipment: 'shipped', shipped: 'in_transit', in_transit: 'delivered', delivered: 'completed' } as Record<string, string>)[current] || ''
}

function canRequestRefund(row: any) {
  return canAfterSalesWrite.value && row?.payment_status === 'paid' && row?.refund_status === 'none'
}

function canAdvanceFulfillment(row: any) {
  const refundOpen = row?.refund_status && !['none', 'rejected'].includes(row.refund_status)
  return canOperationsWrite.value && row?.payment_status === 'paid' && !refundOpen && Boolean(nextFulfillmentStatus(row))
}

async function requestOrderRefund(row: any) {
  if (!canRequestRefund(row)) return
  try {
    const result = await ElMessageBox.prompt('可选：填写退款原因，便于售后记录。', '申请退款', {
      inputPlaceholder: '例如：客户取消订单',
      inputValidator: (value: string) => value.length <= 255 || '退款原因不能超过 255 个字符',
      confirmButtonText: '提交申请',
      cancelButtonText: '取消',
    })
    orderActionID.value = row.id
    await request.post(`/commerce/orders/${row.id}/refund-requests`, {
      idempotency_key: `admin-refund-${row.id}-${Date.now()}`,
      reason: result.value?.trim() || '管理员申请退款',
    })
    ElMessage.success('退款申请已提交')
    await loadOrders()
  } catch { /* cancelled or request interceptor already reported the error */ }
  finally {
    orderActionID.value = 0
  }
}

async function advanceFulfillment(row: any) {
  if (!canAdvanceFulfillment(row)) return
  const next = nextFulfillmentStatus(row)
  if (!next) return
  if (row.business_type === 'retail' && row.fulfillment_status === 'pending_shipment') {
    shippingOrder.value = row
    Object.assign(shippingForm, { carrier: '', tracking_no: '' })
    shippingDialogVisible.value = true
    return
  }
  try {
    await ElMessageBox.confirm(`确认将订单推进为“${fulfillmentStatusLabel(next)}”？`, '推进履约', {
      type: 'warning', confirmButtonText: '确认', cancelButtonText: '取消',
    })
    orderActionID.value = row.id
    const endpoint = row.business_type === 'restaurant' ? 'restaurant-fulfillment' : 'retail-fulfillment'
    await request.post(`/commerce/orders/${row.id}/${endpoint}`, { status: next })
    ElMessage.success('履约状态已更新')
    await loadOrders()
  } catch { /* cancelled or request interceptor already reported the error */ }
  finally {
    orderActionID.value = 0
  }
}

async function submitShipping() {
  const row = shippingOrder.value
  const carrier = shippingForm.carrier.trim()
  const trackingNo = shippingForm.tracking_no.trim()
  if (!row?.id || !carrier || !trackingNo) {
    ElMessage.warning('请填写物流公司和运单号')
    return
  }
  orderActionID.value = row.id
  try {
    await request.post(`/commerce/orders/${row.id}/retail-fulfillment`, { status: 'shipped', carrier, tracking_no: trackingNo })
    shippingDialogVisible.value = false
    shippingOrder.value = null
    ElMessage.success('订单已标记发货')
    await loadOrders()
  } finally {
    orderActionID.value = 0
  }
}

function switchDomain(value: CommerceDomain) {
  if (!configuredDomains.value.includes(value)) return
  void router.push({ name: 'commerce', params: { businessType: value } })
}

function handleIdentityRefresh(event: Event) {
  user.value = (event as CustomEvent).detail || readStoredUser()
  if (!isCurrentDomainActive.value) {
    productDialogVisible.value = false
    skuDialogVisible.value = false
    optionGroupDialogVisible.value = false
    optionDialogVisible.value = false
    locationDialogVisible.value = false
    inventoryDialogVisible.value = false
    adjustmentDialogVisible.value = false
  }
}

function openProductDialog() {
  Object.assign(productForm, { name: '', short_title: '', description: '', category_name: '', status: 'draft' })
  productForm.skus = [newSkuForm()]
  productDialogVisible.value = true
}

function addProductSku() {
  productForm.skus.push(newSkuForm())
}

function removeProductSku(index: number) {
  if (productForm.skus.length > 1) productForm.skus.splice(index, 1)
}

async function saveProduct() {
  if (!canWrite.value) return
  if (!productForm.name.trim()) {
    ElMessage.warning('请填写商品名称')
    return
  }
  if (!productForm.skus.length || productForm.skus.some(sku => !sku.sku_code.trim() || !sku.name.trim())) {
    ElMessage.warning('请完整填写至少一个 SKU')
    return
  }
  if (productForm.skus.some(sku => Number(sku.price) > Number(sku.original_price))) {
    ElMessage.warning('SKU 售价不能高于原价')
    return
  }
  saving.value = true
  try {
    await request.post('/commerce/products', {
      business_type: currentDomain.value,
      name: productForm.name.trim(),
      short_title: productForm.short_title.trim(),
      description: productForm.description.trim(),
      category_name: productForm.category_name.trim(),
      status: productForm.status,
      skus: productForm.skus.map(sku => ({
        sku_code: sku.sku_code.trim(),
        name: sku.name.trim(),
        original_price_cents: Math.round(Number(sku.original_price || 0) * 100),
        price_cents: Math.round(Number(sku.price || 0) * 100),
        status: sku.status || 'active',
        attributes: sku.attributes?.trim() || '',
      })),
    })
    productDialogVisible.value = false
    ElMessage.success('商品已保存')
    await loadProducts()
  } finally {
    saving.value = false
  }
}

async function toggleProductStatus(row: any) {
  if (!canWrite.value) return
  const nextStatus: ProductStatus = row.status === 'online' ? 'offline' : 'online'
  try {
    await ElMessageBox.confirm(`确认${nextStatus === 'online' ? '上架' : '下架'}“${row.name}”？`, '变更商品状态', { type: 'warning' })
    await request.patch(`/commerce/products/${row.id}/status`, { status: nextStatus })
    ElMessage.success(`商品已${nextStatus === 'online' ? '上架' : '下架'}`)
    await loadProducts()
  } catch { /* cancelled */ }
}

async function openProductDetail(row: any) {
  detailProduct.value = row
  optionGroups.value = []
  productDetailVisible.value = true
  detailLoading.value = true
  try {
    const response = await request.get(`/commerce/products/${row.id}/options`, { skipErrorToast: true } as any)
    optionGroups.value = response.data?.data || []
  } catch (error) {
    if (statusCode(error) !== 403) ElMessage.error('规格暂时无法加载')
  } finally {
    detailLoading.value = false
  }
}

function syncDetailProduct() {
  if (!detailProduct.value) return
  const next = products.value.find(row => Number(row.id) === Number(detailProduct.value.id))
  if (next) detailProduct.value = next
}

function openSkuDialog(row?: any) {
  Object.assign(skuForm, {
    id: row?.id || 0,
    key: row?.id || Date.now(),
    sku_code: row?.sku_code || '',
    name: row?.name || '',
    original_price: Number(row?.original_price_cents || 0) / 100,
    price: Number(row?.price_cents || 0) / 100,
    status: row?.status || 'active',
    attributes: row?.attributes || '',
  })
  skuDialogVisible.value = true
}

async function saveSku() {
  if (!canWrite.value || !detailProduct.value) return
  if (!skuForm.sku_code.trim() || !skuForm.name.trim()) {
    ElMessage.warning('请填写 SKU 编码和名称')
    return
  }
  if (Number(skuForm.price) > Number(skuForm.original_price)) {
    ElMessage.warning('SKU 售价不能高于原价')
    return
  }
  const payload = {
    sku_code: skuForm.sku_code.trim(),
    name: skuForm.name.trim(),
    original_price_cents: Math.round(Number(skuForm.original_price || 0) * 100),
    price_cents: Math.round(Number(skuForm.price || 0) * 100),
    status: skuForm.status,
    attributes: skuForm.attributes?.trim() || '',
  }
  saving.value = true
  try {
    if (skuForm.id) await request.put(`/commerce/skus/${skuForm.id}`, payload)
    else await request.post(`/commerce/products/${detailProduct.value.id}/skus`, payload)
    skuDialogVisible.value = false
    ElMessage.success('SKU 已保存')
    await loadProducts()
    syncDetailProduct()
  } finally {
    saving.value = false
  }
}

function openOptionGroupDialog() {
  Object.assign(optionGroupForm, { name: '', required: false, min_selections: 0, max_selections: 1 })
  optionGroupDialogVisible.value = true
}

async function saveOptionGroup() {
  if (!canWrite.value || !detailProduct.value) return
  if (!optionGroupForm.name.trim()) {
    ElMessage.warning('请填写规格组名称')
    return
  }
  if (optionGroupForm.max_selections < optionGroupForm.min_selections || (optionGroupForm.required && optionGroupForm.min_selections < 1)) {
    ElMessage.warning('请选择有效的规格数量范围')
    return
  }
  saving.value = true
  try {
    await request.post(`/commerce/products/${detailProduct.value.id}/options`, {
      name: optionGroupForm.name.trim(),
      required: optionGroupForm.required,
      min_selections: optionGroupForm.min_selections,
      max_selections: optionGroupForm.max_selections,
    })
    optionGroupDialogVisible.value = false
    ElMessage.success('规格组已保存')
    await openProductDetail(detailProduct.value)
  } finally {
    saving.value = false
  }
}

function openOptionDialog(group: any) {
  Object.assign(optionForm, { group_id: group.id, name: '', price_delta: 0, status: 'active' })
  optionDialogVisible.value = true
}

async function saveOption() {
  if (!canWrite.value || !optionForm.group_id) return
  if (!optionForm.name.trim()) {
    ElMessage.warning('请填写规格名称')
    return
  }
  saving.value = true
  try {
    await request.post(`/commerce/option-groups/${optionForm.group_id}/options`, {
      name: optionForm.name.trim(),
      price_delta_cents: Math.round(Number(optionForm.price_delta || 0) * 100),
      status: optionForm.status,
    })
    optionDialogVisible.value = false
    ElMessage.success('规格已保存')
    if (detailProduct.value) await openProductDetail(detailProduct.value)
  } finally {
    saving.value = false
  }
}

function openLocationDialog() {
  Object.assign(locationForm, { name: '', location_type: 'store', status: 'active' })
  locationDialogVisible.value = true
}

async function saveLocation() {
  if (!canWrite.value) return
  if (!locationForm.name.trim()) {
    ElMessage.warning('请填写地点名称')
    return
  }
  saving.value = true
  try {
    await request.post('/commerce/locations', {
      business_type: currentDomain.value,
      name: locationForm.name.trim(),
      location_type: locationForm.location_type,
      status: locationForm.status,
    })
    locationDialogVisible.value = false
    ElMessage.success('履约地点已保存')
    await loadLocations()
  } finally {
    saving.value = false
  }
}

async function toggleLocationStatus(row: any) {
  if (!canWrite.value) return
  const nextStatus = row.status === 'active' ? 'inactive' : 'active'
  try {
    await ElMessageBox.confirm(`确认${nextStatus === 'active' ? '启用' : '停用'}“${row.name}”？`, '变更地点状态', { type: 'warning' })
    await request.patch(`/commerce/locations/${row.id}/status`, { status: nextStatus })
    ElMessage.success(`地点已${nextStatus === 'active' ? '启用' : '停用'}`)
    await loadLocations()
  } catch { /* cancelled */ }
}

function openInventoryDialog(row?: any) {
  Object.assign(inventoryForm, {
    id: row?.id || 0,
    sku_id: row?.sku_id || skuOptions.value[0]?.id || 0,
    location_id: row?.location_id || activeLocations.value[0]?.id || 0,
    available_qty: row?.available_qty || 0,
  })
  inventoryDialogVisible.value = true
}

async function saveInventory() {
  if (!canWrite.value) return
  if (!inventoryForm.sku_id || !inventoryForm.location_id || inventoryForm.available_qty < 0) {
    ElMessage.warning('请选择 SKU、履约地点并填写可用数量')
    return
  }
  saving.value = true
  try {
    await request.put('/commerce/inventory', {
      sku_id: inventoryForm.sku_id,
      location_id: inventoryForm.location_id,
      available_qty: Math.round(Number(inventoryForm.available_qty)),
    })
    inventoryDialogVisible.value = false
    ElMessage.success('库存已保存')
    await loadInventory()
  } finally {
    saving.value = false
  }
}

function openAdjustmentDialog(row: any) {
  adjustmentRow.value = row
  adjustmentForm.delta = 0
  adjustmentDialogVisible.value = true
}

async function saveAdjustment() {
  if (!canWrite.value || !adjustmentRow.value || !adjustmentForm.delta) {
    if (!adjustmentForm.delta) ElMessage.warning('请输入非零调整数量')
    return
  }
  saving.value = true
  try {
    await request.patch('/commerce/inventory', {
      sku_id: adjustmentRow.value.sku_id,
      location_id: adjustmentRow.value.location_id,
      delta: Math.round(Number(adjustmentForm.delta)),
    })
    adjustmentDialogVisible.value = false
    ElMessage.success('库存已调整')
    await loadInventory()
  } finally {
    saving.value = false
  }
}

watch(() => route.params.businessType, async () => {
  activeTab.value = 'products'
  productSearch.value = ''
  productStatus.value = ''
  detailProduct.value = null
  productDetailVisible.value = false
  user.value = readStoredUser()
  await loadWorkspace()
})

onMounted(async () => {
  window.addEventListener('tenant-identity-refreshed', handleIdentityRefresh)
  user.value = await refreshStoredTenantIdentity()
  await loadWorkspace()
})

onBeforeUnmount(() => {
  window.removeEventListener('tenant-identity-refreshed', handleIdentityRefresh)
})
</script>

<style scoped>
.commerce-page { display: flex; flex-direction: column; gap: 16px; }
.commerce-heading { align-items: flex-start; }
.section-kicker { color: var(--ui-text-secondary); font-size: 12px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; }
.commerce-heading h1 { margin: 4px 0 0; font-size: 26px; line-height: 1.25; }
.commerce-heading p { margin: 6px 0 0; }
.page-actions { display: flex; align-items: center; justify-content: flex-end; flex-wrap: wrap; gap: 8px; }
.domain-switcher { padding: 2px 0; }
.capability-alert { margin: 0; }
.commerce-workspace { min-width: 0; }
.workspace-tabs { min-height: 420px; }
.workspace-section { min-width: 0; padding-top: 8px; }
.section-toolbar, .detail-section-heading, .form-section-heading, .option-group-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.section-toolbar { margin-bottom: 14px; }
.section-toolbar h2 { margin: 0; font-size: 19px; }
.section-toolbar p { margin: 4px 0 0; }
.muted, .secondary-cell { color: var(--ui-text-secondary); font-size: 12px; }
.commerce-filter-bar { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
.commerce-search { width: min(360px, 100%); }
.status-filter { width: 150px; }
.commerce-table { width: 100%; }
.primary-cell { color: var(--ui-text); font-weight: 650; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0 16px; }
.full-width { width: 100%; }
.commerce-form { padding-top: 4px; }
.form-section-heading { align-items: center; padding: 4px 0 12px; border-bottom: 1px solid var(--ui-border); }
.form-section-heading strong { display: block; }
.form-section-heading span, .detail-section-heading span, .option-group-heading span { display: block; margin-top: 4px; color: var(--ui-text-secondary); font-size: 12px; }
.sku-form-list { display: flex; flex-direction: column; gap: 8px; padding-top: 12px; }
.sku-form-row { display: grid; grid-template-columns: minmax(120px, 1fr) minmax(140px, 1.3fr) minmax(120px, 0.85fr) minmax(120px, 0.85fr) 36px; align-items: center; gap: 8px; }
.sku-price-input { width: 100%; }
.detail-workspace { display: flex; flex-direction: column; gap: 24px; }
.detail-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding-bottom: 16px; border-bottom: 1px solid var(--ui-border); }
.detail-header h2 { margin: 0; font-size: 20px; }
.detail-header p { margin: 6px 0 0; }
.detail-section { display: flex; flex-direction: column; gap: 12px; }
.detail-section-heading { align-items: center; }
.detail-section-heading h3 { display: inline; margin: 0; font-size: 16px; }
.detail-section-heading > div > span { display: inline; margin-left: 8px; }
.option-group-list { display: flex; flex-direction: column; gap: 10px; }
.option-group-row { padding: 14px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius); }
.option-group-heading { align-items: center; }
.option-list { display: flex; flex-direction: column; gap: 8px; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--ui-border); }
.option-row { display: grid; grid-template-columns: 1fr 100px 70px; align-items: center; gap: 8px; font-size: 13px; }
.option-price { color: var(--ui-text-secondary); text-align: right; }
.empty-options { padding-top: 12px; }
.selection-range { display: inline-flex; align-items: center; gap: 8px; margin-left: 18px; vertical-align: middle; }
.selection-range .el-input-number { width: 110px; }
.form-suffix { margin-left: 8px; color: var(--ui-text-secondary); }
.adjustment-context { display: flex; flex-direction: column; gap: 4px; margin-bottom: 16px; padding: 12px; background: var(--ui-surface-soft); border: 1px solid var(--ui-border); border-radius: var(--ui-radius); }
@media (max-width: 800px) {
  .commerce-heading { flex-direction: column; }
  .page-actions { justify-content: flex-start; }
  .form-grid { grid-template-columns: 1fr; }
  .sku-form-row { grid-template-columns: 1fr 1fr 1fr 1fr 36px; }
  .sku-code-input, .sku-name-input { grid-column: span 2; }
}
@media (max-width: 560px) {
  .commerce-heading h1 { font-size: 22px; }
  .commerce-filter-bar { align-items: stretch; flex-direction: column; }
  .commerce-search, .status-filter { width: 100%; }
  .sku-form-row { grid-template-columns: 1fr 1fr 36px; }
  .sku-code-input, .sku-name-input { grid-column: span 2; }
  .sku-price-input { width: 100%; }
  .selection-range { display: flex; margin: 12px 0 0; }
  .option-row { grid-template-columns: 1fr 78px 64px; }
}
</style>
