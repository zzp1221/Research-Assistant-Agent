import type { OutcomeDetails } from '../../contracts/api';
import { describeBlockedNode, planOutcomeLabel, summarizePlanOutcome } from '../shared/planOutcome';

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

/** Renders the accepted core goals and the optional work that was lost. */
export function OutcomeDetailsList({ outcome, outcomeReason, details }: OutcomeDetailsListProps) {
  const summary = summarizePlanOutcome({
    status: '',
    outcome: outcome ?? '',
    outcome_reason: outcomeReason ?? undefined,
    outcome_details: details ?? undefined,
  });
  const accepted = (details?.accepted_goals ?? []).filter((goal) => goal.verified);
  return (
    <div className="space-y-1 text-[11px] leading-5 text-ink-soft" data-testid="outcome-details">
      <p className="font-semibold text-ink-soft">{planOutcomeLabel(outcome) || summary.headline}</p>
      {summary.detail ? <p>{summary.detail}</p> : null}
      {accepted.length > 0 ? (
        <div>
          <span className="font-semibold">已通过的核心目标</span>
          <ul className="list-disc pl-4">
            {accepted.map((goal) => (
              <li key={goal.id}>
                {goal.statement}
                {goal.evidence_refs && goal.evidence_refs.length > 0 ? '（证据：' + goal.evidence_refs.join('、') + '）' : ''}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {summary.losses.length > 0 ? (
        <div>
          <span className="font-semibold">缺少的可选工作</span>
          <ul className="list-disc pl-4" data-testid="optional-losses">
            {summary.losses.map((loss) => (
              <li key={loss}>{loss}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {summary.limitations.length > 0 ? (
        <div>
          <span className="font-semibold">{summary.limitationsLabel}</span>
          <ul className="list-disc pl-4">
            {summary.limitations.map((limitation) => (
              <li key={limitation}>{limitation}</li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
