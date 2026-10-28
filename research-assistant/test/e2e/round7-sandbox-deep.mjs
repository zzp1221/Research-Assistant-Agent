#!/usr/bin/env node
// Round 7 sandbox command/stream/list/cleanup coverage.
const BASE = (process.env.SANDBOX_URL || 'http://docker-sandbox:8082').replace(/\/$/, '');
const TOKEN = process.env.SANDBOX_API_TOKEN || '';
const IMAGE = process.env.SANDBOX_TEST_IMAGE || 'python:3.11-bullseye';
let failures = 0;
const check = (ok, message, detail) => { console.log((ok ? 'ok   ' : 'FAIL ') + message + (detail === undefined ? '' : ' (' + JSON.stringify(detail) + ')')); if (!ok) failures++; };
async function api(method, path, body) {
  const response = await fetch(BASE + path, { method, headers: { Authorization: 'Bearer ' + TOKEN, 'Content-Type': 'application/json', Accept: 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await response.text();
  let data = text; try { data = JSON.parse(text); } catch {}
  return { status: response.status, data };
}
async function main() {
  const planId = 'round7-sandbox-' + Date.now();
  const created = await api('POST', '/api/v1/sandboxes', { image: IMAGE, task_id: 'round7-sandbox', tenant_id: 'e2e', plan_id: planId, network_mode: 'none', limits: { max_disk_gb: 1, max_runtime_sec: 120, max_download_bytes: 16 * 1024 * 1024 } });
  const id = created.data?.sandbox_id;
  check(created.status === 200 && Boolean(id), 'sandbox created for command tests', created.status);
  const command = await api('POST', '/api/v1/sandboxes/' + id + '/commands', { cmd: ['sh', '-c', "printf 'hello'; printf 'oops' 1>&2"] });
  check(command.status === 200 && String(command.data?.stdout || '').includes('hello') && String(command.data?.stderr || '').includes('oops'), 'command stdout/stderr captured', { status: command.status, stdout: command.data?.stdout, stderr: command.data?.stderr });
  const stream = await fetch(BASE + '/api/v1/sandboxes/' + id + '/commands/stream', { method: 'POST', headers: { Authorization: 'Bearer ' + TOKEN, 'Content-Type': 'application/json', Accept: 'application/x-ndjson' }, body: JSON.stringify({ cmd: ['sh', '-c', "printf 'stream-ok'"] }) });
  const streamText = await stream.text();
  check(stream.status === 200 && String(stream.headers.get('content-type') || '').includes('ndjson') && streamText.includes('stream-ok'), 'command stream returns ndjson output', { status: stream.status, bytes: streamText.length });
  const list = await api('GET', '/api/v1/sandboxes?plan_id=' + encodeURIComponent(planId));
  check(list.status === 200 && (list.data?.sandboxes || []).some((s) => s.plan_id === planId), 'sandbox list contains created sandbox', { status: list.status, count: list.data?.count });
  const deleted = await api('DELETE', '/api/v1/sandboxes/' + id);
  check(deleted.status === 200, 'sandbox deleted after command tests', deleted.status);
  if (failures) { console.log('\n' + failures + ' sandbox checks failed'); process.exit(1); }
  console.log('\nall sandbox command checks passed');
}
main().catch((error) => { console.error('sandbox checks failed: ' + (error?.stack || error)); process.exit(1); });
