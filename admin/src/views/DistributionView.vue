<template>
  <div class="space-y-6">
    <header class="page-heading">
      <div class="page-heading-copy">
        <h2 class="text-lg font-bold text-gray-900">供销合作</h2>
        <p class="text-xs text-gray-500 mt-1">维护供应商授权、分销商铺货和履约关系</p>
      </div>
      <div class="page-actions">
         <el-button v-if="canDistribute && canWrite && activeTab === 'suppliers'" type="primary" :icon="Connection" @click="dialogVisible = true">寻找供应商</el-button>
      </div>
    </header>

    <div class="data-panel">
      <el-tabs v-model="activeTab" @tab-change="handleTabChange">
        
        <!-- Tab 1: My Suppliers -->
        <el-tab-pane v-if="canDistribute" label="我的供应商 (我是分销商)" name="suppliers">
            <div class="flex items-center justify-between mb-4 mt-2">
                <h3 class="font-bold text-gray-700">已合作的供应商</h3>
                <el-button link type="primary" @click="fetchSuppliers"><el-icon><Refresh /></el-icon></el-button>
            </div>
            <el-table :data="suppliers" style="width: 100%" v-loading="loadingSuppliers">
                <el-table-column prop="supplier_name" label="供应商名称" min-width="180">
                <template #default="{ row }">
                    <div class="font-medium">{{ row.supplier_name }}</div>
                    <div class="text-xs text-gray-400">系统编号: {{ row.supplier_code }}</div>
                </template>
                </el-table-column>
                <el-table-column prop="status" label="合作状态" width="120">
                <template #default="{ row }">
                    <el-tag :type="getStatusType(row.status)">{{ getStatusText(row.status) }}</el-tag>
                </template>
                </el-table-column>
                <el-table-column prop="agent_level" label="分销等级" width="120">
                <template #default="{ row }">
                    <el-tag effect="plain">{{ getLevelText(row.agent_level) }}</el-tag>
                </template>
                </el-table-column>
                <el-table-column prop="balance" label="预付余额" width="150" align="right">
                <template #default="{ row }">
                    <span class="font-mono font-bold text-orange-500">¥{{ row.balance || '0.00' }}</span>
                </template>
                </el-table-column>
                <el-table-column label="操作" width="200" fixed="right" align="center">
                <template #default="{ row }">
                    <el-button v-if="canWrite" type="primary" size="small" @click="handleSourcing(row)">采购/上架</el-button>
                </template>
                </el-table-column>
            </el-table>
        </el-tab-pane>

        <el-tab-pane v-if="canDistribute" label="组合产品" name="bundles">
          <div class="flex items-center justify-between mb-4 mt-2">
            <div>
              <h3 class="font-bold text-gray-700">跨供应商组合产品</h3>
              <p class="text-xs text-gray-500 mt-1">销售一个产品，系统按组件分别生成供应商票权和结算明细。</p>
            </div>
            <div class="flex gap-2">
              <el-button link type="primary" @click="fetchBundles"><el-icon><Refresh /></el-icon></el-button>
              <el-button v-if="canWrite" type="primary" @click="openBundleForm()">新建组合产品</el-button>
            </div>
          </div>
          <el-table :data="bundles" v-loading="loadingBundles" stripe>
            <el-table-column prop="name" label="组合产品" min-width="180" />
            <el-table-column label="售价" width="110"><template #default="{ row }">¥{{ centsToYuan(row.retail_price_cents) }}</template></el-table-column>
            <el-table-column label="销售端" width="100"><template #default="{ row }">{{ row.type === 'offline' ? '售票窗口' : '线上' }}</template></el-table-column>
            <el-table-column label="版本" width="80"><template #default="{ row }">V{{ row.version }}</template></el-table-column>
            <el-table-column label="组件" min-width="280">
              <template #default="{ row }">
                <div v-for="component in row.components || []" :key="component.id" class="text-xs leading-6">
                  {{ component.supplier_name }} · {{ component.seller_product_name }} × {{ component.quantity }}
                  <span class="text-gray-400">（分摊 ¥{{ centsToYuan(component.retail_allocation_cents) }}）</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="130">
              <template #default="{ row }">
                <el-tag :type="row.status === 'online' && row.available ? 'success' : row.available ? 'info' : 'danger'">
                  {{ !row.available ? '组件已失效' : row.status === 'online' ? '销售中' : '已下架' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="190" fixed="right">
              <template #default="{ row }">
                <el-button v-if="canWrite" link type="primary" @click="openBundleForm(row)">编辑</el-button>
                <el-button v-if="canWrite && row.status !== 'online'" link type="success" :disabled="!row.available" @click="changeBundleStatus(row, 'online')">上架</el-button>
                <el-button v-else-if="canWrite" link type="warning" @click="changeBundleStatus(row, 'offline')">下架</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>

        <!-- Tab 2: My Agents -->
        <el-tab-pane v-if="canViewSupplyHistory" label="我的分销商 (我是供应商)" name="agents">
            <div class="flex items-center justify-between mb-4 mt-2">
                <h3 class="font-bold text-gray-700">代理申请列表</h3>
                <el-button link type="primary" @click="fetchAgents"><el-icon><Refresh /></el-icon></el-button>
            </div>
             <el-table :data="agents" style="width: 100%" v-loading="loadingAgents">
                <el-table-column prop="agent_name" label="分销商名称" min-width="180">
                <template #default="{ row }">
                    <div class="font-medium">{{ row.agent_name }}</div>
                    <div class="text-xs text-gray-400">联系人: {{ row.agent_contact || '暂无' }}</div>
                </template>
                </el-table-column>
                <el-table-column prop="agent_code" label="系统编号" width="150">
                    <template #default="{ row }">
                       <span class="font-mono">{{ row.agent_code }}</span>
                    </template>
                </el-table-column>
                <el-table-column prop="created_at" label="申请时间" width="160" />
                <el-table-column prop="status" label="状态" width="120">
                <template #default="{ row }">
                    <el-tag :type="getStatusType(row.status)">{{ getStatusText(row.status) }}</el-tag>
                </template>
                </el-table-column>
                <el-table-column label="操作" width="200" fixed="right" align="center">
                <template #default="{ row }">
                    <div v-if="row.status === 'pending'">
                        <el-button v-if="canSupplierWrite" type="success" size="small" @click="handleAudit(row, 'active')">通过</el-button>
                        <el-button v-if="canSupplierWrite" type="danger" size="small" @click="handleAudit(row, 'rejected')">拒绝</el-button>
                    </div>
                    <div v-else>
                         <el-button v-if="canSupplierHistoryWrite" type="primary" size="small" @click="handleOffers(row)">产品结算价</el-button>
                         <el-button v-if="canSupplierWrite" type="warning" size="small" @click="handleRecharge(row)">充值</el-button>
                    </div>
                </template>
                </el-table-column>
            </el-table>
        </el-tab-pane>

        <el-tab-pane v-if="canViewSupplyHistory" label="供应履约" name="fulfillments">
            <div class="flex items-center justify-between mb-4 mt-2">
                <div class="flex gap-2 items-center">
                    <el-select v-model="fulfillmentStatus" clearable placeholder="全部状态" style="width: 160px" @change="fetchFulfillments">
                        <el-option label="已预占" value="reserved" />
                        <el-option label="已支付" value="paid" />
                        <el-option label="已履约" value="fulfilled" />
                        <el-option label="已取消" value="cancelled" />
                    </el-select>
                    <el-select v-model="fulfillmentDistributorId" clearable filterable placeholder="全部分销商" style="width: 220px" @change="fetchFulfillments">
                        <el-option v-for="agent in activeAgents" :key="agent.agent_tenant_id" :label="agent.agent_name" :value="agent.agent_tenant_id" />
                    </el-select>
                </div>
                <el-button link type="primary" @click="fetchFulfillments"><el-icon><Refresh /></el-icon></el-button>
            </div>
            <el-table :data="fulfillments" style="width: 100%" v-loading="loadingFulfillments" stripe>
                <el-table-column prop="fulfillment_no" label="履约单" min-width="190" />
                <el-table-column prop="sales_order_no" label="销售订单" min-width="190" />
                <el-table-column prop="sales_tenant_name" label="分销商" min-width="150" />
                <el-table-column prop="scenic_area_name" label="景区" min-width="140" />
                <el-table-column label="应结" width="110"><template #default="{ row }">¥{{ Number(row.settlement_amount || 0).toFixed(2) }}</template></el-table-column>
                <el-table-column label="状态" width="110"><template #default="{ row }">{{ fulfillmentStatusText(row.status) }}</template></el-table-column>
                <el-table-column label="票数" width="120">
                    <template #default="{ row }">{{ row.used_count }}/{{ row.ticket_count }} 已核销</template>
                </el-table-column>
                <el-table-column label="操作" width="90" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openFulfillment(row)">详情</el-button></template></el-table-column>
            </el-table>
        </el-tab-pane>

      </el-tabs>
    </div>

    <el-drawer v-model="fulfillmentDrawer" title="供应履约详情" size="78%" destroy-on-close>
      <div v-loading="loadingFulfillmentDetail" class="space-y-6">
        <el-descriptions v-if="fulfillmentDetail" :column="3" border>
          <el-descriptions-item label="履约单">{{ fulfillmentDetail.fulfillment.fulfillment_no }}</el-descriptions-item>
          <el-descriptions-item label="销售订单">{{ fulfillmentDetail.fulfillment.sales_order_no }}</el-descriptions-item>
          <el-descriptions-item label="履约状态">{{ fulfillmentStatusText(fulfillmentDetail.fulfillment.status) }}</el-descriptions-item>
          <el-descriptions-item label="分销商">{{ fulfillmentDetail.fulfillment.sales_tenant_name }}</el-descriptions-item>
          <el-descriptions-item label="履约景区">{{ fulfillmentDetail.fulfillment.scenic_area_name }}</el-descriptions-item>
          <el-descriptions-item label="结算状态">{{ fulfillmentDetail.settlement.statement_status ? settlementStatusText(fulfillmentDetail.settlement.statement_status) : '尚未生成结算单' }}</el-descriptions-item>
        </el-descriptions>

        <div v-if="fulfillmentDetail" class="grid grid-cols-4 gap-3">
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">履约总额</div><strong>¥{{ centsToYuan(fulfillmentDetail.settlement.gross_cents) }}</strong></div>
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">退款冲减</div><strong>¥{{ centsToYuan(fulfillmentDetail.settlement.refund_cents) }}</strong></div>
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">佣金</div><strong>¥{{ centsToYuan(fulfillmentDetail.settlement.commission_cents) }}</strong></div>
          <div class="bg-gray-50 p-4"><div class="text-xs text-gray-500">应结净额</div><strong class="text-green-700">¥{{ centsToYuan(fulfillmentDetail.settlement.net_cents) }}</strong></div>
        </div>

        <section v-for="item in fulfillmentDetail?.items || []" :key="item.id">
          <div class="flex items-center justify-between mb-2">
            <strong>{{ item.product_name }}</strong>
            <span class="text-sm text-gray-500">{{ formatDate(item.use_date) }} · {{ item.quantity }} 张 · 结算价 ¥{{ centsToYuan(item.settlement_price_cents) }}</span>
          </div>
          <el-table :data="item.tickets" size="small" border>
            <el-table-column prop="ticket_code" label="票码" min-width="180" />
            <el-table-column label="游客" min-width="160"><template #default="{ row }"><div>{{ row.visitor_name || '-' }}</div><div class="text-xs text-gray-400">{{ row.visitor_phone || '-' }}</div></template></el-table-column>
            <el-table-column prop="visitor_id" label="证件号" min-width="180" show-overflow-tooltip />
            <el-table-column label="票状态" width="110"><template #default="{ row }">{{ ticketStatusText(row.entitlement_status || row.status) }}</template></el-table-column>
            <el-table-column label="核销记录" min-width="240"><template #default="{ row }"><span v-if="!row.check_in_records?.length">暂无</span><div v-for="record in row.check_in_records || []" :key="record.id" class="text-xs">{{ formatDateTime(record.check_in_time) }} · {{ record.check_point?.name || `检票点 ${record.check_point_id}` }} · {{ record.reversed_at ? '已随退票撤销' : (record.result === 'success' ? '成功' : '失败') }}</div></template></el-table-column>
            <el-table-column v-if="currentUser.is_initial_admin" label="操作" width="90" fixed="right"><template #default="{ row }"><el-button v-if="row.check_in_count > 0 && row.status !== 'refunded'" link type="danger" @click="openUsedRefund(item, row)">退票</el-button></template></el-table-column>
          </el-table>
        </section>

        <section v-if="fulfillmentDetail">
          <h3 class="font-semibold mb-2">退款与售后责任</h3>
          <el-table :data="fulfillmentDetail.after_sales" size="small" empty-text="该履约单暂无售后记录">
            <el-table-column prop="request_no" label="售后单" min-width="180" />
            <el-table-column label="类型" width="100"><template #default="{ row }">{{ afterSaleTypeText(row.type) }}</template></el-table-column>
            <el-table-column label="状态" width="110"><template #default="{ row }">{{ afterSaleStatusText(row.status) }}</template></el-table-column>
            <el-table-column label="退款金额" width="120"><template #default="{ row }">¥{{ centsToYuan(row.amount_cents) }}</template></el-table-column>
            <el-table-column prop="reason" label="原因" min-width="220" />
          </el-table>
          <el-table v-if="fulfillmentDetail.refunds?.length" :data="fulfillmentDetail.refunds" size="small" class="mt-3">
            <el-table-column prop="refund_no" label="退款单" min-width="190" />
            <el-table-column label="票码" min-width="190"><template #default="{ row }">{{ refundTicketCodes(row.ticket_codes) }}</template></el-table-column>
            <el-table-column label="金额" width="120"><template #default="{ row }">¥{{ centsToYuan(row.amount_cents) }}</template></el-table-column>
            <el-table-column label="状态" width="120"><template #default="{ row }">{{ refundStatusText(row.status) }}</template></el-table-column>
            <el-table-column prop="reason" label="原因" min-width="200" />
          </el-table>
        </section>
      </div>
    </el-drawer>

    <el-dialog v-model="usedRefundDialog" title="已核销票退票" width="500px" append-to-body>
      <el-alert title="退款将按原订单支付方式处理，已恢复的预付款或授信不会在结算冲减时重复变动。" type="warning" :closable="false" class="mb-4" />
      <el-descriptions v-if="usedRefundTarget.ticket" :column="1" border>
        <el-descriptions-item label="销售订单">{{ fulfillmentDetail?.fulfillment?.sales_order_no }}</el-descriptions-item>
        <el-descriptions-item label="票种">{{ usedRefundTarget.item?.product_name }}</el-descriptions-item>
        <el-descriptions-item label="票码">{{ usedRefundTarget.ticket?.ticket_code }}</el-descriptions-item>
        <el-descriptions-item label="退款金额">¥{{ centsToYuan(usedRefundTarget.ticket?.refund_value_cents) }}</el-descriptions-item>
      </el-descriptions>
      <el-form label-position="top" class="mt-4">
        <el-form-item label="退票原因" required><el-input v-model="usedRefundReason" type="textarea" :rows="3" maxlength="255" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="usedRefundDialog = false">取消</el-button><el-button type="danger" :loading="usedRefundSaving" @click="submitUsedRefund">确认退票</el-button></template>
    </el-dialog>

    <!-- Apply Dialog -->
    <el-dialog v-model="dialogVisible" title="申请代理权益" width="500px">
      <!-- ... (Same as before) ... -->
      <el-form label-position="top">
        <el-form-item label="请输入目标供应商的系统编号">
          <div class="flex gap-2">
            <el-input v-model="targetSystemCode" placeholder="例如: SYS001" class="flex-1" />
            <el-button @click="handleSearch" :loading="searching">查询</el-button>
          </div>
        </el-form-item>

        <div v-if="foundSupplier" class="bg-gray-50 p-4 rounded-lg mb-4 border border-gray-200">
           <div class="flex items-center gap-3 mb-2">
             <el-avatar :size="40" class="bg-indigo-100 text-indigo-500 font-bold">{{ foundSupplier.name.charAt(0) }}</el-avatar>
             <div>
               <div class="font-bold text-gray-800">{{ foundSupplier.name }}</div>
               <div class="text-xs text-gray-500">联系人: {{ foundSupplier.contact || '暂无' }}</div>
               <div class="text-xs text-gray-400 font-mono">CODE: {{ foundSupplier.code }}</div>
             </div>
           </div>
           <el-alert title="确认申请后，需等待对方审核通过才可代理其产品。" type="info" :closable="false" />
        </div>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleApply" :disabled="!foundSupplier" :loading="applying">确认申请</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- Sourcing Dialog (Product List) -->
    <el-dialog v-model="sourcingDialogVisible" title="可转销产品列表" width="800px">
        <el-table :data="supplierProducts" v-loading="loadingProducts" height="400">
            <el-table-column prop="name" label="产品名称" min-width="150" />
            <el-table-column prop="settlement_price" label="结算价" width="120">
                <template #default="{ row }">
                   <span class="font-bold text-orange-500">¥{{ row.settlement_price }}</span>
                </template>
            </el-table-column>
            <el-table-column prop="validity_type" label="有效期" width="150">
                 <template #default="{ row }">
                    <span v-if="row.validity_type === 'date'">指定日期</span>
                    <span v-else>有效期{{ row.validity_days }}天</span>
                 </template>
            </el-table-column>
            <el-table-column label="操作" width="120" fixed="right">
                <template #default="{ row }">
                    <el-button type="primary" size="small" @click="handleImportConfig(row)">一键上架</el-button>
                </template>
            </el-table-column>
        </el-table>
    </el-dialog>

    <!-- Import Config Dialog -->
    <el-dialog v-model="importDialogVisible" title="上架配置" width="500px">
        <el-form label-position="top">
            <el-alert title="将供应商产品映射到您的本地票务库，对接成功后可直接售卖。" type="success" :closable="false" class="mb-4"/>
            <el-form-item label="产品名称 (本地重命名)">
                <el-input v-model="importForm.name" />
            </el-form-item>
            <el-form-item label="您的售价 (Display Price)">
                <el-input-number v-model="importForm.price" :min="0" :precision="2" class="w-full" />
                <div class="text-xs text-gray-400 mt-1">结算成本: ¥{{ importForm.settlement_price }}</div>
            </el-form-item>
            <el-form-item label="上架渠道 (可多选)">
                <el-checkbox-group v-model="importForm.channels">
                    <el-checkbox label="online">线上微商城</el-checkbox>
                    <el-checkbox label="offline">线下售票窗口</el-checkbox>
                </el-checkbox-group>
            </el-form-item>
        </el-form>
        <template #footer>
            <span class="dialog-footer">
                <el-button @click="importDialogVisible = false">取消</el-button>
                <el-button type="primary" @click="confirmImport" :loading="importing">确认上架</el-button>
            </span>
        </template>
    </el-dialog>

    <!-- Recharge Dialog -->
    <el-dialog v-model="rechargeDialogVisible" title="资金充值" width="400px">
        <el-form label-position="top">
            <el-alert type="warning" :closable="false" class="mb-4">
                <template #title>
                    正在给 <b>{{ rechargeForm.agent_name }}</b> 充值
                </template>
            </el-alert>
            <el-form-item label="充值金额 (CNY)">
                <el-input-number v-model="rechargeForm.amount" :min="0" :step="100" class="w-full" />
            </el-form-item>
        </el-form>
        <template #footer>
            <span class="dialog-footer">
                <el-button @click="rechargeDialogVisible = false">取消</el-button>
                <el-button type="primary" @click="confirmRecharge" :loading="recharging">确认充值</el-button>
            </span>
        </template>
    </el-dialog>

    <el-dialog v-model="offersDialogVisible" :title="`${selectedDistributor?.agent_name || '分销商'} · 产品结算价`" width="980px">
        <div class="flex justify-between items-center mb-3">
            <span class="text-sm text-gray-500">这里的每一行同时决定该分销商可以销售的产品和对应结算价。酒景套餐暂不支持跨租户分销。</span>
            <div class="flex gap-2">
                <el-button size="small" @click="loadOffers">刷新</el-button>
                <el-button v-if="canSupplierWrite" type="primary" size="small" @click="openOfferForm()">添加产品价格</el-button>
            </div>
        </div>
        <el-table :data="offers" v-loading="loadingOffers" height="360" stripe>
            <el-table-column label="产品" min-width="190"><template #default="{ row }"><div class="font-medium">{{ sourceProductName(row.source_product_id) }}</div><div class="text-xs text-gray-400">产品编号 {{ row.source_product_id }}</div></template></el-table-column>
            <el-table-column prop="settlement_price" label="该分销商结算价" width="150"><template #default="{ row }"><strong>¥{{ Number(row.settlement_price || 0).toFixed(2) }}</strong></template></el-table-column>
            <el-table-column label="最低售价" width="120"><template #default="{ row }">¥{{ centsToYuan(row.minimum_retail_price_cents) }}</template></el-table-column>
            <el-table-column label="额度" width="90"><template #default="{ row }">{{ row.quota ? row.quota : '不限' }}</template></el-table-column>
            <el-table-column label="销售渠道" min-width="150"><template #default="{ row }">{{ channelText(row.allowed_channels) }}</template></el-table-column>
            <el-table-column label="状态" width="100"><template #default="{ row }">{{ offerStatusText(row.status) }}</template></el-table-column>
            <el-table-column label="操作" width="220" fixed="right">
                <template #default="{ row }">
                    <el-button v-if="canSupplierWrite" link type="primary" @click="openOfferForm(row)">修改价格</el-button>
                    <el-button v-if="canSupplierHistoryWrite && row.status === 'active'" link type="warning" @click="handleOfferStatus(row, 'suspended')">暂停</el-button>
                    <el-button v-else-if="canSupplierWrite && row.status === 'suspended'" link type="success" @click="handleOfferStatus(row, 'active')">恢复</el-button>
                    <el-button v-if="canSupplierHistoryWrite && row.status !== 'expired'" link type="danger" @click="handleOfferStatus(row, 'expired')">终止</el-button>
                </template>
            </el-table-column>
        </el-table>
    </el-dialog>

    <el-dialog v-model="offerFormVisible" :title="offerForm.source_product_id ? '设置产品结算价' : '添加产品结算价'" width="560px">
        <el-form :model="offerForm" label-position="top">
            <el-form-item label="供货产品">
                <el-select v-model="offerForm.source_product_id" :disabled="editingOffer" filterable class="w-full" placeholder="选择已上架且允许分销的产品">
                    <el-option v-for="product in sourceProducts" :key="product.id" :label="product.name" :value="product.id" />
                </el-select>
            </el-form-item>
            <div class="grid grid-cols-2 gap-3">
                <el-form-item label="结算价">
                    <el-input-number v-model="offerForm.settlement_price" :min="0.01" :precision="2" class="w-full" />
                </el-form-item>
                <el-form-item label="最低零售价">
                    <el-input-number v-model="offerForm.minimum_retail_price" :min="0" :precision="2" class="w-full" />
                </el-form-item>
                <el-form-item label="销售额度（0表示不限）">
                    <el-input-number v-model="offerForm.quota" :min="0" :precision="0" class="w-full" />
                </el-form-item>
                <el-form-item label="佣金比例（万分比）">
                    <el-input-number v-model="offerForm.commission_bps" :min="0" :max="10000" :precision="0" class="w-full" />
                </el-form-item>
            </div>
            <el-form-item label="允许销售渠道">
                <el-checkbox-group v-model="offerChannels">
                    <el-checkbox label="window">售票窗口</el-checkbox>
                    <el-checkbox label="online">线上商城</el-checkbox>
                    <el-checkbox label="ota">外部渠道</el-checkbox>
                </el-checkbox-group>
            </el-form-item>
        </el-form>
        <template #footer>
            <el-button @click="offerFormVisible = false">取消</el-button>
            <el-button type="primary" :loading="savingOffer" @click="createOffer">创建</el-button>
        </template>
    </el-dialog>

    <el-dialog v-model="bundleFormVisible" :title="bundleForm.id ? '编辑组合产品' : '新建组合产品'" width="760px" destroy-on-close>
      <el-form label-position="top">
        <div class="grid grid-cols-2 gap-4">
          <el-form-item label="组合产品名称"><el-input v-model="bundleForm.name" maxlength="100" /></el-form-item>
          <el-form-item label="销售端">
            <el-radio-group v-model="bundleForm.type" @change="loadBundleComponents">
              <el-radio-button v-if="canSupply" label="offline">售票窗口</el-radio-button>
              <el-radio-button label="online">线上</el-radio-button>
            </el-radio-group>
          </el-form-item>
        </div>
        <el-form-item label="组合售价">
          <el-input-number v-model="bundleForm.retail_price" :min="0.01" :precision="2" class="w-full" />
        </el-form-item>
        <div class="flex items-center justify-between mb-2">
          <strong class="text-sm">产品组件</strong>
          <span class="text-xs" :class="allocationDifference === 0 ? 'text-green-600' : 'text-orange-600'">
            已分摊 ¥{{ bundleAllocationTotal.toFixed(2) }}，{{ allocationDifference === 0 ? '金额一致' : `还差 ¥${allocationDifference.toFixed(2)}` }}
          </span>
        </div>
        <div v-for="(component, index) in bundleForm.components" :key="index" class="grid grid-cols-[1fr_110px_150px_36px] gap-2 items-end mb-3">
          <el-form-item label="供应商产品" class="mb-0">
            <el-select v-model="component.seller_product_id" filterable class="w-full" placeholder="选择已导入产品">
              <el-option v-for="option in eligibleBundleComponents" :key="option.seller_product_id" :value="option.seller_product_id"
                :disabled="isBundleComponentSelected(option.seller_product_id, index)"
                :label="`${option.supplier_name} · ${option.seller_product_name}`" />
            </el-select>
          </el-form-item>
          <el-form-item label="数量" class="mb-0"><el-input-number v-model="component.quantity" :min="1" :max="100" :precision="0" class="w-full" /></el-form-item>
          <el-form-item label="售价分摊" class="mb-0"><el-input-number v-model="component.allocation" :min="0.01" :precision="2" class="w-full" /></el-form-item>
          <el-button text type="danger" aria-label="删除组件" @click="removeBundleComponent(index)">×</el-button>
        </div>
        <el-button plain class="w-full" @click="addBundleComponent">添加组件</el-button>
        <el-alert v-if="bundleForm.components.length < 2" class="mt-3" type="warning" :closable="false" title="组合产品至少需要两个不同供应商的产品" />
      </el-form>
      <template #footer>
        <el-button @click="bundleFormVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingBundle" @click="saveBundle">保存并下架</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { Connection, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { hasPermission } from '@/utils/permissions'
import request from '@/utils/request'
import { activeCapabilitySet, configuredSupplierBusinessTypeSet, isActiveScenicSupplier, isScenicHistorySupplier, readStoredUser } from '@/utils/tenantAccess'

const currentUser = readStoredUser()
const activeCapabilities = activeCapabilitySet(currentUser)
const configuredBusinessTypes = configuredSupplierBusinessTypeSet(currentUser)
const canWrite = hasPermission(currentUser, 'distribution.write')
const canSupply = computed(() => isActiveScenicSupplier(currentUser))
const canViewSupplyHistory = computed(() => isScenicHistorySupplier(currentUser))
const canSupplierWrite = computed(() => canWrite && canSupply.value)
const canSupplierHistoryWrite = computed(() => canWrite && canViewSupplyHistory.value)
const canDistribute = computed(() => activeCapabilities.has('distributor'))
const activeTab = ref(canDistribute.value ? 'suppliers' : 'agents')

// Suppliers State
const loadingSuppliers = ref(false)
const suppliers = ref<any[]>([])

// Agents State
const loadingAgents = ref(false)
const agents = ref<any[]>([])
const loadingFulfillments = ref(false)
const fulfillments = ref<any[]>([])
const fulfillmentStatus = ref('')
const fulfillmentDistributorId = ref<number | undefined>()
const fulfillmentDrawer = ref(false)
const loadingFulfillmentDetail = ref(false)
const fulfillmentDetail = ref<any>(null)
const usedRefundDialog = ref(false)
const usedRefundSaving = ref(false)
const usedRefundReason = ref('')
const usedRefundTarget = reactive<any>({ item: null, ticket: null })

// Apply Dialog State
const dialogVisible = ref(false)
const targetSystemCode = ref('')
const searching = ref(false)
const applying = ref(false)
const foundSupplier = ref<any>(null)

// Sourcing State
const sourcingDialogVisible = ref(false)
const loadingProducts = ref(false)
const supplierProducts = ref<any[]>([])
const currentSupplierId = ref(0)

// Import State
const importDialogVisible = ref(false)
const importing = ref(false)
const importForm = reactive({
    source_product_id: 0,
    name: '',
    price: 0,
    settlement_price: 0,
    channels: ['online']
})

const offersDialogVisible = ref(false)
const offerFormVisible = ref(false)
const loadingOffers = ref(false)
const savingOffer = ref(false)
const offers = ref<any[]>([])
const sourceProducts = ref<any[]>([])
const packageProductIds = ref(new Set<number>())
const selectedDistributorId = ref(0)
const selectedDistributor = ref<any>(null)
const activeAgents = computed(() => agents.value.filter((agent: any) => agent.status === 'active'))
const editingOffer = ref(false)
const offerForm = reactive({
    source_product_id: null as number | null,
    settlement_price: 0,
    minimum_retail_price: 0,
    quota: 0,
    commission_bps: 0,
    allowed_channels: 'window,online,ota'
})
const offerChannels = ref(['window', 'online', 'ota'])
const sourceProductName = (productID: number) => sourceProducts.value.find((product: any) => Number(product.id) === Number(productID))?.name || `产品 ${productID}`

const loadingBundles = ref(false)
const savingBundle = ref(false)
const bundleFormVisible = ref(false)
const bundles = ref<any[]>([])
const eligibleBundleComponents = ref<any[]>([])
const bundleForm = reactive<any>({ id: 0, name: '', type: canSupply.value ? 'offline' : 'online', retail_price: 0, components: [] })
const bundleAllocationTotal = computed(() => bundleForm.components.reduce((sum: number, item: any) => sum + Number(item.allocation || 0), 0))
const allocationDifference = computed(() => Number((Number(bundleForm.retail_price || 0) - bundleAllocationTotal.value).toFixed(2)))

// Methods
const fetchSuppliers = async () => {
  loadingSuppliers.value = true
  try {
     const res = await request.get('/distribution/suppliers')
     suppliers.value = res.data.data || []
  } catch (e) {
     ElMessage.error('获取供应商列表失败')
  } finally {
     loadingSuppliers.value = false
  }
}

const fetchAgents = async () => {
  loadingAgents.value = true
  try {
     const res = await request.get('/distribution/agents')
     agents.value = res.data.data || []
  } catch (e) {
     ElMessage.error('获取分销商列表失败')
  } finally {
     loadingAgents.value = false
  }
}

const handleTabChange = (tabName: string) => {
    if (tabName === 'suppliers') {
        fetchSuppliers()
    } else if (tabName === 'agents') {
        fetchAgents()
	} else if (tabName === 'fulfillments') {
		Promise.all([fetchAgents(), fetchFulfillments()])
	} else if (tabName === 'bundles') {
		fetchBundles()
	}
}

const fetchBundles = async () => {
  loadingBundles.value = true
  try {
    bundles.value = (await request.get('/distribution/bundles')).data.data || []
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '组合产品加载失败')
  } finally {
    loadingBundles.value = false
  }
}

const loadBundleComponents = async () => {
  if (!canSupply.value && bundleForm.type === 'offline') bundleForm.type = 'online'
  try {
    eligibleBundleComponents.value = (await request.get('/distribution/bundle-components', { params: { type: bundleForm.type } })).data.data || []
  } catch (e: any) {
    eligibleBundleComponents.value = []
    ElMessage.error(e.response?.data?.error || '可用供应商产品加载失败')
  }
}

const addBundleComponent = () => bundleForm.components.push({ seller_product_id: null, quantity: 1, allocation: 0 })
const removeBundleComponent = (index: number | string) => bundleForm.components.splice(Number(index), 1)
const isBundleComponentSelected = (productID: number, currentIndex: number | string) => bundleForm.components.some((item: any, itemIndex: number) => itemIndex !== Number(currentIndex) && item.seller_product_id === productID)

const openBundleForm = async (row?: any) => {
  bundleForm.id = row?.id || 0
  bundleForm.name = row?.name || ''
  bundleForm.type = row?.type === 'offline' && canSupply.value ? 'offline' : 'online'
  bundleForm.retail_price = row ? Number(row.retail_price_cents || 0) / 100 : 0
  bundleForm.components = row
    ? (row.components || []).map((item: any) => ({ seller_product_id: item.seller_product_id, quantity: item.quantity, allocation: Number(item.retail_allocation_cents || 0) / 100 }))
    : [{ seller_product_id: null, quantity: 1, allocation: 0 }, { seller_product_id: null, quantity: 1, allocation: 0 }]
  await loadBundleComponents()
  bundleFormVisible.value = true
}

const saveBundle = async () => {
  if (!canSupply.value && bundleForm.type === 'offline') {
    ElMessage.error('当前商户不具备窗口售票能力，只能创建线上组合产品')
    return
  }
  if (!bundleForm.name.trim() || bundleForm.retail_price <= 0 || bundleForm.components.length < 2 || allocationDifference.value !== 0 || bundleForm.components.some((item: any) => !item.seller_product_id || item.quantity <= 0 || item.allocation <= 0)) {
    ElMessage.warning('请填写完整信息，并确保组件分摊金额等于组合售价')
    return
  }
  savingBundle.value = true
  const payload = {
    name: bundleForm.name.trim(), type: bundleForm.type, retail_price_cents: Math.round(bundleForm.retail_price * 100),
    components: bundleForm.components.map((item: any) => ({ seller_product_id: item.seller_product_id, quantity: item.quantity, retail_allocation_cents: Math.round(item.allocation * 100) }))
  }
  try {
    if (bundleForm.id) await request.put(`/distribution/bundles/${bundleForm.id}`, payload)
    else await request.post('/distribution/bundles', payload)
    ElMessage.success('组合产品已保存，请确认组件后再上架')
    bundleFormVisible.value = false
    await fetchBundles()
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '组合产品保存失败')
  } finally {
    savingBundle.value = false
  }
}

const changeBundleStatus = async (row: any, status: string) => {
  try {
    let reason = ''
    if (status === 'offline') {
      const result = await ElMessageBox.prompt('请输入下架原因', '下架组合产品', { inputPattern: /\S+/, inputErrorMessage: '必须填写原因' })
      reason = result.value
    }
    await request.patch(`/distribution/bundles/${row.id}/status`, { status, reason })
    ElMessage.success(status === 'online' ? '组合产品已上架' : '组合产品已下架')
    await fetchBundles()
  } catch (e: any) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(e.response?.data?.error || '状态更新失败')
  }
}

const fetchFulfillments = async () => {
    loadingFulfillments.value = true
    try {
        const params: Record<string, string | number> = { page: 1, page_size: 100 }
        if (fulfillmentStatus.value) params.status = fulfillmentStatus.value
        if (fulfillmentDistributorId.value) params.distributor_tenant_id = fulfillmentDistributorId.value
        const res = await request.get('/distribution/fulfillments', { params })
        fulfillments.value = res.data.data || []
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '履约工作列表加载失败')
    } finally {
        loadingFulfillments.value = false
    }
}

const openFulfillment = async (row: any) => {
    fulfillmentDrawer.value = true
    loadingFulfillmentDetail.value = true
    fulfillmentDetail.value = null
    try {
        fulfillmentDetail.value = (await request.get(`/distribution/fulfillments/${row.id}`)).data
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '履约详情加载失败')
    } finally {
        loadingFulfillmentDetail.value = false
    }
}

const openUsedRefund = (item: any, ticket: any) => {
    usedRefundTarget.item = item
    usedRefundTarget.ticket = ticket
    usedRefundReason.value = ''
    usedRefundDialog.value = true
}

const submitUsedRefund = async () => {
    if (!usedRefundReason.value.trim()) { ElMessage.warning('请填写退票原因'); return }
    usedRefundSaving.value = true
    try {
        const fulfillmentID = Number(fulfillmentDetail.value?.fulfillment?.id || 0)
        const response = await request.post(`/distribution/fulfillments/${fulfillmentID}/used-refunds`, {
            idempotency_key: `supplier-used-refund-${fulfillmentID}-${usedRefundTarget.ticket.ticket_code}-${Date.now()}`,
            ticket_codes: [usedRefundTarget.ticket.ticket_code],
            reason: usedRefundReason.value.trim(),
        })
        usedRefundDialog.value = false
        ElMessage.success(['pending', 'group_pending'].includes(response.data.status) ? '退票已提交，等待原支付渠道确认' : '退票已完成')
        await Promise.all([openFulfillment({ id: fulfillmentID }), fetchFulfillments()])
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '已核销票退票失败')
    } finally {
        usedRefundSaving.value = false
    }
}

const centsToYuan = (value: number) => (Number(value || 0) / 100).toFixed(2)
const formatDate = (value: string) => value ? new Date(value).toLocaleDateString('zh-CN') : '未指定日期'
const formatDateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const fulfillmentStatusText = (value: string) => ({ reserved: '已预占', paid: '已支付', fulfilled: '已履约', cancelled: '已取消' } as any)[value] || '未知状态'
const settlementStatusText = (value: string) => ({ draft: '草稿', supplier_confirmed: '供应商已确认', confirmed: '双方已确认', disputed: '有争议', paid: '已付款' } as any)[value] || '未知状态'
const ticketStatusText = (value: string) => ({ issued: '已出票', active: '可使用', unused: '未使用', used: '已核销', refunded: '已退款', void: '已作废', expired: '已过期' } as any)[value] || '未知状态'
const afterSaleTypeText = (value: string) => ({ refund: '退票', reschedule: '改期', exchange: '换票', void: '作废', reissue: '补打' } as any)[value] || '其他售后'
const afterSaleStatusText = (value: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '失败' } as any)[value] || '未知状态'
const refundStatusText = (value: string) => ({ group_pending: '等待原支付渠道', group_succeeded: '已退款', pending: '等待原支付渠道', processing: '处理中', submitted: '渠道处理中', succeeded: '已退款', failed: '失败', manual_review: '待人工复核' } as any)[value] || '未知状态'
const refundTicketCodes = (value: string) => { try { return (JSON.parse(value || '[]') || []).join('、') || '-' } catch { return value || '-' } }
const offerStatusText = (value: string) => ({ active: '生效中', suspended: '已暂停', expired: '已终止' } as Record<string, string>)[value] || '未知状态'
const channelText = (value: string) => String(value || '').split(',').map(item => ({ window: '售票窗口', online: '线上商城', ota: '外部渠道' } as Record<string, string>)[item.trim()] || '其他渠道').join('、')

const handleSearch = async () => {
  if (!targetSystemCode.value) return
  searching.value = true
  foundSupplier.value = null
  try {
    const res = await request.get('/distribution/search', { params: { code: targetSystemCode.value }})
    foundSupplier.value = res.data.data
  } catch (e: any) {
    ElMessage.warning(e.response?.data?.error || '未找到该供应商')
    foundSupplier.value = null
  } finally {
    searching.value = false
  }
}

const handleApply = async () => {
    if (!foundSupplier.value) return
    applying.value = true
    try {
        await request.post('/distribution/apply', { system_code: foundSupplier.value.code })
        ElMessage.success('申请已提交')
        dialogVisible.value = false
        foundSupplier.value = null
        targetSystemCode.value = ''
        fetchSuppliers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '申请失败')
    } finally {
        applying.value = false
    }
}

// Recharge Logic
const rechargeDialogVisible = ref(false)
const recharging = ref(false)
const rechargeForm = reactive({
    agent_id: 0, // Relationship ID
    agent_name: '',
    amount: 1000
})

const handleRecharge = (row: any) => {
    rechargeForm.agent_id = row.id // Relationship ID
    rechargeForm.agent_name = row.agent_name || row.supplier_name // Handle both views if needed, but usually Supplier recharges Agent
    rechargeForm.amount = 1000
    rechargeDialogVisible.value = true
}

const confirmRecharge = async () => {
    if (rechargeForm.amount <= 0) {
        ElMessage.warning('金额必须大于0')
        return
    }
    recharging.value = true
    try {
        await request.post(`/distribution/agents/${rechargeForm.agent_id}/recharge`, {
            amount_cents: Math.round(rechargeForm.amount * 100),
            idempotency_key: `admin-recharge-${rechargeForm.agent_id}-${Date.now()}-${Math.random().toString(36).slice(2)}`
        })
        ElMessage.success('充值成功')
        rechargeDialogVisible.value = false
        // Refresh lists
        if (activeTab.value === 'suppliers') fetchSuppliers()
        else fetchAgents()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '充值失败')
    } finally {
        recharging.value = false
    }
}

const handleOffers = async (row: any) => {
    selectedDistributorId.value = row.agent_tenant_id
    selectedDistributor.value = row
    offersDialogVisible.value = true
    await Promise.all([loadOffers(), loadSourceProducts()])
}

const loadOffers = async () => {
    if (!selectedDistributorId.value) return
    loadingOffers.value = true
    try {
        const res = await request.get('/distribution/offers', { params: { distributor_tenant_id: selectedDistributorId.value, page: 1, page_size: 100 } })
        offers.value = res.data.data || []
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '供货报价加载失败')
    } finally {
        loadingOffers.value = false
    }
}

const loadSourceProducts = async () => {
    try {
        const productsResponse = await request.get('/products', { params: { page: 1, page_size: 100 } })
        if (configuredBusinessTypes.has('hotel')) {
            const packagesResponse = await request.get('/scenic-hotel-packages')
            packageProductIds.value = new Set((packagesResponse.data.data || []).map((item: any) => Number(item.product_id)))
        } else {
            packageProductIds.value = new Set()
        }
        sourceProducts.value = (productsResponse.data.data || []).filter((product: any) => product.status === 'online' && product.is_distributable && !packageProductIds.value.has(Number(product.id)))
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '可供货产品加载失败')
    }
}

const openOfferForm = (row?: any) => {
    editingOffer.value = Boolean(row)
    offerForm.source_product_id = row?.source_product_id || sourceProducts.value[0]?.id || null
    offerForm.settlement_price = Number(row?.settlement_price || 0)
    offerForm.minimum_retail_price = Number(row?.minimum_retail_price_cents || 0) / 100
    offerForm.quota = Number(row?.quota || 0)
    offerForm.commission_bps = Number(row?.commission_bps || 0)
    offerForm.allowed_channels = row?.allowed_channels || 'window,online,ota'
    offerChannels.value = offerForm.allowed_channels.split(',').filter(Boolean)
    offerFormVisible.value = true
}

const createOffer = async () => {
    if (!selectedDistributorId.value || !offerForm.source_product_id || offerForm.settlement_price <= 0 || !offerChannels.value.length) {
        ElMessage.warning('请选择供货产品，并填写结算价和销售渠道')
        return
    }
    savingOffer.value = true
    try {
        offerForm.allowed_channels = offerChannels.value.join(',')
        await request.post('/distribution/offers', { distributor_tenant_id: selectedDistributorId.value, ...offerForm })
        ElMessage.success('供货报价已创建')
        offerFormVisible.value = false
        await loadOffers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '供货报价创建失败')
    } finally {
        savingOffer.value = false
    }
}

const handleOfferStatus = async (row: any, status: string) => {
    try {
        await request.patch(`/distribution/offers/${row.id}/status`, { status, reason: `供应商在管理端将报价状态调整为${offerStatusText(status)}` })
        ElMessage.success('供货报价状态已更新')
        await loadOffers()
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '供货报价状态更新失败')
    }
}

const handleAudit = async (row: any, status: string) => {
    const actionText = status === 'active' ? '通过' : '拒绝'
    try {
        await ElMessageBox.confirm(`确定要${actionText}该分销商的申请吗？`, '提示', {
            type: status === 'active' ? 'success' : 'warning'
        })
        await request.post(`/distribution/agents/${row.id}/audit`, { status })
        ElMessage.success('操作成功')
        fetchAgents()
    } catch (e) {
        // cancelled or error
    }
}

const handleSourcing = async (row: any) => {
    currentSupplierId.value = row.supplier_tenant_id // Note: row structure depends on API
    sourcingDialogVisible.value = true
    loadingProducts.value = true
    try {
        // We need supplier_id, from DB struct it is SupplierTenantID
        const res = await request.get('/distribution/products', { params: { supplier_id: row.supplier_tenant_id }})
        supplierProducts.value = res.data.data || []
    } catch (e) {
        ElMessage.error('获取商品列表失败')
    } finally {
        loadingProducts.value = false
    }
}

const handleImportConfig = (product: any) => {
    importForm.source_product_id = product.id
    importForm.name = product.name
    importForm.price = product.price // Default to retail price
    importForm.settlement_price = product.settlement_price
    importForm.channels = ['online']
    importDialogVisible.value = true
}

const confirmImport = async () => {
    if (importForm.channels.length === 0) {
        ElMessage.warning('请至少选择一个上架渠道')
        return
    }
    importing.value = true
    try {
        for (const channel of importForm.channels) {
            await request.post('/distribution/products/import', {
                source_product_id: importForm.source_product_id,
                name: importForm.name + (channel === 'offline' && importForm.channels.length > 1 ? ' (线下)' : ''),
                price: importForm.price,
                type: channel
            })
        }
        ElMessage.success('对接成功！请前往“门票管理”查看')
        importDialogVisible.value = false
        sourcingDialogVisible.value = false // Optionally close parent
    } catch (e: any) {
        ElMessage.error(e.response?.data?.error || '对接失败')
    } finally {
        importing.value = false
    }
}


const getStatusType = (status: string) => {
    const map: any = { active: 'success', pending: 'warning', rejected: 'danger' }
    return map[status] || 'info'
}

const getStatusText = (status: string) => {
    const map: any = { active: '合作中', pending: '待审核', rejected: '已拒绝' }
    return map[status] || '未知状态'
}

const getLevelText = (level: string) => {
    const map: any = { standard: '普通代理', core: '核心代理', diamond: '金牌代理' }
    return map[level] || level
}

onMounted(() => {
    if (canDistribute.value) fetchSuppliers()
    if (canViewSupplyHistory.value) fetchAgents()
})
</script>
