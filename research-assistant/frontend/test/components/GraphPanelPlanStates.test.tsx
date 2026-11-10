import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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

describe('GraphPanel business status labels', () => {
  it.each([
    ['awaiting_approval', '待批准'],
    ['awaiting_input', '待回答'],
    ['in_progress', '运行中'],
    ['completed', '已完成'],
    ['failed', '失败'],
    ['canceled', '已取消'],
    ['blocked', '已阻塞'],
    ['pending', '未开始'],
  ])('labels %s as %s instead of 待运行', (planStatus, label) => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus={planStatus} />);
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent(label);
  });

  it('does not label an unmapped status as 待运行', () => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus="quarantined_by_reviewer" />);
    const pill = screen.getByTestId('plan-state-pill');
    expect(pill).toHaveTextContent('未知状态');
    expect(pill).not.toHaveTextContent('待运行');
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/无法确认计划状态/);
  });

  it('shows 已暂停 for a paused plan even when the status looks runnable', () => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus="in_progress" planPaused />);
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('已暂停');
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/计划已暂停/);
  });

  it('prefers the 待运行 label only for a plan that still has pending nodes', () => {
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="ready"
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'ready', task: { ID: 't1' } } }]}
      />,
    );
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('待运行');
    expect(screen.getByTestId('plan-run')).toHaveTextContent('运行计划');
  });
});

describe('GraphPanel primary action gating', () => {
  it('hides the run action while the server reports in_progress and shows cancel', () => {
    const onRunAll = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        isExecuting={false}
        onRunAll={onRunAll}
        onCancel={onCancel}
      />,
    );

    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /运行计划/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });

  it('keeps the run action for a refresh-discovered running plan visible as cancel only', () => {
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        connectionState="live"
        canRun={false}
      />,
    );
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-connection-pill')).toHaveTextContent('实时更新');
  });

  it.each(['awaiting_input', 'awaiting_approval', 'awaiting_review', 'blocked', 'mystery_state'])(
    'does not offer a plain execute action for %s',
    (planStatus) => {
      const onRunAll = vi.fn();
      render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus={planStatus} onRunAll={onRunAll} />);
      expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /运行计划/ })).not.toBeInTheDocument();
      expect(onRunAll).not.toHaveBeenCalled();
    },
  );

  it('routes awaiting_input to the question entry', () => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus="awaiting_input" />);
    expect(screen.getByTestId('plan-answer-question')).toHaveTextContent('回答问题');
  });

  it('keeps the dedicated approval entry when awaiting_approval', () => {
    const onApproveAndRun = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        onApproveAndRun={onApproveAndRun}
      />,
    );
    const approve = screen.getByTestId('plan-approve-and-run');
    expect(approve).toHaveTextContent('审批并运行');
    fireEvent.click(approve);
    expect(onApproveAndRun).toHaveBeenCalledTimes(1);
  });

  it('keeps pause authoritative: no run and no plan-level approval while paused', () => {
    const onApproveAndRun = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        planPaused
        onApproveAndRun={onApproveAndRun}
      />,
    );
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    // Pause is an independent flag: the plan is not observable as "awaiting
    // approval" work here, canceling stays the primary action.
    expect(screen.queryByTestId('plan-approve-and-run')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('已暂停');
  });

  it('does not duplicate the plan approval when a runtime action is pending', () => {
    const onApproveAndRun = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        requiresApproval={false}
        onApproveAndRun={onApproveAndRun}
        runtimeActions={[{
          id: 'ra-1',
          plan_id: 'plan-0123456789',
          task_id: 'task-1',
          node_type: 'install_dependencies',
          kind: 'dependency_install',
          title: '安装依赖',
          status: 'pending',
        }]}
      />,
    );
    const entry = screen.getByTestId('plan-open-approval');
    expect(entry).toHaveTextContent('处理审批');
    fireEvent.click(entry);
    expect(onApproveAndRun).not.toHaveBeenCalled();
    expect(screen.getByText('安装依赖')).toBeInTheDocument();
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/运行授权/);
  });

  it('keeps 取消 but no execute for a paused plan', () => {
    const onRunAll = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        planPaused
        onRunAll={onRunAll}
        onCancel={onCancel}
      />,
    );
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });

  it('offers no resume entry for a paused plan without a resume action', () => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus="in_progress" planPaused />);
    expect(screen.queryByTestId('plan-resume')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
  });

  it('keeps an explicit retry entry for a failed terminal plan', () => {
    const onRetryFailed = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="failed"
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'failed', task: { ID: 't1' } } }]}
        onRetryFailed={onRetryFailed}
      />,
    );
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('失败');
    fireEvent.click(screen.getByTestId('plan-retry-failed'));
    expect(onRetryFailed).toHaveBeenCalledTimes(1);
  });

  it('offers 审阅后重新运行 instead of a dead retry for a failed plan without failed nodes', () => {
    const onRetryFailed = vi.fn();
    const onOpenReview = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="failed"
        onRetryFailed={onRetryFailed}
        onOpenReview={onOpenReview}
      />,
    );
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    // Without failed nodes the runtime returns immediately, so the retry entry
    // would be dead; the review entry is the working alternative.
    expect(screen.queryByTestId('plan-retry-failed')).not.toBeInTheDocument();
    const reviewAndRerun = screen.getByTestId('plan-review-and-rerun');
    expect(reviewAndRerun).toHaveTextContent('审阅后重新运行');
    fireEvent.click(reviewAndRerun);
    expect(onOpenReview).toHaveBeenCalledTimes(1);
    expect(onRetryFailed).not.toHaveBeenCalled();
  });

  it('falls back to the results entry for a failed plan without failed nodes or review', () => {
    const onRetryFailed = vi.fn();
    const onNodeClick = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="failed"
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'completed', task: { ID: 't1' } } }]}
        onRetryFailed={onRetryFailed}
        onNodeClick={onNodeClick}
      />,
    );
    expect(screen.queryByTestId('plan-retry-failed')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-view-result'));
    expect(onNodeClick).toHaveBeenCalledTimes(1);
    expect(onRetryFailed).not.toHaveBeenCalled();
  });

  it('offers only a result/retry entry after a completed plan', () => {
    const onNodeClick = vi.fn();
    const onRetryFailed = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="completed"
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'completed', task: { ID: 't1' } } }]}
        onNodeClick={onNodeClick}
        onRetryFailed={onRetryFailed}
      />,
    );
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    const viewResult = screen.getByTestId('plan-view-result');
    expect(viewResult).toHaveTextContent('查看结果');
    fireEvent.click(viewResult);
    expect(onNodeClick).toHaveBeenCalledTimes(1);
    expect(onRetryFailed).not.toHaveBeenCalled();
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/本次运行已结束/);
  });

  it('shows no execute action for a terminal plan without nodes', () => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus="canceled" />);
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('已取消');
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.queryByTestId('plan-view-result')).not.toBeInTheDocument();
    expect(screen.queryByTestId('plan-retry-failed')).not.toBeInTheDocument();
  });

  it('disables the run action after an accepted start (202)', () => {
    const onRunAll = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="pending"
        startAccepted
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'pending', task: { ID: 't1' } } }]}
        onRunAll={onRunAll}
      />,
    );
    const run = screen.getByTestId('plan-run');
    expect(run).toHaveTextContent('启动已受理');
    expect(run).toBeDisabled();
    fireEvent.click(run);
    expect(onRunAll).not.toHaveBeenCalled();
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/正在确认启动，请稍候/);
    expect(screen.getByTestId('plan-start-accepted')).toHaveTextContent('启动已受理');
  });

  it('keeps an accepted start as 受理 + cancel only, never back to a plain run', () => {
    const onRunAll = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="pending"
        startAccepted
        isExecuting
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'pending', task: { ID: 't1' } } }]}
        onRunAll={onRunAll}
        onCancel={onCancel}
      />,
    );

    // Still the 受理 entry, disabled: the request is never submitted twice.
    const run = screen.getByTestId('plan-run');
    expect(run).toHaveTextContent('启动已受理');
    expect(run).not.toHaveTextContent('运行计划');
    expect(run).toBeDisabled();

    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });

  it('falls back to a disabled run action when the view model says it cannot run', () => {
    const onRunAll = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="pending"
        canRun={false}
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'pending', task: { ID: 't1' } } }]}
        onRunAll={onRunAll}
      />,
    );
    const run = screen.getByTestId('plan-run');
    expect(run).toBeDisabled();
    fireEvent.click(run);
    expect(onRunAll).not.toHaveBeenCalled();
  });
});

describe('GraphPanel waiting gates while the run is observed', () => {
  // The runtime reports `isExecuting` for a plan it observes, which includes
  // awaiting_approval / awaiting_input: those plans are started but still
  // waiting on the user, so the waiting entry must win over 取消计划.
  it('keeps 审批并运行 (not cancel) for awaiting_approval while isExecuting', () => {
    const onApproveAndRun = vi.fn();
    const onRunAll = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        isExecuting
        onApproveAndRun={onApproveAndRun}
        onRunAll={onRunAll}
        onCancel={onCancel}
      />,
    );

    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    const approve = screen.getByTestId('plan-approve-and-run');
    expect(approve).toHaveTextContent('审批并运行');
    fireEvent.click(approve);
    expect(onApproveAndRun).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
    // Cancel stays available as an explicit secondary control.
    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });

  it('keeps 处理审批 with a pending runtime action for awaiting_approval while isExecuting', () => {
    const onApproveAndRun = vi.fn();
    const onRunAll = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        isExecuting
        requiresApproval={false}
        onApproveAndRun={onApproveAndRun}
        onRunAll={onRunAll}
        runtimeActions={[{
          id: 'ra-1',
          plan_id: 'plan-0123456789',
          task_id: 'task-1',
          node_type: 'install_dependencies',
          kind: 'dependency_install',
          title: '安装依赖',
          status: 'pending',
        }]}
      />,
    );

    expect(screen.queryByTestId('plan-approve-and-run')).not.toBeInTheDocument();
    const entry = screen.getByTestId('plan-open-approval');
    expect(entry).toHaveTextContent('处理审批');
    fireEvent.click(entry);
    expect(screen.getByText('安装依赖')).toBeInTheDocument();
    expect(onApproveAndRun).not.toHaveBeenCalled();
    expect(onRunAll).not.toHaveBeenCalled();
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
  });

  it('keeps 回答问题 (not cancel) for awaiting_input while isExecuting', () => {
    const onRunAll = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_input"
        isExecuting
        questions={[{
          id: 'q-1',
          plan_id: 'plan-0123456789',
          task_id: 'task-1',
          header: '数据集版本',
          prompt: '请选择要复现的配置',
          status: 'pending',
        }]}
        onAnswerQuestion={vi.fn()}
        onRunAll={onRunAll}
        onCancel={onCancel}
      />,
    );

    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    const answer = screen.getByTestId('plan-answer-question');
    expect(answer).toHaveTextContent('回答问题');
    fireEvent.click(answer);
    expect(screen.getByText('请选择要复现的配置')).toBeInTheDocument();
    expect(onRunAll).not.toHaveBeenCalled();
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/先回答问题，计划会继续/);

    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });

  it('keeps pause authoritative while isExecuting: no approval entry, cancel only', () => {
    const onApproveAndRun = vi.fn();
    const onRunAll = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        isExecuting
        planPaused
        onApproveAndRun={onApproveAndRun}
        onRunAll={onRunAll}
        onCancel={onCancel}
      />,
    );

    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.queryByTestId('plan-approve-and-run')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onApproveAndRun).not.toHaveBeenCalled();
    expect(onRunAll).not.toHaveBeenCalled();
  });

  it('keeps the waiting entry and cancel when the progress feed was interrupted', () => {
    const onApproveAndRun = vi.fn();
    const onRunAll = vi.fn();
    const onReconnect = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        isExecuting
        connectionState="interrupted"
        onApproveAndRun={onApproveAndRun}
        onReconnect={onReconnect}
        onRunAll={onRunAll}
      />,
    );

    expect(screen.getByTestId('plan-approve-and-run')).toHaveTextContent('审批并运行');
    expect(screen.getByTestId('plan-reconnect')).toBeInTheDocument();
    expect(screen.getByTestId('plan-business-help')).toHaveTextContent(/进度更新中断，可重新连接；任务仍在运行/);
    fireEvent.click(screen.getByTestId('plan-reconnect'));
    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });
});

describe('GraphPanel paused plan resume control', () => {
  it('resumes a started, executing paused plan exactly once and never runs or approves', () => {
    const onResume = vi.fn();
    const onRunAll = vi.fn();
    const onApproveAndRun = vi.fn();
    const onRetryFailed = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        planPaused
        isExecuting
        onResume={onResume}
        onRunAll={onRunAll}
        onApproveAndRun={onApproveAndRun}
        onRetryFailed={onRetryFailed}
        onCancel={onCancel}
      />,
    );

    // Pause outranks the local execution flag: no run entry at all.
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /运行计划/ })).not.toBeInTheDocument();
    const resume = screen.getByTestId('plan-resume');
    expect(resume).toHaveTextContent('恢复计划');
    expect(resume).toBeEnabled();

    fireEvent.click(resume);

    expect(onResume).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
    expect(onApproveAndRun).not.toHaveBeenCalled();
    expect(onRetryFailed).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it('keeps cancel as the secondary control next to resume', () => {
    const onResume = vi.fn();
    const onCancel = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        planPaused
        isExecuting
        onResume={onResume}
        onCancel={onCancel}
      />,
    );

    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onResume).not.toHaveBeenCalled();
  });

  it('keeps resume ahead of the approval gate and the runtime action entry', () => {
    const onResume = vi.fn();
    const onApproveAndRun = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_approval"
        planPaused
        isExecuting
        onResume={onResume}
        onApproveAndRun={onApproveAndRun}
        runtimeActions={[{
          id: 'ra-1',
          plan_id: 'plan-0123456789',
          task_id: 'task-1',
          node_type: 'install_dependencies',
          kind: 'dependency_install',
          title: '安装依赖',
          status: 'pending',
        }]}
      />,
    );

    expect(screen.queryByTestId('plan-approve-and-run')).not.toBeInTheDocument();
    expect(screen.queryByTestId('plan-open-approval')).not.toBeInTheDocument();
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('已暂停');
    // The approval entry stays reachable through the pending-work list only.
    expect(screen.getByText('待办 1')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('plan-resume'));
    expect(onResume).toHaveBeenCalledTimes(1);
    expect(onApproveAndRun).not.toHaveBeenCalled();
  });

  it('keeps resume ahead of the review gate', () => {
    const onResume = vi.fn();
    const onOpenReview = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="awaiting_review"
        planPaused
        requiresReview
        onResume={onResume}
        onOpenReview={onOpenReview}
      />,
    );

    expect(screen.queryByTestId('plan-open-review')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-resume'));
    expect(onResume).toHaveBeenCalledTimes(1);
    expect(onOpenReview).not.toHaveBeenCalled();
  });

  it('keeps resume available when the progress feed was interrupted', () => {
    const onResume = vi.fn();
    const onReconnect = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        planPaused
        isExecuting
        connectionState="interrupted"
        onResume={onResume}
        onReconnect={onReconnect}
      />,
    );

    expect(screen.getByTestId('plan-resume')).toHaveTextContent('恢复计划');
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
    expect(screen.getByTestId('plan-reconnect')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-resume'));
    expect(onResume).toHaveBeenCalledTimes(1);
    expect(onReconnect).not.toHaveBeenCalled();
  });

  it('never shows 恢复计划 for a plan that is not paused', () => {
    const onResume = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        isExecuting
        onResume={onResume}
      />,
    );

    expect(screen.queryByTestId('plan-resume')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
    expect(onResume).not.toHaveBeenCalled();
  });
});

describe('GraphPanel legacy prop compatibility', () => {
  it('keeps the plain run action for callers that pass no observation props', () => {
    const onRunAll = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        nodes={[{ id: 't1', position: { x: 0, y: 0 }, data: { status: 'pending', task: { ID: 't1' } } }]}
        onRunAll={onRunAll}
      />,
    );
    const run = screen.getByTestId('plan-run');
    expect(run).toHaveTextContent('运行计划');
    expect(run).toBeEnabled();
    fireEvent.click(run);
    expect(onRunAll).toHaveBeenCalledTimes(1);
  });

  it('shows cancel when only isExecuting is set (no planStatus)', () => {
    const onCancel = vi.fn();
    render(<GraphPanel {...baseProps} isExecuting onCancel={onCancel} />);
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('keeps the review entry for requiresReview without a plan status', () => {
    const onOpenReview = vi.fn();
    render(<GraphPanel {...baseProps} requiresReview onOpenReview={onOpenReview} />);
    fireEvent.click(screen.getByTestId('plan-open-review'));
    expect(onOpenReview).toHaveBeenCalledTimes(1);
  });
});

describe('GraphPanel connection state', () => {
  it.each([
    ['connecting', '连接中'],
    ['live', '实时更新'],
    ['reconnecting', '正在重连'],
    ['interrupted', '更新已中断'],
  ] as const)('renders the %s connection state as %s', (connectionState, label) => {
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        connectionState={connectionState}
      />,
    );
    expect(screen.getByTestId('plan-connection-pill')).toHaveTextContent(label);
  });

  it('never hides the business status behind the connection state', () => {
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        connectionState="interrupted"
      />,
    );
    // A dropped stream must not turn the run into a failure or reopen 运行.
    expect(screen.getByTestId('plan-state-pill')).toHaveTextContent('运行中');
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-connection-pill')).toHaveTextContent('更新已中断');
  });

  it('offers an explicit 重新连接 button that never executes', () => {
    const onReconnect = vi.fn();
    const onRunAll = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="in_progress"
        connectionState="interrupted"
        connectionDetail="重连次数已用尽"
        onReconnect={onReconnect}
        onRunAll={onRunAll}
      />,
    );

    const reconnect = screen.getByTestId('plan-reconnect');
    expect(reconnect).toHaveTextContent('重新连接');
    fireEvent.click(reconnect);

    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
    expect(screen.getByTestId('plan-connection-detail')).toHaveTextContent(/重连次数已用尽/);
  });

  it('disables 重新连接 when the caller has no reconnect action', () => {
    render(
      <GraphPanel {...baseProps} planId="plan-0123456789" planStatus="in_progress" connectionState="interrupted" />,
    );
    expect(screen.getByTestId('plan-reconnect')).toBeDisabled();
  });

  it('does not show a connection pill without a connection state (legacy props)', () => {
    render(<GraphPanel {...baseProps} planId="plan-0123456789" planStatus="in_progress" />);
    expect(screen.queryByTestId('plan-connection-pill')).not.toBeInTheDocument();
    expect(screen.getByTestId('plan-cancel')).toBeInTheDocument();
  });
});

describe('GraphPanel unknown status refresh entry', () => {  it('offers a refresh (not run) entry for an unmapped status', () => {
    const onReconnect = vi.fn();
    const onRunAll = vi.fn();
    render(
      <GraphPanel
        {...baseProps}
        planId="plan-0123456789"
        planStatus="mystery_state"
        onReconnect={onReconnect}
        onRunAll={onRunAll}
      />,
    );
    expect(screen.queryByTestId('plan-run')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-refresh-state'));
    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(onRunAll).not.toHaveBeenCalled();
  });
});
