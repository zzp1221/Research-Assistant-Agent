import { useEffect } from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useResearchAssistantLayoutState } from '../../src/app/hooks/useResearchAssistantLayoutState';
import { estimateContainerWidth, layoutMetrics } from '../../src/app/hooks/workspaceLayout';

type ObserverEntry = { contentRect: { width: number } };

/** Minimal ResizeObserver stand-in so container measurement can be driven by hand. */
class FakeResizeObserver {
  static instances: FakeResizeObserver[] = [];

  readonly observed: Element[] = [];
  readonly disconnect = vi.fn();
  readonly unobserve = vi.fn();
  private readonly callback: (entries: ObserverEntry[], observer: FakeResizeObserver) => void;

  constructor(callback: (entries: ObserverEntry[], observer: FakeResizeObserver) => void) {
    this.callback = callback;
    FakeResizeObserver.instances.push(this);
  }

  observe(target: Element): void {
    this.observed.push(target);
  }

  emit(width: number): void {
    this.callback([{ contentRect: { width } }], this);
  }
}

const setViewport = (width: number) => {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width });
  window.dispatchEvent(new Event('resize'));
};

const emitContainer = (width: number) => {
  const observer = FakeResizeObserver.instances[FakeResizeObserver.instances.length - 1];
  if (!observer) throw new Error('no ResizeObserver was created');
  act(() => {
    observer.emit(width);
  });
};

/**
 * `renderHook` plus a detached row element: the hook only starts observing once
 * `containerRef.current` points at something, which is what the shell does.
 */
const renderLayout = () => {
  const row = document.createElement('div');
  document.body.appendChild(row);
  const hook = renderHook(() => {
    const layout = useResearchAssistantLayoutState();
    const attachRow = layout.containerRef;
    useEffect(() => { attachRow(row); return () => attachRow(null); }, [attachRow]);
    return layout;
  });
  return { ...hook, row };
};

const originalViewport = window.innerWidth;

describe('useResearchAssistantLayoutState', () => {
  beforeEach(() => {
    FakeResizeObserver.instances = [];
    vi.stubGlobal('ResizeObserver', FakeResizeObserver);
    window.localStorage.clear();
    setViewport(1024);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    window.localStorage.clear();
    setViewport(originalViewport);
  });

  it('starts in the medium workbench with the documented defaults', () => {
    const { result } = renderLayout();

    expect(result.current.layoutMode).toBe('medium');
    expect(result.current.detailsMode).toBe('drawer');
    expect(result.current.chatCollapsed).toBe(false);
    expect(result.current.detailsOpen).toBe(false);
    expect(result.current.leftPanelPixels).toBe(layoutMetrics.chatMinPx);
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsDefaultPx);
    expect(result.current.leftPanelWidth).toBeCloseTo(
      (layoutMetrics.chatMinPx / estimateContainerWidth(1024)) * 100,
      1,
    );
  });

  it('measures the row with ResizeObserver and docks details when it fits', () => {
    setViewport(1440);
    const { result } = renderLayout();

    emitContainer(1400);

    expect(result.current.containerWidth).toBe(1400);
    expect(result.current.layoutMode).toBe('wide');
    expect(result.current.detailsMode).toBe('docked');
    expect(result.current.sidebarWidth).toBe(440);
    expect(result.current.leftPanelPixels).toBeGreaterThanOrEqual(layoutMetrics.chatMinPx);
    expect(result.current.leftPanelPixels + result.current.sidebarWidth).toBeLessThan(1400);
  });

  it('switches to a single main area for a squeezed container and recovers', () => {
    setViewport(1440);
    const { result } = renderLayout();

    emitContainer(1400);
    const wideChat = result.current.leftPanelPixels;

    emitContainer(700);
    expect(result.current.layoutMode).toBe('single');
    expect(result.current.detailsMode).toBe('drawer');
    expect(result.current.leftPanelPixels).toBe(700);
    expect(result.current.leftPanelWidth).toBe(100);

    emitContainer(1400);
    expect(result.current.layoutMode).toBe('wide');
    expect(result.current.leftPanelPixels).toBe(wideChat);
  });

  it('reaches the mobile mode from the viewport breakpoint', () => {
    const { result } = renderLayout();

    act(() => setViewport(390));
    emitContainer(390);

    expect(result.current.layoutMode).toBe('mobile');
    expect(result.current.detailsMode).toBe('fullscreen');
    expect(result.current.leftPanelPixels).toBe(390);
    expect(result.current.leftPanelWidth).toBe(100);

    act(() => setViewport(1024));
    emitContainer(930);
    expect(result.current.layoutMode).toBe('medium');
  });

  it('syncs detailsOpen from the shell without changing the details mode', () => {
    setViewport(1440);
    const { result } = renderLayout();
    emitContainer(1400);

    act(() => result.current.setDetailsOpen(true));
    expect(result.current.detailsOpen).toBe(true);
    expect(result.current.detailsMode).toBe('docked');

    act(() => result.current.setDetailsOpen(false));
    expect(result.current.detailsOpen).toBe(false);
    expect(result.current.detailsMode).toBe('docked');
  });

  it('toggles the chat column', () => {
    const { result } = renderLayout();

    act(() => result.current.toggleChat());
    expect(result.current.chatCollapsed).toBe(true);

    act(() => result.current.toggleChat());
    expect(result.current.chatCollapsed).toBe(false);
  });

  it('constrains keyboard adjustments to the visible split and preserves them across a resize', () => {
    setViewport(1440);
    const { result } = renderLayout();
    emitContainer(1400);
    act(() => result.current.setDetailsOpen(true));

    act(() => result.current.adjustLeftPanel(200));

    // Preference grew, but the canvas floor still wins inside this container.
    const clamped = result.current.leftPanelPixels;
    expect(clamped).toBeGreaterThan(layoutMetrics.chatMinPx);
    expect(1400 - clamped - result.current.sidebarWidth).toBeGreaterThanOrEqual(
      layoutMetrics.canvasMinPx - 32,
    );

    // The deliberate keyboard change remains the preference after growing.
    emitContainer(2200);
    expect(result.current.leftPanelPixels).toBe(clamped);
    expect(result.current.leftPanelPixels).toBeLessThanOrEqual(layoutMetrics.chatMaxPx);
  });

  it('clamps sidebar adjustments at the 360px floor', () => {
    setViewport(1440);
    const { result } = renderLayout();
    emitContainer(1400);

    act(() => result.current.adjustSidebar(-400));
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsMinPx);

    act(() => result.current.adjustSidebar(100));
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsMinPx + 100);

    act(() => result.current.adjustSidebar(5000));
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsMaxPx);
  });

  it('resets both columns to the defaults', () => {
    setViewport(1440);
    const { result } = renderLayout();
    emitContainer(1400);

    act(() => result.current.adjustLeftPanel(200));
    act(() => result.current.adjustSidebar(-200));
    act(() => result.current.resetLeftPanel());
    act(() => result.current.resetSidebar());

    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsDefaultPx);
    expect(result.current.leftPanelPixels).toBe(
      Math.round(Math.max(layoutMetrics.chatMinPx, Math.min(layoutMetrics.chatMaxPx, 1400 * 0.24))),
    );
  });

  it('tracks the drag flags for both handles', () => {
    const { result } = renderLayout();

    act(() => result.current.startResizingLeftPanel());
    expect(result.current.isResizing).toBe(true);
    expect(result.current.isResizingSidebar).toBe(false);

    act(() => result.current.startResizingSidebar());
    expect(result.current.isResizing).toBe(false);
    expect(result.current.isResizingSidebar).toBe(true);

    act(() => {
      window.dispatchEvent(new Event('pointerup'));
    });
    expect(result.current.isResizingSidebar).toBe(false);
  });

  it('reads the legacy 24% ratio cache as a ratio, not as 24 pixels', () => {
    setViewport(1494); // container estimate = 1400px
    window.localStorage.setItem('ra.leftPanelWidth', '24');

    const { result } = renderLayout();

    expect(result.current.leftPanelPixels).toBe(336);
    expect(result.current.leftPanelWidth).toBeCloseTo(24, 0);
    expect(result.current.leftPanelPixels).toBeGreaterThan(24);
  });

  it('clamps the legacy sidebar cache to the 360px floor', () => {
    window.localStorage.setItem('ra.sidebarWidth', '320');
    const { result } = renderLayout();
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsMinPx);
  });

  it('falls back to defaults for invalid caches', () => {
    window.localStorage.setItem('ra.leftPanelWidth', 'huge');
    window.localStorage.setItem('ra.sidebarWidth', '-40');

    const { result } = renderLayout();

    expect(result.current.leftPanelPixels).toBe(layoutMetrics.chatMinPx);
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsDefaultPx);
  });

  it('survives a storage that throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage denied');
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage denied');
    });

    const { result } = renderLayout();

    expect(result.current.layoutMode).toBe('medium');
    expect(result.current.leftPanelPixels).toBe(layoutMetrics.chatMinPx);
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsDefaultPx);

    act(() => result.current.adjustLeftPanel(40));
    expect(result.current.leftPanelPixels).toBe(layoutMetrics.chatMinPx + 40);
  });

  it('persists the preference and the legacy mirror after a user change', async () => {
    setViewport(1494);
    const { result } = renderLayout();

    act(() => result.current.adjustLeftPanel(100));

    await waitFor(() => {
      expect(window.localStorage.getItem('ra.chatWidthPx')).toBe('436');
    });
    expect(window.localStorage.getItem('ra.detailsWidthPx')).toBe('440');
    expect(window.localStorage.getItem('ra.leftPanelWidth')).toBe('31.1');
    expect(window.localStorage.getItem('ra.sidebarWidth')).toBe('440');
  });

  it('does not rewrite the legacy ratio cache on mount', async () => {
    setViewport(1494);
    window.localStorage.setItem('ra.leftPanelWidth', '24');

    renderLayout();

    await new Promise((resolve) => setTimeout(resolve, 260));
    expect(window.localStorage.getItem('ra.leftPanelWidth')).toBe('24');
    expect(window.localStorage.getItem('ra.chatWidthPx')).toBe('336');
  });

  it('ignores non-finite keyboard deltas', () => {
    const { result } = renderLayout();
    const before = result.current.leftPanelPixels;

    act(() => result.current.adjustLeftPanel(Number.NaN));
    act(() => result.current.adjustSidebar(Number.NaN));

    expect(result.current.leftPanelPixels).toBe(before);
    expect(result.current.sidebarWidth).toBe(layoutMetrics.detailsDefaultPx);
  });

  it('only reserves details width while the details panel is open', () => {
    setViewport(1440);
    localStorage.setItem('ra.chatWidthPx', '600');
    const { result } = renderLayout();
    emitContainer(1346);
    expect(result.current.leftPanelPixels).toBe(600);
    act(() => result.current.setDetailsOpen(true));
    expect(result.current.leftPanelPixels).toBeLessThan(600);
    act(() => result.current.setDetailsOpen(false));
    expect(result.current.leftPanelPixels).toBe(600);
  });

  it('distinguishes a malformed new pixel cache from the old percentage cache', () => {
    setViewport(1920);
    localStorage.setItem('ra.chatWidthPx', '24');
    const { result } = renderLayout();
    expect(result.current.leftPanelPixels).toBe(320);
  });

  it('responds immediately to a keyboard reduction after container clamping', () => {
    setViewport(1440);
    localStorage.setItem('ra.chatWidthPx', '720');
    const { result } = renderLayout();
    emitContainer(1346);
    act(() => result.current.setDetailsOpen(true));
    const visible = result.current.leftPanelPixels;
    act(() => result.current.adjustLeftPanel(-24));
    expect(result.current.leftPanelPixels).toBe(visible - 24);
  });

  it('begins observing when the row mounts after onboarding', () => {
    const row = document.createElement('div');
    const { result, rerender } = renderHook(({ mounted }) => {
      const state = useResearchAssistantLayoutState();
      const attach = state.containerRef;
      useEffect(() => { attach(mounted ? row : null); return () => attach(null); }, [attach, mounted]);
      return state;
    }, { initialProps: { mounted: false } });
    expect(FakeResizeObserver.instances).toHaveLength(0);
    rerender({ mounted: true });
    expect(FakeResizeObserver.instances.at(-1)?.observed).toContain(row);
    emitContainer(700);
    expect(result.current.layoutMode).toBe('single');
  });

  it('remeasures the row on resize when ResizeObserver is unavailable', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const { result, row } = renderLayout();
    vi.spyOn(row, 'getBoundingClientRect').mockReturnValue({ left: 100, width: 700 } as DOMRect);
    act(() => setViewport(1440));
    expect(result.current.containerWidth).toBe(700);
    expect(result.current.layoutMode).toBe('single');
  });

  it('keeps the shell bottom at the panned visual viewport and cleans up listeners', () => {
    const viewport = Object.assign(new EventTarget(), { height: 360, offsetTop: 60 });
    vi.stubGlobal('visualViewport', viewport);
    const { unmount } = renderLayout();
    const style = document.documentElement.style;
    expect(style.getPropertyValue('--workbench-viewport-height')).toBe('360px');
    expect(style.getPropertyValue('--workbench-viewport-bottom')).toBe('420px');
    viewport.offsetTop = 80;
    act(() => viewport.dispatchEvent(new Event('scroll')));
    expect(style.getPropertyValue('--workbench-viewport-bottom')).toBe('440px');
    unmount();
    expect(style.getPropertyValue('--workbench-viewport-bottom')).toBe('');
    viewport.offsetTop = 100;
    viewport.dispatchEvent(new Event('scroll'));
    expect(style.getPropertyValue('--workbench-viewport-bottom')).toBe('');
  });
});
