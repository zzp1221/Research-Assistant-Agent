import { useState } from 'react';
import {
  highlightPlugin,
  type RenderHighlightTargetProps,
  type RenderHighlightContentProps,
} from '@react-pdf-viewer/highlight';
import { Languages, BookOpen } from 'lucide-react';
import type { AssistResponse } from '../contracts/api';
import { AITranslationPopover, type AssistMode } from './AITranslationPopover';
import '@react-pdf-viewer/highlight/lib/styles/index.css';

export type { AssistMode } from './AITranslationPopover';

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
        className="ra-fade-up flex items-center gap-0.5 rounded-[2px] border border-line bg-surface p-1 shadow-[var(--shadow-float)]"
      >
        <button
          onClick={() => {
            props.toggle();
            void run('translate', props.selectedText, props.selectionRegion.pageIndex + 1);
          }}
          className="flex items-center gap-1.5 rounded-[2px] px-2.5 py-1.5 text-[11px] font-semibold text-accent-ink transition-colors hover:bg-accent-soft"
        >
          <Languages className="h-3.5 w-3.5" />
          翻译
        </button>
        <button
          onClick={() => {
            props.toggle();
            void run('explain', props.selectedText, props.selectionRegion.pageIndex + 1);
          }}
          className="flex items-center gap-1.5 rounded-[2px] px-2.5 py-1.5 text-[11px] font-semibold text-plum transition-colors hover:bg-plum-soft"
        >
          <BookOpen className="h-3.5 w-3.5" />
          解释
        </button>
      </div>
    ),

    renderHighlightContent: (props: RenderHighlightContentProps) => (
      <AITranslationPopover
        selectionRegion={props.selectionRegion}
        selectedText={props.selectedText}
        mode={mode}
        isLoading={isLoading}
        error={error}
        answer={answer}
        onClose={() => {
          reset();
          props.cancel();
        }}
        onAskAI={onAskAI}
      />
    ),
  });

  return highlightPluginInstance;
};