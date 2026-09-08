const app = getApp();
const calendar = require('../../utils/calendar');

Page({
  data: {
    orderNo: '',
    entitlementNo: '',
    entitlement: null,
    checkInDate: '',
    checkOutDate: '',
    minDate: '',
    maxDate: '',
    dateChips: [],
    calendarOpen: true,
    calendarTitle: '',
    calendarCells: [],
    canPreviousMonth: false,
    canNextMonth: false,
    guestName: '',
    contactPhone: '',
    loading: true,
    submitting: false,
    error: ''
  },

  onLoad(options) {
    app.setNavigationTitle(app.globalData.storeName || '官方商城');
    this.orderNo = options.order_no || '';
    this.entitlementNo = options.entitlement_no || '';
    this.bookingRequestId = `${Date.now()}-${Math.random().toString(36).slice(2, 12)}`;
    this.setData({ orderNo: this.orderNo, entitlementNo: this.entitlementNo });
    this.loadOrder();
  },

  loadOrder() {
    this.setData({ loading: true, error: '' });
    return app.request(`/orders/${encodeURIComponent(this.orderNo)}`).then(order => {
      const entitlement = (order.package_entitlements || []).find(item => item.entitlement_no === this.entitlementNo);
      if (!entitlement || entitlement.status !== 'pending_booking') throw new Error('该套餐当前不可预约');
      const today = new Date();
      const validFrom = calendar.parseDate(entitlement.valid_from);
      const validUntil = calendar.parseDate(entitlement.valid_until);
      if (!validFrom || !validUntil) throw new Error('预约权益日期无效，请联系商家处理');
      const advanceDate = calendar.addDays(today, Math.max(0, Number(entitlement.min_advance_days || 0)));
      const minDate = calendar.compareDates(validFrom, advanceDate) > 0 ? validFrom : advanceDate;
      const nights = Math.max(1, Number(entitlement.nights || 1));
      const maxDate = calendar.addDays(validUntil, -(nights - 1));
      if (calendar.compareDates(minDate, maxDate) > 0) throw new Error('当前权益没有可预约日期，请联系商家处理');
      this.calendarMonth = calendar.monthStart(minDate);
      this.setData({
        entitlement,
        minDate: calendar.formatDate(minDate),
        maxDate: calendar.formatDate(maxDate),
        loading: false,
        error: ''
      }, () => this.refreshCalendar());
    }).catch(error => this.setData({ loading: false, error: error.message || '预约信息加载失败' }));
  },

  retry() { return this.loadOrder(); },
  goOrder() { xhs.redirectTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(this.orderNo)}` }); },
  onPullDownRefresh() {
    if (this.data.submitting) return xhs.stopPullDownRefresh();
    this.loadOrder().finally(() => xhs.stopPullDownRefresh());
  },

  selectDate(event) {
    if (this.data.submitting) return;
    const checkInDate = event.currentTarget.dataset.date;
    if (!calendar.isDateWithin(checkInDate, this.data.minDate, this.data.maxDate)) return;
    const nights = Math.max(1, Number(this.data.entitlement.nights || 1));
    this.setData({ checkInDate, checkOutDate: calendar.formatDate(calendar.addDays(calendar.parseDate(checkInDate), nights)), error: '' });
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
  onGuestNameInput(event) { this.setData({ guestName: event.detail.value || '', error: '' }); },
  onContactPhoneInput(event) { this.setData({ contactPhone: event.detail.value || '', error: '' }); },

  submit() {
    if (this.data.submitting) return;
    if (!calendar.isDateWithin(this.data.checkInDate, this.data.minDate, this.data.maxDate)) return this.setData({ error: '请选择可预约的入住日期' });
    if (!this.data.guestName.trim()) return this.setData({ error: '请填写入住人姓名' });
    if (!/^[0-9+\-\s]{6,20}$/.test(this.data.contactPhone.trim())) return this.setData({ error: '请填写有效的联系电话' });
    this.setData({ submitting: true, error: '' });
    app.request(`/orders/${encodeURIComponent(this.orderNo)}/package-bookings`, {
      method: 'POST',
      data: {
        entitlement_no: this.entitlementNo,
        check_in_date: this.data.checkInDate,
        guest_name: this.data.guestName.trim(),
        contact_phone: this.data.contactPhone.trim(),
        request_id: this.bookingRequestId
      }
    }).then(() => xhs.redirectTo({ url: `/pages/order/detail?order_no=${encodeURIComponent(this.orderNo)}` }))
      .catch(error => this.setData({ submitting: false, error: error.message || '预约失败，请稍后重试' }));
  },

  refreshCalendar() {
    const today = new Date();
    const month = this.calendarMonth || calendar.monthStart(today);
    this.setData({
      dateChips: calendar.buildDateChips(today, this.data.minDate, this.data.maxDate, this.data.checkInDate),
      calendarTitle: calendar.monthTitle(month),
      calendarCells: calendar.buildCalendarCells(month, this.data.minDate, this.data.maxDate, this.data.checkInDate),
      canPreviousMonth: calendar.canMoveMonth(month, -1, this.data.minDate, this.data.maxDate),
      canNextMonth: calendar.canMoveMonth(month, 1, this.data.minDate, this.data.maxDate)
    });
  }
});
