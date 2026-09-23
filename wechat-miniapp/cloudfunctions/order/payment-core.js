// Canonical source. Run node scripts/build-cloud-shared.js before deploying.
// Legacy CloudPay contracts only; authenticated v3 notifications use apply* directly.
'use strict';
const crypto = require('crypto');

function fail(code) { const error = new Error(code); error.code = code; throw error; }
function unwrap(response) {
  if (!response || typeof response !== 'object') return {};
  if (response.returnCode !== undefined || response.return_code !== undefined) return response;
  return response.result || response.data || response;
}
function field(value, camel, snake) {
  return value[snake] !== undefined ? value[snake] : value[camel];
}
function succeeded(response) {
  const value = unwrap(response);
  return field(value, 'returnCode', 'return_code') === 'SUCCESS' && field(value, 'resultCode', 'result_code') === 'SUCCESS';
}
function identity(value) {
  // Service-provider app/openid/merchant belong to a DIFFERENT identity namespace.
  return {
    appid: field(value, 'subAppid', 'sub_appid') || value.appid || value.appId,
    mchid: field(value, 'subMchId', 'sub_mch_id') || value.mchid || value.mchId || value.mch_id,
    payer: { openid: field(value, 'subOpenid', 'sub_openid') || value.openid }
  };
}
function normalizePayment(response) {
  const value = unwrap(response);
  return Object.assign(identity(value), {
    out_trade_no: field(value, 'outTradeNo', 'out_trade_no'),
    transaction_id: field(value, 'transactionId', 'transaction_id'),
    trade_state: field(value, 'tradeState', 'trade_state'),
    // v2 fee_type is optional and has protocol default CNY. No amount is inferred.
    amount: { total: field(value, 'totalFee', 'total_fee'), currency: field(value, 'feeType', 'fee_type') || 'CNY' }
  });
}
function normalizeRefund(response, outRefundNo) {
  const value = unwrap(response);
  let entry;
  if (field(value, 'outRefundNo', 'out_refund_no') === outRefundNo) {
    entry = value;
  } else {
    const count = Number(field(value, 'refundCount', 'refund_count'));
    if (!Number.isInteger(count) || count < 1 || count > 100) fail('REFUND_RESPONSE_INVALID');
    for (let i = 0; i < count; i += 1) {
      // wx-server-sdk@4.0.2 preserves the underscore before numeric indexes.
      const indexed = (camel, snake) => field(value, `${camel}_${i}`, `${snake}_${i}`);
      if (indexed('outRefundNo', 'out_refund_no') !== outRefundNo) continue;
      if (entry) fail('REFUND_RESPONSE_INVALID');
      entry = { outRefundNo, refundStatus: indexed('refundStatus', 'refund_status'), refundFee: indexed('refundFee', 'refund_fee'), refundId: indexed('refundId', 'refund_id') };
    }
  }
  if (!entry) fail('REFUND_ORDER_MISMATCH');
  return Object.assign(identity(value), {
    out_trade_no: field(value, 'outTradeNo', 'out_trade_no'),
    transaction_id: field(value, 'transactionId', 'transaction_id'),
    out_refund_no: field(entry, 'outRefundNo', 'out_refund_no'),
    refund_id: field(entry, 'refundId', 'refund_id'),
    refund_status: field(entry, 'refundStatus', 'refund_status'),
    amount: { total: field(value, 'totalFee', 'total_fee'), refund: field(entry, 'refundFee', 'refund_fee'), currency: field(value, 'feeType', 'fee_type') || 'CNY' }
  });
}
function money(value) {
  if ((typeof value !== 'number' && typeof value !== 'string') || value === '' || !/^\d+$/.test(String(value))) fail('PAYMENT_AMOUNT_INVALID');
  const amount = Number(value);
  if (!Number.isSafeInteger(amount) || amount < 0) fail('PAYMENT_AMOUNT_INVALID');
  return amount;
}
function paymentConfig(cloud) {
  const appId = process.env.WECHAT_PAY_APPID || process.env.WECHAT_PAY_APP_ID;
  const subMchId = process.env.WECHAT_PAY_SUB_MCH_ID || process.env.WECHAT_PAY_MCH_ID;
  if (!appId || !subMchId) fail('PAYMENT_NOT_CONFIGURED');
  return { appId, subMchId, subAppid: appId, envId: process.env.CLOUDBASE_ENV_ID || '', functionName: process.env.WECHAT_PAY_CALLBACK_FUNCTION || 'paymentCallback' };
}
function normalizeV3Payment(value) {
  return Object.assign({}, value, { appid: value.sub_appid || value.appid, mchid: value.sub_mchid || value.mchid, payer: { openid: value.payer && (value.payer.sub_openid || value.payer.openid) } });
}
function validatePayment(order, resource, settings) {
  if (!order.payment || !resource.out_trade_no || resource.out_trade_no !== order.payment.outTradeNo) fail('PAYMENT_ORDER_MISMATCH');
  if (resource.appid !== settings.appId) fail('PAYMENT_APPID_MISMATCH');
  if (resource.mchid !== settings.subMchId) fail('PAYMENT_MCHID_MISMATCH');
  if (!resource.payer || resource.payer.openid !== order.userId) fail('PAYMENT_OPENID_MISMATCH');
  if (resource.trade_state !== 'SUCCESS') fail('PAYMENT_NOT_SUCCESS');
  if (!resource.transaction_id || typeof resource.transaction_id !== 'string') fail('PAYMENT_TRANSACTION_MISSING');
  if (!resource.amount || resource.amount.currency !== 'CNY') fail('PAYMENT_CURRENCY_MISMATCH');
  if (money(resource.amount.total) !== money(order.payableAmount)) fail('PAYMENT_AMOUNT_MISMATCH');
  if (order.payment.transactionId && order.payment.transactionId !== resource.transaction_id) fail('PAYMENT_TRANSACTION_MISMATCH');
}

function createCore(cloud, db) {
  const now = () => db.serverDate();
  const ref = (store, collection, id) => store.collection(collection).doc(id);
  async function atomic(work) {
    const tx = await db.startTransaction();
    try { const result = await work(tx); await tx.commit(); return result; }
    catch (error) { await tx.rollback().catch(() => {}); throw error; }
  }
  async function getOrder(store, id) {
    const order = (await ref(store, 'orders', id).get()).data;
    if (!order || order.storeId !== 'store_001') fail('ORDER_NOT_FOUND');
    return order;
  }
  async function provider(method, args) {
    if (!cloud.cloudPay || typeof cloud.cloudPay[method] !== 'function') fail('PAYMENT_PROVIDER_UNAVAILABLE');
    const result = await cloud.cloudPay[method](args);
    if (!succeeded(result)) fail('PAYMENT_PROVIDER_REJECTED');
    return result;
  }
  function queryArgs(order, settings) { return { nonceStr: crypto.randomBytes(16).toString('hex'), subMchId: settings.subMchId, subAppid: settings.appId, outTradeNo: order.payment.outTradeNo }; }
  async function consumeCoupon(tx, order) {
    const id = order.couponSnapshot && order.couponSnapshot.id;
    if (!id) return;
    const coupon = (await ref(tx, 'user_coupons', id).get()).data;
    if (!coupon || coupon.userId !== order.userId) fail('COUPON_STATE_INVALID');
    if (coupon.status === 'USED' && coupon.usedOrderId === order._id) return;
    if (coupon.status !== 'LOCKED' || coupon.lockedOrderId !== order._id) fail('COUPON_STATE_INVALID');
    await ref(tx, 'user_coupons', id).update({ data: { status: 'USED', usedOrderId: order._id, usedAt: now(), updatedAt: now() } });
  }
  async function notifyStaff(order) {
    const templateId = process.env.WECHAT_ORDER_NOTICE_TEMPLATE_ID;
    const subscribeMessage = cloud.openapi && cloud.openapi.subscribeMessage;
    const sender = subscribeMessage && subscribeMessage.send;
    if (!order || !templateId || typeof sender !== 'function') return false;
    try {
      const staffResult = await db.collection('staff').where({ storeId: 'store_001', enabled: true }).limit(50).get();
      const recipients = staffResult.data.filter(item => (item.permissions || []).includes('ORDER_MANAGE')).map(item => item.userId).filter(Boolean);
      for (const touser of recipients) {
        try {
          await sender.call(subscribeMessage, { touser, templateId, page: `pages/merchant/index/index?orderId=${order._id}`, miniprogramState: process.env.WECHAT_MINIPROGRAM_STATE || 'formal', data: {
            thing1: { value: `${order.fulfillmentType === 'COURIER' ? '冷吃快递' : '校园外卖'} ${order.orderNo}`.slice(0, 20) },
            amount1: { value: `${(Number(order.payableAmount || 0) / 100).toFixed(2)}元` },
            phrase2: { value: order.fulfillmentType === 'COURIER' ? '待发货' : '待接单' },
            time3: { value: new Date(order.createdAt || Date.now()).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) }
          } });
        } catch (error) { console.warn('order_notice_send_failed', touser, error.code || error.message); }
      }
      return true;
    } catch (error) {
      console.warn('order_notice_query_failed', error.code || error.message);
      return false;
    }
  }
  async function applyPayment(orderId, resource, notificationId = '') {
    const settings = paymentConfig(cloud);
    const applied = await atomic(async (tx) => {
      const order = await getOrder(tx, orderId);
      validatePayment(order, resource, settings);
      if (['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'COMPLETED', 'REFUNDING', 'REFUND_FAILED', 'REFUND_REVIEW', 'REFUNDED'].includes(order.status)) return { status: order.status, duplicate: true };
      if (order.status !== 'WAIT_PAY') {
        if (order.status !== 'CANCELED') fail('PAYMENT_ORDER_STATUS_INVALID');
        // Already-released stock/coupon cannot be consumed again. Queue a full refund durably.
        const outRefundNo = order.payment.outRefundNo || `SGREF_${order.orderNo}`;
        await ref(tx, 'orders', orderId).update({ data: { status: 'REFUNDING', 'payment.transactionId': resource.transaction_id, 'payment.paidAmount': money(resource.amount.total), 'payment.outRefundNo': outRefundNo, 'payment.refundAmount': money(resource.amount.total), 'payment.reviewRequired': true, 'payment.lateRefund': true, updatedAt: now() } });
        await ref(tx, 'payment_records', resource.out_trade_no).set({ data: { orderId, orderNo: order.orderNo, userId: order.userId, type: 'PAYMENT', status: 'SUCCESS', amount: money(resource.amount.total), transactionId: resource.transaction_id, notificationId, updatedAt: now() } });
        await ref(tx, 'payment_records', outRefundNo).set({ data: { orderId, orderNo: order.orderNo, userId: order.userId, type: 'REFUND', status: 'REFUNDING', amount: money(resource.amount.total), updatedAt: now() } });
        await ref(tx, 'operation_logs', `late_${orderId}`).set({ data: { storeId: order.storeId, operatorId: 'SYSTEM', action: 'LATE_PAYMENT_AUTO_REFUND', orderId, outRefundNo, createdAt: now() } });
        return { status: 'REFUNDING', latePayment: true };
      }
      await consumeCoupon(tx, order);
      await ref(tx, 'orders', orderId).update({ data: { status: 'PAID', 'payment.transactionId': resource.transaction_id, 'payment.paidAmount': money(resource.amount.total), paidAt: now(), updatedAt: now() } });
      await ref(tx, 'payment_records', resource.out_trade_no).set({ data: { orderId, orderNo: order.orderNo, userId: order.userId, type: 'PAYMENT', status: 'SUCCESS', amount: money(resource.amount.total), transactionId: resource.transaction_id, notificationId, paidAt: now(), updatedAt: now() } });
      return { status: 'PAID' };
    });
    if (applied.latePayment) {
      try { return Object.assign({}, applied, await createRefund(orderId, 'SYSTEM')); }
      catch (error) { console.warn('late_refund_dispatch_pending', orderId, error.code || error.message); }
    }
    if (applied.status === 'PAID' && !applied.duplicate) await notifyStaff(await getOrder(db, orderId));
    return applied;
  }
  async function createPayment(orderId, openid) {
    const settings = paymentConfig(cloud);
    if (!settings.envId || typeof settings.envId !== 'string') fail('PAYMENT_ENV_NOT_CONFIGURED');
    const order = await atomic(async (tx) => {
      const current = await getOrder(tx, orderId);
      if (current.userId !== openid) fail('ORDER_NOT_FOUND');
      if (current.status !== 'WAIT_PAY' || current.payment.closingRequested) fail('ORDER_STATUS_INVALID');
      if (!current.expireAt || new Date(current.expireAt).getTime() <= Date.now()) fail('ORDER_EXPIRED');
      if (money(current.payableAmount) < 1) fail('PAYMENT_AMOUNT_INVALID');
      const outTradeNo = current.payment.outTradeNo || `SGPAY_${current.orderNo}`;
      if (!/^[A-Za-z0-9_-]{6,64}$/.test(outTradeNo)) fail('PAYMENT_ORDER_MISMATCH');
      // Durable intent BEFORE network I/O. Unknown/failed calls still count as attempted.
      await ref(tx, 'orders', orderId).update({ data: { 'payment.outTradeNo': outTradeNo, 'payment.attempted': true, updatedAt: now() } });
      return Object.assign({}, current, { payment: Object.assign({}, current.payment, { outTradeNo }) });
    });
    const response = await provider('unifiedOrder', Object.assign(queryArgs(order, settings), { body: '食光便当订单', spbillCreateIp: '127.0.0.1', totalFee: money(order.payableAmount), envId: settings.envId, functionName: settings.functionName }));
    const payment = unwrap(response).payment;
    if (!payment || !payment.timeStamp || !payment.nonceStr || !/^prepay_id=.+/.test(payment.package || '') || !payment.paySign || !['RSA', 'MD5', 'HMAC-SHA256'].includes(payment.signType)) fail('PAYMENT_RESPONSE_INVALID');
    const current = await getOrder(db, orderId);
    if (current.status !== 'WAIT_PAY' || current.payment.closingRequested) fail('ORDER_STATUS_INVALID');
    return { success: true, payment };
  }
  async function queryPayment(orderId, openid) {
    const order = await getOrder(db, orderId);
    if (openid && order.userId !== openid) fail('ORDER_NOT_FOUND');
    if (order.status !== 'WAIT_PAY' || !order.payment.outTradeNo) return { success: true, status: order.status };
    const args = queryArgs(order, paymentConfig(cloud));
    if (!cloud.cloudPay || typeof cloud.cloudPay.queryOrder !== 'function') fail('PAYMENT_PROVIDER_UNAVAILABLE');
    const response = await cloud.cloudPay.queryOrder(args);
    if (!succeeded(response)) {
      const value = unwrap(response);
      // A retry may safely reuse the SAME trade number when no provider order exists.
      // This is not proof for cancellation; cancelUnpaid intentionally remains strict.
      if (field(value, 'returnCode', 'return_code') === 'SUCCESS' && field(value, 'resultCode', 'result_code') === 'FAIL' && field(value, 'errCode', 'err_code') === 'ORDERNOTEXIST') return { success: true, status: (await getOrder(db, orderId)).status, providerOrderMissing: true };
      fail('PAYMENT_PROVIDER_REJECTED');
    }
    const resource = normalizePayment(response);
    if (resource.trade_state === 'SUCCESS') return Object.assign({ success: true }, await applyPayment(orderId, resource));
    return { success: true, status: (await getOrder(db, orderId)).status };
  }
  async function cancelUnpaid(orderId, openid, expiredOnly = false) {
    const order = await atomic(async (tx) => {
      const current = await getOrder(tx, orderId);
      if (openid && current.userId !== openid) fail('ORDER_NOT_FOUND');
      if (current.status !== 'WAIT_PAY') return current;
      if (expiredOnly && (!current.expireAt || new Date(current.expireAt).getTime() > Date.now())) fail('ORDER_NOT_EXPIRED');
      // Conflicts with payment creation transaction, preventing new provider calls after freeze.
      await ref(tx, 'orders', orderId).update({ data: { 'payment.closingRequested': true, updatedAt: now() } });
      return current;
    });
    if (order.status !== 'WAIT_PAY') return { success: true, status: order.status };
    if (order.payment.attempted || order.payment.outTradeNo) {
      const settings = paymentConfig(cloud);
      const response = await provider('queryOrder', queryArgs(order, settings));
      const resource = normalizePayment(response);
      if (resource.trade_state === 'SUCCESS') return Object.assign({ success: true }, await applyPayment(orderId, resource));
      if (resource.out_trade_no !== order.payment.outTradeNo || resource.appid !== settings.appId || resource.mchid !== settings.subMchId) fail('PAYMENT_ORDER_MISMATCH');
      if (resource.trade_state === 'NOTPAY') {
        // Definitive provider close; failure/timeout/ORDERNOTEXIST must never release resources.
        await provider('closeOrder', queryArgs(order, settings));
      } else if (resource.trade_state !== 'CLOSED') {
        fail('PAYMENT_PENDING_VERIFICATION');
      }
    }
    return atomic(async (tx) => {
      const current = await getOrder(tx, orderId);
      if (current.status !== 'WAIT_PAY') return { success: true, status: current.status };
      if (!current.payment.closingRequested || current.payment.outTradeNo !== order.payment.outTradeNo) fail('ORDER_STATUS_INVALID');
      if (!current.inventoryRestored) {
        const quantities = new Map();
        for (const item of current.itemsSnapshot || []) quantities.set(item.productId, (quantities.get(item.productId) || 0) + item.quantity);
        for (const [id, quantity] of quantities) {
          const product = (await ref(tx, 'products', id).get()).data;
          if (!product) fail('PRODUCT_NOT_FOUND');
          if (product.stockMode !== 'UNLIMITED') await ref(tx, 'products', id).update({ data: { stock: db.command.inc(quantity), isSoldOut: Boolean(product.manualSoldOut || product.isSoldOut && product.stock > 0), updatedAt: now() } });
        }
      }
      const couponId = current.couponSnapshot && current.couponSnapshot.id;
      if (couponId) {
        const coupon = (await ref(tx, 'user_coupons', couponId).get()).data;
        if (!coupon || coupon.status !== 'LOCKED' || coupon.lockedOrderId !== orderId) fail('COUPON_STATE_INVALID');
        await ref(tx, 'user_coupons', couponId).update({ data: { status: 'AVAILABLE', lockedOrderId: db.command.remove(), lockedAt: db.command.remove(), updatedAt: now() } });
      }
      await ref(tx, 'orders', orderId).update({ data: { status: 'CANCELED', inventoryRestored: true, canceledAt: now(), updatedAt: now() } });
      return { success: true, status: 'CANCELED' };
    });
  }
  async function applyRefund(orderId, resource, notificationId = '', requireAppId = true) {
    const settings = paymentConfig(cloud);
    return atomic(async (tx) => {
      const order = await getOrder(tx, orderId);
      if (!resource.out_refund_no || resource.out_refund_no !== order.payment.outRefundNo || resource.out_trade_no !== order.payment.outTradeNo) fail('REFUND_ORDER_MISMATCH');
      // v3 refund notifications omit appid; their authenticated merchant + payment IDs bind the order.
      if ((requireAppId || resource.appid) && resource.appid !== settings.appId) fail('PAYMENT_APPID_MISMATCH');
      // Standard v3 RefundNotification omits both appid and mchid. Only the signed,
      // APIv3-key-decrypted HTTP handler may opt out; transaction/refund IDs still bind it.
      if ((requireAppId || resource.mchid) && resource.mchid !== settings.subMchId) fail('PAYMENT_MCHID_MISMATCH');
      if (!resource.transaction_id || resource.transaction_id !== order.payment.transactionId || !resource.refund_id) fail('REFUND_TRANSACTION_MISMATCH');
      if (!resource.amount || resource.amount.currency !== 'CNY') fail('REFUND_CURRENCY_MISMATCH');
      if (money(resource.amount.total) !== money(order.payableAmount) || money(resource.amount.refund) !== money(order.payment.refundAmount)) fail('REFUND_AMOUNT_MISMATCH');
      if (order.status === 'REFUNDED') return { status: 'REFUNDED', duplicate: true };
      if (!['REFUNDING', 'REFUND_FAILED', 'REFUND_REVIEW'].includes(order.status)) fail('REFUND_STATUS_INVALID');
      const terminalFailure = { REFUNDCLOSE: 'REFUND_FAILED', CLOSED: 'REFUND_FAILED', CHANGE: 'REFUND_REVIEW', ABNORMAL: 'REFUND_REVIEW' }[resource.refund_status];
      if (terminalFailure) {
        await ref(tx, 'orders', orderId).update({ data: { status: terminalFailure, 'payment.refundProviderStatus': resource.refund_status, 'payment.reviewRequired': true, updatedAt: now() } });
        await ref(tx, 'payment_records', resource.out_refund_no).set({ data: { orderId, orderNo: order.orderNo, userId: order.userId, amount: money(resource.amount.refund), type: 'REFUND', status: terminalFailure, providerStatus: resource.refund_status, refundId: resource.refund_id, notificationId, updatedAt: now() } });
        await ref(tx, 'operation_logs', `exception_${resource.out_refund_no}`).set({ data: { storeId: order.storeId, operatorId: 'SYSTEM', action: terminalFailure, orderId, outRefundNo: resource.out_refund_no, createdAt: now() } });
        return { status: terminalFailure, reviewRequired: true };
      }
      if (resource.refund_status !== 'SUCCESS') fail('REFUND_PENDING_VERIFICATION');
      await ref(tx, 'orders', orderId).update({ data: { status: 'REFUNDED', 'payment.refundId': resource.refund_id, 'payment.reviewRequired': false, refundedAt: now(), updatedAt: now() } });
      await ref(tx, 'payment_records', resource.out_refund_no).set({ data: { orderId, orderNo: order.orderNo, userId: order.userId, amount: money(resource.amount.refund), status: 'SUCCESS', type: 'REFUND', refundId: resource.refund_id, notificationId, refundedAt: now(), updatedAt: now() } });
      return { status: 'REFUNDED' };
    });
  }
  async function queryRefund(orderId) {
    const order = await getOrder(db, orderId);
    if (order.status === 'REFUNDED') return { success: true, status: 'REFUNDED' };
    if (!['REFUNDING', 'REFUND_FAILED', 'REFUND_REVIEW'].includes(order.status) || !order.payment.outRefundNo) fail('REFUND_STATUS_INVALID');
    const args = queryArgs(order, paymentConfig(cloud));
    delete args.outTradeNo;
    // Query one refund directly: querying the payment returns only the first page (10 refunds).
    args.outRefundNo = order.payment.outRefundNo;
    const response = await provider('queryRefund', args);
    const resource = normalizeRefund(response, order.payment.outRefundNo);
    // Pending, definitively closed, and abnormal refunds have different operational paths.
    return Object.assign({ success: true }, await applyRefund(orderId, resource));
  }
  async function createRefund(orderId, operatorId, options = {}) {
    const order = await atomic(async (tx) => {
      const current = await getOrder(tx, orderId);
      if (current.status === 'REFUNDED') return current;
      if (!['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'COMPLETED', 'REFUNDING', 'REFUND_FAILED'].includes(current.status)) fail('REFUND_STATUS_INVALID');
      if (options.newAttempt && options.previousRefundNo !== current.payment.outRefundNo) {
        if (current.status === 'REFUNDING' && options.previousRefundNo === current.payment.previousRefundNo) return current;
        fail('REFUND_STATE_CHANGED');
      }
      if (current.payment.method === 'COUPON_ZERO' && money(current.payableAmount) === 0) {
        await ref(tx, 'orders', orderId).update({ data: { status: 'REFUNDED', refundedAt: now(), updatedAt: now() } });
        await ref(tx, 'operation_logs', `zero_refund_${orderId}`).set({ data: { storeId: current.storeId, operatorId, action: 'ZERO_PAYMENT_REFUND', orderId, amount: 0, createdAt: now() } });
        return Object.assign({}, current, { status: 'REFUNDED' });
      }
      if (!current.payment.outTradeNo || !current.payment.transactionId) fail('REFUND_PAYMENT_MISSING');
      if (current.status === 'REFUNDING') return current; // never overwrite a successful ledger on retry
      const replacingClosed = current.status === 'REFUND_FAILED';
      if (replacingClosed && (!options.newAttempt || !['REFUNDCLOSE', 'CLOSED'].includes(current.payment.refundProviderStatus))) fail('REFUND_NEW_ATTEMPT_REQUIRED');
      const attempt = replacingClosed ? Number(current.payment.refundAttempt || 1) + 1 : 1;
      if (!Number.isSafeInteger(attempt) || attempt > 99) fail('REFUND_ATTEMPT_LIMIT');
      const outRefundNo = replacingClosed ? `SGREF_${current.orderNo}_${attempt}` : current.payment.outRefundNo || `SGREF_${current.orderNo}`;
      paymentConfig(cloud); // Validate real-payment config before committing intent.
      await ref(tx, 'orders', orderId).update({ data: { status: 'REFUNDING', 'payment.outRefundNo': outRefundNo, 'payment.previousRefundNo': replacingClosed ? current.payment.outRefundNo : '', 'payment.refundAttempt': attempt, 'payment.refundProviderStatus': '', 'payment.refundAmount': money(current.payableAmount), updatedAt: now() } });
      await ref(tx, 'payment_records', outRefundNo).set({ data: { orderId, orderNo: current.orderNo, userId: current.userId, amount: money(current.payableAmount), type: 'REFUND', status: 'REFUNDING', updatedAt: now() } });
      await ref(tx, 'operation_logs', `refund_${outRefundNo}`).set({ data: { storeId: current.storeId, operatorId, action: 'CREATE_REFUND', orderId, outRefundNo, createdAt: now() } });
      return Object.assign({}, current, { status: 'REFUNDING', payment: Object.assign({}, current.payment, { outRefundNo, refundAmount: money(current.payableAmount) }) });
    });
    if (order.status === 'REFUNDED') return { success: true, status: 'REFUNDED' };
    const settings = paymentConfig(cloud);
    await provider('refund', Object.assign(queryArgs(order, settings), { outRefundNo: order.payment.outRefundNo, totalFee: money(order.payableAmount), refundFee: money(order.payment.refundAmount) }));
    return { success: true, status: (await getOrder(db, orderId)).status, outRefundNo: order.payment.outRefundNo };
  }
  async function completeDelivered(orderId, cutoff) {
    return atomic(async (tx) => {
      const order = await getOrder(tx, orderId);
      if (order.status !== 'DELIVERING' || !order.deliveringAt || new Date(order.deliveringAt).getTime() >= cutoff.getTime()) return false;
      await ref(tx, 'orders', orderId).update({ data: { status: 'COMPLETED', completedAt: now(), updatedAt: now() } });
      return true;
    });
  }
  return { applyPayment, applyRefund, createPayment, queryPayment, cancelUnpaid, createRefund, queryRefund, completeDelivered, notifyStaff };
}
module.exports = { createCore, succeeded, normalizePayment, normalizeRefund, normalizeV3Payment, validatePayment, money };
