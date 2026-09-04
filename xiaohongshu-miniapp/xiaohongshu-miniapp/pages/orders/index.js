const app = getApp();

Page({
  data: {
    allOrders: [],
    orders: [],
    activeStatus: 'all',
    statusCounts: { all: 0, unpaid: 0, paid: 0, closed: 0 },
    page: 1,
    total: 0,
    loading: true,
    loadingMore: false,
    loadingStatus: '',
    error: ''
  },

  onLoad() {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
    this.loadOrders(true);
  },
  onShow() { if (this.data.allOrders.length) this.loadOrders(true); },
  onPullDownRefresh() { this.loadOrders(true).finally(() => xhs.stopPullDownRefresh()); },
  onReachBottom() {
    if (!this.data.loading && !this.data.loadingMore && this.data.allOrders.length < this.data.total) this.loadOrders(false);
  },

  loadOrders(reset) {
    const page = reset ? 1 : this.data.page + 1;
    this.setData(reset ? { loading: true, error: '' } : { loadingMore: true, error: '' });
    return app.request(`/orders?page=${page}&page_size=40`).then(result => {
      const incoming = (result.items || []).map(order => ({
        ...order,
        isPackage: order.product_kind === 'scenic_hotel_package',
        amountText: this.formatPrice(order.amount_cents),
        statusText: this.statusText(order.status),
        statusClass: this.statusClass(order.status),
        createdText: this.formatDate(order.created_at)
      }));
      const allOrders = reset ? incoming : this.data.allOrders.concat(incoming);
      this.setData({ allOrders, page, total: Number(result.total || 0), loading: false, loadingMore: false }, () => this.applyStatus());
    }).catch(error => this.setData({ loading: false, loadingMore: false, error: error.message || '订单加载失败，请稍后重试' }));
  },

  selectStatus(event) {
    const activeStatus = event.currentTarget.dataset.status;
    this.setData({ activeStatus }, () => {
      if (activeStatus !== 'all' && this.data.total > this.data.allOrders.length) {
        this.setData({ loadingStatus: '正在加载全部订单...' });
        this.loadAllOrders().finally(() => this.setData({ loadingStatus: '' }, () => this.applyStatus()));
        return;
      }
      this.applyStatus();
    });
  },

  loadAllOrders() {
    if (this.loadAllPromise) return this.loadAllPromise;
    this.loadAllPromise = (async () => {
      while (this.data.allOrders.length < this.data.total) {
        const loadedBefore = this.data.allOrders.length;
        await this.loadOrders(false);
        if (this.data.allOrders.length <= loadedBefore) break;
      }
    })().finally(() => { this.loadAllPromise = null; });
    return this.loadAllPromise;
  },

  applyStatus() {
    const active = this.data.activeStatus;
    const complete = this.data.total === 0 || this.data.allOrders.length >= this.data.total;
    const statusCounts = { all: this.data.total || this.data.allOrders.length, unpaid: null, paid: null, closed: null };
    if (complete) {
      statusCounts.unpaid = 0;
      statusCounts.paid = 0;
      statusCounts.closed = 0;
      this.data.allOrders.forEach(order => {
        if (order.status === 'unpaid') statusCounts.unpaid += 1;
        else if (['paid', 'completed', 'partial_refunded'].indexOf(order.status) >= 0) statusCounts.paid += 1;
        else statusCounts.closed += 1;
      });
    }
    const orders = this.data.allOrders.filter(order => {
      if (active === 'all') return true;
      if (active === 'closed') return ['cancelled', 'failed', 'refunded'].indexOf(order.status) >= 0;
      if (active === 'paid') return ['paid', 'completed', 'partial_refunded'].indexOf(order.status) >= 0;
      return order.status === active;
    });
    this.setData({ orders, statusCounts });
  },

  openOrder(event) {
    xhs.navigateTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(event.currentTarget.dataset.no)}` });
  },

  goHome() { xhs.reLaunch({ url: '/pages/index/index' }); },
  goOrders() {},
  retry() { this.loadOrders(true); },

  formatPrice(cents) { return (Number(cents || 0) / 100).toFixed(2); },
  formatDate(value) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    const pad = number => String(number).padStart(2, '0');
    return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
  },
  statusText(status) {
    return { unpaid: '待支付', paid: '已支付', completed: '已使用', partial_refunded: '部分退款', cancelled: '已取消', failed: '未完成', refunded: '已退款' }[status] || '处理中';
  },
  statusClass(status) {
    if (['paid', 'completed', 'partial_refunded'].indexOf(status) >= 0) return 'paid';
    if (status === 'unpaid') return 'unpaid';
    return 'closed';
  }
});
