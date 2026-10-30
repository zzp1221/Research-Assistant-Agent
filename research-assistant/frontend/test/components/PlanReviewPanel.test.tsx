import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { PlanReviewPanel } from '../../src/features/review/PlanReviewPanel';

const graph = {
  id: 'plan-1',
  title: 'Attention reproduction',
  user_intent: 'reproduce attention',
  intent_type: 'Paper_Reproduction',
  status: 'awaiting_review',
  trace_id: 'trace',
  approval: { required: false, status: 'not_required' },
  budget: { max_task_attempts: 10, max_duration_seconds: 600, max_cost_usd: 1.5, max_tokens: 10000 },
  usage: { task_attempts: 0 },
  nodes: [
    {
      id: 'n1', name: 'Generate', type: 'generate_code', description: 'gen', assigned_to: 'coder_agent',
      status: 'pending', dependencies: [], required_artifacts: [], output_artifacts: [], parallelizable: false,
      priority: 0, retry_limit: 1, run_count: 0, execution_epoch: 0, inputs: { prompt: 'v1' },
      contract: { version: 'v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    },
    {
      id: 'n2', name: 'Run', type: 'execute_code', description: 'run', assigned_to: 'sandbox_agent',
      status: 'pending', dependencies: ['n1'], required_artifacts: [], output_artifacts: [], parallelizable: false,
      priority: 0, retry_limit: 0, run_count: 0, execution_epoch: 0,
      contract: { version: 'v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
    },
  ],
  edges: [{ id: 'e1', from: 'n1', to: 'n2', type: 'control' }],
  artifacts: {},
};

const revisions = [
  { id: 'rev-1', plan_id: 'plan-1', revision: 1, parent_revision: 0, status: 'draft', created_at: '2026-10-29T01:00:00Z', reason: 'initial', hash: 'abcdef1234567890' },
  { id: 'rev-2', plan_id: 'plan-1', revision: 2, parent_revision: 1, status: 'draft', created_at: '2026-10-29T02:00:00Z', reason: 'change prompt', hash: '1234567890abcdef' },
];

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  listRevisions: vi.fn(async () => ({ revisions, current_revision: 1, latest_revision: 2 })),
  getRevision: vi.fn(async () => ({ ...revisions[1], graph })),
  createRevision: vi.fn(async () => ({ revision: revisions[1], plan: {} })),
  validateRevision: vi.fn(async () => ({ valid: true, errors: [] })),
  approveRevision: vi.fn(async () => ({ status: 'approved', review_status: 'approved' })),
  rejectRevision: vi.fn(async () => ({ status: 'rejected', review_status: 'rejected' })),
  executeRevision: vi.fn(async () => ({ message: 'started' })),
  getPlanDiff: vi.fn(async () => [
    { kind: 'node_modified', node_id: 'n1', field: 'name', before: 'Generate', after: 'Generate v2' },
  ]),
}));

afterEach(() => {
  vi.clearAllMocks();
});

describe('PlanReviewPanel', () => {
  it('lists revisions and loads the selected graph', async () => {
    render(<PlanReviewPanel planId="plan-1" onClose={() => undefined} onApplied={() => undefined} />);
    expect(await screen.findByText('r1')).toBeInTheDocument();
    expect(screen.getByText('r2')).toBeInTheDocument();
    expect(await screen.findByDisplayValue('Generate')).toBeInTheDocument();
    expect(screen.getByText(/当前执行 r1/)).toBeInTheDocument();
    expect(screen.getByText(/最新 r2/)).toBeInTheDocument();
  });

  it('saves a draft through createRevision with the latest base revision', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    const onApplied = vi.fn();
    render(<PlanReviewPanel planId="plan-1" onClose={() => undefined} onApplied={onApplied} />);
    await screen.findByDisplayValue('Generate');
    await userEvent.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(api.createRevision).toHaveBeenCalled());
    expect(vi.mocked(api.createRevision).mock.calls[0][1].base_revision).toBe(2);
    expect(onApplied).toHaveBeenCalled();
  });

  it('validates the selected revision', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<PlanReviewPanel planId="plan-1" onClose={() => undefined} onApplied={() => undefined} />);
    await screen.findByDisplayValue('Generate');
    await userEvent.click(screen.getByRole('button', { name: '校验' }));
    await waitFor(() => expect(api.validateRevision).toHaveBeenCalledWith('plan-1', 2));
    expect(await screen.findByText('校验通过')).toBeInTheDocument();
  });

  it('approves and executes a revision', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<PlanReviewPanel planId="plan-1" onClose={() => undefined} onApplied={() => undefined} />);
    await screen.findByDisplayValue('Generate');
    await userEvent.click(screen.getByRole('button', { name: '批准并运行' }));
    await waitFor(() => expect(api.approveRevision).toHaveBeenCalledWith('plan-1', 2));
    expect(vi.mocked(api.executeRevision).mock.calls[0][0]).toBe('plan-1');
    expect(vi.mocked(api.executeRevision).mock.calls[0][1]).toBe(2);
  });

  it('shows the diff against the previous revision', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<PlanReviewPanel planId="plan-1" onClose={() => undefined} onApplied={() => undefined} />);
    await screen.findByDisplayValue('Generate');
    await userEvent.click(screen.getByRole('button', { name: /与 r1 比较/ }));
    await waitFor(() => expect(api.getPlanDiff).toHaveBeenCalledWith('plan-1', 1, 2));
    expect(await screen.findByText('Generate v2')).toBeInTheDocument();
  });
});