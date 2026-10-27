#!/usr/bin/env node
// Verifies the default-off sandbox network policy against a running stack.
// Run inside the compose network: node test/e2e/sandbox-network.mjs
const BASE = (process.env.SANDBOX_URL || 'http://docker-sandbox:8082').replace(/\/$/, '');
const TOKEN = process.env.SANDBOX_API_TOKEN || '';
const IMAGE = process.env.SANDBOX_TEST_IMAGE || 'python:3.11-bullseye';

let failures = 0;
const check = (ok, message) => {
  console.log((ok ? 'ok   ' : 'FAIL ') + message);
  if (!ok) failures++;
};

async function api(method, path, body) {
  const response = await fetch(BASE + path, {
    method,
    headers: {
      Authorization: 'Bearer ' + TOKEN,
      'Content-Type': 'application/json',
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  return { status: response.status, data };
}

const PYTHON = `import socket
try:
    print("RESOLVED", socket.gethostbyname("pypi.org"))
except Exception as error:
    print("BLOCKED", type(error).__name__)
`;

async function resolveHost(sandboxId) {
  const result = await api('POST', '/api/v1/sandboxes/' + sandboxId + '/python', { code: PYTHON });
  if (result.status !== 200) return { ok: false, detail: JSON.stringify(result.data) };
  const output = String(result.data.stdout || '') + String(result.data.stderr || '');
  return { ok: output.includes('RESOLVED'), detail: output.trim() };
}

async function main() {
  const created = await api('POST', '/api/v1/sandboxes', {
    image: IMAGE,
    task_id: 'network-e2e',
    tenant_id: 'e2e',
    plan_id: 'network-e2e-plan',
    network_mode: 'none',
    limits: { max_disk_gb: 2, max_runtime_sec: 120, max_download_bytes: 64 * 1024 * 1024 },
  });
  if (created.status !== 200) {
    console.error('cannot create sandbox: ' + JSON.stringify(created.data));
    process.exit(1);
  }
  const sandboxId = created.data.sandbox_id;
  check(Boolean(sandboxId), 'sandbox created');

  const offline = await resolveHost(sandboxId);
  check(!offline.ok, 'default sandbox cannot resolve public DNS (' + (offline.detail || 'no output') + ')');

  const opened = await api('POST', '/api/v1/sandboxes/' + sandboxId + '/network', { mode: 'bridge' });
  check(opened.status === 200, 'network can be opened explicitly (' + JSON.stringify(opened.data) + ')');

  let online = { ok: false, detail: 'not attempted' };
  for (let attempt = 0; attempt < 5 && !online.ok; attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 1000));
    online = await resolveHost(sandboxId);
  }
  check(online.ok, 'approved sandbox can resolve public DNS (' + (online.detail || 'no output') + ')');

  const closed = await api('POST', '/api/v1/sandboxes/' + sandboxId + '/network', { mode: 'none' });
  check(closed.status === 200, 'network can be closed again');

  await new Promise((resolve) => setTimeout(resolve, 1500));
  const offlineAgain = await resolveHost(sandboxId);
  check(!offlineAgain.ok, 'sandbox is offline again after the install step (' + (offlineAgain.detail || 'no output') + ')');

  const usage = await api('GET', '/api/v1/sandboxes/' + sandboxId + '/usage');
  check(usage.status === 200 && usage.data.limits && usage.data.limits.max_disk_gb === 2, 'usage reports limits (' + JSON.stringify(usage.data) + ')');

  const deleted = await api('DELETE', '/api/v1/sandboxes/' + sandboxId);
  check(deleted.status === 200, 'sandbox deleted');

  if (failures > 0) {
    console.log('\n' + failures + ' sandbox network check(s) failed');
    process.exit(1);
  }
  console.log('\nall sandbox network checks passed');
}

main().catch((error) => {
  console.error('sandbox network test failed: ' + error.message);
  process.exit(1);
});
