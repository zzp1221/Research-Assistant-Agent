import { useRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { DocumentInfoResponse } from '../../src/contracts/api';

type ViewerProps = {
  fileUrl: string;
  plugins: Array<{ install?: (functions: unknown) => void; uninstall?: () => void }>;
  defaultScale?: number;
  initialPage?: number;
  onDocumentLoad?: () => void;
  onPageChange?: (event: { currentPage: number }) => void;
  onZoom?: (event: { scale: number }) => void;
};

// Hoisted so the mock factory can keep every mounted viewer's latest props and
// count instantiations: "the reader was not recreated" is only observable by the
// number of Viewer mounts, not by DOM identity.
const viewerState = vi.hoisted(() => ({ mounts: [] as string[], latest: new Map<string, ViewerProps>() }));

vi.mock('@react-pdf-viewer/core', () => ({
  Worker: ({ workerUrl, children }: { workerUrl: string; children?: React.ReactNode }) => (
    <div data-testid="pdf-worker" data-worker-url={workerUrl}>{children}</div>
  ),
  Viewer: (props: ViewerProps) => {
    const idRef = useRef<string>('');
    if (!idRef.current) {
      idRef.current = `viewer-${viewerState.mounts.length + 1}`;
      viewerState.mounts.push(idRef.current);
    }
    viewerState.latest.set(idRef.current, props);
    return (
      <div
        data-testid="pdf-viewer"
        data-instance-id={idRef.current}
        data-initial-page={props.initialPage}
        data-default-scale={props.defaultScale}
      />
    );
  },
}));

import { PdfPanel } from '../../src/features/pdf-viewer/PdfPanel';

const documentInfo: DocumentInfoResponse = {
  upload_id: 'upload-1',
  name: 'paper.pdf',
  document: { status: 'extracted', page_count: 3, characters: 1234, extractor: 'pdftotext' },
  pages: [{ number: 1, chars: 400 }, { number: 2, chars: 400 }, { number: 3, chars: 434 }],
};

const translation = {
  job: null,
  isStarting: false,
  error: '',
  pageText: '',
  pageTextLoading: false,
  pageTextError: '',
  onStart: vi.fn(),
  onCancel: vi.fn(),
  onOpenPage: vi.fn(),
};

const latestViewer = (): ViewerProps => {
  const id = viewerState.mounts[viewerState.mounts.length - 1];
  const props = viewerState.latest.get(id);
  if (!props) throw new Error('viewer was not rendered');
  return props;
};

// The page-control plugin is the third entry: default layout, AI translation,
// then the panel's own page control.
const installPageControl = (functions: Record<string, unknown>) => {
  const plugin = latestViewer().plugins[2];
  plugin.install?.(functions);
  return plugin;
};

describe('PdfPanel', () => {
  it('uses local pdfjs assets and opens the translation panel', async () => {
    const user = userEvent.setup();
    render(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        translation={translation}
      />,
    );

    expect(screen.getByText('共 3 页')).toBeInTheDocument();
    expect(screen.getByTestId('pdf-worker')).toHaveAttribute('data-worker-url', '/pdfjs/pdf.worker.min.js');
    await user.click(screen.getByRole('button', { name: '全文翻译' }));
    expect(screen.getByText('开始全文翻译')).toBeInTheDocument();
  });

  it('explains why a scanned PDF cannot be translated', () => {
    render(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={{ ...documentInfo, document: { ...documentInfo.document, status: 'empty', page_count: 0 } }}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        translation={translation}
      />,
    );

    expect(screen.getByText(/没有可提取的文本层/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '全文翻译' })).toBeDisabled();
  });

  it('restores the saved one-based page and zoom scale on first mount', () => {
    render(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={2}
        initialScale={1.5}
        translation={translation}
      />,
    );

    // The viewer takes a zero-based page; the prop and every other surface stay
    // one-based, so page 2 must arrive as index 1.
    expect(screen.getByTestId('pdf-viewer')).toHaveAttribute('data-initial-page', '1');
    expect(screen.getByTestId('pdf-viewer')).toHaveAttribute('data-default-scale', '1.5');
    expect(screen.getByText('第 2 页')).toBeInTheDocument();
  });

  it('jumps the mounted reader to a new initialPage without recreating it', () => {
    const { rerender } = render(
      <PdfPanel
        pdfUrl="blob:test"
        documentKey="upload-1"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={1}
        translation={translation}
      />,
    );
    const instanceId = screen.getByTestId('pdf-viewer').getAttribute('data-instance-id');
    const mountsBefore = viewerState.mounts.length;
    const jumpToPage = vi.fn().mockResolvedValue(undefined);
    const zoom = vi.fn();
    installPageControl({ jumpToPage, zoom, getViewerState: () => ({ scale: 1, pageIndex: 0 }) });

    rerender(
      <PdfPanel
        pdfUrl="blob:test"
        documentKey="upload-1"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={3}
        initialScale={2}
        translation={translation}
      />,
    );

    // One-based prop -> zero-based viewer index, through the mounted reader.
    expect(jumpToPage).toHaveBeenCalledWith(2);
    expect(zoom).toHaveBeenCalledWith(2);
    expect(viewerState.mounts.length).toBe(mountsBefore);
    expect(screen.getByTestId('pdf-viewer')).toHaveAttribute('data-instance-id', instanceId);

    // The header follows the reader, so it updates once the viewer reports the
    // page it actually moved to rather than optimistically claiming a page.
    act(() => latestViewer().onPageChange?.({ currentPage: 2 }));
    expect(screen.getByText(/第 3 页/)).toBeInTheDocument();
  });

  it('reports one-based pages upward without jumping back to them', () => {
    const onPageChange = vi.fn();
    const { rerender } = render(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={1}
        onPageChange={onPageChange}
        translation={translation}
      />,
    );
    const jumpToPage = vi.fn().mockResolvedValue(undefined);
    installPageControl({ jumpToPage, zoom: vi.fn() });

    act(() => latestViewer().onPageChange?.({ currentPage: 2 }));
    expect(onPageChange).toHaveBeenCalledWith(3);
    expect(screen.getByText(/第 3 页/)).toBeInTheDocument();

    // The parent echoes the reported page back as initialPage; that must not
    // produce a jump, otherwise scrolling would fight a page-change feedback loop.
    rerender(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={3}
        onPageChange={onPageChange}
        translation={translation}
      />,
    );
    expect(jumpToPage).not.toHaveBeenCalled();

    const onScaleChange = vi.fn();
    rerender(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={3}
        onScaleChange={onScaleChange}
        translation={translation}
      />,
    );
    latestViewer().onZoom?.({ scale: 1.25 });
    expect(onScaleChange).toHaveBeenCalledWith(1.25);
  });

  it('applies a restore request that arrives before the reader reports ready', () => {
    const { rerender } = render(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={1}
        translation={translation}
      />,
    );

    // The page change lands before the viewer installed its plugin functions:
    // the jump must be parked, not dropped, and not applied by remounting.
    rerender(
      <PdfPanel
        pdfUrl="blob:test"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={5}
        translation={translation}
      />,
    );

    const jumpToPage = vi.fn().mockResolvedValue(undefined);
    installPageControl({ jumpToPage, zoom: vi.fn() });
    expect(jumpToPage).not.toHaveBeenCalled();

    act(() => latestViewer().onDocumentLoad?.());
    expect(jumpToPage).toHaveBeenCalledWith(4);
    act(() => latestViewer().onPageChange?.({ currentPage: 4 }));
    expect(screen.getByText(/第 5 页/)).toBeInTheDocument();
  });

  it('keeps the reader instance when the document key and reading position are unchanged', () => {
    const { rerender } = render(
      <PdfPanel
        pdfUrl="blob:test"
        documentKey="upload-1"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={2}
        initialScale={1.25}
        translation={translation}
      />,
    );
    const instanceId = screen.getByTestId('pdf-viewer').getAttribute('data-instance-id');
    const mountsBefore = viewerState.mounts.length;
    const jumpToPage = vi.fn().mockResolvedValue(undefined);
    installPageControl({ jumpToPage, zoom: vi.fn() });
    act(() => latestViewer().onDocumentLoad?.());
    act(() => latestViewer().onPageChange?.({ currentPage: 1 }));
    jumpToPage.mockClear();

    // A chat update re-renders the panel with a new translation object and new
    // callback identities; neither may move or recreate the reader.
    rerender(
      <PdfPanel
        pdfUrl="blob:test"
        documentKey="upload-1"
        documentInfo={documentInfo}
        defaultLayoutPluginInstance={{} as never}
        aiTranslationPluginInstance={{} as never}
        initialPage={2}
        initialScale={1.25}
        onPageChange={() => undefined}
        onScaleChange={() => undefined}
        translation={{ ...translation, onStart: vi.fn() }}
      />,
    );

    expect(viewerState.mounts.length).toBe(mountsBefore);
    expect(screen.getByTestId('pdf-viewer')).toHaveAttribute('data-instance-id', instanceId);
    expect(jumpToPage).not.toHaveBeenCalled();
  });

  it('keeps the saved page through initialization and then accepts normal navigation', () => {
    const onPageChange = vi.fn();
    render(<PdfPanel pdfUrl="blob:test" documentKey="upload-1"
      documentInfo={documentInfo} defaultLayoutPluginInstance={{} as never}
      aiTranslationPluginInstance={{} as never} initialPage={2}
      onPageChange={onPageChange} translation={translation} />);
    const jumpToPage = vi.fn().mockResolvedValue(undefined);
    installPageControl({ jumpToPage, zoom: vi.fn() });
    act(() => latestViewer().onDocumentLoad?.());
    expect(jumpToPage).toHaveBeenCalledWith(1);
    act(() => latestViewer().onPageChange?.({ currentPage: 0 }));
    expect(onPageChange).not.toHaveBeenCalled();
    expect(screen.getByText('第 2 页')).toBeInTheDocument();
    act(() => latestViewer().onPageChange?.({ currentPage: 1 }));
    expect(onPageChange).not.toHaveBeenCalled();
    act(() => latestViewer().onPageChange?.({ currentPage: 2 }));
    expect(onPageChange).toHaveBeenCalledWith(3);
    expect(screen.getByText('第 3 页')).toBeInTheDocument();
  });
});
