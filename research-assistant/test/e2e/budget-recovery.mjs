#!/usr/bin/env node
// Budget recovery E2E: a soft-paused plan resumes after the budget is raised
// through PATCH /api/plans/:id {budget}.
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const cookies = new Map();
let failures = 0;
function check(condition, message) { if (condition) process.stdout.write('ok   ' + message + '\n'); else { failures++; process.stdout.write('FAIL ' + message + '\n'); } }
async function request(method, path, body) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) headers.Cookie = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) { const pair = raw.split(';')[0] || ''; const i = pair.indexOf('='); if (i > 0) cookies.set(pair.slice(0, i), pair.slice(i + 1)); }
  const text = await response.text(); let data = text; try { data = JSON.parse(text); } catch {}
  return { status: response.status, data };
}
async function main() {
  const created = await request('POST', '/api/plan', { intent: '用 Python 计算 5+5 并打印', max_cost_usd: 0.000001, budget_policy: 'soft' });
  const plan = created.data.plan_graph;
  await request('POST', '/api/plans/' + plan.id + '/execute', {});
  await new Promise((resolve) => setTimeout(resolve, 2500));
  let detail = await request('GET', '/api/plans/' + plan.id);
  check(detail.data.plan_graph.paused === true, 'tiny budget pauses plan before dispatch');
  check(detail.data.plan_graph.usage?.budget_status === 'exceeded', 'budget status exceeded');
  const raised = await request('PATCH', '/api/plans/' + plan.id, { budget: { max_cost_usd: 1000000 } });
  check(raised.status === 200, 'raise plan budget');
  detail = await request('GET', '/api/plans/' + plan.id);
  check(!detail.data.plan_graph.paused, 'raising budget clears soft pause');
  await request('POST', '/api/plans/' + plan.id + '/cancel', {});
  process.stdout.write(failures === 0 ? '\nALL PASSED\n' : '\n' + failures + ' FAILED\n');
  process.exit(failures === 0 ? 0 : 1);
}
main().catch((error) => { console.error(error); process.exit(1); });
