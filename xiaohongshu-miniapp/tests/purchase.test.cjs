const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');
const calendar = require(path.join(miniappRoot, 'utils/calendar.js'));

function flush() {
  return new Promise(resolve => setImmediate(resolve));
}

function loadPage(relativePath, app, xhs) {
  let definition;
  const source = fs.readFileSync(path.join(miniappRoot, relativePath), 'utf8');
  vm.runInNewContext(source, {
    Page: value => { definition = value; }, getApp: () => app, xhs,
    require: request => {
      if (request === '../../utils/calendar') return calendar;
      if (request === '../../utils/payment') return require(path.join(miniappRoot, 'utils/payment.js'));
      throw new Error(`unexpected require: ${request}`);
    },
    Date, Math, Number, String, Boolean, RegExp, encodeURIComponent, setTimeout: () => 1, clearTimeout: () => {}
  }, { filename: relativePath });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (update, callback) => { Object.assign(page.data, update); if (callback) callback(); };
  return page;
}

test('calendar handles leap days and preserves cross-month bounds', () => {
  assert.equal(calendar.formatDate(calendar.addDays('2028-02-28', 1)), '2028-02-29');
  assert.equal(calendar.isDateWithin('2028-03-01', '2028-02-29', '2028-03-01'), true);
  assert.equal(calendar.isDateWithin('2028-02-28', '2028-02-29', '2028-03-01'), false);
  const cells = calendar.buildCalendarCells('2028-02-01', '2028-02-29', '2028-03-01', '2028-02-29');
  assert.equal(cells.find(cell => cell.iso === '2028-02-29').className, 'calendar-day selected');
});

test('calendar rejects invalid days, changes year correctly and bounds month navigation', () => {
  assert.equal(calendar.parseDate('2027-02-29'), null);
  assert.equal(calendar.parseDate('2028-02-30'), null);
  assert.equal(calendar.formatDate(calendar.addDays('2026-12-31', 1)), '2027-01-01');
  assert.equal(calendar.canMoveMonth('2026-12-01', 1, '2026-12-30', '2027-01-02'), true);
  assert.equal(calendar.canMoveMonth('2027-01-01', 1, '2026-12-30', '2027-01-02'), false);
  assert.equal(calendar.canMoveMonth('2026-12-01', -1, '2026-12-30', '2027-01-02'), false);
  assert.equal(calendar.buildCalendarCells('2026-10-01').length, 35);
});

test('detail quantity limit respects the API maximum of 100', () => {
  const page = loadPage('pages/product/detail.js', {}, {});
  assert.equal(page.deriveMaxQuantity(80000, 1), 100);
  assert.equal(page.deriveMaxQuantity(80000, 8000), 10);
});

test('detail carries a chosen date and quantity to confirmation without merchant demo fields', () => {
  let target = '';
  const app = { globalData: {}, setNavigationTitle() {}, setStoreName() {}, request: () => Promise.resolve({}) };
  const page = loadPage('pages/product/detail.js', app, { navigateTo: input => { target = input.url; } });
  page.data.product = { id: 9, requiresUseDate: true, isPackage: false };
  page.data.useDate = '2030-03-01'; page.data.quantity = 3; page.data.minDate = '2030-01-01'; page.data.maxDate = '2030-12-31';
  page.continuePurchase();
  assert.equal(target, '/pages/order/confirm?mapping_id=9&quantity=3&use_date=2030-03-01');
  assert.equal(fs.readFileSync(path.join(miniappRoot, 'pages/product/detail.xhsml'), 'utf8').includes('云门'), false);
});

test('detail renders merchant and product presentation data returned by the catalog', async () => {
  const catalog = {
    store_name: '山水商家',
    products: [{ id: 9, name: '后端商品名', image_url: 'https://example.test/product.jpg', description: '后端商品介绍', product_kind: 'ticket', price_cents: 8800, tags: [] }]
  };
  const app = { globalData: {}, setNavigationTitle() {}, setStoreName(value) { this.savedName = value; }, request: () => Promise.resolve(catalog) };
  const page = loadPage('pages/product/detail.js', app, {});
  page.mappingId = 9;
  page.loadProduct();
  await flush();
  assert.equal(page.data.storeName, '山水商家');
  assert.equal(app.savedName, '山水商家');
  assert.equal(page.data.product.name, '后端商品名');
  assert.equal(page.data.product.image_url, 'https://example.test/product.jpg');
  assert.equal(page.data.product.description, '后端商品介绍');
});

test('confirmation keeps deferred packages date-free and submits the stable order request', () => {
  const requests = [];
  const app = {
    globalData: {}, setNavigationTitle() {}, setStoreName() {},
    request: (url, options) => { requests.push({ url, options }); return Promise.resolve({ order_no: 'ORD-1' }); }
  };
  const page = loadPage('pages/order/confirm.js', app, {});
  page.data.product = { id: 7, price_cents: 5000, isPackage: true, isDeferredPackage: true, requiresUseDate: false };
  page.data.quantity = 2; page.orderRequestId = 'stable-request-id';
  page.submit();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.data.use_date, '');
  assert.equal(requests[0].options.data.request_id, 'stable-request-id');
});

test('booking rejects a locally out-of-bounds date before it requests the API', () => {
  let calls = 0;
  const app = { globalData: {}, setNavigationTitle() {}, request: () => { calls += 1; return Promise.resolve({}); } };
  const page = loadPage('pages/booking/index.js', app, {});
  page.data.checkInDate = '2028-03-03'; page.data.minDate = '2028-03-04'; page.data.maxDate = '2028-03-12';
  page.submit();
  assert.equal(calls, 0);
  assert.match(page.data.error, /可预约/);
});
