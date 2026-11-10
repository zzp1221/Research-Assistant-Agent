import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const fixture = JSON.parse(readFileSync(new URL('../fixtures/a11y-plan.json', import.meta.url), 'utf8'));
const plan = { ...fixture, user_intent: '超长中文研究计划标题：验证审阅、运行恢复、论文连续追问与不同尺寸工作台中的主要操作始终可用' };

async function openWorkbench(page: Page, preferences: Record<string, string> = {}) {
  await page.addInitScript(({ graph, stored }) => {
    const state = {
      userId: 'a11y-user', activeSessionId: 'a11y-session',
      sessions: [{
        id: 'a11y-session', title: '工作台交互验收', createdAt: '2026-11-09T00:00:00Z', updatedAt: '2026-11-09T00:00:00Z',
        chatHistory: [], activePlanId: graph.id,
        intentContext: { raw_intent: graph.user_intent, intent_type: graph.intent_type, entities: {}, constraints: {}, metadata: {} },
        planGraph: null,
      }],
    };
    localStorage.setItem('research-assistant.chat-sessions.v1', JSON.stringify(state));
    for (const [key, value] of Object.entries(stored)) localStorage.setItem(key, value);
  }, { graph: plan, stored: preferences });
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const path = new URL(route.request().url()).pathname;
    let body: unknown = {};
    if (path === '/api/me') body = { user_id: 'a11y-user', session_id: 'a11y-session' };
    else if (path === `/api/plans/${plan.id}`) body = { plan_graph: plan };
    else if (path.endsWith('/events')) body = { events: [] };
    else if (path === '/api/plans') body = { plans: [], total: 0, next_cursor: '' };
    else if (path === '/api/approvals') body = { items: [] };
    else if (path.endsWith('/alerts')) body = { alerts: [] };
    else if (path === '/api/budgets') body = { budget: null };
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
  await page.goto('/');
  await expect(page.locator('.react-flow__node').first()).toBeVisible();
}

async function assertNoPageOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(1);
}

for (const size of [
  { width: 1440, height: 900 }, { width: 1366, height: 768 },
  { width: 1280, height: 768 }, { width: 1279, height: 768 },
  { width: 1024, height: 768 }, { width: 1024, height: 500 },
  // Effective CSS viewport of a 1440x900 window at 125% and 150% zoom.
  { width: 1152, height: 720 }, { width: 960, height: 600 },
  { width: 768, height: 768 }, { width: 767, height: 768 },
  { width: 390, height: 844 }, { width: 320, height: 640 },
  { width: 844, height: 390 },
]) {
  test(`L-01–05 detail and composer geometry at ${size.width}x${size.height}`, async ({ page }, testInfo) => {
    await page.setViewportSize(size);
    await openWorkbench(page);
    const shell = page.locator('.research-assistant-app-shell');
    const mode = await shell.getAttribute('data-layout-mode');
    const node = page.locator('.react-flow__node').first();
    await node.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('.research-assistant-execution-sidebar')).toBeVisible();
    const detailsMode = await shell.getAttribute('data-details-mode');
    if (size.width < 1280) expect(detailsMode).not.toBe('docked');
    if (size.width < 768) expect(detailsMode).toBe('fullscreen');
    if (mode === 'wide' || mode === 'medium') {
      const canvas = await page.locator('.research-assistant-graph-panel').boundingBox();
      expect(canvas?.width).toBeGreaterThanOrEqual(479);
    }
    await assertNoPageOverflow(page);
    await page.screenshot({ path: testInfo.outputPath(`details-${size.width}x${size.height}.png`), fullPage: true });
    await page.keyboard.press('Escape');
    await expect(page.locator('.research-assistant-execution-sidebar')).toHaveCount(0);
    if (detailsMode !== 'docked') await expect(node).toBeFocused();

    if (mode === 'single' || mode === 'mobile') {
      await page.locator('.research-assistant-pane-switcher').getByRole('button', { name: '对话', exact: true }).click();
    }
    await expect(page.getByRole('button', { name: '发送消息' })).toBeVisible();
    await page.getByRole('textbox').fill('保留这段尚未发送的研究问题');
    const send = await page.getByRole('button', { name: '发送消息' }).boundingBox();
    const navigation = await page.locator('.research-assistant-rail').boundingBox();
    expect(send).not.toBeNull();
    if (size.width < 768) expect(send!.y + send!.height).toBeLessThanOrEqual(navigation!.y + 1);
    await assertNoPageOverflow(page);
    await page.screenshot({ path: testInfo.outputPath(`chat-${size.width}x${size.height}.png`), fullPage: true });
  });
}

test('L-06/08 resizing preserves preferences and chat drafts across collapse and narrow windows', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1000 });
  await openWorkbench(page, { 'ra.leftPanelWidth': '24', 'ra.sidebarWidth': 'invalid' });
  await page.getByRole('textbox').fill('收起后仍保留的草稿');
  const divider = page.getByRole('separator', { name: '调整对话栏宽度' });
  await divider.focus();
  await page.keyboard.press('ArrowRight');
  const preferred = await divider.getAttribute('aria-valuenow');
  await page.getByRole('button', { name: '收起对话', exact: true }).click();
  await expect(page.getByRole('textbox')).toBeHidden();
  await page.getByRole('button', { name: '展开对话', exact: true }).click();
  await expect(page.getByRole('textbox')).toHaveValue('收起后仍保留的草稿');
  await page.setViewportSize({ width: 768, height: 700 });
  await expect(page.locator('.research-assistant-app-shell')).toHaveAttribute('data-layout-mode', 'single');
  await page.setViewportSize({ width: 1920, height: 1000 });
  await expect(divider).toHaveAttribute('aria-valuenow', preferred!);
  await expect(page.getByRole('textbox')).toHaveValue('收起后仍保留的草稿');
});

test('L-07 modal details contain keyboard focus and return it to the node', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await openWorkbench(page);
  const node = page.locator('.react-flow__node').first();
  await node.focus();
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog', { name: '节点详情', exact: true });
  await expect(dialog).toBeVisible();
  for (let i = 0; i < 18; i++) {
    await page.keyboard.press('Tab');
    expect(await dialog.evaluate((element) => element.contains(document.activeElement))).toBe(true);
  }
  const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
  expect(axe.violations.filter((item) => item.impact === 'critical' || item.impact === 'serious')).toEqual([]);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(node).toBeFocused();
});

test('P-06 a refreshed plan snapshot preserves the mobile conversation and draft', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openWorkbench(page);
  await page.locator('.research-assistant-pane-switcher').getByRole('button', { name: '对话', exact: true }).click();
  await page.getByRole('textbox', { name: '消息输入框' }).fill('进度刷新时保留我的追问');
  const refreshedTitle = '刷新后的服务端计划快照';
  await page.route((url) => url.pathname === `/api/plans/${plan.id}`, (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ plan_graph: {
      ...plan, user_intent: refreshedTitle, nodes: plan.nodes.map((node: Record<string, unknown>) => ({ ...node, name: refreshedTitle })),
    } }),
  }));
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(page.locator('.react-flow__node').first()).toContainText(refreshedTitle);
  await expect(page.locator('.research-assistant-app-shell')).toHaveAttribute('data-mobile-pane', 'chat');
  await expect(page.getByRole('textbox', { name: '消息输入框' })).toBeVisible();
  await expect(page.getByRole('textbox', { name: '消息输入框' })).toHaveValue('进度刷新时保留我的追问');
});
