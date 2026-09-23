const mock = require('../../../data/mock');
const api = require('../../../services/api');
const format = require('../../../utils/format');

const scopes = [
  { id: 'UNIVERSAL', name: '外卖与冷吃通用' },
  { id: 'TAKEAWAY', name: '仅校园外卖' },
  { id: 'COURIER', name: '仅冷吃快递' }
];

function emptyForm() {
  return { id: '', name: '通用优惠券', discountYuan: '3.00', minimumYuan: '25.00', validDays: '5', scope: 'UNIVERSAL', excludeDeliveryFee: true, enabled: true };
}

function scopeName(id) {
  const item = scopes.find(scope => scope.id === id);
  return item ? item.name : '外卖与冷吃通用';
}

function decorate(template) {
  return Object.assign({}, template, {
    id: template.id || template._id,
    discountText: format.yuan(template.discountAmount),
    minimumText: format.yuan(template.minGoodsAmount),
    scopeText: scopeName(template.scope),
    validText: `${template.validDays || 0} 天有效`,
    statusText: template.enabled === false ? '已停用' : '可用于新活动',
    createdText: template.createdAt ? format.dateTime(template.createdAt) : '刚刚'
  });
}

function formFromTemplate(template) {
  return {
    id: template.id || template._id || '',
    name: template.name || '',
    discountYuan: format.yuan(template.discountAmount),
    minimumYuan: format.yuan(template.minGoodsAmount),
    validDays: String(template.validDays || 5),
    scope: template.scope || 'UNIVERSAL',
    excludeDeliveryFee: template.excludeDeliveryFee !== false,
    enabled: template.enabled !== false
  };
}

Page({
  data: { templates: [], scopes, scopeIndex: 0, form: emptyForm(), production: false, unavailable: false, loading: false, saving: false },

  onShow() { this.loadTemplates(); },

  loadTemplates() {
    this.setData({ production: api.isProduction(), loading: true });
    if (api.isProduction()) {
      api.getCouponTemplates().then(result => {
        const templates = (result.data || []).map(decorate);
        this.setData({ templates, loading: false });
        if (templates.length && !this.data.form.id) this.selectTemplateById(templates[0].id);
      }).catch(error => {
        console.error('load coupon templates failed', error);
        this.setData({ loading: false, unavailable: String(error && (error.code || error.message) || '').indexOf('MERCHANT_API_UNAVAILABLE') >= 0, form: { id: '', name: '', discountYuan: '', minimumYuan: '', validDays: '', scope: 'UNIVERSAL', excludeDeliveryFee: true, enabled: false } });
        wx.showToast({ title: this.data.unavailable ? '优惠券管理暂未开放' : '优惠券模板加载失败', icon: 'none' });
      });
      return;
    }
    const demo = decorate({ id: 'demo_coupon_template', name: mock.campaign.starterCoupon, discountAmount: 300, minGoodsAmount: 2500, validDays: 5, scope: 'UNIVERSAL', excludeDeliveryFee: true, enabled: true, createdAt: new Date().toISOString() });
    this.setData({ templates: [demo], loading: false });
    if (!this.data.form.id) this.selectTemplateById(demo.id);
  },

  selectTemplateById(id) {
    const template = this.data.templates.find(item => item.id === id);
    if (!template) return;
    const form = formFromTemplate(template);
    this.setData({ form, scopeIndex: Math.max(0, scopes.findIndex(scope => scope.id === form.scope)) });
  },

  selectTemplate(event) { this.selectTemplateById(event.currentTarget.dataset.id); },
  newTemplate() { this.setData({ form: emptyForm(), scopeIndex: 0 }); },
  onInput(event) { this.setData({ [`form.${event.currentTarget.dataset.field}`]: event.detail.value }); },
  selectScope(event) {
    const scopeIndex = Number(event.detail.value);
    this.setData({ scopeIndex, 'form.scope': scopes[scopeIndex] ? scopes[scopeIndex].id : 'UNIVERSAL' });
  },
  toggleExcludeDeliveryFee(event) { this.setData({ 'form.excludeDeliveryFee': event.detail.value }); },
  toggleEnabled(event) { this.setData({ 'form.enabled': event.detail.value }); },

  async save() {
    if (this.data.saving) return;
    if (this.data.unavailable) { wx.showToast({ title: '优惠券管理暂未开放', icon: 'none' }); return; }
    const form = this.data.form;
    if (!form.name.trim() || !/^\d+(\.\d{1,2})?$/.test(form.discountYuan) || !/^\d+(\.\d{1,2})?$/.test(form.minimumYuan) || !/^\d+$/.test(form.validDays)) {
      wx.showToast({ title: '请检查名称、金额和有效天数', icon: 'none' });
      return;
    }
    const discountAmount = Math.round(Number(form.discountYuan) * 100);
    const minGoodsAmount = Math.round(Number(form.minimumYuan) * 100);
    const validDays = Number(form.validDays);
    if (discountAmount <= 0 || minGoodsAmount < 0 || validDays < 1 || validDays > 365) {
      wx.showToast({ title: '优惠金额和有效期不合法', icon: 'none' });
      return;
    }
    if (!this.data.production) {
      wx.showToast({ title: '演示模式不能修改云端模板', icon: 'none' });
      return;
    }
    this.setData({ saving: true });
    try {
      await api.saveCouponTemplate({ id: form.id, name: form.name.trim(), discountAmount, minGoodsAmount, validDays, scope: form.scope, excludeDeliveryFee: form.excludeDeliveryFee, enabled: form.enabled });
      this.setData({ form: emptyForm(), scopeIndex: 0 });
      await this.loadTemplates();
      wx.showToast({ title: '模板已保存，新建版本已生效', icon: 'success' });
    } catch (error) {
      console.error('save coupon template failed', error);
      wx.showToast({ title: '模板保存失败，请重试', icon: 'none' });
    } finally {
      this.setData({ saving: false });
    }
  }
});
