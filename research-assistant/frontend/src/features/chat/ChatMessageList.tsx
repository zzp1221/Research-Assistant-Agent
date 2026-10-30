import { useEffect, useRef, useState } from 'react';
import { ArrowDown, FileText, Maximize2, X } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ChatMessage } from '../../contracts/api';

interface PdfActions {
  onOpenPdf: () => void;
  onClosePdf: () => void;
}

interface TaskActions {
  onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
}

interface ChatMessageListProps {
  chatHistory: ChatMessage[];
  loading: boolean;
  isLoggedIn: boolean;
  pdfActions: PdfActions;
  taskActions: TaskActions;
}

const roleLabel = (role: string) => (role === 'user' ? '你' : '助手');

/**
 * Conversation reads as a quiet research thread: role above, body below,
 * user text in a soft green bubble, assistant text on the page itself.
 */
export function ChatMessageList(props: ChatMessageListProps) {
  const { chatHistory, loading, isLoggedIn, pdfActions, taskActions } = props;
  const scrollRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const [nearBottom, setNearBottom] = useState(true);

  const scrollToBottom = (behavior: ScrollBehavior = 'smooth') => {
    bottomRef.current?.scrollIntoView({ behavior, block: 'end' });
  };

  useEffect(() => {
    if (nearBottom) {
      bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
    }
  }, [chatHistory, loading, nearBottom]);

  const handleScroll = () => {
    const node = scrollRef.current;
    if (!node) return;
    const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
    setNearBottom(distance < 96);
  };

  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        // Scrollable log region needs keyboard access (WCAG 2.1.1); axe flags it
        // as scrollable-region-focusable when it cannot be focused.
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
        tabIndex={0}
        role="log"
        aria-label="对话记录"
        className="research-assistant-chat-messages h-full focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
      >
        {!isLoggedIn ? (
          <div className="rounded-xl bg-surface-muted px-3 py-2">
            <p className="text-[10px] font-semibold text-muted">正在建立安全会话</p>
            <p className="mt-0.5 text-[11.5px] leading-5 text-ink-soft">计划会保存在服务器，刷新后可从计划库重新打开。</p>
          </div>
        ) : null}

        {chatHistory.map((msg, i) => {
          const isUser = msg.role === 'user';
          return (
            <article
              key={i}
              className={`research-assistant-chat-message ${isUser ? 'is-user' : 'is-assistant'}`}
            >
              <span className="message-role">{roleLabel(msg.role)}</span>
              <div className="message-body">
                <div
                  className={`prose prose-sm max-w-none text-[13px] leading-[1.7] prose-p:my-1 prose-p:leading-[1.7] prose-headings:text-ink prose-headings:font-semibold prose-strong:text-ink prose-code:rounded-md prose-code:bg-surface-muted prose-code:px-1 prose-code:py-0.5 prose-code:text-[11.5px] prose-code:font-normal prose-code:text-ink prose-pre:rounded-xl prose-pre:border prose-pre:border-line prose-pre:bg-surface-muted prose-pre:text-[11.5px] prose-pre:text-ink-soft prose-a:text-accent prose-a:underline-offset-2 prose-blockquote:border-l-2 prose-blockquote:border-l-accent prose-blockquote:font-normal prose-blockquote:not-italic ${isUser ? 'text-ink' : 'text-ink-soft'}`}
                >
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.text}</ReactMarkdown>
                </div>

                {msg.actions && msg.actions.length > 0 ? (
                  <div className="message-actions">
                    {msg.actions.includes('open_pdf') ? (
                      <button
                        onClick={pdfActions.onOpenPdf}
                        className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
                      >
                        <FileText className="h-3 w-3" />
                        打开论文原文
                      </button>
                    ) : null}
                    {msg.actions.includes('close_pdf') ? (
                      <button
                        onClick={pdfActions.onClosePdf}
                        className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[10.5px] text-muted transition-colors hover:border-line-strong hover:text-ink"
                      >
                        <X className="h-3 w-3" />
                        关闭阅读器
                      </button>
                    ) : null}
                    {msg.actions.includes('view_plot') && msg.taskId ? (
                      <button
                        onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'plot')}
                        className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
                      >
                        <Maximize2 className="h-3 w-3" />
                        查看图表
                      </button>
                    ) : null}
                    {msg.actions.includes('view_report') && msg.taskId ? (
                      <button
                        onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'report')}
                        className="inline-flex h-7 items-center gap-1.5 rounded-lg border border-line bg-surface px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent"
                      >
                        <FileText className="h-3 w-3" />
                        查看报告
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </article>
          );
        })}

        {loading ? (
          <div className="research-assistant-chat-message is-assistant">
            <span className="message-role">助手</span>
            <p className="message-body flex items-center gap-2 text-[12px]">
              <span className="proof-pulse h-1.5 w-1.5 shrink-0 rounded-full bg-accent" aria-hidden="true" />
              正在生成执行计划：解析意图、绑定上下文、编排节点依赖
            </p>
          </div>
        ) : null}
        <div ref={bottomRef} />
      </div>

      {!nearBottom ? (
        <button
          type="button"
          onClick={() => scrollToBottom()}
          className="proof-in absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full border border-line bg-surface px-3 py-1.5 text-[10.5px] text-ink shadow-card transition-colors hover:border-accent hover:text-accent"
        >
          <ArrowDown className="h-3 w-3" />
          回到底部
        </button>
      ) : null}
    </div>
  );
}
