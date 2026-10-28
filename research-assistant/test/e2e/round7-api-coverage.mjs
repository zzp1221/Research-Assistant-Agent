#!/usr/bin/env node
// Round 7 deep API coverage: exercises endpoints not covered by the existing E2E suites.
// Usage: RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/round7-api-coverage.mjs
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const FIXTURES = process.env.RA_FIXTURES_DIR || join(process.cwd(), 'test', 'fixtures');
const jars = new Map();
const jarFor = (user) => {
  if (!jars.has(user)) jars.set(user, new Map());
  return jars.get(user);
};
let failures = 0;
const check = (ok, message, detail) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + message + (detail === undefined ? '' : ' (' + JSON.stringify(detail) + ')'));
  if (!ok) failures++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function request(method, path, { body, form, headers = {}, user = 'round7-default', auth = true, raw = false } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (auth && TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (user) finalHeaders['X-User-Id'] = user;
  const jar = jarFor(user);
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (form) payload = form;
  else if (payload !== undefined && payload !== null && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload, redirect: 'manual' });
  for (const cookie of response.headers.getSetCookie?.() || []) {
    const pair = cookie.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) jar.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim());
  }
  if (raw) return response;
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json();
  else if (contentType.startsWith('text/') || contentType.includes('application/x-ndjson') || contentType.includes('application/problem+json')) data = await response.text();
  else data = Buffer.from(await response.arrayBuffer());
  return { status: response.status, data, headers: response.headers };
}

async function uploadFile(user, name, buffer, type = 'application/pdf') {
  const form = new FormData();
  form.append('file', new Blob([buffer], { type }), name);
  return request('POST', '/api/uploads', { form, user });
}

async function main() {
  // ---- basics ----
  const health = await request('GET', '/api/health', { user: 'round7-basic' });
  check(health.status === 200 && health.data.ok === true, 'health ok');
  const hello = await request('GET', '/api/hello', { user: 'round7-basic' });
  check(hello.status === 200 && String(hello.data).includes('hello api group'), 'hello endpoint returns text');
  const me = await request('GET', '/api/me', { user: 'round7-basic' });
  check(me.status === 200 && Boolean(me.data.user_id) && Boolean(me.data.session_id), 'me returns user/session');
  const noAuth = await request('GET', '/api/plans', { user: 'round7-basic', auth: false });
  check(noAuth.status === 401, 'unauthenticated plans request is 401', noAuth.status);
  const badAuth = await request('GET', '/api/plans', { user: 'round7-basic', auth: false, headers: { Authorization: 'Bearer wrong-token' } });
  check(badAuth.status === 401, 'wrong bearer token is 401', badAuth.status);
  const executeLegacy = await request('POST', '/api/execute', { body: {}, user: 'round7-basic' });
  check(executeLegacy.status === 410, 'legacy /api/execute is tombstoned with 410', executeLegacy.status);

  // ---- PDF proxy ----
  const proxyMissing = await request('GET', '/api/pdf-proxy', { user: 'round7-basic' });
  check(proxyMissing.status === 400, 'pdf-proxy missing url is 400', proxyMissing.status);
  const proxyPrivate = await request('GET', '/api/pdf-proxy?url=' + encodeURIComponent('http://127.0.0.1:8080/api/health'), { user: 'round7-basic' });
  check(proxyPrivate.status === 400, 'pdf-proxy private url blocked', proxyPrivate.status);
  const proxyPublic = await request('GET', '/api/pdf-proxy?url=' + encodeURIComponent('https://www.orimi.com/pdf-test.pdf'), { user: 'round7-basic', raw: true });
  const proxyBuffer = Buffer.from(await proxyPublic.arrayBuffer());
  check(proxyPublic.status === 200 && String(proxyPublic.headers.get('content-type') || '').includes('application/pdf') && proxyBuffer.subarray(0, 4).toString() === '%PDF', 'pdf-proxy fetches public PDF', proxyPublic.status);

  // ---- upload lifecycle ----
  const uploadText = '# round7 upload\nline two\n';
  const uploaded = await request('POST', '/api/uploads', { form: (() => { const f = new FormData(); f.append('file', new Blob([uploadText], { type: 'text/markdown' }), 'round7.md'); return f; })(), user: 'round7-basic' });
  check(uploaded.status === 201 && Boolean(uploaded.data.id), 'upload text file returns id', uploaded.status);
  const uploadId = uploaded.data.id;
  const uploadMeta = await request('GET', '/api/uploads/' + uploadId, { user: 'round7-basic' });
  check(uploadMeta.status === 200 && uploadMeta.data.name === 'round7.md' && uploadMeta.data.size === Buffer.byteLength(uploadText), 'upload metadata matches');
  const uploadContent = await request('GET', '/api/uploads/' + uploadId + '/content', { user: 'round7-basic' });
  check(uploadContent.status === 200 && String(uploadContent.data) === uploadText, 'upload content round-trips');
  const uploadDeleted = await request('DELETE', '/api/uploads/' + uploadId, { user: 'round7-basic' });
  check(uploadDeleted.status === 200, 'upload delete succeeds', uploadDeleted.status);
  const uploadGone = await request('GET', '/api/uploads/' + uploadId, { user: 'round7-basic' });
  check(uploadGone.status === 404, 'upload is gone after delete', uploadGone.status);

  // ---- plan lifecycle ----
  const created = await request('POST', '/api/plan', { body: { intent: '你好' }, user: 'round7-life' });
  const plan = created.data?.plan_graph;
  check(created.status === 200 && Boolean(plan?.id), 'plan created', created.status);
  check(plan?.intent_type === 'General' && plan?.nodes?.length === 1, 'direct plan has one General node', plan?.nodes?.length);
  check(plan?.meta?.general_plan_shape === 'direct' && plan?.meta?.complexity === 'L0', 'direct plan meta is L0/direct', plan?.meta?.complexity);
  const planId = plan?.id;
  const events = await request('GET', '/api/plans/' + planId + '/events', { user: 'round7-life' });
  const eventTypes = (events.data?.events || []).map((e) => e.event_type);
  check(events.status === 200 && eventTypes.includes('plan_created'), 'plan events include plan_created', eventTypes.slice(0, 4));
  const exportJson = await request('GET', '/api/plans/' + planId + '/export?format=json', { user: 'round7-life' });
  const exportJsonData = typeof exportJson.data === 'string' ? JSON.parse(exportJson.data) : exportJson.data;
  check(exportJson.status === 200 && exportJsonData.id === planId, 'json export matches plan id');
  const exportMd = await request('GET', '/api/plans/' + planId + '/export?format=md', { user: 'round7-life' });
  check(exportMd.status === 200 && String(exportMd.data).includes('## Nodes'), 'markdown export has nodes section');
  const exportZip = await request('GET', '/api/plans/' + planId + '/export?format=zip', { user: 'round7-life', raw: true });
  const zipBuffer = Buffer.from(await exportZip.arrayBuffer());
  check(exportZip.status === 200 && zipBuffer.subarray(0, 2).toString() === 'PK', 'zip export is a zip archive', exportZip.status);
  const exportBad = await request('GET', '/api/plans/' + planId + '/export?format=xml', { user: 'round7-life' });
  check(exportBad.status === 400, 'invalid export format is 400', exportBad.status);
  const ticket = await request('GET', '/api/plans/' + planId + '/stream-ticket', { user: 'round7-life' });
  check(ticket.status === 200 && Boolean(ticket.data.ticket) && ticket.data.expires_in === 60, 'stream ticket issued');
  // EventSource-style ticket auth without Authorization header.
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 8000);
  let sseText = '';
  let sseContentType = '';
  const streamJar = jarFor('round7-life');
  const cookieHeader = [...streamJar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  try {
    const sse = await fetch(BASE + '/api/plans/' + planId + '/stream?ticket=' + encodeURIComponent(ticket.data.ticket), { headers: { Accept: 'text/event-stream', ...(cookieHeader ? { Cookie: cookieHeader } : {}) }, signal: controller.signal });
    sseContentType = sse.headers.get('content-type') || '';
    const reader = sse.body.getReader();
    const decoder = new TextDecoder();
    while (sseText.length < 20000) {
      const { value, done } = await reader.read();
      if (done) break;
      sseText += decoder.decode(value, { stream: true });
      if (sseText.includes('plan_created')) break;
    }
    await reader.cancel().catch(() => {});
  } catch (error) {
    if (error.name !== 'AbortError') throw error;
  } finally {
    clearTimeout(timer);
  }
  check(sseContentType.includes('text/event-stream') && sseText.includes('plan_created'), 'stream ticket replays plan_created event', { contentType: sseContentType, bytes: sseText.length });
  const sessions = await request('GET', '/api/sessions', { user: 'round7-life' });
  const sessionRow = (sessions.data?.sessions || []).find((s) => s.session_id === plan.session_id);
  check(sessions.status === 200 && Boolean(sessionRow) && sessionRow.plan_count >= 1, 'sessions list includes plan session');
  const nodeId = plan.nodes[0].id;
  const retryPending = await request('POST', '/api/plans/' + planId + '/tasks/' + nodeId + '/retry', { user: 'round7-life' });
  check(retryPending.status === 409, 'retry of pending task is 409', retryPending.status);
  const reassign = await request('POST', '/api/plans/' + planId + '/tasks/' + nodeId + '/reassign', { body: { assigned_to: 'general_agent' }, user: 'round7-life' });
  check(reassign.status === 200 && reassign.data.assigned_to === 'general_agent', 'pending task reassignment succeeds', reassign.status);
  const retryMissing = await request('POST', '/api/plans/' + planId + '/tasks/not-a-task/retry', { user: 'round7-life' });
  check([404, 409].includes(retryMissing.status), 'retry unknown task is rejected', retryMissing.status);
  const actions = await request('GET', '/api/plans/' + planId + '/runtime-actions', { user: 'round7-life' });
  check(actions.status === 200 && (Array.isArray(actions.data.actions) || actions.data.actions === null), 'runtime actions list is readable');
  const badDecision = await request('POST', '/api/plans/' + planId + '/runtime-actions/not-an-action/decision', { body: { decision: 'approve' }, user: 'round7-life' });
  check([404, 400, 409].includes(badDecision.status), 'unknown runtime action decision is rejected', badDecision.status);
  const deletePlan = await request('DELETE', '/api/plans/' + planId, { user: 'round7-life' });
  check(deletePlan.status === 200 && deletePlan.data.deleted === true, 'plan delete succeeds', deletePlan.status);
  const planGone = await request('GET', '/api/plans/' + planId, { user: 'round7-life' });
  check(planGone.status === 404, 'plan is gone after delete', planGone.status);
  const deleteAgain = await request('DELETE', '/api/plans/' + planId, { user: 'round7-life' });
  check(deleteAgain.status === 404, 'second plan delete is 404', deleteAgain.status);

  // ---- session delete ----
  const sessionPlan = await request('POST', '/api/plan', { body: { intent: '用一句话解释什么是二分查找' }, user: 'round7-session' });
  const sp = sessionPlan.data?.plan_graph;
  const sessionId = sp?.session_id;
  const sessionDelete = await request('DELETE', '/api/sessions/' + encodeURIComponent(sessionId), { user: 'round7-session' });
  check(sessionDelete.status === 200 && sessionDelete.data.deleted >= 1, 'session delete removes plans', sessionDelete.status);
  const sessionPlanGone = await request('GET', '/api/plans/' + sp.id, { user: 'round7-session' });
  check(sessionPlanGone.status === 404, 'plan removed with session', sessionPlanGone.status);

  // ---- audit / metrics / routing / CORS ----
  const audit = await request('GET', '/api/audit/export?limit=100', { user: 'round7-basic' });
  check(audit.status === 200 && Array.isArray(audit.data.events) && audit.data.events.length > 0, 'audit export returns events', audit.data?.count);
  const auditJsonl = await request('GET', '/api/audit/export?format=jsonl&limit=10', { user: 'round7-basic', raw: true });
  const auditLines = (await auditJsonl.text()).trim().split('\n').filter(Boolean);
  check(auditJsonl.status === 200 && auditLines.length > 0 && auditLines.every((line) => { try { JSON.parse(line); return true; } catch { return false; } }), 'audit jsonl lines parse');
  const metrics = await request('GET', '/api/metrics', { user: 'round7-basic' });
  check(metrics.status === 200 && String(metrics.data).includes('plan_route_total') && String(metrics.data).includes('plan_general_shape_total') && String(metrics.data).includes('http_requests_total'), 'metrics expose route/shape/http counters');
  const routing = await request('GET', '/api/model-routing', { user: 'round7-basic' });
  check(routing.status === 200 && routing.data.routing !== undefined, 'model routing is readable');
  const routingBad = await request('PUT', '/api/model-routing', { body: { main_profile_id: 123 }, user: 'round7-basic' });
  check(routingBad.status === 400, 'invalid model routing payload is 400', routingBad.status);
  const cors = await fetch(BASE + '/api/plan', { method: 'OPTIONS', headers: { Origin: 'http://localhost:5173', 'Access-Control-Request-Method': 'POST', Authorization: TOKEN ? 'Bearer ' + TOKEN : '' } });
  check(cors.status === 204 && String(cors.headers.get('access-control-allow-origin') || '').includes('localhost:5173'), 'CORS preflight allows configured origin', { status: cors.status, acao: cors.headers.get('access-control-allow-origin') });
  const artifactMissing = await request('GET', '/api/artifacts/not-a-real-ref', { user: 'round7-basic' });
  check(artifactMissing.status === 404, 'missing artifact is 404', artifactMissing.status);
  const artifactTraversal = await request('GET', '/api/artifacts/..%2F..%2Fetc%2Fpasswd', { user: 'round7-basic' });
  check([400, 404].includes(artifactTraversal.status), 'artifact path traversal is rejected', artifactTraversal.status);

  // ---- documents ----
  const textPdf = await readFile(join(FIXTURES, 'text-10pages.pdf'));
  const docUpload = await uploadFile('round7-doc', 'text-10pages.pdf', textPdf);
  check(docUpload.status === 201 && docUpload.data.document?.page_count === 10, 'text PDF upload parsed 10 pages', docUpload.data?.document?.page_count);
  const docId = docUpload.data.id;
  const search = await request('GET', '/api/uploads/' + docId + '/document/search?q=GNN&top_k=5', { user: 'round7-doc' });
  check(search.status === 200 && Array.isArray(search.data.results) && search.data.results.length > 0, 'document search returns results', search.data?.results?.length);
  const assistBadMode = await request('POST', '/api/uploads/' + docId + '/assist', { body: { mode: 'invalid-mode', text: 'hello' }, user: 'round7-doc' });
  check(assistBadMode.status === 400, 'document assist rejects invalid mode', assistBadMode.status);
  const assistNoText = await request('POST', '/api/uploads/' + docId + '/assist', { body: { mode: 'explain' }, user: 'round7-doc' });
  check(assistNoText.status === 400, 'document assist requires text', assistNoText.status);
  const scannedPdf = await readFile(join(FIXTURES, 'scanned-20pages.pdf'));
  const scannedUpload = await uploadFile('round7-doc', 'scanned-20pages.pdf', scannedPdf);
  const scannedId = scannedUpload.data.id;
  const translationScanned = await request('POST', '/api/uploads/' + scannedId + '/translation-jobs', { body: { start_page: 1, end_page: 1, target_lang: 'zh' }, user: 'round7-doc' });
  check(translationScanned.status === 202 && Boolean(translationScanned.data.job?.id), 'translation on OCR-scanned PDF is accepted', translationScanned.status);
  const translation = await request('POST', '/api/uploads/' + docId + '/translation-jobs', { body: { start_page: 1, end_page: 1, target_lang: 'zh' }, user: 'round7-doc' });
  check(translation.status === 202 && Boolean(translation.data.job?.id), 'translation job accepted', translation.status);
  const jobId = translation.data?.job?.id;
  let job = null;
  const deadline = Date.now() + 180000;
  while (Date.now() < deadline) {
    const current = await request('GET', '/api/uploads/' + docId + '/translation-jobs/' + jobId, { user: 'round7-doc' });
    job = current.data?.job;
    if (job && ['completed', 'failed', 'canceled'].includes(job.status)) break;
    await sleep(3000);
  }
  check(Boolean(job) && job.status === 'completed' && job.completed_pages >= 1, 'translation job completed one page', job?.status);
  const pageTranslation = await request('GET', '/api/uploads/' + docId + '/translation-jobs/' + jobId + '/pages/1', { user: 'round7-doc' });
  check(pageTranslation.status === 200 && Boolean(pageTranslation.data.translation || pageTranslation.data.text), 'translation page returns translated text', pageTranslation.status);
  const jobList = await request('GET', '/api/uploads/' + docId + '/translation-jobs', { user: 'round7-doc' });
  check(jobList.status === 200 && (jobList.data.jobs || []).some((j) => j.id === jobId), 'translation job list contains job');
  await request('DELETE', '/api/uploads/' + docId, { user: 'round7-doc' });
  await request('DELETE', '/api/uploads/' + scannedId, { user: 'round7-doc' });

  // ---- literature attach/detach ----
  const imported = await request('POST', '/api/literature/import', { body: { ref: '10.48550/arxiv.1810.00826', attach: false }, user: 'round7-lit' });
  const record = imported.data?.record;
  check(imported.status === 200 && Boolean(record?.id), 'literature DOI import creates record', imported.status);
  if (record?.id) {
    const attach = await request('POST', '/api/literature/records/' + record.id + '/attach', { body: { session_id: 'round7-lit-session' }, user: 'round7-lit' });
    check(attach.status === 200 && attach.data.attached_session === 'round7-lit-session', 'literature record attaches to session', attach.status);
    const recordAfter = await request('GET', '/api/literature/records/' + record.id, { user: 'round7-lit' });
    check(recordAfter.status === 200 && (recordAfter.data.record?.attached_sessions || []).includes('round7-lit-session'), 'record lists attached session');
    const detach = await request('DELETE', '/api/literature/records/' + record.id + '/attach', { body: { session_id: 'round7-lit-session' }, user: 'round7-lit' });
    check(detach.status === 200 && detach.data.detached_session === 'round7-lit-session', 'literature record detaches', detach.status);
    const bibtex = await request('GET', '/api/literature/records/' + record.id + '/bibtex', { user: 'round7-lit' });
    check(bibtex.status === 200 && String(bibtex.data).includes('@'), 'bibtex export contains entry');
    const deletedRecord = await request('DELETE', '/api/literature/records/' + record.id, { user: 'round7-lit' });
    check(deletedRecord.status === 200, 'literature record delete succeeds', deletedRecord.status);
    const deletedAgain = await request('DELETE', '/api/literature/records/' + record.id, { user: 'round7-lit' });
    check(deletedAgain.status === 404, 'second literature delete is 404', deletedAgain.status);
  }

  if (failures > 0) {
    console.log('\n' + failures + ' deep API check(s) failed');
    process.exit(1);
  }
  console.log('\nall deep API checks passed');
}

main().catch((error) => {
  console.error('deep api failed: ' + (error?.stack || error));
  process.exit(1);
});
