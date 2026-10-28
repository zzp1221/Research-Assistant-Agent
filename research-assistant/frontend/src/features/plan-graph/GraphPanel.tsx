import { lazy, memo, Suspense, useEffect, useRef, useState } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { Eye, Play, RotateCcw, ShieldCheck, Square } from 'lucide-react';
import type { IntentContext, MissingRequirement, RuntimeAction, Verification } from '../../contracts/api';
import type { Task } from '../../contracts/api';
import { getProofMark } from '../shared/agentVisuals';
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

const statusInk = (status: string) => {
  switch (status) {
    case 'completed':
      return 'text-ok';
    case 'in_progress':
    case 'awaiting_approval':
      return 'text-warn';
    case 'failed':
      return 'text-proof';
    case 'ready':
      return 'text-ink';
    default:
      return 'text-muted';
  }
};

function GraphCanvasSkeleton() {
  return (
    <div className="plate-frame absolute inset-0 flex items-center justify-center" aria-busy="true" aria-label="正在加载执行图">
      <div className="flex items-center gap-2" aria-hidden="true">
        <span className="h-10 w-20 animate-pulse border border-line bg-surface" />
        <span className="h-px w-8 bg-line-strong" />
        <span className="h-10 w-20 animate-pulse border border-ink/40 bg-surface" />
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
  const [showColophon, setShowColophon] = useState(false);

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

  const selectNode = (nodeId: string) => {
    setSelectedNodeId(nodeId);
    const node = nodes.find((candidate) => candidate.id === nodeId);
    if (node) onNodeClick(null, node);
  };

  const handleCanvasNodeClick = (event: unknown, node: Node) => {
    setSelectedNodeId(node.id);
    onNodeClick(event, node);
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
      <header className="shrink-0 border-b border-line bg-surface px-5 py-3">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
              <h2 className="font-serif text-[17px] font-semibold leading-6 tracking-[-0.01em] text-ink">{graphTitle}</h2>
              <span className="journal-num text-[11px] text-muted">{planId ? planId.slice(0, 8).toUpperCase() : 'PLAN'}</span>
              {intentLabel && <span className="journal-caps text-[10px]">〔{intentLabel}〕</span>}
              {outcome && <span className="proof-stamp">{outcomeLabels[outcome] ?? outcome}</span>}
              <VerificationBadge verification={verification} onOpenSettings={onOpenModelSettings} />
              {plannerSource === 'template' && (
                <span className="journal-caps text-[10px] text-warn" title={plannerFallbackReason || '该计划由模板规划生成'}>
                  ◦ 模板规划
                </span>
              )}
            </div>
            <p className="mt-1 max-w-3xl truncate font-serif text-[13px] leading-6 text-ink-soft" title={subtitle}>
              {subtitle}
            </p>
          </div>

          <div className="flex shrink-0 items-center gap-2 pt-0.5">
            {isExecuting ? (
              <button
                type="button"
                onClick={onCancel}
                title="取消计划"
                className="inline-flex h-7 items-center gap-1.5 border border-proof bg-surface px-2.5 text-[12px] font-semibold text-proof transition-colors hover:bg-proof-soft active:translate-y-px"
              >
                <Square className="h-2.5 w-2.5 fill-current" />
                取消计划
              </button>
            ) : requiresApproval ? (
              <button
                type="button"
                onClick={onApproveAndRun}
                className="inline-flex h-7 items-center gap-1.5 bg-ink px-2.5 text-[12px] font-semibold text-paper transition-colors hover:bg-black active:translate-y-px"
              >
                <ShieldCheck className="h-3.5 w-3.5" />
                审批并运行
              </button>
            ) : failedCount > 0 ? (
              <button
                type="button"
                onClick={onRetryFailed}
                className="inline-flex h-7 items-center gap-1.5 border border-warn bg-surface px-2.5 text-[12px] font-semibold text-warn transition-colors hover:bg-warn-soft active:translate-y-px"
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
                className="inline-flex h-7 items-center gap-1.5 bg-ink px-2.5 text-[12px] font-semibold text-paper transition-colors hover:bg-black active:translate-y-px disabled:cursor-not-allowed disabled:border-line disabled:bg-[#e6e0d3] disabled:text-[#4a4238]"
              >
                <Play className="h-3 w-3 fill-current" />
                {runAllText}
              </button>
            )}
            {nodes.length > 0 && (
              <button
                type="button"
                onClick={() => {
                  const target = nodes.find((node) => node.id === selectedNodeId) ?? nodes[0];
                  if (target) selectNode(target.id);
                }}
                aria-label="查看节点详情"
                title="查看节点详情"
                className="inline-flex h-7 items-center gap-1.5 border border-line-strong bg-surface px-2.5 text-[12px] font-semibold text-ink-soft transition-colors hover:border-ink hover:text-ink active:translate-y-px md:hidden"
              >
                <Eye className="h-3.5 w-3.5" />
                查看节点
              </button>
            )}
            <button
              type="button"
              onClick={() => setShowColophon((value) => !value)}
              aria-expanded={showColophon}
              className="flex h-7 w-7 items-center justify-center border border-line bg-surface text-muted transition-colors hover:border-ink hover:text-ink"
              title="版本记录"
              aria-label="版本记录"
            >
              ⋯
            </button>
          </div>
        </div>

        {showColophon && (
          <dl className="proof-fade mt-2 grid grid-cols-2 gap-x-6 gap-y-1 border-t border-line pt-2 journal-num text-[11px] sm:grid-cols-4">
            <dt className="text-muted">意图类型</dt>
            <dd className="text-ink-soft">{intentLabel ?? '未识别'}</dd>
            <dt className="text-muted">规划来源</dt>
            <dd className="text-ink-soft">{plannerSource === 'template' ? '模板规划' : plannerSource === 'agent' ? 'Agent 规划' : '未知'}</dd>
            <dt className="text-muted">复杂度</dt>
            <dd className="text-ink-soft" data-testid="plan-complexity">{complexity ?? '未评估'}</dd>
            <dt className="text-muted">规划路径</dt>
            <dd className="text-ink-soft" data-testid="plan-planning-route">
              {planningRoute === 'fast_path'
                ? '快速路径'
                : planningRoute === 'standard_template'
                  ? '标准模板'
                  : planningRoute === 'agent_augmented'
                    ? 'Agent 增强'
                    : planningRoute === 'template_fallback'
                      ? '模板回退'
                      : '未知'}
            </dd>
            <dt className="text-muted">模板变体</dt>
            <dd className="text-ink-soft" data-testid="plan-template-variant">{templateVariant ?? '—'}</dd>
            <dt className="text-muted">运行权限</dt>
            <dd className="text-ink-soft">{runtimePermissionMode === 'full_access' ? '完全权限' : '确认模式'}</dd>
            <dt className="text-muted">运行方式</dt>
            <dd className="text-ink-soft" data-testid="plan-run-trigger">
              {runTrigger === 'auto' ? '自动运行' : runTrigger === 'manual' ? '手动运行' : '未运行'}
            </dd>
            <dt className="text-muted">结果状态</dt>
            <dd className="text-ink-soft">{outcome ? outcomeLabels[outcome] ?? outcome : '尚未产出'}</dd>
            <dt className="text-muted">校验</dt>
            <dd className="text-ink-soft">
              {verification ? (
                <VerificationBadge verification={verification} onOpenSettings={onOpenModelSettings} />
              ) : (
                '未校验（旧计划或尚未产出图像）'
              )}
            </dd>
          </dl>
        )}
      </header>

      <dl className="research-assistant-plate-meta grid shrink-0 grid-cols-5 border-b border-line bg-surface-muted">
        <div className="border-r border-line px-4 py-2">
          <dt className="journal-caps text-[10px]">节点</dt>
          <dd className="journal-num mt-0.5 text-[12.5px] text-ink">{nodes.length}</dd>
        </div>
        <div className="border-r border-line px-4 py-2">
          <dt className="journal-caps text-[10px]">已完成</dt>
          <dd className="journal-num mt-0.5 text-[12.5px] text-ink">
            <span className="text-ok">{completedCount}</span>
            <span className="text-muted"> / {nodes.length}</span>
          </dd>
        </div>
        <div className="border-r border-line px-4 py-2">
          <dt className="journal-caps text-[10px]">运行中</dt>
          <dd className="journal-num mt-0.5 text-[12.5px] text-ink">{runningCount}</dd>
        </div>
        <div className="border-r border-line px-4 py-2">
          <dt className="journal-caps text-[10px]">待处理</dt>
          <dd className="mt-0.5 text-[12.5px] text-ink">
            {pendingRuntimeActions.length > 0
              ? <span className="font-semibold text-proof">{pendingRuntimeActions.length} 项待批</span>
              : failedCount > 0
                ? <span className="font-semibold text-proof">{failedCount} 项失败</span>
                : <span className="text-muted">无</span>}
          </dd>
        </div>
        <div className="px-4 py-2">
          <dt className="journal-caps text-[10px]">权限</dt>
          <dd className="mt-0.5 text-[12.5px] text-ink-soft">{runtimePermissionMode === 'full_access' ? '完全权限' : '确认模式'}</dd>
        </div>
      </dl>

      {pendingRuntimeActions.length > 0 && (
        <div className="shrink-0 border-b border-line bg-proof-soft px-5 py-2.5">
          <div className="mb-1.5 flex items-baseline gap-2">
            <span className="journal-caps text-[10.5px] font-semibold text-proof">需要你批准的操作</span>
            <span className="journal-num text-[11px] text-muted">{pendingRuntimeActions.length} 项</span>
          </div>
          <div className="space-y-1.5">
            {pendingRuntimeActions.map((action) => {
              const detailsText = describeRuntimeAction(action);
              const isThisActionBusy = runtimeDecision?.actionId === action.id;
              return (
                <div key={action.id} className="flex flex-wrap items-center justify-between gap-2 border border-proof/25 bg-surface px-3 py-2">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-baseline gap-x-2">
                      <span className="font-serif text-[13px] font-semibold text-ink">{action.title}</span>
                      <span className="journal-caps text-[10px]">{action.kind}</span>
                      {action.risk && <span className="journal-caps text-[10px] text-proof">{action.risk}</span>}
                    </div>
                    {detailsText && <p className="mt-0.5 max-w-[42rem] break-words journal-num text-[11px] leading-4 text-ink-soft" title={detailsText}>{detailsText}</p>}
                  </div>
                  <div className="flex shrink-0 items-center gap-1.5">
                    <button
                      type="button"
                      disabled={runtimeDecisionBusy}
                      aria-busy={isThisActionBusy && runtimeDecision?.decision === 'reject'}
                      onClick={() => void handleRuntimeActionDecision(action.id, 'reject')}
                      className="h-6 border border-line-strong bg-surface px-2 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-proof hover:text-proof disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      {isThisActionBusy && runtimeDecision?.decision === 'reject' ? '提交中…' : '拒绝'}
                    </button>
                    <button
                      type="button"
                      disabled={runtimeDecisionBusy}
                      aria-busy={isThisActionBusy && runtimeDecision?.decision === 'approve'}
                      onClick={() => void handleRuntimeActionDecision(action.id, 'approve')}
                      className="h-6 bg-ink px-2 text-[11.5px] font-semibold text-paper transition-colors hover:bg-black disabled:cursor-not-allowed disabled:opacity-50"
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
        <section className="shrink-0 border-b border-line bg-proof-soft px-5 py-2" aria-label="缺失材料">
          <p className="journal-caps text-[10.5px] font-semibold text-proof">
            缺失材料 · {missingRequirements.length} 项
          </p>
          <ul className="mt-1 space-y-1.5">
            {missingRequirements.map((item, index) => {
              const remediation = item.remediation || '请补充该材料后重试。';
              return (
                <li key={`${item.kind}-${index}`} className="border-l border-proof/30 pl-2.5">
                  <div className="flex flex-wrap items-baseline gap-x-2 font-serif text-[12.5px] leading-5 text-ink-soft">
                    <span className="journal-caps text-[10px] text-proof">{item.kind}</span>
                    <span className="font-semibold text-ink">{item.name || item.kind}</span>
                  </div>
                  {item.detail ? (
                    <p className="font-serif text-[12px] leading-5 text-muted">{item.detail}</p>
                  ) : null}
                  <p className="font-serif text-[11.5px] leading-5 text-ink-soft">
                    <span className="font-semibold text-proof">修复建议: </span>
                    {remediation}
                  </p>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      <div className="relative min-h-0 flex-1 p-4">
        {nodes.length === 0 && (
          <div className="plate-frame absolute inset-4 flex items-center justify-center p-8">
            <div className="max-w-md">
              <div className="journal-caps text-[10px]">执行拓扑（待生成）</div>
              <p className="mt-3 font-serif text-[17px] font-semibold leading-7 text-ink">还没有执行图</p>
              <p className="mt-1.5 font-serif text-[13px] leading-6 text-ink-soft">
                在左侧写下研究目标。系统会先固定指标与约束，再把执行拆成可审计的节点，逐条画在这张图上。
              </p>
              <ol className="mt-4 border-t border-line">
                <li className="border-b border-line py-1.5 font-serif text-[12.5px] text-ink-soft"><span className="journal-num mr-2 text-muted">一</span>复现论文核心方法并跑通最小测试</li>
                <li className="border-b border-line py-1.5 font-serif text-[12.5px] text-ink-soft"><span className="journal-num mr-2 text-muted">二</span>对两个 RAG 框架做冻结指标评测</li>
                <li className="border-b border-line py-1.5 font-serif text-[12.5px] text-ink-soft"><span className="journal-num mr-2 text-muted">三</span>核查论文主张与证据链</li>
              </ol>
            </div>
          </div>
        )}
        {nodes.length > 0 && (
          <div className="plate-frame absolute inset-4 overflow-hidden">
            <Suspense fallback={<GraphCanvasSkeleton />}>
              <GraphCanvas
                nodes={nodes}
                edges={edges}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onNodeClick={handleCanvasNodeClick}
              />
            </Suspense>
            <div className="pointer-events-none absolute inset-x-0 bottom-0 border-t border-line bg-surface/90 px-3 py-1.5">
              <p className="truncate font-serif text-[11.5px] leading-5 text-ink-soft">
                <span className="font-semibold text-ink">图 1</span>
                {'　'}执行拓扑：{nodes.length} 个节点，{edges.length} 条依赖。实线为控制流，虚线为数据流；符号见图例。
              </p>
            </div>
          </div>
        )}

        {isExecuting && (
          <div className="pointer-events-none absolute inset-x-4 top-4 z-10 h-0.5 overflow-hidden bg-line" aria-hidden="true">
            <div className="proof-pulse h-full w-1/3 bg-proof" />
          </div>
        )}
      </div>

      {nodes.length > 0 && (
        <div className="shrink-0 border-t border-ink bg-surface">
          <div className="flex items-baseline justify-between px-5 pb-1.5 pt-2">
            <span className="journal-caps text-[10px]">执行记录</span>
            <span className="journal-caps text-[10px]">按计划顺序 · 点击行查看详情</span>
          </div>
          <div
            className="research-assistant-node-table"
            role="toolbar"
            aria-label="执行记录摘要"
            aria-orientation="horizontal"
            onKeyDown={handleIndexKeyDown}
          >
            <div className="grid grid-cols-[3.2rem_minmax(0,1fr)_9rem_4.5rem] border-t border-line bg-surface-muted journal-caps text-[10px]">
              <span className="border-r border-line px-2 py-1">序号</span>
              <span className="border-r border-line px-2 py-1">节点</span>
              <span className="border-r border-line px-2 py-1">负责者</span>
              <span className="px-2 py-1 text-right">状态</span>
            </div>
            <div className="max-h-[7.25rem] overflow-y-auto">
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
                    aria-current={isSelected ? 'true' : undefined}
                    className={`grid w-full grid-cols-[3.2rem_minmax(0,1fr)_9rem_4.5rem] border-b border-line/70 text-left transition-colors last:border-b-0 hover:bg-surface-muted ${isSelected ? 'bg-surface-muted' : ''}`}
                  >
                    <span className={`border-r border-line/70 px-2 py-1 journal-num text-[11px] ${isSelected ? 'text-proof' : 'text-muted'}`}>
                      {String(nodeStep(node) ?? index + 1).padStart(2, '0')}
                    </span>
                    <span className="truncate border-r border-line/70 px-2 py-1 font-serif text-[12px] text-ink-soft">{task?.Name ?? node.id}</span>
                    <span className="truncate border-r border-line/70 px-2 py-1 journal-caps text-[10px] text-muted">{task?.AssignedTo ? task.AssignedTo.replace('_agent', '').replace('research_coding', '科研 Coding').replace('benchmark', '评测').replace('sandbox', '沙箱').replace('librarian', '文献').replace('general', '通用').replace('data', '数据').replace('coder', '代码').replace('answer', '回答') : '—'}</span>
                    <span className={`flex items-center justify-end gap-1 px-2 py-1 journal-caps text-[10px] ${statusInk(status)}`}>
                      <span aria-hidden="true">{getProofMark(status)}</span>
                      {status === 'completed' ? '已完成' : status === 'in_progress' ? '运行中' : status === 'failed' ? '失败' : status === 'awaiting_approval' ? '待批准' : status === 'blocked' ? '受阻' : status === 'canceled' ? '已取消' : status === 'skipped' ? '已跳过' : status === 'ready' ? '等待中' : '待运行'}
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        </div>
      )}
    </div>
  );
});