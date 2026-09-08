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
    Page: value => { definition = value; },
    getApp: () => app,
    xhs,
    require: request => {
      if (request === '../../utils/calendar') return calendar;
      if (request === '../../utils/payment') return { requestGuaranteeOrderPayment() {} };
      throw new Error(`unexpected require: ${request}`);
    },
    Date, Math, Number, String, Boolean, RegExp, encodeURIComponent, setTimeout: () => 1, clearTimeout: () => {}
  }, { filename: relativePath });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = update => Object.assign(page.data, update);
  return page;
}

test('native button styling uses explicit disabled state classes instead of attribute selectors', () => {
  const appStyles = fs.readFileSync(path.join(miniappRoot, 'app.css'), 'utf8');
  const confirmTemplate = fs.readFileSync(path.join(miniappRoot, 'pages/order/confirm.xhsml'), 'utf8');

  assert.doesNotMatch(appStyles, /button\[disabled\]/);
  assert.match(appStyles, /button\.is-disabled/);
  assert.match(confirmTemplate, /class="stepper-button \{\{quantity <= 1 \|\| submitting \|\| createdOrderNo \? 'is-disabled' : ''\}\}"[^>]*disabled="\{\{quantity <= 1 \|\| submitting \|\| createdOrderNo\}\}"/);
  assert.match(confirmTemplate, /class="pay-button \{\{submitting \? 'is-disabled' : ''\}\}"[^>]*disabled="\{\{submitting\}\}"/);
});

test('detail purchase bypasses the obsolete selector popup and opens confirmation', () => {
  let target = '';
  const detailTemplate = fs.readFileSync(path.join(miniappRoot, 'pages/product/detail.xhsml'), 'utf8');
  const page = loadPage('pages/product/detail.js', { globalData: {} }, { navigateTo: input => { target = input.url; } });

  page.data.product = { id: 42 };
  page.buy();

  assert.equal(target, '/pages/order/confirm?mapping_id=42');
  assert.doesNotMatch(detailTemplate, /purchase-sheet|sheet-mask|sheet-scroll/);
  assert.equal(Object.hasOwn(page.data, 'purchaseOpen'), false);
});

test('confirmation applies the product max_quantity before allowing requested quantity', async () => {
  const app = {
    globalData: {},
    setNavigationTitle() {},
    setStoreName() {},
    request: () => Promise.resolve({
      products: [{ id: 42, name: '单券', product_kind: 'ticket', price_cents: 100, max_quantity: 1 }],
      max_order_cents: 10000
    })
  };
  const page = loadPage('pages/order/confirm.js', app, {});
  page.mappingId = 42;
  page.requestedQuantity = 9;
  page.requestedUseDate = '';
  page.loadProduct();
  await flush();

  assert.equal(page.data.maxQuantity, 1);
  assert.equal(page.data.quantity, 1);
});
