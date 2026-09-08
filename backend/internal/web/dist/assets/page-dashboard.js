// Dashboard: cluster overview + throughput panel.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration } from './dom.js';
import { card, statTile } from './views.js';

export async function dashboard(ctx) {
  const grid = el('div', { class: 'grid grid-4' });
  const throughput = el('div', { class: 'grid grid-4' });
  const quick = el('div', { class: 'card' });
  quick.appendChild(el('div', { class: 'card-head' }, el('h2', {}, 'Quick start')));
  const qb = el('div', { class: 'card-body' });
  qb.innerHTML = `
    <div class="row" style="gap:12px;align-items:flex-start">
      <div style="flex:1;min-width:260px">
        <div class="hint" style="margin-bottom:6px">Start an infinite echo session:</div>
        <pre class="raw-view" style="margin:0;font-size:11.5px">curl -N http://${location.host}/v1/chat/completions \\
  -H "Authorization: Bearer test" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"epic-alpha","messages":[{"role":"user","content":"Hello"}],"stream":true}'</pre>
      </div>
      <div style="flex:1;min-width:260px">
        <div class="hint" style="margin-bottom:6px">Responses API streaming:</div>
        <pre class="raw-view" style="margin:0;font-size:11.5px">curl -N http://${location.host}/v1/responses \\
  -H "Authorization: Bearer test" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"epic-alpha","input":"Responses Test","stream":true}'</pre>
      </div>
    </div>`;
  quick.appendChild(qb);

  ctx.content.appendChild(grid);
  ctx.content.appendChild(throughput);
  ctx.content.appendChild(quick);

  async function load() {
    try {
      const s = await api.stats();
      state.stats = s;
      clear(grid);
      grid.appendChild(statTile('Active Sessions', `${fmtNum(s.active_sessions)} / ${fmtNum(s.max_active_sessions)}`, 'concurrent limit', 'accent'));
      grid.appendChild(statTile('Total Requests', fmtNum(s.total_requests), 'since startup'));
      grid.appendChild(statTile('Streaming', fmtNum(s.streaming_connections), `${fmtNum(s.manual_sessions)} manual · ${fmtNum(s.paused_sessions)} paused`, 'green'));
      grid.appendChild(statTile('Errors Injected', fmtNum(s.errors_injected), 'fault injection count', 'red'));
      grid.appendChild(statTile('Uploaded Assets', fmtNum(s.uploaded_assets), 'files stored', 'purple'));
      grid.appendChild(statTile('Storage Usage', fmtBytes(s.storage_usage), 'asset store'));
      grid.appendChild(statTile('Uptime', fmtDuration(s.uptime_seconds * 1000), 'server uptime'));
      const badge = document.querySelector('[data-live]');
      if (badge) badge.textContent = String(s.active_sessions);

      const t = s.throughput || {};
      clear(throughput);
      throughput.appendChild(statTile('Current Throughput', fmtRate(t.current_token_rate) + '/s', 'output token/s', 'accent'));
      throughput.appendChild(statTile('1 min Average', fmtRate(t.avg_1m_token_rate) + '/s', 'rolling window'));
      throughput.appendChild(statTile('Peak', fmtRate(t.peak_token_rate) + '/s', 'observed maximum', 'purple'));
      const limit = t.global_limit > 0 ? fmtRate(t.global_limit) + '/s' : 'Unlimited';
      throughput.appendChild(statTile('Global Limit', limit, (t.utilization || 0).toFixed(1) + '% utilization', t.global_limit > 0 ? 'yellow' : 'green'));
    } catch (e) {
      clear(grid);
      grid.appendChild(el('div', { class: 'empty' }, 'Failed to load stats: ' + e.message));
    }
  }

  await load();
  const timer = setInterval(load, 2000);
  state.onEvent = () => {};
  registerCleanup(timer);
}

// clearInterval when navigating away
function registerCleanup(timer) {
  const prev = state._cleanup;
  if (prev) prev();
  state._cleanup = () => clearInterval(timer);
}
