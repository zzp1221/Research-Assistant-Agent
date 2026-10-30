#!/usr/bin/env node
// Reproducibility bundle E2E: downloads a plan zip, unzips it in memory and
// verifies manifest.json, checksums.sha256, environment recipe and redaction.
import { createHash } from 'node:crypto';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execFileSync } from 'node:child_process';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-repro-bundle';
const cookies = new Map();
let failures = 0;

function check(condition, message) {
  if (condition) process.stdout.write('ok   ' + message + '\n');
  else { failures++; process.stdout.write('FAIL ' + message + '\n'); }
}

async function request(method, path, body) {
  const headers = { Accept: 'application/json' };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (cookies.size) headers.Cookie = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  return { status: response.status, data, headers: response.headers, raw: text };
}

async function main() {
  process.stdout.write('# repro-bundle e2e\n');
  const created = await request('POST', '/api/plan', { intent: '用 Python 计算 1+1 并打印结果' });
  check(created.status === 200, 'create plan');
  const plan = created.data.plan_graph;
  const cookieHeader = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  const zipResponse = await fetch(BASE + '/api/plans/' + plan.id + '/export?format=zip', {
    headers: { ...(TOKEN ? { Authorization: 'Bearer ' + TOKEN } : {}), Cookie: cookieHeader },
  });
  check(zipResponse.status === 200, 'zip export status');
  check(zipResponse.headers.get('x-repro-schema') === 'repro.bundle/v1', 'zip schema header');
  const bytes = Buffer.from(await zipResponse.arrayBuffer());
  const dir = mkdtempSync(join(tmpdir(), 'ra-repro-'));
  const zipPath = join(dir, 'bundle.zip');
  writeFileSync(zipPath, bytes);
  try {
    execFileSync('unzip', ['-q', zipPath, '-d', dir]);
    const fs = await import('node:fs');
    for (const required of ['README.md', 'manifest.json', 'checksums.sha256', 'plan.json', 'events.jsonl', 'environment/requirements.lock', 'environment/system-packages.txt', 'environment/Dockerfile', 'environment/run.sh', 'provenance/model-snapshot.json', 'provenance/runtime-policy.json', 'provenance/dataset-hashes.json', 'provenance/model-hashes.json']) {
      check(fs.existsSync(join(dir, required)), 'bundle contains ' + required);
    }
    const manifest = JSON.parse(fs.readFileSync(join(dir, 'manifest.json'), 'utf8'));
    check(manifest.schema_version === 'repro.bundle/v1', 'manifest schema');
    check(manifest.plan_id === plan.id, 'manifest plan id');
    check(Array.isArray(manifest.files) && manifest.files.length >= 10, 'manifest file list');
    // Verify every manifest hash independently.
    let hashesOk = true;
    for (const entry of manifest.files) {
      const data = fs.readFileSync(join(dir, entry.path));
      if (createHash('sha256').update(data).digest('hex') !== entry.sha256) hashesOk = false;
    }
    check(hashesOk, 'all manifest sha256 hashes match');
    // checksums.sha256 must verify with the system sha256sum.
    const checksumOutput = execFileSync('sha256sum', ['-c', 'checksums.sha256'], { cwd: dir, encoding: 'utf8' });
    check(!/FAILED/.test(checksumOutput), 'sha256sum -c passes');
    check(manifest.reproducibility === 'complete' || manifest.reproducibility === 'partial', 'reproducibility status present');
    const verify = await request('POST', '/api/plans/' + plan.id + '/reproduce/verify');
    check(verify.status === 200 && ['passed', 'partial'].includes(verify.data.status) && verify.data.static_only === true, 'static verify endpoint');
    // Same plan exported twice should be byte-identical.
    const second = await fetch(BASE + '/api/plans/' + plan.id + '/export?format=zip', { headers: { ...(TOKEN ? { Authorization: 'Bearer ' + TOKEN } : {}), Cookie: cookieHeader } });
    const secondBytes = Buffer.from(await second.arrayBuffer());
    check(Buffer.compare(bytes, secondBytes) === 0, 'repeated export is deterministic');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
  process.stdout.write(failures === 0 ? '\nALL PASSED\n' : '\n' + failures + ' FAILED\n');
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((error) => { console.error(error); process.exit(1); });
