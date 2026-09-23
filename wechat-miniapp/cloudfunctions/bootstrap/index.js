const cloud = require('wx-server-sdk');

cloud.init({ env: cloud.DYNAMIC_CURRENT_ENV });
const db = cloud.database({ throwOnNotFound: false });
const _ = db.command;

async function getUser(openid) {
  const result = await db.collection('users').doc(openid).get().catch(() => null);
  return result && result.data ? result.data : null;
}

async function ensureUser(openid) {
  const tx = await db.startTransaction();
  try {
    const current = (await tx.collection('users').doc(openid).get()).data;
    const now = db.serverDate();
    const user = current || { _id: openid, nickname: '同学', avatarUrl: '', phone: '', role: 'CUSTOMER', status: 'ACTIVE', createdAt: now, updatedAt: now };
    if (!current) await tx.collection('users').doc(openid).set({ data: user });
    await tx.commit();
    return user;
  } catch (error) { await tx.rollback(); throw error; }
}

async function getStore() {
  const result = await db.collection('store_settings').doc('store_001').get();
  if (!result.data) throw new Error('STORE_NOT_CONFIGURED');
  return result.data;
}

exports.main = async (event) => {
  const { OPENID: openid } = cloud.getWXContext();
  const action = event.action || 'getBootstrap';
  if (!openid) throw new Error('UNAUTHORIZED');
  const user = await ensureUser(openid);
  if (action === 'updateProfile') {
    const profile = event.profile || {};
    await db.collection('users').doc(openid).update({ data: { nickname: profile.nickname || user.nickname, avatarUrl: profile.avatarUrl || user.avatarUrl, updatedAt: db.serverDate() } });
    return { success: true };
  }
  const staff = await db.collection('staff').where({ userId: openid, storeId: 'store_001', enabled: true }).limit(1).get().catch(() => ({ data: [] }));
  return { success: true, user, store: await getStore(), isStaff: staff.data.length > 0, permissions: staff.data[0] ? staff.data[0].permissions || [] : [] };
};
