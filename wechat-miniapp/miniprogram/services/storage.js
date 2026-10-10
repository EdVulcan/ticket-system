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
  storefrontName: 'food_storefront_name_v1',
  profile: 'food_profile_v1',
  assist: 'food_assist_v1',
  checkoutAttempt: 'food_checkout_attempt_v1',
  checkoutBatchAttempt: 'food_checkout_batch_attempt_v1',
  directCheckout: 'food_direct_checkout_v1',
  orderListFilter: 'food_order_list_filter_v1'
};

function production() { const app = getApp(); return Boolean(app && app.globalData && app.globalData.deploymentMode === 'production'); }
function scoped(key) {
  if (!production()) return key;
  const app = getApp();
  return `production_${app.globalData.env || 'unconfigured'}_${key}`;
}

function storefrontNameKey() {
  if (!production()) return KEYS.storefrontName;
  const data = getApp().globalData;
  // Prevent an AppID/API configuration switch from showing another store's cached name.
  return `${scoped(KEYS.storefrontName)}_${encodeURIComponent(data.appId || '')}_${encodeURIComponent(data.apiBaseUrl || '')}`;
}

function customerToken() {
  try {
    const app = getApp();
    const appSession = app && app.globalData && app.globalData.session;
    if (appSession && appSession.token) return String(appSession.token);
    // Load lazily to avoid a module cycle during app startup. The HTTP layer
    // owns the persisted session created from wx.login.
    const http = require('./http');
    const session = http && typeof http.getSession === 'function' ? http.getSession() : null;
    return session && session.token ? String(session.token) : '';
  } catch (error) {
    return '';
  }
}

function tokenFingerprint(value) {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) hash = Math.imul(hash ^ value.charCodeAt(index), 16777619);
  return (hash >>> 0).toString(16).padStart(8, '0');
}

function customerScoped(key) {
  if (!production()) return key;
  const app = getApp();
  const token = customerToken();
  // Do not read or write a shared anonymous cart. Pages wait for ensureSession
  // before loading customer data, so a missing token fails closed here too.
  if (!token) return '';
  return `production_${app.globalData.env || 'unconfigured'}_customer_${tokenFingerprint(token)}_${key}`;
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function read(key, fallback) {
  try {
    const value = wx.getStorageSync(key);
    return value === '' || typeof value === 'undefined' ? clone(fallback) : value;
  } catch (error) {
    return clone(fallback);
  }
}

function write(key, value) {
  if (!key) return value;
  wx.setStorageSync(key, value);
  return value;
}

function get(key, fallback) {
  return read(scoped(key), fallback);
}

function set(key, value) {
  return write(scoped(key), value);
}

function customerGet(key, fallback) {
  const storageKey = customerScoped(key);
  return storageKey ? read(storageKey, fallback) : clone(fallback);
}

function customerSet(key, value) {
  return write(customerScoped(key), value);
}

function makeId(prefix) {
  return `${prefix}_${Date.now()}_${Math.floor(Math.random() * 10000)}`;
}

module.exports = {
  keys: KEYS,
  getCart() { return customerGet(KEYS.cart, []); },
  saveCart(value) { return customerSet(KEYS.cart, value); },
  getDirectCheckout() { return customerGet(KEYS.directCheckout, null); },
  saveDirectCheckout(value) { return customerSet(KEYS.directCheckout, value); },
  getOrders() { return customerGet(KEYS.orders, []); },
  saveOrders(value) { return customerSet(KEYS.orders, value); },
  getAddresses() { return customerGet(KEYS.addresses, production() ? [] : [mock.defaultAddress, mock.defaultShippingAddress]); },
  saveAddresses(value) { return customerSet(KEYS.addresses, value); },
  getCoupons() { return customerGet(KEYS.coupons, production() ? [] : mock.coupons); },
  saveCoupons(value) { return customerSet(KEYS.coupons, value); },
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
    // Display metadata is shared by both businesses; fulfillment settings stay separate.
    const name = String(value && (value.displayName || value.display_name || value.name) || '').trim();
    write(storefrontNameKey(), name);
    const app = getApp();
    if (app && app.globalData) app.globalData.storeName = name || brand.name;
    const type = String(businessType || '').toLowerCase();
    if (type === 'restaurant' || type === 'retail') {
      const stores = get(KEYS.stores, {});
      stores[type] = clone(value || {});
      return set(KEYS.stores, stores);
    }
    return set(KEYS.store, value);
  },
  getStorefrontName() {
    if (typeof getApp !== 'function' || !getApp()) return '';
    return String(read(storefrontNameKey(), '') || '').trim();
  },
  getProfile() { return customerGet(KEYS.profile, null); },
  saveProfile(value) { return customerSet(KEYS.profile, value); },
  getAssist() { return customerGet(KEYS.assist, null); },
  saveAssist(value) { return customerSet(KEYS.assist, value); },
  getCheckoutAttempt() { return customerGet(KEYS.checkoutAttempt, null); },
  saveCheckoutAttempt(value) { return customerSet(KEYS.checkoutAttempt, value); },
  getCheckoutBatchAttempt() { return customerGet(KEYS.checkoutBatchAttempt, null); },
  saveCheckoutBatchAttempt(value) { return customerSet(KEYS.checkoutBatchAttempt, value); },
  saveOrderListFilter(value) { return set(KEYS.orderListFilter, value || 'ALL'); },
  consumeOrderListFilter() {
    const key = scoped(KEYS.orderListFilter);
    const value = get(KEYS.orderListFilter, '');
    try { wx.removeStorageSync(key); } catch (error) { set(KEYS.orderListFilter, ''); }
    return value;
  },
  makeId
};
