const API_ROOT = 'https://ymsq.edvulcan.top/api/v1/storefront/xiaohongshu';
const APP_ID = '6a79580901ada500017dac60';
const SESSION_STORAGE_KEY = 'ticket-miniapp-session';

App({
  onLaunch() {
    this.ensureSession().catch(() => {});
  },

  ensureSession(forceRefresh) {
    if (!forceRefresh) {
      const cached = xhs.getStorageSync(SESSION_STORAGE_KEY);
      if (cached && cached.token && new Date(cached.expires_at).getTime() > Date.now() + 60000) {
        this.globalData.session = cached;
        return Promise.resolve(cached);
      }
      if (this.globalData.sessionPromise) return this.globalData.sessionPromise;
    }

    this.globalData.sessionPromise = new Promise((resolve, reject) => {
      xhs.login({
        success: loginResult => {
          if (!loginResult.code) {
            reject(new Error('未取得小红书登录凭证'));
            return;
          }
          xhs.request({
            url: `${API_ROOT}/session`,
            method: 'POST',
            header: { 'content-type': 'application/json' },
            data: { app_id: APP_ID, code: loginResult.code },
            success: response => {
              if (response.statusCode !== 200 || !response.data || !response.data.token) {
                reject(new Error(this.readError(response, '登录失败，请稍后重试')));
                return;
              }
              this.globalData.session = response.data;
              xhs.setStorageSync(SESSION_STORAGE_KEY, response.data);
              resolve(response.data);
            },
            fail: () => reject(new Error('网络连接失败，请稍后重试'))
          });
        },
        fail: () => reject(new Error('小红书登录失败，请重新进入小程序'))
      });
    }).finally(() => {
      this.globalData.sessionPromise = null;
    });
    return this.globalData.sessionPromise;
  },

  request(path, options) {
    const settings = options || {};
    return this.ensureSession(false)
      .then(session => this.sendAuthorized(path, settings, session))
      .catch(error => {
        if (error && error.code === 'SESSION_EXPIRED') {
          return this.ensureSession(true).then(session => this.sendAuthorized(path, settings, session));
        }
        throw error;
      });
  },

  sendAuthorized(path, settings, session) {
    return new Promise((resolve, reject) => {
      xhs.request({
        url: `${API_ROOT}${path}`,
        method: settings.method || 'GET',
        data: settings.data,
        header: {
          Authorization: `Bearer ${session.token}`,
          'content-type': 'application/json'
        },
        success: response => {
          if (response.statusCode === 401) {
            xhs.removeStorageSync(SESSION_STORAGE_KEY);
            this.globalData.session = null;
            const expired = new Error('登录状态已失效');
            expired.code = 'SESSION_EXPIRED';
            reject(expired);
            return;
          }
          if (response.statusCode < 200 || response.statusCode >= 300) {
            reject(new Error(this.readError(response, '请求失败，请稍后重试')));
            return;
          }
          resolve(response.data);
        },
        fail: () => reject(new Error('网络连接失败，请稍后重试'))
      });
    });
  },

  readError(response, fallback) {
    return response && response.data && response.data.error ? response.data.error : fallback;
  },

  globalData: { session: null, sessionPromise: null }
});
