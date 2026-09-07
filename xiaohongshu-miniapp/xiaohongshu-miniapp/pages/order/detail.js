const app = getApp();
const { requestGuaranteeOrderPayment } = require('../../utils/payment');

Page({
  data: {
    orderNo: '',
    order: null,
    status: 'unpaid',
    statusLabel: '确认中',
    statusTitle: '正在确认支付结果',
    statusDetail: '请稍候，不要重复支付',
    ticketCodes: [],
    loading: true,
    paying: false,
    error: ''
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
    this.orderNo = options.order_no || '';
    this.setData({ orderNo: this.orderNo });
    this.pollCount = 0;
    this.paymentInFlight = false;
    this.awaitingPaymentConfirmation = false;
    this.paymentFeedback = '';
    this.loadOrder();
  },

  onShow() { if (this.orderNo && !this.data.loading) this.loadOrder(); },
  onUnload() { if (this.timer) clearTimeout(this.timer); },

  loadOrder() {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    if (!this.orderNo) {
      this.setData({ loading: false, error: '订单编号无效' });
      return;
    }
    app.request(`/orders/${encodeURIComponent(this.orderNo)}`).then(order => {
	  const coreStatus = order.core_order_status || order.status;
	  const canUsePaidEntitlements = ['paid', 'partial_refunded'].indexOf(coreStatus) >= 0;
      order.isPackage = order.product_kind === 'scenic_hotel_package';
      if (order.hotel_stay) {
        order.hotel_stay.checkInText = this.formatDay(order.hotel_stay.check_in_date);
        order.hotel_stay.checkOutText = this.formatDay(order.hotel_stay.check_out_date);
        order.hotel_stay.contactPhoneText = this.maskPhone(order.hotel_stay.contact_phone);
      }
      order.package_entitlements = (order.package_entitlements || []).map((item, index) => ({
        ...item,
        index: index + 1,
        validUntilText: this.formatDay(item.valid_until),
        checkInText: this.formatDay(item.check_in_date),
        checkOutText: this.formatDay(item.check_out_date),
        phoneText: this.maskPhone(item.contact_phone),
		statusLabel: this.entitlementStatusLabel(item.status),
		canBook: canUsePaidEntitlements && item.status === 'pending_booking',
        canCancel: item.status === 'booked' && Number(item.reschedule_count || 0) < Number(item.max_reschedules || 0)
      }));
	  const view = this.statusView(coreStatus, order.product_kind, Boolean(order.pay_token));
      order.amountText = (Number(order.amount_cents || 0) / 100).toFixed(2);
      const awaitingPaymentConfirmation = this.awaitingPaymentConfirmation && coreStatus === 'unpaid' && this.pollCount < 15;
      if (coreStatus !== 'unpaid') {
        this.awaitingPaymentConfirmation = false;
        this.paymentFeedback = '';
      }
      this.setData({
        order,
		status: coreStatus,
        statusLabel: view.label,
        statusTitle: view.title,
        statusDetail: view.detail,
        ticketCodes: (order.ticket_codes || []).map((code, index) => ({ code, index: index + 1 })),
        loading: false,
        paying: this.paymentInFlight || awaitingPaymentConfirmation,
        error: this.paymentFeedback || ''
      });
	  if (coreStatus === 'unpaid' && this.pollCount < 15) {
        this.pollCount += 1;
        this.timer = setTimeout(() => this.loadOrder(), 2000);
      }
    }).catch(error => {
      if (!this.paymentInFlight) {
        this.awaitingPaymentConfirmation = false;
        this.paymentFeedback = '';
      }
      this.setData({
        loading: false,
        paying: this.paymentInFlight,
        error: error.message || '订单查询失败，请稍后重试'
      });
    });
  },

  continuePayment() {
    const order = this.data.order;
    if (this.data.paying) return;
    if (!order || !order.order_id || !order.pay_token) {
      requestGuaranteeOrderPayment(xhs, order, {
        onFailure: result => {
          this.paymentFeedback = result.message;
          this.setData({ paying: false, error: result.message });
        }
      });
      return;
    }
    this.paymentFeedback = '';
    this.paymentInFlight = true;
    this.setData({ paying: true, error: '' });
    requestGuaranteeOrderPayment(xhs, order, {
      onSuccess: () => {
        this.paymentInFlight = false;
        this.awaitingPaymentConfirmation = true;
        this.paymentFeedback = '支付请求已完成，正在核实订单状态，请稍候';
        this.pollCount = 0;
        this.loadOrder();
      },
      onFailure: result => {
        this.paymentInFlight = false;
        this.awaitingPaymentConfirmation = false;
        this.paymentFeedback = result.message;
        this.setData({ paying: false, error: result.message });
      },
      onComplete: result => {
        if (result.outcome === 'failure') return;
        if (result.outcome === 'unknown') {
          this.paymentInFlight = false;
          this.awaitingPaymentConfirmation = true;
          this.paymentFeedback = '支付结果正在确认，订单已保留，请稍候';
          this.pollCount = 0;
          this.loadOrder();
        }
      }
    });
  },

  retry() {
    this.pollCount = 0;
    this.paymentFeedback = '';
    this.setData({ loading: true, error: '' });
    this.loadOrder();
  },
  goOrders() { xhs.redirectTo({ url: '/pages/orders/index' }); },
  goHome() { xhs.reLaunch({ url: '/pages/index/index' }); },
  bookPackage(event) {
    const entitlementNo = event.currentTarget.dataset.entitlement;
    xhs.navigateTo({ url: `/pages/booking/index?order_no=${encodeURIComponent(this.orderNo)}&entitlement_no=${encodeURIComponent(entitlementNo)}` });
  },
  cancelPackage(event) {
    const entitlementNo = event.currentTarget.dataset.entitlement;
    xhs.showModal({
      title: '取消本次预约',
      content: '取消后将释放当前日期的门票和房量，可在规则允许次数内重新预约。',
      success: result => {
        if (!result.confirm) return;
        this.setData({ loading: true, error: '' });
        app.request(`/orders/${encodeURIComponent(this.orderNo)}/package-bookings/${encodeURIComponent(entitlementNo)}/cancel`, { method: 'POST' })
          .then(() => this.loadOrder())
          .catch(error => this.setData({ loading: false, error: error.message || '取消预约失败，请稍后重试' }));
      }
    });
  },

  statusView(status, productKind, hasPayToken) {
    const isPackage = productKind === 'scenic_hotel_package';
    if (status === 'unpaid' && !hasPayToken) return { label: '确认中', title: '正在准备支付', detail: '支付信息正在确认，请稍候再试' };
    if (status === 'unpaid') return { label: '待支付', title: '订单待支付', detail: '完成支付后出票，无需重复下单' };
    if (status === 'paid') return { label: '已支付', title: '支付成功', detail: isPackage ? '请在下方查看或完成每份套餐的入住预约' : '门票已经出票，请妥善保管票码' };
    if (status === 'completed') return { label: '已使用', title: '订单已使用', detail: '具体使用记录以实际核销结果为准' };
    if (status === 'partial_refunded') return { label: '部分退款', title: '订单部分退款', detail: '未退款的权益按原订单规则使用，票码能否继续使用以核销结果为准' };
    if (status === 'cancelled' || status === 'failed') return { label: '已关闭', title: '订单未完成', detail: '本次订单已关闭，请勿使用本订单凭证' };
    if (status === 'refunded') return { label: '已退款', title: '退款完成', detail: '款项将按小红书规则原路退回' };
    return { label: '确认中', title: '正在确认支付结果', detail: '系统正在向小红书核实，请不要重复支付' };
  },

	entitlementStatusLabel(status) {
	  return {
		pending_booking: '待预约', booking_pending: '预约处理中', booked: '已预约',
		cancel_pending: '取消处理中', refunded: '已退款', expired: '已过期', cancelled: '已关闭'
	  }[status] || '处理中';
	},

  formatDay(value) {
    if (!value) return '';
    return String(value).slice(0, 10);
  },

  maskPhone(value) {
    const phone = String(value || '');
    return phone.length === 11 ? `${phone.slice(0, 3)}****${phone.slice(7)}` : phone;
  }
});
