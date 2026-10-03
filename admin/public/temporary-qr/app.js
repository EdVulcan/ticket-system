(function () {
  'use strict';
  const $ = (id) => document.getElementById(id);
  let tickets = [], revision = 0, filter = 'all', busy = false, signature = '';
  let celebrationTimer = null, physicsFrame = 0, sprites = [];
  let fieldWidth = 0, fieldHeight = 0, fieldRatio = 1;
  let hasLoadedState = false, wasSoldOut = false;
  const celebration = $('celebration');
  const confetti = $('confetti');
  const playfield = $('playfield');
  const context = playfield.getContext('2d');
  const spriteCache = new Map();
  async function api(path, options = {}) {
    const response = await fetch('/api/temporary-qr/' + path, {
      ...options,
      headers: { 'Content-Type': 'application/json' },
      cache: 'no-store'
    });
    if (response.status === 409) throw new Error('其他终端已修改数据，已刷新，请重新操作。');
    if (!response.ok) throw new Error('服务器保存或读取失败，请稍后重试。');
    return response.json();
  }
  function apply(state) {
    const next = JSON.stringify(state);
    revision = state.revision;
    tickets = state.tickets || [];
    if (document.activeElement !== $('titleInput')) $('titleInput').value = state.title || '';
    const nextSoldOut = tickets.length > 0 && tickets.every((ticket) => ticket.sold);
    const becameSoldOut = hasLoadedState && !wasSoldOut && nextSoldOut;
    wasSoldOut = nextSoldOut;
    hasLoadedState = true;
    if (next !== signature) { signature = next; render(); }
    if (becameSoldOut) showCelebration();
  }
  async function refresh() {
    if (busy) return;
    const state = await api('state');
    apply(state);
  }
  async function mutate(change) {
    if (busy) return;
    busy = true;
    try {
      change();
      const state = await api('state', { method: 'PUT', body: JSON.stringify({ title: $('titleInput').value.trim(), tickets, revision }) });
      apply(state);
    } catch (error) {
      alert(error.message);
      try { apply(await api('state')); } catch (_) { /* The next refresh will retry. */ }
    } finally { busy = false; }
  }
  function render() {
    const shown = tickets.filter((ticket) => filter === 'all' || (filter === 'sold' ? ticket.sold : !ticket.sold));
    $('ticketGrid').replaceChildren();
    shown.forEach((ticket) => {
      const card = document.createElement('article');
      card.className = 'ticket-card ' + (ticket.sold ? 'is-sold' : '');
      const qr = document.createElement('button');
      qr.type = 'button'; qr.className = 'qr'; qr.style.border = '0'; qr.style.background = 'white';
      qr.setAttribute('aria-label', '查看门票 ' + ticket.code);
      qr.onclick = () => openTicket(ticket);
      new QRCode(qr, { text: ticket.code, width: 150, height: 150, correctLevel: QRCode.CorrectLevel.M });
      const code = document.createElement('button');
      code.className = 'code-button'; code.textContent = ticket.code; code.onclick = () => openTicket(ticket);
      const status = document.createElement('button');
      status.className = 'status ' + (ticket.sold ? 'sold' : 'available');
      status.textContent = ticket.sold ? '已销售' : '未销售';
      status.onclick = () => mutate(() => { ticket.sold = !ticket.sold; });
      card.append(qr, code, status); $('ticketGrid').append(card);
    });
    const sold = tickets.filter((t) => t.sold).length;
    $('summary').textContent = '共 ' + tickets.length + ' 张 · 已销售 ' + sold + ' 张 · 未销售 ' + (tickets.length - sold) + ' 张';
    $('emptyState').hidden = shown.length > 0;
  }
  function burst() {
    confetti.replaceChildren();
    const colors = ['#2563eb', '#12b7a6', '#f59e0b', '#ef6680', '#8b5cf6'];
    for (let i = 0; i < 38; i += 1) {
      const piece = document.createElement('i');
      piece.style.setProperty('--color', colors[i % colors.length]);
      piece.style.setProperty('--left', `${Math.random() * 100}%`);
      piece.style.setProperty('--x', `${(Math.random() - .5) * 240}px`);
      piece.style.setProperty('--y', `${window.innerHeight * .95 + Math.random() * 260}px`);
      piece.style.setProperty('--rotate', `${Math.random() * 90 - 45}deg`);
      piece.style.setProperty('--spin', `${Math.random() * 800 - 400}deg`);
      piece.style.setProperty('--delay', `${Math.random() * .18}s`);
      confetti.append(piece);
    }
  }
  function resizePlayfield() {
    const ratio = window.devicePixelRatio || 1;
    const width = celebration.clientWidth;
    const height = celebration.clientHeight;
    if (width !== fieldWidth || height !== fieldHeight || ratio !== fieldRatio) {
      if (ratio !== fieldRatio) spriteCache.clear();
      fieldWidth = width;
      fieldHeight = height;
      fieldRatio = ratio;
      playfield.width = Math.round(width * ratio);
      playfield.height = Math.round(height * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
    }
    return { width, height };
  }
  function getSpriteImage(icon, radius) {
    const key = `${icon}:${radius}:${fieldRatio}`;
    const cached = spriteCache.get(key);
    if (cached) return cached;
    const padding = 12;
    const cssSize = radius * 2 + padding * 2;
    const image = document.createElement('canvas');
    image.width = Math.ceil(cssSize * fieldRatio);
    image.height = Math.ceil(cssSize * fieldRatio);
    const imageContext = image.getContext('2d');
    imageContext.scale(fieldRatio, fieldRatio);
    imageContext.font = `${radius * 1.85}px "Apple Color Emoji", "Segoe UI Emoji", sans-serif`;
    imageContext.textAlign = 'center';
    imageContext.textBaseline = 'middle';
    imageContext.shadowColor = '#ffffff';
    imageContext.shadowBlur = 8;
    imageContext.fillText(icon, cssSize / 2, cssSize / 2);
    const result = { canvas: image, cssSize };
    spriteCache.set(key, result);
    return result;
  }
  function createSprites() {
    const { width, height } = resizePlayfield();
    const icons = ['🐰', '🐱', '🐶', '🐰', '🐰', '🐶', '🐰'];
    sprites = icons.map((icon, index) => {
      const radius = 27 + (index % 3) * 5;
      return { icon, radius, image: getSpriteImage(icon, radius), x: radius + Math.random() * Math.max(1, width - radius * 2), y: radius + Math.random() * Math.max(1, height - radius * 2), vx: (index % 2 ? -1 : 1) * (75 + Math.random() * 55), vy: (index % 3 ? 1 : -1) * (55 + Math.random() * 65), angle: Math.random() * Math.PI * 2, spin: (index % 2 ? -1 : 1) * (1.5 + Math.random() * 1.5) };
    });
  }
  function collideSprites(left, right) {
    const dx = right.x - left.x;
    const dy = right.y - left.y;
    const distance = Math.hypot(dx, dy) || 0.01;
    const minimum = left.radius + right.radius;
    if (distance >= minimum) return;
    const nx = dx / distance;
    const ny = dy / distance;
    const overlap = minimum - distance;
    left.x -= nx * overlap / 2; left.y -= ny * overlap / 2;
    right.x += nx * overlap / 2; right.y += ny * overlap / 2;
    const relative = (right.vx - left.vx) * nx + (right.vy - left.vy) * ny;
    if (relative > 0) return;
    left.vx += relative * nx; left.vy += relative * ny;
    right.vx -= relative * nx; right.vy -= relative * ny;
  }
  function drawSprites(timestamp) {
    const width = fieldWidth;
    const height = fieldHeight;
    const now = timestamp / 1000;
    const previous = drawSprites.lastTime || now;
    const delta = Math.min(now - previous, 0.035);
    drawSprites.lastTime = now;
    context.clearRect(0, 0, width, height);
    sprites.forEach((sprite) => {
      sprite.x += sprite.vx * delta;
      sprite.y += sprite.vy * delta;
      sprite.angle += sprite.spin * delta;
      if (sprite.x - sprite.radius < 0) { sprite.x = sprite.radius; sprite.vx = Math.abs(sprite.vx); }
      if (sprite.x + sprite.radius > width) { sprite.x = width - sprite.radius; sprite.vx = -Math.abs(sprite.vx); }
      if (sprite.y - sprite.radius < 0) { sprite.y = sprite.radius; sprite.vy = Math.abs(sprite.vy); }
      if (sprite.y + sprite.radius > height) { sprite.y = height - sprite.radius; sprite.vy = -Math.abs(sprite.vy); }
    });
    for (let left = 0; left < sprites.length; left += 1) for (let right = left + 1; right < sprites.length; right += 1) collideSprites(sprites[left], sprites[right]);
    sprites.forEach((sprite) => {
      context.save();
      context.translate(sprite.x, sprite.y);
      context.rotate(sprite.angle);
      context.shadowBlur = 0;
      context.drawImage(sprite.image.canvas, -sprite.image.cssSize / 2, -sprite.image.cssSize / 2, sprite.image.cssSize, sprite.image.cssSize);
      context.restore();
    });
    physicsFrame = window.requestAnimationFrame(drawSprites);
  }
  function startPhysics() {
    window.cancelAnimationFrame(physicsFrame);
    drawSprites.lastTime = 0;
    createSprites();
    physicsFrame = window.requestAnimationFrame(drawSprites);
  }
  function stopPhysics() {
    window.cancelAnimationFrame(physicsFrame);
    physicsFrame = 0;
    sprites = [];
    context.clearRect(0, 0, fieldWidth, fieldHeight);
  }
  function showCelebration() {
    celebration.classList.add('show');
    celebration.setAttribute('aria-hidden', 'false');
    burst();
    startPhysics();
    window.clearInterval(celebrationTimer);
    celebrationTimer = window.setInterval(burst, 2600);
  }
  function closeCelebration() {
    window.clearInterval(celebrationTimer);
    celebrationTimer = null;
    stopPhysics();
    celebration.classList.remove('show');
    celebration.setAttribute('aria-hidden', 'true');
  }
  function openTicket(ticket) {
    $('dialogTitle').textContent = $('titleInput').value.trim() || '临时门票';
    $('dialogCode').textContent = ticket.code;
    $('dialogQr').replaceChildren();
    new QRCode($('dialogQr'), { text: ticket.code, width: 240, height: 240, correctLevel: QRCode.CorrectLevel.H });
    $('ticketDialog').showModal();
  }
  async function saveTicketImage() {
    const button = $('saveTicketImage');
    button.disabled = true; button.textContent = '生成中...';
    const ticketExport = $('ticketExport');
    ticketExport.classList.add('exporting');
    try {
      const canvas = await html2canvas(ticketExport, { backgroundColor: '#ffffff', scale: 3, logging: false });
      const link = document.createElement('a');
      const fileTitle = ($('dialogTitle').textContent || '临时门票').replace(/[\\/:*?"<>|]/g, '-');
      const fileCode = $('dialogCode').textContent.replace(/[\\/:*?"<>|]/g, '-');
      link.download = `${fileTitle}-${fileCode}.png`;
      link.href = canvas.toDataURL('image/png'); link.click();
    } catch (_) { alert('图片生成失败，请重试。'); }
    finally { ticketExport.classList.remove('exporting'); button.disabled = false; button.textContent = '保存票据图片'; }
  }
  $('importButton').onclick = () => mutate(() => {
    const known = new Set(tickets.map((t) => t.code));
    $('codesInput').value.split(/\r?\n/).map((v) => v.trim()).filter(Boolean).forEach((code) => {
      if (!known.has(code)) { tickets.push({ code, sold: false }); known.add(code); }
    });
    $('codesInput').value = '';
  });
  $('clearButton').onclick = () => { if (confirm('确定清空所有终端共享的二维码吗？')) mutate(() => { tickets = []; }); };
  $('titleInput').onchange = () => mutate(() => {});
  document.querySelectorAll('.filter').forEach((button) => button.onclick = () => {
    filter = button.dataset.filter;
    document.querySelectorAll('.filter').forEach((item) => item.classList.toggle('active', item === button));
    render();
  });
  $('closeDialog').onclick = () => $('ticketDialog').close();
  $('closeDialogSecondary').onclick = () => $('ticketDialog').close();
  $('saveTicketImage').onclick = saveTicketImage;
  $('ticketDialog').onclick = (event) => { if (event.target === $('ticketDialog')) $('ticketDialog').close(); };
  $('closeCelebration').onclick = closeCelebration;
  celebration.onclick = (event) => { if (event.target === celebration) closeCelebration(); };
  window.addEventListener('resize', () => { if (celebration.classList.contains('show')) resizePlayfield(); });
  setInterval(() => { if (!document.hidden && document.activeElement !== $('titleInput')) refresh().catch(() => {}); }, 5000);
  window.addEventListener('focus', () => refresh().catch(() => {}));
  refresh().catch((error) => alert(error.message));
})();
