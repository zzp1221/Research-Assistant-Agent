# planner

[简体中文](README.md) | English

`planner` converts user intent into an executable `PlanGraph`. It is the boundary between model output and deterministic scheduling: a model may propose a plan, but only a structurally validated DAG reaches the Scheduler.

## Architecture diagrams

- [Runtime flow](../../../../docs/diagrams/runtime-flow.drawio) ([SVG preview](../../../../docs/diagrams/runtime-flow.svg))
- [System architecture](../../../../docs/diagrams/architecture.drawio) ([SVG preview](../../../../docs/diagrams/architecture.svg))

## Main files

| File | Responsibility |
|---|---|
| `planner.go` | Core `Planner`, `BuildPlanWithRoute` entry, node/edge assembly, default timeouts, tool allowlists |
| `planner_dataset.go` | Dataset experiments and custom benchmark templates |
| `planner_autoresearch.go` | AutoResearch templates and input construction |
| `planner_graph.go` | DAG edges/state/metadata, artifact contracts, graph validation |
| `planner_templates.go` | Framework / Paper / Code / General templates |
| `planner_literature.go` | Literature/paper-analysis intent, retrieval parameters, repository discovery helpers |
| `planner_names_validation.go` | Task-name translation, agent planning, critical-node contract validation |
| `route.go` | `ComplexityRouter`: L0–L4 rule scoring, route/variant decisions, confirmer hook |
| `plan_builder.go` | `TemplateKey{IntentType,Variant}` registry and simple/standard/full templates |
| `plan_variants.go` | Template variants for General/Code/Framework/Paper |
| `general_plan.go` | General plan shapes: deterministic direct/research/paper/literature classification |
| `agent_planner.go` | Agent-based planning entry, skeleton merge, and repair session |
| `context_binder.go` | InputBindings context/artifact binding resolution |
| `semantic_validation.go` | Semantic validation (node contracts, paper analysis, etc.) |
| `runtime_model.go` | Plan-level model snapshot and resolution |
| `taskregistry.go` | Task-type to agent registry |
| `planner_test.go` | Plan structure, dependency, routing, variant, and validation tests |

## Planning flow

1. Receive `IntentContext`, attachments, and user constraints.
2. Choose a task template by intent and complexity (paper reproduction, code execution, benchmark, AutoResearch, general Q&A, etc.).
3. Generate nodes, dependency edges, agent assignment, `InputBindings`, and input/output contracts.
4. Validate the DAG: acyclic, dependencies exist, required inputs complete, node types legal, artifact producer unique.
5. Return `models.PlanGraph`; the API writes it to the Plan Store and creates a revision.

### Complexity routing and template variants

- `ComplexityRouter.Decide` makes the final decision after attachment/paper context resolution: rule scores first; L0/L1 use a fast path (no planner LLM), L2 uses standard templates, L3/L4 use skeleton + agent enhancement.
- General plans follow `general_plan.go`: direct (1 node) / research (2 nodes) / paper (4 nodes) / literature (3 nodes); L4 research requests append a verification phase; `GENERAL_PLAN_FAST_PATH=false` restores the old 3-node shape.
- Variants: General / Code_Execution / Framework_Evaluation offer simple/standard/full; Paper_Reproduction offers light/standard/full; other intents default to standard.
- Routing and variants are recorded in `GraphMeta` (`planning_route` / `complexity` / `complexity_source` / `template_variant` / `general_plan_shape`) and exposed through SSE, plan details, and `/api/metrics`.

## Design constraints

- The planner does not execute tasks or access Docker directly.
- Every node must have an explicit `assigned_to` and task type.
- Paper-reproduction plans must include repository preparation, runtime preparation, experiment execution, and result comparison nodes.
- AutoResearch plans must include contract freeze, candidate search, holdout validation, and report nodes.
- Model output is untrusted; parsing/validation failures trigger one structured repair, then fall back to deterministic templates or return a clear error.

## Tests

```bash
go test ./internal/planner/...
```

Tests cover DAG structure, dependencies, intent mapping, routing/variants, rollback switches, and invalid input.
