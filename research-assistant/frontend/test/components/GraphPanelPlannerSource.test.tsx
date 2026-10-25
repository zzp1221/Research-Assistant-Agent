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

describe('GraphPanel planner source badge', () => {
  it('shows the template-planning badge with its fallback reason', () => {
    render(
      <GraphPanel
        {...baseProps}
        plannerSource="template"
        plannerFallbackReason="execute_code node is missing generated code input"
      />,
    );
    const badge = screen.getByText('模板规划');
    expect(badge).toBeInTheDocument();
    expect(badge.getAttribute('title')).toContain('missing generated code input');
  });

  it('does not show the badge for agent-planned graphs', () => {
    render(<GraphPanel {...baseProps} plannerSource="agent" />);
    expect(screen.queryByText('模板规划')).not.toBeInTheDocument();
  });
});
