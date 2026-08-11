const app = getApp();

Page({
  data: {
    storeName: '景区购票',
    allProducts: [],
    products: [],
    featuredProduct: null,
    scenicOptions: [],
    activeScenic: '全部',
    keyword: '',
    sort: 'default',
    loading: true,
    error: ''
  },

  onLoad() { this.loadCatalog(); },

  onShow() {
    if (this.data.allProducts.length) this.applyFilters();
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
      const scenicOptions = ['全部'];
      products.forEach(product => {
        const scenic = product.scenic_area_name || '其他景区';
        if (scenicOptions.indexOf(scenic) < 0) scenicOptions.push(scenic);
      });
      this.setData({
        storeName: catalog.store_name || '景区购票',
        allProducts: products,
        featuredProduct: products.length ? products[0] : null,
        scenicOptions,
        loading: false
      }, () => this.applyFilters());
    }).catch(error => {
      this.setData({ loading: false, error: error.message || '票种加载失败，请稍后重试' });
    });
  },

  onKeywordInput(event) {
    this.setData({ keyword: event.detail.value || '' }, () => this.applyFilters());
  },

  clearKeyword() {
    this.setData({ keyword: '' }, () => this.applyFilters());
  },

  selectScenic(event) {
    this.setData({ activeScenic: event.currentTarget.dataset.scenic }, () => this.applyFilters());
  },

  selectSort(event) {
    this.setData({ sort: event.currentTarget.dataset.sort }, () => this.applyFilters());
  },

  applyFilters() {
    const keyword = this.data.keyword.trim().toLowerCase();
    let products = this.data.allProducts.filter(product => {
      const scenic = product.scenic_area_name || '其他景区';
      const scenicMatched = this.data.activeScenic === '全部' || scenic === this.data.activeScenic;
      const text = `${product.name || ''} ${scenic} ${product.priceText || ''} ${(product.tags || []).join(' ')}`.toLowerCase();
      return scenicMatched && (!keyword || text.indexOf(keyword) >= 0);
    });
    if (this.data.sort === 'priceAsc') products.sort((a, b) => a.price_cents - b.price_cents);
    if (this.data.sort === 'priceDesc') products.sort((a, b) => b.price_cents - a.price_cents);
    this.setData({ products });
  },

  openProduct(event) {
    const mappingId = event.currentTarget.dataset.id;
    if (mappingId) xhs.navigateTo({ url: `/pages/product/detail?mapping_id=${mappingId}` });
  },

  goHome() {},

  goOrders() { xhs.navigateTo({ url: '/pages/orders/index' }); },

  retry() { this.loadCatalog(); },

  formatPrice(cents) {
    const amount = Number(cents || 0) / 100;
    return amount % 1 === 0 ? amount.toFixed(0) : amount.toFixed(2);
  },

  formatValidity(product) {
    if (product.validity_type === 'days' && product.validity_days > 0) {
      return `购买后${product.validity_days}天内有效`;
    }
    if (product.validity_type === 'unlimited') return '有效期内可用';
    return '按选定日期使用';
  }
});
