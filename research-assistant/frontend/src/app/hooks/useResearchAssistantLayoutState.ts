import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  chatWidthFromPointer,
  clamp,
  computeEffectiveLayout,
  defaultChatWidthPx,
  detailsWidthFromPointer,
  estimateContainerWidth,
  layoutMetrics,
  maxChatWidthPx,
  maxDetailsWidthPx,
  parseChatWidthCache,
  parseDetailsWidthCache,
  toLegacyChatRatio,
  type LayoutBounds,
  type LayoutPreferences,
} from './workspaceLayout';

/** Preferred chat width in CSS pixels (new key, never a ratio). */
const CHAT_WIDTH_KEY = 'ra.chatWidthPx';
/** Preferred details width in CSS pixels (new key). */
const DETAILS_WIDTH_KEY = 'ra.detailsWidthPx';
/** Legacy ratio cache: 24 means 24% of the container, not 24px. */
const LEFT_WIDTH_KEY = 'ra.leftPanelWidth';
/** Legacy details cache in pixels (old clamp was 320–760, floor is now 360). */
const SIDEBAR_WIDTH_KEY = 'ra.sidebarWidth';
const STORAGE_DELAY_MS = 180;

const readStored = (key: string): string | null => {
  if (typeof window === 'undefined') return null;
  try {
    return window.localStorage.getItem(key);
  } catch {
    // Private mode / disabled storage must never break the workbench.
    return null;
  }
};

const writeStored = (key: string, value: string): void => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Ignore quota and permission errors.
  }
};

const readViewportWidth = (): number => {
  if (typeof window === 'undefined') return 0;
  return Math.max(0, Number(window.innerWidth) || 0);
};

const readPreferences = (containerWidth: number): LayoutPreferences => {
  const storedPixels = readStored(CHAT_WIDTH_KEY);
  const pixelValue = storedPixels?.trim() ? Number(storedPixels) : Number.NaN;
  const chat =
    (Number.isFinite(pixelValue) && pixelValue > 0
      ? clamp(pixelValue, layoutMetrics.chatMinPx, layoutMetrics.chatMaxPx) : null) ??
    parseChatWidthCache(readStored(LEFT_WIDTH_KEY), containerWidth) ??
    defaultChatWidthPx(containerWidth);
  const details =
    parseDetailsWidthCache(readStored(DETAILS_WIDTH_KEY)) ??
    parseDetailsWidthCache(readStored(SIDEBAR_WIDTH_KEY)) ??
    layoutMetrics.detailsDefaultPx;
  return { chatWidthPx: chat, detailsWidthPx: details };
};

/**
 * Workbench layout state: container measurement, breakpoints, chat collapse and
 * the two drag handles.
 *
 * The hook keeps the *user preference* (`ra.chatWidthPx` / `ra.detailsWidthPx`)
 * separate from the *effective* widths derived from the measured container, so
 * shrinking the window temporarily never overwrites the wide-screen preference
 * and re-enlarging restores it.
 */
export function useResearchAssistantLayoutState() {
  const [viewportWidth, setViewportWidth] = useState(readViewportWidth);
  const [containerWidth, setContainerWidth] = useState(() => estimateContainerWidth(readViewportWidth()));
  const [preferences, setPreferences] = useState<LayoutPreferences>(() =>
    readPreferences(estimateContainerWidth(readViewportWidth())),
  );
  const [chatCollapsed, setChatCollapsed] = useState(false);
  const [detailsOpen, setDetailsOpenState] = useState(false);
  const [isResizing, setIsResizing] = useState(false);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);

  const rowRef = useRef<HTMLDivElement | null>(null);
  const [containerNode, setContainerNode] = useState<HTMLDivElement | null>(null);
  const containerRef = useCallback((node: HTMLDivElement | null) => {
    rowRef.current = node;
    setContainerNode(node);
  }, []);
  const frameRef = useRef<number | null>(null);
  const containerWidthRef = useRef(containerWidth);
  const prefersLegacyWriteRef = useRef(false);

  const effective = useMemo(
    () => computeEffectiveLayout(containerWidth, viewportWidth, preferences, { detailsOpen, chatCollapsed }),
    [containerWidth, viewportWidth, preferences, detailsOpen, chatCollapsed],
  );
  const effectiveRef = useRef(effective);

  useEffect(() => {
    containerWidthRef.current = containerWidth;
  }, [containerWidth]);

  useEffect(() => {
    effectiveRef.current = effective;
  }, [effective]);

  // Measure the workbench row (rail, margins, borders and handles are already
  // excluded from it) and keep following container changes.
  useEffect(() => {
    const node = containerNode;
    const applyWidth = (width: number) => {
      if (!Number.isFinite(width) || width <= 0) return;
      const next = Math.round(width);
      setContainerWidth((prev) => (prev === next ? prev : next));
    };

    if (node && typeof node.getBoundingClientRect === 'function') {
      const rect = node.getBoundingClientRect();
      if (rect && rect.width > 0) applyWidth(rect.width);
    }

    if (!node || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      const width = entry?.contentRect?.width ?? entry?.borderBoxSize?.[0]?.inlineSize ?? 0;
      applyWidth(width);
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, [containerNode]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const handleResize = () => {
      setViewportWidth(readViewportWidth());
      const width = rowRef.current?.getBoundingClientRect().width || estimateContainerWidth(readViewportWidth());
      setContainerWidth(Math.round(width));
      document.documentElement.style.setProperty('--workbench-viewport-height', `${window.visualViewport?.height ?? window.innerHeight}px`);
      // The shell starts at the layout viewport origin. When the keyboard pans
      // the visual viewport, its visible bottom also includes offsetTop.
      document.documentElement.style.setProperty('--workbench-viewport-bottom', `${(window.visualViewport?.offsetTop ?? 0) + (window.visualViewport?.height ?? window.innerHeight)}px`);
      document.documentElement.style.setProperty('--workbench-viewport-top', `${window.visualViewport?.offsetTop ?? 0}px`);
    };
    handleResize();
    window.addEventListener('resize', handleResize);
    window.visualViewport?.addEventListener('resize', handleResize);
    window.visualViewport?.addEventListener('scroll', handleResize);
    return () => {
      window.removeEventListener('resize', handleResize);
      window.visualViewport?.removeEventListener('resize', handleResize);
      window.visualViewport?.removeEventListener('scroll', handleResize);
      document.documentElement.style.removeProperty('--workbench-viewport-height');
      document.documentElement.style.removeProperty('--workbench-viewport-bottom');
      document.documentElement.style.removeProperty('--workbench-viewport-top');
    };
  }, []);

  // Persist the *preference* only; effective widths are recomputed on resize.
  useEffect(() => {
    const timeout = window.setTimeout(() => {
      writeStored(CHAT_WIDTH_KEY, String(Math.round(preferences.chatWidthPx)));
      writeStored(DETAILS_WIDTH_KEY, String(Math.round(preferences.detailsWidthPx)));
      if (!prefersLegacyWriteRef.current) return;
      // Compatibility mirror for older builds reading the ratio cache.
      writeStored(LEFT_WIDTH_KEY, String(toLegacyChatRatio(preferences.chatWidthPx, containerWidthRef.current)));
      writeStored(SIDEBAR_WIDTH_KEY, String(Math.round(preferences.detailsWidthPx)));
    }, STORAGE_DELAY_MS);
    return () => window.clearTimeout(timeout);
  }, [preferences]);

  useEffect(() => {
    if (!isResizing && !isResizingSidebar) return;

    const readBounds = (): LayoutBounds => {
      const node = rowRef.current;
      if (node && typeof node.getBoundingClientRect === 'function') {
        const rect = node.getBoundingClientRect();
        if (rect && rect.width > 0) return { left: rect.left, width: rect.width };
      }
      return { left: 0, width: containerWidthRef.current };
    };

    const applyPointerPosition = (event: PointerEvent) => {
      const bounds = readBounds();
      if (isResizing) {
        const dockedDetails =
          detailsOpen && effectiveRef.current.detailsMode === 'docked' ? effectiveRef.current.detailsWidthPx : 0;
        const next = chatWidthFromPointer(event.clientX, bounds, dockedDetails);
        setPreferences((prev) => (prev.chatWidthPx === next ? prev : { ...prev, chatWidthPx: next }));
      } else if (isResizingSidebar) {
        const next = detailsWidthFromPointer(event.clientX, bounds, effectiveRef.current.chatWidthPx);
        setPreferences((prev) =>
          prev.detailsWidthPx === next ? prev : { ...prev, detailsWidthPx: next },
        );
      }
    };

    const handlePointerMove = (event: PointerEvent) => {
      if (frameRef.current !== null) return;
      frameRef.current = window.requestAnimationFrame(() => {
        frameRef.current = null;
        applyPointerPosition(event);
      });
    };

    const stopResizing = () => {
      if (frameRef.current !== null) {
        window.cancelAnimationFrame(frameRef.current);
        frameRef.current = null;
      }
      setIsResizing(false);
      setIsResizingSidebar(false);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };

    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', stopResizing);
    window.addEventListener('pointercancel', stopResizing);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    return () => {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', stopResizing);
      window.removeEventListener('pointercancel', stopResizing);
      if (frameRef.current !== null) {
        window.cancelAnimationFrame(frameRef.current);
        frameRef.current = null;
      }
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
  }, [isResizing, isResizingSidebar, detailsOpen]);

  const startResizingLeftPanel = useCallback(() => {
    prefersLegacyWriteRef.current = true;
    setIsResizingSidebar(false);
    setIsResizing(true);
  }, []);

  const startResizingSidebar = useCallback(() => {
    prefersLegacyWriteRef.current = true;
    setIsResizing(false);
    setIsResizingSidebar(true);
  }, []);

  /** Keyboard/toolbar nudge: preference only, the container clamps the result. */
  const adjustLeftPanel = useCallback((deltaPx: number) => {
    if (!Number.isFinite(deltaPx) || deltaPx === 0) return;
    prefersLegacyWriteRef.current = true;
    const current = effectiveRef.current;
    const detailWidth = detailsOpen && current.detailsMode === 'docked' ? current.detailsWidthPx : 0;
    setPreferences((prev) => ({
      ...prev,
      chatWidthPx: clamp(
        Math.round(current.chatWidthPx + deltaPx),
        layoutMetrics.chatMinPx,
        maxChatWidthPx(current.containerWidth, detailWidth),
      ),
    }));
  }, [detailsOpen]);

  const adjustSidebar = useCallback((deltaPx: number) => {
    if (!Number.isFinite(deltaPx) || deltaPx === 0) return;
    prefersLegacyWriteRef.current = true;
    const current = effectiveRef.current;
    setPreferences((prev) => ({
      ...prev,
      detailsWidthPx: clamp(
        Math.round(current.detailsWidthPx + deltaPx),
        layoutMetrics.detailsMinPx,
        detailsOpen && current.detailsMode === 'docked'
          ? maxDetailsWidthPx(current.containerWidth, current.chatWidthPx)
          : layoutMetrics.detailsMaxPx,
      ),
    }));
  }, [detailsOpen]);

  const resetLeftPanel = useCallback(() => {
    prefersLegacyWriteRef.current = true;
    setPreferences((prev) => ({
      ...prev,
      chatWidthPx: defaultChatWidthPx(containerWidthRef.current),
    }));
  }, []);

  const resetSidebar = useCallback(() => {
    prefersLegacyWriteRef.current = true;
    setPreferences((prev) => ({ ...prev, detailsWidthPx: layoutMetrics.detailsDefaultPx }));
  }, []);

  const toggleChat = useCallback(() => setChatCollapsed((prev) => !prev), []);

  /** Shell syncs this with "a graph node is selected". */
  const setDetailsOpen = useCallback((open: boolean) => {
    setDetailsOpenState(Boolean(open));
  }, []);

  return {
    /** Attach to the workbench row so the hook can measure the real width. */
    containerRef,
    containerWidth: effective.containerWidth,
    layoutMode: effective.layoutMode,
    detailsMode: effective.detailsMode,
    detailsOpen,
    setDetailsOpen,
    chatCollapsed,
    toggleChat,
    /** Effective chat width in pixels (row CSS variable `--chat-width`). */
    leftPanelPixels: effective.chatWidthPx,
    leftPanelMax: maxChatWidthPx(effective.containerWidth, detailsOpen && effective.detailsMode === 'docked' ? effective.detailsWidthPx : 0),
    /** Effective chat width as a percentage of the container (legacy API). */
    leftPanelWidth: effective.chatWidthPercent,
    /** Effective details width in pixels. */
    sidebarWidth: effective.detailsWidthPx,
    sidebarMax: detailsOpen && effective.detailsMode === 'docked'
      ? maxDetailsWidthPx(effective.containerWidth, effective.chatWidthPx) : layoutMetrics.detailsMaxPx,
    isResizing,
    isResizingSidebar,
    startResizingLeftPanel,
    startResizingSidebar,
    adjustLeftPanel,
    adjustSidebar,
    resetLeftPanel,
    resetSidebar,
  };
}

export type ResearchAssistantLayoutState = ReturnType<typeof useResearchAssistantLayoutState>;
