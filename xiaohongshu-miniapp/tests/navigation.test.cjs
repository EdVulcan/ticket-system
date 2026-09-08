const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const miniappRoot = path.join(__dirname, '..', 'xiaohongshu-miniapp');

function loadPage(relativePath, xhs) {
  let definition;
  const source = fs.readFileSync(path.join(miniappRoot, relativePath), 'utf8');
  vm.runInNewContext(source, {
    Page: value => { definition = value; },
    getApp: () => ({
      globalData: {},
      setNavigationTitle() {},
      setStoreName() {}
    }),
    xhs,
    require
  }, { filename: relativePath });
  return { ...definition, data: JSON.parse(JSON.stringify(definition.data)) };
}

test('top-level bottom-nav links replace the current page with one consistent transition', () => {
  const calls = [];
  const xhs = {
    navigateTo: options => calls.push(['navigateTo', options.url]),
    redirectTo: options => calls.push(['redirectTo', options.url]),
    reLaunch: options => calls.push(['reLaunch', options.url])
  };

  loadPage('pages/index/index.js', xhs).goOrders();
  loadPage('pages/orders/index.js', xhs).goHome();

  assert.deepEqual(calls, [
    ['redirectTo', '/pages/orders/index'],
    ['redirectTo', '/pages/index/index']
  ]);
});

test('both bottom-nav templates give every item the same native tap feedback', () => {
  for (const relativePath of ['pages/index/index.xhsml', 'pages/orders/index.xhsml']) {
    const source = fs.readFileSync(path.join(miniappRoot, relativePath), 'utf8');
    const bottomNav = source.match(/<view class="bottom-nav">[\s\S]*?<\/view>\s*<\/view>\s*$/);
    assert.ok(bottomNav, `${relativePath} should contain a bottom nav`);
    const navItems = bottomNav[0].match(/<view class="nav-item(?: [^"]+)?"[^>]*>/g) || [];
    assert.equal(navItems.length, 2, `${relativePath} should expose two nav items`);
    for (const navItem of navItems) assert.match(navItem, /hover-class="tap-feedback"/);
  }
});
