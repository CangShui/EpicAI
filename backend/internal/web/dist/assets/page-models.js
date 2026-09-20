// Model management: create, edit, clone, enable/disable, delete.
import { api } from './api.js';
import { state } from './state.js';
import { el, clear, fmtNum } from './dom.js';
import { card, modal, confirmModal, emptyBox } from './views.js';

const BEHAVIORS = [
  ['infinite_echo', '无限回显 (Infinite Echo)'],
  ['infinite_echo_max', '无限回显MAX (压测大Token聚合)'],
  ['manual_only', '仅人工接管 (Manual Only)'],
  ['immediate_error', '即刻报错 (Immediate Error)'],
  ['hang_forever', '永久挂起/卡死 (Hang Forever)'],
  ['static_response', '固定静态文本 (Static Response)'],
  ['connection_drop', '强制断开连接 (Connection Drop)'],
];

export async function modelsPage(ctx) {
  const toolbar = el('div', { class: 'toolbar' });
  const addBtn = el('button', { class: 'btn btn-primary btn-sm' }, '+ 新增模型');
  const refreshBtn = el('button', { class: 'btn btn-sm' }, '刷新');
  toolbar.appendChild(addBtn);
  toolbar.appendChild(refreshBtn);
  toolbar.appendChild(el('span', { class: 'faint', style: 'font-size:12px' },
    '修改实时生效 — 无需重启后端服务。'));
  ctx.content.appendChild(toolbar);

  const c = card('模型列表', { tight: true });
  const wrap = el('div', { class: 'table-wrap' });
  c.body.appendChild(wrap);
  ctx.content.appendChild(c.root);

  addBtn.addEventListener('click', () => openEditor(null, load));
  refreshBtn.addEventListener('click', load);

  async function load() {
    let list = [];
    try { list = await api.models(); } catch (e) {
      clear(wrap); wrap.appendChild(emptyBox('加载模型失败: ' + e.message)); return;
    }
    clear(wrap);
    const table = el('table');
    const thead = el('thead');
    const hr = el('tr');
    ['模型 ID', '显示名称', '行为模式', '回显间隔', '回显单位', 'Agent', '状态', '创建时间', '操作'].forEach(h => hr.appendChild(el('th', {}, h)));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    if (!list.length) {
      tbody.appendChild(el('tr', {}, el('td', { colspan: '9' }, emptyBox('暂无配置的模型。'))));
    }
    list.forEach(m => {
      const tr = el('tr');
      tr.appendChild(el('td', { class: 'mono' }, m.model_id));
      tr.appendChild(el('td', {}, m.display_name));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + behaviorTone(m.behavior) }, m.behavior)));
      tr.appendChild(el('td', { class: 'num mono' }, m.default_echo_interval_ms + 'ms'));
      tr.appendChild(el('td', { class: 'mono dim' }, m.echo_content_mode || 'message'));
      tr.appendChild(el('td', {}, m.enable_agent ? el('span', { class: 'badge accent' }, '已开启 ×' + (m.subagent_count || 1)) : el('span', { class: 'badge gray' }, '未开启')));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + (m.enabled ? 'green' : 'gray') }, m.enabled ? '已启用' : '已禁用')));
      tr.appendChild(el('td', { class: 'nowrap dim' }, new Date(m.created_at).toLocaleDateString('zh-CN')));
      const acts = el('div', { class: 'row', style: 'gap:5px' });
      const edit = el('button', { class: 'btn btn-xs' }, '编辑');
      edit.addEventListener('click', () => openEditor(m, load));
      const tog = el('button', { class: 'btn btn-xs' }, m.enabled ? '禁用' : '启用');
      tog.addEventListener('click', async () => { await api.updateModel(m.model_id, { enabled: !m.enabled }); load(); });
      const clone = el('button', { class: 'btn btn-xs' }, '克隆');
      clone.addEventListener('click', async () => {
        const newId = prompt('请输入新模型 ID:', m.model_id + '-copy');
        if (!newId) return;
        await api.cloneModel(m.model_id, newId);
        load();
      });
      const del = el('button', { class: 'btn btn-xs btn-danger' }, '删除');
      del.addEventListener('click', () => confirmModal('删除模型',
        `确认删除模型 "${m.model_id}"？删除后客户端发起请求将返回 404。`,
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
    infinite_echo: 'green', infinite_echo_max: 'accent', manual_only: 'purple',
    immediate_error: 'red', hang_forever: 'yellow',
    static_response: 'gray', connection_drop: 'red',
  }[b] || 'gray';
}

export function openEditor(model, onDone) {
  const isNew = !model;
  const m = Object.assign({
    model_id: '', display_name: '', behavior: 'infinite_echo',
    default_echo_interval_ms: 500, echo_content_mode: 'message',
    description: '', static_response: '',
    error_status: 503, error_code: 'service_unavailable', error_type: 'server_error', error_message: 'Service Unavailable',
    token_rate: 0, max_echo_count: 0, enabled: true,
    enable_agent: false, subagent_count: 1, max_token_chunk: 4096,
  }, model || {});

  const body = el('div');
  const idIn = el('input', { type: 'text', value: m.model_id, placeholder: '例如：epic-alpha' });
  if (!isNew) idIn.disabled = true;
  const nameIn = el('input', { type: 'text', value: m.display_name, placeholder: '用于界面展示的名称' });
  const descIn = el('input', { type: 'text', value: m.description || '', placeholder: '模型功能说明' });

  // 行为模式
  const beh = el('select');
  BEHAVIORS.forEach(([v, l]) => beh.appendChild(el('option', { value: v, selected: m.behavior === v }, l)));

  // 通用头部：ID、名称、模式、描述
  body.appendChild(el('div', { class: 'row grow' },
    el('label', { class: 'field', style: 'flex:2' }, el('span', {}, '模型 ID (唯一标识)'), idIn),
    el('label', { class: 'field', style: 'flex:2' }, el('span', {}, '显示名称'), nameIn)));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '模型描述'), descIn));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '行为模式 (Behavior)'), beh));

  // --- 动态区域容器 ---
  const dynamicContainer = el('div', { style: 'margin-top:10px' });
  body.appendChild(dynamicContainer);

  // 字段控件定义
  const iv = el('input', { type: 'number', value: String(m.default_echo_interval_ms || 0) });
  const em = el('select');
  [['message', '整条消息 (message)'], ['line', '按行分块 (line)'], ['exact', '原样紧凑 (exact)'], ['block', '分块段落 (block)']]
    .forEach(([v, l]) => em.appendChild(el('option', { value: v, selected: (m.echo_content_mode || 'message') === v }, l)));
  const rate = el('input', { type: 'number', value: String(m.token_rate || 0) });

  const agentChk = el('input', { type: 'checkbox', checked: !!m.enable_agent });
  const subagentNum = el('input', { type: 'number', value: String(m.subagent_count || 1), min: '1', max: '50', style: 'width:90px' });

  const maxChunkIn = el('input', { type: 'number', value: String(m.max_token_chunk || 4096), placeholder: '默认 4096', style: 'width:120px' });

  const staticText = el('textarea', { placeholder: '输入固定回复的文本内容…', style: 'min-height:80px' });
  staticText.value = m.static_response || '';

  const eStatus = el('input', { type: 'number', value: String(m.error_status || 503) });
  const eCode = el('input', { type: 'text', value: m.error_code || 'service_unavailable' });
  const eType = el('input', { type: 'text', value: m.error_type || 'server_error' });
  const eMsg = el('input', { type: 'text', value: m.error_message || 'Service Unavailable' });

  // 渲染动态字段
  function renderBehaviorFields() {
    clear(dynamicContainer);
    const selected = beh.value;

    if (selected === 'infinite_echo' || selected === 'infinite_echo_max') {
      // Echo 参数设置
      const echoCard = el('div', { class: 'card', style: 'padding:12px;margin-bottom:10px' });
      echoCard.appendChild(el('div', { class: 'card-head', style: 'font-weight:600;margin-bottom:8px' }, '回显参数设置'));
      
      const row1 = el('div', { class: 'row grow' });
      row1.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '回显间隔 (ms，0=最高速)'), iv));
      if (selected === 'infinite_echo') {
        row1.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '回显内容模式'), em));
      }
      row1.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '输出速率限制 (0 = 不限速)'), rate));
      echoCard.appendChild(row1);

      // 如果是 MAX 模式，展示聚合 Token 压测设置
      if (selected === 'infinite_echo_max') {
        const maxSection = el('div', { style: 'margin-top:10px;padding:10px;background:rgba(255,165,0,0.1);border-radius:6px;border:1px solid rgba(255,165,0,0.25)' });
        maxSection.appendChild(el('div', { style: 'font-weight:600;color:var(--yellow);margin-bottom:6px' }, '⚡ 压测配置：用户输入聚合模式'));
        const maxRow = el('div', { class: 'row grow', style: 'align-items:center;gap:12px' });
        maxRow.appendChild(el('span', { style: 'font-size:13px' }, '单次聚合目标 Token 块大小:'));
        maxRow.appendChild(maxChunkIn);
        maxSection.appendChild(maxRow);
        maxSection.appendChild(el('div', { class: 'hint', style: 'margin-top:4px' }, '例如用户发送“你好”，系统会自动复制拼接为 4096 Token 的超大块后按间隔无限循环输出。'));
        echoCard.appendChild(maxSection);
      }

      // Agent / 子代理配置
      const agentSection = el('div', { style: 'margin-top:10px;padding:10px;background:rgba(88,166,255,0.08);border-radius:6px;border:1px solid rgba(88,166,255,0.2)' });
      agentSection.appendChild(el('div', { style: 'font-weight:600;color:var(--accent);margin-bottom:6px' }, '🤖 Agent / 子代理循环能力'));
      const agentRow = el('div', { class: 'row grow', style: 'align-items:center;gap:16px' });
      agentRow.appendChild(el('label', { class: 'row', style: 'gap:6px;font-size:13px;cursor:pointer' }, agentChk, ' 开启 Agent 工具调用'));
      agentRow.appendChild(el('label', { class: 'row', style: 'gap:6px;font-size:13px' }, el('span', {}, '并发子代理数:'), subagentNum));
      agentSection.appendChild(agentRow);
      agentSection.appendChild(el('div', { class: 'hint', style: 'margin-top:6px' }, '客户端若在 Agent 环境请求（携带 tools 声明），模型将边吐字边派发指定数量的子代理调用（如 task / bash），并在子代理内执行 echo 循环。'));
      echoCard.appendChild(agentSection);

      dynamicContainer.appendChild(echoCard);
    } else if (selected === 'manual_only') {
      const box = el('div', { class: 'notice info' });
      box.innerHTML = '<strong>仅人工接管模式：</strong><br/>客户端发起请求后，模型不会自动生成回复，保持长连接挂起。<br/>管理员可在【会话管理】中进入对应会话，点击【人工接管】实时打字或推送消息。';
      dynamicContainer.appendChild(box);
    } else if (selected === 'immediate_error') {
      const errCard = el('div', { class: 'card', style: 'padding:12px' });
      errCard.appendChild(el('div', { class: 'card-head', style: 'font-weight:600;margin-bottom:8px' }, '即刻报错配置 (客户端请求将立即收到指定错误)'));

      // 预设按钮
      const presetRow = el('div', { class: 'row', style: 'gap:6px;flex-wrap:wrap;margin-bottom:12px' });
      const presets = [
        { s: 400, c: 'bad_request', t: 'invalid_request_error', m: 'Bad Request' },
        { s: 401, c: 'invalid_api_key', t: 'authentication_error', m: 'Incorrect API key provided' },
        { s: 403, c: 'permission_denied', t: 'permission_error', m: 'Missing permissions for model' },
        { s: 404, c: 'model_not_found', t: 'invalid_request_error', m: 'The model does not exist' },
        { s: 429, c: 'rate_limit_exceeded', t: 'rate_limit_error', m: 'Rate limit exceeded' },
        { s: 500, c: 'server_error', t: 'api_error', m: 'Internal Server Error' },
        { s: 503, c: 'service_unavailable', t: 'server_error', m: 'Service Unavailable' },
        { s: 429, c: '6004', t: 'quota_exceeded', m: '您的使用量已超出频率限制' },
      ];
      presets.forEach(p => {
        const b = el('button', { class: 'btn btn-xs', type: 'button' }, `${p.s} ${p.c}`);
        b.addEventListener('click', () => {
          eStatus.value = String(p.s);
          eCode.value = p.c;
          eType.value = p.t;
          eMsg.value = p.m;
        });
        presetRow.appendChild(b);
      });
      errCard.appendChild(el('div', { class: 'hint', style: 'margin-bottom:4px' }, '快速选择预设:'));
      errCard.appendChild(presetRow);

      const row1 = el('div', { class: 'row grow' });
      row1.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, 'HTTP 状态码'), eStatus));
      row1.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '错误代码 (code)'), eCode));
      errCard.appendChild(row1);

      const row2 = el('div', { class: 'row grow' });
      row2.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '错误类型 (type)'), eType));
      row2.appendChild(el('label', { class: 'field', style: 'flex:1' }, el('span', {}, '错误描述信息 (message)'), eMsg));
      errCard.appendChild(row2);

      dynamicContainer.appendChild(errCard);
    } else if (selected === 'hang_forever') {
      const box = el('div', { class: 'notice warn' });
      box.innerHTML = '<strong>【混沌测试】永久挂起模式：</strong><br/>连接建立后服务器保持 TCP/HTTP 连通，但不发送任何数据，用于测试客户端的超时（Timeout）检测与重试机制。';
      dynamicContainer.appendChild(box);
    } else if (selected === 'static_response') {
      const statCard = el('div', { class: 'card', style: 'padding:12px' });
      statCard.appendChild(el('div', { class: 'card-head', style: 'font-weight:600;margin-bottom:8px' }, '固定静态回复配置'));
      statCard.appendChild(el('label', { class: 'field' }, el('span', {}, '返回的固定文本 (留空则返回最后一条用户输入)'), staticText));
      dynamicContainer.appendChild(statCard);
    } else if (selected === 'connection_drop') {
      const box = el('div', { class: 'notice danger' });
      box.innerHTML = '<strong>【异常测试】强制断开连接：</strong><br/>客户端连接后，服务器在 50ms 内直接关闭 TCP Socket（不发送 HTTP 结束符），用于测试客户端在面对网络突发中断时的异常处理。';
      dynamicContainer.appendChild(box);
    }
  }

  beh.addEventListener('change', renderBehaviorFields);
  renderBehaviorFields();

  const foot = el('div', { class: 'row' });
  const cancel = el('button', { class: 'btn' }, '取消');
  const save = el('button', { class: 'btn btn-primary' }, isNew ? '创建' : '保存');
  foot.appendChild(cancel); foot.appendChild(save);
  const md = modal(isNew ? '新建模型' : '编辑模型', body, foot);
  cancel.addEventListener('click', () => md.close());
  save.addEventListener('click', async () => {
    const payload = {
      model_id: idIn.value.trim(),
      display_name: nameIn.value.trim() || idIn.value.trim(),
      description: descIn.value.trim(),
      behavior: beh.value,
      default_echo_interval_ms: Number(iv.value) || 0,
      echo_content_mode: em.value,
      token_rate: Number(rate.value) || 0,
      enable_agent: agentChk.checked,
      subagent_count: Number(subagentNum.value) || 1,
      max_token_chunk: Number(maxChunkIn.value) || 4096,
      static_response: staticText.value,
      error_status: Number(eStatus.value) || 500,
      error_code: eCode.value.trim(),
      error_type: eType.value.trim(),
      error_message: eMsg.value.trim(),
    };
    try {
      if (isNew) await api.createModel(payload);
      else await api.updateModel(m.model_id, payload);
      md.close();
      if (onDone) onDone();
    } catch (e) {
      alert('操作失败: ' + e.message);
    }
  });
}
