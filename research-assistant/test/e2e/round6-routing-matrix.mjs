#!/usr/bin/env node
// Round-6 deep routing matrix: records plan meta, node shape and creation latency.
import { randomUUID } from 'node:crypto';
const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = 'e2e-matrix-' + Math.random().toString(36).slice(2, 10);
const jars = new Map();
const jar = jars.get(USER) || new Map(); jars.set(USER, jar);
let failures = 0;
const check = (ok, msg) => { console.log((ok ? 'ok   ' : 'FAIL ') + msg); if (!ok) failures++; };
async function request(method, path, body) {
  const headers = { Accept: 'application/json', 'X-User-Id': USER };
  if (TOKEN) headers.Authorization = 'Bearer ' + TOKEN;
  if (jar.size) headers.Cookie = [...jar.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(payload); }
  const response = await fetch(BASE + path, { method, headers, body: payload });
  for (const raw of response.headers.getSetCookie?.() || []) {
    const pair = raw.split(';')[0] || ''; const i = pair.indexOf('=');
    if (i > 0) jar.set(pair.slice(0, i), pair.slice(i + 1));
  }
  const ct = response.headers.get('content-type') || '';
  const data = ct.includes('application/json') ? await response.json() : await response.text();
  return { status: response.status, data };
}
const cases = [
  ['你好', 'General', 'L0', 'fast_path', 'simple', ['general_synthesis']],
  ['解释一下 RAG 是什么', 'General', 'L0', 'fast_path', 'simple', ['general_synthesis']],
  ['总结 RAG 技术的最新进展', 'General', 'L2', 'standard_template', 'standard', ['general_research','general_synthesis']],
  ['用 Python 计算 1+1', 'Code_Execution', 'L1', 'fast_path', 'simple', ['generate_code','execute_code','verify_result']],
  ['用 Python 画一个正弦函数和余弦函数的对比图', 'Code_Execution', 'L2', 'standard_template', 'standard', null],
  ['对比 LangChain 和 LlamaIndex 的响应时间柱状图', 'Framework_Evaluation', null, null, null, null],
  ['帮我找 5 篇关于 graph neural network 的综述并总结', 'General', null, null, null, null],
  ['复现论文 Attention Is All You Need', 'Paper_Reproduction', null, null, null, null],
  ['对 LangChain、LlamaIndex、Haystack 三个框架做多指标 benchmark 并生成报告', 'Framework_Evaluation', null, null, null, null],
  ['先分析数据，再训练模型，然后评估指标，最后生成报告，使用自定义评估流程', 'General', null, null, null, null],
];
const rows = [];
for (const [intent, expectType, expectComplexity, expectRoute, expectVariant, expectNodes] of cases) {
  const t0 = Date.now();
  const created = await request('POST', '/api/plan', { intent });
  const ms = Date.now() - t0;
  if (created.status !== 200) { check(false, intent + ' -> HTTP ' + created.status + ' ' + JSON.stringify(created.data).slice(0, 200)); continue; }
  const p = created.data.plan_graph;
  const types = (p.nodes || []).map(n => n.type);
  const meta = p.meta || {};
  rows.push({ intent, ms, intent_type: p.intent_type, complexity: meta.complexity, route: meta.planning_route, variant: meta.template_variant, shape: meta.general_plan_shape, nodes: types });
  check(p.intent_type === expectType, intent + ': intent_type=' + p.intent_type + ' (expected ' + expectType + ')');
  if (expectComplexity) check(meta.complexity === expectComplexity, intent + ': complexity=' + meta.complexity + ' (expected ' + expectComplexity + ')');
  if (expectRoute) check(meta.planning_route === expectRoute, intent + ': route=' + meta.planning_route + ' (expected ' + expectRoute + ')');
  if (expectVariant) check(meta.template_variant === expectVariant, intent + ': variant=' + meta.template_variant + ' (expected ' + expectVariant + ')');
  if (expectNodes) check(JSON.stringify(types) === JSON.stringify(expectNodes), intent + ': nodes=' + JSON.stringify(types));
  check(Boolean(meta.planning_route && meta.complexity && meta.template_variant), intent + ': meta route fields present');
  if (p.id) await request('DELETE', '/api/plans/' + p.id);
}
console.log('\nMATRIX ' + JSON.stringify(rows, null, 2));
const slow = rows.filter(r => r.ms > 120000);
check(slow.length === 0, 'all plan creations under the 120s hard timeout (slow: ' + slow.map(r => r.intent + '=' + r.ms + 'ms').join('; ') + ')');
const over5s = rows.filter(r => r.ms > 5000);
if (over5s.length) console.log('WARN ' + over5s.length + ' plan creations exceeded 5s: ' + over5s.map(r => r.intent + '=' + r.ms + 'ms').join('; '));
if (failures) { console.log('\n' + failures + ' failure(s)'); process.exit(1); }
console.log('\nround6 routing matrix passed');
