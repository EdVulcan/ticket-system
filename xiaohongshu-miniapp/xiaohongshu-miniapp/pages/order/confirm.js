const app = getApp();
const { requestGuaranteeOrderPayment } = require('../../utils/payment');
const calendar = require('../../utils/calendar');

Page({
  data: {
    product: null,
    storeName: '',
    quantity: 1,
    maxQuantity: 10,
    useDate: '',
    guestName: '',
    contactPhone: '',
    minDate: '',
    maxDate: '',
    dateChips: [],
    calendarOpen: false,
    createdOrderNo: '',
    calendarTitle: '',
    calendarCells: [],
    canPreviousMonth: false,
    canNextMonth: false,
    totalText: '0.00',
    loading: true,
    submitting: false,
    error: ''
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '确认订单');
    this.mappingId = Number(options.mapping_id || 0);
    this.requestedQuantity = Number(options.quantity || 1);
    this.requestedUseDate = options.use_date || '';
    // Reuse one idempotency key if the network drops after order creation.
    this.orderRequestId = `${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
    this.loadProduct();
  },

  loadProduct() {
    this.setData({ loading: true, error: '' });
    app.request('/catalog').then(catalog => {
      const product = (catalog.products || []).find(item => Number(item.id) === this.mappingId);
      if (!product) throw new Error('票种当前不可购买');
      product.priceText = (Number(product.price_cents) / 100).toFixed(2);
      product.isPackage = product.product_kind === 'scenic_hotel_package';
      product.kindLabel = product.isPackage ? '酒景套餐' : (product.product_kind === 'ticket' ? '景区门票' : '商品');
      product.quantityLabel = product.isPackage ? '套餐份数' : '购票数量';
      product.quantityHint = product.isPackage
        ? `每份含${product.rooms_per_package}间房住${product.nights}晚`
        : '订单权益以服务端确认结果为准';
      product.isDeferredPackage = product.isPackage && product.booking_mode === 'after_purchase';
      product.requiresUseDate = Boolean(product.requires_use_date) && !product.isDeferredPackage;
      product.useDateLabel = product.isPackage ? '入住日期' : '游玩日期';
      product.stayText = product.isPackage ? `${product.hotel_name} · ${product.room_type_name} · ${product.nights}晚` : '';
      const orderMaximum = Number(catalog.max_order_cents) > 0 && Number(product.price_cents) > 0
        ? Math.max(1, Math.floor(Number(catalog.max_order_cents) / Number(product.price_cents)))
        : 100;
      const configuredProductMaximum = Math.floor(Number(product.max_quantity));
      const productMaximum = configuredProductMaximum > 0 ? configuredProductMaximum : 100;
      const maxQuantity = Math.min(100, productMaximum, orderMaximum);
      const today = new Date();
      const minDate = calendar.formatDate(calendar.addDays(today, product.isPackage ? Math.max(0, Number(product.min_advance_days || 0)) : 0));
      const maxDate = calendar.formatDate(calendar.addDays(today, 365));
      const quantity = Math.min(Math.max(1, Math.floor(this.requestedQuantity || 1)), maxQuantity);
      const useDate = product.requiresUseDate && calendar.isDateWithin(this.requestedUseDate, minDate, maxDate) ? this.requestedUseDate : '';
      this.calendarMonth = calendar.monthStart(useDate || today);
      this.setData({ product, storeName: catalog.store_name || '', maxQuantity, quantity, useDate, minDate, maxDate, loading: false }, () => {
        app.setStoreName(catalog.store_name || '');
        this.updateTotal();
        this.refreshCalendar();
      });
    }).catch(error => this.setData({ loading: false, error: error.message || '票种加载失败' }));
  },

  retry() { this.loadProduct(); },

  decrease() {
    if (this.data.submitting || this.data.createdOrderNo) return;
    if (this.data.quantity <= 1) return;
    this.setData({ quantity: this.data.quantity - 1, error: '' }, () => this.updateTotal());
  },

  increase() {
    if (this.data.submitting || this.data.createdOrderNo) return;
    if (this.data.quantity >= this.data.maxQuantity) return;
    this.setData({ quantity: this.data.quantity + 1, error: '' }, () => this.updateTotal());
  },

  selectDate(event) {
    if (this.data.submitting || this.data.createdOrderNo) return;
    const useDate = event.currentTarget.dataset.date;
    if (!calendar.isDateWithin(useDate, this.data.minDate, this.data.maxDate)) return;
    this.setData({ useDate, error: '' });
    this.refreshCalendar();
  },

  toggleCalendar() { this.setData({ calendarOpen: !this.data.calendarOpen }); },

  previousMonth() {
    if (!calendar.canMoveMonth(this.calendarMonth, -1, this.data.minDate, this.data.maxDate)) return;
    this.calendarMonth = new Date(this.calendarMonth.getFullYear(), this.calendarMonth.getMonth() - 1, 1);
    this.refreshCalendar();
  },

  nextMonth() {
    if (!calendar.canMoveMonth(this.calendarMonth, 1, this.data.minDate, this.data.maxDate)) return;
    this.calendarMonth = new Date(this.calendarMonth.getFullYear(), this.calendarMonth.getMonth() + 1, 1);
    this.refreshCalendar();
  },

  onGuestNameInput(event) {
    if (this.data.submitting || this.data.createdOrderNo) return;
    this.setData({ guestName: event.detail.value || '', error: '' });
  },

  onContactPhoneInput(event) {
    if (this.data.submitting || this.data.createdOrderNo) return;
    this.setData({ contactPhone: event.detail.value || '', error: '' });
  },

  updateTotal() {
    const cents = Number(this.data.product ? this.data.product.price_cents : 0) * this.data.quantity;
    this.setData({ totalText: (cents / 100).toFixed(2) });
  },

  submit() {
    if (!this.data.product || this.data.submitting) return;
    if (this.data.createdOrderNo) {
      xhs.redirectTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(this.data.createdOrderNo)}` });
      return;
    }
    if (this.data.product.requiresUseDate && !calendar.isDateWithin(this.data.useDate, this.data.minDate, this.data.maxDate)) {
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
      if (!order || !order.order_no) {
        this.setData({ submitting: false, error: '订单信息不完整，订单已保留，请在订单列表中查看' });
        return;
      }
      this.setData({ createdOrderNo: order.order_no, calendarOpen: false });
      const started = requestGuaranteeOrderPayment(xhs, order, {
        onSuccess: () => {
          xhs.redirectTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(order.order_no || '')}` });
        },
        onFailure: result => {
          this.setData({ submitting: false, error: result.message });
        },
        onComplete: result => {
          if (result.outcome === 'unknown') {
            this.setData({ submitting: false, error: '支付结果正在确认，订单已保留，请在订单详情继续查看' });
          }
        }
      });
      if (!started) return;
    }).catch(error => {
      this.setData({ submitting: false, error: error.message || '订单创建失败，请稍后重试' });
    });
  },

  refreshCalendar() {
    const today = new Date();
    const month = this.calendarMonth || calendar.monthStart(today);
    this.setData({
      dateChips: calendar.buildDateChips(today, this.data.minDate, this.data.maxDate, this.data.useDate),
      calendarTitle: calendar.monthTitle(month),
      calendarCells: calendar.buildCalendarCells(month, this.data.minDate, this.data.maxDate, this.data.useDate),
      canPreviousMonth: calendar.canMoveMonth(month, -1, this.data.minDate, this.data.maxDate),
      canNextMonth: calendar.canMoveMonth(month, 1, this.data.minDate, this.data.maxDate)
    });
  }
});
