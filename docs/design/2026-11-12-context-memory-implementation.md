# 上下文与记忆改进实施记录

- 日期：2026-11-12（Asia/Shanghai）
- 分支：`feature/memory`；先快进到本地最新 `master` 的 `d4d2af1`，再进行本次修改。
- 评审基线：[上下文与记忆系统评估](../review/2026-11-12-context-memory-review.md)。评审原文保留为基线。
- 状态：CM-01～05 的下述增量已实现；CM-06 已交付冻结评测工具和合成试测，真实科研长任务收益仍待验证。
- 后续容器实测：已于 11 月 13 日完成现有后端容器热更新、25 项真实接口断言、默认配置及跨重启验证，并修复文档页顶事实遗漏，详见 [容器验收记录](../test/2026-11-13-context-memory-container.md)。下文环境与回归描述保留首次源码验收的时间边界。

## 1. 以实际环境为准

本次检查时，本项目原有服务容器均已停止；运行中的 `pragent-*` 属于其他项目。因此验收使用临时容器挂载当前源码，没有把其他项目的运行状态当作本项目证据，也没有重启旧服务或修改现有业务数据卷。本次结果说明源码行为，不代表服务镜像已经部署更新。

用户原有的两份文档索引修改和未跟踪的评审文件均保留。另有一处原测试未指定文献源，默认源变更后不能命中测试自己的 Semantic Scholar mock；本次将测试输入显式固定为 Semantic Scholar + arXiv，没有修改文献业务默认值。

## 2. 实施映射

| 评审项 | 实际改进 | 验证与边界 |
|---|---|---|
| CM-01 资料角色 | 聊天 memory、历史召回、Wiki、文档均为普通 user 数据块，附来源、可信度、内容版本、保留策略；当前资料从持久历史中分离 | 捕获 Chat Completions / Responses 最终请求；当前检索替换后旧资料不继续注入。协议角色和数据边界不等于对所有模型的提示注入免疫证明 |
| CM-02 摘要内容 | 强制逐字完整原句/原行、原始来源内容校验；识别关键数值、否定、假设和显式更正，跨压缩保留原文锚点 | 反例测试与连续三次压缩回归；校验标记明确为 extractive grounding，不能宣称语义真实性验证 |
| CM-03 检索校准 | MMR 相关性归一化；数值、修订、实验条件、负结果差异不作为重复惩罚；固定双语别名；检索阶段诊断 | 同义改写、隔离和检索机制测试；仍是 BM25/实体/规则路径，未加入向量或语义重排 |
| CM-04 预算一致性 | 聊天和 DAG 共用 `ResolveContextBudget`，读取 owner settings 并受实际模型容量约束；召回、Wiki、文档共用动态池 | 小窗口、Wiki 占满、设置修改后下一次调用回归；最终协议预算检查仍由 modelresolver 执行 |
| CM-05 条件案例 | 按完整作用域生成待审案例汇编，保存问题、动作、环境、协议、结果、原文和来源；去重执行身份 | 成败条件、重复重放、私有作用域、真实 action ledger 字段回归；没有自动晋升通用方法 |
| CM-06 收益对照 | 冻结任务、隔离快照、轮换 A/B/C、逐样本留存失败、配对差异和标准误、可选真实模型 | 12 个合成科研事实场景；尚不是建议的 30～50 个真实研究执行长任务 |

## 3. 资料边界与预算

`contextwindow.Material` 把当前资料作为调用局部数据传递。JSON 转义使资料原文不能闭合分隔符或伪造外层元数据；资料中的命令不会成为 system/developer 消息。适配器从 schema 消息提取资料，再在最终普通消息中发送。直接注入的测试模型也接收相同的 user 数据块。

每块记录 `id/kind/source/trust/version/retention` 和估算 token。显式文档引用为 `verbatim`，必须完整保留；可重新检索的 memory/Wiki/召回为 `retrievable`，不足时整块舍弃。资料不会成为首条受保护的历史 turn。`context_report.materials` 记录最终选择；被舍弃的 memory/召回不再进入回答引用候选。

当前资料加入请求缓存身份，但不加入持久聊天历史身份。Responses 的 canonical 活动窗口也不直接保存当前资料；若服务端不透明压缩可能携带旧资料，资料版本变化时重建窗口。子级无分支调用清空外层资料，防止抽取等旁路调用继承当前检索。

预算顺序为：实际 prompt limit 减当前必需输入，分配 core 额度，再分配动态 retrieved 池；聊天召回、Wiki 与其文档逐项扣除，最后分配检索 memory。Wiki 的总 token 已包含文档，账目只扣一次。无模型配置的 store-only / 注入模型场景报告 `owner_settings_model_unresolved`，不伪造模型容量。`context_budget_report` 是检索分配报告，`context_report.materials` 是最终模型预检结果，应结合查看；包装及协议开销可能使最终选择更少。

## 4. 摘要的可靠性策略

摘要结构保持 `context.summary/v1`，但所有自然语言字段必须由原件支持。完整原句/原行比自由改写更保守：来源 ID 正确、文字却改了数字、单位、否定或条件，均会被拒绝；attempt 的动作与结果必须由同一来源支持。拒绝未知 JSON 字段，保存前规范化 JSON。对于可识别的实验/指标身份及显式事实坐标，后来更正阻止旧值作为当前 fact；不同实验不互相覆盖。

关键锚点每次从原始历史重建，保存原文、顺序、来源、状态及可识别的显式字段，包括实验、数据版本、划分、方法、配置、指标、聚合、单位、seed、事实身份和时间。旧摘要不充当新一轮的独立证据。旧缓存摘要缺少 grounding 标记时会重建。

摘要为空、截断、超目标、内容不受原件支持或携带过期事实时，记录具体失败码；有容量则保留原历史继续生成。摘要加原文锚点若无法容纳或没有缩小窗口，也拒绝提交。必需原文无法容纳时显式失败，不静默丢失关键证据。错误码经过筛选，不把服务商原始网络错误写入摘要诊断。

该策略没有解决开放语义理解：更正和状态识别是可解释规则，未覆盖所有自然语言；数值密集长历史的锚点可能很大。它优先拒绝不可靠压缩，不能承诺任意历史都能压缩成功。诊断中的 `extractive_original_sources/v1;semantic_truth_not_verified` 应按字面解释。

## 5. 检索与案例汇编

MMR 使用候选分数相对最大值的归一化结果，使相关性与 0～1 的相似度具有可比尺度。固定别名覆盖 OOM/显存不足、learning rate/学习率、batch size/批大小、negative result/负结果、macro-F1/宏平均 F1；新增路径仍执行 owner、项目、会话、计划、Agent、状态及修复兼容过滤。报告区分无记录、不可见、未召回、预算剔除和已选择，并报告排序/预算排除数。聊天召回显式排除刚保存的当前用户输入，避免自身召回。

案例汇编按完整 Scope 分组，不把会话或计划私有记录扩展到项目级。优先保留不同条件与结果，最多八个案例，额外案例数显式记录；每例保留最多 400 字原文并标记截断，完整来源可通过 `derived_from` 查询。独立执行必须有执行身份和实际证据；重复 execution ID 不重复计数。已验证 repair action 只标为单例验证。新 reflection 始终为 `TrustInferred / pending_review`，`generalization=not_established`；重复维护不会重复写入相同汇编，写入失败会返回错误。

这不是自动方法发现或历史数据迁移。既有 procedure 的 retrieved/adopted/executed/verified 阶段与兼容门槛继续保留；未实现对全部旧 reflection 的自动重新审查，也未把检索命中次数当作验证次数。

## 6. 本次验收

使用 `golang:1.27.3-bookworm`、只读仓库挂载和隔离 `/tmp` 数据目录，复用已有依赖缓存，关闭网络：

| 检查 | 结果 |
|---|---|
| 全后端 `go test -count=1 ./...` | PASS，包含 API、调度器、Wiki、Agent、memoryeval 和原集成测试 |
| 改动包及 CLI `go vet` | PASS |
| contextwindow / modelresolver / memory / memoryeval `go test -race -count=1` | PASS |
| 前端 TypeScript `tsc -p tsconfig.app.json --noEmit` | PASS |

本次没有运行浏览器 E2E、部署验收或多日浸泡测试。Go 1.27 下 sonic 提示使用 encoding/json 回退，不影响上述结果。

从仓库根目录复现全后端测试：

```powershell
$memoryRepoPath = (Get-Location).Path
docker run --rm --network none `
  --mount "type=bind,source=$memoryRepoPath,target=/repo,readonly" `
  --mount type=volume,source=ra-go-mod,target=/go/pkg/mod `
  --mount type=volume,source=ra-go-build,target=/root/.cache/go-build `
  -e GOTOOLCHAIN=local -e GOWORK=off -e GOPROXY=off `
  -e MEMORY_DIR=/tmp/memory -e WIKI_DIR=/tmp/wiki `
  -w /repo/research-assistant/backend golang:1.27.3-bookworm `
  go test -count=1 ./...
```

将最后一行替换为以下命令可复现专项检查：

```text
go vet ./internal/contextwindow ./internal/modelresolver ./internal/memory ./internal/memoryeval ./internal/api ./internal/scheduler ./cmd/memoryeval
go test -race -count=1 ./internal/contextwindow ./internal/modelresolver ./internal/memory ./internal/memoryeval
```

前端检查使用 `node:22-bookworm-slim`，只读挂载 frontend 并挂载已有 `ra-frontend-node-modules`，执行：

```text
node node_modules/typescript/bin/tsc -p tsconfig.app.json --noEmit --incremental --tsBuildInfoFile /tmp/memory-app.tsbuildinfo
```

## 7. 冻结评测与结果边界

工具说明、参数、冻结自定义题集及指标定义见 [memoryeval README](../../research-assistant/backend/cmd/memoryeval/README.md)。题集 SHA256：`3b409867e025e5c8373ba4a7ba31dadabf7082576a457681ab4dd5e81cc9a7be`。

最新 mock 固定 12 题、每组重复两次：A 20/24、B 20/24、C 24/24；48 次 B/C 摘要完成。A/B 的失败来自两题需要历史任务经验、当前历史没有答案；不能据此推断压缩优于完整历史。C−B 的任务级均值差为 0.1667，标准误 0.1124，小样本不支持真实任务收益结论。

真实配置为 `deepseek-v4.1-flash`，使用已有环境配置，密钥没有写入报告。首次 12 题单次试测：A 10/12、B 10/12、C 12/12；24 次摘要中 5 次完成、19 次因旧版通用 `summary_invalid` 诊断失败。后续前两题、较大输出额度的诊断试测，4 次摘要均为空并回退；再扩大额度的一题试测出现一次摘要通过和一次 `summary_superseded_fact` 拒绝。这些原报告保留原样，不用最新诊断名称改写已执行历史。

最终源码以 32,000 窗口、8,192 输出预留、0.1 触发比复测前两题：A/B/C 各 2/2；4 次摘要中 3 次完成，1 次因 `summary_unsupported_content` 被拒绝并回退原历史。结果及全部逐样本数据见本地 [验收报告](../../reports/context-memory-2026-11-12/REPORT.md)。报告目录默认被 Git 忽略；最新 mock 位于 `mock-final/report.json`，最终真实复测位于 `real-final/report.json`。费用未核验，保持 `null`。本次没有生成修改前后的完整真实模型因果对照；保留下来的修改前证据主要是评审和原回归。

后续应使用预先冻结的 30～50 个真实或接近真实的研究执行任务，隔离经验产生与评测数据，固定工具与契约，多次运行并记录失败、超限、权限违背、实际动作和验收结果。当前工具和试测建立了可复现起点，没有完成这项研究规模的收益验证。
