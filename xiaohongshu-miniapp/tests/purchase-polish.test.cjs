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

function loadConfirmationPage(app) {
  let definition;
  const source = fs.readFileSync(path.join(miniappRoot, 'pages/order/confirm.js'), 'utf8');
  vm.runInNewContext(source, {
    Page: value => { definition = value; },
    getApp: () => app,
    xhs: {},
    require: request => {
      if (request === '../../utils/calendar') return calendar;
      if (request === '../../utils/payment') return { requestGuaranteeOrderPayment() {} };
      if (request === '../../utils/promotion') return require(path.join(miniappRoot, 'utils/promotion.js'));
      throw new Error(`unexpected require: ${request}`);
    },
    Date, Math, Number, String, Boolean, RegExp, encodeURIComponent
  }, { filename: 'pages/order/confirm.js' });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (update, callback) => { Object.assign(page.data, update); if (callback) callback(); };
  return page;
}

test('confirmation retry restores an initial catalog failure without replacing the order request id', async () => {
  let attempts = 0;
  const app = {
    globalData: {},
    setNavigationTitle() {},
    setStoreName() {},
    request: () => {
      attempts += 1;
      if (attempts === 1) return Promise.reject(new Error('网络暂不可用'));
      return Promise.resolve({ products: [{ id: 7, name: '后端商品', product_kind: 'ticket', price_cents: 8000 }] });
    }
  };
  const page = loadConfirmationPage(app);
  page.mappingId = 7;
  page.requestedQuantity = 1;
  page.requestedUseDate = '';
  page.orderRequestId = 'stable-request-id';

  page.loadProduct();
  await flush();
  assert.equal(page.data.loading, false);
  assert.match(page.data.error, /网络暂不可用/);

  page.retry();
  await flush();
  assert.equal(attempts, 3);
  assert.equal(page.data.loading, false);
  assert.equal(page.data.error, '');
  assert.equal(page.data.product.name, '后端商品');
  assert.equal(page.orderRequestId, 'stable-request-id');
});

test('purchase templates keep recovery while confirmation owns selection controls', () => {
  const confirmTemplate = fs.readFileSync(path.join(miniappRoot, 'pages/order/confirm.xhsml'), 'utf8');
  const detailTemplate = fs.readFileSync(path.join(miniappRoot, 'pages/product/detail.xhsml'), 'utf8');
  assert.match(confirmTemplate, /class="retry-button" bindtap="retry"/);
  assert.match(confirmTemplate, /class="date-card"/);
  assert.doesNotMatch(detailTemplate, /purchase-sheet|sheet-mask|sheet-scroll/);
});
