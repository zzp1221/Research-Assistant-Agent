#!/usr/bin/env node
// E2E: complexity routing, General plan shapes and prompt-budget metrics.
// Usage: RA_API_URL=http://127.0.0.1:18080 RA_API_TOKEN=... node test/e2e/routing-and-prompts.mjs
import { randomUUID } from 'node:crypto';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-routing-user';

const cookies = new Map();
let failures = 0;
function check(condition, message) {
  if (condition) process.stdout.write('ok   ' + message + '\n');
  else {
    failures++;
    process.stdout.write('FAIL ' + message + '\n');
  }
}

async function request(method, requestPath, { body, expect } = {}) {
  const headers = { Accept: 'application/json', 'X-User-Id': USER };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) headers.Cookie = [...cookies.entries()].map(([key, value]) => key + '=' + value).join('; ');
  let payload = body;
  if (payload !== undefined && payload !== null && typeof payload !== 'string') {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + requestPath, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const contentType = response.headers.get('content-type') || '';
  const data = contentType.includes('application/json') ? await response.json() : await response.text();
  if (expect && response.status !== expect) {
    throw new Error(method + ' ' + requestPath + ' expected ' + expect + ' got ' + response.status + ': ' + JSON.stringify(data).slice(0, 500));
  }
  return { status: response.status, data };
}

async function createPlan(intent) {
  const created = await request('POST', '/api/plan', {
    body: { intent },
    expect: 200,
  });
  return created.data.plan_graph;
}

function nodeTypes(plan) {
  return (plan.nodes || []).map((node) => node.type);
}

async function main() {
  const health = await request('GET', '/api/health', { expect: 200 });
  check(health.data.ok !== undefined, 'health responds');

  // 1. Direct General plan: 1 node, fast path.
  const direct = await createPlan('你好');
  check(nodeTypes(direct).length === 1, 'direct General plan has exactly 1 node');
  check(nodeTypes(direct)[0] === 'general_synthesis', 'direct General node is general_synthesis');
  check(direct.meta?.general_plan_shape === 'direct', 'direct General shape recorded');
  check(direct.meta?.complexity === 'L0', 'direct General complexity is L0');
  check(direct.meta?.planning_route === 'fast_path', 'direct General route is fast_path');
  check(direct.meta?.template_variant === 'simple', 'direct General variant is simple');

  // 2. Research General plan: 2 nodes, standard template.
  const research = await createPlan('总结 RAG 技术的最新进展');
  const researchTypes = nodeTypes(research);
  check(researchTypes.length === 2, 'research General plan has exactly 2 nodes');
  check(researchTypes[0] === 'general_research' && researchTypes[1] === 'general_synthesis', 'research General node order is research -> synthesis');
  check(research.meta?.general_plan_shape === 'research', 'research General shape recorded');
  check(research.meta?.complexity === 'L2', 'research General complexity is L2');
  check(research.meta?.planning_route === 'standard_template', 'research General route is standard_template');

  // 3. Simple code plan: 3 nodes, fast path.
  const code = await createPlan('用 Python 计算 1+1');
  const codeTypes = nodeTypes(code);
  check(codeTypes.length === 3, 'simple code plan has exactly 3 nodes');
  check(codeTypes.join(',') === 'generate_code,execute_code,verify_result', 'simple code node order');
  check(code.meta?.complexity === 'L1', 'simple code complexity is L1');
  check(code.meta?.planning_route === 'fast_path', 'simple code route is fast_path');
  check(code.meta?.template_variant === 'simple', 'simple code variant is simple');

  // 4. Paper analysis keeps the deterministic four-node shape.
  const paper = await createPlan('分析一下这篇论文的主要创新点和局限性');
  const paperTypes = nodeTypes(paper);
  check(paperTypes.length === 4, 'paper analysis plan has exactly 4 nodes');
  check(paperTypes.join(',') === 'paper_parse,general_research,verify_result,general_synthesis', 'paper analysis node order');
  check(paper.meta?.general_plan_shape === 'paper', 'paper analysis shape recorded');
  check(paper.meta?.complexity === 'L2', 'paper analysis complexity is L2');

  // 5. Plan detail exposes the same metadata.
  const detail = await request('GET', '/api/plans/' + research.id, { expect: 200 });
  check(detail.data.plan_graph?.meta?.complexity === 'L2', 'plan detail exposes complexity');
  check(detail.data.plan_graph?.meta?.planning_route === 'standard_template', 'plan detail exposes planning route');
  check(detail.data.plan_graph?.meta?.template_variant === 'standard', 'plan detail exposes template variant');

  // 6. Metrics expose routing counters.
  const metrics = await request('GET', '/api/metrics', { expect: 200 });
  const body = String(metrics.data);
  check(body.includes('plan_route_total'), 'metrics expose plan_route_total');
  check(body.includes('plan_general_shape_total'), 'metrics expose plan_general_shape_total');
  check(body.includes('plan_llm_nodes_total'), 'metrics expose plan_llm_nodes_total');

  if (failures > 0) {
    process.stdout.write('\n' + failures + ' check(s) failed\n');
    process.exit(1);
  }
  process.stdout.write('\nrouting-and-prompts e2e passed\n');
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
