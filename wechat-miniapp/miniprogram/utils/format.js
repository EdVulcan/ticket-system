function money(cents) {
  return (Number(cents || 0) / 100).toFixed(2);
}

function yuan(cents) {
  const value = Number(cents || 0) / 100;
  return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

function pad(value) {
  return value < 10 ? `0${value}` : String(value);
}

function dateTime(value) {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return `${date.getMonth() + 1}月${date.getDate()}日 ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function orderStatus(status, fulfillmentType) {
  if (fulfillmentType === 'COURIER') {
    const courierMap = { PAID: '待发货', SHIPPED: '待收货' };
    if (courierMap[status]) return courierMap[status];
  }
  const map = {
    WAIT_PAY: '待付款',
    PAID: '待接单',
    PREPARING: '制作中',
    DELIVERING: '配送中',
    COMPLETED: '已完成',
    CANCELED: '已取消',
    REFUNDING: '退款中',
    REFUND_FAILED: '退款已关闭',
    REFUND_REVIEW: '退款异常待核查',
    REFUNDED: '已退款'
  };
  return map[status] || status;
}

function sum(list, field) {
  return (list || []).reduce((total, item) => total + Number(item[field] || 0), 0);
}

module.exports = { money, yuan, dateTime, orderStatus, sum };
