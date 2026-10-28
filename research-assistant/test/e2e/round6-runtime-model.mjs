#!/usr/bin/env node
// Round-6 runtime model routing: prove DataAgent/verify_result uses the user's
// main profile instead of the env bootstrap model.
const BASE = (process.env.RA_API_URL || 'http://backend:8080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const MODEL_BASE = (process.env.MOCK_VISION_URL || '').replace(/\/$/, '');
const USER = 'e2e-runtime-' + Math.random().toString(36).slice(2, 10);
const jar = new Map();
let failures = 0;
const check = (ok, msg) => { console.log((ok ? 'ok   ' : 'FAIL ') + msg); if (!ok) failures++; };
async function request(method, path, body) {
  const headers = { Accept: 'application/json', 'X-User-Id': USER };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (jar.size) headers.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload;
  if (body !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(body); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || ''; const i = pair.indexOf('=');
    if (i > 0) jar.set(pair.slice(0, i), pair.slice(i + 1));
  }
  const ct = response.headers.get('content-type') || '';
  return { status: response.status, data: ct.includes('json') ? await response.json() : await response.text() };
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function main() {
  if (!MODEL_BASE) { console.error('MOCK_VISION_URL required'); process.exit(2); }
  await request('GET', '/api/me');
  const created = await request('POST', '/api/model-profiles', {
    name: 'Runtime Main', provider: 'openai_compatible', base_url: MODEL_BASE,
    model: 'mock-main', api_key: 'sk-runtime-0001', roles: ['main'], supports_vision: 'no',
  });
  check(created.status === 201, 'profile created (' + created.status + ')');
  const profile = created.data;
  const routing = await request('PUT', '/api/model-routing', { main_profile_id: profile.id, vision_profile_ids: [] });
  check(routing.status === 200, 'routing saved (' + routing.status + ')');
  const plan = await request('POST', '/api/plan', {
    intent: '用 Python 计算 1+1',
    runtime_permission_mode: 'full_access',
    runtime_permission_acknowledged: true,
  });
  check(plan.status === 200, 'plan created (' + plan.status + ')');
  const graph = plan.data.plan_graph;
  check(graph.model_snapshot?.text?.profile_id === profile.id, 'plan snapshot records the user profile (' + graph.model_snapshot?.text?.profile_id + ')');
  const exec = await request('POST', '/api/plans/' + graph.id + '/execute', { trigger: 'manual' });
  check(exec.status === 200 || exec.status === 202, 'plan execute accepted (' + exec.status + ')');
  let current = graph;
  for (let i = 0; i < 150; i++) {
    await sleep(2000);
    const detail = await request('GET', '/api/plans/' + graph.id);
    current = detail.data.plan_graph || current;
    if (['completed', 'failed', 'canceled'].includes(current.status)) break;
  }
  check(current.status === 'completed', 'plan completed (' + current.status + ')');
  const nodes = current.nodes || [];
  const generate = nodes.find((n) => n.type === 'generate_code');
  const verify = nodes.find((n) => n.type === 'verify_result');
  const generateModels = Object.keys(generate?.usage?.models || {});
  const verifyModels = Object.keys(verify?.usage?.models || {});
  console.log('     generate_code models=' + JSON.stringify(generateModels) + ' verify_result models=' + JSON.stringify(verifyModels));
  check(generateModels.includes('mock-main'), 'generate_code used the runtime profile model');
  check(verifyModels.includes('mock-main'), 'verify_result/DataAgent used the runtime profile model (not env bootstrap)');
  if (failures) { console.log('\n' + failures + ' failure(s)'); process.exit(1); }
  console.log('\nruntime model routing checks passed');
}
main().catch((e) => { console.error('runtime model test failed: ' + e.message); process.exit(1); });
