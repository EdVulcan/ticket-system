const FULFILLMENT = Object.freeze({
  TAKEAWAY: 'TAKEAWAY',
  COURIER: 'COURIER'
});

function fulfillmentOf(item) {
  return item && (item.fulfillmentType || item.saleMode || item.channel) === FULFILLMENT.COURIER ? FULFILLMENT.COURIER : FULFILLMENT.TAKEAWAY;
}

function addressTypeOf(address) {
  const value = String(address && (address.addressType || address.address_type || address.type) || '').toUpperCase();
  if (value === 'SHIPPING') return 'SHIPPING';
  if (value === 'DELIVERY') return 'DELIVERY';
  // Historical CAMPUS records remain readable during the address migration.
  if (value === 'CAMPUS') return 'CAMPUS';
  return 'DELIVERY';
}

function isCourier(item) {
  return fulfillmentOf(item) === FULFILLMENT.COURIER;
}

function businessTypeOf(item) {
  const value = String(item && (item.businessType || item.business_type || '') || '').toLowerCase();
  if (value === 'retail' || value === 'restaurant') return value;
  return fulfillmentOf(item) === FULFILLMENT.COURIER ? 'retail' : 'restaurant';
}

function fulfillmentForBusinessType(type) {
  return String(type || '').toLowerCase() === 'retail' ? FULFILLMENT.COURIER : FULFILLMENT.TAKEAWAY;
}

function businessTypeForFulfillment(type) {
  return String(type || '').toUpperCase() === FULFILLMENT.COURIER ? 'retail' : 'restaurant';
}

module.exports = { FULFILLMENT, fulfillmentOf, addressTypeOf, isCourier, businessTypeOf, fulfillmentForBusinessType, businessTypeForFulfillment };
