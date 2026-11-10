import { Buffer } from 'node:buffer';
import process from 'node:process';
import { expect, test, type Page } from '@playwright/test';
import { PDFDocument, StandardFonts } from 'pdf-lib';

const liveApi = process.env.WORKBENCH_LIVE_API;
test.skip(!liveApi, 'Set WORKBENCH_LIVE_API to an isolated backend with document extraction and a configured chat model.');

async function seedSession(page: Page, graph: Record<string, unknown>, userId: string, sessionId: string) {
  await page.addInitScript(({ plan, user, session }) => {
    localStorage.setItem('research-assistant.chat-sessions.v1', JSON.stringify({
      userId: user, activeSessionId: session,
      sessions: [{ id: session, title: '真实后端工作台验收', createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
        chatHistory: [], activePlanId: plan.id, planGraph: null,
        intentContext: { raw_intent: plan.user_intent, intent_type: plan.intent_type, entities: {}, constraints: {}, metadata: {} },
      }],
    }));
  }, { plan: graph, user: userId, session: sessionId });
}

test('live revision execution survives refresh and session restoration without a second execute', async ({ page }, testInfo) => {
  test.setTimeout(150_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  const identity = await (await page.request.get(`${liveApi}/api/me`)).json();
  // Use an owner-scoped native tool with enough time for a refresh and a
  // session round trip. The bundled timeout fixture is limited to five seconds.
  const toolId = `workbench:sleep-${Date.now()}`;
  const tool = await page.request.post(`${liveApi}/api/tools`, { data: {
    id: toolId, name: 'Workbench observation wait', version: '1.0.0', transport: 'native', native: 'sleep',
    timeout_sec: 60, approval: 'never', side_effect: false,
    input_schema: JSON.stringify({ type: 'object', properties: { ms: { type: 'integer', minimum: 1, maximum: 60000 } }, required: ['ms'], additionalProperties: false }),
  } });
  expect(tool.status(), await tool.text()).toBe(201);
  const nodes = Array.from({ length: 1 }, (_, index) => ({
    id: `wait-${index}`, name: `真实调度步骤 ${index + 1}`, type: 'tool_call', assigned_to: 'tool_executor', status: 'pending',
    dependencies: [], required_artifacts: [], output_artifacts: ['tool_result'],
    parallelizable: false, retry_limit: 0,
    inputs: { tool_id: toolId, arguments: { ms: 48000 } },
    contract: { version: 'v1', allowed_tools: [toolId] },
  }));
  const created = await page.request.post(`${liveApi}/api/plans/from-graph`, { data: {
    require_review: true,
    graph: { user_intent: '真实后端刷新接续验收（48 秒原生等待任务）', intent_type: 'General', nodes,
      edges: nodes.slice(1).map((node, index) => ({ id: `edge-${index}`, from: nodes[index].id, to: node.id, type: 'depends_on' })),
    },
  } });
  expect(created.status(), await created.text()).toBe(201);
  const graph = (await created.json()).plan_graph;
  await seedSession(page, graph, identity.user_id, identity.session_id);
  const executes: string[] = [];
  const streamUrls: string[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (request.method() === 'POST' && url.pathname.endsWith('/execute')) executes.push(url.pathname);
    if (url.pathname.endsWith('/stream')) streamUrls.push(request.url());
  });
  await page.goto('/');
  await page.getByRole('button', { name: '计划审阅', exact: true }).click();
  const review = page.getByRole('dialog', { name: '计划审阅', exact: true });
  await review.getByRole('textbox', { name: '节点名称', exact: true }).fill('审阅后保存的新节点名称');
  await review.getByRole('button', { name: '保存并校验', exact: true }).click();
  await expect(review.getByText('校验通过（r2）', { exact: true })).toBeVisible();
  await expect(review).toBeVisible();
  await review.getByRole('button', { name: '批准并运行 r2', exact: true }).click();
  await expect(review).toHaveCount(0);
  await expect.poll(() => executes.length).toBe(1);
  await expect.poll(() => streamUrls.length).toBeGreaterThanOrEqual(1);
  await expect.poll(async () => (await (await page.request.get(`${liveApi}/api/plans/${graph.id}`)).json()).plan_graph.status).toBe('in_progress');
  await page.screenshot({ path: testInfo.outputPath('live-running-before-refresh.png') });
  const streamsBeforeRefresh = streamUrls.length;
  await page.reload();
  await expect.poll(() => streamUrls.length).toBeGreaterThan(streamsBeforeRefresh);
  const resumedUrl = new URL(streamUrls.at(-1)!);
  expect(Number(resumedUrl.searchParams.get('after_seq'))).toBeGreaterThan(0);
  expect(executes).toHaveLength(1);
  await expect(page.getByRole('button', { name: /运行计划|批准并运行/ })).toHaveCount(0);

  // A second local conversation has no plan; returning must only observe.
  await page.getByRole('button', { name: '新建会话', exact: true }).click();
  await page.locator('button[aria-haspopup="listbox"]').click();
  const streamsBeforeReturn = streamUrls.length;
  await page.getByRole('option').filter({ hasText: '真实后端工作台验收' }).click();
  await expect.poll(() => streamUrls.length).toBeGreaterThan(streamsBeforeReturn);
  expect(executes).toHaveLength(1);

  await expect.poll(async () => (await (await page.request.get(`${liveApi}/api/plans/${graph.id}`)).json()).plan_graph.status, { timeout: 90_000 }).toBe('completed');
  await expect(page.locator('.research-assistant-status-bar')).toContainText('已完成');
  const events = (await (await page.request.get(`${liveApi}/api/plans/${graph.id}/events`)).json()).events;
  expect(events.filter((event: { event_type: string }) => event.event_type === 'plan_completed')).toHaveLength(1);
  expect(events.every((event: { seq: number }) => event.seq > 0)).toBe(true);
  const finalPlan = (await (await page.request.get(`${liveApi}/api/plans/${graph.id}`)).json()).plan_graph;
  expect(finalPlan.current_revision).toBe(2);
  expect(finalPlan.nodes[0].name).toBe('审阅后保存的新节点名称');
  expect(executes).toEqual([`/api/plans/${graph.id}/revisions/2/execute`]);
  await page.screenshot({ path: testInfo.outputPath('live-completed-after-restoration.png') });
});

test('live multi-page PDF supports cited follow-up chat and restores the reading page', async ({ page }, testInfo) => {
  test.setTimeout(180_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  const document = await PDFDocument.create();
  const font = await document.embedFont(StandardFonts.Helvetica);
  for (const text of [
    'INTRODUCTION: This document contains three scientific measurements.',
    'RESULT: The measured treatment value is 42.73 units.',
    'CONTROL: The measured control value is 19.26 units.',
  ]) {
    const pdfPage = document.addPage([600, 700]);
    pdfPage.drawText(text, { x: 32, y: 620, size: 14, font });
  }
  const bytes = Buffer.from(await document.save());
  const chats: Array<Record<string, unknown>> = [];
  let planRequests = 0;
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname;
    if (request.method() === 'POST' && path === '/api/chat') chats.push(request.postDataJSON());
    if (request.method() === 'POST' && path === '/api/plan') planRequests++;
  });
  await page.goto('/');
  await page.locator('input[type=file]').setInputFiles({ name: 'workbench-three-pages.pdf', mimeType: 'application/pdf', buffer: bytes });
  await page.getByRole('button', { name: '打开 workbench-three-pages.pdf', exact: true }).click();
  await expect(page.getByText('共 3 页', { exact: true })).toBeVisible({ timeout: 30_000 });
  const reader = page.locator('.research-assistant-reader-workspace');
  await reader.getByRole('button', { name: 'Next page', exact: true }).click();
  await expect(reader.getByRole('textbox', { name: 'Enter a page number', exact: true })).toHaveValue('2');
  const text = reader.locator('.rpv-core__text-layer-text').filter({ hasText: 'RESULT:' }).first();
  await expect(text).toBeVisible();
  await page.getByRole('textbox', { name: '消息输入框', exact: true }).fill('请解释这个测量值，并注明来源页码。');
  // Filling the composer can move the visible reader. Select only once the
  // text layer actually owns the hit point, then use its current geometry.
  await text.scrollIntoViewIfNeeded();
  await expect.poll(() => text.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return element.contains(document.elementFromPoint(rect.left + 2, rect.top + rect.height / 2));
  })).toBe(true);
  const box = await text.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.move(box!.x + 2, box!.y + box!.height / 2);
  await page.mouse.down();
  await page.mouse.move(box!.x + box!.width - 2, box!.y + box!.height / 2, { steps: 10 });
  await page.mouse.up();
  const ask = page.getByRole('button', { name: '询问 AI', exact: true });
  await expect(ask).toBeVisible();
  await ask.click();
  await expect(page.getByRole('textbox', { name: '消息输入框', exact: true })).toHaveValue('请解释这个测量值，并注明来源页码。');
  await expect(page.getByTestId('composer-document-reference')).toContainText('workbench-three-pages.pdf');
  await expect(page.getByTestId('composer-document-reference')).toContainText('第 2 页');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  await expect.poll(() => chats.length).toBe(1);
  expect((chats[0].document_references as Array<{ page: number }>)[0].page).toBe(2);
  await expect(page.locator('.research-assistant-chat-message.is-assistant').last()).toContainText('42.73', { timeout: 90_000 });
  expect(planRequests).toBe(0);
  await expect(reader).toBeVisible();
  await page.getByRole('textbox', { name: '消息输入框', exact: true }).fill('这个值对应的对照值是多少？请使用相邻页原文回答。');
  await page.getByRole('button', { name: '发送消息', exact: true }).click();
  await expect(page.locator('.research-assistant-chat-message.is-assistant').last()).toContainText('19.26', { timeout: 90_000 });
  expect(planRequests).toBe(0);
  await page.screenshot({ path: testInfo.outputPath('live-pdf-continuous-chat.png') });
  await page.reload();
  await expect(page.getByText('共 3 页', { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('.research-assistant-reader-workspace').getByRole('textbox', { name: 'Enter a page number' })).toHaveValue('2');
});
