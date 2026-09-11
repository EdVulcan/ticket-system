const app = getApp();
const { requestGuaranteeOrderPayment } = require('../../utils/payment');
const calendar = require('../../utils/calendar');
const promotion = require('../../utils/promotion');

const ORDER_RECOVERY_ERROR_CODES = ['idempotency_payload_mismatch', 'existing_order_recovery_required'];
const ORDER_PRECHECK_ERROR_CODES = ['order_not_created'];

function errorData(error) {
  return error && error.data && typeof error.data === 'object' ? error.data : {};
}

function errorCode(error) {
  const data = errorData(error);
  return String(data.error_code || error && (error.error_code || error.errorCode) || '').trim();
}

function errorOrderNo(error) {
  const data = errorData(error);
  return String(data.order_no || data.orderNo || error && (error.order_no || error.orderNo) || '').trim();
}

function includesErrorCode(codes, code) {
  return Boolean(code) && codes.indexOf(code) >= 0;
}

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
    originalTotalText: '0.00',
    discountText: '0.00',
    hasDiscount: false,
    quoteToken: '',
    quoteReady: false,
    quoteLoading: false,
    quoteError: '',
    opportunity: null,
    opportunityVisible: false,
    opportunityAmountText: '',
    opportunityCountdown: '',
    opportunityReservedOrderNo: '',
    orderRecoveryPending: false,
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
    this.loadOpportunity();
  },

  onShow() {
    if (!this.hasShown) {
      this.hasShown = true;
      return;
    }
    if (this.data.product && !this.data.submitting && !this.data.createdOrderNo && !this.data.orderRecoveryPending) {
      this.loadOpportunity();
      this.refreshQuote();
    }
  },

  onUnload() {
    this.quoteVersion = (this.quoteVersion || 0) + 1;
    this.opportunityVersion = (this.opportunityVersion || 0) + 1;
    this.stopOpportunityTimer();
  },

  loadProduct() {
    if (this.data.orderRecoveryPending) return Promise.resolve();
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
        this.refreshQuote();
      });
    }).catch(error => this.setData({ loading: false, error: error.message || '票种加载失败' }));
  },

  retry() { this.loadProduct(); },

  isOrderSelectionLocked() {
    return Boolean(this.data.submitting || this.data.createdOrderNo || this.data.orderRecoveryPending);
  },

  decrease() {
    if (this.isOrderSelectionLocked()) return;
    if (this.data.quantity <= 1) return;
    this.setData({ quantity: this.data.quantity - 1, error: '' }, () => {
      this.updateTotal();
      this.refreshQuote();
    });
  },

  increase() {
    if (this.isOrderSelectionLocked()) return;
    if (this.data.quantity >= this.data.maxQuantity) return;
    this.setData({ quantity: this.data.quantity + 1, error: '' }, () => {
      this.updateTotal();
      this.refreshQuote();
    });
  },

  selectDate(event) {
    if (this.isOrderSelectionLocked()) return;
    const useDate = event.currentTarget.dataset.date;
    if (!calendar.isDateWithin(useDate, this.data.minDate, this.data.maxDate)) return;
    this.setData({ useDate, error: '' });
    this.refreshCalendar();
  },

  toggleCalendar() {
    if (this.isOrderSelectionLocked()) return;
    this.setData({ calendarOpen: !this.data.calendarOpen });
  },

  previousMonth() {
    if (this.isOrderSelectionLocked()) return;
    if (!calendar.canMoveMonth(this.calendarMonth, -1, this.data.minDate, this.data.maxDate)) return;
    this.calendarMonth = new Date(this.calendarMonth.getFullYear(), this.calendarMonth.getMonth() - 1, 1);
    this.refreshCalendar();
  },

  nextMonth() {
    if (this.isOrderSelectionLocked()) return;
    if (!calendar.canMoveMonth(this.calendarMonth, 1, this.data.minDate, this.data.maxDate)) return;
    this.calendarMonth = new Date(this.calendarMonth.getFullYear(), this.calendarMonth.getMonth() + 1, 1);
    this.refreshCalendar();
  },

  onGuestNameInput(event) {
    if (this.isOrderSelectionLocked()) return;
    this.setData({ guestName: event.detail.value || '', error: '' });
  },

  onContactPhoneInput(event) {
    if (this.isOrderSelectionLocked()) return;
    this.setData({ contactPhone: event.detail.value || '', error: '' });
  },

  updateTotal() {
    const cents = Number(this.data.product ? this.data.product.price_cents : 0) * this.data.quantity;
    this.setData({ originalTotalText: promotion.money(cents) });
  },

  loadOpportunity() {
    if (this.data.orderRecoveryPending) return Promise.resolve();
    const version = (this.opportunityVersion || 0) + 1;
    this.opportunityVersion = version;
    return app.request('/promotion', { method: 'POST', data: {} }).then(raw => {
      if (version !== this.opportunityVersion) return;
      this.applyOpportunity(promotion.normalize(raw, Date.now()));
      // Acquisition may finish after the first quote. Always price again
      // from the server so a late offer cannot leave the full-price quote.
      return this.refreshQuote();
    }).catch(() => {
      if (version === this.opportunityVersion) this.applyOpportunity(null);
    });
  },

  applyOpportunity(opportunity) {
    if (this.data.orderRecoveryPending) return;
    const hadApplicableGrant = this.opportunity && this.opportunity.status === 'available' &&
      this.opportunity.mappingIds.indexOf(Number(this.mappingId)) >= 0;
    this.opportunity = opportunity;
    const available = promotion.appliesTo(opportunity, this.mappingId);
    const reserved = opportunity && opportunity.status === 'reserved' && opportunity.reservedOrderNo;
    this.setData({
      opportunity,
      opportunityVisible: Boolean(available || reserved),
      opportunityAmountText: available ? promotion.money(opportunity.discountCents) : '',
      opportunityCountdown: promotion.countdown(opportunity),
      opportunityReservedOrderNo: reserved ? opportunity.reservedOrderNo : ''
    });
    this.startOpportunityTimer();
    if (hadApplicableGrant && !available && this.data.quoteReady && !this.data.quoteLoading && !this.data.createdOrderNo) {
      this.refreshQuote().then(() => this.setData({ error: '优惠已结束，价格已更新，请确认后重新提交' }));
    }
  },

  startOpportunityTimer() {
    this.stopOpportunityTimer();
    if (this.data.orderRecoveryPending || !this.opportunity || !promotion.appliesTo(this.opportunity, this.mappingId) || typeof setInterval !== 'function') return;
    this.opportunityTimer = setInterval(() => {
      if (!promotion.appliesTo(this.opportunity, this.mappingId)) return this.applyOpportunity(this.opportunity);
      this.setData({ opportunityCountdown: promotion.countdown(this.opportunity) });
    }, 1000);
  },

  stopOpportunityTimer() {
    if (this.opportunityTimer && typeof clearInterval === 'function') clearInterval(this.opportunityTimer);
    this.opportunityTimer = null;
  },

  refreshQuote() {
    if (!this.data.product || this.data.createdOrderNo || this.data.orderRecoveryPending) return Promise.resolve();
    const version = (this.quoteVersion || 0) + 1;
    this.quoteVersion = version;
    this.setData({ quoteLoading: true, quoteReady: false, quoteToken: '', quoteError: '', hasDiscount: false, totalText: '—' });
    return app.request('/order-quote', {
      method: 'POST',
      data: { mapping_id: this.data.product.id, quantity: this.data.quantity }
    }).then(quote => {
      if (version !== this.quoteVersion) return;
      const amount = Number(quote && quote.amount_cents);
      const original = Number(quote && quote.original_amount_cents);
      const discount = Number(quote && quote.discount_cents);
      const quoteToken = String((quote && quote.quote_token) || '');
      if (!quoteToken || !Number.isFinite(amount) || amount < 0 || !Number.isFinite(original) || original < 0 || !Number.isFinite(discount) || discount < 0) {
        throw new Error('优惠价格暂不可用，请刷新后重试');
      }
      if (quote.promotion) this.applyOpportunity(promotion.normalize(quote.promotion, Date.now()));
      this.setData({
        quoteLoading: false,
        quoteReady: true,
        quoteToken,
        quoteError: '',
        originalTotalText: promotion.money(original),
        discountText: promotion.money(discount),
        hasDiscount: discount > 0,
        totalText: promotion.money(amount)
      });
    }).catch(error => {
      if (version !== this.quoteVersion) return;
      this.setData({ quoteLoading: false, quoteReady: false, quoteToken: '', quoteError: error.message || '价格更新失败，请重试', hasDiscount: false, totalText: '—' });
    });
  },

  buildOrderPayload() {
    return {
      mapping_id: this.data.product.id,
      quantity: this.data.quantity,
      request_id: this.orderRequestId,
      quote_token: this.data.quoteToken,
      use_date: this.data.useDate,
      guest_name: this.data.guestName.trim(),
      contact_phone: this.data.contactPhone.trim()
    };
  },

  captureOrderRecoverySnapshot(payload) {
    return {
      quantity: payload.quantity,
      useDate: payload.use_date,
      guestName: payload.guest_name,
      contactPhone: payload.contact_phone,
      quoteReady: this.data.quoteReady,
      quoteToken: payload.quote_token,
      originalTotalText: this.data.originalTotalText,
      discountText: this.data.discountText,
      hasDiscount: this.data.hasDiscount,
      totalText: this.data.totalText
    };
  },

  invalidateMutableRequests() {
    this.quoteVersion = (this.quoteVersion || 0) + 1;
    this.opportunityVersion = (this.opportunityVersion || 0) + 1;
    this.stopOpportunityTimer();
  },

  restoreOrderRecoverySnapshot() {
    const snapshot = this.orderRecoverySnapshot;
    if (!snapshot) return;
    this.setData({
      quantity: snapshot.quantity,
      useDate: snapshot.useDate,
      guestName: snapshot.guestName,
      contactPhone: snapshot.contactPhone,
      quoteLoading: false,
      quoteReady: Boolean(snapshot.quoteReady && snapshot.quoteToken),
      quoteToken: snapshot.quoteToken,
      quoteError: '',
      originalTotalText: snapshot.originalTotalText,
      discountText: snapshot.discountText,
      hasDiscount: snapshot.hasDiscount,
      totalText: snapshot.totalText,
      calendarOpen: false,
      orderRecoveryPending: true
    });
  },

  enterUnknownOrderRecovery() {
    this.invalidateMutableRequests();
    this.restoreOrderRecoverySnapshot();
    this.setData({
      submitting: false,
      orderRecoveryPending: true,
      error: '订单创建结果暂时无法确认，已保留首次提交的日期、数量、游客信息和报价。请点击“重试确认订单”恢复原请求，不要修改当前选择。'
    });
  },

  releaseOrderRecovery(error) {
    this.invalidateMutableRequests();
    this.orderPayload = null;
    this.orderRecoverySnapshot = null;
    const detail = String(error && error.message || '').trim();
    const baseMessage = detail || '订单校验未通过';
    this.setData({ submitting: false, orderRecoveryPending: false, error: `${baseMessage}，已确认本次未创建订单，请核对信息并重新报价后提交` });
    return this.refreshQuote().then(() => {
      if (this.data.orderRecoveryPending) return;
      const suffix = this.data.quoteReady ? '请核对最新报价后重新提交' : '请重新报价后提交';
      this.setData({ error: `${baseMessage}，已确认本次未创建订单，${suffix}` });
    });
  },

  recoverExistingOrder(error, errorCodeValue) {
    this.invalidateMutableRequests();
    this.orderPayload = null;
    this.orderRecoverySnapshot = null;
    const orderNo = errorOrderNo(error);
    const message = errorCodeValue === 'idempotency_payload_mismatch'
      ? '原请求已关联其他订单选择，未自动发起支付；请打开原订单核对后继续。'
      : '原订单已创建但当前响应信息不足，未自动发起支付；请打开订单详情核对后继续。';
    this.setData({ submitting: false, orderRecoveryPending: false, createdOrderNo: orderNo, error: message });
    const url = orderNo
      ? `/pages/order/detail?order_no=${encodeURIComponent(orderNo)}`
      : '/pages/orders/index';
    if (typeof xhs.redirectTo === 'function') xhs.redirectTo({ url });
  },

  handleOrderCreateError(error) {
    const code = errorCode(error);
    if (includesErrorCode(ORDER_RECOVERY_ERROR_CODES, code)) {
      this.recoverExistingOrder(error, code);
      return;
    }
    if (includesErrorCode(ORDER_PRECHECK_ERROR_CODES, code)) {
      this.releaseOrderRecovery(error);
      return;
    }
    this.enterUnknownOrderRecovery();
  },

  createOrder(payload) {
    this.setData({ submitting: true, error: '' });
    return app.request('/orders', {
      method: 'POST',
      data: { ...payload }
    }).then(order => {
      if (!order || !order.order_no) {
        this.enterUnknownOrderRecovery();
        return;
      }
      this.orderPayload = null;
      this.orderRecoverySnapshot = null;
      this.setData({ createdOrderNo: order.order_no, orderRecoveryPending: false, calendarOpen: false });
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
    }).catch(error => this.handleOrderCreateError(error));
  },

  submit() {
    if (!this.data.product || this.data.submitting) return;
    if (this.data.createdOrderNo) {
      xhs.redirectTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(this.data.createdOrderNo)}` });
      return;
    }
    if (this.data.orderRecoveryPending) {
      if (!this.orderPayload) {
        this.setData({ error: '订单恢复信息不可用，请打开订单列表核对，勿重复下单' });
        if (typeof xhs.redirectTo === 'function') xhs.redirectTo({ url: '/pages/orders/index' });
        return;
      }
      return this.createOrder(this.orderPayload);
    }
    if (!this.data.quoteReady || !this.data.quoteToken) {
      if (!this.data.quoteLoading) this.refreshQuote();
      this.setData({ error: '正在更新优惠价格，请确认最新金额后提交' });
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
    const payload = this.buildOrderPayload();
    this.orderPayload = payload;
    this.orderRecoverySnapshot = this.captureOrderRecoverySnapshot(payload);
    this.invalidateMutableRequests();
    return this.createOrder(payload);
  },

  openPromotionOrder() {
    if (this.data.orderRecoveryPending) return;
    if (this.data.opportunityReservedOrderNo) xhs.navigateTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(this.data.opportunityReservedOrderNo)}` });
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
