const cloud = require('wx-server-sdk');
const { createCore } = require('./payment-core');
cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database();
const payment = createCore(cloud, db);

exports.main = async (event = {}) => {
  // Timer payload alone is not authorization: reject all mini-program callers too.
  const context = cloud.getWXContext();
  if (context.OPENID || context.SOURCE === 'wx_client' || event.Type !== 'Timer' || event.TriggerName !== 'everyTenMinutes') throw new Error('FORBIDDEN');
  const summary = { success: true, canceled: 0, paid: 0, refunded: 0, completed: 0, pendingVerification: 0 };
  const expired = await db.collection('orders').where({ storeId: 'store_001', status: 'WAIT_PAY', expireAt: db.command.lt(new Date()) }).orderBy('updatedAt', 'asc').limit(100).get();
  for (const order of expired.data) {
    try {
      const result = await payment.cancelUnpaid(order._id, null, true);
      if (result.status === 'CANCELED') summary.canceled += 1;
      if (result.status === 'PAID') summary.paid += 1;
    } catch (error) {
      summary.pendingVerification += 1;
      console.warn('order_reconciliation_pending', order._id, error.code || error.message);
    }
  }
  const refunds = await db.collection('orders').where({ storeId: 'store_001', status: 'REFUNDING' }).orderBy('updatedAt', 'asc').limit(100).get();
  for (const order of refunds.data) {
    try { const result = await payment.queryRefund(order._id); if (result.status === 'REFUNDED') summary.refunded += 1; if (result.reviewRequired) summary.pendingVerification += 1; }
    catch (error) {
      summary.pendingVerification += 1;
      console.warn('refund_reconciliation_pending', order._id, error.code || error.message);
      // A crash can occur after persisting refund intent but before sending it. Same ID is idempotent.
      if ((error.code || error.message) !== 'REFUND_PENDING_VERIFICATION') {
        try { await payment.createRefund(order._id, 'SYSTEM_RECONCILIATION'); }
        catch (dispatchError) { console.warn('refund_dispatch_pending', order._id, dispatchError.code || dispatchError.message); }
      }
      // Rotate failed rows, otherwise 100 stuck refunds starve every later order forever.
      await db.collection('orders').doc(order._id).update({ data: { 'payment.lastCheckedAt': db.serverDate(), updatedAt: db.serverDate() } });
    }
  }
  const cutoff = new Date(Date.now() - 4 * 60 * 60 * 1000);
  const deliverable = await db.collection('orders').where({ storeId: 'store_001', status: 'DELIVERING', deliveringAt: db.command.lt(cutoff) }).limit(100).get();
  for (const order of deliverable.data) {
    try { if (await payment.completeDelivered(order._id, cutoff)) summary.completed += 1; }
    catch (error) { console.warn('order_completion_pending', order._id, error.code || error.message); }
  }
  return summary;
};
