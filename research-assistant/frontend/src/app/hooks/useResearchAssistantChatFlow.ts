import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ChatMessage, IntentContext, PlanClarification, PlanGraph, UploadedFile } from '../../contracts/api';
import { chat, createPlan, getMe, getPlan, listPlans, uploadFile } from '../../services/api/researchAssistantApi';
import type { PlanSummary } from '../../services/api/researchAssistantApi';
import { uiText } from '../constants/uiText';

const isTaskIntent = (input: string) => {
  const normalized = input.toLowerCase();
  return [
    /对比|比较|评估|选型|复现|执行|运行|画图|绘图|代码|论文|报告|总结|分析/,
    /rag|benchmark|plot|matplotlib|python|reproduce|summary|report/,
  ].some((pattern) => pattern.test(normalized));
};

interface UseResearchAssistantChatFlowOptions {
  onPlanGraphChanged: (planGraph: PlanGraph | null) => void;
  onPlanReady?: (planGraph: PlanGraph, intentContext: IntentContext | null) => void;
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

interface SessionSummary {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  messageCount: number;
}

const STORAGE_KEY = 'research-assistant.chat-sessions.v1';

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
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(false);
	const [pendingAttachments, setPendingAttachments] = useState<UploadedFile[]>([]);
	const [uploadingAttachments, setUploadingAttachments] = useState(false);
	const [attachmentError, setAttachmentError] = useState('');
  const [runtimePermissionMode, setRuntimePermissionMode] = useState<'confirm_important' | 'full_access'>('confirm_important');
  const [fullAccessAcknowledged, setFullAccessAcknowledged] = useState(false);
  const [showFullAccessConfirm, setShowFullAccessConfirm] = useState(false);
  const [loginInput, setLoginInput] = useState('');
  // The server owns identity (signed HttpOnly cookie). The client only mirrors
  // it for display; it never sends a user id header.
  const [serverIdentity, setServerIdentity] = useState<{ userId: string; sessionId: string } | null>(null);
  const [serverPlans, setServerPlans] = useState<PlanSummary[]>([]);
  const [storageWarning, setStorageWarning] = useState('');

  const refreshServerPlans = useCallback(async () => {
    try {
      const response = await listPlans(20);
      setServerPlans(response.plans ?? []);
    } catch (error) {
      console.warn('[chat] failed to load server plan library', error);
    }
  }, []);

  const handleOpenServerPlan = useCallback(async (planId: string) => {
    try {
      const planGraph = await getPlan(planId, { userId: '', sessionId: '' });
      setPersistedState((prev) => {
        const existing = prev.sessions.find((session) => session.activePlanId === planId);
        if (existing) {
          return { ...prev, activeSessionId: existing.id };
        }
        const now = new Date().toISOString();
        const record: ChatSessionRecord = {
          id: createId('session'),
          title: planGraph.user_intent?.slice(0, 18) || '历史计划',
          createdAt: now,
          updatedAt: now,
          chatHistory: [createWelcomeMessage()],
          activePlanId: planId,
          intentContext: null,
          planGraph,
        };
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
    setPrompt('');
	setPendingAttachments([]);
	setAttachmentError('');
  }, [persistedState.sessions.length, persistedState.userId]);

  const handleSwitchSession = useCallback((sessionId: string) => {
    if (sessionId.startsWith('plan:')) {
      void handleOpenServerPlan(sessionId.slice('plan:'.length));
      return;
    }
    setPersistedState((prev) => ({
      ...prev,
      activeSessionId: sessionId,
    }));
    setPrompt('');
	setPendingAttachments([]);
	setAttachmentError('');
  }, [handleOpenServerPlan]);

	const handleAttachFiles = useCallback(async (files: File[]) => {
		if (files.length === 0 || !persistedState.activeSessionId) return;
		const availableSlots = Math.max(0, 8 - pendingAttachments.length);
		const selected = files.slice(0, availableSlots);
		if (selected.length === 0) {
			setAttachmentError('每个计划最多添加 8 个附件');
			return;
		}
		setUploadingAttachments(true);
		setAttachmentError('');
		const identity = {
			userId: serverIdentity?.userId ?? '',
			sessionId: persistedState.activeSessionId,
		};
		const uploaded: UploadedFile[] = [];
		try {
			for (const file of selected) {
				uploaded.push(await uploadFile(file, identity));
			}
			setPendingAttachments((current) => [...current, ...uploaded].slice(0, 8));
		} catch (error) {
			console.error(error);
			setAttachmentError('附件上传失败，请检查文件类型和大小');
		} finally {
			setUploadingAttachments(false);
		}
	}, [serverIdentity?.userId, pendingAttachments.length, persistedState.activeSessionId]);

	const handleRemoveAttachment = useCallback((uploadId: string) => {
		setPendingAttachments((current) => current.filter((attachment) => attachment.id !== uploadId));
		setAttachmentError('');
	}, []);

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
    try {
      const planGraph = await getPlan(planId, {
        userId: serverIdentity?.userId ?? '',
        sessionId,
      });
      updateSessionPlanState(sessionId, {
        activePlanId: planGraph.id,
        intentContext: session.intentContext ?? null,
        planGraph,
      });
    } catch (error) {
      console.warn('[chat] failed to refresh plan state', error);
    }
  }, [activeSession, serverIdentity?.userId, persistedState.activeSessionId, updateSessionPlanState]);

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

  const handleSendMessage = useCallback(async () => {
	if (loading || (!prompt.trim() && pendingAttachments.length === 0) || !persistedState.activeSessionId || uploadingAttachments) return;

	const userPrompt = prompt.trim() || '请分析上传的材料，并设计预算受限的轻量消融实验。';
	const attachmentNames = pendingAttachments.map((attachment) => attachment.name);
	const attachmentIDs = pendingAttachments.map((attachment) => attachment.id);
    const requestUserId = serverIdentity?.userId ?? '';
    const requestSessionId = persistedState.activeSessionId;
    setLoading(true);
	appendMessageToSession(requestSessionId, {
		role: 'user',
		text: attachmentNames.length > 0 ? `${userPrompt}\n\n附件：${attachmentNames.join('、')}` : userPrompt,
	});
    setPrompt('');

	const isTaskRequest = pendingAttachments.length > 0 || isTaskIntent(userPrompt);

    try {
      if (isTaskRequest) {
		const response = await createPlan(
          userPrompt,
          { userId: requestUserId, sessionId: requestSessionId },
          attachmentIDs,
          runtimePermissionMode,
          runtimePermissionMode === 'full_access' && fullAccessAcknowledged,
        );
        const generatedPlanGraph = response.plan_graph;
        const returnedIntentContext = response.intent_context;
        if (!generatedPlanGraph) throw new Error('Backend did not return plan_graph');

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
        setPendingAttachments([]);
        setAttachmentError('');
        // The plan message must be appended before onPlanReady triggers the
        // auto-run message, otherwise the chat log claims the run started before
        // the plan existed.
        const autoRuns =
          generatedPlanGraph.intent_type === 'Code_Execution' &&
          !generatedPlanGraph.approval?.required &&
          generatedPlanGraph.nodes.some((node) => node.type === 'render_plot');
        appendMessageToSession(requestSessionId, {
          role: 'system',
          text: autoRuns ? uiText.planGeneratedAutoStart : uiText.planGenerated,
        });
        onPlanReady?.(generatedPlanGraph, returnedIntentContext ?? null);
        void refreshServerPlans();
      } else {
        const response = await chat(userPrompt, {
          userId: requestUserId,
          sessionId: requestSessionId,
        });
        appendMessageToSession(requestSessionId, { role: 'system', text: response.response });
      }
    } catch (error) {
      console.error(error);
      appendMessageToSession(requestSessionId, { role: 'system', text: uiText.backendError });
    } finally {
      setLoading(false);
    }
	}, [appendMessageToSession, fullAccessAcknowledged, serverIdentity?.userId, loading, onPlanReady, pendingAttachments, persistedState.activeSessionId, prompt, refreshServerPlans, runtimePermissionMode, updateSessionPlanState, uploadingAttachments]);

  return {
    prompt,
    setPrompt,
    loading,
	pendingAttachments,
	uploadingAttachments,
	attachmentError,
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
	refreshActivePlan,
  };
}
