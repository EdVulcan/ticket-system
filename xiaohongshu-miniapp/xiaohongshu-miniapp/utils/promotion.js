function asCents(value) {
  const cents = Number(value);
  return Number.isFinite(cents) && cents > 0 ? Math.floor(cents) : 0;
}

function asTime(value) {
  if (typeof value === 'number' && Number.isFinite(value)) return value > 100000000000 ? value : value * 1000;
  const parsed = Date.parse(value || '');
  return Number.isFinite(parsed) ? parsed : 0;
}

function normalize(raw, receivedAt) {
  const value = raw || {};
  const serverTime = asTime(value.server_now || value.server_time);
  const clockOffsetMs = serverTime ? serverTime - (receivedAt || Date.now()) : 0;
  return {
    status: String(value.status || 'inactive'),
    grantId: String(value.grant_id || ''),
    discountCents: asCents(value.discount_cents),
    expiresAt: asTime(value.expires_at),
    nextEligibleAt: asTime(value.next_eligible_at),
    mappingIds: (value.eligible_mapping_ids || value.mapping_ids || []).map(Number).filter(Number.isFinite),
    reservedOrderNo: String(value.reserved_order_no || ''),
    clockOffsetMs
  };
}

function now(opportunity, clientNow) {
  return (clientNow || Date.now()) + Number((opportunity || {}).clockOffsetMs || 0);
}

function isAvailable(opportunity, clientNow) {
  return opportunity && opportunity.status === 'available' && opportunity.discountCents > 0 &&
    (!opportunity.expiresAt || opportunity.expiresAt > now(opportunity, clientNow));
}

function appliesTo(opportunity, mappingID, clientNow) {
  return isAvailable(opportunity, clientNow) && opportunity.mappingIds.indexOf(Number(mappingID)) >= 0;
}

function money(cents) {
  return (Math.max(0, Number(cents) || 0) / 100).toFixed(2);
}

function countdown(opportunity, clientNow) {
  if (!isAvailable(opportunity, clientNow) || !opportunity.expiresAt) return '';
  const seconds = Math.max(0, Math.ceil((opportunity.expiresAt - now(opportunity, clientNow)) / 1000));
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return `${minutes}:${String(remainder).padStart(2, '0')}`;
}

module.exports = { normalize, isAvailable, appliesTo, money, countdown };
