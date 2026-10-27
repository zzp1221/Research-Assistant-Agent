#!/usr/bin/env node
// Research Assistant CLI (dependency-free).
//
// Usage:
//   RA_API_URL=http://localhost:18080 RA_API_TOKEN=... node scripts/ra.mjs create --intent "画一个正弦对比图"
//   node scripts/ra.mjs status <planId>
//   node scripts/ra.mjs execute <planId> --trigger auto
//   node scripts/ra.mjs events <planId> --after 0
//   node scripts/ra.mjs pause|resume|cancel|approve <planId>
//   node scripts/ra.mjs wait <planId> --timeout 3600
//   node scripts/ra.mjs export <planId> --format zip --out plan.zip
//   node scripts/ra.mjs upload ./paper.pdf
//   node scripts/ra.mjs search "community detection" --sources arxiv,openalex
//   node scripts/ra.mjs import 1706.03762
//   node scripts/ra.mjs openapi > openapi.json

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const BASE = (process.env.RA_API_URL || 'http://localhost:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const COOKIE_FILE = process.env.RA_COOKIE_FILE || path.join(os.homedir(), '.research-assistant-cookies.json');

function loadCookies() {
  try {
    return JSON.parse(fs.readFileSync(COOKIE_FILE, 'utf8'));
  } catch {
    return {};
  }
}

function saveCookies(cookies) {
  try {
    fs.writeFileSync(COOKIE_FILE, JSON.stringify(cookies, null, 2), { mode: 0o600 });
  } catch {
    // Cookie persistence is best-effort; a read-only HOME must not break the CLI.
  }
}

let cookies = loadCookies();

async function request(method, requestPath, options = {}) {
  const headers = { Accept: 'application/json', ...(options.headers || {}) };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  const cookieHeader = Object.entries(cookies).map(([name, value]) => name + '=' + value).join('; ');
  if (cookieHeader) headers.Cookie = cookieHeader;
  let body = options.body;
  if (body !== undefined && !(body instanceof FormData) && typeof body !== 'string') {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(body);
  }
  const response = await fetch(BASE + requestPath, { method, headers, body, signal: AbortSignal.timeout(options.timeoutMs ?? 180000) });
  const setCookies = typeof response.headers.getSetCookie === 'function' ? response.headers.getSetCookie() : [];
  for (const raw of setCookies) {
    const pair = raw.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) cookies[pair.slice(0, index).trim()] = pair.slice(index + 1).trim();
  }
  if (setCookies.length) saveCookies(cookies);
  const contentType = response.headers.get('content-type') || '';
  let payload;
  if (contentType.includes('application/json')) payload = await response.json();
  else if (contentType.startsWith('text/') || contentType.includes('application/x-ndjson')) payload = await response.text();
  else payload = Buffer.from(await response.arrayBuffer());
  if (!response.ok) {
    const message = typeof payload === 'string' ? payload : JSON.stringify(payload);
    const error = new Error('HTTP ' + response.status + ': ' + message);
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

function parseArgs(argv) {
  const args = [...argv];
  const command = args.shift();
  const options = {};
  const positional = [];
  while (args.length) {
    const item = args.shift();
    if (item.startsWith('--')) {
      const key = item.slice(2);
      const next = args[0];
      if (next === undefined || next.startsWith('--')) options[key] = true;
      else options[key] = args.shift();
    } else {
      positional.push(item);
    }
  }
  return { command, options, positional };
}

async function uploadFile(filePath) {
  const buffer = fs.readFileSync(filePath);
  const form = new FormData();
  form.append('file', new Blob([buffer]), path.basename(filePath));
  return request('POST', '/api/uploads', { body: form });
}

function print(value) {
  if (Buffer.isBuffer(value)) process.stdout.write(value);
  else if (typeof value === 'string') process.stdout.write(value.endsWith('\n') ? value : value + '\n');
  else process.stdout.write(JSON.stringify(value, null, 2) + '\n');
}

async function waitForPlan(planId, timeoutSeconds) {
  const deadline = Date.now() + timeoutSeconds * 1000;
  let lease = 0;
  while (Date.now() < deadline) {
    const payload = await request('GET', '/api/plans/' + planId);
    const plan = payload.plan_graph;
    const status = String(plan.status || '');
    if (status === 'completed' || status === 'failed' || status === 'canceled') {
      return plan;
    }
    process.stderr.write('[wait] status=' + status + ' paused=' + Boolean(plan.paused) + '\n');
    if (plan.missing_requirements?.length) {
      for (const missing of plan.missing_requirements) {
        process.stderr.write('[missing] ' + missing.kind + ' ' + missing.name + ': ' + missing.detail + ' -> ' + missing.remediation + '\n');
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 2000 + lease));
    lease = Math.min(lease + 1000, 5000);
  }
  throw new Error('timed out waiting for plan ' + planId);
}

async function main() {
  const { command, options, positional } = parseArgs(process.argv.slice(2));
  if (!command || command === 'help' || options.help) {
    print(`Research Assistant CLI

Commands:
  create --intent <text> [--file <uploadId>]... [--mode confirm_important|full_access] [--auto]
  list [--limit 20]
  status <planId>
  events <planId> [--after <seq>]
  execute <planId> [--trigger manual|auto]
  pause|resume|cancel|approve <planId>
  wait <planId> [--timeout 3600]
  export <planId> [--format md|json|zip] [--out <file>]
  upload <file>
  search <query> [--sources arxiv,openalex,crossref,github] [--limit 10]
  import <arxivId|doi|url> [--no-attach]
  openapi
  health

Environment: RA_API_URL, RA_API_TOKEN, RA_COOKIE_FILE`);
    return;
  }
  if (command === 'health') return print(await request('GET', '/api/health'));
  if (command === 'openapi') return print(await request('GET', '/openapi.json'));
  if (command === 'create') {
    const attachments = [];
    if (options.file) attachments.push(...[options.file].flat());
    const payload = { intent: options.intent || positional.join(' ') };
    if (attachments.length) payload.attachments = attachments;
    if (options.mode) payload.runtime_permission_mode = options.mode;
    if (options.mode === 'full_access') payload.runtime_permission_acknowledged = true;
    const response = await request('POST', '/api/plan', { body: payload });
    print(response);
    if (options.auto) {
      await request('POST', '/api/plans/' + response.plan_graph.id + '/execute', { body: { trigger: 'auto' } });
      process.stderr.write('[create] plan started (trigger=auto)\n');
    }
    return;
  }
  if (command === 'list') {
    const limit = options.limit || 20;
    return print(await request('GET', '/api/plans?limit=' + encodeURIComponent(limit)));
  }
  const planId = positional[0];
  if (['status', 'events', 'execute', 'pause', 'resume', 'cancel', 'approve', 'wait', 'export'].includes(command)) {
    if (!planId) throw new Error(command + ' requires a plan id');
  }
  if (command === 'status') return print((await request('GET', '/api/plans/' + planId)).plan_graph);
  if (command === 'events') {
    const after = options.after || 0;
    return print(await request('GET', '/api/plans/' + planId + '/events?after_seq=' + encodeURIComponent(after)));
  }
  if (command === 'execute') {
    const trigger = options.trigger === 'auto' ? 'auto' : 'manual';
    return print(await request('POST', '/api/plans/' + planId + '/execute', { body: { trigger } }));
  }
  if (['pause', 'resume', 'cancel', 'approve'].includes(command)) {
    return print(await request('POST', '/api/plans/' + planId + '/' + command));
  }
  if (command === 'wait') {
    const timeout = Number(options.timeout || 3600);
    return print(await waitForPlan(planId, timeout));
  }
  if (command === 'export') {
    const format = options.format || 'md';
    const payload = await request('GET', '/api/plans/' + planId + '/export?format=' + encodeURIComponent(format));
    const out = options.out || ('plan-' + planId + '.' + (format === 'markdown' ? 'md' : format));
    if (Buffer.isBuffer(payload)) fs.writeFileSync(out, payload);
    else fs.writeFileSync(out, typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2));
    process.stderr.write('[export] wrote ' + out + '\n');
    return;
  }
  if (command === 'upload') {
    if (!positional[0]) throw new Error('upload requires a file path');
    return print(await uploadFile(positional[0]));
  }
  if (command === 'search') {
    const query = options.q || positional.join(' ');
    if (!query) throw new Error('search requires a query');
    const params = new URLSearchParams({ q: query });
    if (options.sources) params.set('sources', options.sources);
    if (options.limit) params.set('limit', String(options.limit));
    return print(await request('GET', '/api/literature/search?' + params.toString()));
  }
  if (command === 'import') {
    const ref = positional[0];
    if (!ref) throw new Error('import requires an arXiv id/DOI/URL');
    return print(await request('POST', '/api/literature/import', { body: { ref, attach: !options['no-attach'] } }));
  }
  throw new Error('unknown command: ' + command);
}

main().catch((error) => {
  process.stderr.write('[ra] ' + (error?.payload ? JSON.stringify(error.payload) : error.message) + '\n');
  process.exit(1);
});
