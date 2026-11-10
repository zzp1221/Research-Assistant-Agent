import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { DocumentReferenceDraft } from '../../src/contracts/api';
import { ChatPanel } from '../../src/features/chat/ChatPanel';

// The reference card is the contract between the PDF reader and the composer:
// it must show the paper name and a one-based page, stay independent of the
// user's own question, and be removable or re-openable.

const makeReference = (overrides: Partial<DocumentReferenceDraft> = {}): DocumentReferenceDraft => ({
  id: 'ref-1',
  upload_id: 'upload-1',
  document_name: 'Attention Is All You Need.pdf',
  page: 4,
  quote: 'Scaled dot-product attention is identical to our algorithm',
  sessionId: 'session-1',
  ...overrides,
});

const baseState = {
  chatHistory: [],
  loading: false,
  prompt: '',
  showSuggestions: false,
  pendingAttachments: [],
  documentReferences: [] as DocumentReferenceDraft[],
  uploadingAttachments: false,
  attachmentError: '',
  runtimePermissionMode: 'confirm_important' as const,
  showFullAccessConfirm: false,
  isLoggedIn: true,
  userId: 'user-1',
  loginInput: '',
  activeSessionId: 'session-1',
  sessions: [],
};

const baseActions = {
  setPrompt: vi.fn(),
  setShowSuggestions: vi.fn(),
  onSendMessage: vi.fn(),
  setLoginInput: vi.fn(),
  onLogin: vi.fn(),
  onCreateSession: vi.fn(),
  onSwitchSession: vi.fn(),
  onAttachFiles: vi.fn(),
  onRemoveAttachment: vi.fn(),
  onOpenAttachment: vi.fn(),
  onRuntimePermissionModeChange: vi.fn(),
  onConfirmFullAccess: vi.fn(),
  onCancelFullAccess: vi.fn(),
};

const renderChat = (overrides: {
  state?: Partial<typeof baseState>;
  chatActions?: Partial<typeof baseActions>;
} = {}) =>
  render(
    <ChatPanel
      state={{ ...baseState, ...overrides.state }}
      chatActions={{ ...baseActions, ...overrides.chatActions }}
      pdfActions={{ onOpenPdf: vi.fn(), onClosePdf: vi.fn() }}
      taskActions={{ onOpenTaskView: vi.fn() }}
      onOpenLiterature={vi.fn()}
    />,
  );

describe('chat document references', () => {
  it('shows the paper name and one-based page next to existing user text', () => {
    const { container } = renderChat({
      state: { prompt: '这个公式为什么能去掉缩放因子？', documentReferences: [makeReference()] },
    });

    const card = screen.getByTestId('composer-document-reference');
    expect(card).toHaveTextContent('Attention Is All You Need.pdf');
    expect(card).toHaveTextContent('· 第 4 页');
    expect(card).toHaveTextContent('用户选中文字');
    // The question the user already typed stays in the textarea, untouched.
    expect(screen.getByRole('textbox')).toHaveValue('这个公式为什么能去掉缩放因子？');
    expect(container.querySelectorAll('[data-testid="composer-document-reference"]')).toHaveLength(1);
  });

  it('removes a reference without clearing the user prompt', async () => {
    const user = userEvent.setup();
    const setPrompt = vi.fn();
    const onRemoveDocumentReference = vi.fn();
    renderChat({
      state: { prompt: '继续解释这一段的动机', documentReferences: [makeReference({ id: 'ref-42' })] },
      chatActions: { setPrompt, onRemoveDocumentReference },
    });

    await user.click(screen.getByRole('button', { name: /移除引用 Attention Is All You Need\.pdf 第 4 页/ }));
    expect(onRemoveDocumentReference).toHaveBeenCalledWith('ref-42');
    // Removing a reference is not a prompt edit.
    expect(setPrompt).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox')).toHaveValue('继续解释这一段的动机');
  });

  it('returns to the original page instead of sending or closing the reader', async () => {
    const user = userEvent.setup();
    const onOpenDocumentReference = vi.fn();
    const onSendMessage = vi.fn();
    renderChat({
      state: { documentReferences: [makeReference()] },
      chatActions: { onOpenDocumentReference, onSendMessage },
    });

    await user.click(screen.getByRole('button', { name: /返回 Attention Is All You Need\.pdf 第 4 页/ }));
    expect(onOpenDocumentReference).toHaveBeenCalledWith(makeReference());
    expect(onSendMessage).not.toHaveBeenCalled();
  });

  it('keeps several references independent and one-based', () => {
    renderChat({
      state: {
        documentReferences: [
          makeReference({ id: 'ref-1', page: 1 }),
          makeReference({ id: 'ref-2', page: 12, document_name: 'Transformer v2.pdf' }),
        ],
      },
    });

    const cards = screen.getAllByTestId('composer-document-reference');
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveTextContent('第 1 页');
    expect(cards[1]).toHaveTextContent('Transformer v2.pdf');
    expect(cards[1]).toHaveTextContent('· 第 12 页');
  });

  it('renders sent provenance and returns to its page through the host reader action', async () => {
    const onOpenDocumentReference = vi.fn();
    renderChat({
      chatActions: { onOpenDocumentReference },
      state: {
        chatHistory: [
          {
            role: 'user',
            text: '这段的复杂度分析怎么来的？',
            documentReferences: [
              { upload_id: 'upload-1', document_name: 'Attention Is All You Need.pdf', page: 6, quote: 'self-attention layer connects all positions' },
            ],
          },
          { role: 'assistant', text: '它来自每个位置与所有位置的直接连接。' },
        ],
      },
    });

    expect(screen.getByText('出处：用户选中文字')).toBeInTheDocument();
    expect(screen.getByText(/未由服务端核实/)).toBeInTheDocument();
    expect(screen.getByText('Attention Is All You Need.pdf · 第 6 页')).toBeInTheDocument();
    // It must never be presented as checked/verified evidence.
    expect(screen.queryByText(/已核实/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '返回原页' }));
    expect(onOpenDocumentReference).toHaveBeenCalledWith(expect.objectContaining({
      upload_id: 'upload-1', page: 6,
      quote: 'self-attention layer connects all positions', sessionId: 'session-1',
    }));
  });

  it('uses an accurate loading label for an ordinary answer instead of claiming a plan', () => {
    renderChat({ state: { loading: true, chatHistory: [{ role: 'user', text: '解释这段' }] } });

    expect(screen.getByText(/正在整理回答/)).toBeInTheDocument();
    expect(screen.queryByText(/正在生成执行计划/)).not.toBeInTheDocument();
  });
});
