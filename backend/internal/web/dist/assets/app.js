// EpicAI Admin — application bootstrap, router and realtime wiring.
import { api, wsUrl } from './api.js?v=1.4';
import { state } from './state.js?v=1.4';
import { renderShell, mountView } from './views.js?v=1.4';
import { el } from './dom.js?v=1.4';

const routes = [
  { path: '/dashboard', title: '仪表盘', icon: '◈', group: '系统总览', view: 'dashboard' },
  { path: '/sessions',  title: '会话管理',  icon: '⇄', group: '系统总览', view: 'sessions' },
  { path: '/models',    title: '模型管理',    icon: '⬢', group: '配置中心', view: 'models' },
  { path: '/api-keys',  title: 'API 密钥',  icon: '⚿', group: '配置中心', view: 'keys' },
  { path: '/files',     title: '文件存储',     icon: '▤', group: '数据查看', view: 'files' },
  { path: '/logs',      title: '日志审计',      icon: '☰', group: '数据查看', view: 'logs' },
  { path: '/settings',  title: '全局设置',  icon: '⚙', group: '系统管理', view: 'settings' },
];

function currentRoute() {
  const raw = location.hash.replace(/^#/, '') || '/dashboard';
  const base = raw.startsWith('/admin') ? raw.slice('/admin'.length) : raw;
  if (base === '' || base === '/') return routes[0];
  // /sessions/:id
  const parts = base.split('/').filter(Boolean);
  if (parts[0] === 'sessions' && parts[1]) {
    return { path: base, title: '会话详情', icon: '⇄', group: '系统总览', view: 'session', id: parts[1] };
  }
  const found = routes.find(r => r.path === '/' + parts[0]);
  return found || routes[0];
}

async function boot() {
  const root = document.getElementById('app');
  try {
    const urlParams = new URLSearchParams(location.search);
    if (urlParams.get('token')) {
      localStorage.setItem('epicai_token', urlParams.get('token'));
    }

    const token = localStorage.getItem('epicai_token');
    if (token) {
      api.token = token;
      state.token = token;
      try {
        const me = await api.me();
        state.user = me.user;
      } catch (err) {
        console.warn('Authentication token expired or invalid:', err);
        localStorage.removeItem('epicai_token');
        api.token = null;
        state.token = null;
        state.user = null;
      }
    }
    root.innerHTML = '';
    if (!state.user) {
      renderLogin(root);
      return;
    }

    root.appendChild(renderShell(routes));
    state.route = currentRoute();
    await mountView(state.route);
    connectWS();
    window.addEventListener('hashchange', async () => {
      state.route = currentRoute();
      await mountView(state.route);
    });
  } catch (fatal) {
    console.error('Boot failure:', fatal);
    root.innerHTML = '';
    renderLogin(root);
  }
}

function renderLogin(root) {
  const wrap = el('div', { class: 'login-wrap' });
  const box = el('div', { class: 'login-box' });
  box.appendChild(el('h1', {}, 'EpicAI'));
  box.appendChild(el('p', {}, 'OpenAI 兼容接口模拟器 / 故障注入与人工接管平台'));

  const form = el('form');
  const userField = el('label', { class: 'field' });
  userField.appendChild(el('span', {}, '管理员账号'));
  const user = el('input', { type: 'text', value: 'admin', autocomplete: 'username' });
  userField.appendChild(user);

  const passField = el('label', { class: 'field' });
  passField.appendChild(el('span', {}, '登录密码'));
  const pass = el('input', { type: 'password', value: 'epicai', autocomplete: 'current-password' });
  passField.appendChild(pass);

  const err = el('div', { style: 'color:var(--red);font-size:12.5px;min-height:18px' });
  const btn = el('button', { class: 'btn btn-primary', type: 'submit', style: 'width:100%;justify-content:center' }, '登录后台');

  form.appendChild(userField);
  form.appendChild(passField);
  form.appendChild(err);
  form.appendChild(btn);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    err.textContent = '';
    btn.disabled = true;
    try {
      const res = await api.login(user.value, pass.value);
      localStorage.setItem('epicai_token', res.token);
      api.token = res.token;
      state.user = res.user;
      location.hash = '#/dashboard';
      boot();
    } catch (e2) {
      err.textContent = e2.message || '登录失败，请检查账号密码';
      btn.disabled = false;
    }
  });
  box.appendChild(form);
  wrap.appendChild(box);
  root.appendChild(wrap);
}

function connectWS() {
  if (state.ws) { try { state.ws.close(); } catch {} }
  const curToken = api.token || state.token || localStorage.getItem('epicai_token') || '';
  let proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = `${proto}://${location.host}/admin/api/ws?token=${encodeURIComponent(curToken)}`;
  let ws;
  try { ws = new WebSocket(url); } catch { scheduleReconnect(); return; }
  state.ws = ws;

  ws.onopen = () => {
    console.log('WebSocket connected');
    state.connected = true;
    updateConn();
  };
  ws.onclose = () => {
    state.connected = false;
    updateConn();
    scheduleReconnect();
  };
  ws.onerror = (e) => {
    console.warn('WS error:', e);
    state.connected = false;
    updateConn();
  };
  ws.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      if (state.onEvent) state.onEvent(msg);
    } catch {}
  };
}

let reconnectTimer = null;
function scheduleReconnect() {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    if (!state.connected && state.user) connectWS();
  }, 3000);
}

function updateConn() {
  const dot = document.querySelector('.conn-dot');
  const txt = document.querySelector('.conn-text');
  if (dot) dot.classList.toggle('on', !!state.connected);
  if (txt) txt.textContent = state.connected ? '实时在线' : '离线';
}

state.updateConn = updateConn;
state.routes = routes;

boot();
