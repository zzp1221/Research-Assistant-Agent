import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Check,
  GitCompare,
  Loader2,
  RefreshCw,
  Save,
  ShieldCheck,
  X,
} from 'lucide-react';
import type {
  PlanDiffEntry,
  PlanGraph,
  PlanRevision,
  PlanRevisionSummary,
  ValidationIssue,
} from '../../contracts/api';
import {
  approveRevision,
  createRevision,
  executeRevision,
  getPlanDiff,
  getRevision,
  listRevisions,
  rejectRevision,
  validateRevision,
} from '../../services/api/researchAssistantApi';

interface PlanReviewPanelProps {
  planId: string;
  onClose: () => void;
  onApplied: () => void;
}

const revisionStatusLabel = (status: string): string => {
  switch (status) {
    case 'draft':
      return '草稿';
    case 'approved':
      return '已批准';
    case 'executing':
      return '执行中';
    case 'completed':
      return '已完成';
    case 'superseded':
      return '已被取代';
    case 'rejected':
      return '已拒绝';
    default:
      return status;
  }
};

const formatTime = (value?: string): string => {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
};

const renderValue = (value: unknown): string => {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

interface DraftNode {
  id: string;
  name: string;
  type: string;
  description: string;
  assigned_to: string;
  timeout_seconds: number;
  retry_limit: number;
  dependencies: string[];
  enabled: boolean;
  inputs: Record<string, unknown>;
}

interface DraftForm {
  title: string;
  maxCostUSD: number;
  maxTokens: number;
  maxDurationSeconds: number;
  nodes: DraftNode[];
}

const formFromGraph = (graph: PlanGraph): DraftForm => ({
  title: graph.title ?? '',
  maxCostUSD: graph.budget?.max_cost_usd ?? 0,
  maxTokens: graph.budget?.max_tokens ?? 0,
  maxDurationSeconds: graph.budget?.max_duration_seconds ?? 0,
  nodes: (graph.nodes ?? []).map((node) => ({
    id: node.id,
    name: node.name,
    type: node.type,
    description: node.description ?? '',
    assigned_to: node.assigned_to,
    timeout_seconds: node.timeout_seconds ?? 0,
    retry_limit: node.retry_limit ?? 0,
    dependencies: [...(node.dependencies ?? [])],
    enabled: node.enabled !== false,
    inputs: { ...(node.inputs ?? {}) },
  })),
});

const graphFromForm = (base: PlanGraph, form: DraftForm): PlanGraph => ({
  ...base,
  title: form.title,
  budget: {
    ...base.budget,
    max_cost_usd: form.maxCostUSD,
    max_tokens: form.maxTokens,
    max_duration_seconds: form.maxDurationSeconds,
  },
  nodes: base.nodes.map((node) => {
    const draft = form.nodes.find((candidate) => candidate.id === node.id);
    if (!draft) return node;
    return {
      ...node,
      name: draft.name,
      description: draft.description,
      assigned_to: draft.assigned_to,
      timeout_seconds: draft.timeout_seconds,
      retry_limit: draft.retry_limit,
      dependencies: draft.dependencies,
      enabled: draft.enabled,
      inputs: draft.inputs,
    };
  }),
});

export function PlanReviewPanel({ planId, onClose, onApplied }: PlanReviewPanelProps) {
  const [revisions, setRevisions] = useState<PlanRevisionSummary[]>([]);
  const [currentRevision, setCurrentRevision] = useState(0);
  const [selected, setSelected] = useState<number | null>(null);
  const [detail, setDetail] = useState<PlanRevision | null>(null);
  const [form, setForm] = useState<DraftForm | null>(null);
  const [activeNodeId, setActiveNodeId] = useState<string>('');
  const [diff, setDiff] = useState<PlanDiffEntry[]>([]);
  const [diffFrom, setDiffFrom] = useState<number | null>(null);
  const [diffTo, setDiffTo] = useState<number | null>(null);
  const [validation, setValidation] = useState<ValidationIssue[]>([]);
  const [validState, setValidState] = useState<'unknown' | 'valid' | 'invalid'>('unknown');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');

  const refresh = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const payload = await listRevisions(planId);
      setRevisions(payload.revisions);
      setCurrentRevision(payload.current_revision ?? 0);
      const latest = payload.revisions[payload.revisions.length - 1];
      const target = selected ?? latest?.revision ?? 1;
      setSelected(target);
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载 revision 失败');
    } finally {
      setLoading(false);
    }
  }, [planId, selected]);

  useEffect(() => {
    queueMicrotask(() => void refresh());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [planId]);

  useEffect(() => {
    if (selected === null) return;
    let cancelled = false;
    queueMicrotask(() => {
      if (cancelled) return;
      setDetail(null);
      setValidation([]);
      setValidState('unknown');
      getRevision(planId, selected)
        .then((revision) => {
          if (cancelled) return;
          setDetail(revision);
          if (revision.graph) {
            setForm(formFromGraph(revision.graph));
            setActiveNodeId(revision.graph.nodes?.[0]?.id ?? '');
          }
        })
        .catch((err) => {
          if (!cancelled) setError(err instanceof Error ? err.message : '加载 revision 详情失败');
        });
    });
    return () => {
      cancelled = true;
    };
  }, [planId, selected]);

  const activeNode = useMemo(() => form?.nodes.find((node) => node.id === activeNodeId) ?? null, [form, activeNodeId]);
  const latestRevision = revisions.length > 0 ? revisions[revisions.length - 1].revision : 0;

  const updateNode = (patch: Partial<DraftNode>) => {
    if (!form || !activeNode) return;
    setForm({
      ...form,
      nodes: form.nodes.map((node) => (node.id === activeNode.id ? { ...node, ...patch } : node)),
    });
    setValidState('unknown');
  };

  const handleSaveDraft = async () => {
    if (!detail?.graph || !form) return;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      const graph = graphFromForm(detail.graph, form);
      const created = await createRevision(planId, {
        base_revision: latestRevision,
        reason: 'edited in review panel',
        graph: graph as unknown as Record<string, unknown>,
      });
      setNotice('已保存为新草稿 revision');
      await refresh();
      // refresh() may still hold the previous selection in its closure; make
      // sure the freshly created draft is what the user sees next.
      if (created?.revision?.revision) setSelected(created.revision.revision);
      onApplied();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存草稿失败');
    } finally {
      setBusy(false);
    }
  };

  const handleValidate = async () => {
    if (selected === null) return;
    setBusy(true);
    setError('');
    try {
      const result = await validateRevision(planId, selected);
      setValidation(result.errors);
      setValidState(result.valid ? 'valid' : 'invalid');
    } catch (err) {
      setError(err instanceof Error ? err.message : '校验失败');
    } finally {
      setBusy(false);
    }
  };

  const handleApproveAndRun = async () => {
    if (selected === null) return;
    setBusy(true);
    setError('');
    setNotice('');
    try {
      await approveRevision(planId, selected);
      await executeRevision(planId, selected);
      setNotice('已批准并开始执行 revision ' + selected);
      onApplied();
    } catch (err) {
      setError(err instanceof Error ? err.message : '批准执行失败');
    } finally {
      setBusy(false);
    }
  };

  const handleReject = async () => {
    if (selected === null) return;
    setBusy(true);
    setError('');
    try {
      await rejectRevision(planId, selected);
      setNotice('已拒绝 revision ' + selected);
      await refresh();
      onApplied();
    } catch (err) {
      setError(err instanceof Error ? err.message : '拒绝失败');
    } finally {
      setBusy(false);
    }
  };

  const handleCompare = async (from: number, to: number) => {
    setBusy(true);
    setError('');
    try {
      const entries = await getPlanDiff(planId, from, to);
      setDiff(entries);
      setDiffFrom(from);
      setDiffTo(to);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'diff 加载失败');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex h-full max-h-full min-h-0 flex-col overflow-hidden rounded-3xl bg-paper text-ink">
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-line px-4">
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-proof" />
          <span className="text-[17px] font-semibold tracking-[-0.02em]">计划审阅</span>
          <span className="text-[9px] text-muted">版本 · 差异 · 审阅</span>
        </div>
        <button type="button" onClick={onClose} aria-label="关闭审阅面板" className="flex h-8 w-8 items-center justify-center rounded-lg border border-line text-ink-soft transition-colors hover:border-accent hover:text-accent">
          <X className="h-3.5 w-3.5" />
        </button>
      </header>

      {error && <div className="border-b border-danger/40 bg-danger/10 px-4 py-2 text-[12px] text-danger">{error}</div>}
      {notice && <div className="border-b border-ok/40 bg-ok/10 px-4 py-2 text-[12px] text-ink">{notice}</div>}

      <div className="flex min-h-0 flex-1">
        <aside className="w-56 shrink-0 overflow-y-auto border-r border-line">
          <div className="flex items-center justify-between px-3 py-2">
            <span className="text-[9px] font-semibold text-muted">历史 revision</span>
            <button type="button" onClick={() => void refresh()} aria-label="刷新 revision" className="text-ink-soft hover:text-ink">
              <RefreshCw className={'h-3 w-3 ' + (loading ? 'animate-spin' : '')} />
            </button>
          </div>
          <ul>
            {revisions.map((revision) => (
              <li key={revision.id}>
                <button
                  type="button"
                  onClick={() => setSelected(revision.revision)}
                  className={
                    'flex w-full flex-col gap-0.5 border-l-2 px-3 py-2 text-left ' +
                    (selected === revision.revision ? 'border-proof bg-surface' : 'border-transparent hover:bg-surface')
                  }
                >
                  <span className="flex items-center justify-between text-[12px] font-medium">
                    <span>r{revision.revision}</span>
                    <span className="journal-num text-[10px] text-muted">{revisionStatusLabel(revision.status)}</span>
                  </span>
                  <span className="truncate text-[11px] text-muted">{revision.reason || '（无说明）'}</span>
                  <span className="journal-num text-[9px] text-muted">{formatTime(revision.created_at)}</span>
                </button>
              </li>
            ))}
          </ul>
          {selected !== null && selected > 1 && (
            <div className="border-t border-line px-3 py-2">
              <button type="button" onClick={() => void handleCompare(selected - 1, selected)} className="inline-flex items-center gap-1 text-[11px] text-ink-soft hover:text-ink">
                <GitCompare className="h-3 w-3" /> 与 r{selected - 1} 比较
              </button>
            </div>
          )}
        </aside>

        <section className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
          <div className="border-b border-line px-4 py-2">
            <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted">
              <span>当前执行 r{currentRevision || '—'}</span>
              <span>·</span>
              <span>最新 r{latestRevision}</span>
              {selected !== null && selected !== latestRevision && <span className="text-warn">· 查看 r{selected}（非最新）</span>}
              {detail?.hash && <><span>·</span><span className="journal-num">{detail.hash.slice(0, 12)}</span></>}
            </div>
          </div>

          {form && (
            <div className="border-b border-line bg-surface px-4 py-2.5">
              <div className="flex flex-wrap items-end gap-3">
                <div className="mr-1">
                  <div className="text-[9px] font-semibold text-muted">预算 / Budget</div>
                  <div className="mt-0.5 text-[10px] text-muted">保存草稿后生效</div>
                </div>
                <label className="block text-[10px] text-muted">
                  最大成本 USD
                  <input type="number" min={0} step="0.01" value={form.maxCostUSD} onChange={(event) => setForm({ ...form, maxCostUSD: Number(event.target.value) })} className="mt-0.5 h-8 w-28 rounded-lg border border-line bg-surface px-2 font-mono text-[10.5px] text-ink" />
                </label>
                <label className="block text-[10px] text-muted">
                  最大 tokens
                  <input type="number" min={0} value={form.maxTokens} onChange={(event) => setForm({ ...form, maxTokens: Number(event.target.value) })} className="mt-0.5 h-8 w-28 rounded-lg border border-line bg-surface px-2 font-mono text-[10.5px] text-ink" />
                </label>
                <label className="block text-[10px] text-muted">
                  最大时长（秒）
                  <input type="number" min={0} value={form.maxDurationSeconds} onChange={(event) => setForm({ ...form, maxDurationSeconds: Number(event.target.value) })} className="mt-0.5 h-8 w-28 rounded-lg border border-line bg-surface px-2 font-mono text-[10.5px] text-ink" />
                </label>
              </div>
            </div>
          )}

          {form && (
            <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] overflow-hidden">
              <div className="min-h-0 overflow-y-auto border-r border-line">
                <div className="px-3 py-2 text-[9px] font-semibold text-muted">节点</div>
                <ul>
                  {form.nodes.map((node) => (
                    <li key={node.id}>
                      <button
                        type="button"
                        onClick={() => setActiveNodeId(node.id)}
                        className={
                          'flex w-full items-center justify-between border-l-2 px-3 py-1.5 text-left text-[12px] ' +
                          (activeNodeId === node.id ? 'border-proof bg-surface' : 'border-transparent hover:bg-surface')
                        }
                      >
                        <span className="min-w-0 truncate">{node.name || node.id}</span>
                        <span className="journal-num text-[9px] text-muted">{node.type}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </div>

              <div className="min-h-0 overflow-y-auto p-3">
                {activeNode ? (
                  <div className="space-y-3">
                    <label className="block text-[11px]">
                      <span className="text-[9px] font-semibold text-muted">名称</span>
                      <input value={activeNode.name} onChange={(event) => updateNode({ name: event.target.value })} className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px]" />
                    </label>
                    <label className="block text-[11px]">
                      <span className="text-[9px] font-semibold text-muted">描述</span>
                      <textarea value={activeNode.description} rows={2} onChange={(event) => updateNode({ description: event.target.value })} className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px]" />
                    </label>
                    <div className="grid grid-cols-2 gap-2">
                      <label className="block text-[11px]">
                        <span className="text-[9px] font-semibold text-muted">执行者</span>
                        <input value={activeNode.assigned_to} onChange={(event) => updateNode({ assigned_to: event.target.value })} className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px]" />
                      </label>
                      <label className="block text-[11px]">
                        <span className="text-[9px] font-semibold text-muted">超时（秒）</span>
                        <input type="number" min={0} value={activeNode.timeout_seconds} onChange={(event) => updateNode({ timeout_seconds: Number(event.target.value) })} className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px]" />
                      </label>
                      <label className="block text-[11px]">
                        <span className="text-[9px] font-semibold text-muted">重试</span>
                        <input type="number" min={0} value={activeNode.retry_limit} onChange={(event) => updateNode({ retry_limit: Number(event.target.value) })} className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 text-[11px]" />
                      </label>
                      <label className="flex items-end gap-2 text-[11px]">
                        <input type="checkbox" checked={activeNode.enabled} onChange={(event) => updateNode({ enabled: event.target.checked })} />
                        <span>启用节点</span>
                      </label>
                    </div>
                    <fieldset className="border border-line p-2">
                      <legend className="journal-caps px-1 text-[9px] text-muted">依赖</legend>
                      <div className="max-h-32 space-y-1 overflow-y-auto">
                        {form.nodes.filter((candidate) => candidate.id !== activeNode.id).map((candidate) => (
                          <label key={candidate.id} className="flex items-center gap-2 text-[11px]">
                            <input
                              type="checkbox"
                              checked={activeNode.dependencies.includes(candidate.id)}
                              onChange={(event) => {
                                const next = event.target.checked
                                  ? [...activeNode.dependencies, candidate.id]
                                  : activeNode.dependencies.filter((id) => id !== candidate.id);
                                updateNode({ dependencies: next });
                              }}
                            />
                            <span className="truncate">{candidate.name || candidate.id}</span>
                          </label>
                        ))}
                      </div>
                    </fieldset>
                    <label className="block text-[11px]">
                      <span className="text-[9px] font-semibold text-muted">输入 JSON</span>
                      <textarea
                        key={activeNode.id}
                        rows={4}
                        defaultValue={JSON.stringify(activeNode.inputs ?? {}, null, 2)}
                        onBlur={(event) => {
                          try {
                            updateNode({ inputs: JSON.parse(event.target.value || '{}') as Record<string, unknown> });
                          } catch {
                            setError('输入必须是合法 JSON');
                          }
                        }}
                        className="mt-1 w-full rounded-lg border border-line bg-surface px-2 py-1.5 font-mono text-[10.5px]"
                      />
                    </label>
                  </div>
                ) : (
                  <p className="text-[12px] text-muted">选择一个节点进行编辑。</p>
                )}
              </div>
            </div>
          )}

          {validation.length > 0 && (
            <div className="border-t border-line px-4 py-2">
              <div className="mb-1 text-[9px] font-semibold text-proof">校验问题</div>
              <ul className="space-y-1 text-[11px]">
                {validation.map((issue, index) => (
                  <li key={index} className="text-danger">
                    {issue.node_id ? <span className="journal-num">[{issue.node_id}] </span> : null}
                    {issue.message}
                    {issue.remediation ? <span className="text-muted"> — {issue.remediation}</span> : null}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {diff.length > 0 && (
            <div className="border-t border-line px-4 py-2">
              <div className="journal-caps mb-1 text-[9px] text-muted">
                DIFF r{diffFrom} → r{diffTo}
              </div>
              <div className="max-h-56 overflow-y-auto">
                <table className="w-full text-[11px]">
                  <thead>
                    <tr className="border-b border-line text-left text-muted">
                      <th className="py-1 pr-2">类型</th>
                      <th className="py-1 pr-2">节点/字段</th>
                      <th className="py-1 pr-2">之前</th>
                      <th className="py-1">之后</th>
                    </tr>
                  </thead>
                  <tbody>
                    {diff.map((entry, index) => (
                      <tr key={index} className="border-b border-line/50 align-top">
                        <td className="py-1 pr-2 whitespace-nowrap">{entry.kind}</td>
                        <td className="py-1 pr-2 whitespace-nowrap">{entry.node_id ?? ''} {entry.field ?? ''}</td>
                        <td className="max-w-[14rem] break-words py-1 pr-2 text-muted">{renderValue(entry.before)}</td>
                        <td className="max-w-[14rem] break-words py-1">{renderValue(entry.after)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </section>
      </div>

      <footer className="flex shrink-0 items-center justify-between gap-2 border-t border-line px-4 py-2">
        <div className="text-[11px] text-muted">
          {validState === 'valid' ? <span className="text-ok">校验通过</span> : validState === 'invalid' ? <span className="text-danger">校验未通过</span> : '保存草稿不会自动执行'}
        </div>
        <div className="flex items-center gap-2">
          <button type="button" disabled={busy} onClick={() => void handleValidate()} className="inline-flex h-8 items-center gap-1 rounded-lg border border-line px-2.5 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-40">
            <Check className="h-3 w-3" /> 校验
          </button>
          <button type="button" disabled={busy || !form} onClick={() => void handleSaveDraft()} className="inline-flex h-8 items-center gap-1 rounded-lg border border-line px-2.5 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-40">
            {busy ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />} 保存草稿
          </button>
          <button type="button" disabled={busy || selected === null} onClick={() => void handleReject()} className="inline-flex h-7 items-center gap-1 border border-danger/60 px-2 text-[11px] text-danger hover:border-danger disabled:opacity-40">
            <X className="h-3 w-3" /> 拒绝
          </button>
          <button type="button" disabled={busy || selected === null} onClick={() => void handleApproveAndRun()} className="inline-flex h-8 items-center gap-1 rounded-lg bg-accent px-2.5 text-[10px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-40">
            <ShieldCheck className="h-3 w-3" /> 批准并运行
          </button>
        </div>
      </footer>
    </div>
  );
}

export default PlanReviewPanel;