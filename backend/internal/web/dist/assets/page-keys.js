// API key management.
import { api } from './api.js';
import { el, clear, fmtNum } from './dom.js';
import { card, modal, confirmModal, emptyBox } from './views.js';

export async function keysPage(ctx) {
  const c = card('Rate & Key Settings', { tight: true });
  const cfgBody = el('div', { style: 'padding:14px 16px' });
  c.body.appendChild(cfgBody);
  ctx.content.appendChild(c.root);

  let settings = null;
  try { settings = await api.settings(); } catch {}
  const rt = settings?.runtime || {};

  const modeSel = el('select', { style: 'width:auto;min-width:190px' });
  [['any', 'Accept Any Non-empty Key'], ['require', 'Require Managed Key'], ['allow_empty', 'Allow Empty Key']]
    .forEach(([v, l]) => modeSel.appendChild(el('option', { value: v, selected: rt.key_mode === v }, l)));
  modeSel.addEventListener('change', async () => {
    await api.updateSettings({ key_mode: modeSel.value });
    notice.textContent = 'Key mode updated to: ' + modeSel.value;
  });
  const notice = el('div', { class: 'notice', style: 'margin:0' },
    'API keys are only stored as SHA-256 hashes. Only fingerprints are displayed.');
  const row = el('div', { class: 'row', style: 'margin-top:10px' });
  row.appendChild(el('span', {}, 'Key mode:'));
  row.appendChild(modeSel);
  cfgBody.appendChild(row);
  cfgBody.appendChild(el('div', { style: 'margin-top:10px' }, notice));
  cfgBody.appendChild(el('div', { class: 'hint' },
    'In "Require Managed Key" mode unknown keys receive HTTP 401.'));

  const toolbar = el('div', { class: 'toolbar', style: 'margin-top:16px' });
  const addBtn = el('button', { class: 'btn btn-primary btn-sm' }, '+ Create key');
  toolbar.appendChild(addBtn);
  ctx.content.appendChild(toolbar);

  const kc = card('API Keys', { tight: true });
  const wrap = el('div', { class: 'table-wrap' });
  kc.body.appendChild(wrap);
  ctx.content.appendChild(kc.root);

  addBtn.addEventListener('click', () => openCreate(load));

  async function load() {
    let list = [];
    try { list = await api.keys(); } catch (e) {
      clear(wrap); wrap.appendChild(emptyBox('Failed: ' + e.message)); return;
    }
    clear(wrap);
    const table = el('table');
    const thead = el('thead');
    const hr = el('tr');
    ['Name', 'Fingerprint', 'Status', 'Uses', 'Last Used', 'Models', 'Max Sessions', ''].forEach(h => hr.appendChild(el('th', {}, h)));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    if (!list.length) tbody.appendChild(el('tr', {}, el('td', { colspan: '8' }, emptyBox('No managed keys yet.'))));
    list.forEach(k => {
      const tr = el('tr');
      tr.appendChild(el('td', {}, k.name));
      tr.appendChild(el('td', { class: 'mono' }, k.masked || k.fingerprint));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + (k.enabled ? 'green' : 'gray') }, k.enabled ? 'enabled' : 'disabled')));
      tr.appendChild(el('td', { class: 'num mono' }, fmtNum(k.use_count)));
      tr.appendChild(el('td', { class: 'nowrap dim' }, k.last_used_at ? new Date(k.last_used_at).toLocaleString('en-GB') : 'never'));
      tr.appendChild(el('td', { class: 'dim' }, (k.models || []).join(', ') || 'all'));
      tr.appendChild(el('td', { class: 'num mono' }, k.max_sessions || '—'));
      const acts = el('div', { class: 'row', style: 'gap:5px' });
      const tog = el('button', { class: 'btn btn-xs' }, k.enabled ? 'Disable' : 'Enable');
      tog.addEventListener('click', async () => { await api.updateKey(k.id, { enabled: !k.enabled }); load(); });
      const del = el('button', { class: 'btn btn-xs btn-danger' }, 'Delete');
      del.addEventListener('click', () => confirmModal('Delete key', `Delete key "${k.name}"?`, async () => {
        await api.deleteKey(k.id); load();
      }, true));
      acts.appendChild(tog); acts.appendChild(del);
      tr.appendChild(el('td', {}, acts));
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
  }

  await load();
}

function openCreate(onDone) {
  const body = el('div');
  const name = el('input', { type: 'text', value: 'key-' + Date.now() });
  const models = el('input', { type: 'text', placeholder: 'comma separated, blank = all' });
  const maxSessions = el('input', { type: 'number', value: '0' });
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Name'), name));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Allowed models (blank = all)'), models));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Max concurrent sessions (0 = global default)'), maxSessions));
  const foot = el('div', { class: 'row' });
  const cancel = el('button', { class: 'btn' }, 'Cancel');
  const ok = el('button', { class: 'btn btn-primary' }, 'Create');
  foot.appendChild(cancel); foot.appendChild(ok);
  const md = modal('Create API key', body, foot);
  cancel.addEventListener('click', () => md.close());
  ok.addEventListener('click', async () => {
    try {
      const res = await api.createKey({
        name: name.value,
        models: models.value ? models.value.split(',').map(s => s.trim()).filter(Boolean) : null,
        max_sessions: Number(maxSessions.value),
      });
      md.close();
      showKey(res);
      if (onDone) onDone();
    } catch (e) { alert('Failed: ' + e.message); }
  });
}

function showKey(key) {
  const body = el('div');
  body.appendChild(el('div', { class: 'notice danger' },
    'This is the only time the full key is shown. Copy it now.'));
  const box = el('div', { class: 'raw-view' }, key.key);
  body.appendChild(box);
  body.appendChild(el('div', { class: 'hint' }, 'Stored as: ' + (key.masked || '')));
  const foot = el('div', { class: 'row' });
  const copy = el('button', { class: 'btn' }, 'Copy');
  const close = el('button', { class: 'btn btn-primary' }, 'Done');
  copy.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(key.key); copy.textContent = 'Copied'; } catch {}
  });
  foot.appendChild(copy); foot.appendChild(close);
  const md = modal('API key created', body, foot);
  close.addEventListener('click', () => md.close());
}
