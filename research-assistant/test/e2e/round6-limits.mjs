#!/usr/bin/env node
// Round-6 limits: plan rate limiting, upload size and filename sanitisation.
import { readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const FIXTURES = process.env.RA_FIXTURES_DIR || join(process.cwd(), 'test', 'fixtures');
const jars = new Map();
const jarFor = (u) => { if (!jars.has(u)) jars.set(u, new Map()); return jars.get(u); };
let failures = 0;
const check = (ok, msg) => { console.log((ok ? 'ok   ' : 'FAIL ') + msg); if (!ok) failures++; };
async function request(method, path, { body, form, user } = {}) {
  const jar = jarFor(user);
  const headers = { Accept: 'application/json', 'X-User-Id': user };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (jar.size) headers.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (form) payload = form;
  else if (payload !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || ''; const i = pair.indexOf('=');
    if (i > 0) jar.set(pair.slice(0, i), pair.slice(i + 1));
  }
  const ct = response.headers.get('content-type') || '';
  const data = ct.includes('application/json') ? await response.json() : await response.text();
  return { status: response.status, data };
}
async function main() {
  const user = 'e2e-limits-' + Math.random().toString(36).slice(2, 10);
  console.log('Plan rate limit (burst=3, rate=10/min)');
  // Warm the signed identity cookie first: six cold requests would each mint a
  // fresh anonymous principal and land in six different buckets.
  await request('GET', '/api/me', { user });
  const results = await Promise.all(Array.from({ length: 6 }, () => request('POST', '/api/plan', { user, body: { intent: '你好' } })));
  const codes = results.map((r) => r.status);
  console.log('     statuses: ' + codes.join(','));
  check(codes.filter((c) => c === 200).length <= 4, 'rate limit: at most 4 of 6 concurrent plans accepted');
  check(codes.includes(429), 'rate limit: at least one 429 returned');
  const retryAfter = results.find((r) => r.status === 429)?.data;
  check(Boolean(retryAfter), 'rate limit: 429 body present');

  console.log('Upload size limit');
  const big = Buffer.alloc(33 * 1024 * 1024, 0x41);
  const bigForm = new FormData();
  bigForm.append('file', new Blob([big], { type: 'application/pdf' }), 'big.pdf');
  const bigRes = await request('POST', '/api/uploads', { user, form: bigForm });
  check(bigRes.status === 413 || bigRes.status === 400, 'upload: 33MB rejected (' + bigRes.status + ')');

  console.log('Upload filename sanitisation');
  const bytes = await readFile(join(FIXTURES, 'text-10pages.pdf'));
  const form = new FormData();
  form.append('file', new Blob([bytes], { type: 'application/pdf' }), '../../evil.pdf');
  const res = await request('POST', '/api/uploads', { user, form });
  check(res.status === 200 || res.status === 201, 'upload: traversal filename accepted (' + res.status + ')');
  const name = String(res.data?.name || res.data?.document?.name || '');
  check(!name.includes('..') && !name.includes('/') && !name.includes('\\'), 'upload: stored name is sanitised (' + JSON.stringify(name) + ')');
  if (res.data?.id) await request('DELETE', '/api/uploads/' + res.data.id, { user });

  if (failures) { console.log('\n' + failures + ' failure(s)'); process.exit(1); }
  console.log('\nround6 limits checks passed');
}
main().catch((e) => { console.error('limits failed: ' + e.message); process.exit(1); });
