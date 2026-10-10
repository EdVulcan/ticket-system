const brand = require('../config/brand');
const mock = require('../data/mock');
const storage = require('./storage');
const format = require('../utils/format');
const api = require('./api');
const commerce = require('../config/commerce');
const promotion = require('../utils/promotion');
const storefrontBrand = require('./storefront-brand');

function extraPrice(value) {
  const match = String(value || '').match(/\+(\d+(?:\.\d{1,2})?)元$/);
  return match ? Math.round(Number(match[1]) * 100) : 0;
}

function pageCopy(channel) {
  return channel === commerce.FULFILLMENT.COURIER ? {
    channelName: '零售',
    channelKicker: '商品快递到家',
    heroLine1: '商品',
    heroLine2: '快递到家',
    heroSubtitle: '按快递规则发货',
    menuTitle: '零售商品',
    serviceTitle: '快递发货',
    serviceText: '快递费以结算页为准'
  } : {
    channelName: '餐饮',
    channelKicker: '配送或到店自取',
    heroLine1: '现点现做',
    heroLine2: '配送或自取',
    heroSubtitle: '支持配送和到店自取',
    menuTitle: '餐饮菜单',
    serviceTitle: '配送 / 到店自取',
    serviceText: '配送费以结算页为准'
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

function hasAssistCoupon(reward) {
  const amount = Number(reward && reward.discountAmount);
  return Boolean(reward && Number.isFinite(amount) && amount > 0);
}

function isAssistCampaignAvailable(campaign, businessType) {
  if (!campaign || !campaign.id || String(campaign.status || '').toLowerCase() !== 'active') return false;
  if (!promotion.appliesToBusiness(campaign, businessType)) return false;
  return hasAssistCoupon(campaign.starterReward) && hasAssistCoupon(campaign.helperReward);
}

function assistRewardText(campaign) {
  const starter = campaign && campaign.starterReward;
  const helper = campaign && campaign.helperReward;
  const starterAmount = Number(starter && starter.discountAmount);
  const helperAmount = Number(helper && helper.discountAmount);
  if (!Number.isFinite(starterAmount) || !Number.isFinite(helperAmount)) return '发起助力，双方都能领券';
  return `你减 ${format.yuan(starterAmount)} 元 · 好友减 ${format.yuan(helperAmount)} 元`;
}

function assistAmountText(reward) {
  const amount = Number(reward && reward.discountAmount);
  return Number.isFinite(amount) && amount > 0 ? format.yuan(amount) : '—';
}

function skuPrice(product, skuId) {
  const skus = Array.isArray(product && product.activeSkus) ? product.activeSkus : [];
  const selected = skus.find(sku => String(sku.id || sku.skuId || sku.sku_id) === String(skuId || '')) || (skus.length === 1 ? skus[0] : null);
  const value = selected ? (selected.priceCents !== undefined ? selected.priceCents : selected.price_cents) : product && product.price;
  const price = Number(value);
  return Number.isFinite(price) ? price : null;
}

function syncCartPrice(product, businessType) {
  if (!product || !product.id || !storage.saveCart) return;
  const cart = storage.getCart();
  let changed = false;
  const next = cart.map(item => {
    if (String(item.productId) !== String(product.id) || commerce.businessTypeOf(item) !== businessType) return item;
    const basePrice = skuPrice(product, item.skuId);
    if (basePrice === null) return item;
    const optionExtra = (item.selectedOptions || []).reduce((sum, option) => sum + extraPrice(typeof option === 'string' ? option : option.label || option.name), 0);
    const unitPrice = basePrice + optionExtra;
    if (Number(item.unitPrice) === unitPrice) return item;
    changed = true;
    return Object.assign({}, item, { unitPrice, name: product.name, coverImageUrl: product.coverImageUrl || item.coverImageUrl || '' });
  });
  if (changed) storage.saveCart(next);
}

function createCatalogPage(channel) {
  const copy = pageCopy(channel);
  const businessType = commerce.businessTypeForFulfillment(channel);
  return {
    data: Object.assign({
      brand,
      storeName: brand.name,
      channel,
      categories: [],
      activeCategory: 'all',
      visibleProducts: [],
      heroProduct: null,
      heroBanner: null,
      cartCount: 0,
      cartAmountText: '0.00',
      channelOpen: false,
      assistAvailable: false,
      assistCampaign: null,
      assistRewardText: '发起助力，双方都能领券',
      assistStarterDiscountText: '—',
      assistHelperDiscountText: '—',
      assistLoaded: false,
      catalogLoaded: false,
      store: { name: '', businessStatus: 'PAUSED', courierStatus: 'PAUSED' }
    }, copy),

    onLoad() { this.catalogProducts = []; },

    onShow() {
      this.catalogVisible = true;
      this.syncStoreName(storage.getStore(businessType));
      const ready = api.isProduction() && typeof api.ensureSession === 'function'
        ? api.ensureSession().catch(error => { console.error('storefront session unavailable', error); return null; })
        : Promise.resolve();
      ready.then(() => {
        this.refreshCart();
        this.loadCatalog();
        this.loadAssistCampaign();
      });
    },

    onHide() { this.catalogVisible = false; },

    syncStoreName(store) {
      const name = storefrontBrand.apply(this, store);
      if (this.catalogVisible && typeof wx.setNavigationBarTitle === 'function') {
        wx.setNavigationBarTitle({ title: `${name} · ${copy.channelName}` });
      }
    },

    loadAssistCampaign() {
      const hadLoaded = this.data.assistLoaded;
      if (!hadLoaded) this.setData({ assistAvailable: false, assistCampaign: null, assistRewardText: '发起助力，双方都能领券', assistStarterDiscountText: '—', assistHelperDiscountText: '—' });
      if (typeof api.getAssistCampaigns !== 'function') {
        this.setData({ assistLoaded: true });
        return;
      }
      api.getAssistCampaigns(businessType).then((result) => {
        const campaigns = Array.isArray(result && result.data) ? result.data : [];
        const campaign = campaigns.find(item => isAssistCampaignAvailable(item, businessType)) || null;
        this.setData({ assistLoaded: true, assistAvailable: Boolean(campaign), assistCampaign: campaign, assistRewardText: assistRewardText(campaign), assistStarterDiscountText: assistAmountText(campaign && campaign.starterReward), assistHelperDiscountText: assistAmountText(campaign && campaign.helperReward) });
      }).catch((error) => {
        // 助力是可选营销入口，活动接口异常时保持隐藏，避免给用户展示无法使用的入口。
        console.error('load assist campaign failed', error);
        this.setData(hadLoaded ? { assistLoaded: true } : { assistLoaded: true, assistAvailable: false, assistCampaign: null, assistRewardText: '发起助力，双方都能领券', assistStarterDiscountText: '—', assistHelperDiscountText: '—' });
      });
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
      const store = api.normalizeStore(storage.getStore(businessType));
      if (api.isProduction() && !api.isCloudEnabled()) {
        if (!this.data.catalogLoaded) this.setData({ visibleProducts: [], heroProduct: null, heroBanner: null, store: Object.assign({}, store, { businessStatus: 'PAUSED', courierStatus: 'PAUSED' }), channelOpen: false, catalogLoaded: true });
        return;
      }
      const localProducts = api.isProduction() ? [] : storage.getProducts(businessType).map((item, index) => decorate(item, index, businessType));
      const localCategories = api.isProduction() ? [] : channelCategories(mock.categories, channel);
      this.catalogProducts = localProducts.filter(product => commerce.fulfillmentOf(product) === channel);
      const localVisibleProducts = this.filterProducts(this.catalogProducts, this.data.activeCategory);
      const initialCatalog = !this.data.catalogLoaded;
      const localData = { store, serviceText: serviceText(channel, store), categories: localCategories, channelOpen: this.isChannelOpen(store), catalogLoaded: true };
      if (initialCatalog) Object.assign(localData, { heroBanner: null, visibleProducts: localVisibleProducts, heroProduct: this.featuredProduct(localVisibleProducts) });
      this.setData(localData);
      if (!api.isCloudEnabled()) return;
      api.getCatalog(businessType).then(result => {
        const remoteProducts = (result.products || []).map((item, index) => decorate(item, index, businessType));
        const catalogProducts = remoteProducts.length || api.isProduction() ? remoteProducts : storage.getProducts().map(decorate);
        const remoteStore = api.normalizeStore(result.store || store);
        const categories = channelCategories(result.categories || [], channel);
        storage.saveProducts(remoteProducts, businessType);
        storage.saveStore(remoteStore, businessType);
        this.syncStoreName(remoteStore);
        const activeBusinessType = String(remoteStore.activeBusinessType || remoteStore.businessType || '').toLowerCase();
        this.catalogProducts = catalogProducts.filter(product => commerce.fulfillmentOf(product) === channel && (!api.isProduction() || (product.catalogReady && activeBusinessType && commerce.fulfillmentForBusinessType(activeBusinessType) === channel)));
        catalogProducts.forEach(product => syncCartPrice(product, businessType));
        const remoteVisibleProducts = this.filterProducts(this.catalogProducts, this.data.activeCategory);
        this.setData({ store: remoteStore, heroBanner: result.hero || null, serviceText: serviceText(channel, remoteStore), categories, channelOpen: this.isChannelOpen(remoteStore), visibleProducts: remoteVisibleProducts, heroProduct: this.featuredProduct(remoteVisibleProducts) });
      }).catch(error => {
        console.error('load catalog failed', error);
        if (api.isProduction()) {
          if (!this.data.catalogLoaded || (!this.data.visibleProducts || !this.data.visibleProducts.length)) this.setData({ visibleProducts: [], heroProduct: null, heroBanner: null, channelOpen: false, store: Object.assign({}, store, { businessStatus: 'PAUSED', courierStatus: 'PAUSED' }), catalogLoaded: true });
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

    featuredProduct(products) {
      return (products || []).find(product => product.coverImageUrl) || (products || [])[0] || null;
    },

    selectCategory(event) {
      const activeCategory = event.currentTarget.dataset.id;
      const visibleProducts = this.filterProducts(this.catalogProducts, activeCategory);
      this.setData({ activeCategory, visibleProducts, heroProduct: this.featuredProduct(visibleProducts) });
    },

    refreshCart() {
      const cart = storage.getCart();
      const cartCount = cart.reduce((total, item) => total + Number(item.quantity || 0), 0);
      const cartAmount = cart.reduce((total, item) => total + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0);
      this.setData({ cartCount, cartAmountText: format.yuan(cartAmount) });
    },

    openProduct(event) { wx.navigateTo({ url: `/pages/product/detail/index?id=${event.currentTarget.dataset.id}` }); },

    openHero(event) {
      const hero = this.data.heroBanner;
      const productID = hero && hero.targetType === 'product' ? hero.targetProductId : '';
      if (productID) this.openProduct({ currentTarget: { dataset: { id: productID } } });
    },

    onHeroImageError() {
      // A stale or unreachable configured image should never leave a blank
      // first screen. The product cover fallback remains data-driven.
      this.setData({ heroBanner: null });
    },

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
      if (current) {
        current.quantity = Math.min(limit, current.quantity + 1);
        current.unitPrice = product.price + optionExtra;
        current.name = product.name;
        current.coverImageUrl = product.coverImageUrl || current.coverImageUrl || '';
      }
      else cart.push({ cartKey, productId: product.id, skuId, name: product.name, emoji: product.emoji, color: product.color, coverImageUrl: product.coverImageUrl || '', unitPrice: product.price + optionExtra, quantity: 1, selectedOptions: options, fulfillmentType: commerce.fulfillmentOf(product), businessType: commerce.businessTypeOf(product), remark: '' });
      storage.saveCart(cart);
      this.refreshCart();
      wx.showToast({ title: '已加入购物车', icon: 'success' });
    },

    goCart() { wx.navigateTo({ url: '/pages/cart/index' }); },
    goAssist() {
      const typeQuery = businessType === 'restaurant' ? '' : `?business_type=${encodeURIComponent(businessType)}`;
      wx.navigateTo({ url: `/pages/assist/index/index${typeQuery}` });
    },
    goCoupons() { wx.switchTab({ url: '/pages/profile/index/index' }); },
    goTakeaway() { wx.switchTab({ url: '/pages/index/index' }); },
    goCold() { wx.switchTab({ url: '/pages/cold/index' }); },
    onShareAppMessage() { return { title: `${storefrontBrand.name(this.data.store)}｜${channel === commerce.FULFILLMENT.COURIER ? '商品快递到家' : '餐饮配送或自取'}`, path: channel === commerce.FULFILLMENT.COURIER ? '/pages/cold/index' : '/pages/index/index' }; }
  };
}

module.exports = createCatalogPage;
