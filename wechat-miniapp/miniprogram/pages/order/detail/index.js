const storage = require('../../../services/storage');
const format = require('../../../utils/format');
const api = require('../../../services/api');
const paymentFlow = require('../../../services/payment');
const commerce = require('../../../config/commerce');

function fulfillmentStatusOf(order) {
  const direct = order && (order.fulfillmentStatus || order.fulfillment_status);
  if (direct) return String(direct).toLowerCase();
  const nested = order && (commerce.fulfillmentOf(order) === commerce.FULFILLMENT.COURIER ? order.retailFulfillment || order.retail_fulfillment : order.restaurantFulfillment || order.restaurant_fulfillment);
  return String(nested && (nested.status || nested.fulfillmentStatus || nested.fulfillment_status) || '').toLowerCase();
}

function fulfillmentCompletedOf(order) {
  return Boolean(order && (order.fulfillmentCompleted === true || (fulfillmentStatusOf(order) ? fulfillmentStatusOf(order) === 'completed' : String(order.status || '').toUpperCase() === 'COMPLETED')));
}

function serverRefundDecision(order) {
  const actions = order && (order.availableActions || order.available_actions);
  if (!actions) return false;
  if (typeof actions.canRefund === 'boolean') return actions.canRefund;
  if (typeof actions.can_refund === 'boolean') return actions.can_refund;
  return false;
}

function canRequestRefund(order) {
  if (!order || !api.isProduction()) return false;
  return serverRefundDecision(order);
}

function actionAllowed(order, name) {
  const actions = order && (order.availableActions || order.available_actions);
  if (!actions) return false;
  return Boolean(actions[name] !== undefined ? actions[name] : actions[name.replace(/[A-Z]/g, letter => `_${letter.toLowerCase()}`)]);
}

function addressText(address) {
  if (!address) return '';
  if (address.addressType === 'CAMPUS') return [address.campusName, address.zoneName, address.building, address.room, address.detailAddress].filter(Boolean).join(' · ');
  return [address.province, address.city, address.district, address.detailAddress].filter(Boolean).join(' ');
}

function refundMessage(status, order) {
  const normalizedStatus = String(status || '').toUpperCase();
  if (normalizedStatus === 'REFUNDING') {
    const afterSale = order && (order.afterSale || order.after_sale);
    const afterSaleStatus = String(order && (order.afterSaleStatus || order.after_sale_status) || afterSale && (afterSale.status || afterSale.state) || '').toUpperCase();
    const refundStatus = String(order && (order.refundStatus || order.refund_status) || '').toUpperCase();
    if (afterSaleStatus === 'APPROVED' && refundStatus === 'REQUESTED') return '商家已审核通过，正在安排退款';
    if (afterSaleStatus === 'PROCESSING' || ['PROCESSING', 'REFUNDING'].indexOf(refundStatus) >= 0) return '退款申请处理中，等待支付渠道确认';
  }
  switch (normalizedStatus) {
    case 'AFTER_SALE_REVIEW': return '售后申请已提交，等待商家审核';
    case 'REFUNDING': return '退款申请已提交，正在等待支付渠道确认';
    case 'REFUNDED': return '退款已完成，款项将原路退回';
    case 'REFUND_FAILED': return '退款未完成，请刷新订单状态或联系门店';
    case 'REFUND_REVIEW': return '退款状态需要门店核查，请勿重复提交';
    default: return '';
  }
}

Page({
  data: { order: null, paying: false, actionSubmitting: false, refundSubmitting: false, refundQuerying: false, statusIcon: '✓', statusDescription: '', refundMessage: '', fulfillmentTitle: '', goodsAmountText: '0.00', deliveryFeeText: '0.00', shippingFeeText: '0.00', discountText: '0.00', payableText: '0.00', isCourier: false, canCancelOrder: false, canConfirmReceipt: false, canRequestRefund: false },

  onLoad(options) { this.orderId = options.id; },
  onShow() { if (this.orderId && !this.data.paying) this.loadOrder(true); },
  refreshOrder() { this.loadOrder(true); },

  loadOrder(reconcile = false) {
    if (api.isProduction()) {
      const query = reconcile ? api.queryPayment(this.orderId).catch(() => null) : Promise.resolve();
      return query.then(() => api.getOrder(this.orderId)).then((result) => {
        if (!result.data) return;
        this.renderOrder(result.data);
      }).catch((error) => { console.error('load order failed', error); wx.showToast({ title: '订单加载失败', icon: 'none' }); });
    }
    const raw = storage.getOrders().find((item) => item.id === this.orderId);
    if (raw) {
      this.renderOrder(raw);
    }
  },

  renderOrder(raw) {
    raw = typeof api.normalizeOrder === 'function' ? api.normalizeOrder(raw) : raw;
    const isCourier = commerce.fulfillmentOf(raw) === commerce.FULFILLMENT.COURIER;
    const fulfillmentCompleted = fulfillmentCompletedOf(raw);
    const description = isCourier ? { WAIT_PAY: '请在有效期内完成支付', PAID: '门店已收到订单，等待打包发货', SHIPPED: '商家已发货，收到商品后请确认收货', DELIVERED: '商家已发货，收到商品后请确认收货', COMPLETED: '已确认收货，订单已完成', CANCELED: '订单已取消', AFTER_SALE_REVIEW: '售后申请已提交，等待商家审核', REFUNDING: '退款申请处理中', REFUND_FAILED: '退款未完成，请刷新订单状态或联系门店', REFUND_REVIEW: '退款状态需要门店核查，请勿重复提交', REFUNDED: '款项已原路退回' } : { WAIT_PAY: '请在有效期内完成支付', PAID: '门店已收到订单，等待制作', ACCEPTED: '商家已接单，等待开始制作', PREPARING: '餐食正在制作中', READY: raw.deliveryMethod === 'PICKUP' ? '餐食已备好，请到店取餐' : '餐食已备好，等待安排配送', DELIVERING: '餐食已出发，正在配送途中', COMPLETED: '订单已完成', CANCELED: '订单已取消', AFTER_SALE_REVIEW: '售后申请已提交，等待商家审核', REFUNDING: '退款申请处理中', REFUND_FAILED: '退款未完成，请刷新订单状态或联系门店', REFUND_REVIEW: '退款状态需要门店核查，请勿重复提交', REFUNDED: '款项已原路退回' };
    const statusIcon = raw.status === 'CANCELED' || raw.status === 'REFUNDED' ? '·' : fulfillmentCompleted ? '✓' : raw.status === 'DELIVERING' || raw.status === 'SHIPPED' || raw.status === 'DELIVERED' ? '↗' : '…';
    const order = Object.assign({}, raw, { id: raw.id || raw._id, retailDispatched: isCourier && (Boolean(raw.shippedAt) || ['shipped', 'in_transit', 'delivered', 'completed'].includes(fulfillmentStatusOf(raw)) || ['SHIPPED', 'DELIVERED', 'COMPLETED'].includes(raw.status)), fulfillmentCompleted, timelineStatus: fulfillmentCompleted ? 'COMPLETED' : raw.status, addressText: addressText(raw.addressSnapshot), statusText: isCourier && raw.status === 'DELIVERED' ? '待确认收货' : format.orderStatus(raw.status, raw.fulfillmentType, raw.deliveryMethod), createdText: format.dateTime(raw.createdAt), readyText: raw.readyAt ? format.dateTime(raw.readyAt) : '', preparingText: raw.preparingAt ? format.dateTime(raw.preparingAt) : '', deliveringText: raw.deliveringAt ? format.dateTime(raw.deliveringAt) : '', deliveredText: raw.deliveredAt ? format.dateTime(raw.deliveredAt) : '', shippedText: raw.shippedAt ? format.dateTime(raw.shippedAt) : '', completedText: raw.completedAt ? format.dateTime(raw.completedAt) : '', itemsSnapshot: (raw.itemsSnapshot || []).map((item, index) => Object.assign({}, item, { lineId: item.lineId || `${item.productId}_${index}`, subtotalText: format.yuan(item.subtotal), optionsText: (item.selectedOptions || []).map((option) => typeof option === 'string' ? option : option.name).join(' · ') })) });
    const canCancelOrder = api.isProduction() ? actionAllowed(raw, 'canCancelOrder') : raw.status === 'WAIT_PAY';
    const canConfirmReceipt = api.isProduction() ? actionAllowed(raw, 'canConfirmReceipt') : (isCourier ? ['SHIPPED', 'DELIVERED'].includes(raw.status) : raw.status === 'DELIVERING');
    this.setData({ order, isCourier, fulfillmentTitle: isCourier ? '零售配送' : raw.deliveryMethod === 'PICKUP' ? '餐饮自取' : '餐饮配送', canCancelOrder, canConfirmReceipt, canRequestRefund: canRequestRefund(raw), statusIcon, statusDescription: raw.paymentClosing ? '正在核实取消和支付结果，请刷新或联系门店' : description[raw.status] || '', refundMessage: refundMessage(raw.status, raw), goodsAmountText: format.yuan(raw.goodsAmount), deliveryFeeText: format.yuan(raw.deliveryFee), shippingFeeText: format.yuan(raw.shippingFee), discountText: format.yuan(raw.discountAmount), payableText: format.yuan(raw.payableAmount) });
  },

  async payOrder() {
    if (this.data.paying || !this.orderId) return;
    if (!api.isProduction()) { wx.showToast({ title: '演示订单无需再次支付', icon: 'none' }); return; }
    this.setData({ paying: true });
    try {
      const result = await paymentFlow.payOrder(this.orderId);
      wx.showToast({ title: paymentFlow.isSettled(result.status) ? '订单已支付' : result.status === 'CANCELED' ? '订单已取消' : '支付结果确认中', icon: 'none' });
    } catch (error) {
      console.error('retry payment failed', error);
      wx.showToast({ title: '未确认支付结果，请刷新订单', icon: 'none' });
    } finally {
      this.setData({ paying: false });
      this.loadOrder(true);
    }
  },

  updateOrder(status) {
    if (this.data.actionSubmitting) return;
    if (status === 'CANCELED' && !this.data.canCancelOrder) { wx.showToast({ title: '当前订单不可取消', icon: 'none' }); return; }
    if (status === 'COMPLETED' && !this.data.canConfirmReceipt) { wx.showToast({ title: '当前订单暂不可确认收货', icon: 'none' }); return; }
    if (api.isProduction()) {
      this.setData({ actionSubmitting: true });
      const request = status === 'COMPLETED' ? api.confirmReceipt(this.orderId) : api.cancelOrder(this.orderId);
      request.then((result) => { this.loadOrder(true); wx.showToast({ title: status === 'COMPLETED' ? '已确认收货' : result.status === 'CANCELED' ? '订单已取消' : '订单已支付，无法取消', icon: 'none' }); }).catch((error) => { console.error('update order failed', error); wx.showToast({ title: '订单待核实，请刷新后重试', icon: 'none' }); }).finally(() => this.setData({ actionSubmitting: false }));
      return;
    }
    const orders = storage.getOrders();
    const index = orders.findIndex((item) => item.id === this.orderId);
    if (index < 0) return;
    orders[index].status = status;
    if (status === 'COMPLETED') orders[index].fulfillmentStatus = 'completed';
    orders[index].updatedAt = new Date().toISOString();
    if (status === 'COMPLETED') orders[index].completedAt = orders[index].updatedAt;
    storage.saveOrders(orders);
    this.loadOrder();
  },

  cancelOrder() { if (!this.data.canCancelOrder) return; wx.showModal({ title: '取消订单', content: '确定取消这个订单吗？', success: (res) => { if (res.confirm) this.updateOrder('CANCELED'); } }); },
  confirmReceipt() {
    if (!this.data.canConfirmReceipt || this.data.actionSubmitting || this.receiptPromptOpen) return;
    this.receiptPromptOpen = true;
    wx.showModal({
      title: '确认收货', content: '请确认已收到商品，确认后订单将完成。', confirmText: '已收到商品',
      success: result => { if (result.confirm) this.updateOrder('COMPLETED'); },
      complete: () => { this.receiptPromptOpen = false; }
    });
  },
  copyTrackingNo() {
    const shipping = this.data.order && this.data.order.shipping;
    const trackingNo = String(shipping && shipping.trackingNo || '').trim();
    if (!this.data.isCourier || !trackingNo) return;
    wx.setClipboardData({ data: trackingNo, fail: () => wx.showToast({ title: '复制失败，请重试', icon: 'none' }) });
  },
  contactStore() { const phoneNumber = storage.getStore().phone; if (phoneNumber) wx.makePhoneCall({ phoneNumber }); else wx.showToast({ title: '门店暂未配置联系电话', icon: 'none' }); },
  callRunner() {
    const delivery = this.data.order && this.data.order.delivery;
    const phoneNumber = delivery && delivery.contactPhone;
    if (phoneNumber) wx.makePhoneCall({ phoneNumber });
  },

  refundOrder() {
    if (!this.data.canRequestRefund || this.data.refundSubmitting || this.data.refundQuerying) return;
    const reasons = ['商品不需要了', '下单有误', '其他原因'];
    const isCompleted = fulfillmentCompletedOf(this.data.order);
    const confirm = reason => wx.showModal({ title: isCompleted ? '申请售后' : '申请退款', content: isCompleted ? '订单已完成，将提交售后申请，商家审核后再处理退款。是否继续？' : '将按原支付方式申请整单退款，是否继续？', confirmText: '确认申请', success: result => { if (result.confirm) this.submitRefund(reason); } });
    if (typeof wx.showActionSheet === 'function') wx.showActionSheet({ itemList: reasons, success: result => confirm(reasons[result.tapIndex] || '客户申请退款') });
    else confirm('客户申请退款');
  },

  submitRefund(reason) {
    if (!this.data.canRequestRefund || this.data.refundSubmitting || !this.orderId || typeof api.createRefund !== 'function') return;
    const submittedForAfterSale = fulfillmentCompletedOf(this.data.order);
    const idempotencyKey = `refund_${this.orderId}_initial`;
    this.setData({ refundSubmitting: true, refundMessage: '退款申请提交中' });
    return api.createRefund(this.orderId, { idempotencyKey, clientRequestId: idempotencyKey, reason: reason || '客户申请退款' }).then(result => {
      if (result && result.order) this.renderOrder(result.order);
      else this.setData({ refundMessage: submittedForAfterSale ? '售后申请已提交，等待商家审核' : '退款申请已提交，正在等待支付渠道确认' });
      wx.showToast({ title: submittedForAfterSale ? '售后申请已提交' : '退款申请已提交', icon: 'none' });
      return this.queryRefund(false);
    }).catch(error => {
      console.error('create refund failed', error);
      return this.queryRefund(false).then(order => {
        const status = String(order && order.status || '').toUpperCase();
        const reconciled = submittedForAfterSale ? ['AFTER_SALE_REVIEW', 'REFUNDING', 'REFUNDED'].includes(status) : ['REFUNDING', 'REFUNDED'].includes(status);
        if (!order || !reconciled) wx.showToast({ title: error && error.userMessage || '退款申请未提交，请刷新订单后重试', icon: 'none' });
      }).catch(() => wx.showToast({ title: error && error.userMessage || '退款状态待核实，请刷新订单', icon: 'none' }));
    }).finally(() => this.setData({ refundSubmitting: false }));
  },

  queryRefund(showToast = true) {
    if (!this.orderId || !api.isProduction() || typeof api.queryRefund !== 'function' || this.data.refundQuerying) return Promise.resolve(null);
    this.setData({ refundQuerying: true });
    return api.queryRefund(this.orderId).then(order => {
      if (order && (order.id || order.orderNo || order.order_no)) this.renderOrder(order);
      if (showToast) wx.showToast({ title: '退款状态已更新', icon: 'none' });
      return order;
    }).catch(error => {
      console.error('query refund failed', error);
      if (showToast) wx.showToast({ title: '退款状态待核实，请稍后重试', icon: 'none' });
      return null;
    }).finally(() => this.setData({ refundQuerying: false }));
  },

  reorder() {
    const cart = storage.getCart();
    this.data.order.itemsSnapshot.forEach((item) => {
      const selectedOptions = (item.selectedOptions || []).map((option) => typeof option === 'string' ? option : option.name);
      const cartKey = `${this.data.order.businessType || commerce.businessTypeForFulfillment(this.data.order.fulfillmentType)}_${item.productId}_${selectedOptions.join('_') || 'default'}`;
      const current = cart.find((entry) => entry.cartKey === cartKey);
      if (current) current.quantity += item.quantity;
      else cart.push({ cartKey, productId: item.productId, name: item.name, emoji: item.emoji, color: item.color, coverImageUrl: item.coverImageUrl || '', unitPrice: item.unitPrice, quantity: item.quantity, fulfillmentType: this.data.order.fulfillmentType, businessType: this.data.order.businessType || commerce.businessTypeForFulfillment(this.data.order.fulfillmentType), remark: item.remark || '' });
    });
    storage.saveCart(cart);
    wx.navigateTo({ url: '/pages/cart/index' });
  }
});
