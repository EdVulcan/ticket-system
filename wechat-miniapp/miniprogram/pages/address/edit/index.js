const mock = require('../../../data/mock');
const storage = require('../../../services/storage');
const api = require('../../../services/api');

const addressTypes = [{ id: 'DELIVERY', name: '配送地址' }, { id: 'SHIPPING', name: '快递地址' }];

Page({
  data: { addressId: '', production: false, addressTypes, typeIndex: 0, zones: [], zoneIndex: -1, saving: false, form: { addressType: 'DELIVERY', campusName: '', zoneName: '', zoneId: '', building: '', room: '', province: '', city: '', district: '', detailAddress: '', contactName: '', contactPhone: '', remark: '', deliveryFee: 0, isDefault: false } },

  onLoad(options) {
    const addressId = options.id || '';
    const requestedType = options.type === 'SHIPPING' ? 'SHIPPING' : 'DELIVERY';
    this.setData({ addressId, production: api.isProduction() });
    const apply = (addresses, zones) => {
      const existing = addresses.find(item => item.id === addressId);
      const type = existing && existing.addressType || requestedType;
      const form = existing || Object.assign({}, type === 'SHIPPING' ? mock.defaultShippingAddress : mock.defaultAddress, { id: '', addressType: type, isDefault: false });
      const types = type === 'CAMPUS' ? addressTypes.concat([{ id: 'CAMPUS', name: '历史配送地址' }]) : addressTypes;
      const typeIndex = types.findIndex(item => item.id === type);
      this.setData({ form, addressTypes: types, typeIndex: typeIndex < 0 ? 0 : typeIndex, zones, zoneIndex: (type === 'DELIVERY' || type === 'CAMPUS') && form.zoneId ? zones.findIndex(zone => zone.id === form.zoneId) : -1 });
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
  selectType(event) {
    const typeIndex = Number(event.detail.value);
    const addressType = addressTypes[typeIndex] ? addressTypes[typeIndex].id : 'DELIVERY';
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
    return Boolean(form.zoneId && form.building && form.room);
  },

  saveAddress() {
    if (this.data.saving) return;
    const selectedType = this.data.addressTypes[this.data.typeIndex] ? this.data.addressTypes[this.data.typeIndex].id : 'DELIVERY';
    const preserveLegacyType = this.data.form.addressType === 'CAMPUS' && Boolean(this.data.addressId);
    const form = Object.assign({}, this.data.form, { addressType: preserveLegacyType ? 'CAMPUS' : selectedType });
    if (!this.validForm(form)) { wx.showToast({ title: form.addressType === 'SHIPPING' ? '请填写完整快递地址和手机号' : '请填写完整配送地址和手机号', icon: 'none' }); return; }
    if (api.isProduction()) {
      if (form.addressType === 'CAMPUS' && (!form.campusName || !form.zoneName)) { wx.showToast({ title: '请补充配送区域信息', icon: 'none' }); return; }
      this.setData({ saving: true });
      api.saveAddress(Object.assign({}, form, { id: this.data.addressId })).then(result => {
        const saved = result.data;
        const addresses = storage.getAddresses().filter(item => item.id !== saved.id);
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
    const sameType = addresses.filter(item => (item.addressType || 'CAMPUS') === address.addressType);
    if (address.isDefault || sameType.length === 1) addresses.forEach(item => { if ((item.addressType || 'CAMPUS') === address.addressType) item.isDefault = item.id === address.id; });
    storage.saveAddresses(addresses);
    wx.showToast({ title: '保存成功', icon: 'success' });
    setTimeout(() => wx.navigateBack(), 450);
  }
});
