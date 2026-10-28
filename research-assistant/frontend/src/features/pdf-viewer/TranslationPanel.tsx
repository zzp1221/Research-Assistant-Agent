import { useEffect, useMemo, useRef, useState } from 'react';
import { CheckCircle2, ChevronLeft, ChevronRight, Loader2, Square, XCircle } from 'lucide-react';
import type { TranslationJob } from '../../contracts/api';

export interface TranslationPanelProps {
  job: TranslationJob | null;
  isStarting: boolean;
  error: string;
  onStart: () => void;
  onCancel: () => void;
  onOpenPage: (page: number) => void;
  onClose: () => void;
  pageText: string;
  pageTextLoading: boolean;
  pageTextError: string;
}

const statusLabel = (status: string) => {
  switch (status) {
    case 'completed':
      return '已完成';
    case 'partial':
      return '部分完成';
    case 'running':
      return '翻译中';
    case 'failed':
      return '失败';
    case 'skipped':
      return '无文本';
    case 'canceled':
      return '已取消';
    case 'interrupted':
      return '已中断';
    default:
      return '等待中';
  }
};

const statusColor = (status: string) => {
  switch (status) {
    case 'completed':
      return 'border-ok/30 bg-ok-soft text-ok';
    case 'running':
      return 'border-accent/30 bg-accent-soft text-accent-ink';
    case 'failed':
      return 'border-flag/30 bg-flag-soft text-flag';
    case 'skipped':
    case 'canceled':
      return 'border-line bg-surface-muted text-faint';
    default:
      return 'border-line bg-surface text-muted';
  }
};

export function TranslationPanel(props: TranslationPanelProps) {
  const { job, isStarting, error, onStart, onCancel, onOpenPage, onClose, pageText, pageTextLoading, pageTextError } = props;
  const [selectedPage, setSelectedPage] = useState<number | null>(null);
  const progressRef = useRef<HTMLDivElement>(null);

  const currentPage = job?.current_page ?? 0;
  useEffect(() => {
    if (currentPage > 0 && progressRef.current) {
      progressRef.current.scrollIntoView({ block: 'nearest' });
    }
  }, [currentPage]);

  const percent = useMemo(() => {
    if (!job || job.total_pages === 0) return 0;
    return Math.round((job.completed_pages / job.total_pages) * 100);
  }, [job]);

  const active = job && (job.status === 'running' || job.status === 'queued');
  const finished = job && (job.status === 'completed' || job.status === 'partial');

  const handleOpen = (page: number) => {
    setSelectedPage(page);
    onOpenPage(page);
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-surface">
      <div className="flex h-9 shrink-0 items-center justify-between border-b border-line px-2.5">
        <div className="flex items-center gap-2">
          <span className="instrument-kicker">全文翻译</span>
          {job && <span className="instrument-num border border-line bg-surface-muted px-1.5 py-0.5 text-[11px] text-ink-soft">{job.completed_pages}/{job.total_pages} 页</span>}
        </div>
        <div className="flex items-center gap-1">
          {active ? (
            <button
              type="button"
              onClick={onCancel}
              className="flex h-6 items-center gap-1 rounded-[2px] border border-flag/30 bg-surface px-2 text-[10.5px] font-semibold text-flag transition-colors hover:bg-flag-soft"
            >
              <Square className="h-2.5 w-2.5 fill-current" />
              取消
            </button>
          ) : (
            <button
              type="button"
              onClick={onStart}
              disabled={isStarting}
              className="h-6 rounded-[2px] bg-accent px-2 text-[10.5px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-40"
            >
              {isStarting ? '启动中…' : finished ? '重新翻译' : '开始全文翻译'}
            </button>
          )}
          <button
            type="button"
            onClick={onClose}
            className="flex h-6 w-6 items-center justify-center rounded-[2px] text-faint transition-colors hover:bg-surface-muted hover:text-ink-soft"
            aria-label="关闭翻译面板"
          >
            <ChevronRight className="h-3.5 w-3.5 rotate-90" />
          </button>
        </div>
      </div>

      {error && <div className="border-b border-flag/25 bg-flag-soft px-3 py-2 text-[11px] text-flag">{error}</div>}

      {!job && !isStarting && (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 text-center">
          <p className="font-serif text-[13.5px] text-ink-soft">按页翻译整篇论文</p>
          <p className="max-w-[16rem] text-[11px] leading-5 text-muted">
            逐页缓存，可随时取消。翻译调用真实模型，页数较多时请留意耗时。
            <br />
            只想翻译某一句？直接在 PDF 中选中文字即可。
          </p>
        </div>
      )}

      {job && (
        <>
          <div className="shrink-0 px-3 py-2">
            <div className="h-1 w-full overflow-hidden rounded-full bg-paper-deep" ref={progressRef}>
              <div
                className={finished ? 'h-full bg-ok transition-[width] duration-300' : 'h-full bg-accent transition-[width] duration-300'}
                style={{ width: `${percent}%` }}
              />
            </div>
            <div className="mt-1.5 flex items-center justify-between text-[10px] text-muted">
              <span>
                {job.status === 'running' ? `正在翻译第 ${job.current_page || '-'} 页` : statusLabel(job.status)}
                {job.failed_pages > 0 ? ` · ${job.failed_pages} 页失败` : ''}
              </span>
              <span className="font-mono">{percent}%</span>
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-3">
            <div className="grid grid-cols-6 gap-1">
              {job.pages.map((page) => (
                <button
                  key={page.number}
                  type="button"
                  onClick={() => handleOpen(page.number)}
                  title={`${page.number} · ${statusLabel(page.status)}${page.error ? ` · ${page.error}` : ''}`}
                  className={`flex h-7 items-center justify-center rounded-[2px] border text-[10.5px] font-semibold transition-colors ${statusColor(page.status)} ${selectedPage === page.number ? 'ring-2 ring-accent/30' : ''}`}
                >
                  {page.status === 'running' ? <Loader2 className="h-3 w-3 animate-spin" /> : page.number}
                </button>
              ))}
            </div>
            {job.error && <p className="mt-2 text-[11px] text-flag">{job.error}</p>}
          </div>

          <div className="max-h-[45%] shrink-0 overflow-y-auto border-t border-line bg-surface-muted px-3 py-2">
            {selectedPage === null ? (
              <p className="text-[11px] text-faint">点击页码查看该页译文。</p>
            ) : pageTextLoading ? (
              <p className="flex items-center gap-1 text-[11px] text-muted"><Loader2 className="h-3 w-3 animate-spin" />正在读取译文…</p>
            ) : pageTextError ? (
              <p className="text-[11px] text-flag">{pageTextError}</p>
            ) : (
              <div>
                <div className="mb-1 flex items-center gap-1 text-[10px] font-semibold text-muted">
                  {pageText ? <CheckCircle2 className="h-3 w-3 text-ok" /> : <XCircle className="h-3 w-3 text-faint" />}
                  第 {selectedPage} 页译文
                </div>
                <p className="whitespace-pre-wrap font-serif text-[12px] leading-5 text-ink-soft">{pageText || '（暂无译文）'}</p>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

export function PageIndicator({ page, total }: { page: number; total: number }) {
  return (
    <span className="flex items-center gap-1 text-[10.5px] text-muted">
      <ChevronLeft className="h-3 w-3" />
      {page} / {total}
      <ChevronRight className="h-3 w-3" />
    </span>
  );
}