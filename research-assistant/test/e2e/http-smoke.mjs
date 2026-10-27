#!/usr/bin/env node
// HTTP smoke test for a running Research Assistant stack.
// Usage: RA_API_URL=http://127.0.0.1:18080 RA_API_TOKEN=... node test/e2e/http-smoke.mjs
import { randomUUID } from 'node:crypto';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-smoke-user';

const cookies = new Map();
let failures = 0;

function check(condition, message) {
  if (condition) {
    process.stdout.write('ok   ' + message + '\n');
  } else {
    failures++;
    process.stdout.write('FAIL ' + message + '\n');
  }
}

async function request(method, requestPath, { body, headers = {}, expect } = {}) {
  const finalHeaders = { Accept: 'application/json', 'X-User-Id': USER, ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) finalHeaders.Cookie = [...cookies.entries()].map(([key, value]) => key + '=' + value).join('; ');
  let payload = body;
  if (payload !== undefined && payload !== null && !(payload instanceof FormData) && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + requestPath, { method, headers: finalHeaders, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json();
  else data = await response.text();
  if (expect && response.status !== expect) {
    throw new Error(method + ' ' + requestPath + ' expected ' + expect + ' got ' + response.status + ': ' + JSON.stringify(data).slice(0, 500));
  }
  return { status: response.status, data, headers: response.headers };
}

async function main() {
  const health = await request('GET', '/api/health', { expect: 200 });
  check(health.data.ok !== undefined, 'health responds with ok');
  check(!('base_url' in health.data) && !('model' in health.data), 'health does not leak provider details');

  const openapi = await request('GET', '/openapi.json', { expect: 200 });
  check(String(openapi.data.openapi || '').startsWith('3.'), 'openapi document is version 3.x');

  const idempotencyKey = 'smoke-' + randomUUID();
  const created = await request('POST', '/api/plan', {
    body: { intent: '你好，简单介绍一下你能做什么' },
    headers: { 'Idempotency-Key': idempotencyKey },
    expect: 200,
  });
  const plan = created.data.plan_graph;
  check(Boolean(plan?.id), 'plan creation returns a plan id');
  check(Array.isArray(plan?.nodes) && plan.nodes.length > 0, 'plan has nodes');

  const replay = await request('POST', '/api/plan', {
    body: { intent: '你好，简单介绍一下你能做什么' },
    headers: { 'Idempotency-Key': idempotencyKey },
    expect: 200,
  });
  check(replay.data.plan_graph.id === plan.id, 'Idempotency-Key returns the same plan');

  const list = await request('GET', '/api/plans?limit=20', { expect: 200 });
  check(list.data.plans.some((item) => item.id === plan.id), 'plan appears in the library');

  const fetched = await request('GET', '/api/plans/' + plan.id, { expect: 200 });
  check(fetched.data.plan_graph.id === plan.id, 'plan detail is readable');

  const paused = await request('POST', '/api/plans/' + plan.id + '/pause', { expect: 200 });
  check(paused.data.paused === true, 'plan pause is accepted');
  const resumed = await request('POST', '/api/plans/' + plan.id + '/resume', { expect: 200 });
  check(resumed.data.paused === false, 'plan resume is accepted');

  const markdown = await request('GET', '/api/plans/' + plan.id + '/export?format=md', { expect: 200 });
  check(String(markdown.data).includes('## Nodes'), 'markdown export contains nodes');
  const zip = await request('GET', '/api/plans/' + plan.id + '/export?format=zip', { expect: 200 });
  check(
    (Buffer.isBuffer(zip.data) && zip.data.subarray(0, 2).toString() === 'PK') ||
      (typeof zip.data === 'string' && zip.data.startsWith('PK')),
    'zip export returns a zip archive',
  );

  const metrics = await fetch(BASE + '/api/metrics', { headers: { Authorization: TOKEN ? 'Bearer ' + TOKEN : '' } });
  const metricsText = await metrics.text();
  check(metrics.status === 200 && metricsText.includes('http_requests_total'), 'metrics endpoint exposes request counters');

  const form = new FormData();
  form.append('file', new Blob(['# smoke\nline\n']), 'smoke.md');
  const upload = await request('POST', '/api/uploads', { body: form, expect: 201 });
  check(Boolean(upload.data.id), 'upload returns an id');
  const dedupe = await request('POST', '/api/uploads', { body: (() => { const again = new FormData(); again.append('file', new Blob(['# smoke\nline\n']), 'smoke.md'); return again; })(), expect: 200 });
  check(dedupe.data.deduplicated === true && dedupe.data.id === upload.data.id, 'duplicate upload deduplicates');
  await request('DELETE', '/api/uploads/' + upload.data.id, { expect: 200 });

  const deleted = await request('DELETE', '/api/sessions/' + encodeURIComponent(plan.session_id), { expect: 200 });
  check(deleted.data.deleted >= 1, 'session delete removes plans');

  const hugeIntent = 'a'.repeat(30000);
  const tooLong = await request('POST', '/api/plan', { body: { intent: hugeIntent } });
  check(tooLong.status === 400, 'oversized intent is rejected with 400');

  if (failures > 0) {
    process.stdout.write('\n' + failures + ' smoke check(s) failed\n');
    process.exit(1);
  }
  process.stdout.write('\nall smoke checks passed\n');
}

main().catch((error) => {
  console.error('smoke failed: ' + error.message);
  process.exit(1);
});
