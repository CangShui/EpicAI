// Scenario editor with the EpicAI action DSL.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear } from './dom.js';
import { card, modal, confirmModal, emptyBox } from './views.js';

const ACTIONS = ['echo', 'wait', 'pause', 'send_text', 'send_asset', 'send_raw_sse',
  'send_malformed_sse', 'finish', 'error', 'disconnect', 'loop'];

export async function scenariosPage(ctx) {
  const toolbar = el('div', { class: 'toolbar' });
  const addBtn = el('button', { class: 'btn btn-primary btn-sm' }, '+ New scenario');
  toolbar.appendChild(addBtn);
  toolbar.appendChild(el('span', { class: 'faint', style: 'font-size:12px' },
    'Bind a scenario to a model by setting the model\'s Scenario ID.'));
  ctx.content.appendChild(toolbar);

  const host = el('div');
  ctx.content.appendChild(host);

  addBtn.addEventListener('click', () => openEditor(null, load));

  async function load() {
    let list = [];
    try { list = await api.scenarios(); } catch (e) {
      clear(host); host.appendChild(emptyBox('Failed to load: ' + e.message)); return;
    }
    clear(host);
    if (!list.length) {
      host.appendChild(emptyBox('No scenarios yet. Create one, e.g. "429-after-10-chunks".'));
      return;
    }
    list.forEach(sc => {
      const c = card(sc.name + '  ' + sc.id, { tight: true });
      const b = el('div', { style: 'padding:12px 16px' });
      if (sc.description) b.appendChild(el('div', { class: 'dim', style: 'margin-bottom:8px' }, sc.description));
      const steps = el('div', { class: 'raw-view', style: 'max-height:200px' });
      (sc.steps || []).forEach((s, i) => {
        steps.appendChild(el('div', { class: 'raw-line' },
          el('span', { class: 'raw-seq' }, String(i + 1)),
          `${s.action} ${describe(s)}`));
      });
      b.appendChild(steps);
      const acts = el('div', { class: 'row', style: 'margin-top:10px' });
      const edit = el('button', { class: 'btn btn-xs' }, 'Edit');
      edit.addEventListener('click', () => openEditor(sc, load));
      const clone = el('button', { class: 'btn btn-xs' }, 'Duplicate');
      clone.addEventListener('click', async () => {
        await api.createScenario({ name: sc.name + ' (copy)', description: sc.description, steps: sc.steps });
        load();
      });
      const del = el('button', { class: 'btn btn-xs btn-danger' }, 'Delete');
      del.addEventListener('click', () => confirmModal('Delete scenario', `Delete "${sc.name}"?`, async () => {
        await api.deleteScenario(sc.id); load();
      }, true));
      acts.appendChild(edit); acts.appendChild(clone); acts.appendChild(del);
      b.appendChild(acts);
      c.body.appendChild(b);
      host.appendChild(c.root);
    });
  }

  await load();
}

function describe(s) {
  const bits = [];
  if (s.count) bits.push('count=' + s.count);
  if (s.interval_ms) bits.push('interval=' + s.interval_ms + 'ms');
  if (s.wait_ms) bits.push('wait=' + s.wait_ms + 'ms');
  if (s.text) bits.push('text="' + s.text.slice(0, 30) + '"');
  if (s.http_status) bits.push('status=' + s.http_status);
  if (s.message) bits.push('msg="' + s.message.slice(0, 30) + '"');
  if (s.error_mode) bits.push('mode=' + s.error_mode);
  if (s.loop) bits.push('loop');
  if (s.raw) bits.push('raw');
  return bits.join(' ');
}

function openEditor(scenario, onDone) {
  const isNew = !scenario;
  let steps = scenario ? (scenario.steps || []).map(s => ({ ...s })) : [
    { action: 'echo', count: 10, interval_ms: 100 },
    { action: 'error', http_status: 429, message: 'Rate limit exceeded', error_mode: 'sse_error' },
  ];

  const body = el('div');
  const nameIn = el('input', { type: 'text', value: scenario?.name || '' });
  const descIn = el('input', { type: 'text', value: scenario?.description || '' });
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Name'), nameIn));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Description'), descIn));

  const stepHost = el('div');
  body.appendChild(el('div', { class: 'hint', style: 'margin-bottom:4px' }, 'Steps'));
  body.appendChild(stepHost);
  const addStep = el('button', { class: 'btn btn-xs', style: 'margin-top:6px' }, '+ Add step');
  addStep.addEventListener('click', () => { steps.push({ action: 'echo', count: 1 }); renderSteps(); });
  body.appendChild(addStep);

  const jsonToggle = el('button', { class: 'btn btn-xs', style: 'margin-top:10px' }, 'Edit as JSON');
  body.appendChild(el('div', {}, jsonToggle));
  const jsonArea = el('textarea', { style: 'min-height:160px;display:none;margin-top:8px' });
  body.appendChild(jsonArea);

  jsonToggle.addEventListener('click', () => {
    if (jsonArea.style.display === 'none') {
      jsonArea.value = JSON.stringify(steps, null, 2);
      jsonArea.style.display = '';
      jsonToggle.textContent = 'Apply JSON';
    } else {
      try {
        steps = JSON.parse(jsonArea.value);
        jsonArea.style.display = 'none';
        jsonToggle.textContent = 'Edit as JSON';
        renderSteps();
      } catch (e) { alert('Invalid JSON: ' + e.message); }
    }
  });

  function renderSteps() {
    clear(stepHost);
    steps.forEach((s, i) => {
      const row = el('div', { class: 'row', style: 'gap:6px;margin-bottom:6px;align-items:flex-end' });
      row.appendChild(el('span', { class: 'faint mono', style: 'width:20px' }, String(i + 1)));
      const sel = el('select', { style: 'width:auto;min-width:130px' });
      ACTIONS.forEach(a => sel.appendChild(el('option', { value: a, selected: s.action === a }, a)));
      sel.addEventListener('change', () => { s.action = sel.value; renderSteps(); });
      row.appendChild(sel);

      if (['echo', 'loop'].includes(s.action)) {
        const c = el('input', { type: 'number', value: String(s.count || 1), style: 'width:74px' });
        c.addEventListener('change', () => s.count = Number(c.value));
        row.appendChild(labeled('count', c));
      }
      if (s.action === 'echo') {
        const iv = el('input', { type: 'number', value: String(s.interval_ms || 0), style: 'width:80px' });
        iv.addEventListener('change', () => s.interval_ms = Number(iv.value));
        row.appendChild(labeled('interval ms', iv));
      }
      if (s.action === 'wait') {
        const w = el('input', { type: 'number', value: String(s.wait_ms || 1000), style: 'width:84px' });
        w.addEventListener('change', () => s.wait_ms = Number(w.value));
        row.appendChild(labeled('wait ms', w));
      }
      if (['send_text', 'send_asset', 'send_raw_sse'].includes(s.action)) {
        const t = el('input', { type: 'text', value: s.text || s.raw || '', style: 'flex:1;min-width:130px' });
        t.addEventListener('change', () => { s.text = t.value; s.raw = t.value; });
        row.appendChild(t);
      }
      if (s.action === 'error') {
        const st = el('input', { type: 'number', value: String(s.http_status || 500), style: 'width:70px' });
        st.addEventListener('change', () => s.http_status = Number(st.value));
        row.appendChild(labeled('status', st));
        const cd = el('input', { type: 'text', value: s.error_code || '', style: 'width:90px' });
        cd.addEventListener('change', () => s.error_code = cd.value);
        row.appendChild(labeled('code', cd));
        const mg = el('input', { type: 'text', value: s.message || '', style: 'flex:1;min-width:120px' });
        mg.addEventListener('change', () => s.message = mg.value);
        row.appendChild(labeled('message', mg));
        const md = el('select', { style: 'width:auto' });
        [['sse_error', 'SSE error'], ['close', 'close'], ['http_error', 'http error'], ['malformed', 'malformed']]
          .forEach(([v, l]) => md.appendChild(el('option', { value: v, selected: (s.error_mode || 'sse_error') === v }, l)));
        md.addEventListener('change', () => s.error_mode = md.value);
        row.appendChild(md);
      }
      if (s.action === 'loop') {
        const lc = el('input', { type: 'number', value: String(s.loop_count || 0), style: 'width:80px' });
        lc.addEventListener('change', () => s.loop_count = Number(lc.value));
        row.appendChild(labeled('loop count (0=forever)', lc));
      }
      const up = el('button', { class: 'btn btn-xs' }, '↑');
      up.addEventListener('click', () => { if (i > 0) { [steps[i - 1], steps[i]] = [steps[i], steps[i - 1]]; renderSteps(); } });
      const dn = el('button', { class: 'btn btn-xs' }, '↓');
      dn.addEventListener('click', () => { if (i < steps.length - 1) { [steps[i + 1], steps[i]] = [steps[i], steps[i + 1]]; renderSteps(); } });
      const rm = el('button', { class: 'btn btn-xs btn-danger' }, '×');
      rm.addEventListener('click', () => { steps.splice(i, 1); renderSteps(); });
      row.appendChild(up); row.appendChild(dn); row.appendChild(rm);
      stepHost.appendChild(row);
    });
  }

  function labeled(text, input) {
    const l = el('label', { style: 'display:flex;flex-direction:column;gap:2px' });
    l.appendChild(el('span', { class: 'faint', style: 'font-size:10px' }, text));
    l.appendChild(input);
    return l;
  }

  renderSteps();

  const foot = el('div', { class: 'row' });
  const cancel = el('button', { class: 'btn' }, 'Cancel');
  const save = el('button', { class: 'btn btn-primary' }, isNew ? 'Create' : 'Save');
  foot.appendChild(cancel); foot.appendChild(save);
  const md = modal(isNew ? 'New scenario' : 'Edit scenario', body, foot);
  cancel.addEventListener('click', () => md.close());
  save.addEventListener('click', async () => {
    const payload = { name: nameIn.value, description: descIn.value, steps };
    try {
      if (isNew) await api.createScenario(payload);
      else await api.updateScenario(scenario.id, payload);
      md.close();
      if (onDone) onDone();
    } catch (e) { alert('Failed: ' + e.message); }
  });
}
