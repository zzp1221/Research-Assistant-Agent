import { useCallback, useEffect, useMemo, useState } from 'react';
import { Brain, Loader2, Pin, PinOff, Plus, RefreshCw, Search, Trash2 } from 'lucide-react';
import type { MemoryRecord, MemorySettings, MemoryStats } from '../../contracts/api';
import {
  createMemory,
  deleteMemory,
  getMemorySettings,
  getMemoryStats,
  listMemories,
  saveMemorySettings,
  updateMemory,
} from '../../services/api/researchAssistantApi';

const KINDS = ['profile', 'fact', 'episode', 'procedure', 'constraint', 'negative', 'document', 'reflection'];

const kindLabel: Record<string, string> = {
  profile: '偏好',
  fact: '事实',
  episode: '事件',
  procedure: '经验',
  constraint: '约束',
  negative: '负结果',
  document: '文档',
  reflection: '归纳',
};

export function MemoryPanel({ onClose }: { onClose?: () => void }) {
  const [items, setItems] = useState<MemoryRecord[]>([]);
  const [stats, setStats] = useState<MemoryStats | null>(null);
  const [settings, setSettings] = useState<MemorySettings | null>(null);
  const [query, setQuery] = useState('');
  const [kind, setKind] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [draft, setDraft] = useState({ kind: 'fact', title: '', content: '', importance: 6, pinned: false });

  const reload = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [list, nextStats, nextSettings] = await Promise.all([
        listMemories({ kind: kind || undefined, q: query || undefined, limit: 100 }),
        getMemoryStats(),
        getMemorySettings(),
      ]);
      setItems(list.memories);
      setStats(nextStats);
      setSettings(nextSettings);
    } catch (err) {
      setError(err instanceof Error ? err.message : '记忆加载失败');
    } finally {
      setLoading(false);
    }
  }, [kind, query]);

  useEffect(() => { void reload(); }, [reload]);

  const totals = useMemo(() => stats?.by_kind ?? {}, [stats]);

  const submit = async () => {
    if (!draft.title.trim() && !draft.content.trim()) return;
    setLoading(true);
    try {
      await createMemory(draft);
      setDraft({ kind: 'fact', title: '', content: '', importance: 6, pinned: false });
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建记忆失败');
    } finally {
      setLoading(false);
    }
  };

  const togglePin = async (record: MemoryRecord) => {
    await updateMemory(record.id, { pinned: !record.pinned });
    await reload();
  };

  const remove = async (record: MemoryRecord) => {
    await deleteMemory(record.id);
    await reload();
  };

  const saveSettings = async (next: MemorySettings) => {
    setSettings(next);
    try {
      await saveMemorySettings(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存设置失败');
    }
  };

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="flex items-center justify-between border-b border-line px-4 py-2">
        <div className="flex items-center gap-2 text-[13px] font-semibold text-ink"><Brain className="h-4 w-4" />记忆</div>
        <div className="flex items-center gap-2">
          <button type="button" onClick={() => void reload()} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2 text-[12px] text-ink-soft hover:text-ink">
            <RefreshCw className="h-3.5 w-3.5" />刷新
          </button>
          {onClose && <button type="button" onClick={onClose} className="text-[12px] text-ink-soft hover:text-ink">关闭</button>}
        </div>
      </div>
      <div className="grid gap-3 border-b border-line bg-surface px-4 py-3 md:grid-cols-[1fr_auto]">
        <div className="grid gap-2">
          <div className="flex items-center gap-2">
            <Search className="h-4 w-4 text-ink-soft" />
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索标题或内容" className="h-8 flex-1 border border-line bg-paper px-2 text-[12px]" />
            <select value={kind} onChange={(event) => setKind(event.target.value)} className="h-8 border border-line bg-paper px-2 text-[12px]">
              <option value="">全部类型</option>
              {KINDS.map((entry) => <option key={entry} value={entry}>{kindLabel[entry]}</option>)}
            </select>
          </div>
          <div className="grid gap-2 md:grid-cols-[140px_1fr_1fr_90px_auto]">
            <select value={draft.kind} onChange={(event) => setDraft({ ...draft, kind: event.target.value })} className="h-8 border border-line bg-paper px-2 text-[12px]">
              {KINDS.map((entry) => <option key={entry} value={entry}>{kindLabel[entry]}</option>)}
            </select>
            <input value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} placeholder="标题" className="h-8 border border-line bg-paper px-2 text-[12px]" />
            <input value={draft.content} onChange={(event) => setDraft({ ...draft, content: event.target.value })} placeholder="内容" className="h-8 border border-line bg-paper px-2 text-[12px]" />
            <input type="number" min={1} max={10} value={draft.importance} onChange={(event) => setDraft({ ...draft, importance: Number(event.target.value) })} className="h-8 border border-line bg-paper px-2 text-[12px]" />
            <button type="button" onClick={() => void submit()} disabled={loading} className="inline-flex h-8 items-center gap-1 border border-ink bg-ink px-2 text-[12px] font-semibold text-paper disabled:opacity-50">
              <Plus className="h-3.5 w-3.5" />添加
            </button>
          </div>
        </div>
        <div className="text-[11px] text-ink-soft">
          <div>共 {stats?.total ?? 0} 条 · 活跃 {stats?.active ?? 0} · pinned {stats?.pinned ?? 0}</div>
          <div>tokens ≈ {stats?.tokens ?? 0}</div>
          <div className="mt-1 flex flex-wrap gap-1">{Object.entries(totals).map(([key, value]) => <span key={key} className="border border-line px-1">{kindLabel[key] ?? key}:{value}</span>)}</div>
        </div>
      </div>
      {settings && (
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-2 text-[12px] text-ink-soft">
          <label className="flex items-center gap-1"><input type="checkbox" checked={settings.enabled} onChange={(event) => void saveSettings({ ...settings, enabled: event.target.checked })} />启用记忆</label>
          <label className="flex items-center gap-1">模式
            <select value={settings.mode} onChange={(event) => void saveSettings({ ...settings, mode: event.target.value })} className="h-7 border border-line bg-paper px-1">
              <option value="on">on</option><option value="shadow">shadow</option><option value="off">off</option>
            </select>
          </label>
          <label className="flex items-center gap-1">检索 TopK <input type="number" value={settings.retrieval_topk} onChange={(event) => void saveSettings({ ...settings, retrieval_topk: Number(event.target.value) })} className="h-7 w-16 border border-line bg-paper px-1" /></label>
          <label className="flex items-center gap-1">core 预算 <input type="number" value={settings.core_budget_tokens} onChange={(event) => void saveSettings({ ...settings, core_budget_tokens: Number(event.target.value) })} className="h-7 w-20 border border-line bg-paper px-1" /></label>
          <label className="flex items-center gap-1">retrieved 预算 <input type="number" value={settings.retrieved_budget_tokens} onChange={(event) => void saveSettings({ ...settings, retrieved_budget_tokens: Number(event.target.value) })} className="h-7 w-20 border border-line bg-paper px-1" /></label>
        </div>
      )}
      {error && <div className="border-b border-proof/40 bg-proof-soft px-4 py-2 text-[12px] text-proof">{error}</div>}
      <div className="min-h-0 flex-1 overflow-auto px-4 py-3">
        {loading && items.length === 0 ? <div className="flex items-center gap-2 text-[12px] text-ink-soft"><Loader2 className="h-4 w-4 animate-spin" />加载中</div> : null}
        {!loading && items.length === 0 ? <div className="text-[12px] text-ink-soft">暂无记忆。可在上方手动添加，或让计划/任务执行后自动写入。</div> : null}
        <div className="grid gap-2">
          {items.map((record) => (
            <div key={record.id} className="border border-line bg-paper px-3 py-2">
              <div className="flex items-start justify-between gap-2">
                <div>
                  <div className="flex flex-wrap items-center gap-2 text-[12px] font-semibold text-ink">
                    <span className="border border-line-strong px-1 text-[10px] uppercase">{kindLabel[record.kind] ?? record.kind}</span>
                    <span>{record.title || record.content.slice(0, 40)}</span>
                    <span className="text-[10px] text-ink-soft">{record.trust} · {record.status} · importance {record.importance}</span>
                  </div>
                  <div className="mt-1 whitespace-pre-wrap text-[12px] text-ink-soft">{record.content}</div>
                  <div className="mt-1 text-[10px] text-ink-soft">来源 {record.source.type}{record.source.ref ? ':' + record.source.ref : ''} · 更新 {record.updated_at?.slice(0, 10)}</div>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <button type="button" title={record.pinned ? '取消 pin' : 'pin'} onClick={() => void togglePin(record)} className="p-1 text-ink-soft hover:text-ink">{record.pinned ? <PinOff className="h-3.5 w-3.5" /> : <Pin className="h-3.5 w-3.5" />}</button>
                  <button type="button" title="删除" onClick={() => void remove(record)} className="p-1 text-ink-soft hover:text-proof"><Trash2 className="h-3.5 w-3.5" /></button>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

export default MemoryPanel;
