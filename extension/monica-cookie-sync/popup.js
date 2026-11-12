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

// 找到当前标签页所在的 Cookie 存储（Ego / Chrome 多个空间或配置文件时各自独立）
async function currentStoreIds() {
  const ids = [];
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    const stores = await chrome.cookies.getAllCookieStores();
    if (tab) {
      const own = stores.find(s => (s.tabIds || []).includes(tab.id));
      if (own) ids.push(own.id);
    }
    for (const s of stores) if (!ids.includes(s.id)) ids.push(s.id);
  } catch (e) { /* 忽略，使用默认存储 */ }
  if (ids.length === 0) ids.push(undefined);
  return ids;
}

async function collect(query) {
  try { return await chrome.cookies.getAll(query); } catch (e) { return []; }
}

// 返回 { cookie, names }：cookie 为拼好的 Cookie 串（找不到 session_id 时为 null），names 仅用于诊断
async function readMonicaCookie() {
  const seenNames = new Set();
  for (const storeId of await currentStoreIds()) {
    const base = storeId === undefined ? {} : { storeId };
    const found = [
      ...(await collect({ ...base, domain: 'monica.im' })),
      ...(await collect({ ...base, url: 'https://monica.im/' })),
      ...(await collect({ ...base, url: 'https://api.monica.im/' })),
      // 分区 Cookie（CHIPS）
      ...(await collect({ ...base, domain: 'monica.im', partitionKey: {} })),
      ...(await collect({ ...base, url: 'https://api.monica.im/', partitionKey: { topLevelSite: 'https://monica.im' } })),
    ];
    const byName = new Map();
    for (const c of found) { byName.set(c.name, c.value); seenNames.add(c.name); }
    if (byName.has('session_id')) {
      return { cookie: Array.from(byName, ([k, v]) => k + '=' + v).join('; '), names: [] };
    }
  }
  // 兜底：部分浏览器（如 Ego）的 cookies API 只返回部分 Cookie。
  // Monica 的 session_id 不是 HttpOnly，可以直接在 monica.im 标签页里读取 document.cookie。
  const fromPage = await readFromActiveTab();
  if (fromPage.cookie) return fromPage;
  return { cookie: null, names: Array.from(new Set([...seenNames, ...fromPage.names])), pageError: fromPage.error };
}

async function readFromActiveTab() {
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (!tab || !/^https:\/\/([a-z0-9-]+\.)*monica\.im\//i.test(tab.url || '')) {
      return { cookie: null, names: [], error: '请在 monica.im 的标签页上点击扩展图标' };
    }
    const [res] = await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: () => document.cookie });
    const raw = (res && res.result) || '';
    const names = raw.split(';').map(x => x.split('=')[0].trim()).filter(Boolean);
    return { cookie: /(^|;\s*)session_id=/.test(raw) ? raw : null, names };
  } catch (e) {
    return { cookie: null, names: [], error: e.message || String(e) };
  }
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
    const { cookie, names, pageError } = await readMonicaCookie();
    if (!cookie) {
      show('没有找到 monica.im 的 session_id。\n' +
        (pageError ? pageError + '\n' : '') +
        (names.length ? '找到的 Cookie 名称：' + names.join(', ') : '扩展读不到任何 monica.im 的 Cookie。') +
        '\n也可以改用代理网页手动粘贴 Cookie。', 'err');
      return;
    }

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
