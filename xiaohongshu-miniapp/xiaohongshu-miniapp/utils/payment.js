function paymentDiagnostic(error, order) {
  const rawMessage = String(error && (error.errMsg || error.message || error.errorMessage) || '');
  let message = rawMessage;
  [
    order && order.pay_token,
    order && order.order_id,
    error && error.pay_token,
    error && error.payToken,
    error && error.order_id,
    error && error.orderId
  ].forEach(secret => {
    if (secret) message = message.split(String(secret)).join('[已隐藏]');
  });
  message = message
    .replace(/(["']?(?:pay[_-]?token|token|access[_-]?token|authorization|order[_-]?id|orderid)["']?\s*[:=]\s*)["']?[^,\s"'&}\]]+/gi, '$1[已隐藏]')
    .replace(/bearer\s+[^\s,;&]+/gi, 'Bearer [已隐藏]')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 120);
  const codeValue = error && (error.errCode || error.errNo || error.code);
  const code = String(codeValue === undefined || codeValue === null ? '' : codeValue)
    .replace(/[^a-zA-Z0-9_.-]/g, '')
    .slice(0, 32);
  const parts = [];
  if (code) parts.push(`错误 ${code}`);
  if (message) parts.push(message);
  return parts.join('：');
}

function paymentFailure(error, order) {
  const diagnostic = paymentDiagnostic(error, order);
  const rawMessage = String(error && (error.errMsg || error.message || error.errorMessage) || '').toLowerCase();
  if (rawMessage.indexOf('cancel') >= 0 || rawMessage.indexOf('取消') >= 0) {
    return `您已取消支付，订单已保留，可在订单详情继续支付${diagnostic ? `（${diagnostic}）` : ''}`;
  }
  return `支付未完成${diagnostic ? `（${diagnostic}）` : ''}，订单已保留，请稍后重试或在订单详情继续支付`;
}

function paymentInfoError() {
  return '支付信息不完整，订单已保留，请在订单详情重新尝试';
}

function paymentApiError() {
  return '当前环境暂不支持小红书支付，请在小红书客户端中打开后重试';
}

function requestGuaranteeOrderPayment(xhsApi, order, callbacks) {
  const handlers = callbacks || {};
  const fail = error => {
    const result = { outcome: 'failure', message: paymentFailure(error, order) };
    if (typeof handlers.onFailure === 'function') handlers.onFailure(result);
    return result;
  };

  if (!order || !order.order_id || !order.pay_token) {
    const result = { outcome: 'failure', message: paymentInfoError() };
    if (typeof handlers.onFailure === 'function') handlers.onFailure(result);
    return false;
  }
  if (!xhsApi || typeof xhsApi.requestGuaranteeOrderPayment !== 'function') {
    const result = { outcome: 'failure', message: paymentApiError() };
    if (typeof handlers.onFailure === 'function') handlers.onFailure(result);
    return false;
  }

  let outcome = '';
  try {
    xhsApi.requestGuaranteeOrderPayment({
      orderInfo: { payToken: order.pay_token, orderId: order.order_id },
      success: result => {
        if (outcome) return;
        outcome = 'success';
        if (typeof handlers.onSuccess === 'function') handlers.onSuccess(result);
      },
      fail: error => {
        if (outcome) return;
        outcome = 'failure';
        fail(error);
      },
      complete: result => {
        if (!outcome) outcome = 'unknown';
        if (typeof handlers.onComplete === 'function') handlers.onComplete({ outcome, result });
      }
    });
    return true;
  } catch (error) {
    outcome = 'failure';
    fail(error);
    return false;
  }
}

module.exports = {
  requestGuaranteeOrderPayment
};
