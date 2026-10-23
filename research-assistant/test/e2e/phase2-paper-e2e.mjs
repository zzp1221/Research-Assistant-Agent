import fs from 'node:fs';

const BASE = process.env.E2E_BASE || 'http://backend:8080';
const TOKEN = process.env.E2E_TOKEN || '';
const USER = process.env.E2E_USER || 'phase2-paper';
const SESSION = process.env.E2E_SESSION || 'phase2-paper-session';
const OUT = process.env.E2E_OUT || '/reports/phase2-paper-plan.json';
const TIMEOUT_MS = Number(process.env.E2E_TIMEOUT_MS || 2400000);
const INTENT = process.env.E2E_INTENT || '请复现论文 Apriori: Mining Association Rules 的频繁项集算法，使用 https://github.com/asaini/Apriori 仓库，采用 smoke 模式只运行仓库自带的 INTEGRATED-DATASET.csv，输出频繁项集和关联规则，不要下载额外数据集。';

const headers = {
  ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
  'X-User-Id': USER,
  'X-Session-Id': SESSION,
};
const jsonHeaders = { ...headers, 'Content-Type': 'application/json' };

async function call(method, url, body) {
  const response = await fetch(BASE + url, {
    method,
    headers: jsonHeaders,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  if (!response.ok) throw new Error(`${method} ${url} -> ${response.status}: ${text.slice(0, 500)}`);
  return text ? JSON.parse(text) : null;
}

async function main() {
  const created = await call('POST', '/api/plan', { intent: INTENT, attachments: [] });
  const plan = created.plan_graph;
  console.log('[ok] plan', plan.id, plan.intent_type, plan.nodes.length, 'nodes');
  if (plan.intent_type !== 'Paper_Reproduction') throw new Error('unexpected intent type ' + plan.intent_type);
  if (plan.approval?.required) {
    await call('POST', `/api/plans/${plan.id}/approve`, {});
  }
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
        if (node.status === 'failed') console.log('  failed:', node.type, (node.error || '').slice(0, 240));
      }
      last = done;
    }
    if (['completed', 'failed', 'canceled'].includes(current.status)) break;
    await new Promise((resolve) => setTimeout(resolve, 3000));
  }

  const artifacts = Object.fromEntries(
    Object.entries(current.artifacts || {}).map(([key, artifact]) => [key, {
      type: artifact.type,
      producer_task_id: artifact.producer_task_id,
      value: artifact.value,
    }]),
  );
  const report = {
    plan_id: current.id,
    status: current.status,
    intent_type: current.intent_type,
    user_intent: current.user_intent,
    nodes: current.nodes.map((node) => ({
      id: node.id,
      name: node.name,
      type: node.type,
      status: node.status,
      error: node.error || null,
      result_chars: (node.result || '').length,
      structured_chars: (node.structured_data || '').length,
    })),
    artifacts,
    finished_at: new Date().toISOString(),
  };
  fs.writeFileSync(OUT, JSON.stringify(report, null, 2));
  const metrics = artifacts.run_metrics?.value || '';
  const comparison = artifacts.comparison_report?.value || '';
  const evidence = artifacts.claim_evidence_graph?.value || '';
  console.log('PAPER_DONE', current.status, 'artifacts:', Object.keys(artifacts).length);
  console.log('run_metrics:', metrics.slice(0, 500));
  console.log('comparison_report:', comparison.slice(0, 500));
  console.log('claim_evidence chars:', evidence.length);
  if (current.status !== 'completed') process.exitCode = 2;
}

main().catch((error) => {
  console.error('PAPER_FAILED', error.message);
  process.exit(1);
});
