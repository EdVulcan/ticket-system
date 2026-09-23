const crypto = require('crypto');
const cloud = require('wx-server-sdk');
cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database({ throwOnNotFound: false });
const STORE_ID = 'store_001';
const FULFILLMENT_TYPES = ['TAKEAWAY', 'COURIER'];
function fail(code) { const error = new Error(code); error.code = code; throw error; }
function text(value, max, required) { if (typeof value !== 'string' || value.length > max || (required && !value.trim())) fail('PAYLOAD_INVALID'); return value.trim(); }
function amount(value, min = 0, max = 10000000) { if (!Number.isSafeInteger(value) || value < min || value > max) fail('AMOUNT_INVALID'); return value; }
async function assertStaff(openid, permission) {
  const staff = (await db.collection('staff').where({ userId: openid, storeId: STORE_ID, enabled: true }).limit(1).get()).data[0];
  if (!staff || !(staff.permissions || []).includes(permission)) fail('FORBIDDEN');
  return staff;
}
async function atomic(work) {
  const tx = await db.startTransaction();
  try { const result = await work(tx); await tx.commit(); return result; }
  catch (error) { await tx.rollback(); throw error; }
}
async function log(tx, operatorId, action, targetId, change) {
  await tx.collection('operation_logs').doc('log_' + crypto.randomBytes(16).toString('hex')).set({ data: { storeId: STORE_ID, operatorId, action, targetId, change, createdAt: db.serverDate() } });
}
function productFields(event) {
  const update = {};
  for (const [key, max, required] of [['name', 60, true], ['description', 240, false]]) if (event[key] !== undefined) update[key] = text(event[key], max, required);
  for (const key of ['basePrice', 'originalPrice']) if (event[key] !== undefined) update[key] = amount(event[key], 1);
  for (const key of ['isOnSale', 'isSoldOut']) if (event[key] !== undefined) { if (typeof event[key] !== 'boolean') fail('PRODUCT_PAYLOAD_INVALID'); update[key] = event[key]; }
  if (event.isSoldOut !== undefined) update.manualSoldOut = event.isSoldOut;
  if (event.categoryId !== undefined) update.categoryId = text(event.categoryId, 128, true);
  if (event.fulfillmentType !== undefined) {
    if (!FULFILLMENT_TYPES.includes(event.fulfillmentType)) fail('FULFILLMENT_INVALID');
    update.fulfillmentType = event.fulfillmentType;
  }
  if (event.imageFileIds !== undefined) {
    if (!Array.isArray(event.imageFileIds) || event.imageFileIds.length > 5 || event.imageFileIds.some(id => typeof id !== 'string' || id.length > 512 || !id.startsWith('cloud://'))) fail('PRODUCT_IMAGE_INVALID');
    update.imageFileIds = event.imageFileIds;
  }
  if (event.optionGroups !== undefined) {
    if (!Array.isArray(event.optionGroups) || event.optionGroups.length > 6) fail('PRODUCT_OPTIONS_INVALID');
    const groupIds = new Set();
    update.optionGroups = event.optionGroups.map(group => {
      const id = text(group.id, 80, true);
      if (groupIds.has(id) || !Array.isArray(group.options) || !group.options.length || group.options.length > 12 || typeof group.required !== 'boolean') fail('PRODUCT_OPTIONS_INVALID');
      groupIds.add(id);
      const optionIds = new Set();
      return { id, name: text(group.name, 40, true), required: group.required, enabled: true, options: group.options.map(option => {
        const optionId = text(option.id, 80, true);
        if (optionIds.has(optionId)) fail('PRODUCT_OPTIONS_INVALID');
        optionIds.add(optionId);
        return { id: optionId, name: text(option.name, 40, true), priceDelta: amount(option.priceDelta, 0, 100000), enabled: true };
      }) };
    });
  }
  return update;
}
exports.main = async (event = {}) => {
  const { OPENID: openid } = cloud.getWXContext();
  if (!openid) fail('UNAUTHORIZED');
  const action = event.action;
  if (action === 'getOrders') {
    await assertStaff(openid, 'ORDER_MANAGE');
    const [result, exceptions] = await Promise.all([
      db.collection('orders').where({ storeId: STORE_ID }).orderBy('createdAt', 'desc').limit(100).get(),
      db.collection('orders').where({ storeId: STORE_ID, status: db.command.in(['REFUND_FAILED', 'REFUND_REVIEW']) }).orderBy('updatedAt', 'desc').limit(100).get()
    ]);
    const orders = [...new Map(result.data.concat(exceptions.data).map(order => [order._id, order])).values()];
    const attention = order => ['REFUND_FAILED', 'REFUND_REVIEW'].includes(order.status) ? 1 : 0;
    orders.sort((a, b) => attention(b) - attention(a) || new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime());
    return { success: true, data: orders.map(order => {
      const safe = Object.assign({}, order); delete safe.userId; delete safe.requestFingerprint; delete safe.clientRequestId;
      safe.paymentReviewRequired = Boolean(order.payment && order.payment.reviewRequired);
      safe.refundReference = order.payment && order.payment.outRefundNo || '';
      delete safe.payment;
      return safe;
    }) };
  }
  if (action === 'getProducts') {
    await assertStaff(openid, 'PRODUCT_MANAGE');
    const [products, categories] = await Promise.all([db.collection('products').where({ storeId: STORE_ID }).orderBy('sort', 'asc').limit(200).get(), db.collection('categories').where({ storeId: STORE_ID, enabled: true }).orderBy('sort', 'asc').limit(100).get()]);
    return { success: true, data: products.data, categories: categories.data };
  }
  if (action === 'getStats') {
    await assertStaff(openid, 'ORDER_MANAGE');
    const [ordersResult, assistResult] = await Promise.all([
      db.collection('orders').where({ storeId: STORE_ID }).orderBy('createdAt', 'desc').limit(2000).get(),
      db.collection('assist_records').where({ storeId: STORE_ID }).orderBy('createdAt', 'desc').limit(2000).get()
    ]);
    const orders = ordersResult.data;
    const paidStatuses = ['PAID', 'PREPARING', 'DELIVERING', 'SHIPPED', 'COMPLETED', 'REFUNDING', 'REFUND_FAILED', 'REFUND_REVIEW'];
    const sales = orders.filter(order => paidStatuses.includes(order.status)).reduce((sum, order) => sum + Number(order.payableAmount || 0), 0);
    const dayKey = value => { const date = new Date(value); return Number.isNaN(date.getTime()) ? '' : new Date(date.getTime() + 8 * 60 * 60 * 1000).toISOString().slice(0, 10); };
    const daily = [];
    const shiftedNow = new Date(Date.now() + 8 * 60 * 60 * 1000);
    const todayUtc = Date.UTC(shiftedNow.getUTCFullYear(), shiftedNow.getUTCMonth(), shiftedNow.getUTCDate());
    for (let index = 6; index >= 0; index -= 1) {
      const key = new Date(todayUtc - index * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
      const sameDay = orders.filter(order => dayKey(order.createdAt) === key);
      daily.push({ date: key, orderCount: sameDay.length, salesAmount: sameDay.filter(order => paidStatuses.includes(order.status)).reduce((sum, order) => sum + Number(order.payableAmount || 0), 0) });
    }
    const successfulAssists = assistResult.data.filter(record => record.result === 'SUCCESS');
    return { success: true, data: { orderCount: orders.length, salesAmount: sales, takeawayOrders: orders.filter(order => (order.fulfillmentType || 'TAKEAWAY') === 'TAKEAWAY').length, courierOrders: orders.filter(order => order.fulfillmentType === 'COURIER').length, assistCount: successfulAssists.length, couponCount: successfulAssists.length * 2, daily } };
  }
  if (action === 'createProduct' || action === 'updateProduct') {
    await assertStaff(openid, 'PRODUCT_MANAGE');
    const creating = action === 'createProduct';
    const productId = creating ? 'product_' + crypto.randomBytes(16).toString('hex') : text(event.productId, 128, true);
    const update = productFields(event);
    if (creating && (!update.name || !update.basePrice || !update.categoryId)) fail('PRODUCT_PAYLOAD_INVALID');
    await atomic(async tx => {
      const current = (await tx.collection('products').doc(productId).get()).data;
      if (!creating && (!current || current.storeId !== STORE_ID)) fail('PRODUCT_NOT_FOUND');
      const product = current || { storeId: STORE_ID, stock: 0, stockMode: 'LIMITED', isOnSale: false, isSoldOut: true, imageFileIds: [], optionGroups: [], sort: 100, createdAt: db.serverDate() };
      const categoryId = update.categoryId || product.categoryId;
      const category = (await tx.collection('categories').doc(categoryId).get()).data;
      if (!category || category.storeId !== STORE_ID || category.enabled !== true) fail('CATEGORY_INVALID');
      const fulfillmentType = update.fulfillmentType || product.fulfillmentType || category.fulfillmentType || 'TAKEAWAY';
      if (category.fulfillmentType && category.fulfillmentType !== fulfillmentType) fail('CATEGORY_FULFILLMENT_MISMATCH');
      update.fulfillmentType = fulfillmentType;
      if (event.stock !== undefined) {
        if (!creating && event.expectedStock !== product.stock) fail('STOCK_CHANGED_REFRESH');
        update.stock = amount(event.stock, 0, 100000);
      }
      if (event.stockDelta !== undefined) {
        if (!Number.isSafeInteger(event.stockDelta) || Math.abs(event.stockDelta) > 10000 || event.stock !== undefined) fail('PRODUCT_PAYLOAD_INVALID');
        update.stock = amount(product.stock + event.stockDelta, 0, 100000);
      }
      if (update.stock !== undefined) update.isSoldOut = update.stock === 0 || (update.manualSoldOut !== undefined ? update.manualSoldOut : Boolean(product.manualSoldOut));
      const next = Object.assign({}, product, update, { updatedAt: db.serverDate() });
      if (creating && !next.originalPrice) next.originalPrice = next.basePrice;
      if (next.originalPrice < next.basePrice) fail('PRODUCT_PRICE_INVALID');
      if (creating) await tx.collection('products').doc(productId).set({ data: next });
      else await tx.collection('products').doc(productId).update({ data: Object.assign({}, update, { updatedAt: db.serverDate() }) });
      await log(tx, openid, creating ? 'CREATE_PRODUCT' : 'UPDATE_PRODUCT', productId, update);
    });
    return { success: true, productId };
  }
  if (action === 'getSettings') {
    await assertStaff(openid, 'STORE_SETTING');
    const [store, zones] = await Promise.all([db.collection('store_settings').doc(STORE_ID).get(), db.collection('delivery_zones').where({ storeId: STORE_ID }).limit(200).get()]);
    return { success: true, store: store.data, zones: zones.data };
  }
  if (action === 'updateStoreSettings') {
    await assertStaff(openid, 'STORE_SETTING');
    const input = event.settings;
    if (!input || typeof input !== 'object' || Array.isArray(input)) fail('STORE_PAYLOAD_INVALID');
    const update = {};
    if (input.announcement !== undefined) update.announcement = text(input.announcement, 100, false);
    if (input.phone !== undefined) { update.phone = text(input.phone, 20, true); if (!/^1\d{10}$/.test(update.phone)) fail('STORE_PHONE_INVALID'); }
    if (input.businessStatus !== undefined) { if (!['OPEN', 'PAUSED'].includes(input.businessStatus)) fail('STORE_PAYLOAD_INVALID'); update.businessStatus = input.businessStatus; }
    if (input.courierStatus !== undefined) { if (!['OPEN', 'PAUSED'].includes(input.courierStatus)) fail('STORE_PAYLOAD_INVALID'); update.courierStatus = input.courierStatus; }
    if (input.businessHours !== undefined) update.businessHours = text(input.businessHours, 100, true);
    for (const key of ['defaultDeliveryFee', 'defaultMinGoodsAmount', 'defaultShippingFee', 'freeShippingThreshold', 'courierMinGoodsAmount']) if (input[key] !== undefined) update[key] = amount(input[key]);
    if (input.shippingCarrier !== undefined) update.shippingCarrier = text(input.shippingCarrier, 40, false);
    if (input.orderPayTimeoutMinutes !== undefined) update.orderPayTimeoutMinutes = amount(input.orderPayTimeoutMinutes, 1, 60);
    if (!Object.keys(update).length) fail('STORE_PAYLOAD_INVALID');
    await atomic(async tx => { await tx.collection('store_settings').doc(STORE_ID).get(); await tx.collection('store_settings').doc(STORE_ID).update({ data: Object.assign({}, update, { updatedAt: db.serverDate() }) }); await log(tx, openid, 'UPDATE_STORE', STORE_ID, update); });
    return { success: true };
  }
  if (action === 'saveZone') {
    await assertStaff(openid, 'STORE_SETTING');
    const input = event.zone || {};
    const id = input.id ? text(input.id, 128, true) : 'zone_' + crypto.randomBytes(16).toString('hex');
    if (typeof input.enabled !== 'boolean') fail('ZONE_PAYLOAD_INVALID');
    const change = { storeId: STORE_ID, campusName: text(input.campusName, 60, true), zoneName: text(input.zoneName, 60, true), deliveryFee: amount(input.deliveryFee), minGoodsAmount: amount(input.minGoodsAmount), enabled: input.enabled, updatedAt: db.serverDate() };
    await atomic(async tx => { const current = (await tx.collection('delivery_zones').doc(id).get()).data; if (input.id && (!current || current.storeId !== STORE_ID)) fail('ZONE_NOT_FOUND'); await tx.collection('delivery_zones').doc(id).set({ data: Object.assign({}, current || {}, change) }); await log(tx, openid, 'SAVE_ZONE', id, change); });
    return { success: true, id };
  }
  if (action === 'getCampaign') {
    await assertStaff(openid, 'STORE_SETTING');
    const campaigns = (await db.collection('assist_campaigns').where({ storeId: STORE_ID }).orderBy('startAt', 'desc').limit(50).get()).data;
    const store = (await db.collection('store_settings').doc(STORE_ID).get()).data;
    const campaign = store && Object.prototype.hasOwnProperty.call(store, 'activeCampaignId') ? campaigns.find(item => item._id === store.activeCampaignId) || campaigns.find(item => item.enabled) || campaigns[0] || null : campaigns.find(item => item.enabled) || campaigns[0] || null;
    const template = campaign ? (await db.collection('coupon_templates').doc(campaign.starterCouponTemplateId).get()).data : null;
    return { success: true, campaign, template };
  }
  if (action === 'getCouponTemplates') {
    await assertStaff(openid, 'STORE_SETTING');
    const templates = (await db.collection('coupon_templates').where({ storeId: STORE_ID }).orderBy('createdAt', 'desc').limit(200).get()).data;
    return { success: true, data: templates.map(template => Object.assign({}, template, { id: template._id })) };
  }
  if (action === 'saveCouponTemplate') {
    await assertStaff(openid, 'STORE_SETTING');
    const input = event.template || {};
    if (!input || typeof input !== 'object' || Array.isArray(input)) fail('COUPON_TEMPLATE_PAYLOAD_INVALID');
    const scope = input.scope || 'UNIVERSAL';
    if (!['UNIVERSAL', 'TAKEAWAY', 'COURIER'].includes(scope)) fail('COUPON_TEMPLATE_PAYLOAD_INVALID');
    const templateId = 'template_' + crypto.randomBytes(16).toString('hex');
    const previousId = input.id ? text(input.id, 128, true) : '';
    if (input.enabled !== undefined && typeof input.enabled !== 'boolean') fail('COUPON_TEMPLATE_PAYLOAD_INVALID');
    if (input.excludeDeliveryFee !== undefined && typeof input.excludeDeliveryFee !== 'boolean') fail('COUPON_TEMPLATE_PAYLOAD_INVALID');
    const template = { storeId: STORE_ID, name: text(input.name, 60, true), type: 'FIXED', discountAmount: amount(input.discountAmount, 1), minGoodsAmount: amount(input.minGoodsAmount), validDays: amount(input.validDays, 1, 365), scope, excludeDeliveryFee: input.excludeDeliveryFee !== false, enabled: input.enabled !== false, createdAt: db.serverDate(), updatedAt: db.serverDate() };
    const relatedCampaigns = previousId ? (await db.collection('assist_campaigns').where({ storeId: STORE_ID }).limit(200).get()).data : [];
    const storeSnapshot = previousId ? (await db.collection('store_settings').doc(STORE_ID).get()).data : null;
    const referencedCampaignIds = [...new Set(relatedCampaigns.filter(campaign => campaign.starterCouponTemplateId === previousId || campaign.helperCouponTemplateId === previousId).map(campaign => campaign._id).concat(storeSnapshot && storeSnapshot.activeCampaignId ? [storeSnapshot.activeCampaignId] : []))];
    await atomic(async tx => {
      if (previousId) {
        const previous = (await tx.collection('coupon_templates').doc(previousId).get()).data;
        if (!previous || previous.storeId !== STORE_ID) fail('COUPON_TEMPLATE_NOT_FOUND');
        const enabledReferences = [];
        for (const campaignId of referencedCampaignIds) {
          const campaign = (await tx.collection('assist_campaigns').doc(campaignId).get()).data;
          if (campaign && campaign.storeId === STORE_ID && campaign.enabled === true) enabledReferences.push(campaign);
        }
        if (!template.enabled && enabledReferences.length) fail('COUPON_TEMPLATE_IN_USE');
        await tx.collection('coupon_templates').doc(previous._id).update({ data: { enabled: false, supersededAt: db.serverDate(), updatedAt: db.serverDate() } });
        for (const campaign of enabledReferences) {
          const change = {};
          if (campaign.starterCouponTemplateId === previousId) change.starterCouponTemplateId = templateId;
          if (campaign.helperCouponTemplateId === previousId) change.helperCouponTemplateId = templateId;
          if (Object.keys(change).length) await tx.collection('assist_campaigns').doc(campaign._id).update({ data: Object.assign({}, change, { updatedAt: db.serverDate() }) });
        }
      }
      await tx.collection('coupon_templates').doc(templateId).set({ data: template });
      await log(tx, openid, 'SAVE_COUPON_TEMPLATE', templateId, { scope, discountAmount: template.discountAmount, minGoodsAmount: template.minGoodsAmount, enabled: template.enabled, replacedTemplateId: previousId });
    });
    return { success: true, id: templateId };
  }
  if (action === 'saveCampaign') {
    await assertStaff(openid, 'STORE_SETTING');
    const input = event.campaign || {};
    if (typeof input.enabled !== 'boolean') fail('CAMPAIGN_PAYLOAD_INVALID');
    const startAt = new Date(input.startAt); const endAt = new Date(input.endAt);
    if (!Number.isFinite(startAt.getTime()) || !Number.isFinite(endAt.getTime()) || endAt <= startAt) fail('CAMPAIGN_DATES_INVALID');
    const id = input.id ? text(input.id, 128, true) : 'campaign_' + crypto.randomBytes(16).toString('hex');
    // Versioned templates: edits never change already-issued coupons.
    const templateId = 'template_' + crypto.randomBytes(16).toString('hex');
    const scope = input.scope || 'UNIVERSAL';
    if (!['UNIVERSAL', 'TAKEAWAY', 'COURIER'].includes(scope)) fail('CAMPAIGN_PAYLOAD_INVALID');
    const template = { storeId: STORE_ID, name: text(input.name, 60, true), discountAmount: amount(input.discountAmount, 1), minGoodsAmount: amount(input.minGoodsAmount), validDays: amount(input.validDays, 1, 365), scope, excludeDeliveryFee: true, enabled: true, createdAt: db.serverDate(), updatedAt: db.serverDate() };
    const campaign = { storeId: STORE_ID, name: template.name, enabled: input.enabled, startAt, endAt, requiredHelpers: 1, sessionValidHours: 24, starterCouponTemplateId: templateId, helperCouponTemplateId: templateId, updatedAt: db.serverDate() };
    const legacyActive = (await db.collection('assist_campaigns').where({ storeId: STORE_ID, enabled: true }).limit(50).get()).data;
    await atomic(async tx => {
      const store = (await tx.collection('store_settings').doc(STORE_ID).get()).data;
      if (!store) fail('STORE_NOT_FOUND');
      const current = (await tx.collection('assist_campaigns').doc(id).get()).data;
      if (input.id && (!current || current.storeId !== STORE_ID)) fail('CAMPAIGN_NOT_FOUND');
      // Keep one managed active campaign. This store pointer serializes concurrent publications.
      if (input.enabled && store.activeCampaignId && store.activeCampaignId !== id) {
        const old = (await tx.collection('assist_campaigns').doc(store.activeCampaignId).get()).data;
        if (old) await tx.collection('assist_campaigns').doc(old._id).update({ data: { enabled: false, updatedAt: db.serverDate() } });
      }
      if (input.enabled) for (const candidate of legacyActive) {
        if (candidate._id === id || candidate._id === store.activeCampaignId) continue;
        const old = (await tx.collection('assist_campaigns').doc(candidate._id).get()).data;
        if (old && old.enabled) await tx.collection('assist_campaigns').doc(candidate._id).update({ data: { enabled: false, updatedAt: db.serverDate() } });
      }
      await tx.collection('store_settings').doc(STORE_ID).update({ data: { activeCampaignId: input.enabled ? id : store.activeCampaignId === id ? '' : store.activeCampaignId || '', updatedAt: db.serverDate() } });
      await tx.collection('coupon_templates').doc(templateId).set({ data: template });
      await tx.collection('assist_campaigns').doc(id).set({ data: Object.assign({}, current || {}, campaign) });
      await log(tx, openid, 'SAVE_CAMPAIGN', id, { enabled: input.enabled, templateId, discountAmount: template.discountAmount, minGoodsAmount: template.minGoodsAmount });
    });
    return { success: true, id };
  }
  if (action === 'getOperationLogs') {
    await assertStaff(openid, 'STORE_SETTING');
    return { success: true, data: (await db.collection('operation_logs').where({ storeId: STORE_ID }).orderBy('createdAt', 'desc').limit(100).get()).data };
  }
  fail('ACTION_NOT_SUPPORTED');
};
