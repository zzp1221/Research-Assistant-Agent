import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { FullAccessConfirmDialog } from '../../src/features/chat/FullAccessConfirmDialog';

describe('FullAccessConfirmDialog', () => {
  it('renders in a body portal and moves focus into the dialog', async () => {
    const onCancel = vi.fn();
    render(<FullAccessConfirmDialog open onCancel={onCancel} onConfirm={vi.fn()} />);

    const dialog = screen.getByRole('dialog');
    expect(document.body.contains(dialog)).toBe(true);
    await waitFor(() => {
      expect(document.activeElement).toBe(screen.getByRole('button', { name: '关闭完全权限确认' }));
    });
  });

  it('closes on Escape instead of letting the background handle it', async () => {
    const onCancel = vi.fn();
    render(<FullAccessConfirmDialog open onCancel={onCancel} onConfirm={vi.fn()} />);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('keeps Tab focus inside the dialog', async () => {
    const onCancel = vi.fn();
    render(<FullAccessConfirmDialog open onCancel={onCancel} onConfirm={vi.fn()} />);
    const dialog = screen.getByRole('dialog');
    const close = screen.getByRole('button', { name: '关闭完全权限确认' });
    await waitFor(() => expect(document.activeElement).toBe(close));

    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true });
    expect(dialog.contains(document.activeElement)).toBe(true);

    fireEvent.keyDown(document, { key: 'Tab' });
    expect(dialog.contains(document.activeElement)).toBe(true);
  });
});
