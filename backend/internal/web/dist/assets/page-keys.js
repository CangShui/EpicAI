// API key management.
import { api } from './api.js';
import { el, clear, fmtNum } from './dom.js';
import { card, modal, confirmModal, emptyBox } from './views.js';

export async function keysPage(ctx) {
  const c = card('速率与密钥策略', { tight: true });
  const cfgBody = el('div', { style: 'padding:14px 16px' });
  c.body.appendChild(cfgBody);
  ctx.content.appendChild(c.root);

  let settings = null;
  try { settings = await api.settings(); } catch {}
  const rt = settings?.runtime || {};

  const modeSel = el('select', { style: 'width:auto;min-width:190px' });
  [['any', '接受任意非空 Bearer Key'], ['require', '必须校验托管 Key'], ['allow_empty', '允许空 Key']]
    .forEach(([v, l]) => modeSel.appendChild(el('option', { value: v, selected: rt.key_mode === v }, l)));
  modeSel.addEventListener('change', async () => {
    await api.updateSettings({ key_mode: modeSel.value });
    notice.textContent = 'API Key 校验模式已更新为: ' + modeSel.value;
  });
  const notice = el('div', { class: 'notice', style: 'margin:0' },
    'API 密钥在数据库中仅保留 SHA-256 哈希脱敏指纹，后台绝不明文显示或存储。');
  const row = el('div', { class: 'row', style: 'margin-top:10px' });
  row.appendChild(el('span', {}, '当前校验模式:'));
  row.appendChild(modeSel);
  cfgBody.appendChild(row);
  cfgBody.appendChild(el('div', { style: 'margin-top:10px' }, notice));
  cfgBody.appendChild(el('div', { class: 'hint' },
    '在“必须校验托管 Key”模式下，未在下方创建的 Key 请求将返回 HTTP 401。'));

  const toolbar = el('div', { class: 'toolbar', style: 'margin-top:16px' });
  const addBtn = el('button', { class: 'btn btn-primary btn-sm' }, '+ 创建 API 密钥');
  toolbar.appendChild(addBtn);
  ctx.content.appendChild(toolbar);

  const kc = card('托管 API 密钥列表', { tight: true });
  const wrap = el('div', { class: 'table-wrap' });
  kc.body.appendChild(wrap);
  ctx.content.appendChild(kc.root);

  addBtn.addEventListener('click', () => openCreate(load));

  async function load() {
    let list = [];
    try { list = await api.keys(); } catch (e) {
      clear(wrap); wrap.appendChild(emptyBox('加载密钥失败: ' + e.message)); return;
    }
    clear(wrap);
    const table = el('table');
    const thead = el('thead');
    const hr = el('tr');
    ['名称', '密钥指纹', '状态', '调用次数', '最后使用时间', '限定模型', '最大并发数', '操作'].forEach(h => hr.appendChild(el('th', {}, h)));
    thead.appendChild(hr);
    table.appendChild(thead);
    const tbody = el('tbody');
    if (!list.length) tbody.appendChild(el('tr', {}, el('td', { colspan: '8' }, emptyBox('暂无托管密钥。'))));
    list.forEach(k => {
      const tr = el('tr');
      tr.appendChild(el('td', {}, k.name));
      tr.appendChild(el('td', { class: 'mono' }, k.masked || k.fingerprint));
      tr.appendChild(el('td', {}, el('span', { class: 'badge ' + (k.enabled ? 'green' : 'gray') }, k.enabled ? '已启用' : '已禁用')));
      tr.appendChild(el('td', { class: 'num mono' }, fmtNum(k.use_count)));
      tr.appendChild(el('td', { class: 'nowrap dim' }, k.last_used_at ? new Date(k.last_used_at).toLocaleString('zh-CN') : '从未'));
      tr.appendChild(el('td', { class: 'dim' }, (k.models || []).join(', ') || '全部模型'));
      tr.appendChild(el('td', { class: 'num mono' }, k.max_sessions || '—'));
      const acts = el('div', { class: 'row', style: 'gap:5px' });
      const tog = el('button', { class: 'btn btn-xs' }, k.enabled ? '禁用' : '启用');
      tog.addEventListener('click', async () => { await api.updateKey(k.id, { enabled: !k.enabled }); load(); });
      const del = el('button', { class: 'btn btn-xs btn-danger' }, '删除');
      del.addEventListener('click', () => confirmModal('删除密钥', `确认删除密钥 "${k.name}"？`, async () => {
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
  const models = el('input', { type: 'text', placeholder: '英文逗号分隔，留空表示不限制' });
  const maxSessions = el('input', { type: 'number', value: '0' });
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '密钥备注名称'), name));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '允许调用的模型 (留空=允许全部)'), models));
  body.appendChild(el('label', { class: 'field' }, el('span', {}, '最大并发会话数 (0 = 遵循全局限制)'), maxSessions));
  const foot = el('div', { class: 'row' });
  const cancel = el('button', { class: 'btn' }, '取消');
  const ok = el('button', { class: 'btn btn-primary' }, '创建');
  foot.appendChild(cancel); foot.appendChild(ok);
  const md = modal('创建 API 密钥', body, foot);
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
    } catch (e) { alert('创建失败: ' + e.message); }
  });
}

function showKey(key) {
  const body = el('div');
  body.appendChild(el('div', { class: 'notice danger' },
    '这是唯一一次展示完整明文密钥，请立即复制妥善保存。'));
  const box = el('div', { class: 'raw-view' }, key.key);
  body.appendChild(box);
  body.appendChild(el('div', { class: 'hint' }, '脱敏存储形式: ' + (key.masked || '')));
  const foot = el('div', { class: 'row' });
  const copy = el('button', { class: 'btn' }, '复制密钥');
  const close = el('button', { class: 'btn btn-primary' }, '完成');
  copy.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(key.key); copy.textContent = '已复制'; } catch {}
  });
  foot.appendChild(copy); foot.appendChild(close);
  const md = modal('API 密钥已生成', body, foot);
  close.addEventListener('click', () => md.close());
}
