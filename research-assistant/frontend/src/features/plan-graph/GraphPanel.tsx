import { lazy, memo, Suspense, useEffect, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { GitBranch, Play, RotateCcw, ShieldAlert, ShieldCheck, Square } from 'lucide-react';
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
  plannerSource?: string | null;
  plannerFallbackReason?: string | null;
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

const nodeStep = (node: Node) => (typeof node.data.step === 'number' ? node.data.step : undefined);

const nodeStatusGlyph = (status: string) => {
  switch (status) {
    case 'completed':
      return '●';
    case 'in_progress':
      return '◐';
    case 'failed':
      return '✕';
    case 'blocked':
      return '▲';
    case 'awaiting_approval':
      return '⚠';
    case 'ready':
      return '◔';
    case 'canceled':
    case 'skipped':
      return '—';
    default:
      return '○';
  }
};

const nodeStatusColor = (status: string) => {
  switch (status) {
    case 'completed':
      return 'text-ok';
    case 'in_progress':
    case 'awaiting_approval':
      return 'text-warn';
    case 'failed':
      return 'text-flag';
    case 'ready':
      return 'text-accent';
    default:
      return 'text-faint';
  }
};

function GraphCanvasSkeleton() {
  return (
    <div className="instrument-grid absolute inset-0 flex items-center justify-center" aria-busy="true" aria-label="正在加载计划画布">
      <div className="flex items-center gap-2" aria-hidden="true">
        <span className="h-10 w-20 animate-pulse border border-line bg-surface" />
        <span className="h-px w-8 bg-line-strong" />
        <span className="h-10 w-20 animate-pulse border border-accent/40 bg-accent-soft" />
        <span className="h-px w-8 bg-line-strong" />
        <span className="h-10 w-20 animate-pulse border border-line bg-surface" />
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
    plannerSource,
    plannerFallbackReason,
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
    onOpenTask,
  } = props;
  const completedCount = nodes.filter((node) => node.data.status === 'completed').length;
  const runningCount = nodes.filter((node) => node.data.status === 'in_progress').length;
  const failedCount = nodes.filter((node) => node.data.status === 'failed').length;
  const pendingCount = nodes.filter((node) => node.data.task && !terminalNodeStatuses.has(String(node.data.status))).length;
  const intentLabel = intentContext ? intentLabels[intentContext.intent_type] ?? intentContext.intent_type : null;
  const subtitle = intentContext?.raw_intent || graphHint;
  const pendingRuntimeActions = (runtimeActions ?? []).filter((action) => action.status === 'pending');
  const [runtimeDecision, setRuntimeDecision] = useState<{ actionId: string; decision: 'approve' | 'reject' } | null>(null);
  const runtimeDecisionLockRef = useRef(false);
  const runtimeDecisionBusy = runtimeDecision !== null;
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [showPlanInfo, setShowPlanInfo] = useState(false);

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

  const selectNode = (nodeId: string) => {
    setSelectedNodeId(nodeId);
    const node = nodes.find((candidate) => candidate.id === nodeId);
    if (node) onNodeClick(null, node);
  };

  const activeIndex = nodes.findIndex((node) => node.id === selectedNodeId);
  const handleIndexKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft' && event.key !== 'Home' && event.key !== 'End') return;
    if (nodes.length === 0) return;
    event.preventDefault();
    const current = activeIndex < 0 ? 0 : activeIndex;
    const next = event.key === 'Home'
      ? 0
      : event.key === 'End'
        ? nodes.length - 1
        : (current + (event.key === 'ArrowRight' ? 1 : -1) + nodes.length) % nodes.length;
    const target = nodes[next];
    if (!target) return;
    selectNode(target.id);
    const container = event.currentTarget;
    window.requestAnimationFrame(() => {
      container.querySelectorAll<HTMLButtonElement>('button')[next]?.focus();
    });
  };

  useEffect(() => {
    if (selectedNodeId && !nodes.some((node) => node.id === selectedNodeId)) setSelectedNodeId(null);
  }, [nodes, selectedNodeId]);

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col bg-paper">
      <header className="shrink-0 border-b border-line bg-surface px-4 py-3">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-2">
              <GitBranch className="h-3.5 w-3.5 shrink-0 text-muted" />
              <h2 className="truncate font-display text-[15px] font-semibold tracking-[-0.01em] text-ink">{graphTitle}</h2>
              {intentLabel && (
                <span className="instrument-num shrink-0 border border-line bg-surface-muted px-1.5 py-0.5 text-[11px] font-medium text-ink-soft">
                  {intentLabel}
                </span>
              )}
              {outcome && (
                <span className="instrument-num shrink-0 border border-line px-1.5 py-0.5 text-[11px] font-semibold text-ink-soft">
                  {outcomeLabels[outcome] ?? outcome}
                </span>
              )}
              {plannerSource === 'template' && (
                <span
                  className="instrument-num shrink-0 border border-warn/30 bg-warn-soft px-1.5 py-0.5 text-[11px] font-medium text-warn"
                  title={plannerFallbackReason || '该计划由模板规划生成'}
                >
                  模板规划
                </span>
              )}
            </div>
            <p className="mt-1 max-w-3xl truncate text-[12.5px] leading-5 text-muted" title={subtitle}>
              {subtitle}
            </p>
          </div>

          <div className="flex shrink-0 items-center gap-2 pt-0.5">
            {isExecuting ? (
              <button
                type="button"
                onClick={onCancel}
                title="取消计划"
                className="inline-flex h-7 items-center gap-1.5 border border-flag/40 bg-surface px-2.5 text-[12.5px] font-semibold text-flag transition-colors hover:bg-flag-soft active:translate-y-px"
              >
                <Square className="h-2.5 w-2.5 fill-current" />
                取消计划
              </button>
            ) : requiresApproval ? (
              <button
                type="button"
                onClick={onApproveAndRun}
                className="inline-flex h-7 items-center gap-1.5 bg-accent px-2.5 text-[12.5px] font-semibold text-white transition-colors hover:bg-accent-strong active:translate-y-px"
              >
                <ShieldCheck className="h-3.5 w-3.5" />
                审批并运行
              </button>
            ) : failedCount > 0 ? (
              <button
                type="button"
                onClick={onRetryFailed}
                className="inline-flex h-7 items-center gap-1.5 border border-warn/40 bg-surface px-2.5 text-[12.5px] font-semibold text-warn transition-colors hover:bg-warn-soft active:translate-y-px"
              >
                <RotateCcw className="h-3.5 w-3.5" />
                重试失败节点
              </button>
            ) : (
              <button
                type="button"
                onClick={onRunAll}
                disabled={pendingCount === 0 || pendingRuntimeActions.length > 0}
                title={pendingRuntimeActions.length > 0 ? '先处理运行权限确认' : undefined}
                className="inline-flex h-7 items-center gap-1.5 bg-accent px-2.5 text-[12.5px] font-semibold text-white transition-colors hover:bg-accent-strong active:translate-y-px disabled:cursor-not-allowed disabled:bg-surface-strong disabled:text-faint"
              >
                <Play className="h-3 w-3 fill-current" />
                {runAllText}
              </button>
            )}
            <button
              type="button"
              onClick={() => setShowPlanInfo((value) => !value)}
              aria-expanded={showPlanInfo}
              className="flex h-7 w-7 items-center justify-center border border-line bg-surface text-muted transition-colors hover:border-line-strong hover:text-ink"
              title="计划信息"
              aria-label="计划信息"
            >
              ⋯
            </button>
          </div>
        </div>

        {showPlanInfo && (
          <div className="ra-fade-up mt-2 grid grid-cols-2 gap-x-6 gap-y-1 border-t border-line pt-2 text-[12px] sm:grid-cols-4">
            <span className="text-faint">意图类型</span>
            <span className="text-ink-soft">{intentLabel ?? '未识别'}</span>
            <span className="text-faint">规划来源</span>
            <span className="text-ink-soft">{plannerSource === 'template' ? '模板规划' : plannerSource === 'agent' ? 'Agent 规划' : '未知'}</span>
            <span className="text-faint">运行权限</span>
            <span className="text-ink-soft">{runtimePermissionMode === 'full_access' ? '完全权限' : '确认模式'}</span>
            <span className="text-faint">结果状态</span>
            <span className="text-ink-soft">{outcome ? outcomeLabels[outcome] ?? outcome : '尚未产出'}</span>
          </div>
        )}
      </header>

      <dl className="research-assistant-meta-strip grid shrink-0 grid-cols-5 border-b border-line bg-surface">
        <div className="border-r border-line px-3.5 py-2">
          <dt className="instrument-kicker">步骤</dt>
          <dd className="instrument-num mt-0.5 text-[12.5px] text-ink">{nodes.length}</dd>
        </div>
        <div className="border-r border-line px-3.5 py-2">
          <dt className="instrument-kicker">已完成</dt>
          <dd className="instrument-num mt-0.5 text-[12.5px] text-ink">
            <span className="text-ok">{completedCount}</span>
            <span className="text-faint"> / {nodes.length}</span>
          </dd>
        </div>
        <div className="border-r border-line px-3.5 py-2">
          <dt className="instrument-kicker">进行中</dt>
          <dd className="instrument-num mt-0.5 text-[12.5px] text-ink">{runningCount}</dd>
        </div>
        <div className="border-r border-line px-3.5 py-2">
          <dt className="instrument-kicker">待处理</dt>
          <dd className="mt-0.5 text-[12.5px] text-ink">
            {pendingRuntimeActions.length > 0
              ? <span className="font-semibold text-warn">{pendingRuntimeActions.length} 项授权</span>
              : failedCount > 0
                ? <span className="font-semibold text-flag">{failedCount} 个失败</span>
                : <span className="text-muted">无</span>}
          </dd>
        </div>
        <div className="px-3.5 py-2">
          <dt className="instrument-kicker">权限</dt>
          <dd className="mt-0.5 text-[12.5px] text-ink-soft">{runtimePermissionMode === 'full_access' ? '完全权限' : '确认模式'}</dd>
        </div>
      </dl>

      {pendingRuntimeActions.length > 0 && (
        <div className="shrink-0 border-b border-line bg-warn-soft px-4 py-2.5">
          <div className="mb-1.5 flex items-center gap-1.5 text-[12px] font-semibold text-warn">
            <ShieldAlert className="h-3.5 w-3.5" />
            需要运行权限确认
            <span className="instrument-num ml-1 font-normal text-muted">{pendingRuntimeActions.length} 项</span>
          </div>
          <div className="space-y-1.5">
            {pendingRuntimeActions.map((action) => {
              const detailsText = describeRuntimeAction(action);
              const isThisActionBusy = runtimeDecision?.actionId === action.id;
              return (
                <div key={action.id} className="flex flex-wrap items-center justify-between gap-2 border border-warn/25 border-l-2 border-l-warn bg-surface px-2.5 py-1.5">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-baseline gap-x-2 text-[12.5px]">
                      <span className="font-semibold text-ink">{action.title}</span>
                      <span className="instrument-num text-[11px] text-faint">{action.kind}</span>
                      {action.risk && <span className="instrument-num text-[11px] uppercase text-warn">{action.risk}</span>}
                    </div>
                    {detailsText && <p className="mt-0.5 max-w-[42rem] break-words text-[11.5px] leading-4 text-muted" title={detailsText}>{detailsText}</p>}
                  </div>
                  <div className="flex shrink-0 items-center gap-1.5">
                    <button
                      type="button"
                      disabled={runtimeDecisionBusy}
                      aria-busy={isThisActionBusy && runtimeDecision?.decision === 'reject'}
                      onClick={() => void handleRuntimeActionDecision(action.id, 'reject')}
                      className="h-6 border border-flag/40 bg-surface px-2 text-[11.5px] font-medium text-flag transition-colors hover:bg-flag-soft disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {isThisActionBusy && runtimeDecision?.decision === 'reject' ? '提交中…' : '拒绝'}
                    </button>
                    <button
                      type="button"
                      disabled={runtimeDecisionBusy}
                      aria-busy={isThisActionBusy && runtimeDecision?.decision === 'approve'}
                      onClick={() => void handleRuntimeActionDecision(action.id, 'approve')}
                      className="h-6 bg-accent px-2 text-[11.5px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {isThisActionBusy && runtimeDecision?.decision === 'approve' ? '提交中…' : '允许一次'}
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {missingRequirements && missingRequirements.length > 0 && (
        <div className="shrink-0 border-b border-line bg-flag-soft px-4 py-2">
          <p className="text-[12px] font-semibold text-flag">缺少必要条件</p>
          <ul className="mt-1 space-y-0.5">
            {missingRequirements.map((item, index) => (
              <li key={`${item.kind}-${index}`} className="text-[12px] leading-5 text-ink-soft">
                <span className="font-medium">{item.name || item.kind}</span>
                {item.detail ? <span className="text-muted"> — {item.detail}</span> : null}
                {item.remediation ? <span className="text-faint"> · 建议：{item.remediation}</span> : null}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="relative min-h-0 flex-1">
        {nodes.length === 0 && (
          <div className="instrument-grid absolute inset-0 z-10 flex items-center justify-center p-8">
            <div className="max-w-sm">
              <div className="flex items-end gap-1.5" aria-hidden="true">
                <span className="block h-8 w-14 border border-line bg-surface" />
                <span className="mb-3 h-px w-5 bg-line-strong" />
                <span className="block h-8 w-14 border border-accent/50 bg-accent-soft" />
                <span className="mb-3 h-px w-5 bg-line-strong" />
                <span className="block h-8 w-14 border border-line bg-surface" />
              </div>
              <p className="mt-5 text-[14px] font-semibold text-ink">尚无运行</p>
              <p className="mt-1 text-[12.5px] leading-5 text-muted">
                在左侧描述研究目标。系统会生成可执行的执行图，节点状态、日志与产物都在此处呈现。
              </p>
              <ul className="mt-3 border-t border-line">
                <li className="border-b border-line py-1.5 text-[12px] text-ink-soft"><span className="instrument-num mr-2 text-faint">01</span>复现论文核心方法并跑通最小测试</li>
                <li className="border-b border-line py-1.5 text-[12px] text-ink-soft"><span className="instrument-num mr-2 text-faint">02</span>对两个 RAG 框架做冻结指标评测</li>
                <li className="border-b border-line py-1.5 text-[12px] text-ink-soft"><span className="instrument-num mr-2 text-faint">03</span>核查论文主张与证据链</li>
              </ul>
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

        {isExecuting && (
          <div className="pointer-events-none absolute inset-x-0 top-0 z-10 h-0.5 overflow-hidden bg-surface-strong" aria-hidden="true">
            <div className="ra-pulse h-full w-1/3 bg-accent" />
          </div>
        )}
      </div>

      {nodes.length > 0 && (
        <div
          className="research-assistant-node-index flex shrink-0 items-stretch border-t border-line bg-surface"
          role="toolbar"
          aria-label="节点索引"
          aria-orientation="horizontal"
          onKeyDown={handleIndexKeyDown}
        >
          {nodes.map((node, index) => {
            const status = String(node.data.status);
            const task = node.data.task as Task | undefined;
            const isSelected = selectedNodeId === node.id;
            return (
              <button
                key={node.id}
                type="button"
                onClick={() => selectNode(node.id)}
                onDoubleClick={() => onOpenTask?.(node.id)}
                aria-label={`${(nodeStep(node) ?? index + 1).toString().padStart(2, '0')} ${task?.Name ?? node.id}，${status}`}
                className={`relative flex min-w-0 flex-1 flex-col gap-1 border-r border-line px-2.5 py-2 text-left transition-colors last:border-r-0 hover:bg-surface-muted ${isSelected ? 'bg-surface-strong' : ''}`}
                aria-current={isSelected ? 'true' : undefined}
              >
                {isSelected && <span className="absolute inset-x-0 top-0 h-0.5 bg-accent" aria-hidden="true" />}
                <span className="flex items-center gap-1.5">
                  <span className="instrument-num text-[11px] text-faint">{(nodeStep(node) ?? index + 1).toString().padStart(2, '0')}</span>
                  <span className={`text-[11px] font-semibold ${nodeStatusColor(status)}`} aria-hidden="true">{nodeStatusGlyph(status)}</span>
                </span>
                <span className="line-clamp-2 text-[11.5px] font-medium leading-[1.35] text-ink-soft">{task?.Name ?? node.id}</span>
                <span className="mt-auto block h-0.5 w-full bg-surface-strong" aria-hidden="true">
                  <span
                    className={`block h-full ${status === 'completed' ? 'bg-ok' : status === 'in_progress' || status === 'awaiting_approval' ? 'bg-warn' : status === 'failed' ? 'bg-flag' : 'bg-transparent'}`}
                    style={{ width: status === 'completed' ? '100%' : status === 'in_progress' || status === 'awaiting_approval' ? '48%' : '0%' }}
                  />
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
});