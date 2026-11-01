import { act, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { UpdateAvailableBanner } from '../../src/components/UpdateAvailableBanner';

describe('UpdateAvailableBanner', () => {
  it('renders the version and release notes', () => {
    render(<UpdateAvailableBanner update={{ version: '1.2.3', release_notes_url: 'https://example.com/notes' }} />);
    expect(screen.getByRole('status')).toHaveTextContent('发现新版本 1.2.3');
    expect(screen.getByRole('link')).toHaveAttribute('href', 'https://example.com/notes');
  });

  it('dismisses via the callback', () => {
    const onDismiss = vi.fn();
    render(<UpdateAvailableBanner update={{ version: '1.2.3' }} onDismiss={onDismiss} />);
    act(() => screen.getByLabelText('关闭更新提示').click());
    expect(onDismiss).toHaveBeenCalled();
  });

  it('renders from the Wails runtime event', () => {
    const listeners: Record<string, (...args: unknown[]) => void> = {};
    (window as unknown as { runtime: unknown }).runtime = {
      EventsOn: (event: string, callback: (...args: unknown[]) => void) => {
        listeners[event] = callback;
        return () => {};
      },
    };
    render(<UpdateAvailableBanner />);
    act(() => listeners['ra:update-available']?.({ version: '2.0.0' }));
    expect(screen.getByRole('status')).toHaveTextContent('发现新版本 2.0.0');
    delete (window as unknown as { runtime?: unknown }).runtime;
  });
});
