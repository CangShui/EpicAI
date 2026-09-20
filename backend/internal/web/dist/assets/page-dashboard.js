// Dashboard: cluster overview + throughput panel.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtBytes, fmtNum, fmtRate, fmtDuration } from './dom.js';
import { card, statTile } from './views.js';

export async function dashboard(ctx) {
  const grid = el('div', { class: 'grid grid-4' });
  const throughput = el('div', { class: 'grid grid-4' });
  const quick = el('div', { class: 'card' });
  quick.appendChild(el('div', { class: 'card-head' }, el('h2', {}, '快速测试指引')));
  const qb = el('div', { class: 'card-body' });
  qb.innerHTML = `
    <div class="row" style="gap:12px;align-items:flex-start">
      <div style="flex:1;min-width:260px">
        <div class="hint" style="margin-bottom:6px">发起 Chat Completions 无限 Echo 流式会话：</div>
        <pre class="raw-view" style="margin:0;font-size:11.5px">curl -N http://${location.host}/v1/chat/completions \\
  -H "Authorization: Bearer test" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"epic-alpha","messages":[{"role":"user","content":"Hello"}],"stream":true}'</pre>
      </div>
      <div style="flex:1;min-width:260px">
        <div class="hint" style="margin-bottom:6px">发起 Responses API 流式会话：</div>
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
      grid.appendChild(statTile('当前活跃会话', `${fmtNum(s.active_sessions)} / ${fmtNum(s.max_active_sessions)}`, '当前 / 并发上限', 'accent'));
      grid.appendChild(statTile('累计请求总数', fmtNum(s.total_requests), '自启动以来'));
      grid.appendChild(statTile('流式连接数', fmtNum(s.streaming_connections), `${fmtNum(s.manual_sessions)} 人工接管 · ${fmtNum(s.paused_sessions)} 暂停中`, 'green'));
      grid.appendChild(statTile('故障注入次数', fmtNum(s.errors_injected), '累计触发错误', 'red'));
      grid.appendChild(statTile('已存文件数', fmtNum(s.uploaded_assets), '多模态与文件资产', 'purple'));
      grid.appendChild(statTile('存储占用', fmtBytes(s.storage_usage), '资产存储空间'));
      grid.appendChild(statTile('系统运行时间', fmtDuration(s.uptime_seconds * 1000), '服务持续运行时长'));
      const badge = document.querySelector('[data-live]');
      if (badge) badge.textContent = String(s.active_sessions);

      const t = s.throughput || {};
      clear(throughput);
      throughput.appendChild(statTile('当前输出吞吐', fmtRate(t.current_token_rate) + ' token/s', '实时 Token 速率', 'accent'));
      throughput.appendChild(statTile('1 分钟平均', fmtRate(t.avg_1m_token_rate) + ' token/s', '滑动平均速率'));
      throughput.appendChild(statTile('观测峰值', fmtRate(t.peak_token_rate) + ' token/s', '历史最高速率', 'purple'));
      const limit = t.global_limit > 0 ? fmtRate(t.global_limit) + ' token/s' : '不限速 (Unlimited)';
      throughput.appendChild(statTile('全局限速阈值', limit, (t.utilization || 0).toFixed(1) + '% 负载利用率', t.global_limit > 0 ? 'yellow' : 'green'));
    } catch (e) {
      clear(grid);
      grid.appendChild(el('div', { class: 'empty' }, '加载统计信息失败: ' + e.message));
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
