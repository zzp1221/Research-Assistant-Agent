import type { PlanEvent } from '../../contracts/api';
import { eventSeq } from './planStreamReconnect';

/**
 * Plan observation helpers (UI-02).
 *
 * The plan graph contract predates the fields this feature needs (`seq`,
 * `revision`, run-boundary timestamps), so every reader below stays structural
 * and optional. Nothing here mutates shared contracts.
 *
 * Run identity is **not** `PlanEvent.execution_id`: that field is the *task*
 * execution id, so a single plan run produces many different values (and legacy
 * run-terminal events usually carry none at all). A plan run is identified by:
 *
 * 1. the `plan_started` event that opened it (`seq` + timestamp), or
 * 2. the snapshot's `usage.started_at` (server-side run start time),
 * 3. with `revision` only as a coarse secondary signal.
 */

/** Plan events that open a new run of the plan (published by the scheduler). */
export const PLAN_STARTED_EVENT_TYPE = 'plan_started';

/** Connection lifecycle, deliberately separate from the business plan status. */
export type PlanConnectionState = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'interrupted';

export type PlanBusinessState =
  | 'unknown'
  | 'runnable'
  | 'observing'
  | 'awaiting_approval'
  | 'awaiting_input'
  | 'paused'
  | 'review'
  | 'blocked'
  | 'terminal';

// Plan statuses (models.TaskStatus on the plan) — not the terminal *event*
// names, which are `plan_completed` / `plan_failed` / `plan_canceled`.
const TERMINAL_PLAN_STATUSES: ReadonlySet<string> = new Set(['completed', 'failed', 'canceled']);

/** Statuses whose server-side run is still producing events. */
const OBSERVABLE_PLAN_STATUSES: ReadonlySet<string> = new Set(['in_progress', 'awaiting_approval', 'awaiting_input']);

const RUNNABLE_PLAN_STATUSES: ReadonlySet<string> = new Set(['', 'pending', 'ready']);

export const isTerminalPlanStatus = (status: string | null | undefined): boolean =>
  TERMINAL_PLAN_STATUSES.has(String(status ?? '').trim());

/** True when a live subscription is useful for this server status. */
export const isObservablePlanStatus = (status: string | null | undefined): boolean =>
  OBSERVABLE_PLAN_STATUSES.has(String(status ?? '').trim());

/**
 * Business mapping used by the canvas buttons. `blocked` is a server-defined
 * attention state: it is neither runnable nor terminal, and never "待运行".
 */
export const planBusinessState = (status: string | null | undefined, paused = false): PlanBusinessState => {
  const normalized = String(status ?? '').trim();
  if (isTerminalPlanStatus(normalized)) return 'terminal';
  if (paused) return 'paused';
  switch (normalized) {
    case 'in_progress':
      return 'observing';
    case 'awaiting_approval':
      return 'awaiting_approval';
    case 'awaiting_input':
      return 'awaiting_input';
    case 'awaiting_review':
      return 'review';
    case 'blocked':
      return 'blocked';
    default:
      return RUNNABLE_PLAN_STATUSES.has(normalized) ? 'runnable' : 'unknown';
  }
};

/** The run may only be started from a runnable status with nothing in flight. */
export const canStartPlanRun = (
  status: string | null | undefined,
  options: { paused?: boolean; startAccepted?: boolean; connectionActive?: boolean } = {},
): boolean => {
  if (options.startAccepted) return false;
  if (options.paused) return false;
  return planBusinessState(status, false) === 'runnable';
};

/**
 * Whether this view should keep a live subscription for the given snapshot.
 *
 * `startAccepted` is the local "the execute request returned 202" flag: the
 * scheduler may not have flipped the snapshot yet, so a transient `pending`
 * snapshot must not tear the subscription down or reopen the run button.
 */
export const shouldObservePlan = (
  status: string | null | undefined,
  options: { paused?: boolean; startAccepted?: boolean } = {},
): boolean => {
  const normalized = String(status ?? '').trim();
  if (isTerminalPlanStatus(normalized)) return false;
  if (normalized === 'blocked') return false;
  if (isObservablePlanStatus(normalized)) return true;
  // A paused plan keeps its subscription so resume/cancel stay live, but only
  // when it was actually started in this view or the server says it is running.
  if (options.paused && options.startAccepted) return true;
  return Boolean(options.startAccepted);
};

/** Snapshot statuses that prove an accepted start has reached the scheduler. */
export const isStartConfirmedStatus = (status: string | null | undefined): boolean => {
  const normalized = String(status ?? '').trim();
  if (isObservablePlanStatus(normalized)) return true;
  return isTerminalPlanStatus(normalized);
};

export const describeConnectionState = (state: PlanConnectionState): string => {
  switch (state) {
    case 'connecting':
      return '连接中';
    case 'live':
      return '实时更新';
    case 'reconnecting':
      return '正在重连';
    case 'interrupted':
      return '更新已中断';
    default:
      return '';
  }
};

/**
 * Run boundary used to keep one execution's terminal event from ending a newer
 * run of the same plan.
 *
 * - `startedAtMs` is the server start time of the observed run (snapshot
 *   `usage.started_at`, or the timestamp of the `plan_started` event).
 * - `startSeq` is the `seq` of the `plan_started` event that opened this run,
 *   when the client has seen it. `seq` is monotonic per plan, so anything below
 *   it belongs to a previous run.
 * - `revision` is a coarse secondary signal, not a run identity: re-running the
 *   same revision must still start a new run.
 */
export interface PlanRunBoundary {
  revision: number;
  startedAtMs: number;
  startSeq: number;
}

export const emptyRunBoundary = (): PlanRunBoundary => ({ revision: 0, startedAtMs: 0, startSeq: 0 });

interface PlanEventBoundaryFields {
  seq?: unknown;
  revision?: unknown;
  timestamp?: unknown;
}

const asRecord = (value: unknown): PlanEventBoundaryFields | null =>
  value && typeof value === 'object' ? (value as PlanEventBoundaryFields) : null;

const readNonNegativeInt = (value: unknown): number => {
  const numeric =
    typeof value === 'number' ? value : typeof value === 'string' && value.trim() !== '' ? Number(value) : Number.NaN;
  if (!Number.isFinite(numeric) || numeric < 0) return 0;
  return Math.floor(numeric);
};

const readTimestampMs = (value: unknown): number => {
  if (typeof value !== 'string' || value.trim() === '') return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
};

export const planEventRevision = (event: unknown): number => readNonNegativeInt(asRecord(event)?.revision);

export const planEventTimestampMs = (event: unknown): number => readTimestampMs(asRecord(event)?.timestamp);

export interface PlanEventBoundary extends PlanRunBoundary {
  seq: number;
}

/** Structural view of one event's run-boundary fields. */
export const planEventBoundary = (event: unknown): PlanEventBoundary => ({
  revision: planEventRevision(event),
  startedAtMs: planEventTimestampMs(event),
  startSeq: 0,
  seq: eventSeq(event),
});

/**
 * Run boundary implied by a plan snapshot: the server records the current run's
 * start in `usage.started_at`, which is what survives a page refresh.
 */
export interface PlanSnapshotBoundarySource {
  current_revision?: number | null;
  status?: string | null;
  paused?: boolean | null;
  usage?: { started_at?: string | null; finished_at?: string | null } | null;
}

export const planRunBoundaryFromSnapshot = (snapshot: PlanSnapshotBoundarySource | null | undefined): PlanRunBoundary => ({
  revision: readNonNegativeInt(snapshot?.current_revision),
  startedAtMs: readTimestampMs(snapshot?.usage?.started_at),
  startSeq: 0,
});

/**
 * Shared merge state: one instance per observed plan. History hydration and the
 * live stream both go through {@link mergePlanEvent}, so the cursor only moves
 * after an event has actually been merged into the view.
 */
export interface PlanMergeState {
  planId: string;
  /** Highest seq actually applied to the view (the SSE `after_seq` cursor). */
  appliedSeq: number;
  run: PlanRunBoundary;
  /** Fingerprints of legacy zero-seq events already merged. */
  legacySeen: Set<string>;
  /** Terminal event already handled for the current run. */
  terminalHandled: boolean;
  /** An execute request was accepted here but no `plan_started` arrived yet. */
  startPending: boolean;
  /** Cursor at the moment the start was accepted: the pre-request floor. */
  startFloorSeq: number;
  /** Local clock when the start was accepted (identifies the new run's events). */
  startAcceptedAtMs: number;
  /** Observed run start time when the start was accepted (the previous run). */
  startBoundaryStartedAtMs: number;
}

export const createPlanMergeState = (planId = '', run: Partial<PlanRunBoundary> = {}): PlanMergeState => ({
  planId,
  appliedSeq: 0,
  run: { ...emptyRunBoundary(), ...run },
  legacySeen: new Set<string>(),
  terminalHandled: false,
  startPending: false,
  startFloorSeq: 0,
  startAcceptedAtMs: 0,
  startBoundaryStartedAtMs: 0,
});

/**
 * Mark "this view just received 202 for an execute request".
 *
 * Until the scheduler publishes the new run's `plan_started`, every terminal
 * event belongs to the previous run and must not end the new observation. The
 * pre-request cursor floor and the previously observed run start are kept so
 * history hydration can tell the new run's opening event apart from the old
 * one's.
 */
export const markStartAccepted = (state: PlanMergeState, nowMs = Date.now()): void => {
  state.startPending = true;
  state.startFloorSeq = state.appliedSeq;
  state.startAcceptedAtMs = nowMs;
  state.startBoundaryStartedAtMs = state.run.startedAtMs;
};

/**
 * True when a run opener (a `plan_started` event, or a snapshot start time) can
 * only belong to a run opened *after* this view's execute request. An older
 * run's opener fails both tests: its seq is at or below the pre-request cursor
 * and its timestamp is not after the previously observed run start.
 */
export const isStartRequestRunOpenerBoundary = (
  state: PlanMergeState,
  boundary: { seq: number; startedAtMs: number },
): boolean => {
  if (state.startBoundaryStartedAtMs > 0 && boundary.startedAtMs > 0) {
    // Timestamps are the strongest signal: every start records a new one.
    return boundary.startedAtMs > state.startBoundaryStartedAtMs;
  }
  return boundary.seq > 0 && boundary.seq > state.startFloorSeq;
};

export const isStartRequestRunOpener = (state: PlanMergeState, event: PlanEvent): boolean => {
  if (!isPlanStartedEventType(String(event?.event_type ?? ''))) return false;
  return isStartRequestRunOpenerBoundary(state, planEventBoundary(event));
};

/**
 * Anchor the observed run to a snapshot. Only forward-moving: a stale snapshot
 * read must not move the boundary back to a previous run.
 */
export const anchorRunFromSnapshot = (
  state: PlanMergeState,
  snapshot: PlanSnapshotBoundarySource | null | undefined,
): boolean => {
  const next = planRunBoundaryFromSnapshot(snapshot);
  let changed = false;
  if (next.revision > state.run.revision) {
    state.run.revision = next.revision;
    changed = true;
  }
  if (next.startedAtMs > state.run.startedAtMs) {
    const previous = state.run.startedAtMs;
    state.run.startedAtMs = next.startedAtMs;
    if (previous > 0) {
      // A newer server-side start is a new run: old terminal/result state ends.
      state.terminalHandled = false;
      state.startPending = false;
      state.legacySeen.clear();
    }
    changed = true;
  }
  return changed;
};

export const isTerminalPlanEventType = (eventType: string): boolean =>
  eventType === 'plan_completed' || eventType === 'plan_failed' || eventType === 'plan_canceled';

export const isPlanStartedEventType = (eventType: string): boolean =>
  String(eventType ?? '').trim() === PLAN_STARTED_EVENT_TYPE;

export type PlanMergeReason =
  | 'applied'
  | 'duplicate'
  | 'stale-run'
  | 'pending-start'
  | 'missing-seq'
  | 'heartbeat'
  | 'apply-failed';

export interface PlanMergeDecision {
  /** Apply this event's state to the view (true only after the apply callback ran). */
  accept: boolean;
  reason: PlanMergeReason;
  /** Cursor value after processing. */
  appliedSeq: number;
  /** Seq committed to the cursor by this call (0 = cursor unchanged). */
  commitSeq: number;
  /** Snapshot / numbered-history resync is required. */
  resync: boolean;
  /** The event opened a new run: node logs/results of the old run are void. */
  runChanged: boolean;
  /** Terminal event of the *current* run. */
  terminal: boolean;
  /** Callers may surface a user-visible notification for this event. */
  notify: boolean;
}

export interface PlanMergeOptions {
  live: boolean;
  /**
   * Applies the event to the view. Invoked **before** the cursor moves; if it
   * throws, the cursor stays where it was and the event counts as not applied.
   */
  apply?: (event: PlanEvent) => void;
  /**
   * A `plan_started` opener moved the observed run: the previous run's logs and
   * results are void. Invoked before `apply`.
   */
  onRunChanged?: () => void;
}

const legacyFingerprint = (event: PlanEvent): string => {
  const payload = (event.payload ?? {}) as Record<string, unknown>;
  return [
    event.event_type,
    event.task_id ?? '',
    String(event.timestamp ?? ''),
    String(payload.message ?? ''),
    String(payload.result ?? ''),
    String(payload.error ?? ''),
  ].join('|');
};

const sameRunBoundary = (left: PlanRunBoundary, right: PlanRunBoundary): boolean =>
  left.revision === right.revision && left.startedAtMs === right.startedAtMs && left.startSeq === right.startSeq;

const runApply = (apply: ((event: PlanEvent) => void) | undefined, event: PlanEvent): boolean => {
  if (!apply) return true;
  try {
    apply(event);
    return true;
  } catch (error) {
    console.error('[plan] failed to apply event', error);
    return false;
  }
};

/**
 * Merge one plan event into the shared cursor state.
 *
 * Rules that matter for the acceptance cases:
 * - `seq` is a hard dedupe key; an event is applied to the view *first* and the
 *   cursor is committed afterwards, so a throw can never skip an event.
 * - zero/missing seq (legacy revisions) never advances the cursor and never
 *   produces a user-visible notification; live callers resync from a snapshot
 *   or the numbered history instead.
 * - an event belonging to an older run of the same plan is dropped, so a
 *   previous run's terminal event cannot end the current run. While an accepted
 *   start has not yet produced `plan_started`, *every* terminal event is old.
 */
export const mergePlanEvent = (
  state: PlanMergeState,
  event: PlanEvent,
  options: PlanMergeOptions,
): PlanMergeDecision => {
  const seq = eventSeq(event);
  const eventType = String(event.event_type ?? '');
  const boundary = planEventBoundary(event);
  const idle = (reason: PlanMergeReason, extra: Partial<PlanMergeDecision> = {}): PlanMergeDecision => ({
    accept: false,
    reason,
    appliedSeq: state.appliedSeq,
    commitSeq: 0,
    resync: false,
    runChanged: false,
    terminal: false,
    notify: false,
    ...extra,
  });

  if (eventType === 'heartbeat' || eventType === '') return idle('heartbeat');

  // A live event without a usable seq carries no run identity at all (legacy
  // revisions, malformed payloads). Reject it *before* anything below can move
  // the run boundary or clear the start/terminal flags: a zero-seq
  // `plan_started` must not open a run, and a zero-seq task event must not
  // advance the boundary with its revision/timestamp. The caller resyncs from
  // the snapshot or the numbered history instead. Historical (non-live)
  // zero-seq events are still hydrated below.
  if (seq <= 0 && options.live) return idle('missing-seq', { resync: true });

  // Everything below may move the run boundary before the view update runs, so
  // keep the pre-merge state: if `apply` throws, the merge state must not stay
  // on a run that never reached the view (the same event is retried later).
  const previousBoundary: PlanRunBoundary = { ...state.run };
  const previousStartPending = state.startPending;
  const previousTerminalHandled = state.terminalHandled;
  const previousLegacySeen = new Set(state.legacySeen);
  const restoreRunState = (): void => {
    state.run = previousBoundary;
    state.startPending = previousStartPending;
    state.terminalHandled = previousTerminalHandled;
    state.legacySeen = previousLegacySeen;
  };

  let runChanged = false;
  if (isPlanStartedEventType(eventType)) {
    // A replayed plan_started must not roll the run back and re-arm terminal
    // handling for this run.
    if (seq > 0 && seq <= state.appliedSeq) return idle('duplicate');
    if (state.startPending && !isStartRequestRunOpener(state, event)) {
      // Still the opener of the run that was already finished when this view
      // accepted the new start; keep observing under the pre-request boundary.
      return idle('pending-start');
    }
    const nextRun: PlanRunBoundary = {
      revision: Math.max(state.run.revision, boundary.revision),
      startedAtMs: boundary.startedAtMs || state.run.startedAtMs,
      startSeq: seq > 0 ? seq : state.run.startSeq,
    };
    runChanged = !sameRunBoundary(state.run, nextRun);
    state.run = nextRun;
    state.startPending = false;
    state.terminalHandled = false;
    state.legacySeen.clear();
    if (runChanged) options.onRunChanged?.();
  } else {
    if (state.startPending && isTerminalPlanEventType(eventType)) {
      // Accepted start, scheduler has not published plan_started yet: the only
      // terminal events around belong to the previous run. Never end this
      // observation on them; ask for a snapshot/numbered history instead so a
      // genuinely finished new run is still detected from the server.
      return idle('pending-start', { resync: true });
    }
    if (isStaleRunEvent(state.run, boundary)) return idle('stale-run');
    if (boundary.revision > state.run.revision) state.run.revision = boundary.revision;
    if (boundary.startedAtMs > 0 && state.run.startedAtMs === 0) state.run.startedAtMs = boundary.startedAtMs;
  }

  const terminal = isTerminalPlanEventType(eventType);

  if (seq <= 0) {
    // Live zero-seq events were rejected above; only history hydration reaches
    // this branch.
    const fingerprint = legacyFingerprint(event);
    if (state.legacySeen.has(fingerprint)) return idle('duplicate', { runChanged });
    if (!runApply(options.apply, event)) {
      // Nothing was merged into the view: drop the boundary move and keep the
      // fingerprint unseen so a later retry of this event is not skipped.
      restoreRunState();
      return idle('apply-failed');
    }
    // Recorded only after the event actually reached the view.
    state.legacySeen.add(fingerprint);
    const terminalForView = terminal && !state.terminalHandled;
    if (terminalForView) state.terminalHandled = true;
    return {
      accept: true,
      reason: 'missing-seq',
      appliedSeq: state.appliedSeq,
      commitSeq: 0,
      resync: false,
      runChanged,
      terminal: terminalForView,
      notify: false,
    };
  }

  if (seq <= state.appliedSeq) return idle('duplicate', { runChanged });

  if (terminal && state.terminalHandled) {
    // Same run already reported its terminal event; keep the cursor moving so a
    // reconnect does not replay it forever, but never apply or notify twice.
    state.appliedSeq = seq;
    return idle('duplicate', { runChanged, commitSeq: seq, appliedSeq: seq });
  }

  if (!runApply(options.apply, event)) {
    // The event never reached the view: a positive-seq opener must not leave the
    // boundary/terminal flags on a run the view does not know about, otherwise
    // the retried opener looks like a no-op replay and the run is lost.
    restoreRunState();
    return idle('apply-failed');
  }

  state.appliedSeq = seq;
  if (terminal) state.terminalHandled = true;

  return {
    accept: true,
    reason: 'applied',
    appliedSeq: state.appliedSeq,
    commitSeq: seq,
    resync: false,
    runChanged,
    terminal,
    notify: options.live,
  };
};

/**
 * True when the event belongs to an execution that started before the run this
 * view is observing. Unknown fields never make an event stale.
 */
export const isStaleRunEvent = (run: PlanRunBoundary, event: PlanEventBoundary): boolean => {
  if (event.revision > 0 && run.revision > 0 && event.revision < run.revision) return true;
  if (run.startSeq > 0 && event.seq > 0 && event.seq < run.startSeq) return true;
  if (run.startedAtMs > 0 && event.startedAtMs > 0 && event.startedAtMs < run.startedAtMs) return true;
  return false;
};

/**
 * Detect the start of a new run for cache invalidation: a newer revision, a
 * newer server start time, or a plan that moved from a terminal status back to
 * an active one.
 */
export const isNewPlanRun = (
  previous: { revision: number; startedAtMs: number; status: string | null | undefined },
  next: { revision: number; startedAtMs: number; status: string | null | undefined },
): boolean => {
  if (next.revision > 0 && previous.revision > 0 && next.revision > previous.revision) return true;
  if (next.startedAtMs > 0 && next.startedAtMs > previous.startedAtMs) return true;
  if (isTerminalPlanStatus(previous.status) && !isTerminalPlanStatus(next.status)) return true;
  return false;
};

/** Read an HTTP status from an axios/fetch style error without importing types. */
export const readHttpStatus = (error: unknown): number => {
  if (!error || typeof error !== 'object') return 0;
  const record = error as { response?: { status?: unknown }; status?: unknown };
  const raw = record.response?.status ?? record.status;
  const numeric = typeof raw === 'number' ? raw : typeof raw === 'string' ? Number(raw) : Number.NaN;
  return Number.isFinite(numeric) ? Math.floor(numeric) : 0;
};

/** 401/403/404 are terminal for automatic retries; they need an explicit fix. */
export const isAuthorizationFailureStatus = (status: number): boolean =>
  status === 401 || status === 403 || status === 404;

export const describeAuthorizationFailure = (status: number): string => {
  switch (status) {
    case 401:
      return '登录状态已失效，进度更新已停止；请重新登录后点击“重新连接”。';
    case 403:
      return '当前身份无权查看该计划，进度更新已停止；请切换到有权限的账号。';
    case 404:
      return '计划不存在或已被删除，进度更新已停止。';
    default:
      return '无法继续观察该计划，进度更新已停止。';
  }
};
