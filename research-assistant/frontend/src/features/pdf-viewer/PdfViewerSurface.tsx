import { useRef } from 'react';
import { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import { useAITranslationPlugin, type AssistMode } from '../../hooks/useAITranslationPlugin';
import type { AssistResponse, DocumentInfoResponse, TranslationJob } from '../../contracts/api';
import { PdfPanel } from './PdfPanel';
import '@react-pdf-viewer/core/lib/styles/index.css';
import '@react-pdf-viewer/default-layout/lib/styles/index.css';

export interface PdfViewerSurfaceProps {
  pdfUrl: string;
  /**
   * Stable document identity for the AI plugin generation guard. It must not be
   * the blob/pdf URL: re-creating the blob would otherwise reset the plugin and
   * drop in-flight explanations for the same paper.
   */
  documentKey?: string;
  documentInfo: DocumentInfoResponse | null;
  /** One-based restore page; the viewer itself takes a zero-based page. */
  initialPage?: number;
  /** Restored zoom scale (1 = 100%). */
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
  onAskAI: (text: string, page: number) => void;
  onAssist: (mode: AssistMode, text: string, page: number) => Promise<AssistResponse>;
  onOpenHelp?: () => void;
}

export default function PdfViewerSurface(props: PdfViewerSurfaceProps) {
  const {
    pdfUrl, documentKey, documentInfo, initialPage, initialScale, onPageChange, onScaleChange,
    translation, onAskAI, onAssist, onOpenHelp,
  } = props;
  // defaultLayoutPlugin() uses React hooks internally; it must be called at the
  // component top level, never inside useMemo or a conditional.
  // Keep the viewer toolbar intentionally small. The default toolbar renders
  // popover triggers as divs with aria-expanded and a zoom button whose visible
  // text is not part of its accessible name, both of which fail axe. The
  // workbench exposes translation through its own panel, so the built-in
  // sidebar and search/more-actions popovers are unnecessary here.
  const defaultLayoutPluginInstance = defaultLayoutPlugin({
    sidebarTabs: () => [],
    renderToolbar: (Toolbar) => (
      <Toolbar>
        {(toolbarSlot) => (
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-line bg-surface px-2.5 py-1 text-[11.5px] text-muted">
            {toolbarSlot.GoToPreviousPage({})}
            {toolbarSlot.CurrentPageInput()}
            <span>/</span>
            {toolbarSlot.NumberOfPages({})}
            {toolbarSlot.GoToNextPage({})}
            <span className="mx-1 h-3 w-px bg-line" />
            {toolbarSlot.ZoomOut({})}
            {toolbarSlot.ZoomIn({})}
          </div>
        )}
      </Toolbar>
    ),
  });
  const readerRef = useRef<HTMLDivElement>(null);
  const aiTranslationPluginInstance = useAITranslationPlugin({
    onAskAI,
    onAssist,
    readerRef,
    documentKey: documentKey || pdfUrl,
  });

  return (
    <PdfPanel
      readerRef={readerRef}
      pdfUrl={pdfUrl}
      documentInfo={documentInfo}
      defaultLayoutPluginInstance={defaultLayoutPluginInstance}
      aiTranslationPluginInstance={aiTranslationPluginInstance}
      documentKey={documentKey || pdfUrl}
      initialPage={initialPage}
      initialScale={initialScale}
      onPageChange={onPageChange}
      onScaleChange={onScaleChange}
      translation={translation}
      onOpenHelp={onOpenHelp}
    />
  );
}
