import { PLAN_EVENTS } from '../../contracts/events';

// The plan SSE endpoint closes the stream after a terminal plan event, so these
// events must never trigger an automatic reconnect.
const TERMINAL_PLAN_EVENT_TYPES: ReadonlySet<string> = new Set([
  PLAN_EVENTS.PLAN_COMPLETED,
  PLAN_EVENTS.PLAN_FAILED,
  PLAN_EVENTS.PLAN_CANCELED,
]);

export const DEFAULT_MAX_RECONNECT_ATTEMPTS = 6;

const BASE_BACKOFF_MS = 1_000;
const MAX_BACKOFF_MS = 30_000;

// Deterministic jitter fractions keep retries reproducible in tests while still
// spreading clients out when a backend restarts.
const JITTER_FRACTIONS = [0.37, 0.11, 0.73, 0.05, 0.61, 0.29] as const;

/**
 * Exponential backoff for a zero-based reconnect attempt:
 * 1s, 2s, 4s, 8s, 16s and then a 30s cap.
 *
 * `jitterRatio` (0..1) scales a deterministic per-attempt reduction of the
 * delay; the default 0 keeps the plain exponential series.
 */
export const nextBackoffMs = (attempt: number, jitterRatio = 0): number => {
  const safeAttempt = Number.isFinite(attempt) ? Math.max(0, Math.floor(attempt)) : 0;
  const baseDelay = Math.min(MAX_BACKOFF_MS, BASE_BACKOFF_MS * 2 ** safeAttempt);
  const ratio = Number.isFinite(jitterRatio) ? Math.min(1, Math.max(0, jitterRatio)) : 0;
  if (ratio === 0) return baseDelay;
  const fraction = JITTER_FRACTIONS[safeAttempt % JITTER_FRACTIONS.length];
  return Math.round(baseDelay * (1 - ratio * fraction));
};

/** True when the plan already reached a terminal state. */
export const isTerminalPlanEventType = (eventType: string): boolean =>
  TERMINAL_PLAN_EVENT_TYPES.has(String(eventType ?? '').trim());

/**
 * Decide whether an SSE error should schedule another connection.
 *
 * `eventType` is the type of the last plan event received on the stream ('' if
 * none); `attempts` counts reconnects already scheduled. A terminal plan event
 * or an exhausted attempt budget stops the loop.
 */
export const shouldReconnect = (
  eventType: string,
  attempts: number,
  maxAttempts: number = DEFAULT_MAX_RECONNECT_ATTEMPTS,
): boolean => {
  if (isTerminalPlanEventType(eventType)) return false;
  const safeAttempts = Number.isFinite(attempts) ? Math.max(0, Math.floor(attempts)) : 0;
  const safeMaxAttempts = Number.isFinite(maxAttempts) ? Math.max(0, Math.floor(maxAttempts)) : 0;
  return safeAttempts < safeMaxAttempts;
};

/**
 * Read the numeric `seq` field of a plan event. Events without a usable seq
 * (heartbeats, legacy payloads, seq <= 0) yield 0.
 */
export const eventSeq = (event: unknown): number => {
  if (!event || typeof event !== 'object') return 0;
  const raw = (event as { seq?: unknown }).seq;
  const value =
    typeof raw === 'number'
      ? raw
      : typeof raw === 'string' && raw.trim() !== ''
        ? Number(raw)
        : Number.NaN;
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.floor(value);
};

/** Update an SSE cursor, never moving it backwards. */
export const updateLastSeq = (current: number, event: unknown): number => {
  const safeCurrent = Number.isFinite(current) ? Math.max(0, Math.floor(current)) : 0;
  return Math.max(safeCurrent, eventSeq(event));
};

/** Highest numeric seq across a list of events (0 when none carry a seq). */
export const lastSeqFromEvents = (events: ReadonlyArray<unknown>): number =>
  events.reduce<number>((maxSeq, event) => updateLastSeq(maxSeq, event), 0);

/**
 * User-facing Chinese status for the reconnect that is about to run.
 * `attempt` is the 1-based number of the scheduled reconnect.
 */
export const describeReconnect = (attempt: number): string => {
  const safeAttempt = Number.isFinite(attempt) ? Math.max(1, Math.floor(attempt)) : 1;
  const seconds = Math.max(1, Math.round(nextBackoffMs(safeAttempt - 1) / 1_000));
  return `计划事件流已中断，${seconds} 秒后自动重连（第 ${safeAttempt} 次）。`;
};
