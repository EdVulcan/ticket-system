const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const root = path.join(__dirname, '../xiaohongshu-miniapp');
const promotion = require(path.join(root, 'utils/promotion'));
const flush = () => new Promise(resolve => setImmediate(resolve));
const offer = () => ({ status: 'available', grant_id: 7, discount_cents: 67,
  eligible_mapping_ids: [42], expires_at: new Date(Date.now() + 60000).toISOString() });
const quote = (discount = 67) => ({ original_amount_cents: 8000, amount_cents: 8000 - discount,
  discount_cents: discount, quote_token: 'server-quote', promotion: offer() });

function pageFor(file, request) {
  let definition;
  let tick;
  vm.runInNewContext(fs.readFileSync(path.join(root, file), 'utf8'), {
    Page: value => { definition = value; },
    getApp: () => ({ request, globalData: {}, setStoreName() {}, setNavigationTitle() {} }),
    require: () => promotion,
    setInterval: callback => { tick = callback; return 1; }, clearInterval() {}, clearTimeout() {}
  });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)), mappingId: 42 };
  page.setData = (data, callback) => { Object.assign(page.data, data); if (callback) callback(); };
  return { page, tick: () => tick() };
}

for (const [file, loader, product] of [
  ['pages/index/index.js', 'loadCatalog', page => page.data.products[0]],
  ['pages/product/detail.js', 'loadProduct', page => page.data.product]
]) {
  for (const promotionFirst of [true, false]) {
    test(`${file}: shows authoritative one-ticket discount regardless of response order (${promotionFirst})`, async () => {
      const requests = [];
      const { page } = pageFor(file, (route, options) => {
        if (route === '/catalog') return Promise.resolve({ products: [{ id: 42, price_cents: 8000, product_kind: 'ticket' }] });
        assert.equal(route, '/order-quote');
        requests.push(options.data);
        return Promise.resolve(quote(50)); // Server minimum-price cap differs from the drawn 67 cents.
      });
      if (promotionFirst) page.applyOpportunity(promotion.normalize(offer()));
      await page[loader]();
      await flush();
      if (!promotionFirst) page.applyOpportunity(promotion.normalize(offer()));
      await flush();
      assert.equal(product(page).hasPromotionPrice, true);
      assert.equal(product(page).displayPriceText, '79.50');
      assert.equal(product(page).originalPriceText, '80.00');
      assert.equal(product(page).promotionDiscountText, '0.50');
      assert.equal(requests.length, 1);
      assert.equal(requests[0].mapping_id, 42);
      assert.equal(requests[0].quantity, 1);
      page.applyOpportunity(promotion.normalize({ ...offer(), status: 'reserved', reserved_order_no: 'ORD-1' }));
      assert.equal(product(page).hasPromotionPrice, false);
      assert.equal(product(page).displayPriceText, '80');
    });
  }

  test(`${file}: late price response cannot restore expired or unloaded promotion`, async () => {
    let resolveQuote;
    const { page, tick } = pageFor(file, route => route === '/catalog'
      ? Promise.resolve({ products: [{ id: 42, price_cents: 8000 }] })
      : new Promise(resolve => { resolveQuote = resolve; }));
    await page[loader]();
    await flush();
    page.applyOpportunity(promotion.normalize(offer()));
    page.opportunity.expiresAt = Date.now() - 1;
    tick();
    resolveQuote(quote());
    await flush();
    assert.equal(product(page).hasPromotionPrice, false);
    page.applyOpportunity(promotion.normalize(offer()));
    page.onUnload();
    page.setData = () => assert.fail('late quote updated an unloaded page');
    resolveQuote(quote());
    await flush();
  });

  test(`${file}: fixed-price template has no starting-price suffix`, () => {
    const template = fs.readFileSync(path.join(root, file.replace('.js', '.xhsml')), 'utf8');
    assert.doesNotMatch(template, />起<\/text>/);
    assert.match(template, /displayPriceText/);
    assert.match(template, /originalPriceText/);
  });

  test(`${file}: price lookup failure keeps the original price and can recover on refresh`, async () => {
    let failed = true;
    const { page } = pageFor(file, route => route === '/catalog'
      ? Promise.resolve({ products: [{ id: 42, price_cents: 8000 }] })
      : failed ? Promise.reject(new Error('network unavailable')) : Promise.resolve(quote()));
    await page[loader]();
    await flush();
    await page.applyOpportunity(promotion.normalize(offer()));
    assert.equal(product(page).hasPromotionPrice, false);
    assert.equal(product(page).displayPriceText, '80');
    failed = false;
    await page.applyOpportunity(promotion.normalize(offer()));
    assert.equal(product(page).displayPriceText, '79.33');
  });
}

test('non-participating and reserved offers never request or display a discount price', async () => {
  const product = { id: 43, price_cents: 8000, priceText: '80' };
  const opportunity = promotion.normalize(offer());
  const unexpected = () => assert.fail('ineligible product triggered a price request');
  assert.deepEqual(await promotion.loadProductPrices(unexpected, [product], opportunity), {});
  const reserved = promotion.normalize({ ...offer(), status: 'reserved' });
  assert.deepEqual(await promotion.loadProductPrices(unexpected, [{ ...product, id: 42 }], reserved), {});
  const quoted = { ...quote(), opportunity };
  assert.equal(promotion.productPrice(product, opportunity, quoted).hasPromotionPrice, false);
  assert.equal(promotion.productPrice({ ...product, id: 42 }, reserved, quoted).hasPromotionPrice, false);
});

test('zero, inconsistent, changed-price and mismatched-grant quotes cannot advertise a saving', () => {
  const product = { id: 42, price_cents: 8000, priceText: '80' };
  const opportunity = promotion.normalize(offer());
  for (const invalid of [
    { ...quote(0), opportunity },
    { ...quote(), opportunity, amount_cents: 7000 },
    { ...quote(), opportunity, amount_cents: 8933, original_amount_cents: 9000 },
    { ...quote(), opportunity: { ...opportunity, grantId: 'another-grant' } }
  ]) {
    const view = promotion.productPrice(product, opportunity, invalid);
    assert.equal(view.hasPromotionPrice, false);
    assert.equal(view.displayPriceText, '80');
  }
});

test('catalog price sorting uses displayed amounts and returns to original order when promotion expires', async () => {
  const { page } = pageFor('pages/index/index.js', route => route === '/catalog'
    ? Promise.resolve({ products: [{ id: 42, price_cents: 8000 }, { id: 43, price_cents: 7970 }] })
    : Promise.resolve(quote()));
  await page.loadCatalog();
  await page.applyOpportunity(promotion.normalize(offer()));
  page.selectSort({ currentTarget: { dataset: { sort: 'priceAsc' } } });
  assert.equal(page.data.products[0].id, 42);
  await page.applyOpportunity(promotion.normalize({ ...offer(), status: 'expired' }));
  assert.equal(page.data.products[0].id, 43);
});
