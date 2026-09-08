// Global settings: security, rate limits, resource protection, performance.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtRate, fmtNum } from './dom.js';
import { card, emptyBox } from './views.js';

export async function settingsPage(ctx) {
  ctx.content.appendChild(el('div', { class: 'empty' }, 'Loading settings…'));
  let s;
  try { s = await api.settings(); } catch (e) {
    clear(ctx.content);
    ctx.content.appendChild(emptyBox('Failed to load settings: ' + e.message));
    return;
  }
  const rt = s.runtime || {};
  clear(ctx.content);

  const saveBar = el('div', { class: 'toolbar' });
  const saveBtn = el('button', { class: 'btn btn-primary btn-sm' }, 'Save settings');
  const status = el('span', { class: 'dim', style: 'font-size:12.5px' }, '');
  saveBar.appendChild(saveBtn);
  saveBar.appendChild(status);
  ctx.content.appendChild(saveBar);

  const pending = {};

  function field(label, input, key, hint) {
    const f = el('label', { class: 'field' });
    f.appendChild(el('span', {}, label));
    f.appendChild(input);
    if (hint) f.appendChild(el('div', { class: 'hint' }, hint));
    input.addEventListener('change', () => { pending[key] = input.type === 'checkbox' ? input.checked : coerce(input); });
    return f;
  }
  function coerce(input) {
    if (input.type === 'number') return Number(input.value);
    return input.value;
  }
  const num = (v, w = '100%') => el('input', { type: 'number', value: String(v ?? 0), style: `width:${w}` });
  const txt = (v) => el('input', { type: 'text', value: String(v ?? '') });
  const chk = (v) => el('input', { type: 'checkbox', checked: !!v });
  const sel = (opts, v) => {
    const s2 = el('select');
    opts.forEach(([val, lab]) => s2.appendChild(el('option', { value: val, selected: String(v) === String(val) }, lab)));
    return s2;
  };

  // ---------- Security ----------
  const sec = card('Security & Access');
  sec.body.appendChild(field('API key mode', sel([
    ['any', 'Accept Any Non-empty Bearer Key'],
    ['require', 'Require Managed Key'],
    ['allow_empty', 'Allow Empty Key'],
  ], rt.key_mode), 'key_mode'));
  sec.body.appendChild(field('CORS mode', sel([
    ['disabled', 'Disabled'], ['allow_all', 'Allow All'], ['allow_list', 'Allow List'],
  ], rt.cors_mode), 'cors_mode'));
  sec.body.appendChild(field('CORS allow list (comma separated origins)', txt((rt.cors_allow_origins || []).join(',')), 'cors_allow_origins',
    'Only used when mode is Allow List.'));
  ctx.content.appendChild(sec.root);

  // ---------- Rate limiting ----------
  const rate = card('Output Rate Limiting');
  const rg = el('div', { class: 'grid grid-3' });
  rg.appendChild(field('Global token rate (0 = Unlimited)', num(rt.global_token_rate), 'global_token_rate'));
  rg.appendChild(field('Global burst (seconds)', num(rt.global_burst_seconds), 'global_burst_seconds'));
  rg.appendChild(field('Default session token rate (0 = Unlimited)', num(rt.session_token_rate), 'session_token_rate'));
  rg.appendChild(field('Session burst (seconds)', num(rt.session_burst_seconds), 'session_burst_seconds'));
  rg.appendChild(field('Rate mode', sel([['smooth', 'Smooth'], ['burst', 'Burst'], ['unlimited', 'Unlimited']], rt.rate_mode), 'rate_mode'));
  rg.appendChild(field('Chunk size in tokens (0 = auto)', num(rt.chunk_size_tokens), 'chunk_size_tokens'));
  rate.body.appendChild(rg);
  rate.body.appendChild(field('Bypass rate limit for manual messages', chk(rt.bypass_manual_rate), 'bypass_manual_rate',
    'When on, administrator messages are delivered immediately.'));
  rate.body.appendChild(field('Manual typing speed (chars/sec, 0 = send at once)', num(rt.manual_chars_per_second), 'manual_chars_per_second'));
  ctx.content.appendChild(rate.root);

  // ---------- Concurrency ----------
  const conc = card('Concurrency & Resource Protection');
  const cg = el('div', { class: 'grid grid-3' });
  cg.appendChild(field('Max active sessions', num(rt.max_active_sessions), 'max_active_sessions'));
  cg.appendChild(field('Max sessions per IP (0 = off)', num(rt.max_sessions_per_ip), 'max_sessions_per_ip'));
  cg.appendChild(field('Max sessions per API key (0 = off)', num(rt.max_sessions_per_key), 'max_sessions_per_key'));
  cg.appendChild(field('Max file size (bytes)', num(rt.max_file_size), 'max_file_size'));
  cg.appendChild(field('Max asset storage (bytes)', num(rt.max_assets_size), 'max_assets_size'));
  cg.appendChild(field('Max events per session', num(rt.max_events_per_session), 'max_events_per_session'));
  cg.appendChild(field('Log retention (days)', num(rt.retention_days), 'retention_days'));
  conc.body.appendChild(cg);
  conc.body.appendChild(field('Overload behavior', sel([
    ['reject', 'Reject (HTTP 429)'], ['queue', 'Queue until a slot frees'],
    ['hang', 'Hang (no response)'], ['custom_error', 'Custom error'],
  ], rt.overload_behavior), 'overload_behavior'));
  conc.body.appendChild(field('Resource limit action', sel([
    ['disconnect', 'Disconnect'], ['error', 'Error'], ['pause', 'Pause'],
  ], rt.resource_action), 'resource_action'));
  ctx.content.appendChild(conc.root);

  // ---------- Echo defaults ----------
  const echo = card('Echo & Protocol Behaviour');
  const eg = el('div', { class: 'grid grid-3' });
  eg.appendChild(field('Default echo interval (ms)', num(rt.default_echo_interval_ms), 'default_echo_interval_ms'));
  eg.appendChild(field('Echo content mode', sel([
    ['message', 'message'], ['line', 'line'], ['exact', 'exact'], ['block', 'block'],
  ], rt.echo_content_mode), 'echo_content_mode'));
  eg.appendChild(field('Image echo mode', sel([
    ['strict', 'Strict OpenAI (asset URL)'], ['relaxed', 'Relaxed Multimodal Mirror'],
  ], rt.image_echo_mode), 'image_echo_mode'));
  echo.body.appendChild(eg);
  echo.body.appendChild(el('div', { class: 'notice' },
    'Relaxed Multimodal Mirror may emit non-official extensions. Use only when testing third-party clients.'));
  echo.body.appendChild(field('Unknown model behavior', sel([
    ['error', 'Return 404'], ['fallback', 'Fall back to default model'],
  ], rt.unknown_model_behavior), 'unknown_model_behavior'));
  echo.body.appendChild(field('Repeat file reference on echo', chk(rt.repeat_file_reference), 'repeat_file_reference'));
  echo.body.appendChild(field('Repeat download URL on echo', chk(rt.repeat_download_url), 'repeat_download_url'));
  echo.body.appendChild(field('Repeat metadata on echo', chk(rt.repeat_metadata), 'repeat_metadata'));
  const holdWrap = el('label', { class: 'field' });
  holdWrap.appendChild(el('span', {}, 'NON-STANDARD / CHAOS MODE — Hold connection forever (non-stream)'));
  holdWrap.appendChild(chk(rt.hold_connection_forever));
  holdWrap.dataset.chaos = '1';
  holdWrap.addEventListener('change', () => { pending['hold_connection_forever'] = holdWrap.querySelector('input').checked; });
  echo.body.appendChild(holdWrap);
  echo.body.appendChild(el('div', { class: 'notice danger' },
    'Hold Connection Forever intentionally never completes non-streaming HTTP requests. This is NOT standard OpenAI behavior — use only for timeout/abort testing.'));
  ctx.content.appendChild(echo.root);

  // ---------- Performance ----------
  const perf = card('Performance');
  const pBody = el('div');
  perf.body.appendChild(pBody);
  ctx.content.appendChild(perf.root);

  async function renderPerf() {
    clear(pBody);
    let bs;
    try { bs = await api.benchmarks(); } catch (e) {
      pBody.appendChild(emptyBox('Failed: ' + e.message)); return;
    }
    const measured = bs.measured_maximum || {};
    const benchList = bs.benchmarks || [];

    const info = el('div', { class: 'notice info' },
      'Token accounting is based on EpicAI Canonical Tokenizer. Unlimited ≠ a fixed token/s: actual throughput depends on CPU, serialization, SSE framing, network and client read speed.');
    pBody.appendChild(info);

    const grid = el('div', { class: 'grid grid-3', style: 'margin-bottom:14px' });
    Object.entries(measured).forEach(([proto, val]) => {
      const t = el('div', { class: 'stat' });
      t.appendChild(el('div', { class: 'stat-label' }, 'Measured max · ' + proto));
      t.appendChild(el('div', { class: 'stat-value', style: 'font-size:19px' }, fmtRate(Number(val)) + ' tok/s'));
      t.appendChild(el('div', { class: 'stat-sub' }, 'Safe max (P50 × 90%): ' + fmtRate(Number(val) * 0.9)));
      grid.appendChild(t);
    });
    if (!Object.keys(measured).length) {
      grid.appendChild(el('div', { class: 'stat' },
        el('div', { class: 'stat-label' }, 'Measured maximum'),
        el('div', { class: 'stat-value', style: 'font-size:16px' }, 'Not benchmarked'),
        el('div', { class: 'stat-sub' }, 'Maximum has not been benchmarked.')));
    }
    pBody.appendChild(grid);

    const row = el('div', { class: 'row' });
    row.appendChild(el('span', { class: 'dim', style: 'font-size:12.5px' }, 'Run uncapped benchmark:'));
    const protoSel = sel([['chat.completions', 'Chat Completions'], ['responses', 'Responses']], 'chat.completions');
    protoSel.style.width = 'auto';
    const durSel = sel([['1000', '1s'], ['3000', '3s'], ['5000', '5s'], ['10000', '10s']], '3000');
    durSel.style.width = 'auto';
    const chunkSel = sel([['1', 'chunk 1'], ['8', 'chunk 8'], ['32', 'chunk 32'], ['128', 'chunk 128'], ['512', 'chunk 512']], '32');
    chunkSel.style.width = 'auto';
    const run = el('button', { class: 'btn btn-primary btn-sm' }, 'Run Uncapped Benchmark');
    row.appendChild(protoSel); row.appendChild(durSel); row.appendChild(chunkSel); row.appendChild(run);
    pBody.appendChild(row);

    const prog = el('div', { class: 'hint', style: 'margin-top:6px' }, '');
    pBody.appendChild(prog);

    run.addEventListener('click', async () => {
      run.disabled = true;
      prog.textContent = 'Measuring… (real echo engine, tokenizer, adapter and SSE framing)';
      try {
        const res = await api.runBenchmark({
          protocol: protoSel.value, duration_ms: Number(durSel.value), chunk_size: Number(chunkSel.value),
        });
        prog.textContent = `Result: average ${fmtRate(res.average_token_rate)} token/s, peak ${fmtRate(res.peak_token_rate)} token/s, ${fmtNum(res.tokens_sent)} tokens in ${res.duration_ms}ms`;
        renderPerf();
      } catch (e) {
        prog.textContent = 'Benchmark failed: ' + e.message;
      }
      run.disabled = false;
    });

    if (benchList.length) {
      const c2 = card('Benchmark history', { tight: true });
      const wrap = el('div', { class: 'table-wrap' });
      const table = el('table');
      const hr = el('tr');
      ['Time', 'Protocol', 'Chunk', 'Duration', 'Tokens', 'Average', 'Peak', 'Bytes'].forEach(h => hr.appendChild(el('th', {}, h)));
      table.appendChild(el('thead', {}, hr));
      const tb = el('tbody');
      benchList.forEach(b => {
        const tr = el('tr');
        tr.appendChild(el('td', { class: 'nowrap dim' }, new Date(b.timestamp).toLocaleString('en-GB')));
        tr.appendChild(el('td', {}, b.protocol));
        tr.appendChild(el('td', { class: 'num mono' }, b.chunk_size));
        tr.appendChild(el('td', { class: 'num mono' }, b.duration_ms + 'ms'));
        tr.appendChild(el('td', { class: 'num mono' }, fmtNum(b.tokens_sent)));
        tr.appendChild(el('td', { class: 'num mono' }, fmtRate(b.average_token_rate) + '/s'));
        tr.appendChild(el('td', { class: 'num mono' }, fmtRate(b.peak_token_rate) + '/s'));
        tr.appendChild(el('td', { class: 'num mono' }, fmtNum(b.bytes_sent)));
        tb.appendChild(tr);
      });
      table.appendChild(tb);
      wrap.appendChild(table);
      c2.body.appendChild(wrap);
      pBody.appendChild(c2.root);
    }
  }
  await renderPerf();

  // ---------- environment ----------
  const env = card('Environment');
  const kv = el('dl', { class: 'kv' });
  const st = s.static || {};
  [['Host', st.host], ['Port', st.port], ['Database', st.database_url],
   ['Storage path', st.storage_path], ['Default model', st.default_model],
   ['Log level', st.log_level], ['Admin user', st.admin_user]].forEach(([k, v]) => {
    kv.appendChild(el('dt', {}, k));
    kv.appendChild(el('dd', {}, String(v ?? '—')));
  });
  env.body.appendChild(kv);
  ctx.content.appendChild(env.root);

  saveBtn.addEventListener('click', async () => {
    if (!Object.keys(pending).length) { status.textContent = 'No changes'; return; }
    try {
      await api.updateSettings(pending);
      status.textContent = 'Saved at ' + new Date().toLocaleTimeString('en-GB');
    } catch (e) {
      status.textContent = 'Failed: ' + e.message;
    }
  });

  if (state._cleanup) state._cleanup();
  state._cleanup = () => {};
}
