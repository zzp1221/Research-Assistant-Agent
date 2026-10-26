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

const roleLabel = (role: string) => {
  if (role === 'user') return '作者';
  if (role === 'system') return '校样';
  return '助手';
};

const paragraphNumber = (index: number) => {
  const n = index + 1;
  return `§${n}`;
};

/**
 * Messages read as sectioned manuscript copy: a marginal paragraph number,
 * a running head for the role, and serif body text — no chat bubbles.
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
      <div className="flex h-8 shrink-0 items-baseline justify-between border-b border-line bg-surface px-4">
        <span className="journal-caps text-[10px]">正文 · 校样批注</span>
        <span className="journal-num text-[10.5px] text-muted">{chatHistory.length} 段</span>
      </div>
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        // Scrollable log region needs keyboard access (WCAG 2.1.1); axe flags it
        // as scrollable-region-focusable when it cannot be focused.
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
        tabIndex={0}
        role="log"
        aria-label="校样正文"
        className="h-[calc(100%-2rem)] overflow-y-auto focus-visible:outline focus-visible:outline-2 focus-visible:outline-proof"
      >
        {!isLoggedIn && (
          <div className="border-b border-line bg-surface-muted px-4 py-2">
            <p className="journal-caps text-[10px]">正在建立安全会话</p>
            <p className="mt-0.5 font-serif text-[12px] leading-5 text-ink-soft">服务端正在签发会话身份；计划会保存在服务器，刷新后可从计划库重新打开。</p>
          </div>
        )}

        {chatHistory.map((msg, i) => {
          const isSystem = msg.role === 'system';
          return (
            <article
              key={i}
              className={`proof-fade border-b border-line/70 px-4 py-3 ${isSystem ? 'bg-surface-muted/60' : 'bg-surface'}`}
            >
              <div className="flex items-baseline justify-between gap-3">
                <span className="journal-num text-[10.5px] text-muted">{paragraphNumber(i)}</span>
                <span className={`journal-caps text-[10px] ${msg.role === 'user' ? 'font-semibold text-ink' : 'text-muted'}`}>
                  {roleLabel(msg.role)}
                </span>
              </div>
              <div className="mt-1.5">
                <div
                  className={`prose prose-sm max-w-none font-serif text-[13px] leading-[1.72] ${isSystem ? 'text-ink-soft' : 'text-ink'} prose-p:my-1 prose-p:leading-[1.72] prose-headings:font-serif prose-headings:font-semibold prose-headings:text-ink prose-strong:text-ink prose-code:rounded-none prose-code:bg-surface-strong prose-code:px-1 prose-code:py-0.5 prose-code:text-[12px] prose-code:font-normal prose-code:text-ink prose-pre:rounded-none prose-pre:border prose-pre:border-line-strong prose-pre:bg-surface-muted prose-pre:text-[12px] prose-pre:text-ink-soft prose-a:text-proof prose-a:underline-offset-2 prose-blockquote:border-l-2 prose-blockquote:border-l-proof prose-blockquote:font-normal prose-blockquote:not-italic`}
                >
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.text}</ReactMarkdown>
                </div>

                {msg.actions && msg.actions.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {msg.actions.includes('open_pdf') && (
                      <button
                        onClick={pdfActions.onOpenPdf}
                        className="inline-flex h-6 items-center gap-1.5 border border-line-strong bg-surface px-2 text-[11.5px] text-ink-soft transition-colors hover:border-ink hover:text-ink"
                      >
                        <FileText className="h-3 w-3" />
                        打开论文原文
                      </button>
                    )}
                    {msg.actions.includes('close_pdf') && (
                      <button
                        onClick={pdfActions.onClosePdf}
                        className="inline-flex h-6 items-center gap-1.5 border border-line bg-surface px-2 text-[11.5px] text-muted transition-colors hover:border-line-strong hover:text-ink"
                      >
                        <X className="h-3 w-3" />
                        关闭阅读器
                      </button>
                    )}
                    {msg.actions.includes('view_plot') && msg.taskId && (
                      <button
                        onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'plot')}
                        className="inline-flex h-6 items-center gap-1.5 border border-line-strong bg-surface px-2 text-[11.5px] text-ink-soft transition-colors hover:border-ink hover:text-ink"
                      >
                        <Maximize2 className="h-3 w-3" />
                        查看图版
                      </button>
                    )}
                    {msg.actions.includes('view_report') && msg.taskId && (
                      <button
                        onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'report')}
                        className="inline-flex h-6 items-center gap-1.5 border border-line-strong bg-surface px-2 text-[11.5px] text-ink-soft transition-colors hover:border-ink hover:text-ink"
                      >
                        <FileText className="h-3 w-3" />
                        查看批注报告
                      </button>
                    )}
                  </div>
                )}
              </div>
            </article>
          );
        })}

        {loading && (
          <div className="proof-in border-b border-line/70 bg-surface-muted/60 px-4 py-3">
            <div className="flex items-baseline justify-between">
              <span className="journal-num text-[10.5px] text-muted">§·</span>
              <span className="journal-caps text-[10px] text-muted">校样</span>
            </div>
            <p className="mt-1 flex items-center gap-2 font-serif text-[12.5px] leading-6 text-ink-soft">
              <span className="proof-pulse h-1.5 w-1.5 shrink-0 bg-proof" aria-hidden="true" />
              正在排版：解析意图、绑定上下文、编排节点依赖
            </p>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {!nearBottom && (
        <button
          type="button"
          onClick={() => scrollToBottom()}
          className="proof-in absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 border border-ink bg-surface px-3 py-1.5 text-[11.5px] text-ink shadow-float transition-colors hover:bg-surface-muted"
        >
          <ArrowDown className="h-3 w-3" />
          回到末页
        </button>
      )}
    </div>
  );
}