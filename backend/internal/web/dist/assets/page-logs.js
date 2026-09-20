// Admin audit log and conversation/access logs.
import { api } from './api.js';
import { el, clear, fmtDateTime } from './dom.js';
import { card, emptyBox } from './views.js';

export async function logsPage(ctx) {
  const tabs = el('div', { class: 'tabs' });
  const host = el('div', { style: 'margin-top:14px' });
  let active = 'audit';
  [['audit', '管理员操作审计 (Admin Audit)'], ['vibe', '白话全链路开发审计 (Vibe Logger)'], ['conversation', '会话事件明细 (Conversation)'], ['access', '请求访问日志 (Access Log)']].forEach(([k, l]) => {
    const t = el('div', { class: 'tab' + (k === active ? ' active' : '') }, l);
    t.addEventListener('click', () => {
      active = k;
      document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
      t.classList.add('active');
      render();
    });
    tabs.appendChild(t);
  });
  ctx.content.appendChild(tabs);
  ctx.content.appendChild(host);

  const toolbar = el('div', { class: 'toolbar' });
  const sessIn = el('input', { type: 'text', placeholder: '输入会话 ID 查看事件明细' });
  const loadBtn = el('button', { class: 'btn btn-sm' }, '查询');
  toolbar.appendChild(sessIn); toolbar.appendChild(loadBtn);
  ctx.content.insertBefore(toolbar, host);

  async function render() {
    clear(host);
    if (active === 'audit') {
      const c = card('管理员操作审计日志', { tight: true });
      const wrap = el('div', { class: 'table-wrap' });
      c.body.appendChild(wrap);
      host.appendChild(c.root);
      let list = [];
      try { list = await api.audit(300); } catch (e) {
        wrap.appendChild(emptyBox('加载日志失败: ' + e.message)); return;
      }
      const table = el('table');
      const hr = el('tr');
      ['操作时间', '管理员', '动作指令', '涉及会话', '参数明细', '操作来源 IP'].forEach(h => hr.appendChild(el('th', {}, h)));
      table.appendChild(el('thead', {}, hr));
      const tbody = el('tbody');
      if (!list.length) tbody.appendChild(el('tr', {}, el('td', { colspan: '6' }, emptyBox('暂无管理员操作记录。'))));
      list.forEach(a => {
        const tr = el('tr');
        tr.appendChild(el('td', { class: 'nowrap dim' }, fmtDateTime(a.timestamp)));
        tr.appendChild(el('td', {}, a.admin));
        tr.appendChild(el('td', {}, el('span', { class: 'badge ' + actionTone(a.action) }, a.action)));
        tr.appendChild(el('td', { class: 'mono', style: 'font-size:11.5px' }, a.session_id || '—'));
        tr.appendChild(el('td', { class: 'mono', style: 'font-size:11.5px' }, a.params || '—'));
        tr.appendChild(el('td', { class: 'mono dim' }, a.ip || '—'));
        tbody.appendChild(tr);
      });
      table.appendChild(tbody);
      wrap.appendChild(table);
      return;
    }

    if (active === 'vibe') {
      const c = card('白话全链路开发审计日志 (Vibe Coding Audit)', { tight: true });
      const box = el('div', { class: 'raw-view', style: 'max-height:600px;overflow-y:auto' });
      c.body.appendChild(box);
      host.appendChild(c.root);
      try {
        const res = await api.vlogs();
        const audits = res.audit || [];
        const fronts = res.frontend || [];
        box.appendChild(el('div', { class: 'faint', style: 'margin-bottom:8px' }, `后端全链路白话日志 (${audits.length}条落盘)`));
        audits.forEach((l, i) => {
          box.appendChild(el('div', { class: 'raw-line' }, el('span', { class: 'raw-seq' }, '#' + (i + 1)), l));
        });
        if (fronts.length) {
          box.appendChild(el('div', { class: 'faint', style: 'margin:14px 0 8px' }, `前端上报白话日志 (${fronts.length}条落盘)`));
          fronts.forEach((l, i) => {
            box.appendChild(el('div', { class: 'raw-line' }, el('span', { class: 'raw-seq' }, 'FE#' + (i + 1)), l));
          });
        }
        box.scrollTop = box.scrollHeight;
      } catch (e) {
        box.appendChild(emptyBox('加载失败: ' + e.message));
      }
      return;
    }

    if (active === 'conversation') {
      const sid = sessIn.value.trim();
      if (!sid) {
        host.appendChild(emptyBox('请在上方的输入框中输入会话 ID 并点击查询。'));
        return;
      }
      const res = await api.sessionEvents(sid, 500);
      const box = el('div', { class: 'raw-view' });
      (res.events || []).forEach(e => {
        box.appendChild(el('div', { class: 'raw-line' },
          el('span', { class: 'raw-seq' }, '#' + e.seq),
          `[${e.kind}] ${e.role} tokens=${e.tokens} :: ${String(e.content).slice(0, 300)}`));
      });
      host.appendChild(box);
      return;
    }

    // access log = session records
    const res = await api.sessions({ limit: 200 });
    const c = card('HTTP 访问请求记录', { tight: true });
    const wrap = el('div', { class: 'table-wrap' });
    c.body.appendChild(wrap);
    host.appendChild(c.root);
    const table = el('table');
    const hr = el('tr');
    ['请求时间', '方法与路径', '协议类型', '模型 ID', '客户端 IP', 'API Key 指纹', '最终状态', '流量输出'].forEach(h => hr.appendChild(el('th', {}, h)));
    table.appendChild(el('thead', {}, hr));
    const tbody = el('tbody');
    (res.sessions || []).forEach(s => {
      const tr = el('tr');
      tr.appendChild(el('td', { class: 'nowrap dim' }, fmtDateTime(s.created_at)));
      tr.appendChild(el('td', { class: 'mono', style: 'font-size:11.5px' },
        `${s.request?.method || 'POST'} ${s.request?.path || '—'}`));
      tr.appendChild(el('td', {}, s.protocol));
      tr.appendChild(el('td', { class: 'mono' }, s.model));
      tr.appendChild(el('td', { class: 'mono dim' }, s.client_ip));
      tr.appendChild(el('td', { class: 'mono dim' }, s.key_fingerprint ? 'sk-****' + s.key_fingerprint.slice(-4) : '—'));
      tr.appendChild(el('td', {}, el('span', { class: 'badge gray' }, s.state)));
      tr.appendChild(el('td', { class: 'num mono' }, s.bytes_out));
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
  }

  loadBtn.addEventListener('click', render);
  await render();
}

function actionTone(a) {
  if (a.includes('DELETE') || a.includes('DROP')) return 'red';
  if (a.includes('INJECT')) return 'yellow';
  if (a.includes('TAKEOVER') || a.includes('SEND')) return 'purple';
  if (a.includes('CREATE')) return 'green';
  if (a.includes('UPDATE')) return 'blue';
  return 'gray';
}
