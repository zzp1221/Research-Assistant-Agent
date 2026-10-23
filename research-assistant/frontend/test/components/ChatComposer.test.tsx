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
});
