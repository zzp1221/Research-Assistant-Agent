import { useCallback, useEffect, useRef, useState } from 'react';
import type {
  AssistResponse,
  ChatMessage,
  DocumentInfoResponse,
  DocumentReferenceDraft,
  TranslationJob,
  UploadedFile,
} from '../../contracts/api';
import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';
import { isPdfAttachment, resolveAttachmentContentUrl } from '../../features/pdf-viewer/pdfAttachment';
import {
  DEFAULT_READING_STATE,
  latestReadingEntryForSession,
  normalizeReadingPage,
  normalizeReadingState,
  readReadingEntry,
  readTranslationJobRecord,
  saveReadingEntry,
  saveTranslationJobRecord,
  clearTranslationJobRecord,
  type PdfReadingState,
} from '../../features/pdf-viewer/readingStateStorage';
import {
  assistDocument,
  cancelTranslationJob,
  createTranslationJob,
  getTranslationJob,
  getTranslationPage,
  getUpload,
  getUploadDocument,
} from '../../services/api/documentApi';
import type { RequestIdentity } from '../../services/api/researchAssistantApi';

interface UsePdfAssistFlowOptions {
  setPrompt: (value: string) => void;
  appendChatMessage: (message: ChatMessage) => void;
  appendSelectedTaskLog: (line: string) => void;
  identity: RequestIdentity;
  // Chat session the reader is scoped to. Reading position and translation job
  // identity are stored per user/session/upload, so a draft can never jump to a
  // document that belongs to another session.
  activeSessionId?: string | null;
  // Preferred "ask AI" path: the composer owns the draft, so the reader only
  // hands over an immutable reference snapshot.
  onAskReference?: (reference: DocumentReferenceDraft) => void;
}

const ACTIVE_JOB_STATUSES = new Set(['queued', 'running']);
const POLL_INTERVAL_MS = 1200;

const ABSENT = '';

const identityKeyOf = (ownerId: string, sessionId: string): string => `${ownerId}|${sessionId}`;

// A full-translation start is locked per owner/session/upload, the same scope a
// job record is persisted under. `promise` is the tracked request, kept so the
// lock can be released only by the exact request that owns it.
interface PendingStart {
  promise: Promise<TranslationJob | null>;
}

const startScopeKey = (ownerId: string, sessionId: string, uploadId: string): string =>
  `${ownerId}|${sessionId}|${uploadId}`;

export function usePdfAssistFlow(options: UsePdfAssistFlowOptions) {
  // `setPrompt` stays in the options for backwards compatibility; the reader
  // never writes the composer text itself, so a draft in progress is not
  // overwritten by an "ask AI" action.
  const { appendChatMessage, appendSelectedTaskLog, identity, activeSessionId, onAskReference } = options;

  // Stable scalars: every effect below depends on these instead of on the
  // caller's identity object, which is rebuilt on each render.
  const ownerId = identity?.userId ?? ABSENT;
  const sessionId = activeSessionId ?? identity?.sessionId ?? ABSENT;
  const identityKey = identityKeyOf(ownerId, sessionId);

  const identityRef = useRef(identity);
  identityRef.current = identity;
  const ownerIdRef = useRef(ownerId);
  ownerIdRef.current = ownerId;
  const sessionIdRef = useRef(sessionId);
  sessionIdRef.current = sessionId;
  const identityKeyRef = useRef(identityKey);
  const onAskReferenceRef = useRef(onAskReference);
  onAskReferenceRef.current = onAskReference;
  const appendChatMessageRef = useRef(appendChatMessage);
  appendChatMessageRef.current = appendChatMessage;
  const appendSelectedTaskLogRef = useRef(appendSelectedTaskLog);
  appendSelectedTaskLogRef.current = appendSelectedTaskLog;

  const [pdfUrl, setPdfUrlState] = useState<string | null>(null);
  const pdfUrlRef = useRef<string | null>(null);
  const [uploadId, setUploadIdState] = useState<string | null>(null);
  const uploadIdRef = useRef<string | null>(null);
  const [documentInfo, setDocumentInfo] = useState<DocumentInfoResponse | null>(null);
  const [documentName, setDocumentNameState] = useState('');
  const documentNameRef = useRef('');
  const [showSuggestions, setShowSuggestions] = useState(true);
  const [job, setJobState] = useState<TranslationJob | null>(null);
  const jobRef = useRef<TranslationJob | null>(null);
  const [translationError, setTranslationError] = useState('');
  const [pageText, setPageText] = useState('');
  const [pageTextLoading, setPageTextLoading] = useState(false);
  const [pageTextError, setPageTextError] = useState('');
  const [readingState, setReadingStateValue] = useState<PdfReadingState>(DEFAULT_READING_STATE);
  const readingStateRef = useRef<PdfReadingState>(DEFAULT_READING_STATE);
  const [documentError, setDocumentError] = useState('');

  // Every document load/close bumps the generation. Async work that started
  // under an older generation drops its result instead of overwriting the
  // document the user is looking at now.
  const generationRef = useRef(0);
  const controllerRef = useRef<AbortController | null>(null);
  const suppressRestoreRef = useRef('');
  const restoredKeyRef = useRef('');
  // In-flight translation starts keyed by owner/session/upload. A start that is
  // still pending when the reader closes keeps its lock: the server may already
  // have created the job, so a second create would duplicate it.
  const pendingStartsRef = useRef(new Map<string, PendingStart>());
  // Upload ids whose translation start is currently visible/waiting, so a
  // reopened document can show "starting" while it awaits the same request.
  const [startingScopes, setStartingScopes] = useState<Record<string, true>>({});
  // Monotonic token for getTranslationPage so a slow page 1 cannot overwrite a
  // newer page 2 of the same document. The scope is captured with the token so
  // a stale completion never stops the current request's loading flag.
  const pageRequestRef = useRef({ seq: 0, scope: '' });
  // One poll in flight per document/job: overlapping polls could apply an older
  // response after a newer one and roll a completed job back to running.
  const pollStateRef = useRef<{ key: string; inFlight: boolean }>({ key: '', inFlight: false });
  const restoredJobKeyRef = useRef('');
  const referenceSeqRef = useRef(0);
  // Owner/session the open document was loaded under. Persistence uses this
  // scope, never the live one, so switching session cannot write the previous
  // session's document into the new session's reading entries.
  const documentScopeRef = useRef<{ ownerId: string; sessionId: string } | null>(null);

  // Releasing the previous owner's document happens in an effect, i.e. after the
  // render that already carries the new owner/session. That render still holds
  // the previous document's state, and stable callbacks cannot see it, so both
  // re-derive the same answer from the refs: the open document is only usable
  // while the scope it was opened under is still the live owner/session.
  // Reading refs during render is safe — they are assigned before this point —
  // and nothing here calls setState, so a document is never cached in state and
  // one identity keeps one stable reader instance.
  //
  // A null scope means "no document is open" (never opened, or already
  // released), which is not stale: only a document filed under a *different*
  // scope can leak into another identity.
  const isDocumentScopeStale = useCallback((): boolean => {
    const scope = documentScopeRef.current;
    if (!scope) return false;
    return scope.ownerId !== ownerIdRef.current || scope.sessionId !== sessionIdRef.current;
  }, []);
  const documentScopeStale = isDocumentScopeStale();

  const replacePdfUrl = useCallback((next: string | null) => {
    const previous = pdfUrlRef.current;
    if (previous?.startsWith('blob:') && previous !== next) {
      URL.revokeObjectURL(previous);
    }
    pdfUrlRef.current = next;
    setPdfUrlState(next);
  }, []);

  const setUploadId = useCallback((next: string | null) => {
    uploadIdRef.current = next;
    setUploadIdState(next);
  }, []);

  const setDocumentName = useCallback((next: string) => {
    documentNameRef.current = next;
    setDocumentNameState(next);
  }, []);

  const setJob = useCallback((next: TranslationJob | null) => {
    jobRef.current = next;
    setJobState(next);
  }, []);

  const setReadingState = useCallback(
    (patch: Partial<PdfReadingState> | ((previous: PdfReadingState) => Partial<PdfReadingState>)) => {
      setReadingStateValue((previous) => {
        const raw = typeof patch === 'function' ? patch(previous) : patch;
        // Undefined keys mean "keep the current value", not "reset to default".
        const delta: Partial<PdfReadingState> = {};
        if (raw?.page !== undefined) delta.page = raw.page;
        if (raw?.scale !== undefined) delta.scale = raw.scale;
        const merged = normalizeReadingState({ ...previous, ...delta });
        if (!merged) return previous;
        if (merged.page === previous.page && merged.scale === previous.scale) return previous;
        return merged;
      });
    },
    [],
  );

  useEffect(() => {
    readingStateRef.current = readingState;
  }, [readingState]);

  // Reading position lives per user/session/upload; only scalars are persisted.
  // The scope is the one the document was opened under, so a session switch
  // releases the document instead of re-filing it under the new session.
  useEffect(() => {
    const scope = documentScopeRef.current;
    if (!uploadId || !scope) return;
    saveReadingEntry(scope.ownerId, scope.sessionId, uploadId, documentName, readingState);
  }, [documentName, readingState, uploadId]);

  // Unmount: invalidate every in-flight request (so a pending download cannot
  // create a blob URL nobody revokes) and free the current blob. The restore
  // bookkeeping is reset too, so React StrictMode's effect cleanup/replay still
  // restores the persisted document on the replayed mount.
  useEffect(() => () => {
    generationRef.current += 1;
    controllerRef.current?.abort();
    controllerRef.current = null;
    if (pdfUrlRef.current?.startsWith('blob:')) {
      URL.revokeObjectURL(pdfUrlRef.current);
    }
    pdfUrlRef.current = null;
    suppressRestoreRef.current = '';
    restoredKeyRef.current = '';
    pageRequestRef.current = { seq: pageRequestRef.current.seq + 1, scope: '' };
    pollStateRef.current = { key: '', inFlight: false };
  }, []);

  const beginRequest = useCallback(() => {
    generationRef.current += 1;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    return { generation: generationRef.current, controller };
  }, []);

  const setStartingScope = useCallback((scopeKey: string, active: boolean) => {
    setStartingScopes((previous) => {
      if (active) {
        if (previous[scopeKey]) return previous;
        return { ...previous, [scopeKey]: true as const };
      }
      if (!previous[scopeKey]) return previous;
      const next = { ...previous };
      delete next[scopeKey];
      return next;
    });
  }, []);

  // resetTranslationRuntime only clears what is displayed for the current
  // document. Pending start locks and their persistence live per scope, so
  // closing/opening cannot drop a start that is still in flight.
  const resetTranslationRuntime = useCallback(() => {
    setJob(null);
    setTranslationError('');
    setPageText('');
    setPageTextError('');
    setPageTextLoading(false);
    restoredJobKeyRef.current = '';
    pageRequestRef.current = { seq: pageRequestRef.current.seq + 1, scope: '' };
    pollStateRef.current = { key: '', inFlight: false };
  }, [setJob]);

  // releaseDocument invalidates every in-flight request for the current
  // document and frees the blob URL. The persisted reading position and job id
  // stay in storage so the document can be reopened where it was left.
  const releaseDocument = useCallback(() => {
    generationRef.current += 1;
    controllerRef.current?.abort();
    controllerRef.current = null;
    replacePdfUrl(null);
    setUploadId(null);
    setDocumentInfo(null);
    setDocumentName('');
    documentScopeRef.current = null;
    resetTranslationRuntime();
    setDocumentError('');
    setReadingStateValue(DEFAULT_READING_STATE);
  }, [replacePdfUrl, resetTranslationRuntime, setDocumentName, setUploadId]);

  const downloadPdfBlob = useCallback(async (upload: UploadedFile, signal: AbortSignal): Promise<Blob> => {
    const url = resolveAttachmentContentUrl(API_BASE_URL, upload.content_url);
    if (!url) throw new Error('该 PDF 没有可用的内容地址，请重新上传。');
    const response = await fetch(url, {
      credentials: 'include',
      headers: API_AUTH_TOKEN ? { Authorization: `Bearer ${API_AUTH_TOKEN}` } : undefined,
      signal,
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const blob = await response.blob();
    if (!blob.size) throw new Error('empty PDF');
    return blob;
  }, []);

  const applyDocument = useCallback(
    (
      upload: UploadedFile,
      blob: Blob,
      generation: number,
      seed: { page?: number; scale?: number; documentName?: string },
    ) => {
      resetTranslationRuntime();
      setDocumentError('');
      const stored = readReadingEntry(ownerIdRef.current, sessionIdRef.current, upload.id);
      const nextState = normalizeReadingState({
        page: seed.page ?? stored?.page ?? DEFAULT_READING_STATE.page,
        scale: seed.scale ?? stored?.scale ?? DEFAULT_READING_STATE.scale,
      }) ?? DEFAULT_READING_STATE;
      readingStateRef.current = nextState;
      setReadingStateValue(nextState);
      const name = seed.documentName || upload.name || stored?.documentName || '';
      setDocumentName(name);
      documentScopeRef.current = { ownerId: ownerIdRef.current, sessionId: sessionIdRef.current };
      setDocumentInfo(null);
      setUploadId(upload.id);
      replacePdfUrl(URL.createObjectURL(blob));
      suppressRestoreRef.current = '';
      restoredJobKeyRef.current = '';
      // Document metadata is cheap and cached server-side after upload.
      getUploadDocument(upload.id, identityRef.current)
        .then((info) => {
          if (generation !== generationRef.current || uploadIdRef.current !== upload.id) return;
          setDocumentInfo(info);
          // The upload name is the stable document identity; server metadata only
          // fills a still-empty name so "询问 AI" never shows a renamed document.
          if (info?.name && !documentNameRef.current) setDocumentName(info.name);
        })
        .catch((documentError) => {
          console.warn('[pdf] failed to load document metadata', documentError);
        });
    },
    [replacePdfUrl, resetTranslationRuntime, setDocumentName, setUploadId],
  );

  const openDocumentByIdentity = useCallback(
    async (targetUploadId: string, seed: { page?: number; scale?: number; documentName?: string } = {}) => {
      if (!targetUploadId) return;
      // Same document: never rebuild the blob or the reader instance. Only when
      // the document still belongs to the live owner/session — a release is
      // queued in an effect, so this window must not be treated as "open".
      if (!isDocumentScopeStale() && uploadIdRef.current === targetUploadId && pdfUrlRef.current) {
        if (Number.isFinite(seed.page) || Number.isFinite(seed.scale)) {
          setReadingState({ page: seed.page, scale: seed.scale });
        }
        return;
      }
      const { generation, controller } = beginRequest();
      try {
        // Re-verify the upload under the current identity before touching the
        // reader: a stale reference must not render another owner's document.
        const upload = await getUpload(targetUploadId);
        if (generation !== generationRef.current || controller.signal.aborted) return;
        if (!isPdfAttachment(upload)) throw new Error('该附件不是 PDF，无法在阅读器中打开。');
        const blob = await downloadPdfBlob(upload, controller.signal);
        if (generation !== generationRef.current || controller.signal.aborted) return;
        applyDocument(upload, blob, generation, seed);
      } catch (error) {
        if (generation !== generationRef.current || controller.signal.aborted) return;
        console.warn('[pdf] failed to open document', error);
        const reason = error instanceof Error && error.message ? error.message : '未知错误';
        const message = `无法恢复该文献（${reason}）；请确认文件仍属于当前身份且会话有访问权限。`;
        setDocumentError(message);
        suppressRestoreRef.current = identityKeyRef.current;
        restoredKeyRef.current = '';
        appendChatMessageRef.current({ role: 'system', text: message });
      }
    },
    [applyDocument, beginRequest, downloadPdfBlob, isDocumentScopeStale, setReadingState],
  );

  const openPdfFromUpload = useCallback(
    async (upload: UploadedFile) => {
      if (!upload?.id) return;
      if (!isPdfAttachment(upload)) {
        appendChatMessageRef.current({ role: 'system', text: '该附件不是 PDF，无法在阅读器中打开。' });
        return;
      }
      // Opening the document already on screen keeps the blob and the viewer,
      // unless that document belongs to the previous owner/session.
      if (!isDocumentScopeStale() && uploadIdRef.current === upload.id && pdfUrlRef.current) {
        return;
      }
      const { generation, controller } = beginRequest();
      try {
        const blob = await downloadPdfBlob(upload, controller.signal);
        if (generation !== generationRef.current || controller.signal.aborted) return;
        applyDocument(upload, blob, generation, { documentName: upload.name });
      } catch (error) {
        if (generation !== generationRef.current || controller.signal.aborted) return;
        console.error(error);
        appendChatMessageRef.current({ role: 'system', text: 'PDF 读取失败；请确认文件已上传且当前会话有访问权限。' });
      }
    },
    [applyDocument, beginRequest, downloadPdfBlob, isDocumentScopeStale],
  );

  const handleAskAI = useCallback(
    (selectedText: string, page?: number) => {
      // A reference must describe the document of the *live* owner/session. In
      // the render window before the release effect runs, the old document is
      // still in state, so asking for a reference is refused instead of handing
      // the previous identity's upload id to the new one.
      if (isDocumentScopeStale()) {
        appendSelectedTaskLogRef.current('[System] 文档已切换，请先重新打开 PDF 后再使用“询问 AI”。');
        return;
      }
      const activeUploadId = uploadIdRef.current;
      const text = String(selectedText ?? '');
      if (!activeUploadId) {
        appendSelectedTaskLogRef.current('[System] 请先打开 PDF 后再使用“询问 AI”。');
        return;
      }
      const oneBasedPage = typeof page === 'number' && Number.isFinite(page)
        ? normalizeReadingPage(page)
        : readingStateRef.current.page;
      referenceSeqRef.current += 1;
      const reference: DocumentReferenceDraft = {
        id: `ref-${Date.now().toString(36)}-${referenceSeqRef.current}`,
        upload_id: activeUploadId,
        document_name: documentNameRef.current,
        page: oneBasedPage,
        quote: text,
        sessionId: sessionIdRef.current,
      };
      const askReference = onAskReferenceRef.current;
      if (askReference) {
        // The composer owns the draft: never overwrite text, never send.
        askReference(reference);
        return;
      }
      // Without a draft owner the reader only explains how to continue; the
      // unknown composer text is left untouched and nothing is sent.
      appendSelectedTaskLogRef.current(
        `[System] 已获取“${reference.document_name || '当前文献'} · 第 ${reference.page} 页”的引用；请输入框中继续提问，现有输入内容不会被覆盖。`,
      );
    },
    [isDocumentScopeStale],
  );

  const assistWithUpload = useCallback(
    async (mode: 'translate' | 'explain', text: string, page: number): Promise<AssistResponse> => {
      // The selection belongs to the document that is on screen under the live
      // owner/session; a stale one must never be sent as this identity.
      if (isDocumentScopeStale()) throw new Error('文档已切换，已忽略上一次解释结果。');
      const activeUploadId = uploadIdRef.current;
      if (!activeUploadId) throw new Error('请先打开 PDF 再使用划词翻译。');
      const generation = generationRef.current;
      const response = await assistDocument(
        activeUploadId,
        { mode, text, page, context_pages: 1 },
        identityRef.current,
      );
      if (generation !== generationRef.current || uploadIdRef.current !== activeUploadId) {
        // A slow explanation must never surface over a newer document.
        throw new Error('文档已切换，已忽略上一次解释结果。');
      }
      return response;
    },
    [isDocumentScopeStale],
  );

  const closePdf = useCallback(() => {
    const activeJob = jobRef.current;
    const closingUploadId = uploadIdRef.current;
    // The stored entry belongs to the scope the document was opened under, never
    // to the ambient one: a close that lands in the release window must not file
    // a previous session's job under the new identity.
    const closingScope = documentScopeRef.current;
    const persistentOwner = closingScope?.ownerId ?? ownerIdRef.current;
    const persistentSession = closingScope?.sessionId ?? sessionIdRef.current;
    if (activeJob && closingUploadId && ACTIVE_JOB_STATUSES.has(activeJob.status)) {
      saveTranslationJobRecord(persistentOwner, persistentSession, closingUploadId, {
        jobId: activeJob.id,
        status: activeJob.status,
      });
      appendChatMessageRef.current({
        role: 'system',
        text: `已关闭文档；全文翻译任务 ${activeJob.id} 仍在服务端继续（状态：${activeJob.status}）。重新打开该文档可继续查看进度，取消请在重新打开后操作。`,
      });
    } else if (
      closingUploadId &&
      pendingStartsRef.current.has(startScopeKey(persistentOwner, persistentSession, closingUploadId))
    ) {
      // The create response has not arrived yet, so the job id is unknown. The
      // request keeps its lock and will persist its result under this scope.
      appendChatMessageRef.current({
        role: 'system',
        text: '已关闭文档；全文翻译的启动结果尚未返回，服务端可能已继续创建任务。重新打开该文档可确认进度，届时不会重复发起。',
      });
    }
    releaseDocument();
    // The reading position is kept, but this close must not immediately reopen
    // the document on its own.
    suppressRestoreRef.current = identityKeyRef.current;
    restoredKeyRef.current = '';
  }, [releaseDocument]);

  const restoreReference = useCallback(
    async (reference: DocumentReferenceDraft) => {
      const refUploadId = reference?.upload_id ?? '';
      if (!refUploadId) return;
      const currentSession = sessionIdRef.current;
      if (reference.sessionId && currentSession && reference.sessionId !== currentSession) {
        appendChatMessageRef.current({
          role: 'system',
          text: '该引用属于另一个会话，未切换文档；请回到原会话后继续阅读。',
        });
        return;
      }
      const seed = {
        page: normalizeReadingPage(reference.page ?? DEFAULT_READING_STATE.page),
        documentName: reference.document_name || '',
      };
      await openDocumentByIdentity(refUploadId, seed);
    },
    [openDocumentByIdentity],
  );

  // Identity/session change: drop the previous owner's document immediately so
  // no cached text is shown for the new identity.
  useEffect(() => {
    if (identityKeyRef.current === identityKey) return;
    identityKeyRef.current = identityKey;
    releaseDocument();
    suppressRestoreRef.current = '';
    restoredKeyRef.current = '';
  }, [identityKey, releaseDocument]);

  // Refresh / session switch: once the server user id and the active session are
  // both stable, reopen the session's last document from its upload identity.
  useEffect(() => {
    if (!ownerId || !sessionId) return;
    if (uploadIdRef.current) return;
    if (suppressRestoreRef.current === identityKey) return;
    const entry = latestReadingEntryForSession(ownerId, sessionId);
    if (!entry) return;
    const key = `${identityKey}|${entry.uploadId}`;
    if (restoredKeyRef.current === key) return;
    restoredKeyRef.current = key;
    void openDocumentByIdentity(entry.uploadId, {
      page: entry.page,
      scale: entry.scale,
      documentName: entry.documentName,
    });
  }, [identityKey, openDocumentByIdentity, ownerId, sessionId]);

  const activeJobId = job?.id ?? ABSENT;
  const activeJobStatus = job?.status ?? ABSENT;

  // Poll only while a job is active; stop when it reaches a terminal state.
  // Polls are gated so they never overlap: an older response cannot arrive
  // after a newer one and roll a completed job back to running.
  useEffect(() => {
    if (!activeJobId || !uploadId || !ACTIVE_JOB_STATUSES.has(activeJobStatus)) return;
    const generation = generationRef.current;
    const pollingUploadId = uploadId;
    const pollKey = `${pollingUploadId}|${activeJobId}|${generation}`;
    let cancelled = false;
    pollStateRef.current = { key: pollKey, inFlight: false };
    const isStale = () =>
      cancelled
      || pollStateRef.current.key !== pollKey
      || generation !== generationRef.current
      || uploadIdRef.current !== pollingUploadId;
    const timer = window.setInterval(() => {
      if (isStale() || pollStateRef.current.inFlight) return;
      pollStateRef.current.inFlight = true;
      void getTranslationJob(pollingUploadId, activeJobId, identityRef.current)
        .then((current) => {
          if (isStale()) return;
          setJob(current);
          saveTranslationJobRecord(ownerIdRef.current, sessionIdRef.current, pollingUploadId, {
            jobId: activeJobId,
            status: current.status,
          });
        })
        .catch((pollError) => {
          console.warn('[pdf] translation job poll failed', pollError);
        })
        .finally(() => {
          if (pollStateRef.current.key === pollKey) pollStateRef.current.inFlight = false;
        });
    }, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      pollStateRef.current = { key: '', inFlight: false };
      window.clearInterval(timer);
    };
  }, [activeJobId, activeJobStatus, setJob, uploadId]);

  // Reopening a document (or refreshing) resumes the persisted server job
  // instead of starting a second one. A start that is still in flight for this
  // scope also blocks a competing restore: that request will persist its own
  // result and apply it when the scope is on screen again.
  useEffect(() => {
    if (!uploadId || !ownerId) return;
    const record = readTranslationJobRecord(ownerId, sessionId, uploadId);
    if (!record?.jobId) return;
    if (jobRef.current?.id === record.jobId) return;
    // A still-pending start for this scope will persist and apply its own
    // result; restoring in parallel would race it.
    if (pendingStartsRef.current.has(startScopeKey(ownerId, sessionId, uploadId))) return;
    const key = `${ownerId}|${sessionId}|${uploadId}|${record.jobId}`;
    if (restoredJobKeyRef.current === key) return;
    restoredJobKeyRef.current = key;
    let cancelled = false;
    const generation = generationRef.current;
    getTranslationJob(uploadId, record.jobId, identityRef.current)
      .then((restored) => {
        if (cancelled || generation !== generationRef.current || uploadIdRef.current !== uploadId) return;
        setJob(restored);
        saveTranslationJobRecord(ownerId, sessionId, uploadId, { jobId: restored.id, status: restored.status });
        appendSelectedTaskLogRef.current?.(
          `[System] 已恢复全文翻译任务 ${restored.id}（状态：${restored.status}），未重复发起。`,
        );
      })
      .catch(() => {
        if (cancelled) return;
        clearTranslationJobRecord(ownerId, sessionId, uploadId);
      });
    return () => {
      cancelled = true;
    };
  }, [ownerId, sessionId, setJob, uploadId]);

  const openTranslationPage = useCallback(
    async (page: number) => {
      // A page request belongs to the document on screen under the live scope.
      if (isDocumentScopeStale()) return;
      const activeUploadId = uploadIdRef.current;
      const activeJob = jobRef.current;
      if (!activeUploadId || !activeJob) return;
      const generation = generationRef.current;
      // Per-request token, scoped to the document: only the newest page request
      // of the document on screen may write text, error or the loading flag.
      const scope = `${activeJob.id}|${activeUploadId}`;
      pageRequestRef.current = { seq: pageRequestRef.current.seq + 1, scope };
      const seq = pageRequestRef.current.seq;
      const isCurrent = () =>
        pageRequestRef.current.seq === seq && pageRequestRef.current.scope === scope;
      setPageText('');
      setPageTextError('');
      setPageTextLoading(true);
      try {
        const text = await getTranslationPage(activeUploadId, activeJob.id, page, identityRef.current);
        if (!isCurrent() || generation !== generationRef.current || uploadIdRef.current !== activeUploadId) return;
        setPageText(text);
      } catch (pageError) {
        if (!isCurrent() || generation !== generationRef.current || uploadIdRef.current !== activeUploadId) return;
        setPageTextError(pageError instanceof Error ? pageError.message : '读取译文失败。');
      } finally {
        if (isCurrent() && generation === generationRef.current && uploadIdRef.current === activeUploadId) {
          setPageTextLoading(false);
        }
      }
    },
    [isDocumentScopeStale],
  );

  // Applies a completed start to the UI only when the same document is on
  // screen; persistence happens regardless, keyed by the scope it was started
  // for, so a response that arrived while the reader was closed is still
  // recoverable on reopen.
  const applyStartedJob = useCallback(
    (created: TranslationJob, owner: string, session: string, startedUploadId: string) => {
      saveTranslationJobRecord(owner, session, startedUploadId, {
        jobId: created.id,
        status: created.status,
      });
      // The document must still be open under the scope the start was made for.
      const scope = documentScopeRef.current;
      if (
        scope?.ownerId === owner
        && scope.sessionId === session
        && uploadIdRef.current === startedUploadId
      ) {
        setJob(created);
        appendSelectedTaskLogRef.current(
          `[System] 已启动全文翻译任务 ${created.id}（${created.total_pages} 页）。`,
        );
      }
    },
    [setJob],
  );

  const startFullTranslation = useCallback(async () => {
    // A start must describe the document of the live owner/session, so the
    // release window cannot create the previous identity's job for the new one.
    if (isDocumentScopeStale()) {
      setTranslationError('文档已切换，请重新打开 PDF 后再启动全文翻译。');
      return;
    }
    const activeUploadId = uploadIdRef.current;
    if (!activeUploadId) {
      setTranslationError('请先打开 PDF。');
      return;
    }
    const existing = jobRef.current;
    if (existing && ACTIVE_JOB_STATUSES.has(existing.status)) return;
    const owner = ownerIdRef.current;
    const session = sessionIdRef.current;
    const scopeKey = startScopeKey(owner, session, activeUploadId);
    // A start that is still pending for this exact scope owns the lock: wait for
    // it instead of creating a second server job.
    if (pendingStartsRef.current.has(scopeKey)) return;
    const pending = createTranslationJob(activeUploadId, { target_lang: 'zh' }, identityRef.current)
      .then((created) => {
        applyStartedJob(created, owner, session, activeUploadId);
        return created;
      })
      .catch((error: unknown) => {
        const scope = documentScopeRef.current;
        if (
          scope?.ownerId === owner
          && scope.sessionId === session
          && uploadIdRef.current === activeUploadId
        ) {
          setTranslationError(error instanceof Error && error.message ? error.message : '启动全文翻译失败。');
        }
        return null;
      })
      .finally(() => {
        // Release only this request's own lock: a start from another document
        // must never be unlocked by this finally.
        if (pendingStartsRef.current.get(scopeKey)?.promise === pending) {
          pendingStartsRef.current.delete(scopeKey);
        }
        setStartingScope(scopeKey, false);
      });
    pendingStartsRef.current.set(scopeKey, { promise: pending });
    setStartingScope(scopeKey, true);
    setTranslationError('');
    await pending;
  }, [applyStartedJob, isDocumentScopeStale, setStartingScope]);

  const cancelFullTranslation = useCallback(async () => {
    if (isDocumentScopeStale()) return;
    const activeUploadId = uploadIdRef.current;
    const activeJob = jobRef.current;
    if (!activeUploadId || !activeJob) return;
    const generation = generationRef.current;
    const cancelScope = documentScopeRef.current;
    try {
      const canceled = await cancelTranslationJob(activeUploadId, activeJob.id, identityRef.current);
      if (generation !== generationRef.current || uploadIdRef.current !== activeUploadId) return;
      setJob(canceled);
      if (cancelScope) {
        saveTranslationJobRecord(cancelScope.ownerId, cancelScope.sessionId, activeUploadId, {
          jobId: canceled.id,
          status: canceled.status,
        });
      }
    } catch (error) {
      if (generation !== generationRef.current || uploadIdRef.current !== activeUploadId) return;
      setTranslationError(error instanceof Error ? error.message : '取消全文翻译失败。');
    }
  }, [isDocumentScopeStale, setJob]);

  // Renders the public surface. When the render already carries a new
  // owner/session but the release effect has not run yet, every field that
  // belongs to the previous document is masked for this render only: nothing is
  // written to state, so the next render (after the release effect, or on the
  // same identity) reports the real values again and one identity keeps one
  // stable reader instance.
  const documentKey = !documentScopeStale && uploadId
    ? `${ownerId || 'anonymous'}::${sessionId || 'session'}::${uploadId}`
    : '';
  // Only the visible document's own pending start shows as "starting"; another
  // document's in-flight start stays invisible but keeps its lock.
  const isStartingTranslation = Boolean(
    !documentScopeStale && uploadId && startingScopes[startScopeKey(ownerId, sessionId, uploadId)],
  );

  return {
    pdfUrl: documentScopeStale ? null : pdfUrl,
    setPdfUrl: replacePdfUrl,
    uploadId: documentScopeStale ? null : uploadId,
    documentInfo: documentScopeStale ? null : documentInfo,
    documentName: documentScopeStale ? '' : documentName,
    documentKey,
    documentError: documentScopeStale ? '' : documentError,
    readingState: documentScopeStale ? DEFAULT_READING_STATE : readingState,
    setReadingState,
    openPdfFromUpload,
    closePdf,
    restoreReference,
    showSuggestions,
    setShowSuggestions,
    handleAskAI,
    assistWithUpload,
    translation: {
      job: documentScopeStale ? null : job,
      isStarting: isStartingTranslation,
      error: documentScopeStale ? '' : translationError,
      pageText: documentScopeStale ? '' : pageText,
      pageTextLoading: documentScopeStale ? false : pageTextLoading,
      pageTextError: documentScopeStale ? '' : pageTextError,
      onStart: () => void startFullTranslation(),
      onCancel: () => void cancelFullTranslation(),
      onOpenPage: (page: number) => void openTranslationPage(page),
    },
  };
}
