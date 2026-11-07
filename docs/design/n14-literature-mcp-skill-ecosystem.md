# N-14 文献工作流与 Skill 模板化 MCP 方案

- **状态**：设计稿（待评审）
- **日期**：2026-11-02
- **范围**：文献检索与文献库、Zotero 集成、Skill 模板化、AgentProfile/PromptVersion、MCP 工具权限与审计
- **关联**：`docs/design/n13-agent-tool-ecosystem-mcp.md`、`research-assistant/backend/internal/tools`、`internal/agentprofile`、`internal/literature`、`internal/documents`、`internal/api/tools.go`、`frontend/src/features/settings`
- **一句话结论**：不要把视频里的 Skill 直接当成 MCP server；应把"文献工作流"做成受控的领域 MCP（literature / zotero / science），把"Skill"做成可版本化、可评测的 AgentProfile + PromptVersion 模板。N-13 已有的 ToolRegistry、ToolGrant、Approval、ToolCallRecord、AgentProfile 可以直接复用。

---

## 1. 摘要

外部经验（视频）里有三个值得吸收的判断：

1. **通用 Skill 不适合直接做文献检索**：AI 看不到完整数据库，论文站反爬强，逐页下载效率低且容易编造引用。
2. **文献工作流应该由专用工具/网站 + Zotero 组成**：检索用专用数据源，管理用 Zotero，关系发现用 Connected Papers 类工具。
3. **优秀 Skill 的本质是"工作流 + Prompt + 工具约束"**：Nature Skills 的 15 个子技能、Academic Research 的苏格拉底式质疑、Cortex Auto Research 的反复实验，都是可产品化的流程，而不是一个黑盒脚本。

对当前项目的启示不是"再装五个 Skill"，而是：

- 把文献能力 MCP 化/工具化，提供**可追溯的来源、DOI、provider、页码**；
- 把 Skill 变成 **AgentProfile + PromptVersion + ToolAllowlist + 评测集**；
- 把 Zotero 作为**首个外部 MCP 数据源**，先只读、再写回；
- 所有写入/下载/批注走 **ToolGrant + Approval + ToolCallRecord**。

---

## 2. 背景与参考

### 2.1 外部内容要点

| 外部内容 | 核心观点 | 对本项目的含义 |
|---|---|---|
| Scientific Agent Skills | 科学数据库、Python 科学包、数据分析与可视化 | 科学计算应作为受控工具（native/sandbox），不是自由代码 |
| Nature Skills（15 子 skill） | 观点 → 文献 → 写作 → 绘图全流程 | 拆成多个 AgentProfile，按阶段收紧工具白名单 |
| Academic Research Skill | 苏格拉底式反复质疑、Idea 提取、论文架构 | 用结构化提问/HITL，而不是模型自问自答 |
| Cortex Auto Research | 反复实验、调参、改代码、数据分析 | 复用 AutoResearch + sandbox + 预算/审批/回放 |
| Codex Cloud Academic Skills | 中文用户、人文社科写作 | 中文 Prompt 模板 + 模型 Profile + 评测集 |
| 文献管理 | 不推荐通用 Skill 直接检索；推荐专用网站 + Zotero | 文献检索走 provider API；Zotero 走 MCP |
| Undermoon / Connected Papers | 调研全面、关系网络 | 优先 API/导出；关系图用 OpenAlex 引用图实现 |

### 2.2 现有项目能力（N-13 之后）

| 能力 | 现状 | 可复用点 |
|---|---|---|
| 工具注册与调用 | `internal/tools`：ToolDefinition、ToolRegistry、ToolInvoker、native/HTTP/MCP stdio/MCP HTTP | 直接承载 literature/zotero MCP 工具 |
| 权限 | ToolGrant、AllowedTools、AgentProfile ToolAllowlist、RuntimeAction tool_call 审批 | Zotero 写入/PDF 下载必须授权 + 审批 |
| 审计 | ToolCallRecord（参数 hash、结果 hash、耗时、ApprovalID） | 引用溯源、回放、导出 |
| Agent 配置 | AgentProfile + PromptVersion + ModelProfile | Skill 模板的运行时载体 |
| 文献 | `internal/literature`：多 provider、去重、排序、文献库、BibTeX、OpenAlex 引用图 | literature MCP 的后端实现 |
| 文档 | `internal/documents`：PDF 解析、OCR、BM25、版面/表格/图注、quote locate | Zotero 附件/PDF 阅读工具 |
| 前端 | 设置页模型/工具/Agent 三 tab；计划图 tool_call 展示 | 新增文献/Zotero 连接卡与引用溯源面板 |

---

## 3. 目标与非目标

### 3.1 目标

1. **文献 MCP**：把现有 `internal/literature` 包装成统一工具面，供 Planner/Agent 调用；结果必须带 provider、DOI/arXiv ID、来源 URL、检索时间。
2. **Zotero MCP**：支持读取 Zotero 文献库/集合/条目/附件文本，后续支持受控写回（新建条目、加入集合、附件、标签）。
3. **Skill 模板化**：定义可导入/导出的 Skill manifest，落地为 PromptVersion + AgentProfile + ToolAllowlist + EvalCases。
4. **安全边界**：文献抓取不绕过 ToS；Zotero 写入必须审批；secret 不进入 plan/export/log；PDF 文本标记 untrusted。
5. **可评测**：Prompt 版本、Skill 模板都有 eval 结果；可回滚。
6. **可审计**：每次文献/Zotero 调用有 ToolCallRecord，能回答"用了哪个库、哪篇论文、哪一页、哪个版本"。

### 3.2 非目标

- 不做论文站点的通用爬虫/反爬绕过；
- 不自动下载受版权保护的 PDF（需用户授权/合法来源）；
- 不做公开 Skill 市场/计费/评级；
- 不允许 Skill 携带任意可执行代码；工具必须是注册的 native/HTTP/MCP；
- 不复制闭源 Skill 的 Prompt 资产；只借鉴工作流模式。

---

## 4. 总体架构

```text
                 ┌────────────────────────────────────────────┐
                 │              Frontend Workbench             │
                 │  Settings → Tools / Agents                  │
                 │  Plan Graph → citation provenance / tool call│
                 └──────────────────────┬─────────────────────┘
                                        │ REST / SSE
                                        ▼
┌──────────────────────────────────────────────────────────────────┐
│                         Go Backend                                │
│  Planner ──► Scheduler ──► RoutedTaskExecutor                     │
│                    │              │                               │
│                    │              ├─ AgentProfile (PromptVersion  │
│                    │              │   + ModelProfile + Allowlist) │
│                    │              └─ ToolInvoker                   │
│                    │                    ├─ schema / grant / approval│
│                    │                    ├─ transport: native/http  │
│                    │                    │   / mcp_stdio / mcp_http  │
│                    │                    └─ ToolCallRecord          │
│                    ▼                                              │
│              PlanGraph / Review / RuntimeAction / Audit           │
└──────────────────────────────────────────────────────────────────┘
                                        │ MCP
          ┌─────────────────────────────┼─────────────────────────────┐
          ▼                             ▼                             ▼
  mcp:literature                 mcp:zotero                    mcp:science
  OpenAlex/Crossref/             Zotero Local API /            Python/sandbox
  arXiv/PubMed/S2/               Web API / attachments         data/profile/plot
  Unpaywall/DBLP
```

**分层原则**：

- **数据源层**：只做 provider 调用、去重、排序、缓存；不暴露给 LLM 直接访问。
- **MCP 层**：把领域能力包装成有 schema 的工具/资源；负责协议、超时、输出大小。
- **权限层**：ToolGrant + AllowedTools + Approval；写操作默认拒绝。
- **Agent 层**：PromptVersion + ModelProfile + ToolAllowlist；Skill 是这一层的模板。
- **计划层**：显式 tool_call 节点或 profile 节点；不把工具名写进自由文本 Prompt。

---

## 5. 详细设计

### 5.1 领域 MCP Server 划分

不建议"一个 Skill 一个 MCP server"，建议按领域拆：

| MCP Server | 职责 | 典型工具 | 部署形态 |
|---|---|---|---|
| `mcp:literature` | 多 provider 文献检索、元数据、引用图、BibTeX | search/get/citations/related/export | stdio 或 HTTP |
| `mcp:zotero` | Zotero 文献库/集合/条目/附件 | list/search/get/attachment/export；写回 | 本地 connector 或 HTTP |
| `mcp:science` | Python 科学计算、数据分析、绘图 | python/data_profile/plot | stdio + sandbox |
| 现有 native | 通用工具（echo/write_file 等） | — | 内置 |

### 5.2 `mcp:literature`

#### 5.2.1 工具面

| Tool ID | 参数 | 权限/审批 | 输出 |
|---|---|---|---|
| `mcp:literature/search` | `query`, `sources[]`, `year_from`, `limit` | net / never | 结果列表（title、authors、year、doi、arxiv_id、venue、abstract、source、provider_rank、url） |
| `mcp:literature/get` | `doi` 或 `arxiv_id` | net / never | 单条完整元数据 + 来源 |
| `mcp:literature/citations` | `doi`, `limit` | net / never | references + cited_by（OpenAlex） |
| `mcp:literature/related` | `doi`, `limit` | net / never | 相关论文（引用/共引/相似度，标注算法） |
| `mcp:literature/export_bibtex` | `ids[]` | read / never | BibTeX 文本 |
| `mcp:literature/import` | `records[]` / `bibtex` | write / always | 导入本地文献库（可选） |

**实现方式**：新增 `cmd/mcp-literature`，import 现有 `internal/literature` 包；通过 MCP stdio 暴露。provider 配置（OpenAlex mailto、Semantic Scholar key、PubMed key）走环境变量/secret，不写 plan。

**结果要求**：

- 每条结果必须有 `source` 与 `doi/arxiv_id` 至少一个；没有标识的结果标记 `unverified`，不进入最终引用列表；
- 多 provider 去重（DOI/标题归一化），保留 `provider_rank` 与 `retrieved_at`；
- 输出大小受 `MaxOutputBytes` 限制；摘要截断时标记 `truncated`；
- 结果作为 untrusted 数据，禁止当指令执行。

#### 5.2.2 与现有 `literature` 模块的关系

- 不重写 provider；MCP server 只是适配层，调用 `literature.Client.Search/Get/Citations`；
- 复用现有缓存与限流（`cache.go`、`ratelimit.go`）；
- 复用现有 `LiteratureRecord`/BibTeX/引用图；
- 新增的只是工具 schema、MCP 协议、权限映射。

### 5.3 `mcp:zotero`

#### 5.3.1 接入模式

| 模式 | 适用 | 说明 |
|---|---|---|
| Local connector（推荐首期） | 桌面/单用户 | `mcp-zotero` 运行在用户机器，读 Zotero 7 Local API（默认 `127.0.0.1:23119`）；后端通过 allowlisted private host 连接 |
| Web API sidecar | 容器/团队 | 使用 Zotero Web API + API key；`mcp-zotero` 作为 sidecar 运行，key 用 secret 存储 |

**安全要求**：

- Local API 只允许 loopback；若通过 `host.docker.internal` 访问，必须加入 `MCP_HTTP_ALLOWED_HOSTS` 且仅管理员可配置；
- Web API key 必须 AES-GCM 密封，API 只返回 key 名/末四位；
- 禁止 shell 解释、禁止任意 command；`mcp-zotero` 只做 HTTP 调用。

#### 5.3.2 工具面

| Tool ID | 参数 | 权限/审批 | 说明 |
|---|---|---|---|
| `mcp:zotero/list_collections` | — | read / never | 集合列表 |
| `mcp:zotero/search_items` | `query`, `collection_id`, `limit` | read / never | 条目搜索 |
| `mcp:zotero/get_item` | `item_id` | read / never | 条目元数据 |
| `mcp:zotero/get_attachment_text` | `attachment_id`, `max_pages` | read / never | PDF 文本（标记 untrusted；复用 documents 解析） |
| `mcp:zotero/export_bibtex` | `item_ids[]` | read / never | BibTeX |
| `mcp:zotero/create_item` | `item_type`, `fields{}` | write / always | 新建条目 |
| `mcp:zotero/add_to_collection` | `item_ids[]`, `collection_id` | write / always | 加入集合 |
| `mcp:zotero/attach_file` | `item_id`, `file_path`/`url` | write / always | 附件；需文件路径白名单 |
| `mcp:zotero/update_tags` | `item_ids[]`, `add[]`, `remove[]` | write / always | 标签 |

**MCP Resources**：

- `zotero://collections/{id}`
- `zotero://items/{id}`
- `zotero://items/{id}/attachments/{attachmentId}`

**写回流程**：

1. Agent 生成 `tool_call` 节点，工具为 `mcp:zotero/create_item` 等；
2. `prepareRuntimeGate` 创建 `tool_call` pending action，详情包含 item 摘要/目标集合；
3. 用户批准后执行；拒绝则不写入；
4. ToolCallRecord 记录 approval_id、item id、结果 hash；
5. 写回失败返回 `tool_execution_failed`，不自动重试写操作。

#### 5.3.3 文献导入流程（推荐）

```text
用户提问/计划
  → literature.search（多 provider）
  → 结果卡片（DOI/来源/摘要）
  → 用户选择
  → zotero.create_item / add_to_collection（审批）
  → zotero.get_attachment_text（只读）
  → paper.read_section / claim 提取
  → 最终引用列表（带 DOI + Zotero item id）
```

### 5.4 `mcp:science`（可选，P2）

| Tool ID | 权限 | 说明 |
|---|---|---|
| `mcp:science/python` | shell / always | 在 sandbox 执行 Python；复用现有 sandbox 工具，不新开任意执行面 |
| `mcp:science/data_profile` | fs.read / never | 数据集 profile |
| `mcp:science/plot` | fs.write / always | 生成图并写 artifact |

设计上不把 `pandas`/`RDKit` 等库直接塞进 Prompt；工具 schema 明确输入输出，执行仍在 sandbox 内。

### 5.5 Skill 模板层

#### 5.5.1 核心判断

**Skill 不是 MCP server，也不是新运行时。** Skill 是一个可导入/导出的 manifest，落地为：

```text
SkillManifest
  ├─ PromptVersion（不可变模板 + 变量）
  ├─ AgentProfile（PromptVersion + ModelProfile + ToolAllowlist + MaxSteps + OutputSchema）
  └─ EvalCases（模板级/模型级评测）
```

这样 Skill 的版本、评测、回滚全部复用 N-13 的 AgentProfile/PromptVersion，不需要第二套执行逻辑。

#### 5.5.2 Manifest 格式（建议）

```yaml
id: skill:nature/literature-review
name: Nature 文献调研
version: 1
description: 从观点到文献综述的工作流
prompt:
  template: |
    You are a literature review agent. ...
    Research question: {{question}}
    Candidate papers: {{candidates}}
  variables: [question, candidates]
model:
  profile_id: env-bootstrap
tools:
  allowlist:
    - mcp:literature/search
    - mcp:literature/get
    - mcp:zotero/search_items
    - mcp:zotero/export_bibtex
max_steps: 12
output_schema: |
  {"type":"object","properties":{"answer":{"type":"string"},"citations":{"type":"array"}},"required":["answer"]}
eval_cases:
  - name: basic
    variables: {question: "graph neural networks"}
    expected_contains: "DOI"
```

导入时：

- 校验 tool allowlist 中的工具存在且 owner 可见；
- 校验 model profile 可见；
- 创建 PromptVersion（draft）→ 创建/更新 AgentProfile；
- 返回 profile id 与 prompt version id；
- 用户激活 prompt / 启用 profile 后才可使用。

#### 5.5.3 五个外部 Skill 的映射

| 外部 Skill | 建议 profile | Prompt 要点 | 工具白名单 |
|---|---|---|---|
| Scientific Agent Skills | `profile:science-analyst` | 数据特征、统计检验、可视化规范 | `mcp:science/data_profile`、`mcp:science/plot`、`mcp:science/python` |
| Nature Skills | 5 个阶段 profile：idea/lit/write/figure/review | 各阶段模板 + 输出 schema | literature + zotero + science |
| Academic Research | `profile:socratic-reviewer` | 追问假设、反例、证据缺口 | `ask_user_question`、literature、documents |
| Cortex Auto Research | `profile:autoresearch` | 实验循环、keep/reject、预算 | sandbox + science + literature |
| Codex Cloud Academic | `profile:zh-academic-writer` | 中文论文结构、引用规范、文风 | literature + zotero + documents |

**注意**：不复制外部 Prompt 原文；只借鉴流程和检查项。

### 5.6 Plan / Scheduler 集成

- `tool_call` 节点：文献检索/Zotero 读取/写入均可用显式节点；
- `profile:<id>` 节点：Skill 模板执行；
- Planner 校验：工具存在、版本匹配、profile 可见、工具 allowlist 包含；
- 执行期：ToolInvoker 再做 grant/allowlist/schema/approval；
- 审批：写操作生成 `tool_call` RuntimeAction，详情含 item/collection/attachment；
- 审计：ToolCallRecord 增加 `provenance`（见 5.9）；
- 回放：文献检索 dry-run 可复现；Zotero 写回真实重放需审批。

### 5.7 API 设计

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/tools` | 注册 `mcp:literature` / `mcp:zotero` server（复用现有 MCP 注册） |
| GET | `/api/tools` | 返回 server 状态、工具列表、fingerprint |
| POST | `/api/tools/:id/test` | 测试工具（默认 dry-run） |
| GET/POST | `/api/tool-grants` | 给 profile 授权 literature/zotero 工具 |
| POST | `/api/integrations/zotero/connect` | 保存 Zotero 连接（local/web，secret 密封） |
| GET | `/api/integrations/zotero/status` | 连接/库/集合健康状态 |
| GET/POST | `/api/skills` | Skill manifest 列表/导入（内部转 AgentProfile + PromptVersion） |
| POST | `/api/skills/:id/eval` | Skill 评测（复用 AgentProfile eval） |
| GET | `/api/plans/:id/citations` | 计划的引用/来源汇总（从 ToolCallRecord + artifacts 聚合） |

> 注：`/api/integrations/zotero/*` 与 `/api/skills` 是新增建议；现有 N-13 的 `/api/tools`、`/api/tool-grants`、`/api/agent-profiles`、`/api/prompt-versions` 可直接复用。

### 5.8 前端设计

**设置 → 工具**

- "文献与 Zotero" 卡片：
  - 连接状态（local/web）、库名、集合数、最近同步；
  - 测试连接、重新发现 tools/list、查看 fingerprint；
  - provider 状态（OpenAlex/Crossref/arXiv/PubMed/Semantic Scholar/Unpaywall）；
- 工具列表按 server 分组，显示 scopes/approval/最近测试。

**设置 → Agent**

- Skill 模板库：导入/导出 manifest、查看 Prompt 版本、评测分数、激活/回滚；
- Prompt 编辑器：变量、diff、eval cases；
- 内置模板：Nature 5 阶段、Socratic、AutoResearch、中文写作。

**计划图 / 执行侧栏**

- tool_call 节点显示 `mcp:literature/search` / `mcp:zotero/get_item`；
- 结果面板显示：DOI、provider、来源 URL、Zotero item id、页码/章节；
- 引用列表可点击打开 Zotero/DOI；
- 写操作显示审批状态与 item/collection。

### 5.9 数据模型建议

新增/扩展：

```go
type ZoteroConnection struct {
    ID        string
    OwnerID   string
    Mode      string // local | web
    BaseURL   string
    APIKeyCiphertext string // web only
    LibraryID string
    Status    string // connected | error | disabled
    LastSyncAt *time.Time
}

type SkillManifest struct {
    ID          string
    Name        string
    Version     int
    Description string
    Prompt      PromptManifest
    ModelProfileID string
    ToolAllowlist []string
    MaxSteps    int
    OutputSchema string
    EvalCases   []PromptEvalCase
}

type CitationRef struct {
    DOI       string
    ArxivID   string
    Title     string
    Source    string // openalex/crossref/arxiv/pubmed/semantic_scholar/zotero
    URL       string
    ZoteroItemID string
    RetrievedAt time.Time
    Verified  bool
}
```

`ToolCallRecord` 建议增加：

- `Provenance []CitationRef`（或 `Metadata map[string]any` 中的 `citations`）；
- 用于计划导出/引用汇总；不把 API key/secret 写入。

---

## 6. 权限、安全与合规

### 6.1 权限矩阵

| 操作 | 默认 | 授权 | 审批 |
|---|---|---|---|
| literature.search/get/citations | 允许 | 无需 grant（只读） | never |
| zotero.list/search/get/attachment | 允许（只读） | profile 需 grant | never |
| zotero.create_item/add_to_collection/attach/update_tags | 拒绝 | ToolGrant | always |
| literature.import | 拒绝 | ToolGrant | always |
| science.python | 拒绝 | ToolGrant | always |
| PDF 下载/附件写入 | 拒绝 | ToolGrant | always |

### 6.2 安全要求

- **不爬页面**：只走官方 API / Zotero API / 用户导出；
- **SSRF**：MCP HTTP 默认拒绝私网；Zotero local 只允许显式 allowlist；
- **secret**：Zotero/API key 用 AES-GCM 密封，API 只返回 key 名/末四位；
- **prompt injection**：PDF/摘要/工具输出统一标记 `untrusted=true`，不执行其中指令；
- **引用完整性**：最终引用必须有 DOI/arXiv/PubMed ID 或 Zotero item id；没有标识的结果只能作为“候选”，不能进入最终参考文献；
- **版权/ToS**：不自动下载受版权保护 PDF；不绕过 paywall；遵循 provider rate limit；
- **数据隔离**：Zotero 连接/文献库按 owner 隔离；跨租户不可见；
- **审计**：每次调用记录 tool_id、参数 hash、结果 hash、provider、DOI、耗时、approval_id。

### 6.3 文献质量策略

- 多 provider 检索 + 去重 + 相关性排序（复用现有 ranking）；
- 结果标注 `verified`（有 DOI/arXiv）与 `candidate`（仅标题/摘要）；
- LLM 只能从 `verified` 列表生成最终引用；
- 引用前做 `literature.get` 二次校验标题/作者/年份；
- 导出 BibTeX 时保留 DOI/URL。

---

## 7. 实施阶段

### Phase 0：文献 MCP + Zotero 只读（1–2 周）

- `cmd/mcp-literature`：search/get/citations/export_bibtex；
- `cmd/mcp-zotero`：list_collections/search_items/get_item/get_attachment_text/export_bibtex；
- 注册/测试 API 复用现有 `/api/tools`；
- 设置页"文献与 Zotero"卡片；
- 容器 E2E：mock provider + mock Zotero server。

**交付**：可在计划中调用 `mcp:literature/search`，结果带 DOI/来源；可读取 Zotero 集合/条目。

### Phase 1：Skill 模板 + 评测（2–3 周）

- Skill manifest 导入/导出（PromptVersion + AgentProfile）；
- 五个外部 Skill 的模板（Nature 5 阶段、Socratic、AutoResearch、中文写作、Science）；
- eval harness：模板级 + 模型级；
- 设置页 Agent 模板库/评测/回滚；
- Socratic profile 接入 `ask_user_question`/review gate。

**交付**：导入 Skill → 创建 profile → 计划使用 → eval 可见 → 回滚可用。

### Phase 2：Zotero 写回 + 附件（2 周）

- create_item/add_to_collection/attach_file/update_tags；
- ToolGrant + approval always；
- 写回审计/失败重试策略；
- 附件路径白名单。

**交付**：批准后写入 Zotero；拒绝无副作用；ToolCallRecord 含 approval_id。

### Phase 3：关系图与科学工具（可选，2–3 周）

- `literature.related`/citation graph 前端可视化；
- `mcp:science` 的 python/data_profile/plot；
- 预算/并发/缓存优化。

---

## 8. 验收标准（容器内）

1. **文献 MCP**：`mcp:literature/search` 返回至少 2 个 provider 的去重结果，每条带 DOI/来源；无标识结果标记 candidate；重复查询命中缓存。
2. **Zotero 只读**：`list_collections/search_items/get_item/get_attachment_text` 正常；owner B 看不到 owner A 的连接/库；secret 不出现在 API/导出/日志。
3. **Zotero 写回**：create_item 创建 pending tool_call；拒绝不写入；批准后写入并记录 approval_id；失败返回结构化错误。
4. **Skill 模板**：导入 manifest 生成 PromptVersion + AgentProfile；计划使用 profile；切换 Prompt v2 行为变化；回滚 v1 恢复；eval 分数可见。
5. **Socratic 流程**：`ask_user_question` 暂停/回答/恢复；review gate 不被绕过。
6. **安全**：私网 Zotero 地址默认被 SSRF 拒绝，allowlist 后可用；PDF 文本标记 untrusted；无 API key 泄漏。
7. **审计**：ToolCallRecord 含 provider/DOI/result_hash；计划导出包含引用来源；dry-run 回放 hash 一致。
8. **回归**：N-13 tool-ecosystem E2E 44/44 仍通过；既有 http-smoke/plan-run/plan-library/plan-revisions/review-hitl 通过。

---

## 9. 测试策略

- 后端单测：literature MCP schema/去重/缓存；zotero client 的 mock HTTP；ToolGrant/approval；Skill manifest 校验。
- 容器 E2E：mock MCP literature server + mock Zotero HTTP server；真实 backend 容器；覆盖 8 条验收。
- 回归：N-13 `tool-ecosystem.mjs` 全量；既有 E2E。
- 安全测试：SSRF 私网拒绝、secret 脱敏、owner 隔离、prompt injection 标记。
- 性能：provider 并发/超时/缓存命中；大结果截断。

---

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 文献 provider 限流/不稳定 | 多 provider 降级、缓存、超时、结果标注 partial |
| Zotero Local API 版本差异 | 适配层抽象 + 版本探测；失败给出可操作错误 |
| 反爬/ToS | 只走官方 API/导出，不爬页面 |
| 引用编造 | 只允许 verified 引用；DOI/arXiv 二次校验 |
| PDF prompt injection | untrusted 标记 + 内容长度限制 + 不执行指令 |
| Zotero 写错/重复 | 审批 + 幂等 key + 写前查重 + ToolCallRecord |
| secret 泄漏 | AES-GCM 密封、只返回 key 名、导出脱敏 |
| Skill 质量差 | eval 集 + 灰度 active + 回滚 |
| MCP server 供应链 | 只允许管理员/owner 注册；command allowlist；fingerprint 审计 |

---

## 11. 与 N-13 的关系

- **不改变 N-13 协议层**：ToolDefinition/ToolInvoker/ToolGrant/Approval/ToolCallRecord 原样复用；
- **新增的是领域适配器**：`mcp:literature`、`mcp:zotero`、`mcp:science`；
- **Skill 是 AgentProfile 的模板**，不是新执行引擎；
- **前端复用**设置页工具/Agent tab，只增加文献/Zotero 卡片与引用面板；
- **兼容**：未注册 literature/zotero 时，现有工具和计划行为不变。

---

## 12. 不照搬外部内容的点

1. 不把 Skill 当 MCP server；
2. 不让通用 Skill 直接爬论文站；
3. 不自动安装 MCP server；
4. 不复制闭源 Prompt 资产；
5. 不自动下载受版权 PDF；
6. 不把"AI 生成的引用"直接当参考文献。

---

## 13. 开放问题

1. Zotero 首期用 Local API 还是 Web API？建议 Local 优先（隐私/速度），Web API 作为团队模式。
2. Undermoon 是否有稳定 API/导出？若有，优先 API；否则只做导出文件导入。
3. Semantic Scholar/PubMed 的 API key 是否由部署方统一配置，还是用户自填？
4. 文献结果是否要落库（本地 literature library）还是只作为 plan artifact？
5. Skill manifest 的权限模型：owner 私有 vs 管理员全局模板？
6. 引用去重以 DOI 为主，arXiv/PubMed 如何映射？
7. 中文论文/人文社科数据源（CNKI 等）是否在范围内？版权/接口需单独评估。

---

## 14. 结论

- **有参考价值，但价值在"文献工作流产品化 + Skill 模板化"，不在"多装几个 Skill"**；
- 最优先做 **mcp:literature + mcp:zotero（只读）**，它们直接复用现有 literature/documents 与 N-13 工具层；
- Skill 应落成 **PromptVersion + AgentProfile + ToolAllowlist + EvalCases**，而不是新的运行时；
- 所有写操作/下载/批注必须 **ToolGrant + Approval + ToolCallRecord**；
- 先只读、再写回、最后关系图/科学工具，避免一次性把外部生态的风险引入主链路。
