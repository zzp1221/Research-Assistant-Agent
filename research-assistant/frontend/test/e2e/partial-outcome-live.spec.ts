import { writeFileSync } from 'node:fs';
import process from 'node:process';
import { expect, test, type Page, type TestInfo } from '@playwright/test';

// Opt in only against a running, same-origin nginx/backend deployment. All
// plans belong to this fresh browser context and are removed in finally.
// No API routes, scheduler outcomes, model providers or EventSource transports
// are mocked. The native tools make the run deterministic and require no LLM.
test.skip(process.env.PARTIAL_OUTCOME_LIVE !== '1', 'Set PARTIAL_OUTCOME_LIVE=1 to test the deployed frontend and real backend.');

interface LiveDetails {
  mode?: string;
  accepted_goals?: Array<{ id: string; statement: string; verified: boolean; evidence_refs?: string[]; reason?: string }>;
  optional_losses?: Array<{ task_id: string; name?: string; status: string; reason?: string }>;
  missing_artifacts?: string[];
  limitations?: string[];
}

interface LivePlan {
  id: string;
  title: string;
  status: string;
  outcome?: string;
  outcome_reason?: string;
  outcome_details?: LiveDetails;
  current_revision?: number;
  review?: { required: boolean; status: string };
  questions?: Array<{ id: string; status: string; task_id: string }>;
  nodes: Array<{ id: string; status: string; error?: string }>;
}

interface StreamEvent {
  event_type: string;
  plan_id: string;
  payload?: { outcome?: string; outcome_details?: LiveDetails };
}

async function jsonRequest<T>(page: Page, method: 'GET' | 'POST' | 'DELETE', path: string, data?: unknown): Promise<T> {
  const response = await page.request.fetch(path, { method, ...(data === undefined ? {} : { data }) });
  const body = await response.text();
  expect(response.ok(), `${method} ${path}: ${response.status()} ${body}`).toBe(true);
  return JSON.parse(body) as T;
}

async function getPlan(page: Page, planId: string) {
  return (await jsonRequest<{ plan_graph: LivePlan }>(page, 'GET', `/api/plans/${planId}`)).plan_graph;
}

async function importPlan(page: Page, graph: Record<string, unknown>, title: string) {
  const plan = (await jsonRequest<{ plan_graph: LivePlan }>(page, 'POST', '/api/plans/from-graph', { graph, title })).plan_graph;
  // Respect an always-review deployment instead of weakening its policy.
  if (plan.review?.required && plan.review.status !== 'approved') {
    await jsonRequest(page, 'POST', `/api/plans/${plan.id}/revisions/${plan.current_revision ?? 1}/approve`, {});
  }
  return plan;
}

function toolNode(id: string, name: string, toolId: string, args: Record<string, unknown>, options: {
  dependencies?: string[]; optional?: boolean; output?: string;
} = {}) {
  // The registry requires tool_result; a second, unique output prevents the
  // acceptance evidence from depending on that shared compatibility key.
  const outputs = ['tool_result', options.output ?? `${id}_result`];
  return {
    id, name, type: 'tool_call', assigned_to: 'tool_executor', status: 'pending',
    description: 'Deterministic partial-outcome browser acceptance',
    dependencies: options.dependencies ?? [], optional: options.optional ?? false,
    required_artifacts: [], output_artifacts: outputs, parallelizable: true,
    retry_limit: 0, timeout_seconds: 20,
    contract: { version: 'v1', allowed_tools: [toolId], input_artifacts: [], output_artifacts: outputs },
    inputs: { tool_id: toolId, arguments: args },
  };
}

async function openFromLibrary(page: Page, title: string) {
  await page.getByRole('button', { name: '计划库', exact: true }).click();
  const library = page.getByRole('region', { name: '计划库', exact: true });
  await library.getByRole('textbox', { name: '搜索计划', exact: true }).fill(title);
  await library.getByRole('button', { name: '搜索', exact: true }).click();
  const card = library.locator('article').filter({ has: page.getByRole('heading', { name: title, exact: true }) });
  await card.getByRole('button', { name: '打开', exact: true }).click();
  await expect(library).toHaveCount(0);
}

async function cleanPlans(page: Page, ids: string[]) {
  const deleted: Array<{ id: string; status: number }> = [];
  for (const id of ids) {
    const snapshot = await getPlan(page, id);
    if (['in_progress', 'awaiting_approval', 'awaiting_input'].includes(snapshot.status)) {
      await jsonRequest(page, 'POST', `/api/plans/${id}/cancel`, { reason: 'partial-outcome browser acceptance cleanup' });
    }
    await expect.poll(async () => {
      const response = await page.request.delete(`/api/plans/${id}`);
      // A task cancellation can still be draining; only retry that known state.
      expect([200, 409]).toContain(response.status());
      return response.status();
    }, { timeout: 20_000 }).toBe(200);
    deleted.push({ id, status: 200 });
  }
  return deleted;
}

function watchBrowser(page: Page) {
  const errors: string[] = [];
  const consoleErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('response', (response) => {
    const path = new URL(response.url()).pathname;
    if (path.startsWith('/api/') && response.status() >= 500) serverErrors.push(`${response.status()} ${path}`);
  });
  return { errors, consoleErrors, serverErrors };
}

function saveEvidence(testInfo: TestInfo, name: string, value: unknown) {
  writeFileSync(testInfo.outputPath(name), JSON.stringify(value, null, 2));
}

async function showGraphOnSmallScreen(page: Page) {
  const switcher = page.locator('.research-assistant-pane-switcher');
  if (await switcher.isVisible()) {
    await switcher.getByRole('button', { name: /^执行图/ }).click();
  }
}

async function reloadWithoutPlanCache(page: Page, planId: string) {
  const cleared = await page.evaluate((id) => {
    const key = 'research-assistant.chat-sessions.v1';
    const state = JSON.parse(localStorage.getItem(key)!) as {
      activeSessionId: string;
      sessions: Array<{ id: string; activePlanId?: string | null; planGraph?: unknown }>;
    };
    const active = state.sessions.find((session) => session.id === state.activeSessionId);
    if (!active || active.activePlanId !== id) throw new Error('Expected plan is not the active conversation');
    active.planGraph = null;
    localStorage.setItem(key, JSON.stringify(state));
    return { activePlanId: active.activePlanId, cachedPlanGraph: active.planGraph };
  }, planId);
  const snapshotResponse = page.waitForResponse((response) =>
    response.request().method() === 'GET'
    && new URL(response.url()).pathname === `/api/plans/${planId}`
    && response.status() === 200);
  await page.reload();
  const response = await snapshotResponse;
  const restored = (await response.json() as { plan_graph: LivePlan }).plan_graph;
  expect(restored.id).toBe(planId);
  return { cleared, status: response.status(), plan: restored };
}

test('deployed partial outcome follows real SSE, restores from API and stays readable at desktop and 390px', async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  const created: string[] = [];
  const browser = watchBrowser(page);
  const streamRequests: string[] = [];
  const terminalSnapshotReads: string[] = [];
  const executionRequests: string[] = [];
  const stamp = Date.now();
  const title = `PO live partial ${stamp}`;
  const goal = '核心实验复现成功，唯一验收证据是本次真实执行生成的 core_report；额外指标的缺失不改变核心目标已经通过验收的事实。';
  const longReason = 'metric dataset missing: ' + '缺少用于可选评估的数据集，无法补全该项指标。'.repeat(10) + ` END-${stamp}`;

  // Instrument the real native EventSource only to retain terminal evidence;
  // requests still go directly through nginx to the backend's durable stream.
  await page.addInitScript(() => {
    const Native = window.EventSource;
    const events: StreamEvent[] = [];
    Object.assign(window, { __partialOutcomeEvents: events });
    window.EventSource = class extends Native {
      constructor(url: string | URL, options?: EventSourceInit) {
        super(url, options);
        this.addEventListener('plan_event', (event) => {
          const parsed = JSON.parse((event as MessageEvent).data) as StreamEvent;
          if (['plan_completed', 'plan_failed', 'plan_canceled'].includes(parsed.event_type)) events.push(parsed);
        });
      }
    };
  });

  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (path.endsWith('/stream')) streamRequests.push(path);
    if (request.method() === 'POST' && path.endsWith('/execute')) executionRequests.push(path);
  });
  page.on('response', async (response) => {
    if (response.request().method() !== 'GET' || !/\/api\/plans\/[^/]+$/.test(new URL(response.url()).pathname) || response.status() !== 200) return;
    try {
      const body = await response.json() as { plan_graph?: LivePlan };
      if (body.plan_graph?.outcome === 'partial') terminalSnapshotReads.push(body.plan_graph.id);
    } catch {
      // Navigation may intentionally discard an in-flight response body.
    }
  });

  try {
    await jsonRequest(page, 'GET', '/api/me');
    const optionalNodes = Array.from({ length: 8 }, (_, index) => toolNode(
      `optional-${index}`, `可选指标 ${index + 1}：额外数据集覆盖与稳定性评估`, 'native:fail',
      { message: index === 7 ? longReason : `metric dataset missing ${index + 1}` },
      { optional: true },
    ));
    const partial = await importPlan(page, {
      user_intent: title, intent_type: 'Code_Execution', artifacts: {},
      acceptance: {
        version: 'acceptance/v1', goals: [{ id: 'core-goal', statement: goal, evidence_refs: ['core_report'] }],
        required_artifacts: ['core_report'],
      },
      nodes: [
        {
          id: 'observe-wait', name: '真实事件订阅建立后继续', type: 'ask_user_question', assigned_to: 'general_agent', status: 'pending',
          dependencies: [], required_artifacts: [], output_artifacts: [], retry_limit: 0,
          contract: { version: 'v1', allowed_tools: ['conversation.respond'], input_artifacts: [], output_artifacts: [] },
          inputs: { question_prompt: '浏览器已建立事件订阅，可以继续核心实验吗？' },
        },
        toolNode('core', '核心实验', 'native:echo', { message: 'core experiment accepted' }, { dependencies: ['observe-wait'], output: 'core_report' }),
        ...optionalNodes,
        {
          id: 'report', name: '汇总报告', type: 'ask_user_question', assigned_to: 'general_agent', status: 'pending',
          dependencies: ['core'], optional_dependencies: optionalNodes.map((node) => node.id),
          required_artifacts: [], output_artifacts: [], retry_limit: 0,
          contract: { version: 'v1', allowed_tools: ['conversation.respond'], input_artifacts: [], output_artifacts: [] },
          inputs: { question_prompt: '继续汇总？', question_answer: 'yes' },
        },
      ],
      edges: [
        { id: 'wait-core', from: 'observe-wait', to: 'core', type: 'control' },
        { id: 'core-report', from: 'core', to: 'report', type: 'control' },
        ...optionalNodes.map((node) => ({ id: `${node.id}-report`, from: node.id, to: 'report', type: 'optional' })),
      ],
      budget: { max_task_attempts: 40, max_duration_seconds: 120 },
    }, title);
    created.push(partial.id);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/');
    await openFromLibrary(page, title);
    const banner = page.getByTestId('plan-outcome-details');
    await expect(banner).toHaveCount(0);
    await page.getByRole('button', { name: '运行计划', exact: true }).click();
    await expect.poll(() => streamRequests.filter((path) => path === `/api/plans/${partial.id}/stream`).length).toBeGreaterThan(0);
    await expect.poll(async () => (await getPlan(page, partial.id)).questions?.some((question) => question.task_id === 'observe-wait' && question.status === 'pending'), { timeout: 15_000 }).toBe(true);
    const waiting = await getPlan(page, partial.id);
    const pendingQuestion = waiting.questions!.find((question) => question.task_id === 'observe-wait' && question.status === 'pending')!;
    // Answer through the real owner-authenticated endpoint, without replacing
    // browser state. Only SSE and the app's normal snapshot refresh may reveal
    // the terminal outcome in the UI.
    await jsonRequest(page, 'POST', `/api/plans/${partial.id}/questions/${pendingQuestion.id}/answer`, { answer: 'yes' });
    await expect(banner).toHaveAttribute('data-outcome', 'partial', { timeout: 45_000 });
    await expect(banner).toContainText(goal);
    await expect(banner).toContainText('core_report');
    await expect(banner).toContainText('metric dataset missing 1');
    await expect(banner).toContainText(longReason);
    await expect(banner).toContainText('相关结论不能宣称完整');
    await expect.poll(() => terminalSnapshotReads.filter((id) => id === partial.id).length).toBeGreaterThan(0);

    const terminalEvents = await page.evaluate(() => (window as unknown as { __partialOutcomeEvents: StreamEvent[] }).__partialOutcomeEvents);
    const terminal = terminalEvents.find((event) => event.plan_id === partial.id && event.event_type === 'plan_completed');
    expect(terminal?.payload?.outcome).toBe('partial');
    expect(terminal?.payload?.outcome_details?.optional_losses).toHaveLength(8);
    const snapshot = await getPlan(page, partial.id);
    expect(snapshot.status).toBe('completed');
    expect(snapshot.outcome).toBe('partial');
    expect(snapshot.nodes.filter((node) => node.id.startsWith('optional-')).every((node) => node.status === 'failed')).toBe(true);
    expect(snapshot.outcome_details?.accepted_goals?.[0].verified).toBe(true);
    await expect(banner).toContainText(snapshot.outcome_reason!);
    expect(terminal!.payload!.outcome_details).toEqual(snapshot.outcome_details);
    expect(executionRequests).toHaveLength(1);

    await page.screenshot({ path: testInfo.outputPath('partial-desktop.png'), fullPage: true });
    const apiRestoration = await reloadWithoutPlanCache(page, partial.id);
    expect(apiRestoration.plan.outcome_details).toEqual(snapshot.outcome_details);
    expect(apiRestoration.plan.outcome_reason).toBe(snapshot.outcome_reason);
    await expect(banner).toContainText(goal);
    await expect(banner).toContainText(longReason);
    expect(executionRequests).toHaveLength(1);

    await page.setViewportSize({ width: 390, height: 844 });
    await showGraphOnSmallScreen(page);
    await expect(banner).toBeVisible();
    const geometry = await banner.evaluate((element) => {
      const rect = element.getBoundingClientRect();
      return { left: rect.left, right: rect.right, width: rect.width,
        viewport: window.innerWidth, documentWidth: document.documentElement.scrollWidth,
        scrollHeight: element.scrollHeight, clientHeight: element.clientHeight };
    });
    expect(geometry.left).toBeGreaterThanOrEqual(0);
    expect(geometry.right).toBeLessThanOrEqual(geometry.viewport + 1);
    expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewport + 1);
    const firstLoss = banner.getByTestId('optional-losses').locator('li').first();
    await firstLoss.scrollIntoViewIfNeeded();
    await expect(firstLoss).toBeVisible();
    await expect(firstLoss).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath('partial-mobile-losses.png'), fullPage: true });
    const lastLimit = banner.locator('li').filter({ hasText: '相关结论不能宣称完整' }).last();
    await lastLimit.scrollIntoViewIfNeeded();
    await expect(lastLimit).toBeVisible();
    await expect(lastLimit).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath('partial-mobile-limitations.png'), fullPage: true });

    await page.setViewportSize({ width: 1440, height: 900 });
    await page.getByRole('button', { name: '新建会话', exact: true }).click();
    await expect(banner).toHaveCount(0);
    await expect(page.getByText(goal, { exact: true })).toHaveCount(0);
    await openFromLibrary(page, title);
    await expect(banner).toContainText(goal);
    expect(executionRequests).toHaveLength(1);
    expect(browser.errors).toEqual([]);
    expect(browser.consoleErrors).toEqual([]);
    expect(browser.serverErrors).toEqual([]);
    saveEvidence(testInfo, 'partial-live-evidence.json', {
      plan: snapshot, terminal, streamRequests, terminalSnapshotReads, executionRequests,
      mobileGeometry: geometry, ...browser, apiRestoration,
      chromeVersion: page.context().browser()!.version(),
      restoration: 'local planGraph cleared before reload; real API restores details; plan library reopens after a blank conversation',
    });
  } catch (error) {
    for (const planId of created) {
      saveEvidence(testInfo, `failed-plan-${planId}.json`, await getPlan(page, planId));
      saveEvidence(testInfo, `failed-events-${planId}.json`, await jsonRequest(page, 'GET', `/api/plans/${planId}/events`));
    }
    saveEvidence(testInfo, 'failed-browser-errors.json', browser);
    throw error;
  } finally {
    saveEvidence(testInfo, 'cleanup.json', await cleanPlans(page, created));
  }
});

test('deployed review explains missing evidence and an empty structured result without reusing previous details', async ({ page }, testInfo) => {
  test.setTimeout(90_000);
  const created: string[] = [];
  const browser = watchBrowser(page);
  const stamp = Date.now();
  try {
    await jsonRequest(page, 'GET', '/api/me');
    const missingTitle = `PO live missing ${stamp}`;
    const goal = '待补充的核心验收目标';
    const missing = await importPlan(page, {
      user_intent: missingTitle, intent_type: 'Code_Execution', artifacts: {},
      acceptance: { version: 'acceptance/v1', goals: [{ id: 'missing-goal', statement: goal, evidence_refs: ['missing_report'] }], required_artifacts: ['missing_report'] },
      nodes: [toolNode('missing-core', '实际完成但未生成验收报告', 'native:echo', { message: 'execution completed' })],
      edges: [], budget: { max_task_attempts: 10, max_duration_seconds: 60 },
    }, missingTitle);
    created.push(missing.id);
    await jsonRequest(page, 'POST', `/api/plans/${missing.id}/execute`);
    await expect.poll(async () => (await getPlan(page, missing.id)).status, { timeout: 30_000 }).toBe('completed');
    const missingFinal = await getPlan(page, missing.id);
    expect(missingFinal.outcome).toBe('needs_review');
    expect(missingFinal.outcome_details?.missing_artifacts).toContain('missing_report');
    expect(missingFinal.outcome_details?.accepted_goals?.[0].verified).toBe(false);

    const emptyTitle = `PO live empty ${stamp}`;
    // General's final-answer check rejects a tool-only run with no final-answer
    // artifact. It yields needs_review and a real {mode:'strict'} detail object.
    // Imports intentionally discard caller-provided outcomes; this is not a
    // fabricated historical partial/null snapshot or a mutation of stored data.
    const empty = await importPlan(page, {
      user_intent: emptyTitle, intent_type: 'General', artifacts: {},
      nodes: [toolNode('empty-core', '没有最终回答产物的普通任务', 'native:echo', { message: 'done' })],
      edges: [], budget: { max_task_attempts: 10, max_duration_seconds: 60 },
    }, emptyTitle);
    created.push(empty.id);
    await jsonRequest(page, 'POST', `/api/plans/${empty.id}/execute`);
    await expect.poll(async () => (await getPlan(page, empty.id)).status, { timeout: 30_000 }).toBe('completed');
    const emptyFinal = await getPlan(page, empty.id);
    expect(emptyFinal.outcome).toBe('needs_review');
    expect(emptyFinal.outcome_details).toEqual({ mode: 'strict' });

    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/');
    await openFromLibrary(page, missingTitle);
    const banner = page.getByTestId('plan-outcome-details');
    await expect(banner).toHaveAttribute('data-outcome', 'needs_review');
    await expect(banner).toContainText('尚未通过验证的核心目标');
    await expect(banner).toContainText(goal);
    await expect(banner).toContainText(missingFinal.outcome_details!.accepted_goals![0].reason!);
    await expect(banner).toContainText('缺少或不可信的验收产物');
    await expect(banner).toContainText('missing_report');
    await expect(banner.getByText('已通过的核心目标', { exact: true })).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath('review-missing-evidence.png'), fullPage: true });

    await openFromLibrary(page, emptyTitle);
    await expect(banner).toContainText('该计划未提供结构化验收详情');
    await expect(banner).toContainText(emptyFinal.outcome_reason!);
    await expect(banner.getByText(goal, { exact: true })).toHaveCount(0);
    await expect(banner.getByText('缺少或不可信的验收产物', { exact: true })).toHaveCount(0);
    const apiRestoration = await reloadWithoutPlanCache(page, empty.id);
    expect(apiRestoration.plan.outcome_details).toEqual({ mode: 'strict' });
    expect(apiRestoration.plan.outcome_reason).toBe(emptyFinal.outcome_reason);
    await expect(banner).toContainText('该计划未提供结构化验收详情');
    await expect(banner).toContainText(emptyFinal.outcome_reason!);
    await page.screenshot({ path: testInfo.outputPath('review-empty-details-restored.png'), fullPage: true });
    expect(browser.errors).toEqual([]);
    expect(browser.consoleErrors).toEqual([]);
    expect(browser.serverErrors).toEqual([]);
    saveEvidence(testInfo, 'review-live-evidence.json', {
      missing: missingFinal, empty: emptyFinal, ...browser, apiRestoration,
      chromeVersion: page.context().browser()!.version(),
    });
  } finally {
    saveEvidence(testInfo, 'cleanup.json', await cleanPlans(page, created));
  }
});
