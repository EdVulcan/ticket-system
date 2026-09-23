const brand = require('../../../config/brand');
const storage = require('../../../services/storage');
const format = require('../../../utils/format');
const api = require('../../../services/api');
const commerce = require('../../../config/commerce');

function extraPrice(label) {
  const match = String(label || '').match(/\+(\d+(?:\.\d{1,2})?)元$/);
  return match ? Math.round(Number(match[1]) * 100) : 0;
}

Page({
  data: {
    brand,
    product: { options: [] },
    priceText: '0.00',
    originalPriceText: '0.00',
    totalPriceText: '0.00',
    selectedValues: {},
    skuChoices: [],
    selectedSkuId: '',
    quantity: 1,
    remark: '',
    fulfillmentType: 'TAKEAWAY',
    deliveryText: ''
  },

  onLoad(options) {
    if (api.isProduction()) {
      api.getCatalog().then(result => {
        const products = (result.products || []).map(api.normalizeProduct);
        if (result.catalogs) Object.keys(result.catalogs).forEach(type => {
          const catalog = result.catalogs[type] || {};
          storage.saveProducts((catalog.products || []).map(api.normalizeProduct), type);
          storage.saveStore(api.normalizeStore(catalog.store), type);
        });
        else if (products.length) storage.saveProducts(products, products[0].businessType);
        if (result.store) storage.saveStore(api.normalizeStore(result.store));
        this.applyProduct(products.find(item => String(item.id) === String(options.id)));
      }).catch(() => wx.showToast({ title: '商品加载失败', icon: 'none' }));
      return;
    }
    this.applyProduct(storage.getProducts().find(item => String(item.id) === String(options.id)));
  },

  applyProduct(product) {
    if (!product || !product.isOnSale) { this.setData({ product: { options: [] }, unavailable: true }); wx.showToast({ title: '商品不存在或已下架', icon: 'none' }); return; }
    product = api.normalizeProduct(product);
    const fulfillmentType = commerce.fulfillmentOf(product);
    const store = api.normalizeStore(storage.getStore(commerce.businessTypeOf(product)));
    const activeBusinessType = String(store.activeBusinessType || store.businessType || '').toLowerCase();
    if (api.isProduction() && activeBusinessType && commerce.fulfillmentForBusinessType(activeBusinessType) !== fulfillmentType) { this.setData({ product: { options: [] }, unavailable: true }); wx.showToast({ title: '当前门店未开放该业务', icon: 'none' }); return; }
    const open = api.isProduction() ? store.catalogOpen === true : fulfillmentType === commerce.FULFILLMENT.COURIER ? (store.courierStatus || 'OPEN') === 'OPEN' : store.businessStatus === 'OPEN';
    const activeSkus = (product.activeSkus || []).length ? product.activeSkus : (product.skuId ? [product.sku] : []);
    const skuChoices = activeSkus.length > 1 ? activeSkus.map(sku => ({ id: sku.id || sku.skuId || sku.sku_id, name: sku.name || sku.skuCode || sku.sku_code || '默认规格', price: Number(sku.priceCents !== undefined ? sku.priceCents : sku.price_cents || 0), originalPrice: Number(sku.originalPriceCents !== undefined ? sku.originalPriceCents : sku.original_price_cents || sku.priceCents || sku.price_cents || 0) })) : [];
    const selectedSkuId = (activeSkus[0] && (activeSkus[0].id || activeSkus[0].skuId || activeSkus[0].sku_id)) || product.skuId || '';
    if (api.isProduction() && (!product.catalogReady || !selectedSkuId)) { this.setData({ product: { options: [] }, unavailable: true }); wx.showToast({ title: '商品规格暂未配置完成', icon: 'none' }); return; }
    const selectedValues = {};
    (product.options || []).forEach((group) => { if (group.required !== false && group.values.length) selectedValues[group.id] = group.values[0]; });
    const selectedSku = activeSkus.find(sku => String(sku.id || sku.skuId || sku.sku_id) === String(selectedSkuId)) || activeSkus[0] || product.sku || {};
    const deliveryText = fulfillmentType === commerce.FULFILLMENT.COURIER ? store.shippingText || '快递费用以结算页为准' : store.deliveryText || '配送费用以结算页为准';
    this.setData({ product, selectedValues, skuChoices, selectedSkuId, fulfillmentType, deliveryText, unavailable: !open, priceText: format.yuan(Number(selectedSku.priceCents !== undefined ? selectedSku.priceCents : selectedSku.price_cents !== undefined ? selectedSku.price_cents : product.price)), originalPriceText: format.yuan(Number(selectedSku.originalPriceCents !== undefined ? selectedSku.originalPriceCents : selectedSku.original_price_cents !== undefined ? selectedSku.original_price_cents : product.originalPrice)) });
    this.refreshTotal();
  },

  selectSku(event) {
    const selectedSkuId = event.currentTarget.dataset.id;
    this.setData({ selectedSkuId });
    this.refreshTotal();
  },

  selectOption(event) {
    const selectedValues = Object.assign({}, this.data.selectedValues, { [event.currentTarget.dataset.group]: event.currentTarget.dataset.value });
    const group = (this.data.product.options || []).find(item => item.id === event.currentTarget.dataset.group);
    if (group && group.required === false && this.data.selectedValues[group.id] === event.currentTarget.dataset.value) delete selectedValues[group.id];
    this.setData({ selectedValues });
    this.refreshTotal();
  },

  refreshTotal() {
    const optionExtra = Object.keys(this.data.selectedValues || {}).reduce((total, key) => total + extraPrice(this.data.selectedValues[key]), 0);
    const selected = (this.data.product.activeSkus || []).find(sku => String(sku.id || sku.skuId || sku.sku_id) === String(this.data.selectedSkuId));
    const basePrice = selected ? Number(selected.priceCents !== undefined ? selected.priceCents : selected.price_cents || 0) : this.data.product.price;
    const originalPrice = selected ? Number(selected.originalPriceCents !== undefined ? selected.originalPriceCents : selected.original_price_cents !== undefined ? selected.original_price_cents : basePrice) : this.data.product.originalPrice;
    const unitPrice = basePrice + optionExtra;
    this.setData({ priceText: format.yuan(basePrice), originalPriceText: format.yuan(originalPrice), totalPriceText: format.yuan(unitPrice * this.data.quantity) });
  },

  increase() {
    if (this.data.quantity >= Math.min(this.data.product.stock || 99, 10)) return;
    this.setData({ quantity: this.data.quantity + 1 });
    this.refreshTotal();
  },

  decrease() {
    if (this.data.quantity <= 1) return;
    this.setData({ quantity: this.data.quantity - 1 });
    this.refreshTotal();
  },

  onRemarkInput(event) { this.setData({ remark: event.detail.value }); },

  addToCart() {
    const product = this.data.product;
    const store = api.normalizeStore(storage.getStore(commerce.businessTypeOf(product)));
    const open = api.isProduction() ? store.catalogOpen === true : commerce.fulfillmentOf(product) === commerce.FULFILLMENT.COURIER ? (store.courierStatus || 'OPEN') === 'OPEN' : store.businessStatus === 'OPEN';
    if (!product.id || !product.catalogReady && api.isProduction() || !product.isOnSale || product.isSoldOut || !open) { wx.showToast({ title: '商品暂不可购买', icon: 'none' }); return; }
    const selectedOptions = Object.keys(this.data.selectedValues || {}).map((key) => this.data.selectedValues[key]);
    const optionExtra = selectedOptions.reduce((total, label) => total + extraPrice(label), 0);
    const selectedSku = (product.activeSkus || []).find(sku => String(sku.id || sku.skuId || sku.sku_id) === String(this.data.selectedSkuId)) || product.sku || {};
    const skuId = selectedSku.id || selectedSku.skuId || selectedSku.sku_id || product.skuId || '';
    const basePrice = selectedSku.priceCents !== undefined ? selectedSku.priceCents : selectedSku.price_cents !== undefined ? selectedSku.price_cents : product.price;
    const cartKey = `${commerce.businessTypeOf(product)}_${product.id}_${skuId}_${selectedOptions.join('_') || 'default'}`;
    const cart = storage.getCart();
    const current = cart.find((item) => item.cartKey === cartKey);
    const stockLimit = product.stockMode === 'UNLIMITED' ? 99 : Math.max(0, Number(product.stock || 0));
    const productCount = cart.filter(item => item.productId === product.id && commerce.businessTypeOf(item) === commerce.businessTypeOf(product)).reduce((sum, item) => sum + item.quantity, 0);
    if (productCount + this.data.quantity > stockLimit) { wx.showToast({ title: '库存不足', icon: 'none' }); return; }
    if (stockLimit < 1 || (current && current.quantity >= stockLimit) || (!current && this.data.quantity > stockLimit)) { wx.showToast({ title: '库存不足', icon: 'none' }); return; }
    if (current) current.quantity = Math.min(stockLimit, current.quantity + this.data.quantity);
    else cart.push({ cartKey, productId: product.id, skuId, name: product.name, emoji: product.emoji, color: product.color, coverImageUrl: product.coverImageUrl || '', unitPrice: Number(basePrice) + optionExtra, quantity: this.data.quantity, selectedOptions, fulfillmentType: commerce.fulfillmentOf(product), businessType: commerce.businessTypeOf(product), remark: this.data.remark });
    storage.saveCart(cart);
    wx.showToast({ title: '已加入购物车', icon: 'success' });
    setTimeout(() => wx.navigateBack(), 450);
  }
});
