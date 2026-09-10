const app = getApp();
const { requestGuaranteeOrderPayment } = require('../../utils/payment');
const { renderTicketQRCodes } = require('../../utils/qr');

Page({
  data: {
    orderNo: '',
    storeName: '',
    orderDetailsExpanded: false,
    expandedTicket: null,
    expandedQRError: false,
    order: null,
    status: 'unpaid',
    statusLabel: '确认中',
    statusTitle: '正在确认支付结果',
    statusDetail: '请稍候，不要重复支付',
    ticketCodes: [],
    issuancePending: false,
    issuanceManualReview: false,
    qrRenderError: false,
    loading: true,
    paying: false,
    applyingRefund: false,
    refundApplicationMessage: '',
    refundUnavailableMessage: '',
    error: ''
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
    this.orderNo = options.order_no || '';
    this.setData({ orderNo: this.orderNo, storeName: app.globalData.storeName || '' });
    this.pollCount = 0;
    this.refundPollCount = 0;
    this.paymentInFlight = false;
    this.awaitingPaymentConfirmation = false;
    this.paymentFeedback = '';
    this.orderRequestVersion = 0;
    this.qrRenderVersion = 0;
    this.isPageVisible = true;
    this.hasActiveOrderRequest = false;
    this.loadOrder();
  },

  onShow() {
    this.isPageVisible = true;
    this.refundPollCount = 0;
    if (this.orderNo && !this.hasActiveOrderRequest) this.loadOrder();
  },
  onHide() {
    this.isPageVisible = false;
    this.hasActiveOrderRequest = false;
    this.stopPolling();
    this.orderRequestVersion = (this.orderRequestVersion || 0) + 1;
    this.qrRenderVersion = (this.qrRenderVersion || 0) + 1;
  },
  onPullDownRefresh() {
    this.refundPollCount = 0;
    return Promise.resolve(this.loadOrder()).finally(() => xhs.stopPullDownRefresh());
  },
  onUnload() {
    this.isPageVisible = false;
    this.hasActiveOrderRequest = false;
    this.stopPolling();
    this.orderRequestVersion = (this.orderRequestVersion || 0) + 1;
    this.qrRenderVersion = (this.qrRenderVersion || 0) + 1;
  },

  stopPolling() {
    this.pollTimerVersion = (this.pollTimerVersion || 0) + 1;
    if (this.timer !== null && this.timer !== undefined) clearTimeout(this.timer);
    this.timer = null;
  },

  schedulePoll(delay) {
    this.stopPolling();
    const timerVersion = (this.pollTimerVersion || 0) + 1;
    this.pollTimerVersion = timerVersion;
    this.timer = setTimeout(() => {
      if (timerVersion !== this.pollTimerVersion || this.isPageVisible === false) return;
      this.timer = null;
      this.loadOrder();
    }, delay);
  },

  loadOrder() {
    this.stopPolling();
    if (!this.orderNo) {
      this.setData({ loading: false, error: '订单编号无效' });
      return;
    }
    const requestVersion = (this.orderRequestVersion || 0) + 1;
    this.orderRequestVersion = requestVersion;
    this.hasActiveOrderRequest = true;
    this.qrRenderVersion = (this.qrRenderVersion || 0) + 1;
    const qrRenderVersion = this.qrRenderVersion;
    return app.request(`/orders/${encodeURIComponent(this.orderNo)}`).then(order => {
	  if (requestVersion !== this.orderRequestVersion) return;
	  this.hasActiveOrderRequest = false;
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
      const ticketCodes = order.refund_pending ? [] : this.usableTicketCodes(order.ticket_codes, coreStatus).map(ticket => {
        const usage = (order.tickets || []).find(item => item.code === ticket.code);
        const used = usage && (Number(usage.check_in_count) > 0 || usage.status === 'used');
        return { ...ticket, usageLabel: usage ? (used ? '已使用' : '未使用') : '使用状态待查询',
          usageDetail: used ? `已核验 ${Number(usage.check_in_count) || 1} 次 · 后续使用按票种规则核验` : '按所购票种规则使用',
          used: Boolean(used) };
      });
      const packageAwaitingBooking = order.isPackage && order.package_entitlements.some(item => item.status === 'pending_booking');
      const issuanceStatus = order.voucher_issuance_status || '';
      const issuancePending = coreStatus === 'paid' && !order.refund_pending && !packageAwaitingBooking && ticketCodes.length === 0 && (issuanceStatus === 'pending' || issuanceStatus === '');
      const issuanceManualReview = coreStatus === 'paid' && !order.refund_pending && !packageAwaitingBooking && issuanceStatus === 'manual_review';
	  const view = this.statusView(coreStatus, order.product_kind, Boolean(order.pay_token), issuancePending, issuanceManualReview);
      order.amountText = (Number(order.amount_cents || 0) / 100).toFixed(2);
	  order.discountText = (Number(order.discount_cents || 0) / 100).toFixed(2);
	  order.originalAmountText = (Number(order.original_amount_cents || order.amount_cents || 0) / 100).toFixed(2);
      const refundApplicationMessage = this.formatRefundApplicationStatus(order.refund_application_status);
      const awaitingPaymentConfirmation = this.awaitingPaymentConfirmation && coreStatus === 'unpaid' && this.pollCount < 15;
      if (coreStatus !== 'unpaid') {
        this.awaitingPaymentConfirmation = false;
        this.paymentFeedback = '';
      }
      this.setData({
        order,
		status: coreStatus,
        statusLabel: order.refund_pending ? '退款处理中' : view.label,
        statusTitle: order.refund_pending ? '退款处理中' : view.title,
        statusDetail: order.refund_pending ? '正在核实退款结果，期间票码暂停使用，请勿重复申请' : view.detail,
        ticketCodes,
        expandedTicket: null,
        expandedQRError: false,
        issuancePending,
        issuanceManualReview,
        qrRenderError: false,
        refundApplicationMessage,
        refundUnavailableMessage: !order.can_apply_refund && !refundApplicationMessage && ['paid', 'completed', 'partial_refunded'].indexOf(coreStatus) >= 0
          ? (order.refund_application_message || '退票资格暂未确认，请下拉刷新订单后重试；仍无法查询请联系商家') : '',
        loading: false,
        paying: this.paymentInFlight || awaitingPaymentConfirmation,
        error: this.paymentFeedback || ''
	  }, () => {
		if (requestVersion === this.orderRequestVersion && ticketCodes.length) {
		  renderTicketQRCodes(this, ticketCodes, qrRenderVersion, () => {
			if (requestVersion === this.orderRequestVersion && qrRenderVersion === this.qrRenderVersion) {
			  this.setData({ qrRenderError: true });
			}
		  });
		}
	  });
	  if (order.refund_pending && Number(this.refundPollCount || 0) < 48) {
        this.refundPollCount = Number(this.refundPollCount || 0) + 1;
        this.schedulePoll(2500);
      } else if (coreStatus === 'unpaid' && this.pollCount < 15) {
        this.pollCount += 1;
        this.schedulePoll(2000);
      }
	}).catch(error => {
	  if (requestVersion !== this.orderRequestVersion) return;
      this.hasActiveOrderRequest = false;
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
    this.refundPollCount = 0;
    this.paymentFeedback = '';
    this.setData({ loading: true, error: '' });
    this.loadOrder();
  },
  applyRefund() {
    const order = this.data.order;
    if (!order || !order.can_apply_refund || this.data.applyingRefund) return;
    this.setData({ applyingRefund: true, error: '' });
    xhs.showModal({
      title: '申请整单退票',
      content: `将退回本单全部 ${order.quantity} 张门票，金额 ¥${order.amountText}。系统确认未使用且符合退票规则后自动办理原路退款，处理中票码暂停使用。`,
      confirmText: '确认退票',
      success: result => {
        if (!result.confirm) { this.setData({ applyingRefund: false }); return; }
        if (!this.refundRequestId) this.refundRequestId = `refund-${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
        app.request(`/orders/${encodeURIComponent(this.orderNo)}/refund-applications`, {
          method: 'POST', data: { request_id: this.refundRequestId, reason: '游客主动申请整单退票' }
        }).then(result => {
          // Acknowledged application is not proof of a completed funds refund.
          this.refundRequestId = '';
          const refundStarted = result.status === 'processing' || result.status === 'completed';
          if (refundStarted) {
            this.qrRenderVersion = (this.qrRenderVersion || 0) + 1;
            this.refundPollCount = 0;
          }
          this.setData({ applyingRefund: false, order: { ...this.data.order, can_apply_refund: false,
            refund_pending: refundStarted || this.data.order.refund_pending,
            refund_application_status: result.status }, refundApplicationMessage: this.formatRefundApplicationStatus(result.status),
            ...(refundStarted ? { ticketCodes: [], expandedTicket: null, statusTitle: '退款处理中', statusLabel: '退款处理中',
              statusDetail: '正在核实退款结果，期间票码暂停使用，请勿重复申请' } : {}) });
          this.loadOrder();
        }).catch(error => this.setData({ applyingRefund: false, error: error.message || '退票申请提交失败，请重试' }));
      },
      fail: () => this.setData({ applyingRefund: false, error: '未能打开退票确认，请重试' })
    });
  },
  formatRefundApplicationStatus(status) {
    return {
      pending: '退票申请审核中；门票仍可使用，使用后将无法退票。',
      approved: '退票申请已通过，等待商家处理；使用后将无法退票。',
      processing: '已通过系统校验，正在原路退款；票码暂停使用。',
      rejected: '退票申请未通过，请联系商家了解原因。',
      failed: '退票申请处理未完成，请联系商家核查。',
      completed: '退票申请已处理，请以最新退款状态为准。'
    }[status] || '';
  },
  goOrders() { xhs.redirectTo({ url: '/pages/orders/index' }); },
  goHome() { xhs.reLaunch({ url: '/pages/index/index' }); },
  toggleOrderDetails() { this.setData({ orderDetailsExpanded: !this.data.orderDetailsExpanded }); },
  showTicketQR(event) {
    const canvasId = event.currentTarget.dataset.canvasId;
    const ticket = this.data.ticketCodes.find(item => item.canvasId === canvasId);
    if (!ticket) return;
    this.setData({ expandedTicket: ticket, expandedQRError: false }, () => {
      if (!this.data.expandedTicket || this.data.expandedTicket.canvasId !== canvasId) return;
      renderTicketQRCodes(this, [{ ...ticket, canvasId: 'ticket-qr-expanded' }], this.qrRenderVersion, () => {
        if (this.data.expandedTicket && this.data.expandedTicket.canvasId === canvasId) this.setData({ expandedQRError: true });
      });
    });
  },
  closeTicketQR() { this.setData({ expandedTicket: null, expandedQRError: false }); },
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

  usableTicketCodes(codes, status) {
	// The server exposes only issued, non-refunded ticket codes. A completed
	// order can still have remaining checkpoint rights, so completion alone
	// must not hide its shared ticket code.
	if (['paid', 'completed', 'partial_refunded'].indexOf(status) < 0 || !Array.isArray(codes)) return [];
	return codes
	  .filter(code => typeof code === 'string' && code.length > 0)
	  .map((code, index) => ({ code, index: index + 1, canvasId: `ticket-qr-${index + 1}` }));
  },

  statusView(status, productKind, hasPayToken, issuancePending, issuanceManualReview) {
    const isPackage = productKind === 'scenic_hotel_package';
    if (status === 'unpaid' && !hasPayToken) return { label: '确认中', title: '正在准备支付', detail: '支付信息正在确认，请稍候再试' };
    if (status === 'unpaid') return { label: '待支付', title: '订单待支付', detail: '完成支付后出票，无需重复下单' };
    if (status === 'paid' && issuanceManualReview) return { label: '已支付', title: '支付成功，出票待处理', detail: '票码正在人工核查，请联系商家确认' };
    if (status === 'paid' && issuancePending) {
	  return { label: '已支付', title: '支付成功，正在出票', detail: '正在生成可核验票码，请稍后重新查询' };
	}
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
