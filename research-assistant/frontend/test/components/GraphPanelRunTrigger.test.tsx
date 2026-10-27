import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { GraphPanel } from '../../src/features/plan-graph/GraphPanel';
import { readPlanRunTrigger } from '../../src/app/viewModels/useGraphExecutionViewModel';
import type { PlanGraph } from '../../src/contracts/api';

const baseProps = {
  nodes: [],
  edges: [],
  onNodesChange: vi.fn(),
  onEdgesChange: vi.fn(),
  onNodeClick: vi.fn(),
  intentContext: null,
  runtimeActions: [],
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

const openColophon = () => {
  fireEvent.click(screen.getByRole('button', { name: '版本记录' }));
  return screen.getByTestId('plan-run-trigger');
};

describe('readPlanRunTrigger', () => {
  it('reads the backend run_trigger defensively', () => {
    const planWithTrigger = (runTrigger?: string) => ({ run_trigger: runTrigger } as unknown as PlanGraph);
    expect(readPlanRunTrigger(planWithTrigger('auto'))).toBe('auto');
    expect(readPlanRunTrigger(planWithTrigger('manual'))).toBe('manual');
    expect(readPlanRunTrigger(planWithTrigger(undefined))).toBe('');
    expect(readPlanRunTrigger(planWithTrigger('something-else'))).toBe('');
    expect(readPlanRunTrigger(null)).toBe('');
  });
});

describe('GraphPanel run trigger', () => {
  it.each([
    ['manual', '手动运行'],
    ['auto', '自动运行'],
    ['', '未运行'],
  ] as const)('shows %s as %s in the colophon', (runTrigger, label) => {
    render(<GraphPanel {...baseProps} runTrigger={runTrigger} />);
    expect(openColophon()).toHaveTextContent(label);
  });

  it('falls back to 未运行 when the plan has no trigger yet', () => {
    render(<GraphPanel {...baseProps} />);
    expect(openColophon()).toHaveTextContent('未运行');
  });
});
