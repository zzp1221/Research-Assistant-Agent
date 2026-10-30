import { lazy, memo, Suspense, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { ChevronDown, Eye, MoreHorizontal, Play, RotateCcw, ShieldCheck, Square, X } from 'lucide-react';
import type { IntentContext, MissingRequirement, PlanQuestion, RuntimeAction, Verification } from '../../contracts/api';
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
  onOpenTask?: (taskId: string) => void;
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
  } = props;
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
  const planStateLabel = requiresReview
    ? '待审阅'
    : requiresApproval
      ? '待批准'
      : planStatus === 'in_progress'
        ? '运行中'
        : planStatus === 'completed'
          ? '已完成'
          : planStatus === 'failed'
            ? '失败'
            : planStatus === 'awaiting_input'
              ? '待回答'
              : nodes.length > 0
                ? '待运行'
                : '未开始';
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
            {plannerSource === 'template' ? (
              <span className="rounded-full bg-warn-soft px-2 py-0.5 text-[9.5px] text-warn" title={plannerFallbackReason || '该计划由模板规划生成'}>
                模板规划
              </span>
            ) : null}
          </div>
        </div>

        <div className="research-assistant-canvas-actions">
          {planId ? (
            <span className={`research-assistant-status-pill ${planStateLabel === '待审阅' || planStateLabel === '待批准' ? 'is-alert' : ''}`}>
              <i aria-hidden="true" />
              {planStateLabel}
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

          {isExecuting ? (
            <button type="button" onClick={onCancel} title="取消计划" className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-proof/40 bg-surface px-3 text-[10.5px] font-semibold text-proof transition-colors hover:bg-proof-soft active:translate-y-px">
              <Square className="h-2.5 w-2.5 fill-current" />
              取消计划
            </button>
          ) : requiresReview ? (
            <button type="button" onClick={onOpenReview} className="research-assistant-canvas-primary">
              <ShieldCheck className="h-3.5 w-3.5" />
              去审阅
            </button>
          ) : requiresApproval ? (
            <button type="button" onClick={onApproveAndRun} className="research-assistant-canvas-primary">
              <ShieldCheck className="h-3.5 w-3.5" />
              审批并运行
            </button>
          ) : failedCount > 0 ? (
            <button type="button" onClick={onRetryFailed} className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-warn/40 bg-surface px-3 text-[10.5px] font-semibold text-warn transition-colors hover:bg-warn-soft active:translate-y-px">
              <RotateCcw className="h-3.5 w-3.5" />
              重试失败节点
            </button>
          ) : (
            <button
              type="button"
              onClick={onRunAll}
              disabled={pendingCount === 0 || pendingRuntimeActions.length > 0}
              title={pendingRuntimeActions.length > 0 ? '先处理运行权限确认' : undefined}
              className="research-assistant-canvas-primary"
            >
              <Play className="h-3 w-3 fill-current" />
              {runAllText}
            </button>
          )}

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
