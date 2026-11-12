import { lazy, memo, Suspense, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { Ban, ChevronDown, Eye, MessageSquare, MoreHorizontal, Play, RefreshCw, RotateCcw, ShieldCheck, Square, WifiOff, X } from 'lucide-react';
import type { IntentContext, MissingRequirement, PlanQuestion, RuntimeAction, Verification } from '../../contracts/api';
import { describeConnectionState, planBusinessState } from '../../app/hooks/planObservation';
import type { PlanBusinessState, PlanConnectionState } from '../../app/hooks/planObservation';
import type { OutcomeDetails } from '../../contracts/api';
import { summarizePlanOutcome } from '../shared/planOutcome';
import { VerificationBadge } from '../verification/VerificationBadge';

const GraphCanvas = lazy(() => import('./GraphCanvas'));

interface GraphPanelProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  onNodeClick: (_: unknown, node: Node) => void;
  intentContext: IntentContext | null;
  outcome?: string | null;
  /** Explains a terminal outcome: accepted core goals and optional losses. */
  outcomeDetails?: OutcomeDetails | null;
  missingRequirements?: MissingRequirement[];
  runtimeActions?: RuntimeAction[];
  questions?: PlanQuestion[];
  onAnswerQuestion?: (questionId: string, answer: { answer?: string; answer_option?: string }) => Promise<void> | void;
  runtimePermissionMode?: string | null;
  plannerSource?: string | null;
  plannerFallbackReason?: string | null;
  complexity?: string | null;
  planningRoute?: string | null;
  templateVariant?: string | null;
  runTrigger?: 'manual' | 'auto' | '' | null;
  verification?: Verification | null;
  onOpenModelSettings?: () => void;
  onRuntimeActionDecision?: (actionId: string, decision: 'approve' | 'reject') => Promise<boolean | void> | boolean | void;
  runAllText: string;
  graphTitle: string;
  graphHint: string;
  planId?: string | null;
  planStatus?: string | null;
  isExecuting: boolean;
  requiresApproval: boolean;
  requiresReview?: boolean;
  onOpenReview?: () => void;
  onRunAll: () => void;
  onApproveAndRun: () => void;
  onCancel: () => void;
  onRetryFailed: () => void;
  /**
   * Resume an already-started paused plan. Absent when the runtime has no
   * backend-permitted resume action; a paused plan then only offers cancel.
   */
  onResume?: () => void;
  onOpenTask?: (taskId: string) => void;
  /** Primary action gate computed by the view model from the business status. */
  canRun?: boolean;
  /** Local "execute returned 202" flag: the run must not be submitted again. */
  startAccepted?: boolean;
  /** Pause is an independent flag, not a plan status. */
  planPaused?: boolean;
  connectionState?: PlanConnectionState;
  connectionDetail?: string;
  /** Rebuild the observation flow only; never executes the plan. */
  onReconnect?: () => void;
}

const intentLabels: Record<string, string> = {
  Paper_Reproduction: '论文复现',
  Framework_Evaluation: '框架对比',
  Framework_Comparison: '框架对比',
  Code_Execution: '代码执行',
  AutoResearch: '自动研究',
  Custom_Benchmark: '数据评测',
  General: '通用任务',
};

const outcomeLabels: Record<string, string> = {
  completed: '完成',
  partial: '部分完成',
  smoke: '冒烟',
  mock: '模拟',
  blocked: '阻塞',
  failed: '失败',
  needs_review: '待复核',
};

const terminalNodeStatuses = new Set(['completed', 'failed', 'skipped', 'canceled']);

const formatRuntimeActionBytes = (bytes?: number): string => {
  if (!bytes || bytes <= 0) return '';
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(0)} MB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
};

const describeRuntimeAction = (action: RuntimeAction): string => {
  const details = action.details ?? {};
  const parts: string[] = [];
  if (typeof details.image === 'string' && details.image.trim() !== '') {
    const registry = typeof details.registry === 'string' && details.registry !== 'docker.io' ? ` (${details.registry})` : '';
    parts.push(`镜像 ${details.image}${registry}`);
  }
  if (Array.isArray(details.packages) && details.packages.length > 0) {
    parts.push(`依赖 ${details.packages.map(String).join('、')}`);
  }
  if (typeof details.gpu_type === 'string' && details.gpu_type.trim() !== '') {
    parts.push(`GPU ${details.gpu_type}`);
  }
  if (typeof details.cuda === 'string' && details.cuda.trim() !== '') {
    parts.push(`CUDA ${details.cuda}`);
  }
  if (details.network_access === true) {
    parts.push('运行阶段访问网络');
  }
  const estimatedBytes = formatRuntimeActionBytes(action.estimated_bytes);
  if (estimatedBytes) parts.push(`预计下载 ${estimatedBytes}`);
  if (typeof action.estimated_sec === 'number' && action.estimated_sec > 0) parts.push(`预计 ${action.estimated_sec}s`);
  return parts.join(' · ');
};

const runTriggerLabel = (trigger: 'manual' | 'auto' | '' | null | undefined) =>
  trigger === 'auto' ? '自动运行' : trigger === 'manual' ? '手动运行' : '未运行';

/**
 * Business label for the canvas pill. Derived from the server snapshot through
 * `planBusinessState`, so `blocked`/`paused`/`unknown` never fall back to
 * "待运行"; `unknown` stays explicitly unknown instead of pretending to be
 * runnable.
 */
const planStateLabelFor = (
  state: PlanBusinessState,
  requiresReview: boolean | undefined,
  requiresApproval: boolean | undefined,
  hasNodes: boolean,
  planStatus?: string | null,
  outcome?: string | null,
): string => {
  if (requiresReview || state === 'review') return '待审阅';
  if (requiresApproval || state === 'awaiting_approval') return '待批准';
  switch (state) {
    case 'observing':
      return '运行中';
    case 'awaiting_input':
      return '待回答';
    case 'paused':
      return '已暂停';
    case 'blocked':
      return '已阻塞';
    case 'terminal':
      return terminalStateLabel(planStatus, outcome);
    case 'runnable':
      return hasNodes ? '待运行' : '未开始';
    default:
      return '未知状态';
  }
};

/** Outcome/terminal labels (`outcome` already carries the fine-grained value). */
const terminalStateLabel = (planStatus: string | null | undefined, outcome?: string | null): string => {
  if (outcome && outcomeLabels[outcome]) return outcomeLabels[outcome];
  switch (String(planStatus ?? '').trim()) {
    case 'completed':
      return '已完成';
    case 'failed':
      return '失败';
    case 'canceled':
      return '已取消';
    default:
      return '已结束';
  }
};

/**
 * Concrete help line for the current business state, so the canvas explains why
 * the primary action is what it is. Wording stays user-facing: no stream/API/
 * snapshot implementation details, only what the user can decide next.
 */
const businessHelpText = (
  state: PlanBusinessState,
  options: {
    requiresReview?: boolean;
    requiresApproval?: boolean;
    startAccepted?: boolean;
    hasPendingRuntimeActions?: boolean;
    planStatus?: string | null;
    outcome?: string | null;
    connectionInterrupted?: boolean;
  },
): string => {
  const body = (): string => {
    if (options.startAccepted) {
      return '正在确认启动，请稍候。';
    }
    if (options.requiresReview || state === 'review') {
      return '计划还在审阅中，审阅通过后才会开始运行；请先打开审阅处理意见。';
    }
    if (options.requiresApproval || state === 'awaiting_approval') {
      return options.hasPendingRuntimeActions
        ? '计划在等你确认一项运行授权；请在上面的“需要你处理”里批准或拒绝。'
        : '计划正在等待审批，批准后会立即开始（或继续）运行。';
    }
    switch (state) {
      case 'observing':
        return '计划正在运行，进度会自动更新。';
      case 'awaiting_input':
        return '先回答问题，计划会继续。';
      case 'paused':
        return '计划已暂停；恢复或取消后才能继续。';
      case 'blocked':
        return '计划暂时不能继续；先解决下面的问题。';
      case 'terminal':
        return `本次运行已结束：${terminalStateLabel(options.planStatus, options.outcome)}。可以查看结果，或按需重试。`;
      case 'runnable':
        return '计划尚未启动，可以从这里开始运行。';
      default:
        return '暂时无法确认计划状态，请刷新后再试。';
    }
  };
  const text = body();
  if (!options.connectionInterrupted) return text;
  // A dropped feed never changes the business state. A run that is still going
  // keeps that reassurance; a waiting gate also keeps its own next step.
  const runContinues =
    options.startAccepted === true ||
    state === 'observing' ||
    state === 'awaiting_approval' ||
    state === 'awaiting_input';
  if (!runContinues) return `进度更新中断，可重新连接。${text}`;
  const interruptedNotice = '进度更新中断，可重新连接；任务仍在运行。';
  return state === 'observing' ? interruptedNotice : `${interruptedNotice}${text}`;
};

function GraphCanvasSkeleton() {
  return (
    <div className="absolute inset-0 flex items-center justify-center" aria-busy="true" aria-label="正在加载执行图">
      <div className="flex items-center gap-2" aria-hidden="true">
        <span className="h-10 w-24 animate-pulse rounded-xl border border-line bg-surface" />
        <span className="h-px w-8 bg-line-strong" />
        <span className="h-10 w-24 animate-pulse rounded-xl border border-line bg-surface" />
        <span className="h-px w-8 bg-line-strong" />
        <span className="h-10 w-24 animate-pulse rounded-xl border border-line bg-surface" />
      </div>
    </div>
  );
}

export const GraphPanel = memo(function GraphPanel(props: GraphPanelProps) {
  const {
    nodes,
    edges,
    onNodesChange,
    onEdgesChange,
    onNodeClick,
    intentContext,
    outcome,
    outcomeDetails,
    missingRequirements,
    runtimeActions,
    questions,
    onAnswerQuestion,
    runtimePermissionMode,
    plannerSource,
    plannerFallbackReason,
    complexity,
    planningRoute,
    templateVariant,
    runTrigger,
    verification,
    onOpenModelSettings,
    onRuntimeActionDecision,
    runAllText,
    graphTitle,
    graphHint,
    planId,
    planStatus,
    isExecuting,
    requiresApproval,
    requiresReview,
    onOpenReview,
    onRunAll,
    onApproveAndRun,
    onCancel,
    onRetryFailed,
    onResume,
    canRun,
    startAccepted,
    planPaused,
    connectionState,
    connectionDetail,
    onReconnect,
  } = props;
  const outcomeSummary = summarizePlanOutcome({
    status: planStatus ?? '',
    outcome: outcome ?? '',
    outcome_details: outcomeDetails ?? undefined,
  });
  const blockingCount = nodes.filter((node) => node.data.status === 'blocked').length;
  const completedCount = nodes.filter((node) => node.data.status === 'completed').length;
  const runningCount = nodes.filter((node) => node.data.status === 'in_progress').length;
  const failedCount = nodes.filter((node) => node.data.status === 'failed').length;
  const pendingCount = nodes.filter((node) => node.data.task && !terminalNodeStatuses.has(String(node.data.status))).length;
  const intentLabel = intentContext ? intentLabels[intentContext.intent_type] ?? intentContext.intent_type : null;
  const displayTitle = intentContext?.raw_intent?.trim() || graphTitle;
  const displayHint = nodes.length === 0 ? graphHint : (intentContext ? '' : graphHint);
  const pendingRuntimeActions = (runtimeActions ?? []).filter((action) => action.status === 'pending');
  const pendingQuestions = (questions ?? []).filter((question) => question.status === 'pending');
  const pendingWorkCount = pendingQuestions.length + pendingRuntimeActions.length + (missingRequirements?.length ?? 0);
  // Business status comes from the server snapshot, never from the connection.
  const businessState = planBusinessState(planStatus, planPaused === true);
  const planStateLabel = planStateLabelFor(businessState, requiresReview, requiresApproval, nodes.length > 0, planStatus, outcome);
  const connectionLabel = connectionState ? describeConnectionState(connectionState) : '';
  // Design 4.4: an exhausted reconnect keeps the last snapshot and shows
  // "更新已中断" plus an explicit "重新连接" entry, for any business status.
  const showReconnect = connectionState === 'interrupted';
  // `canRun` is the view model's business gate; the local `pendingCount` check
  // keeps legacy callers on the previous behavior and stops an empty run.
  const runAllowed =
    businessState === 'runnable' &&
    !startAccepted &&
    !planPaused &&
    pendingCount > 0 &&
    pendingRuntimeActions.length === 0 &&
    (canRun ?? true);
  // A plan the server considers started (or whose start this view already had
  // accepted). Kept separate from the local `isExecuting` flag so a refreshed
  // run still exposes "cancel" and the observation hints.
  const planStarted = startAccepted === true || businessState === 'observing';
  // Cancel stays available for a run the server reports as active (including a
  // paused one), whether or not this view started it.
  const planCancelable =
    isExecuting || businessState === 'observing' || businessState === 'paused' || startAccepted === true;
  const [questionDrafts, setQuestionDrafts] = useState<Record<string, { option: string; text: string }>>({});
  const [runtimeDecision, setRuntimeDecision] = useState<{ actionId: string; decision: 'approve' | 'reject' } | null>(null);
  const runtimeDecisionLockRef = useRef(false);
  const runtimeDecisionBusy = runtimeDecision !== null;
  const [showColophon, setShowColophon] = useState(false);
  const [showPendingWork, setShowPendingWork] = useState(false);

  const handleRuntimeActionDecision = async (actionId: string, decision: 'approve' | 'reject') => {
    if (runtimeDecisionLockRef.current) return;
    runtimeDecisionLockRef.current = true;
    setRuntimeDecision({ actionId, decision });
    try {
      await onRuntimeActionDecision?.(actionId, decision);
    } catch {
      // The runtime hook already reports approval failures in the manuscript notes.
    } finally {
      runtimeDecisionLockRef.current = false;
      setRuntimeDecision(null);
    }
  };

  // --- Primary action ------------------------------------------------------
  // The business state owns the primary entry: a plan waiting for approval or
  // an answer is *started but still observed*, so the runtime reports
  // `isExecuting` for it too. Checking that local flag first replaced
  // 审批并运行 / 回答问题 with 取消计划. Cancel stays as an explicit secondary
  // control next to those gates instead.
  const planIsPaused = planPaused === true || businessState === 'paused';

  const cancelButton = (title: string) => (
    <button
      type="button"
      onClick={onCancel}
      title={title}
      data-testid="plan-cancel"
      className="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-xl border border-proof/40 bg-surface px-3 text-[10.5px] font-semibold text-proof transition-colors hover:bg-proof-soft active:translate-y-px"
    >
      <Square className="h-2.5 w-2.5 fill-current" />
      取消计划
    </button>
  );

  // Waiting gates: review, plan approval and plan questions. A paused plan keeps
  // pause authoritative, so these entries never appear next to it.
  const waitingGateAction = (() => {
    if (planIsPaused) return null;
    if (requiresReview || businessState === 'review') {
      return (
        <button type="button" onClick={onOpenReview} data-testid="plan-open-review" className="research-assistant-canvas-primary shrink-0 whitespace-nowrap">
          <ShieldCheck className="h-3.5 w-3.5" />
          去审阅
        </button>
      );
    }
    if (requiresApproval || businessState === 'awaiting_approval') {
      // A pending runtime action must be decided on its own card first; the
      // plan-level button would be a guaranteed rejection.
      return pendingRuntimeActions.length > 0 ? (
        <button
          type="button"
          onClick={() => setShowPendingWork(true)}
          data-testid="plan-open-approval"
          className="research-assistant-canvas-primary shrink-0 whitespace-nowrap"
        >
          <ShieldCheck className="h-3.5 w-3.5" />
          处理审批
        </button>
      ) : (
        <button type="button" onClick={onApproveAndRun} data-testid="plan-approve-and-run" className="research-assistant-canvas-primary shrink-0 whitespace-nowrap">
          <ShieldCheck className="h-3.5 w-3.5" />
          审批并运行
        </button>
      );
    }
    if (businessState === 'awaiting_input') {
      return (
        <button
          type="button"
          onClick={() => setShowPendingWork(true)}
          disabled={pendingQuestions.length === 0}
          data-testid="plan-answer-question"
          title={pendingQuestions.length === 0 ? '还没有收到问题内容，请刷新后再试' : undefined}
          className="research-assistant-canvas-primary shrink-0 whitespace-nowrap"
        >
          <MessageSquare className="h-3.5 w-3.5" />
          回答问题
        </button>
      );
    }
    return null;
  })();

  const waitingGateWithCancel = waitingGateAction ? (
    <>
      {waitingGateAction}
      {planCancelable ? cancelButton('取消这次运行；计划会停止等待') : null}
    </>
  ) : null;

  // A paused plan that was already started is resumed or canceled. Resume is
  // the primary entry and only exists when the runtime exposes the
  // backend-permitted resume action; cancel stays the secondary control. Pause
  // outranks the local `isExecuting` flag and every review/approval gate, and
  // this entry never submits an execute, approve or retry request.
  const resumeButton = onResume ? (
    <button
      type="button"
      onClick={() => onResume()}
      data-testid="plan-resume"
      title="从暂停处继续；不会重新开始计划"
      className="research-assistant-canvas-primary shrink-0 whitespace-nowrap"
    >
      <Play className="h-3 w-3 fill-current" />
      恢复计划
    </button>
  ) : null;
  const pausedAction = (
    <>
      {resumeButton}
      {cancelButton('取消已暂停的计划')}
    </>
  );

  const primaryAction = (() => {
    if (showReconnect) {
      // A dropped stream keeps the business state, the waiting gate and the
      // cancel entry: the run continues, only the progress feed needs rebuilding.
      return (
        <>
          {planIsPaused
            ? pausedAction
            : waitingGateWithCancel ?? (planCancelable ? cancelButton('取消这次运行；任务会停止') : null)}
          <button
            type="button"
            onClick={() => onReconnect?.()}
            disabled={!onReconnect}
            data-testid="plan-reconnect"
            title="重新获取进度；不会重新启动任务"
            className="inline-flex h-8 max-w-[16rem] shrink-0 items-center gap-1.5 whitespace-nowrap rounded-xl border border-warn/45 bg-warn-soft px-3 text-[10.5px] font-semibold text-warn transition-colors hover:border-warn active:translate-y-px disabled:cursor-not-allowed disabled:opacity-60"
          >
            <RefreshCw className="h-3.5 w-3.5" />
            重新连接
          </button>
        </>
      );
    }

    // Pause is authoritative: it wins over the local execution flag and over
    // the review/approval/question gates, so those entries can never replace it.
    if (planIsPaused) return pausedAction;

    if (waitingGateWithCancel) return waitingGateWithCancel;

    // An accepted start keeps its 受理 entry (disabled) plus cancel, even while
    // the runtime already observes it as executing: it must never fall back to
    // a plain "run" and never be submitted twice.
    if (startAccepted && businessState === 'runnable') {
      return (
        <>
          <button
            type="button"
            onClick={onRunAll}
            disabled
            data-testid="plan-run"
            title="正在确认启动，请稍候"
            className="research-assistant-canvas-primary shrink-0 whitespace-nowrap"
          >
            <Play className="h-3 w-3 fill-current" />
            启动已受理
          </button>
          {planCancelable ? cancelButton('取消这次运行；任务会停止') : null}
        </>
      );
    }

    if (isExecuting || businessState === 'observing') {
      return cancelButton('取消正在运行的计划');
    }

    if (businessState === 'blocked') {
      return (
        <button
          type="button"
          onClick={() => setShowPendingWork(pendingWorkCount > 0)}
          disabled={pendingWorkCount === 0}
          data-testid="plan-blocked"
          title="计划暂时不能继续；先解决下面的问题"
          className="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-xl border border-warn/45 bg-warn-soft px-3 text-[10.5px] font-semibold text-warn disabled:cursor-not-allowed disabled:opacity-70"
        >
          <Ban className="h-3.5 w-3.5" />
          已阻塞
        </button>
      );
    }

    if (businessState === 'terminal') {
      const resultNode = [...nodes].reverse().find((node) => Boolean(node.data.task)) ?? null;
      // Only a real failed node can be retried: the runtime returns immediately
      // when there is nothing failed, so the button would do nothing.
      if (failedCount > 0) {
        return (
          <button
            type="button"
            onClick={onRetryFailed}
            data-testid="plan-retry-failed"
            title="只重跑失败的节点；其余结果保留"
            className="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-xl border border-warn/40 bg-surface px-3 text-[10.5px] font-semibold text-warn transition-colors hover:bg-warn-soft active:translate-y-px"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            重试失败节点
          </button>
        );
      }
      // A failed plan without failed nodes still needs a working entry: adjust
      // the plan through the review, otherwise fall back to the last results.
      if (planStatus === 'failed' && onOpenReview) {
        return (
          <button type="button" onClick={onOpenReview} data-testid="plan-review-and-rerun" className="research-assistant-canvas-primary shrink-0 whitespace-nowrap">
            <ShieldCheck className="h-3.5 w-3.5" />
            审阅后重新运行
          </button>
        );
      }
      if (resultNode) {
        return (
          <button
            type="button"
            onClick={() => onNodeClick(null, resultNode)}
            data-testid="plan-view-result"
            className="research-assistant-canvas-primary shrink-0 whitespace-nowrap"
          >
            <Eye className="h-3.5 w-3.5" />
            查看结果
          </button>
        );
      }
      // A finished plan without result nodes has nothing left to run.
      return null;
    }

    if (businessState === 'runnable') {
      const blockedByRuntimeAction = pendingRuntimeActions.length > 0;
      return (
        <button
          type="button"
          onClick={onRunAll}
          disabled={!runAllowed || blockedByRuntimeAction}
          data-testid="plan-run"
          title={blockedByRuntimeAction ? '先处理运行授权，再开始运行' : undefined}
          className="research-assistant-canvas-primary shrink-0 whitespace-nowrap"
        >
          <Play className="h-3 w-3 fill-current" />
          {runAllText}
        </button>
      );
    }

    // unknown: never default to "run"; keep the plan visible and non-actionable.
    return (
      <button
        type="button"
        onClick={() => onReconnect?.()}
        disabled={!onReconnect}
        data-testid="plan-refresh-state"
        title="先刷新一次，确认计划的当前状态"
        className="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-xl border border-line-strong bg-surface px-3 text-[10.5px] font-semibold text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:cursor-not-allowed disabled:opacity-60"
      >
        <RefreshCw className="h-3.5 w-3.5" />
        刷新状态
      </button>
    );
  })();

  return (
    <div className="research-assistant-graph-panel relative">
      <header className="research-assistant-canvas-head">
        <div className="min-w-0 flex-1">
          <div className="research-assistant-canvas-breadcrumb">
            <span>研究工作台</span>
            <span className="sep">/</span>
            <span>{planId ? `计划 ${planId.slice(0, 8).toUpperCase()}` : '新计划'}</span>
          </div>
          <div className="research-assistant-canvas-title-row">
            <h2 title={displayTitle}>{displayTitle}</h2>
            {outcome ? <span className="proof-stamp">{outcomeLabels[outcome] ?? outcome}</span> : null}
            <VerificationBadge verification={verification} onOpenSettings={onOpenModelSettings} />
          </div>
          <div className="research-assistant-canvas-meta">
            {displayHint ? <span className="max-w-[44rem] truncate" title={displayHint}>{displayHint}</span> : null}
            <span>{nodes.length} 节点</span>
            <span>{completedCount} 完成</span>
            {runningCount > 0 ? <span>{runningCount} 运行中</span> : null}
            {blockingCount > 0 ? <span className="text-proof" data-testid="plan-blocked-count">{blockingCount} 被阻断</span> : null}
            {plannerSource === 'template' ? (
              <span className="rounded-full bg-warn-soft px-2 py-0.5 text-[9.5px] text-warn" title={plannerFallbackReason || '该计划由模板规划生成'}>
                模板规划
              </span>
            ) : null}
          </div>
        </div>

        <div className="research-assistant-canvas-actions flex-wrap justify-end">
          {planId ? (
            <span
              className={`research-assistant-status-pill ${planStateLabel === '待审阅' || planStateLabel === '待批准' || planStateLabel === '已阻塞' ? 'is-alert' : ''}`}
              data-testid="plan-state-pill"
              title={businessHelpText(businessState, { requiresReview, requiresApproval, startAccepted, hasPendingRuntimeActions: pendingRuntimeActions.length > 0, planStatus, outcome, connectionInterrupted: showReconnect })}
            >
              <i aria-hidden="true" />
              {planStateLabel}
            </span>
          ) : null}
          {connectionLabel ? (
            <span
              className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[10px] font-semibold ${
                connectionState === 'interrupted'
                  ? 'border-warn/40 bg-warn-soft text-warn'
                  : connectionState === 'live'
                    ? 'border-ok/30 bg-ok-soft text-ok'
                    : 'border-line bg-surface text-muted'
              }`}
              data-testid="plan-connection-pill"
              data-connection-state={connectionState}
              title={connectionDetail || connectionLabel}
            >
              {connectionState === 'interrupted' ? <WifiOff className="h-3 w-3" aria-hidden="true" /> : <span className={`h-1.5 w-1.5 rounded-full ${connectionState === 'live' ? 'bg-ok' : 'bg-muted'}`} aria-hidden="true" />}
              {connectionLabel}
            </span>
          ) : null}
          {pendingWorkCount > 0 ? (
            <button
              type="button"
              onClick={() => setShowPendingWork((value) => !value)}
              aria-expanded={showPendingWork}
              className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-proof/30 bg-proof-soft px-3 text-[10.5px] font-semibold text-proof transition-colors hover:border-proof/60"
            >
              <span className="h-1.5 w-1.5 rounded-full bg-proof" aria-hidden="true" />
              待办 {pendingWorkCount}
              <ChevronDown className={`h-3 w-3 transition-transform ${showPendingWork ? 'rotate-180' : ''}`} />
            </button>
          ) : null}

          {primaryAction}

          {nodes.length > 0 ? (
            <button
              type="button"
              onClick={() => {
                const target = nodes[0];
                if (target) onNodeClick(null, target);
              }}
              aria-label="查看节点详情"
              title="查看节点详情"
              className="research-assistant-canvas-chip px-2.5 md:hidden"
            >
              <Eye className="h-3.5 w-3.5" />
              查看节点
            </button>
          ) : null}
          <button
            type="button"
            onClick={() => setShowColophon((value) => !value)}
            aria-expanded={showColophon}
            className="research-assistant-canvas-icon-btn"
            title="版本记录"
            aria-label="版本记录"
          >
            <MoreHorizontal className="h-4 w-4" />
          </button>
        </div>
      </header>

      {planId || nodes.length > 0 ? (
        <div
          className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 px-6 pb-2.5 pt-1.5 text-[10.5px] leading-4 text-muted"
          data-testid="plan-statusline"
        >
          <span className="min-w-0 flex-1 break-words" data-testid="plan-business-help">
            {businessHelpText(businessState, {
              requiresReview,
              requiresApproval,
              startAccepted,
              hasPendingRuntimeActions: pendingRuntimeActions.length > 0,
              planStatus,
              outcome,
              connectionInterrupted: showReconnect,
            })}
          </span>
          {connectionLabel ? (
            <span
              className={`shrink-0 ${connectionState === 'interrupted' ? 'text-warn' : connectionState === 'live' ? 'text-ok' : 'text-muted'}`}
              data-testid="plan-connection-detail"
            >
              {connectionDetail ? `${connectionLabel} · ${connectionDetail}` : connectionLabel}
            </span>
          ) : null}
          {planStarted && !isExecuting && businessState !== 'terminal' ? (
            <span className="shrink-0 text-proof" data-testid="plan-start-accepted">
              {startAccepted ? '启动已受理' : '任务运行中'}
            </span>
          ) : null}
        </div>
      ) : null}

      {businessState === 'terminal' && (outcome === 'partial' || outcome === 'blocked' || outcome === 'needs_review') ? (
        <div
          className="mx-6 mb-2 rounded-xl border border-warn/40 bg-warn-soft px-3 py-2 text-[11px] leading-5 text-ink-soft"
          data-testid="plan-outcome-details"
          data-outcome={outcome}
        >
          <p className="font-semibold text-warn">
            {outcomeSummary.headline}
            {outcomeSummary.detail ? ' · ' : ''}
            <span className="font-normal text-ink-soft">{outcomeSummary.detail}</span>
          </p>
          {outcomeSummary.losses.length > 0 ? (
            <ul className="mt-1 list-disc pl-4">
              {outcomeSummary.losses.map((loss) => (
                <li key={loss}>{loss}</li>
              ))}
            </ul>
          ) : null}
          {outcomeSummary.limitations.length > 0 ? (
            <div className="mt-1">
              <span className="font-semibold">{outcomeSummary.limitationsLabel}</span>
              <ul className="list-disc pl-4">
                {outcomeSummary.limitations.map((limitation) => (
                  <li key={limitation}>{limitation}</li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}

      {showColophon ? (
        <div className="absolute right-6 top-[86px] z-30 w-[min(420px,calc(100%-3rem))] rounded-2xl border border-line bg-surface p-4 shadow-float">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-[10px] font-semibold text-muted">版本记录</span>
            <button type="button" onClick={() => setShowColophon(false)} aria-label="关闭版本记录" className="grid h-6 w-6 place-items-center rounded-md text-muted hover:bg-surface-muted hover:text-ink">
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
          <dl className="grid grid-cols-2 gap-x-5 gap-y-2 text-[10.5px]">
            <dt className="text-muted">意图类型</dt><dd className="truncate text-ink-soft" title={intentLabel ?? '未识别'}>{intentLabel ?? '未识别'}</dd>
            <dt className="text-muted">规划来源</dt><dd className="truncate text-ink-soft" title={plannerFallbackReason || undefined}>{plannerSource === 'template' ? '模板规划' : plannerSource === 'agent' ? 'Agent 规划' : '未知'}</dd>
            <dt className="text-muted">复杂度</dt><dd className="text-ink-soft" data-testid="plan-complexity">{complexity ?? '未评估'}</dd>
            <dt className="text-muted">规划路径</dt><dd className="text-ink-soft" data-testid="plan-planning-route">{planningRoute === 'fast_path' ? '快速路径' : planningRoute === 'standard_template' ? '标准模板' : planningRoute === 'agent_augmented' ? 'Agent 增强' : planningRoute === 'template_fallback' ? '模板回退' : '未知'}</dd>
            <dt className="text-muted">模板变体</dt><dd className="truncate text-ink-soft" data-testid="plan-template-variant" title={templateVariant ?? undefined}>{templateVariant ?? '—'}</dd>
            <dt className="text-muted">运行权限</dt><dd className="text-ink-soft">{runtimePermissionMode === 'full_access' ? '完全权限' : '确认模式'}</dd>
            <dt className="text-muted">运行方式</dt><dd className="text-ink-soft" data-testid="plan-run-trigger">{runTriggerLabel(runTrigger)}</dd>
            <dt className="text-muted">结果状态</dt><dd className="text-ink-soft">{outcome ? outcomeLabels[outcome] ?? outcome : '尚未产出'}</dd>
            <dt className="text-muted">校验</dt><dd className="text-ink-soft">{verification ? <VerificationBadge verification={verification} onOpenSettings={onOpenModelSettings} /> : '未校验'}</dd>
          </dl>
        </div>
      ) : null}

      <div className="research-assistant-canvas-stage">
        {nodes.length === 0 ? (
          <div className="research-assistant-empty-state">
            <div className="research-assistant-empty-card">
              <div className="research-assistant-empty-diagram" aria-hidden="true">
                <span className="empty-link" />
                <span className="empty-node">01</span>
                <span className="empty-node">02</span>
                <span className="empty-node">03</span>
              </div>
              <h3>还没有执行图</h3>
              <p>在对话里写下研究目标。计划会先固定指标与约束，再拆成可核对的节点。</p>
              <div className="research-assistant-empty-examples">
                <span>复现论文核心方法</span>
                <span>对比两个 RAG 框架</span>
                <span>核查论文主张</span>
              </div>
            </div>
          </div>
        ) : (
          <div className="absolute inset-0 overflow-hidden">
            <Suspense fallback={<GraphCanvasSkeleton />}>
              <GraphCanvas
                nodes={nodes}
                edges={edges}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onNodeClick={onNodeClick}
              />
            </Suspense>
            <div className="pointer-events-none absolute left-4 bottom-4 flex items-center gap-3 rounded-lg border border-line bg-surface/85 px-2.5 py-1.5 text-[9.5px] text-muted backdrop-blur-sm">
              <span className="flex items-center gap-1"><i className="h-1.5 w-1.5 rounded-full bg-ok" aria-hidden="true" />已完成</span>
              <span className="flex items-center gap-1"><i className="h-1.5 w-1.5 rounded-full bg-accent" aria-hidden="true" />运行中</span>
              <span className="flex items-center gap-1"><i className="h-1.5 w-1.5 rounded-full bg-line-strong" aria-hidden="true" />等待</span>
            </div>
          </div>
        )}

        {showPendingWork && pendingWorkCount > 0 ? (
          <div className="absolute right-4 top-4 z-20 max-h-[calc(100%-2rem)] w-[min(480px,calc(100%-2rem))] overflow-y-auto rounded-2xl border border-line bg-surface p-3 shadow-float">
            <div className="mb-2 flex items-center justify-between">
              <div>
                <div className="text-[10px] font-semibold text-muted">需要你处理</div>
                <div className="mt-0.5 text-[13px] font-semibold text-ink">{pendingWorkCount} 项</div>
              </div>
              <button type="button" onClick={() => setShowPendingWork(false)} aria-label="关闭待办" className="grid h-7 w-7 place-items-center rounded-lg text-muted hover:bg-surface-muted hover:text-ink">
                <X className="h-3.5 w-3.5" />
              </button>
            </div>

            {pendingQuestions.length > 0 ? (
              <section className="border-t border-line py-2.5" aria-label="需要你回答的问题">
                <div className="mb-1.5 flex items-baseline gap-2">
                  <span className="text-[10px] font-semibold text-proof">需要你回答的问题</span>
                  <span className="text-[10px] text-muted">{pendingQuestions.length} 项</span>
                </div>
                <div className="space-y-2">
                  {pendingQuestions.map((question) => {
                    const draft = questionDrafts[question.id] ?? { option: '', text: '' };
                    return (
                      <div key={question.id} className="rounded-xl bg-surface-muted p-2.5">
                        <div className="flex flex-wrap items-baseline gap-x-2">
                          <span className="text-[12px] font-semibold text-ink">{question.header || '计划提问'}</span>
                          {question.task_id ? <span className="font-mono text-[9.5px] text-muted">{question.task_id}</span> : null}
                        </div>
                        <p className="mt-1 whitespace-pre-wrap text-[11.5px] leading-5 text-ink-soft">{question.prompt}</p>
                        {question.options && question.options.length > 0 ? (
                          <div className="mt-2 flex flex-wrap gap-1.5">
                            {question.options.map((option) => (
                              <label key={option.id} className="inline-flex items-center gap-1.5 rounded-full border border-line bg-surface px-2 py-1 text-[10.5px]">
                                <input
                                  type={question.multi_select ? 'checkbox' : 'radio'}
                                  name={`question-${question.id}`}
                                  checked={draft.option === option.id}
                                  onChange={() => setQuestionDrafts({ ...questionDrafts, [question.id]: { ...draft, option: option.id } })}
                                />
                                <span title={option.description}>{option.label}</span>
                                {question.default_option === option.id ? <span className="text-[9px] text-muted">默认</span> : null}
                              </label>
                            ))}
                          </div>
                        ) : null}
                        {question.allow_free_text !== false ? (
                          <input
                            value={draft.text}
                            onChange={(event) => setQuestionDrafts({ ...questionDrafts, [question.id]: { ...draft, text: event.target.value } })}
                            placeholder="自由输入（可选）"
                            className="mt-2 w-full rounded-lg border border-line bg-surface px-2 py-1 text-[11px]"
                          />
                        ) : null}
                        <button
                          type="button"
                          disabled={!draft.option && !draft.text}
                          onClick={() => void onAnswerQuestion?.(question.id, { answer: draft.text || undefined, answer_option: draft.option || undefined })}
                          className="mt-2 h-7 rounded-lg bg-accent px-2.5 text-[10.5px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-40"
                        >
                          提交回答
                        </button>
                      </div>
                    );
                  })}
                </div>
              </section>
            ) : null}

            {pendingRuntimeActions.length > 0 ? (
              <section className="border-t border-line py-2.5" aria-label="需要你批准的操作">
                <div className="mb-1.5 flex items-baseline gap-2">
                  <span className="text-[10px] font-semibold text-proof">需要你批准的操作</span>
                  <span className="text-[10px] text-muted">{pendingRuntimeActions.length} 项</span>
                </div>
                <div className="space-y-1.5">
                  {pendingRuntimeActions.map((action) => {
                    const detailsText = describeRuntimeAction(action);
                    const isThisActionBusy = runtimeDecision?.actionId === action.id;
                    return (
                      <div key={action.id} className="flex flex-wrap items-center justify-between gap-2 rounded-xl bg-surface-muted px-2.5 py-2">
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-baseline gap-x-2">
                            <span className="text-[11.5px] font-semibold text-ink">{action.title}</span>
                            <span className="text-[9px] text-muted">{action.kind}</span>
                            {action.risk ? <span className="text-[9px] text-proof">{action.risk}</span> : null}
                          </div>
                          {detailsText ? <p className="mt-0.5 max-w-[24rem] break-words font-mono text-[10px] leading-4 text-ink-soft" title={detailsText}>{detailsText}</p> : null}
                        </div>
                        <div className="flex shrink-0 items-center gap-1.5">
                          <button
                            type="button"
                            disabled={runtimeDecisionBusy}
                            aria-busy={isThisActionBusy && runtimeDecision?.decision === 'reject'}
                            onClick={() => void handleRuntimeActionDecision(action.id, 'reject')}
                            className="h-6 rounded-md border border-line-strong bg-surface px-2 text-[10.5px] font-medium text-ink-soft transition-colors hover:border-proof hover:text-proof disabled:opacity-50"
                          >
                            {isThisActionBusy && runtimeDecision?.decision === 'reject' ? '提交中…' : '拒绝'}
                          </button>
                          <button
                            type="button"
                            disabled={runtimeDecisionBusy}
                            aria-busy={isThisActionBusy && runtimeDecision?.decision === 'approve'}
                            onClick={() => void handleRuntimeActionDecision(action.id, 'approve')}
                            className="h-6 rounded-md bg-accent px-2 text-[10.5px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-50"
                          >
                            {isThisActionBusy && runtimeDecision?.decision === 'approve' ? '提交中…' : '允许一次'}
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </section>
            ) : null}

            {missingRequirements && missingRequirements.length > 0 ? (
              <section className="border-t border-line py-2.5" aria-label="缺失材料">
                <p className="text-[10px] font-semibold text-proof">缺失材料 · {missingRequirements.length} 项</p>
                <ul className="mt-1.5 space-y-2">
                  {missingRequirements.map((item, index) => {
                    const remediation = item.remediation || '请补充该材料后重试。';
                    return (
                      <li key={`${item.kind}-${index}`} className="rounded-xl bg-surface-muted px-2.5 py-2">
                        <div className="flex flex-wrap items-baseline gap-x-2 text-[11.5px] leading-5 text-ink-soft">
                          <span className="text-[9.5px] font-semibold text-proof">{item.kind}</span>
                          <span className="font-semibold text-ink">{item.name || item.kind}</span>
                        </div>
                        {item.detail ? <p className="text-[10.5px] leading-5 text-muted">{item.detail}</p> : null}
                        <p className="text-[10.5px] leading-5 text-ink-soft"><span className="font-semibold text-proof">修复建议: </span>{remediation}</p>
                      </li>
                    );
                  })}
                </ul>
              </section>
            ) : null}
          </div>
        ) : null}

        {isExecuting ? (
          <div className="pointer-events-none absolute inset-x-4 top-4 z-10 h-0.5 overflow-hidden rounded-full bg-line" aria-hidden="true">
            <div className="proof-pulse h-full w-1/3 bg-proof" />
          </div>
        ) : null}
      </div>
    </div>
  );
});
