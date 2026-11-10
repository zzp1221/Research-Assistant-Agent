import type { ChatMessage, DocumentReference, DocumentReferenceDraft, UploadedFile } from '../../contracts/api';
import { ChatComposer } from './ChatComposer';
import { FullAccessConfirmDialog } from './FullAccessConfirmDialog';
import { ChatMessageList } from './ChatMessageList';
import { ChatSessionManager } from './ChatSessionManager';

interface ChatPanelState {
  chatHistory: ChatMessage[];
  loading: boolean;
  prompt: string;
  showSuggestions: boolean;
  pendingAttachments: UploadedFile[];
  /** Draft reading references; limits and validation live in the chat hook. */
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
}

interface ChatActions {
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
  onRemoveDocumentReference?: (id: string) => void;
  onOpenDocumentReference?: (reference: DocumentReferenceDraft) => void;
  onRuntimePermissionModeChange: (mode: 'confirm_important' | 'full_access') => void;
  onConfirmFullAccess: () => void;
  onCancelFullAccess: () => void;
}

interface PdfActions {
  onOpenPdf: () => void;
  onClosePdf: () => void;
}

interface TaskActions {
  onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
}

interface ChatPanelProps {
  state: ChatPanelState;
  chatActions: ChatActions;
  pdfActions: PdfActions;
  taskActions: TaskActions;
  onOpenLiterature: () => void;
  /**
   * Wait copy for the in-flight turn. Defaults to a neutral "answering" label so
   * an ordinary reading question is not described as plan construction; a host
   * that knows a plan is being built can pass the specific text.
   */
  loadingLabel?: string;
  /** Reopen a sent message's source page; omitted when the host has no reader. */
  onOpenDocumentReference?: (reference: DocumentReference) => void;
}

export function ChatPanel(props: ChatPanelProps) {
  const { state, chatActions, pdfActions, taskActions, onOpenLiterature, loadingLabel, onOpenDocumentReference } = props;
  const openSentReference = onOpenDocumentReference ?? (
    chatActions.onOpenDocumentReference && state.activeSessionId
      ? (reference: DocumentReference) => chatActions.onOpenDocumentReference?.({
        ...reference,
        id: `sent-${reference.upload_id}-${reference.page}`,
        sessionId: state.activeSessionId!,
      })
      : undefined
  );

  return (
    <div className="flex h-full min-h-0 flex-col bg-surface">
      <ChatSessionManager
        state={{
          isLoggedIn: state.isLoggedIn,
          userId: state.userId,
          loginInput: state.loginInput,
          activeSessionId: state.activeSessionId,
          sessions: state.sessions,
          loading: state.loading,
        }}
        actions={{
          setLoginInput: chatActions.setLoginInput,
          onLogin: chatActions.onLogin,
          onCreateSession: chatActions.onCreateSession,
          onSwitchSession: chatActions.onSwitchSession,
        }}
      />
      <ChatMessageList
        chatHistory={state.chatHistory}
        loading={state.loading}
        isLoggedIn={state.isLoggedIn}
        pdfActions={pdfActions}
        taskActions={taskActions}
        loadingLabel={loadingLabel}
        onOpenDocumentReference={openSentReference}
      />
      <ChatComposer
        prompt={state.prompt}
        hasConversation={state.chatHistory.some((message) => message.role === 'user')}
        loading={state.loading}
        isLoggedIn={state.isLoggedIn}
        showSuggestions={state.showSuggestions}
        pendingAttachments={state.pendingAttachments}
        documentReferences={state.documentReferences}
        uploadingAttachments={state.uploadingAttachments}
        attachmentError={state.attachmentError}
        setPrompt={chatActions.setPrompt}
        setShowSuggestions={chatActions.setShowSuggestions}
        onSendMessage={chatActions.onSendMessage}
        onAttachFiles={chatActions.onAttachFiles}
        onRemoveAttachment={chatActions.onRemoveAttachment}
        onOpenAttachment={chatActions.onOpenAttachment}
        onRemoveDocumentReference={chatActions.onRemoveDocumentReference}
        onOpenDocumentReference={chatActions.onOpenDocumentReference}
        onOpenLiterature={onOpenLiterature}
        runtimePermissionMode={state.runtimePermissionMode}
        onRuntimePermissionModeChange={chatActions.onRuntimePermissionModeChange}
      />
      <FullAccessConfirmDialog
        open={state.showFullAccessConfirm}
        onCancel={chatActions.onCancelFullAccess}
        onConfirm={chatActions.onConfirmFullAccess}
      />
    </div>
  );
}
