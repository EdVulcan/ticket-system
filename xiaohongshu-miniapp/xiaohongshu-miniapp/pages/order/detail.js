const app = getApp();

Page({
  data: { orderNo: '', status: 'unpaid', statusMark: '…', statusTitle: '正在确认支付结果', statusDetail: '请稍候，不要重复支付', ticketCodes: [], loading: true, error: '' },

  onLoad(options) {
    this.orderNo = options.order_no || '';
    this.setData({ orderNo: this.orderNo });
    this.pollCount = 0;
    this.loadOrder();
  },

  onShow() {
    if (this.orderNo && !this.data.loading) this.loadOrder();
  },

  onUnload() {
    if (this.timer) clearTimeout(this.timer);
  },

  loadOrder() {
    if (!this.orderNo) { this.setData({ loading: false, error: '订单编号无效' }); return; }
    app.request(`/orders/${encodeURIComponent(this.orderNo)}`).then(order => {
      const view = this.statusView(order.status);
      this.setData({ status: order.status, statusMark: view.mark, statusTitle: view.title, statusDetail: view.detail, ticketCodes: order.ticket_codes || [], loading: false, error: '' });
      if (order.status === 'unpaid' && this.pollCount < 15) {
        this.pollCount += 1;
        this.timer = setTimeout(() => this.loadOrder(), 2000);
      }
    }).catch(error => {
      this.setData({ loading: false, error: error.message || '订单查询失败，请稍后重试' });
    });
  },

  retry() {
    this.pollCount = 0;
    this.setData({ loading: true, error: '' });
    this.loadOrder();
  },

  statusView(status) {
    if (status === 'paid') return { mark: '✓', title: '支付成功', detail: '门票已出票，请妥善保管票码' };
    if (status === 'cancelled' || status === 'failed') return { mark: '×', title: '订单未完成', detail: '本次订单已关闭，未生成有效门票' };
    return { mark: '…', title: '正在确认支付结果', detail: '系统正在向小红书核实，请不要重复支付' };
  }
});
