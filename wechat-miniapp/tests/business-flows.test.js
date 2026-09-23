const { test } = require('node:test');
const assert = require('node:assert/strict');
const { memoryDb } = require('./helpers/memory-db');
const { loadFunction } = require('./helpers/load-function');
function dbFixture() {
  return memoryDb({
    store_settings: [{ _id: 'store_001', businessStatus: 'OPEN', defaultMinGoodsAmount: 0 }],
    delivery_zones: [{ _id: 'zone1', storeId: 'store_001', campusName: '东校区', zoneName: '宿舍区', deliveryFee: 0, minGoodsAmount: 0, enabled: true }],
    addresses: [{ _id: 'a1', userId: 'buyer', zoneId: 'zone1', campusName: '东校区', zoneName: '宿舍区', building: '1号楼', room: '101', contactName: '同学', contactPhone: '13800000000', isDefault: true }],
    products: [{ _id: 'p1', storeId: 'store_001', name: '便当', basePrice: 1000, stock: 5, stockMode: 'LIMITED', isOnSale: true, optionGroups: [{ id: 'disabled', enabled: false, required: true, options: [] }] }],
    user_coupons: [{ _id: 'c1', storeId: 'store_001', userId: 'buyer', name: '优惠券', status: 'AVAILABLE', discountAmount: 1000, minGoodsAmount: 0, expireAt: new Date(Date.now() + 600000) }]
  });
}
function payload(change = {}) { return Object.assign({ clientRequestId: 'request_12345678', addressId: 'a1', items: [{ productId: 'p1', quantity: 1, selectedOptions: [] }] }, change); }
test('real order entry point uses zero delivery fee, aggregates rows, and sanitizes every return', async () => {
  const db = dbFixture(); const main = loadFunction('order', db);
  const request = payload({ items: [{ productId: 'p1', quantity: 2 }, { productId: 'p1', quantity: 1 }] });
  const result = await main({ action: 'createOrder', payload: request });
  assert.equal(result.order.goodsAmount, 3000); assert.equal(result.order.deliveryFee, 0); assert.equal(db.read('products', 'p1').stock, 2);
  assert.notEqual(result.order.itemsSnapshot[0].lineId, result.order.itemsSnapshot[1].lineId);
  for (const key of ['userId', 'payment', 'clientRequestId', 'requestFingerprint']) assert.equal(result.order[key], undefined);
  const duplicate = await main({ action: 'createOrder', payload: request });
  assert.equal(duplicate.duplicate, true); assert.equal(duplicate.order.payment, undefined); assert.equal(db.read('products', 'p1').stock, 2);
});
test('oversold aggregate variants roll back all stock changes', async () => {
  const db = dbFixture(); const main = loadFunction('order', db);
  await assert.rejects(main({ action: 'createOrder', payload: payload({ items: [{ productId: 'p1', quantity: 3 }, { productId: 'p1', quantity: 3 }] }) }), /STOCK_NOT_ENOUGH/);
  assert.equal(db.read('products', 'p1').stock, 5); assert.equal(db.all('orders').length, 0);
});
test('same idempotency key with changed coupon is rejected', async () => {
  const db = dbFixture(); const main = loadFunction('order', db);
  await main({ action: 'createOrder', payload: payload() });
  await assert.rejects(main({ action: 'createOrder', payload: payload({ couponId: 'c1' }) }), /IDEMPOTENCY_CONFLICT/);
});
test('fully discounted order settles locally without pretending there was a WeChat payment', async () => {
  const db = dbFixture(); const main = loadFunction('order', db);
  const result = await main({ action: 'createOrder', payload: payload({ couponId: 'c1' }) });
  const saved = db.read('orders', result.orderId);
  assert.equal(saved.status, 'PAID'); assert.equal(saved.payableAmount, 0); assert.equal(saved.payment.method, 'COUPON_ZERO'); assert.equal(saved.payment.transactionId, '');
  assert.equal(db.read('user_coupons', 'c1').status, 'USED'); assert.equal(db.all('payment_records')[0].type, 'ZERO_PAYMENT');
});
test('prototype-like product IDs cannot bypass product lookup', async () => {
  const db = dbFixture(); const main = loadFunction('order', db);
  for (const id of ['__proto__', 'constructor', 'toString']) await assert.rejects(main({ action: 'createOrder', payload: payload({ items: [{ productId: id, quantity: 1 }] }) }), /PRODUCT_UNAVAILABLE/);
});
test('address CRUD uses document transactions and a single default pointer', async () => {
  const db = dbFixture(); const main = loadFunction('address', db);
  const created = await main({ action: 'upsert', address: { zoneId: 'zone1', building: '2号楼', room: '202', contactName: '同学', contactPhone: '13900000000', isDefault: true } });
  assert.equal(created.data.isDefault, true); assert.equal(created.data.deliveryFee, 0);
  assert.equal((await main({ action: 'list' })).data.filter(x => x.isDefault).length, 1);
  await main({ action: 'setDefault', addressId: 'a1' });
  assert.equal(db.read('address_books', 'buyer').defaultId, 'a1');
  await main({ action: 'delete', addressId: 'a1' });
  assert.equal(db.read('address_books', 'buyer').defaultId, created.data.id);
  assert.equal((await main({ action: 'list' })).data.length, 1);
});
test('address ownership and disabled delivery zones are enforced', async () => {
  const db = dbFixture(); const main = loadFunction('address', db, { OPENID: 'attacker' });
  await assert.rejects(main({ action: 'setDefault', addressId: 'a1' }), /ADDRESS_NOT_FOUND/);
  await db.collection('delivery_zones').doc('zone1').update({ data: { enabled: false } });
  await assert.rejects(main({ action: 'upsert', address: { zoneId: 'zone1', building: '1号楼', room: '101', contactName: '同学', contactPhone: '13900000000' } }), /ADDRESS_OUT_OF_RANGE/);
  assert.equal((await loadFunction('address', db)({ action: 'list' })).data[0].deliverable, false);
});

test('shipping addresses keep an independent default and remain valid without a campus zone', async () => {
  const db = dbFixture(); const main = loadFunction('address', db);
  const created = await main({ action: 'upsert', address: { addressType: 'SHIPPING', province: '四川省', city: '成都市', district: '武侯区', detailAddress: '科华北路88号', contactName: '同学', contactPhone: '13900000000', isDefault: true } });
  assert.equal(created.data.addressType, 'SHIPPING'); assert.equal(created.data.isDefault, true); assert.equal(created.data.deliverable, true);
  const addresses = (await main({ action: 'list' })).data;
  assert.equal(addresses.filter(item => item.addressType === 'SHIPPING' && item.isDefault).length, 1);
  assert.equal(addresses.find(item => item.id === created.data.id).deliveryFee, 0);
  await main({ action: 'setDefault', addressId: 'a1' });
  assert.equal((await main({ action: 'list' })).data.find(item => item.id === 'a1').isDefault, true);
  await main({ action: 'delete', addressId: created.data.id });
  assert.equal((await main({ action: 'list' })).data.some(item => item.id === created.data.id), false);
});

test('editing a shipping address without addressType preserves its fulfillment type', async () => {
  const db = dbFixture(); const main = loadFunction('address', db);
  const created = await main({ action: 'upsert', address: { addressType: 'SHIPPING', province: '四川省', city: '成都市', district: '武侯区', detailAddress: '科华北路88号', contactName: '同学', contactPhone: '13800000000' } });
  const edited = await main({ action: 'upsert', address: { id: created.data.id, province: '四川省', city: '成都市', district: '锦江区', detailAddress: '春熙路18号', contactName: '同学', contactPhone: '13900000000' } });
  assert.equal(edited.data.addressType, 'SHIPPING');
  assert.equal(db.read('addresses', created.data.id).district, '锦江区');
  assert.equal(db.read('addresses', created.data.id).zoneId, '');
});

test('courier order uses shipping address, free-shipping rule and courier status transitions', async () => {
  const db = memoryDb({
    store_settings: [{ _id: 'store_001', businessStatus: 'OPEN', courierStatus: 'OPEN', defaultShippingFee: 800, freeShippingThreshold: 5000, courierMinGoodsAmount: 1000 }],
    staff: [{ _id: 'staff', userId: 'owner', storeId: 'store_001', enabled: true, permissions: ['ORDER_MANAGE'] }],
    addresses: [{ _id: 'ship1', userId: 'buyer', storeId: 'store_001', addressType: 'SHIPPING', province: '四川省', city: '成都市', district: '武侯区', detailAddress: '科华北路88号', contactName: '同学', contactPhone: '13800000000' }],
    products: [{ _id: 'cold1', storeId: 'store_001', fulfillmentType: 'COURIER', name: '冷吃兔', basePrice: 5000, stock: 5, stockMode: 'LIMITED', isOnSale: true, isSoldOut: false }]
  });
  const buyer = loadFunction('order', db, { OPENID: 'buyer' });
  const payload = { clientRequestId: 'courier_123456', fulfillmentType: 'COURIER', deliveryMethod: 'DELIVERY', addressId: 'ship1', items: [{ productId: 'cold1', quantity: 1, selectedOptions: [] }] };
  const created = await buyer({ action: 'createOrder', payload });
  assert.equal(created.order.shippingFee, 0); assert.equal(created.order.deliveryFee, 0); assert.equal(created.order.addressSnapshot.addressType, 'SHIPPING');
  await db.collection('orders').doc(created.orderId).update({ data: { status: 'PAID' } });
  const merchant = loadFunction('order', db, { OPENID: 'owner' });
  await assert.rejects(merchant({ action: 'changeStatus', orderId: created.orderId, status: 'SHIPPED', shipping: { carrier: '中通快递' } }), /SHIPPING_PAYLOAD_INVALID/);
  await merchant({ action: 'changeStatus', orderId: created.orderId, status: 'SHIPPED', shipping: { carrier: '中通快递', trackingNo: 'ZT123456789' } });
  assert.equal(db.read('orders', created.orderId).status, 'SHIPPED'); assert.equal(db.read('orders', created.orderId).shipping.trackingNo, 'ZT123456789');
  await buyer({ action: 'confirmReceipt', orderId: created.orderId });
  assert.equal(db.read('orders', created.orderId).status, 'COMPLETED');
});

test('fulfillment and coupon scope cannot cross order channels', async () => {
  const db = memoryDb({
    store_settings: [{ _id: 'store_001', businessStatus: 'OPEN', courierStatus: 'OPEN', defaultMinGoodsAmount: 0, courierMinGoodsAmount: 0 }],
    delivery_zones: [{ _id: 'zone1', storeId: 'store_001', zoneName: '宿舍区', campusName: '东校区', enabled: true, deliveryFee: 300, minGoodsAmount: 0 }],
    addresses: [{ _id: 'campus1', userId: 'buyer', storeId: 'store_001', addressType: 'CAMPUS', zoneId: 'zone1', building: '1号楼', room: '101', contactName: '同学', contactPhone: '13800000000' }, { _id: 'ship1', userId: 'buyer', storeId: 'store_001', addressType: 'SHIPPING', province: '四川省', city: '成都市', district: '武侯区', detailAddress: '科华北路88号', contactName: '同学', contactPhone: '13800000000' }],
    products: [{ _id: 'take1', storeId: 'store_001', fulfillmentType: 'TAKEAWAY', name: '便当', basePrice: 1000, stock: 5, stockMode: 'LIMITED', isOnSale: true, isSoldOut: false }, { _id: 'cold1', storeId: 'store_001', fulfillmentType: 'COURIER', name: '冷吃兔', basePrice: 1000, stock: 5, stockMode: 'LIMITED', isOnSale: true, isSoldOut: false }],
    user_coupons: [{ _id: 'takeCoupon', storeId: 'store_001', userId: 'buyer', scope: 'TAKEAWAY', status: 'AVAILABLE', discountAmount: 100, minGoodsAmount: 0, expireAt: new Date(Date.now() + 600000) }]
  });
  const main = loadFunction('order', db);
  const base = { clientRequestId: 'cross_123456', items: [{ productId: 'cold1', quantity: 1, selectedOptions: [] }], addressId: 'campus1', fulfillmentType: 'COURIER', deliveryMethod: 'DELIVERY' };
  await assert.rejects(main({ action: 'createOrder', payload: base }), /ADDRESS_TYPE_INVALID/);
  await assert.rejects(main({ action: 'createOrder', payload: Object.assign({}, base, { clientRequestId: 'cross_123457', addressId: 'ship1', couponId: 'takeCoupon' }) }), /COUPON_UNAVAILABLE/);
});
test('concurrent default-address changes cannot both commit stale pointers', async () => {
  const db = dbFixture(); const main = loadFunction('address', db);
  const request = { action: 'upsert', address: { zoneId: 'zone1', building: '2', room: '202', contactName: '同学', contactPhone: '13900000000', isDefault: true } };
  const results = await Promise.allSettled([main(request), main(request)]);
  assert.equal(results.filter(x => x.status === 'fulfilled').length, 1);
  assert.equal((await main({ action: 'list' })).data.filter(x => x.isDefault).length, 1);
});
test('expired coupon display is derived and never writes stale AVAILABLE state', async () => {
  const db = dbFixture(); await db.collection('user_coupons').doc('c1').update({ data: { expireAt: new Date(0) } });
  const result = await loadFunction('coupon', db)({ action: 'list' });
  assert.equal(result.data[0].status, 'EXPIRED'); assert.equal(db.read('user_coupons', 'c1').status, 'AVAILABLE');
});
test('client cannot forge timer trigger to auto-complete deliveries', async () => {
  const db = dbFixture(); const main = loadFunction('scheduledTasks', db);
  await assert.rejects(main({ Type: 'Timer', TriggerName: 'everyTenMinutes' }), /FORBIDDEN/);
});
test('empty authenticated timer run succeeds', async () => {
  const result = await loadFunction('scheduledTasks', dbFixture(), {})({ Type: 'Timer', TriggerName: 'everyTenMinutes' });
  assert.equal(result.success, true); assert.equal(result.canceled, 0);
});

test('created assist sessions can finish with their disabled template version', async () => {
  const db = dbFixture();
  await db.collection('coupon_templates').doc('old_template').set({ data: { _id: 'old_template', storeId: 'store_001', name: '旧券', discountAmount: 300, minGoodsAmount: 0, validDays: 5, enabled: false } });
  await db.collection('assist_campaigns').doc('campaign_old').set({ data: { _id: 'campaign_old', storeId: 'store_001', enabled: true, requiredHelpers: 1, starterCouponTemplateId: 'old_template', helperCouponTemplateId: 'old_template' } });
  await db.collection('assist_sessions').doc('session_old').set({ data: { _id: 'session_old', storeId: 'store_001', campaignId: 'campaign_old', starterId: 'starter', helperCount: 0, status: 'ACTIVE', starterCouponTemplateId: 'old_template', helperCouponTemplateId: 'old_template', expireAt: new Date(Date.now() + 600000) } });
  const result = await loadFunction('assist', db, { OPENID: 'helper' })({ action: 'helpAssist', sessionId: 'session_old' });
  assert.equal(result.data.status, 'SUCCESS');
  assert.equal(db.all('user_coupons').filter(coupon => coupon.campaignId === 'campaign_old').length, 2);
});
test('100 failing refunds do not starve the 101st on the next scheduled run', async () => {
  process.env.WECHAT_PAY_APPID = 'app'; process.env.WECHAT_PAY_MCH_ID = 'merchant'; process.env.WECHAT_PAY_SUB_MCH_ID = 'merchant';
  const orders = Array.from({ length: 101 }, (_, index) => ({ _id: `r${index}`, orderNo: `r${index}`, userId: 'buyer', storeId: 'store_001', status: 'REFUNDING', payableAmount: 100, updatedAt: new Date(index * 1000), payment: { outTradeNo: `trade${index}`, transactionId: `tx${index}`, outRefundNo: `refund${index}`, refundAmount: 100 } }));
  const db = memoryDb({ orders }); const queried = [];
  const main = loadFunction('scheduledTasks', db, {}, {
    queryRefund: async args => {
      queried.push(args.outRefundNo);
      if (args.outRefundNo !== 'refund100') return { returnCode: 'SUCCESS', resultCode: 'FAIL', errCode: 'REFUNDNOTEXIST' };
      return { returnCode: 'SUCCESS', resultCode: 'SUCCESS', subAppid: 'app', subMchId: 'merchant', outTradeNo: 'trade100', transactionId: 'tx100', totalFee: 100, refundCount: 1, outRefundNo_0: 'refund100', refundId_0: 'wx-refund', refundFee_0: 100, refundStatus_0: 'SUCCESS' };
    },
    refund: async () => ({ returnCode: 'FAIL' })
  });
  await main({ Type: 'Timer', TriggerName: 'everyTenMinutes' });
  assert.equal(queried.includes('refund100'), false);
  await main({ Type: 'Timer', TriggerName: 'everyTenMinutes' });
  assert.equal(queried.includes('refund100'), true); assert.equal(db.read('orders', 'r100').status, 'REFUNDED');
});
test('two createSession calls cannot reset an existing completed session', async () => {
  const db = dbFixture(); await db.collection('assist_campaigns').doc('campaign1').set({ data: { storeId: 'store_001', enabled: true, requiredHelpers: 1 } });
  const main = loadFunction('assist', db);
  const first = await main({ action: 'createSession' });
  await db.collection('assist_sessions').doc(first.data.id).update({ data: { status: 'SUCCESS', helperCount: 1 } });
  const again = await main({ action: 'createSession' });
  assert.equal(again.data.status, 'SUCCESS'); assert.equal(again.data.helperCount, 1);
});
test('unsupported multi-helper campaign is rejected before any coupons issue', async () => {
  const db = dbFixture(); await db.collection('assist_campaigns').doc('campaign1').set({ data: { storeId: 'store_001', enabled: true, requiredHelpers: 2 } });
  await assert.rejects(loadFunction('assist', db)({ action: 'createSession' }), /CAMPAIGN_CONFIG_INVALID/);
  assert.equal(db.all('assist_sessions').length, 0);
});
