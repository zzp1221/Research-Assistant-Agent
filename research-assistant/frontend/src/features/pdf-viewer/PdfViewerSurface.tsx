import { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import { useAITranslationPlugin, type AssistMode } from '../../hooks/useAITranslationPlugin';
import type { AssistResponse, DocumentInfoResponse, TranslationJob } from '../../contracts/api';
import { PdfPanel } from './PdfPanel';
import '@react-pdf-viewer/core/lib/styles/index.css';
import '@react-pdf-viewer/default-layout/lib/styles/index.css';

export interface PdfViewerSurfaceProps {
  pdfUrl: string;
  documentInfo: DocumentInfoResponse | null;
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
  onAskAI: (text: string) => void;
  onAssist: (mode: AssistMode, text: string, page: number) => Promise<AssistResponse>;
}

export default function PdfViewerSurface(props: PdfViewerSurfaceProps) {
  const { pdfUrl, documentInfo, onPageChange, translation, onAskAI, onAssist } = props;
  // defaultLayoutPlugin() uses React hooks internally; it must be called at the
  // component top level, never inside useMemo or a conditional.
  const defaultLayoutPluginInstance = defaultLayoutPlugin();
  const aiTranslationPluginInstance = useAITranslationPlugin({ onAskAI, onAssist });

  return (
    <PdfPanel
      pdfUrl={pdfUrl}
      documentInfo={documentInfo}
      onPageChange={onPageChange}
      defaultLayoutPluginInstance={defaultLayoutPluginInstance}
      aiTranslationPluginInstance={aiTranslationPluginInstance}
      translation={translation}
    />
  );
}
