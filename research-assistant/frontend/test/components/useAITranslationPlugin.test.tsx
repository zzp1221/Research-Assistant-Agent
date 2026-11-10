import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, renderHook, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import type { HighlightArea, RenderHighlightContentProps, RenderHighlightTargetProps } from '@react-pdf-viewer/highlight';
import type { AssistResponse } from '../../src/contracts/api';

// The viewer owns the highlight machinery, so the plugin factory is neutralised
// here: the test only needs the callbacks the hook hands to it and the exact
// props the viewer would pass back for a real text selection.
const captured = vi.hoisted(() => ({
  options: {} as {
    renderHighlightTarget?: (props: RenderHighlightTargetProps) => ReactElement;
    renderHighlightContent?: (props: RenderHighlightContentProps) => ReactElement;
  },
}));

vi.mock('@react-pdf-viewer/highlight', () => ({
  highlightPlugin: (options: typeof captured.options) => {
    captured.options = options;
    return { pluginName: 'highlight' };
  },
}));

import { useAITranslationPlugin } from '../../src/hooks/useAITranslationPlugin';

const assistResponse = { result: '译好的段落', model: 'test-model' } as AssistResponse;

// The viewer reports a zero-based page index; the selection sits on page 7.
const selectionRegion: HighlightArea = { pageIndex: 6, left: 12, top: 34, width: 20, height: 3 };

const makeTargetProps = (overrides: Partial<RenderHighlightTargetProps> = {}) => {
  const props = {
    highlightAreas: [],
    previewImage: '',
    selectedText: 'attention is all you need',
    selectionRegion,
    cancel: vi.fn(),
    toggle: vi.fn(),
    ...overrides,
  };
  return props as RenderHighlightTargetProps & {
    cancel: ReturnType<typeof vi.fn>;
    toggle: ReturnType<typeof vi.fn>;
  };
};

const renderTarget = (props: RenderHighlightTargetProps) => {
  const renderHighlightTarget = captured.options.renderHighlightTarget;
  if (!renderHighlightTarget) throw new Error('renderHighlightTarget was not registered');
  return render(renderHighlightTarget(props));
};

interface HookOptions {
  onAskAI?: (text: string, page: number) => void;
  onAssist?: (mode: 'translate' | 'explain', text: string, page: number) => Promise<AssistResponse>;
}

const renderPlugin = (options: HookOptions = {}) => {
  const onAssist = options.onAssist ?? vi.fn(async () => assistResponse);
  renderHook(() =>
    useAITranslationPlugin({
      readerRef: { current: null },
      documentKey: 'upload-1',
      onAskAI: options.onAskAI,
      onAssist,
    }),
  );
  return { onAssist };
};

beforeEach(() => {
  captured.options = {};
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe('AI selection toolbar', () => {
  it('asks AI directly with the one-based source page and clears the selection', async () => {
    const onAskAI = vi.fn();
    const { onAssist } = renderPlugin({ onAskAI });
    const target = makeTargetProps();
    renderTarget(target);

    const askButton = screen.getByRole('button', { name: '询问 AI' });
    expect(askButton.querySelector('svg')).not.toBeNull();

    await userEvent.click(askButton);

    expect(onAskAI).toHaveBeenCalledTimes(1);
    expect(onAskAI).toHaveBeenCalledWith('attention is all you need', 7);
    expect(target.cancel).toHaveBeenCalledTimes(1);
    // A direct ask must not first translate/explain, and must not open the
    // popover: it goes straight to the chat flow.
    expect(onAssist).not.toHaveBeenCalled();
    expect(target.toggle).not.toHaveBeenCalled();
  });

  it('keeps translation and explanation as assisted actions', async () => {
    const onAskAI = vi.fn();
    const { onAssist } = renderPlugin({ onAskAI });
    const target = makeTargetProps({ selectedText: '  padded selection  ' });
    renderTarget(target);

    await userEvent.click(screen.getByRole('button', { name: '翻译' }));

    expect(target.toggle).toHaveBeenCalledTimes(1);
    expect(onAssist).toHaveBeenCalledWith('translate', 'padded selection', 7);
    expect(onAskAI).not.toHaveBeenCalled();
    expect(target.cancel).not.toHaveBeenCalled();
  });

  it('hides the direct ask button when no onAskAI handler is supplied', () => {
    renderPlugin({});
    renderTarget(makeTargetProps());

    expect(screen.queryByRole('button', { name: '询问 AI' })).toBeNull();
    expect(screen.getByRole('button', { name: '翻译' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '解释' })).toBeInTheDocument();
  });

  it('wraps the toolbar instead of overflowing a narrow page', () => {
    renderPlugin({ onAskAI: vi.fn() });
    const view = renderTarget(makeTargetProps());
    const toolbar = view.container.firstElementChild as HTMLElement;

    expect(toolbar.className).toContain('flex-wrap');
    expect(toolbar.className).toContain('max-w-');
    // The bar keeps its anchor under the selection; wrapping absorbs the width
    // instead of pushing the third button outside the reader.
    expect(toolbar.style.left).toBe('12%');
    expect(toolbar.style.top).toBe('37%');
  });

  it('keeps the ask-AI entry point inside the popover for assisted answers', () => {
    const onAskAI = vi.fn();
    renderPlugin({ onAskAI });
    const renderHighlightContent = captured.options.renderHighlightContent;
    if (!renderHighlightContent) throw new Error('renderHighlightContent was not registered');

    const element = renderHighlightContent({
      highlightAreas: [],
      previewImage: '',
      selectedText: 'attention is all you need',
      selectionRegion,
      cancel: vi.fn(),
    });

    expect(element.props.onAskAI).toBe(onAskAI);
    expect(element.props.selectedText).toBe('attention is all you need');
    expect(element.props.selectionRegion).toEqual(selectionRegion);
  });
});
