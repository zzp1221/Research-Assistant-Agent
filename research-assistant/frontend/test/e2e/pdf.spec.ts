import { Buffer } from 'node:buffer';
import { expect, test, type Page } from '@playwright/test';
import { PDFDocument, StandardFonts } from 'pdf-lib';

async function makePDF(): Promise<Buffer> {
  const document = await PDFDocument.create();
  const page = document.addPage([420, 300]);
  const font = await document.embedFont(StandardFonts.Helvetica);
  page.drawText('Hello PDF', { x: 48, y: 220, size: 24, font });
  return Buffer.from(await document.save());
}

async function openTranslationPDF(page: Page, resultMode: 'long' | 'short' | 'error' = 'long', gate?: Promise<void>) {
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
    if (gate) await gate;
    if (resultMode === 'error') {
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'assist unavailable' }) });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        mode: 'translate',
        result: resultMode === 'short' ? '短译文' : '这是一段很长的翻译结果，用于验证翻译浮层在缩放后仍然完整可见。'.repeat(40),
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
  await page.setViewportSize({ width: 1024, height: 500 });

  await openTranslationPDF(page);

  await page.getByRole('button', { name: 'Zoom out' }).click();
  await page.getByRole('button', { name: 'Zoom out' }).click();

  const assertInsideReader = async () => {
    await expect.poll(async () => { const geometry = await page.evaluate(() => {
      const popup = document.querySelector<HTMLElement>('[data-testid="ai-translation-popover"]');
      const body = document.querySelector<HTMLElement>('.rpv-default-layout__body');
      const toolbar = document.querySelector<HTMLElement>('.rpv-default-layout__toolbar');
      if (!popup || !body || !toolbar) return { fits: false };
      const p = popup.getBoundingClientRect(), b = body.getBoundingClientRect(), t = toolbar.getBoundingClientRect();
      return {
        fits: getComputedStyle(popup).visibility === 'visible' && p.x >= b.x - 1 && p.y >= t.bottom - 1
          && p.right <= b.right + 1 && p.bottom <= b.bottom + 1,
        popup: { x: p.x, y: p.y, width: p.width, height: p.height, visibility: getComputedStyle(popup).visibility },
        body: { x: b.x, y: b.y, width: b.width, height: b.height }, toolbarBottom: t.bottom,
      };
    }); return geometry.fits ? 'inside reader' : JSON.stringify(geometry); }).toBe('inside reader');
  };
  // Preserve the original reader-body boundary assertion, on all four edges.
  await assertInsideReader();
  await page.getByRole('button', { name: 'Zoom in' }).click();
  await assertInsideReader();
  await page.getByRole('button', { name: '全文翻译', exact: true }).click();
  await expect(page.getByTestId('ai-translation-popover')).toBeHidden();
  await page.getByRole('button', { name: '收起全文翻译', exact: true }).click();
  await assertInsideReader();
  await page.setViewportSize({ width: 1440, height: 900 });
  await assertInsideReader();
  await page.getByRole('button', { name: '全文翻译', exact: true }).click();
  await assertInsideReader();
  await page.getByRole('button', { name: '收起全文翻译', exact: true }).click();
  await assertInsideReader();
  for (const size of [{ width: 1440, height: 900 }, { width: 1024, height: 500 }]) {
    await page.setViewportSize(size);
    await assertInsideReader();
  }
  // A container may move without changing page size (e.g. a splitter).
  const splitter = page.getByRole('separator').first();
  const split = await splitter.boundingBox();
  if (split) {
    await page.mouse.move(split.x + split.width / 2, split.y + split.height / 2);
    await page.mouse.down(); await page.mouse.move(split.x + 60, split.y + split.height / 2, { steps: 8 }); await page.mouse.up();
    await assertInsideReader();
  }
  const pdfPage = page.getByTestId('core__page-layer-0');
  let zoomSteps = 0;
  while ((await pdfPage.boundingBox())!.height < 900 && zoomSteps < 12) {
    const previousHeight = (await pdfPage.boundingBox())!.height;
    await page.getByRole('button', { name: 'Zoom in' }).click();
    await expect.poll(async () => (await pdfPage.boundingBox())!.height).toBeGreaterThan(previousHeight);
    zoomSteps += 1;
  }
  const scroller = page.getByTestId('core__inner-pages');
  await expect.poll(async () => {
    await scroller.evaluate((element) => { element.scrollTop = element.scrollHeight; });
    const text = await page.locator('.rpv-core__text-layer-text').first().boundingBox();
    const visible = await scroller.boundingBox();
    return !!text && !!visible && text.y + text.height < visible.y;
  }).toBe(true);
  await expect(page.getByTestId('ai-translation-popover')).toBeHidden();
  for (let i = 0; i < zoomSteps; i += 1) await page.getByRole('button', { name: 'Zoom out' }).click();
  await scroller.evaluate((element) => { element.scrollTop = 0; element.scrollLeft = 0; });
  await assertInsideReader();
  const reader = page.getByTestId('pdf-reader-container');
  await reader.evaluate((element) => { element.style.width = '130px'; element.style.height = '100px'; });
  await expect(page.getByTestId('ai-translation-popover')).toBeHidden();
  // Hide if even header/close controls cannot fit; retain the result in DOM.
  await reader.evaluate((element) => { element.style.height = '35px'; });
  await expect(page.getByTestId('ai-translation-popover')).toBeHidden();
  await reader.evaluate((element) => { element.style.width = ''; element.style.height = ''; });
  await assertInsideReader();
  await expect(page.getByTestId('ai-translation-popover')).toContainText('这是一段很长的翻译结果');
  // Simulate a smaller visual viewport with nonzero offsets.
  await page.evaluate(() => {
    const viewport = new EventTarget();
    Object.assign(viewport, { offsetLeft: 120, offsetTop: 60, width: 740, height: 360 });
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: viewport });
    window.dispatchEvent(new Event('resize'));
  });
  await assertInsideReader();
  await expect.poll(async () => {
    const box = await page.getByTestId('ai-translation-popover').boundingBox();
    return !!box && box.x >= 119 && box.y >= 59 && box.x + box.width <= 861 && box.y + box.height <= 421;
  }).toBe(true);
  await page.getByTestId('ai-translation-popover').getByRole('button', { name: '关闭', exact: true }).click();
  await expect(page.getByTestId('ai-translation-popover')).toHaveCount(0);
});

test('translation stays within reader at 390x844', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openTranslationPDF(page);


  await expect.poll(async () => {
    const popup = await page.getByTestId('ai-translation-popover').boundingBox();
    const body = await page.locator('.rpv-default-layout__body').boundingBox();
    return !!popup && !!body && popup.x >= body.x - 1 && popup.y >= body.y - 1
      && popup.x + popup.width <= body.x + body.width + 1 && popup.y + popup.height <= body.y + body.height + 1;
  }).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
  await page.getByRole('button', { name: '关闭论文阅读器' }).click();
  await expect(page.getByTestId('ai-translation-popover')).toHaveCount(0);
});

test('translation stays within reader at 1440x900', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await openTranslationPDF(page);


  await expect.poll(async () => {
    const popup = await page.getByTestId('ai-translation-popover').boundingBox();
    const body = await page.locator('.rpv-default-layout__body').boundingBox();
    return !!popup && !!body && popup.x >= body.x - 1 && popup.y >= body.y - 1
      && popup.x + popup.width <= body.x + body.width + 1 && popup.y + popup.height <= body.y + body.height + 1;
  }).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
  await page.getByRole('button', { name: '关闭论文阅读器' }).click();
  await expect(page.getByTestId('ai-translation-popover')).toHaveCount(0);
});

for (const resultMode of ['short', 'error'] as const) {
  test(`translation contains loading and ${resultMode} states within the reading area`, async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 500 });
    const gate = Promise.withResolvers<void>();
    await openTranslationPDF(page, resultMode, gate.promise);
    const popup = page.getByTestId('ai-translation-popover');
    await expect(popup).toContainText('正在翻译论文段落');
    const assertBounds = async () => {
      await expect.poll(async () => {
        const p = await popup.boundingBox();
        const body = await page.locator('.rpv-default-layout__body').boundingBox();
        const toolbar = await page.locator('.rpv-default-layout__toolbar').boundingBox();
        return !!p && !!body && !!toolbar && p.x >= body.x - 1 && p.y >= toolbar.y + toolbar.height - 1
          && p.x + p.width <= body.x + body.width + 1 && p.y + p.height <= body.y + body.height + 1;
      }).toBe(true);
    };
    await assertBounds();
    gate.resolve();
    await expect(popup).not.toContainText('正在翻译论文段落');
    if (resultMode === 'short') await expect(popup).toContainText('短译文');
    await assertBounds();
    await popup.getByRole('button', { name: '关闭', exact: true }).click();
    await expect(popup).toHaveCount(0);
  });
}
