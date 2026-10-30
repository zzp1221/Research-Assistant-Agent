#!/usr/bin/env node
// Plan revision E2E: exercises immutable snapshots, optimistic concurrency,
// running-plan protection and field/edge/budget diff against a live stack.
import { randomUUID } from 'node:crypto';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-plan-revisions-' + randomUUID().slice(0, 8);
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
  return { status: response.status, data };
}

async function createPlan(intent) {
  const body = typeof intent === 'string' ? { intent } : intent;
  const response = await request('POST', '/api/plan', { body });
  if (response.status !== 200) throw new Error('create plan failed: ' + response.status + ' ' + JSON.stringify(response.data).slice(0, 400));
  return response.data.plan_graph;
}

// Rebuild a plan graph payload from an API plan_graph, keeping only fields the
// revision endpoint needs to validate.
function graphPayload(plan) {
  return {
    id: plan.id,
    user_intent: plan.user_intent,
    intent_type: plan.intent_type,
    status: 'pending',
    nodes: (plan.nodes || []).map((node) => ({
      id: node.id,
      name: node.name,
      type: node.type,
      description: node.description,
      assigned_to: node.assigned_to,
      status: 'pending',
      dependencies: node.dependencies || [],
      required_artifacts: node.required_artifacts || [],
      output_artifacts: node.output_artifacts || [],
      inputs: node.inputs || {},
      timeout_seconds: node.timeout_seconds || 0,
      retry_limit: node.retry_limit || 0,
      contract: node.contract,
    })),
    edges: (plan.edges || []).map((edge) => ({ id: edge.id, from: edge.from, to: edge.to, type: edge.type })),
    artifacts: {},
    budget: plan.budget,
  };
}

async function main() {
  process.stdout.write('# plan-revisions e2e user=' + USER + '\n');
  const health = await request('GET', '/api/health');
  check(health.status === 200, 'health responds');

  const plan = await createPlan('用 Python 画一个正弦函数图像');
  check(Boolean(plan.id), 'created plan');

  // A1: initial revision exists and editing yields revision 2 without touching r1.
  const initial = await request('GET', '/api/plans/' + plan.id + '/revisions');
  check(initial.status === 200 && initial.data.revisions?.length === 1 && initial.data.revisions[0].revision === 1, 'initial revision 1 exists');

  const edited = graphPayload(plan);
  edited.nodes[0].name = 'E2E renamed node';
  const created = await request('POST', '/api/plans/' + plan.id + '/revisions', {
    body: { base_revision: 1, reason: 'e2e rename', graph: edited },
  });
  check(created.status === 200 && created.data.revision?.revision === 2, 'editing creates revision 2');

  const firstRevision = await request('GET', '/api/plans/' + plan.id + '/revisions/1');
  check(
    firstRevision.status === 200 && firstRevision.data.revision?.graph?.nodes?.[0]?.name !== 'E2E renamed node',
    'revision 1 remains unchanged after edit',
  );

  // A2: stale base revision returns 409 with the current revision.
  const stale = await request('POST', '/api/plans/' + plan.id + '/revisions', {
    body: { base_revision: 1, graph: graphPayload(plan) },
  });
  check(stale.status === 409 && stale.data.code === 'revision_conflict' && stale.data.current_revision === 2, 'stale base revision returns 409 + current revision');

  // A5: diff shows field, edge and budget changes.
  const diffBody = graphPayload(plan);
  diffBody.nodes[0].inputs = { ...(diffBody.nodes[0].inputs || {}), e2e_marker: 'changed' };
  diffBody.budget = { ...diffBody.budget, max_duration_seconds: (diffBody.budget?.max_duration_seconds || 600) + 60 };
  diffBody.edges = [];
  const base = await request('GET', '/api/plans/' + plan.id + '/revisions');
  const latest = base.data.revisions[base.data.revisions.length - 1].revision;
  const diffCreate = await request('POST', '/api/plans/' + plan.id + '/revisions', {
    body: { base_revision: latest, reason: 'e2e diff', graph: diffBody },
  });
  check(diffCreate.status === 200, 'third revision created');
  const diff = await request('GET', '/api/plans/' + plan.id + '/diff?from=1&to=' + diffCreate.data.revision.revision);
  const kinds = new Set((diff.data.diff || []).map((entry) => entry.kind));
  const fields = new Set((diff.data.diff || []).map((entry) => entry.field));
  check(diff.status === 200 && kinds.has('node_modified') && kinds.has('edge_removed') && kinds.has('budget_changed'), 'diff reports node/edge/budget changes');
  check(fields.has('inputs') && fields.has('max_duration_seconds'), 'diff reports field-level changes');

  // Validate endpoint.
  const validation = await request('POST', '/api/plans/' + plan.id + '/revisions/1/validate', { body: {} });
  check(validation.status === 200 && validation.data.valid === true, 'valid revision passes validation');

  // A3: a running plan cannot be edited or executed; after cancel it can.
  const running = await createPlan('运行一段 Python 代码并输出结果');
  const execute = await request('POST', '/api/plans/' + running.id + '/execute', { body: {} });
  check(execute.status === 202 || execute.status === 200 || execute.status === 409, 'execute plan accepted');
  const editRunning = await request('POST', '/api/plans/' + running.id + '/revisions', {
    body: { base_revision: 1, graph: graphPayload(running) },
  });
  check(editRunning.status === 409, 'editing a running plan returns 409');
  const execRunning = await request('POST', '/api/plans/' + running.id + '/revisions/1/execute', { body: {} });
  check(execRunning.status === 409, 'executing a running plan revision returns 409');
  await request('POST', '/api/plans/' + running.id + '/cancel', { body: {} });
  const afterCancel = await request('POST', '/api/plans/' + running.id + '/revisions/1/execute', { body: {} });
  check(afterCancel.status === 202, 'revision executes after cancel');

  // Executing a revision binds the plan to it and emits revision-tagged events.
  const eventPlan = await createPlan({ intent: '用 Python 画一个余弦函数图像' });
  const execCreated = await request('POST', '/api/plans/' + eventPlan.id + '/revisions', {
    body: { base_revision: 1, graph: graphPayload(eventPlan) },
  });
  const execNumber = execCreated.data.revision.revision;
  const execResponse = await request('POST', '/api/plans/' + eventPlan.id + '/revisions/' + execNumber + '/execute', { body: {} });
  check(execResponse.status === 202, 'revision 2 execution starts');
  const detail = await request('GET', '/api/plans/' + eventPlan.id);
  check(detail.data.plan_graph?.current_revision === execNumber, 'plan current_revision binds the executed revision');
  const events = await request('GET', '/api/plans/' + eventPlan.id + '/events');
  const executedEvent = (events.data.events || []).find((event) => event.event_type === 'plan_revision_executed');
  check(executedEvent?.revision === execNumber, 'execution event carries the revision number');
  await request('POST', '/api/plans/' + eventPlan.id + '/cancel', { body: {} });

  // A8: non-owner cannot read or edit.
  const anonymous = await fetch(BASE + '/api/plans/' + plan.id + '/revisions');
  check(anonymous.status === 401 || anonymous.status === 403, 'unauthenticated access rejected');

  process.stdout.write(failures === 0 ? '\nALL PASSED\n' : '\n' + failures + ' FAILED\n');
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});