const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.join(__dirname, '../xiaohongshu-miniapp');

function loadDetail(order, api = {}, request = () => Promise.resolve(order)) {
  let definition;
  vm.runInNewContext(fs.readFileSync(path.join(root, 'pages/order/detail.js'), 'utf8'), {
    Page: value => { definition = value; }, getApp: () => ({ request }), xhs: api,
    require: name => name.endsWith('/qr') ? { renderTicketQRCodes() {} } : {},
    setTimeout: () => 1, clearTimeout() {},
  });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)), orderNo: 'DEMO' };
  page.setData = (data, done) => { Object.assign(page.data, data); if (done) done(); };
  return page;
}

test('ticket shows shared usage facts without removing repeat-use QR', async () => {
  for (const [status, count, label] of [['unused', 0, '未使用'], ['active', 1, '已使用'], ['used', 2, '已使用']]) {
    const page = loadDetail({status: 'paid', ticket_codes: ['DEMO'], tickets: [{code:'DEMO',status,check_in_count:count}]});
    await page.loadOrder();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(page.data.ticketCodes[0].usageLabel, label);
    assert.equal(page.data.ticketCodes[0].code, 'DEMO');
    if(count) assert.match(page.data.ticketCodes[0].usageDetail, /后续使用按票种规则核验/);
  }
});

test('older backend without usage projection is not falsely labelled unused', async () => {
  const page = loadDetail({status:'paid',ticket_codes:['DEMO']});
  page.loadOrder();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(page.data.ticketCodes[0].usageLabel, '使用状态待查询');
});

test('refund service remains visible when issued ticket metadata is missing', async () => {
  const page = loadDetail({status: 'paid', ticket_codes: ['DEMO'], can_apply_refund: false,
    refund_application_message: '该订单暂不支持自助退款，请联系景区客服'});
  await page.loadOrder();
  assert.match(page.data.refundUnavailableMessage, /暂不支持自助退款/);
});

test('paid orders without refund projection show a refresh explanation, not a hidden service', async () => {
  const page = loadDetail({status: 'paid', ticket_codes: ['DEMO']});
  await page.loadOrder();
  assert.match(page.data.refundUnavailableMessage, /刷新|查询/);
  assert.notEqual(page.data.order.can_apply_refund, true);
});

test('used paid tickets retain the server refund explanation without allowing an application', async () => {
  const page = loadDetail({status: 'paid', ticket_codes: ['DEMO'], can_apply_refund: false,
    tickets: [{code: 'DEMO', status: 'active', check_in_count: 1}],
    refund_application_message: '票券已核销或核销状态待确认，暂不能申请退款'});
  await page.loadOrder();
  assert.match(page.data.refundUnavailableMessage, /已核销/);
});

test('pull-down refresh updates ticket usage and completes the native refresh indicator', async () => {
  const order={status:'paid',ticket_codes:['DEMO'],tickets:[{code:'DEMO',status:'active',check_in_count:1}]};
  let stopped=0;
  const page=loadDetail(order,{stopPullDownRefresh:()=>{stopped++;}});
  await page.onPullDownRefresh();
  assert.equal(page.data.ticketCodes[0].usageLabel,'已使用');
  assert.equal(stopped,1);
});

test('automatic refund waits for explicit confirmation then pauses QR without claiming funds returned', async () => {
  const order = {status:'paid',quantity:1,amountText:'80.00',ticket_codes:['DEMO'],can_apply_refund:true};
  let modal;
  const posts=[];
  const page=loadDetail(order,{showModal:options=>{modal=options;}},(url,options)=>{
    if(options){posts.push({url,options});order.can_apply_refund=false;order.refund_application_status='processing';order.refund_pending=true;return Promise.resolve({status:'processing'});}
    return Promise.resolve(order);
  });
  page.data.order=order;
  page.applyRefund();
  page.applyRefund();
  assert.equal(posts.length,0);
  assert.match(modal.content,/整|全部/);
  assert.match(modal.content,/自动办理原路退款/);
  modal.success({confirm:true});
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(posts.length,1);
  assert.equal(posts[0].url,'/orders/DEMO/refund-applications');
  assert.deepEqual(Object.keys(posts[0].options.data).sort(),['reason','request_id']);
  assert.equal(page.data.status,'paid');
  assert.equal(page.data.ticketCodes.length,0);
  assert.equal(page.data.statusTitle,'退款处理中');
  assert.match(page.data.refundApplicationMessage,/正在原路退款/);
});

test('refund cancellation makes no request; failed network keeps request identity for retry',async()=>{
  const order={can_apply_refund:true,quantity:1,amountText:'80.00'};
  let modal; const keys=[];
  const page=loadDetail(order,{showModal:options=>{modal=options;}},(url,options)=>{
    keys.push(options.data.request_id);return Promise.reject(new Error('网络中断'));
  });
  page.data.order=order;
  page.applyRefund();modal.success({confirm:false});assert.equal(keys.length,0);
  page.applyRefund();modal.success({confirm:true});
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(page.data.applyingRefund,false);
  assert.match(page.data.error,/网络中断/);
  page.applyRefund();modal.success({confirm:true});
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(keys.length,2);assert.equal(keys[0],keys[1]);
});
