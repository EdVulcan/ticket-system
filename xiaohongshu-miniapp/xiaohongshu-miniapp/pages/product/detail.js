const app = getApp();
const promotion = require('../../utils/promotion');

Page({
  data: {
    product: null, storeName: '', loading: true, navigating: false, error: '',
    opportunity: null, opportunityVisible: false, opportunityAmountText: '', opportunityCountdown: '', opportunityReservedOrderNo: ''
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '商品详情');
    this.mappingId = Number(options.mapping_id || 0);
    this.loadProduct();
    this.loadOpportunity();
  },

  onShow() {
    if (this.mappingId) this.loadOpportunity();
  },

  onUnload() {
    this.opportunityVersion = (this.opportunityVersion || 0) + 1;
    this.productVersion = (this.productVersion || 0) + 1;
    this.priceVersion = (this.priceVersion || 0) + 1;
    if (this.navigateTimer) clearTimeout(this.navigateTimer);
    this.stopOpportunityTimer();
  },

  loadProduct() {
    const version = (this.productVersion || 0) + 1;
    this.productVersion = version;
    this.setData({ loading: true, error: '' });
    return app.request('/catalog').then(catalog => {
      if (version !== this.productVersion) return;
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
      Object.assign(product, promotion.productPrice(product, this.opportunity, null));
      this.setData({ product, storeName: catalog.store_name || '', loading: false });
      app.setStoreName(catalog.store_name || '');
      return this.refreshPromotionPrices();
    }).catch(error => {
      if (version === this.productVersion) this.setData({ loading: false, error: error.message || '票种加载失败' });
    });
  },

  loadOpportunity() {
    const version = (this.opportunityVersion || 0) + 1;
    this.opportunityVersion = version;
    return app.request('/promotion', { method: 'POST', data: {} }).then(raw => {
      if (version !== this.opportunityVersion) return;
      return this.applyOpportunity(promotion.normalize(raw, Date.now()));
    }).catch(() => {
      if (version === this.opportunityVersion) this.applyOpportunity(null);
    });
  },

  applyOpportunity(opportunity) {
    this.opportunity = opportunity;
    const available = promotion.appliesTo(opportunity, this.mappingId);
    const reserved = opportunity && opportunity.status === 'reserved' && opportunity.reservedOrderNo;
    this.setData({
      opportunity,
      opportunityVisible: Boolean(available || reserved),
      opportunityAmountText: available ? promotion.money(opportunity.discountCents) : '',
      opportunityCountdown: promotion.countdown(opportunity),
      opportunityReservedOrderNo: reserved ? opportunity.reservedOrderNo : ''
    });
    this.startOpportunityTimer();
    return this.refreshPromotionPrices();
  },

  refreshPromotionPrices() {
    const version = (this.priceVersion || 0) + 1;
    this.priceVersion = version;
    const product = this.data.product;
    if (!product) return Promise.resolve();
    this.setData({ product: { ...product, ...promotion.productPrice(product, this.opportunity, null) } });
    return promotion.loadProductPrices((...args) => app.request(...args), [product], this.opportunity).then(prices => {
      if (version !== this.priceVersion) return;
      this.setData({ product: { ...product, ...promotion.productPrice(product, this.opportunity, prices[product.id]) } });
    });
  },

  startOpportunityTimer() {
    this.stopOpportunityTimer();
    if (!this.opportunity || !promotion.appliesTo(this.opportunity, this.mappingId) || typeof setInterval !== 'function') return;
    this.opportunityTimer = setInterval(() => {
      if (!promotion.appliesTo(this.opportunity, this.mappingId)) return this.applyOpportunity(this.opportunity);
      this.setData({ opportunityCountdown: promotion.countdown(this.opportunity) });
    }, 1000);
  },

  stopOpportunityTimer() {
    if (this.opportunityTimer && typeof clearInterval === 'function') clearInterval(this.opportunityTimer);
    this.opportunityTimer = null;
  },

  buy() {
    if (!this.data.product || this.data.navigating) return;
    this.setData({ navigating: true });
    this.navigateTimer = setTimeout(() => this.setData({ navigating: false }), 1200);
    xhs.navigateTo({
      url: `/pages/order/confirm?mapping_id=${this.data.product.id}`,
      fail: () => this.setData({ navigating: false })
    });
  },

  openPromotionOrder() {
    if (this.data.opportunityReservedOrderNo) xhs.navigateTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(this.data.opportunityReservedOrderNo)}` });
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
  }
});
