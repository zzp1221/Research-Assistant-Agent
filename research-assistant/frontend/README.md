# frontend

React + TypeScript + Vite 工作台，负责对话、DAG 可视化、节点执行面板、PDF 阅读和 AutoResearch 结果展示。

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

`src/App.tsx` 是早期单文件页面实现，保留用于对照和局部功能参考；当前 `main.tsx` 挂载的是 `app/ResearchAssistantApp.tsx`。

## 开发

```bash
npm install
npm run dev       # Vite 开发服务器，默认 5173
npm run build     # TypeScript 构建 + 生产打包
npm run lint      # ESLint
npm test          # Node 原生测试
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
npm test
```

测试覆盖消融计划、实验账本和 Trial 账本解析。`test/fixtures/` 中的 JSON 是前端回放和断言使用的稳定数据。
