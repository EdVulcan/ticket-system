const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const { createRequire } = require('node:module');
const brand = require('../miniprogram/config/brand');

function storageHarness(mode, entries) {
  const values = new Map(entries);
  const filename = path.resolve(__dirname, '../miniprogram/services/storage.js');
  const module = { exports: {} };
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), {
    module, require: createRequire(filename),
    getApp: () => ({ globalData: { deploymentMode: mode, env: 'test' } }),
    wx: { getStorageSync: key => values.get(key), setStorageSync: (key, value) => values.set(key, value) }
  });
  return { storage: module.exports, values };
}

test('formal brand is consistent in app navigation, demo defaults and deployment seed', () => {
  const app = require('../miniprogram/app.json');
  const seed = require('../database/seed-data.json');
  const mock = require('../miniprogram/data/mock');
  assert.equal(brand.name, '琑遇·二两兔');
  assert.equal(app.window.navigationBarTitleText, brand.name);
  assert.equal(seed.store_settings[0].name, brand.name);
  assert.equal(mock.store.name, brand.name);
  assert.equal(mock.store.slogan, brand.tagline);
});

test('legacy demo name upgrades without overwriting saved settings or transaction data', () => {
  const store = { name: brand.legacyDemoName, phone: '123', businessStatus: 'PAUSED', defaultDeliveryFee: 0 };
  const cart = [{ productId: 'p_001', quantity: 2 }];
  const orders = [{ id: 'existing-order' }];
  const { storage, values } = storageHarness('demo', [['food_store_v1', store], ['food_cart_v1', cart], ['food_orders_v1', orders]]);
  const upgraded = storage.getStore();
  assert.equal(upgraded.name, brand.name);
  assert.equal(upgraded.phone, store.phone);
  assert.equal(upgraded.businessStatus, 'PAUSED');
  assert.equal(upgraded.defaultDeliveryFee, 0);
  assert.equal(storage.getCart(), cart);
  assert.equal(storage.getOrders(), orders);
  assert.equal(values.get('food_store_v1'), store);
});

test('custom and production store names stay authoritative', () => {
  const custom = { name: '用户自定义店名' };
  assert.equal(storageHarness('demo', [['food_store_v1', custom]]).storage.getStore(), custom);
  const remote = { name: brand.legacyDemoName, businessStatus: 'CLOSED' };
  assert.equal(storageHarness('production', [['production_test_food_store_v1', remote]]).storage.getStore(), remote);
  assert.equal(storageHarness('production', []).storage.getStore().businessStatus, 'PAUSED');
});

test('homepage and assist share cards use the formal brand while keeping their routes', () => {
  for (const [route, expected] of [['index/index', brand.shareTitle], ['assist/index/index', brand.assistShareTitle]]) {
    const filename = path.resolve(__dirname, '../miniprogram/pages', route + '.js');
    let page;
    vm.runInNewContext(fs.readFileSync(filename, 'utf8'), { require: createRequire(filename), Page: value => { page = value; } });
    const share = page.onShareAppMessage();
    assert.equal(share.title, expected);
    assert.ok(share.path.startsWith('/pages/'));
    assert.equal(page.data.brand.name, brand.name);
  }
});
