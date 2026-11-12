# Research Assistant Agent

**A multi-agent research execution system for paper reproduction, data evaluation, and budget-constrained automated research**

[简体中文](README.md) | English

> Turn a natural-language research goal into a verifiable execution plan: understand the task → retrieve sources → freeze the evaluation contract → build a DAG → execute in isolation → deliver auditable results through artifacts, metrics, environment manifests, and evidence graphs.

Research Assistant Agent is version **0.9.0**. It ships in three forms — a **Windows desktop client**, a **Docker Compose server**, and a **source-development setup** — covering everything from a single researcher's laptop to a shared lab server.

---

## Table of contents

- [0. Positioning](#0-positioning)
- [1. What changed in this upgrade](#1-what-changed-in-this-upgrade)
- [2. Core capabilities](#2-core-capabilities)
- [3. System architecture](#3-system-architecture)
- [4. Agent matrix](#4-agent-matrix)
- [5. Sandbox and runtime](#5-sandbox-and-runtime)
- [6. Memory system](#6-memory-system)
- [7. Tool ecosystem and MCP](#7-tool-ecosystem-and-mcp)
- [8. Desktop client](#8-desktop-client)
- [9. Quick start](#9-quick-start)
- [10. Configuration](#10-configuration)
- [11. API summary](#11-api-summary)
- [12. Repository layout](#12-repository-layout)
- [13. Testing and quality gates](#13-testing-and-quality-gates)
- [14. Operating boundaries and security](#14-operating-boundaries-and-security)
- [15. Documentation index](#15-documentation-index)

---

## 0. Positioning

Research work differs from ordinary Q&A in one crucial way: **the answer must be executable, reproducible, and verifiable**. A research goal usually spans a paper, a code repository, a dataset, a runtime environment, evaluation metrics, and a budget. Missing any one of them turns "it looks done" into an untrustworthy result.

Research Assistant Agent decomposes the task into an auditable chain:

1. **Understand** — intent recognition plus a typed `ResearchContext` (attachments, paper metadata, document references, literature library, constraints, memory).
2. **Plan** — a unified PlanBuilder produces a DAG; `InputBindings` bind context and artifacts; the DAG Validator checks dependencies, artifact producers, and node contracts.
3. **Review** — immutable revisions, a plan review gate, and structured questions let a human inspect, edit, approve, or reject important plans before execution.
4. **Execute** — the Scheduler dispatches nodes with leases, retries, cancellation, budgets, and runtime approvals; specialized agents run code and experiments inside isolated sandboxes.
5. **Accept** — artifacts, `EnvironmentManifest`, `PlanOutcome`, and claim-level evidence graphs distinguish `completed`, `partial`, `smoke`, `blocked`, `failed`, and `needs_review`.

The system does not treat "all nodes finished" as success, and it never lets mock results masquerade as real experiments.

### 0.1 Three ways to run it

| Form | Entry point | Best for | Requirements |
|---|---|---|---|
| Windows desktop client | `ResearchAssistant.exe` (NSIS installer) | Individual research, single-machine use, zero-Docker start | Windows 10/11 + WebView2 |
| Docker Compose server | `http://localhost:5173` | Shared team deployment, Linux servers, full container isolation | Docker Engine 20.10+ / Compose v2 |
| Source development | `make run-sandbox` / `make run-backend` / `make run-frontend` | Extending agents, debugging, local development | Go 1.27.3, Node.js 22+, Docker (optional) |

### 0.2 Design principles

- **Real execution** — code, commands, dependency installation, and evaluation all run in a sandbox; the model never self-reports final metrics.
- **Honest degradation** — insufficient capabilities return `blocked` with `missing_requirements`, never a silent downgrade to mock or smoke.
- **Evidence first** — every paper claim must bind to a locatable artifact, page, section, or run record.
- **Tight by default** — offline sandbox networking, image allowlists, resource limits, leases, and approvals; important operations require confirmation by default.
- **Reversible** — plan revisions, memory switches, prompt versions, model profiles, and tool grants all have explicit rollback paths.

---

## 1. What changed in this upgrade

The 2026-10 → 2026-11 upgrade moved the system from a single-machine web prototype to a deliverable research execution platform.

| Area | Upgrade |
|---|---|
| Desktop delivery | Replaced Wails with **MyGo 0.7.4** as the default desktop shell; added an NSIS MUI2 installer, tray, single-instance handling, crash dumps, redacted diagnostics, and Ed25519-signed auto-updates; the installer bundles `backend.exe`, `sandbox.exe`, and frontend assets |
| Sandbox execution | Expanded from Docker-only to **docker / native / vm / remote** providers; added uv-managed CPython, per-plan venvs, Windows AppContainer/lowil runners, WSL2 path translation, and an OpenSandbox adapter; the UI shows the real capability matrix |
| Agent memory | Landed **M0–M3 memory**: JSONL + snapshots, BM25 + Chinese bigram, RRF/MMR, core/retrieved budgets, multi-turn chat, plan/task/document/AutoResearch extraction, consolidation, MemoryPanel, and a full API |
| Plan governance | Plan library (projects/search/pagination/archive/duplicate), immutable revisions, review gate, structured questions, approval inbox, and a cost/budget console |
| Tool ecosystem | N-13: ToolRegistry, ToolGrant, native/HTTP/MCP stdio/MCP HTTP transports, tool_call nodes and approvals, ToolCallRecord audit/replay, AgentProfile/PromptVersion evaluation and rollback |
| Literature and documents | N-04/N-07: multi-provider literature search and library, citation graph, PDF OCR/layout/tables/figures/quality reports, BM25 + Chinese bigram retrieval, and full-text translation |
| Workbench UI | A research-instrument workbench that unifies chat, DAG, execution sidebar, PDF reader, literature library, plan library, review, cost, memory, and settings into one information hierarchy |

> The [Paper Wiki](docs/design/2026-11-02-paper-wiki-memory.md) and [N-14 literature MCP/skill ecosystem](docs/design/n14-literature-mcp-skill-ecosystem.md) documents are design drafts, not implemented features. They are listed as roadmap only.

---

## 2. Core capabilities

### 2.1 Research tasks and planning

| Capability | Description |
|---|---|
| Multi-type research tasks | Paper reproduction, code execution, framework comparison, data evaluation, budget-constrained AutoResearch, general research Q&A |
| Intent recognition | `rule` / `llm` / `hybrid` / `shadow` modes; rule fast path + optional Python intent service + post-validation |
| Complexity routing | L0–L4: L0/L1 use a fast path, L2 uses standard templates, L3/L4 use skeleton + agent planner with one structured repair pass |
| Unified DAG planning | Template registry, simple/standard/full variants, General direct/research/paper/literature shapes, node contracts, and artifact validation |
| Typed context | `ResearchContext` + `InputBindings` unify attachments, paper metadata, documents, literature, constraints, and memory instead of hand-assembled template inputs |
| Plan library | Project grouping, title/intent search, status/type filters, keyset pagination, rename, archive/restore, duplicate, export, and delete |
| Immutable revisions | Every edit creates an r1/r2… snapshot; `base_revision` optimistic locking; running plans reject edits or execution of a new revision |

### 2.2 Execution and acceptance

| Capability | Description |
|---|---|
| Specialized agent routing | Route by `assigned_to` to Chat, Librarian, Benchmark, Coder, Research Coding, Data, AutoResearch, Experiment Research, Ablation ToT, Paper Debug, Visual Verifier, and Answer |
| Real isolated execution | Independent Go sandbox service with Docker / Native / VM / Remote providers; persistent workspaces, streaming logs, and artifact return |
| Runtime approvals | `confirm_important` (default) pauses before image pulls, dependency installs, data/model downloads, GPU use, or network access; `full_access` auto-executes within boundaries and records audit events |
| Paper-reproduction runtime | RuntimeSpec, image/dependency probing, real installation, `EnvironmentManifest` (image digest, Python/pip freeze, GPU, data hashes, commands, working directory) |
| Repository-first reproduction | Discover or use a specified Git repository, pin the revision, generate RuntimeSpec, install dependencies, and run controlled minimal/full experiments |
| Budget-constrained AutoResearch | Freeze ResearchSpec and metrics, generate code patches or parameter candidates within a restricted file scope, execute for real, then Keep/Reject, roll back regressions, and validate on a hidden holdout |
| Trustworthy Benchmark | Dataset audit, reproducible splits, leakage checks, Metric/Reward contracts, and backend recomputation on hidden labels |
| Layered candidate search | Bounded exhaustive search, route UCB, Top-K Beam, UCT-style parameter trees, virtual visits, and hidden holdout validation |
| Claim-level evidence | Freeze a hierarchical rubric and bind paper claims, criteria, and real artifacts into an evidence graph with coverage |
| Semantic outcomes | `PlanOutcome` distinguishes completed / partial / smoke / mock / blocked / failed / needs_review and lists missing requirements |
| Waiting and block semantics | Approvals/questions stay `awaiting_*` without blocking dependents; `block_info` keeps the root cause, the forwarding node and the epoch, so the reason survives a refresh |
| Partial acceptance | A plan can declare an `acceptance` contract (core goals + required artifacts) and `optional` work; when only optional work is lost the run ends `completed/partial` with the losses listed |
| Visual verification | Image structure gate + multimodal visual verdict (pass / fail / inconclusive); visual failure never fabricates completed |
| Reproducible export | `manifest.json` + `checksums.sha256` + `environment/*` + `provenance/*`; missing key evidence marks the bundle partial and lists what is missing |

### 2.3 Memory, tools, and governance

| Capability | Description |
|---|---|
| Agent memory | Layered memory (profile/fact/episode/procedure/constraint/negative/document/working/reflection), BM25 + entity index, RRF/MMR, core/retrieved budgets, citations and trust levels, owner isolation, redaction, and injection scanning |
| Chat memory | Server-side Session + ChatSession + turn summarization + citations; survives restarts |
| Plan/task memory | Plan episodes, task failure/retry lessons, document memory, AutoResearch trial memory, and consolidation |
| Tool registry | native / HTTP / MCP stdio / MCP HTTP; JSON Schema validation, connection tests, and schema fingerprints |
| Tool permissions | `AllowedTools` + AgentProfile `ToolAllowlist` + `ToolGrant`; unauthorized calls return `tool_forbidden` with no side effects |
| Tool approval and audit | `SideEffect` or approval policy creates a `tool_call` RuntimeAction; `ToolCallRecord` stores argument hash, result hash, duration, and approval ID; dry-run and real replay are supported |
| AgentProfile / PromptVersion | Immutable prompts, active-pointer rollback, template rendering, evaluation, and profile runner; unknown profiles fail plan validation |
| Cost and budget | token / USD / sandbox / download limits; soft overage pauses, hard overage cancels; 80% warnings, 100% rejection; pricing follows the model profile |
| Observability | Prometheus metrics, structured audit events, SSE plan/node/log/artifact events, diagnostics bundles, and log rotation |

---

## 3. System architecture

### 3.1 Diagrams

| Diagram | draw.io source | SVG preview | Description |
|---|---|---|---|
| System architecture | [architecture.drawio](docs/diagrams/architecture.drawio) | [architecture.svg](docs/diagrams/architecture.svg) | Layered view of clients, desktop shell, React workbench, Go backend, agents, sandbox providers, memory/persistence, and external dependencies |
| Runtime flow | [runtime-flow.drawio](docs/diagrams/runtime-flow.drawio) | [runtime-flow.svg](docs/diagrams/runtime-flow.svg) | End-to-end sequence from research goal to plan, review, scheduling, sandbox execution, artifacts, memory, and SSE |
| Agent execution flow | [agent-execution-flow.drawio](docs/diagrams/agent-execution-flow.drawio) | [agent-execution-flow.svg](docs/diagrams/agent-execution-flow.svg) | RoutedTaskExecutor routing, memory injection, tool calls, sandbox execution, and the evidence/search sub-loops |

Archify interactive diagrams (self-contained HTML, open directly in a browser, with light/dark themes, zoom, and export):

- [System architecture](docs/diagrams/system-architecture.html)
- [Multi-agent execution workflow](docs/diagrams/multi-agent-workflow.html)

![System architecture](docs/diagrams/architecture.svg)

### 3.2 Layers

```text
Researcher / Paper / Repository / Dataset
                    │
        ┌───────────┴────────────┐
        │                        │
Windows Desktop (MyGo)      Web Browser / API Client
  ├─ MyGo window            └─ nginx BFF (static assets + /api proxy)
  ├─ bridge (mygo.Bind)
  ├─ supervisor (backend.exe / sandbox.exe)
  ├─ loopback proxy (SSE / uploads / downloads)
  ├─ settings + DPAPI
  ├─ updater + diagnostics
  └─ tray / single instance
        │
        └──────────► React Workbench (embedded or served)
                    │ REST / Upload / SSE
                    ▼
        Go Backend (API + Orchestration)
        ├─ API: auth / CORS / rate limit / OpenAPI
        ├─ Cognition: Intent Router → Complexity Router → Planner + DAG Validator
        ├─ Governance: Plan Store / Revision / Review gate / Budget / Audit
        ├─ Scheduling: Scheduler (lease / retry / cancel / budget / approval)
        ├─ Execution: RoutedTaskExecutor → specialized agents
        ├─ Memory: Memory Store + Retrieval + Injection
        ├─ Tools: ToolRegistry / ToolInvoker (native / HTTP / MCP)
        └─ Events: Event Bus → SSE
                    │
                    ▼
        Sandbox Client → docker-sandbox gateway → executor
                    ├─ Docker Provider (containers / images / GPU / network)
                    ├─ Native Provider (uv CPython + venv + AppContainer/lowil)
                    ├─ VM Provider (WSL2 / Docker in VM)
                    └─ Remote Provider (OpenSandbox)
                    │
                    ▼
        Artifact / EnvironmentManifest / PlanOutcome / Claim-Evidence
                    │
                    ▼
        Event Bus / SSE → React Workbench
```

| Layer | Main components | Responsibility |
|---|---|---|
| Client | Windows desktop shell, web frontend, API/CLI | Submit goals, upload material, inspect DAGs, approve, read results |
| Access | Gin API, auth, CORS, rate limiting, OpenAPI | Unified REST/SSE contract, identity, quotas |
| Cognition | Intent Router, Complexity Router, Planner, DAG Validator | Turn intent into a valid, executable PlanGraph |
| Governance | Plan Store, Revision Store, Review Gate, Budget, Audit | Plan persistence, versioning, review, cost, audit |
| Scheduling | Scheduler, lease, Runtime Gate, retry/cancel | Advance the DAG safely and recoverably |
| Execution | RoutedTaskExecutor, specialized agents, ToolInvoker | Select agents, inject memory, call tools and sandboxes |
| Sandbox | gateway, executor, docker/native/vm/remote providers | Real isolated execution and capability negotiation |
| State | Plan/Revision/Memory/Upload/Document/Literature/Budget/Audit | Durable, replayable state |
| External | LLM provider, Research Optimizer, literature sources | Models, candidate ranking, literature retrieval |

### 3.3 Lifecycle of one request

1. **Input and context** — the user submits a goal, uploads PDFs/CSVs, or links a repository; the backend resolves `AttachmentRef` / `DocumentRef` / `PaperMetadata`, writes `ResearchContext`, and retrieves relevant memory.
2. **Intent and complexity** — the Intent Router returns a type and confidence; the Complexity Router makes the final L0–L4 decision after context resolution and selects a template variant or agent planner.
3. **Planning** — PlanBuilder emits nodes, dependencies, `assigned_to`, `InputBindings`, and artifact contracts; the DAG Validator checks acyclicity, dependencies, unique producers, and required nodes.
4. **Revision and review** — the plan is saved and a revision is created; if the review gate applies (`PLAN_REVIEW_MODE=always`, cost threshold, high-risk nodes, missing requirements, L4), the plan enters `awaiting_review` for edit/validate/diff/approve/reject.
5. **Execution** — the Scheduler computes in-degrees, dispatches ready nodes, and acquires leases; the runtime gate creates `RuntimeActionRequest` records before image/dependency/GPU/network operations.
6. **Agent execution** — RoutedTaskExecutor resolves the AgentProfile (PromptVersion + ModelProfile + ToolAllowlist + MaxSteps), injects core/retrieved memory, and invokes the agent; the agent uses ToolInvoker for tools or the Sandbox Client for code execution.
7. **Sandbox execution** — the gateway authenticates and proxies; the executor selects a provider; the provider executes according to its `Capabilities` and returns stdout/stderr/usage/artifacts; missing capabilities return a structured `blocked`.
8. **Acceptance** — artifacts are validated (non-empty, typed, producer-bound); PlanFinalizer combines node states, EnvironmentManifest, TrialLedger, and Claim-Evidence into `PlanOutcome`; memory records plan/task/trial lessons.
9. **Feedback** — the event bus persists and streams over SSE; the UI updates the DAG, execution sidebar, evidence graph, and result panel; the plan can be exported as a reproducible bundle.

---

## 4. Agent matrix

| Agent | Responsibility | Typical task types |
|---|---|---|
| **Chat / General** | General conversation, scope clarification, non-paper requests, lightweight research Q&A | `general_process`, `general_synthesis` |
| **Librarian** | Paper parsing, `paper_metadata`, DocumentStore retrieval, literature search, claim rubric | `paper_parse`, `literature_search`, `claim_rubric_extract` |
| **Benchmark** | Dataset audit, leakage-safe splits, Metric/Reward contracts, hidden-label recomputation | `dataset_audit`, `split_materialize`, `contract_freeze`, `benchmark_execute` |
| **Coder** | Code generation, plotting, runtime prepare/install/validate, dependency resolution, bounded repair | `generate_code`, `render_plot`, `runtime_prepare`, `install_dependencies` |
| **Research Coding** | Repository preparation, benchmark adapters, paper-code execution/debugging, AutoResearch | `repo_prepare`, `paper_code_execute`, `autoresearch_run` |
| **Data** | Metric aggregation, paper-claim comparison, evidence-graph construction, reports and charts, visual verification orchestration | `result_analysis`, `paper_compare`, `claim_evidence_build` |
| **AutoResearch** | ResearchSpec freeze, candidate execution, Keep/Reject, rollback, holdout validation | `autoresearch_spec`, `autoresearch_search`, `autoresearch_validate` |
| **Experiment Research** | `experiment.spec/v1` configuration search and domain adapters | `experiment_design`, `experiment_search` |
| **Ablation ToT** | Bounded Tree-of-Thoughts ablation design | `ablation_design` |
| **Paper Debug** | Paper-repository debugging state machine and bounded patch validation | `paper_debug` |
| **Visual Verifier** | Image structure gate and multimodal verdict (pass/fail/inconclusive) | `verify_result` |
| **Answer** | Final user-facing answer synthesis and citation organization | `final_answer` |

Agents do not share mutable memory directly; all cross-agent data flows through artifacts, InputBindings, events, and memory references.

---

## 5. Sandbox and runtime

### 5.1 Provider matrix

The sandbox service abstracts execution behind a single `Provider` interface selected by `SANDBOX_PROVIDER`; `auto` prefers native when Docker is unavailable.

| Provider | Isolation | Image/environment | Network control | GPU | Best for |
|---|---|---|---|---|---|
| `docker` | Container | OCI image + digest | OS-level (offline bridge by default) | NVIDIA runtime passthrough | Servers, existing Docker, image-level reproduction |
| `native` | Process (AppContainer / lowil / Job Object) | uv-managed CPython + per-plan venv | AppContainer dual profiles (offline/online) | Host GPU directly | Windows desktop, zero-Docker start, pure-Python tasks |
| `vm` | Virtual machine (WSL2, etc.) | Bundled Docker Engine / OCI | VM network | GPU inside VM (config-dependent) | No Docker Desktop but full OCI semantics required |
| `remote` | Remote sandbox service | Provided by the service | Provided by the service | Provided by the service | GPU, large memory, long-running jobs |

Providers report their real capability matrix through `GET /api/v1/capabilities` (images, network, GPU, Python versions, probe results). When a task needs a capability that is unavailable, the system returns `blocked` with `missing_requirements=[sandbox_capability]` and actionable guidance instead of silently degrading.

### 5.2 Runtime chain

Paper-reproduction main chain:

```text
reproduction_spec -> runtime_probe -> image_prepare -> prepare_runtime
-> install_dependencies -> runtime_validate -> paper_code_execute
```

- `runtime_validate` emits `EnvironmentManifest`: image digest (or native runtime hash), Python/pip freeze, GPU, data hashes, run command, and working directory.
- Insufficient resources never turn smoke into success; `outcome` explicitly marks `smoke`, `partial`, or `blocked`.
- Runtime permission only decides whether important operations auto-run; it never bypasses image allowlists, resource limits, sandbox isolation, or plan ownership checks.

### 5.3 Security boundaries

- In Compose, the gateway mounts neither the Docker socket nor the host workspace; only the internal, unpublished executor holds the socket.
- Containers default to `network=none` (internal offline network); enabling network requires explicit approval and is reverted immediately after the command.
- CPU, memory, PID, capabilities, image allowlists, mount paths, and TTL are limited by default; `strict` enables non-root, read-only rootfs, and tmpfs.
- The native provider uses AppContainer / lowil plus Job Object limits; isolation is weaker than containers, so keep `confirm_important` and never use `full_access` on untrusted code.
- Desktop API keys are encrypted with Windows DPAPI and passed to child processes through the environment; they never touch disk or the command line.

---

## 6. Memory system

The M0–M3 agent memory system preserves reusable experience across chat, plans, tasks, and experiments without breaking prompt caching or security boundaries.

### 6.1 Memory types

| Type | Content |
|---|---|
| `profile` | User preferences, research domain, output style |
| `fact` | Paper metadata, metrics, environment facts |
| `episode` | The course and conclusion of a plan/task |
| `procedure` | Concrete steps that fixed a failure |
| `constraint` | Provider, image, Python, GPU, and network constraints |
| `negative` | Reproduction failures, rejected candidates, dead ends |
| `document` | Document page counts, sections, parse status, references |
| `working` | Short-term working memory for the current session |
| `reflection` | Reflections induced from multiple episodes/negatives (pending_review by default) |

### 6.2 Retrieval and injection

- **Index** — mixed Chinese/English tokenizer (CJK bigram), BM25, and entity/kind/status inverted indexes.
- **Recall** — BM25 + entity + recency recall, RRF fusion, trust/importance/recency/confidence/scope weighting, and MMR de-duplication.
- **Budget** — a stable core-memory prefix plus a dynamic retrieved-memory block, trimmed by token budget so task inputs are not crowded out.
- **Safety** — hard owner isolation; rejects secrets/private keys/large base64; rejects hidden benchmarks; memory is data, not instructions, and is injection-scanned before injection.

### 6.3 Switches and rollback

| Variable | Default | Description |
|---|---|---|
| `MEMORY_ENABLED` | `true` | `false` disables initialization, injection, and writes entirely |
| `MEMORY_MODE` | `on` | `shadow` retrieves without injecting, useful for A/B |
| `MEMORY_DIR` | `<PLAN_STORE_PATH dir>/memory` | Separate memory directory for isolated experiments |
| `MEMORY_CONSOLIDATE_INTERVAL_SECONDS` | empty (off) | Background consolidation interval; `0` disables |

The frontend "Settings → Memory" tab provides list, search, create, pin, delete, settings, and export/import; plan details show which memory references were used.

---

## 7. Tool ecosystem and MCP

N-13 turns "tools" from free text in a prompt into governed capabilities with schemas, permissions, and audit trails.

- **Registry** — `GET/POST /api/tools`, `GET/PUT/DELETE /api/tools/:id`, `POST /api/tools/:id/test`, `GET /api/tools/:id/schema`; supports native / HTTP / MCP stdio / MCP HTTP.
- **MCP onboarding** — registration runs `initialize` + `tools/list` and stores the schema fingerprint; MCP HTTP rejects private networks by default and requires an explicit allowlist.
- **Permission boundary** — execution enforces node `AllowedTools`, AgentProfile `ToolAllowlist`, and `ToolGrant`; violations return `tool_forbidden` with no side effects.
- **Approval** — tools with `SideEffect=true` or `Approval=always/write` create a `tool_call` RuntimeAction before dispatch; rejection prevents the call, approval executes it with an `ApprovalID`.
- **Audit/replay** — every call is recorded in `PlanGraph.ToolCalls` (argument hash, result hash, duration, approval ID, error code); `GET /api/plans/:id/tool-calls` and `POST .../tool-calls/:callId/replay` support dry-run hash comparison and approval-gated real replay.
- **AgentProfile/PromptVersion** — `AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps`; prompts are immutable and the active pointer can roll back; settings has five tabs: models, tools, agents, runtime, memory.

---

## 8. Desktop client

The Windows desktop client is the flagship 0.9.0 delivery form, located in `desktop/` (a standalone Go module).

### 8.1 Packages

| Package | Responsibility |
|---|---|
| `desktop/paths` | Per-user install/data/log directories |
| `desktop/settings` | `settings.json`, environment injection, DPAPI secret store |
| `desktop/supervisor` | Single-instance lock, OS-assigned ports, child-process startup and health checks, Windows Job Object cleanup |
| `desktop/proxy` | Fixed-loopback-origin reverse proxy with SSE `FlushInterval=-1`, Host/Origin guards, and SPA fallback |
| `desktop/shell` | Orchestrates settings → ports → tokens → supervisor → proxy; crash restart; `/desktop/state`, `/desktop/diagnostics`, `/desktop/retry`, and the error page |
| `desktop/diagnostics` | Redacted support bundle (log tail, settings, health, system info, failed plans) |
| `desktop/updater` | `latest.json` check, semver comparison, SHA-256 + Ed25519 verification, staged download |
| `desktop/onboarding` | First-run state, credential validation, and a minimal completion test |
| `desktop/bridge` | Desktop API bound with `mygo.Bind`; the Wails fallback still exposes `window.go.main.API` |
| `desktop/mygo` | MyGo adapter (tray, dialogs, notifications, shell); business code depends only on this layer |
| `desktop/tray` | Tray/notification contract (MyGo driver, systray fallback, memory driver for tests) |

### 8.2 Runtime layout

```text
%LOCALAPPDATA%\Programs\ResearchAssistant\   ResearchAssistant.exe backend.exe sandbox.exe uv.exe
%LOCALAPPDATA%\ResearchAssistant\            settings.json secrets.dat lock data\ runtimes\ workspaces\ logs\
```

### 8.3 Build

```powershell
# One command: frontend + backend.exe + sandbox.exe + MyGo exe + NSIS installer
powershell -ExecutionPolicy Bypass -File desktop/build/build.ps1 -Version 0.9.0

# Pure-logic tests only
cd desktop; go test ./...

# Windows cross-build (default MyGo entry point)
GOOS=windows GOARCH=amd64 go build ./...
```

The default entry point is `desktop/main_mygo.go` (build tag `!wails && !headless`); the Wails fallback uses `-tags "desktop wails"`; the headless stub uses `-tags headless`. Builds land in `desktop/dist/ResearchAssistant-<version>-Setup.exe`.

### 8.4 Updates and diagnostics

- `RA_UPDATE_ENDPOINT` + `RA_UPDATE_PUBLIC_KEY` enable signed updates; installers and manifests use SHA-256 + Ed25519.
- Crash dumps are registered through WER LocalDumps under `%LOCALAPPDATA%\ResearchAssistant\crashes`.
- "Export diagnostics" produces a redacted zip with logs, settings (secrets redacted), health checks, and failed-plan summaries.

---

## 9. Quick start

### 9.1 Prerequisites

- **Desktop client** — Windows 10/11 + WebView2 Runtime (bundled with Windows 11).
- **Docker Compose** — Docker Engine 20.10+, Docker Compose v2, Git.
- **Source development** — Go 1.27.3, Node.js 22+, Python 3.12 (optional AI services/optimizer), Docker (sandbox).
- An LLM API key compatible with the Chat Completions protocol (provided by the deployment).

### 9.2 Option A: Windows desktop client

1. Run `desktop/dist/ResearchAssistant-0.9.0-Setup.exe` and choose the install location and optional PDF association.
2. Complete onboarding on first launch: Base URL, model name, and API key (stored in DPAPI).
3. In "Settings → Runtime", choose a Sandbox Provider: `auto` is recommended; native is used when Docker is absent.
4. Start a conversation, enter a research goal or upload PDF/CSV, select the runtime permission, and send.

> Run the installer from a normal user folder such as Desktop or Downloads. Some ACL-restricted workspace folders fail the NSIS temporary-file check.

### 9.3 Option B: Docker Compose

```bash
cd research-assistant
cp .env.example .env
# Edit .env: set random SANDBOX_API_TOKEN, RESEARCH_OPTIMIZER_API_TOKEN, and API_AUTH_TOKEN
cp backend.env.example backend.env
# Edit backend.env: at minimum set OPENAI_API_KEY
docker compose up --build -d
```

Then visit (ports can be overridden via `*_HOST_PORT` in `.env`):

| Service | Address |
|---|---|
| Web UI | http://localhost:5173 |
| Backend API | http://localhost:8080 |
| Health check | http://localhost:8080/api/health (and `/ping`; no Bearer required) |
| Sandbox API | http://localhost:8082 |
| Research Optimizer | http://localhost:8090 |

```bash
curl -s http://localhost:8080/api/health
# Expect backend.ok=true, repository.ok=true, sandbox.ok=true, research_optimizer.ok=true
docker compose logs -f
docker compose down
```

### 9.4 Option C: Local development

```bash
cd research-assistant
make install
export SANDBOX_API_TOKEN=<random-token>
make run-sandbox     # Terminal 1: local engine-mode sandbox
make run-backend     # Terminal 2: needs SANDBOX_URL and API_AUTH_TOKEN
make run-frontend    # Terminal 3: Vite dev server
```

Windows developers should prefer the desktop client or Docker Compose. The backend builds on Windows through the `procgroup` / `sysinfo` abstractions, and the desktop runs it as `backend.exe`; however, some POSIX-dependent tests/features (symlinks, file permissions, external PDF OCR tools) are limited on Windows, and Linux containers remain the recommended server runtime.

### 9.5 Typical tasks

#### Paper reproduction

Enter this in the web UI or desktop client:

```text
Reproduce "Attention Is All You Need" using
https://github.com/harvardnlp/annotated-transformer in smoke mode,
run a lightweight attention ablation, and do not run full WMT14 training.
```

Choose "Confirm important operations" or "Full access" before sending. The system builds and executes the chain: parse paper → freeze claim rubric → discover repository → prepare workspace → resolve dependencies → generate RuntimeSpec → probe sandbox capabilities → prepare image → prepare runtime → install dependencies → validate runtime / write EnvironmentManifest → run experiment → compare with paper claims → claim-evidence graph.

#### Custom data evaluation

Upload CSV/TSV/JSON/JSONL and enter:

```text
Run a benchmark using https://github.com/OWNER/REPOSITORY.
The input column is review, the label column is label, and use at most 500 samples.
```

The Benchmark Agent audits column mapping, creates leakage-safe train/validation/test splits, freezes the Metric/Reward contract, and recomputes final metrics on hidden labels the model never sees.

#### Budget-constrained AutoResearch

```text
Run AutoResearch on https://github.com/OWNER/REPOSITORY using autoresearch.json,
at most 3 rounds, a 15-minute wall-clock budget, and 3 final validation runs.
```

The system freezes ResearchSpec and the repository revision, generates candidates within the allowed file scope, and performs Keep/Reject, regression rollback, and hidden-holdout validation against real metrics.

---

## 10. Configuration

See [backend.env.example](research-assistant/backend.env.example), [docker-compose.yml](research-assistant/docker-compose.yml), and [backend/README.md](research-assistant/backend/README.md) for the full list. Common variables by area:

### 10.1 LLM and models

| Variable | Required | Description |
|---|---:|---|
| `OPENAI_API_KEY` | Yes | Compatible-service API key |
| `OPENAI_BASE_URL` | No | API base URL; uses the deployment default when unset |
| `OPENAI_MODEL_NAME` | No | Model name; uses the deployment default when unset |
| `MODEL_PROFILE_DIR` / `MODEL_PROFILE_ENCRYPTION_KEY` | No | Runtime model-profile storage and encryption; saving profiles is disabled without a key |
| `ADMIN_TOKEN` | No | Operator token for global model profiles |
| `LLM_MAX_ATTEMPTS` / `LLM_ATTEMPT_TIMEOUT_SECONDS` / `LLM_RETRY_BASE_MS` / `LLM_RETRY_MAX_MS` | No | LLM retry and timeout policy |

### 10.2 Intent and planning

| Variable | Description |
|---|---|
| `INTENT_SERVICE_URL` | Optional intent-service URL; empty means rule-only routing |
| `INTENT_ROUTER_MODE` | `rule` / `llm` / `hybrid` / `shadow`; Compose defaults to `hybrid` |
| `INTENT_LLM_CONFIDENCE_THRESHOLD` | Minimum model confidence before falling back to rules (default 0.65) |
| `INTENT_CLIENT_TIMEOUT_SECONDS` | Per-call timeout for the intent service |
| `GENERAL_PLAN_FAST_PATH` | Default `true`: General plans use direct/research/paper/literature shapes |
| `COMPLEXITY_ROUTER_ENABLED` | Default `true`: enable L0–L4 complexity routing |
| `PLANNER_MAX_NODES_L3` / `PLANNER_MAX_NODES_L4` | Node caps for the agent planner at L3/L4 (default 16 / 24) |

### 10.3 Sandbox and runtime

| Variable | Description |
|---|---|
| `SANDBOX_URL` / `SANDBOX_API_TOKEN` | Sandbox address and Bearer token; gateway and executor fail closed |
| `SANDBOX_PROVIDER` | `auto` / `docker` / `native` / `vm` / `remote` |
| `SANDBOX_DEFAULT_IMAGE` / `SANDBOX_IMAGE_ALLOWLIST` | Default runtime image and allowed image prefixes |
| `SANDBOX_NETWORK_MODE` / `SANDBOX_ALLOW_NETWORK` | Default network mode (`none`) and whether approved networking is allowed |
| `SANDBOX_SECURITY_PROFILE` | `standard` / `strict` (non-root + read-only rootfs + tmpfs) |
| `SANDBOX_WINDOWS_RUNNER` | `auto` / `appcontainer` / `lowil` / `raw` (native provider) |
| `SANDBOX_DEFAULT_PYTHON` | Default Python version for the native provider |
| `SANDBOX_WORKSPACE_ROOTS` | Allowed workspace roots for mounts |
| `SANDBOX_TTL_MINUTES` / `SANDBOX_REAPER_INTERVAL_SECONDS` | Sandbox TTL and reaper interval |
| `OPEN_SANDBOX_URL` / `ENABLE_OPENSANDBOX_FALLBACK` | OpenSandbox URL and fallback switch |

### 10.4 Storage and memory

| Variable | Description |
|---|---|
| `PLAN_STORE_PATH` | Base path for plan snapshots (`<path>.d`) and the JSONL index/event log |
| `REVISION_STORE_PATH` | Immutable revision storage; defaults to `<PLAN_STORE_PATH>.revisions.json` |
| `UPLOAD_ROOT` / `UPLOAD_MAX_MB` | Upload directory and per-file size limit |
| `UPLOAD_USER_QUOTA_MB` / `UPLOAD_MAX_FILES` / `UPLOAD_TTL_DAYS` | Per-user quota, file count, and retention |
| `BLOB_STORE_PATH` | Content-addressed artifact store |
| `MEMORY_ENABLED` / `MEMORY_MODE` / `MEMORY_DIR` | Memory switch, mode, and directory |
| `MEMORY_CONSOLIDATE_INTERVAL_SECONDS` | Background consolidation interval; empty disables |
| `DOCUMENT_CACHE_DIR` / `LITERATURE_CACHE_DIR` / `LITERATURE_LIBRARY_DIR` | Document cache, literature cache, and library |
| `PROJECT_STORE_PATH` / `BUDGET_STORE_PATH` | Plan projects and cost ledger; created beside `PLAN_STORE_PATH` by default |

### 10.5 Approval, budget, and security

| Variable | Description |
|---|---|
| `API_AUTH_TOKEN` / `REQUIRE_API_AUTH` | Static API Bearer; Compose forces `REQUIRE_API_AUTH=true` and fails closed on an empty token |
| `CORS_ALLOWED_ORIGINS` | Allowed frontend origins |
| `APPROVAL_TIMEOUT_SECONDS` | Runtime approval window |
| `REQUIRE_PLAN_APPROVAL` / `PLAN_REVIEW_MODE` / `REVIEW_COST_THRESHOLD_USD` | Plan approval and review gate |
| `QUESTION_TIMEOUT_SECONDS` | Default structured-question timeout (900 s) |
| `PLAN_MAX_COST_USD` / `PLAN_MAX_TOKENS` / `PLAN_MAX_SANDBOX_SECONDS` / `PLAN_MAX_DOWNLOAD_BYTES` / `PLAN_BUDGET_POLICY` | Plan budgets; soft / hard (default soft) |
| `PLAN_MAX_TASK_ATTEMPTS` / `PLAN_MAX_DURATION_SECONDS` | Task attempt and duration budgets |
| `PLAN_RATE_LIMIT_PER_MINUTE` / `PLAN_RATE_LIMIT_BURST` / `PLAN_MAX_ACTIVE_PER_USER` | Plan creation rate limits and per-user active-plan cap |
| `EXPORT_MAX_FILE_MB` / `EXPORT_MAX_TOTAL_MB` | Reproducible-export per-file and total limits (16 / 128 MB) |

### 10.6 Literature and documents

| Variable | Description |
|---|---|
| `LITERATURE_PROVIDER_TIMEOUT_SECONDS` / `LITERATURE_MAX_RETRIES` | Literature provider timeout and retries |
| `WEB_FETCH_MAX_MB` / `WEB_FETCH_TIMEOUT_SECONDS` / `WEB_FETCH_RESPECT_ROBOTS` | Web-content extraction limits |
| `PDF_PARSE_MAX_MB` / `PDF_PARSE_TIMEOUT_SECONDS` | PDF parsing budgets |
| `PDF_OCR_MAX_PAGES` / `PDF_OCR_LANG` / `PDF_OCR_TIMEOUT_SECONDS` / `PDF_OCR_MIN_TEXT_CHARS` | OCR budget, language, and text threshold |
| `VISION_MAX_EDGE_PX` / `VISION_MAX_IMAGES` / `VISION_TIMEOUT_SECONDS` / `FEATURE_VISION_VERIFICATION` | Visual-verification budget and switch |

> `VITE_API_AUTH_TOKEN` is only for local Vite development. Production Compose builds do not embed the token; nginx injects `Authorization` at request time.

---

## 11. API summary

Backend API defaults to `:8080`. Except for `GET /ping`, `GET /api/health`, and `GET /openapi.json`, all `/api` routes pass through `APIAuthMiddleware`; `GET /api/plans/:id/stream` can use a 60-second HMAC ticket instead of a Bearer token. See [OpenAPI](research-assistant/backend/internal/api/openapi.json) and [backend/README.md](research-assistant/backend/README.md) for the full contract.

### 11.1 Health, identity, and metadata

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/ping` / `/api/health` | Liveness / backend, repository, sandbox, optimizer, and intent-service health |
| `GET` | `/api/me` | Server-derived user_id / session_id |
| `GET` | `/openapi.json` | OpenAPI 3.1 contract (no Bearer at the root path) |
| `GET` | `/api/sandbox/capabilities` | Current sandbox provider capability matrix |
| `GET` | `/api/metrics` (and `/metrics`) | Prometheus text metrics (Bearer protected) |
| `GET` | `/api/audit/export` | Audit export (caller-isolated) |

### 11.2 Plans, revisions, and review

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/plan` | Create a plan (intent, attachments, runtime permission, complexity) |
| `GET` | `/api/plans` / `/api/plans/:id` | List/query plans, nodes, artifacts, and outcomes |
| `POST` | `/api/plans/:id/execute` / `pause` / `resume` / `cancel` | Execute/pause/resume/cancel |
| `POST` | `/api/plans/:id/approve` | Approve a plan that requires human confirmation |
| `POST` | `/api/plans/:id/duplicate` / `PATCH` / `DELETE` | Duplicate/rename/delete |
| `GET` / `POST` | `/api/plans/:id/revisions` | List/create immutable revisions |
| `GET` | `/api/plans/:id/revisions/:revision` / `diff` | Read a revision / diff against the current one |
| `POST` | `.../revisions/:revision/validate` / `approve` / `reject` / `execute` | Validate/approve/reject/execute a revision |
| `POST` | `/api/plans/:id/questions` / `.../answer` | Structured questions and answers |
| `GET` | `/api/approvals` / `POST /api/approvals/bulk` | Approval inbox / low-risk bulk decisions |
| `POST` | `/api/plans/:id/tasks/:taskId/retry` / `reassign` / `run` / `execute` | Retry/reassign/rerun-from-node/execute-one-node |
| `GET` | `/api/plans/:id/export?format=md` / `json` / `zip` | Export a minimal reproduction bundle |
| `POST` | `/api/plans/:id/reproduce/verify` | Statically verify bundle completeness |

### 11.3 Events, runtime, and tools

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/plans/:id/events` | Event history |
| `GET` | `/api/plans/:id/stream-ticket` | 60-second HMAC SSE ticket |
| `GET` | `/api/plans/:id/stream` | SSE stream (supports `?ticket=`) |
| `GET` | `/api/plans/:id/runtime-actions` | Runtime approval requests |
| `POST` | `.../runtime-actions/:actionId/decision` | Approve/reject a runtime action |
| `GET` / `POST` | `/api/tools`, `/api/tools/:id`, `/api/tools/:id/test`, `/api/tools/:id/schema` | Tool registration, tests, schema |
| `GET` / `POST` / `DELETE` | `/api/tool-grants` | Tool grants |
| `GET` / `POST` | `/api/agent-profiles`, `/api/prompt-versions` | AgentProfile and PromptVersion |
| `POST` | `.../prompt-versions/:id/activate` / `eval` | Activate/evaluate a prompt |
| `GET` | `/api/plans/:id/tool-calls` / `POST .../replay` | Tool-call audit and replay |

### 11.4 Memory, sessions, and uploads

| Method | Endpoint | Description |
|---|---|---|
| `GET` / `POST` / `PATCH` / `DELETE` | `/api/memories` (plus `/:id`, `/search`, `/stats`, `/export`, `/import`, `/settings`, `/consolidate`, `/feedback`) | Memory CRUD, search, stats, export/import, settings, consolidation |
| `GET` / `POST` / `DELETE` | `/api/sessions` (plus `/:id/activate`) | Server-side sessions |
| `POST` | `/api/uploads` | Upload attachments (SHA-256 dedup, quota, optional ClamAV) |
| `GET` | `/api/uploads/:id` / `content` / `document` | Metadata / content / PDF text-layer summary |
| `GET` | `/api/uploads/:id/document/pages/:page` / `sections/:section` / `search` | Page/section/BM25 retrieval |
| `GET` | `/api/uploads/:id/document/quality` / `pages/:page/layout` / `locate` | Quality report / layout / quote location |
| `GET` | `/api/uploads/:id/document/tables/:tableId` / `figures/:figureId` | Table JSON/CSV / figure captions and images |
| `POST` | `/api/uploads/:id/assist` / `translation-jobs` | Selection translation/explanation / full-text translation jobs |
| `DELETE` | `/api/uploads/:id` | Delete uploads and derived documents |

### 11.5 Literature, models, and web

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/literature/search` | Multi-provider search (arXiv/OpenAlex/Crossref/GitHub/Semantic Scholar/PubMed/DBLP) |
| `POST` | `/api/literature/import` | Import and parse arXiv ID/URL, DOI, or PDF URL |
| `GET` / `POST` / `DELETE` | `/api/literature/records` (plus `/:id`, `/attach`, `/bibtex`) | Library, tags, BibTeX, attach to session |
| `GET` | `/api/literature/citations/:doi` | Citation/cited-by graph |
| `POST` | `/api/web/fetch` | Extract public web content (SSRF/robots/content-type/size/timeout/cache) |
| `POST` | `/api/chat` | Multi-turn chat (memory, citations, sessions) |
| `GET` / `POST` / `PUT` / `DELETE` | `/api/model-profiles` (plus `/:id/test`, `/probe-vision`), `/api/model-routing` | Runtime model profiles and routing |
| `GET` / `POST` | `/api/usage/summary` / `timeseries` / `/api/plans/:id/usage` / `/api/budgets` | Usage and budgets |

The sandbox service (default `:8082`) also exposes `/api/v1/health`, `/capabilities`, `/images/ensure`, `/sandboxes`, `/sandboxes/:id/python`, `/commands`, `/network`, `/usage`, and `/runtime/status|install|cleanup`. See [docker-sandbox/README.md](research-assistant/docker-sandbox/README.md).

---

## 12. Repository layout

```text
Research Assistant Agent/
├── README.md                     # Simplified Chinese
├── README.en.md                  # This file (English)
├── .github/workflows/ci.yml      # Go / frontend / Python / Docker / Compose CI
├── docs/                         # Diagrams, designs, ADRs, CHANGELOG, verification
│   ├── diagrams/                 # draw.io / SVG / Archify HTML
│   ├── design/                   # Design documents (including unimplemented drafts)
│   ├── audit/                    # Context-system audit
│   └── improvement-*/            # Per-round fix and test reports
├── desktop/                      # Windows desktop client (standalone Go module)
│   ├── mygo/                     # MyGo adapter
│   ├── shell/ supervisor/ proxy/ settings/ bridge/ tray/
│   ├── diagnostics/ updater/ onboarding/ crashdump/ paths/
│   └── build/                    # MyGo + NSIS packaging pipeline
├── docker-core/                  # Docker/gVisor sandbox kernel experiments
│   ├── checkpoint/               # Snapshots, rollback, negative feedback
│   ├── config/                   # TOML configuration
│   ├── executor/                 # DAG scheduling, node racing, resource gateway
│   ├── sandbox/                  # Persistent TTY sandbox
│   └── watchdog/                 # Semantic circuit breaker and timeouts
├── research-assistant/           # Main product implementation
│   ├── backend/                  # Go API, planner, scheduler, agents, memory, tools
│   ├── frontend/                 # React + TypeScript workbench
│   ├── docker-sandbox/           # Standalone Go sandbox service (gateway/engine/providers)
│   ├── research-optimizer/       # Python candidate policy and experience service
│   ├── ai-services/              # Optional Python AI services (intent recognition)
│   ├── examples/                 # Runnable examples and acceptance scripts
│   ├── test/                     # E2E, golden tests, and fixtures
│   ├── scripts/                  # Unix / Windows startup and maintenance scripts
│   ├── backend.env.example
│   ├── docker-compose.yml
│   └── Makefile
└── tool/                         # Zero-dependency Go Function Calling example
```

---

## 13. Testing and quality gates

### 13.1 Local commands

```bash
# Backend (Linux or container)
cd research-assistant/backend
go test ./...
go vet ./...

# Frontend
cd research-assistant/frontend
npm test
npm run lint
npm run build
npm run check:bundle

# Sandbox and docker-core
cd research-assistant/docker-sandbox && go test ./...
cd docker-core && go test ./...

# Desktop
cd desktop && go test ./...
GOOS=windows GOARCH=amd64 go build ./...

# Python
cd research-assistant/research-optimizer && python -m unittest discover -s tests -p 'test_*.py'
```

### 13.2 CI coverage

- **Go** — Linux/Windows matrix; gofmt, backend tests/race/security regressions/coverage gate, sandbox and docker-core tests, Windows cross-build.
- **Desktop** — Linux pure-logic tests + Windows cross-build; Windows MyGo build, generated-client consistency check, and installer artifacts.
- **Frontend** — build, bundle budget, ESLint, unit/component tests, CLI syntax, accessibility, and visual regression.
- **Python** — Research Optimizer, paper-reproduction example, AutoResearch example.
- **Docker** — backend/sandbox/frontend image builds; Compose smoke on the main branch.
- **Hygiene** — UTF-8/mojibake check and mock audit.

The latest verification status (2026-11-02) and known defects are in [docs/verification.md](docs/verification.md).

### 13.3 E2E

E2E scripts live in `research-assistant/test/e2e/` and use `RA_API_URL` / `RA_API_TOKEN`. Coverage:

- Basics: `http-smoke`, `routing-and-prompts`, `literature`, `literature-plan`, `document-intelligence`, `model-profiles`, `verification`, `sandbox-network`, `round6-*`
- Plan governance: `plan-library`, `repro-bundle`, `cost-budget`, `budget-recovery`, `plan-revisions`, `review-hitl`
- Tool ecosystem: `tool-ecosystem`
- Deep suites: `round7-api-coverage`, `round7-real-scenarios`, `round7-paper-reproduction`, `round7-autoresearch`, `round7-task-execute`, `round7-doc-assist`, `round7-sandbox-deep`
- Desktop/commands: `desktop-smoke`, `sandbox-command`

---

## 14. Operating boundaries and security

- This is a research prototype; APIs and data structures may change without backward-compatibility guarantees.
- Single-node persistence uses "atomic per-plan snapshots + a small append-only JSONL index/event log"; multi-replica deployment needs a shared transactional database and leader election.
- Static API tokens, plan ownership, and plan-level approvals exist, but this is not OIDC, RBAC, or production multi-tenant authentication.
- In Compose, the gateway mounts neither the Docker socket nor the host workspace; only the internal executor holds the socket, and it remains a high-privilege execution boundary.
- The default runtime image is a CPU/general Python image; GPU passthrough requires NVIDIA Container Toolkit and `SANDBOX_DOCKER_GPUS=all`.
- The native provider is weaker than a container; do not use `full_access` on untrusted code, and do not share one desktop data directory across tenants.
- Paper reproduction focuses on lightweight smoke tests and structural ablations, not large-scale dataset training; insufficient resources return `blocked/partial`.
- Framework_Evaluation keeps a deterministic offline baseline; it is not a real agent-framework performance benchmark.
- Candidate search is layered UCB, Top-K Beam, and UCT-style search driven by real validation reward — not full MCTS or a general RL policy.
- Memory uses deterministic extraction by default; LLM extraction and embedding/graph extensions are off by default and must be enabled and evaluated explicitly.

---

## 15. Documentation index

### 15.1 Module READMEs

| Module | Chinese | English |
|---|---|---|
| Main product | [research-assistant](research-assistant/README.md) | [English](research-assistant/README.en.md) |
| Go backend | [backend](research-assistant/backend/README.md) | [English](research-assistant/backend/README.en.md) |
| React workbench | [frontend](research-assistant/frontend/README.md) | [English](research-assistant/frontend/README.en.md) |
| Sandbox service | [docker-sandbox](research-assistant/docker-sandbox/README.md) | [English](research-assistant/docker-sandbox/README.en.md) |
| Python optimizer | [research-optimizer](research-assistant/research-optimizer/README.md) | [English](research-assistant/research-optimizer/README.en.md) |
| AI services | [ai-services](research-assistant/ai-services/README.md) | [English](research-assistant/ai-services/README.en.md) |
| Examples | [examples](research-assistant/examples/README.md) | [English](research-assistant/examples/README.en.md) |
| Scripts | [scripts](research-assistant/scripts/README.md) | [English](research-assistant/scripts/README.en.md) |
| Desktop client | [desktop](desktop/README.md) | [English](desktop/README.en.md) |
| Sandbox kernel experiments | [docker-core](docker-core/README.md) | [English](docker-core/README.en.md) |
| Tool-calling example | [tool](tool/README.md) | [English](tool/README.en.md) |

### 15.2 Project documents

- [docs/README.md](docs/README.md): diagram, design, ADR, and round-report index
- [docs/diagrams/README.md](docs/diagrams/README.md): diagram files and editing guide
- [docs/CHANGELOG.md](docs/CHANGELOG.md): commit-oriented change log
- [docs/verification.md](docs/verification.md): latest verification status and known defects
- [reports/README.md](reports/README.md): local acceptance-report directory

---

<p align="center">
  <sub>Research Assistant Agent · 0.9.0</sub>
</p>
