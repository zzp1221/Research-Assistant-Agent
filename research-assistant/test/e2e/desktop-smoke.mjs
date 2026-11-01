#!/usr/bin/env node
// Desktop smoke test: verifies the provider capability contract without a GUI.
// Usage: RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/desktop-smoke.mjs
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
let failures = 0;
const check = (condition, message) => {
  if (condition) process.stdout.write('ok   ' + message + '\n');
  else { failures++; process.stdout.write('FAIL ' + message + '\n'); }
};
async function get(path) {
  const response = await fetch(BASE + path, { headers: { Accept: 'application/json', Authorization: 'Bearer ' + TOKEN } });
  const data = await response.json();
  if (response.status !== 200) throw new Error(path + ' status ' + response.status + ': ' + JSON.stringify(data).slice(0, 300));
  return data;
}
async function main() {
  const health = await get('/api/health');
  check(health.ok === true, 'health ok');
  check(health.sandbox && health.sandbox.ok === true, 'sandbox health ok');
  const caps = await get('/api/sandbox/capabilities');
  check(typeof caps.provider === 'string' && caps.provider.length > 0, 'provider is exposed');
  check(typeof caps.isolation === 'string' && caps.isolation.length > 0, 'isolation is exposed');
  check(['os', 'firewall', 'none'].includes(caps.network_enforced), 'network enforcement is honest');
  check(caps.images === true, 'docker provider reports OCI images');
  process.stdout.write('provider=' + caps.provider + ' isolation=' + caps.isolation + ' network=' + caps.network_enforced + '\n');
  if (failures > 0) process.exit(1);
  process.stdout.write('desktop smoke passed\n');
}
main().catch((error) => { console.error(error); process.exit(1); });
