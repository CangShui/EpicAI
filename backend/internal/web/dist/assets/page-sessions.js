// Session list with live updates and filters.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, stateBadge } from './dom.js';
import { card, emptyBox } from './views.js';

export async function sessionsPage(ctx) {
  const filters = { q: '', model: '', protocol: '', state: '', ip: '', active: '' };

  const toolbar = el('div', { class: 'toolbar' });
  const q = el('input', { type: 'text', placeholder: 'Search session / model / IP' });
  const modelSel = el('select');
  modelSel.appendChild(el('option', { value: '' }, 'All models'));
  const protoSel = el('select');
  protoSel.appendChild(el('option', { value: '' }, 'All protocols'));
  protoSel.appendChild(el('option', { value: 'chat.completions' }, 'chat.completions'));
  protoSel.appendChild(el('option', { value: 'responses' }, 'responses'));
  const stateSel = el('select');
  stateSel.appendChild(el('option', { value: '' }, 'All states'));
  ['CONNECTED', 'ECHOING', 'PAUSED', 'MANUAL', 'ERROR_PENDING', 'ENDED', 'CLIENT_DISCONNECTED']
    .forEach(s => stateSel.appendChild(el('option', { value: s }, s)));
  const ipIn = el('input', { type: 'text', placeholder: 'Client IP' });
  const activeOnly = el('input', { type: 'checkbox' });
  const refreshBtn = el('button', { class: 'btn btn-sm' }, 'Refresh');

  toolbar.appendChild(q);
  toolbar.appendChild(modelSel);
  toolbar.appendChild(protoSel);
  toolbar.appendChild(stateSel);
  toolbar.appendChild(ipIn);
  toolbar.appendChild(el('label', { class: 'row', style: 'gap:5px;font-size:12.5px' }, activeOnly, ' Active only'));
  toolbar.appendChild(refreshBtn);

  const c = card('Sessions', { tight: true });
  const wrap = el('div', { class: 'table-wrap' });
  const table = el('table');
  wrap.appendChild(table);
  c.body.appendChild(wrap);

  const pager = el('div', { class: 'row', style: 'margin-top:10px;justify-content:flex-end;gap:8px' });
  const prevBtn = el('button', { class: 'btn btn-sm' }, 'Prev');
  const nextBtn = el('button', { class: 'btn btn-sm' }, 'Next');
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
      pageInfo.textContent = `${offset + 1}–${offset + list.length} of ${res.total}`;
      prevBtn.disabled = offset <= 0;
      nextBtn.disabled = offset + list.length >= res.total;
      const badge = document.querySelector('[data-live]');
      if (badge) badge.textContent = String(res.total);
    } catch (e) {
      table.innerHTML = '';
      table.appendChild(el('tbody', {}, el('tr', {}, el('td', {}, 'Failed to load: ' + e.message))));
    }
  }

  function render(list) {
    clear(table);
    const thead = el('thead');
    const hr = el('tr');
    ['Session', 'Model', 'Protocol', 'Client', 'Started', 'Mode', 'State', 'Echo', 'Bytes Out', 'Rate', '']
      .forEach((h, i) => hr.appendChild(el('th', { class: i >= 7 ? 'num' : '' }, h)));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    if (!list.length) {
      tbody.appendChild(el('tr', {}, el('td', { colspan: '11' }, emptyBox('No sessions yet. Start one with the curl command on the Dashboard.'))));
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
      const open = el('a', { class: 'btn btn-xs', href: `#/admin/sessions/${s.session_id}` }, 'Open');
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
