const storage = require('../../../services/storage');
const format = require('../../../utils/format');
const api = require('../../../services/api');

Page({
  data: { form: {}, unavailable: false, isOpen: false, courierOpen: true, deliveryFeeYuan: '0', minGoodsAmount: '0', courierMinGoodsAmount: '0', shippingFeeYuan: '0', freeShippingYuan: '0', shippingCarrier: '', zones: [], logs: [], saving: false, zoneForm: { id: '', campusName: '', zoneName: '', deliveryFeeYuan: '0', minGoodsAmountYuan: '0', enabled: true } },
  onShow() { this.loadSettings(); },

  loadSettings() {
    if (api.isProduction()) {
      api.getMerchantSettings().then((result) => {
        const form = api.normalizeStore(result.store || storage.getStore());
        storage.saveStore(form);
        this.setData({ form, zones: (result.zones || []).map(zone => Object.assign({}, zone, { id: zone._id, feeText: format.yuan(zone.deliveryFee), minText: format.yuan(zone.minGoodsAmount) })), isOpen: form.businessStatus === 'OPEN', courierOpen: (form.courierStatus || 'OPEN') === 'OPEN', deliveryFeeYuan: format.yuan(form.defaultDeliveryFee), minGoodsAmount: format.yuan(form.minGoodsAmount), courierMinGoodsAmount: format.yuan(form.courierMinGoodsAmount), shippingFeeYuan: format.yuan(form.defaultShippingFee), freeShippingYuan: format.yuan(form.freeShippingThreshold), shippingCarrier: form.shippingCarrier || '' });
      }).catch((error) => { console.error('load store settings failed', error); this.setData({ unavailable: true, form: {}, zones: [], logs: [] }); wx.showToast({ title: String(error && (error.code || error.message) || '').indexOf('MERCHANT_API_UNAVAILABLE') >= 0 ? '店铺设置暂未开放' : '店铺设置加载失败', icon: 'none' }); });
      return;
    }
    const form = storage.getStore();
    this.setData({ form, isOpen: form.businessStatus === 'OPEN', courierOpen: (form.courierStatus || 'OPEN') === 'OPEN', deliveryFeeYuan: format.yuan(form.defaultDeliveryFee), minGoodsAmount: format.yuan(form.minGoodsAmount), courierMinGoodsAmount: format.yuan(form.courierMinGoodsAmount), shippingFeeYuan: format.yuan(form.defaultShippingFee), freeShippingYuan: format.yuan(form.freeShippingThreshold), shippingCarrier: form.shippingCarrier || '' });
  },

  toggleOpen(event) { this.setData({ isOpen: event.detail.value }); },
  toggleCourier(event) { this.setData({ courierOpen: event.detail.value }); },
  onAnnouncementInput(event) { this.setData({ 'form.announcement': event.detail.value }); },
  onPhoneInput(event) { this.setData({ 'form.phone': event.detail.value }); },
  onHoursInput(event) { this.setData({ 'form.businessHours': event.detail.value }); },
  onMinimumInput(event) { this.setData({ minGoodsAmount: event.detail.value }); },
  onCourierMinimumInput(event) { this.setData({ courierMinGoodsAmount: event.detail.value }); },
  onShippingFeeInput(event) { this.setData({ shippingFeeYuan: event.detail.value }); },
  onFreeShippingInput(event) { this.setData({ freeShippingYuan: event.detail.value }); },
  onShippingCarrierInput(event) { this.setData({ shippingCarrier: event.detail.value }); },
  onZoneInput(event) { this.setData({ [`zoneForm.${event.currentTarget.dataset.field}`]: event.detail.value }); },
  onZoneEnabled(event) { this.setData({ 'zoneForm.enabled': event.detail.value }); },
  newZone() { this.setData({ zoneForm: { id: '', campusName: '', zoneName: '', deliveryFeeYuan: '0', minGoodsAmountYuan: '0', enabled: true } }); },
  editZone(event) {
    const zone = this.data.zones.find(item => item.id === event.currentTarget.dataset.id);
    if (zone) this.setData({ zoneForm: Object.assign({}, zone, { deliveryFeeYuan: format.yuan(zone.deliveryFee), minGoodsAmountYuan: format.yuan(zone.minGoodsAmount) }) });
  },
  async saveZone() {
    if (this.data.saving) return;
    if (api.isProduction() && this.data.unavailable) { wx.showToast({ title: '店铺设置暂未开放', icon: 'none' }); return; }
    if (!api.isProduction()) { wx.showToast({ title: '配送区域管理需配置云环境', icon: 'none' }); return; }
    const form = this.data.zoneForm;
    if (!form.campusName.trim() || !form.zoneName.trim() || !/^\d+(\.\d{1,2})?$/.test(form.deliveryFeeYuan) || !/^\d+(\.\d{1,2})?$/.test(form.minGoodsAmountYuan)) { wx.showToast({ title: '请检查区域和金额', icon: 'none' }); return; }
    this.setData({ saving: true });
    try {
      await api.saveDeliveryZone({ id: form.id, campusName: form.campusName, zoneName: form.zoneName, deliveryFee: Math.round(Number(form.deliveryFeeYuan) * 100), minGoodsAmount: Math.round(Number(form.minGoodsAmountYuan) * 100), enabled: form.enabled });
      this.newZone(); this.loadSettings(); wx.showToast({ title: '配送区域已保存', icon: 'success' });
    } catch (error) { wx.showToast({ title: '区域保存失败', icon: 'none' }); }
    finally { this.setData({ saving: false }); }
  },
  loadLogs() {
    if (!api.isProduction()) return;
    api.getOperationLogs().then(result => this.setData({ logs: (result.data || []).map(item => Object.assign({}, item, { timeText: format.dateTime(item.createdAt), detailText: JSON.stringify(item.change || {}) })) })).catch(() => wx.showToast({ title: '日志加载失败', icon: 'none' }));
  },
  changeFee(event) { const value = Math.max(0, Number(this.data.form.defaultDeliveryFee || 0) + Number(event.currentTarget.dataset.delta) * 100); this.setData({ 'form.defaultDeliveryFee': value, deliveryFeeYuan: format.yuan(value) }); },

  saveSettings() {
    if (api.isProduction() && this.data.unavailable) { wx.showToast({ title: '店铺设置暂未开放', icon: 'none' }); return; }
    if (!/^\d+(\.\d{1,2})?$/.test(this.data.minGoodsAmount) || !/^\d+(\.\d{1,2})?$/.test(this.data.courierMinGoodsAmount)) { wx.showToast({ title: '请填写正确起送价', icon: 'none' }); return; }
    if (!/^\d+(\.\d{1,2})?$/.test(this.data.shippingFeeYuan) || !/^\d+(\.\d{1,2})?$/.test(this.data.freeShippingYuan)) { wx.showToast({ title: '请填写正确快递费规则', icon: 'none' }); return; }
    const form = Object.assign({}, this.data.form, { minGoodsAmount: Math.round(Number(this.data.minGoodsAmount) * 100), courierMinGoodsAmount: Math.round(Number(this.data.courierMinGoodsAmount) * 100), businessStatus: this.data.isOpen ? 'OPEN' : 'PAUSED', courierStatus: this.data.courierOpen ? 'OPEN' : 'PAUSED', defaultShippingFee: Math.round(Number(this.data.shippingFeeYuan) * 100), freeShippingThreshold: Math.round(Number(this.data.freeShippingYuan) * 100), shippingCarrier: this.data.shippingCarrier });
    if (api.isProduction()) {
      api.updateStoreSettings({ announcement: form.announcement, phone: form.phone, businessHours: form.businessHours, businessStatus: form.businessStatus, courierStatus: form.courierStatus, defaultDeliveryFee: Number(form.defaultDeliveryFee), defaultMinGoodsAmount: Number(form.minGoodsAmount), courierMinGoodsAmount: Number(form.courierMinGoodsAmount), defaultShippingFee: Number(form.defaultShippingFee), freeShippingThreshold: Number(form.freeShippingThreshold), shippingCarrier: form.shippingCarrier }).then(() => { storage.saveStore(api.normalizeStore(Object.assign({}, form, { defaultMinGoodsAmount: form.minGoodsAmount }))); wx.showToast({ title: '设置已保存', icon: 'success' }); }).catch((error) => { console.error('save store settings failed', error); wx.showToast({ title: '设置保存失败', icon: 'none' }); });
      return;
    }
    storage.saveStore(Object.assign({}, form, { deliveryText: `满 ${format.yuan(form.minGoodsAmount)} 元起送 · 跑腿费 ${format.yuan(form.defaultDeliveryFee)} 元起` }));
    wx.showToast({ title: '设置已保存', icon: 'success' });
  }
});
