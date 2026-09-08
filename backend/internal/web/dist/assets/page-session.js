// Session detail: control bar, live conversation, raw inspector, fault injection.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration, fmtTime, fmtDateTime } from './dom.js';
import { card, modal, confirmModal } from './views.js';

const MAX_EVENTS = 1000; // UI cap; full log stays server-side

export async function sessionPage(ctx) {
  const id = ctx.route.id;
  ctx.content.appendChild(el('div', { class: 'empty' }, 'Loading session…'));

  let session = null;
  try {
    session = await api.session(id);
  } catch {
    clear(ctx.content);
    ctx.content.appendChild(el('div', { class: 'empty' }, 'Session not found: ' + id));
    return;
  }

  clear(ctx.content);

  // ---------- header ----------
  const head = el('div', { class: 'row', style: 'margin-bottom:12px;align-items:center' });
  head.appendChild(el('a', { class: 'btn btn-sm', href: '#/admin/sessions' }, '← Sessions'));
  head.appendChild(el('span', { class: 'mono', style: 'font-size:14px' }, id));
  const stateChip = el('span', { class: 'badge' }, session.state);
  head.appendChild(stateChip);
  head.appendChild(el('span', { class: 'dim mono' }, session.model));
  head.appendChild(el('span', { class: 'badge ' + (session.protocol === 'responses' ? 'purple' : 'blue') }, session.protocol));
  if (!session.streaming) head.appendChild(el('span', { class: 'badge gray' }, 'non-streaming'));
  ctx.content.appendChild(head);

  // ---------- control bar ----------
  const bar = el('div', { class: 'control-bar' });
  const mk = (label, cls, action, extra = null) => {
    const b = el('button', { class: 'btn btn-sm ' + (cls || '') }, label);
    b.addEventListener('click', () => { if (extra) extra(); else send(action, {}); });
    return b;
  };
  bar.appendChild(mk('PAUSE', '', { action: 'pause' }));
  bar.appendChild(mk('RESUME', '', { action: 'resume' }));
  bar.appendChild(el('div', { class: 'control-sep' }));
  bar.appendChild(mk('TAKE OVER', 'btn-warn', { action: 'takeover' }));
  bar.appendChild(mk('RETURN TO ECHO', '', { action: 'return' }));
  const sendBtn = el('button', { class: 'btn btn-sm btn-primary' }, 'SEND');
  sendBtn.addEventListener('click', openManualSend);
  bar.appendChild(sendBtn);
  bar.appendChild(el('div', { class: 'control-sep' }));
  const finishBtn = el('button', { class: 'btn btn-sm' }, 'FINISH');
  finishBtn.addEventListener('click', () => confirmModal('Finish normally', 'End this stream with a valid finish_reason and close cleanly?', () => send({ action: 'finish' }, {}), false));
  bar.appendChild(finishBtn);
  const injectBtn = el('button', { class: 'btn btn-sm btn-warn' }, 'INJECT ERROR');
  injectBtn.addEventListener('click', openInject);
  bar.appendChild(injectBtn);
  const dropBtn = el('button', { class: 'btn btn-sm btn-danger' }, 'DROP CONNECTION');
  dropBtn.addEventListener('click', () => confirmModal('Drop connection', 'Close the socket immediately without any clean ending?', () => send({ action: 'drop' }, {}), true));
  bar.appendChild(dropBtn);
  bar.appendChild(el('div', { class: 'control-sep' }));
  const delBtn = el('button', { class: 'btn btn-sm btn-danger' }, 'DELETE');
  delBtn.addEventListener('click', () => confirmModal('Delete session', 'Permanently delete this session and its logs?', async () => {
    await api.deleteSession(id);
    location.hash = '#/admin/sessions';
  }, true));
  bar.appendChild(delBtn);
  ctx.content.appendChild(bar);

  // ---------- inspector ----------
  const inspCard = card('Session Inspector', { tight: true });
  const inspBody = el('div', { style: 'padding:14px 16px' });
  inspCard.body.appendChild(inspBody);
  ctx.content.appendChild(inspCard.root);

  // ---------- rate control ----------
  const rateCard = card('Rate Control', { tight: true });
  const rateBody = el('div', { style: 'padding:14px 16px' });
  rateCard.body.appendChild(rateBody);
  ctx.content.appendChild(rateCard.root);

  // ---------- tabs ----------
  const tabsRow = el('div', { class: 'tabs' });
  const tabDefs = [
    { key: 'convo', label: 'Conversation' },
    { key: 'request', label: 'Raw Request' },
    { key: 'response', label: 'Raw Response' },
    { key: 'sse', label: 'Raw SSE' },
  ];
  let activeTab = 'convo';
  tabDefs.forEach(t => {
    const tab = el('div', { class: 'tab' + (t.key === activeTab ? ' active' : '') }, t.label);
    tab.addEventListener('click', () => {
      activeTab = t.key;
      document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
      tab.classList.add('active');
      renderTab();
    });
    tabsRow.appendChild(tab);
  });
  const tabHost = el('div', { style: 'margin-top:14px' });
  ctx.content.appendChild(tabsRow);
  ctx.content.appendChild(tabHost);

  // ---------- live conversation (capped) ----------
  let convoLines = [];
  const convo = el('div', { class: 'convo' });
  let lastSeq = 0;

  async function send(body, opts = {}) {
    try {
      await api.control(id, body);
      if (!opts.quiet) flash('ok');
      await refresh();
    } catch (e) {
      flash('error: ' + e.message);
    }
  }

  function flash(msg) {
    const n = el('span', { class: 'dim', style: 'font-size:12px;margin-left:8px' }, msg);
    bar.appendChild(n);
    setTimeout(() => n.remove(), 2500);
  }

  function openManualSend() {
    const ta = el('textarea', { placeholder: 'Type a manual assistant response…', style: 'min-height:110px' });
    const modeSel = el('select');
    modeSel.appendChild(el('option', { value: 'full' }, 'Send once (complete)'));
    modeSel.appendChild(el('option', { value: 'typing' }, 'Simulate typing (uses chars/sec setting)'));
    const body = el('div');
    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Manual response'), ta));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Delivery mode'), modeSel));
    const foot = el('div', { class: 'row' });
    const cancel = el('button', { class: 'btn' }, 'Cancel');
    const ok = el('button', { class: 'btn btn-primary' }, 'Send to client');
    foot.appendChild(cancel); foot.appendChild(ok);
    const m = modal('Manual message', body, foot);
    cancel.addEventListener('click', () => m.close());
    ok.addEventListener('click', async () => {
      const text = ta.value;
      if (!text.trim()) return;
      if (modeSel.value === 'typing') {
        const cps = Number(prompt('Characters per second:', '10') || 10);
        await api.updateSettings({ manual_chars_per_second: cps });
      } else {
        await api.updateSettings({ manual_chars_per_second: 0 });
      }
      m.close();
      await send({ action: 'send', text }, {});
    });
  }

  function openInject() {
    const body = el('div');
    const status = el('input', { type: 'number', value: '429' });
    const code = el('input', { type: 'text', value: '6004' });
    const type = el('input', { type: 'text', value: 'rate_limit_error' });
    const msg = el('input', { type: 'text', value: '您的使用量已超出频率限制' });
    const modeSel = el('select');
    modeSel.appendChild(el('option', { value: 'sse_error' }, 'B: SSE error event (default)'));
    modeSel.appendChild(el('option', { value: 'close' }, 'A: Close connection'));
    modeSel.appendChild(el('option', { value: 'http_error' }, 'C: In-band HTTP error'));
    modeSel.appendChild(el('option', { value: 'malformed' }, 'D: Malformed chunk then close'));
    const delay = el('input', { type: 'number', value: '0' });
    const afterChunks = el('input', { type: 'number', value: '0' });
    const raw = el('textarea', { placeholder: '{"code":429,"message":"upstream 429"}' });
    const rawMode = el('input', { type: 'checkbox' });

    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'HTTP status'), status));
    body.appendChild(el('div', { class: 'row grow' },
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Error code'), code),
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Error type'), type)));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Error message'), msg));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Failure mode'), modeSel));
    body.appendChild(el('div', { class: 'row grow' },
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'Delay (ms)'), delay),
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'After N chunks (0 = now)'), afterChunks)));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'Raw JSON (returned byte-for-byte when RAW is checked)'), raw));
    body.appendChild(el('label', { class: 'row', style: 'gap:6px;font-size:12.5px' }, rawMode, ' RAW RESPONSE'));

    const presetHost = el('div', { class: 'preset-grid', style: 'margin-bottom:14px' });
    api.presets().then(ps => {
      ps.forEach(p => {
        const b = el('button', { class: 'preset-btn' },
          el('div', { class: 'st' }, String(p.http_status)),
          el('div', { class: 'nm' }, p.code || ''));
        b.addEventListener('click', () => {
          status.value = p.http_status; code.value = p.code || '';
          type.value = p.type || ''; msg.value = p.message || '';
        });
        presetHost.appendChild(b);
      });
    }).catch(() => {});
    body.insertBefore(el('div', { class: 'hint', style: 'margin-bottom:6px' }, 'Presets:'), presetHost);
    body.insertBefore(presetHost, body.firstChild.nextSibling);

    const foot = el('div', { class: 'row' });
    const cancel = el('button', { class: 'btn' }, 'Cancel');
    const ok = el('button', { class: 'btn btn-danger' }, 'Inject');
    foot.appendChild(cancel); foot.appendChild(ok);
    const m = modal('Inject error', body, foot);
    cancel.addEventListener('click', () => m.close());
    ok.addEventListener('click', async () => {
      m.close();
      await send({
        action: 'inject', http_status: Number(status.value), code: code.value,
        type: type.value, message: msg.value, fault_mode: modeSel.value,
        delay_ms: Number(delay.value), after_chunks: Number(afterChunks.value),
        raw_body: raw.value, raw_mode: rawMode.checked,
      });
    });
  }

  // ---------- renderers ----------
  function renderInspector(s) {
    clear(inspBody);
    const kv = el('dl', { class: 'kv' });
    const rows = [
      ['Session ID', s.session_id], ['Request ID', s.request_id],
      ['Protocol', s.protocol], ['Model', s.model],
      ['Streaming', s.streaming ? 'yes' : 'no'], ['State', s.state],
      ['Mode', s.mode], ['Echo Count', fmtNum(s.echo_count)],
      ['Chunks Sent', fmtNum(s.chunk_count)],
      ['Bytes In', fmtBytes(s.bytes_in)], ['Bytes Out', fmtBytes(s.bytes_out)],
      ['Input Tokens', fmtNum(s.input_tokens)], ['Output Tokens', fmtNum(s.output_tokens)],
      ['Total Tokens', fmtNum((s.input_tokens || 0) + (s.output_tokens || 0))],
      ['Echo Interval', s.echo_interval_ms + ' ms'],
      ['Echo Content Mode', s.echo_content_mode || '—'],
      ['Started At', fmtDateTime(s.created_at)],
      ['Duration', fmtDuration(s.duration_ms)],
      ['Client IP', s.client_ip || '—'],
      ['User Agent', s.user_agent || '—'],
      ['API Key', s.key_fingerprint ? 'sk-****' + String(s.key_fingerprint).slice(-4).toUpperCase() : '—'],
      ['End Reason', s.end_reason || '—'],
    ];
    rows.forEach(([k, v]) => {
      kv.appendChild(el('dt', {}, k));
      kv.appendChild(el('dd', {}, String(v)));
    });
    inspBody.appendChild(kv);
  }

  function renderRate(s) {
    clear(rateBody);
    const line = el('div', { class: 'grid grid-4', style: 'margin-bottom:12px' });
    const mkTile = (l, v) => {
      const d = el('div', { class: 'stat' });
      d.appendChild(el('div', { class: 'stat-label' }, l));
      d.appendChild(el('div', { class: 'stat-value', style: 'font-size:18px' }, v));
      return d;
    };
    line.appendChild(mkTile('Current Rate', fmtRate(s.current_rate) + ' tok/s'));
    line.appendChild(mkTile('Configured', s.rate?.token_rate > 0 ? fmtNum(s.rate.token_rate) + ' tok/s' : 'Unlimited'));
    line.appendChild(mkTile('Global Limit', s.global_limit > 0 ? fmtNum(s.global_limit) + ' tok/s' : 'Unlimited'));
    line.appendChild(mkTile('Effective', fmtNum(s.effective_limit > 0 ? s.effective_limit : 0) || 'Unlimited'));
    const line2 = el('div', { class: 'grid grid-4' });
    line2.appendChild(mkTile('Average Rate', fmtRate(s.average_rate) + ' tok/s'));
    line2.appendChild(mkTile('Peak Rate', fmtRate(s.peak_rate) + ' tok/s'));
    line2.appendChild(mkTile('Tokens Sent', fmtNum(s.output_tokens)));
    line2.appendChild(mkTile('Echoes', fmtNum(s.echo_count)));
    rateBody.appendChild(line);
    rateBody.appendChild(line2);

    const row = el('div', { class: 'row', style: 'margin-top:12px' });
    row.appendChild(el('span', { class: 'dim', style: 'font-size:12px' }, 'Set rate:'));
    const rates = [
      { label: 'Unlimited', v: 0 },
      { label: '1', v: 1 }, { label: '5', v: 5 }, { label: '10', v: 10 },
      { label: '20', v: 20 }, { label: '50', v: 50 }, { label: '100', v: 100 },
      { label: '500', v: 500 }, { label: '1000', v: 1000 },
    ];
    const cur = s.rate?.token_rate || 0;
    rates.forEach(r => {
      const b = el('button', { class: 'btn btn-xs rate-btn' + (cur === r.v ? ' active' : '') }, r.label);
      b.addEventListener('click', () => send({ action: 'rate', rate: r.v }, { quiet: true }));
      row.appendChild(b);
    });
    const custom = el('input', { type: 'number', placeholder: 'custom tok/s', style: 'width:110px' });
    const apply = el('button', { class: 'btn btn-xs' }, 'Apply');
    apply.addEventListener('click', () => send({ action: 'rate', rate: Number(custom.value) }, { quiet: true }));
    row.appendChild(custom); row.appendChild(apply);
    rateBody.appendChild(row);

    const row2 = el('div', { class: 'row', style: 'margin-top:10px' });
    row2.appendChild(el('span', { class: 'dim', style: 'font-size:12px' }, 'Echo interval (ms):'));
    const iv = el('input', { type: 'number', value: String(s.echo_interval_ms ?? 500), style: 'width:100px' });
    const ivBtn = el('button', { class: 'btn btn-xs' }, 'Set');
    ivBtn.addEventListener('click', () => send({ action: 'interval', interval_ms: Number(iv.value) }, { quiet: true }));
    row2.appendChild(iv); row2.appendChild(ivBtn);
    row2.appendChild(el('div', { class: 'control-sep' }));
    row2.appendChild(el('span', { class: 'dim', style: 'font-size:12px' }, 'Echo mode:'));
    const em = el('select', { style: 'width:auto' });
    ['message', 'line', 'exact', 'block'].forEach(m => em.appendChild(el('option', { value: m, selected: (s.echo_content_mode || 'message') === m }, m)));
    em.addEventListener('change', () => send({ action: 'echo_mode', mode: em.value }, { quiet: true }));
    row2.appendChild(em);
    rateBody.appendChild(row2);
  }

  function renderConvo() {
    clear(convo);
    // Render only the last MAX_EVENTS lines so the DOM never grows unbounded.
    const lines = convoLines.slice(-MAX_EVENTS);
    for (const l of lines) {
      const role = el('span', { class: 'convo-role ' + l.kind }, l.label);
      convo.appendChild(el('div', { class: 'convo-line' }, role, l.content));
    }
    convo.scrollTop = convo.scrollHeight;
  }

  async function renderRequest() {
    const s = await api.session(id);
    const host = el('div', { class: 'raw-view' });
    const req = s.request || {};
    host.appendChild(el('div', {}, `${req.method || ''} ${req.path || ''}${req.query ? '?' + req.query : ''}`));
    if (req.headers) {
      host.appendChild(el('div', { style: 'margin-top:8px;color:var(--text-faint)' }, 'Headers:'));
      for (const [k, v] of Object.entries(req.headers)) {
        host.appendChild(el('div', { class: 'raw-line' }, `${k}: ${v}`));
      }
    }
    host.appendChild(el('div', { style: 'margin-top:8px;color:var(--text-faint)' }, 'Body:'));
    host.appendChild(el('div', { class: 'raw-line' }, prettyJSON(req.body)));
    return host;
  }

  function prettyJSON(s) {
    if (!s) return '—';
    try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return String(s); }
  }

  async function renderSSE() {
    const res = await api.sessionRawSSE(id);
    const host = el('div');
    if (!res.frames || !res.frames.length) {
      host.appendChild(el('div', { class: 'empty' }, res.note || 'No SSE frames captured yet.'));
      return host;
    }
    const box = el('div', { class: 'raw-view' });
    box.appendChild(el('div', { class: 'faint', style: 'margin-bottom:6px' },
      `Last ${res.frames.length} frames (ring buffer, newest last)`));
    res.frames.forEach(f => {
      box.appendChild(el('div', { class: 'raw-line' },
        el('span', { class: 'raw-seq' }, '#' + f.seq),
        el('span', { class: 'faint' }, f.at + '  '),
        f.data));
    });
    host.appendChild(box);
    return host;
  }

  async function renderResponse() {
    const evs = await api.sessionEvents(id, 200);
    const host = el('div', { class: 'raw-view' });
    const list = evs.events || [];
    host.appendChild(el('div', { class: 'faint', style: 'margin-bottom:6px' },
      `Last ${list.length} output events (total ${evs.total})`));
    list.forEach(e => {
      host.appendChild(el('div', { class: 'raw-line' },
        el('span', { class: 'raw-seq' }, '#' + e.seq),
        `[${e.kind}] ${e.role} tokens=${e.tokens} bytes=${e.bytes} :: ${String(e.content).slice(0, 200)}`));
    });
    return host;
  }

  async function renderTab() {
    clear(tabHost);
    if (activeTab === 'convo') {
      tabHost.appendChild(convo);
      renderConvo();
      tabHost.appendChild(el('div', { class: 'hint' },
        `Showing the most recent ${MAX_EVENTS} events. The complete log is stored server-side.`));
    } else if (activeTab === 'request') {
      tabHost.appendChild(await renderRequest());
    } else if (activeTab === 'response') {
      tabHost.appendChild(await renderResponse());
    } else if (activeTab === 'sse') {
      tabHost.appendChild(await renderSSE());
    }
  }

  async function loadEvents() {
    try {
      const evs = await api.sessionEvents(id, 500);
      const list = (evs.events || []);
      convoLines = list.map(e => {
        let label = e.role || e.kind;
        let kind = e.kind;
        if (e.kind === 'input') { label = 'USER'; kind = 'user'; }
        else if (e.kind === 'output') { label = 'EPIC-ALPHA'; kind = 'assistant'; }
        else if (e.kind === 'manual') { label = 'ADMIN'; kind = 'manual'; }
        else if (e.kind === 'error') { label = 'ERROR'; kind = 'error'; }
        else { label = 'SYSTEM'; kind = 'system'; }
        return { kind, label, content: e.content };
      });
      if (activeTab === 'convo') renderConvo();
    } catch {}
  }

  async function refresh() {
    try {
      const s = await api.session(id);
      session = s;
      stateChip.textContent = s.state;
      stateChip.className = 'badge ' + badgeTone(s.state);
      renderInspector(s);
      renderRate(s);
    } catch {}
  }

  await refresh();
  await loadEvents();
  await renderTab();

  const timer = setInterval(() => { refresh(); loadEvents(); }, 1500);
  const timer2 = setInterval(() => { if (activeTab === 'sse') renderTab(); }, 3000);
  if (state._cleanup) state._cleanup();
  state._cleanup = () => { clearInterval(timer); clearInterval(timer2); };
}

function badgeTone(s) {
  return {
    ECHOING: 'green', PAUSED: 'yellow', MANUAL: 'purple', CONNECTED: 'blue',
    ERROR_PENDING: 'orange', ENDED: 'gray', CLIENT_DISCONNECTED: 'gray',
  }[s] || 'gray';
}
