import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import type { ChatMessage, DocumentReferenceDraft, PlanGraph, UploadedFile } from '../../src/contracts/api';
import { useResearchAssistantChatFlow } from '../../src/app/hooks/useResearchAssistantChatFlow';
import * as api from '../../src/services/api/researchAssistantApi';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  PLAN_CREATE_TIMEOUT_MESSAGE: 'timeout', chat: vi.fn(), createPlan: vi.fn(), getMe: vi.fn(),
  getPlan: vi.fn(), listPlans: vi.fn(), uploadFile: vi.fn(),
}));

const noop = () => undefined;
const plan = (id: string, status = 'ready'): PlanGraph => ({
  id, status, user_intent: '运行代码', intent_type: 'Code_Execution', trace_id: `trace-${id}`,
  nodes: [], edges: [], budget: { max_task_attempts: 2, max_duration_seconds: 600 }, usage: { task_attempts: 0 },
});
const source = (sessionId: string, quote = 'selected passage'): DocumentReferenceDraft => ({
  id: 'reference-1', sessionId, upload_id: 'source-1', document_name: 'paper.pdf', page: 2, quote,
});
const attachment: UploadedFile = {
  id: 'staged-data', name: '未发送数据.csv', content_type: 'text/csv', size: 10,
  sha256: 'sha', content_url: '/api/uploads/staged-data/content', created_at: '2026-11-09T00:00:00Z',
};
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};
const renderFlow = () => renderHook(() => useResearchAssistantChatFlow({ onPlanGraphChanged: noop }));
// ES2022-safe "last match" helper: reverse() copies first, so the asserted
// history array is never mutated in place.
const lastUserMessage = (history: ChatMessage[]) => [...history].reverse().find((message) => message.role === 'user');

describe('document chat integration boundaries', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetAllMocks();
    vi.mocked(api.getMe).mockResolvedValue({ user_id: 'owner-1', session_id: 'cookie-session' });
    vi.mocked(api.listPlans).mockResolvedValue({ plans: [], total: 0, next_cursor: '' });
    vi.mocked(api.chat).mockResolvedValue({ response: 'answer' });
  });

  it.each([false, true])('sends a reference-only explanation as chat with staged attachments=%s', async (withAttachment) => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const sessionId = result.current.activeSessionId!;
    act(() => result.current.addDocumentReference(source(sessionId)));
    if (withAttachment) act(() => { result.current.attachUploadToSession(attachment); });
    expect(api.chat).not.toHaveBeenCalled();
    await act(() => result.current.handleSendMessage());
    expect(api.chat).toHaveBeenCalledWith('请结合原文解释引用的论文内容。',
      { userId: 'owner-1', sessionId }, { sessionId, documentReferences: [{
        upload_id: 'source-1', document_name: 'paper.pdf', page: 2, quote: 'selected passage',
      }] });
    expect(api.createPlan).not.toHaveBeenCalled();
    expect(result.current.documentReferences).toEqual([]);
    expect(result.current.pendingAttachments).toHaveLength(withAttachment ? 1 : 0);
    expect(lastUserMessage(result.current.chatHistory)?.text).not.toContain('附件：');
  });

  it('uses the same Unicode quote boundary as the backend', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    act(() => result.current.addDocumentReference(source(result.current.activeSessionId!, '📚'.repeat(2000))));
    expect(result.current.documentReferences).toHaveLength(1);
    expect(result.current.attachmentError).toBe('');
  });

  it('keeps a newly created plan when the previous plan snapshot finishes later', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    vi.mocked(api.createPlan).mockResolvedValueOnce({ message: 'ok', plan_graph: plan('old-plan') })
      .mockResolvedValueOnce({ message: 'ok', plan_graph: plan('new-plan') });
    act(() => result.current.setPrompt('运行代码 A'));
    await act(() => result.current.handleSendMessage());
    const oldSnapshot = deferred<PlanGraph>();
    vi.mocked(api.getPlan).mockReturnValueOnce(oldSnapshot.promise);
    let refresh!: Promise<void>;
    act(() => { refresh = result.current.refreshActivePlan(); });
    act(() => result.current.setPrompt('运行代码 B'));
    await act(() => result.current.handleSendMessage());
    expect(result.current.activePlanId).toBe('new-plan');
    await act(async () => { oldSnapshot.resolve(plan('old-plan', 'completed')); await refresh; });
    expect(result.current.activePlanId).toBe('new-plan');
    expect(result.current.activePlanGraph?.id).toBe('new-plan');
  });

  it('keeps the latest issued refresh when snapshots arrive out of order', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    vi.mocked(api.createPlan).mockResolvedValue({ message: 'ok', plan_graph: plan('active-plan') });
    act(() => result.current.setPrompt('运行代码'));
    await act(() => result.current.handleSendMessage());
    const older = deferred<PlanGraph>();
    const newer = deferred<PlanGraph>();
    vi.mocked(api.getPlan).mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    let first!: Promise<void>;
    let second!: Promise<void>;
    act(() => { first = result.current.refreshActivePlan(); second = result.current.refreshActivePlan(); });
    await act(async () => { newer.resolve(plan('active-plan', 'completed')); await second; });
    await act(async () => { older.resolve(plan('active-plan', 'in_progress')); await first; });
    expect(result.current.activePlanStatus).toBe('completed');
  });

  it('keeps the latest library open when A arrives after B, preserving existing session drafts', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const original = result.current.activeSessionId!;
    act(() => result.current.setPrompt('原会话尚未发送的问题'));
    const a = deferred<PlanGraph>();
    const b = deferred<PlanGraph>();
    vi.mocked(api.getPlan).mockImplementation(async (id) => {
      if (id === 'library-a') return a.promise;
      if (id === 'library-b') return b.promise;
      return plan(id);
    });
    let openA!: Promise<void>;
    let openB!: Promise<void>;
    act(() => { openA = result.current.handleOpenServerPlan('library-a'); openB = result.current.handleOpenServerPlan('library-b'); });
    await act(async () => { b.resolve(plan('library-b')); await openB; });
    expect(result.current.activePlanId).toBe('library-b');
    act(() => result.current.setPrompt('B 会话尚未发送的问题'));
    await act(async () => { a.resolve(plan('library-a')); await openA; });
    expect(result.current.activePlanId).toBe('library-b');
    expect(result.current.prompt).toBe('B 会话尚未发送的问题');
    expect(result.current.sessionSummaries.filter((session) => session.id === 'plan:library-a')).toHaveLength(0);
    act(() => result.current.handleSwitchSession(original));
    expect(result.current.prompt).toBe('原会话尚未发送的问题');
    await act(() => result.current.handleOpenServerPlan('library-b'));
    expect(result.current.prompt).toBe('B 会话尚未发送的问题');
    expect(result.current.sessionSummaries.filter((session) => session.id === 'plan:library-b')).toHaveLength(1);
  });

  it('abandons pending library opens synchronously when selecting or creating another session', async () => {
    const { result } = renderFlow();
    await waitFor(() => expect(result.current.isLoggedIn).toBe(true));
    const firstSession = result.current.activeSessionId!;
    act(() => result.current.setPrompt('第一会话草稿'));
    act(() => result.current.handleCreateSession());
    const secondSession = result.current.activeSessionId!;
    act(() => result.current.setPrompt('第二会话草稿'));
    act(() => result.current.handleSwitchSession(firstSession));
    const pendingSwitch = deferred<PlanGraph>();
    const pendingCreate = deferred<PlanGraph>();
    vi.mocked(api.getPlan).mockReturnValueOnce(pendingSwitch.promise).mockReturnValueOnce(pendingCreate.promise);
    let switched!: Promise<void>;
    act(() => {
      switched = result.current.handleOpenServerPlan('abandoned-switch');
      result.current.handleSwitchSession(secondSession);
    });
    await act(async () => { pendingSwitch.resolve(plan('abandoned-switch')); await switched; });
    expect(result.current.activeSessionId).toBe(secondSession);
    expect(result.current.prompt).toBe('第二会话草稿');
    let created!: Promise<void>;
    act(() => {
      created = result.current.handleOpenServerPlan('abandoned-create');
      result.current.handleCreateSession();
    });
    const newSession = result.current.activeSessionId!;
    await act(async () => { pendingCreate.resolve(plan('abandoned-create')); await created; });
    expect(result.current.activeSessionId).toBe(newSession);
    expect(result.current.activePlanId).toBeNull();
    expect(result.current.sessionSummaries).toHaveLength(3);
    act(() => result.current.handleSwitchSession(firstSession));
    expect(result.current.prompt).toBe('第一会话草稿');
    act(() => result.current.handleSwitchSession(secondSession));
    expect(result.current.prompt).toBe('第二会话草稿');
  });

  it('abandons an open across identity resolution and discards a later response after unmount', async () => {
    const identity = deferred<{ user_id: string; session_id: string }>();
    vi.mocked(api.getMe).mockReturnValueOnce(identity.promise);
    const graphChanged = vi.fn();
    const { result, unmount } = renderHook(() => useResearchAssistantChatFlow({ onPlanGraphChanged: graphChanged }));
    const original = result.current.activeSessionId;
    const oldIdentity = deferred<PlanGraph>();
    const afterUnmount = deferred<PlanGraph>();
    vi.mocked(api.getPlan).mockReturnValueOnce(oldIdentity.promise).mockReturnValueOnce(afterUnmount.promise);
    let opening!: Promise<void>;
    act(() => { opening = result.current.handleOpenServerPlan('old-identity'); });
    await act(async () => { identity.resolve({ user_id: 'owner-2', session_id: 'new-cookie-session' }); });
    await act(async () => { oldIdentity.resolve(plan('old-identity')); await opening; });
    expect(result.current.activeSessionId).toBe(original);
    expect(result.current.activePlanId).toBeNull();
    let detached!: Promise<void>;
    act(() => { detached = result.current.handleOpenServerPlan('after-unmount'); });
    unmount();
    const callsBefore = graphChanged.mock.calls.length;
    const storedBefore = localStorage.getItem('research-assistant.chat-sessions.v1');
    await act(async () => { afterUnmount.resolve(plan('after-unmount')); await detached; });
    expect(graphChanged).toHaveBeenCalledTimes(callsBefore);
    expect(localStorage.getItem('research-assistant.chat-sessions.v1')).toBe(storedBefore);
  });
});

// UI-04 refresh regression. `readingMode` is in-memory only, so a reload loses it
// while the persisted user turn keeps its documentReferences. These tests send a
// real referenced turn, drop the hook, and remount it from localStorage.
const STORAGE_KEY = 'research-assistant.chat-sessions.v1';

const sendReferencedTurnThenRemount = async () => {
  const first = renderFlow();
  await waitFor(() => expect(first.result.current.isLoggedIn).toBe(true));
  const sessionId = first.result.current.activeSessionId!;
  act(() => first.result.current.addDocumentReference(source(sessionId)));
  await act(() => first.result.current.handleSendMessage());
  // The referenced user turn must be on disk before the reload, otherwise the
  // remount would not be exercising the regression at all.
  await waitFor(() => {
    expect(lastUserMessage(first.result.current.chatHistory)?.documentReferences).toHaveLength(1);
    expect(localStorage.getItem(STORAGE_KEY)).toContain('source-1');
  });
  first.unmount();
  vi.mocked(api.chat).mockClear();
  vi.mocked(api.createPlan).mockClear();

  const second = renderFlow();
  await waitFor(() => expect(second.result.current.isLoggedIn).toBe(true));
  expect(second.result.current.activeSessionId).toBe(sessionId);
  return { result: second.result, sessionId };
};

describe('reading session survives a reload', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetAllMocks();
    vi.mocked(api.getMe).mockResolvedValue({ user_id: 'owner-1', session_id: 'cookie-session' });
    vi.mocked(api.listPlans).mockResolvedValue({ plans: [], total: 0, next_cursor: '' });
    vi.mocked(api.chat).mockResolvedValue({ response: 'answer' });
    vi.mocked(api.createPlan).mockResolvedValue({ message: 'ok', plan_graph: plan('follow-up-plan') });
  });

  it('routes 总结论文 to chat after the hook is remounted from storage', async () => {
    const { result, sessionId } = await sendReferencedTurnThenRemount();
    act(() => result.current.setPrompt('总结论文'));
    await act(() => result.current.handleSendMessage());
    expect(api.createPlan).not.toHaveBeenCalled();
    expect(api.chat).toHaveBeenCalledTimes(1);
    expect(api.chat).toHaveBeenCalledWith('总结论文', { userId: 'owner-1', sessionId }, { sessionId });
    expect(lastUserMessage(result.current.chatHistory)?.text).toBe('总结论文');
  });

  it('still plans an explicit 复现实验 after the remount, without guessing old upload ids', async () => {
    const { result, sessionId } = await sendReferencedTurnThenRemount();
    act(() => result.current.setPrompt('复现实验'));
    await act(() => result.current.handleSendMessage());
    expect(api.chat).not.toHaveBeenCalled();
    expect(api.createPlan).toHaveBeenCalledTimes(1);
    // The stale reference id from the pre-reload turn must not be replayed as a
    // fresh attachment; the user did not re-attach it to this turn.
    expect(api.createPlan).toHaveBeenCalledWith(
      '复现实验',
      { userId: 'owner-1', sessionId },
      [],
      'confirm_important',
      false,
      expect.any(String),
    );
    expect(result.current.activePlanId).toBe('follow-up-plan');
  });
});
