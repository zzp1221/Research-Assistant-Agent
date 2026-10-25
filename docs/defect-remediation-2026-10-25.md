# Research Assistant Agent 缺陷修复文档（根因 · 影响 · 方案）

- **文档版本**：v1.1（2026-10-25 复核修订）
- **日期**：2026-10-25
- **代码基线**：`master @ 4b8df45`（容器内后端镜像构建于 2026-10-24 21:23，前端 22:17）
- **证据来源**：[reports/eval-2026-10-25/REPORT.md](../reports/eval-2026-10-25/REPORT.md) 第 1–8 节、`summary.json` 的 `root_cause_analysis`、`pipeline-log-evidence.txt`、`scorecard.json`
- **文档性质**：**修复设计**。只描述「为什么修 / 哪里有问题 / 怎么修」，不含已落地的代码改动；落地时按第 4 节拆分提交。

> **v1.1 修订说明**：本版依据对基线代码与 `reports/eval-2026-10-25/` 已保存证据的逐项复核，修订了三类问题：① 更正 6 处事实/数字（fallback 次数、Apriori 标题长度、测试样本行数、Attention 抽取结论、D2 的 arXiv 前提、D6 的重试现状）；② 修复全部相对链接（文档位于 `docs/`，代码与证据链接需 `../` 前缀）与多处行号引用；③ 消除 D5/D6/D7/D8/D9/D10 方案中的自相矛盾或不可执行点。缺陷集合、分层与总体结论不变。

---

## 0. 摘要

本轮四场景测试（绘图 / 算法性能对比 / 论文分析 / 论文复现）共暴露 **10 个缺陷**。它们不是 10 个孤立 bug，而是沿着同一条链路分布的 5 类问题：**输入理解 → 资源获取 → 规划契约 → 执行环境 → 结果判定**。

| 编号 | 缺陷 | 用户看到的现象 | 根因（一句话） | 严重度 | 修复量 |
|---|---|---|---|---|---|
| D1 | 论文标题被抽成机构地址 | 复现上传论文时，系统拿「Institute for Mathematical Sciences, Imperial College London...」去搜仓库 | 标题抽取是「取最长的像标题的行」，而不是「取标题」 | P1 | S |
| D2 | 仓库检索选不到 | `repo_discovery` 失败，后续 11 个节点全部不执行，计划停在 2/14 | 「先选任意 URL」阻断「GitHub 回退」，「可信过滤」又否掉「先选结果」，两次判据不一致；arXiv ID 还因版本后缀未被解析 | P1 | M |
| D3 | 「对比…性能」被判成框架评测 | 生成 `Framework A` / `Framework B` 的空壳基准计划，与问题无关 | 关键词规则先于语义判断，且 rule 路径跳过了 `postValidate` | P1 | S |
| D4 | `bisect` 装不上 | 纯标准库任务在 `install_dependencies` 失败，报 `You must give at least one requirement to install` | stdlib 白名单只有 37 项；依赖被删空后仍继续执行空参数 pip | P1 | S |
| D5 | planner 静默回退 | 只要计划含绘图或依赖安装节点，LLM 规划就可能被拒、静默换成模板（11 次日志） | registry 与 `validateCriticalNodeContracts` 两套产物契约不同源；默认值只补空不补缺；回退不写元数据 | P2 | M |
| D6 | 主张验证 0% 覆盖仍判成功 | 证据图为空、13–16 条主张不可验证，但计划显示 `smoke` / `completed` | `degraded` 状态不参与 plan outcome 计算 | P2 | S |
| D7 | 数据语义不校验 | 用「节点→部门标签」的文件当边表，跑出 1005 个节点、21 个「社区」 | `repo_prepare` 只 clone 不看数据形状 | P2 | M |
| D8 | 子目录入口不可执行 | 入口在 `src/` 的仓库必然 `No such file` | 入口命令只用 `filepath.Base`，丢失子目录 | P2 | S |
| D9 | 回答变成评估报告 | 用户问 A，回答是「实验目标 / 执行过程 / 核心指标分析」 | `verify_result` 复用了「数据分析师」prompt | P3 | S |
| D10 | 同一问题计划形状不稳定 | 同样的论文分析，有时 3 节点、有时 5 节点（模板兜底则是 2 节点） | `General` + 论文没有确定性 builder，claim 节点约束只写在 `Paper_Reproduction` 规则里 | P3 | S |

> 修复量：S ≈ 单文件 + 单测；M ≈ 2–3 个文件 + 契约调整；L ≈ 跨模块（本轮没有）。

> **严重度口径**：本文用 P1–P3 表示**用户影响**（P1 阻断主链路或高频误判，P2 静默错误/结果不可信，P3 体验与稳定性）。`REPORT.md` §8.9 的 P0–P2 是上一版评估口径，与本文不完全对应，落地与排期以本文表格与 §4.1 为准。

### 0.1 任务清单（可直接当 issue 拆分）

- [ ] **D1** `documents/metadata.go`：标题候选从「最长」改为「位置 + 结构特征」；补 3 个真实 PDF fixture 回归
- [ ] **D2** `scheduler/repo_discovery.go`：合并 `firstSelectedRepo` / `firstTrustedRepo` 为一次选择；找不到时 `blocked` 而非空 artifact；arXiv 正则支持版本后缀并打通 `paper_metadata.arxiv_id`
- [ ] **D3** `intent/resolver.go`：所有 `return rule` 走 `postValidate`；拆 `evalKeywords`，`benchmark` 移出框架语境
- [ ] **D4** `agent/coder.go`：stdlib 表改为生成文件；依赖删空后短路；`pipInstallArgs` 防御空参数
- [ ] **D5** `planner/`：registry 成为唯一产物契约；`applyPlannerNodeDefaults` 按 registry 补齐；回退写 plan 元数据
- [ ] **D6** `planner/semantic_validation.go` + `agent/claim_evidence.go`：`degraded` → `needs_review`（任务状态保持 completed）
- [ ] **D7** `scheduler/repo_prepare.go`：新增数据形状探测与 gate；图算法 recipe 要求 `edge_list`
- [ ] **D8** `agent/reproduction_spec.go`：入口改用 workspace 相对路径，抽出公共路径解析函数
- [ ] **D9** `prompts/prompts.go` + `agent/answer.go`：`verify_result` 复用 `AnswerSystemPrompt` / `AnswerAgent`
- [ ] **D10** `planner/planner.go`：`General` + 论文走确定性 builder；限制 claim 节点只在 `Paper_Reproduction`

### 0.2 证据等级与修订记录

本文对关键结论标注证据等级：`[E]` 已保存评测产物（`reports/eval-2026-10-25/`）、`[C]` 基线源码直接可读、`[R]` 本地复算（未运行 Go 单测）、`[I]` 由错误信息反推。全部代码位置以基线 `4b8df45` 为准。

v1.1 的具体修订：

1. **D1**：Apriori 标题长度 41 → **44** 字符；`metadata_test.go` 样本 6 → **7** 行；更正「Attention 恰好最长」——按同一提取器与规则复算，Attention 首页会选中 80 字符的版权说明行，**同样抽错** `[R]`。
2. **D2**：补充 arXiv 版本后缀根因——`\b\d{4}\.\d{4,5}\b` 不匹配 `0803.0476v2`，且 `paper_metadata.arxiv_id` 未进入 `repo_discovery` 输入 `[C]`。
3. **D4**：修正 `applyRuleBasedDependencyFallback` 行号 648-660 → **763-784**；补充「Python 3.11.13 容器实测 305/217/缺 181」的版本绑定 `[E]`。
4. **D5**：fallback 次数 12 → **11**（8 + 2 + 1），并注明 `REPORT.md` §8.5 的「5 次」为旧口径；修正「只有 General 能通过」的过度概括；补充输入产物契约的迁移条件 `[E][C]`。
5. **D6**：更正「一次失败即降级」——`llmclient` 已默认 3 次尝试，4b/4c 的 `status_reason` 原文是 `LLM request failed after 3 attempt(s): context deadline exceeded`；方案 A 不再推荐 `StatusBlocked`（与 scheduler 冲突）`[E][C]`。
6. **D7**：artifact 更名 `dataset_shape_profile`，避开已存在的 `dataset_profile` 任务类型 `[C]`。
7. **D8**：说明 `paperDebugWorkspaceEntry` 不能直接复用（输入字段不同），需先抽公共函数 `[C]`。
8. **D9**：复用已有 `AnswerSystemPrompt` / `AnswerAgent`，不新增第四套同义 prompt；修正 `prompts.go` 行号 26-40 → **26-34** `[C]`。
9. **D10**：行号 1582-1594 → **1889-1893**；补充 claim rubric 的取舍说明 `[C]`。
10. **结构**：§4.2 提交顺序按 §4.1 阶段重排；§6 验收按「单测 / 集成 / fixture」分类；相对链接统一加 `../`。

---

## 1. 为什么要修

### 1.1 四个测试的真实结果说明问题在哪一层

| 测试 | 结果 | 暴露的问题 |
|---|---|---|
| 1. 反正弦 vs 反余弦对比图 | ✅ PASS（6/6，真实 PNG 1200×900） | 无（D5 只影响规划路径，不影响最终产物） |
| 2. Python 二分 vs 摩尔排序性能 | ✅ PASS，但花了 **3 次尝试** | D3 误路由 → D4 stdlib 依赖失败 → 才成功 |
| 3. 论文创新点与局限性 | ✅ 内容 PASS / ⚠️ 元数据 WARN | D1 标题和作者都抽错 |
| 4a. 复现上传论文（原始措辞） | ❌ **FAIL（2/14 节点）** | D1 + D2 叠加：用地址搜仓库 → 空 `repo_url` → 11 个下游节点无法执行 |
| 4b. 复现（显式标题+仓库，人工修补） | ⚠️ smoke PASS（非产品自动修复） | D6 证据图 0% 覆盖 |
| 4c. 复现（显式标题+仓库，自动修复） | ⚠️ smoke PASS / 科学有效性 BLOCKED | D6 + D7：数据是标签表却当边表 |

注意一个关键区别：**4a 是「可见的失败」，而 D6 / D7 是「静默的错误」**。后者更危险——用户看到 `completed`，实际上什么都没验证。修复的优先级不应只看失败次数，而要看「错误是否会被用户当成正确结果」。

### 1.2 不修的代价

**（1）用户信任成本。** 4a 的失败信息是 `task ... produced empty artifact "repo_url"`——普通用户既看不懂，也不知道下一步该做什么。而 4c 显示「14/14 完成」，实际跑的是错误数据。前者让人放弃产品，后者让人得出错误结论，后者更严重。

**（2）能力退化不可见。** 本轮日志里 `fallback to template planner` 共出现 **11 次**（`pipeline-log-evidence.txt` 逐行统计：`render_plot` 8 次、`result_visualization` 2 次、`install_dependencies` 1 次），全部集中在带这三类节点的计划上。用户看到的功能是模板提供的，一旦模板没覆盖新场景（例如新的任务类型），系统不会「降级」，而是直接失败。这条链路对绘图类与复现类计划实际上是**死的**，只是被模板掩盖了。

**（3）修复成本随时间上升。** D1 的「取最长行」被 Apriori 的旧用例「碰巧」掩盖（Apriori 首页标题 44 字符恰好是首页最长合格行），没有回归测试保护；Attention 首页同样会抽错，只是历史评测从未断言过 `paper_metadata.title`。任何新的论文 PDF 都可能再次触发。D2 的选择逻辑与 `catalog/repositories.json`（只有 2 个条目）耦合，新增论文时问题只会更多。

### 1.3 修复后要达到的状态

沿用项目已有的五条原则，并补两条本轮新增的：

1. **Fail closed，不 fail open。** 找不到仓库、数据语义不符、证据无法验证，都必须明确 `blocked` / `needs_review`，不能伪装成 `completed`。
2. **单一事实来源。** 任务类型的产物契约只在 `taskregistry.go` 定义一次，planner 默认值、critical contract、scheduler 校验都从它派生。
3. **结构化选择，不靠字符串最长/最短。** 标题、仓库、数据集都必须有明确的判定函数与可解释的 `reason`。
4. **降级必须可见。** 任何 fallback（模板计划、规则兜底、mock）都要写进 plan 元数据并在前端展示。
5. **语义验收与结构验收同等重要。** 「节点完成 + artifact 存在」只是最低条件。
6. **（新增）输入层要有置信度。** `PaperMetadata` 这类抽取结果应携带 `source` / `confidence`，低置信度要触发二次校验而不是直接下传。
7. **（新增）错误信息要可行动。** `produced empty artifact "repo_url"` 应变成「未找到可信仓库，请提供 GitHub URL 或 arXiv ID」。

### 1.4 不在本次范围

- 不扩充 `catalog/repositories.json` 到大规模论文库（D2 的修复不依赖它）。
- 不重写 scheduler / 事件总线 / 前端框架。
- 不引入新的外部服务；D1 的兜底只使用已有的 `paper_parse` LLM 能力。
- 不改变 API 路径与鉴权方式。

---

## 2. 缺陷分层：一条链路上的五个断点

```
用户请求
   │
   ├─ ① 输入理解层
   │     D1 论文元数据抽取（标题/作者）
   │     D3 意图识别（Framework_Evaluation vs Code_Execution）
   │
   ├─ ② 资源获取层
   │     D2 仓库检索与选择
   │
   ├─ ③ 规划契约层
   │     D5 task registry / planner 默认值 / critical contract 不一致
   │     D10 General + 论文没有确定性 builder
   │
   ├─ ④ 执行环境层
   │     D4 stdlib 依赖识别与空依赖处理
   │     D8 复现入口路径
   │     D7 数据集语义
   │
   └─ ⑤ 结果判定层
         D6 claim evidence degraded 不参与 outcome
         D9 verify_result 输出错类型
```

这个分层决定了修复顺序：**上游错一个字段，下游会放大成整条链路失败**。D1 一个 79 字符的地址，最终让 4a 停在 2/14（11 个下游节点未执行）；D3 一个关键词，导致 test2 生成 12 节点空壳计划。所以先修 ①②，再修 ③④，最后修 ⑤。

---

## 3. 逐项说明：为什么修 / 哪里出现了问题 / 解决方案

### D1 论文元数据抽取：把「最长的一行」当成了标题

#### 3.1.1 为什么必须修

论文元数据是整条论文链路的**第一个输入**。`paper_metadata.title` 会被直接写进 `paper_title` / `paper_search_query`（[plan_runtime.go:149-157](../research-assistant/backend/internal/api/plan_runtime.go#L149-L157)），而 `repo_discovery` 用它当检索词。标题错一个词，仓库检索就完全偏离；测试 4a 的 11 个下游节点因此无法执行，源头就是这一个字段。

它还会污染第二处：`paper_parse` 在输入没有 `paper_metadata` 时会自己再调一次 `ExtractMetadata`（[librarian.go:168-172](../research-assistant/backend/internal/agent/librarian.go#L168-L172)），同一个错误会被独立复现两次，无法靠「换个入口」绕开。

#### 3.1.2 哪里出了问题

**核心位置**：[metadata.go:44-53](../research-assistant/backend/internal/documents/metadata.go#L44-L53)

```go
title := ""
titleIndex := -1
for i := 0; i < limit; i++ {
	line := normalized[i]
	if !plausibleTitleLine(line) { continue }
	if title == "" || len([]rune(line)) > len([]rune(title)) {
		title = line          // ← 规则：取「像标题」的行中最长的一条
		titleIndex = i
	}
}
```

**为什么这条规则在这里失效**：[plausibleTitleLine](../research-assistant/backend/internal/documents/metadata.go#L98-L115) 是一份黑名单，只排除 `proceedings / conference / symposium / copyright / permission / ibm / research center / university / @ / http / www / doi / arxiv / keywords / abstract`。它没有排除 `Institute / College / Department / Campus / Université / avenue / street / Road` 这类**机构与地址词**。

对后端真实保存的 `document.json`（pdftotext 提取，2,760 字符）逐行套用同一规则，合格候选 8 条 `[E]`：

| 行号 | 长度 | 内容 |
|---:|---:|---|
| 0 | 47 | `Fast unfolding of communities in large networks` ← 真标题 |
| 2 | 57 | `Vincent D. Blondel1;a , Jean-Loup Guillaume1,2;b , Renaud` |
| 3 | 36 | `Lambiotte1,3;c and Etienne Lefebvre1` |
| 5 | 76 | `Department of Mathematical Engineering, Université catholique de Louvain, 4` |
| 6 | 57 | `avenue Georges Lemaitre, B-1348 Louvain-la-Neuve, Belgium` |
| 8 | 77 | `LIP6, Université Pierre et Marie Curie, 4 place Jussieu, 75005 Paris, France` |
| 10 | **79** | **`Institute for Mathematical Sciences, Imperial College London, 53 Prince's Gate,`** ← 实际选中 |
| 11 | 35 | `South Kensington campus, SW72PG, UK` |

79 > 47，第 10 行胜出。随后 `titleIndex` 指向第 10 行，作者候选从第 11 行开始，第一个通过 `plausibleAuthorLine` 的是 `South Kensington campus, SW72PG, UK`（[metadata.go:58-69](../research-assistant/backend/internal/documents/metadata.go#L58-L69)），作者也错。即使外部把标题覆盖正确（`test3-title-guided-paper-metadata.json`），作者仍是 `["South Kensington campus","SW72PG","UK"]`，说明标题与作者必须分别修 `[E]`。

**为什么之前没暴露**：Apriori 首页标题 `Fast Algorithms for Mining Association Rules` 是 **44** 字符，恰好是首页最长合格行，所以旧用例通过只是巧合。Attention 并不安全：用同一提取器（`pdftotext -layout`）与同一规则对仓库内 `attention-is-all-you-need.pdf` 首页复算，合格候选 7 条，胜出的是 80 字符的版权说明行 `reproduce the tables and figures in this paper solely for use in journalistic or`，标题 `Attention Is All You Need`（25 字符）落选 `[R]`。**D1 的影响面比原文档描述的更大：三篇论文里至少两篇会抽错。**

**为什么测试没抓到**：[metadata_test.go](../research-assistant/backend/internal/documents/metadata_test.go) 只有一个用例，输入是手工裁剪的 **7 行**文本，标题天然最长。

**缺少兜底**：[context_resolver.go:64-74](../research-assistant/backend/internal/api/context_resolver.go#L64-L74) 直接采用 `ExtractMetadata` 的结果，只在用户 prompt 里写了《…》时才覆盖标题（[extractQuotedPaperTitle](../research-assistant/backend/internal/api/routes.go#L519)）。没有置信度、没有二次校验、没有 LLM 兜底。

#### 3.1.3 解决方案

**（A）把「最长行」改成「标题评分 + 位置优先」**，新增 `scoreTitleCandidate(line, index, total)`：

| 特征 | 分值 | 理由 |
|---|---:|---|
| 位于首页前 40% | +40 | 标题几乎总在页面上部 |
| 前一行含 arXiv ID / 后一行像作者名 | +20 | 标题的典型上下邻居 |
| 长度 20–120 字符 | +15 | 标题的可读长度 |
| 含 `and` / 逗号分隔的人名模式 | −40 | 更像作者行 |
| 命中机构/地址词（institute, college, department, campus, université, avenue, street, road, 邮编） | −80 | 直接压到不可能胜出 |
| 以数字开头 | −60 | 地址行常见（如 `3 Institute...`） |
| 命中版权/许可/「provided proper attribution」等声明词 | −80 | Attention 这类首页的前置版权块 |

取最高分作为标题起点；若下一行也命中标题特征且不是新句子开头，合并为多行标题（解决换行标题）。

**（B）作者行单独判定**：`plausibleAuthorLine` 增加正特征（含 `and` / 多个逗号 / 人名大小写模式），不再只靠黑名单。

**（C）给结果加置信度**：`models.PaperMetadata` 增加 `Source string`（`rule` / `llm` / `user`）与 `Confidence float64`。规则得分低于阈值时，`paper_parse` 用现有 LLM 对首页文本做一次结构化「标题/作者」抽取，与规则结果比对；仍不一致则写 `paper_metadata_warnings`，并把计划降级为 `needs_review`（不阻断，论文分析本身仍可进行）。

**（D）必须补的回归测试**（新增 `documents/testdata/` 下真实首页文本 fixture）：

1. `louvain-2008.page1.txt` → title = `Fast unfolding of communities in large networks`，authors 至少含 `Blondel`、`Guillaume`、`Lambiotte`、`Lefebvre`；
2. `apriori-1994.page1.txt` → 现有断言不变（标题 44 字符）；
3. `attention-2017.page1.txt` → title = `Attention Is All You Need`，authors[0] = `Ashish Vaswani`（必须覆盖首页版权块场景）；
4. 反向用例：只有机构地址行、没有标题时，必须返回 error 而不是返回地址。

**验收标准**：三个真实 PDF 的 `paper_metadata.title` 全部正确；`repo_discovery` 的 `paper_title` 与论文标题逐字一致；单测覆盖 3 个正向 + 1 个反向。

---

### D2 仓库检索：不是搜不到，是两次选择互相抵消

#### 3.2.1 为什么必须修

`repo_discovery` 是论文复现链路的**唯一入口**。它失败后 11 个下游节点全部无法执行，测试 4a 停在 2/14。更严重的是失败方式：任务先被标成 `completed`、再因为空 artifact 被 scheduler 判 `failed`（[scheduler.go:705-735](../research-assistant/backend/internal/scheduler/scheduler.go#L705-L735)，空值判定在 [:734](../research-assistant/backend/internal/scheduler/scheduler.go#L734)），用户看到的是「任务失败」而不是「没找到可信仓库，请提供 URL」。

#### 3.2.2 哪里出了问题

**（0）前置条件：arXiv ID 根本没进入检索** `[C]`。Louvain 首页第一行是 `arXiv:0803.0476v2 [physics.soc-ph]`，但 [repo_discovery.go:29](../research-assistant/backend/internal/scheduler/repo_discovery.go#L29)、[metadata.go:11](../research-assistant/backend/internal/documents/metadata.go#L11)、[routes.go:38](../research-assistant/backend/internal/api/routes.go#L38) 三处共用的正则都是 `\b\d{4}\.\d{4,5}\b`，**不匹配带版本后缀的 `0803.0476v2`**，因此 `PaperMetadata.ArxivID` 为空（`test3-exact-paper-metadata.json` 中无 `arxiv_id` 字段）`[E]`。更关键的是，[buildRepoDiscoveryInputs:1427](../research-assistant/backend/internal/planner/planner.go#L1427) 只读 `entities["paper_arxiv_id"]`，不会从 `paper_metadata.arxiv_id` 提升；于是 `repo_discovery` 只能退化为按标题检索。**即使下面的选择逻辑修好，arXiv 路径仍然不通。**

**（1）两次选择互相抵消**，核心在 [repo_discovery.go:128-154](../research-assistant/backend/internal/scheduler/repo_discovery.go#L128-L154)：

```go
selected := firstSelectedRepo(candidates)        // 128：取第一个「有任意仓库」的候选
fallbackUsed := false
if selected == "" {                              // 130：只有完全没选中才走 GitHub 回退
	githubCandidates, ghErr := githubRepoSearch(...)
	...
}
...
sort.SliceStable(candidates, ...)                // 147
strictTrustedSelection := arxivIDRe.FindString(query) == ""  // 148
if strictTrustedSelection {
	selected = firstTrustedRepo(query, candidates)   // 150：只接受「标题核心词命中」的候选
} else {
	selected = firstSelectedRepo(candidates)
}
```

两个函数判据完全不同：

- [firstSelectedRepo](../research-assistant/backend/internal/scheduler/repo_discovery.go#L455-L462)：`len(RepoURLs) > 0` 就返回第一个，**不看相关性**；
- [firstTrustedRepo](../research-assistant/backend/internal/scheduler/repo_discovery.go#L464-L475)：必须通过 [isTrustedRepoCandidate](../research-assistant/backend/internal/scheduler/repo_discovery.go#L477-L518) 的标题 token 匹配。

于是形成死锁：HF 返回的 5 篇论文里，`2301.13187`（Weighted Flow Diffusion）带 `s-h-yang/wfd`，`1910.09483` 带 `HanbaekLyu/motif_sampling`，都与 Louvain 无关。`firstSelectedRepo` 取到第一个非空 URL，`selected != ""`，GitHub 回退**根本不会执行**；随后 `firstTrustedRepo` 又因为这些仓库不匹配标题而全部否决，`selected` 变回空串。

**实测证据**（后端同一网络命名空间内直接调用包内函数）`[E]`：

```
FIRST_SELECTED="https://github.com/s-h-yang/wfd"
  0803.0476 | Fast unfolding of communities in large networks | repos=[]
  1810.08473 | From Louvain to Leiden... | repos=[]
  1909.13021 | Multi-scale Attributed Node Embedding | repos=[]
  2301.13187 | Weighted Flow Diffusion... | repos=[https://github.com/s-h-yang/wfd]
  1910.09483 | Sampling random graph homomorphisms... | repos=[https://github.com/HanbaekLyu/motif_sampling]
FIRST_TRUSTED_HF=""
```

而 GitHub 回退本身完全可用：

```
GITHUB ERR=<nil> CANDIDATES=3
  Woodstock-z/Louvain | Louvain（Fast unfolding of communities in large networks） | score=15
  xiafan68/comdetection | python version of "Fast unfolding of community hierarchies..." | score=12
  AO33/communityStructure_louvainMethod | ... | score=12
TRUSTED="https://github.com/Woodstock-z/Louvain"
```

**附带问题**：[completeRepoDiscoveryTask:158-175](../research-assistant/backend/internal/scheduler/repo_discovery.go#L158-L175) 在 `selected == ""` 时仍把 `repo_url: ""` 写进 artifact，把「没找到」伪装成「产物为空」，最终由 scheduler 报技术性错误。

**结构性诱因**：[catalog/repositories.json](../research-assistant/backend/internal/catalog/repositories.json) 只有 2 个条目（Attention、Apriori），任何新论文都只能依赖在线检索，而在线检索恰好被上面的死锁挡住。

#### 3.2.3 解决方案

**（A）合并为一次选择**，删除 `firstSelectedRepo` 的独立使用，新增 `selectRepo(query, candidates) (repo, reason)`：

```text
1. 过滤：archived / fork / 空 URL / 非 github.com 域名
2. 打分（0-100）：
   - 标题 token 命中率 × 50（复用 significantTokens）
   - 仓库名含论文方法词 × 20
   - star 数（log 归一化）× 10
   - curated catalog 命中 → 直接置顶
   - 来源权重：user_preference 1000 / curated 100 / hf 5 / github 3
3. 取最高分；低于阈值（如 50）视为「无候选」
4. 返回 (url, "matched_tokens=4/5; source=github_search; score=88")
```

**（B）GitHub 回退条件改为「没有可信候选」**，而不是「`selected == ""`」：

```go
selected, reason := selectRepo(query, candidates)
if selected == "" {
	githubCandidates, ghErr := githubRepoSearch(...)
	candidates = append(candidates, githubCandidates...)
	selected, reason = selectRepo(query, candidates)
}
```

**（C）arXiv 解析与输入打通** `[C]`：`arxivIDRe` 改为 `\b\d{4}\.\d{4,5}(?:v\d+)?\b`（三处共用常量应抽到公共包，避免再次漂移）；`buildRepoDiscoveryInputs` 在 `paper_arxiv_id` 缺失时，从 `paper_metadata` 读取 `arxiv_id` 并写入 `paper_arxiv_id`。同时把 `paper_search_query` 的优先级保持为「用户显式给出 > arXiv ID > 标题 > 方法名」。

**（D）找不到时 `blocked` 而不是空 artifact**：写 `MissingRequirement{Kind: "repo_url", Remediation: "请提供论文的 GitHub 仓库 URL，或补充 arXiv ID"}`，`repo_validation_report` 保留候选与拒绝原因。scheduler 不再需要靠空 artifact 报错。

**（E）`repo_validation_report` 结构化**：从纯文本改为 JSON（`query / candidates[] / selected / reason / fallback_chain`），前端可渲染「为什么选它 / 为什么都不选」。

**验收标准**：Louvain 论文（有 `arXiv:0803.0476v2`、无 curated 条目）能解析出 `0803.0476` 并作为 `paper_arxiv_id` 传入，自动选出 `Woodstock-z/Louvain` 或 `AO33/communityStructure_louvainMethod`；GitHub 搜索 mock 为空时任务必须 `blocked` 且带 `MissingRequirement.remediation`；Attention / Apriori 命中 curated 的行为不变。

---

### D3 意图识别：「对比/性能」不等于「框架评测」

#### 3.3.1 为什么必须修

意图是**计划选择的唯一开关**：`IntentType` 直接决定走哪个 `PlanBuilder`（[plan_builder.go:41-77](../research-assistant/backend/internal/planner/plan_builder.go#L41-L77)）。一旦判成 `Framework_Evaluation`，系统会生成 `Framework A` / `Framework B` 两条并行分支（[planner.go:1360-1365](../research-assistant/backend/internal/planner/planner.go#L1360-L1365) 的 `fallbackFrameworkNames`），跑的是两个空壳 mock，与用户问的「二分查找 vs Boyer–Moore」毫无关系。

测试 2 第一次尝试就撞上它，用户看到的是 12 节点、两个 `Framework A/B` 占位符的计划。

#### 3.3.2 哪里出了问题

**（1）关键词表把「泛对比词」和「框架语境」混在一起**：[routes.go:355-363](../research-assistant/backend/internal/api/routes.go#L355-L363)

```go
evalKeywords := []string{
	"对比", "比较", "评估", "选型", "哪个好", "哪个更", "区别", "异同",
	"rag", "langchain", "llamaindex", "llama_index", "haystack",
	"dspy", "autogen", "crewai", "langgraph", "framework",
	"框架", "测试两", "比一比", "pk", "vs",
}
if containsAny(intent, evalKeywords) { return "Framework_Evaluation" }
```

`对比一下python中二分法和摩尔排序的性能` 命中「对比」，直接进入框架评测，与是否出现框架名无关。

**（2）`postValidate` 本来能修好，但规则路径根本没调用它**：[resolver.go:50-90](../research-assistant/backend/internal/intent/resolver.go#L50-L90) 中 `Resolve` 共有 **8 处 `return rule`**（`r == nil`、`ModeRule`（含空 mode）、`ModeShadow`、未知 mode、`rule_fast_path`、`Classifier == nil`、分类器报错、置信度不足），全部直接返回；只有 LLM 成功合并后才走 [postValidate](../research-assistant/backend/internal/intent/resolver.go#L155-L199)。包内实测：

```
RAW="对比一下python中二分法和摩尔排序的性能"
  rule=Framework_Evaluation   postValidate=General   frameworkCtx=false
  resolve_on_classifier_error=Framework_Evaluation
```

即 `postValidate` 会把 `Framework_Evaluation` 降级为 `General`（[resolver.go:175-183](../research-assistant/backend/internal/intent/resolver.go#L175-L183)），但这条路径永远走不到。

**（3）LLM 的正确判断被「高风险保护」丢弃**：直连 intent 服务 `/predict`，模型对同一句给出 `Custom_Benchmark`（confidence 0.86，`needs_benchmark=true`）。而 [resolver.go:128-133](../research-assistant/backend/internal/intent/resolver.go#L128-L133)：

```go
if (llmType == "AutoResearch" || llmType == "Custom_Benchmark") && rule.IntentType != llmType {
	log.Printf("[intent] model proposed high-risk intent %s without rule support, keeping rule intent %s", ...)
	return rule    // ← 直接丢弃 LLM 结论，且不经过 postValidate
}
```

这条保护本身合理（防止模型把普通问题升级成昂贵的 benchmark），但结果是：规则层的错误 `Framework_Evaluation` 被保留，LLM 的正确方向被当成越权丢弃。

**（4）`benchmark` 被同时当成「框架语境」**：[keywords.go:10-13](../research-assistant/backend/internal/intent/keywords.go#L10-L13) 的 `frameworkKeywords` 含 `benchmark`，导致任何带「benchmark」的句子都无法被 `postValidate` 降级。

#### 3.3.3 解决方案

**（A）所有出口统一走 `postValidate`**（最小、最高杠杆的改动）：

```go
func (r *Resolver) Resolve(...) models.IntentContext {
	// 每个 return rule 改为 return postValidate(rule)
}
```

`postValidate` 是幂等的（只做实体归一化与意图降级/升级），重复调用安全。

**（B）拆分关键词语义**：

| 常量 | 内容 | 用途 |
|---|---|---|
| `frameworkNames` | langchain / llamaindex / haystack / dspy / autogen / crewai / langgraph | 唯一能强触发 `Framework_Evaluation` 的信号 |
| `frameworkContextWords` | 框架 / framework / 选型 / 多框架对比 | 需与 `frameworkNames` 同时出现才升级 |
| `comparisonWords` | 对比 / 比较 / 区别 / 异同 / vs / pk | 只表示「想比较」，不决定意图 |
| `benchmarkWords` | benchmark / 性能 / 耗时 / 延迟 / 吞吐 | 只表示「要测指标」，可驱动 `needs_benchmark` |

`DetectIntentType` 改为：

```text
if reproKeywords                                  -> Paper_Reproduction
if plot && no frameworkNames                       -> Code_Execution
if frameworkNames >= 1 && (comparison || benchmark) -> Framework_Evaluation
if benchmarkWords && codeKeywords                  -> Code_Execution   // 性能对比是代码任务
if paperAnalysisWords                              -> General
if codeKeywords                                    -> Code_Execution
default                                            -> General
```

**（C）`ContainsFrameworkContext` 只看框架名与「框架」二字**，从 `frameworkKeywords` 删除 `benchmark` / `选型`。

**（D）高风险保护改为「先 postValidate 再判断」**：

```go
merged := postValidate(merge(rule, classified))
if merged.IntentType == "Custom_Benchmark" && len(uploadedFiles) == 0 {
	merged.IntentType = "Code_Execution"   // 无数据文件的自定义 benchmark 就是代码任务
}
```

**验收标准**：`对比一下python中二分法和摩尔排序的性能` → `Code_Execution`；`对比 LangChain 和 LlamaIndex 的 RAG 性能` → `Framework_Evaluation`；`帮我画一个正弦和余弦的对比图` → `Code_Execution`；分类器超时/不可用时结果与正常一致（都走 `postValidate`）。

---

### D4 依赖安装：白名单只有 37 项，且删空后继续 pip

#### 3.4.1 为什么必须修

任何只用标准库的代码任务（二分查找、排序、图算法……）都会在第一步 `install_dependencies` 失败。这不是「某个包缺失」，而是**整类任务不可用**。测试 2 第一次尝试因此失败，用户需要自己猜出「加个 numpy 才能过」这种反直觉的 workaround。

#### 3.4.2 哪里出了问题

**（1）stdlib 白名单严重不全**：[coder.go:950-956](../research-assistant/backend/internal/agent/coder.go#L950-L956)

```go
var pythonStandardLibraryModules = map[string]struct{}{
	"abc": {}, "argparse": {}, "asyncio": {}, "base64": {}, "collections": {}, "contextlib": {}, "copy": {}, "csv": {}, "dataclasses": {}, "datetime": {}, "functools": {},
	"hashlib": {}, "io": {}, "itertools": {}, "json": {}, "logging": {}, "math": {}, "os": {}, "pathlib": {}, "random": {},
	"re": {}, "statistics": {}, "string": {}, "subprocess": {}, "sys": {}, "tempfile": {}, "time": {}, "typing": {}, "uuid": {}, "warnings": {},
	"__future__": {}, "codecs": {}, "glob": {}, "inspect": {}, "shutil": {}, "tarfile": {}, "urllib": {},
}
```

共 **37** 项（这 37 项确实都是标准库，没有误收录）。对照 **`python:3.11-bullseye` 容器内实测**的 Python 3.11.13 `sys.stdlib_module_names`（305 项，其中不含下划线的公开顶层模块 217 项），**缺 181 项** `[E]`，包括 `bisect`、`heapq`、`array`、`ast`、`enum`、`decimal`、`sqlite3`、`ssl`、`http`、`email`、`xml`、`multiprocessing`、`concurrent` 等。数字随 Python 版本变化（本机 3.14.7 为 297/194），**引用时必须绑定镜像版本**。

**（2）删空依赖后没有短路**：[coder.go:633-648](../research-assistant/backend/internal/agent/coder.go#L633-L648)

```go
if plan, reactErr := a.planDependencyRecovery(ctx, dependencies, errText); reactErr == nil && plan.Action != "" {
	nextDependencies, nextRuntimeSession, changed, applyErr := a.applyDependencyRecoveryPlan(...)
	if applyErr == nil && changed {
		dependencies = nextDependencies    // ← 可能变成空列表
		continue                            // ← 直接进入下一轮 pip
	}
}
```

`remove_package` 把唯一依赖 `bisect` 删掉后，`dependencies` 为空，但循环没有重新检查 `len(dependencies)==0`，下一轮 [pipInstallArgs()](../research-assistant/backend/internal/agent/coder.go#L1778-L1784) 只拼出 `python3 -m pip install --default-timeout ... --index-url ...`，pip 报 `ERROR: You must give at least one requirement to install`。

函数入口处的早退分支（[coder.go:547-580](../research-assistant/backend/internal/agent/coder.go#L547-L580)）写得是对的，但它只在**进入循环之前**检查一次。

**失败日志**（以下为 `REPORT.md` §8.4 记录的后端日志摘录 `[E]`；`test2-bisect-stdlib-failure-plan.json` 只保留了 `install_dependencies` 的最终错误，原始 pip 输出未单独落盘，复跑时应补存事件流）：

```
识别到依赖: bisect
pip install ... bisect  →  ERROR: No matching distribution found for bisect  (exit 1)
ReAct repair applied: action=remove_package, reason=bisect 是 Python 标准库模块
pip install ...（空参数）  →  ERROR: You must give at least one requirement to install  (exit 1)
ReAct repair returned no effective change, fallback to rules.
→ task failed
```

#### 3.4.3 解决方案

**（A）stdlib 列表改为「生成 + 校验」**，不再手写：

- 新增 `internal/agent/stdlib_modules.txt` 与生成脚本，从**固定版本**的 Python 镜像导出（`python3 -c "import sys; print(chr(10).join(sorted(sys.stdlib_module_names)))"`）；
- 生成物随代码提交，构建时不依赖 Python；
- CI 增加一致性测试：容器内 `python3.11` 导出的集合与 `stdlib_modules.txt` 完全一致，否则失败。

**（B）把「空依赖」收敛成一个函数**：

```go
func (a *CoderAgent) completeWithoutDependencies(task *models.Task, runtimeSession, reason string) error {
	task.Result = reason
	task.Status = models.StatusCompleted
	// 写 prepared_runtime / dependency_install_report
	return nil
}
```

在**每一处** `dependencies` 被赋值之后调用：入口解析后、过滤后、ReAct 应用后、规则兜底后。

**（C）`pipInstallArgs` 防御空参数**：返回 `([]string, error)`，空列表直接报错，避免「空 pip」这种低级错误再次进入日志。

**（D）规则兜底同步修复**：`applyRuleBasedDependencyFallback`（[coder.go:763-784](../research-assistant/backend/internal/agent/coder.go#L763-L784)，调用点在 [coder.go:648-660](../research-assistant/backend/internal/agent/coder.go#L648-L660)）在 `dropDependencyByRoot`（[coder.go:867](../research-assistant/backend/internal/agent/coder.go#L867)）之后同样调用 `completeWithoutDependencies`。

**验收标准**：`dependency_spec=["bisect"]` → 任务 `completed`、`dependency_install_report=no external dependencies detected`；`["bisect","numpy"]` → 只装 numpy；`["not-a-real-package"]` → 仍然失败且错误信息包含包名。

---

### D5 规划契约：三套规则各说各话，LLM 计划动辄被拒

#### 3.5.1 为什么必须修

`PlannerAgent`（LLM）是「理解新场景」的唯一路径，模板只能覆盖已知场景。本轮日志里 `fallback to template planner` 出现 **11 次**（8 次 `render_plot`、2 次 `result_visualization`、1 次 `install_dependencies`）`[E]`，全部是计划里的产物契约没写全、被 registry 或 critical contract 判死后回退。能通过校验的是 LLM 恰好把产物写全的计划（例如 test2b 的 7 节点 `Code_Execution`，`install_dependencies` 明确输出 `prepared_runtime`）`[E]`。这意味着：**凡是涉及绘图或复现的「智能规划」，只要产物契约不完整就退化为模板**，本轮 11 次回退全部属于这一类。

更麻烦的是它**静默**：[BuildPlan:59-70](../research-assistant/backend/internal/planner/planner.go#L59-L70) 捕获错误后只 `logPlannerFallback`，计划里没有任何字段说明「这是模板」。

#### 3.5.2 哪里出了问题

同一件事（一个任务类型必须产出哪些 artifact）被定义了三遍，且互相不一致：

| 位置 | `render_plot` | `install_dependencies` |
|---|---|---|
| [taskregistry.go:259-263](../research-assistant/backend/internal/planner/taskregistry.go#L259-L263) | `RequiredOutputArtifacts: ["plot_image"]` | 无（[taskregistry.go:206-210](../research-assistant/backend/internal/planner/taskregistry.go#L206-L210) 只有 AllowedAgents/ExecutionKind） |
| [agent_planner.go:499-507](../research-assistant/backend/internal/planner/agent_planner.go#L499-L507) | 只在 `len(OutputArtifacts)==0` 时补 `plot_image` | 不补（仅 [agent_planner.go:426](../research-assistant/backend/internal/planner/agent_planner.go#L426) 设 `AssignedTo`） |
| [planner.go:1824-1834](../research-assistant/backend/internal/planner/planner.go#L1824-L1834) | 无专项检查（依赖 registry） | 强制要求 `prepared_runtime` |

[validateTaskNode:343-346](../research-assistant/backend/internal/planner/taskregistry.go#L343-L346) 用 registry 的 `RequiredOutputArtifacts` 判死；[validateCriticalNodeContracts:1833](../research-assistant/backend/internal/planner/planner.go#L1833) 又单独要求 `prepared_runtime`。

**实际触发链路**：LLM 给出 `render_plot` 的 `output_artifacts: ["generated_code", "plot_report"]`（非空、但不含 `plot_image`）→ 默认值不补 → registry 判 `must output artifact "plot_image"` → `BuildPlan` 捕获 → 模板兜底。`install_dependencies` 同理。该 blueprint 未单独落盘，**属由错误信息反推的典型形态** `[I]`；复跑时应保存被拒的原始 blueprint。

**实测日志**（11 次回退，3 类错误）`[E]`：

```
... err=task type "render_plot" must output artifact "plot_image"              ×8
... err=task type "result_visualization" must output artifact "result_plot"   ×2
... err=install_dependencies node "... / Install Dependencies" must output prepared_runtime ×1
```

> `REPORT.md` §8.5 写的是「5 次」，与 `pipeline-log-evidence.txt` 中实际的 11 行不一致；本文以日志文件逐行统计为准（11 次）。

#### 3.5.3 解决方案

**（A）registry 成为唯一产物契约**，补齐缺失项：

```go
"install_dependencies": {
	AllowedAgents:           []string{"sandbox_agent"},
	ExecutionKind:           ExecutionReal,
	RequiredOutputArtifacts: []string{"prepared_runtime", "dependency_install_report"},
}
```

**（B）`applyPlannerNodeDefaults` 从 registry 派生**，而不是手写 switch：

```go
func applyPlannerNodeDefaults(bp plannerNodeBlueprint) plannerNodeBlueprint {
	spec, ok := lookupTaskTypeSpec(bp.Type)
	if !ok { return bp }
	bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, spec.RequiredOutputArtifacts...)
	return bp
}
```

这样 `render_plot` 无论 LLM 写了什么，最终一定包含 `plot_image`（`ensureArtifacts` 是追加语义，见 [agent_planner.go:512-524](../research-assistant/backend/internal/planner/agent_planner.go#L512-L524)）。

**（C）删除 `validateCriticalNodeContracts` 中与 registry 重复的**输出**检查**，只保留结构性约束（必须存在某类节点、依赖顺序）与**输入产物**检查。注意：`TaskTypeSpec` 目前只有 `RequiredOutputArtifacts`（[taskregistry.go:46-54](../research-assistant/backend/internal/planner/taskregistry.go#L46-L54)），没有输入产物字段；若要把输入要求也收敛到 registry，必须先给 `TaskTypeSpec` 增加 `RequiredInputArtifacts []string` 并迁移全部 42 个活跃条目，否则不要删除 `validateCriticalNodeContracts` 中现有的 `RequiredArtifacts` 检查。

**（D）回退必须可见**：`PlanGraph.Meta` 增加 `planner_source`（`agent` / `template`）与 `planner_fallback_reason`；前端计划卡片显示「模板规划」徽标；API 返回里也带。`GraphMeta` 目前只有节点计数（[graph.go:66-73](../research-assistant/backend/internal/models/graph.go#L66-L73)），需要同步扩展。

**（E）契约测试**：遍历 registry 所有活跃 task type，构造「只有必填字段、输出产物为空」的 blueprint，断言 `applyPlannerNodeDefaults` 之后 `validateTaskNode` 全部通过。这条测试能防止未来新增 task type 时再出现同类不一致。

**验收标准**：先在基线记录 6 条固定提示词 × 3 次运行的 `planner_source` 分布（当前 `GraphMeta` 无该字段，方案 D 落地后才可测）；修复后同批提示词 `planner_source=agent` ≥ 80%，且每次回退都带 `planner_fallback_reason`；故意写错 `output_artifacts` 的 blueprint 能补齐而不是判死；前端可见回退徽标。

---

### D6 主张验证：0% 覆盖仍然判「成功」

#### 3.6.1 为什么必须修

`claim_evidence_build` 是论文复现的**科学有效性闸门**：它决定「论文声称的结论到底被证据支持了多少」。当前实现在 LLM 超时时把 `status` 设为 `degraded`，但任务仍然 `completed`、计划仍然是 `smoke`，用户看到的是一份「复现成功」的报告，里面 13–16 条主张全部不可验证、覆盖率 0%。**这比失败更糟，因为它会让人相信一个没有验证过的结论。**

#### 3.6.2 哪里出了问题

**（1）降级状态照常完成任务**：[claim_evidence.go:118-146](../research-assistant/backend/internal/agent/claim_evidence.go#L118-L146) 里，超时、JSON 解析失败、证据契约违规三种情况都把 `status = "degraded"`；但同函数末尾 [claim_evidence.go:164-170](../research-assistant/backend/internal/agent/claim_evidence.go#L164-L170) 无条件执行：

```go
task.Result = report
task.StructuredData = string(graphPayload)
task.Status = models.StatusCompleted      // ← degraded 也 completed
setClaimArtifactValues(task, map[string]string{...})
```

**（2）outcome 计算完全不看证据图**：[semantic_validation.go:122-164](../research-assistant/backend/internal/planner/semantic_validation.go#L122-L164) 的 `PlanFinalizer.Finalize` 只检查：计划失败、节点失败、框架 mock、smoke 模式、附件覆盖、General 最终答案。它没有读取 `claim_evidence_graph.status`，因此 `degraded` 完全不影响 `plan.Outcome`。

**（3）根因在上游，但重试已经存在**：LLM 网关 `api.ai-pixel.online` 间歇性返回 `unexpected EOF` / `context deadline exceeded`。`llmclient` 默认已是 **3 次尝试、500ms 起指数退避、单次 180s 超时**（[llmclient.go:31-38](../research-assistant/backend/internal/llmclient/llmclient.go#L31-L38)）；4b/4c 的 `status_reason` 原文是 `LLM request failed after 3 attempt(s): context deadline exceeded` `[E]`。所以问题不是「没有重试」，而是**每次尝试都耗到超时、整体 deadline 先到**，以及降级后没有重评估机制。

#### 3.6.3 解决方案

**（A）任务状态保持不变，语义交给 outcome**：`executeClaimEvidenceBuild` 仍把任务保持 `StatusCompleted`（`claim_evidence_graph` / `claim_verification_report` 是有效产物），但在 `task.Metadata` 写 `degraded_reason`，由 `PlanFinalizer` 统一降级 outcome。

**不要**把任务标成 `StatusBlocked`：scheduler 的 `hasFailure` 把 blocked / canceled 当作失败（[scheduler.go:1149-1156](../research-assistant/backend/internal/scheduler/scheduler.go#L1149-L1156)），计划会直接 `OutcomeFailed`，且 `PlanFinalizer` 只在计划仍为 completed 时执行（[scheduler.go:274-289](../research-assistant/backend/internal/scheduler/scheduler.go#L274-L289)），这与验收要求的 `needs_review` **直接冲突**。若确实需要 blocked 语义，必须先改 `hasFailure` / 终态映射，让 blocked → `OutcomeNeedsReview`，并补 scheduler 级单测。

**（B）`PlanFinalizer` 增加证据闸门**：

```go
if graph := claimEvidenceGraph(plan); graph != nil {
	if graph.Status != "assessed" {
		plan.Outcome = models.OutcomeNeedsReview
		plan.OutcomeReason = "claim evidence graph is " + graph.Status + ": " + graph.StatusReason
		return nil
	}
	if graph.Summary.Verified == 0 && len(graph.Claims) > 0 {
		plan.Outcome = models.OutcomeNeedsReview
		plan.OutcomeReason = "no claim was verified against evidence"
		return nil
	}
}
```

注意顺序：这段应放在 smoke 判定**之前**，否则 `smoke` 会先返回。

**（C）超时与重评**：① 区分错误类型——`context deadline exceeded` 与 429/5xx 分开处理，前者应缩短单次 `AttemptTimeout` 或提高整体 deadline，而不是重复同一超时；② `StatusReason` 保留原文（当前已保留），前端展示「证据验证未完成（模型服务不可用）」而不是「已完成」；③ 可选：证据图已落盘，模型恢复后异步重跑 adjudication（不重跑执行）。

**（D）单测**：构造三种 graph（`assessed` / `degraded` / 空 claims），断言 Finalize 分别得到 `smoke`、`needs_review`、`needs_review`。

**验收标准**：本次 4b/4c 的计划 outcome 必须变成 `needs_review`，任务状态仍为 `completed`；LLM 正常时仍为 `smoke`（smoke 是运行模式，不是证据状态）。

---

### D7 数据语义：标签表被当成边表

#### 3.7.1 为什么必须修

4c 的「成功」是假的：`email-Eu-core-department-labels.txt` 是 **1,005 行、每行「节点 ID + 部门标签」**（第一列 0–1004 唯一，第二列 0–41），不是边表。仓库代码的 `construct_graph` 按 `u v` 解析，于是把「每个节点连到自己的部门编号」当成边，得到 1,005 个节点、21 个「社区」。这个结果既不是 Louvain 的社区划分，也不是任何有意义的图。**产品没有任何一个环节发现它。**

#### 3.7.2 哪里出了问题

- [executeRepoPrepare:61](../research-assistant/backend/internal/scheduler/repo_prepare.go#L61) 只做 clone、扫描依赖/代码文件、选入口（[scanRepositoryWorkspace:1082](../research-assistant/backend/internal/scheduler/repo_prepare.go#L1082)、[choosePreferredCodeFile:1120](../research-assistant/backend/internal/scheduler/repo_prepare.go#L1120)），**不检查数据文件结构**；
- [maybeCreateReproductionSmokeRunner:307](../research-assistant/backend/internal/scheduler/repo_prepare.go#L307) 的 recipe 匹配（[smokeRecipeMatches:348](../research-assistant/backend/internal/scheduler/repo_prepare.go#L348)）只看文件名与任务文本关键词，[recipes.json](../research-assistant/backend/internal/catalog/recipes.json) 里也没有任何「数据形状」声明；
- `paper_code_execute` 的修复 harness 只看 exit code 与 stdout，不判断「这个输出是否可能是标签表的产物」。

因此整条链路对「数据集语义」是**盲的**。

#### 3.7.3 解决方案

**（A）新增轻量数据形状探测**（`scheduler/dataset_probe.go`）：

```text
输入：workspace 内的 .txt/.csv/.tsv/.edges 文件（限定 5MB 内）
输出：DatasetShape{
  rows, cols,
  col0_unique_ratio, col1_max, col1_unique,
  looks_like_edge_list,     // 两列均为整数、col0/col1 有交叉、无单调递增
  looks_like_node_label,    // col0 唯一且 == 行数、col1 取值数 << rows 且 <= 100
  looks_like_feature_matrix // 多列浮点
}
```

判定规则：`col0_unique_ratio == 1.0 && col1_unique <= 100 && col1_max < rows/2` → `node_label`。

**（B）在 `repo_prepare` 输出 `dataset_shape_profile` artifact**（形状 + 判定 + 证据行样本），并让 `paper_code_execute` 的 recipe 声明所需形状：

```json
{ "id": "louvain-community", "requires_dataset_shape": "edge_list", "reject_shapes": ["node_label"] }
```

> 不复用已有任务类型 `dataset_profile`（[taskregistry.go:122-126](../research-assistant/backend/internal/planner/taskregistry.go#L122-L126)，registry 的 `AllowedAgents` 为 `research_coding_agent`，而 [benchmark_agent.go:27](../research-assistant/backend/internal/agent/benchmark_agent.go#L27) 与 [research_coding_agent.go:50](../research-assistant/backend/internal/agent/research_coding_agent.go#L50) 的执行入口都有 `dataset_profile` 分支）——那是 benchmark 的数据集画像，与复现数据形状不是同一语义。若坚持复用，需在 registry 与两处 agent 分派中显式合并语义并改测试。

**（C）形状不符时 `blocked`**，写 `MissingRequirement{Kind:"dataset", Detail:"email-Eu-core-department-labels.txt 是节点→标签表，不是边表", Remediation:"请提供边表（两列节点 ID），或改用有 edges.txt 的仓库"}`，而不是继续执行。

**（D）LLM 辅助（可选）**：对无法规则判定的文件，让 `paper_parse` 的模型看前 20 行与仓库 README，输出「这个文件是边表/标签表/特征矩阵」；结果写入 `dataset_shape_profile` 供人工复核。

**验收标准**：4c 的数据集必须被判为 `node_label` 并 `blocked`；换成真实的边表（如 Karate club 的 78 条边）时正常执行；探测耗时 < 200ms（5MB 文件）。

---

### D8 复现入口：只用 basename，子目录入口必然找不到

#### 3.8.1 为什么必须修

复现入口决定了「跑哪个文件」。当前实现丢弃目录信息，**任何入口不在仓库根目录的仓库都会 100% 失败**。本次能跑通只是因为 Louvain 的 `main.py` 与 Apriori 的 `apriori.py` 恰好在根目录——这又是巧合。

#### 3.8.2 哪里出了问题

[reproduction_spec.go:42-50](../research-assistant/backend/internal/agent/reproduction_spec.go#L42-L50)：

```go
entry := filepath.Base(strings.TrimSpace(manifest.SelectedCodeFile))
if entry == "" || entry == "." {
	entry = "research-assistant_repro_smoke.py"
}
runCommand := []string{"python3", entry}
if manifest.ReproEntryKind == "apriori_frequent_itemsets" {
	runCommand = []string{"python3", "apriori.py", "-f", "INTEGRATED-DATASET.csv", "-s", "0.15", "-c", "0.6"}
	entry = "apriori.py"
}
```

单测实测：

```
输入 selected_code_file=/tmp/ws/src/main.py
输出 ENTRY="main.py"  RUN_COMMAND=[python3 main.py]  WORKDIR="/workspace"
```

`cd /workspace && python3 main.py` 必然 `No such file`。同时 `WorkingDir` 固定为 `/workspace`，也没有考虑「入口在 `src/` 子目录、需要把 `src` 加进 `PYTHONPATH`」的情况。

#### 3.8.3 解决方案

**（A）抽出公共的「workspace 相对路径」解析函数，而不是直接调用 `paperDebugWorkspaceEntry`**：后者已经实现「绝对路径 → workspace 相对路径 + 越界检查 + 后缀检查」（[paper_debug_harness.go:341-356](../research-assistant/backend/internal/agent/paper_debug_harness.go#L341-L356)），但它读取的是任务顶层的 `workspace_path` / `code_file_path`（[paper_debug_harness.go:342-343](../research-assistant/backend/internal/agent/paper_debug_harness.go#L342-L343)），而 `reproduction_spec` 的输入只有 `repo_manifest`（[reproduction_spec.go:30-33](../research-assistant/backend/internal/agent/reproduction_spec.go#L30-L33)）。因此需要：

1. 把核心逻辑抽成 `workspaceRelativeEntry(workspacePath, candidate string) (relative, abs string, err error)`（即现有 `paperDebugExistingPythonPath`，[paper_debug_harness.go:358-404](../research-assistant/backend/internal/agent/paper_debug_harness.go#L358-L404)）；
2. `executeReproductionSpec` 从 `manifest.WorkspacePath` / `manifest.SelectedCodeFile` 调用它；
3. `paperDebugWorkspaceEntry` 改为调用同一函数，保持现有行为与测试不变。

```go
relative, _, err := workspaceRelativeEntry(manifest.WorkspacePath, manifest.SelectedCodeFile)
if err != nil { ... }
runCommand := []string{"python3", filepath.ToSlash(relative)}
```

**（B）`WorkingDir` 与 `PYTHONPATH` 按入口所在目录推导**：若入口在 `src/`，则 `WorkingDir=/workspace`、`PYTHONPATH=/workspace/src`（或 `WorkingDir=/workspace/src`，二选一，需在 spec 里显式记录）。

**（C）`ReproductionRuntimeSpec` 增加 `EntrypointRelative` 字段**（保留 `Entrypoint` 兼容旧计划），并在 `runtime_validate` 阶段断言「该文件在镜像内存在」。

**（D）回归测试**：`selected_code_file=/ws/src/main.py` → `RUN_COMMAND=["python3","src/main.py"]`；`/ws/main.py` → `["python3","main.py"]`；`/etc/passwd` → 报错（越界）；`paperDebugWorkspaceEntry` 现有用例不回归。

**验收标准**：子目录入口仓库（如 transformers 示例、scikit-learn examples）能正常启动；越界路径被拒；旧计划（只有 `Entrypoint`）仍可执行。

---

### D9 回答类型：用户问 A，返回「评估报告」

#### 3.9.1 为什么必须修

`verify_result` 通常是代码/实验类计划的收尾节点（test2 是；test2b 的收尾是 `general_synthesis`），它的输出直接呈现给用户。当前它复用「数据分析师」系统提示词（[prompts.go:26-34](../research-assistant/backend/internal/prompts/prompts.go#L26-L34) 的 `DataSystemPrompt`），要求输出「实验目标 / 执行过程 / 核心指标分析 / 结论与建议」四段式报告，而用户问的是「二分法和摩尔排序谁快」。结果是一份 4,581 字的过程报告，结论埋在第三段。这是**产品语气问题，不是正确性问题**，但会显著影响可用性。

#### 3.9.2 哪里出了问题

[DataSystemPromptForTask:64-79](../research-assistant/backend/internal/prompts/prompts.go#L64-L79) 按 `intentType` 与 task 名分流，只有 `autoresearch` / `paper_reproduction` / `framework_evaluation` 三类有专用 prompt，其余全部落到 `DataSystemPrompt`。`verify_result` 不属于任何一类，因此拿到「评估报告」模板。

#### 3.9.3 解决方案

**（A）复用已有的 `AnswerSystemPrompt` / `AnswerAgent`，不新增第四套 prompt**：代码里已经有 [AnswerSystemPrompt](../research-assistant/backend/internal/prompts/prompts.go#L17-L24) 和 `AnswerAgent`（[answer.go:22-33](../research-assistant/backend/internal/agent/answer.go#L22-L33)），其规则正是「先结论后论据、不要输出实验目标/执行过程/核心指标分析、不得编造」。建议把 `verify_result` 路由到 `answer_agent`（或在 `DataSystemPromptForTask` 中对 `task == "verify_result"` 返回 `AnswerSystemPrompt`）。若最终仍要新增 `VerifyResultSystemPrompt`，必须说明它与 `AnswerSystemPrompt` 的差异及合并计划，避免两套同义 prompt 漂移。

**（B）若选择新增专用 prompt，最小规则如下**（内容与 `AnswerSystemPrompt` 基本重合，仅作兜底）：

```text
1. 第一段必须是直接结论（一句话回答用户的问题）
2. 第二段给出支撑数据（表格或要点，引用上游 artifact 的实际数值）
3. 第三段说明局限（样本、环境、未覆盖项）
4. 不要输出「实验目标 / 执行过程」这类流程标题
5. 不得编造上游没有的数据
```

**（C）前端**：`verify_result` 的 `verification_report` 默认折叠「过程细节」，展开显示完整报告。

**验收标准**：测试 2 的 `verification_report` 第一段是直接结论（如「二分查找比 Boyer–Moore 慢约 40 倍，每元素 0.8µs vs 19ns」这类量级），长度 < 1,500 字。

---

### D10 计划形状不稳定：同一问题 3 节点 / 5 节点都可能

#### 3.10.1 为什么必须修

同一句「分析一下这篇论文的主要创新点和局限性」，本次测试出现了两种计划：

| 运行 | 节点数 | 节点序列 |
|---|---:|---|
| 精确措辞（test3-exact） | 3 | paper_parse → general_research → general_synthesis |
| 加论文标题（test3） | 5 | paper_parse → claim_rubric_extract → general_research → verify_result → general_synthesis |

两次都不是模板形状（模板 `paperAnalysis` 固定 2 节点），可判定为 agent 生成；但当时 `GraphMeta` 未记录 `planner_source`，严格说只能由形状反推 `[E][C]`。

另外，模板兜底 [buildGeneralNodesV2:1304-1327](../research-assistant/backend/internal/planner/planner.go#L1304-L1327) 的 paperAnalysis 分支只有 2 节点（paper_parse → general_synthesis），与上面两种都不同——也就是说同一条链路存在三种合法形状。

计划形状不稳定会让：评测无法复现、成本不可预测（5 节点比 3 节点多约 60% 时间）、用户看到的流程随机。

#### 3.10.2 哪里出了问题

- [buildGeneralNodesV2:1304-1327](../research-assistant/backend/internal/planner/planner.go#L1304-L1327) 只有三个分支：`paperAnalysis`（2 节点）、summary/report（2 节点）、默认（3 节点）。它**没有 claim rubric、没有 verify_result**。
- 但 LLM planner 在 `General` 场景也可能自由添加 `claim_rubric_extract` / `verify_result`，因为 prompt 的 8.9/8.10 规则（[prompts.go:733-734](../research-assistant/backend/internal/prompts/prompts.go#L733-L734)）写的是「Every paper reproduction plan」，而模型把它理解成「凡是论文都要」。
- `validateAgentPlannedNodes`（[planner.go:1576-1657](../research-assistant/backend/internal/planner/planner.go#L1576-L1657)）只在 `Paper_Reproduction` 的 canonical 节点清单里强制 claim 节点（[planner.go:1889-1893](../research-assistant/backend/internal/planner/planner.go#L1889-L1893)），对 `General` 不校验，于是 LLM 加不加都合法 → 形状随机。
- 一旦 LLM 计划被拒（D5），`buildGeneralNodesV2` 的 `paperAnalysis` 分支只有 2 节点，形成「有时 5、有时 3、兜底 2」。

#### 3.10.3 解决方案

**（A）给 `General` + 论文场景一个确定性 builder**：在 `buildGeneralNodesV2` 的 `paperAnalysis` 分支里固定节点序列：

```text
paper_parse -> general_research -> verify_result -> general_synthesis
```

**（A′）取舍说明（必须先做 A/B 再定节点数）**：claim rubric 正是「提取论文主张」的节点，直接禁掉会让 General 论文分析失去结构化主张。若 A/B 评测表明 `general_research` 足以稳定覆盖「创新点/局限性」，采用 4 节点；否则保留一个轻量 `claim_rubric_extract`（**不含** `claim_evidence_build`），固定为 5 节点。二者择一，但必须以评测结果为依据，不能只按节点数少来选。

**（B）claim rubric 只属于 `Paper_Reproduction`**：把 prompt 8.9/8.10 改为「Only Paper_Reproduction plans may include claim_rubric_extract / claim_evidence_build」，并在 `validateAgentPlannedNodes` 增加反向断言：`General` 计划里出现 `claim_*` 节点 → 直接拒绝（而不是静默接受）。

**（C）计划形状快照测试**：对 6 条固定提示词（绘图 / 性能对比 / 论文分析×2 / 复现×2）断言节点类型序列完全相等，防止漂移。

**验收标准**：同一提示词 10 次调用产生完全相同的节点类型序列；`General` 计划不再出现 `claim_rubric_extract`（若 A/B 选择 5 节点方案，则允许且仅允许一个 `claim_rubric_extract`，不得出现 `claim_evidence_build`）。

---

## 4. 落地顺序与提交拆分

### 4.1 为什么是这个顺序

修复顺序由「错误传播距离」决定，而不是由严重度单独决定：

```text
Phase 0  D1 + D2        输入与资源：不修这两条，论文复现链路永远跑不通
Phase 1  D3 + D4        意图与依赖：不修这两条，代码类任务要靠用户绕路
Phase 2  D5 + D8 + D10  规划与执行契约：让 LLM 路径真正可用、入口可移植
Phase 3  D6 + D7 + D9   判定与呈现：让「成功」可信、让输出可读
```

Phase 0 内部也有顺序：**D1 先于 D2**。D1 修好后 D2 的测试才有意义（否则查询串本身是错的，无法判断选择逻辑是否修好）；D2 内部先修 arXiv 解析与输入打通，再修选择逻辑，否则「有 arXiv ID」的用例无法覆盖。

### 4.2 提交拆分建议

| 提交 | 内容 | 文件 | 验证 |
|---|---|---|---|
| `fix(documents): score title candidates by position and structure` | D1 | `documents/metadata.go` + `testdata/` + 测试 | 3 正向 + 1 反向 fixture |
| `feat(repo-discovery): single scored selection with blocked fallback` | D2 | `scheduler/repo_discovery.go` + 新 `selectRepo`；arXiv 正则抽公共包（`documents/metadata.go`、`api/routes.go`、`scheduler/repo_discovery.go`）；`planner/planner.go` 的 `buildRepoDiscoveryInputs` | Louvain 自动命中 + arXiv 版本后缀 + 空搜索 blocked |
| `fix(intent): always run postValidate and split eval keywords` | D3 | `intent/resolver.go`、`intent/keywords.go`、`api/routes.go` | 4 条提示词表驱动测试 |
| `fix(coder): generated stdlib list and empty-dependency short circuit` | D4 | `agent/stdlib_modules.txt`、`agent/coder.go` | `bisect` / `bisect+numpy` / 无效包 三例 |
| `fix(planner): derive node defaults from task registry` | D5 | `planner/agent_planner.go`、`planner/taskregistry.go`、`planner/planner.go`、`models/graph.go` | registry 全类型契约测试 + `planner_source` 元数据 |
| `fix(reproduction): resolve entry path relative to workspace` | D8 | `agent/reproduction_spec.go`、`agent/paper_debug_harness.go` | 3 条路径用例 + 现有 helper 不回归 |
| `fix(planner): deterministic General+paper builder` | D10 | `planner/planner.go`、`prompts/prompts.go` | 6 条提示词快照 + A/B 结论 |
| `fix(plan-outcome): treat degraded claim evidence as needs_review` | D6 | `planner/semantic_validation.go`、`agent/claim_evidence.go` | 3 种 graph 状态 + 任务状态保持 completed |
| `feat(repo-prepare): dataset shape probe and gate` | D7 | `scheduler/dataset_probe.go`、`scheduler/repo_prepare.go`、`catalog/recipes.json` | 标签表 blocked、边表通过 |
| `feat(prompts): reuse answer prompt for verify_result` | D9 | `prompts/prompts.go`、`agent/data.go`、`agent/answer.go` | 报告首段为结论 |

> 提交顺序按 §4.1 的阶段排列；同一 Phase 内按依赖关系（D1→D2、D5→D10）排序。每个提交都必须自带单测；跨提交的集成验证放在 4.3。

### 4.3 集成验收（对应四个测试的复跑）

| 验收项 | 期望 |
|---|---|
| 测试 1 绘图 | 计划 `planner_source=agent`（或模板但显示徽标），PNG 1200×900，双曲线像素校验通过 |
| 测试 2 性能对比 | 第一次尝试即 `Code_Execution`；无 `bisect` 安装失败；`verification_report` 首段为直接结论 |
| 测试 3 论文分析 | `paper_metadata.title` 正确；计划固定为 4 或 5 节点（按 D10 的 A/B 结论二选一），不再出现 3/5/2 漂移；回答带页码引用 |
| 测试 4a 原始措辞复现 | `repo_discovery` 自动找到仓库；不再 2/14 |
| 测试 4b/4c 复现 | 入口按 workspace 相对路径解析；数据集语义不符时 `blocked`；证据图 `degraded` 时 outcome=`needs_review` |

复跑脚本沿用 [reports/eval-2026-10-25/REPORT.md](../reports/eval-2026-10-25/REPORT.md) 第 1 节的方法（`POST /api/plan` → `POST /api/plans/:id/execute` → 轮询 `GET /api/plans/:id`），结果写入新的 `reports/eval-<date>/`。

---

## 5. 风险与回退

| 改动 | 风险 | 缓解 |
|---|---|---|
| D1 标题评分 | 新评分函数可能又对某类排版失效（双栏、LaTeX 作者块） | 保留旧逻辑为 fallback；三种真实 PDF 必须通过；低置信度走 LLM |
| D2 合并选择 | 打分阈值过严会让本来能用的 curated 命中失效 | curated 条目直接置顶绕过阈值；保留 `preferred_repo_url` 最高优先级 |
| D2 arXiv 正则放宽 | 可能匹配正文里的年份/编号 | 只匹配 `\d{4}\.\d{4,5}(v\d+)?`，并优先在首页 / `arXiv:` 前缀附近解析 |
| D3 关键词拆分 | 真实的框架对比请求可能被降级为 Code_Execution | 以 `frameworkNames` 为强信号；补 8 条正负例测试 |
| D4 生成 stdlib 表 | Python 版本升级后表过期 | CI 一致性测试；表随镜像版本更新 |
| D5 默认值从 registry 派生 | 某些 LLM 计划可能多出不想要的 artifact | `ensureArtifacts` 只追加不去重；多余 artifact 由 scheduler 忽略 |
| D6 outcome 降级 | 正常 smoke 复现可能被误判 `needs_review` | 只在 `status != assessed` 时降级；任务状态保持 completed，避免 `hasFailure` 把计划判 failed |
| D7 数据探测 | 边表被误判为标签表 | 判定要求「col0 唯一 + col1 取值数很少」双条件；不确定时只警告不阻断 |
| D8 入口路径 | 旧计划里 `Entrypoint` 是 basename，新逻辑可能找不到 | 保留 `Entrypoint` 字段；新字段 `EntrypointRelative` 优先，缺失时回退旧逻辑 |

**统一回退开关**：以上改动都通过配置项控制（如 `PLANNER_DEFAULTS_FROM_REGISTRY=true`、`REPO_DISCOVERY_SCORING=v2`），出问题时可以单条回退而不需要回滚整个版本。

---

## 6. 验收清单（Definition of Done）

- [ ] 10 个缺陷各自有对应的验证，且全部通过：单测（D1/D3/D4/D5/D8/D9/D10）、集成测试（D2/D6）、fixture 驱动（D7）
- [ ] 三个真实 PDF fixture 的标题/作者抽取正确（含 Attention 版权块场景）
- [ ] `planner_source=agent` 在四类测试提示词下 ≥ 80%（基线先测，D5 元数据落地后）
- [ ] `bisect` 任务可完成；空依赖不再触发 pip
- [ ] Louvain 论文 `arXiv:0803.0476v2` 能解析出 `0803.0476` 并进入 `repo_discovery`；无 curated 条目时可自动找到仓库
- [ ] 数据集形状不符时任务 `blocked` 且带可行动的 remediation
- [ ] `claim_evidence_graph.status=degraded` 时 outcome=`needs_review`，任务仍为 completed
- [ ] 四个测试按 4.3 复跑，结果写入新报告
- [ ] 前端可见「模板规划 / 降级」标识
- [ ] 被拒 blueprint、pip 原始日志、探针输出均已落盘，可从证据目录复跑
- [ ] 无新增 lint / vet 告警

---

## 附录 A：缺陷与代码位置对照（快速索引）

| 编号 | 首要文件 | 关键行 | 次要文件 |
|---|---|---|---|
| D1 | `backend/internal/documents/metadata.go` | 44-53、58-69、98-115 | `api/context_resolver.go` 64、`api/routes.go` 519 |
| D2 | `backend/internal/scheduler/repo_discovery.go` | 29、128-154、158-175、455-518 | `catalog/repositories.json`、`scheduler/scheduler.go` 705-735、`planner/planner.go` 1427 |
| D3 | `backend/internal/api/routes.go` | 38、355-363 | `intent/resolver.go` 50-90、155-199、`intent/keywords.go` 10-13 |
| D4 | `backend/internal/agent/coder.go` | 547-580、633-648、763-784、867、950-956、1778-1784 | — |
| D5 | `backend/internal/planner/agent_planner.go` | 441-510 | `planner/taskregistry.go` 46-54、206-210、259-263、343-346；`planner/planner.go` 1824-1834；`models/graph.go` 66-73 |
| D6 | `backend/internal/agent/claim_evidence.go` | 118-146、164-170 | `planner/semantic_validation.go` 122-164；`scheduler/scheduler.go` 274-289、1149-1156；`llmclient/llmclient.go` 31-38 |
| D7 | `backend/internal/scheduler/repo_prepare.go` | 61、307-367、1082、1120 | `catalog/recipes.json`、`planner/taskregistry.go` 122 |
| D8 | `backend/internal/agent/reproduction_spec.go` | 30-33、42-50 | `agent/paper_debug_harness.go` 341-356、358-404 |
| D9 | `backend/internal/prompts/prompts.go` | 17-24、26-34、64-79 | `agent/data.go` 94-122、`agent/answer.go` 22-33 |
| D10 | `backend/internal/planner/planner.go` | 1304-1327、1576-1657、1889-1893 | `prompts/prompts.go` 733-734 |

## 附录 B：相关文档与证据

> **注意**：`reports/**` 当前被 `.gitignore` 整体忽略（仅 `reports/README.md` 入库），以下链接只在本地工作区可打开。提交 issue 或交接时，请把关键证据（plan JSON、`summary.json`、`scorecard.json`、`pipeline-log-evidence.txt` 的相关片段）复制到 `docs/evidence/2026-10-25/` 或附在 issue 中。

- 测试报告与证据：[reports/eval-2026-10-25/REPORT.md](../reports/eval-2026-10-25/REPORT.md)（第 8 节为根因定位）
- 机器可读摘要：[reports/eval-2026-10-25/summary.json](../reports/eval-2026-10-25/summary.json) 的 `root_cause_analysis` 字段
- 评分卡：[reports/eval-2026-10-25/scorecard.json](../reports/eval-2026-10-25/scorecard.json)
- 规划回退日志：[reports/eval-2026-10-25/pipeline-log-evidence.txt](../reports/eval-2026-10-25/pipeline-log-evidence.txt)
- 历史整改方案（上一轮 7 类问题）：[reports/root-cause-remediation-plan.md](../reports/root-cause-remediation-plan.md)
- 历史整改实现（RC4 / 非 RC4）：[reports/implementation-docs-README.md](../reports/implementation-docs-README.md)

## 附录 C：术语表

| 术语 | 含义 |
|---|---|
| `artifact` | 任务产出的结构化数据，按 key 在计划内传递（如 `repo_url`、`plot_image`） |
| task registry | `taskregistry.go` 中的任务类型注册表，定义允许的 agent 与必需产物 |
| `postValidate` | 意图后处理：归一化实体，并按框架/绘图等信号升降级意图类型 |
| `outcome` | 计划语义结果：`completed` / `partial` / `smoke` / `mock` / `blocked` / `failed` / `needs_review` |
| `fallback` | 规划阶段的模板兜底（`buildFallbackNodes`）或依赖安装阶段的规则兜底 |
| `smoke` | 论文复现的运行模式：能跑通但不保证完整复现论文指标 |
| `degraded` | Claim evidence 的降级状态：模型不可用导致所有主张「不可验证」 |
| `planner_source` | 计划来源标记：`agent`（LLM 规划）或 `template`（模板兜底），D5 新增 |
