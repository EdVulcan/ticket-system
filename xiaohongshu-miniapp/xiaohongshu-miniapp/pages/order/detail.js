const app = getApp();

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
    this.orderNo = options.order_no || '';
    this.setData({ orderNo: this.orderNo });
    this.pollCount = 0;
    this.loadOrder();
  },

  onShow() { if (this.orderNo && !this.data.loading) this.loadOrder(); },
  onUnload() { if (this.timer) clearTimeout(this.timer); },

  loadOrder() {
    if (!this.orderNo) {
      this.setData({ loading: false, error: '订单编号无效' });
      return;
    }
    app.request(`/orders/${encodeURIComponent(this.orderNo)}`).then(order => {
      const view = this.statusView(order.status);
      order.amountText = (Number(order.amount_cents || 0) / 100).toFixed(2);
      this.setData({
        order,
        status: order.status,
        statusLabel: view.label,
        statusTitle: view.title,
        statusDetail: view.detail,
        ticketCodes: (order.ticket_codes || []).map((code, index) => ({ code, index: index + 1 })),
        loading: false,
        paying: false,
        error: ''
      });
      if (order.status === 'unpaid' && this.pollCount < 15) {
        this.pollCount += 1;
        this.timer = setTimeout(() => this.loadOrder(), 2000);
      }
    }).catch(error => this.setData({ loading: false, paying: false, error: error.message || '订单查询失败，请稍后重试' }));
  },

  continuePayment() {
    const order = this.data.order;
    if (!order || !order.order_id || !order.pay_token || this.data.paying) return;
    this.setData({ paying: true, error: '' });
    xhs.requestGuaranteeOrderPayment({
      orderInfo: { payToken: order.pay_token, orderId: order.order_id },
      complete: () => {
        this.pollCount = 0;
        this.loadOrder();
      }
    });
  },

  retry() { this.pollCount = 0; this.setData({ loading: true, error: '' }); this.loadOrder(); },
  goOrders() { xhs.redirectTo({ url: '/pages/orders/index' }); },
  goHome() { xhs.reLaunch({ url: '/pages/index/index' }); },

  statusView(status) {
    if (status === 'paid') return { label: '已支付', title: '支付成功', detail: '门票已经出票，请妥善保管票码' };
    if (status === 'completed') return { label: '已使用', title: '门票已使用', detail: '本次入园记录已经完成' };
    if (status === 'partial_refunded') return { label: '部分退款', title: '订单部分退款', detail: '剩余有效票码仍可按规则使用' };
    if (status === 'cancelled' || status === 'failed') return { label: '已关闭', title: '订单未完成', detail: '本次订单已关闭，未生成有效门票' };
    if (status === 'refunded') return { label: '已退款', title: '退款完成', detail: '款项将按小红书规则原路退回' };
    return { label: '确认中', title: '正在确认支付结果', detail: '系统正在向小红书核实，请不要重复支付' };
  }
});
