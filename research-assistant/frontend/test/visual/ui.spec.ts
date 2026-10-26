import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';

const planGraph = JSON.parse(readFileSync(new URL('../fixtures/a11y-plan.json', import.meta.url), 'utf8'));

const sessionState = {
  userId: 'visual-user',
  activeSessionId: 'visual-session',
  sessions: [
    {
      id: 'visual-session',
      title: '视觉回归示例会话',
      createdAt: '2026-10-23T00:00:00Z',
      updatedAt: '2026-10-23T00:00:00Z',
      chatHistory: [
        { role: 'user', text: '验证这篇论文的隐藏评测证据。' },
        { role: 'system', text: '计划已完成，节点结果可在右侧查看。' },
      ],
      activePlanId: 'a11y-plan',
      intentContext: { raw_intent: 'Accessibility smoke plan', intent_type: 'Custom_Benchmark', entities: {}, constraints: {}, metadata: {} },
      planGraph,
    },
  ],
};

async function seedPlanSession(page: Page) {
  await page.addInitScript(({ key, value }) => {
    window.localStorage.setItem(key, value);
  }, { key: 'research-assistant.chat-sessions.v1', value: JSON.stringify(sessionState) });
  await page.route(/\/api\/plans\/a11y-plan\/events$/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ events: [] }) });
  });
  await page.route(/\/api\/plans\/a11y-plan$/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ plan_graph: planGraph }) });
  });
}

test('home empty state', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('本页尚无图版')).toBeVisible();
  await expect(page).toHaveScreenshot('home.png');
});

test('session menu open', async ({ page }) => {
  await page.goto('/');
  await page.locator('button[aria-haspopup="listbox"]').click();
  await expect(page.getByText('往期稿次')).toBeVisible();
  await expect(page).toHaveScreenshot('session-menu.png');
});

test('plan graph', async ({ page }) => {
  await seedPlanSession(page);
  await page.goto('/');
  await expect(page.locator('.react-flow__node').first()).toBeVisible();
  await expect(page).toHaveScreenshot('plan-graph.png');
});

test('node sidebar logs', async ({ page }) => {
  await seedPlanSession(page);
  await page.goto('/');
  await page.locator('.react-flow__node').first().click();
  await expect(page.locator('.research-assistant-execution-sidebar').getByText('页边批注')).toBeVisible();
  await expect(page).toHaveScreenshot('node-sidebar.png');
});

test('benchmark report', async ({ page }) => {
  await seedPlanSession(page);
  await page.goto('/');
  await page.locator('.react-flow__node').first().click();
  await page.getByRole('tab', { name: '评测报告' }).click();
  await expect(page.getByText('已通过隐藏标签验收')).toBeVisible();
  await expect(page).toHaveScreenshot('benchmark-report.png');
});

test('mobile home', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.locator('.research-assistant-mobile-tabbar')).toBeVisible();
  await expect(page).toHaveScreenshot('mobile-home.png');
});