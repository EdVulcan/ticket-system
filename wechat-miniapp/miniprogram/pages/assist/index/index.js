const brand = require('../../../config/brand');
const storage = require('../../../services/storage');
const api = require('../../../services/api');
const promotion = require('../../../utils/promotion');
const storefrontBrand = require('../../../services/storefront-brand');

const BUSINESS_TYPE = 'restaurant';

function normalizeBusinessType(value) {
  const type = String(value || '').toLowerCase();
  return type === 'retail' ? 'retail' : BUSINESS_TYPE;
}

function hasReward(reward) {
  const amount = Number(reward && reward.discountAmount);
  return Boolean(reward && Number.isFinite(amount) && amount > 0);
}

function isAvailableCampaign(campaign, businessType) {
  if (!campaign || !campaign.id || String(campaign.status || '').toLowerCase() !== 'active') return false;
  return promotion.appliesToBusiness(campaign, businessType) && hasReward(campaign.starterReward) && hasReward(campaign.helperReward);
}

function amountText(cents) {
  const amount = Number(cents);
  if (!Number.isFinite(amount)) return '';
  const yuan = amount / 100;
  return Number.isInteger(yuan) ? String(yuan) : yuan.toFixed(2);
}

function rewardAmount(reward) {
  return reward && reward.discountAmount !== null && reward.discountAmount !== undefined ? amountText(reward.discountAmount) : '—';
}

function rewardSummary(reward) {
  if (!reward) return '以到账券规则为准';
  const parts = [];
  const hasDiscount = reward.discountAmount !== null && reward.discountAmount !== undefined && amountText(reward.discountAmount) !== '';
  const hasMinimum = reward.minGoodsAmount !== null && reward.minGoodsAmount !== undefined && amountText(reward.minGoodsAmount) !== '';
  if (hasDiscount && hasMinimum) parts.push(`满 ${amountText(reward.minGoodsAmount)} 元减 ${amountText(reward.discountAmount)} 元`);
  else if (hasDiscount) parts.push(`减 ${amountText(reward.discountAmount)} 元`);
  if (reward.validDays !== null && reward.validDays !== undefined && Number.isFinite(Number(reward.validDays))) parts.push(`${reward.validDays}天有效`);
  return parts.join(' · ') || '以到账券规则为准';
}

function toLocalSession(session) {
  if (!session) return null;
  return Object.assign({}, session, {
    id: session.id || session._id,
    token: session.token || session.shareToken || session.share_token || '',
    status: String(session.status || '').toLowerCase()
  });
}

function makeCoupon(id, role, reward) {
  const value = reward || {};
  return {
    id,
    name: role === 'STARTER' ? '发起者助力券' : '好友助力券',
    description: rewardSummary(value),
    discountAmount: value.discountAmount,
    minGoodsAmount: value.minGoodsAmount,
    validDays: value.validDays,
    status: 'AVAILABLE',
    source: 'ASSIST',
    sourceRole: role,
    expireAt: value.templateEndsAt || '2026-09-30'
  };
}

Page({
  data: {
    brand,
    storeName: brand.name,
    campaign: null,
    session: null,
    isDemo: true,
    creating: false,
    benefitText: '加载活动规则中',
    starterDiscountText: '—',
    helperDiscountText: '—',
    starterRewardText: '以到账券规则为准',
    helperRewardText: '以到账券规则为准',
    campaignAvailable: false,
    businessType: BUSINESS_TYPE
  },

  applyRewards(source) {
    const starterReward = source && source.starterReward;
    const helperReward = source && source.helperReward;
    this.setData({
      benefitText: starterReward || helperReward ? `发起者：${rewardSummary(starterReward)} · 好友：${rewardSummary(helperReward)}` : '请以到账券规则为准',
      starterDiscountText: rewardAmount(starterReward),
      helperDiscountText: rewardAmount(helperReward),
      starterRewardText: rewardSummary(starterReward),
      helperRewardText: rewardSummary(helperReward)
    });
  },

  loadCampaign() {
    api.getAssistCampaigns(this.businessType || BUSINESS_TYPE).then((result) => {
      const campaigns = Array.isArray(result.data) ? result.data : [];
      const campaign = campaigns.find(item => isAvailableCampaign(item, this.businessType || BUSINESS_TYPE)) || null;
      this.currentCampaign = campaign;
      this.setData({ campaign, campaignAvailable: Boolean(campaign && campaign.id) });
      this.applyRewards(campaign);
    }).catch((error) => {
      console.error('load assist campaigns failed', error);
      this.currentCampaign = null;
      this.setData({ campaign: null, campaignAvailable: false, benefitText: '活动加载失败' });
    });
  },

  loadCachedSession() {
    const cached = toLocalSession(storage.getAssist());
    if (!cached || !cached.token) return;
    api.getAssistSession(cached.token, this.businessType || BUSINESS_TYPE).then((result) => {
      const session = toLocalSession(result.data);
      storage.saveAssist(session);
      this.setData({ session });
      this.applyRewards(session);
    }).catch(() => {});
  },

  onShow() {
    storefrontBrand.refresh(this, this.businessType || BUSINESS_TYPE);
    const demo = !api.isProduction();
    const session = demo ? toLocalSession(storage.getAssist()) : null;
    this.setData({ session, isDemo: demo, businessType: this.businessType || BUSINESS_TYPE });
    if (demo) {
      if ((this.businessType || BUSINESS_TYPE) !== BUSINESS_TYPE) {
        this.currentCampaign = null;
        this.setData({ campaign: null, campaignAvailable: false, benefitText: '当前业务暂无可用活动' });
        return;
      }
      this.currentCampaign = {
        id: 'campaign_demo_001',
        title: '好友助力，双方得券',
        status: 'active',
        starterReward: { discountAmount: 300, minGoodsAmount: 2500, validDays: 7, templateEndsAt: '2026-09-30' },
        helperReward: { discountAmount: 200, minGoodsAmount: 3000, validDays: 5, templateEndsAt: '2026-09-30' }
      };
      this.setData({ campaign: this.currentCampaign, campaignAvailable: true });
      this.applyRewards(this.data.session || this.currentCampaign);
      return;
    }
    this.setData({ campaign: null, campaignAvailable: false });
    this.loadCampaign();
    this.loadCachedSession();
  },

  createSession() {
    const campaign = this.currentCampaign || this.data.campaign;
    if (!campaign || !campaign.id) {
      wx.showToast({ title: '暂无可用活动', icon: 'none' });
      return;
    }
    if (this.data.creating) return;
    if (api.isProduction()) {
      this.setData({ creating: true });
      const idempotencyKey = storage.makeId('assist_create');
      api.createAssistSession(campaign.id, idempotencyKey, this.businessType || BUSINESS_TYPE).then((result) => {
        const session = toLocalSession(result.data);
        storage.saveAssist(session);
        this.setData({ session, creating: false });
        this.applyRewards(session && (session.starterReward || session.helperReward) ? session : campaign);
        wx.showToast({ title: '助力已发起', icon: 'success' });
        wx.showShareMenu({ withShareTicket: true });
      }).catch((error) => {
        console.error('create assist session failed', error);
        this.setData({ creating: false });
        wx.showToast({ title: error && error.userMessage ? error.userMessage : '活动暂不可用', icon: 'none' });
      });
      return;
    }
    const session = toLocalSession({
      id: storage.makeId('assist'),
      campaignId: campaign.id,
      starterId: 'demo_user_001',
      token: `demo-token-${Math.floor(Math.random() * 9000 + 1000)}`,
      status: 'active',
      helperId: '',
      helperCount: 0,
      starterReward: campaign.starterReward,
      helperReward: campaign.helperReward,
      createdAt: new Date().toISOString(),
      expiresAt: '2026-09-30'
    });
    storage.saveAssist(session);
    this.setData({ session });
    this.applyRewards(session);
    wx.showToast({ title: '助力已发起', icon: 'success' });
    wx.showShareMenu({ withShareTicket: true });
  },

  simulateHelp() {
    if (api.isProduction()) return;
    this.completeSession();
  },

  completeSession() {
    const session = toLocalSession(storage.getAssist());
    if (!session || session.status !== 'active') return;
    session.status = 'success';
    session.helperId = 'demo_helper_001';
    session.helperCount = 1;
    session.succeededAt = new Date().toISOString();
    storage.saveAssist(session);
    const coupons = storage.getCoupons();
    if (!coupons.some(coupon => coupon.id === `${session.id}_starter`)) coupons.push(makeCoupon(`${session.id}_starter`, 'STARTER', session.starterReward));
    if (!coupons.some(coupon => coupon.id === `${session.id}_helper`)) coupons.push(makeCoupon(`${session.id}_helper`, 'HELPER', session.helperReward));
    storage.saveCoupons(coupons);
    this.setData({ session });
    this.applyRewards(session);
    wx.showToast({ title: '助力成功，券已到账', icon: 'success' });
  },

  goHome() { wx.switchTab({ url: '/pages/index/index' }); },

  onLoad(options) {
    const requestedType = options && (options.business_type || options.businessType);
    this.businessType = normalizeBusinessType(requestedType);
    if (!requestedType && typeof api.getAuthorizedBusinesses === 'function') {
      const businesses = api.getAuthorizedBusinesses();
      if (businesses.length === 1) this.businessType = normalizeBusinessType(businesses[0] && (businesses[0].businessType || businesses[0].business_type));
    }
    this.setData({ businessType: this.businessType });
  },

  onShareAppMessage() {
    const token = this.data.session && this.data.session.token ? this.data.session.token : '';
    const type = this.businessType || BUSINESS_TYPE;
    const typeQuery = type === BUSINESS_TYPE ? '' : `&business_type=${encodeURIComponent(type)}`;
    return { title: `${storefrontBrand.name()}｜帮我助力，一起领券`, path: `/pages/assist/detail/index?token=${encodeURIComponent(token)}${typeQuery}` };
  }
});
