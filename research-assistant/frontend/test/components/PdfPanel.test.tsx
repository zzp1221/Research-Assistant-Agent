import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { DocumentInfoResponse } from '../../src/contracts/api';

vi.mock('@react-pdf-viewer/core', () => ({
  Worker: ({ workerUrl, children }: { workerUrl: string; children?: React.ReactNode }) => (
    <div data-testid="pdf-worker" data-worker-url={workerUrl}>{children}</div>
  ),
  Viewer: () => <div data-testid="pdf-viewer" />,
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
});
