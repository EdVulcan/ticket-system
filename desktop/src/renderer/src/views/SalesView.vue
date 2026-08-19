<template>
  <div class="pos-shell">
    <header class="topbar">
      <div class="brand-block">
        <div class="brand-mark"><el-icon :size="22"><Tickets /></el-icon></div>
        <div>
          <div class="brand-title">窗口售票</div>
          <div class="brand-subtitle">{{ getPageTitle }}</div>
        </div>
      </div>

      <nav class="workspace-tabs" aria-label="窗口工作区">
        <button v-if="canSell" class="workspace-tab" :class="{ active: currentView === 'pos' }" @click="currentView = 'pos'">
          <el-icon><Monitor /></el-icon><span>售票</span>
        </button>
        <button v-if="canSell" class="workspace-tab" :class="{ active: currentView === 'orders' }" @click="currentView = 'orders'">
          <el-icon><List /></el-icon><span>订单</span>
        </button>
        <button v-if="canVerify" class="workspace-tab" :class="{ active: currentView === 'verify' }" @click="currentView = 'verify'">
          <el-icon><Checked /></el-icon><span>核销</span>
        </button>
        <button class="workspace-tab" :class="{ active: currentView === 'settings' }" @click="currentView = 'settings'">
          <el-icon><Setting /></el-icon><span>终端</span>
        </button>
      </nav>

      <div class="operator-block">
        <div class="operator-meta">
          <span class="clock">{{ currentTime }}</span>
          <span>{{ currentStaff.name }} · {{ currentStaff.job_number }}</span>
        </div>
        <button v-if="canSell" class="shift-chip" :class="{ open: shiftState.isOpen }" @click="handleShiftAction">
          <span class="status-dot"></span>{{ shiftState.isOpen ? '当班中' : '未开班' }}
        </button>
        <el-tooltip content="退出登录" placement="bottom">
          <button class="icon-button danger" aria-label="退出登录" @click="handleLogout"><el-icon><SwitchButton /></el-icon></button>
        </el-tooltip>
      </div>
    </header>

    <main class="workspace">
      <section v-if="currentView === 'pos'" class="sales-workspace">
        <div class="catalog-pane">
          <div v-if="!shiftState.isOpen || !posDeviceId" class="readiness-banner">
            <el-icon><Warning /></el-icon>
            <span v-if="!posDeviceId">尚未配置售票终端，请先进入终端设置。</span>
            <span v-else>当前未开班，开班后才能创建窗口订单。</span>
            <button @click="!posDeviceId ? (currentView = 'settings') : handleShiftAction()">立即处理</button>
          </div>

          <div class="catalog-toolbar">
            <div class="toolbar-label"><span>可售票种</span><strong>{{ products.length }}</strong></div>
            <el-input ref="searchInput" v-model="searchQuery" size="large" clearable placeholder="按票名搜索" aria-label="票名搜索" :prefix-icon="Search" />
            <el-input-number v-model="priceSearch" aria-label="票价搜索" class="price-filter" size="large" :min="0" :precision="2" :controls="false" placeholder="输入票价" />
            <div class="catalog-count">{{ filteredProducts.length }} / {{ products.length }} 种</div>
            <el-tooltip content="刷新商品与库存" placement="bottom">
              <el-button :icon="Refresh" circle aria-label="刷新商品与库存" @click="fetchProducts" />
            </el-tooltip>
          </div>

          <div v-if="productCategories.length" class="category-strip custom-scrollbar" aria-label="票种分类">
            <button :class="{ active: categoryFilter === '' }" @click="categoryFilter = ''">全部</button>
            <button v-for="category in productCategories" :key="category" :class="{ active: categoryFilter === category }" @click="categoryFilter = category">{{ category }}</button>
          </div>

          <div class="product-grid custom-scrollbar">
            <button v-for="p in filteredProducts" :key="p.catalogKey" class="product-tile" @click="addToCart(p)">
              <div class="product-main">
                <div class="product-name">{{ p.name }}</div>
                <div class="product-tags">
                  <span v-for="tag in p.parsedTags?.slice(0, 2)" :key="tag">{{ tag }}</span>
                  <span class="stock-tag">库存 {{ p.stock_type === 'unlimited' ? '充足' : p.daily_stock }}</span>
                </div>
              </div>
              <div class="product-action">
                <strong>¥{{ Number(p.price).toFixed(2) }}</strong>
                <span class="add-icon"><el-icon><Plus /></el-icon></span>
              </div>
            </button>
            <div v-if="filteredProducts.length === 0" class="empty-state">
              <el-icon :size="36"><Search /></el-icon>
              <strong>{{ products.length === 0 ? '尚未配置窗口票种' : '没有匹配的票种' }}</strong>
              <span>{{ products.length === 0 ? '请在管理后台将可售票种设置为窗口售票' : '调整票名、分类或价格后再试' }}</span>
              <el-button v-if="products.length > 0" text type="primary" @click="clearProductFilters">清除筛选</el-button>
            </div>
          </div>

          <div class="quick-tools">
            <button @click="showPolicy = true"><el-icon><Reading /></el-icon><span>政策</span></button>
            <button @click="showCalc = true"><el-icon><Grid /></el-icon><span>计算器</span></button>
            <button @click="openHolds"><el-icon><Notebook /></el-icon><span>挂单列表</span></button>
            <button @click="openPrintTaskCenter"><el-icon><Printer /></el-icon><span>打印任务</span></button>
            <button @click="showNote = true"><el-icon><EditPen /></el-icon><span>交班便签</span></button>
          </div>
        </div>

        <aside class="cart-pane">
          <div class="cart-heading">
            <div>
              <span class="eyebrow">本次交易</span>
              <h2>购票清单 <em>{{ cartItemCount }}</em></h2>
            </div>
            <el-button text type="danger" :disabled="cart.length === 0" @click="clearCart">清空</el-button>
          </div>

          <div class="cart-list custom-scrollbar">
            <div v-if="cart.length === 0" class="empty-cart">
              <div class="empty-cart-icon"><el-icon :size="32"><ShoppingCart /></el-icon></div>
              <strong>还没有选择票种</strong>
              <span>点击左侧票种即可加入</span>
            </div>
            <div v-for="(item, idx) in cart" :key="item.catalogKey" class="cart-item">
              <div class="cart-item-top">
                <div class="cart-item-name">{{ item.name }}</div>
                <strong>¥{{ (item.price * item.quantity).toFixed(2) }}</strong>
              </div>
              <div class="cart-item-bottom">
                <span>¥{{ Number(item.price).toFixed(2) }} / 张</span>
                <div class="quantity-stepper">
                  <button aria-label="减少数量" @click="updateQty(idx, -1)"><el-icon><Minus /></el-icon></button>
                  <span>{{ item.quantity }}</span>
                  <button aria-label="增加数量" @click="updateQty(idx, 1)"><el-icon><Plus /></el-icon></button>
                </div>
              </div>
            </div>
          </div>

          <div class="checkout-panel">
            <div v-if="paymentComplete || pendingPrintOrderNo" class="sale-lock-banner">
              <div>
                <strong>已收款订单待处理</strong>
                <span>{{ currentOrder?.order_no || pendingPrintOrderNo }} · 请先完成打印任务，避免重复收款</span>
              </div>
              <div class="sale-lock-actions">
                <el-button size="small" @click="openOrderDetail(currentOrder?.order_no || pendingPrintOrderNo)">查看订单</el-button>
                <el-button size="small" type="primary" @click="openPrintTaskCenter">处理打印</el-button>
              </div>
            </div>
            <div class="total-line"><span>应收 · 共 {{ cartItemCount }} 张</span><strong>¥{{ totalAmount.toFixed(2) }}</strong></div>
            <div class="checkout-actions">
              <el-button size="large" :disabled="cart.length === 0" @click="handleHold"><el-icon><Notebook /></el-icon>挂单</el-button>
              <el-button type="success" size="large" :disabled="cart.length === 0 || !shiftState.isOpen || !posDeviceId || paymentComplete || !!pendingPrintOrderNo" @click="handleCheckout">
                <el-icon><Wallet /></el-icon>收款
              </el-button>
            </div>
          </div>
        </aside>
      </section>

      <section v-if="currentView === 'orders'" class="page-workspace">
        <div class="page-heading"><div><h1>窗口订单</h1><p>查询售票记录并处理后续操作</p></div></div>
        <div class="filter-bar">
          <el-input v-model="orderSearchQuery" placeholder="订单号或联系人" clearable :prefix-icon="Search" @keyup.enter="searchOrders" />
          <el-date-picker v-model="orderDateRange" type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" value-format="YYYY-MM-DD" @change="searchOrders" />
          <el-select v-model="orderStatus" aria-label="订单状态" placeholder="全部状态" clearable @change="searchOrders">
            <el-option label="待支付" value="unpaid" />
            <el-option label="已支付" value="paid" />
            <el-option label="已完成" value="completed" />
            <el-option label="部分退款" value="partial_refunded" />
            <el-option label="已退款" value="refunded" />
            <el-option label="已取消" value="cancelled" />
          </el-select>
          <el-button type="primary" :icon="Search" @click="searchOrders">查询</el-button>
        </div>
        <div class="data-panel">
          <el-table v-loading="ordersLoading" :data="orders" height="100%" stripe>
            <el-table-column prop="order_no" label="订单号" min-width="205" />
            <el-table-column prop="contact_name" label="联系人" width="120" />
            <el-table-column label="商品" min-width="240">
              <template #default="{ row }"><span v-for="item in row.items" :key="item.id" class="order-item-text"><b v-if="item.bundle_name">{{ item.bundle_name }} · </b>{{ item.product_name }} × {{ item.quantity }}</span></template>
            </el-table-column>
            <el-table-column label="金额" width="110"><template #default="{ row }"><strong class="money">¥{{ row.total_amount }}</strong></template></el-table-column>
            <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="orderStatusTag(row.status)">{{ orderStatusLabel(row.status) }}</el-tag></template></el-table-column>
            <el-table-column label="下单时间" width="180"><template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template></el-table-column>
            <el-table-column label="操作" width="100" fixed="right"><template #default="{ row }"><el-button link type="primary" @click="openOrderDetail(row.order_no)">详情</el-button></template></el-table-column>
          </el-table>
        </div>
        <el-pagination
          v-model:current-page="orderPage"
          v-model:page-size="orderPageSize"
          class="order-pagination"
          :page-sizes="[10, 20, 40]"
          :total="orderTotal"
          layout="total, sizes, prev, pager, next"
          @current-change="fetchOrders"
          @size-change="searchOrders"
        />
      </section>

      <section v-if="currentView === 'verify'" class="verify-workspace">
        <div class="verify-main">
          <div class="verify-heading"><div class="verify-icon"><el-icon><FullScreen /></el-icon></div><h1>票券核销</h1><p>扫描二维码，或输入完整票码</p></div>
          <div class="verify-entry">
            <el-input ref="verifyInputRef" v-model="verifyInput" size="large" clearable placeholder="等待扫码或输入票码" @keyup.enter="handleVerify" />
            <el-button type="success" size="large" :disabled="!verifyInput.trim() || !currentCheckPointId || !posDeviceId" @click="handleVerify">确认核销</el-button>
          </div>
          <div class="verify-context">
            <span><el-icon><Place /></el-icon>{{ currentCheckpointName }}</span>
            <span><el-icon><Monitor /></el-icon>设备 {{ posDeviceId || '未配置' }}</span>
          </div>
        </div>
        <aside class="history-pane">
          <div class="history-heading"><h2>最近核销</h2><span>{{ verifyHistory.length }} 条</span></div>
          <div class="history-list custom-scrollbar">
            <div v-if="verifyHistory.length === 0" class="history-empty">暂无核销记录</div>
            <div v-for="entry in verifyHistory" :key="`${entry.code}-${entry.time}`" class="history-item" :class="entry.status">
              <el-icon><CircleCheck v-if="entry.status === 'success'" /><CircleClose v-else /></el-icon>
              <div><strong>{{ entry.msg }}</strong><span>{{ entry.code }}</span><small>{{ entry.time }}</small></div>
            </div>
          </div>
        </aside>
      </section>

      <section v-if="currentView === 'settings'" class="page-workspace">
        <div class="page-heading"><div><h1>终端与班次</h1><p>配置当前窗口的设备归属和交接班状态</p></div></div>
        <div class="settings-grid">
          <section class="settings-section">
            <div class="section-heading"><el-icon><Place /></el-icon><div><h2>窗口归属</h2><p>核销与售票操作将记录到所选设备</p></div></div>
            <el-form label-position="top">
              <el-form-item label="当前检票点"><el-select v-model="currentCheckPointId" placeholder="请选择检票点" class="w-full" @change="saveSettings"><el-option v-for="cp in checkpoints" :key="cp.id" :label="cp.name" :value="cp.id" /></el-select></el-form-item>
              <el-form-item label="当前售票终端">
                <el-select v-model="posDeviceId" placeholder="请选择已授权终端" class="w-full" :disabled="shiftState.isOpen" @change="selectPOSTerminal">
                  <el-option v-for="terminal in posTerminals" :key="terminal.id" :label="terminal.name" :value="terminal.id" />
                </el-select>
                <div v-if="posTerminals.length === 0" class="terminal-empty">当前工号尚未分配售票终端，请让管理员在员工管理中分配终端。</div>
              </el-form-item>
            </el-form>
          </section>
          <section class="settings-section">
            <div class="section-heading"><el-icon><Printer /></el-icon><div><h2>本机硬件</h2><p>硬件适配器未配置时不会伪报成功</p></div></div>
            <div class="hardware-row"><span>小票打印机</span><el-tag type="warning">待配置</el-tag></div>
            <div class="hardware-row"><span>证件阅读器</span><el-tag type="info">待配置</el-tag></div>
            <div class="hardware-row"><span>窗口端版本</span><div class="version-action"><code>{{ desktopVersion || '检测中' }}</code><el-button size="small" :loading="updateChecking" @click="offerDesktopUpdate(true)">检查更新</el-button></div></div>
          </section>
          <section v-if="canSell" class="settings-section">
            <div class="section-heading"><el-icon><Notebook /></el-icon><div><h2>当前班次</h2><p>{{ shiftState.isOpen ? `开始于 ${new Date(shiftState.startTime!).toLocaleString()}` : '开班后才能进行窗口收款' }}</p></div></div>
            <div class="shift-summary"><span>状态</span><el-tag :type="shiftState.isOpen ? 'success' : 'info'">{{ shiftState.isOpen ? '当班中' : '未开班' }}</el-tag></div>
            <div v-if="shiftState.isOpen" class="shift-summary"><span>开班备用金</span><strong>¥{{ cents(shiftState.openingCents) }}</strong></div>
            <el-button :type="shiftState.isOpen ? 'danger' : 'success'" size="large" class="w-full" @click="handleShiftAction">{{ shiftState.isOpen ? '结束当班并交班' : '开始当班' }}</el-button>
          </section>
        </div>
      </section>

      <el-dialog v-model="showCalc" title="计算器" width="320px" :modal="false" draggable align-center><Calculator :active="showCalc" /></el-dialog>
      <el-dialog v-model="showPayment" title="收款" width="520px" align-center :close-on-click-modal="false" :close-on-press-escape="!paymentLocked" :show-close="!paymentLocked">
        <PaymentModal v-if="showPayment" :amount="currentOrder?.total_amount || 0" :order-no="currentOrder?.order_no || ''" :shift-id="shiftState.shiftId || 0" :device-id="posDeviceId || 0" @success="handlePaymentSuccess" @cancelled="handlePaymentCancelled" @lock-change="paymentLocked = $event" />
      </el-dialog>
      <el-dialog v-model="showOpenShift" title="开始当班" width="420px" align-center :close-on-click-modal="false">
        <div class="shift-dialog-intro">请清点钱箱内用于找零的备用金。该金额会计入本班应交现金。</div>
        <el-form label-position="top">
          <el-form-item label="开班备用金（元）">
            <el-input-number v-model="openingAmount" :min="0" :precision="2" :step="10" :controls="false" class="money-input" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="showOpenShift = false">取消</el-button>
          <el-button type="success" :loading="shiftSubmitting" @click="submitOpenShift">确认开班</el-button>
        </template>
      </el-dialog>
      <el-dialog v-model="showCloseShift" title="交班清点" width="720px" align-center :close-on-click-modal="false">
        <div v-loading="shiftSummaryLoading">
          <div class="shift-summary-grid">
            <div v-for="method in shiftMethods" :key="method.key" class="shift-method-panel">
              <div class="shift-method-title"><span>{{ method.label }}</span><strong>净收 ¥{{ cents(methodSummary(method.key).net_cents) }}</strong></div>
              <div><span>实收</span><b>¥{{ cents(methodSummary(method.key).gross_cents) }}</b></div>
              <div><span>退款</span><b>¥{{ cents(methodSummary(method.key).refund_cents) }}</b></div>
            </div>
          </div>
          <div class="cash-count-panel">
            <div class="cash-fact"><span>开班备用金</span><strong>¥{{ cents(closeSummary?.shift?.opening_cents) }}</strong></div>
            <div class="cash-fact"><span>应交现金</span><strong>¥{{ cents(closeSummary?.cash_expected_cents) }}</strong></div>
            <div class="cash-count-input">
              <label>钱箱实盘（元）</label>
              <el-input-number v-model="closingAmount" :min="0" :precision="2" :step="10" :controls="false" />
            </div>
            <div class="cash-difference" :class="{ balanced: closeDifferenceCents === 0 }"><span>差异</span><strong>{{ signedCents(closeDifferenceCents) }}</strong></div>
          </div>
          <el-form label-position="top" class="mt-4"><el-form-item label="交班说明"><el-input v-model="closingNotes" type="textarea" :rows="3" maxlength="255" show-word-limit placeholder="有差异或需交接的事项请在此说明" /></el-form-item></el-form>
        </div>
        <template #footer>
          <el-button @click="showCloseShift = false">取消</el-button>
          <el-button type="danger" :loading="shiftSubmitting" :disabled="shiftSummaryLoading" @click="submitCloseShift">确认关班</el-button>
        </template>
      </el-dialog>
      <el-dialog v-model="showHolds" title="挂单列表" width="760px" align-center>
        <div class="flex justify-between items-center mb-3">
          <span class="text-sm text-gray-400">挂单只保存商品选择，恢复时会重新校验价格、上下架和库存。</span>
          <el-button :icon="Refresh" circle title="刷新挂单" @click="loadHolds" />
        </div>
        <el-table :data="holds" stripe max-height="360" v-loading="holdsLoading">
          <el-table-column prop="hold_no" label="挂单号" width="220" />
          <el-table-column label="商品" min-width="220">
            <template #default="{ row }">{{ formatHoldItems(row) }}</template>
          </el-table-column>
          <el-table-column label="金额" width="110">
            <template #default="{ row }">¥{{ (row.total_cents / 100).toFixed(2) }}</template>
          </el-table-column>
          <el-table-column prop="expires_at" label="有效期" width="180" />
          <el-table-column label="操作" width="160" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="resumeHold(row)">恢复</el-button>
              <el-button link type="danger" @click="cancelHold(row)">取消</el-button>
            </template>
          </el-table-column>
        </el-table>
        <template #footer><el-button @click="showHolds = false">关闭</el-button></template>
      </el-dialog>

      <el-dialog v-model="showPrintTasks" title="打印任务中心" width="900px" align-center @open="loadPrintJobs">
        <el-alert
          v-if="printJobs.some(job => job.status === 'printing')"
          class="mb-3"
          type="warning"
          :closable="false"
          title="存在打印中任务，请先确认设备是否已经出纸；系统不会自动重打，避免重复出票。"
        />
        <div class="print-task-toolbar">
          <span>当前终端的排队、打印中、失败和已完成任务</span>
          <el-button :icon="Refresh" circle aria-label="刷新打印任务" :loading="printJobsLoading" @click="loadPrintJobs" />
        </div>
        <el-table :data="printJobs" stripe max-height="480" v-loading="printJobsLoading" row-key="id">
          <el-table-column prop="id" label="任务" width="80" />
          <el-table-column prop="order_no" label="订单号" min-width="180" />
          <el-table-column prop="ticket_code" label="票码" min-width="180"><template #default="{ row }">{{ row.ticket_code || '整单快照' }}</template></el-table-column>
          <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="printStatusTag(row.status)">{{ printStatusLabel(row.status) }}</el-tag></template></el-table-column>
          <el-table-column label="失败原因" min-width="210" show-overflow-tooltip><template #default="{ row }">{{ row.last_error || (row.status === 'printing' ? '请确认设备输出' : '—') }}</template></el-table-column>
          <el-table-column label="操作" width="130" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.status === 'queued' || row.status === 'failed'" link type="primary" :loading="printJobPrinting === row.id" @click="retryPrintJob(row)">逐项重打</el-button>
              <span v-else-if="row.status === 'printing'" class="muted-action">待人工确认</span>
              <span v-else class="muted-action">—</span>
            </template>
          </el-table-column>
        </el-table>
        <template #footer><el-button @click="showPrintTasks = false">关闭</el-button></template>
      </el-dialog>

      <el-dialog v-model="showOrderDetail" title="窗口订单详情" width="960px" align-center>
        <div v-loading="orderDetailLoading" class="order-detail-dialog">
          <template v-if="selectedOrderDetail?.order">
            <div class="order-detail-summary">
              <div><span class="eyebrow">订单号</span><strong>{{ selectedOrderDetail.order.order_no }}</strong></div>
              <div><span class="eyebrow">状态</span><el-tag :type="orderStatusTag(selectedOrderDetail.order.status)">{{ orderStatusLabel(selectedOrderDetail.order.status) }}</el-tag></div>
              <div><span class="eyebrow">应收金额</span><strong class="money">¥{{ Number(selectedOrderDetail.order.total_amount || 0).toFixed(2) }}</strong></div>
              <div><span class="eyebrow">下单时间</span><span>{{ new Date(selectedOrderDetail.order.created_at).toLocaleString() }}</span></div>
            </div>
            <el-divider content-position="left">票券</el-divider>
            <el-table :data="selectedOrderDetail.order.items || []" stripe size="small">
              <el-table-column prop="product_name" label="票种" min-width="180" />
              <el-table-column prop="quantity" label="数量" width="80" />
              <el-table-column label="票码与状态" min-width="360">
                <template #default="{ row }">
                  <div v-if="row.tickets?.length" class="ticket-detail-list"><span v-for="ticket in row.tickets" :key="ticket.id"><code>{{ ticket.ticket_code }}</code><el-tag size="small" :type="ticket.status === 'refunded' ? 'danger' : ticket.status === 'used' ? 'success' : 'info'">{{ ticketStatusLabel(ticket.status) }}</el-tag></span></div>
                  <span v-else class="muted-action">暂无出票</span>
                </template>
              </el-table-column>
            </el-table>
            <el-divider content-position="left">支付流水</el-divider>
            <el-table :data="selectedOrderDetail.payments || []" stripe size="small">
              <el-table-column prop="payment_no" label="流水号" min-width="180" />
              <el-table-column prop="method" label="方式" width="100" />
              <el-table-column label="金额" width="110"><template #default="{ row }">¥{{ Number(row.amount_cents || Math.round(Number(row.amount || 0) * 100)) / 100 }}</template></el-table-column>
              <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="paymentStatusTag(row.status)">{{ paymentStatusLabel(row.status) }}</el-tag></template></el-table-column>
              <el-table-column prop="error_message" label="说明" min-width="180" />
            </el-table>
            <el-divider content-position="left">打印任务</el-divider>
            <el-table :data="selectedOrderDetail.print_jobs || []" stripe size="small">
              <el-table-column prop="id" label="任务" width="80" />
              <el-table-column prop="ticket_code" label="票码" min-width="180" />
              <el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="printStatusTag(row.status)">{{ printStatusLabel(row.status) }}</el-tag></template></el-table-column>
              <el-table-column prop="last_error" label="最后说明" min-width="220" />
            </el-table>
            <el-divider content-position="left">售后记录</el-divider>
            <el-table :data="selectedOrderDetail.after_sales || []" stripe size="small">
              <el-table-column prop="request_no" label="申请号" min-width="170" />
              <el-table-column label="类型" width="100"><template #default="{ row }">{{ afterSaleTypeLabel(row.type) }}</template></el-table-column>
              <el-table-column label="状态" width="110"><template #default="{ row }"><el-tag :type="afterSaleStatusTag(row.status)">{{ afterSaleStatusLabel(row.status) }}</el-tag></template></el-table-column>
              <el-table-column prop="reason" label="原因" min-width="220" />
            </el-table>
            <el-alert class="mt-3" type="info" :closable="false" title="售后申请、审核和资金处理仍由授权后台岗位执行；窗口售票员不能绕过审批直接退款或作废。" />
          </template>
          <el-empty v-else description="暂无订单详情" />
        </div>
        <template #footer><el-button @click="showOrderDetail = false">关闭</el-button></template>
      </el-dialog>

      <el-dialog v-model="showPolicy" title="票务政策" width="600px" align-center>
        <PolicyModal />
      </el-dialog>

      <el-dialog v-model="showNote" title="交班便签" width="400px" align-center>
        <el-input v-model="noteContent" type="textarea" rows="5" placeholder="请记录需要传达给下一班次的事项..." />
        <template #footer>
          <el-button @click="showNote = false">取消</el-button>
          <el-button type="primary" @click="saveNote">保存</el-button>
        </template>
      </el-dialog>

    </main>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { 
  Monitor, List, Checked, Setting, SwitchButton, 
  Reading, Grid, Printer, Notebook, Refresh,
  ShoppingCart, FullScreen, Search, Plus, Minus,
  Tickets, Wallet, Warning, EditPen, Place,
  CircleCheck, CircleClose
} from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import axios from 'axios'
import { localizeErrorMessage } from '../utils/localize'

const router = useRouter()

// Configure Axios
axios.defaults.baseURL = import.meta.env.VITE_API_URL || 'http://127.0.0.1:8080/api/v1'
axios.interceptors.request.use(config => {
  const token = sessionStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

axios.interceptors.response.use(res => res, err => {
  if (err.response && err.response.status === 401) {
    ElMessage.error('登录失效，请重新登录')
    router.push('/login')
  } else {
    const message = localizeErrorMessage(err.response?.data?.error || err.message)
    if (err.response?.data && typeof err.response.data === 'object') err.response.data.error = message
    err.message = message
  }
  return Promise.reject(err)
})

import Calculator from '../components/Calculator.vue'
import PolicyModal from '../components/PolicyModal.vue'
import PaymentModal from '../components/PaymentModal.vue'
import { printTicket } from '../services/hardwareBridge'
import { checkDesktopUpdate, installDesktopUpdate } from '../services/desktopUpdateBridge'

// --- State ---
const currentView = ref('pos')
const searchQuery = ref('')
const categoryFilter = ref('')
const priceSearch = ref<number | undefined>(undefined)
const products = ref<any[]>([])
const cart = ref<any[]>([])
const searchInput = ref()
const currentTime = ref('')
const currentStaff = ref({ name: '?', job_number: '?', roles: '' })
const staffRoles = computed(() => String(currentStaff.value.roles || '').split(',').map(role => role.trim()).filter(Boolean))
const canSell = computed(() => staffRoles.value.includes('seller'))
const canVerify = computed(() => staffRoles.value.includes('checker'))


// --- Modals State ---
const showCalc = ref(false)
const showPolicy = ref(false)
const showNote = ref(false)
const noteContent = ref('')
const showPayment = ref(false)
const paymentLocked = ref(false)
const currentOrder = ref<any>(null)
const paymentComplete = ref(false)
const orderClientRequestID = ref('')
const pendingPrintOrderNo = ref('')
const showHolds = ref(false)
const holds = ref<any[]>([])
const holdsLoading = ref(false)
const showPrintTasks = ref(false)
const printJobs = ref<any[]>([])
const printJobsLoading = ref(false)
const printJobPrinting = ref<number | null>(null)
const showOrderDetail = ref(false)
const selectedOrderDetail = ref<any>(null)
const orderDetailLoading = ref(false)
const updateChecking = ref(false)
const desktopVersion = ref('')

// --- Orders State ---
const orders = ref<any[]>([])
const orderSearchQuery = ref('')
const orderDateRange = ref<[string, string] | null>(null)
const orderStatus = ref('')
const ordersLoading = ref(false)
const orderPage = ref(1)
const orderPageSize = ref(20)
const orderTotal = ref(0)

// --- Verify State ---
const verifyInput = ref('')
const verifyHistory = ref<any[]>([])
const verifyInputRef = ref()
const checkpoints = ref<any[]>([])
const posTerminals = ref<any[]>([])
const currentCheckPointId = ref<number | null>(null)
const posDeviceId = ref<number | null>(null)

// --- Settings Logic ---
const shiftState = ref({
  isOpen: false,
  shiftId: null as number | null,
  startTime: null as string | null,
  operator: '未登录员工',
  openingCents: 0
})
const showOpenShift = ref(false)
const showCloseShift = ref(false)
const openingAmount = ref(0)
const closingAmount = ref(0)
const closingNotes = ref('')
const closeSummary = ref<any>(null)
const shiftSummaryLoading = ref(false)
const shiftSubmitting = ref(false)
const shiftMethods = [
  { key: 'cash', label: '现金' },
  { key: 'pos', label: 'POS机' },
  { key: 'wechat', label: '微信' },
  { key: 'alipay', label: '支付宝' }
]

const fetchCheckPoints = async () => {
  try {
    const res = await axios.get('/checkpoints', { params: { page_size: 100 } })
    checkpoints.value = res.data.data
  } catch (e) {
    console.error('Failed to fetch checkpoints', e)
  }
}

const selectPOSTerminal = (deviceId: number | null, notify = true) => {
  posDeviceId.value = deviceId
  const terminal = posTerminals.value.find((item: any) => item.id === deviceId)
  if (!terminal) {
    localStorage.removeItem('pos_device_id')
    return
  }
  localStorage.setItem('pos_device_id', String(terminal.id))
  if (terminal.check_point_id) {
    currentCheckPointId.value = terminal.check_point_id
    localStorage.setItem('pos_checkpoint_id', String(terminal.check_point_id))
  }
  if (notify) ElMessage.success('售票终端已切换')
}

const fetchPOSTerminals = async () => {
  try {
    const { data } = await axios.get('/operations/terminals')
    posTerminals.value = data.data || []
    const selected = posTerminals.value.find((item: any) => item.id === posDeviceId.value)
    if (!selected) {
      posDeviceId.value = posTerminals.value.length > 0 ? posTerminals.value[0].id : null
      localStorage.removeItem('pos_shift_state')
      shiftState.value = { isOpen: false, shiftId: null, startTime: null, operator: currentStaff.value.name || '当前员工', openingCents: 0 }
    }
    if (posDeviceId.value) selectPOSTerminal(posDeviceId.value, false)
    else localStorage.removeItem('pos_device_id')
  } catch (error: any) {
    posTerminals.value = []
    posDeviceId.value = null
    localStorage.removeItem('pos_device_id')
    ElMessage.error(error.response?.data?.error || '获取可用售票终端失败')
  }
}

const offerDesktopUpdate = async (force = false) => {
  if (updateChecking.value) return
  updateChecking.value = true
  let offeredVersion = ''
  try {
    const update = await checkDesktopUpdate()
    if (!update) {
      if (force) ElMessage.warning('当前窗口端无法连接更新服务')
      return
    }
    desktopVersion.value = update.current_version ? update.current_version.slice(0, 7) : '未知'
    const pendingVersion = localStorage.getItem('pos_update_pending_version')
    if (pendingVersion && pendingVersion === update.current_version) {
      localStorage.removeItem('pos_update_pending_version')
      ElMessage.success(`窗口端已更新至 ${desktopVersion.value}`)
    }
    if (!update.available) {
      if (force) ElMessage.success(update.message || '当前已是最新版本')
      return
    }
    if (!force && sessionStorage.getItem(`pos_update_skipped_${update.version}`)) return
    offeredVersion = update.version
    await ElMessageBox.confirm('发现窗口端新版本。现在更新将自动下载、校验并重启窗口端。', '窗口端更新', {
      confirmButtonText: '立即更新',
      cancelButtonText: '本次稍后',
      type: 'info',
    })
    localStorage.setItem('pos_update_pending_version', update.version)
    const progressMessage = ElMessage({ message: '正在下载并校验更新，请勿关闭窗口端', type: 'info', duration: 0 })
    const result = await installDesktopUpdate()
    if (!result.success) {
      progressMessage.close()
      localStorage.removeItem('pos_update_pending_version')
      ElMessage.error(result.message)
    }
  } catch (reason) {
    if ((reason === 'cancel' || reason === 'close') && offeredVersion) sessionStorage.setItem(`pos_update_skipped_${offeredVersion}`, '1')
  } finally {
    updateChecking.value = false
  }
}

const saveSettings = () => {
  if (currentCheckPointId.value) {
    localStorage.setItem('pos_checkpoint_id', currentCheckPointId.value.toString())
  }
  if (posDeviceId.value) {
    localStorage.setItem('pos_device_id', posDeviceId.value.toString())
  }
  ElMessage.success('设置已保存')
}

const loadSettings = () => {
  const savedId = localStorage.getItem('pos_checkpoint_id')
  if (savedId) {
    currentCheckPointId.value = parseInt(savedId)
  }
  const savedDeviceId = localStorage.getItem('pos_device_id')
  if (savedDeviceId) posDeviceId.value = parseInt(savedDeviceId)
  const savedShift = localStorage.getItem('pos_shift_state')
  if (savedShift) {
    try {
      shiftState.value = JSON.parse(savedShift)
    } catch (e) {}
  }

  // Load note
  const savedNote = localStorage.getItem('pos_shift_note')
  if (savedNote) noteContent.value = savedNote
}

const handleShiftAction = async () => {
  if (!posDeviceId.value) {
    ElMessage.warning('当前工号尚未分配售票终端，请联系管理员')
    return
  }
  if (!shiftState.value.isOpen) {
    openingAmount.value = 0
    showOpenShift.value = true
  } else {
    await prepareCloseShift()
  }
}

const submitOpenShift = async () => {
  if (!posDeviceId.value) return
  shiftSubmitting.value = true
  try {
    const res = await axios.post('/operations/shifts', { device_id: posDeviceId.value, opening_cents: Math.round(openingAmount.value * 100) })
    const shift = res.data
    shiftState.value = { isOpen: true, shiftId: shift.id, startTime: shift.opened_at, operator: currentStaff.value.name || '当前操作员', openingCents: shift.opening_cents || 0 }
    localStorage.setItem('pos_shift_state', JSON.stringify(shiftState.value))
    showOpenShift.value = false
    ElMessage.success('已开始当班')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '开班失败')
  } finally {
    shiftSubmitting.value = false
  }
}

const prepareCloseShift = async () => {
  if (!shiftState.value.shiftId) { ElMessage.error('当前班次缺少服务端编号，请重新登录恢复'); return }
  showCloseShift.value = true
  shiftSummaryLoading.value = true
  try {
    const { data } = await axios.get(`/operations/shifts/${shiftState.value.shiftId}/summary`)
    closeSummary.value = data
    closingAmount.value = (data.cash_expected_cents || 0) / 100
    closingNotes.value = noteContent.value
  } catch (error: any) {
    showCloseShift.value = false
    ElMessage.error(error.response?.data?.error || '获取班次汇总失败')
  } finally {
    shiftSummaryLoading.value = false
  }
}

const submitCloseShift = async () => {
  if (!shiftState.value.shiftId) return
  shiftSubmitting.value = true
  try {
    await axios.post(`/operations/shifts/${shiftState.value.shiftId}/close`, { closing_cents: Math.round(closingAmount.value * 100), notes: closingNotes.value })
    const difference = closeDifferenceCents.value
    showCloseShift.value = false
    shiftState.value = { isOpen: false, shiftId: null, startTime: null, operator: currentStaff.value.name || '当前员工', openingCents: 0 }
    localStorage.removeItem('pos_shift_state')
    noteContent.value = ''
    localStorage.removeItem('pos_shift_note')
    ElMessage.success(difference === 0 ? '交班完成，现金账实相符' : `交班完成，现金差异 ${signedCents(difference)}`)
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '交班失败')
  } finally {
    shiftSubmitting.value = false
  }
}

const restoreOpenShift = async () => {
  const deviceId = Number(localStorage.getItem('pos_device_id') || 0)
  if (!deviceId) return
  try {
    const { data: shift } = await axios.get('/operations/shifts/open', { params: { device_id: deviceId } })
    shiftState.value = { isOpen: true, shiftId: shift.id, startTime: shift.opened_at, operator: currentStaff.value.name || '当前员工', openingCents: shift.opening_cents || 0 }
    localStorage.setItem('pos_shift_state', JSON.stringify(shiftState.value))
  } catch (error: any) {
    if (error.response?.status === 404) {
      shiftState.value = { isOpen: false, shiftId: null, startTime: null, operator: currentStaff.value.name || '当前员工', openingCents: 0 }
      localStorage.removeItem('pos_shift_state')
    }
  }
}

// Actions
const saveNote = () => {
  localStorage.setItem('pos_shift_note', noteContent.value)
  showNote.value = false
  ElMessage.success('便签已保存')
}

const serverPrintPayload = (job: any) => {
  let document: unknown = job?.print_document
  if (typeof document === 'string') {
    try { document = JSON.parse(document) } catch { document = null }
  }
  if (!document || !job?.content_hash) throw new Error('服务端打印快照缺失，已拒绝调用打印机')
  return { document, content_hash: job.content_hash, template_revision_id: job.template_revision_id, paper_width_mm: job.paper_width_mm, orientation: job.orientation || (document as any).orientation || 'portrait', copy_count: job.copy_count || 1 }
}

const openPrintTaskCenter = async () => {
  if (!posDeviceId.value) {
    ElMessage.warning('请先配置当前售票终端')
    return
  }
  showPrintTasks.value = true
  await loadPrintJobs()
}

const loadPrintJobs = async () => {
  if (!posDeviceId.value) return
  printJobsLoading.value = true
  try {
    const { data } = await axios.get('/operations/print-jobs', { params: { device_id: posDeviceId.value } })
    printJobs.value = Array.isArray(data.data) ? data.data : []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '获取打印任务失败')
  } finally {
    printJobsLoading.value = false
  }
}

const retryPrintJob = async (job: any) => {
  if (!posDeviceId.value || !job || !['queued', 'failed'].includes(job.status)) return
  try {
    await ElMessageBox.confirm(
      `确认只重打任务 ${job.id}${job.ticket_code ? `（${job.ticket_code}）` : ''}？请先确认这张票没有已经出纸。`,
      '逐项重打确认',
      { type: 'warning', confirmButtonText: '确认重打', cancelButtonText: '取消' },
    )
    printJobPrinting.value = job.id
    await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printing' })
    let physicalPrinted = false
    try {
      const result = await printTicket(serverPrintPayload(job))
      if (!result?.success) throw new Error(result?.message || '打印失败')
      physicalPrinted = true
      await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'printed' })
      ElMessage.success(`任务 ${job.id} 已打印`)
    } catch (error: any) {
      if (physicalPrinted) {
        await loadPrintJobs()
        ElMessage.warning('打印机已报告出纸，但任务状态同步失败；已保留为打印中，请人工确认后再处理。')
        return
      }
      await axios.post(`/operations/print-jobs/${job.id}/status`, { device_id: posDeviceId.value, status: 'failed', error: error.message || '打印失败' }).catch(() => undefined)
      throw error
    }
    await loadPrintJobs()
    await clearPendingPrintIfComplete()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || error.message || '重打失败')
    await loadPrintJobs()
  } finally {
    printJobPrinting.value = null
  }
}

const clearPendingPrintIfComplete = async () => {
  if (!pendingPrintOrderNo.value) return
  try {
    const { data } = await axios.get(`/orders/${encodeURIComponent(pendingPrintOrderNo.value)}`)
    const jobs = data.print_jobs || []
    if (jobs.length > 0 && jobs.every((job: any) => job.status === 'printed')) {
      pendingPrintOrderNo.value = ''
      localStorage.removeItem('pos_pending_print_order')
      paymentComplete.value = false
      currentOrder.value = null
      orderClientRequestID.value = ''
      cart.value = []
      ElMessage.success('打印任务已全部完成，可以开始下一笔交易')
    }
  } catch {
    // Keep the lock when the reconciliation query is unavailable.
  }
}

const openOrderDetail = async (orderNo: string) => {
  if (!String(orderNo || '').trim()) return
  showOrderDetail.value = true
  selectedOrderDetail.value = null
  orderDetailLoading.value = true
  try {
    const { data } = await axios.get(`/orders/${encodeURIComponent(orderNo)}`)
    selectedOrderDetail.value = data
    if (pendingPrintOrderNo.value === orderNo && data.order) currentOrder.value = data.order
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '获取订单详情失败')
  } finally {
    orderDetailLoading.value = false
  }
}

const handleHold = async () => {
  if (cart.value.length === 0) {
    await loadHolds()
    showHolds.value = true
    return
  }
  if (!shiftState.value.isOpen || !shiftState.value.shiftId || !posDeviceId.value) {
    ElMessage.warning('请先开班并配置售票终端')
    return
  }
  try {
    await axios.post('/operations/holds', {
      device_id: posDeviceId.value,
      shift_id: shiftState.value.shiftId,
      items: cart.value.map(orderLinePayload)
    })
    cart.value = []
    ElMessage.success('挂单已保存')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '挂单失败')
  }
}

const openHolds = async () => {
  await loadHolds()
  showHolds.value = true
}

const loadHolds = async () => {
  holdsLoading.value = true
  try {
    const { data } = await axios.get('/operations/holds', { params: { status: 'held', page_size: 50 } })
    holds.value = data.data || []
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || '获取挂单失败')
  } finally {
    holdsLoading.value = false
  }
}

const formatHoldItems = (hold: any) => {
  return (hold.items || []).map((item: any) => `${item.bundle_product_id ? `组合 #${item.bundle_product_id}` : `商品 #${item.product_id}`} x${item.quantity}`).join('，')
}

const resumeHold = async (hold: any) => {
  try {
    const { data } = await axios.post(`/operations/holds/${hold.id}/resume`)
    const restored = (data.items || []).map((line: any) => {
      const key = line.bundle_product_id ? `bundle-${line.bundle_product_id}` : `product-${line.product_id}`
      const product = products.value.find(item => item.catalogKey === key)
      if (!product) throw new Error(`${line.bundle_product_id ? '组合产品' : '商品'} #${line.bundle_product_id || line.product_id} 已不再可售`)
      return { ...product, quantity: line.quantity }
    })
    cart.value = restored
    showHolds.value = false
    ElMessage.success('挂单已恢复，请核对后结账')
  } catch (error: any) {
    ElMessage.error(error.response?.data?.error || error.message || '恢复挂单失败')
    await loadHolds()
  }
}

const cancelHold = async (hold: any) => {
  try {
    await ElMessageBox.confirm(`确认取消挂单 ${hold.hold_no}？`, '取消挂单', { type: 'warning' })
    await axios.post(`/operations/holds/${hold.id}/cancel`, { reason: '收银员取消挂单' })
    ElMessage.success('挂单已取消')
    await loadHolds()
  } catch (error: any) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(error.response?.data?.error || '取消挂单失败')
  }
}

const handleLogout = () => {
    ElMessageBox.confirm('确定要退出登录吗?', '提示', { type: 'warning' })
        .then(() => {
            sessionStorage.clear()
            router.push('/login')
        })
}

// --- Computed ---
const getPageTitle = computed(() => {
  const map: any = { pos: '收银台', orders: '订单管理', verify: '核销终端', settings: '系统设置' }
  return map[currentView.value]
})

const cartItemCount = computed(() => cart.value.reduce((sum, item) => sum + item.quantity, 0))

const currentCheckpointName = computed(() => {
  if (!currentCheckPointId.value) return '未配置检票点'
  return checkpoints.value.find(item => item.id === currentCheckPointId.value)?.name || `检票点 ${currentCheckPointId.value}`
})

const orderStatusLabel = (status: string) => {
  const labels: Record<string, string> = { unpaid: '待支付', paid: '已支付', completed: '已完成', partial_refunded: '部分退款', refunded: '已退款', cancelled: '已取消' }
  return labels[status] || '未知状态'
}
const orderStatusTag = (status: string) => status === 'paid' || status === 'completed' ? 'success' : status === 'partial_refunded' ? 'warning' : status === 'refunded' || status === 'cancelled' ? 'danger' : 'info'
const ticketStatusLabel = (status: string) => ({ unused: '未使用', active: '有效', used: '已核销', refunded: '已退款', cancelled: '已取消' } as Record<string, string>)[status] || status || '未知'
const paymentStatusLabel = (status: string) => ({ pending: '待确认', paid: '已支付', partial_refunded: '部分退款', refunded: '已退款', failed: '失败' } as Record<string, string>)[status] || status || '未知'
const paymentStatusTag = (status: string) => status === 'paid' ? 'success' : status === 'failed' ? 'danger' : status === 'refunded' || status === 'partial_refunded' ? 'warning' : 'info'
const printStatusLabel = (status: string) => ({ queued: '排队中', printing: '打印中·待确认', printed: '已打印', failed: '失败待处理' } as Record<string, string>)[status] || status || '未知'
const printStatusTag = (status: string) => status === 'printed' ? 'success' : status === 'failed' ? 'danger' : status === 'printing' ? 'warning' : 'info'
const afterSaleTypeLabel = (type: string) => ({ refund: '退款', reschedule: '改期', exchange: '换票', void: '作废', reissue: '补打' } as Record<string, string>)[type] || type || '未知'
const afterSaleStatusLabel = (status: string) => ({ pending: '待审核', approved: '已批准', processing: '处理中', completed: '已完成', rejected: '已拒绝', failed: '失败' } as Record<string, string>)[status] || status || '未知'
const afterSaleStatusTag = (status: string) => status === 'completed' ? 'success' : status === 'failed' ? 'danger' : status === 'rejected' ? 'info' : 'warning'

const filteredProducts = computed(() => {
  let res = products.value
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    res = res.filter(p => p.name.toLowerCase().includes(query))
  }
  if (categoryFilter.value) res = res.filter(p => p.parsedTags?.includes(categoryFilter.value))
  if (priceSearch.value !== undefined && priceSearch.value !== null) {
    const targetCents = Math.round(Number(priceSearch.value) * 100)
    res = res.filter(p => Math.round(Number(p.price) * 100) === targetCents)
  }
  return res
})

const productCategories = computed(() => Array.from(new Set(products.value.flatMap(product => product.parsedTags || []))) as string[])

const clearProductFilters = () => {
  searchQuery.value = ''
  categoryFilter.value = ''
  priceSearch.value = undefined
}

const totalAmount = computed(() => cart.value.reduce((sum, item) => sum + item.price * item.quantity, 0))
const closeDifferenceCents = computed(() => Math.round(closingAmount.value * 100) - Number(closeSummary.value?.cash_expected_cents || 0))
const cents = (value: number | undefined) => ((Number(value) || 0) / 100).toFixed(2)
const signedCents = (value: number | undefined) => `${Number(value || 0) > 0 ? '+' : Number(value || 0) < 0 ? '-' : ''}¥${cents(Math.abs(Number(value || 0)))}`
const methodSummary = (method: string) => closeSummary.value?.payments?.find((item: any) => item.method === method) || { gross_cents: 0, refund_cents: 0, net_cents: 0 }

// --- Methods ---
const updateTime = () => {
  const now = new Date()
  currentTime.value = now.toLocaleString()
}

const fetchProducts = async () => {
  try {
    const [productResponse, bundleResponse] = await Promise.all([
      axios.get('/products', { params: { page_size: 100, type: 'offline' } }),
      axios.get('/bundle-catalog', { params: { type: 'offline' } })
    ])
    const ordinary = productResponse.data.data.filter((p: any) => (
      !p.real_name_required && Number(p.limit_per_phone || 0) === 0 && Number(p.limit_per_id || 0) === 0 && !String(p.region_limit || '').trim()
    )).map((p: any) => {
      try {
        p.parsedTags = p.tags ? JSON.parse(p.tags) : []
      } catch (e) {
        p.parsedTags = []
      }
      return { ...p, catalogKey: `product-${p.id}`, is_bundle: false }
    })
    const bundles = (bundleResponse.data.data || []).map((item: any) => ({
      ...item, catalogKey: `bundle-${item.id}`, price: Number(item.retail_price_cents || 0) / 100,
      parsedTags: ['组合产品'], stock_type: 'unlimited', daily_stock: 0, is_bundle: true
    }))
    products.value = [...ordinary, ...bundles]
  } catch (e) {
    ElMessage.error('获取产品失败')
  }
}


const addToCart = (product: any) => {
  const existing = cart.value.find(item => item.catalogKey === product.catalogKey)
  if (existing) {
    existing.quantity++
  } else {
    cart.value.push({ ...product, quantity: 1 })
  }
}

const updateQty = (index: number, delta: number) => {
  const item = cart.value[index]
  item.quantity += delta
  if (item.quantity <= 0) {
    cart.value.splice(index, 1)
  }
}

const clearCart = () => {
  if (cart.value.length === 0) return
  ElMessageBox.confirm('确定清空当前购物车吗？', '提示', { type: 'warning' })
    .then(() => cart.value = [])
    .catch(() => {})
}

const handleCheckout = async () => {
  if (cart.value.length === 0) return
  if (!shiftState.value.isOpen || !shiftState.value.shiftId || !posDeviceId.value) {
    ElMessage.warning('请先在当前售票终端上开班')
    return
  }
  if (paymentComplete.value || pendingPrintOrderNo.value) {
    ElMessage.warning('当前已有已收款订单待完成打印，请先处理打印任务')
    return
  }
  if (currentOrder.value) {
    showPayment.value = true
    return
  }
  try {
    if (!orderClientRequestID.value) orderClientRequestID.value = globalThis.crypto?.randomUUID?.() || `window-${Date.now()}-${Math.random().toString(16).slice(2)}`
    const orderData = {
      items: cart.value.map(orderLinePayload),
      client_request_id: orderClientRequestID.value,
    }
    const res = await axios.post('/orders', orderData)
    currentOrder.value = res.data
    if (['paid', 'completed', 'partial_refunded'].includes(String(res.data?.status || ''))) {
      paymentComplete.value = true
      pendingPrintOrderNo.value = res.data.order_no
      localStorage.setItem('pos_pending_print_order', res.data.order_no)
      await handlePaymentSuccess()
      return
    }
    showPayment.value = true
  } catch (e: any) {
    ElMessage.error(e.response?.data?.error || '下单失败')
  }
}

const handlePaymentSuccess = async () => {
    paymentLocked.value = false
    showPayment.value = false
    paymentComplete.value = true
    if (!currentOrder.value) {
      ElMessage.warning('支付已成功，但未能恢复订单，请到订单页核对。')
      return
    }
    pendingPrintOrderNo.value = currentOrder.value.order_no
    localStorage.setItem('pos_pending_print_order', currentOrder.value.order_no)
    if (!posDeviceId.value || !shiftState.value.shiftId) {
      ElMessage.warning('支付已成功，但未能创建打印任务，请到打印任务中心处理。')
      return
    }
    const ticketCodes = [...new Set((currentOrder.value.items || [])
      .flatMap((item: any) => item.tickets || [])
      .map((ticket: any) => String(ticket.ticket_code || '').trim())
      .filter(Boolean))]
    // One server snapshot represents one template revision. Normal window
    // orders queue one job per ticket so product-specific templates cannot be
    // accidentally replaced by the first product in a mixed cart. The empty
    // fallback keeps compatibility with older order responses that did not
    // include ticket codes; the server still rejects mixed-template orders.
    const codesToPrint = ticketCodes.length > 0 ? ticketCodes : ['']
    const jobs: Array<{ job: any, status: 'queued' | 'printing' | 'printed', physicalPrinted: boolean }> = []
    try {
      for (const ticketCode of codesToPrint) {
        const queued = await axios.post('/operations/print-jobs', {
          device_id: posDeviceId.value,
          shift_id: shiftState.value.shiftId,
          order_no: currentOrder.value.order_no,
          ...(ticketCode ? { ticket_code: ticketCode } : {}),
        })
        jobs.push({ job: queued.data, status: 'queued', physicalPrinted: false })
      }
      for (const entry of jobs) {
        entry.status = 'printing'
        await axios.post(`/operations/print-jobs/${entry.job.id}/status`, { device_id: posDeviceId.value, status: 'printing' })
        const result = await printTicket(serverPrintPayload(entry.job))
        if (!result?.success) throw new Error(result?.message || '打印失败')
        // The device may already have produced paper when the follow-up
        // status request is lost. Do not turn this into a retryable failure.
        entry.physicalPrinted = true
        entry.status = 'printed'
        await axios.post(`/operations/print-jobs/${entry.job.id}/status`, { device_id: posDeviceId.value, status: 'printed' })
      }
      cart.value = []
      currentOrder.value = null
      paymentComplete.value = false
      pendingPrintOrderNo.value = ''
      orderClientRequestID.value = ''
      localStorage.removeItem('pos_pending_print_order')
      ElMessage.success('支付成功，打印完成')
    } catch (error: any) {
      for (const entry of jobs) {
        if (entry.status === 'printing' && !entry.physicalPrinted) {
          await axios.post(`/operations/print-jobs/${entry.job.id}/status`, { device_id: posDeviceId.value, status: 'failed', error: error.message || '打印失败' }).catch(() => undefined)
        }
      }
      const hasUnconfirmedPhysicalOutput = jobs.some(entry => entry.physicalPrinted && entry.status === 'printed')
      ElMessage.error(hasUnconfirmedPhysicalOutput
        ? '支付已成功，至少一张票已出纸但打印状态同步失败，任务已保留待人工确认，请勿直接重打。'
        : jobs.some(entry => entry.status === 'printed')
          ? '支付已成功，部分票据已打印，剩余打印任务已保留，请逐张重打。'
          : '支付已成功，但打印失败。订单和打印任务已保留，可稍后重打。')
    }
}

const handleVerify = async () => {
  const code = verifyInput.value.trim()
  if (!code) return
  
  try {
    // Determine if it's a ticket code or order no (simple heuristic or backend handles both)
    // For now assume ticket code or order no.
    // We need a checkpoint ID. For desktop POS, we might need to select a checkpoint or use a default one.
    const checkPointId = currentCheckPointId.value
    if (!checkPointId) {
      ElMessage.warning('请先在设置中配置当前检票点')
      return
    }
    
    await axios.post('/tickets/verify', {
      code: code,
      check_point_id: checkPointId,
      device_id: posDeviceId.value
    })
    
    ElMessage.success('核销成功')
    verifyHistory.value.unshift({
      code: code,
      status: 'success',
      time: new Date().toLocaleString(),
      msg: '核销成功'
    })
    verifyInput.value = ''
  } catch (e: any) {
    const msg = e.response?.data?.error || '核销失败'
    ElMessage.error(msg)
    verifyHistory.value.unshift({
      code: code,
      status: 'fail',
      time: new Date().toLocaleString(),
      msg: msg
    })
    verifyInput.value = ''
  }
}

const fetchOrders = async () => {
  ordersLoading.value = true
  try {
    const params: any = { page: orderPage.value, page_size: orderPageSize.value, channel: 'window' }
    if (orderSearchQuery.value) params.search = orderSearchQuery.value
    if (orderStatus.value) params.status = orderStatus.value
    if (orderDateRange.value && orderDateRange.value.length === 2) {
      params.start_date = orderDateRange.value[0]
      params.end_date = orderDateRange.value[1]
    }
    
    const res = await axios.get('/orders', { params })
    orders.value = res.data.data
    orderTotal.value = res.data.total || 0
  } catch (e) {
    ElMessage.error('获取订单失败')
  } finally {
    ordersLoading.value = false
  }
}

const orderLinePayload = (item: any) => item.is_bundle
  ? { bundle_product_id: item.id, quantity: item.quantity }
  : { product_id: item.id, quantity: item.quantity }

const searchOrders = () => {
  orderPage.value = 1
  fetchOrders()
}

const restorePendingPrintOrder = async () => {
  const orderNo = String(localStorage.getItem('pos_pending_print_order') || '').trim()
  if (!orderNo) return
  pendingPrintOrderNo.value = orderNo
  paymentComplete.value = true
  try {
    const { data } = await axios.get(`/orders/${encodeURIComponent(orderNo)}`)
    if (!data?.order || !['paid', 'completed', 'partial_refunded'].includes(String(data.order.status || ''))) {
      pendingPrintOrderNo.value = ''
      paymentComplete.value = false
      localStorage.removeItem('pos_pending_print_order')
      return
    }
    currentOrder.value = data.order
    await clearPendingPrintIfComplete()
  } catch {
    ElMessage.warning('检测到一笔已收款但打印状态待确认的订单，请在打印任务中心核对')
  }
}

// --- Lifecycle ---
let timer: any
let updateTimer: any
const handleWindowFocus = () => { void offerDesktopUpdate() }
const handleGlobalKeydown = (e: KeyboardEvent) => {
  if (e.key === 'F2' && canSell.value) { e.preventDefault(); searchInput.value?.focus() }
  if (e.key === 'F3' || (e.ctrlKey && e.key === 'f')) { e.preventDefault(); showPolicy.value = true }
  if (e.key === 'F5' && canSell.value) { e.preventDefault(); fetchProducts() }
  if (e.key === 'F4' && canSell.value) { e.preventDefault(); handleHold() }
  if (e.key === 'Delete' && canSell.value) clearCart()
  if (e.code === 'Space' && canSell.value && currentView.value === 'pos') { e.preventDefault(); handleCheckout() }
}

onMounted(async () => {
  loadSettings()
  const staffStr = sessionStorage.getItem('staff')
  if (staffStr) {
    try {
      currentStaff.value = JSON.parse(staffStr)
    } catch(e) {}
  }
  if (!canSell.value && canVerify.value) currentView.value = 'verify'
  if (canSell.value) {
    await Promise.all([fetchProducts(), fetchPOSTerminals()])
    await restoreOpenShift()
    await restorePendingPrintOrder()
  }
  await fetchCheckPoints()
  timer = setInterval(updateTime, 1000)
  updateTime()
  window.addEventListener('keydown', handleGlobalKeydown)
  window.addEventListener('focus', handleWindowFocus)
  updateTimer = setInterval(() => void offerDesktopUpdate(), 5 * 60 * 1000)
  void offerDesktopUpdate()
})

import { watch } from 'vue'
watch(currentView, (val) => {
  if (val === 'orders') fetchOrders()
  if (val === 'verify') {
    setTimeout(() => verifyInputRef.value?.focus(), 100)
  }
})

const handlePaymentCancelled = () => {
  paymentLocked.value = false
  showPayment.value = false
  paymentComplete.value = false
  pendingPrintOrderNo.value = ''
  orderClientRequestID.value = ''
  localStorage.removeItem('pos_pending_print_order')
  cart.value = []
  currentOrder.value = null
}

onUnmounted(() => {
  clearInterval(timer)
  clearInterval(updateTimer)
  window.removeEventListener('keydown', handleGlobalKeydown)
  window.removeEventListener('focus', handleWindowFocus)
})
</script>

<style scoped>
.pos-shell {
  --ink: #1d2420;
  --muted: #687169;
  --line: #d8ded8;
  --surface: #ffffff;
  --canvas: #eef1ed;
  --green: #14734a;
  --green-dark: #0d5d38;
  --amber: #b85e12;
  height: 100vh;
  min-width: 1024px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: var(--ink);
  background: var(--canvas);
}

.topbar {
  height: 62px;
  flex: 0 0 62px;
  display: flex;
  align-items: center;
  gap: 20px;
  padding: 0 18px;
  background: #18231d;
  color: #fff;
  border-bottom: 1px solid #131713;
}

.brand-block, .operator-block, .workspace-tabs, .workspace-tab, .shift-chip,
.catalog-toolbar, .product-tile, .product-action, .quick-tools, .cart-heading,
.cart-item-top, .cart-item-bottom, .quantity-stepper, .total-line, .checkout-actions,
.filter-bar, .verify-entry, .verify-context, .history-heading, .history-item,
.section-heading, .hardware-row, .shift-summary {
  display: flex;
  align-items: center;
}

.brand-block { width: 176px; gap: 10px; flex: 0 0 176px; }
.brand-mark { width: 36px; height: 36px; display: grid; place-items: center; border-radius: 6px; background: #f3c95d; color: #18231d; }
.brand-title { font-size: 16px; line-height: 20px; font-weight: 700; }
.brand-subtitle { margin-top: 1px; font-size: 11px; line-height: 14px; color: #aeb7ae; }

.workspace-tabs { height: 40px; padding: 3px; gap: 2px; border: 1px solid #3d4941; border-radius: 7px; background: #111914; }
.workspace-tab { height: 32px; min-width: 74px; justify-content: center; gap: 6px; border: 0; border-radius: 5px; background: transparent; color: #bcc4bd; cursor: pointer; font-size: 14px; }
.workspace-tab:hover { color: #fff; background: #303732; }
.workspace-tab.active { color: #162119; background: #fff; font-weight: 700; box-shadow: 0 1px 3px rgba(0, 0, 0, .14); }

.operator-block { min-width: 0; margin-left: auto; justify-content: flex-end; gap: 10px; }
.operator-meta { text-align: right; font-size: 12px; line-height: 17px; color: #d5dbd5; white-space: nowrap; }
.operator-meta .clock { display: block; color: #96a097; font-variant-numeric: tabular-nums; }
.shift-chip { height: 32px; gap: 7px; padding: 0 10px; border: 1px solid #59615a; border-radius: 6px; background: #303632; color: #d7ddd7; cursor: pointer; }
.shift-chip.open { border-color: #48a374; background: #163d2a; color: #dff5e8; }
.status-dot { width: 7px; height: 7px; border-radius: 50%; background: #9da49e; }
.shift-chip.open .status-dot { background: #57d28e; }
.icon-button { width: 32px; height: 32px; display: grid; place-items: center; border: 1px solid #4a514b; border-radius: 6px; background: transparent; color: #d7ddd7; cursor: pointer; }
.icon-button:hover { background: #363d37; color: #fff; }
.icon-button.danger:hover { border-color: #a84949; background: #582525; }

.workspace { flex: 1; min-height: 0; overflow: hidden; }
.sales-workspace { height: 100%; display: grid; grid-template-columns: minmax(0, 1fr) minmax(360px, 35%); }
.catalog-pane { min-width: 0; min-height: 0; display: flex; flex-direction: column; padding: 14px 16px 0; border-right: 1px solid var(--line); background: #f3f5f2; }
.readiness-banner { min-height: 40px; display: flex; align-items: center; gap: 8px; margin-bottom: 10px; padding: 8px 10px; border: 1px solid #e8c787; border-radius: 6px; background: #fff8e9; color: #7a4a0b; font-size: 13px; }
.readiness-banner span { min-width: 0; flex: 1; }
.readiness-banner button { border: 0; background: transparent; color: #8e4b08; font-weight: 700; cursor: pointer; }
.catalog-toolbar { gap: 9px; margin-bottom: 11px; }
.toolbar-label { height: 42px; min-width: 88px; display: flex; flex-direction: column; justify-content: center; padding-right: 10px; border-right: 1px solid #d6dcd5; }
.toolbar-label span { color: var(--muted); font-size: 11px; line-height: 14px; }
.toolbar-label strong { color: var(--ink); font-size: 16px; line-height: 19px; font-variant-numeric: tabular-nums; }
.catalog-toolbar :deep(.el-input) { flex: 1; }
.price-filter { width: 140px; flex: 0 0 140px; }
.catalog-toolbar :deep(.el-input__wrapper) { min-height: 42px; border-radius: 7px; box-shadow: 0 0 0 1px #ccd2ca inset; }
.catalog-toolbar :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 2px #278157 inset; }
.category-strip { display: flex; flex: 0 0 auto; gap: 6px; margin: -1px 0 11px; overflow-x: auto; padding-bottom: 3px; }
.category-strip button { min-width: 58px; height: 32px; padding: 0 13px; border: 1px solid #d7ddd5; border-radius: 5px; background: #fff; color: #4d554d; white-space: nowrap; }
.category-strip button:hover { border-color: #78a98e; color: var(--green); }
.category-strip button.active { border-color: var(--green); background: #e8f4ed; color: var(--green); font-weight: 700; }
.catalog-count { white-space: nowrap; color: var(--muted); font-size: 13px; }

.product-grid { min-height: 0; flex: 1; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); align-content: start; gap: 9px; overflow-y: auto; padding: 1px 5px 12px 1px; }
.product-tile { width: 100%; min-height: 112px; flex-direction: column; align-items: stretch; justify-content: space-between; gap: 10px; padding: 12px 13px 11px; text-align: left; border: 1px solid #d7ddd6; border-radius: 7px; background: var(--surface); color: var(--ink); cursor: pointer; }
.product-tile:hover { border-color: #72a78a; background: #fbfefc; box-shadow: 0 3px 10px rgba(30, 58, 40, .07); }
.product-tile:active { border-color: var(--green); background: #edf8f1; transform: translateY(1px); }
.product-main { min-width: 0; }
.product-name { min-width: 0; min-height: 40px; font-size: 14px; line-height: 20px; font-weight: 700; word-break: break-word; }
.product-tags { display: flex; flex-wrap: wrap; gap: 5px; margin-top: 8px; }
.product-tags span { max-width: 94px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 2px 6px; border-radius: 4px; background: #edf0eb; color: #667066; font-size: 10px; }
.product-tags .stock-tag { background: #fff4dc; color: #8a570f; }
.product-action { flex: 0 0 auto; justify-content: space-between; align-items: center; padding-top: 9px; border-top: 1px solid #edf0ec; }
.product-action strong { color: var(--amber); font-size: 17px; font-variant-numeric: tabular-nums; }
.add-icon { width: 25px; height: 25px; display: grid; place-items: center; border-radius: 5px; background: #e5f3eb; color: var(--green); }
.empty-state { grid-column: 1 / -1; min-height: 260px; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; color: #9aa199; }
.empty-state strong { color: #5e665e; }
.empty-state span { font-size: 13px; }

.quick-tools { height: 58px; flex: 0 0 58px; gap: 5px; border-top: 1px solid var(--line); }
.quick-tools button { height: 36px; display: flex; align-items: center; gap: 6px; padding: 0 10px; border: 1px solid transparent; border-radius: 6px; background: transparent; color: #566158; cursor: pointer; }
.quick-tools button:hover { border-color: #d5dbd4; background: #fff; color: var(--ink); }

.cart-pane { min-width: 0; min-height: 0; display: flex; flex-direction: column; background: #fff; }
.cart-heading { height: 70px; flex: 0 0 70px; justify-content: space-between; padding: 0 18px; border-bottom: 1px solid var(--line); }
.eyebrow { color: var(--muted); font-size: 11px; }
.cart-heading h2 { margin: 2px 0 0; font-size: 18px; line-height: 24px; }
.cart-heading h2 em { display: inline-flex; min-width: 22px; height: 22px; align-items: center; justify-content: center; margin-left: 5px; border-radius: 5px; background: #edf0eb; color: #586058; font-size: 12px; font-style: normal; }
.cart-list { flex: 1; min-height: 0; overflow-y: auto; padding: 12px 14px; background: #f5f7f4; }
.empty-cart { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 7px; color: #8b938b; }
.empty-cart-icon { width: 58px; height: 58px; display: grid; place-items: center; margin-bottom: 4px; border-radius: 8px; background: #edf0eb; color: #8a928a; }
.empty-cart strong { color: #586058; }
.empty-cart span { font-size: 13px; }
.cart-item { margin-bottom: 9px; padding: 12px; border: 1px solid #d9dfd8; border-left: 3px solid #7da991; border-radius: 7px; background: #fff; }
.cart-item-top { justify-content: space-between; gap: 12px; }
.cart-item-name { min-width: 0; font-size: 14px; line-height: 20px; font-weight: 700; word-break: break-word; }
.cart-item-top strong { flex: 0 0 auto; color: var(--amber); font-size: 16px; }
.cart-item-bottom { justify-content: space-between; margin-top: 10px; color: var(--muted); font-size: 12px; }
.quantity-stepper { height: 30px; overflow: hidden; border: 1px solid #cfd5cd; border-radius: 6px; background: #fff; }
.quantity-stepper button { width: 30px; height: 28px; display: grid; place-items: center; border: 0; background: #f0f2ee; color: #3d453e; cursor: pointer; }
.quantity-stepper button:hover { background: #dfe5dd; }
.quantity-stepper span { width: 34px; text-align: center; color: var(--ink); font-size: 14px; font-weight: 700; }
.checkout-panel { flex: 0 0 auto; padding: 15px 18px 18px; border-top: 1px solid var(--line); background: #fff; box-shadow: 0 -6px 18px rgba(31, 43, 35, .035); }
.sale-lock-banner { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 13px; padding: 10px 11px; border: 1px solid #e3c37a; border-radius: 7px; background: #fff8e8; color: #73500f; }
.sale-lock-banner > div:first-child { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.sale-lock-banner strong { font-size: 13px; }
.sale-lock-banner span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; }
.sale-lock-actions { display: flex; flex: 0 0 auto; gap: 5px; }
.total-line { justify-content: space-between; margin-bottom: 14px; color: var(--muted); }
.total-line strong { color: var(--amber); font-size: 31px; line-height: 36px; font-variant-numeric: tabular-nums; }
.checkout-actions { gap: 10px; }
.checkout-actions :deep(.el-button) { height: 46px; margin: 0; border-radius: 7px; font-weight: 700; }
.checkout-actions :deep(.el-button:first-child) { width: 110px; }
.checkout-actions :deep(.el-button:last-child) { flex: 1; }

.page-workspace { height: 100%; min-height: 0; display: flex; flex-direction: column; padding: 20px; }
.page-heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.page-heading h1 { margin: 0; font-size: 22px; line-height: 28px; }
.page-heading p { margin: 3px 0 0; color: var(--muted); font-size: 13px; }
.filter-bar { gap: 10px; margin-bottom: 12px; padding: 12px; border: 1px solid var(--line); border-radius: 7px; background: #fff; }
.filter-bar :deep(.el-input) { width: 240px; }
.filter-bar :deep(.el-date-editor) { width: 260px; }
.filter-bar :deep(.el-select) { width: 140px; }
.data-panel { min-height: 0; flex: 1; overflow: hidden; border: 1px solid var(--line); border-radius: 7px; background: #fff; }
.order-pagination { justify-content: flex-end; padding-top: 12px; }
.order-item-text { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.money { color: var(--amber); font-variant-numeric: tabular-nums; }
.print-task-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 10px; color: var(--muted); font-size: 13px; }
.muted-action { color: #8b948c; font-size: 12px; }
.order-detail-dialog { min-height: 260px; }
.order-detail-summary { display: grid; grid-template-columns: 1.5fr .8fr .8fr 1.2fr; gap: 10px; padding: 13px; border: 1px solid #dce3db; border-radius: 7px; background: #f7faf7; }
.order-detail-summary > div { min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.order-detail-summary > div > strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ticket-detail-list { display: flex; flex-wrap: wrap; gap: 6px; }
.ticket-detail-list > span { display: inline-flex; align-items: center; gap: 5px; padding: 3px 5px; border: 1px solid #e1e6df; border-radius: 4px; background: #fbfcfa; }
.ticket-detail-list code { color: #3d5545; font-size: 11px; }

.verify-workspace { height: 100%; display: grid; grid-template-columns: minmax(0, 1fr) 360px; background: #f7f8f5; }
.verify-main { display: flex; flex-direction: column; align-items: center; justify-content: center; padding: 30px; }
.verify-heading { text-align: center; }
.verify-icon { width: 54px; height: 54px; display: grid; place-items: center; margin: 0 auto 14px; border-radius: 8px; background: #e2f1e8; color: var(--green); font-size: 27px; }
.verify-heading h1 { margin: 0; font-size: 26px; }
.verify-heading p { margin: 7px 0 0; color: var(--muted); }
.verify-entry { width: min(680px, 92%); gap: 10px; margin-top: 28px; }
.verify-entry :deep(.el-input__wrapper) { min-height: 58px; border-radius: 7px; box-shadow: 0 0 0 2px #cbd2c9 inset; }
.verify-entry :deep(.el-input__inner) { text-align: center; font-size: 21px; font-variant-numeric: tabular-nums; }
.verify-entry :deep(.el-button) { height: 58px; min-width: 120px; border-radius: 7px; font-weight: 700; }
.verify-context { gap: 18px; margin-top: 16px; color: var(--muted); font-size: 13px; }
.verify-context span { display: flex; align-items: center; gap: 5px; }
.history-pane { min-height: 0; display: flex; flex-direction: column; border-left: 1px solid var(--line); background: #fff; }
.history-heading { height: 62px; justify-content: space-between; padding: 0 18px; border-bottom: 1px solid var(--line); }
.history-heading h2 { margin: 0; font-size: 17px; }
.history-heading span { color: var(--muted); font-size: 12px; }
.history-list { min-height: 0; flex: 1; overflow-y: auto; padding: 12px; }
.history-empty { padding-top: 80px; text-align: center; color: #9aa19a; }
.history-item { align-items: flex-start; gap: 10px; margin-bottom: 8px; padding: 12px; border: 1px solid #dfe3dc; border-left: 4px solid #23915b; border-radius: 6px; }
.history-item.fail { border-left-color: #c74646; }
.history-item > .el-icon { margin-top: 2px; color: #23915b; }
.history-item.fail > .el-icon { color: #c74646; }
.history-item div { min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.history-item strong { font-size: 14px; }
.history-item span { overflow: hidden; text-overflow: ellipsis; color: #596159; font-size: 12px; white-space: nowrap; }
.history-item small { color: #969d96; }

.settings-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; }
.settings-section { padding: 18px; border: 1px solid var(--line); border-radius: 7px; background: #fff; }
.section-heading { align-items: flex-start; gap: 10px; margin-bottom: 18px; }
.section-heading > .el-icon { margin-top: 2px; color: var(--green); font-size: 20px; }
.section-heading h2 { margin: 0; font-size: 17px; }
.section-heading p { margin: 4px 0 0; color: var(--muted); font-size: 12px; line-height: 18px; }
.terminal-empty { margin-top: 8px; color: #b5473b; font-size: 12px; line-height: 18px; }
.hardware-row, .shift-summary { justify-content: space-between; min-height: 46px; border-top: 1px solid #ecefeb; }
.hardware-row:last-child { border-bottom: 1px solid #ecefeb; }
.version-action { display: flex; align-items: center; gap: 8px; }
.version-action code { color: #596159; font-size: 12px; }
.shift-summary:last-of-type { margin-bottom: 16px; }
.shift-dialog-intro { margin-bottom: 18px; padding: 10px 12px; border: 1px solid #dfe4dc; border-radius: 6px; background: #f6f8f5; color: #626a62; font-size: 13px; line-height: 20px; }
.money-input { width: 100%; }
.money-input :deep(.el-input__wrapper) { min-height: 48px; }
.money-input :deep(.el-input__inner) { text-align: left; font-size: 22px; font-weight: 700; }
.shift-summary-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.shift-method-panel { padding: 12px; border: 1px solid #dfe3dc; border-radius: 7px; background: #fafbf9; }
.shift-method-panel > div { display: flex; align-items: center; justify-content: space-between; min-height: 27px; color: #697169; font-size: 12px; }
.shift-method-panel .shift-method-title { min-height: 34px; margin-bottom: 5px; padding-bottom: 7px; border-bottom: 1px solid #e5e9e3; color: #303630; }
.shift-method-title span { font-size: 15px; font-weight: 700; }
.shift-method-title strong { color: #16784a; }
.shift-method-panel b { color: #343a34; font-weight: 600; }
.cash-count-panel { display: grid; grid-template-columns: 1fr 1fr 1.4fr 1fr; align-items: end; gap: 10px; margin-top: 12px; padding: 14px; border: 1px solid #d9ded7; border-radius: 7px; background: #fff; }
.cash-fact, .cash-difference { min-height: 52px; display: flex; flex-direction: column; justify-content: center; gap: 4px; }
.cash-fact span, .cash-difference span, .cash-count-input label { color: #717971; font-size: 12px; }
.cash-fact strong, .cash-difference strong { font-size: 18px; font-variant-numeric: tabular-nums; }
.cash-count-input :deep(.el-input-number) { width: 100%; margin-top: 5px; }
.cash-count-input :deep(.el-input__inner) { text-align: left; font-weight: 700; }
.cash-difference strong { color: #bf3f3f; }
.cash-difference.balanced strong { color: #16784a; }

.custom-scrollbar::-webkit-scrollbar { width: 6px; }
.custom-scrollbar::-webkit-scrollbar-track { background: transparent; }
.custom-scrollbar::-webkit-scrollbar-thumb { border-radius: 3px; background: #c4cbc3; }
.custom-scrollbar::-webkit-scrollbar-thumb:hover { background: #a8b0a7; }

:deep(.el-dialog) { border-radius: 8px; }
:deep(.el-button--success) { --el-button-bg-color: var(--green); --el-button-border-color: var(--green); --el-button-hover-bg-color: var(--green-dark); --el-button-hover-border-color: var(--green-dark); }

@media (max-width: 1120px) {
  .topbar { gap: 12px; padding: 0 12px; }
  .brand-block { width: 150px; flex-basis: 150px; }
  .workspace-tab { min-width: 66px; }
  .operator-meta .clock { display: none; }
  .product-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .quick-tools button { padding: 0 7px; }
}
</style>
