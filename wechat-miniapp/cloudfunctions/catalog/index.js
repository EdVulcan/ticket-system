const cloud = require('wx-server-sdk');

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database({ throwOnNotFound: false });
const _ = db.command;
const STORE_ID = 'store_001';

exports.main = async (event) => {
  const storeId = STORE_ID;
  const [storeResult, categoriesResult, productsResult, zonesResult] = await Promise.all([
    db.collection('store_settings').doc(storeId).get(),
    db.collection('categories').where({ storeId, enabled: true }).orderBy('sort', 'asc').limit(100).get(),
    db.collection('products').where({ storeId, isOnSale: true, isSoldOut: _.neq(true) }).orderBy('sort', 'asc').limit(200).get(),
    db.collection('delivery_zones').where({ storeId, enabled: true }).orderBy('sort', 'asc').limit(200).get()
  ]);
  if (!storeResult.data) throw new Error('STORE_NOT_CONFIGURED');
  return { success: true, store: storeResult.data, categories: categoriesResult.data, products: productsResult.data, deliveryZones: zonesResult.data };
};
