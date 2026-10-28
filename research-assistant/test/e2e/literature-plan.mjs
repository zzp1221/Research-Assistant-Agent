#!/usr/bin/env node
// Planner literature_search node E2E:
//   RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/literature-plan.mjs
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const jars = new Map();
const jarFor = (user) => {
  if (!jars.has(user)) jars.set(user, new Map());
  return jars.get(user);
};
let failures = 0;
const check = (ok, message) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + message);
  if (!ok) failures++;
};

async function request(method, path, { body, headers = {}, user } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (user) finalHeaders['X-User-Id'] = user;
  const jar = jarFor(user || 'default');
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([key, value]) => key + '=' + value).join('; ');
  let payload = body;
  if (payload !== undefined && payload !== null && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) jar.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const contentType = response.headers.get('content-type') || '';
  const data = contentType.includes('application/json') ? await response.json() : await response.text();
  return { status: response.status, data };
}

async function main() {
  const user = 'e2e-plan-' + Math.random().toString(36).slice(2, 10);
  const response = await request('POST', '/api/plan', {
    user,
    headers: { 'Idempotency-Key': 'e2e-lit-plan-' + Math.random().toString(36).slice(2, 10) },
    body: {
      intent: '帮我找 5 篇关于 graph neural network 的综述并总结',
      runtime_permission_mode: 'full_access',
      runtime_permission_acknowledged: true,
    },
  });
  check(response.status === 200, 'plan: HTTP 200 (' + response.status + ')');
  const plan = response.data.plan_graph;
  const types = (plan?.nodes || []).map((node) => node.type);
  check(types.includes('literature_search'), 'plan: contains literature_search node (' + types.join(',') + ')');
  const search = (plan?.nodes || []).find((node) => node.type === 'literature_search');
  check(search?.assigned_to === 'librarian_agent', 'plan: literature_search assigned to librarian');
  check(String(search?.inputs?.query || '').length > 0, 'plan: search query frozen in inputs');
  if (plan?.id) {
    await request('DELETE', '/api/plans/' + plan.id, { user });
  }
  if (failures > 0) {
    console.log('\n' + failures + ' plan check(s) failed');
    process.exit(1);
  }
  console.log('\nall literature-plan checks passed');
}

main().catch((error) => {
  console.error('literature-plan e2e failed: ' + error.message);
  process.exit(1);
});
