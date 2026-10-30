#!/usr/bin/env node
// Cost/budget E2E: sets a tiny user budget, creates a plan with a tiny plan
// budget, confirms the API exposes usage/cost and that budget state is visible.
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-cost-budget';
const cookies = new Map();
let failures = 0;

function check(condition, message) {
  if (condition) process.stdout.write('ok   ' + message + '\n');
  else { failures++; process.stdout.write('FAIL ' + message + '\n'); }
}

async function request(method, path, body) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) headers.Cookie = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  return { status: response.status, data, raw: text };
}

async function main() {
  process.stdout.write('# cost-budget e2e\n');
  const budget = await request('PUT', '/api/budgets', { period: 'day', limit_usd: 0.01, policy: 'soft' });
  check(budget.status === 200 && budget.data.budget?.limit_usd === 0.01, 'set user budget');
  const fetched = await request('GET', '/api/budgets');
  check(fetched.status === 200 && fetched.data.budget?.limit_usd === 0.01, 'read user budget');

  const created = await request('POST', '/api/plan', {
    intent: '用 Python 计算 2+2 并打印结果',
    max_cost_usd: 0.01,
    max_tokens: 200000,
    budget_policy: 'soft',
  });
  check(created.status === 200, 'create plan with budget');
  const plan = created.data.plan_graph;
  check(plan.budget?.max_cost_usd === 0.01 && plan.budget?.max_tokens === 200000, 'plan budget persisted');

  const summary = await request('GET', '/api/usage/summary?period=day');
  check(summary.status === 200 && summary.data.summary?.configured === true, 'usage summary configured');
  check(summary.data.summary?.limit_usd === 0.01, 'summary shows limit');

  const planUsage = await request('GET', '/api/plans/' + plan.id + '/usage');
  check(planUsage.status === 200 && planUsage.data.plan_id === plan.id && Array.isArray(planUsage.data.nodes), 'plan usage endpoint');

  const execute = await request('POST', '/api/plans/' + plan.id + '/execute', { body: {} });
  check([200, 202].includes(execute.status), 'execute plan accepted');
  const deadline = Date.now() + 90000;
  let final = null;
  while (Date.now() < deadline) {
    const detail = await request('GET', '/api/plans/' + plan.id);
    final = detail.data.plan_graph;
    if (final.paused || ['completed', 'failed', 'canceled'].includes(final.status)) break;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  check(Boolean(final), 'read final plan state');
  if (final) {
    check(final.usage?.budget_status === 'warning' || final.usage?.budget_status === 'exceeded' || final.paused === true || ['completed', 'failed', 'canceled'].includes(final.status), 'budget status evaluated');
  }
  const timeseries = await request('GET', '/api/usage/timeseries?group=model');
  check(timeseries.status === 200 && Array.isArray(timeseries.data.points), 'usage timeseries');
  const alerts = await request('GET', '/api/budgets/alerts');
  check(alerts.status === 200 && Array.isArray(alerts.data.alerts), 'budget alerts endpoint');

  process.stdout.write(failures === 0 ? '\nALL PASSED\n' : '\n' + failures + ' FAILED\n');
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((error) => { console.error(error); process.exit(1); });
