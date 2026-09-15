const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');
const templatePath = path.join(miniappRoot, 'pages/order/detail.xhsml');
const stylePath = path.join(miniappRoot, 'pages/order/detail.css');

test('refund service follows the ticket and stays above, outside the collapsed order details', () => {
  const template = fs.readFileSync(templatePath, 'utf8');
  const ticketStart = template.indexOf('<view class="keepsake-tickets"');
  const refundStart = template.indexOf('<view class="ticket-after-sale"');
  const detailsToggle = template.indexOf('<button class="order-details-toggle"');
  const collapsedDetails = template.indexOf('<view xhs:if="{{!ticketCodes.length || orderDetailsExpanded}}">');

  assert.ok(ticketStart >= 0, 'ticket block should exist');
  assert.ok(refundStart > ticketStart, 'refund block should follow the ticket block');
  assert.ok(detailsToggle > refundStart, 'refund block should precede the details toggle');
  assert.ok(collapsedDetails > refundStart, 'refund block must not be inside collapsed order details');
});

test('ticket product title uses bounded fluid sizing without clipping long names', () => {
  const template = fs.readFileSync(templatePath, 'utf8');
  const styles = fs.readFileSync(stylePath, 'utf8');
  assert.match(template, /class="ticket-product"/);
  assert.match(styles, /\.ticket-product\s*\{[\s\S]*font-size:\s*clamp\(52rpx,\s*8vw,\s*60rpx\)/);
  assert.match(styles, /\.ticket-product\s*\{[\s\S]*(?:word-break|overflow-wrap):/);
  assert.doesNotMatch(styles, /\.ticket-product\s*\{[^}]*text-overflow\s*:/);
});

test('multiple QR codes share one ticket shell and scroll horizontally', () => {
  const template = fs.readFileSync(templatePath, 'utf8');
  const styles = fs.readFileSync(stylePath, 'utf8');
  const ticketBlock = template.slice(template.indexOf('<view class="keepsake-tickets"'), template.indexOf('<view class="ticket-after-sale"'));

  assert.match(ticketBlock, /<view class="keepsake-ticket">/);
  assert.doesNotMatch(ticketBlock, /class="keepsake-ticket"[^>]*xhs:for/);
  assert.match(ticketBlock, /<scroll-view class="ticket-code-scroll" scroll-x="\{\{true\}\}">/);
  assert.match(ticketBlock, /class="ticket-code-card" xhs:for="\{\{ticketCodes\}\}"/);
  assert.match(styles, /\.ticket-code-list\s*\{[\s\S]*display:\s*inline-flex/);
  assert.match(styles, /\.ticket-code-card\s*\{[\s\S]*flex:\s*none/);
});
