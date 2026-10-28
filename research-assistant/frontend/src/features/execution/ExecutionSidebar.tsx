import { lazy, Suspense, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from 'react';
import {
  ChartNoAxesCombined,
  Check,
  Code,
  Copy,
  Download,
  Eye,
  GitBranch,
  Loader2,
  Maximize2,
  Play,
  RotateCcw,
  ShieldAlert,
  TerminalSquare,
  X,
} from 'lucide-react';
import type { ExecutionDisplayMode } from '../../app/hooks/useResearchAssistantRuntime';
import type { Task } from '../../contracts/api';
import { formatApiError } from '../../services/api/apiError';
import { parseBenchmarkReport } from '../benchmark/benchmarkReport';
import { VerificationBadge, VerificationDetails } from '../verification/VerificationBadge';
import { getAgentIcon, getAgentLabel } from '../shared/agentVisuals';
import { DEFAULT_LOG_TAIL, filterLogs, splitLogLines, windowLogs, type LogLevel } from './logWindow';

const BenchmarkReportView = lazy(() => import('../benchmark/BenchmarkReportView').then((module) => ({ default: module.BenchmarkReportView })));
const AutoResearchTrialView = lazy(() => import('../autoresearch/AutoResearchTrialView').then((module) => ({ default: module.AutoResearchTrialView })));
const ExperimentResearchView = lazy(() => import('../experiment/ExperimentResearchView').then((module) => ({ default: module.ExperimentResearchView })));
const AblationTreeView = lazy(() => import('../ablation/AblationTreeView').then((module) => ({ default: module.AblationTreeView })));
const ClaimEvidenceGraphView = lazy(() => import('../claim-evidence/ClaimEvidenceGraphView').then((module) => ({ default: module.ClaimEvidenceGraphView })));
const MarkdownReport = lazy(() => import('../shared/MarkdownReport'));

type CompactMode = Exclude<ExecutionDisplayMode, 'report-expanded' | 'plot-expanded' | 'evidence-expanded' | 'trials-expanded' | 'ablation-expanded'>;

interface ResultTab {
  mode: CompactMode;
  label: string;
  icon: ReactNode;
}

const benchmarkTaskTypes = new Set([
  'benchmark_validate', 'benchmark_execute', 'benchmark_contract_freeze',
  'benchmark_split_materialize', 'benchmark_dataset_audit', 'benchmark_adapter_preflight',
]);
const hasBenchmarkReport = (task: Task, structuredData: string, result: string): boolean =>
  benchmarkTaskTypes.has(task.Type || '') && Boolean(parseBenchmarkReport(structuredData || result));

const taskStatusLabels: Record<string, string> = {
  pending: '等待',
  ready: '就绪',
  in_progress: '运行中',
  completed: '已完成',
  failed: '失败',
  blocked: '已阻塞',
  canceled: '已取消',
  skipped: '已跳过',
  awaiting_approval: '待审批',
};

interface ExecutionSidebarProps {
  selectedTask: Task;
  width: string;
  isExecuting: boolean;
  requiresApproval: boolean;
  displayMode: ExecutionDisplayMode;
  executionLogs: string;
  executionResult: string;
  executionCode: string;
  executionStructuredData: string;
  executionImage: string;
  logsEndRef: RefObject<HTMLDivElement | null>;
  onClose: () => void;
  onExecute: () => void | Promise<unknown>;
  onChangeDisplayMode: (mode: ExecutionDisplayMode) => void;
  onOpenModelSettings?: () => void;
}

const resolveCompactMode = (displayMode: ExecutionDisplayMode): CompactMode => {
  if (displayMode === 'report-expanded') return 'report';
  if (displayMode === 'plot-expanded') return 'plot';
  if (displayMode === 'evidence-expanded') return 'evidence';
  if (displayMode === 'trials-expanded') return 'trials';
  if (displayMode === 'ablation-expanded') return 'ablation';
  return displayMode;
};

const reportAgents = new Set(['librarian_agent', 'data_agent', 'benchmark_agent', 'research_coding_agent']);

function LazyViewSkeleton({ label = '正在加载结果视图' }: { label?: string }) {
  return (
    <div className="flex h-full min-h-[12rem] items-center justify-center bg-surface" aria-busy="true" aria-label={label}>
      <span className="flex items-center gap-2 text-[11px] text-faint">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        {label}
      </span>
    </div>
  );
}

function LazyBenchmarkReport({ raw, expanded = false }: { raw: string; expanded?: boolean }) {
  return (
    <Suspense fallback={<LazyViewSkeleton label="正在加载评测报告" />}>
      <BenchmarkReportView raw={raw} expanded={expanded} />
    </Suspense>
  );
}

function LazyTrialView({ rawLedger, experiment = false, expanded = false }: { rawLedger: string; experiment?: boolean; expanded?: boolean }) {
  return (
    <Suspense fallback={<LazyViewSkeleton label="正在加载实验记录" />}>
      {experiment
        ? <ExperimentResearchView raw={rawLedger} expanded={expanded} />
        : <AutoResearchTrialView rawLedger={rawLedger} expanded={expanded} />}
    </Suspense>
  );
}

function LazyAblationView({ rawPlan, expanded = false }: { rawPlan: string; expanded?: boolean }) {
  return (
    <Suspense fallback={<LazyViewSkeleton label="正在加载方案树" />}>
      <AblationTreeView rawPlan={rawPlan} expanded={expanded} />
    </Suspense>
  );
}

function LazyEvidenceView({ rawGraph, expanded = false }: { rawGraph: string; expanded?: boolean }) {
  return (
    <Suspense fallback={<LazyViewSkeleton label="正在加载证据图" />}>
      <ClaimEvidenceGraphView rawGraph={rawGraph} expanded={expanded} />
    </Suspense>
  );
}

function ReportMarkdown({ children }: { children: string }) {
  return (
    <Suspense fallback={<div className="h-24 animate-pulse rounded-[2px] bg-paper-deep" aria-busy="true" aria-label="正在加载报告" />}>
      <MarkdownReport>{children}</MarkdownReport>
    </Suspense>
  );
}

function PanelHeader(props: { eyebrow: string; title: string; onClose: () => void; closeLabel: string }) {
  const { eyebrow, title, onClose, closeLabel } = props;
  return (
    <div className="flex shrink-0 items-start justify-between gap-4 border-b border-line bg-surface px-4 py-2.5">
      <div className="min-w-0">
        <div className="journal-caps text-[10px]">{eyebrow}</div>
        <h2 className="mt-0.5 truncate font-serif text-[15px] font-semibold leading-5 text-ink">{title}</h2>
      </div>
      <button
        type="button"
        onClick={onClose}
        className="flex h-7 w-7 shrink-0 items-center justify-center border border-transparent text-muted transition-colors hover:border-line-strong hover:bg-surface-muted hover:text-ink"
        title={closeLabel}
        aria-label={closeLabel}
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

export function ExecutionSidebar(props: ExecutionSidebarProps) {
  const {
    selectedTask,
    width,
    isExecuting,
    requiresApproval,
    displayMode,
    executionLogs,
    executionResult,
    executionCode,
    executionStructuredData,
    executionImage,
    logsEndRef,
    onClose,
    onExecute,
    onChangeDisplayMode,
    onOpenModelSettings,
  } = props;

  const activeMode = resolveCompactMode(displayMode);
  const isExpanded = displayMode.endsWith('-expanded');

  return (
    <div
      style={{ width }}
      className={`research-assistant-execution-sidebar proof-slide z-20 flex flex-col border-l border-ink bg-surface ${
        isExpanded ? 'absolute inset-0 shadow-[var(--shadow-float)]' : 'relative'
      }`}
    >
      {displayMode === 'evidence-expanded' ? (
        <ExpandedEvidenceView title={selectedTask.Name} rawGraph={executionStructuredData} onClose={() => onChangeDisplayMode('evidence')} />
      ) : displayMode === 'trials-expanded' ? (
        <ExpandedTrialView
          title={selectedTask.Name}
          rawLedger={executionStructuredData || executionResult}
          experiment={selectedTask.Type === 'experiment_run' || selectedTask.Type === 'experiment_validate'}
          onClose={() => onChangeDisplayMode('trials')}
        />
      ) : displayMode === 'ablation-expanded' ? (
        <ExpandedAblationView title={selectedTask.Name} rawPlan={executionStructuredData || executionResult} onClose={() => onChangeDisplayMode('ablation')} />
      ) : displayMode === 'plot-expanded' ? (
        <ExpandedPlotView executionImage={executionImage} onClose={() => onChangeDisplayMode('plot')} />
      ) : displayMode === 'report-expanded' ? (
        hasBenchmarkReport(selectedTask, executionStructuredData, executionResult) ? (
          <div className="flex min-h-0 flex-1 flex-col bg-surface">
            <PanelHeader eyebrow="评测报告" title={selectedTask.Name} onClose={() => onChangeDisplayMode('report')} closeLabel="退出全屏报告" />
            <div className="min-h-0 flex-1 overflow-y-auto">
              <LazyBenchmarkReport raw={executionStructuredData || executionResult} expanded />
            </div>
          </div>
        ) : (
          <ExpandedReportView title={selectedTask.Name} executionResult={executionResult} onClose={() => onChangeDisplayMode('report')} />
        )
      ) : (
        <ExecutionSidebarShell
          key={selectedTask.ID}
          selectedTask={selectedTask}
          isExecuting={isExecuting}
          requiresApproval={requiresApproval}
          activeMode={activeMode}
          executionLogs={executionLogs}
          executionResult={executionResult}
          executionCode={executionCode}
          executionStructuredData={executionStructuredData}
          executionImage={executionImage}
          logsEndRef={logsEndRef}
          onClose={onClose}
          onExecute={onExecute}
          onChangeDisplayMode={onChangeDisplayMode}
          onOpenModelSettings={onOpenModelSettings}
        />
      )}
    </div>
  );
}

interface ExpandedTrialViewProps {
  title: string;
  rawLedger: string;
  experiment?: boolean;
  onClose: () => void;
}

function ExpandedTrialView({ title, rawLedger, experiment = false, onClose }: ExpandedTrialViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      <PanelHeader
        eyebrow={experiment ? 'Scientific AutoResearch' : 'AutoResearch Trials'}
        title={title}
        onClose={onClose}
        closeLabel="退出全屏实验视图"
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        <div className="min-h-full overflow-hidden rounded-[2px] border border-line bg-surface">
          <LazyTrialView rawLedger={rawLedger} experiment={experiment} expanded />
        </div>
      </div>
    </div>
  );
}

interface ExpandedAblationViewProps {
  title: string;
  rawPlan: string;
  onClose: () => void;
}

function ExpandedAblationView({ title, rawPlan, onClose }: ExpandedAblationViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      <PanelHeader eyebrow="Ablation Thought Tree" title={title} onClose={onClose} closeLabel="退出全屏消融树" />
      <div className="min-h-0 flex-1 overflow-hidden p-4">
        <div className="h-full overflow-hidden rounded-[2px] border border-line bg-surface">
          <LazyAblationView rawPlan={rawPlan} expanded />
        </div>
      </div>
    </div>
  );
}

interface ExpandedEvidenceViewProps {
  title: string;
  rawGraph: string;
  onClose: () => void;
}

function ExpandedEvidenceView({ title, rawGraph, onClose }: ExpandedEvidenceViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      <PanelHeader eyebrow="Claim-to-Evidence Graph" title={title} onClose={onClose} closeLabel="退出全屏证据图" />
      <div className="min-h-0 flex-1 p-4">
        <div className="h-full overflow-hidden rounded-[2px] border border-line bg-surface">
          <LazyEvidenceView rawGraph={rawGraph} expanded />
        </div>
      </div>
    </div>
  );
}

interface ExpandedPlotViewProps {
  executionImage: string;
  onClose: () => void;
}

function ExpandedPlotView({ executionImage, onClose }: ExpandedPlotViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      <PanelHeader eyebrow="Figure" title="生成的图表" onClose={onClose} closeLabel="退出全屏图表" />
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-paper-deep/50 p-8">
        <img
          src={`data:image/png;base64,${executionImage}`}
          alt="Full Resolution Plot"
          className="max-h-full max-w-full rounded-[2px] border border-line bg-surface object-contain shadow-[var(--shadow-float)]"
        />
      </div>
    </div>
  );
}

interface ExpandedReportViewProps {
  title: string;
  executionResult: string;
  onClose: () => void;
}

function ExpandedReportView({ title, executionResult, onClose }: ExpandedReportViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-surface">
      <PanelHeader eyebrow="Analysis Report" title={title} onClose={onClose} closeLabel="退出全屏报告" />
      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-6">
        <article className="mx-auto max-w-[68ch] font-serif text-[15px] leading-[1.85] text-ink-soft prose prose-stone prose-headings:font-serif prose-headings:text-ink prose-strong:text-ink prose-code:font-sans prose-code:text-[13px] prose-code:text-accent-ink prose-pre:rounded-[2px] prose-pre:bg-[#12181d] prose-pre:text-[13px] prose-pre:text-[#dfe6ea] prose-img:rounded-[2px] prose-img:border prose-img:border-line">
          <ReportMarkdown>{executionResult}</ReportMarkdown>
        </article>
      </div>
    </div>
  );
}

interface ExecutionSidebarShellProps {
  selectedTask: Task;
  isExecuting: boolean;
  requiresApproval: boolean;
  activeMode: CompactMode;
  executionLogs: string;
  executionResult: string;
  executionCode: string;
  executionStructuredData: string;
  executionImage: string;
  logsEndRef: RefObject<HTMLDivElement | null>;
  onClose: () => void;
  onExecute: () => void | Promise<unknown>;
  onChangeDisplayMode: (mode: ExecutionDisplayMode) => void;
  onOpenModelSettings?: () => void;
}

const logLevelOptions: { value: LogLevel; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'error', label: '错误' },
  { value: 'warn', label: '警告' },
  { value: 'info', label: '信息' },
];

function ExecutionSidebarShell(props: ExecutionSidebarShellProps) {
  const {
    selectedTask,
    isExecuting,
    requiresApproval,
    activeMode,
    executionLogs,
    executionResult,
    executionCode,
    executionStructuredData,
    executionImage,
    logsEndRef,
    onClose,
    onExecute,
    onChangeDisplayMode,
    onOpenModelSettings,
  } = props;
  const [copied, setCopied] = useState(false);
  const [executeFailure, setExecuteFailure] = useState<{ taskId: string; error: unknown } | null>(null);
  const [logLevel, setLogLevel] = useState<LogLevel>('all');
  const [showAllLogs, setShowAllLogs] = useState(false);
  const logLines = useMemo(() => splitLogLines(executionLogs), [executionLogs]);
  const filteredLogLines = useMemo(() => filterLogs(logLines, logLevel), [logLines, logLevel]);
  const visibleLogWindow = useMemo(
    () => (showAllLogs ? { lines: filteredLogLines, hiddenCount: 0 } : windowLogs(filteredLogLines, DEFAULT_LOG_TAIL)),
    [filteredLogLines, showAllLogs],
  );
  // Prefer a structured failure, then a task-level backend error, and finally
  // the last streamed "[error]" line. All three are formatted through the same
  // mapper so the panel never echoes an unfiltered stack trace.
  const lastLogError = useMemo(() => {
    for (let index = logLines.length - 1; index >= 0; index -= 1) {
      const match = /\[(?:(?:plan|execution)\s+)?error\]\s*(.+)$/i.exec(logLines[index]);
      if (match?.[1]) return match[1].trim();
    }
    return '';
  }, [logLines]);
  const executeErrorMessage = executeFailure && executeFailure.taskId === selectedTask.ID ? executeFailure.error : null;
  const taskErrorMessage = selectedTask.Error || lastLogError;
  const apiFailure = executeErrorMessage !== null
    ? formatApiError(executeErrorMessage)
    : taskErrorMessage.trim() !== ''
      ? formatApiError(taskErrorMessage)
      : null;
  const ablationBudget = selectedTask.Type === 'ablation_design' ? selectedTask.Inputs : undefined;
  const hasAblationEvidence = selectedTask.Type === 'ablation_design' && Boolean(executionStructuredData || executionResult);
  const hasAutoResearchEvidence = (selectedTask.Type === 'autoresearch_run' || selectedTask.Type === 'autoresearch_validate') && Boolean(executionStructuredData || executionResult);
  const hasExperimentEvidence = (selectedTask.Type === 'experiment_run' || selectedTask.Type === 'experiment_validate') && Boolean(executionStructuredData || executionResult);
  const hasTrialEvidence = hasAutoResearchEvidence || hasExperimentEvidence;
  const hasBenchmarkEvidence = hasBenchmarkReport(selectedTask, executionStructuredData, executionResult);

  const tabItems: ResultTab[] = [
    { mode: 'logs', label: '日志', icon: <TerminalSquare className="h-3.5 w-3.5" /> },
    ...(executionCode ? [{ mode: 'code' as const, label: '代码', icon: <Code className="h-3.5 w-3.5" /> }] : []),
    ...(hasTrialEvidence
      ? [{
          mode: 'trials' as const,
          label: selectedTask.Type === 'autoresearch_validate' || selectedTask.Type === 'experiment_validate' ? '验收' : '实验',
          icon: <ChartNoAxesCombined className="h-3.5 w-3.5" />,
        }]
      : []),
    ...(hasAblationEvidence ? [{ mode: 'ablation' as const, label: '方案树', icon: <GitBranch className="h-3.5 w-3.5" /> }] : []),
    ...(executionImage ? [{ mode: 'plot' as const, label: '图表', icon: <Maximize2 className="h-3.5 w-3.5" /> }] : []),
    ...(selectedTask.Type === 'claim_evidence_build' && executionStructuredData
      ? [{ mode: 'evidence' as const, label: '证据图', icon: <GitBranch className="h-3.5 w-3.5" /> }]
      : []),
    ...((hasBenchmarkEvidence || (reportAgents.has(selectedTask.AssignedTo) && executionResult && !hasTrialEvidence && !hasAblationEvidence))
      ? [{ mode: 'report' as const, label: hasBenchmarkEvidence ? '评测报告' : '分析报告', icon: <Eye className="h-3.5 w-3.5" /> }]
      : []),
  ];
  const activeTabIndex = tabItems.findIndex((tab) => tab.mode === activeMode);

  const handleTabKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (tabItems.length === 0) return;
    let nextIndex: number | null = null;
    if (event.key === 'ArrowRight') nextIndex = (Math.max(activeTabIndex, 0) + 1) % tabItems.length;
    if (event.key === 'ArrowLeft') nextIndex = (Math.max(activeTabIndex, 0) - 1 + tabItems.length) % tabItems.length;
    if (event.key === 'Home') nextIndex = 0;
    if (event.key === 'End') nextIndex = tabItems.length - 1;
    if (nextIndex === null) return;
    event.preventDefault();
    const nextTab = tabItems[nextIndex];
    if (nextTab) {
      onChangeDisplayMode(nextTab.mode);
      window.requestAnimationFrame(() => document.getElementById(`execution-tab-${nextTab.mode}`)?.focus());
    }
  };

  const copyLogs = async () => {
    try {
      await navigator.clipboard.writeText(executionLogs);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard access can be blocked; the log remains selectable.
    }
  };

  const handleExecute = () => {
    setExecuteFailure(null);
    try {
      const result: unknown = onExecute();
      if (result && typeof (result as Promise<unknown>).then === 'function') {
        void (result as Promise<unknown>).catch((error: unknown) => {
          setExecuteFailure({ taskId: selectedTask.ID, error });
        });
      }
    } catch (error: unknown) {
      setExecuteFailure({ taskId: selectedTask.ID, error });
    }
  };

  const downloadLogs = () => {
    if (typeof URL.createObjectURL !== 'function') return;
    const blob = new Blob([executionLogs], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    const safeName = (selectedTask.Name || selectedTask.ID || 'execution')
      .replace(/[\\/:*?"<>|\s]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 48);
    anchor.href = url;
    anchor.download = `${safeName || 'execution'}.log`;
    anchor.rel = 'noopener';
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  return (
    <>
      <div className="flex shrink-0 items-start justify-between gap-3 border-b border-ink bg-surface px-4 py-2.5">
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <span className="journal-caps text-[10px]">节点详情</span>
            <span className="journal-num text-[10.5px] text-muted">{getAgentLabel(selectedTask.AssignedTo)}</span>
          </div>
          <h3 className="mt-0.5 truncate font-serif text-[14px] font-semibold leading-5 text-ink" title={selectedTask.Name}>{selectedTask.Name}</h3>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-[2px] border border-transparent text-muted transition-colors hover:border-line hover:bg-surface-muted hover:text-ink"
          title="关闭节点详情"
          aria-label="关闭节点详情"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {selectedTask.Verification ? <VerificationDetails verification={selectedTask.Verification} onOpenSettings={onOpenModelSettings} /> : null}
        <table className="w-full border-collapse border-b border-line">
          <caption className="sr-only">节点规格</caption>
          <tbody>
            <tr className="border-b border-line">
              <th scope="row" className="w-[5.5rem] border-r border-line px-4 py-1.5 text-left journal-caps text-[10px] font-normal">负责者</th>
              <td className="px-3 py-1.5 text-[12px] text-ink-soft">
                <span className="flex items-center gap-1.5">{getAgentIcon(selectedTask.AssignedTo, 'h-3.5 w-3.5 text-muted')}{getAgentLabel(selectedTask.AssignedTo)}</span>
              </td>
            </tr>
            <tr className="border-b border-line">
              <th scope="row" className="border-r border-line px-4 py-1.5 text-left journal-caps text-[10px] font-normal">状态</th>
              <td className="px-3 py-1.5 text-[12px] text-ink-soft">{taskStatusLabels[selectedTask.Status] ?? selectedTask.Status}</td>
            </tr>
            <tr className="border-b border-line">
              <th scope="row" className="border-r border-line px-4 py-1.5 text-left journal-caps text-[10px] font-normal">任务类型</th>
              <td className="px-3 py-1.5 journal-num text-[11px] text-ink-soft">{selectedTask.Type || '—'}</td>
            </tr>
            <tr>
              <th scope="row" className="border-r border-line px-4 py-1.5 text-left journal-caps text-[10px] font-normal">节点编号</th>
              <td className="px-3 py-1.5 journal-num text-[11px] text-ink-soft">{selectedTask.ID.slice(0, 12)}</td>
            </tr>
          </tbody>
        </table>

        {ablationBudget && (
          <div className="grid grid-cols-3 border-b border-line bg-surface">
            <BudgetValue label="实验" value={ablationBudget.ablation_max_experiments} suffix="组" />
            <BudgetValue label="GPU" value={ablationBudget.ablation_max_gpu_minutes} suffix="分钟" />
            <BudgetValue label="总耗时" value={ablationBudget.ablation_max_wall_minutes} suffix="分钟" />
          </div>
        )}

        <div className="border-b border-line px-4 py-2">
          <button
            type="button"
            onClick={handleExecute}
            disabled={isExecuting || requiresApproval}
            title={requiresApproval ? '计划尚未审批，请先使用右上角“审批并运行”' : '启动当前节点'}
            className="flex h-8 w-full items-center justify-center gap-1.5 border border-line-strong bg-surface px-3 text-[12px] font-semibold text-ink-soft transition-colors hover:border-ink hover:text-ink active:translate-y-px disabled:cursor-not-allowed disabled:border-line disabled:bg-surface-muted disabled:text-muted"
          >
            {requiresApproval ? (
              <>
                <ShieldAlert className="h-3.5 w-3.5" />
                计划待审批，暂不可执行
              </>
            ) : isExecuting ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                正在执行…
              </>
            ) : (
              <>
                {selectedTask.Status === 'completed' || selectedTask.Status === 'failed' ? (
                  <RotateCcw className="h-3.5 w-3.5" />
                ) : (
                  <Play className="h-3.5 w-3.5 fill-current" />
                )}
                {selectedTask.Status === 'completed'
                  ? '重新运行任务'
                  : selectedTask.Status === 'failed'
                    ? '重试任务'
                    : '启动任务'}
              </>
            )}
          </button>
        </div>

        {apiFailure && (
          <div role="alert" className="border-b border-line bg-proof-soft px-4 py-2">
            <div className="flex items-baseline justify-between gap-2">
              <span className="font-serif text-[12.5px] font-semibold text-proof">{apiFailure.title}</span>
              <span className="journal-num text-[10px] uppercase text-muted">{apiFailure.code}</span>
            </div>
            <p className="mt-0.5 break-words text-[11.5px] leading-5 text-ink-soft">{apiFailure.detail}</p>
            <p className="mt-1 break-words text-[11px] leading-5 text-ink-soft">
              <span className="font-semibold">修复建议: </span>
              {apiFailure.remediation}
            </p>
          </div>
        )}

        {(executionResult || executionCode || executionStructuredData) && (
          <div className="flex items-center justify-between border-b border-line px-1">
            <div role="tablist" aria-label="节点结果视图" className="flex flex-1 items-center overflow-x-auto">
              {tabItems.map((tab) => (
                <TabButton
                  key={tab.mode}
                  id={`execution-tab-${tab.mode}`}
                  controls="execution-result-panel"
                  active={activeMode === tab.mode}
                  onClick={() => onChangeDisplayMode(tab.mode)}
                  onKeyDown={handleTabKeyDown}
                  icon={tab.icon}
                  label={tab.label}
                />
              ))}
            </div>
            {(activeMode === 'report' || activeMode === 'plot' || activeMode === 'evidence' || activeMode === 'trials' || activeMode === 'ablation') && (
              <button
                onClick={() => onChangeDisplayMode(`${activeMode}-expanded` as ExecutionDisplayMode)}
                className="ml-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-[2px] text-muted transition-colors hover:bg-surface-muted hover:text-accent-ink"
                title="全屏查看"
                aria-label="全屏查看"
              >
                <Maximize2 className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        )}

        <div
          id="execution-result-panel"
          role="tabpanel"
          aria-labelledby={activeTabIndex >= 0 ? `execution-tab-${activeMode}` : undefined}
          className="flex min-h-0 flex-1 flex-col p-3"
        >
          {activeMode === 'logs' ? (
            <div className="flex min-h-0 flex-1 flex-col">
              <div className="mb-1.5 flex items-center justify-between gap-2 px-0.5">
                <span className="journal-caps flex items-center gap-1.5 text-[10px]">
                  <TerminalSquare className="h-3 w-3" />
                  运行日志
                  {logLines.length > 0 && (
                    <span className="journal-num text-[10px] text-faint">
                      {visibleLogWindow.lines.length}/{filteredLogLines.length}
                    </span>
                  )}
                </span>
                <div className="flex items-center gap-0.5">
                  {logLines.length > 0 && (
                    <button
                      type="button"
                      onClick={downloadLogs}
                      className="flex items-center gap-1 px-1.5 py-0.5 text-[11px] text-faint transition-colors hover:bg-surface-muted hover:text-ink-soft"
                      title="下载完整日志"
                      aria-label="下载完整日志文件"
                    >
                      <Download className="h-3 w-3" />
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => void copyLogs()}
                    className="flex items-center gap-1 px-1.5 py-0.5 text-[11px] text-faint transition-colors hover:bg-surface-muted hover:text-ink-soft"
                    aria-label="复制运行日志"
                  >
                    {copied ? <Check className="h-3 w-3 text-ok" /> : <Copy className="h-3 w-3" />}
                    {copied ? '已复制' : '复制'}
                  </button>
                </div>
              </div>
              {logLines.length > 0 && (
                <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-0.5">
                  <div role="group" aria-label="日志级别筛选" className="flex items-stretch border border-line">
                    {logLevelOptions.map((option) => (
                      <button
                        key={option.value}
                        type="button"
                        aria-pressed={logLevel === option.value}
                        onClick={() => setLogLevel(option.value)}
                        className={`border-r border-line px-2 py-0.5 text-[10.5px] transition-colors last:border-r-0 ${
                          logLevel === option.value
                            ? 'bg-ink text-paper'
                            : 'text-muted hover:bg-surface-muted hover:text-ink-soft'
                        }`}
                      >
                        {option.label}
                      </button>
                    ))}
                  </div>
                  {filteredLogLines.length > DEFAULT_LOG_TAIL && (
                    <button
                      type="button"
                      aria-pressed={showAllLogs}
                      onClick={() => setShowAllLogs((value) => !value)}
                      className="border border-line px-2 py-0.5 text-[10.5px] text-muted transition-colors hover:border-line-strong hover:text-ink-soft"
                    >
                      {showAllLogs ? `收起（最近 ${DEFAULT_LOG_TAIL} 行）` : `显示全部（${filteredLogLines.length} 行）`}
                    </button>
                  )}
                </div>
              )}
              {visibleLogWindow.hiddenCount > 0 && (
                <p className="mb-1.5 px-0.5 text-[10.5px] text-faint">
                  已隐藏 {visibleLogWindow.hiddenCount} 行（
                  <button
                    type="button"
                    onClick={downloadLogs}
                    className="underline decoration-dotted underline-offset-2 transition-colors hover:text-ink-soft"
                    aria-label="下载完整日志"
                  >
                    <Download className="mr-0.5 inline h-3 w-3 align-[-1px]" aria-hidden="true" />
                    下载完整日志
                  </button>
                  ）
                </p>
              )}
              <div className="min-h-[16rem] flex-1 overflow-y-auto border border-line-strong bg-[#17130f] px-3 py-2.5 font-mono text-[11.5px] leading-[1.75] text-[#e8e3d9] selection:bg-proof/40">
                {logLines.length === 0 ? (
                  <span className="whitespace-pre-wrap break-words">&gt; 等待执行…</span>
                ) : filteredLogLines.length === 0 ? (
                  <span className="text-[#9d968b]">当前级别没有匹配的日志；可切换到“全部”查看。</span>
                ) : (
                  visibleLogWindow.lines.map((line, index) => (
                    <div
                      key={`${index}-${line.slice(0, 24)}`}
                      data-log-row=""
                      className="whitespace-pre-wrap break-words"
                    >
                      {line || '\u00a0'}
                    </div>
                  ))
                )}
                {executionResult && !reportAgents.has(selectedTask.AssignedTo) && (
                  <div className="mt-4 whitespace-pre-wrap break-words border-t border-white/10 pt-3 text-[#a9c9b5]">
                    [输出]{'\n'}
                    {executionResult}
                  </div>
                )}
                <div ref={logsEndRef} />
              </div>
            </div>
          ) : activeMode === 'trials' ? (
            <div className="min-h-[32rem] flex-1 overflow-hidden rounded-[2px] border border-line bg-surface">
              <LazyTrialView rawLedger={executionStructuredData || executionResult} experiment={hasExperimentEvidence} />
            </div>
          ) : activeMode === 'ablation' ? (
            <div className="min-h-[32rem] flex-1 overflow-hidden rounded-[2px] border border-line bg-surface">
              <LazyAblationView rawPlan={executionStructuredData || executionResult} />
            </div>
          ) : activeMode === 'code' ? (
            <div className="flex-1 overflow-y-auto rounded-[2px] border border-line bg-surface-muted p-4">
              <pre className="m-0 overflow-x-auto rounded-[2px] bg-[#1d1e1a] p-4 font-mono text-[11.5px] leading-[1.7] text-[#e6e1d5]">
                <code>{executionCode}</code>
              </pre>
            </div>
          ) : activeMode === 'plot' ? (
            <div className="relative flex min-h-[20rem] flex-1 flex-col items-center justify-center overflow-hidden rounded-[2px] border border-line bg-surface-muted p-3">
              {selectedTask.Verification ? (
                <div className="absolute right-2 top-2 z-10">
                  <VerificationBadge verification={selectedTask.Verification} onOpenSettings={onOpenModelSettings} />
                </div>
              ) : null}
              <img src={`data:image/png;base64,${executionImage}`} alt="Generated Plot" className="max-h-full max-w-full rounded-[2px] object-contain shadow-[var(--shadow-card)]" />
              <div className="mt-2 text-[11px] text-faint">使用右上角按钮全屏查看</div>
            </div>
          ) : activeMode === 'evidence' ? (
            <div className="min-h-0 flex-1 overflow-hidden rounded-[2px] border border-line bg-surface">
              <LazyEvidenceView rawGraph={executionStructuredData} />
            </div>
          ) : activeMode === 'report' && hasBenchmarkEvidence ? (
            <div className="min-h-[32rem] flex-1 overflow-hidden rounded-[2px] border border-line bg-surface">
              <LazyBenchmarkReport raw={executionStructuredData || executionResult} />
            </div>
          ) : (
            <div className="flex-1 overflow-y-auto rounded-[2px] border border-line bg-surface p-5">
              <div className="prose prose-sm max-w-none font-serif text-[14px] leading-6 text-ink-soft prose-headings:font-serif prose-headings:text-ink prose-strong:text-ink prose-code:font-sans prose-code:text-[12.5px] prose-code:text-accent-ink">
                <ReportMarkdown>{executionResult}</ReportMarkdown>
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

function TabButton(props: { active: boolean; onClick: () => void; onKeyDown?: (event: ReactKeyboardEvent<HTMLElement>) => void; icon: ReactNode; label: string; id?: string; controls?: string }) {
  const { active, onClick, onKeyDown, icon, label, id, controls } = props;
  return (
    <button
      type="button"
      role="tab"
      id={id}
      aria-controls={controls}
      aria-selected={active}
      tabIndex={active ? 0 : -1}
      onClick={onClick}
      onKeyDown={onKeyDown}
      className={`flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 journal-caps text-[10px] transition-colors ${
        active
          ? 'border-accent text-accent-ink'
          : 'border-transparent text-muted hover:border-line-strong hover:text-ink-soft'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}

function BudgetValue({ label, value, suffix }: { label: string; value: unknown; suffix: string }) {
  const displayValue = typeof value === 'number' || typeof value === 'string' ? String(value) : '-';
  return (
    <div className="border-r border-line px-3 py-2 text-center last:border-r-0">
      <div className="journal-caps text-[10px]">{label}</div>
      <div className="journal-num mt-0.5 text-[12.5px] font-semibold text-ink-soft">{displayValue} {suffix}</div>
    </div>
  );
}