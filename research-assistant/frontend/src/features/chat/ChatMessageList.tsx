import { useEffect, useRef, useState } from 'react';
import { ArrowDown, Bot, FileText, Maximize2, X } from 'lucide-react';
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
      <div ref={scrollRef} onScroll={handleScroll} className="h-full space-y-4 overflow-y-auto px-4 py-4">
        {!isLoggedIn && (
          <div className="flex items-start gap-2 rounded-[6px] border border-line bg-surface-muted px-3 py-2 text-[11.5px] leading-5 text-muted">
            <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />
            <span>游客模式：可以直接对话和新建临时会话，刷新页面后不会保留记录。</span>
          </div>
        )}

        {chatHistory.map((msg, i) => (
          <div key={i} className={`ra-fade-up flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
            {msg.role === 'user' ? (
              <div className="max-w-[84%] rounded-[9px] rounded-br-[3px] border border-line bg-paper-deep/75 px-3.5 py-2.5 text-[13.5px] leading-6 text-ink shadow-[0_1px_0_rgb(255_255_255/0.6)_inset]">
                {msg.text}
              </div>
            ) : (
              <div className="flex max-w-[94%] items-start gap-2.5">
                <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-line bg-surface text-accent">
                  <Bot className="h-3.5 w-3.5" />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="prose prose-sm max-w-none text-[13.5px] leading-6 text-ink-soft prose-p:my-1.5 prose-p:leading-6 prose-headings:font-serif prose-headings:font-semibold prose-headings:text-ink prose-strong:text-ink prose-code:rounded-[3px] prose-code:bg-paper-deep prose-code:px-1 prose-code:py-0.5 prose-code:text-[12.5px] prose-code:font-normal prose-code:text-accent-ink prose-pre:rounded-[6px] prose-pre:border prose-pre:border-white/10 prose-pre:bg-[#1d1e1a] prose-pre:text-[12.5px] prose-pre:text-[#e6e1d5] prose-a:text-accent prose-a:underline-offset-2">
                    <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.text}</ReactMarkdown>
                  </div>

                  {msg.actions && msg.actions.length > 0 && (
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {msg.actions.includes('open_pdf') && (
                        <button
                          onClick={pdfActions.onOpenPdf}
                          className="inline-flex items-center gap-1.5 rounded-[5px] border border-line bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-accent/40 hover:bg-accent-soft hover:text-accent-ink"
                        >
                          <FileText className="h-3 w-3" />
                          打开论文原文
                        </button>
                      )}
                      {msg.actions.includes('close_pdf') && (
                        <button
                          onClick={pdfActions.onClosePdf}
                          className="inline-flex items-center gap-1.5 rounded-[5px] border border-line bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-muted transition-colors hover:border-line-strong hover:bg-surface-muted hover:text-ink"
                        >
                          <X className="h-3 w-3" />
                          关闭阅读器
                        </button>
                      )}
                      {msg.actions.includes('view_plot') && msg.taskId && (
                        <button
                          onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'plot')}
                          className="inline-flex items-center gap-1.5 rounded-[5px] border border-line bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-accent/40 hover:bg-accent-soft hover:text-accent-ink"
                        >
                          <Maximize2 className="h-3 w-3" />
                          查看生成的图表
                        </button>
                      )}
                      {msg.actions.includes('view_report') && msg.taskId && (
                        <button
                          onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'report')}
                          className="inline-flex items-center gap-1.5 rounded-[5px] border border-line bg-surface px-2.5 py-1.5 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-accent/40 hover:bg-accent-soft hover:text-accent-ink"
                        >
                          <FileText className="h-3 w-3" />
                          查看分析报告
                        </button>
                      )}
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>
        ))}

        {loading && (
          <div className="ra-fade-in flex items-start gap-2.5">
            <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-line bg-surface text-accent">
              <Bot className="h-3.5 w-3.5" />
            </span>
            <div className="flex items-center gap-1.5 rounded-[6px] border border-line bg-surface px-3 py-2 text-[12px] text-muted">
              <span className="ra-pulse">正在使用 Planner 编排多智能体任务拓扑图</span>
              <span className="flex gap-0.5">
                <span className="ra-pulse h-1 w-1 rounded-full bg-accent" />
                <span className="ra-pulse h-1 w-1 rounded-full bg-accent [animation-delay:160ms]" />
                <span className="ra-pulse h-1 w-1 rounded-full bg-accent [animation-delay:320ms]" />
              </span>
            </div>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {!nearBottom && (
        <button
          type="button"
          onClick={() => scrollToBottom()}
          className="ra-fade-in absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full border border-line bg-surface px-3 py-1.5 text-[11px] font-medium text-muted shadow-[var(--shadow-card)] transition-colors hover:border-accent/40 hover:text-accent-ink"
        >
          <ArrowDown className="h-3 w-3" />
          回到底部
        </button>
      )}
    </div>
  );
}
