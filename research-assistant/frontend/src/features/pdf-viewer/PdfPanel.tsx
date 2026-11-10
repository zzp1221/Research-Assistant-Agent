import { useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import { HelpCircle, Highlighter, Languages, X } from 'lucide-react';
import { Viewer, Worker } from '@react-pdf-viewer/core';
import type { PdfJs } from '@react-pdf-viewer/core';
import type { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import type { Plugin } from '@react-pdf-viewer/core';
import type { DocumentInfoResponse, TranslationJob } from '../../contracts/api';
import { TranslationPanel } from './TranslationPanel';
import { usePdfPageControl } from './usePdfPageControl';
import { clampReadingScale, normalizeReadingPage } from './readingStateStorage';

interface PdfPanelProps {
  readerRef?: RefObject<HTMLDivElement | null>;
  pdfUrl: string;
  /**
   * Stable document identity. Changing it means a different document, which is
   * allowed to reset the reader; chat and layout updates must not change it.
   */
  documentKey?: string;
  documentInfo: DocumentInfoResponse | null;
  defaultLayoutPluginInstance: ReturnType<typeof defaultLayoutPlugin>;
  aiTranslationPluginInstance: Plugin;
  /** One-based page the reader should show (restored reading position). */
  initialPage?: number;
  /** Zoom scale the reader should show (1 = 100%). */
  initialScale?: number;
  onPageChange?: (page: number) => void;
  onScaleChange?: (scale: number) => void;
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
    readerRef,
    documentKey,
    documentInfo,
    defaultLayoutPluginInstance,
    aiTranslationPluginInstance,
    initialPage,
    initialScale,
    onPageChange,
    onScaleChange,
    translation,
    onOpenHelp,
  } = props;
  const [showTranslation, setShowTranslation] = useState(false);
  const [showStructure, setShowStructure] = useState(false);
  const [showReaderHint, setShowReaderHint] = useState(() => {
    try {
      return window.localStorage.getItem(READER_HINT_KEY) !== '1';
    } catch {
      return true;
    }
  });
  const restoredPage = normalizeReadingPage(initialPage ?? 1);
  const restoredScale = clampReadingScale(initialScale ?? 1);
  const [currentPage, setCurrentPage] = useState(restoredPage);
  const pageControl = usePdfPageControl();
  const { jumpToPage, zoomTo, getPagesContainer, isPageVisible } = pageControl;
  const [documentLoadVersion, setDocumentLoadVersion] = useState(0);
  // Latest handlers in refs: the restore effect must not re-run (and must never
  // remount the viewer) just because a parent re-created a callback.
  const onPageChangeRef = useRef(onPageChange);
  const onScaleChangeRef = useRef(onScaleChange);
  useEffect(() => {
    onPageChangeRef.current = onPageChange;
    onScaleChangeRef.current = onScaleChange;
  });
  // Only an explicit initialPage/initialScale change (document switch, "return to
  // page") may move the reader. The echo of our own onPageChange/onScaleChange
  // report is skipped, otherwise onPageChange -> props -> jumpToPage would feed
  // back into itself and fight the user's scrolling.
  const lastReportedPageRef = useRef(restoredPage);
  const lastReportedScaleRef = useRef(restoredScale);
  // A jump requested before the document is ready is parked here and applied on
  // the reader's document-load callback, never by remounting the viewer.
  const pendingPageRef = useRef<number | null>(restoredPage > 1 ? restoredPage : null);
  // Initialization can report page one before the requested page is laid out.
  // Keep the restore target until the viewer confirms it in its visible area.
  const restoringPageRef = useRef<number | null>(restoredPage > 1 ? restoredPage : null);
  const pendingScaleRef = useRef<number | null>(null);
  // A parked restore request belongs to the document that requested it. When the
  // host swaps the document without remounting this panel, the stale request must
  // not jump the new paper to the old paper's page.
  const restoreDocumentRef = useRef(documentKey || pdfUrl);

  const applyPendingRestore = () => {
    const pendingPage = pendingPageRef.current;
    if (pendingPage !== null && jumpToPage(pendingPage)) pendingPageRef.current = null;
    const pendingScale = pendingScaleRef.current;
    if (pendingScale !== null && zoomTo(pendingScale)) pendingScaleRef.current = null;
  };

  useEffect(() => {
    const next = documentKey || pdfUrl;
    if (next === restoreDocumentRef.current) return;
    // A different document: drop the previous paper's parked request so it cannot
    // drag the new paper to the old paper's page.
    restoreDocumentRef.current = next;
    restoringPageRef.current = restoredPage > 1 ? restoredPage : null;
    pendingPageRef.current = restoringPageRef.current;
    pendingScaleRef.current = null;
  }, [documentKey, pdfUrl, restoredPage]);

  useEffect(() => {
    if (restoredPage === lastReportedPageRef.current) return;
    lastReportedPageRef.current = restoredPage;
    restoringPageRef.current = restoredPage;
    // Moving the reader is an imperative call into the mounted viewer; the page
    // label follows the viewer's own change event, so a failed or deferred jump
    // never claims a page the reader is not on.
    if (!jumpToPage(restoredPage)) pendingPageRef.current = restoredPage;
  }, [jumpToPage, restoredPage]);

  useEffect(() => {
    if (restoredScale === lastReportedScaleRef.current) return;
    lastReportedScaleRef.current = restoredScale;
    if (!zoomTo(restoredScale)) pendingScaleRef.current = restoredScale;
  }, [restoredScale, zoomTo]);

  useEffect(() => {
    // The captured plugin functions and the document-load callback can arrive in
    // either order, so whichever render finally has both drains the parked
    // request. Nothing here recreates the viewer.
    if (pendingPageRef.current !== null || pendingScaleRef.current !== null) applyPendingRestore();
  });

  const handleDocumentLoad = () => {
    // Child effects (the Viewer's plugin install) run before this panel's
    // effects, but a document reload can still land before the plugin
    // functions are handed back, so both sides drain the parked request.
    applyPendingRestore();
    setDocumentLoadVersion((version) => version + 1);
  };

  useEffect(() => {
    const container = getPagesContainer();
    if (!container || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => {
      const target = restoringPageRef.current;
      if (target !== null && jumpToPage(target)) pendingPageRef.current = null;
      const scale = pendingScaleRef.current;
      if (scale !== null && zoomTo(scale)) pendingScaleRef.current = null;
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, [documentLoadVersion, getPagesContainer, jumpToPage, zoomTo]);

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
    <div className="flex min-h-0 flex-1 flex-col bg-surface-muted">
      <div className="flex min-h-10 shrink-0 flex-wrap items-center justify-between gap-x-2 gap-y-1 border-b border-line bg-surface px-3 py-1 text-[11px] text-muted">
        <span className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
          <span className="hidden text-[10px] font-semibold text-muted sm:inline">论文阅读器</span>
          {pageCount > 0 && <span className="shrink-0 rounded-full bg-surface-muted px-2 py-0.5 font-mono text-[10px] text-ink-soft">共 {pageCount} 页</span>}
          {pageCount > 0 && <span className="hidden shrink-0 font-mono text-[10px] text-faint sm:inline">第 {currentPage} 页</span>}
          {pageCount > 0 && <span className="shrink-0 rounded-full border border-line px-2 py-0.5 font-mono text-[9.5px] text-muted">{documentBadge}</span>}
          {ocrStats && ocrStats.ocr_pages > 0 ? (
            <span className="instrument-num hidden truncate text-[10.5px] text-muted md:inline">
              OCR {ocrStats.ocr_pages} 页 · 置信度 {Math.round((ocrStats.average_confidence ?? 0) * 100)}%
            </span>
          ) : null}
        </span>
        {/* Secondary controls collapse before the primary translation action, so
            narrow readers wrap instead of clipping the main control. */}
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
          <span
            className="hidden h-7 items-center gap-1 rounded-full border border-line bg-surface px-2 text-[9.5px] text-muted lg:inline-flex"
            title="在 PDF 中选中任意文字，浮层会出现「翻译」和「解释」"
          >
            <Highlighter className="h-3 w-3" />
            划词翻译
          </span>
          {onOpenHelp ? (
            <button
              type="button"
              onClick={onOpenHelp}
              className="flex h-9 w-9 items-center justify-center rounded-lg border border-transparent text-muted transition-colors hover:bg-surface-muted hover:text-ink sm:h-7 sm:w-7"
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
              <h4 className="mb-1 text-[9.5px] font-semibold text-muted">表格</h4>
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
              <h4 className="mb-1 text-[9.5px] font-semibold text-muted">图注</h4>
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
        <div ref={readerRef} data-testid="pdf-reader-container" className={`overflow-y-auto bg-surface-muted ${showTranslation ? 'h-[55%]' : 'h-full'}`}>
          <Worker workerUrl={PDF_WORKER_URL}>
            <div style={{ height: '100%', width: '100%' }}>
              <Viewer
                fileUrl={pdfUrl}
                plugins={[defaultLayoutPluginInstance, aiTranslationPluginInstance, pageControl.plugin]}
                defaultScale={restoredScale}
                initialPage={Math.max(0, restoredPage - 1)}
                transformGetDocumentParams={transformGetDocumentParams}
                onDocumentLoad={handleDocumentLoad}
                onPageChange={(event) => {
                  // The viewer reports a zero-based index; every callback,
                  // stored state and reference card above stays one-based.
                  const page = event.currentPage + 1;
                  const target = restoringPageRef.current;
                  if (target !== null) {
                    if (page !== target || !isPageVisible(target)) {
                      pendingPageRef.current = jumpToPage(target) ? null : target;
                      return;
                    }
                    restoringPageRef.current = null;
                    pendingPageRef.current = null;
                  }
                  setCurrentPage(page);
                  if (page === lastReportedPageRef.current) return;
                  lastReportedPageRef.current = page;
                  onPageChangeRef.current?.(page);
                }}
                onZoom={(event) => {
                  const scale = event.scale;
                  if (!Number.isFinite(scale) || scale <= 0) return;
                  const clamped = clampReadingScale(scale);
                  if (clamped === lastReportedScaleRef.current) return;
                  lastReportedScaleRef.current = clamped;
                  onScaleChangeRef.current?.(clamped);
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
