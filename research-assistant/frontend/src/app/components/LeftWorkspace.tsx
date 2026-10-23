import type { ReactNode } from 'react';
import { BookOpenText, UserRound, X } from 'lucide-react';
import { ChatPanel } from '../../features/chat/ChatPanel';
import { PdfPanel } from '../../features/pdf-viewer/PdfPanel';
import type { ChatMessage, DocumentInfoResponse, TranslationJob, UploadedFile } from '../../contracts/api';
import type { Plugin } from '@react-pdf-viewer/core';
import type { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';

interface LeftWorkspaceShellProps {
  widthPercent: number;
  showClosePdf: boolean;
  onClosePdf: () => void;
  documentName?: string;
  accountLabel?: string;
  children: ReactNode;
}

function LeftWorkspaceShell(props: LeftWorkspaceShellProps) {
  const { widthPercent, showClosePdf, onClosePdf, documentName, accountLabel, children } = props;

  return (
    <div
      style={{ width: `${widthPercent}%` }}
      className="research-assistant-left-workspace relative z-10 flex flex-shrink-0 flex-col overflow-hidden border-r border-line bg-surface"
    >
      <header className="flex h-[58px] shrink-0 items-center justify-between gap-3 border-b border-line bg-surface px-4">
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-[5px] bg-accent text-surface shadow-sm">
            <span className="font-serif text-[15px] font-semibold leading-none">R</span>
          </div>
          <div className="min-w-0">
            <h1 className="truncate font-serif text-[15px] font-semibold leading-tight tracking-[-0.01em] text-ink">
              Research Assistant{' '}
              <span className="font-sans text-[10px] font-semibold uppercase tracking-[0.16em] text-muted">Agent</span>
            </h1>
            <p className="mt-0.5 truncate text-[10.5px] tracking-[0.04em] text-faint">
              {documentName ? documentName : '论文阅读 · 计划执行 · 结果核验'}
            </p>
          </div>
        </div>
        {showClosePdf ? (
          <button
            type="button"
            onClick={onClosePdf}
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[5px] border border-line bg-surface text-muted transition-colors hover:border-line-strong hover:bg-surface-muted hover:text-ink"
            title="关闭论文阅读器"
            aria-label="关闭论文阅读器"
          >
            <X className="h-4 w-4" />
          </button>
        ) : accountLabel ? (
          <span
            className="flex max-w-28 shrink-0 items-center gap-1.5 rounded-full border border-line bg-surface-muted px-2.5 py-1 text-[10.5px] font-medium text-muted"
            title={accountLabel}
          >
            <UserRound className="h-3 w-3 shrink-0 text-accent" />
            <span className="truncate">{accountLabel}</span>
          </span>
        ) : (
          <span className="hidden shrink-0 items-center gap-1.5 rounded-full border border-line bg-surface-muted px-2.5 py-1 text-[10.5px] font-medium text-muted sm:flex">
            <BookOpenText className="h-3 w-3 text-accent" />
            科研工作台
          </span>
        )}
      </header>
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
      accountLabel={state.isLoggedIn ? state.userId ?? '已登录' : '游客模式'}
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
  defaultLayoutPluginInstance: ReturnType<typeof defaultLayoutPlugin>;
  aiTranslationPluginInstance: Plugin;
  onPageChange?: (page: number) => void;
  translation: TranslationPanelState;
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

export function LeftWorkspacePdf(props: LeftWorkspacePdfProps) {
  const { widthPercent, pdfUrl, documentInfo, onClosePdf, defaultLayoutPluginInstance, aiTranslationPluginInstance, onPageChange, translation } = props;
  return (
    <LeftWorkspaceShell
      widthPercent={widthPercent}
      showClosePdf
      onClosePdf={onClosePdf}
      documentName={documentInfo?.name}
    >
      <PdfPanel
        pdfUrl={pdfUrl}
        documentInfo={documentInfo}
        onPageChange={onPageChange}
        defaultLayoutPluginInstance={defaultLayoutPluginInstance}
        aiTranslationPluginInstance={aiTranslationPluginInstance}
        translation={translation}
      />
    </LeftWorkspaceShell>
  );
}
