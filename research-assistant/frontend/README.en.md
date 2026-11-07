# frontend

[简体中文](README.md) | English

React + TypeScript + Vite workbench for chat, DAG visualization, node execution, PDF reading, literature, plan library, review, cost, memory, and settings.

## UI and flow

- [System architecture](../../docs/diagrams/architecture.drawio) ([SVG preview](../../docs/diagrams/architecture.svg))
- [Runtime flow](../../docs/diagrams/runtime-flow.drawio) ([SVG preview](../../docs/diagrams/runtime-flow.svg))
- [Agent execution flow](../../docs/diagrams/agent-execution-flow.drawio) ([SVG preview](../../docs/diagrams/agent-execution-flow.svg))

The frontend talks to the backend only through REST/SSE: it renders the PlanGraph after plan creation, subscribes to plan-level SSE events during execution, and fills the execution sidebar with node logs, code, metrics, reports, and evidence graphs.

## Tech stack

- React 19 + TypeScript 5.9
- Vite 8
- Tailwind CSS 4
- React Flow (`@xyflow/react`) for DAGs and parameter trees
- `@react-pdf-viewer` + `pdfjs-dist` for PDF reading
- `react-markdown` + KaTeX for report rendering
- MyGo runtime (`mygo-runtime`) for desktop bridge types
- Vitest + Testing Library (components), Playwright (E2E / visual regression / accessibility)

## Directory layout

```text
frontend/
├── src/
│   ├── main.tsx                       App entry
│   ├── app/
│   │   ├── ResearchAssistantApp.tsx   Production shell
│   │   ├── context/                   Runtime context and providers
│   │   ├── hooks/                     Chat, layout, execution, PDF, update hooks
│   │   └── constants/uiText.ts        UI copy
│   ├── features/
│   │   ├── chat/                      Chat, attachments, session manager, full-access confirmation
│   │   ├── plan-graph/                DAG layout and node components
│   │   ├── execution/                 Node execution sidebar
│   │   ├── plan-library/              Plan library (projects/search/archive/duplicate)
│   │   ├── review/                    Plan review and approval inbox
│   │   ├── cost/                      Cost and budget console
│   │   ├── memory/                    Memory panel
│   │   ├── literature/                Literature search, library, citation graph
│   │   ├── pdf-viewer/                PDF panel and full-text translation
│   │   ├── settings/                  Models / tools / agents / runtime / memory
│   │   ├── autoresearch/              Trial ledger and candidate search view
│   │   ├── experiment/                Experiment search ledger and view
│   │   ├── benchmark/                 Benchmark report view
│   │   ├── claim-evidence/            Claim-evidence graph
│   │   ├── ablation/                  Ablation tree view
│   │   ├── verification/              Visual-verification badge and details
│   │   ├── onboarding/                Desktop first-run wizard
│   │   ├── help/                      Help drawer
│   │   └── workbench/                 Rail navigation and status bar
│   ├── services/api/                  HTTP and SSE clients
│   ├── services/desktopBridge.ts      Desktop bridge adapter (MyGo / Wails / tests)
│   ├── services/mygo.ts               Generated MyGo typed client
│   ├── contracts/                     API, event, and desktop contracts
│   └── config/env.ts                  API base URL resolution
├── test/                              Node tests, component tests, Playwright, visual baselines
├── public/                            Static assets
└── package.json
```

`main.tsx` mounts `app/ResearchAssistantApp.tsx`; the early single-file `src/App.tsx` was removed to avoid two divergent UI behaviors.

## Main surfaces

- **Chat workspace** — research goal input, attachment upload, session history, runtime-permission selection, and full-access confirmation.
- **Plan graph** — DAG nodes, dependencies, complexity/planning route/template variant; AutoResearch nodes expand into a composite search view.
- **Execution sidebar** — node logs, generated code, reports, metrics, charts, evidence graph, and visual-verification badge.
- **PDF panel** — paper reading, selection follow-up, full-text translation, and AI explanation; document-intelligence panels show quality, tables, and figures.
- **Literature panel** — search, import, library, citation graph, and attach-to-session.
- **Plan library** — project grouping, search, filters, pagination, rename, archive/restore, duplicate, export, and delete.
- **Plan review** — revision list, node inspector, dependency editing, diff, save draft, approve-and-run, reject.
- **Inbox** — cross-plan runtime approvals and structured questions.
- **Cost console** — budget settings, usage trends, plan cost, alerts.
- **Settings** — five tabs: models, tools (MCP/HTTP), agents (PromptVersion/Profile), runtime (provider/runtime), memory.
- **Desktop capabilities** — onboarding, runtime status badge, update banner, diagnostics export, directory selection, reveal-in-folder.

## Development

```bash
npm install
npm run dev          # Vite dev server, default 5173
npm run build        # TypeScript build + production bundle
npm run check:bundle # First-screen and lazy-chunk budget check
npm run lint         # ESLint (including jsx-a11y)
npm test             # Node parsing tests + Vitest component tests
npm run test:unit    # Node native tests (test/*.test.mjs)
npm run test:components # Vitest + Testing Library (test/components)
npm run test:e2e     # Playwright (requires a running frontend)
npm run test:a11y    # axe accessibility smoke + keyboard tests
npm run test:visual  # Playwright visual regression (Linux baseline)
npm run preview      # Preview the production build
```

Development API resolution in `src/config/env.ts`:

1. `VITE_API_BASE_URL` has highest priority.
2. Development defaults to `http://<current host>:8080`.
3. Production uses same-origin, proxied by nginx to `backend:8080`; desktop uses the loopback proxy for same-origin.
4. `VITE_API_AUTH_TOKEN` is development-only; production bundles never contain the token, and nginx injects `Authorization` at request time.

## Desktop bridge

`src/services/desktopBridge.ts` selects an implementation in this order:

1. `window.__RA_DESKTOP__` (component-test injection);
2. MyGo-generated `API` (when `window.mygo` exists);
3. Wails `window.go.main.API` (`-tags "desktop wails"` fallback);
4. `undefined` in an ordinary browser.

Bridge methods cover onboarding, runtime settings/install/cleanup, model connection tests, directory selection, reveal-in-folder, update check/download/install, notifications, diagnostics export, and retry. After changing exported methods in `desktop/bridge/bridge.go`, run `mygo generate` to refresh `src/services/mygo.ts`.

## Build and deployment

The Docker build uses `Dockerfile`: Node 22 builds the app, then nginx serves static assets and proxies `/api/` to `backend:8080`. `nginx.conf.template` injects `API_AUTH_TOKEN` with envsubst at container start, so browsers never hold the token; `client_max_body_size 33m` matches the backend's 32 MiB upload limit. Desktop/single-file mode embeds `dist` in the Go backend or MyGo app.

## Tests

```bash
npm test                # Parsing tests + component tests
npm run test:components # Vitest component tests only
npm run test:e2e        # Playwright E2E
npm run test:a11y       # axe + keyboard accessibility
npm run test:visual     # Visual regression (build and start preview first)
```

- `test/*.test.mjs`: ablation plans, experiment ledger, trial ledger, PDF attachments, benchmark report parsing.
- `test/components/`: Vitest + Testing Library + jsdom, covering the local PDF worker, translation panel, benchmark report, ExecutionSidebar, ChatComposer, literature panel, model settings, session switch/login, graph layout performance, desktop bridge, and SSE reconnect.
- `test/e2e/`: Playwright covering desktop/mobile layout, PDF upload/open/full-text translation, benchmark report, accessibility smoke, and keyboard operation.
- `test/visual/`: Playwright visual regression for six high-value scenarios; baselines are generated on Linux with Noto CJK fonts.
- `test/fixtures/`: stable JSON for frontend replay and assertions.

The latest baseline (36 test files / 144 tests passing, build/lint/bundle green) and known UI defects are in [docs/verification.md](../../docs/verification.md).

### Performance and quality gates

- Entry JS gzip ≤ 320 KB, entry CSS gzip ≤ 35 KB, individual lazy chunk gzip ≤ 400 KB; enforced by `npm run check:bundle`.
- The PDF reader, React Flow canvas, Markdown/KaTeX reports, and execution result views are lazy-loaded.
- Accessibility uses `@axe-core/playwright` + `eslint-plugin-jsx-a11y`; serious/critical issues must be zero.

E2E defaults to `http://127.0.0.1:15173` and can be overridden with `E2E_BASE_URL`; inside containers use `http://research-assistant-frontend-1` on the Compose network.
