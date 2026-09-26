const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { createRequire } = require('node:module');

function load(relative, stubs, globals = {}) {
  const filename = path.resolve(__dirname, '..', relative);
  const module = { exports: {} };
  const localRequire = createRequire(filename);
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), Object.assign({
    module,
    exports: module.exports,
    console,
    setTimeout,
    clearTimeout,
    require: id => Object.prototype.hasOwnProperty.call(stubs, id) ? stubs[id] : localRequire(id)
  }, globals), { filename });
  return module.exports;
}

function loadApi(request) {
  return load('miniprogram/services/api.js', {
    './storage': {
      getStore: () => ({}),
      getProducts: () => [],
      getAddresses: () => [],
      saveProducts() {},
      saveStore() {},
      getCoupons: () => [],
      getOrders: () => []
    },
    './http': {
      isConfigured: () => true,
      getBusinesses: () => [],
      createError: (code, message) => Object.assign(new Error(message), { code, userMessage: message }),
      request
    }
  }, { getApp: () => ({ globalData: { deploymentMode: 'production' } }) });
}

test('assist normalizers preserve separate rewards and lowercase status without zero defaults', () => {
  const api = loadApi(async () => ({ data: {} }));
  const campaign = api.normalizeAssistCampaign({
    id: 7,
    status: 'ACTIVE',
    business_type: 'restaurant',
    starter_reward: { discount_cents: 300, min_goods_subtotal_cents: 2500, valid_days: 7, template_ends_at: '2026-10-01T00:00:00Z' },
    helper_reward: { discount_cents: 200, min_goods_subtotal_cents: 3000, valid_days: 5, template_ends_at: '2026-09-29T00:00:00Z' }
  });
  assert.equal(campaign.status, 'active');
  assert.equal(campaign.starterReward.discountAmount, 300);
  assert.equal(campaign.starterReward.minGoodsAmount, 2500);
  assert.equal(campaign.starterReward.validDays, 7);
  assert.equal(campaign.starterReward.templateEndsAt, '2026-10-01T00:00:00Z');
  assert.equal(campaign.helperReward.discountAmount, 200);
  assert.equal(campaign.helperReward.minGoodsAmount, 3000);
  assert.equal(campaign.helperReward.validDays, 5);
  assert.equal(campaign.helperReward.templateEndsAt, '2026-09-29T00:00:00Z');

  const missing = api.normalizeAssistCampaign({ starter_reward: { valid_days: 3 } });
  assert.equal(missing.starterReward.discountAmount, null);
  assert.equal(missing.starterReward.minGoodsAmount, null);
  const session = api.normalizeAssistSession({ status: 'SUCCESS', share_token: 'raw-token', starter_reward: { discount_cents: 100 }, helper_reward: { discount_cents: 50 } });
  assert.equal(session.status, 'success');
  assert.equal(session.token, 'raw-token');
  assert.equal(session.starterReward.discountAmount, 100);
  assert.equal(session.helperReward.discountAmount, 50);
});

test('storefront assist, quote, checkout, cancel, receipt, and shipment APIs use server contracts', async () => {
  const calls = [];
  const request = async (pathValue, options = {}) => {
    calls.push({ path: pathValue, options });
    if (pathValue === '/assist-campaigns') return { data: { campaigns: [{ id: 17, status: 'ACTIVE', business_type: 'restaurant', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } }] } };
    if (pathValue === '/assist-sessions') return { data: { id: 19, share_token: 'raw-token', status: 'ACTIVE', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } } };
    if (pathValue === '/assist-sessions/raw-token') return { data: { id: 19, status: 'ACTIVE', share_token: 'raw-token', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } } };
    if (pathValue === '/assist-sessions/raw-token/help') return { data: { id: 19, status: 'SUCCESS', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } } };
    if (pathValue === '/checkout-quotes') return { data: { quote_token: 'quote-token', quote: { goods_subtotal_cents: 2500, payable_amount_cents: 2400 } } };
    if (pathValue === '/carts/cart-1/checkout') return { data: { order_no: 'ORDER-1', status: 'PAID', payment_status: 'paid', business_type: 'restaurant', fulfillment_type: 'TAKEAWAY', items: [] } };
    if (pathValue === '/orders/ORDER-1/cancel') return { data: { order_no: 'ORDER-1', status: 'CANCELED' } };
    if (pathValue === '/orders/ORDER-1/confirm-receipt') return { data: { order_no: 'ORDER-1', status: 'COMPLETED' } };
    if (pathValue === '/orders/ORDER-1/shipments') return { data: { events: [{ id: 'late', occurred_at: '2026-09-21T10:00:00Z' }, { id: 'early', occurred_at: '2026-09-21T09:00:00Z' }] } };
    return { data: {} };
  };
  const api = loadApi(request);

  const campaigns = await api.getAssistCampaigns('restaurant');
  assert.equal(campaigns.data[0].starterReward.discountAmount, 300);
  assert.equal(calls[0].path, '/assist-campaigns');
  assert.equal(calls[0].options.businessType, 'restaurant');

  const created = await api.createAssistSession(17, 'assist-idem-1', 'restaurant');
  assert.equal(created.data.token, 'raw-token');
  assert.equal(calls[1].options.data.campaign_id, 17);
  assert.equal(calls[1].options.data.idempotency_key, 'assist-idem-1');
  assert.equal(calls[1].options.businessType, 'restaurant');

  const loaded = await api.getAssistSession('raw-token', 'restaurant');
  assert.equal(loaded.data.token, 'raw-token');
  const helped = await api.helpAssist('raw-token', 'restaurant');
  assert.equal(helped.data.token, 'raw-token');
  assert.equal(calls[2].path, '/assist-sessions/raw-token');
  assert.equal(calls[3].path, '/assist-sessions/raw-token/help');

  const quote = await api.createCheckoutQuote({ fulfillment_method: 'delivery', address_id: '9', zone_id: '7', slot_id: '8', slot_date: '2026-09-22' }, 'restaurant');
  assert.equal(quote.quoteToken, 'quote-token');
  const quoteCall = calls.find(call => call.path === '/checkout-quotes');
  assert.equal(quoteCall.options.businessType, 'restaurant');
  assert.equal(quoteCall.options.data.slot_date, '2026-09-22T00:00:00Z');

  const order = await api.checkoutCart('cart-1', { businessType: 'restaurant', idempotencyKey: 'checkout-idem-1', contactName: '朋友', contactPhone: '13800000000', addressId: 9, quoteToken: 'quote-token', couponGrantId: '44', deliveryMethod: 'DELIVERY' });
  assert.equal(order.orderNo, 'ORDER-1');
  const checkoutCall = calls.find(call => call.path === '/carts/cart-1/checkout');
  assert.equal(checkoutCall.options.data.quote_token, 'quote-token');
  assert.equal(checkoutCall.options.data.coupon_grant_id, 44);
  assert.equal(checkoutCall.options.data.fulfillment_method, 'delivery');

  const canceled = await api.cancelOrder('ORDER-1');
  const completed = await api.confirmReceipt('ORDER-1');
  const shipments = await api.getShipments('ORDER-1');
  assert.equal(canceled.status, 'CANCELED');
  assert.equal(completed.status, 'COMPLETED');
  assert.deepEqual(shipments.events.map(event => event.id), ['early', 'late']);
  assert.equal(calls.find(call => call.path === '/orders/ORDER-1/cancel').options.method, 'POST');
  assert.equal(calls.find(call => call.path === '/orders/ORDER-1/confirm-receipt').options.method, 'POST');
  assert.equal(calls.find(call => call.path === '/orders/ORDER-1/shipments').options.method, 'GET');
});

test('production storefront APIs do not fall back to local demo state', async () => {
  const calls = [];
  const api = loadApi(async (pathValue, options) => { calls.push({ path: pathValue, options }); return { data: { coupons: [] } }; });
  const result = await api.getCoupons('retail');
  assert.deepEqual(result.data, []);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, '/coupons');
  assert.equal(calls[0].options.businessType, 'retail');
});

test('storefront contact is read from the shared channel endpoint and disabled data stays hidden', async () => {
  const calls = [];
  const api = loadApi(async (pathValue, options) => {
    calls.push({ path: pathValue, options });
    return { contact_type: 'enterprise_wechat', contact_name: '门店客服', wechat_id: 'shop-service', qr_code_url: 'https://example.com/contact.png', status: 'active', available: true };
  });
  const contact = await api.getStorefrontContact();
  assert.equal(calls[0].path, '/contact');
  assert.equal(contact.available, true);
  assert.equal(contact.contactType, 'enterprise_wechat');
  assert.equal(contact.wechatId, 'shop-service');
  assert.equal(contact.qrCodeUrl, 'https://example.com/contact.png');

  const hidden = api.normalizeStorefrontContact({ status: 'disabled', available: false, wechat_id: 'must-not-leak', qr_code_url: 'https://example.com/hidden.png' });
  assert.equal(hidden.available, false);
  assert.equal(hidden.status, 'disabled');
  assert.equal(hidden.contactType, '');
  assert.equal(hidden.contactName, '');
  assert.equal(hidden.wechatId, '');
  assert.equal(hidden.qrCodeUrl, '');
});

test('member profile and trusted phone authorization use the shared storefront contract', async () => {
  const calls = [];
  const api = loadApi(async (pathValue, options) => {
    calls.push({ path: pathValue, options });
    if (pathValue === '/member/me') return { member_no: 'M-001', membership_status: 'provisional', phone_verified: false };
    return { verified: true, phone_masked: '138****8000', membership_status: 'active' };
  });
  const profile = await api.getMemberProfile();
  assert.equal(profile.member_no, 'M-001');
  const verified = await api.verifyMemberPhone('phone-code', 'member-phone-1', true, 'member-phone-v1');
  assert.equal(verified.membership_status, 'active');
  assert.equal(calls[0].path, '/member/me');
  assert.equal(calls[1].path, '/member/verify-phone');
  assert.equal(calls[1].options.method, 'POST');
  assert.deepEqual(JSON.parse(JSON.stringify(calls[1].options.data)), {
    code: 'phone-code',
    request_id: 'member-phone-1',
    membership_consent_granted: true,
    membership_policy_version: 'member-phone-v1'
  });
});

test('assist pages use explicit APIs and share the raw token', async () => {
  let indexPage;
  let detailPage;
  let storedSession = null;
  const calls = [];
  const storage = {
    getAssist: () => storedSession,
    saveAssist: value => { storedSession = value; },
    makeId: prefix => `${prefix}_1`,
    getCoupons: () => [],
    saveCoupons() {}
  };
  const api = {
    isProduction: () => true,
    getAssistCampaigns: async (businessType) => { calls.push(['campaigns', businessType]); return { data: [{ id: 17, status: 'active', starterReward: { discountAmount: 300 }, helperReward: { discountAmount: 200 } }] }; },
    createAssistSession: async (campaignId, idempotencyKey, businessType) => { calls.push(['create', campaignId, idempotencyKey, businessType]); return { data: { share_token: 'raw-token', status: 'ACTIVE', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } } }; },
    getAssistSession: async (token, businessType) => { calls.push(['get', token, businessType]); return { data: { share_token: token, status: 'ACTIVE', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } } }; },
    helpAssist: async (token, businessType) => { calls.push(['help', token, businessType]); return { data: { share_token: token, status: 'SUCCESS', starter_reward: { discount_cents: 300 }, helper_reward: { discount_cents: 200 } } }; }
  };
  const wx = { showToast() {}, showShareMenu() {} };
  load('miniprogram/pages/assist/index/index.js', { '../../../services/storage': storage, '../../../services/api': api }, { Page: value => { indexPage = value; }, wx });
  indexPage.setData = change => { indexPage.data = Object.assign({}, indexPage.data, change); };
  indexPage.onShow();
  await new Promise(resolve => setTimeout(resolve, 0));
  indexPage.createSession();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(calls[0][0], 'campaigns');
  assert.equal(calls[0][1], 'restaurant');
  assert.deepEqual(calls.find(call => call[0] === 'create').slice(1), [17, 'assist_create_1', 'restaurant']);
  assert.equal(indexPage.onShareAppMessage().path, '/pages/assist/detail/index?token=raw-token');

  load('miniprogram/pages/assist/detail/index.js', { '../../../services/storage': storage, '../../../services/api': api }, { Page: value => { detailPage = value; }, wx });
  detailPage.setData = change => { detailPage.data = Object.assign({}, detailPage.data, change); };
  detailPage.onLoad({ token: 'raw-token' });
  await new Promise(resolve => setTimeout(resolve, 0));
  detailPage.help();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(calls.filter(call => call[0] === 'get').pop().slice(1), ['raw-token', 'restaurant']);
  assert.deepEqual(calls.filter(call => call[0] === 'help').pop().slice(1), ['raw-token', 'restaurant']);
});
