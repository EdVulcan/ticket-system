const crypto = require('crypto');
const cloud = require('wx-server-sdk');

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database({ throwOnNotFound: false });

const STORE_ID = 'store_001';

function fail(message) {
  const error = new Error(message);
  error.code = message;
  throw error;
}

function hash(value) {
  return crypto.createHash('sha256').update(value).digest('hex').slice(0, 40);
}

function activeCampaign(campaign) {
  if (!campaign || campaign.storeId !== STORE_ID || campaign.enabled !== true) return false;
  const now = Date.now();
  return (!campaign.startAt || new Date(campaign.startAt).getTime() <= now) && (!campaign.endAt || new Date(campaign.endAt).getTime() >= now);
}

function safeSession(session) {
  return {
    id: session._id,
    campaignId: session.campaignId,
    shareToken: session.shareToken,
    status: session.status,
    helperCount: Number(session.helperCount || 0),
    expireAt: session.expireAt,
    createdAt: session.createdAt,
    successAt: session.successAt || ''
  };
}

async function getCampaign(campaignId) {
  if (!campaignId) {
    const store = (await db.collection('store_settings').doc(STORE_ID).get()).data;
    if (store && Object.prototype.hasOwnProperty.call(store, 'activeCampaignId')) {
      if (!store.activeCampaignId) return null;
      const selected = (await db.collection('assist_campaigns').doc(store.activeCampaignId).get()).data;
      return activeCampaign(selected) ? selected : null;
    }
  }
  const result = campaignId ? await db.collection('assist_campaigns').doc(campaignId).get() : await db.collection('assist_campaigns').where({ storeId: STORE_ID, enabled: true }).orderBy('startAt', 'desc').limit(20).get();
  if (campaignId) return activeCampaign(result.data) ? result.data : null;
  return result.data.find(activeCampaign) || null;
}

async function couponTemplate(templateId, database, allowDisabled = false) {
  const client = database || db;
  const result = await client.collection('coupon_templates').doc(templateId).get();
  if (!result.data || result.data.storeId !== STORE_ID || (!allowDisabled && result.data.enabled === false)) fail('COUPON_TEMPLATE_NOT_FOUND');
  return result.data;
}

function couponData(id, userId, campaign, template, role) {
  const validDays = Number(template.validDays || 5);
  if (!Number.isInteger(validDays) || validDays < 1 || validDays > 365 || !Number.isSafeInteger(template.discountAmount) || template.discountAmount <= 0 || !Number.isSafeInteger(template.minGoodsAmount) || template.minGoodsAmount < 0) fail('COUPON_TEMPLATE_INVALID');
  return {
    _id: id,
    userId,
    storeId: STORE_ID,
    templateId: template._id,
    campaignId: campaign._id,
    name: template.name,
    description: `满 ${Number(template.minGoodsAmount || 0) / 100} 元可用${template.excludeDeliveryFee === false ? '' : ' · 不抵扣跑腿费'}`,
    discountAmount: Number(template.discountAmount || 0),
    minGoodsAmount: Number(template.minGoodsAmount || 0),
    scope: template.scope || 'UNIVERSAL',
    excludeDeliveryFee: template.excludeDeliveryFee !== false,
    status: 'AVAILABLE',
    source: 'ASSIST',
    sourceRole: role,
    receivedAt: db.serverDate(),
    expireAt: new Date(Date.now() + validDays * 24 * 60 * 60 * 1000)
  };
}

async function ensureCoupon(transaction, id, data) {
  const result = await transaction.collection('user_coupons').doc(id).get();
  if (result.data) {
    if (result.data.userId !== data.userId || result.data.campaignId !== data.campaignId) fail('COUPON_ISSUE_CONFLICT');
    if (result.data.status === 'USED' || result.data.status === 'LOCKED') fail('COUPON_ALREADY_USED');
    return;
  }
  await transaction.collection('user_coupons').doc(id).set({ data });
}

exports.main = async (event) => {
  const { OPENID: openid } = cloud.getWXContext();
  if (!openid) fail('UNAUTHORIZED');

  if (event.action === 'getCampaign') {
    const campaign = await getCampaign();
    if (!campaign) return { success: true, data: null };
    const template = await couponTemplate(campaign.starterCouponTemplateId);
    return { success: true, data: { id: campaign._id, name: campaign.name || template.name, requiredHelpers: 1, endAt: campaign.endAt || '', discountAmount: template.discountAmount, minGoodsAmount: template.minGoodsAmount, validDays: template.validDays, excludeDeliveryFee: template.excludeDeliveryFee !== false } };
  }

  if (event.action === 'createSession') {
    const campaign = await getCampaign();
    if (!campaign) fail('CAMPAIGN_NOT_FOUND');
    const sessionId = `assist_${hash(`${campaign._id}:${openid}`)}`;
    if (Number(campaign.requiredHelpers || 1) !== 1) fail('CAMPAIGN_CONFIG_INVALID');
    const durationHours = Math.max(1, Number(campaign.sessionValidHours || 24));
    const endAt = campaign.endAt ? new Date(campaign.endAt).getTime() : Date.now() + durationHours * 60 * 60 * 1000;
    const session = {
      _id: sessionId,
      campaignId: campaign._id,
      starterId: openid,
      shareToken: `SG${hash(`${sessionId}:${Date.now()}`).slice(0, 16)}`,
      status: 'ACTIVE',
      helperId: '',
      helperCount: 0,
      starterCouponTemplateId: campaign.starterCouponTemplateId,
      helperCouponTemplateId: campaign.helperCouponTemplateId || campaign.starterCouponTemplateId,
      createdAt: db.serverDate(),
      expireAt: new Date(Math.min(Date.now() + durationHours * 60 * 60 * 1000, endAt))
    };
    const transaction = await db.startTransaction();
    try {
      const existing = await transaction.collection('assist_sessions').doc(sessionId).get();
      if (!existing.data) await transaction.collection('assist_sessions').doc(sessionId).set({ data: session });
      await transaction.commit();
    } catch (error) { await transaction.rollback(); throw error; }
    const saved = await db.collection('assist_sessions').doc(sessionId).get();
    return { success: true, data: safeSession(saved.data) };
  }

  if (event.action === 'helpAssist') {
    if (typeof event.sessionId !== 'string' || !event.sessionId) fail('SESSION_INVALID');
    const transaction = await db.startTransaction();
    try {
      const sessionResult = await transaction.collection('assist_sessions').doc(event.sessionId).get();
      const session = sessionResult.data;
      if (!session || session.status !== 'ACTIVE' || (session.expireAt && new Date(session.expireAt).getTime() <= Date.now())) fail('SESSION_EXPIRED');
      if (session.starterId === openid) fail('CANNOT_HELP_SELF');
      const campaignResult = await transaction.collection('assist_campaigns').doc(session.campaignId).get();
      const campaign = campaignResult.data;
      if (!activeCampaign(campaign)) fail('CAMPAIGN_NOT_ACTIVE');
      if (Number(campaign.requiredHelpers || 1) !== 1) fail('CAMPAIGN_CONFIG_INVALID');
      const recordId = `assist_record_${hash(`${session.campaignId}:${openid}`)}`;
      const existingRecord = await transaction.collection('assist_records').doc(recordId).get();
      if (existingRecord.data) fail('ALREADY_HELPED');
      const starterTemplate = await couponTemplate(session.starterCouponTemplateId || campaign.starterCouponTemplateId, transaction, true);
      const helperTemplate = await couponTemplate(session.helperCouponTemplateId || campaign.helperCouponTemplateId || campaign.starterCouponTemplateId, transaction, true);
      const starterCouponId = `coupon_${hash(`${session.campaignId}:${session.starterId}:STARTER`)}`;
      const helperCouponId = `coupon_${hash(`${session.campaignId}:${openid}:HELPER`)}`;
      await ensureCoupon(transaction, starterCouponId, couponData(starterCouponId, session.starterId, campaign, starterTemplate, 'STARTER'));
      await ensureCoupon(transaction, helperCouponId, couponData(helperCouponId, openid, campaign, helperTemplate, 'HELPER'));
      await transaction.collection('assist_records').doc(recordId).set({ data: { _id: recordId, storeId: 'store_001', campaignId: session.campaignId, sessionId: session._id, starterId: session.starterId, helperId: openid, result: 'SUCCESS', starterCouponId, helperCouponId, createdAt: db.serverDate() } });
      const helperCount = Number(session.helperCount || 0) + 1;
      const requiredHelpers = Math.max(1, Number(campaign.requiredHelpers || 1));
      await transaction.collection('assist_sessions').doc(session._id).update({ data: { status: helperCount >= requiredHelpers ? 'SUCCESS' : 'ACTIVE', helperCount, helperId: openid, successAt: helperCount >= requiredHelpers ? db.serverDate() : '', updatedAt: db.serverDate() } });
      await transaction.commit();
      return { success: true, data: { status: helperCount >= requiredHelpers ? 'SUCCESS' : 'ACTIVE', helperCount } };
    } catch (error) {
      await transaction.rollback();
      throw error;
    }
  }

  if (event.action === 'getSession') {
    let result;
    if (event.sessionId) result = await db.collection('assist_sessions').doc(event.sessionId).get();
    else if (event.shareToken) result = await db.collection('assist_sessions').where({ shareToken: event.shareToken }).limit(1).get().then((response) => ({ data: response.data[0] }));
    else fail('SESSION_INVALID');
    if (!result.data) fail('SESSION_NOT_FOUND');
    const session = safeSession(result.data);
    const campaign = (await db.collection('assist_campaigns').doc(result.data.campaignId).get()).data;
    const templateId = result.data.starterCouponTemplateId || campaign && campaign.starterCouponTemplateId;
    if (templateId) {
      const template = await couponTemplate(templateId, undefined, true);
      session.couponTerms = { discountAmount: template.discountAmount, minGoodsAmount: template.minGoodsAmount, validDays: template.validDays, excludeDeliveryFee: template.excludeDeliveryFee !== false };
    }
    return { success: true, data: session };
  }

  fail('ACTION_NOT_SUPPORTED');
};
