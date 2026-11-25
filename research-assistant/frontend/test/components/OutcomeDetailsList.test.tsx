import { describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import type { OutcomeDetails } from '../../src/contracts/api';
import { OutcomeDetailsList } from '../../src/features/shared/PlanOutcomeBits';

const fallbackText = '该计划未提供结构化验收详情';

const group = (title: string) => within(screen.getByRole('region', { name: title }));

describe('OutcomeDetailsList', () => {
  it('separates verified and unverified goals and preserves their evidence and reasons', () => {
    render(
      <OutcomeDetailsList
        outcome="needs_review"
        outcomeReason="执行已经结束，但扩展结论尚待复核"
        details={{
          accepted_goals: [
            { id: 'accepted', statement: '核心实验已复现', verified: true, evidence_refs: ['core-report', 'metrics'] },
            { id: 'pending', statement: '扩展结论尚未证实', verified: false, evidence_refs: ['extension-report'], reason: '证据来自失败的任务' },
          ],
          missing_artifacts: ['extension-report'],
        }}
      />,
    );

    expect(screen.getByText('已结束·待复核')).toBeInTheDocument();
    expect(screen.getByText('执行已经结束，但扩展结论尚待复核')).toBeInTheDocument();
    const accepted = group('已通过的核心目标');
    expect(accepted.getByText(/核心实验已复现/)).toHaveTextContent('core-report、metrics');
    expect(accepted.queryByText(/扩展结论尚未证实/)).not.toBeInTheDocument();
    const unverified = group('尚未通过验证的核心目标');
    expect(unverified.getByText(/扩展结论尚未证实/)).toHaveTextContent('证据来自失败的任务');
    expect(unverified.getByText(/扩展结论尚未证实/)).toHaveTextContent('extension-report');
    expect(group('缺少或不可信的验收产物').getByText('extension-report')).toBeInTheDocument();
    expect(screen.queryByText(fallbackText)).not.toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('does not invent evidence for a goal that has no evidence references', () => {
    render(
      <OutcomeDetailsList
        outcome="partial"
        details={{ accepted_goals: [{ id: 'g', statement: '目标已验证', verified: true }] }}
      />,
    );
    expect(group('已通过的核心目标').getByText('目标已验证')).toBeInTheDocument();
    expect(screen.queryByText(/证据：/)).not.toBeInTheDocument();
    expect(screen.queryByText('尚未通过验证的核心目标')).not.toBeInTheDocument();
  });

  it('shows the actual optional loss statuses and falls back from an empty name to the task ID', () => {
    render(
      <OutcomeDetailsList
        outcome="partial"
        details={{
          optional_losses: [
            { task_id: 'metric', name: '  可选指标  ', status: 'failed', reason: 'metric dataset missing' },
            { task_id: 'plot-task', name: '  ', status: 'blocked', reason: '绘图输入缺失' },
            { task_id: 'canceled-task', status: 'canceled' },
            { task_id: 'skipped-task', status: 'skipped' },
            { task_id: 'future-task', status: 'future_status', reason: '原始状态说明' },
          ],
        }}
      />,
    );
    const losses = group('未完成的可选工作');
    expect(losses.getByText('可选指标（失败：metric dataset missing）')).toBeInTheDocument();
    expect(losses.getByText('plot-task（被阻断：绘图输入缺失）')).toBeInTheDocument();
    expect(losses.getByText('canceled-task（已取消）')).toBeInTheDocument();
    expect(losses.getByText('skipped-task（已跳过）')).toBeInTheDocument();
    expect(losses.getByText('future-task（future_status：原始状态说明）')).toBeInTheDocument();
  });

  it('renders each generated loss once while keeping the backend reason unchanged', () => {
    const loss = '可选指标（失败：dataset unavailable）';
    render(
      <OutcomeDetailsList
        outcome="partial"
        outcomeReason="服务端原文：可选指标未完成"
        details={{
          optional_losses: [{ task_id: 'metric', name: '可选指标', status: 'failed', reason: 'dataset unavailable' }],
          missing_artifacts: ['missing-report'],
          limitations: ['不能宣称覆盖全部指标'],
        }}
      />,
    );
    const details = screen.getByTestId('outcome-details');
    expect(details.textContent?.split(loss)).toHaveLength(2);
    expect(screen.getByText('服务端原文：可选指标未完成')).toBeInTheDocument();
    expect(group('缺少或不可信的验收产物').getByText('missing-report')).toBeInTheDocument();
    expect(group('这些缺失限制的结论').getByText('不能宣称覆盖全部指标')).toBeInTheDocument();
  });

  it('preserves different tasks with the same loss text without duplicate-key warnings', () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      render(
        <OutcomeDetailsList
          outcome="partial"
          details={{
            optional_losses: [
              { task_id: 'metric-a', name: '额外指标', status: 'failed', reason: '同一数据源缺失' },
              { task_id: 'metric-b', name: '额外指标', status: 'failed', reason: '同一数据源缺失' },
            ],
            limitations: ['适用范围有限', '适用范围有限'],
          }}
        />,
      );
      expect(screen.getByTestId('optional-losses').querySelectorAll('li')).toHaveLength(2);
      const keyWarnings = consoleError.mock.calls.filter((call) => /same key|unique.*key/i.test(call.map(String).join(' ')));
      expect(keyWarnings).toHaveLength(0);
    } finally {
      consoleError.mockRestore();
    }
  });

  it.each([
    ['undefined', undefined],
    ['null', null],
    ['empty object', {}],
    ['empty arrays', { accepted_goals: [], optional_losses: [], missing_artifacts: [], limitations: [] }],
    ['mode only', { mode: 'acceptance' }],
    ['claim counters only', { claims_verified: 2, claims_total: 3 }],
  ] satisfies Array<[string, OutcomeDetails | null | undefined]>)('explains missing displayable details for %s', (_name, details) => {
    render(<OutcomeDetailsList outcome="partial" outcomeReason="旧计划仅保留了结果原因" details={details} />);
    expect(screen.getByText('部分完成')).toBeInTheDocument();
    expect(screen.getByText('旧计划仅保留了结果原因')).toBeInTheDocument();
    expect(screen.getByText(fallbackText)).toBeInTheDocument();
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
    expect(screen.queryByText('已通过的核心目标')).not.toBeInTheDocument();
  });

  it('keeps the outcome visible when both the reason and details are missing', () => {
    render(<OutcomeDetailsList outcome="blocked" />);
    expect(screen.getByText('缺少条件')).toBeInTheDocument();
    expect(screen.getByText(fallbackText)).toBeInTheDocument();
  });

  it('clears all previous groups when details are removed', () => {
    const { rerender } = render(
      <OutcomeDetailsList outcome="partial" details={{
        accepted_goals: [{ id: 'g', statement: '旧目标', verified: true, evidence_refs: ['old-evidence'] }],
        optional_losses: [{ task_id: 'old-task', status: 'failed', reason: '旧失败原因' }],
        missing_artifacts: ['old-artifact'],
        limitations: ['旧限制'],
      }} />,
    );
    expect(screen.getByText(/旧目标/)).toBeInTheDocument();
    rerender(<OutcomeDetailsList outcome="needs_review" outcomeReason="当前原因" details={null} />);
    expect(screen.getByText('已结束·待复核')).toBeInTheDocument();
    expect(screen.getByText('当前原因')).toBeInTheDocument();
    expect(screen.getByText(fallbackText)).toBeInTheDocument();
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
    expect(screen.getByTestId('outcome-details')).not.toHaveTextContent(/旧目标|old-evidence|old-task|old-artifact|旧限制/);
  });

  it('renders server text as text rather than HTML', () => {
    const serverText = '<img src=x onerror=alert(1)>';
    const { container } = render(
      <OutcomeDetailsList outcome="needs_review" outcomeReason={serverText} details={{
        accepted_goals: [{ id: 'g', statement: '<script>goal</script>', verified: false, reason: serverText, evidence_refs: ['<a href="x">report</a>'] }],
      }} />,
    );
    expect(screen.getByTestId('outcome-details')).toHaveTextContent(serverText);
    expect(container.querySelector('img, script, a')).toBeNull();
  });
});
