import fs from 'node:fs';
import path from 'node:path';

const BASE = process.env.E2E_BASE || 'http://backend:8080';
const TOKEN = process.env.E2E_TOKEN || '';
const USER = process.env.E2E_USER || 'phase1-e2e';
const PDF = process.env.E2E_PDF || '/fixtures/attention-is-all-you-need.pdf';
const results = { steps: [] };

async function request(method, url, { body, headers = {}, raw = false } = {}) {
  const response = await fetch(BASE + url, {
    method,
    headers: {
      ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
      'X-User-Id': USER,
      'X-Session-Id': 'phase1-session',
      ...headers,
    },
    body,
  });
  const text = await response.text();
  if (!response.ok) {
    throw new Error(`${method} ${url} -> ${response.status}: ${text.slice(0, 400)}`);
  }
  return raw ? text : text ? JSON.parse(text) : null;
}

function record(name, detail) {
  results.steps.push({ name, ...detail });
  console.log(`[ok] ${name}`, JSON.stringify(detail).slice(0, 300));
}

async function uploadPdf() {
  const data = fs.readFileSync(PDF);
  const form = new FormData();
  form.append('file', new Blob([data], { type: 'application/pdf' }), path.basename(PDF));
  const uploaded = await request('POST', '/api/uploads', { body: form });
  record('upload', { id: uploaded.id, size: uploaded.size, document: uploaded.document });
  if (!uploaded.document || uploaded.document.status !== 'extracted') {
    throw new Error('PDF text was not extracted: ' + JSON.stringify(uploaded.document));
  }
  return uploaded;
}

async function documentFlow(upload) {
  const info = await request('GET', `/api/uploads/${upload.id}/document`);
  record('document-info', { pages: info.pages.length, status: info.document.status, characters: info.document.characters });
  const page = await request('GET', `/api/uploads/${upload.id}/document/pages/1`);
  const snippet = page.page.text.slice(0, 120).replace(/\s+/g, ' ');
  record('document-page', { page: page.page.number, chars: page.page.chars, snippet });
  if (!/Attention Is All You Need/i.test(page.page.text)) {
    throw new Error('page 1 does not contain the paper title');
  }

  const translate = await request('POST', `/api/uploads/${upload.id}/assist`, {
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode: 'translate', text: 'Attention Is All You Need', page: 1 }),
  });
  record('assist-translate', { model: translate.model, result: translate.result.slice(0, 120), terms: translate.terms?.length ?? 0 });
  if (!translate.result || translate.result.length < 2) throw new Error('empty translation');

  const explain = await request('POST', `/api/uploads/${upload.id}/assist`, {
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode: 'explain', text: 'The Transformer allows for significantly more parallelization.', page: 1 }),
  });
  record('assist-explain', { model: explain.model, result: explain.result.slice(0, 160) });
  if (!explain.result) throw new Error('empty explanation');
}

async function translationFlow(upload) {
  const created = await request('POST', `/api/uploads/${upload.id}/translation-jobs`, {
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ start_page: 1, end_page: 2 }),
  });
  let job = created.job;
  record('translation-start', { id: job.id, total: job.total_pages, status: job.status });
  const deadline = Date.now() + 240000;
  while (Date.now() < deadline && ['queued', 'running'].includes(job.status)) {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    job = (await request('GET', `/api/uploads/${upload.id}/translation-jobs/${job.id}`)).job;
  }
  record('translation-finish', { status: job.status, completed: job.completed_pages, failed: job.failed_pages });
  if (job.status !== 'completed') throw new Error('translation job did not complete: ' + job.status);
  const page = await request('GET', `/api/uploads/${upload.id}/translation-jobs/${job.id}/pages/1`);
  record('translation-page', { page: page.page, chars: page.text.length, snippet: page.text.slice(0, 100) });
  if (!page.text) throw new Error('translation page text is empty');
}

async function main() {
  const upload = await uploadPdf();
  await documentFlow(upload);
  await translationFlow(upload);
  results.uploadId = upload.id;
  fs.writeFileSync('/reports/phase1-pdf-e2e.json', JSON.stringify(results, null, 2));
  console.log('PDF_E2E_DONE');
}

main().catch((error) => {
  console.error('PDF_E2E_FAILED', error.message);
  process.exit(1);
});
