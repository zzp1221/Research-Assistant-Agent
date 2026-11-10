import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AITranslationPopover } from '../../src/hooks/AITranslationPopover';
import type { AssistResponse } from '../../src/contracts/api';

const rect = (left: number, top: number, width: number, height: number) => ({ left, top, right: left + width, bottom: top + height, width, height, x: left, y: top, toJSON: () => ({}) });
let roots: HTMLElement[];
let observers: { disconnected: boolean; trigger: () => void }[];
let frames: Map<number, FrameRequestCallback>;
let nextFrame: number;
const flush = () => act(() => { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach((cb) => cb(0)); });
function reader(left: number, pageIndex = 0) {
  const root = document.createElement('div');
  root.innerHTML = `<div class="rpv-default-layout__body"><div data-testid="core__page-layer-${pageIndex}"></div></div>`;
  root.getBoundingClientRect = () => rect(left, 80, 280, 300);
  const body = root.firstElementChild as HTMLElement;
  body.getBoundingClientRect = () => rect(left, 110, 280, 270);
  const page = body.firstElementChild as HTMLElement;
  page.getBoundingClientRect = () => rect(left, 110, 280, 500);
  const parent = document.createElement('div'); parent.append(root);
  document.body.append(parent); roots.push(parent);
  return { root, body, page };
}
const props = {
  selectedText: 'selection', mode: 'translate' as const, isLoading: false, error: '',
  answer: { result: 'translated result', model: 'test' } as AssistResponse,
  selectionRegion: { pageIndex: 0, left: 10, top: 10, width: 10, height: 5 },
  onClose: vi.fn(), onAskAI: vi.fn(),
};
beforeEach(() => {
  roots = []; observers = []; frames = new Map(); nextFrame = 0;
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => { frames.set(++nextFrame, callback); return nextFrame; });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
  vi.stubGlobal('ResizeObserver', class {
    entry: { disconnected: boolean; trigger: () => void };
    constructor(callback: () => void) { this.entry = { disconnected: false, trigger: callback }; observers.push(this.entry); }
    observe() {} unobserve() {} disconnect() { this.entry.disconnected = true; }
  });
});
afterEach(() => { roots.forEach((root) => root.remove()); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('reader-scoped translation popover', () => {
  it('uses the triggering reader when another PDF has the same page index', () => {
    const other = reader(0); const actual = reader(500);
    const view = render(<AITranslationPopover {...props} readerRef={{ current: actual.root as HTMLDivElement }} />);
    expect(parseFloat(screen.getByRole('dialog').style.left)).toBeGreaterThanOrEqual(500);
    actual.page.getBoundingClientRect = () => rect(500, -800, 280, 500);
    act(() => window.dispatchEvent(new Event('scroll'))); flush();
    expect(screen.getByTestId('ai-translation-popover').style.visibility).toBe('hidden');
    expect(screen.getByTestId('ai-translation-popover')).toHaveTextContent('translated result');
    actual.page.getBoundingClientRect = () => rect(500, 110, 280, 500);
    act(() => window.dispatchEvent(new Event('scroll'))); flush();
    expect(screen.getByRole('dialog')).toBeVisible();
    view.rerender(<AITranslationPopover {...props} readerRef={{ current: other.root as HTMLDivElement }} />);
    expect(parseFloat(screen.getByRole('dialog').style.left)).toBeLessThan(300);
    expect(observers.slice(0, -1).every((o) => o.disconnected)).toBe(true);
    // Schedule an update, then ensure unmount removes observers and frames.
    act(() => window.dispatchEvent(new Event('resize')));
    expect(frames.size).toBe(1);
    view.unmount();
    expect(frames.size).toBe(0); expect(observers.every((o) => o.disconnected)).toBe(true);
  });
  it('hides a portal when the reader ancestor becomes hidden without a resize', async () => {
    const actual = reader(200);
    render(<AITranslationPopover {...props} readerRef={{ current: actual.root as HTMLDivElement }} />);
    expect(screen.getByRole('dialog')).toBeVisible();
    await act(async () => { actual.root.parentElement!.style.visibility = 'hidden'; });
    flush();
    expect(screen.getByTestId('ai-translation-popover').style.visibility).toBe('hidden');
    await act(async () => { actual.root.parentElement!.style.visibility = ''; });
    flush();
    expect(screen.getByRole('dialog')).toBeVisible();
  });
  it('remeasures loading, errors and results, and cleans up each reopening', () => {
    const actual = reader(200); const readerRef = { current: actual.root as HTMLDivElement };
    const view = render(<AITranslationPopover {...props} readerRef={readerRef} isLoading answer={null} />);
    expect(screen.getByRole('dialog')).toBeVisible();
    view.rerender(<AITranslationPopover {...props} readerRef={readerRef} error="request failed" answer={null} />);
    expect(screen.getByRole('dialog')).toHaveTextContent('request failed');
    view.rerender(<AITranslationPopover {...props} readerRef={readerRef} />);
    expect(screen.getByRole('dialog')).toHaveTextContent('translated result');
    expect(observers.slice(0, -1).every((o) => o.disconnected)).toBe(true);
    view.unmount(); expect(observers.every((o) => o.disconnected)).toBe(true);
  });

  it('passes the selection text and its one-based page to 询问 AI', async () => {
    // The anchoring logic looks for the page layer of the selection's index, so
    // the reader has to expose the same page the region points at.
    const actual = reader(200, 6);
    const onAskAI = vi.fn();
    const onClose = vi.fn();
    render(
      <AITranslationPopover
        {...props}
        readerRef={{ current: actual.root as HTMLDivElement }}
        selectionRegion={{ pageIndex: 6, left: 10, top: 10, width: 10, height: 5 }}
        selectedText="the selected paragraph"
        onAskAI={onAskAI}
        onClose={onClose}
      />,
    );

    // The dialog header and the reference card must agree with the callback: the
    // viewer reports a zero-based page index, the reader is one-based.
    expect(screen.getByRole('dialog')).toHaveTextContent('第 7 页');
    await userEvent.click(screen.getByRole('button', { name: /针对此段落向 Research Assistant Agent 追问/ }));
    expect(onAskAI).toHaveBeenCalledWith('the selected paragraph', 7);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps working with a legacy single-argument onAskAI handler', async () => {
    const actual = reader(200);
    const legacyHandler = vi.fn<[string], void>();
    render(
      <AITranslationPopover
        {...props}
        readerRef={{ current: actual.root as HTMLDivElement }}
        selectionRegion={{ pageIndex: 0, left: 10, top: 10, width: 10, height: 5 }}
        selectedText="first page text"
        // A host that was not updated yet only reads the first argument. Extra
        // arguments are ignored by JavaScript, so the call must still succeed.
        onAskAI={legacyHandler}
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: /针对此段落向 Research Assistant Agent 追问/ }));
    expect(legacyHandler).toHaveBeenCalledTimes(1);
    expect(legacyHandler.mock.calls[0][0]).toBe('first page text');
  });
});
