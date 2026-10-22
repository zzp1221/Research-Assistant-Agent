import fs from 'node:fs';
import path from 'node:path';

const BASE = process.env.E2E_BASE || 'http://backend:8080';
const TOKEN = process.env.E2E_TOKEN || '';
const USER = process.env.E2E_USER || 'phase1-bench';
const SESSION = process.env.E2E_SESSION || 'phase1-bench-session';
const DATASET = process.env.E2E_DATASET || '/fixtures/reviews.csv';
const REPO = process.env.E2E_REPO || 'https://github.com/asaini/Apriori';
const OUT = process.env.E2E_OUT || '/reports/phase1-benchmark.json';
const TIMEOUT_MS = Number(process.env.E2E_TIMEOUT_MS || 1800000);

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

async function upload() {
  const data = fs.readFileSync(DATASET);
  const form = new FormData();
  form.append('file', new Blob([data], { type: 'text/csv' }), path.basename(DATASET));
  const response = await fetch(BASE + '/api/uploads', { method: 'POST', headers, body: form });
  const text = await response.text();
  if (!response.ok) throw new Error('upload failed: ' + text);
  const uploaded = JSON.parse(text);
  console.log('[ok] uploaded', uploaded.id, uploaded.size);
  return uploaded;
}

async function main() {
  const uploaded = await upload();
  const intent = `请用 ${REPO} 做 benchmark，评测上传的评论数据集，输入列是 review，标签列是 label，最多运行 24 条样本。`;
  const created = await call('POST', '/api/plan', { intent, attachments: [uploaded.id] });
  const plan = created.plan_graph;
  console.log('[ok] plan', plan.id, plan.intent_type, plan.nodes.length, 'nodes');
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
        if (node.status === 'failed') console.log('  failed:', node.type, node.error?.slice(0, 200));
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
  const validation = report.artifacts.benchmark_validation_report?.value || '';
  console.log('BENCHMARK_DONE', current.status, 'validation chars:', validation.length);
  console.log(validation.slice(0, 800));
  if (current.status !== 'completed') process.exitCode = 2;
}

main().catch((error) => {
  console.error('BENCHMARK_FAILED', error.message);
  process.exit(1);
});
