import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApprovalInboxPanel } from '../../src/features/review/ApprovalInboxPanel';

const items = [
  {
    id: 'ra-low', kind: 'approval', plan_id: 'plan-1', plan_title: 'Plan A', task_id: 't1',
    node_type: 'install_dependencies', title: '安装依赖', risk: 'low', waiting_seconds: 30,
    requested_at: '2026-10-29T01:00:00Z', action_kind: 'dependency_install',
  },
  {
    id: 'ra-high', kind: 'approval', plan_id: 'plan-2', plan_title: 'Plan B', task_id: 't2',
    node_type: 'runtime_probe', title: '使用 GPU', risk: 'high', waiting_seconds: 90,
    requested_at: '2026-10-29T01:01:00Z', action_kind: 'gpu_use',
  },
  {
    id: 'q-1', kind: 'question', plan_id: 'plan-3', plan_title: 'Plan C', task_id: 't3',
    title: '选择数据集', prompt: '使用哪个数据集？', waiting_seconds: 10,
    requested_at: '2026-10-29T01:02:00Z', options: [{ id: 'v1', label: 'v1' }, { id: 'v2', label: 'v2' }],
    allow_free_text: true,
  },
];

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  listApprovals: vi.fn(async () => ({ items, total: 3, next_cursor: '' })),
  bulkApprovals: vi.fn(async () => ({ decided: ['ra-low'], count: 1 })),
  answerQuestion: vi.fn(async () => ({ id: 'q-1' })),
}));

afterEach(() => {
  vi.clearAllMocks();
});

describe('ApprovalInboxPanel', () => {
  it('aggregates approvals and questions from multiple plans', async () => {
    render(<ApprovalInboxPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    expect(await screen.findByText('安装依赖')).toBeInTheDocument();
    expect(screen.getByText('使用 GPU')).toBeInTheDocument();
    expect(screen.getByText('选择数据集')).toBeInTheDocument();
    expect(screen.getByText(/3 项待处理/)).toBeInTheDocument();
  });

  it('disables the high-risk checkbox but allows the low-risk one', async () => {
    render(<ApprovalInboxPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    await screen.findByText('安装依赖');
    expect(screen.getByLabelText('选择 使用 GPU')).toBeDisabled();
    expect(screen.getByLabelText('选择 安装依赖')).not.toBeDisabled();
  });

  it('bulk approves only the selected low-risk approvals', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<ApprovalInboxPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    await screen.findByText('安装依赖');
    await userEvent.click(screen.getByLabelText('选择 安装依赖'));
    await userEvent.click(screen.getByRole('button', { name: /批量批准 \(1\)/ }));
    await waitFor(() => expect(api.bulkApprovals).toHaveBeenCalledWith({ ids: ['ra-low'], decision: 'approve' }));
  });

  it('answers a question with the selected option', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<ApprovalInboxPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    await screen.findByText('选择数据集');
    await userEvent.click(screen.getByLabelText('v2'));
    await userEvent.click(screen.getByRole('button', { name: '提交回答' }));
    await waitFor(() => expect(api.answerQuestion).toHaveBeenCalledWith('plan-3', 'q-1', { answer: undefined, answer_option: 'v2' }));
  });
});
