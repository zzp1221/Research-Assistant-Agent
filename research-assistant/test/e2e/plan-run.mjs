#!/usr/bin/env node
// Creates a plan, optionally starts it, approves runtime actions and waits for
// a terminal state. Prints events and a compact summary. Intended to run inside
// the compose network.
//
// Usage: node test/e2e/plan-run.mjs --intent "..." [--execute] [--full-access]
//        [--timeout 1800] [--approve-all] [--auto-run]
const BASE = (process.env.RA_API_URL || 'http://backend:8080').replace(/\/$/, '');
const TOKEN = process.env.RA_API_TOKEN || '';
const args = process.argv.slice(2);
const option = (name, fallback) => {
  const index = args.indexOf('--' + name);
  return index >= 0 && args[index + 1] && !args[index + 1].startsWith('--') ? args[index + 1] : fallback;
};
const flag = (name) => args.includes('--' + name);
const intent = option('intent', '用 Python 画一个正弦函数和余弦函数的对比图，并保存为 PNG');
const timeoutSeconds = Number(option('timeout', '1800'));
const cookies = new Map();

async function api(method, path, body) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) headers.Cookie = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  if (!response.ok) {
    const error = new Error(method + ' ' + path + ' -> ' + response.status + ': ' + (typeof data === 'string' ? data : JSON.stringify(data)));
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function decidePendingActions(planId) {
  const payload = await api('GET', '/api/plans/' + planId + '/runtime-actions');
  const actions = payload.runtime_actions || payload.actions || [];
  for (const action of actions) {
    if (action.status !== 'pending') continue;
    console.log('[approve] ' + action.kind + ' ' + action.title);
    await api('POST', '/api/plans/' + planId + '/runtime-actions/' + action.id + '/decision', {
      decision: 'approve',
      note: 'e2e auto approval',
    });
  }
}

async function main() {
  const created = await api('POST', '/api/plan', {
    intent,
    runtime_permission_mode: flag('full-access') ? 'full_access' : 'confirm_important',
    runtime_permission_acknowledged: flag('full-access'),
  });
  const plan = created.plan_graph;
  console.log('[created] plan=' + plan.id + ' intent_type=' + plan.intent_type + ' nodes=' + plan.nodes.length + ' approval=' + JSON.stringify(plan.approval));
  if (flag('approve-all') && plan.approval?.required) {
    await api('POST', '/api/plans/' + plan.id + '/approve');
    console.log('[approved] plan-level approval granted');
  }
  if (flag('execute') || flag('auto-run')) {
    await api('POST', '/api/plans/' + plan.id + '/execute', { trigger: flag('auto-run') ? 'auto' : 'manual' });
    console.log('[execute] started');
  }
  const deadline = Date.now() + timeoutSeconds * 1000;
  let lastStatus = '';
  let lastEventSeq = 0;
  while (Date.now() < deadline) {
    if (flag('approve-all')) await decidePendingActions(plan.id);
    const detail = await api('GET', '/api/plans/' + plan.id);
    const current = detail.plan_graph;
    if (current.status !== lastStatus) {
      console.log('[status] ' + current.status + (current.paused ? ' paused' : '') + ' outcome=' + (current.outcome || '-'));
      lastStatus = current.status;
    }
    try {
      const events = await api('GET', '/api/plans/' + plan.id + '/events?after_seq=' + lastEventSeq);
      for (const event of events.events || []) {
        lastEventSeq = Math.max(lastEventSeq, event.seq || 0);
        if (['task_completed', 'task_failed', 'task_blocked', 'runtime_action_requested', 'plan_completed', 'plan_failed', 'plan_stalled', 'plan_paused', 'plan_resumed'].includes(event.event_type)) {
          const summary = event.event_type === 'task_completed'
            ? String(event.payload?.result_summary || '').slice(0, 160)
            : String(event.payload?.error || event.payload?.reason || event.payload?.action?.title || '').slice(0, 160);
          console.log('[event] seq=' + event.seq + ' ' + event.event_type + (event.task_id ? ' ' + event.task_id : '') + (summary ? ' :: ' + summary : ''));
        }
      }
    } catch (error) {
      console.log('[events] unavailable: ' + error.message);
    }
    if (['completed', 'failed', 'canceled'].includes(current.status)) {
      const usage = current.usage || {};
      console.log('[usage] llm_calls=' + (usage.llm_calls || 0) + ' tokens=' + (usage.llm_prompt_tokens || 0) + '/' + (usage.llm_completion_tokens || 0) + ' cost_usd=' + (usage.llm_cost_usd || 0) + ' sandbox_seconds=' + (usage.sandbox_seconds || 0));
      const missing = current.missing_requirements || [];
      if (missing.length) console.log('[missing] ' + JSON.stringify(missing));
      const images = (current.nodes || []).filter((node) => node.image_url || node.image_base64).map((node) => node.name + ':' + (node.image_url || 'inline'));
      console.log('[images] ' + images.join(', '));
      console.log('[final] status=' + current.status + ' outcome=' + (current.outcome || '-') + ' url=' + BASE + '/api/plans/' + plan.id);
      if (current.status !== 'completed') process.exit(1);
      return;
    }
    await sleep(2000);
  }
  console.log('[timeout] plan did not finish within ' + timeoutSeconds + 's');
  process.exit(1);
}

main().catch((error) => {
  console.error('[failed] ' + error.message);
  process.exit(1);
});
