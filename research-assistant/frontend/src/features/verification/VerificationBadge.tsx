import type { Verification, VerificationStatus } from '../../contracts/api';

const STATUS_LABELS: Record<VerificationStatus, string> = {
  pass: '校验通过',
  fail: '校验未通过',
  inconclusive: '不可确认',
  skipped: '未校验',
};

const STATUS_HINTS: Record<VerificationStatus, string> = {
  pass: '结构门禁与视觉判断均已通过',
  fail: '校验发现问题；计划执行状态不受影响',
  inconclusive: '未配置可用视觉模型或视觉调用失败，仅完成结构与文本检查',
  skipped: '没有图像产物需要校验',
};

export interface VerificationBadgeProps {
  verification?: Verification | null;
  compact?: boolean;
  onOpenSettings?: () => void;
}

/** Renders the advisory verification verdict without pretending a file is checked. */
export function VerificationBadge({ verification, compact = false, onOpenSettings }: VerificationBadgeProps) {
  if (!verification) {
    return null;
  }
  const status = verification.status;
  const tone =
    status === 'pass'
      ? 'border-ok/45 bg-ok-soft text-ok'
      : status === 'fail'
        ? 'border-proof/45 bg-proof-soft text-proof'
        : status === 'inconclusive'
          ? 'border-warn/45 bg-warn-soft text-warn'
          : 'border-line bg-surface text-muted';
  const tooltip = [STATUS_HINTS[status], verification.reason, verification.verified_by?.model ? `判图模型：${verification.verified_by.model}` : '']
    .filter(Boolean)
    .join(' · ');
  return (
    <span
      className={`inline-flex items-center gap-1 border px-1.5 py-0.5 journal-caps text-[10px] ${tone}`}
      title={tooltip}
      data-testid="verification-badge"
      data-verification-status={status}
    >
      <span aria-hidden="true">{status === 'pass' ? '✓' : status === 'fail' ? '✗' : status === 'inconclusive' ? '?' : '—'}</span>
      {compact ? STATUS_LABELS[status] : `${STATUS_LABELS[status]} · ${verification.level}`}
      {status === 'inconclusive' && onOpenSettings ? (
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            onOpenSettings();
          }}
          className="ml-1 underline decoration-dotted underline-offset-2"
        >
          去设置
        </button>
      ) : null}
    </span>
  );
}

export interface VerificationDetailsProps {
  verification?: Verification | null;
  onOpenSettings?: () => void;
}

/** Full verdict panel used by the verify node sidebar. */
export function VerificationDetails({ verification, onOpenSettings }: VerificationDetailsProps) {
  if (!verification) return null;
  return (
    <section className="border-b border-line bg-surface px-3 py-2" aria-label="校验结论">
      <div className="flex flex-wrap items-center gap-2">
        <span className="journal-caps text-[10px] font-semibold text-ink">校验结论</span>
        <VerificationBadge verification={verification} onOpenSettings={onOpenSettings} />
        {verification.verified_by?.model ? (
          <span className="journal-num text-[10.5px] text-muted">
            {verification.verified_by.mode === 'vision_fallback' ? '备用视觉' : '主模型'} · {verification.verified_by.model}
          </span>
        ) : null}
        {typeof verification.confidence === 'number' && verification.confidence > 0 ? (
          <span className="journal-num text-[10.5px] text-muted">置信度 {Math.round(verification.confidence * 100)}%</span>
        ) : null}
      </div>
      {verification.reason ? <p className="mt-1 font-serif text-[12px] leading-5 text-ink-soft">{verification.reason}</p> : null}
      {verification.issues && verification.issues.length > 0 ? (
        <ul className="mt-1 space-y-0.5">
          {verification.issues.map((issue, index) => (
            <li key={index} className="border-l border-proof/30 pl-2 font-serif text-[11.5px] leading-5 text-proof">
              {issue}
            </li>
          ))}
        </ul>
      ) : null}
      {verification.status === 'inconclusive' && onOpenSettings ? (
        <button type="button" onClick={onOpenSettings} className="mt-1 text-[11.5px] text-ink-soft underline decoration-dotted underline-offset-2">
          前往模型设置配置视觉模型
        </button>
      ) : null}
    </section>
  );
}
