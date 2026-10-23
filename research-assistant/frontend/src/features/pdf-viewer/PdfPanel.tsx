import { useMemo, useState } from 'react';
import { FileUp, Languages, X } from 'lucide-react';
import { Viewer, Worker } from '@react-pdf-viewer/core';
import type { PdfJs } from '@react-pdf-viewer/core';
import type { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import type { Plugin } from '@react-pdf-viewer/core';
import type { DocumentInfoResponse, TranslationJob } from '../../contracts/api';
import { TranslationPanel } from './TranslationPanel';

interface PdfPanelProps {
  pdfUrl: string;
  documentInfo: DocumentInfoResponse | null;
  defaultLayoutPluginInstance: ReturnType<typeof defaultLayoutPlugin>;
  aiTranslationPluginInstance: Plugin;
  onPageChange?: (page: number) => void;
  translation: {
    job: TranslationJob | null;
    isStarting: boolean;
    error: string;
    pageText: string;
    pageTextLoading: boolean;
    pageTextError: string;
    onStart: () => void;
    onCancel: () => void;
    onOpenPage: (page: number) => void;
  };
}

// The worker, cMaps and standard fonts are shipped from /pdfjs so the viewer
// never depends on unpkg being reachable. This matters for offline container
// deployments and for CJK papers that need the character maps.
const PDF_WORKER_URL = '/pdfjs/pdf.worker.min.js';
const PDF_CMAP_URL = '/pdfjs/cmaps/';
const PDF_FONT_URL = '/pdfjs/standard_fonts/';

// pdfjs-dist 3.11 supports standardFontDataUrl at runtime but the bundled
// @react-pdf-viewer types only declare cMapUrl/cMapPacked.
const transformGetDocumentParams = (options: PdfJs.GetDocumentParams): PdfJs.GetDocumentParams => ({
  ...options,
  cMapUrl: PDF_CMAP_URL,
  cMapPacked: true,
  standardFontDataUrl: PDF_FONT_URL,
} as PdfJs.GetDocumentParams & { standardFontDataUrl: string });

export function PdfPanel(props: PdfPanelProps) {
  const {
    pdfUrl,
    documentInfo,
    defaultLayoutPluginInstance,
    aiTranslationPluginInstance,
    onPageChange,
    translation,
  } = props;
  const [showTranslation, setShowTranslation] = useState(false);
  const [currentPage, setCurrentPage] = useState(1);

  const pageCount = documentInfo?.document.page_count ?? 0;
  const documentStatus = documentInfo?.document.status ?? 'unknown';
  const statusHint = useMemo(() => {
    switch (documentStatus) {
      case 'extracted':
        return null;
      case 'empty':
        return '该 PDF 没有可提取的文本层（可能是扫描件），划词翻译和全文翻译不可用。';
      case 'encrypted':
        return '该 PDF 受密码保护，无法提取文本。';
      case 'unsupported':
        return '当前后端未安装 PDF 文本提取器，文档阅读可用，但划词/全文翻译不可用。';
      case 'failed':
        return documentInfo?.document.error || 'PDF 文本提取失败。';
      default:
        return null;
    }
  }, [documentInfo?.document.error, documentStatus]);

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-paper-deep/40">
      <div className="flex h-10 shrink-0 items-center justify-between border-b border-line bg-surface px-3 text-[11.5px] text-muted">
        <span className="flex min-w-0 items-center gap-1.5 font-medium">
          <FileUp className="h-3.5 w-3.5 text-accent" />
          <span className="truncate">论文阅读</span>
          {pageCount > 0 && <span className="rounded-[3px] border border-line bg-surface-muted px-1.5 py-0.5 text-[10px] text-muted">共 {pageCount} 页</span>}
          {pageCount > 0 && <span className="hidden text-[10px] text-faint sm:inline">第 {currentPage} 页</span>}
        </span>
        <div className="flex shrink-0 items-center gap-1.5">
          <button
            type="button"
            onClick={() => setShowTranslation((value) => !value)}
            disabled={documentStatus !== 'extracted' || pageCount === 0}
            title={documentStatus !== 'extracted' ? '该 PDF 没有可提取的文本层' : '按页翻译全文'}
            className={`flex h-7 items-center gap-1.5 rounded-[4px] border px-2 text-[11px] font-medium transition-colors ${
              showTranslation
                ? 'border-accent/35 bg-accent-soft text-accent-ink'
                : 'border-line bg-surface text-muted hover:border-line-strong hover:text-ink-soft disabled:cursor-not-allowed disabled:opacity-40'
            }`}
          >
            <Languages className="h-3 w-3" />
            {showTranslation ? '收起全文翻译' : '全文翻译'}
          </button>
        </div>
      </div>

      {statusHint && (
        <div className="flex shrink-0 items-start gap-2 border-b border-warn/25 bg-warn-soft px-3 py-2 text-[11px] leading-5 text-warn">
          <X className="mt-0.5 h-3 w-3 shrink-0" />
          {statusHint}
        </div>
      )}

      <div className="relative min-h-0 flex-1 overflow-hidden">
        <div className={`overflow-y-auto bg-paper-deep/50 ${showTranslation ? 'h-[55%]' : 'h-full'}`}>
          <Worker workerUrl={PDF_WORKER_URL}>
            <div style={{ height: '100%', width: '100%' }}>
              <Viewer
                fileUrl={pdfUrl}
                plugins={[defaultLayoutPluginInstance, aiTranslationPluginInstance]}
                transformGetDocumentParams={transformGetDocumentParams}
                onPageChange={(event) => {
                  const page = event.currentPage + 1;
                  setCurrentPage(page);
                  onPageChange?.(page);
                }}
              />
            </div>
          </Worker>
        </div>

        {showTranslation && (
          <div className="absolute bottom-0 left-0 right-0 top-[55%] border-t border-line bg-surface">
            <TranslationPanel
              job={translation.job}
              isStarting={translation.isStarting}
              error={translation.error}
              pageText={translation.pageText}
              pageTextLoading={translation.pageTextLoading}
              pageTextError={translation.pageTextError}
              onStart={translation.onStart}
              onCancel={translation.onCancel}
              onOpenPage={translation.onOpenPage}
              onClose={() => setShowTranslation(false)}
            />
          </div>
        )}
      </div>
    </div>
  );
}
