#!/usr/bin/env node
// Round 7 lightweight paper reproduction: Apriori (1994) with asaini/Apriori repository.
// Retries a failed claim_rubric_extract node once before giving up.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const REPORT_DIR = process.env.RA_REPORT_DIR || '/reports';
const PAPER_DIR = process.env.RA_PAPER_DIR || '/papers';
const USER = 'round7-paper-repro';
const jars = new Map();
const jarFor = (user) => { if (!jars.has(user)) jars.set(user, new Map()); return jars.get(user); };
let failures = 0;
const check = (ok, message, detail) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + message + (detail === undefined ? '' : ' (' + JSON.stringify(detail) + ')'));
  if (!ok) failures++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function request(method, path, { body, form, timeoutMs = 180000 } = {}) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  headers['X-User-Id'] = USER;
  const jar = jarFor(USER);
  if (jar.size) headers.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (form) payload = form;
  else if (payload !== undefined && payload !== null && typeof payload !== 'string') {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers, body: payload, signal: AbortSignal.timeout(timeoutMs) });
  for (const cookie of response.headers.getSetCookie?.() || []) {
    const pair = cookie.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) jar.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim());
  }
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json();
  else data = await response.text();
  return { status: response.status, data };
}

function artifact(plan, key) {
  const value = plan.artifacts?.[key]?.value;
  return typeof value === 'string' ? value : '';
}

async function waitForPlan(planId, timeoutMs) {
  const started = Date.now();
  let current = null;
  let lastDone = -1;
  while (Date.now() - started < timeoutMs) {
    current = (await request('GET', '/api/plans/' + planId)).data.plan_graph;
    const done = (current.nodes || []).filter((n) => ['completed', 'failed', 'skipped', 'canceled'].includes(n.status)).length;
    if (done !== lastDone) {
      console.log('[' + new Date().toISOString() + '] status=' + current.status + ' ' + done + '/' + (current.nodes || []).length);
      for (const node of current.nodes || []) if (node.status === 'failed') console.log('  failed ' + node.name + ': ' + String(node.error || '').slice(0, 300));
      lastDone = done;
    }
    if (current.status === 'awaiting_approval') {
      const list = await request('GET', '/api/plans/' + planId + '/runtime-actions');
      for (const action of list.data?.actions || []) if (action.status === 'pending') {
        const result = await request('POST', '/api/plans/' + planId + '/runtime-actions/' + action.id + '/decision', { body: { decision: 'approve', note: 'round7 paper reproduction auto-approval' } });
        console.log('[approve] ' + action.kind + ' -> ' + result.status);
      }
    }
    if (['completed', 'failed', 'canceled', 'blocked'].includes(current.status)) return current;
    await sleep(5000);
  }
  throw new Error('plan ' + planId + ' timed out');
}

async function main() {
  const pdf = await readFile(join(PAPER_DIR, 'apriori-1994.pdf'));
  const form = new FormData();
  form.append('file', new Blob([pdf], { type: 'application/pdf' }), 'apriori-1994.pdf');
  const upload = await request('POST', '/api/uploads', { form });
  check(upload.status === 201 && Boolean(upload.data.id), 'Apriori PDF uploaded', upload.status);

  const intent = '请复现论文 Apriori: Mining Association Rules 的频繁项集算法，使用 https://github.com/asaini/Apriori 仓库，采用 smoke 模式只运行仓库自带的 INTEGRATED-DATASET.csv，输出频繁项集和关联规则，不要下载额外数据集。';
  const created = await request('POST', '/api/plan', { body: { intent, attachments: [upload.data.id], runtime_permission_mode: 'full_access', runtime_permission_acknowledged: true } });
  const plan = created.data?.plan_graph;
  check(created.status === 200 && Boolean(plan?.id), 'paper reproduction plan created', created.status);
  check(plan?.intent_type === 'Paper_Reproduction', 'intent is Paper_Reproduction', plan?.intent_type);
  console.log('plan_id=' + plan?.id + ' nodes=' + plan?.nodes?.length);
  if (plan?.approval?.required) await request('POST', '/api/plans/' + plan.id + '/approve', { body: {} });
  const executed = await request('POST', '/api/plans/' + plan.id + '/execute', { body: {} });
  check([200, 202].includes(executed.status), 'paper reproduction started', executed.status);

  let current = await waitForPlan(plan.id, 45 * 60 * 1000);
  let retried = 0;
  while (current.status === 'failed' && retried < 2) {
    const failedNode = (current.nodes || []).find((n) => n.status === 'failed' && n.type === 'claim_rubric_extract');
    if (!failedNode) break;
    const retry = await request('POST', '/api/plans/' + plan.id + '/tasks/' + failedNode.id + '/retry', { body: {} });
    check(retry.status === 200, 'retry claim_rubric_extract task accepted', retry.status);
    const restarted = await request('POST', '/api/plans/' + plan.id + '/execute', { body: {} });
    check([200, 202].includes(restarted.status), 'paper reproduction resumed after retry', restarted.status);
    retried++;
    current = await waitForPlan(plan.id, 30 * 60 * 1000);
  }
  await writeFile(join(REPORT_DIR, 'round7-paper-reproduction.json'), JSON.stringify({ plan_graph: current, retries: retried }, null, 2));

  const nodes = current.nodes || [];
  const failed = nodes.filter((n) => n.status === 'failed');
  const repo = artifact(current, 'repo_url');
  const metrics = artifact(current, 'run_metrics');
  const comparison = artifact(current, 'comparison_report');
  const paperMeta = artifact(current, 'paper_metadata');
  check(current.status === 'completed', 'paper reproduction completed', { status: current.status, retries: retried });
  check(nodes.length > 0 && failed.length === 0, 'all paper reproduction nodes completed', { nodes: nodes.length, failed: failed.length });
  check(/asaini\/Apriori/i.test(repo), 'selected expected Apriori repository', repo.slice(0, 120));
  check(metrics.length > 20, 'run_metrics artifact is substantive', metrics.length);
  check(comparison.length > 20, 'comparison_report artifact is substantive', comparison.length);
  await writeFile(join(REPORT_DIR, 'round7-paper-run-metrics.txt'), metrics);
  await writeFile(join(REPORT_DIR, 'round7-paper-comparison.md'), comparison);
  if (failures > 0) { console.log('\n' + failures + ' paper reproduction check(s) failed'); process.exit(1); }
  console.log('\nall paper reproduction checks passed');
}
main().catch((error) => { console.error('paper reproduction failed: ' + (error?.stack || error)); process.exit(1); });
