import { describe, expect, it } from 'vitest';
import { describeBlockedNode, planOutcomeLabel, summarizePlanOutcome } from '../../src/features/shared/planOutcome';

describe('planOutcome helpers', () => {
  it('labels every outcome, never leaving a terminal plan blank', () => {
    for (const [outcome, label] of Object.entries({
      completed: '全部完成', partial: '部分完成', smoke: '冒烟', mock: '模拟',
      blocked: '缺少条件', failed: '失败', needs_review: '已结束·待复核',
    })) {
      expect(planOutcomeLabel(outcome)).toBe(label);
    }
    expect(planOutcomeLabel('partial')).toBe('部分完成');
    // A finished run whose evidence is pending is not the same as a failure.
    expect(planOutcomeLabel('needs_review')).toContain('待复核');
    expect(planOutcomeLabel('needs_review')).toContain('已结束');
    expect(planOutcomeLabel('future_outcome')).toBe('future_outcome');
    expect(planOutcomeLabel(null)).toBe('');
    expect(planOutcomeLabel(undefined)).toBe('');
  });

  it('explains a blocked node from its structured reason', () => {
    const text = describeBlockedNode(
      {
        code: 'upstream_failed',
        message: '因任务 核心实验 上游执行失败而阻断',
        occurred_at: '2026-11-11T00:00:00Z',
        causes: [
          { code: 'upstream_failed', root_task_id: 'a', via_task_id: 'a', status: 'failed' },
          { code: 'upstream_failed', root_task_id: 'b', via_task_id: 'b', status: 'failed' },
        ],
      },
      null,
    );
    expect(text).toContain('上游执行失败');
    expect(text).toContain('因任务 核心实验 上游执行失败而阻断');
  });

  it('falls back to the plain error for legacy snapshots', () => {
    expect(describeBlockedNode(undefined, 'upstream failed')).toBe('upstream failed');
    expect(describeBlockedNode(undefined, undefined)).toBe('');
  });

  it('never reports a partial plan as fully successful', () => {
    const summary = summarizePlanOutcome({
      status: 'completed',
      outcome: 'partial',
      outcome_reason: 'core goal accepted with optional losses',
      outcome_details: {
        optional_losses: [{ task_id: 'b', name: '可选指标', status: 'failed', reason: 'metric dataset missing' }],
        limitations: ['缺少可选工作 可选指标，相关结论不能宣称完整'],
      },
    });
    expect(summary.headline).toBe('部分完成');
    expect(summary.detail).toContain('可选指标');
    expect(summary.losses[0]).toContain('metric dataset missing');
    expect(summary.limitations.length).toBe(1);
    expect(summary.limitationsLabel).not.toBe('');
  });

  it('describes a blocked outcome as missing conditions, not a plain failure', () => {
    const summary = summarizePlanOutcome({
      status: 'failed',
      outcome: 'blocked',
      outcome_reason: 'required conditions are missing: a',
    });
    expect(summary.headline).toBe('缺少条件');
    expect(summary.detail).toContain('required conditions');
  });

  it('retains reason, losses and missing artifacts in the public summary detail', () => {
    const summary = summarizePlanOutcome({
      status: 'completed', outcome: 'needs_review', outcome_reason: '报告尚未验证',
      outcome_details: {
        optional_losses: [{ task_id: 'plot', name: '额外图表', status: 'failed', reason: 'plot failed' }],
        missing_artifacts: ['report', 'metrics'],
        limitations: ['结果只覆盖部分指标'],
      },
    });
    expect(summary.detail).toBe('报告尚未验证；缺少可选工作：额外图表（失败：plot failed）；缺少产物：report、metrics');
    expect(summary.losses).toEqual(['额外图表（失败：plot failed）']);
    expect(summary.limitations).toEqual(['结果只覆盖部分指标']);
    expect(summary.limitationsLabel).toBe('这些缺失限制的结论');
  });

  it('keeps optional loss status labels, task ID fallback and unknown status text', () => {
    const summary = summarizePlanOutcome({
      status: 'completed', outcome: 'partial',
      outcome_details: { optional_losses: [
        { task_id: 'a', name: '  指标 A  ', status: 'failed', reason: '失败原因' },
        { task_id: 'b', name: '  ', status: 'blocked', reason: '阻断原因' },
        { task_id: 'c', status: 'canceled' },
        { task_id: 'd', status: 'skipped' },
        { task_id: 'e', status: 'future_status', reason: '服务端说明' },
      ] },
    });
    expect(summary.losses).toEqual([
      '指标 A（失败：失败原因）', 'b（被阻断：阻断原因）', 'c（已取消）', 'd（已跳过）', 'e（future_status：服务端说明）',
    ]);
  });

  it.each(['smoke', 'mock', 'blocked', 'failed', 'needs_review'])('does not reinterpret %s as partial when optional losses exist', (outcome) => {
    const summary = summarizePlanOutcome({
      status: outcome === 'failed' || outcome === 'blocked' ? 'failed' : 'completed', outcome,
      outcome_details: { optional_losses: [{ task_id: 'b', status: 'failed' }] },
    });
    expect(summary.headline).toBe(planOutcomeLabel(outcome));
    expect(summary.headline).not.toBe('部分完成');
  });

  it('preserves completed and canceled status fallbacks when no outcome was recorded', () => {
    expect(summarizePlanOutcome({ status: 'completed' }).headline).toBe('已完成');
    expect(summarizePlanOutcome({ status: 'canceled' }).headline).toBe('已取消');
    expect(summarizePlanOutcome(undefined)).toEqual({
      headline: '已结束', detail: '', losses: [], limitations: [], limitationsLabel: '',
    });
    expect(summarizePlanOutcome(null).detail).toBe('');
  });

  it('does not fabricate summary details for empty arrays or mode-only metadata', () => {
    for (const details of [{}, { mode: 'acceptance' }, { accepted_goals: [], optional_losses: [], missing_artifacts: [], limitations: [] }]) {
      expect(summarizePlanOutcome({ status: 'completed', outcome: 'partial', outcome_details: details })).toEqual({
        headline: '部分完成', detail: '', losses: [], limitations: [], limitationsLabel: '',
      });
    }
  });
});
