import { Background, Controls, ReactFlow } from '@xyflow/react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { CheckCircle2, GitBranch, LoaderCircle, Play, RotateCcw, ShieldCheck, Square } from 'lucide-react';
import type { IntentContext } from '../../contracts/api';
import type { Task } from '../../contracts/api';

interface GraphPanelProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  onNodeClick: (_: unknown, node: Node) => void;
  intentContext: IntentContext | null;
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

export function GraphPanel(props: GraphPanelProps) {
  const {
    nodes,
    edges,
    onNodesChange,
    onEdgesChange,
    onNodeClick,
    intentContext,
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
            </div>
            <p className="mt-1 max-w-3xl truncate text-[11.5px] leading-4 text-muted" title={subtitle}>
              {subtitle}
            </p>
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
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          nodesConnectable={false}
          nodesDraggable={false}
          fitView
          fitViewOptions={{ padding: 0.16, minZoom: 0.72, maxZoom: 1.02 }}
          minZoom={0.2}
          className="bg-paper"
        >
          <Background color="#d8d1c0" gap={22} size={1.2} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
    </div>
  );
}
