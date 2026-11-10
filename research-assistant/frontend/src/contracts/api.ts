export type ChatAction =
  | 'open_pdf'
  | 'close_pdf'
  | 'view_plot'
  | 'view_report';

// A document reference is the reading context a chat turn was asked about. It
// is deliberately keyed by the uploaded document identity (never a temporary
// blob URL) so the backend can verify access and reload the quoted page.
export interface DocumentReference {
  upload_id: string;
  document_name: string;
  // One-based page number, converted from the viewer's zero-based page index.
  page: number;
  quote: string;
}

// Front-end draft only: the composer keeps these fields so a reference card can
// be removed or re-opened, and so a draft can never leak into another session.
export interface DocumentReferenceDraft extends DocumentReference {
  id: string;
  sessionId: string;
}

export interface ChatMessage {
  role: string;
  text: string;
  actions?: ChatAction[];
  taskId?: string;
  contextReport?: ContextReport;
  // Provenance of a user turn that was asked against a document passage. These
  // are snapshots of what was sent, not editable drafts.
  documentReferences?: DocumentReference[];
}

export interface OCRStats {
  engine?: string;
  language?: string;
  ocr_pages: number;
  average_confidence?: number;
  failed_pages: number;
  skipped_pages: number;
  total_pages: number;
  partial?: boolean;
}

export interface DocumentPageQuality {
  page: number;
  source: string;
  chars: number;
  ocr_confidence?: number;
  failed?: boolean;
  skipped?: boolean;
  error?: string;
}

export interface UploadDocumentSummary {
  status: string;
  page_count: number;
  total_pages?: number;
  characters: number;
  extractor?: string;
  error?: string;
  extracted_at?: string;
  ocr_stats?: OCRStats;
  tables?: number;
  figures?: number;
  blocks?: number;
}

export interface UploadedFile {
  id: string;
  name: string;
  content_type: string;
  size: number;
  sha256: string;
  content_url: string;
  created_at: string;
  document?: UploadDocumentSummary;
}

export interface DocumentPageInfo {
  number: number;
  chars: number;
  source?: string;
}

export interface DocumentTableSummary {
  id: string;
  page: number;
  caption?: string;
  rows: number;
  confidence?: number;
  csv_ref?: string;
}

export interface DocumentFigureSummary {
  id: string;
  page: number;
  caption?: string;
  image_ref?: string;
  warning?: string;
}

export interface DocumentBlock {
  id: string;
  type: string;
  bbox: [number, number, number, number];
  text: string;
  confidence: number;
  page: number;
}

export interface DocumentInfoResponse {
  upload_id: string;
  name: string;
  document: UploadDocumentSummary;
  pages: DocumentPageInfo[];
  tables?: DocumentTableSummary[];
  figures?: DocumentFigureSummary[];
  quality?: DocumentPageQuality[];
  ocr_stats?: OCRStats;
}

export interface DocumentQualityResponse {
  upload_id: string;
  status: string;
  total_pages: number;
  ocr_stats: OCRStats;
  pages: DocumentPageQuality[];
}

export interface AssistTerm {
  source: string;
  target: string;
  note?: string;
}

export interface AssistResponse {
  mode: string;
  page: number;
  result: string;
  bullets?: string[];
  terms?: AssistTerm[];
  model: string;
  cached: boolean;
}

export interface TranslationPageJob {
  number: number;
  status: string;
  chars: number;
  error?: string;
  updated_at: string;
}

export interface TranslationJob {
  id: string;
  upload_id: string;
  status: string;
  total_pages: number;
  completed_pages: number;
  failed_pages: number;
  current_page: number;
  pages: TranslationPageJob[];
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface PlanBudget {
  max_task_attempts: number;
  max_duration_seconds: number;
  max_tokens?: number;
  max_cost_usd?: number;
  max_sandbox_seconds?: number;
  max_download_bytes?: number;
  policy?: 'soft' | 'hard' | string;
}

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
  review?: ReviewState;
  current_revision?: number;
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

export interface UserBudget {
  owner_id: string;
  period: 'day' | 'month';
  limit_usd: number;
  policy: 'soft' | 'hard';
  updated_at: string;
}

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

export interface UsageTimeseriesPoint {
  key: string;
  cost_usd: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  calls?: number;
}

export interface AgentRunState {
  version: string;
  id: string;
  checkpoint_ref: string;
  status: string;
  stop_reason?: string;
  steps: number;
  max_steps: number;
  tool_calls: number;
  max_tool_calls: number;
  execution_id?: string;
  execution_epoch?: number;
  pending_call_id?: string;
  updated_at: string;
}

export interface AgentFailureInfo {
  class: string;
  code: string;
  stage: string;
  message: string;
  fingerprint: string;
  state_hash: string;
  recoverable: boolean;
  evidence_refs?: string[];
}

export interface AgentRecoveryState {
  chain_id: string;
  max_actions: number;
  used_actions: number;
  http_attempts: number;
  max_http_attempts: number;
  replans: number;
  decisions?: { action: string; reason: string; consumed: number; remaining: number; at: string }[];
}

export interface AgentReplanProposal {
  reason: string;
  failure_fingerprint: string;
  base_revision: number;
  requires_review: boolean;
  frozen_contract_hash: string;
  preserve_artifacts?: string[];
  invalidate_artifacts?: string[];
}

export interface Task {
  ID: string;
  Name: string;
  Type?: string;
  Description: string;
  AssignedTo: string;
  Status: string;
  Dependencies: string[];
  Inputs?: Record<string, unknown>;
  RequiredArtifacts?: string[];
  OutputArtifacts?: string[];
  Result?: string;
  Code?: string;
  StructuredData?: string;
  ImageBase64?: string;
  Error?: string;
  Usage?: TaskUsage;
  Verification?: Verification;
  AgentRun?: AgentRunState;
  Recovery?: AgentRecoveryState;
  Failure?: AgentFailureInfo;
  ReplanProposal?: AgentReplanProposal;
}

export interface GraphTask {
  id: string;
  name: string;
  type: string;
  description: string;
  assigned_to: string;
  status: string;
  dependencies: string[];
  inputs?: Record<string, unknown>;
  required_artifacts: string[];
  output_artifacts: string[];
  parallelizable: boolean;
  priority: number;
  retry_limit: number;
  run_count: number;
  revision?: number;
  enabled?: boolean;
  execution_id?: string;
  execution_epoch: number;
  lease_owner?: string;
  lease_expires_at?: string;
  timeout_seconds?: number;
  runtime_action_ids?: string[];
  contract: {
    version: string;
    input_artifacts: string[];
    output_artifacts: string[];
    allowed_tools: string[];
  };
  result?: string;
  code?: string;
  structured_data?: string;
  image_base64?: string;
  image_base_64?: string;
  error?: string;
  usage?: TaskUsage;
  verification?: Verification;
  agent_run?: AgentRunState;
  recovery?: AgentRecoveryState;
  failure?: AgentFailureInfo;
  replan_proposal?: AgentReplanProposal;
  metadata?: { agent_run?: AgentRunState; [key: string]: unknown };
}

export interface TaskUsageModel {
  model: string;
  calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  cost_usd: number;
  estimated?: boolean;
  cost_source?: string;
}

export interface TaskUsage {
  llm_prompt_tokens?: number;
  llm_completion_tokens?: number;
  llm_calls?: number;
  llm_cost_usd?: number;
  llm_estimated?: boolean;
  cost_source?: string;
  sandbox_seconds?: number;
  duration_seconds?: number;
  attempts?: number;
  models?: Record<string, number>;
  model_breakdown?: TaskUsageModel[];
  prompt_budget_limit?: number;
  prompt_truncated_sections?: number;
  llm_cached_tokens?: number;
}

export interface PlanArtifact {
  key: string;
  type: string;
  producer_task_id: string;
  value?: string;
  location?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface GraphEdge {
  id: string;
  from: string;
  to: string;
  type: string;
}

export interface MissingRequirement {
  kind: string;
  name?: string;
  detail?: string;
  remediation?: string;
}

export interface RuntimeAction {
  id: string;
  plan_id: string;
  task_id: string;
  node_type: string;
  kind: string;
  title: string;
  details?: Record<string, unknown>;
  risk?: string;
  estimated_bytes?: number;
  estimated_sec?: number;
  status: 'pending' | 'approved' | 'rejected' | 'expired' | string;
  requested_at: string;
  decided_at?: string;
  decided_by?: string;
  decision_note?: string;
  auto_approved?: boolean;
}

export interface RuntimePolicy {
  mode: 'confirm_important' | 'full_access' | string;
  hard_limits?: Record<string, unknown>;
  acknowledged_at?: string;
}

export interface QuestionOption {
  id: string;
  label: string;
  description?: string;
}

export interface PlanQuestion {
  id: string;
  plan_id: string;
  revision?: number;
  task_id?: string;
  node_type?: string;
  header?: string;
  prompt: string;
  options?: QuestionOption[];
  multi_select?: boolean;
  allow_free_text?: boolean;
  default_option?: string;
  status: 'pending' | 'answered' | 'expired' | 'canceled' | string;
  requested_at?: string;
  answer?: string;
  answer_option?: string;
  answered_by?: string;
  answered_at?: string;
  expires_at?: string;
  auto_answered?: boolean;
}

export interface ReviewState {
  required: boolean;
  status: 'not_required' | 'pending' | 'approved' | 'rejected' | string;
  reason?: string;
  risk?: 'low' | 'medium' | 'high' | string;
  estimated_usd?: number;
  reviewed_by?: string;
  reviewed_at?: string;
}

export interface PlanRevisionSummary {
  id: string;
  plan_id: string;
  revision: number;
  parent_revision: number;
  status: 'draft' | 'approved' | 'executing' | 'completed' | 'superseded' | 'rejected' | string;
  created_by?: string;
  created_at: string;
  reason?: string;
  diff_summary?: string[];
  hash?: string;
  approved_by?: string;
  approved_at?: string;
}

export interface PlanRevision extends PlanRevisionSummary {
  graph?: PlanGraph;
}

export interface PlanDiffEntry {
  kind: 'node_added' | 'node_removed' | 'node_modified' | 'edge_added' | 'edge_removed' | 'budget_changed' | string;
  node_id?: string;
  field?: string;
  before?: unknown;
  after?: unknown;
}

export interface ApprovalInboxItem {
  id: string;
  kind: 'approval' | 'question' | string;
  plan_id: string;
  plan_title: string;
  task_id?: string;
  node_type?: string;
  revision?: number;
  title: string;
  prompt?: string;
  risk?: string;
  estimated_usd?: number;
  estimated_bytes?: number;
  waiting_seconds: number;
  requested_at: string;
  expires_at?: string;
  action_kind?: string;
  options?: QuestionOption[];
  multi_select?: boolean;
  allow_free_text?: boolean;
  default_option?: string;
  details?: Record<string, unknown>;
}

export interface ValidationIssue {
  node_id?: string;
  field?: string;
  message: string;
  remediation?: string;
}

export interface PlanGraph {
  id: string;
  title?: string;
  user_intent: string;
  intent_type: string;
  parent_plan_id?: string;
  context?: { parent_plan_summary?: string; memory_refs?: string[]; memory_context?: string; [key: string]: unknown };
  status: string;
  outcome?: string;
  outcome_reason?: string;
  current_revision?: number;
  review?: ReviewState;
  questions?: PlanQuestion[];
  paused?: boolean;
  paused_at?: string;
  run_trigger?: 'manual' | 'auto';
  verification?: Verification;
  model_snapshot?: Record<string, unknown>;
  missing_requirements?: MissingRequirement[];
  runtime_policy?: RuntimePolicy;
  runtime_actions?: RuntimeAction[];
  environment_manifest?: Record<string, unknown>;
  owner_id?: string;
  session_id?: string;
  trace_id: string;
  approval: {
    required: boolean;
    status: string;
    reason?: string;
    approved_by?: string;
    approved_at?: string;
  };
  budget: {
    max_task_attempts: number;
    max_duration_seconds: number;
    max_tokens?: number;
    max_cost_usd?: number;
    max_sandbox_seconds?: number;
    max_download_bytes?: number;
    policy?: string;
  };
  usage: {
    task_attempts: number;
    started_at?: string;
    finished_at?: string;
    llm_prompt_tokens?: number;
    llm_completion_tokens?: number;
    llm_calls?: number;
    llm_cost_usd?: number;
    llm_estimated?: boolean;
    cost_source?: string;
    sandbox_seconds?: number;
    budget_status?: string;
    budget_warnings?: string[];
  };
  nodes: GraphTask[];
  edges: GraphEdge[];
  artifacts?: Record<string, PlanArtifact>;
  meta?: {
    total_nodes: number;
    completed_nodes: number;
    failed_nodes: number;
    blocked_nodes: number;
    in_progress_nodes: number;
    ready_nodes: number;
    planner_source?: 'agent' | 'template' | string;
    planner_fallback_reason?: string;
    planning_route?: 'fast_path' | 'standard_template' | 'agent_augmented' | 'template_fallback' | string;
    complexity?: 'L0' | 'L1' | 'L2' | 'L3' | 'L4' | string;
    complexity_source?: 'rule' | 'llm' | 'merged' | 'fallback' | 'disabled' | string;
    template_variant?: 'simple' | 'standard' | 'full' | 'light' | string;
    general_plan_shape?: 'direct' | 'research' | 'paper' | 'literature' | 'legacy' | string;
    memory_refs?: string[];
  };
}

export interface IntentContext {
  raw_intent: string;
  intent_type: string;
  entities: Record<string, unknown>;
  constraints: Record<string, unknown>;
  metadata: Record<string, unknown>;
}

export interface PlanEvent {
  plan_id: string;
  event_type: string;
  task_id?: string;
  task_status?: string;
  payload?: Record<string, unknown>;
  timestamp: string;
  trace_id?: string;
  span_id?: string;
  execution_id?: string;
}

export interface NodeExecutionState {
  logs: string;
  result: string;
  code: string;
  structuredData: string;
  imageBase64?: string;
}

export interface ReproductionResourceProbe {
  cpu_count?: number;
  memory_gb?: number;
  disk_free_gb?: number;
  gpu_count?: number;
  gpu_names?: string[];
  thresholds?: Record<string, unknown>;
}

export interface ReproductionModeDecision {
  requested_mode?: string;
  effective_mode?: string;
  full_eligible?: boolean;
  reasons?: string[];
  probe?: ReproductionResourceProbe;
}

export interface PlanClarificationOption {
  id: string;
  label: string;
  description: string;
}

export interface PlanClarification {
  required?: boolean;
  type?: string;
  recommended_mode?: string;
  question: string;
  options?: PlanClarificationOption[];
  mode_decision?: ReproductionModeDecision;
  resource_probe?: ReproductionResourceProbe;
}

export interface PlanResponse {
  message: string;
  plan_graph: PlanGraph;
  clarification?: PlanClarification;
  intent_context?: IntentContext;
  session_id?: string;
  anon_user_id?: string;
  user_id?: string;
}

export interface MemoryCitation {
  memory_id: string;
  title: string;
  kind: string;
  date?: string;
  source?: string;
  trust?: string;
  quote?: string;
  session_id?: string;
  message_id?: string;
  fact_id?: string;
  revision_id?: string;
}

export interface ContextReport {
	  http_requests?: { generation: number; compaction: number; counting: number };
  profile_id: string;
  profile_revision: number;
  model: string;
  capacity: number;
  input_budget: number;
  compaction_mode: string;
  generation: number;
  input_observation: { tokens?: number; source: string; confidence: string };
  last_compaction?: {
    mode: string; status: string; request_attempts: number; latency_ms: number;
    cost_usd?: number; at: string;
  };
}

export interface ChatResponse {
  response: string;
  context_report?: ContextReport;
  session_id?: string;
  anon_user_id?: string;
  user_id?: string;
  citations?: MemoryCitation[];
  retrieved_sources?: MemoryCitation[];
  answer_citations?: MemoryCitation[];
  wiki_citations?: MemoryCitation[];
}

export interface MemoryRecord {
  id: string;
  version: number;
  kind: string;
  scope: { owner_id: string; project_id?: string; session_id?: string; plan_id?: string; agent_id?: string };
  title: string;
  content: string;
  tags?: string[];
  entities?: { type: string; value: string }[];
  source: { type: string; ref?: string; url?: string; created_at?: string };
  evidence: { class: string; confidence?: number; hidden?: boolean };
  validity?: { valid_from?: string; valid_to?: string; recorded_at?: string };
  importance: number;
  confidence?: number;
  trust: string;
  status: string;
  pinned?: boolean;
  use_count?: number;
  created_at: string;
  updated_at: string;
}

export interface MemorySettings {
  enabled: boolean;
  mode: string;
  retrieval_topk: number;
  core_budget_tokens: number;
  retrieved_budget_tokens: number;
  llm_extraction: boolean;
  retention_days: number;
}

export interface MemoryStats {
  total: number;
  active: number;
  by_kind: Record<string, number>;
  by_status: Record<string, number>;
  tokens: number;
  pinned: number;
}

export interface SessionSummary {
  session_id: string;
  title?: string;
  kind?: string;
  plan_count: number;
  chat_turns: number;
  last_active: string;
}

export interface ExecutePlanResponse {
  message: string;
  plan_id: string;
}

export interface ExecuteTaskPayload {
  task_id: string;
  task_name: string;
  task_type?: string;
  task_description: string;
  assigned_to: string;
  inputs?: Record<string, unknown>;
}

export interface ExecuteTaskResultEvent {
  result?: string;
  code?: string;
  structured_data?: string;
  image_base64?: string;
  image_base_64?: string;
  verification?: Verification;
}

export type VerificationStatus = 'pass' | 'fail' | 'inconclusive' | 'skipped';
export type VerificationLevel = 'structural' | 'text' | 'visual' | 'none';

export interface VerificationVerifiedBy {
  profile_id?: string;
  model?: string;
  mode?: string;
}

export interface ImageVerification {
  source_key?: string;
  sha256?: string;
  width?: number;
  height?: number;
  bytes?: number;
  structural_status?: string;
  structural_issues?: string[];
  visual_status?: string;
  summary?: string;
  issues?: string[];
  evidence?: string[];
  confidence?: number;
  profile_id?: string;
  model?: string;
  error?: string;
}

export interface Verification {
  status: VerificationStatus;
  level: VerificationLevel;
  verified: boolean;
  verified_by?: VerificationVerifiedBy;
  rubric_version?: string;
  image_sha256?: string[];
  issues?: string[];
  evidence?: string[];
  confidence?: number;
  reason?: string;
  checked_at?: string;
  duration_ms?: number;
  token_usage?: { prompt: number; completion: number; estimated?: boolean };
  images?: ImageVerification[];
  warnings?: string[];
}

export interface ModelContextPolicy {
  api_format?: 'chat_completions' | 'responses';
  context_window_source?: 'manual' | 'existing_config' | 'environment' | 'unknown';
  compaction_mode?: 'off' | 'standalone' | 'server' | 'text_summary';
  compaction_trigger_ratio?: number;
  text_summary_target_ratio?: number;
  capabilities?: {
    responses: 'yes' | 'no' | 'unknown';
    standalone_compaction: 'yes' | 'no' | 'unknown';
    server_compaction: 'yes' | 'no' | 'unknown';
    rendered_input_count: 'yes' | 'no' | 'unknown';
	tool_calling?: 'yes' | 'no' | 'unknown';
  };
}

export interface ModelProfile extends ModelContextPolicy {
  id: string;
  owner_id: string;
  global: boolean;
  name: string;
  provider: string;
  base_url: string;
  model: string;
  api_key_masked: string;
  api_key_last4?: string;
  roles: string[];
  supports_vision: 'yes' | 'no' | 'unknown' | string;
  vision_probe_reason?: string;
  vision_probed_at?: string;
  vision_overridden_at?: string;
  timeout_seconds: number;
  max_output_tokens?: number;
  context_window_tokens?: number;
  prompt_price_per_1k?: number;
  completion_price_per_1k?: number;
  revision: number;
  bootstrap?: boolean;
  created_at: string;
  updated_at: string;
}

export interface ModelRouting {
  owner_id: string;
  main_profile_id: string;
  vision_profile_ids?: string[];
  fallback_profile_ids?: string[];
  updated_at?: string;
}

export interface ModelProfileListResponse {
  profiles: ModelProfile[];
  encryption_available: boolean;
  is_admin: boolean;
  max_per_user: number;
}

export interface LiteratureResult {
  source: string;
  id: string;
  title: string;
  authors?: string[];
  year?: number;
  venue?: string;
  doi?: string;
  arxiv_id?: string;
  url?: string;
  pdf_url?: string;
  abstract?: string;
  citations?: number;
  repository_url?: string;
  open_access?: boolean;
  license?: string;
  rank?: number;
  score?: number;
  rank_reason?: string;
}

export interface LiteratureWarning {
  source: string;
  message: string;
}

export interface LiteratureSearchResponse {
  query: string;
  results: LiteratureResult[];
  count: number;
  warnings?: LiteratureWarning[];
  sources?: string[];
  sort?: string;
}

export interface LiteratureRecord {
  id: string;
  source: string;
  external_id?: string;
  title: string;
  authors?: string[];
  year?: number;
  venue?: string;
  abstract?: string;
  doi?: string;
  arxiv_id?: string;
  url?: string;
  pdf_url?: string;
  citations?: number;
  open_access?: boolean;
  license?: string;
  tags?: string[];
  upload_id?: string;
  attached_sessions?: string[];
  created_at: string;
  updated_at: string;
}

export interface LiteratureRecordListResponse {
  records: LiteratureRecord[];
  count: number;
  next_cursor: number;
}

export interface LiteratureImportResponse {
  metadata: LiteratureResult;
  source: string;
  attached: boolean;
  metadata_only?: boolean;
  record?: LiteratureRecord;
  upload?: UploadedFile;
}

export interface LiteratureAttachResponse {
  record: LiteratureRecord;
  upload?: UploadedFile;
  attached_session?: string;
}

export interface CitationGraph {
  doi: string;
  openalex_id?: string;
  title?: string;
  cited_by_count: number;
  references: LiteratureResult[];
  cited_by: LiteratureResult[];
  warnings?: LiteratureWarning[];
}

export interface WebFetchResult {
  title: string;
  url: string;
  text: string;
  excerpt: string;
  links: string[];
  fetched_at: string;
  content_type: string;
  word_count: number;
  cached: boolean;
}

export interface ModelProfilePayload extends ModelContextPolicy {
  name?: string;
  provider?: string;
  base_url?: string;
  model?: string;
  api_key?: string;
  roles?: string[];
  supports_vision?: string;
  timeout_seconds?: number;
  max_output_tokens?: number;
  context_window_tokens?: number;
  prompt_price_per_1k?: number;
  completion_price_per_1k?: number;
  global?: boolean;
}

// ---------------------------------------------------------------------------
// N-13 tool / agent ecosystem
// ---------------------------------------------------------------------------

export interface ToolDefinition {
  id: string;
  owner_id?: string;
  name: string;
  version: string;
  transport: 'native' | 'http' | 'mcp_stdio' | 'mcp_http' | string;
  description?: string;
  input_schema?: string;
  output_schema?: string;
  scopes?: string[];
  side_effect: boolean;
  timeout_sec?: number;
  max_output_bytes?: number;
  approval?: string;
  enabled: boolean;
  builtin?: boolean;
  native?: string;
  server_id?: string;
  remote_name?: string;
  created_at?: string;
  updated_at?: string;
}

export interface MCPServer {
  id: string;
  owner_id?: string;
  name: string;
  transport: string;
  command?: string;
  args?: string[];
  env_keys?: string[];
  working_dir?: string;
  url?: string;
  header_keys?: string[];
  fingerprint?: string;
  status?: string;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ToolGrant {
  id: string;
  owner_id: string;
  agent_profile_id: string;
  tool_id: string;
  scopes?: string[];
  approval?: string;
  created_by?: string;
  created_at: string;
}

export interface AgentProfile {
  id: string;
  owner_id?: string;
  name: string;
  description?: string;
  prompt_version_id?: string;
  model_profile_id?: string;
  tool_allowlist?: string[];
  max_steps?: number;
  execution_mode?: 'single' | 'tool_loop';
  max_tool_calls?: number;
  output_schema?: string;
  builtin?: boolean;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface PromptVersion {
  id: string;
  owner_id?: string;
  name: string;
  version: number;
  template: string;
  variables?: string[];
  status: 'draft' | 'active' | 'archived' | string;
  eval_score?: number;
  eval_detail?: string;
  created_by?: string;
  created_at: string;
}

export interface ToolCallRecord {
  id: string;
  plan_id: string;
  task_id: string;
  tool_id: string;
  tool_version: string;
  agent_profile_id?: string;
  arguments?: Record<string, unknown>;
  arguments_hash: string;
  result_hash?: string;
  result_preview?: string;
  status: string;
  error_code?: string;
  error?: string;
  approval_id?: string;
  dry_run?: boolean;
  truncated?: boolean;
  output_bytes?: number;
  replay_of?: string;
  started_at: string;
  finished_at?: string;
  duration_ms?: number;
}

export interface PromptEvalCase {
  name?: string;
  variables?: Record<string, string>;
  expected_contains?: string;
  check_rendering?: boolean;
  task?: { description: string; inputs?: Record<string, unknown>; contract: { version: string; input_artifacts: string[]; output_artifacts: string[]; allowed_tools?: string[] } };
  grader?: { output_schema?: string; expected_json?: Record<string, unknown>; required_tools?: string[]; required_artifacts?: Record<string, string>; expected_status?: string; forbidden_tools?: string[]; max_model_steps?: number; max_tool_calls?: number; expected_stop_reason?: string };
}

export type PromptEvalMode = 'template' | 'model' | 'task';
export interface PromptEvalRequest { cases: PromptEvalCase[]; mode?: PromptEvalMode; use_model?: boolean; model_profile_id?: string; profile_id?: string }

export interface PromptEvalResult {
  prompt_version_id: string;
  score: number;
  passed: number;
  total: number;
  scored: number;
  failed: number;
  errors: number;
  skipped: number;
  attempted: number;
  score_available: boolean;
  scored_rate: number | null;
  planned_rate: number | null;
  grader_version: string;
  profile_id?: string;
  cases: { name?: string; passed: boolean; score: number; detail?: string; status: 'passed' | 'failed' | 'error' | 'skipped'; mode: PromptEvalMode; grader_version: string; attempted: boolean; model_invoked: boolean; evidence_refs?: string[] }[];
  mode: PromptEvalMode;
  evaluated_at: string;
}

export interface SandboxProbeCase {
  name: string;
  security: boolean;
  passed: boolean;
  detail?: string;
}

export interface SandboxProbeReport {
  cases?: SandboxProbeCase[];
  appcontainer_ok?: boolean;
  reason?: string;
}

export interface SandboxCapabilities {
  probe?: SandboxProbeReport;
  provider?: string;
  isolation?: string;
  images?: boolean;
  network_control?: boolean;
  network_enforced?: 'os' | 'firewall' | 'none' | string;
  gpu?: boolean;
  gpu_enforced?: boolean;
  python_versions?: string[];
  os?: string;
  notes?: string[];
  docker_available?: boolean;
  server_version?: string;
  cpu_count?: number;
  memory_total_bytes?: number;
  disk_free_bytes?: number;
  network_mode?: string;
}

export interface EnvironmentManifestView {
  sandbox_provider?: 'docker' | 'native' | 'vm' | 'remote' | string;
  isolation?: 'container' | 'appcontainer' | 'lowil' | string;
  runtime_hash?: string;
  base_os?: string;
  network_enforced?: 'os' | 'none' | string;
  image?: string;
  image_digest?: string;
  python_version?: string;
  status?: string;
}
// ---- Paper Wiki (PaperPage / Point / Link) ----

export interface WikiSourceRef {
  document_id?: string;
  block_id?: string;
  page?: number;
  section?: string;
  quote?: string;
  artifact_ref?: string;
  task_id?: string;
  plan_id?: string;
}

export interface WikiPoint {
  record: MemoryRecord;
  paper_id: string;
  kind: string;
  refs?: WikiSourceRef[];
}

export interface WikiLink {
  id: string;
  owner_id: string;
  project_id?: string;
  from_paper: string;
  to_paper: string;
  type: string;
  evidence?: WikiSourceRef[];
  source: string;
  confidence: number;
  status: string;
  revision: number;
  superseded_by?: string;
  created_at: string;
  updated_at: string;
}

export interface WikiReproductionStatus {
  status: string;
  last_plan_id?: string;
  last_error?: string;
}

export interface WikiPaperPage {
  paper_id: string;
  canonical_key?: string;
  owner_id: string;
  project_id?: string;
  title: string;
  authors?: string[];
  year?: number;
  venue?: string;
  arxiv_id?: string;
  doi?: string;
  abstract?: string;
  tldr?: string;
  tldr_inferred?: boolean;
  sections?: { title: string; start_page: number; end_page: number; chunks: number; characters: number }[];
  point_ids?: string[];
  link_ids?: string[];
  reproduction: WikiReproductionStatus;
  document_ids?: string[];
  entities?: { type: string; value: string }[];
  archived?: boolean;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface WikiPaperList {
  papers: WikiPaperPage[];
  total: number;
  next_cursor: number;
}

export interface WikiPointList {
  points: WikiPoint[];
  total: number;
  next_cursor: number;
}

export interface WikiLinkList {
  links: WikiLink[];
  total: number;
  next_cursor: number;
}

export interface WikiStats {
  papers: number;
  points: number;
  links: number;
  pending_links: number;
  by_kind?: Record<string, number>;
}

export interface WikiBuildJob {
  job_id: string;
  mode: string;
  status: string;
  started_at: string;
  finished_at?: string;
  papers: number;
  links: number;
  error?: string;
}
