// QR matrix generation is vendored from qrcode-generator 1.4.4 (MIT).
const qrcode = require('../vendor/qrcode-generator-1.4.4');

const QUIET_ZONE_MODULES = 4;
const CANVAS_SIZE = 280;

// Ticket codes are opaque server-issued strings. UTF-8 preserves each supplied
// character; callers deliberately do not trim, prefix, or otherwise transform it.
qrcode.stringToBytes = qrcode.stringToBytesFuncs['UTF-8'];

function createTicketQRMatrix(ticketCode) {
  if (typeof ticketCode !== 'string' || ticketCode.length === 0) throw new Error('ticket code is required');
  const qr = qrcode(0, 'M');
  qr.addData(ticketCode, 'Byte');
  qr.make();
  const count = qr.getModuleCount();
  const matrix = [];
  for (let row = 0; row < count; row += 1) {
    const cells = [];
    for (let column = 0; column < count; column += 1) cells.push(qr.isDark(row, column));
    matrix.push(cells);
  }
  return matrix;
}

function drawTicketQR(context, matrix, canvasWidth = CANVAS_SIZE, canvasHeight = canvasWidth) {
  if (!Number.isFinite(canvasWidth) || !Number.isFinite(canvasHeight) || canvasWidth <= 0 || canvasHeight <= 0) {
    throw new Error('canvas size is required');
  }
  const totalModules = matrix.length + QUIET_ZONE_MODULES * 2;
  const qrSize = Math.min(canvasWidth, canvasHeight);
  const moduleSize = qrSize / totalModules;
  const offsetX = (canvasWidth - qrSize) / 2;
  const offsetY = (canvasHeight - qrSize) / 2;
  context.setFillStyle('#ffffff');
  context.fillRect(0, 0, canvasWidth, canvasHeight);
  context.setFillStyle('#111111');
  matrix.forEach((row, rowIndex) => row.forEach((dark, columnIndex) => {
    if (!dark) return;
    context.fillRect(
      offsetX + (columnIndex + QUIET_ZONE_MODULES) * moduleSize,
      offsetY + (rowIndex + QUIET_ZONE_MODULES) * moduleSize,
      moduleSize,
      moduleSize
    );
  }));
  context.draw();
}

function isUsableRect(rect) {
  return rect && Number.isFinite(rect.width) && Number.isFinite(rect.height) && rect.width > 0 && rect.height > 0;
}

function measureTicketCanvases(api, tickets, done) {
  if (typeof api.createSelectorQuery !== 'function') {
    done(new Error('canvas measurement is unavailable'));
    return;
  }
  const compactTickets = tickets.filter(ticket => ticket.canvasId !== 'ticket-qr-expanded');
  const expandedTickets = tickets.filter(ticket => ticket.canvasId === 'ticket-qr-expanded');
  try {
    const query = api.createSelectorQuery();
    const groups = [];
    if (compactTickets.length) {
      // XHS SelectorQuery supports class selectors, and these canvases are rendered
      // by xhs:for in the same order as tickets.
      query.selectAll('.ticket-qr').boundingClientRect();
      groups.push({ tickets: compactTickets, multiple: true });
    }
    expandedTickets.forEach(ticket => {
      query.select('.expanded-canvas').boundingClientRect();
      groups.push({ tickets: [ticket], multiple: false });
    });
    query.exec(results => {
      try {
        const sizes = new Map();
        if (!Array.isArray(results) || results.length !== groups.length) throw new Error('canvas measurement failed');
        groups.forEach((group, index) => {
          const rects = group.multiple ? results[index] : [results[index]];
          if (!Array.isArray(rects) || rects.length !== group.tickets.length) throw new Error('canvas measurement failed');
          group.tickets.forEach((ticket, ticketIndex) => {
            const rect = rects[ticketIndex];
            if (!isUsableRect(rect)) throw new Error('canvas measurement failed');
            sizes.set(ticket.canvasId, rect);
          });
        });
        done(null, sizes);
      } catch (error) {
        done(error);
      }
    });
  } catch (error) {
    done(error);
  }
}

function renderTicketQRCodes(page, tickets, renderVersion, onError, xhsApi) {
  const api = xhsApi || (typeof xhs === 'undefined' ? null : xhs);
  if (!api || typeof api.createCanvasContext !== 'function') {
    if (typeof onError === 'function') onError();
    return;
  }
  setTimeout(() => {
    if (page.qrRenderVersion !== renderVersion) return;
    measureTicketCanvases(api, tickets, (measurementError, sizes) => {
      if (page.qrRenderVersion !== renderVersion) return;
      if (measurementError) {
        if (typeof onError === 'function') onError();
        return;
      }
      let failed = false;
      tickets.forEach(ticket => {
        if (page.qrRenderVersion !== renderVersion) return;
        try {
          const context = api.createCanvasContext(ticket.canvasId, page);
          const rect = sizes.get(ticket.canvasId);
          drawTicketQR(context, createTicketQRMatrix(ticket.code), rect.width, rect.height);
        } catch (_) {
          failed = true;
        }
      });
      if (failed && page.qrRenderVersion === renderVersion && typeof onError === 'function') onError();
    });
  }, 0);
}

module.exports = { CANVAS_SIZE, QUIET_ZONE_MODULES, createTicketQRMatrix, drawTicketQR, renderTicketQRCodes };
