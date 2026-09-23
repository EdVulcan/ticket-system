const runtime = require('../config/runtime');

const SESSION_KEY = 'saas_wechat_session_v1';
const SESSION_SKEW_MS = 30 * 1000;
const STOREFRONT_PATH_PREFIX = '/storefront/wechat';
let sessionPromise = null;

function appData() {
  try {
    if (typeof getApp === 'function') {
      const app = getApp();
      return app && app.globalData || {};
    }
  } catch (error) {
    // The module is also loaded by static tests without a running App.
  }
  return {};
}

function wxApi() {
  if (typeof wx === 'undefined') throw createError('WX_UNAVAILABLE', '当前运行环境不支持微信请求');
  return wx;
}

function isProduction() {
  return (appData().deploymentMode || runtime.deploymentMode) === 'production';
}

function settings() {
  const data = appData();
  return {
    baseUrl: String(data.apiBaseUrl !== undefined ? data.apiBaseUrl : runtime.apiBaseUrl || '').trim(),
    appId: String(data.appId !== undefined ? data.appId : runtime.appId || '').trim(),
    timeoutMs: Number(data.requestTimeoutMs || runtime.requestTimeoutMs || 10000)
  };
}

function createError(code, userMessage, statusCode) {
  const error = new Error(code);
  error.code = code;
  error.userMessage = userMessage;
  if (statusCode) error.statusCode = statusCode;
  return error;
}

function normalizeBusiness(value) {
  const type = String(value || '').trim().toLowerCase();
  return type === 'restaurant' || type === 'retail' ? type : '';
}

function normalizeBusinesses(session) {
  const rows = Array.isArray(session && session.businesses) ? session.businesses : [];
  const legacyType = normalizeBusiness(session && (session.business_type || session.businessType));
  const legacyLocationId = session && (session.location_id !== undefined ? session.location_id : session.locationId);
  const source = rows.length ? rows : legacyType ? [{ business_type: legacyType, location_id: legacyLocationId, location: session.location }] : [];
  return source.map((entry) => {
    const value = entry || {};
    const businessType = normalizeBusiness(value.business_type || value.businessType);
    const location = value.location || null;
    const locationId = value.location_id !== undefined ? value.location_id : value.locationId !== undefined ? value.locationId : location && (location.id !== undefined ? location.id : location.location_id);
    return { businessType, business_type: businessType, locationId, location_id: locationId, location };
  }).filter((entry) => entry.businessType);
}

function assertConfigured() {
  const config = settings();
  if (!isProduction()) return config;
  if (!config.baseUrl || !/^https:\/\/[^\/\s]+(?:\/|$)/i.test(config.baseUrl)) {
    throw createError('API_BASE_URL_MISSING', '服务尚未配置，请稍后再试');
  }
  if (!config.appId) throw createError('WECHAT_APP_ID_MISSING', '小程序尚未配置，请稍后再试');
  return config;
}

function readSession() {
  try {
    const stored = wxApi().getStorageSync(SESSION_KEY);
    if (!stored || !stored.token) return null;
    const expiresAt = Date.parse(stored.expiresAt);
    if (!Number.isFinite(expiresAt) || expiresAt <= Date.now() + SESSION_SKEW_MS) {
      clearSession();
      return null;
    }
    return { token: String(stored.token), expiresAt: stored.expiresAt, businesses: normalizeBusinesses(stored) };
  } catch (error) {
    return null;
  }
}

function saveSession(session) {
  const token = session && String(session.token || '').trim();
  const expiresAt = session && session.expires_at !== undefined ? session.expires_at : session && session.expiresAt;
  if (!token || !expiresAt || !Number.isFinite(Date.parse(expiresAt))) {
    throw createError('SESSION_INVALID', '登录状态无效，请稍后重试');
  }
  const value = { token, expiresAt, businesses: normalizeBusinesses(session) };
  wxApi().setStorageSync(SESSION_KEY, value);
  const data = appData();
  data.session = value;
  return value;
}

function clearSession() {
  try { wxApi().removeStorageSync(SESSION_KEY); } catch (error) { /* best effort */ }
  const data = appData();
  if (data) data.session = null;
}

function requestUrl(path) {
  const baseUrl = assertConfigured().baseUrl.replace(/\/+$/, '');
  let relativePath = `/${String(path || '').replace(/^\/+/, '')}`;
  if (isProduction() && relativePath !== STOREFRONT_PATH_PREFIX && !relativePath.startsWith(`${STOREFRONT_PATH_PREFIX}/`)) {
    relativePath = `${STOREFRONT_PATH_PREFIX}${relativePath}`;
  }
  return `${baseUrl}${relativePath}`;
}

function businessPath(path, businessType) {
  const value = String(path || '');
  const type = normalizeBusiness(businessType);
  if (!type || /(?:^|[?&])business_type=/.test(value)) return value;
  return `${value}${value.indexOf('?') >= 0 ? '&' : '?'}business_type=${encodeURIComponent(type)}`;
}

function responseMessage(data, statusCode) {
  if (data && typeof data === 'object') {
    const message = data.error || data.message;
    if (typeof message === 'string' && message.trim()) return message.trim();
  }
  if (statusCode === 401) return '登录状态已失效，请稍后重试';
  if (statusCode === 403) return '当前账号暂无权限执行此操作';
  if (statusCode === 404) return '请求的内容不存在';
  if (statusCode === 409) return '当前业务选择需要重新确认，请刷新后重试';
  if (statusCode >= 500) return '服务暂时不可用，请稍后重试';
  return '请求未完成，请稍后重试';
}

function rawRequest(path, options) {
  const config = assertConfigured();
  const opts = options || {};
  const headers = Object.assign({ 'content-type': 'application/json' }, opts.headers || {});
  if (opts.token) headers.Authorization = `Bearer ${opts.token}`;
  return new Promise((resolve, reject) => {
    let settled = false;
    const fail = (error) => {
      if (settled) return;
      settled = true;
      reject(error);
    };
    try {
      wxApi().request({
        url: requestUrl(businessPath(path, opts.businessType)),
        method: opts.method || 'GET',
        data: opts.data,
        header: headers,
        timeout: config.timeoutMs,
        success(response) {
          if (settled) return;
          settled = true;
          const statusCode = Number(response && response.statusCode || 0);
          if (statusCode >= 200 && statusCode < 300) {
            resolve(response.data);
            return;
          }
          const error = createError(`HTTP_${statusCode || 'UNKNOWN'}`, responseMessage(response && response.data, statusCode), statusCode);
          error.responseData = response && response.data;
          reject(error);
        },
        fail(error) {
          const message = String(error && error.errMsg || '').toLowerCase();
          const code = message.indexOf('timeout') >= 0 ? 'REQUEST_TIMEOUT' : 'NETWORK_ERROR';
          fail(createError(code, code === 'REQUEST_TIMEOUT' ? '网络响应超时，请检查网络后重试' : '网络连接失败，请检查网络后重试'));
        }
      });
    } catch (error) {
      fail(createError('REQUEST_FAILED', '请求未完成，请稍后重试'));
    }
  });
}

function wxLogin() {
  return new Promise((resolve, reject) => {
    try {
      wxApi().login({
        success(result) {
          if (result && result.code) resolve(String(result.code));
          else reject(createError('WX_LOGIN_FAILED', '微信登录失败，请稍后重试'));
        },
        fail() { reject(createError('WX_LOGIN_FAILED', '微信登录失败，请稍后重试')); }
      });
    } catch (error) {
      reject(createError('WX_LOGIN_FAILED', '微信登录失败，请稍后重试'));
    }
  });
}

function login() {
  const config = assertConfigured();
  if (sessionPromise) return sessionPromise;
  sessionPromise = wxLogin()
    .then(code => rawRequest('/session', { method: 'POST', data: { app_id: config.appId, code } }))
    .then(saveSession)
    .finally(() => { sessionPromise = null; });
  return sessionPromise;
}

function ensureSession(force) {
  assertConfigured();
  if (!force) {
    const session = readSession();
    if (session) return Promise.resolve(session);
  }
  return login();
}

function request(path, options) {
  const opts = options || {};
  assertConfigured();
  const retry = (authRetried, businessRetried) => ensureSession(false).then(session => rawRequest(path, Object.assign({}, opts, { token: session.token }))).catch(error => {
    if (error && error.statusCode === 401 && !authRetried) {
      clearSession();
      return ensureSession(true).then(session => rawRequest(path, Object.assign({}, opts, { token: session.token })));
    }
    // A pre-existing single-business session has no authorization list. If a
    // newly multi-business account rejects the first selected request with
    // 409, exchange one fresh wx.login code, persist businesses, then retry
    // the same server-authorized selector once.
    if (error && error.statusCode === 409 && opts.businessType && !businessRetried) {
      const session = readSession();
      if (!session || !session.businesses || !session.businesses.length) {
        clearSession();
        return ensureSession(true).then(next => rawRequest(path, Object.assign({}, opts, { token: next.token })));
      }
    }
    throw error;
  });
  return retry(false, false);
}

module.exports = {
  SESSION_KEY,
  isProduction,
  isConfigured() {
    if (!isProduction()) return true;
    try { assertConfigured(); return true; } catch (error) { return false; }
  },
  getSession: readSession,
  getBusinesses() {
    const session = readSession();
    return session && Array.isArray(session.businesses) ? session.businesses : [];
  },
  saveSession,
  clearSession,
  ensureSession,
  login,
  request,
  createError
};
