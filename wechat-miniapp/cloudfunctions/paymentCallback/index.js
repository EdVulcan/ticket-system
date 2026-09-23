const crypto = require('crypto');
const cloud = require('wx-server-sdk');

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database();

const STORE_ID = 'store_001';

function fail(message) {
  const error = new Error(message);
  error.code = message;
  throw error;
}

function config(requireSignature) {
  const appId = process.env.WECHAT_PAY_APPID || process.env.WECHAT_PAY_APP_ID;
  const mchId = process.env.WECHAT_PAY_MCH_ID;
  const apiV3Key = process.env.WECHAT_PAY_API_V3_KEY || '';
  const apiV3KeyBase64 = process.env.WECHAT_PAY_API_V3_KEY_BASE64 || '';
  const certificate = process.env.WECHAT_PAY_PLATFORM_CERTIFICATE || process.env.WECHAT_PAY_PLATFORM_CERTIFICATE_BASE64;
  if (!appId || !mchId) fail('PAYMENT_NOT_CONFIGURED');
  const key = apiV3KeyBase64 ? Buffer.from(apiV3KeyBase64, 'base64') : Buffer.from(apiV3Key, 'utf8');
  if (requireSignature && ((!apiV3Key && !apiV3KeyBase64) || !certificate)) fail('PAYMENT_NOT_CONFIGURED');
  if ((apiV3Key || apiV3KeyBase64) && key.length !== 32) fail('PAYMENT_API_KEY_INVALID');
  return { appId, subAppid: appId, mchId, subMchId: process.env.WECHAT_PAY_SUB_MCH_ID || mchId, key, certificate, platformSerialNo: process.env.WECHAT_PAY_PLATFORM_SERIAL_NO || '' };
}

function header(headers, name) {
  const wanted = name.toLowerCase();
  const key = Object.keys(headers || {}).find((candidate) => candidate.toLowerCase() === wanted);
  return key ? headers[key] : '';
}

function requestBody(event) {
  const headers = event.headers || event.header || {};
  let body = typeof event.body === 'string' ? event.body : typeof event.rawBody === 'string' ? event.rawBody : '';
  if (body && event.isBase64Encoded) body = Buffer.from(body, 'base64').toString('utf8');
  if (!body && typeof event.data === 'string') body = event.data;
  if (!body && event.body && typeof event.body === 'object') body = JSON.stringify(event.body);
  if (!body && event.resource) body = JSON.stringify(event);
  if (!body) fail('PAYMENT_BODY_MISSING');
  return { body, headers };
}

function hasHttpSignatureRequest(event) {
  const headers = event.headers || event.header || {};
  return Boolean(event.body || event.rawBody || event.isBase64Encoded || event.data || header(headers, 'Wechatpay-Signature'));
}

function certificatePem(value) {
  if (value.indexOf('BEGIN ') >= 0) return value.replace(/\\n/g, '\n');
  return Buffer.from(value, 'base64').toString('utf8');
}

function verifyRequest(body, headers, settings) {
  const timestamp = header(headers, 'Wechatpay-Timestamp');
  const nonce = header(headers, 'Wechatpay-Nonce');
  const signature = header(headers, 'Wechatpay-Signature');
  const serial = header(headers, 'Wechatpay-Serial');
  if (!timestamp || !nonce || !signature || !serial) fail('PAYMENT_SIGNATURE_INVALID');
  if (settings.platformSerialNo && serial !== settings.platformSerialNo) fail('PAYMENT_CERTIFICATE_INVALID');
  if (!/^\d+$/.test(timestamp) || Math.abs(Math.floor(Date.now() / 1000) - Number(timestamp)) > 300) fail('PAYMENT_TIMESTAMP_INVALID');
  const message = `${timestamp}\n${nonce}\n${body}\n`;
  const verifier = crypto.createVerify('RSA-SHA256');
  verifier.update(message, 'utf8');
  verifier.end();
  let publicKey;
  try {
    publicKey = crypto.createPublicKey(certificatePem(settings.certificate));
  } catch (error) {
    fail('PAYMENT_CERTIFICATE_INVALID');
  }
  let verified = false;
  try {
    verified = verifier.verify(publicKey, signature, 'base64');
  } catch (error) {
    fail('PAYMENT_SIGNATURE_INVALID');
  }
  if (!verified) fail('PAYMENT_SIGNATURE_INVALID');
}

function decryptResource(resource, key) {
  if (!resource || !resource.ciphertext || !resource.nonce) fail('PAYMENT_RESOURCE_INVALID');
  const encrypted = Buffer.from(resource.ciphertext, 'base64');
  if (encrypted.length <= 16) fail('PAYMENT_RESOURCE_INVALID');
  let plaintext;
  try {
    const decipher = crypto.createDecipheriv('aes-256-gcm', key, Buffer.from(resource.nonce, 'utf8'));
    decipher.setAuthTag(encrypted.subarray(encrypted.length - 16));
    if (resource.associated_data) decipher.setAAD(Buffer.from(resource.associated_data, 'utf8'));
    plaintext = Buffer.concat([decipher.update(encrypted.subarray(0, encrypted.length - 16)), decipher.final()]).toString('utf8');
  } catch (error) {
    fail('PAYMENT_RESOURCE_INVALID');
  }
  try {
    return JSON.parse(plaintext);
  } catch (error) {
    fail('PAYMENT_RESOURCE_INVALID');
  }
}

function unwrapNotification(event, settings) {
  const request = requestBody(event);
  verifyRequest(request.body, request.headers, settings);
  let payload;
  try {
    payload = JSON.parse(request.body);
  } catch (error) {
    fail('PAYMENT_BODY_INVALID');
  }
  if (!payload.resource || payload.resource.algorithm !== 'AEAD_AES_256_GCM') fail('PAYMENT_RESOURCE_INVALID');
  const resource = decryptResource(payload.resource, settings.key);
  if (!resource || typeof resource !== 'object') fail('PAYMENT_RESOURCE_INVALID');
  return { eventType: payload.event_type || payload.eventType || '', notificationId: payload.id || '', resource };
}

const { createCore, succeeded, normalizePayment, normalizeV3Payment } = require('./payment-core');
const payment = createCore(cloud, db);

async function findOrder(resource) {
  const outTradeNo = resource.out_trade_no || resource.outTradeNo;
  const outRefundNo = resource.out_refund_no || resource.outRefundNo;
  if (typeof outTradeNo !== 'string' && typeof outRefundNo !== 'string') fail('PAYMENT_BODY_MISSING');
  const condition = { storeId: STORE_ID };
  if (outTradeNo) condition['payment.outTradeNo'] = outTradeNo;
  else if (outRefundNo) condition['payment.outRefundNo'] = outRefundNo;
  else fail('PAYMENT_BODY_MISSING');
  const order = (await db.collection('orders').where(condition).limit(1).get()).data[0];
  if (!order) fail('ORDER_NOT_FOUND');
  return order;
}

exports.main = async (event = {}) => {
  const legacy = !hasHttpSignatureRequest(event);
  if (legacy) {
    // Callback payload is a hint only: even a forged cloud invocation cannot assert money.
    const settings = config(false);
    const order = await findOrder(event);
    let result;
    if (order.payment.outRefundNo) result = await payment.queryRefund(order._id);
    else {
      const queried = await cloud.cloudPay.queryOrder({ nonceStr: crypto.randomBytes(16).toString('hex'), subMchId: settings.subMchId, subAppid: settings.appId, outTradeNo: order.payment.outTradeNo });
      if (!succeeded(queried)) fail('PAYMENT_QUERY_FAILED');
      result = await payment.applyPayment(order._id, normalizePayment(queried), 'legacy_query');
    }
    // Ack only after durable processing; pending refund/error is thrown for retry.
    return { errcode: 0, errmsg: '' };
  }
  try {
    const notification = unwrapNotification(event, config(true));
    if (!['TRANSACTION.SUCCESS', 'REFUND.SUCCESS', 'REFUND.ABNORMAL', 'REFUND.CLOSED'].includes(notification.eventType)) fail('PAYMENT_EVENT_UNSUPPORTED');
    const order = await findOrder(notification.resource);
    if (notification.eventType.startsWith('REFUND.')) await payment.applyRefund(order._id, notification.resource, notification.notificationId, false);
    else await payment.applyPayment(order._id, normalizeV3Payment(notification.resource), notification.notificationId);
    return { statusCode: 200, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'SUCCESS', message: '成功' }) };
  } catch (error) {
    console.warn('payment_notification_rejected', error.code || error.message);
    return { statusCode: 500, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: 'FAIL', message: '处理失败，请重试' }) };
  }
};
