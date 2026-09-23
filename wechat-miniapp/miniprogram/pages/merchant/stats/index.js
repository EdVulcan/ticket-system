const storage = require('../../../services/storage');
const api = require('../../../services/api');
const format = require('../../../utils/format');

const PAID_STATUSES = ['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'COMPLETED', 'REFUNDING', 'REFUND_FAILED', 'REFUND_REVIEW'];

function dayKey(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return new Date(date.getTime() + 8 * 60 * 60 * 1000).toISOString().slice(0, 10);
}

function lastSevenDays() {
  const shifted = new Date(Date.now() + 8 * 60 * 60 * 1000);
  const base = Date.UTC(shifted.getUTCFullYear(), shifted.getUTCMonth(), shifted.getUTCDate());
  return Array.from({ length: 7 }, (_, index) => new Date(base - (6 - index) * 24 * 60 * 60 * 1000).toISOString().slice(0, 10));
}

function fromOrders(orders, assistCount) {
  const validOrders = orders || [];
  const salesAmount = validOrders.filter(order => PAID_STATUSES.includes(order.status)).reduce((sum, order) => sum + Number(order.payableAmount || 0), 0);
  const daily = lastSevenDays().map(date => {
    const sameDay = validOrders.filter(order => dayKey(order.createdAt) === date);
    return { date, orderCount: sameDay.length, salesAmount: sameDay.filter(order => PAID_STATUSES.includes(order.status)).reduce((sum, order) => sum + Number(order.payableAmount || 0), 0) };
  });
  return { orderCount: validOrders.length, salesAmount, takeawayOrders: validOrders.filter(order => (order.fulfillmentType || 'TAKEAWAY') === 'TAKEAWAY').length, courierOrders: validOrders.filter(order => order.fulfillmentType === 'COURIER').length, assistCount, couponCount: assistCount * 2, daily };
}

function decorate(stats) {
  const daily = (stats.daily || []).map(item => Object.assign({}, item, { dateText: item.date ? item.date.slice(5) : '', salesText: format.yuan(item.salesAmount) }));
  const maxSales = Math.max(1, ...daily.map(item => Number(item.salesAmount || 0)));
  return Object.assign({}, stats, { salesText: format.yuan(stats.salesAmount), daily: daily.map(item => Object.assign({}, item, { barWidth: item.salesAmount ? Math.max(6, Math.round(Number(item.salesAmount) / maxSales * 100)) : 0 })) });
}

Page({
  data: { stats: decorate(fromOrders([], 0)), production: false, unavailable: false, loading: false },

  onShow() { this.loadStats(); },

  loadStats() {
    this.setData({ production: api.isProduction(), loading: true });
    if (api.isProduction()) {
      api.getMerchantStats().then(result => this.setData({ stats: decorate(result.data || fromOrders([], 0)), loading: false })).catch(error => {
        console.error('load merchant stats failed', error);
        this.setData({ loading: false, unavailable: String(error && (error.code || error.message) || '').indexOf('MERCHANT_API_UNAVAILABLE') >= 0, stats: {} });
        wx.showToast({ title: this.data.unavailable ? '数据统计暂未开放' : '统计数据加载失败', icon: 'none' });
      });
      return;
    }
    const assist = storage.getAssist();
    const assistCount = assist && assist.status === 'SUCCESS' ? 1 : 0;
    this.setData({ stats: decorate(fromOrders(storage.getOrders(), assistCount)), loading: false });
  },

  refresh() { this.loadStats(); }
});
