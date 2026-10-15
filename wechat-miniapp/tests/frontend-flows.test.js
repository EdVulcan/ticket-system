const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const { createRequire } = require('node:module');
function load(relative, stubs, globals = {}) {
  const filename = path.resolve(__dirname, '..', relative); const module = { exports: {} }; const localRequire = createRequire(filename);
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), Object.assign({ module, exports: module.exports, console, setTimeout, require: id => Object.prototype.hasOwnProperty.call(stubs, id) ? stubs[id] : localRequire(id) }, globals), { filename });
  return module.exports;
}
test('profile order shortcuts open the order list with the requested filter', () => {
  let profilePage; let orderPage; let requestedTab = '';
  load('miniprogram/pages/profile/index/index.js', {
    '../../../config/brand': {},
    '../../../data/mock': { store: {} },
    '../../../services/storage': { saveOrderListFilter: value => { requestedTab = value; } },
    '../../../services/api': {}
  }, {
    Page: value => { profilePage = value; },
    wx: { switchTab: ({ url }) => assert.equal(url, '/pages/order/list/index') }
  });
  profilePage.goOrders({ currentTarget: { dataset: { tab: 'DELIVERING' } } });
  assert.equal(requestedTab, 'DELIVERING');

  load('miniprogram/pages/order/list/index.js', {
    '../../../services/storage': {
      consumeOrderListFilter: () => requestedTab,
      getOrders: () => [
        { id: 'delivery', fulfillmentType: 'TAKEAWAY', status: 'DELIVERING', itemsSnapshot: [] },
        { id: 'complete', fulfillmentType: 'TAKEAWAY', status: 'COMPLETED', itemsSnapshot: [] }
      ]
    },
    '../../../utils/format': { orderStatus: value => value, dateTime: () => '', yuan: () => '0.00' },
    '../../../services/api': { isProduction: () => false },
    '../../../config/commerce': { FULFILLMENT: { COURIER: 'COURIER' } }
  }, { Page: value => { orderPage = value; }, wx: {} });
  orderPage.data = Object.assign({}, orderPage.data);
  orderPage.setData = change => { orderPage.data = Object.assign({}, orderPage.data, change); };
  orderPage.onShow();
  assert.equal(orderPage.data.activeTab, 'DELIVERING');
  assert.deepEqual(orderPage.data.filteredOrders.map(item => item.id), ['delivery']);
});
test('profile contact sheet previews the managed QR code and copies the configured WeChat ID', () => {
  let profilePage; let previewed; let copied; let toast;
  load('miniprogram/pages/profile/index/index.js', {
    '../../../config/brand': {},
    '../../../data/mock': { store: {} },
    '../../../services/storage': {},
    '../../../services/api': {}
  }, {
    Page: value => { profilePage = value; },
    wx: {
      previewImage: options => { previewed = options; },
      setClipboardData: options => { copied = options.data; options.success(); },
      showToast: options => { toast = options.title; }
    }
  });
  profilePage.data = Object.assign({}, profilePage.data, { contact: { available: true, wechatId: 'shop-service', qrCodeUrl: 'https://example.com/contact.png' } });
  profilePage.setData = change => { profilePage.data = Object.assign({}, profilePage.data, change); };
  profilePage.openContact();
  assert.equal(profilePage.data.showContact, true);
  profilePage.previewContactQRCode();
  profilePage.copyWechatID();
  assert.equal(previewed.current, 'https://example.com/contact.png');
  assert.equal(previewed.urls.length, 1);
  assert.equal(previewed.urls[0], 'https://example.com/contact.png');
  assert.equal(copied, 'shop-service');
  assert.equal(toast, '微信号已复制');
  profilePage.closeContact();
  assert.equal(profilePage.data.showContact, false);
});
test('client payment failure is reconciled as paid when provider already succeeded', async () => {
  let queries = 0;
  const flow = load('miniprogram/services/payment.js', { './api': { queryPayment: async () => ({ status: ++queries === 1 ? 'WAIT_PAY' : 'PREPARING' }), createPayment: async () => ({ success: true, payment: { timeStamp: '1', nonceStr: 'n', package: 'prepay_id=p', paySign: 's', signType: 'RSA' } }) } }, { wx: { requestPayment: params => params.fail(new Error('client timeout')) } });
  assert.equal((await flow.payOrder('order')).status, 'PREPARING'); assert.equal(queries, 2);
});
test('already-paid order never opens another payment sheet', async () => {
  const flow = load('miniprogram/services/payment.js', { './api': { queryPayment: async () => ({ status: 'DELIVERING' }), createPayment: async () => assert.fail('must not create') } });
  assert.equal((await flow.payOrder('order')).status, 'DELIVERING');
});
test('refund states never reopen the WeChat payment sheet', async () => {
  for (const status of ['AFTER_SALE_REVIEW', 'REFUNDING']) {
    let created = false;
    const flow = load('miniprogram/services/payment.js', { './api': { queryPayment: async () => ({ status }), createPayment: async () => { created = true; } } }, { wx: { requestPayment: () => assert.fail('must not open payment') } });
    assert.equal((await flow.payOrder('order')).status, status); assert.equal(created, false);
  }
});
test('payment flow obtains a fresh wx login code for a new JSAPI payment', async () => {
  let loginCount = 0; let paymentOptions;
  const flow = load('miniprogram/services/payment.js', { './api': {
    queryPayment: async () => ({ status: loginCount ? 'PAID' : 'WAIT_PAY' }),
    createPayment: async (orderNo, options) => { paymentOptions = { orderNo, options }; return { status: 'WAIT_PAY', payment: { timeStamp: '1', nonceStr: 'n', package: 'prepay_id=p', paySign: 's', signType: 'RSA' } }; }
  } }, { wx: {
    login: options => { loginCount += 1; options.success({ code: 'fresh-code' }); },
    requestPayment: options => options.success({})
  } });
  assert.equal((await flow.payOrder('NO-1')).status, 'PAID');
  assert.equal(paymentOptions.orderNo, 'NO-1'); assert.equal(paymentOptions.options.clientRequestId, 'payment_NO-1'); assert.equal(paymentOptions.options.loginCode, 'fresh-code');
  assert.equal(loginCount, 1);
});
test('production payment adapter uses order number endpoints and normalizes payment params', async () => {
  const calls = [];
  const api = load('miniprogram/services/api.js', {
    './storage': { getStore: () => ({}), getProducts: () => [], getAddresses: () => [], saveProducts() {}, saveStore() {} },
    './http': {
      isConfigured: () => true,
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => { calls.push({ path, options }); return path.endsWith('/payment') ? { data: { status: 'PAID', payment_status: 'paid', out_trade_no: 'OT-1' } } : { data: { status: 'WAIT_PAY', attempt_id: 'A-1', payment: { time_stamp: '1', nonce_str: 'n', package: 'prepay_id=p', sign_type: 'RSA', pay_sign: 's' } } }; }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const created = await api.createPayment('NO-1', { loginCode: 'fresh-code' });
  const queried = await api.queryPayment('NO-1');
  assert.equal(created.payment.timeStamp, '1'); assert.equal(created.payment.nonceStr, 'n'); assert.equal(created.attemptId, 'A-1');
  assert.equal(queried.status, 'PAID'); assert.equal(queried.outTradeNo, 'OT-1');
  assert.equal(calls[0].path, '/orders/NO-1/payments'); assert.equal(calls[0].options.data.order_no, 'NO-1'); assert.equal(calls[0].options.data.login_code, 'fresh-code');
  assert.equal(calls[1].path, '/orders/NO-1/payment'); assert.equal(calls[1].options.method, 'GET');
});

test('payment view closes payment for pending approval and provider refund states', () => {
  const api = load('miniprogram/services/api.js', {
    './storage': {},
    './http': { isProduction: () => false, isConfigured: () => false, createError: (code, message) => Object.assign(new Error(message), { code }) }
  });
  const pending = api.normalizePaymentView({ status: 'WAIT_PAY', payment_status: 'paid', fulfillment_status: 'completed', refund_status: 'requested', after_sales: [{ id: 1, status: 'requested', type: 'refund' }] });
  assert.equal(pending.status, 'AFTER_SALE_REVIEW');
  assert.equal(pending.fulfillmentCompleted, true);
  assert.equal(pending.afterSaleStatus, 'REQUESTED');
  const approved = api.normalizePaymentView({ status: 'PAID', payment_status: 'paid', fulfillment_status: 'completed', refund_status: 'requested', after_sales: [{ id: 1, status: 'approved', type: 'refund' }] });
  assert.equal(approved.status, 'REFUNDING');
  const processed = api.normalizePaymentView({ status: 'PAID', payment_status: 'paid', fulfillment_status: 'completed', refund_status: 'processing', after_sales: [{ id: 1, status: 'processing', type: 'refund' }] });
  assert.equal(processed.status, 'REFUNDING');
  assert.equal(api.normalizePaymentView({ status: 'REFUNDING' }).status, 'REFUNDING');
  const refunded = api.normalizePaymentView({ status: 'PAID', payment_status: 'refunded', fulfillment_status: 'completed', refund_status: 'refunded', after_sales: [{ id: 1, status: 'completed', type: 'refund' }] });
  assert.equal(refunded.status, 'REFUNDED');
});

test('completed fulfillment remains visible while after-sale moves from review to provider processing', () => {
  const api = load('miniprogram/services/api.js', {
    './storage': {},
    './http': { isProduction: () => false, isConfigured: () => false, createError: (code, message) => Object.assign(new Error(message), { code }) }
  });
  const base = { order_no: 'ORDER-AFTER-SALE-1', business_type: 'restaurant', status: 'COMPLETED', payment_status: 'paid', fulfillment_status: 'completed', refund_status: 'requested', after_sales: [{ id: 1, status: 'requested', type: 'refund' }] };
  const pending = api.normalizeOrder(base);
  assert.equal(pending.status, 'AFTER_SALE_REVIEW');
  assert.equal(pending.fulfillmentStatus, 'completed');
  assert.equal(pending.fulfillmentCompleted, true);
  assert.equal(pending.afterSaleStatus, 'REQUESTED');
  const approved = api.normalizeOrder(Object.assign({}, base, { after_sales: [{ id: 1, status: 'approved', type: 'refund' }] }));
  assert.equal(approved.status, 'REFUNDING');
  const processed = api.normalizeOrder(Object.assign({}, base, { refund_status: 'processing', after_sales: [{ id: 1, status: 'processing', type: 'refund' }] }));
  assert.equal(processed.status, 'REFUNDING');
  const pendingProvider = api.normalizeOrder(Object.assign({}, base, { refund_status: 'pending', after_sales: [] }));
  assert.equal(pendingProvider.status, 'REFUNDING');
  const refunded = api.normalizeOrder(Object.assign({}, base, { payment_status: 'refunded', refund_status: 'refunded', after_sales: [{ id: 1, status: 'completed', type: 'refund' }] }));
  assert.equal(refunded.status, 'REFUNDED');
  assert.equal(refunded.fulfillmentStatus, 'completed');
});
test('production HTTP requests use the storefront wechat route prefix for every SaaS resource', async () => {
  const requests = [];
  const session = { token: 'session-token', expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString() };
  const http = load('miniprogram/services/http.js', {
    '../config/runtime': { deploymentMode: 'production', apiBaseUrl: 'https://ymsq.edvulcan.top/api/v1', appId: 'wx-test', requestTimeoutMs: 1000 }
  }, {
    getApp: () => ({ globalData: { deploymentMode: 'production', apiBaseUrl: 'https://ymsq.edvulcan.top/api/v1', appId: 'wx-test' } }),
    wx: {
      getStorageSync: () => session,
      request: options => { requests.push(options.url); options.success({ statusCode: 200, data: {} }); }
    }
  });
  for (const pathValue of [
    '/catalog',
    '/carts',
    '/carts/cart-1/items',
    '/orders',
    '/orders/order-1/payments',
    '/orders/order-1/payment',
    '/orders/order-1/refund-requests',
    '/addresses',
    '/addresses/address-1/default'
  ]) await http.request(pathValue);
  assert.deepEqual(requests, [
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/catalog',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/carts',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/carts/cart-1/items',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/orders',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/orders/order-1/payments',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/orders/order-1/payment',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/orders/order-1/refund-requests',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/addresses',
    'https://ymsq.edvulcan.top/api/v1/storefront/wechat/addresses/address-1/default'
  ]);
});
test('production session login uses the storefront wechat route', async () => {
  const requests = [];
  const http = load('miniprogram/services/http.js', {
    '../config/runtime': { deploymentMode: 'production', apiBaseUrl: 'https://ymsq.edvulcan.top/api/v1', appId: 'wx-test', requestTimeoutMs: 1000 }
  }, {
    getApp: () => ({ globalData: { deploymentMode: 'production', apiBaseUrl: 'https://ymsq.edvulcan.top/api/v1', appId: 'wx-test' } }),
    wx: {
      getStorageSync: () => null,
      setStorageSync: () => {},
      request: options => { requests.push(options.url); options.success({ statusCode: 200, data: { token: 'session-token', expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString() } }); },
      login: options => options.success({ code: 'login-code' })
    }
  });
  await http.ensureSession(true);
  assert.deepEqual(requests, ['https://ymsq.edvulcan.top/api/v1/storefront/wechat/session']);
});

test('shared storefront login stores server-authorized businesses and reuses one wx login code', async () => {
  let stored = null;
  let loginCount = 0;
  const globalData = { deploymentMode: 'production', apiBaseUrl: 'https://api.example.test/api/v1', appId: 'wx-test' };
  const requests = [];
  const http = load('miniprogram/services/http.js', {
    '../config/runtime': { deploymentMode: 'production', apiBaseUrl: globalData.apiBaseUrl, appId: globalData.appId, requestTimeoutMs: 1000 }
  }, {
    getApp: () => ({ globalData }),
    wx: {
      getStorageSync: () => stored,
      setStorageSync: (key, value) => { stored = value; },
      removeStorageSync: () => { stored = null; },
      request: options => { requests.push(options); options.success({ statusCode: 200, data: { token: 'shared-token', expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(), businesses: [{ business_type: 'restaurant', location: { id: 11 } }, { business_type: 'retail', location: { id: 22 } }] } }); },
      login: options => { loginCount += 1; options.success({ code: `code-${loginCount}` }); }
    }
  });
  const first = await http.ensureSession(true);
  const second = await http.ensureSession();
  assert.equal(loginCount, 1);
  assert.equal(first.token, 'shared-token');
  assert.deepEqual(first.businesses.map(item => item.businessType), ['restaurant', 'retail']);
  assert.deepEqual(second.businesses.map(item => item.locationId), [11, 22]);
  assert.equal(requests[0].data.app_id, 'wx-test');
  assert.equal(requests[0].data.code, 'code-1');
  assert.equal(requests[0].data.business_type, undefined);
});

test('legacy session without businesses recovers once after a selected business returns 409', async () => {
  let stored = { token: 'legacy-token', expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString() };
  let loginCount = 0;
  const requests = [];
  const http = load('miniprogram/services/http.js', {
    '../config/runtime': { deploymentMode: 'production', apiBaseUrl: 'https://api.example.test/api/v1', appId: 'wx-test', requestTimeoutMs: 1000 }
  }, {
    getApp: () => ({ globalData: { deploymentMode: 'production', apiBaseUrl: 'https://api.example.test/api/v1', appId: 'wx-test' } }),
    wx: {
      getStorageSync: () => stored,
      setStorageSync: (key, value) => { stored = value; },
      removeStorageSync: () => { stored = null; },
      request: options => {
        requests.push(options);
        if (options.url.indexOf('/catalog?business_type=retail') >= 0 && requests.filter(item => item.url.indexOf('/catalog?business_type=retail') >= 0).length === 1) {
          options.success({ statusCode: 409, data: { error: '该小程序同时发布了多个业务，请指定业务类型' } });
          return;
        }
        if (options.url.endsWith('/session')) {
          options.success({ statusCode: 200, data: { token: 'fresh-token', expires_at: new Date(Date.now() + 60 * 60 * 1000).toISOString(), businesses: [{ business_type: 'retail', location: { id: 22 } }] } });
          return;
        }
        options.success({ statusCode: 200, data: {} });
      },
      login: options => { loginCount += 1; options.success({ code: 'fresh-code' }); }
    }
  });
  await http.request('/catalog', { businessType: 'retail' });
  assert.equal(loginCount, 1);
  assert.equal(requests.filter(item => item.url.indexOf('/catalog?business_type=retail') >= 0).length, 2);
  assert.equal(stored.businesses[0].businessType, 'retail');
});

test('shared session loads independent restaurant and retail catalogs with selectors', async () => {
  const requests = [];
  const saved = [];
  const products = {
    restaurant: [{ id: 'meal', business_type: 'restaurant', skus: [{ id: 'meal-sku', status: 'active', price_cents: 1200 }] }],
    retail: [{ id: 'snack', business_type: 'retail', skus: [{ id: 'snack-sku', status: 'active', price_cents: 2200 }] }]
  };
  const api = load('miniprogram/services/api.js', {
    './storage': {
      getProducts: () => [], getAddresses: () => [], saveProducts: (value, type) => saved.push({ type, value }), saveStore() {}, getStore: () => ({})
    },
    './http': {
      isConfigured: () => true,
      isProduction: () => true,
      getBusinesses: () => [{ businessType: 'restaurant' }, { businessType: 'retail' }],
      ensureSession: () => Promise.resolve({}),
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => {
        requests.push({ path, options });
        const type = options.businessType;
        return { data: { business_type: type, location: { id: type === 'restaurant' ? 11 : 22, business_type: type, status: 'active' }, products: products[type] } };
      }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const result = await api.getCatalogs();
  assert.equal(requests.map(item => item.options.businessType).join(','), 'restaurant,retail');
  assert.equal(result.products.map(item => item.businessType).join(','), 'restaurant,retail');
  assert.equal(saved.map(item => item.type).join(','), 'restaurant,retail');
});

test('legacy cached session refreshes before an unscoped catalog request', async () => {
  const requests = [];
  const ensureCalls = [];
  let businesses = [];
  const api = load('miniprogram/services/api.js', {
    './storage': { getProducts: () => [], getAddresses: () => [], saveProducts() {}, saveStore() {}, getStore: () => ({}) },
    './http': {
      isConfigured: () => true,
      isProduction: () => true,
      getSession: () => ({ token: businesses.length ? 'fresh-token' : 'legacy-token', businesses }),
      getBusinesses: () => businesses,
      ensureSession: force => {
        ensureCalls.push(Boolean(force));
        if (force) businesses = [{ businessType: 'restaurant' }, { businessType: 'retail' }];
        return Promise.resolve({ token: businesses.length ? 'fresh-token' : 'legacy-token', businesses });
      },
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => {
        requests.push({ path, options: options || {} });
        const type = options.businessType;
        return { data: { business_type: type, location: { id: type === 'restaurant' ? 11 : 22, business_type: type, status: 'active' }, products: [] } };
      }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const result = await api.getCatalog();
  assert.equal(result.products.length, 0);
  assert.deepEqual(ensureCalls, [false, true, false]);
  assert.deepEqual(requests.map(item => item.options.businessType), ['restaurant', 'retail']);
  assert.ok(requests.every(item => item.options.businessType));
});

test('restaurant and retail carts and checkout calls never share a business selector', async () => {
  const calls = [];
  const products = {
    restaurant: [{ id: 'meal', businessType: 'restaurant', skuId: 'meal-sku', skus: [{ id: 'meal-sku', status: 'active' }] }],
    retail: [{ id: 'snack', businessType: 'retail', skuId: 'snack-sku', skus: [{ id: 'snack-sku', status: 'active' }] }]
  };
  const api = load('miniprogram/services/api.js', {
    './storage': { getProducts: type => products[type] || [], getAddresses: () => [], saveProducts() {}, saveStore() {}, getStore: () => ({}) },
    './http': {
      isConfigured: () => true,
      isProduction: () => true,
      getBusinesses: () => [{ businessType: 'restaurant' }, { businessType: 'retail' }],
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => {
        calls.push({ path, options: options || {} });
        if (path === '/carts') return { id: `cart-${options.businessType}`, business_type: options.businessType, items: [] };
        if (path === '/checkout-quotes') return { quote_token: `quote-${options.businessType}`, quote: { goods_subtotal_cents: 1000, payable_amount_cents: 1000 } };
        if (path.indexOf('/checkout') >= 0) return { order_no: `order-${options.businessType}`, business_type: options.businessType, payment_status: 'paid', items: [] };
        return { id: `cart-${options.businessType}`, business_type: options.businessType, items: [] };
      }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  await api.createOrder({ businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', items: [{ productId: 'meal', skuId: 'meal-sku', quantity: 1 }] });
  await api.createOrder({ businessType: 'retail', fulfillmentType: 'COURIER', items: [{ productId: 'snack', skuId: 'snack-sku', quantity: 1 }] });
  const transactionCalls = calls.filter(item => item.path === '/carts' || item.path.indexOf('/items') >= 0 || item.path.endsWith('/checkout'));
  assert.deepEqual(transactionCalls.map(item => item.options.businessType), ['restaurant', 'restaurant', 'restaurant', 'retail', 'retail', 'retail']);
  assert.ok(transactionCalls.every(item => !item.options.data || item.options.data.business_type === undefined));
  const quoteCalls = calls.filter(item => item.path === '/checkout-quotes');
  assert.deepEqual(quoteCalls.map(item => item.options.businessType), ['restaurant', 'retail']);
  assert.notEqual(quoteCalls[0].options.data, quoteCalls[1].options.data);
  const checkoutCalls = transactionCalls.filter(item => item.path.endsWith('/checkout'));
  assert.equal(checkoutCalls[0].options.data.fulfillment_method, 'delivery');
  assert.equal(Object.prototype.hasOwnProperty.call(checkoutCalls[1].options.data, 'fulfillment_method'), false);
});

test('createOrder clears an active server cart before adding and converges on retry', async () => {
  const calls = [];
  const timeline = [];
  const products = { meal: [{ id: 'meal', businessType: 'restaurant', skuId: 'meal-sku', skus: [{ id: 'meal-sku', status: 'active' }] }] };
  let serverCart = { id: 'cart-restaurant', items: [{ id: 'stale-item', product_id: 'old-product', sku_id: 'old-sku', quantity: 3 }] };
  let orderNumber = 0;
  let quoteNumber = 0;
  const api = load('miniprogram/services/api.js', {
    './storage': { getProducts: type => type === 'restaurant' ? products.meal : [], getAddresses: () => [], saveProducts() {}, saveStore() {}, getStore: () => ({}) },
    './http': {
      isConfigured: () => true,
      isProduction: () => true,
      getBusinesses: () => [{ businessType: 'restaurant' }],
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => {
        calls.push({ path, options: options || {} });
        if (path === '/carts') return { id: serverCart.id, items: serverCart.items.map(item => Object.assign({}, item)) };
        if (path.indexOf('/items/') >= 0 && options.method === 'DELETE') {
          timeline.push(`remove:${options.businessType}`);
          const itemId = path.split('/').pop();
          serverCart.items = serverCart.items.filter(item => String(item.id) !== String(itemId));
          return { id: serverCart.id, items: serverCart.items.map(item => Object.assign({}, item)) };
        }
        if (path.endsWith('/items')) {
          timeline.push(`add:${options.businessType}`);
          const body = options.data;
          serverCart.items.push({ id: `added-${serverCart.items.length}`, product_id: body.product_id, sku_id: body.sku_id, quantity: body.quantity });
          return { id: serverCart.id, items: serverCart.items.map(item => Object.assign({}, item)) };
        }
        if (path === '/checkout-quotes') return { quote_token: `quote-restaurant-${++quoteNumber}`, quote: { goods_subtotal_cents: 1000, payable_amount_cents: 1000 } };
        if (path.indexOf('/checkout') >= 0) {
          timeline.push(`checkout:${options.businessType}`);
          return { order_no: `order-${++orderNumber}`, business_type: 'restaurant', payment_status: 'paid', items: [] };
        }
        return { id: serverCart.id, items: serverCart.items.map(item => Object.assign({}, item)) };
      }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const payload = { businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', items: [{ productId: 'meal', skuId: 'meal-sku', quantity: 1 }] };
  await api.createOrder(payload);
  await api.createOrder(payload);
  const removeCalls = calls.filter(item => item.options.method === 'DELETE');
  const addCalls = calls.filter(item => item.path.endsWith('/items') && item.options.method === 'POST');
  assert.equal(removeCalls.length, 2);
  assert.equal(addCalls.length, 2);
  assert.ok(removeCalls.every(item => item.options.businessType === 'restaurant'));
  assert.deepEqual(timeline, ['remove:restaurant', 'add:restaurant', 'checkout:restaurant', 'remove:restaurant', 'add:restaurant', 'checkout:restaurant']);
  assert.equal(serverCart.items.length, 1);
  assert.equal(serverCart.items[0].quantity, 1);
});

test('history selector remains readable after a business is removed from the current session', async () => {
  const calls = [];
  const api = load('miniprogram/services/api.js', {
    './storage': { getOrders: () => [], getAddresses: () => [], saveAddresses() {}, getProducts: () => [], getStore: () => ({}) },
    './http': {
      isConfigured: () => true,
      isProduction: () => true,
      getBusinesses: () => [{ businessType: 'restaurant' }],
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => {
        calls.push({ path, options: options || {} });
        if (path === '/orders') return { data: [] };
        if (path.indexOf('/payment') >= 0) return { data: { status: 'PAID' } };
        if (path.indexOf('/orders/') >= 0) return { data: { order_no: 'NO-1', business_type: 'retail', payment_status: 'paid', items: [] } };
        if (path === '/addresses') return { data: [] };
        return { data: {} };
      }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  await api.getOrders();
  await api.getOrders('retail');
  await api.getOrder('NO-1');
  await api.createPayment('NO-1', { loginCode: 'fresh' });
  await api.queryPayment('NO-1');
  await api.createRefund('NO-1', { clientRequestId: 'refund_NO-1_initial', reason: '下单有误' });
  await api.queryRefund('NO-1');
  await api.getAddresses();
  await api.saveAddress({ addressType: 'SHIPPING', contactName: '同学', contactPhone: '13800000000' });
  await api.deleteAddress('1');
  await api.setDefaultAddress('1');
  const filtered = calls.find(item => item.path === '/orders' && item.options.businessType === 'retail');
  assert.ok(filtered);
  assert.notEqual(filtered.options.businessType, undefined);
  const refundCall = calls.find(item => item.path === '/orders/NO-1/refund-requests');
  assert.equal(refundCall.options.data.idempotency_key, 'refund_NO-1_initial');
  assert.equal(refundCall.options.data.reason, '下单有误');
  calls.filter(item => item !== filtered).forEach(item => assert.ok(!item.options.businessType, `${item.path} unexpectedly selected a business`));
});

test('legacy single-business catalog and checkout remain usable without businesses in the cached session', async () => {
  const calls = [];
  const storage = {
    getProducts: () => [{ id: 'meal', businessType: 'restaurant', skuId: 'meal-sku', skus: [{ id: 'meal-sku', status: 'active' }] }],
    getAddresses: () => [], saveProducts() {}, saveStore() {}, getStore: () => ({})
  };
  const api = load('miniprogram/services/api.js', {
    './storage': storage,
    './http': {
      isConfigured: () => true,
      isProduction: () => true,
      getBusinesses: () => [],
      ensureSession: () => Promise.resolve({ token: 'legacy' }),
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async (path, options) => {
        calls.push({ path, options: options || {} });
        if (path === '/catalog') return { data: { business_type: 'restaurant', location: { id: 11, business_type: 'restaurant', status: 'active' }, products: storage.getProducts() } };
        if (path === '/carts') return { id: 'cart-restaurant', items: [] };
        if (path === '/checkout-quotes') return { quote_token: 'quote-restaurant-legacy', quote: { goods_subtotal_cents: 1000, payable_amount_cents: 1000 } };
        if (path.indexOf('/checkout') >= 0) return { order_no: 'order-restaurant', business_type: 'restaurant', payment_status: 'paid', items: [] };
        return { id: 'cart-restaurant', items: [] };
      }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  await api.getCatalog();
  const result = await api.createOrder({ fulfillmentType: 'TAKEAWAY', items: [{ productId: 'meal', skuId: 'meal-sku', quantity: 1 }] });
  assert.equal(result.orderId, 'order-restaurant');
  assert.ok(calls.some(item => item.path === '/carts' && item.options.businessType === 'restaurant'));
});

test('restaurant storefront binding rejects a courier order in the mini program adapter', async () => {
  const api = load('miniprogram/services/api.js', {
    './storage': { getStore: () => ({}), getProducts: () => [{ id: 'cold', skuId: 'sku-cold' }], getAddresses: () => [], saveProducts() {}, saveStore() {} },
    './http': {
      isConfigured: () => true,
      createError: (code, message) => Object.assign(new Error(code), { code, userMessage: message }),
      request: async path => path === '/catalog' ? { data: { business_type: 'restaurant', location: { id: 'restaurant-location', business_type: 'restaurant', status: 'active' }, products: [{ id: 'take', sku_id: 'sku-take' }] } } : { data: { id: 'cart-1' } }
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  await api.getCatalog();
  let rejected = false;
  try { await api.createOrder({ fulfillmentType: 'COURIER', items: [{ productId: 'cold', quantity: 1 }] }); } catch (error) { rejected = String(error && (error.code || error.message || error)).indexOf('BUSINESS_BINDING_MISMATCH') >= 0; }
  assert.equal(rejected, true);
});
test('normalization preserves zero fees and current base price, filters disabled options', () => {
  const api = load('miniprogram/services/api.js', { './storage': {} }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const store = api.normalizeStore({ defaultDeliveryFee: 0, defaultMinGoodsAmount: 0 });
  assert.equal(store.defaultDeliveryFee, 0); assert.equal(store.minGoodsAmount, 0); assert.equal(store.businessStatus, 'PAUSED');
  const product = api.normalizeProduct({ basePrice: 1200, price: 1000, optionGroups: [{ id: 'off', enabled: false, options: [] }, { id: 'on', options: [{ name: '鸡蛋', priceDelta: 150 }, { name: '下架加料', enabled: false }] }] });
  assert.equal(product.price, 1200); assert.equal(product.options.length, 1); assert.equal(product.options[0].values[0], '鸡蛋 +1.5元'); assert.equal(product.options[0].values.length, 1);
});

test('product media maps cover and ordered detail images without losing legacy compatibility', () => {
  const api = load('miniprogram/services/api.js', { './storage': {} }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const product = api.normalizeProduct({
    imageFileIds: ['https://legacy.example/cover.jpg'],
    media: [
      { id: 4, kind: 'detail', url: 'https://cdn.example/detail-2.jpg', sort_order: 2 },
      { id: 2, kind: 'cover', url: 'https://cdn.example/cover.jpg', sort_order: 0 },
      { id: 3, kind: 'detail', url: 'https://cdn.example/detail-1.jpg', sort_order: 1 },
      { id: 5, kind: 'detail', url: 'https://cdn.example/detail-1.jpg', sort_order: 3 },
      { id: 6, kind: 'unknown', url: 'https://cdn.example/ignored.jpg', sort_order: 0 }
    ]
  });
  assert.equal(product.coverImageUrl, 'https://cdn.example/cover.jpg');
  assert.equal(product.detailImageUrls.join('|'), 'https://cdn.example/detail-1.jpg|https://cdn.example/detail-2.jpg');
  assert.equal(product.imageFileIds.join('|'), 'https://cdn.example/cover.jpg|https://cdn.example/detail-1.jpg|https://cdn.example/detail-2.jpg|https://legacy.example/cover.jpg');

  const legacy = api.normalizeProduct({ imageUrl: 'https://legacy.example/single.jpg' });
  assert.equal(legacy.coverImageUrl, 'https://legacy.example/single.jpg');
  assert.equal(legacy.detailImageUrls.length, 0);
});

test('storefront missing product fields receive safe display fallbacks', () => {
  const api = load('miniprogram/services/api.js', { './storage': {} }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const product = api.normalizeProduct({ id: 'missing-fields', business_type: 'restaurant', title: 'undefined', description: undefined, sold: undefined, stock: undefined, category_name: undefined, skus: [{ id: 'sku-1', status: 'active', price_cents: 1200 }] }, 0, 'restaurant');
  assert.equal(product.name, '未命名商品');
  assert.equal(product.description, '');
  assert.equal(product.sold, null);
  assert.equal(product.soldText, '销量待同步');
  assert.equal(product.stockKnown, false);
  assert.equal(product.stockText, '库存待同步');
  assert.equal(product.categoryName, '');
  const list = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/index/index.wxml'), 'utf8');
  const coldList = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/cold/index.wxml'), 'utf8');
  const detail = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/product/detail/index.wxml'), 'utf8');
  const merchantList = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/merchant/products/index.wxml'), 'utf8');
  assert.match(list, /item\.soldText \|\| '销量待同步'/);
  assert.match(coldList, /item\.soldText \|\| '销量待同步'/);
  assert.match(detail, /库存待同步/);
  assert.match(detail, /wx:if="\{\{product\.description\}\}"/);
  assert.match(merchantList, /item\.soldText \|\| '销量待同步'/);
  assert.doesNotMatch(merchantList, /item\.sold\}\}/);
});
test('production catalog derives sellability from active binding and location', () => {
  const api = load('miniprogram/services/api.js', { './storage': {} }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  const active = api.normalizeCatalog({ business_type: 'restaurant', location: { id: 12, business_type: 'restaurant', status: 'active' }, products: [{ id: 7, business_type: 'restaurant', status: 'online', category_name: '盖饭', skus: [{ id: 71, name: '标准份', status: 'active', price_cents: 1800 }], option_groups: [{ id: 8, name: '口味', required: true, options: [{ id: 81, name: '微辣', status: 'active', price_delta_cents: 0 }] }] }] });
  assert.equal(active.store.catalogOpen, true); assert.equal(active.store.businessStatus, 'OPEN');
  assert.equal(active.categories.some(item => item.name === '盖饭'), true); assert.equal(active.products[0].catalogReady, true); assert.equal(active.products[0].skuId, 71); assert.equal(active.products[0].options[0].optionRecords[0].id, 81);
  const inactive = api.normalizeCatalog({ business_type: 'restaurant', location: { id: 12, business_type: 'restaurant', status: 'inactive' }, products: [] });
  assert.equal(inactive.store.catalogOpen, false); assert.equal(inactive.store.businessStatus, 'PAUSED');
});
test('catalog page uses derived storefront availability instead of legacy status fields', () => {
  const createCatalogPage = load('miniprogram/services/catalog-page.js', { './api': { isProduction: () => true }, './storage': {} });
  const page = createCatalogPage('TAKEAWAY');
  assert.equal(page.isChannelOpen({ activeBusinessType: 'restaurant', catalogOpen: true, businessStatus: 'PAUSED' }), true);
  assert.equal(page.isChannelOpen({ activeBusinessType: 'restaurant', catalogOpen: false, businessStatus: 'OPEN' }), false);
  assert.equal(page.isChannelOpen({ activeBusinessType: 'retail', catalogOpen: true, courierStatus: 'OPEN' }), false);
});
test('production unsupported storefront helpers fail closed without local data', async () => {
  const api = load('miniprogram/services/api.js', { './storage': { getCoupons: () => [{ id: 'local' }] } }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
  await assert.rejects(api.getDeliveryZones(), error => error.code === 'DELIVERY_ZONES_UNAVAILABLE');
  assert.throws(() => api.getCoupons(), error => error.code === 'BUSINESS_SELECTOR_INVALID');
});
test('production storage cannot load demo orders, addresses or mock products', () => {
  const values = new Map([['food_orders_v1', [{ id: 'demo' }]]]); let mode = 'production';
  const storage = load('miniprogram/services/storage.js', {}, { getApp: () => ({ globalData: { deploymentMode: mode, env: 'env' } }), wx: { getStorageSync: key => values.get(key), setStorageSync: (key, value) => values.set(key, value) } });
  assert.equal(storage.getOrders().length, 0); assert.equal(storage.getAddresses().length, 0); assert.equal(storage.getProducts().length, 0); assert.equal(storage.getStore().businessStatus, 'PAUSED');
  storage.saveOrders([{ id: 'real' }]); mode = 'demo'; assert.equal(storage.getOrders()[0].id, 'demo');
});

test('production checkout keeps an unknown attempt and retries the original payload when the product changes', async () => {
  let page;
  let attempt = null;
  let createCount = 0;
  const payloads = [];
  let initialUnknownMarker;
  let modal;
  let retryPromise;
  const storage = {
    makeId: prefix => `${prefix}_current`,
    getCheckoutBatchAttempt: () => attempt,
    saveCheckoutBatchAttempt: value => { attempt = value; },
    saveCart() {}
  };
  const api = {
    isProduction: () => true,
    isBusinessAvailable: () => true,
    createOrder: async payload => {
      initialUnknownMarker = attempt && attempt.creationResultUnknown;
      payloads.push(payload);
      createCount += 1;
      if (createCount === 1) throw new Error('network');
      return { success: true, orderId: 'NEW-1', order: { status: 'PAID' } };
    }
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': api,
    '../../services/payment': { payOrder: async () => ({ status: 'PAID' }), isSettled: status => status === 'PAID' },
    '../../config/commerce': { FULFILLMENT: { TAKEAWAY: 'TAKEAWAY', COURIER: 'COURIER' }, businessTypeForFulfillment: type => type === 'COURIER' ? 'retail' : 'restaurant' }
  }, { Page: value => { page = value; }, wx: { showToast() {}, showModal: options => { modal = options; }, redirectTo() {} } });
  page.batchId = 'batch_current';
  page.data = Object.assign({}, page.data, {
    takeawayItems: [{ productId: 'old-product', skuId: 'old-sku', quantity: 1, unitPrice: 1000, selectedOptions: [] }],
    courierItems: [], takeawayDeliveryMethod: 'PICKUP', takeawayAddress: null, courierAddress: null,
    customerRemark: '', courierRemark: '', couponTarget: 'TAKEAWAY', selectedCoupon: null, paying: false
  });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  const submit = page.submitProductionOrder;
  page.submitProductionOrder = retryOriginal => {
    const result = submit.call(page, retryOriginal);
    if (retryOriginal === true) retryPromise = result;
    return result;
  };
  await page.submitProductionOrder();
  assert.equal(payloads.length, 1);
  assert.equal(initialUnknownMarker, false);
  assert.equal(payloads[0].items[0].productId, 'old-product');
  assert.ok(attempt && attempt.fingerprint);
  assert.equal(attempt.creationResultUnknown, true);
  page.data.takeawayItems = [{ productId: 'new-product', skuId: 'new-sku', quantity: 1, unitPrice: 1200, selectedOptions: [] }];
  await page.submitProductionOrder();
  assert.equal(modal.confirmText, '重试原单');
  modal.success({ confirm: true });
  await retryPromise;
  assert.equal(payloads.length, 2);
  assert.equal(payloads[1].items[0].productId, 'old-product');
  assert.equal(attempt, null);
});

test('a fresh explicit checkout rejection clears only the direct draft attempt so edited goods can retry', async () => {
  let page;
  let draft = { id: 'direct-1', batchId: 'batch-old', items: [{ productId: 'old-product' }], attempt: null };
  const payloads = [];
  let createCount = 0;
  const storage = {
    makeId: prefix => `${prefix}_fresh`,
    getDirectCheckout: () => draft,
    saveDirectCheckout: value => { draft = value; },
    saveCart() {}
  };
  const api = {
    isProduction: () => true,
    createOrder: async payload => {
      payloads.push(payload);
      createCount += 1;
      if (createCount === 1) throw Object.assign(new Error('HTTP_400'), { statusCode: 400, reasonCode: 'ZERO_PAY_NOT_SUPPORTED', userMessage: '优惠后金额为零' });
      return { success: true, orderId: 'NEW-EDITED', order: { status: 'PAID' } };
    }
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': api,
    '../../services/payment': { payOrder: async () => ({ status: 'PAID' }), isSettled: status => status === 'PAID' },
    '../../config/commerce': { FULFILLMENT: { TAKEAWAY: 'TAKEAWAY', COURIER: 'COURIER' }, businessTypeForFulfillment: type => type === 'COURIER' ? 'retail' : 'restaurant' }
  }, { Page: value => { page = value; }, wx: { showToast() {}, redirectTo() {} } });
  page.directCheckoutId = 'direct-1';
  page.batchId = 'batch-old';
  page.data = Object.assign({}, page.data, {
    takeawayItems: [{ productId: 'old-product', skuId: 'old-sku', quantity: 1, unitPrice: 1000, selectedOptions: [] }],
    courierItems: [], takeawayDeliveryMethod: 'PICKUP', takeawayAddress: null, courierAddress: null,
    customerRemark: '', courierRemark: '', couponTarget: 'TAKEAWAY', selectedCoupon: null, paying: false
  });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  await page.submitProductionOrder();
  assert.equal(draft.attempt, null);

  page.data.takeawayItems = [{ productId: 'new-product', skuId: 'new-sku', quantity: 1, unitPrice: 1200, selectedOptions: [] }];
  await page.submitProductionOrder();
  assert.equal(payloads.length, 2);
  assert.equal(payloads[0].items[0].productId, 'old-product');
  assert.equal(payloads[1].items[0].productId, 'new-product');
  assert.equal(draft, null);
});

test('an unknown checkout result stays sticky when a retry returns an explicit rejection', async () => {
  let page;
  let attempt = null;
  let createCount = 0;
  const storage = {
    makeId: prefix => `${prefix}_sticky`,
    getCheckoutBatchAttempt: () => attempt,
    saveCheckoutBatchAttempt: value => { attempt = value; },
    saveCart() {}
  };
  const api = {
    isProduction: () => true,
    createOrder: async () => {
      createCount += 1;
      if (createCount === 1) throw Object.assign(new Error('network'), { code: 'NETWORK_ERROR' });
      throw Object.assign(new Error('HTTP_400'), { statusCode: 400, reasonCode: 'ZERO_PAY_NOT_SUPPORTED' });
    }
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': api,
    '../../services/payment': { payOrder: async () => ({ status: 'PAID' }), isSettled: status => status === 'PAID' },
    '../../config/commerce': { FULFILLMENT: { TAKEAWAY: 'TAKEAWAY', COURIER: 'COURIER' }, businessTypeForFulfillment: type => type === 'COURIER' ? 'retail' : 'restaurant' }
  }, { Page: value => { page = value; }, wx: { showToast() {}, showModal() {}, redirectTo() {} } });
  page.batchId = 'batch-sticky';
  page.data = Object.assign({}, page.data, {
    takeawayItems: [{ productId: 'same-product', skuId: 'same-sku', quantity: 1, unitPrice: 1000, selectedOptions: [] }],
    courierItems: [], takeawayDeliveryMethod: 'PICKUP', takeawayAddress: null, courierAddress: null,
    customerRemark: '', courierRemark: '', couponTarget: 'TAKEAWAY', selectedCoupon: null, paying: false
  });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  await page.submitProductionOrder();
  const originalAttempt = attempt;
  assert.equal(originalAttempt.creationResultUnknown, true);
  await page.submitProductionOrder();
  assert.equal(createCount, 2);
  assert.equal(attempt, originalAttempt);
  assert.equal(attempt.creationResultUnknown, true);
});

test('a legacy checkout attempt without a result marker stays sticky after an explicit rejection', async () => {
  let page;
  let attempt = null;
  let createCount = 0;
  const storage = {
    makeId: prefix => `${prefix}_legacy`,
    getCheckoutBatchAttempt: () => attempt,
    saveCheckoutBatchAttempt: value => { attempt = value; },
    saveCart() {}
  };
  const api = {
    isProduction: () => true,
    createOrder: async () => {
      createCount += 1;
      if (createCount === 1) throw Object.assign(new Error('network'), { code: 'NETWORK_ERROR' });
      throw Object.assign(new Error('HTTP_400'), { statusCode: 400, reasonCode: 'ZERO_PAY_NOT_SUPPORTED' });
    }
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': api,
    '../../services/payment': { payOrder: async () => ({ status: 'PAID' }), isSettled: status => status === 'PAID' },
    '../../config/commerce': { FULFILLMENT: { TAKEAWAY: 'TAKEAWAY', COURIER: 'COURIER' }, businessTypeForFulfillment: type => type === 'COURIER' ? 'retail' : 'restaurant' }
  }, { Page: value => { page = value; }, wx: { showToast() {}, redirectTo() {} } });
  page.batchId = 'batch-legacy';
  page.data = Object.assign({}, page.data, {
    takeawayItems: [{ productId: 'legacy-product', skuId: 'legacy-sku', quantity: 1, unitPrice: 1000, selectedOptions: [] }],
    courierItems: [], takeawayDeliveryMethod: 'PICKUP', takeawayAddress: null, courierAddress: null,
    customerRemark: '', courierRemark: '', couponTarget: 'TAKEAWAY', selectedCoupon: null, paying: false
  });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  await page.submitProductionOrder();
  assert.equal(attempt.creationResultUnknown, true);
  delete attempt.creationResultUnknown;
  const legacyAttempt = attempt;
  assert.equal(Object.prototype.hasOwnProperty.call(legacyAttempt, 'creationResultUnknown'), false);
  await page.submitProductionOrder();
  assert.equal(createCount, 2);
  assert.equal(attempt, legacyAttempt);
  assert.equal(Object.prototype.hasOwnProperty.call(attempt, 'creationResultUnknown'), false);
});

test('an idempotency conflict never replaces the pending checkout attempt with a new order', async () => {
  let page;
  let attempt = null;
  let createCount = 0;
  const storage = {
    makeId: prefix => `${prefix}_conflict`,
    getCheckoutBatchAttempt: () => attempt,
    saveCheckoutBatchAttempt: value => { attempt = value; },
    saveCart() {}
  };
  const api = {
    isProduction: () => true,
    createOrder: async () => {
      createCount += 1;
      throw Object.assign(new Error('HTTP_409'), { statusCode: 409, reasonCode: 'IDEMPOTENCY_CONFLICT' });
    }
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': api,
    '../../services/payment': { payOrder: async () => ({ status: 'PAID' }), isSettled: status => status === 'PAID' },
    '../../config/commerce': { FULFILLMENT: { TAKEAWAY: 'TAKEAWAY', COURIER: 'COURIER' }, businessTypeForFulfillment: type => type === 'COURIER' ? 'retail' : 'restaurant' }
  }, { Page: value => { page = value; }, wx: { showToast() {}, showModal() {}, redirectTo() {} } });
  page.batchId = 'batch-conflict';
  page.data = Object.assign({}, page.data, {
    takeawayItems: [{ productId: 'original-product', skuId: 'original-sku', quantity: 1, unitPrice: 1000, selectedOptions: [] }],
    courierItems: [], takeawayDeliveryMethod: 'PICKUP', takeawayAddress: null, courierAddress: null,
    customerRemark: '', courierRemark: '', couponTarget: 'TAKEAWAY', selectedCoupon: null, paying: false
  });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  await page.submitProductionOrder();
  const originalAttempt = attempt;
  page.data.takeawayItems = [{ productId: 'edited-product', skuId: 'edited-sku', quantity: 1, unitPrice: 1200, selectedOptions: [] }];
  await page.submitProductionOrder();
  assert.equal(createCount, 1);
  assert.equal(attempt, originalAttempt);
  assert.equal(attempt.creationResultUnknown, true);
});

test('production checkout does not create a new order when an older attempt has an order ID', async () => {
  let page;
  const oldAttempt = { fingerprint: 'old-fingerprint', attempts: [{ key: 'restaurant', businessType: 'restaurant', payload: { businessType: 'restaurant' }, orderId: 'OLD-1', status: 'WAIT_PAY' }] };
  let createCount = 0;
  const storage = {
    getCheckoutBatchAttempt: () => oldAttempt,
    saveCheckoutBatchAttempt: () => { throw new Error('attempt should not be replaced'); },
    saveCart() {},
    makeId: prefix => `${prefix}_current`
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': { isProduction: () => true, createOrder: async () => { createCount += 1; return { success: true, orderId: 'NEW-1' }; } },
    '../../services/payment': { payOrder: async () => ({ status: 'PAID' }), isSettled: status => status === 'PAID' },
    '../../config/commerce': { FULFILLMENT: { TAKEAWAY: 'TAKEAWAY', COURIER: 'COURIER' }, businessTypeForFulfillment: type => type === 'COURIER' ? 'retail' : 'restaurant' }
  }, { Page: value => { page = value; }, wx: { showToast() {}, redirectTo() {} } });
  page.batchId = 'batch_current';
  page.data = Object.assign({}, page.data, {
    takeawayItems: [{ productId: 'new-product', skuId: 'new-sku', quantity: 1, unitPrice: 1200, selectedOptions: [] }],
    courierItems: [], takeawayDeliveryMethod: 'PICKUP', takeawayAddress: null, courierAddress: null,
    customerRemark: '', courierRemark: '', couponTarget: 'TAKEAWAY', selectedCoupon: null, paying: false
  });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  let opened;
  page.openCreatedOrder = orderId => { opened = orderId; };
  await page.submitProductionOrder();
  assert.equal(createCount, 0);
  assert.equal(opened, 'OLD-1');
  assert.equal(page.data.paying, false);
});

test('production checkout explains missing delivery configuration instead of asking for hidden choices', () => {
  let page;
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': {},
    '../../services/api': { isProduction: () => true },
    '../../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    '../../services/payment': {}
  }, { Page: value => { page = value; }, wx: {} });
  page.data = Object.assign({}, page.data, {
    store: {},
    hasTakeaway: true,
    hasCourier: false,
    takeawayItems: [{ productId: 'meal-1' }],
    courierItems: [],
    takeawayDeliveryMethod: 'DELIVERY',
    takeawayAddress: { addressType: 'DELIVERY', province: '省', city: '市', district: '区', detailAddress: '路1号' },
    restaurantZones: [],
    restaurantZoneIndex: -1,
    restaurantSlots: [],
    restaurantSlotIndex: -1,
    restaurantSlotDate: ''
  });
  assert.equal(page.validateBeforeSubmit(), '门店暂未配置配送区域，请联系商家');
  page.data.restaurantZones = [{ id: 'zone-1' }];
  page.data.restaurantZoneIndex = 0;
  assert.equal(page.validateBeforeSubmit(), '门店暂未配置配送时段，请联系商家');
  page.data.restaurantSlots = [{ id: 'slot-1' }];
  page.data.restaurantSlotIndex = 0;
  page.data.restaurantSlotDate = '2026-10-04';
  page.data.store = null;
  assert.equal(page.validateBeforeSubmit(), '店铺信息加载中');
});

test('checkout matches delivery area from the address instead of defaulting to the first area', () => {
  let page;
  const storage = {
    getStore: () => ({}),
    getProducts: () => [],
    getCart: () => [{ productId: 'meal-1', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', unitPrice: 1000, quantity: 1 }]
  };
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../services/api': { isProduction: () => true, normalizeStore: value => value },
    '../../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    '../../services/payment': {}
  }, { Page: value => { page = value; }, wx: { getStorageSync: () => '' } });
  page.data = Object.assign({}, page.data, { takeawayDeliveryMethod: 'DELIVERY', couponTarget: 'TAKEAWAY' });
  page.setData = (change, callback) => { page.data = Object.assign({}, page.data, change); if (callback) callback(); };
  const options = {
    restaurant: {
      config: {},
      zones: [
        { id: 'zone-other', name: '其他区域', province: '省', city: '市', district: '其他区' },
        { id: 'zone-match', name: '当前区域', province: '省', city: '市', district: '区' }
      ],
      slots: [{ id: 'slot-match', zoneId: 'zone-match', dayOfWeek: new Date().getDay(), startMinute: 0, endMinute: 1439 }]
    },
    retail: null
  };
  const address = { id: 'address-1', addressType: 'DELIVERY', contactName: '顾客', contactPhone: '13800000000', province: '省', city: '市', district: '区', detailAddress: '路1号' };
  page.applyData([address], [], options);
  assert.equal(page.data.restaurantZoneIndex, 1);
  assert.equal(page.data.restaurantSlots[0].id, 'slot-match');
  page.applyData([Object.assign({}, address, { district: '未覆盖区' })], [], options);
  assert.equal(page.data.restaurantZoneIndex, -1);
  assert.equal(page.data.restaurantSlots.length, 0);
});

test('checkout pre-order contact card opens without an order-completion prompt', async () => {
  let page;
  const checkoutWxml = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/checkout/index.wxml'), 'utf8');
  assert.doesNotMatch(checkoutWxml, /测试联系商家弹窗/);
  assert.doesNotMatch(checkoutWxml, /查看订单/);
  assert.match(checkoutWxml, /加店家微信/);
  let previewed = '';
  let copied = '';
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': {},
    '../../services/api': {
      isProduction: () => true,
      getStorefrontContact: async () => ({ available: true, contactName: '门店客服', wechatId: 'shop-service', qrCodeUrl: 'https://example.com/contact.png' })
    },
    '../../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    '../../services/payment': {}
  }, { Page: value => { page = value; }, wx: { previewImage: ({ current }) => { previewed = current; }, setClipboardData: ({ data, success }) => { copied = data; success(); }, showToast() {} } });
  page.setData = (change, callback) => { page.data = Object.assign({}, page.data, change); if (callback) callback(); };
  page.data.preOrderContact = { available: true, contactName: '门店客服', wechatId: 'shop-service', qrCodeUrl: 'https://example.com/contact.png' };
  page.openPreOrderContact();
  assert.equal(page.data.showPreOrderContact, true);
  page.previewPreOrderContactQRCode();
  assert.equal(previewed, 'https://example.com/contact.png');
  page.copyPreOrderContactWechat();
  assert.equal(copied, 'shop-service');
  page.closePreOrderContact();
  assert.equal(page.data.preOrderContactClosing, true);
  await new Promise(resolve => setTimeout(resolve, 240));
  assert.equal(page.data.preOrderContactClosing, false);
  assert.equal(page.data.showPreOrderContact, false);
});

test('product detail does not substitute the first product for a missing ID', () => {
  let page; let result;
  load('miniprogram/pages/product/detail/index.js', { '../../../services/storage': { getProducts: () => [{ id: 'other', isOnSale: true }] }, '../../../services/api': { isProduction: () => false } }, { Page: value => { page = value; }, wx: { showToast() {} } });
  page.setData = change => { result = change; }; page.onLoad({ id: 'missing' });
  assert.equal(result.unavailable, true); assert.equal(result.product.id, undefined);
});

test('product detail matches numeric API IDs against string route parameters', () => {
  let page; let result;
  const product = { id: 7, isOnSale: true, catalogReady: true, skuId: 71, sku: { id: 71, priceCents: 1200 }, activeSkus: [{ id: 71, priceCents: 1200 }], options: [], fulfillmentType: 'TAKEAWAY', businessType: 'restaurant', price: 1200, originalPrice: 1200, stockMode: 'LIMITED', stock: 5 };
  load('miniprogram/pages/product/detail/index.js', {
    '../../../services/storage': { getProducts: () => [product], getStore: () => ({ businessStatus: 'OPEN' }) },
    '../../../services/api': { isProduction: () => false, normalizeProduct: value => value, normalizeStore: value => value },
    '../../../utils/format': { yuan: value => (Number(value) / 100).toFixed(2) },
    '../../../config/brand': {},
    '../../../config/commerce': { FULFILLMENT: { COURIER: 'COURIER' }, fulfillmentOf: value => value.fulfillmentType, businessTypeOf: value => value.businessType }
  }, { Page: value => { page = value; }, wx: { showToast() {} } });
  page.data = Object.assign({}, page.data);
  page.setData = change => { page.data = Object.assign({}, page.data, change); result = page.data; };
  page.onLoad({ id: '7' });
  assert.equal(result.product.id, 7);
  assert.equal(result.unavailable, false);
});

test('merchant product editor loads and saves numeric product and category IDs', async () => {
  let page;
  let savedProducts;
  const products = [{
    id: 7,
    categoryId: 21,
    fulfillmentType: 'TAKEAWAY',
    name: '招牌鸡腿饭',
    description: '现做套餐',
    basePrice: 1800,
    originalPrice: 2200,
    stock: 8,
    optionGroups: [{ id: 31, name: '辣度', required: true, options: [{ id: 311, name: '微辣', priceDelta: 0 }] }]
  }];
  const storage = {
    getProducts: () => products,
    saveProducts: value => { savedProducts = value; },
    makeId: prefix => `${prefix}_new`
  };
  load('miniprogram/pages/merchant/product-edit/index.js', {
    '../../../services/storage': storage,
    '../../../services/api': { isProduction: () => false },
    '../../../data/mock': { categories: [{ id: 'all', name: '全部' }, { id: 20, name: '小吃' }, { id: 21, name: '套餐' }] }
  }, {
    Page: value => { page = value; },
    wx: { showToast() {}, navigateBack() {} }
  });
  page.data = Object.assign({}, page.data);
  page.setData = change => { page.data = Object.assign({}, page.data, change); };

  page.onLoad({ id: '7' });

  assert.equal(page.data.form.name, '招牌鸡腿饭');
  assert.equal(page.data.form.optionGroups[0].name, '辣度');
  assert.equal(page.data.categoryIndex, 1);

  await page.saveProduct();

  assert.equal(savedProducts.length, 1);
  assert.equal(savedProducts[0].id, 7);
  assert.equal(savedProducts[0].optionGroups[0].options[0].name, '微辣');
});

test('demo mixed checkout binds the coupon to its actual fulfillment order', async () => {
  const values = { cart: [], orders: [], products: [{ id: 'take', stockMode: 'LIMITED', stock: 5 }, { id: 'cold', stockMode: 'LIMITED', stock: 5 }], coupons: [{ id: 'coupon', status: 'AVAILABLE' }] };
  const storage = { getCart: () => values.cart, saveCart: value => { values.cart = value; }, getOrders: () => values.orders, saveOrders: value => { values.orders = value; }, getProducts: () => values.products, saveProducts: value => { values.products = value; }, getCoupons: () => values.coupons, saveCoupons: value => { values.coupons = value; }, getCheckoutBatchAttempt: () => null, saveCheckoutBatchAttempt() {}, makeId: prefix => `${prefix}_id`, getStore: () => ({}) };
  let page;
  load('miniprogram/pages/checkout/index.js', { '../../services/storage': storage, '../../services/api': { isProduction: () => false, normalizeStore: value => value }, '../../services/payment': { isSettled: status => ['PAID', 'COMPLETED'].includes(status) } }, { Page: value => { page = value; }, wx: { getStorageSync: () => '', showToast() {}, redirectTo() {} }, setTimeout, getApp: () => ({ globalData: { deploymentMode: 'demo' } }) });
  page.data = Object.assign({}, page.data, { hasTakeaway: true, hasCourier: true, takeawayItems: [{ productId: 'take', name: '便当', unitPrice: 1000, quantity: 1, fulfillmentType: 'TAKEAWAY' }], courierItems: [{ productId: 'cold', name: '冷吃兔', unitPrice: 2000, quantity: 1, fulfillmentType: 'COURIER' }], takeawayDeliveryMethod: 'PICKUP', takeawayDeliveryFee: 0, courierShippingFee: 800, selectedCoupon: { id: 'coupon', name: '券', scope: 'COURIER' }, couponTarget: 'COURIER', discount: 300, customerRemark: '', courierRemark: '', paying: false });
  page.batchId = 'batch_id'; page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.submitDemoOrder();
  await new Promise(resolve => setTimeout(resolve, 550));
  const couponOrder = values.orders.find(order => order.couponSnapshot);
  assert.equal(couponOrder.fulfillmentType, 'COURIER'); assert.equal(values.coupons[0].usedOrderId, couponOrder.id); assert.equal(values.orders.length, 2);
});

test('merchant notice action requests the configured subscription template', () => {
  let page; let request;
  load('miniprogram/pages/merchant/index/index.js', {
    '../../../services/storage': {},
    '../../../services/api': {},
    '../../../config/notifications': { orderNoticeTemplateId: 'notice-template' }
  }, {
    Page: value => { page = value; },
    wx: { requestSubscribeMessage: options => { request = options; options.success({ 'notice-template': 'accept' }); }, showToast() {} }
  });
  page.requestOrderNotice();
  assert.equal(request.tmplIds.length, 1);
  assert.equal(request.tmplIds[0], 'notice-template');
});

test('catalog quick add stores the product cover instead of an arbitrary image entry', () => {
  const cart = [];
  let savedCart;
  const createCatalogPage = load('miniprogram/services/catalog-page.js', {
    '../config/brand': {},
    '../data/mock': { categories: [] },
    './storage': {
      getCart: () => cart,
      saveCart: value => { savedCart = value; },
    },
    '../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    './api': { isProduction: () => false },
  }, { wx: { showToast() {} } });
  const page = createCatalogPage('TAKEAWAY');
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.data.channelOpen = true;
  page.catalogProducts = [{
    id: 'meal-1',
    name: '套餐',
    businessType: 'restaurant',
    fulfillmentType: 'TAKEAWAY',
    coverImageUrl: 'https://cdn.example/cover.jpg',
    imageFileIds: ['https://cdn.example/detail.jpg'],
    stockMode: 'UNLIMITED',
    stock: 99,
    isOnSale: true,
    isSoldOut: false,
    price: 1200,
  }];
  page.addQuick({ currentTarget: { dataset: { id: 'meal-1' } } });
  assert.equal(savedCart[0].coverImageUrl, 'https://cdn.example/cover.jpg');
  assert.notEqual(savedCart[0].coverImageUrl, 'https://cdn.example/detail.jpg');
});

test('catalog quick add refreshes a changed price before increasing quantity', () => {
  const cart = [{ cartKey: 'restaurant_meal-1__default', productId: 'meal-1', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', unitPrice: 10000, quantity: 1 }];
  let savedCart;
  const createCatalogPage = load('miniprogram/services/catalog-page.js', {
    '../config/brand': {},
    '../data/mock': { categories: [] },
    './storage': { getCart: () => cart, saveCart: value => { savedCart = value; } },
    '../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    './api': { isProduction: () => false },
  }, { wx: { showToast() {} } });
  const page = createCatalogPage('TAKEAWAY');
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.data.channelOpen = true;
  page.catalogProducts = [{ id: 'meal-1', name: '套餐', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', stockMode: 'UNLIMITED', stock: 99, isOnSale: true, isSoldOut: false, price: 100 }];
  page.addQuick({ currentTarget: { dataset: { id: 'meal-1' } } });
  assert.equal(savedCart[0].unitPrice, 100);
  assert.equal(savedCart[0].quantity, 2);
});

test('order normalization uses immutable media_snapshot cover and never promotes detail media', () => {
  const api = load('miniprogram/services/api.js', {
    './storage': {},
    './http': { isProduction: () => false, isConfigured: () => false, createError: (code, message) => Object.assign(new Error(message), { code }) },
  });
  const order = api.normalizeOrder({
    order_no: 'ORDER-MEDIA-1',
    business_type: 'restaurant',
    items: [{
      product_id: 7,
      quantity: 1,
      unit_price_cents: 1200,
      line_amount_cents: 1200,
      media_snapshot: JSON.stringify([
        { kind: 'detail', url: 'https://cdn.example/detail-first.jpg', sort_order: 0 },
        { kind: 'cover', url: 'https://cdn.example/cover-late.jpg', sort_order: 5 },
        { kind: 'cover', url: 'https://cdn.example/cover-order-time.jpg', sort_order: 1 },
        { kind: 'detail', url: 'https://cdn.example/cover-order-time.jpg', sort_order: 6 },
      ]),
    }, {
      product_id: 8,
      quantity: 1,
      unit_price_cents: 800,
      line_amount_cents: 800,
      MediaSnapshotJSON: JSON.stringify([{ kind: 'cover', url: 'https://cdn.example/legacy-cover.jpg', sort_order: 0 }]),
    }],
  });
  assert.equal(order.itemsSnapshot[0].coverImageUrl, 'https://cdn.example/cover-order-time.jpg');
  assert.equal(order.itemsSnapshot[0].detailImageUrls.join('|'), 'https://cdn.example/detail-first.jpg');
  assert.equal(order.itemsSnapshot[1].coverImageUrl, 'https://cdn.example/legacy-cover.jpg');
});

test('cart and checkout backfill missing legacy images from the current catalog without saving history', () => {
  const cart = [{ cartKey: 'restaurant_meal-1_default', productId: 'meal-1', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', name: '套餐', unitPrice: 1200, quantity: 1 }];
  let saveCartCalls = 0;
  const product = { id: 'meal-1', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', coverImageUrl: 'https://cdn.example/current-cover.jpg', stockMode: 'UNLIMITED', stock: 99 };
  const store = { businessStatus: 'OPEN', courierStatus: 'OPEN', catalogOpen: true, minGoodsAmount: 0, courierMinGoodsAmount: 0, defaultDeliveryFee: 0, defaultShippingFee: 0, freeShippingThreshold: 0 };
  const storage = {
    getCart: () => cart,
    saveCart: () => { saveCartCalls += 1; },
    getProducts: () => [product],
    getStore: () => store,
  };
  let cartPage;
  load('miniprogram/pages/cart/index.js', {
    '../../services/storage': storage,
    '../../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    '../../services/api': { isProduction: () => false, isBusinessAvailable: () => true, normalizeStore: value => value },
  }, { Page: value => { cartPage = value; }, wx: {} });
  cartPage.setData = change => { cartPage.data = Object.assign({}, cartPage.data, change); };
  cartPage.loadCart();
  assert.equal(cartPage.data.cart[0].coverImageUrl, 'https://cdn.example/current-cover.jpg');
  assert.equal(cart[0].coverImageUrl, undefined);
  assert.equal(saveCartCalls, 0);

  let checkoutPage;
  load('miniprogram/pages/checkout/index.js', {
    '../../services/storage': storage,
    '../../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    '../../services/api': { isProduction: () => false, normalizeStore: value => value },
  }, { Page: value => { checkoutPage = value; }, wx: { getStorageSync: () => '' } });
  checkoutPage.setData = (change, callback) => { checkoutPage.data = Object.assign({}, checkoutPage.data, change); if (callback) callback(); };
  checkoutPage.applyData([], []);
  assert.equal(checkoutPage.data.takeawayItems[0].coverImageUrl, 'https://cdn.example/current-cover.jpg');
  assert.equal(cart[0].coverImageUrl, undefined);
});

test('cart refreshes a stored line price from the current catalog', () => {
  const cart = [{ cartKey: 'restaurant_meal-1__default', productId: 'meal-1', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', unitPrice: 10000, quantity: 2 }];
  let savedCart;
  const storage = {
    getCart: () => cart,
    saveCart: value => { savedCart = value; },
    getProducts: () => [{ id: 'meal-1', businessType: 'restaurant', fulfillmentType: 'TAKEAWAY', price: 100, stockMode: 'UNLIMITED', stock: 99 }],
    getStore: () => ({ businessStatus: 'OPEN', courierStatus: 'OPEN', catalogOpen: true })
  };
  let cartPage;
  load('miniprogram/pages/cart/index.js', {
    '../../services/storage': storage,
    '../../utils/format': { yuan: value => String(Number(value || 0) / 100) },
    '../../services/api': { isProduction: () => false, isBusinessAvailable: () => true, normalizeStore: value => value },
  }, { Page: value => { cartPage = value; }, wx: {} });
  cartPage.setData = change => { cartPage.data = Object.assign({}, cartPage.data, change); };
  cartPage.loadCart();
  assert.equal(savedCart[0].unitPrice, 100);
  assert.equal(cartPage.data.cart[0].subtotalText, '2');
});

test('rebuy keeps the order-time cover and order surfaces render stable cover thumbnails', () => {
  let detailPage; let savedCart; let navigated;
  load('miniprogram/pages/order/detail/index.js', {
    '../../../services/storage': { getCart: () => [], saveCart: value => { savedCart = value; }, getStore: () => ({}) },
    '../../../utils/format': { orderStatus: value => value, dateTime: () => '', yuan: () => '0.00' },
    '../../../services/api': { isProduction: () => false },
    '../../../services/payment': {},
  }, { Page: value => { detailPage = value; }, wx: { navigateTo: ({ url }) => { navigated = url; } } });
  detailPage.data.order = {
    businessType: 'restaurant',
    fulfillmentType: 'TAKEAWAY',
    itemsSnapshot: [{ productId: 'meal-1', name: '套餐', coverImageUrl: 'https://cdn.example/order-cover.jpg', imageFileId: 'https://cdn.example/detail.jpg', unitPrice: 1200, quantity: 1 }],
  };
  detailPage.reorder();
  assert.equal(savedCart[0].coverImageUrl, 'https://cdn.example/order-cover.jpg');
  assert.notEqual(savedCart[0].coverImageUrl, 'https://cdn.example/detail.jpg');
  assert.equal(navigated, '/pages/cart/index');

  const list = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/order/list/index.wxml'), 'utf8');
  const detail = fs.readFileSync(path.resolve(__dirname, '..', 'miniprogram/pages/order/detail/index.wxml'), 'utf8');
  for (const source of [list, detail]) {
    assert.match(source, /coverImageUrl/);
    assert.match(source, /mode="aspectFill"/);
    assert.match(source, /wx:else/);
  }
  assert.doesNotMatch(list, /detailImageUrls/);
  assert.doesNotMatch(detail, /detailImageUrls/);
});

test('refund eligibility requires a paid order with business-appropriate pending fulfillment', () => {
  let page;
  let production = true;
  load('miniprogram/pages/order/detail/index.js', {
    '../../../services/storage': { getOrders: () => [], getCart: () => [], getStore: () => ({}) },
    '../../../utils/format': { orderStatus: value => value, dateTime: () => '', yuan: value => String(Number(value || 0) / 100) },
    '../../../services/api': { isProduction: () => production, normalizeOrder: value => value },
    '../../../services/payment': {}
  }, { Page: value => { page = value; }, wx: {} });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  const base = { id: 'ORDER-REFUND-1', orderNo: 'ORDER-REFUND-1', status: 'PREPARING', paymentStatus: 'paid', refundStatus: '', fulfillmentStatus: 'pending_acceptance', fulfillmentType: 'TAKEAWAY', businessType: 'restaurant', itemsSnapshot: [] };

  page.renderOrder(base);
  assert.equal(page.data.canRequestRefund, false);
  page.renderOrder(Object.assign({}, base, { available_actions: { can_refund: false, reason: '商家已接单，暂不可退款' } }));
  assert.equal(page.data.canRequestRefund, false);
  page.renderOrder(Object.assign({}, base, { available_actions: { can_refund: true } }));
  assert.equal(page.data.canRequestRefund, true);
  page.renderOrder(Object.assign({}, base, { paymentStatus: 'unpaid', available_actions: { can_refund: false } }));
  assert.equal(page.data.canRequestRefund, false);

  page.renderOrder(Object.assign({}, base, { fulfillmentStatus: 'preparing', available_actions: { can_refund: false } }));
  assert.equal(page.data.canRequestRefund, false);
  page.renderOrder(Object.assign({}, base, { paymentStatus: 'unpaid', available_actions: { can_refund: false } }));
  assert.equal(page.data.canRequestRefund, false);
  page.renderOrder(Object.assign({}, base, { refundStatus: 'requested', available_actions: { can_refund: false } }));
  assert.equal(page.data.canRequestRefund, false);
  page.renderOrder(Object.assign({}, base, { businessType: 'restaurant', fulfillmentStatus: 'pending_shipment', available_actions: { can_refund: false } }));
  assert.equal(page.data.canRequestRefund, false);

  page.renderOrder(Object.assign({}, base, { businessType: 'retail', fulfillmentType: 'COURIER', fulfillmentStatus: 'pending_shipment', available_actions: { can_refund: true } }));
  assert.equal(page.data.canRequestRefund, true);
  production = false;
  page.renderOrder(base);
  assert.equal(page.data.canRequestRefund, false);
});

test('refund submission uses one deterministic key and reconciles an ambiguous POST', async () => {
  let page;
  const calls = [];
  let queryCount = 0;
  const refundOrder = { id: 'ORDER-REFUND-2', orderNo: 'ORDER-REFUND-2', status: 'REFUNDING', paymentStatus: 'paid', refundStatus: 'requested', fulfillmentStatus: 'pending_acceptance', fulfillmentType: 'TAKEAWAY', businessType: 'restaurant', itemsSnapshot: [], available_actions: { can_refund: false } };
  const api = {
    isProduction: () => true,
    normalizeOrder: value => value,
    createRefund: async (orderId, options) => { calls.push({ orderId, options }); throw Object.assign(new Error('timeout'), { userMessage: '退款状态待核实，请刷新订单' }); },
    queryRefund: async orderId => { queryCount += 1; assert.equal(orderId, 'ORDER-REFUND-2'); return refundOrder; }
  };
  const toasts = [];
  load('miniprogram/pages/order/detail/index.js', {
    '../../../services/storage': { getOrders: () => [], getCart: () => [], getStore: () => ({}) },
    '../../../utils/format': { orderStatus: value => value, dateTime: () => '', yuan: value => String(Number(value || 0) / 100) },
    '../../../services/api': api,
    '../../../services/payment': {}
  }, { Page: value => { page = value; }, wx: { showToast: value => toasts.push(value) } });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.orderId = 'ORDER-REFUND-2';
  page.renderOrder(Object.assign({}, refundOrder, { status: 'PREPARING', refundStatus: '', fulfillmentStatus: 'pending_acceptance', available_actions: { can_refund: true } }));

  const first = page.submitRefund('下单有误');
  const second = page.submitRefund('商品不需要了');
  await Promise.all([first, second]);

  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.idempotencyKey, 'refund_ORDER-REFUND-2_initial');
  assert.equal(calls[0].options.clientRequestId, 'refund_ORDER-REFUND-2_initial');
  assert.equal(calls[0].options.reason, '下单有误');
  assert.equal(queryCount, 1);
  assert.equal(page.data.order.status, 'REFUNDING');
  assert.equal(page.data.canRequestRefund, false);
  assert.equal(toasts.some(item => item.title === '退款申请未提交，请刷新订单后重试'), false);
});

test('completed after-sale submission renders merchant review, toasts by original fulfillment, and keeps processed refunds separate', async () => {
  const orderApi = load('miniprogram/services/api.js', {
    './storage': {},
    './http': { isProduction: () => false, isConfigured: () => false, createError: (code, message) => Object.assign(new Error(message), { code }) }
  });
  let page;
  const toasts = [];
  const completedOrder = { order_no: 'ORDER-AFTER-SALE-2', business_type: 'restaurant', status: 'COMPLETED', payment_status: 'paid', fulfillment_status: 'completed', available_actions: { can_refund: true }, items: [] };
  const pendingOrder = Object.assign({}, completedOrder, { refund_status: 'requested', after_sales: [{ id: 2, status: 'requested', type: 'refund' }], available_actions: { can_refund: false } });
  const processedOrder = Object.assign({}, completedOrder, { refund_status: 'processing', after_sales: [{ id: 2, status: 'processing', type: 'refund' }], available_actions: { can_refund: false } });
  const api = {
    isProduction: () => true,
    normalizeOrder: orderApi.normalizeOrder,
    createRefund: async () => ({ order: pendingOrder }),
    queryRefund: async () => pendingOrder
  };
  load('miniprogram/pages/order/detail/index.js', {
    '../../../services/storage': { getOrders: () => [], getCart: () => [], getStore: () => ({}) },
    '../../../utils/format': { orderStatus: status => status === 'AFTER_SALE_REVIEW' ? '售后待审核' : status, dateTime: () => '', yuan: value => String(Number(value || 0) / 100) },
    '../../../services/api': api,
    '../../../services/payment': {}
  }, { Page: value => { page = value; }, wx: { showToast: value => toasts.push(value) } });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.orderId = 'ORDER-AFTER-SALE-2';
  page.renderOrder(completedOrder);
  assert.equal(page.data.canRequestRefund, true);
  await page.submitRefund('商品不需要了');
  assert.equal(page.data.order.status, 'AFTER_SALE_REVIEW');
  assert.equal(page.data.order.statusText, '售后待审核');
  assert.equal(page.data.order.fulfillmentStatus, 'completed');
  assert.equal(page.data.order.timelineStatus, 'COMPLETED');
  assert.equal(page.data.statusDescription, '售后申请已提交，等待商家审核');
  assert.equal(page.data.refundMessage, '售后申请已提交，等待商家审核');
  assert.equal(toasts.some(item => item.title === '售后申请已提交'), true);

  page.renderOrder(processedOrder);
  assert.equal(page.data.order.status, 'REFUNDING');
  assert.equal(page.data.order.fulfillmentStatus, 'completed');
  assert.equal(page.data.statusDescription, '退款申请处理中');
  assert.equal(page.data.refundMessage, '退款申请处理中，等待支付渠道确认');

  const approvedOrder = Object.assign({}, completedOrder, { refund_status: 'requested', after_sales: [{ id: 2, status: 'approved', type: 'refund' }], available_actions: { can_refund: false } });
  page.renderOrder(approvedOrder);
  assert.equal(page.data.order.status, 'REFUNDING');
  assert.equal(page.data.refundMessage, '商家已审核通过，正在安排退款');
});

test('legacy CAMPUS addresses remain selectable in the shared address book', () => {
  let page;
  const addresses = [
    { id: 'legacy-campus', addressType: 'CAMPUS', campusName: '园区东区', zoneName: '配送区域', building: '1号楼', room: '101' },
    { id: 'shipping', addressType: 'SHIPPING', province: '四川省', city: '成都市', district: '武侯区', detailAddress: '科华北路' }
  ];
  load('miniprogram/pages/address/list/index.js', {
    '../../../services/storage': { getAddresses: () => addresses, saveAddresses() {} },
    '../../../services/api': { isProduction: () => false }
  }, { Page: value => { page = value; }, wx: {} });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.onLoad({ type: 'CAMPUS' });
  page.onShow();
  assert.equal(page.data.title, '收货地址');
  assert.deepEqual(page.data.addresses.map(item => item.id), ['legacy-campus', 'shipping']);
});

test('shared address book keeps restaurant and retail checkout selections independent', () => {
  let page;
  const writes = [];
  const addresses = [{ id: 'delivery', addressType: 'SHIPPING', province: '四川省', city: '成都市', district: '武侯区', detailAddress: '科华北路' }];
  load('miniprogram/pages/address/list/index.js', {
    '../../../services/storage': { getAddresses: () => addresses, saveAddresses() {} },
    '../../../services/api': { isProduction: () => false }
  }, { Page: value => { page = value; }, wx: { setStorageSync: (key, value) => writes.push([key, value]), navigateBack() {} } });
  page.setData = change => { page.data = Object.assign({}, page.data, change); };
  page.onLoad({ from: 'checkout', type: 'DELIVERY', business: 'restaurant' });
  page.onShow();
  page.selectAddress({ currentTarget: { dataset: { id: 'delivery' } } });
  assert.deepEqual(writes, [['checkout_takeaway_address_id', 'delivery'], ['checkout_address_id', 'delivery']]);
});

test('fulfillment projection preserves ready and delivered before customer completion', () => {
  const api = load('miniprogram/services/api.js', { './storage': {}, './http': {} });
  const format = require('../miniprogram/utils/format');
  const base = { payment_status: 'paid', status: 'PAID', business_type: 'restaurant', items: [] };
  assert.equal(api.normalizeOrder({ ...base, fulfillment_status: 'accepted' }).status, 'ACCEPTED');
  assert.equal(api.normalizeOrder({ ...base, fulfillment_status: 'preparing' }).status, 'PREPARING');
  for (const method of ['pickup', 'delivery']) {
    const ready = api.normalizeOrder({ ...base, fulfillment_status: 'ready', fulfillment_method: method, ready_at: '2026-10-14T09:00:00Z' });
    assert.equal(ready.status, 'READY');
    assert.equal(ready.readyAt, '2026-10-14T09:00:00Z');
    assert.equal(ready.fulfillmentCompleted, false);
    assert.equal(format.orderStatus(ready.status, ready.fulfillmentType, ready.deliveryMethod), method === 'pickup' ? '待取餐' : '待配送');
  }
  const delivered = api.normalizeOrder({ ...base, business_type: 'retail', status: 'COMPLETED', fulfillment_status: 'delivered' });
  assert.equal(delivered.status, 'DELIVERED');
  assert.equal(delivered.fulfillmentCompleted, false);
  assert.equal(api.normalizeOrder({ ...base, business_type: 'retail', fulfillment_status: 'completed' }).fulfillmentCompleted, true);
  const closed = api.normalizeOrder({ ...base, status: 'COMPLETED', fulfillment_status: 'completed', refund_status: 'closed' });
  assert.equal(closed.status, 'REFUND_FAILED');
  assert.equal(closed.fulfillmentCompleted, true);
});

test('ready and signed retail pages retain work tabs and obey server receipt permissions', () => {
  const api = load('miniprogram/services/api.js', { './storage': {}, './http': {} });
  let detail; let list;
  let confirmations = 0;
  load('miniprogram/pages/order/detail/index.js', {
    '../../../services/storage': {},
    '../../../services/api': { normalizeOrder: api.normalizeOrder, isProduction: () => true, confirmReceipt: () => { confirmations++; } },
    '../../../services/payment': {}
  }, { Page: page => { detail = page; }, wx: { showToast() {} } });
  detail.setData = change => { detail.data = { ...detail.data, ...change }; };
  load('miniprogram/pages/order/list/index.js', { '../../../services/storage': {}, '../../../services/api': {} }, { Page: page => { list = page; } });
  for (const method of ['pickup', 'delivery']) {
    detail.renderOrder({ business_type: 'restaurant', payment_status: 'paid', fulfillment_status: 'ready', fulfillment_method: method, available_actions: { can_confirm_receipt: false }, items: [] });
    assert.equal(detail.data.order.status, 'READY');
    assert.equal(detail.data.canConfirmReceipt, false);
    assert.match(detail.data.statusDescription, method === 'pickup' ? /到店取餐/ : /等待安排配送/);
    assert.equal(list.matchesTab(detail.data.order, 'PROCESSING'), true);
    assert.equal(list.matchesTab(detail.data.order, 'DELIVERING'), false);
    detail.confirmReceipt();
  }
  assert.equal(confirmations, 0);
  detail.renderOrder({ business_type: 'restaurant', payment_status: 'paid', fulfillment_status: 'accepted', available_actions: { can_confirm_receipt: false }, items: [] });
  assert.equal(detail.data.order.statusText, '已接单');
  assert.equal(detail.data.statusDescription, '商家已接单，等待开始制作');
  assert.equal(list.matchesTab(detail.data.order, 'PROCESSING'), true);
  detail.renderOrder({ business_type: 'retail', payment_status: 'paid', fulfillment_status: 'delivered', available_actions: { can_confirm_receipt: true, can_refund: false }, items: [] });
  assert.equal(detail.data.order.status, 'DELIVERED');
  assert.equal(detail.data.order.fulfillmentCompleted, false);
  assert.equal(detail.data.order.timelineStatus, 'DELIVERED');
  assert.equal(detail.data.canRequestRefund, false);
  assert.equal(detail.data.canConfirmReceipt, true);
  assert.match(detail.data.statusDescription, /待确认收货/);
  assert.equal(list.matchesTab(detail.data.order, 'SHIPPED'), true);
  assert.equal(list.matchesTab(detail.data.order, 'COMPLETED'), false);
  detail.renderOrder({ business_type: 'restaurant', payment_status: 'paid', fulfillment_status: 'delivering', available_actions: { can_confirm_receipt: false }, items: [] });
  assert.match(detail.data.statusDescription, /配送途中/);
  assert.equal(detail.data.canConfirmReceipt, false);
});

test('profile work counts and payment polling include ready and delivered without completing them', () => {
  const api = load('miniprogram/services/api.js', { './storage': {}, './http': {} });
  let profile;
  load('miniprogram/pages/profile/index/index.js', {
    '../../../services/storage': { getStore: () => ({}) }, '../../../services/api': { normalizeOrder: api.normalizeOrder }
  }, { Page: page => { profile = page; } });
  profile.setData = change => { profile.data = { ...profile.data, ...change }; };
  profile.renderProfile({ globalData: { user: {} } }, [], [
    { business_type: 'restaurant', payment_status: 'paid', fulfillment_status: 'accepted' },
    { business_type: 'restaurant', payment_status: 'paid', fulfillment_status: 'ready', fulfillment_method: 'pickup' },
    { business_type: 'retail', payment_status: 'paid', fulfillment_status: 'delivered' }
  ]);
  assert.equal(profile.data.orderStats.processing, 2);
  assert.equal(profile.data.orderStats.delivering, 0);
  assert.equal(profile.data.orderStats.shipped, 1);
  assert.equal(profile.data.orderStats.completed, 0);
  const payment = load('miniprogram/services/payment.js', { './api': {} });
  assert.equal(payment.isSettled('ACCEPTED'), true);
  assert.equal(payment.isSettled('READY'), true);
  assert.equal(payment.isSettled('DELIVERED'), true);
});

test('demo receipt waits for signed retail delivery and completion persists fulfillment truth', () => {
  const api = load('miniprogram/services/api.js', { './storage': {}, './http': {} });
  let detail;
  const order = { id: 'demo-retail', businessType: 'retail', paymentStatus: 'paid', status: 'SHIPPED', fulfillmentStatus: 'shipped', items: [] };
  let saves = 0;
  load('miniprogram/pages/order/detail/index.js', {
    '../../../services/storage': { getOrders: () => [order], saveOrders: () => { saves++; } },
    '../../../services/api': { normalizeOrder: api.normalizeOrder, isProduction: () => false },
    '../../../services/payment': {}
  }, { Page: page => { detail = page; }, wx: { showToast() {} } });
  detail.setData = change => { detail.data = { ...detail.data, ...change }; };
  detail.orderId = order.id;
  detail.loadOrder = () => detail.renderOrder(order);
  detail.renderOrder(order);
  assert.equal(detail.data.canConfirmReceipt, false);
  detail.confirmReceipt();
  assert.equal(saves, 0);
  Object.assign(order, { status: 'DELIVERED', fulfillmentStatus: 'delivered', delivered_at: '2026-10-14T10:00:00Z' });
  detail.renderOrder(order);
  assert.equal(detail.data.canConfirmReceipt, true);
  assert.ok(detail.data.order.deliveredText);
  assert.equal(detail.data.order.fulfillmentCompleted, false);
  detail.confirmReceipt();
  assert.equal(saves, 1);
  assert.equal(order.fulfillmentStatus, 'completed');
  assert.equal(detail.data.order.fulfillmentCompleted, true);
});

test('demo merchant dispatch combines readiness and departure in one write while preserving deferred and pickup flows', () => {
  let merchant; let writes = 0;
  const orders = [
    { id: 'combined', status: 'PREPARING', fulfillmentType: 'TAKEAWAY', deliveryMethod: 'DELIVERY', deliveryFee: 300 },
    { id: 'deferred', status: 'PREPARING', fulfillmentType: 'TAKEAWAY', deliveryMethod: 'DELIVERY' },
    { id: 'pickup', status: 'PREPARING', fulfillmentType: 'TAKEAWAY', deliveryMethod: 'PICKUP' }
  ];
  load('miniprogram/pages/merchant/index/index.js', {
    '../../../services/storage': { getOrders: () => orders, saveOrders: saved => { assert.equal(saved, orders); writes++; } },
    '../../../services/api': { isProduction: () => false }
  }, { Page: page => { merchant = page; }, wx: { showToast() {} } });
  merchant.setData = change => { merchant.data = { ...merchant.data, ...change }; };
  merchant.deliveryDrafts = {};
  const event = id => ({ currentTarget: { dataset: { id } } });
  merchant.onRunnerInput({ currentTarget: { dataset: { id: 'combined', field: 'runnerName' } }, detail: { value: '配送师傅' } });
  merchant.onRunnerInput({ currentTarget: { dataset: { id: 'combined', field: 'runnerPhone' } }, detail: { value: '13911112222' } });
  merchant.startDelivery(event('combined'));
  assert.equal(writes, 1);
  assert.equal(orders[0].status, 'DELIVERING');
  assert.equal(orders[0].fulfillmentStatus, 'delivering');
  assert.ok(orders[0].readyAt);
  assert.equal(orders[0].readyAt, orders[0].deliveringAt);
  assert.equal(orders[0].delivery.runnerName, '配送师傅');
  assert.equal(orders[0].delivery.runnerPhone, '13911112222');
  merchant.finishPreparing(event('deferred'));
  assert.equal(writes, 2);
  assert.equal(orders[1].status, 'READY');
  assert.ok(orders[1].readyAt);
  assert.equal(orders[1].deliveringAt, undefined);
  orders[1].readyAt = '2026-10-14T09:00:00Z';
  merchant.startDelivery(event('deferred'));
  assert.equal(writes, 3);
  assert.equal(orders[1].status, 'DELIVERING');
  assert.equal(orders[1].readyAt, '2026-10-14T09:00:00Z');
  assert.ok(orders[1].deliveringAt);
  merchant.startDelivery(event('pickup'));
  assert.equal(writes, 3);
  assert.equal(orders[2].status, 'PREPARING');
  merchant.finishPreparing(event('pickup'));
  assert.equal(writes, 4);
  assert.equal(orders[2].status, 'READY');
  assert.ok(orders[2].readyAt);
  merchant.startDelivery(event('pickup'));
  assert.equal(writes, 4);
  assert.equal(orders[2].deliveringAt, undefined);
  merchant.completeOrder(event('pickup'));
  assert.equal(writes, 5);
  assert.equal(orders[2].status, 'COMPLETED');
  assert.ok(orders[2].completedAt);
});
