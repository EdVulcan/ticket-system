const { test } = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const { memoryDb } = require('./helpers/memory-db');
const { loadFunction } = require('./helpers/load-function');
process.env.WECHAT_PAY_APPID = 'app';
process.env.WECHAT_PAY_SUB_MCH_ID = 'merchant';
process.env.WECHAT_PAY_MCH_ID = 'provider-merchant';
const key = crypto.randomBytes(32);
const { privateKey, publicKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
process.env.WECHAT_PAY_API_V3_KEY_BASE64 = key.toString('base64');
process.env.WECHAT_PAY_PLATFORM_CERTIFICATE = publicKey.export({ type: 'spki', format: 'pem' });
process.env.WECHAT_PAY_PLATFORM_SERIAL_NO = 'test-serial';
function fixture(status = 'WAIT_PAY', provider = {}) {
  const db = memoryDb({ orders: [{ _id: 'order', orderNo: 'order', storeId: 'store_001', userId: 'buyer', status, payableAmount: 1200, payment: { outTradeNo: 'trade', transactionId: status === 'REFUNDING' ? 'transaction' : '', outRefundNo: status === 'REFUNDING' ? 'refund' : '', refundAmount: 1200 } }] });
  return { db, main: loadFunction('paymentCallback', db, {}, provider) };
}
function signed(eventType, resource, timestamp = String(Math.floor(Date.now() / 1000))) {
  const nonce = '123456789012'; const aad = 'notification';
  const cipher = crypto.createCipheriv('aes-256-gcm', key, Buffer.from(nonce)); cipher.setAAD(Buffer.from(aad));
  const ciphertext = Buffer.concat([cipher.update(JSON.stringify(resource)), cipher.final(), cipher.getAuthTag()]).toString('base64');
  const body = JSON.stringify({ id: 'notification-id', event_type: eventType, resource: { algorithm: 'AEAD_AES_256_GCM', ciphertext, nonce, associated_data: aad } });
  const requestNonce = 'signature-nonce';
  const signature = crypto.sign('RSA-SHA256', Buffer.from(`${timestamp}\n${requestNonce}\n${body}\n`), privateKey).toString('base64');
  return { body, headers: { 'Wechatpay-Timestamp': timestamp, 'Wechatpay-Nonce': requestNonce, 'Wechatpay-Signature': signature, 'Wechatpay-Serial': 'test-serial' } };
}
function payment() { return { appid: 'app', mchid: 'merchant', payer: { openid: 'buyer' }, out_trade_no: 'trade', transaction_id: 'transaction', trade_state: 'SUCCESS', amount: { total: 1200, currency: 'CNY' } }; }
test('valid RSA/AES-GCM v3 payment returns HTTP ack only after commit', async () => {
  const { db, main } = fixture(); const request = signed('TRANSACTION.SUCCESS', payment());
  assert.equal((await main(request)).statusCode, 200); assert.equal(db.read('orders', 'order').status, 'PAID');
  assert.equal((await main(request)).statusCode, 200); assert.equal(db.all('payment_records').length, 1);
});
test('service-provider v3 fields normalize sub_appid/sub_mchid/payer.sub_openid', async () => {
  const { db, main } = fixture(); const resource = payment(); delete resource.appid; delete resource.mchid;
  Object.assign(resource, { sp_appid: 'provider-app', sp_mchid: 'provider-merchant', sub_appid: 'app', sub_mchid: 'merchant', payer: { sp_openid: 'provider-user', sub_openid: 'buyer' } });
  assert.equal((await main(signed('TRANSACTION.SUCCESS', resource))).statusCode, 200); assert.equal(db.read('orders', 'order').status, 'PAID');
});
test('standard v3 refund without appid/mchid is bound by authenticated IDs and exact amounts', async () => {
  const { db, main } = fixture('REFUNDING');
  const resource = { out_trade_no: 'trade', transaction_id: 'transaction', out_refund_no: 'refund', refund_id: 'wx-refund', refund_status: 'SUCCESS', amount: { total: 1200, refund: 1200, currency: 'CNY' } };
  assert.equal((await main(signed('REFUND.SUCCESS', resource))).statusCode, 200); assert.equal(db.read('orders', 'order').status, 'REFUNDED');
});
for (const type of ['tamper', 'expired', 'wrong-serial', 'wrong-amount', 'wrong-payer']) {
  test(`reject ${type} callback without modifying order`, async () => {
    const { db, main } = fixture(); const resource = payment();
    if (type === 'wrong-amount') resource.amount.total = 1;
    if (type === 'wrong-payer') resource.payer.openid = 'attacker';
    const request = signed('TRANSACTION.SUCCESS', resource, type === 'expired' ? '1' : undefined);
    if (type === 'tamper') request.body += ' ';
    if (type === 'wrong-serial') request.headers['Wechatpay-Serial'] = 'other';
    assert.equal((await main(request)).statusCode, 500); assert.equal(db.read('orders', 'order').status, 'WAIT_PAY'); assert.equal(db.all('payment_records').length, 0);
  });
}
test('legacy callback re-queries provider with nonce and returns errcode ACK', async () => {
  const { db, main } = fixture('WAIT_PAY', { queryOrder: async args => { assert.match(args.nonceStr, /^[a-f0-9]{32}$/); return { returnCode: 'SUCCESS', resultCode: 'SUCCESS', subAppid: 'app', subMchId: 'merchant', subOpenid: 'buyer', outTradeNo: 'trade', transactionId: 'transaction', tradeState: 'SUCCESS', totalFee: 1200 }; } });
  const result = await main({ outTradeNo: 'trade', totalFee: 1, tradeState: 'FAIL' });
  assert.equal(result.errcode, 0); assert.equal(result.errmsg, ''); assert.equal(db.read('orders', 'order').status, 'PAID');
});
test('legacy indexed refund uses SDK camel_0 field shape', async () => {
  const { db, main } = fixture('REFUNDING', { queryRefund: async args => { assert.match(args.nonceStr, /^[a-f0-9]{32}$/); return { returnCode: 'SUCCESS', resultCode: 'SUCCESS', subAppid: 'app', subMchId: 'merchant', outTradeNo: 'trade', transactionId: 'transaction', totalFee: 1200, refundCount: 1, outRefundNo_0: 'refund', refundId_0: 'wx-refund', refundStatus_0: 'SUCCESS', refundFee_0: 1200 }; } });
  assert.equal((await main({ outTradeNo: 'trade' })).errcode, 0); assert.equal(db.read('orders', 'order').status, 'REFUNDED');
});
