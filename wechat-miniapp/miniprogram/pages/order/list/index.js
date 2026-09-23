const storage = require('../../../services/storage');
const format = require('../../../utils/format');
const api = require('../../../services/api');
const commerce = require('../../../config/commerce');

function decorate(order) {
  order = typeof api.normalizeOrder === 'function' ? api.normalizeOrder(order) : order;
  return Object.assign({}, order, {
    id: order.id || order._id,
    statusText: format.orderStatus(order.status, order.fulfillmentType),
    fulfillmentText: order.fulfillmentType === commerce.FULFILLMENT.COURIER ? '零售快递' : '餐饮订单',
    createdText: format.dateTime(order.createdAt),
    payableText: format.yuan(order.payableAmount),
    itemCount: (order.itemsSnapshot || []).reduce((sum, item) => sum + item.quantity, 0),
    itemsSnapshot: (order.itemsSnapshot || []).map((item, index) => Object.assign({}, item, { lineId: item.lineId || `${item.productId}_${index}`, fulfillmentType: order.fulfillmentType, subtotalText: format.yuan(item.subtotal) }))
  });
}

Page({
  data: {
    tabs: [
      { id: 'ALL', name: '全部' },
      { id: 'WAIT_PAY', name: '待付款' },
      { id: 'PROCESSING', name: '制作中' },
      { id: 'WAIT_SHIP', name: '待发货' },
      { id: 'SHIPPED', name: '待收货' },
      { id: 'DELIVERING', name: '配送中' },
      { id: 'COMPLETED', name: '已完成' }
    ],
    activeTab: 'ALL',
    allOrders: [],
    filteredOrders: []
  },

  onShow() {
    const requestedTab = storage.consumeOrderListFilter();
    const activeTab = this.data.tabs.some((tab) => tab.id === requestedTab) ? requestedTab : this.data.activeTab;
    if (activeTab !== this.data.activeTab) this.setData({ activeTab });
    this.loadOrders();
  },

  loadOrders() {
    if (api.isProduction()) {
      api.getOrders().then((result) => this.renderOrders(result.data || [])).catch((error) => { console.error('load orders failed', error); wx.showToast({ title: '订单加载失败', icon: 'none' }); });
      return;
    }
    this.renderOrders(storage.getOrders());
  },

  renderOrders(rawOrders) {
    const orders = rawOrders.map(decorate);
    const tabs = this.data.tabs.map((tab) => Object.assign({}, tab, { count: tab.id === 'ALL' ? 0 : orders.filter((order) => this.matchesTab(order, tab.id)).length }));
    this.setData({ tabs, allOrders: orders, filteredOrders: this.filterOrders(orders, this.data.activeTab) });
  },

  filterOrders(orders, tab) {
    if (tab === 'ALL') return orders;
    return orders.filter((order) => this.matchesTab(order, tab));
  },

  matchesTab(order, tab) {
    if (tab === 'PROCESSING') return order.fulfillmentType !== commerce.FULFILLMENT.COURIER && ['PAID', 'PREPARING'].indexOf(order.status) > -1;
    if (tab === 'WAIT_SHIP') return order.fulfillmentType === commerce.FULFILLMENT.COURIER && order.status === 'PAID';
    if (tab === 'SHIPPED') return order.fulfillmentType === commerce.FULFILLMENT.COURIER && order.status === 'SHIPPED';
    return order.status === tab;
  },

  selectTab(event) {
    const activeTab = event.currentTarget.dataset.id;
    this.setData({ activeTab, filteredOrders: this.filterOrders(this.data.allOrders, activeTab) });
  },

  openOrder(event) { wx.navigateTo({ url: `/pages/order/detail/index?id=${event.currentTarget.dataset.id}` }); },
  goHome() { wx.switchTab({ url: '/pages/index/index' }); }
});
