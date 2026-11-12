import { describe, expect, it } from 'vitest';
import { describeBlockedNode, planOutcomeLabel, summarizePlanOutcome } from '../../src/features/shared/planOutcome';

describe('planOutcome helpers', () => {
  it('labels every outcome, never leaving a terminal plan blank', () => {
    for (const outcome of ['completed', 'partial', 'smoke', 'mock', 'blocked', 'failed', 'needs_review']) {
      expect(planOutcomeLabel(outcome)).not.toBe('');
    }
    expect(planOutcomeLabel('partial')).toBe('部分完成');
    // A finished run whose evidence is pending is not the same as a failure.
    expect(planOutcomeLabel('needs_review')).toContain('待复核');
    expect(planOutcomeLabel('needs_review')).toContain('已结束');
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
});