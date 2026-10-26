import { BrainCircuit, CheckCircle2, Clock3, Cpu, GitBranch, Scissors, Sparkles } from 'lucide-react';
import { parseAblationPlan, type AblationCandidateView, type AblationPlanView } from './ablationPlan';

interface AblationTreeViewProps {
  rawPlan: string;
  expanded?: boolean;
}

const categoryLabels: Record<string, string> = {
  parameter: '参数敏感性',
  module: '模块消融',
  data_scale: '数据规模',
  seed_stability: '随机种子',
  runtime_cost: '运行成本',
};

function BranchStatus({ candidate, plan }: { candidate: AblationCandidateView; plan: AblationPlanView }) {
  if (plan.selectedIds.has(candidate.id)) {
    return <span className="inline-flex items-center gap-1 rounded-[2px] border border-ok/25 bg-ok-soft px-2 py-1 text-[11px] font-semibold text-ok"><CheckCircle2 className="h-3.5 w-3.5" />入选</span>;
  }
  if (plan.expandedParentIds.has(candidate.id)) {
    return <span className="inline-flex items-center gap-1 rounded-[2px] border border-accent/25 bg-accent-soft px-2 py-1 text-[11px] font-semibold text-accent-ink"><GitBranch className="h-3.5 w-3.5" />已扩展</span>;
  }
  return <span className="inline-flex items-center gap-1 rounded-[2px] border border-line bg-surface-muted px-2 py-1 text-[11px] font-semibold text-muted"><Scissors className="h-3.5 w-3.5" />剪枝</span>;
}

function BranchRow({ candidate, plan, child = false }: { candidate: AblationCandidateView; plan: AblationPlanView; child?: boolean }) {
  return (
    <div className={`border-b border-line/70 px-4 py-4 last:border-b-0 ${child ? 'ml-7 border-l-2 border-l-accent/20 bg-surface-muted/60' : 'bg-surface'}`}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <BranchStatus candidate={candidate} plan={plan} />
            <span className="rounded-[2px] bg-paper-deep px-2 py-1 text-[10px] font-semibold text-muted">L{candidate.depth}</span>
            <span className="text-xs font-semibold text-ink">{candidate.title}</span>
          </div>
          <div className="mt-1 text-[10px] font-medium text-accent-ink">{categoryLabels[candidate.category] || candidate.category}</div>
        </div>
        <div className="shrink-0 text-right">
          <div className="font-mono text-sm font-semibold text-ink">{candidate.score.toFixed(4)}</div>
          <div className="text-[10px] text-muted">综合分</div>
        </div>
      </div>

      {candidate.expansionReason && <div className="mt-3 text-[11px] leading-5 text-accent-ink"><span className="font-semibold">扩展依据：</span>{candidate.expansionReason}</div>}
      {candidate.hypothesis && <div className="mt-2 text-[11px] leading-5 text-ink-soft"><span className="font-semibold text-ink">假设：</span>{candidate.hypothesis}</div>}
      {candidate.change && <div className="mt-1 text-[11px] leading-5 text-muted"><span className="font-semibold text-ink">变量：</span>{candidate.change}</div>}
      {candidate.evaluationReason && <div className="mt-1 text-[11px] leading-5 text-muted"><span className="font-semibold">评分依据：</span>{candidate.evaluationReason}</div>}
      {candidate.decisionReason && <div className="mt-1 text-[11px] leading-5 text-muted"><span className="font-semibold">决策依据：</span>{candidate.decisionReason}</div>}

      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 border-y border-line/70 py-2 text-[10px] text-muted">
        <span>信息增益 <strong className="font-mono text-ink-soft">{candidate.informationGain.toFixed(2)}</strong></span>
        <span>相关性 <strong className="font-mono text-ink-soft">{candidate.relevance.toFixed(2)}</strong></span>
        <span>可复现 <strong className="font-mono text-ink-soft">{candidate.reproducibility.toFixed(2)}</strong></span>
        <span>风险 <strong className="font-mono text-ink-soft">{candidate.risk.toFixed(2)}</strong></span>
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-[10px] text-muted">
        <span className="inline-flex items-center gap-1"><Clock3 className="h-3.5 w-3.5" />{candidate.estimatedMinutes} 分钟</span>
        <span className="inline-flex items-center gap-1"><Cpu className="h-3.5 w-3.5" />GPU {candidate.estimatedGpuMinutes} 分钟</span>
        {candidate.metrics.length > 0 && <span className="break-all font-mono">{candidate.metrics.join(' · ')}</span>}
      </div>
    </div>
  );
}

export function AblationTreeView({ rawPlan, expanded = false }: AblationTreeViewProps) {
  const parsed = parseAblationPlan(rawPlan);
  if (!parsed.ok) {
    return <div className="flex min-h-48 items-center justify-center border border-flag/25 bg-flag-soft px-6 text-center text-sm text-flag">{parsed.error}</div>;
  }

  const plan = parsed.plan;
  const childrenByParent = new Map<string, AblationCandidateView[]>();
  for (const candidate of plan.candidates) {
    if (candidate.parentId === 'root') continue;
    const children = childrenByParent.get(candidate.parentId) || [];
    children.push(candidate);
    childrenByParent.set(candidate.parentId, children);
  }
  const roots = plan.candidates.filter((candidate) => candidate.parentId === 'root');

  return (
    <div className={`flex min-h-0 flex-col bg-surface ${expanded ? 'h-full' : ''}`}>
      <div className="border-b border-accent/25 bg-accent-soft px-4 py-4 text-accent-ink">
        <div className="flex items-start gap-3">
          <BrainCircuit className="mt-0.5 h-5 w-5 shrink-0" />
          <div>
            <div className="text-sm font-semibold">受限 Tree of Thoughts</div>
            <div className="mt-1 text-xs leading-5 text-accent-ink">先评分根分支，再细化高价值方向；最终入选由预算规则确定。</div>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 border-b border-line bg-surface-muted sm:grid-cols-4">
        <div className="border-r border-line px-3 py-3 text-center"><div className="text-sm font-semibold text-ink">{plan.actualDepth}</div><div className="mt-1 text-[10px] text-muted">实际层数</div></div>
        <div className="border-r border-line px-3 py-3 text-center"><div className="text-sm font-semibold text-ink">{plan.candidates.length} / {plan.branchLimit}</div><div className="mt-1 text-[10px] text-muted">候选分支</div></div>
        <div className="border-r border-line px-3 py-3 text-center"><div className="text-sm font-semibold text-ok">{plan.selectedIds.size}</div><div className="mt-1 text-[10px] text-muted">最终入选</div></div>
        <div className="px-3 py-3 text-center"><div className="text-sm font-semibold text-ink">{plan.budget.maxWallMinutes}m</div><div className="mt-1 text-[10px] text-muted">总耗时预算</div></div>
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-line px-4 py-3 text-[11px] text-muted">
        <span className="inline-flex items-center gap-1.5 font-semibold text-ink"><Sparkles className="h-3.5 w-3.5 text-accent" />预算约束</span>
        <span>实验 {plan.budget.maxExperiments} 组</span>
        <span>GPU {plan.budget.maxGpuMinutes} 分钟</span>
        <span>树深 {plan.actualDepth}/{plan.maxDepth}</span>
      </div>

      <div className={`min-h-0 flex-1 ${expanded ? 'overflow-y-auto' : 'max-h-[36rem] overflow-y-auto'}`}>
        {roots.map((root) => (
          <section key={root.id} className="border-b-4 border-line/70 last:border-b-0">
            <BranchRow candidate={root} plan={plan} />
            {(childrenByParent.get(root.id) || []).map((child) => <BranchRow key={child.id} candidate={child} plan={plan} child />)}
          </section>
        ))}
      </div>

      {plan.selectionReason && <div className="border-t border-line px-4 py-3 text-[11px] leading-5 text-muted">{plan.selectionReason}</div>}
    </div>
  );
}