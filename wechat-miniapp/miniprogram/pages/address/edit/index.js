const mock = require('../../../data/mock');
const storage = require('../../../services/storage');
const api = require('../../../services/api');

const addressTypes = [{ id: 'SHIPPING', name: '收货地址' }];

Page({
  data: { addressId: '', production: false, addressTypes, typeIndex: 0, zones: [], zoneIndex: -1, regionValue: [], regionText: '', saving: false, form: { addressType: 'SHIPPING', campusName: '', zoneName: '', zoneId: '', building: '', room: '', province: '', city: '', district: '', detailAddress: '', contactName: '', contactPhone: '', remark: '', deliveryFee: 0, isDefault: false } },

  onLoad(options) {
    const addressId = options.id || '';
    const requestedType = 'SHIPPING';
    this.setData({ addressId, production: api.isProduction() });
    const apply = (addresses, zones) => {
      const existing = addresses.find(item => String(item.id) === String(addressId));
      const legacyCampus = existing && existing.addressType === 'CAMPUS';
      const type = legacyCampus ? 'CAMPUS' : requestedType;
      const form = Object.assign({}, existing || mock.defaultShippingAddress, { id: existing ? existing.id : '', addressType: type, isDefault: existing ? Boolean(existing.isDefault) : false });
      const types = addressTypes;
      const typeIndex = 0;
      const regionValue = form.province && form.city && form.district ? [form.province, form.city, form.district] : [];
      this.setData({ form, addressTypes: types, typeIndex: typeIndex < 0 ? 0 : typeIndex, zones, regionValue, regionText: regionValue.join(' / '), zoneIndex: (type === 'DELIVERY' || type === 'CAMPUS') && form.zoneId ? zones.findIndex(zone => zone.id === form.zoneId) : -1 });
    };
    if (api.isProduction()) {
      api.getAddresses().then((result) => {
        const addresses = result.data || [];
        storage.saveAddresses(addresses);
        apply(addresses, []);
      }).catch(error => { console.error('load address failed', error); wx.showToast({ title: '地址加载失败', icon: 'none' }); });
      return;
    }
    apply(storage.getAddresses(), []);
  },

  onInput(event) { this.setData({ [`form.${event.currentTarget.dataset.field}`]: event.detail.value }); },
  selectRegion(event) {
    const value = event.detail.value || [];
    this.setData({ regionValue: value, regionText: value.join(' / '), 'form.province': value[0] || '', 'form.city': value[1] || '', 'form.district': value[2] || '' });
  },
  selectType(event) {
    const typeIndex = Number(event.detail.value);
    const addressType = addressTypes[typeIndex] ? addressTypes[typeIndex].id : 'SHIPPING';
    this.setData({ typeIndex, 'form.addressType': addressType, zoneIndex: -1 });
  },
  selectZone(event) {
    const zoneIndex = Number(event.detail.value);
    const zone = this.data.zones[zoneIndex];
    if (!zone) return;
    this.setData({ zoneIndex, 'form.zoneId': zone.id, 'form.campusName': zone.campusName, 'form.zoneName': zone.zoneName, 'form.deliveryFee': zone.deliveryFee });
  },

  validForm(form) {
    if (!form.contactName || !form.contactPhone || !/^1\d{10}$/.test(form.contactPhone)) return false;
    if (form.addressType === 'SHIPPING' || form.addressType === 'DELIVERY') return Boolean(form.province && form.city && form.district && form.detailAddress);
    const hasUnifiedAddress = Boolean(form.province && form.city && form.district && form.detailAddress);
    return hasUnifiedAddress || Boolean(form.campusName && form.zoneName && form.building && form.room);
  },

  saveAddress() {
    if (this.data.saving) return;
    const selectedType = 'SHIPPING';
    const hasUnifiedAddress = Boolean(this.data.form.province && this.data.form.city && this.data.form.district && this.data.form.detailAddress);
    const preserveLegacyType = this.data.form.addressType === 'CAMPUS' && Boolean(this.data.addressId) && !hasUnifiedAddress;
    const form = Object.assign({}, this.data.form, { addressType: preserveLegacyType ? 'CAMPUS' : selectedType });
    if (!this.validForm(form)) { wx.showToast({ title: '请填写完整收货地址和手机号', icon: 'none' }); return; }
    if (api.isProduction()) {
      if (form.addressType === 'CAMPUS' && (!form.campusName || !form.zoneName)) { wx.showToast({ title: '请补充配送区域信息', icon: 'none' }); return; }
      this.setData({ saving: true });
      api.saveAddress(Object.assign({}, form, { id: this.data.addressId })).then(result => {
        const saved = result.data;
        const addresses = storage.getAddresses().filter(item => String(item.id) !== String(saved.id));
        addresses.push(saved);
        storage.saveAddresses(addresses);
        wx.showToast({ title: '保存成功', icon: 'success' });
        setTimeout(() => wx.navigateBack(), 450);
      }).catch(error => { console.error('save address failed', error); wx.showToast({ title: '地址保存失败', icon: 'none' }); }).finally(() => this.setData({ saving: false }));
      return;
    }
    const addresses = storage.getAddresses();
    const address = Object.assign({}, form, { id: this.data.addressId || storage.makeId('address'), deliveryFee: form.addressType === 'CAMPUS' ? Number(form.deliveryFee || mock.store.defaultDeliveryFee) : 0 });
    const index = addresses.findIndex(item => item.id === address.id);
    if (index > -1) addresses[index] = address; else addresses.push(address);
    if (address.isDefault || addresses.length === 1) addresses.forEach(item => { item.isDefault = item.id === address.id; });
    storage.saveAddresses(addresses);
    wx.showToast({ title: '保存成功', icon: 'success' });
    setTimeout(() => wx.navigateBack(), 450);
  }
});
