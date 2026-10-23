# frontend

React + TypeScript + Vite 工作台，负责对话、DAG 可视化、节点执行面板、PDF 阅读和 AutoResearch 结果展示。

## 界面与流程

- [系统架构图](../../docs/diagrams/architecture.drawio)（[SVG 预览](../../docs/diagrams/architecture.svg)）
- [运行流程图](../../docs/diagrams/runtime-flow.drawio)（[SVG 预览](../../docs/diagrams/runtime-flow.svg)）
- [Agent 执行流程图](../../docs/diagrams/agent-execution-flow.drawio)（[SVG 预览](../../docs/diagrams/agent-execution-flow.svg)）

前端只通过 REST/SSE 与后端交互：创建计划后渲染 PlanGraph，执行时订阅计划级 SSE 事件，并把节点日志、代码、指标、报告和证据图回填到执行侧栏。

## 技术栈

- React 19 + TypeScript
- Vite 8
- Tailwind CSS 4
- React Flow（`@xyflow/react`）用于 DAG 与参数树
- `@react-pdf-viewer` + `pdfjs-dist` 用于 PDF 阅读
- `react-markdown` + KaTeX 用于报告渲染

## 目录结构

```text
frontend/
├── src/
│   ├── main.tsx                       应用入口
│   ├── app/
│   │   ├── ResearchAssistantApp.tsx   当前生产主壳
│   │   ├── context/                   运行时 Context 与 Provider
│   │   ├── hooks/                     聊天、布局、执行运行时 hooks
│   │   └── constants/uiText.ts        UI 文案
│   ├── features/
│   │   ├── chat/                      聊天与附件
│   │   ├── plan-graph/                DAG 布局与节点组件
│   │   ├── execution/                 节点执行侧栏
│   │   ├── autoresearch/              Trial 账本与候选搜索视图
│   │   ├── experiment/                实验搜索账本与视图
│   │   ├── claim-evidence/            主张证据图
│   │   ├── ablation/                  消融树视图
│   │   └── pdf-viewer/                PDF 面板
│   ├── services/api/                  HTTP 与 SSE 客户端
│   ├── contracts/                     API 与事件类型
│   └── config/env.ts                  API Base URL 解析
├── test/                              Node 原生测试与回放 fixture
├── public/                            静态资源
└── package.json
```

早期单文件 `src/App.tsx` 已删除；当前 `main.tsx` 挂载的是 `app/ResearchAssistantApp.tsx`，避免两套 UI 行为继续分叉。

## 开发

```bash
npm install
npm run dev       # Vite 开发服务器，默认 5173
npm run build     # TypeScript 构建 + 生产打包
npm run check:bundle # 检查首屏与懒加载 chunk 预算
npm run lint      # ESLint（含 jsx-a11y）
npm test          # Node 解析测试 + Vitest 组件测试
npm run test:unit # Node 原生测试（test/*.test.mjs）
npm run test:components # Vitest + Testing Library（test/components）
npm run test:e2e  # Playwright（需运行中的 frontend）
npm run test:a11y # axe 无障碍冒烟 + 键盘测试
npm run test:visual # Playwright 视觉回归（Linux 基线）
npm run preview   # 预览生产构建
```

开发态 API 地址由 `src/config/env.ts` 解析：

1. 优先使用 `VITE_API_BASE_URL`。
2. 开发态默认指向当前主机的 `:8080`。
3. 生产态使用同源地址，由后端或 Nginx 反向代理。

## 主要界面

- 对话工作区：输入研究目标、上传附件、查看会话。
- 计划图：展示 DAG 节点、依赖和状态；AutoResearch 节点会展开为复合搜索视图。
- 执行侧栏：节点日志、生成代码、报告、指标、图表和证据图。
- PDF 面板：论文阅读、划词追问和翻译。
- Trial 视图：展示候选、父路径、Reward、UCB/UCT、Keep/Reject 和耗时。

## 构建与部署

Docker 构建使用 `Dockerfile`：Node 构建后由 Nginx 提供静态资源，并把 `/api/` 反向代理到 `backend:8080`。本地单文件模式则由 Go 后端嵌入 `dist` 目录。

## 测试

```bash
npm test                # 解析测试 + 组件测试
npm run test:components # 只跑 Vitest 组件测试
npm run test:e2e        # Playwright E2E
npm run test:a11y       # axe + 键盘无障碍测试
npm run test:visual     # 视觉回归（需先构建并启动 preview）
```

- `test/*.test.mjs`：消融计划、实验账本、Trial 账本、PDF 附件和 Benchmark 报告解析。
- `test/components/`：Vitest + Testing Library + jsdom，覆盖 PDF 本地 worker、全文翻译面板、Benchmark 报告、ExecutionSidebar、ChatComposer 和会话切换/登录。
- `test/e2e/`：Playwright，覆盖首页、PDF 上传/打开/全文翻译流程、Benchmark 报告页和无障碍冒烟/键盘操作。
- `test/visual/`：6 个高价值场景的 Playwright 视觉回归；基线在 Linux + Noto CJK 字体环境生成。
- `test/fixtures/` 中的 JSON 是前端回放和断言使用的稳定数据。

### 性能与质量门禁

- 首屏入口 JS gzip ≤ 320 KB，入口 CSS gzip ≤ 35 KB，单个懒加载 chunk gzip ≤ 400 KB；由 `npm run check:bundle` 检查。
- PDF 阅读器、React Flow 画布、报告 Markdown/KaTeX 和执行侧栏结果视图均按需加载。
- a11y 使用 `@axe-core/playwright` + `eslint-plugin-jsx-a11y`；严重/致命问题为 0 才通过。

E2E 默认访问 `http://127.0.0.1:15173`，可用 `E2E_BASE_URL` 覆盖；容器内运行时请使用
compose 网络中的 `http://research-assistant-frontend-1`。