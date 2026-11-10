import type { PlanEvent, PlanGraph } from '../../contracts/api';
import {
  getPlan,
  getPlanEvents,
  getRevision,
  listRevisions,
  type RequestIdentity,
} from '../../services/api/researchAssistantApi';
import { formFromGraph, formSignature } from './reviewDraft';

/**
 * Event types that prove a revision execution was accepted by the server.
 * `plan_completed` / `plan_failed` / `plan_canceled` are deliberately absent:
 * a terminal event from an earlier round must never be read as "this submit
 * started the plan".
 */
export const EXECUTION_START_EVENT_TYPES: ReadonlyArray<string> = ['plan_revision_executed', 'plan_started'];

/** Business statuses that mean the target revision is actually being run. */
export const ACTIVE_PLAN_STATUSES: ReadonlyArray<string> = ['in_progress', 'awaiting_input', 'awaiting_approval'];

export interface EventBoundary {
  /**
   * Highest seq the server had already published when the submit was made, as
   * observed by the client. Meaningful only when `observed` is true.
   */
  seq: number;
  /**
   * True when the pre-submit history read actually succeeded. An observed read
   * that returned no events proves the history was empty, so the first
   * *numbered* event afterwards is newer. A failed read proves nothing.
   */
  observed: boolean;
  /** Client timestamp, kept as diagnostic detail only; never decides recency. */
  timestamp: string;
}

export const eventSeqOf = (event: unknown): number => {
  if (!event || typeof event !== 'object') return 0;
  const raw = (event as { seq?: unknown }).seq;
  const value = typeof raw === 'number' ? raw : typeof raw === 'string' && raw.trim() !== '' ? Number(raw) : Number.NaN;
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.floor(value);
};

export const maxEventSeq = (events: ReadonlyArray<unknown>): number =>
  events.reduce<number>((max, event) => Math.max(max, eventSeqOf(event)), 0);

export const eventRevisionOf = (event: PlanEvent): number => {
  const topLevel = (event as unknown as { revision?: unknown }).revision;
  const fromPayload = event.payload ? event.payload.revision : undefined;
  const raw = topLevel ?? fromPayload;
  const value = typeof raw === 'number' ? raw : typeof raw === 'string' ? Number(raw) : Number.NaN;
  return Number.isFinite(value) ? Math.floor(value) : 0;
};

export const isExecutionStartEvent = (event: PlanEvent): boolean =>
  EXECUTION_START_EVENT_TYPES.includes(String(event.event_type ?? '').trim());

/**
 * Recency is decided by the server-assigned `seq` alone, and only a history
 * read that actually succeeded can prove anything.
 *
 * A client timestamp cannot establish ordering: client and server clocks
 * drift, so an old event from an earlier round would be read as this submit.
 * Zero `seq` (legacy events published without a number) must never unlock a
 * retry either. When the pre-submit read failed, or either side lacks a
 * positive seq, the answer is "cannot prove it is newer", which leaves the
 * outcome unknown. An *observed* empty history (seq 0, observed) does prove
 * that any later numbered event is new.
 */
export const isAfterBoundary = (event: PlanEvent, boundary: EventBoundary): boolean => {
  if (!boundary.observed) return false;
  const seq = eventSeqOf(event);
  if (seq <= 0) return false;
  if (boundary.seq <= 0) return true;
  return seq > boundary.seq;
};

/**
 * Record the pre-submit boundary so an unknown outcome can be reconciled
 * without mistaking an older event for this submit.
 */
export const captureEventBoundary = async (
  planId: string,
  identity?: RequestIdentity,
): Promise<EventBoundary> => {
  const timestamp = new Date().toISOString();
  if (!identity) return { seq: 0, observed: false, timestamp };
  try {
    const events = await getPlanEvents(planId, identity);
    return { seq: maxEventSeq(events), observed: true, timestamp };
  } catch {
    // A failed read is not an empty history: nothing about recency is proven.
    return { seq: 0, observed: false, timestamp };
  }
};

export type ReconcileState = 'started' | 'not_started' | 'unknown';

export interface ExecutionObservation {
  state: ReconcileState;
  detail: string;
}

export interface ReconcileExecutionArgs {
  planId: string;
  revision: number;
  boundary: EventBoundary;
  identity?: RequestIdentity;
}

/**
 * Resolve an unknown execution outcome from the server.
 *
 * The result only becomes `started` on positive evidence: a *numbered* start
 * event for this revision published after the boundary, or a plan that is
 * actually active on this revision. It only becomes `not_started` when the
 * server shows the plan running something else — the one case where a retry
 * cannot duplicate a start.
 *
 * A `pending`/`ready` snapshot proves nothing: the scheduler may not have
 * flipped the status yet, so that path stays `unknown` and the panel keeps the
 * re-execute locked. Zero-seq events are likewise `unknown`, and a terminal
 * event from an earlier round of the same revision is never read as this start.
 */
export const reconcileExecution = async ({
  planId,
  revision,
  boundary,
  identity,
}: ReconcileExecutionArgs): Promise<ExecutionObservation> => {
  if (!identity) {
    return { state: 'unknown', detail: '缺少请求身份，无法核对服务端状态。' };
  }
  let plan: PlanGraph;
  let events: PlanEvent[];
  try {
    plan = await getPlan(planId, identity);
    events = await getPlanEvents(planId, identity);
  } catch (error) {
    const detail = error instanceof Error ? error.message : '核对服务端状态失败';
    return { state: 'unknown', detail };
  }
  const startEvent = events.find(
    (event) => isExecutionStartEvent(event) && eventRevisionOf(event) === revision && isAfterBoundary(event, boundary),
  );
  if (startEvent) {
    return { state: 'started', detail: `发现提交后的 ${startEvent.event_type} 事件（r${revision}）。` };
  }
  const status = String(plan.status ?? '');
  const planRevision = Number(plan.current_revision ?? 0) || 0;
  if (ACTIVE_PLAN_STATUSES.includes(status)) {
    if (planRevision === revision) {
      return { state: 'started', detail: `计划当前为 ${status}，执行版本为 r${planRevision}。` };
    }
    if (planRevision > 0) {
      return { state: 'not_started', detail: `计划正在运行的是 r${planRevision}，不是 r${revision}。` };
    }
    return { state: 'unknown', detail: `计划当前为 ${status}，但无法确认执行版本是否为 r${revision}。` };
  }
  const unnumberedStart = events.find(
    (event) =>
      isExecutionStartEvent(event) && eventRevisionOf(event) === revision && eventSeqOf(event) <= 0,
  );
  if (unnumberedStart) {
    return {
      state: 'unknown',
      detail: `发现 r${revision} 的启动事件，但没有可用的 seq，无法证明它属于本次提交。`,
    };
  }
  return {
    state: 'unknown',
    detail: `计划当前为 ${status || '未知状态'}，未发现提交后有编号的启动事件；快照可能尚未更新。`,
  };
};

export type SaveReconcileState = 'found' | 'absent' | 'unknown';
export interface SaveObservation {
  state: SaveReconcileState;
  revision?: number;
  detail: string;
}

export interface ReconcileSaveArgs {
  planId: string;
  /** Revision the save was based on; only newer revisions can match. */
  baseRevision: number;
  /** Canonical signature of the local draft that was submitted. */
  signature: string;
}

/**
 * Look for a revision the server may already have created from an unknown save
 * response. Matching is by content, never by "latest number", so a revision
 * created by someone else is never adopted as this draft.
 */
export const reconcileSaveOutcome = async ({
  planId,
  baseRevision,
  signature,
}: ReconcileSaveArgs): Promise<SaveObservation> => {
  let candidates: number[] = [];
  try {
    const payload = await listRevisions(planId);
    candidates = (payload.revisions ?? [])
      .map((entry) => entry.revision)
      .filter((value) => Number.isFinite(value) && value > baseRevision)
      .sort((left, right) => right - left);
  } catch (error) {
    return { state: 'unknown', detail: error instanceof Error ? error.message : '读取版本列表失败' };
  }
  let unreadable = 0;
  for (const revision of candidates) {
    try {
      const detail = await getRevision(planId, revision);
      if (detail?.graph && formSignature(formFromGraph(detail.graph)) === signature) {
        return { state: 'found', revision, detail: `服务端已有内容一致的 r${revision}。` };
      }
    } catch {
      // A single unreadable revision must not abort the whole comparison, but
      // it does mean "absent" cannot be claimed: that revision may be ours.
      unreadable += 1;
    }
  }
  if (unreadable > 0) {
    return {
      state: 'unknown',
      detail: `有 ${unreadable} 个新版本无法读取，不能确认本地草稿是否已保存。`,
    };
  }
  return { state: 'absent', detail: '服务端未发现与本地内容一致的新版本。' };
};

export type ApprovalObservation = 'approved' | 'not_approved' | 'unknown';

export interface ReconcileApprovalArgs {
  planId: string;
  revision: number;
}

/**
 * Resolve an unknown approval outcome. A 5xx answers nothing about whether the
 * review gate moved, so the only safe next step is to read the server's own
 * record instead of approving again.
 */
export const reconcileApproval = async ({
  planId,
  revision,
}: ReconcileApprovalArgs): Promise<{ state: ApprovalObservation; detail: string }> => {
  try {
    const summaries = await listRevisions(planId);
    const entry = (summaries.revisions ?? []).find((candidate) => candidate.revision === revision);
    if (!entry) {
      return { state: 'unknown', detail: `版本列表中没有 r${revision}。` };
    }
    const status = String(entry.status ?? '');
    if (status === 'approved' || status === 'executing' || status === 'completed') {
      return { state: 'approved', detail: `服务端已把 r${revision} 记为 ${status}。` };
    }
    if (status === 'draft' || status === 'rejected') {
      return { state: 'not_approved', detail: `服务端 r${revision} 仍为 ${status}。` };
    }
    return { state: 'unknown', detail: `服务端 r${revision} 状态为 ${status}，无法判断批准是否生效。` };
  } catch (error) {
    return { state: 'unknown', detail: error instanceof Error ? error.message : '读取版本状态失败' };
  }
};
