const brand = require('../../../config/brand');
const mock = require('../../../data/mock');
const storage = require('../../../services/storage');
const api = require('../../../services/api');

Page({
  data: {
    brand,
    profile: { nickname: '朋友' },
    store: mock.store,
    coupons: [],
    couponCount: 0,
    showCoupons: false,
    contact: { available: false, contactType: '', contactName: '', wechatId: '', qrCodeUrl: '' },
    showContact: false,
    orderStats: { waitPay: 0, processing: 0, delivering: 0, completed: 0 }
  },

  onShow() { this.loadProfile(); },

  loadProfile() {
    const app = getApp();
    if (api.isProduction()) {
      api.getStorefrontContact().then((contact) => {
        this.setData({ contact: contact || { available: false }, showContact: contact && contact.available ? this.data.showContact : false });
      }).catch((error) => {
        console.error('load storefront contact failed', error);
        this.setData({ contact: { available: false }, showContact: false });
      });
      Promise.all([api.getCoupons(), api.getOrders()]).then(([couponResult, orderResult]) => {
        const coupons = couponResult.data || [];
        const orders = orderResult.data || [];
        storage.saveCoupons(coupons);
        this.renderProfile(app, coupons, orders);
      }).catch((error) => { console.error('load profile data failed', error); this.renderProfile(app, [], []); });
      return;
    }
    const coupons = storage.getCoupons().filter((coupon) => coupon.status === 'AVAILABLE');
    const orders = storage.getOrders();
    this.renderProfile(app, coupons, orders);
  },

  renderProfile(app, allCoupons, orders) {
    const coupons = allCoupons.filter((coupon) => coupon.status === 'AVAILABLE');
    this.setData({ profile: app.globalData.user, store: storage.getStore(), coupons, couponCount: coupons.length, orderStats: { waitPay: orders.filter((item) => item.status === 'WAIT_PAY').length, processing: orders.filter((item) => ['PAID', 'PREPARING'].indexOf(item.status) > -1).length, delivering: orders.filter((item) => item.status === 'DELIVERING').length, completed: orders.filter((item) => item.status === 'COMPLETED').length } });
  },

  toggleCoupons() { this.setData({ showCoupons: !this.data.showCoupons }); },
  goOrders(event) {
    storage.saveOrderListFilter(event && event.currentTarget && event.currentTarget.dataset.tab || 'ALL');
    wx.switchTab({ url: '/pages/order/list/index' });
  },
  goAddress() { wx.navigateTo({ url: '/pages/address/list/index' }); },
  goAssist() { wx.navigateTo({ url: '/pages/assist/index/index' }); },
  openContact() {
    if (this.data.contact && this.data.contact.available) this.setData({ showContact: true });
  },
  closeContact() { this.setData({ showContact: false }); },
  keepContactOpen() {},
  previewContactQRCode() {
    const url = this.data.contact && this.data.contact.qrCodeUrl;
    if (url) wx.previewImage({ current: url, urls: [url] });
  },
  copyWechatID() {
    const wechatId = this.data.contact && this.data.contact.wechatId;
    if (!wechatId) return;
    wx.setClipboardData({ data: wechatId, success: () => wx.showToast({ title: '微信号已复制', icon: 'success' }) });
  }
});
