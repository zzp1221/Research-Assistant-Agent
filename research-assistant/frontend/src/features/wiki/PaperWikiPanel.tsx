import { useCallback, useEffect, useMemo, useState } from 'react';
import { BookOpen, Check, FileText, Link2, Loader2, Plus, RefreshCw, Search, X } from 'lucide-react';
import type { WikiLink, WikiPaperPage, WikiPoint, WikiStats } from '../../contracts/api';
import {
  archiveWikiPaper,
  confirmWikiLink,
  createWikiPaper,
  createWikiPoint,
  deleteWikiPoint,
  getWikiStats,
  listWikiPaperLinks,
  listWikiPapers,
  listWikiPoints,
  rejectWikiLink,
  startWikiBuild,
  updateWikiPoint,
} from '../../services/api/wikiApi';

const POINT_GROUPS: { kind: string; label: string }[] = [
  { kind: 'claim', label: '结论' },
  { kind: 'method', label: '方法' },
  { kind: 'result', label: '结果' },
  { kind: 'task', label: '任务' },
  { kind: 'procedure', label: '修复经验' },
  { kind: 'negative', label: '失败与负结果' },
  { kind: 'limitation', label: '局限' },
  { kind: 'open_question', label: '待验证' },
  { kind: 'constraint', label: '环境约束' },
  { kind: 'user_note', label: '用户笔记' },
];

const trustLabel: Record<string, string> = {
  verified: '已验证',
  inferred: '推断',
  user_explicit: '用户',
  untrusted: '不可信',
};

const statusLabel: Record<string, string> = {
  active: '生效',
  pending_review: '待审核',
  superseded: '已替代',
  archived: '已归档',
  deleted: '已删除',
};

export function PaperWikiPanel({ onClose }: { onClose?: () => void }) {
  const [papers, setPapers] = useState<WikiPaperPage[]>([]);
  const [stats, setStats] = useState<WikiStats | null>(null);
  const [query, setQuery] = useState('');
  const [selectedID, setSelectedID] = useState('');
  const [selected, setSelected] = useState<WikiPaperPage | null>(null);
  const [points, setPoints] = useState<WikiPoint[]>([]);
  const [links, setLinks] = useState<WikiLink[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [note, setNote] = useState('');
  const [draft, setDraft] = useState({ title: '', arxiv_id: '', doi: '' });

  const reloadList = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [list, nextStats] = await Promise.all([
        listWikiPapers({ q: query || undefined, limit: 100 }),
        getWikiStats(),
      ]);
      setPapers(list.papers);
      setStats(nextStats);
      if (!selectedID && list.papers.length > 0) {
        setSelectedID(list.papers[0].paper_id);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '论文列表加载失败');
    } finally {
      setLoading(false);
    }
  }, [query, selectedID]);

  const reloadDetail = useCallback(async (paperID: string) => {
    if (!paperID) return;
    try {
      const [paperList, pointList, linkList] = await Promise.all([
        listWikiPapers({ limit: 200 }),
        listWikiPoints(paperID, { limit: 200 }),
        listWikiPaperLinks(paperID, { limit: 200 }),
      ]);
      const paper = paperList.papers.find((entry) => entry.paper_id === paperID) ?? null;
      setSelected(paper);
      setPoints(pointList.points);
      setLinks(linkList.links);
    } catch (err) {
      setError(err instanceof Error ? err.message : '论文详情加载失败');
    }
  }, []);

  useEffect(() => { void reloadList(); }, [reloadList]);
  useEffect(() => { void reloadDetail(selectedID); }, [selectedID, reloadDetail]);

  const grouped = useMemo(() => {
    const map = new Map<string, WikiPoint[]>();
    for (const point of points) {
      const list = map.get(point.kind) ?? [];
      list.push(point);
      map.set(point.kind, list);
    }
    return map;
  }, [points]);

  const submitPaper = async () => {
    if (!draft.title.trim() && !draft.arxiv_id.trim() && !draft.doi.trim()) return;
    setLoading(true);
    try {
      const paper = await createWikiPaper(draft);
      setDraft({ title: '', arxiv_id: '', doi: '' });
      setSelectedID(paper.paper_id);
      await reloadList();
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建论文失败');
    } finally {
      setLoading(false);
    }
  };

  const addNote = async () => {
    if (!selected || !note.trim()) return;
    try {
      await createWikiPoint(selected.paper_id, note.trim());
      setNote('');
      await reloadDetail(selected.paper_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : '添加笔记失败');
    }
  };

  const confirmPoint = async (point: WikiPoint) => {
    await updateWikiPoint(point.record.id, { status: 'active' });
    await reloadDetail(point.paper_id);
  };

  const removePoint = async (point: WikiPoint) => {
    await deleteWikiPoint(point.record.id);
    await reloadDetail(point.paper_id);
  };

  const rebuild = async () => {
    setLoading(true);
    try {
      await startWikiBuild('full');
      await reloadList();
    } catch (err) {
      setError(err instanceof Error ? err.message : '重建失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="flex items-center justify-between border-b border-line px-4 py-2">
        <div className="flex items-center gap-2 text-[13px] font-semibold text-ink">
          <BookOpen className="h-4 w-4" />论文知识 Wiki
          {stats && <span className="text-[11px] font-normal text-ink-soft">论文 {stats.papers} · Point {stats.points} · Link {stats.links}（待审核 {stats.pending_links}）</span>}
        </div>
        <div className="flex items-center gap-2">
          <button type="button" onClick={() => void rebuild()} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2 text-[12px] text-ink-soft hover:text-ink">
            <RefreshCw className="h-3.5 w-3.5" />重建
          </button>
          {onClose && <button type="button" onClick={onClose} className="text-[12px] text-ink-soft hover:text-ink">关闭</button>}
        </div>
      </div>
      {error && <div className="border-b border-red-200 bg-red-50 px-4 py-1.5 text-[12px] text-red-700">{error}</div>}
      <div className="grid min-h-0 flex-1 grid-cols-[320px_1fr]">
        <aside className="flex min-h-0 flex-col border-r border-line">
          <div className="grid gap-2 border-b border-line bg-surface px-3 py-2">
            <div className="flex items-center gap-2">
              <Search className="h-4 w-4 text-ink-soft" />
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索论文标题/摘要/DOI" className="h-8 flex-1 border border-line bg-paper px-2 text-[12px]" />
            </div>
            <div className="grid gap-1">
              <input value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} placeholder="标题" className="h-8 border border-line bg-paper px-2 text-[12px]" />
              <div className="flex gap-1">
                <input value={draft.arxiv_id} onChange={(event) => setDraft({ ...draft, arxiv_id: event.target.value })} placeholder="arXiv ID" className="h-8 min-w-0 flex-1 border border-line bg-paper px-2 text-[12px]" />
                <input value={draft.doi} onChange={(event) => setDraft({ ...draft, doi: event.target.value })} placeholder="DOI" className="h-8 min-w-0 flex-1 border border-line bg-paper px-2 text-[12px]" />
                <button type="button" onClick={() => void submitPaper()} className="inline-flex h-8 items-center gap-1 border border-ink bg-ink px-2 text-[12px] text-paper">
                  <Plus className="h-3.5 w-3.5" />添加
                </button>
              </div>
            </div>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto">
            {loading && papers.length === 0 ? (
              <div className="flex items-center gap-2 px-3 py-3 text-[12px] text-ink-soft"><Loader2 className="h-4 w-4 animate-spin" />加载中</div>
            ) : papers.length === 0 ? (
              <div className="px-3 py-3 text-[12px] text-ink-soft">还没有论文页。上传 PDF 或在上方添加 arXiv/DOI。</div>
            ) : papers.map((paper) => (
              <button
                key={paper.paper_id}
                type="button"
                onClick={() => setSelectedID(paper.paper_id)}
                className={paper.paper_id === selectedID ? 'block w-full border-b border-line bg-surface px-3 py-2 text-left' : 'block w-full border-b border-line px-3 py-2 text-left hover:bg-surface'}
              >
                <div className="truncate text-[12px] font-semibold text-ink">{paper.title}</div>
                <div className="mt-0.5 flex items-center gap-2 text-[11px] text-ink-soft">
                  <span>{paper.year || '—'}</span>
                  <span>{paper.venue || paper.arxiv_id || paper.doi || ''}</span>
                  <span className={paper.reproduction?.status === 'failed' ? 'text-red-600' : ''}>{paper.reproduction?.status === 'none' ? '' : paper.reproduction?.status}</span>
                </div>
              </button>
            ))}
          </div>
        </aside>
        <section className="min-h-0 overflow-y-auto px-4 py-3">
          {!selected ? (
            <div className="text-[12px] text-ink-soft">选择左侧论文查看详情。</div>
          ) : (
            <div className="grid gap-4">
              <header>
                <h2 className="text-[16px] font-semibold text-ink">{selected.title}</h2>
                <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-ink-soft">
                  {selected.authors?.slice(0, 6).map((author) => <span key={author}>{author}</span>)}
                  {(selected.year ?? 0) > 0 && <span>{selected.year}</span>}
                  {selected.venue && <span>{selected.venue}</span>}
                  {selected.arxiv_id && <span>arXiv:{selected.arxiv_id}</span>}
                  {selected.doi && <span>DOI:{selected.doi}</span>}
                  <span className="border border-line-strong px-1.5">revision {selected.revision}</span>
                  <span className={selected.reproduction?.status === 'failed' ? 'border border-red-300 px-1.5 text-red-600' : 'border border-line-strong px-1.5'}>{selected.reproduction?.status || 'none'}</span>
                </div>
              </header>
              {selected.tldr && (
                <div className="border border-line bg-surface px-3 py-2">
                  <div className="text-[11px] font-semibold text-ink-soft">TLDR {selected.tldr_inferred && <span className="font-normal">（推断）</span>}</div>
                  <p className="mt-1 text-[12px] text-ink">{selected.tldr}</p>
                </div>
              )}
              {selected.sections && selected.sections.length > 0 && (
                <div>
                  <div className="mb-1 text-[11px] font-semibold text-ink-soft">章节地图（{selected.sections.length}）</div>
                  <div className="flex flex-wrap gap-1">
                    {selected.sections.map((section) => (
                      <span key={section.title} className="border border-line px-1.5 py-0.5 text-[11px] text-ink-soft" title={`p.${section.start_page}-${section.end_page}`}>{section.title}</span>
                    ))}
                  </div>
                </div>
              )}
              <div>
                <div className="mb-1 flex items-center gap-2 text-[11px] font-semibold text-ink-soft"><FileText className="h-3.5 w-3.5" />Point</div>
                {points.length === 0 ? <div className="text-[12px] text-ink-soft">暂无 Point。解析论文或添加用户笔记后会出现在这里。</div> : (
                  <div className="grid gap-3">
                    {POINT_GROUPS.filter((group) => (grouped.get(group.kind) ?? []).length > 0).map((group) => (
                      <div key={group.kind}>
                        <div className="mb-1 text-[11px] font-semibold text-ink">{group.label}（{(grouped.get(group.kind) ?? []).length}）</div>
                        <div className="grid gap-1">
                          {(grouped.get(group.kind) ?? []).map((point) => (
                            <div key={point.record.id} className="border border-line px-2 py-1.5">
                              <div className="flex items-start justify-between gap-2">
                                <div className="min-w-0">
                                  <div className="text-[12px] font-medium text-ink">{point.record.title}</div>
                                  <div className="mt-0.5 whitespace-pre-wrap text-[12px] text-ink-soft">{point.record.content}</div>
                                  <div className="mt-1 flex flex-wrap items-center gap-2 text-[10px] text-ink-soft">
                                    <span>{trustLabel[point.record.trust] ?? point.record.trust}</span>
                                    <span>{statusLabel[point.record.status] ?? point.record.status}</span>
                                    {point.refs?.[0]?.page ? <span>p.{point.refs[0].page}</span> : null}
                                    {point.refs?.[0]?.section ? <span>{point.refs[0].section}</span> : null}
                                    {point.refs?.[0]?.quote ? <span className="truncate">“{point.refs[0].quote}”</span> : null}
                                  </div>
                                </div>
                                <div className="flex shrink-0 items-center gap-1">
                                  {point.record.status === 'pending_review' && (
                                    <button type="button" title="确认" onClick={() => void confirmPoint(point)} className="inline-flex h-6 w-6 items-center justify-center border border-line-strong text-ink-soft hover:text-ink"><Check className="h-3.5 w-3.5" /></button>
                                  )}
                                  <button type="button" title="删除" onClick={() => void removePoint(point)} className="inline-flex h-6 w-6 items-center justify-center border border-line-strong text-ink-soft hover:text-red-600"><X className="h-3.5 w-3.5" /></button>
                                </div>
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
                <div className="mt-2 flex gap-1">
                  <input value={note} onChange={(event) => setNote(event.target.value)} placeholder="添加用户笔记…" className="h-8 min-w-0 flex-1 border border-line bg-paper px-2 text-[12px]" />
                  <button type="button" onClick={() => void addNote()} className="inline-flex h-8 items-center gap-1 border border-ink bg-ink px-2 text-[12px] text-paper"><Plus className="h-3.5 w-3.5" />添加</button>
                </div>
              </div>
              <div>
                <div className="mb-1 flex items-center gap-2 text-[11px] font-semibold text-ink-soft"><Link2 className="h-3.5 w-3.5" />相关论文（{links.length}）</div>
                {links.length === 0 ? <div className="text-[12px] text-ink-soft">暂无跨论文关系。</div> : (
                  <div className="grid gap-1">
                    {links.map((link) => (
                      <div key={link.id} className="flex items-center justify-between border border-line px-2 py-1.5">
                        <div className="min-w-0">
                          <div className="text-[12px] text-ink"><span className="font-medium">{link.type}</span> → {link.to_paper === selected.paper_id ? link.from_paper : link.to_paper}</div>
                          <div className="text-[10px] text-ink-soft">{link.source} · confidence {link.confidence.toFixed(2)} · {statusLabel[link.status] ?? link.status} · r{link.revision}</div>
                        </div>
                        <div className="flex items-center gap-1">
                          {link.status === 'pending_review' && (
                            <>
                              <button type="button" title="确认" onClick={() => void confirmWikiLink(link.id).then(() => reloadDetail(selected.paper_id))} className="inline-flex h-6 w-6 items-center justify-center border border-line-strong text-ink-soft hover:text-ink"><Check className="h-3.5 w-3.5" /></button>
                              <button type="button" title="拒绝" onClick={() => void rejectWikiLink(link.id, 'user rejected').then(() => reloadDetail(selected.paper_id))} className="inline-flex h-6 w-6 items-center justify-center border border-line-strong text-ink-soft hover:text-red-600"><X className="h-3.5 w-3.5" /></button>
                            </>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="flex items-center gap-2 border-t border-line pt-2">
                <button type="button" onClick={() => void archiveWikiPaper(selected.paper_id).then(() => reloadList())} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2 text-[12px] text-ink-soft hover:text-ink">归档论文页</button>
              </div>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

export default PaperWikiPanel;
