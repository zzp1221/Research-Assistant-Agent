#!/usr/bin/env node
// N-04 literature E2E. Runs inside the compose network with real provider access:
//   RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/literature.mjs
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
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

async function request(method, path, { body, headers = {}, user } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (user) finalHeaders['X-User-Id'] = user;
  const jar = jarFor(user || 'default');
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([key, value]) => key + '=' + value).join('; ');
  let payload = body;
  if (payload !== undefined && payload !== null && typeof payload !== 'string') {
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
  const data = contentType.includes('application/json') ? await response.json() : await response.text();
  return { status: response.status, data, headers: response.headers };
}

async function main() {
  const user = 'e2e-lit-' + Math.random().toString(36).slice(2, 10);
  const other = 'e2e-lit-other-' + Math.random().toString(36).slice(2, 10);

  console.log('Real multi-provider search');
  const search = await request('GET', '/api/literature/search?q=graph%20neural%20network&sources=crossref,openalex&limit=8&sort=relevance', { user });
  check(search.status === 200, 'search: HTTP 200 (' + search.status + ')');
  const results = search.data.results || [];
  check(results.length >= 2, 'search: >=2 results from Crossref/OpenAlex (' + results.length + ')');
  check(results.some((item) => item.doi), 'search: at least one DOI result');
  check(Array.isArray(search.data.warnings), 'search: warnings array present');

  const citationSearch = await request('GET', '/api/literature/search?q=graph%20neural%20network&sources=crossref,openalex&limit=8&sort=citations', { user });
  const citations = (citationSearch.data.results || []).map((item) => item.citations || 0);
  check(citations.every((value, index) => index === 0 || citations[index - 1] >= value), 'search: citations sort is non-increasing');

  console.log('Metadata-only import and library');
  const firstDoi = results.find((item) => item.doi)?.doi;
  check(Boolean(firstDoi), 'import: a DOI is available');
  if (firstDoi) {
    const imported = await request('POST', '/api/literature/import', { user, body: { ref: firstDoi, metadata_only: true } });
    check(imported.status === 200 && imported.data.record?.id, 'import: metadata-only record created');
    const recordId = imported.data.record?.id;
    const list = await request('GET', '/api/literature/records?limit=50', { user });
    check(list.status === 200 && (list.data.records || []).some((record) => record.id === recordId), 'library: record appears in owner library');
    const otherList = await request('GET', '/api/literature/records?limit=50', { user: other });
    check(otherList.status === 200 && (otherList.data.records || []).length === 0, 'library: owner isolation');
    const bibtex = await request('GET', '/api/literature/records/' + recordId + '/bibtex', { user });
    check(bibtex.status === 200 && String(bibtex.data).includes('@article{'), 'library: BibTeX export');
    const deleted = await request('DELETE', '/api/literature/records/' + recordId, { user });
    check(deleted.status === 200, 'library: record deleted');
  }

  console.log('Citation graph');
  if (firstDoi) {
    const graph = await request('GET', '/api/literature/citations/' + encodeURIComponent(firstDoi) + '?limit=10', { user });
    check(graph.status === 200 && Array.isArray(graph.data.references) && Array.isArray(graph.data.cited_by), 'citations: graph arrays returned');
  }

  console.log('Web reader');
  const web = await request('POST', '/api/web/fetch', { user, body: { url: 'https://example.com' } });
  check(web.status === 200, 'web: example.com HTTP 200 (' + web.status + ')');
  check(String(web.data.title || '').toLowerCase().includes('example'), 'web: title extracted');
  check(String(web.data.text || '').length > 50, 'web: body text extracted');
  const blocked = await request('POST', '/api/web/fetch', { user, body: { url: 'http://127.0.0.1:8080/api/health' } });
  check(blocked.status === 403 && blocked.data.code === 'ssrf_blocked', 'web: private URL blocked');

  if (failures > 0) {
    console.log('\n' + failures + ' literature check(s) failed');
    process.exit(1);
  }
  console.log('\nall literature checks passed');
}

main().catch((error) => {
  console.error('literature e2e failed: ' + error.message);
  process.exit(1);
});
