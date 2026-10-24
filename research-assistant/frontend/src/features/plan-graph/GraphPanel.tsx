import { lazy, memo, Suspense, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { CheckCircle2, GitBranch, LoaderCircle, Play, RotateCcw, ShieldAlert, ShieldCheck, Square } from 'lucide-react';
import type { IntentContext, MissingRequirement, RuntimeAction } from '../../contracts/api';
import type { Task } from '../../contracts/api';

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
  runtimePermissionMode?: string | null;
  onRuntimeActionDecision?: (actionId: string, decision: 'approve' | 'reject') => Promise<boolean | void> | boolean | void;
  runAllText: string;
  graphTitle: string;
  graphHint: string;
  isExecuting: boolean;
  requiresApproval: boolean;
  onRunAll: () => void;
  onApproveAndRun: () => void;
  onCancel: () => void;
  onRetryFailed: () => void;
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

function GraphCanvasSkeleton() {
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-paper" aria-busy="true" aria-label="正在加载计划画布">
      <div className="flex items-center gap-2" aria-hidden="true">
        <span className="h-12 w-20 animate-pulse rounded-[5px] border border-line bg-surface" />
        <span className="h-px w-8 border-t border-dashed border-line-strong" />
        <span className="h-12 w-20 animate-pulse rounded-[5px] border border-accent/30 bg-accent-soft" />
        <span className="h-px w-8 border-t border-dashed border-line-strong" />
        <span className="h-12 w-20 animate-pulse rounded-[5px] border border-line bg-surface" />
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
    runtimePermissionMode,
    onRuntimeActionDecision,
    runAllText,
    graphTitle,
    graphHint,
    isExecuting,
    requiresApproval,
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
  const subtitle = intentContext?.raw_intent || graphHint;
  const experimentSearchTask = nodes.map((node) => node.data.task as Task | undefined).find((task) => task?.Type === 'experiment_run');
  const experimentParallelism = typeof experimentSearchTask?.Inputs?.experiment_max_parallel_trials === 'number'
    ? experimentSearchTask.Inputs.experiment_max_parallel_trials
    : 4;
  const pendingRuntimeActions = (runtimeActions ?? []).filter((action) => action.status === 'pending');
  const [runtimeDecision, setRuntimeDecision] = useState<{ actionId: string; decision: 'approve' | 'reject' } | null>(null);
  const runtimeDecisionLockRef = useRef(false);
  const runtimeDecisionBusy = runtimeDecision !== null;

  const handleRuntimeActionDecision = async (actionId: string, decision: 'approve' | 'reject') => {
    if (runtimeDecisionLockRef.current) return;
    runtimeDecisionLockRef.current = true;
    setRuntimeDecision({ actionId, decision });
    try {
      await onRuntimeActionDecision?.(actionId, decision);
    } catch {
      // The runtime hook already reports approval failures in the chat.
    } finally {
      runtimeDecisionLockRef.current = false;
      setRuntimeDecision(null);
    }
  };

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col bg-paper">
      <header className="shrink-0 border-b border-line bg-surface px-5 py-3">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-2">
              <GitBranch className="h-4 w-4 shrink-0 text-accent" />
              <h2 className="truncate text-[14px] font-semibold tracking-[-0.01em] text-ink">{graphTitle}</h2>
              {intentLabel && (
                <span className="shrink-0 rounded-[3px] border border-line bg-surface-muted px-1.5 py-0.5 text-[10.5px] font-medium text-ink-soft">
                  {intentLabel}
                </span>
              )}
              {outcome && (
                <span className="shrink-0 rounded-[3px] border border-accent/30 bg-accent-soft px-1.5 py-0.5 text-[10.5px] font-semibold uppercase tracking-wide text-accent-ink">
                  {outcome}
                </span>
              )}
              {runtimePermissionMode && (
                <span className={`shrink-0 rounded-[3px] border px-1.5 py-0.5 text-[10.5px] font-medium ${runtimePermissionMode === 'full_access' ? 'border-flag/30 bg-flag-soft text-flag' : 'border-line bg-surface-muted text-ink-soft'}`}>
                  {runtimePermissionMode === 'full_access' ? '完全权限' : '重要操作需确认'}
                </span>
              )}
            </div>
            <p className="mt-1 max-w-3xl truncate text-[11.5px] leading-4 text-muted" title={subtitle}>
              {subtitle}
            </p>
            {missingRequirements && missingRequirements.length > 0 && (
              <p className="mt-1 max-w-3xl truncate text-[11px] leading-4 text-flag" title={missingRequirements.map((item) => item.detail || item.kind).join('；')}>
                缺少：{missingRequirements.map((item) => item.name || item.kind).join('、')}
              </p>
            )}
            <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted">
              <span className="tabular-nums">{nodes.length} 个步骤</span>
              <span className="h-2.5 w-px bg-line" />
              <span className="inline-flex items-center gap-1 text-ok">
                <CheckCircle2 className="h-3 w-3" />
                {completedCount}/{nodes.length} 完成
              </span>
              {runningCount > 0 && (
                <span className="inline-flex items-center gap-1 text-warn">
                  <LoaderCircle className="h-3 w-3 animate-spin" />
                  {runningCount} 运行中
                </span>
              )}
              {failedCount > 0 && <span className="inline-flex items-center gap-1 font-medium text-flag">{failedCount} 失败</span>}
              {experimentSearchTask && (
                <>
                  <span className="h-2.5 w-px bg-line" />
                  <span className="inline-flex items-center gap-1 rounded-[3px] bg-plum-soft px-1.5 py-0.5 font-medium text-plum">
                    ToT 设计与环境异步
                  </span>
                  <span className="inline-flex items-center gap-1 rounded-[3px] bg-accent-soft px-1.5 py-0.5 font-medium text-accent-ink">
                    Search Agent ×{experimentParallelism}
                  </span>
                </>
              )}
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2 pt-0.5">
            {isExecuting ? (
              <button
                type="button"
                onClick={onCancel}
                title="取消计划"
                className="inline-flex h-8 items-center gap-1.5 rounded-[5px] border border-flag/30 bg-surface px-3 text-[12.5px] font-semibold text-flag transition-colors hover:bg-flag-soft active:scale-[0.98]"
              >
                <Square className="h-3 w-3 fill-current" />
                取消
              </button>
            ) : requiresApproval ? (
              <button
                type="button"
                onClick={onApproveAndRun}
                className="inline-flex h-8 items-center gap-1.5 rounded-[5px] bg-accent px-3 text-[12.5px] font-semibold text-white shadow-sm transition-colors hover:bg-accent-strong active:scale-[0.98]"
              >
                <ShieldCheck className="h-3.5 w-3.5" />
                审批并运行
              </button>
            ) : failedCount > 0 ? (
              <button
                type="button"
                onClick={onRetryFailed}
                className="inline-flex h-8 items-center gap-1.5 rounded-[5px] border border-warn/30 bg-surface px-3 text-[12.5px] font-semibold text-warn transition-colors hover:bg-warn-soft active:scale-[0.98]"
              >
                <RotateCcw className="h-3.5 w-3.5" />
                重试失败节点
              </button>
            ) : (
              <button
                type="button"
                onClick={onRunAll}
                disabled={pendingCount === 0}
                className="inline-flex h-8 items-center gap-1.5 rounded-[5px] bg-accent px-3 text-[12.5px] font-semibold text-white shadow-sm transition-colors hover:bg-accent-strong active:scale-[0.98] disabled:cursor-not-allowed disabled:bg-line disabled:text-faint disabled:shadow-none"
              >
                <Play className="h-3.5 w-3.5 fill-current" />
                {runAllText}
              </button>
            )}
          </div>
        </div>
      </header>

      {pendingRuntimeActions.length > 0 && (
        <div className="shrink-0 border-b border-warn/30 bg-warn-soft px-5 py-2.5">
          <div className="mb-1.5 flex items-center gap-1.5 text-[12px] font-semibold text-warn">
            <ShieldAlert className="h-3.5 w-3.5" />
            需要运行权限确认
          </div>
          <div className="space-y-1.5">
            {pendingRuntimeActions.map((action) => {
              const detailsText = describeRuntimeAction(action);
              const isThisActionBusy = runtimeDecision?.actionId === action.id;
              return (
                <div key={action.id} className="flex flex-wrap items-center justify-between gap-2 rounded-[5px] border border-warn/20 bg-surface px-2.5 py-1.5 text-[11.5px]">
                  <div className="min-w-0">
                    <div>
                      <span className="font-medium text-ink">{action.title}</span>
                      <span className="ml-1 text-muted">({action.kind})</span>
                      {action.risk && <span className="ml-1 text-[10.5px] uppercase text-warn">{action.risk}</span>}
                    </div>
                    {detailsText && <p className="mt-0.5 max-w-[42rem] break-words text-[10.5px] leading-4 text-muted" title={detailsText}>{detailsText}</p>}
                  </div>
                  <div className="flex shrink-0 items-center gap-1.5">
                    <button
                      type="button"
                      disabled={runtimeDecisionBusy}
                      aria-busy={isThisActionBusy && runtimeDecision?.decision === 'approve'}
                      onClick={() => void handleRuntimeActionDecision(action.id, 'approve')}
                      className="rounded-[4px] bg-accent px-2 py-1 text-[11px] font-semibold text-white hover:bg-accent-strong disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {isThisActionBusy && runtimeDecision?.decision === 'approve' ? '提交中…' : '允许一次'}
                    </button>
                    <button
                      type="button"
                      disabled={runtimeDecisionBusy}
                      aria-busy={isThisActionBusy && runtimeDecision?.decision === 'reject'}
                      onClick={() => void handleRuntimeActionDecision(action.id, 'reject')}
                      className="rounded-[4px] border border-flag/30 px-2 py-1 text-[11px] font-medium text-flag hover:bg-flag-soft disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {isThisActionBusy && runtimeDecision?.decision === 'reject' ? '提交中…' : '拒绝'}
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      <div className="relative min-h-0 flex-1">
        {nodes.length === 0 && (
          <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center p-8">
            <div className="max-w-sm text-center">
              <div className="mx-auto mb-6 flex items-center justify-center" aria-hidden="true">
                <span className="h-10 w-16 rounded-[4px] border border-line bg-surface shadow-[var(--shadow-card)]" />
                <span className="mx-2 w-7 border-t border-dashed border-line-strong" />
                <span className="h-10 w-16 rounded-[4px] border border-accent/35 bg-accent-soft shadow-[var(--shadow-card)]" />
                <span className="mx-2 w-7 border-t border-dashed border-line-strong" />
                <span className="h-10 w-16 rounded-[4px] border border-line bg-surface shadow-[var(--shadow-card)]" />
              </div>
              <p className="font-serif text-[17px] font-semibold tracking-[-0.01em] text-ink">还没有执行图</p>
              <p className="mx-auto mt-2 max-w-[17rem] text-[12px] leading-6 text-muted">
                在左侧描述你的研究目标，系统会生成可执行的 DAG，并在右侧展示节点与实时状态。
              </p>
            </div>
          </div>
        )}
        {nodes.length > 0 && (
          <Suspense fallback={<GraphCanvasSkeleton />}>
            <GraphCanvas
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onNodeClick={onNodeClick}
            />
          </Suspense>
        )}
      </div>
    </div>
  );
});
