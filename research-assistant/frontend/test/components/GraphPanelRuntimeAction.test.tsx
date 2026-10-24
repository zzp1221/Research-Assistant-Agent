import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { GraphPanel } from '../../src/features/plan-graph/GraphPanel';
import type { RuntimeAction } from '../../src/contracts/api';

const runtimeAction: RuntimeAction = {
  id: 'ra-test',
  plan_id: 'plan-test',
  task_id: 'task-test',
  node_type: 'install_dependencies',
  kind: 'dependency_install',
  title: '安装论文依赖',
  details: { packages: ['mock==2.0.0'] },
  risk: 'medium',
  status: 'pending',
  requested_at: '2026-10-24T00:00:00Z',
};

const baseProps = {
  nodes: [],
  edges: [],
  onNodesChange: vi.fn(),
  onEdgesChange: vi.fn(),
  onNodeClick: vi.fn(),
  intentContext: null,
  runtimeActions: [runtimeAction],
  runtimePermissionMode: 'confirm_important',
  runAllText: '运行计划',
  graphTitle: '多智能体执行流程',
  graphHint: '等待生成执行计划',
  isExecuting: false,
  requiresApproval: false,
  onRunAll: vi.fn(),
  onApproveAndRun: vi.fn(),
  onCancel: vi.fn(),
  onRetryFailed: vi.fn(),
};

describe('GraphPanel runtime action card', () => {
  it('shows action details and prevents duplicate decisions while submitting', async () => {
    let resolveDecision: (value: boolean) => void = () => undefined;
    const onRuntimeActionDecision = vi.fn(() => new Promise<boolean>((resolve) => {
      resolveDecision = resolve;
    }));

    render(<GraphPanel {...baseProps} onRuntimeActionDecision={onRuntimeActionDecision} />);

    expect(screen.getByText('安装论文依赖')).toBeInTheDocument();
    expect(screen.getByText(/依赖 mock==2\.0\.0/)).toBeInTheDocument();

    const approve = screen.getByRole('button', { name: '允许一次' });
    const reject = screen.getByRole('button', { name: '拒绝' });
    fireEvent.click(approve);
    expect(onRuntimeActionDecision).toHaveBeenCalledWith('ra-test', 'approve');
    expect(approve).toBeDisabled();
    expect(reject).toBeDisabled();

    fireEvent.click(reject);
    expect(onRuntimeActionDecision).toHaveBeenCalledTimes(1);

    resolveDecision(true);
    await waitFor(() => expect(approve).not.toBeDisabled());
  });
});
