import { expect, test } from '@playwright/test';

test('home page renders the research workbench', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('Research Assistant').first()).toBeVisible();
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
  await expect(page.getByText('Research Assistant').first()).toBeVisible();
  await expect(page.locator('.research-assistant-mobile-tabbar')).toBeVisible();

  await page.getByRole('button', { name: /执行图/ }).click();
  await expect(page.locator('.research-assistant-app-shell')).toHaveAttribute('data-mobile-pane', 'graph');
  await page.getByRole('button', { name: '对话' }).click();
  await expect(page.locator('.research-assistant-app-shell')).toHaveAttribute('data-mobile-pane', 'chat');

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  expect(consoleErrors).toEqual([]);
});