// Admin API client.
import { state } from './state.js';

function token() {
  if (!state.token) state.token = localStorage.getItem('epicai_token');
  return state.token;
}

export async function logReport(entry) {
  try {
    const t = token();
    const headers = { 'Content-Type': 'application/json' };
    if (t) headers['X-Admin-Token'] = t;
    await fetch('/admin/api/logs/report', {
      method: 'POST',
      headers,
      body: JSON.stringify(Object.assign({ clientTime: new Date().toISOString() }, entry)),
    });
  } catch {}
}

async function request(path, opts = {}) {
  const headers = Object.assign({ 'Content-Type': 'application/json' }, opts.headers || {});
  const t = token();
  if (t) headers['X-Admin-Token'] = t;
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (res.status === 401) {
    localStorage.removeItem('epicai_token');
    state.token = null;
    throw new Error('unauthorized');
  }
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch { data = text; } }
  if (!res.ok) {
    const msg = (data && data.error) || res.statusText || 'request failed';
    logReport({
      tag: '[前端接口异常]',
      action: (opts.method || 'GET') + ' ' + path,
      status: res.status,
      error: msg,
      businessImpact: '操作未成功完成',
    });
    throw new Error(msg);
  }
  logReport({
    tag: '[前端接口响应]',
    action: (opts.method || 'GET') + ' ' + path,
    status: res.status,
    result: '成功',
  });
  return data;
}

export const api = {
  token: null,
  login: (username, password) => request('/admin/api/login', {
    method: 'POST', body: JSON.stringify({ username, password })
  }).then(d => { api.token = d.token; state.token = d.token; return d; }),
  me: () => request('/admin/api/me'),
  changePassword: (old_password, new_password) => request('/admin/api/password', {
    method: 'POST', body: JSON.stringify({ old_password, new_password })
  }),

  stats: () => request('/admin/api/stats'),
  settings: () => request('/admin/api/settings'),
  updateSettings: (patch) => request('/admin/api/settings', { method: 'POST', body: JSON.stringify(patch) }),

  sessions: (params = {}) => {
    const q = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== '' && v !== undefined));
    return request('/admin/api/sessions?' + q.toString());
  },
  session: (id) => request('/admin/api/sessions/' + encodeURIComponent(id)),
  sessionEvents: (id, limit = 500) => request(`/admin/api/sessions/${encodeURIComponent(id)}/events?limit=${limit}`),
  sessionRawSSE: (id) => request(`/admin/api/sessions/${encodeURIComponent(id)}/raw-sse`),
  control: (id, body) => request(`/admin/api/sessions/${encodeURIComponent(id)}/control`, {
    method: 'POST', body: JSON.stringify(body)
  }),
  deleteSession: (id) => request('/admin/api/sessions/' + encodeURIComponent(id), { method: 'DELETE' }),

  models: () => request('/admin/api/models'),
  createModel: (m) => request('/admin/api/models', { method: 'POST', body: JSON.stringify(m) }),
  updateModel: (id, patch) => request('/admin/api/models/' + encodeURIComponent(id), { method: 'PUT', body: JSON.stringify(patch) }),
  cloneModel: (id, newId) => request(`/admin/api/models/${encodeURIComponent(id)}/clone`, { method: 'POST', body: JSON.stringify({ model_id: newId }) }),
  deleteModel: (id) => request('/admin/api/models/' + encodeURIComponent(id), { method: 'DELETE' }),

  keys: () => request('/admin/api/keys'),
  createKey: (body) => request('/admin/api/keys', { method: 'POST', body: JSON.stringify(body) }),
  updateKey: (id, patch) => request('/admin/api/keys/' + encodeURIComponent(id), { method: 'PUT', body: JSON.stringify(patch) }),
  deleteKey: (id) => request('/admin/api/keys/' + encodeURIComponent(id), { method: 'DELETE' }),

  files: () => request('/admin/api/files'),
  deleteFile: (id) => request('/admin/api/files/' + encodeURIComponent(id), { method: 'DELETE' }),

  audit: (limit = 200) => request('/admin/api/audit?limit=' + limit),
  logs: (params) => request('/admin/api/logs?' + new URLSearchParams(params).toString()),
  benchmarks: () => request('/admin/api/benchmarks'),
  runBenchmark: (body) => request('/admin/api/benchmarks', { method: 'POST', body: JSON.stringify(body) }),
  presets: () => request('/admin/api/fault-presets'),
  vlogs: () => request('/admin/api/vlogs'),
};

export function wsUrl() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${location.host}/admin/api/ws?token=${encodeURIComponent(token() || '')}`;
}
