#!/usr/bin/env node
// Round 7 document assist positive path: explain and translate a selected passage.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const FIXTURES = process.env.RA_FIXTURES_DIR || join(process.cwd(), 'test', 'fixtures');
const jars = new Map();
const jarFor = (user) => { if (!jars.has(user)) jars.set(user, new Map()); return jars.get(user); };
let failures = 0;
const check = (ok, message, detail) => { console.log((ok ? 'ok   ' : 'FAIL ') + message + (detail === undefined ? '' : ' (' + JSON.stringify(detail) + ')')); if (!ok) failures++; };
async function request(method, path, { body, form, user = 'round7-assist' } = {}) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  headers['X-User-Id'] = user;
  const jar = jarFor(user);
  if (jar.size) headers.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (form) payload = form;
  else if (payload !== undefined && payload !== null && typeof payload !== 'string') { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload, signal: AbortSignal.timeout(180000) });
  for (const cookie of response.headers.getSetCookie?.() || []) { const pair = cookie.split(';')[0] || ''; const i = pair.indexOf('='); if (i > 0) jar.set(pair.slice(0, i).trim(), pair.slice(i + 1).trim()); }
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json(); else data = await response.text();
  return { status: response.status, data };
}
async function main() {
  const pdf = await readFile(join(FIXTURES, 'text-10pages.pdf'));
  const form = new FormData();
  form.append('file', new Blob([pdf], { type: 'application/pdf' }), 'text-10pages.pdf');
  const upload = await request('POST', '/api/uploads', { form });
  check(upload.status === 201, 'assist PDF uploaded', upload.status);
  const id = upload.data.id;
  const explain = await request('POST', '/api/uploads/' + id + '/assist', { body: { mode: 'explain', text: 'Graph Neural Networks', page: 2, context_pages: 1 } });
  const explainText = String(explain.data?.result || '');
  check(explain.status === 200 && explainText.length > 20, 'assist explain returns substantive text', { status: explain.status, chars: explainText.length });
  check(Array.isArray(explain.data?.bullets) || Array.isArray(explain.data?.terms), 'assist returns structured bullets or terms');
  const translate = await request('POST', '/api/uploads/' + id + '/assist', { body: { mode: 'translate', text: 'Graph Neural Networks are effective for representation learning on graphs.', page: 2, context_pages: 1 } });
  const translateText = String(translate.data?.result || '');
  const hasCjk = /[\u4e00-\u9fff]/.test(translateText);
  check(translate.status === 200 && translateText.length > 10 && hasCjk, 'assist translate returns Chinese text', { status: translate.status, chars: translateText.length });
  await request('DELETE', '/api/uploads/' + id);
  if (failures) { console.log('\n' + failures + ' assist checks failed'); process.exit(1); }
  console.log('\nall document assist checks passed');
}
main().catch((error) => { console.error('assist checks failed: ' + (error?.stack || error)); process.exit(1); });
