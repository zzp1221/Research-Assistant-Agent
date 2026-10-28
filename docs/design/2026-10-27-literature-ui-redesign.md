# 文献库与 PDF 阅读体验改版

- 状态：已实现并验证
- 日期：2026-10-27
- 范围：`research-assistant/frontend`、`research-assistant/backend/internal/api`、`research-assistant/backend/internal/literature`
- 关联：`docs/design/n04-n07-literature-and-document-intelligence.md`、`docs/improvement-2026-10-26/FIX-REPORT-ROUND4.md`

## 一句话

把文献库从「能搜、能存，但接不进当前会话」的侧栏，改成「检索 → 导入 PDF → 加入会话 → 阅读 / 划词翻译」的完整闭环；同时把工作台里妨碍理解的校样隐喻文案，改成用户能直接执行的动作。

## 用户任务

- 研究者需要找真实论文、确认是否有全文、把论文带进当前研究对话；
- 读论文时需要随时翻译单句、解释段落，或按页翻译整篇；
- 第一次使用时要能看懂：文献库、阅读器、执行图分别在哪里、怎么配合。

## 方向探索

三个首屏方向（`explore/a.html`、`b.html`、`c.html`）已在 `style-explorer.html` 并排对比：

| 方向 | 核心 | 首屏骨架 |
| --- | --- | --- |
| A · 文献台 · 卡片目录 | 可翻阅的论文卡片目录，保留纸张、衬线与红色批注的编辑部气质 | 左索引卡 / 中论文详情 / 右当前会话与使用指引 |
| B · 文献实验台 · 数据表 | 把文献库做成实验台数据面板，状态用仪表读数呈现 | 数据表 + 右侧检查器 + 底部状态条 |
| C · 阅览室 · 书架与阅读 | 阅读优先的阅览室，大标题、书封与克制的书目 | 非对称首屏：左标题 / 右书架 / 下方书目 |

选择 **A**：它保留现有工作台已经建立的纸张与编辑部识别度，同时用卡片目录把「检索、文献库、当前会话」三件事放在同一屏；实现代价最低，也不与现有执行图争夺主角。对比页与候选缩略图：

- `style-explorer.html`
- `shots/explorer/page.png`（对比页）
- `shots/explore/sheet.png`（三张首屏并排）
- `shots/explore/01.png`、`02.png`、`03.png`

## 关键改动

### 文献库

- 检索结果卡明确区分「导入并加入会话」和「仅保存元数据」；导入成功后原地显示「已加入当前会话」与「打开 PDF」。
- 「我的文献库」增加当前会话托盘：已加入的论文一目了然，可直接打开或移出。
- 仅元数据记录禁用「加入会话」，并在按钮 title 与使用手册里解释原因：先导入 PDF。
- 记录支持展开摘要、DOI/arXiv、被引数与标签；BibTeX、删除、打开 PDF 都在同一行。
- 空状态与检索空态加入三步使用指引，并链接到使用手册。

### PDF 阅读

- 阅读器顶部常驻「划词翻译」提示，首次打开显示一次可关闭的说明条：选中文字 → 翻译 / 解释；也可整篇翻译。
- 工具栏新增「划词翻译」状态标与「阅读指南」入口；「全文翻译」保持原入口。
- 翻译面板空态补充「只想翻译某一句？直接在 PDF 中选中文字即可」。

### 全局文案与导航

- 「校样」→「对话」，「当前稿次」→「当前会话」，「往期稿次」→「历史会话」，「投稿说明」→「输入指令」，「起始选题」→「示例任务」。
- 页头「The Research Proof / Manuscript Workbench / 执行校样」→「Research Assistant / 研究工作台 / 研究执行台」。
- 执行图「图版 / 已校 / 在排 / 校样批注 / 校次」→「节点 / 已完成 / 运行中 / 待处理 / 权限」。
- 新增全局「使用手册」抽屉：快速开始、文献库、PDF 阅读、执行图四个章节；从页头、文献库、PDF 工具栏分别打开对应章节。

### 交互修复

- **从文献库加入会话真正可用**：点击后读取 upload 元数据，把 PDF 放进输入框上方的附件栏，随下一条消息提交；8 个附件上限与去重逻辑和手动上传一致。
- **从文献库打开 PDF**：新增 `GET /api/uploads/:id`，直接进入阅读器，不再要求重新上传。
- **移出会话可持久化**：新增 `DELETE /api/literature/records/:id/attach`，刷新后不会再次显示为已加入。
- **导入 PDF 记录本地会话**：导入并加入会话时同时写入当前工作台会话 id，文献库重新加载后仍能识别。
- 后端新增 `LibraryStore.DetachSession` 与上传元数据接口；原有 `attach` 语义保持不变，避免破坏既有调用。

## 动效

- 使用手册抽屉使用 `proof-slide` 220ms 滑入；
- 文献库提示条、卡片和 PDF 阅读提示沿用 `proof-fade` 180ms；
- 没有新增循环动画；运行中状态沿用原有 `proof-pulse`。

## 验证

- 前端：`npm test` 25 个测试文件 / 112 个测试通过；`tsc -b` 通过；`vite build` 通过。
- 后端：`go test ./internal/api ./internal/literature` 通过；`gofmt -l` 无输出。
- 生产容器（`http://127.0.0.1:15173`）真实流程：导入 arXiv 1706.03762 → 文献库 → 加入会话 → 输入框出现 PDF 附件 → 打开 PDF → 划词提示可见 → 全文翻译面板打开 → 移出会话 → 刷新后仍为未加入。控制台无错误。
- Oil UI 截图脚本：三个候选首屏与 `sheet.png` 无控制台错误、横向溢出或加载失败；`style-explorer.html` 对比页 3 张候选卡片正常渲染；文献库检索/文献库两个状态的可读性 lint 无提示。
- 证据目录：`shots/final/final-literature-library.png`、`shots/final/final-literature-attached.png`、`shots/final/final-chat-attachment.png`、`shots/final/final-pdf-reader.png`、`shots/final/final-pdf-translation.png`、`shots/final/final-help-pdf.png`、`shots/final/final-search-results.png`、`shots/explore/sheet.png`、`shots/explorer/page.png`。

## 已知限制

- 当前会话模型不支持图像输入，无法人工目检截图；视觉核对以 DOM 尺寸/溢出检测、Oil UI 可读性 lint 和截图脚本代替，最终仍建议在浏览器中打开 `style-explorer.html` 与生产页面确认。
- 全量 `npm run lint` 仍有 5 个存量 React Compiler 规则报错（`useResearchAssistantChatFlow` 3 处、`GraphPanel` 1 处、`ModelSettingsPanel` 1 处），均落在原有 effect/memoization 代码行；新增的 `HelpDrawer`、`LiteraturePanel` 及其余本次改动 UI 文件 lint 通过。
- 标签编辑、批量导入/导出、引用格式批量导出不在本轮范围。