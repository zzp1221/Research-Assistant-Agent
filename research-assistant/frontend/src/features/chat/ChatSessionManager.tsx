import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown, LogIn, MessageSquarePlus, MessagesSquare, UserRound } from 'lucide-react';

interface SessionSummary {
  id: string;
  title: string;
  updatedAt: string;
  messageCount: number;
}

interface SessionManagerState {
  isLoggedIn: boolean;
  userId: string | null;
  loginInput: string;
  activeSessionId: string | null;
  sessions: SessionSummary[];
  loading: boolean;
}

interface SessionManagerActions {
  setLoginInput: (value: string) => void;
  onLogin: () => void;
  onCreateSession: () => void;
  onSwitchSession: (sessionId: string) => void;
}

interface ChatSessionManagerProps {
  state: SessionManagerState;
  actions: SessionManagerActions;
}

const formatTime = (value: string) => {
  const time = new Date(value);
  if (Number.isNaN(time.getTime())) return '--';
  return time.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
};

export function ChatSessionManager(props: ChatSessionManagerProps) {
  const { state, actions } = props;
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [open]);

  const activeSession = state.sessions.find((session) => session.id === state.activeSessionId) ?? state.sessions[0] ?? null;

  return (
    <div ref={rootRef} className="relative shrink-0 border-b border-line bg-surface-muted/70 px-3 py-2">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => setOpen((value) => !value)}
          aria-expanded={open}
          aria-haspopup="listbox"
          className="group flex min-w-0 flex-1 items-center gap-2 rounded-[5px] px-2 py-1.5 text-left transition-colors hover:bg-surface"
        >
          <MessagesSquare className="h-4 w-4 shrink-0 text-accent" />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[13px] font-semibold leading-4 text-ink">
              {activeSession?.title || '未命名会话'}
            </span>
            <span className="mt-0.5 block text-[10.5px] leading-3 text-faint">
              {activeSession ? `${activeSession.messageCount} 条消息 · ${formatTime(activeSession.updatedAt)}` : '新建一个会话开始研究'}
            </span>
          </span>
          <ChevronDown className={`h-3.5 w-3.5 shrink-0 text-faint transition-transform duration-150 ${open ? 'rotate-180' : ''}`} />
        </button>

        <button
          type="button"
          onClick={actions.onCreateSession}
          disabled={state.loading}
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-[5px] border border-line bg-surface text-muted transition-colors hover:border-line-strong hover:bg-surface hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
          title={state.isLoggedIn ? '新建会话' : '新建临时会话'}
          aria-label={state.isLoggedIn ? '新建会话' : '新建临时 Session'}
        >
          <MessageSquarePlus className="h-3.5 w-3.5" />
        </button>
      </div>

      {open && (
        <div className="ra-fade-up absolute left-3 right-3 top-[calc(100%-2px)] z-40 overflow-hidden rounded-[7px] border border-line bg-surface shadow-[var(--shadow-float)]">
          <div className="flex items-center justify-between border-b border-line px-3 py-2">
            <span className="text-[10.5px] font-semibold uppercase tracking-[0.14em] text-muted">会话记录</span>
            <span className="text-[10.5px] text-faint">{state.sessions.length} 个</span>
          </div>

          <div className="max-h-56 overflow-y-auto py-1" role="listbox" aria-label="会话列表">
            {state.sessions.length === 0 && (
              <p className="px-3 py-4 text-center text-[11.5px] text-faint">还没有会话记录</p>
            )}
            {state.sessions.map((session) => {
              const isActive = session.id === state.activeSessionId;
              return (
                <button
                  key={session.id}
                  type="button"
                  role="option"
                  aria-selected={isActive}
                  onClick={() => {
                    actions.onSwitchSession(session.id);
                    setOpen(false);
                  }}
                  className={`flex w-full items-start gap-2 px-3 py-2 text-left transition-colors hover:bg-surface-muted ${isActive ? 'bg-accent-soft/60' : ''}`}
                >
                  <span className={`mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border ${isActive ? 'border-accent bg-accent text-white' : 'border-line-strong text-transparent'}`}>
                    <Check className="h-2.5 w-2.5" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[12.5px] font-medium leading-4 text-ink">{session.title}</span>
                    <span className="mt-0.5 block truncate text-[10.5px] leading-3 text-faint">
                      {session.messageCount} 条 · {formatTime(session.updatedAt)}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>

          <div className="border-t border-line bg-surface-muted/60 px-3 py-2.5">
            {state.isLoggedIn ? (
              <div className="flex items-center gap-2 text-[11.5px] text-muted">
                <UserRound className="h-3.5 w-3.5 text-accent" />
                <span className="truncate">已登录：<span className="font-medium text-ink-soft">{state.userId}</span></span>
              </div>
            ) : (
              <div>
                <div className="mb-1.5 flex items-center gap-1.5 text-[10.5px] font-medium text-muted">
                  <UserRound className="h-3 w-3" />
                  游客模式 · 登录后持久保存会话
                </div>
                <div className="flex gap-1.5">
                  <input
                    value={state.loginInput}
                    onChange={(event) => actions.setLoginInput(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') {
                        event.preventDefault();
                        actions.onLogin();
                      }
                    }}
                    placeholder="用户 ID，例如 alice"
                    aria-label="用户 ID"
                    className="h-8 min-w-0 flex-1 rounded-[5px] border border-line bg-surface px-2 text-[12px] text-ink outline-none transition-colors placeholder:text-faint focus:border-accent focus:ring-2 focus:ring-accent/15"
                  />
                  <button
                    type="button"
                    onClick={actions.onLogin}
                    disabled={state.loading || !state.loginInput.trim()}
                    className="inline-flex h-8 items-center gap-1 rounded-[5px] bg-accent px-2.5 text-[12px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:cursor-not-allowed disabled:opacity-40"
                  >
                    <LogIn className="h-3 w-3" />
                    登录
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
