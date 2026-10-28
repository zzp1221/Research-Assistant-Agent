export type ChatAction =
  | 'open_pdf'
  | 'close_pdf'
  | 'view_plot'
  | 'view_report';

export interface ChatMessage {
  role: string;
  text: string;
  actions?: ChatAction[];
  taskId?: string;
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
  Verification?: Verification;
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
  verification?: Verification;
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

export interface PlanGraph {
  id: string;
  user_intent: string;
  intent_type: string;
  status: string;
  outcome?: string;
  outcome_reason?: string;
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
    sandbox_seconds?: number;
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

export interface ChatResponse {
  response: string;
  session_id?: string;
  anon_user_id?: string;
  user_id?: string;
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

export interface ModelProfile {
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

export interface ModelProfilePayload {
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
