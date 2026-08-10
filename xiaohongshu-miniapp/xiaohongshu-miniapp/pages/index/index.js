const app = getApp();

Page({
  data: {
    storeName: '景区购票',
    products: [],
    loading: true,
    error: ''
  },

  onLoad() {
    this.loadCatalog();
  },

  onPullDownRefresh() {
    this.loadCatalog().finally(() => xhs.stopPullDownRefresh());
  },

  loadCatalog() {
    this.setData({ loading: true, error: '' });
    return app.request('/catalog').then(catalog => {
      const products = (catalog.products || []).map(product => ({
        ...product,
        priceText: this.formatPrice(product.price_cents),
        validityText: this.formatValidity(product)
      }));
      this.setData({
        storeName: catalog.store_name || '景区购票',
        products,
        loading: false
      });
    }).catch(error => {
      this.setData({ loading: false, error: error.message || '票种加载失败，请稍后重试' });
    });
  },

  retry() {
    this.loadCatalog();
  },

  formatPrice(cents) {
    const amount = Number(cents || 0) / 100;
    return amount % 1 === 0 ? amount.toFixed(0) : amount.toFixed(2);
  },

  formatValidity(product) {
    if (product.validity_type === 'days' && product.validity_days > 0) {
      return `购买后${product.validity_days}天内有效`;
    }
    return '按选定日期使用';
  }
});
