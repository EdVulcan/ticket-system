const storage = require('../../services/storage');
const format = require('../../utils/format');
const api = require('../../services/api');
const commerce = require('../../config/commerce');

function imageValue(value) {
  const text = String(value || '').trim();
  return text && text.toLowerCase() !== 'undefined' && text.toLowerCase() !== 'null' ? text : '';
}

function coverImageOf(item, product) {
  const itemCover = imageValue(item && item.coverImageUrl);
  if (itemCover) return itemCover;
  const productCover = imageValue(product && product.coverImageUrl) || (typeof api.resolveCoverImage === 'function' ? api.resolveCoverImage(product) : '');
  if (productCover) return productCover;
  return imageValue(item && item.imageFileId);
}

function decorate(item, product) {
  const fulfillmentType = item.fulfillmentType || commerce.fulfillmentOf(product);
  return Object.assign({}, item, {
    fulfillmentType,
    coverImageUrl: coverImageOf(item, product),
    optionsText: (item.selectedOptions || []).map(option => typeof option === 'string' ? option : option.label || option.name).join(' · '),
    unitPriceText: format.yuan(item.unitPrice),
    subtotalText: format.yuan(Number(item.unitPrice || 0) * Number(item.quantity || 0))
  });
}

Page({
  data: { groups: [], cart: [], cartCount: 0, goodsAmountText: '0.00', totalText: '0.00', takeawayGoodsText: '0.00', courierGoodsText: '0.00', hasTakeaway: false, hasCourier: false, businessBindingMismatch: false },

  onShow() { this.loadCart(); },

  loadCart() {
    const cart = storage.getCart().map(item => {
      const businessType = commerce.businessTypeOf(item);
      const products = storage.getProducts(businessType);
      return decorate(item, products.find(product => product.id === item.productId || product._id === item.productId));
    });
    const stores = {
      restaurant: api.normalizeStore(storage.getStore('restaurant')),
      retail: api.normalizeStore(storage.getStore('retail'))
    };
    const unavailableBusinesses = api.isProduction() ? cart.filter(item => !api.isBusinessAvailable(commerce.businessTypeOf(item))).map(item => commerce.businessTypeOf(item)).filter((item, index, values) => values.indexOf(item) === index) : [];
    const makeGroup = (type, title, subtitle, icon) => {
      const items = cart.filter(item => item.fulfillmentType === type);
      return { type, title, subtitle, icon, items, goodsAmount: items.reduce((sum, item) => sum + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0), goodsText: format.yuan(items.reduce((sum, item) => sum + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0)) };
    };
    const groups = [makeGroup(commerce.FULFILLMENT.TAKEAWAY, '餐饮配送', '门店现制 · 配送到家或到店自取', 'pin'), makeGroup(commerce.FULFILLMENT.COURIER, '零售快递', '商品打包 · 快递送达', 'truck')].filter(group => group.items.length);
    const goodsAmount = cart.reduce((sum, item) => sum + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0);
    this.setData({ groups, cart, cartCount: cart.reduce((sum, item) => sum + Number(item.quantity || 0), 0), goodsAmountText: format.yuan(goodsAmount), totalText: format.yuan(goodsAmount), takeawayGoodsText: format.yuan((groups.find(group => group.type === commerce.FULFILLMENT.TAKEAWAY) || {}).goodsAmount || 0), courierGoodsText: format.yuan((groups.find(group => group.type === commerce.FULFILLMENT.COURIER) || {}).goodsAmount || 0), hasTakeaway: groups.some(group => group.type === commerce.FULFILLMENT.TAKEAWAY), hasCourier: groups.some(group => group.type === commerce.FULFILLMENT.COURIER), businessBindingMismatch: unavailableBusinesses.length > 0, unavailableBusinesses, stores });
  },

  changeQuantity(event) {
    const cart = storage.getCart();
    const item = cart.find(entry => entry.cartKey === event.currentTarget.dataset.key);
    if (!item) return;
    const delta = Number(event.currentTarget.dataset.delta);
    const product = storage.getProducts().find(entry => entry.id === item.productId || entry._id === item.productId);
    const businessType = commerce.businessTypeOf(item);
    const scopedProduct = storage.getProducts(businessType).find(entry => entry.id === item.productId || entry._id === item.productId);
    const currentProduct = scopedProduct || product;
    const stockLimit = currentProduct && currentProduct.stockMode === 'UNLIMITED' ? 99 : Math.max(0, Number(currentProduct ? currentProduct.stock : 0));
    const productCount = cart.filter(entry => entry.productId === item.productId && commerce.businessTypeOf(entry) === businessType).reduce((sum, entry) => sum + Number(entry.quantity || 0), 0);
    if (delta > 0 && (!currentProduct || currentProduct.isSoldOut || productCount >= stockLimit)) { wx.showToast({ title: '库存不足', icon: 'none' }); return; }
    item.quantity = Math.min(stockLimit, Number(item.quantity || 0) + delta);
    if (item.quantity <= 0) cart.splice(cart.indexOf(item), 1);
    storage.saveCart(cart);
    this.loadCart();
  },

  clearCart() { wx.showModal({ title: '清空购物车', content: '确定移除已选商品吗？', success: result => { if (result.confirm) { storage.saveCart([]); this.loadCart(); } } }); },

  goCheckout() {
    if (this.data.businessBindingMismatch) { wx.showToast({ title: '购物车包含当前账号未授权的业务，请重新登录后重试', icon: 'none' }); return; }
    const restaurantStore = this.data.stores && this.data.stores.restaurant || api.normalizeStore(storage.getStore('restaurant'));
    const retailStore = this.data.stores && this.data.stores.retail || api.normalizeStore(storage.getStore('retail'));
    if (this.data.hasTakeaway && api.isProduction() && restaurantStore.catalogOpen === false) { wx.showToast({ title: '餐饮业务当前暂停接单', icon: 'none' }); return; }
    if (this.data.hasCourier && api.isProduction() && retailStore.catalogOpen === false) { wx.showToast({ title: '零售业务当前暂停售卖', icon: 'none' }); return; }
    if (this.data.hasTakeaway && !api.isProduction() && restaurantStore.businessStatus !== 'OPEN') { wx.showToast({ title: '餐饮当前暂停接单', icon: 'none' }); return; }
    if (this.data.hasCourier && !api.isProduction() && (retailStore.courierStatus || 'OPEN') !== 'OPEN') { wx.showToast({ title: '零售当前暂停售卖', icon: 'none' }); return; }
    const takeaway = this.data.groups.find(group => group.type === commerce.FULFILLMENT.TAKEAWAY);
    if (takeaway && !api.isProduction() && takeaway.goodsAmount < Number(restaurantStore.minGoodsAmount || 0)) { wx.showToast({ title: `餐饮满${format.yuan(restaurantStore.minGoodsAmount)}元起送`, icon: 'none' }); return; }
    wx.navigateTo({ url: '/pages/checkout/index' });
  },

  goHome() { wx.switchTab({ url: '/pages/index/index' }); },
  goCold() { wx.switchTab({ url: '/pages/cold/index' }); }
});
