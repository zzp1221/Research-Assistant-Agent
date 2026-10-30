import { useCallback, useMemo, useState } from 'react';
import {
  AlertCircle,
  ArrowRight,
  BookOpen,
  Check,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  FileText,
  Globe,
  HelpCircle,
  Info,
  Library,
  Loader2,
  Plus,
  Quote,
  Search,
  Sparkles,
  Tag,
  Trash2,
  X,
} from 'lucide-react';
import type {
  CitationGraph,
  LiteratureRecord,
  LiteratureResult,
  LiteratureWarning,
  UploadedFile,
  WebFetchResult,
} from '../../contracts/api';
import { getUpload } from '../../services/api/researchAssistantApi';
import {
  attachLiteratureRecord,
  deleteLiteratureRecord,
  detachLiteratureRecord,
  getLiteratureCitations,
  importLiterature,
  listLiteratureRecords,
  literatureBibTeXUrl,
  searchLiterature,
  fetchWebPage,
} from '../../services/api/literatureApi';

type LiteratureTab = 'search' | 'library' | 'citations' | 'web';
type NoticeTone = 'info' | 'success' | 'warn';

interface Notice {
  tone: NoticeTone;
  text: string;
}

export interface LiteraturePanelProps {
  activeSessionId?: string | null;
  onAttachToSession?: (upload: UploadedFile, record: LiteratureRecord) => 'added' | 'duplicate' | 'full';
  onDetachFromSession?: (uploadId: string) => void;
  onOpenPdf?: (upload: UploadedFile) => void;
  onOpenHelp?: () => void;
  onGoToChat?: () => void;
}

const SOURCE_OPTIONS = [
  { value: 'arxiv', label: 'arXiv' },
  { value: 'openalex', label: 'OpenAlex' },
  { value: 'crossref', label: 'Crossref' },
  { value: 'github', label: 'GitHub' },
  { value: 'semanticscholar', label: 'S2' },
  { value: 'pubmed', label: 'PubMed' },
  { value: 'dblp', label: 'DBLP' },
];

const tabLabels: Record<LiteratureTab, string> = {
  search: '检索',
  library: '我的文献库',
  citations: '引用网络',
  web: '网页阅读',
};

function resultReference(result: LiteratureResult): string {
  if (result.doi) return result.doi;
  if (result.arxiv_id) return `arxiv:${result.arxiv_id}`;
  if (result.pdf_url) return result.pdf_url;
  return '';
}

function recordReference(record: LiteratureRecord): string {
  if (record.doi) return record.doi;
  if (record.arxiv_id) return `arxiv:${record.arxiv_id}`;
  if (record.pdf_url) return record.pdf_url;
  if (record.url) return record.url;
  return '';
}

function sourceLabel(source: string): string {
  return SOURCE_OPTIONS.find((option) => option.value === source)?.label ?? source;
}

function NoticeBanner({ notice, onClose }: { notice: Notice; onClose: () => void }) {
  const toneClass =
    notice.tone === 'success'
      ? 'border-ok/25 bg-ok-soft text-ok'
      : notice.tone === 'warn'
        ? 'border-warn/25 bg-warn-soft text-warn'
        : 'border-line bg-surface-muted text-ink-soft';
  const Icon = notice.tone === 'success' ? Check : notice.tone === 'warn' ? AlertCircle : Info;
  return (
    <div className={`flex shrink-0 items-start gap-2 border-b px-3 py-2 text-[11px] leading-4 ${toneClass}`} role="status">
      <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0 flex-1">{notice.text}</span>
      <button type="button" onClick={onClose} className="shrink-0 opacity-70 hover:opacity-100" aria-label="关闭提示">
        <X className="h-3 w-3" />
      </button>
    </div>
  );
}

function ResultCard(props: {
  result: LiteratureResult;
  importing: boolean;
  imported?: { upload?: UploadedFile; record?: LiteratureRecord };
  onImport: (result: LiteratureResult, metadataOnly: boolean) => void;
  onOpenPdf?: (upload: UploadedFile) => void;
}) {
  const { result, importing, imported, onImport, onOpenPdf } = props;
  const [expanded, setExpanded] = useState(false);
  return (
    <article className="border border-line bg-surface px-3 py-2.5 transition-colors hover:border-line-strong">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5 text-[9.5px] text-muted">
            <span className="journal-caps border border-line px-1 py-0.5">{sourceLabel(result.source)}</span>
            {result.year ? <span className="journal-num">{result.year}</span> : null}
            {result.venue ? <span className="max-w-[11rem] truncate">{result.venue}</span> : null}
            {typeof result.citations === 'number' && result.citations > 0 ? (
              <span className="journal-num">被引 {result.citations.toLocaleString('zh-CN')}</span>
            ) : null}
            {result.open_access ? <span className="border border-ok/40 bg-ok-soft px-1 text-[9.5px] text-ok">OA</span> : null}
          </div>
          <h3 className="mt-1 min-w-0 text-[13px] font-semibold leading-5 text-ink">{result.title || '未命名文献'}</h3>
          {result.authors?.length ? (
            <p className="mt-0.5 truncate text-[10.5px] text-muted">{result.authors.join(', ')}</p>
          ) : null}
        </div>
      </div>
      {result.abstract ? (
        <p className={`mt-1.5 text-[12px] leading-[1.65] text-ink-soft ${expanded ? '' : 'line-clamp-2'}`}>{result.abstract}</p>
      ) : null}
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        {imported?.upload ? (
          <>
            <span className="inline-flex items-center gap-1 border border-ok/35 bg-ok-soft px-1.5 py-0.5 text-[10px] font-medium text-ok">
              <Check className="h-3 w-3" /> 已加入当前会话
            </span>
            {onOpenPdf ? (
              <button
                type="button"
                onClick={() => onOpenPdf(imported.upload as UploadedFile)}
                className="inline-flex h-7 items-center gap-1 rounded-lg border border-line bg-surface px-2 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
              >
                <FileText className="h-3 w-3" /> 打开 PDF
              </button>
            ) : null}
          </>
        ) : (
          <>
            <button
              type="button"
              disabled={importing || !resultReference(result)}
              onClick={() => onImport(result, false)}
              className="inline-flex h-7 items-center gap-1 rounded-lg bg-accent px-2.5 text-[10px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:cursor-not-allowed disabled:opacity-40"
              title={!resultReference(result) ? '该结果没有可导入的 DOI / arXiv / PDF 链接' : '下载 PDF、存入文献库并加入当前会话'}
            >
              {importing ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}
              导入并加入会话
            </button>
            <button
              type="button"
              disabled={importing}
              onClick={() => onImport(result, true)}
              className="h-6 border border-line bg-surface px-2 text-[10.5px] text-muted transition-colors hover:border-line-strong hover:text-ink disabled:opacity-40"
              title="只保存题录信息，不下载 PDF；之后可在文献库补充 PDF"
            >
              仅保存元数据
            </button>
          </>
        )}
        {result.abstract ? (
          <button
            type="button"
            onClick={() => setExpanded((value) => !value)}
            className="inline-flex items-center gap-0.5 text-[10.5px] text-muted hover:text-ink"
          >
            {expanded ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
            {expanded ? '收起' : '摘要'}
          </button>
        ) : null}
        {result.url ? (
          <a
            href={result.url}
            target="_blank"
            rel="noreferrer"
            className="ml-auto inline-flex items-center gap-0.5 text-[10.5px] text-muted hover:text-ink"
          >
            <ExternalLink className="h-3 w-3" /> 原文
          </a>
        ) : null}
      </div>
    </article>
  );
}

function LibraryCard(props: {
  record: LiteratureRecord;
  attached: boolean;
  busy: boolean;
  onAttach: (record: LiteratureRecord) => void;
  onDetach: (record: LiteratureRecord) => void;
  onOpenPdf: (record: LiteratureRecord) => void;
  onImportPdf: (record: LiteratureRecord) => void;
  onDelete: (record: LiteratureRecord) => void;
}) {
  const { record, attached, busy, onAttach, onDetach, onOpenPdf, onImportPdf, onDelete } = props;
  const [expanded, setExpanded] = useState(false);
  const hasPdf = Boolean(record.upload_id);
  return (
    <article className="border border-line bg-surface px-3 py-2.5">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5 text-[9.5px] text-muted">
            <span className={`border px-1 py-0.5 ${hasPdf ? 'border-ok/40 bg-ok-soft text-ok' : 'border-line text-muted'}`}>
              {hasPdf ? 'PDF 可读' : '仅元数据'}
            </span>
            <span className="journal-caps">{sourceLabel(record.source)}</span>
            {record.year ? <span className="journal-num">{record.year}</span> : null}
            {record.venue ? <span className="max-w-[10rem] truncate">{record.venue}</span> : null}
            {attached ? (
              <span className="inline-flex items-center gap-0.5 text-ok"><Check className="h-2.5 w-2.5" /> 已加入会话</span>
            ) : null}
          </div>
          <h3 className="mt-1 text-[13px] font-semibold leading-5 text-ink">{record.title}</h3>
          {record.authors?.length ? <p className="mt-0.5 truncate text-[10.5px] text-muted">{record.authors.slice(0, 5).join(', ')}</p> : null}
        </div>
        <button
          type="button"
          onClick={() => setExpanded((value) => !value)}
          className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center text-muted hover:bg-surface-muted hover:text-ink"
          aria-label={expanded ? '收起文献详情' : '展开文献详情'}
        >
          {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
        </button>
      </div>

      {record.tags?.length ? (
        <div className="mt-1.5 flex flex-wrap gap-1">
          {record.tags.map((tag) => (
            <span key={tag} className="inline-flex items-center gap-0.5 border border-line px-1 text-[9.5px] text-muted">
              <Tag className="h-2.5 w-2.5" />{tag}
            </span>
          ))}
        </div>
      ) : null}

      {expanded ? (
        <div className="mt-2 border-t border-line pt-2">
          {record.abstract ? <p className="text-[12px] leading-[1.7] text-ink-soft">{record.abstract}</p> : <p className="text-[12px] text-faint">这条记录没有摘要。</p>}
          <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-[10px] text-muted">
            {record.doi ? <span className="journal-num">DOI {record.doi}</span> : null}
            {record.arxiv_id ? <span className="journal-num">arXiv:{record.arxiv_id}</span> : null}
            {typeof record.citations === 'number' && record.citations > 0 ? <span className="journal-num">被引 {record.citations.toLocaleString('zh-CN')}</span> : null}
          </div>
        </div>
      ) : null}

      <div className="mt-2 flex flex-wrap items-center gap-1.5 border-t border-line/70 pt-2">
        {attached ? (
          <button
            type="button"
            disabled={busy}
            onClick={() => onDetach(record)}
            className="h-6 border border-line bg-surface px-2 text-[10.5px] text-muted hover:border-warn hover:text-warn disabled:opacity-40"
          >
            从会话移出
          </button>
        ) : (
          <button
            type="button"
            disabled={busy || !hasPdf}
            onClick={() => onAttach(record)}
            title={hasPdf ? '把 PDF 加入当前会话的附件栏' : '该条目只有元数据，请先导入 PDF'}
            className="inline-flex h-7 items-center gap-1 rounded-lg bg-accent px-2.5 text-[10px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:cursor-not-allowed disabled:bg-surface-strong disabled:text-muted"
          >
            {busy ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}
            加入会话
          </button>
        )}
        {hasPdf ? (
          <button
            type="button"
            onClick={() => onOpenPdf(record)}
            className="inline-flex h-7 items-center gap-1 rounded-lg border border-line bg-surface px-2 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
          >
            <FileText className="h-3 w-3" /> 打开 PDF
          </button>
        ) : (
          <button
            type="button"
            disabled={busy}
            onClick={() => onImportPdf(record)}
            className="inline-flex h-7 items-center gap-1 rounded-lg border border-line bg-surface px-2 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-40"
          >
            <DownloadIcon /> 导入 PDF
          </button>
        )}
        <a href={literatureBibTeXUrl(record.id)} download className="text-[10.5px] text-muted underline-offset-2 hover:text-ink hover:underline">
          BibTeX
        </a>
        <button
          type="button"
          onClick={() => onDelete(record)}
          className="ml-auto inline-flex items-center gap-0.5 text-[10.5px] text-muted hover:text-warn"
        >
          <Trash2 className="h-3 w-3" /> 删除
        </button>
      </div>
    </article>
  );
}

function DownloadIcon() {
  return <Plus className="h-3 w-3" />;
}

export function LiteraturePanel(props: LiteraturePanelProps = {}) {
  const { activeSessionId, onAttachToSession, onDetachFromSession, onOpenPdf, onOpenHelp, onGoToChat } = props;
  const [tab, setTab] = useState<LiteratureTab>('search');
  const [query, setQuery] = useState('');
  const [sources, setSources] = useState<string[]>(['arxiv', 'openalex', 'crossref']);
  const [yearFrom, setYearFrom] = useState('');
  const [sort, setSort] = useState<'relevance' | 'citations' | 'year'>('relevance');
  const [results, setResults] = useState<LiteratureResult[]>([]);
  const [warnings, setWarnings] = useState<LiteratureWarning[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [busyKey, setBusyKey] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const [imported, setImported] = useState<Record<string, { upload?: UploadedFile; record?: LiteratureRecord }>>({});

  const [records, setRecords] = useState<LiteratureRecord[]>([]);
  const [libraryQuery, setLibraryQuery] = useState('');
  const [tagFilter, setTagFilter] = useState('');
  const [libraryLoading, setLibraryLoading] = useState(false);
  const [attachedNow, setAttachedNow] = useState<string[]>([]);

  const [doi, setDoi] = useState('');
  const [graph, setGraph] = useState<CitationGraph | null>(null);
  const [citationLoading, setCitationLoading] = useState(false);

  const [webUrl, setWebUrl] = useState('');
  const [webResult, setWebResult] = useState<WebFetchResult | null>(null);
  const [webLoading, setWebLoading] = useState(false);

  const runSearch = useCallback(async () => {
    const trimmed = query.trim();
    if (!trimmed) {
      setError('请输入检索关键词');
      return;
    }
    setLoading(true);
    setError('');
    setNotice(null);
    try {
      const response = await searchLiterature({
        query: trimmed,
        sources,
        yearFrom: Number(yearFrom) || undefined,
        sort,
        limit: 20,
      });
      setResults(response.results ?? []);
      setWarnings(response.warnings ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : '检索失败');
    } finally {
      setLoading(false);
    }
  }, [query, sources, yearFrom, sort]);

  const loadLibrary = useCallback(async () => {
    setLibraryLoading(true);
    setError('');
    try {
      const response = await listLiteratureRecords({ query: libraryQuery, tag: tagFilter, limit: 100 });
      setRecords(response.records ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : '文献库加载失败');
    } finally {
      setLibraryLoading(false);
    }
  }, [libraryQuery, tagFilter]);

  const selectTab = useCallback((value: LiteratureTab) => {
    setTab(value);
    if (value === 'library') {
      void loadLibrary();
    }
  }, [loadLibrary]);

  const attachUpload = useCallback(
    (upload: UploadedFile, record: LiteratureRecord): boolean => {
      if (!onAttachToSession) return false;
      const result = onAttachToSession(upload, record);
      if (result === 'duplicate') {
        setNotice({ tone: 'info', text: `《${record.title}》已经在当前会话的附件栏里。` });
        return true;
      }
      if (result === 'full') {
        setNotice({ tone: 'warn', text: '当前会话最多添加 8 个附件，请先移除一个再试。' });
        return false;
      }
      setAttachedNow((current) => (current.includes(record.id) ? current : [...current, record.id]));
      setNotice({ tone: 'success', text: `已加入当前会话：${record.title}。附件栏在输入框上方，随下一条消息一起提交。` });
      return true;
    },
    [onAttachToSession],
  );

  const handleImport = useCallback(
    async (result: LiteratureResult, metadataOnly: boolean) => {
      const ref = resultReference(result);
      if (!ref) {
        setNotice({ tone: 'warn', text: '该结果没有可导入的 DOI、arXiv ID 或 PDF 链接。' });
        return;
      }
      const key = `${result.source}:${result.id || result.title}`;
      setBusyKey(key);
      setNotice(null);
      try {
        const response = await importLiterature({ ref, attach: !metadataOnly, metadataOnly });
        const record = response.record;
        if (metadataOnly) {
          setImported((current) => ({ ...current, [key]: { record } }));
          setNotice({ tone: 'success', text: `已保存到文献库（仅元数据）：${response.metadata?.title ?? result.title}` });
        } else if (response.upload && record) {
          setImported((current) => ({ ...current, [key]: { upload: response.upload, record } }));
          if (activeSessionId) {
            // The import endpoint uses the server cookie session; record the
            // local workspace session too so the library can show it later.
            try {
              await attachLiteratureRecord(record.id, activeSessionId);
            } catch (attachError) {
              console.warn('[literature] failed to record local session attachment', attachError);
            }
          }
          attachUpload(response.upload, record);
        } else {
          setImported((current) => ({ ...current, [key]: { record } }));
          setNotice({ tone: 'info', text: '已保存到文献库，但没有可加入会话的 PDF。可稍后在文献库中点「导入 PDF」。' });
        }
      } catch (err) {
        setNotice({ tone: 'warn', text: err instanceof Error ? err.message : '导入失败，请稍后重试。' });
      } finally {
        setBusyKey('');
      }
    },
    [activeSessionId, attachUpload],
  );

  const handleAttachRecord = useCallback(
    async (record: LiteratureRecord) => {
      setBusyKey(`attach:${record.id}`);
      setNotice(null);
      try {
        if (!record.upload_id) {
          const ref = recordReference(record);
          if (!ref) {
            setNotice({ tone: 'warn', text: '这条记录没有 DOI / arXiv / PDF 链接，无法自动导入 PDF。请手动上传 PDF。' });
            return;
          }
          const response = await importLiterature({ ref, attach: true });
          if (!response.upload) {
            setNotice({ tone: 'warn', text: '该文献只有元数据，暂时拿不到 PDF；可先查看原文或手动上传 PDF。' });
            return;
          }
          await attachLiteratureRecord(record.id, activeSessionId ?? undefined);
          attachUpload(response.upload, record);
          void loadLibrary();
          return;
        }
        const upload = await getUpload(record.upload_id);
        await attachLiteratureRecord(record.id, activeSessionId ?? undefined);
        attachUpload(upload, record);
      } catch (err) {
        setNotice({ tone: 'warn', text: err instanceof Error ? err.message : '加入会话失败，请稍后重试。' });
      } finally {
        setBusyKey('');
      }
    },
    [activeSessionId, attachUpload, loadLibrary],
  );

  const handleDetachRecord = useCallback(
    async (record: LiteratureRecord) => {
      setBusyKey(`detach:${record.id}`);
      try {
        await detachLiteratureRecord(record.id, activeSessionId ?? undefined);
        if (record.upload_id) onDetachFromSession?.(record.upload_id);
        setAttachedNow((current) => current.filter((id) => id !== record.id));
        setNotice({ tone: 'info', text: `已从当前会话移出：${record.title}` });
      } catch (err) {
        setNotice({ tone: 'warn', text: err instanceof Error ? err.message : '移出会话失败，请稍后重试。' });
      } finally {
        setBusyKey('');
      }
    },
    [activeSessionId, onDetachFromSession],
  );

  const handleOpenRecord = useCallback(
    async (record: LiteratureRecord) => {
      if (!record.upload_id) {
        setNotice({ tone: 'warn', text: '这条记录只有元数据，先点「导入 PDF」后才能打开阅读器。' });
        return;
      }
      setBusyKey(`open:${record.id}`);
      try {
        const upload = await getUpload(record.upload_id);
        onOpenPdf?.(upload);
      } catch (err) {
        setNotice({ tone: 'warn', text: err instanceof Error ? err.message : '打开 PDF 失败。' });
      } finally {
        setBusyKey('');
      }
    },
    [onOpenPdf],
  );

  const handleImportPdf = useCallback(
    async (record: LiteratureRecord) => {
      const ref = recordReference(record);
      if (!ref) {
        setNotice({ tone: 'warn', text: '这条记录没有 DOI / arXiv / PDF 链接，请手动上传 PDF。' });
        return;
      }
      setBusyKey(`pdf:${record.id}`);
      setNotice(null);
      try {
        const response = await importLiterature({ ref, attach: false });
        if (response.upload) {
          setNotice({ tone: 'success', text: `PDF 已导入文献库：${record.title}。现在可以加入会话或打开阅读。` });
        } else {
          setNotice({ tone: 'warn', text: '没有拿到 PDF，可能只有受限全文；请手动上传 PDF。' });
        }
        void loadLibrary();
      } catch (err) {
        setNotice({ tone: 'warn', text: err instanceof Error ? err.message : '导入 PDF 失败。' });
      } finally {
        setBusyKey('');
      }
    },
    [loadLibrary],
  );

  const handleDelete = useCallback(async (record: LiteratureRecord) => {
    setBusyKey(`delete:${record.id}`);
    try {
      await deleteLiteratureRecord(record.id);
      setRecords((current) => current.filter((item) => item.id !== record.id));
      setAttachedNow((current) => current.filter((id) => id !== record.id));
      setNotice({ tone: 'info', text: `已从文献库删除：${record.title}` });
    } catch (err) {
      setNotice({ tone: 'warn', text: err instanceof Error ? err.message : '删除失败' });
    } finally {
      setBusyKey('');
    }
  }, []);

  const loadCitations = useCallback(async () => {
    const trimmed = doi.trim();
    if (!trimmed) {
      setError('请输入 DOI');
      return;
    }
    setCitationLoading(true);
    setError('');
    try {
      setGraph(await getLiteratureCitations(trimmed));
    } catch (err) {
      setError(err instanceof Error ? err.message : '引用查询失败');
      setGraph(null);
    } finally {
      setCitationLoading(false);
    }
  }, [doi]);

  const loadWebPage = useCallback(async () => {
    const trimmed = webUrl.trim();
    if (!trimmed) {
      setError('请输入网页 URL');
      return;
    }
    setWebLoading(true);
    setError('');
    try {
      setWebResult(await fetchWebPage(trimmed));
    } catch (err) {
      setError(err instanceof Error ? err.message : '网页读取失败');
      setWebResult(null);
    } finally {
      setWebLoading(false);
    }
  }, [webUrl]);

  const toggleSource = (source: string) => {
    setSources((current) =>
      current.includes(source) ? current.filter((item) => item !== source) : [...current, source],
    );
  };

  const attachedRecords = useMemo(
    () =>
      records.filter(
        (record) =>
          attachedNow.includes(record.id) ||
          Boolean(activeSessionId) && Boolean(record.attached_sessions?.includes(activeSessionId as string)),
      ),
    [records, attachedNow, activeSessionId],
  );

  const isAttached = useCallback(
    (record: LiteratureRecord) =>
      attachedNow.includes(record.id) ||
      Boolean(activeSessionId) && Boolean(record.attached_sessions?.includes(activeSessionId as string)),
    [attachedNow, activeSessionId],
  );

  return (
    <div className="flex h-full min-h-0 flex-col bg-surface">
      <div className="shrink-0 border-b border-line bg-surface px-4 pb-0 pt-4">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="text-[16px] font-semibold leading-6 tracking-[-0.02em] text-ink">文献库</h2>
            <p className="mt-0.5 text-[10.5px] leading-4 text-muted">检索真实文献 · 管理收藏 · 加入当前会话</p>
          </div>
          <button
            type="button"
            onClick={onOpenHelp}
            className="inline-flex h-7 shrink-0 items-center gap-1 rounded-lg border border-line bg-surface px-2 text-[10px] text-muted transition-colors hover:border-accent hover:text-accent"
          >
            <HelpCircle className="h-3 w-3" /> 使用手册
          </button>
        </div>
        <div role="tablist" aria-label="文献库分区" className="mt-3 flex gap-0.5">
          {(Object.keys(tabLabels) as LiteratureTab[]).map((value) => (
            <button
              key={value}
              type="button"
              role="tab"
              aria-selected={tab === value}
              onClick={() => selectTab(value)}
              className={`h-8 border-b-2 px-2 text-[10.5px] transition-colors ${
                tab === value
                  ? 'border-ink font-semibold text-ink'
                  : 'border-transparent text-muted hover:text-ink-soft'
              }`}
            >
              {tabLabels[value]}
            </button>
          ))}
        </div>
      </div>

      {error ? <div className="shrink-0 border-b border-warn/25 bg-warn-soft px-3 py-1.5 text-[11px] text-warn">{error}</div> : null}
      {notice ? <NoticeBanner notice={notice} onClose={() => setNotice(null)} /> : null}

      <div className="min-h-0 flex-1 overflow-y-auto">
        {tab === 'search' ? (
          <div className="flex flex-col gap-3 px-3 py-3">
            <div className="flex gap-1.5">
              <div className="relative min-w-0 flex-1">
                <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
                <input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') void runSearch();
                  }}
                  placeholder="关键词，如 graph neural network"
                  aria-label="文献检索关键词"
                  className="h-9 w-full rounded-xl border border-line bg-surface pl-7 pr-2 text-[11.5px] text-ink outline-none transition-colors focus:border-accent"
                />
              </div>
              <button
                type="button"
                onClick={() => void runSearch()}
                disabled={loading}
                aria-label="执行检索"
                className="h-9 shrink-0 rounded-xl bg-accent px-3 text-[10.5px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-50"
              >
                {loading ? '检索中…' : '检索'}
              </button>
            </div>
            <div className="flex flex-wrap items-center gap-1.5 text-[10.5px]">
              {SOURCE_OPTIONS.map((option) => (
                <button
                  key={option.value}
                  type="button"
                  onClick={() => toggleSource(option.value)}
                  aria-pressed={sources.includes(option.value)}
                  className={`rounded-full border px-2 py-0.5 transition-colors ${
                    sources.includes(option.value)
                      ? 'border-accent/30 bg-accent-soft text-accent'
                      : 'border-line text-muted hover:border-line-strong'
                  }`}
                >
                  {option.label}
                </button>
              ))}
              <input
                value={yearFrom}
                onChange={(event) => setYearFrom(event.target.value.replace(/[^0-9]/g, ''))}
                placeholder="起始年"
                aria-label="起始年份"
                className="h-7 w-14 rounded-lg border border-line bg-surface px-1.5 text-[10px] text-ink outline-none transition-colors focus:border-accent"
              />
              <select
                value={sort}
                onChange={(event) => setSort(event.target.value as 'relevance' | 'citations' | 'year')}
                aria-label="排序方式"
                className="h-7 rounded-lg border border-line bg-surface px-1.5 text-[10px] text-ink outline-none transition-colors focus:border-accent"
              >
                <option value="relevance">相关度</option>
                <option value="citations">引用数</option>
                <option value="year">年份</option>
              </select>
            </div>
            {warnings.length > 0 ? (
              <div className="border border-warn/25 bg-warn-soft px-2 py-1 text-[10.5px] leading-4 text-warn">
                {warnings.map((warning) => (
                  <div key={warning.source}>{sourceLabel(warning.source)}: {warning.message}</div>
                ))}
              </div>
            ) : null}
            <div className="flex flex-col gap-2">
              {results.map((result) => {
                const key = `${result.source}:${result.id || result.title}`;
                return (
                  <ResultCard
                    key={key}
                    result={result}
                    importing={busyKey === key}
                    imported={imported[key]}
                    onImport={(item, metadataOnly) => void handleImport(item, metadataOnly)}
                    onOpenPdf={onOpenPdf}
                  />
                );
              })}
              {!loading && results.length === 0 ? (
                <div className="border border-line bg-surface-muted px-3 py-3">
                  <div className="flex items-center gap-2">
                    <Sparkles className="h-3.5 w-3.5 text-accent-ink" />
                    <p className="text-[11.5px] font-semibold text-ink">从一次真实检索开始</p>
                  </div>
                  <p className="mt-1 text-[12.5px] leading-5 text-muted">
                    输入关键词，或试试下面的例子。检索结果不会自动进入会话，需要先导入 PDF 再点「加入会话」。
                  </p>
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {['graph neural network', 'retrieval augmented generation', 'attention is all you need'].map((example) => (
                      <button
                        key={example}
                        type="button"
                        onClick={() => setQuery(example)}
                        className="rounded-full border border-line bg-surface px-2 py-0.5 text-[9.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
                      >
                        {example}
                      </button>
                    ))}
                  </div>
                  <button type="button" onClick={onOpenHelp} className="mt-2 inline-flex items-center gap-1 text-[10.5px] text-muted underline-offset-2 hover:text-ink hover:underline">
                    <BookOpen className="h-3 w-3" /> 查看完整使用手册 <ArrowRight className="h-3 w-3" />
                  </button>
                </div>
              ) : null}
            </div>
          </div>
        ) : null}

        {tab === 'library' ? (
          <div className="flex flex-col gap-3 px-3 py-3">
            <section className="border border-line bg-surface-muted px-3 py-2.5" aria-label="当前会话文献">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="journal-caps text-[9.5px]">当前会话</div>
                  <p className="mt-0.5 text-[11.5px] font-medium text-ink">
                    {attachedRecords.length > 0 ? `${attachedRecords.length} 篇文献已加入附件栏` : '还没有文献加入当前会话'}
                  </p>
                </div>
                {onGoToChat ? (
                  <button type="button" onClick={onGoToChat} className="inline-flex shrink-0 items-center gap-1 text-[10.5px] text-muted hover:text-ink">
                    去对话 <ArrowRight className="h-3 w-3" />
                  </button>
                ) : null}
              </div>
              {attachedRecords.length > 0 ? (
                <ul className="mt-2 flex flex-col gap-1.5">
                  {attachedRecords.map((record) => (
                    <li key={record.id} className="flex items-center gap-2 border border-line bg-surface px-2 py-1.5">
                      <FileText className="h-3 w-3 shrink-0 text-ok" />
                      <span className="min-w-0 flex-1 truncate text-[10.5px] text-ink-soft">{record.title}</span>
                      {record.upload_id ? (
                        <button type="button" onClick={() => void handleOpenRecord(record)} className="shrink-0 text-[10px] text-muted hover:text-ink">
                          打开
                        </button>
                      ) : null}
                      <button type="button" onClick={() => handleDetachRecord(record)} className="shrink-0 text-[10px] text-muted hover:text-warn">
                        移出
                      </button>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="mt-1.5 text-[12px] leading-5 text-muted">
                  在下方文献条目点「加入会话」，PDF 会出现在输入框上方的附件栏。
                </p>
              )}
            </section>

            <div className="flex gap-1.5">
              <input
                value={libraryQuery}
                onChange={(event) => setLibraryQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void loadLibrary();
                }}
                placeholder="搜索我的文献库"
                aria-label="搜索文献库"
                className="h-8 min-w-0 flex-1 rounded-lg border border-line bg-surface px-2 text-[11px] outline-none transition-colors focus:border-accent"
              />
              <input
                value={tagFilter}
                onChange={(event) => setTagFilter(event.target.value)}
                placeholder="标签"
                aria-label="按标签筛选"
                className="h-8 w-16 rounded-lg border border-line bg-surface px-2 text-[11px] outline-none transition-colors focus:border-accent"
              />
              <button
                type="button"
                onClick={() => void loadLibrary()}
                className="h-8 rounded-lg border border-line px-2 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
              >
                刷新
              </button>
            </div>

            {libraryLoading ? <p className="text-[11px] text-faint">加载中…</p> : null}

            <div className="flex flex-col gap-2">
              {records.map((record) => (
                <LibraryCard
                  key={record.id}
                  record={record}
                  attached={isAttached(record)}
                  busy={busyKey === `attach:${record.id}` || busyKey === `detach:${record.id}` || busyKey === `open:${record.id}` || busyKey === `pdf:${record.id}` || busyKey === `delete:${record.id}`}
                  onAttach={(item) => void handleAttachRecord(item)}
                  onDetach={(item) => void handleDetachRecord(item)}
                  onOpenPdf={(item) => void handleOpenRecord(item)}
                  onImportPdf={(item) => void handleImportPdf(item)}
                  onDelete={(item) => void handleDelete(item)}
                />
              ))}
            </div>

            {!libraryLoading && records.length === 0 ? (
              <div className="border border-line bg-surface-muted px-4 py-6 text-center">
                <Library className="mx-auto h-6 w-6 text-faint" />
                <h3 className="mt-2 text-[14px] font-semibold text-ink">文献库还是空的</h3>
                <p className="mx-auto mt-1 max-w-[18rem] text-[12.5px] leading-5 text-muted">
                  从检索开始：找到论文 → 导入 PDF → 加入当前会话。也可以直接把本地 PDF 拖进工作台。
                </p>
                <div className="mt-3 flex items-center justify-center gap-2">
                  <button type="button" onClick={() => selectTab('search')} className="h-8 rounded-lg bg-accent px-3 text-[10.5px] font-semibold text-white transition-colors hover:bg-accent-strong">
                    去检索文献
                  </button>
                  <button type="button" onClick={onOpenHelp} className="h-8 rounded-lg border border-line bg-surface px-3 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent">
                    看使用手册
                  </button>
                </div>
              </div>
            ) : null}
          </div>
        ) : null}

        {tab === 'citations' ? (
          <div className="flex flex-col gap-3 px-3 py-3">
            <div className="border border-line bg-surface-muted px-3 py-2 text-[12px] leading-5 text-muted">
              输入 DOI 查看这篇论文引用了谁（References），以及被哪些工作引用（Cited by）。数据来自 OpenAlex。
            </div>
            <div className="flex gap-1.5">
              <input
                value={doi}
                onChange={(event) => setDoi(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void loadCitations();
                }}
                placeholder="DOI，如 10.1234/example"
                aria-label="引用查询 DOI"
                className="h-8 min-w-0 flex-1 rounded-lg border border-line bg-surface px-2 text-[11px] outline-none transition-colors focus:border-accent"
              />
              <button
                type="button"
                onClick={() => void loadCitations()}
                disabled={citationLoading}
                className="h-8 shrink-0 rounded-lg border border-line px-3 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-50"
              >
                {citationLoading ? '查询中…' : '查询引用'}
              </button>
            </div>
            {graph ? (
              <div className="flex flex-col gap-3">
                <div className="border border-line bg-surface px-3 py-2">
                  <div className="flex items-center gap-2 text-[12px] text-ink">
                    <Quote className="h-3.5 w-3.5 text-muted" />
                    <span className="min-w-0 truncate">{graph.title || graph.doi}</span>
                  </div>
                  <div className="mt-1 text-[10.5px] text-muted">被引 {graph.cited_by_count} · 参考文献 {graph.references.length} · 引用它的工作 {graph.cited_by.length}</div>
                </div>
                <div>
                  <h4 className="journal-caps mb-1 text-[10px]">参考文献 · References</h4>
                  {graph.references.map((item) => (
                    <div key={`ref-${item.id}`} className="border-b border-line/60 py-1 text-[11px] text-ink-soft">
                      {item.title} {item.year ? <span className="journal-num text-muted">({item.year})</span> : null}
                    </div>
                  ))}
                  {graph.references.length === 0 ? <p className="text-[10.5px] text-faint">没有可用的参考文献数据。</p> : null}
                </div>
                <div>
                  <h4 className="journal-caps mb-1 text-[10px]">被引文献 · Cited by</h4>
                  {graph.cited_by.map((item) => (
                    <div key={`cited-${item.id}`} className="border-b border-line/60 py-1 text-[11px] text-ink-soft">
                      {item.title} {item.year ? <span className="journal-num text-muted">({item.year})</span> : null}
                    </div>
                  ))}
                  {graph.cited_by.length === 0 ? <p className="text-[10.5px] text-faint">没有可用的被引数据。</p> : null}
                </div>
              </div>
            ) : null}
          </div>
        ) : null}

        {tab === 'web' ? (
          <div className="flex flex-col gap-3 px-3 py-3">
            <div className="border border-line bg-surface-muted px-3 py-2 text-[12px] leading-5 text-muted">
              读取公开网页的正文，用于博客、项目主页或文档。只抓取静态 HTML，不执行脚本，也不绕过付费墙。
            </div>
            <div className="flex gap-1.5">
              <input
                value={webUrl}
                onChange={(event) => setWebUrl(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void loadWebPage();
                }}
                placeholder="https://example.com/article"
                aria-label="网页 URL"
                className="h-8 min-w-0 flex-1 rounded-lg border border-line bg-surface px-2 text-[11px] outline-none transition-colors focus:border-accent"
              />
              <button
                type="button"
                onClick={() => void loadWebPage()}
                disabled={webLoading}
                className="h-8 shrink-0 rounded-lg border border-line px-3 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-50"
              >
                {webLoading ? '读取中…' : '读取正文'}
              </button>
            </div>
            {webResult ? (
              <article className="border border-line bg-surface px-3 py-2">
                <div className="flex items-center gap-2">
                  <Globe className="h-3.5 w-3.5 text-muted" />
                  <h3 className="min-w-0 truncate text-[12.5px] font-medium text-ink">{webResult.title || webResult.url}</h3>
                </div>
                <p className="mt-1 text-[10.5px] text-muted">{webResult.word_count} 词 · {webResult.content_type}{webResult.cached ? ' · 缓存' : ''}</p>
                <p className="mt-2 max-h-72 overflow-y-auto whitespace-pre-wrap text-[11.5px] leading-5 text-ink-soft">{webResult.text}</p>
              </article>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}

export default LiteraturePanel;
