import { useCallback, useMemo, useRef } from 'react';
import type { Plugin, PluginFunctions } from '@react-pdf-viewer/core';

export interface PdfPageControl {
  /** Plugin that captures the viewer's imperative page functions. */
  plugin: Plugin;
  /**
   * Jump to a one-based page. Returns false when the viewer has not installed
   * its plugin functions yet (before the first document mount).
   */
  jumpToPage: (oneBasedPage: number) => boolean;
  /** Zoom without remounting the viewer. Returns false before the first mount. */
  zoomTo: (scale: number) => boolean;
  /** Whether the viewer has registered its plugin functions. */
  isReady: () => boolean;
  getPagesContainer: () => HTMLElement | null;
  isPageVisible: (oneBasedPage: number) => boolean;
}

/**
 * Reading page control for an already mounted viewer.
 *
 * `@react-pdf-viewer/core` only exposes `jumpToPage` through the plugin
 * functions handed to `Plugin.install`, so the reader keeps a stable plugin
 * instance and calls into it instead of remounting the viewer (a remount would
 * drop the reader to page one on every chat or layout update).
 *
 * The plugin instance is memoised with an empty dependency list: re-creating it
 * would uninstall the captured functions and lose the mounted reader.
 */
export const usePdfPageControl = (): PdfPageControl => {
  const functionsRef = useRef<PluginFunctions | null>(null);

  const plugin = useMemo<Plugin>(
    () => ({
      install: (functions: PluginFunctions) => {
        functionsRef.current = functions;
      },
      uninstall: () => {
        functionsRef.current = null;
      },
    }),
    [],
  );

  const isReady = useCallback(() => functionsRef.current !== null, []);
  const getPagesContainer = useCallback(() => functionsRef.current?.getPagesContainer?.() ?? null, []);
  const isPageVisible = useCallback((oneBasedPage: number) => {
    const container = getPagesContainer();
    // Lightweight hosts may omit DOM functions; the viewer event is authoritative.
    if (!container) return functionsRef.current !== null;
    const layer = container.querySelector<HTMLElement>(`[data-testid="core__page-layer-${oneBasedPage - 1}"]`);
    if (!layer) return false;
    const bounds = container.getBoundingClientRect();
    const page = layer.getBoundingClientRect();
    return bounds.width > 0 && bounds.height > 0
      && page.bottom > bounds.top && page.top < bounds.bottom
      && page.right > bounds.left && page.left < bounds.right;
  }, [getPagesContainer]);

  const jumpToPage = useCallback((oneBasedPage: number) => {
    const functions = functionsRef.current;
    if (!functions) return false;
    const container = functions.getPagesContainer?.();
    if (container && (container.clientWidth === 0 || container.clientHeight === 0)) return false;
    const requested = Number.isFinite(oneBasedPage) ? Math.floor(oneBasedPage) : 1;
    const pageIndex = Math.max(0, requested - 1);
    void functions.jumpToPage(pageIndex);
    return true;
  }, []);

  const zoomTo = useCallback((scale: number) => {
    const functions = functionsRef.current;
    if (!functions || !Number.isFinite(scale) || scale <= 0) return false;
    functions.zoom(scale);
    return true;
  }, []);

  return { plugin, jumpToPage, zoomTo, isReady, getPagesContainer, isPageVisible };
};
