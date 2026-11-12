import assert from 'node:assert/strict';

/**
 * Online acceptance for the scheduler waiting / propagation / partial fixes.
 *
 * Two graphs are imported through the real API and inspected through the
 * persisted snapshot and event history, so the evidence is backend state rather
 * than a screenshot:
 *
 *  A. wait -> approve -> continue: a tool that requires approval parks the plan
 *     on awaiting_approval, its hard dependent stays pending (no upstream_failed),
 *     and after the approval both nodes complete.
 *  B. core + optional: the core tool succeeds, the optional tool fails, the
 *     report continues and the plan ends completed/partial with outcome_details.
 *
 * Usage: node research-assistant/test/scheduler-acceptance.mjs
 * Env:   REMEDIATION_BASE_URL (default http://127.0.0.1:18081)
 */

const base = process.env.REMEDIATION_BASE_URL || 'http://127.0.0.1:18081';

const me = await fetch(base + '/api/me');
assert.equal(me.status, 200, 'GET /api/me failed: ' + me.status);
const cookie = me.headers.getSetCookie().map((value) => value.split(';')[0]).join('; ');
const headers = { 'Content-Type': 'application/json', Cookie: cookie };

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function getPlan(planId) {
  const response = await fetch(base + '/api/plans/' + planId, { headers });
  assert.equal(response.status, 200, 'GET plan failed: ' + response.status);
  const body = await response.json();
  return body.plan_graph ?? body;
}

async function getEvents(planId) {
  const response = await fetch(base + '/api/plans/' + planId + '/events', { headers });
  assert.equal(response.status, 200, 'GET events failed: ' + response.status);
  const body = await response.json();
  return Array.isArray(body) ? body : body.events ?? [];
}

async function waitFor(planId, predicate, label, timeoutMs = 120000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await getPlan(planId);
    if (predicate(last)) return last;
    await sleep(200);
  }
  const summary = last && {
    status: last.status,
    outcome: last.outcome,
    reason: last.outcome_reason,
    nodes: (last.nodes ?? []).map((node) => [node.id, node.status, node.error]),
  };
  assert.fail('timed out waiting for ' + label + ': ' + JSON.stringify(summary));
}

async function importGraph(graph, title) {
  const response = await fetch(base + '/api/plans/from-graph', {
    method: 'POST',
    headers,
    body: JSON.stringify({ graph, title }),
  });
  const body = await response.text();
  assert.equal(response.status, 201, 'import failed: ' + response.status + ' ' + body);
  return JSON.parse(body).plan_graph;
}

async function deletePlan(planId) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await fetch(base + '/api/plans/' + planId, { method: 'DELETE', headers });
    if (response.status === 200) return;
    const body = await response.text();
    if (response.status === 409) {
      await fetch(base + '/api/plans/' + planId + '/cancel', {
        method: 'POST',
        headers,
        body: JSON.stringify({ reason: 'acceptance cleanup' }),
      });
      await sleep(200);
      continue;
    }
    assert.fail('delete failed: ' + response.status + ' ' + body);
  }
  assert.fail('plan could not be removed: ' + planId);
}

function toolNode(id, name, { toolId, args, dependencies = [], optional = false, allowedTools }) {
  return {
    id,
    name,
    type: 'tool_call',
    description: 'scheduler acceptance probe',
    assigned_to: 'tool_executor',
    status: 'pending',
    dependencies,
    required_artifacts: [],
    output_artifacts: ['tool_result'],
    parallelizable: true,
    priority: 0,
    retry_limit: 0,
    run_count: 0,
    execution_epoch: 0,
    timeout_seconds: 120,
    optional,
    contract: {
      version: 'v1',
      input_artifacts: [],
      output_artifacts: ['tool_result'],
      allowed_tools: allowedTools,
    },
    inputs: { tool_id: toolId, arguments: args },
  };
}

function controlNode(id, name, { dependencies = [], optionalDependencies = [], inputs = {} }) {
  return {
    id,
    name,
    type: 'ask_user_question',
    description: 'scheduler acceptance control node',
    assigned_to: 'general_agent',
    status: 'pending',
    dependencies,
    optional_dependencies: optionalDependencies,
    required_artifacts: [],
    output_artifacts: [],
    parallelizable: true,
    priority: 0,
    retry_limit: 0,
    run_count: 0,
    execution_epoch: 0,
    timeout_seconds: 120,
    contract: { version: 'v1', input_artifacts: [], output_artifacts: [], allowed_tools: ['conversation.respond'] },
    inputs: { question_prompt: 'continue?', question_answer: 'yes', ...inputs },
  };
}

const created = [];
const report = {};

try {
  // --- A: wait -> approve -> dependent continues ----------------------------
  const waitPlan = await importGraph(
    {
      user_intent: 'scheduler acceptance: waiting is not a failure',
      intent_type: 'Code_Execution',
      status: 'pending',
      artifacts: {},
      // Artifact keys must be unique per plan; the dependent emits its own key.
      nodes: [
        toolNode('wait-1', 'approval tool', {
          toolId: 'native:write_file',
          args: { path: 'acceptance.txt', content: 'scheduler acceptance' },
          allowedTools: ['native:write_file'],
        }),
        {
          ...controlNode('wait-2', 'dependent step', { dependencies: ['wait-1'] }),
          output_artifacts: ['dependent_answer'],
          contract: { version: 'v1', input_artifacts: [], output_artifacts: ['dependent_answer'], allowed_tools: ['conversation.respond'] },
        },
      ],
      edges: [{ id: 'e1', from: 'wait-1', to: 'wait-2', type: 'control' }],
      budget: { max_task_attempts: 20, max_duration_seconds: 600 },
    },
    'scheduler acceptance: waiting',
  );
  created.push(waitPlan.id);
  const executeResponse = await fetch(base + '/api/plans/' + waitPlan.id + '/execute', { method: 'POST', headers });
  assert.ok([200, 202].includes(executeResponse.status), 'execute failed: ' + executeResponse.status);

  const paused = await waitFor(waitPlan.id, (plan) => plan.status === 'awaiting_approval', 'awaiting_approval');
  const waitingNode = paused.nodes.find((node) => node.id === 'wait-1');
  const dependent = paused.nodes.find((node) => node.id === 'wait-2');
  assert.equal(waitingNode.status, 'awaiting_approval', 'upstream status=' + waitingNode.status);
  assert.equal(waitingNode.failure ?? null, null, 'waiting node carries a failure');
  assert.equal(waitingNode.finished_at ?? null, null, 'waiting node carries a finish time');
  assert.equal(waitingNode.error ?? '', '', 'waiting node carries an error');
  assert.equal(dependent.status, 'pending', 'dependent of a waiting task changed: ' + dependent.status);
  assert.equal(dependent.block_info ?? null, null, 'dependent of a waiting task was blocked');
  const pendingAction = (paused.runtime_actions ?? []).find((action) => action.status === 'pending');
  assert.ok(pendingAction, 'no pending runtime action was recorded');
  const pausedEvents = await getEvents(waitPlan.id);
  assert.equal(pausedEvents.filter((event) => event.event_type === 'task_failed').length, 0, 'a wait published task_failed');
  assert.equal(pausedEvents.filter((event) => event.event_type === 'task_blocked').length, 0, 'a wait published task_blocked');
  report.A_WAIT = {
    planID: waitPlan.id,
    planStatus: paused.status,
    planOutcome: paused.outcome ?? '',
    upstream: waitingNode.status,
    dependent: dependent.status,
    dependentBlockInfo: dependent.block_info ?? null,
    pendingAction: { id: pendingAction.id, kind: pendingAction.kind },
    taskFailedEvents: 0,
    taskBlockedEvents: 0,
  };

  const decision = await fetch(base + '/api/plans/' + waitPlan.id + '/runtime-actions/' + pendingAction.id + '/decision', {
    method: 'POST',
    headers,
    body: JSON.stringify({ decision: 'approve', note: 'acceptance' }),
  });
  assert.equal(decision.status, 200, 'approve failed: ' + decision.status + ' ' + (await decision.text()));

  const resumed = await waitFor(
    waitPlan.id,
    (plan) => ['completed', 'failed', 'canceled'].includes(plan.status),
    'resume after approval',
  );
  const resumedNodes = Object.fromEntries(resumed.nodes.map((node) => [node.id, node.status]));
  const resumedEvents = await getEvents(waitPlan.id);
  assert.equal(resumedNodes['wait-1'], 'completed', 'approved tool did not complete');
  assert.equal(resumedNodes['wait-2'], 'completed', 'dependent did not continue after the approval');
  assert.equal(resumed.status, 'completed', 'resumed plan status=' + resumed.status + ' outcome=' + resumed.outcome + ' reason=' + resumed.outcome_reason);
  assert.ok(resumed.outcome, 'resumed plan has no outcome');
  assert.equal(resumedEvents.filter((event) => event.event_type === 'task_failed').length, 0, 'resumed run reported a task failure');
  report.A_APPROVED = {
    planID: waitPlan.id,
    planStatus: resumed.status,
    outcome: resumed.outcome,
    outcomeReason: resumed.outcome_reason,
    nodes: resumedNodes,
    taskFailedEvents: 0,
  };

  // --- B: core completed, optional failed -> partial ------------------------
  const partialPlan = await importGraph(
    {
      user_intent: 'scheduler acceptance: core accepted with an optional loss',
      intent_type: 'Code_Execution',
      status: 'pending',
      artifacts: {},
      acceptance: {
        version: 'acceptance/v1',
        goals: [{ id: 'g1', statement: 'core tool result produced', evidence_refs: ['tool_result'] }],
        required_artifacts: ['tool_result'],
      },
      nodes: [
        toolNode('core-1', 'core tool', { toolId: 'native:echo', args: { message: 'core' }, allowedTools: ['native:echo'] }),
        toolNode('opt-1', 'optional tool', {
          toolId: 'native:fail',
          args: { message: 'optional metric unavailable' },
          optional: true,
          allowedTools: ['native:fail'],
        }),
        controlNode('report-1', 'report', { dependencies: ['core-1'], optionalDependencies: ['opt-1'] }),
      ],
      edges: [
        { id: 'e1', from: 'core-1', to: 'report-1', type: 'control' },
        { id: 'e2', from: 'opt-1', to: 'report-1', type: 'optional' },
      ],
      budget: { max_task_attempts: 20, max_duration_seconds: 600 },
    },
    'scheduler acceptance: partial',
  );
  created.push(partialPlan.id);
  report.B_IMPORTED = {
    planID: partialPlan.id,
    acceptance: partialPlan.acceptance ?? null,
    optional: partialPlan.nodes.find((node) => node.id === 'opt-1')?.optional,
    optionalRelationEdge: (partialPlan.edges ?? []).find((edge) => edge.type === 'optional') ?? null,
  };

  const executePartial = await fetch(base + '/api/plans/' + partialPlan.id + '/execute', { method: 'POST', headers });
  assert.ok([200, 202].includes(executePartial.status), 'execute failed: ' + executePartial.status);
  partialPlanAction: for (let attempt = 0; attempt < 200; attempt += 1) {
    const plan = await getPlan(partialPlan.id);
    if (['completed', 'failed', 'canceled'].includes(plan.status)) break partialPlanAction;
    const action = (plan.runtime_actions ?? []).find((candidate) => candidate.status === 'pending');
    if (action) {
      await fetch(base + '/api/plans/' + partialPlan.id + '/runtime-actions/' + action.id + '/decision', {
        method: 'POST',
        headers,
        body: JSON.stringify({ decision: 'approve' }),
      });
    }
    await sleep(200);
  }
  const partial = await waitFor(partialPlan.id, (plan) => ['completed', 'failed', 'canceled'].includes(plan.status), 'partial terminal');
  const partialNodes = Object.fromEntries(partial.nodes.map((node) => [node.id, node.status]));
  assert.equal(partialNodes['core-1'], 'completed', 'core tool did not complete');
  assert.equal(partialNodes['opt-1'], 'failed', 'optional tool did not fail: ' + partialNodes['opt-1']);
  assert.equal(partialNodes['report-1'], 'completed', 'report did not continue after the optional failure');
  assert.equal(partial.status, 'completed', 'partial case status=' + partial.status + ' reason=' + partial.outcome_reason);
  assert.equal(partial.outcome, 'partial', 'partial case outcome=' + partial.outcome + ' reason=' + partial.outcome_reason);
  assert.ok(partial.outcome_details, 'partial outcome has no details');
  assert.ok((partial.outcome_details.optional_losses ?? []).length > 0, 'partial outcome lists no loss');
  assert.ok((partial.outcome_details.accepted_goals ?? []).length > 0, 'partial outcome lists no accepted goal');
  assert.equal(partial.outcome_details.optional_losses[0].task_id, 'opt-1', 'the loss does not name the optional task');
  const partialEvents = await getEvents(partialPlan.id);
  const terminal = partialEvents.filter((event) => ['plan_completed', 'plan_failed'].includes(event.event_type)).pop();
  assert.ok(terminal, 'no terminal event was recorded');
  assert.equal(terminal.payload?.outcome, 'partial', 'terminal event outcome=' + terminal.payload?.outcome);
  assert.ok(terminal.payload?.outcome_details, 'terminal event has no outcome details');
  report.B_PARTIAL = {
    planID: partialPlan.id,
    planStatus: partial.status,
    outcome: partial.outcome,
    outcomeReason: partial.outcome_reason,
    nodes: partialNodes,
    acceptedGoals: partial.outcome_details.accepted_goals,
    optionalLosses: partial.outcome_details.optional_losses,
    limitations: partial.outcome_details.limitations,
    terminalEventOutcome: terminal.payload?.outcome,
  };

  console.log(JSON.stringify({ result: 'PASS', ...report }, null, 2));
} finally {
  for (const planId of created) {
    try {
      await deletePlan(planId);
    } catch (error) {
      console.warn('cleanup failed for ' + planId + ': ' + error.message);
    }
  }
  console.error('acceptance plans removed');
}