import { useLayoutEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { X, Loader2, MessageSquare } from 'lucide-react';
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
      className="ra-fade-up flex flex-col overflow-hidden border border-line-strong bg-surface shadow-float"
    >
      <div className="flex items-center justify-between border-b border-line bg-surface-muted px-2.5 py-1.5">
        <span className="flex items-center gap-1.5">
          <span className="instrument-kicker">{mode === 'translate' ? '段落翻译' : '段落解释'}</span>
          <span className="instrument-num text-[11px] text-faint">第 {pageIndex + 1} 页</span>
        </span>
        <button onClick={onClose} className="flex h-5 w-5 items-center justify-center text-muted transition-colors hover:bg-surface-strong hover:text-ink" aria-label="关闭">
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto bg-surface p-3 text-[12.5px] leading-6 text-ink-soft">
        <div className="mb-2 border border-line bg-surface-muted p-2 font-serif text-[11.5px] italic leading-5 text-muted">
          “{selectedText.substring(0, 120)}{selectedText.length > 120 ? '…' : ''}”
        </div>

        {isLoading ? (
          <div className="flex flex-col items-center justify-center gap-2 py-5 text-muted">
            <Loader2 className="h-5 w-5 animate-spin" />
            <span className="ra-pulse text-[12px]">{mode === 'translate' ? '正在翻译论文段落…' : '正在阅读并解释…'}</span>
          </div>
        ) : error ? (
          <p className="border border-flag/25 bg-flag-soft p-2 text-[11.5px] text-flag">{error}</p>
        ) : answer ? (
          <div className="space-y-2">
            <p className="whitespace-pre-wrap text-[12.5px] leading-6 text-ink-soft">{answer.result}</p>
            {answer.bullets && answer.bullets.length > 0 && (
              <ul className="list-disc space-y-1 pl-4 text-[11.5px] leading-5 text-muted">
                {answer.bullets.map((bullet) => <li key={bullet}>{bullet}</li>)}
              </ul>
            )}
            {answer.terms && answer.terms.length > 0 && (
              <div className="flex flex-wrap gap-1 border-t border-line pt-2">
                {answer.terms.map((term) => (
                  <span key={`${term.source}-${term.target}`} className="bg-surface-strong px-1.5 py-0.5 text-[11px] text-muted" title={term.note}>
                    {term.source} → {term.target}
                  </span>
                ))}
              </div>
            )}
            <p className="text-[11px] text-faint">
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
          className="flex items-center justify-center gap-2 border-t border-line bg-accent-soft py-2 text-[11.5px] font-semibold text-accent-ink transition-colors hover:bg-accent-soft/70"
        >
          <MessageSquare className="h-3 w-3" />
          针对此段落向 Research Assistant Agent 追问
        </button>
      )}
    </div>,
    document.body,
  );
}
