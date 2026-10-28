import { useMemo, useState } from 'react';
import { HelpCircle, Highlighter, Languages, X } from 'lucide-react';
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
  onOpenHelp?: () => void;
}

const READER_HINT_KEY = 'ra.pdf.readerHintSeen.v1';

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
    onOpenHelp,
  } = props;
  const [showTranslation, setShowTranslation] = useState(false);
  const [showStructure, setShowStructure] = useState(false);
  const [currentPage, setCurrentPage] = useState(1);
  const [showReaderHint, setShowReaderHint] = useState(() => {
    try {
      return window.localStorage.getItem(READER_HINT_KEY) !== '1';
    } catch {
      return true;
    }
  });

  const dismissReaderHint = () => {
    setShowReaderHint(false);
    try {
      window.localStorage.setItem(READER_HINT_KEY, '1');
    } catch {
      // The hint is optional; a blocked localStorage must not break reading.
    }
  };

  const pageCount = documentInfo?.document.page_count ?? 0;
  const documentStatus = documentInfo?.document.status ?? 'unknown';
  const uploadId = documentInfo?.upload_id ?? '';
  const ocrStats = documentInfo?.document.ocr_stats;
  const quality = documentInfo?.quality ?? [];
  const tables = documentInfo?.tables ?? [];
  const figures = documentInfo?.figures ?? [];
  const lowConfidencePages = quality.filter(
    (page) => typeof page.ocr_confidence === 'number' && page.ocr_confidence > 0 && page.ocr_confidence < 0.6,
  );
  const documentBadge = useMemo(() => {
    if (documentStatus === 'partial') return '部分';
    if (documentStatus === 'empty') return '无文本';
    if (documentStatus === 'encrypted') return '加密';
    if (ocrStats && ocrStats.ocr_pages > 0) {
      return ocrStats.ocr_pages >= pageCount ? 'OCR' : '混合';
    }
    return '文本层';
  }, [documentStatus, ocrStats, pageCount]);
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
      <div className="flex h-9 shrink-0 items-center justify-between border-b border-line bg-surface px-2.5 text-[12px] text-muted">
        <span className="flex min-w-0 items-center gap-2">
          <span className="instrument-kicker">论文阅读器</span>
          {pageCount > 0 && <span className="instrument-num border border-line bg-surface-muted px-1.5 py-0.5 text-[11px] text-ink-soft">共 {pageCount} 页</span>}
          {pageCount > 0 && <span className="instrument-num hidden text-[11px] text-faint sm:inline">第 {currentPage} 页</span>}
          {pageCount > 0 && <span className="instrument-num border border-line px-1.5 py-0.5 text-[10.5px] text-muted">{documentBadge}</span>}
          {ocrStats && ocrStats.ocr_pages > 0 ? (
            <span className="instrument-num hidden text-[10.5px] text-muted sm:inline">
              OCR {ocrStats.ocr_pages} 页 · 置信度 {Math.round((ocrStats.average_confidence ?? 0) * 100)}%
            </span>
          ) : null}
        </span>
        <div className="flex shrink-0 items-center gap-1.5">
          <span
            className="hidden h-6 items-center gap-1 border border-line bg-surface px-1.5 text-[10.5px] text-muted sm:inline-flex"
            title="在 PDF 中选中任意文字，浮层会出现「翻译」和「解释」"
          >
            <Highlighter className="h-3 w-3" />
            划词翻译
          </span>
          {onOpenHelp ? (
            <button
              type="button"
              onClick={onOpenHelp}
              className="flex h-6 w-6 items-center justify-center border border-transparent text-muted transition-colors hover:border-line-strong hover:text-ink"
              title="打开阅读指南"
              aria-label="打开阅读指南"
            >
              <HelpCircle className="h-3.5 w-3.5" />
            </button>
          ) : null}
          {tables.length + figures.length > 0 ? (
            <button
              type="button"
              onClick={() => setShowStructure((value) => !value)}
              className={`flex h-6 items-center gap-1.5 border px-2 text-[11.5px] font-medium transition-colors ${
                showStructure
                  ? 'border-accent/35 bg-accent-soft text-accent-ink'
                  : 'border-line bg-surface text-muted hover:border-line-strong hover:text-ink-soft'
              }`}
            >
              表格/图注 {tables.length + figures.length}
            </button>
          ) : null}
          <button
            type="button"
            onClick={() => setShowTranslation((value) => !value)}
            disabled={(documentStatus !== 'extracted' && documentStatus !== 'partial') || pageCount === 0}
            title={documentStatus !== 'extracted' && documentStatus !== 'partial' ? '该 PDF 没有可提取的文本层' : '按页翻译全文'}
            className={`flex h-6 items-center gap-1.5 border px-2 text-[11.5px] font-medium transition-colors ${
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

      {showReaderHint ? (
        <div className="flex shrink-0 items-start gap-2 border-b border-accent/25 bg-accent-soft px-3 py-2 text-[11px] leading-5 text-accent-ink">
          <Highlighter className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <p className="min-w-0 flex-1">
            <b>划词翻译：</b>在论文里选中任意一句，浮层会出现「翻译」和「解释」；也可以点右上角「全文翻译」按页翻译整篇。
          </p>
          <button type="button" onClick={dismissReaderHint} className="shrink-0 border border-accent/30 px-1.5 py-0.5 text-[10px] hover:bg-surface">
            知道了
          </button>
        </div>
      ) : null}

      {statusHint && (
        <div className="flex shrink-0 items-start gap-2 border-b border-warn/25 bg-warn-soft px-3 py-2 text-[11px] leading-5 text-warn">
          <X className="mt-0.5 h-3 w-3 shrink-0" />
          {statusHint}
        </div>
      )}

      {lowConfidencePages.length > 0 ? (
        <div className="flex shrink-0 items-start gap-2 border-b border-warn/25 bg-warn-soft px-3 py-1.5 text-[11px] leading-4 text-warn">
          <X className="mt-0.5 h-3 w-3 shrink-0" />
          第 {lowConfidencePages.map((page) => page.page).join('、')} 页 OCR 置信度低于 60%，引用前请核对原文。
        </div>
      ) : null}

      {showStructure ? (
        <div className="max-h-40 shrink-0 overflow-y-auto border-b border-line bg-surface px-3 py-2">
          {tables.length > 0 ? (
            <div className="mb-2">
              <h4 className="journal-caps mb-1 text-[10px]">表格</h4>
              {tables.map((table) => (
                <div key={table.id} className="flex items-center gap-2 border-b border-line/60 py-1 text-[11px] text-ink-soft">
                  <span className="min-w-0 truncate">{table.caption || `Table ${table.id}`}</span>
                  <span className="instrument-num text-muted">p.{table.page}</span>
                  <a
                    href={`/api/uploads/${uploadId}/document/tables/${table.id}?format=csv`}
                    className="ml-auto shrink-0 text-muted hover:text-ink"
                    download
                  >
                    下载 CSV
                  </a>
                </div>
              ))}
            </div>
          ) : null}
          {figures.length > 0 ? (
            <div>
              <h4 className="journal-caps mb-1 text-[10px]">图注</h4>
              {figures.map((figure) => (
                <div key={figure.id} className="flex items-center gap-2 border-b border-line/60 py-1 text-[11px] text-ink-soft">
                  <span className="min-w-0 truncate">{figure.caption || figure.id}</span>
                  <span className="instrument-num text-muted">p.{figure.page}</span>
                  {figure.image_ref ? (
                    <a
                      href={`/api/uploads/${uploadId}/document/figures/${figure.id}`}
                      target="_blank"
                      rel="noreferrer"
                      className="ml-auto shrink-0 text-muted hover:text-ink"
                    >
                      查看原图
                    </a>
                  ) : (
                    <span className="ml-auto shrink-0 text-faint">无图</span>
                  )}
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

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