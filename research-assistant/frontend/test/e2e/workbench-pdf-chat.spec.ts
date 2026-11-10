import { Buffer } from 'node:buffer';
import { writeFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import { PDFDocument, StandardFonts } from 'pdf-lib';

const documentName = 'workbench-three-pages.pdf';
const question = '请解释这个测量结果，并注明来源页码。';

async function threePagePdf() {
  const document = await PDFDocument.create();
  const font = await document.embedFont(StandardFonts.Helvetica);
  for (const text of ['INTRODUCTION: Three measurements.', 'RESULT: 42.73 units.', 'CONTROL: 19.26 units.']) {
    const pdfPage = document.addPage([360, 300]);
    pdfPage.drawText(text, { x: 24, y: 240, size: 14, font });
  }
  return Buffer.from(await document.save());
}

async function mockReaderApi(page: Page, bytes: Buffer) {
  const requests: { method: string; path: string; body?: Record<string, unknown> }[] = [];
  const upload = { id: 'pdf-chat-upload', name: documentName, content_type: 'application/pdf', size: bytes.length,
    sha256: 'pdf-chat-three-pages', content_url: '/api/uploads/pdf-chat-upload/content', created_at: '2026-11-09T00:00:00Z' };
  await page.addInitScript(() => {
    localStorage.setItem('research-assistant.chat-sessions.v1', JSON.stringify({
      userId: 'pdf-chat-user', activeSessionId: 'pdf-chat-session', sessions: [{
        id: 'pdf-chat-session', title: 'PDF 追问验收', createdAt: '2026-11-09T00:00:00Z', updatedAt: '2026-11-09T00:00:00Z',
        chatHistory: [], activePlanId: null, planGraph: null, intentContext: null,
      }],
    }));
  });
  // Match API pathnames only: Vite imports also contain /services/api/.
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requests.push({ method: request.method(), path,
      ...(request.method() === 'POST' && path !== '/api/uploads' ? { body: request.postDataJSON() } : {}) });
    if (path === '/api/uploads/pdf-chat-upload/content') {
      await route.fulfill({ status: 200, contentType: 'application/pdf', body: bytes });
      return;
    }
    let body: unknown = {};
    if (path === '/api/me') body = { user_id: 'pdf-chat-user', session_id: 'pdf-chat-session' };
    else if (path === '/api/uploads' || path === '/api/uploads/pdf-chat-upload') body = upload;
    else if (path === '/api/uploads/pdf-chat-upload/document') body = { upload_id: upload.id, name: documentName,
      document: { status: 'extracted', page_count: 3, characters: 80, extractor: 'pdftotext' },
      pages: [1, 2, 3].map((number) => ({ number, chars: 25 })) };
    else if (path === '/api/chat') body = { response: '第 2 页的测量结果是 42.73 units，这是处理组的数值。' };
    else if (path === '/api/plans') body = { plans: [], total: 0, next_cursor: '' };
    else if (path === '/api/approvals') body = { items: [] };
    else if (path.endsWith('/alerts')) body = { alerts: [] };
    await route.fulfill({ status: path === '/api/uploads' ? 201 : 200, contentType: 'application/json', body: JSON.stringify(body) });
  });
  return requests;
}

async function readingState(page: Page) {
  return page.evaluate(() => {
    const store = JSON.parse(localStorage.getItem('research-assistant.pdf-reading.v1') || '{}');
    return store['pdf-chat-user|pdf-chat-session|pdf-chat-upload'] as { page: number; scale: number } | undefined;
  });
}

for (const size of [{ width: 1024, height: 500 }, { width: 390, height: 844 }]) {
  test(`P-08 page 2 selected-text chat preserves the PDF reader at ${size.width}x${size.height}`, async ({ page }, testInfo) => {
    await page.setViewportSize(size);
    const bytes = await threePagePdf();
    const requests = await mockReaderApi(page, bytes);
    await page.goto('/');
    const composer = page.getByRole('textbox', { name: '消息输入框', exact: true });
    await page.locator('input[type=file]').setInputFiles({ name: documentName, mimeType: 'application/pdf', buffer: bytes });
    await composer.fill(question);
    await page.getByRole('button', { name: `打开 ${documentName}`, exact: true }).click();
    const reader = page.locator('.research-assistant-reader-workspace');
    await expect(reader.getByText('共 3 页', { exact: true })).toBeVisible();
    await reader.getByRole('button', { name: 'Next page', exact: true }).click();
    const currentPage = reader.getByRole('textbox', { name: 'Enter a page number', exact: true });
    await expect(currentPage).toHaveValue('2');
    await reader.getByRole('button', { name: 'Zoom in', exact: true }).click();
    await expect.poll(async () => (await readingState(page))?.scale).toBeGreaterThan(1);
    await expect(currentPage).toHaveValue('2');
    await expect.poll(async () => (await readingState(page))?.page).toBe(2);
    const initialState = await readingState(page);
    const readerInstance = await page.getByTestId('pdf-reader-container').elementHandle();
    const pageControlInstance = await currentPage.elementHandle();
    const contentsBeforeChat = requests.filter((request) => request.path.endsWith('/content')).length;
    const selected = reader.locator('.rpv-core__text-layer-text').filter({ hasText: 'RESULT:' }).first();
    await expect(selected).toBeVisible();
    await selected.scrollIntoViewIfNeeded();
    await expect.poll(async () => selected.evaluate((element) => {
      const rect = element.getBoundingClientRect();
      return element.contains(document.elementFromPoint(rect.left + 2, rect.top + rect.height / 2));
    })).toBe(true);
    const box = await selected.boundingBox();
    expect(box).not.toBeNull();
    await page.screenshot({ path: testInfo.outputPath(`pdf-before-selection-${size.width}x${size.height}.png`), fullPage: true });
    await page.mouse.move(box!.x + 2, box!.y + box!.height / 2);
    await page.mouse.down();
    await page.mouse.move(box!.x + box!.width - 2, box!.y + box!.height / 2, { steps: 10 });
    await page.mouse.up();
    const ask = page.getByRole('button', { name: '询问 AI', exact: true });
    await expect(ask).toBeVisible();
    await ask.click();
    await expect(composer).toBeVisible();
    await expect(composer).toHaveValue(question);
    const reference = page.getByTestId('composer-document-reference');
    await expect(reference).toContainText(documentName);
    await expect(reference).toContainText('第 2 页');
    await expect(reference).toContainText('42.73 units');
    await page.getByRole('button', { name: '发送消息', exact: true }).click();
    const answer = page.locator('.research-assistant-chat-message.is-assistant').last();
    await expect(answer).toContainText('测量结果是 42.73 units');
    await expect(answer).toBeVisible();
    const chats = requests.filter((request) => request.method === 'POST' && request.path === '/api/chat');
    expect(chats).toHaveLength(1);
    expect(chats[0].body?.message).toBe(question);
    expect(chats[0].body?.document_references).toEqual([{ upload_id: 'pdf-chat-upload', document_name: documentName,
      page: 2, quote: expect.stringContaining('42.73 units') }]);
    expect(requests.filter((request) => request.method === 'POST' && /\/api\/plan(?:s)?(?:\/|$)/.test(request.path))).toHaveLength(0);
    expect(requests.filter((request) => request.path.endsWith('/assist'))).toHaveLength(0);
    await expect(page.locator('.research-assistant-chat-message.is-user').last()).toContainText(`${documentName} · 第 2 页`);
    await page.screenshot({ path: testInfo.outputPath(`pdf-chat-answer-${size.width}x${size.height}.png`), fullPage: true });
    // The sent message's source navigation must return to the selected page.
    await page.locator('.research-assistant-chat-message.is-user').last().getByRole('button', { name: '返回原页', exact: true }).click({ timeout: 10_000 });
    await expect(reader).toBeVisible();
    await expect(currentPage).toHaveValue('2');
    await expect.poll(async () => (await readingState(page))?.scale).toBe(initialState!.scale);
    expect(await page.getByTestId('pdf-reader-container').evaluate((element, original) => element === original, readerInstance)).toBe(true);
    expect(await currentPage.evaluate((element, original) => element === original, pageControlInstance)).toBe(true);
    await reader.getByRole('button', { name: '返回执行图', exact: true }).click();
    await expect(page.locator('.research-assistant-graph-workspace')).toBeVisible();
    await expect(reader).toBeHidden();
    await page.locator('.research-assistant-view-toolbar').getByRole('button', { name: '论文', exact: true }).click();
    await expect(reader).toBeVisible();
    await expect(currentPage).toHaveValue('2');
    await expect.poll(async () => (await readingState(page))?.scale).toBe(initialState!.scale);
    expect(await page.getByTestId('pdf-reader-container').evaluate((element, original) => element === original, readerInstance)).toBe(true);
    expect(await currentPage.evaluate((element, original) => element === original, pageControlInstance)).toBe(true);
    expect(requests.filter((request) => request.path.endsWith('/content'))).toHaveLength(contentsBeforeChat);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`pdf-returned-page2-${size.width}x${size.height}.png`), fullPage: true });
    writeFileSync(testInfo.outputPath('pdf-chat-network.json'), JSON.stringify({ viewport: size, readingBefore: initialState,
      readingAfter: await readingState(page), instancePreserved: true, requests }, null, 2));
    await page.reload();
    if (size.width < 768) await page.locator('.research-assistant-pane-switcher').getByRole('button', { name: '论文', exact: true }).click();
    await expect(reader.getByText('共 3 页', { exact: true })).toBeVisible();
    // Let the viewer's initial intersection/scroll reports finish before
    // checking that they did not overwrite the persisted page with page one.
    await page.waitForTimeout(750);
    await expect(currentPage).toHaveValue('2');
    await expect.poll(async () => (await readingState(page))?.scale).toBe(initialState!.scale);
    await expect(reader.locator('.rpv-core__text-layer-text').filter({ hasText: 'RESULT:' }).first()).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`pdf-refreshed-page2-${size.width}x${size.height}.png`), fullPage: true });
    writeFileSync(testInfo.outputPath('pdf-chat-restoration.json'), JSON.stringify({ viewport: size,
      readingBefore: initialState, readingAfterRefresh: await readingState(page), requests }, null, 2));
  });
}
