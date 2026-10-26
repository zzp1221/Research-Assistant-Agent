import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown, MessageSquarePlus, UserRound } from 'lucide-react';

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

/** Running head of the manuscript: which issue (session) is open. */
export function ChatSessionManager(props: ChatSessionManagerProps) {
  const { state, actions } = props;
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointerDown = (event: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const activeOption = rootRef.current?.querySelector<HTMLElement>('[role="option"][aria-selected="true"]');
    activeOption?.focus();
  }, [open]);

  const activeSession = state.sessions.find((session) => session.id === state.activeSessionId) ?? state.sessions[0] ?? null;

  return (
    <div ref={rootRef} className="relative shrink-0 border-b border-line bg-surface px-4 py-2.5">
      <div className="flex items-start gap-2">
        <button
          ref={triggerRef}
          type="button"
          onClick={() => setOpen((value) => !value)}
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-controls="chat-session-menu"
          className="group flex min-w-0 flex-1 items-start gap-2 text-left"
        >
          <span className="min-w-0 flex-1">
            <span className="journal-caps block text-[10px]">当前稿次</span>
            <span className="mt-0.5 block truncate font-serif text-[14px] font-semibold leading-5 text-ink">
              {activeSession?.title || '未命名会话'}
            </span>
            {activeSession && (
              <span className="mt-0.5 block journal-num text-[11px] leading-4 text-muted">
                {activeSession.messageCount} 条记录 · 更新于 {formatTime(activeSession.updatedAt)}
              </span>
            )}
          </span>
          <ChevronDown className={`mt-1 h-3.5 w-3.5 shrink-0 text-muted transition-transform duration-150 ${open ? 'rotate-180' : ''}`} />
        </button>

        <button
          type="button"
          onClick={actions.onCreateSession}
          disabled={state.loading}
          className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center border border-line bg-surface text-muted transition-colors hover:border-ink hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
          title={state.isLoggedIn ? '新建会话' : '新建临时会话'}
          aria-label={state.isLoggedIn ? '新建会话' : '新建临时 Session'}
        >
          <MessageSquarePlus className="h-3.5 w-3.5" />
        </button>
      </div>

      {open && (
        <div className="proof-fade absolute left-3 right-3 top-[calc(100%+2px)] z-40 border border-ink bg-surface shadow-float">
          <div className="flex items-baseline justify-between border-b border-line px-3 py-2">
            <span className="journal-caps text-[10px]">往期稿次</span>
            <span className="journal-num text-[10.5px] text-muted">{state.sessions.length} 篇</span>
          </div>

          <div id="chat-session-menu" className="max-h-56 overflow-y-auto" role="listbox" aria-label="会话列表">
            {state.sessions.length === 0 && (
              <p className="px-3 py-4 text-center font-serif text-[12px] text-muted">还没有会话记录</p>
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
                  className={`flex w-full items-start gap-2 border-b border-line/60 px-3 py-2 text-left transition-colors last:border-b-0 hover:bg-surface-muted ${isActive ? 'bg-surface-muted' : ''}`}
                >
                  <span className={`mt-1 flex h-3 w-3 shrink-0 items-center justify-center border ${isActive ? 'border-ink bg-ink text-paper' : 'border-line-strong text-transparent'}`}>
                    <Check className="h-2 w-2" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className={`block truncate text-[12.5px] leading-4 ${isActive ? 'font-semibold text-ink' : 'text-ink-soft'}`}>{session.title}</span>
                    <span className="journal-num mt-0.5 block truncate text-[10.5px] leading-3 text-ink-soft">
                      {session.messageCount} 条 · {formatTime(session.updatedAt)}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>

          <div className="border-t border-line bg-surface-muted px-3 py-2.5">
            <div className="flex items-center gap-2 text-[11.5px] text-ink-soft">
              <UserRound className="h-3.5 w-3.5" />
              <span className="truncate">
                会话身份：<span className="font-medium text-ink">{state.userId || '正在建立安全会话…'}</span>
              </span>
            </div>
            <p className="mt-1 text-[10.5px] leading-4 text-muted">
              身份由服务端签名 Cookie 签发，刷新或换设备后仍可找回计划。
            </p>
          </div>
        </div>
      )}
    </div>
  );
}