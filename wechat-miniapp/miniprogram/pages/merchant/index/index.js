const storage = require('../../../services/storage');
const format = require('../../../utils/format');
const api = require('../../../services/api');
const commerce = require('../../../config/commerce');
const notifications = require('../../../config/notifications');

Page({
  data: { stats: { salesText: '0.00', pending: 0 }, merchantUnavailable: false, tabs: [{ id: 'ALL', name: '全部' }, { id: 'PAID', name: '待处理' }, { id: 'PREPARING', name: '制作中' }, { id: 'DELIVERING', name: '配送中' }, { id: 'SHIPPED', name: '待收货' }, { id: 'COMPLETED', name: '已完成' }], activeTab: 'ALL', allOrders: [], filteredOrders: [], notificationConfigured: Boolean(notifications.orderNoticeTemplateId) },
  deliveryDrafts: {},
  onShow() {
    if (!api.isProduction()) { this.setData({ canOrders: true, canProducts: true, canSettings: true, canRefund: true }); this.loadOrders(); return; }
    // Merchant storefront APIs are not part of the public SaaS customer
    // boundary yet. Do not render local demo facts as a production dashboard.
    this.setData({ merchantUnavailable: true, canOrders: false, canProducts: false, canSettings: false, canRefund: false, stats: {}, allOrders: [], filteredOrders: [] });
    return;
  },

  loadOrders() {
    if (api.isProduction()) {
      api.getMerchantOrders().then((result) => this.renderOrders(result.data || [])).catch((error) => { console.error('load merchant orders failed', error); wx.showToast({ title: '订单加载失败', icon: 'none' }); });
      return;
    }
    this.renderOrders(storage.getOrders());
  },

  renderOrders(raw) {
    const today = new Date().toDateString();
    const todayOrders = raw.filter((item) => new Date(item.createdAt).toDateString() === today);
    const sales = todayOrders.reduce((sum, item) => sum + (['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'COMPLETED'].indexOf(item.status) >= 0 ? item.payableAmount : 0), 0);
    const pending = raw.filter((item) => ['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'REFUND_FAILED', 'REFUND_REVIEW'].indexOf(item.status) > -1).length;
    const orders = raw.map((order) => this.decorateOrder(order));
    const tabs = this.data.tabs.map((tab) => Object.assign({}, tab, { count: tab.id === 'ALL' ? 0 : orders.filter((order) => order.status === tab.id).length }));
    this.setData({ stats: { salesText: format.yuan(sales), pending }, tabs, allOrders: raw, filteredOrders: this.filterOrders(orders, this.data.activeTab) });
  },

  decorateOrder(order) { const fulfillmentType = order.fulfillmentType || commerce.FULFILLMENT.TAKEAWAY; return Object.assign({}, order, { fulfillmentType, statusText: format.orderStatus(order.status, fulfillmentType), fulfillmentText: fulfillmentType === commerce.FULFILLMENT.COURIER ? '冷吃快递' : '校园外卖', createdText: format.dateTime(order.createdAt), payableText: format.yuan(order.payableAmount), itemCount: (order.itemsSnapshot || []).reduce((sum, item) => sum + item.quantity, 0), itemsText: (order.itemsSnapshot || []).map((item) => `${item.name} ×${item.quantity}`).join('、') }); },
  filterOrders(orders, tab) { return tab === 'ALL' ? orders : orders.filter((order) => order.status === tab); },
  selectTab(event) { const activeTab = event.currentTarget.dataset.id; const orders = this.data.allOrders.map((order) => this.decorateOrder(order)); this.setData({ activeTab, filteredOrders: this.filterOrders(orders, activeTab) }); },

  updateStatus(id, status, delivery) {
    if (api.isProduction()) {
      api.changeOrderStatus(id, status, delivery).then(() => { this.loadOrders(); wx.showToast({ title: '订单已更新', icon: 'success' }); }).catch((error) => { console.error('change order status failed', error); wx.showToast({ title: '订单状态更新失败', icon: 'none' }); });
      return;
    }
    const orders = storage.getOrders(); const order = orders.find((item) => item.id === id); if (!order) return; order.status = status; order.updatedAt = new Date().toISOString(); if (status === 'PREPARING') order.preparingAt = order.updatedAt; if (status === 'DELIVERING') { order.deliveringAt = order.updatedAt; order.delivery = Object.assign({}, order.delivery, { status: 'CALLED', runnerName: delivery && delivery.runnerName ? delivery.runnerName : '王师傅', runnerPhone: delivery && delivery.runnerPhone ? delivery.runnerPhone : '13900001234', actualFee: order.deliveryFee }); } if (status === 'SHIPPED') { order.shippedAt = order.updatedAt; order.shipping = Object.assign({}, order.shipping, { status: 'SHIPPED', carrier: delivery && delivery.carrier || '中通快递', trackingNo: delivery && delivery.trackingNo || 'SFDEMO123456789', shippingRemark: delivery && delivery.shippingRemark || '' }); } if (status === 'COMPLETED') order.completedAt = order.updatedAt; storage.saveOrders(orders); this.loadOrders(); wx.showToast({ title: '订单已更新', icon: 'success' });
  },
  onRunnerInput(event) { const id = event.currentTarget.dataset.id; this.deliveryDrafts[id] = Object.assign({}, this.deliveryDrafts[id] || {}, { [event.currentTarget.dataset.field]: event.detail.value }); },
  startPreparing(event) { this.updateStatus(event.currentTarget.dataset.id, 'PREPARING'); },
  startDelivery(event) { const id = event.currentTarget.dataset.id; this.updateStatus(id, 'DELIVERING', this.deliveryDrafts[id] || {}); },
  shipOrder(event) { const id = event.currentTarget.dataset.id; const draft = this.deliveryDrafts[id] || {}; if (!draft.carrier || !draft.trackingNo) { wx.showToast({ title: '请填写快递公司和单号', icon: 'none' }); return; } this.updateStatus(id, 'SHIPPED', draft); },
  onShippingInput(event) { const id = event.currentTarget.dataset.id; this.deliveryDrafts[id] = Object.assign({}, this.deliveryDrafts[id] || {}, { [event.currentTarget.dataset.field]: event.detail.value }); },
  completeOrder(event) { this.updateStatus(event.currentTarget.dataset.id, 'COMPLETED'); },
  refundOrder(event) {
    const id = event.currentTarget.dataset.id;
    wx.showModal({ title: '发起退款', content: '将按原支付路径发起全额退款，确定继续吗？', success: (res) => {
      if (!res.confirm) return;
      if (api.isProduction()) {
        api.createRefund(id).then(() => { this.loadOrders(); wx.showToast({ title: '退款申请已提交', icon: 'success' }); }).catch((error) => { console.error('create refund failed', error); wx.showToast({ title: '退款申请失败', icon: 'none' }); });
        return;
      }
      this.updateStatus(id, 'REFUNDED');
    } });
  },
  newRefundAttempt(event) {
    const order = this.data.allOrders.find(item => item.id === event.currentTarget.dataset.id);
    if (!order || order.status !== 'REFUND_FAILED') return;
    wx.showModal({ title: '重新发起退款', content: '微信已确认原退款单关闭。将保留旧记录并使用新的退款单号再次全额退款，是否继续？', success: result => {
      if (!result.confirm) return;
      api.createRefund(order.id, { newAttempt: true, previousRefundNo: order.refundReference }).then(() => { this.loadOrders(); wx.showToast({ title: '新退款申请已提交', icon: 'none' }); }).catch(() => { this.loadOrders(); wx.showToast({ title: '退款状态已变化，请刷新核实', icon: 'none' }); });
    } });
  },
  queryRefund(event) {
    api.queryRefund(event.currentTarget.dataset.id).then(() => { this.loadOrders(); wx.showToast({ title: '退款结果已更新', icon: 'none' }); }).catch(() => { this.loadOrders(); wx.showToast({ title: '退款尚未确认，可稍后重查', icon: 'none' }); });
  },
  callCustomer(event) { const phoneNumber = event.currentTarget.dataset.phone; if (phoneNumber) wx.makePhoneCall({ phoneNumber }); },
  requestOrderNotice() {
    const templateId = notifications.orderNoticeTemplateId;
    if (!templateId || typeof wx.requestSubscribeMessage !== 'function') { wx.showToast({ title: '请先配置订单通知模板', icon: 'none' }); return; }
    wx.requestSubscribeMessage({ tmplIds: [templateId], success: result => { const accepted = result && result[templateId] === 'accept'; wx.showToast({ title: accepted ? '新订单提醒已开启' : '未开启订单提醒', icon: accepted ? 'success' : 'none' }); }, fail: error => { console.error('request order notice failed', error); wx.showToast({ title: '订单提醒开启失败', icon: 'none' }); } });
  },
  goProducts() { wx.navigateTo({ url: '/pages/merchant/products/index' }); },
  goSettings() { wx.navigateTo({ url: '/pages/merchant/settings/index' }); },
  goCampaign() { wx.navigateTo({ url: '/pages/merchant/campaign/index' }); },
  goCoupons() { wx.navigateTo({ url: '/pages/merchant/coupons/index' }); },
  goStats() { wx.navigateTo({ url: '/pages/merchant/stats/index' }); }
});
