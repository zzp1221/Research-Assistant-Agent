import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { GraphPanel } from '../../src/features/plan-graph/GraphPanel';

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

describe('GraphPanel outcome surface', () => {
  it('explains a partial outcome, its accepted goals and its losses', () => {
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-partial"
        planStatus="completed"
        outcome="partial"
        outcomeDetails={{
          mode: 'acceptance',
          accepted_goals: [{ id: 'g1', statement: '核心实验复现成功', verified: true, evidence_refs: ['report'] }],
          optional_losses: [{ task_id: 'b', name: '可选指标', status: 'failed', reason: 'metric dataset missing' }],
          limitations: ['缺少可选工作 可选指标，相关结论不能宣称完整'],
        }}
      />,
    );
    const details = screen.getByTestId('plan-outcome-details');
    expect(details.getAttribute('data-outcome')).toBe('partial');
    // The canvas must not present a partial run as a plain success.
    expect(details.textContent).toContain('部分完成');
    expect(details.textContent).toContain('可选指标');
    expect(screen.getAllByText(/部分完成/).length).toBeGreaterThan(0);
  });

  it('renders a blocked outcome as missing conditions', () => {
    render(
      <GraphPanel {...baseProps} planId="plan-blocked" planStatus="failed" outcome="blocked" />,
    );
    const details = screen.getByTestId('plan-outcome-details');
    expect(details.getAttribute('data-outcome')).toBe('blocked');
    expect(details.textContent).toContain('缺少条件');
  });

  it('shows the blocked node count in the meta row', () => {
    render(
      <GraphPanel
        {...baseProps}
        nodes={[
          { id: 'a', position: { x: 0, y: 0 }, data: { status: 'blocked', task: null } as never },
          { id: 'b', position: { x: 0, y: 0 }, data: { status: 'completed', task: null } as never },
        ]}
        planId="plan-blocked-nodes"
        planStatus="failed"
        outcome="blocked"
      />,
    );
    expect(screen.getByTestId('plan-blocked-count').textContent).toContain('1');
  });

  it('does not render the outcome banner for a plain success', () => {
    render(<GraphPanel {...baseProps} planId="plan-ok" planStatus="completed" outcome="completed" />);
    expect(screen.queryByTestId('plan-outcome-details')).not.toBeInTheDocument();
  });
});
