import { lazy, Suspense, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { ChatPanel } from '../../features/chat/ChatPanel';
import type { AssistResponse, ChatMessage, DocumentInfoResponse, TranslationJob, UploadedFile } from '../../contracts/api';
import type { AssistMode } from '../../hooks/useAITranslationPlugin';

const PdfViewerSurface = lazy(() => import('../../features/pdf-viewer/PdfViewerSurface'));

interface LeftWorkspaceShellProps {
  widthPercent: number;
  showClosePdf: boolean;
  onClosePdf: () => void;
  documentName?: string;
  accountLabel?: string;
  children: ReactNode;
}

/** The left page of the spread: manuscript copy under review. */
function LeftWorkspaceShell(props: LeftWorkspaceShellProps) {
  const { widthPercent, showClosePdf, onClosePdf, documentName, children } = props;

  return (
    <div
      style={{ width: `${widthPercent}%` }}
      className="research-assistant-left-workspace relative z-10 flex flex-shrink-0 flex-col overflow-hidden border-r border-line bg-surface"
    >
      {showClosePdf && (
        <header className="flex h-10 shrink-0 items-center justify-between gap-3 border-b border-line bg-surface px-4">
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="journal-caps text-[10px]">论文原文</span>
            <span className="truncate font-serif text-[12.5px] text-ink-soft">{documentName || '未命名稿件'}</span>
          </div>
          <button
            type="button"
            onClick={onClosePdf}
            className="flex h-6 w-6 shrink-0 items-center justify-center border border-transparent text-muted transition-colors hover:border-line-strong hover:text-ink"
            title="关闭论文阅读器"
            aria-label="关闭论文阅读器"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </header>
      )}
      {children}
    </div>
  );
}

interface LeftWorkspaceChatProps {
  widthPercent: number;
  state: {
    chatHistory: ChatMessage[];
    loading: boolean;
    prompt: string;
    showSuggestions: boolean;
    pendingAttachments: UploadedFile[];
    uploadingAttachments: boolean;
    attachmentError: string;
    runtimePermissionMode: 'confirm_important' | 'full_access';
    showFullAccessConfirm: boolean;
    isLoggedIn: boolean;
    userId: string | null;
    loginInput: string;
    activeSessionId: string | null;
    sessions: Array<{
      id: string;
      title: string;
      createdAt: string;
      updatedAt: string;
      messageCount: number;
    }>;
  };
  chatActions: {
    setPrompt: (value: string) => void;
    setShowSuggestions: (next: boolean) => void;
    onSendMessage: () => void;
    setLoginInput: (value: string) => void;
    onLogin: () => void;
    onCreateSession: () => void;
    onSwitchSession: (sessionId: string) => void;
    onAttachFiles: (files: File[]) => void;
    onRemoveAttachment: (uploadId: string) => void;
    onOpenAttachment: (attachment: UploadedFile) => void;
    onRuntimePermissionModeChange: (mode: 'confirm_important' | 'full_access') => void;
    onConfirmFullAccess: () => void;
    onCancelFullAccess: () => void;
  };
  pdfActions: {
    onOpenPdf: () => void;
    onClosePdf: () => void;
  };
  taskActions: {
    onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
  };
}

export function LeftWorkspaceChat(props: LeftWorkspaceChatProps) {
  const { widthPercent, state, chatActions, pdfActions, taskActions } = props;
  return (
    <LeftWorkspaceShell
      widthPercent={widthPercent}
      showClosePdf={false}
      onClosePdf={pdfActions.onClosePdf}
      accountLabel={state.userId ? `会话 ${state.userId.slice(0, 8)}` : '正在建立安全会话…'}
    >
      <ChatPanel state={state} chatActions={chatActions} pdfActions={pdfActions} taskActions={taskActions} />
    </LeftWorkspaceShell>
  );
}

interface LeftWorkspacePdfProps {
  widthPercent: number;
  pdfUrl: string;
  documentInfo: DocumentInfoResponse | null;
  onClosePdf: () => void;
  onPageChange?: (page: number) => void;
  translation: TranslationPanelState;
  onAskAI: (text: string) => void;
  onAssist: (mode: AssistMode, text: string, page: number) => Promise<AssistResponse>;
}

export interface TranslationPanelState {
  job: TranslationJob | null;
  isStarting: boolean;
  error: string;
  pageText: string;
  pageTextLoading: boolean;
  pageTextError: string;
  onStart: () => void;
  onCancel: () => void;
  onOpenPage: (page: number) => void;
}

function PdfViewerSkeleton() {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-paper" aria-busy="true" aria-label="正在加载论文阅读器">
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-line bg-surface px-3">
        <span className="h-2.5 w-16 animate-pulse bg-paper-deep" />
        <span className="h-2.5 w-10 animate-pulse bg-paper-deep" />
      </div>
      <div className="flex flex-1 items-center justify-center p-6">
        <div className="h-full w-full max-w-2xl animate-pulse border border-line bg-surface" />
      </div>
    </div>
  );
}

export function LeftWorkspacePdf(props: LeftWorkspacePdfProps) {
  const { widthPercent, pdfUrl, documentInfo, onClosePdf, onPageChange, translation, onAskAI, onAssist } = props;
  return (
    <LeftWorkspaceShell
      widthPercent={widthPercent}
      showClosePdf
      onClosePdf={onClosePdf}
      documentName={documentInfo?.name}
    >
      <Suspense fallback={<PdfViewerSkeleton />}>
        <PdfViewerSurface
          pdfUrl={pdfUrl}
          documentInfo={documentInfo}
          onPageChange={onPageChange}
          translation={translation}
          onAskAI={onAskAI}
          onAssist={onAssist}
        />
      </Suspense>
    </LeftWorkspaceShell>
  );
}