import { expect, test } from '@playwright/test';

test('home page renders the research workbench', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('Research Assistant Agent').first()).toBeVisible();
  await expect(page.getByRole('button', { name: '发送消息' })).toBeVisible();
  await expect(page.getByText('试试推荐指令')).toBeVisible();
});
