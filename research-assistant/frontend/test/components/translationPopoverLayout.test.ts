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
    const small = { left: 0, top: 0, right: 130, bottom: 100 };
    const selection = { left: 0, top: 0, right: 20, bottom: 20 };
    const result = computePopoverLayout(small, selection, 500, 60)!;
    expect(result.width).toBeLessThan(160); expect(result.height).toBeLessThan(120);
    expect(computePopoverLayout({ ...small, bottom: 30 }, selection, 500, 60)).toBeNull();
    expect(computePopoverLayout({ ...small, right: 40 }, selection, 500, 60)).toBeNull();
    expect(computePopoverLayout(bounds, selection, 500, 60)).toBeNull();
  });
});
