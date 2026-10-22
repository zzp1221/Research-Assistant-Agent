import fs from 'node:fs';

const BASE = process.env.E2E_BASE || 'http://backend:8080';
const TOKEN = process.env.E2E_TOKEN || '';
const USER = process.env.E2E_USER || 'phase1-auto';
const SESSION = process.env.E2E_SESSION || 'phase1-auto-session';
const OUT = process.env.E2E_OUT || '/reports/phase1-autoresearch.json';
const TIMEOUT_MS = Number(process.env.E2E_TIMEOUT_MS || 2400000);
const REPO = process.env.E2E_REPO || 'https://github.com/dorianbrown/rank_bm25';
const SPEC = process.env.E2E_SPEC || 'autoresearch.json';

const headers = {
  ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
  'X-User-Id': USER,
  'X-Session-Id': SESSION,
};
const jsonHeaders = { ...headers, 'Content-Type': 'application/json' };

async function call(method, url, body) {
  const response = await fetch(BASE + url, { method, headers: jsonHeaders, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await response.text();
  if (!response.ok) throw new Error(`${method} ${url} -> ${response.status}: ${text.slice(0, 500)}`);
  return text ? JSON.parse(text) : null;
}

async function main() {
  const intent = `用 ${REPO} 做 AutoResearch，按 ${SPEC} 运行，最多 3 轮，总时长不超过 15 分钟，最终验证 3 次。`;
  const created = await call('POST', '/api/plan', { intent, attachments: [] });
  const plan = created.plan_graph;
  console.log('[ok] plan', plan.id, plan.intent_type, plan.nodes.length, 'nodes');
  if (plan.intent_type !== 'AutoResearch') throw new Error('unexpected intent type ' + plan.intent_type);
  await call('POST', `/api/plans/${plan.id}/execute`, {});
  const startedAt = Date.now();
  let current = plan;
  let last = -1;
  while (Date.now() - startedAt < TIMEOUT_MS) {
    current = (await call('GET', `/api/plans/${plan.id}`)).plan_graph;
    const done = current.nodes.filter((node) => ['completed', 'failed', 'skipped', 'canceled'].includes(node.status)).length;
    if (done !== last) {
      console.log(`[${new Date().toISOString()}] ${current.status} ${done}/${current.nodes.length}`);
      for (const node of current.nodes) {
        if (node.status === 'failed') console.log('  failed:', node.type, node.error?.slice(0, 250));
      }
      last = done;
    }
    if (['completed', 'failed', 'canceled'].includes(current.status)) break;
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }
  const report = {
    plan_id: current.id,
    status: current.status,
    intent_type: current.intent_type,
    nodes: current.nodes.map((node) => ({ name: node.name, type: node.type, status: node.status, error: node.error || null })),
    artifacts: Object.fromEntries(Object.entries(current.artifacts || {}).map(([key, artifact]) => [key, { type: artifact.type, value: artifact.value }])),
    finished_at: new Date().toISOString(),
  };
  fs.writeFileSync(OUT, JSON.stringify(report, null, 2));
  const validation = report.artifacts.research_validation_report?.value || '';
  console.log('AUTORESEARCH_DONE', current.status, 'validation chars:', validation.length);
  console.log(validation.slice(0, 900));
  if (current.status !== 'completed') process.exitCode = 2;
}

main().catch((error) => {
  console.error('AUTORESEARCH_FAILED', error.message);
  process.exit(1);
});
