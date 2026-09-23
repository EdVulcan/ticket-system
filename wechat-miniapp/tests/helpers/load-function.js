'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { createRequire } = require('node:module');
function loadFunction(name, db, context = { OPENID: 'buyer' }, cloudPay = {}, cloudOptions = {}) {
  const filename = path.resolve(__dirname, '../../cloudfunctions', name, 'index.js');
  const localRequire = createRequire(filename);
  const cloud = Object.assign({ init() {}, database: () => db, getWXContext: () => context, DYNAMIC_CURRENT_ENV: 'test-env', cloudPay }, cloudOptions);
  const module = { exports: {} };
  const sandbox = { module, exports: module.exports, Buffer, Date, process, console: { warn() {}, error() {}, log() {} }, require: (id) => id === 'wx-server-sdk' ? cloud : localRequire(id) };
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), sandbox, { filename });
  return module.exports.main;
}
module.exports = { loadFunction };
