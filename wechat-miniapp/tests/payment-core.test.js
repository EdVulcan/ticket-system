'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { memoryDb } = require('./helpers/memory-db');
const { createCore, succeeded, normalizePayment, normalizeRefund, validatePayment, money } = require('../cloud-shared/payment-core');
process.env.WECHAT_PAY_APPID = 'buyer-app';
process.env.WECHAT_PAY_MCH_ID = 'parent-merchant';
process.env.WECHAT_PAY_SUB_MCH_ID = 'store-merchant';
process.env.CLOUDBASE_ENV_ID = 'test-env';
const ok = { returnCode: 'SUCCESS', resultCode: 'SUCCESS' };
function paymentResponse(change = {}) {
  return Object.assign({}, ok, { appid: 'parent-app', mchId: 'parent-merchant', openid: 'parent-user', subAppid: 'buyer-app', subMchId: 'store-merchant', subOpenid: 'buyer', outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction', totalFee: 1200, tradeState: 'SUCCESS' }, change);
}
function refundResponse(change = {}) {
  return Object.assign({}, paymentResponse(), { refundCount: 2, outRefundNo_0: 'other-refund', refundFee_0: 10, refundStatus_0: 'SUCCESS', refundId_0: 'other', outRefundNo_1: 'SGREF_order1', refundFee_1: 1200, refundStatus_1: 'SUCCESS', refundId_1: 'wx-refund' }, change);
}
function fixture(change = {}, provider = {}, cloudOptions = {}) {
  const order = Object.assign({ _id: 'order1', orderNo: 'order1', storeId: 'store_001', userId: 'buyer', payableAmount: 1200, status: 'WAIT_PAY', expireAt: new Date(Date.now() + 600000), itemsSnapshot: [{ productId: 'p1', quantity: 1 }, { productId: 'p1', quantity: 2 }], inventoryRestored: false, couponSnapshot: { id: 'coupon1' }, payment: { outTradeNo: 'SGPAY_order1', attempted: true, transactionId: '' } }, change);
  const db = memoryDb({ orders: [order], products: [{ _id: 'p1', stock: 2, stockMode: 'LIMITED', isSoldOut: false }], user_coupons: [{ _id: 'coupon1', userId: 'buyer', status: 'LOCKED', lockedOrderId: 'order1' }] });
  const calls = [];
  const cloudPay = Object.fromEntries(['queryOrder', 'closeOrder', 'unifiedOrder', 'refund', 'queryRefund'].map((method) => [method, async (args) => { assert.match(args.nonceStr, /^[a-f0-9]{32}$/); calls.push({ method, args }); return provider[method] ? provider[method](args) : {}; }]));
  return { db, calls, core: createCore(Object.assign({ cloudPay, DYNAMIC_CURRENT_ENV: 'test-env' }, cloudOptions), db) };
}
test('provider response requires BOTH explicit SUCCESS codes', () => {
  for (const value of [null, {}, { returnCode: 'SUCCESS' }, { resultCode: 'SUCCESS' }, { returnCode: 'FAIL', resultCode: 'SUCCESS' }, { data: {} }]) assert.equal(succeeded(value), false);
  assert.equal(succeeded(ok), true);
  assert.equal(succeeded({ result: ok }), true);
});
test('sub-merchant, sub-app and sub-openid override provider identities', () => {
  const value = normalizePayment(paymentResponse());
  assert.equal(value.appid, 'buyer-app'); assert.equal(value.mchid, 'store-merchant'); assert.equal(value.payer.openid, 'buyer');
  validatePayment(fixture().db.read('orders', 'order1'), value, { appId: 'buyer-app', subMchId: 'store-merchant' });
});
test('missing or malformed monetary fields are never inferred/coerced', () => {
  for (const value of [undefined, null, '', false, [], {}, '1e3', '1.0', -1, 0.1, Number.MAX_SAFE_INTEGER + 1]) assert.throws(() => money(value));
  assert.equal(money('1200'), 1200);
  assert.equal(normalizePayment({}).amount.total, undefined);
});
test('indexed refund response selects matching ID, not first refund', () => {
  const value = normalizeRefund(refundResponse(), 'SGREF_order1');
  assert.equal(value.amount.refund, 1200); assert.equal(value.refund_id, 'wx-refund');
  assert.throws(() => normalizeRefund(refundResponse(), 'missing'), /REFUND_ORDER_MISMATCH/);
  assert.throws(() => normalizeRefund({}, 'SGREF_order1'), /REFUND_RESPONSE_INVALID/);
});
test('refund normalizer does not fill missing amount/transaction/merchant from order', () => {
  const value = normalizeRefund({ outRefundNo: 'r', refundStatus: 'SUCCESS' }, 'r');
  assert.equal(value.amount.refund, undefined); assert.equal(value.amount.total, undefined); assert.equal(value.transaction_id, undefined); assert.equal(value.mchid, undefined);
});
test('payment callback commits coupon and order once', async () => {
  const { core, db } = fixture();
  await core.applyPayment('order1', normalizePayment(paymentResponse()));
  assert.equal(db.read('orders', 'order1').status, 'PAID');
  assert.equal(db.read('user_coupons', 'coupon1').status, 'USED');
  assert.equal((await core.applyPayment('order1', normalizePayment(paymentResponse()))).duplicate, true);
  assert.equal(db.all('payment_records').length, 1);
});
test('paid transition sends one staff notice and duplicate callbacks do not resend it', async () => {
  const sent = [];
  const previousTemplateId = process.env.WECHAT_ORDER_NOTICE_TEMPLATE_ID;
  process.env.WECHAT_ORDER_NOTICE_TEMPLATE_ID = 'notice-template';
  try {
    const { core, db } = fixture({}, {}, { openapi: { subscribeMessage: { send: async payload => sent.push(payload) } } });
    await db.collection('staff').doc('staff1').set({ data: { _id: 'staff1', userId: 'owner', storeId: 'store_001', enabled: true, permissions: ['ORDER_MANAGE'] } });
    const result = await core.applyPayment('order1', normalizePayment(paymentResponse()));
    await core.applyPayment('order1', normalizePayment(paymentResponse()));
    assert.equal(result.status, 'PAID');
    assert.equal(sent.length, 1);
    assert.equal(sent[0].touser, 'owner');
    assert.equal(sent[0].templateId, 'notice-template');
  } finally {
    if (previousTemplateId === undefined) delete process.env.WECHAT_ORDER_NOTICE_TEMPLATE_ID;
    else process.env.WECHAT_ORDER_NOTICE_TEMPLATE_ID = previousTemplateId;
  }
});
for (const change of [{ totalFee: 1 }, { subAppid: 'attacker' }, { subMchId: 'parent-merchant' }, { subOpenid: 'other' }, { transactionId: '' }, { outTradeNo: 'wrong' }, { feeType: 'USD' }]) {
  test(`invalid verified payment is rolled back: ${JSON.stringify(change)}`, async () => {
    const { core, db } = fixture();
    await assert.rejects(core.applyPayment('order1', normalizePayment(paymentResponse(change))));
    assert.equal(db.read('orders', 'order1').status, 'WAIT_PAY'); assert.equal(db.all('payment_records').length, 0);
  });
}
test('missing coupon aborts payment transaction without partial writes', async () => {
  const { core, db } = fixture({ couponSnapshot: { id: 'missing' } });
  await assert.rejects(core.applyPayment('order1', normalizePayment(paymentResponse())), /COUPON_STATE_INVALID/);
  assert.equal(db.read('orders', 'order1').status, 'WAIT_PAY');
});
test('payment notification after refund preserves ledger and refund state', async () => {
  const { core, db } = fixture({ status: 'REFUNDED', payment: { outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction' } });
  await db.collection('payment_records').doc('SGPAY_order1').set({ data: { status: 'SUCCESS', marker: 'preserve' } });
  await core.applyPayment('order1', normalizePayment(paymentResponse()));
  assert.equal(db.read('orders', 'order1').status, 'REFUNDED'); assert.equal(db.read('payment_records', 'SGPAY_order1').marker, 'preserve');
});
for (const state of ['USERPAYING', 'REVOKED', 'PAYERROR', 'REFUND', 'NEW_UNDOCUMENTED_STATE', '']) {
  test(`uncertain provider state ${state} never releases stock or coupon`, async () => {
    const { core, db } = fixture({}, { queryOrder: () => paymentResponse({ tradeState: state }) });
    await assert.rejects(core.cancelUnpaid('order1', 'buyer'), /PAYMENT_PENDING_VERIFICATION/);
    assert.equal(db.read('orders', 'order1').status, 'WAIT_PAY'); assert.equal(db.read('products', 'p1').stock, 2); assert.equal(db.read('user_coupons', 'coupon1').status, 'LOCKED');
  });
}
test('empty close response is not a definitive close', async () => {
  const { core, db } = fixture({}, { queryOrder: () => paymentResponse({ tradeState: 'NOTPAY' }) });
  await assert.rejects(core.cancelUnpaid('order1', 'buyer'), /PAYMENT_PROVIDER_REJECTED/);
  assert.equal(db.read('orders', 'order1').status, 'WAIT_PAY'); assert.equal(db.read('products', 'p1').stock, 2);
});
test('cancel closes provider before restoring aggregate stock, exactly once', async () => {
  const { core, db, calls } = fixture({}, { queryOrder: () => paymentResponse({ tradeState: 'NOTPAY' }), closeOrder: () => { assert.equal(db.read('products', 'p1').stock, 2); return ok; } });
  assert.equal((await core.cancelUnpaid('order1', 'buyer')).status, 'CANCELED');
  assert.equal(db.read('products', 'p1').stock, 5); assert.equal(db.read('user_coupons', 'coupon1').status, 'AVAILABLE');
  await core.cancelUnpaid('order1', 'buyer');
  assert.equal(db.read('products', 'p1').stock, 5); assert.deepEqual(calls.map(x => x.method), ['queryOrder', 'closeOrder']);
});
test('cancel converts provider success to PAID without releasing reservations', async () => {
  const { core, db } = fixture({}, { queryOrder: () => paymentResponse() });
  assert.equal((await core.cancelUnpaid('order1', 'buyer')).status, 'PAID'); assert.equal(db.read('products', 'p1').stock, 2);
});
test('never-attempted payment can cancel locally, then creation is blocked', async () => {
  const { core, db, calls } = fixture({ payment: { outTradeNo: '' } });
  await core.cancelUnpaid('order1', 'buyer');
  await assert.rejects(core.createPayment('order1', 'buyer'), /ORDER_STATUS_INVALID/);
  assert.equal(db.read('orders', 'order1').status, 'CANCELED'); assert.equal(calls.length, 0);
});
test('provider ORDERNOTEXIST permits same-number payment retry but not resource release', async () => {
  const { core, db } = fixture({}, { queryOrder: () => ({ returnCode: 'SUCCESS', resultCode: 'FAIL', errCode: 'ORDERNOTEXIST' }) });
  const result = await core.queryPayment('order1', 'buyer');
  assert.equal(result.status, 'WAIT_PAY'); assert.equal(result.providerOrderMissing, true);
  assert.equal(db.read('orders', 'order1').payment.outTradeNo, 'SGPAY_order1'); assert.equal(db.read('products', 'p1').stock, 2);
  await assert.rejects(core.cancelUnpaid('order1', 'buyer'), /PAYMENT_PROVIDER_REJECTED/);
});
test('cancellation restores inventory without undoing manual sold-out setting', async () => {
  const { core, db } = fixture({ payment: { outTradeNo: '' } });
  await db.collection('products').doc('p1').update({ data: { manualSoldOut: true, isSoldOut: true } });
  await core.cancelUnpaid('order1', 'buyer');
  assert.equal(db.read('products', 'p1').stock, 5); assert.equal(db.read('products', 'p1').isSoldOut, true);
});
test('create timeout keeps durable intent; ORDERNOTEXIST never releases it', async () => {
  const { core, db } = fixture({ payment: { outTradeNo: '' } }, { unifiedOrder: () => { throw new Error('NETWORK_TIMEOUT'); }, queryOrder: () => ({ returnCode: 'SUCCESS', resultCode: 'FAIL', errCode: 'ORDERNOTEXIST' }) });
  await assert.rejects(core.createPayment('order1', 'buyer'), /NETWORK_TIMEOUT/);
  assert.equal(db.read('orders', 'order1').payment.attempted, true);
  await assert.rejects(core.cancelUnpaid('order1', 'buyer'), /PAYMENT_PROVIDER_REJECTED/);
  assert.equal(db.read('products', 'p1').stock, 2);
});
test('in-flight create response after cancellation does not expose payable params', async () => {
  let release; let entered;
  const waiting = new Promise(resolve => { entered = resolve; });
  const { core, db } = fixture({ payment: { outTradeNo: '' } }, {
    unifiedOrder: () => { entered(); return new Promise(resolve => { release = resolve; }); },
    queryOrder: () => paymentResponse({ tradeState: 'NOTPAY' }), closeOrder: () => ok
  });
  const creating = core.createPayment('order1', 'buyer');
  await waiting;
  await core.cancelUnpaid('order1', 'buyer');
  release(Object.assign({}, ok, { payment: { timeStamp: '123', nonceStr: 'n', package: 'prepay_id=p', paySign: 's', signType: 'MD5' } }));
  await assert.rejects(creating, /ORDER_STATUS_INVALID/); assert.equal(db.read('orders', 'order1').status, 'CANCELED');
});
test('callback while close is in flight wins over cancellation release', async () => {
  const { core, db } = fixture({}, { queryOrder: () => paymentResponse({ tradeState: 'NOTPAY' }), closeOrder: async () => { await core.applyPayment('order1', normalizePayment(paymentResponse())); return ok; } });
  assert.equal((await core.cancelUnpaid('order1', 'buyer')).status, 'PAID'); assert.equal(db.read('products', 'p1').stock, 2);
});
test('refund retry does not reset successful callback record', async () => {
  const { core, db, calls } = fixture({ status: 'PAID', payment: { outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction' } }, { refund: async () => { await core.applyRefund('order1', normalizeRefund(refundResponse(), 'SGREF_order1')); return ok; } });
  assert.equal((await core.createRefund('order1', 'owner')).status, 'REFUNDED');
  await core.createRefund('order1', 'owner');
  assert.equal(db.read('payment_records', 'SGREF_order1').status, 'SUCCESS'); assert.equal(calls.length, 1); assert.equal(db.all('operation_logs').length, 1);
});
test('automatic completion cannot overwrite refund status', async () => {
  const { core, db } = fixture({ status: 'REFUNDING', deliveringAt: new Date(0) });
  assert.equal(await core.completeDelivered('order1', new Date()), false); assert.equal(db.read('orders', 'order1').status, 'REFUNDING');
});
test('late verified money after cancellation enters automatic refund without consuming released resources', async () => {
  const { core, db, calls } = fixture({ status: 'CANCELED', inventoryRestored: true }, { refund: () => ok, queryRefund: () => refundResponse() });
  await db.collection('user_coupons').doc('coupon1').update({ data: { status: 'AVAILABLE' } });
  assert.equal((await core.applyPayment('order1', normalizePayment(paymentResponse()))).status, 'REFUNDING');
  assert.equal(db.read('products', 'p1').stock, 2); assert.equal(db.read('user_coupons', 'coupon1').status, 'AVAILABLE');
  assert.equal(calls[0].method, 'refund'); assert.equal(calls[0].args.refundFee, 1200);
  assert.equal((await core.queryRefund('order1')).status, 'REFUNDED');
  assert.equal(db.read('orders', 'order1').payment.reviewRequired, false);
});
test('zero-payment refund never invokes external payment provider', async () => {
  const { core, db, calls } = fixture({ status: 'PAID', payableAmount: 0, payment: { method: 'COUPON_ZERO' } });
  assert.equal((await core.createRefund('order1', 'owner')).status, 'REFUNDED');
  assert.equal(db.read('orders', 'order1').status, 'REFUNDED'); assert.equal(calls.length, 0);
});
test('closed refund becomes explicit failure, permits one CAS-protected new refund ID', async () => {
  const { core, db, calls } = fixture({ status: 'REFUNDING', payment: { outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction', outRefundNo: 'SGREF_order1', refundAmount: 1200 } }, { queryRefund: () => refundResponse({ refundStatus_1: 'REFUNDCLOSE' }), refund: () => ok });
  assert.equal((await core.queryRefund('order1')).status, 'REFUND_FAILED');
  assert.equal(db.read('payment_records', 'SGREF_order1').status, 'REFUND_FAILED');
  await assert.rejects(core.createRefund('order1', 'owner'), /REFUND_NEW_ATTEMPT_REQUIRED/);
  const options = { newAttempt: true, previousRefundNo: 'SGREF_order1' };
  const result = await core.createRefund('order1', 'owner', options);
  assert.equal(result.outRefundNo, 'SGREF_order1_2');
  await core.createRefund('order1', 'owner', options);
  assert.equal(db.read('orders', 'order1').payment.outRefundNo, 'SGREF_order1_2');
  assert.equal(db.read('payment_records', 'SGREF_order1').status, 'REFUND_FAILED');
  assert.equal(calls.filter(item => item.method === 'refund').every(item => item.args.outRefundNo === 'SGREF_order1_2'), true);
});
test('abnormal refund requires review and forbids automatic or new-ID resubmission', async () => {
  const { core, db, calls } = fixture({ status: 'REFUNDING', payment: { outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction', outRefundNo: 'SGREF_order1', refundAmount: 1200 } }, { queryRefund: () => refundResponse({ refundStatus_1: 'CHANGE' }) });
  assert.equal((await core.queryRefund('order1')).status, 'REFUND_REVIEW');
  assert.equal(db.read('orders', 'order1').payment.reviewRequired, true);
  await assert.rejects(core.createRefund('order1', 'owner', { newAttempt: true, previousRefundNo: 'SGREF_order1' }), /REFUND_STATUS_INVALID/);
  assert.equal(calls.filter(item => item.method === 'refund').length, 0);
});
test('exceptional refund can reconcile to success without creating another refund', async () => {
  const { core, db } = fixture({ status: 'REFUND_REVIEW', payment: { outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction', outRefundNo: 'SGREF_order1', refundAmount: 1200 } }, { queryRefund: () => refundResponse() });
  assert.equal((await core.queryRefund('order1')).status, 'REFUNDED'); assert.equal(db.read('orders', 'order1').payment.reviewRequired, false);
});
test('11th refund attempt queries its exact refund number, not the first page of payment refunds', async () => {
  const outRefundNo = 'SGREF_order1_11';
  const { core, db } = fixture({ status: 'REFUNDING', payment: { outTradeNo: 'SGPAY_order1', transactionId: 'wx-transaction', outRefundNo, refundAmount: 1200, refundAttempt: 11 } }, { queryRefund: args => {
    assert.equal(args.outRefundNo, outRefundNo); assert.equal(args.outTradeNo, undefined);
    return Object.assign({}, paymentResponse(), { refundCount: 1, outRefundNo_0: outRefundNo, refundFee_0: 1200, refundStatus_0: 'SUCCESS', refundId_0: 'wx-refund-11' });
  } });
  assert.equal((await core.queryRefund('order1')).status, 'REFUNDED'); assert.equal(db.read('orders', 'order1').payment.refundId, 'wx-refund-11');
});
test('dynamic environment Symbol is never passed as actual CloudPay envId', async () => {
  const previous = process.env.CLOUDBASE_ENV_ID; delete process.env.CLOUDBASE_ENV_ID;
  try { const { core, db, calls } = fixture({ payment: { outTradeNo: '' } }); await assert.rejects(core.createPayment('order1', 'buyer'), /PAYMENT_ENV_NOT_CONFIGURED/); assert.equal(calls.length, 0); assert.equal(db.read('orders', 'order1').payment.attempted, undefined); }
  finally { process.env.CLOUDBASE_ENV_ID = previous; }
});
test('MVCC double rejects conflicting commits (doc-only test surface)', async () => {
  const { db } = fixture(); const a = await db.startTransaction(); const b = await db.startTransaction();
  assert.equal(a.collection('orders').where, undefined);
  await a.collection('orders').doc('order1').get();
  await b.collection('orders').doc('order1').update({ data: { status: 'REFUNDING' } }); await b.commit();
  await a.collection('orders').doc('order1').update({ data: { status: 'COMPLETED' } });
  await assert.rejects(a.commit(), /TRANSACTION_CONFLICT/); assert.equal(db.read('orders', 'order1').status, 'REFUNDING');
});
