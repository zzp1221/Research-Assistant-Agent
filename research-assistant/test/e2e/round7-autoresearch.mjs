#!/usr/bin/env node
// Round 7 AutoResearch campaign: rank-bm25 boundary robustness package.
// Usage: RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/round7-autoresearch.mjs
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const REPORT_DIR = process.env.RA_REPORT_DIR || '/reports';
const PACKAGE = process.env.RA_AUTORESEARCH_PACKAGE || join(process.cwd(), 'examples/autoresearch/real_repositories/rank_bm25');
const USER = 'round7-autoresearch';
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
function parseArtifact(plan, key) {
  const raw = artifact(plan, key);
  if (!raw.trim()) return {};
  try { return JSON.parse(raw); } catch { return {}; }
}

async function main() {
  const uploads = [];
  for (const name of ['autoresearch.json', 'evaluator.py', 'holdout_evaluator.py']) {
    const buffer = await readFile(join(PACKAGE, name));
    const form = new FormData();
    form.append('file', new Blob([buffer]), name);
    const upload = await request('POST', '/api/uploads', { form });
    check(upload.status === 201 && Boolean(upload.data.id), 'upload ' + name, upload.status);
    uploads.push(upload.data.id);
  }
  const intent = '用 https://github.com/dorianbrown/rank_bm25 做 AutoResearch，使用上传的 autoresearch.json，最多 2 次实验，总时长 5 分钟，模型不可见 holdout 验收 2 次。';
  const created = await request('POST', '/api/plan', { body: { intent, attachments: uploads, runtime_permission_mode: 'full_access', runtime_permission_acknowledged: true } });
  const plan = created.data?.plan_graph;
  check(created.status === 200 && Boolean(plan?.id), 'AutoResearch plan created', created.status);
  check(plan?.intent_type === 'AutoResearch', 'intent is AutoResearch', plan?.intent_type);
  console.log('plan_id=' + plan?.id + ' nodes=' + plan?.nodes?.length);
  if (plan?.approval?.required) await request('POST', '/api/plans/' + plan.id + '/approve', { body: {} });
  const executed = await request('POST', '/api/plans/' + plan.id + '/execute', { body: {} });
  check([200, 202].includes(executed.status), 'AutoResearch started', executed.status);

  const started = Date.now();
  let current = plan;
  let lastDone = -1;
  while (Date.now() - started < 45 * 60 * 1000) {
    current = (await request('GET', '/api/plans/' + plan.id)).data.plan_graph;
    const done = (current.nodes || []).filter((n) => ['completed', 'failed', 'skipped', 'canceled'].includes(n.status)).length;
    if (done !== lastDone) {
      console.log('[' + new Date().toISOString() + '] status=' + current.status + ' ' + done + '/' + (current.nodes || []).length);
      for (const node of current.nodes || []) if (node.status === 'failed') console.log('  failed ' + node.name + ': ' + String(node.error || '').slice(0, 300));
      lastDone = done;
    }
    if (current.status === 'awaiting_approval') {
      const list = await request('GET', '/api/plans/' + plan.id + '/runtime-actions');
      for (const action of list.data?.actions || []) if (action.status === 'pending') {
        const result = await request('POST', '/api/plans/' + plan.id + '/runtime-actions/' + action.id + '/decision', { body: { decision: 'approve', note: 'round7 autoresearch auto-approval' } });
        console.log('[approve] ' + action.kind + ' -> ' + result.status);
      }
    }
    if (['completed', 'failed', 'canceled', 'blocked'].includes(current.status)) break;
    await sleep(5000);
  }
  await writeFile(join(REPORT_DIR, 'round7-autoresearch-plan.json'), JSON.stringify({ plan_graph: current }, null, 2));

  const manifest = parseArtifact(current, 'repo_manifest');
  const ledger = parseArtifact(current, 'research_trial_ledger');
  const validation = parseArtifact(current, 'research_validation_report');
  const nodes = current.nodes || [];
  const failed = nodes.filter((n) => n.status === 'failed');
  check(current.status === 'completed', 'AutoResearch completed', current.status);
  check(nodes.length > 0 && failed.length === 0, 'all AutoResearch nodes completed', { nodes: nodes.length, failed: failed.length });
  check(Boolean(manifest.repository_commit || manifest.requested_revision), 'repo manifest records a commit', manifest.repository_commit || manifest.requested_revision);
  check(Number(ledger.completed_trials || 0) >= 1, 'trial ledger recorded at least one trial', ledger.completed_trials);
  check(Number(ledger.best_score) >= Number(ledger.baseline_score), 'best score >= baseline', { baseline: ledger.baseline_score, best: ledger.best_score });
  check(['passed', 'hidden_holdout', 'validated'].includes(String(validation.status || '').toLowerCase()) || Number(validation.passed_runs) >= 1, 'holdout validation passed', validation.status);
  const summary = {
    plan_id: current.id,
    status: current.status,
    repository_commit: manifest.repository_commit,
    baseline: ledger.baseline_score,
    best: ledger.best_score,
    target: ledger.target_score,
    stop_reason: ledger.stop_reason,
    completed_trials: ledger.completed_trials,
    accepted_trials: ledger.accepted_trials,
    validation_status: validation.status,
    validation_runs: validation.requested_runs,
  };
  await writeFile(join(REPORT_DIR, 'round7-autoresearch-summary.json'), JSON.stringify(summary, null, 2));
  console.log(JSON.stringify(summary, null, 2));
  if (failures > 0) { console.log('\n' + failures + ' AutoResearch check(s) failed'); process.exit(1); }
  console.log('\nall AutoResearch checks passed');
}
main().catch((error) => { console.error('AutoResearch failed: ' + (error?.stack || error)); process.exit(1); });
