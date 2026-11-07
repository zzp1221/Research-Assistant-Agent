# frontend

简体中文 | [English](README.en.md)

React + TypeScript + Vite 工作台，负责对话、DAG 可视化、节点执行面板、PDF 阅读、文献库、计划库、审阅、成本、记忆与设置。

## 界面与流程

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [运行流程图](../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../docs/diagrams/agent-execution-flow.svg)）

前端只通过 REST/SSE 与后端交互：创建计划后渲染 PlanGraph，执行时订阅计划级 SSE 事件，并把节点日志、代码、指标、报告和证据图回填到执行侧栏。

## 技术栈

- React 19 + TypeScript 5.9
- Vite 8
- Tailwind CSS 4
- React Flow（`@xyflow/react`）用于 DAG 与参数树
- `@react-pdf-viewer` + `pdfjs-dist` 用于 PDF 阅读
- `react-markdown` + KaTeX 用于报告渲染
- MyGo runtime（`mygo-runtime`）用于桌面 bridge 类型
- Vitest + Testing Library（组件）、Playwright（E2E / 视觉回归 / 无障碍）

## 目录结构

```text
frontend/
├── src/
│   ├── main.tsx                       应用入口
│   ├── app/
│   │   ├── ResearchAssistantApp.tsx   当前生产主壳
│   │   ├── context/                   运行时 Context 与 Provider
│   │   ├── hooks/                     聊天、布局、执行、PDF、更新 hooks
│   │   └── constants/uiText.ts        UI 文案
│   ├── features/
│   │   ├── chat/                      聊天、附件、会话管理、全权确认
│   │   ├── plan-graph/                DAG 布局与节点组件
│   │   ├── execution/                 节点执行侧栏
│   │   ├── plan-library/              计划库（项目/搜索/归档/复制）
│   │   ├── review/                    计划审阅与审批收件箱
│   │   ├── cost/                      成本与预算控制台
│   │   ├── memory/                    记忆面板
│   │   ├── literature/                文献检索、文献库、引用图
│   │   ├── pdf-viewer/                PDF 面板与全文翻译
│   │   ├── settings/                  模型 / 工具 / Agent / 运行模式 / 记忆
│   │   ├── autoresearch/              Trial 账本与候选搜索视图
│   │   ├── experiment/                实验搜索账本与视图
│   │   ├── benchmark/                 Benchmark 报告视图
│   │   ├── claim-evidence/            主张证据图
│   │   ├── ablation/                  消融树视图
│   │   ├── verification/              视觉校验徽标与详情
│   │   ├── onboarding/                桌面首次启动向导
│   │   ├── help/                      使用手册抽屉
│   │   └── workbench/                 左侧导航、状态栏
│   ├── services/api/                  HTTP 与 SSE 客户端
│   ├── services/desktopBridge.ts      桌面 bridge 适配（MyGo / Wails / 测试）
│   ├── services/mygo.ts               MyGo 生成的类型客户端
│   ├── contracts/                     API、事件与桌面契约
│   └── config/env.ts                  API Base URL 解析
├── test/                              Node 原生测试、组件测试、Playwright 与视觉基线
├── public/                            静态资源
└── package.json
```

当前 `main.tsx` 挂载 `app/ResearchAssistantApp.tsx`；早期单文件 `src/App.tsx` 已删除，避免两套 UI 行为分叉。

## 主要界面

- **对话工作区**：输入研究目标、上传附件、查看会话与历史会话；支持运行权限选择与全权确认。
- **计划图**：展示 DAG 节点、依赖、复杂度/规划路径/模板变体；AutoResearch 节点展开为复合搜索视图。
- **执行侧栏**：节点日志、生成代码、报告、指标、图表、证据图与视觉校验徽标。
- **PDF 面板**：论文阅读、划词追问、全文翻译与 AI 解释；文档智能面板展示质量/表格/图注。
- **文献面板**：检索、导入、文献库、引用图、加入会话。
- **计划库**：项目分组、搜索、过滤、分页、重命名、归档/恢复、复制、导出与删除。
- **计划审阅**：revision 列表、节点 inspector、依赖编辑、diff、保存草稿、批准并运行、拒绝。
- **待办中心**：跨计划聚合运行时审批与结构化提问。
- **成本控制台**：预算设置、用量趋势、计划成本、预警。
- **设置**：模型、工具（MCP/HTTP）、Agent（PromptVersion/Profile）、运行模式（Provider/Runtime）、记忆五个 tab。
- **桌面能力**：Onboarding、运行状态徽章、更新可用提示、诊断导出、目录选择/定位文件。

## 开发

```bash
npm install
npm run dev          # Vite 开发服务器，默认 5173
npm run build        # TypeScript 构建 + 生产打包
npm run check:bundle # 检查首屏与懒加载 chunk 预算
npm run lint         # ESLint（含 jsx-a11y）
npm test             # Node 解析测试 + Vitest 组件测试
npm run test:unit    # Node 原生测试（test/*.test.mjs）
npm run test:components # Vitest + Testing Library（test/components）
npm run test:e2e     # Playwright（需运行中的 frontend）
npm run test:a11y    # axe 无障碍冒烟 + 键盘测试
npm run test:visual  # Playwright 视觉回归（Linux 基线）
npm run preview      # 预览生产构建
```

开发态 API 地址由 `src/config/env.ts` 解析：

1. 优先使用 `VITE_API_BASE_URL`。
2. 开发态默认指向 `http://<当前主机>:8080`。
3. 生产态使用同源地址，由 Nginx 反向代理到 `backend:8080`；桌面端由 loopback proxy 提供同源。
4. `VITE_API_AUTH_TOKEN` 只在开发态使用；生产构建不把 Token 编进 bundle，Nginx 在请求时注入 `Authorization`。

## 桌面 bridge

`src/services/desktopBridge.ts` 按以下顺序选择实现：

1. `window.__RA_DESKTOP__`（组件测试注入）；
2. MyGo 生成的 `API`（`window.mygo` 存在时）；
3. Wails 的 `window.go.main.API`（`-tags "desktop wails"` 回退）；
4. 普通浏览器返回 `undefined`。

桥接方法包括 Onboarding、Runtime 设置/安装/清理、模型连接测试、目录选择、在文件夹中显示、更新检查/下载/安装、通知、诊断导出与重试。修改 `desktop/bridge/bridge.go` 的导出方法后需运行 `mygo generate` 重新生成 `src/services/mygo.ts`。

## 构建与部署

Docker 构建使用 `Dockerfile`：Node 22 构建后由 Nginx 提供静态资源，并把 `/api/` 反向代理到 `backend:8080`。Nginx 模板 `nginx.conf.template` 在容器启动时用 envsubst 注入 `API_AUTH_TOKEN`，浏览器不持有该 Token；`client_max_body_size 33m` 与后端默认 32 MiB 上传上限对齐。桌面/单文件模式由 Go 后端或 MyGo 内嵌 `dist`。

## 测试

```bash
npm test                # 解析测试 + 组件测试
npm run test:components # 只跑 Vitest 组件测试
npm run test:e2e        # Playwright E2E
npm run test:a11y       # axe + 键盘无障碍测试
npm run test:visual     # 视觉回归（需先构建并启动 preview）
```

- `test/*.test.mjs`：消融计划、实验账本、Trial 账本、PDF 附件、Benchmark 报告解析。
- `test/components/`：Vitest + Testing Library + jsdom，覆盖 PDF 本地 worker、全文翻译面板、Benchmark 报告、ExecutionSidebar、ChatComposer、文献面板、模型设置、会话切换/登录、图布局性能、桌面 bridge 与 SSE 重连等。
- `test/e2e/`：Playwright，覆盖首页/移动端布局、PDF 上传/打开/全文翻译、Benchmark 报告、无障碍冒烟与键盘操作。
- `test/visual/`：6 个高价值场景的 Playwright 视觉回归；基线在 Linux + Noto CJK 字体环境生成。
- `test/fixtures/` 中的 JSON 是前端回放和断言使用的稳定数据。

最近一次基线（36 个测试文件 / 144 个测试通过，build/lint/bundle 全通过）与已知 UI 缺陷见 [docs/verification.md](../../docs/verification.md)。

### 性能与质量门禁

- 首屏入口 JS gzip ≤ 320 KB，入口 CSS gzip ≤ 35 KB，单个懒加载 chunk gzip ≤ 400 KB；由 `npm run check:bundle` 检查。
- PDF 阅读器、React Flow 画布、报告 Markdown/KaTeX 和执行侧栏结果视图均按需加载。
- a11y 使用 `@axe-core/playwright` + `eslint-plugin-jsx-a11y`；严重/致命问题为 0 才通过。

E2E 默认访问 `http://127.0.0.1:15173`，可用 `E2E_BASE_URL` 覆盖；容器内运行时请使用 compose 网络中的 `http://research-assistant-frontend-1`。
