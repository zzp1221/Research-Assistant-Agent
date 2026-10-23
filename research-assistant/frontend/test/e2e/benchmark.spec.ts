import { expect, test } from '@playwright/test';

const validation = {
  version: 'benchmark.validation/v1',
  status: 'validated',
  contract_id: 'contract-e2e',
  primary_metric: 'macro_f1',
  direction: 'maximize',
  public_metrics: { accuracy: 0.75, macro_f1: 0.74 },
  hidden_metrics: { accuracy: 1, macro_f1: 1 },
  hidden_sample_count: 4,
  target_reached: true,
  protected_files_valid: true,
  checks: ['metrics were recomputed by the backend'],
  validated_at: '2026-10-22T00:00:00Z',
};

const planGraph = {
  id: 'plan-bench',
  user_intent: 'Run a small benchmark',
  intent_type: 'Custom_Benchmark',
  status: 'completed',
  owner_id: 'e2e-user',
  session_id: 'e2e-session',
  trace_id: 'trace-e2e',
  approval: { required: false, status: 'not_required' },
  budget: { max_task_attempts: 10, max_duration_seconds: 600 },
  usage: { task_attempts: 1 },
  nodes: [
    {
      id: 'task-bench',
      name: 'Validate Hidden Benchmark Evidence',
      type: 'benchmark_validate',
      description: 'Validate hidden benchmark evidence',
      assigned_to: 'benchmark_agent',
      status: 'completed',
      dependencies: [],
      inputs: {},
      required_artifacts: [],
      output_artifacts: [],
      parallelizable: false,
      priority: 0,
      retry_limit: 1,
      run_count: 1,
      execution_epoch: 1,
      contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
      result: JSON.stringify(validation),
      structured_data: JSON.stringify(validation),
    },
  ],
  edges: [],
  artifacts: {},
  meta: { total_nodes: 1, completed_nodes: 1, failed_nodes: 0, blocked_nodes: 0, in_progress_nodes: 0, ready_nodes: 0 },
  created_at: '2026-10-22T00:00:00Z',
  updated_at: '2026-10-22T00:00:00Z',
};

test('benchmark node opens a readable report', async ({ page }) => {
  const state = {
    userId: 'e2e-user',
    activeSessionId: 'e2e-session',
    sessions: [
      {
        id: 'e2e-session',
        title: 'E2E benchmark',
        createdAt: '2026-10-22T00:00:00Z',
        updatedAt: '2026-10-22T00:00:00Z',
        chatHistory: [],
        activePlanId: 'plan-bench',
        intentContext: { raw_intent: 'Run a small benchmark', intent_type: 'Custom_Benchmark', entities: {}, constraints: {}, metadata: {} },
        planGraph,
      },
    ],
  };

  await page.addInitScript(({ key, value }) => {
    window.localStorage.setItem(key, value);
  }, { key: 'research-assistant.chat-sessions.v1', value: JSON.stringify(state) });

  await page.route(/\/api\/plans\/plan-bench\/events$/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ events: [] }) });
  });
  await page.route(/\/api\/plans\/plan-bench$/, async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ plan_graph: planGraph }) });
  });

  await page.goto('/');
  const node = page.locator('.react-flow__node').first();
  await expect(node).toBeVisible();
  await node.click();

  const reportTab = page.getByRole('button', { name: '评测报告' });
  await expect(reportTab).toBeVisible();
  await reportTab.click();
  await expect(page.getByText('已通过隐藏标签验收')).toBeVisible();
  await expect(page.getByText('公开集指标（adapter 自报）')).toBeVisible();
  await expect(page.getByText('隐藏标签重算指标（后端）')).toBeVisible();
});
