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
        className="flex items-center gap-1 rounded-lg border border-blue-200 bg-white p-1 shadow-2xl animate-in fade-in zoom-in duration-200"
      >
        <button
          onClick={() => {
            props.toggle();
            void run('translate', props.selectedText, props.selectionRegion.pageIndex + 1);
          }}
          className="flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-semibold text-blue-600 transition-all hover:bg-blue-50"
        >
          <Languages className="h-3.5 w-3.5" />
          翻译
        </button>
        <button
          onClick={() => {
            props.toggle();
            void run('explain', props.selectedText, props.selectionRegion.pageIndex + 1);
          }}
          className="flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-semibold text-violet-600 transition-all hover:bg-violet-50"
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
        className="flex max-h-[70vh] flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl animate-in slide-in-from-top-2 duration-300"
      >
        <div className={`flex items-center justify-between px-3 py-2 text-white ${mode === 'translate' ? 'bg-blue-600' : 'bg-violet-600'}`}>
          <span className="flex items-center gap-1 text-xs font-bold">
            <Sparkles className="h-3 w-3" />
            {mode === 'translate' ? 'AI 翻译' : 'AI 解释'}
            <span className="rounded bg-white/20 px-1.5 py-0.5 text-[10px] font-medium">第 {props.selectionRegion.pageIndex + 1} 页</span>
          </span>
          <button onClick={() => { reset(); props.cancel(); }} className="text-white/80 transition-colors hover:text-white" aria-label="关闭">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="max-h-72 overflow-y-auto bg-white p-3 text-sm leading-relaxed text-gray-700">
          <div className="mb-2 rounded-lg border border-gray-100 bg-gray-50 p-2 text-[11px] italic text-gray-500">
            "{props.selectedText.substring(0, 120)}{props.selectedText.length > 120 ? '...' : ''}"
          </div>

          {isLoading ? (
            <div className="flex flex-col items-center justify-center gap-2 py-4 text-blue-500">
              <Loader2 className="h-6 w-6 animate-spin" />
              <span className="animate-pulse text-xs">{mode === 'translate' ? '正在翻译论文段落...' : '正在阅读并解释...'}</span>
            </div>
          ) : error ? (
            <p className="rounded-lg border border-rose-200 bg-rose-50 p-2 text-xs text-rose-700">{error}</p>
          ) : answer ? (
            <div className="space-y-2">
              <p className="whitespace-pre-wrap text-[13px] text-gray-800">{answer.result}</p>
              {answer.bullets && answer.bullets.length > 0 && (
                <ul className="list-disc space-y-1 pl-4 text-[12px] text-gray-600">
                  {answer.bullets.map((bullet) => <li key={bullet}>{bullet}</li>)}
                </ul>
              )}
              {answer.terms && answer.terms.length > 0 && (
                <div className="flex flex-wrap gap-1 border-t border-gray-100 pt-2">
                  {answer.terms.map((term) => (
                    <span key={`${term.source}-${term.target}`} className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-600" title={term.note}>
                      {term.source} → {term.target}
                    </span>
                  ))}
                </div>
              )}
              <p className="text-[10px] text-gray-400">
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
            className="flex items-center justify-center gap-2 border-t border-blue-100 bg-blue-50 py-2 text-xs font-semibold text-blue-700 transition-colors hover:bg-blue-100"
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
