import { useCallback, useEffect, useMemo, useState } from 'react';
import { Bell, Check, Inbox, Loader2, RefreshCw, ShieldAlert, X } from 'lucide-react';
import type { ApprovalInboxItem } from '../../contracts/api';
import {
  answerQuestion,
  bulkApprovals,
  decideRuntimeAction,
  listApprovals,
} from '../../services/api/researchAssistantApi';

interface ApprovalInboxPanelProps {
  onClose: () => void;
  onOpenPlan: (planId: string) => void;
}

const riskLabel = (risk?: string): string => {
  switch (risk) {
    case 'high':
      return '高风险';
    case 'medium':
      return '中风险';
    case 'low':
      return '低风险';
    default:
      return '—';
  }
};

const waitingLabel = (seconds: number): string => {
  if (seconds < 60) return seconds + ' 秒';
  if (seconds < 3600) return Math.round(seconds / 60) + ' 分钟';
  return Math.round(seconds / 360) / 10 + ' 小时';
};

export function ApprovalInboxPanel({ onClose, onOpenPlan }: ApprovalInboxPanelProps) {
  const [items, setItems] = useState<ApprovalInboxItem[]>([]);
  const [kind, setKind] = useState<'all' | 'approval' | 'question'>('all');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [answers, setAnswers] = useState<Record<string, { option: string; text: string }>>({});

  const refresh = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const payload = await listApprovals({ kind, status: 'pending', limit: 100 });
      setItems(payload.items);
      setSelected({});
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载收件箱失败');
    } finally {
      setLoading(false);
    }
  }, [kind]);

  useEffect(() => {
    queueMicrotask(() => void refresh());
  }, [refresh]);

  const approvalCount = useMemo(() => items.filter((item) => item.kind === 'approval').length, [items]);
  const questionCount = useMemo(() => items.filter((item) => item.kind === 'question').length, [items]);
  const highRiskCount = useMemo(() => items.filter((item) => item.risk === 'high').length, [items]);
  const bulkEligible = useMemo(
    () => items.filter((item) => item.kind === 'approval' && item.risk !== 'high'),
    [items],
  );
  const selectedEligible = bulkEligible.filter((item) => selected[item.id]);

  const toggle = (id: string) => setSelected((current) => ({ ...current, [id]: !current[id] }));

  const handleBulk = async (decision: 'approve' | 'reject') => {
    if (selectedEligible.length === 0) return;
    setBusy(true);
    setError('');
    try {
      await bulkApprovals({ ids: selectedEligible.map((item) => item.id), decision });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量处理失败');
    } finally {
      setBusy(false);
    }
  };

  const handleDecision = async (item: ApprovalInboxItem, decision: 'approve' | 'reject') => {
    if (item.risk === 'high' && decision === 'approve' && !window.confirm('这是高风险操作，确认批准一次？')) return;
    setBusy(true);
    setError('');
    try {
      await decideRuntimeAction(item.plan_id, item.id, decision, { userId: '', sessionId: '' });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : '审批失败');
    } finally {
      setBusy(false);
    }
  };

  const handleAnswer = async (item: ApprovalInboxItem) => {
    const draft = answers[item.id] ?? { option: '', text: '' };
    setBusy(true);
    setError('');
    try {
      await answerQuestion(item.plan_id, item.id, {
        answer: draft.text || undefined,
        answer_option: draft.option || undefined,
      });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : '回答失败');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-full max-h-full min-h-0 flex-col overflow-hidden bg-paper text-ink">
      <header className="flex h-16 shrink-0 items-center justify-between border-b border-line px-5">
        <div className="flex min-w-0 items-center gap-2">
          <Inbox className="h-4 w-4 shrink-0 text-proof" />
          <span className="text-[17px] font-semibold tracking-[-0.02em]">待办中心</span>
          <span className="text-[9.5px] font-semibold text-muted">{items.length} 项待处理</span>
          <span className="hidden items-center gap-1.5 border-l border-line pl-2 text-[9.5px] text-muted sm:flex">
            <span>{approvalCount} 审批</span><span>·</span><span>{questionCount} 提问</span>{highRiskCount > 0 ? <><span>·</span><span className="text-proof">{highRiskCount} 高风险</span></> : null}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <select value={kind} onChange={(event) => setKind(event.target.value as 'all' | 'approval' | 'question')} className="h-8 rounded-lg border border-line bg-surface px-2 text-[10.5px]">
            <option value="all">全部</option>
            <option value="approval">运行时审批</option>
            <option value="question">计划提问</option>
          </select>
          <button type="button" onClick={() => void refresh()} aria-label="刷新收件箱" className="flex h-8 w-8 items-center justify-center rounded-lg border border-line text-ink-soft transition-colors hover:border-accent hover:text-accent">
            <RefreshCw className={'h-3.5 w-3.5 ' + (loading ? 'animate-spin' : '')} />
          </button>
          <button type="button" onClick={onClose} aria-label="关闭收件箱" className="flex h-8 w-8 items-center justify-center rounded-lg border border-line text-ink-soft transition-colors hover:border-accent hover:text-accent">
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      </header>

      {error && <div className="border-b border-proof/30 bg-proof-soft px-5 py-2 text-[11px] text-proof">{error}</div>}

      <div className="flex shrink-0 items-center justify-between border-b border-line px-5 py-2.5 text-[10.5px]">
        <span className="text-muted">批量操作仅对低风险审批可用；高风险必须逐条处理。</span>
        <div className="flex items-center gap-2">
          <button type="button" disabled={busy || selectedEligible.length === 0} onClick={() => void handleBulk('approve')} className="inline-flex h-8 items-center gap-1 rounded-lg border border-ok/40 px-2.5 text-[10px] text-ink transition-colors hover:border-ok disabled:opacity-40">
            <Check className="h-3 w-3" /> 批量批准 ({selectedEligible.length})
          </button>
          <button type="button" disabled={busy || selectedEligible.length === 0} onClick={() => void handleBulk('reject')} className="inline-flex h-8 items-center gap-1 rounded-lg border border-proof/40 px-2.5 text-[10px] text-proof transition-colors hover:border-proof disabled:opacity-40">
            <X className="h-3 w-3" /> 批量拒绝
          </button>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading && items.length === 0 ? (
          <div className="flex items-center gap-2 px-4 py-6 text-[12px] text-muted"><Loader2 className="h-4 w-4 animate-spin" /> 加载中…</div>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center gap-2 px-4 py-10 text-[12px] text-muted">
            <Bell className="h-5 w-5" />
            没有待处理的审批或提问。
          </div>
        ) : (
          <ul className="divide-y divide-line">
            {items.map((item) => {
              const isQuestion = item.kind === 'question';
              const highRisk = item.risk === 'high';
              const draft = answers[item.id] ?? { option: '', text: '' };
              return (
                <li key={item.id} className="px-5 py-3.5">
                  <div className="flex items-start gap-3">
                    {!isQuestion && (
                      <input
                        type="checkbox"
                        aria-label={'选择 ' + item.title}
                        disabled={highRisk}
                        checked={Boolean(selected[item.id])}
                        onChange={() => toggle(item.id)}
                        className="mt-1"
                      />
                    )}
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2 text-[12px]">
                        <span className="font-medium">{item.title || (isQuestion ? '计划提问' : '运行时审批')}</span>
                        <span className={'rounded-full px-2 py-0.5 text-[9px] font-medium ' + (highRisk ? 'bg-proof-soft text-proof' : 'bg-surface-muted text-muted')}>
                          {isQuestion ? '提问' : riskLabel(item.risk)}
                        </span>
                        <span className="font-mono text-[9.5px] text-muted">等待 {waitingLabel(item.waiting_seconds)}</span>
                        {item.expires_at && <span className="font-mono text-[9.5px] text-muted">截止 {new Date(item.expires_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</span>}
                      </div>
                      <div className="mt-0.5 truncate text-[10.5px] text-muted">
                        {item.plan_title} · {item.task_id || '—'} {item.node_type ? '· ' + item.node_type : ''}
                      </div>
                      {isQuestion && (
                        <div className="mt-2 space-y-2">
                          <p className="text-[12px]">{item.prompt}</p>
                          {item.options && item.options.length > 0 ? (
                            <div className="flex flex-wrap gap-2">
                              {item.options.map((option) => (
                                <label key={option.id} className="inline-flex items-center gap-1 border border-line-strong px-2 py-1 text-[11px]">
                                  <input
                                    type={item.multi_select ? 'checkbox' : 'radio'}
                                    name={item.id}
                                    checked={draft.option === option.id}
                                    onChange={() => setAnswers({ ...answers, [item.id]: { ...draft, option: option.id } })}
                                  />
                                  <span title={option.description}>{option.label}</span>
                                </label>
                              ))}
                            </div>
                          ) : null}
                          {item.allow_free_text !== false && (
                            <input
                              value={draft.text}
                              onChange={(event) => setAnswers({ ...answers, [item.id]: { ...draft, text: event.target.value } })}
                              placeholder="补充说明（可选）"
                              className="w-full border border-line-strong bg-surface px-2 py-1 text-[12px]"
                            />
                          )}
                          <div className="flex items-center gap-2">
                            <button type="button" disabled={busy || (!draft.option && !draft.text)} onClick={() => void handleAnswer(item)} className="inline-flex h-7 items-center gap-1 border border-ink bg-ink px-2 text-[11px] text-paper disabled:opacity-40">
                              提交回答
                            </button>
                            <button type="button" onClick={() => onOpenPlan(item.plan_id)} className="text-[11px] text-ink-soft underline hover:text-ink">
                              打开计划
                            </button>
                          </div>
                        </div>
                      )}
                      {!isQuestion && (
                        <div className="mt-2 flex flex-wrap items-center gap-1.5">
                          <button type="button" disabled={busy} onClick={() => void handleDecision(item, 'approve')} className="h-7 border border-ok/60 bg-ok-soft px-2 text-[10.5px] font-semibold text-ok hover:border-ok disabled:opacity-40">
                            批准一次
                          </button>
                          <button type="button" disabled={busy} onClick={() => void handleDecision(item, 'reject')} className="h-7 border border-danger/50 bg-surface px-2 text-[10.5px] font-semibold text-danger hover:border-danger disabled:opacity-40">
                            拒绝
                          </button>
                          <button type="button" onClick={() => onOpenPlan(item.plan_id)} className="h-7 border border-line-strong bg-surface px-2 text-[10.5px] text-ink-soft hover:border-ink hover:text-ink">
                            打开计划
                          </button>
                          {highRisk && <span className="inline-flex items-center gap-1 text-[9.5px] text-danger"><ShieldAlert className="h-3 w-3" /> 高风险需逐条确认</span>}
                        </div>
                      )}
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

export default ApprovalInboxPanel;