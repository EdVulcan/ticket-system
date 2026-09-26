const mock = require('../data/mock');
const storage = require('./storage');
const commerce = require('../config/commerce');
const http = require('./http');

const palette = {
  rice: ['🍗', '#FBE7D8'],
  noodle: ['🍜', '#F6DCD3'],
  snack: ['🍿', '#F5E5B9'],
  drink: ['🍋', '#E7F0CB']
};

let catalogState = { products: [], categories: [], locations: [], store: null };
const catalogStates = {};

function appGlobalData() {
  try {
    if (typeof getApp === 'function') {
      const app = getApp();
      return app && app.globalData || {};
    }
  } catch (error) {
    // Static tests can load this module without a running App.
  }
  return {};
}

function productionMode() {
  const data = appGlobalData();
  return (data.deploymentMode || http.isProduction()) === 'production';
}

// Kept as a compatibility name for existing catalog pages. It means that the
// HTTPS storefront configuration is independent of any legacy backend.
function cloudEnabled() { return productionMode() && http.isConfigured(); }

function apiError(code, message) {
  return http.createError(code, message || '服务暂时不可用，请稍后重试');
}

function unwrap(value) {
  if (!value || typeof value !== 'object') return {};
  if (value.data && !Array.isArray(value.data) && typeof value.data === 'object') return value.data;
  return value;
}

function firstDefined() {
  for (let index = 0; index < arguments.length; index += 1) {
    const value = arguments[index];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

function displayText(value, fallback) {
  if (value === undefined || value === null) return fallback;
  const text = String(value).trim();
  if (!text || text.toLowerCase() === 'undefined' || text.toLowerCase() === 'null') return fallback;
  return text;
}

function numericId(value) {
  if (value === undefined || value === null || value === '') return value;
  const text = String(value);
  return /^\d+$/.test(text) ? Number(text) : value;
}

function priceText(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

function isActiveStatus(value) {
  return String(value || '').trim().toLowerCase() === 'active';
}

function normalizeOption(option) {
  if (typeof option === 'string') {
    const text = option.trim();
    const match = text.match(/\s*\+(\d+(?:\.\d{1,2})?)元$/);
    return {
      id: undefined,
      name: match ? text.slice(0, match.index).trim() : text,
      label: match ? text.slice(0, match.index).trim() : text,
      priceDelta: match ? Math.round(Number(match[1]) * 100) : 0,
      enabled: true
    };
  }
  const value = option || {};
  const delta = priceText(firstDefined(value.priceDeltaCents, value.price_delta_cents, value.priceDelta, 0));
  const name = displayText(firstDefined(value.name, value.label, value.value, ''), '');
  return {
    id: firstDefined(value.id, value.optionId, value.option_id),
    name,
    label: name,
    priceDelta: delta,
    enabled: value.enabled !== false && value.status !== 'inactive'
  };
}

function cleanImageURL(value) {
  const url = String(value || '').trim();
  return !url || url === 'undefined' || url === 'null' ? '' : url;
}

function normalizeStorefrontContact(value) {
  const contact = unwrap(value);
  const qrCodeUrl = cleanImageURL(firstDefined(contact.qrCodeUrl, contact.qr_code_url, ''));
  const status = String(firstDefined(contact.status, 'disabled') || 'disabled').trim().toLowerCase();
  const available = Boolean(firstDefined(contact.available, status === 'active' && qrCodeUrl));
  if (!available || status !== 'active' || !qrCodeUrl) {
    return { available: false, status: 'disabled', contactType: '', contactName: '', wechatId: '', qrCodeUrl: '' };
  }
  return {
    available: true,
    status,
    contactType: String(firstDefined(contact.contactType, contact.contact_type, 'personal_wechat') || 'personal_wechat'),
    contactName: displayText(firstDefined(contact.contactName, contact.contact_name, ''), ''),
    wechatId: displayText(firstDefined(contact.wechatId, contact.wechat_id, ''), ''),
    qrCodeUrl
  };
}

function uniqueImageURLs(values) {
  return values.map(cleanImageURL).filter((url, index, all) => url && all.indexOf(url) === index);
}

function normalizeMediaEntries(value) {
  const parsed = parseJSON(value);
  const source = Array.isArray(parsed) ? parsed : parsed && Array.isArray(parsed.media) ? parsed.media : [];
  return source.map((entry, index) => ({
    kind: String(firstDefined(entry && entry.kind, entry && entry.type, '') || '').trim().toLowerCase(),
    url: cleanImageURL(firstDefined(entry && entry.url, entry && entry.imageUrl, entry && entry.image_url, '')),
    order: Number(firstDefined(entry && entry.sortOrder, entry && entry.sort_order, entry && entry.order, index))
  })).filter(entry => entry.url && (entry.kind === 'cover' || entry.kind === 'detail')).sort((left, right) => {
    const leftOrder = Number.isFinite(left.order) ? left.order : 0;
    const rightOrder = Number.isFinite(right.order) ? right.order : 0;
    return leftOrder - rightOrder;
  });
}

function snapshotMediaEntries(item) {
  item = item || {};
  return normalizeMediaEntries(firstDefined(item.mediaSnapshot, item.media_snapshot, item.MediaSnapshotJSON, item.media_snapshot_json));
}

function resolveCoverImage(item, fallback) {
  item = item || {};
  const explicit = cleanImageURL(firstDefined(item.coverImageUrl, item.cover_image_url, ''));
  if (explicit) return explicit;
  const mediaCover = normalizeMediaEntries(item.media).find(entry => entry.kind === 'cover');
  if (mediaCover) return mediaCover.url;
  if (fallback) {
    const fallbackCover = resolveCoverImage(fallback);
    if (fallbackCover) return fallbackCover;
  }
  return cleanImageURL(firstDefined(item.imageFileId, item.image_file_id, ''));
}

function normalizeProduct(item, index, defaultBusinessType) {
  item = item || {};
  const skuList = Array.isArray(item.skus) ? item.skus : Array.isArray(item.SKUs) ? item.SKUs : [];
  const activeSkus = skuList.filter((sku) => sku && (!sku.status || isActiveStatus(sku.status)));
  const activeSku = item.sku || activeSkus[0] || skuList[0] || {};
  const name = displayText(firstDefined(item.name, item.productName, item.product_name, item.title), '未命名商品');
  const description = displayText(firstDefined(item.description, item.shortDescription, item.short_description, item.subtitle, item.summary, item.productDescription, item.product_description), '');
  const categoryName = displayText(firstDefined(item.categoryName, item.category_name, ''), '');
  const categoryId = firstDefined(item.categoryId, item.category_id, item.category, item.storefrontCategory, item.storefront_category, categoryName || undefined, 'all');
  const visual = palette[categoryId] || palette.rice;
  const groups = firstDefined(item.optionGroups, item.option_groups, item.options, []);
  const options = (Array.isArray(groups) ? groups : []).filter((group) => group && group.enabled !== false && group.status !== 'inactive').map((group) => {
    const values = firstDefined(group.optionRecords, group.options, group.values, []);
    const records = (Array.isArray(values) ? values : []).map(normalizeOption).filter((option) => option.enabled);
    return {
      id: firstDefined(group.id, group.optionGroupId, group.option_group_id, group.name),
      name: displayText(group.name || group.label, '可选项'),
      required: group.required !== false,
      values: records.map((option) => `${option.name}${option.priceDelta ? ` +${option.priceDelta / 100}元` : ''}`),
      optionRecords: records
    };
  });
  const inventory = item.inventory || {};
  const stockValue = firstDefined(item.stock, item.availableQty, item.available_qty, inventory.availableQty, inventory.available_qty);
  const stockMode = String(firstDefined(item.stockMode, item.stock_mode, item.inventoryType, '') || '').toUpperCase();
  const isUnlimited = stockMode === 'UNLIMITED' || item.stockType === 'unlimited' || item.stock_type === 'unlimited';
  const status = String(firstDefined(item.status, item.saleStatus, item.sale_status, '') || '').toLowerCase();
  const onSale = item.isOnSale !== undefined ? item.isOnSale : item.is_on_sale !== undefined ? item.is_on_sale : status ? status === 'online' || status === 'active' : true;
  const price = priceText(firstDefined(item.basePrice, item.base_price, item.basePriceCents, item.base_price_cents, item.priceCents, item.price_cents, activeSku.priceCents, activeSku.price_cents, item.price, 0));
  const originalPrice = priceText(firstDefined(item.originalPrice, item.original_price, item.originalPriceCents, item.original_price_cents, activeSku.originalPriceCents, activeSku.original_price_cents, price));
  // The storefront catalog may intentionally omit stock. Keep an optimistic
  // display cap in that case; add/checkout still goes through server stock
  // validation and this value is never sent as an order authority.
  const stockTextValue = stockValue === undefined || stockValue === null ? '' : String(stockValue).trim().toLowerCase();
  const stockKnown = Boolean(stockTextValue && stockTextValue !== 'undefined' && stockTextValue !== 'null' && Number.isFinite(Number(stockValue)));
  const stock = stockKnown ? priceText(stockValue) : 99;
  const stockText = isUnlimited ? '不限库存' : stockKnown ? `剩余库存：${stock}` : '库存待同步';
  const soldValue = firstDefined(item.sold, item.sales, item.soldCount, item.sold_count);
  const soldNumber = Number(soldValue);
  const soldKnown = soldValue !== undefined && soldValue !== null && String(soldValue).trim() !== '' && String(soldValue).trim().toLowerCase() !== 'undefined' && String(soldValue).trim().toLowerCase() !== 'null' && Number.isFinite(soldNumber);
  const businessType = String(firstDefined(item.businessType, item.business_type, defaultBusinessType, '') || '').toLowerCase();
  const fulfillmentType = businessType === 'retail' || commerce.fulfillmentOf(item) === commerce.FULFILLMENT.COURIER ? commerce.FULFILLMENT.COURIER : commerce.FULFILLMENT.TAKEAWAY;
  const id = firstDefined(item.id, item.productId, item.product_id, item._id);
  const skuId = firstDefined(item.skuId, item.sku_id, activeSku.id, activeSku.skuId, activeSku.sku_id);
  const catalogReady = Boolean(id && skuId && activeSkus.length);
  const legacyImageURLs = uniqueImageURLs(Array.isArray(item.imageFileIds) ? item.imageFileIds : Array.isArray(item.image_file_ids) ? item.image_file_ids : []);
  const legacyCoverURL = cleanImageURL(firstDefined(item.imageUrl, item.image_url, legacyImageURLs[0], ''));
  const media = normalizeMediaEntries(item.media);
  const mediaCover = media.find(entry => entry.kind === 'cover');
  const coverImageUrl = resolveCoverImage(item) || cleanImageURL(mediaCover && mediaCover.url) || legacyCoverURL;
  const existingDetailImageUrls = Array.isArray(item.detailImageUrls) ? item.detailImageUrls : Array.isArray(item.detail_image_urls) ? item.detail_image_urls : [];
  const detailImageUrls = uniqueImageURLs(media.filter(entry => entry.kind === 'detail').map(entry => entry.url).concat(existingDetailImageUrls)).filter(url => url !== coverImageUrl);
  const imageFileIds = uniqueImageURLs([coverImageUrl].concat(detailImageUrls, legacyImageURLs));
  return Object.assign({}, item, {
    id,
    name,
    description,
    productId: id,
    categoryId,
    categoryName,
    price,
    originalPrice,
    stock,
    stockMode: isUnlimited ? 'UNLIMITED' : stockMode || 'LIMITED',
    stockKnown,
    stockText,
    sold: soldKnown ? soldNumber : null,
    soldText: soldKnown ? `月售 ${soldNumber}` : '销量待同步',
    isSoldOut: stockKnown && !isUnlimited && stock <= 0,
    isOnSale: Boolean(onSale) && (!productionMode() || catalogReady),
    catalogReady,
    emoji: item.emoji || visual[0],
    color: item.color || visual[1],
    coverImageUrl,
    detailImageUrls,
    imageFileIds,
    tag: displayText(item.tag, index === 0 ? '本店招牌' : ''),
    fulfillmentType,
    businessType: businessType || (fulfillmentType === commerce.FULFILLMENT.COURIER ? 'retail' : 'restaurant'),
    skuId,
    sku: activeSku,
    skus: skuList,
    activeSkus,
    options,
    optionGroups: options
  });
}

function categorySlug(name, index) {
  const value = String(name || '').trim();
  return value ? `category_${value.replace(/\s+/g, '_')}` : `category_${index}`;
}

function normalizeCategory(item, index, defaultBusinessType) {
  item = item || {};
  const name = displayText(firstDefined(item.name, item.title, item.categoryName, item.category_name, '其他'), '其他');
  const businessType = String(firstDefined(item.businessType, item.business_type, defaultBusinessType, '') || '').toLowerCase();
  return Object.assign({}, item, {
    id: firstDefined(item.id, item.categoryId, item.category_id, categorySlug(name, index)),
    name,
    sortOrder: Number(firstDefined(item.sortOrder, item.sort_order, item.order, index)),
    channel: businessType === 'retail' ? commerce.FULFILLMENT.COURIER : businessType === 'restaurant' ? commerce.FULFILLMENT.TAKEAWAY : item.channel
  });
}

function normalizeStore(item, locations) {
  item = item || {};
  const locationList = Array.isArray(locations) ? locations : [];
  const activeLocation = locationList.find((location) => location && isActiveStatus(location.status)) || locationList[0] || {};
  const defaults = productionMode() ? {
    id: '',
    name: '',
    businessStatus: 'PAUSED',
    courierStatus: 'PAUSED',
    businessHours: '',
    phone: '',
    minGoodsAmount: 0,
    defaultDeliveryFee: 0,
    defaultShippingFee: 0,
    freeShippingThreshold: 0
  } : mock.store;
  const store = Object.assign({}, defaults, activeLocation, item);
  store.id = firstDefined(item.id, item.locationId, item.location_id, activeLocation.id, store.id, '');
  store.locationId = firstDefined(item.locationId, item.location_id, activeLocation.id, store.locationId, store.id, '');
  store.name = firstDefined(item.name, item.storeName, item.store_name, store.name, '');
  store.minGoodsAmount = priceText(firstDefined(item.defaultMinGoodsAmount, item.default_min_goods_amount, item.minGoodsAmount, item.min_goods_amount, store.minGoodsAmount));
  store.defaultDeliveryFee = priceText(firstDefined(item.defaultDeliveryFee, item.default_delivery_fee, item.deliveryFee, item.delivery_fee, store.defaultDeliveryFee));
  store.courierMinGoodsAmount = priceText(firstDefined(item.courierMinGoodsAmount, item.courier_min_goods_amount, store.courierMinGoodsAmount, 0));
  store.defaultShippingFee = priceText(firstDefined(item.defaultShippingFee, item.default_shipping_fee, item.shippingFee, item.shipping_fee, store.defaultShippingFee, 0));
  store.freeShippingThreshold = priceText(firstDefined(item.freeShippingThreshold, item.free_shipping_threshold, store.freeShippingThreshold, 0));
  const businessType = String(firstDefined(item.businessType, item.business_type, '') || '').toLowerCase();
  store.businessType = businessType || store.businessType || '';
  const bindingStatus = String(firstDefined(item.bindingStatus, item.binding_status, item.binding && item.binding.status, item.status, activeLocation.status, '') || '').toLowerCase();
  const locationStatus = String(firstDefined(item.locationStatus, item.location_status, activeLocation.status, item.status, '') || '').toLowerCase();
  const hasSingleBinding = businessType === 'restaurant' || businessType === 'retail';
  const catalogOpen = hasSingleBinding && isActiveStatus(bindingStatus) && isActiveStatus(locationStatus);
  store.bindingStatus = bindingStatus;
  store.locationStatus = locationStatus;
  store.catalogOpen = productionMode() && hasSingleBinding ? catalogOpen : undefined;
  if (productionMode() && hasSingleBinding) {
    store.courierStatus = businessType === 'retail' && catalogOpen ? 'OPEN' : 'PAUSED';
    store.businessStatus = businessType === 'restaurant' && catalogOpen ? 'OPEN' : 'PAUSED';
  } else {
    store.courierStatus = String(firstDefined(item.courierStatus, item.courier_status, store.courierStatus, 'PAUSED')).toUpperCase();
    store.businessStatus = String(firstDefined(item.businessStatus, item.business_status, store.businessStatus, 'PAUSED')).toUpperCase();
  }
  store.activeBusinessType = businessType || firstDefined(item.activeBusinessType, item.active_business_type, '');
  store.shippingCarrier = firstDefined(item.shippingCarrier, item.shipping_carrier, store.shippingCarrier, '');
  store.businessHours = Array.isArray(item.businessHours) ? item.businessHours.map((slot) => `${slot.start}-${slot.end}`).join(' / ') : firstDefined(item.businessHours, item.business_hours, store.businessHours, '');
  store.deliveryText = `满 ${store.minGoodsAmount / 100} 元起送 · 配送费 ${store.defaultDeliveryFee / 100} 元起`;
  store.shippingText = store.freeShippingThreshold > 0 ? `满 ${store.freeShippingThreshold / 100} 元包邮` : '快递费按规则计算';
  return store;
}

function normalizeCatalog(payload, businessTypeHint) {
  const root = unwrap(payload);
  const locations = Array.isArray(root.locations) ? root.locations : Array.isArray(root.fulfillment_locations) ? root.fulfillment_locations : [];
  const catalogBusinessType = String(firstDefined(root.businessType, root.business_type, root.binding && (root.binding.businessType || root.binding.business_type), root.location && (root.location.businessType || root.location.business_type), businessTypeHint, '') || '').toLowerCase();
  const rawCategories = Array.isArray(root.categories) ? root.categories : [];
  const rawProducts = Array.isArray(root.products) ? root.products : [];
  const normalizedProducts = rawProducts.map((item, index) => normalizeProduct(item, index, catalogBusinessType));
  const categoryNames = normalizedProducts.map((product) => product.categoryName).filter(Boolean).filter((name, index, values) => values.indexOf(name) === index);
  const categorySource = rawCategories.length ? rawCategories : categoryNames.map((name) => ({ name, businessType: catalogBusinessType }));
  const categories = categorySource.map((item, index) => normalizeCategory(item, index, catalogBusinessType)).sort((left, right) => left.sortOrder - right.sortOrder);
  const categoryByName = {};
  categories.forEach((category) => { categoryByName[category.name] = category.id; });
  const products = normalizedProducts.map((product) => {
    const name = firstDefined(product.categoryName, product.category_name, product.storefrontCategory, product.storefront_category);
    return Object.assign({}, product, { categoryId: categoryByName[name] || product.categoryId || 'all' });
  });
  const storeInput = Object.assign({}, root.location || {}, root.store || {});
  if (catalogBusinessType) storeInput.businessType = catalogBusinessType;
  const store = normalizeStore(Object.assign({}, storeInput, { activeBusinessType: catalogBusinessType || storeInput.activeBusinessType }), locations);
  return Object.assign({}, root, { data: root.data || root, businessType: catalogBusinessType, products, categories, locations, store });
}

function parseJSON(value) {
  if (!value || typeof value !== 'string') return value;
  try { return JSON.parse(value); } catch (error) { return value; }
}

function normalizeAvailableActions(value) {
  const actions = value || {};
  return {
    canRefund: Boolean(firstDefined(actions.canRefund, actions.can_refund, false)),
    canCancelOrder: Boolean(firstDefined(actions.canCancelOrder, actions.can_cancel_order, false)),
    canConfirmReceipt: Boolean(firstDefined(actions.canConfirmReceipt, actions.can_confirm_receipt, false)),
    reason: String(firstDefined(actions.reason, '') || '')
  };
}

function normalizeFulfillment(value) {
  const row = value || {};
  return Object.assign({}, row, {
    status: String(firstDefined(row.status, row.fulfillmentStatus, row.fulfillment_status, '') || '').toUpperCase(),
    method: String(firstDefined(row.method, row.fulfillmentMethod, row.fulfillment_method, '') || '').toUpperCase(),
    runnerName: firstDefined(row.runnerName, row.runner_name, ''),
    runnerPhone: firstDefined(row.runnerPhone, row.runner_phone, ''),
    carrier: firstDefined(row.carrierName, row.carrier_name, row.carrier, ''),
    trackingNo: firstDefined(row.trackingNo, row.tracking_no, '')
  });
}

function normalizeOrder(item) {
  item = item || {};
  const businessType = String(firstDefined(item.businessType, item.business_type, '') || '').toLowerCase();
  const fulfillmentType = businessType === 'retail' || commerce.fulfillmentOf(item) === commerce.FULFILLMENT.COURIER ? commerce.FULFILLMENT.COURIER : commerce.FULFILLMENT.TAKEAWAY;
  const restaurantFulfillment = normalizeFulfillment(firstDefined(item.restaurantFulfillment, item.restaurant_fulfillment, item.delivery));
  const retailFulfillment = normalizeFulfillment(firstDefined(item.retailFulfillment, item.retail_fulfillment, item.shipping));
  const paymentStatus = String(firstDefined(item.paymentStatus, item.payment_status, '') || '').toLowerCase();
  const fulfillmentStatus = String(firstDefined(item.fulfillmentStatus, item.fulfillment_status, fulfillmentType === commerce.FULFILLMENT.COURIER ? retailFulfillment.status : restaurantFulfillment.status, '') || '').toLowerCase();
  const refundStatus = String(firstDefined(item.refundStatus, item.refund_status, item.refund && (item.refund.status || item.refund.state), '') || '').toLowerCase();
  const availableActions = normalizeAvailableActions(firstDefined(item.availableActions, item.available_actions));
  let status = String(item.status || '').toUpperCase();
  if (['refunded', 'succeeded', 'success', 'completed'].indexOf(refundStatus) >= 0) status = 'REFUNDED';
  else if (['requested', 'processing', 'refunding', 'pending'].indexOf(refundStatus) >= 0) status = 'REFUNDING';
  else if (['failed', 'closed', 'rejected'].indexOf(refundStatus) >= 0) status = 'REFUND_FAILED';
  else if (['review', 'abnormal'].indexOf(refundStatus) >= 0) status = 'REFUND_REVIEW';
  if (!status || status === 'UNKNOWN') {
    if (refundStatus === 'refunded' || paymentStatus === 'refunded') status = 'REFUNDED';
    else if (refundStatus === 'requested' || refundStatus === 'processing') status = 'REFUNDING';
    else if (paymentStatus === 'unpaid' || paymentStatus === 'pending') status = 'WAIT_PAY';
    else if (paymentStatus !== 'paid') status = 'WAIT_PAY';
    else if (fulfillmentType === commerce.FULFILLMENT.COURIER) {
      status = ['shipped', 'in_transit'].indexOf(fulfillmentStatus) >= 0 ? 'SHIPPED' : ['delivered', 'completed'].indexOf(fulfillmentStatus) >= 0 ? 'COMPLETED' : 'PAID';
    } else {
      status = fulfillmentStatus === 'preparing' ? 'PREPARING' : ['delivering', 'ready'].indexOf(fulfillmentStatus) >= 0 ? 'DELIVERING' : fulfillmentStatus === 'completed' ? 'COMPLETED' : 'PAID';
    }
  }
  const rawItems = Array.isArray(item.items) ? item.items : Array.isArray(item.itemsSnapshot) ? item.itemsSnapshot : [];
  const itemsSnapshot = rawItems.map((line, index) => {
    const unitPrice = priceText(firstDefined(line.unitPrice, line.unit_price_cents, line.unitPriceCents, line.unit_price, 0));
    const quantity = Number(firstDefined(line.quantity, 0));
    const mediaSnapshot = snapshotMediaEntries(line);
    const snapshotCover = mediaSnapshot.find(entry => entry.kind === 'cover');
    const coverImageUrl = cleanImageURL(snapshotCover && snapshotCover.url) || cleanImageURL(firstDefined(line.coverImageUrl, line.cover_image_url, line.imageFileId, line.image_file_id, ''));
    const detailImageUrls = uniqueImageURLs(mediaSnapshot.filter(entry => entry.kind === 'detail').map(entry => entry.url)).filter(url => url !== coverImageUrl);
    return Object.assign({}, line, {
      lineId: firstDefined(line.lineId, line.id, `${firstDefined(line.productId, line.product_id, 'line')}_${index}`),
      productId: firstDefined(line.productId, line.product_id),
      name: firstDefined(line.name, line.productName, line.product_name, line.skuName, line.sku_name, ''),
      quantity,
      unitPrice,
      subtotal: priceText(firstDefined(line.subtotal, line.lineAmountCents, line.line_amount_cents, line.line_amount, unitPrice * quantity)),
      selectedOptions: parseJSON(firstDefined(line.selectedOptions, line.optionsSnapshot, line.options_snapshot, [])) || [],
      coverImageUrl,
      detailImageUrls,
      imageFileId: coverImageUrl
    });
  });
  const total = priceText(firstDefined(item.totalAmountCents, item.total_amount_cents, item.payableAmount, item.payable_amount, 0));
  const original = priceText(firstDefined(item.originalAmountCents, item.original_amount_cents, item.goodsAmount, item.goods_amount, total));
  const address = parseJSON(firstDefined(item.shippingAddress, item.shipping_address, item.addressSnapshot, item.address_snapshot));
  const addressSnapshot = address && typeof address === 'object' ? Object.assign({}, address, {
    id: firstDefined(address.id, address.addressId, address.address_id),
    addressType: commerce.addressTypeOf(address) === 'DELIVERY' && fulfillmentType === commerce.FULFILLMENT.COURIER ? 'SHIPPING' : commerce.addressTypeOf(address),
    contactName: firstDefined(address.contactName, address.contact_name, address.recipientName, address.recipient_name, item.contactName, item.contact_name, ''),
    contactPhone: firstDefined(address.contactPhone, address.contact_phone, address.phone, item.contactPhone, item.contact_phone, ''),
    detailAddress: firstDefined(address.detailAddress, address.detail_address, address.detail, '')
  }) : address;
  return Object.assign({}, item, {
    id: firstDefined(item.orderNo, item.order_no, item.id, item.orderId, item.order_id),
    recordId: firstDefined(item.id, item.orderId, item.order_id),
    orderNo: firstDefined(item.orderNo, item.order_no, item.id),
    businessType: businessType || (fulfillmentType === commerce.FULFILLMENT.COURIER ? 'retail' : 'restaurant'),
    fulfillmentType,
    deliveryMethod: String(firstDefined(item.fulfillmentMethod, item.fulfillment_method, restaurantFulfillment.method, fulfillmentType === commerce.FULFILLMENT.COURIER ? 'DELIVERY' : 'DELIVERY')).toUpperCase(),
    status,
    refundStatus: refundStatus ? refundStatus.toUpperCase() : firstDefined(item.refundStatus, item.refund_status, ''),
    paymentStatus: paymentStatus || String(firstDefined(item.paymentStatus, item.payment_status, '') || '').toLowerCase(),
    fulfillmentStatus: fulfillmentStatus || String(firstDefined(item.fulfillmentStatus, item.fulfillment_status, item.restaurantFulfillment && item.restaurantFulfillment.status, item.restaurant_fulfillment && item.restaurant_fulfillment.status, item.retailFulfillment && item.retailFulfillment.status, item.retail_fulfillment && item.retail_fulfillment.status, '') || '').toLowerCase(),
    availableActions,
    availableActionsProvided: item.availableActions !== undefined || item.available_actions !== undefined,
    refundReference: firstDefined(item.refundReference, item.refund_reference, item.outRefundNo, item.out_refund_no, item.refund && (item.refund.outRefundNo || item.refund.out_refund_no), ''),
    paymentReviewRequired: Boolean(item.paymentReviewRequired || item.payment_review_required),
    goodsAmount: original,
    discountAmount: priceText(firstDefined(item.discountCents, item.discount_cents, item.discountAmount, item.discount_amount, 0)),
    payableAmount: total,
    itemsSnapshot,
    addressSnapshot,
    delivery: fulfillmentType === commerce.FULFILLMENT.COURIER ? null : restaurantFulfillment,
    shipping: fulfillmentType === commerce.FULFILLMENT.COURIER ? retailFulfillment : null,
    createdAt: firstDefined(item.createdAt, item.created_at),
    paidAt: firstDefined(item.paidAt, item.paid_at),
    shippingFee: priceText(firstDefined(item.shippingFee, item.shipping_fee, 0)),
    deliveryFee: priceText(firstDefined(item.deliveryFee, item.delivery_fee, 0))
  });
}

function normalizePaymentParams(value) {
  const payment = value || {};
  return {
    timeStamp: firstDefined(payment.timeStamp, payment.time_stamp),
    nonceStr: firstDefined(payment.nonceStr, payment.nonce_str),
    package: payment.package,
    signType: firstDefined(payment.signType, payment.sign_type),
    paySign: firstDefined(payment.paySign, payment.pay_sign)
  };
}

function normalizePaymentView(value) {
  const root = resource(value);
  const rawStatus = firstDefined(root.status, root.paymentStatus, root.payment_status, root.orderStatus, root.order_status, 'WAIT_PAY');
  const payment = root.payment || root.payment_params || root.paymentParams;
  return Object.assign({}, root, {
    status: String(rawStatus || 'WAIT_PAY').toUpperCase(),
    attemptId: firstDefined(root.attemptId, root.attempt_id, ''),
    outTradeNo: firstDefined(root.outTradeNo, root.out_trade_no, ''),
    providerState: firstDefined(root.providerState, root.provider_state, ''),
    lastError: firstDefined(root.lastError, root.last_error, ''),
    payment: payment ? normalizePaymentParams(payment) : null
  });
}

function normalizeCart(payload) {
  const value = unwrap(payload);
  const items = (Array.isArray(value.items) ? value.items : []).map((item) => Object.assign({}, item, {
    id: firstDefined(item.id, item.itemId, item.item_id),
    productId: firstDefined(item.productId, item.product_id),
    skuId: firstDefined(item.skuId, item.sku_id),
    quantity: Number(item.quantity || 0),
    optionsSnapshot: parseJSON(firstDefined(item.optionsSnapshot, item.options_snapshot, [])) || []
  }));
  return Object.assign({}, value, { id: firstDefined(value.id, value.cartId, value.cart_id), items });
}

function productionRequest(path, options) {
  if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示模式不发送线上请求'));
  return http.request(path, options);
}

function localAddressResult() {
  return { data: storage.getAddresses().map(normalizeAddress) };
}

function normalizeAddress(item) {
  item = item || {};
  return Object.assign({}, item, {
    id: firstDefined(item.id, item.addressId, item.address_id, item._id),
    zoneId: firstDefined(item.zoneId, item.zone_id, item.deliveryZoneId, item.delivery_zone_id, ''),
    addressType: commerce.addressTypeOf(item),
    contactName: firstDefined(item.contactName, item.contact_name, item.recipientName, item.recipient_name, ''),
    contactPhone: firstDefined(item.contactPhone, item.contact_phone, item.phone, ''),
    detailAddress: firstDefined(item.detailAddress, item.detail_address, item.detail, ''),
    deliveryFee: priceText(firstDefined(item.deliveryFee, item.delivery_fee, 0)),
    shippingFee: priceText(firstDefined(item.shippingFee, item.shipping_fee, 0))
  });
}

function normalizeCoupon(item) {
  item = item || {};
  const businessType = String(firstDefined(item.businessType, item.business_type, '') || '').toLowerCase();
  const status = String(firstDefined(item.status, '') || '').toUpperCase();
  return Object.assign({}, item, {
    id: firstDefined(item.id, item.grantId, item.grant_id, item._id),
    name: displayText(firstDefined(item.name, item.title, item.templateName, item.template_name), '优惠券'),
    description: displayText(firstDefined(item.description, item.memo, ''), ''),
    businessType,
    scope: firstDefined(item.scope, businessType === 'restaurant' ? 'TAKEAWAY' : businessType === 'retail' ? 'COURIER' : 'UNIVERSAL'),
    status: status || 'AVAILABLE',
    discountAmount: priceText(firstDefined(item.discountAmount, item.discount_amount, item.discountCents, item.discount_cents, 0)),
    minGoodsAmount: priceText(firstDefined(item.minGoodsAmount, item.min_goods_amount, item.minGoodsSubtotalCents, item.min_goods_subtotal_cents, 0)),
    expireAt: firstDefined(item.expireAt, item.expire_at, item.expiresAt, item.expires_at, ''),
    grantId: firstDefined(item.id, item.grantId, item.grant_id, item._id)
  });
}

function normalizeDeliveryOptions(payload, businessType) {
  const root = unwrap(payload);
  const config = Object.assign({}, root.config || root.configuration || {}, {
    businessType: String(firstDefined(root.config && (root.config.businessType || root.config.business_type), root.config && root.config.business_type, businessType, '') || '').toLowerCase(),
    pickupEnabled: Boolean(firstDefined(root.config && (root.config.pickupEnabled || root.config.pickup_enabled), false)),
    deliveryEnabled: Boolean(firstDefined(root.config && (root.config.deliveryEnabled || root.config.delivery_enabled), false)),
    shippingEnabled: Boolean(firstDefined(root.config && (root.config.shippingEnabled || root.config.shipping_enabled), false)),
    minGoodsAmount: priceText(firstDefined(root.config && (root.config.minGoodsAmount || root.config.min_goods_amount), root.config && (root.config.minGoodsCents || root.config.min_goods_cents), 0)),
    packagingFee: priceText(firstDefined(root.config && (root.config.packagingFee || root.config.packaging_fee), root.config && (root.config.packagingFeeCents || root.config.packaging_fee_cents), 0)),
    shippingFee: priceText(firstDefined(root.config && (root.config.shippingFee || root.config.shipping_fee), root.config && (root.config.shippingFeeCents || root.config.shipping_fee_cents), 0)),
    freeShippingThreshold: priceText(firstDefined(root.config && (root.config.freeShippingThreshold || root.config.free_shipping_threshold), root.config && (root.config.freeShippingThresholdCents || root.config.free_shipping_threshold_cents), 0)),
    estimatedMinutes: Number(firstDefined(root.config && (root.config.estimatedMinutes || root.config.estimated_minutes), 0)) || 0,
    status: String(firstDefined(root.config && root.config.status, '') || '').toLowerCase()
  });
  const zones = (Array.isArray(root.zones) ? root.zones : []).map(zone => Object.assign({}, zone, {
    id: firstDefined(zone.id, zone.zoneId, zone.zone_id),
    name: displayText(firstDefined(zone.name, zone.title), '配送区域'),
    province: firstDefined(zone.province, ''),
    city: firstDefined(zone.city, ''),
    district: firstDefined(zone.district, ''),
    feeCents: priceText(firstDefined(zone.feeCents, zone.fee_cents, zone.deliveryFee, zone.delivery_fee, 0)),
    minGoodsCentsOverride: firstDefined(zone.minGoodsCentsOverride, zone.min_goods_cents_override),
    estimatedMinutes: Number(firstDefined(zone.estimatedMinutes, zone.estimated_minutes, 0)) || 0,
    status: String(firstDefined(zone.status, '') || '').toLowerCase()
  }));
  const slots = (Array.isArray(root.slots) ? root.slots : []).map(slot => Object.assign({}, slot, {
    id: firstDefined(slot.id, slot.slotId, slot.slot_id),
    zoneId: firstDefined(slot.zoneId, slot.zone_id),
    dayOfWeek: Number(firstDefined(slot.dayOfWeek, slot.day_of_week, 0)),
    startMinute: Number(firstDefined(slot.startMinute, slot.start_minute, 0)),
    endMinute: Number(firstDefined(slot.endMinute, slot.end_minute, 0)),
    orderCutoffMinutes: Number(firstDefined(slot.orderCutoffMinutes, slot.order_cutoff_minutes, 0)),
    capacity: Number(firstDefined(slot.capacity, 0)),
    status: String(firstDefined(slot.status, '') || '').toLowerCase()
  }));
  return { config, zones, slots, businessType: config.businessType || String(businessType || '').toLowerCase() };
}

function normalizeSlotDate(value) {
  if (value instanceof Date) return value.toISOString();
  const text = String(value || '').trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(text)) return `${text}T00:00:00Z`;
  return text;
}

function normalizeCheckoutQuote(payload) {
  const root = resource(payload);
  const quote = root.quote && typeof root.quote === 'object' ? root.quote : root;
  return {
    quoteToken: firstDefined(root.token, root.quoteToken, root.quote_token, quote.token, quote.quoteToken, quote.quote_token, ''),
    estimatedMinutes: Number(firstDefined(root.estimatedMinutes, root.estimated_minutes, 0)) || 0,
    quote: Object.assign({}, quote, {
      goodsAmount: priceText(firstDefined(quote.goodsAmount, quote.goods_amount, quote.goodsSubtotalCents, quote.goods_subtotal_cents, 0)),
      packagingFee: priceText(firstDefined(quote.packagingFee, quote.packaging_fee, quote.packagingFeeCents, quote.packaging_fee_cents, 0)),
      deliveryFee: priceText(firstDefined(quote.deliveryFee, quote.delivery_fee, quote.deliveryFeeCents, quote.delivery_fee_cents, 0)),
      shippingFee: priceText(firstDefined(quote.shippingFee, quote.shipping_fee, quote.shippingFeeCents, quote.shipping_fee_cents, 0)),
      discountAmount: priceText(firstDefined(quote.discountAmount, quote.discount_amount, quote.discountCents, quote.discount_cents, 0)),
      payableAmount: priceText(firstDefined(quote.payableAmount, quote.payable_amount, quote.totalCents, quote.total_cents, 0)),
      expiresAt: firstDefined(quote.expiresAt, quote.expires_at, '')
    })
  };
}

function normalizeShipmentTimeline(payload) {
  const root = resource(payload);
  const shipment = root.shipment || {};
  const events = (Array.isArray(root.events) ? root.events : Array.isArray(shipment.events) ? shipment.events : []).map((event, index) => Object.assign({}, event, {
    id: firstDefined(event.id, `${event.occurredAt || event.occurred_at || 'event'}_${index}`),
    status: String(firstDefined(event.status, '') || '').toUpperCase(),
    description: displayText(firstDefined(event.description, event.location, ''), '物流状态更新'),
    occurredAt: firstDefined(event.occurredAt, event.occurred_at, ''),
    location: firstDefined(event.location, '')
  })).sort((left, right) => String(left.occurredAt).localeCompare(String(right.occurredAt)) || String(left.id).localeCompare(String(right.id)));
  return { shipment: Object.assign({}, shipment, { status: String(firstDefined(shipment.status, '') || '').toUpperCase() }), trackingMode: firstDefined(root.trackingMode, root.tracking_mode, shipment.source, ''), events };
}

function normalizeAssistCampaign(item) {
  item = item || {};
  return Object.assign({}, item, {
    id: firstDefined(item.id, item.campaignId, item.campaign_id, item._id),
    title: displayText(firstDefined(item.title, item.name), '好友助力'),
    businessType: String(firstDefined(item.businessType, item.business_type, '') || '').toLowerCase(),
    starterReward: normalizeAssistReward(firstDefined(item.starterReward, item.starter_reward)),
    helperReward: normalizeAssistReward(firstDefined(item.helperReward, item.helper_reward)),
    discountAmount: nullableNumber(firstDefined(item.discountAmount, item.discount_amount, item.discountCents, item.discount_cents, item.starterDiscountCents, item.starter_discount_cents)),
    minGoodsAmount: nullableNumber(firstDefined(item.minGoodsAmount, item.min_goods_amount, item.minGoodsSubtotalCents, item.min_goods_subtotal_cents)),
    requiredUniqueHelpers: Number(firstDefined(item.requiredUniqueHelpers, item.required_unique_helpers, 1)) || 1,
    status: String(firstDefined(item.status, '') || '').toLowerCase()
  });
}

function normalizeAssistReward(value) {
  if (!value || typeof value !== 'object') return null;
  const discount = firstDefined(value.discountAmount, value.discount_amount, value.discountCents, value.discount_cents);
  const minimum = firstDefined(value.minGoodsAmount, value.min_goods_amount, value.minGoodsSubtotalCents, value.min_goods_subtotal_cents);
  const validDays = firstDefined(value.validDays, value.valid_days);
  return Object.assign({}, value, {
    discountAmount: nullableNumber(discount),
    minGoodsAmount: nullableNumber(minimum),
    validDays: nullableNumber(validDays),
    templateEndsAt: firstDefined(value.templateEndsAt, value.template_ends_at, null)
  });
}

function nullableNumber(value) {
  return value === undefined || value === null || value === '' || !Number.isFinite(Number(value)) ? null : Number(value);
}

function normalizeAssistSession(payload, shareToken) {
  const root = resource(payload);
  const view = root.session && typeof root.session === 'object' ? root.session : root;
  return Object.assign({}, view, {
    id: firstDefined(view.id, view.sessionId, view.session_id, view._id),
    campaignId: firstDefined(view.campaignId, view.campaign_id),
    token: firstDefined(shareToken, root.shareToken, root.share_token, view.token, view.shareToken, view.share_token, ''),
    starterReward: normalizeAssistReward(firstDefined(view.starterReward, view.starter_reward, root.starterReward, root.starter_reward)),
    helperReward: normalizeAssistReward(firstDefined(view.helperReward, view.helper_reward, root.helperReward, root.helper_reward)),
    status: String(firstDefined(view.status, '') || '').toLowerCase(),
    helperCount: Number(firstDefined(view.helperCount, view.helper_count, 0)) || 0,
    requiredUniqueHelpers: Number(firstDefined(view.requiredUniqueHelpers, view.required_unique_helpers, 1)) || 1,
    expiresAt: firstDefined(view.expiresAt, view.expires_at, ''),
    succeededAt: firstDefined(view.succeededAt, view.succeeded_at, '')
  });
}

function resource(value) {
  const root = unwrap(value);
  return root.data && typeof root.data === 'object' && !Array.isArray(root.data) ? root.data : root;
}

function cartBusinessType(payload) {
  const raw = String(firstDefined(payload && payload.businessType, payload && payload.business_type, payload && payload.fulfillmentType, '') || '').toUpperCase();
  return raw === commerce.FULFILLMENT.COURIER || raw === 'RETAIL' ? 'retail' : 'restaurant';
}

function cartFulfillmentMethod(payload, businessType) {
  const raw = String(firstDefined(payload && payload.fulfillmentMethod, payload && payload.fulfillment_method, payload && payload.deliveryMethod, '') || '').toUpperCase();
  if (businessType === 'retail') return 'delivery';
  return raw === 'PICKUP' ? 'pickup' : 'delivery';
}

function catalogLocation(businessType, payload) {
  const state = catalogForBusiness(businessType) || catalogState;
  const requested = firstDefined(payload && payload.locationId, payload && payload.location_id);
  if (requested !== undefined && requested !== null && requested !== '') {
    const requestedLocation = (state.locations || []).find((item) => String(firstDefined(item && item.id, item && item.locationId, item && item.location_id)) === String(requested));
    const requestedType = String(firstDefined(requestedLocation && requestedLocation.businessType, requestedLocation && requestedLocation.business_type, '') || '').toLowerCase();
    if (requestedLocation && requestedType && requestedType !== businessType) return undefined;
    return numericId(requested);
  }
  const location = (state.locations || []).find((item) => {
    const type = String(firstDefined(item.businessType, item.business_type, '') || '').toLowerCase();
    return item && item.status !== 'inactive' && (!type || type === businessType);
  });
  const activeType = String(firstDefined(state.businessType, state.store && (state.store.activeBusinessType || state.store.businessType), '') || '').toLowerCase();
  if (activeType && activeType !== businessType) return undefined;
  return location && firstDefined(location.id, location.locationId, location.location_id) || (activeType === businessType && state.store && state.store.locationId);
}

function expectedBusinessType(payload) {
  return businessTypeFrom(payload, true) || 'restaurant';
}

function assertActiveBusiness(payload) {
  const requestedType = assertBusinessAuthorized(expectedBusinessType(payload));
  const authorized = authorizedBusinessTypes();
  const activeType = String(firstDefined(catalogState.businessType, catalogState.store && (catalogState.store.activeBusinessType || catalogState.store.businessType), '') || '').toLowerCase();
  // Legacy single-business clients may have a valid old session without the
  // server-provided business list. Keep the old local binding guard only in
  // that compatibility case; a current multi-business session is checked by
  // the server-authorized list above and never by a stale shared store value.
  if (productionMode() && !authorized.length && activeType && activeType !== requestedType) {
    throw apiError('BUSINESS_BINDING_MISMATCH', requestedType === 'restaurant' ? '当前门店未开放餐饮服务' : '当前门店未开放零售服务');
  }
  return requestedType;
}

function productForItem(item, businessType) {
  const productId = firstDefined(item && item.productId, item && item.product_id);
  const products = storage.getProducts ? storage.getProducts(businessType) : [];
  const product = products.find((entry) => String(firstDefined(entry.id, entry.productId, entry.product_id, entry._id)) === String(productId));
  const state = catalogForBusiness(businessType) || catalogState;
  return product || (state.products || []).find((entry) => String(entry.id) === String(productId));
}

function optionIdsForItem(item, product) {
  const selected = Array.isArray(item && item.selectedOptions) ? item.selectedOptions : Array.isArray(item && item.options) ? item.options : [];
  const groups = product && (product.optionGroups || product.option_groups || product.options) || [];
  return selected.map((selectedOption) => {
    if (selectedOption && typeof selectedOption === 'object') return firstDefined(selectedOption.id, selectedOption.optionId, selectedOption.option_id);
    const label = String(selectedOption || '').replace(/\s*\+\d+(?:\.\d+)?元$/, '');
    for (const group of groups) {
      const values = group && (group.optionRecords || group.options || []);
      const found = values.find((option) => String(firstDefined(option.name, option.label, option.value, '')).trim() === label.trim());
      if (found) return firstDefined(found.id, found.optionId, found.option_id);
    }
    return undefined;
  }).filter((id) => id !== undefined && id !== null && id !== '');
}

function skuIdForItem(item, product) {
  const direct = firstDefined(item && item.skuId, item && item.sku_id, product && product.skuId, product && product.sku_id);
  if (direct !== undefined && direct !== null && direct !== '') return numericId(direct);
  const skus = product && Array.isArray(product.skus) ? product.skus : [];
  const sku = skus.find((entry) => !entry.status || entry.status === 'active') || skus[0];
  return sku && numericId(firstDefined(sku.id, sku.skuId, sku.sku_id));
}

function addressForPayload(payload) {
  if (payload && payload.address) return normalizeAddress(payload.address);
  const id = firstDefined(payload && payload.addressId, payload && payload.address_id);
  if (id !== undefined && id !== null && id !== '' && storage.getAddresses) {
    const found = storage.getAddresses().find((item) => String(firstDefined(item.id, item._id)) === String(id));
    if (found) return normalizeAddress(found);
  }
  return null;
}

function checkoutAddress(address) {
  if (!address) return '';
  if (address.addressType === 'SHIPPING') return [address.province, address.city, address.district, address.detailAddress].filter(Boolean).join(' ');
  if (address.addressType === 'CAMPUS') return [address.campusName, address.zoneName, address.building, address.room, address.detailAddress].filter(Boolean).join(' · ');
  return [address.province, address.city, address.district, address.detailAddress].filter(Boolean).join(' ');
}

function normalizeBusinessType(value) {
  const type = String(value || '').trim().toLowerCase();
  return type === 'restaurant' || type === 'retail' ? type : '';
}

function sessionBusinesses() {
  if (typeof http.getBusinesses === 'function') return http.getBusinesses();
  const session = typeof http.getSession === 'function' ? http.getSession() : null;
  return session && Array.isArray(session.businesses) ? session.businesses : [];
}

function ensureHttpSession(force) {
  return typeof http.ensureSession === 'function' ? http.ensureSession(force) : Promise.resolve(null);
}

function authorizedBusinessTypes() {
  return sessionBusinesses().map((item) => normalizeBusinessType(item && (item.businessType || item.business_type))).filter((item, index, values) => item && values.indexOf(item) === index);
}

function businessTypeFrom(value, allowFulfillmentFallback) {
  const input = typeof value === 'string' ? { businessType: value } : value || {};
  const explicit = normalizeBusinessType(firstDefined(input.businessType, input.business_type));
  if (explicit) return explicit;
  const authorized = authorizedBusinessTypes();
  if (authorized.length === 1) return authorized[0];
  if (authorized.length > 1) {
    throw apiError('BUSINESS_SELECTOR_REQUIRED', '该小程序同时开放餐饮和零售，请先选择业务类型');
  }
  if (allowFulfillmentFallback) {
    const fulfillment = String(firstDefined(input.fulfillmentType, input.fulfillment_type, input.deliveryMethod, '') || '').toUpperCase();
    if (fulfillment) return commerce.businessTypeForFulfillment(fulfillment);
  }
  return '';
}

function assertBusinessAuthorized(businessType) {
  const type = normalizeBusinessType(businessType);
  if (!type) throw apiError('BUSINESS_SELECTOR_REQUIRED', '请先选择餐饮或零售业务');
  const authorized = authorizedBusinessTypes();
  if (authorized.length && authorized.indexOf(type) < 0) {
    throw apiError('BUSINESS_NOT_AUTHORIZED', type === 'restaurant' ? '当前账号暂无餐饮业务权限' : '当前账号暂无零售业务权限');
  }
  return type;
}

function assertBusinessSelector(businessType) {
  const type = normalizeBusinessType(businessType);
  if (!type) throw apiError('BUSINESS_SELECTOR_INVALID', '业务筛选只能选择餐饮或零售');
  return type;
}

function catalogForBusiness(type) {
  return catalogStates[type] || null;
}

function combinedCatalog(catalogs) {
  const values = catalogs || [];
  const products = values.reduce((all, catalog) => all.concat(catalog.products || []), []);
  const categories = values.reduce((all, catalog) => all.concat(catalog.categories || []), []);
  const locations = values.reduce((all, catalog) => all.concat(catalog.locations || []), []);
  return {
    businesses: values.map(catalog => ({ businessType: catalog.businessType, location: catalog.store && catalog.store.locationId ? { id: catalog.store.locationId } : null })),
    catalogs: values.reduce((all, catalog) => { all[catalog.businessType] = catalog; return all; }, {}),
    products,
    categories,
    locations,
    store: values.length === 1 ? values[0].store : null,
    businessType: values.length === 1 ? values[0].businessType : ''
  };
}

module.exports = {
  isCloudEnabled: cloudEnabled,
  isProduction: productionMode,
  isConfigured: http.isConfigured,
  getSession: http.getSession,
  getAuthorizedBusinesses() { return sessionBusinesses(); },
  isBusinessAvailable(businessType) {
    const type = normalizeBusinessType(businessType);
    if (!type) return false;
    const authorized = authorizedBusinessTypes();
    return !authorized.length || authorized.indexOf(type) >= 0;
  },
  ensureSession: http.ensureSession,
  clearSession: http.clearSession,
  getCatalog(businessType) {
    if (!productionMode()) {
      const type = normalizeBusinessType(businessType);
      const products = type ? storage.getProducts(type) : storage.getProducts();
      return Promise.resolve({ categories: mock.categories, products, store: storage.getStore(type), locations: [], businessType: type });
    }
    const requested = normalizeBusinessType(businessType);
    if (requested) {
      const type = assertBusinessAuthorized(requested);
      return productionRequest('/catalog', { businessType: type }).then((result) => {
        const catalog = normalizeCatalog(result, type);
        catalogStates[type] = catalog;
        catalogState = catalog;
        storage.saveProducts(catalog.products, type);
        storage.saveStore(catalog.store, type);
        return catalog;
      });
    }
    const cachedSession = typeof http.getSession === 'function' ? http.getSession() : null;
    return ensureHttpSession().then(() => {
      const resolveCatalog = () => {
        const authorized = authorizedBusinessTypes();
        if (authorized.length > 1) return module.exports.getCatalogs(authorized);
        if (authorized.length === 1) return module.exports.getCatalog(authorized[0]);
        // Keep the selector-free path only for a server that still supports
        // the legacy single-business session contract.
        return productionRequest('/catalog').then((result) => {
          const catalog = normalizeCatalog(result);
          const type = normalizeBusinessType(catalog.businessType);
          if (type) catalogStates[type] = catalog;
          catalogState = catalog;
          storage.saveProducts(catalog.products, type);
          storage.saveStore(catalog.store, type);
          return catalog;
        }).catch((error) => {
          const refreshed = authorizedBusinessTypes();
          if (error && error.statusCode === 409 && refreshed.length > 1) return module.exports.getCatalogs(refreshed);
          throw error;
        });
      };
      const legacyCachedSession = cachedSession && (!Array.isArray(cachedSession.businesses) || !cachedSession.businesses.length);
      return legacyCachedSession ? ensureHttpSession(true).then(resolveCatalog) : resolveCatalog();
    });
  },
  getCatalogs(businessTypes) {
    if (!productionMode()) return Promise.resolve(module.exports.getCatalog());
    return ensureHttpSession().then(() => {
      const types = (Array.isArray(businessTypes) ? businessTypes : authorizedBusinessTypes()).map(normalizeBusinessType).filter((item, index, values) => item && values.indexOf(item) === index);
      if (!types.length) throw apiError('BUSINESSES_UNAVAILABLE', '当前账号没有可用业务');
      return Promise.all(types.map(type => module.exports.getCatalog(type))).then(combinedCatalog);
    });
  },
  getStorefrontContact() {
    if (!productionMode()) return Promise.resolve(normalizeStorefrontContact({}));
    return productionRequest('/contact').then(normalizeStorefrontContact);
  },
  getMemberProfile() {
    if (!productionMode()) {
      return Promise.resolve({
        member_no: '',
        status: 'active',
        membership_status: 'provisional',
        phone_masked: '',
        phone_verified: false,
        membership_consent_granted: false,
        source_count: 1
      });
    }
    return productionRequest('/member/me').then(resource);
  },
  verifyMemberPhone(code, requestId, membershipConsentGranted, membershipPolicyVersion) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示模式不发送线上请求'));
    const value = String(code || '').trim();
    if (!value) return Promise.reject(apiError('PHONE_CODE_MISSING', '没有取得有效的手机号授权凭据'));
    return productionRequest('/member/verify-phone', {
      method: 'POST',
      data: {
        code: value,
        request_id: String(requestId || ''),
        membership_consent_granted: membershipConsentGranted !== false,
        membership_policy_version: String(membershipPolicyVersion || 'member-phone-v1')
      }
    }).then(resource);
  },
  createCart(input) {
    // The storefront resolves business type, tenant, channel and location from
    // the bearer session. Do not send client-owned binding facts as authority.
    const businessType = businessTypeFrom(input, true);
    if (businessType) assertBusinessAuthorized(businessType);
    return productionRequest('/carts', { method: 'GET', businessType }).then(normalizeCart);
  },
  getCart(cartId, businessType) {
    const type = businessTypeFrom(businessType, false);
    if (type) assertBusinessAuthorized(type);
    return productionRequest(`/carts/${encodeURIComponent(cartId)}`, { businessType: type }).then(normalizeCart);
  },
  addCartItem(cartId, input) {
    const businessType = businessTypeFrom(input, true);
    if (businessType) assertBusinessAuthorized(businessType);
    const body = { product_id: numericId(firstDefined(input && input.product_id, input && input.productId)), sku_id: numericId(firstDefined(input && input.sku_id, input && input.skuId)), quantity: Number(input && input.quantity || 0), option_ids: (input && input.option_ids || input && input.optionIds || []).map(numericId) };
    return productionRequest(`/carts/${encodeURIComponent(cartId)}/items`, { method: 'POST', data: body, businessType }).then(normalizeCart);
  },
  updateCartItem(cartId, itemId, quantity, businessType) {
    const type = businessTypeFrom(businessType, false);
    if (type) assertBusinessAuthorized(type);
    return productionRequest(`/carts/${encodeURIComponent(cartId)}/items/${encodeURIComponent(itemId)}`, { method: 'PATCH', data: { quantity: Number(quantity) }, businessType: type }).then(normalizeCart);
  },
  removeCartItem(cartId, itemId, businessType) {
    const type = businessTypeFrom(businessType, false);
    if (type) assertBusinessAuthorized(type);
    return productionRequest(`/carts/${encodeURIComponent(cartId)}/items/${encodeURIComponent(itemId)}`, { method: 'DELETE', businessType: type }).then(normalizeCart);
  },
  checkoutCart(cartId, input) {
    const businessType = businessTypeFrom(input, true);
    if (businessType) assertBusinessAuthorized(businessType);
    const body = {
      idempotency_key: String(firstDefined(input && input.idempotency_key, input && input.idempotencyKey) || ''),
      contact_name: String(firstDefined(input && input.contact_name, input && input.contactName) || ''),
      contact_phone: String(firstDefined(input && input.contact_phone, input && input.contactPhone) || ''),
      address_id: numericId(firstDefined(input && input.address_id, input && input.addressId)),
      quote_token: String(firstDefined(input && input.quote_token, input && input.quoteToken) || ''),
      coupon_grant_id: numericId(firstDefined(input && input.coupon_grant_id, input && input.couponGrantId))
    };
    if (businessType !== 'retail') body.fulfillment_method = cartFulfillmentMethod(input, businessType || 'restaurant');
    if (!body.address_id) delete body.address_id;
    if (!body.coupon_grant_id) delete body.coupon_grant_id;
    if (Array.isArray(input && input.items)) body.items = input.items;
    return productionRequest(`/carts/${encodeURIComponent(cartId)}/checkout`, { method: 'POST', data: body, businessType }).then((result) => normalizeOrder(resource(result)));
  },
  createOrder(payload) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示模式请使用本地订单'));
    payload = payload || {};
    const businessType = assertActiveBusiness(payload);
    const address = addressForPayload(payload);
    const items = Array.isArray(payload.items) ? payload.items : [];
    if (!items.length) return Promise.reject(apiError('ORDER_PAYLOAD_INVALID', '购物车为空，请重新选择商品'));
    const mismatchedItem = items.find((item) => {
      const product = productForItem(item, businessType);
      return product && commerce.businessTypeOf(product) !== businessType;
    });
    if (mismatchedItem) return Promise.reject(apiError('FULFILLMENT_MISMATCH', '购物车包含当前业务不可售的商品'));
    let cartId;
    return module.exports.createCart({ businessType }).then((cart) => {
      cartId = cart.id;
      if (!cartId) throw apiError('CART_ID_MISSING', '购物车创建失败，请稍后重试');
      const existingItems = Array.isArray(cart.items) ? cart.items.slice() : [];
      return existingItems.reduce((promise, item) => promise.then(() => {
        const itemId = firstDefined(item && item.id, item && item.itemId, item && item.item_id);
        if (!itemId) throw apiError('CART_ITEM_ID_MISSING', '购物车状态已变化，请稍后重试');
        return module.exports.removeCartItem(cartId, itemId, businessType);
      }), Promise.resolve());
    }).then(() => {
      return items.reduce((promise, item) => promise.then(() => {
        const product = productForItem(item, businessType);
        const skuId = skuIdForItem(item, product);
        const productId = firstDefined(item.productId, item.product_id, product && product.id);
        if (!productId || !skuId) throw apiError('PRODUCT_UNAVAILABLE', '商品规格已变化，请重新选择');
        return module.exports.addCartItem(cartId, { businessType, product_id: productId, sku_id: skuId, quantity: item.quantity, option_ids: optionIdsForItem(item, product) });
      }), Promise.resolve());
    }).then(() => {
      const contactName = firstDefined(payload.contactName, payload.contact_name, address && address.contactName, '');
      const contactPhone = firstDefined(payload.contactPhone, payload.contact_phone, address && address.contactPhone, '');
      const quoteInput = {
        businessType,
        fulfillment_method: businessType === 'retail' ? 'shipping' : cartFulfillmentMethod(payload, businessType),
        address_id: address && address.id,
        zone_id: numericId(firstDefined(payload.zoneId, payload.zone_id)),
        slot_id: numericId(firstDefined(payload.slotId, payload.slot_id)),
        slot_date: firstDefined(payload.slotDate, payload.slot_date)
      };
      return module.exports.createCheckoutQuote(quoteInput, businessType).then((quote) => {
        if (!quote || !quote.quoteToken) throw apiError('QUOTE_TOKEN_MISSING', '结算报价无效，请重新确认订单');
        const checkoutInput = {
        idempotency_key: firstDefined(payload.idempotencyKey, payload.idempotency_key, payload.clientRequestId, `checkout_${Date.now()}`),
        contact_name: contactName,
        contact_phone: contactPhone,
        address_id: address && address.id,
        quote_token: quote.quoteToken,
        coupon_grant_id: numericId(firstDefined(payload.couponGrantId, payload.coupon_grant_id, payload.couponId)),
        businessType
        };
      if (businessType === 'restaurant') checkoutInput.fulfillment_method = cartFulfillmentMethod(payload, businessType);
        return module.exports.checkoutCart(cartId, checkoutInput);
      });
    }).then((order) => {
      const normalized = normalizeOrder(order);
      const orderId = firstDefined(normalized.orderNo, normalized.id);
      if (!orderId) throw apiError('ORDER_ID_MISSING', '订单创建结果无效，请稍后查询订单');
      return { success: true, orderId, order: normalized };
    });
  },
  getOrders(options) {
    if (!productionMode()) return Promise.resolve({ data: storage.getOrders().map(normalizeOrder) });
    const rawBusinessType = typeof options === 'string' ? options : options && (options.businessType !== undefined ? options.businessType : options.business_type);
    const hasSelector = rawBusinessType !== undefined && rawBusinessType !== null && String(rawBusinessType).trim() !== '';
    const businessType = hasSelector ? assertBusinessSelector(rawBusinessType) : '';
    return productionRequest('/orders', { businessType }).then((result) => {
      const root = unwrap(result);
      const rows = Array.isArray(root) ? root : Array.isArray(root.data) ? root.data : Array.isArray(root.orders) ? root.orders : [];
      return Object.assign({}, root, { data: rows.map(normalizeOrder) });
    });
  },
  getOrder(orderId) {
    if (!productionMode()) {
      const found = storage.getOrders().find((item) => String(item.id) === String(orderId));
      return Promise.resolve({ data: found ? normalizeOrder(found) : null });
    }
    return productionRequest(`/orders/${encodeURIComponent(orderId)}`).then((result) => ({ data: normalizeOrder(resource(result)) }));
  },
  getDeliveryOptions(businessType) {
    const type = assertBusinessSelector(businessType);
    if (!productionMode()) {
      const store = normalizeStore(storage.getStore(type));
      return Promise.resolve(normalizeDeliveryOptions({ config: { business_type: type, pickup_enabled: type === 'restaurant', delivery_enabled: type === 'restaurant', shipping_enabled: type === 'retail', min_goods_cents: store.minGoodsAmount, shipping_fee_cents: store.defaultShippingFee, free_shipping_threshold_cents: store.freeShippingThreshold, status: 'active' }, zones: [], slots: [] }, type));
    }
    return productionRequest('/delivery-options', { businessType: type }).then((result) => normalizeDeliveryOptions(result, type));
  },
  createCheckoutQuote(input, businessType) {
    input = input || {};
    const type = assertBusinessSelector(businessType || input.businessType || input.business_type);
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示模式不需要线上报价'));
    const method = String(firstDefined(input.fulfillmentMethod, input.fulfillment_method, type === 'retail' ? 'shipping' : 'pickup')).toLowerCase();
    const body = {
      fulfillment_method: method,
      address_id: numericId(firstDefined(input.addressId, input.address_id)),
      zone_id: numericId(firstDefined(input.zoneId, input.zone_id)),
      slot_id: numericId(firstDefined(input.slotId, input.slot_id)),
      slot_date: normalizeSlotDate(firstDefined(input.slotDate, input.slot_date))
    };
    Object.keys(body).forEach(key => { if (body[key] === undefined || body[key] === null || body[key] === '') delete body[key]; });
    return productionRequest('/checkout-quotes', { method: 'POST', data: body, businessType: type }).then(normalizeCheckoutQuote);
  },
  createPayment(orderNo, options) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示订单无需调用微信支付'));
    if (!orderNo) return Promise.reject(apiError('ORDER_ID_MISSING', '订单号无效，请重新打开订单'));
    options = options || {};
    const body = {
      order_no: String(orderNo),
      client_request_id: String(firstDefined(options.clientRequestId, options.client_request_id, `payment_${orderNo}`))
    };
    if (options.loginCode) body.login_code = String(options.loginCode);
    return productionRequest(`/orders/${encodeURIComponent(orderNo)}/payments`, { method: 'POST', data: body }).then(normalizePaymentView);
  },
  queryPayment(orderNo) {
    if (!productionMode()) return Promise.resolve({ status: 'WAIT_PAY', payment: null });
    if (!orderNo) return Promise.reject(apiError('ORDER_ID_MISSING', '订单号无效，请重新打开订单'));
    return productionRequest(`/orders/${encodeURIComponent(orderNo)}/payment`, { method: 'GET' }).then(normalizePaymentView);
  },
  createRefund(orderId, options) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示订单无需申请退款'));
    options = options || {};
    const attemptKey = options.newAttempt ? firstDefined(options.previousRefundNo, options.previous_refund_no, 'retry') : 'initial';
    const key = firstDefined(options.idempotency_key, options.idempotencyKey, options.clientRequestId, options.client_request_id, `refund_${orderId}_${attemptKey}`);
    const reason = String(firstDefined(options.reason, '客户申请退款'));
    return productionRequest(`/orders/${encodeURIComponent(orderId)}/refund-requests`, { method: 'POST', data: { idempotency_key: key, reason } }).then(resource);
  },
  queryRefund(orderId) {
    if (!productionMode()) return Promise.resolve({ status: 'REFUND_STATUS_UNAVAILABLE', unavailable: true });
    return module.exports.getOrder(orderId).then((result) => {
      const order = result && result.data ? result.data : null;
      return Object.assign({}, order || {}, { status: order && order.status || 'REFUND_STATUS_UNAVAILABLE', refundStatus: order && order.refundStatus || '' });
    });
  },
  cancelOrder(orderNo) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示订单请在本地操作'));
    if (!orderNo) return Promise.reject(apiError('ORDER_ID_MISSING', '订单号无效，请重新打开订单'));
    return productionRequest(`/orders/${encodeURIComponent(orderNo)}/cancel`, { method: 'POST' }).then((result) => normalizeOrder(resource(result)));
  },
  confirmReceipt(orderNo) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示订单请在本地操作'));
    if (!orderNo) return Promise.reject(apiError('ORDER_ID_MISSING', '订单号无效，请重新打开订单'));
    return productionRequest(`/orders/${encodeURIComponent(orderNo)}/confirm-receipt`, { method: 'POST' }).then((result) => normalizeOrder(resource(result)));
  },
  getShipments(orderNo) {
    if (!productionMode()) return Promise.resolve({ shipment: null, trackingMode: '', events: [] });
    if (!orderNo) return Promise.reject(apiError('ORDER_ID_MISSING', '订单号无效，请重新打开订单'));
    return productionRequest(`/orders/${encodeURIComponent(orderNo)}/shipments`, { method: 'GET' }).then(normalizeShipmentTimeline);
  },
  getAddresses() {
    if (!productionMode()) return Promise.resolve(localAddressResult());
    return productionRequest('/addresses').then((result) => {
      const root = unwrap(result);
      const rows = Array.isArray(root) ? root : Array.isArray(root.data) ? root.data : Array.isArray(root.addresses) ? root.addresses : [];
      return { data: rows.map(normalizeAddress) };
    });
  },
  getDeliveryZones() { return productionMode() ? Promise.reject(apiError('DELIVERY_ZONES_UNAVAILABLE', '配送区域由服务端校验，暂不提供区域列表接口')) : Promise.resolve({ data: [] }); },
  saveAddress(address) {
    if (productionMode()) {
      const value = normalizeAddress(address || {});
      const payload = {
        address_type: value.addressType,
        recipient_name: value.contactName,
        phone: value.contactPhone,
        province: value.province || '',
        city: value.city || '',
        district: value.district || '',
        campus_name: value.campusName || '',
        zone_name: value.zoneName || '',
        building: value.building || '',
        room: value.room || '',
        detail: value.detailAddress || '',
        is_default: Boolean(value.isDefault)
      };
      const id = firstDefined(value.id, value.addressId, value.address_id);
      const path = id ? `/addresses/${encodeURIComponent(id)}` : '/addresses';
      return productionRequest(path, { method: id ? 'PUT' : 'POST', data: payload }).then((result) => ({ data: normalizeAddress(resource(result)) }));
    }
    const value = normalizeAddress(Object.assign({}, address, { id: firstDefined(address && address.id, storage.makeId('address')) }));
    const addresses = storage.getAddresses().filter((item) => String(item.id) !== String(value.id));
    if (value.isDefault || !addresses.some((item) => commerce.addressTypeOf(item) === value.addressType)) addresses.forEach((item) => { if (commerce.addressTypeOf(item) === value.addressType) item.isDefault = false; });
    addresses.push(value);
    storage.saveAddresses(addresses);
    return Promise.resolve({ data: value });
  },
  deleteAddress(addressId) {
    if (productionMode()) return productionRequest(`/addresses/${encodeURIComponent(addressId)}`, { method: 'DELETE' });
    storage.saveAddresses(storage.getAddresses().filter((item) => String(item.id) !== String(addressId)));
    return Promise.resolve({});
  },
  setDefaultAddress(addressId) {
    if (productionMode()) return productionRequest(`/addresses/${encodeURIComponent(addressId)}/default`, { method: 'POST' });
    const addresses = storage.getAddresses();
    const selected = addresses.find((item) => String(item.id) === String(addressId));
    if (selected) addresses.forEach((item) => { if (commerce.addressTypeOf(item) === commerce.addressTypeOf(selected)) item.isDefault = String(item.id) === String(addressId); });
    storage.saveAddresses(addresses);
    return Promise.resolve({});
  },
  getCoupons(businessType) {
    if (!productionMode()) return Promise.resolve({ data: storage.getCoupons().map(normalizeCoupon) });
    const type = assertBusinessSelector(businessType || (authorizedBusinessTypes().length === 1 ? authorizedBusinessTypes()[0] : ''));
    return productionRequest('/coupons', { businessType: type }).then((result) => {
      const root = unwrap(result);
      const rows = Array.isArray(root) ? root : Array.isArray(root.data) ? root.data : Array.isArray(root.coupons) ? root.coupons : [];
      return Object.assign({}, root, { data: rows.map(normalizeCoupon) });
    });
  },
  getMerchantOrders() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getMerchantProducts() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  changeOrderStatus() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  updateProduct() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  createProduct() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getMerchantSettings() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  saveDeliveryZone() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getMerchantCampaign() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  saveCampaign() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getOperationLogs() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getMerchantStats() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getCouponTemplates() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  saveCouponTemplate() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  updateStoreSettings() { return Promise.reject(apiError('MERCHANT_API_UNAVAILABLE', '商家工作台接口暂未接入')); },
  getAssistCampaigns(businessType) {
    if (!productionMode()) return Promise.resolve({ data: mock.campaign ? [normalizeAssistCampaign(Object.assign({}, mock.campaign, { businessType: 'restaurant', status: 'active' }))] : [] });
    const type = assertBusinessSelector(businessType || (authorizedBusinessTypes().length === 1 ? authorizedBusinessTypes()[0] : ''));
    return productionRequest('/assist-campaigns', { businessType: type }).then((result) => {
      const root = unwrap(result);
      const rows = Array.isArray(root) ? root : Array.isArray(root.data) ? root.data : Array.isArray(root.campaigns) ? root.campaigns : [];
      return Object.assign({}, root, { data: rows.map(normalizeAssistCampaign) });
    });
  },
  createAssistSession(campaignId, idempotencyKey, businessType) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示模式请使用本地助力'));
    const type = assertBusinessSelector(businessType || (authorizedBusinessTypes().length === 1 ? authorizedBusinessTypes()[0] : ''));
    if (!campaignId) return Promise.reject(apiError('ASSIST_CAMPAIGN_MISSING', '助力活动不存在'));
    const key = String(idempotencyKey || `assist_${campaignId}_${Date.now()}`);
    return productionRequest('/assist-sessions', { method: 'POST', businessType: type, data: { campaign_id: numericId(campaignId), idempotency_key: key } }).then((result) => {
      const root = resource(result);
      return Object.assign({}, root, { data: normalizeAssistSession(root, firstDefined(root.shareToken, root.share_token)) });
    });
  },
  getAssistSession(token, businessType) {
    if (!productionMode()) return Promise.resolve({ data: normalizeAssistSession(storage.getAssist(), token) });
    const type = assertBusinessSelector(businessType || (authorizedBusinessTypes().length === 1 ? authorizedBusinessTypes()[0] : ''));
    const rawToken = String(token || '').trim();
    if (!rawToken) return Promise.reject(apiError('ASSIST_TOKEN_MISSING', '助力链接无效'));
    return productionRequest(`/assist-sessions/${encodeURIComponent(rawToken)}`, { businessType: type }).then((result) => ({ data: normalizeAssistSession(result) }));
  },
  helpAssist(token, businessType) {
    if (!productionMode()) return Promise.reject(apiError('DEMO_ONLY', '演示模式请使用本地助力'));
    const type = assertBusinessSelector(businessType || (authorizedBusinessTypes().length === 1 ? authorizedBusinessTypes()[0] : ''));
    const rawToken = String(token || '').trim();
    if (!rawToken) return Promise.reject(apiError('ASSIST_TOKEN_MISSING', '助力链接无效'));
    return productionRequest(`/assist-sessions/${encodeURIComponent(rawToken)}/help`, { method: 'POST', businessType: type }).then((result) => {
      const root = resource(result);
      return Object.assign({}, root, { data: normalizeAssistSession(root, rawToken) });
    });
  },
  assist(action, options) {
    options = options || {};
    const type = options.businessType || options.business_type;
    if (action === 'getCampaign') return module.exports.getAssistCampaigns(type).then(result => ({ data: (result.data || [])[0] || null }));
    if (action === 'createSession') return module.exports.createAssistSession(options.campaignId || options.campaign_id, options.idempotencyKey || options.idempotency_key, type).then(result => ({ data: result.data }));
    if (action === 'getSession') return module.exports.getAssistSession(options.token || options.shareToken || options.share_token || options.sessionId, type);
    if (action === 'helpAssist') return module.exports.helpAssist(options.token || options.shareToken || options.share_token || options.sessionId, type);
    return Promise.reject(apiError('ASSIST_ACTION_INVALID', '助力请求无效'));
  },
  normalizeProduct,
  resolveCoverImage,
  normalizeCatalog,
  normalizeStore,
  normalizeOrder,
  normalizePaymentView,
  normalizeCart,
  normalizeAddress,
  normalizeCoupon,
  normalizeDeliveryOptions,
  normalizeCheckoutQuote,
  normalizeShipmentTimeline,
  normalizeAssistReward,
  normalizeAssistCampaign,
  normalizeAssistSession,
  normalizeStorefrontContact,
  call(functionName, data, fallback) {
    if (!productionMode()) return Promise.resolve(typeof fallback === 'function' ? fallback() : fallback);
    return Promise.reject(apiError('API_UNAVAILABLE', `接口 ${functionName || ''} 暂未接入`));
  },
  callRequired(functionName) { return Promise.reject(apiError('API_UNAVAILABLE', `接口 ${functionName || ''} 暂未接入`)); }
};
