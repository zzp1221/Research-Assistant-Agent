#!/usr/bin/env node
// Round 7 task-level execution and cancellation checks.
import { writeFile } from 'node:fs/promises';
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const jars = new Map();
const jarFor = (user) => { if (!jars.has(user)) jars.set(user, new Map()); return jars.get(user); };
let failures = 0;
const check = (ok, message, detail) => { console.log((ok ? 'ok   ' : 'FAIL ') + message + (detail === undefined ? '' : ' (' + JSON.stringify(detail) + ')')); if (!ok) failures++; };
async function request(method, path, { body, user = 'round7-task', raw = false } = {}) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  headers['X-User-Id'] = user;
  const jar = jarFor(user);
  if (jar.size) headers.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined && payload !== null && typeof payload !== 'string') { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const cookie of response.headers.getSetCookie?.() || []) { const pair = cookie.split(';')[0] || ''; const i = pair.indexOf('='); if (i > 0) jar.set(pair.slice(0, i).trim(), pair.slice(i + 1).trim()); }
  if (raw) return response;
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json(); else data = await response.text();
  return { status: response.status, data };
}
async function createPlan(user, intent) {
  const response = await request('POST', '/api/plan', { body: { intent, runtime_permission_mode: 'full_access', runtime_permission_acknowledged: true }, user });
  if (response.status !== 200) throw new Error('create failed ' + response.status + ' ' + JSON.stringify(response.data).slice(0, 300));
  return response.data.plan_graph;
}
async function main() {
  const plan = await createPlan('round7-task', '请只回答：3+4等于几？只输出数字。');
  check(plan.intent_type === 'General' && plan.nodes.length === 1, 'task plan created', plan.nodes.length);
  const taskId = plan.nodes[0].id;
  const response = await request('POST', '/api/plans/' + plan.id + '/tasks/' + taskId + '/execute', { user: 'round7-task', raw: true });
  const text = await response.text();
  check(response.status === 200 && String(response.headers.get('content-type') || '').includes('text/event-stream'), 'task execute returns SSE', response.status);
  check(text.includes('event:result') || text.includes('"result"'), 'task SSE contains result event', text.slice(0, 200));
  check(/(^|\D)7(\D|$)/.test(text), 'task result contains 7', text.slice(-500));
  const cancelPlan = await createPlan('round7-cancel', '请用一句话解释什么是哈希表。');
  const canceled = await request('POST', '/api/plans/' + cancelPlan.id + '/cancel', { body: {}, user: 'round7-cancel' });
  check(canceled.status === 200, 'cancel plan succeeds', canceled.status);
  const fetched = await request('GET', '/api/plans/' + cancelPlan.id, { user: 'round7-cancel' });
  check(fetched.data?.plan_graph?.status === 'canceled', 'plan status is canceled', fetched.data?.plan_graph?.status);
  await writeFile('/reports/round7-task-execute.json', JSON.stringify({ plan_id: plan.id, task_id: taskId, sse_tail: text.slice(-2000) }, null, 2)).catch(() => {});
  if (failures) { console.log('\n' + failures + ' task checks failed'); process.exit(1); }
  console.log('\nall task execution checks passed');
}
main().catch((error) => { console.error('task checks failed: ' + (error?.stack || error)); process.exit(1); });
