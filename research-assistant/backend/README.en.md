# backend

[简体中文](README.md) | English

The Go backend is the business core of Research Assistant Agent. It owns the HTTP API, intent routing, planning, DAG scheduling, agent orchestration, memory system, tool ecosystem, sandbox calls, uploads and document management, literature retrieval, and SSE event delivery.

Module path: `research-assistant-backend`; the Go version is in [go.mod](go.mod) (currently 1.27.3).

## Architecture and lifecycle

- [System architecture](../../docs/diagrams/architecture.drawio) ([SVG preview](../../docs/diagrams/architecture.svg))
- [Runtime flow](../../docs/diagrams/runtime-flow.drawio) ([SVG preview](../../docs/diagrams/runtime-flow.svg))
- [Agent execution flow](../../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../../docs/diagrams/agent-execution-flow.svg))

Plan lifecycle:

```text
HTTP API -> Intent Router -> Complexity Router -> Planner + DAG Validator
          -> Revision Store + Review Gate (optional)
          -> PlanStore
          -> Scheduler (lease/retry/cancel/budget/runtime approval)
          -> RoutedTaskExecutor -> AgentProfile + Memory injection
          -> specialized agent -> ToolInvoker / Sandbox Client
          -> artifact validation -> PlanFinalizer -> Event Bus -> SSE
```

## Entry points

| Entry point | Description |
|---|---|
| `cmd/api/main.go` | Pure API server on `:8080`, designed to work with an external sandbox service |
| `cmd/app/main.go` | Single-file app with embedded frontend; requires `SANDBOX_URL` at runtime and never starts a local sandbox |
| `cmd/mockmcp/main.go` | MCP stdio/HTTP mock server for container tests |

Both main entries expose `GET /ping`. The backend builds on Windows through build-tag abstractions in `internal/procgroup`, `internal/sysinfo`, and `internal/winpath`; the desktop client runs it as `backend.exe`. Linux containers remain the recommended server runtime.

## Directory layout

```text
backend/
├── cmd/
│   ├── api/                 Pure API entry
│   ├── app/                 Single-file app entry
│   └── mockmcp/             MCP stdio/HTTP mock server for container tests
├── internal/
│   ├── agent/               Specialized agents, repair sessions, AutoResearch/Benchmark/paper reproduction
│   ├── agentctx/            Task context propagation
│   ├── agentprofile/        AgentProfile/PromptVersion storage, rendering, evaluation, profile runner
│   ├── api/                 HTTP routes, auth, uploads, plan runtime, memory/literature/document/model APIs
│   ├── appconfig/           LLM config loading (env + optional config.toml)
│   ├── apppaths/            Application data directory resolution
│   ├── assist/              PDF selection translation/explanation (no tools, no code execution)
│   ├── audit/               Audit export
│   ├── blobstore/           Content-addressed artifact storage (blob:<sha256>)
│   ├── budget/              Plan/task cost budgets, reservation/settlement, alerts
│   ├── catalog/             Data-driven repository and smoke recipe catalog
│   ├── documents/           PDF parsing, OCR, layout/tables/figures, quote location, quality reports
│   ├── events/              Event bus
│   ├── intent/              Rule/LLM/hybrid/shadow intent routing and post-validation
│   ├── literature/          Multi-provider search, library, BibTeX, citation graph
│   ├── llmclient/           OpenAI-compatible text client (retry/timeout)
│   ├── logging/             Logging and rotation
│   ├── memory/              Layered memory, BM25/entity index, RRF/MMR, extraction, consolidation
│   ├── metrics/             Prometheus metrics
│   ├── modelprofile/        Per-user/global model profiles, encrypted storage, routing, vision probe
│   ├── modelresolver/       Owner/role model-profile resolution with revision cache
│   ├── models/              Plan, task, artifact, runtime, AutoResearch, memory contracts
│   ├── oaichat/             Multimodal OpenAI-compatible client (visual verification/probe)
│   ├── paperid/             arXiv ID parsing
│   ├── planner/             Intent-to-DAG planner, complexity router, template registry
│   ├── procgroup/           Linux process group / Windows Job Object abstraction
│   ├── project/             Plan project grouping
│   ├── prompts/             Prompt assembly, memory sections, token budgets
│   ├── proxyenv/            Proxy environment cleanup
│   ├── revision/            Immutable plan revision storage
│   ├── sandbox/             Sandbox gateway HTTP client (including OpenSandbox)
│   ├── scan/                Optional ClamAV malware scanning
│   ├── scheduler/           DAG execution, leases, runtime approval, repo discovery/prep, memory injection
│   ├── session/             Server-side session store
│   ├── store/               Plan and event storage (file/memory)
│   ├── sysinfo/             CPU/memory/disk capability probing (Linux/macOS/Windows)
│   ├── tools/               Tool registry, JSON Schema validation, native/HTTP/MCP transports, ToolInvoker
│   ├── uploads/             Upload path helpers
│   ├── uploadstore/         Upload quota, dedup, lifecycle
│   ├── usage/               Token/cost/sandbox-time accounting
│   ├── webread/             Web content extraction with SSRF/robots protection
│   ├── winpath/             Windows path and POSIX path translation
│   └── workspace/           Workspace paths
├── evals/                   Planning evaluation dataset and tests
├── tests/                   Cross-module integration tests
└── webui/                   Frontend embed entry
```

## Package responsibilities

### planner

Converts user intent, attachments, and context into a `PlanGraph`. It owns node types, dependencies, input/output contracts, and structural validation so the Scheduler only receives valid DAGs.

- `BuildPlanWithRoute` is the main entry; `ComplexityRouter` in `route.go` makes the final L0–L4 decision after attachment/paper context resolution.
- `plan_builder.go` / `plan_variants.go` own the template registry and simple/standard/full variants; `general_plan.go` owns the General direct/research/paper/literature shapes.
- L3/L4 use skeleton + agent planner with one structured repair pass and fall back to standard templates on failure.

### scheduler

Executes the DAG by in-degree and maintains leases, retries, cancellation, budgets, and runtime approvals. Repository tasks include discovery (`repo_discovery.go`), preparation (`repo_prepare.go`), and reproduction-mode control (`reproduction_mode.go`). `runtime_gate.go` decides whether image pulls, dependency installs, data/model downloads, GPU use, and network access pause for approval; `SetToolGate` wires tool_call allowlist/grant checks into the same gate.

### memory

Layered memory: `types.go` defines Kind/Trust/Status/Scope/Source/Evidence/Validity/Entity/Record/Query/Context/Citation/Settings/Patch; `store.go` implements JSONL WAL + atomic snapshots + directory locking + idempotent dedup + supersede + compaction; `index.go` provides a mixed Chinese/English tokenizer (CJK bigram), BM25, and entity/kind/status inverted indexes; `retrieve.go` handles owner/scope filtering, BM25 + entity + recency recall, RRF, trust/importance/recency/confidence/scope weighting, MMR, and core/retrieved budgets; `extract*.go` extracts plan/task/document/AutoResearch memory; `consolidate.go` performs TTL/archival and reflection.

### session

Server-side `SessionStore`: create/ensure/list/touch/delete and activate; chat sessions and plan sessions share the same identity and survive restarts.

### tools

Tools are "code with side effects": `internal/tools` provides Memory/File stores for ToolDefinition/MCPServer/ToolGrant/ToolCallRecord, JSON Schema validation, native/HTTP/MCP stdio/MCP HTTP transports, and `ToolInvoker`. Execution enforces `AllowedTools`, AgentProfile allowlists, and ToolGrant; approved calls are written to `PlanGraph.ToolCalls` and support dry-run/real replay.

### agentprofile

`AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps`. Prompt versions are immutable; `ActivatePrompt` only moves the active pointer. `ProfileRunner` renders templates, resolves the model, and records `agent_profile_id/prompt_version/prompt_hash/model`; unknown profiles fail plan validation.

### agent

| Agent | Files | Responsibility |
|---|---|---|
| Chat | `chat.go` | General Q&A and lightweight task entry |
| Librarian | `librarian.go`, `claim_evidence.go`, `literature_search.go` | Paper parsing, retrieval, method/claim extraction, claim rubric, literature search |
| Benchmark | `benchmark_agent.go`, `benchmark_contract_agent.go`, `benchmark_dataset.go`, `benchmark_harness.go`, `benchmark_adapter.go` | Dataset audit, splits, leakage checks, Metric/Reward contracts, hidden metric recomputation |
| Coder | `coder.go`, `coder_eino.go`, `coder_runtime.go`, `coder_runtime_flow.go`, `coder_exec.go`, `coder_python.go`, `coder_deps.go`, `coder_image.go` | Repository discovery, code generation, plotting, runtime prepare/install/validate, dependency resolution |
| Research Coding | `research_coding_agent.go`, `reproduction_spec.go` | Paper-repo debugging, benchmark adapters, RuntimeSpec, code-patch AutoResearch |
| Data | `data.go` | Metric aggregation, paper-claim comparison, evidence-graph verdicts, reports, visual-verification orchestration |
| AutoResearch | `autoresearch*.go` | ResearchSpec freeze, candidate execution, Keep/Reject, rollback, holdout |
| Experiment Research | `experiment_research.go`, `experiment_retrieval_adapter.go`, `experiment_portable_adapter.go` | `experiment.spec/v1` configuration search and domain adapters |
| Ablation ToT | `ablation_tot.go` | Bounded Tree-of-Thoughts ablation design |
| Paper Debug | `paper_debug_harness.go` | Paper-repo debugging state machine and bounded patch validation |
| Visual Verifier | `visual_verifier.go` | Image structure gate + multimodal verdict (pass/fail/inconclusive) |
| Answer | `answer.go` | Final user-facing answer synthesis |

### models

Defines `PlanGraph`, `Task`, `Artifact`, `ResearchSpec`, `TrialLedger`, `BenchmarkContract`, `RuntimePolicy` / `EnvironmentManifest`, `MemoryRecord`, and other shared contracts. When changing them, update the frontend `src/contracts/` and `openapi.json` too.

### store

`PlanStore` has in-memory and file implementations. File storage uses "atomic per-plan snapshots + a small append-only JSONL index/event log": each plan is written to `PLAN_STORE_PATH + ".d/<plan-id>.json"`, while `PLAN_STORE_PATH` only appends small `plan_saved`/`plan_updated`/`event` records. Startup loads snapshots and replays events; legacy full-snapshot JSONL is migrated to snapshot files and compressed to `.legacy-*.gz`.

### revision / project / budget

- `revision`: immutable plan revisions (r1/r2…) + optimistic locking + diff + status projection.
- `project`: plan-library project grouping and archiving.
- `budget`: token/USD/sandbox/download budgets, reservation/settlement, 80% alerts, and soft/hard gates.

### sandbox

`sandbox` is the client for the standalone sandbox gateway; the embedded `sandboxserver` was removed, and even single-file mode must connect to an external sandbox through `SANDBOX_URL`.

## API

Except for `GET /ping`, `GET /api/health`, and `GET /openapi.json`, all `/api` routes pass through `APIAuthMiddleware` (when `API_AUTH_TOKEN` is empty and `REQUIRE_API_AUTH=false`, local development is allowed). `GET /api/plans/:id/stream` can use a 60-second HMAC ticket instead of a Bearer token.

### Health, identity, metadata

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/ping` | Lightweight liveness check |
| `GET` | `/api/health` | Backend, repository, sandbox, optimizer, and intent-service health |
| `GET` | `/api/me` | Server-derived user_id / session_id |
| `GET` | `/openapi.json` | OpenAPI 3.1 contract (no Bearer at root; `/api/openapi.json` is auth-protected) |
| `GET` | `/api/sandbox/capabilities` | Current sandbox provider capability matrix |
| `GET` | `/api/metrics` (and `/metrics`) | Prometheus text metrics (Bearer) |
| `GET` | `/api/audit/export` | Audit export (caller-isolated) |

### Plans, revisions, review

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/plan` | Create a plan |
| `GET` | `/api/plans` / `/api/plans/:id` | List plans / query plan, node states, artifacts |
| `POST` | `/api/plans/:id/execute` | Execute a plan (trigger=manual/auto) |
| `POST` | `/api/plans/:id/pause` / `resume` / `cancel` | Pause/resume/cancel |
| `POST` | `/api/plans/:id/approve` | Approve a plan that needs human confirmation |
| `POST` | `/api/plans/:id/duplicate` | Duplicate a plan |
| `PATCH` / `DELETE` | `/api/plans/:id` | Rename/delete |
| `GET` / `POST` | `/api/plans/:id/revisions` | List/create immutable revisions |
| `GET` | `/api/plans/:id/revisions/:revision` / `/diff` | Read a revision / diff |
| `POST` | `/api/plans/:id/revisions/:revision/validate` / `approve` / `reject` / `execute` | Validate/approve/reject/execute a revision |
| `GET` / `POST` | `/api/plans/:id/questions` / `.../questions/:questionId/answer` | Structured questions and answers |
| `GET` | `/api/approvals` / `POST /api/approvals/bulk` | Approval inbox / bulk decisions |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` / `reassign` / `run` / `execute` | Retry/reassign/rerun-from-node/execute-one-node |
| `GET` | `/api/plans/:id/export?format=md` / `json` / `zip` | Export a minimal reproduction bundle |
| `POST` | `/api/plans/:id/reproduce/verify` | Statically verify the bundle |
| `GET` | `/api/plans/:id/events` | Event history |
| `GET` | `/api/plans/:id/stream-ticket` | 60-second HMAC SSE ticket |
| `GET` | `/api/plans/:id/stream` | SSE stream (supports `?ticket=`) |
| `GET` | `/api/plans/:id/runtime-actions` | Runtime approval requests |
| `POST` | `/api/plans/:id/runtime-actions/:actionId/decision` | Approve/reject a runtime action |
| `GET` | `/api/plans/:id/tool-calls` / `POST .../tool-calls/:callId/replay` | Tool-call audit/replay |

### Memory and sessions

| Method | Endpoint | Description |
|---|---|---|
| `GET` / `POST` | `/api/memories` | List / create memory |
| `GET` / `PATCH` / `DELETE` | `/api/memories/:id` | Read / update / delete |
| `POST` | `/api/memories/search` / `/consolidate` / `/import` / `/feedback` | Search / consolidate / import / feedback |
| `GET` | `/api/memories/stats` / `/export` / `/settings` | Stats / export / settings |
| `PUT` | `/api/memories/settings` | Update memory settings |
| `GET` / `POST` / `DELETE` | `/api/sessions` (plus `/:id/activate`) | Session list/create/activate/delete |

### Tools, agents, prompts

| Method | Endpoint | Description |
|---|---|---|
| `GET` / `POST` | `/api/tools` | List / register tools (native/HTTP/MCP) |
| `GET` / `PUT` / `DELETE` | `/api/tools/:id` | Read/update/delete a tool |
| `POST` | `/api/tools/:id/test` | Test tool connectivity |
| `GET` | `/api/tools/:id/schema` | Tool JSON Schema |
| `GET` / `POST` / `DELETE` | `/api/tool-grants` | Tool grants |
| `GET` / `POST` | `/api/agent-profiles` | List/create AgentProfiles |
| `PUT` / `DELETE` | `/api/agent-profiles/:id` | Update/delete |
| `POST` | `/api/agent-profiles/:id/eval` | Profile evaluation |
| `GET` / `POST` | `/api/prompt-versions` | List/create prompt versions |
| `POST` | `/api/prompt-versions/:id/activate` / `eval` | Activate/evaluate a prompt |

### Uploads and documents

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/uploads` | Upload attachments (SHA-256 dedup, quota, optional ClamAV) |
| `GET` | `/api/uploads/:id` / `content` / `document` | Metadata / content / PDF text-layer summary and page list |
| `GET` | `/api/uploads/:id/document/pages/:page` / `sections/:section` | Page / section |
| `GET` | `/api/uploads/:id/document/search?q=...` | BM25 + Chinese bigram document retrieval |
| `GET` | `/api/uploads/:id/document/quality` / `pages/:page/layout` / `locate` | Quality report / layout / quote location |
| `GET` | `/api/uploads/:id/document/tables/:tableId` / `figures/:figureId` | Table JSON/CSV / figure caption and image |
| `POST` | `/api/uploads/:id/assist` / `translation-jobs` | Selection translation/explanation / full-text translation |
| `GET` | `/api/uploads/:id/translation-jobs` / `:jobId` / `:jobId/pages/:page` | Translation job list/status/page result |
| `POST` | `/api/uploads/:id/translation-jobs/:jobId/cancel` | Cancel a translation job |
| `DELETE` | `/api/uploads/:id` | Delete upload and derived documents |

### Literature, web, models, usage

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/literature/search` | Multi-provider literature search with cross-provider relevance ranking |
| `POST` | `/api/literature/import` | Import DOI/arXiv/PDF URL |
| `GET` / `POST` / `DELETE` | `/api/literature/records` (plus `/:id`, `/attach`, `/bibtex`) | Library, tags, BibTeX, attach to session |
| `GET` | `/api/literature/citations/:doi` | Citation/cited-by graph |
| `POST` | `/api/web/fetch` | Extract public web content |
| `POST` | `/api/chat` | Multi-turn chat |
| `GET` | `/api/pdf-proxy` | PDF proxy |
| `GET` / `POST` / `PUT` / `DELETE` | `/api/model-profiles` (plus `/:id/test`, `/probe-vision`) | Model profiles |
| `GET` / `PUT` | `/api/model-routing` | Main/vision/fallback routing |
| `GET` | `/api/usage/summary` / `/timeseries` / `/api/plans/:id/usage` | Usage statistics |
| `GET` / `PUT` | `/api/budgets` / `/api/budgets/alerts` | Budgets and alerts |
| `GET` / `POST` / `PATCH` / `DELETE` | `/api/projects` (plus `/:id`) | Plan projects |
| `POST` | `/api/execute` | Disabled (410); use in-plan node execution |

## Environment variables

| Variable | Description |
|---|---|
| `OPENAI_API_KEY` | Required OpenAI-compatible API key |
| `OPENAI_BASE_URL` | API base URL; defaults to DeepSeek `https://api.deepseek.com/v1` |
| `OPENAI_MODEL_NAME` | Model name; defaults to `deepseek-chat` |
| `INTENT_SERVICE_URL` | Optional intent service; empty means rule-only routing |
| `INTENT_ROUTER_MODE` | `rule` / `llm` / `hybrid` / `shadow`; code default `rule`, Compose default `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` | Minimum confidence before falling back to rules (default 0.65) |
| `INTENT_CLIENT_TIMEOUT_SECONDS` | Intent-service per-call timeout |
| `SANDBOX_URL` | Sandbox service URL (default `http://localhost:8082`) |
| `SANDBOX_API_TOKEN` | Sandbox Bearer token |
| `SANDBOX_DEFAULT_IMAGE` | Default runtime image for paper reproduction |
| `PLAN_STORE_PATH` | Base path for plan snapshots (`<path>.d`) and the small JSONL index/event log |
| `REVISION_STORE_PATH` | Immutable revision storage; defaults to `<PLAN_STORE_PATH>.revisions.json` |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | Upload directory and size limit |
| `UPLOAD_USER_QUOTA_MB` / `UPLOAD_MAX_FILES` / `UPLOAD_TTL_DAYS` | Per-user quota, file cap, retention |
| `BLOB_STORE_PATH` | Content-addressed artifact storage |
| `MEMORY_ENABLED` / `MEMORY_MODE` / `MEMORY_DIR` | Memory switch, mode, directory |
| `MEMORY_CONSOLIDATE_INTERVAL_SECONDS` | Background consolidation interval; empty disables |
| `BENCHMARK_PRIVATE_ROOT` | Hidden labels and evaluator directory; never mount into the sandbox |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python optimizer |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | Static API token and fail-closed switch; Compose forces `REQUIRE_API_AUTH=true` |
| `AUDIT_LOG_PATH` / `AUDIT_EXPORT_TOKEN` | Audit log path and cross-user export token |
| `CORS_ALLOWED_ORIGINS` | Allowed frontend origins |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | Task and duration budgets |
| `PLAN_RATE_LIMIT_PER_MINUTE` / `PLAN_RATE_LIMIT_BURST` / `PLAN_MAX_ACTIVE_PER_USER` | Plan-creation rate limits and per-user active-plan cap |
| `PLAN_MAX_CONCURRENT_TASKS` / `PLAN_GLOBAL_MAX_TASKS` / `PLAN_STALL_SECONDS` | Concurrency slots and stall detection |
| `APPROVAL_TIMEOUT_SECONDS` | Runtime approval window |
| `PLAN_REVIEW_MODE` / `REVIEW_COST_THRESHOLD_USD` / `QUESTION_TIMEOUT_SECONDS` | Review gate and question timeout |
| `PLAN_MAX_COST_USD` / `PLAN_MAX_TOKENS` / `PLAN_MAX_SANDBOX_SECONDS` / `PLAN_MAX_DOWNLOAD_BYTES` / `PLAN_BUDGET_POLICY` | Plan-level budgets |
| `LLM_MAX_ATTEMPTS` / `LLM_ATTEMPT_TIMEOUT_SECONDS` / `LLM_RETRY_BASE_MS` / `LLM_RETRY_MAX_MS` | LLM retry and timeout |
| `PDF_PARSE_MAX_MB` / `PDF_PARSE_TIMEOUT_SECONDS` | PDF parsing budgets |
| `PDF_OCR_MAX_PAGES` / `PDF_OCR_TIMEOUT_SECONDS` / `PDF_OCR_LANG` / `PDF_OCR_MIN_TEXT_CHARS` | OCR budget, language, text threshold |
| `DOCUMENT_CACHE_DIR` | Document cache; defaults under `PLAN_STORE_PATH` |
| `LITERATURE_CACHE_DIR` / `LITERATURE_LIBRARY_DIR` / `LITERATURE_MAX_RETRIES` / `LITERATURE_PROVIDER_TIMEOUT_SECONDS` | Literature cache, library, provider budget |
| `WEB_FETCH_MAX_MB` / `WEB_FETCH_TIMEOUT_SECONDS` / `WEB_FETCH_RESPECT_ROBOTS` / `WEB_FETCH_CACHE_TTL_SECONDS` | Web-content extraction limits |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` / `ADMIN_TOKEN` | Model-profile storage, encryption key, global profile admin |
| `MODEL_PROFILE_MAX_PER_USER` / `MODEL_PROBE_RATE_LIMIT_SECONDS` | Per-user profile cap and probe rate |
| `VISION_MAX_EDGE_PX` / `VISION_MAX_IMAGES` / `VISION_TIMEOUT_SECONDS` / `VISION_MIN_EDGE_PX` / `FEATURE_VISION_VERIFICATION` | Visual-verification budget and switch |
| `PROMPT_CONTEXT_WINDOW` / `PROMPT_MAX_OUTPUT_TOKENS` / `PROMPT_SAFETY_TOKENS` | Prompt token budgets |
| `PLANNER_AGENT_ENABLED` | Enable the agent planner (E2E turns it off to pin plan shape) |
| `REQUIRE_PLAN_APPROVAL` | Force human approval before execution |
| `LOG_MODEL_OUTPUT` | Write model output to process logs |

## Build and test

```bash
go mod tidy
go build -o bin/api ./cmd/api
go build -o bin/research-assistant ./cmd/app

go test ./...
go test -race -count=1 ./internal/agent/conversation ./internal/prompts ./internal/planner ./internal/api ./internal/literature ./internal/documents ./internal/scheduler ./internal/modelprofile ./internal/modelresolver ./internal/memory ./internal/tools
go test -v ./evals
```

The backend contains platform-specific Linux process-group, `syscall.Kill`, and `Statfs` logic, abstracted behind `procgroup` / `sysinfo` build tags for Windows builds. Server tests should run in a Linux container:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false ./...
```

Single-file builds need frontend output and an external sandbox:

```bash
cd ../frontend && npm run build
cd ../backend && go build -o bin/research-assistant ./cmd/app
SANDBOX_URL=http://localhost:8082 ./bin/research-assistant
```

`cmd/app` no longer starts an embedded sandbox; it exits if `SANDBOX_URL` is missing. `webui/embed.go` uses `//go:embed dist`; `webui/dist/placeholder.txt` keeps compilation working before the frontend is built.

## Integration tests

`tests/` and some `internal/agent` tests need real LLM, network, or Docker access and are skipped by default. Enable them through the environment variables documented in each test, for example:

```bash
REAL_PAPER_REPRO_TEST=1 SANDBOX_URL=http://localhost:8082 \
  go test ./tests -run TestRealPaperReproductionFlow -v -count=1
```

## Sub-modules

- [agent](internal/agent/README.md)
- [planner](internal/planner/README.md)
- [scheduler](internal/scheduler/README.md)
