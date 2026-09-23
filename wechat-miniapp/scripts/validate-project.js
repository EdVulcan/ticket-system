'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const root = path.resolve(__dirname, '..');
function filesIn(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => ['node_modules', '.git'].includes(entry.name) ? [] : entry.isDirectory() ? filesIn(path.join(dir, entry.name)) : [path.join(dir, entry.name)]);
}
const files = filesIn(root);
let jsCount = 0; let jsonCount = 0; let wxmlCount = 0;
for (const file of files) {
  if (file.endsWith('.js')) {
    const result = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8', windowsHide: true });
    if (result.status !== 0) throw new Error(result.stderr || `Syntax failure: ${file}`);
    jsCount += 1;
  }
  if (file.endsWith('.json')) { JSON.parse(fs.readFileSync(file, 'utf8').replace(/^\uFEFF/, '')); jsonCount += 1; }
}
const app = JSON.parse(fs.readFileSync(path.join(root, 'miniprogram/app.json'), 'utf8'));
const builtin = new Set('view text image button input textarea picker switch scroll-view block navigator form label radio checkbox radio-group checkbox-group swiper swiper-item movable-view movable-area video canvas map cover-view cover-image rich-text web-view slot template import include'.split(' '));
for (const page of app.pages) {
  const base = path.join(root, 'miniprogram', page);
  for (const extension of ['js', 'json', 'wxml', 'wxss']) if (!fs.existsSync(`${base}.${extension}`)) throw new Error(`Missing page file ${page}.${extension}`);
  const source = fs.readFileSync(`${base}.wxml`, 'utf8').replace(/<!--[\s\S]*?-->/g, '');
  const code = fs.readFileSync(`${base}.js`, 'utf8');
  // Catalog tabs share their Page definition through a factory module.
  const handlerCode = code.includes('createCatalogPage') ? `${code}\n${fs.readFileSync(path.join(root, 'miniprogram/services/catalog-page.js'), 'utf8')}` : code;
  const config = JSON.parse(fs.readFileSync(`${base}.json`, 'utf8'));
  const custom = Object.keys(Object.assign({}, app.usingComponents, config.usingComponents));
  const stack = [];
  for (const match of source.matchAll(/<\/?[a-zA-Z][\w:-]*(?:"[^"]*"|'[^']*'|[^'">])*>/g)) {
    const token = match[0]; const name = /^<\/?([\w:-]+)/.exec(token)[1];
    if (!builtin.has(name) && !custom.includes(name)) throw new Error(`${page}: unregistered tag ${name}`);
    if (token.startsWith('</')) { if (stack.pop() !== name) throw new Error(`${page}: unbalanced ${name}`); }
    else if (!token.endsWith('/>')) stack.push(name);
  }
  if (stack.length) throw new Error(`${page}: unclosed ${stack.join(',')}`);
  for (const match of source.matchAll(/\b(?:bind|catch):?[\w-]+="([A-Za-z_$][\w$]*)"/g)) {
    const handler = match[1];
    if (!(new RegExp(`\\b${handler}\\s*\\(`)).test(handlerCode)) throw new Error(`${page}: handler ${handler} not defined`);
  }
  wxmlCount += 1;
}
// CloudBase remains only in the unregistered vendor example page. Any
// wx.cloud call in a page shipped through app.json would create a second
// production authority alongside the SaaS API, so fail the package check.
for (const page of app.pages) {
  const source = fs.readFileSync(path.join(root, 'miniprogram', `${page}.js`), 'utf8');
  if (/\bwx\.cloud\b/.test(source)) throw new Error(`${page}: production pages must use the SaaS HTTPS API, not wx.cloud`);
}
for (const tab of app.tabBar.list) if (!app.pages.includes(tab.pagePath)) throw new Error(`Unregistered tab ${tab.pagePath}`);
if (!process.argv.includes('--check')) process.argv.push('--check');
require('./build-cloud-shared');
console.log(`Static checks passed: ${jsCount} JS, ${jsonCount} JSON, ${wxmlCount} registered pages. Not a WeChat render test.`);
