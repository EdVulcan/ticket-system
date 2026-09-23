'use strict';
// Offline compiler check only. Does not launch the IDE, upload, preview or publish.
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const argument = process.argv.findIndex(value => value === '--compiler-dir' || value.startsWith('--compiler-dir='));
const flaggedDirectory = argument >= 0
  ? (process.argv[argument].includes('=') ? process.argv[argument].slice(process.argv[argument].indexOf('=') + 1) : process.argv[argument + 1])
  : undefined;
// Some npm versions on Windows strip the unknown flag and forward only its value.
const positionalDirectory = argument < 0 && process.argv[2] && !process.argv[2].startsWith('-') ? process.argv[2] : undefined;
const directory = flaggedDirectory || positionalDirectory || process.env.WECHAT_COMPILER_DIR;
if (!directory) throw new Error('Pass --compiler-dir <path>, --compiler-dir=<path>, or WECHAT_COMPILER_DIR pointing to WeChat DevTools node_modules/wcc-exec');
const root = path.resolve(__dirname, '../miniprogram');
const pages = JSON.parse(fs.readFileSync(path.join(root, 'app.json'), 'utf8')).pages;
function walk(dir) { return fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => entry.isDirectory() ? walk(path.join(dir, entry.name)) : [path.join(dir, entry.name)]); }
const styles = walk(root).filter(file => file.endsWith('.wxss')).map(file => path.relative(root, file).replace(/\\/g, '/'));
for (const [name, inputs] of [['wcc.exe', pages.map(page => page + '.wxml')], ['wcsc.exe', styles]]) {
  const compiler = path.resolve(directory, name);
  if (!fs.existsSync(compiler)) throw new Error(`Missing compiler: ${compiler}`);
  const result = spawnSync(compiler, inputs, { cwd: root, encoding: 'utf8', windowsHide: true, timeout: 30000, maxBuffer: 20 * 1024 * 1024 });
  if (result.error || result.status !== 0) throw new Error(`${name} failed: ${result.error || result.stderr || result.stdout}`);
  if (result.stderr.trim()) console.log(`${name} diagnostics: ${result.stderr.trim()}`);
  console.log(`${name}: compiled ${inputs.length} files successfully`);
}
console.log('Offline WeChat compilation passed. Layout, device APIs and live payments remain unverified.');
