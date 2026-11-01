#!/usr/bin/env node
// Verifies the provider-neutral command contract against a live sandbox.
// Run inside the compose network: node test/e2e/sandbox-command.mjs
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
    headers: { Authorization: 'Bearer ' + TOKEN, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  return { status: response.status, data };
}
async function main() {
  const created = await api('POST', '/api/v1/sandboxes', {
    image: IMAGE, task_id: 'command-e2e', tenant_id: 'e2e', plan_id: 'command-e2e-plan',
    network_mode: 'none', limits: { max_disk_gb: 2, max_runtime_sec: 120 },
  });
  if (created.status !== 200) { console.error('cannot create sandbox: ' + JSON.stringify(created.data)); process.exit(1); }
  const id = created.data.sandbox_id;
  check(Boolean(id), 'sandbox created');
  try {
    const version = await api('POST', '/api/v1/sandboxes/' + id + '/commands', {
      argv: ['python', '--version'], cwd: '/workspace', env: { PYTHONPATH: '/workspace' }, max_output_kb: 64,
    });
    check(version.status === 200, 'argv command accepted');
    check(version.data.exit_code === 0, 'argv command exit 0');
    check(String(version.data.stdout).includes('Python'), 'argv command stdout has Python version');

    const legacy = await api('POST', '/api/v1/sandboxes/' + id + '/commands', { cmd: ['python3', '-c', 'print("legacy-ok")'] });
    check(legacy.status === 200 && String(legacy.data.stdout).includes('legacy-ok'), 'legacy cmd payload still works');

    const big = await api('POST', '/api/v1/sandboxes/' + id + '/commands', {
      argv: ['python', '-c', 'print("x" * 5000)'], max_output_kb: 1,
    });
    check(big.status === 200, 'large output command accepted');
    check(big.data.truncated === true, 'large output is marked truncated');
    check(String(big.data.stdout).length <= 1200, 'large output is bounded');
  } finally {
    await api('DELETE', '/api/v1/sandboxes/' + id);
  }
  if (failures > 0) process.exit(1);
  console.log('all sandbox command checks passed');
}
main().catch((error) => { console.error(error); process.exit(1); });
