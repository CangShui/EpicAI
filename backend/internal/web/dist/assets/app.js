// EpicAI Admin — application bootstrap, router and realtime wiring.
import { api, wsUrl } from './api.js';
import { state } from './state.js';
import { renderShell, mountView } from './views.js';
import { el } from './dom.js';

const routes = [
  { path: '/dashboard', title: 'Dashboard', icon: '◈', group: 'Overview', view: 'dashboard' },
  { path: '/sessions',  title: 'Sessions',  icon: '⇄', group: 'Overview', view: 'sessions' },
  { path: '/models',    title: 'Models',    icon: '⬢', group: 'Configure', view: 'models' },
  { path: '/scenarios', title: 'Scenarios', icon: '⚡', group: 'Configure', view: 'scenarios' },
  { path: '/api-keys',  title: 'API Keys',  icon: '⚿', group: 'Configure', view: 'keys' },
  { path: '/files',     title: 'Files',     icon: '▤', group: 'Data', view: 'files' },
  { path: '/logs',      title: 'Logs',      icon: '☰', group: 'Data', view: 'logs' },
  { path: '/settings',  title: 'Settings',  icon: '⚙', group: 'System', view: 'settings' },
];

function currentRoute() {
  const raw = location.hash.replace(/^#/, '') || '/dashboard';
  const base = raw.startsWith('/admin') ? raw.slice('/admin'.length) : raw;
  if (base === '' || base === '/') return routes[0];
  // /sessions/:id
  const parts = base.split('/').filter(Boolean);
  if (parts[0] === 'sessions' && parts[1]) {
    return { path: base, title: 'Session', icon: '⇄', group: 'Overview', view: 'session', id: parts[1] };
  }
  const found = routes.find(r => r.path === '/' + parts[0]);
  return found || routes[0];
}

async function boot() {
  const root = document.getElementById('app');
  root.innerHTML = '';

  const urlParams = new URLSearchParams(location.search);
  if (urlParams.get('token')) {
    localStorage.setItem('epicai_token', urlParams.get('token'));
  }

  const token = localStorage.getItem('epicai_token');
  if (token) {
    try {
      const me = await api.me();
      state.user = me.user;
    } catch {
      localStorage.removeItem('epicai_token');
      api.token = null;
    }
  }
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
}

function renderLogin(root) {
  const wrap = el('div', { class: 'login-wrap' });
  const box = el('div', { class: 'login-box' });
  box.appendChild(el('h1', {}, 'EpicAI'));
  box.appendChild(el('p', {}, 'OpenAI Compatible AI Endpoint Simulator'));

  const form = el('form');
  const userField = el('label', { class: 'field' });
  userField.appendChild(el('span', {}, 'Username'));
  const user = el('input', { type: 'text', value: 'admin', autocomplete: 'username' });
  userField.appendChild(user);

  const passField = el('label', { class: 'field' });
  passField.appendChild(el('span', {}, 'Password'));
  const pass = el('input', { type: 'password', value: 'epicai', autocomplete: 'current-password' });
  passField.appendChild(pass);

  const err = el('div', { style: 'color:var(--red);font-size:12.5px;min-height:18px' });
  const btn = el('button', { class: 'btn btn-primary', type: 'submit', style: 'width:100%;justify-content:center' }, 'Sign in');

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
      err.textContent = e2.message || 'Login failed';
      btn.disabled = false;
    }
  });
  box.appendChild(form);
  wrap.appendChild(box);
  root.appendChild(wrap);
}

function connectWS() {
  if (state.ws) { try { state.ws.close(); } catch {} }
  let proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const url = `${proto}://${location.host}/admin/api/ws?token=${encodeURIComponent(api.token || '')}`;
  let ws;
  try { ws = new WebSocket(url); } catch { scheduleReconnect(); return; }
  state.ws = ws;

  ws.onopen = () => { state.connected = true; updateConn(); };
  ws.onclose = () => { state.connected = false; updateConn(); scheduleReconnect(); };
  ws.onerror = () => { state.connected = false; updateConn(); };
  ws.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      if (state.onEvent) state.onEvent(msg);
    } catch {}
  };
}

function scheduleReconnect() {
  setTimeout(() => { if (!state.connected) connectWS(); }, 3000);
}

function updateConn() {
  const dot = document.querySelector('.conn-dot');
  const txt = document.querySelector('.conn-text');
  if (dot) dot.classList.toggle('on', !!state.connected);
  if (txt) txt.textContent = state.connected ? 'Live' : 'Offline';
}

state.updateConn = updateConn;
state.routes = routes;

boot();
