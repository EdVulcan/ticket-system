const app = getApp();

Page({
  data: { product: null, storeName: '官方商城', loading: true, navigating: false, error: '' },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
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
      product.kindLabel = product.isPackage ? '酒景套餐' : '景区门票';
      product.stayText = product.isPackage ? `${product.nights}晚 · 每份${product.rooms_per_package}间房` : '';
      this.setData({ product, storeName: catalog.store_name || '官方商城', loading: false });
      app.setStoreName(catalog.store_name || '官方商城');
    }).catch(error => this.setData({ loading: false, error: error.message || '票种加载失败' }));
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
