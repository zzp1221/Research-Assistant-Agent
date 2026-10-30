import { ChevronRight, CircleDollarSign, Inbox, ShieldCheck } from 'lucide-react';
import type { WorkbenchBudgetState } from '../../app/hooks/useWorkbenchBudget';
import { planStatusLabel } from './planStatus';

interface WorkbenchStatusBarProps {
  planId: string | null;
  planStatus: string | null;
  reviewRequired: boolean;
  reviewStatus?: string | null;
  stepCount: number;
  completedCount: number;
  runningCount: number;
  budget: WorkbenchBudgetState;
  pendingWorkCount: number;
  pendingApprovalCount: number;
  pendingQuestionCount: number;
  permissionMode: string | null;
  onOpenPlanLibrary: () => void;
  onOpenCostConsole: () => void;
  onOpenInbox: () => void;
  onOpenReview: () => void;
}

/** A single quiet status line: plan, budget, pending work, review, access. */
export function WorkbenchStatusBar(props: WorkbenchStatusBarProps) {
  const {
    planId,
    planStatus,
    reviewRequired,
    reviewStatus,
    stepCount,
    completedCount,
    runningCount,
    budget,
    pendingWorkCount,
    pendingApprovalCount,
    pendingQuestionCount,
    permissionMode,
    onOpenPlanLibrary,
    onOpenCostConsole,
    onOpenInbox,
    onOpenReview,
  } = props;

  const reviewPending = reviewRequired && reviewStatus !== 'approved';
  const statusLabel = planId ? planStatusLabel(planStatus, reviewPending) : '尚无计划';
  const summary = budget.summary;
  const configured = Boolean(summary?.configured || budget.budget);
  const limit = summary?.limit_usd ?? budget.budget?.limit_usd ?? 0;
  const spent = summary?.spent_usd ?? 0;
  const reserved = summary?.reserved_usd ?? 0;
  const percent = limit > 0 ? Math.min(100, Math.round(((spent + reserved) / limit) * 100)) : 0;
  const budgetTone = percent >= 100 ? 'bg-proof' : percent >= 80 ? 'bg-warn' : 'bg-ok';
  const budgetLabel = configured ? `$${spent.toFixed(2)} / $${limit.toFixed(2)}` : budget.loading ? '读取中' : '预算未设置';

  return (
    <div className="research-assistant-status-bar" aria-label="工作台状态">
      <button type="button" onClick={onOpenPlanLibrary} className="research-assistant-status-item" title="打开计划库">
        <span className="research-assistant-status-label">计划</span>
        <span className="min-w-0 truncate">
          <b className="truncate">{planId ? planId.slice(0, 8).toUpperCase() : '—'}</b>
          <em>{stepCount} 节点 · {completedCount} 完成{runningCount > 0 ? ` · ${runningCount} 运行中` : ''}</em>
        </span>
        {planId ? <span className={`research-assistant-status-chip ${reviewPending ? 'is-alert' : statusLabel === '运行中' ? 'is-live' : ''}`}>{statusLabel}</span> : null}
      </button>

      <button type="button" onClick={onOpenCostConsole} className="research-assistant-status-item" title="打开成本与预算">
        <CircleDollarSign className="h-3.5 w-3.5 text-muted" />
        <span className="min-w-0 truncate">
          <b>{budgetLabel}</b>
          <em>{configured ? `已用 + 预留 $${(spent + reserved).toFixed(2)} · ${percent}%` : budget.error ? '预算不可用' : '点击设置每日限额'}</em>
        </span>
        {configured ? <span className="research-assistant-budget-bar"><i className={budgetTone} style={{ width: `${percent}%` }} /></span> : null}
      </button>

      <button type="button" onClick={onOpenInbox} className="research-assistant-status-item" title="打开待办中心">
        <Inbox className="h-3.5 w-3.5 text-muted" />
        <span className="min-w-0 truncate">
          <b className={pendingWorkCount > 0 ? 'text-proof' : ''}>{pendingWorkCount > 0 ? `${pendingWorkCount} 项待办` : '没有待办'}</b>
          <em>{pendingWorkCount > 0 ? `${pendingApprovalCount} 审批 · ${pendingQuestionCount} 提问` : '审批与提问'}</em>
        </span>
        {pendingWorkCount > 0 ? <span className="research-assistant-status-dot" aria-hidden="true" /> : null}
      </button>

      <button type="button" onClick={onOpenReview} disabled={!planId} className="research-assistant-status-item" title="打开计划审阅">
        <ShieldCheck className={`h-3.5 w-3.5 ${reviewPending ? 'text-proof' : 'text-muted'}`} />
        <span className="min-w-0 truncate">
          <b className={reviewPending ? 'text-proof' : ''}>{reviewPending ? '待审阅' : planId ? '无需审阅' : '未打开计划'}</b>
          <em>{reviewPending ? '批准后才会执行' : permissionMode === 'full_access' ? '完全权限' : '逐项确认'}</em>
        </span>
        <ChevronRight className="h-3 w-3 shrink-0 text-faint" />
      </button>
    </div>
  );
}
