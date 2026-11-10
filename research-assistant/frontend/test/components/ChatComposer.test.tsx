import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ChatComposer } from '../../src/features/chat/ChatComposer';

const baseProps = {
  prompt: '',
  loading: false,
  isLoggedIn: true,
  showSuggestions: true,
  pendingAttachments: [],
  uploadingAttachments: false,
  attachmentError: '',
  setPrompt: vi.fn(),
  setShowSuggestions: vi.fn(),
  onSendMessage: vi.fn(),
  onAttachFiles: vi.fn(),
  onRemoveAttachment: vi.fn(),
  onOpenAttachment: vi.fn(),
};

describe('ChatComposer', () => {
  it('fills the prompt from a suggestion and sends only non-empty text', async () => {
    const user = userEvent.setup();
    const setPrompt = vi.fn();
    const { rerender } = render(<ChatComposer {...baseProps} setPrompt={setPrompt} />);

    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: /帮我画一个正弦函数/ }));
    expect(setPrompt).toHaveBeenCalledWith('帮我画一个正弦函数和余弦函数的对比图');

    await user.type(screen.getByRole('textbox'), '分析这篇论文');
    expect(setPrompt).toHaveBeenCalled();
    rerender(<ChatComposer {...baseProps} prompt="分析这篇论文" setPrompt={setPrompt} />);
    const send = screen.getByRole('button', { name: '发送消息' });
    expect(send).toBeEnabled();
    await user.click(send);
    expect(baseProps.onSendMessage).toHaveBeenCalledTimes(1);
  });

  it('shows the attachment entry point', () => {
    render(<ChatComposer {...baseProps} />);
    expect(screen.getByRole('button', { name: '添加附件' })).toBeInTheDocument();
  });

  it('renders a reading reference as a separate card without touching the prompt', async () => {
    const user = userEvent.setup();
    const setPrompt = vi.fn();
    const onRemoveDocumentReference = vi.fn();
    const onOpenDocumentReference = vi.fn();
    const reference = {
      id: 'ref-1',
      upload_id: 'upload-1',
      document_name: 'Attention Is All You Need.pdf',
      page: 4,
      quote: 'Scaled dot-product attention is identical to our algorithm',
      sessionId: 'session-1',
    };
    render(
      <ChatComposer
        {...baseProps}
        prompt="这个公式有什么直觉解释？"
        documentReferences={[reference]}
        setPrompt={setPrompt}
        onRemoveDocumentReference={onRemoveDocumentReference}
        onOpenDocumentReference={onOpenDocumentReference}
      />,
    );

    const card = screen.getByTestId('composer-document-reference');
    expect(card).toHaveTextContent('Attention Is All You Need.pdf');
    expect(card).toHaveTextContent('· 第 4 页');
    expect(card).toHaveTextContent('Scaled dot-product attention');
    // The quote is the user's own selection, so it is labelled as such and never
    // as server-verified evidence.
    expect(card).toHaveTextContent('用户选中文字');

    // The user's existing question survives untouched: the reference is an
    // independent card, not text merged into the textarea.
    expect(screen.getByRole('textbox')).toHaveValue('这个公式有什么直觉解释？');
    expect(setPrompt).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: '返回 Attention Is All You Need.pdf 第 4 页' }));
    expect(onOpenDocumentReference).toHaveBeenCalledWith(reference);
    expect(onRemoveDocumentReference).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: '移除引用 Attention Is All You Need.pdf 第 4 页' }));
    expect(onRemoveDocumentReference).toHaveBeenCalledWith('ref-1');
    expect(setPrompt).not.toHaveBeenCalled();
  });

  it('offers an explicit send for a reference while preserving the normal empty guard', async () => {
    const onSendMessage = vi.fn();
    const { rerender } = render(<ChatComposer {...baseProps} onSendMessage={onSendMessage} />);
    expect(screen.getByRole('button', { name: '发送消息' })).toBeDisabled();

    // The flow supplies a reading explanation question when only a selection
    // is sent; adding the reference alone still never sends automatically.
    rerender(
      <ChatComposer
        {...baseProps}
        onSendMessage={onSendMessage}
        documentReferences={[
          { id: 'ref-2', upload_id: 'upload-1', document_name: 'paper.pdf', page: 2, quote: 'quote', sessionId: 'session-1' },
        ]}
      />,
    );
    expect(screen.getByRole('button', { name: '发送消息' })).toBeEnabled();
    expect(onSendMessage).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: '发送消息' }));
    expect(onSendMessage).toHaveBeenCalledTimes(1);

    rerender(
      <ChatComposer
        {...baseProps}
        onSendMessage={onSendMessage}
        prompt="这段的直觉是什么？"
        documentReferences={[
          { id: 'ref-2', upload_id: 'upload-1', document_name: 'paper.pdf', page: 2, quote: 'quote', sessionId: 'session-1' },
        ]}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: '发送消息' }));
    expect(onSendMessage).toHaveBeenCalledTimes(2);
    // A reference must never be turned into a chat attachment, which would
    // re-route a reading question into plan creation.
    expect(baseProps.onAttachFiles).not.toHaveBeenCalled();
  });
});
