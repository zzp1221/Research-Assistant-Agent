import { useCallback, useEffect, useRef, useState } from 'react';
import type {
  AssistResponse,
  ChatMessage,
  DocumentInfoResponse,
  TranslationJob,
  UploadedFile,
} from '../../contracts/api';
import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';
import { isPdfAttachment, resolveAttachmentContentUrl } from '../../features/pdf-viewer/pdfAttachment';
import {
  assistDocument,
  cancelTranslationJob,
  createTranslationJob,
  getTranslationJob,
  getTranslationPage,
  getUploadDocument,
} from '../../services/api/documentApi';
import type { RequestIdentity } from '../../services/api/researchAssistantApi';

interface UsePdfAssistFlowOptions {
  setPrompt: (value: string) => void;
  appendChatMessage: (message: ChatMessage) => void;
  appendSelectedTaskLog: (line: string) => void;
  identity: RequestIdentity;
}

const ACTIVE_JOB_STATUSES = new Set(['queued', 'running']);

export function usePdfAssistFlow(options: UsePdfAssistFlowOptions) {
  const { setPrompt, appendChatMessage, appendSelectedTaskLog, identity } = options;
  const [pdfUrl, setPdfUrl] = useState<string | null>(null);
  const pdfUrlRef = useRef<string | null>(null);
  const [uploadId, setUploadId] = useState<string | null>(null);
  const [documentInfo, setDocumentInfo] = useState<DocumentInfoResponse | null>(null);
  const [showSuggestions, setShowSuggestions] = useState(true);
  const [job, setJob] = useState<TranslationJob | null>(null);
  const [isStartingTranslation, setIsStartingTranslation] = useState(false);
  const [translationError, setTranslationError] = useState('');
  const [pageText, setPageText] = useState('');
  const [pageTextLoading, setPageTextLoading] = useState(false);
  const [pageTextError, setPageTextError] = useState('');
  const activeJobRef = useRef<string | null>(null);

  const handleAskAI = useCallback(
    (selectedText: string) => {
      setPrompt(`请帮我详细解释这篇文献中的这段内容：\n"${selectedText}"`);
      appendSelectedTaskLog('[System] 已获取划词内容，已填入输入框；发送后会由 Research Assistant Agent 结合计划上下文回答。');
    },
    [appendSelectedTaskLog, setPrompt],
  );

  const assistWithUpload = useCallback(
    async (mode: 'translate' | 'explain', text: string, page: number): Promise<AssistResponse> => {
      if (!uploadId) throw new Error('请先打开 PDF 再使用划词翻译。');
      return assistDocument(uploadId, { mode, text, page, context_pages: 1 }, identity);
    },
    [identity, uploadId],
  );

  const replacePdfUrl = useCallback((next: string | null) => {
    const previous = pdfUrlRef.current;
    if (previous?.startsWith('blob:') && previous !== next) {
      URL.revokeObjectURL(previous);
    }
    pdfUrlRef.current = next;
    setPdfUrl(next);
  }, []);

  useEffect(() => () => {
    if (pdfUrlRef.current?.startsWith('blob:')) {
      URL.revokeObjectURL(pdfUrlRef.current);
    }
  }, []);

  // Poll only while a job is active; stop when it reaches a terminal state.
  useEffect(() => {
    const jobId = job?.id;
    if (!jobId || !uploadId || !job.status || !ACTIVE_JOB_STATUSES.has(job.status)) return;
    let cancelled = false;
    const timer = window.setInterval(async () => {
      try {
        const current = await getTranslationJob(uploadId, jobId, identity);
        if (!cancelled) {
          setJob(current);
        }
      } catch (pollError) {
        console.warn('[pdf] translation job poll failed', pollError);
      }
    }, 1200);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [identity, job?.id, job?.status, uploadId]);

  const openTranslationPage = useCallback(
    async (page: number) => {
      if (!uploadId || !job) return;
      setPageText('');
      setPageTextError('');
      setPageTextLoading(true);
      try {
        const text = await getTranslationPage(uploadId, job.id, page, identity);
        setPageText(text);
      } catch (pageError) {
        setPageTextError(pageError instanceof Error ? pageError.message : '读取译文失败。');
      } finally {
        setPageTextLoading(false);
      }
    },
    [identity, job, uploadId],
  );

  const startFullTranslation = useCallback(async () => {
    if (!uploadId) {
      setTranslationError('请先打开 PDF。');
      return;
    }
    setIsStartingTranslation(true);
    setTranslationError('');
    try {
      const created = await createTranslationJob(uploadId, { target_lang: 'zh' }, identity);
      activeJobRef.current = created.id;
      setJob(created);
      appendSelectedTaskLog(`[System] 已启动全文翻译任务 ${created.id}（${created.total_pages} 页）。`);
    } catch (error) {
      setTranslationError(error instanceof Error ? error.message : '启动全文翻译失败。');
    } finally {
      setIsStartingTranslation(false);
    }
  }, [appendSelectedTaskLog, identity, uploadId]);

  const cancelFullTranslation = useCallback(async () => {
    if (!uploadId || !job) return;
    try {
      const canceled = await cancelTranslationJob(uploadId, job.id, identity);
      setJob(canceled);
    } catch (error) {
      setTranslationError(error instanceof Error ? error.message : '取消全文翻译失败。');
    }
  }, [identity, job, uploadId]);

  const resetDocumentState = useCallback(() => {
    setJob(null);
    setTranslationError('');
    setPageText('');
    setPageTextError('');
    activeJobRef.current = null;
  }, []);

  const openPdfFromUpload = useCallback(
    async (upload: UploadedFile) => {
      if (!isPdfAttachment(upload)) {
        appendChatMessage({ role: 'system', text: '该附件不是 PDF，无法在阅读器中打开。' });
        return;
      }
      const url = resolveAttachmentContentUrl(API_BASE_URL, upload.content_url);
      if (!url) {
        appendChatMessage({ role: 'system', text: '该 PDF 没有可用的内容地址，请重新上传。' });
        return;
      }
      try {
        const response = await fetch(url, {
          credentials: 'include',
          headers: API_AUTH_TOKEN ? { Authorization: `Bearer ${API_AUTH_TOKEN}` } : undefined,
        });
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        const blob = await response.blob();
        if (!blob.size) {
          throw new Error('empty PDF');
        }
        resetDocumentState();
        setUploadId(upload.id);
        setDocumentInfo(null);
        replacePdfUrl(URL.createObjectURL(blob));
        // Document metadata is cheap and cached server-side after upload.
        getUploadDocument(upload.id, identity)
          .then((info) => setDocumentInfo(info))
          .catch((documentError) => {
            console.warn('[pdf] failed to load document metadata', documentError);
          });
      } catch (error) {
        console.error(error);
        appendChatMessage({ role: 'system', text: 'PDF 读取失败；请确认文件已上传且当前会话有访问权限。' });
      }
    },
    [appendChatMessage, identity, replacePdfUrl, resetDocumentState],
  );

  const closePdf = useCallback(() => {
    resetDocumentState();
    setUploadId(null);
    setDocumentInfo(null);
    replacePdfUrl(null);
  }, [replacePdfUrl, resetDocumentState]);

  return {
    pdfUrl,
    setPdfUrl: replacePdfUrl,
    uploadId,
    documentInfo,
    openPdfFromUpload,
    closePdf,
    showSuggestions,
    setShowSuggestions,
    handleAskAI,
    assistWithUpload,
    translation: {
      job,
      isStarting: isStartingTranslation,
      error: translationError,
      pageText,
      pageTextLoading,
      pageTextError,
      onStart: () => void startFullTranslation(),
      onCancel: () => void cancelFullTranslation(),
      onOpenPage: (page: number) => void openTranslationPage(page),
    },
  };
}
