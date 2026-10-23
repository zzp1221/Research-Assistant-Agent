import {
  CheckCircle2,
  Circle,
  CircleDot,
  GitBranch,
  Layers3,
  LoaderCircle,
  Network,
  TimerReset,
  Waypoints,
  XCircle,
  Zap,
} from 'lucide-react';
import type { Task } from '../../contracts/api';
import { formatExperimentNumber, parseExperimentLedger, type ExperimentTrialView } from '../experiment/experimentLedger';

interface ExperimentSearchNodeLabelProps {
  task: Task;
  status: string;
  step?: number;
}

const statusText: Record<string, string> = {
  pending: '等待',
  ready: '就绪',
  in_progress: '运行中',
  completed: '已完成',
  failed: '失败',
  blocked: '已阻塞',
};

const inputNumber = (task: Task, key: string, fallback: number) => {
  const value = task.Inputs?.[key];
  return typeof value === 'number' && Number.isFinite(value) ? Math.max(1, Math.round(value)) : fallback;
};

const strategyFallback = (task: Task) => {
  const domain = String(task.Inputs?.research_domain || '').toLowerCase();
  return domain === 'retrieval'
    ? ['BM25', 'TF-IDF', 'Hybrid RRF', 'Graph Hybrid']
    : ['Baseline', 'Method A', 'Method B', 'Parameter branch'];
};

const branchState = (trial: ExperimentTrialView | undefined, targetStopped: boolean, running: boolean, index: number) => {
  if (trial?.number === 0) return { label: 'Baseline', tone: 'text-[#3e5c76]', icon: CircleDot };
  if (trial?.status === 'kept') return { label: 'Keep', tone: 'text-ok', icon: CheckCircle2 };
  if (trial) return { label: 'Reject', tone: 'text-flag', icon: XCircle };
  if (targetStopped) return { label: 'Pruned', tone: 'text-warn', icon: TimerReset };
  if (running && index < 3) return { label: 'Running', tone: 'text-accent', icon: LoaderCircle };
  return { label: 'Queued', tone: 'text-faint', icon: Circle };
};

const shortStrategyName = (name: string) => name.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());

export function ExperimentSearchNodeLabel({ task, status, step }: ExperimentSearchNodeLabelProps) {
  const parsed = parseExperimentLedger(task.StructuredData || task.Result || '');
  const ledger = parsed.ok ? parsed.ledger : null;
  const parallelism = ledger?.maxParallelTrials ?? inputNumber(task, 'experiment_max_parallel_trials', 4);
  const peakParallelism = ledger?.resourceUsage.peakParallelism ?? (status === 'in_progress' ? parallelism : 0);
  const strategies = ledger?.strategySpace.map((strategy) => strategy.name) ?? strategyFallback(task);
  const targetStopped = ledger?.stopReason === 'target_score_reached' || ledger?.stopReason === 'baseline_target_reached';
  const running = status === 'in_progress';
  const totBranches = ledger?.designedBranches || inputNumber(task, 'ablation_max_experiments', Math.min(5, strategies.length));

  return (
    <div className="w-full bg-surface text-left">
      <div className="flex items-center justify-between gap-3 border-b border-line bg-surface-muted px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <Network className="h-3.5 w-3.5 shrink-0 text-accent" />
          <span className="font-mono text-[10px] font-semibold text-faint">{String(step ?? 0).padStart(2, '0')}</span>
          <span className="truncate text-[12.5px] font-semibold text-ink">多策略异步搜索</span>
        </div>
        <div className="flex shrink-0 items-center gap-1.5 text-[9.5px] font-semibold">
          <span className="rounded-[3px] border border-plum/25 bg-plum-soft px-1.5 py-0.5 text-plum">ToT {totBranches} branches</span>
          <span className="rounded-[3px] border border-accent/25 bg-accent-soft px-1.5 py-0.5 text-accent-ink">Agent ×{parallelism}</span>
          <span className={status === 'failed' ? 'text-flag' : status === 'completed' ? 'text-ok' : 'text-warn'}>{statusText[status] ?? status}</span>
        </div>
      </div>

      <div className="grid grid-cols-4 border-b border-line bg-surface">
        {[
          { icon: GitBranch, title: '默认穷举', detail: `${strategies.length} 个 Model` },
          { icon: Waypoints, title: '路线 UCB', detail: '预算与探索平衡' },
          { icon: Layers3, title: 'Beam + UCT', detail: `Top-${ledger?.beamWidth ?? 3} + Explore` },
          { icon: Zap, title: '异步评测', detail: `${Math.max(peakParallelism, parallelism)} 个 Search Agent` },
        ].map((stage, index) => {
          const Icon = stage.icon;
          return (
            <div key={stage.title} className="relative min-w-0 border-r border-line px-3 py-2 last:border-r-0">
              {index < 3 && <span className="absolute -right-1 top-1/2 z-10 h-2 w-2 -translate-y-1/2 rotate-45 border-r border-t border-line-strong bg-surface" aria-hidden="true" />}
              <div className="flex min-w-0 items-center gap-1.5 text-[9.5px] font-semibold text-ink-soft">
                <Icon className="h-3 w-3 shrink-0 text-accent" />
                <span className="truncate">{stage.title}</span>
              </div>
              <div className="mt-1 break-words text-[9px] leading-3 text-faint">{stage.detail}</div>
            </div>
          );
        })}
      </div>

      <div className="bg-surface-muted px-4 py-2.5">
        <div className="grid grid-cols-4 gap-2">
          {strategies.slice(0, 4).map((strategy, index) => {
            const rootTrial = ledger?.trials.find((trial) => trial.candidate.strategy === strategy && trial.candidate.depth === 0);
            const state = branchState(rootTrial, targetStopped, running, index);
            const StateIcon = state.icon;
            return (
              <div key={strategy} className="min-w-0 rounded-[4px] border border-line bg-surface px-2.5 py-2">
                <div className="flex items-center justify-between gap-1.5">
                  <span className="truncate font-mono text-[9.5px] font-semibold text-ink-soft" title={strategy}>{shortStrategyName(strategy)}</span>
                  <StateIcon className={`h-3 w-3 shrink-0 ${state.tone} ${state.label === 'Running' ? 'animate-spin' : ''}`} />
                </div>
                <div className="mt-2 flex items-center justify-between gap-2 text-[9px]">
                  <span className={`font-semibold ${state.tone}`}>{state.label}</span>
                  <span className="font-mono text-muted">{rootTrial?.score === null || rootTrial?.score === undefined ? 'awaiting' : formatExperimentNumber(rootTrial.score)}</span>
                </div>
                {rootTrial && rootTrial.number > 0 && <div className="mt-1 truncate font-mono text-[8px] text-faint">{rootTrial.agentId} · C{rootTrial.completionOrder}</div>}
              </div>
            );
          })}
        </div>
        <div className="mt-2.5 flex items-center justify-between gap-4 border-t border-line pt-2 text-[9px] text-muted">
          <span className="truncate">默认配置屏障 · UCB/UCT 调度 · 完成顺序入账 · 隐藏 Holdout</span>
          <span className="shrink-0 font-mono font-semibold text-ink-soft">
            {ledger ? `${ledger.metricKey} ${formatExperimentNumber(ledger.baselineScore)} → ${formatExperimentNumber(ledger.bestScore)}` : 'metric pending'}
          </span>
        </div>
      </div>
    </div>
  );
}
