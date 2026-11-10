import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Node } from '@xyflow/react';
import type { ChatMessage, ExecuteTaskResultEvent, NodeExecutionState, PlanEvent, PlanGraph, Task } from '../../contracts/api';
import { PLAN_EVENTS, pickImageBase64 } from '../../contracts/events';
import {
  approvePlan,
  cancelPlan,
  createPlanEventSource,
  decideRuntimeAction,
  executePlan,
  executePlanTaskStream,
  getPlan,
  resumePlan,
  retryTask,
} from '../../services/api/researchAssistantApi';
import type { PlanExecutionTrigger, RequestIdentity } from '../../services/api/researchAssistantApi';
import { getDesktopBridge } from '../../services/desktopBridge';
import { usePlanHistoryHydrator } from './usePlanHistoryHydrator';
import {
  anchorRunFromSnapshot,
  createPlanMergeState,
  describeAuthorizationFailure,
  emptyRunBoundary,
  isAuthorizationFailureStatus,
  isObservablePlanStatus,
  isTerminalPlanStatus,
  markStartAccepted,
  mergePlanEvent,
  planBusinessState,
  planEventBoundary,
  planRunBoundaryFromSnapshot,
  readHttpStatus,
  shouldObservePlan,
} from './planObservation';
import type { PlanConnectionState, PlanMergeState } from './planObservation';
import {
  describeInterruptedStream,
  describeReconnect,
  describeResumedStream,
  eventSeq,
  nextBackoffMs,
  shouldReconnect,
} from './planStreamReconnect';
import { getTaskStyleByStatus } from '../../features/shared/agentVisuals';
import { createTaskNodeLabel } from '../../features/plan-graph/nodeLabelFactory';
import { uiText } from '../constants/uiText';

export type ExecutionDisplayMode =
  | 'logs'
  | 'report'
  | 'code'
  | 'plot'
  | 'evidence'
  | 'trials'
  | 'ablation'
  | 'report-expanded'
  | 'plot-expanded'
  | 'evidence-expanded'
  | 'trials-expanded'
  | 'ablation-expanded';

/**
 * Runtime execution state.
 *
 * The observation fields are optional so the runtime context mocks and the
 * canvas/sidebar views written before UI-02 keep compiling and working.
 * `connectionState` describes the *stream*, never the business plan status: a
 * dropped connection must not make a running plan look failed.
 */
export interface ExecutionState {
  selectedTask: Task | null;
  nodeStates: Record<string, NodeExecutionState>;
  displayMode: ExecutionDisplayMode;
  isExecuting: boolean;
  approvalResolved: boolean;
  /** Stream lifecycle: idle / connecting / live / reconnecting / interrupted. */
  connectionState?: PlanConnectionState;
  /** User-facing explanation for the current connection state. */
  connectionDetail?: string;
  /** This view received 202 for a start and must not submit another one. */
  startAccepted?: boolean;
  /** Latest server-side plan status this view observed. */
  serverPlanStatus?: string;
  planPaused?: boolean;
  observedPlanId?: string;
}

type ExecutionAction =
  | { type: 'select-task'; task: Task; state?: NodeExecutionState }
  | { type: 'close-task' }
  | { type: 'patch-task-state'; taskId: string; updater: (prev: NodeExecutionState) => NodeExecutionState }
  | { type: 'set-display-mode'; mode: ExecutionDisplayMode }
  | { type: 'set-executing'; value: boolean }
  | { type: 'set-approval-resolved'; value: boolean }
  | { type: 'observe-patch'; patch: Partial<ExecutionState> }
  | { type: 'clear-results' }
  | { type: 'reset'; isExecuting: boolean };

const initialNodeExecutionState: NodeExecutionState = { logs: '', result: '', code: '', structuredData: '', imageBase64: '' };
const reportAgents = new Set(['librarian_agent', 'data_agent', 'benchmark_agent', 'research_coding_agent']);
const isReportAgent = (assignedTo: string) => reportAgents.has(assignedTo);

const terminalNodeStatuses = new Set(['completed', 'failed', 'skipped', 'canceled']);

const updateNodeStatus = (node: Node, status: string, taskPatch: Partial<Task> = {}): Node => {
	const task = node.data.task as Task;
	if (!task) return node;
	const updatedTask = { ...task, ...taskPatch, Status: status };
	return {
		...node,
		data: {
			...node.data,
			status,
			task: updatedTask,
			label: createTaskNodeLabel({
				assignedTo: updatedTask.AssignedTo,
				taskName: updatedTask.Name,
				status,
				step: typeof node.data.step === 'number' ? node.data.step : undefined,
				task: updatedTask,
			}),
		},
		style: {
			...(node.style || {}),
			...getTaskStyleByStatus(status),
		},
	};
};

const updateNodeStatusIfActive = (node: Node, status: string): Node => {
	if (terminalNodeStatuses.has(String(node.data.status))) return node;
	return updateNodeStatus(node, status);
};

const detectBestDisplayMode = (task: Task, state: NodeExecutionState): ExecutionDisplayMode => {
  if (task.Type === 'claim_evidence_build' && state.structuredData) return 'evidence';
  if ((task.Type === 'autoresearch_run' || task.Type === 'autoresearch_validate' || task.Type === 'experiment_run' || task.Type === 'experiment_validate') && (state.structuredData || state.result)) return 'trials';
  if (task.Type === 'ablation_design' && (state.structuredData || state.result)) return 'ablation';
  if (state.imageBase64) return 'plot';
  if (state.code && !state.result) return 'code';
  if (state.result && isReportAgent(task.AssignedTo)) return 'report';
  return 'logs';
};

const executionStateFromTask = (task: Task): NodeExecutionState => ({
  logs: '',
  result: task.Result || '',
  code: task.Code || '',
  structuredData: task.StructuredData || '',
  imageBase64: task.ImageBase64 || '',
});

const PARAM_PREVIEW_LIMIT = 1200;

const previewParameterValue = (key: string, value: unknown): string => {
  if (value === undefined || value === null) return '';
  const keyLower = key.toLowerCase();
  if (typeof value === 'string' && (keyLower.includes('image') || keyLower.includes('base64'))) {
    return `<base64 image, ${value.length} chars>`;
  }

  const raw = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  if (!raw) return '';
  return raw.length > PARAM_PREVIEW_LIMIT ? `${raw.slice(0, PARAM_PREVIEW_LIMIT)}...[truncated]` : raw;
};

const formatParameterObject = (title: string, values: Record<string, unknown>): string => {
  const entries = Object.entries(values).filter(([, value]) => value !== undefined && value !== null && String(value).trim() !== '');
  if (entries.length === 0) return `${title}: 无`;
  return [
    `${title}:`,
    ...entries.map(([key, value]) => `- ${key}: ${previewParameterValue(key, value)}`),
  ].join('\n');
};

const formatArtifactSummaries = (title: string, rawArtifacts: unknown): string => {
  if (!Array.isArray(rawArtifacts) || rawArtifacts.length === 0) return `${title}: 无`;
  const lines = rawArtifacts.map((item) => {
    const artifact = item as Record<string, unknown>;
    const key = String(artifact.key || 'unknown');
    const type = artifact.type ? ` (${String(artifact.type)})` : '';
    const producer = artifact.producer_task_id ? ` <- ${String(artifact.producer_task_id)}` : '';
    return `- ${key}${type}${producer}: ${previewParameterValue(key, artifact.value_preview || '')}`;
  });
  return [`${title}:`, ...lines].join('\n');
};

const buildDirectExecutionOutputs = (result: string, code: string, structuredData: string, imageBase64: string): Record<string, unknown> => {
  const outputs: Record<string, unknown> = {};
  if (code) outputs.generated_code = code;
  if (result) outputs.result = result;
  if (structuredData) outputs.structured_data = structuredData;
  if (imageBase64) outputs.image_base64 = imageBase64;
  return outputs;
};

const INITIAL_EXECUTION_STATE: ExecutionState = {
  selectedTask: null,
  nodeStates: {},
  displayMode: 'logs',
  isExecuting: false,
  approvalResolved: false,
  connectionState: 'idle',
  connectionDetail: '',
  startAccepted: false,
  serverPlanStatus: '',
  planPaused: false,
  observedPlanId: '',
};

const executionReducer = (state: ExecutionState, action: ExecutionAction): ExecutionState => {
  switch (action.type) {
    case 'select-task': {
      const taskState = action.state ?? executionStateFromTask(action.task);
      return {
        ...state,
        selectedTask: action.task,
        displayMode: detectBestDisplayMode(action.task, taskState),
      };
    }
    case 'close-task':
      return {
        ...state,
        selectedTask: null,
        displayMode: 'logs',
      };
    case 'patch-task-state': {
      const prevTaskState = state.nodeStates[action.taskId] ?? initialNodeExecutionState;
      return {
        ...state,
        nodeStates: {
          ...state.nodeStates,
          [action.taskId]: action.updater(prevTaskState),
        },
      };
    }
    case 'set-display-mode':
      return { ...state, displayMode: action.mode };
    case 'set-executing':
      return { ...state, isExecuting: action.value };
    case 'set-approval-resolved':
      return { ...state, approvalResolved: action.value };
    case 'observe-patch':
      return { ...state, ...action.patch };
    case 'clear-results':
      // A new run (or a new plan) voids the previous run's logs and results.
      return { ...state, nodeStates: {} };
    case 'reset':
      // A session switch resets the *view*; the observation connection of the
      // freshly restored session is not touched by this action.
      return {
        ...state,
        selectedTask: null,
        nodeStates: {},
        displayMode: 'logs',
        isExecuting: action.isExecuting,
        approvalResolved: false,
      };
    default:
      return state;
  }
};

interface UseResearchAssistantRuntimeOptions {
  nodes: Node[];
  setNodes: Dispatch<SetStateAction<Node[]>>;
  appendChatMessage: (message: ChatMessage) => void;
  identity: RequestIdentity;
  refreshPlanState?: () => void | Promise<void>;
  activePlanGraph?: PlanGraph | null;
}

export interface ObservePlanOptions {
  /** This view just received 202 for a start of this plan. */
  startAccepted?: boolean;
  /** Drop the existing observation and rebuild it (manual reconnect / refocus). */
  force?: boolean;
}

/**
 * One observation generation for one (plan, identity, start) flow.
 *
 * Every asynchronous continuation — snapshot read, history read, ticket fetch,
 * queued EventSource callback, reconnect timer, resync — checks that its own
 * session is still the current one, so a plan or session switch can never be
 * polluted by the previous flow.
 */
interface ObserveSession {
  planId: string;
  identityKey: string;
  /** The identity this session's requests must use (never the current prop). */
  identity: RequestIdentity;
  /**
   * "This view received 202 for a start that is not confirmed yet."
   *
   * It only ever protects the *pending* window: the moment the server publishes
   * evidence that belongs to this start (a numbered `plan_started`, or a
   * snapshot running with a newer `started_at`) it is cleared, so a finished
   * run can be retried/started again from this view.
   */
  startAccepted: boolean;
  /** Run start observed when this view's start was accepted (identifies it). */
  acceptedBoundaryStartedAtMs: number;
  /** Cursor floor captured when this view's start was accepted. */
  acceptedFloorSeq: number;
  mergeState: PlanMergeState;
  source: EventSource | null;
  timer: ReturnType<typeof setTimeout> | null;
  /** Bounded "was the accepted start confirmed?" timer (never the SSE one). */
  confirmTimer: ReturnType<typeof setTimeout> | null;
  confirmAttempts: number;
  resyncPromise: Promise<void> | null;
  inflight: Promise<void> | null;
  attempts: number;
  /** An automatic reconnect is in flight but unproven (no open/event yet). */
  recovering: boolean;
  connectionToken: number;
  closed: boolean;
  terminalNotified: boolean;
  serverStatus: string;
  planPaused: boolean;
}

/** One view generation: plan, identity and selection at the moment of a call. */
interface ViewCapture {
  generation: number;
  planId: string;
  identityKey: string;
}

const identityKeyOf = (identity: RequestIdentity): string => `${identity.userId}::${identity.sessionId}`;

/** Bounded confirmation cadence for an accepted-but-unconfirmed start. */
const START_CONFIRM_INTERVAL_MS = 3_000;
const START_CONFIRM_MAX_QUERIES = 3;
const START_PENDING_UNCONFIRMED_DETAIL = '启动已受理，暂未确认进度，可重新连接。';
const START_PENDING_FAILED_DETAIL = '无法确认本次启动结果，进度更新暂停；请重新连接确认，不会重复启动。';

const TERMINAL_EVENT_STATUS: Record<string, string> = {
  [PLAN_EVENTS.PLAN_COMPLETED]: 'completed',
  [PLAN_EVENTS.PLAN_FAILED]: 'failed',
  [PLAN_EVENTS.PLAN_CANCELED]: 'canceled',
};

export function useResearchAssistantRuntime(options: UseResearchAssistantRuntimeOptions) {
	const { nodes, setNodes, appendChatMessage, identity, refreshPlanState, activePlanGraph } = options;
  // Latest option values for async continuations (they must never read a stale
  // closure from a previous render).
  const depsRef = useRef({ appendChatMessage, setNodes, refreshPlanState, identity });
  depsRef.current = { appendChatMessage, setNodes, refreshPlanState, identity };

  const sessionRef = useRef<ObserveSession | null>(null);
  const detachedMergeStateRef = useRef<PlanMergeState>(createPlanMergeState());
  const detachedMergeStateIdentityRef = useRef('');
  const observedRunRef = useRef<{ planId: string; identityKey: string; startedAtMs: number }>({
    planId: '',
    identityKey: '',
    startedAtMs: 0,
  });
  const directStreamGenerationRef = useRef(0);
  /**
   * View generation. Bumped whenever the observed plan/identity changes or the
   * current session is replaced, so a slow response captured *before* the bump
   * can never rebuild, cancel or patch the new view's state.
   */
  const viewGenerationRef = useRef(0);
  const activePlanIdRef = useRef('');
  const startRequestLockRef = useRef(false);
  const runtimeDecisionInFlightRef = useRef(false);

  const [executionState, dispatchExecution] = useReducer(executionReducer, INITIAL_EXECUTION_STATE);

  const patchExecution = useCallback((patch: Partial<ExecutionState>) => {
    dispatchExecution({ type: 'observe-patch', patch });
  }, []);

  /** The only liveness test: a session is used while it is open and current. */
  const isSessionLive = useCallback(
    (session: ObserveSession | null): session is ObserveSession =>
      Boolean(session) && !session!.closed && sessionRef.current === session,
    [],
  );

  /**
   * Capture the view (plan + identity + selection generation) at the moment a
   * request starts. Every async continuation re-checks it before touching the
   * view: a slow response for plan A must never re-observe A, close B or patch
   * B's nodes once the user moved to B.
   */
  const captureView = useCallback(
    (planId: string): ViewCapture => ({
      generation: viewGenerationRef.current,
      planId,
      identityKey: identityKeyOf(depsRef.current.identity),
    }),
    [],
  );

  const isCurrentView = useCallback((capture: ViewCapture): boolean => {
    if (capture.generation !== viewGenerationRef.current) return false;
    if (capture.identityKey !== identityKeyOf(depsRef.current.identity)) return false;
    const active = activePlanIdRef.current;
    if (active && active !== capture.planId) return false;
    const session = sessionRef.current;
    if (session && !session.closed && session.planId !== capture.planId) return false;
    return true;
  }, []);

  const selectedTaskState = useMemo(() => {
    if (!executionState.selectedTask) return initialNodeExecutionState;
    return executionState.nodeStates[executionState.selectedTask.ID] ?? executionStateFromTask(executionState.selectedTask);
  }, [executionState.selectedTask, executionState.nodeStates]);

  const patchNodeState = useCallback((taskId: string, updater: (prev: NodeExecutionState) => NodeExecutionState) => {
    dispatchExecution({ type: 'patch-task-state', taskId, updater });
  }, []);

  const appendNodeLog = useCallback(
    (taskId: string, line: string) => {
      patchNodeState(taskId, (prev) => ({
        ...prev,
        logs: prev.logs ? `${prev.logs}\n${line}` : line,
      }));
    },
    [patchNodeState],
  );

  const updateNodeVisualState = useCallback((taskId: string, status: string) => {
    depsRef.current.setNodes((nds) => nds.map((node) => (node.id === taskId ? updateNodeStatus(node, status) : node)));
  }, []);

  /**
   * The single state-merge path for history replay and for the live stream:
   * node status, logs, parameter summaries, results, images and verification all
   * land in the same view. `notify` (live only) is the one thing that separates
   * a restored state from a user-visible system message.
   */
  const applyPlanEventToView = useCallback(
    (event: PlanEvent, applyOptions: { notify: boolean }) => {
      const { setNodes: setNodesNow, appendChatMessage: appendMessage, refreshPlanState: refresh } = depsRef.current;
      const { notify } = applyOptions;

      if (event.task_id && event.task_status) {
        updateNodeVisualState(event.task_id, event.task_status);
      }

      if (event.event_type === PLAN_EVENTS.TASK_READY && event.task_id) {
        appendNodeLog(event.task_id, `[Plan] ready\n${formatArtifactSummaries('[Plan Params] 本次上游传入参数', event.payload?.inputs)}`);
      }
      if (event.event_type === PLAN_EVENTS.TASK_STARTED && event.task_id) {
        appendNodeLog(event.task_id, '[Plan] started');
      }
      if (event.event_type === PLAN_EVENTS.TASK_LOG && event.task_id) {
        appendNodeLog(event.task_id, String(event.payload?.message || ''));
      }
      if (event.event_type === PLAN_EVENTS.ARTIFACT_CREATED && event.task_id) {
        const keys = Array.isArray(event.payload?.artifact_keys) ? event.payload?.artifact_keys?.join(', ') : '';
        appendNodeLog(
          event.task_id,
          `[Plan] artifacts created${keys ? `: ${keys}` : ''}\n${formatArtifactSummaries('[Plan Params] 将传递给下游的参数', event.payload?.artifacts)}`,
        );
      }
      if (event.event_type === PLAN_EVENTS.TASK_BLOCKED && event.task_id) {
        const upstream = String(event.payload?.upstream_task_id || '');
        appendNodeLog(event.task_id, `[Plan] blocked${upstream ? ` by ${upstream}` : ''}`);
      }
      if (event.event_type === PLAN_EVENTS.TASK_COMPLETED && event.task_id) {
        const result = String(event.payload?.result || event.payload?.result_summary || '');
        const code = String(event.payload?.code || '');
        const structuredData = String(event.payload?.structured_data || '');
        const imageBase64 = pickImageBase64(event.payload);
        patchNodeState(event.task_id, (prev) => ({
          ...prev,
          logs: prev.logs ? `${prev.logs}\n[Plan] task_completed` : '[Plan] task_completed',
          result: result || prev.result || '',
          code: code || prev.code || '',
          structuredData: structuredData || prev.structuredData || '',
          imageBase64: imageBase64 || prev.imageBase64 || '',
        }));
        setNodesNow((current) => current.map((node) => node.id === event.task_id
          ? updateNodeStatus(node, 'completed', {
              Result: result || (node.data.task as Task | undefined)?.Result,
              Code: code || (node.data.task as Task | undefined)?.Code,
              StructuredData: structuredData || (node.data.task as Task | undefined)?.StructuredData,
            })
          : node));
      }
      if (event.event_type === PLAN_EVENTS.RUNTIME_ACTION_REQUESTED && notify) {
        const action = event.payload?.action as { title?: string; kind?: string } | undefined;
        appendMessage({ role: 'system', text: `需要你的运行权限确认：${action?.title || action?.kind || '重要操作'}。请在右侧计划面板允许或拒绝。` });
        void refresh?.();
      }
      if (event.event_type === PLAN_EVENTS.RUNTIME_ACTION_RESOLVED && notify) {
        void refresh?.();
      }
      if ((event.event_type === PLAN_EVENTS.VERIFICATION_COMPLETED || event.event_type === PLAN_EVENTS.VERIFICATION_FAILED) && event.task_id) {
        const verification = event.payload?.verification as Task['Verification'] | undefined;
        if (verification) {
          patchNodeState(event.task_id, (prev) => ({
            ...prev,
            logs: prev.logs ? `${prev.logs}\n[Plan] verification_${verification.status}` : `[Plan] verification_${verification.status}`,
          }));
          setNodesNow((current) => current.map((node) => node.id === event.task_id
            ? updateNodeStatus(node, String(node.data.status || 'completed'), {
                Verification: verification,
                Result: (node.data.task as Task | undefined)?.Result,
                Code: (node.data.task as Task | undefined)?.Code,
                StructuredData: (node.data.task as Task | undefined)?.StructuredData,
              })
            : node));
          if (notify) void refresh?.();
        }
      }
      if (event.event_type === PLAN_EVENTS.TASK_FAILED && event.task_id) {
        const errorText = String(event.payload?.error || 'Task failed');
        patchNodeState(event.task_id, (prev) => ({
          ...prev,
          logs: prev.logs ? `${prev.logs}\n[Plan Error] ${errorText}` : `[Plan Error] ${errorText}`,
        }));
      }
    },
    [appendNodeLog, patchNodeState, updateNodeVisualState],
  );

  // History and the live stream must share one merge state, otherwise a
  // reconnect either replays or skips events.
  const getMergeState = useCallback(
    (): PlanMergeState => sessionRef.current?.mergeState ?? detachedMergeStateRef.current,
    [],
  );

  const { applyHistory } = usePlanHistoryHydrator({
    identity,
    getMergeState,
    applyEvent: (event, applyOptions) => applyPlanEventToView(event, applyOptions),
  });

  const disposeSource = useCallback((session: ObserveSession) => {
    if (session.timer !== null) {
      clearTimeout(session.timer);
      session.timer = null;
    }
    session.source?.close();
    session.source = null;
  }, []);

  const clearConfirmTimer = useCallback((session: ObserveSession) => {
    if (session.confirmTimer !== null) {
      clearTimeout(session.confirmTimer);
      session.confirmTimer = null;
    }
  }, []);

  const closeActiveSession = useCallback(() => {
    const session = sessionRef.current;
    sessionRef.current = null;
    if (!session) return;
    session.closed = true;
    clearConfirmTimer(session);
    disposeSource(session);
  }, [clearConfirmTimer, disposeSource]);

  const notifyInterrupted = useCallback(
    (session: ObserveSession, detail: string) => {
      disposeSource(session);
      // The server-side run is untouched: keep the business state and never
      // reopen the start button.
      const stillRunning = isObservablePlanStatus(session.serverStatus) || session.startAccepted;
      patchExecution({
        connectionState: 'interrupted',
        connectionDetail: detail,
        isExecuting: stillRunning,
        startAccepted: session.startAccepted,
      });
      depsRef.current.appendChatMessage({ role: 'system', text: detail });
    },
    [disposeSource, patchExecution],
  );

  /**
   * Clear the local "this view's start is not confirmed yet" protection.
   *
   * Called only on real evidence about *this* run (a numbered `plan_started`
   * belonging to the accepted start, a snapshot running with a newer
   * `started_at`, or a terminal event of the current run). Never called because
   * of a lagging terminal snapshot of the previous run.
   */
  const clearAcceptedStart = useCallback(
    (session: ObserveSession) => {
      session.startAccepted = false;
      session.acceptedBoundaryStartedAtMs = 0;
      session.acceptedFloorSeq = 0;
      session.confirmAttempts = 0;
      clearConfirmTimer(session);
      patchExecution({ startAccepted: false });
    },
    [clearConfirmTimer, patchExecution],
  );

  /**
   * True when the snapshot itself proves the accepted start reached the server:
   * a *running* status (`in_progress` / `awaiting_*`) whose `started_at` is newer
   * than the run start observed when this view submitted the start.
   *
   * A terminal status never proves it: the only terminal snapshot visible
   * before this view's own run shows up belongs to the previous run.
   */
  const snapshotProvesAcceptedStart = useCallback(
    (session: ObserveSession, snapshot: PlanGraph | null): boolean => {
      if (!session.startAccepted) return false;
      if (!isObservablePlanStatus(String(snapshot?.status ?? ''))) return false;
      const startedAtMs = planRunBoundaryFromSnapshot(snapshot).startedAtMs;
      return startedAtMs > 0 && startedAtMs > session.acceptedBoundaryStartedAtMs;
    },
    [],
  );


  /**
   * True when the snapshot can only describe the *previous* run: this view
   * accepted a start but no evidence of it has appeared yet. Such a snapshot
   * must neither end the observation nor clear the pending protection.
   */
  const snapshotIsStaleForAcceptedStart = useCallback(
    (session: ObserveSession, snapshot: PlanGraph | null): boolean => {
      if (!session.startAccepted) return false;
      if (snapshotProvesAcceptedStart(session, snapshot)) return false;
      const status = String(snapshot?.status ?? '');
      if (isObservablePlanStatus(status)) return false;
      // Terminal / pending / blocked: the accepted run has not been observed yet.
      return true;
    },
    [snapshotProvesAcceptedStart],
  );


  /** Anchor the observed run to a snapshot and publish the business state. */
  const applySnapshotToSession = useCallback(
    (session: ObserveSession, snapshot: PlanGraph): boolean => {
      const status = String(snapshot?.status ?? '');
      const paused = Boolean(snapshot?.paused);
      const provesAcceptedStart = snapshotProvesAcceptedStart(session, snapshot);
      const staleForAcceptedStart = snapshotIsStaleForAcceptedStart(session, snapshot);
      anchorRunFromSnapshot(session.mergeState, {
        current_revision: snapshot?.current_revision,
        status,
        paused,
        usage: snapshot?.usage,
      });
      if (provesAcceptedStart) {
        // The snapshot itself proves this view's start reached the scheduler:
        // the local "accepted but unconfirmed" protection is no longer needed.
        clearAcceptedStart(session);
      }
      session.serverStatus = status;
      session.planPaused = paused;

      const previousRun = observedRunRef.current;
      if (
        previousRun.planId !== session.planId ||
        previousRun.identityKey !== session.identityKey ||
        session.mergeState.run.startedAtMs > previousRun.startedAtMs
      ) {
        // A new plan, a new identity or a new server-side run: old results void.
        session.terminalNotified = false;
        dispatchExecution({ type: 'clear-results' });
        observedRunRef.current = {
          planId: session.planId,
          identityKey: session.identityKey,
          startedAtMs: session.mergeState.run.startedAtMs,
        };
      }

      let observing = shouldObservePlan(status, { paused, startAccepted: session.startAccepted });
      if (staleForAcceptedStart) {
        // The accepted start has not shown up yet: keep waiting instead of
        // closing on the previous run's terminal status.
        observing = true;
        session.mergeState.terminalHandled = false;
      }
      patchExecution({
        observedPlanId: session.planId,
        serverPlanStatus: status,
        planPaused: paused,
        startAccepted: session.startAccepted,
        // A run that already reported its terminal event never becomes
        // "running" again because of a lagging snapshot.
        isExecuting: observing && !session.mergeState.terminalHandled,
      });
      return observing;
    },
    [clearAcceptedStart, patchExecution, snapshotIsStaleForAcceptedStart, snapshotProvesAcceptedStart],
  );

  const handleTerminal = useCallback(
    (session: ObserveSession, event: PlanEvent | null, statusOverride = '') => {
      if (!isSessionLive(session)) return;
      session.mergeState.terminalHandled = true;
      // A terminal of the *current* run ends this view's accepted start, so a
      // finished plan keeps its explicit "run again" entry.
      clearAcceptedStart(session);
      // Invalidate queued callbacks from the closed connection.
      session.connectionToken += 1;
      disposeSource(session);
      session.attempts = 0;
      const status = statusOverride || (event ? TERMINAL_EVENT_STATUS[String(event.event_type ?? '')] ?? '' : '');
      if (status) session.serverStatus = status;
      patchExecution({
        connectionState: 'idle',
        connectionDetail: '',
        isExecuting: false,
        serverPlanStatus: session.serverStatus,
        startAccepted: false,
      });
      if (event?.event_type === PLAN_EVENTS.PLAN_CANCELED) {
        depsRef.current.setNodes((current) => current.map((node) => updateNodeStatusIfActive(node, 'canceled')));
      }
      if (session.terminalNotified) return;
      session.terminalNotified = true;
      if (event?.event_type === PLAN_EVENTS.PLAN_COMPLETED || event?.event_type === PLAN_EVENTS.PLAN_FAILED) {
        const bridge = getDesktopBridge();
        if (bridge) {
          const completed = event.event_type === PLAN_EVENTS.PLAN_COMPLETED;
          void Promise.resolve()
            .then(() => bridge.notify(
              completed ? '计划已完成' : '计划失败',
              completed ? '计划已在后台完成。' : '计划执行失败，请打开详情查看原因。',
            ))
            .catch((error) => console.warn('[plan] desktop notification failed', error));
        }
      }
      if (event?.event_type === PLAN_EVENTS.PLAN_COMPLETED) {
        depsRef.current.appendChatMessage({ role: 'system', text: '整张拓扑图已经执行完成。' });
      } else if (event?.event_type === PLAN_EVENTS.PLAN_CANCELED) {
        depsRef.current.appendChatMessage({ role: 'system', text: '计划已取消，未完成任务已停止。' });
      } else if (event) {
        depsRef.current.appendChatMessage({
          role: 'system',
          text: `计划执行失败：${String(event.payload?.error || event.payload?.reason || '未知错误')}`,
        });
      }
      void depsRef.current.refreshPlanState?.();
      // Stop reconnecting, then read the final result set once.
      void (async () => {
        try {
          const snapshot = await getPlan(session.planId, session.identity);
          if (!isSessionLive(session)) return;
          applySnapshotToSession(session, snapshot);
        } catch (error) {
          console.warn('[plan] failed to refresh the final snapshot', error);
        }
      })();
    },
    [applySnapshotToSession, clearAcceptedStart, disposeSource, isSessionLive, patchExecution],
  );

  /** Read the final result set + apply the numbered history once. */
  const resyncFromServer = useCallback(
    async (session: ObserveSession) => {
      if (!isSessionLive(session)) return;
      const history = await applyHistory(
        { id: session.planId, status: session.serverStatus },
        { force: true, isCurrent: () => isSessionLive(session) },
      );
      if (!isSessionLive(session)) return;
      if (history.authorizationFailed) {
        notifyInterrupted(session, describeAuthorizationFailure(history.status));
        return;
      }
      let snapshot: PlanGraph;
      try {
        snapshot = await getPlan(session.planId, session.identity);
      } catch (error) {
        if (!isSessionLive(session)) return;
        const status = readHttpStatus(error);
        if (isAuthorizationFailureStatus(status)) notifyInterrupted(session, describeAuthorizationFailure(status));
        return;
      }
      if (!isSessionLive(session)) return;
      applySnapshotToSession(session, snapshot);
      if (isTerminalPlanStatus(snapshot.status)) handleTerminal(session, null, String(snapshot.status));
    },
    [applyHistory, applySnapshotToSession, handleTerminal, isSessionLive, notifyInterrupted],
  );

  const handlePlanEvent = useCallback(
    (session: ObserveSession, event: PlanEvent) => {
      if (!isSessionLive(session)) return null;
      return mergePlanEvent(session.mergeState, event, {
        live: true,
        // Apply first; the merge helper advances the cursor only afterwards.
        apply: (pending) => applyPlanEventToView(pending, { notify: true }),
        onRunChanged: () => {
          // A new run of the same plan starts from an empty result view.
          session.terminalNotified = false;
          dispatchExecution({ type: 'clear-results' });
          observedRunRef.current = {
            planId: session.planId,
            identityKey: session.identityKey,
            startedAtMs: session.mergeState.run.startedAtMs,
          };
        },
      });
    },
    [applyPlanEventToView, isSessionLive],
  );

  /** True when a numbered `plan_started` belongs to the accepted start. */
  const isAcceptedStartOpener = useCallback((session: ObserveSession, event: PlanEvent): boolean => {
    if (String(event.event_type ?? '').trim() !== 'plan_started') return false;
    const startedAtMs = planEventBoundary(event).startedAtMs;
    if (session.acceptedBoundaryStartedAtMs > 0 && startedAtMs > 0) {
      return startedAtMs > session.acceptedBoundaryStartedAtMs;
    }
    const seq = eventSeq(event);
    return seq > 0 && seq > session.acceptedFloorSeq;
  }, []);

  /**
   * Build the observation subscription for one session.
   *
   * `recovering` means an automatic reconnect is in flight: the connection only
   * becomes `live` — and the reconnect budget only resets — once the source
   * proves itself, either through a native `open` event or through a real plan
   * event. Resolving the ticket Promise proves nothing about the connection.
   */
  const openStream = useCallback(
    async (session: ObserveSession, recovering: boolean) => {
      if (!isSessionLive(session)) return;
      const token = (session.connectionToken += 1);
      const afterSeq = session.mergeState.appliedSeq;

      const markLive = (resumeSeq?: number) => {
        const wasRecovering = session.recovering || session.attempts > 0;
        session.recovering = false;
        session.attempts = 0;
        patchExecution({ connectionState: 'live', connectionDetail: '' });
        if (wasRecovering && recovering) {
          depsRef.current.appendChatMessage({
            role: 'system',
            text: describeResumedStream(resumeSeq ?? Math.max(0, Math.floor(afterSeq))),
          });
        }
      };

      if (recovering) session.recovering = true;

      let created: EventSource;
      try {
        created = await createPlanEventSource(
          session.planId,
          session.identity,
          {
            onPlanEvent: (event) => {
              // Ignore queued callbacks from a superseded plan/session/connection.
              if (!isSessionLive(session) || token !== session.connectionToken) return;

              const seq = eventSeq(event);
              // A real event is the strongest proof the connection is alive.
              markLive(Math.max(seq, session.mergeState.appliedSeq));

              // Evidence that this view's accepted start reached the scheduler:
              // the local pending protection is no longer needed, so a later
              // terminal of *this* run can end the observation normally.
              if (session.startAccepted && isAcceptedStartOpener(session, event)) clearAcceptedStart(session);

              const decision = handlePlanEvent(session, event);
              if (!decision) return;
              if (decision.resync) {
                // Zero/missing seq: never notifies, never moves the cursor; the
                // numbered history and the snapshot fill the gap.
                scheduleResyncRef.current?.(session);
                return;
              }
              if (decision.terminal) {
                void (async () => {
                  handleTerminal(session, event);
                  void depsRef.current.refreshPlanState?.();
                })();
              }
            },
            onError: () => {
              // A native EventSource error carries no HTTP status: never infer
              // an authorization failure (or anything else) from it.
              if (!isSessionLive(session) || token !== session.connectionToken) return;
              disposeSource(session);
              scheduleReconnectRef.current?.(session);
            },
          },
          // Tickets expire quickly, so every observation connection fetches a
          // fresh one; `after_seq` carries the applied cursor for the replay.
          { afterSeq: afterSeq > 0 ? afterSeq : undefined, forceTicket: true },
        );
      } catch (error) {
        if (!isSessionLive(session) || token !== session.connectionToken) return;
        const status = readHttpStatus(error);
        if (isAuthorizationFailureStatus(status)) {
          // Ticket 401/403/404: stop automatic retries with an explicit reason.
          notifyInterrupted(session, describeAuthorizationFailure(status));
          return;
        }
        scheduleReconnectRef.current?.(session);
        return;
      }

      if (!isSessionLive(session) || token !== session.connectionToken) {
        created.close();
        return;
      }
      session.source = created;

      // A created source is *not* a live connection: wait for the native open
      // event (or the first plan event) before claiming "live".
      if (typeof created?.addEventListener === 'function') {
        try {
          created.addEventListener('open', () => {
            if (!isSessionLive(session) || token !== session.connectionToken) return;
            if (session.mergeState.terminalHandled) return;
            markLive();
          });
        } catch {
          // A minimal EventSource stub without addEventListener is tolerated.
        }
      }
      if (!recovering) patchExecution({ connectionState: 'connecting', connectionDetail: '' });
    },
    [clearAcceptedStart, disposeSource, handlePlanEvent, handleTerminal, isAcceptedStartOpener, isSessionLive, notifyInterrupted, patchExecution],
  );

  const scheduleReconnect = useCallback(
    (session: ObserveSession) => {
      if (!isSessionLive(session)) return;
      if (session.mergeState.terminalHandled) return;
      if (session.timer !== null) return;
      const attempt = session.attempts;
      if (!shouldReconnect('', attempt)) {
        // Budget exhausted: keep the last snapshot, say so, keep the server-side
        // run and the run button closed.
        notifyInterrupted(session, describeInterruptedStream());
        return;
      }
      const nextAttempt = attempt + 1;
      session.attempts = nextAttempt;
      patchExecution({ connectionState: 'reconnecting', connectionDetail: describeReconnect(nextAttempt) });
      depsRef.current.appendChatMessage({ role: 'system', text: describeReconnect(nextAttempt) });
      session.timer = setTimeout(() => {
        session.timer = null;
        if (!isSessionLive(session)) return;
        void openStreamRef.current?.(session, true);
      }, nextBackoffMs(attempt));
    },
    [isSessionLive, notifyInterrupted, patchExecution],
  );

  const openStreamRef = useRef(openStream);
  openStreamRef.current = openStream;
  const scheduleReconnectRef = useRef(scheduleReconnect);
  scheduleReconnectRef.current = scheduleReconnect;

  const scheduleResync = useCallback(
    (session: ObserveSession) => {
      if (!isSessionLive(session) || session.resyncPromise) return;
      const promise = (async () => {
        try {
          await resyncFromServer(session);
        } catch (error) {
          console.error('[plan] resync failed', error);
        } finally {
          session.resyncPromise = null;
        }
      })();
      session.resyncPromise = promise;
    },
    [isSessionLive, resyncFromServer],
  );
  const scheduleResyncRef = useRef(scheduleResync);
  scheduleResyncRef.current = scheduleResync;

  const runObservation = useCallback(
    async (session: ObserveSession) => {
      patchExecution({
        observedPlanId: session.planId,
        connectionState: 'connecting',
        connectionDetail: '',
        startAccepted: session.startAccepted,
      });

      let snapshot: PlanGraph | null = null;
      try {
        snapshot = await getPlan(session.planId, session.identity);
      } catch (error) {
        if (!isSessionLive(session)) return;
        const status = readHttpStatus(error);
        if (isAuthorizationFailureStatus(status)) {
          const detail = describeAuthorizationFailure(status);
          patchExecution({ connectionState: 'interrupted', connectionDetail: detail });
          depsRef.current.appendChatMessage({ role: 'system', text: detail });
          return;
        }
        // Unreadable snapshot: keep a safe unknown state. Never "retry" by
        // executing the plan again.
        patchExecution({
          connectionState: 'interrupted',
          connectionDetail: '无法读取计划状态，进度更新暂停；可点击“重新连接”重试。',
        });
        return;
      }
      if (!isSessionLive(session)) return;

      const observing = snapshot ? applySnapshotToSession(session, snapshot) : session.startAccepted;

      // History replay restores state only: it never notifies, never closes the
      // stream and never executes anything. The per-call guard pins this read to
      // *this* session: a same-plan forced re-read from an earlier generation
      // must not advance the cursor or settle the cache of this flow.
      const history = await applyHistory(
        { id: session.planId, status: snapshot?.status ?? session.serverStatus, revision: snapshot?.current_revision },
        { force: true, isCurrent: () => isSessionLive(session) },
      );
      if (!isSessionLive(session) || history.canceled) return;
      if (history.authorizationFailed) {
        const detail = describeAuthorizationFailure(history.status);
        patchExecution({ connectionState: 'interrupted', connectionDetail: detail });
        depsRef.current.appendChatMessage({ role: 'system', text: detail });
        return;
      }
      if (history.failed) {
        // Keep the previous cursor: the stream replays from there.
        patchExecution({ connectionDetail: '历史事件读取失败，实时事件仍会继续。' });
      }

      if (!observing) {
        // Terminal / blocked / not started and not accepted here: no stream.
        patchExecution({ connectionState: 'idle', connectionDetail: '', isExecuting: false });
        return;
      }
      await openStreamRef.current?.(session, false);
    },
    [applyHistory, applySnapshotToSession, isSessionLive, patchExecution],
  );

  const createSession = useCallback((planId: string, startAccepted: boolean): ObserveSession => {
    const identityKey = identityKeyOf(depsRef.current.identity);
    const shared = detachedMergeStateRef.current;
    // The carried-over merge state is only reusable for the *same* plan *and*
    // the same identity: another identity's cursor/results must never leak in.
    const reusable =
      shared.planId === planId &&
      detachedMergeStateIdentityRef.current === identityKey &&
      shared.planId !== '';
    const mergeState = reusable ? shared : createPlanMergeState(planId);
    if (!reusable) {
      mergeState.planId = planId;
      mergeState.appliedSeq = 0;
      mergeState.run = emptyRunBoundary();
      mergeState.legacySeen.clear();
      mergeState.terminalHandled = false;
      mergeState.startPending = false;
      mergeState.startFloorSeq = 0;
      mergeState.startAcceptedAtMs = 0;
      mergeState.startBoundaryStartedAtMs = 0;
    }
    detachedMergeStateRef.current = mergeState;
    detachedMergeStateIdentityRef.current = identityKey;
    const session: ObserveSession = {
      planId,
      identityKey,
      identity: depsRef.current.identity,
      startAccepted,
      acceptedBoundaryStartedAtMs: startAccepted ? mergeState.run.startedAtMs : 0,
      acceptedFloorSeq: startAccepted ? mergeState.appliedSeq : 0,
      mergeState,
      source: null,
      timer: null,
      confirmTimer: null,
      confirmAttempts: 0,
      resyncPromise: null,
      inflight: null,
      attempts: 0,
      recovering: false,
      connectionToken: 0,
      closed: false,
      terminalNotified: false,
      serverStatus: '',
      planPaused: false,
    };
    if (startAccepted) markStartAccepted(mergeState);
    return session;
  }, []);

  /**
   * Verify the snapshot of an already-observed plan.
   *
   * With a live source this only re-reads the snapshot. Without one it runs the
   * *same* observation pipeline as a first observation (snapshot → numbered
   * history → subscribe), single-flight, so a pending plan that another client
   * started picks its stream up again. It never executes anything.
   */
  const resyncOrRefresh = useCallback(
    async (session: ObserveSession) => {
      if (!isSessionLive(session)) return;
      if (!session.source) {
        // No subscription yet (e.g. the plan was pending and is now running):
        // rebuild it through the shared hydration pipeline.
        await runSessionObservationRef.current?.(session);
        return;
      }
      try {
        const snapshot = await getPlan(session.planId, session.identity);
        if (!isSessionLive(session)) return;
        const observing = applySnapshotToSession(session, snapshot);
        if (isTerminalPlanStatus(snapshot.status)) {
          handleTerminal(session, null, String(snapshot.status));
          return;
        }
        if (!observing) {
          disposeSource(session);
          return;
        }
      } catch (error) {
        if (!isSessionLive(session)) return;
        const status = readHttpStatus(error);
        if (isAuthorizationFailureStatus(status)) notifyInterrupted(session, describeAuthorizationFailure(status));
      }
    },
    [
      applySnapshotToSession,
      disposeSource,
      handleTerminal,
      isSessionLive,
      notifyInterrupted,
    ],
  );

  /** Run the snapshot + history + stream flow on one session, single-flight. */
  const runSessionObservation = useCallback(
    async (session: ObserveSession) => {
      if (session.inflight) return session.inflight;
      const run = runObservation(session);
      session.inflight = run;
      try {
        await run;
      } finally {
        if (session.inflight === run) session.inflight = null;
      }
    },
    [runObservation],
  );
  const runSessionObservationRef = useRef(runSessionObservation);
  runSessionObservationRef.current = runSessionObservation;

  const observePlan = useCallback(
    async (planId: string, observeOptions: ObservePlanOptions = {}): Promise<void> => {
      if (!planId) return;
      const identityKey = identityKeyOf(depsRef.current.identity);
      const existing = sessionRef.current;

      if (!observeOptions.force && isSessionLive(existing) && existing.planId === planId && existing.identityKey === identityKey) {
        const session = existing;
        if (observeOptions.startAccepted && !session.startAccepted) {
          session.startAccepted = true;
          session.acceptedBoundaryStartedAtMs = session.mergeState.run.startedAtMs;
          session.acceptedFloorSeq = session.mergeState.appliedSeq;
          markStartAccepted(session.mergeState);
          patchExecution({ startAccepted: true, isExecuting: true });
        }
        // StrictMode, repeated focus and repeated refresh must not create a
        // parallel subscription for the same plan.
        if (session.inflight) return session.inflight;
        if (session.source) {
          await resyncOrRefresh(session);
          return;
        }
        // No stream yet (e.g. 202 accepted before the flow finished): continue
        // on the same session so its pre-request cursor floor is kept.
        await runSessionObservation(session);
        return;
      }

      // A new session supersedes every in-flight continuation of the old one.
      viewGenerationRef.current += 1;
      const carriedStart = observeOptions.startAccepted;
      closeActiveSession();
      const session = createSession(planId, Boolean(carriedStart));
      if (observeOptions.startAccepted && !session.startAccepted) session.startAccepted = true;
      if (session.startAccepted && !session.mergeState.startPending) markStartAccepted(session.mergeState);
      sessionRef.current = session;
      await runSessionObservation(session);
    },
    [
      closeActiveSession,
      createSession,
      isSessionLive,
      patchExecution,
      resyncOrRefresh,
      runSessionObservation,
    ],
  );

  const handleReconnect = useCallback(async (): Promise<void> => {
    const session = sessionRef.current;
    const planId = session?.planId || activePlanGraph?.id || '';
    if (!planId) return;
    // A manual reconnect never executes anything: it rebuilds the observation.
    // Only a start accepted *through this view's request* keeps the
    // "no second submit" flag; a run that was already running is simply
    // re-observed from the snapshot.
    const startAccepted = Boolean(session?.startAccepted);
    await observePlan(planId, { force: true, startAccepted });
  }, [activePlanGraph?.id, observePlan]);

  const observePlanRef = useRef(observePlan);
  observePlanRef.current = observePlan;
  const refreshSessionRef = useRef(resyncOrRefresh);
  refreshSessionRef.current = resyncOrRefresh;

  // Refresh, session switch, plan switch and plan-library open all land here.
  // The flow reads the snapshot + history and starts the stream; it never
  // executes, retries, resumes or approves anything.
  const activePlanGraphId = activePlanGraph?.id ?? '';
  const activePlanGraphStatus = activePlanGraph?.status ?? '';
  const identityKey = identityKeyOf(identity);

  useEffect(
    () => () => {
      // Leaving this plan (or switching identity) closes the connection and any
      // pending reconnect/confirmation timer of the abandoned flow, and voids
      // queued direct task-stream callbacks of the previous session.
      directStreamGenerationRef.current += 1;
      viewGenerationRef.current += 1;
      closeActiveSession();
    },
    [activePlanGraphId, identityKey, closeActiveSession],
  );

  useEffect(() => {
    activePlanIdRef.current = activePlanGraphId;
    viewGenerationRef.current += 1;
    if (!activePlanGraphId) {
      directStreamGenerationRef.current += 1;
      observedRunRef.current = { planId: '', identityKey: '', startedAtMs: 0 };
      // Switching to "no plan" must not keep another plan's results or the
      // previously selected task on screen.
      dispatchExecution({ type: 'reset', isExecuting: false });
      patchExecution({
        observedPlanId: '',
        connectionState: 'idle',
        connectionDetail: '',
        startAccepted: false,
        serverPlanStatus: '',
        planPaused: false,
      });
      return;
    }
    directStreamGenerationRef.current += 1;
    void observePlanRef.current(activePlanGraphId);
  }, [activePlanGraphId, identityKey, patchExecution]);

  // The workspace already refreshed the plan (revision executed, approval
  // handled elsewhere, ...): re-read the snapshot instead of rebuilding.
  useEffect(() => {
    if (!activePlanGraphId) return;
    const session = sessionRef.current;
    if (!session || session.planId !== activePlanGraphId) return;
    void refreshSessionRef.current(session);
  }, [activePlanGraphId, activePlanGraphStatus]);

  // Window refocus verifies the server state before anything else; it rebuilds
  // the stream only when the observation is not live.
  useEffect(() => {
    if (typeof window === 'undefined') return;
    const onFocus = () => {
      const session = sessionRef.current;
      if (!session) return;
      // Focus never creates a plan session of its own: it either verifies the
      // current one or rebuilds that same session's stream.
      void observePlanRef.current(session.planId, { startAccepted: session.startAccepted });
    };
    window.addEventListener('focus', onFocus);
    return () => window.removeEventListener('focus', onFocus);
  }, []);

  const querySnapshot = useCallback(async (planId: string): Promise<PlanGraph | null> => {
    try {
      return await getPlan(planId, depsRef.current.identity);
    } catch (error) {
      console.warn('[plan] failed to confirm the start against the server', error);
      return null;
    }
  }, []);

  /**
   * Bounded "was the accepted start confirmed?" polling.
   *
   * This is deliberately *not* the SSE reconnect timer: it exists only for a
   * 202 whose scheduler evidence never arrived, it is capped, and it never
   * executes anything. Running out of queries keeps the "start accepted, not
   * yet confirmed" lock so the run cannot be submitted twice.
   */
  const startConfirmTimerRef = useRef<((session: ObserveSession) => void) | null>(null);
  const scheduleStartConfirm = useCallback(
    (session: ObserveSession) => {
      if (!isSessionLive(session) || !session.startAccepted) return;
      if (session.confirmTimer !== null) return;
      if (session.confirmAttempts >= START_CONFIRM_MAX_QUERIES) return;
      session.confirmAttempts += 1;
      session.confirmTimer = setTimeout(() => {
        session.confirmTimer = null;
        if (!isSessionLive(session) || !session.startAccepted) return;
        void (async () => {
          const snapshot = await querySnapshot(session.planId);
          if (!isSessionLive(session) || !session.startAccepted) return;
          if (snapshot) {
            // Confirmed by evidence, or resolved as not running: the shared
            // snapshot path publishes the resulting business state.
            applySnapshotToSession(session, snapshot);
            if (!session.startAccepted) return;
            if (isTerminalPlanStatus(snapshot.status)) {
              handleTerminal(session, null, String(snapshot.status));
              return;
            }
          }
          if (session.confirmAttempts < START_CONFIRM_MAX_QUERIES) {
            scheduleStartConfirmRef.current?.(session);
            return;
          }
          // Out of queries and still unconfirmed: keep the run button closed and
          // hand the user an explicit "reconnect" entry instead of guessing.
          patchExecution({
            connectionState: 'interrupted',
            connectionDetail: snapshot ? START_PENDING_UNCONFIRMED_DETAIL : START_PENDING_FAILED_DETAIL,
            isExecuting: true,
            startAccepted: true,
          });
          depsRef.current.appendChatMessage({
            role: 'system',
            text: snapshot ? START_PENDING_UNCONFIRMED_DETAIL : START_PENDING_FAILED_DETAIL,
          });
        })();
      }, START_CONFIRM_INTERVAL_MS);
    },
    [applySnapshotToSession, handleTerminal, isSessionLive, patchExecution, querySnapshot],
  );
  const scheduleStartConfirmRef = useRef(scheduleStartConfirm);
  scheduleStartConfirmRef.current = scheduleStartConfirm;
  startConfirmTimerRef.current = scheduleStartConfirm;

  /** The 202 path: publish the accepted start everywhere it must be visible. */
  const markSessionStartAccepted = useCallback(
    (session: ObserveSession) => {
      session.startAccepted = true;
      session.acceptedBoundaryStartedAtMs = session.mergeState.run.startedAtMs;
      session.acceptedFloorSeq = session.mergeState.appliedSeq;
      markStartAccepted(session.mergeState);
      scheduleStartConfirmRef.current?.(session);
    },
    [],
  );

  /**
   * Shared 202 path for run / approve-and-run / retry: a synchronous ref lock
   * keeps a double click from submitting twice, `startAccepted` is published
   * before the request so a transient `pending` snapshot neither tears the
   * observation down nor reopens the run button.
   */
  const runStartRequests = useCallback(
    async (planId: string, request: () => Promise<void>, messages: { started: string; failed: string }) => {
      if (startRequestLockRef.current) return;
      const existing = sessionRef.current;
      if (existing && existing.planId === planId && existing.startAccepted) return;
      // The start belongs to the view that submitted it: capture it up front.
      const capture = captureView(planId);
      startRequestLockRef.current = true;
      dispatchExecution({ type: 'set-executing', value: true });
      if (existing && existing.planId === planId) {
        // Mark the accepted start on the *existing* session so its pre-request
        // cursor floor is preserved while the scheduler catches up.
        markSessionStartAccepted(existing);
      }
      patchExecution({ startAccepted: true, observedPlanId: planId, connectionState: 'connecting' });
      depsRef.current.appendChatMessage({ role: 'system', text: messages.started });

      try {
        await request();
        if (!isCurrentView(capture)) return;
        await observePlanRef.current(planId, { startAccepted: true });
        if (!isCurrentView(capture)) return;
        const session = sessionRef.current;
        if (session && session.planId === planId && session.startAccepted) {
          scheduleStartConfirmRef.current?.(session);
        }
      } catch (error) {
        console.error(error);
        if (!isCurrentView(capture)) return;
        // The start response is unclear: query the server before anything else.
        // Re-executing is never used as a probe.
        const snapshot = await querySnapshot(planId);
        if (!isCurrentView(capture)) return;
        // "Running" here means the *server* says so. `startAccepted` must not be
        // fed back into this test, or every status would look like a live run.
        const running = Boolean(snapshot) && isObservablePlanStatus(snapshot!.status);
        if (running) {
          await observePlanRef.current(planId, { startAccepted: true });
          return;
        }
        const status = readHttpStatus(error);
        const session = sessionRef.current;
        const sameSession = Boolean(session) && session!.planId === planId;
        if (status === 400 || status === 409) {
          if (sameSession && snapshot && !isObservablePlanStatus(snapshot.status)) {
            // An explicit rejection plus a snapshot that proves nothing is
            // running: the start really did not take, so the entry reopens.
            session!.startAccepted = false;
            session!.mergeState.startPending = false;
            clearConfirmTimer(session!);
            dispatchExecution({ type: 'set-executing', value: false });
            patchExecution({ startAccepted: false, connectionState: 'idle' });
            depsRef.current.appendChatMessage({ role: 'system', text: messages.failed });
            return;
          }
        }
        // Unknown outcome (a lost response, an unreadable snapshot): the start
        // may well have landed. Keep the accepted/unknown lock and let the
        // bounded confirmation timer or a manual reconnect settle it.
        if (sameSession) {
          markSessionStartAccepted(session!);
          patchExecution({
            connectionState: 'interrupted',
            connectionDetail: snapshot ? START_PENDING_UNCONFIRMED_DETAIL : START_PENDING_FAILED_DETAIL,
            isExecuting: true,
            startAccepted: true,
          });
        }
        if (!snapshot) {
          depsRef.current.appendChatMessage({ role: 'system', text: START_PENDING_FAILED_DETAIL });
          return;
        }
        depsRef.current.appendChatMessage({ role: 'system', text: messages.failed });
      } finally {
        startRequestLockRef.current = false;
      }
    },
    [captureView, clearConfirmTimer, isCurrentView, markSessionStartAccepted, patchExecution, querySnapshot],
  );

  const handleExecuteTask = useCallback(
    async (task: Task, planId: string | null) => {
      if (!planId) {
        depsRef.current.appendChatMessage({ role: 'system', text: '无法执行节点：当前会话没有关联的计划。' });
        return;
      }
      // Direct task streams keep working; their async callbacks are generation
      // bound so a plan/session switch cannot append into the new view. The task
      // keeps running server-side when the user navigates away; only its
      // *writes* are dropped.
      const capture = captureView(planId);
      const generation = directStreamGenerationRef.current;
      const stillCurrent = () => generation === directStreamGenerationRef.current && isCurrentView(capture);
      const { setNodes: setNodesNow, appendChatMessage: appendMessage } = depsRef.current;
      dispatchExecution({ type: 'set-executing', value: true });
      dispatchExecution({
        type: 'select-task',
        task,
        state: executionState.nodeStates[task.ID],
      });
      dispatchExecution({ type: 'set-display-mode', mode: 'logs' });

      const nodeInputs = task.Inputs ?? {};
      const initLog = `[System] 正在唤醒 ${task.AssignedTo}...\n[System] 正在通过 Eino 框架调用 DeepSeek 模型${
        isReportAgent(task.AssignedTo) ? '生成结构化结果' : '生成代码'
      }...\n\n${formatParameterObject('[Params] 计划内任务输入', nodeInputs)}\n`;
      patchNodeState(task.ID, () => ({ logs: initLog, result: '', code: '', structuredData: '', imageBase64: '' }));

      try {
        await executePlanTaskStream(
          planId,
          task.ID,
          identity,
          {
            onLog: (line) => {
              if (!stillCurrent()) return;
              patchNodeState(task.ID, (prev) => ({
                ...prev,
                logs: prev.logs ? `${prev.logs}\n${line}` : line,
              }));
            },
            onResult: (rawData) => {
              if (!stillCurrent()) return;
              let finalResult = rawData;
              let generatedCode = '';
              let structuredData = '';
              let imageBase64 = '';

              try {
                const parsed = JSON.parse(rawData) as ExecuteTaskResultEvent;
                if (parsed?.result) finalResult = parsed.result;
                if (parsed?.code) generatedCode = parsed.code;
                if (parsed?.structured_data) structuredData = parsed.structured_data;
                imageBase64 = pickImageBase64(parsed);
              } catch {
                // keep raw fallback
              }

              const directOutputs = buildDirectExecutionOutputs(finalResult, generatedCode, structuredData, imageBase64);

              patchNodeState(task.ID, (prev) => ({
                ...prev,
                logs: `${prev.logs || ''}\n\n${formatParameterObject('[Params] 将传递给下游的参数', directOutputs)}\n\n[exec] agent finished`,
                result: finalResult,
                code: generatedCode,
                structuredData,
                imageBase64,
              }));
              setNodesNow((current) => current.map((node) => node.id === task.ID
                ? updateNodeStatus(node, 'completed', {
                    Result: finalResult,
                    Code: generatedCode,
                    StructuredData: structuredData,
                    ImageBase64: imageBase64,
                  })
                : node));

              const taskActions: ('view_plot' | 'view_report')[] = [];
              if (imageBase64) taskActions.push('view_plot');
              if (finalResult && isReportAgent(task.AssignedTo)) {
                taskActions.push('view_report');
              }

              appendMessage({
                role: 'system',
                taskId: task.ID,
                text:
                  `节点 **[${task.Name}]** 已完成。完整结果可在右侧检查器的日志、报告与图表中查看。` +
                  (generatedCode
                    ? `\n\n**生成的代码片段:**\n\`\`\`python\n${generatedCode.substring(0, 300)}${
                        generatedCode.length > 300 ? '\n... (代码较长，请在右侧面板查看完整代码)' : ''
                      }\n\`\`\``
                    : ''),
                actions: taskActions.length > 0 ? taskActions : undefined,
              });

              if (task.Type === 'claim_evidence_build' && structuredData) {
                dispatchExecution({ type: 'set-display-mode', mode: 'evidence' });
              } else if ((task.Type === 'autoresearch_run' || task.Type === 'autoresearch_validate' || task.Type === 'experiment_run' || task.Type === 'experiment_validate') && (structuredData || finalResult)) {
                dispatchExecution({ type: 'set-display-mode', mode: 'trials' });
              } else if (imageBase64) dispatchExecution({ type: 'set-display-mode', mode: 'plot' });
              else if (isReportAgent(task.AssignedTo)) {
                dispatchExecution({ type: 'set-display-mode', mode: 'report' });
              }

              updateNodeVisualState(task.ID, 'completed');
            },
            onError: (errorMessage) => {
              throw new Error(errorMessage);
            },
          },
        );
      } catch (error: unknown) {
        console.error(error);
        if (!stillCurrent()) return;
        const message = error instanceof Error ? error.message : String(error);
        const errorMsg =
          message === 'Failed to fetch'
            ? '与后端连接中断：可能是模型响应超时或本地端口被占用。请稍后重试，或检查后端服务状态。'
            : message;

        patchNodeState(task.ID, (prev) => ({
          ...prev,
          logs: `${prev.logs}\n\n[error] 执行中断 ${errorMsg}`,
        }));
        appendMessage({
          role: 'system',
          text: `节点 **[${task.Name}]** 执行失败。\n\n**错误信息**\n\n${errorMsg}`,
        });
        updateNodeVisualState(task.ID, 'failed');
        throw error;
      } finally {
        // A completion that lands after the user moved to another plan must not
        // clear the *new* view's "executing" flag.
        if (stillCurrent()) dispatchExecution({ type: 'set-executing', value: false });
      }
    },
    [captureView, executionState.nodeStates, identity, isCurrentView, patchNodeState, updateNodeVisualState],
  );

  const canSubmitStart = useCallback((planId: string): boolean => {
    const session = sessionRef.current;
    if (!session || session.planId !== planId) return true;
    if (session.startAccepted) return false;
    const status = session.serverStatus;
    if (status === '') return true;
    if (session.planPaused) return false;
    const business = planBusinessState(status);
    // A finished plan keeps its explicit "run again" entry; running, waiting,
    // paused, review-gated and blocked plans never submit execute from here.
    return business === 'runnable' || business === 'terminal';
  }, []);

  const handleRunAllTasks = useCallback(
    async (activePlanId: string | null, trigger: PlanExecutionTrigger = 'manual') => {
      if (!activePlanId) {
        depsRef.current.appendChatMessage({ role: 'system', text: uiText.noPlanMessage });
        return;
      }
      if (!canSubmitStart(activePlanId)) return;
      await runStartRequests(
        activePlanId,
        async () => {
          await executePlan(activePlanId, identity, trigger);
        },
        { started: uiText.planStartMessage, failed: uiText.planStartFailedMessage },
      );
    },
    [canSubmitStart, identity, runStartRequests],
  );

  const handleApproveAndRun = useCallback(
    async (activePlanId: string | null) => {
      if (!activePlanId) return;
      const session = sessionRef.current;
      if (session && session.planId === activePlanId && session.startAccepted) return;
      // The approval and the execution it triggers belong to the view that
      // requested them: a slow approval of plan A must neither resolve plan B's
      // approval banner nor start B's run.
      const capture = captureView(activePlanId);
      await runStartRequests(
        activePlanId,
        async () => {
          await approvePlan(activePlanId, identity);
          if (!isCurrentView(capture)) return;
          dispatchExecution({ type: 'set-approval-resolved', value: true });
          await executePlan(activePlanId, identity);
        },
        { started: '计划已审批，开始执行。', failed: '计划审批或启动失败，请检查运行状态。' },
      );
    },
    [captureView, identity, isCurrentView, runStartRequests],
  );

  /**
   * Resume a paused plan.
   *
   * The backend keeps the run going, so this view only asks for the resume and
   * then re-observes the plan: it never executes, retries or approves anything
   * and never claims a start of its own (`startAccepted` stays untouched).
   */
  const resumeInFlightRef = useRef(false);
  const handleResumePlan = useCallback(
    async (activePlanId: string | null): Promise<void> => {
      if (!activePlanId) return;
      // Synchronous guard: a double click must send exactly one resume.
      if (resumeInFlightRef.current) return;
      // The resume belongs to the view it was made in: capture the selection
      // generation and identity up front and use that identity for the call.
      const capture = captureView(activePlanId);
      const capturedIdentity = depsRef.current.identity;
      resumeInFlightRef.current = true;
      try {
        await resumePlan(activePlanId, capturedIdentity);
        if (!isCurrentView(capture)) return;
        await observePlanRef.current(activePlanId);
        if (!isCurrentView(capture)) return;
        void depsRef.current.refreshPlanState?.();
      } catch (error) {
        console.error(error);
        if (!isCurrentView(capture)) return;
        depsRef.current.appendChatMessage({ role: 'system', text: '恢复计划失败，请刷新计划状态后重试。' });
      } finally {
        resumeInFlightRef.current = false;
      }
    },
    [captureView, isCurrentView],
  );

  const handleRuntimeActionDecision = useCallback(
    async (planId: string | null, actionId: string, decision: 'approve' | 'reject'): Promise<boolean> => {
      if (!planId || runtimeDecisionInFlightRef.current) return false;
      // The decision belongs to the view it was made in: a slow response must
      // not re-observe plan A once the user is looking at plan B.
      const capture = captureView(planId);
      runtimeDecisionInFlightRef.current = true;
      try {
        const action = await decideRuntimeAction(planId, actionId, decision, identity);
        if (!isCurrentView(capture)) return true;
        depsRef.current.appendChatMessage({
          role: 'system',
          text: decision === 'approve' ? `已允许运行操作：${action.title}。` : `已拒绝运行操作：${action.title}，计划已停止。`,
        });
        await depsRef.current.refreshPlanState?.();
        if (!isCurrentView(capture)) return true;
        // Continue observing only: deciding a runtime action never executes again.
        await observePlanRef.current(planId, { startAccepted: decision === 'approve' });
        return true;
      } catch (error) {
        console.error(error);
        if (!isCurrentView(capture)) return false;
        depsRef.current.appendChatMessage({ role: 'system', text: '运行权限审批失败，请刷新计划状态后重试。' });
        return false;
      } finally {
        runtimeDecisionInFlightRef.current = false;
      }
    },
    [captureView, identity, isCurrentView],
  );

  const handleCancelPlan = useCallback(
    async (activePlanId: string | null) => {
      if (!activePlanId) return;
      // Cancel is bound to the plan it was requested for: a slow cancel of A
      // must not close B's observation or mark B's nodes canceled.
      const capture = captureView(activePlanId);
      try {
        await cancelPlan(activePlanId, identity);
        if (!isCurrentView(capture)) return;
        const session = sessionRef.current;
        if (session && session.planId === activePlanId) {
          session.startAccepted = false;
          clearConfirmTimer(session);
        }
        closeActiveSession();
        depsRef.current.setNodes((current) => current.map((node) => updateNodeStatusIfActive(node, 'canceled')));
        dispatchExecution({ type: 'set-executing', value: false });
        patchExecution({
          connectionState: 'idle',
          connectionDetail: '',
          startAccepted: false,
          serverPlanStatus: 'canceled',
        });
        depsRef.current.appendChatMessage({ role: 'system', text: '计划已取消，未完成任务已停止。' });
      } catch (error) {
        console.error(error);
        if (!isCurrentView(capture)) return;
        depsRef.current.appendChatMessage({ role: 'system', text: '取消计划失败，请刷新计划状态后重试。' });
      }
    },
    [captureView, clearConfirmTimer, closeActiveSession, identity, isCurrentView, patchExecution],
  );

  const handleRetryFailedPlan = useCallback(
    async (activePlanId: string | null) => {
      if (!activePlanId) return;
      const failedNodes = nodes.filter((node) => node.data.status === 'failed');
      if (failedNodes.length === 0) return;
      const session = sessionRef.current;
      if (session && session.planId === activePlanId && session.startAccepted) return;
      // Each retry request and the node/execute mutation that follows it belong
      // to the view that started them: once the user moved to plan B, an
      // approval-style continuation must stop instead of touching B.
      const capture = captureView(activePlanId);
      await runStartRequests(
        activePlanId,
        async () => {
          for (const failedNode of failedNodes) {
            await retryTask(activePlanId, failedNode.id, identity);
            if (!isCurrentView(capture)) return;
          }
          if (!isCurrentView(capture)) return;
          depsRef.current.setNodes((current) =>
            current.map((node) =>
              ['failed', 'blocked', 'canceled'].includes(String(node.data.status))
                ? updateNodeStatus(node, 'pending')
                : node,
            ),
          );
          await executePlan(activePlanId, identity);
        },
        { started: '失败节点已重置，计划开始重试。', failed: '重试计划失败，请检查节点状态。' },
      );
    },
    [captureView, identity, isCurrentView, nodes, runStartRequests],
  );

  const onNodeClick = useCallback(
    (_: unknown, node: Node) => {
      const taskData = node.data.task as Task;
      if (!taskData) return;
      dispatchExecution({
        type: 'select-task',
        task: taskData,
        state: executionState.nodeStates[taskData.ID],
      });
    },
    [executionState.nodeStates],
  );

  const handleOpenTaskView = useCallback(
    (taskId: string, mode: 'plot' | 'report') => {
      const targetNode = nodes.find((n) => n.id === taskId);
      if (!targetNode) return;
      onNodeClick(null, targetNode);
      dispatchExecution({ type: 'set-display-mode', mode });
    },
    [nodes, onNodeClick],
  );

  const appendSelectedTaskLog = useCallback(
    (line: string) => {
      if (!executionState.selectedTask) return;
      appendNodeLog(executionState.selectedTask.ID, line);
    },
    [appendNodeLog, executionState.selectedTask],
  );

  useEffect(
    () => () => {
      closeActiveSession();
    },
    [closeActiveSession],
  );

  const setDisplayMode = useCallback((mode: ExecutionDisplayMode) => {
    dispatchExecution({ type: 'set-display-mode', mode });
  }, []);

  const closeTaskPanel = useCallback(() => {
    dispatchExecution({ type: 'close-task' });
  }, []);

  const resetRuntimeState = useCallback(() => {
    // A session switch resets the view only. The observation of the newly
    // restored session must survive this call: it is neither closed nor reset
    // to "not running".
    directStreamGenerationRef.current += 1;
    const session = sessionRef.current;
    const keepRunning =
      Boolean(session) &&
      !session!.closed &&
      (session!.source !== null || session!.startAccepted || isObservablePlanStatus(session!.serverStatus));
    dispatchExecution({ type: 'reset', isExecuting: keepRunning });
  }, []);

  return {
    executionState,
    selectedTaskState,
    onNodeClick,
    handleOpenTaskView,
    handleExecuteTask,
    handleRunAllTasks,
	handleApproveAndRun,
	handleResumePlan,
	handleRuntimeActionDecision,
	handleCancelPlan,
	handleRetryFailedPlan,
    appendSelectedTaskLog,
    setDisplayMode,
    closeTaskPanel,
    resetRuntimeState,
    observePlan,
    handleReconnect,
  };
}
