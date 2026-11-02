import { afterEach, describe, expect, it, vi } from 'vitest';
import { getDesktopBridge } from '../../src/services/desktopBridge';

describe('getDesktopBridge', () => {
  afterEach(() => {
    delete (window as unknown as { mygo?: unknown }).mygo;
    delete (window as unknown as { __RA_DESKTOP__?: unknown }).__RA_DESKTOP__;
    vi.restoreAllMocks();
  });

  it('uses the MyGo binding when window.mygo exists', async () => {
    const call = vi.fn(async (method: string) => (method === 'API.NeedsOnboarding' ? true : null));
    (window as unknown as { mygo?: unknown }).mygo = { call };
    const bridge = getDesktopBridge();
    expect(bridge).toBeDefined();
    await expect(bridge!.needsOnboarding()).resolves.toBe(true);
    expect(call).toHaveBeenCalledWith('API.NeedsOnboarding');
  });

  it('prefers the test injection', () => {
    const injected = { needsOnboarding: () => false } as never;
    (window as unknown as { __RA_DESKTOP__?: unknown }).__RA_DESKTOP__ = injected;
    expect(getDesktopBridge()).toBe(injected);
  });
});
