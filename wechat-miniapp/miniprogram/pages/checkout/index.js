const brand = require('../../config/brand');
const mock = require('../../data/mock');
const storage = require('../../services/storage');
const format = require('../../utils/format');
const api = require('../../services/api');
const paymentFlow = require('../../services/payment');
const commerce = require('../../config/commerce');

function orderNumber() {
  const date = new Date();
  const pad = value => value < 10 ? `0${value}` : value;
  return `${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}${Math.floor(Math.random() * 90 + 10)}`;
}

function addressLabel(address) {
  if (!address) return '';
  if (address.addressType === 'SHIPPING') return `${address.province} ${address.city} ${address.district} ${address.detailAddress}`;
  if (address.addressType === 'CAMPUS') return [address.campusName, address.zoneName, address.building, address.room, address.detailAddress].filter(Boolean).join(' · ');
  return [address.province, address.city, address.district, address.detailAddress].filter(Boolean).join(' ');
}

function slotLabel(slot) {
  if (!slot) return '';
  const minuteLabel = minute => `${String(Math.floor(Number(minute || 0) / 60)).padStart(2, '0')}:${String(Number(minute || 0) % 60).padStart(2, '0')}`;
  return `${minuteLabel(slot.startMinute)} - ${minuteLabel(slot.endMinute)}`;
}

function itemOptions(item) {
  return (item.selectedOptions || []).map(option => typeof option === 'string' ? option : option.label || option.name).join(' · ');
}

function coverImageOf(item, product) {
  if (typeof api.resolveCoverImage === 'function') return api.resolveCoverImage(item, product);
  return item && (item.coverImageUrl || item.imageFileId) || product && product.coverImageUrl || '';
}

function fingerprintValue(value) {
  if (Array.isArray(value)) return value.map(fingerprintValue);
  if (!value || typeof value !== 'object') return value === undefined ? null : value;
  return Object.keys(value).sort().reduce((result, key) => {
    result[key] = fingerprintValue(value[key]);
    return result;
  }, {});
}

function fingerprintAddress(address) {
  if (!address) return null;
  return {
    id: address.id || '',
    addressType: address.addressType || address.type || '',
    zoneId: address.zoneId || address.zone_id || '',
    contactName: address.contactName || address.contact_name || '',
    contactPhone: address.contactPhone || address.contact_phone || '',
    province: address.province || '',
    city: address.city || '',
    district: address.district || '',
    detailAddress: address.detailAddress || address.detail_address || address.detail || '',
    campusName: address.campusName || '',
    zoneName: address.zoneName || '',
    building: address.building || '',
    room: address.room || ''
  };
}

function checkoutFingerprint(groups) {
  const normalized = groups.map(group => ({
    businessType: group.key,
    fulfillmentType: group.fulfillmentType,
    deliveryMethod: group.deliveryMethod || '',
    address: fingerprintAddress(group.address),
    couponId: group.couponId || '',
    remark: group.remark || '',
    items: group.items.map(item => ({
      productId: item.productId || item.product_id || '',
      skuId: item.skuId || item.sku_id || '',
      quantity: Number(item.quantity || 0),
      unitPrice: Number(item.unitPrice || 0),
      selectedOptions: (item.selectedOptions || item.options || []).map(fingerprintValue).sort((left, right) => {
        const leftText = JSON.stringify(left);
        const rightText = JSON.stringify(right);
        return leftText < rightText ? -1 : leftText > rightText ? 1 : 0;
      }),
      remark: item.remark || ''
    })).sort((left, right) => {
      const leftText = JSON.stringify(left);
      const rightText = JSON.stringify(right);
      return leftText < rightText ? -1 : leftText > rightText ? 1 : 0;
    })
  })).sort((left, right) => left.businessType < right.businessType ? -1 : left.businessType > right.businessType ? 1 : 0);
  return JSON.stringify(fingerprintValue(normalized));
}

Page({
  data: {
    brand,
    store: { businessStatus: 'PAUSED', courierStatus: 'PAUSED', defaultShippingFee: 0, freeShippingThreshold: 0 },
    businessStores: { restaurant: null, retail: null },
    deliveryOptions: { restaurant: null, retail: null },
    restaurantZones: [],
    restaurantZoneNames: [],
    restaurantSlots: [],
    restaurantSlotNames: [],
    restaurantZoneIndex: -1,
    restaurantSlotIndex: -1,
    restaurantSlotDate: '',
    restaurantSlotDateMin: '',
    restaurantSlotDateMax: '',
    production: false,
    takeawayItems: [],
    courierItems: [],
    takeawayAddress: null,
    courierAddress: null,
    takeawayDeliveryMethod: 'DELIVERY',
    coupons: [],
    selectedCoupon: null,
    couponTarget: 'TAKEAWAY',
    hasTakeaway: false,
    hasCourier: false,
    cartCount: 0,
    takeawayGoodsAmount: 0,
    courierGoodsAmount: 0,
    takeawayDeliveryFee: 0,
    courierShippingFee: 0,
    discount: 0,
    payable: 0,
    takeawayGoodsText: '0.00',
    courierGoodsText: '0.00',
    takeawayFeeText: '0.00',
    courierFeeText: '0.00',
    discountText: '0.00',
    payableText: '0.00',
    customerRemark: '',
    courierRemark: '',
    showCouponSheet: false,
    couponsUnavailable: false,
    paying: false,
    paymentModeText: '当前为演示支付模式',
    pricingNote: '费用按不同履约分别核算'
  },

  onLoad() {
    this.batchId = storage.makeId('checkout_batch');
    const store = api.normalizeStore(storage.getStore());
    const activeBusinessType = String(store.activeBusinessType || store.businessType || '').toLowerCase();
    const bindingText = activeBusinessType === 'restaurant' ? '当前门店为餐饮业务绑定，金额由云端核算' : activeBusinessType === 'retail' ? '当前门店为零售业务绑定，金额由云端核算' : '金额由云端核算';
    this.setData({ store, production: api.isProduction(), takeawayDeliveryMethod: 'DELIVERY', paymentModeText: api.isProduction() ? bindingText : '当前为演示支付模式，提交只写入本地演示数据', pricingNote: api.isProduction() ? '配送费与优惠以服务端报价为准' : '费用按不同履约分别核算' });
  },

  onShow() {
    if (this.data.paying) return;
    this.loadData();
  },

  loadData() {
    if (api.isProduction()) {
      const types = [];
      storage.getCart().forEach(item => { const type = commerce.businessTypeOf(item); if (types.indexOf(type) < 0) types.push(type); });
      const couponRequests = types.map(type => api.getCoupons(type).then(result => ({ type, result })).catch(error => ({ type, error, result: { data: [] } })));
      const optionRequests = types.map(type => api.getDeliveryOptions(type).then(result => ({ type, result })).catch(error => ({ type, error, result: { config: {}, zones: [], slots: [] } })));
      Promise.all([api.getAddresses(), Promise.all(couponRequests), Promise.all(optionRequests)]).then(([addressResult, couponResults, optionResults]) => {
        const addresses = (addressResult.data || []).filter(address => address.deliverable !== false);
        const coupons = couponResults.reduce((all, entry) => all.concat((entry.result && entry.result.data || []).map(coupon => Object.assign({}, coupon, { businessType: coupon.businessType || entry.type }))), []);
        const deliveryOptions = optionResults.reduce((all, entry) => { all[entry.type] = entry.result; return all; }, { restaurant: null, retail: null });
        storage.saveAddresses(addresses);
        storage.saveCoupons(coupons);
        this.setData({ couponsUnavailable: couponResults.length > 0 && couponResults.every(entry => entry.error), deliveryOptions });
        this.applyData(addresses, coupons, deliveryOptions);
      }).catch(error => { console.error('load checkout data failed', error); wx.showToast({ title: '线上信息加载失败', icon: 'none' }); });
      return;
    }
    this.applyData(storage.getAddresses(), storage.getCoupons(), { restaurant: null, retail: null });
  },

  applyData(addresses, couponList, deliveryOptions) {
    const store = api.normalizeStore(storage.getStore());
    const businessStores = {
      restaurant: api.normalizeStore(storage.getStore('restaurant')),
      retail: api.normalizeStore(storage.getStore('retail'))
    };
    const products = storage.getProducts();
    const cart = storage.getCart().map(item => {
      const businessType = commerce.businessTypeOf(item);
      const scopedProducts = storage.getProducts(businessType);
      const product = scopedProducts.find(entry => String(entry.id || entry._id) === String(item.productId)) || products.find(entry => String(entry.id || entry._id) === String(item.productId));
      const fulfillmentType = item.fulfillmentType || commerce.fulfillmentOf(product);
      return Object.assign({}, item, { businessType, fulfillmentType, coverImageUrl: coverImageOf(item, product), optionsText: itemOptions(item), subtotalText: format.yuan(Number(item.unitPrice || 0) * Number(item.quantity || 0)) });
    });
    const takeawayItems = cart.filter(item => item.fulfillmentType === commerce.FULFILLMENT.TAKEAWAY);
    const courierItems = cart.filter(item => item.fulfillmentType === commerce.FULFILLMENT.COURIER);
    const selectedTakeawayId = wx.getStorageSync('checkout_takeaway_address_id') || wx.getStorageSync('checkout_address_id');
    const selectedCourierId = wx.getStorageSync('checkout_courier_address_id');
    const displayAddresses = addresses.map(address => Object.assign({}, address, { displayText: addressLabel(address) }));
    const delivery = displayAddresses.filter(address => (address.addressType || 'DELIVERY') === 'DELIVERY' || address.addressType === 'CAMPUS');
    const shipping = displayAddresses.filter(address => address.addressType === 'SHIPPING');
    const takeawayAddress = delivery.find(address => address.id === selectedTakeawayId) || delivery.find(address => address.isDefault) || delivery[0] || null;
    const courierAddress = shipping.find(address => address.id === selectedCourierId) || shipping.find(address => address.isDefault) || shipping[0] || null;
    const takeawayGoodsAmount = takeawayItems.reduce((sum, item) => sum + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0);
    const courierGoodsAmount = courierItems.reduce((sum, item) => sum + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0);
    const coupons = couponList.filter(coupon => String(coupon.status || '').toUpperCase() === 'AVAILABLE' && ((coupon.scope || 'UNIVERSAL') === 'UNIVERSAL' || (coupon.scope || '') === (takeawayItems.length && !courierItems.length ? 'TAKEAWAY' : courierItems.length && !takeawayItems.length ? 'COURIER' : (coupon.scope || 'UNIVERSAL'))));
    let selectedCoupon = this.couponChoice !== undefined ? coupons.find(item => item.id === this.couponChoice) || null : this.data.selectedCoupon && coupons.find(item => item.id === this.data.selectedCoupon.id) || null;
    let couponTarget = this.data.couponTarget || (takeawayItems.length ? commerce.FULFILLMENT.TAKEAWAY : commerce.FULFILLMENT.COURIER);
    if (!takeawayItems.length) couponTarget = commerce.FULFILLMENT.COURIER;
    if (!courierItems.length) couponTarget = commerce.FULFILLMENT.TAKEAWAY;
    if (!selectedCoupon || !this.isCouponForTarget(selectedCoupon, couponTarget)) selectedCoupon = coupons.find(coupon => this.isCouponForTarget(coupon, couponTarget) && coupon.minGoodsAmount <= (couponTarget === commerce.FULFILLMENT.COURIER ? courierGoodsAmount : takeawayGoodsAmount)) || null;
    const restaurantOptions = deliveryOptions && deliveryOptions.restaurant || null;
    const restaurantZones = restaurantOptions && restaurantOptions.zones || [];
    const restaurantConfig = restaurantOptions && restaurantOptions.config || {};
    let takeawayDeliveryMethod = this.data.takeawayDeliveryMethod;
    if (api.isProduction() && restaurantOptions) {
      if (takeawayDeliveryMethod === 'DELIVERY' && restaurantConfig.deliveryEnabled === false && restaurantConfig.pickupEnabled !== false) takeawayDeliveryMethod = 'PICKUP';
      if (takeawayDeliveryMethod === 'PICKUP' && restaurantConfig.pickupEnabled === false && restaurantConfig.deliveryEnabled !== false) takeawayDeliveryMethod = 'DELIVERY';
    }
    const matchingZone = takeawayAddress && restaurantZones.find(zone => zone.province === takeawayAddress.province && zone.city === takeawayAddress.city && zone.district === takeawayAddress.district) || restaurantZones[0];
    const restaurantZoneIndex = matchingZone ? Math.max(0, restaurantZones.findIndex(zone => String(zone.id) === String(matchingZone.id))) : -1;
    const restaurantSlots = restaurantOptions && restaurantOptions.slots ? restaurantOptions.slots.filter(slot => !matchingZone || !slot.zoneId || String(slot.zoneId) === String(matchingZone.id)) : [];
    const restaurantSlotIndex = restaurantSlots.length ? 0 : -1;
    const restaurantSlotDate = restaurantSlots.length ? this.nextSlotDate(restaurantSlots[restaurantSlotIndex]) : '';
    const restaurantZoneNames = restaurantZones.map(zone => zone.name || '配送区域');
    const restaurantSlotNames = restaurantSlots.map(slotLabel);
    const restaurantSlotDateMin = restaurantSlotDate || new Date().toISOString().slice(0, 10);
    const maxDate = new Date();
    maxDate.setDate(maxDate.getDate() + 14);
    const restaurantSlotDateMax = maxDate.toISOString().slice(0, 10);
    this.setData({ store, businessStores, deliveryOptions: deliveryOptions || this.data.deliveryOptions, restaurantZones, restaurantZoneNames, restaurantSlots, restaurantSlotNames, restaurantZoneIndex, restaurantSlotIndex, restaurantSlotDate, restaurantSlotDateMin, restaurantSlotDateMax, takeawayDeliveryMethod, takeawayItems, courierItems, takeawayAddress, courierAddress, coupons, selectedCoupon, couponTarget, hasTakeaway: takeawayItems.length > 0, hasCourier: courierItems.length > 0, cartCount: cart.length ? cart.reduce((sum, item) => sum + Number(item.quantity || 0), 0) : 0, takeawayGoodsAmount, courierGoodsAmount }, () => this.calculate());
  },

  nextSlotDate(slot) {
    if (!slot || slot.dayOfWeek === undefined || slot.dayOfWeek === null) return '';
    const date = new Date();
    const delta = (Number(slot.dayOfWeek) - date.getDay() + 7) % 7;
    date.setDate(date.getDate() + delta);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
  },

  isCouponForTarget(coupon, target) {
    const scope = coupon && coupon.scope || 'UNIVERSAL';
    const businessType = coupon && String(coupon.businessType || coupon.business_type || '').toLowerCase();
    const targetBusiness = target === commerce.FULFILLMENT.COURIER ? 'retail' : 'restaurant';
    return scope === 'UNIVERSAL' || scope === target || !businessType || businessType === targetBusiness;
  },

  calculate() {
    const restaurantStore = this.data.businessStores && this.data.businessStores.restaurant || this.data.store;
    const retailStore = this.data.businessStores && this.data.businessStores.retail || this.data.store;
    const restaurantOptions = this.data.deliveryOptions && this.data.deliveryOptions.restaurant || {};
    const retailOptions = this.data.deliveryOptions && this.data.deliveryOptions.retail || {};
    const selectedZone = this.data.restaurantZones && this.data.restaurantZones[this.data.restaurantZoneIndex];
    const takeawayFee = this.data.hasTakeaway && this.data.takeawayDeliveryMethod === 'DELIVERY' && this.data.takeawayAddress ? Number(selectedZone && selectedZone.feeCents || this.data.takeawayAddress.deliveryFee || restaurantStore.defaultDeliveryFee || 0) : 0;
    const threshold = Number(retailStore.freeShippingThreshold || 0);
    const baseShippingFee = Number(retailOptions && retailOptions.config && retailOptions.config.shippingFee || retailStore.defaultShippingFee || 0);
    const courierFee = this.data.hasCourier ? (threshold > 0 && this.data.courierGoodsAmount >= threshold ? 0 : baseShippingFee) : 0;
    let discount = 0;
    if (this.data.selectedCoupon && this.isCouponForTarget(this.data.selectedCoupon, this.data.couponTarget)) {
      const goodsAmount = this.data.couponTarget === commerce.FULFILLMENT.COURIER ? this.data.courierGoodsAmount : this.data.takeawayGoodsAmount;
      if (goodsAmount >= Number(this.data.selectedCoupon.minGoodsAmount || 0)) {
        const fee = this.data.couponTarget === commerce.FULFILLMENT.COURIER ? courierFee : takeawayFee;
        const base = this.data.selectedCoupon.excludeDeliveryFee === false ? goodsAmount + fee : goodsAmount;
        discount = Math.min(Number(this.data.selectedCoupon.discountAmount || 0), base);
      }
    }
    const payable = Math.max(0, this.data.takeawayGoodsAmount + this.data.courierGoodsAmount + takeawayFee + courierFee - discount);
    this.setData({ takeawayDeliveryFee: takeawayFee, courierShippingFee: courierFee, discount, payable, takeawayGoodsText: format.yuan(this.data.takeawayGoodsAmount), courierGoodsText: format.yuan(this.data.courierGoodsAmount), takeawayFeeText: format.yuan(takeawayFee), courierFeeText: format.yuan(courierFee), discountText: format.yuan(discount), payableText: format.yuan(payable) });
  },

  chooseAddress(event) {
    const type = event.currentTarget.dataset.type;
    wx.navigateTo({ url: `/pages/address/list/index?from=checkout&type=${type}` });
  },

  toggleDeliveryMethod(event) {
    this.setData({ takeawayDeliveryMethod: event.currentTarget.dataset.method }, () => this.calculate());
  },

  selectDeliveryZone(event) {
    const zoneIndex = Number(event.detail.value);
    const zone = this.data.restaurantZones[zoneIndex];
    if (!zone) return;
    const options = this.data.deliveryOptions && this.data.deliveryOptions.restaurant;
    const restaurantSlots = options && options.slots ? options.slots.filter(slot => !slot.zoneId || String(slot.zoneId) === String(zone.id)) : [];
    const restaurantSlotDate = restaurantSlots.length ? this.nextSlotDate(restaurantSlots[0]) : '';
    this.setData({ restaurantZoneIndex: zoneIndex, restaurantSlots, restaurantSlotNames: restaurantSlots.map(slotLabel), restaurantSlotIndex: restaurantSlots.length ? 0 : -1, restaurantSlotDate });
  },

  selectDeliverySlot(event) {
    const slotIndex = Number(event.detail.value);
    const slot = this.data.restaurantSlots[slotIndex];
    if (!slot) return;
    this.setData({ restaurantSlotIndex: slotIndex, restaurantSlotDate: this.nextSlotDate(slot) });
  },

  selectSlotDate(event) { this.setData({ restaurantSlotDate: event.detail.value }); },

  onRemarkInput(event) { this.setData({ [event.currentTarget.dataset.field]: event.detail.value }); },
  openCouponSheet() { if (this.data.couponsUnavailable) { wx.showToast({ title: '优惠券功能暂未开放', icon: 'none' }); return; } this.setData({ showCouponSheet: true }); },
  closeCouponSheet() { this.setData({ showCouponSheet: false }); },
  stopPropagation() {},
  chooseCouponTarget(event) { this.setData({ couponTarget: event.currentTarget.dataset.target }, () => this.calculate()); },

  chooseCoupon(event) {
    this.couponChoice = event.currentTarget.dataset.id || '';
    const selectedCoupon = this.data.coupons.find(item => item.id === event.currentTarget.dataset.id) || null;
    this.setData({ selectedCoupon, showCouponSheet: false }, () => this.calculate());
  },

  validateBeforeSubmit() {
    if (!this.data.store) return '店铺信息加载中';
    if (this.data.hasTakeaway && this.data.takeawayDeliveryMethod === 'DELIVERY' && (!this.data.takeawayAddress || this.data.takeawayAddress.addressType === 'SHIPPING' || (this.data.takeawayAddress.addressType !== 'CAMPUS' && (!this.data.takeawayAddress.province || !this.data.takeawayAddress.city || !this.data.takeawayAddress.district || !this.data.takeawayAddress.detailAddress)))) return '请先选择完整配送地址';
    if (this.data.hasCourier && (!this.data.courierAddress || this.data.courierAddress.addressType !== 'SHIPPING')) return '请先选择普通快递地址';
    const restaurantStore = this.data.businessStores && this.data.businessStores.restaurant || this.data.store;
    const retailStore = this.data.businessStores && this.data.businessStores.retail || this.data.store;
    if (this.data.hasTakeaway && !api.isProduction() && this.data.takeawayGoodsAmount < Number(this.data.takeawayAddress && this.data.takeawayAddress.minGoodsAmount || restaurantStore.minGoodsAmount || 0)) return `餐饮满${format.yuan(this.data.takeawayAddress && this.data.takeawayAddress.minGoodsAmount || restaurantStore.minGoodsAmount)}元起送`;
    if (this.data.hasCourier && !api.isProduction() && this.data.courierGoodsAmount < Number(retailStore.courierMinGoodsAmount || 0)) return `零售满${format.yuan(retailStore.courierMinGoodsAmount)}元起购`;
    if (!this.data.takeawayItems.length && !this.data.courierItems.length) return '购物车为空';
    if (api.isProduction()) {
      if (this.data.hasTakeaway && this.data.takeawayDeliveryMethod === 'DELIVERY' && (!this.data.restaurantZones.length || this.data.restaurantZoneIndex < 0 || this.data.restaurantSlotIndex < 0 || !this.data.restaurantSlotDate)) return '请选择配送区域和时间';
      const requested = [];
      if (this.data.hasTakeaway) requested.push('restaurant');
      if (this.data.hasCourier) requested.push('retail');
      if (requested.some(type => !api.isBusinessAvailable(type))) return '当前账号未授权购物车中的业务，请重新登录后重试';
    }
    return '';
  },

  groupPayload(type, items, address, remark, couponId) {
    const isCourier = type === commerce.FULFILLMENT.COURIER;
    const businessType = commerce.businessTypeForFulfillment(type);
    return { clientRequestId: `${this.batchId}_${businessType}`, orderGroupId: this.batchId, businessType, fulfillmentType: type, deliveryMethod: isCourier ? 'DELIVERY' : this.data.takeawayDeliveryMethod, addressId: address ? address.id : '', couponId: couponId || '', zoneId: !isCourier && this.data.takeawayDeliveryMethod === 'DELIVERY' && this.data.restaurantZones[this.data.restaurantZoneIndex] ? this.data.restaurantZones[this.data.restaurantZoneIndex].id : '', slotId: !isCourier && this.data.takeawayDeliveryMethod === 'DELIVERY' && this.data.restaurantSlots[this.data.restaurantSlotIndex] ? this.data.restaurantSlots[this.data.restaurantSlotIndex].id : '', slotDate: !isCourier && this.data.takeawayDeliveryMethod === 'DELIVERY' ? this.data.restaurantSlotDate : '', customerRemark: remark || '', items: items.map(item => ({ productId: item.productId, skuId: item.skuId || '', quantity: item.quantity, selectedOptions: item.selectedOptions || [], remark: item.remark || '' })) };
  },

  submitOrder() {
    if (this.data.paying) return;
    const validation = this.validateBeforeSubmit();
    if (validation) { wx.showToast({ title: validation, icon: 'none' }); return; }
    if (api.isProduction()) this.submitProductionOrder();
    else this.submitDemoOrder();
  },

  buildGroups() {
    const groups = [];
    if (this.data.takeawayItems.length) groups.push({ key: 'restaurant', fulfillmentType: commerce.FULFILLMENT.TAKEAWAY, deliveryMethod: this.data.takeawayDeliveryMethod, items: this.data.takeawayItems, address: this.data.takeawayDeliveryMethod === 'PICKUP' ? null : this.data.takeawayAddress, remark: this.data.customerRemark, couponId: this.data.couponTarget === commerce.FULFILLMENT.TAKEAWAY && this.data.selectedCoupon ? this.data.selectedCoupon.id : '' });
    if (this.data.courierItems.length) groups.push({ key: 'retail', fulfillmentType: commerce.FULFILLMENT.COURIER, deliveryMethod: 'DELIVERY', items: this.data.courierItems, address: this.data.courierAddress, remark: this.data.courierRemark, couponId: this.data.couponTarget === commerce.FULFILLMENT.COURIER && this.data.selectedCoupon ? this.data.selectedCoupon.id : '' });
    return groups;
  },

  openCreatedOrder(orderId) { if (orderId) wx.redirectTo({ url: `/pages/order/detail/index?id=${orderId}` }); },

  async submitProductionOrder() {
    this.setData({ paying: true });
    const groups = this.buildGroups();
    const fingerprint = checkoutFingerprint(groups);
    let attempt = storage.getCheckoutBatchAttempt();
    const attemptMatches = Boolean(attempt && attempt.fingerprint === fingerprint && Array.isArray(attempt.attempts) && attempt.attempts.length === groups.length);
    const existingOrder = attempt && Array.isArray(attempt.attempts) && attempt.attempts.find(entry => entry && entry.orderId);
    if (!attemptMatches && existingOrder) {
      this.setData({ paying: false });
      wx.showToast({ title: '已有待处理订单，请先查看', icon: 'none' });
      this.openCreatedOrder(existingOrder.orderId);
      return;
    }
    if (!attemptMatches) {
      attempt = { batchId: this.batchId, fingerprint, attempts: groups.map(group => ({ key: group.key, businessType: group.key, payload: this.groupPayload(group.fulfillmentType, group.items, group.address, group.remark, group.couponId), orderId: '', status: 'PENDING' })) };
    }
    attempt.attempts.forEach((entry, index) => {
      const businessType = entry.businessType || (entry.key === commerce.FULFILLMENT.COURIER ? 'retail' : entry.key === commerce.FULFILLMENT.TAKEAWAY ? 'restaurant' : groups[index] && groups[index].key);
      entry.businessType = businessType;
      if (entry.payload) entry.payload.businessType = businessType;
    });
    storage.saveCheckoutBatchAttempt(attempt);
    let firstOrderId = '';
    try {
      for (const entry of attempt.attempts) {
        if (!entry.orderId) {
          const result = await api.createOrder(entry.payload);
          if (!result || !result.success || !result.orderId) throw new Error('ORDER_ID_MISSING');
          entry.orderId = result.orderId;
          entry.status = result.order && result.order.status || 'WAIT_PAY';
          storage.saveCheckoutBatchAttempt(attempt);
          if (!firstOrderId) firstOrderId = entry.orderId;
        }
        const payment = await paymentFlow.payOrder(entry.orderId);
        entry.status = payment.status;
        storage.saveCheckoutBatchAttempt(attempt);
        if (!paymentFlow.isSettled(payment.status)) { firstOrderId = entry.orderId; break; }
      }
      const complete = attempt.attempts.every(entry => paymentFlow.isSettled(entry.status));
      if (complete) { storage.saveCart([]); storage.saveCheckoutBatchAttempt(null); wx.showToast({ title: `已完成${attempt.attempts.length}个关联订单`, icon: 'success' }); }
      else wx.showToast({ title: '首个订单待核实，请在订单详情继续', icon: 'none' });
    } catch (error) {
      console.error('production order payment failed', error);
      const code = String(error && (error.errMsg || error.message) || '');
      const rejected = error && error.statusCode === 409 || ['STORE_NOT_OPEN', 'ADDRESS_NOT_FOUND', 'ADDRESS_TYPE_INVALID', 'ADDRESS_OUT_OF_RANGE', 'PRODUCT_UNAVAILABLE', 'FULFILLMENT_MISMATCH', 'PRODUCT_OPTIONS_INVALID', 'PRODUCT_OPTIONS_REQUIRED', 'STOCK_NOT_ENOUGH', 'MIN_AMOUNT_NOT_REACHED', 'COUPON_UNAVAILABLE', 'COUPON_EXPIRED', 'ORDER_AMOUNT_INVALID', 'ORDER_PAYLOAD_INVALID', 'BUSINESS_SELECTOR_REQUIRED', 'BUSINESS_NOT_AUTHORIZED', 'BUSINESS_BINDING_MISMATCH'].some(item => code.indexOf(item) >= 0);
      if (rejected && !attempt.attempts.some(entry => entry.orderId)) { storage.saveCheckoutBatchAttempt(null); this.batchId = storage.makeId('checkout_batch'); }
      wx.showToast({ title: rejected ? (error && error.statusCode === 409 && error.userMessage || '商品、地址、业务选择或优惠已变化，请重新确认') : '提交未确认，请在订单中核实', icon: 'none' });
    } finally {
      this.setData({ paying: false });
      const pending = storage.getCheckoutBatchAttempt();
      const orderId = firstOrderId || pending && pending.attempts.find(entry => entry.orderId && !paymentFlow.isSettled(entry.status)) && pending.attempts.find(entry => entry.orderId && !paymentFlow.isSettled(entry.status)).orderId;
      if (orderId) this.openCreatedOrder(orderId);
    }
  },

  submitDemoOrder() {
    this.setData({ paying: true });
    const now = new Date().toISOString();
    const products = storage.getProducts();
    const groups = this.buildGroups();
    const created = groups.map(group => {
      const isCourier = group.fulfillmentType === commerce.FULFILLMENT.COURIER;
      const goodsAmount = group.items.reduce((sum, item) => sum + Number(item.unitPrice || 0) * Number(item.quantity || 0), 0);
      const fee = isCourier ? (Number(this.data.courierShippingFee || 0)) : Number(this.data.takeawayDeliveryFee || 0);
      const discount = group.couponId ? Number(this.data.discount || 0) : 0;
      const addressSnapshot = isCourier ? Object.assign({}, group.address, { addressType: 'SHIPPING' }) : (group.address ? Object.assign({}, group.address, { addressType: group.address.addressType || 'DELIVERY' }) : { addressType: 'PICKUP', pickupName: '门店自提' });
      const order = { id: storage.makeId('order'), orderNo: orderNumber(), storeId: 'store_001', userId: 'demo_user_001', orderGroupId: this.batchId, businessType: group.key, fulfillmentType: group.fulfillmentType, deliveryMethod: isCourier ? 'DELIVERY' : this.data.takeawayDeliveryMethod, itemsSnapshot: group.items.map(item => ({ productId: item.productId, name: item.name, emoji: item.emoji, color: item.color, coverImageUrl: item.coverImageUrl || '', imageFileId: item.coverImageUrl || '', quantity: item.quantity, unitPrice: item.unitPrice, subtotal: Number(item.unitPrice || 0) * item.quantity, selectedOptions: item.selectedOptions || [], remark: item.remark || '' })), goodsAmount, deliveryFee: isCourier ? 0 : fee, shippingFee: isCourier ? fee : 0, discountAmount: discount, payableAmount: Math.max(0, goodsAmount + fee - discount), addressSnapshot, couponSnapshot: group.couponId ? { id: group.couponId, name: this.data.selectedCoupon.name, discountAmount: discount, scope: this.data.selectedCoupon.scope || 'UNIVERSAL' } : null, customerRemark: group.remark || '', status: 'PAID', delivery: isCourier ? null : { status: this.data.takeawayDeliveryMethod === 'PICKUP' ? 'PICKUP' : 'NOT_CALLED', runnerName: '', runnerPhone: '', actualFee: fee, internalRemark: '' }, shipping: isCourier ? { status: 'WAIT_SHIP', carrier: '', trackingNo: '', shippingRemark: '' } : null, payment: { outTradeNo: `demo_${orderNumber()}`, transactionId: `demo_tx_${Date.now()}`, paidAmount: Math.max(0, goodsAmount + fee - discount) }, createdAt: now, paidAt: now, updatedAt: now };
      group.items.forEach(item => { const product = products.find(entry => entry.id === item.productId); if (product && product.stockMode !== 'UNLIMITED') { product.stock = Math.max(0, Number(product.stock || 0) - item.quantity); product.isSoldOut = product.stock === 0; } });
      return order;
    });
    const orders = storage.getOrders();
    created.slice().reverse().forEach(order => orders.unshift(order));
    storage.saveOrders(orders);
    storage.saveProducts(products);
    const couponOrder = created.find(order => order.couponSnapshot && order.couponSnapshot.id === (this.data.selectedCoupon && this.data.selectedCoupon.id));
    if (this.data.selectedCoupon && couponOrder) { const coupons = storage.getCoupons().map(coupon => coupon.id === this.data.selectedCoupon.id ? Object.assign({}, coupon, { status: 'USED', usedOrderId: couponOrder.id, usedAt: now }) : coupon); storage.saveCoupons(coupons); }
    storage.saveCart([]);
    setTimeout(() => { this.setData({ paying: false }); wx.showToast({ title: `已生成${created.length}个关联订单`, icon: 'success' }); this.openCreatedOrder(created[0] && created[0].id); }, 500);
  }
});
