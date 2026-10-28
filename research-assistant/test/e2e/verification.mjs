#!/usr/bin/env node
// Scenario A-D E2E for the generic visual verification pipeline.
// Run inside the compose network with the mock-vision profile enabled:
//   RA_API_URL=http://backend:8080 MOCK_VISION_URL=http://mock-vision:9099 \
//   RA_API_TOKEN=... ADMIN_TOKEN=... node test/e2e/verification.mjs
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

async function createProfile(user, payload) {
  const response = await request('POST', '/api/model-profiles', { user, body: payload });
  if (response.status !== 201) throw new Error('create profile failed: ' + JSON.stringify(response.data));
  return response.data;
}

async function setRouting(user, mainId, visionIds) {
  const response = await request('PUT', '/api/model-routing', { user, body: { main_profile_id: mainId, vision_profile_ids: visionIds } });
  if (response.status !== 200) throw new Error('routing failed: ' + JSON.stringify(response.data));
}

async function createPlan(user, intent) {
  const response = await request('POST', '/api/plan', {
    user,
    headers: { 'Idempotency-Key': 'e2e-' + randomUUID() },
    body: { intent, runtime_permission_mode: 'full_access', runtime_permission_acknowledged: true },
  });
  if (response.status !== 200) throw new Error('plan creation failed: ' + JSON.stringify(response.data).slice(0, 400));
  return response.data.plan_graph;
}

async function waitForTerminal(planId, user, timeoutMs = 420000) {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const detail = await request('GET', '/api/plans/' + planId, { user });
    const plan = detail.data.plan_graph;
    if (['completed', 'failed', 'canceled'].includes(plan.status)) return plan;
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  throw new Error('plan did not reach a terminal state within ' + timeoutMs + 'ms');
}

async function execute(planId, user) {
  const response = await request('POST', '/api/plans/' + planId + '/execute', { user, body: { trigger: 'manual' } });
  if (response.status !== 200 && response.status !== 202) throw new Error('execute failed: ' + response.status + ' ' + JSON.stringify(response.data));
}

function imageArtifacts(plan) {
  const artifacts = plan.artifacts || {};
  return Object.values(artifacts).filter((artifact) =>
    String(artifact.key || '').includes('image') || String(artifact.key || '').includes('plot'));
}

// Artifacts whose metadata proves the verification badge was stamped.
function stampedImageArtifacts(plan) {
  return imageArtifacts(plan).filter((artifact) => Boolean(artifact?.metadata?.verification?.status));
}

async function scenario({ name, mainSupportsVision, mainModel = 'mock-main', visionModel, expectStatus }) {
  const user = 'e2e-verify-' + randomUUID().slice(0, 8);
  const main = await createProfile(user, {
    name: 'E2E Main', provider: 'openai_compatible', base_url: MODEL_BASE, model: mainModel,
    api_key: 'sk-main-' + randomUUID().slice(0, 6), roles: ['main'],
    supports_vision: mainSupportsVision ? 'yes' : 'no',
  });
  let visionId = '';
  if (visionModel) {
    const vision = await createProfile(user, {
      name: 'E2E Vision', provider: 'openai_compatible', base_url: MODEL_BASE, model: visionModel,
      api_key: 'sk-vision-' + randomUUID().slice(0, 6), roles: ['vision'], supports_vision: 'yes',
    });
    visionId = vision.id;
  }
  await setRouting(user, main.id, visionId ? [visionId] : []);

  const plan = await createPlan(user, '用 Python 生成一个包含两条序列的折线图并保存为 PNG，随后总结图表内容');
  await execute(plan.id, user);
  const finished = await waitForTerminal(plan.id, user);
  const verification = finished.verification;
  const artefactCount = imageArtifacts(finished).length;
  console.log('     [' + name + '] plan status=' + finished.status + ' verification=' + (verification ? verification.status : 'none') + ' images=' + artefactCount);
  check(finished.status === 'completed', name + ': plan completed (' + finished.status + ')');
  check(Boolean(verification), name + ': plan.verification present');
  if (verification) {
    check(verification.status === expectStatus, name + ': verification status=' + verification.status + ' (expected ' + expectStatus + ')');
    check(typeof verification.verified === 'boolean', name + ': verification.verified present');
    check(typeof verification.level === 'string', name + ': verification.level present');
    if (expectStatus !== 'skipped') {
      check(Array.isArray(verification.image_sha256) && verification.image_sha256.length > 0, name + ': image sha256 recorded');
    }
    if (expectStatus === 'inconclusive') {
      check(String(verification.reason || '').length > 0, name + ': inconclusive reason recorded');
    }
    if (expectStatus === 'fail') {
      check((verification.issues || []).length > 0, name + ': fail issues recorded');
    }
    if (expectStatus === 'pass') {
      check(Boolean(verification.verified_by?.profile_id), name + ': verified_by profile recorded');
    }
  }
  // The verify node carries the same verdict.
  const verifyNode = (finished.nodes || []).find((node) => node.type === 'verify_result');
  check(Boolean(verifyNode && verifyNode.verification), name + ': verify node carries verification');
  // Image artifacts carry the per-artifact badge metadata.
  const stamped = stampedImageArtifacts(finished);
  check(stamped.length > 0, name + ': at least one image artifact carries a verification badge (' + stamped.length + '/' + imageArtifacts(finished).length + ')');
  // Events include a verification event.
  const events = await request('GET', '/api/plans/' + plan.id + '/events', { user });
  const eventTypes = (events.data.events || []).map((event) => event.event_type);
  check(eventTypes.includes('verification_completed') || eventTypes.includes('verification_failed'), name + ': verification event emitted (' + eventTypes.filter((type) => type.startsWith('verification')).join(',') + ')');
  return finished;
}

async function main() {
  if (!MODEL_BASE) {
    console.error('MOCK_VISION_URL is required');
    process.exit(2);
  }
  console.log('Scenario A: main model has vision');
  // Scenario A needs a genuinely multimodal main profile, so it points at the
  // vision-capable mock model (a text-only model mislabeled as vision-capable
  // must degrade to inconclusive instead of pretending to pass).
  await scenario({ name: 'A', mainSupportsVision: true, mainModel: 'mock-vision', visionModel: '', expectStatus: 'pass' });

  console.log('Scenario B: main model is text-only, vision fallback judges');
  const scenarioB = await scenario({ name: 'B', mainSupportsVision: false, visionModel: 'mock-vision', expectStatus: 'pass' });
  const verifyB = (scenarioB.nodes || []).find((node) => node.type === 'verify_result');
  check(verifyB?.verification?.verified_by?.mode === 'vision_fallback', 'B: verdict came from the fallback vision profile');

  console.log('Scenario C: no vision model configured');
  await scenario({ name: 'C', mainSupportsVision: false, visionModel: '', expectStatus: 'inconclusive' });

  console.log('Scenario D: vision model rejects the figure');
  await scenario({ name: 'D', mainSupportsVision: false, visionModel: 'mock-fail', expectStatus: 'fail' });

  if (failures > 0) {
    console.log('\n' + failures + ' verification check(s) failed');
    process.exit(1);
  }
  console.log('\nall verification scenarios passed');
}

main().catch((error) => {
  console.error('verification e2e failed: ' + error.message);
  process.exit(1);
});