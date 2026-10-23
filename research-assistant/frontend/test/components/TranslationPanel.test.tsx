import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TranslationPanel } from '../../src/features/pdf-viewer/TranslationPanel';
import type { TranslationJob } from '../../src/contracts/api';

const baseProps = {
  isStarting: false,
  error: '',
  pageText: '第一页译文',
  pageTextLoading: false,
  pageTextError: '',
  onStart: vi.fn(),
  onCancel: vi.fn(),
  onOpenPage: vi.fn(),
  onClose: vi.fn(),
};

const runningJob: TranslationJob = {
  id: 'job-1',
  upload_id: 'upload-1',
  status: 'running',
  total_pages: 3,
  completed_pages: 1,
  failed_pages: 0,
  current_page: 2,
  pages: [
    { number: 1, status: 'completed', chars: 100, updated_at: '2026-10-22T00:00:00Z' },
    { number: 2, status: 'running', chars: 100, updated_at: '2026-10-22T00:00:00Z' },
    { number: 3, status: 'pending', chars: 100, updated_at: '2026-10-22T00:00:00Z' },
  ],
  created_at: '2026-10-22T00:00:00Z',
};

describe('TranslationPanel', () => {
  it('starts a translation when no job exists', async () => {
    const user = userEvent.setup();
    render(<TranslationPanel {...baseProps} job={null} />);

    await user.click(screen.getByRole('button', { name: '开始全文翻译' }));
    expect(baseProps.onStart).toHaveBeenCalledTimes(1);
  });

  it('shows progress, supports cancellation and opens a page translation', async () => {
    const user = userEvent.setup();
    render(<TranslationPanel {...baseProps} job={runningJob} />);

    expect(screen.getByText('1/3 页')).toBeInTheDocument();
    expect(screen.getByText('正在翻译第 2 页')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(baseProps.onCancel).toHaveBeenCalledTimes(1);

    await user.click(screen.getByTitle('1 · 已完成'));
    expect(baseProps.onOpenPage).toHaveBeenCalledWith(1);
    expect(screen.getByText('第 1 页译文')).toBeInTheDocument();
    expect(screen.getByText('第一页译文')).toBeInTheDocument();
  });

  it('offers retranslation after completion', () => {
    render(<TranslationPanel {...baseProps} job={{ ...runningJob, status: 'completed', completed_pages: 3, current_page: 3 }} />);
    expect(screen.getByRole('button', { name: '重新翻译' })).toBeInTheDocument();
  });
});
