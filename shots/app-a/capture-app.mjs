import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs';
const require = createRequire('D:/项目/Research Assistant agent/research-assistant/frontend/package.json');
const { chromium } = require('playwright');
const base = 'D:/项目/Research Assistant agent';
const planGraph = JSON.parse(fs.readFileSync(path.join(base, 'research-assistant/frontend/test/fixtures/a11y-plan.json'), 'utf8'));
planGraph.user_intent = '复现 Transformer 注意力机制并跑通最小实验';
planGraph.nodes = [
  {
    id: 'task-1', name: '解析论文与数据', type: 'experiment_dataset_prepare', description: '解析论文与数据',
    assigned_to: 'librarian_agent', status: 'completed', dependencies: [], inputs: {}, required_artifacts: [], output_artifacts: [],
    parallelizable: false, priority: 0, retry_limit: 1, run_count: 1, execution_epoch: 1,
    contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    result: 'done', structured_data: '{}'
  },
  {
    id: 'task-2', name: '复现注意力机制', type: 'experiment_spec', description: '复现注意力机制',
    assigned_to: 'coder_agent', status: 'completed', dependencies: ['task-1'], inputs: {}, required_artifacts: [], output_artifacts: [],
    parallelizable: false, priority: 0, retry_limit: 1, run_count: 1, execution_epoch: 1,
    contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    result: 'done', structured_data: '{}'
  },
  {
    id: 'task-3', name: '构造训练脚本', type: 'execute_code', description: '构造训练脚本',
    assigned_to: 'coder_agent', status: 'in_progress', dependencies: ['task-1', 'task-2'], inputs: {}, required_artifacts: [], output_artifacts: [],
    parallelizable: false, priority: 0, retry_limit: 1, run_count: 1, execution_epoch: 1,
    contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    result: '', structured_data: '{}'
  },
  {
    id: 'task-4', name: '运行基线实验', type: 'experiment_validate', description: '运行基线实验',
    assigned_to: 'sandbox_agent', status: 'ready', dependencies: ['task-3'], inputs: {}, required_artifacts: [], output_artifacts: [],
    parallelizable: false, priority: 0, retry_limit: 1, run_count: 0, execution_epoch: 0,
    contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    result: '', structured_data: '{}'
  },
  {
    id: 'task-5', name: '汇总结果与报告', type: 'claim_evidence_build', description: '汇总结果与报告',
    assigned_to: 'benchmark_agent', status: 'ready', dependencies: ['task-4'], inputs: {}, required_artifacts: [], output_artifacts: [],
    parallelizable: false, priority: 0, retry_limit: 1, run_count: 0, execution_epoch: 0,
    contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    result: '', structured_data: '{}'
  }
];
planGraph.edges = [
  { id: 'e1', from: 'task-1', to: 'task-2', type: 'control' },
  { id: 'e2', from: 'task-1', to: 'task-3', type: 'control' },
  { id: 'e3', from: 'task-2', to: 'task-3', type: 'control' },
  { id: 'e4', from: 'task-3', to: 'task-4', type: 'control' },
  { id: 'e5', from: 'task-4', to: 'task-5', type: 'control' }
];
planGraph.status = 'in_progress';
planGraph.meta = { ...planGraph.meta, total_nodes: 5, completed_nodes: 2, failed_nodes: 0, blocked_nodes: 0, in_progress_nodes: 1, ready_nodes: 2 };
const savedState = {
  userId: 'demo-researcher',
  activeSessionId: 'demo-session',
  sessions: [{
    id: 'demo-session',
    title: 'Transformer 论文复现',
    createdAt: '2026-10-30T03:00:00Z',
    updatedAt: '2026-10-30T05:00:00Z',
    chatHistory: [
      { role: 'user', text: '复现这篇论文的核心实验，先跑通最小版本，再和基线做对比。' },
      { role: 'system', text: '已拆成 5 个节点。先确认运行环境，再从注意力机制开始。预计 18 分钟。' }
    ],
    activePlanId: 'a11y-plan',
    intentContext: { raw_intent: '复现 Transformer 注意力机制并跑通最小实验', intent_type: 'Paper_Reproduction', entities: {}, constraints: {}, metadata: {} },
    planGraph
  }]
};

const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
const page = await ctx.newPage();
page.on('pageerror', (err) => console.error('PAGEERROR', err.message));
page.on('console', (msg) => { if (msg.type() === 'error') console.error('CONSOLE', msg.text()); });
await page.addInitScript(({ value }) => {
  window.localStorage.setItem('research-assistant.chat-sessions.v1', value);
}, { value: JSON.stringify(savedState) });
await page.route('**/api/**', async (route) => {
  if (route.request().resourceType() === 'script') return route.continue();
  const url = new URL(route.request().url());
  const p = url.pathname;
  if (p === '/api/me') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ user_id: 'demo-researcher', session_id: 'demo-session' }) });
  if (p === '/api/approvals') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: [] }) });
  if (p === '/api/budgets') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ budget: { owner_id: 'demo-researcher', period: 'day', limit_usd: 5, policy: 'soft', updated_at: '2026-10-30T05:00:00Z' } }) });
  if (p === '/api/budgets/alerts') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ alerts: [] }) });
  if (p === '/api/usage/summary') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ summary: { period: 'day', configured: true, limit_usd: 5, spent_usd: 1.24, reserved_usd: 0, remaining_usd: 3.76, today_usd: 1.24, month_usd: 12.4, top_plans: [], top_models: [] } }) });
  if (p === '/api/plans') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ plans: [], total: 0, next_cursor: '' }) });
  if (p.startsWith('/api/plans/a11y-plan/events')) return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ events: [] }) });
  if (p === '/api/plans/a11y-plan') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ plan_graph: planGraph }) });
  return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({}) });
});
await page.goto('http://127.0.0.1:5173/', { waitUntil: 'load' });
await page.waitForTimeout(2200);
await page.screenshot({ path: path.join(base, 'shots/app-a/home-1440x900.png'), fullPage: false });
await page.screenshot({ path: path.join(base, 'shots/app-a/home-full-1440x900.png'), fullPage: true });
await page.locator('.research-assistant-rail-btn[aria-label="计划库"]').click();
await page.waitForTimeout(900);
await page.screenshot({ path: path.join(base, 'shots/app-a/plan-library-1440x900.png'), fullPage: false });
await page.getByRole('button', { name: '关闭', exact: true }).click();
await page.waitForTimeout(400);
await page.locator('.research-assistant-rail-btn[aria-label="成本与预算"]').click();
await page.waitForTimeout(600);
await page.screenshot({ path: path.join(base, 'shots/app-a/cost-1440x900.png'), fullPage: false });
await page.getByRole('button', { name: '关闭', exact: true }).click();
await page.waitForTimeout(400);
await page.locator('.research-assistant-rail-btn[aria-label="文献库"]').click();
await page.waitForTimeout(900);
await page.screenshot({ path: path.join(base, 'shots/app-a/literature-1440x900.png'), fullPage: false });
await page.locator('.research-assistant-rail-btn[aria-label="对话"]').click();
await page.waitForTimeout(400);
const desktop = await page.evaluate(() => {
  const sel = ['.research-assistant-app-shell','.research-assistant-rail','.research-assistant-left-workspace','.research-assistant-graph-panel','.research-assistant-canvas-stage','.research-assistant-status-bar'];
  const out = {};
  for (const s of sel) { const el = document.querySelector(s); if (el) { const r = el.getBoundingClientRect(); out[s] = { x:+r.x.toFixed(1), y:+r.y.toFixed(1), w:+r.width.toFixed(1), h:+r.height.toFixed(1), bottom:+r.bottom.toFixed(1), right:+r.right.toFixed(1) }; } }
  return { title: document.title, viewport:[innerWidth,innerHeight], doc:[document.documentElement.scrollWidth,document.documentElement.scrollHeight], bodyText: document.body.innerText.slice(0, 800), out };
});
console.log('DESKTOP ' + JSON.stringify(desktop, null, 2));
const nodeCount = await page.locator('.react-flow__node').count();
console.log('NODECOUNT', nodeCount);
if (nodeCount > 0) {
  await page.locator('.react-flow__node').first().click({ force: true });
  await page.waitForTimeout(700);
  console.log('NODE_TEXT ' + (await page.locator('.react-flow__node').first().innerText()));
  console.log('NODE_HTML ' + (await page.locator('.react-flow__node').first().innerHTML()).slice(0, 1200));
  await page.screenshot({ path: path.join(base, 'shots/app-a/node-detail-1440x900.png'), fullPage: false });
  console.log('SIDEBAR ' + await page.locator('.research-assistant-execution-sidebar').count());
  const sideMetrics = await page.evaluate(() => { const el = document.querySelector('.research-assistant-execution-sidebar'); if (!el) return null; const r = el.getBoundingClientRect(); const cs = getComputedStyle(el); return { x:+r.x.toFixed(1), y:+r.y.toFixed(1), w:+r.width.toFixed(1), h:+r.height.toFixed(1), bottom:+r.bottom.toFixed(1), margin: cs.margin, borderRadius: cs.borderRadius }; });
  console.log('SIDEMETRICS ' + JSON.stringify(sideMetrics));
  await page.locator('.research-assistant-execution-sidebar button[aria-label="关闭节点详情"]').click();
  await page.waitForTimeout(300);
}
await page.screenshot({ path: path.join(base, 'shots/app-a/home-1440x900.png'), fullPage: false });
await page.setViewportSize({ width: 390, height: 844 });
await page.waitForTimeout(500);
await page.screenshot({ path: path.join(base, 'shots/app-a/home-390x844.png'), fullPage: false });
const mobile = await page.evaluate(() => {
  const sel = ['.research-assistant-app-shell','.research-assistant-rail','.research-assistant-left-workspace','.research-assistant-graph-workspace','.research-assistant-mobile-tabbar'];
  const out = {};
  for (const s of sel) { const el = document.querySelector(s); if (el) { const r = el.getBoundingClientRect(); const cs = getComputedStyle(el); out[s] = { x:+r.x.toFixed(1), y:+r.y.toFixed(1), w:+r.width.toFixed(1), h:+r.height.toFixed(1), visibility:cs.visibility }; } }
  return { viewport:[innerWidth,innerHeight], doc:[document.documentElement.scrollWidth,document.documentElement.scrollHeight], out };
});
console.log('MOBILE ' + JSON.stringify(mobile, null, 2));
await page.locator('.research-assistant-mobile-tabbar button').first().click();
await page.waitForTimeout(500);
await page.screenshot({ path: path.join(base, 'shots/app-a/chat-390x844.png'), fullPage: false });
await browser.close();
