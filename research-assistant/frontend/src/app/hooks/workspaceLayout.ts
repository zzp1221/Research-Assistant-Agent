/**
 * Pure geometry, cache parsing and breakpoint helpers for the responsive
 * workbench layout (UI-03).
 *
 * Everything here is side-effect free so it can be unit tested without a DOM.
 * `useResearchAssistantLayoutState` only owns React state, measurement and
 * pointer plumbing; every size decision goes through this module.
 *
 * The numbers mirror the constraints in
 * `docs/design/2026-11-09-workbench-interaction-improvements.md` §5:
 *
 * - chat column (desktop)        min 320px
 * - canvas content area          min 480px (desktop dual/triple column)
 * - docked details column        min 360px, default 440px
 * - mobile                       < 768px viewport
 * - dockable (wide) workbench    >= 1280px viewport *and* room for all three
 *
 * Space is always measured on the workbench row itself (the element that owns
 * `containerRef`), never on `window.innerWidth`, because the rail, the shell
 * margin/border and the drag handles all take space away from the columns.
 */

export type LayoutMode = 'wide' | 'medium' | 'single' | 'mobile';
export type DetailsMode = 'docked' | 'drawer' | 'fullscreen';

/** Sizes are shared with `src/styles/workbench-layout.css`; keep them in sync. */
export const layoutMetrics = {
  /** Desktop chat column floor. */
  chatMinPx: 320,
  /** Upper bound for a user *preference* (the container may clamp lower). */
  chatMaxPx: 720,
  /** Canvas content area floor for the desktop dual/triple column layouts. */
  canvasMinPx: 480,
  /** Docked details column floor. */
  detailsMinPx: 360,
  detailsMaxPx: 760,
  detailsDefaultPx: 440,
  /** `.w-1.5` chat resize handle. */
  chatHandlePx: 6,
  /** `border-right` of the chat column. */
  chatBorderPx: 1,
  /** `.w-1.5` details resize handle. */
  detailsHandlePx: 6,
  /** `.research-assistant-execution-sidebar` right margin. */
  detailsGutterPx: 10,
  /** Workbench rail width on desktop (the rail moves to the bottom on mobile). */
  railPx: 68,
  /** Shell 12px margin + 1px border on both sides. */
  shellInsetPx: 26,
  /** Legacy default of `ra.leftPanelWidth` (24% of the container). */
  defaultChatRatio: 0.24,
  mobileMaxViewportPx: 767,
  wideMinViewportPx: 1280,
} as const;

/** Space the chat column takes besides its own width: handle + border. */
export const chatChromePx = layoutMetrics.chatHandlePx + layoutMetrics.chatBorderPx;
/** Space the docked details column takes besides its own width. */
export const detailsChromePx = layoutMetrics.detailsHandlePx + layoutMetrics.detailsGutterPx;

export interface LayoutPreferences {
  /** User preferred chat width in CSS pixels (never a percentage). */
  chatWidthPx: number;
  /** User preferred details width in CSS pixels. */
  detailsWidthPx: number;
}

export interface LayoutBounds {
  left: number;
  width: number;
}

export interface EffectiveLayout {
  layoutMode: LayoutMode;
  detailsMode: DetailsMode;
  /** Effective chat width in pixels after container clamping. */
  chatWidthPx: number;
  /** Effective details width in pixels after container clamping. */
  detailsWidthPx: number;
  /** Effective chat width as a percentage of the container (0–100). */
  chatWidthPercent: number;
  containerWidth: number;
}

export const clamp = (value: number, min: number, max: number): number => {
  if (!Number.isFinite(value)) return min;
  const upper = Math.max(min, max);
  return Math.min(upper, Math.max(min, value));
};

const finiteOr = (value: number, fallback: number): number =>
  Number.isFinite(value) ? value : fallback;

/**
 * Best-effort width of the workbench row before (or without) a real
 * measurement: viewport minus rail, shell margin and shell border. On mobile the
 * rail sits at the bottom and the shell is edge to edge.
 */
export function estimateContainerWidth(viewportWidth: number): number {
  const viewport = Math.max(0, finiteOr(viewportWidth, 0));
  if (viewport <= 0) return 0;
  if (viewport <= layoutMetrics.mobileMaxViewportPx) return Math.round(viewport);
  return Math.max(0, Math.round(viewport - layoutMetrics.railPx - layoutMetrics.shellInsetPx));
}

/** Chat + canvas fit side by side. */
export function canFitTwoColumns(containerWidth: number): boolean {
  const container = Math.max(0, finiteOr(containerWidth, 0));
  return container - chatChromePx >= layoutMetrics.chatMinPx + layoutMetrics.canvasMinPx;
}

/** Chat + canvas + docked details fit at their minimum widths. */
export function canFitThreeColumns(containerWidth: number): boolean {
  const container = Math.max(0, finiteOr(containerWidth, 0));
  return (
    container - chatChromePx - detailsChromePx >=
    layoutMetrics.chatMinPx + layoutMetrics.canvasMinPx + layoutMetrics.detailsMinPx
  );
}

/**
 * Breakpoints come from the viewport, the docking decision also needs the real
 * container: a 1280px window with a squeezed row is a medium workbench, not a
 * wide one.
 */
export function resolveLayoutMode(containerWidth: number, viewportWidth: number): LayoutMode {
  const viewport = Math.max(0, finiteOr(viewportWidth, 0));
  if (viewport > 0 && viewport <= layoutMetrics.mobileMaxViewportPx) return 'mobile';

  const container =
    containerWidth > 0 ? Math.round(containerWidth) : estimateContainerWidth(viewport);

  if (viewport >= layoutMetrics.wideMinViewportPx && canFitThreeColumns(container)) return 'wide';
  if (canFitTwoColumns(container)) return 'medium';
  return 'single';
}

/** Details are docked only in the wide workbench; everything else overlays. */
export function resolveDetailsMode(layoutMode: LayoutMode): DetailsMode {
  if (layoutMode === 'wide') return 'docked';
  if (layoutMode === 'mobile') return 'fullscreen';
  return 'drawer';
}

export function defaultChatWidthPx(containerWidth: number): number {
  const container = Math.max(0, finiteOr(containerWidth, 0));
  if (container <= 0) return layoutMetrics.chatMinPx;
  return clamp(
    Math.round(container * layoutMetrics.defaultChatRatio),
    layoutMetrics.chatMinPx,
    layoutMetrics.chatMaxPx,
  );
}

/** Widest chat column that still leaves the canvas at its minimum. */
export function maxChatWidthPx(containerWidth: number, dockedDetailsPx = 0): number {
  const container = Math.max(0, finiteOr(containerWidth, 0));
  const details = dockedDetailsPx > 0 ? dockedDetailsPx + detailsChromePx : 0;
  const available = container - chatChromePx - details - layoutMetrics.canvasMinPx;
  return Math.max(layoutMetrics.chatMinPx, Math.min(layoutMetrics.chatMaxPx, available));
}

/** Widest details column that still leaves chat and canvas at their minimums. */
export function maxDetailsWidthPx(containerWidth: number, chatWidthPx: number): number {
  const container = Math.max(0, finiteOr(containerWidth, 0));
  const chat = Math.max(0, finiteOr(chatWidthPx, layoutMetrics.chatMinPx));
  const available =
    container - (chat > 0 ? chatChromePx : 0) - detailsChromePx - chat - layoutMetrics.canvasMinPx;
  return Math.max(layoutMetrics.detailsMinPx, Math.min(layoutMetrics.detailsMaxPx, available));
}

/**
 * Resolve effective widths from the user preference and the measured container.
 *
 * Order of concessions (§5.2): shrink the details column down to its floor
 * first, then switch details to an overlay, then collapse to a single main
 * area. Widths are never negative and never below the column floors.
 */
export function computeEffectiveLayout(
  containerWidth: number,
  viewportWidth: number,
  preferences: LayoutPreferences,
  options: { detailsOpen?: boolean; chatCollapsed?: boolean } = { detailsOpen: true },
): EffectiveLayout {
  const viewport = Math.max(0, finiteOr(viewportWidth, 0));
  const container =
    containerWidth > 0
      ? Math.round(containerWidth)
      : estimateContainerWidth(viewport);
  const { detailsOpen = false, chatCollapsed = false } = options;
  const chatSpace = chatCollapsed ? 0 : layoutMetrics.chatMinPx + chatChromePx;
  const canDock = container >= chatSpace + layoutMetrics.canvasMinPx + layoutMetrics.detailsMinPx + detailsChromePx;
  const canSplit = container >= chatSpace + layoutMetrics.canvasMinPx;
  const layoutMode: LayoutMode = viewport > 0 && viewport <= layoutMetrics.mobileMaxViewportPx
    ? 'mobile'
    : viewport >= layoutMetrics.wideMinViewportPx && (detailsOpen ? canDock : canSplit)
      ? 'wide'
      : canSplit ? 'medium' : 'single';
  const detailsMode = resolveDetailsMode(layoutMode);

  const preferredChat = Math.max(
    0,
    finiteOr(preferences?.chatWidthPx, layoutMetrics.chatMinPx),
  );
  const preferredDetails = Math.max(
    0,
    finiteOr(preferences?.detailsWidthPx, layoutMetrics.detailsDefaultPx),
  );

  let chatWidthPx: number;
  let detailsWidthPx: number;

  if (layoutMode === 'single' || layoutMode === 'mobile') {
    // One main area at a time: the pane takes the whole row, details overlay it.
    chatWidthPx = container;
    detailsWidthPx =
      layoutMode === 'mobile'
        ? container
        : clamp(preferredDetails, layoutMetrics.detailsMinPx, Math.min(layoutMetrics.detailsMaxPx, Math.max(layoutMetrics.detailsMinPx, container)));
  } else if (layoutMode === 'medium' || !detailsOpen) {
    // Chat + canvas; details open as a drawer, so they do not steal width here.
    chatWidthPx = chatCollapsed ? 0 : clamp(
      preferredChat,
      layoutMetrics.chatMinPx,
      maxChatWidthPx(container, 0),
    );
    detailsWidthPx = clamp(
      preferredDetails,
      layoutMetrics.detailsMinPx,
      Math.min(layoutMetrics.detailsMaxPx, Math.max(layoutMetrics.detailsMinPx, container)),
    );
  } else {
    // Wide: keep the canvas at its minimum by shrinking details first.
    detailsWidthPx = clamp(
      preferredDetails,
      layoutMetrics.detailsMinPx,
      maxDetailsWidthPx(container, chatCollapsed ? 0 : layoutMetrics.chatMinPx),
    );
    chatWidthPx = chatCollapsed ? 0 : clamp(
      preferredChat,
      layoutMetrics.chatMinPx,
      maxChatWidthPx(container, detailsWidthPx),
    );
    // Re-clamp details against the *effective* chat width so the three columns
    // never add up to more than the container.
    detailsWidthPx = clamp(
      detailsWidthPx,
      layoutMetrics.detailsMinPx,
      maxDetailsWidthPx(container, chatWidthPx),
    );
  }

  const chatWidthPercent =
    container > 0
      ? Math.round(clamp((chatWidthPx / container) * 100, 0, 100) * 10) / 10
      : Math.round(layoutMetrics.defaultChatRatio * 1000) / 10;

  return {
    layoutMode,
    detailsMode,
    chatWidthPx,
    detailsWidthPx,
    chatWidthPercent,
    containerWidth: container,
  };
}

/**
 * Chat width for a pointer position. Bounded by the container bounds and the
 * joint column minimums, so a drag can never produce a negative or
 * unrecoverable layout.
 */
export function chatWidthFromPointer(
  pointerClientX: number,
  bounds: LayoutBounds,
  dockedDetailsPx = 0,
): number {
  const left = finiteOr(bounds?.left, 0);
  const width = Math.max(0, finiteOr(bounds?.width, 0));
  const offset = finiteOr(pointerClientX, left) - left;
  return clamp(
    Math.round(offset),
    layoutMetrics.chatMinPx,
    maxChatWidthPx(width, dockedDetailsPx),
  );
}

/** Details width for a pointer position, measured from the container's right edge. */
export function detailsWidthFromPointer(
  pointerClientX: number,
  bounds: LayoutBounds,
  chatWidthPx: number,
): number {
  const left = finiteOr(bounds?.left, 0);
  const width = Math.max(0, finiteOr(bounds?.width, 0));
  const offset = left + width - finiteOr(pointerClientX, left + width);
  return clamp(
    Math.round(offset),
    layoutMetrics.detailsMinPx,
    maxDetailsWidthPx(width, chatWidthPx),
  );
}

/**
 * Parse `ra.chatWidthPx` (pixels) or the legacy `ra.leftPanelWidth` cache.
 *
 * The legacy key stored a *ratio* (default 24 = 24% of the container), so a
 * value ≤ 100 must never be read as 24 pixels. Values above 100 can only come
 * from a pixel-based writer and are accepted as pixels. Anything non-numeric,
 * zero/negative or absurd falls back to `null` (caller default).
 */
export function parseChatWidthCache(
  raw: string | null | undefined,
  containerWidth: number,
): number | null {
  if (raw === null || raw === undefined || raw === '') return null;
  const value = Number(raw);
  if (!Number.isFinite(value) || value <= 0) return null;

  if (value <= 100) {
    const container = Math.max(0, finiteOr(containerWidth, 0));
    if (container <= 0) return null;
    return clamp(
      Math.round((value / 100) * container),
      layoutMetrics.chatMinPx,
      layoutMetrics.chatMaxPx,
    );
  }

  // Beyond 100 it can only be pixels; reject clearly broken caches.
  if (value > layoutMetrics.chatMaxPx * 4) return null;
  return clamp(Math.round(value), layoutMetrics.chatMinPx, layoutMetrics.chatMaxPx);
}

/** Parse `ra.detailsWidthPx` or the legacy `ra.sidebarWidth` cache (always pixels). */
export function parseDetailsWidthCache(raw: string | null | undefined): number | null {
  if (raw === null || raw === undefined || raw === '') return null;
  const value = Number(raw);
  if (!Number.isFinite(value) || value <= 0) return null;
  if (value > layoutMetrics.detailsMaxPx * 4) return null;
  return clamp(Math.round(value), layoutMetrics.detailsMinPx, layoutMetrics.detailsMaxPx);
}

/**
 * Compatibility mirror for the legacy `ra.leftPanelWidth` ratio cache. Written
 * from the *preference* (never from a temporarily clamped effective width) so a
 * shrunken window cannot overwrite the wide-screen preference.
 */
export function toLegacyChatRatio(chatWidthPx: number, containerWidth: number): number {
  const container = Math.max(0, finiteOr(containerWidth, 0));
  const chat = Math.max(0, finiteOr(chatWidthPx, 0));
  if (container <= 0 || chat <= 0) return Math.round(layoutMetrics.defaultChatRatio * 1000) / 10;
  return Math.round(clamp((chat / container) * 100, 1, 100) * 10) / 10;
}
