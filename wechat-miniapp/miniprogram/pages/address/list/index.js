const storage = require('../../../services/storage');
const api = require('../../../services/api');
const format = require('../../../utils/format');

function addressText(address) {
  if (!address) return '';
  if (address.addressType === 'CAMPUS') return [address.campusName, address.zoneName, address.building, address.room, address.detailAddress].filter(Boolean).join(' · ');
  return [address.province, address.city, address.district, address.detailAddress].filter(Boolean).join(' ');
}

function matchesType(address, type) {
  // Delivery and shipping now share one address book. Keep the type query
  // only for backwards-compatible routes; it must never hide a saved address.
  return Boolean(address) && (type === 'DELIVERY' || type === 'SHIPPING' || type === 'CAMPUS' || !type);
}

function addressZoneParts(address) {
  const value = address || {};
  return {
    province: value.province || value.campusName || '',
    city: value.city || value.campusName || '',
    district: value.district || value.zoneName || ''
  };
}

function zoneMatchesAddress(zone, address) {
  const parts = addressZoneParts(address);
  return zone && String(zone.province || '').trim().toLowerCase() === String(parts.province).trim().toLowerCase() && String(zone.city || '').trim().toLowerCase() === String(parts.city).trim().toLowerCase() && String(zone.district || '').trim().toLowerCase() === String(parts.district).trim().toLowerCase();
}

Page({
  data: { addresses: [], from: '', type: '', businessType: '', title: '收货地址' },

  decorateAddresses(addresses, deliveryOptions) {
    const zones = (deliveryOptions && deliveryOptions.zones || []).filter(zone => !zone.status || ['active', 'open', 'enabled'].indexOf(String(zone.status).toLowerCase()) >= 0);
    return addresses.map(item => {
      const matchedZone = zones.find(zone => zoneMatchesAddress(zone, item));
      const deliveryAvailable = this.data.businessType === 'restaurant'
        ? (!api.isProduction() || Boolean(matchedZone))
        : this.data.businessType === 'retail'
          ? item.addressType !== 'CAMPUS'
          : true;
      return Object.assign({}, item, {
        displayText: addressText(item),
        deliveryAvailable,
        deliveryZoneName: matchedZone && matchedZone.name || '',
        deliveryFeeText: matchedZone ? format.yuan(matchedZone.feeCents) : ''
      });
    });
  },

  applyAddresses(addresses, deliveryOptions) {
    this.deliveryOptions = deliveryOptions || null;
    const decorated = this.decorateAddresses(addresses, deliveryOptions);
    const filtered = this.data.type ? decorated.filter(item => matchesType(item, this.data.type)) : decorated;
    this.setData({ addresses: filtered });
  },

  onLoad(options) {
    const type = options.type === 'SHIPPING' ? 'SHIPPING' : options.type === 'DELIVERY' ? 'DELIVERY' : options.type === 'CAMPUS' ? 'CAMPUS' : '';
    const businessType = options.business === 'restaurant' || options.business === 'retail' ? options.business : '';
    this.setData({ from: options.from || '', type, businessType, title: '收货地址' });
  },

  onShow() {
    if (api.isProduction()) {
      const optionsRequest = this.data.businessType === 'restaurant' ? api.getDeliveryOptions('restaurant').catch(error => { console.error('load delivery options failed', error); return { zones: [] }; }) : Promise.resolve(null);
      Promise.all([api.getAddresses(), optionsRequest]).then(([result, deliveryOptions]) => { const addresses = result.data || []; storage.saveAddresses(addresses); this.applyAddresses(addresses, deliveryOptions); }).catch(error => { console.error('load addresses failed', error); wx.showToast({ title: '地址加载失败', icon: 'none' }); });
      return;
    }
    const mockZones = this.data.businessType === 'restaurant' ? { zones: [] } : null;
    this.applyAddresses(storage.getAddresses(), mockZones);
  },

  selectAddress(event) {
    const id = event.currentTarget.dataset.id;
    const address = this.data.addresses.find(item => String(item.id) === String(id));
    if (address && address.deliveryAvailable === false) {
      wx.showToast({ title: this.data.businessType === 'retail' ? '请先补充完整地址' : '该地址不在配送范围内', icon: 'none' });
      return;
    }
    if (!address) return;
    if (this.data.from === 'checkout') {
      const selectionKey = this.data.businessType === 'retail' || this.data.type === 'SHIPPING'
        ? 'checkout_courier_address_id'
        : 'checkout_takeaway_address_id';
      wx.setStorageSync(selectionKey, id);
      wx.setStorageSync('checkout_address_id', id);
      wx.navigateBack();
      return;
    }
    if (api.isProduction()) {
      api.setDefaultAddress(id).then(() => { this.reloadDefault(id); wx.showToast({ title: '已设为默认地址', icon: 'success' }); }).catch(error => { console.error('set default address failed', error); wx.showToast({ title: '操作失败', icon: 'none' }); });
      return;
    }
    this.reloadDefault(id);
    wx.showToast({ title: '已设为默认地址', icon: 'success' });
  },

  reloadDefault(id) {
    const all = storage.getAddresses();
    const addresses = all.map(item => Object.assign({}, item, { isDefault: String(item.id) === String(id) }));
    storage.saveAddresses(addresses);
    this.applyAddresses(addresses, this.deliveryOptions);
  },

  addAddress() { wx.navigateTo({ url: `/pages/address/edit/index${this.data.type ? `?type=${this.data.type}` : ''}` }); },
  editAddress(event) { wx.navigateTo({ url: `/pages/address/edit/index?id=${event.currentTarget.dataset.id}` }); },
  deleteAddress(event) {
    wx.showModal({ title: '删除地址', content: '确定删除这个收货地址吗？', success: result => {
      if (!result.confirm) return;
      const id = event.currentTarget.dataset.id;
      const done = () => {
        const addresses = storage.getAddresses().filter(item => String(item.id) !== String(id));
        if (addresses.length && !addresses.some(item => item.isDefault)) addresses[0].isDefault = true;
        storage.saveAddresses(addresses);
        this.applyAddresses(addresses, this.deliveryOptions);
      };
      if (api.isProduction()) api.deleteAddress(id).then(done).catch(error => { console.error('delete address failed', error); wx.showToast({ title: '删除失败', icon: 'none' }); });
      else done();
    } });
  }
});
