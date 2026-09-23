const cloud = require('wx-server-sdk');
const { createCore } = require('./payment-core');
cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database();
const payment = createCore(cloud, db);

exports.main = async (event = {}) => {
  const { OPENID: openid } = cloud.getWXContext();
  if (!openid) throw new Error('UNAUTHORIZED');
  if (typeof event.orderId !== 'string' || !event.orderId) throw new Error('ORDER_PAYLOAD_INVALID');
  if (event.action === 'createPayment') return payment.createPayment(event.orderId, openid);
  if (event.action === 'queryPayment') return payment.queryPayment(event.orderId, openid);
  if (event.action === 'createRefund' || event.action === 'queryRefund') {
    const staff = (await db.collection('staff').where({ userId: openid, storeId: 'store_001', enabled: true }).limit(1).get()).data[0];
    if (!staff || !(staff.permissions || []).includes('REFUND')) throw new Error('FORBIDDEN');
    return event.action === 'createRefund' ? payment.createRefund(event.orderId, openid, { newAttempt: event.newAttempt === true, previousRefundNo: event.previousRefundNo }) : payment.queryRefund(event.orderId);
  }
  throw new Error('ACTION_NOT_SUPPORTED');
};
