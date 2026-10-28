#!/usr/bin/env node
// Round 7 real-LLM scenarios: execute plans end-to-end and verify the textual outputs.
// Usage: RA_API_URL=http://backend:8080 RA_API_TOKEN=... node test/e2e/round7-real-scenarios.mjs
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const FIXTURES = process.env.RA_FIXTURES_DIR || join(process.cwd(), 'test', 'fixtures');
const REPORT_DIR = process.env.RA_REPORT_DIR || '/reports';
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

async function request(method, path, { body, form, headers = {}, user = 'round7-real', timeoutMs = 180000 } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (user) finalHeaders['X-User-Id'] = user;
  const jar = jarFor(user);
  if (jar.size) finalHeaders.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (form) payload = form;
  else if (payload !== undefined && payload !== null && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload, signal: AbortSignal.timeout(timeoutMs) });
  for (const cookie of response.headers.getSetCookie?.() || []) {
    const pair = cookie.split(';')[0] || '';
    const index = pair.indexOf('=');
    if (index > 0) jar.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim());
  }
  const contentType = response.headers.get('content-type') || '';
  let data;
  if (contentType.includes('application/json')) data = await response.json();
  else data = await response.text();
  return { status: response.status, data, headers: response.headers };
}

function allText(plan) {
  const chunks = [];
  for (const node of plan.nodes || []) {
    if (node.result) chunks.push(String(node.result));
    if (node.structured_data) chunks.push(typeof node.structured_data === 'string' ? node.structured_data : JSON.stringify(node.structured_data));
  }
  for (const [key, artifact] of Object.entries(plan.artifacts || {})) {
    const value = artifact?.value;
    if (typeof value === 'string' && value.trim()) chunks.push(value);
  }
  return chunks.join('\n---\n');
}
function artifactValue(plan, key) {
  const artifact = plan.artifacts?.[key];
  return artifact && typeof artifact.value === 'string' ? artifact.value : '';
}
function findPng(plan) {
  for (const [key, artifact] of Object.entries(plan.artifacts || {})) {
    const value = artifact?.value || '';
    if (typeof value === 'string' && value.startsWith('iVBOR')) return { key, buffer: Buffer.from(value, 'base64') };
  }
  for (const node of plan.nodes || []) {
    if (node.image_base64) return { key: 'node:' + node.name, buffer: Buffer.from(node.image_base64, 'base64') };
  }
  return null;
}

async function createPlan(user, intent, attachments = []) {
  const response = await request('POST', '/api/plan', { body: { intent, attachments, runtime_permission_mode: 'full_access', runtime_permission_acknowledged: true }, user });
  if (response.status !== 200) throw new Error('create plan failed: ' + response.status + ' ' + JSON.stringify(response.data).slice(0, 400));
  return response.data.plan_graph;
}

async function approveRuntimeActions(user, planId) {
  const list = await request('GET', '/api/plans/' + planId + '/runtime-actions', { user });
  for (const action of list.data?.actions || []) {
    if (action.status === 'pending') {
      const result = await request('POST', '/api/plans/' + planId + '/runtime-actions/' + action.id + '/decision', { body: { decision: 'approve', note: 'round7 test auto-approval' }, user });
      console.log('[approve] runtime action ' + action.kind + ' -> ' + result.status);
    }
  }
}

async function runPlan(user, plan, timeoutMs, label) {
  if (plan.approval?.required) {
    await request('POST', '/api/plans/' + plan.id + '/approve', { body: {}, user });
  }
  const executed = await request('POST', '/api/plans/' + plan.id + '/execute', { body: {}, user });
  if (![200, 202].includes(executed.status)) throw new Error('execute failed: ' + executed.status + ' ' + JSON.stringify(executed.data).slice(0, 300));
  const started = Date.now();
  let current = plan;
  let lastDone = -1;
  while (Date.now() - started < timeoutMs) {
    current = (await request('GET', '/api/plans/' + plan.id, { user })).data.plan_graph;
    const done = (current.nodes || []).filter((n) => ['completed', 'failed', 'skipped', 'canceled'].includes(n.status)).length;
    if (done !== lastDone) {
      console.log('[' + label + '] status=' + current.status + ' ' + done + '/' + (current.nodes || []).length);
      for (const node of current.nodes || []) if (node.status === 'failed') console.log('  failed ' + node.name + ': ' + String(node.error || '').slice(0, 300));
      lastDone = done;
    }
    if (current.status === 'awaiting_approval') await approveRuntimeActions(user, plan.id);
    if (['completed', 'failed', 'canceled', 'blocked'].includes(current.status)) return current;
    await sleep(4000);
  }
  throw new Error('plan ' + plan.id + ' timed out after ' + timeoutMs + 'ms');
}

async function main() {
  const results = {};

  // 1) Chat endpoint: direct textual answer.
  const chat = await request('POST', '/api/chat', { body: { message: '请只回答数字：1+1等于几？' }, user: 'round7-chat', timeoutMs: 240000 });
  const chatText = String(chat.data?.response || '');
  check(chat.status === 200 && chatText.includes('2'), 'chat answers 1+1=2', chatText.slice(0, 80));
  results.chat = { status: chat.status, response: chatText };

  // 2) General plan: short factual answer.
  const general = await createPlan('round7-general', '请只回答：1+1等于几？只输出数字。');
  check(general.intent_type === 'General' && general.nodes.length === 1, 'general direct plan created', general.nodes.length);
  const generalFinal = await runPlan('round7-general', general, 300000, 'general');
  const generalText = allText(generalFinal);
  check(generalFinal.status === 'completed' && /(^|\D)2(\D|$)/.test(generalText), 'general answer contains 2', generalFinal.status);
  results.general = { plan_id: generalFinal.id, status: generalFinal.status, text: generalText.slice(0, 2000) };

  // 3) Code execution: compute 2**10.
  const code = await createPlan('round7-code', '用 Python 计算 2 的 10 次方，并打印结果。');
  check(code.intent_type === 'Code_Execution', 'code plan classified as Code_Execution', code.intent_type);
  const codeFinal = await runPlan('round7-code', code, 600000, 'code');
  const codeText = allText(codeFinal);
  const executionResult = artifactValue(codeFinal, 'execution_result');
  const generatedCode = artifactValue(codeFinal, 'generated_code');
  check(codeFinal.status === 'completed', 'code plan completed', codeFinal.status);
  check(executionResult.includes('1024') || codeText.includes('1024'), 'code output contains 1024', executionResult.slice(0, 120));
  check(generatedCode.includes('**') || generatedCode.includes('pow('), 'generated code uses exponentiation');
  results.code = { plan_id: codeFinal.id, status: codeFinal.status, execution_result: executionResult.slice(0, 2000), generated_code: generatedCode.slice(0, 4000) };

  // 4) Plot: sin/cos comparison PNG.
  const plot = await createPlan('round7-plot', '用 Python 绘制 y=sin(x) 和 y=cos(x) 在 [-6.28, 6.28] 区间内的对比图，保存为 PNG 文件。');
  check(plot.intent_type === 'Code_Execution', 'plot plan classified as Code_Execution', plot.intent_type);
  const plotFinal = await runPlan('round7-plot', plot, 600000, 'plot');
  const png = findPng(plotFinal);
  check(plotFinal.status === 'completed' && Boolean(png), 'plot plan produced a PNG artifact', plotFinal.status);
  if (png) {
    const isPng = png.buffer.subarray(0, 8).toString('hex') === '89504e470d0a1a0a';
    const width = png.buffer.readUInt32BE(16);
    const height = png.buffer.readUInt32BE(20);
    check(isPng && width >= 400 && height >= 300 && png.buffer.length > 10000, 'plot PNG has valid dimensions', { width, height, bytes: png.buffer.length, key: png.key });
    await writeFile(join(REPORT_DIR, 'round7-plot.png'), png.buffer);
  }
  results.plot = { plan_id: plotFinal.id, status: plotFinal.status, png: png ? { key: png.key, bytes: png.buffer.length } : null };

  // 5) Paper analysis with page citations, using the real Attention PDF.
  const pdf = await readFile(join(process.env.RA_PAPER_DIR || '/papers', 'attention-is-all-you-need.pdf'));
  const form = new FormData();
  form.append('file', new Blob([pdf], { type: 'application/pdf' }), 'attention-is-all-you-need.pdf');
  const upload = await request('POST', '/api/uploads', { form, user: 'round7-paper' });
  check(upload.status === 201 && Boolean(upload.data.id), 'paper PDF uploaded', upload.status);
  const paper = await createPlan('round7-paper', '分析一下这篇论文的主要创新点和局限性，请引用具体页码。', [upload.data.id]);
  check(paper.intent_type === 'General' && (paper.meta?.general_plan_shape === 'paper' || paper.nodes.length >= 3), 'paper analysis plan shape', { intent: paper.intent_type, nodes: paper.nodes.length, shape: paper.meta?.general_plan_shape });
  const paperFinal = await runPlan('round7-paper', paper, 900000, 'paper');
  const paperText = [artifactValue(paperFinal, 'final_report'), artifactValue(paperFinal, 'general_response'), artifactValue(paperFinal, 'innovation_limitation_notes'), allText(paperFinal)].filter(Boolean).join('\n---\n');
  const hasPageCitation = /第\s*\d+\s*页|\[?\s*p\.?\s*\d+|Page\s+\d+/i.test(paperText);
  const mentionsTransformer = /Transformer|自注意力|self-attention/i.test(paperText);
  const mentionsBleu = /BLEU|28\.4/i.test(paperText);
  check(paperFinal.status === 'completed', 'paper analysis completed', paperFinal.status);
  check(paperText.length > 1000, 'paper analysis is substantive', paperText.length);
  check(mentionsTransformer && mentionsBleu, 'paper analysis mentions Transformer and BLEU', { transformer: mentionsTransformer, bleu: mentionsBleu });
  check(hasPageCitation, 'paper analysis cites page numbers');
  await writeFile(join(REPORT_DIR, 'round7-paper-analysis.md'), paperText);
  results.paper_analysis = { plan_id: paperFinal.id, status: paperFinal.status, chars: paperText.length, page_citations: hasPageCitation };

  // 6) Literature search: real metadata quality.
  const search = await request('GET', '/api/literature/search?q=' + encodeURIComponent('graph neural network survey') + '&sources=arxiv,openalex&limit=5', { user: 'round7-lit-search', timeoutMs: 120000 });
  const items = search.data?.results || [];
  const titled = items.filter((item) => String(item.title || '').trim().length > 10);
  const relevant = titled.filter((item) => /graph|neural|survey/i.test(String(item.title)));
  check(search.status === 200 && items.length >= 1 && titled.length === items.length, 'literature search returns titled results', items.length);
  check(relevant.length >= Math.min(3, items.length), 'literature results are topically relevant', relevant.length + '/' + items.length);
  results.literature_search = { status: search.status, count: items.length, titles: items.map((i) => i.title).slice(0, 5) };

  await writeFile(join(REPORT_DIR, 'round7-real-scenarios.json'), JSON.stringify(results, null, 2));
  if (failures > 0) {
    console.log('\n' + failures + ' real-scenario check(s) failed');
    process.exit(1);
  }
  console.log('\nall real-scenario checks passed');
}

main().catch((error) => {
  console.error('real scenarios failed: ' + (error?.stack || error));
  process.exit(1);
});
