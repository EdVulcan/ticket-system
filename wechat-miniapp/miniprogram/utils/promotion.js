// Display eligibility only. The server authorizes every campaign request
// against the current tenant, channel account and selected business.
function appliesToBusiness(promotion, businessType) {
  const type = String(businessType || '').trim().toLowerCase();
  if (!promotion || (type !== 'restaurant' && type !== 'retail')) return false;
  const types = promotion.businessTypes !== undefined ? promotion.businessTypes : promotion.business_types;
  if (types !== undefined) {
    // The explicit list is authoritative, including an empty list. The legacy
    // singular field may contain just its first entry and must not override it.
    return Array.isArray(types) && types.some(value => String(value || '').trim().toLowerCase() === type);
  }
  const legacyType = String(promotion.businessType || promotion.business_type || '').trim().toLowerCase();
  // Older scoped responses may omit the business field entirely.
  return !legacyType || legacyType === type;
}

module.exports = { appliesToBusiness };
