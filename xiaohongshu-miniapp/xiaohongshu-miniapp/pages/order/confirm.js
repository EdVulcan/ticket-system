const app = getApp();

Page({
  data: {
    product: null,
    quantity: 1,
    maxQuantity: 10,
    totalText: '0.00',
    loading: true,
    submitting: false,
    error: ''
  },

  onLoad(options) {
    this.mappingId = Number(options.mapping_id || 0);
    this.loadProduct();
  },

  loadProduct() {
    this.setData({ loading: true, error: '' });
    app.request('/catalog').then(catalog => {
      const product = (catalog.products || []).find(item => Number(item.id) === this.mappingId);
      if (!product) throw new Error('票种当前不可购买');
      product.priceText = (Number(product.price_cents) / 100).toFixed(2);
      const maxQuantity = catalog.max_order_cents
        ? Math.max(1, Math.floor(Number(catalog.max_order_cents) / Number(product.price_cents)))
        : 100;
      this.setData({ product, maxQuantity, loading: false }, () => this.updateTotal());
    }).catch(error => this.setData({ loading: false, error: error.message || '票种加载失败' }));
  },

  decrease() {
    if (this.data.quantity <= 1) return;
    this.setData({ quantity: this.data.quantity - 1 }, () => this.updateTotal());
  },

  increase() {
    if (this.data.quantity >= this.data.maxQuantity) return;
    this.setData({ quantity: this.data.quantity + 1 }, () => this.updateTotal());
  },

  updateTotal() {
    const cents = Number(this.data.product ? this.data.product.price_cents : 0) * this.data.quantity;
    this.setData({ totalText: (cents / 100).toFixed(2) });
  },

  submit() {
    if (!this.data.product || this.data.submitting) return;
    this.setData({ submitting: true, error: '' });
    const requestId = `${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
    app.request('/orders', {
      method: 'POST',
      data: { mapping_id: this.data.product.id, quantity: this.data.quantity, request_id: requestId }
    }).then(order => {
      if (!order.order_id || !order.pay_token) throw new Error('支付订单信息不完整');
      xhs.requestGuaranteeOrderPayment({
        orderInfo: { payToken: order.pay_token, orderId: order.order_id },
        complete: () => xhs.redirectTo({ url: `/pages/order/detail?order_no=${order.order_no}` })
      });
    }).catch(error => {
      this.setData({ submitting: false, error: error.message || '订单创建失败，请稍后重试' });
    });
  }
});
