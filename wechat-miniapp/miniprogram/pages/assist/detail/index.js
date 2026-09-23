const brand = require('../../../config/brand');
const storage = require('../../../services/storage');
const api = require('../../../services/api');

const BUSINESS_TYPE = 'restaurant';

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

function toLocalSession(session, token) {
  if (!session) return null;
  return Object.assign({}, session, {
    id: session.id || session._id,
    token: token || session.token || session.shareToken || session.share_token || '',
    status: String(session.status || '').toLowerCase()
  });
}

Page({
  data: {
    brand,
    session: null,
    benefitText: '规则加载中',
    starterDiscountText: '—',
    helperDiscountText: '—',
    starterRewardText: '以到账券规则为准',
    helperRewardText: '以到账券规则为准'
  },

  applyRewards(session) {
    const starterReward = session && session.starterReward;
    const helperReward = session && session.helperReward;
    this.setData({
      benefitText: starterReward || helperReward ? `发起者：${rewardSummary(starterReward)} · 好友：${rewardSummary(helperReward)}` : '请以到账券规则为准',
      starterDiscountText: rewardAmount(starterReward),
      helperDiscountText: rewardAmount(helperReward),
      starterRewardText: rewardSummary(starterReward),
      helperRewardText: rewardSummary(helperReward)
    });
  },

  onLoad(options) {
    this.shareToken = String((options && (options.token || options.share_token)) || '').trim();
    this.setData({ shareToken: this.shareToken });
    if (api.isProduction()) {
      if (!this.shareToken) {
        wx.showToast({ title: '助力链接无效', icon: 'none' });
        return;
      }
      api.getAssistSession(this.shareToken, BUSINESS_TYPE).then((result) => {
        const session = toLocalSession(result.data, this.shareToken);
        this.setData({ session });
        this.applyRewards(session);
      }).catch((error) => {
        console.error('load assist session failed', error);
        wx.showToast({ title: error && error.userMessage ? error.userMessage : '助力任务不存在', icon: 'none' });
      });
      return;
    }
    const session = toLocalSession(storage.getAssist());
    const matched = session && (!this.shareToken || session.token === this.shareToken) ? session : null;
    this.setData({ session: matched });
    this.applyRewards(matched);
  },

  help() {
    if (!this.shareToken) {
      wx.showToast({ title: '助力链接无效', icon: 'none' });
      return;
    }
    if (api.isProduction()) {
      api.helpAssist(this.shareToken, BUSINESS_TYPE).then((result) => {
        const next = toLocalSession(result.data, this.shareToken);
        this.setData({ session: next });
        this.applyRewards(next);
        wx.showToast({ title: '助力成功', icon: 'success' });
      }).catch((error) => {
        console.error('help assist failed', error);
        wx.showToast({ title: error && error.userMessage ? error.userMessage : '助力失败，请稍后重试', icon: 'none' });
      });
      return;
    }
    const session = toLocalSession(storage.getAssist());
    if (!session || session.token !== this.shareToken) { wx.showToast({ title: '助力任务不存在', icon: 'none' }); return; }
    if (session.starterId === 'demo_user_001') { wx.showToast({ title: '不能给自己助力', icon: 'none' }); return; }
    session.status = 'success';
    session.helperId = 'demo_user_001';
    session.helperCount = 1;
    storage.saveAssist(session);
    const coupons = storage.getCoupons();
    if (!coupons.some(coupon => coupon.id === `${session.id}_starter`)) coupons.push({ id: `${session.id}_starter`, name: '发起者助力券', description: rewardSummary(session.starterReward), discountAmount: session.starterReward && session.starterReward.discountAmount, minGoodsAmount: session.starterReward && session.starterReward.minGoodsAmount, status: 'AVAILABLE', source: 'ASSIST', sourceRole: 'STARTER', expireAt: session.starterReward && session.starterReward.templateEndsAt });
    if (!coupons.some(coupon => coupon.id === `${session.id}_helper`)) coupons.push({ id: `${session.id}_helper`, name: '好友助力券', description: rewardSummary(session.helperReward), discountAmount: session.helperReward && session.helperReward.discountAmount, minGoodsAmount: session.helperReward && session.helperReward.minGoodsAmount, status: 'AVAILABLE', source: 'ASSIST', sourceRole: 'HELPER', expireAt: session.helperReward && session.helperReward.templateEndsAt });
    storage.saveCoupons(coupons);
    this.setData({ session });
    this.applyRewards(session);
    wx.showToast({ title: '助力成功', icon: 'success' });
  }
});
