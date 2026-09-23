const mock = require('../data/mock');
const brand = require('../config/brand');

const KEYS = {
  cart: 'food_cart_v1',
  orders: 'food_orders_v1',
  addresses: 'food_addresses_v1',
  coupons: 'food_coupons_v1',
  products: 'food_products_v1',
  catalogs: 'food_catalogs_v1',
  store: 'food_store_v1',
  stores: 'food_stores_v1',
  profile: 'food_profile_v1',
  assist: 'food_assist_v1',
  checkoutAttempt: 'food_checkout_attempt_v1',
  checkoutBatchAttempt: 'food_checkout_batch_attempt_v1',
  orderListFilter: 'food_order_list_filter_v1'
};

function production() { const app = getApp(); return Boolean(app && app.globalData && app.globalData.deploymentMode === 'production'); }
function scoped(key) {
  if (!production()) return key;
  const app = getApp();
  return `production_${app.globalData.env || 'unconfigured'}_${key}`;
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function get(key, fallback) {
  try {
    const value = wx.getStorageSync(scoped(key));
    return value === '' || typeof value === 'undefined' ? clone(fallback) : value;
  } catch (error) {
    return clone(fallback);
  }
}

function set(key, value) {
  wx.setStorageSync(scoped(key), value);
  return value;
}

function makeId(prefix) {
  return `${prefix}_${Date.now()}_${Math.floor(Math.random() * 10000)}`;
}

module.exports = {
  keys: KEYS,
  getCart() { return get(KEYS.cart, []); },
  saveCart(value) { return set(KEYS.cart, value); },
  getOrders() { return get(KEYS.orders, []); },
  saveOrders(value) { return set(KEYS.orders, value); },
  getAddresses() { return get(KEYS.addresses, production() ? [] : [mock.defaultAddress, mock.defaultShippingAddress]); },
  saveAddresses(value) { return set(KEYS.addresses, value); },
  getCoupons() { return get(KEYS.coupons, production() ? [] : mock.coupons); },
  saveCoupons(value) { return set(KEYS.coupons, value); },
  getProducts(businessType) {
    const type = String(businessType || '').toLowerCase();
    if (type === 'restaurant' || type === 'retail') {
      const catalogs = get(KEYS.catalogs, {});
      if (catalogs && Object.prototype.hasOwnProperty.call(catalogs, type)) return clone(catalogs[type] || []);
      return get(KEYS.products, production() ? [] : mock.products).filter(item => {
        const itemType = String(item && (item.businessType || item.business_type) || '').toLowerCase();
        return itemType ? itemType === type : (type === 'retail' ? item && item.fulfillmentType === 'COURIER' : !(item && item.fulfillmentType === 'COURIER'));
      });
    }
    const catalogs = get(KEYS.catalogs, {});
    const values = catalogs && Object.keys(catalogs).length ? Object.keys(catalogs).reduce((all, key) => all.concat(catalogs[key] || []), []) : get(KEYS.products, production() ? [] : mock.products);
    return clone(values);
  },
  saveProducts(value, businessType) {
    const type = String(businessType || '').toLowerCase();
    if (type === 'restaurant' || type === 'retail') {
      const catalogs = get(KEYS.catalogs, {});
      catalogs[type] = clone(value || []);
      return set(KEYS.catalogs, catalogs);
    }
    return set(KEYS.products, value);
  },
  getStore(businessType) {
    const isProduction = production();
    const type = String(businessType || '').toLowerCase();
    const stores = get(KEYS.stores, {});
    const fallback = isProduction ? { name: '', businessStatus: 'PAUSED', courierStatus: 'PAUSED', minGoodsAmount: 0, defaultDeliveryFee: 0, phone: '', businessHours: '', announcement: '' } : mock.store;
    const store = (type === 'restaurant' || type === 'retail') && stores && Object.prototype.hasOwnProperty.call(stores, type) ? clone(stores[type]) : get(KEYS.store, fallback);
    // Only upgrade the former demo brand, without overwriting user settings or production data.
    if (!isProduction && store && store.name === brand.legacyDemoName) return Object.assign({}, store, { name: brand.name });
    return store;
  },
  saveStore(value, businessType) {
    const type = String(businessType || '').toLowerCase();
    if (type === 'restaurant' || type === 'retail') {
      const stores = get(KEYS.stores, {});
      stores[type] = clone(value || {});
      return set(KEYS.stores, stores);
    }
    return set(KEYS.store, value);
  },
  getProfile() { return get(KEYS.profile, null); },
  saveProfile(value) { return set(KEYS.profile, value); },
  getAssist() { return get(KEYS.assist, null); },
  saveAssist(value) { return set(KEYS.assist, value); },
  getCheckoutAttempt() { return get(KEYS.checkoutAttempt, null); },
  saveCheckoutAttempt(value) { return set(KEYS.checkoutAttempt, value); },
  getCheckoutBatchAttempt() { return get(KEYS.checkoutBatchAttempt, null); },
  saveCheckoutBatchAttempt(value) { return set(KEYS.checkoutBatchAttempt, value); },
  saveOrderListFilter(value) { return set(KEYS.orderListFilter, value || 'ALL'); },
  consumeOrderListFilter() {
    const key = scoped(KEYS.orderListFilter);
    const value = get(KEYS.orderListFilter, '');
    try { wx.removeStorageSync(key); } catch (error) { set(KEYS.orderListFilter, ''); }
    return value;
  },
  makeId
};
