const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');
const validIdCard = '11010519491231002X';

function flush() { return new Promise(resolve => setImmediate(resolve)); }

function loadPage(relativePath, request, xhs = {}) {
  let definition;
  const app = { globalData: {}, setNavigationTitle() {}, setStoreName() {}, request };
  vm.runInNewContext(fs.readFileSync(path.join(miniappRoot, relativePath), 'utf8'), {
    Page(value) { definition = value; }, getApp: () => app, xhs,
    require: relative => require(path.resolve(path.dirname(path.join(miniappRoot, relativePath)), relative)),
    Date, Math, Number, String, Boolean, RegExp, encodeURIComponent,
    setTimeout: () => 1, clearTimeout() {}
  }, { filename: relativePath });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (update, callback) => { Object.assign(page.data, update); if (callback) callback(); };
  return page;
}

function confirmation(request = () => Promise.resolve({}), xhs = {}) {
  const page = loadPage('pages/order/confirm.js', request, xhs);
  Object.assign(page.data, {
    product: { id: 7, price_cents: 0, contact_real_name_required: true },
    quoteReady: true, quoteToken: 'server-quote', guestName: '订单联系人', contactPhone: '13800138000',
    quantity: 3, contactIdCard: validIdCard
  });
  page.orderRequestId = 'stable-order-request';
  return page;
}

function withChecksum(prefix) {
  const weights = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2];
  const sum = weights.reduce((value, weight, index) => value + Number(prefix[index]) * weight, 0);
  return prefix + '10X98765432'[sum % 11];
}

test('catalog enables the single contact identity only for an explicit independent true flag', async () => {
  for (const flag of [undefined, false, true, 'true']) {
    const page = confirmation(url => Promise.resolve(url === '/catalog'
      ? { products: [{ id: 7, product_kind: 'ticket', price_cents: 0, real_name_required: true, contact_real_name_required: flag }] }
      : { amount_cents: 0, original_amount_cents: 0, discount_cents: 0, quote_token: 'zero-quote' }));
    page.mappingId = 7;
    page.loadProduct();
    await flush();
    assert.equal(page.data.product.contact_real_name_required, flag === true);
    assert.equal(page.data.product.priceText, '0.00');
    assert.equal(page.data.totalText, '0.00');
  }
  const template = fs.readFileSync(path.join(miniappRoot, 'pages/order/confirm.xhsml'), 'utf8');
  assert.equal((template.match(/bindinput="onContactIdCardInput"/g) || []).length, 1);
  assert.match(template, /xhs:if="\{\{product.contact_real_name_required\}\}"/);
  assert.match(template, /不代表逐游客实名/);
});

test('contact identity rejects invalid dates, sequence, region and check digit', () => {
  const page = confirmation();
  assert.equal(page.isValidContactIdCard(` ${validIdCard.toLowerCase()} `), true);
  assert.equal(page.isValidContactIdCard(withChecksum('11010520000229001')), true);
  for (const value of ['', '110105491231002', '00010519491231002X', '110105194912310020',
    withChecksum('11010520010229001'), withChecksum('11010518991231001'),
    withChecksum('11010529991231001'), withChecksum('11010519491231000')]) {
    assert.equal(page.isValidContactIdCard(value), false, value);
  }
});

test('required contact identity validates before creating and sends one normalized identity for multiple units', async () => {
  const writes = [];
  const page = confirmation((url, options) => {
    writes.push({ url, payload: options.data });
    return Promise.resolve({ order_no: 'ORD-IDENTITY', status: 'unpaid' });
  });
  page.data.contactIdCard = '';
  page.submit();
  assert.equal(writes.length, 0);
  assert.match(page.data.error, /有效的联系人身份证/);
  page.data.contactIdCard = ` ${validIdCard.toLowerCase()} `;
  await page.submit();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].payload.contact_id_card, validIdCard);
  assert.equal(writes[0].payload.quantity, 3);
  assert.equal(writes[0].payload.guest_name, '订单联系人');
  assert.equal(Object.hasOwn(writes[0].payload, 'visitors'), false);
  assert.equal(page.data.contactIdCard, '');
});

test('disabled contact identity never submits a stale identity or treats visitor real-name as the contact flag', async () => {
  for (const flag of [false, undefined]) {
    let payload;
    const page = confirmation((url, options) => { payload = options.data; return Promise.resolve({ order_no: 'ORD-OPTIONAL', status: 'unpaid' }); });
    page.data.product = { id: 7, real_name_required: true, contact_real_name_required: flag };
    await page.submit();
    assert.equal(Object.hasOwn(payload, 'contact_id_card'), false);
    assert.equal(page.data.contactIdCard, '');
  }
});

test('unknown creation retries the frozen contact identity only in page memory and clears it on success', async () => {
  const payloads = [];
  const page = confirmation((url, options) => {
    payloads.push(options.data);
    return payloads.length === 1 ? Promise.reject(new Error('network unavailable'))
      : Promise.resolve({ order_no: 'ORD-RECOVERED', status: 'unpaid', order_id: 'platform-id', pay_token: 'platform-token' });
  }, {
    setStorageSync() { assert.fail('contact identity must not enter storage'); },
    requestGuaranteeOrderPayment(options) { options.fail({ errMsg: 'cancel' }); options.complete(); }
  });
  await page.submit();
  assert.equal(page.data.orderRecoveryPending, true);
  page.onContactIdCardInput({ detail: { value: withChecksum('11010520000229001') } });
  await page.submit();
  assert.deepEqual(payloads[1], payloads[0]);
  assert.equal(payloads[1].contact_id_card, validIdCard);
  assert.equal(page.orderPayload, null);
  assert.equal(page.orderRecoverySnapshot, null);
  assert.equal(page.data.contactIdCard, '');
});

test('explicit not-created response clears the identity and requires re-entry', async () => {
  let creates = 0;
  const page = confirmation(url => {
    if (url !== '/orders') return Promise.resolve({ amount_cents: 0, original_amount_cents: 0, discount_cents: 0, quote_token: 'fresh-quote' });
    creates += 1;
    const error = new Error('订单未创建');
    error.data = { error_code: 'order_not_created' };
    return Promise.reject(error);
  });
  await page.submit();
  await flush();
  assert.equal(page.data.contactIdCard, '');
  assert.equal(page.data.orderRecoveryPending, false);
  assert.equal(page.orderPayload, null);
  page.submit();
  assert.equal(creates, 1);
  assert.match(page.data.error, /有效的联系人身份证/);
});

test('existing-order recovery and unload clear all page identity copies', async () => {
  const page = confirmation(() => {
    const error = new Error('已存在订单');
    error.data = { error_code: 'idempotency_payload_mismatch', order_no: 'OTHER-ORDER' };
    return Promise.reject(error);
  }, { redirectTo() {} });
  await page.submit();
  assert.equal(page.data.contactIdCard, '');
  assert.equal(page.orderPayload, null);
  assert.equal(page.orderRecoverySnapshot, null);
  page.data.contactIdCard = validIdCard;
  page.orderPayload = { contact_id_card: validIdCard };
  page.orderRecoverySnapshot = { quantity: 3 };
  page.onUnload();
  assert.equal(page.data.contactIdCard, '');
  assert.equal(page.orderPayload, null);
  assert.equal(page.orderRecoverySnapshot, null);
});

test('zero-price order creation still uses platform payment fields and real status', async () => {
  for (const response of [
    { status: 'unpaid', order_id: 'platform-id', pay_token: 'platform-token' },
    { status: 'paid', order_id: 'platform-id', pay_token: 'platform-token' },
    { status: 'paid', core_order_status: 'unpaid', order_id: 'platform-id', pay_token: 'platform-token' },
    { status: 'unpaid', order_id: 'platform-id' },
    { order_id: 'platform-id', pay_token: 'platform-token' }
  ]) {
    let creates = 0;
    let payments = 0;
    let destination = '';
    const page = confirmation((url, options) => {
      assert.equal(url, '/orders');
      assert.equal(options.data.quote_token, 'server-quote');
      creates += 1;
      return Promise.resolve({ order_no: 'ORD-ZERO', amount_cents: 0, ...response });
    }, {
      requestGuaranteeOrderPayment(options) { payments += 1; options.fail({ errMsg: 'cancel' }); options.complete(); },
      redirectTo({ url }) { destination = url; }
    });
    await page.submit();
    const status = response.core_order_status || response.status;
    assert.equal(creates, 1);
    assert.equal(payments, status === 'unpaid' && Boolean(response.pay_token) ? 1 : 0);
    assert.equal(destination, status === 'paid' ? '/pages/order/detail?order_no=ORD-ZERO' : '');
    assert.equal(page.data.createdOrderNo, 'ORD-ZERO');
    assert.equal(Object.hasOwn(page.data, 'ticketCodes'), false);
    if (!response.pay_token || !status) assert.ok(page.data.error);
  }
});

test('SDK success for a zero-price order only opens the existing order for server confirmation', async () => {
  let destination = '';
  const page = confirmation(() => Promise.resolve({ order_no: 'ORD-ZERO', status: 'unpaid', amount_cents: 0, order_id: 'platform-id', pay_token: 'platform-token' }), {
    requestGuaranteeOrderPayment(options) { options.success({}); options.complete(); },
    redirectTo({ url }) { destination = url; }
  });
  await page.submit();
  assert.equal(destination, '/pages/order/detail?order_no=ORD-ZERO');
  assert.equal(Object.hasOwn(page.data, 'status'), false);
  assert.equal(Object.hasOwn(page.data, 'ticketCodes'), false);
});

test('zero-price order queries keep an unconfirmed order unpaid and wait for real voucher issuance after payment', async () => {
  for (const status of ['unpaid', 'paid']) {
    const page = loadPage('pages/order/detail.js', () => Promise.resolve({
      order_no: 'ORD-ZERO', status, core_order_status: status, amount_cents: 0,
      order_id: 'platform-id', product_kind: 'ticket', ticket_codes: [], ticket_issuance_status: 'pending'
    }));
    page.onLoad({ order_no: 'ORD-ZERO' });
    await flush();
    assert.equal(page.data.status, status);
    assert.equal(page.data.order.amountText, '0.00');
    assert.equal(page.data.ticketCodes.length, 0);
    assert.equal(page.data.issuancePending, status === 'paid');
  }
});

test('production-blocked zero-price product stays visibly zero and cannot quote or buy', async () => {
  const product = { id: 7, product_kind: 'ticket', price_cents: 0, purchase_blocked_reason: '零元商品暂未开放购买', tags: [] };
  const request = url => {
    assert.equal(url, '/catalog');
    return Promise.resolve({ products: [{ ...product }] });
  };
  const detail = loadPage('pages/product/detail.js', request, { navigateTo() { assert.fail('blocked product cannot navigate to purchase'); } });
  detail.mappingId = 7;
  await detail.loadProduct();
  assert.equal(detail.data.product.displayPriceText, '0');
  detail.buy();
  assert.equal(detail.data.error, product.purchase_blocked_reason);
  const page = confirmation(request);
  page.mappingId = 7;
  page.loadProduct();
  await flush();
  assert.equal(page.data.product.priceText, '0.00');
  assert.equal(page.data.totalText, '0.00');
  assert.equal(page.data.quoteReady, false);
  page.submit();
  assert.equal(page.data.error, product.purchase_blocked_reason);
  for (const name of ['pages/product/detail.xhsml', 'pages/order/confirm.xhsml']) {
    assert.match(fs.readFileSync(path.join(miniappRoot, name), 'utf8'), /\{\{product.purchase_blocked_reason\}\}/);
  }
});
