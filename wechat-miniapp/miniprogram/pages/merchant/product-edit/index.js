const storage = require('../../../services/storage');
const api = require('../../../services/api');
const mock = require('../../../data/mock');
const yuan = value => (Number(value || 0) / 100).toFixed(2);
Page({
  data: { productId: '', unavailable: false, saving: false, uploading: false, categories: [], categoryIndex: 0, fulfillmentTypes: [{ id: 'TAKEAWAY', name: '校园外卖' }, { id: 'COURIER', name: '冷吃快递' }], fulfillmentIndex: 0, form: { fulfillmentType: 'TAKEAWAY', name: '', description: '', basePriceYuan: '', originalPriceYuan: '', stock: '0', imageFileIds: [], optionGroups: [] } },
  onLoad(options) {
    this.setData({ productId: options.id || '' });
    const apply = (products, categories) => {
      const product = products.find(item => String(item.id || item._id) === String(this.data.productId));
      if (this.data.productId && !product) { wx.showToast({ title: '商品不存在', icon: 'none' }); return; }
      const fulfillmentType = product && product.fulfillmentType === 'COURIER' ? 'COURIER' : 'TAKEAWAY';
      this.setData({ categories, fulfillmentIndex: fulfillmentType === 'COURIER' ? 1 : 0, categoryIndex: Math.max(0, categories.findIndex(item => String(item.id || item._id) === String(product && product.categoryId))) });
      if (product) this.setData({ form: { fulfillmentType, name: product.name || '', description: product.description || '', basePriceYuan: yuan(product.basePrice !== undefined ? product.basePrice : product.price), originalPriceYuan: yuan(product.originalPrice || product.basePrice || product.price), stock: String(product.stock || 0), imageFileIds: product.imageFileIds || [], optionGroups: (product.optionGroups || []).map(group => Object.assign({}, group, { required: group.required !== false, options: group.options.map(option => Object.assign({}, option, { priceYuan: yuan(option.priceDelta) })) })) } });
    };
    if (api.isProduction()) api.getMerchantProducts().then(result => apply(result.data || [], result.categories || [])).catch(error => { this.setData({ unavailable: true }); wx.showToast({ title: String(error && (error.code || error.message) || '').indexOf('MERCHANT_API_UNAVAILABLE') >= 0 ? '商品管理暂未开放' : '商品加载失败', icon: 'none' }); });
    else apply(storage.getProducts(), mock.categories.filter(item => item.id !== 'all'));
  },
  onInput(event) { this.setData({ [`form.${event.currentTarget.dataset.field}`]: event.detail.value }); },
  selectFulfillment(event) { const fulfillmentIndex = Number(event.detail.value); this.setData({ fulfillmentIndex, 'form.fulfillmentType': this.data.fulfillmentTypes[fulfillmentIndex].id }); },
  selectCategory(event) { this.setData({ categoryIndex: Number(event.detail.value) }); },
  addGroup() {
    if (this.data.form.optionGroups.length >= 6) return;
    this.setData({ 'form.optionGroups': this.data.form.optionGroups.concat({ id: storage.makeId('group'), name: '', required: true, options: [{ id: storage.makeId('option'), name: '', priceYuan: '0' }] }) });
  },
  removeGroup(event) { this.setData({ 'form.optionGroups': this.data.form.optionGroups.filter((item, index) => index !== Number(event.currentTarget.dataset.group)) }); },
  groupInput(event) { const { group, field } = event.currentTarget.dataset; this.setData({ [`form.optionGroups[${group}].${field}`]: event.detail.value }); },
  optionInput(event) { const { group, option, field } = event.currentTarget.dataset; this.setData({ [`form.optionGroups[${group}].options[${option}].${field}`]: event.detail.value }); },
  addOption(event) {
    const index = Number(event.currentTarget.dataset.group); const group = this.data.form.optionGroups[index];
    if (!group || group.options.length >= 12) return;
    this.setData({ [`form.optionGroups[${index}].options`]: group.options.concat({ id: storage.makeId('option'), name: '', priceYuan: '0' }) });
  },
  removeOption(event) {
    const { group, option } = event.currentTarget.dataset;
    this.setData({ [`form.optionGroups[${group}].options`]: this.data.form.optionGroups[group].options.filter((item, index) => index !== Number(option)) });
  },
  chooseImage() {
    if (this.data.uploading) return;
    // CloudBase is retired from the production path. Keep the control explicit
    // until the SaaS media endpoint is wired; never upload a product image to a
    // second, unscoped storage authority.
    wx.showToast({ title: api.isProduction() ? '图片上传接口尚未接入' : '演示模式暂不上传图片', icon: 'none' });
  },
  async saveProduct() {
    if (this.data.saving || this.data.uploading) return;
    if (api.isProduction() && this.data.unavailable) { wx.showToast({ title: '商品管理暂未开放', icon: 'none' }); return; }
    const form = this.data.form;
    const category = this.data.categories[this.data.categoryIndex];
    if (!form.name.trim() || !category || !/^\d+(\.\d{1,2})?$/.test(form.basePriceYuan) || !/^\d+(\.\d{1,2})?$/.test(form.originalPriceYuan)) { wx.showToast({ title: '请检查名称、分类和价格', icon: 'none' }); return; }
    const change = { name: form.name.trim(), description: form.description.trim(), basePrice: Math.round(Number(form.basePriceYuan) * 100), originalPrice: Math.round(Number(form.originalPriceYuan) * 100), categoryId: category.id || category._id, fulfillmentType: form.fulfillmentType, imageFileIds: form.imageFileIds };
    if (form.optionGroups.some(group => !group.name.trim() || !group.options.length || group.options.some(option => !option.name.trim() || !/^\d+(\.\d{1,2})?$/.test(option.priceYuan)))) { wx.showToast({ title: '请完善规格名称、选项及加价', icon: 'none' }); return; }
    change.optionGroups = form.optionGroups.map(group => ({ id: group.id, name: group.name.trim(), required: group.required, options: group.options.map(option => ({ id: option.id, name: option.name.trim(), priceDelta: Math.round(Number(option.priceYuan) * 100) })) }));
    if (change.basePrice <= 0 || change.originalPrice < change.basePrice) { wx.showToast({ title: '划线价不能低于现价', icon: 'none' }); return; }
    if (!this.data.productId) {
      if (!/^\d+$/.test(form.stock)) { wx.showToast({ title: '请填写非负整数库存', icon: 'none' }); return; }
      change.stock = Number(form.stock);
    }
    this.setData({ saving: true });
    try {
      if (api.isProduction()) {
        if (this.data.productId) await api.updateProduct(this.data.productId, change);
        else await api.createProduct(change);
      } else {
        const products = storage.getProducts();
        const product = products.find(item => String(item.id) === String(this.data.productId));
        if (product) Object.assign(product, change, { price: change.basePrice });
        else products.push(Object.assign({}, change, { id: storage.makeId('product'), price: change.basePrice, stockMode: 'LIMITED', isOnSale: false, isSoldOut: change.stock === 0, options: [], emoji: form.fulfillmentType === 'COURIER' ? '🐇' : '🍱', color: form.fulfillmentType === 'COURIER' ? '#F6E1D8' : '#FBE7D8' }));
        storage.saveProducts(products);
      }
      wx.showToast({ title: this.data.productId ? '商品已保存' : '已新建，请在列表上架', icon: 'success' });
      wx.navigateBack();
    } catch (error) { wx.showToast({ title: '商品保存失败，请检查信息', icon: 'none' }); }
    finally { this.setData({ saving: false }); }
  }
});
