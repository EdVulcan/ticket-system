const brand = require('../config/brand');
const storage = require('./storage');
const api = require('./api');

const pending = {};

function name(store) {
  return storage.getStorefrontName() || String(store && (store.displayName || store.display_name || store.name) || '').trim() || brand.name;
}

function apply(page, store) {
  const storeName = name(store);
  page.setData({ storeName });
  return storeName;
}

// A shared-link landing must load the name even when no catalog tab was visited.
function refresh(page, preferredBusinessType) {
  apply(page);
  if (!api.isProduction()) return Promise.resolve(apply(page, storage.getStore()));
  return api.ensureSession().then(() => {
    const types = api.getAuthorizedBusinesses().map(item => String(item.businessType || item.business_type || '').toLowerCase());
    const type = types.includes(preferredBusinessType) ? preferredBusinessType : types.find(value => value === 'restaurant' || value === 'retail');
    const key = type || 'legacy';
    if (!pending[key]) {
      pending[key] = Promise.resolve().then(() => api.getCatalog(type)).finally(() => { delete pending[key]; });
    }
    return pending[key];
  }).then(catalog => apply(page, catalog.store)).catch(error => {
    console.warn('storefront name unavailable', error && error.code || 'CATALOG_UNAVAILABLE');
    return apply(page);
  });
}

module.exports = { name, apply, refresh };
