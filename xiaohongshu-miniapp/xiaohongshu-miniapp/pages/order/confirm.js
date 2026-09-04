const app = getApp();

Page({
  data: {
    product: null,
    storeName: '官方商城',
    quantity: 1,
    maxQuantity: 10,
    useDate: '',
    guestName: '',
    contactPhone: '',
    minDate: '',
    maxDate: '',
    totalText: '0.00',
    loading: true,
    submitting: false,
    error: ''
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
    this.mappingId = Number(options.mapping_id || 0);
    // Reuse one idempotency key if the network drops after order creation.
    this.orderRequestId = `${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
    const today = new Date();
    const max = new Date(today.getFullYear() + 1, today.getMonth(), today.getDate());
    this.setData({ minDate: this.formatDate(today), maxDate: this.formatDate(max) });
    this.loadProduct();
  },

  loadProduct() {
    this.setData({ loading: true, error: '' });
    app.request('/catalog').then(catalog => {
      const product = (catalog.products || []).find(item => Number(item.id) === this.mappingId);
      if (!product) throw new Error('票种当前不可购买');
      product.priceText = (Number(product.price_cents) / 100).toFixed(2);
      product.isPackage = product.product_kind === 'scenic_hotel_package';
      product.kindLabel = product.isPackage ? '酒景套餐' : '景区门票';
      product.quantityLabel = product.isPackage ? '套餐份数' : '购票数量';
      product.quantityHint = product.isPackage
        ? `每份含${product.rooms_per_package}间房住${product.nights}晚`
        : '每人一票一码';
      product.isDeferredPackage = product.isPackage && product.booking_mode === 'after_purchase';
      product.useDateLabel = product.isPackage ? '入住日期' : '游玩日期';
      product.stayText = product.isPackage ? `${product.hotel_name} · ${product.room_type_name} · ${product.nights}晚` : '';
      const maxQuantity = catalog.max_order_cents
        ? Math.max(1, Math.floor(Number(catalog.max_order_cents) / Number(product.price_cents)))
        : 100;
      this.setData({ product, storeName: catalog.store_name || '官方商城', maxQuantity, loading: false }, () => {
        app.setStoreName(catalog.store_name || '官方商城');
        this.updateTotal();
      });
    }).catch(error => this.setData({ loading: false, error: error.message || '票种加载失败' }));
  },

  decrease() {
    if (this.data.quantity <= 1) return;
    this.setData({ quantity: this.data.quantity - 1, error: '' }, () => this.updateTotal());
  },

  increase() {
    if (this.data.quantity >= this.data.maxQuantity) return;
    this.setData({ quantity: this.data.quantity + 1, error: '' }, () => this.updateTotal());
  },

  onDateChange(event) {
    this.setData({ useDate: event.detail.value || '', error: '' });
  },

  onGuestNameInput(event) {
    this.setData({ guestName: event.detail.value || '', error: '' });
  },

  onContactPhoneInput(event) {
    this.setData({ contactPhone: event.detail.value || '', error: '' });
  },

  updateTotal() {
    const cents = Number(this.data.product ? this.data.product.price_cents : 0) * this.data.quantity;
    this.setData({ totalText: (cents / 100).toFixed(2) });
  },

  submit() {
    if (!this.data.product || this.data.submitting) return;
    if (this.data.product.requires_use_date && !this.data.useDate) {
      this.setData({ error: `请选择${this.data.product.useDateLabel}` });
      return;
    }
    if (this.data.product.isPackage && !this.data.product.isDeferredPackage && !this.data.guestName.trim()) {
      this.setData({ error: '请填写入住人姓名' });
      return;
    }
    if (this.data.product.isPackage && !this.data.product.isDeferredPackage && !/^[0-9+\-\s]{6,20}$/.test(this.data.contactPhone.trim())) {
      this.setData({ error: '请填写有效的联系电话' });
      return;
    }
    this.setData({ submitting: true, error: '' });
    app.request('/orders', {
      method: 'POST',
      data: {
        mapping_id: this.data.product.id,
        quantity: this.data.quantity,
        request_id: this.orderRequestId,
        use_date: this.data.useDate,
        guest_name: this.data.guestName.trim(),
        contact_phone: this.data.contactPhone.trim()
      }
    }).then(order => {
      if (!order.order_id || !order.pay_token) throw new Error('支付订单信息不完整');
      xhs.requestGuaranteeOrderPayment({
        orderInfo: { payToken: order.pay_token, orderId: order.order_id },
        complete: () => xhs.redirectTo({ url: `/pages/order/detail?order_no=${order.order_no}` })
      });
    }).catch(error => {
      this.setData({ submitting: false, error: error.message || '订单创建失败，请稍后重试' });
    });
  },

  formatDate(date) {
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }
});
