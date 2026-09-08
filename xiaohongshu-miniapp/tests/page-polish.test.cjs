const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const test = require('node:test');
const root = path.join(__dirname, '..', 'xiaohongshu-miniapp');

function loadPage(relative, request) {
  let definition;
  const file = path.join(root, relative);
  vm.runInNewContext(fs.readFileSync(file, 'utf8'), {
    Page: value => { definition = value; },
    getApp: () => ({ request, globalData: {}, setStoreName() {}, setNavigationTitle() {} }),
    require: name => require(path.resolve(path.dirname(file), name)),
    xhs: { stopPullDownRefresh() {}, navigateTo() {}, redirectTo() {} }
  });
  const page = {...definition, data: JSON.parse(JSON.stringify(definition.data))};
  page.setData = (data, cb) => { Object.assign(page.data, data); if (cb) cb(); };
  return page;
}
const deferred = () => { let resolve; const promise = new Promise(r => {resolve=r;}); return {promise,resolve}; };
const product = {id:1,name:'门票',product_kind:'ticket',scenic_area_name:'景区',price_cents:100};

test('catalog refresh preserves results and filters, including after failure', async()=>{
  let fail=false;
  const page=loadPage('pages/index/index.js',()=>fail ? Promise.reject(new Error('网络失败')) : Promise.resolve({products:[product]}));
  await page.loadCatalog();
  page.onKeywordInput({detail:{value:'门票'}});
  const refresh=page.loadCatalog();
  assert.equal(page.data.loading,false);
  assert.equal(page.data.refreshing,true);
  assert.equal(page.data.products.length,1);
  await refresh;
  assert.equal(page.data.keyword,'门票');
  fail=true;await page.loadCatalog();
  assert.equal(page.data.products.length,1);
  assert.equal(page.data.error,'网络失败');
  assert.equal(page.data.refreshing,false);
});

test('catalog latest refresh wins and a removed scenic filter resets',async()=>{
  const first=deferred(),second=deferred();let n=0;
  const page=loadPage('pages/index/index.js',()=>++n===1?first.promise:second.promise);
  page.data.activeScenic='已移除景区';
  const a=page.loadCatalog(),b=page.loadCatalog();
  second.resolve({store_name:'当前商户',products:[product]});await b;
  first.resolve({store_name:'旧商户',products:[]});await a;
  assert.equal(page.data.storeName,'当前商户');
  assert.equal(page.data.activeScenic,'全部');
  assert.equal(page.data.products.length,1);
});

test('orders refresh keeps visible rows and stale pagination cannot replace refresh',async()=>{
  const stale=deferred();let n=0;
  const order={order_no:'ONE',status:'paid',amount_cents:100};
  const page=loadPage('pages/orders/index.js',()=>++n===1 ? stale.promise : Promise.resolve({items:[order],total:1}));
  page.data.allOrders=[order];page.applyStatus();
  const a=page.loadOrders(false),b=page.loadOrders(true);
  assert.equal(page.data.orders.length,1);
  assert.equal(page.data.loading,false);
  await b;
  stale.resolve({items:[{...order,order_no:'STALE'}],total:2});await a;
  assert.deepEqual(Array.from(page.data.orders,x=>x.order_no),['ONE']);
  assert.equal(page.data.page,1);
});

test('orders failed refresh retains rows and filter switches immediately',async()=>{
  const page=loadPage('pages/orders/index.js',()=>Promise.reject(new Error('离线')));
  page.data.allOrders=[{order_no:'ONE',status:'paid'}];page.data.total=1;page.applyStatus();
  await page.loadOrders(true);
  assert.equal(page.data.orders.length,1);
  assert.equal(page.data.error,'离线');
  page.selectStatus({currentTarget:{dataset:{status:'unpaid'}}});
  assert.equal(page.data.orders.length,0);
  page.showAll();assert.equal(page.data.orders.length,1);
});

test('booking selected stay derives checkout and submitting keeps selection fixed',()=>{
  const page=loadPage('pages/booking/index.js',()=>Promise.resolve({}));
  Object.assign(page.data,{minDate:'2026-09-08',maxDate:'2026-10-01',entitlement:{nights:2}});
  page.selectDate({currentTarget:{dataset:{date:'2026-09-10'}}});
  assert.equal(page.data.checkOutDate,'2026-09-12');
  page.data.submitting=true;
  page.selectDate({currentTarget:{dataset:{date:'2026-09-11'}}});
  assert.equal(page.data.checkInDate,'2026-09-10');
});
