# research-assistant

[简体中文](README.md) | English

`research-assistant` is the main product implementation of Research Assistant Agent. It combines a Go backend, a React workbench, a standalone sandbox service, a Python research optimizer, and optional AI services into a runnable multi-agent research execution system.

- Backend: [backend](backend/README.md)
- Frontend: [frontend](frontend/README.md)
- Sandbox: [docker-sandbox](docker-sandbox/README.md)
- Optimizer: [research-optimizer](research-optimizer/README.md)
- Optional AI services: [ai-services](ai-services/README.md)
- Examples: [examples](examples/README.md)
- Scripts: [scripts](scripts/README.md)
- Test sample: [test/claim-evidence](test/claim-evidence/README.md)

## 1. Services

| Service | Directory | Port | Responsibility |
|---|---|---:|---|
| Backend | `backend/` | 8080 | Gin API, intent routing, planner, scheduler, agents, memory, tools, SSE |
| Frontend | `frontend/` | 5173 (dev) / 80 (container) | React workbench, DAG visualization, PDF reader, plan library, review, cost, memory, settings |
| Docker Sandbox | `docker-sandbox/` | 8082 | Container/process/VM/remote sandbox lifecycle, command and Python execution, network and resource governance |
| Research Optimizer | `research-optimizer/` | 8090 | Data features, candidate ranking, and SQLite cross-task experience |
| AI Services | `ai-services/` | 18000 | Optional real-LLM intent service and BERT/Qwen training scripts |

Compose splits the sandbox into two roles: `docker-sandbox` is the control plane (gateway) that only authenticates and proxies; `docker-sandbox-executor` is the execution plane (engine) that holds the Docker socket and `/tmp`. The executor does not publish a host port.

## 2. Architecture and main chain

- [System architecture](../docs/diagrams/architecture.drawio) ([SVG preview](../docs/diagrams/architecture.svg))
- [Runtime flow](../docs/diagrams/runtime-flow.drawio) ([SVG preview](../docs/diagrams/runtime-flow.svg))
- [Agent execution flow](../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../docs/diagrams/agent-execution-flow.svg))
- [System architecture (Archify interactive)](../docs/diagrams/system-architecture.html)
- [Multi-agent execution workflow (Archify interactive)](../docs/diagrams/multi-agent-workflow.html)

Main chain for one task:

```text
POST /api/plan
  -> Intent Router / Complexity Router / ResearchContext
  -> Planner + DAG Validator
  -> Revision Store + Review Gate (optional)
  -> PlanStore
  -> Scheduler (lease / retry / cancel / budget / runtime approval)
  -> RoutedTaskExecutor
  -> AgentProfile + Memory injection
  -> specialized agent
  -> ToolInvoker (native / HTTP / MCP) or Sandbox Client
  -> docker-sandbox gateway -> executor -> Provider (docker / native / vm / remote)
  -> Artifact / EnvironmentManifest / PlanOutcome / Claim-Evidence
  -> Memory write + Event Bus / SSE
  -> React Workbench
```

## 3. Capability modules

### 3.1 Planning and context

- **Intent recognition** — rule fast path + optional LLM intent service + post-validation; supports `rule` / `llm` / `hybrid` / `shadow`.
- **Complexity routing** — L0–L4; L0/L1 skip the planner LLM, L2 uses standard templates, L3/L4 use skeleton + agent planner with one structured repair pass.
- **Unified PlanBuilder** — template registry, variants (simple/standard/full), General direct/research/paper/literature shapes, node contracts, and artifact producer validation.
- **ResearchContext + InputBindings** — attachments, paper metadata, document references, literature, constraints, and memory are bound uniformly; missing required inputs fail the task immediately.
- **Plan library** — project grouping, search, filters, keyset pagination, rename, archive/restore, duplicate, export, and delete.
- **Immutable revisions** — every edit creates an r1/r2… snapshot; `base_revision` optimistic locking; running plans reject edits or execution of a new revision.

### 3.2 Execution and results

- **Scheduler** — leases, concurrency slots, retries, cancellation, budgets, stall detection, and runtime approvals; waiting for approval is not a terminal state.
- **Specialized agents** — Chat/General, Librarian, Benchmark, Coder, Research Coding, Data, AutoResearch, Experiment Research, Ablation ToT, Paper Debug, Visual Verifier, Answer.
- **Paper-reproduction runtime** — RuntimeSpec, image/dependency probing, real installation, `EnvironmentManifest`; insufficient resources return `blocked/partial` instead of passing smoke off as success.
- **AutoResearch** — freeze ResearchSpec, baseline, candidate patches/parameters, real evaluator, Keep/Reject, rollback, hidden holdout.
- **Benchmark** — dataset audit, leakage-safe splits, Metric/Reward contracts, hidden-label recomputation.
- **Claim-Evidence** — frozen rubric, claim/criterion/artifact binding, coverage, and evidence graph.
- **Reproducible export** — `manifest.json` + `checksums.sha256` + `environment/*` + `provenance/*`; missing evidence marks the bundle partial.

### 3.3 Memory system (M0–M3)

- `internal/memory` — typed records, JSONL + snapshots, BM25 + Chinese bigram, entity index, RRF/MMR, core/retrieved budgets, redaction, and injection scanning.
- `internal/session` + `chat_store` — server-side sessions, chat turns, summary compression, and citations.
- Plan/task/document/AutoResearch extraction — episode, fact, constraint, negative, procedure, and document memory.
- `consolidate` — TTL/archival plus reflection (pending_review by default).
- API — full `/api/memories` CRUD/search/stats/export/import/settings/consolidate/feedback; `MEMORY_ENABLED=false` is a complete rollback.

### 3.4 Tool ecosystem and MCP (N-13)

- `internal/tools` — ToolDefinition, ToolRegistry, ToolInvoker, native/HTTP/MCP stdio/MCP HTTP.
- Permissions — node `AllowedTools`, AgentProfile `ToolAllowlist`, and `ToolGrant`; violations return `tool_forbidden`.
- Approval — `SideEffect` or `Approval=always/write` creates a `tool_call` RuntimeAction.
- Audit/replay — `ToolCallRecord` stores argument/result hashes, duration, and approval ID; dry-run and real replay are supported.
- `AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps`; prompts are immutable, evaluable, and rollback-capable.

### 3.5 Literature and documents (N-04 / N-07)

- **Literature** — arXiv/OpenAlex/Crossref/GitHub/Semantic Scholar/PubMed/DBLP search, cross-provider relevance ranking, library, tags, BibTeX, citation/cited-by graph, and attach-to-session.
- **Documents** — PDF text-layer parsing, OCR, layout blocks/reading order, table JSON/CSV, figure captions/images, quality reports, BM25 + Chinese bigram retrieval, quote location, selection translation/explanation, and full-text translation.
- **Web** — `/api/web/fetch` extracts public page content with SSRF/robots/content-type/size/timeout/cache protections.

### 3.6 Plan review and cost (N-01/N-02/N-03/N-05)

- **Review gate** — `PLAN_REVIEW_MODE=always`, `require_review=true`, cost threshold, high-risk nodes, missing requirements, or L4 place a plan in `awaiting_review`.
- **Structured questions** — `ask_user_question` task type + `/api/plans/:id/questions`; a timeout auto-answers when a default exists, otherwise blocks and records the timeout.
- **Approval inbox** — aggregates runtime approvals and questions across plans; `POST /api/approvals/bulk` allows only low-risk bulk decisions (≤50).
- **Cost control** — token/USD/sandbox/download budgets; soft overage pauses, hard overage cancels; 80% warning, 100% rejection; pricing follows model profiles.
- **Export bundles** — provider-specific Dockerfile or uv.lock + run command; missing critical information marks the bundle partial.

## 4. Agent matrix

| Agent | Responsibility |
|---|---|
| Chat / General | General conversation, scope clarification, non-paper requests |
| Librarian | Paper parsing, paper_metadata, DocumentStore retrieval, literature search, claim rubric |
| Benchmark | Dataset audit, splits, leakage checks, Metric/Reward contracts, hidden metric recomputation |
| Coder | Code generation, plotting, runtime prepare/install/validate, dependency resolution, bounded repair |
| Research Coding | Repository preparation, benchmark adapters, paper-code execution/debugging, AutoResearch |
| Data | Metric aggregation, paper-claim comparison, evidence-graph construction, reports/charts, visual verification orchestration |
| AutoResearch | ResearchSpec freeze, candidate execution, Keep/Reject, rollback, holdout |
| Experiment Research | `experiment.spec/v1` configuration search and domain adapters |
| Ablation ToT | Bounded Tree-of-Thoughts ablation design |
| Paper Debug | Paper-repository debugging state machine and bounded patch validation |
| Visual Verifier | Image structure gate and multimodal verdict (pass/fail/inconclusive) |
| Answer | Final answer synthesis and citation organization |

## 5. Directory layout

```text
research-assistant/
├── backend/                 Go API, planner, scheduler, agents, memory, tools
├── frontend/                React + TypeScript workbench
├── docker-sandbox/          Standalone Go sandbox service (gateway / engine / providers)
├── research-optimizer/      Python candidate policy and experience service
├── ai-services/             Optional Python AI services (intent recognition)
├── examples/                Runnable examples and acceptance scripts
├── test/                    E2E, golden tests, and fixtures
├── scripts/                 Unix / Windows startup and maintenance scripts
├── docs/                    phase1 / phase2 notes
├── backend.env.example      Backend environment template
├── .env.example             Compose internal token template
├── docker-compose.yml       Local orchestration
└── Makefile                 Development entry points
```

## 6. Quick start

### 6.1 Docker Compose

```bash
cd research-assistant
cp .env.example .env
# Edit .env: set random SANDBOX_API_TOKEN, RESEARCH_OPTIMIZER_API_TOKEN, and API_AUTH_TOKEN
cp backend.env.example backend.env
# Edit backend.env: at minimum set OPENAI_API_KEY; SANDBOX_API_TOKEN must match .env
docker compose up --build -d
```

Then visit:

| Service | Address |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health check | http://localhost:8080/api/health (and `/ping`; no Bearer) |
| Sandbox API | http://localhost:8082 |
| Research Optimizer | http://localhost:8090 |

Health check:

```bash
curl -s http://localhost:8080/api/health
```

Expect `backend.ok=true`, `repository.ok=true`, `sandbox.ok=true`, and `research_optimizer.ok=true`. Logs and shutdown:

```bash
docker compose logs -f
docker compose down
```

### 6.2 Local development

```bash
make install
export SANDBOX_API_TOKEN=<random-token>
make run-sandbox     # Terminal 1: local engine-mode sandbox (requires Docker)
make run-backend     # Terminal 2: needs SANDBOX_URL and API_AUTH_TOKEN
make run-frontend    # Terminal 3
```

Windows developers should prefer the desktop client (`../desktop`) or Docker Compose. The backend builds on Windows through `procgroup` / `sysinfo`; the desktop runs it as `backend.exe`. Some POSIX-dependent features remain limited on Windows.

## 7. Configuration

See [backend.env.example](backend.env.example) and [docker-compose.yml](docker-compose.yml) for the full list. Common variables:

| Variable | Description |
|---|---|
| `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL_NAME` | LLM access; sample values in `backend.env.example` |
| `INTENT_SERVICE_URL` / `INTENT_ROUTER_MODE` | Intent service URL and `rule` / `llm` / `hybrid` / `shadow` mode |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` / `INTENT_CLIENT_TIMEOUT_SECONDS` | Confidence threshold and per-call timeout |
| `SANDBOX_URL` / `SANDBOX_API_TOKEN` | Sandbox URL and Bearer token; gateway/executor fail closed |
| `SANDBOX_PROVIDER` | `auto` / `docker` / `native` / `vm` / `remote` |
| `SANDBOX_DEFAULT_IMAGE` / `SANDBOX_IMAGE_ALLOWLIST` | Default runtime image and allowed image prefixes |
| `PLAN_STORE_PATH` | Base path for plan snapshots (`<path>.d`) and the append-only JSONL index/event log |
| `REVISION_STORE_PATH` | Immutable revision storage; defaults to `<PLAN_STORE_PATH>.revisions.json` |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | Upload directory and per-file limit |
| `MEMORY_ENABLED` / `MEMORY_MODE` / `MEMORY_DIR` | Memory switch, mode, and directory |
| `RESEARCH_OPTIMIZER_URL` / `RESEARCH_OPTIMIZER_API_TOKEN` | Python optimizer URL and internal token |
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | Static API Bearer; Compose forces `REQUIRE_API_AUTH=true` |
| `CORS_ALLOWED_ORIGINS` | Allowed frontend origins |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` | Runtime model-profile directory and encryption key |
| `PLAN_REVIEW_MODE` / `REVIEW_COST_THRESHOLD_USD` | Review gate: `always` forces review; cost threshold in USD (0 disables) |
| `QUESTION_TIMEOUT_SECONDS` | Default structured-question timeout (900 s) |
| `PLAN_MAX_COST_USD` / `PLAN_MAX_TOKENS` / `PLAN_MAX_SANDBOX_SECONDS` / `PLAN_MAX_DOWNLOAD_BYTES` / `PLAN_BUDGET_POLICY` | Plan budgets; soft / hard (default soft) |
| `EXPORT_MAX_FILE_MB` / `EXPORT_MAX_TOTAL_MB` | Reproducible-export per-file and total limits (16 / 128 MB) |

> `VITE_API_AUTH_TOKEN` is only for local Vite development. Production Compose builds do not embed the token; nginx injects `Authorization` at request time.

## 8. Testing and quality gates

```bash
make test                 # backend + sandbox + frontend + offline examples
make test-optimizer       # Python optimizer tests
make eval                 # planner route/plan evaluation dataset
bash scripts/container-go.sh "go test -count=1 ./..."  # full test in a fixed Go image
```

E2E scripts live in [test/e2e](test/e2e) and require a running Compose stack; use `RA_API_URL` / `RA_API_TOKEN`. Existing suites include `http-smoke`, `routing-and-prompts`, `literature`, `literature-plan`, `document-intelligence`, `model-profiles`, `verification`, `sandbox-network`, and `round6-*`; round7 deep tests are in the same directory. Plan-library/repro-bundle/cost-budget suites are `plan-library`, `repro-bundle`, `cost-budget`, `budget-recovery`; review/HITL suites are `plan-revisions`, `review-hitl`; the tool ecosystem suite is `tool-ecosystem`. See [../docs/verification.md](../docs/verification.md) for environment variables and the latest results.

## 9. Operating boundaries

- The default is a single-node research prototype, not a multi-tenant production service.
- Compose exposes only the gateway port locally; the gateway mounts neither the Docker socket nor the host workspace, and only the unpublished internal executor holds the socket.
- Static API tokens, plan ownership, and plan-level approvals exist, but this is not OIDC, RBAC, or production multi-tenant authentication.
- The default image is a CPU/general Python image; GPU passthrough requires NVIDIA Container Toolkit and `SANDBOX_DOCKER_GPUS=all`.
- The paper-reproduction example is a lightweight smoke test, not full dataset training; insufficient resources return `blocked/partial` and never fake success with mocks.
- Framework_Evaluation keeps a deterministic offline baseline; it is not a real LangChain/LlamaIndex performance benchmark.
- The native provider is weaker than a container; do not use `full_access` on untrusted code.

## 10. Sub-modules

- [backend](backend/README.md)
- [frontend](frontend/README.md)
- [docker-sandbox](docker-sandbox/README.md)
- [research-optimizer](research-optimizer/README.md)
- [ai-services](ai-services/README.md)
- [examples](examples/README.md)
- [scripts](scripts/README.md)
- [test/claim-evidence](test/claim-evidence/README.md)

## 11. Related documents

- [Root README](../README.md): project overview and quick start
- [docs/README.md](../docs/README.md): diagrams, design, and round-report index
- [docs/CHANGELOG.md](../docs/CHANGELOG.md): commit-oriented change log
- [docs/verification.md](../docs/verification.md): latest verification status and known issues
- [reports/README.md](../reports/README.md): local acceptance-report directory
