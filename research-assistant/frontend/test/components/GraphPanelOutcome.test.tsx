import { describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
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
        outcomeReason="核心目标已通过验收，可选指标计算未完成"
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
    expect(details).toHaveTextContent('部分完成');
    expect(details).toHaveTextContent('核心目标已通过验收，可选指标计算未完成');
    expect(details).toHaveTextContent('已通过的核心目标');
    expect(details).toHaveTextContent('核心实验复现成功');
    expect(details).toHaveTextContent('证据：report');
    expect(within(details).getByText('可选指标（失败：metric dataset missing）')).toBeInTheDocument();
    expect(details).toHaveTextContent('缺少可选工作 可选指标，相关结论不能宣称完整');
    expect(details.textContent?.split('可选指标（失败：metric dataset missing）')).toHaveLength(2);
    expect(screen.getAllByText(/部分完成/).length).toBeGreaterThan(0);
  });

  it('renders a blocked outcome as missing conditions', () => {
    render(
      <GraphPanel {...baseProps} planId="plan-blocked" planStatus="failed" outcome="blocked" outcomeReason="缺少训练数据集" />,
    );
    const details = screen.getByTestId('plan-outcome-details');
    expect(details.getAttribute('data-outcome')).toBe('blocked');
    expect(details.textContent).toContain('缺少条件');
    expect(details).toHaveTextContent('缺少训练数据集');
    expect(details).toHaveTextContent('该计划未提供结构化验收详情');
  });

  it('keeps an unverified goal and missing acceptance artifact under needs_review', () => {
    render(
      <GraphPanel {...baseProps} planId="review-plan" planStatus="completed" outcome="needs_review"
        outcomeReason="核心目标缺少可信证据"
        outcomeDetails={{
          accepted_goals: [{ id: 'g', statement: '待验证目标', verified: false, evidence_refs: ['report'], reason: 'report 来自失败任务' }],
          missing_artifacts: ['report'],
        }} />,
    );
    const details = screen.getByTestId('plan-outcome-details');
    expect(details).toHaveAttribute('data-outcome', 'needs_review');
    expect(details).toHaveTextContent('已结束·待复核');
    expect(details).toHaveTextContent('尚未通过验证的核心目标');
    expect(details).toHaveTextContent('待验证目标');
    expect(details).toHaveTextContent('report 来自失败任务');
    expect(details).toHaveTextContent('缺少或不可信的验收产物');
    expect(within(details).getByText('report')).toBeInTheDocument();
    expect(within(details).queryByText('已通过的核心目标')).not.toBeInTheDocument();
    expect(within(details).queryByText('部分完成')).not.toBeInTheDocument();
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

  it.each([
    ['completed', 'completed', '完成'],
    ['failed', 'failed', '失败'],
    ['canceled', null, '已取消'],
    ['completed', 'smoke', '冒烟'],
    ['completed', 'mock', '模拟'],
  ])('preserves %s/%s labels without adding an outcome explanation banner', (planStatus, outcome, label) => {
    render(<GraphPanel {...baseProps} planId="plan-other-result" planStatus={planStatus} outcome={outcome}
      outcomeDetails={{ optional_losses: [{ task_id: 'loss', status: 'failed', reason: '不应改变结果语义' }] }} />);
    expect(screen.queryByTestId('plan-outcome-details')).not.toBeInTheDocument();
    expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    expect(screen.queryByText('部分完成')).not.toBeInTheDocument();
    expect(screen.queryByText(/不应改变结果语义/)).not.toBeInTheDocument();
  });

  it.each(['pending', 'ready', 'in_progress', 'awaiting_approval', 'paused'])('does not show terminal explanation for a %s plan', (planStatus) => {
    render(<GraphPanel {...baseProps} planId="plan-running" planStatus={planStatus} outcome="partial"
      outcomeDetails={{ limitations: ['不应提前显示的旧限制'] }} />);
    expect(screen.queryByTestId('plan-outcome-details')).not.toBeInTheDocument();
    expect(screen.queryByText('不应提前显示的旧限制')).not.toBeInTheDocument();
  });

  it('removes the previous explanation when the plan returns to a nonterminal state', () => {
    const details = { limitations: ['上轮限制说明'] };
    const { rerender } = render(<GraphPanel {...baseProps} planId="plan-rerun" planStatus="completed" outcome="partial" outcomeDetails={details} />);
    expect(screen.getByText('上轮限制说明')).toBeInTheDocument();
    rerender(<GraphPanel {...baseProps} planId="plan-rerun" planStatus="in_progress" outcome="partial" outcomeDetails={details} />);
    expect(screen.queryByTestId('plan-outcome-details')).not.toBeInTheDocument();
    expect(screen.queryByText('上轮限制说明')).not.toBeInTheDocument();
  });
});
