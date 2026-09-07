const app = getApp();
const calendar = require('../../utils/calendar');

Page({
  data: {
    product: null, storeName: '', loading: true, navigating: false, error: '', purchaseOpen: false,
    quantity: 1, maxQuantity: 100, useDate: '', minDate: '', maxDate: '', dateChips: [],
    calendarOpen: false, calendarTitle: '', calendarCells: [], canPreviousMonth: false, canNextMonth: false
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '商品详情');
    this.mappingId = Number(options.mapping_id || 0);
    this.loadProduct();
  },

  onUnload() {
    if (this.navigateTimer) clearTimeout(this.navigateTimer);
  },

  loadProduct() {
    this.setData({ loading: true, error: '' });
    app.request('/catalog').then(catalog => {
      const product = (catalog.products || []).find(item => Number(item.id) === this.mappingId);
      if (!product) throw new Error('票种当前不可购买');
      product.priceText = this.formatPrice(product.price_cents);
      product.validityText = this.formatValidity(product);
      product.isPackage = product.product_kind === 'scenic_hotel_package';
      product.isDeferredPackage = product.isPackage && product.booking_mode === 'after_purchase';
      product.requiresUseDate = Boolean(product.requires_use_date) && !product.isDeferredPackage;
      product.kindLabel = product.isPackage ? '酒景套餐' : (product.product_kind === 'ticket' ? '景区门票' : '商品');
      product.useDateLabel = product.isPackage ? '入住日期' : '游玩日期';
      product.stayText = product.isPackage ? `${product.nights}晚 · 每份${product.rooms_per_package}间房` : '';
      const maxQuantity = this.deriveMaxQuantity(catalog.max_order_cents, product.price_cents);
      const today = new Date();
      const minDate = calendar.formatDate(calendar.addDays(today, product.isPackage ? Math.max(0, Number(product.min_advance_days || 0)) : 0));
      const maxDate = calendar.formatDate(calendar.addDays(today, 365));
      this.calendarMonth = calendar.monthStart(minDate);
      this.setData({ product, storeName: catalog.store_name || '', maxQuantity, minDate, maxDate, loading: false });
      app.setStoreName(catalog.store_name || '');
      this.refreshCalendar();
    }).catch(error => this.setData({ loading: false, error: error.message || '票种加载失败' }));
  },

  buy() {
    if (!this.data.product || this.data.navigating) return;
    this.setData({ purchaseOpen: true, error: '' });
    this.refreshCalendar();
  },

  closePurchase() { this.setData({ purchaseOpen: false, error: '' }); },

  decrease() {
    if (this.data.quantity > 1) this.setData({ quantity: this.data.quantity - 1 });
  },

  increase() {
    if (this.data.quantity < this.data.maxQuantity) this.setData({ quantity: this.data.quantity + 1 });
  },

  toggleCalendar() { this.setData({ calendarOpen: !this.data.calendarOpen }); },

  selectDate(event) {
    const useDate = event.currentTarget.dataset.date;
    if (!calendar.isDateWithin(useDate, this.data.minDate, this.data.maxDate)) return;
    this.setData({ useDate, error: '' });
    this.refreshCalendar();
  },

  previousMonth() {
    if (!calendar.canMoveMonth(this.calendarMonth, -1, this.data.minDate, this.data.maxDate)) return;
    this.calendarMonth = new Date(this.calendarMonth.getFullYear(), this.calendarMonth.getMonth() - 1, 1);
    this.refreshCalendar();
  },

  nextMonth() {
    if (!calendar.canMoveMonth(this.calendarMonth, 1, this.data.minDate, this.data.maxDate)) return;
    this.calendarMonth = new Date(this.calendarMonth.getFullYear(), this.calendarMonth.getMonth() + 1, 1);
    this.refreshCalendar();
  },

  continuePurchase() {
    const product = this.data.product;
    if (!product || this.data.navigating) return;
    if (product.requiresUseDate && !calendar.isDateWithin(this.data.useDate, this.data.minDate, this.data.maxDate)) {
      this.setData({ error: `请选择${product.isPackage ? '入住' : '游玩'}日期` });
      return;
    }
    this.setData({ navigating: true });
    this.navigateTimer = setTimeout(() => this.setData({ navigating: false }), 1200);
    const useDate = product.requiresUseDate ? `&use_date=${encodeURIComponent(this.data.useDate)}` : '';
    xhs.navigateTo({
      url: `/pages/order/confirm?mapping_id=${product.id}&quantity=${this.data.quantity}${useDate}`,
      fail: () => this.setData({ navigating: false })
    });
  },

  retry() { this.loadProduct(); },

  formatPrice(cents) {
    const amount = Number(cents || 0) / 100;
    return amount % 1 === 0 ? amount.toFixed(0) : amount.toFixed(2);
  },

  formatValidity(product) {
    if (product.validity_type === 'days' && product.validity_days > 0) return `购买后${product.validity_days}天内有效`;
    if (product.validity_type === 'unlimited') return '有效期内可用';
    return '按选定日期使用';
  },

  deriveMaxQuantity(maxOrderCents, priceCents) {
    const limit = Number(maxOrderCents || 0);
    const price = Number(priceCents || 0);
    return limit > 0 && price > 0 ? Math.min(100, Math.max(1, Math.floor(limit / price))) : 100;
  },

  refreshCalendar() {
    const today = new Date();
    const month = this.calendarMonth || calendar.monthStart(today);
    this.setData({
      dateChips: calendar.buildDateChips(today, this.data.minDate, this.data.maxDate, this.data.useDate),
      calendarTitle: calendar.monthTitle(month),
      calendarCells: calendar.buildCalendarCells(month, this.data.minDate, this.data.maxDate, this.data.useDate),
      canPreviousMonth: calendar.canMoveMonth(month, -1, this.data.minDate, this.data.maxDate),
      canNextMonth: calendar.canMoveMonth(month, 1, this.data.minDate, this.data.maxDate)
    });
  }
});
