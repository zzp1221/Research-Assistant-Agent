import { expect, test } from '@playwright/test';

test.beforeEach(async ({ page }) => {
  // This shell test does not require a running sandbox or provider service.
  await page.route((url) => url.pathname === '/desktop/state', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ state: 'running' }),
  }));
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const path = new URL(route.request().url()).pathname;
    const body = path === '/api/me' ? { user_id: 'shell-user', session_id: 'shell-session' }
      : path === '/api/approvals' ? { items: [] }
        : path.endsWith('/alerts') ? { alerts: [] } : {};
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
});

test('home page renders the research workbench', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('complementary', { name: '工作台导航' })).toBeVisible();
  await expect(page.getByRole('button', { name: '发送消息' })).toBeVisible();
  await expect(page.getByRole('button', { name: '添加附件' })).toBeVisible();
  await expect(page.locator('.research-assistant-graph-workspace')).toBeVisible();
});

test('mobile layout switches panes without overflow or console errors', async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.getByRole('textbox', { name: '消息输入框' })).toBeVisible();
  await expect(page.locator('.research-assistant-mobile-tabbar')).toBeVisible();

  await page.getByRole('button', { name: /执行图/ }).click();
  await expect(page.locator('.research-assistant-app-shell')).toHaveAttribute('data-mobile-pane', 'graph');
  await page.locator('.research-assistant-pane-switcher').getByRole('button', { name: '对话', exact: true }).click();
  await expect(page.locator('.research-assistant-app-shell')).toHaveAttribute('data-mobile-pane', 'chat');

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  expect(consoleErrors).toEqual([]);
});
