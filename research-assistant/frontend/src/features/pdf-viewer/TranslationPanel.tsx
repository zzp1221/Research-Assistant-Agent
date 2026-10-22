import { useEffect, useMemo, useRef, useState } from 'react';
import { CheckCircle2, ChevronLeft, ChevronRight, FileText, Loader2, Square, XCircle } from 'lucide-react';
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
      return 'border-emerald-200 bg-emerald-50 text-emerald-700';
    case 'running':
      return 'border-blue-200 bg-blue-50 text-blue-700';
    case 'failed':
      return 'border-rose-200 bg-rose-50 text-rose-700';
    case 'skipped':
    case 'canceled':
      return 'border-slate-200 bg-slate-50 text-slate-500';
    default:
      return 'border-slate-200 bg-white text-slate-500';
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
    <div className="flex h-full min-h-0 flex-col border-t border-slate-200 bg-slate-50">
      <div className="flex shrink-0 items-center justify-between border-b border-slate-200 bg-white px-3 py-2">
        <div className="flex items-center gap-2 text-xs font-semibold text-slate-700">
          <FileText className="h-3.5 w-3.5 text-blue-600" />
          全文翻译
          {job && <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-500">{job.completed_pages}/{job.total_pages} 页</span>}
        </div>
        <div className="flex items-center gap-1.5">
          {active ? (
            <button
              type="button"
              onClick={onCancel}
              className="flex items-center gap-1 rounded border border-rose-200 bg-white px-2 py-1 text-[11px] font-semibold text-rose-700 hover:bg-rose-50"
            >
              <Square className="h-3 w-3 fill-current" />
              取消
            </button>
          ) : (
            <button
              type="button"
              onClick={onStart}
              disabled={isStarting}
              className="rounded bg-blue-600 px-2 py-1 text-[11px] font-semibold text-white hover:bg-blue-700 disabled:opacity-50"
            >
              {isStarting ? '启动中...' : finished ? '重新翻译' : '开始全文翻译'}
            </button>
          )}
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 text-slate-400 hover:bg-slate-100 hover:text-slate-700"
            aria-label="关闭翻译面板"
          >
            <ChevronRight className="h-4 w-4 rotate-90" />
          </button>
        </div>
      </div>

      {error && <div className="border-b border-rose-200 bg-rose-50 px-3 py-2 text-[11px] text-rose-700">{error}</div>}

      {!job && !isStarting && (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-4 text-center text-xs text-slate-500">
          <p>按页翻译整篇论文，逐页缓存，可随时取消。</p>
          <p className="text-[11px] text-slate-400">翻译调用真实模型，页数较多时请留意耗时。</p>
        </div>
      )}

      {job && (
        <>
          <div className="shrink-0 px-3 py-2">
            <div className="h-2 w-full overflow-hidden rounded-full bg-slate-200" ref={progressRef}>
              <div
                className={finished ? 'h-full bg-emerald-500 transition-all' : 'h-full bg-blue-500 transition-all'}
                style={{ width: `${percent}%` }}
              />
            </div>
            <div className="mt-1 flex items-center justify-between text-[10px] text-slate-500">
              <span>
                {job.status === 'running' ? `正在翻译第 ${job.current_page || '-'} 页` : statusLabel(job.status)}
                {job.failed_pages > 0 ? ` · ${job.failed_pages} 页失败` : ''}
              </span>
              <span>{percent}%</span>
            </div>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-3">
            <div className="grid grid-cols-6 gap-1.5">
              {job.pages.map((page) => (
                <button
                  key={page.number}
                  type="button"
                  onClick={() => handleOpen(page.number)}
                  title={`${page.number} · ${statusLabel(page.status)}${page.error ? ` · ${page.error}` : ''}`}
                  className={`flex h-8 items-center justify-center rounded border text-[11px] font-semibold transition-colors ${statusColor(page.status)} ${selectedPage === page.number ? 'ring-2 ring-blue-300' : ''}`}
                >
                  {page.status === 'running' ? <Loader2 className="h-3 w-3 animate-spin" /> : page.number}
                </button>
              ))}
            </div>
            {job.error && <p className="mt-2 text-[11px] text-rose-600">{job.error}</p>}
          </div>

          <div className="max-h-[45%] shrink-0 overflow-y-auto border-t border-slate-200 bg-white px-3 py-2">
            {selectedPage === null ? (
              <p className="text-[11px] text-slate-400">点击页码查看该页译文。</p>
            ) : pageTextLoading ? (
              <p className="flex items-center gap-1 text-[11px] text-slate-500"><Loader2 className="h-3 w-3 animate-spin" />正在读取译文...</p>
            ) : pageTextError ? (
              <p className="text-[11px] text-rose-600">{pageTextError}</p>
            ) : (
              <div>
                <div className="mb-1 flex items-center gap-1 text-[10px] font-semibold text-slate-500">
                  {pageText ? <CheckCircle2 className="h-3 w-3 text-emerald-500" /> : <XCircle className="h-3 w-3 text-slate-400" />}
                  第 {selectedPage} 页译文
                </div>
                <p className="whitespace-pre-wrap text-[12px] leading-5 text-slate-700">{pageText || '（暂无译文）'}</p>
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
    <span className="flex items-center gap-1 text-[11px] text-slate-500">
      <ChevronLeft className="h-3 w-3" />
      {page} / {total}
      <ChevronRight className="h-3 w-3" />
    </span>
  );
}
