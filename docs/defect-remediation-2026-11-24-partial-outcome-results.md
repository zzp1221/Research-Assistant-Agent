# 部分成功解释信息展示：实施与验收报告

状态：**已实施，容器针对性端到端验收通过**。验收日期：2026-11-25（Asia/Shanghai）。

实施基线：`4d85ac24dbb05148c3b808da314d85e344689267`，修复通过分支 `codex/partial-outcome-explanations` 提交，具体提交与远程 CI 状态以关联 PR 为准。对应[修复方案](defect-remediation-2026-11-24-partial-outcome.md)的 PO-01～04。历史评分与历史测试仍以[交接文档](project-assessment-handoff-2026-11-18.md)为历史背景，本文只报告本轮实际验证。

主执行图现在能够展示服务端已有的结果原因、已通过目标与证据、未验证目标与原因、可选工作损失、缺少或不可信的验收产物及结论限制。旧计划没有可展示详情时明确提示；切换计划或清空详情后不会保留上一份说明。

已将工作区源码复制进 Linux QA 容器，安装锁文件依赖并构建，再将新静态产物复制进现有前端容器。真实浏览器通过该容器访问既有后端，实际执行确定性计划并验证 SSE、API 和页面展示。最终专项 E2E 两项全部通过，未发现本次修复引入的新业务问题；最终两个用例中的浏览器异常、console error 和 API 5xx 均为 0。

## 1. 实施内容

| 项目 | 已完成修改 | 直接验证 |
|---|---|---|
| PO-01 详情丢失 | `ResearchAssistantApp` 的工作区调用不再固定传 `null`，而是传入当前活动计划详情 | 真实 App 接线组合测试；固定 `null` 单行对照；容器真实 partial E2E |
| PO-02 原因传递 | `outcome_reason` 经 App、Workspace、view model 到 `GraphPanel.outcomeReason`；加入 memo 依赖 | 同计划原因更新；其他依赖引用固定的直接 hook 测试；E2E 比较真实 API 原因与 DOM |
| PO-03 完整解释 | 主图复用 `OutcomeDetailsList`，分别展示目标、证据、损失、产物和限制 | 已验证/未验证混合目标测试；真实 missing evidence 和 partial 页面 |
| PO-04 接线回归 | 新增真实 App → Workspace → view model → GraphPanel 组合测试，外壳只负责转交真实 props | 9 项 App 测试；旧缺陷与单行对照均在失败原因文本断言处失败 |

业务代码位于前端的以下文件：

- [ResearchAssistantApp.tsx](../research-assistant/frontend/src/app/ResearchAssistantApp.tsx)：转交当前计划的详情与原因。
- [useGraphExecutionViewModel.ts](../research-assistant/frontend/src/app/viewModels/useGraphExecutionViewModel.ts)：传递原因，维护详情、原因更新依赖。
- [GraphPanel.tsx](../research-assistant/frontend/src/features/plan-graph/GraphPanel.tsx)：复用统一详情组件，保留原终态入口，说明区支持换行、限高滚动和键盘聚焦。
- [PlanOutcomeBits.tsx](../research-assistant/frontend/src/features/shared/PlanOutcomeBits.tsx)：按 `verified === true` 分组，只将可信目标列入已通过组；明确未验证证据引用；分组展示信息，避免摘要和列表双重渲染相同损失；空详情提示；列表 key 支持重复文本。

`summarizePlanOutcome` 的公开摘要语义保留。后端状态机、API 字段、存储格式、执行/审批/取消/恢复按钮逻辑均未修改。证据标识保持原文，不生成虚构证据链接；限制说明仍使用后端已有通用句式，不新增科研影响推导能力。

## 2. 环境与本地证据

| 项目 | 实际值 |
|---|---|
| 工作区 | `D:\项目\Research Assistant agent` |
| QA 容器 | `partial-outcome-fix-qa-20261125`，`node:22-bookworm-slim`，源码 `/work/frontend` |
| QA Node / npm | `v22.23.4` / `10.9.9` |
| 宿主浏览器驱动 | Windows，Node `v24.14.0`，Chrome `158.0.8127.88`，Playwright 使用已安装 Chrome |
| 运行中前端 | `research-assistant-frontend-1`，`http://127.0.0.1:15173` |
| 运行中后端 | `research-assistant-backend-1`，宿主映射 `18080`；浏览器统一经前端 nginx 同源 `/api` 访问 |
| 原始证据根目录 | `reports/partial-outcome-fix-2026-11-25/` |

后文日志路径相对证据根目录。`reports/` 默认被 Git 忽略，原始日志、快照、备份和截图保存在本机，克隆仓库不会自动得到这些产物；永久测试和本文随源码保留。原始计划快照和预升级备份不应直接作为公开附件发布。

宿主现有 `node_modules` 初始仍为 PDF.js `3.11.174`，与锁文件的 `6.5.438` 不一致，导致首次本机 PDF 组件断言和构建失败。已运行 `npm ci --no-audit --no-fund` 恢复锁文件依赖，没有修改依赖版本或锁文件。宿主 Node 对若干依赖出现 engine 提示，因此全量门禁和生产构建以满足依赖要求的 Linux Node `22.23.4` QA 容器结果为准。

## 3. 测试与门禁实际结果

| 检查 | 结果 | 证据 |
|---|---|---|
| 修复前原始接线回归 | 关键 App 用例真实失败：找不到 `A指标数据集缺失`，当时 DOM 只有结果标题和空解释 | `app-wiring-baseline-failure.log`，明确标注为首次输出摘录 |
| 固定空值单行对照 | 仅把工作区详情参数恢复为 `null`：1 项失败；还原修复：同一项通过；源码确认已恢复 | `null-control-and-restored.log`；辅助脚本 `verify-null-regression.cjs` |
| 五文件专项 | App 9、view model 36、GraphPanel 15、详情组件 14、摘要 14，共 88 项通过 | 独立专项输出及全量日志中的对应文件 |
| Linux 前端全量 `npm run test` | 23 项 Node 单元测试、61 文件中的 559 项 Vitest 测试全部通过，无跳过 | `container-frontend-tests.log` |
| Linux `npm run lint` | 通过；最终 E2E 测试文件更新后再次 lint 通过 | `container-frontend-lint.log`、`container-frontend-lint-final.log` |
| Linux `npm run build` | TypeScript 与 Vite 生产构建通过 | `container-frontend-build.log` |
| Linux `npm run check:bundle` | 通过：入口 JS gzip 237.2 KB，CSS gzip 17.2 KB，低于 320 / 35 KB 门槛 | `container-bundle-budget.log` |
| 最终真实容器 E2E | 2 项全部通过，5.2 秒 | `browser-e2e-r4.log`、`browser-r4/` |
| 新 E2E 文件独立检查 | ESLint、TypeScript noEmit 检查通过；未启用 opt-in 时两项确实跳过 | agent 定向检查记录、`opt-in-disabled-check/` |
| 交付产物与格式 | 源码清单、运行产物清单核验；`git diff --check` 通过 | `deployment.summary.json`、`runtime-verification.summary.json`、SHA-256 清单 |

全量测试中既有错误处理测试会主动输出模拟网络错误，另有 PDF.js 的 Node legacy 提示和 RuntimePanel 重复 key 警告；它们不影响 559 项通过，但不能描述为全量日志无警告。Vite 保留大 chunk 提示，包体预算实际通过。新增结果详情的重复文本 key 已有独立回归，最终真实页面没有 console error。

## 4. 新代码复制、构建与运行交付

本轮执行了以下复制与构建流程：

```powershell
# 仓库根目录；归档排除 node_modules、dist 和测试运行产物。
docker cp reports/partial-outcome-fix-2026-11-25/frontend-source.tar partial-outcome-fix-qa-20261125:/tmp/frontend-source.tar
docker exec partial-outcome-fix-qa-20261125 tar -xf /tmp/frontend-source.tar -C /work/frontend
docker exec partial-outcome-fix-qa-20261125 npm ci --no-audit --no-fund
docker exec partial-outcome-fix-qa-20261125 npm run test
docker exec partial-outcome-fix-qa-20261125 npm run lint
docker exec partial-outcome-fix-qa-20261125 npm run build
docker exec partial-outcome-fix-qa-20261125 npm run check:bundle
docker cp partial-outcome-fix-qa-20261125:/work/frontend/dist/. reports/partial-outcome-fix-2026-11-25/frontend-dist/
docker cp reports/partial-outcome-fix-2026-11-25/frontend-dist/. research-assistant-frontend-1:/usr/share/nginx/html/
```

随后将最终源码归档和清单同步到运行前端的 `/opt/partial-outcome-fix-20261125/`，供本机追溯；nginx 实际提供的是 `/usr/share/nginx/html` 中的构建产物。

| 核验项 | 结果 |
|---|---|
| 源码清单 | 217 个源码、测试、脚本、vendor 与配置文件；QA 容器与工作区逐文件一致 |
| 最终源码归档 SHA-256 | `4bd5c85a381587194d3b7ac67b8f40bf2ee9ff7064cb9cace2d48cd64c5748ca` |
| 新构建产物 | 282 个文件，运行容器逐文件 SHA-256 一致 |
| `index.html` SHA-256 | `e83edc6ffc91ad154392dfef701d06c9ffa72207341dcc3a95c5488b19dfcb18` |
| 新入口 JS | `assets/index-BDLJVa9w.js` |
| HTTP 服务确认 | 首页 200，返回新入口；同源 `/api/health` 200 |

本轮没有重建镜像。新产物保留在现有容器可写层；删除并重建容器会恢复原镜像，需要重新从当前源码构建镜像或再次交付产物。此边界不影响本次已经在新产物上完成的端到端验收。

### 启动恢复与备份

Docker Desktop 初始未运行，启动后发现现有 backend 因 2026-11-19 遗留的 PID 1 memory/wiki 锁退出。已先确认没有其他运行容器共享该计划数据卷，再将完整 `/app/data` 复制到 `pre-upgrade-data/`，将旧前端目录复制到 `frontend-previous/`。

仅将失效的 `.memory.lock` 和 `.wiki.lock` 改名保存为 `.stale-20261125-partial-outcome` 后启动 backend，未删除原业务数据、未修改后端锁实现。后端恢复后健康检查通过。该启动环境问题与前端结果解释缺陷分别记录，不能将其写成前端修复解决了后端锁问题。

## 5. 真实端到端验收

永久用例：[partial-outcome-live.spec.ts](../research-assistant/frontend/test/e2e/partial-outcome-live.spec.ts)。默认跳过，仅在明确设置 `PARTIAL_OUTCOME_LIVE=1` 时访问真实服务。

实际命令在 `research-assistant/frontend/` 运行：

```powershell
$env:PARTIAL_OUTCOME_LIVE = '1'
$env:E2E_BASE_URL = 'http://127.0.0.1:15173'
$env:PLAYWRIGHT_CHROME_PATH = 'C:\Program Files\Google\Chrome\Application\chrome.exe'
npx playwright test test/e2e/partial-outcome-live.spec.ts --workers=1 --output=../../reports/partial-outcome-fix-2026-11-25/browser-r4
```

浏览器与请求共享真实签名 cookie，经 nginx 注入服务认证。没有 mock API 响应、伪造终态、改写存储或使用付费模型；EventSource 的子类仅记录真实消息，运输和事件处理继续使用浏览器原生实现。

### 5.1 部分成功：真实执行、真实 SSE 与自动展示

最终计划：`plan-490b407e-353c-40ab-8c05-9e05cc44769a`。

1. 经真实 `/api/plans/from-graph` 创建计划，从真实计划库打开，点击页面“运行计划”。
2. 计划先进入真实提问等待；浏览器已订阅流后，通过真实回答接口释放等待，确保覆盖运行中的观察到终态。
3. 核心 `native:echo` 生成可信 `core_report`，8 项可选 `native:fail` 失败，汇总节点完成，后端真实生成 `completed + partial`。
4. 捕获 `plan_completed`（Seq 50），事件包含 8 项损失；事件详情与真实 API 快照相同。
5. 没有刷新页面或注入状态，应用自己从流处理触发读取新快照，页面显示目标、证据、失败原因、全部损失和限制。
6. 所有可选节点仍为 `failed`，没有被前端改成成功；整个用例只发送一次 execute。

证据：`browser-r4/partial-outcome-live-deplo-cdf33-adable-at-desktop-and-390px/partial-live-evidence.json`，保存实际计划、终态事件、浏览器 GET 记录和 execute 记录。

### 5.2 清空本地缓存后的真实 API 恢复

刷新前只将当前本地会话的 `planGraph` 设为 `null`，保留活动计划 ID 和身份；预先等待精确匹配该计划的 GET 响应，再 reload。实际 GET 200 后核对 ID、详情与原因，并断言 DOM 恢复。因此通过结果不能由本地旧计划缓存单独解释。

partial 和空结构详情两个计划均执行该验证，记录在各自 evidence 的 `apiRestoration` 中。另验证新建空白会话时旧说明消失，再从计划库重开时恢复，execute 仍只有一次。

### 5.3 桌面与 390px 窄屏

实际视口：1440×900、390×844。8 项损失中包含长中文原因。说明区长列表可以滚动，首条损失与最后一项限制均在滚动后通过 `toBeInViewport`。

窄屏测量：左边界 24、右边界 366、宽度 342；页面宽度与视口均为 390，无横向溢出；内容高度 1148、可见高度 336。已人工检视最终五张截图，文字可换行、主要操作区未被遮挡。

最终截图位于 `browser-r4/` 对应子目录：

- `partial-desktop.png`
- `partial-mobile-losses.png`
- `partial-mobile-limitations.png`
- `review-missing-evidence.png`
- `review-empty-details-restored.png`

### 5.4 待复核与空详情

| 真实计划 | 实际结果与页面行为 |
|---|---|
| `plan-1fb94c2a-4cda-45d9-878c-f1df83e675f3` | 任务实际完成，但没有 `missing_report` 验收证据；后端生成 `needs_review`；页面显示未验证目标、服务端原因和缺失产物，没有将目标列入已通过组 |
| `plan-8f6aeb84-c8bb-484f-943f-876cb16f0aae` | General 工具任务没有最终回答产物；后端生成 `needs_review`、详情仅 `{mode:"strict"}`；页面展示真实原因与“该计划未提供结构化验收详情”，切换后不残留前一计划信息 |

证据：`browser-r4/partial-outcome-live-deplo-159b4-ut-reusing-previous-details/review-live-evidence.json`。公开导入接口会清除调用方伪造的终态，因此没有通过篡改存储来制造历史 `partial + null`；历史缺省、null 和空数组兼容由永久组件/应用测试验证，真实集成验证使用后端实际生成的 mode-only 空展示详情。

最终两个用例的 pageerror、console error、API 5xx 均为 0。本轮创建的三个最终验收计划均在 finally 以 DELETE 200 清理，结果保存在两份 `cleanup.json`；没有删除既有用户计划。

## 6. 18 项方案验收映射

缩写：A = `ResearchAssistantAppOutcome.test.tsx`，V = `useGraphExecutionViewModel.test.tsx`，G = `GraphPanelOutcome.test.tsx`，O = `OutcomeDetailsList.test.tsx`，S = `planOutcome.test.ts`，E = 最终真实容器 E2E。

| 用例 | 证据与结果 |
|---|---|
| PO-T01 完整 partial | A 的真实接线、目标证据断言与 E 第 1 项通过 |
| PO-T02 只有原因 | A 跨计划进入无详情终态、O 降级参数化与原因断言通过 |
| PO-T03 各种空详情 | O 的 undefined/null/空对象/空数组/mode-only/claims-only 均通过；E mode-only 通过 |
| PO-T04 缺必需产物 | G 的 needs_review、O 未验证目标、E missing evidence 通过 |
| PO-T05 混合目标 | O 分别用具名 region 断言已通过/未验证分组，无错误归类 |
| PO-T06 真实损失状态与名称回退 | O/S 覆盖 failed/blocked/canceled/skipped/未知状态与空名称回退 |
| PO-T07 A→无详情终态 B | A 断言原目标、证据、损失和限制清空；E missing→empty 无残留 |
| PO-T08 A→运行中 B | A 断言说明区消失；G 非终态和重跑边界通过 |
| PO-T09 同计划新详情 | A/V 在 ID、状态、结果保持时替换详情，旧文本消失、新文本出现 |
| PO-T10 同计划清空详情 | A 对 null/undefined 均通过；V/O 清空回归通过 |
| PO-T11 只更新原因 | V 固定其他全部 memo 依赖引用的直接 renderHook 用例通过；A 接线补充通过 |
| PO-T12 运行→partial | A 新快照测试与 E 真实 SSE 自动展示均通过 |
| PO-T13 其他结果不混淆 | G 保留 completed/failed/canceled/smoke/mock 标签和原有入口，S 锁定语义 |
| PO-T14 blocked 终态 | G 显示缺少条件、真实原因与缺省提示通过 |
| PO-T15 分组完整、不重复 | O 单次损失渲染、产物/限制与原原因；S 公开摘要语义通过 |
| PO-T16 长文本与窄屏 | E 1440/390 视口测量、滚动可见性断言和五张截图视觉复核通过 |
| PO-T17 真实 API 恢复 | E 清空本地 planGraph 后等待真实 GET 200、比对详情并验证 DOM，通过 |
| PO-T18 真实终态刷新 | E 第 1 项真实执行、原生 SSE、自动 GET、详情 DOM，execute 仅一次，通过 |

## 7. 过程中发现并处理的事项

| 事项 | 处理与保留证据 |
|---|---|
| 宿主依赖陈旧 | 首轮本机 558 组件通过、1 项 PDF 失败，构建缺少现代 worker；按锁文件重装，改用 Node 22.23.4 容器完成全量门禁。原始日志 `frontend-tests-stale-dependencies.log`、`frontend-build-stale-dependencies.log` 保留 |
| 新滚动区 lint | 首轮 no-noninteractive-tabindex 拒绝；保留有名称、可键盘滚动的 region，并为该可访问性用途写明 ESLint 例外原因；最终 lint 通过。初始日志 `frontend-lint-initial-scroll-focus.log` 保留 |
| E2E 第一轮夹具不合法 | 工具节点漏注册表要求的 `tool_result`，导入 400；补齐后再执行。`browser-e2e-initial.log` 与 `browser-initial/` 保留 |
| E2E 第二轮夹具产物冲突 | 等待工具和核心工具均成功产生 `tool_result`，后端正确拒绝同名产物，core failed；改用真实提问门等待订阅，只保留一个成功工具生产者，没有放松后端或 UI 断言。`browser-e2e-r2.log` 与 trace 保留 |
| E2E 第三轮 | 两项通过；随后审计发现恢复断言可能被本地缓存满足，补强为清空缓存并等待真实 GET。r3 全部记录保留 |
| E2E 最终第四轮 | 加强恢复证据后两项再次通过，作为最终关闭依据；未再修改业务代码 |

预期的 `native:fail` 是本轮验证可选损失的输入，不能把这些任务失败记为产品新缺陷。前两轮 fixture 失败也不能被写成“首轮端到端全通过”；它们已单独保留。最终没有未解决的本次业务回归。

## 8. 回退与未测范围

前端回退可使用 `frontend-previous/` 中的完整目录恢复运行容器；当前源码归档、新构建产物和两类 SHA-256 清单均保留。回退静态前端不要求回退后端数据，也不应删除计划或重置研究结果。

本轮未重跑全后端 Go 测试、race、桌面安装包、所有历史浏览器 E2E、安全审计、真实付费模型科研任务、长期运行或性能测试。通知、导出、记忆等其他结果入口没有被本轮重新验收。通过结论限于文档定义的主执行图解释修复及本次针对性端到端范围；不代表项目所有历史问题均已关闭。
