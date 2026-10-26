import { Buffer } from 'node:buffer';
import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { PDFDocument, StandardFonts } from 'pdf-lib';

const planGraph = JSON.parse(readFileSync(new URL('../fixtures/a11y-plan.json', import.meta.url), 'utf8'));

const seriousViolations = (violations: Awaited<ReturnType<AxeBuilder['analyze']>>['violations']) =>
  violations.filter((violation) => violation.impact === 'critical' || violation.impact === 'serious');

async function makePDF(): Promise<Buffer> {
  const document = await PDFDocument.create();
  const page = document.addPage([420, 300]);
  const font = await document.embedFont(StandardFonts.Helvetica);
  page.drawText('Hello PDF', { x: 48, y: 220, size: 24, font });
  return Buffer.from(await document.save());
}

async function expectNoSeriousA11yViolations(page: Page, label: string) {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .analyze();
  const violations = seriousViolations(results.violations);
  expect(
    violations,
    label + ' serious/critical axe violations:\n' + violations.map((item) => `- ${item.id}: ${item.help} (${item.nodes.length} nodes)`).join('\n'),
  ).toEqual([]);
}

test('session menu is keyboard operable and returns focus', async ({ page }) => {
  await page.goto('/');
  const trigger = page.locator('button[aria-haspopup="listbox"]');
  await trigger.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText('往期稿次')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByText('往期稿次')).toBeHidden();
  await expect(trigger).toBeFocused();
});

test('home has no serious accessibility violations', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('button', { name: '发送消息' })).toBeVisible();
  await expectNoSeriousA11yViolations(page, 'home');
});

test('session menu has no serious accessibility violations', async ({ page }) => {
  await page.goto('/');
  await page.locator('button[aria-haspopup="listbox"]').click();
  await expect(page.getByText('往期稿次')).toBeVisible();
  await expectNoSeriousA11yViolations(page, 'session-menu');
});

test('PDF reader has no serious accessibility violations', async ({ page }) => {
  const pdfBytes = await makePDF();
  await page.route('**/api/uploads', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'upload-a11y',
        name: 'a11y.pdf',
        content_type: 'application/pdf',
        size: pdfBytes.length,
        sha256: 'a11y-sha',
        content_url: '/api/uploads/upload-a11y/content',
        created_at: '2026-10-23T00:00:00Z',
      }),
    });
  });
  await page.route('**/api/uploads/upload-a11y/content', (route) => route.fulfill({ status: 200, contentType: 'application/pdf', body: pdfBytes }));
  await page.route('**/api/uploads/upload-a11y/document', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      upload_id: 'upload-a11y',
      name: 'a11y.pdf',
      document: { status: 'extracted', page_count: 1, characters: 9, extractor: 'pdftotext' },
      pages: [{ number: 1, chars: 9 }],
    }),
  }));

  await page.goto('/');
  await page.locator('input[type=file]').setInputFiles({ name: 'a11y.pdf', mimeType: 'application/pdf', buffer: pdfBytes });
  await page.getByRole('button', { name: '打开 a11y.pdf' }).click();
  await expect(page.getByText('共 1 页')).toBeVisible({ timeout: 20_000 });
  await expectNoSeriousA11yViolations(page, 'pdf-reader');
});

test('mobile shell has no serious accessibility violations', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page.locator('.research-assistant-mobile-tabbar')).toBeVisible();
  await expectNoSeriousA11yViolations(page, 'mobile');
});

test('plan node sidebar has no serious accessibility violations', async ({ page }) => {
  const state = {
    userId: 'a11y-user',
    activeSessionId: 'a11y-session',
    sessions: [
      {
        id: 'a11y-session',
        title: 'A11y smoke',
        createdAt: '2026-10-23T00:00:00Z',
        updatedAt: '2026-10-23T00:00:00Z',
        chatHistory: [],
        activePlanId: 'a11y-plan',
        intentContext: { raw_intent: 'Accessibility smoke plan', intent_type: 'Custom_Benchmark', entities: {}, constraints: {}, metadata: {} },
        planGraph,
      },
    ],
  };
  await page.addInitScript(({ key, value }) => {
    window.localStorage.setItem(key, value);
  }, { key: 'research-assistant.chat-sessions.v1', value: JSON.stringify(state) });
  await page.route(/\/api\/plans\/a11y-plan\/events$/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ events: [] }) });
  });
  await page.route(/\/api\/plans\/a11y-plan$/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ plan_graph: planGraph }) });
  });

  await page.goto('/');
  const node = page.locator('.react-flow__node').first();
  await expect(node).toBeVisible();
  await node.click();
  await expect(page.locator('.research-assistant-execution-sidebar').getByText('页边批注')).toBeVisible();
  const logsTab = page.getByRole('tab', { name: '日志' });
  const reportTab = page.getByRole('tab', { name: '评测报告' });
  await expect(reportTab).toHaveAttribute('aria-selected', 'true');
  await reportTab.focus();
  await page.keyboard.press('ArrowLeft');
  await expect(logsTab).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowRight');
  await expect(reportTab).toHaveAttribute('aria-selected', 'true');
  await expectNoSeriousA11yViolations(page, 'plan-sidebar');
});