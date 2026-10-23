import { Buffer } from 'node:buffer';
import { expect, test } from '@playwright/test';
import { PDFDocument, StandardFonts } from 'pdf-lib';

async function makePDF(): Promise<Buffer> {
  const document = await PDFDocument.create();
  const page = document.addPage([420, 300]);
  const font = await document.embedFont(StandardFonts.Helvetica);
  page.drawText('Hello PDF', { x: 48, y: 220, size: 24, font });
  return Buffer.from(await document.save());
}

test('uploaded PDF opens locally and runs a full translation job', async ({ page }) => {
  const pdfBytes = await makePDF();
  const job = {
    id: 'job-1',
    upload_id: 'upload-1',
    target_lang: 'zh',
    status: 'completed',
    total_pages: 1,
    completed_pages: 1,
    failed_pages: 0,
    current_page: 1,
    pages: [{ number: 1, status: 'completed', chars: 9, updated_at: '2026-10-22T00:00:00Z' }],
    created_at: '2026-10-22T00:00:00Z',
    finished_at: '2026-10-22T00:00:00Z',
  };

  await page.route('**/api/uploads', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'upload-1',
        name: 'tiny.pdf',
        content_type: 'application/pdf',
        size: pdfBytes.length,
        sha256: 'e2e-sha',
        content_url: '/api/uploads/upload-1/content',
        created_at: '2026-10-22T00:00:00Z',
      }),
    });
  });
  await page.route('**/api/uploads/upload-1/content', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/pdf', body: pdfBytes });
  });
  await page.route('**/api/uploads/upload-1/document', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        upload_id: 'upload-1',
        name: 'tiny.pdf',
        document: { status: 'extracted', page_count: 1, characters: 9, extractor: 'pdftotext' },
        pages: [{ number: 1, chars: 9 }],
      }),
    });
  });
  await page.route('**/api/uploads/upload-1/translation-jobs', async (route) => {
    await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ job }) });
  });
  await page.route('**/api/uploads/upload-1/translation-jobs/job-1', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ job }) });
  });
  await page.route('**/api/uploads/upload-1/translation-jobs/job-1/pages/1', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ text: '译文内容' }) });
  });

  await page.goto('/');
  await page.locator('input[type=file]').setInputFiles({ name: 'tiny.pdf', mimeType: 'application/pdf', buffer: pdfBytes });
  await page.getByRole('button', { name: '打开 tiny.pdf' }).click();
  await expect(page.getByText('共 1 页')).toBeVisible({ timeout: 20_000 });

  await page.getByRole('button', { name: '全文翻译' }).click();
  await page.getByRole('button', { name: '开始全文翻译' }).click();
  await expect(page.getByText('1/1 页')).toBeVisible();
  await page.getByTitle('1 · 已完成').click();
  await expect(page.getByText('译文内容')).toBeVisible();
});

test('AI translation popover stays inside the PDF viewer after zooming out', async ({ page }) => {
  const pdfBytes = await makePDF();
  await page.setViewportSize({ width: 1024, height: 500 });

  await page.route('**/api/uploads', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue();
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'upload-1',
        name: 'tiny.pdf',
        content_type: 'application/pdf',
        size: pdfBytes.length,
        sha256: 'e2e-sha',
        content_url: '/api/uploads/upload-1/content',
        created_at: '2026-10-22T00:00:00Z',
      }),
    });
  });
  await page.route('**/api/uploads/upload-1/content', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/pdf', body: pdfBytes });
  });
  await page.route('**/api/uploads/upload-1/document', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        upload_id: 'upload-1',
        name: 'tiny.pdf',
        document: { status: 'extracted', page_count: 1, characters: 9, extractor: 'pdftotext' },
        pages: [{ number: 1, chars: 9 }],
      }),
    });
  });
  await page.route('**/api/uploads/upload-1/assist', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        mode: 'translate',
        result: '这是一段很长的翻译结果，用于验证翻译浮层在缩放后仍然完整可见。'.repeat(40),
        bullets: ['要点一', '要点二'],
        terms: [],
        model: 'deepseek-v4.1-flash-1017',
        cached: false,
      }),
    });
  });

  await page.goto('/');
  await page.locator('input[type=file]').setInputFiles({ name: 'tiny.pdf', mimeType: 'application/pdf', buffer: pdfBytes });
  await page.getByRole('button', { name: '打开 tiny.pdf' }).click();
  await expect(page.getByText('共 1 页')).toBeVisible({ timeout: 20_000 });
  const textLayer = page.locator('.rpv-core__text-layer-text').first();
  await textLayer.waitFor({ timeout: 20_000 });
  const textBox = await textLayer.boundingBox();
  expect(textBox).not.toBeNull();
  if (!textBox) return;

  await page.mouse.move(textBox.x + 2, textBox.y + textBox.height / 2);
  await page.mouse.down();
  await page.mouse.move(textBox.x + textBox.width - 2, textBox.y + textBox.height / 2, { steps: 8 });
  await page.mouse.up();
  await page.getByRole('button', { name: '翻译', exact: true }).click();
  await expect(page.getByTestId('ai-translation-popover')).toBeVisible();

  await page.getByRole('button', { name: 'Zoom out' }).click();
  await page.getByRole('button', { name: 'Zoom out' }).click();

  const popoverBox = await page.getByTestId('ai-translation-popover').boundingBox();
  const viewerBox = await page.locator('.rpv-default-layout__body').boundingBox();
  expect(popoverBox).not.toBeNull();
  expect(viewerBox).not.toBeNull();
  if (!popoverBox || !viewerBox) return;

  expect(popoverBox.y).toBeGreaterThanOrEqual(viewerBox.y - 1);
  expect(popoverBox.y + popoverBox.height).toBeLessThanOrEqual(viewerBox.y + viewerBox.height + 1);
  expect(popoverBox.x).toBeGreaterThanOrEqual(0);
  expect(popoverBox.x + popoverBox.width).toBeLessThanOrEqual(page.viewportSize()!.width + 1);
});
