const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');
const calendar = require(path.join(miniappRoot, 'utils/calendar.js'));
const promotion = require(path.join(miniappRoot, 'utils/promotion.js'));

function flush() {
  return new Promise(resolve => setImmediate(resolve));
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function loadConfirmation(app, xhs) {
  let definition;
  vm.runInNewContext(fs.readFileSync(path.join(miniappRoot, 'pages/order/confirm.js'), 'utf8'), {
    Page: value => { definition = value; },
    getApp: () => app,
    xhs: xhs || {},
    require: request => {
      if (request === '../../utils/calendar') return calendar;
      if (request === '../../utils/promotion') return promotion;
      if (request === '../../utils/payment') return { requestGuaranteeOrderPayment() { return false; } };
      throw new Error(`unexpected require: ${request}`);
    },
    Date, Math, Number, String, Boolean, RegExp, encodeURIComponent,
    setInterval: () => 1, clearInterval() {}
  }, { filename: 'pages/order/confirm.js' });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (update, callback) => { Object.assign(page.data, update); if (callback) callback(); };
  return page;
}

for (const pageFile of ['pages/index/index.js', 'pages/product/detail.js']) {
  test(`${pageFile} ignores an opportunity response after unload`, async () => {
    const response = deferred();
    let definition;
    let timers = 0;
    vm.runInNewContext(fs.readFileSync(path.join(miniappRoot, pageFile), 'utf8'), {
      Page: value => { definition = value; },
      getApp: () => ({ request: () => response.promise }),
      require: () => promotion,
      setInterval: () => { timers++; return 1; }, clearInterval() {}, clearTimeout() {}
    });
    const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)), mappingId: 42 };
    page.setData = () => assert.fail('unloaded page received a state update');
    const pending = page.loadOpportunity();
    page.onUnload();
    response.resolve({ status: 'available', discount_cents: 900, eligible_mapping_ids: [42] });
    await pending;
    assert.equal(timers, 0);
  });
}

test('opportunity parses the backend field names and uses server time for expiry', () => {
  const receivedAt = 1_700_000_000_000;
  const item = promotion.normalize({
    status: 'available', grant_id: 'grant-1', discount_cents: 900,
    server_now: 1_700_000_010_000, expires_at: 1_700_000_070_000,
    eligible_mapping_ids: [42]
  }, receivedAt);
  assert.equal(promotion.appliesTo(item, 42, receivedAt + 50_000), true);
  assert.equal(promotion.countdown(item, receivedAt + 50_000), '0:10');
  assert.equal(promotion.appliesTo(item, 42, receivedAt + 110_000), false);
});

test('latest quote wins and checkout remains disabled until it has a fresh token', async () => {
  const first = deferred();
  const second = deferred();
  let quoteCalls = 0;
  const page = loadConfirmation({
    request(path) {
      assert.equal(path, '/order-quote');
      quoteCalls += 1;
      return quoteCalls === 1 ? first.promise : second.promise;
    }
  });
  page.data.product = { id: 42, price_cents: 5000 };
  page.data.quantity = 1;
  const older = page.refreshQuote();
  const newer = page.refreshQuote();
  assert.equal(page.data.quoteReady, false);
  assert.equal(page.data.quoteToken, '');
  second.resolve({ original_amount_cents: 10000, discount_cents: 900, amount_cents: 9100, quote_token: 'new-token', promotion: { status: 'available', discount_cents: 900, eligible_mapping_ids: [42] } });
  await newer;
  first.resolve({ original_amount_cents: 5000, discount_cents: 0, amount_cents: 5000, quote_token: 'old-token' });
  await older;
  assert.equal(page.data.quoteToken, 'new-token');
  assert.equal(page.data.totalText, '91.00');
  assert.equal(page.data.hasDiscount, true);
});

test('orders receive only the fresh quote token and unknown network retry retains identity', async () => {
  const requests = [];
  let attempts = 0;
  const page = loadConfirmation({
    request(path, options) {
      requests.push({ path, options });
      attempts += 1;
      return attempts === 1 ? Promise.reject(new Error('网络连接失败')) : Promise.resolve({ order_no: 'ORD-42' });
    }
  });
  page.data.product = { id: 42, price_cents: 5000 };
  page.data.quoteReady = true;
  page.data.quoteToken = 'fresh-quote-token';
  page.orderRequestId = 'same-request-id';
  page.submit();
  await flush();
  page.submit();
  await flush();
  assert.equal(requests.length, 2);
  for (const request of requests) {
    assert.equal(request.path, '/orders');
    assert.equal(request.options.data.quote_token, 'fresh-quote-token');
    assert.equal(request.options.data.request_id, 'same-request-id');
    assert.equal(Object.hasOwn(request.options.data, 'amount_cents'), false);
    assert.equal(Object.hasOwn(request.options.data, 'discount_cents'), false);
  }
});

test('price conflict fetches a new quote and requires a subsequent submit', async () => {
  const requests = [];
  const page = loadConfirmation({
    request(path, options) {
      requests.push({ path, options });
      if (path === '/orders') return Promise.reject(new Error('quote expired'));
      return Promise.resolve({ original_amount_cents: 5000, discount_cents: 0, amount_cents: 5000, quote_token: 'replacement' });
    }
  });
  page.data.product = { id: 42, price_cents: 5000 };
  page.data.quoteReady = true;
  page.data.quoteToken = 'expired-token';
  page.orderRequestId = 'same-request-id';
  page.submit();
  await flush();
  await flush();
  assert.equal(requests.filter(request => request.path === '/orders').length, 1);
  assert.equal(requests.filter(request => request.path === '/order-quote').length, 1);
  assert.equal(page.data.quoteToken, 'replacement');
  assert.match(page.data.error, /重新提交/);
});

test('opportunity expiry clears a ready quote before a customer can submit', async () => {
  const page = loadConfirmation({
    request(path) {
      assert.equal(path, '/order-quote');
      return Promise.resolve({ original_amount_cents: 5000, discount_cents: 0, amount_cents: 5000, quote_token: 'after-expiry' });
    }
  });
  page.mappingId = 42;
  page.data.product = { id: 42, price_cents: 5000 };
  page.data.quoteReady = true;
  page.data.quoteToken = 'expiring-token';
  page.opportunity = promotion.normalize({ status: 'available', discount_cents: 900, eligible_mapping_ids: [42] }, Date.now());
  page.applyOpportunity(promotion.normalize({ status: 'expired', discount_cents: 900, eligible_mapping_ids: [42] }, Date.now()));
  assert.equal(page.data.quoteReady, false);
  assert.equal(page.data.quoteToken, '');
  await flush();
  assert.equal(page.data.quoteToken, 'after-expiry');
  assert.match(page.data.error, /优惠已结束/);
});

test('a late opportunity acquisition replaces the earlier full-price quote', async () => {
  const acquisition = deferred();
  let acquired = false;
  const page = loadConfirmation({
    request(path) {
      if (path === '/promotion') return acquisition.promise;
      assert.equal(path, '/order-quote');
      return Promise.resolve({ original_amount_cents: 5000, discount_cents: acquired ? 900 : 0,
        amount_cents: acquired ? 4100 : 5000, quote_token: acquired ? 'discounted' : 'full-price' });
    }
  });
  page.mappingId = 42;
  page.data.product = { id: 42, price_cents: 5000 };
  const pending = page.loadOpportunity();
  await page.refreshQuote();
  assert.equal(page.data.totalText, '50.00');
  acquired = true;
  acquisition.resolve({ status: 'available', grant_id: 1, discount_cents: 900, eligible_mapping_ids: [42] });
  await pending;
  assert.equal(page.data.quoteToken, 'discounted');
  assert.equal(page.data.totalText, '41.00');
});
