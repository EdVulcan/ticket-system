const app = getApp();

Page({
  data: {
    storeName: '官方商城',
    allProducts: [],
    products: [],
    featuredProduct: null,
    resultCount: 0,
    scenicOptions: [],
    activeScenic: '全部',
    kindOptions: [],
    activeKind: 'all',
    keyword: '',
    sort: 'default',
    hasActiveFilters: false,
    emptyStateDetail: '换个关键词或分类试试',
    loading: true,
    error: ''
  },

  onLoad() {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
    this.loadCatalog();
  },

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
        validityText: this.formatValidity(product),
        kindLabel: product.product_kind === 'scenic_hotel_package' ? '酒景套餐' : '景区门票',
        packageSummary: product.product_kind === 'scenic_hotel_package'
          ? `${product.hotel_name} · ${product.room_type_name} · ${product.nights}晚`
          : ''
      }));
      const scenicOptions = ['全部'];
      products.forEach(product => {
        const scenic = product.scenic_area_name || '其他景区';
        if (scenicOptions.indexOf(scenic) < 0) scenicOptions.push(scenic);
      });
      const kinds = [];
      if (products.some(product => product.product_kind !== 'scenic_hotel_package')) kinds.push({ value: 'ticket', label: '景区门票' });
      if (products.some(product => product.product_kind === 'scenic_hotel_package')) kinds.push({ value: 'scenic_hotel_package', label: '酒景套餐' });
      const kindOptions = kinds.length > 1 ? [{ value: 'all', label: '全部' }, ...kinds] : kinds;
      this.setData({
        storeName: catalog.store_name || '官方商城',
        allProducts: products,
        featuredProduct: products.length > 1 ? products[0] : null,
        resultCount: products.length,
        scenicOptions,
        kindOptions,
        activeKind: kindOptions.length ? kindOptions[0].value : 'all',
        hasActiveFilters: false,
        emptyStateDetail: '换个关键词或分类试试',
        loading: false
      }, () => {
        app.setStoreName(catalog.store_name || '官方商城');
        this.applyFilters();
      });
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

  resetFilters() {
    const defaultKind = this.data.kindOptions.length ? this.data.kindOptions[0].value : 'all';
    this.setData({ keyword: '', activeScenic: '全部', activeKind: defaultKind, sort: 'default' }, () => this.applyFilters());
  },

  selectScenic(event) {
    this.setData({ activeScenic: event.currentTarget.dataset.scenic }, () => this.applyFilters());
  },

  selectKind(event) {
    this.setData({ activeKind: event.currentTarget.dataset.kind }, () => this.applyFilters());
  },

  selectSort(event) {
    this.setData({ sort: event.currentTarget.dataset.sort }, () => this.applyFilters());
  },

  applyFilters() {
    const keyword = this.data.keyword.trim().toLowerCase();
    let products = this.data.allProducts.filter(product => {
      const scenic = product.scenic_area_name || '其他景区';
      const scenicMatched = this.data.activeScenic === '全部' || scenic === this.data.activeScenic;
      const kindMatched = this.data.activeKind === 'all' || product.product_kind === this.data.activeKind;
      const text = `${product.name || ''} ${scenic} ${product.hotel_name || ''} ${product.room_type_name || ''} ${product.priceText || ''} ${(product.tags || []).join(' ')}`.toLowerCase();
      return scenicMatched && kindMatched && (!keyword || text.indexOf(keyword) >= 0);
    });
    if (this.data.sort === 'priceAsc') products.sort((a, b) => a.price_cents - b.price_cents);
    if (this.data.sort === 'priceDesc') products.sort((a, b) => b.price_cents - a.price_cents);
    const hasActiveFilters = Boolean(keyword || this.data.activeScenic !== '全部' || (this.data.kindOptions.length > 1 && this.data.activeKind !== 'all') || this.data.sort !== 'default');
    let emptyStateDetail = '换个关键词或分类试试';
    if (keyword && this.data.activeScenic !== '全部') emptyStateDetail = '换个关键词或景区试试';
    else if (keyword) emptyStateDetail = '换个关键词试试';
    else if (this.data.activeScenic !== '全部' || (this.data.kindOptions.length > 1 && this.data.activeKind !== 'all')) emptyStateDetail = '换个分类或景区试试';
    const visibleProducts = !hasActiveFilters && this.data.featuredProduct
      ? products.filter(product => product.id !== this.data.featuredProduct.id)
      : products;
    this.setData({ products: visibleProducts, resultCount: products.length, hasActiveFilters, emptyStateDetail });
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
