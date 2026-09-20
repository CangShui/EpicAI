// Uploaded files and assets.
import { api } from './api.js';
import { el, clear, fmtBytes } from './dom.js';
import { card, confirmModal, emptyBox } from './views.js';

export async function filesPage(ctx) {
  const hint = el('div', { class: 'notice' },
    '可通过标准接口上传：curl -F file=@test.txt -F purpose=assistants http://HOST/v1/files -H "Authorization: Bearer test"');
  ctx.content.appendChild(hint);

  const c = card('文件与多模态资产', { tight: true });
  const grid = el('div', { class: 'grid grid-3', style: 'padding:14px 16px;gap:12px' });
  c.body.appendChild(grid);
  ctx.content.appendChild(c.root);

  async function load() {
    let res;
    try { res = await api.files(); } catch (e) {
      clear(grid); grid.appendChild(emptyBox('加载文件列表失败: ' + e.message)); return;
    }
    const list = res.files || [];
    clear(grid);
    if (!list.length) {
      grid.appendChild(emptyBox('暂无上传的文件。'));
      return;
    }
    list.forEach(f => {
      const tile = el('div', { class: 'stat' });
      tile.appendChild(el('div', { class: 'stat-label' }, f.filename));
      tile.appendChild(el('div', { class: 'stat-value', style: 'font-size:16px' }, fmtBytes(f.bytes)));
      tile.appendChild(el('div', { class: 'stat-sub mono' }, f.id.slice(0, 22) + '…'));
      tile.appendChild(el('div', { class: 'stat-sub' }, f.mime_type || 'application/octet-stream'));
      if (f.is_image) {
        tile.appendChild(el('img', { class: 'thumb', src: f.preview_url, alt: f.filename, style: 'margin-top:8px' }));
      }
      tile.appendChild(el('div', { class: 'stat-sub mono', style: 'margin-top:6px' }, 'sha256: ' + String(f.sha256 || '').slice(0, 16)));
      const acts = el('div', { class: 'row', style: 'margin-top:8px;gap:5px' });
      const dl = el('a', { class: 'btn btn-xs', href: `/v1/files/${f.id}/content`, target: '_blank' }, '下载原文件');
      const del = el('button', { class: 'btn btn-xs btn-danger' }, '删除');
      del.addEventListener('click', () => confirmModal('删除文件', `确认删除文件 "${f.filename}"？`, async () => {
        await api.deleteFile(f.id); load();
      }, true));
      acts.appendChild(dl); acts.appendChild(del);
      tile.appendChild(acts);
      grid.appendChild(tile);
    });
  }

  await load();
}
