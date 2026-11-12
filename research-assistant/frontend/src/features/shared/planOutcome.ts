import type { BlockInfo, OutcomeDetails, PlanGraph } from '../../contracts/api';

/** User-facing labels for the fine-grained plan outcome. */
export const planOutcomeLabels: Record<string, string> = {
  completed: '全部完成',
  partial: '部分完成',
  smoke: '冒烟',
  mock: '模拟',
  blocked: '缺少条件',
  failed: '失败',
  // "execution finished, evidence pending": the run succeeded but its result is
  // not yet usable, so it must not read as a plain failure or a plain success.
  needs_review: '已结束·待复核',
};

export const planOutcomeLabel = (outcome?: string | null): string => {
  if (!outcome) return '';
  return planOutcomeLabels[outcome] ?? outcome;
};

const blockCodeLabels: Record<string, string> = {
  upstream_failed: '上游执行失败',
  upstream_blocked: '上游缺少可补条件',
  upstream_canceled: '上游已取消',
  unsatisfied_dependencies: '硬依赖无法满足',
  missing_artifacts: '必需产物缺失',
};

export const blockCodeLabel = (code?: string | null): string => {
  if (!code) return '';
  return blockCodeLabels[code] ?? code;
};

/**
 * Explain a blocked node. The structured block_info is authoritative and
 * survives a refresh; the plain message is only used for legacy snapshots that
 * predate structured reasons.
 */
export const describeBlockedNode = (
  blockInfo?: BlockInfo | null,
  fallbackMessage?: string | null,
): string => {
  if (blockInfo && (blockInfo.code || (blockInfo.causes?.length ?? 0) > 0)) {
    const code = blockCodeLabel(blockInfo.code);
    const rootIds = Array.from(new Set((blockInfo.causes ?? []).map((cause) => cause.root_task_id).filter(Boolean)));
    const detail = blockInfo.message || (rootIds.length > 0 ? '来源任务 ' + rootIds.join('、') : '');
    return [code, detail].filter(Boolean).join('：');
  }
  if (fallbackMessage && fallbackMessage.trim() !== '') return fallbackMessage;
  return '';
};

export const isPartialOutcome = (plan: Pick<PlanGraph, 'outcome'> | null | undefined): boolean =>
  plan?.outcome === 'partial';

const lossStatusLabels: Record<string, string> = {
  failed: '失败',
  blocked: '被阻断',
  canceled: '已取消',
  skipped: '已跳过',
};

const describeLoss = (status: string, reason?: string): string => {
  const label = lossStatusLabels[status] ?? status;
  if (!reason) return label;
  return label + '：' + reason;
};

export interface PlanOutcomeSummary {
  headline: string;
  detail: string;
  losses: string[];
  limitations: string[];
  limitationsLabel: string;
}

/**
 * Summarise a terminal outcome for the canvas, history and notifications. It
 * never claims full success for partial/blocked/needs_review results.
 */
export const summarizePlanOutcome = (
  plan: Pick<PlanGraph, 'status' | 'outcome' | 'outcome_reason' | 'outcome_details'> | null | undefined,
): PlanOutcomeSummary => {
  const details: OutcomeDetails | undefined = plan?.outcome_details;
  const losses = (details?.optional_losses ?? []).map((loss) => {
    const name = loss.name && loss.name.trim() ? loss.name.trim() : loss.task_id;
    const reason = describeLoss(loss.status, loss.reason);
    return reason ? name + '（' + reason + '）' : name;
  });
  const limitations = details?.limitations ?? [];
  const headline =
    planOutcomeLabel(plan?.outcome) ||
    (plan?.status === 'completed' ? '已完成' : plan?.status === 'canceled' ? '已取消' : '已结束');
  const parts: string[] = [];
  if (plan?.outcome_reason) parts.push(plan.outcome_reason);
  if (losses.length > 0) parts.push('缺少可选工作：' + losses.join('、'));
  if (details?.missing_artifacts && details.missing_artifacts.length > 0) {
    parts.push('缺少产物：' + details.missing_artifacts.join('、'));
  }
  return {
    headline,
    detail: parts.join('；'),
    losses,
    limitations,
    limitationsLabel: limitations.length > 0 ? '这些缺失限制的结论' : '',
  };
};
