import { describe, expect, it } from 'vitest';
import {
  DEFAULT_MAX_RECONNECT_ATTEMPTS,
  describeReconnect,
  eventSeq,
  isTerminalPlanEventType,
  lastSeqFromEvents,
  nextBackoffMs,
  shouldReconnect,
  updateLastSeq,
} from '../../src/app/hooks/planStreamReconnect';

describe('nextBackoffMs', () => {
  it('doubles from 1s and caps at 30s', () => {
    const delays = Array.from({ length: 8 }, (_, attempt) => nextBackoffMs(attempt));
    expect(delays).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
  });

  it('keeps jitter deterministic and inside the configured window', () => {
    const first = nextBackoffMs(2, 0.5);
    expect(nextBackoffMs(2, 0.5)).toBe(first);
    expect(first).toBeGreaterThan(2000);
    expect(first).toBeLessThan(4000);
    expect(nextBackoffMs(0, 1)).toBe(630);
    expect(nextBackoffMs(0, -1)).toBe(1000);
    expect(nextBackoffMs(0, 5)).toBe(630);
  });

  it('normalizes invalid or negative attempts to the first delay', () => {
    expect(nextBackoffMs(-1)).toBe(1000);
    expect(nextBackoffMs(Number.NaN)).toBe(1000);
    expect(nextBackoffMs(2.9)).toBe(4000);
  });
});

describe('shouldReconnect', () => {
  it('allows retries while the attempt budget is not exhausted', () => {
    expect(shouldReconnect('task_log', 0)).toBe(true);
    expect(shouldReconnect('', 0)).toBe(true);
    expect(shouldReconnect('task_log', DEFAULT_MAX_RECONNECT_ATTEMPTS - 1)).toBe(true);
    expect(shouldReconnect('task_log', DEFAULT_MAX_RECONNECT_ATTEMPTS)).toBe(false);
    expect(shouldReconnect('task_log', DEFAULT_MAX_RECONNECT_ATTEMPTS + 3)).toBe(false);
  });

  it('honors an explicit max attempt budget', () => {
    expect(shouldReconnect('task_log', 1, 2)).toBe(true);
    expect(shouldReconnect('task_log', 2, 2)).toBe(false);
    expect(shouldReconnect('task_log', 0, 0)).toBe(false);
  });

  it('never reconnects after a terminal plan event', () => {
    expect(isTerminalPlanEventType('plan_completed')).toBe(true);
    expect(isTerminalPlanEventType('plan_failed')).toBe(true);
    expect(isTerminalPlanEventType('plan_canceled')).toBe(true);
    expect(shouldReconnect('plan_completed', 0)).toBe(false);
    expect(shouldReconnect('plan_failed', 0)).toBe(false);
    expect(shouldReconnect('plan_canceled', 1, 100)).toBe(false);
    expect(isTerminalPlanEventType('task_completed')).toBe(false);
  });
});

describe('event sequence cursor', () => {
  it('reads numeric seq values and ignores missing or invalid ones', () => {
    expect(eventSeq({ seq: 7 })).toBe(7);
    expect(eventSeq({ seq: '7' })).toBe(7);
    expect(eventSeq({ seq: 7.8 })).toBe(7);
    expect(eventSeq({ seq: 0 })).toBe(0);
    expect(eventSeq({ seq: -2 })).toBe(0);
    expect(eventSeq({ seq: 'abc' })).toBe(0);
    expect(eventSeq({})).toBe(0);
    expect(eventSeq(null)).toBe(0);
    expect(eventSeq(undefined)).toBe(0);
    expect(eventSeq('not-an-event')).toBe(0);
  });

  it('never moves the cursor backwards', () => {
    expect(updateLastSeq(3, { seq: 9 })).toBe(9);
    expect(updateLastSeq(9, { seq: 3 })).toBe(9);
    expect(updateLastSeq(4, {})).toBe(4);
    expect(updateLastSeq(Number.NaN, { seq: 2 })).toBe(2);
  });

  it('returns the highest seq across a batch of events', () => {
    expect(lastSeqFromEvents([])).toBe(0);
    expect(lastSeqFromEvents([{ seq: 2 }, { seq: 11 }, { seq: 5 }, {}])).toBe(11);
    expect(lastSeqFromEvents([{ event_type: 'task_log' }, { seq: '3' }])).toBe(3);
  });
});

describe('describeReconnect', () => {
  it('returns a user-facing Chinese message with the upcoming delay', () => {
    expect(describeReconnect(1)).toContain('计划事件流已中断');
    expect(describeReconnect(1)).toContain('1 秒后自动重连');
    expect(describeReconnect(1)).toContain('第 1 次');
    expect(describeReconnect(2)).toContain('2 秒后自动重连');
    expect(describeReconnect(5)).toContain('16 秒后自动重连');
  });

  it('caps the advertised delay at 30 seconds', () => {
    expect(describeReconnect(10)).toContain('30 秒后自动重连');
    expect(describeReconnect(0)).toContain('第 1 次');
  });
});
