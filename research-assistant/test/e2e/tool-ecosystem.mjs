#!/usr/bin/env node
// N-13 tool ecosystem E2E. Runs against a live container stack.
// Usage:
//   RA_API_URL=http://127.0.0.1:18080 RA_API_TOKEN=... RA_ADMIN_TOKEN=... \
//     node test/e2e/tool-ecosystem.mjs
import { randomUUID } from 'node:crypto';
import { execSync } from 'node:child_process';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const ADMIN = (process.env.RA_ADMIN_TOKEN || '').trim();
const BACKEND_CONTAINER = process.env.RA_BACKEND_CONTAINER || 'research-assistant-backend-1';
const RUN = randomUUID().slice(0, 8);

let failures = 0;
const checks = [];
function check(condition, message, detail) {
  checks.push({ message, ok: Boolean(condition), detail });
  if (condition) {
    process.stdout.write('ok   ' + message + '\n');
  } else {
    failures++;
    process.stdout.write('FAIL ' + message + (detail ? ' :: ' + JSON.stringify(detail).slice(0, 500) : '') + '\n');
  }
}

class Client {
  constructor(label) {
    this.label = label;
    this.cookies = new Map();
  }

  async request(method, requestPath, { body, headers = {}, expect, admin = false } = {}) {
    const finalHeaders = { Accept: 'application/json', 'X-User-Id': this.label, ...headers };
    if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
    if (admin && ADMIN) finalHeaders['X-Admin-Token'] = ADMIN;
    if (this.cookies.size) {
      finalHeaders.Cookie = [...this.cookies.entries()].map(([key, value]) => key + '=' + value).join('; ');
    }
    let payload = body;
    if (payload !== undefined && payload !== null && typeof payload !== 'string') {
      finalHeaders['Content-Type'] = 'application/json';
      payload = JSON.stringify(payload);
    }
    const response = await fetch(BASE + requestPath, { method, headers: finalHeaders, body: payload });
    for (const raw of response.headers.getSetCookie?.() || []) {
      const pair = raw.split(';')[0] || '';
      const index = pair.indexOf('=');
      if (index > 0) this.cookies.set(pair.slice(0, index), pair.slice(index + 1));
    }
    const contentType = response.headers.get('content-type') || '';
    let data;
    if (contentType.includes('application/json')) data = await response.json();
    else data = await response.text();
    if (expect && response.status !== expect) {
      throw new Error(`${method} ${requestPath} expected ${expect} got ${response.status}: ${JSON.stringify(data).slice(0, 700)}`);
    }
    return { status: response.status, data, headers: response.headers };
  }
}

const toolCallNode = (id, toolId, args, options = {}) => ({
  id,
  name: options.name || toolId,
  type: 'tool_call',
  assigned_to: options.assignedTo || 'tool_executor',
  status: 'pending',
  dependencies: [],
  required_artifacts: [],
  output_artifacts: ['tool_result'],
  parallelizable: false,
  priority: 0,
  retry_limit: 0,
  inputs: {
    tool_id: toolId,
    ...(options.version ? { tool_version: options.version } : {}),
    arguments: args,
  },
  contract: { version: 'v1', allowed_tools: options.allowedTools || [toolId] },
});

const llmNode = (id, assignedTo) => ({
  id,
  name: 'custom agent answer',
  type: 'general_process',
  assigned_to: assignedTo,
  status: 'pending',
  dependencies: [],
  required_artifacts: [],
  output_artifacts: [],
  parallelizable: false,
  priority: 0,
  retry_limit: 0,
  contract: { version: 'v1' },
});

const makeGraph = (nodes) => ({
  user_intent: 'N-13 tool ecosystem E2E',
  intent_type: 'General',
  nodes,
  edges: [],
});

async function importPlan(client, graph) {
  return client.request('POST', '/api/plans/from-graph', { body: { graph }, expect: 201 });
}

async function executePlan(client, planId) {
  const response = await client.request('POST', `/api/plans/${planId}/execute`);
  if (response.status !== 200 && response.status !== 202) {
    throw new Error(`execute ${planId} returned ${response.status}: ${JSON.stringify(response.data).slice(0, 400)}`);
  }
  return response;
}

async function waitPlan(client, planId, statuses, timeoutMs = 90000) {
  const wanted = new Set(statuses);
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    const response = await client.request('GET', `/api/plans/${planId}`);
    last = response.data?.plan_graph || response.data;
    if (wanted.has(last?.status)) return last;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`plan ${planId} stayed in status ${last?.status}; wanted ${[...wanted].join('/')}`);
}

async function toolCalls(client, planId) {
  const response = await client.request('GET', `/api/plans/${planId}/tool-calls`, { expect: 200 });
  return response.data.tool_calls || [];
}

function containerFileExists(relativePath) {
  try {
    execSync(`docker exec ${BACKEND_CONTAINER} test -f /tmp/research-assistant-tool-workspace/${relativePath}`, { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

function startMockMCPHTTP() {
  try {
    execSync(`docker exec ${BACKEND_CONTAINER} sh -lc 'pkill -f "mockmcp -http" || true'`, { stdio: 'ignore' });
  } catch {}
  execSync(`docker exec -d ${BACKEND_CONTAINER} /app/mockmcp -http :9100`, { stdio: 'ignore' });
}

function stopMockMCPHTTP() {
  try {
    execSync(`docker exec ${BACKEND_CONTAINER} sh -lc 'pkill -f "mockmcp -http" || true'`, { stdio: 'ignore' });
  } catch {}
}

async function main() {
  const a = new Client('e2e-tools-a-' + RUN);
  const b = new Client('e2e-tools-b-' + RUN);
  const createdPlans = [];
  const createdTools = [];
  const createdGrants = [];
  const createdProfiles = [];
  let mcpHTTPServerId = '';
  let promptName = 'e2e-prompt-' + RUN;

  try {
    const health = await a.request('GET', '/api/health', { expect: 200 });
    check(health.data && health.data.ok !== undefined, 'health responds');

    // ---- A3 builtin registry ----
    const toolsResponse = await a.request('GET', '/api/tools', { expect: 200 });
    const builtinIDs = (toolsResponse.data.tools || []).map((tool) => tool.id);
    check(builtinIDs.includes('native:echo'), 'A3 builtin native:echo is registered', builtinIDs);

    // ---- A2 unknown profile must not downgrade ----
    const badGraph = makeGraph([llmNode('n1', 'profile:unknown-' + RUN)]);
    const badPlan = await a.request('POST', '/api/plans/from-graph', { body: { graph: badGraph } });
    const badText = JSON.stringify(badPlan.data);
    check(badPlan.status === 400 && badText.includes('unknown agent profile'), 'A2 unknown profile returns 400 validation error', badPlan.data);

    // ---- A3 native tool plan ----
    const echoMessage = 'echo-' + RUN;
    const echoPlanResponse = await importPlan(a, makeGraph([toolCallNode('tool-1', 'native:echo', { message: echoMessage })]));
    const echoPlan = echoPlanResponse.data.plan_graph;
    createdPlans.push(echoPlan.id);
    await executePlan(a, echoPlan.id);
    const echoDone = await waitPlan(a, echoPlan.id, ['completed', 'failed']);
    check(echoDone.status === 'completed', 'A3 native:echo plan completes', echoDone.status);
    const echoCalls = await toolCalls(a, echoPlan.id);
    const echoRecord = echoCalls[0];
    check(Boolean(echoRecord) && echoRecord.status === 'completed', 'A3 ToolCallRecord completed', echoRecord);
    check(echoRecord && echoRecord.arguments && echoRecord.arguments.message === echoMessage, 'A3 ToolCallRecord keeps arguments');
    check(echoRecord && typeof echoRecord.duration_ms === 'number', 'A3 ToolCallRecord records duration');
    check(Boolean(echoDone.artifacts && echoDone.artifacts.tool_result && String(echoDone.artifacts.tool_result.value) === echoMessage), 'A3 tool_result artifact written');

    // ---- A1 whitelist enforcement ----
    const promptV1 = (await a.request('POST', '/api/prompt-versions', {
      body: { name: promptName, template: 'Reply with exactly the token PROMPT_V1 and nothing else.' },
      expect: 201,
    })).data.prompt;
    const promptV2 = (await a.request('POST', '/api/prompt-versions', {
      body: { name: promptName, template: 'Reply with exactly the token PROMPT_V2 and nothing else.' },
      expect: 201,
    })).data.prompt;
    check(promptV1.version === 1 && promptV2.version === 2, 'A8 prompt versions increment and are immutable', { v1: promptV1.version, v2: promptV2.version });
    const promptV1Reload = (await a.request('GET', `/api/prompt-versions/${promptV1.id}`, { expect: 200 })).data.prompt;
    check(promptV1Reload.template.includes('PROMPT_V1'), 'A8 v1 template unchanged after v2 creation');

    const profileResponse = await a.request('POST', '/api/agent-profiles', {
      body: {
        name: 'E2E Agent ' + RUN,
        prompt_version_id: promptV1.id,
        model_profile_id: 'env-bootstrap',
        tool_allowlist: ['native:echo'],
        max_steps: 4,
      },
      expect: 201,
    });
    const profile = profileResponse.data.profile;
    createdProfiles.push(profile.id);
    check(profile.id.startsWith('profile:'), 'A7 custom AgentProfile created', profile.id);
    const grant = await a.request('POST', '/api/tool-grants', {
      body: { agent_profile_id: profile.id, tool_id: 'native:echo' },
      expect: 201,
    });
    createdGrants.push(grant.data.grant.id);

    const forbiddenPlanResponse = await importPlan(a, makeGraph([
      toolCallNode('tool-1', 'native:write_file', { path: 'e2e-forbidden-' + RUN + '.txt', content: 'forbidden' }, {
        assignedTo: profile.id,
        allowedTools: ['native:write_file'],
      }),
    ]));
    const forbiddenPlan = forbiddenPlanResponse.data.plan_graph;
    createdPlans.push(forbiddenPlan.id);
    await executePlan(a, forbiddenPlan.id);
    const forbiddenDone = await waitPlan(a, forbiddenPlan.id, ['failed', 'completed']);
    const forbiddenCalls = await toolCalls(a, forbiddenPlan.id);
    check(forbiddenDone.status === 'failed' && forbiddenCalls[0]?.error_code === 'tool_forbidden', 'A1 profile allowlist returns tool_forbidden', { status: forbiddenDone.status, record: forbiddenCalls[0] });
    const forbiddenFile = containerFileExists('e2e-forbidden-' + RUN + '.txt');
    check(forbiddenFile === false, 'A1 forbidden call has no side effect', forbiddenFile);

    // ---- A6 approval: reject and approve ----
    const rejectPath = 'e2e-reject-' + RUN + '.txt';
    const rejectPlanResponse = await importPlan(a, makeGraph([
      toolCallNode('tool-1', 'native:write_file', { path: rejectPath, content: 'should-not-exist' }),
    ]));
    const rejectPlan = rejectPlanResponse.data.plan_graph;
    createdPlans.push(rejectPlan.id);
    await executePlan(a, rejectPlan.id);
    await waitPlan(a, rejectPlan.id, ['awaiting_approval']);
    const rejectActions = (await a.request('GET', `/api/plans/${rejectPlan.id}/runtime-actions`, { expect: 200 })).data.actions || [];
    const rejectAction = rejectActions.find((action) => action.kind === 'tool_call');
    check(Boolean(rejectAction) && rejectAction.status === 'pending', 'A6 side-effecting tool creates pending tool_call action', rejectAction);
    const pendingCalls = await toolCalls(a, rejectPlan.id);
    check(pendingCalls[0]?.status === 'pending', 'A6 pending ToolCallRecord exists before decision', pendingCalls[0]);
    await a.request('POST', `/api/plans/${rejectPlan.id}/runtime-actions/${rejectAction.id}/decision`, { body: { decision: 'reject' }, expect: 200 });
    await waitPlan(a, rejectPlan.id, ['failed']);
    const rejectedCalls = await toolCalls(a, rejectPlan.id);
    check(rejectedCalls[0]?.status === 'rejected' && rejectedCalls[0]?.approval_id === rejectAction.id, 'A6 reject records rejected ToolCallRecord with approval id', rejectedCalls[0]);
    check(containerFileExists(rejectPath) === false, 'A6 rejected call has no side effect');

    const approvePath = 'e2e-approve-' + RUN + '.txt';
    const approvePlanResponse = await importPlan(a, makeGraph([
      toolCallNode('tool-1', 'native:write_file', { path: approvePath, content: 'approved' }),
    ]));
    const approvePlan = approvePlanResponse.data.plan_graph;
    createdPlans.push(approvePlan.id);
    await executePlan(a, approvePlan.id);
    await waitPlan(a, approvePlan.id, ['awaiting_approval']);
    const approveActions = (await a.request('GET', `/api/plans/${approvePlan.id}/runtime-actions`, { expect: 200 })).data.actions || [];
    const approveAction = approveActions.find((action) => action.kind === 'tool_call');
    await a.request('POST', `/api/plans/${approvePlan.id}/runtime-actions/${approveAction.id}/decision`, { body: { decision: 'approve' }, expect: 200 });
    await waitPlan(a, approvePlan.id, ['completed', 'failed']);
    const approvedCalls = await toolCalls(a, approvePlan.id);
    check(approvedCalls[0]?.status === 'completed' && approvedCalls[0]?.approval_id === approveAction.id, 'A6 approve executes and links approval id', approvedCalls[0]);
    check(containerFileExists(approvePath) === true, 'A6 approved call writes the file');
    const approveEvents = (await a.request('GET', `/api/plans/${approvePlan.id}/events`, { expect: 200 })).data.events || [];
    const eventTypes = approveEvents.map((event) => event.event_type);
    check(eventTypes.includes('tool_call_completed') && eventTypes.includes('runtime_action_resolved'), 'A6 tool call and approval events are audited', eventTypes);

    // ---- A4 MCP stdio ----
    const stdioServerId = 'mcp:e2e-' + RUN;
    const stdioRegistration = await a.request('POST', '/api/tools', {
      body: {
        id: stdioServerId,
        name: 'E2E Mock MCP',
        transport: 'mcp_stdio',
        mcp: { id: stdioServerId, name: 'E2E Mock MCP', command: '/app/mockmcp', args: [] },
        scopes: [],
        side_effect: false,
        approval: 'never',
      },
      expect: 201,
    });
    const stdioTools = stdioRegistration.data.tools || [];
    createdTools.push(stdioServerId);
    const stdioToolIDs = stdioTools.map((tool) => tool.id);
    check(stdioToolIDs.includes(stdioServerId + '/echo'), 'A4 MCP stdio tools/list exposes echo', stdioToolIDs);

    const mcpEchoPlan = (await importPlan(a, makeGraph([
      toolCallNode('tool-1', stdioServerId + '/echo', { message: 'mcp-stdio-' + RUN }),
    ]))).data.plan_graph;
    createdPlans.push(mcpEchoPlan.id);
    await executePlan(a, mcpEchoPlan.id);
    const mcpEchoDone = await waitPlan(a, mcpEchoPlan.id, ['completed', 'failed']);
    check(mcpEchoDone.status === 'completed', 'A4 MCP stdio plan call succeeds', mcpEchoDone.status);

    const slowToolID = stdioServerId + '/slow';
    await a.request('PUT', `/api/tools/${encodeURIComponent(slowToolID)}`, { body: { timeout_sec: 1 }, expect: 200 });
    const slowPlan = (await importPlan(a, makeGraph([
      toolCallNode('tool-1', slowToolID, { ms: 3000 }),
    ]))).data.plan_graph;
    createdPlans.push(slowPlan.id);
    await executePlan(a, slowPlan.id);
    await waitPlan(a, slowPlan.id, ['failed']);
    const slowCalls = await toolCalls(a, slowPlan.id);
    check(slowCalls[0]?.error_code === 'tool_timeout', 'A4 MCP stdio timeout returns tool_timeout', slowCalls[0]);

    const crashPlan = (await importPlan(a, makeGraph([
      toolCallNode('tool-1', stdioServerId + '/crash', {}),
    ]))).data.plan_graph;
    createdPlans.push(crashPlan.id);
    await executePlan(a, crashPlan.id);
    await waitPlan(a, crashPlan.id, ['failed']);
    const crashCalls = await toolCalls(a, crashPlan.id);
    check(crashCalls[0]?.error_code === 'tool_unavailable', 'A4 MCP server exit returns tool_unavailable', crashCalls[0]);

    // ---- A5 MCP HTTP + SSRF ----
    let httpAvailable = true;
    try {
      startMockMCPHTTP();
      await new Promise((resolve) => setTimeout(resolve, 600));
    } catch (error) {
      httpAvailable = false;
      check(false, 'A5 mock MCP HTTP server could not start', String(error));
    }
    if (httpAvailable) {
      const blocked = await a.request('POST', '/api/tools', {
        body: {
          id: 'mcp:blocked-' + RUN,
          name: 'blocked',
          transport: 'mcp_http',
          mcp: { id: 'mcp:blocked-' + RUN, url: 'http://127.0.0.1:9100/mcp' },
        },
      });
      check(blocked.status === 400 && blocked.data.code === 'ssrf_blocked', 'A5 private MCP HTTP URL is rejected by SSRF policy', blocked.data);

      mcpHTTPServerId = 'mcp:http-e2e-' + RUN;
      const registered = await a.request('POST', '/api/tools', {
        body: {
          id: mcpHTTPServerId,
          name: 'E2E HTTP MCP',
          transport: 'mcp_http',
          mcp: { id: mcpHTTPServerId, name: 'E2E HTTP MCP', url: 'http://127.0.0.1:9100/mcp', allow_private_hosts: true },
          max_output_bytes: 1024,
          scopes: [],
          side_effect: false,
          approval: 'never',
        },
        admin: true,
        expect: 201,
      });
      createdTools.push(mcpHTTPServerId);
      const httpToolIDs = (registered.data.tools || []).map((tool) => tool.id);
      check(httpToolIDs.includes(mcpHTTPServerId + '/echo'), 'A5 MCP HTTP tools/list exposes echo', httpToolIDs);
      const bigTest = await a.request('POST', `/api/tools/${encodeURIComponent(mcpHTTPServerId + '/big')}/test`, {
        body: { arguments: { bytes: 4096 }, dry_run: true },
        expect: 200,
      });
      check(bigTest.data.record?.truncated === true && String(bigTest.data.output).includes('[truncated]'), 'A5 oversized MCP response is truncated and marked', bigTest.data.record);
    }

    // ---- A7 profile execution / behavior change / rollback ----
    const v1Plan = (await importPlan(a, makeGraph([llmNode('n1', profile.id)]))).data.plan_graph;
    createdPlans.push(v1Plan.id);
    await executePlan(a, v1Plan.id);
    const v1Done = await waitPlan(a, v1Plan.id, ['completed', 'failed'], 180000);
    const v1Node = v1Done.nodes?.[0];
    check(v1Done.status === 'completed' && String(v1Node?.result || '').includes('PROMPT_V1'), 'A7 profile v1 behavior is observable', { status: v1Done.status, result: v1Node?.result });
    check(v1Node?.metadata?.prompt_version === 1, 'A7 task metadata records prompt version 1', v1Node?.metadata);

    await a.request('PUT', `/api/agent-profiles/${profile.id}`, { body: { prompt_version_id: promptV2.id }, expect: 200 });
    const v2Plan = (await importPlan(a, makeGraph([llmNode('n1', profile.id)]))).data.plan_graph;
    createdPlans.push(v2Plan.id);
    await executePlan(a, v2Plan.id);
    const v2Done = await waitPlan(a, v2Plan.id, ['completed', 'failed'], 180000);
    check(v2Done.status === 'completed' && String(v2Done.nodes?.[0]?.result || '').includes('PROMPT_V2'), 'A7 switching to prompt v2 changes behavior', { status: v2Done.status, result: v2Done.nodes?.[0]?.result });

    await a.request('POST', `/api/prompt-versions/${promptV1.id}/activate`, { expect: 200 });
    await a.request('PUT', `/api/agent-profiles/${profile.id}`, { body: { prompt_version_id: promptV1.id }, expect: 200 });
    const rollbackPlan = (await importPlan(a, makeGraph([llmNode('n1', profile.id)]))).data.plan_graph;
    createdPlans.push(rollbackPlan.id);
    await executePlan(a, rollbackPlan.id);
    const rollbackDone = await waitPlan(a, rollbackPlan.id, ['completed', 'failed'], 180000);
    check(rollbackDone.status === 'completed' && String(rollbackDone.nodes?.[0]?.result || '').includes('PROMPT_V1'), 'A7 rollback to v1 restores behavior', { status: rollbackDone.status, result: rollbackDone.nodes?.[0]?.result });

    const evalResponse = await a.request('POST', `/api/prompt-versions/${promptV1.id}/eval`, {
      body: { cases: [{ name: 'default', variables: {}, expected_contains: 'PROMPT_V1' }] },
      expect: 200,
    });
    check(evalResponse.data.result?.score === 1, 'A7 prompt eval result is visible', evalResponse.data.result);
    const profileEval = await a.request('POST', `/api/agent-profiles/${profile.id}/eval`, { body: { cases: [] }, expect: 200 });
    check(profileEval.data.result?.total >= 1, 'A7 agent profile eval runs', profileEval.data.result);

    // ---- A8 snapshot/export ----
    const exportResponse = await a.request('GET', `/api/plans/${v2Plan.id}/export?format=json`, { expect: 200 });
    const exportText = typeof exportResponse.data === 'string' ? exportResponse.data : JSON.stringify(exportResponse.data);
    check(exportText.includes('agent_snapshot') && exportText.includes('prompt_version'), 'A8 export includes agent/prompt versions', exportText.slice(0, 300));
    const v2DetailResponse = await a.request('GET', `/api/plans/${v2Plan.id}`, { expect: 200 });
    const v2Detail = v2DetailResponse.data?.plan_graph || v2DetailResponse.data;
    check(Array.isArray(v2Detail.agent_snapshot) && v2Detail.agent_snapshot.length > 0, 'A8 plan snapshot records agent profile', v2Detail.agent_snapshot);

    // ---- A9 replay ----
    const dryReplay = await a.request('POST', `/api/plans/${echoPlan.id}/tool-calls/${echoRecord.id}/replay`, {
      body: { allow_side_effects: false },
      expect: 200,
    });
    check(dryReplay.data.dry_run === true && dryReplay.data.match === true, 'A9 dry-run replay matches result hash', dryReplay.data);
    const realRequest = await a.request('POST', `/api/plans/${echoPlan.id}/tool-calls/${echoRecord.id}/replay`, {
      body: { allow_side_effects: true },
    });
    check(realRequest.status === 202 && realRequest.data.status === 'pending_approval' && realRequest.data.approval_id, 'A9 real replay requires approval', realRequest.data);
    if (realRequest.status === 202 && realRequest.data.approval_id) {
      await a.request('POST', `/api/plans/${echoPlan.id}/runtime-actions/${realRequest.data.approval_id}/decision`, { body: { decision: 'approve' }, expect: 200 });
      const realReplay = await a.request('POST', `/api/plans/${echoPlan.id}/tool-calls/${echoRecord.id}/replay`, {
        body: { allow_side_effects: true, approval_id: realRequest.data.approval_id },
        expect: 200,
      });
      check(realReplay.data.replayed === true && realReplay.data.record?.status === 'completed', 'A9 approved real replay completes', realReplay.data);
    }

    // ---- A10 tenant isolation and secret redaction ----
    const secret = 'sk-e2e-secret-' + RUN;
    const secretToolID = 'http:e2e-' + RUN;
    await a.request('POST', '/api/tools', {
      body: {
        id: secretToolID,
        name: 'Secret HTTP tool',
        version: '1.0.0',
        transport: 'http',
        input_schema: '{"type":"object"}',
        http: { url: 'https://example.com/tool', headers: { Authorization: 'Bearer ' + secret } },
        scopes: [],
        side_effect: false,
        approval: 'never',
      },
      expect: 201,
    });
    createdTools.push(secretToolID);
    const aTools = await a.request('GET', '/api/tools', { expect: 200 });
    const aToolsText = JSON.stringify(aTools.data);
    check(aToolsText.includes(secretToolID), 'A10 owner sees own tool');
    check(!aToolsText.includes(secret), 'A10 tool list never returns the secret value');
    const bTools = await b.request('GET', '/api/tools', { expect: 200 });
    check(!JSON.stringify(bTools.data).includes(secretToolID), 'A10 other owner cannot see the tool');
    const bGet = await b.request('GET', `/api/tools/${encodeURIComponent(secretToolID)}`);
    check(bGet.status === 403, 'A10 other owner gets 403 for the tool', bGet.status);
    const secretExport = await a.request('GET', `/api/plans/${echoPlan.id}/export?format=json`, { expect: 200 });
    check(!String(secretExport.data).includes(secret), 'A10 plan export does not contain tool secrets');

    // ---- A8 rollback only moves the active pointer ----
    const prompts = (await a.request('GET', `/api/prompt-versions?name=${encodeURIComponent(promptName)}`, { expect: 200 })).data.prompts || [];
    const active = prompts.filter((prompt) => prompt.status === 'active');
    check(active.length === 1 && active[0].version === 1, 'A8 exactly one active prompt version after rollback', prompts.map((prompt) => ({ v: prompt.version, s: prompt.status })));
  } catch (error) {
    failures++;
    process.stdout.write('FAIL unexpected error: ' + (error && error.stack ? error.stack : String(error)) + '\n');
  } finally {
    // Best-effort cleanup so repeated runs do not accumulate test fixtures.
    for (const planId of createdPlans) {
      try { await a.request('DELETE', `/api/plans/${planId}`); } catch {}
    }
    for (const toolId of createdTools) {
      try { await a.request('DELETE', `/api/tools/${encodeURIComponent(toolId)}`); } catch {}
    }
    for (const grantId of createdGrants) {
      try { await a.request('DELETE', `/api/tool-grants/${grantId}`); } catch {}
    }
    for (const profileId of createdProfiles) {
      try { await a.request('DELETE', `/api/agent-profiles/${encodeURIComponent(profileId)}`); } catch {}
    }
    stopMockMCPHTTP();
  }

  process.stdout.write(`\n${checks.length - failures}/${checks.length} checks passed\n`);
  if (failures > 0) process.exit(1);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
