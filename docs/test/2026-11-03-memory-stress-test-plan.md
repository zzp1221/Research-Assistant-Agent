# Research Assistant Agent 记忆系统压力测试方案

> **版本**：v1.0（待执行稿）
> **日期**：2026-11-03（Asia/Shanghai）
> **适用基线**：master + feature/memory，产品版本 0.9.0；代码路径均相对仓库根目录
> **测试对象**：分层 Memory（M0–M3）与 Paper Wiki（W0–W3 / W2.5）
> **文档性质**：测试方案（Test Plan），不是测试报告；本文所有“预期结果”均为待验证假设，执行后须按第 10 节模板回填实测值
> **配套文件**：[用例 CSV](2026-11-03-memory-stress-test-cases.csv)（166 行，可直接导入 TestRail/Jira/飞书多维表格）
> **关联文档**：[Agent 记忆系统设计](../design/2026-11-01-agent-memory-system.md)、[Paper Wiki 设计](../design/2026-11-02-paper-wiki-memory.md)、[Paper Wiki 评审](../review/2026-11-02-paper-wiki-memory-review.md)、[记忆系统测试报告](../improvement-2026-11-01/MEMORY-TEST-REPORT.md)、[Paper Wiki 测试报告](../improvement-2026-11-02/TEST-REPORT.md)

---

## 摘要（给管理层）

- **测什么**：分层 Memory + Paper Wiki 在**长任务、长论文、交叉论文**三类高压场景下的正确性、隔离性、时效性、持久性、可重建性与规模表现。
- **怎么测**：166 条用例（P0 78 条）+ 4 套端到端剧本（72 小时长任务 / 320 页长论文 / 120 篇交叉论文 / 7 天浸泡）+ 10 个可证伪假设 + 40 条代码级风险。
- **怎么判**：12 类记忆错误（E1–E12）逐类检测；P0 全过 + 关键指标达标 + 无 S0/S1 才判“可发布”。
- **最大风险**：现有实现存在若干“静默出错”点（摘要只覆盖部分丢弃轮次、ContentHash 不含 paper_id、200 篇链接上限、中文 wiki 检索退化、锁失败回退内存库），本方案为每条都设计了可复现用例。
- **投入**：25 个工作日（2 机并行 15 天）；真实 LLM 约 4.2 万次、mock 约 21 万次；证据包约 100 GB。

---

## 目录

| 章节 | 主题 | 内容摘要 |
|---|---|---|
| [0](#0-结论先行这次压测到底在测什么) | 结论先行 | 三条战线、10 个可证伪假设、规模与交付物 |
| [1](#1-测试目标范围与判定框架) | 目标与判定 | G1–G6、12 类错误定义、Pass/Fail/Blocked 规则 |
| [2](#2-被测系统基线代码事实) | 代码基线 | 写入/检索/存储真相、开关矩阵、硬上限、R-01~R-40 |
| [3](#3-测试环境与工具链) | 环境与工具 | ENV-A/B/C、记录型代理、证据包、预检清单 |
| [4](#4-测试数据工程) | 数据工程 | 记忆针规范、S1/S2/S3 语料、数据账本 |
| [5](#5-用例总表) | 166 条用例 | LT/LP/CP/MB/LC/SC/PF/RB 八套用例表 |
| [6](#6-四套详细剧本) | 可执行剧本 | S1 72h 长任务、S2 320 页、S3 120 篇、S4 7 天浸泡 |
| [7](#7-指标评分与验收门槛) | 指标与门槛 | 13 项指标、评分脚本、Release Gate |
| [8](#8-执行计划与资源预算) | 排期与预算 | 25 个工作日、资源估算、里程碑 |
| [9](#9-缺陷管理与修复验证) | 缺陷管理 | S0–S3 分级、缺陷单模板、回归矩阵 |
| [10](#10-结果记录与报告模板) | 报告模板 | verdict.json、scorecard、最终报告结构 |
| [11](#11-附录) | 附录 A–G | 端点、字段、生成器接口、已知限制、证据索引 |
| [12](#12-附可执行的压测冒烟最小脚本建议p0-快用例) | CI 冒烟 | 12 条 30–45 分钟快用例 |
| [13](#13-附长任务长论文交叉论文的记忆错误典型现场用于培训执行人员) | 典型现场 | 10 个现场与 5 分钟确认法 |
| [14](#14-附术语表) | 术语表 | Needle/Injection/Point/Link 等 |
| [15](#15-附文档修订记录) | 修订记录 | 版本、日期、变更与同步要求 |

---

## 0. 结论先行：这次压测到底在测什么

### 0.1 一句话

> 这次不是验收“记忆功能能不能用”，而是破坏性验证“记忆在**长任务、长论文、交叉论文**三种压力下，会不会**记错、串台、丢失、过期、伪造引用或在重建后漂移**”。

功能测试回答的是“有没有”；压力测试回答的是“什么时候开始坏、坏在哪一层、坏得多安静”。记忆系统的错误往往是**静默的**：接口 200、计划 completed、回答语气自信，但事实已经张冠李戴。因此本方案把每一条被记住的事实都变成**可核对的 ground truth（记忆针）**，把每一次注入都变成**可回放的 prompt 证据**。

### 0.2 三条主战线

| 战线 | 压力是什么 | 典型失真 | 本次规模 |
|---|---|---|---|
| **长任务 Long Task** | 200+ 轮对话、8 条计划链、100+ 任务节点、多次重启与故障 | 上下文压缩后早期事实消失；跨计划经验错挂；任务重试产生重复/覆盖；进程重启后记忆回退到内存库 | 200 轮 × 3 次重启 × 8 计划 × 120 任务，约 7 天连续运行 |
| **长论文 Long Paper** | 300+ 页、40 节、120 条参考文献、表格/图/公式、OCR 与多语言 | 章节识别错误导致检索不到；引用页码错；quote 定位失败仍被注入；TLDR 只覆盖前 24k 字符；重建后 Point 丢失 | 320 页合成综述 + 3 篇真实论文，30 类问答，100 次 quote 核验 |
| **交叉论文 Cross-Paper** | 120 篇论文、300 条真实关系、同名/同 DOI/同数据集/同仓库、矛盾结论 | 同一论文分裂成多页；不同论文被合并；Link 张冠李戴；跨论文问答引用错论文；超过 200 篇后最早的论文不参与链接 | 120 篇 × 6 个主题簇 × 300 条 ground-truth 边，Link P/R/F1 全量评分 |

### 0.3 本次压测要回答的 10 个问题（H-01 ~ H-10）

这些问题来自对当前代码的静态审阅（证据见 2.6 与附录 E），是本方案设计的“可证伪假设”。每条假设都必须由至少一个 P0 用例给出“成立/不成立”的明确结论。

| 编号 | 假设 | 为什么可疑（代码事实） | 主用例 |
|---|---|---|---|
| H-01 | 长对话压缩后，被丢弃轮次里只有最前面约 5 对被摘入摘要，其余历史事实不可恢复 | ChatStore 摘要在被丢弃轮次中**只取最前面的 10 条 turn（约 5 对）**、每轮截 80 字（chat_store.go SummarizeChatTurns）；一旦被丢弃的 turn 超过 10 条，更晚的部分直接丢失 | LT-02、LT-03、S1-16 |
| H-02 | 任务执行器不读 per-owner 记忆设置，预算与开关在任务链路失效 | scheduler/executor.go 使用 memory.DefaultSettings()，而非 MemorySettings().Get(owner) | LT-13、RB-09 |
| H-03 | 三个入口对 shadow 的判定不一致：环境变量 shadow 管不住 chat，per-owner shadow 管不住 task | chat 只看 per-owner settings.Mode（不读环境 MEMORY_MODE）；plan 同时看环境 InjectionEnabled 与 per-owner settings；task 只看环境 InjectionEnabled 且用 DefaultSettings | LT-14、RB-02 |
| H-04 | 未传 project_id 时，项目级记忆会泄漏到其他项目 | matchQuery 仅当 q.ProjectID 非空才过滤 ProjectID | LT-16、SC-02 |
| H-05 | Wiki 的中文查询几乎不命中，回退到“最近 2 篇”，跨论文场景张冠李戴 | matchPapers 用 strings.Fields 空格分词后做子串匹配；无命中回退最近更新的论文 | LP-24、CP-25、S3-07 |
| H-06 | 论文数超过 200 后，最早的论文不参与链接构建与注入 | PaperStore.List 按 UpdatedAt 倒序返回且上限 200；BuildLinksForPaper / RebuildOwner / matchPapers 查询时均传 Limit: 200（API 分页本身可翻到 220） | CP-12、CP-27 |
| H-07 | 不同论文上文本相同的 Point 会被 ContentHash 去重到第一篇论文 | Record.ContentHash 不含 Tags/PaperID；同 owner/project/plan、同标题同文本即同哈希 | LP-07、MB-12 |
| H-08 | W3 的 TLDR 只读文档前 24000 字符，长论文后半部分事实永不进入 TLDR | documents.Excerpt 固定截断 MaxExcerptChars=24000 | LP-03、S2-04 |
| H-09 | 全量重建后 Link 版本漂移、LinkIDs 被 500 条截断，或 confirm/reject 状态改变 | writeLink 每次生成新 revision 并 supersede；refreshPaperLinks 只取前 500 | CP-06、CP-07、CP-14 |
| H-10 | kill -9 后 WAL 尾部损坏会丢弃后续已提交记录；文件锁失败会静默回退内存库 | replayWAL 遇首条不可解析即 break；NewFileStore 失败时 log + NewMemoryStore | LC-03、LC-04、LC-07 |

### 0.4 测试规模与交付物

| 项目 | 数量/规模 |
|---|---|
| 测试套件 | 8 类：LT 长任务、LP 长论文、CP 交叉论文、MB 记忆基础、LC 生命周期、SC 安全隔离、PF 性能规模、RB 回滚配置 |
| 用例总数 | **166 条**（P0 78 条、P1 77 条、P2 11 条） |
| 详细剧本 | 4 套（S1 长任务 72 小时、S2 长论文 320 页、S3 交叉论文 120 篇、S4 组合浸泡 7 天） |
| 记忆针 | 长任务 60 条、长论文 40 条、交叉论文 60 条，合计 160 条 ground truth |
| 预计周期 | 25 个工作日（可 2 机并行压缩到 15 个工作日） |
| 通过门槛 | 见 7.3：8 套 P0 全部 100% 通过、关键指标达标、无未关闭 S0/S1；任一 P0 失败即整体不通过 |
| 交付物 | 本方案 + 每次运行的 prompt 原始日志 + memory/wiki 导出 + 计分卡 + 缺陷单 + 最终报告 |
| 机器可读用例表 | [2026-11-03-memory-stress-test-cases.csv](2026-11-03-memory-stress-test-cases.csv)（166 行走查/导入 Jira/TestRail） |

### 0.5 三条铁律

1. **没有 ground truth 的记忆测试不算测试。** 每条被写入的事实必须有唯一 ID、唯一“针”、写入位置、期望生命周期；否则无法区分“模型没答对”和“记忆没给对”。
2. **先看 prompt，再看回答。** 回答错可能是模型推理问题，也可能是记忆注入问题。每个失败用例必须同时保留：请求 payload、服务端返回、mock/provider 的完整 messages、/api/memories/export 快照、/api/wiki/stats、后端日志。只看最终回答会误判。
3. **压力必须可复现。** 所有数据生成器使用固定 seed；所有测试 owner 使用 stress-<run_id>-<case> 命名；所有用例可在干净的数据目录上一条命令重放。

### 0.6 如何使用本文档

| 读者 | 建议阅读顺序 | 关注点 |
|---|---|---|
| 测试负责人 | 0 → 1.3 → 5 → 7 → 8 | 范围、判定、排期、门槛 |
| 执行工程师 | 0.2 → 2 → 3 → 4 → 5 → 6 | 代码事实、环境、数据、脚本 |
| 开发工程师 | 2.5 → 2.6 → 2.7 → 9 | 开关真相、硬上限、风险登记、修复验证 |
| 产品/评审 | 0 → 7.3 → 10.3 | 结论、门槛、报告结构 |

**文档边界：** 第 3–6 节中的 tools/stress/* 是**待建工具清单**（当前仓库只有 test/e2e/*.py 的既有脚本），执行前必须先实现 11.3 列出的最小脚本集并自测通过；本方案不包含这些脚本的实现代码。

---

## 1. 测试目标、范围与判定框架

### 1.1 测试目标

| 编号 | 目标 | 可观测的验收信号 |
|---|---|---|
| G1 | **正确性**：被写入的事实，在需要时能被正确检索、正确归属、正确引用 | 记忆针 Recall@k、Answer Accuracy、Citation Precision/Recall、Quote Locate Rate |
| G2 | **隔离性**：owner / project / session / plan 四层作用域零串扰 | 跨 owner 泄漏率 = 0；跨 project 泄漏率 = 0；跨 session 泄漏率 = 0 |
| G3 | **时效性**：事实更新后旧值不再作为当前值；历史仍可追溯 | 知识更新用例 100% 返回新值；as-of 查询能返回旧值且标 superseded |
| G4 | **持久性**：重启、崩溃、kill -9、锁竞争、磁盘压力下不丢已提交记忆 | 20 次故障注入后 committed record 丢失数 = 0；RTO < 5s |
| G5 | **可重建性**：删除 wiki 目录后 PaperPage/Point/Link/用户决策可重建，且不重复调用 LLM | 重建一致性 ≥ 99%；TLDR LLM 调用次数 = 0；confirm/reject 保留率 100% |
| G6 | **规模与成本**：在 1 万篇/10 万 Point 目标规模下检索 p95 < 50ms；注入 token 不超预算 | 见 7.3 性能门槛；PromptReport.WikiTokens 与 retrieved tokens 均达标 |

### 1.2 测试范围

**在范围内：**

- research-assistant/backend/internal/memory：类型、FileStore、索引、检索、渲染、脱敏、抽取、整合、ChatStore、Settings。
- research-assistant/backend/internal/wiki：类型、身份、PaperStore、Builder、Points、Links、PDF References、W3 摘要、检索渲染、设置。
- research-assistant/backend/internal/api：memory/wiki/chat/plan runtime、memory_sink、session runtime。
- research-assistant/backend/internal/scheduler：executor 注入、任务记录、计划终态记录、取消路径。
- research-assistant/backend/internal/prompts：core/retrieved/wiki section 的顺序与预算。
- research-assistant/backend/internal/documents：Outline、BM25、Quote、References、Excerpt。
- 前端只验证“记忆/论文面板可见、引用可点、确认/拒绝可操作”，不做 UI 像素级压测。

**不在范围内：**

- 模型本身的推理能力（同一 prompt 下答错，若 prompt 与记忆正确，则记为模型问题，不记记忆缺陷）。
- 沙箱执行正确性（除非它影响记忆写入，例如任务结果 100KB 截断）。
- 多副本部署、分布式一致性、OIDC/RBAC（当前设计明确单写进程、owner 隔离）。
- W4 embedding/图增强（未实现，默认关闭）。

### 1.3 “记忆出错”的操作定义与错误分类

本方案把“记忆出错”拆成 12 类，每类都有独立检测器。**一个用例可以同时触发多类错误，但缺陷单必须只归入最主要的一类**，避免重复计数。

**一个判定示例（LT-02）：**

~~~text
现象：第 100 轮问“我叫什么”，模型答“不知道”。
第 1 步 看 prompt：该轮 messages 中没有“小明” → 不是模型问题，是注入/检索问题。
第 2 步 看 export：memory 中“用户叫小明”仍 active → 不是写入丢失，是检索/预算问题。
第 3 步 看 summary：摘要只包含前 5 对，第 1 轮已在被丢弃的 15 对之后 → E1 丢失，根因 R-01。
结论：缺陷单 DEF-xxx，E1，R-01，P0；回归 LT-02 + LT-03。
~~~

| 编号 | 错误类型 | 操作定义 | 典型现象 | 检测器 | 关联用例 |
|---|---|---|---|---|---|
| E1 | **丢失 Amnesia** | 已成功写入且应保留的事实，在后续检索/注入/回答中不可得 | 回答“不知道”；prompt 中没有该记忆块；export 中记录缺失 | 针 Recall、export diff、prompt 断言 | LT-02/03/10、LC-01/03 |
| E2 | **错配 Misattribution** | 事实存在，但被挂到错误的人/项目/论文/计划/任务/时间 | 引用指向别的 paper_id；任务经验挂到别的 plan；更新写成另一条 | citation id 校验、scope 校验、paper_id 断言 | CP-01/02、LT-07 |
| E3 | **串扰 Cross-talk** | A 作用域的记忆出现在 B 作用域的检索结果或 prompt 中 | B 的聊天提到 A 的项目；跨 owner 搜索命中 | 隔离标记（每 owner/project 唯一哨兵词） | LT-15/16、SC-01/02/03 |
| E4 | **陈旧 Staleness** | 已被新值替代的旧事实仍作为“当前事实”被注入或回答 | BLEU 已改 29.0，仍答 27.3；旧 mock 策略仍生效 | 更新链用例、superseded 状态断言 | LT-04/05、MB-13 |
| E5 | **重复/膨胀 Duplication** | 同一事实被写成多条记录，或重复写入不断抬高 UseCount/UpdatedAt 挤占预算 | export 中同内容多条；检索结果前 5 条几乎一样 | ContentHash 去重检查、MMR 多样性、记录计数 | LT-08、LT-26、MB-12 |
| E6 | **伪引用 Fabricated Citation** | 回答引用了不存在的记录，或引用记录并不支持该结论，或 quote 在原文中不存在 | citation id 404；quote LocateQuote 失败；页码对不上 | 引用完整性扫描、quote 定位率 | LP-05/26、LT-23、CP-24 |
| E7 | **预算饥饿 Budget Starvation** | 相关记忆存在，但因 core/retrieved/wiki 预算或 per-kind 上限被挤出 prompt | PromptReport 显示 dropped；回答缺少关键约束 | PromptReport、注入块长度、针召回 | LT-12/22、PF-04 |
| E8 | **顺序/角色错乱 Ordering** | 记忆块位置错误、被当成 user 消息、或 wiki 抢在 retrieved 之前挤掉预算 | prompt 中 [wiki context] 出现在 [retrieved memory] 前；记忆被模型当指令执行 | prompt 结构断言、注入测试 | MB-06、SC-06/07、RB-04 |
| E9 | **重建漂移 Rebuild Drift** | 删除 wiki 或 memory 后重建，PaperPage/Point/Link/决策发生变化 | point_ids 变化；Link 丢失；TLDR 重新生成；confirm 变 pending | 重建前后快照 diff | CP-06、CP-07、CP-26、LP-17、LC-15 |
| E10 | **性能退化 Performance** | 规模上升后检索/写入/构建超出阈值，或长任务把请求拖死 | p95 超 50ms；compaction 时 3s 卡顿；chat 超时 | 延迟直方图、CPU/内存、WAL 指标 | PF-01~12 |
| E11 | **持久化失效 Durability** | 已返回 200 的写入在重启/崩溃后消失，或锁失败静默回退内存库 | 重启后 stats 归零；日志出现 falling back to in-memory store | 重启对账、WAL 回放、进程双开 | LC-01~08 |
| E12 | **安全/污染 Security** | 隐藏 benchmark、密钥、base64、prompt injection 通过记忆进入 prompt 或被检索 | hidden 论文出现在列表；记忆内容执行了“忽略指令” | 安全用例、注入扫描、export 审计 | SC-04~15 |

### 1.4 判定规则

- **Pass**：所有断言通过，且无新增 S1 及以上缺陷。
- **Fail**：任一 P0 断言失败，或出现新的 S0/S1 缺陷。
- **Blocked**：环境/依赖不满足（provider 不可用、磁盘不足、容器无法启动），必须记录 blocker 并在 24h 内升级；不得用“跳过”冒充通过。
- **Expected Fail（预期失败）**：已在附录 F“已知限制”中登记的行为，若实测与登记一致，记为 expected，不计入失败；若比登记更差，则升级为缺陷。
- **Flaky**：同一用例在相同数据上重跑 3 次结果不一致，记为 flaky，必须定位是模型随机性、并发还是数据未清理；不允许直接重跑到绿。

### 1.5 角色与职责

| 角色 | 职责 |
|---|---|
| 测试负责人 | 维护本方案、数据账本、执行排期、最终报告签字 |
| 执行工程师 | 按用例执行、保存证据包、提交缺陷单 |
| 开发值班 | 24h 内响应 S0/S1，提供修复分支与回滚方案 |
| 评审人 | 抽查 10% 用例的 ground truth 与证据，复核评分脚本 |

### 1.6 三类任务为什么会打穿记忆系统（失效机理 → 测试设计）

> 压力测试不能只堆轮次和页数，必须针对“记忆系统在什么机理下失效”来设计。下面把三类任务的失效机理与本文用例一一对应。

| 机理 | 说明 | 长任务表现 | 长论文表现 | 交叉论文表现 | 对应测试设计 |
|---|---|---|---|---|---|
| **有损压缩** | 上下文窗口满后必须摘要，摘要不可逆 | 第 1 轮姓名/偏好被挤出后消失 | 320 页无法整体入 prompt，只能检索片段 | 120 篇无法同时入 prompt | LT-01~03、LP-03、S2/S3 针 |
| **检索稀释** | 语料越大，正确记录越难进 top-k | 200 条 episode 中 1 条关键经验被淹没 | 40 节中只有 1 节含答案 | 120 篇中 5 篇同数据集易漏 | MB-02/05、LT-12、PF-01 |
| **实体消歧** | 同名/同题/同 DOI 变体需正确合并或区分 | 多项目同名“实验 1” | 同一论文多版本 PDF | 同名不同论文、同标题同年 | CP-01~03、LP-22、ADV-01~12 |
| **时间漂移** | 旧值必须以历史而非当前存在 | 基线改 3 次 | 论文修订版指标变化 | A 的结论被 B 推翻 | LT-04/05、MB-13、CP-10 |
| **作用域泄漏** | owner/project/session 过滤不严 | A 项目密钥出现在 B | 私人论文出现在共享项目 | 他人论文 Link 出现在自己页面 | LT-15/16、SC-01~03 |
| **预算饥饿** | core/retrieved/wiki 预算竞争 | pinned 太多挤掉关键约束 | wiki 块挤掉 retrieved | 多论文时第二篇整块丢弃 | MB-06~08、LT-22/24、PF-04 |
| **证据漂移** | 引用与原文脱节 | 引用已删除记录 | quote 不在原文、页码错 | Link evidence 指向错论文 | LP-05/26、CP-21/24 |
| **重建漂移** | 投影删除后重建，版本/状态变化 | 重启后摘要与引用变化 | 删 wiki 后 sections 丢失 | Link revision 漂移、confirm 丢失 | LC-01~04、LP-17、CP-06/07/26 |
| **污染** | 文档/网页中的指令或错误事实进入记忆 | 记忆里“忽略指令”被执行 | PDF 虚假 claim 被当事实 | A 的摘要污染 B 的 Link | SC-06~14、CP-10 |
| **规模拐点** | 超阈值后性能/正确性突变 | 2000 条 compaction 长尾 | 24000 字符摘要截断 | 200 篇链接截断、500 条 LinkIDs 截断 | PF-01~12、CP-12、LP-03 |

**设计原则：** 每个机理至少 2 条用例（1 正向 + 1 对抗），且必须有一条落在“规模边界”（199 vs 201 篇、1999 vs 2001 条、23999 vs 24001 字符）。


---

## 2. 被测系统基线（代码事实）

> 本节不是设计复述，而是**按当前代码核对后的基线**。所有测试预期都必须以此为准；设计与代码不一致时，以代码为准并在缺陷单中标注“设计-实现偏差”。

### 2.1 组件与数据流

~~~text
                      ┌──────────────────────────────┐
                      │ Chat / Plan / Task / Wiki API │
                      └──────────────┬───────────────┘
                                     │
        ┌────────────────────────────┼────────────────────────────┐
        ▼                            ▼                            ▼
  ChatSessionStore             memory.Store                  wiki.Builder
  (chat_sessions.json)     (memory.wal/snapshot)        (papers.jsonl + memory)
        │                            │                            │
        │  写入：轮次/摘要             │  写入：RecordPlan/Task/     │  写入：PaperPage
        │                            │  Trial/Document/User        │  Point/Link = memory.Record
        ▼                            ▼                            ▼
  prompt 组装：system → core → summary → history → retrieved → wiki → user
                                     │
                                     ▼
                          LLM Provider（真实或 mock）
                                     │
                                     ▼
                         回答 + citations + wiki_citations
~~~

### 2.2 写入路径基线（谁在什么时机写什么）

| 触发 | 代码入口 | 记录 ID 规则 | Kind | 内容上限 | 初始 Trust / Status |
|---|---|---|---|---|---|
| 计划终态（完成/失败/取消） | memory.RecordPlan（api/memory_sink.go 或 scheduler_control.go） | mem-plan-<planID> | episode | 800 字符 | verified / active |
| 计划终态且带论文元数据 | 同上 | mem-paper-<planID> | fact | 约 300 字符 | verified / active |
| 计划终态且带 EnvironmentManifest | 同上 | mem-env-<planID> | constraint | 约 200 字符 | verified / active |
| 计划终态且有缺失要求 | 同上 | mem-missing-<planID> | constraint | 500 字符 | verified / active |
| 计划终态且 outcome ≠ completed | 同上 | mem-negative-<planID> | negative | 300 字符 | verified / active |
| 任务失败 | memory.RecordTask | mem-task-<planID>-<taskID> | episode | 700 字符 | verified / active |
| 任务完成且 RunCount > 1 | memory.RecordTask | mem-proc-<planID>-<taskID> | procedure | 约 200 字符 | verified / active |
| AutoResearch trial rejected/failed/compromised | memory.RecordTrial | mem-trial-<planID>-<n> | negative | 700 字符 | verified / active |
| AutoResearch kept 且 SearchRuns ≥ 2 | memory.RecordTrial | mem-trial-proc-<planID>-<n> | procedure | 700 字符 | verified / active |
| AutoResearch 汇总指标 | memory.RecordTrial | mem-metric-<planID> | fact | 约 200 字符 | verified / active |
| 文档解析完成 | memory.RecordDocument | mem-doc-<documentID> | document | 800 字符；章节最多 20 个 | verified / active（无章节则 pending） |
| Wiki 自动 TLDR | wiki.persistTLDR | mem-wiki-tldr-<paperID> | fact | 800 字符 | inferred / pending_review |
| Wiki Point（paper_parse 摘要） | wiki.IngestPaperParse | point-summary-<hash16> | fact | 800 字符 | inferred / pending_review |
| Wiki Point（用户笔记） | wiki.CreatePoint | point-<key>-<hash16>，user_note 为 point-<uuid20> | fact | 800 字符 | user_explicit / active |
| Wiki Point（任务/负结果/程序） | wiki.TagPlanRecords / TagTaskRecord | 复用 mem-task-* / mem-proc-* / mem-negative-* | 对应 Kind | 原记录不变 | 原记录不变，仅追加 Tags |
| Wiki Link | wiki.writeLink | link-<key24>-r<revision> | fact | 500 字符 | verified / active 或 pending（confidence<0.6 或 source=llm） |
| 聊天轮次 | memory.ChatStore.Append | chat-<id> 会话 JSON | 非 memory.Record | 无单独上限（整文件重写） | 不属于记忆记录，不参与检索 |
| 用户显式记忆 | POST /api/memories | 自动 mem-<uuid20> | 任意合法 Kind | title 300 / content 8000 | user_explicit / active |
| 整合反思 | memory.Consolidator | mem-reflection-<owner>-<project> | reflection | 800 字符 | inferred / pending_review |

**关键事实：**

1. **聊天历史本身不是 memory.Record。** 它存在 chat_sessions.json，只有显式创建的 profile/fact/user_note 会进入 memory 检索。因此“聊天里说过”不等于“记忆里有”；长任务必须同时测试 ChatHistory 与 Memory 两条链。
2. **正常完成的任务不写 episode。** RecordTask 只在失败或 RunCount > 1 的完成时写；一次成功的普通任务只存在于 PlanGraph，不会进入记忆。测试“任务经验复用”时必须构造失败或重试。
3. **计划 episode 只保留前 650 字符的失败列表。** 超过约 3–4 个失败节点后，后续失败不会出现在计划记忆里；单任务失败记录另有 700 字符上限。

### 2.3 检索与注入路径基线

| 入口 | 检索函数 | 查询构造 | TopK | Core 预算 | Retrieved 预算 | Wiki 预算 | 注入顺序 |
|---|---|---|---|---|---|---|---|
| 聊天 /api/chat | memory.Store.Retrieve + wiki.RetrieveForTask | 当前消息 + 最近 3 条用户历史，截 2000 runes | settings.RetrievalTopK（默认 8） | settings.CoreBudgetTokens（默认 512） | settings.RetrievedBudgetTokens（默认 2000）− wiki tokens | 2% PromptLimit，clamp 300–1500，硬上限 3%，低于 150 不注入 | system → [core memory] → summary → history → [retrieved memory] → [wiki context] → user |
| 计划创建 /api/plan | memory.Store.Retrieve（collectPlanMemoryContext） | 用户 intent，截 2000 runes | settings.RetrievalTopK | settings.CoreBudgetTokens | **硬编码 800** | 不检索 wiki | 写入 ResearchContext.MemoryContext（截 1600 字符），再进 PlanContext 的“Memory context”段 |
| 任务执行 scheduler | memory.Store.Retrieve + wiki.RetrieveForTask | task.Name + task.Description 截 500 | memory.DefaultSettings().RetrievalTopK = 8 | DefaultSettings().CoreBudgetTokens = 512 | **硬编码 3000 − wikiTokens** | 2% 目标、clamp 300–1500、3% 硬上限、<150 不注入 | core_memory p1 → plan_context/task_instruction p2 → retrieved_memory p3 → wiki_context p3（retrieved 之后）→ task_inputs p4 → volatile p5 |

**预算细节：**

- BuildSectionsWithLimit 对 Priority ≤ 2 的 section 强制全量；若 mandatory 超过 limit，直接返回 ErrPromptBudgetExceeded，任务失败而不是静默截断。
- task_inputs 先用 limit − systemTokens 预渲染，再按真实剩余空间二次截断；因此 wiki/retrieved 的膨胀会先挤掉 task_inputs，而 task_inputs 往往承载用户输入。
- RenderContext 对第一个 paper block 单独做 truncateToTokenBudget；后续 block 只要总 token 超预算就**整块丢弃**，不是截断。因此多论文场景下，第二篇论文可能整篇消失。
- 聊天中 retrievedBudget = settings.RetrievedBudgetTokens − wikiCtx.Tokens；若 wiki 用满，retrieved 可能为 0。计划创建不检索 wiki，retrieved 预算硬编码 800；任务链路 retrieved 预算硬编码 3000 − wikiTokens，且不读 per-owner settings。

### 2.4 存储、去重与生命周期基线

| 主题 | 当前实现 | 压测含义 |
|---|---|---|
| Memory 存储 | 单目录下 memory.wal.jsonl + memory.snapshot.json + .memory.lock + settings.json（所有 owner 共用同一目录，靠记录 Scope 隔离） | WAL 每条 put 都 fsync；写入吞吐受磁盘影响；单文件随规模线性增长 |
| 去重 | ContentHash = sha256(kind + scope + normalized(title+content))；命中则 UseCount++、UpdatedAt=now，返回旧记录 | 同文本不同证据/不同论文会共用第一条；更新时间被抬高，影响 recency |
| 版本 | 同 ID 且内容变化：旧记录置 superseded + ValidTo + SupersededBy，新记录 Version+1 | 更新链可追溯；但 ID 由调用方决定，抽取器 ID 稳定 |
| 压缩 | 每 2000 条 WAL 全量写 snapshot，持有写锁 | 大规模写入时会出现周期性长尾延迟 |
| Wiki 存储 | 每 owner 一个 owners/<sha256(owner)[:16]>/，内含 papers.jsonl、papers.snapshot.json、aliases.json、.wiki.lock | 每 500 条 WAL 压缩；删除目录可重建 |
| 文件锁 | lockfile.Acquire(path, 3s)，PID 存活检测；失败时 NewFileStore 返回错误，API 层 log 后回退 NewMemoryStore() | **静默回退**：进程看似正常，重启后数据消失 |
| TTL/归档 | working 1 天、episode/constraint 90 天、fact/negative 180 天、procedure/document/reflection 365 天、profile 永不过期；仅 active 且非 pinned | 长任务跨 90 天会归档 episode；需要测试“归档后是否仍可 as-of 查询” |
| 反思 | 同一 owner+project 累计 ≥5 条 episode/negative 生成一条 reflection，pending_review、TrustInferred | 反思默认不注入（pending），不能作为“自动学习”验收 |
| 聊天窗口 | 20 轮 / 32000 token；超出部分生成确定性摘要，摘要不落盘（SetSummary 未被调用） | 摘要每次请求重算；只覆盖被丢弃轮次中最前面的 10 条 turn（约 5 对），每轮 80 字 |

### 2.5 配置开关矩阵（哪些是真的，哪些只是文档里写了）

| 变量 | 文档默认 | 代码实际 | 结论 |
|---|---|---|---|
| MEMORY_ENABLED | true | api/memory_runtime.go 读取；false 时 store=nil、不注入、不写 | ✅ 生效 |
| MEMORY_MODE | on | memory/extract.go Mode() 被 plan/task 使用；**chat 使用 per-owner Settings.Mode，不读环境变量** | ⚠️ 入口不一致 |
| MEMORY_DIR | 由 PLAN_STORE_PATH 推导 | 生效 | ✅ |
| MEMORY_CONSOLIDATE_INTERVAL_SECONDS | 空（关闭） | 生效，>0 启动后台 goroutine | ✅ |
| MEMORY_RETRIEVAL_TOPK | 8 | **无读取代码** | ❌ 文档写了但未接线 |
| MEMORY_CORE_BUDGET_TOKENS | 512 | **无读取代码** | ❌ |
| MEMORY_RETRIEVED_BUDGET_TOKENS | 2000 | **无读取代码** | ❌ |
| MEMORY_WRITE_MODE | deterministic/hybrid/llm | **无读取代码**，仅确定性抽取 | ❌ |
| MEMORY_EMBEDDINGS | off | **无实现** | ❌ |
| WIKI_ENABLED | true | 路由注册、Builder、注入均检查 | ✅ |
| WIKI_MODE | on | InjectionEnabled() 被 chat/task 检查；构建仍执行 | ✅（shadow=构建不注入） |
| WIKI_DIR | 推导 | 生效 | ✅ |
| WIKI_LLM_MODE | summary | summary/links/all 均接线到 summarize.go | ✅ |
| WIKI_LINK_MODE | explicit | **仅解析，无调用点** | ❌ 文档写了但未接线 |
| WIKI_PDF_REFS | false | pdfrefs.go 检查 | ✅ |
| WIKI_BUDGET_TOKENS | 空 | BudgetOverride() 仅被测试调用，BudgetTokens() 不读 | ❌ |
| PROMPT_CONTEXT_WINDOW / MAX_OUTPUT / SAFETY | 128k/8k/2k | 生效 | ✅ |

> **测试要求**：凡是标 ❌ 的开关，用例必须验证“设置后无效果”，并写入缺陷单（设计-实现偏差）；不得把它们当作已实现能力来设计预期。

### 2.6 硬上限与截断点总表（压测的“物理边界”）

| 组件 | 上限 | 代码位置/常量 | 压测影响 |
|---|---|---|---|
| 单条 memory title | 300 字符 | redact.go ValidateRecord | 超长标题被拒绝，整个写入失败 |
| 单条 memory content | 8000 字符 | redact.go | 超长内容被拒绝；抽取器通常 700–800 |
| SourceRef.Quote | 500 字符 | redact.go | 超长 quote 被拒绝；LLM 输出先截断 |
| 检索 BM25 候选 | 50 | retrieve.go SearchBM25(q.Text, 50) | 第 51 条起不参与 RRF |
| 实体召回候选 | 20 | retrieve.go EntityIDs 后截断 | 高重合实体下大量记录被忽略 |
| 时间词召回 | 10 | retrieve.go 最近 10 条 | “上次/之前”只覆盖最近 10 条 |
| MMR 每 Kind 上限 | 3 | mmrSelect perKindMax=3 | 最多 3 条 episode/negative，长任务经验可能被挤出 |
| Core 预算 | 默认 512 token | Settings | pinned 过多时低 importance 被静默截断 |
| Chat retrieved | 默认 2000 token | Settings | wiki 先扣，剩余可能为 0 |
| Plan retrieved | 800 token / 文本 1600 字符 | plan_memory.go | 长计划上下文被大幅压缩 |
| Task retrieved | 3000 − wikiTokens | executor.go | 硬编码，不读 per-owner |
| Wiki 注入 | 2% PromptLimit，clamp 300–1500，hard 3%，<150 不注入 | wiki/retrieve.go BudgetTokens | 128k 模型约 1500；8k 模型可能低于 150 不注入 |
| PaperPage 列表 | 200 | PaperStore.List（按 UpdatedAt 倒序，limit > 200 截断） | 最早的论文不参与链接/注入/重建 |
| PaperPage sections | 20 | builder.loadSections | 第 21 节起不显示 |
| PaperPage points | 2000 | RefreshPaper 查询 Limit 2000 | 超过后 point_ids 截断 |
| Link 列表 | 5000（查询）/ 500（refreshPaperLinks） | links.go | LinkIDs 只保留前 500 |
| 文档摘要输入 | 24000 字符 | documents.MaxExcerptChars | 长论文后半部分不进入 TLDR |
| 文档章节检测行 | 100 字符 | looksLikeSectionHeading | 超长标题不被识别 |
| 聊天窗口 | 20 轮 / 32000 token | chat_runtime.go ContextTurns | 超出后摘要 |
| 聊天摘要 | 被丢弃轮次中前 10 条 turn × 80 字 | SummarizeChatTurns | 更晚的、被压缩掉的历史信息永久丢失 |
| 计划 episode | 800 字符；失败列表约 650 字符 | extract.go | 长计划只保留前几个失败 |
| 任务 episode | 700 字符 | extract.go | 长错误栈被截断 |
| 文档记忆 | 800 字符；章节 20 个 | extract_document.go | 长文档章节图不全 |
| Wiki Link 构建 | 单实体 500 候选；单论文 200 候选 | links.go | 高被引论文/大数据集截断 |
| Wiki 构建 | 全量仅 200 篇 | RebuildOwner | 10000 篇目标无法通过该 API 全量重建 |
| Wiki 构建并发 | 全局 1 个任务 | api/wiki.go wikiBuildState.running | 跨 owner 互斥，B 用户被 A 阻塞 |
| Wiki build job 生命周期 | 进程内 map，无持久化、无 TTL | api/wiki.go | 重启后 job 查询 404；长期运行内存增长 |

### 2.7 代码级风险登记（R-01 ~ R-40）

> 这些是静态审阅发现的“高概率出错点”，不是已确认缺陷。每条都必须由用例证实或证伪；确认后转为缺陷单。

| 编号 | 风险 | 证据（文件/函数） | 影响 | 对应用例 |
|---|---|---|---|---|
| R-01 | 聊天摘要只取被丢弃轮次的**最前面 10 条 turn**（约 5 对），每轮 80 字；被丢弃的其余 turn 不进摘要 | memory/chat_store.go SummarizeChatTurns | 长对话中期事实永久丢失 | LT-02/03 |
| R-02 | 摘要不持久化（SetSummary 无调用点） | memory/chat_store.go:143 | 每次请求重算；重启后摘要不保留 | LT-10、LC-01 |
| R-03 | ChatStore 每次 Append 重写整个 JSON | memory/chat_store.go persistLocked | 会话数大时写放大、卡顿 | LT-29、PF-09 |
| R-04 | 任务链路使用 DefaultSettings | scheduler/executor.go:331 | per-owner topK/budget/开关失效 | LT-13、RB-09 |
| R-05 | 聊天不读环境 MEMORY_MODE=shadow，只读 per-owner Settings.Mode | api/chat_runtime.go:103-108 | 用环境变量做 shadow 回滚时，聊天仍然注入记忆 | LT-14、RB-02 |
| R-06 | task 不读 per-owner settings（用 DefaultSettings），因此单用户 shadow 对任务链路无效；plan 则同时看环境与 per-owner | plan_memory.go:93 / scheduler/executor.go:331 | 单用户 shadow 只对 chat/plan 有效，任务仍注入 | LT-14、RB-02 |
| R-07 | project_id 为空时项目记忆不过滤；chat 的 projectID 只取 payload.ProjectID，不继承 session 的 ProjectID；且 q.PlanID 为空时 plan-scoped 记录也不受 PlanID 过滤 | memory/retrieve.go matchQuery（80-91 行）；api/chat_runtime.go:46-56 | 跨项目串扰：带 session 但未传 project_id 时，项目 A 的计划 episode 可出现在项目 B 的聊天里 | LT-16、SC-02 |
| R-08 | session_id 为空时 session 记忆不过滤 | 同上 | 跨会话串扰（若记录带 session） | LT-15、SC-03 |
| R-09 | 全局记忆 scopeWeight 固定 0.7，仍可能与项目记忆竞争 | retrieve.go scopeWeight | 旧全局事实压过当前项目事实 | MB-11、LT-16 |
| R-10 | MMR 每 Kind 最多 3 条 | retrieve.go mmrSelect | 长任务 episode 被挤出 | LT-12、MB-06 |
| R-11 | BM25 候选只取 50 | retrieve.go:227 | 高重复语料下正确记录进不了候选 | MB-02、PF-01 |
| R-12 | Core 预算只渲染 pinned；超预算静默截断 | retrieve.go:367-384 | 重要 pinned 事实丢失且无告警 | LT-22、MB-07 |
| R-13 | 内容哈希不含 Refs/Evidence/Tags | memory/types.go ContentHash | 同文本不同证据共用记录；第二篇论文 point 丢失 | MB-12、LP-07 |
| R-14 | 重复 Put 返回旧记录且不更新 Refs/Attributes | memory/store.go:219-231 | 第一次写入的元数据“赢”，后续修正无效 | MB-12 |
| R-15 | List 只能 Limit，无 offset | memory/store.go List / Query 无 offset | 无法服务端分页；>5000 后截断 | CP-12、PF-07 |
| R-16 | 文件锁失败回退内存库 | api/memory_runtime.go:90-94 | 双进程/锁残留时数据静默分裂 | LC-07、LC-08 |
| R-17 | WAL 首条坏行即停止回放 | memory/store.go replayWAL | 崩溃后尾部已提交记录丢失 | LC-03/04 |
| R-18 | 压缩持有写锁写全量 snapshot | memory/store.go compactLocked | 大库周期性卡顿 | PF-03 |
| R-19 | Wiki 构建全局单任务 | api/wiki.go wikiBuildState | 跨 owner 阻塞；job 重启丢失 | CP-26、RB-10 |
| R-20 | Wiki 重建只处理最近更新的 200 篇 | wiki/builder.go RebuildOwner（List 按 UpdatedAt 倒序 Limit 200） | 规模 >200 时最早论文不重建 | CP-12/26/30 |
| R-21 | W3 TLDR 只读前 24000 字符 | documents.Excerpt | 长论文后半事实不进 TLDR | LP-03 |
| R-22 | matchPapers 用空格分词 | wiki/retrieve.go:99-106 | 中文查询不命中；回退最近论文 | LP-24、CP-25 |
| R-23 | matchPapers 无命中回退最近 2 篇 | wiki/retrieve.go:124-135 | 无关论文被注入，张冠李戴 | CP-25 |
| R-24 | Wiki 预算忽略 WIKI_BUDGET_TOKENS | wiki/retrieve.go BudgetTokens | 配置无效 | RB-07 |
| R-25 | WIKI_LINK_MODE 无调用点 | wiki/settings.go LinkMode | 配置无效，无法做 explicit/hybrid 消融 | RB-06 |
| R-26 | writeLink 每次候选都扫描 5000 条 Link | links.go linksForKey | 大规模链接构建 O(n²) | CP-13、PF-06 |
| R-27 | refreshPaperLinks 只取 500 条 | links.go:366 | LinkIDs 截断，UI/注入少链接 | CP-06、CP-14、CP-26 |
| R-28 | 实体候选每实体 500、每论文 200 | links.go | 高被引论文链接缺失 | CP-11 |
| R-29 | 同标题同年不同论文会被合并（无作者校验） | wiki/identity.go Resolve | 假合并、内容串页 | CP-02/03 |
| R-30 | 中文论文标题/摘要的 wiki 匹配无 CJK 分词 | wiki/retrieve.go | 中文项目问答退化 | LP-11/24 |
| R-31 | ScanInjection 只扫描 Refs.Quote | memory/redact.go:90 | 记忆正文中的注入不被降级 | SC-06 |
| R-32 | Attributes 只扫描字符串值，不扫描嵌套结构 | memory/redact.go:96-105 | 嵌套 hidden/base64 可绕过 | SC-04 |
| R-33 | 任务执行失败（runner 返回 error）不触发 Wiki.RefreshForTask | scheduler/executor.go:357-372 | 失败任务的 Point 不挂论文 | LT-11、CP-23 |
| R-34 | 计划中途崩溃不写 episode（只在终态写） | scheduler_execute.go / scheduler_control.go | 长任务中断后经验丢失 | LT-10 |
| R-35 | 取消路径依赖 memorySink 是否装配；直接调用 memory.RecordPlan 时不刷新 Wiki | scheduler_control.go:71-73；apiMemorySink.RecordPlan 内部会 tagAndRefreshPlan | 若 sink 未装配或替换，取消计划的 negative Point 不挂论文 | LT-09、CP-23 |
| R-36 | 客户端传 messages 数组时绕过服务端摘要，可能重复历史 | api/chat_runtime.go:71-83、161-172 | 长对话 prompt 膨胀/重复 | LT-29 |
| R-37 | per-owner settings.Enabled=false 只跳过 memory 块，wiki 上下文仍按独立开关注入 | chat_runtime.go:102-152 | 用户关闭记忆后，聊天里仍出现论文上下文 | RB-09 |
| R-38 | MEMORY_ENABLED=false 时 WikiBuilder 仍用 nil MemoryStore | api/wiki_runtime.go:73 | Wiki 可建页但 Point 写入失败 | RB-01/03 |
| R-39 | Wiki build job 无 TTL、无上限 | api/wiki.go wikiBuildState.jobs | 长期压测内存增长 | PF-08 |
| R-40 | PaperStore.List 的 limit 上限 200 对所有调用者生效 | wiki/store.go:325-327 | 200 篇以上分页正常，但链接/重建只看最近的 200 篇 | CP-12/27 |

---

## 3. 测试环境与工具链

### 3.1 环境拓扑

压测必须同时覆盖“桌面形态”和“服务端形态”，因为两者的文件锁、路径、进程模型完全不同。

~~~text
┌─────────────────────────── ENV-A：Windows 11 桌面形态 ───────────────────────────┐
│ ResearchAssistant.exe + backend.exe + sandbox.exe（native provider）              │
│ 用途：文件锁/DPAPI/单实例/托盘/长路径；LC、RB 套件                                 │
└──────────────────────────────────────────────────────────────────────────────────┘
┌─────────────────────────── ENV-B：Linux Compose 形态 ────────────────────────────┐
│ backend:18080  frontend:15173  sandbox:8082  intent  optimizer                   │
│ 用途：API 全链路、真实 SSE、前端引用面板；LT、LP、CP、SC 套件                       │
└──────────────────────────────────────────────────────────────────────────────────┘
┌─────────────────────────── ENV-C：性能压测机 ────────────────────────────────────┐
│ 8 vCPU / 16 GB / NVMe SSD / Docker；仅 backend 单进程，无前端                       │
│ 用途：PF 套件、1 万篇/10 万 Point 规模                                             │
└──────────────────────────────────────────────────────────────────────────────────┘
                          │
                          ▼
        ┌─────────────────────────────────────────┐
        │ 记录型代理（Recording Proxy，:19090）     │
        │ 透明转发 OpenAI-compatible 请求，逐条落盘  │
        │ prompts.jsonl：完整 messages + 时间戳      │
        └─────────────────────────────────────────┘
                          │
          ┌───────────────┴───────────────┐
          ▼                               ▼
   Mock Provider（确定性）          真实 Provider（deepseek-v4.1-flash）
   用于断言注入结构、性能、回归       用于回答质量、引用、拒答
~~~

**为什么必须有记录型代理：** 大量“记忆错误”其实是“注入正确但模型答错”或“注入错误但模型猜对”。只有拿到完整 messages，才能把责任归到正确的一层。代理必须原样转发，不得改写请求；记录文件按 case_id 分目录。

### 3.2 环境变量基线

| 变量 | 基线值 | 压测变体 | 说明 |
|---|---|---|---|
| APP_ADDR | :18080 | 每个 case 独立端口 18100+ | 避免端口冲突 |
| APP_DATA_DIR | /stress/<run_id>/data | — | 每 run 独立 |
| PLAN_STORE_PATH | <APP_DATA_DIR>/plans.json | — | memory/wiki 目录默认由此推导；不设置时默认落到 apppaths.DataDir()/memory 与 /wiki |
| MEMORY_DIR | <APP_DATA_DIR>/memory | 显式设置，便于单独删除 | — |
| WIKI_DIR | <APP_DATA_DIR>/wiki | 显式设置，便于单独删除 | — |
| MEMORY_ENABLED | true | false（RB-01） | — |
| MEMORY_MODE | on | shadow / off（RB-02、LT-14） | 注意入口差异 |
| WIKI_ENABLED | true | false（RB-03） | — |
| WIKI_MODE | on | shadow（RB-04） | — |
| WIKI_LLM_MODE | summary | off / links / all（RB-05、CP-10） | — |
| WIKI_PDF_REFS | true（压测期） | false（LP-16 对照） | 默认关闭，压测显式打开 |
| PROMPT_CONTEXT_WINDOW | 128000 | 8000（小模型预算用例） | — |
| PROMPT_MAX_OUTPUT_TOKENS | 8192 | — | — |
| PROMPT_SAFETY_TOKENS | 2000 | — | — |
| PROMPT_CONTEXT_V2 | true | false（对照） | — |
| BENCHMARK_PRIVATE_ROOT | <APP_DATA_DIR>/private | — | 安全用例 |
| REQUIRE_API_AUTH | false（内网压测） | true（SC 套件） | — |
| ALLOW_LEGACY_USER_HEADER | true | — | 便于按 owner 注入 |
| GIN_MODE | release | — | 避免调试日志拖慢 |
| MEMORY_CONSOLIDATE_INTERVAL_SECONDS | 空 | 60（LT-18） | 默认关闭 |

### 3.3 测试账号与目录

| Owner ID | 用途 | 说明 |
|---|---|---|
| stress-<run>-main | 主用户，LT/LP/CP 正向用例 | 请求头 X-User-Id；与所有 owner 共用同一 APP_DATA_DIR（memory 按 Scope.OwnerID 隔离） |
| stress-<run>-alice | 跨 owner 用例 A | 同上，同一 backend 进程 |
| stress-<run>-bob | 跨 owner 用例 B | 同上，同一 backend 进程 |
| stress-<run>-projA / projB | 跨项目用例 | 同一 owner 下的两个 project_id；chat 需显式传 project_id |
| stress-<run>-perf | 性能套件 | 独立 backend + 独立 APP_DATA_DIR（data-perf/），避免与功能用例互相干扰 |

> **注意：** 跨 owner 用例必须在**同一个 backend 进程、同一个 APP_DATA_DIR** 下用不同 X-User-Id 验证，才能测出 store 层隔离；只有锁竞争/双开用例（LC-07、RB-12）才启动第二个进程。所有 owner 在 run 结束后由清理脚本统一删除；禁止复用上一次 run 的 owner，否则统计不可比。

### 3.4 工具链

| 工具 | 版本/来源 | 用途 |
|---|---|---|
| Go | 1.27.3（与仓库一致） | 构建后端二进制 |
| Python | 3.11（容器内） | 生成 PDF、调用 API、评分脚本 |
| Node.js | 22 | 前端 E2E、SSE 校验 |
| jq | 1.7+ | JSON 断言、diff |
| k6 或 hey | 最新稳定 | 并发压测（PF-11） |
| pprof | Go 自带 net/http/pprof | 性能剖析（PF-03 长尾） |
| 记录型代理 | 自研 Python 脚本 | 捕获完整 prompt |
| PDF 生成 | reportlab（Python） | 320 页合成论文 |
| 评分脚本 | 自研 Python | 针召回、引用、Link P/R/F1 |
| 快照工具 | tar + sha256sum | 证据包封存 |

**记录型代理的最小要求：**

1. 监听 19090，转发到真实 provider 或 mock；返回体原样透传。
2. 每条请求写入 prompts.jsonl：case_id、turn_index、model、messages 全量、max_tokens、timestamp。
3. 每条响应写入 responses.jsonl：status、usage、content、finish_reason。
4. 不得缓存、不得重试、不得改写 messages；网络错误原样返回 502。
5. 记录文件按 run_id/case_id 分目录，避免单文件过大。

### 3.5 观测与取证

每个用例执行后自动生成证据包：

~~~text
evidence/<run_id>/<case_id>/
├── case.json                 # 用例定义、seed、开始/结束时间
├── requests.jsonl            # 对 backend 的 HTTP 请求
├── responses.jsonl           # backend 响应（含 citations）
├── prompts.jsonl             # 记录型代理捕获的完整 messages
├── memory_export.json        # GET /api/memories/export
├── wiki_stats.json           # GET /api/wiki/stats
├── wiki_papers.json          # GET /api/wiki/papers?limit=200
├── prompt_report.json        # 从任务/事件中提取的 PromptReport
├── backend.log               # 后端日志尾部 5MB
├── metrics.json              # 延迟、token、CPU、RSS
└── verdict.json              # 每条断言的 pass/fail 与证据指针
~~~

**取证原则：**

- 失败用例必须额外保存“失败时刻的完整 20 行上下文”（前后各 10 条请求）。
- 涉及引用错误时，必须保存 citations 中每个 memory_id 对应的 memory 记录快照；记录不存在时保存 404 响应。
- 涉及 wiki 时，必须保存 paper_id → point_ids → link_ids 的完整闭包。

### 3.6 环境预检清单

执行前逐项打勾，任何一项不满足则不得开始计时：

- [ ] backend 二进制 git commit 与基线一致（记录 git rev-parse HEAD）。
- [ ] go test ./... 全绿；前端 npm test 全绿。
- [ ] 真实 provider 可用（发 1 条 8 token 请求成功）。
- [ ] mock provider 可用且记录文件可写。
- [ ] 磁盘剩余 ≥ 50 GB（1 万篇/10 万 Point 与证据包）。
- [ ] 内存 ≥ 16 GB，swap 关闭或 ≥ 8 GB。
- [ ] 时区统一 Asia/Shanghai，NTP 同步。
- [ ] 所有数据目录为空或已归档。
- [ ] 记录型代理记录路径可写且已清空。
- [ ] 压测脚本版本与数据账本 sha256 记录一致。

---

## 4. 测试数据工程

### 4.1 记忆针（Needle）规范

每条记忆针是一个 JSON 对象，写入 data/needles/<suite>.jsonl，并由生成器保证唯一：

~~~json
{
  "needle_id": "LT-N001",
  "suite": "LT",
  "owner": "stress-r1-main",
  "project": "proj-attention",
  "session": "sess-main",
  "plan": "plan-A",
  "kind": "fact",
  "title": "项目基线 BLEU",
  "content": "WMT14 EN-DE 基线 BLEU=27.3（seed=42）",
  "write_path": "api_memory",
  "expected_query": "基线 BLEU 是多少",
  "expected_answer_contains": ["27.3"],
  "expected_citation_title": "项目基线 BLEU",
  "valid_from": "2026-11-03T09:00:00+08:00",
  "ttl_days": 180,
  "supersedes": null,
  "adversarial_tag": null,
  "notes": "第 1 轮写入；第 100 轮后必须仍能命中"
}
~~~

**针的六种写入路径（必须全覆盖）：**

| write_path | 写入方式 | 验证点 |
|---|---|---|
| api_memory | POST /api/memories | 用户显式记忆、pinned/core |
| chat_turn | POST /api/chat 自然语言 | 聊天历史 + 摘要链路 |
| plan_outcome | 执行计划后 RecordPlan | 计划 episode/fact/constraint/negative |
| task_outcome | 任务失败/重试后 RecordTask | 任务 episode/procedure |
| document_parse | 上传 PDF + paper_parse | 文档记忆 |
| wiki_point | POST /api/wiki/papers/:id/points 或 W3 自动摘要 | Point 与引用 |

**针的生命周期断言：**

- 写入后立即 export，确认记录存在且字段正确（created 检查）。
- 在 T+1 轮、T+50 轮、T+150 轮、T+200 轮分别检索一次（recall 检查）。
- 在任意时刻删除/更新后，旧针必须变为 superseded 或 deleted（lifecycle 检查）。
- 每个针必须至少被一次回答引用（citation 检查），否则无法证明它参与了推理。

### 4.2 长任务语料（S1）

**200 轮对话剧本结构：**

| 阶段 | 轮次 | 内容 | 针数量 | 目的 |
|---|---|---|---|---|
| P1 建立 | 1–40 | 用户画像、项目背景、论文、环境、基线、失败史 | 20 | 建立 core/retrieved 基线 |
| P2 切换 | 41–90 | 切换 3 个项目，每个项目 15 轮 | 15 | 测试 project scope 与串扰 |
| P3 冲突 | 91–140 | 同一事实多次更新、用户纠正、旧值回滚 | 10 | 测试 supersede/冲突 |
| P4 噪声 | 141–180 | 大量无关问答、长代码粘贴、工具输出 | 5 | 测试预算与去重 |
| P5 回忆 | 181–200 | 逐条询问 P1–P4 的针 | 10 | 测试长期保持与引用 |

> 合计 20+15+10+5+10 = **60 条针**；其中 P1 的 20 条是“是否还记得”的核心集合，P2–P5 的 40 条覆盖切换、冲突、噪声与引用。

**8 条计划链（每条 10–20 节点，共约 120 任务）：**

| 计划 | 意图 | 节点数 | 失败注入 | 期望记忆产物 |
|---|---|---|---|---|
| A1 | Attention 复现 | 15 | 第 5 节点依赖冲突 | episode + negative + fact |
| A2 | 基于 A1 的消融 | 12 | 无 | episode + 父子链 |
| A3 | BERT 复现（同项目） | 14 | 第 3 节点 CUDA OOM | negative + constraint |
| B1 | RAG 评测（项目 B） | 16 | 第 7 节点数据集缺失 | episode + missing |
| B2 | 基于 B1 的修复 | 10 | 第 2 节点重试成功 | procedure |
| C1 | GraphRAG 对比（项目 C） | 18 | 无 | 10 个 result point |
| C2 | 基于 C1 的扩展 | 20 | 第 11 节点超时 | negative |
| D1 | 跨项目复用 A1 经验 | 15 | 无 | 应命中 A1 的 negative/procedure |

**在关键轮次执行故障动作**（模拟真实长任务）：

1. 第 20 轮：SIGTERM 优雅重启。
2. 第 60 轮：kill -9 强杀。
3. 第 100 轮：锁文件被占用 3 秒。
4. 第 140 轮：磁盘临时写满 30 秒。
5. 第 180 轮：provider 超时 2 次。

### 4.3 长论文语料（S2）

**合成综述《Neural Retrieval: A Survey》规格：**

| 维度 | 规格 | 生成方式 |
|---|---|---|
| 页数 | 320 页 | 8 部分 × 5 章 × 8 页 |
| 章节 | 40 个二级标题 + 8 个一级标题 | 每章固定标题，包含中英混排 |
| 参考文献 | 120 条 | 60 条真实 DOI/arXiv + 60 条虚构但格式合法 |
| 表格 | 30 个，3–8 行 × 3–6 列 | 含指标、数据集、消融 |
| 图 | 24 个，含图注 | 图注写入正文，供检索 |
| 公式 | 60 个 | LaTeX 文本，测试特殊字符 |
| 页眉页脚 | 每页重复 | 测试噪声对 BM25 的影响 |
| 错字/OCR | 5% 页面注入 OCR 错字 | 测试 quote 容错 |
| 语言 | 中英各 50% | 测试 CJK 分词 |

**真实论文（作为对照组）：**

| 论文 | 来源 | 页数 | 用途 |
|---|---|---|---|
| Attention Is All You Need | reports/fixtures/attention-is-all-you-need.pdf | 15 | 已知 ground truth |
| Apriori 1994 | reports/eval-2026-10-24/fixtures/apriori-1994.pdf | 10 | 经典算法 |
| 自选 2024–2026 综述 | 用户提供或 arXiv 下载 | 40+ | 长文真实排版 |

**40 条论文针分布：**

| 类型 | 数量 | 示例 |
|---|---|---|
| 章节事实 | 10 | “第 4.2 节提出什么方法” |
| 指标 | 8 | “Table 3 的最佳 F1” |
| 引用关系 | 6 | “本文引用 [42] 的哪篇论文” |
| 局限 | 5 | “作者自述的三个局限” |
| 公式 | 3 | “公式 (7) 的损失函数” |
| 页级定位 | 5 | “第 187 页说了什么” |
| 图注 | 3 | “Figure 12 的图注” |

### 4.4 交叉论文图谱（S3）

**6 个主题簇（每簇 20 篇，共 120 篇）：**

| 簇 | 主题 | 论文数 | 关系密度 |
|---|---|---|---|
| C1 | Transformer 与 LLM | 20 | 高（每篇 5+ cites） |
| C2 | RAG | 20 | 高 |
| C3 | GraphRAG / 知识图谱 | 20 | 中 |
| C4 | Agent Memory | 20 | 中 |
| C5 | Benchmark / Evaluation | 20 | 中 |
| C6 | Multimodal Retrieval | 20 | 低 |

**300 条 ground-truth 边：**

| 类型 | 数量 | 说明 |
|---|---|---|
| cites | 180 | 有向，A → B |
| extends | 40 | 有向，A 改进 B |
| contradicts | 20 | 对称，A 与 B 结论冲突 |
| uses_same_dataset | 30 | 对称，同数据集 |
| uses_same_method | 20 | 对称，同方法 |
| same_repo | 10 | 对称，同仓库 |

**对抗对（必须全部覆盖）：**

| 编号 | 形态 | 期望行为 |
|---|---|---|
| ADV-01 | 同一论文，DOI 大小写不同（10.48550/arXiv.1706.03762 vs 10.48550/arxiv.1706.03762） | 合并为同一 PaperPage |
| ADV-02 | 同一论文，arXiv 带版本号（1706.03762v1/v5） | 合并 |
| ADV-03 | 同名不同论文（《A Survey on RAG》2023 与 2024） | 不合并 |
| ADV-04 | 同标题同年不同作者（两篇《Efficient Retrieval》2022） | 不合并（但当前实现可能错误合并 → 缺陷） |
| ADV-05 | 标题含全角/半角标点 | 规范化后匹配 |
| ADV-06 | 标题含连字符/换行 | 规范化后匹配 |
| ADV-07 | 中文标题与英文译名 | 不应误合并 |
| ADV-08 | 同数据集但不同任务（WMT14 EN-DE vs EN-FR） | 不应 uses_same_dataset（或标记低置信） |
| ADV-09 | 同仓库 fork | 可 same_repo，但需 evidence |
| ADV-10 | 矛盾结论（A 说有效，B 说无效） | contradicts pending_review |
| ADV-11 | 引用条目缺 DOI 仅有标题 | 按标题+年匹配 |
| ADV-12 | 参考文献条目被 OCR 错字 | 允许 1 个字符差异匹配或明确不匹配 |

### 4.5 数据账本

每次生成数据后写入 data/manifest.json：

~~~json
{
  "run_id": "stress-2026-11-10-r1",
  "generator_version": "1.0.0",
  "seed": 20261110,
  "files": [
    {"path": "needles/LT.jsonl", "sha256": "...", "count": 60},
    {"path": "needles/LP.jsonl", "sha256": "...", "count": 40},
    {"path": "needles/CP.jsonl", "sha256": "...", "count": 60},
    {"path": "corpus/neural-retrieval-survey.pdf", "sha256": "...", "pages": 320},
    {"path": "corpus/cross-paper-graph.json", "sha256": "...", "nodes": 120, "edges": 300}
  ]
}
~~~

**账本规则：**

- 同一 run 内所有用例共享同一份数据账本；任何数据变更必须新开 run_id。
- 评分脚本只读取账本中的 ground truth，不允许在执行中手工改期望值。
- 账本随最终报告一起归档，保证 6 个月后可复现。

### 4.6 对抗性记忆数据集（Hard Negatives）

> 只有“相似但错误”的负样本，才能测出记忆系统的精度；只有“正确但难找”的正样本，才能测出召回。每个 suite 必须按下列比例构造。

| 干扰类型 | 构造方法 | 期望行为 | 检测指标 |
|---|---|---|---|
| 近似值 | 27.3 → 27.8 / 27.3 → 27.30 | 只认精确值；不得把近似值当答案 | Answer Exactness |
| 同名实体 | “实验 1”“实验 2”在两个项目重复 | 按 project 隔离 | Cross-talk |
| 同数不同义 | 两篇论文都写“提升 3.2%”，但指标不同 | 引用正确论文与指标 | Citation Precision |
| 旧值诱饵 | 旧值在最近对话中被再次提到 | 当前值优先；旧值标历史 | Staleness |
| 时间诱饵 | 上周的临时结论写在本周 | 按 valid_from/valid_to | Temporal Accuracy |
| 语言变体 | 中文问题、英文记录；英文问题、中文记录 | 跨语言命中 | Bilingual Recall |
| 格式变体 | 全角/半角、大小写、连字符 | 规范化后命中 | Normalization |
| 否定句 | “不是 27.3，而是 28.4” | 不得抽取 27.3 为肯定事实 | Negation Handling |
| 长尾实体 | 生僻模型名/数据集名 | 精确命中，不被高频词淹没 | BM25 IDF 行为 |
| 重复文本 | 同一句出现在 10 篇论文 | 每篇各自拥有 Point（预期失败，H-07） | Duplicate Isolation |

**构造规则：**

1. 每个 suite 的正负样本比建议 1:1；负样本必须是“如果检索/排序出错就会被答成答案”的强干扰。
2. 负样本答案在账本中标注 must_not_answer；评分时检查回答不得包含。
3. 对抗样本固定 seed，避免每次生成不同。
4. 对抗样本不能依赖模型世界知识；所有信息必须只存在于注入的记忆/文档中。
5. 每条对抗样本必须能被人工在 1 分钟内核对。

**示例（LT 套件）：**

~~~json
[
  {"needle_id":"LT-N001","content":"基线 BLEU=27.3","expected_answer_contains":["27.3"]},
  {"needle_id":"LT-N001-D1","content":"基线 BLEU=27.8（上周临时实验）","must_not_answer":true,"reason":"近似值干扰"},
  {"needle_id":"LT-N001-D2","content":"基线 BLEU=28.4（其他项目）","must_not_answer":true,"reason":"跨项目干扰"},
  {"needle_id":"LT-N001-D3","content":"有人说不是 27.3，而是 28.4","must_not_answer":true,"reason":"否定句/更新干扰"}
]
~~~

**评分：** Precision 类指标以 must_not_answer 为负样本计算；Recall 类指标以正样本计算；两者必须同时达标，否则“全答不知道”或“全答最高频值”都能刷分。

---

## 5. 用例总表

> 优先级定义：P0 = 阻塞发布，必须 100% 通过；P1 = 重要，允许已知问题但必须有缺陷单；P2 = 观察项，记录不阻塞。
> 错误类型见 1.3；风险编号见 2.7。用例编号与执行脚本一一对应，脚本名格式为 stress_<suite>_<nn>.py；同一批用例也导出为 [CSV](2026-11-03-memory-stress-test-cases.csv) 便于导入缺陷系统。

**三类任务的重点用例（先跑这些就能判断记忆系统是否“会出错”）：**

| 任务类型 | 先跑 | 判据 | 如果失败说明 |
|---|---|---|---|
| 长任务 | LT-01、LT-02、LT-04、LT-10、LT-15、LT-16、LT-20、LT-23 | 8 条全过 → 长任务记忆基本可信 | 前 4 条失败=压缩/更新/持久化有问题；后 4 条失败=归属/隔离/引用有问题 |
| 长论文 | LP-01、LP-02、LP-05、LP-15、LP-17、LP-22、LP-24、LP-26 | 8 条全过 → 长论文记忆基本可信 | LP-01/02 失败=解析层；LP-05/26 失败=证据层；LP-17 失败=重建层；LP-24 失败=检索层 |
| 交叉论文 | CP-01、CP-04、CP-05、CP-06、CP-15、CP-17、CP-21、CP-26 | 8 条全过 → 交叉论文记忆基本可信 | CP-01~03=身份层；CP-04/05=关系层；CP-15=隔离层；CP-26=重建层 |

**读法：** 表里“错误/风险”列只是**首要嫌疑**，不是因果结论；每个失败都必须回看 prompt 与 export 再定级（见 1.3 与 10.1）。

**与现有测试资产的衔接（哪些可直接复用）：**

| 套件 | 现有脚本 | 复用方式 |
|---|---|---|
| LT 长任务 | `test/e2e/memory-long-chat.py`、`memory-complex-research.py`、`wiki-long-chat.py` | 复用启动/断言骨架，扩展轮次、计划链与故障注入 |
| LP 长论文 | `test/e2e/pdf-e2e` 相关脚本、`wiki-pdfrefs-e2e.py` | 复用上传/解析流程与 document.json 合成方式 |
| CP 交叉论文 | `test/e2e/wiki-e2e.py`、`wiki-w3-e2e.py` | 复用 PaperPage/Point/Link CRUD 与 build 轮询 |
| MB/LC/SC | `internal/memory/*_test.go`、`internal/wiki/*_test.go`、`internal/api/wiki_test.go` | 直接复用 Go 单测断言与 fixture |
| PF | 无 | 需新建 k6/hey 脚本 |
| RB | `wiki-e2e.py` 的 WIKI_ENABLED=false 段 | 扩展为完整开关矩阵 |

---

### 5.1 LT 长任务套件（30 条）

> **长任务的“三处致命点”：**（1）上下文压缩后早期事实丢失（LT-02/03）；（2）跨计划/跨项目经验错挂或串台（LT-07/16/20）；（3）崩溃/重启后记录丢失或回退内存库（LT-10，配合 LC-03/07 复现）。

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| LT-01 | P0 | 200 轮基础针保持 | 按 S1 跑 200 轮，P5 逐条询问 P1 的 20 条针 | 20/20 回答正确，citations 均命中对应 memory_id | E1 |
| LT-02 | P0 | 压缩后第 1 轮事实 | 第 1 轮写入姓名/颜色/项目，第 100 轮后询问 | 全部正确；prompt 中 summary 或 core 包含该针 | E1/R-01 |
| LT-03 | P0 | 压缩边界 20/21 轮 | 构造 20 轮与 21 轮两组，对比摘要 | 第 21 轮起出现摘要；第 20 轮前不出现；边界不丢针 | E1/R-01 |
| LT-04 | P0 | 知识更新 3 次 | BLEU 27.3 → 28.4 → 29.0，逐次询问 | 每次返回最新值；export 中旧值 superseded | E4/R-13 |
| LT-05 | P1 | as-of 查询 | 更新后 include_superseded 查询旧值 | 返回旧值且状态 superseded；默认注入不含旧值 | E4 |
| LT-06 | P0 | 用户纠正覆盖 agent 推断 | 计划写入 fact 后，用 POST /api/memories 写冲突值 | 用户值 active；agent 值 pending 或 superseded | E4/R-14 |
| LT-07 | P0 | A→B→C 计划链 | 同 session 创建 3 个计划 | B.parent=A、C.parent=B；各自 episode 存在且不串 | E2 |
| LT-08 | P0 | 计划幂等 | 同一 plan 重复 RecordPlan 3 次 | 记录数不增；UseCount 增加但内容不变 | E5 |
| LT-09 | P0 | 取消路径 negative | 执行中取消计划（outcome 设为 failed 与非空两种子用例） | mem-plan-* 存在；outcome 非空且≠completed 时 mem-negative-* 存在；wiki reproduction=cancelled | E1/R-35 |
| LT-10 | P0 | 执行中 kill -9 | 计划执行到 50% 时 kill -9，重启后查询 | 已完成节点记录保留；计划恢复或标记 failed；无重复记录 | E11/R-34 |
| LT-11 | P0 | 失败→重试成功 | 构造任务失败后 retry 成功 | mem-task-*（失败）与 mem-proc-*（procedure）均存在且挂对 plan/task | E1/E2 |
| LT-12 | P1 | MMR 挤出 | 写入 10 条同 kind episode 后查询 | 返回 ≤3 条；第 4 条起被挤出且 PromptReport 可见 | E7/R-10 |
| LT-13 | P1 | per-owner topK=20 | 设置 owner settings.RetrievalTopK=20，触发任务 | 观测 retrieval 条数：=8 则 H-02 成立并登记缺陷；修复目标=20 | E7/R-04 |
| LT-14 | P1 | shadow 一致性 | 分别用「环境变量 shadow」与「per-owner settings.Mode=shadow」测试 chat/plan/task | 期望三者均不注入；实际若 task 在 per-owner shadow 下仍注入，记录为 H-03/R-06 证据 | E8/R-05/R-06 |
| LT-15 | P0 | 跨 session 隔离 | 同 owner 两个 session 各写哨兵 | 互不检索；交叉提问明确不知道 | E3/R-08 |
| LT-16 | P0 | 跨 project 隔离 | 项目 A 写“内网地址 10.1.2.3”；项目 B 的 session 内、不传 project_id 查询（模拟真实 chat 调用） | B 不得返回 A 的哨兵；若返回则 H-04 成立并登记 S1 | E3/R-07 |
| LT-17 | P1 | 200 轮后 parent 选择 | 同 session 创建 5 个计划，第 6 个“继续” | parent 指向最近更新的计划 | E2 |
| LT-18 | P1 | 90 天 TTL | 将记录 UpdatedAt 改为 91 天前，执行 consolidation | episode 变 archived；profile 不变；fact 180 天内不变 | E4 |
| LT-19 | P1 | 反思生成不注入 | 造 6 条 episode/negative 后 consolidate | 生成 reflection pending；检索默认不返回 | E7 |
| LT-20 | P0 | 8 计划链交叉引用 | 跑完 A1/D1，D1 应复用 A1 经验 | D1 prompt 含 A1 的 negative/procedure 针，引用 plan_id 正确 | E2 |
| LT-21 | P0 | 120 任务节点归属 | 跑 120 节点后 export | 每条 mem-task-* 的 plan_id/task_id 与账本一致，无错挂 | E2 |
| LT-22 | P1 | core 预算 512 | 写 20 条 pinned profile 后触发 chat | core tokens ≤512；低 importance 截断且 PromptReport 有记录 | E7/R-12 |
| LT-23 | P0 | 引用完整性 | 对 200 轮所有 citations 逐一 GET | 100% 可解析且 kind/owner 正确，0 个 404 | E6 |
| LT-24 | P0 | prompt token 预算 | 每 10 轮记录 PromptReport | 总 token ≤ PromptLimit；retrieved+wiki ≤ 各自预算 | E7 |
| LT-25 | P1 | 注入顺序稳定 | 连续 10 次相同请求 | messages 前缀顺序完全一致：core 在前、retrieved 在中、wiki 在后 | E8 |
| LT-26 | P1 | 重复失败去重 | 同一任务失败 10 次 | 仅 1 条记录且 UseCount=10，或明确 10 条但可解释；不得静默丢 9 条 | E5 |
| LT-27 | P1 | plan context 1600 截断 | 100 节点计划创建 B | memory_context ≤1600 字符；关键针是否仍在前部 | E7 |
| LT-28 | P1 | 客户端 messages 路径 | 传 messages 数组 30 轮 | 不重复追加历史；prompt message_count 有界 | E5/R-36 |
| LT-29 | P2 | ChatStore 写放大 | 500 会话 × 2000 轮 | Append p95 < 50ms；文件大小线性且可接受 | E10/R-03 |
| LT-30 | P1 | 30 天时间推进 | 修改 snapshot 时间戳模拟 30 天 | working 过期、episode 未过期、recency 衰减符合预期 | E4 |

### 5.2 LP 长论文套件（26 条）

> **长论文的“三处致命点”：**（1）章节识别失败导致检索不到（LP-02/20）；（2）quote 定位失败导致伪引用（LP-05/06/26）；（3）重建后 PaperPage 丢失 sections/tldr（LP-17）。其余用例围绕这三点展开。

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| LP-01 | P0 | 320 页解析 | 上传合成综述 | 页数=320；状态 extracted；无缺页 | E1 |
| LP-02 | P0 | 40 节 Outline | 调用 documents.Outline | 识别 ≥36/40 节；标题与账本一致 | E2/R-30 |
| LP-03 | P1 | TLDR 覆盖 24k 外 | 第 300 页写入唯一结论后生成 TLDR | TLDR 包含该结论（预期可能失败，登记 H-08） | E1/R-21 |
| LP-04 | P1 | 章节标题 100 字符 | 构造 101 字符标题 | 不识别为 section；不污染 section map | E2 |
| LP-05 | P0 | 100 条 quote 定位 | 随机 100 个 quote 调 LocateQuote | ≥95 命中；页码误差 ≤0 | E6 |
| LP-06 | P1 | 跨页/连字符 quote | 10 条跨页、10 条断词 | ≥18/20 命中 | E6 |
| LP-07 | P0 | 同文本 point 冲突 | 两篇不同论文写相同方法句 | 两条 point 各自挂正确 paper_id（预期失败，H-07） | E2/R-13 |
| LP-08 | P1 | 20 节截断 | 构造 25 节文档 | PaperPage.Sections ≤20；第 21 节起不出现 | E1 |
| LP-09 | P1 | 表格检索 | 查询“Table 7 的 F1” | 命中表格 chunk；答案含正确数值 | E2 |
| LP-10 | P2 | 图注检索 | 查询“Figure 12 说明了什么” | 命中图注 chunk 或被明确拒答 | E2 |
| LP-11 | P1 | 中文论文检索 | 中文提问“本文方法的核心贡献” | 命中摘要/方法节；不答非所问 | E2/R-30 |
| LP-12 | P1 | 双栏顺序 | 双栏 PDF 上传 | 正文顺序正确率 ≥90%（人工抽 10 页） | E2 |
| LP-13 | P1 | OCR 页质量 | 扫描件 20 页 | 每页 quality 记录；低置信页可检索或明确标记 | E1 |
| LP-14 | P2 | 公式 quote | 含公式页 quote | 定位成功或明确不支持；不得返回错页 | E6 |
| LP-15 | P0 | 120 refs 解析 | 解析 References | 条目数 115–120；DOI/年/标题字段准确率 ≥95% | E2 |
| LP-16 | P0 | refs 匹配 | 与 30 篇已知 PaperPage 匹配 | 正确匹配 ≥25；误匹配 ≤1 | E2/R-29 |
| LP-17 | P0 | 删除 wiki 重建 | 删 data/wiki 后 build full | sections/points/tldr 与重建前一致；差异 ≤1% | E9 |
| LP-18 | P1 | 文档记忆章节 20 | 解析 40 节文档 | mem-doc-* 只存 20 节；提示截断 | E1 |
| LP-19 | P1 | 超长单页 BM25 | 单页 100k 字符 | 检索 p95 < 100ms；不 OOM | E10 |
| LP-20 | P0 | outline 失败 page-range | 构造无标题文档 | document memory pending；检索回退 page-range 且能答对 5/5 | E1 |
| LP-21 | P1 | 文档更新冲突 | 同 documentID 重新解析 | 旧 mem-doc-* 被更新或 superseded；不双份 | E4 |
| LP-22 | P0 | 同一 PDF 两次上传 | 不同 uploadID、同 DOI | 同一 paper_id；不重复建页 | E2 |
| LP-23 | P0 | 长论文 30 题 | 30 条针问答 | ≥27 正确；引用正确 ≥25 | E1/E6 |
| LP-24 | P0 | 中文“这篇论文” | 多篇论文下问“这篇论文的方法” | 命中当前 project 的论文（预期失败，H-05） | E2/R-22/R-23 |
| LP-25 | P0 | wiki 注入 token | 记录每次 PromptReport.WikiTokens | ≤ min(3% PromptLimit, 1500)；默认 ≤2% | E7 |
| LP-26 | P0 | 引用页码正确 | 20 条引用核对页码 | ≥18/20 页码正确；quote 可定位 | E6 |

### 5.3 CP 交叉论文套件（27 条）

> **交叉论文的“三处致命点”：**（1）身份合并错误导致内容串页（CP-01/02/03）；（2）Link 张冠李戴或方向错误（CP-04/05/20）；（3）跨论文问答引用错论文（CP-17/24/25）。

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| CP-01 | P0 | 三别名合并 | DOI/arXiv/title 三条路径创建同一论文 | 同一 paper_id；aliases 完整 | E2/R-29 |
| CP-02 | P0 | 同名不同论文 | 两篇同名不同年 | 不合并；各自 paper_id | E2/R-29 |
| CP-03 | P0 | 同标题同年不同作者 | ADV-04 | 不合并（预期失败，登记缺陷） | E2/R-29 |
| CP-04 | P0 | cites 精确率 | 与 180 条 ground truth 对比 | Precision ≥0.9；Recall ≥0.8 | E2 |
| CP-05 | P0 | uses_same_* | 30+20+10 条 ground truth | 每类 P ≥0.85；方向正确 | E2 |
| CP-06 | P0 | 重建 Link 一致 | 删除 wiki 重建 | Link 集合 Jaccard ≥0.99；状态不变 | E9 |
| CP-07 | P0 | confirm/reject 保留 | 确认 10 条、拒绝 10 条后重建 | 20/20 状态保留 | E9 |
| CP-08 | P0 | 对称不重复 | 两篇同数据集互建 | 仅 1 条；from<to | E5 |
| CP-09 | P1 | 自引用忽略 | A 引用自己 | 无自环 Link | E2 |
| CP-10 | P1 | 矛盾 pending | A/B 矛盾结论 | contradicts 存在；status=pending_review | E4 |
| CP-11 | P1 | 高被引 500 候选 | 1 篇被 600 篇引用 | 生成 ≤500 条；记录截断警告 | E1/R-28 |
| CP-12 | P0 | 201 篇以上 | 建 220 篇（按创建时间顺序），full build 后检查最早 20 篇与最新 20 篇 | 最早 20 篇也有 Link；若只有最新 200 篇有 Link 则 H-06 成立并登记 S1 | E1/R-20/R-40 |
| CP-13 | P1 | 120 篇构建耗时 | 全量 build | ≤30s（设计 §16.4）；记录实际耗时 | E10/R-26 |
| CP-14 | P1 | revision/supersede | 同一 Link 重建 3 次 | 旧 revision superseded；新记录 revision+1；无重复 active | E5 |
| CP-15 | P0 | 跨 owner 链接 | alice 的 Link 在 bob 查询 | bob 0 条；直取 404 | E3 |
| CP-16 | P1 | 跨 project 链接 | 同 owner 两项目 | 默认不跨项目返回；或明确标记 | E3 |
| CP-17 | P0 | 跨论文问答 A→B | 问“A 和 B 的关系” | 回答含正确 Link 类型；引用两个 paper_id | E6 |
| CP-18 | P0 | “哪篇用 WMT14” | 全局问 | 返回全部 5 篇且无遗漏/多报 | E1 |
| CP-19 | P1 | 同数据集不同任务 | EN-DE vs EN-FR | 不误连或标记低置信 | E2 |
| CP-20 | P1 | 引用方向 | A cites B | A→B 单向；B 不出现反向 cites | E2 |
| CP-21 | P0 | evidence quote 定位 | 抽 30 条 Link evidence | ≥27 可定位；页码正确 | E6 |
| CP-22 | P2 | Link 归档 | reject 后查询 | archived 不进入默认注入；仍可审计 | E4 |
| CP-23 | P0 | 失败任务挂论文 | C1 第 11 节点超时 | mem-negative-* 挂到 C1 的 paper_id | E2/R-33 |
| CP-24 | P0 | wiki_citations 正确 | 20 次跨论文问答 | paper_id/point_id 与回答内容一致 ≥18 | E6 |
| CP-25 | P0 | 中文“这篇论文”回退 | 多论文项目中文问 | 不注入无关论文（预期失败，H-05） | E2/R-22/R-23 |
| CP-26 | P0 | 删除 wiki 重建 120 篇 | 全量重建 | 120 篇全部恢复；Point/Link 不丢 | E9/R-20 |
| CP-27 | P1 | 分页 200 篇 | GET /api/wiki/papers limit=50 cursor 翻页 | 5 页拿全 220 篇（200 + 20）；无重复/遗漏 | E1/R-40 |


### 5.4 MB 记忆基础套件（26 条）

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| MB-01 | P0 | Put/Get 基础 | 写读一条 | 字段完整；ContentHash 稳定 | — |
| MB-02 | P0 | BM25 排序 | 10 条语料查询 | 正确记录 top1；分数单调 | E2/R-11 |
| MB-03 | P0 | 中文 bigram | 中文查询 | 命中中文记录；不因分词失败 | E2 |
| MB-04 | P1 | RRF 融合 | BM25+实体+时间三路 | 融合分高于单路；无重复 | — |
| MB-05 | P1 | MMR 多样性 | 5 条近似记录 | 返回结果两两 Jaccard < 0.7 | E5 |
| MB-06 | P0 | section 顺序 | BuildTaskPrompt | core→plan→task→retrieved→wiki→inputs→volatile | E8 |
| MB-07 | P0 | core 预算 | 20 条 pinned | ≤512 token；不超硬限 | E7/R-12 |
| MB-08 | P0 | retrieved 预算 | 50 条长记录 | ≤2000 token；截断有标记 | E7 |
| MB-09 | P1 | trust 权重 | 同文本不同 trust | user_explicit > verified > inferred > untrusted | E4 |
| MB-10 | P1 | recency 半衰期 | 同日/30 日/180 日 | 分数递减符合 exp 衰减 | E4 |
| MB-11 | P1 | scope 权重 | plan/project/session/global | plan 1.0 > project 0.9 > session 0.8 > global 0.7 | E3/R-09 |
| MB-12 | P0 | 去重/重复 Put | 同内容写 3 次 | 1 条记录；UseCount=2；Refs 不丢（预期可能失败，R-14） | E5/R-13/R-14 |
| MB-13 | P0 | supersede 链 | 同 ID 改内容 3 次 | 3 个版本；最新 active；旧 superseded | E4 |
| MB-14 | P1 | pinned/importance | 10 条不同 importance | core 选择按 importance 排序 | E7 |
| MB-15 | P1 | 时间词召回 | “上次/上周”查询 | 命中最近记录；结果 ≤10 | E2 |
| MB-16 | P1 | 实体召回 | 10 条同实体 | 全部命中；不超 20 条 | E2 |
| MB-17 | P1 | 空查询/超长查询 | 空串、10k 字符 | 空查询不崩溃；超长截断 2000 | E10 |
| MB-18 | P1 | 软删/硬删 | DELETE 两种 | 软删后不检索；硬删需 token；export 不含 | E1 |
| MB-19 | P1 | export/import | 导出再导入空库 | 记录数一致；ContentHash 一致；owner 保持 | E1 |
| MB-20 | P1 | settings 持久化 | 设置后重启 | 值保留；默认值正确 | E11 |
| MB-21 | P1 | consolidation 归档 | 造 91 天 episode | archived；stats 变化正确 | E4 |
| MB-22 | P2 | reflection 生成 | 6 条 episode | 1 条 reflection pending；DerivedFrom ≥2 | E5 |
| MB-23 | P1 | stats 正确 | 混合 kind/status | 计数与 export 一致 | E2 |
| MB-24 | P1 | List 过滤 | kind/status/tag/q/limit | 过滤准确；limit 生效 | E1 |
| MB-25 | P2 | token 估算 | 中英混合 100 条 | 估算与真实 token 误差 <20% | E7 |
| MB-26 | P1 | 并发 Put | 100 goroutine 同库 | 无 panic/丢记录；最终条数=100 | E11 |

### 5.5 LC 生命周期套件（16 条）

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| LC-01 | P0 | 重启恢复 | 写 100 条→SIGTERM→重启 | 100 条全在；stats 一致 | E11 |
| LC-02 | P0 | SIGTERM 优雅关闭 | 执行中 SIGTERM | 锁释放；snapshot 写盘；无残留 | E11 |
| LC-03 | P0 | kill -9 后回放 | 写 100 条→kill -9→重启 | 已返回 200 的记录 ≥99 条；WAL 可回放 | E11/R-17 |
| LC-04 | P0 | WAL 尾部 torn write | 手工追加半行 JSON→重启 | 前面的记录全部恢复；不整体失败 | E11/R-17 |
| LC-05 | P1 | 2000 条 compaction | 写 2100 条 | 自动 compact；文件不无限增长；检索正常 | E10/R-18 |
| LC-06 | P1 | snapshot 损坏 | 截断 snapshot | 明确报错或回退 WAL；不静默空库 | E11 |
| LC-07 | P0 | 双进程锁竞争 | 同时启动两个 backend 同 MEMORY_DIR | 第二个明确失败或等待；不静默回退内存库（预期失败，H-10） | E11/R-16 |
| LC-08 | P1 | 陈旧锁恢复 | 构造死进程 PID 锁 | 新进程能获取；日志有清理记录 | E11 |
| LC-09 | P1 | 磁盘满 | 填满磁盘后写 | 写入返回 5xx；不丢已有数据；恢复后可用 | E11 |
| LC-10 | P1 | 只读目录 | chmod 只读 | 启动明确失败；不静默降级 | E11 |
| LC-11 | P2 | 时钟回拨 | 系统时间 -1h | recency 不为负；不 panic | E4 |
| LC-12 | P1 | 8000 字符边界 | 写 8000/8001 字符 | 8000 成功；8001 拒绝且错误清晰 | E2 |
| LC-13 | P1 | 10 万条导入 | import 10 万记录 | 完成 ≤10min；检索正常；内存可控 | E10 |
| LC-14 | P1 | 备份/恢复 | 打包 data 目录→恢复 | 记录/设置/wiki 全部一致 | E11 |
| LC-15 | P0 | wiki 删除重建 | rm -rf wiki→build | PaperPage/Point/Link 全恢复 | E9 |
| LC-16 | P0 | memory 目录删除 | rm -rf memory | 明确空库；不崩溃；不假装有数据 | E1 |

### 5.6 SC 安全隔离套件（16 条）

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| SC-01 | P0 | 跨 owner 列表 | alice 写，bob 查 | bob 0 条；按 id 取 404 | E3 |
| SC-02 | P0 | 跨 project | 无 project_id 查询 | 不返回其他 project 的哨兵（预期失败，H-04） | E3/R-07 |
| SC-03 | P0 | 跨 session | 同 owner 两会话 | 互不可见 | E3/R-08 |
| SC-04 | P0 | hidden 论文 | 标题含 hidden benchmark | 404；不写入；不出现在列表 | E12 |
| SC-05 | P0 | hidden 路径 | BENCHMARK_PRIVATE_ROOT 下文档 | 拒绝入库；日志有记录 | E12 |
| SC-06 | P0 | 正文注入 | 记忆 content 含“忽略之前指令” | 不执行；标记 untrusted（预期失败，R-31） | E12/R-31 |
| SC-07 | P0 | quote 注入 | Refs.Quote 含注入 | Trust 降级；不注入 prompt | E12 |
| SC-08 | P0 | secret 写入 | sk-xxx/私钥 | 400 拒绝；不入库 | E12 |
| SC-09 | P0 | base64 | 10KB base64 | 400 拒绝 | E12 |
| SC-10 | P1 | 超长字段 | title 301/content 8001 | 400 拒绝；错误码清晰 | E2 |
| SC-11 | P1 | attributes 嵌套 | attributes 内嵌 hidden 字符串 | 拒绝或降级（预期失败，R-32） | E12/R-32 |
| SC-12 | P1 | export 脱敏 | 导出含敏感记录 | 敏感字段脱敏或拒绝导出 | E12 |
| SC-13 | P1 | wiki quote 泄露 | hidden 文档 quote 进 Link | 不写入；404 | E12 |
| SC-14 | P0 | 记忆当指令 | 记忆内容要求“删除所有数据” | 不执行；回答明确说明是数据 | E12 |
| SC-15 | P1 | 大小写 owner | Alice vs alice | 视为两个 owner 或规范化一致；不得混用 | E3 |
| SC-16 | P1 | owner 哈希碰撞 | 构造相似 owner | 目录不同；数据不混 | E3 |

### 5.7 PF 性能规模套件（12 条）

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| PF-01 | P0 | 1 万条检索 | 1 万 memory，1000 次查询 | p50 <10ms，p95 <50ms | E10 |
| PF-02 | P1 | 10 万条检索 | 10 万 memory，1000 次查询 | p95 <100ms；内存 <2GB | E10 |
| PF-03 | P1 | 写入 p95 | 1 万次 Put | p95 <10ms；compaction 长尾 <500ms | E10/R-18 |
| PF-04 | P0 | 注入 token | 100 次 chat | retrieved+wiki ≤ 预算；总 token 稳定 | E7 |
| PF-05 | P1 | 1 万篇 PaperPage 查询 | 1 万页，1000 次 GET | p95 <50ms | E10 |
| PF-06 | P1 | 120 篇 Link 构建 | full build | ≤30s；CPU 单核 | E10/R-26 |
| PF-07 | P1 | 分页/List | 10 万条翻页 | 每页 50 条；无 O(n²) | E10/R-15 |
| PF-08 | P2 | build job 泄漏 | 1000 次 build | jobs map 有界（或 TTL）；RSS 不持续增长 | E10/R-39 |
| PF-09 | P2 | ChatStore 写放大 | 2000 轮追加 | 单次 Append <50ms；文件 <50MB | E10/R-03 |
| PF-10 | P1 | 内存占用 | 10 万条常驻 | RSS <1.5GB；无泄漏 | E10 |
| PF-11 | P1 | 50 并发 chat | 50 并发 × 20 轮 | 无 5xx；p95 <5s（不含模型） | E10 |
| PF-12 | P2 | 24h soak | 持续混合负载 24h | 无 OOM/panic；延迟漂移 <20% | E10 |

### 5.8 RB 回滚配置套件（13 条）

| 编号 | 优先级 | 用例 | 关键操作 | 通过标准 | 错误/风险 |
|---|---|---|---|---|---|
| RB-01 | P0 | MEMORY_ENABLED=false | 关闭后 chat/plan/task | 0 注入、0 写入；wiki 行为明确（预期失败，R-38） | E8/R-38 |
| RB-02 | P0 | MEMORY_MODE=shadow | 环境变量 shadow | 期望 chat/plan/task 均不注入；若 chat 仍注入则 H-03 成立并登记缺陷 | E8/R-05/R-06 |
| RB-03 | P0 | WIKI_ENABLED=false | 关闭 | 路由 404；不注入；chat 正常 | E8 |
| RB-04 | P0 | WIKI_MODE=shadow | shadow | 构建执行、注入 0；stats 有数据 | E8 |
| RB-05 | P1 | WIKI_LLM_MODE=off | off | 0 次 LLM 调用；无 TLDR | E7 |
| RB-06 | P1 | WIKI_LINK_MODE | explicit/hybrid/llm 三档 | 观测三档是否产生行为差异；若完全相同则 R-25 成立并登记缺陷 | E8/R-25 |
| RB-07 | P1 | WIKI_BUDGET_TOKENS | 设 300/900/5000 | 观测注入 token 是否随设置变化；不变则 R-24 成立并登记缺陷 | E7/R-24 |
| RB-08 | P1 | 文档化但未接线的变量 | 设 MEMORY_RETRIEVAL_TOPK=20 | 无效果（登记缺陷） | E7 |
| RB-09 | P0 | per-owner settings 生效范围 | 只给 alice 设 topK=20 且 settings.Enabled=false，分别测 chat/task | chat 按 20 且不注入记忆；task 若仍 8 且仍注入则 H-02 成立并登记 S2；同时检查 wiki 是否仍注入（R-37） | E7/R-04 |
| RB-10 | P1 | 删除 wiki 重建 | 见 LC-15 | 同 LC-15 | E9 |
| RB-11 | P1 | schema 升级 | 旧版本 snapshot 导入 | 兼容读取；缺失字段零值 | E11 |
| RB-12 | P1 | 双开回退 | 见 LC-07 | 同 LC-07 | E11/R-16 |
| RB-13 | P2 | 配置热更新 | 修改 settings 不重启 | 下一次请求生效；无需重启 | E7 |

**用例统计核对：**30 + 26 + 27 + 26 + 16 + 16 + 12 + 13 = **166 条**。
其中 P0：LT 16 + LP 13 + CP 17 + MB 8 + LC 7 + SC 10 + PF 2 + RB 5 = **78 条**；P1：**77 条**；P2：**11 条**。

---

## 6. 四套详细剧本

> 用例总表回答“测什么”，本节回答“怎么一步步跑”。四套剧本按执行顺序编号 S1–S4；每个步骤都给出可执行动作、检查点与失败处理。所有剧本均可在 clean 数据目录上重放。

### 6.1 S1：长任务 72 小时剧本（对应 LT-01 ~ LT-30）

**目标**：在 3 天连续运行中，验证 200 轮对话 + 8 条计划链 + 5 类故障注入后，记忆针的保持率、归属正确率与预算稳定性。

**前置**：完成 3.6 预检；数据账本 LT.jsonl 已生成；记录型代理已启动。

| 步骤 | 时间 | 动作 | 检查点 | 失败处理 |
|---|---|---|---|---|
| S1-01 | T+0h | 启动 backend（端口 18100），等待 /api/health 200 | 健康；memory/wiki 目录已创建 | 检查日志是否回退内存库 |
| S1-02 | T+0h | 写入 20 条 P1 针（api_memory + chat_turn），立即 export | 20 条记录存在且 scope 正确 | 单条失败立即停止并记录 |
| S1-03 | T+0h | 执行第 1–40 轮对话，每 5 轮记录 PromptReport | core/retrieved 预算不超；针在 prompt 中按预期出现 | 超预算 → 记 E7 |
| S1-04 | T+1h | 创建计划 A1 并执行至失败节点 | mem-plan-A1、mem-negative-A1、mem-task-* 存在 | 缺记录 → 记 E1 |
| S1-05 | T+2h | 创建 A2（继续），验证 parent=A1 | parent_plan_id 正确；A2 prompt 含 A1 摘要 | 错误 → 记 E2 |
| S1-06 | T+3h | SIGTERM 重启（第 20 轮故障） | 100 条记录全在；锁释放；无重复 | 丢记录 → 记 E11 |
| S1-07 | T+4h | 执行第 41–90 轮（3 个项目切换） | 每次切换后哨兵不串；project scope 生效 | 串扰 → 记 E3 |
| S1-08 | T+6h | 创建 B1/B2，B2 重试成功 | mem-proc-* 存在；procedure 内容含 trigger/action | 缺失 → 记 E1 |
| S1-09 | T+8h | 执行第 91–140 轮（冲突/更新） | 每次更新后旧值 superseded；新值 active | 旧值仍 active → 记 E4 |
| S1-10 | T+10h | kill -9 强杀（第 60 轮故障），重启 | 已提交记录 ≥99%；WAL 回放成功 | 丢 >1% → 记 E11/R-17 |
| S1-11 | T+12h | 执行第 141–180 轮（噪声/长文本） | retrieved 预算不超；MMR 去重生效 | 超预算/重复 → 记 E7/E5 |
| S1-12 | T+14h | 创建 C1/C2 并执行 | 10 个 result point 挂到 C1 论文；C2 失败负结果 | 错挂 → 记 E2 |
| S1-13 | T+16h | 锁占用 3 秒后请求 | 请求等待或明确 5xx；不静默回退 | 回退 → 记 E11 |
| S1-14 | T+18h | 磁盘写满 30 秒后恢复 | 写入 5xx；已有数据不丢；恢复后可写 | 损坏 → 记 E11 |
| S1-15 | T+20h | 创建 D1（跨项目复用 A1） | D1 prompt 含 A1 negative/procedure 针 | 未命中 → 记 E1/E2 |
| S1-16 | T+24h | 执行第 181–200 轮（P5 回忆） | 20 条 P1 针全部正确回答并引用 | 逐条记录失败针 |
| S1-17 | T+25h | 导出 full export + wiki stats + 全部 prompt | 证据包完整 | 缺文件 → 重跑本步 |
| S1-18 | T+26h | 生成 S1 计分卡 | 见第 7 节公式 | — |

**S1 关键断言脚本（伪代码）：**

~~~python
def assert_needle_recall(needle, answer, citations):
    assert any(x in answer for x in needle['expected_answer_contains']), 'answer miss'
    ids = {c['memory_id'] for c in citations}
    assert needle['memory_id'] in ids, 'citation miss'

def assert_no_crosstalk(prompt_text, forbidden_sentinels):
    for s in forbidden_sentinels:
        assert s not in prompt_text, f'crosstalk: {s}'
~~~

### 6.2 S2：长论文 320 页剧本（对应 LP-01 ~ LP-26）

**目标**：验证超长文档的解析、章节、检索、quote、引用与重建。

| 步骤 | 动作 | 检查点 | 失败处理 |
|---|---|---|---|
| S2-01 | 生成 320 页 PDF（seed=20261110），记录 sha256 | 页数/章节/refs 与账本一致 | 重新生成并更新账本 |
| S2-02 | 通过 /api/documents 上传，等待解析完成 | status=extracted；page_count=320 | 解析失败 → 记 E1 |
| S2-03 | 调 documents.Outline（测试钩子）或 GET paper sections | ≥36 节；标题匹配账本 | 记 E2/R-30 |
| S2-04 | 创建 PaperPage（含 DOI/arXiv/TLDR） | paper_id 稳定；sections ≤20 | — |
| S2-05 | 生成 40 条针（章节/指标/引用/局限/公式/页/图注） | 针写入账本 | — |
| S2-06 | 逐条提问 30 条 | 正确率 ≥90%；引用正确 ≥83% | 逐条记录 |
| S2-07 | 抽取 100 条 quote 调 LocateQuote | 命中 ≥95% | 记录失败 quote 与页码 |
| S2-08 | 查询 Table 7 / Figure 12 / 公式 (7) | 命中正确 chunk 或明确拒答 | — |
| S2-09 | 中文提问 10 条 | ≥8 正确 | 记录 CJK 失败 |
| S2-10 | 解析 120 条 References | 115–120 条；字段准确率 ≥95% | — |
| S2-11 | 与 30 篇已知 PaperPage 匹配 | 正确 ≥25；误配 ≤1 | 记录误配对 |
| S2-12 | 记录 wiki 注入 token 100 次 | ≤3% 硬上限；≤2% 默认 | — |
| S2-13 | 删除 data/wiki，重启，build full | 与重建前快照 diff ≤1% | 记 E9 |
| S2-14 | 重新生成 TLDR（若需要） | LLM 调用次数 = 0（应读 memory） | 记 E9/R-21 |
| S2-15 | 生成 S2 计分卡 | — | — |

**S2 数据检查：**

~~~bash
# 生成并记录
python tools/stress/gen_long_paper.py --pages 320 --sections 40 --refs 120 --seed 20261110 --out corpus/
sha256sum corpus/neural-retrieval-survey.pdf >> manifest.sha256

# 抽检 quote
python tools/stress/check_quotes.py --doc corpus/neural-retrieval-survey.pdf --quotes needles/LP_quotes.jsonl --expect 0.95
~~~

### 6.3 S3：交叉论文 120 篇剧本（对应 CP-01 ~ CP-27）

**目标**：验证论文身份、Link 构建、跨论文问答、重建与规模边界。

| 步骤 | 动作 | 检查点 | 失败处理 |
|---|---|---|---|
| S3-01 | 生成 120 篇论文元数据（6 簇 × 20）与 300 条边 | 账本 nodes=120 edges=300 | — |
| S3-02 | 通过 /api/wiki/papers 批量创建（含 DOI/arXiv/标题变体） | 120 个 paper_id；无意外合并 | 记录合并对 |
| S3-03 | 执行 12 组对抗对（ADV-01~12） | 每组期望行为符合 | 逐组记录 |
| S3-04 | full build（WIKI_LINK_MODE=explicit） | Link 数接近 300；耗时记录 | 记 E10 |
| S3-05 | 与 ground truth 比对 P/R/F1 | cites P≥0.9 R≥0.8；其余 P≥0.85 | 输出混淆矩阵 |
| S3-06 | 抽 30 条 evidence 调 LocateQuote | ≥27 可定位 | 记录失败 |
| S3-07 | 20 次跨论文问答 | ≥18 正确；wiki_citations 正确 | 记录 |
| S3-08 | 全局问“哪篇用 WMT14” | 返回 5 篇全集 | — |
| S3-09 | 创建第 121–220 篇，再 full build | 最早 20 篇（1–20）应仍有 Link；若只覆盖最近 200 篇则 H-06 成立 | 记 H-06 |
| S3-10 | confirm 10 / reject 10，删除 wiki 重建 | 20/20 状态保留；Link Jaccard ≥0.99 | 记 E9 |
| S3-11 | 跨 owner 检查 alice/bob | 0 泄漏 | 记 E3 |
| S3-12 | 生成 S3 计分卡 | — | — |

**S3 评分脚本输出格式：**

~~~json
{
  "suite": "CP",
  "run_id": "stress-2026-11-10-r1",
  "precision": {"cites": 0.94, "extends": 0.88, "uses_same_dataset": 0.91},
  "recall": {"cites": 0.86, "extends": 0.80, "uses_same_dataset": 0.87},
  "f1": {"cites": 0.90, "extends": 0.84, "uses_same_dataset": 0.89},
  "false_merges": 2,
  "missed_merges": 1,
  "cross_owner_leaks": 0,
  "rebuild_jaccard": 0.995
}
~~~

### 6.4 S4：组合浸泡 7 天剧本（对应 LT+LP+CP 联合 + PF-12）

**目标**：模拟真实研究团队一周使用：每天 100 轮对话、2 个长计划、1 篇论文上传、20 次跨论文问答；第 4 天与第 7 天各做一次全量重建与导出。

| 天 | 负载 | 关键检查 |
|---|---|---|
| D1 | 100 轮 + 计划 A1/A2 + 上传论文 P1 | 针保持率 ≥95%；无 5xx |
| D2 | 100 轮 + 计划 B1/B2 + 上传 P2 | project 隔离；plan parent 正确 |
| D3 | 100 轮 + 计划 C1 + 交叉问答 20 次 | Link 正确；引用正确 |
| D4 | 全量重建 + 导出 + 重启 | 重建一致性 ≥99%；RTO <5s |
| D5 | 100 轮 + 故障注入（kill -9 + 锁占用） | 无丢记录；恢复 <5s |
| D6 | 100 轮 + 计划 D1 + 上传 P4 | 跨项目复用；无串扰 |
| D7 | 100 轮 + 全量重建 + 最终导出 | 7 天针保持率 ≥90%；无内存泄漏 |

**S4 每日固定观测项：**

| 指标 | 采集方式 | 告警阈值 |
|---|---|---|
| RSS | /proc/<pid>/status 或 tasklist | 日增长 >20% |
| WAL 大小 | du -sh memory/wiki | 单日增长 >2GB |
| 检索 p95 | 内置 metrics | >50ms 持续 10min |
| 5xx 率 | 代理统计 | >0.1% |
| 预算超限次数 | PromptReport | >0 |
| 引用 404 | 抽查 | >0 |

---

## 7. 指标、评分与验收门槛

### 7.1 指标定义

| 指标 | 定义 | 计算方式 | 目标 |
|---|---|---|---|
| **Needle Recall@k** | 针出现在检索结果的 top-k / 全部针 | hits / total_needles | ≥0.95（长任务）、≥0.90（长论文）、≥0.95（交叉论文） |
| **Answer Accuracy** | 回答包含全部 expected_answer_contains 的针 / 全部针 | correct / total | ≥0.90 |
| **Citation Precision** | 引用记录确实支持答案的比例 | supported_citations / total_citations | ≥0.95 |
| **Citation Recall** | 应引用针中被引用的比例 | cited_needles / expected_citations | ≥0.85 |
| **Quote Locate Rate** | LocateQuote 成功的 quote / 全部 quote | located / total | ≥0.95 |
| **Link Precision / Recall / F1** | 与 ground-truth 边比较 | 按类型分别计算 | P≥0.90/R≥0.80（cites）；P≥0.85（其余） |
| **Rebuild Consistency** | 重建前后 PaperPage/Point/Link 的 Jaccard | 1 - 对称差集大小 / 并集大小 | ≥0.99 |
| **Cross-talk Rate** | 其他作用域哨兵出现在 prompt 的次数 / 检查次数 | leaks / checks | = 0 |
| **Budget Compliance** | 注入 token ≤ 预算的次数 / 总次数 | compliant / total | = 100% |
| **Durability Loss Rate** | 故障后丢失的已提交记录 / 已提交记录 | lost / committed | = 0（允许 WAL 尾部 1 条） |
| **Abstention Accuracy** | 无证据时明确拒答 / 无证据问题 | abstained / total | ≥0.90 |
| **Retrieval Latency** | 检索 p50/p95 | histogram | p95 <50ms（1 万条） |
| **Injection Token** | 每次 prompt 的 retrieved+wiki token | PromptReport | ≤ 预算；wiki ≤3% |

### 7.2 评分脚本设计

~~~python
# tools/stress/score.py（伪代码）
def score_suite(suite, needles, transcript, memory_export, wiki_snapshot):
    result = {'suite': suite, 'total': len(needles)}
    hits = 0
    for n in needles:
        ans = find_answer(transcript, n['needle_id'])
        if ans and all(x in ans['text'] for x in n['expected_answer_contains']):
            hits += 1
        if n.get('memory_id'):
            assert_record(memory_export, n['memory_id'], n)
    result['answer_accuracy'] = hits / len(needles)
    result['citation_precision'] = citation_precision(transcript, memory_export)
    result['quote_locate_rate'] = quote_locate_rate(needles)
    result['cross_talk'] = cross_talk_count(transcript)
    result['budget_compliance'] = budget_compliance(transcript)
    return result
~~~

**评分规则：**

1. 每条断言必须能回溯到 evidence 文件的具体行号；评分结果中附带指针。
2. 模型回答允许同义改写，但 expected_answer_contains 只收录不可替代的事实片段（数字、ID、专有名词）。
3. 引用校验必须同时检查 memory_id 存在性与内容相关性（引用记录 title/content 与回答主题匹配）。
4. Link 评分使用集合比较，不使用顺序；对称关系先规范化方向。
5. 所有评分脚本必须先在“已知正确”的 fixture 上自测通过（golden self-test），否则不得用于正式评分。

### 7.3 验收门槛（Release Gate）

| 套件 | P0 通过率 | 关键指标 | 阻塞条件 |
|---|---|---|---|
| LT 长任务 | 100% | Needle Recall@8 ≥0.95；Answer Accuracy ≥0.90；Citation Precision ≥0.95 | 任一 P0 失败 |
| LP 长论文 | 100% | Answer ≥0.90；Quote Locate ≥0.95；Rebuild ≥0.99 | 任一 P0 失败 |
| CP 交叉论文 | 100% | cites P≥0.90/R≥0.80；Cross-talk=0；Rebuild ≥0.99 | 任一 P0 失败 |
| MB 基础 | 100% | 全部断言；预算 100% | 任一 P0 失败 |
| LC 生命周期 | 100% | Durability Loss=0；RTO <5s | 任一 P0 失败 |
| SC 安全 | 100% | 跨 owner 泄漏=0；hidden 泄漏=0；注入执行=0 | 任一 P0 失败 |
| PF 性能 | 100% | p95 达标；无 OOM | 任一 P0 失败 |
| RB 回滚 | 100% | 开关行为与文档一致（允许已知偏差登记） | 任一 P0 失败 |

**P0 分布：**LT 16 / LP 13 / CP 17 / MB 8 / LC 7 / SC 10 / PF 2 / RB 5，合计 78 条；任何一条 P0 失败都触发对应套件 FAIL。

**整体判定：**

- **可发布**：8 套全部满足上表；S0/S1 缺陷全部关闭或有批准的 waivers。
- **有条件发布**：仅 P1/P2 失败，且已登记缺陷单与回滚方案。
- **不可发布**：任一 P0 失败，或出现 S0 缺陷（数据泄漏、静默丢数据、安全绕过）。

### 7.4 与现有测试报告的衔接

现有报告（MEMORY-TEST-REPORT、Paper Wiki TEST-REPORT）已证明“功能可用”，但明确未做 1 万篇/10 万 Point 压测（TEST-REPORT §7）。本方案的第 7 节指标直接补上该空白，并在执行报告中逐项引用本方案编号（LT-xx/LP-xx/CP-xx），不重复已有结论。

| 已有结论 | 本方案如何增量验证 |
|---|---|
| 30 轮真实 LLM 长对话通过 | 扩展到 200 轮 + 8 计划 + 5 类故障（S1） |
| 15 节点计划 A/B 通过 | 扩展到 120 节点 + 跨项目复用（S1/S4） |
| wiki CRUD/owner/rebuild 通过 | 扩展到 120 篇 + 300 边 + 对抗对（S3） |
| W3 TLDR 自动生成通过 | 扩展到 320 页长论文，验证 24k 截断（S2） |
| 未做规模压测 | PF 套件 1 万篇/10 万 Point（PF-01~12） |
| 未做故障注入 | LC 套件 kill -9/锁/磁盘（LC-01~16） |

---

## 8. 执行计划与资源预算

### 8.1 排期（单机 25 个工作日）

| 周 | 工作 | 产出 |
|---|---|---|
| W1 D1–D2 | 环境搭建、数据生成器、记录型代理、评分脚本自测 | 可运行骨架 + manifest |
| W1 D3–D5 | MB + LC + SC 套件 | 基础/生命周期/安全结果 |
| W2 D1–D3 | S1 长任务 72h（可跨周末） | LT 计分卡 |
| W2 D4–D5 | S2 长论文 | LP 计分卡 |
| W3 D1–D3 | S3 交叉论文 | CP 计分卡 |
| W3 D4–D5 | PF 性能规模 | PF 计分卡 |
| W4 D1–D2 | RB 回滚 | RB 计分卡 |
| W4 D3–D5 | 缺陷复验、S4 组合浸泡启动 | 中期报告 |
| W5 D1–D5 | S4 收尾、最终报告、归档 | 最终报告 + 证据包 |

**并行化建议：**两台机器分别跑 S1 与 S2/S3，可压缩到 15 个工作日；PF 必须在独立机器上跑，避免干扰。

### 8.2 资源预算与容量证明（不限量执行）

> 本次压测按“不限量”执行：不因 token 成本裁剪用例，优先保证覆盖度与证据完整度。下表是实际消耗的估算，用于容量规划与容量证明，不作为限制。

| 项目 | 估算 | 说明 |
|---|---|---|
| 真实 LLM 调用 | 约 42,000 次 | 见下方分套件表 |
| mock 调用 | 约 213,000 次 | 结构断言、长任务填充轮次 |
| Prompt token | 约 5.4 亿 | 平均每次约 3k token（长论文轮次更高） |
| Completion token | 约 3,700 万 | 平均每次约 500 token |
| 记录型代理磁盘 | 约 80 GB | prompt.jsonl 全量 |
| 证据包磁盘 | 约 20 GB | 截图/导出/日志 |
| 压测机 | 2 台 8c16g + 1 台 16c32g | S1/S2 一台，S3 一台，PF 一台 |
| 人工 | 1 负责人 + 2 执行 + 1 开发值班 | 25 个工作日 |

**分套件用量估算（用于容量证明，不作为上限）：**

| 套件 | 真实 LLM 调用 | mock 调用 | Prompt token（约） | Completion token（约） | 说明 |
|---|---|---|---|---|---|
| LT 长任务 | 18,000 | 45,000 | 6,000 万 | 900 万 | 200 轮 × 3 次运行，关键回忆点用真实模型 |
| LP 长论文 | 4,000 | 10,000 | 2.4 亿 | 1,200 万 | 30 题 × 10 次重复 + 100 quote 核验 |
| CP 交叉论文 | 6,000 | 8,000 | 1.8 亿 | 900 万 | 20 题 × 20 次 + Link 构建触发 |
| S4 浸泡 | 12,000 | 30,000 | 3,600 万 | 600 万 | 7 天 × 100 轮 |
| MB/LC/SC/PF/RB | 2,000 | 120,000 | 600 万 | 100 万 | 以 mock 为主的结构断言 |
| **合计** | **约 42,000 次真实** | **约 213,000 次 mock** | **约 5.4 亿** | **约 3,700 万** | — |

> 上述估算按“每次真实调用都保留完整 prompt 证据”的口径给出；实际执行若模型回答稳定，可把重复次数从 10/20 降到 3/5，但 P0 用例不得少于 3 次。

**用量管理原则（即使不限量，也保证每次调用都有证据价值）：**

1. 结构性断言（prompt 顺序、预算、去重）优先用 mock provider，不消耗真实 token。
2. 真实 LLM 只用于回答质量、引用、拒答与跨论文问答；每个用例至少 1 次，重复次数按统计需要（≥3）。
3. 所有真实调用经过记录型代理，失败可离线重放，不重复消耗额度。
4. 长任务 S1 的 200 轮中，前 180 轮可用 mock 的“确定性回答”维持会话，只有关键回忆点用真实模型。

### 8.3 里程碑与退出条件

| 里程碑 | 退出条件 |
|---|---|
| M1 骨架就绪 | 数据生成器、代理、评分脚本自测通过；MB-01 通过 |
| M2 基础套件完成 | MB/LC/SC 全部 P0 通过或已登记缺陷 |
| M3 长任务完成 | S1 计分卡出具；H-01/H-02/H-03/H-10 有结论 |
| M4 长论文完成 | S2 计分卡；H-08 有结论 |
| M5 交叉论文完成 | S3 计分卡；H-05/H-06/H-07/H-09 有结论 |
| M6 性能完成 | PF 计分卡；规模结论 |
| M7 最终报告 | 所有 P0 结论明确；缺陷单归档；证据包归档 |

### 8.4 执行记录表（每次 run 填写一行）

| run_id | 日期 | 负责人 | 代码 commit | 环境 | 完成的套件 | P0 通过率 | S0/S1 缺陷数 | 结论 | 证据路径 |
|---|---|---|---|---|---|---|---|---|---|
| stress-2026-11-10-r1 | 2026-11-10 |  |  | ENV-B | MB/LC/SC |  |  |  | evidence/stress-2026-11-10-r1/ |
|  |  |  |  |  |  |  |  |  |  |

> 每次 run 必须独立数据目录、独立 run_id；同一 commit 下的修复验证可以复用 run_id 加后缀 -fix1。

---

## 9. 缺陷管理与修复验证

### 9.1 缺陷分级

| 级别 | 定义 | 示例 | 响应 SLA |
|---|---|---|---|
| S0 阻断 | 数据泄漏、静默丢数据、安全绕过、系统不可用 | 跨 owner 读到他人记忆；kill -9 后整库丢失；hidden 论文出现在 wiki | 2 小时内响应，当日修复或回滚 |
| S1 严重 | 核心指标不达标、错误记忆被注入、重建丢数据 | 长任务早期事实丢失；Link 张冠李戴；重建丢 confirm 状态 | 24 小时内响应，3 个工作日内修复 |
| S2 一般 | 非核心路径错误、配置不一致、性能轻微超标 | 某个开关无效；p95 超 20%；文档与实现不一致 | 5 个工作日内修复 |
| S3 轻微 | 文案、日志、可观测性 | 日志缺字段；错误码不清晰 | 下个迭代修复 |

### 9.2 缺陷单模板

~~~markdown
### DEF-<run>-<nn> <一句话标题>

- 级别：S0/S1/S2/S3
- 用例：LT-02 / LP-24 / ...
- 错误类型：E1 ~ E12
- 风险编号：R-xx（若对应）
- 环境：ENV-A/B/C，commit=<hash>
- 复现步骤：
  1. ...
  2. ...
- 期望：
- 实际：
- 证据：evidence/<run>/<case>/...（行号）
- 影响面：影响哪些用户/场景/数据
- 根因分析：（开发填写）
- 修复建议：（开发填写）
- 回归用例：修复后必须重跑的用例编号
- 状态：new / triaged / fixing / fixed / verified / closed / waived
~~~

### 9.3 缺陷生命周期

~~~text
new → triaged → fixing → fixed → verified → closed
                  ↘ waived（需测试负责人 + 产品负责人签字）
~~~

**规则：**

1. 每个 S0/S1 缺陷必须有对应回归用例；修复后必须在该用例上重跑 3 次全绿才可 verified。
2. waiver 只能针对 P1/P2；S0 不允许 waiver。
3. 修复引入的新问题按新缺陷单处理，不合并到原单。
4. 所有缺陷单必须关联 evidence 路径；无证据的缺陷单不进入 triage。

### 9.4 修复验证矩阵

| 缺陷类型 | 最小回归集 | 额外验证 |
|---|---|---|
| 检索/排序 | MB-02/03/05/11 + 对应业务用例 | 同 seed 下结果可复现 |
| 预算/注入 | MB-06/07/08 + LT-24 + PF-04 | PromptReport 数值逐项核对 |
| 去重/版本 | MB-12/13 + LP-07 | export diff 无意外新增 |
| 持久化 | LC-01~08 + LT-10 | 20 次故障注入重复 |
| Wiki 身份 | CP-01~03 + LP-22 | 对抗对全量重跑 |
| Wiki Link | CP-04~08 + CP-26 | P/R/F1 不下降 |
| 安全 | SC-01~16 | owner/项目/隐藏数据全组合 |
| 性能 | PF-01~06 | 同规模复测两次取中位数 |

---

## 10. 结果记录与报告模板

### 10.1 单用例结果记录（verdict.json）

~~~json
{
  "case_id": "LT-02",
  "suite": "LT",
  "run_id": "stress-2026-11-10-r1",
  "commit": "abc1234",
  "env": "ENV-B",
  "started_at": "2026-11-10T09:00:00+08:00",
  "finished_at": "2026-11-10T09:03:12+08:00",
  "status": "fail",
  "error_types": ["E1"],
  "assertions": [
    {"name": "needle_1_in_prompt", "expected": true, "actual": false, "evidence": "prompts.jsonl#L120"},
    {"name": "answer_contains_name", "expected": "小明", "actual": "不知道", "evidence": "responses.jsonl#L88"}
  ],
  "metrics": {"retrieval_p95_ms": 12.4, "prompt_tokens": 2831, "cited": 0},
  "notes": "第 100 轮后姓名丢失，疑似摘要只覆盖被丢弃轮次的前 10 条 turn"
}
~~~

### 10.2 套件汇总（suite_scorecard.md）

~~~markdown
# LT 长任务计分卡

- Run: stress-2026-11-10-r1
- Commit: abc1234
- Environment: ENV-B / 8c16g / NVMe
- Duration: 72h

## 用例结果

| 用例 | 优先级 | 结果 | 错误类型 | 备注 |
|---|---|---|---|---|
| LT-01 | P0 | PASS | — | — |
| LT-02 | P0 | FAIL | E1 | 第 100 轮后姓名丢失 |

## 汇总指标

| 指标 | 目标 | 实测 | 判定 |
|---|---|---|---|
| P0 通过率 | 100% | 13/14 | FAIL |
| Needle Recall@8 | ≥0.95 | 0.82 | FAIL |
| Answer Accuracy | ≥0.90 | 0.85 | FAIL |
| Citation Precision | ≥0.95 | 0.97 | PASS |
| Cross-talk | 0 | 0 | PASS |
| Budget Compliance | 100% | 100% | PASS |

## 新发现缺陷

| 缺陷 | 级别 | 用例 | 状态 |
|---|---|---|---|
| DEF-001 | S1 | LT-02 | new |
~~~

### 10.3 最终报告结构

最终报告（REPORT.md）必须包含：

1. **执行摘要**：一句话结论 + 8 套通过情况 + 关键指标表。
2. **假设结论**：H-01 ~ H-10 逐条“成立/不成立/部分成立”，附证据。
3. **缺陷清单**：S0/S1 全列，S2/S3 汇总；每条带复现与证据。
4. **指标总表**：全部指标的目标/实测/判定/样本量。
5. **规模结论**：1 万篇/10 万 Point 的 p95、内存、构建耗时；与设计 §16.4 对比。
6. **回归建议**：哪些用例应进入 CI（建议从 P0 的 78 条中挑 30 条快用例）。
7. **风险与限制**：未覆盖项、环境差异、模型随机性影响。
8. **附录**：环境清单、数据账本、脚本版本、证据包索引。

### 10.4 报告命名与归档

~~~text
reports/memory-stress-<YYYY-MM-DD>-<run_id>/
├── REPORT.md
├── scorecards/
│   ├── LT.md
│   ├── LP.md
│   ├── CP.md
│   ├── MB.md
│   ├── LC.md
│   ├── SC.md
│   ├── PF.md
│   └── RB.md
├── defects/
│   └── DEF-*.md
├── evidence/（软链接或压缩包）
└── manifest.json
~~~

---

## 11. 附录

### 11.1 附录 A：环境变量与端点速查

| 端点 | 方法 | 用途 |
|---|---|---|
| /api/health | GET | 健康检查 |
| /api/chat | POST | 多轮聊天（session_id/message） |
| /api/plan | POST | 创建计划（intent/session_id） |
| /api/plans/:id/execute | POST | 执行计划 |
| /api/plans/:id/cancel | POST | 取消计划 |
| /api/plans/:id/tasks/:taskId/retry | POST | 重试任务 |
| /api/memories | GET/POST | 列表/创建 |
| /api/memories/search | POST | 检索（返回 context） |
| /api/memories/export | GET | 导出全部 |
| /api/memories/import | POST | 导入 |
| /api/memories/stats | GET | 统计 |
| /api/memories/settings | GET/PUT | per-owner 设置 |
| /api/memories/consolidate | POST | 手动整合 |
| /api/wiki/papers | GET/POST | 论文列表/创建 |
| /api/wiki/papers/:id/points | GET/POST | Point 列表/创建 user_note |
| /api/wiki/papers/:id/links | GET | Link 列表（limit/cursor/status/type） |
| /api/wiki/links/:id/confirm | POST | 确认 |
| /api/wiki/links/:id/reject | POST | 拒绝 |
| /api/wiki/build | POST | 异步全量/增量构建 |
| /api/wiki/build/:jobID | GET | 构建状态 |
| /api/wiki/stats | GET | Wiki 统计 |

### 11.2 附录 B：记忆记录字段速查

| 字段 | 类型 | 说明 | 压测关注点 |
|---|---|---|---|
| id | string | 记录 ID | 稳定性、可引用性 |
| version | int | 版本 | supersede 链 |
| kind | enum | profile/fact/episode/procedure/constraint/negative/document/working/reflection | 抽取类型正确 |
| scope | object | owner/project/session/plan/agent | 隔离边界 |
| title/content | string | 标题/正文 | 300/8000 限制 |
| tags | []string | wiki_point/wiki_link/paper:* | 视图正确 |
| entities | []Entity | paper/repo/dataset/metric | 实体召回 |
| source | object | type/ref/url/created_at | 来源可审计 |
| evidence | object | class/confidence/sha256/hidden | 信任与隐藏 |
| refs | []SourceRef | document/page/section/quote/artifact/task/plan | quote 定位 |
| validity | object | valid_from/valid_to/superseded_by | 时效 |
| importance/confidence/trust/status | — | 排序与治理 | 权重正确 |
| content_hash | string | 去重键 | 不含 refs 的风险 |
| use_count/last_used_at | — | 使用统计 | 重复写入检测 |

### 11.3 附录 C：测试数据生成器接口

~~~text
tools/stress/
├── gen_needles.py          # 按 suite 生成针
├── gen_long_paper.py       # 生成 320 页 PDF
├── gen_cross_paper.py      # 生成 120 篇元数据 + 300 边
├── gen_long_task.py        # 生成 200 轮脚本
├── run_suite.py            # 执行单个 suite
├── score.py                # 评分
├── record_proxy.py         # 记录型代理
└── fixtures/
    ├── attention-is-all-you-need.pdf
    └── apriori-1994.pdf
~~~

### 11.4 附录 D：已知限制与预期失败清单

| 编号 | 预期失败项 | 依据 | 处理 |
|---|---|---|---|
| K-01 | 聊天摘要只覆盖被丢弃轮次中最前面的 10 条 turn（约 5 对） | chat_store.go SummarizeChatTurns | 若 LT-02 失败，登记 S1 缺陷；修复前不 blocking 其他功能 |
| K-02 | 任务链路不读 per-owner settings | executor.go DefaultSettings | RB-09 预期失败；登记 S2 |
| K-03 | WIKI_BUDGET_TOKENS/LINK_MODE/MEMORY_*_BUDGET 未接线 | settings.go/retrieve.go | RB-06/07/08 记录“无效果”，登记 S2 |
| K-04 | 中文 wiki 匹配退化 | matchPapers | LP-24/CP-25 可预期失败；建议 P1 修复 |
| K-05 | 200 篇上限 | PaperStore.List | CP-12 预期失败；登记 S1 |
| K-06 | 内容哈希不含 paper_id | ContentHash | LP-07 预期失败；登记 S1 |
| K-07 | 24000 字符摘要上限 | documents.Excerpt | LP-03 记录实际覆盖；不 blocking |
| K-08 | W4 embedding 未实现 | 设计默认关闭 | 不在本方案范围 |
| K-09 | 多副本/跨机器不支持 | 设计边界 | 不在本方案范围 |
| K-10 | 模型本身随机性 | provider | 每用例至少 3 次取多数，失败需人工复核 |

### 11.5 附录 E：代码审阅证据索引

| 证据 | 文件:行 | 支撑的风险 |
|---|---|---|
| SummarizeChatTurns 只取被丢弃轮次的前 10 条 turn | backend/internal/memory/chat_store.go:259-287 | R-01/R-02 |
| ContextTurns 20 轮/32000 token | backend/internal/api/chat_runtime.go:81 | R-01 |
| DefaultSettings 用于 task | backend/internal/scheduler/executor.go:331 | R-04 |
| chat 不读环境 MEMORY_MODE | backend/internal/api/chat_runtime.go:103-108 | R-05 |
| matchQuery project 过滤条件 | backend/internal/memory/retrieve.go:80-85 | R-07/R-08 |
| scopeWeight 固定值 | backend/internal/memory/retrieve.go:132-143 | R-09 |
| mmrSelect perKindMax=3 | backend/internal/memory/retrieve.go:292-326 | R-10 |
| BM25 候选 50 | backend/internal/memory/retrieve.go:227 | R-11 |
| renderWithBudget 静默截断 | backend/internal/memory/retrieve.go:419-452 | R-12 |
| ContentHash 字段 | backend/internal/memory/types.go:196-201 | R-13 |
| Put 去重返回旧记录 | backend/internal/memory/store.go:219-231 | R-14 |
| List 无 offset | backend/internal/memory/store.go:361-401 | R-15 |
| 内存库回退 | backend/internal/api/memory_runtime.go:90-94 | R-16 |
| replayWAL 首条坏行 break | backend/internal/memory/store.go:153-190 | R-17 |
| compactLocked 全量写 | backend/internal/memory/store.go:479-510 | R-18 |
| wikiBuildState.running | backend/internal/api/wiki.go:392-438 | R-19/R-39 |
| RebuildOwner Limit 200 | backend/internal/wiki/builder.go:257-276 | R-20 |
| Excerpt 24000 | backend/internal/documents/document.go:52-54、338-346 | R-21 |
| matchPapers 空格分词 | backend/internal/wiki/retrieve.go:91-136 | R-22/R-23 |
| BudgetTokens 不读 override | backend/internal/wiki/retrieve.go:13-35 | R-24 |
| LinkMode 仅解析 | backend/internal/wiki/settings.go:44-51 | R-25 |
| linksForKey 5000 扫描 | backend/internal/wiki/links.go:331-348 | R-26 |
| refreshPaperLinks 500 | backend/internal/wiki/links.go:361-381 | R-27 |
| 实体候选 500 | backend/internal/wiki/links.go:156-186 | R-28 |
| identity 合并规则 | backend/internal/wiki/identity.go:171-237 | R-29 |
| ScanInjection 只查 quote | backend/internal/memory/redact.go:76-107 | R-31 |
| Attributes 只查 string | backend/internal/memory/redact.go:96-105 | R-32 |
| 失败任务不刷 wiki | backend/internal/scheduler/executor.go:357-372 | R-33 |
| 取消路径 RecordPlan | backend/internal/scheduler/scheduler_control.go:71-73 | R-35 |
| messages 数组路径 | backend/internal/api/chat_runtime.go:71-83、161-172 | R-36 |
| settings.Enabled 只跳 memory | backend/internal/api/chat_runtime.go:102-152 | R-37 |
| WikiBuilder 用 MemoryStore | backend/internal/api/wiki_runtime.go:61-77 | R-38 |
| PaperStore.List 200 上限 | backend/internal/wiki/store.go:277-333 | R-40 |

### 11.6 附录 F：执行前必须确认的开放问题

| 编号 | 问题 | 建议默认 | 影响 |
|---|---|---|---|
| Q-01 | 压测使用真实 provider 还是 mock 为主？ | 结构用 mock、质量用真实 | 成本与可重复性 |
| Q-02 | 是否允许修改生产 compose 数据？ | 否，全部独立 data 目录 | 风险 |
| Q-03 | 1 万篇数据是否保留？ | 保留 7 天用于复测，之后归档 | 磁盘 |
| Q-04 | W3/W2.5 是否默认开启？ | 压测期开启，报告中标注 | 覆盖度 |
| Q-05 | 是否需要前端 UI 取证？ | 关键路径截图，不做像素级 | 工作量 |
| Q-06 | 多语言（中英）比例？ | 50/50 | 覆盖 CJK |
| Q-07 | 是否测试桌面端？ | 是，LC/RB 在 ENV-A 跑一遍 | 平台差异 |
| Q-08 | 缺陷修复是否阻塞报告？ | S0/S1 阻塞；S2/S3 可带缺陷发布 | 发布节奏 |

### 11.7 附录 G：一页纸执行清单（可打印）

~~~text
[ ] 1. 记录 commit，建 run_id，生成 manifest
[ ] 2. 启动记录型代理 + mock + backend（独立 data）
[ ] 3. 跑 MB（26）→ LC（16）→ SC（16）
[ ] 4. 跑 S1 长任务 72h（LT 30）
[ ] 5. 跑 S2 长论文（LP 26）
[ ] 6. 跑 S3 交叉论文（CP 27）
[ ] 7. 跑 PF（12）
[ ] 8. 跑 RB（13）
[ ] 9. 跑 S4 组合浸泡 7 天（含 PF-12）
[ ] 10. 回归所有 S0/S1 缺陷用例 3 次
[ ] 11. 生成 8 份 scorecard + 最终 REPORT
[ ] 12. 归档证据包与 manifest，清理临时 owner
~~~

---

## 12. 附：可执行的"压测冒烟"最小脚本建议（P0 快用例）

> 本方案 166 条全量执行需要 25 个工作日。为了让压测**每天都能跑**，建议把下面 12 条压成 30–45 分钟的快用例接入 CI（nightly）。它们覆盖了 12 类错误中最容易回归的 6 类（E1/E2/E3/E4/E6/E7/E9/E11/E12 均有覆盖）。

| 顺序 | 快用例 | 来源 | 预计耗时 | 覆盖错误类型 |
|---|---|---|---|---|
| 1 | 200 轮压缩召回（mock LLM） | LT-01、LT-02、LT-03 | 6 min | E1、E7 |
| 2 | 知识更新 3 次 + supersede | LT-04、MB-13 | 2 min | E4 |
| 3 | 跨 owner/project/session 三隔离 | LT-15、LT-16、SC-01~03 | 3 min | E3 |
| 4 | 计划 A→B parent + episode | LT-07、LT-08 | 2 min | E2、E5 |
| 5 | kill -9 + 重启对账 | LC-01、LC-03、LC-04 | 5 min | E11 |
| 6 | 2000 条 compaction | LC-05、MB-26 | 4 min | E10、E11 |
| 7 | wiki 三别名 + 对抗对 | CP-01~03 | 3 min | E2 |
| 8 | 120 篇 Link 构建 + P/R 粗检 | CP-04、CP-05、CP-13 | 5 min | E2、E10 |
| 9 | 删除 wiki 重建 + 决策保留 | CP-06、CP-07、LC-15 | 3 min | E9 |
| 10 | quote 100 条定位 | LP-05 | 2 min | E6 |
| 11 | prompt 顺序 + 预算 | MB-06~08、LT-24 | 2 min | E7、E8 |
| 12 | hidden/secret/注入 | SC-04~09、SC-14 | 3 min | E12 |

**CI 接入方式：**

~~~bash
# nightly job（容器内）
cd research-assistant/backend && go build -o /tmp/ra-backend ./cmd/api
python tools/stress/run_suite.py --suite smoke --backend /tmp/ra-backend --seed 20261110 --out /tmp/stress-smoke
python tools/stress/score.py --input /tmp/stress-smoke --gate smoke
~~~

**冒烟门禁：** 12 条全过才允许合并到 master；任一失败自动创建缺陷单草稿并 @ 值班。

---

## 13. 附：长任务/长论文/交叉论文的"记忆错误"典型现场（用于培训执行人员）

> 以下 10 个现场来自设计评审与现有测试报告中已暴露的真实问题模式，用于训练执行人员识别“看起来正常、其实错了”的场景。每条都给出：现象、如何 5 分钟内确认、根因线索。

### 13.1 长任务现场

| # | 现象 | 5 分钟确认法 | 根因线索 |
|---|---|---|---|
| 1 | 第 150 轮问“我的项目基线是多少”，模型答“您没有提供” | 看该轮 prompt 是否有 [retrieved memory] 与针；再看 export 中针是否还在 | 摘要只覆盖前 10 条 turn；或 MMR 把 episode 挤掉 |
| 2 | 计划 B 的 parent 指向 A 的第一版而不是最新版 | GET /api/plans 按 UpdatedAt 排序，核对 parent 的 id 与最新 revision | findParentPlan 只按 UpdatedAt，revision 更新不改变 UpdatedAt 时可能选错 |
| 3 | 同一任务失败 5 次，export 里只有 1 条 mem-task | 查 UseCount 是否=5；查 UpdatedAt 是否被反复抬高 | ContentHash 命中后只 UseCount++，原记录内容/Refs 不更新 |
| 4 | 取消计划后 paper page 的 reproduction 仍是 running | GET /api/wiki/papers/:id 看 reproduction.status | 取消路径的 RecordPlan 是否走 apiMemorySink；RefreshForPlan 是否被调用 |

### 13.2 长论文现场

| # | 现象 | 5 分钟确认法 | 根因线索 |
|---|---|---|---|
| 5 | 问第 280 页的结论，回答引用了第 30 页 | 看 citation.page 与 quote；用 LocateQuote 复核 | quote 来自摘要前 24k；或 LLM 自行编造页码 |
| 6 | 40 节论文的 PaperPage 只显示 20 节 | GET /api/wiki/papers/:id 看 sections 长度 | builder.loadSections 截断 20 |
| 7 | 中文问“这篇论文的方法”，答的是另一篇 | 看该轮 [wiki context] 的 paper title | matchPapers 无 CJK 分词 → 无命中 → 回退最近 2 篇 |

### 13.3 交叉论文现场

| # | 现象 | 5 分钟确认法 | 根因线索 |
|---|---|---|---|
| 8 | A 和 B 两篇不同论文被合成一个 PaperPage | 比较 aliases.json 中两条 alias 的 canonical key | 同标题同年被 title+year 合并；无作者校验 |
| 9 | 两篇论文都有“我们使用 WMT14”这一句，但只有第一篇有 result Point | 查第二篇的 point_ids 是否为空；查 export 中该文本的 content_hash | ContentHash 不含 paper_id，第二条 Put 命中第一条 |
| 10 | 220 篇论文 full build 后，最早建的 20 篇没有任何 Link | 查 build job 的 papers 数是否=200；查最早 20 篇的 entities | RebuildOwner/BuildLinksForPaper 的 Limit: 200，且 List 按 UpdatedAt 倒序 |

**培训要求：** 每个执行人员在上岗前必须能独立复现上述 10 个现场中的 6 个，并写出对应的 evidence 路径。

---

## 14. 附：术语表

| 术语 | 定义 |
|---|---|
| 记忆针（Needle） | 一条带唯一 ID 与期望答案的 ground-truth 记忆，用于检测召回与引用 |
| 注入（Injection） | 把检索到的记忆渲染成 prompt 中的一段文本 |
| Core Memory | 常驻 prompt 的稳定记忆块（pinned），预算默认 512 token |
| Retrieved Memory | 按查询动态检索的记忆块，预算默认 2000 token（chat） |
| Wiki Context | Paper Wiki 的论文页/Point/Link 渲染块，独立预算 2%/3% |
| Point | memory.Record 的 wiki 视图（Tags 含 wiki_point），不是独立存储 |
| Link | Kind=fact、Tags 含 wiki_link 的 memory.Record，带 from/to/type/evidence |
| PaperPage | 可删除、可重建的论文聚合投影缓存 |
| ContentHash | kind + scope + title + content 的 sha256，用于去重 |
| Supersede | 同 ID 内容更新时旧记录置 superseded，保留历史 |
| Rebuild | 删除 wiki 目录后从 memory + documents 重新物化 PaperPage |
| Cross-talk | 一个作用域的记忆出现在另一个作用域的检索/注入中 |
| Shadow Mode | 只检索/构建、不注入的观察模式 |
| Expected Fail | 已知限制导致的预期失败，不计入缺陷但必须登记 |

---

## 15. 附：文档修订记录

| 版本 | 日期 | 修订人 | 变更 |
|---|---|---|---|
| v1.0 | 2026-11-03 | 测试负责人 | 首版：166 条用例、4 套剧本、12 类错误、40 条代码风险、CI 冒烟 12 条 |
|  |  |  |  |

> 修订本方案时必须同步更新：用例 CSV、docs/README.md 索引、以及第 2 节的代码基线（若代码已变化）。任何用例增删都要更新 5 节的“166 条”统计与 7.3 的 P0 分布。
