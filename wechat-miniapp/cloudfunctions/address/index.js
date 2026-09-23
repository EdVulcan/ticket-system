const crypto = require('crypto');
const cloud = require('wx-server-sdk');
cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database({ throwOnNotFound: false });
const STORE_ID = 'store_001';

function fail(code) { const error = new Error(code); error.code = code; throw error; }
function text(value, max, required) {
  const result = typeof value === 'string' ? value.trim() : '';
  if ((required && !result) || result.length > max) fail('ADDRESS_PAYLOAD_INVALID');
  return result;
}
function addressTypeOf(address) { return address && address.addressType === 'SHIPPING' ? 'SHIPPING' : 'CAMPUS'; }
function uniqueIds(values) { return [...new Set((values || []).filter(value => typeof value === 'string' && value))]; }
function publicAddress(address, defaultId, zone) {
  const addressType = address.addressType === 'SHIPPING' ? 'SHIPPING' : 'CAMPUS';
  return { id: address._id, _id: address._id, addressType, zoneId: address.zoneId || '', campusId: address.campusId || '', campusName: address.campusName || '', zoneName: address.zoneName || '', building: address.building || '', room: address.room || '', province: address.province || '', city: address.city || '', district: address.district || '', detailAddress: address.detailAddress || '', contactName: address.contactName, contactPhone: address.contactPhone, remark: address.remark || '', isDefault: address._id === defaultId, deliverable: addressType === 'SHIPPING' || Boolean(zone), deliveryFee: zone ? Number(zone.deliveryFee) : 0, shippingFee: addressType === 'SHIPPING' ? 0 : 0, minGoodsAmount: zone ? Number(zone.minGoodsAmount || 0) : 0 };
}
async function zones() {
  return (await db.collection('delivery_zones').where({ storeId: STORE_ID, enabled: true }).limit(200).get()).data;
}
async function legacyAddresses(openid) {
  return (await db.collection('addresses').where({ userId: openid }).limit(50).get()).data;
}
async function bookInTransaction(tx, openid, legacy) {
  const result = await tx.collection('address_books').doc(openid).get();
  const book = result.data || {};
  const campusFallback = legacy.find(item => addressTypeOf(item) === 'CAMPUS' && item.isDefault) || legacy.find(item => addressTypeOf(item) === 'CAMPUS');
  const shippingFallback = legacy.find(item => addressTypeOf(item) === 'SHIPPING' && item.isDefault) || legacy.find(item => addressTypeOf(item) === 'SHIPPING');
  const defaultCampusId = book.defaultCampusId || book.defaultId || campusFallback && campusFallback._id || '';
  const defaultShippingId = book.defaultShippingId || shippingFallback && shippingFallback._id || '';
  return Object.assign({}, book, { userId: openid, addressIds: uniqueIds((book.addressIds || []).concat(legacy.map(item => item._id))), defaultCampusId, defaultShippingId, defaultId: defaultCampusId });
}
async function replacementDefault(tx, ids, addressType, excludedId) {
  let first = '';
  for (const id of ids) {
    if (id === excludedId) continue;
    const result = await tx.collection('addresses').doc(id).get();
    if (!result.data || addressTypeOf(result.data) !== addressType) continue;
    if (!first) first = id;
    if (result.data.isDefault) return id;
  }
  return first;
}
exports.main = async (event = {}) => {
  const { OPENID: openid } = cloud.getWXContext();
  if (!openid) fail('UNAUTHORIZED');
  const action = event.action || 'list';
  if (action === 'getZones') return { success: true, data: (await zones()).map(zone => ({ id: zone._id, addressType: 'CAMPUS', campusId: zone.campusId || '', campusName: zone.campusName, zoneName: zone.zoneName, deliveryFee: zone.deliveryFee, minGoodsAmount: zone.minGoodsAmount || 0 })) };
  if (action === 'list') {
    const [addresses, zoneList, book] = await Promise.all([legacyAddresses(openid), zones(), db.collection('address_books').doc(openid).get()]);
    const defaultCampusId = book.data && (book.data.defaultCampusId || book.data.defaultId) || (addresses.find(item => addressTypeOf(item) === 'CAMPUS' && item.isDefault) || addresses.find(item => addressTypeOf(item) === 'CAMPUS') || {})._id || '';
    const defaultShippingId = book.data && book.data.defaultShippingId || (addresses.find(item => addressTypeOf(item) === 'SHIPPING' && item.isDefault) || addresses.find(item => addressTypeOf(item) === 'SHIPPING') || {})._id || '';
    return { success: true, data: addresses.map(item => {
      const itemDefault = addressTypeOf(item) === 'SHIPPING' ? defaultShippingId : defaultCampusId;
      return publicAddress(item, itemDefault, zoneList.find(zone => zone._id === item.zoneId));
    }).sort((a, b) => Number(b.isDefault) - Number(a.isDefault)) };
  }
  if (!['upsert', 'setDefault', 'delete'].includes(action)) fail('ACTION_NOT_SUPPORTED');
  const input = event.address || {};
  let addressId = action === 'upsert' ? input.id || input._id || '' : event.addressId;
  if (typeof addressId !== 'string' || addressId.length > 128 || (action !== 'upsert' && !addressId)) fail('ADDRESS_PAYLOAD_INVALID');
  const creating = action === 'upsert' && !addressId;
  let zone; let fields; let addressType = 'CAMPUS';
  let existingPreview = null;
  if (action === 'upsert') {
    if (input.addressType !== undefined && !['CAMPUS', 'SHIPPING'].includes(input.addressType)) fail('ADDRESS_TYPE_INVALID');
    if (input.addressType === undefined && addressId) existingPreview = (await db.collection('addresses').doc(addressId).get()).data;
    addressType = input.addressType === 'SHIPPING' ? 'SHIPPING' : existingPreview ? addressTypeOf(existingPreview) : 'CAMPUS';
    const contactPhone = text(input.contactPhone, 11, true);
    if (!/^1\d{10}$/.test(contactPhone)) fail('ADDRESS_PHONE_INVALID');
    if (addressType === 'SHIPPING') {
      fields = { userId: openid, storeId: STORE_ID, addressType, zoneId: '', campusId: '', campusName: '', zoneName: '', province: text(input.province, 30, true), city: text(input.city, 30, true), district: text(input.district, 40, true), detailAddress: text(input.detailAddress, 160, true), building: '', room: '', contactName: text(input.contactName, 40, true), contactPhone, remark: text(input.remark, 120, false), updatedAt: db.serverDate() };
    } else {
      const zoneId = text(input.zoneId, 128, true);
      zone = (await db.collection('delivery_zones').doc(zoneId).get()).data;
      if (!zone || zone.storeId !== STORE_ID || zone.enabled !== true) fail('ADDRESS_OUT_OF_RANGE');
      fields = { userId: openid, storeId: STORE_ID, addressType, zoneId, campusId: zone.campusId || '', campusName: zone.campusName, zoneName: zone.zoneName, province: '', city: '', district: '', detailAddress: '', building: text(input.building, 40, true), room: text(input.room, 40, true), contactName: text(input.contactName, 40, true), contactPhone, remark: text(input.remark, 120, false), updatedAt: db.serverDate() };
    }
    if (creating) addressId = 'address_' + crypto.randomBytes(16).toString('hex');
  }
  // Migration query outside transaction. The single per-user book document serializes writers.
  const legacy = await legacyAddresses(openid);
  const tx = await db.startTransaction();
  let defaultId;
  try {
    const book = await bookInTransaction(tx, openid, legacy);
    const ids = book.addressIds.slice();
    const existing = (await tx.collection('addresses').doc(addressId).get()).data;
    if (!creating && (!existing || existing.userId !== openid || !ids.includes(addressId))) fail('ADDRESS_NOT_FOUND');
    if (creating && (existing || ids.length >= 50)) fail('ADDRESS_LIMIT_REACHED');
    if (action === 'upsert' && input.addressType === undefined && existingPreview && existing && addressTypeOf(existing) !== addressTypeOf(existingPreview)) fail('ADDRESS_CHANGED_REFRESH');
    const previousType = existing ? addressTypeOf(existing) : addressType;
    let campusDefaultId = book.defaultCampusId || book.defaultId || '';
    let shippingDefaultId = book.defaultShippingId || '';
    if (action !== 'upsert') {
      const currentType = addressTypeOf(existing);
      addressType = currentType;
    }
    if (action === 'upsert') {
      if (creating) ids.push(addressId);
      if (previousType !== addressType && (previousType === 'SHIPPING' ? shippingDefaultId : campusDefaultId) === addressId) {
        const replacement = await replacementDefault(tx, ids, previousType, addressId);
        if (previousType === 'SHIPPING') shippingDefaultId = replacement; else campusDefaultId = replacement;
      }
      const currentDefault = addressType === 'SHIPPING' ? shippingDefaultId : campusDefaultId;
      if (input.isDefault === true || !currentDefault) {
        if (addressType === 'SHIPPING') shippingDefaultId = addressId; else campusDefaultId = addressId;
      }
      if (creating) await tx.collection('addresses').doc(addressId).set({ data: Object.assign({}, fields, { createdAt: db.serverDate() }) });
      else await tx.collection('addresses').doc(addressId).update({ data: fields });
    } else if (action === 'delete') {
      ids.splice(ids.indexOf(addressId), 1);
      await tx.collection('addresses').doc(addressId).remove();
      if ((addressType === 'SHIPPING' ? shippingDefaultId : campusDefaultId) === addressId) {
        const replacement = await replacementDefault(tx, ids, addressType, addressId);
        if (addressType === 'SHIPPING') shippingDefaultId = replacement; else campusDefaultId = replacement;
      }
    } else if (addressType === 'SHIPPING') shippingDefaultId = addressId;
    else campusDefaultId = addressId;
    const existingBook = Object.assign({}, book, { addressIds: ids });
    existingBook.defaultShippingId = shippingDefaultId;
    existingBook.defaultCampusId = campusDefaultId;
    existingBook.defaultId = campusDefaultId;
    defaultId = addressType === 'SHIPPING' ? shippingDefaultId : campusDefaultId;
    await tx.collection('address_books').doc(openid).set({ data: Object.assign(existingBook, { userId: openid, updatedAt: db.serverDate() }) });
    await tx.commit();
  } catch (error) { await tx.rollback(); throw error; }
  if (action !== 'upsert') return { success: true };
  const saved = (await db.collection('addresses').doc(addressId).get()).data;
  return { success: true, data: publicAddress(saved, defaultId, saved && saved.addressType === 'SHIPPING' ? null : zone) };
};
