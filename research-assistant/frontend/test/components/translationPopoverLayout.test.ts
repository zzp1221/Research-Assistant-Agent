import { describe, expect, it } from 'vitest';
import { computePopoverLayout, intersectRects } from '../../src/hooks/translationPopoverLayout';
const bounds = { left: 200, top: 100, right: 560, bottom: 440 };
describe('translation popover geometry', () => {
  it('intersects reader clipping and visual viewport offsets', () => {
    expect(intersectRects(bounds, { left: 0, top: 150, right: 500, bottom: 400 })).toEqual({ left: 200, top: 150, right: 500, bottom: 400 });
    expect(intersectRects(bounds, { left: 0, top: 0, right: 100, bottom: 50 })).toBeNull();
  });
  it('contains all four edges at every corner with short and long content', () => {
    for (const x of [200, 540]) for (const y of [100, 420]) for (const height of [70, 900]) {
      const result = computePopoverLayout(bounds, { left: x, top: y, right: x + 20, bottom: y + 20 }, height, 60)!;
      expect(result).not.toBeNull();
      expect(result.left).toBeGreaterThanOrEqual(bounds.left);
      expect(result.top).toBeGreaterThanOrEqual(bounds.top);
      expect(result.left + result.width).toBeLessThanOrEqual(bounds.right);
      expect(result.top + result.height).toBeLessThanOrEqual(bounds.bottom);
    }
  });
  it('shrinks below the former 160x120 minimum and hides if controls cannot fit', () => {
    // Tall enough for the chrome plus one readable body line (114px available),
    // so this case still exercises shrinking instead of hiding.
    const small = { left: 0, top: 0, right: 130, bottom: 130 };
    const selection = { left: 0, top: 0, right: 20, bottom: 20 };
    const result = computePopoverLayout(small, selection, 500, 60)!;
    expect(result.width).toBeLessThan(160); expect(result.height).toBeLessThan(120);
    expect(computePopoverLayout({ ...small, bottom: 30 }, selection, 500, 60)).toBeNull();
    expect(computePopoverLayout({ ...small, right: 40 }, selection, 500, 60)).toBeNull();
    expect(computePopoverLayout(bounds, selection, 500, 60)).toBeNull();
  });
  it('shrinks the vertical margin in a tight cell so the body keeps a readable line', () => {
    // 1024x500 with full-document translation leaves a ~122px cell: the 70px
    // chrome plus 48px of body only fit when the 8px gutters shrink to 2px.
    const selection = { left: 220, top: 130, right: 320, bottom: 160 };
    const tight = { left: 200, top: 100, right: 560, bottom: 222 };
    expect(tight.bottom - tight.top).toBe(122);
    const exact = computePopoverLayout(tight, selection, 400, 70)!;
    expect(exact).not.toBeNull();
    expect(exact.height - 70).toBeGreaterThanOrEqual(48);
    expect(exact.top).toBe(tight.top + 2);
    expect(exact.top + exact.height).toBeLessThanOrEqual(tight.bottom - 2);
    // Fractional browser chrome still leaves a full line in this 122px cell.
    const fractional = computePopoverLayout(tight, selection, 400, 71.5)!;
    expect(fractional).not.toBeNull();
    expect(fractional.height - 71.5).toBeGreaterThanOrEqual(48);
    // Below 120px even the minimum 1px gutters cannot fit chrome and body.
    expect(computePopoverLayout({ ...tight, bottom: 219 }, selection, 400, 70)).toBeNull();
    expect(computePopoverLayout({ ...tight, bottom: 220 }, selection, 400, 70)).not.toBeNull();
    // With room to spare the full 8px gutter comes back.
    const restored = computePopoverLayout({ ...tight, bottom: 234 }, selection, 400, 70)!;
    expect(restored).not.toBeNull();
    expect(restored.top).toBe(tight.top + 8);
    expect(restored.height - 70).toBeGreaterThanOrEqual(48);
    // The original 92px full-document translation cell still shows nothing.
    const original = { left: 200, top: 100, right: 560, bottom: 192 };
    expect(computePopoverLayout(original, selection, 400, 70)).toBeNull();
  });
});
