const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');
const qr = require(path.join(miniappRoot, 'utils/qr.js'));
const { BinaryBitmap, HybridBinarizer, QRCodeReader, RGBLuminanceSource } = require('../../admin/node_modules/@zxing/library');

function decodeMatrix(matrix) {
  const scale = 6;
  const modules = matrix.length + qr.QUIET_ZONE_MODULES * 2;
  const pixels = new Uint8ClampedArray(modules * scale * modules * scale).fill(255);
  matrix.forEach((row, rowIndex) => row.forEach((dark, columnIndex) => {
    if (!dark) return;
    const startX = (columnIndex + qr.QUIET_ZONE_MODULES) * scale;
    const startY = (rowIndex + qr.QUIET_ZONE_MODULES) * scale;
    for (let y = startY; y < startY + scale; y += 1) {
      for (let x = startX; x < startX + scale; x += 1) pixels[y * modules * scale + x] = 0;
    }
  }));
  const size = modules * scale;
  const bitmap = new BinaryBitmap(new HybridBinarizer(new RGBLuminanceSource(pixels, size, size)));
  return new QRCodeReader().decode(bitmap).getText();
}

function createRecordingContext() {
  const fills = [];
  let fillStyle = '#000000';
  return {
    fills,
    setFillStyle(value) { fillStyle = value; },
    fillRect(x, y, width, height) { fills.push({ color: fillStyle, x, y, width, height }); },
    draw() {}
  };
}

function decodeRecordedCanvas(context, size) {
  const pixels = new Uint8ClampedArray(size * size).fill(255);
  context.fills.forEach(fill => {
    const luminance = fill.color === '#111111' ? 0 : 255;
    const startX = Math.max(0, Math.floor(fill.x));
    const startY = Math.max(0, Math.floor(fill.y));
    const endX = Math.min(size, Math.ceil(fill.x + fill.width));
    const endY = Math.min(size, Math.ceil(fill.y + fill.height));
    for (let y = startY; y < endY; y += 1) {
      for (let x = startX; x < endX; x += 1) {
        const offset = y * size + x;
        pixels[offset] = luminance;
      }
    }
  });
  const bitmap = new BinaryBitmap(new HybridBinarizer(new RGBLuminanceSource(pixels, size, size)));
  return new QRCodeReader().decode(bitmap).getText();
}

function deferred() {
  let resolve;
  const promise = new Promise(value => { resolve = value; });
  return { promise, resolve };
}

function flush() {
  return new Promise(resolve => setImmediate(resolve));
}

function loadOrderDetail(app, xhs) {
  let definition;
  const source = fs.readFileSync(path.join(miniappRoot, 'pages/order/detail.js'), 'utf8');
  vm.runInNewContext(source, {
    Page: value => { definition = value; }, getApp: () => app, xhs,
    require: request => {
      if (request === '../../utils/payment') return require(path.join(miniappRoot, 'utils/payment.js'));
      if (request === '../../utils/qr') return qr;
      throw new Error(`unexpected require: ${request}`);
    },
    Boolean, Math, Number, String, encodeURIComponent,
    setTimeout: () => 1, clearTimeout: () => {}
  }, { filename: 'pages/order/detail.js' });
  const page = { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
  page.setData = (update, callback) => { Object.assign(page.data, update); if (callback) callback(); };
  return page;
}

test('each native QR matrix decodes to its exact server ticket code', () => {
  const codes = ['XHS-TKT-20260907-A1B2C3', 'XHS-TKT-20260907-D4E5F6'];
  assert.deepEqual(codes.map(code => decodeMatrix(qr.createTicketQRMatrix(code))), codes);
});

test('order detail maps one paid ticket code to one canvas and preserves the raw code', () => {
  const page = loadOrderDetail({}, {});
  const rawCodes = [' A-01 ', 'B-02'];
  const tickets = page.usableTicketCodes(rawCodes, 'paid');
  assert.deepEqual(JSON.parse(JSON.stringify(tickets)), [
    { code: ' A-01 ', index: 1, canvasId: 'ticket-qr-1' },
    { code: 'B-02', index: 2, canvasId: 'ticket-qr-2' }
  ]);
  assert.deepEqual(JSON.parse(JSON.stringify(page.usableTicketCodes(rawCodes, 'refunded'))), []);
});

test('a later order refresh wins, so old ticket QR codes cannot overwrite it', async () => {
  const first = deferred();
  const second = deferred();
  let calls = 0;
  const page = loadOrderDetail({ request: () => (calls++ === 0 ? first.promise : second.promise) }, {});
  page.orderNo = 'ORDER-1';
  page.loadOrder();
  page.loadOrder();
  second.resolve({ status: 'paid', amount_cents: 100, ticket_codes: ['CURRENT-CODE'] });
  await flush();
  first.resolve({ status: 'paid', amount_cents: 100, ticket_codes: ['STALE-CODE'] });
  await flush();
  assert.equal(page.data.ticketCodes.length, 1);
  assert.equal(page.data.ticketCodes[0].code, 'CURRENT-CODE');
});

test('a paid order without a server ticket code remains issuance-pending and retryable', async () => {
  const page = loadOrderDetail({ request: () => Promise.resolve({ status: 'paid', amount_cents: 100, ticket_codes: [], voucher_issuance_status: 'pending' }) }, {});
  page.orderNo = 'ORDER-1';
  page.loadOrder();
  await flush();
  assert.equal(page.data.status, 'paid');
  assert.equal(page.data.issuancePending, true);
  assert.equal(page.data.ticketCodes.length, 0);
  assert.match(page.data.statusTitle, /正在出票/);
});

test('manual-review issuance is not presented as a retrying provider state', async () => {
  const page = loadOrderDetail({ request: () => Promise.resolve({ status: 'paid', amount_cents: 100, ticket_codes: [], voucher_issuance_status: 'manual_review' }) }, {});
  page.orderNo = 'ORDER-1';
  page.loadOrder();
  await flush();
  assert.equal(page.data.issuancePending, false);
  assert.equal(page.data.issuanceManualReview, true);
  assert.match(page.data.statusTitle, /待处理/);
  assert.doesNotMatch(page.data.statusTitle, /正在出票/);
});

test('a deferred package awaiting booking does not claim ticket issuance', async () => {
  const page = loadOrderDetail({ request: () => Promise.resolve({
    status: 'paid', amount_cents: 100, product_kind: 'scenic_hotel_package', ticket_codes: [], voucher_issuance_status: 'not_required',
    package_entitlements: [{ entitlement_no: 'ENT-1', status: 'pending_booking' }]
  }) }, {});
  page.orderNo = 'ORDER-1';
  page.loadOrder();
  await flush();
  assert.equal(page.data.issuancePending, false);
  assert.equal(page.data.issuanceManualReview, false);
  assert.doesNotMatch(page.data.statusTitle, /正在出票/);
});

test('pending refund hides old codes without pretending the paid order is refunded or issuing', async () => {
  const page = loadOrderDetail({ request: () => Promise.resolve({
    status: 'paid', amount_cents: 100, refund_pending: true, ticket_codes: ['OLD']
  }) }, {});
  page.orderNo = 'ORDER-1';
  page.loadOrder();
  await flush();
  assert.equal(page.data.status, 'paid');
  assert.equal(page.data.ticketCodes.length, 0);
  assert.equal(page.data.issuancePending, false);
  assert.equal(page.data.issuanceManualReview, false);
  assert.equal(page.data.statusTitle, '退款处理中');
  assert.equal(page.data.statusLabel, '退款处理中');
});

test('unload invalidates a pending order request before it can update page data', async () => {
  const pending = deferred();
  const page = loadOrderDetail({ request: () => pending.promise }, {});
  page.orderNo = 'ORDER-1';
  page.loadOrder();
  page.onUnload();
  pending.resolve({ status: 'paid', amount_cents: 100, ticket_codes: ['SHOULD-NOT-RENDER'] });
  await flush();
  assert.equal(page.data.order, null);
  assert.equal(page.data.ticketCodes.length, 0);
});

test('canvas setup failure is reported so the page can show a retry prompt', () => {
  let reported = false;
  qr.renderTicketQRCodes({ qrRenderVersion: 1 }, [{ code: 'TICKET-CODE', canvasId: 'ticket-qr-1' }], 1, () => { reported = true; }, {});
  assert.equal(reported, true);
});

test('native logical canvas bounds contain and decode compact and expanded ticket QR codes', async () => {
  const compact = createRecordingContext();
  const expanded = createRecordingContext();
  const selectors = [];
  const compactCode = ' XHS-TKT-20260908-COMPACT ';
  const expandedCode = 'XHS-TKT-20260908-EXPANDED';
  const api = {
    createCanvasContext(canvasId) {
      return canvasId === 'ticket-qr-expanded' ? expanded : compact;
    },
    createSelectorQuery() {
      const requests = [];
      return {
        selectAll(selector) { selectors.push(selector); requests.push({ selector, multiple: true }); return this; },
        select(selector) { selectors.push(selector); requests.push({ selector, multiple: false }); return this; },
        boundingClientRect() { return this; },
        exec(callback) {
          callback(requests.map(request => request.multiple
            ? [{ width: 145, height: 145 }]
            : { width: 280, height: 280 }));
        }
      };
    }
  };
  qr.renderTicketQRCodes(
    { qrRenderVersion: 1 },
    [{ code: compactCode, canvasId: 'ticket-qr-1' }, { code: expandedCode, canvasId: 'ticket-qr-expanded' }],
    1,
    error => { throw error || new Error('QR render failed'); },
    api
  );
  await flush();

  assert.deepEqual(selectors, ['.ticket-qr', '.expanded-canvas']);
  for (const [context, size] of [[compact, 145], [expanded, 280]]) {
    assert(context.fills.length > 0);
    context.fills.forEach(fill => {
      assert(fill.x >= 0 && fill.y >= 0);
      assert(fill.x + fill.width <= size);
      assert(fill.y + fill.height <= size);
    });
  }
  assert.equal(decodeRecordedCanvas(compact, 145), compactCode);
  assert.equal(decodeRecordedCanvas(expanded, 280), expandedCode);
});

test('a stale native canvas measurement cannot draw or report an error', async () => {
  const context = createRecordingContext();
  let measured;
  let reported = false;
  const page = { qrRenderVersion: 1 };
  qr.renderTicketQRCodes(page, [{ code: 'STALE-CODE', canvasId: 'ticket-qr-1' }], 1, () => { reported = true; }, {
    createCanvasContext: () => context,
    createSelectorQuery: () => ({
      selectAll() { return this; },
      boundingClientRect() { return this; },
      exec(callback) { measured = callback; }
    })
  });
  await flush();
  page.qrRenderVersion = 2;
  measured([[{ width: 145, height: 145 }]]);
  assert.equal(context.fills.length, 0);
  assert.equal(reported, false);
});

test('souvenir ticket uses the current merchant and raw tickets without inventing dates', async () => {
  const page = loadOrderDetail({
    globalData: { storeName: '另一家商户' }, setNavigationTitle: () => {},
    request: () => Promise.resolve({ status: 'paid', product_name: '周末双人票', amount_cents: 100, ticket_codes: ['ONE', 'TWO'] })
  }, {});
  page.onLoad({ order_no: 'ORDER-2' });
  await flush();
  assert.equal(page.data.storeName, '另一家商户');
  assert.equal(page.data.order.product_name, '周末双人票');
  assert.equal(page.data.ticketCodes.length, 2);
  assert.equal(page.data.ticketCodes[1].code, 'TWO');
  const template = fs.readFileSync(path.join(miniappRoot, 'pages/order/detail.xhsml'), 'utf8');
  assert.match(template, /\{\{storeName\}\}/);
  assert.match(template, /\{\{order\.image_url\}\}/);
  assert.doesNotMatch(template, /storefront_image_url|created_at|已入园|核销成功/);
  const ticketTemplate = template.slice(template.indexOf('<view class="keepsake-tickets"'), template.indexOf('<view class="ticket-after-sale"'));
  assert.doesNotMatch(ticketTemplate, /image_url|ticket-photo/);
  assert.match(ticketTemplate, /assets\/ticket-art\.svg/);
  assert(ticketTemplate.indexOf('ticket-product') < ticketTemplate.indexOf('ticket-merchant'));
  assert.equal(page.data.ticketCodes.length, 2);
});

test('ticket enlargement selects the exact ticket and order details toggle independently', () => {
  const page = loadOrderDetail({}, {});
  page.data.ticketCodes = page.usableTicketCodes([' FIRST ', 'SECOND'], 'paid');
  page.showTicketQR({ currentTarget: { dataset: { canvasId: 'unknown' } } });
  assert.equal(page.data.expandedTicket, null);
  page.showTicketQR({ currentTarget: { dataset: { canvasId: 'ticket-qr-2' } } });
  assert.equal(page.data.expandedTicket.code, 'SECOND');
  page.toggleOrderDetails();
  assert.equal(page.data.orderDetailsExpanded, true);
  page.closeTicketQR();
  assert.equal(page.data.expandedTicket, null);
  assert.equal(page.data.expandedQRError, false);
  page.showTicketQR({ currentTarget: { dataset: { canvasId: 'ticket-qr-1' } } });
  assert.equal(page.data.expandedTicket.code, ' FIRST ');
  page.toggleOrderDetails();
  assert.equal(page.data.orderDetailsExpanded, false);
});

test('a refreshed refund removes an enlarged old ticket', async () => {
  const page = loadOrderDetail({
    request: () => Promise.resolve({ status: 'refunded', amount_cents: 100, ticket_codes: ['OLD'] })
  }, {});
  page.orderNo = 'ORDER-1';
  page.data.ticketCodes = page.usableTicketCodes(['OLD'], 'paid');
  page.showTicketQR({ currentTarget: { dataset: { canvasId: 'ticket-qr-1' } } });
  page.loadOrder();
  await flush();
  assert.equal(page.data.expandedTicket, null);
  assert.equal(page.data.ticketCodes.length, 0);
});

test('souvenir design never enables QR display for an unpaid, closed or refunded order', () => {
  const page = loadOrderDetail({}, {});
  for (const status of ['unpaid', 'cancelled', 'failed', 'refunded', 'completed']) {
    assert.equal(page.usableTicketCodes(['OLD-CODE'], status).length, 0);
  }
  assert.equal(page.usableTicketCodes(['REMAINING-CODE'], 'partial_refunded').length, 1);
});
