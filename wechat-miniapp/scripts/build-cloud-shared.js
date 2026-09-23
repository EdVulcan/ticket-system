'use strict';
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'cloud-shared/payment-core.js'));
const targets = ['payment', 'paymentCallback', 'order', 'scheduledTasks'];
for (const name of targets) {
  const target = path.join(root, 'cloudfunctions', name, 'payment-core.js');
  if (process.argv.includes('--check')) {
    if (!fs.existsSync(target) || !fs.readFileSync(target).equals(source)) throw new Error(`Shared runtime stale: ${name}. Run node scripts/build-cloud-shared.js`);
  } else fs.writeFileSync(target, source);
}
console.log(`Shared payment runtime ${process.argv.includes('--check') ? 'verified' : 'built'} (${targets.length} functions)`);
