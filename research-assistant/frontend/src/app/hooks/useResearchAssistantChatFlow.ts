import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type {
  ChatMessage,
  DocumentReference,
  DocumentReferenceDraft,
  IntentContext,
  PlanClarification,
  PlanGraph,
  UploadedFile,
} from '../../contracts/api';
import { PLAN_CREATE_TIMEOUT_MESSAGE, chat, createPlan, getMe, getPlan, listPlans, uploadFile } from '../../services/api/researchAssistantApi';
import type { PlanSummary } from '../../services/api/researchAssistantApi';
import { formatApiError } from '../../services/api/apiError';
import { uiText } from '../constants/uiText';

const isTaskIntent = (input: string) => {
  const normalized = input.toLowerCase();
  return [
    /对比|比较|评估|选型|复现|执行|运行|画图|绘图|代码|论文|报告|总结|分析/,
    /rag|benchmark|plot|matplotlib|python|reproduce|summary|report/,
  ].some((pattern) => pattern.test(normalized));
};

// A reading session only escalates to the plan path when the user asks for the
// experiment itself (reproduce / run code / execute the plan). Ordinary reading
// questions — including ones that mention 论文、分析、总结 or summary — stay in
// chat so a document reference can never turn into a plan on its own.
const EXPLICIT_EXECUTION_PATTERNS = [
  /复现|重现|重跑|重新运行|跑实验|做实验|执行实验|执行计划|执行代码|执行脚本|运行实验|运行代码|运行脚本|跑一下代码|跑一下脚本/,
  /reproduce|re-run|rerun|run the (experiment|code|script|plan)|execute the (experiment|code|script|plan)/,
];

const isExplicitExecutionIntent = (input: string) => {
  const normalized = input.toLowerCase();
  return EXPLICIT_EXECUTION_PATTERNS.some((pattern) => pattern.test(normalized));
};

// UI-04 refresh regression: `readingMode` lives only in the in-memory draft map,
// so a reload forgets it. What a reload does keep is the persisted chat history,
// where a user turn that was asked against a paper still carries the
// documentReferences it was sent with. Re-deriving the reading session from that
// history is what stops a later "总结论文" from silently turning into a plan.
//
// Only the fact that a referenced turn exists is recovered, never the upload ids
// behind it: an explicit reproduce/run turn must be planned from the draft the
// user is sending right now, not from references the user never re-attached.
const historyHasReadingTurn = (history: ChatMessage[]) => history.some((message) => (
  message.role === 'user'
  && Array.isArray(message.documentReferences)
  && message.documentReferences.length > 0
));

export const MAX_DOCUMENT_REFERENCES = 4;
export const MAX_DOCUMENT_QUOTE_LENGTH = 2000;
export const MAX_SESSION_ATTACHMENTS = 8;

export type DocumentReferenceAddResult =
  | 'added'
  | 'duplicate'
  | 'full'
  | 'session-mismatch'
  | 'invalid-page'
  | 'quote-too-long'
  | 'invalid';

export interface PlanReadyOptions {
  autoRun: boolean;
}

interface UseResearchAssistantChatFlowOptions {
  onPlanGraphChanged: (planGraph: PlanGraph | null) => void;
  onPlanReady?: (planGraph: PlanGraph, options: PlanReadyOptions, intentContext: IntentContext | null) => void;
}

interface ChatSessionRecord {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  chatHistory: ChatMessage[];
  activePlanId: string | null;
  intentContext: IntentContext | null;
  planGraph: PlanGraph | null;
}

interface PersistedChatState {
  userId: string | null;
  activeSessionId: string | null;
  sessions: ChatSessionRecord[];
}

// One composer draft, owned by one session. Prompt text, pending attachments and
// document references travel together so a session switch preserves all three
// and can never mix drafts from two sessions.
interface SessionDraft {
  prompt: string;
  pendingAttachments: UploadedFile[];
  documentReferences: DocumentReferenceDraft[];
  uploadingAttachments: boolean;
  attachmentError: string;
  // A session that has been asked about a document passage stays in reading
  // mode: follow-up turns keep going to chat even when they mention 论文/总结/分析,
  // and only an explicit reproduce/run request escalates to a plan.
  readingMode: boolean;
}

const createEmptyDraft = (): SessionDraft => ({
  prompt: '',
  pendingAttachments: [],
  documentReferences: [],
  uploadingAttachments: false,
  attachmentError: '',
  readingMode: false,
});

// Stable instance so an untouched session does not hand fresh empty arrays to
// every consumer on each render.
const EMPTY_DRAFT: SessionDraft = createEmptyDraft();

const readDraft = (drafts: Record<string, SessionDraft>, sessionId: string | null): SessionDraft =>
  (sessionId ? drafts[sessionId] : undefined) ?? EMPTY_DRAFT;

// A reference only counts when it came from the session it is drafted in. The
// other bounds (page > 0, quote length) are validated before this point so an
// invalid reference is reported instead of silently rewritten.
const isReferenceForSession = (reference: DocumentReferenceDraft, sessionId: string | null) =>
  Boolean(sessionId) && reference.sessionId === sessionId;

const toWireReference = (reference: DocumentReferenceDraft): DocumentReference => ({
  upload_id: reference.upload_id,
  document_name: reference.document_name,
  page: reference.page,
  quote: reference.quote,
});

// Everything one send froze at submission time. A failure replays it into the
// same session; a success only clears the parts that did not change meanwhile.
interface SentDraftSnapshot {
  sessionId: string;
  userId: string;
  prompt: string;
  history: ChatMessage[];
  attachments: UploadedFile[];
  references: DocumentReferenceDraft[];
}

interface SessionSummary {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  messageCount: number;
}

const STORAGE_KEY = 'research-assistant.chat-sessions.v1';
export const AUTO_RUN_ON_CREATE_STORAGE_KEY = 'research-assistant.auto-run-on-create.v1';

export interface AutoRunOnCreateControl {
  autoRunOnCreate: boolean;
  setAutoRunOnCreate: (next: boolean) => void;
}

// The preference lives in the flow hook so the composer can only change what the
// next plan creation will actually read. The default is deliberately inert so a
// standalone composer (tests, previews) never starts anything by itself.
export const AutoRunOnCreateContext = createContext<AutoRunOnCreateControl>({
  autoRunOnCreate: false,
  setAutoRunOnCreate: () => undefined,
});

export const useAutoRunOnCreateControl = (): AutoRunOnCreateControl => useContext(AutoRunOnCreateContext);

const loadAutoRunOnCreate = (): boolean => {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(AUTO_RUN_ON_CREATE_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
};

// Mirrors the backend/UX rule: only a plot-producing Code_Execution plan without
// a pending plan-level approval may start itself. Manual plans never match.
export const isAutoRunPlanEligible = (planGraph: PlanGraph, autoRunOnCreate: boolean): boolean => {
  if (!autoRunOnCreate) return false;
  if (planGraph.intent_type !== 'Code_Execution') return false;
  if (planGraph.approval?.required) return false;
  return planGraph.nodes.some((node) => node.type === 'render_plot');
};

const createId = (prefix: string) => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
};

const createWelcomeMessage = (): ChatMessage => ({
  role: 'system',
  text: uiText.appWelcome,
});

const formatPlanClarification = (clarification?: PlanClarification) => {
  if (!clarification?.required || clarification.type !== 'paper_reproduction_mode') return '';

  const probe = clarification.resource_probe;
  const decision = clarification.mode_decision;
  const lines = [
    '### 需要确认复现模式',
    '',
    clarification.question,
  ];

  if (clarification.recommended_mode) {
    lines.push('', `**当前建议模式：** \`${clarification.recommended_mode}\``);
  }

  if (probe) {
    lines.push(
      '',
      '**本机资源探测：**',
      `- CPU：${probe.cpu_count ?? '未知'}`,
      `- 内存：${typeof probe.memory_gb === 'number' ? `${probe.memory_gb.toFixed(1)}GB` : '未知'}`,
      `- 可用磁盘：${typeof probe.disk_free_gb === 'number' ? `${probe.disk_free_gb.toFixed(1)}GB` : '未知'}`,
      `- CUDA GPU：${probe.gpu_count ?? 0}`,
    );
  }

  if (decision?.reasons?.length) {
    lines.push('', '**判断依据：**', ...decision.reasons.map((reason) => `- ${reason}`));
  }

  if (clarification.options?.length) {
    lines.push(
      '',
      '**可选运行方式：**',
      ...clarification.options.map((option) => `- \`${option.id}\` ${option.label}：${option.description}`),
    );
  }

  lines.push('', '当前会先生成任务拓扑；如果你确认要全量复现，下一步可以把运行模式切到 `full`。');
  return lines.join('\n');
};

const createSessionRecord = (index: number): ChatSessionRecord => {
  const now = new Date().toISOString();
  return {
    id: createId('session'),
    title: `新会话 ${index}`,
    createdAt: now,
    updatedAt: now,
    chatHistory: [createWelcomeMessage()],
    activePlanId: null,
    intentContext: null,
    planGraph: null,
  };
};

const createSessionState = (userId: string | null, sessionTitlePrefix: string): PersistedChatState => {
  const firstSession = createSessionRecord(1);
  return {
    userId,
    activeSessionId: firstSession.id,
    sessions: [{ ...firstSession, title: `${sessionTitlePrefix} 1` }],
  };
};

const normalizeChatState = (raw: unknown): PersistedChatState => {
  if (!raw || typeof raw !== 'object') {
    return { userId: null, activeSessionId: null, sessions: [] };
  }

  const data = raw as Partial<PersistedChatState>;
  // Persisted sessions never carry a PlanGraph: plan payloads contain base64
  // figures and would blow the localStorage quota. Only lightweight ids/titles
  // are stored; the graph is re-fetched from the server on demand.
  const sessions = Array.isArray(data.sessions)
    ? data.sessions
        .filter((session): session is ChatSessionRecord => Boolean(session?.id))
        .map((session) => ({ ...session, planGraph: null }))
    : [];

  return {
    userId: typeof data.userId === 'string' && data.userId.trim() ? data.userId.trim() : null,
    activeSessionId: typeof data.activeSessionId === 'string' && data.activeSessionId.trim() ? data.activeSessionId.trim() : null,
    sessions,
  };
};

const loadPersistedChatState = (): PersistedChatState => {
  if (typeof window === 'undefined') {
    return createSessionState(null, '临时会话');
  }

  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return createSessionState(null, '临时会话');

    const normalized = normalizeChatState(JSON.parse(raw));
    if (normalized.sessions.length === 0 || !normalized.activeSessionId) {
      return createSessionState(normalized.userId, normalized.userId ? '新会话' : '临时会话');
    }
    return normalized;
  } catch {
    return createSessionState(null, '临时会话');
  }
};

// serializePersistedState strips in-memory-only payloads (PlanGraph) before
// writing to localStorage so large base64 images can never exhaust the quota.
const serializePersistedState = (state: PersistedChatState): string =>
  JSON.stringify({
    ...state,
    sessions: state.sessions.map((session) => ({ ...session, planGraph: null })),
  });

const deriveSessionTitle = (fallbackTitle: string, messages: ChatMessage[]): string => {
  const firstUserMessage = messages.find((message) => message.role === 'user' && message.text.trim());
  if (!firstUserMessage) return fallbackTitle;
  const compactText = firstUserMessage.text.replace(/\s+/g, ' ').trim();
  return compactText.length > 18 ? `${compactText.slice(0, 18)}...` : compactText;
};

const updateSessionRecord = (
  sessions: ChatSessionRecord[],
  sessionId: string,
  updater: (session: ChatSessionRecord) => ChatSessionRecord,
) => sessions.map((session) => (session.id === sessionId ? updater(session) : session));

export function useResearchAssistantChatFlow(options: UseResearchAssistantChatFlowOptions) {
  const { onPlanGraphChanged, onPlanReady } = options;
  const [persistedState, setPersistedState] = useState<PersistedChatState>(() => loadPersistedChatState());
  const pendingSubmissions = useRef(new Map<string, {
    signature: string;
    key: string;
    args: Parameters<typeof createPlan>;
    commitUnknown: boolean;
  }>());
  const planRefreshSequence = useRef(0);
  // Opening a library plan changes the active conversation. Only the latest
  // navigation intent may apply its response, even before React effects run.
  const planOpenGeneration = useRef(0);
  const planOpenIdentityKey = useRef('');
  const sending = useRef(false);
  const activeSessionRef = useRef(persistedState.activeSessionId);
  useEffect(() => { activeSessionRef.current = persistedState.activeSessionId; }, [persistedState.activeSessionId]);
  // UI-04: drafts are per session. A prompt, a document reference or a pending
  // attachment belongs to exactly one session and can never leak into another
  // one after a switch. The map is in-memory only; blobs are never persisted.
  const [drafts, setDrafts] = useState<Record<string, SessionDraft>>({});
  const draftsRef = useRef(drafts);
  useEffect(() => { draftsRef.current = drafts; }, [drafts]);

  const updateDraftForSession = useCallback((
    sessionId: string,
    updater: (draft: SessionDraft) => SessionDraft,
  ) => {
    setDrafts((prev) => {
      const current = prev[sessionId] ?? createEmptyDraft();
      const next = updater(current);
      if (next === current) return prev;
      const updated = { ...prev, [sessionId]: next };
      draftsRef.current = updated;
      return updated;
    });
  }, []);

  const activeSessionId = persistedState.activeSessionId;
  const activeDraft = readDraft(drafts, activeSessionId);

  const setPrompt = useCallback((value: string) => {
    const sessionId = activeSessionRef.current;
    if (!sessionId) return;
    updateDraftForSession(sessionId, (draft) => ({ ...draft, prompt: value }));
  }, [updateDraftForSession]);

  const prompt = activeDraft.prompt;
  const pendingAttachments = activeDraft.pendingAttachments;
  const uploadingAttachments = activeDraft.uploadingAttachments;
  const attachmentError = activeDraft.attachmentError;
  const documentReferences = activeDraft.documentReferences;
  const [loading, setLoading] = useState(false);
  const [runtimePermissionMode, setRuntimePermissionMode] = useState<'confirm_important' | 'full_access'>('confirm_important');
  const [fullAccessAcknowledged, setFullAccessAcknowledged] = useState(false);
  const [showFullAccessConfirm, setShowFullAccessConfirm] = useState(false);
  const [loginInput, setLoginInput] = useState('');
  // The server owns identity (signed HttpOnly cookie). The client only mirrors
  // it for display; it never sends a user id header.
  const [serverIdentity, setServerIdentity] = useState<{ userId: string; sessionId: string } | null>(null);
  const [serverPlans, setServerPlans] = useState<PlanSummary[]>([]);
  const [storageWarning, setStorageWarning] = useState('');
  const [autoRunOnCreate, setAutoRunOnCreateState] = useState<boolean>(loadAutoRunOnCreate);

  const setAutoRunOnCreate = useCallback((next: boolean) => {
    setAutoRunOnCreateState(next);
    if (typeof window === 'undefined') return;
    try {
      window.localStorage.setItem(AUTO_RUN_ON_CREATE_STORAGE_KEY, next ? '1' : '0');
    } catch (error) {
      console.warn('[chat] failed to persist auto-run preference', error);
    }
  }, []);

  const autoRunControl = useMemo<AutoRunOnCreateControl>(
    () => ({ autoRunOnCreate, setAutoRunOnCreate }),
    [autoRunOnCreate, setAutoRunOnCreate],
  );

  const refreshServerPlans = useCallback(async () => {
    try {
      const response = await listPlans(20);
      setServerPlans(response.plans ?? []);
    } catch (error) {
      console.warn('[chat] failed to load server plan library', error);
    }
  }, []);

  const handleOpenServerPlan = useCallback(async (planId: string) => {
    const generation = ++planOpenGeneration.current;
    const sourceSessionId = activeSessionRef.current;
    const identityKey = planOpenIdentityKey.current;
    try {
      const planGraph = await getPlan(planId, { userId: '', sessionId: '' });
      if (generation !== planOpenGeneration.current
        || identityKey !== planOpenIdentityKey.current
        || sourceSessionId !== activeSessionRef.current) return;
      setPersistedState((prev) => {
        // React may apply this updater after a newer navigation was queued.
        if (generation !== planOpenGeneration.current
          || identityKey !== planOpenIdentityKey.current
          || prev.activeSessionId !== sourceSessionId) return prev;
        const existing = prev.sessions.find((session) => session.activePlanId === planId);
        if (existing) {
          activeSessionRef.current = existing.id;
          return { ...prev, activeSessionId: existing.id };
        }
        const now = new Date().toISOString();
        // A plan opened from the library gets a stable, plan-keyed session id.
        // It is a direct view of the server plan, not a throwaway local chat:
        // reopening it never creates a second copy, and the session summary
        // merges it with the server plan instead of showing a duplicate.
        const record: ChatSessionRecord = {
          id: 'plan:' + planId,
          title: planGraph.user_intent?.slice(0, 18) || '历史计划',
          createdAt: now,
          updatedAt: now,
          chatHistory: [createWelcomeMessage()],
          activePlanId: planId,
          intentContext: null,
          planGraph,
        };
        activeSessionRef.current = record.id;
        return { ...prev, activeSessionId: record.id, sessions: [record, ...prev.sessions] };
      });
    } catch (error) {
      console.warn('[chat] failed to open server plan', error);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    getMe()
      .then((identity) => {
        if (cancelled) return;
        const key = `${identity.user_id}|${identity.session_id}`;
        if (key !== planOpenIdentityKey.current) {
          planOpenGeneration.current += 1;
          planOpenIdentityKey.current = key;
        }
        setServerIdentity({ userId: identity.user_id, sessionId: identity.session_id });
      })
      .catch((error) => {
        console.warn('[chat] failed to resolve server identity', error);
      })
      .finally(() => {
        if (!cancelled) void refreshServerPlans();
      });
    return () => {
      cancelled = true;
      planOpenGeneration.current += 1;
    };
  }, [refreshServerPlans]);

  const activeSession = useMemo(
    () => persistedState.sessions.find((session) => session.id === persistedState.activeSessionId) ?? null,
    [persistedState.activeSessionId, persistedState.sessions],
  );

  const chatHistory = activeSession?.chatHistory ?? [];
  const activePlanId = activeSession?.activePlanId ?? null;
	const activePlanStatus = activeSession?.planGraph?.status ?? null;
  const intentContext = activeSession?.intentContext ?? null;
  const sessionSummaries = useMemo<SessionSummary[]>(() => {
    const local = [...persistedState.sessions]
      .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
      .map((session) => ({
        id: session.id,
        title: session.title,
        createdAt: session.createdAt,
        updatedAt: session.updatedAt,
        messageCount: session.chatHistory.length,
      }));
    // Server plan library: every owned plan appears even when this browser has
    // no local session for it (refresh, cleared storage, another device).
    const knownPlanIds = new Set(persistedState.sessions.map((session) => session.activePlanId).filter(Boolean) as string[]);
    const remote = serverPlans
      .filter((plan) => !knownPlanIds.has(plan.id))
      .map((plan) => ({
        id: `plan:${plan.id}`,
        title: `[计划] ${plan.title || plan.intent_type}`,
        createdAt: plan.created_at,
        updatedAt: plan.updated_at,
        messageCount: plan.total_nodes,
      }));
    return [...local, ...remote];
  }, [persistedState.sessions, serverPlans]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    try {
      window.localStorage.setItem(STORAGE_KEY, serializePersistedState(persistedState));
      setStorageWarning('');
    } catch (error) {
      console.warn('[chat] failed to persist sessions; local storage quota or privacy mode blocked it', error);
      setStorageWarning('本地存储空间不足，已自动降级为服务端恢复模式；计划仍可从服务器重新打开。');
      try {
        window.localStorage.removeItem(STORAGE_KEY);
      } catch {
        /* ignore */
      }
    }
  }, [persistedState]);

  useEffect(() => {
    onPlanGraphChanged(activeSession?.planGraph ?? null);
  }, [activeSession?.id, activeSession?.planGraph, onPlanGraphChanged]);

  const appendMessageToSession = useCallback((sessionId: string, message: ChatMessage) => {
    const now = new Date().toISOString();
    setPersistedState((prev) => ({
      ...prev,
      sessions: updateSessionRecord(prev.sessions, sessionId, (session) => {
        const nextChatHistory = [...session.chatHistory, message];
        return {
          ...session,
          chatHistory: nextChatHistory,
          updatedAt: now,
          title: deriveSessionTitle(session.title, nextChatHistory),
        };
      }),
    }));
  }, []);

  const appendChatMessage = useCallback(
    (message: ChatMessage) => {
      if (!persistedState.activeSessionId) return;
      appendMessageToSession(persistedState.activeSessionId, message);
    },
    [appendMessageToSession, persistedState.activeSessionId],
  );

  // Identity is issued by the backend; there is no client-side "login" that
  // could impersonate another user. Kept as a no-op for API compatibility.
  const handleLogin = useCallback(() => {
    setLoginInput('');
  }, []);

  const handleCreateSession = useCallback(() => {
    planOpenGeneration.current += 1;
    const sessionTitlePrefix = persistedState.userId ? '新会话' : '临时会话';
    const nextSession = {
      ...createSessionRecord(persistedState.sessions.length + 1),
      title: `${sessionTitlePrefix} ${persistedState.sessions.length + 1}`,
    };
    setPersistedState((prev) => ({
      ...prev,
      activeSessionId: nextSession.id,
      sessions: [nextSession, ...prev.sessions],
    }));
    // A brand-new session starts with an empty draft; the previous session keeps
    // its own prompt, references and attachments untouched.
    updateDraftForSession(nextSession.id, () => createEmptyDraft());
    activeSessionRef.current = nextSession.id;
  }, [persistedState.sessions.length, persistedState.userId, updateDraftForSession]);

  const handleSwitchSession = useCallback((sessionId: string) => {
    planOpenGeneration.current += 1;
    if (sessionId.startsWith('plan:')) {
      void handleOpenServerPlan(sessionId.slice('plan:'.length));
      return;
    }
    setPersistedState((prev) => ({
      ...prev,
      activeSessionId: sessionId,
    }));
    // No prompt reset: the target session renders whatever draft it already had,
    // and the source session draft stays where it was.
    activeSessionRef.current = sessionId;
  }, [handleOpenServerPlan]);

  const handleAttachFiles = useCallback(async (files: File[]) => {
    // Bind the upload to the session and identity that started it. A slow
    // response must land in that session's draft, never in a newer one.
    const requestSessionId = activeSessionRef.current;
    if (files.length === 0 || !requestSessionId) return;
    const requestUserId = serverIdentity?.userId ?? '';
    const currentDraft = readDraft(draftsRef.current, requestSessionId);
    const availableSlots = Math.max(0, MAX_SESSION_ATTACHMENTS - currentDraft.pendingAttachments.length);
    const selected = files.slice(0, availableSlots);
    if (selected.length === 0) {
      updateDraftForSession(requestSessionId, (draft) => ({
        ...draft,
        attachmentError: '每个计划最多添加 8 个附件',
      }));
      return;
    }
    const identity = { userId: requestUserId, sessionId: requestSessionId };
    updateDraftForSession(requestSessionId, (draft) => ({
      ...draft,
      uploadingAttachments: true,
      attachmentError: '',
    }));
    const uploaded: UploadedFile[] = [];
    try {
      for (const file of selected) {
        uploaded.push(await uploadFile(file, identity));
      }
      updateDraftForSession(requestSessionId, (draft) => ({
        ...draft,
        pendingAttachments: [
          ...draft.pendingAttachments,
          ...uploaded.filter((upload) => !draft.pendingAttachments.some((existing) => existing.id === upload.id)),
        ].slice(0, MAX_SESSION_ATTACHMENTS),
        uploadingAttachments: false,
        attachmentError: '',
      }));
    } catch (error) {
      console.error(error);
      updateDraftForSession(requestSessionId, (draft) => ({
        ...draft,
        uploadingAttachments: false,
        attachmentError: '附件上传失败，请检查文件类型和大小',
      }));
    }
  }, [serverIdentity?.userId, updateDraftForSession]);

  const handleRemoveAttachment = useCallback((uploadId: string) => {
    const sessionId = activeSessionRef.current;
    if (!sessionId) return;
    updateDraftForSession(sessionId, (draft) => ({
      ...draft,
      pendingAttachments: draft.pendingAttachments.filter((attachment) => attachment.id !== uploadId),
      attachmentError: '',
    }));
  }, [updateDraftForSession]);

  // Literature records and search results become ordinary chat attachments.
  // Keeping this in the flow hook means the composer, the literature panel and
  // the PDF reader all agree on the same eight-slot attachment budget.
  const attachUploadToSession = useCallback((upload: UploadedFile): 'added' | 'duplicate' | 'full' => {
    const sessionId = activeSessionRef.current;
    if (!sessionId) return 'full';
    const draft = readDraft(draftsRef.current, sessionId);
    if (draft.pendingAttachments.some((attachment) => attachment.id === upload.id)) return 'duplicate';
    if (draft.pendingAttachments.length >= MAX_SESSION_ATTACHMENTS) {
      updateDraftForSession(sessionId, (current) => ({ ...current, attachmentError: '每个计划最多添加 8 个附件' }));
      return 'full';
    }
    updateDraftForSession(sessionId, (current) => (
      current.pendingAttachments.some((attachment) => attachment.id === upload.id)
        ? current
        : {
            ...current,
            pendingAttachments: [...current.pendingAttachments, upload].slice(0, MAX_SESSION_ATTACHMENTS),
            attachmentError: '',
          }
    ));
    return 'added';
  }, [updateDraftForSession]);

  // UI-04 draft references. "Ask AI" only ever drafts: no message is sent and no
  // plan is created, and an existing prompt is never overwritten.
  const addDocumentReference = useCallback((reference: DocumentReferenceDraft): DocumentReferenceAddResult => {
    const sessionId = activeSessionRef.current;
    if (!sessionId) return 'invalid';
    // A reference raised in another session must not enter this one.
    if (reference?.sessionId && reference.sessionId !== sessionId) return 'session-mismatch';
    const uploadId = typeof reference?.upload_id === 'string' ? reference.upload_id.trim() : '';
    const page = reference?.page;
    const quote = typeof reference?.quote === 'string' ? reference.quote : '';
    if (!uploadId) {
      updateDraftForSession(sessionId, (draft) => ({ ...draft, attachmentError: '引用缺少文档标识，无法添加。' }));
      return 'invalid';
    }
    if (!Number.isFinite(page) || Math.floor(page) !== page || page < 1) {
      updateDraftForSession(sessionId, (draft) => ({ ...draft, attachmentError: '引用页码必须是大于 0 的整数。' }));
      return 'invalid-page';
    }
    if (Array.from(quote).length > MAX_DOCUMENT_QUOTE_LENGTH) {
      updateDraftForSession(sessionId, (draft) => ({
        ...draft,
        attachmentError: `引用原文超过 ${MAX_DOCUMENT_QUOTE_LENGTH} 字，请缩短选中范围后再引用。`,
      }));
      return 'quote-too-long';
    }
    const draft = readDraft(draftsRef.current, sessionId);
    if (draft.documentReferences.some((existing) => (
      existing.upload_id === uploadId && existing.page === page && existing.quote === quote
    ))) {
      return 'duplicate';
    }
    if (draft.documentReferences.length >= MAX_DOCUMENT_REFERENCES) {
      updateDraftForSession(sessionId, (current) => ({
        ...current,
        attachmentError: `每个会话最多引用 ${MAX_DOCUMENT_REFERENCES} 段原文，请先移除已有引用。`,
      }));
      return 'full';
    }
    const nextReference: DocumentReferenceDraft = {
      upload_id: uploadId,
      document_name: typeof reference.document_name === 'string' ? reference.document_name : '',
      page,
      quote,
      id: typeof reference.id === 'string' && reference.id.trim() ? reference.id : createId('ref'),
      sessionId,
    };
    updateDraftForSession(sessionId, (current) => (
      current.documentReferences.length >= MAX_DOCUMENT_REFERENCES
        ? current
        : {
            ...current,
            documentReferences: [...current.documentReferences, nextReference],
            attachmentError: '',
          }
    ));
    return 'added';
  }, [updateDraftForSession]);

  const removeDocumentReference = useCallback((referenceId: string) => {
    const sessionId = activeSessionRef.current;
    if (!sessionId) return;
    updateDraftForSession(sessionId, (draft) => ({
      ...draft,
      documentReferences: draft.documentReferences.filter((reference) => reference.id !== referenceId),
      attachmentError: '',
    }));
  }, [updateDraftForSession]);

  const updateSessionPlanState = useCallback(
    (sessionId: string, payload: { activePlanId: string | null; intentContext: IntentContext | null; planGraph: PlanGraph | null }) => {
      const now = new Date().toISOString();
      setPersistedState((prev) => ({
        ...prev,
        sessions: updateSessionRecord(prev.sessions, sessionId, (session) => ({
          ...session,
          activePlanId: payload.activePlanId,
          intentContext: payload.intentContext,
          planGraph: payload.planGraph,
          updatedAt: now,
        })),
      }));
    },
    [],
  );

  const refreshActivePlan = useCallback(async () => {
    const session = activeSession;
    const planId = session?.activePlanId;
    const sessionId = persistedState.activeSessionId;
    if (!planId || !sessionId) return;
    const sequence = ++planRefreshSequence.current;
    try {
      const planGraph = await getPlan(planId, {
        userId: serverIdentity?.userId ?? '',
        sessionId,
      });
      if (sequence !== planRefreshSequence.current) return;
      setPersistedState((previous) => {
        const target = previous.sessions.find((record) => record.id === sessionId);
        // A new plan may have been created while this snapshot was loading.
        if (target?.activePlanId !== planId) return previous;
        return {
          ...previous,
          sessions: updateSessionRecord(previous.sessions, sessionId, (record) => ({
            ...record,
            planGraph,
            updatedAt: new Date().toISOString(),
          })),
        };
      });
    } catch (error) {
      console.warn('[chat] failed to refresh plan state', error);
    }
  }, [activeSession, serverIdentity?.userId, persistedState.activeSessionId]);

  useEffect(() => {
    if (activeSession?.activePlanId) {
      void refreshActivePlan();
    }
    // Refresh once per session switch, not on every planGraph update.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSession?.id]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const refreshOnFocus = () => {
      if (activeSession?.activePlanId) {
        void refreshActivePlan();
      }
    };
    window.addEventListener('focus', refreshOnFocus);
    return () => window.removeEventListener('focus', refreshOnFocus);
  }, [activeSession?.activePlanId, refreshActivePlan]);

  // A failed send puts the question, its references and the staged attachments
  // back into the session they were sent from. It never writes into whatever
  // session or identity the user has moved to meanwhile.
  const restoreFailedDraft = useCallback((sent: SentDraftSnapshot) => {
    const currentUserId = serverIdentity?.userId ?? '';
    if (sent.userId && currentUserId && sent.userId !== currentUserId) return;
    updateDraftForSession(sent.sessionId, (draft) => {
      const missingReferences = sent.references.filter((reference, index) => (
        draft.documentReferences.length + index < MAX_DOCUMENT_REFERENCES
        && !draft.documentReferences.some((existing) => existing.id === reference.id)
      ));
      const missingAttachments = sent.attachments.filter((attachment) => (
        !draft.pendingAttachments.some((existing) => existing.id === attachment.id)
      ));
      return {
        ...draft,
        prompt: draft.prompt.trim() ? draft.prompt : sent.prompt,
        documentReferences: [...draft.documentReferences, ...missingReferences].slice(0, MAX_DOCUMENT_REFERENCES),
        pendingAttachments: [...draft.pendingAttachments, ...missingAttachments].slice(0, MAX_SESSION_ATTACHMENTS),
      };
    });
  }, [serverIdentity?.userId, updateDraftForSession]);

  // Clears only what this send actually consumed. Anything the user typed,
  // attached or quoted while the request was in flight stays in the draft.
  const clearSentDraft = useCallback((sessionId: string, sent: SentDraftSnapshot, consumeAttachments: boolean) => {
    updateDraftForSession(sessionId, (draft) => {
      const sentReferenceIds = new Set(sent.references.map((reference) => reference.id));
      const sentAttachmentIds = new Set(sent.attachments.map((attachment) => attachment.id));
      const nextPrompt = draft.prompt.trim() === sent.prompt ? '' : draft.prompt;
      const nextReferences = draft.documentReferences.filter((reference) => !sentReferenceIds.has(reference.id));
      const nextAttachments = consumeAttachments
        ? draft.pendingAttachments.filter((attachment) => !sentAttachmentIds.has(attachment.id))
        : draft.pendingAttachments;
      return {
        ...draft,
        prompt: nextPrompt,
        documentReferences: nextReferences,
        pendingAttachments: nextAttachments,
        attachmentError: consumeAttachments ? '' : draft.attachmentError,
      };
    });
  }, [updateDraftForSession]);

  const handleSendMessage = useCallback(async () => {
    // The lock is taken synchronously, so a double click or an Enter+click race
    // can only ever produce one request for one snapshot.
    if (sending.current || loading) return;
    const requestSessionId = activeSessionRef.current;
    if (!requestSessionId) return;
    const draftSnapshot = readDraft(draftsRef.current, requestSessionId);
    if (draftSnapshot.uploadingAttachments) return;
    const requestText = draftSnapshot.prompt.trim();
    // Only references drafted in this session are ever sent; the prompt text,
    // attachments and references are frozen here and never re-read from state.
    const requestReferences = draftSnapshot.documentReferences
      .filter((reference) => isReferenceForSession(reference, requestSessionId));
    const requestAttachments = draftSnapshot.pendingAttachments;
    if (!requestText && requestAttachments.length === 0 && requestReferences.length === 0) return;

    const requestUserId = serverIdentity?.userId ?? '';
    const requestHistorySnapshot = [...(activeSession?.chatHistory ?? [])];
    const userPrompt = requestText || (requestReferences.length > 0
      ? '请结合原文解释引用的论文内容。'
      : '请分析上传的材料，并设计预算受限的轻量消融实验。');
    const attachmentNames = requestAttachments.map((attachment) => attachment.name);
    const referenceSnapshot = requestReferences.map(toWireReference);
    const sentSnapshot: SentDraftSnapshot = {
      sessionId: requestSessionId,
      userId: requestUserId,
      prompt: userPrompt,
      history: requestHistorySnapshot,
      attachments: requestAttachments,
      references: requestReferences,
    };
    // Plan submissions carry both the pending attachment ids and the upload ids
    // behind the document references, de-duplicated into the same 8 slots.
    const attachmentIDs = Array.from(new Set([
      ...sentSnapshot.attachments.map((attachment) => attachment.id),
      ...referenceSnapshot.map((reference) => reference.upload_id),
    ])).slice(0, MAX_SESSION_ATTACHMENTS);
    const explicitExecution = isExplicitExecutionIntent(userPrompt);
    // Reading mode survives a reload through the persisted user turns: a history
    // that already contains a referenced turn marks this session as a reading
    // session even though the in-memory draft was reset. Ordinary reading turns
    // — including 总结论文/分析/总结 — stay in chat, while an explicit
    // reproduce/run request still escalates to a plan.
    const isReadingTurn = draftSnapshot.readingMode
      || requestReferences.length > 0
      || historyHasReadingTurn(requestHistorySnapshot);
    const isTaskRequest = isReadingTurn
      ? explicitExecution
      : requestAttachments.length > 0 || isTaskIntent(userPrompt);
    sending.current = true;
    setLoading(true);
	appendMessageToSession(requestSessionId, {
		role: 'user',
		text: isTaskRequest && attachmentNames.length > 0 ? `${userPrompt}\n\n附件：${attachmentNames.join('、')}` : userPrompt,
		...(referenceSnapshot.length > 0 ? { documentReferences: referenceSnapshot } : {}),
	});

    // A turn that carries a document reference is a reading turn: it only goes
    // to the plan path when the user explicitly asks to reproduce or run
    // something. A session that is already reading keeps that mode for later
    // follow-ups, even when they mention 论文/分析/总结/summary. Untouched
    // attachments never force a reading question into a plan.
    if (requestReferences.length > 0 && !explicitExecution && !draftSnapshot.readingMode) {
      updateDraftForSession(requestSessionId, (draft) => ({ ...draft, readingMode: true }));
    }

    try {
      if (isTaskRequest) {
        const args: Parameters<typeof createPlan> = [
          userPrompt,
          { userId: requestUserId, sessionId: requestSessionId },
          attachmentIDs,
          runtimePermissionMode,
          runtimePermissionMode === 'full_access' && fullAccessAcknowledged,
        ];
        const signature = JSON.stringify(args);
        let submission = pendingSubmissions.current.get(requestSessionId);
        if (!submission || submission.signature !== signature) {
          submission = { signature, key: createId('plan'), args, commitUnknown: false };
          pendingSubmissions.current.set(requestSessionId, submission);
        }
        if (submission.commitUnknown) {
          throw new Error('原计划保存结果尚未确认，请先从计划库核对原计划；确认前不能重新创建。');
        }
        // A retry uses both the original key and its original request values.
        const response = await createPlan(submission.args[0], submission.args[1], submission.args[2], submission.args[3], submission.args[4], submission.key);
        const generatedPlanGraph = response.plan_graph;
        const returnedIntentContext = response.intent_context;
        if (!generatedPlanGraph) throw new Error('Backend did not return plan_graph');
        pendingSubmissions.current.delete(requestSessionId);

        // 计划数据跟随 session 保存，切回历史 session 时可以还原右侧图谱。
        updateSessionPlanState(requestSessionId, {
          intentContext: returnedIntentContext ?? null,
          activePlanId: generatedPlanGraph.id,
          planGraph: generatedPlanGraph,
        });
        const clarificationText = formatPlanClarification(response.clarification);
        if (clarificationText) {
		appendMessageToSession(requestSessionId, {
            role: 'system',
            text: clarificationText,
		});
        }
        clearSentDraft(requestSessionId, sentSnapshot, true);
        // The plan message must land before any execution request, otherwise the
        // chat log claims the run started before the plan existed. Auto-run gets
        // a short first line plus the dedicated "auto started" line; approval
        // requirements force the manual copy so the UI never claims a start.
        const autoRun = isAutoRunPlanEligible(generatedPlanGraph, autoRunOnCreate);
        appendMessageToSession(requestSessionId, {
          role: 'system',
          text: autoRun ? uiText.planGeneratedBrief : uiText.planGenerated,
        });
        if (autoRun) {
          appendMessageToSession(requestSessionId, {
            role: 'system',
            text: uiText.planAutoStarted,
          });
        }
        onPlanReady?.(generatedPlanGraph, { autoRun }, returnedIntentContext ?? null);
        void refreshServerPlans();
      } else {
        const response = await chat(userPrompt, {
          userId: requestUserId,
          sessionId: requestSessionId,
        }, {
          sessionId: requestSessionId,
          ...(referenceSnapshot.length > 0 ? { documentReferences: referenceSnapshot } : {}),
        });
        const citationText = response.answer_citations && response.answer_citations.length > 0
          ? `\n\n原文依据：${response.answer_citations.map((citation) => `[${citation.kind}] ${citation.title}${citation.quote ? `：“${citation.quote}”` : ''}`).join('；')}`
          : '';
        // A plain reading chat never consumes pending attachments: they stay in
        // the session draft for the实验 request they were staged for.
        clearSentDraft(requestSessionId, sentSnapshot, false);
        appendMessageToSession(requestSessionId, { role: 'system', text: response.response + citationText, contextReport: response.context_report });
      }
    } catch (error) {
      console.error(error);
      // A plan may have been created server-side even though the client timed
      // out; point the user at the plan library instead of a generic failure.
      let message: string = uiText.backendError;
      if (error instanceof Error && error.message === PLAN_CREATE_TIMEOUT_MESSAGE) {
        message = error.message;
      } else {
        const formatted = formatApiError(error);
        message = formatted.remediation
          ? `${formatted.title}：${formatted.detail}\n修复建议：${formatted.remediation}`
          : `${formatted.title}：${formatted.detail}`;
      }
      if (isTaskRequest) {
        const data = (error as { response?: { data?: { code?: string; plan_id?: string } } })?.response?.data;
        const submission = pendingSubmissions.current.get(requestSessionId);
        if (data?.code === 'idempotency_commit_unknown' && submission) {
          submission.commitUnknown = true;
          message = `计划保存结果尚未确认${data.plan_id ? `（${data.plan_id}）` : ''}，请先从计划库查询原计划。服务重启或切换后端后，请先核对，避免重复创建。`;
        } else if (submission && !submission.commitUnknown) {
          message += '\n可再次发送相同输入重试；当前服务进程内会复用原提交。若服务已重启，请先核对计划库。';
        }
        void refreshServerPlans();
      }
      // A failed send keeps the question and its references in the session draft
      // it was sent from, so the user can retry without retyping or re-quoting.
      restoreFailedDraft(sentSnapshot);
      const contextReport = (error as { response?: { data?: { context_report?: import('../../contracts/api').ContextReport } } })?.response?.data?.context_report;
      appendMessageToSession(requestSessionId, { role: 'system', text: message, contextReport });
    } finally {
      sending.current = false;
      setLoading(false);
    }
	}, [activeSession?.chatHistory, appendMessageToSession, autoRunOnCreate, clearSentDraft, fullAccessAcknowledged, serverIdentity?.userId, loading, onPlanReady, refreshServerPlans, restoreFailedDraft, runtimePermissionMode, updateDraftForSession, updateSessionPlanState]);

  return {
    prompt,
    setPrompt,
    loading,
	pendingAttachments,
	uploadingAttachments,
	attachmentError,
    documentReferences,
    addDocumentReference,
    removeDocumentReference,
    chatHistory,
    activePlanId,
	activePlanStatus,
	activePlanGraph: activeSession?.planGraph ?? null,
    intentContext,
    appendChatMessage,
    isLoggedIn: Boolean(serverIdentity?.userId),
    userId: serverIdentity?.userId ?? persistedState.userId,
	requestIdentity: {
		userId: serverIdentity?.userId ?? '',
		sessionId: serverIdentity?.sessionId ?? persistedState.activeSessionId ?? 'session-unavailable',
	},
	storageWarning,
    serverPlans,
    handleOpenServerPlan,
    refreshServerPlans,
    loginInput,
    setLoginInput,
    activeSessionId: persistedState.activeSessionId,
    sessionSummaries,
    handleLogin,
    handleCreateSession,
    handleSwitchSession,
    handleSendMessage,
    runtimePermissionMode,
    autoRunOnCreate,
    setAutoRunOnCreate,
    autoRunControl,
    showFullAccessConfirm,
    handleRuntimePermissionModeChange: (mode: 'confirm_important' | 'full_access') => {
      if (mode === 'full_access') {
        setShowFullAccessConfirm(true);
        return;
      }
      setRuntimePermissionMode('confirm_important');
      setFullAccessAcknowledged(false);
      setShowFullAccessConfirm(false);
    },
    handleConfirmFullAccess: () => {
      setRuntimePermissionMode('full_access');
      setFullAccessAcknowledged(true);
      setShowFullAccessConfirm(false);
    },
    handleCancelFullAccess: () => setShowFullAccessConfirm(false),
	handleAttachFiles,
	handleRemoveAttachment,
	attachUploadToSession,
	refreshActivePlan,
  };
}
