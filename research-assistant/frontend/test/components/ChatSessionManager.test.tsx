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
    expect(screen.getByText('历史会话')).toBeInTheDocument();

    await user.click(screen.getByRole('option', { name: /Apriori 论文复现/ }));
    expect(onSwitchSession).toHaveBeenCalledWith('s-2');

    await user.click(screen.getByRole('button', { name: '新建临时 Session' }));
    expect(onCreateSession).toHaveBeenCalledTimes(1);
  });

  it('shows the server-issued session identity and never offers a user id input', async () => {
    const user = userEvent.setup();

    render(
      <ChatSessionManager
        state={{ ...baseState, isLoggedIn: true, userId: 'user-1234567890' }}
        actions={{
          setLoginInput: vi.fn(),
          onLogin: vi.fn(),
          onCreateSession: vi.fn(),
          onSwitchSession: vi.fn(),
        }}
      />,
    );

    await user.click(screen.getByRole('button', { name: /工业检索多策略实验/ }));
    expect(screen.getByText(/会话身份/)).toBeInTheDocument();
    expect(screen.queryByLabelText('用户 ID')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '登录' })).not.toBeInTheDocument();
  });
});