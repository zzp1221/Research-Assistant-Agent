#!/usr/bin/env node
// End-to-end checks for runtime model profiles + advisory visual verification.
// Run inside the compose network: node test/e2e/model-verification-e2e.mjs
import { randomUUID } from 'node:crypto';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const MODEL_BASE = (process.env.MOCK_VISION_URL || '').replace(/\/$/, '');
const ADMIN_TOKEN = (process.env.ADMIN_TOKEN || '').trim();

// Each logical principal keeps its own signed-cookie jar: the backend derives
// identity exclusively from the server-signed HttpOnly cookie, so sharing one
// jar across users would collapse them into a single principal.
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
  if (TOKEN && !finalHeaders.Authorization) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (user) finalHeaders['X-User-Id'] = user;
  const jar = jarFor(user || 'default');
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([key, value]) => key + '=' + value).join('; ');
  let payload = body;
  if (payload !== undefined && payload !== null && typeof payload !== 'string' && !(payload instanceof FormData)) {
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
  if (!MODEL_BASE) {
    console.error('MOCK_VISION_URL is required');
    process.exit(2);
  }
  const alice = 'e2e-alice-' + randomUUID().slice(0, 8);
  const bob = 'e2e-bob-' + randomUUID().slice(0, 8);

  // 1. Profile CRUD, masking, ownership isolation
  const created = await request('POST', '/api/model-profiles', {
    user: alice,
    body: { name: 'Alice Main', provider: 'openai_compatible', base_url: MODEL_BASE, model: 'mock-main', api_key: 'sk-alice-secret-0001', roles: ['main'], supports_vision: 'no' },
  });
  check(created.status === 201, 'alice can create a profile (' + created.status + ')');
  const aliceProfile = created.data;
  check(Boolean(aliceProfile?.id), 'created profile returns an id');
  check(!JSON.stringify(created.data).includes('sk-alice-secret-0001'), 'create response does not echo the api key');
  check(aliceProfile.api_key_masked === '****0001', 'api key is masked to the last four characters');

  const bobList = await request('GET', '/api/model-profiles', { user: bob });
  check(!(bobList.data.profiles || []).some((profile) => profile.id === aliceProfile.id), 'bob cannot see alice profiles');

  const bobUpdate = await request('PUT', '/api/model-profiles/' + aliceProfile.id, { user: bob, body: { name: 'hijacked' } });
  check(bobUpdate.status === 404, 'bob cannot update alice profile (' + bobUpdate.status + ')');

  // 2. Global profile requires an admin token
  const denied = await request('POST', '/api/model-profiles', {
    user: alice,
    body: { name: 'Global ' + randomUUID().slice(0, 6), provider: 'openai_compatible', base_url: MODEL_BASE, model: 'mock-global', api_key: 'sk-global-9999', global: true },
  });
  check(denied.status === 403, 'non-admin cannot create a global profile (' + denied.status + ')');
  const adminHeaders = ADMIN_TOKEN ? { 'X-Admin-Token': ADMIN_TOKEN } : {};
  const globalCreated = await request('POST', '/api/model-profiles', {
    user: alice,
    headers: adminHeaders,
    body: { name: 'Global Vision ' + randomUUID().slice(0, 6), provider: 'openai_compatible', base_url: MODEL_BASE, model: 'mock-vision', api_key: 'sk-global-9999', roles: ['vision'], global: true },
  });
  check(globalCreated.status === 201, 'admin can create a global profile (' + globalCreated.status + ')');

  // 3. Text + vision probe against the mock provider
  const test = await request('POST', '/api/model-profiles/' + aliceProfile.id + '/test', { user: alice });
  check(test.status === 200 && test.data.ok === true, 'text probe succeeds (' + JSON.stringify(test.data).slice(0, 140) + ')');
  const probe = await request('POST', '/api/model-profiles/' + globalCreated.data.id + '/probe-vision', { user: alice });
  check(probe.status === 200 && probe.data.status === 'yes', 'vision probe detects red/blue (' + JSON.stringify(probe.data).slice(0, 140) + ')');
  if (probe.status !== 200 || probe.data.status !== 'yes') {
    const profiles = await request('GET', '/api/model-profiles', { user: alice });
    const globalProfile = (profiles.data.profiles || []).find((profile) => profile.id === globalCreated.data.id);
    console.log('     probe diagnostics: ' + JSON.stringify(globalProfile?.vision_probe_reason || ''));
  }

  // 4. Routing: main without vision, global vision as fallback
  const routing = await request('PUT', '/api/model-routing', {
    user: alice,
    body: { main_profile_id: aliceProfile.id, vision_profile_ids: [globalCreated.data.id] },
  });
  check(routing.status === 200, 'alice can save routing (' + routing.status + ')');
  const routingRead = await request('GET', '/api/model-routing', { user: alice });
  check(routingRead.data.routing?.main_profile_id === aliceProfile.id, 'routing is persisted');
  const deleteInUse = await request('DELETE', '/api/model-profiles/' + aliceProfile.id, { user: alice });
  check(deleteInUse.status === 409, 'a profile referenced by routing cannot be deleted (' + deleteInUse.status + ')');

  // 5. Key never leaks into the plan snapshot
  const plan = await request('POST', '/api/plan', {
    user: alice,
    headers: { 'Idempotency-Key': 'e2e-verify-' + randomUUID() },
    body: { intent: '画一个简单的时间序列折线图，并总结结果' },
  });
  check(plan.status === 200, 'alice can create a plan (' + plan.status + ')');
  const graph = plan.data.plan_graph;
  check(Boolean(graph.model_snapshot?.text?.profile_id), 'plan records the resolved text profile snapshot');
  check(!JSON.stringify(graph).includes('sk-alice-secret-0001'), 'plan snapshot does not contain the api key');
  const verifyNode = (graph.nodes || []).find((node) => node.type === 'verify_result');
  check(Boolean(verifyNode), 'plan contains a verify_result node');

  const detail = await request('GET', '/api/plans/' + graph.id, { user: alice });
  const detailText = JSON.stringify(detail.data);
  check(!detailText.includes('sk-alice-secret-0001'), 'plan detail does not contain the api key');
  check(detail.data.plan_graph.verification === undefined || detail.data.plan_graph.verification === null, 'new plan has no verification before execution');

  // 6. Legacy plan compatibility: a plan without verification reads normally
  check(Array.isArray(detail.data.plan_graph.nodes) && detail.data.plan_graph.nodes.length > 0, 'legacy-compatible plan detail is readable');

  if (failures > 0) {
    console.log('\n' + failures + ' model profile check(s) failed');
    process.exit(1);
  }
  console.log('\nall model profile checks passed');
}

main().catch((error) => {
  console.error('model profile e2e failed: ' + error.message);
  process.exit(1);
});