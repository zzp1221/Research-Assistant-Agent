#!/usr/bin/env node
// Plan library E2E: exercises projects, filters, stable pagination, rename,
// archive, duplicate and the running-plan delete guard against a live stack.
import { randomUUID } from 'node:crypto';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-plan-library-' + randomUUID().slice(0, 8);
const cookies = new Map();
let failures = 0;

function check(condition, message) {
  if (condition) process.stdout.write('ok   ' + message + '\n');
  else { failures++; process.stdout.write('FAIL ' + message + '\n'); }
}

async function request(method, path, { body, headers = {} } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) finalHeaders.Cookie = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  return { status: response.status, data, headers: response.headers };
}

async function createPlan(intent) {
  const response = await request('POST', '/api/plan', { body: { intent } });
  if (response.status !== 200) throw new Error('create plan failed: ' + response.status + ' ' + JSON.stringify(response.data).slice(0, 400));
  return response.data.plan_graph;
}

async function main() {
  process.stdout.write('# plan-library e2e user=' + USER + '\n');
  const health = await request('GET', '/api/health');
  check(health.status === 200, 'health responds');

  const project = await request('POST', '/api/projects', { body: { name: 'E2E Project ' + randomUUID().slice(0, 6), description: 'e2e' } });
  check(project.status === 200 && project.data.project?.id, 'create project');
  const projectId = project.data.project.id;

  const planA = await createPlan('用 Python 画一个正弦函数图像');
  const planB = await createPlan('总结量子计算的基本概念');
  check(Boolean(planA.id && planB.id), 'created two plans');

  const renamed = await request('PATCH', '/api/plans/' + planA.id, { body: { title: 'E2E renamed plan', project_id: projectId } });
  check(renamed.status === 200 && renamed.data.plan?.title === 'E2E renamed plan' && renamed.data.plan?.project_id === projectId, 'rename and move plan');

  const filtered = await request('GET', '/api/plans?project_id=' + encodeURIComponent(projectId) + '&q=E2E');
  check(filtered.status === 200 && filtered.data.plans?.length === 1 && filtered.data.plans[0].id === planA.id, 'filter by project and query');

  const archived = await request('PATCH', '/api/plans/' + planA.id, { body: { archived: true } });
  check(archived.status === 200 && archived.data.plan?.archived === true, 'archive plan');
  const defaultList = await request('GET', '/api/plans?q=E2E%20renamed');
  check(defaultList.status === 200 && defaultList.data.plans?.length === 0, 'archived plan hidden by default');
  const archivedList = await request('GET', '/api/plans?q=E2E%20renamed&archived=true');
  check(archivedList.status === 200 && archivedList.data.plans?.length === 1, 'archived filter shows plan');

  const restored = await request('PATCH', '/api/plans/' + planA.id, { body: { archived: false } });
  check(restored.status === 200 && restored.data.plan?.archived === false, 'restore plan');

  const duplicated = await request('POST', '/api/plans/' + planA.id + '/duplicate', { body: {} });
  check(duplicated.status === 200 && duplicated.data.plan?.id !== planA.id && duplicated.data.plan?.title.startsWith('Copy of'), 'duplicate plan');

  const firstPage = await request('GET', '/api/plans?limit=1');
  check(firstPage.status === 200 && firstPage.data.plans?.length === 1 && firstPage.data.next_cursor, 'first page returns a cursor');
  if (firstPage.data.next_cursor) {
    const secondPage = await request('GET', '/api/plans?limit=1&cursor=' + encodeURIComponent(firstPage.data.next_cursor));
    check(secondPage.status === 200 && secondPage.data.plans?.length === 1 && secondPage.data.plans[0].id !== firstPage.data.plans[0].id, 'keyset cursor has no duplicate');
  }

  const exportResponse = await fetch(BASE + '/api/plans/' + planB.id + '/export?format=zip', {
    headers: { ...(TOKEN ? { Authorization: 'Bearer ' + TOKEN } : {}), Cookie: [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ') },
  });
  check(exportResponse.status === 200 && exportResponse.headers.get('content-type')?.includes('zip'), 'zip export downloads');
  const zipBytes = new Uint8Array(await exportResponse.arrayBuffer());
  check(zipBytes.length > 100, 'zip export is non-empty');

  const nonEmptyDelete = await request('DELETE', '/api/projects/' + projectId);
  check(nonEmptyDelete.status === 409, 'non-empty project delete rejected');

  const runningPlan = await createPlan('运行一段 Python 代码并输出结果');
  const execute = await request('POST', '/api/plans/' + runningPlan.id + '/execute', { body: {} });
  check(execute.status === 202 || execute.status === 200 || execute.status === 409, 'execute plan accepted');
  const deleteRunning = await request('DELETE', '/api/plans/' + runningPlan.id);
  check(deleteRunning.status === 409, 'running plan delete rejected');
  await request('POST', '/api/plans/' + runningPlan.id + '/cancel', { body: {} });

  process.stdout.write(failures === 0 ? '\nALL PASSED\n' : '\n' + failures + ' FAILED\n');
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
