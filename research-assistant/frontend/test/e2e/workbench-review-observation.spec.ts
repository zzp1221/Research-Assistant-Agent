import { readFileSync, writeFileSync } from 'node:fs';
import { createServer, type ServerResponse } from 'node:http';
import { expect, test, type Page } from '@playwright/test';

const baseline = JSON.parse(readFileSync(new URL('../fixtures/a11y-plan.json', import.meta.url), 'utf8'));
const startedAt = '2026-11-09T00:00:00Z';
type Graph = typeof baseline;
type RequestRecord = { method: string; path: string; query: string; body?: unknown };

function graph(id: string, status = 'awaiting_review'): Graph {
  return {
    ...structuredClone(baseline), id, title: `验收 ${id}`, status, current_revision: 2,
    latest_revision: 2, review_required: true, review_status: 'pending',
    usage: { task_attempts: 1, started_at: startedAt },
    nodes: baseline.nodes.map((node: Record<string, unknown>) => ({
      ...node, status: status === 'in_progress' ? 'in_progress' : 'pending',
      result: '', structured_data: '',
    })),
    meta: { total_nodes: 1, completed_nodes: 0, failed_nodes: 0, blocked_nodes: 0, in_progress_nodes: status === 'in_progress' ? 1 : 0 },
  };
}

function summary(planId: string, revision: number) {
  return { id: `${planId}-r${revision}`, plan_id: planId, revision, parent_revision: revision - 1,
    status: 'draft', reason: `revision ${revision}`, hash: `hash-${revision}`, created_at: startedAt };
}

async function seedWorkbench(page: Page, activeGraph: Graph | null) {
  await page.addInitScript(({ plan }) => {
    localStorage.setItem('research-assistant.chat-sessions.v1', JSON.stringify({
      userId: 'a11y-user', activeSessionId: 'a11y-session',
      sessions: [{ id: 'a11y-session', title: '工作台交互验收', createdAt: '2026-11-09T00:00:00Z',
        updatedAt: '2026-11-09T00:00:00Z', chatHistory: [], activePlanId: plan?.id ?? null,
        planGraph: null, intentContext: plan ? { raw_intent: plan.user_intent,
          intent_type: plan.intent_type, entities: {}, constraints: {}, metadata: {} } : null }],
    }));
  }, { plan: activeGraph });
}

function writeEvidence(path: string, value: unknown) {
  writeFileSync(path, JSON.stringify(value, null, 2));
}

test('R-01 dirty review guards Escape, backdrop and revision changes; save stays open and validates its returned revision', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const plan = graph('review-browser');
  const revisions = new Map<number, Graph>([[1, graph(plan.id)], [2, plan]]);
  revisions.get(1).nodes[0].name = '历史节点名称';
  let latest = 2;
  const network: RequestRecord[] = [];
  await seedWorkbench(page, plan);
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    network.push({ method: request.method(), path: url.pathname, query: url.search,
      ...(request.postData() ? { body: request.postDataJSON() } : {}) });
    let body: unknown = {};
    if (url.pathname === '/api/me') body = { user_id: 'a11y-user', session_id: 'a11y-session' };
    else if (url.pathname === `/api/plans/${plan.id}`) body = { plan_graph: { ...revisions.get(latest), current_revision: latest, latest_revision: latest } };
    else if (url.pathname === `/api/plans/${plan.id}/revisions` && request.method() === 'POST') {
      const submitted = request.postDataJSON();
      latest += 1;
      revisions.set(latest, { ...submitted.graph, id: plan.id, status: 'awaiting_review', current_revision: latest, latest_revision: latest });
      body = { revision: summary(plan.id, latest), plan: revisions.get(latest) };
    } else if (url.pathname === `/api/plans/${plan.id}/revisions`) {
      body = { revisions: [...revisions.keys()].map((n) => summary(plan.id, n)), current_revision: latest, latest_revision: latest };
    } else if (/\/revisions\/\d+\/validate$/.test(url.pathname)) body = { valid: true, errors: [] };
    else if (/\/revisions\/\d+$/.test(url.pathname)) {
      const revision = Number(url.pathname.split('/').at(-1));
      body = { revision: { ...summary(plan.id, revision), graph: revisions.get(revision) } };
    } else if (url.pathname.endsWith('/events')) body = { events: [] };
    else if (url.pathname === '/api/plans') body = { plans: [], total: 0, next_cursor: '' };
    else if (url.pathname === '/api/projects') body = { projects: [] };
    else if (url.pathname === '/api/approvals') body = { items: [] };
    else if (url.pathname.endsWith('/alerts')) body = { alerts: [] };
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
  await page.goto('/');
  await expect(page.locator('.react-flow__node')).toBeVisible();
  const trigger = page.getByRole('button', { name: '计划审阅', exact: true });
  await trigger.click();
  const review = page.getByRole('dialog', { name: '计划审阅', exact: true });
  const name = review.getByRole('textbox', { name: '节点名称', exact: true });
  await expect(name).toBeEnabled();
  await name.fill('保留的浏览器草稿');
  await page.keyboard.press('Escape');
  const leave = page.getByRole('dialog', { name: 'r2 有未保存的修改', exact: true });
  await expect(leave).toBeVisible();
  await expect(page.locator('dialog[open]')).toHaveCount(2);
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press('Tab');
    expect(await leave.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  }
  await page.keyboard.press('Escape');
  await expect(leave).toHaveCount(0);
  await expect(name).toHaveValue('保留的浏览器草稿');
  const bounds = await review.boundingBox();
  expect(bounds).not.toBeNull();
  // This click lands on the native backdrop outside the dialog rectangle.
  const backdrop = bounds!.x > 5 ? { x: 2, y: 2 } : { x: 1438, y: 898 };
  expect(backdrop.x < bounds!.x || backdrop.x > bounds!.x + bounds!.width || backdrop.y < bounds!.y || backdrop.y > bounds!.y + bounds!.height).toBe(true);
  await page.mouse.click(backdrop.x, backdrop.y);
  await expect(leave).toBeVisible();
  await leave.getByRole('button', { name: '继续编辑', exact: true }).click();
  await expect(name).toHaveValue('保留的浏览器草稿');
  const revisionOneReads = () => network.filter((r) => r.method === 'GET' && r.path.endsWith('/revisions/1')).length;
  const beforeSwitch = revisionOneReads();
  await review.getByRole('button', { name: /^r1\s/ }).click();
  await expect(leave).toBeVisible();
  // A canceled revision switch must never fetch or replace the historical graph.
  await leave.getByRole('button', { name: '继续编辑', exact: true }).click();
  expect(revisionOneReads()).toBe(beforeSwitch);
  await expect(name).toHaveValue('保留的浏览器草稿');
  await review.getByRole('button', { name: '保存草稿', exact: true }).click();
  await expect(review.getByTestId('review-state-chip')).toHaveText('已保存');
  await expect(review).toBeVisible();
  await expect(review.getByText('正在编辑 r3', { exact: true })).toBeVisible();
  await expect(name).toHaveValue('保留的浏览器草稿');
  await name.fill('保存并校验的浏览器草稿');
  await review.getByRole('button', { name: '保存并校验', exact: true }).click();
  await expect(review.getByTestId('review-state-chip')).toHaveText('已保存');
  await expect(review.getByText('正在编辑 r4', { exact: true })).toBeVisible();
  const saves = network.filter((r) => r.method === 'POST' && r.path.endsWith('/revisions'));
  expect(saves).toHaveLength(2);
  expect(saves.map((r) => (r.body as { base_revision: number }).base_revision)).toEqual([2, 3]);
  expect(network.filter((r) => r.method === 'POST' && r.path.endsWith('/validate')).map((r) => r.path)).toEqual([`/api/plans/${plan.id}/revisions/4/validate`]);
  expect(network.filter((r) => r.method === 'POST' && /\/(execute|approve)$/.test(r.path))).toHaveLength(0);
  await page.screenshot({ path: testInfo.outputPath('review-saved-open.png'), fullPage: true });
  writeEvidence(testInfo.outputPath('review-network.json'), network);
  await review.getByRole('button', { name: '关闭审阅面板', exact: true }).click();
  await expect(review).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

async function startSseServer() {
  const active = new Map<string, Set<ServerResponse>>();
  const opened: { plan: string; ticket: string | null; afterSeq: string | null }[] = [];
  const maxActive: Record<string, number> = {};
  const server = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://127.0.0.1');
    const planId = url.pathname.split('/')[3];
    response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache',
      'Access-Control-Allow-Origin': request.headers.origin ?? 'http://127.0.0.1:15173',
      'Access-Control-Allow-Credentials': 'true', Vary: 'Origin' });
    response.flushHeaders();
    response.write(': connected\n\n');
    const group = active.get(planId) ?? new Set<ServerResponse>();
    active.set(planId, group);
    group.add(response);
    maxActive[planId] = Math.max(maxActive[planId] ?? 0, group.size);
    opened.push({ plan: planId, ticket: url.searchParams.get('ticket'), afterSeq: url.searchParams.get('after_seq') });
    response.on('close', () => group.delete(response));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('SSE test server failed to bind');
  return { origin: `http://127.0.0.1:${address.port}`, active, opened, maxActive,
    emit(planId: string, event: unknown) { for (const response of active.get(planId) ?? []) response.write(`event: plan_event\ndata: ${JSON.stringify(event)}\n\n`); },
    drop(planId: string) { for (const response of active.get(planId) ?? []) response.end(); },
    async close() { for (const group of active.values()) for (const response of group) response.destroy(); await new Promise<void>((resolve) => server.close(() => resolve())); },
  };
}

test('O-01–05 library opens observe without execute; reconnect, focus and plan switch keep one native EventSource', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const plans = [graph('running-a', 'in_progress'), graph('running-b', 'in_progress')];
  const network: RequestRecord[] = [];
  const tickets: Record<string, number> = {};
  const sse = await startSseServer();
  // Instrument lifecycle while retaining Chromium's real EventSource transport.
  await page.addInitScript(() => {
    const Native = window.EventSource;
    const audit = { active: 0, maximum: 0, opened: 0 };
    Object.assign(window, { __workbenchSseAudit: audit });
    window.EventSource = class extends Native {
      private closedByApp = false;
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init); audit.active += 1; audit.opened += 1; audit.maximum = Math.max(audit.maximum, audit.active);
      }
      close() { if (!this.closedByApp) { audit.active -= 1; this.closedByApp = true; } super.close(); }
    };
  });
  await seedWorkbench(page, null);
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    network.push({ method: request.method(), path: url.pathname, query: url.search });
    if (url.pathname.endsWith('/stream')) {
      await route.fulfill({ status: 307, headers: { Location: `${sse.origin}${url.pathname}${url.search}` } });
      return;
    }
    let body: unknown = {};
    const plan = plans.find((item) => url.pathname.startsWith(`/api/plans/${item.id}`));
    if (url.pathname === '/api/me') body = { user_id: 'a11y-user', session_id: 'a11y-session' };
    else if (url.pathname === '/api/plans') body = { plans, total: plans.length, next_cursor: '' };
    else if (url.pathname === '/api/projects') body = { projects: [] };
    else if (plan && url.pathname === `/api/plans/${plan.id}`) body = { plan_graph: plan };
    else if (plan && url.pathname.endsWith('/events')) body = { events: [{ plan_id: plan.id, event_type: 'plan_started', seq: 1, revision: 2, timestamp: startedAt, payload: {} }] };
    else if (plan && url.pathname.endsWith('/stream-ticket')) {
      tickets[plan.id] = (tickets[plan.id] ?? 0) + 1;
      body = { ticket: `${plan.id}-ticket-${tickets[plan.id]}` };
    } else if (url.pathname === '/api/approvals') body = { items: [] };
    else if (url.pathname.endsWith('/alerts')) body = { alerts: [] };
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
  const libraryOpen = async (title: string) => {
    await page.getByRole('button', { name: '计划库', exact: true }).click();
    const library = page.getByRole('dialog', { name: '计划库', exact: true });
    await library.locator('article').filter({ has: page.getByRole('heading', { name: title, exact: true }) }).getByRole('button', { name: '打开', exact: true }).click();
    await expect(library).toHaveCount(0);
  };
  try {
    await page.goto('/');
    await libraryOpen(plans[0].title);
    await expect.poll(() => sse.active.get('running-a')?.size ?? 0).toBe(1);
    expect(sse.opened[0]).toEqual({ plan: 'running-a', ticket: 'running-a-ticket-1', afterSeq: '1' });
    expect(network.some((r) => r.method === 'GET' && r.path.endsWith('/running-a/events'))).toBe(true);
    const node = page.locator('.react-flow__node').first();
    await node.focus();
    await page.keyboard.press('Enter');
    sse.emit('running-a', { plan_id: 'running-a', event_type: 'task_log', task_id: 'task-a11y',
      seq: 2, revision: 2, timestamp: '2026-11-09T00:00:01Z', payload: { message: 'native SSE before reconnect' } });
    await expect(page.locator('.research-assistant-execution-sidebar')).toContainText('native SSE before reconnect');
    sse.drop('running-a');
    await expect.poll(() => sse.opened.filter((item) => item.plan === 'running-a').length).toBe(2);
    const reopened = sse.opened.filter((item) => item.plan === 'running-a')[1];
    expect(reopened).toEqual({ plan: 'running-a', ticket: 'running-a-ticket-2', afterSeq: '2' });
    sse.emit('running-a', { plan_id: 'running-a', event_type: 'task_log', task_id: 'task-a11y',
      seq: 3, revision: 2, timestamp: '2026-11-09T00:00:02Z', payload: { message: 'native SSE after reconnect' } });
    await expect(page.locator('.research-assistant-execution-sidebar')).toContainText('native SSE after reconnect');
    const beforeFocus = network.filter((r) => r.path === '/api/plans/running-a').length;
    await page.evaluate(() => { window.dispatchEvent(new Event('focus')); window.dispatchEvent(new Event('focus')); });
    await expect.poll(() => network.filter((r) => r.path === '/api/plans/running-a').length).toBeGreaterThan(beforeFocus);
    expect(tickets['running-a']).toBe(2);
    expect(sse.opened.filter((item) => item.plan === 'running-a')).toHaveLength(2);
    await page.keyboard.press('Escape');
    await libraryOpen(plans[1].title);
    await expect.poll(() => sse.active.get('running-a')?.size ?? 0).toBe(0);
    await expect.poll(() => sse.active.get('running-b')?.size ?? 0).toBe(1);
    expect(sse.opened.filter((item) => item.plan === 'running-b')).toHaveLength(1);
    // Leave a reconnect timer pending for B, then switch back before it fires.
    sse.drop('running-b');
    await expect(page.getByText(/正在重连/).first()).toBeVisible();
    await libraryOpen(plans[0].title);
    await expect.poll(() => sse.active.get('running-a')?.size ?? 0).toBe(1);
    await page.waitForTimeout(1300);
    expect(tickets['running-b']).toBe(1);
    expect(sse.active.get('running-b')?.size ?? 0).toBe(0);
    const audit = await page.evaluate(() => (window as unknown as { __workbenchSseAudit: { active: number; maximum: number; opened: number } }).__workbenchSseAudit);
    expect(audit.active).toBe(1);
    expect(audit.maximum).toBe(1);
    expect(Object.values(sse.maxActive).every((value) => value === 1)).toBe(true);
    const commands = network.filter((r) => r.method === 'POST' && /\/(execute|approve|retry|resume)$/.test(r.path));
    expect(commands).toHaveLength(0);
    await page.screenshot({ path: testInfo.outputPath('observation-restored.png'), fullPage: true });
    writeEvidence(testInfo.outputPath('observation-network.json'), { requests: network, streams: sse.opened, maxActive: sse.maxActive, lifecycle: audit, executeRequests: commands.length });
  } finally {
    writeEvidence(testInfo.outputPath('observation-network-final.json'), { requests: network, streams: sse.opened, maxActive: sse.maxActive });
    await page.close();
    await sse.close();
  }
});
