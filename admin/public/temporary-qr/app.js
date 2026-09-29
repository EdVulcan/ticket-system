(function () {
  'use strict';
  const accessKey = 'temporary-qr-ticket-token-v1';
  const $ = (id) => document.getElementById(id);
  let tickets = [], revision = 0, filter = 'all', busy = false, signature = '';
  const token = () => sessionStorage.getItem(accessKey) || '';

  function lock() {
    sessionStorage.removeItem(accessKey);
    $('manager').hidden = true;
    $('lockScreen').hidden = false;
    $('passwordInput').value = '';
  }
  async function api(path, options = {}) {
    const response = await fetch('/api/temporary-qr/' + path, {
      ...options,
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token() },
      cache: 'no-store'
    });
    if (response.status === 401) { lock(); throw new Error('密码错误或登录已失效，请重新登录。'); }
    if (response.status === 409) throw new Error('其他终端已修改数据，已刷新，请重新操作。');
    if (!response.ok) throw new Error('服务器保存或读取失败，请稍后重试。');
    return response.json();
  }
  function apply(state) {
    const next = JSON.stringify(state);
    revision = state.revision;
    tickets = state.tickets || [];
    if (document.activeElement !== $('titleInput')) $('titleInput').value = state.title || '';
    if (next !== signature) { signature = next; render(); }
  }
  async function refresh() {
    if (busy || !token()) return;
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
      if (token()) {
        try { apply(await api('state')); } catch (_) { lock(); }
      }
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
  function openTicket(ticket) {
    $('dialogTitle').textContent = $('titleInput').value.trim() || '临时门票';
    $('dialogCode').textContent = ticket.code;
    $('dialogQr').replaceChildren();
    new QRCode($('dialogQr'), { text: ticket.code, width: 240, height: 240, correctLevel: QRCode.CorrectLevel.H });
    $('ticketDialog').showModal();
  }
  async function initManager() {
    try {
      apply(await api('state'));
      $('lockScreen').hidden = true; $('manager').hidden = false;
    } catch (error) {
      lock(); $('loginError').hidden = false; $('loginError').textContent = error.message;
    }
  }
  $('loginForm').onsubmit = async (event) => {
    event.preventDefault();
    const button = event.submitter; button.disabled = true;
    try {
      const result = await api('login', { method: 'POST', body: JSON.stringify({ password: $('passwordInput').value }) });
      sessionStorage.setItem(accessKey, result.token);
      await initManager();
    } catch (error) { $('loginError').hidden = false; $('loginError').textContent = error.message; }
    finally { button.disabled = false; }
  };
  $('logoutButton').onclick = lock;
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
  $('ticketDialog').onclick = (event) => { if (event.target === $('ticketDialog')) $('ticketDialog').close(); };
  setInterval(() => { if (!document.hidden && document.activeElement !== $('titleInput')) refresh().catch(() => {}); }, 5000);
  window.addEventListener('focus', () => refresh().catch(() => {}));
  if (token()) initManager();
})();
