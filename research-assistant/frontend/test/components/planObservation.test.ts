import { describe, expect, it, vi } from 'vitest';
import type { PlanEvent } from '../../src/contracts/api';
import {
  anchorRunFromSnapshot,
  canStartPlanRun,
  createPlanMergeState,
  describeAuthorizationFailure,
  describeConnectionState,
  emptyRunBoundary,
  isAuthorizationFailureStatus,
  isNewPlanRun,
  isObservablePlanStatus,
  isPlanStartedEventType,
  isStaleRunEvent,
  isStartRequestRunOpener,
  isTerminalPlanStatus,
  markStartAccepted,
  mergePlanEvent,
  planBusinessState,
  planEventBoundary,
  planRunBoundaryFromSnapshot,
  readHttpStatus,
  shouldObservePlan,
} from '../../src/app/hooks/planObservation';
import { selectRunHistory } from '../../src/app/hooks/usePlanHistoryHydrator';

const event = (patch: Partial<PlanEvent> & { seq?: number }): PlanEvent =>
  ({
    plan_id: 'plan-1',
    event_type: 'task_log',
    timestamp: '2026-11-09T10:00:00Z',
    ...patch,
  }) as PlanEvent;

describe('plan business mapping', () => {
  it('maps terminal, paused, blocked and runnable statuses without defaulting blocked to runnable', () => {
    expect(planBusinessState('completed')).toBe('terminal');
    expect(planBusinessState('failed')).toBe('terminal');
    expect(planBusinessState('canceled')).toBe('terminal');
    expect(planBusinessState('in_progress')).toBe('observing');
    expect(planBusinessState('awaiting_approval')).toBe('awaiting_approval');
    expect(planBusinessState('awaiting_input')).toBe('awaiting_input');
    expect(planBusinessState('awaiting_review')).toBe('review');
    expect(planBusinessState('blocked')).toBe('blocked');
    expect(planBusinessState('pending')).toBe('runnable');
    expect(planBusinessState('weird_future_status')).toBe('unknown');
    expect(planBusinessState('in_progress', true)).toBe('paused');
  });

  it('keeps the start button closed once a start was accepted or the plan is paused', () => {
    expect(canStartPlanRun('pending')).toBe(true);
    expect(canStartPlanRun('pending', { startAccepted: true })).toBe(false);
    expect(canStartPlanRun('pending', { paused: true })).toBe(false);
    expect(canStartPlanRun('in_progress')).toBe(false);
    expect(canStartPlanRun('blocked')).toBe(false);
    expect(canStartPlanRun('completed')).toBe(false);
  });

  it('observes running, waiting and start-accepted-pending plans only', () => {
    expect(shouldObservePlan('in_progress')).toBe(true);
    expect(shouldObservePlan('awaiting_approval')).toBe(true);
    expect(shouldObservePlan('awaiting_input')).toBe(true);
    expect(shouldObservePlan('paused', { paused: true, startAccepted: true })).toBe(true);
    expect(shouldObservePlan('pending')).toBe(false);
    expect(shouldObservePlan('pending', { startAccepted: true })).toBe(true);
    expect(shouldObservePlan('completed', { startAccepted: true })).toBe(false);
    expect(shouldObservePlan('blocked', { startAccepted: true })).toBe(false);
  });

  it('separates the connection labels from the business status', () => {
    expect(describeConnectionState('idle')).toBe('');
    expect(describeConnectionState('connecting')).toBe('连接中');
    expect(describeConnectionState('live')).toBe('实时更新');
    expect(describeConnectionState('reconnecting')).toBe('正在重连');
    expect(describeConnectionState('interrupted')).toBe('更新已中断');
    expect(isTerminalPlanStatus('canceled')).toBe(true);
    expect(isObservablePlanStatus('awaiting_input')).toBe(true);
    expect(isObservablePlanStatus('blocked')).toBe(false);
  });
});

describe('run boundaries', () => {
  it('reads the run start from the snapshot usage.started_at, not from execution_id', () => {
    const boundary = planRunBoundaryFromSnapshot({
      current_revision: 4,
      status: 'in_progress',
      usage: { started_at: '2026-11-09T10:00:00Z' },
    });
    expect(boundary.revision).toBe(4);
    expect(boundary.startedAtMs).toBe(Date.parse('2026-11-09T10:00:00Z'));
    expect(boundary.startSeq).toBe(0);
  });

  it('treats a per-task execution_id as irrelevant to the run boundary', () => {
    const state = createPlanMergeState('plan-1');
    mergePlanEvent(state, event({ event_type: 'plan_started', seq: 10, timestamp: '2026-11-09T10:00:00Z' }), { live: false });
    // Two different task execution ids in the same run must not look like two runs.
    mergePlanEvent(state, event({ seq: 11, execution_id: 'task-exec-a', task_id: 'a' }), { live: false });
    mergePlanEvent(state, event({ seq: 12, execution_id: 'task-exec-b', task_id: 'b' }), { live: false });
    expect(state.run.startSeq).toBe(10);
    expect(state.run.revision).toBe(0);
  });

  it('drops events that predate the anchored run start', () => {
    const run = { ...emptyRunBoundary(), startedAtMs: Date.parse('2026-11-09T12:00:00Z') };
    expect(isStaleRunEvent(run, planEventBoundary(event({ seq: 99, timestamp: '2026-11-09T11:00:00Z' })))).toBe(true);
    expect(isStaleRunEvent(run, planEventBoundary(event({ seq: 99, timestamp: '2026-11-09T13:00:00Z' })))).toBe(false);
    // Unknown fields never make an event stale.
    expect(isStaleRunEvent(emptyRunBoundary(), planEventBoundary(event({ seq: 1 })))).toBe(false);
  });

  it('only moves the anchored run forward when the snapshot reports a newer start', () => {
    const state = createPlanMergeState('plan-1');
    anchorRunFromSnapshot(state, { usage: { started_at: '2026-11-09T10:00:00Z' }, current_revision: 2 });
    expect(state.run.startedAtMs).toBe(Date.parse('2026-11-09T10:00:00Z'));
    // A stale snapshot read must not roll the boundary back.
    anchorRunFromSnapshot(state, { usage: { started_at: '2026-11-09T09:00:00Z' }, current_revision: 1 });
    expect(state.run.startedAtMs).toBe(Date.parse('2026-11-09T10:00:00Z'));
    expect(state.run.revision).toBe(2);
  });

  it('re-arms terminal handling when the same revision is started again', () => {
    const state = createPlanMergeState('plan-1');
    mergePlanEvent(state, event({ event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' }), { live: false });
    const terminal = mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 5, timestamp: '2026-11-09T10:05:00Z' }), { live: false });
    expect(terminal.terminal).toBe(true);
    expect(state.terminalHandled).toBe(true);

    // Same revision, new server-side run (that is what `usage.started_at` shows).
    anchorRunFromSnapshot(state, { current_revision: 1, usage: { started_at: '2026-11-09T11:00:00Z' } });
    expect(state.terminalHandled).toBe(false);

    const second = mergePlanEvent(state, event({ event_type: 'plan_started', seq: 6, timestamp: '2026-11-09T11:00:00Z' }), { live: false });
    expect(second.accept).toBe(true);
    expect(second.terminal).toBe(false);
    const secondTerminal = mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 9, timestamp: '2026-11-09T11:30:00Z' }), { live: false });
    expect(secondTerminal.terminal).toBe(true);
  });

  it('keeps an old run terminal from ending an accepted-but-pending start', () => {
    const state = createPlanMergeState('plan-1');
    mergePlanEvent(state, event({ event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' }), { live: false });
    mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 4, timestamp: '2026-11-09T10:10:00Z' }), { live: false });
    markStartAccepted(state, Date.parse('2026-11-09T12:00:00Z'));

    // The scheduler has not published the new plan_started yet: a replayed
    // terminal from the previous run must not end the new observation.
    const replay = mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 6, timestamp: '2026-11-09T10:10:00Z' }), { live: true });
    expect(replay.accept).toBe(false);
    expect(replay.reason).toBe('pending-start');
    expect(replay.terminal).toBe(false);
    expect(state.appliedSeq).toBe(4);

    // The previous run's opener is not the request's opener either.
    expect(isStartRequestRunOpener(state, event({ event_type: 'plan_started', seq: 4, timestamp: '2026-11-09T10:00:00Z' }))).toBe(false);
    const replayedOpener = mergePlanEvent(state, event({ event_type: 'plan_started', seq: 6, timestamp: '2026-11-09T10:00:00Z' }), { live: true });
    expect(replayedOpener.reason).toBe('pending-start');

    // The real new opener passes both the cursor floor and the timestamp test.
    const opener = mergePlanEvent(state, event({ event_type: 'plan_started', seq: 7, timestamp: '2026-11-09T12:00:01Z' }), { live: true });
    expect(opener.accept).toBe(true);
    expect(opener.runChanged).toBe(true);
    expect(state.startPending).toBe(false);
  });

  it('recognises a new run for cache invalidation', () => {
    expect(isNewPlanRun({ revision: 1, startedAtMs: 1, status: 'in_progress' }, { revision: 2, startedAtMs: 1, status: 'in_progress' })).toBe(true);
    expect(isNewPlanRun({ revision: 1, startedAtMs: 1, status: 'in_progress' }, { revision: 1, startedAtMs: 2, status: 'in_progress' })).toBe(true);
    expect(isNewPlanRun({ revision: 1, startedAtMs: 1, status: 'completed' }, { revision: 1, startedAtMs: 1, status: 'in_progress' })).toBe(true);
    expect(isNewPlanRun({ revision: 1, startedAtMs: 2, status: 'in_progress' }, { revision: 1, startedAtMs: 2, status: 'in_progress' })).toBe(false);
  });

  it('detects the plan_started opener type', () => {
    expect(isPlanStartedEventType('plan_started')).toBe(true);
    expect(isPlanStartedEventType(' plan_started ')).toBe(true);
    expect(isPlanStartedEventType('plan_completed')).toBe(false);
  });
});

describe('mergePlanEvent cursor discipline', () => {
  it('applies an event to the view before the cursor moves', () => {
    const state = createPlanMergeState('plan-1');
    const seen: number[] = [];
    const decision = mergePlanEvent(state, event({ seq: 3 }), {
      live: true,
      apply: () => {
        // The cursor has not moved yet while the view is being updated.
        seen.push(state.appliedSeq);
      },
    });
    expect(seen).toEqual([0]);
    expect(decision.accept).toBe(true);
    expect(decision.commitSeq).toBe(3);
    expect(state.appliedSeq).toBe(3);
  });

  it('never commits the cursor when the view update throws', () => {
    const state = createPlanMergeState('plan-1');
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const decision = mergePlanEvent(state, event({ seq: 8 }), {
      live: true,
      apply: () => {
        throw new Error('render exploded');
      },
    });
    expect(decision.accept).toBe(false);
    expect(decision.reason).toBe('apply-failed');
    expect(state.appliedSeq).toBe(0);
    spy.mockRestore();

    // A later retry of the same event still lands.
    const retry = mergePlanEvent(state, event({ seq: 8 }), { live: true });
    expect(retry.accept).toBe(true);
    expect(state.appliedSeq).toBe(8);
  });

  it('dedupes by seq so overlapping history and live events append once', () => {
    const state = createPlanMergeState('plan-1');
    const applied: number[] = [];
    const apply = (pending: PlanEvent) => applied.push(Number((pending as { seq?: number }).seq));
    expect(mergePlanEvent(state, event({ seq: 2 }), { live: false, apply }).accept).toBe(true);
    expect(mergePlanEvent(state, event({ seq: 2 }), { live: true, apply }).reason).toBe('duplicate');
    expect(mergePlanEvent(state, event({ seq: 1 }), { live: true, apply }).reason).toBe('duplicate');
    expect(applied).toEqual([2]);
  });

  it('treats zero/missing seq live events as a resync signal that never notifies', () => {
    const state = createPlanMergeState('plan-1');
    const apply = vi.fn();
    const decision = mergePlanEvent(state, event({ event_type: 'task_log' }), { live: true, apply });
    expect(decision.accept).toBe(false);
    expect(decision.resync).toBe(true);
    expect(decision.notify).toBe(false);
    expect(decision.appliedSeq).toBe(0);
    expect(apply).not.toHaveBeenCalled();
    expect(state.appliedSeq).toBe(0);
  });

  it('restores legacy zero-seq history events once and without notification', () => {
    const state = createPlanMergeState('plan-1');
    const apply = vi.fn();
    const legacy = event({ event_type: 'task_log', payload: { message: 'old line' } });
    const first = mergePlanEvent(state, legacy, { live: false, apply });
    expect(first.accept).toBe(true);
    expect(first.notify).toBe(false);
    expect(first.appliedSeq).toBe(0);
    expect(apply).toHaveBeenCalledTimes(1);
    const second = mergePlanEvent(state, legacy, { live: false, apply });
    expect(second.reason).toBe('duplicate');
    expect(apply).toHaveBeenCalledTimes(1);
  });

  it('rejects a live zero-seq plan_started before it can touch the accepted-start state', () => {
    const state = createPlanMergeState('plan-1');
    mergePlanEvent(state, event({ event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' }), { live: false });
    mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 4, timestamp: '2026-11-09T10:10:00Z' }), { live: false });
    // A hydrated legacy line: its fingerprint must survive the rejected event.
    expect(mergePlanEvent(state, event({ event_type: 'task_log', payload: { message: 'old line' } }), { live: false }).accept).toBe(true);
    markStartAccepted(state, Date.parse('2026-11-09T12:00:00Z'));

    const before = {
      appliedSeq: state.appliedSeq,
      run: { ...state.run },
      startPending: state.startPending,
      terminalHandled: state.terminalHandled,
      legacySeenSize: state.legacySeen.size,
    };
    const apply = vi.fn();
    const onRunChanged = vi.fn();

    // Zero seq: the event cannot prove it belongs to the request's run, even
    // though its timestamp is newer than the previous run start.
    const decision = mergePlanEvent(state, event({ event_type: 'plan_started', timestamp: '2026-11-09T12:00:01Z' }), {
      live: true,
      apply,
      onRunChanged,
    });

    expect(decision.accept).toBe(false);
    expect(decision.resync).toBe(true);
    expect(decision.runChanged).toBe(false);
    expect(decision.notify).toBe(false);
    expect(decision.appliedSeq).toBe(before.appliedSeq);
    expect(decision.commitSeq).toBe(0);
    expect(apply).not.toHaveBeenCalled();
    expect(onRunChanged).not.toHaveBeenCalled();

    expect(state.appliedSeq).toBe(before.appliedSeq);
    expect(state.run).toEqual(before.run);
    expect(state.startPending).toBe(before.startPending);
    expect(state.startPending).toBe(true);
    expect(state.terminalHandled).toBe(before.terminalHandled);
    expect(state.terminalHandled).toBe(true);
    expect(state.legacySeen.size).toBe(before.legacySeenSize);
    expect(state.legacySeen.size).toBe(1);
  });

  it('rejects a live zero-seq task event instead of letting it move the run boundary', () => {
    const state = createPlanMergeState('plan-1', { revision: 2, startedAtMs: Date.parse('2026-11-09T10:00:00Z') });
    state.appliedSeq = 5;
    const apply = vi.fn();

    // A higher revision and a newer timestamp would otherwise be merged into the
    // boundary by the non-plan_started branch.
    const decision = mergePlanEvent(
      state,
      event({ event_type: 'task_log', revision: 9, timestamp: '2026-11-09T13:00:00Z', payload: { message: 'legacy' } }),
      { live: true, apply },
    );

    expect(decision.accept).toBe(false);
    expect(decision.reason).toBe('missing-seq');
    expect(decision.resync).toBe(true);
    expect(decision.runChanged).toBe(false);
    expect(apply).not.toHaveBeenCalled();
    expect(state.appliedSeq).toBe(5);
    expect(state.run.revision).toBe(2);
    expect(state.run.startedAtMs).toBe(Date.parse('2026-11-09T10:00:00Z'));
  });

  it('retries a legacy zero-seq history event after its view update throws', () => {
    const state = createPlanMergeState('plan-1');
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const legacy = event({ event_type: 'task_log', revision: 7, timestamp: '2026-11-09T11:00:00Z', payload: { message: 'old line' } });

    const failed = mergePlanEvent(state, legacy, {
      live: false,
      apply: () => {
        throw new Error('hydrate exploded');
      },
    });
    expect(failed.accept).toBe(false);
    expect(failed.reason).toBe('apply-failed');
    expect(state.legacySeen.size).toBe(0);
    // The failed merge must not leave its boundary move behind either.
    expect(state.run.revision).toBe(0);
    expect(state.run.startedAtMs).toBe(0);
    spy.mockRestore();

    // The fingerprint was not recorded before the throw, so a retry still lands.
    const apply = vi.fn();
    const retry = mergePlanEvent(state, legacy, { live: false, apply });
    expect(retry.accept).toBe(true);
    expect(retry.reason).toBe('missing-seq');
    expect(apply).toHaveBeenCalledTimes(1);
    expect(state.legacySeen.size).toBe(1);

    expect(mergePlanEvent(state, legacy, { live: false, apply }).reason).toBe('duplicate');
    expect(apply).toHaveBeenCalledTimes(1);
  });

  it('restores the merge state when a positive-seq opener fails to apply, so the retry succeeds', () => {
    const state = createPlanMergeState('plan-1');
    mergePlanEvent(state, event({ event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' }), { live: false });
    const opener = event({ event_type: 'plan_started', seq: 8, timestamp: '2026-11-09T12:00:00Z' });

    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const onRunChanged = vi.fn();
    const failed = mergePlanEvent(state, opener, {
      live: true,
      apply: () => {
        throw new Error('render exploded');
      },
      onRunChanged,
    });
    expect(failed.accept).toBe(false);
    expect(failed.reason).toBe('apply-failed');
    expect(failed.resync).toBe(false);
    expect(failed.appliedSeq).toBe(1);
    // Cursor stays, and the boundary/flags are rolled back to the previous run.
    expect(state.appliedSeq).toBe(1);
    expect(state.run.startSeq).toBe(1);
    expect(state.run.startedAtMs).toBe(Date.parse('2026-11-09T10:00:00Z'));
    expect(state.run.revision).toBe(0);
    expect(state.startPending).toBe(false);
    expect(state.terminalHandled).toBe(false);
    spy.mockRestore();

    // The same opener is a real run change again, not a no-op replay.
    const applied: string[] = [];
    const retry = mergePlanEvent(state, opener, { live: true, apply: (pending) => applied.push(String(pending.event_type)) });
    expect(retry.accept).toBe(true);
    expect(retry.runChanged).toBe(true);
    expect(applied).toEqual(['plan_started']);
    expect(state.run.startSeq).toBe(8);
    expect(state.run.startedAtMs).toBe(Date.parse('2026-11-09T12:00:00Z'));
    expect(state.appliedSeq).toBe(8);
  });

  it('ignores heartbeats and never counts them as business events', () => {
    const state = createPlanMergeState('plan-1');
    const decision = mergePlanEvent(state, event({ event_type: 'heartbeat', seq: 12 }), { live: true });
    expect(decision.reason).toBe('heartbeat');
    expect(state.appliedSeq).toBe(0);
  });

  it('notifies a terminal event once per run', () => {
    const state = createPlanMergeState('plan-1');
    const first = mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 4 }), { live: true });
    expect(first.terminal).toBe(true);
    const second = mergePlanEvent(state, event({ event_type: 'plan_completed', seq: 5 }), { live: true });
    expect(second.terminal).toBe(false);
    expect(second.notify).toBe(false);
    // The cursor still advances so a reconnect does not replay it forever.
    expect(state.appliedSeq).toBe(5);
  });
});

describe('selectRunHistory', () => {
  const started = (seq: number, timestamp: string) => event({ event_type: 'plan_started', seq, timestamp });
  const log = (seq: number, timestamp: string) => event({ event_type: 'task_log', seq, timestamp, payload: { message: `line-${seq}` } });

  it('keeps only events of the last run', () => {
    const state = createPlanMergeState('plan-1');
    const events = [
      started(1, '2026-11-09T10:00:00Z'),
      log(2, '2026-11-09T10:01:00Z'),
      event({ event_type: 'plan_completed', seq: 3, timestamp: '2026-11-09T10:05:00Z' }),
      started(4, '2026-11-09T12:00:00Z'),
      log(5, '2026-11-09T12:01:00Z'),
    ];
    expect(selectRunHistory(events, state).map((item) => (item as { seq?: number }).seq)).toEqual([4, 5]);
  });

  it('uses the snapshot start time to drop an older run without a plan_started', () => {
    const state = createPlanMergeState('plan-1');
    anchorRunFromSnapshot(state, { usage: { started_at: '2026-11-09T12:00:00Z' } });
    const events = [
      log(1, '2026-11-09T10:00:00Z'),
      event({ event_type: 'plan_completed', seq: 2, timestamp: '2026-11-09T10:05:00Z' }),
      log(3, '2026-11-09T12:01:00Z'),
    ];
    expect(selectRunHistory(events, state).map((item) => (item as { seq?: number }).seq)).toEqual([3]);
  });

  it('keeps only post-request events while the accepted start has no opener yet', () => {
    const state = createPlanMergeState('plan-1');
    mergePlanEvent(state, event({ event_type: 'plan_started', seq: 1, timestamp: '2026-11-09T10:00:00Z' }), { live: false });
    mergePlanEvent(state, log(2, '2026-11-09T10:01:00Z'), { live: false });
    markStartAccepted(state, Date.parse('2026-11-09T12:00:00Z'));
    const events = [
      started(1, '2026-11-09T10:00:00Z'),
      log(2, '2026-11-09T10:01:00Z'),
      event({ event_type: 'plan_completed', seq: 3, timestamp: '2026-11-09T10:05:00Z' }),
      log(4, '2026-11-09T12:00:30Z'),
    ];
    expect(selectRunHistory(events, state).map((item) => (item as { seq?: number }).seq)).toEqual([4]);
  });
});

describe('http failure classification', () => {
  it('reads an axios style status without importing axios', () => {
    expect(readHttpStatus({ response: { status: 403 } })).toBe(403);
    expect(readHttpStatus({ status: '404' })).toBe(404);
    expect(readHttpStatus(new Error('network'))).toBe(0);
    expect(readHttpStatus(null)).toBe(0);
  });

  it('stops automatic retries for 401/403/404 only', () => {
    expect(isAuthorizationFailureStatus(401)).toBe(true);
    expect(isAuthorizationFailureStatus(403)).toBe(true);
    expect(isAuthorizationFailureStatus(404)).toBe(true);
    expect(isAuthorizationFailureStatus(500)).toBe(false);
    expect(isAuthorizationFailureStatus(0)).toBe(false);
    expect(describeAuthorizationFailure(401)).toContain('登录状态已失效');
    expect(describeAuthorizationFailure(403)).toContain('无权查看');
    expect(describeAuthorizationFailure(404)).toContain('不存在');
  });
});
