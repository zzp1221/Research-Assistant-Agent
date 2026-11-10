import { lazy, Suspense, useState, type ReactNode } from 'react';
import { BookOpen, MessagesSquare, Network, X } from 'lucide-react';
import { ChatPanel } from '../../features/chat/ChatPanel';
import type {
  AssistResponse,
  ChatMessage,
  DocumentInfoResponse,
  DocumentReferenceDraft,
  LiteratureRecord,
  TranslationJob,
  UploadedFile,
} from '../../contracts/api';
import type { AssistMode } from '../../hooks/useAITranslationPlugin';

const PdfViewerSurface = lazy(() => import('../../features/pdf-viewer/PdfViewerSurface'));
const LiteraturePanel = lazy(() => import('../../features/literature/LiteraturePanel'));

interface LeftWorkspaceShellProps {
  widthPercent: number;
  documentName?: string;
  /** Renders the reader header. Chat and literature panes have no header. */
  showReaderHeader?: boolean;
  onClosePdf?: () => void;
  onReturnToGraph?: () => void;
  onShowChat?: () => void;
  accountLabel?: string;
  children: ReactNode;
}

// Reader toolbar actions stay reachable on the narrowest supported workbench and
// grow to a 44px touch target on mobile. They wrap instead of clipping so the
// document name and the close control never overlap the primary actions.
const readerToolbarButtonClass =
  'inline-flex h-11 shrink-0 items-center justify-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[11px] font-medium text-ink-soft transition-colors hover:border-accent hover:text-accent sm:h-7';

/** The left page of the spread: manuscript copy under review. */
function LeftWorkspaceShell(props: LeftWorkspaceShellProps) {
  const { widthPercent, showReaderHeader, documentName, onClosePdf, onReturnToGraph, onShowChat, children } = props;

  return (
    <div
      style={{ width: `${widthPercent}%` }}
      className="research-assistant-left-workspace relative z-10 flex flex-shrink-0 flex-col overflow-hidden"
    >
      {showReaderHeader && (
        <header className="flex min-h-11 shrink-0 flex-wrap items-center justify-between gap-x-3 gap-y-1.5 border-b border-line bg-surface px-3 py-1.5">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <span className="grid h-6 w-6 shrink-0 place-items-center rounded-lg bg-accent-soft text-accent">
              <BookOpen className="h-3.5 w-3.5" />
            </span>
            <span className="truncate text-[12px] font-semibold text-ink-soft" title={documentName || '未命名稿件'}>
              {documentName || '未命名稿件'}
            </span>
          </div>
          <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
            {onReturnToGraph ? (
              <button type="button" onClick={onReturnToGraph} className={readerToolbarButtonClass} title="返回执行图，不关闭这篇论文">
                <Network className="h-3.5 w-3.5" />
                返回执行图
              </button>
            ) : null}
            {onShowChat ? (
              <button type="button" onClick={onShowChat} className={readerToolbarButtonClass} title="显示对话输入框，不关闭这篇论文">
                <MessagesSquare className="h-3.5 w-3.5" />
                显示对话
              </button>
            ) : null}
            {onClosePdf ? (
              <button
                type="button"
                onClick={onClosePdf}
                className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-muted hover:text-ink sm:h-7 sm:w-7"
                title="关闭论文阅读器"
                aria-label="关闭论文阅读器"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            ) : null}
          </div>
        </header>
      )}
      {children}
    </div>
  );
}

interface LeftWorkspaceChatProps {
  widthPercent: number;
  activeView?: 'chat' | 'literature';
  onActiveViewChange?: (view: 'chat' | 'literature') => void;
  state: {
    chatHistory: ChatMessage[];
    loading: boolean;
    prompt: string;
    showSuggestions: boolean;
    pendingAttachments: UploadedFile[];
    /** Independent reading references attached to the next message. */
    documentReferences?: DocumentReferenceDraft[];
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
    /** Optional so hosts that never attach reading references keep compiling. */
    onRemoveDocumentReference?: (id: string) => void;
    onOpenDocumentReference?: (reference: DocumentReferenceDraft) => void;
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
  literatureActions: {
    activeSessionId: string | null;
    onAttachToSession: (upload: UploadedFile, record: LiteratureRecord) => 'added' | 'duplicate' | 'full';
    onDetachFromSession: (uploadId: string) => void;
    onOpenPdf: (upload: UploadedFile) => void;
    onOpenHelp: () => void;
  };
}

export function LeftWorkspaceChat(props: LeftWorkspaceChatProps) {
  const { widthPercent, activeView: activeViewProp, onActiveViewChange, state, chatActions, pdfActions, taskActions, literatureActions } = props;
  const [internalView, setInternalView] = useState<'chat' | 'literature'>('chat');
  const activeView = activeViewProp ?? internalView;
  const setActiveView = onActiveViewChange ?? setInternalView;
  return (
    <LeftWorkspaceShell
      widthPercent={widthPercent}
      showReaderHeader={false}
      accountLabel={state.userId ? `会话 ${state.userId.slice(0, 8)}` : '正在建立安全会话…'}
    >
      <div className="min-h-0 flex-1">
        {activeView === 'chat' ? (
          <ChatPanel
            state={state}
            chatActions={chatActions}
            pdfActions={pdfActions}
            taskActions={taskActions}
            onOpenLiterature={() => setActiveView('literature')}
          />
        ) : (
          <Suspense fallback={<div className="p-4 text-[11px] text-faint">正在加载文献面板…</div>}>
            <LiteraturePanel
              activeSessionId={literatureActions.activeSessionId}
              onAttachToSession={literatureActions.onAttachToSession}
              onDetachFromSession={literatureActions.onDetachFromSession}
              onOpenPdf={literatureActions.onOpenPdf}
              onOpenHelp={literatureActions.onOpenHelp}
              onGoToChat={() => setActiveView('chat')}
            />
          </Suspense>
        )}
      </div>
    </LeftWorkspaceShell>
  );
}

interface LeftWorkspacePdfProps {
  widthPercent: number;
  pdfUrl: string;
  /**
   * Stable document identity (the upload id) used as the reader key. Using the
   * pdf URL would rebuild the viewer whenever the blob is re-created, which
   * drops the reader back to page one on a chat or layout update.
   */
  documentKey?: string;
  documentName?: string;
  documentInfo: DocumentInfoResponse | null;
  /** One-based page to restore when the reader mounts or the document changes. */
  initialPage?: number;
  /** Zoom scale to restore (1 = 100%). */
  initialScale?: number;
  onClosePdf: () => void;
  onReturnToGraph?: () => void;
  onShowChat?: () => void;
  onPageChange?: (page: number) => void;
  onScaleChange?: (scale: number) => void;
  translation: TranslationPanelState;
  /** Kept as `(text, page)`; a legacy single-argument handler still works. */
  onAskAI: (text: string, page: number) => void;
  onAssist: (mode: AssistMode, text: string, page: number) => Promise<AssistResponse>;
  onOpenHelp?: () => void;
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
  const {
    widthPercent, pdfUrl, documentKey, documentName, documentInfo, initialPage, initialScale,
    onClosePdf, onReturnToGraph, onShowChat, onPageChange, onScaleChange, translation, onAskAI, onAssist, onOpenHelp,
  } = props;
  // The reader instance must survive chat updates and reading-position props;
  // only a different document may remount it.
  const readerKey = documentKey || pdfUrl;
  return (
    <LeftWorkspaceShell
      widthPercent={widthPercent}
      showReaderHeader
      onClosePdf={onClosePdf}
      onReturnToGraph={onReturnToGraph}
      onShowChat={onShowChat}
      documentName={documentName || documentInfo?.name}
    >
      <Suspense fallback={<PdfViewerSkeleton />}>
        <PdfViewerSurface
          key={readerKey}
          pdfUrl={pdfUrl}
          documentKey={readerKey}
          documentInfo={documentInfo}
          initialPage={initialPage}
          initialScale={initialScale}
          onPageChange={onPageChange}
          onScaleChange={onScaleChange}
          translation={translation}
          onAskAI={onAskAI}
          onAssist={onAssist}
          onOpenHelp={onOpenHelp}
        />
      </Suspense>
    </LeftWorkspaceShell>
  );
}