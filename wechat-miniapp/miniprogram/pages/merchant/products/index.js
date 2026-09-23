const storage = require('../../../services/storage');
const format = require('../../../utils/format');
const api = require('../../../services/api');

function decorate(products) {
  return products.map((item) => { const normalized = api.normalizeProduct(item); return Object.assign({}, normalized, { id: normalized.id || normalized._id, priceText: format.yuan(normalized.price || normalized.basePrice) }); });
}

Page({
  data: { products: [], unavailable: false },
  onShow() { this.loadProducts(); },

  loadProducts() {
    if (api.isProduction()) {
      api.getMerchantProducts().then((result) => {
        const products = result.data || [];
        storage.saveProducts(products);
        this.setData({ products: decorate(products) });
      }).catch((error) => { console.error('load products failed', error); this.setData({ products: [], unavailable: true }); wx.showToast({ title: String(error && (error.code || error.message) || '').indexOf('MERCHANT_API_UNAVAILABLE') >= 0 ? '商品管理暂未开放' : '商品加载失败，生产环境已禁用写操作', icon: 'none' }); });
      return;
    }
    this.setData({ products: decorate(storage.getProducts()) });
  },

  updateProduct(id, change) {
    if (api.isProduction()) {
      if (this.data.unavailable) { wx.showToast({ title: '商品管理暂未开放', icon: 'none' }); return; }
      api.updateProduct(id, change).then(() => { this.loadProducts(); wx.showToast({ title: '商品已更新', icon: 'success' }); }).catch((error) => { console.error('update product failed', error); wx.showToast({ title: '商品更新失败', icon: 'none' }); });
      return;
    }
    const products = storage.getProducts();
    const product = products.find((item) => item.id === id);
    if (!product) return;
    Object.assign(product, change);
    storage.saveProducts(products);
    this.loadProducts();
  },

  toggleSale(event) {
    const product = this.data.products.find((item) => item.id === event.currentTarget.dataset.id);
    if (product) this.updateProduct(product.id, { isOnSale: !product.isOnSale });
  },

  editProduct(event) { if (this.data.unavailable) { wx.showToast({ title: '商品管理暂未开放', icon: 'none' }); return; } wx.navigateTo({ url: `/pages/merchant/product-edit/index?id=${event.currentTarget.dataset.id}` }); },
  createProduct() { if (this.data.unavailable) { wx.showToast({ title: '商品管理暂未开放', icon: 'none' }); return; } wx.navigateTo({ url: '/pages/merchant/product-edit/index' }); },

  changeStock(event) {
    const product = this.data.products.find((item) => item.id === event.currentTarget.dataset.id);
    if (!product || product.stockMode === 'UNLIMITED') return;
    const stock = Math.max(0, Number(product.stock || 0) + Number(event.currentTarget.dataset.delta));
    this.updateProduct(product.id, api.isProduction() ? { stockDelta: Number(event.currentTarget.dataset.delta) } : { stock, isSoldOut: stock === 0 });
  },

  toggleSoldOut(event) {
    const product = this.data.products.find((item) => item.id === event.currentTarget.dataset.id);
    if (!product) return;
    if (product.stock > 0) this.updateProduct(product.id, { isSoldOut: !product.isSoldOut });
    else this.updateProduct(product.id, api.isProduction() ? { stockDelta: 20, isSoldOut: false, isOnSale: true } : { stock: 20, isSoldOut: false, isOnSale: true });
  }
});
