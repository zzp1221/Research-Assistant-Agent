import { useEffect, useRef, useState } from 'react';
import { ArrowDown, BookOpen, FileText, Maximize2, Quote, X } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ChatMessage, DocumentReference } from '../../contracts/api';
import { ContextStatus } from './ContextStatus';

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
  /**
   * Copy shown while a turn is in flight. Ordinary answers and plan creation are
   * different waits, so the neutral default must not claim a plan is being built.
   */
  loadingLabel?: string;
  /** Reopen a sent message's source page; omitted when the host has no reader. */
  onOpenDocumentReference?: (reference: DocumentReference) => void;
}

const roleLabel = (role: string) => (role === 'user' ? '你' : '助手');

export const DEFAULT_CHAT_LOADING_LABEL = '正在整理回答：阅读你的问题与已引用段落';

// Provenance, not verification: these references are a snapshot of what the
// user selected, so they are labelled as such and never as checked evidence.
function DocumentReferenceSources(props: { references: DocumentReference[]; onOpen?: (reference: DocumentReference) => void }) {
  const { references, onOpen } = props;
  return (
    <div className="mt-1.5 rounded-lg border border-line bg-surface-muted px-2 py-1.5">
      <div className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[9.5px] font-semibold text-muted">
        <Quote className="h-3 w-3" aria-hidden="true" />
        出处：用户选中文字
        <span className="font-normal text-faint">· 未由服务端核实</span>
      </div>
      <ul className="mt-1 flex flex-col gap-1">
        {references.map((reference, index) => (
          <li key={`${reference.upload_id}-${reference.page}-${index}`} className="flex min-w-0 items-start gap-1.5 text-[10.5px] leading-4 text-ink-soft">
            <div className="min-w-0 flex-1">
              <span className="min-w-0 break-words font-medium">
                {reference.document_name || '论文'} · 第 {reference.page} 页
              </span>
              {reference.quote ? (
                <span className="mt-0.5 line-clamp-2 block text-[10px] text-muted">“{reference.quote}”</span>
              ) : null}
            </div>
            {onOpen ? (
              <button
                type="button"
                onClick={() => onOpen(reference)}
                className="flex shrink-0 items-center gap-1 rounded-md px-1 py-0.5 text-[10px] text-accent transition-colors hover:bg-surface"
                title={`返回 ${reference.document_name || '论文'} 第 ${reference.page} 页`}
              >
                <BookOpen className="h-3 w-3" />
                返回原页
              </button>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}


/**
 * Conversation reads as a quiet research thread: role above, body below,
 * user text in a soft green bubble, assistant text on the page itself.
 */
export function ChatMessageList(props: ChatMessageListProps) {
  const { chatHistory, loading, isLoggedIn, pdfActions, taskActions, loadingLabel = DEFAULT_CHAT_LOADING_LABEL, onOpenDocumentReference } = props;
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
                <ContextStatus report={msg.contextReport} />

                {msg.documentReferences && msg.documentReferences.length > 0 ? (
                  <DocumentReferenceSources references={msg.documentReferences} onOpen={onOpenDocumentReference} />
                ) : null}

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
              {loadingLabel}
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
