#!/usr/bin/env node
// Review gate + HITL E2E: exercises the pre-execution review gate, structured
// questions, the cross-plan approval inbox, bulk decisions and audit events.
import { randomUUID } from 'node:crypto';

const BASE = (process.env.RA_API_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const TOKEN = (process.env.RA_API_TOKEN || '').trim();
const USER = process.env.RA_E2E_USER || 'e2e-review-hitl-' + randomUUID().slice(0, 8);
const cookies = new Map();
let failures = 0;

function check(condition, message) {
  if (condition) process.stdout.write('ok   ' + message + '\n');
  else { failures++; process.stdout.write('FAIL ' + message + '\n'); }
}

async function request(method, path, { body, headers = {}, freshIdentity = false } = {}) {
  const finalHeaders = { Accept: 'application/json', ...headers };
  if (TOKEN) finalHeaders.Authorization = 'Bearer ' + TOKEN;
  if (!freshIdentity && cookies.size) finalHeaders.Cookie = [...cookies.entries()].map(([k, v]) => k + '=' + v).join('; ');
  let payload = body;
  if (payload !== undefined && typeof payload !== 'string') {
    finalHeaders['Content-Type'] = 'application/json';
    payload = JSON.stringify(payload);
  }
  const response = await fetch(BASE + path, { method, headers: finalHeaders, body: payload });
  if (!freshIdentity) {
    for (const raw of response.headers.getSetCookie?.() || []) {
      const pair = raw.split(';')[0] || '';
      const index = pair.indexOf('=');
      if (index > 0) cookies.set(pair.slice(0, index), pair.slice(index + 1));
    }
  }
  const text = await response.text();
  let data = text;
  try { data = JSON.parse(text); } catch {}
  return { status: response.status, data };
}

async function createPlan(body) {
  const response = await request('POST', '/api/plan', { body });
  if (response.status !== 200) throw new Error('create plan failed: ' + response.status + ' ' + JSON.stringify(response.data).slice(0, 400));
  return response.data.plan_graph;
}

function graphPayload(plan) {
  return {
    id: plan.id,
    user_intent: plan.user_intent,
    intent_type: plan.intent_type,
    status: 'pending',
    nodes: (plan.nodes || []).map((node) => ({
      id: node.id, name: node.name, type: node.type, description: node.description,
      assigned_to: node.assigned_to, status: 'pending', dependencies: node.dependencies || [],
      required_artifacts: node.required_artifacts || [], output_artifacts: node.output_artifacts || [],
      inputs: node.inputs || {}, timeout_seconds: node.timeout_seconds || 0,
      retry_limit: node.retry_limit || 0, contract: node.contract,
    })),
    edges: (plan.edges || []).map((edge) => ({ id: edge.id, from: edge.from, to: edge.to, type: edge.type })),
    artifacts: {}, budget: plan.budget,
  };
}

async function main() {
  process.stdout.write('# review-hitl e2e user=' + USER + '\n');
  const health = await request('GET', '/api/health');
  check(health.status === 200, 'health responds');

  // A4: explicit review gate blocks direct execution; approving the revision
  // allows it; rejecting keeps it blocked.
  const gated = await createPlan({ intent: '总结量子计算的基本概念', require_review: true });
  check(gated.status === 'awaiting_review' && gated.review?.required === true, 'require_review plan enters awaiting_review');
  const directRun = await request('POST', '/api/plans/' + gated.id + '/execute', { body: {} });
  check(directRun.status === 409 && directRun.data.code === 'review_pending', 'direct execution blocked while awaiting review');

  const approved = await request('POST', '/api/plans/' + gated.id + '/revisions/1/approve', { body: {} });
  check(approved.status === 200 && approved.data.review_status === 'approved', 'revision approval succeeds');
  const executeApproved = await request('POST', '/api/plans/' + gated.id + '/revisions/1/execute', { body: {} });
  check(executeApproved.status === 202, 'approved revision can execute');

  const rejectedPlan = await createPlan({ intent: '总结机器学习的基本概念', require_review: true });
  const rejected = await request('POST', '/api/plans/' + rejectedPlan.id + '/revisions/1/reject', { body: {} });
  check(rejected.status === 200 && rejected.data.review_status === 'rejected', 'revision rejection succeeds');
  const afterReject = await request('POST', '/api/plans/' + rejectedPlan.id + '/revisions/1/execute', { body: {} });
  check(afterReject.status === 409, 'rejected revision cannot execute');

  // A6: structured question create -> inbox -> answer -> task resumes.
  const qPlan = await createPlan({ intent: '用 Python 画一个正弦函数图像' });
  const taskId = qPlan.nodes?.[0]?.id;
  const question = await request('POST', '/api/plans/' + qPlan.id + '/questions', {
    body: {
      task_id: taskId,
      header: '选择数据集',
      prompt: '使用哪个版本的训练集？',
      options: [{ id: 'v1', label: 'v1（原始）' }, { id: 'v2', label: 'v2（清洗后）' }],
      allow_free_text: true,
      default_option: 'v1',
      timeout_seconds: 600,
    },
  });
  check(question.status === 200 && question.data.question?.status === 'pending', 'structured question created');
  const qId = question.data.question.id;
  const listQ = await request('GET', '/api/plans/' + qPlan.id + '/questions');
  check(listQ.status === 200 && listQ.data.questions?.length === 1, 'question list returns the pending question');
  const answered = await request('POST', '/api/plans/' + qPlan.id + '/questions/' + qId + '/answer', {
    body: { answer_option: 'v2' },
  });
  check(answered.status === 200 && answered.data.question?.status === 'answered', 'question answered');
  const reloaded = await request('GET', '/api/plans/' + qPlan.id);
  const task = (reloaded.data.plan_graph?.nodes || []).find((node) => node.id === taskId);
  check(task && task.status === 'ready', 'task resumed after answer');

  // A6 timeout: a question with a default auto-answers; without one it blocks.
  const timeoutPlan = await createPlan({ intent: '总结深度学习的应用' });
  const timeoutQ = await request('POST', '/api/plans/' + timeoutPlan.id + '/questions', {
    body: {
      task_id: timeoutPlan.nodes?.[0]?.id,
      prompt: 'continue?',
      options: [{ id: 'yes', label: 'yes' }],
      default_option: 'yes',
      timeout_seconds: 1,
    },
  });
  check(timeoutQ.status === 200, 'timeout question created');
  await new Promise((resolve) => setTimeout(resolve, 1600));
  const swept = await request('GET', '/api/plans/' + timeoutPlan.id + '/questions');
  const sweptQuestion = (swept.data.questions || []).find((item) => item.id === timeoutQ.data.question.id);
  check(sweptQuestion?.status === 'answered' && sweptQuestion?.auto_answered === true, 'overdue question auto-answered from default');

  // A7: inbox aggregates pending items across plans. Two plans each get one
  // pending question; the inbox must show both in a single call.
  const inboxPlanA = await createPlan({ intent: '总结数据库系统的基本概念' });
  const inboxPlanB = await createPlan({ intent: '总结操作系统的调度算法' });
  for (const candidate of [inboxPlanA, inboxPlanB]) {
    const created = await request('POST', '/api/plans/' + candidate.id + '/questions', {
      body: { task_id: candidate.nodes?.[0]?.id, prompt: 'pending inbox item', allow_free_text: true, timeout_seconds: 600 },
    });
    check(created.status === 200, 'pending question created for inbox');
  }
  const inbox = await request('GET', '/api/approvals?status=pending&kind=question&limit=100');
  check(inbox.status === 200, 'inbox responds');
  const planIds = new Set((inbox.data.items || []).map((item) => item.plan_id));
  check(planIds.has(inboxPlanA.id) && planIds.has(inboxPlanB.id), 'inbox aggregates pending items from two plans');

  // The bulk endpoint must validate ids rather than silently accepting them.
  const bulkMissing = await request('POST', '/api/approvals/bulk', { body: { ids: ['ra-does-not-exist'], decision: 'approve' } });
  check(bulkMissing.status === 404 && bulkMissing.data.code === 'action_not_found', 'bulk rejects unknown approval ids');

  // A8: another principal cannot see the owner's inbox entries.
  const otherInbox = await request('GET', '/api/approvals?status=pending', { freshIdentity: true });
  check(otherInbox.status === 200 && (otherInbox.data.items || []).length === 0, 'inbox is owner scoped');

  // A9: audit events are queryable.
  const audit = await request('GET', '/api/audit/export?format=jsonl&limit=500');
  const auditText = typeof audit.data === 'string' ? audit.data : JSON.stringify(audit.data);
  check(audit.status === 200 && auditText.includes('plan_reviewed'), 'audit records plan_reviewed');
  check(auditText.includes('question_answered'), 'audit records question_answered');

  process.stdout.write(failures === 0 ? '\nALL PASSED\n' : '\n' + failures + ' FAILED\n');
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});