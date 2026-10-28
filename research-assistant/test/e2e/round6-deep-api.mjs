#!/usr/bin/env node
// Round-6 deep API checks: document quote location, literature import dedup, web SSRF.
import { readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const FIXTURES = process.env.RA_FIXTURES_DIR || join(process.cwd(), 'test', 'fixtures');
const USER = 'e2e-deep-' + Math.random().toString(36).slice(2, 10);
const jars = new Map();
const jarFor = (user) => { if (!jars.has(user)) jars.set(user, new Map()); return jars.get(user); };
let failures = 0;
const check = (ok, msg) => { console.log((ok ? 'ok   ' : 'FAIL ') + msg); if (!ok) failures++; };
async function request(method, path, { body, form, headers = {}, user = USER } = {}) {
  const jar = jarFor(user);
  const finalHeaders = { Accept: 'application/json', 'X-User-Id': user, ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (form) payload = form;
  else if (payload !== undefined && payload !== null) { finalHeaders['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || ''; const i = pair.indexOf('=');
    if (i > 0) jar.set(pair.slice(0, i), pair.slice(i + 1));
  }
  const ct = response.headers.get('content-type') || '';
  const data = ct.includes('application/json') ? await response.json() : await response.text();
  return { status: response.status, data, headers: response.headers };
}
async function upload(name) {
  const bytes = await readFile(join(FIXTURES, name));
  const form = new FormData();
  form.append('file', new Blob([bytes], { type: 'application/pdf' }), basename(name));
  const r = await request('POST', '/api/uploads', { form });
  if (r.status !== 200 && r.status !== 201) throw new Error('upload ' + name + ' failed: ' + r.status + ' ' + JSON.stringify(r.data).slice(0, 300));
  return r.data;
}
async function main() {
  console.log('Document quote location');
  const text = await upload('text-10pages.pdf');
  const page2 = await request('GET', '/api/uploads/' + text.id + '/document/pages/2');
  const body = String(page2.data?.page?.text || '');
  const quote = body.includes('Paragraph 1:') ? 'Paragraph 1:' : body.slice(0, 40);
  const loc = await request('GET', '/api/uploads/' + text.id + '/document/locate?quote=' + encodeURIComponent(quote));
  check(loc.status === 200 && loc.data.location?.found === true, 'locate: existing quote found (' + loc.status + ')');
  check(loc.data.location?.page === 2, 'locate: page number is 2 (' + loc.data.location?.page + ')');
  const missing = await request('GET', '/api/uploads/' + text.id + '/document/locate?quote=' + encodeURIComponent('this quote definitely does not exist 1234567890'));
  check(missing.status === 404 && missing.data.code === 'quote_not_found', 'locate: missing quote returns 404 quote_not_found (' + missing.status + ')');
  const empty = await request('GET', '/api/uploads/' + text.id + '/document/locate?quote=');
  check(empty.status === 400, 'locate: empty quote rejected (' + empty.status + ')');
  const long = await request('GET', '/api/uploads/' + text.id + '/document/locate?quote=' + encodeURIComponent('x'.repeat(2001)));
  check(long.status === 400, 'locate: >2000 rune quote rejected (' + long.status + ')');
  const badPage = await request('GET', '/api/uploads/' + text.id + '/document/pages/999');
  check(badPage.status === 404 || badPage.status === 400, 'document: out-of-range page rejected (' + badPage.status + ')');
  const otherUser = 'e2e-other-' + Math.random().toString(36).slice(2, 8);
  await request('GET', '/api/me', { user: otherUser });
  const other = await request('GET', '/api/uploads/' + text.id + '/document/locate?quote=' + encodeURIComponent(quote), { user: otherUser });
  check(other.status === 404 || other.status === 403, 'locate: other user cannot read upload (' + other.status + ')');

  console.log('Literature import dedup');
  const search = await request('GET', '/api/literature/search?q=graph%20neural%20network&sources=crossref&limit=5');
  const doi = (search.data.results || []).find((r) => r.doi)?.doi;
  check(Boolean(doi), 'dedup: search returned a DOI');
  if (doi) {
    const first = await request('POST', '/api/literature/import', { body: { ref: doi, metadata_only: true } });
    const second = await request('POST', '/api/literature/import', { body: { ref: doi, metadata_only: true } });
    check(first.status === 200 && second.status === 200, 'dedup: both imports succeed');
    const id1 = first.data.record?.id || first.data.record_id;
    const id2 = second.data.record?.id || second.data.record_id;
    check(Boolean(id1) && id1 === id2, 'dedup: same DOI reuses the same record id (' + id1 + ' vs ' + id2 + ')');
    const list = await request('GET', '/api/literature/records?limit=100');
    const matches = (list.data.records || []).filter((r) => r.doi === doi || r.id === id1);
    check(matches.length === 1, 'dedup: library has exactly one record for the DOI (' + matches.length + ')');
    if (id1) await request('DELETE', '/api/literature/records/' + id1);
  }
  const invalid = await request('POST', '/api/literature/import', { body: { ref: 'not-a-real-doi-xyz' } });
  check(invalid.status >= 400 && invalid.status < 500, 'import: invalid ref rejected with 4xx (' + invalid.status + ')');

  console.log('Web fetch SSRF variants');
  const urls = [
    ['http://127.0.0.1:8080/api/health', 403],
    ['http://localhost:8080/api/health', 403],
    ['http://10.0.0.1/', 403],
    ['http://169.254.169.254/latest/meta-data/', 403],
    ['file:///etc/passwd', 400],
    ['gopher://127.0.0.1:6379/', 400],
    ['http://[::1]:8080/api/health', 403],
  ];
  for (const [url, expected] of urls) {
    const r = await request('POST', '/api/web/fetch', { body: { url } });
    check(r.status === expected, 'web: ' + url + ' blocked with ' + expected + ' (got ' + r.status + ')');
  }
  const publicFetch = await request('POST', '/api/web/fetch', { body: { url: 'https://example.com' } });
  check(publicFetch.status === 200 && String(publicFetch.data.title || '').length > 0, 'web: public URL still works');

  if (failures) { console.log('\n' + failures + ' failure(s)'); process.exit(1); }
  console.log('\nround6 deep API checks passed');
}
main().catch((e) => { console.error('deep api failed: ' + e.message); process.exit(1); });
