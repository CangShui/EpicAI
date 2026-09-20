// Global settings: security, rate limits, resource protection, performance.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtRate, fmtNum } from './dom.js';
import { card, emptyBox } from './views.js';

export async function settingsPage(ctx) {
  ctx.content.appendChild(el('div', { class: 'empty' }, '正在加载配置…'));
  let s;
  try { s = await api.settings(); } catch (e) {
    clear(ctx.content);
    ctx.content.appendChild(emptyBox('加载配置失败: ' + e.message));
    return;
  }
  const rt = s.runtime || {};
  clear(ctx.content);

  const saveBar = el('div', { class: 'toolbar' });
  const saveBtn = el('button', { class: 'btn btn-primary btn-sm' }, '保存全局配置');
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
  const sec = card('安全与鉴权配置');
  sec.body.appendChild(field('API 密钥鉴权模式', sel([
    ['any', '接受任意非空 Bearer Key (开发默认)'],
    ['require', '必须校验托管 API Key'],
    ['allow_empty', '允许无 Key / 空 Key 请求'],
  ], rt.key_mode), 'key_mode'));
  sec.body.appendChild(field('CORS 跨域模式', sel([
    ['disabled', '禁用 CORS'], ['allow_all', '允许所有域名跨域 (Allow All)'], ['allow_list', '仅白名单域名 (Allow List)'],
  ], rt.cors_mode), 'cors_mode'));
  sec.body.appendChild(field('CORS 白名单来源 (逗号分隔)', txt((rt.cors_allow_origins || []).join(',')), 'cors_allow_origins',
    '仅在 CORS 模式选择白名单时生效。'));
  ctx.content.appendChild(sec.root);

  // ---------- Rate limiting ----------
  const rate = card('输出速率与吞吐限制 (Token Rate Limiting)');
  const rg = el('div', { class: 'grid grid-3' });
  rg.appendChild(field('全局输出速率 (token/s，0=不限速)', num(rt.global_token_rate), 'global_token_rate'));
  rg.appendChild(field('全局突发缓冲窗口 (秒)', num(rt.global_burst_seconds), 'global_burst_seconds'));
  rg.appendChild(field('单会话默认速率 (token/s，0=不限速)', num(rt.session_token_rate), 'session_token_rate'));
  rg.appendChild(field('单会话突发缓冲窗口 (秒)', num(rt.session_burst_seconds), 'session_burst_seconds'));
  rg.appendChild(field('速率模式', sel([['smooth', '平滑输出 (Smooth)'], ['burst', '突发优先 (Burst)'], ['unlimited', '极致速度 (Unlimited)']], rt.rate_mode), 'rate_mode'));
  rg.appendChild(field('每个 Chunk 的 Token 大小 (0=自适应)', num(rt.chunk_size_tokens), 'chunk_size_tokens'));
  rate.body.appendChild(rg);
  rate.body.appendChild(field('人工接管消息绕过限速', chk(rt.bypass_manual_rate), 'bypass_manual_rate',
    '开启后，管理员发送的人工接管消息将立即送达客户端，不等待速率配额。'));
  rate.body.appendChild(field('模拟人工打字速度 (字符/秒，0=整段直接发送)', num(rt.manual_chars_per_second), 'manual_chars_per_second'));
  ctx.content.appendChild(rate.root);

  // ---------- Concurrency ----------
  const conc = card('并发控制与资源保护 (Resource Protection)');
  const cg = el('div', { class: 'grid grid-3' });
  cg.appendChild(field('最大活跃会话数 (并发连接上限)', num(rt.max_active_sessions), 'max_active_sessions'));
  cg.appendChild(field('单 IP 最大会话数 (0=不限)', num(rt.max_sessions_per_ip), 'max_sessions_per_ip'));
  cg.appendChild(field('单 API Key 最大会话数 (0=不限)', num(rt.max_sessions_per_key), 'max_sessions_per_key'));
  cg.appendChild(field('单个文件最大大小 (字节，Bytes)', num(rt.max_file_size), 'max_file_size'));
  cg.appendChild(field('资产存储容量上限 (字节，Bytes)', num(rt.max_assets_size), 'max_assets_size'));
  cg.appendChild(field('单会话最大事件记录数 (防内存泄漏)', num(rt.max_events_per_session), 'max_events_per_session'));
  cg.appendChild(field('日志自动保留天数 (天)', num(rt.retention_days), 'retention_days'));
  conc.body.appendChild(cg);
  conc.body.appendChild(field('并发过载处理行为', sel([
    ['reject', '拒绝新请求 (返回 HTTP 429)'], ['queue', '排队等待空闲槽位'],
    ['hang', '挂起不响应 (Hang)'], ['custom_error', '返回自定义错误'],
  ], rt.overload_behavior), 'overload_behavior'));
  conc.body.appendChild(field('超出单会话事件限额时的动作', sel([
    ['disconnect', '断开连接 (Disconnect)'], ['error', '返回 429 报错'], ['pause', '暂停输出 (Pause)'],
  ], rt.resource_action), 'resource_action'));
  ctx.content.appendChild(conc.root);

  // ---------- Echo defaults ----------
  const echo = card('回显行为与协议适配');
  const eg = el('div', { class: 'grid grid-3' });
  eg.appendChild(field('默认回显间隔 (毫秒 ms)', num(rt.default_echo_interval_ms), 'default_echo_interval_ms'));
  eg.appendChild(field('回显内容模式', sel([
    ['message', '整条消息 (message)'], ['line', '按行 (line)'], ['exact', '原样紧凑 (exact)'], ['block', '分块 (block)'],
  ], rt.echo_content_mode), 'echo_content_mode'));
  eg.appendChild(field('图片回显模式', sel([
    ['strict', '严格 OpenAI 兼容 (返回图片资产 URL)'], ['relaxed', '宽松多模态镜像 (Relaxed Multimodal Mirror)'],
  ], rt.image_echo_mode), 'image_echo_mode'));
  echo.body.appendChild(eg);
  echo.body.appendChild(el('div', { class: 'notice' },
    '“宽松多模态镜像”模式会按输入结构原样回显，可能包含非官方拓展字段，仅用于测试特定第三方客户端。'));
  echo.body.appendChild(field('未知模型处理方式', sel([
    ['error', '直接返回 HTTP 404 (model_not_found)'], ['fallback', '自动降级回退到默认模型'],
  ], rt.unknown_model_behavior), 'unknown_model_behavior'));
  echo.body.appendChild(field('回显包含文件引用标识 (file_id)', chk(rt.repeat_file_reference), 'repeat_file_reference'));
  echo.body.appendChild(field('回显包含文件下载链接 (download_url)', chk(rt.repeat_download_url), 'repeat_download_url'));
  echo.body.appendChild(field('回显包含文件元数据 (metadata)', chk(rt.repeat_metadata), 'repeat_metadata'));
  const holdWrap = el('label', { class: 'field' });
  holdWrap.appendChild(el('span', {}, '【非标准 / 混沌模式】保持非流式 HTTP 连接永久挂起 (Hold Connection Forever)'));
  holdWrap.appendChild(chk(rt.hold_connection_forever));
  holdWrap.dataset.chaos = '1';
  holdWrap.addEventListener('change', () => { pending['hold_connection_forever'] = holdWrap.querySelector('input').checked; });
  echo.body.appendChild(holdWrap);
  echo.body.appendChild(el('div', { class: 'notice danger' },
    '警告：永久挂起模式下，非流式请求故意永不返回，专门用于测试客户端的超时 (Timeout) 和主动中止 (Abort) 机制，切勿在常规测试中开启！'));
  ctx.content.appendChild(echo.root);

  // ---------- Performance ----------
  const perf = card('硬件性能基准实测 (Uncapped Benchmark)');
  const pBody = el('div');
  perf.body.appendChild(pBody);
  ctx.content.appendChild(perf.root);

  async function renderPerf() {
    clear(pBody);
    let bs;
    try { bs = await api.benchmarks(); } catch (e) {
      pBody.appendChild(emptyBox('加载基准数据失败: ' + e.message)); return;
    }
    const measured = bs.measured_maximum || {};
    const benchList = bs.benchmarks || [];

    const info = el('div', { class: 'notice info' },
      'Token 统计基于 EpicAI 统一 Tokenizer。在不限速 (Unlimited) 模式下，实际吞吐取决于 CPU、JSON 序列化、SSE 分帧、网络带宽及客户端读取速度。');
    pBody.appendChild(info);

    const grid = el('div', { class: 'grid grid-3', style: 'margin-bottom:14px' });
    Object.entries(measured).forEach(([proto, val]) => {
      const t = el('div', { class: 'stat' });
      t.appendChild(el('div', { class: 'stat-label' }, '实测极限吞吐 · ' + proto));
      t.appendChild(el('div', { class: 'stat-value', style: 'font-size:19px' }, fmtRate(Number(val)) + ' tok/s'));
      t.appendChild(el('div', { class: 'stat-sub' }, '建议安全峰值 (P50 × 90%): ' + fmtRate(Number(val) * 0.9) + ' tok/s'));
      grid.appendChild(t);
    });
    if (!Object.keys(measured).length) {
      grid.appendChild(el('div', { class: 'stat' },
        el('div', { class: 'stat-label' }, '实测最高速率'),
        el('div', { class: 'stat-value', style: 'font-size:16px' }, '尚未执行测试'),
        el('div', { class: 'stat-sub' }, '点击下方按钮执行本机极限吞吐测算')));
    }
    pBody.appendChild(grid);

    const row = el('div', { class: 'row' });
    row.appendChild(el('span', { class: 'dim', style: 'font-size:12.5px' }, '执行不限速硬件基准测算:'));
    const protoSel = sel([['chat.completions', 'Chat Completions'], ['responses', 'Responses']], 'chat.completions');
    protoSel.style.width = 'auto';
    const durSel = sel([['1000', '持续 1 秒'], ['3000', '持续 3 秒'], ['5000', '持续 5 秒'], ['10000', '持续 10 秒']], '3000');
    durSel.style.width = 'auto';
    const chunkSel = sel([['1', '1 个 token/块'], ['8', '8 个 token/块'], ['32', '32 个 token/块'], ['128', '128 个 token/块'], ['512', '512 个 token/块']], '32');
    chunkSel.style.width = 'auto';
    const run = el('button', { class: 'btn btn-primary btn-sm' }, '开始运行基准测试');
    row.appendChild(protoSel); row.appendChild(durSel); row.appendChild(chunkSel); row.appendChild(run);
    pBody.appendChild(row);

    const prog = el('div', { class: 'hint', style: 'margin-top:6px' }, '');
    pBody.appendChild(prog);

    run.addEventListener('click', async () => {
      run.disabled = true;
      prog.textContent = '正在执行全速测算… (调用真实 Echo 引擎、分词器、协议适配器及 SSE 分帧)';
      try {
        const res = await api.runBenchmark({
          protocol: protoSel.value, duration_ms: Number(durSel.value), chunk_size: Number(chunkSel.value),
        });
        prog.textContent = `测算完毕：平均速率 ${fmtRate(res.average_token_rate)} token/s，峰值速率 ${fmtRate(res.peak_token_rate)} token/s，在 ${res.duration_ms}ms 内生成 ${fmtNum(res.tokens_sent)} 个 Token。`;
        renderPerf();
      } catch (e) {
        prog.textContent = '基准测试失败: ' + e.message;
      }
      run.disabled = false;
    });

    if (benchList.length) {
      const c2 = card('历史基准测试记录', { tight: true });
      const wrap = el('div', { class: 'table-wrap' });
      const table = el('table');
      const hr = el('tr');
      ['测试时间', '接口协议', '分块大小', '测试耗时', '发送 Token 数', '平均速率', '峰值速率', '传输字节数'].forEach(h => hr.appendChild(el('th', {}, h)));
      table.appendChild(el('thead', {}, hr));
      const tb = el('tbody');
      benchList.forEach(b => {
        const tr = el('tr');
        tr.appendChild(el('td', { class: 'nowrap dim' }, new Date(b.timestamp).toLocaleString('zh-CN')));
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
  const env = card('环境与静态参数');
  const kv = el('dl', { class: 'kv' });
  const st = s.static || {};
  [['监听地址 (Host)', st.host], ['监听端口 (Port)', st.port], ['数据库连接', st.database_url],
   ['文件存储路径', st.storage_path], ['默认启动模型', st.default_model],
   ['系统日志级别', st.log_level], ['初始管理员账号', st.admin_user]].forEach(([k, v]) => {
    kv.appendChild(el('dt', {}, k));
    kv.appendChild(el('dd', {}, String(v ?? '—')));
  });
  env.body.appendChild(kv);
  ctx.content.appendChild(env.root);

  saveBtn.addEventListener('click', async () => {
    if (!Object.keys(pending).length) { status.textContent = '暂无修改'; return; }
    try {
      await api.updateSettings(pending);
      status.textContent = '设置已于 ' + new Date().toLocaleTimeString('zh-CN') + ' 成功保存并生效';
    } catch (e) {
      status.textContent = '保存失败: ' + e.message;
    }
  });

  if (state._cleanup) state._cleanup();
  state._cleanup = () => {};
}
