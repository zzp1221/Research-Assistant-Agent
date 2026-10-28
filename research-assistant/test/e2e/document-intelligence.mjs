#!/usr/bin/env node
// N-07 document-intelligence E2E. Runs inside the compose network:
//   RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/document-intelligence.mjs
import { readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const FIXTURES = process.env.RA_FIXTURES_DIR || join(process.cwd(), 'test', 'fixtures');
const jars = new Map();
const jarFor = (user) => {
  if (!jars.has(user)) jars.set(user, new Map());
  return jars.get(user);
};
let failures = 0;
const check = (ok, message) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + message);
  if (!ok) failures++;
};

async function request(method, path, { body, form, headers = {}, user } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (user) finalHeaders['X-User-Id'] = user;
  const jar = jarFor(user || 'default');
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([key, value]) => key + '=' + value).join('; ');
  let payload = body;
  if (form) {
    payload = form;
  } else if (payload !== undefined && payload !== null && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) jar.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json();
  else data = await response.text();
  return { status: response.status, data, headers: response.headers };
}

async function uploadPdf(user, fileName) {
  const bytes = await readFile(join(FIXTURES, fileName));
  const form = new FormData();
  form.append('file', new Blob([bytes], { type: 'application/pdf' }), basename(fileName));
  const response = await request('POST', '/api/uploads', { user, form });
  if (response.status !== 201 && response.status !== 200) {
    throw new Error('upload failed ' + response.status + ': ' + JSON.stringify(response.data).slice(0, 300));
  }
  return response.data;
}

function hasHan(text) {
  return /[\u4e00-\u9fff]/.test(String(text || ''));
}

async function main() {
  const user = 'e2e-doc-' + Math.random().toString(36).slice(2, 10);

  console.log('Scanned 20-page PDF with full OCR budget');
  const scanned = await uploadPdf(user, 'scanned-20pages.pdf');
  check(scanned.document?.status === 'extracted', 'scanned: status=extracted (' + scanned.document?.status + ')');
  check(scanned.document?.page_count === 20, 'scanned: page_count=20 (' + scanned.document?.page_count + ')');
  check(scanned.document?.ocr_stats?.ocr_pages === 20, 'scanned: ocr_pages=20 (' + scanned.document?.ocr_stats?.ocr_pages + ')');
  check((scanned.document?.ocr_stats?.average_confidence || 0) > 0.3, 'scanned: average confidence reported');
  const scannedQuality = await request('GET', '/api/uploads/' + scanned.id + '/document/quality', { user });
  check(scannedQuality.status === 200 && (scannedQuality.data.pages || []).length === 20, 'scanned: quality has 20 pages');
  check((scannedQuality.data.pages || []).every((page) => page.source === 'ocr'), 'scanned: every page is OCR-sourced');
  const scannedLayout = await request('GET', '/api/uploads/' + scanned.id + '/document/pages/1/layout', { user });
  check(scannedLayout.status === 200 && (scannedLayout.data.blocks || []).length > 0, 'scanned: layout blocks present');

  console.log('Mixed PDF (2 scanned + 8 text pages)');
  const mixed = await uploadPdf(user, 'mixed-10pages.pdf');
  check(mixed.document?.status === 'extracted', 'mixed: status=extracted (' + mixed.document?.status + ')');
  check(mixed.document?.page_count === 10, 'mixed: page_count=10 (' + mixed.document?.page_count + ')');
  check(mixed.document?.ocr_stats?.ocr_pages === 2, 'mixed: only 2 pages OCR (' + mixed.document?.ocr_stats?.ocr_pages + ')');
  const mixedQuality = await request('GET', '/api/uploads/' + mixed.id + '/document/quality', { user });
  check(mixedQuality.data.pages?.[0]?.source === 'ocr', 'mixed: page 1 is OCR');
  check(mixedQuality.data.pages?.[2]?.source === 'text', 'mixed: page 3 is text');
  const mixedPage1 = await request('GET', '/api/uploads/' + mixed.id + '/document/pages/1', { user });
  check(String(mixedPage1.data?.page?.text || '').length > 0, 'mixed: scanned page was OCR-filled');
  const mixedPage3 = await request('GET', '/api/uploads/' + mixed.id + '/document/pages/3', { user });
  check(String(mixedPage3.data?.page?.text || '').includes('retrieval augmented'), 'mixed: text page preserved');

  console.log('Text PDF layout, tables and figures');
  const text = await uploadPdf(user, 'text-10pages.pdf');
  check(text.document?.status === 'extracted', 'text: status=extracted');
  const page2 = await request('GET', '/api/uploads/' + text.id + '/document/pages/2', { user });
  const page2Text = String(page2.data?.page?.text || '');
  const p1 = page2Text.indexOf('Paragraph 1:');
  const p4 = page2Text.indexOf('Paragraph 4:');
  const p5 = page2Text.indexOf('Paragraph 5:');
  check(p1 >= 0 && p4 > p1 && p5 > p4, 'text: two-column reading order left-then-right');
  const layout = await request('GET', '/api/uploads/' + text.id + '/document/pages/2/layout', { user });
  check(layout.status === 200 && (layout.data.blocks || []).length >= 4, 'text: layout blocks present');
  const docDetail = await request('GET', '/api/uploads/' + text.id + '/document', { user });
  const tables = docDetail.data.tables || [];
  check(tables.length >= 1, 'text: table summary present (' + tables.length + ')');
  if (tables.length > 0) {
    const tableId = tables[0].id;
    const tableJson = await request('GET', '/api/uploads/' + text.id + '/document/tables/' + tableId, { user });
    check(tableJson.status === 200 && (tableJson.data.table?.rows || []).length >= 4, 'text: table rows >= 4');
    const tableCsv = await request('GET', '/api/uploads/' + text.id + '/document/tables/' + tableId + '?format=csv', { user });
    check(tableCsv.status === 200 && String(tableCsv.data).includes('Model') && String(tableCsv.data).includes('GNN'), 'text: CSV download contains data');
  }
  const figures = docDetail.data.figures || [];
  check(figures.length >= 1, 'text: figure summary present (' + figures.length + ')');
  if (figures.length > 0) {
    const figureImage = await request('GET', '/api/uploads/' + text.id + '/document/figures/' + figures[0].id, { user });
    check(figureImage.status === 200 && String(figureImage.headers.get('content-type') || '').includes('image/'), 'text: figure image served');
  }

  console.log('Chinese scanned PDF');
  const chinese = await uploadPdf(user, 'chinese-scan-2pages.pdf');
  check((chinese.document?.characters || 0) > 0, 'chinese: characters extracted');
  const chinesePage = await request('GET', '/api/uploads/' + chinese.id + '/document/pages/1', { user });
  check(hasHan(chinesePage.data?.page?.text), 'chinese: Han characters recovered by chi_sim');

  if (failures > 0) {
    console.log('\n' + failures + ' document-intelligence check(s) failed');
    process.exit(1);
  }
  console.log('\nall document-intelligence checks passed');
}

main().catch((error) => {
  console.error('document-intelligence e2e failed: ' + error.message);
  process.exit(1);
});
