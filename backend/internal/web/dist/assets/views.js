// View composition: shell + routed pages.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, fmtDateTime, stateBadge } from './dom.js';
import { dashboard } from './page-dashboard.js';
import { sessionsPage } from './page-sessions.js';
import { sessionPage } from './page-session.js';
import { modelsPage } from './page-models.js';
import { scenariosPage } from './page-scenarios.js';
import { keysPage } from './page-keys.js';
import { filesPage } from './page-files.js';
import { logsPage } from './page-logs.js';
import { settingsPage } from './page-settings.js';

export function renderShell(routes) {
  const layout = el('div', { class: 'layout' });

  const sidebar = el('aside', { class: 'sidebar' });
  const brand = el('div', { class: 'brand' });
  brand.appendChild(el('div', { class: 'brand-name' }, 'EpicAI'));
  brand.appendChild(el('div', { class: 'brand-sub' }, 'Endpoint Simulator'));
  sidebar.appendChild(brand);

  const nav = el('nav', { class: 'nav' });
  const groups = {};
  for (const r of routes) {
    if (!groups[r.group]) {
      groups[r.group] = el('div', { class: 'nav-group' });
      groups[r.group].appendChild(el('div', { class: 'nav-group-title' }, r.group));
      nav.appendChild(groups[r.group]);
    }
    const item = el('a', {
      class: 'nav-item', href: '#/admin' + r.path,
    });
    item.dataset.path = r.path;
    item.appendChild(el('span', { class: 'ico' }, r.icon));
    item.appendChild(el('span', {}, r.title));
    if (r.view === 'sessions') {
      const b = el('span', { class: 'nav-badge' }, '0');
      b.dataset.live = '1';
      item.appendChild(b);
    }
    groups[r.group].appendChild(item);
  }
  sidebar.appendChild(nav);

  const main = el('div', { class: 'main' });
  const topbar = el('div', { class: 'topbar' });
  topbar.appendChild(el('h1', {}, ''));
  topbar.dataset.title = '1';
  topbar.appendChild(el('div', { style: 'flex:1' }));
  const conn = el('div', { class: 'conn' });
  conn.appendChild(el('span', { class: 'conn-dot' }));
  conn.appendChild(el('span', { class: 'conn-text' }, 'Offline'));
  topbar.appendChild(conn);
  topbar.appendChild(el('span', { class: 'dim mono' }, state.user || ''));
  const out = el('button', { class: 'btn btn-sm', onclick: () => {
    localStorage.removeItem('epicai_token');
    state.token = null; api.token = null; state.user = null;
    location.reload();
  } }, 'Sign out');
  topbar.appendChild(out);
  main.appendChild(topbar);

  const content = el('div', { class: 'content' });
  content.dataset.content = '1';
  main.appendChild(content);

  layout.appendChild(sidebar);
  layout.appendChild(main);
  return layout;
}

export async function mountView(route) {
  const content = document.querySelector('[data-content]');
  if (!content) return;
  clear(content);
  const titleEl = document.querySelector('[data-title] h1');
  if (titleEl) titleEl.textContent = route.title;

  document.querySelectorAll('.nav-item').forEach(n => {
    n.classList.toggle('active', n.dataset.path === route.path || route.view === 'session' && n.dataset.path === '/sessions');
  });

  const ctx = { content, route };
  switch (route.view) {
    case 'dashboard': return dashboard(ctx);
    case 'sessions': return sessionsPage(ctx);
    case 'session': return sessionPage(ctx);
    case 'models': return modelsPage(ctx);
    case 'scenarios': return scenariosPage(ctx);
    case 'keys': return keysPage(ctx);
    case 'files': return filesPage(ctx);
    case 'logs': return logsPage(ctx);
    case 'settings': return settingsPage(ctx);
    default: return dashboard(ctx);
  }
}

export function card(title, opts = {}) {
  const c = el('div', { class: 'card' });
  const head = el('div', { class: 'card-head' });
  if (title) head.appendChild(el('h2', {}, title));
  if (opts.head) head.appendChild(opts.head);
  if (title || opts.head) c.appendChild(head);
  const body = el('div', { class: 'card-body' + (opts.tight ? ' tight' : '') });
  if (opts.body) body.appendChild(opts.body);
  c.appendChild(body);
  return { root: c, body, head };
}

export function statTile(label, value, sub = '', tone = '') {
  const s = el('div', { class: 'stat' + (tone ? ' ' + tone : '') });
  s.appendChild(el('div', { class: 'stat-label' }, label));
  s.appendChild(el('div', { class: 'stat-value' }, value));
  if (sub) s.appendChild(el('div', { class: 'stat-sub' }, sub));
  return s;
}

export function emptyBox(msg) { return el('div', { class: 'empty' }, msg); }

export function modal(title, bodyNode, footerNode) {
  const back = el('div', { class: 'modal-backdrop' });
  const m = el('div', { class: 'modal' });
  const head = el('div', { class: 'modal-head' });
  head.appendChild(el('h3', {}, title));
  head.appendChild(el('div', { class: 'spacer' }));
  const x = el('button', { class: 'close-x' }, '×');
  x.addEventListener('click', () => back.remove());
  head.appendChild(x);
  m.appendChild(head);
  const b = el('div', { class: 'modal-body' });
  b.appendChild(bodyNode);
  m.appendChild(b);
  if (footerNode) {
    const f = el('div', { class: 'modal-foot' });
    f.appendChild(footerNode);
    m.appendChild(f);
  }
  back.appendChild(m);
  back.addEventListener('click', (e) => { if (e.target === back) back.remove(); });
  document.body.appendChild(back);
  return { root: back, body: b, close: () => back.remove() };
}

export function confirmModal(title, message, onConfirm, danger = true) {
  const body = el('div', {}, el('p', { style: 'margin:0 0 6px' }, message));
  const foot = el('div', { class: 'row' });
  const cancel = el('button', { class: 'btn' }, 'Cancel');
  const ok = el('button', { class: danger ? 'btn btn-danger' : 'btn btn-primary' }, 'Confirm');
  foot.appendChild(cancel); foot.appendChild(ok);
  const m = modal(title, body, foot);
  cancel.addEventListener('click', () => m.close());
  ok.addEventListener('click', async () => { m.close(); await onConfirm(); });
  return m;
}

export { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, fmtDateTime, stateBadge };
