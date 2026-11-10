import { describe, expect, it } from 'vitest';
import {
  canFitThreeColumns,
  canFitTwoColumns,
  chatChromePx,
  chatWidthFromPointer,
  clamp,
  computeEffectiveLayout,
  defaultChatWidthPx,
  detailsChromePx,
  detailsWidthFromPointer,
  estimateContainerWidth,
  layoutMetrics,
  maxChatWidthPx,
  maxDetailsWidthPx,
  parseChatWidthCache,
  parseDetailsWidthCache,
  resolveDetailsMode,
  resolveLayoutMode,
  toLegacyChatRatio,
} from '../../src/app/hooks/workspaceLayout';

/** Effective chat width in a 1440×900 window, details open at the default. */
const wideDefault = computeEffectiveLayout(
  estimateContainerWidth(1440),
  1440,
  { chatWidthPx: defaultChatWidthPx(estimateContainerWidth(1440)), detailsWidthPx: 440 },
);

const canvasWidthOf = (layout: ReturnType<typeof computeEffectiveLayout>) =>
  layout.containerWidth - chatChromePx - layout.chatWidthPx - layout.detailsWidthPx - detailsChromePx;

describe('workspaceLayout container measurement', () => {
  it('derives the row width from the viewport minus rail, margin and border', () => {
    expect(estimateContainerWidth(1440)).toBe(1440 - 68 - 26);
    expect(estimateContainerWidth(1366)).toBe(1366 - 68 - 26);
    expect(estimateContainerWidth(1024)).toBe(1024 - 68 - 26);
  });

  it('uses the full viewport on phones where the rail moves below the row', () => {
    expect(estimateContainerWidth(767)).toBe(767);
    expect(estimateContainerWidth(390)).toBe(390);
    expect(estimateContainerWidth(320)).toBe(320);
    expect(estimateContainerWidth(0)).toBe(0);
    expect(estimateContainerWidth(Number.NaN)).toBe(0);
  });
});

describe('workspaceLayout breakpoints (L-01 / L-03)', () => {
  it('docks details at 1440×900 when all three columns reach their minimum', () => {
    expect(wideDefault.layoutMode).toBe('wide');
    expect(wideDefault.detailsMode).toBe('docked');
    expect(canvasWidthOf(wideDefault)).toBeGreaterThanOrEqual(layoutMetrics.canvasMinPx);
    expect(wideDefault.chatWidthPx).toBeGreaterThanOrEqual(layoutMetrics.chatMinPx);
    expect(wideDefault.detailsWidthPx).toBeGreaterThanOrEqual(layoutMetrics.detailsMinPx);
  });

  it('still docks at 1366×768 but keeps the canvas usable', () => {
    const layout = computeEffectiveLayout(estimateContainerWidth(1366), 1366, {
      chatWidthPx: defaultChatWidthPx(estimateContainerWidth(1366)),
      detailsWidthPx: 440,
    });
    expect(layout.layoutMode).toBe('wide');
    expect(layout.detailsMode).toBe('docked');
    expect(canvasWidthOf(layout)).toBeGreaterThanOrEqual(layoutMetrics.canvasMinPx);
  });

  it('switches details to a drawer at 1024×768 and 1024×500 (L-02)', () => {
    const container = estimateContainerWidth(1024);
    const prefs = { chatWidthPx: 320, detailsWidthPx: 440 };
    for (const viewport of [1024]) {
      const layout = computeEffectiveLayout(container, viewport, prefs);
      expect(layout.layoutMode).toBe('medium');
      expect(layout.detailsMode).toBe('drawer');
      // Details overlay the canvas, so chat + canvas keep their minimums.
      expect(container - chatChromePx - layout.chatWidthPx).toBeGreaterThanOrEqual(
        layoutMetrics.canvasMinPx,
      );
    }
    // Height does not change the mode; 1024×500 behaves like 1024×768.
    expect(computeEffectiveLayout(container, 1024, prefs).layoutMode).toBe('medium');
  });

  it('is stable across the 767/768 and 1279/1280 boundaries', () => {
    expect(resolveLayoutMode(estimateContainerWidth(767), 767)).toBe('mobile');
    expect(resolveDetailsMode('mobile')).toBe('fullscreen');
    expect(resolveLayoutMode(estimateContainerWidth(768), 768)).toBe('single');
    expect(resolveDetailsMode('single')).toBe('drawer');

    const at1279 = computeEffectiveLayout(estimateContainerWidth(1279), 1279, {
      chatWidthPx: 320,
      detailsWidthPx: 440,
    });
    // The geometry already fits, but docking requires a >=1280 window.
    expect(canFitThreeColumns(estimateContainerWidth(1279))).toBe(true);
    expect(at1279.layoutMode).toBe('medium');
    expect(at1279.detailsMode).toBe('drawer');

    const at1280 = computeEffectiveLayout(estimateContainerWidth(1280), 1280, {
      chatWidthPx: 320,
      detailsWidthPx: 440,
    });
    expect(at1280.layoutMode).toBe('wide');
    expect(at1280.detailsMode).toBe('docked');
    expect(canvasWidthOf(at1280)).toBeGreaterThanOrEqual(layoutMetrics.canvasMinPx);
    expect(at1280.chatWidthPx + at1280.detailsWidthPx).toBeLessThanOrEqual(at1280.containerWidth);
  });

  it('falls back to a single main area when two columns no longer fit', () => {
    expect(canFitTwoColumns(estimateContainerWidth(768))).toBe(false);
    const layout = computeEffectiveLayout(estimateContainerWidth(768), 768, {
      chatWidthPx: 320,
      detailsWidthPx: 440,
    });
    expect(layout.layoutMode).toBe('single');
    expect(layout.chatWidthPx).toBe(layout.containerWidth);
    expect(layout.chatWidthPercent).toBe(100);
  });
});

describe('workspaceLayout effective widths (L-06)', () => {
  it('never returns negative widths on tiny containers', () => {
    for (const container of [0, 1, 120, 300, 640]) {
      const layout = computeEffectiveLayout(container, 900, { chatWidthPx: 320, detailsWidthPx: 440 });
      expect(layout.chatWidthPx).toBeGreaterThanOrEqual(0);
      expect(layout.detailsWidthPx).toBeGreaterThanOrEqual(0);
      expect(layout.chatWidthPercent).toBeGreaterThanOrEqual(0);
      expect(layout.chatWidthPercent).toBeLessThanOrEqual(100);
    }
  });

  it('keeps the container width when the row is unmeasured', () => {
    const layout = computeEffectiveLayout(0, 1440, { chatWidthPx: 320, detailsWidthPx: 440 });
    expect(layout.containerWidth).toBe(estimateContainerWidth(1440));
    expect(layout.layoutMode).toBe('wide');
  });

  it('shrinks the details column before giving up on docking', () => {
    // At 1280 the only way to dock is to pull details down to its 360px floor.
    const layout = computeEffectiveLayout(estimateContainerWidth(1280), 1280, {
      chatWidthPx: 320,
      detailsWidthPx: 440,
    });
    expect(layout.detailsMode).toBe('docked');
    expect(layout.detailsWidthPx).toBeGreaterThanOrEqual(layoutMetrics.detailsMinPx);
    expect(layout.detailsWidthPx).toBeLessThan(440);
    expect(canvasWidthOf(layout)).toBeGreaterThanOrEqual(layoutMetrics.canvasMinPx);
  });

  it('clamps the chat column so the canvas keeps its minimum', () => {
    const container = estimateContainerWidth(1366);
    const layout = computeEffectiveLayout(container, 1366, {
      chatWidthPx: layoutMetrics.chatMaxPx,
      detailsWidthPx: 440,
    });
    expect(layout.chatWidthPx).toBe(maxChatWidthPx(container, layout.detailsWidthPx));
    expect(canvasWidthOf(layout)).toBeGreaterThanOrEqual(layoutMetrics.canvasMinPx);
  });

  it('restores the user preference after the window grows again', () => {
    const preferences = { chatWidthPx: 420, detailsWidthPx: 500 };
    const small = computeEffectiveLayout(estimateContainerWidth(1366), 1366, preferences);
    const large = computeEffectiveLayout(2200, 2400, preferences);

    expect(small.chatWidthPx).toBeLessThan(preferences.chatWidthPx);
    expect(large.chatWidthPx).toBe(preferences.chatWidthPx);
    expect(large.detailsWidthPx).toBe(preferences.detailsWidthPx);
    // The preference object itself is never mutated.
    expect(preferences).toEqual({ chatWidthPx: 420, detailsWidthPx: 500 });
  });

  it('reports the chat width as a percentage of the measured container', () => {
    const layout = computeEffectiveLayout(1346, 1440, { chatWidthPx: 336, detailsWidthPx: 440 });
    expect(layout.chatWidthPercent).toBeCloseTo((336 / 1346) * 100, 1);
    expect(computeEffectiveLayout(1346, 1440, { chatWidthPx: 320, detailsWidthPx: 440 }).chatWidthPercent)
      .toBeCloseTo((320 / 1346) * 100, 1);
  });

  it('treats a broken preference object as the documented defaults', () => {
    const layout = computeEffectiveLayout(1346, 1440, {
      chatWidthPx: Number.NaN,
      detailsWidthPx: Number.POSITIVE_INFINITY,
    });
    expect(layout.chatWidthPx).toBe(layoutMetrics.chatMinPx);
    expect(Number.isFinite(layout.detailsWidthPx)).toBe(true);
  });
});

describe('workspaceLayout pointer constraints', () => {
  const bounds = { left: 100, width: 1272 };

  it('clamps a chat drag to the container bounds and column floors', () => {
    expect(chatWidthFromPointer(-500, bounds, 440)).toBe(layoutMetrics.chatMinPx);
    expect(chatWidthFromPointer(100, bounds, 440)).toBe(layoutMetrics.chatMinPx);
    expect(chatWidthFromPointer(400, bounds, 440)).toBe(layoutMetrics.chatMinPx);
    expect(chatWidthFromPointer(5000, bounds, 440)).toBe(maxChatWidthPx(1272, 440));
    expect(chatWidthFromPointer(5000, bounds, 440)).toBeLessThanOrEqual(layoutMetrics.chatMaxPx);
  });

  it('tracks the pointer inside the legal range', () => {
    // No docked details: the chat column can follow the pointer further.
    expect(chatWidthFromPointer(600, bounds, 0)).toBe(500);
    expect(chatWidthFromPointer(600, bounds, 0)).toBeLessThanOrEqual(
      maxChatWidthPx(bounds.width, 0),
    );
  });

  it('leaves more room for chat when details are not docked', () => {
    expect(chatWidthFromPointer(5000, bounds, 0)).toBeGreaterThan(
      chatWidthFromPointer(5000, bounds, 440),
    );
    expect(chatWidthFromPointer(5000, bounds, 0)).toBeLessThanOrEqual(layoutMetrics.chatMaxPx);
  });

  it('clamps a details drag to the floor and to the canvas minimum', () => {
    expect(detailsWidthFromPointer(5000, bounds, 320)).toBe(layoutMetrics.detailsMinPx);
    expect(detailsWidthFromPointer(0, bounds, 320)).toBe(maxDetailsWidthPx(1272, 320));
    expect(detailsWidthFromPointer(0, bounds, 320)).toBeGreaterThanOrEqual(
      layoutMetrics.detailsMinPx,
    );
    expect(detailsWidthFromPointer(1000, bounds, 320)).toBe(Math.round(100 + 1272 - 1000));
  });

  it('ignores non-finite pointer values instead of producing NaN widths', () => {
    expect(chatWidthFromPointer(Number.NaN, bounds, 440)).toBe(layoutMetrics.chatMinPx);
    expect(detailsWidthFromPointer(Number.NaN, bounds, 320)).toBe(layoutMetrics.detailsMinPx);
  });
});

describe('workspaceLayout cache parsing (L-08)', () => {
  it('reads the legacy ratio cache as a percentage, never as pixels', () => {
    // 24 means 24% of the container: 336px on a 1400px row, 320px (floor) on 930px.
    expect(parseChatWidthCache('24', 1400)).toBe(336);
    expect(parseChatWidthCache('24', 930)).toBe(layoutMetrics.chatMinPx);
    expect(parseChatWidthCache('24.5', 2000)).toBe(490);
    expect(toLegacyChatRatio(336, 1400)).toBe(24);
  });

  it('reads pixel caches above the ratio range', () => {
    expect(parseChatWidthCache('520', 1400)).toBe(520);
    expect(parseChatWidthCache('520', 0)).toBe(520);
    // 100 is still a ratio (100% of the container) and then clamped to the cap.
    expect(parseChatWidthCache('100', 1400)).toBe(layoutMetrics.chatMaxPx);
  });

  it('falls back on invalid or absurd chat caches', () => {
    expect(parseChatWidthCache(null, 1400)).toBeNull();
    expect(parseChatWidthCache('', 1400)).toBeNull();
    expect(parseChatWidthCache('wide', 1400)).toBeNull();
    expect(parseChatWidthCache('0', 1400)).toBeNull();
    expect(parseChatWidthCache('-24', 1400)).toBeNull();
    expect(parseChatWidthCache('999999', 1400)).toBeNull();
    expect(parseChatWidthCache('24', 0)).toBeNull();
  });

  it('clamps the legacy sidebar cache to the new 360px floor', () => {
    expect(parseDetailsWidthCache('440')).toBe(440);
    expect(parseDetailsWidthCache('320')).toBe(layoutMetrics.detailsMinPx);
    expect(parseDetailsWidthCache('900')).toBe(layoutMetrics.detailsMaxPx);
    expect(parseDetailsWidthCache('not-a-size')).toBeNull();
    expect(parseDetailsWidthCache('0')).toBeNull();
    expect(parseDetailsWidthCache('-440')).toBeNull();
    expect(parseDetailsWidthCache('999999')).toBeNull();
    expect(parseDetailsWidthCache(null)).toBeNull();
  });

  it('derives the legacy ratio from the preference, clamped to a sane range', () => {
    expect(toLegacyChatRatio(320, 1346)).toBe(23.8);
    expect(toLegacyChatRatio(320, 0)).toBe(24);
    expect(toLegacyChatRatio(0, 1346)).toBe(24);
  });
});

describe('workspaceLayout primitives', () => {
  it('clamps without producing NaN or inverted ranges', () => {
    expect(clamp(5, 10, 20)).toBe(10);
    expect(clamp(30, 10, 20)).toBe(20);
    expect(clamp(Number.NaN, 10, 20)).toBe(10);
    expect(clamp(15, 20, 10)).toBe(20);
  });

  it('never returns a maximum below the minimum', () => {
    expect(maxChatWidthPx(0, 440)).toBe(layoutMetrics.chatMinPx);
    expect(maxDetailsWidthPx(0, 320)).toBe(layoutMetrics.detailsMinPx);
  });
});
