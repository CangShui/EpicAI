// Session list with live updates and filters.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, stateBadge } from './dom.js';
import { card, emptyBox } from './views.js';

export async function sessionsPage(ctx) {
  const filters = { q: '', model: '', protocol: '', state: '', ip: '', active: '' };

  const toolbar = el('div', { class: 'toolbar' });
  const q = el('input', { type: 'text', placeholder: '搜索会话 ID / 模型 / IP' });
  const modelSel = el('select');
  modelSel.appendChild(el('option', { value: '' }, '全部模型'));
  const protoSel = el('select');
  protoSel.appendChild(el('option', { value: '' }, '全部协议'));
  protoSel.appendChild(el('option', { value: 'chat.completions' }, 'chat.completions'));
  protoSel.appendChild(el('option', { value: 'responses' }, 'responses'));
  const stateSel = el('select');
  stateSel.appendChild(el('option', { value: '' }, '全部状态'));
  [
    ['CONNECTED', '已连接'],
    ['ECHOING', '回显中 (Echoing)'],
    ['PAUSED', '已暂停'],
    ['MANUAL', '人工接管中'],
    ['ERROR_PENDING', '等待注入错误'],
    ['ENDED', '已结束'],
    ['CLIENT_DISCONNECTED', '客户端已断开']
  ].forEach(([s, l]) => stateSel.appendChild(el('option', { value: s }, l)));
  const ipIn = el('input', { type: 'text', placeholder: '客户端 IP' });
  const activeOnly = el('input', { type: 'checkbox' });
  const refreshBtn = el('button', { class: 'btn btn-sm' }, '刷新');

  toolbar.appendChild(q);
  toolbar.appendChild(modelSel);
  toolbar.appendChild(protoSel);
  toolbar.appendChild(stateSel);
  toolbar.appendChild(ipIn);
  toolbar.appendChild(el('label', { class: 'row', style: 'gap:5px;font-size:12.5px' }, activeOnly, ' 仅活跃会话'));
  toolbar.appendChild(refreshBtn);

  const c = card('会话列表', { tight: true });
  const wrap = el('div', { class: 'table-wrap' });
  const table = el('table');
  wrap.appendChild(table);
  c.body.appendChild(wrap);

  const pager = el('div', { class: 'row', style: 'margin-top:10px;justify-content:flex-end;gap:8px' });
  const prevBtn = el('button', { class: 'btn btn-sm' }, '上一页');
  const nextBtn = el('button', { class: 'btn btn-sm' }, '下一页');
  const pageInfo = el('span', { class: 'dim', style: 'font-size:12.5px' }, '');
  pager.appendChild(pageInfo); pager.appendChild(prevBtn); pager.appendChild(nextBtn);

  ctx.content.appendChild(toolbar);
  ctx.content.appendChild(c.root);
  ctx.content.appendChild(pager);

  let offset = 0;
  const limit = 50;

  async function loadModels() {
    try {
      const ms = await api.models();
      ms.forEach(m => modelSel.appendChild(el('option', { value: m.model_id }, m.model_id)));
    } catch {}
  }

  async function load() {
    try {
      const res = await api.sessions({
        q: q.value, model: modelSel.value, protocol: protoSel.value,
        state: stateSel.value, ip: ipIn.value,
        active: activeOnly.checked ? 'true' : '',
        limit, offset,
      });
      const list = res.sessions || [];
      render(list);
      pageInfo.textContent = `第 ${offset + 1}–${offset + list.length} 条，共 ${res.total} 条`;
      prevBtn.disabled = offset <= 0;
      nextBtn.disabled = offset + list.length >= res.total;
      const badge = document.querySelector('[data-live]');
      if (badge) badge.textContent = String(res.total);
    } catch (e) {
      table.innerHTML = '';
      table.appendChild(el('tbody', {}, el('tr', {}, el('td', {}, '加载会话失败: ' + e.message))));
    }
  }

  function render(list) {
    clear(table);
    const thead = el('thead');
    const hr = el('tr');
    ['会话 ID', '模型', '协议', '客户端 IP', '开始时间', '运行模式', '当前状态', '回显轮次', '已发送流量', '速率 (tok/s)', '操作']
      .forEach((h, i) => hr.appendChild(el('th', { class: i >= 7 && i <= 9 ? 'num' : '' }, h)));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    if (!list.length) {
      tbody.appendChild(el('tr', {}, el('td', { colspan: '11' }, emptyBox('暂无会话。可在终端执行 curl 命令开始测试。'))));
      table.appendChild(tbody);
      return;
    }
    for (const s of list) {
      const tr = el('tr');
      const link = el('a', { href: `#/admin/sessions/${s.session_id}` }, s.session_id);
      tr.appendChild(el('td', {}, link));
      tr.appendChild(el('td', { class: 'mono' }, s.model));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + (s.protocol === 'responses' ? 'purple' : 'blue') }, s.protocol)));
      tr.appendChild(el('td', { class: 'mono dim' }, s.client_ip || '—'));
      tr.appendChild(el('td', { class: 'nowrap dim' }, fmtTime(s.created_at)));
      tr.appendChild(el('td', {}, el('span', { class: 'badge gray' }, s.mode)));
      tr.appendChild(el('td', {}, stateBadge(s.state)));
      tr.appendChild(el('td', { class: 'num mono' }, fmtNum(s.echo_count)));
      tr.appendChild(el('td', { class: 'num mono' }, fmtBytes(s.bytes_out)));
      tr.appendChild(el('td', { class: 'num mono' }, fmtRate(s.current_rate)));
      const open = el('a', { class: 'btn btn-xs', href: `#/admin/sessions/${s.session_id}` }, '进入会话');
      tr.appendChild(el('td', {}, open));
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
  }

  [q, modelSel, protoSel, stateSel, ipIn].forEach(inp => {
    inp.addEventListener('change', () => { offset = 0; load(); });
    if (inp.tagName === 'INPUT') inp.addEventListener('keyup', debounce(() => { offset = 0; load(); }, 350));
  });
  activeOnly.addEventListener('change', () => { offset = 0; load(); });
  refreshBtn.addEventListener('click', load);
  prevBtn.addEventListener('click', () => { offset = Math.max(0, offset - limit); load(); });
  nextBtn.addEventListener('click', () => { offset += limit; load(); });

  await loadModels();
  await load();
  const timer = setInterval(load, 3000);
  if (state._cleanup) state._cleanup();
  state._cleanup = () => clearInterval(timer);
}

function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}
