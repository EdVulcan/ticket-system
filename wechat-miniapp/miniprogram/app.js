const brand = require('./config/brand');
const storage = require('./services/storage');
const api = require('./services/api');
const runtime = require('./config/runtime');

App({
  globalData: {
    // The runtime file selects demo or production explicitly. Production requires an HTTPS API prefix
    // and AppID; no app secret belongs in this package.
    deploymentMode: runtime.deploymentMode,
    apiBaseUrl: runtime.apiBaseUrl,
    appId: runtime.appId,
    requestTimeoutMs: runtime.requestTimeoutMs,
    storeId: 'store_001',
    storeName: brand.name,
    user: {
      id: 'demo_user_001',
      nickname: '朋友',
      avatarUrl: '',
      role: 'CUSTOMER'
    }
  },

  onLaunch() {
    const savedProfile = storage.getProfile();
    if (savedProfile) {
      this.globalData.user = Object.assign({}, this.globalData.user, savedProfile);
    }

    if (this.globalData.deploymentMode !== 'production') return;
    if (!api.isConfigured()) {
      console.error('生产模式缺少 HTTPS API 或 AppID，已拒绝发起业务请求');
      return;
    }
    // Login is lazy-safe and also warms the session for the first catalog request.
    // The server exchanges the one-time code; no secret is ever shipped here.
    api.ensureSession().catch((error) => console.warn('小程序登录暂不可用', error && error.code || 'LOGIN_FAILED'));
  },

  setProfile(profile) {
    this.globalData.user = Object.assign({}, this.globalData.user, profile);
    storage.saveProfile(this.globalData.user);
  }
});
