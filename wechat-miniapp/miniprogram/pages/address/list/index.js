const storage = require('../../../services/storage');
const api = require('../../../services/api');

function addressText(address) {
  if (!address) return '';
  if (address.addressType === 'CAMPUS') return [address.campusName, address.zoneName, address.building, address.room, address.detailAddress].filter(Boolean).join(' · ');
  return [address.province, address.city, address.district, address.detailAddress].filter(Boolean).join(' ');
}

function matchesType(address, type) {
  const value = address && (address.addressType || 'DELIVERY');
  return type === 'DELIVERY' ? value === 'DELIVERY' || value === 'CAMPUS' : value === type;
}

Page({
  data: { addresses: [], from: '', type: '', title: '收货地址' },

  onLoad(options) {
    const type = options.type === 'SHIPPING' ? 'SHIPPING' : options.type === 'DELIVERY' ? 'DELIVERY' : options.type === 'CAMPUS' ? 'CAMPUS' : '';
    this.setData({ from: options.from || '', type, title: type === 'SHIPPING' ? '快递地址' : type === 'DELIVERY' || type === 'CAMPUS' ? '配送地址' : '收货地址' });
  },

  onShow() {
    const apply = addresses => {
      const decorated = addresses.map(item => Object.assign({}, item, { displayText: addressText(item) }));
      const filtered = this.data.type ? decorated.filter(item => matchesType(item, this.data.type)) : decorated;
      this.setData({ addresses: filtered });
    };
    if (api.isProduction()) {
      api.getAddresses().then(result => { const addresses = result.data || []; storage.saveAddresses(addresses); apply(addresses); }).catch(error => { console.error('load addresses failed', error); wx.showToast({ title: '地址加载失败', icon: 'none' }); });
      return;
    }
    apply(storage.getAddresses());
  },

  selectAddress(event) {
    const id = event.currentTarget.dataset.id;
    const address = this.data.addresses.find(item => item.id === id);
    if (this.data.from === 'checkout') {
      wx.setStorageSync(this.data.type === 'SHIPPING' ? 'checkout_courier_address_id' : 'checkout_takeaway_address_id', id);
      wx.setStorageSync('checkout_address_id', id);
      wx.navigateBack();
      return;
    }
    if (!address) return;
    if (api.isProduction()) {
      api.setDefaultAddress(id).then(() => { this.reloadDefault(id); wx.showToast({ title: '已设为默认地址', icon: 'success' }); }).catch(error => { console.error('set default address failed', error); wx.showToast({ title: '操作失败', icon: 'none' }); });
      return;
    }
    this.reloadDefault(id);
    wx.showToast({ title: '已设为默认地址', icon: 'success' });
  },

  reloadDefault(id) {
    const all = storage.getAddresses();
    const selected = all.find(item => item.id === id);
    const type = selected && (selected.addressType || 'DELIVERY');
    const addresses = all.map(item => (item.addressType || 'DELIVERY') === type ? Object.assign({}, item, { isDefault: item.id === id }) : item);
    storage.saveAddresses(addresses);
    const decorated = addresses.map(item => Object.assign({}, item, { displayText: addressText(item) }));
    this.setData({ addresses: this.data.type ? decorated.filter(item => matchesType(item, this.data.type)) : decorated });
  },

  addAddress() { wx.navigateTo({ url: `/pages/address/edit/index${this.data.type ? `?type=${this.data.type}` : ''}` }); },
  editAddress(event) { wx.navigateTo({ url: `/pages/address/edit/index?id=${event.currentTarget.dataset.id}` }); },
  deleteAddress(event) {
    wx.showModal({ title: '删除地址', content: '确定删除这个收货地址吗？', success: result => {
      if (!result.confirm) return;
      const id = event.currentTarget.dataset.id;
      const done = () => { const addresses = storage.getAddresses().filter(item => item.id !== id); storage.saveAddresses(addresses); const decorated = addresses.map(item => Object.assign({}, item, { displayText: addressText(item) })); this.setData({ addresses: this.data.type ? decorated.filter(item => matchesType(item, this.data.type)) : decorated }); };
      if (api.isProduction()) api.deleteAddress(id).then(done).catch(error => { console.error('delete address failed', error); wx.showToast({ title: '删除失败', icon: 'none' }); });
      else done();
    } });
  }
});
