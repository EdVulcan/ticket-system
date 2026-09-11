const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');
const { requestGuaranteeOrderPayment } = require(path.join(miniappRoot, 'utils/payment.js'));

function flush() {
  return new Promise(resolve => setImmediate(resolve));
}

function loadPage(relativePath, app, xhs) {
  let definition;
  const source = fs.readFileSync(path.join(miniappRoot, relativePath), 'utf8');
  vm.runInNewContext(source, {
    Page: value => { definition = value; },
    getApp: () => app,
    xhs,
    require: request => {
      if (request === '../../utils/payment') return require(path.join(miniappRoot, 'utils/payment.js'));
      if (request === '../../utils/calendar') return require(path.join(miniappRoot, 'utils/calendar.js'));
      if (request === '../../utils/qr') return require(path.join(miniappRoot, 'utils/qr.js'));
      if (request === '../../utils/promotion') return require(path.join(miniappRoot, 'utils/promotion.js'));
      throw new Error(`unexpected require: ${request}`);
    },
    Date,
    Math,
    Number,
    String,
    Boolean,
    RegExp,
    encodeURIComponent,
    setTimeout: () => 1,
    clearTimeout: () => {}
  }, { filename: relativePath });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (update, callback) => {
    Object.assign(page.data, update);
    if (callback) callback();
  };
  return page;
}

test('payment helper shows a bounded actual failure reason/code but removes payment secrets', () => {
  const order = { order_id: 'order-id', pay_token: 'pay-token' };
  const messages = [];

  assert.equal(requestGuaranteeOrderPayment({}, order, { onFailure: result => messages.push(result.message) }), false);
  assert.match(messages.pop(), /暂不支持小红书支付/);

  assert.equal(requestGuaranteeOrderPayment({
    requestGuaranteeOrderPayment() {
      throw { errCode: 'PAY_1001', errMsg: '支付被拒绝 pay_token=pay-token order_id=order-id' };
    }
  }, order, { onFailure: result => messages.push(result.message) }), false);
  const denied = messages.pop();
  assert.match(denied, /错误 PAY_1001/);
  assert.match(denied, /支付被拒绝/);
  assert.doesNotMatch(denied, /pay-token|order-id/);

  assert.equal(requestGuaranteeOrderPayment({
    requestGuaranteeOrderPayment(options) { options.fail({ errMsg: 'request:fail cancel' }); options.complete(); }
  }, order, { onFailure: result => messages.push(result.message) }), true);
  assert.match(messages.pop(), /取消支付/);
});

test('confirmation page keeps an existing order and visibly reports missing payment fields', async () => {
  const app = { request: () => Promise.resolve({ order_no: 'ORD-1' }) };
  const xhs = { redirectTo: () => assert.fail('missing fields must not redirect') };
  const page = loadPage('pages/order/confirm.js', app, xhs);
  page.data.product = { id: 1, price_cents: 8000 };
  page.data.quoteReady = true; page.data.quoteToken = 'quote-1';
  page.orderRequestId = 'stable-request-id';

  page.submit();
  await flush();

  assert.equal(page.data.submitting, false);
  assert.match(page.data.error, /支付信息不完整/);
});

test('confirmation page reports cancellation rather than silently redirecting', async () => {
  let redirected = false;
  const app = { request: () => Promise.resolve({ order_id: 'order-1', pay_token: 'pay-token', order_no: 'ORD-1' }) };
  const xhs = {
    requestGuaranteeOrderPayment(options) {
      options.fail({ errMsg: 'requestGuaranteeOrderPayment:fail cancel' });
      options.complete();
    },
    redirectTo: () => { redirected = true; }
  };
  const page = loadPage('pages/order/confirm.js', app, xhs);
  page.data.product = { id: 1, price_cents: 8000 };
  page.data.quoteReady = true; page.data.quoteToken = 'quote-1';
  page.orderRequestId = 'stable-request-id';

  page.submit();
  await flush();

  assert.equal(redirected, false);
  assert.equal(page.data.submitting, false);
  assert.match(page.data.error, /取消支付/);
});

test('created confirmation orders freeze selection and continue in that order without a second create', async () => {
  let requests = 0;
  let destination = '';
  const page = loadPage('pages/order/confirm.js', {
    request: () => { requests += 1; return Promise.resolve({ order_no: 'ORD-KEPT', order_id: 'order-1', pay_token: 'pay-token' }); }
  }, {
    requestGuaranteeOrderPayment(options) { options.fail({ errMsg: 'cancel' }); options.complete(); },
    redirectTo: ({ url }) => { destination = url; }
  });
  page.data.product = { id: 1, price_cents: 8000 };
  page.data.quoteReady = true; page.data.quoteToken = 'quote-1';
  page.data.maxQuantity = 10;
  page.orderRequestId = 'stable-request-id';
  page.submit();
  await flush();
  page.increase();
  page.onGuestNameInput({ detail: { value: 'Changed' } });
  assert.equal(page.data.quantity, 1);
  assert.equal(page.data.guestName, '');
  assert.equal(page.data.createdOrderNo, 'ORD-KEPT');
  page.submit();
  assert.equal(requests, 1);
  assert.equal(destination, '/pages/order/detail?order_no=ORD-KEPT');
});

test('unknown order creation freezes the first payload and retries it after attempted edits', async () => {
  const orderRequests = [];
  let firstCreate = true;
  const app = {
    request: (url, options) => {
      if (url === '/orders') {
        orderRequests.push(options.data);
        if (firstCreate) {
          firstCreate = false;
          return Promise.reject(new Error('网络连接失败，请稍后重试'));
        }
        return Promise.resolve({ order_no: 'ORD-RECOVERED', order_id: 'order-1', pay_token: 'pay-1' });
      }
      if (url === '/order-quote') {
        const quantity = options.data.quantity;
        return Promise.resolve({
          amount_cents: quantity * 8000,
          original_amount_cents: quantity * 8000,
          discount_cents: 0,
          quote_token: `quote-${quantity}`
        });
      }
      return Promise.resolve({});
    }
  };
  const page = loadPage('pages/order/confirm.js', app, {
    requestGuaranteeOrderPayment(options) { options.fail({ errMsg: 'cancel' }); options.complete(); }
  });
  page.data.product = { id: 1, price_cents: 8000, requiresUseDate: true, useDateLabel: '游玩日期' };
  page.data.quantity = 1;
  page.data.maxQuantity = 10;
  page.data.useDate = '2026-09-20';
  page.data.minDate = '2026-09-20';
  page.data.maxDate = '2026-10-20';
  page.data.quoteReady = true;
  page.data.quoteToken = 'quote-1';
  page.data.totalText = '80.00';
  page.data.originalTotalText = '80.00';
  page.orderRequestId = 'same-request-id';

  page.submit();
  await flush();

  assert.equal(page.data.orderRecoveryPending, true);
  page.increase();
  page.selectDate({ currentTarget: { dataset: { date: '2026-09-21' } } });
  page.onGuestNameInput({ detail: { value: 'Changed' } });
  page.onContactPhoneInput({ detail: { value: '13900000000' } });
  page.submit();
  await flush();
  await flush();

  assert.equal(orderRequests.length, 2);
  assert.deepEqual(orderRequests[1], orderRequests[0]);
  assert.equal(page.data.createdOrderNo, 'ORD-RECOVERED');
  assert.equal(page.data.quantity, 1);
  assert.equal(page.data.useDate, '2026-09-20');
  assert.equal(page.data.guestName, '');
  assert.equal(page.data.contactPhone, '');
});

test('explicit precheck rejection unlocks editing and requires a fresh quote before retry', async () => {
  const requests = [];
  const app = {
    request: (url, options) => {
      requests.push({ url, data: options && options.data });
      if (url === '/orders') {
        const error = new Error('价格或立减资格已变化，请确认最新金额后重新提交');
        error.data = { error_code: 'order_not_created' };
        return Promise.reject(error);
      }
      return Promise.resolve({ amount_cents: 9000, original_amount_cents: 9000, discount_cents: 0, quote_token: 'fresh-quote' });
    }
  };
  const page = loadPage('pages/order/confirm.js', app, {});
  page.data.product = { id: 1, price_cents: 8000, requiresUseDate: false };
  page.data.quoteReady = true;
  page.data.quoteToken = 'stale-quote';
  page.data.totalText = '80.00';
  page.data.originalTotalText = '80.00';
  page.orderRequestId = 'same-request-id';

  page.submit();
  await flush();
  await flush();

  assert.equal(page.data.orderRecoveryPending, false);
  assert.match(page.data.error, /价格或立减已更新|重新提交/);
  page.increase();
  assert.equal(page.data.quantity, 2);
  assert.ok(requests.some(item => item.url === '/order-quote'));
});

test('idempotency recovery errors never call payment and navigate to the existing order', async () => {
  for (const errorCode of ['idempotency_payload_mismatch', 'existing_order_recovery_required']) {
    let paymentCalls = 0;
    let destination = '';
    const app = {
      request: () => {
        const error = new Error('原请求需要恢复');
        error.data = { error_code: errorCode, order_no: 'ORD-EXISTING' };
        return Promise.reject(error);
      }
    };
    const page = loadPage('pages/order/confirm.js', app, {
      requestGuaranteeOrderPayment() { paymentCalls += 1; },
      redirectTo: ({ url }) => { destination = url; }
    });
    page.data.product = { id: 1, price_cents: 8000, requiresUseDate: false };
    page.data.quoteReady = true;
    page.data.quoteToken = 'quote-1';
    page.orderRequestId = 'same-request-id';

    page.submit();
    await flush();

    assert.equal(paymentCalls, 0);
    assert.equal(destination, '/pages/order/detail?order_no=ORD-EXISTING');
  }
});

test('detail page reports unavailable API, missing fields, and synchronous SDK errors without changing order status', () => {
  const app = { request: () => Promise.resolve({}) };
  const page = loadPage('pages/order/detail.js', app, {});
  page.data.order = { order_id: 'order-1', pay_token: 'pay-token' };
  page.data.status = 'unpaid';

  page.continuePayment();
  assert.equal(page.data.paying, false);
  assert.equal(page.data.status, 'unpaid');
  assert.match(page.data.error, /暂不支持小红书支付/);

  const missingFields = loadPage('pages/order/detail.js', app, {});
  missingFields.data.order = { order_id: 'order-1' };
  missingFields.data.status = 'unpaid';
  missingFields.continuePayment();
  assert.equal(missingFields.data.status, 'unpaid');
  assert.match(missingFields.data.error, /支付信息不完整/);

  const throwing = loadPage('pages/order/detail.js', app, {
    requestGuaranteeOrderPayment() { throw new Error('SDK is unavailable'); }
  });
  throwing.data.order = { order_id: 'order-1', pay_token: 'pay-token' };
  throwing.data.status = 'unpaid';
  throwing.continuePayment();
  assert.equal(throwing.data.paying, false);
  assert.equal(throwing.data.status, 'unpaid');
  assert.match(throwing.data.error, /支付未完成/);
});

test('missing payment fields remain visible after an unpaid order refresh', async () => {
  const app = {
    request: () => Promise.resolve({ order_id: 'order-1', status: 'unpaid', amount_cents: 8000 })
  };
  const page = loadPage('pages/order/detail.js', app, {});
  page.orderNo = 'ORD-1';
  page.data.order = { order_id: 'order-1' };

  page.continuePayment();
  page.loadOrder();
  await flush();

  assert.equal(page.data.status, 'unpaid');
  assert.match(page.data.error, /支付信息不完整/);
});

test('detail polling cannot clear payment feedback or re-enable payment while the SDK is active', async () => {
  let paymentCalls = 0;
  let paymentOptions;
  const app = {
    request: () => Promise.resolve({ order_id: 'order-1', pay_token: 'pay-token', status: 'unpaid', amount_cents: 8000 })
  };
  const xhs = {
    requestGuaranteeOrderPayment(options) {
      paymentCalls += 1;
      paymentOptions = options;
    }
  };
  const page = loadPage('pages/order/detail.js', app, xhs);
  page.orderNo = 'ORD-1';
  page.data.order = { order_id: 'order-1', pay_token: 'pay-token' };

  page.continuePayment();
  page.loadOrder();
  await flush();

  assert.equal(page.data.paying, true);
  page.continuePayment();
  assert.equal(paymentCalls, 1);

  paymentOptions.fail({ errMsg: 'requestGuaranteeOrderPayment:fail cancel' });
  paymentOptions.complete();
  await flush();
  assert.equal(page.data.paying, false);
  assert.match(page.data.error, /取消支付/);
});

test('payment SDK success only refreshes the server order and never locally marks it paid', async () => {
  let paymentOptions;
  let orderRequests = 0;
  const app = {
    request: () => {
      orderRequests += 1;
      return Promise.resolve({ order_id: 'order-1', pay_token: 'pay-token', status: 'unpaid', amount_cents: 8000 });
    }
  };
  const xhs = { requestGuaranteeOrderPayment: options => { paymentOptions = options; } };
  const page = loadPage('pages/order/detail.js', app, xhs);
  page.orderNo = 'ORD-1';
  page.data.order = { order_id: 'order-1', pay_token: 'pay-token' };

  page.continuePayment();
  paymentOptions.success();
  paymentOptions.complete();
  await flush();

  assert.equal(orderRequests, 1);
  assert.equal(page.data.status, 'unpaid');
  assert.equal(page.data.paying, true);
  assert.match(page.data.error, /正在核实订单状态/);
});

test('SDK complete without a result keeps the order in confirmation instead of clearing the prompt', async () => {
  let paymentOptions;
  const app = {
    request: () => Promise.resolve({ order_id: 'order-1', pay_token: 'pay-token', status: 'unpaid', amount_cents: 8000 })
  };
  const xhs = { requestGuaranteeOrderPayment: options => { paymentOptions = options; } };
  const page = loadPage('pages/order/detail.js', app, xhs);
  page.orderNo = 'ORD-1';
  page.data.order = { order_id: 'order-1', pay_token: 'pay-token' };

  page.continuePayment();
  paymentOptions.complete();
  await flush();

  assert.equal(page.data.status, 'unpaid');
  assert.equal(page.data.paying, true);
  assert.match(page.data.error, /支付结果正在确认/);
});

test('a paid server order clears old payment feedback', async () => {
  const app = {
    request: () => Promise.resolve({ order_id: 'order-1', status: 'paid', amount_cents: 8000 })
  };
  const page = loadPage('pages/order/detail.js', app, {});
  page.orderNo = 'ORD-1';
  page.paymentFeedback = '支付结果正在确认，订单已保留，请稍候';
  page.awaitingPaymentConfirmation = true;

  page.loadOrder();
  await flush();

  assert.equal(page.data.status, 'paid');
  assert.equal(page.data.paying, false);
  assert.equal(page.data.error, '');
});

test('a failed confirmation query unlocks retry and a later refresh can recover', async () => {
  let attempts = 0;
  const app = {
    request: () => {
      attempts += 1;
      if (attempts === 1) return Promise.reject(new Error('订单查询失败，请稍后重试'));
      return Promise.resolve({ order_id: 'order-1', pay_token: 'pay-token', status: 'unpaid', amount_cents: 8000 });
    }
  };
  const xhs = {
    requestGuaranteeOrderPayment(options) {
      options.success();
      options.complete();
    }
  };
  const page = loadPage('pages/order/detail.js', app, xhs);
  page.orderNo = 'ORD-1';
  page.data.order = { order_id: 'order-1', pay_token: 'pay-token' };

  page.continuePayment();
  await flush();
  assert.equal(page.data.paying, false);
  assert.match(page.data.error, /订单查询失败/);

  page.retry();
  await flush();
  assert.equal(page.data.paying, false);
  assert.equal(page.data.status, 'unpaid');
  assert.equal(page.data.error, '');
});
