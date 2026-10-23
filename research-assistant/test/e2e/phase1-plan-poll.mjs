import fs from 'node:fs';

const BASE = process.env.E2E_BASE || 'http://backend:8080';
const TOKEN = process.env.E2E_TOKEN || '';
const USER = process.env.E2E_USER || 'phase1-e2e';
const SESSION = process.env.E2E_SESSION || 'phase1-session';
const PLAN_ID = process.env.E2E_PLAN_ID;
const OUT = process.env.E2E_OUT || '/reports/phase1-paper-plan.json';
const TIMEOUT_MS = Number(process.env.E2E_TIMEOUT_MS || 2400000);

const headers = {
  ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
  'X-User-Id': USER,
  'X-Session-Id': SESSION,
};

async function getPlan() {
  const response = await fetch(`${BASE}/api/plans/${PLAN_ID}`, { headers });
  if (!response.ok) throw new Error(`plan ${response.status}: ${await response.text()}`);
  return (await response.json()).plan_graph;
}

async function getEvents() {
  const response = await fetch(`${BASE}/api/plans/${PLAN_ID}/events`, { headers });
  if (!response.ok) throw new Error(`events ${response.status}`);
  return (await response.json()).events ?? [];
}

async function main() {
  const startedAt = Date.now();
  let plan = await getPlan();
  let lastCount = -1;
  while (Date.now() - startedAt < TIMEOUT_MS) {
    plan = await getPlan();
    const done = plan.nodes.filter((node) => ['completed', 'failed', 'skipped', 'canceled'].includes(node.status)).length;
    if (done !== lastCount) {
      lastCount = done;
      console.log(`[${new Date().toISOString()}] status=${plan.status} nodes=${done}/${plan.nodes.length}`);
      for (const node of plan.nodes) {
        if (node.status === 'in_progress' || node.status === 'failed') {
          console.log(`  - ${node.status}: ${node.name}${node.error ? ' :: ' + node.error.slice(0, 200) : ''}`);
        }
      }
    }
    if (['completed', 'failed', 'canceled'].includes(plan.status)) break;
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }
  const events = await getEvents();
  const report = {
    plan_id: plan.id,
    status: plan.status,
    intent_type: plan.intent_type,
    user_intent: plan.user_intent,
    nodes: plan.nodes.map((node) => ({
      id: node.id, name: node.name, type: node.type, status: node.status,
      error: node.error || null, result_chars: (node.result || '').length,
      structured_chars: (node.structured_data || '').length,
    })),
    artifacts: Object.fromEntries(Object.entries(plan.artifacts || {}).map(([key, artifact]) => [key, {
      type: artifact.type, producer_task_id: artifact.producer_task_id, value: artifact.value,
    }])),
    event_count: events.length,
    event_types: [...new Set(events.map((event) => event.event_type))].sort(),
    finished_at: new Date().toISOString(),
  };
  fs.writeFileSync(OUT, JSON.stringify(report, null, 2));
  console.log(`PLAN_DONE status=${plan.status} events=${events.length} artifacts=${Object.keys(report.artifacts).length}`);
  const metrics = report.artifacts.run_metrics?.value || '';
  console.log('run_metrics:', metrics.slice(0, 400));
  const comparison = report.artifacts.comparison_report?.value || '';
  console.log('comparison_report:', comparison.slice(0, 400));
  const evidence = report.artifacts.claim_evidence_graph?.value || '';
  console.log('claim_evidence chars:', evidence.length);
  if (plan.status !== 'completed') process.exitCode = 2;
}

main().catch((error) => {
  console.error('PLAN_POLL_FAILED', error.message);
  process.exit(1);
});
