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
  if (role === 'user') return '你';
  if (role === 'system') return '系统';
  return '助手';
};

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
      <div className="flex h-8 shrink-0 items-center justify-between border-b border-line bg-surface-muted px-3">
        <span className="instrument-kicker">运行记录</span>
        <span className="instrument-num text-[11px] text-faint">{chatHistory.length} 条</span>
      </div>
      <div ref={scrollRef} onScroll={handleScroll} className="h-[calc(100%-2rem)] overflow-y-auto">
        {!isLoggedIn && (
          <div className="border-b border-line bg-surface px-3 py-2 text-[12.5px] leading-5 text-muted">
            游客模式：可直接对话和新建临时会话，刷新后不保留记录；登录后可持久保存。
          </div>
        )}

        {chatHistory.map((msg, i) => {
          const isSystem = msg.role === 'system';
          return (
            <div
              key={i}
              className={`ra-fade-up grid grid-cols-[30px_38px_minmax(0,1fr)] gap-2 border-b border-line/70 px-3 py-2.5 ${isSystem ? 'bg-surface-muted/45' : 'bg-surface'}`}
            >
              <span className="instrument-num pt-0.5 text-[11px] text-faint">{String(i + 1).padStart(2, '0')}</span>
              <span className={`pt-0.5 text-[12px] ${msg.role === 'user' ? 'font-semibold text-ink' : 'font-medium text-faint'}`}>
                {roleLabel(msg.role)}
              </span>
              <div className="min-w-0">
                <div
                  className={`prose prose-sm max-w-none text-[13px] leading-[1.65] ${isSystem ? 'text-ink-soft' : 'text-ink'} prose-p:my-1 prose-p:leading-[1.65] prose-headings:font-display prose-headings:font-semibold prose-headings:text-ink prose-strong:text-ink prose-code:rounded-[2px] prose-code:bg-surface-strong prose-code:px-1 prose-code:py-0.5 prose-code:text-[12px] prose-code:font-normal prose-code:text-accent-ink prose-pre:rounded-[2px] prose-pre:border prose-pre:border-white/10 prose-pre:bg-[#12181d] prose-pre:text-[12px] prose-pre:text-[#dfe6ea] prose-a:text-accent prose-a:underline-offset-2`}
                >
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.text}</ReactMarkdown>
                </div>

                {msg.actions && msg.actions.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {msg.actions.includes('open_pdf') && (
                      <button
                        onClick={pdfActions.onOpenPdf}
                        className="inline-flex h-6 items-center gap-1.5 border border-line-strong bg-surface px-2 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-accent hover:bg-accent-soft hover:text-accent-ink"
                      >
                        <FileText className="h-3 w-3" />
                        打开论文原文
                      </button>
                    )}
                    {msg.actions.includes('close_pdf') && (
                      <button
                        onClick={pdfActions.onClosePdf}
                        className="inline-flex h-6 items-center gap-1.5 border border-line bg-surface px-2 text-[11.5px] font-medium text-muted transition-colors hover:border-line-strong hover:bg-surface-muted hover:text-ink"
                      >
                        <X className="h-3 w-3" />
                        关闭阅读器
                      </button>
                    )}
                    {msg.actions.includes('view_plot') && msg.taskId && (
                      <button
                        onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'plot')}
                        className="inline-flex h-6 items-center gap-1.5 border border-line-strong bg-surface px-2 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-accent hover:bg-accent-soft hover:text-accent-ink"
                      >
                        <Maximize2 className="h-3 w-3" />
                        查看生成的图表
                      </button>
                    )}
                    {msg.actions.includes('view_report') && msg.taskId && (
                      <button
                        onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'report')}
                        className="inline-flex h-6 items-center gap-1.5 border border-line-strong bg-surface px-2 text-[11.5px] font-medium text-ink-soft transition-colors hover:border-accent hover:bg-accent-soft hover:text-accent-ink"
                      >
                        <FileText className="h-3 w-3" />
                        查看分析报告
                      </button>
                    )}
                  </div>
                )}
              </div>
            </div>
          );
        })}

        {loading && (
          <div className="ra-fade-in grid grid-cols-[30px_38px_minmax(0,1fr)] gap-2 border-b border-line/70 bg-surface-muted/45 px-3 py-2.5">
            <span className="instrument-num pt-0.5 text-[11px] text-faint">··</span>
            <span className="pt-0.5 text-[12px] font-medium text-faint">计划</span>
            <div className="flex items-center gap-2 text-[12.5px] text-muted">
              <span className="ra-pulse h-1.5 w-1.5 bg-warn" aria-hidden="true" />
              正在编排执行图：解析意图、绑定上下文、生成节点依赖
            </div>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {!nearBottom && (
        <button
          type="button"
          onClick={() => scrollToBottom()}
          className="ra-fade-in absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 border border-line-strong bg-surface px-3 py-1.5 text-[11.5px] font-medium text-muted shadow-float transition-colors hover:text-accent-ink"
        >
          <ArrowDown className="h-3 w-3" />
          回到底部
        </button>
      )}
    </div>
  );
}
