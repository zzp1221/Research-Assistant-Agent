import type { OutcomeDetails } from '../../contracts/api';
import { describeBlockedNode, summarizePlanOutcome } from '../shared/planOutcome';

/**
 * Small presentational helpers shared by the execution sidebar and history so a
 * blocked node and a partial plan are never rendered as ordinary failures.
 */

export interface BlockedReasonProps {
  blockInfo?: import('../../contracts/api').BlockInfo | null;
  error?: string | null;
  className?: string;
}

export function BlockedReason({ blockInfo, error, className }: BlockedReasonProps) {
  const text = describeBlockedNode(blockInfo, error);
  if (!text) return null;
  return (
    <p
      className={className ?? 'mt-1 break-words text-[10.5px] leading-4 text-warn'}
      data-testid="node-blocked-reason"
      data-block-code={blockInfo?.code ?? 'legacy'}
    >
      {text}
    </p>
  );
}

export interface OutcomeDetailsListProps {
  outcome?: string | null;
  outcomeReason?: string | null;
  details?: OutcomeDetails | null;
}

/** Explains the server verdict without treating missing evidence as success. */
export function OutcomeDetailsList({ outcome, outcomeReason, details }: OutcomeDetailsListProps) {
  const summary = summarizePlanOutcome({
    status: '',
    outcome: outcome ?? '',
    outcome_reason: outcomeReason ?? undefined,
    outcome_details: details ?? undefined,
  });
  const goals = details?.accepted_goals ?? [];
  const accepted = goals.filter((goal) => goal.verified === true);
  const unverified = goals.filter((goal) => goal.verified !== true);
  const missingArtifacts = details?.missing_artifacts ?? [];
  const hasDetails = goals.length > 0 || summary.losses.length > 0 || missingArtifacts.length > 0 || summary.limitations.length > 0;
  return (
    <div className="space-y-2 break-words text-[11px] leading-5 text-ink-soft [overflow-wrap:anywhere]" data-testid="outcome-details">
      <p className="font-semibold text-warn">{summary.headline}</p>
      {outcomeReason?.trim() ? <p>{outcomeReason}</p> : null}
      {accepted.length > 0 ? (
        <section aria-label="已通过的核心目标">
          <span className="font-semibold">已通过的核心目标</span>
          <ul className="list-disc pl-4">
            {accepted.map((goal, index) => (
              <li key={`${goal.id}-${index}`}>
                {goal.statement}
                {goal.evidence_refs && goal.evidence_refs.length > 0 ? '（证据：' + goal.evidence_refs.join('、') + '）' : ''}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {unverified.length > 0 ? (
        <section aria-label="尚未通过验证的核心目标">
          <span className="font-semibold">尚未通过验证的核心目标</span>
          <ul className="list-disc pl-4">
            {unverified.map((goal, index) => (
              <li key={`${goal.id}-${index}`}>
                {goal.statement}
                {goal.reason ? '（原因：' + goal.reason + '）' : ''}
                {goal.evidence_refs && goal.evidence_refs.length > 0 ? '（待验证的证据引用：' + goal.evidence_refs.join('、') + '）' : ''}
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {summary.losses.length > 0 ? (
        <section aria-label="未完成的可选工作">
          <span className="font-semibold">未完成的可选工作</span>
          <ul className="list-disc pl-4" data-testid="optional-losses">
            {summary.losses.map((loss, index) => (
              <li key={`${details?.optional_losses?.[index]?.task_id}-${index}`}>{loss}</li>
            ))}
          </ul>
        </section>
      ) : null}
      {missingArtifacts.length > 0 ? (
        <section aria-label="缺少或不可信的验收产物">
          <span className="font-semibold">缺少或不可信的验收产物</span>
          <ul className="list-disc pl-4">
            {missingArtifacts.map((artifact, index) => (
              <li key={`${artifact}-${index}`}>{artifact}</li>
            ))}
          </ul>
        </section>
      ) : null}
      {summary.limitations.length > 0 ? (
        <section aria-label={summary.limitationsLabel}>
          <span className="font-semibold">{summary.limitationsLabel}</span>
          <ul className="list-disc pl-4">
            {summary.limitations.map((limitation, index) => (
              <li key={`${limitation}-${index}`}>{limitation}</li>
            ))}
          </ul>
        </section>
      ) : null}
      {!hasDetails ? <p>该计划未提供结构化验收详情</p> : null}
    </div>
  );
}
