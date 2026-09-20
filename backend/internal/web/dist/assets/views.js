// View composition: shell + routed pages.
import { api } from './api.js?v=1.4';
import { state } from './state.js?v=1.4';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, fmtDateTime, stateBadge } from './dom.js?v=1.4';
import { dashboard } from './page-dashboard.js?v=1.4';
import { sessionsPage } from './page-sessions.js?v=1.4';
import { sessionPage } from './page-session.js?v=1.4';
import { modelsPage } from './page-models.js?v=1.4';
import { keysPage } from './page-keys.js?v=1.4';
import { filesPage } from './page-files.js?v=1.4';
import { logsPage } from './page-logs.js?v=1.4';
import { settingsPage } from './page-settings.js?v=1.4';

export function renderShell(routes) {
  const layout = el('div', { class: 'layout' });

  const sidebar = el('aside', { class: 'sidebar' });
  const brand = el('div', { class: 'brand' });
  brand.appendChild(el('div', { class: 'brand-name' }, 'EpicAI'));
  brand.appendChild(el('div', { class: 'brand-sub' }, '接口模拟与故障平台'));
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
  const changePwdBtn = el('button', { class: 'btn btn-sm', onclick: () => openChangePasswordModal() }, '修改密码');
  topbar.appendChild(changePwdBtn);
  const out = el('button', { class: 'btn btn-sm', onclick: () => {
    localStorage.removeItem('epicai_token');
    state.token = null; api.token = null; state.user = null;
    location.reload();
  } }, '退出登录');
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
  const cancel = el('button', { class: 'btn' }, '取消');
  const ok = el('button', { class: danger ? 'btn btn-danger' : 'btn btn-primary' }, '确认');
  foot.appendChild(cancel); foot.appendChild(ok);
  const m = modal(title, body, foot);
  cancel.addEventListener('click', () => m.close());
  ok.addEventListener('click', async () => { m.close(); await onConfirm(); });
  return m;
}

export function openChangePasswordModal() {
  const body = el('div');
  const oldPass = el('input', { type: 'password', placeholder: '请输入当前密码' });
  const newPass = el('input', { type: 'password', placeholder: '请输入新密码' });
  const confirmPass = el('input', { type: 'password', placeholder: '请再次输入新密码' });
  const errBox = el('div', { style: 'color:var(--red);font-size:12px;margin-top:4px;min-height:16px' });

  body.appendChild(el('label', { class: 'field' }, el('span', {}, '当前密码'), oldPass));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '新密码'), newPass));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '确认新密码'), confirmPass));
  body.appendChild(errBox);

  const foot = el('div', { class: 'row', style: 'justify-content:flex-end;gap:8px' });
  const cancel = el('button', { class: 'btn' }, '取消');
  const submit = el('button', { class: 'btn btn-primary' }, '确认修改');
  foot.appendChild(cancel); foot.appendChild(submit);

  const m = modal('修改管理员登录密码', body, foot);
  cancel.addEventListener('click', () => m.close());

  submit.addEventListener('click', async () => {
    errBox.textContent = '';
    if (!oldPass.value) { errBox.textContent = '请输入当前密码'; return; }
    if (!newPass.value) { errBox.textContent = '请输入新密码'; return; }
    if (newPass.value !== confirmPass.value) { errBox.textContent = '两次输入的新密码不一致'; return; }

    submit.disabled = true;
    try {
      await api.changePassword(oldPass.value, newPass.value);
      alert('密码修改成功，请重新登录！');
      m.close();
      localStorage.removeItem('epicai_token');
      location.reload();
    } catch (e) {
      errBox.textContent = e.message || '修改密码失败';
      submit.disabled = false;
    }
  });
}

export { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, fmtDateTime, stateBadge };
