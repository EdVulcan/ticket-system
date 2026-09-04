const app = getApp();

Page({
  data: {
    orderNo: '',
    entitlementNo: '',
    entitlement: null,
    checkInDate: '',
    minDate: '',
    maxDate: '',
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
    app.request(`/orders/${encodeURIComponent(this.orderNo)}`).then(order => {
      const entitlement = (order.package_entitlements || []).find(item => item.entitlement_no === this.entitlementNo);
      if (!entitlement || entitlement.status !== 'pending_booking') throw new Error('该套餐当前不可预约');
      const today = new Date();
      const validFrom = new Date(entitlement.valid_from);
      const validUntil = new Date(entitlement.valid_until);
      const advanceDate = new Date(today.getFullYear(), today.getMonth(), today.getDate() + Number(entitlement.min_advance_days || 0));
      const minDate = validFrom > advanceDate ? validFrom : advanceDate;
      const maxDate = new Date(validUntil);
      maxDate.setDate(maxDate.getDate() - Math.max(0, Number(entitlement.nights || 1) - 1));
      this.setData({
        entitlement,
        minDate: this.formatDate(minDate),
        maxDate: this.formatDate(maxDate),
        loading: false,
        error: ''
      });
    }).catch(error => this.setData({ loading: false, error: error.message || '预约信息加载失败' }));
  },

  onDateChange(event) { this.setData({ checkInDate: event.detail.value || '', error: '' }); },
  onGuestNameInput(event) { this.setData({ guestName: event.detail.value || '', error: '' }); },
  onContactPhoneInput(event) { this.setData({ contactPhone: event.detail.value || '', error: '' }); },

  submit() {
    if (this.data.submitting) return;
    if (!this.data.checkInDate) return this.setData({ error: '请选择入住日期' });
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

  formatDate(date) {
    const value = new Date(date);
    const year = value.getFullYear();
    const month = String(value.getMonth() + 1).padStart(2, '0');
    const day = String(value.getDate()).padStart(2, '0');
    return `${year}-${month}-${day}`;
  }
});
