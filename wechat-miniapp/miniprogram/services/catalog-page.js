const brand = require('../config/brand');
const mock = require('../data/mock');
const storage = require('./storage');
const format = require('../utils/format');
const api = require('./api');
const commerce = require('../config/commerce');

function extraPrice(value) {
  const match = String(value || '').match(/\+(\d+(?:\.\d{1,2})?)元$/);
  return match ? Math.round(Number(match[1]) * 100) : 0;
}

function pageCopy(channel) {
  return channel === commerce.FULFILLMENT.COURIER ? {
    channelName: '零售',
    channelKicker: '商品寄送 · 快递到家',
    heroLine1: '把喜欢的',
    heroLine2: '商品寄到你手中。',
    heroSubtitle: '精选商品，按规则配送到家',
    menuTitle: '零售商品',
    serviceTitle: '门店打包，快递送达',
    serviceText: '快递费用按结算页规则计算'
  } : {
    channelName: '餐饮',
    channelKicker: '门店制作 · 配送到家',
    heroLine1: '一份热乎的',
    heroLine2: '餐食，正在路上。',
    heroSubtitle: '门店现制，可配送或到店自取',
    menuTitle: '餐饮菜单',
    serviceTitle: '门店制作，配送或到店自取',
    serviceText: '配送费用以结算页为准'
  };
}

function decorate(product, index, businessType) {
  const normalized = api.normalizeProduct(product, index, businessType);
  const name = normalized.name || '未命名商品';
  const description = normalized.description || '';
  const soldText = normalized.soldText || (normalized.sold === null || normalized.sold === undefined ? '销量待同步' : `月售 ${normalized.sold}`);
  return Object.assign({}, normalized, { name, description, soldText, priceText: format.yuan(normalized.price), originalPriceText: format.yuan(normalized.originalPrice) });
}

function channelCategories(categories, channel) {
  const source = (categories || []).filter(item => item.id === 'all' || commerce.fulfillmentOf(item) === channel || item.channel === channel);
  return [{ id: 'all', name: '全部' }].concat(source.filter(item => item.id !== 'all').map((item, index) => ({ id: item.id || item._id || `category_${index}`, name: item.name || '其他', channel: channel })));
}

function serviceText(channel, store) {
  const value = store || {};
  if (channel === commerce.FULFILLMENT.COURIER) {
    const threshold = Number(value.freeShippingThreshold || 0);
    const carrier = value.shippingCarrier || '默认快递';
    return `${threshold > 0 ? `满 ${format.yuan(threshold)} 元包邮` : '快递费按规则计算'} · ${carrier} · 预计 2-4 天送达`;
  }
  const minimum = Number(value.minGoodsAmount || 0);
  const fee = Number(value.defaultDeliveryFee || 0);
  return `${minimum > 0 ? `满 ${format.yuan(minimum)} 元起送` : '无起送门槛'} · 配送费 ${format.yuan(fee)} 元起`;
}

function createCatalogPage(channel) {
  const copy = pageCopy(channel);
  const businessType = commerce.businessTypeForFulfillment(channel);
  return {
    data: Object.assign({
      brand,
      channel,
      categories: [],
      activeCategory: 'all',
      visibleProducts: [],
      cartCount: 0,
      cartAmountText: '0.00',
      channelOpen: false,
      store: { name: '', businessStatus: 'PAUSED', courierStatus: 'PAUSED' }
    }, copy),

    onLoad() { this.catalogProducts = []; },

    onShow() {
      this.refreshCart();
      this.loadCatalog();
    },

    isChannelOpen(store) {
      const activeBusinessType = String(store && (store.activeBusinessType || store.businessType || '') || '').toLowerCase();
      if (api.isProduction()) {
        if (!activeBusinessType || commerce.fulfillmentForBusinessType(activeBusinessType) !== channel) return false;
        if (store.catalogOpen !== undefined) return store.catalogOpen === true;
      }
      return channel === commerce.FULFILLMENT.COURIER ? (store.courierStatus || 'OPEN') === 'OPEN' : store.businessStatus === 'OPEN';
    },

    loadCatalog() {
      const store = api.normalizeStore(storage.getStore());
      if (api.isProduction() && !api.isCloudEnabled()) {
        this.setData({ visibleProducts: [], store: Object.assign({}, store, { businessStatus: 'PAUSED', courierStatus: 'PAUSED' }), channelOpen: false });
        return;
      }
      const localProducts = api.isProduction() ? [] : storage.getProducts(businessType).map((item, index) => decorate(item, index, businessType));
      const localCategories = api.isProduction() ? [] : channelCategories(mock.categories, channel);
      this.catalogProducts = localProducts.filter(product => commerce.fulfillmentOf(product) === channel);
      this.setData({ store, serviceText: serviceText(channel, store), categories: localCategories, channelOpen: this.isChannelOpen(store), visibleProducts: this.filterProducts(this.catalogProducts, this.data.activeCategory) });
      if (!api.isCloudEnabled()) return;
      api.getCatalog(businessType).then(result => {
        const remoteProducts = (result.products || []).map((item, index) => decorate(item, index, businessType));
        const catalogProducts = remoteProducts.length || api.isProduction() ? remoteProducts : storage.getProducts().map(decorate);
        const remoteStore = api.normalizeStore(result.store || store);
        const categories = channelCategories(result.categories || [], channel);
        storage.saveProducts(remoteProducts, businessType);
        storage.saveStore(remoteStore, businessType);
        const activeBusinessType = String(remoteStore.activeBusinessType || remoteStore.businessType || '').toLowerCase();
        this.catalogProducts = catalogProducts.filter(product => commerce.fulfillmentOf(product) === channel && (!api.isProduction() || (product.catalogReady && activeBusinessType && commerce.fulfillmentForBusinessType(activeBusinessType) === channel)));
        this.setData({ store: remoteStore, serviceText: serviceText(channel, remoteStore), categories, channelOpen: this.isChannelOpen(remoteStore), visibleProducts: this.filterProducts(this.catalogProducts, this.data.activeCategory) });
      }).catch(error => {
        console.error('load catalog failed', error);
        if (api.isProduction()) {
          this.setData({ visibleProducts: [], channelOpen: false, store: Object.assign({}, store, { businessStatus: 'PAUSED', courierStatus: 'PAUSED' }) });
          const message = error && error.statusCode === 409 && error.userMessage ? error.userMessage : '当前业务暂不可用，请稍后重试';
          wx.showToast({ title: message, icon: 'none' });
        }
        else wx.showToast({ title: '云端菜单加载失败，已使用本地菜单', icon: 'none' });
      });
    },

    filterProducts(products, categoryId) {
      const onSale = (products || []).filter(product => product.isOnSale && (!api.isProduction() || product.catalogReady) && !product.isSoldOut && (product.stockMode === 'UNLIMITED' || Number(product.stock) > 0));
      return categoryId === 'all' ? onSale : onSale.filter(product => product.categoryId === categoryId);
    },

    selectCategory(event) {
      const activeCategory = event.currentTarget.dataset.id;
      this.setData({ activeCategory, visibleProducts: this.filterProducts(this.catalogProducts, activeCategory) });
    },

    refreshCart() {
      const cart = storage.getCart();
      const cartCount = cart.reduce((total, item) => total + Number(item.quantity || 0), 0);
      const cartAmount = cart.reduce((total, item) => total + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0);
      this.setData({ cartCount, cartAmountText: format.yuan(cartAmount) });
    },

    openProduct(event) { wx.navigateTo({ url: `/pages/product/detail/index?id=${event.currentTarget.dataset.id}` }); },

    addQuick(event) {
      const product = this.catalogProducts.find(item => item.id === event.currentTarget.dataset.id);
      if (!product) return;
      if (!this.data.channelOpen) { wx.showToast({ title: channel === commerce.FULFILLMENT.COURIER ? '零售暂停售卖' : '餐饮暂时停止接单', icon: 'none' }); return; }
      if (!product.isOnSale || product.isSoldOut || (product.stockMode !== 'UNLIMITED' && product.stock <= 0)) { wx.showToast({ title: '这件商品已售罄', icon: 'none' }); return; }
      if ((product.options && product.options.length) || (product.activeSkus && product.activeSkus.length > 1)) { this.openProduct({ currentTarget: { dataset: { id: product.id } } }); return; }
      this.addToCart(product, []);
    },

    addToCart(product, selectedOptions) {
      const cart = storage.getCart();
      const productCount = cart.filter(item => item.productId === product.id && commerce.businessTypeOf(item) === businessType).reduce((sum, item) => sum + Number(item.quantity || 0), 0);
      const limit = product.stockMode === 'UNLIMITED' ? 99 : Number(product.stock || 0);
      if (productCount >= limit) { wx.showToast({ title: '库存不足', icon: 'none' }); return; }
      const options = selectedOptions || [];
      const skuId = product.skuId || '';
      const cartKey = `${businessType}_${product.id}_${skuId}_${options.map(item => typeof item === 'string' ? item : item.optionId || item.id || item.name).join('_') || 'default'}`;
      const current = cart.find(item => item.cartKey === cartKey);
      const optionExtra = options.reduce((sum, item) => sum + extraPrice(typeof item === 'string' ? item : item.label || item.name), 0);
      if (current) current.quantity = Math.min(limit, current.quantity + 1);
      else cart.push({ cartKey, productId: product.id, skuId, name: product.name, emoji: product.emoji, color: product.color, coverImageUrl: product.coverImageUrl || '', unitPrice: product.price + optionExtra, quantity: 1, selectedOptions: options, fulfillmentType: commerce.fulfillmentOf(product), businessType: commerce.businessTypeOf(product), remark: '' });
      storage.saveCart(cart);
      this.refreshCart();
      wx.showToast({ title: '已加入购物车', icon: 'success' });
    },

    goCart() { wx.navigateTo({ url: '/pages/cart/index' }); },
    goAssist() { wx.navigateTo({ url: '/pages/assist/index/index' }); },
    goCoupons() { wx.switchTab({ url: '/pages/profile/index/index' }); },
    goTakeaway() { wx.switchTab({ url: '/pages/index/index' }); },
    goCold() { wx.switchTab({ url: '/pages/cold/index' }); },
    onShareAppMessage() { return { title: channel === commerce.FULFILLMENT.COURIER ? `${brand.name}｜精选商品，快递到家` : brand.shareTitle, path: channel === commerce.FULFILLMENT.COURIER ? '/pages/cold/index' : '/pages/index/index' }; }
  };
}

module.exports = createCatalogPage;
