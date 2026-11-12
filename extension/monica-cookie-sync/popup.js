const proxyEl = document.getElementById('proxy');
const passwordEl = document.getElementById('password');
const rememberEl = document.getElementById('remember');
const syncEl = document.getElementById('sync');
const statusEl = document.getElementById('status');

const DEFAULT_PROXY = 'https://monica2api.onrender.com';

function show(text, cls) {
  statusEl.textContent = text;
  statusEl.className = cls || 'muted';
}

async function init() {
  const { proxy } = await chrome.storage.local.get('proxy');
  proxyEl.value = proxy || DEFAULT_PROXY;
  // 密码只放在 session storage：浏览器关闭即清除
  const { password } = await chrome.storage.session.get('password');
  if (password) {
    passwordEl.value = password;
    rememberEl.checked = true;
  }
  try {
    const res = await fetch(proxyEl.value.replace(/\/+$/, '') + '/admin/cookie/status');
    const data = await res.json();
    const state = data.status && data.status.state;
    if (state === 'invalid') show('代理当前的 Cookie 已失效，请点击下方按钮更新。', 'err');
    else if (state === 'valid') show('代理当前的 Cookie 有效。', 'ok');
  } catch (e) { /* 代理可能在休眠，忽略 */ }
}

async function readMonicaCookie() {
  const cookies = await chrome.cookies.getAll({ domain: 'monica.im' });
  const byName = new Map();
  for (const c of cookies) byName.set(c.name, c.value);
  if (!byName.has('session_id')) return null;
  return Array.from(byName, ([k, v]) => k + '=' + v).join('; ');
}

async function ensureHostPermission(origin) {
  const pattern = origin + '/*';
  if (await chrome.permissions.contains({ origins: [pattern] })) return true;
  return chrome.permissions.request({ origins: [pattern] });
}

async function sync() {
  const proxy = proxyEl.value.trim().replace(/\/+$/, '');
  const password = passwordEl.value;
  if (!proxy || !password) { show('请填写代理地址和管理员密码。', 'err'); return; }

  let origin;
  try { origin = new URL(proxy).origin; } catch (e) { show('代理地址格式不对。', 'err'); return; }

  syncEl.disabled = true;
  try {
    if (!(await ensureHostPermission(origin))) { show('没有访问该代理地址的权限。', 'err'); return; }
    const cookie = await readMonicaCookie();
    if (!cookie) { show('没有找到 monica.im 的 session_id，请先在本浏览器登录 monica.im。', 'err'); return; }

    await chrome.storage.local.set({ proxy });
    if (rememberEl.checked) await chrome.storage.session.set({ password });
    else await chrome.storage.session.remove('password');

    show('正在提交并让代理向 Monica 验证…（代理休眠时首次可能需要 50 秒）');
    const res = await fetch(proxy + '/admin/cookie', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cookie, password })
    });
    let data = {};
    try { data = await res.json(); } catch (e) { /* ignore */ }
    if (res.ok && data.ok) show('✓ ' + data.message + '\n' + (data.note || ''), 'ok');
    else show('✗ ' + (data.error || ('HTTP ' + res.status)), 'err');
  } catch (e) {
    show('✗ ' + (e.message || String(e)), 'err');
  } finally {
    syncEl.disabled = false;
  }
}

syncEl.addEventListener('click', sync);
init();
