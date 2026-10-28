import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';
import type {
  ChatResponse,
  ExecutePlanResponse,
  ModelProfile,
  ModelProfileListResponse,
  ModelProfilePayload,
  ModelRouting,
  PlanEvent,
  PlanGraph,
  PlanResponse,
  RuntimeAction,
	UploadedFile,
} from '../../contracts/api';
import { DIRECT_EXECUTION_EVENTS, PLAN_STREAM_EVENT_NAME } from '../../contracts/events';
import { httpClient } from './httpClient';

export interface RequestIdentity {
  userId: string;
  sessionId: string;
}

export interface ServerIdentity {
  user_id: string;
  session_id: string;
}

// getMe resolves the server-derived principal (signed cookie based).
export const getMe = async (): Promise<ServerIdentity> => {
  const response = await httpClient.get<ServerIdentity>('/api/me');
  return response.data;
};

// Identity is carried exclusively by server-signed HttpOnly cookies. The
// frontend never sends a user id header, so a client cannot impersonate another
// principal by editing a request.
const buildIdentityHeaders = (identity?: RequestIdentity) => {
  void identity;
  return {};
};

export const PLAN_CREATE_TIMEOUT_MESSAGE = '计划创建超时；请稍后刷新计划库确认是否已创建。';

// Plan creation can legitimately take 14-50s (LLM planning), longer than the
// default client timeout. The header makes a client retry safe: the backend
// replays the cached plan instead of creating a duplicate.
// Paper reproduction plans can take ~2 minutes with arXiv import plus the
// agent planner, so allow a wider budget than a plain chat request.
const PLAN_CREATE_TIMEOUT_MS = 240_000;

const createIdempotencyKey = (): string => {
  const cryptoApi = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined;
  if (cryptoApi && typeof cryptoApi.randomUUID === 'function') {
    return cryptoApi.randomUUID();
  }
  return `idem-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
};

export const createPlan = async (
  intent: string,
  identity: RequestIdentity,
  attachments: string[] = [],
  runtimePermissionMode: 'confirm_important' | 'full_access' = 'confirm_important',
  runtimePermissionAcknowledged = false,
): Promise<PlanResponse> => {
  try {
    const response = await httpClient.post<PlanResponse>('/api/plan', {
      intent,
      attachments,
      runtime_permission_mode: runtimePermissionMode,
      runtime_permission_acknowledged: runtimePermissionAcknowledged,
    }, {
      headers: {
        ...buildIdentityHeaders(identity),
        'Idempotency-Key': createIdempotencyKey(),
      },
      timeout: PLAN_CREATE_TIMEOUT_MS,
    });
    return response.data;
  } catch (error: unknown) {
    const code = error && typeof error === 'object' ? (error as { code?: unknown }).code : undefined;
    if (code === 'ECONNABORTED') {
      throw new Error(PLAN_CREATE_TIMEOUT_MESSAGE);
    }
    throw error;
  }
};

export interface PlanSummary {
  id: string;
  title: string;
  intent_type: string;
  status: string;
  outcome?: string;
  session_id?: string;
  total_nodes: number;
  completed_nodes: number;
  failed_nodes: number;
  created_at: string;
  updated_at: string;
  finished_at?: string;
}

export const listPlans = async (limit = 20, cursor = '', sessionId = ''): Promise<{ plans: PlanSummary[]; total: number; next_cursor: string }> => {
  const params = new URLSearchParams();
  params.set('limit', String(limit));
  if (cursor) params.set('cursor', cursor);
  if (sessionId) params.set('session_id', sessionId);
  const response = await httpClient.get<{ plans: PlanSummary[]; total: number; next_cursor: string }>('/api/plans?' + params.toString());
  return response.data;
};

export const deletePlan = async (planId: string, identity: RequestIdentity): Promise<{ deleted: boolean }> => {
  const response = await httpClient.delete<{ deleted: boolean }>('/api/plans/' + planId, {
    headers: buildIdentityHeaders(identity),
  });
  return response.data;
};

export const getPlan = async (planId: string, identity: RequestIdentity): Promise<PlanGraph> => {
  const response = await httpClient.get<{ plan_graph: PlanGraph }>(`/api/plans/${planId}`, {
    headers: buildIdentityHeaders(identity),
  });
  return response.data.plan_graph;
};

export const getPlanEvents = async (planId: string, identity: RequestIdentity): Promise<PlanEvent[]> => {
  const response = await httpClient.get<{ events: PlanEvent[] }>(`/api/plans/${planId}/events`, {
    headers: buildIdentityHeaders(identity),
  });
  return response.data.events ?? [];
};

export const uploadFile = async (file: File, identity: RequestIdentity): Promise<UploadedFile> => {
  const formData = new FormData();
  formData.append('file', file);
  const response = await httpClient.post<UploadedFile>('/api/uploads', formData, {
    headers: buildIdentityHeaders(identity),
    timeout: 120_000,
  });
  return response.data;
};

// Upload metadata is owner-scoped and used when a library record is opened or
// attached to a session without going through the file picker.
export const getUpload = async (uploadId: string): Promise<UploadedFile> => {
  const response = await httpClient.get<UploadedFile>(`/api/uploads/${uploadId}`);
  return response.data;
};

export const chat = async (message: string, identity: RequestIdentity): Promise<ChatResponse> => {
  const response = await httpClient.post<ChatResponse>('/api/chat', { message }, { headers: buildIdentityHeaders(identity) });
  return response.data;
};

export type PlanExecutionTrigger = 'manual' | 'auto';

export const executePlan = async (
	planId: string,
	identity: RequestIdentity,
	trigger: PlanExecutionTrigger = 'manual',
): Promise<ExecutePlanResponse> => {
	const response = await httpClient.post<ExecutePlanResponse>(
		`/api/plans/${planId}/execute`,
		{ trigger },
		{ headers: buildIdentityHeaders(identity) },
	);
	return response.data;
};

export const getRuntimeActions = async (planId: string, identity: RequestIdentity): Promise<RuntimeAction[]> => {
  const response = await httpClient.get<{ actions: RuntimeAction[] }>(`/api/plans/${planId}/runtime-actions`, {
    headers: buildIdentityHeaders(identity),
  });
  return response.data.actions ?? [];
};

export const decideRuntimeAction = async (
  planId: string,
  actionId: string,
  decision: 'approve' | 'reject',
  identity: RequestIdentity,
  note = '',
): Promise<RuntimeAction> => {
  const response = await httpClient.post<{ action: RuntimeAction }>(
    `/api/plans/${planId}/runtime-actions/${actionId}/decision`,
    { decision, note },
    { headers: buildIdentityHeaders(identity) },
  );
  return response.data.action;
};

export const approvePlan = async (planId: string, identity: RequestIdentity): Promise<ExecutePlanResponse> => {
	const response = await httpClient.post<ExecutePlanResponse>(`/api/plans/${planId}/approve`, {}, { headers: buildIdentityHeaders(identity) });
	return response.data;
};

export const cancelPlan = async (planId: string, identity: RequestIdentity): Promise<ExecutePlanResponse> => {
	const response = await httpClient.post<ExecutePlanResponse>(`/api/plans/${planId}/cancel`, {}, { headers: buildIdentityHeaders(identity) });
	return response.data;
};

export const retryTask = async (planId: string, taskId: string, identity: RequestIdentity): Promise<ExecutePlanResponse> => {
	const response = await httpClient.post<ExecutePlanResponse>(
		`/api/plans/${planId}/tasks/${taskId}/retry`,
		{},
		{ headers: buildIdentityHeaders(identity) },
	);
	return response.data;
};

export const getPdfProxyUrl = (url: string): string =>
  `${API_BASE_URL}/api/pdf-proxy?url=${encodeURIComponent(url)}`;

export const getPlanStreamTicket = async (planId: string, identity: RequestIdentity): Promise<string> => {
	const response = await httpClient.get<{ ticket: string }>(`/api/plans/${planId}/stream-ticket`, {
		headers: buildIdentityHeaders(identity),
	});
	return response.data.ticket;
};

export interface PlanEventSourceOptions {
  /**
   * Incremental replay cursor. The backend also accepts the standard
   * Last-Event-ID header, but the browser EventSource API offers no way to set
   * request headers, so `after_seq` is the supported reconnect mechanism.
   */
  afterSeq?: number;
  /** Fetch a fresh 60s stream ticket even in cookie-auth (non-token) mode. */
  forceTicket?: boolean;
}

export const createPlanEventSource = async (
  planId: string,
  identity: RequestIdentity,
  handlers: {
    onPlanEvent: (event: PlanEvent) => void;
    onError?: () => void;
  },
  options: PlanEventSourceOptions = {},
): Promise<EventSource> => {
	const params = new URLSearchParams();
	// Tickets expire after 60s, so every reconnect must request a new one.
	if (API_AUTH_TOKEN || options.forceTicket) {
		const ticket = await getPlanStreamTicket(planId, identity);
		params.set('ticket', ticket);
	}
	if (typeof options.afterSeq === 'number' && Number.isFinite(options.afterSeq) && options.afterSeq > 0) {
		params.set('after_seq', String(Math.floor(options.afterSeq)));
	}
	const query = params.toString();
	const url = `${API_BASE_URL}/api/plans/${planId}/stream${query ? `?${query}` : ''}`;
	const source = new EventSource(url, { withCredentials: true });
  source.addEventListener(PLAN_STREAM_EVENT_NAME, (evt) => {
    const parsed = JSON.parse((evt as MessageEvent).data) as PlanEvent;
    handlers.onPlanEvent(parsed);
  });
  if (handlers.onError) {
    source.onerror = handlers.onError;
  }
  return source;
};

const parseSseChunk = (chunk: string): { type: string; data: string }[] => {
  const events = chunk.split('\n\n').filter(Boolean);
  return events.map((event) => {
    const lines = event.split('\n');
    let type = 'message';
    let data = '';
    for (const line of lines) {
      if (line.startsWith('event:')) type = line.substring(6).trim();
      if (line.startsWith('data:')) data += line.substring(5).trim();
    }
    return { type, data };
  });
};

export const executePlanTaskStream = async (
  planId: string,
  taskId: string,
  identity: RequestIdentity,
  handlers: {
    onLog: (line: string) => void;
    onResult: (rawData: string) => void;
    onError: (errorMessage: string) => void;
  },
): Promise<void> => {
  const response = await fetch(
    `${API_BASE_URL}/api/plans/${encodeURIComponent(planId)}/tasks/${encodeURIComponent(taskId)}/execute`,
    {
      method: 'POST',
      credentials: 'include',
      headers: {
        'Content-Type': 'application/json',
        ...buildIdentityHeaders(identity),
        ...(API_AUTH_TOKEN ? { Authorization: `Bearer ${API_AUTH_TOKEN}` } : {}),
      },
      body: '{}',
    },
  );

  if (!response.ok) {
    throw new Error(`[HTTP ${response.status}] 请求失败，请稍后重试；若持续失败请导出计划并联系管理员。`);
  }

  if (!response.body) {
    throw new Error('当前浏览器不支持流式输出，请升级到最新版 Chrome/Edge 后重试。');
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buffer = '';
  let receivedResult = false;

  while (true) {
    let readResult: ReadableStreamReadResult<Uint8Array>;
    try {
      readResult = await reader.read();
    } catch {
      throw new Error('网络连接突然断开了 🔌... 可能是大模型正在深度思考，导致连接超时了，建议刷新页面重试～');
    }

    const { value, done } = readResult;
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const splitIndex = buffer.lastIndexOf('\n\n');
    if (splitIndex < 0) continue;

    const completeChunk = buffer.slice(0, splitIndex);
    buffer = buffer.slice(splitIndex + 2);
    const parsedEvents = parseSseChunk(completeChunk);

    for (const event of parsedEvents) {
      if (event.type === DIRECT_EXECUTION_EVENTS.LOG) {
        handlers.onLog(event.data);
      } else if (event.type === DIRECT_EXECUTION_EVENTS.RESULT) {
        receivedResult = true;
        handlers.onResult(event.data);
      } else if (event.type === DIRECT_EXECUTION_EVENTS.ERROR) {
        handlers.onError(event.data);
        return;
      }
    }
  }
  if (!receivedResult) {
    throw new Error('任务流已结束，但没有收到结果事件；请检查后端日志后重试。');
  }
};
export const listModelProfiles = async (): Promise<ModelProfileListResponse> => {
  const response = await httpClient.get<ModelProfileListResponse>('/api/model-profiles');
  return response.data;
};

export const createModelProfile = async (payload: ModelProfilePayload): Promise<ModelProfile> => {
  const response = await httpClient.post<ModelProfile>('/api/model-profiles', payload);
  return response.data;
};

export const updateModelProfile = async (id: string, payload: ModelProfilePayload): Promise<ModelProfile> => {
  const response = await httpClient.put<ModelProfile>('/api/model-profiles/' + id, payload);
  return response.data;
};

export const deleteModelProfile = async (id: string): Promise<{ deleted: boolean }> => {
  const response = await httpClient.delete<{ deleted: boolean }>('/api/model-profiles/' + id);
  return response.data;
};

export const testModelProfile = async (id: string): Promise<{ ok: boolean; status: string; detail?: string; latency_ms?: number; model?: string }> => {
  const response = await httpClient.post<{ ok: boolean; status: string; detail?: string; latency_ms?: number; model?: string }>(
    '/api/model-profiles/' + id + '/test',
    {},
    { validateStatus: () => true },
  );
  return response.data;
};

export const probeModelVision = async (id: string): Promise<{ status: string; detail?: string; latency_ms?: number; model?: string }> => {
  const response = await httpClient.post<{ status: string; detail?: string; latency_ms?: number; model?: string }>(
    '/api/model-profiles/' + id + '/probe-vision',
    {},
    { timeout: 240_000 },
  );
  return response.data;
};

export const getModelRouting = async (): Promise<{ routing: ModelRouting; global_routing: ModelRouting }> => {
  const response = await httpClient.get<{ routing: ModelRouting; global_routing: ModelRouting }>('/api/model-routing');
  return response.data;
};

export const saveModelRouting = async (payload: {
  main_profile_id: string;
  vision_profile_ids?: string[];
  fallback_profile_ids?: string[];
}): Promise<ModelRouting> => {
  const response = await httpClient.put<{ routing: ModelRouting }>('/api/model-routing', payload);
  return response.data.routing;
};
