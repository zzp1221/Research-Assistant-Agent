import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useRef } from 'react';
import type { DocumentInfoResponse } from '../../src/contracts/api';

// The surface is code-split and heavy; this test only cares about what the
// workspace shell passes down, so it records the props it was mounted with and
// how many times it was mounted.
const surfaceState = vi.hoisted(() => ({ mounts: [] as string[], latest: null as Record<string, unknown> | null }));

vi.mock('../../src/features/pdf-viewer/PdfViewerSurface', () => ({
  default: function SurfaceMock(props: Record<string, unknown>) {
    const idRef = useRef<string>('');
    if (!idRef.current) {
      idRef.current = `surface-${surfaceState.mounts.length + 1}`;
      surfaceState.mounts.push(idRef.current);
    }
    surfaceState.latest = props;
    return <div data-testid="pdf-viewer-surface" data-instance-id={idRef.current} />;
  },
}));

import { LeftWorkspacePdf } from '../../src/app/components/LeftWorkspace';

const documentInfo: DocumentInfoResponse = {
  upload_id: 'upload-1',
  name: 'paper.pdf',
  document: { status: 'extracted', page_count: 3, characters: 1234, extractor: 'pdftotext' },
  pages: [{ number: 1, chars: 400 }],
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

const baseProps = {
  widthPercent: 100,
  pdfUrl: 'blob:paper',
  documentKey: 'upload-1',
  documentName: 'Attention Is All You Need.pdf',
  documentInfo,
  initialPage: 3,
  initialScale: 1.5,
  onClosePdf: vi.fn(),
  onPageChange: vi.fn(),
  onScaleChange: vi.fn(),
  translation,
  onAskAI: vi.fn(),
  onAssist: vi.fn().mockResolvedValue({ result: '', model: 'test' }),
};

const renderReader = async (overrides: Partial<typeof baseProps> = {}) => {
  const view = render(<LeftWorkspacePdf {...baseProps} {...overrides} />);
  // The reader surface is code-split, so wait for Suspense to resolve.
  await screen.findByTestId('pdf-viewer-surface');
  return view;
};

describe('LeftWorkspacePdf reader shell', () => {
  it('offers return-to-graph and show-chat without closing the document', async () => {
    const user = userEvent.setup();
    const onReturnToGraph = vi.fn();
    const onShowChat = vi.fn();
    const onClosePdf = vi.fn();
    await renderReader({ onReturnToGraph, onShowChat, onClosePdf });

    expect(screen.getByText('Attention Is All You Need.pdf')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '返回执行图' }));
    expect(onReturnToGraph).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('button', { name: '显示对话' }));
    expect(onShowChat).toHaveBeenCalledTimes(1);
    // Neither action may close the reader; only the explicit close button does.
    expect(onClosePdf).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: '关闭论文阅读器' }));
    expect(onClosePdf).toHaveBeenCalledTimes(1);
  });

  it('keeps one reader instance while chat updates and the reading page change', async () => {
    const { rerender } = await renderReader();
    const instanceId = screen.getByTestId('pdf-viewer-surface').getAttribute('data-instance-id');
    const mountsBefore = surfaceState.mounts.length;

    // A chat update re-creates every callback and the document metadata object;
    // a page change moves initialPage. Neither may rebuild the reader.
    rerender(
      <LeftWorkspacePdf
        {...baseProps}
        documentInfo={{ ...documentInfo }}
        onPageChange={vi.fn()}
        onAskAI={vi.fn()}
        initialPage={7}
      />,
    );

    expect(surfaceState.mounts).toHaveLength(mountsBefore);
    expect(screen.getByTestId('pdf-viewer-surface')).toHaveAttribute('data-instance-id', instanceId);
    expect(surfaceState.latest?.initialPage).toBe(7);
    expect(surfaceState.latest?.documentKey).toBe('upload-1');
    expect(surfaceState.latest?.initialScale).toBe(1.5);
  });

  it('falls back to the pdf url as the reader key when no document key is given', async () => {
    await renderReader({ documentKey: undefined });
    expect(surfaceState.latest?.documentKey).toBe('blob:paper');
  });

  it('rebuilds the reader when the document identity changes', async () => {
    const { rerender } = await renderReader();
    const instanceId = screen.getByTestId('pdf-viewer-surface').getAttribute('data-instance-id');
    const mountsBefore = surfaceState.mounts.length;

    rerender(<LeftWorkspacePdf {...baseProps} documentKey="upload-2" pdfUrl="blob:other" documentName="Second Paper.pdf" />);

    expect(surfaceState.mounts.length).toBe(mountsBefore + 1);
    expect(screen.getByTestId('pdf-viewer-surface').getAttribute('data-instance-id')).not.toBe(instanceId);
    expect(screen.getByText('Second Paper.pdf')).toBeInTheDocument();
  });
});
