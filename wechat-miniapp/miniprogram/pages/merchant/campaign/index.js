const api = require('../../../services/api');
const format = require('../../../utils/format');
const beijingDay = value => new Date(new Date(value).getTime() + 8 * 3600000).toISOString().slice(0, 10);
function emptyForm() { const day = beijingDay(new Date()); return { id: '', name: '好友助力券', discountYuan: '3.00', minimumYuan: '25.00', validDays: '5', startDate: day, endDate: day, enabled: false }; }
Page({
  data: { form: emptyForm(), unavailable: false, saving: false, production: false },
  onShow() {
    this.setData({ production: api.isProduction() });
    if (!api.isProduction()) return;
    api.getMerchantCampaign().then(result => {
      const campaign = result.campaign; const template = result.template;
      if (!campaign || !template) return;
      this.setData({ form: { id: campaign._id, name: template.name, discountYuan: format.yuan(template.discountAmount), minimumYuan: format.yuan(template.minGoodsAmount), validDays: String(template.validDays || 5), startDate: beijingDay(campaign.startAt), endDate: beijingDay(campaign.endAt), enabled: Boolean(campaign.enabled) } });
    }).catch(error => { this.setData({ unavailable: true, form: { id: '', name: '', discountYuan: '', minimumYuan: '', validDays: '', startDate: '', endDate: '', enabled: false } }); wx.showToast({ title: String(error && (error.code || error.message) || '').indexOf('MERCHANT_API_UNAVAILABLE') >= 0 ? '助力活动管理暂未开放' : '活动配置加载失败', icon: 'none' }); });
  },
  onInput(event) { this.setData({ [`form.${event.currentTarget.dataset.field}`]: event.detail.value }); },
  onEnabled(event) { this.setData({ 'form.enabled': event.detail.value }); },
  newCampaign() { this.setData({ form: emptyForm() }); },
  async save() {
    if (this.data.saving) return;
    if (this.data.unavailable) { wx.showToast({ title: '助力活动管理暂未开放', icon: 'none' }); return; }
    if (!api.isProduction()) { wx.showToast({ title: '活动配置需连接生产云环境', icon: 'none' }); return; }
    const form = this.data.form;
    if (!/^\d+(\.\d{1,2})?$/.test(form.discountYuan) || !/^\d+(\.\d{1,2})?$/.test(form.minimumYuan) || !/^\d+$/.test(form.validDays)) { wx.showToast({ title: '请检查优惠金额与有效天数', icon: 'none' }); return; }
    this.setData({ saving: true });
    try {
      const result = await api.saveCampaign({ id: form.id, name: form.name, enabled: form.enabled, discountAmount: Math.round(Number(form.discountYuan) * 100), minGoodsAmount: Math.round(Number(form.minimumYuan) * 100), validDays: Number(form.validDays), startAt: form.startDate + 'T00:00:00+08:00', endAt: form.endDate + 'T23:59:59+08:00' });
      this.setData({ 'form.id': result.id });
      wx.showToast({ title: '活动已保存', icon: 'success' });
    } catch (error) { wx.showToast({ title: '保存失败，请检查日期和金额', icon: 'none' }); }
    finally { this.setData({ saving: false }); }
  }
});
