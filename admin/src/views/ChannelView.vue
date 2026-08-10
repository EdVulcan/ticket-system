<template>
  <section class="space-y-5">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-xl font-semibold text-gray-900">渠道连接</h2>
        <p class="text-sm text-gray-500 mt-1">管理独立渠道凭据、权限、商品映射和账单导入。</p>
      </div>
      <div class="flex gap-2">
        <el-button :icon="Refresh" circle title="刷新" @click="load" />
        <el-button v-if="canWrite" type="primary" :icon="Plus" @click="createDialog = true">新增渠道</el-button>
      </div>
    </div>

    <el-table :data="accounts" v-loading="loading" stripe>
      <el-table-column prop="code" label="渠道编码" width="180" />
      <el-table-column label="适配器类型" width="140"><template #default="{row}">{{ adapterTypeText(row.type) }}</template></el-table-column>
      <el-table-column label="接口参数" width="120"><template #default="{row}"><el-tag v-if="['ctrip', 'xiaohongshu'].includes(row.type)" :type="row.protocol_configured ? 'success' : 'danger'" effect="plain">{{ row.protocol_configured ? '已配置' : '待配置' }}</el-tag><span v-else>-</span></template></el-table-column>
      <el-table-column prop="status" label="状态" width="120"><template #default="{row}"><el-tag :type="row.status === 'active' ? 'success' : row.status === 'sandbox' ? 'warning' : 'info'">{{ accountStatusText(row.status) }}</el-tag></template></el-table-column>
      <el-table-column prop="rate_limit_per_min" label="限流/分钟" width="120" />
      <el-table-column prop="permissions_json" label="权限" min-width="220" show-overflow-tooltip />
      <el-table-column label="操作" width="700" fixed="right">
        <template #default="{row}">
          <el-button v-if="canWrite && row.type === 'ctrip'" link type="primary" @click="openCtripConfig(row)">携程参数</el-button>
          <el-button v-if="canWrite && row.type === 'xiaohongshu'" link type="primary" @click="openXiaohongshuConfig(row)">小红书参数</el-button>
          <el-button v-if="canWrite && row.type === 'ctrip' && row.status === 'sandbox'" link type="primary" @click="openCtripSandboxConsume(row)">沙箱核销测试</el-button>
          <el-button v-if="canWrite" link type="primary" @click="openMapping(row)">商品映射</el-button>
          <el-button link type="primary" @click="openOrders(row)">渠道订单</el-button>
          <el-button link type="primary" @click="openRequests(row)">请求日志</el-button>
          <el-button link type="primary" @click="openReconciliations(row)">账单对账</el-button>
          <el-button v-if="canWrite" link type="warning" @click="toggleStatus(row)">{{ row.status === 'disabled' ? '启用' : '停用' }}</el-button>
          <el-button v-if="canWrite && !['ctrip', 'xiaohongshu'].includes(row.type)" link type="danger" @click="rotate(row)">轮换密钥</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="createDialog" title="新增渠道账号" width="520px">
      <el-form :model="form" label-position="top">
        <el-form-item label="渠道编码"><el-input v-model="form.code" placeholder="例如：携程正式渠道" /></el-form-item>
        <el-form-item label="适配器类型"><el-select v-model="form.type" class="w-full" @change="handleAdapterTypeChange"><el-option label="通用渠道" value="core" /><el-option label="携程" value="ctrip" /><el-option label="小红书" value="xiaohongshu" /></el-select></el-form-item>
        <template v-if="form.type === 'ctrip'">
          <el-alert class="mb-4" type="info" :closable="false" title="请填写携程沙箱“订单参数”中的接口账号、接口密钥、AES 密钥和初始向量。" />
          <el-form-item label="携程接口账号"><el-input v-model="form.app_id" autocomplete="off" /></el-form-item>
          <el-form-item label="携程接口密钥"><el-input v-model="form.secret" type="password" show-password autocomplete="new-password" /></el-form-item>
          <el-form-item label="AES 加密密钥"><el-input v-model="form.aes_key" type="password" show-password autocomplete="new-password" maxlength="16" /></el-form-item>
          <el-form-item label="AES 初始向量"><el-input v-model="form.aes_iv" type="password" show-password autocomplete="new-password" maxlength="16" /></el-form-item>
        </template>
        <template v-else-if="form.type === 'xiaohongshu'">
          <el-alert class="mb-4" type="info" :closable="false" title="使用景区现有专业号小程序的 AppID 和 AppSecret；密钥保存后不会回显。" />
          <el-form-item label="小程序 AppID"><el-input v-model="form.app_id" autocomplete="off" /></el-form-item>
          <el-form-item label="小程序 AppSecret"><el-input v-model="form.secret" type="password" show-password autocomplete="new-password" /></el-form-item>
        </template>
        <el-form-item v-else label="初始密钥"><el-input v-model="form.secret" type="password" show-password /></el-form-item>
        <el-form-item v-if="form.type !== 'xiaohongshu'" label="运行环境"><el-radio-group v-model="form.status"><el-radio-button label="sandbox">测试环境</el-radio-button><el-radio-button label="active">正式环境</el-radio-button></el-radio-group></el-form-item>
        <el-alert v-else class="mb-4" type="warning" :closable="false" title="小红书公开接口未提供独立沙箱地址，保存配置不会自动创建或修改商品。" />
        <el-form-item label="接口权限配置"><el-input v-model="form.permissions_json" /></el-form-item>
        <el-form-item label="每分钟请求上限"><el-input-number v-model="form.rate_limit_per_min" :min="1" :max="100000" /></el-form-item>
        <el-form-item label="允许访问的网络地址"><el-input v-model="form.allowed_ips_json" placeholder='例如 ["203.0.113.5"]' /></el-form-item>
      </el-form>
      <template #footer><el-button @click="createDialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>

    <el-dialog v-model="xiaohongshuConfigDialog" title="小红书小程序参数" width="620px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" title="AppSecret、Token 和 EncodingAESKey 均加密保存且不会回显。更换参数后，小红书后台必须同步更新。" />
      <el-form class="mt-4" :model="xiaohongshuConfig" label-position="top">
        <el-form-item label="小程序 AppID"><el-input v-model="xiaohongshuConfig.app_id" autocomplete="off" /></el-form-item>
        <el-form-item label="小程序 AppSecret"><el-input v-model="xiaohongshuConfig.app_secret" type="password" show-password autocomplete="new-password" /></el-form-item>
        <el-divider content-position="left">消息推送配置</el-divider>
        <el-form-item label="URL（服务器地址）">
          <el-input :model-value="xiaohongshuEndpoint()" readonly><template #append><el-button @click="copyText(xiaohongshuEndpoint(), 'URL')">复制</el-button></template></el-input>
        </el-form-item>
        <el-form-item label="Token（令牌）">
          <el-input v-model="xiaohongshuConfig.message_token" autocomplete="new-password" maxlength="32"><template #append><el-button @click="copyText(xiaohongshuConfig.message_token, 'Token')">复制</el-button></template></el-input>
        </el-form-item>
        <el-form-item label="EncodingAESKey（消息加密密钥）">
          <el-input v-model="xiaohongshuConfig.encoding_aes_key" type="password" show-password autocomplete="new-password" maxlength="43"><template #append><el-button @click="copyText(xiaohongshuConfig.encoding_aes_key, 'EncodingAESKey')">复制</el-button></template></el-input>
        </el-form-item>
        <el-button plain @click="generateXiaohongshuMessageKeys">随机生成 Token 和 EncodingAESKey</el-button>
        <el-alert v-if="xiaohongshuConfigSaved" class="mt-4" type="success" :closable="false" title="参数已保存。现在将 URL、Token 和 EncodingAESKey 分别复制到小红书后台并提交校验。" />
      </el-form>
      <template #footer><el-button @click="xiaohongshuConfigDialog = false">关闭</el-button><el-button type="primary" :loading="xiaohongshuConfigSaving" @click="saveXiaohongshuConfig">保存参数</el-button></template>
    </el-dialog>

    <el-dialog v-model="ctripConfigDialog" title="携程订单接口参数" width="560px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" title="保存后旧参数立即失效。密钥不会在页面回显，四项内容必须重新完整填写。" />
      <el-form class="mt-4" :model="ctripConfig" label-position="top">
        <el-form-item label="携程接口账号"><el-input v-model="ctripConfig.account_id" autocomplete="off" /></el-form-item>
        <el-form-item label="携程接口密钥"><el-input v-model="ctripConfig.sign_key" type="password" show-password autocomplete="new-password" /></el-form-item>
        <el-form-item label="AES 加密密钥（16 位）"><el-input v-model="ctripConfig.aes_key" type="password" show-password maxlength="16" autocomplete="new-password" /></el-form-item>
        <el-form-item label="AES 初始向量（16 位）"><el-input v-model="ctripConfig.aes_iv" type="password" show-password maxlength="16" autocomplete="new-password" /></el-form-item>
        <el-form-item label="提供给携程的商家接口地址"><el-input :model-value="ctripEndpoint" readonly><template #append><el-button @click="copyCtripEndpoint">复制</el-button></template></el-input></el-form-item>
      </el-form>
      <template #footer><el-button @click="ctripConfigDialog = false">取消</el-button><el-button type="primary" :loading="ctripConfigSaving" @click="saveCtripConfig">保存参数</el-button></template>
    </el-dialog>

    <el-dialog v-model="ctripSandboxConsumeDialog" title="携程沙箱模拟核销" width="520px" :close-on-click-modal="false">
      <el-alert type="warning" :closable="false" title="仅用于携程遍历测试。正式订单必须由真实闸机核销，系统不会提供模拟入口。" />
      <el-form class="mt-4" label-position="top">
        <el-form-item label="供应商订单号"><el-input v-model="ctripSandboxSupplierOrderID" placeholder="例如：ORD..." /></el-form-item>
      </el-form>
      <template #footer><el-button @click="ctripSandboxConsumeDialog = false">取消</el-button><el-button type="primary" :loading="ctripSandboxConsuming" @click="simulateCtripSandboxConsumption">确认模拟核销</el-button></template>
    </el-dialog>

    <el-dialog v-model="mappingDialog" title="商品映射" width="1060px">
      <el-alert v-if="selectedAccount?.type === 'ctrip'" class="mb-4" type="info" :closable="false" title="外部编码填写携程 PLU；价格按当前携程合同单独设置，不会改动景区产品原价。" />
      <el-alert v-else-if="selectedAccount?.type === 'xiaohongshu'" class="mb-4" type="info" :closable="false" title="外部编码填写小红书 out_product_id。商品同步将在 POI、类目和小程序页面路径配置完成后开放。" />
      <div class="grid grid-cols-1 gap-2 mb-4 md:grid-cols-5">
        <el-input v-model="mapping.external_code" placeholder="外部商品编码 / PLU" />
        <el-select v-model="mapping.product_id" filterable placeholder="选择本商户产品">
          <el-option v-for="product in products" :key="product.id" :label="product.name" :value="product.id" />
        </el-select>
        <el-input v-if="selectedAccount?.type === 'xiaohongshu'" v-model="mapping.display_name" placeholder="小程序展示名称" />
        <el-input-number v-if="selectedAccount?.type === 'xiaohongshu'" v-model="mapping.channel_sale_yuan" :min="0.01" :precision="2" :step="1" controls-position="right" placeholder="小红书售价" class="w-full" />
        <el-input-number v-if="selectedAccount?.type === 'ctrip'" v-model="mapping.channel_sale_yuan" :min="0.01" :precision="2" :step="1" controls-position="right" placeholder="携程销售价" class="w-full" />
        <el-input-number v-if="selectedAccount?.type === 'ctrip'" v-model="mapping.channel_cost_yuan" :min="0" :precision="2" :step="1" controls-position="right" placeholder="携程结算价" class="w-full" />
        <el-button v-if="canWrite" type="primary" @click="addMapping">添加</el-button>
      </div>
      <div v-if="selectedAccount?.type === 'ctrip'" class="flex flex-wrap items-center gap-2 mb-3">
        <el-date-picker v-model="syncDateRange" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :clearable="false" />
        <span class="text-sm text-gray-500">同步范围最多 90 天</span>
      </div>
      <el-table :data="mappings" stripe>
        <el-table-column prop="external_code" label="外部编码" min-width="150" />
        <el-table-column label="本地产品" min-width="180"><template #default="{ row }">{{ productName(row.product_id) }}</template></el-table-column>
        <el-table-column v-if="selectedAccount?.type === 'xiaohongshu'" prop="display_name" label="小程序展示名称" min-width="180" />
        <el-table-column v-if="selectedAccount?.type === 'xiaohongshu'" label="小红书售价" width="120"><template #default="{ row }">¥{{ cents(row.channel_sale_cents) }}</template></el-table-column>
        <el-table-column v-if="selectedAccount?.type === 'ctrip'" label="携程销售价" width="120"><template #default="{ row }">¥{{ cents(row.channel_sale_cents) }}</template></el-table-column>
        <el-table-column v-if="selectedAccount?.type === 'ctrip'" label="携程结算价" width="120"><template #default="{ row }">¥{{ cents(row.channel_cost_cents) }}</template></el-table-column>
        <el-table-column label="状态" width="90"><template #default="{ row }">{{ mappingStatusText(row.status) }}</template></el-table-column>
        <el-table-column v-if="selectedAccount?.type === 'xiaohongshu'" label="操作" width="100"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openXiaohongshuMappingEdit(row)">编辑映射</el-button></template></el-table-column>
        <el-table-column v-if="selectedAccount?.type === 'ctrip'" label="操作" width="210"><template #default="{ row }"><el-button v-if="canWrite" link type="primary" @click="openPricing(row)">修改价格</el-button><el-button v-if="canWrite" link type="primary" :loading="syncingMappingID === row.id" @click="syncMapping(row)">同步价格库存</el-button></template></el-table-column>
      </el-table>
      <template v-if="selectedAccount?.type === 'ctrip'">
        <div class="flex items-center justify-between mt-5 mb-2"><h3 class="font-medium text-gray-900">最近出站任务</h3><el-button link type="primary" @click="loadCtripSyncTasks">刷新</el-button></div>
        <el-table :data="ctripSyncTasks" size="small" max-height="240" empty-text="暂无出站记录">
          <el-table-column label="内容" width="100"><template #default="{ row }">{{ ctripTaskKindText(row.kind) }}</template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="syncStatusType(row.status)">{{ syncStatusText(row.status) }}</el-tag></template></el-table-column>
          <el-table-column prop="attempt_count" label="尝试次数" width="90" />
          <el-table-column label="时间" width="180"><template #default="{ row }">{{ dateTime(row.completed_at || row.created_at) }}</template></el-table-column>
          <el-table-column label="结果" min-width="260" show-overflow-tooltip><template #default="{ row }">{{ row.last_error || row.result_message || '-' }}</template></el-table-column>
        </el-table>
      </template>
    </el-dialog>

    <el-dialog v-model="xiaohongshuMappingDialog" title="编辑小红书商品映射" width="520px" :close-on-click-modal="false">
      <el-form label-position="top">
        <el-form-item label="外部商品编码"><el-input v-model="xiaohongshuMapping.external_code" /></el-form-item>
        <el-form-item label="小程序展示名称"><el-input v-model="xiaohongshuMapping.display_name" placeholder="留空则使用本地产品名称" /></el-form-item>
        <el-form-item label="小红书售价"><el-input-number v-model="xiaohongshuMapping.channel_sale_yuan" :min="0.01" :precision="2" :step="1" controls-position="right" class="w-full" /></el-form-item>
        <el-form-item label="状态"><el-radio-group v-model="xiaohongshuMapping.status"><el-radio-button label="active">启用</el-radio-button><el-radio-button label="disabled">停用</el-radio-button></el-radio-group></el-form-item>
      </el-form>
      <template #footer><el-button @click="xiaohongshuMappingDialog = false">取消</el-button><el-button type="primary" :loading="xiaohongshuMappingSaving" @click="saveXiaohongshuMapping">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="pricingDialog" title="修改携程渠道价格" width="460px" :close-on-click-modal="false">
      <el-form label-position="top">
        <el-form-item label="携程销售价"><el-input-number v-model="pricing.channel_sale_yuan" :min="0.01" :precision="2" :step="1" controls-position="right" class="w-full" /></el-form-item>
        <el-form-item label="携程结算价"><el-input-number v-model="pricing.channel_cost_yuan" :min="0" :precision="2" :step="1" controls-position="right" class="w-full" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="pricingDialog = false">取消</el-button><el-button type="primary" :loading="pricingSaving" @click="savePricing">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="secretDialog" title="新渠道密钥" width="460px"><el-alert type="warning" :closable="false" title="密钥只在本次显示，请立即交给渠道方并安全保存。"/><el-input class="mt-4" :model-value="newSecret" readonly /></el-dialog>

    <el-dialog v-model="requestsDialog" :title="`渠道请求日志：${selectedAccount?.code || ''}`" width="1060px" :close-on-click-modal="false">
      <div class="mb-3 flex items-center gap-2">
        <el-select v-model="requestStatus" clearable placeholder="全部状态" style="width: 180px" @change="loadRequests">
          <el-option label="处理中" value="processing" />
          <el-option label="已完成" value="completed" />
          <el-option label="失败待处理" value="failed" />
          <el-option label="已授权重试" value="retryable" />
        </el-select>
        <el-button :icon="Refresh" @click="loadRequests">刷新</el-button>
        <span class="text-sm text-gray-500">共 {{ requestTotal }} 条</span>
      </div>
      <el-table :data="channelRequests" v-loading="requestsLoading" stripe height="480" empty-text="暂无请求记录">
        <el-table-column prop="request_id" label="请求编号" min-width="180" show-overflow-tooltip />
        <el-table-column prop="endpoint" label="接口" min-width="210" show-overflow-tooltip />
        <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="requestStatusType(row.status)">{{ requestStatusText(row.status) }}</el-tag></template></el-table-column>
        <el-table-column prop="response_status" label="响应码" width="90" />
        <el-table-column prop="attempt_count" label="尝试" width="80" />
        <el-table-column prop="remote_ip" label="来源网络地址" width="150" />
        <el-table-column label="最后尝试" width="180"><template #default="{ row }">{{ dateTime(row.last_attempt_at || row.created_at) }}</template></el-table-column>
        <el-table-column prop="response_json" label="响应摘要" min-width="220" show-overflow-tooltip />
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }"><el-button v-if="canWrite && row.status === 'failed'" link type="warning" @click="authorizeRetry(row)">授权重试</el-button></template>
        </el-table-column>
      </el-table>
      <template #footer><el-button @click="requestsDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="ordersDialog" :title="`渠道订单：${selectedAccount?.code || ''}`" width="1120px" :close-on-click-modal="false">
      <div class="mb-3 flex items-center gap-2">
        <el-input v-model="orderSearch" clearable placeholder="订单号、外部单号、姓名或手机号" style="width: 300px" @keyup.enter="loadOrders(1)" />
        <el-select v-model="orderStatus" clearable placeholder="全部状态" style="width: 150px" @change="loadOrders(1)">
          <el-option label="待支付" value="unpaid" />
          <el-option label="已支付" value="paid" />
          <el-option label="已完成" value="completed" />
          <el-option label="部分退款" value="partial_refunded" />
          <el-option label="已退款" value="refunded" />
          <el-option label="已取消" value="cancelled" />
        </el-select>
        <el-button type="primary" @click="loadOrders(1)">查询</el-button>
        <el-button :icon="Refresh" @click="loadOrders(orderPage)">刷新</el-button>
      </div>
      <el-table :data="channelOrders" v-loading="ordersLoading" stripe height="470" empty-text="暂无渠道订单">
        <el-table-column prop="external_no" label="外部单号" min-width="160" show-overflow-tooltip />
        <el-table-column prop="order_no" label="内部订单" min-width="170" show-overflow-tooltip />
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag effect="plain">{{ orderStatusText(row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="游客" min-width="150"><template #default="{ row }"><div>{{ row.contact_name || '-' }}</div><div class="text-xs text-gray-500">{{ row.contact_phone || '-' }}</div></template></el-table-column>
        <el-table-column label="票况" width="125"><template #default="{ row }"><div>{{ row.ticket_count }} 张</div><div class="text-xs text-gray-500">已核销 {{ row.used_ticket_count }} / 已退 {{ row.refunded_ticket_count }}</div></template></el-table-column>
        <el-table-column label="订单金额" width="110"><template #default="{ row }">¥{{ Number(row.total_amount || 0).toFixed(2) }}</template></el-table-column>
        <el-table-column label="实收/退款" width="130"><template #default="{ row }"><div>收 ¥{{ cents(row.paid_cents) }}</div><div class="text-xs text-gray-500">退 ¥{{ cents(row.refunded_cents) }}</div></template></el-table-column>
        <el-table-column label="下单时间" width="165"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" width="80" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openOrderDetail(row)">详情</el-button></template></el-table-column>
      </el-table>
      <div class="mt-3 flex justify-end"><el-pagination v-model:current-page="orderPage" :page-size="20" :total="orderTotal" layout="prev, pager, next, total" @current-change="loadOrders" /></div>
      <template #footer><el-button @click="ordersDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="orderDetailDialog" title="渠道订单详情" width="1080px" append-to-body>
      <div v-loading="orderDetailLoading">
        <el-descriptions v-if="orderDetail" :column="4" border>
          <el-descriptions-item label="外部单号">{{ orderDetail.order.external_no || '-' }}</el-descriptions-item>
          <el-descriptions-item label="内部订单">{{ orderDetail.order.order_no }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ orderStatusText(orderDetail.order.status) }}</el-descriptions-item>
          <el-descriptions-item label="金额">¥{{ Number(orderDetail.order.total_amount || 0).toFixed(2) }}</el-descriptions-item>
          <el-descriptions-item label="联系人">{{ orderDetail.order.contact_name || '-' }}</el-descriptions-item>
          <el-descriptions-item label="手机号">{{ orderDetail.order.contact_phone || '-' }}</el-descriptions-item>
          <el-descriptions-item label="创建时间" :span="2">{{ dateTime(orderDetail.order.created_at) }}</el-descriptions-item>
        </el-descriptions>
        <el-tabs v-if="orderDetail" class="mt-4">
          <el-tab-pane label="门票">
            <el-table :data="orderTickets(orderDetail.order)" stripe max-height="380" empty-text="暂无门票">
              <el-table-column prop="ticket_code" label="票码" min-width="180" />
              <el-table-column prop="product_name" label="产品" min-width="180" />
              <el-table-column prop="visitor_name" label="游客" width="120" />
              <el-table-column prop="visitor_phone" label="手机号" width="140" />
              <el-table-column label="状态" width="100"><template #default="{ row }">{{ ticketStatusText(row.status) }}</template></el-table-column>
              <el-table-column prop="check_in_count" label="核销次数" width="100" />
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`支付与退款 (${orderDetail.payments.length}/${orderDetail.refunds.length})`">
            <el-table :data="orderDetail.payments" stripe max-height="180" empty-text="暂无支付">
              <el-table-column prop="payment_no" label="支付单" min-width="160" /><el-table-column label="方式" width="100"><template #default="{ row }">{{ paymentMethodText(row.method) }}</template></el-table-column><el-table-column label="金额" width="120"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ paymentStatusText(row.status) }}</template></el-table-column><el-table-column prop="transaction_id" label="渠道流水" min-width="160" />
            </el-table>
            <el-table :data="orderDetail.refunds" stripe max-height="180" class="mt-3" empty-text="暂无退款">
              <el-table-column prop="refund_no" label="退款单" min-width="160" /><el-table-column label="方式" width="100"><template #default="{ row }">{{ paymentMethodText(row.method) }}</template></el-table-column><el-table-column label="金额" width="120"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ refundStatusText(row.status) }}</template></el-table-column><el-table-column prop="reason" label="原因" min-width="180" />
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`核销 (${orderDetail.check_ins.length})`">
            <el-table :data="orderDetail.check_ins" stripe max-height="380" empty-text="暂无核销">
              <el-table-column prop="ticket_code" label="票码" min-width="180" /><el-table-column label="结果" width="100"><template #default="{ row }">{{ checkInResultText(row.result) }}</template></el-table-column><el-table-column label="说明" min-width="180"><template #default="{ row }">{{ localizeDisplayText(row.message, '-') }}</template></el-table-column><el-table-column label="时间" width="180"><template #default="{ row }">{{ dateTime(row.check_in_time) }}</template></el-table-column>
            </el-table>
          </el-tab-pane>
          <el-tab-pane :label="`售后 (${orderDetail.after_sales.length})`">
            <el-table :data="orderDetail.after_sales" stripe max-height="380" empty-text="暂无售后">
              <el-table-column prop="request_no" label="售后单" min-width="170" /><el-table-column label="类型" width="100"><template #default="{ row }">{{ afterSaleTypeText(row.type) }}</template></el-table-column><el-table-column label="状态" width="110"><template #default="{ row }">{{ afterSaleStatusText(row.status) }}</template></el-table-column><el-table-column prop="reason" label="原因" min-width="200" /><el-table-column label="申请时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
            </el-table>
          </el-tab-pane>
        </el-tabs>
      </div>
      <template #footer><el-button @click="orderDetailDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="reconciliationsDialog" :title="`渠道账单对账：${selectedAccount?.code || ''}`" width="1000px" :close-on-click-modal="false">
      <div class="mb-4 flex justify-between">
        <div class="text-sm text-gray-500">导入渠道销售、支付、取消或退款账单，与本系统订单资金事实逐笔核对。</div>
        <el-button v-if="canWrite" type="primary" :icon="Plus" @click="billImportDialog = true">导入账单</el-button>
      </div>
      <el-table :data="reconciliations" v-loading="reconciliationsLoading" stripe height="430" empty-text="暂无对账批次">
        <el-table-column prop="idempotency_key" label="批次号" min-width="180" />
        <el-table-column prop="record_count" label="记录" width="80" />
        <el-table-column prop="matched_count" label="匹配" width="80" />
        <el-table-column label="差异" width="120"><template #default="{ row }">{{ signedCents(row.difference_cents) }}</template></el-table-column>
        <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="row.status === 'completed' ? 'success' : 'warning'">{{ row.status === 'completed' ? '一致' : '待复核' }}</el-tag></template></el-table-column>
        <el-table-column label="导入时间" width="180"><template #default="{ row }">{{ dateTime(row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" width="90"><template #default="{ row }"><el-button link type="primary" @click="openReconciliationDetail(row)">明细</el-button></template></el-table-column>
      </el-table>
      <template #footer><el-button @click="reconciliationsDialog = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="billImportDialog" title="导入渠道账单" width="680px" append-to-body>
      <el-form label-position="top">
        <el-form-item label="批次号" required><el-input v-model="billBatchKey" maxlength="120" /></el-form-item>
        <el-form-item label="账单内容" required>
          <el-input v-model="billText" type="textarea" :rows="9" placeholder="每行：外部单号,类型,金额(元),发生时间(可选)&#10;示例订单,收款,299.00,2026-08-01 10:30:00" />
          <div class="mt-1 text-xs text-gray-500">类型支持销售、收款、取消、退款；也兼容渠道提供的英文类型值。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="billImportDialog = false">取消</el-button><el-button type="primary" :loading="billImporting" @click="importBill">导入并核对</el-button></template>
    </el-dialog>

    <el-dialog v-model="reconciliationDetailDialog" title="渠道对账明细" width="1080px" append-to-body>
      <el-table :data="reconciliationDetail?.lines || []" v-loading="reconciliationDetailLoading" stripe height="500">
        <el-table-column prop="external_no" label="外部单号" min-width="170" />
        <el-table-column label="类型" width="90"><template #default="{ row }">{{ operationText(row.operation) }}</template></el-table-column>
        <el-table-column label="渠道金额" width="120"><template #default="{ row }">¥{{ cents(row.amount_cents) }}</template></el-table-column>
        <el-table-column prop="matched_order_no" label="内部订单" min-width="160" />
        <el-table-column prop="matched_payment_no" label="支付单" min-width="150" />
        <el-table-column prop="matched_refund_no" label="退款单" min-width="150" />
        <el-table-column label="差异" width="120"><template #default="{ row }">{{ signedCents(row.difference_cents) }}</template></el-table-column>
        <el-table-column label="结果" width="100"><template #default="{ row }"><el-tag :type="row.status === 'matched' ? 'success' : 'danger'" effect="plain">{{ row.status === 'matched' ? '一致' : row.status === 'mismatch' ? '金额差异' : '未匹配' }}</el-tag></template></el-table-column>
      </el-table>
      <template #footer><el-button @click="reconciliationDetailDialog = false">关闭</el-button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { localizeDisplayText } from '@/utils/localize'
import { hasPermission } from '@/utils/permissions'

const currentUser = (() => { try { return JSON.parse(localStorage.getItem('user') || '{}') } catch { return {} } })()
const canWrite = hasPermission(currentUser, 'channels.write')

const accounts = ref<any[]>([])
const mappings = ref<any[]>([])
const products = ref<any[]>([])
const loading = ref(false)
const saving = ref(false)
const createDialog = ref(false)
const mappingDialog = ref(false)
const secretDialog = ref(false)
const selectedID = ref(0)
const newSecret = ref('')
const requestsDialog = ref(false)
const requestsLoading = ref(false)
const selectedAccount = ref<any>(null)
const channelRequests = ref<any[]>([])
const requestStatus = ref('')
const requestTotal = ref(0)
const ordersDialog = ref(false)
const ordersLoading = ref(false)
const channelOrders = ref<any[]>([])
const orderSearch = ref('')
const orderStatus = ref('')
const orderPage = ref(1)
const orderTotal = ref(0)
const orderDetailDialog = ref(false)
const orderDetailLoading = ref(false)
const orderDetail = ref<any>(null)
const reconciliationsDialog = ref(false)
const reconciliationsLoading = ref(false)
const reconciliations = ref<any[]>([])
const billImportDialog = ref(false)
const billImporting = ref(false)
const billBatchKey = ref('')
const billText = ref('')
const reconciliationDetailDialog = ref(false)
const reconciliationDetailLoading = ref(false)
const reconciliationDetail = ref<any>(null)
const ctripConfigDialog = ref(false)
const ctripConfigSaving = ref(false)
const ctripConfig = reactive({ account_id: '', sign_key: '', aes_key: '', aes_iv: '' })
const xiaohongshuConfigDialog = ref(false)
const xiaohongshuConfigSaving = ref(false)
const xiaohongshuConfigSaved = ref(false)
const xiaohongshuConfig = reactive({ app_id: '', app_secret: '', message_token: '', encoding_aes_key: '' })
const ctripSandboxConsumeDialog = ref(false)
const ctripSandboxConsuming = ref(false)
const ctripSandboxSupplierOrderID = ref('')
const ctripEndpoint = `${window.location.origin}/api/v1/integrations/ctrip/order`
const form = reactive({ code: '', type: 'core', app_id: '', secret: '', aes_key: '', aes_iv: '', status: 'active', permissions_json: '["products:read","inventory:reserve","orders:create","orders:query","orders:cancel"]', rate_limit_per_min: 600, allowed_ips_json: '' })
const mapping = reactive<{ external_code: string; display_name: string; product_id: number | null; channel_sale_yuan: number; channel_cost_yuan: number }>({ external_code: '', display_name: '', product_id: null, channel_sale_yuan: 0, channel_cost_yuan: 0 })
const ctripSyncTasks = ref<any[]>([])
const syncingMappingID = ref(0)
const xiaohongshuMappingDialog = ref(false)
const xiaohongshuMappingSaving = ref(false)
const xiaohongshuMapping = reactive({ mapping_id: 0, external_code: '', display_name: '', channel_sale_yuan: 0, status: 'active' })
const pricingDialog = ref(false)
const pricingSaving = ref(false)
const pricing = reactive({ mapping_id: 0, channel_sale_yuan: 0, channel_cost_yuan: 0 })
const dateValue = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
const syncEnd = new Date(); syncEnd.setDate(syncEnd.getDate() + 30)
const syncDateRange = ref<[string, string]>([dateValue(new Date()), dateValue(syncEnd)])

const load = async () => { loading.value = true; try { accounts.value = (await request.get('/channel-accounts')).data.data || [] } finally { loading.value = false } }
const handleAdapterTypeChange = (type: string) => { form.status = type === 'ctrip' ? 'sandbox' : 'active'; form.app_id = ''; form.secret = ''; form.aes_key = ''; form.aes_iv = '' }
const create = async () => {
  if (!form.code.trim()) { ElMessage.warning('请填写渠道编码'); return }
  if (form.type === 'ctrip' && (!form.app_id.trim() || !form.secret.trim() || form.aes_key.length !== 16 || form.aes_iv.length !== 16)) { ElMessage.warning('请完整填写携程接口参数，AES 密钥和初始向量必须为 16 位'); return }
  if (form.type === 'xiaohongshu' && (!form.app_id.trim() || !form.secret.trim())) { ElMessage.warning('请完整填写小红书小程序 AppID 和 AppSecret'); return }
  saving.value = true
  try {
    await request.post('/channel-accounts', { code: form.code, type: form.type, app_id: form.app_id, secret: form.secret, aes_key: form.aes_key, aes_iv: form.aes_iv, status: form.status, permissions_json: form.permissions_json, rate_limit_per_min: form.rate_limit_per_min, allowed_ips_json: form.allowed_ips_json })
    createDialog.value = false
    ElMessage.success('渠道已创建')
    Object.assign(form, { code: '', type: 'core', app_id: '', secret: '', aes_key: '', aes_iv: '', status: 'active' })
    await load()
  } finally { saving.value = false }
}
const openCtripConfig = (row: any) => { selectedAccount.value = row; Object.assign(ctripConfig, { account_id: row.app_id || '', sign_key: '', aes_key: '', aes_iv: '' }); ctripConfigDialog.value = true }
const saveCtripConfig = async () => {
  if (!selectedAccount.value || !ctripConfig.account_id.trim() || !ctripConfig.sign_key.trim() || ctripConfig.aes_key.length !== 16 || ctripConfig.aes_iv.length !== 16) { ElMessage.warning('请完整填写参数，AES 密钥和初始向量必须为 16 位'); return }
  ctripConfigSaving.value = true
  try { await request.put(`/channel-accounts/${selectedAccount.value.id}/ctrip-config`, { ...ctripConfig }); ctripConfigDialog.value = false; ElMessage.success('携程接口参数已保存'); await load() }
  finally { ctripConfigSaving.value = false }
}
const openXiaohongshuConfig = (row: any) => {
  selectedAccount.value = row
  Object.assign(xiaohongshuConfig, { app_id: row.app_id || '', app_secret: '', message_token: '', encoding_aes_key: '' })
  xiaohongshuConfigSaved.value = false
  xiaohongshuConfigDialog.value = true
}
const randomAlphanumeric = (length: number) => {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  const values = new Uint8Array(length)
  window.crypto.getRandomValues(values)
  return Array.from(values, value => alphabet[value % alphabet.length]).join('')
}
const generateXiaohongshuMessageKeys = () => {
  xiaohongshuConfig.message_token = randomAlphanumeric(24)
  xiaohongshuConfig.encoding_aes_key = randomAlphanumeric(43)
  xiaohongshuConfigSaved.value = false
}
const xiaohongshuEndpoint = () => `${window.location.origin}/api/v1/integrations/xiaohongshu/events/${xiaohongshuConfig.app_id.trim()}`
const copyText = async (value: string, label: string) => {
  if (!value) { ElMessage.warning(`请先填写或生成${label}`); return }
  await navigator.clipboard.writeText(value)
  ElMessage.success(`${label}已复制`)
}
const saveXiaohongshuConfig = async () => {
  if (!selectedAccount.value || !xiaohongshuConfig.app_id.trim() || !xiaohongshuConfig.app_secret.trim() || !/^[A-Za-z0-9]{3,32}$/.test(xiaohongshuConfig.message_token) || !/^[A-Za-z0-9]{43}$/.test(xiaohongshuConfig.encoding_aes_key)) { ElMessage.warning('请完整填写 AppID、AppSecret，并生成符合要求的 Token 和 EncodingAESKey'); return }
  xiaohongshuConfigSaving.value = true
  try {
    await request.put(`/channel-accounts/${selectedAccount.value.id}/xiaohongshu-config`, { ...xiaohongshuConfig })
    xiaohongshuConfig.app_secret = ''
    xiaohongshuConfigSaved.value = true
    ElMessage.success('参数已保存，请继续配置小红书后台')
    await load()
  } finally { xiaohongshuConfigSaving.value = false }
}
const copyCtripEndpoint = async () => { await navigator.clipboard.writeText(ctripEndpoint); ElMessage.success('接口地址已复制') }
const openCtripSandboxConsume = (row: any) => { selectedAccount.value = row; ctripSandboxSupplierOrderID.value = ''; ctripSandboxConsumeDialog.value = true }
const simulateCtripSandboxConsumption = async () => {
  if (!selectedAccount.value || !ctripSandboxSupplierOrderID.value.trim()) { ElMessage.warning('请填写供应商订单号'); return }
  await ElMessageBox.confirm('该操作会把指定沙箱订单标记为已核销并向携程发送核销通知，确认继续？', '确认沙箱核销', { type: 'warning' })
  ctripSandboxConsuming.value = true
  try {
    await request.post(`/channel-accounts/${selectedAccount.value.id}/ctrip-sandbox-consume`, { supplier_order_id: ctripSandboxSupplierOrderID.value.trim() })
    ctripSandboxConsumeDialog.value = false
    ElMessage.success('核销通知已进入可靠发送队列')
  } finally { ctripSandboxConsuming.value = false }
}
const toggleStatus = async (row: any) => { const status = row.status === 'disabled' ? (row.environment === 'sandbox' ? 'sandbox' : 'active') : 'disabled'; await request.patch(`/channel-accounts/${row.id}/status`, { status }); row.status = status; ElMessage.success('状态已更新') }
const rotate = async (row: any) => { await ElMessageBox.confirm('轮换后旧密钥立即失效，确认继续？', '确认轮换', { type: 'warning' }); const response = await request.post(`/channel-accounts/${row.id}/rotate-secret`); newSecret.value = response.data.secret; secretDialog.value = true }
const productName = (id: number) => products.value.find((product: any) => Number(product.id) === Number(id))?.name || '已下架或不可见产品'
const openMapping = async (row: any) => {
  selectedAccount.value = row; selectedID.value = row.id
  Object.assign(mapping, { external_code: '', display_name: '', product_id: null, channel_sale_yuan: 0, channel_cost_yuan: 0 })
  const [mappingResponse, productResponse] = await Promise.all([
    request.get('/channel-accounts/mappings', { params: { channel_account_id: row.id } }),
    request.get('/products', { params: { page: 1, page_size: 100 } }),
  ])
  mappings.value = mappingResponse.data.data || []
  products.value = productResponse.data.data || []
  ctripSyncTasks.value = []
  if (row.type === 'ctrip') await loadCtripSyncTasks()
  mappingDialog.value = true
}
const addMapping = async () => {
  if (!mapping.external_code.trim() || !mapping.product_id) { ElMessage.warning('请选择产品并填写外部编码'); return }
  if (selectedAccount.value?.type === 'ctrip' && (mapping.channel_sale_yuan <= 0 || mapping.channel_cost_yuan < 0 || mapping.channel_cost_yuan > mapping.channel_sale_yuan)) { ElMessage.warning('携程销售价必须大于 0，结算价不能高于销售价'); return }
  if (selectedAccount.value?.type === 'xiaohongshu' && mapping.channel_sale_yuan <= 0) { ElMessage.warning('小红书售价必须大于 0'); return }
  const response = await request.post('/channel-accounts/mappings', { channel_account_id: selectedID.value, external_code: mapping.external_code.trim(), display_name: mapping.display_name.trim(), product_id: mapping.product_id, channel_sale_cents: Math.round(mapping.channel_sale_yuan * 100), channel_cost_cents: Math.round(mapping.channel_cost_yuan * 100) })
  mappings.value.unshift(response.data)
  Object.assign(mapping, { external_code: '', display_name: '', product_id: null, channel_sale_yuan: 0, channel_cost_yuan: 0 })
}
const openXiaohongshuMappingEdit = (row: any) => {
  Object.assign(xiaohongshuMapping, { mapping_id: row.id, external_code: row.external_code || '', display_name: row.display_name || '', channel_sale_yuan: Number(row.channel_sale_cents || 0) / 100, status: row.status || 'active' })
  xiaohongshuMappingDialog.value = true
}
const saveXiaohongshuMapping = async () => {
  if (!selectedAccount.value || !xiaohongshuMapping.external_code.trim() || xiaohongshuMapping.channel_sale_yuan <= 0) { ElMessage.warning('请填写外部商品编码和有效售价'); return }
  xiaohongshuMappingSaving.value = true
  try {
    await request.patch(`/channel-accounts/${selectedAccount.value.id}/mappings/${xiaohongshuMapping.mapping_id}`, { external_code: xiaohongshuMapping.external_code.trim(), display_name: xiaohongshuMapping.display_name.trim(), channel_sale_cents: Math.round(xiaohongshuMapping.channel_sale_yuan * 100), channel_cost_cents: 0, status: xiaohongshuMapping.status })
    const row = mappings.value.find((item: any) => item.id === xiaohongshuMapping.mapping_id)
    if (row) Object.assign(row, { external_code: xiaohongshuMapping.external_code.trim(), display_name: xiaohongshuMapping.display_name.trim(), channel_sale_cents: Math.round(xiaohongshuMapping.channel_sale_yuan * 100), channel_cost_cents: 0, status: xiaohongshuMapping.status })
    xiaohongshuMappingDialog.value = false
    ElMessage.success('小红书商品映射已更新')
  } finally { xiaohongshuMappingSaving.value = false }
}
const loadCtripSyncTasks = async () => {
  if (!selectedAccount.value || selectedAccount.value.type !== 'ctrip') return
  ctripSyncTasks.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/ctrip-sync-tasks`, { params: { limit: 100 } })).data.data || []
}
const syncMapping = async (row: any) => {
  if (!selectedAccount.value || syncDateRange.value.length !== 2) return
  syncingMappingID.value = row.id
  try {
    await request.post(`/channel-accounts/${selectedAccount.value.id}/mappings/${row.id}/ctrip-sync`, { start_date: syncDateRange.value[0], end_date: syncDateRange.value[1] })
    ElMessage.success('价格和库存已进入同步队列')
    await loadCtripSyncTasks()
  } finally { syncingMappingID.value = 0 }
}
const openPricing = (row: any) => { Object.assign(pricing, { mapping_id: row.id, channel_sale_yuan: Number(row.channel_sale_cents || 0) / 100, channel_cost_yuan: Number(row.channel_cost_cents || 0) / 100 }); pricingDialog.value = true }
const savePricing = async () => {
  if (!selectedAccount.value || pricing.channel_sale_yuan <= 0 || pricing.channel_cost_yuan < 0 || pricing.channel_cost_yuan > pricing.channel_sale_yuan) { ElMessage.warning('携程销售价必须大于 0，结算价不能高于销售价'); return }
  pricingSaving.value = true
  try {
    await request.patch(`/channel-accounts/${selectedAccount.value.id}/mappings/${pricing.mapping_id}/ctrip-pricing`, { channel_sale_cents: Math.round(pricing.channel_sale_yuan * 100), channel_cost_cents: Math.round(pricing.channel_cost_yuan * 100) })
    const row = mappings.value.find((item: any) => item.id === pricing.mapping_id)
    if (row) { row.channel_sale_cents = Math.round(pricing.channel_sale_yuan * 100); row.channel_cost_cents = Math.round(pricing.channel_cost_yuan * 100) }
    pricingDialog.value = false
    ElMessage.success('携程渠道价格已更新，请按需要重新同步')
  } finally { pricingSaving.value = false }
}
const dateTime = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
const cents = (value: number) => (Number(value || 0) / 100).toFixed(2)
const signedCents = (value: number) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const accountStatusText = (status: string) => ({ active: '正式启用', sandbox: '测试中', disabled: '已停用' } as Record<string, string>)[status] || '未知状态'
const adapterTypeText = (type: string) => ({ core: '通用渠道', ctrip: '携程', xiaohongshu: '小红书', meituan: '美团', zyb: '智游宝上游' } as Record<string, string>)[type] || '自定义渠道'
const mappingStatusText = (status: string) => ({ active: '已启用', disabled: '已停用' } as Record<string, string>)[status] || '未知状态'
const ctripTaskKindText = (kind: string) => ({ price: '价格', inventory: '库存', consumed: '核销通知' } as Record<string, string>)[kind] || '其他'
const syncStatusText = (status: string) => ({ pending: '等待同步', processing: '同步中', succeeded: '已成功', failed: '同步失败' } as Record<string, string>)[status] || '未知状态'
const syncStatusType = (status: string) => status === 'succeeded' ? 'success' : status === 'failed' ? 'danger' : status === 'processing' ? 'primary' : 'warning'
const requestStatusText = (status: string) => ({ processing: '处理中', completed: '已完成', failed: '失败待处理', retryable: '已授权重试' } as Record<string, string>)[status] || '未知状态'
const requestStatusType = (status: string) => status === 'completed' ? 'success' : status === 'failed' ? 'danger' : status === 'retryable' ? 'warning' : 'primary'
const orderStatusText = (status: string) => ({ unpaid: '待支付', paid: '已支付', completed: '已完成', partial_refunded: '部分退款', refunded: '已退款', cancelled: '已取消' } as Record<string, string>)[status] || '未知状态'
const ticketStatusText = (status: string) => ({ unused: '未使用', active: '可使用', issued: '已出票', used: '已核销', refunded: '已退款', expired: '已过期', void: '已作废' } as Record<string, string>)[status] || '未知状态'
const paymentMethodText = (method: string) => ({ cash: '现金', wechat: '微信支付', alipay: '支付宝', touch: '碰一碰支付', balance: '账户余额', credit: '授信挂账', team_account: '团队预付款/授信' } as Record<string, string>)[method] || '其他方式'
const paymentStatusText = (status: string) => ({ pending: '等待支付', processing: '支付处理中', paid: '支付成功', succeeded: '支付成功', failed: '支付失败', cancelled: '已取消', refunded: '已退款', partial_refunded: '部分退款' } as Record<string, string>)[status] || '未知状态'
const refundStatusText = (status: string) => ({ pending: '等待退款', processing: '退款处理中', succeeded: '退款成功', completed: '退款成功', failed: '退款失败', manual_review: '人工复核' } as Record<string, string>)[status] || '未知状态'
const checkInResultText = (result: string) => ({ success: '核销成功', failed: '核销失败', rejected: '已拒绝' } as Record<string, string>)[result] || '未知结果'
const afterSaleTypeText = (type: string) => ({ refund: '退票', reschedule: '改期', exchange: '换票', void: '作废', reissue: '补打' } as Record<string, string>)[type] || '其他售后'
const afterSaleStatusText = (status: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '处理失败' } as Record<string, string>)[status] || '未知状态'
const operationText = (operation: string) => ({ sale: '销售', payment: '收款', cancel: '取消', refund: '退款' } as Record<string, string>)[operation] || '其他'
const orderTickets = (order: any) => (order?.items || []).flatMap((item: any) => (item.tickets || []).map((ticket: any) => ({ ...ticket, product_name: item.product_name })))
const loadOrders = async (page = 1) => {
  if (!selectedAccount.value) return
  orderPage.value = page
  ordersLoading.value = true
  try {
    const response = await request.get(`/channel-accounts/${selectedAccount.value.id}/orders`, { params: { search: orderSearch.value.trim(), status: orderStatus.value, page, page_size: 20 } })
    channelOrders.value = response.data.data || []
    orderTotal.value = Number(response.data.total || 0)
  } finally { ordersLoading.value = false }
}
const openOrders = async (row: any) => {
  selectedAccount.value = row
  orderSearch.value = ''
  orderStatus.value = ''
  ordersDialog.value = true
  await loadOrders(1)
}
const openOrderDetail = async (row: any) => {
  if (!selectedAccount.value) return
  orderDetail.value = null
  orderDetailDialog.value = true
  orderDetailLoading.value = true
  try { orderDetail.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/orders/${encodeURIComponent(row.order_no)}`)).data }
  finally { orderDetailLoading.value = false }
}
const loadRequests = async () => {
  if (!selectedAccount.value) return
  requestsLoading.value = true
  try {
    const response = await request.get(`/channel-accounts/${selectedAccount.value.id}/requests`, { params: { status: requestStatus.value, page: 1, page_size: 100 } })
    channelRequests.value = response.data.data || []
    requestTotal.value = Number(response.data.total || 0)
  } finally { requestsLoading.value = false }
}
const openRequests = async (row: any) => { selectedAccount.value = row; requestStatus.value = ''; requestsDialog.value = true; await loadRequests() }
const authorizeRetry = async (row: any) => {
  if (!selectedAccount.value) return
  try {
    const result = await ElMessageBox.prompt('确认故障原因已排除，并填写授权重试原因。渠道方仍需使用相同请求编号和相同正文重新发送。', '授权渠道重试', { inputType: 'textarea', inputValidator: value => value.trim() ? true : '授权原因必填' })
    await request.post(`/channel-accounts/${selectedAccount.value.id}/requests/${row.id}/authorize-retry`, { reason: result.value.trim() })
    ElMessage.success('已授权，等待渠道方重发原请求')
    await loadRequests()
  } catch (action: any) {
    if (action !== 'cancel' && action !== 'close') ElMessage.error(action.response?.data?.error || '授权重试失败')
  }
}
const loadReconciliations = async () => {
  if (!selectedAccount.value) return
  reconciliationsLoading.value = true
  try { reconciliations.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/reconciliations`, { params: { page: 1, page_size: 100 } })).data.data || [] }
  finally { reconciliationsLoading.value = false }
}
const openReconciliations = async (row: any) => { selectedAccount.value = row; reconciliationsDialog.value = true; await loadReconciliations() }
const parseBill = () => billText.value.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map((line, index) => {
  const cells = line.split(/[\t,，]/).map(value => value.trim())
  if (index === 0 && /外部单号|external/i.test(cells[0] || '')) return null
  const operation = ({ 销售: 'sale', 收款: 'payment', 取消: 'cancel', 退款: 'refund' } as Record<string, string>)[cells[1]] || cells[1]
  const amount = Number(cells[2])
  if (!cells[0] || !['sale', 'payment', 'cancel', 'refund'].includes(operation) || !Number.isFinite(amount) || amount < 0) throw new Error(`第 ${index + 1} 行格式不正确`)
  const occurred = cells[3] ? new Date(cells[3].replace(' ', 'T')) : null
  if (occurred && Number.isNaN(occurred.getTime())) throw new Error(`第 ${index + 1} 行时间格式不正确`)
  return { external_no: cells[0], operation, amount_cents: Math.round(amount * 100), currency: 'CNY', external_occurred_at: occurred?.toISOString() }
}).filter(Boolean)
const importBill = async () => {
  if (!selectedAccount.value || !billBatchKey.value.trim() || !billText.value.trim()) { ElMessage.warning('批次号和账单内容必填'); return }
  billImporting.value = true
  try {
    const records = parseBill()
    await request.post(`/channel-accounts/${selectedAccount.value.id}/bills/import`, { idempotency_key: billBatchKey.value.trim(), records })
    billImportDialog.value = false
    billBatchKey.value = ''
    billText.value = ''
    ElMessage.success('账单已导入并完成核对')
    await loadReconciliations()
  } catch (e: any) { ElMessage.error(e.response?.data?.error || e.message || '账单导入失败') }
  finally { billImporting.value = false }
}
const openReconciliationDetail = async (row: any) => {
  if (!selectedAccount.value) return
  reconciliationDetailDialog.value = true
  reconciliationDetailLoading.value = true
  try { reconciliationDetail.value = (await request.get(`/channel-accounts/${selectedAccount.value.id}/reconciliations/${row.id}`)).data }
  finally { reconciliationDetailLoading.value = false }
}
onMounted(load)
</script>
