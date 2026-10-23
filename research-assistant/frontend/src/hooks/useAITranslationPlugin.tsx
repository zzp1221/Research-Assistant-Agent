import { useState } from 'react';
import {
  highlightPlugin,
  type RenderHighlightTargetProps,
  type RenderHighlightContentProps,
} from '@react-pdf-viewer/highlight';
import { Sparkles, X, Loader2, MessageSquare, Languages, BookOpen } from 'lucide-react';
import type { AssistResponse } from '../contracts/api';
import '@react-pdf-viewer/highlight/lib/styles/index.css';

export type AssistMode = 'translate' | 'explain';

export interface UseAITranslationPluginOptions {
  onAskAI?: (text: string) => void;
  onAssist: (mode: AssistMode, text: string, page: number) => Promise<AssistResponse>;
}

/**
 * AI 划词翻译/解释插件。每次调用都走 /api/uploads/:id/assist，带页码上下文；
 * 不经过 ChatAgent，因此文档内容不会触发代码执行。
 */
export const useAITranslationPlugin = (options: UseAITranslationPluginOptions) => {
  const { onAskAI, onAssist } = options;
  const [mode, setMode] = useState<AssistMode>('translate');
  const [answer, setAnswer] = useState<AssistResponse | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');

  const run = async (nextMode: AssistMode, text: string, page: number) => {
    if (!text.trim()) return;
    setMode(nextMode);
    setIsLoading(true);
    setAnswer(null);
    setError('');
    try {
      const response = await onAssist(nextMode, text.trim(), page);
      setAnswer(response);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '划词请求失败，请稍后重试。');
    } finally {
      setIsLoading(false);
    }
  };

  const reset = () => {
    setAnswer(null);
    setError('');
    setIsLoading(false);
  };

  const highlightPluginInstance = highlightPlugin({
    renderHighlightTarget: (props: RenderHighlightTargetProps) => (
      <div
        style={{
          position: 'absolute',
          left: `${props.selectionRegion.left}%`,
          top: `${props.selectionRegion.top + props.selectionRegion.height}%`,
          transform: 'translate(0, 8px)',
          zIndex: 100,
        }}
        className="ra-fade-up flex items-center gap-0.5 rounded-[6px] border border-line bg-surface p-1 shadow-[var(--shadow-float)]"
      >
        <button
          onClick={() => {
            props.toggle();
            void run('translate', props.selectedText, props.selectionRegion.pageIndex + 1);
          }}
          className="flex items-center gap-1.5 rounded-[4px] px-2.5 py-1.5 text-[11px] font-semibold text-accent-ink transition-colors hover:bg-accent-soft"
        >
          <Languages className="h-3.5 w-3.5" />
          翻译
        </button>
        <button
          onClick={() => {
            props.toggle();
            void run('explain', props.selectedText, props.selectionRegion.pageIndex + 1);
          }}
          className="flex items-center gap-1.5 rounded-[4px] px-2.5 py-1.5 text-[11px] font-semibold text-plum transition-colors hover:bg-plum-soft"
        >
          <BookOpen className="h-3.5 w-3.5" />
          解释
        </button>
      </div>
    ),

    renderHighlightContent: (props: RenderHighlightContentProps) => (
      <div
        style={{
          position: 'absolute',
          left: `${Math.min(props.selectionRegion.left, 55)}%`,
          top: `${props.selectionRegion.top + props.selectionRegion.height}%`,
          transform: 'translate(0, 8px)',
          zIndex: 110,
          width: '360px',
        }}
        className="ra-fade-up flex max-h-[70vh] flex-col overflow-hidden rounded-[7px] border border-line bg-surface shadow-[var(--shadow-float)]"
      >
        <div className={`flex items-center justify-between px-3 py-2 text-white ${mode === 'translate' ? 'bg-accent' : 'bg-plum'}`}>
          <span className="flex items-center gap-1.5 text-[11px] font-semibold">
            <Sparkles className="h-3 w-3" />
            {mode === 'translate' ? 'AI 翻译' : 'AI 解释'}
            <span className="rounded-[2px] bg-white/15 px-1.5 py-0.5 text-[9.5px] font-normal">第 {props.selectionRegion.pageIndex + 1} 页</span>
          </span>
          <button onClick={() => { reset(); props.cancel(); }} className="text-white/75 transition-colors hover:text-white" aria-label="关闭">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="max-h-72 overflow-y-auto bg-surface p-3 text-[12px] leading-6 text-ink-soft">
          <div className="mb-2 rounded-[4px] border border-line bg-surface-muted p-2 font-serif text-[11px] italic leading-5 text-muted">
            “{props.selectedText.substring(0, 120)}{props.selectedText.length > 120 ? '…' : ''}”
          </div>

          {isLoading ? (
            <div className="flex flex-col items-center justify-center gap-2 py-5 text-accent">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="ra-pulse text-[11px]">{mode === 'translate' ? '正在翻译论文段落…' : '正在阅读并解释…'}</span>
            </div>
          ) : error ? (
            <p className="rounded-[4px] border border-flag/25 bg-flag-soft p-2 text-[11px] text-flag">{error}</p>
          ) : answer ? (
            <div className="space-y-2">
              <p className="whitespace-pre-wrap text-[12.5px] leading-6 text-ink-soft">{answer.result}</p>
              {answer.bullets && answer.bullets.length > 0 && (
                <ul className="list-disc space-y-1 pl-4 text-[11px] leading-5 text-muted">
                  {answer.bullets.map((bullet) => <li key={bullet}>{bullet}</li>)}
                </ul>
              )}
              {answer.terms && answer.terms.length > 0 && (
                <div className="flex flex-wrap gap-1 border-t border-line pt-2">
                  {answer.terms.map((term) => (
                    <span key={`${term.source}-${term.target}`} className="rounded-[3px] bg-paper-deep px-1.5 py-0.5 text-[9.5px] text-muted" title={term.note}>
                      {term.source} → {term.target}
                    </span>
                  ))}
                </div>
              )}
              <p className="text-[9.5px] text-faint">
                {answer.cached ? '来自缓存 · ' : ''}{answer.model}
              </p>
            </div>
          ) : null}
        </div>

        {onAskAI && (
          <button
            onClick={() => {
              onAskAI(props.selectedText);
              props.cancel();
            }}
            className="flex items-center justify-center gap-2 border-t border-line bg-accent-soft py-2 text-[10.5px] font-semibold text-accent-ink transition-colors hover:bg-accent-soft/70"
          >
            <MessageSquare className="h-3 w-3" />
            针对此段落向 Research Assistant Agent 追问
          </button>
        )}
      </div>
    ),
  });

  return highlightPluginInstance;
};
