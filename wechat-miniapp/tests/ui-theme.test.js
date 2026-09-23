const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '../miniprogram');
const read = relative => fs.readFileSync(path.join(root, relative), 'utf8');

test('all theme image references resolve to packaged local assets', () => {
  const app = JSON.parse(read('app.json'));
  let count = 0;
  for (const page of app.pages) {
    for (const match of read(page + '.wxml').matchAll(/src="(\/images\/theme\/[^"{}]+)"/g)) {
      assert.ok(fs.existsSync(path.join(root, match[1])), `${page}: missing ${match[1]}`);
      count++;
    }
  }
  assert.ok(count > 25);
  for (const tab of app.tabBar.list) {
    assert.ok(fs.existsSync(path.join(root, tab.iconPath)));
    assert.ok(fs.existsSync(path.join(root, tab.selectedIconPath)));
  }
});

test('home add state supports unlimited inventory, manual sold out and closed stores', () => {
  const source = read('pages/index/index.wxml');
  const expression = /class="add-button \{\{([\s\S]*?)\}\}"/.exec(source)[1];
  const state = (item, status = 'OPEN') => vm.runInNewContext(expression, { item, channelOpen: status === 'OPEN' });
  assert.equal(state({ stockMode: 'UNLIMITED', stock: 0 }), '');
  assert.equal(state({ stockMode: 'UNLIMITED', stock: 0, isSoldOut: true }), 'add-button--disabled');
  assert.equal(state({ stock: 0 }), 'add-button--disabled');
  assert.equal(state({ stock: 3 }), '');
  assert.equal(state({ stock: 3 }, 'PAUSED'), 'add-button--disabled');
});

test('home promotion uses live business information without fixed campus or coupon promises', () => {
  const source = read('pages/index/index.wxml');
  assert.ok(source.includes("store.name || brand.name"));
  assert.ok(source.includes('{{store.announcement}}'));
  assert.ok(read('services/catalog-page.js').includes('配送费用以结算页为准'));
  assert.ok(!source.includes('东校区'));
  assert.ok(!/满\s*25\s*减\s*3/.test(source));
});

test('theme keeps the original routes and demo deployment mode', () => {
  const app = JSON.parse(read('app.json'));
  assert.equal(app.pages.length, 12);
  assert.equal(app.pages.some(page => page.startsWith('pages/merchant/')), false);
  assert.deepEqual(app.tabBar.list.map(tab => tab.text), ['餐饮', '零售', '订单', '我的']);
  assert.match(read('app.js'), /deploymentMode:\s*'demo'/);
  assert.equal((read('app.wxss').match(/^page\s*\{/gm) || []).length, 1);
});

test('consumer copy stays neutral while legacy campus addresses remain supported', () => {
  const app = JSON.parse(read('app.json'));
  const addressEdit = read('pages/address/edit/index.js');
  const addressList = read('pages/address/list/index.wxml');
  const coldConfig = JSON.parse(read('pages/cold/index.json'));
  const cold = read('pages/cold/index.wxml');
  const orderDetail = read('pages/order/detail/index.wxml');
  assert.deepEqual(app.tabBar.list.map(tab => tab.text), ['餐饮', '零售', '订单', '我的']);
  assert.equal(coldConfig.navigationBarTitleText, '零售 · 琑遇·二两兔');
  assert.match(cold, /零售商品/);
  assert.doesNotMatch(cold, /冷吃商品/);
  assert.match(orderDetail, /配送费/);
  assert.doesNotMatch(orderDetail, /跑腿费/);
  assert.match(addressEdit, /addressType === 'CAMPUS'/);
  assert.match(addressEdit, /请补充配送区域信息/);
  assert.match(addressList, /配送地址/);
  assert.doesNotMatch(addressEdit, /请填写校区和配送区域/);
});

test('page actions are distinct and narrow layouts have explicit safeguards', () => {
  const home = read('pages/index/index.wxml');
  const cold = read('pages/cold/index.wxml');
  const profile = read('pages/profile/index/index.wxml');
  const profileLogic = read('pages/profile/index/index.js');
  const checkout = read('pages/checkout/index.wxml');
  const merchant = read('pages/merchant/index/index.wxml');
  const productEdit = read('pages/merchant/product-edit/index.wxml');
  const common = read('styles/common.wxss');
  const homeStyle = read('pages/index/index.wxss');
  const productEditStyle = read('pages/merchant/product-edit/index.wxss');
  const productListStyle = read('pages/merchant/products/index.wxss');
  const merchantStyle = read('pages/merchant/index/index.wxss');
  const productDetail = read('pages/product/detail/index.wxml');
  const productDetailStyle = read('pages/product/detail/index.wxss');
  const cart = read('pages/cart/index.wxml');
  const orderList = read('pages/order/list/index.wxml');
  const orderListStyle = read('pages/order/list/index.wxss');
  const orderDetail = read('pages/order/detail/index.wxml');
  const orderDetailStyle = read('pages/order/detail/index.wxss');
  const appLogic = read('app.js');
  const api = read('services/api.js');

  assert.doesNotMatch(home, /channel-switch/);
  assert.doesNotMatch(cold, /channel-switch/);
  assert.doesNotMatch(profile, /店主工作台|goMerchant|canMerchant/);
  assert.doesNotMatch(profileLogic, /goMerchant|canMerchant|getBootstrap/);
  assert.equal((profile.match(/bindtap="openContact"/g) || []).length, 1);
  assert.doesNotMatch(profile, /配送问题请联系门店/);
  assert.match(profile, /show-menu-by-longpress/);
  assert.match(profile, /bindtap="copyWechatID"/);
  assert.doesNotMatch(appLogic, /isStaff|permissions/);
  assert.doesNotMatch(api, /getBootstrap/);
  for (const tab of ['ALL', 'WAIT_PAY', 'PROCESSING', 'DELIVERING', 'COMPLETED']) assert.match(profile, new RegExp(`data-tab="${tab}"`));
  assert.doesNotMatch(checkout, /sheet-confirm/);
  assert.match(checkout, /class="sheet-close"[^>]+aria-label="关闭"/);
  assert.doesNotMatch(merchant, /item\.status === 'REFUNDING'[^\n]+bindtap="refundOrder"/);
  assert.match(merchant, /item\.status === 'REFUNDING'[^\n]+bindtap="queryRefund"/);
  assert.match(productEdit, /class="option-editor-fields"/);
  assert.match(productEdit, /class="option-remove"/);
  assert.match(common, /overflow-x:\s*hidden/);
  assert.match(common, /@media \(max-width: 360px\)/);
  assert.match(homeStyle, /\.hero-copy[^}]+min-width:\s*0/);
  assert.doesNotMatch(homeStyle, /\.hero-title[^}]+white-space:\s*nowrap/);
  assert.match(productEditStyle, /grid-template-columns:\s*minmax\(0, 1fr\)/);
  assert.match(productListStyle, /\.stock-row[^}]+flex-wrap:\s*wrap/);
  assert.doesNotMatch(merchantStyle, /\.merchant-order-actions[^}]+max-width/);
  assert.match(productDetail, /wx:for="\{\{product\.detailImageUrls\}\}"/);
  assert.match(productDetail, /class="detail-gallery-image"[^>]+mode="widthFix"/);
  assert.match(productDetailStyle, /\.detail-gallery-image[^}]+width:\s*100%/);
  for (const surface of [cart, checkout, orderList, orderDetail]) {
    assert.match(surface, /coverImageUrl/);
    assert.match(surface, /mode="aspectFill"/);
  }
  assert.match(orderListStyle, /\.order-item-visual[^}]+flex-shrink:\s*0[^}]+height:\s*72rpx[^}]+overflow:\s*hidden[^}]+width:\s*72rpx/);
  assert.match(orderDetailStyle, /\.detail-line-visual[^}]+flex-shrink:\s*0[^}]+height:\s*84rpx[^}]+overflow:\s*hidden[^}]+width:\s*84rpx/);
});
