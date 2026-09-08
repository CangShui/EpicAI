// Model management: create, edit, clone, enable/disable, delete.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtNum } from './dom.js';
import { card, modal, confirmModal, emptyBox } from './views.js';

const BEHAVIORS = [
  ['infinite_echo', 'Infinite Echo'],
  ['finite_echo', 'Finite Echo'],
  ['manual_only', 'Manual Only'],
  ['scenario', 'Scenario'],
  ['immediate_error', 'Immediate Error'],
  ['hang_forever', 'Hang Forever'],
  ['static_response', 'Normal Static Response'],
  ['connection_drop', 'Connection Drop'],
];

export async function modelsPage(ctx) {
  const toolbar = el('div', { class: 'toolbar' });
  const addBtn = el('button', { class: 'btn btn-primary btn-sm' }, '+ New model');
  const refreshBtn = el('button', { class: 'btn btn-sm' }, 'Refresh');
  toolbar.appendChild(addBtn);
  toolbar.appendChild(refreshBtn);
  toolbar.appendChild(el('span', { class: 'faint', style: 'font-size:12px' },
    'Changes apply immediately — no restart required.'));
  ctx.content.appendChild(toolbar);

  const c = card('Models', { tight: true });
  const wrap = el('div', { class: 'table-wrap' });
  c.body.appendChild(wrap);
  ctx.content.appendChild(c.root);

  addBtn.addEventListener('click', () => openEditor(null, load));
  refreshBtn.addEventListener('click', load);

  async function load() {
    let list = [];
    try { list = await api.models(); } catch (e) {
      clear(wrap); wrap.appendChild(emptyBox('Failed to load models: ' + e.message)); return;
    }
    clear(wrap);
    const table = el('table');
    const thead = el('thead');
    const hr = el('tr');
    ['Model ID', 'Display Name', 'Behavior', 'Interval', 'Echo Mode', 'Scenario', 'Status', 'Created', ''].forEach(h => hr.appendChild(el('th', {}, h)));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    if (!list.length) {
      tbody.appendChild(el('tr', {}, el('td', { colspan: '9' }, emptyBox('No models configured.'))));
    }
    list.forEach(m => {
      const tr = el('tr');
      tr.appendChild(el('td', { class: 'mono' }, m.model_id));
      tr.appendChild(el('td', {}, m.display_name));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + behaviorTone(m.behavior) }, m.behavior)));
      tr.appendChild(el('td', { class: 'num mono' }, m.default_echo_interval_ms + 'ms'));
      tr.appendChild(el('td', { class: 'mono dim' }, m.echo_content_mode || 'message'));
      tr.appendChild(el('td', { class: 'mono dim' }, m.scenario_id || '—'));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + (m.enabled ? 'green' : 'gray') }, m.enabled ? 'enabled' : 'disabled')));
      tr.appendChild(el('td', { class: 'nowrap dim' }, new Date(m.created_at).toLocaleDateString('en-GB')));
      const acts = el('div', { class: 'row', style: 'gap:5px' });
      const edit = el('button', { class: 'btn btn-xs' }, 'Edit');
      edit.addEventListener('click', () => openEditor(m, load));
      const tog = el('button', { class: 'btn btn-xs' }, m.enabled ? 'Disable' : 'Enable');
      tog.addEventListener('click', async () => { await api.updateModel(m.model_id, { enabled: !m.enabled }); load(); });
      const clone = el('button', { class: 'btn btn-xs' }, 'Clone');
      clone.addEventListener('click', async () => {
        const newId = prompt('New model id:', m.model_id + '-copy');
        if (!newId) return;
        await api.cloneModel(m.model_id, newId);
        load();
      });
      const del = el('button', { class: 'btn btn-xs btn-danger' }, 'Delete');
      del.addEventListener('click', () => confirmModal('Delete model',
        `Delete model "${m.model_id}"? Requests to it will return 404.`,
        async () => { await api.deleteModel(m.model_id); load(); }, true));
      acts.appendChild(edit); acts.appendChild(tog); acts.appendChild(clone); acts.appendChild(del);
      tr.appendChild(el('td', {}, acts));
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
  }

  await load();
}

function behaviorTone(b) {
  return {
    infinite_echo: 'green', finite_echo: 'cyan', manual_only: 'purple',
    scenario: 'blue', immediate_error: 'red', hang_forever: 'yellow',
    static_response: 'gray', connection_drop: 'red',
  }[b] || 'gray';
}

export function openEditor(model, onDone) {
  const isNew = !model;
  const m = Object.assign({
    model_id: '', display_name: '', behavior: 'infinite_echo',
    default_echo_interval_ms: 500, echo_content_mode: 'message',
    scenario_id: '', description: '', static_response: '',
    error_status: 503, error_code: '', error_type: '', error_message: '',
    token_rate: 0, max_echo_count: 0, enabled: true,
  }, model || {});

  const body = el('div');
  const idIn = el('input', { type: 'text', value: m.model_id });
  const nameIn = el('input', { type: 'text', value: m.display_name });
  const beh = el('select');
  BEHAVIORS.forEach(([v, l]) => beh.appendChild(el('option', { value: v, selected: m.behavior === v }, l)));
  const iv = el('input', { type: 'number', value: String(m.default_echo_interval_ms || 0) });
  const em = el('select');
  ['message', 'line', 'exact', 'block'].forEach(v => em.appendChild(el('option', { value: v, selected: (m.echo_content_mode || 'message') === v }, v)));
  const scen = el('input', { type: 'text', value: m.scenario_id || '' });
  const desc = el('input', { type: 'text', value: m.description || '' });
  const rate = el('input', { type: 'number', value: String(m.token_rate || 0) });
  const maxEcho = el('input', { type: 'number', value: String(m.max_echo_count || 0) });
  const staticIn = el('input', { type: 'text', value: m.static_response || '' });
  const eStatus = el('input', { type: 'number', value: String(m.error_status || 503) });
  const eCode = el('input', { type: 'text', value: m.error_code || '' });
  const eType = el('input', { type: 'text', value: m.error_type || '' });
  const eMsg = el('input', { type: 'text', value: m.error_message || '' });

  body.appendChild(el('div', { class: 'row grow' },
    el('label', { class: 'field', style: 'flex:2' }, el('span', {}, 'Model ID'), idIn),
    el('label', { class: 'field', style: 'flex:2' }, el('span', {}, 'Display name'), nameIn)));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Behavior'), beh));
  body.appendChild(el('div', { class: 'row grow' },
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Echo interval (ms)'), iv),
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Echo content mode'), em)));
  body.appendChild(el('div', { class: 'row grow' },
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Scenario ID'), scen),
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Token rate (0 = unlimited)'), rate)));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Description'), desc));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Static response (static_response behavior)'), staticIn));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Max echo count (finite_echo)'), maxEcho));
  const errBox = el('div', { class: 'notice info' }, 'Immediate error fields (used by immediate_error behavior)');
  body.appendChild(errBox);
  body.appendChild(el('div', { class: 'row grow' },
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'HTTP status'), eStatus),
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Error code'), eCode)));
  body.appendChild(el('div', { class: 'row grow' },
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Error type'), eType),
    el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Error message'), eMsg)));

  const foot = el('div', { class: 'row' });
  const cancel = el('button', { class: 'btn' }, 'Cancel');
  const save = el('button', { class: 'btn btn-primary' }, isNew ? 'Create' : 'Save');
  foot.appendChild(cancel); foot.appendChild(save);
  const md = modal(isNew ? 'New model' : 'Edit model', body, foot);
  cancel.addEventListener('click', () => md.close());
  save.addEventListener('click', async () => {
    const payload = {
      model_id: idIn.value, display_name: nameIn.value || idIn.value,
      behavior: beh.value, default_echo_interval_ms: Number(iv.value),
      echo_content_mode: em.value, scenario_id: scen.value, description: desc.value,
      token_rate: Number(rate.value), max_echo_count: Number(maxEcho.value),
      static_response: staticIn.value,
      error_status: Number(eStatus.value), error_code: eCode.value,
      error_type: eType.value, error_message: eMsg.value,
    };
    try {
      if (isNew) await api.createModel(payload);
      else await api.updateModel(m.model_id, payload);
      md.close();
      if (onDone) onDone();
    } catch (e) {
      alert('Failed: ' + e.message);
    }
  });
}
