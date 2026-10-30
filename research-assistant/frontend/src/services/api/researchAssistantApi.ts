import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';
import type {
  AgentProfile,
  ApprovalInboxItem,
  ChatResponse,
  ExecutePlanResponse,
  MCPServer,
  ModelProfile,
  ModelProfileListResponse,
  ModelProfilePayload,
  ModelRouting,
  PlanDiffEntry,
  PlanEvent,
  PlanGraph,
  PlanQuestion,
  PlanResponse,
  PlanRevision,
  PlanRevisionSummary,
  PromptEvalCase,
  PromptEvalResult,
  PromptVersion,
  RuntimeAction,
  ToolCallRecord,
  ToolDefinition,
  ToolGrant,
  ValidationIssue,
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
  project_id?: string;
  total_nodes: number;
  completed_nodes: number;
  failed_nodes: number;
  created_at: string;
  updated_at: string;
  finished_at?: string;
  paused?: boolean;
  archived?: boolean;
  run_trigger?: string;
  usage?: PlanUsageSummary;
}

export interface PlanUsageSummary {
  task_attempts?: number;
  llm_prompt_tokens?: number;
  llm_completion_tokens?: number;
  llm_calls?: number;
  llm_cost_usd?: number;
  llm_estimated?: boolean;
  cost_source?: string;
  sandbox_seconds?: number;
}

export interface Project {
  id: string;
  owner_id?: string;
  name: string;
  description?: string;
  archived?: boolean;
  created_at: string;
  updated_at: string;
}

export interface PlanListQuery {
  q?: string;
  status?: string;
  intent_type?: string;
  project_id?: string;
  archived?: 'false' | 'true' | 'all';
  sort?: 'updated_desc' | 'created_desc' | 'cost_desc';
  cursor?: string;
  limit?: number;
  session_id?: string;
}

export const listPlans = async (limit = 20, cursor = '', sessionId = ''): Promise<{ plans: PlanSummary[]; total: number; next_cursor: string }> => {
  return listPlanLibrary({ limit, cursor, session_id: sessionId });
};

export const listPlanLibrary = async (query: PlanListQuery = {}): Promise<{ plans: PlanSummary[]; total: number; next_cursor: string }> => {
  const params = new URLSearchParams();
  params.set('limit', String(query.limit ?? 20));
  for (const key of ['q', 'status', 'intent_type', 'project_id', 'archived', 'sort', 'cursor', 'session_id'] as const) {
    const value = query[key];
    if (value !== undefined && value !== null && String(value).trim() !== '') {
      params.set(key, String(value));
    }
  }
  const response = await httpClient.get<{ plans: PlanSummary[]; total: number; next_cursor: string }>('/api/plans?' + params.toString());
  return response.data;
};

export const listProjects = async (includeArchived = false): Promise<Project[]> => {
  const response = await httpClient.get<{ projects: Project[] }>('/api/projects?include_archived=' + (includeArchived ? 'true' : 'false'));
  return response.data.projects ?? [];
};

export const createProject = async (payload: { name: string; description?: string }): Promise<Project> => {
  const response = await httpClient.post<{ project: Project }>('/api/projects', payload);
  return response.data.project;
};

export const updateProject = async (id: string, payload: { name?: string; description?: string; archived?: boolean }): Promise<Project> => {
  const response = await httpClient.patch<{ project: Project }>('/api/projects/' + encodeURIComponent(id), payload);
  return response.data.project;
};

export const deleteProject = async (id: string): Promise<{ deleted: boolean }> => {
  const response = await httpClient.delete<{ deleted: boolean }>('/api/projects/' + encodeURIComponent(id));
  return response.data;
};

export const patchPlan = async (planId: string, payload: {
  title?: string;
  project_id?: string;
  archived?: boolean;
  budget?: { max_cost_usd?: number; max_tokens?: number; max_sandbox_seconds?: number; max_download_bytes?: number; policy?: 'soft' | 'hard' };
}): Promise<PlanSummary> => {
  const response = await httpClient.patch<{ plan: PlanSummary }>('/api/plans/' + encodeURIComponent(planId), payload);
  return response.data.plan;
};

export const duplicatePlan = async (planId: string): Promise<PlanSummary> => {
  const response = await httpClient.post<{ plan: PlanSummary }>('/api/plans/' + encodeURIComponent(planId) + '/duplicate', {});
  return response.data.plan;
};

export const getPlanExportUrl = (planId: string, format: 'md' | 'json' | 'zip' = 'zip'): string =>
  API_BASE_URL + '/api/plans/' + encodeURIComponent(planId) + '/export?format=' + format;

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


export interface UsageSummaryResponse {
  summary: {
    owner_id: string;
    period: 'day' | 'month';
    today_usd?: number;
    month_usd?: number;
    spent_usd?: number;
    reserved_usd?: number;
    limit_usd?: number;
    remaining_usd?: number;
    policy?: string;
    configured: boolean;
    top_plans?: { plan_id: string; cost_usd: number; calls: number }[];
    top_models?: { model: string; cost_usd: number; calls: number }[];
  };
}

export interface UsageTimeseriesPoint {
  key: string;
  cost_usd: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  calls?: number;
}

export interface PlanUsageModel {
  model: string;
  calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
  estimated?: boolean;
  cost_source?: string;
}

export interface PlanUsageNode {
  task_id: string;
  name: string;
  type: string;
  status: string;
  prompt_tokens: number;
  completion_tokens: number;
  calls: number;
  cost_usd: number;
  estimated?: boolean;
  cost_source?: string;
  duration_seconds?: number;
  sandbox_seconds?: number;
  models?: PlanUsageModel[];
}

export interface PlanUsageResponse {
  plan_id: string;
  usage: PlanUsageSummary;
  nodes: PlanUsageNode[];
  models: PlanUsageModel[];
  budget?: Record<string, unknown>;
}

export interface UserBudget {
  owner_id: string;
  period: 'day' | 'month';
  limit_usd: number;
  policy: 'soft' | 'hard';
  updated_at: string;
}

export const getUsageSummary = async (period: 'day' | 'month' = 'day'): Promise<UsageSummaryResponse['summary']> => {
  const response = await httpClient.get<UsageSummaryResponse>('/api/usage/summary?period=' + period);
  return response.data.summary;
};

export const getUsageTimeseries = async (group: 'day' | 'model' | 'plan' = 'day', from = '', to = ''): Promise<UsageTimeseriesPoint[]> => {
  const params = new URLSearchParams({ group });
  if (from) params.set('from', from);
  if (to) params.set('to', to);
  const response = await httpClient.get<{ points: UsageTimeseriesPoint[] }>('/api/usage/timeseries?' + params.toString());
  return response.data.points ?? [];
};

export const getPlanUsage = async (planId: string): Promise<PlanUsageResponse> => {
  const response = await httpClient.get<PlanUsageResponse>('/api/plans/' + encodeURIComponent(planId) + '/usage');
  return response.data;
};

export const getBudget = async (): Promise<UserBudget | null> => {
  const response = await httpClient.get<{ budget: UserBudget | null }>('/api/budgets');
  return response.data.budget;
};

export const saveBudget = async (payload: { period: 'day' | 'month'; limit_usd: number; policy: 'soft' | 'hard' }): Promise<UserBudget> => {
  const response = await httpClient.put<{ budget: UserBudget }>('/api/budgets', payload);
  return response.data.budget;
};

export interface BudgetAlert {
  id: string;
  kind: string;
  scope: string;
  policy: string;
  message: string;
  spent_usd: number;
  limit_usd: number;
  at: string;
}

export const getBudgetAlerts = async (): Promise<BudgetAlert[]> => {
  const response = await httpClient.get<{ alerts: BudgetAlert[] }>('/api/budgets/alerts');
  return response.data.alerts ?? [];
};

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

// ---------------------------------------------------------------------------
// N-05: plan revisions, review gate, structured questions and approval inbox
// ---------------------------------------------------------------------------

export const listRevisions = async (planId: string): Promise<{ revisions: PlanRevisionSummary[]; current_revision: number; latest_revision: number }> => {
  const response = await httpClient.get(`/api/plans/${planId}/revisions`);
  return response.data;
};

export const getRevision = async (planId: string, revision: number): Promise<PlanRevision> => {
  const response = await httpClient.get<{ revision: PlanRevision }>(`/api/plans/${planId}/revisions/${revision}`);
  return response.data.revision;
};

export interface CreateRevisionPayload {
  base_revision: number;
  reason?: string;
  graph: Partial<PlanGraph> & Record<string, unknown>;
}

export const createRevision = async (planId: string, payload: CreateRevisionPayload): Promise<{ revision: PlanRevisionSummary; plan: unknown }> => {
  const response = await httpClient.post(`/api/plans/${planId}/revisions`, payload);
  return response.data;
};

export const validateRevision = async (planId: string, revision: number): Promise<{ valid: boolean; errors: ValidationIssue[] }> => {
  const response = await httpClient.post(`/api/plans/${planId}/revisions/${revision}/validate`, {});
  return response.data;
};

export const approveRevision = async (planId: string, revision: number): Promise<{ status: string; review_status: string }> => {
  const response = await httpClient.post(`/api/plans/${planId}/revisions/${revision}/approve`, {});
  return response.data;
};

export const rejectRevision = async (planId: string, revision: number): Promise<{ status: string; review_status: string }> => {
  const response = await httpClient.post(`/api/plans/${planId}/revisions/${revision}/reject`, {});
  return response.data;
};

export const executeRevision = async (planId: string, revision: number, trigger: PlanExecutionTrigger = 'manual'): Promise<ExecutePlanResponse> => {
  const response = await httpClient.post(`/api/plans/${planId}/revisions/${revision}/execute`, { trigger });
  return response.data;
};

export const getPlanDiff = async (planId: string, from: number, to: number): Promise<PlanDiffEntry[]> => {
  const response = await httpClient.get<{ diff: PlanDiffEntry[] }>(`/api/plans/${planId}/diff?from=${from}&to=${to}`);
  return response.data.diff;
};

export const listQuestions = async (planId: string, status = ''): Promise<PlanQuestion[]> => {
  const response = await httpClient.get<{ questions: PlanQuestion[] }>(`/api/plans/${planId}/questions${status ? `?status=${status}` : ''}`);
  return response.data.questions;
};

export interface CreateQuestionPayload {
  task_id?: string;
  header?: string;
  prompt: string;
  options?: { id: string; label: string; description?: string }[];
  multi_select?: boolean;
  allow_free_text?: boolean;
  default_option?: string;
  timeout_seconds?: number;
}

export const createQuestion = async (planId: string, payload: CreateQuestionPayload): Promise<PlanQuestion> => {
  const response = await httpClient.post<{ question: PlanQuestion }>(`/api/plans/${planId}/questions`, payload);
  return response.data.question;
};

export const answerQuestion = async (planId: string, questionId: string, payload: { answer?: string; answer_option?: string }): Promise<PlanQuestion> => {
  const response = await httpClient.post<{ question: PlanQuestion }>(`/api/plans/${planId}/questions/${questionId}/answer`, payload);
  return response.data.question;
};

export const listApprovals = async (params: { status?: string; kind?: string; plan_id?: string; cursor?: string; limit?: number } = {}): Promise<{ items: ApprovalInboxItem[]; total: number; next_cursor: string }> => {
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') query.set(key, String(value));
  });
  const response = await httpClient.get(`/api/approvals?${query.toString()}`);
  return response.data;
};

export const bulkApprovals = async (payload: { ids: string[]; decision: 'approve' | 'reject'; note?: string }): Promise<{ decided: string[]; count: number }> => {
  const response = await httpClient.post('/api/approvals/bulk', { kind: 'approval', ...payload });
  return response.data;
};

// ---------------------------------------------------------------------------
// N-13 tool / agent ecosystem
// ---------------------------------------------------------------------------

export const listTools = async (): Promise<{ tools: ToolDefinition[]; servers: MCPServer[] }> => {
  const response = await httpClient.get('/api/tools');
  return response.data;
};

export const createTool = async (payload: Record<string, unknown>): Promise<{ tool?: ToolDefinition; server?: MCPServer; tools?: ToolDefinition[] }> => {
  const response = await httpClient.post('/api/tools', payload);
  return response.data;
};

export const updateTool = async (id: string, payload: Record<string, unknown>): Promise<{ tool: ToolDefinition }> => {
  const response = await httpClient.put(`/api/tools/${encodeURIComponent(id)}`, payload);
  return response.data;
};

export const deleteTool = async (id: string): Promise<{ disabled: boolean }> => {
  const response = await httpClient.delete(`/api/tools/${encodeURIComponent(id)}`);
  return response.data;
};

export const testTool = async (id: string, payload: { arguments: Record<string, unknown>; dry_run?: boolean }): Promise<{ ok: boolean; record: ToolCallRecord; output: string }> => {
  const response = await httpClient.post(`/api/tools/${encodeURIComponent(id)}/test`, payload);
  return response.data;
};

export const listToolGrants = async (): Promise<ToolGrant[]> => {
  const response = await httpClient.get<{ grants: ToolGrant[] }>('/api/tool-grants');
  return response.data.grants;
};

export const createToolGrant = async (payload: { agent_profile_id: string; tool_id: string; scopes?: string[]; approval?: string }): Promise<ToolGrant> => {
  const response = await httpClient.post<{ grant: ToolGrant }>('/api/tool-grants', payload);
  return response.data.grant;
};

export const deleteToolGrant = async (id: string): Promise<{ deleted: boolean }> => {
  const response = await httpClient.delete(`/api/tool-grants/${encodeURIComponent(id)}`);
  return response.data;
};

export const listAgentProfiles = async (): Promise<AgentProfile[]> => {
  const response = await httpClient.get<{ profiles: AgentProfile[] }>('/api/agent-profiles');
  return response.data.profiles;
};

export const createAgentProfile = async (payload: Record<string, unknown>): Promise<AgentProfile> => {
  const response = await httpClient.post<{ profile: AgentProfile }>('/api/agent-profiles', payload);
  return response.data.profile;
};

export const updateAgentProfile = async (id: string, payload: Record<string, unknown>): Promise<AgentProfile> => {
  const response = await httpClient.put<{ profile: AgentProfile }>(`/api/agent-profiles/${encodeURIComponent(id)}`, payload);
  return response.data.profile;
};

export const deleteAgentProfile = async (id: string): Promise<{ disabled: boolean }> => {
  const response = await httpClient.delete(`/api/agent-profiles/${encodeURIComponent(id)}`);
  return response.data;
};

export const evaluateAgentProfile = async (id: string, payload: { cases?: PromptEvalCase[]; use_model?: boolean; model_profile_id?: string }): Promise<PromptEvalResult> => {
  const response = await httpClient.post<{ result: PromptEvalResult }>(`/api/agent-profiles/${encodeURIComponent(id)}/eval`, payload);
  return response.data.result;
};

export const listPromptVersions = async (name = ''): Promise<PromptVersion[]> => {
  const response = await httpClient.get<{ prompts: PromptVersion[] }>(`/api/prompt-versions${name ? `?name=${encodeURIComponent(name)}` : ''}`);
  return response.data.prompts;
};

export const createPromptVersion = async (payload: { name: string; template: string }): Promise<PromptVersion> => {
  const response = await httpClient.post<{ prompt: PromptVersion }>('/api/prompt-versions', payload);
  return response.data.prompt;
};

export const activatePromptVersion = async (id: string): Promise<PromptVersion> => {
  const response = await httpClient.post<{ prompt: PromptVersion }>(`/api/prompt-versions/${encodeURIComponent(id)}/activate`, {});
  return response.data.prompt;
};

export const evaluatePromptVersion = async (id: string, payload: { cases?: PromptEvalCase[]; use_model?: boolean; model_profile_id?: string }): Promise<PromptEvalResult> => {
  const response = await httpClient.post<{ result: PromptEvalResult }>(`/api/prompt-versions/${encodeURIComponent(id)}/eval`, payload);
  return response.data.result;
};

export const listToolCalls = async (planId: string): Promise<ToolCallRecord[]> => {
  const response = await httpClient.get<{ tool_calls: ToolCallRecord[] }>(`/api/plans/${planId}/tool-calls`);
  return response.data.tool_calls;
};

export const replayToolCall = async (planId: string, callId: string, payload: { allow_side_effects?: boolean; approval_id?: string }): Promise<Record<string, unknown>> => {
  const response = await httpClient.post(`/api/plans/${planId}/tool-calls/${encodeURIComponent(callId)}/replay`, payload);
  return response.data;
};
