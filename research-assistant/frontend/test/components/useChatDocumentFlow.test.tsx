import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import {
  MAX_DOCUMENT_QUOTE_LENGTH,
  MAX_DOCUMENT_REFERENCES,
  useResearchAssistantChatFlow,
} from '../../src/app/hooks/useResearchAssistantChatFlow';
import type { DocumentReferenceDraft, PlanGraph, UploadedFile } from '../../src/contracts/api';
import * as api from '../../src/services/api/researchAssistantApi';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  PLAN_CREATE_TIMEOUT_MESSAGE: '计划创建超时；请稍后刷新计划库确认是否已创建。',
  chat: vi.fn(),
  createPlan: vi.fn(),
  getMe: vi.fn(),
  getPlan: vi.fn(),
  listPlans: vi.fn(),
  uploadFile: vi.fn(),
}));

const noop = () => undefined;

const makePlanGraph = (overrides: Partial<PlanGraph> = {}): PlanGraph => ({
  id: 'plan-reading',
  user_intent: '复现论文实验',
  intent_type: 'Code_Execution',
  status: 'ready',
  trace_id: 'trace-reading',
  approval: { required: false, status: 'not_required' },
  budget: { max_task_attempts: 2, max_duration_seconds: 600 },
  usage: { task_attempts: 0 },
  nodes: [{ id: 'node-code', type: 'execute_code' }] as unknown as PlanGraph['nodes'],
  edges: [],
  ...overrides,
});

const makeUpload = (id: string, name = `${id}.pdf`): UploadedFile => ({
  id,
  name,
  content_type: 'application/pdf',
  size: 1024,
  sha256: `sha-${id}`,
  content_url: `/api/uploads/${id}/content`,
  created_at: new Date().toISOString(),
});

const makeReference = (sessionId: string, overrides: Partial<DocumentReferenceDraft> = {}): DocumentReferenceDraft => ({
  upload_id: 'upload-paper',
  document_name: 'attention.pdf',
  page: 3,
  quote: '固定划分是 SPLIT-73F。',
  id: `ref-${Math.random().toString(16).slice(2, 8)}`,
  sessionId,
  ...overrides,
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

const renderFlow = () => renderHook(() => useResearchAssistantChatFlow({ onPlanGraphChanged: noop }));

describe('session drafts for document references', () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.resetAllMocks();
    vi.mocked(api.getMe).mockResolvedValue({ user_id: 'user-1', session_id: 'server-session' });
    vi.mocked(api.listPlans).mockResolvedValue({ plans: [], total: 0, next_cursor: '' });
    vi.mocked(api.chat).mockResolvedValue({ response: '基于选中文字的回答。' });
    vi.mocked(api.createPlan).mockResolvedValue({ message: 'ok', plan_graph: makePlanGraph() });
  });

  it('adds a reference without overwriting the existing prompt, sending or planning', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));

    act(() => result.current.setPrompt('我原本的问题'));
    act(() => result.current.addDocumentReference(makeReference(result.current.activeSessionId!)));

    expect(result.current.prompt).toBe('我原本的问题');
    expect(result.current.documentReferences).toHaveLength(1);
    expect(result.current.documentReferences[0]).toMatchObject({
      upload_id: 'upload-paper',
      page: 3,
      sessionId: result.current.activeSessionId,
    });
    expect(api.chat).not.toHaveBeenCalled();
    expect(api.createPlan).not.toHaveBeenCalled();
  });

  it('rejects foreign, invalid-page, oversized and excess references without rewriting them', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const sessionId = result.current.activeSessionId!;

    let foreign: string = '';
    act(() => { foreign = result.current.addDocumentReference(makeReference('other-session')); });
    expect(foreign).toBe('session-mismatch');
    expect(result.current.documentReferences).toHaveLength(0);

    let badPage: string = '';
    act(() => { badPage = result.current.addDocumentReference(makeReference(sessionId, { page: 0 })); });
    expect(badPage).toBe('invalid-page');
    expect(result.current.documentReferences).toHaveLength(0);
    expect(result.current.attachmentError).toContain('页码');

    let longQuote: string = '';
    act(() => {
      longQuote = result.current.addDocumentReference(makeReference(sessionId, {
        quote: 'x'.repeat(MAX_DOCUMENT_QUOTE_LENGTH + 1),
      }));
    });
    expect(longQuote).toBe('quote-too-long');
    expect(result.current.documentReferences).toHaveLength(0);
    expect(result.current.attachmentError).toContain(String(MAX_DOCUMENT_QUOTE_LENGTH));

    for (let index = 0; index < MAX_DOCUMENT_REFERENCES; index += 1) {
      act(() => {
        result.current.addDocumentReference(makeReference(sessionId, { id: `ref-${index}`, page: index + 1 }));
      });
    }
    expect(result.current.documentReferences).toHaveLength(MAX_DOCUMENT_REFERENCES);
    let overflow: string = '';
    act(() => { overflow = result.current.addDocumentReference(makeReference(sessionId, { id: 'ref-over', page: 9 })); });
    expect(overflow).toBe('full');
    expect(result.current.documentReferences).toHaveLength(MAX_DOCUMENT_REFERENCES);
  });

  it('keeps prompt, references and attachments per session across switches', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const sessionA = result.current.activeSessionId!;

    act(() => result.current.setPrompt('A 的问题'));
    act(() => result.current.addDocumentReference(makeReference(sessionA)));
    act(() => { result.current.attachUploadToSession(makeUpload('upload-a')); });
    expect(result.current.pendingAttachments).toHaveLength(1);

    act(() => result.current.handleCreateSession());
    const sessionB = result.current.activeSessionId!;
    expect(sessionB).not.toBe(sessionA);
    expect(result.current.prompt).toBe('');
    expect(result.current.documentReferences).toHaveLength(0);
    expect(result.current.pendingAttachments).toHaveLength(0);

    act(() => result.current.setPrompt('B 的问题'));
    act(() => result.current.handleSwitchSession(sessionA));
    expect(result.current.prompt).toBe('A 的问题');
    expect(result.current.documentReferences).toHaveLength(1);
    expect(result.current.pendingAttachments.map((attachment) => attachment.id)).toEqual(['upload-a']);

    act(() => result.current.handleSwitchSession(sessionB));
    expect(result.current.prompt).toBe('B 的问题');
    expect(result.current.documentReferences).toHaveLength(0);
  });

  it('sends a reading question from a pending-attachment session to chat and keeps the attachments', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));

    act(() => { result.current.attachUploadToSession(makeUpload('upload-paper')); });
    const reference = makeReference(result.current.activeSessionId!);
    act(() => result.current.addDocumentReference(reference));
    act(() => result.current.setPrompt('这篇论文的方法是什么？'));

    await act(async () => { await result.current.handleSendMessage(); });

    expect(api.createPlan).not.toHaveBeenCalled();
    expect(api.chat).toHaveBeenCalledTimes(1);
    const [, , options] = vi.mocked(api.chat).mock.calls[0]!;
    expect(options?.documentReferences).toEqual([
      {
        upload_id: 'upload-paper',
        document_name: 'attention.pdf',
        page: 3,
        quote: '固定划分是 SPLIT-73F。',
      },
    ]);
    // A plain reading chat never consumes the staged attachment.
    expect(result.current.pendingAttachments.map((attachment) => attachment.id)).toEqual(['upload-paper']);
    // The sent reference is consumed; the sent question is cleared.
    expect(result.current.documentReferences).toHaveLength(0);
    expect(result.current.prompt).toBe('');

    const userMessage = result.current.chatHistory.find((message) => message.role === 'user');
    expect(userMessage?.documentReferences).toHaveLength(1);
  });

  it('keeps follow-up summary questions in chat without creating a plan', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));

    act(() => result.current.addDocumentReference(makeReference(result.current.activeSessionId!)));
    act(() => result.current.setPrompt('这篇论文的结论是什么？'));
    await act(async () => { await result.current.handleSendMessage(); });
    expect(api.chat).toHaveBeenCalledTimes(1);

    // No new reference this time, but the session stays in reading mode.
    act(() => result.current.setPrompt('请总结这篇论文的分析与结论 summary'));
    await act(async () => { await result.current.handleSendMessage(); });

    expect(api.createPlan).not.toHaveBeenCalled();
    expect(api.chat).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.chat).mock.calls[1]![2]?.documentReferences).toBeUndefined();
  });

  it('sends an explicit reproduce request to the plan path with the reference upload ids', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));

    act(() => { result.current.attachUploadToSession(makeUpload('upload-code')); });
    act(() => result.current.addDocumentReference(makeReference(result.current.activeSessionId!, {
      upload_id: 'upload-paper',
      id: 'ref-paper',
    })));
    act(() => result.current.setPrompt('请复现这篇论文的核心实验'));

    await act(async () => { await result.current.handleSendMessage(); });

    expect(api.chat).not.toHaveBeenCalled();
    expect(api.createPlan).toHaveBeenCalledTimes(1);
    const args = vi.mocked(api.createPlan).mock.calls[0]!;
    expect(args[0]).toBe('请复现这篇论文的核心实验');
    expect(args[2]).toEqual(['upload-code', 'upload-paper']);
    expect(result.current.pendingAttachments).toHaveLength(0);
    expect(result.current.documentReferences).toHaveLength(0);
  });

  it('still routes a plain attachment turn in a non-reading session to the plan path', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));

    act(() => { result.current.attachUploadToSession(makeUpload('upload-plain')); });
    act(() => result.current.setPrompt('帮我看看这个材料'));

    await act(async () => { await result.current.handleSendMessage(); });

    expect(api.chat).not.toHaveBeenCalled();
    expect(api.createPlan).toHaveBeenCalledTimes(1);
    expect(vi.mocked(api.createPlan).mock.calls[0]![2]).toEqual(['upload-plain']);
  });

  it('restores the question, references and attachments when the send fails', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    vi.mocked(api.chat).mockRejectedValue(new Error('network down'));

    act(() => { result.current.attachUploadToSession(makeUpload('upload-paper')); });
    act(() => result.current.addDocumentReference(makeReference(result.current.activeSessionId!)));
    act(() => result.current.setPrompt('这篇论文的结论是什么？'));

    await act(async () => { await result.current.handleSendMessage(); });

    expect(result.current.prompt).toBe('这篇论文的结论是什么？');
    expect(result.current.documentReferences).toHaveLength(1);
    expect(result.current.pendingAttachments.map((attachment) => attachment.id)).toEqual(['upload-paper']);
  });

  it('keeps edits made while a send is in flight and only clears what was sent', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const pending = deferred<Awaited<ReturnType<typeof api.chat>>>();
    vi.mocked(api.chat).mockReturnValue(pending.promise);

    const sentReference = makeReference(result.current.activeSessionId!, { id: 'ref-sent', page: 2 });
    act(() => result.current.addDocumentReference(sentReference));
    act(() => result.current.setPrompt('第一版问题'));

    act(() => { void result.current.handleSendMessage(); });
    await waitFor(() => expect(api.chat).toHaveBeenCalledTimes(1));

    act(() => result.current.setPrompt('发送中改写的第二版问题'));
    act(() => result.current.addDocumentReference(makeReference(result.current.activeSessionId!, {
      id: 'ref-late',
      page: 7,
      quote: '后加的引用',
    })));

    await act(async () => { pending.resolve({ response: '回答' }); await pending.promise; });
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.prompt).toBe('发送中改写的第二版问题');
    expect(result.current.documentReferences.map((reference) => reference.id)).toEqual(['ref-late']);
  });

  it('lands a slow answer in its own session and leaves the new session untouched', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const sessionA = result.current.activeSessionId!;
    const pending = deferred<Awaited<ReturnType<typeof api.chat>>>();
    vi.mocked(api.chat).mockReturnValue(pending.promise);

    act(() => result.current.addDocumentReference(makeReference(sessionA)));
    act(() => result.current.setPrompt('A 会话的追问'));
    act(() => { void result.current.handleSendMessage(); });
    await waitFor(() => expect(api.chat).toHaveBeenCalledTimes(1));

    act(() => result.current.handleCreateSession());
    expect(result.current.activeSessionId).not.toBe(sessionA);

    await act(async () => { pending.resolve({ response: 'A 的回答' }); await pending.promise; });
    await waitFor(() => expect(result.current.loading).toBe(false));

    const sessionBTexts = result.current.chatHistory.map((message) => message.text);
    expect(sessionBTexts.some((text) => text.includes('A 的回答'))).toBe(false);

    act(() => result.current.handleSwitchSession(sessionA));
    const sessionATexts = result.current.chatHistory.map((message) => message.text);
    expect(sessionATexts.some((text) => text.includes('A 的回答'))).toBe(true);
    expect(result.current.prompt).toBe('');
    expect(result.current.documentReferences).toHaveLength(0);
  });

  it('binds a slow upload to the session that started it', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const sessionA = result.current.activeSessionId!;
    const pending = deferred<UploadedFile>();
    vi.mocked(api.uploadFile).mockReturnValue(pending.promise);

    let uploadCall!: Promise<void>;
    act(() => {
      uploadCall = result.current.handleAttachFiles([new File(['x'], 'paper.pdf', { type: 'application/pdf' })]);
    });
    await waitFor(() => expect(api.uploadFile).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.uploadFile).mock.calls[0]![1]).toEqual({ userId: 'user-1', sessionId: sessionA });

    act(() => result.current.handleCreateSession());
    expect(result.current.activeSessionId).not.toBe(sessionA);

    await act(async () => { pending.resolve(makeUpload('upload-slow')); await uploadCall; });

    expect(result.current.pendingAttachments).toHaveLength(0);
    act(() => result.current.handleSwitchSession(sessionA));
    expect(result.current.pendingAttachments.map((attachment) => attachment.id)).toEqual(['upload-slow']);
  });

  it('submits one request for a rapid double send', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const pending = deferred<Awaited<ReturnType<typeof api.chat>>>();
    vi.mocked(api.chat).mockReturnValue(pending.promise);

    act(() => result.current.setPrompt('只发一次'));
    act(() => {
      void result.current.handleSendMessage();
      void result.current.handleSendMessage();
    });
    await waitFor(() => expect(api.chat).toHaveBeenCalledTimes(1));

    await act(async () => { pending.resolve({ response: '回答' }); await pending.promise; });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(api.chat).toHaveBeenCalledTimes(1);
    expect(api.createPlan).not.toHaveBeenCalled();
  });
});
