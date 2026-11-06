import { useLayoutEffect, useRef, useState, type RefObject } from 'react';
import { computePopoverLayout, readVisibleReaderRect, type PopoverLayout } from './translationPopoverLayout';
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

interface AITranslationPopoverProps {
  readerRef: RefObject<HTMLDivElement | null>;
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
  const { readerRef, selectionRegion, selectedText, mode, isLoading, error, answer, onClose, onAskAI } = props;
  const { pageIndex, left: selectionLeft, top: selectionTop, width: selectionWidth, height: selectionHeight } = selectionRegion;
  const popoverRef = useRef<HTMLDivElement>(null);
  const headerRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const footerRef = useRef<HTMLButtonElement>(null);
  const [anchor, setAnchor] = useState<PopoverLayout | null>(null);
  const [measuredWidth, setMeasuredWidth] = useState(360);

  useLayoutEffect(() => {
    const root = readerRef.current;
    if (!root) return;
    const visualViewport = window.visualViewport;
    let frame = 0;
    let disposed = false;
    const observed = new Set<Element>();
    const observe = (element: Element | null) => {
      if (element && !observed.has(element)) { observed.add(element); resizeObserver?.observe(element); }
    };
    const update = () => {
      frame = 0;
      if (disposed) return;
      const body = root.querySelector<HTMLElement>('.rpv-default-layout__body');
      const pageLayer = root.querySelector<HTMLElement>(`[data-testid="core__page-layer-${pageIndex}"]`);
      observe(body); observe(pageLayer);
      let next: PopoverLayout | null = null;
      if (root.isConnected && body && pageLayer) {
        const visible = readVisibleReaderRect(root, body);
        if (visible) {
          const availableWidth = visible.right - visible.left;
          const width = Math.min(360, availableWidth - 2 * Math.min(8, availableWidth / 10));
          setMeasuredWidth((previous) => previous === width ? previous : width);
        }
        const page = pageLayer.getBoundingClientRect();
        const selection = {
          left: page.left + selectionLeft / 100 * page.width,
          top: page.top + selectionTop / 100 * page.height,
          right: page.left + (selectionLeft + selectionWidth) / 100 * page.width,
          bottom: page.top + (selectionTop + selectionHeight) / 100 * page.height,
        };
        const chromeHeight = (headerRef.current?.getBoundingClientRect().height ?? 32) + (footerRef.current?.getBoundingClientRect().height ?? 0) + 2;
        const desiredHeight = chromeHeight + (contentRef.current?.scrollHeight ?? 160);
        if (visible) next = computePopoverLayout(visible, selection, desiredHeight, chromeHeight);
      }
      setAnchor((previous) => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
    };
    const schedule = () => { if (!disposed && !frame) frame = requestAnimationFrame(update); };
    const resizeObserver = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(schedule);
    // Ancestor sizes also detect splitter movement when the reader itself keeps
    // its width but shifts in viewport coordinates.
    for (let element: HTMLElement | null = root; element; element = element.parentElement) observe(element);
    observe(popoverRef.current);
    observe(headerRef.current);
    observe(footerRef.current);
    const mutationObserver = new MutationObserver(schedule);
    mutationObserver.observe(root, { childList: true, subtree: true, attributes: true, attributeFilter: ['class', 'style', 'hidden'] });
    // Switching the mobile pane can hide an ancestor without resizing it.
    // Also detect removal of the reader while the portal is still mounted.
    for (let element = root.parentElement; element; element = element.parentElement) {
      mutationObserver.observe(element, { childList: true, attributes: true, attributeFilter: ['class', 'style', 'hidden', 'data-mobile-pane'] });
    }
    window.addEventListener('resize', schedule);
    window.addEventListener('scroll', schedule, true);
    visualViewport?.addEventListener('resize', schedule);
    visualViewport?.addEventListener('scroll', schedule);
    update();
    return () => {
      disposed = true;
      cancelAnimationFrame(frame);
      resizeObserver?.disconnect(); mutationObserver.disconnect();
      window.removeEventListener('resize', schedule);
      window.removeEventListener('scroll', schedule, true);
      visualViewport?.removeEventListener('resize', schedule);
      visualViewport?.removeEventListener('scroll', schedule);
    };
  }, [readerRef, pageIndex, selectionLeft, selectionTop, selectionWidth, selectionHeight, selectedText, mode, answer, error, isLoading, onAskAI]);

  return createPortal(
    <div
      ref={popoverRef}
      data-testid="ai-translation-popover"
      role="dialog"
      aria-label={mode === 'translate' ? 'AI 翻译' : 'AI 解释'}
      style={{
        position: 'fixed',
        boxSizing: 'border-box',
        left: anchor?.left ?? 0,
        top: anchor?.top ?? 0,
        // Keep measuring at the reader width even while hidden. Resetting to
        // 360px would change wrapped controls and could alternate show/hide.
        width: anchor?.width ?? measuredWidth,
        height: anchor?.height,
        maxHeight: anchor?.height,
        visibility: anchor ? 'visible' : 'hidden',
        pointerEvents: anchor ? 'auto' : 'none',
        zIndex: 1000,
      }}
      className="flex min-w-0 flex-col overflow-hidden border border-line-strong bg-surface shadow-float"
    >
      <div ref={headerRef} className="flex min-w-0 shrink-0 items-center justify-between gap-1 border-b border-line bg-surface-muted px-2.5 py-1.5">
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className="instrument-kicker">{mode === 'translate' ? '段落翻译' : '段落解释'}</span>
          <span className="instrument-num text-[11px] text-faint">第 {pageIndex + 1} 页</span>
        </span>
        <button onClick={onClose} className="flex h-5 w-5 shrink-0 items-center justify-center text-muted transition-colors hover:bg-surface-strong hover:text-ink" aria-label="关闭">
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      <div ref={contentRef} className="min-h-0 min-w-0 flex-1 overflow-y-auto [overflow-wrap:anywhere] bg-surface p-3 text-[12.5px] leading-6 text-ink-soft">
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
          ref={footerRef}
          onClick={() => {
            onAskAI(selectedText);
            onClose();
          }}
          className="flex min-w-0 shrink-0 items-center justify-center gap-2 border-t border-line bg-accent-soft py-2 text-[11.5px] font-semibold text-accent-ink transition-colors hover:bg-accent-soft/70"
        >
          <MessageSquare className="h-3 w-3" />
          针对此段落向 Research Assistant Agent 追问
        </button>
      )}
    </div>,
    document.body,
  );
}
