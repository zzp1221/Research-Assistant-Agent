# Research Assistant Agent：项目评分与上下文交接

整理日期：2026-11-18。用途：让新聊天接续原聊天中的项目评分、审计发现和验证边界。

## 用户需求和当前状态

- 原始需求：从多个维度给本项目评分。
- 已完成：只读源码审阅、部分现有测试和临时审计探针；已经向用户报告综合评分与主要依据。
- 原审阅没有修改项目源码。本文件是后续整理的上下文交接材料。
- 用户随后在新工作树中新开聊天，发现新聊天不知道原对话和评分结论，因此整理本文件供新聊天主动读取。
- 本文件记录历史审阅结论，不应直接视为其他提交已通过验收；后续实现或修复应结合当前源码复核。

## 仓库与基线

- 原工作区：`D:\项目\Research Assistant agent`
- 原评分基线：`d4d2af1a5fda67fbd027f5d1e5cfe9116ace20fa`，原工作区分支为 `master`。
- 用户创建的新工作树：`C:\Users\ASUS\.codex\worktrees\2e14\Research Assistant agent`
- 2026-11-18 已通过 `git worktree list --porcelain` 和 `git rev-parse` 确认：`2e14` 存在、处于 detached HEAD，检出提交同为 `d4d2af1`。
- 上述核对确认了 Git 基线，没有在新工作树重新运行原测试。
- 创建日志中的 `Worktree ready at ...` 表示工作树创建成功；用户所说“读不到上下文”明确指新聊天不了解之前的对话和项目评分，不是缺少源码或项目运行时记忆故障。

## 已给用户的评分

十项等权平均为 **7.55/10**，展示为 **7.6/10**。这是基于抽样审阅和有限测试的工程评价，不是正式 benchmark。

| 维度 | 分数 | 核心依据 |
|---|---:|---|
| 功能完整度 | 8.5 | 规划、审阅、执行、审批、记忆、证据和报告形成闭环，支持 Web 与桌面 |
| 架构与任务编排 | 8.5 | DAG 校验、租约、执行轮次、预算、取消、重试具有实际实现 |
| 科研结果可信机制 | 8.5 | 冻结评测契约、隐藏 Holdout、产物溯源、确定性数值校验 |
| 代码可维护性 | 7.0 | 分层清楚，但多个核心文件超过千行，状态流转复杂 |
| 可靠性与恢复 | 7.0 | 调度边界较充分，持久层仍有一致性和 Windows 迁移缺陷 |
| 安全与隔离 | 6.0 | 有权限、审批、审计和容器限制，但凭据继承、隔离探测、依赖安全存在问题 |
| 测试与质量门禁 | 8.0 | 单元、组件、race、安全回归、视觉测试体系较全，仍有跨平台和集成缺口 |
| 前端与产品完成度 | 8.0 | 科研工作台功能完整，支持执行恢复、PDF、计划审阅，存在结果详情接线遗漏 |
| 性能与规模验证 | 6.5 | 有分包、资源预算和压测工具，多副本及长期运行验证有限 |
| 文档与交付 | 7.5 | 双语文档、架构图和验收记录丰富，跨平台失败及历史验证状态影响交付确定性 |

核心判断：项目有较完整的科研执行工作流，强项是任务治理和证据验收。最有价值的设计是区分“执行完成”与“研究结论成立”，对产物来源、成功执行、评测契约和结果语义进行约束。

## 主要发现及证据强度

以下路径均相对仓库根目录；行号对应 `d4d2af1` 基线。

### 已用临时探针或既有测试复核的问题

1. **计划存储浅拷贝。** `GetPlan` 返回对象中的嵌套 map 仍与内部状态共享。临时 Go overlay 探针修改 `Nodes[0].Inputs["metrics"].(map[string]any)["score"]` 后，再次读取内部 score 从 1 变为 999，未调用 `UpdatePlan`。位置：`research-assistant/backend/internal/store/memory_plan_store.go:228`、`:300`。`FilePlanStore` 复用同一克隆逻辑。未运行 race 复现，不能声称已经检测到数据竞争。
2. **持久化失败后暴露未提交状态。** `FilePlanStore.UpdatePlan` 先更新内存，再写 snapshot；临时 overlay 探针注入写盘错误后，方法返回 error，但 `GetPlan` 已读到未提交的新值。位置：`research-assistant/backend/internal/store/file_plan_store.go:132`。`SavePlan`、`AppendEventReturning` 的类似顺序仅作静态确认。
3. **Windows 旧日志迁移失败。** 加载旧 append-only 日志时通过 `defer Close` 延迟关闭文件，在返回前迁移并 Rename 原文件，触发 Windows 文件占用。既有测试 `TestFilePlanStoreMigratesLegacyAppendOnlyLog` 全包与单独复跑均失败。位置：同一文件 `:284`、`:362`、`:380`。影响旧格式升级路径，不能扩展成“全新安装无法运行”。
4. **Optimizer 数据库连接清理。** Windows 本机 Python Optimizer 8 项测试中 2 项在临时目录清理时因 SQLite 文件占用而出错。`research-assistant/research-optimizer/research_optimizer/store.py:22`、`:30` 等使用连接上下文管理器处理事务，但未显式关闭连接。测试结果不能描述为全部通过，也不能据此声称所有请求失败。

### 静态源码已确认或依赖审计已命中，未做攻击复现的问题

5. **Windows native 子进程继承内部 token。** `desktop/settings/settings.go:160` 起向 sandbox 注入 `SANDBOX_API_TOKEN`；`research-assistant/docker-sandbox/internal/engine/native/rawprocess_windows.go:90` 将全部父进程环境用于研究子进程，`:124` 起传给 Windows 进程创建接口。未执行读取凭据或绕审批攻击；不能断言离线 AppContainer 能连接 loopback。lowil 条件下控制面风险更大。
6. **隔离探测不能证明 AppContainer 安全。** `research-assistant/docker-sandbox/internal/engine/native/probe_windows.go:29` 用普通 `exec.CommandContext` 执行探针，未经过 AppContainer runner；`native/probe.go:17` 的安全项尝试写宿主 Public 文件、连接公网，而 `:34` 起把执行无异常记为通过。未运行这些有副作用的安全探针。lowil 降级在界面有告知，不应描述为静默降级。
7. **镜像白名单匹配过宽。** `research-assistant/docker-sandbox/internal/engine/docker_security.go:102` 起使用前缀及跨 registry 后缀匹配。白名单为 `python:3.11-bullseye` 时，`python:3.11-bullseye-evil`、`evil.example/python:3.11-bullseye` 均会被允许。实际运行仍取决于镜像可用性和运行时审批。
8. **PDF.js 已知高危依赖风险。** `research-assistant/frontend/package.json:34` 使用 `pdfjs-dist ^3.11.174`；`src/features/pdf-viewer/PdfPanel.tsx:55` 的参数未见 `isEvalSupported: false`。`npm audit --omit=dev --json` 命中 GHSA-wgrm-67xf-hhpq，公告涉及打开恶意 PDF 时执行 JavaScript，CVSS 8.8。未制作恶意 PDF 验证。原审计合计 36 项告警，含依赖链传播，不能视为 36 个独立可利用漏洞。公告：https://github.com/advisories/GHSA-wgrm-67xf-hhpq 。

### 产品与成熟度问题

9. **主执行图丢失 outcome_details。** `research-assistant/frontend/src/app/ResearchAssistantApp.tsx:567` 已读取结果详情，但 `:137` 调用 view model 时传入固定 `null`，导致 partial 的可选工作损失、缺少产物及结论限制无法完整展示。该链路为静态代码确认，原组件测试直接传 props，未覆盖上层接线。
10. **复杂度与规模边界。** `useResearchAssistantRuntime.ts` 1883 行，`PlanReviewPanel.tsx` 1612 行，`useResearchAssistantChatFlow.ts` 1091 行；文件存储在全局锁内克隆、序列化和同步写盘，偏向单实例、中小规模。不能据此声称已验证高并发或多副本部署。
11. **真实科研效果缺少统一对照。** `docs/test/2026-11-08-agent-quality-baseline.md:80` 明确说明真实模型科研质量、恢复成功率、记忆净收益等仍无可报告样本。现有离线判分器测试和小型示例不能推出真实科研任务成功率。

额外静态风险、尚待端到端验证：桌面完整启动生成新 backend token，默认身份 cookie 签名又从该 token 派生，可能影响完整退出再启动后的 owner 身份连续性。位置：`desktop/shell/shell.go:165`、`desktop/settings/settings.go:127` 起、`research-assistant/backend/internal/api/cookies.go:42`。不要将此写成已实际重启复现的缺陷。

## 原审阅实际验证范围

环境：Windows/amd64、Go 1.27.3，使用原工作区。以下是历史执行记录，本次交接没有重跑。

- 前端：21 项 Node 单元测试、58 文件中的 500 项 Vitest 组件测试全部通过；`npm run lint`、`npm run build`、`npm run check:bundle` 通过。构建有大 chunk 和 PDF.js eval 警告。
- 离线评测：37 条复杂度路由用例、4 条运行时规划用例通过；这不是 41 次真实模型科研任务。
- 后端：planner、recovery 通过；scheduler 核心验收/派发/终态/执行轮次/阻断/审批/预算/部分成功/恢复/提问边界测试通过；planner、scheduler、store、recovery 四包 `go vet` 通过。
- scheduler 全包另有 4 项 Windows 失败：2 项 CRLF 精确内容比对、2 项符号链接权限不足。属于测试夹具或环境适配问题，未证明 Git revision 固定功能失效。
- store 全包失败包括前述真实 Windows 旧日志迁移问题；两项临时 overlay 探针确认浅拷贝和写盘失败问题。探针不属于仓库已有测试，没有写入仓库。
- desktop：`go test ./... -count=1` 全部通过。
- docker-sandbox：6 项 Windows 测试失败，主要为路径分隔符、假 uv/WSL shell 脚本和 VM 环境断言；顶层和 remote 包通过。不能据此描述为生产沙箱全面失效。
- Python：paper-reproduction、intent_router 离线示例各 1 项通过；Optimizer 8 项中 2 项清理出错。
- 未运行完整后端测试、浏览器 E2E、长期浸泡或本轮真实付费模型科研任务；没有本轮 race 复核。
- 仓库 CI 声明了 race、覆盖率、安全回归、视觉和无障碍等门禁；声明这些流程不等于本轮实际执行了这些检查。

## 原建议的改进顺序

1. 优先处理内部凭据继承、隔离探测可信度、镜像匹配和 PDF.js 依赖风险。
2. 修复计划存储深拷贝、提交一致性、Windows 日志迁移和 SQLite 连接释放。
3. 修复结果详情接线与跨平台测试夹具。
4. 在固定输入、模型、环境和预算条件下补真实任务对照，衡量成功率、费用、恢复、记忆收益与延迟。
5. 在上述边界稳定后逐步拆分大型模块，再根据实际需求扩大并发或部署规模。

## 新聊天使用方式

新聊天先读取本文件，将其作为历史背景，再检查自己当前工作目录、分支、提交与用户最新要求。不要把历史测试结果称为当前工作树已经通过的测试，也不要仅因本文件列出了问题就宣称修复已经完成。
