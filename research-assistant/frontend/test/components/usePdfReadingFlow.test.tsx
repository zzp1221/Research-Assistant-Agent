import { StrictMode, useLayoutEffect } from 'react';
import { act, render, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { DocumentInfoResponse, DocumentReferenceDraft, TranslationJob, UploadedFile } from '../../src/contracts/api';
import * as documentApi from '../../src/services/api/documentApi';
import { usePdfAssistFlow } from '../../src/app/hooks/usePdfAssistFlow';
import {
  DEFAULT_READING_STATE,
  PDF_READING_STORAGE_KEY,
  PDF_TRANSLATION_STORAGE_KEY,
  readReadingEntry,
  readTranslationJobRecord,
  saveReadingEntry,
} from '../../src/features/pdf-viewer/readingStateStorage';

vi.mock('../../src/services/api/documentApi', () => ({
  assistDocument: vi.fn(),
  cancelTranslationJob: vi.fn(),
  createTranslationJob: vi.fn(),
  getTranslationJob: vi.fn(),
  getTranslationPage: vi.fn(),
  getUpload: vi.fn(),
  getUploadDocument: vi.fn(),
}));

interface FetchResult {
  ok: boolean;
  status: number;
  blob: () => Promise<Blob>;
}

const pdfResponse = (label: string): FetchResult => ({
  ok: true,
  status: 200,
  blob: async () => new Blob([`%PDF-1.4 ${label}`], { type: 'application/pdf' }),
});

const uploadOf = (id: string, name: string): UploadedFile => ({
  id,
  name,
  content_type: 'application/pdf',
  size: 1024,
  sha256: `sha-${id}`,
  content_url: `/api/uploads/${id}/content`,
  created_at: '2026-11-09T00:00:00Z',
});

const documentInfoOf = (id: string, name: string): DocumentInfoResponse => ({
  upload_id: id,
  name,
  document: { status: 'ready', page_count: 12 },
  pages: [],
});

const runningJob = (id: string, uploadId: string): TranslationJob => ({
  id,
  upload_id: uploadId,
  status: 'running',
  total_pages: 12,
  completed_pages: 2,
  failed_pages: 0,
  current_page: 3,
  pages: [],
  created_at: '2026-11-09T00:00:00Z',
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

// Blob URLs are fake in jsdom; the reader must still create and revoke them.
let blobCounter = 0;
const createdBlobUrls: string[] = [];
const revokeObjectURL = vi.fn();

let fetchMock: ReturnType<typeof vi.fn>;

interface PendingFetch {
  url: string;
  resolve: (result: FetchResult) => void;
  signal?: AbortSignal;
}

const queueFetch = () => {
  const pending: PendingFetch[] = [];
  fetchMock.mockImplementation(
    (url: string, init?: RequestInit) =>
      new Promise<FetchResult>((resolve) => {
        pending.push({ url: String(url), resolve, signal: init?.signal ?? undefined });
      }),
  );
  return pending;
};

const props = (overrides: Record<string, unknown> = {}) => ({
  setPrompt: vi.fn(),
  appendChatMessage: vi.fn(),
  appendSelectedTaskLog: vi.fn(),
  identity: { userId: 'user-1', sessionId: 'session-1' },
  activeSessionId: 'session-1',
  ...overrides,
});

beforeEach(() => {
  window.localStorage.clear();
  blobCounter = 0;
  createdBlobUrls.length = 0;
  revokeObjectURL.mockReset();
  (URL as unknown as { createObjectURL: (blob: Blob) => string }).createObjectURL = () => {
    const url = `blob:mock-${(blobCounter += 1)}`;
    createdBlobUrls.push(url);
    return url;
  };
  (URL as unknown as { revokeObjectURL: (url: string) => void }).revokeObjectURL = revokeObjectURL;

  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);

  vi.mocked(documentApi.getUploadDocument).mockImplementation(async (uploadId: string) =>
    documentInfoOf(uploadId, `doc-${uploadId}`),
  );
  vi.mocked(documentApi.getUpload).mockImplementation(async (uploadId: string) => uploadOf(uploadId, `doc-${uploadId}`));
  vi.mocked(documentApi.createTranslationJob).mockReset();
  vi.mocked(documentApi.getTranslationJob).mockReset();
  vi.mocked(documentApi.cancelTranslationJob).mockReset();
  vi.mocked(documentApi.getTranslationPage).mockReset();
  vi.mocked(documentApi.assistDocument).mockReset();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('usePdfAssistFlow reading draft', () => {
  it('builds a reference with the current one-based page without touching the composer', async () => {
    const flowProps = props();
    const onAskReference = vi.fn();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: { ...flowProps, onAskReference },
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });

    act(() => {
      flow.result.current.setReadingState({ page: 5 });
    });
    expect(flow.result.current.readingState.page).toBe(5);

    act(() => {
      flow.result.current.handleAskAI('选中的原文');
    });

    expect(onAskReference).toHaveBeenCalledTimes(1);
    const reference = onAskReference.mock.calls[0][0] as DocumentReferenceDraft;
    expect(reference.upload_id).toBe('upload-a');
    expect(reference.document_name).toBe('Paper A');
    expect(reference.page).toBe(5);
    expect(reference.quote).toBe('选中的原文');
    expect(reference.sessionId).toBe('session-1');
    expect(reference.id).toBeTruthy();

    // An existing composer draft must survive: the reader never writes the prompt.
    expect(flowProps.setPrompt).not.toHaveBeenCalled();
    expect(flowProps.appendChatMessage).not.toHaveBeenCalled();

    // An explicit page wins over the stored reading position.
    act(() => {
      flow.result.current.handleAskAI('另一段', 9);
    });
    expect((onAskReference.mock.calls[1][0] as DocumentReferenceDraft).page).toBe(9);
  });

  it('does not overwrite an unknown composer prompt when no reference callback is given', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    act(() => {
      flow.result.current.handleAskAI('引用');
    });

    expect(flowProps.setPrompt).not.toHaveBeenCalled();
    expect(flowProps.appendChatMessage).not.toHaveBeenCalled();
    expect(flowProps.appendSelectedTaskLog).toHaveBeenCalledTimes(1);
    expect(String(flowProps.appendSelectedTaskLog.mock.calls[0][0])).toContain('第 1 页');
  });
});

describe('usePdfAssistFlow document generations', () => {
  it('ignores a slow download for document A once B is open', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    const pending = queueFetch();
    const uploadA = uploadOf('upload-a', 'Paper A');
    const uploadB = uploadOf('upload-b', 'Paper B');

    act(() => {
      void flow.result.current.openPdfFromUpload(uploadA);
    });
    act(() => {
      void flow.result.current.openPdfFromUpload(uploadB);
    });
    expect(pending).toHaveLength(2);
    expect(pending[0].signal?.aborted).toBe(true);

    await act(async () => {
      pending[1].resolve(pdfResponse('B'));
      await Promise.resolve();
    });
    await waitFor(() => expect(flow.result.current.uploadId).toBe('upload-b'));
    const urlForB = flow.result.current.pdfUrl;
    expect(urlForB).toBe(createdBlobUrls.at(-1));

    // A's blob arrives late and must never replace B.
    await act(async () => {
      pending[0].resolve(pdfResponse('A'));
      await Promise.resolve();
    });
    expect(flow.result.current.uploadId).toBe('upload-b');
    expect(flow.result.current.pdfUrl).toBe(urlForB);
    expect(flow.result.current.documentName).toBe('Paper B');
    expect(flow.result.current.documentInfo?.upload_id).toBe('upload-b');
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('releases the blob on close and restores page and scale when reopened', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const uploadA = uploadOf('upload-a', 'Paper A');

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });
    const openUrl = flow.result.current.pdfUrl;
    act(() => {
      flow.result.current.setReadingState({ page: 7, scale: 1.5 });
    });
    await waitFor(() =>
      expect(readReadingEntry('user-1', 'session-1', 'upload-a')?.page).toBe(7),
    );

    act(() => {
      flow.result.current.closePdf();
    });
    expect(revokeObjectURL).toHaveBeenCalledWith(openUrl);
    expect(flow.result.current.pdfUrl).toBeNull();
    expect(flow.result.current.uploadId).toBeNull();
    // The stored position survives a close.
    expect(readReadingEntry('user-1', 'session-1', 'upload-a')?.page).toBe(7);

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });
    expect(flow.result.current.uploadId).toBe('upload-a');
    expect(flow.result.current.readingState).toEqual({ page: 7, scale: 1.5 });
  });

  it('keeps the existing blob and reader for the same document', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const uploadA = uploadOf('upload-a', 'Paper A');

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });
    const firstUrl = flow.result.current.pdfUrl;
    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(blobCounter).toBe(1);
    expect(flow.result.current.pdfUrl).toBe(firstUrl);
    expect(revokeObjectURL).not.toHaveBeenCalled();
  });

  it('isolates documents and references across sessions and identities', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    expect(flow.result.current.uploadId).toBe('upload-a');

    // Switching chat sessions drops the previous session's document.
    flow.rerender({ ...flowProps, activeSessionId: 'session-2', identity: { userId: 'user-1', sessionId: 'session-2' } });
    await waitFor(() => expect(flow.result.current.pdfUrl).toBeNull());
    expect(flow.result.current.uploadId).toBeNull();

    const callsBefore = vi.mocked(documentApi.getUpload).mock.calls.length;
    const reference: DocumentReferenceDraft = {
      id: 'ref-1',
      upload_id: 'upload-a',
      document_name: 'Paper A',
      page: 4,
      quote: 'x',
      sessionId: 'session-1',
    };
    await act(async () => {
      await flow.result.current.restoreReference(reference);
    });

    expect(vi.mocked(documentApi.getUpload).mock.calls.length).toBe(callsBefore);
    expect(flow.result.current.uploadId).toBeNull();
    expect(String(flowProps.appendChatMessage.mock.calls.at(-1)?.[0]?.text)).toContain('另一个会话');
  });

  it('jumps to the referenced page without rebuilding the same document', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const uploadA = uploadOf('upload-a', 'Paper A');

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });
    act(() => {
      flow.result.current.setReadingState({ page: 2, scale: 1.75 });
    });
    const urlBefore = flow.result.current.pdfUrl;

    await act(async () => {
      await flow.result.current.restoreReference({
        id: 'ref-1',
        upload_id: 'upload-a',
        document_name: 'Paper A',
        page: 8,
        quote: '',
        sessionId: 'session-1',
      });
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(vi.mocked(documentApi.getUpload)).not.toHaveBeenCalled();
    expect(flow.result.current.pdfUrl).toBe(urlBefore);
    expect(flow.result.current.readingState).toEqual({ page: 8, scale: 1.75 });
  });

  it('reopens a different document from a reference under the current identity', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });

    await act(async () => {
      await flow.result.current.restoreReference({
        id: 'ref-2',
        upload_id: 'upload-b',
        document_name: 'Paper B',
        page: 11,
        quote: '',
        sessionId: 'session-1',
      });
    });

    expect(vi.mocked(documentApi.getUpload)).toHaveBeenCalledWith('upload-b');
    expect(flow.result.current.uploadId).toBe('upload-b');
    expect(flow.result.current.documentName).toBe('Paper B');
    expect(flow.result.current.readingState.page).toBe(11);
  });

  it('reports a clear reason when the persisted upload cannot be restored', async () => {
    saveReadingEntry('user-1', 'session-1', 'upload-gone', 'Paper Gone', { page: 3, scale: 1 });
    vi.mocked(documentApi.getUpload).mockRejectedValue(new Error('404 not found'));

    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });

    await waitFor(() => expect(flow.result.current.documentError).not.toBe(''));
    expect(flow.result.current.documentError).toContain('404 not found');
    expect(flow.result.current.pdfUrl).toBeNull();
    expect(flow.result.current.uploadId).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(String(flowProps.appendChatMessage.mock.calls.at(-1)?.[0]?.text)).toContain('无法恢复该文献');
  });

  it('restores a persisted document from its upload identity after a refresh', async () => {
    saveReadingEntry('user-1', 'session-1', 'upload-a', 'Paper A', { page: 6, scale: 1.25 });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });

    await waitFor(() => expect(flow.result.current.uploadId).toBe('upload-a'));
    expect(vi.mocked(documentApi.getUpload)).toHaveBeenCalledWith('upload-a');
    expect(flow.result.current.readingState).toEqual({ page: 6, scale: 1.25 });
    expect(flow.result.current.documentKey).toContain('upload-a');
    expect(JSON.parse(window.localStorage.getItem(PDF_READING_STORAGE_KEY) ?? '{}')).not.toHaveProperty(
      'blob',
      expect.anything(),
    );
  });
});

describe('usePdfAssistFlow translation job lifecycle', () => {
  it('starts one server job on a double click and resumes it after close and reopen', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const uploadA = uploadOf('upload-a', 'Paper A');

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });

    const start = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob).mockReturnValue(start.promise);

    act(() => {
      flow.result.current.translation.onStart();
      flow.result.current.translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);

    await act(async () => {
      start.resolve(runningJob('job-1', 'upload-a'));
      await Promise.resolve();
    });
    expect(flow.result.current.translation.job?.id).toBe('job-1');

    const stored = JSON.parse(window.localStorage.getItem(PDF_TRANSLATION_STORAGE_KEY) ?? '{}');
    expect(JSON.stringify(stored)).toContain('job-1');

    act(() => {
      flow.result.current.closePdf();
    });
    // Closing is not a cancellation: the server job stays visible as a message.
    const closeMessage = String(flowProps.appendChatMessage.mock.calls.at(-1)?.[0]?.text);
    expect(closeMessage).toContain('job-1');
    expect(closeMessage).toContain('仍在服务端继续');

    // Reopening resumes the persisted job through getTranslationJob only.
    vi.mocked(documentApi.getTranslationJob).mockResolvedValue({
      ...runningJob('job-1', 'upload-a'),
      status: 'completed',
      completed_pages: 12,
    });
    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });

    await waitFor(() => expect(flow.result.current.translation.job?.id).toBe('job-1'));
    expect(vi.mocked(documentApi.getTranslationJob)).toHaveBeenCalledWith('upload-a', 'job-1', expect.anything());
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);
    expect(vi.mocked(documentApi.cancelTranslationJob)).not.toHaveBeenCalled();
  });

  it('drops a translation page response that arrives after the document changed', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    const job = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob).mockReturnValue(job.promise);
    act(() => {
      flow.result.current.translation.onStart();
    });
    await act(async () => {
      job.resolve(runningJob('job-1', 'upload-a'));
      await Promise.resolve();
    });

    const page = deferred<string>();
    vi.mocked(documentApi.getTranslationPage).mockReturnValue(page.promise);
    act(() => {
      flow.result.current.translation.onOpenPage(3);
    });

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-b', 'Paper B'));
    });
    await act(async () => {
      page.resolve('迟到的译文');
      await Promise.resolve();
    });

    expect(flow.result.current.uploadId).toBe('upload-b');
    expect(flow.result.current.translation.pageText).not.toBe('迟到的译文');
  });
});

describe('usePdfAssistFlow unmount invalidation', () => {
  it('aborts a pending download on unmount so no blob URL is created afterwards', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    const pending = queueFetch();

    act(() => {
      void flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    expect(pending).toHaveLength(1);
    expect(pending[0].signal?.aborted).toBe(false);

    flow.unmount();

    // Cleanup must abort the in-flight download, not just revoke a known blob.
    expect(pending[0].signal?.aborted).toBe(true);
    expect(createdBlobUrls).toHaveLength(0);

    await act(async () => {
      pending[0].resolve(pdfResponse('A'));
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(createdBlobUrls).toHaveLength(0);
    expect(revokeObjectURL).not.toHaveBeenCalled();
  });

  it('invalidates non-abortable in-flight work on unmount', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });

    // assistDocument has no AbortController: only the generation bump performed
    // by the unmount cleanup can drop its result.
    const assist = deferred<{ mode: string; text: string; page: number }>();
    vi.mocked(documentApi.assistDocument).mockReturnValue(assist.promise as never);
    const outcome: { ok: boolean; message?: string } = { ok: false };
    void flow.result.current.assistWithUpload('translate', 'text', 1).then(
      () => {
        outcome.ok = true;
      },
      (error: Error) => {
        outcome.ok = false;
        outcome.message = error.message;
      },
    );

    flow.unmount();

    await act(async () => {
      assist.resolve({ mode: 'translate', text: 'x', page: 1 });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(outcome.ok).toBe(false);
    expect(outcome.message).toBe('文档已切换，已忽略上一次解释结果。');
  });

  it('restores the persisted document under StrictMode effect cleanup and replay', async () => {
    saveReadingEntry('user-1', 'session-1', 'upload-a', 'Paper A', { page: 4, scale: 1.5 });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    // StrictMode replays every effect (cleanup + mount) on the same hook
    // instance, which is the only case where the restore bookkeeping survives a
    // cleanup. `render` triggers that replay; `renderHook` does not here.
    const flowProps = props();
    // Captured through a spy instead of an outer mutable variable: the probe
    // reports every render so the assertions see the replayed state.
    const onSnapshot = vi.fn();
    const Probe = () => {
      onSnapshot(usePdfAssistFlow(flowProps as never));
      return null;
    };
    const latestFlow = () =>
      onSnapshot.mock.calls.at(-1)?.[0] as ReturnType<typeof usePdfAssistFlow> | undefined;

    await act(async () => {
      render(
        <StrictMode>
          <Probe />
        </StrictMode>,
      );
    });

    await waitFor(() => expect(latestFlow()?.uploadId).toBe('upload-a'));
    expect(latestFlow()?.readingState).toEqual({ page: 4, scale: 1.5 });
    expect(latestFlow()?.documentName).toBe('Paper A');
    // The replay survived: the document was fetched again after the cleanup
    // invalidated the first attempt, and only one live blob remains.
    expect(vi.mocked(documentApi.getUpload)).toHaveBeenCalledTimes(2);
    expect(createdBlobUrls).toHaveLength(1);
    expect(revokeObjectURL).not.toHaveBeenCalled();
  });
});

describe('usePdfAssistFlow translation start ownership', () => {
  it('keeps one create request across close/reopen and recovers the job afterwards', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const uploadA = uploadOf('upload-a', 'Paper A');

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });

    const start = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob).mockReturnValue(start.promise);
    vi.mocked(documentApi.getTranslationJob).mockReset();

    act(() => {
      flow.result.current.translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);
    expect(flow.result.current.translation.isStarting).toBe(true);

    // Close while the create response is still pending: the start is not lost.
    act(() => {
      flow.result.current.closePdf();
    });
    const closeMessage = String(flowProps.appendChatMessage.mock.calls.at(-1)?.[0]?.text);
    expect(closeMessage).toContain('启动结果尚未返回');

    // Reopen the same document and press start again: it must wait on the SAME
    // in-flight request instead of creating a second server job.
    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadA);
    });
    act(() => {
      flow.result.current.translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);

    await act(async () => {
      start.resolve(runningJob('job-1', 'upload-a'));
      await Promise.resolve();
      await Promise.resolve();
    });

    // The response is persisted for its scope even though the document was
    // closed while it was pending, and it applies to the reopened document.
    expect(readTranslationJobRecord('user-1', 'session-1', 'upload-a')?.jobId).toBe('job-1');
    expect(flow.result.current.translation.job?.id).toBe('job-1');
    expect(flow.result.current.translation.isStarting).toBe(false);
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);
  });

  it('never lets document A release the start lock owned by document B', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });

    const startA = deferred<TranslationJob>();
    const startB = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob)
      .mockReturnValueOnce(startA.promise)
      .mockReturnValueOnce(startB.promise);

    act(() => {
      flow.result.current.translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);

    // Switch to document B while A's start is still pending.
    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-b', 'Paper B'));
    });
    act(() => {
      flow.result.current.translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(2);

    // A's late response must persist under A only and must not unlock B.
    await act(async () => {
      startA.resolve(runningJob('job-a', 'upload-a'));
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readTranslationJobRecord('user-1', 'session-1', 'upload-a')?.jobId).toBe('job-a');
    expect(flow.result.current.uploadId).toBe('upload-b');
    expect(flow.result.current.translation.job).toBeNull();

    // B is still starting, so a third click must not create yet another job.
    act(() => {
      flow.result.current.translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(2);

    await act(async () => {
      startB.resolve(runningJob('job-b', 'upload-b'));
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(flow.result.current.translation.job?.id).toBe('job-b');
    expect(readTranslationJobRecord('user-1', 'session-1', 'upload-b')?.jobId).toBe('job-b');
  });
});

describe('usePdfAssistFlow translation page ordering', () => {
  it('ignores a slow page 1 response that arrives after page 2 of the same document', async () => {
    const flowProps = props();
    const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
      initialProps: flowProps,
    });
    fetchMock.mockImplementation(async () => pdfResponse('A'));

    await act(async () => {
      await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    const job = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob).mockReturnValue(job.promise);
    act(() => {
      flow.result.current.translation.onStart();
    });
    await act(async () => {
      job.resolve(runningJob('job-1', 'upload-a'));
      await Promise.resolve();
      await Promise.resolve();
    });

    const page1 = deferred<string>();
    const page2 = deferred<string>();
    vi.mocked(documentApi.getTranslationPage)
      .mockReturnValueOnce(page1.promise)
      .mockReturnValueOnce(page2.promise);

    act(() => {
      flow.result.current.translation.onOpenPage(1);
    });
    act(() => {
      flow.result.current.translation.onOpenPage(2);
    });
    expect(vi.mocked(documentApi.getTranslationPage)).toHaveBeenCalledTimes(2);

    // Page 2 answers first, page 1 afterwards: the stale page 1 must not win.
    await act(async () => {
      page2.resolve('第 2 页译文');
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(flow.result.current.translation.pageText).toBe('第 2 页译文');
    expect(flow.result.current.translation.pageTextLoading).toBe(false);

    await act(async () => {
      page1.resolve('第 1 页译文');
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(flow.result.current.translation.pageText).toBe('第 2 页译文');
    expect(flow.result.current.translation.pageTextError).toBe('');
  });
});

describe('usePdfAssistFlow translation polling', () => {
  it('does not overlap polls so an older running response cannot overwrite completed', async () => {
    vi.useFakeTimers();
    try {
      const flowProps = props();
      const flow = renderHook((current: ReturnType<typeof props>) => usePdfAssistFlow(current as never), {
        initialProps: flowProps,
      });
      fetchMock.mockImplementation(async () => pdfResponse('A'));

      await act(async () => {
        await flow.result.current.openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
      });

      const start = deferred<TranslationJob>();
      vi.mocked(documentApi.createTranslationJob).mockReturnValue(start.promise);
      act(() => {
        flow.result.current.translation.onStart();
      });

      const slowRunning = deferred<TranslationJob>();
      const fastCompleted = deferred<TranslationJob>();
      vi.mocked(documentApi.getTranslationJob).mockReset();
      vi.mocked(documentApi.getTranslationJob)
        .mockReturnValueOnce(slowRunning.promise)
        .mockReturnValueOnce(fastCompleted.promise);

      await act(async () => {
        start.resolve(runningJob('job-1', 'upload-a'));
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(flow.result.current.translation.job?.status).toBe('running');

      // First poll (slow, still "running") is in flight.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1200);
      });
      expect(vi.mocked(documentApi.getTranslationJob)).toHaveBeenCalledTimes(1);

      // The interval fires again while the first poll is outstanding: gated.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1200);
        await vi.advanceTimersByTimeAsync(1200);
      });
      expect(vi.mocked(documentApi.getTranslationJob)).toHaveBeenCalledTimes(1);

      // A newer poll observes completion...
      await act(async () => {
        slowRunning.resolve(runningJob('job-1', 'upload-a'));
        await Promise.resolve();
        await Promise.resolve();
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1200);
      });
      expect(vi.mocked(documentApi.getTranslationJob)).toHaveBeenCalledTimes(2);
      await act(async () => {
        fastCompleted.resolve({ ...runningJob('job-1', 'upload-a'), status: 'completed', completed_pages: 12 });
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(flow.result.current.translation.job?.status).toBe('completed');
      expect(readTranslationJobRecord('user-1', 'session-1', 'upload-a')?.status).toBe('completed');

      // Polling stopped at the terminal state: no further requests.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(3600);
      });
      expect(vi.mocked(documentApi.getTranslationJob)).toHaveBeenCalledTimes(2);
      expect(flow.result.current.translation.job?.status).toBe('completed');
    } finally {
      vi.useRealTimers();
    }
  });
});

// The public surface a render publishes for one document. The identity-leak
// regression compares this snapshot across renders: no render may publish the
// previous owner/session's document, not even the render that already carries
// the new identity but still runs before the release effect.
interface PublicSnapshot {
  uploadId: string | null;
  pdfUrl: string | null;
  documentName: string;
  documentKey: string;
  documentInfoId: string | null;
  readingState: { page: number; scale: number };
  jobId: string | null;
  jobStatus: string;
  pageText: string;
  pageTextError: string;
  pageTextLoading: boolean;
  translationError: string;
  isStarting: boolean;
}

const snapshotOf = (flow: ReturnType<typeof usePdfAssistFlow>): PublicSnapshot => ({
  uploadId: flow.uploadId,
  pdfUrl: flow.pdfUrl,
  documentName: flow.documentName,
  documentKey: flow.documentKey,
  documentInfoId: flow.documentInfo?.upload_id ?? null,
  readingState: flow.readingState,
  jobId: flow.translation.job?.id ?? null,
  jobStatus: flow.translation.job?.status ?? '',
  pageText: flow.translation.pageText,
  pageTextError: flow.translation.pageTextError,
  pageTextLoading: flow.translation.pageTextLoading,
  translationError: flow.translation.error,
  isStarting: flow.translation.isStarting,
});

// Everything in a snapshot that names the old document/owner. Empty/neutral
// values are allowed; only a leaked identity matters.
const leakedFields = (snapshot: PublicSnapshot, oldUploadId: string, oldBlobUrl: string | null): string[] => {
  const leaks: string[] = [];
  if (snapshot.uploadId === oldUploadId) leaks.push('uploadId');
  if (oldBlobUrl && snapshot.pdfUrl === oldBlobUrl) leaks.push('pdfUrl');
  if (snapshot.documentKey.includes(oldUploadId)) leaks.push('documentKey');
  if (snapshot.documentInfoId === oldUploadId) leaks.push('documentInfo');
  if (snapshot.jobId) leaks.push('translation.job');
  if (snapshot.pageText) leaks.push('translation.pageText');
  return leaks;
};


type ProbeProps = ReturnType<typeof props> & { windowArmed?: boolean };

// Mounts the hook behind a probe. The probe reports the public snapshot of
// EVERY render — that is the point: the release of the previous owner's
// document happens in an effect, so the render that already carries the new
// identity is exactly where a leak would show up.
const mountProbe = (
  initialProps: ProbeProps,
  layoutAction?: (flow: ReturnType<typeof usePdfAssistFlow>, current: ProbeProps) => void,
) => {
  const renders: PublicSnapshot[] = [];
  const latestFlowRef = { current: null as ReturnType<typeof usePdfAssistFlow> | null };
  const Probe = (current: ProbeProps) => {
    const flow = usePdfAssistFlow(current as never);
    renders.push(snapshotOf(flow));
    // Layout effects of the SAME hook instance run before its passive effects,
    // i.e. before the identity-release effect: that is the leak window.
    useLayoutEffect(() => {
      latestFlowRef.current = flow;
      if (!current.windowArmed || !layoutAction) return;
      layoutAction(flow, current);
    }, [current, flow]);
    return null;
  };
  const latest = () => renders.at(-1) as PublicSnapshot;
  const flowNow = () => {
    const flow = latestFlowRef.current;
    if (!flow) throw new Error('flow is not mounted');
    return flow;
  };
  const view = render(<Probe {...initialProps} />);
  return { view, renders, latest, flowNow, Probe };
};

const withIdentity = (sessionId: string, extra: Record<string, unknown> = {}): ProbeProps =>
  props({ ...extra, identity: { userId: 'user-1', sessionId }, activeSessionId: sessionId });

const otherIdentity = (extra: Record<string, unknown> = {}): ProbeProps =>
  props({ ...extra, identity: { userId: 'user-2', sessionId: 'session-2' }, activeSessionId: 'session-2' });

describe('usePdfAssistFlow identity release window', () => {
  it('never publishes the previous identity document on any render after a switch', async () => {
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const { view, renders, latest, flowNow, Probe } = mountProbe(withIdentity('session-1'));

    // Build a fully populated old-identity document: blob, name, metadata,
    // reading position, a running translation job and its page text.
    await act(async () => {
      await flowNow().openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    act(() => {
      flowNow().setReadingState({ page: 9, scale: 1.5 });
    });
    const start = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob).mockReturnValue(start.promise);
    act(() => {
      flowNow().translation.onStart();
    });
    await act(async () => {
      start.resolve(runningJob('job-1', 'upload-a'));
      await Promise.resolve();
      await Promise.resolve();
    });
    vi.mocked(documentApi.getTranslationPage).mockResolvedValue('第 9 页译文');
    await act(async () => {
      flowNow().translation.onOpenPage(9);
      await Promise.resolve();
      await Promise.resolve();
    });

    const before = latest();
    expect(before.uploadId).toBe('upload-a');
    expect(before.jobId).toBe('job-1');
    expect(before.pageText).toBe('第 9 页译文');
    expect(before.documentName).toBe('Paper A');
    const oldBlobUrl = before.pdfUrl;
    expect(oldBlobUrl).toBeTruthy();

    // Switch identity/session. The effect that releases the document runs after
    // this render, so the render already carries the new owner.
    const rendersBefore = renders.length;
    await act(async () => {
      view.rerender(<Probe {...otherIdentity()} />);
    });

    const afterSwitch = renders.slice(rendersBefore);
    expect(afterSwitch.length).toBeGreaterThan(0);
    for (const snapshot of afterSwitch) {
      expect(leakedFields(snapshot, 'upload-a', oldBlobUrl)).toEqual([]);
      expect(snapshot.documentName).not.toBe('Paper A');
      expect(snapshot.readingState).toEqual(DEFAULT_READING_STATE);
      expect(snapshot.jobStatus).toBe('');
      expect(snapshot.pageTextLoading).toBe(false);
      expect(snapshot.isStarting).toBe(false);
    }
    // The release still happens for real once the effect runs.
    expect(latest().uploadId).toBeNull();
    expect(latest().pdfUrl).toBeNull();
    expect(revokeObjectURL).toHaveBeenCalledWith(oldBlobUrl);
  });

  it('refuses to submit the previous document from the render window before the release effect', async () => {
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const onAskReference = vi.fn();
    const appendSelectedTaskLog = vi.fn();
    // Fire the window actions exactly once: later renders happen after the
    // release effect, which is a different (already-empty) situation.
    let armed = true;
    const { view, flowNow, Probe } = mountProbe(
      withIdentity('session-1', { onAskReference, appendSelectedTaskLog }),
      (flow) => {
        if (!armed) return;
        armed = false;
        flow.handleAskAI('选中的原文', 3);
        flow.translation.onStart();
        void flow.translation.onOpenPage(2);
        void flow.assistWithUpload('translate', '选中的原文', 3).catch(() => undefined);
      },
    );

    await act(async () => {
      await flowNow().openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });

    await act(async () => {
      view.rerender(
        <Probe {...otherIdentity({ windowArmed: true, onAskReference, appendSelectedTaskLog })} />,
      );
    });

    expect(armed).toBe(false);
    expect(onAskReference).not.toHaveBeenCalled();
    expect(vi.mocked(documentApi.createTranslationJob)).not.toHaveBeenCalled();
    expect(vi.mocked(documentApi.getTranslationPage)).not.toHaveBeenCalled();
    expect(vi.mocked(documentApi.assistDocument)).not.toHaveBeenCalled();
    // The window is refused explicitly instead of quietly submitting the old id.
    expect(String(appendSelectedTaskLog.mock.calls[0]?.[0] ?? '')).toContain('文档已切换');
    for (const call of appendSelectedTaskLog.mock.calls) {
      expect(String(call[0])).not.toContain('Paper A');
    }
  });

  it('keeps the same document for an unchanged identity and restores it normally', async () => {
    saveReadingEntry('user-1', 'session-1', 'upload-a', 'Paper A', { page: 4, scale: 1.5 });
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const { view, renders, latest, flowNow, Probe } = mountProbe(withIdentity('session-1'));

    await waitFor(() => expect(latest().uploadId).toBe('upload-a'));
    const openedUrl = latest().pdfUrl;
    const openCalls = vi.mocked(documentApi.getUpload).mock.calls.length;
    const rendersBefore = renders.length;

    // A same-identity rerender with a fresh props/identity object (the caller
    // rebuilds it every render) must neither clear nor rebuild the document.
    await act(async () => {
      view.rerender(<Probe {...withIdentity('session-1')} />);
    });

    expect(renders.length).toBeGreaterThan(rendersBefore);
    for (const snapshot of renders.slice(rendersBefore)) {
      expect(snapshot.uploadId).toBe('upload-a');
      expect(snapshot.pdfUrl).toBe(openedUrl);
      expect(snapshot.documentName).toBe('Paper A');
      expect(snapshot.documentKey).toContain('upload-a');
      expect(snapshot.readingState).toEqual({ page: 4, scale: 1.5 });
    }
    // One reader instance: no second fetch, no revoked blob.
    expect(vi.mocked(documentApi.getUpload).mock.calls.length).toBe(openCalls);
    expect(revokeObjectURL).not.toHaveBeenCalled();

    // Closing still releases it, and the persisted position restores it.
    act(() => {
      flowNow().closePdf();
    });
    expect(latest().uploadId).toBeNull();
    expect(revokeObjectURL).toHaveBeenCalledWith(openedUrl);
    await act(async () => {
      await flowNow().openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    expect(latest().uploadId).toBe('upload-a');
    expect(latest().readingState).toEqual({ page: 4, scale: 1.5 });
  });

  it('keeps a pending translation start from rolling back across the release window', async () => {
    fetchMock.mockImplementation(async () => pdfResponse('A'));
    const { view, latest, flowNow, Probe } = mountProbe(withIdentity('session-1'));

    await act(async () => {
      await flowNow().openPdfFromUpload(uploadOf('upload-a', 'Paper A'));
    });
    const start = deferred<TranslationJob>();
    vi.mocked(documentApi.createTranslationJob).mockReturnValue(start.promise);
    act(() => {
      flowNow().translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);
    expect(latest().isStarting).toBe(true);

    // Identity switch while the create is still in flight.
    await act(async () => {
      view.rerender(<Probe {...otherIdentity()} />);
    });

    await act(async () => {
      start.resolve(runningJob('job-1', 'upload-a'));
      await Promise.resolve();
      await Promise.resolve();
    });

    // The old scope keeps its persisted result, the new identity shows nothing,
    // and the start is never repeated for it.
    expect(readTranslationJobRecord('user-1', 'session-1', 'upload-a')?.jobId).toBe('job-1');
    expect(latest().jobId).toBeNull();
    expect(latest().translationError).toBe('');
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);

    act(() => {
      flowNow().translation.onStart();
    });
    expect(vi.mocked(documentApi.createTranslationJob)).toHaveBeenCalledTimes(1);
  });
});
