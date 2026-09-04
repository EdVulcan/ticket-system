const API_ROOT = 'https://ymsq.edvulcan.top/api/v1/storefront/xiaohongshu';
// The storefront is now bound to the production Xiaohongshu mini-program.
// Keep this value aligned with project.config.json and the production channel account.
const APP_ID = '672997dc4889610001aa80b2';
const SESSION_STORAGE_KEY = 'ticket-miniapp-session';
const STORE_NAME_STORAGE_KEY = 'ticket-miniapp-store-name';

App({
  onLaunch() {
    const cachedStoreName = xhs.getStorageSync(STORE_NAME_STORAGE_KEY);
    if (cachedStoreName) this.setStoreName(cachedStoreName, false);
    this.ensureSession().catch(() => {});
  },

  setStoreName(name, persist) {
    const storeName = String(name || '').trim();
    if (!storeName) return;
    this.globalData.storeName = storeName;
    if (persist !== false) xhs.setStorageSync(STORE_NAME_STORAGE_KEY, storeName);
    this.setNavigationTitle(storeName);
  },

  setNavigationTitle(title) {
    if (typeof xhs.setNavigationBarTitle === 'function') {
      xhs.setNavigationBarTitle({ title: String(title || '官方商城') });
    }
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

  globalData: { session: null, sessionPromise: null, storeName: '官方商城' }
});
