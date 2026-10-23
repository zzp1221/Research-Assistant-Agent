import { useCallback, useEffect, useRef, useState } from 'react';

const LEFT_WIDTH_KEY = 'ra.leftPanelWidth';
const SIDEBAR_WIDTH_KEY = 'ra.sidebarWidth';
const DEFAULT_LEFT_WIDTH = 33;
const DEFAULT_SIDEBAR_WIDTH = 440;

const readNumber = (key: string, fallback: number) => {
  if (typeof window === 'undefined') return fallback;
  const raw = window.localStorage.getItem(key);
  const value = raw ? Number(raw) : NaN;
  return Number.isFinite(value) ? value : fallback;
};

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export function useResearchAssistantLayoutState() {
  const [leftPanelWidth, setLeftPanelWidth] = useState(() => clamp(readNumber(LEFT_WIDTH_KEY, DEFAULT_LEFT_WIDTH), 22, 62));
  const [isResizing, setIsResizing] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(() => clamp(readNumber(SIDEBAR_WIDTH_KEY, DEFAULT_SIDEBAR_WIDTH), 320, 760));
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  const frameRef = useRef<number | null>(null);

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      window.localStorage.setItem(LEFT_WIDTH_KEY, String(Math.round(leftPanelWidth * 10) / 10));
    }, 180);
    return () => window.clearTimeout(timeout);
  }, [leftPanelWidth]);

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      window.localStorage.setItem(SIDEBAR_WIDTH_KEY, String(Math.round(sidebarWidth)));
    }, 180);
    return () => window.clearTimeout(timeout);
  }, [sidebarWidth]);

  useEffect(() => {
    if (!isResizing && !isResizingSidebar) return;

    const applyPointerPosition = (event: PointerEvent) => {
      if (isResizing) {
        const newWidth = (event.clientX / window.innerWidth) * 100;
        setLeftPanelWidth(clamp(newWidth, 22, 62));
      } else if (isResizingSidebar) {
        const newSidebarWidth = window.innerWidth - event.clientX;
        setSidebarWidth(clamp(newSidebarWidth, 320, Math.min(760, window.innerWidth * 0.62)));
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
  }, [isResizing, isResizingSidebar]);

  const resetLeftPanel = useCallback(() => setLeftPanelWidth(DEFAULT_LEFT_WIDTH), []);
  const resetSidebar = useCallback(() => setSidebarWidth(DEFAULT_SIDEBAR_WIDTH), []);

  return {
    leftPanelWidth,
    isResizing,
    sidebarWidth,
    isResizingSidebar,
    startResizingLeftPanel: () => setIsResizing(true),
    startResizingSidebar: () => setIsResizingSidebar(true),
    resetLeftPanel,
    resetSidebar,
  };
}
