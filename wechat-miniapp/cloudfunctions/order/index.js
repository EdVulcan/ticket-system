const crypto = require('crypto');
const cloud = require('wx-server-sdk');

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database({ throwOnNotFound: false });
const paymentCore = require('./payment-core').createCore(cloud, db);

const STORE_ID = 'store_001';
const TAKEAWAY = 'TAKEAWAY';
const COURIER = 'COURIER';

function fail(message) {
  const error = new Error(message);
  error.code = message;
  throw error;
}

function now() {
  return db.serverDate();
}

function hash(value) {
  return crypto.createHash('sha256').update(value).digest('hex');
}

function text(value, maxLength, required) {
  const result = typeof value === 'string' ? value.trim() : '';
  if (required && !result) fail('ORDER_PAYLOAD_INVALID');
  if (result.length > maxLength) fail('ORDER_PAYLOAD_INVALID');
  return result;
}

function fulfillmentOf(value) {
  return value && (value.fulfillmentType || value.saleMode || value.channel) === COURIER ? COURIER : TAKEAWAY;
}

function deliveryMethod(value) {
  return value === 'PICKUP' ? 'PICKUP' : 'DELIVERY';
}

function orderIdFor(openid, clientRequestId) {
  return `order_${hash(`${openid}:${clientRequestId}`).slice(0, 40)}`;
}

function makeOrderNo() {
  const date = new Date();
  const pad = (value) => String(value).padStart(2, '0');
  const stamp = `${date.getUTCFullYear()}${pad(date.getUTCMonth() + 1)}${pad(date.getUTCDate())}${pad(date.getUTCHours())}${pad(date.getUTCMinutes())}${pad(date.getUTCSeconds())}`;
  return `SG${stamp}${crypto.randomBytes(5).toString('hex').toUpperCase()}`;
}

async function getStaff(openid) {
  const result = await db.collection('staff').where({ userId: openid, storeId: STORE_ID, enabled: true }).limit(1).get();
  return result.data[0] || null;
}

async function getOrder(openid, orderId) {
  if (typeof orderId !== 'string' || !orderId) return null;
  const result = await db.collection('orders').where({ _id: orderId, userId: openid }).limit(1).get();
  return result.data[0] || null;
}

function publicOrder(order) {
  const safe = Object.assign({}, order);
  safe.paymentClosing = Boolean(order.payment && order.payment.closingRequested && order.status === 'WAIT_PAY');
  delete safe.userId;
  delete safe.clientRequestId;
  delete safe.requestFingerprint;
  delete safe.inventoryRestored;
  delete safe.payment;
  if (safe.delivery) {
    safe.delivery = Object.assign({}, safe.delivery);
    delete safe.delivery.actualFee;
    delete safe.delivery.internalRemark;
  }
  return safe;
}

async function getDeliveryZone(address) {
  if (address.zoneId) {
    const result = await db.collection('delivery_zones').doc(address.zoneId).get();
    return result.data && result.data.storeId === STORE_ID && result.data.enabled === true ? result.data : null;
  }
  const result = await db.collection('delivery_zones').where({ storeId: STORE_ID, zoneName: address.zoneName, enabled: true }).limit(2).get();
  return result.data.find((zone) => !address.campusName || zone.campusName === address.campusName) || null;
}

function addressSnapshot(address, zone) {
  if (!address) return { addressType: 'PICKUP', pickupName: '门店自提' };
  return {
    addressId: address._id,
    addressType: address.addressType === 'SHIPPING' ? 'SHIPPING' : 'CAMPUS',
    zoneId: zone._id,
    campusId: zone.campusId || '',
    campusName: zone.campusName,
    zoneName: zone.zoneName,
    building: address.building,
    room: address.room,
    province: address.province || '',
    city: address.city || '',
    district: address.district || '',
    detailAddress: address.detailAddress || '',
    contactName: address.contactName,
    contactPhone: address.contactPhone,
    remark: address.remark || ''
  };
}

function shippingAddressSnapshot(address) {
  return {
    addressId: address._id,
    addressType: 'SHIPPING',
    province: address.province,
    city: address.city,
    district: address.district,
    detailAddress: address.detailAddress,
    contactName: address.contactName,
    contactPhone: address.contactPhone,
    remark: address.remark || ''
  };
}

function optionLabel(option) {
  const priceDelta = Number(option.priceDelta || 0);
  return `${option.name}${priceDelta ? ` +${priceDelta / 100}元` : ''}`;
}

function optionMatches(option, selected) {
  const value = typeof selected === 'string' ? selected.trim() : selected && (selected.optionId || selected.id || selected.name || '');
  return value === option.id || value === option.name || value === optionLabel(option);
}

function validateOptions(product, input) {
  const selectedOptions = input === undefined ? [] : input;
  if (!Array.isArray(selectedOptions) || selectedOptions.length > 30) fail('PRODUCT_OPTIONS_INVALID');
  const groups = Array.isArray(product.optionGroups) ? product.optionGroups : (Array.isArray(product.options) ? product.options : []);
  const selectedGroups = Object.create(null);
  const selected = [];
  selectedOptions.forEach((raw) => {
    const requestedGroupId = raw && typeof raw === 'object' ? raw.groupId : '';
    const group = groups.find((candidate) => {
      if (requestedGroupId && candidate.id !== requestedGroupId) return false;
      if (selectedGroups[candidate.id]) return false;
      return candidate.enabled !== false && Array.isArray(candidate.options) && candidate.options.some((option) => option.enabled !== false && optionMatches(option, raw));
    });
    if (!group) fail('PRODUCT_OPTIONS_INVALID');
    const option = group.options.find((candidate) => candidate.enabled !== false && optionMatches(candidate, raw));
    const priceDelta = Number(option.priceDelta || 0);
    if (!Number.isInteger(priceDelta) || priceDelta < 0) fail('PRODUCT_OPTIONS_INVALID');
    selectedGroups[group.id] = true;
    selected.push({ groupId: group.id, groupName: group.name, optionId: option.id, optionName: option.name, label: optionLabel(option), priceDelta });
  });
  groups.forEach((group) => {
    if (group.enabled !== false && group.required !== false && !selectedGroups[group.id]) fail('PRODUCT_OPTIONS_REQUIRED');
  });
  return selected;
}

async function lockCoupon(transaction, couponId, openid, orderId, goodsAmount, fee, fulfillmentType) {
  if (!couponId) return { discountAmount: 0, snapshot: null };
  if (typeof couponId !== 'string' || couponId.length > 128) fail('COUPON_INVALID');
  const result = await transaction.collection('user_coupons').doc(couponId).get();
  const coupon = result.data;
  if (!coupon || coupon.userId !== openid || coupon.storeId !== STORE_ID) fail('COUPON_INVALID');
  if (coupon.status !== 'AVAILABLE' || (coupon.expireAt && new Date(coupon.expireAt).getTime() <= Date.now())) fail('COUPON_UNAVAILABLE');
  const minGoodsAmount = Number(coupon.minGoodsAmount || 0);
  const faceValue = Number(coupon.discountAmount || 0);
  const scope = coupon.scope || 'UNIVERSAL';
  if (!['UNIVERSAL', TAKEAWAY, COURIER].includes(scope) || (scope !== 'UNIVERSAL' && scope !== fulfillmentType)) fail('COUPON_UNAVAILABLE');
  if (!Number.isInteger(minGoodsAmount) || minGoodsAmount < 0 || !Number.isInteger(faceValue) || faceValue <= 0 || goodsAmount < minGoodsAmount) fail('COUPON_UNAVAILABLE');
  const discountBase = coupon.excludeDeliveryFee === false ? goodsAmount + fee : goodsAmount;
  const discountAmount = Math.min(faceValue, discountBase);
  await transaction.collection('user_coupons').doc(couponId).update({ data: { status: 'LOCKED', lockedOrderId: orderId, lockedAt: now(), updatedAt: now() } });
  return {
    discountAmount,
    snapshot: {
      id: coupon._id,
      name: coupon.name || '优惠券',
      discountAmount,
      minGoodsAmount,
      excludeDeliveryFee: coupon.excludeDeliveryFee !== false,
      scope
    }
  };
}

exports.main = async (event) => {
  const { OPENID: openid } = cloud.getWXContext();
  if (!openid) fail('UNAUTHORIZED');
  const action = event.action || 'getOrderList';

  if (action === 'getOrderList') {
    const result = await db.collection('orders').where({ userId: openid }).orderBy('createdAt', 'desc').limit(50).get();
    return { success: true, data: result.data.map(publicOrder) };
  }

  if (action === 'getOrderDetail') {
    const order = await getOrder(openid, event.orderId);
    if (!order) fail('ORDER_NOT_FOUND');
    return { success: true, data: publicOrder(order) };
  }

  if (action === 'cancelUnpaidOrder') {
    const order = await getOrder(openid, event.orderId);
    if (!order) fail('ORDER_NOT_FOUND');
    return paymentCore.cancelUnpaid(order._id, openid);
  }

  if (action === 'confirmReceipt') {
    const order = await getOrder(openid, event.orderId);
    if (!order) fail('ORDER_NOT_FOUND');
    const transaction = await db.startTransaction();
    try {
      const current = (await transaction.collection('orders').doc(order._id).get()).data;
      const canConfirm = current && current.userId === openid && (current.status === 'DELIVERING' || (fulfillmentOf(current) === COURIER && current.status === 'SHIPPED'));
      if (!canConfirm) fail('ORDER_STATUS_INVALID');
      await transaction.collection('orders').doc(current._id).update({ data: { status: 'COMPLETED', completedAt: now(), updatedAt: now() } });
      await transaction.commit();
    } catch (error) {
      await transaction.rollback();
      throw error;
    }
    return { success: true };
  }

  if (action === 'createOrder') {
    const payload = event.payload || {};
    const clientRequestId = typeof payload.clientRequestId === 'string' ? payload.clientRequestId.trim() : '';
    const requestedFulfillment = payload.fulfillmentType === COURIER ? COURIER : TAKEAWAY;
    const requestedDeliveryMethod = deliveryMethod(payload.deliveryMethod);
    const hasAddress = typeof payload.addressId === 'string' && Boolean(payload.addressId);
    if (!/^[A-Za-z0-9:_-]{8,100}$/.test(clientRequestId) || !Array.isArray(payload.items) || !payload.items.length || payload.items.length > 50 || (!hasAddress && requestedDeliveryMethod !== 'PICKUP') || (requestedFulfillment === COURIER && requestedDeliveryMethod !== 'DELIVERY')) fail('ORDER_PAYLOAD_INVALID');
    const requestFingerprint = hash(JSON.stringify({ items: payload.items, addressId: payload.addressId || '', couponId: payload.couponId || '', customerRemark: payload.customerRemark || '', fulfillmentType: requestedFulfillment, deliveryMethod: requestedDeliveryMethod, orderGroupId: payload.orderGroupId || '' }));
    const deterministicOrderId = orderIdFor(openid, clientRequestId);
    const existingResult = await db.collection('orders').doc(deterministicOrderId).get();
    if (existingResult.data) {
      if (existingResult.data.requestFingerprint && existingResult.data.requestFingerprint !== requestFingerprint) fail('IDEMPOTENCY_CONFLICT');
      return { success: true, orderId: existingResult.data._id, order: publicOrder(existingResult.data), duplicate: true };
    }

    const storeResult = await db.collection('store_settings').doc(STORE_ID).get();
    let store = storeResult.data;
    const storeOpen = requestedFulfillment === COURIER ? !store || store.courierStatus !== 'PAUSED' : store && store.businessStatus === 'OPEN';
    if (!store || !storeOpen) fail('STORE_NOT_OPEN');
    let address = null; let zone = null;
    if (requestedDeliveryMethod !== 'PICKUP') {
      const addressResult = await db.collection('addresses').where({ _id: payload.addressId, userId: openid }).limit(1).get();
      address = addressResult.data[0];
      if (!address) fail('ADDRESS_NOT_FOUND');
      if (requestedFulfillment === COURIER) {
        if (address.addressType !== 'SHIPPING') fail('ADDRESS_TYPE_INVALID');
      } else {
        if (address.addressType === 'SHIPPING') fail('ADDRESS_TYPE_INVALID');
        zone = await getDeliveryZone(address);
        if (!zone) fail('ADDRESS_OUT_OF_RANGE');
      }
    }
    let deliveryFee = 0; let shippingFee = 0; let minGoodsAmount = 0;
    if (requestedFulfillment === COURIER) {
      shippingFee = Number(store.defaultShippingFee || 0);
      minGoodsAmount = Number(store.courierMinGoodsAmount || 0);
      if (!Number.isInteger(shippingFee) || shippingFee < 0 || !Number.isInteger(minGoodsAmount) || minGoodsAmount < 0) fail('SHIPPING_RULE_INVALID');
    } else if (requestedDeliveryMethod === 'PICKUP') {
      deliveryFee = 0;
      minGoodsAmount = Number(store.defaultMinGoodsAmount || 0);
      if (!Number.isInteger(minGoodsAmount) || minGoodsAmount < 0) fail('STORE_SETTINGS_INVALID');
    } else {
      deliveryFee = Number(zone.deliveryFee);
      minGoodsAmount = Number(zone.minGoodsAmount !== undefined ? zone.minGoodsAmount : store.defaultMinGoodsAmount || 0);
      if (!Number.isInteger(deliveryFee) || deliveryFee < 0 || !Number.isInteger(minGoodsAmount) || minGoodsAmount < 0) fail('DELIVERY_ZONE_INVALID');
    }

    const transaction = await db.startTransaction();
    let duplicateOrder = null;
    try {
      const currentResult = await transaction.collection('orders').doc(deterministicOrderId).get();
      if (currentResult.data) {
        if (currentResult.data.requestFingerprint && currentResult.data.requestFingerprint !== requestFingerprint) fail('IDEMPOTENCY_CONFLICT');
        duplicateOrder = currentResult.data;
      } else {
        // Lock settings/address/zone too: no stale fee, address or paused-store race.
        store = (await transaction.collection('store_settings').doc(STORE_ID).get()).data;
        const storeStillOpen = requestedFulfillment === COURIER ? store && store.courierStatus !== 'PAUSED' : store && store.businessStatus === 'OPEN';
        if (!store || !storeStillOpen) fail('STORE_NOT_OPEN');
        if (requestedDeliveryMethod !== 'PICKUP') {
          address = (await transaction.collection('addresses').doc(payload.addressId).get()).data;
          if (!address || address.userId !== openid) fail('ADDRESS_NOT_FOUND');
          if (requestedFulfillment === COURIER) {
            if (address.addressType !== 'SHIPPING') fail('ADDRESS_TYPE_INVALID');
          } else {
            zone = (await transaction.collection('delivery_zones').doc(zone._id).get()).data;
            if (!zone || zone.storeId !== STORE_ID || zone.enabled !== true || (address.zoneId && address.zoneId !== zone._id)) fail('ADDRESS_OUT_OF_RANGE');
          }
        } else if (requestedFulfillment === COURIER) fail('ORDER_PAYLOAD_INVALID');
        if (requestedFulfillment === COURIER) {
          deliveryFee = 0;
          shippingFee = Number(store.defaultShippingFee || 0);
          minGoodsAmount = Number(store.courierMinGoodsAmount || 0);
          if (!Number.isSafeInteger(shippingFee) || shippingFee < 0 || !Number.isSafeInteger(minGoodsAmount) || minGoodsAmount < 0) fail('SHIPPING_RULE_INVALID');
        } else if (requestedDeliveryMethod === 'PICKUP') {
          deliveryFee = 0;
          minGoodsAmount = Number(store.defaultMinGoodsAmount || 0);
          if (!Number.isSafeInteger(minGoodsAmount) || minGoodsAmount < 0) fail('STORE_SETTINGS_INVALID');
        } else {
          deliveryFee = Number(zone.deliveryFee);
          minGoodsAmount = Number(zone.minGoodsAmount !== undefined ? zone.minGoodsAmount : store.defaultMinGoodsAmount || 0);
          if (!Number.isSafeInteger(deliveryFee) || deliveryFee < 0 || !Number.isSafeInteger(minGoodsAmount) || minGoodsAmount < 0) fail('DELIVERY_ZONE_INVALID');
        }
        const snapshots = [];
        let goodsAmount = 0;
        const productsById = Object.create(null);
        const requiredStock = Object.create(null);
        for (const input of payload.items) {
          if (!input || typeof input.productId !== 'string' || !input.productId || !Number.isInteger(input.quantity) || input.quantity < 1 || input.quantity > 99) fail('PRODUCT_PAYLOAD_INVALID');
          const product = productsById[input.productId] || (await transaction.collection('products').doc(input.productId).get()).data;
          if (!product || product.storeId !== STORE_ID || product.isOnSale !== true || product.isSoldOut === true) fail('PRODUCT_UNAVAILABLE');
          if (fulfillmentOf(product) !== requestedFulfillment) fail('FULFILLMENT_MISMATCH');
          productsById[input.productId] = product;
          requiredStock[input.productId] = (requiredStock[input.productId] || 0) + input.quantity;
          const basePrice = Number(product.basePrice);
          if (!Number.isInteger(basePrice) || basePrice < 0) fail('PRODUCT_PRICE_INVALID');
          const selected = validateOptions(product, input.selectedOptions);
          const optionExtra = selected.reduce((sum, option) => sum + option.priceDelta, 0);
          const unitPrice = basePrice + optionExtra;
          const subtotal = unitPrice * input.quantity;
          goodsAmount += subtotal;
          snapshots.push({ lineId: `${product._id}_${snapshots.length}`, productId: product._id, name: product.name, imageFileId: product.imageFileIds ? product.imageFileIds[0] : '', quantity: input.quantity, unitPrice, subtotal, selectedOptions: selected.map((option) => option.label), selectedOptionIds: selected.map((option) => option.optionId), remark: text(input.remark, 60, false) });
        }
        for (const productId of Object.keys(requiredStock)) {
          const product = productsById[productId];
          const quantity = requiredStock[productId];
          if (product.stockMode !== 'UNLIMITED') {
            if (!Number.isInteger(Number(product.stock)) || Number(product.stock) < quantity) fail('STOCK_NOT_ENOUGH');
            const nextStock = Number(product.stock) - quantity;
            await transaction.collection('products').doc(product._id).update({ data: { stock: nextStock, isSoldOut: nextStock === 0, updatedAt: now() } });
          }
        }
        if (requestedFulfillment === COURIER) {
          const freeThreshold = Number(store.freeShippingThreshold || 0);
          if (!Number.isSafeInteger(freeThreshold) || freeThreshold < 0) fail('SHIPPING_RULE_INVALID');
          shippingFee = freeThreshold > 0 && goodsAmount >= freeThreshold ? 0 : shippingFee;
        }
        if (goodsAmount < minGoodsAmount) fail('MIN_AMOUNT_NOT_REACHED');
        const timeoutMinutes = Math.min(60, Math.max(1, Number(store.orderPayTimeoutMinutes || 10)));
        if (!Number.isFinite(timeoutMinutes)) fail('STORE_SETTINGS_INVALID');
        const order = {
          _id: deterministicOrderId,
          orderNo: makeOrderNo(),
          clientRequestId,
          requestFingerprint,
          storeId: STORE_ID,
          userId: openid,
          orderGroupId: text(payload.orderGroupId, 100, false),
          fulfillmentType: requestedFulfillment,
          deliveryMethod: requestedDeliveryMethod,
          itemsSnapshot: snapshots,
          goodsAmount,
          deliveryFee,
          shippingFee,
          discountAmount: 0,
          payableAmount: goodsAmount + deliveryFee + shippingFee,
          addressSnapshot: requestedFulfillment === COURIER ? shippingAddressSnapshot(address) : addressSnapshot(address, zone),
          couponSnapshot: null,
          status: 'WAIT_PAY',
          inventoryRestored: false,
          customerRemark: text(payload.customerRemark, 200, false),
          delivery: requestedFulfillment === TAKEAWAY ? { status: requestedDeliveryMethod === 'PICKUP' ? 'PICKUP' : 'NOT_CALLED', runnerName: '', runnerPhone: '', actualFee: deliveryFee, internalRemark: '' } : null,
          shipping: requestedFulfillment === COURIER ? { status: 'WAIT_SHIP', carrier: '', trackingNo: '', shippingRemark: '' } : null,
          payment: { outTradeNo: '', transactionId: '', paidAmount: 0 },
          expireAt: new Date(Date.now() + timeoutMinutes * 60 * 1000),
          createdAt: now(),
          updatedAt: now()
        };
        const coupon = await lockCoupon(transaction, payload.couponId || '', openid, deterministicOrderId, goodsAmount, requestedFulfillment === COURIER ? shippingFee : deliveryFee, requestedFulfillment);
        order.discountAmount = coupon.discountAmount;
        order.couponSnapshot = coupon.snapshot;
        order.payableAmount = Math.max(0, goodsAmount + deliveryFee + shippingFee - coupon.discountAmount);
        if (!Number.isSafeInteger(order.payableAmount) || order.payableAmount > 10000000) fail('ORDER_AMOUNT_INVALID');
        if (order.payableAmount === 0) {
          // Fully discounted orders have no external payment transaction.
          order.status = 'PAID';
          order.paidAt = now();
          order.payment.method = 'COUPON_ZERO';
          if (order.couponSnapshot) await transaction.collection('user_coupons').doc(order.couponSnapshot.id).update({ data: { status: 'USED', usedOrderId: deterministicOrderId, usedAt: now(), updatedAt: now() } });
          await transaction.collection('payment_records').doc(`free_${order.orderNo}`).set({ data: { orderId: deterministicOrderId, orderNo: order.orderNo, userId: openid, type: 'ZERO_PAYMENT', amount: 0, status: 'SUCCESS', createdAt: now() } });
        }
        await transaction.collection('orders').doc(deterministicOrderId).set({ data: order });
      }
      await transaction.commit();
    } catch (error) {
      await transaction.rollback();
      throw error;
    }
    if (duplicateOrder) return { success: true, orderId: duplicateOrder._id, order: publicOrder(duplicateOrder), duplicate: true };
    const saved = await db.collection('orders').doc(deterministicOrderId).get();
    if (saved.data.status === 'PAID') await paymentCore.notifyStaff(saved.data);
    return { success: true, orderId: deterministicOrderId, order: publicOrder(saved.data) };
  }

  if (action === 'changeStatus') {
    const staff = await getStaff(openid);
    if (!staff || (staff.permissions || []).indexOf('ORDER_MANAGE') < 0) fail('FORBIDDEN');
    const next = event.status;
    if (typeof event.orderId !== 'string' || !event.orderId || typeof next !== 'string') fail('ORDER_STATUS_INVALID');
    const delivery = next === 'DELIVERING' && event.delivery ? event.delivery : {};
    if (delivery && typeof delivery !== 'object') fail('DELIVERY_PAYLOAD_INVALID');
    const runnerName = typeof delivery.runnerName === 'string' ? delivery.runnerName.trim() : '';
    const runnerPhone = typeof delivery.runnerPhone === 'string' ? delivery.runnerPhone.trim() : '';
    if (runnerName.length > 40 || runnerPhone.length > 20 || (runnerPhone && !/^1\d{10}$/.test(runnerPhone))) fail('DELIVERY_PAYLOAD_INVALID');
    const shipping = next === 'SHIPPED' && event.shipping ? event.shipping : {};
    if (shipping && typeof shipping !== 'object') fail('SHIPPING_PAYLOAD_INVALID');
    const carrier = typeof shipping.carrier === 'string' ? shipping.carrier.trim() : '';
    const trackingNo = typeof shipping.trackingNo === 'string' ? shipping.trackingNo.trim() : '';
    const shippingRemark = typeof shipping.shippingRemark === 'string' ? shipping.shippingRemark.trim() : '';
    if (carrier.length > 40 || trackingNo.length > 80 || shippingRemark.length > 160 || (next === 'SHIPPED' && (!carrier || !trackingNo))) fail('SHIPPING_PAYLOAD_INVALID');
    const transaction = await db.startTransaction();
    try {
      const current = (await transaction.collection('orders').doc(event.orderId).get()).data;
      const courier = current && fulfillmentOf(current) === COURIER;
      const allowed = courier ? { PAID: ['SHIPPED'], SHIPPED: ['COMPLETED'] } : { PAID: ['PREPARING'], PREPARING: ['DELIVERING'], DELIVERING: ['COMPLETED'] };
      if (!current || current.storeId !== STORE_ID || !allowed[current.status] || allowed[current.status].indexOf(next) < 0) fail('ORDER_STATUS_INVALID');
      const change = { status: next, updatedAt: now() };
      if (next === 'PREPARING') change.preparingAt = now();
      if (next === 'DELIVERING') {
        change.deliveringAt = now();
        change['delivery.status'] = 'CALLED';
        if (runnerName) change['delivery.runnerName'] = runnerName;
        if (runnerPhone) change['delivery.runnerPhone'] = runnerPhone;
      }
      if (next === 'COMPLETED') change.completedAt = now();
      if (next === 'SHIPPED') {
        change.shippedAt = now();
        change['shipping.status'] = 'SHIPPED';
        change['shipping.carrier'] = carrier;
        change['shipping.trackingNo'] = trackingNo;
        change['shipping.shippingRemark'] = shippingRemark;
      }
      await transaction.collection('orders').doc(current._id).update({ data: change });
      await transaction.collection('operation_logs').doc(`status_${current._id}_${next}`).set({ data: { storeId: STORE_ID, operatorId: openid, action: 'ORDER_STATUS', orderId: current._id, change: { from: current.status, to: next }, createdAt: now() } });
      await transaction.commit();
    } catch (error) {
      await transaction.rollback();
      throw error;
    }
    return { success: true };
  }

  fail('ACTION_NOT_SUPPORTED');
};
