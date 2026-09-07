const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const root = path.join(__dirname, '..', 'xiaohongshu-miniapp');

function catalogPage(catalog) {
  let definition;
  let title;
  vm.runInNewContext(fs.readFileSync(path.join(root, 'pages/index/index.js'), 'utf8'), {
    Page: value => { definition = value; },
    getApp: () => ({ request: () => Promise.resolve(catalog), setStoreName: value => { title = value; } })
  });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (data, callback) => { Object.assign(page.data, data); if (callback) callback(); };
  return { page, title: () => title };
}

test('catalog merchant and imagery come from the response, no sample image fallback', async () => {
  const { page, title } = catalogPage({ store_name: '另一家景区', products: [{ id: 1, name: '体验门票', product_kind: 'ticket', price_cents: 1500 }] });
  await page.loadCatalog();
  assert.equal(title(), '另一家景区');
  assert.equal(page.data.storeName, '另一家景区');
  assert.equal(page.data.heroImage, '');
  assert.equal(page.data.products[0].priceText, '15');
  const pictured = catalogPage({ store_name: '独立商户', products: [{ id: 2, name: '套餐', product_kind: 'scenic_hotel_package', price_cents: 15999, image_url: 'https://merchant.example/product.jpg' }] }).page;
  await pictured.loadCatalog();
  assert.equal(pictured.data.heroImage, '');
  assert.equal(pictured.data.products[0].priceText, '159.99');
});

test('storefront and product image fields never fall back to or overwrite each other', async () => {
  const catalog = { store_name: '独立商户', storefront_image_url: 'https://merchant.example/storefront.jpg', products: [
    { id: 1, name: '商品甲', product_kind: 'ticket', price_cents: 8000, image_url: 'https://merchant.example/product-a.jpg' },
    { id: 2, name: '商品乙', product_kind: 'ticket', price_cents: 9000, image_url: '' }
  ] };
  const { page } = catalogPage(catalog);
  await page.loadCatalog();
  assert.equal(page.data.heroImage, catalog.storefront_image_url);
  assert.equal(page.data.products[0].image_url, 'https://merchant.example/product-a.jpg');
  assert.equal(page.data.products[1].image_url, '');
  catalog.products[0].image_url = 'https://merchant.example/replaced-product.jpg';
  await page.loadCatalog();
  assert.equal(page.data.heroImage, 'https://merchant.example/storefront.jpg');
  catalog.storefront_image_url = '';
  await page.loadCatalog();
  assert.equal(page.data.heroImage, '');
  assert.equal(page.data.products[0].image_url, 'https://merchant.example/replaced-product.jpg');
  catalog.storefront_image_url = 'https://merchant.example/storefront.jpg';
  catalog.products = [];
  await page.loadCatalog();
  assert.equal(page.data.heroImage, 'https://merchant.example/storefront.jpg');
  assert.equal(page.data.products.length, 0);
});

test('catalog retains combined search, scenic, kind and price filtering', async () => {
  const { page } = catalogPage({ products: [
    { id: 1, name: '成人门票', product_kind: 'ticket', scenic_area_name: '景区甲', price_cents: 8000 },
    { id: 2, name: '亲子门票', product_kind: 'ticket', scenic_area_name: '景区乙', price_cents: 12000 },
    { id: 3, name: '住宿套餐', product_kind: 'scenic_hotel_package', scenic_area_name: '景区甲', price_cents: 30000 }
  ] });
  await page.loadCatalog();
  page.selectSort({ currentTarget: { dataset: { sort: 'priceDesc' } } });
  assert.equal(page.data.products[0].id, 3);
  page.selectScenic({ currentTarget: { dataset: { scenic: '景区甲' } } });
  page.selectKind({ currentTarget: { dataset: { kind: 'ticket' } } });
  assert.equal(page.data.products.length, 1);
  assert.equal(page.data.products[0].id, 1);
  page.onKeywordInput({ detail: { value: '不存在' } });
  assert.equal(page.data.products.length, 0);
  page.resetFilters();
  assert.equal(page.data.products.length, 3);
  assert.equal(page.data.hasActiveFilters, false);
});

test('catalog shows useful choices only and production templates contain no demo merchant copy', () => {
  const template = fs.readFileSync(path.join(root, 'pages/index/index.xhsml'), 'utf8');
  assert.match(template, /scenicOptions.length > 2/);
  assert.match(template, /allProducts.length > 1/);
  for (const file of ['pages/index/index.xhsml', 'pages/product/detail.xhsml', 'pages/order/confirm.xhsml', 'pages/order/detail.xhsml', 'pages/orders/index.xhsml']) {
    assert.doesNotMatch(fs.readFileSync(path.join(root, file), 'utf8'), /云门山|山水之间|每人一票一码|小红书官方担保/);
  }
});
