const { test } = require('node:test');
const assert = require('node:assert/strict');
const { memoryDb } = require('./helpers/memory-db');
const { loadFunction } = require('./helpers/load-function');
function fixture(permissions = ['PRODUCT_MANAGE', 'STORE_SETTING', 'ORDER_MANAGE']) {
  const db = memoryDb({ staff: [{ _id: 'staff', userId: 'owner', storeId: 'store_001', enabled: true, permissions }], store_settings: [{ _id: 'store_001', businessStatus: 'OPEN' }], categories: [{ _id: 'rice', storeId: 'store_001', enabled: true }], products: [{ _id: 'product1', storeId: 'store_001', categoryId: 'rice', basePrice: 1000, originalPrice: 1200, stock: 5, stockMode: 'LIMITED' }] });
  return { db, main: loadFunction('merchant', db, { OPENID: 'owner' }) };
}
test('merchant creation validates category, monetary fields and cloud image IDs; defaults off-sale', async () => {
  const { db, main } = fixture();
  const result = await main({ action: 'createProduct', name: '便当', basePrice: 1000, originalPrice: 1000, stock: 20, categoryId: 'rice', imageFileIds: ['cloud://env/file.jpg'] });
  const product = db.read('products', result.productId);
  assert.equal(product.isOnSale, false); assert.equal(product.stock, 20); assert.equal(db.all('operation_logs').length, 1);
  await assert.rejects(main({ action: 'updateProduct', productId: result.productId, imageFileIds: ['file:///secret'] }), /PRODUCT_IMAGE_INVALID/);
});
test('stock increments use latest transactional value and stale absolute stock is rejected', async () => {
  const { db, main } = fixture();
  await db.collection('products').doc('product1').update({ data: { stock: 3 } });
  await main({ action: 'updateProduct', productId: 'product1', stockDelta: 1 });
  assert.equal(db.read('products', 'product1').stock, 4);
  await assert.rejects(main({ action: 'updateProduct', productId: 'product1', stock: 20, expectedStock: 5 }), /STOCK_CHANGED_REFRESH/);
  assert.equal(db.read('products', 'product1').stock, 4);
});
test('product option pricing is validated and persisted in cents', async () => {
  const { db, main } = fixture();
  const optionGroups = [{ id: 'g1', name: '加料', required: false, options: [{ id: 'o1', name: '鸡蛋', priceDelta: 150 }] }];
  await main({ action: 'updateProduct', productId: 'product1', optionGroups });
  assert.equal(db.read('products', 'product1').optionGroups[0].options[0].priceDelta, 150);
  optionGroups[0].options[0].priceDelta = -1;
  await assert.rejects(main({ action: 'updateProduct', productId: 'product1', optionGroups }), /AMOUNT_INVALID/);
});
test('merchant role restrictions apply to every management write', async () => {
  const { db, main } = fixture([]);
  for (const action of ['createProduct', 'updateProduct', 'updateStoreSettings', 'saveZone', 'saveCampaign', 'getOperationLogs']) await assert.rejects(main({ action }), /FORBIDDEN/);
  assert.equal(db.all('operation_logs').length, 0);
});
test('store input types cannot replace phone or announcement with objects', async () => {
  const { main } = fixture();
  await assert.rejects(main({ action: 'updateStoreSettings', settings: { phone: { malicious: true } } }), /PAYLOAD_INVALID/);
  await assert.rejects(main({ action: 'updateStoreSettings', settings: { announcement: [] } }), /PAYLOAD_INVALID/);
});
test('zero-fee delivery zone creation and disabling are persisted with audit logs', async () => {
  const { db, main } = fixture();
  const zone = { campusName: '东校区', zoneName: '宿舍区', deliveryFee: 0, minGoodsAmount: 0, enabled: true };
  const created = await main({ action: 'saveZone', zone });
  await main({ action: 'saveZone', zone: Object.assign({}, zone, { id: created.id, enabled: false }) });
  assert.equal(db.read('delivery_zones', created.id).enabled, false); assert.equal(db.read('delivery_zones', created.id).deliveryFee, 0); assert.equal(db.all('operation_logs').length, 2);
});
test('campaign edits create immutable template versions and replace previous active campaign', async () => {
  const { db, main } = fixture();
  await db.collection('assist_campaigns').doc('legacy').set({ data: { storeId: 'store_001', enabled: true } });
  const campaign = { name: '好友券', enabled: true, discountAmount: 300, minGoodsAmount: 2500, validDays: 5, startAt: '2026-09-01T00:00:00+08:00', endAt: '2026-12-31T23:59:59+08:00' };
  const result = await main({ action: 'saveCampaign', campaign });
  const templateId = db.read('assist_campaigns', result.id).starterCouponTemplateId;
  assert.equal(db.read('assist_campaigns', 'legacy').enabled, false);
  await main({ action: 'saveCampaign', campaign: Object.assign({}, campaign, { id: result.id, discountAmount: 500 }) });
  assert.equal(db.read('coupon_templates', templateId).discountAmount, 300);
  assert.notEqual(db.read('assist_campaigns', result.id).starterCouponTemplateId, templateId);
  assert.equal(db.read('store_settings', 'store_001').activeCampaignId, result.id);
});

test('merchant can configure courier rules and version coupon templates', async () => {
  const { db, main } = fixture();
  await main({ action: 'updateStoreSettings', settings: { courierStatus: 'OPEN', courierMinGoodsAmount: 1000, defaultShippingFee: 800, freeShippingThreshold: 5000, shippingCarrier: '中通快递' } });
  assert.equal(db.read('store_settings', 'store_001').freeShippingThreshold, 5000);
  const created = await main({ action: 'saveCouponTemplate', template: { name: '通用券', discountAmount: 300, minGoodsAmount: 2500, validDays: 5, scope: 'UNIVERSAL', excludeDeliveryFee: true, enabled: true } });
  assert.equal(db.read('coupon_templates', created.id).scope, 'UNIVERSAL');
  const next = await main({ action: 'saveCouponTemplate', template: { id: created.id, name: '冷吃券', discountAmount: 500, minGoodsAmount: 3000, validDays: 7, scope: 'COURIER', excludeDeliveryFee: false, enabled: true } });
  assert.equal(db.read('coupon_templates', created.id).enabled, false); assert.equal(db.read('coupon_templates', next.id).scope, 'COURIER');
});

test('editing a coupon template rebinds enabled campaigns but protects an active campaign from disablement', async () => {
  const { db, main } = fixture();
  await db.collection('coupon_templates').doc('old_template').set({ data: { _id: 'old_template', storeId: 'store_001', name: '旧券', discountAmount: 300, minGoodsAmount: 2500, validDays: 5, enabled: true } });
  await db.collection('assist_campaigns').doc('active_campaign').set({ data: { _id: 'active_campaign', storeId: 'store_001', enabled: true, starterCouponTemplateId: 'old_template', helperCouponTemplateId: 'old_template' } });
  await db.collection('store_settings').doc('store_001').update({ data: { activeCampaignId: 'active_campaign' } });
  const next = await main({ action: 'saveCouponTemplate', template: { id: 'old_template', name: '新券', discountAmount: 500, minGoodsAmount: 3000, validDays: 7, enabled: true } });
  assert.equal(db.read('coupon_templates', 'old_template').enabled, false);
  assert.equal(db.read('assist_campaigns', 'active_campaign').starterCouponTemplateId, next.id);
  assert.equal(db.read('assist_campaigns', 'active_campaign').helperCouponTemplateId, next.id);
  await assert.rejects(main({ action: 'saveCouponTemplate', template: { id: next.id, name: '停用新券', discountAmount: 500, minGoodsAmount: 3000, validDays: 7, enabled: false } }), /COUPON_TEMPLATE_IN_USE/);
  assert.equal(db.read('coupon_templates', next.id).enabled, true);
});

test('merchant stats separate fulfillment channels and count successful assist records', async () => {
  const { db, main } = fixture();
  await db.collection('orders').doc('take-order').set({ data: { _id: 'take-order', storeId: 'store_001', fulfillmentType: 'TAKEAWAY', status: 'COMPLETED', payableAmount: 1800, createdAt: new Date() } });
  await db.collection('orders').doc('cold-order').set({ data: { _id: 'cold-order', storeId: 'store_001', fulfillmentType: 'COURIER', status: 'SHIPPED', payableAmount: 3000, createdAt: new Date() } });
  await db.collection('assist_records').doc('assist1').set({ data: { _id: 'assist1', storeId: 'store_001', result: 'SUCCESS', createdAt: new Date() } });
  await db.collection('assist_records').doc('assist_failed').set({ data: { _id: 'assist_failed', storeId: 'store_001', result: 'FAILED', createdAt: new Date() } });
  const result = await main({ action: 'getStats' });
  assert.equal(result.data.orderCount, 2); assert.equal(result.data.salesAmount, 4800); assert.equal(result.data.takeawayOrders, 1); assert.equal(result.data.courierOrders, 1); assert.equal(result.data.assistCount, 1); assert.equal(result.data.couponCount, 2);
});
