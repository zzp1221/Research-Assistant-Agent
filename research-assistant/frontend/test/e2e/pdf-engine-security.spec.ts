import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const enginePackage = JSON.parse(readFileSync(new URL('../../node_modules/pdfjs-dist/package.json', import.meta.url), 'utf8'));

for (const fixture of [
  { name: 'text-10pages.pdf', pages: 10, status: 'extracted' },
  { name: 'chinese-scan-2pages.pdf', pages: 2, status: 'empty' },
]) {
  test(`patched PDF engine loads ${fixture.name} with a matching local module worker`, async ({ page, context, baseURL }) => {
    const bytes = readFileSync(new URL(`../../../test/fixtures/${fixture.name}`, import.meta.url));
    const workers: string[] = [];
    const errors: string[] = [];
    const externalRequests: string[] = [];
    if (!baseURL) throw new Error('A local deployment URL is required');
    const deploymentOrigin = new URL(baseURL).origin;
    // Exercise the actual built deployment with all nonlocal HTTP disabled.
    // A PDF requiring a CDN worker, font or wasm must fail this assertion.
    await context.route((url) => /^https?:$/.test(url.protocol) && url.origin !== deploymentOrigin, async (route) => {
      externalRequests.push(route.request().url());
      await route.abort('internetdisconnected');
    });
    page.on('worker', (worker) => workers.push(worker.url()));
    page.on('pageerror', (error) => errors.push(error.message));
    await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === '/api/uploads/security-pdf/content') {
        await route.fulfill({ status: 200, contentType: 'application/pdf', body: bytes });
        return;
      }
      let body: unknown = {};
      if (path === '/api/uploads') body = {
        id: 'security-pdf', name: fixture.name, content_type: 'application/pdf', size: bytes.length,
        sha256: 'fixture', content_url: '/api/uploads/security-pdf/content', created_at: '2026-11-16T00:00:00Z',
      };
      if (path === '/api/uploads/security-pdf/document') body = {
        upload_id: 'security-pdf', name: fixture.name,
        document: { status: fixture.status, page_count: fixture.pages, characters: fixture.status === 'empty' ? 0 : 1000 },
        pages: Array.from({ length: fixture.pages }, (_, index) => ({ number: index + 1, chars: 100 })),
      };
      if (path === '/api/plans') body = { plans: [], total: 0 };
      await route.fulfill({ status: path === '/api/uploads' ? 201 : 200, contentType: 'application/json', body: JSON.stringify(body) });
    });
    await page.goto('/');
    await page.locator('input[type=file]').setInputFiles({ name: fixture.name, mimeType: 'application/pdf', buffer: bytes });
    await page.getByRole('button', { name: `打开 ${fixture.name}`, exact: true }).click();
    await expect(page.getByTestId('core__canvas-layer-0')).toBeVisible();
    await expect(page.getByTestId('core__text-layer-0')).toBeAttached();
    expect(workers.some((url) => url.endsWith(`/pdfjs/${enginePackage.version}/pdf.worker.min.mjs`))).toBe(true);
    expect(errors).toEqual([]);
    expect(externalRequests).toEqual([]);
    const manifest = await (await page.request.get('/pdfjs/manifest.json')).json();
    expect(manifest).toEqual({ version: enginePackage.version, isEvalSupported: false, worker: `${enginePackage.version}/pdf.worker.min.mjs` });
    const response = await page.request.get(`/pdfjs/${manifest.worker}`);
    expect(response.ok()).toBe(true);
    expect(response.headers()['content-type']).toContain('javascript');
    if (fixture.status === 'empty') await expect(page.getByRole('button', { name: '全文翻译', exact: true })).toBeDisabled();
  });
}
