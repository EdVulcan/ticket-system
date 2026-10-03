const brand = require('../../../config/brand');
const mock = require('../../../data/mock');
const storage = require('../../../services/storage');
const api = require('../../../services/api');
const commerce = require('../../../config/commerce');

function assistCampaignAvailable(campaign) {
  if (!campaign || !campaign.id || String(campaign.status || '').toLowerCase() !== 'active') return false;
  const starterAmount = Number(campaign.starterReward && campaign.starterReward.discountAmount);
  const helperAmount = Number(campaign.helperReward && campaign.helperReward.discountAmount);
  return Number.isFinite(starterAmount) && starterAmount > 0 && Number.isFinite(helperAmount) && helperAmount > 0;
}

Page({
  data: {
    brand,
    profile: { nickname: '朋友' },
    store: mock.store,
    coupons: [],
    couponCount: 0,
    showCoupons: false,
    contact: { available: false, contactType: '', contactName: '', wechatId: '', qrCodeUrl: '' },
    // Membership is retained for a later platform-wide upgrade and is not
    // part of the current customized storefront.
    membershipEnabled: false,
    showContact: false,
    assistAvailable: false,
    memberProfile: { memberNo: '', status: 'active', membershipStatus: 'provisional', phoneMasked: '', phoneVerified: false, membershipConsentGranted: false, sourceCount: 0 },
    memberConsentGranted: false,
    verifyingMemberPhone: false,
    memberError: '',
    orderStats: { waitPay: 0, processing: 0, delivering: 0, completed: 0 }
  },

  onShow() { this.loadProfile(); },

  loadAssistAvailability() {
    if (typeof api.getAssistCampaigns !== 'function') {
      this.setData({ assistAvailable: false });
      return;
    }
    const businesses = typeof api.getAuthorizedBusinesses === 'function' ? api.getAuthorizedBusinesses() : [];
    const types = businesses.map(item => String(item && (item.businessType || item.business_type) || '').toLowerCase()).filter((item, index, values) => (item === 'restaurant' || item === 'retail') && values.indexOf(item) === index);
    const requestedTypes = types.length ? types : (api.isProduction() ? [] : ['restaurant']);
    if (!requestedTypes.length) {
      this.setData({ assistAvailable: false });
      return;
    }
    Promise.all(requestedTypes.map(type => api.getAssistCampaigns(type).catch(() => ({ data: [] })))).then(results => {
      const available = results.some(result => (result && result.data || []).some(assistCampaignAvailable));
      this.setData({ assistAvailable: available });
    });
  },

  loadCoupons() {
    const read = () => {
      const businesses = typeof api.getAuthorizedBusinesses === 'function' ? api.getAuthorizedBusinesses() : [];
      const types = businesses.map(item => String(item && (item.businessType || item.business_type) || '').toLowerCase()).filter((item, index, values) => (item === 'restaurant' || item === 'retail') && values.indexOf(item) === index);
      if (!types.length) return api.getCoupons();
      return Promise.all(types.map(type => api.getCoupons(type))).then(results => {
        const merged = [];
        const byID = new Map();
        results.forEach(result => (result && result.data || []).forEach(coupon => {
          const key = String(coupon && coupon.id || '');
          if (!key || !byID.has(key)) {
            if (key) byID.set(key, coupon);
            merged.push(coupon);
            return;
          }
          const existing = byID.get(key);
          const scopes = new Set([].concat(existing.businessTypes || existing.business_types || [], coupon.businessTypes || coupon.business_types || []).filter(Boolean));
          if (scopes.size) existing.businessTypes = Array.from(scopes);
        }));
        return { data: merged };
      });
    };
    if (api.isProduction() && typeof api.ensureSession === 'function' && (!api.getAuthorizedBusinesses || !api.getAuthorizedBusinesses().length)) return api.ensureSession().then(read);
    return Promise.resolve().then(read);
  },

  loadProfile() {
    const app = getApp();
    if (api.isProduction()) {
      api.getStorefrontContact().then((contact) => {
        this.setData({ contact: contact || { available: false }, showContact: contact && contact.available ? this.data.showContact : false });
      }).catch((error) => {
        console.error('load storefront contact failed', error);
        this.setData({ contact: { available: false }, showContact: false });
      });
      if (this.data.membershipEnabled) {
        api.getMemberProfile().then((profile) => {
          this.setMemberProfile(profile);
        }).catch((error) => {
          console.error('load member profile failed', error);
          this.setData({ memberError: '', memberProfile: Object.assign({}, this.data.memberProfile, { membershipStatus: 'provisional', phoneVerified: false }) });
        });
      }
      Promise.all([this.loadCoupons(), api.getOrders()]).then(([couponResult, orderResult]) => {
        const coupons = couponResult.data || [];
        const orders = orderResult.data || [];
        storage.saveCoupons(coupons);
        this.renderProfile(app, coupons, orders);
        this.loadAssistAvailability();
      }).catch((error) => { console.error('load profile data failed', error); this.renderProfile(app, [], []); });
      return;
    }
    const coupons = storage.getCoupons().filter((coupon) => coupon.status === 'AVAILABLE');
    const orders = storage.getOrders();
    if (this.data.membershipEnabled) this.setMemberProfile({ membership_status: 'provisional', status: 'active', phone_verified: false, membership_consent_granted: false, source_count: 1 });
    this.renderProfile(app, coupons, orders);
    this.loadAssistAvailability();
  },

  setMemberProfile(profile) {
    const value = profile || {};
    const normalized = {
      memberNo: value.memberNo || value.member_no || '',
      status: value.status || 'active',
      membershipStatus: value.membershipStatus || value.membership_status || 'provisional',
      phoneMasked: value.phoneMasked || value.phone_masked || '',
      phoneVerified: Boolean(value.phoneVerified !== undefined ? value.phoneVerified : value.phone_verified),
      membershipConsentGranted: Boolean(value.membershipConsentGranted !== undefined ? value.membershipConsentGranted : value.membership_consent_granted),
      sourceCount: Number(value.sourceCount !== undefined ? value.sourceCount : value.source_count || 0)
    };
    this.setData({ memberProfile: normalized, memberConsentGranted: normalized.membershipConsentGranted });
  },

  onMemberConsentChange(event) {
    const values = event && event.detail && Array.isArray(event.detail.value) ? event.detail.value : [];
    this.setData({ memberConsentGranted: values.indexOf('member') >= 0, memberError: '' });
  },

  onGetPhoneNumber(event) {
    const detail = event && event.detail ? event.detail : {};
    const message = String(detail.errMsg || '');
    if (message && message.indexOf(':ok') < 0) {
      this.setData({ memberError: '未完成会员认证，可稍后再次尝试。' });
      return;
    }
    if (!this.data.memberConsentGranted) {
      this.setData({ memberError: '请先同意会员服务说明，再完成认证。' });
      return;
    }
    const code = detail.code || '';
    if (!code) {
      this.setData({ memberError: '没有取得有效的授权凭据，请重新点击认证。' });
      return;
    }
    if (this.data.verifyingMemberPhone) return;
    this.setData({ verifyingMemberPhone: true, memberError: '' });
    const requestId = `member-phone-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    api.verifyMemberPhone(code, requestId, true, 'member-phone-v1').then((profile) => {
      const next = Object.assign({}, this.data.memberProfile, profile || {}, { phoneVerified: true, membershipStatus: (profile && (profile.membership_status || profile.membershipStatus)) || 'active' });
      this.setMemberProfile(next);
      this.setData({ verifyingMemberPhone: false, memberError: '' });
      wx.showToast({ title: '会员认证完成', icon: 'success' });
    }).catch((error) => {
      this.setData({ verifyingMemberPhone: false, memberError: error.userMessage || error.message || '暂时无法完成会员认证，请稍后重试' });
    });
  },

  renderProfile(app, allCoupons, orders) {
    const coupons = allCoupons.filter((coupon) => coupon.status === 'AVAILABLE');
    const normalizedOrders = orders.map(item => typeof api.normalizeOrder === 'function' ? api.normalizeOrder(item) : item);
    const restaurantOrders = normalizedOrders.filter(item => item.fulfillmentType !== commerce.FULFILLMENT.COURIER);
    this.setData({ profile: app.globalData.user, store: storage.getStore(), coupons, couponCount: coupons.length, orderStats: { waitPay: normalizedOrders.filter((item) => item.status === 'WAIT_PAY').length, processing: restaurantOrders.filter((item) => ['PAID', 'PREPARING'].indexOf(item.status) > -1).length, delivering: restaurantOrders.filter((item) => item.status === 'DELIVERING').length, completed: normalizedOrders.filter((item) => item.status === 'COMPLETED').length } });
  },

  toggleCoupons() { this.setData({ showCoupons: !this.data.showCoupons }); },
  goOrders(event) {
    storage.saveOrderListFilter(event && event.currentTarget && event.currentTarget.dataset.tab || 'ALL');
    wx.switchTab({ url: '/pages/order/list/index' });
  },
  goAddress() { wx.navigateTo({ url: '/pages/address/list/index' }); },
  goAssist() { wx.navigateTo({ url: '/pages/assist/index/index' }); },
  openContact() {
    if (this.data.contact && this.data.contact.available) this.setData({ showContact: true });
  },
  closeContact() { this.setData({ showContact: false }); },
  keepContactOpen() {},
  previewContactQRCode() {
    const url = this.data.contact && this.data.contact.qrCodeUrl;
    if (url) wx.previewImage({ current: url, urls: [url] });
  },
  copyWechatID() {
    const wechatId = this.data.contact && this.data.contact.wechatId;
    if (!wechatId) return;
    wx.setClipboardData({ data: wechatId, success: () => wx.showToast({ title: '微信号已复制', icon: 'success' }) });
  }
});
