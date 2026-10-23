import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ChatSessionManager } from '../../src/features/chat/ChatSessionManager';

const sessions = [
  { id: 's-1', title: '工业检索多策略实验', createdAt: '2026-10-23T09:30:00Z', updatedAt: '2026-10-23T09:52:00Z', messageCount: 3 },
  { id: 's-2', title: 'Apriori 论文复现', createdAt: '2026-10-22T14:10:00Z', updatedAt: '2026-10-22T15:02:00Z', messageCount: 12 },
];

const baseState = {
  isLoggedIn: false,
  userId: null,
  loginInput: '',
  activeSessionId: 's-1',
  sessions,
  loading: false,
};

describe('ChatSessionManager', () => {
  it('switches sessions and creates a new session from the compact menu', async () => {
    const user = userEvent.setup();
    const onSwitchSession = vi.fn();
    const onCreateSession = vi.fn();

    render(
      <ChatSessionManager
        state={baseState}
        actions={{
          setLoginInput: vi.fn(),
          onLogin: vi.fn(),
          onCreateSession,
          onSwitchSession,
        }}
      />,
    );

    await user.click(screen.getByRole('button', { name: /工业检索多策略实验/ }));
    expect(screen.getByText('会话记录')).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /Apriori 论文复现/ }));
    expect(onSwitchSession).toHaveBeenCalledWith('s-2');

    await user.click(screen.getByRole('button', { name: '新建临时 Session' }));
    expect(onCreateSession).toHaveBeenCalledTimes(1);
  });

  it('keeps the guest login flow inside the menu', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn();
    const setLoginInput = vi.fn();

    render(
      <ChatSessionManager
        state={{ ...baseState, loginInput: 'alice' }}
        actions={{
          setLoginInput,
          onLogin,
          onCreateSession: vi.fn(),
          onSwitchSession: vi.fn(),
        }}
      />,
    );

    await user.click(screen.getByRole('button', { name: /工业检索多策略实验/ }));
    const input = screen.getByLabelText('用户 ID');
    expect(input).toHaveValue('alice');
    await user.type(input, 'x');
    expect(setLoginInput).toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: '登录' }));
    expect(onLogin).toHaveBeenCalledTimes(1);
  });
});
