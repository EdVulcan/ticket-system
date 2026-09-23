const api = require('./api');

// Refund states describe after-sale processing, not a successful payment.
function isSettled(status) { return ['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'COMPLETED'].indexOf(String(status || '').toUpperCase()) >= 0; }
function isPaymentClosed(status) { return ['CANCELED', 'REFUNDED', 'REFUNDING', 'REFUND_FAILED', 'REFUND_REVIEW', 'FAILED', 'EXPIRED'].indexOf(String(status || '').toUpperCase()) >= 0; }
function requestPayment(params) {
  if (!params || !params.timeStamp || !params.nonceStr || !params.package || !params.paySign || !params.signType) return Promise.reject(new Error('PAYMENT_PARAMS_MISSING'));
  return new Promise((resolve, reject) => wx.requestPayment({ timeStamp: String(params.timeStamp), nonceStr: params.nonceStr, package: params.package, signType: params.signType, paySign: params.paySign, success: resolve, fail: reject }));
}
function loginCode() {
  if (typeof wx === 'undefined' || typeof wx.login !== 'function') return Promise.resolve('');
  return new Promise((resolve, reject) => wx.login({
    success(result) { if (result && result.code) resolve(String(result.code)); else reject(new Error('WX_LOGIN_FAILED')); },
    fail() { reject(new Error('WX_LOGIN_FAILED')); }
  }));
}
async function payOrder(orderNo) {
  const before = await api.queryPayment(orderNo);
  if (isSettled(before.status) || isPaymentClosed(before.status)) return before;
  let clientError;
  try {
    const code = await loginCode();
    const response = await api.createPayment(orderNo, { clientRequestId: `payment_${orderNo}`, loginCode: code });
    if (!response) throw new Error('PAYMENT_NOT_READY');
    if (!isSettled(response.status) && !isPaymentClosed(response.status)) await requestPayment(response.payment);
  } catch (error) { clientError = error; }
  // requestPayment success/fail/cancel is never authoritative. Recover on either path.
  const verified = await api.queryPayment(orderNo);
  if (isSettled(verified.status) || isPaymentClosed(verified.status)) return verified;
  if (clientError) throw clientError;
  return verified;
}
module.exports = { payOrder, isSettled };
