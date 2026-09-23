const cloud = require('wx-server-sdk');

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database();

function fail(message) {
  const error = new Error(message);
  error.code = message;
  throw error;
}

function expired(value) {
  return value && new Date(value).getTime() <= Date.now();
}

function publicCoupon(coupon) {
  return {
    _id: coupon._id,
    id: coupon._id,
    name: coupon.name || '优惠券',
    description: coupon.description || `满 ${(Number(coupon.minGoodsAmount || 0) / 100).toFixed(2)} 元可用`,
    discountAmount: Number(coupon.discountAmount || 0),
    minGoodsAmount: Number(coupon.minGoodsAmount || 0),
    excludeDeliveryFee: coupon.excludeDeliveryFee !== false,
    status: expired(coupon.expireAt) && coupon.status === 'AVAILABLE' ? 'EXPIRED' : coupon.status,
    source: coupon.source || '',
    sourceRole: coupon.sourceRole || '',
    expireAt: coupon.expireAt || ''
  };
}

exports.main = async (event) => {
  const { OPENID: openid } = cloud.getWXContext();
  if (!openid) fail('UNAUTHORIZED');
  if ((event.action || 'list') !== 'list') fail('ACTION_NOT_SUPPORTED');
  const result = await db.collection('user_coupons').where({ userId: openid, storeId: 'store_001' }).orderBy('expireAt', 'asc').limit(100).get();
  const data = result.data.map(publicCoupon);
  // Expiry is derived on reads; a background write could overwrite a concurrent coupon lock.
  return { success: true, data };
};
