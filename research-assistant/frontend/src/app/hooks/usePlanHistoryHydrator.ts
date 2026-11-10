import { useCallback, useEffect, useRef } from 'react';
import type { PlanEvent } from '../../contracts/api';
import { getPlanEvents } from '../../services/api/researchAssistantApi';
import type { RequestIdentity } from '../../services/api/researchAssistantApi';
import {
  emptyRunBoundary,
  isStaleRunEvent,
  isTerminalPlanEventType,
  mergePlanEvent,
  planEventBoundary,
  readHttpStatus,
} from './planObservation';
import type { PlanMergeState } from './planObservation';

export interface PlanHistoryTarget {
  id: string;
  status?: string | null;
  revision?: number;
}

export interface PlanHistoryOptions {
  /** Re-read the durable log even when this plan was hydrated before. */
  force?: boolean;
  /**
   * Liveness guard captured by the *caller* at request start.
   *
   * The hydrator-level `isCurrent` option answers "is the hook's latest view
   * current?" — which is not enough for a forced re-read of the *same* plan,
   * because a late response from the previous run of that plan (or the previous
   * session generation) still satisfies it. This per-call guard is evaluated
   * after the await, around every merge and in the cache/catch branches, so a
   * superseded call can neither advance the cursor nor clear/settle the cache
   * of the current flow.
   */
  isCurrent?: () => boolean;
}

export interface PlanHistoryResult {
  /** Highest seq that is now applied to the view; the SSE cursor to resume from. */
  appliedSeq: number;
  /** Events applied to the view during this call. */
  applied: number;
  /** The history request failed; the caller keeps its previous cursor. */
  failed: boolean;
  /** HTTP status of the failed request (0 for network / unknown failures). */
  status: number;
  /** 401/403/404: automatic retries must stop until the identity is fixed. */
  authorizationFailed: boolean;
  /** The call was superseded by another plan/identity generation. */
  canceled: boolean;
}

export interface PlanHistoryHydratorOptions {
  identity: RequestIdentity;
  /**
   * The single merge state shared with the live stream. History and realtime
   * must never keep separate cursors, otherwise a reconnect either replays or
   * skips events.
   */
  getMergeState: () => PlanMergeState;
  /** Applies one event to the view; history never triggers side effects. */
  applyEvent: (event: PlanEvent, options: { notify: boolean }) => void;
  /**
   * Generation guard. Checked before touching the merge state and before every
   * single `applyEvent`, so a slow read for plan A can never mutate plan B.
   */
  isCurrent?: () => boolean;
}

export interface PlanHistoryHydrator {
  applyHistory: (target: PlanHistoryTarget | null, options?: PlanHistoryOptions) => Promise<PlanHistoryResult>;
  resetHistory: () => void;
}

/**
 * Pick the events that belong to the run this view is observing.
 *
 * The run start is the last `plan_started` event in the log, or — when this view
 * accepted a start and the scheduler has not published the new opener yet — the
 * pre-request cursor floor. The snapshot's `usage.started_at` (already anchored
 * into the merge state) filters out anything older in either case, so a
 * previous run's terminal event never ends the current observation.
 */
export const selectRunHistory = (events: readonly PlanEvent[], state: PlanMergeState): PlanEvent[] => {
  let lastStartedSeq = 0;
  let lastStartedAtMs = 0;
  for (const event of events) {
    if (String(event?.event_type ?? '').trim() !== 'plan_started') continue;
    const boundary = planEventBoundary(event);
    if (boundary.seq >= lastStartedSeq) {
      lastStartedSeq = boundary.seq;
      lastStartedAtMs = boundary.startedAtMs;
    }
  }

  const pendingOpener =
    state.startPending && !(lastStartedSeq > 0 && lastStartedAtMs > state.startBoundaryStartedAtMs);
  if (pendingOpener) {
    // The new run has not published its opener yet: only non-terminal events
    // appended after the request may belong to it. Every terminal event in the
    // log still belongs to the previous run and must not end this observation.
    return events.filter((event) => {
      const boundary = planEventBoundary(event);
      if (boundary.seq <= 0) return false;
      if (isTerminalPlanEventType(String(event?.event_type ?? ''))) return false;
      return boundary.seq > state.startFloorSeq;
    });
  }

  return events.filter((event) => {
    if (lastStartedSeq > 0) {
      const boundary = planEventBoundary(event);
      if (boundary.seq > 0 && boundary.seq < lastStartedSeq) return false;
    }
    return !isStaleRunEvent(state.run, planEventBoundary(event));
  });
};

/**
 * Rebuilds node logs/results from the durable event log after a refresh or a
 * session switch, and reports how far the view is caught up so the live stream
 * can resume with `after_seq` instead of replaying everything.
 *
 * History replay only restores state: it never raises "plan completed" or
 * permission notices, never closes the live stream and never executes or
 * approves anything.
 */
export function usePlanHistoryHydrator(options: PlanHistoryHydratorOptions): PlanHistoryHydrator {
  const { identity, getMergeState, applyEvent, isCurrent } = options;
  const hydratedRef = useRef<Map<string, string>>(new Map());
  const alive = useCallback(() => isCurrent?.() ?? true, [isCurrent]);

  const applyHistory = useCallback(
    async (target: PlanHistoryTarget | null, historyOptions: PlanHistoryOptions = {}): Promise<PlanHistoryResult> => {
      // Capture both guards at call time: `isCurrent` (per call, owned by the
      // caller) must survive the whole request, and the merge state must still
      // be the one this call started on.
      const callGuard = historyOptions.isCurrent;
      const stillCurrent = () => (callGuard ? callGuard() : true) && alive();
      const state = getMergeState();
      const empty = (extra: Partial<PlanHistoryResult> = {}): PlanHistoryResult => ({
        appliedSeq: state.appliedSeq,
        applied: 0,
        failed: false,
        status: 0,
        authorizationFailed: false,
        canceled: false,
        ...extra,
      });
      if (!target?.id) return empty();
      const key = `${target.id}:${target.revision ?? 0}:${target.status ?? ''}`;
      if (!historyOptions.force && hydratedRef.current.get(target.id) === key) return empty();
      // A cursor only means something for the plan it belongs to.
      if (state.planId !== target.id) {
        state.planId = target.id;
        state.appliedSeq = 0;
        state.legacySeen.clear();
        state.terminalHandled = false;
        state.run = emptyRunBoundary();
      }

      let events: PlanEvent[];
      try {
        events = await getPlanEvents(target.id, identity);
      } catch (error) {
        // Keep the previous cursor: a failed history read must not look like a
        // completed hydration, otherwise the stream would skip those events.
        console.warn('[plan] failed to hydrate history', error);
        // A superseded call must not clear the current flow's cache entry.
        if (stillCurrent()) hydratedRef.current.delete(target.id);
        const status = readHttpStatus(error);
        return empty({
          failed: true,
          status,
          authorizationFailed: status === 401 || status === 403 || status === 404,
          canceled: !stillCurrent(),
        });
      }

      // The request may have outlived a plan, identity or run switch: another
      // generation's state must never be polluted by this result.
      if (!stillCurrent() || getMergeState() !== state || state.planId !== target.id) {
        return empty({ canceled: true });
      }

      let applied = 0;
      let canceled = false;
      for (const event of selectRunHistory(events, state)) {
        if (!stillCurrent()) {
          canceled = true;
          break;
        }
        const decision = mergePlanEvent(state, event, {
          live: false,
          apply: (pending) => applyEvent(pending, { notify: false }),
        });
        if (decision.accept) applied += 1;
      }
      if (canceled) {
        // Never cache a superseded read: the current flow must hydrate again.
        return empty({ applied, canceled: true });
      }

      if (!stillCurrent()) return empty({ applied, canceled: true });

      hydratedRef.current.set(target.id, key);
      return { appliedSeq: state.appliedSeq, applied, failed: false, status: 0, authorizationFailed: false, canceled: false };
    },
    [alive, applyEvent, getMergeState, identity],
  );

  const resetHistory = useCallback(() => {
    hydratedRef.current.clear();
  }, []);

  useEffect(
    () => () => {
      hydratedRef.current.clear();
    },
    [],
  );

  return { applyHistory, resetHistory };
}
