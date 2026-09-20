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
  head.appendChild(el('a', { class: 'btn btn-sm', href: '#/admin/sessions' }, '← 返回会话列表'));
  head.appendChild(el('span', { class: 'mono', style: 'font-size:14px' }, id));
  const stateChip = el('span', { class: 'badge' }, session.state);
  head.appendChild(stateChip);
  head.appendChild(el('span', { class: 'dim mono' }, session.model));
  head.appendChild(el('span', { class: 'badge ' + (session.protocol === 'responses' ? 'purple' : 'blue') }, session.protocol));
  if (!session.streaming) head.appendChild(el('span', { class: 'badge gray' }, '非流式'));
  ctx.content.appendChild(head);

  // ---------- control bar ----------
  const bar = el('div', { class: 'control-bar' });
  const mk = (label, cls, action, extra = null) => {
    const b = el('button', { class: 'btn btn-sm ' + (cls || '') }, label);
    b.addEventListener('click', () => { if (extra) extra(); else send(action, {}); });
    return b;
  };
  bar.appendChild(mk('暂停输出 (PAUSE)', '', { action: 'pause' }));
  bar.appendChild(mk('恢复输出 (RESUME)', '', { action: 'resume' }));
  bar.appendChild(el('div', { class: 'control-sep' }));
  bar.appendChild(mk('人工接管 (TAKE OVER)', 'btn-warn', { action: 'takeover' }));
  bar.appendChild(mk('切回回显 (RETURN TO ECHO)', '', { action: 'return' }));
  const sendBtn = el('button', { class: 'btn btn-sm btn-primary' }, '发送消息 (SEND)');
  sendBtn.addEventListener('click', openManualSend);
  bar.appendChild(sendBtn);
  bar.appendChild(el('div', { class: 'control-sep' }));
  const finishBtn = el('button', { class: 'btn btn-sm' }, '正常结束 (FINISH)');
  finishBtn.addEventListener('click', () => confirmModal('正常结束会话', '向客户端返回合法的 finish_reason 及 [DONE] 并优雅断开？', () => send({ action: 'finish' }, {}), false));
  bar.appendChild(finishBtn);
  const injectBtn = el('button', { class: 'btn btn-sm btn-warn' }, '故障注入 (INJECT ERROR)');
  injectBtn.addEventListener('click', openInject);
  bar.appendChild(injectBtn);
  const dropBtn = el('button', { class: 'btn btn-sm btn-danger' }, '强制断开连接 (DROP)');
  dropBtn.addEventListener('click', () => confirmModal('强制断开连接', '立即掐断底层 TCP Socket，不发送任何结束符（模拟网络突发异常）？', () => send({ action: 'drop' }, {}), true));
  bar.appendChild(dropBtn);
  bar.appendChild(el('div', { class: 'control-sep' }));
  const delBtn = el('button', { class: 'btn btn-sm btn-danger' }, '删除会话');
  delBtn.addEventListener('click', () => confirmModal('删除会话', '永久删除该会话及其全部日志明细？', async () => {
    await api.deleteSession(id);
    location.hash = '#/admin/sessions';
  }, true));
  bar.appendChild(delBtn);
  ctx.content.appendChild(bar);

  // ---------- inspector ----------
  const inspCard = card('会话检查器 (Session Inspector)', { tight: true });
  const inspBody = el('div', { style: 'padding:14px 16px' });
  inspCard.body.appendChild(inspBody);
  ctx.content.appendChild(inspCard.root);

  // ---------- rate control ----------
  const rateCard = card('会话动态调速 (Rate Control)', { tight: true });
  const rateBody = el('div', { style: 'padding:14px 16px' });
  rateCard.body.appendChild(rateBody);
  ctx.content.appendChild(rateCard.root);

  // ---------- tabs ----------
  const tabsRow = el('div', { class: 'tabs' });
  const tabDefs = [
    { key: 'convo', label: '对话实时视图 (Conversation)' },
    { key: 'request', label: '原始请求 (Raw Request)' },
    { key: 'response', label: '原始响应 (Raw Response)' },
    { key: 'sse', label: 'SSE 原始数据流 (Raw SSE)' },
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
    const ta = el('textarea', { placeholder: '输入人工回复内容…', style: 'min-height:110px' });
    const modeSel = el('select');
    modeSel.appendChild(el('option', { value: 'full' }, '整段一次性发送 (完整文本)'));
    modeSel.appendChild(el('option', { value: 'typing' }, '模拟打字输出 (按字符速率流式输出)'));
    const body = el('div');
    body.appendChild(el('label', { class: 'field' }, el('span', {}, '人工发送内容'), ta));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, '发送传输模式'), modeSel));
    const foot = el('div', { class: 'row' });
    const cancel = el('button', { class: 'btn' }, '取消');
    const ok = el('button', { class: 'btn btn-primary' }, '推送给客户端');
    foot.appendChild(cancel); foot.appendChild(ok);
    const m = modal('人工接管消息推送', body, foot);
    cancel.addEventListener('click', () => m.close());
    ok.addEventListener('click', async () => {
      const text = ta.value;
      if (!text.trim()) return;
      if (modeSel.value === 'typing') {
        const cps = Number(prompt('请输入打字速率（字符/秒）：', '10') || 10);
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
    modeSel.appendChild(el('option', { value: 'sse_error' }, '模式 B: 在 SSE 中推送 Error Event (默认)'));
    modeSel.appendChild(el('option', { value: 'close' }, '模式 A: 直接关闭底层连接'));
    modeSel.appendChild(el('option', { value: 'http_error' }, '模式 C: 发送协议标准 HTTP 错误'));
    modeSel.appendChild(el('option', { value: 'malformed' }, '模式 D: 发送畸形数据 Chunk 后断开'));
    const delay = el('input', { type: 'number', value: '0' });
    const afterChunks = el('input', { type: 'number', value: '0' });
    const raw = el('textarea', { placeholder: '{"code":429,"message":"upstream 429"}' });
    const rawMode = el('input', { type: 'checkbox' });

    body.appendChild(el('label', { class: 'field' }, el('span', {}, 'HTTP 状态码'), status));
    body.appendChild(el('div', { class: 'row grow' },
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '错误代码 (code)'), code),
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '错误类型 (type)'), type)));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, '错误文本提示 (message)'), msg));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, '故障中止模式'), modeSel));
    body.appendChild(el('div', { class: 'row grow' },
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '延迟触发 (毫秒 ms)'), delay),
      el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '达到 N 个 Chunk 后触发 (0=立刻)'), afterChunks)));
    body.appendChild(el('label', { class: 'field' }, el('span', {}, '自定义原始 JSON (勾选 RAW RESPONSE 后按字节原样返回)'), raw));
    body.appendChild(el('label', { class: 'row', style: 'gap:6px;font-size:12.5px' }, rawMode, ' 原样返回 (RAW RESPONSE)'));

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
    body.insertBefore(el('div', { class: 'hint', style: 'margin-bottom:6px' }, '快速选择预设故障：'), presetHost);
    body.insertBefore(presetHost, body.firstChild.nextSibling);

    const foot = el('div', { class: 'row' });
    const cancel = el('button', { class: 'btn' }, '取消');
    const ok = el('button', { class: 'btn btn-danger' }, '注入故障');
    foot.appendChild(cancel); foot.appendChild(ok);
    const m = modal('向会话注入故障 (Fault Injection)', body, foot);
    cancel.addEventListener('click', () => m.close());
    ok.addEventListener('click', async () => {
      const payload = {
        action: 'inject_error',
        http_status: Number(status.value),
        code: code.value,
        type: type.value,
        message: msg.value,
        mode: modeSel.value,
        delay_ms: Number(delay.value),
        after_chunks: Number(afterChunks.value),
        raw: rawMode.checked,
        raw_body: raw.value,
      };
      m.close();
      await send(payload, {});
    });
  }

  // ---------- renderers ----------
  function renderInspector(s) {
    clear(inspBody);
    const kv = el('dl', { class: 'kv' });
    const rows = [
      ['会话 ID (Session ID)', s.session_id], ['请求跟踪 ID (Request ID)', s.request_id],
      ['接口协议 (Protocol)', s.protocol], ['目标模型 (Model)', s.model],
      ['流式传输 (Streaming)', s.streaming ? '是' : '否'], ['会话状态 (State)', s.state],
      ['运行模式 (Mode)', s.mode], ['回显轮次 (Echo Count)', fmtNum(s.echo_count)],
      ['分块发送数 (Chunks)', fmtNum(s.chunk_count)],
      ['输入接收字节', fmtBytes(s.bytes_in)], ['输出发送字节', fmtBytes(s.bytes_out)],
      ['输入 Token 数', fmtNum(s.input_tokens)], ['输出 Token 数', fmtNum(s.output_tokens)],
      ['累计总 Token 数', fmtNum((s.input_tokens || 0) + (s.output_tokens || 0))],
      ['回显间隔时间', s.echo_interval_ms + ' ms'],
      ['回显内容模式', s.echo_content_mode || '—'],
      ['会话建立时间', fmtDateTime(s.created_at)],
      ['会话持续时长', fmtDuration(s.duration_ms)],
      ['客户端 IP', s.client_ip || '—'],
      ['客户端 User-Agent', s.user_agent || '—'],
      ['API 密钥指纹', s.key_fingerprint ? 'sk-****' + String(s.key_fingerprint).slice(-4).toUpperCase() : '—'],
      ['结束原因 (End Reason)', s.end_reason || '—'],
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
    line.appendChild(mkTile('实时速率 (Current)', fmtRate(s.current_rate) + ' tok/s'));
    line.appendChild(mkTile('会话设定 (Configured)', s.rate?.token_rate > 0 ? fmtNum(s.rate.token_rate) + ' tok/s' : '不限速 (Unlimited)'));
    line.appendChild(mkTile('全局限制 (Global)', s.global_limit > 0 ? fmtNum(s.global_limit) + ' tok/s' : '不限速 (Unlimited)'));
    line.appendChild(mkTile('实际生效上限', fmtNum(s.effective_limit > 0 ? s.effective_limit : 0) || '不限速 (Unlimited)'));
    const line2 = el('div', { class: 'grid grid-4' });
    line2.appendChild(mkTile('平均速率 (Average)', fmtRate(s.average_rate) + ' tok/s'));
    line2.appendChild(mkTile('最高峰值 (Peak)', fmtRate(s.peak_rate) + ' tok/s'));
    line2.appendChild(mkTile('已发送 Token', fmtNum(s.output_tokens)));
    line2.appendChild(mkTile('累计回显数', fmtNum(s.echo_count)));
    rateBody.appendChild(line);
    rateBody.appendChild(line2);

    const row = el('div', { class: 'row', style: 'margin-top:12px' });
    row.appendChild(el('span', { class: 'dim', style: 'font-size:12px' }, '快速设置速率:'));
    const rates = [
      { label: '不限速', v: 0 },
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
    const custom = el('input', { type: 'number', placeholder: '自定义 tok/s', style: 'width:110px' });
    const apply = el('button', { class: 'btn btn-xs' }, '应用');
    apply.addEventListener('click', () => send({ action: 'rate', rate: Number(custom.value) }, { quiet: true }));
    row.appendChild(custom); row.appendChild(apply);
    rateBody.appendChild(row);

    const row2 = el('div', { class: 'row', style: 'margin-top:10px' });
    row2.appendChild(el('span', { class: 'dim', style: 'font-size:12px' }, '回显间隔 (ms):'));
    const iv = el('input', { type: 'number', value: String(s.echo_interval_ms ?? 500), style: 'width:100px' });
    const ivBtn = el('button', { class: 'btn btn-xs' }, '修改间隔');
    ivBtn.addEventListener('click', () => send({ action: 'interval', interval_ms: Number(iv.value) }, { quiet: true }));
    row2.appendChild(iv); row2.appendChild(ivBtn);
    row2.appendChild(el('div', { class: 'control-sep' }));
    row2.appendChild(el('span', { class: 'dim', style: 'font-size:12px' }, '回显模式:'));
    const em = el('select', { style: 'width:auto' });
    [['message', '消息 (message)'], ['line', '行 (line)'], ['exact', '紧凑 (exact)'], ['block', '分块 (block)']]
      .forEach(([m, l]) => em.appendChild(el('option', { value: m, selected: (s.echo_content_mode || 'message') === m }, l)));
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
      host.appendChild(el('div', { style: 'margin-top:8px;color:var(--text-faint)' }, '请求头信息 (Headers):'));
      for (const [k, v] of Object.entries(req.headers)) {
        host.appendChild(el('div', { class: 'raw-line' }, `${k}: ${v}`));
      }
    }
    host.appendChild(el('div', { style: 'margin-top:8px;color:var(--text-faint)' }, '原始请求体 (Body):'));
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
      host.appendChild(el('div', { class: 'empty' }, res.note || '尚未捕获到 SSE 数据帧。'));
      return host;
    }
    const box = el('div', { class: 'raw-view' });
    box.appendChild(el('div', { class: 'faint', style: 'margin-bottom:6px' },
      `最近 ${res.frames.length} 个 SSE 原始分帧 (环形缓冲区，最新在后)`));
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
      `最近 ${list.length} 个输出事件 (累计共 ${evs.total} 个)`));
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
