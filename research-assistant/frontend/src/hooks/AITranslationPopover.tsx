import { useLayoutEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { Sparkles, X, Loader2, MessageSquare } from 'lucide-react';
import type { AssistResponse } from '../contracts/api';

export type AssistMode = 'translate' | 'explain';

export interface AITranslationSelectionRegion {
  pageIndex: number;
  left: number;
  top: number;
  width: number;
  height: number;
}

interface PopoverAnchor {
  left: number;
  width: number;
  maxHeight: number;
  placement: 'above' | 'below';
  top?: number;
  bottom?: number;
}

const VIEWPORT_MARGIN = 12;
const POPOVER_GAP = 8;
const MIN_POPOVER_HEIGHT = 160;

const computeAnchor = (region: AITranslationSelectionRegion): PopoverAnchor | null => {
  if (typeof window === 'undefined') return null;
  const pageLayer = document.querySelector<HTMLElement>(
    `[data-testid="core__page-layer-${region.pageIndex}"]`,
  );
  if (!pageLayer) return null;

  const pageRect = pageLayer.getBoundingClientRect();
  const anchorX = pageRect.left + (region.left / 100) * pageRect.width;
  const anchorTop = pageRect.top + ((region.top + region.height) / 100) * pageRect.height;
  const anchorBottom = pageRect.top + (region.top / 100) * pageRect.height;
  const width = Math.max(160, Math.min(360, window.innerWidth - VIEWPORT_MARGIN * 2));
  const availableBelow = window.innerHeight - anchorTop - VIEWPORT_MARGIN - POPOVER_GAP;
  const availableAbove = anchorBottom - VIEWPORT_MARGIN - POPOVER_GAP;
  const placement: PopoverAnchor['placement'] =
    availableBelow >= MIN_POPOVER_HEIGHT || availableBelow >= availableAbove ? 'below' : 'above';
  const maxHeight = Math.max(120, placement === 'below' ? availableBelow : availableAbove);
  const left = Math.min(
    Math.max(anchorX, VIEWPORT_MARGIN),
    Math.max(VIEWPORT_MARGIN, window.innerWidth - width - VIEWPORT_MARGIN),
  );

  return {
    left,
    width,
    maxHeight,
    placement,
    ...(placement === 'below'
      ? { top: Math.max(VIEWPORT_MARGIN, anchorTop + POPOVER_GAP) }
      : { bottom: Math.max(VIEWPORT_MARGIN, window.innerHeight - anchorBottom + POPOVER_GAP) }),
  };
};

interface AITranslationPopoverProps {
  selectionRegion: AITranslationSelectionRegion;
  selectedText: string;
  mode: AssistMode;
  isLoading: boolean;
  error: string;
  answer: AssistResponse | null;
  onClose: () => void;
  onAskAI?: (text: string) => void;
}

export function AITranslationPopover(props: AITranslationPopoverProps) {
  const { selectionRegion, selectedText, mode, isLoading, error, answer, onClose, onAskAI } = props;
  const { pageIndex, left: selectionLeft, top: selectionTop, width: selectionWidth, height: selectionHeight } = selectionRegion;
  const [anchor, setAnchor] = useState<PopoverAnchor | null>(() => computeAnchor(selectionRegion));

  useLayoutEffect(() => {
    const update = () => setAnchor(computeAnchor({ pageIndex, left: selectionLeft, top: selectionTop, width: selectionWidth, height: selectionHeight }));
    update();

    const pageLayer = document.querySelector<HTMLElement>(`[data-testid="core__page-layer-${pageIndex}"]`);
    const resizeObserver =
      pageLayer && typeof ResizeObserver !== 'undefined' ? new ResizeObserver(update) : null;
    resizeObserver?.observe(pageLayer as HTMLElement);
    window.addEventListener('resize', update);
    window.addEventListener('scroll', update, true);
    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener('resize', update);
      window.removeEventListener('scroll', update, true);
    };
  }, [pageIndex, selectionLeft, selectionTop, selectionWidth, selectionHeight]);

  if (!anchor) return null;

  return createPortal(
    <div
      data-testid="ai-translation-popover"
      role="dialog"
      aria-label={mode === 'translate' ? 'AI 翻译' : 'AI 解释'}
      style={{
        position: 'fixed',
        left: anchor.left,
        width: anchor.width,
        maxHeight: anchor.maxHeight,
        zIndex: 1000,
        ...(anchor.placement === 'below' ? { top: anchor.top } : { bottom: anchor.bottom }),
      }}
      className="ra-fade-up flex flex-col overflow-hidden rounded-[7px] border border-line bg-surface shadow-[var(--shadow-float)]"
    >
      <div className={`flex items-center justify-between px-3 py-2 text-white ${mode === 'translate' ? 'bg-accent' : 'bg-plum'}`}>
        <span className="flex items-center gap-1.5 text-[11px] font-semibold">
          <Sparkles className="h-3 w-3" />
          {mode === 'translate' ? 'AI 翻译' : 'AI 解释'}
          <span className="rounded-[2px] bg-white/15 px-1.5 py-0.5 text-[9.5px] font-normal">第 {pageIndex + 1} 页</span>
        </span>
        <button onClick={onClose} className="text-white/75 transition-colors hover:text-white" aria-label="关闭">
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto bg-surface p-3 text-[12px] leading-6 text-ink-soft">
        <div className="mb-2 rounded-[4px] border border-line bg-surface-muted p-2 font-serif text-[11px] italic leading-5 text-muted">
          “{selectedText.substring(0, 120)}{selectedText.length > 120 ? '…' : ''}”
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
            onAskAI(selectedText);
            onClose();
          }}
          className="flex items-center justify-center gap-2 border-t border-line bg-accent-soft py-2 text-[10.5px] font-semibold text-accent-ink transition-colors hover:bg-accent-soft/70"
        >
          <MessageSquare className="h-3 w-3" />
          针对此段落向 Research Assistant Agent 追问
        </button>
      )}
    </div>,
    document.body,
  );
}
