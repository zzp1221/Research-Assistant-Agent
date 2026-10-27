# 运行期模型配置与通用视觉校验设计

- 状态：设计稿（待评审）
- 日期：2026-10-26
- 范围：LLM 模型配置、视觉能力探测、verify_result 图像校验、前端设置与校验展示
- 关联：docs/improvement-2026-10-26/02-remaining-issues.md（R-05/R-07/R-14）、backend/internal/agent/data.go、backend/internal/agent/input_summary.go、backend/internal/prompts/prompts.go
- 已确认决策：
  1. 模型配置采用 per-user + 全局默认（owner_id 为空表示全局）。
  2. 没有可用视觉模型时，校验结论为 inconclusive，不得伪装成 pass。
  3. 视觉校验返回 fail 时，计划仍为 completed，但记录 verification: fail（advisory 语义）。

---

## 1. 摘要

当前 verify_result 只做文本推理：图像 base64 被当作文本拼进 prompt，既不能形成视觉判断，又会触发大 prompt 超时；本轮修复改为省略图像字节后，verify_result 只能确认“存在一个图像产物”，无法判断图像内容是否正确。

目标不是给 sin/cos 或任何具体图表写规则，而是：

1. 允许在项目启动后，由前端按用户配置多个 OpenAI-compatible 模型 profile（主模型、视觉模型、兜底模型）；
2. 运行期热更新，不重启服务；
3. 根据能力探测自动路由：主模型有视觉则用主模型判图；否则用配置的其它视觉模型；都没有则只做通用结构检查并返回 inconclusive；
4. 用通用结构检查 + 多模态视觉判断 + 代码/日志证据，对任意图像（折线图、柱状图、散点图、热力图、显微图、流程图等）做校验，禁止按图表类型硬编码；
5. 校验结果与执行状态解耦：执行成功仍 completed，校验结果单独记录为 pass/fail/inconclusive/skipped，失败不阻断计划，但在 UI、事件、指标和 artifact 上明确标记。

### 非目标

- 不按图表类型或学科写死校验规则；
- 本设计默认不因 verification fail 阻断计划，blocking 作为后续可选策略；
- 不做跨用户共享密钥，也不在本期实现完整 RBAC；
- 不做模型自动下载/本地部署；
- 不保证视觉 verdict 100% 正确，只提供可追溯的证据、置信度与失败原因，不能替代人工评审。

---

## 2. 问题出在哪

### 2.1 现象

- 绘图计划可以 completed，但 verify_result 报告写“无法确认图片内容”；
- 失败时（修复前）会卡住约 9 分钟：单次 LLM 180s 超时，重试 3 次；
- verify_result 无法区分“正确的论文图表”和“任意一张非空 PNG”。

### 2.2 根因链

1. 模型在启动时构造
   - answer/chat/coder/librarian/data/planner 等 Agent 在构造时创建 ChatModel，Eino 链也在构造时编译；
   - 没有运行期模型配置，也没有“主模型/视觉模型”概念。

2. verify_result 走纯文本链
   - backend/internal/agent/data.go 中 verify_result 把 task.Inputs 格式化后调用 Eino 文本链；
   - backend/internal/agent/input_summary.go 为控制 prompt 体积，把 image/base64 替换为占位符；
   - 整条调用没有构造 image_url 多模态 content part，视觉模型即使可用也看不到图。

3. 把 base64 当文本没有意义
   - 修复前把 224 KB PNG 的 base64（约 30 万字符）拼进文本 prompt；
   - 文本模型不能从 base64 解码像素；同时 prompt 过大，实测 180s 超时、重试 3 次后失败；
   - 这不是“视觉校验超时”，而是“用错误协议传了错误的数据”。

4. 完成语义把执行成功等同于校验通过
   - DataAgent 在 LLM 调用成功后直接设置 task.Status = Completed；
   - 报告正文里的“无法确认”不参与状态机；
   - 于是计划 completed，但校验结论不可确认。

5. 曾经考虑过的确定性方案方向错误
   - “检测两条彩色曲线”只能验证 sin/cos 这类示例；
   - 科研图表类型无限，任何按图表类型写死的规则都会误判或漏判；
   - 正确方向是通用结构检查 + 语义视觉模型 + 代码/日志证据。

### 2.3 当前模型能力的事实

- 使用当前配置的 OpenAI-compatible provider 发送 1×1 红色 PNG 的 image_url data URI，返回 200 且回答 Red；
- 说明当前主模型具备视觉输入能力；
- 但能力不能靠模型名假设：不同 provider、代理、模型版本、配额策略都可能不同，必须通过 probe 探测并按 profile 记录；
- 视觉能力是 profile 级属性，不是全局属性。

---

## 3. 为什么这么改

### 3.1 为什么改为运行期前端配置

- 用户需要在启动后切换 provider、模型、密钥，而不想改 env 重启；
- 多用户场景下，A 的 key 不应给 B 用；
- 运行期配置可以按计划快照，保证复现；
- 启动时 env 仍作为全局默认的 bootstrap，避免空配置不可用。

### 3.2 为什么 per-user + 全局默认

- per-user：用户自己的 profile、key、额度、路由只作用于自己的计划；
- 全局默认：新用户无需配置即可使用；管理员维护共享模型；
- 解析顺序：用户显式配置 > 用户默认 > 全局默认；
- 安全边界：全局 profile 只读，修改需要管理员；用户 profile 完全隔离。

### 3.3 为什么按能力路由，而不是固定一个视觉模型

- 主模型可能已经有视觉能力，没必要额外付费；
- 主模型没有视觉时，才把图像发给其它视觉模型；
- 视觉模型只负责判图，最终报告仍由主文本模型综合，避免“视觉模型写报告”导致风格/成本失控；
- 所有视觉模型都不可用时，只能做结构/文本检查，必须诚实返回 inconclusive。

### 3.4 为什么 fail 仍 completed

- completed 表示“计划执行完成”，不是“结果正确”；
- 视觉判断存在误判/置信度问题，不应直接杀死一个已经产出结果的研究计划；
- 用户需要看到结果 + 明确失败原因，再决定是否重跑；
- 因此采用 advisory：completed + verification: fail + issues + 事件/指标/UI 标记；
- 后续可通过策略开关升级为 blocking（本设计不默认启用）。

### 3.5 为什么没有视觉模型时是 inconclusive

- 只检查文件是图像/非空，不能证明内容正确；
- skipped 会让人误以为“不需要校验”；
- inconclusive 明确表达“执行完成，但语义未验证”，并说明原因；
- UI 必须展示“未配置可用视觉模型”，引导去设置页。

### 3.6 为什么不用 sin/cos 或图表类型规则

- 科研图表类型开放：折线、柱状、散点、热力、误差带、显微、谱图、网络图、流程图；
- 规则无法覆盖，且会给出虚假的安全感；
- 通用结构指标（可解码、非空白、尺寸/熵/边缘）对所有图像成立；
- 语义判断交给视觉模型 + 用户 intent + 代码/日志，而不是硬编码形状。

---

## 4. 怎么改

### 4.1 总体架构

    前端设置页
      -> /api/model-profiles, /api/model-routing
      -> 加密存储 ModelProfile（per-user + global）
      -> ModelResolver（按 owner/role 解析、缓存、热更新）
      -> Agents / Planner 使用 Resolver 而不是启动时 ChatModel
      -> verify_result:
           结构门禁（Go image decode + 统计）
           -> 选视觉 profile（主 -> 其它 vision -> 无）
           -> VisualVerifier（OpenAI-compatible image_url）
           -> 合并 verdict
           -> 文本报告（主模型，注入 verdict）
      -> Plan/Node/Artifact.verification + SSE 事件 + metrics
      -> 前端展示校验徽标、issues、inconclusive 原因

### 4.2 ModelProfile 数据模型

字段（文件存储 JSON，后续可迁移 SQLite）：

    id                  string   稳定 UUID
    owner_id            string   空字符串 = 全局；否则为 owner
    name                string   用户可见名称
    provider            string   openai_compatible（首期）
    base_url            string   兼容 /v1 的地址
    model               string   模型标识
    api_key_ciphertext  string   AES-GCM 密文
    api_key_last4       string   仅用于 UI 掩码
    roles               []string main / vision / fallback
    supports_vision     string   yes / no / unknown（probe 或人工覆盖）
    vision_probed_at    time     最近 probe 时间
    timeout_seconds     int      默认 120
    max_output_tokens   int
    prompt_price_per_1k float
    completion_price_per_1k float
    revision            int      每次修改递增
    created_at/updated_at time

约束：

- 同一 owner 下 name 唯一；
- 每个 owner 至少一个 main profile 由 routing 指定；
- 全局 profile 的 owner_id 为空，只有管理员可写；
- 用户 profile 数量上限（建议 10）、probe/test 频率限制；
- 密钥不回显、不写日志、不进入计划快照。

ModelRouting：

    owner_id            string
    main_profile_id     string
    vision_profile_ids  []string  按优先级排序；只用于判图
    fallback_profile_ids []string 文本调用失败时兜底（可选）

### 4.3 模型解析与热更新

- 新增 ModelResolver：
  - ResolveText(ownerID, role) 返回可用的 ChatModel 客户端；
  - ResolveVision(ownerID) 按 main（若 supports_vision=yes）-> vision_profile_ids 顺序返回候选；
  - 客户端按 profile revision 缓存；profile 更新后 revision 变化，旧客户端失效；
  - 计划创建时记录 used_profile_ids + revisions 到 EnvironmentManifest；
  - 运行中的计划固定使用创建时快照对应的 profile revision，避免中途换模型导致不可复现。

- 现有代码改造：
  - 现在 answer/chat/coder/librarian/data/planner 在构造时 NewChatModel 并编译 Eino 链；
  - 改为注入 Resolver/ModelFactory；Eino 链按 profile revision 缓存重建；
  - 简单路径（Generate）直接取 Resolver 的模型；
  - 兼容：未配置任何 profile 时，用 env OPENAI_* 自动生成全局默认 profile（bootstrap），行为与当前一致。

- 热更新：
  - 保存 profile/routing 后，Resolver 清理对应 owner 的缓存；
  - 下一次任务使用新配置；已在运行的计划继续用快照；
  - 不需要重启服务。

### 4.4 视觉能力 probe

- 目的：不靠模型名猜能力；
- 文本 probe：发送 ping，要求返回固定短语，验证 key/base_url/model 可用；
- 视觉 probe：发送两张小图（纯红、纯蓝），分别问“图片主色是什么？只回答 red 或 blue”；
  - 两题都正确 -> supports_vision=yes；
  - 任一错误/格式不支持/超时 -> supports_vision=no（记录原因）；
  - 网络错误 -> unknown，允许运行时再试；
- 结果写入 profile，展示在 UI；
- probe 不发送任何用户数据；
- 支持人工覆盖（UI 下拉 yes/no/unknown），覆盖后记录操作者与时间；
- 运行时如果 supports_vision=unknown，仍尝试一次；成功则更新为 yes 并缓存；
- 主模型 probe 为 no 时，verify_result 不调用它判图。

### 4.5 校验流水线（verify_result）

输入：

- 用户原始 intent；
- 生成代码（截断到上限，如 12k 字符）；
- 执行日志/自检输出（截断）；
- 图像 artifact（可能多张）；
- 可选 figure spec（如果代码生成阶段能提供：图表类型、标题、轴标签、系列名、单位；没有也不影响）。

步骤：

1. 结构门禁（所有图像，无论有无视觉模型）
   - 必须能解码 PNG/JPEG（WebP 可选，按依赖）；
   - 尺寸 > 0，建议下限 32×32（可配置）；
   - 非空白：颜色方差/熵/边缘密度/非透明像素比例不低于阈值；纯色、全透明、1×1 直接 fail；
   - 文件大小上限；
   - OCR（tesseract）可选：提取文本作为证据（轴/标题/图例），不强制要求（显微图/照片可能没有文字）。

2. 选择视觉模型
   - main profile supports_vision=yes -> 用 main；
   - 否则按 vision_profile_ids 顺序尝试；
   - 某个 profile 调用失败（超时/4xx/5xx/不支持 image_url）-> 记录 warning，试下一个；
   - 全部失败/无候选 -> 不调用视觉，进入 inconclusive 分支。

3. 视觉判断
   - 对每张图单独调用（多图最多 N 张，默认 4；超出只取关键图并记录）；
   - 图像预处理：最长边缩放到 1280px（可配置），JPEG q=85 或 PNG，控制 token；
   - 输入内容：图像 + intent + 代码 + 日志 + figure spec + 通用 rubric；
   - 要求严格 JSON：

        {
          "verdict": "pass|fail|inconclusive",
          "confidence": 0.0,
          "summary": "一句话结论",
          "issues": ["具体问题"],
          "evidence": ["图里看到的证据"]
        }

   - 通用 rubric（不是图表类型规则）：
     - 图像内容是否回应用户请求；
     - 图表类型是否与请求/代码一致；
     - 标题、轴标签、图例、单位是否可读且不缺；
     - 是否存在空白、截断、遮挡、异常色块、明显错误；
     - 数据趋势/系列数量是否与代码/日志声明一致；
     - 无法确认时返回 inconclusive，不编造。
   - JSON 解析失败：一次修复重试；仍失败 -> inconclusive；
   - 调用超时：按 profile timeout；失败切换到下一个候选。

4. 汇总
   - 无图像 artifact：verification.status = skipped；
   - 结构 fail：verification.status = fail，level=structural，不再调用视觉；
   - 结构 pass + 视觉不可用/全部失败：inconclusive，level=structural/text，注明原因；
   - 结构 pass + 任一图视觉 fail：fail，level=visual，汇总 issues；
   - 结构 pass + 所有图视觉 pass：pass，level=visual；
   - 多图：任一 fail 则整体 fail；无 fail 但有 inconclusive 则整体 inconclusive；全部 pass 才 pass。

5. 报告与展示
   - 文本主模型生成 verify 报告时注入结构化 verdict（pass/fail/inconclusive + issues），要求不得把 inconclusive 说成 pass；
   - Plan 状态不变（completed/failed 由执行决定）；
   - 写入 plan.verification、node.verification、artifact.metadata.verification；
   - 发事件 verification_completed（或 verification_failed），更新 metrics；
   - 前端展示徽标、issues、验证级别、使用的模型；inconclusive 显示“未配置可用视觉模型/视觉调用失败，仅完成结构与文本检查”。

### 4.6 Verification 数据结构

    verification:
      status: pass | fail | inconclusive | skipped
      level: structural | text | visual | none
      verified: bool                 // 仅 pass=true
      verified_by:
        profile_id: string
        model: string
        mode: main | vision_fallback
      rubric_version: string
      image_sha256: []string
      issues: []string
      evidence: []string
      confidence: float
      reason: string                 // inconclusive/skipped 原因
      checked_at: time
      duration_ms: int
      token_usage: { prompt, completion, estimated }

存放位置：

- PlanGraph.Verification（最终汇总）
- TaskNode.Verification（verify_result 节点）
- Artifact.Metadata["verification"]（每个图像）
- PlanEvent payload（SSE 前端即时展示）
- 可选：独立 verification 历史，便于重复校验对比

### 4.7 API 设计

模型配置：

    GET    /api/model-profiles
           返回当前用户可见的 user profiles + global profiles（掩码）
    POST   /api/model-profiles
           创建 user profile；body: name, provider, base_url, model, api_key, timeout...
    PUT    /api/model-profiles/:id
           更新；api_key 留空表示不变
    DELETE /api/model-profiles/:id
    POST   /api/model-profiles/:id/test
           文本连通性测试
    POST   /api/model-profiles/:id/probe-vision
           红/蓝双图视觉能力探测
    GET    /api/model-routing
    PUT    /api/model-routing
           { main_profile_id, vision_profile_ids, fallback_profile_ids }

全局默认：

- owner_id 为空的 profile 只能由管理员创建/修改；
- 管理员凭据建议使用 ADMIN_TOKEN（env）或管理员登录态；
- 普通用户只读；
- 启动时若没有任何 global profile，则用 env OPENAI_* 自动 bootstrap 一个。

校验：

    GET /api/plans/:id
         plan.verification 已包含，无需新端点
    GET /api/plans/:id/verifications
         可选：返回节点/图像级明细
    POST /api/plans/:id/tasks/:taskId/reverify
         可选：重跑视觉校验（不重跑任务）

错误码建议：

    model_profile_invalid
    model_profile_encryption_unavailable
    model_profile_not_found
    model_profile_forbidden (global)
    model_probe_failed
    vision_unavailable
    verification_inconclusive
    ssrf_blocked
    rate_limited

### 4.8 前端设计

设置页（工作台设置 -> 模型）：

- 列表分组：我的模型 / 全局模型（只读）；
- 每个 profile 卡片：名称、provider、模型、掩码 key、能力徽标（视觉：是/否/未知）、最近 probe 时间；
- 添加/编辑弹窗：api_key 只写不回显；保存前可 Test / Probe；
- 路由区：主模型下拉、视觉模型多选排序、兜底模型；
- 成本提示：视觉判断会额外消耗 token；显示估算单价；
- 隐私提示：图像、代码、日志会发送到所选 provider；
- 删除确认：若被 routing 引用，提示先改路由。

校验展示：

- 计划头：执行状态 + 校验徽标（通过/未通过/不可确认/未校验）；
- 节点卡：verify_result 的 issues、confidence、verified_by；
- 图像 artifact 预览：角标“已校验/校验未通过/不可确认”；
- 失败：展开 issues，不改变计划 completed 状态；
- inconclusive：说明“没有可用视觉模型/视觉调用失败”，提供去设置的入口；
- 事件流：SSE 收到 verification_completed 后即时刷新；
- 不把“文件是图像”显示成“已校验”。
### 4.9 安全设计

- 密钥加密：
  - MODEL_PROFILE_ENCRYPTION_KEY（建议 32 字节 base64）；
  - AES-256-GCM，AAD 绑定 profile id + owner + revision；
  - 未配置密钥时拒绝保存 profile（fail closed），而不是明文落盘；
  - API 只返回 last4 + 掩码；日志/错误/审计不含 key。
- 作用域：
  - owner 隔离，global 只读；
  - 计划记录 profile id/revision，不记录 key；
  - 越权访问返回 403。
- SSRF：
  - base_url 默认要求 https；
  - 解析 IP，拒绝 private/loopback/link-local/元数据地址；
  - 不跟随重定向到内网；
  - 可选 ALLOW_HTTP_LOCALHOST 仅开发。
- 滥用与成本：
  - profile 数量上限、test/probe 频率限制；
  - 每计划视觉调用上限（默认 4 图）+ 超时；
  - 可选每日 token/费用预算；
  - 视觉调用失败不无限重试（每候选 1 次 + JSON 修复 1 次）。
- Prompt injection：
  - 用户意图/代码/日志作为数据放入明确分隔区；
  - 视觉模型输出只作为数据解析，不执行其中的指令；
  - verdict JSON 严格校验，未知字段忽略。
- 隐私：
  - 首次启用视觉时提示图像将发送到第三方 provider；
  - 全局管理员可关闭视觉能力（policy）。

### 4.10 兼容与迁移

- 旧计划没有 verification 字段：读取时视为 skipped/legacy，正常展示；
- 现有 env OPENAI_*：
  - 首次启动如果没有 global profile，自动创建 bootstrap profile（不覆盖用户配置）；
  - 之后 env 只作为 fallback，不覆盖前端配置；
- 旧版本前端不传 verification：后端字段可选，不破坏 API；
- 文件存储格式：model_profiles.json + routing.json，原子写入，带版本号；后续可迁移 SQLite；
- 回滚：设置 FEATURE_VISION_VERIFICATION=false 即可退回纯文本 verify，保留字段但不调用视觉。

### 4.11 性能与成本

- 图像缩放：最长边 1280px；JPEG q=85 或 PNG；单图目标 < 1 MB；
- 多图：最多 4 张，超出取 execution 产物优先级；
- 每计划视觉预算（可配置 token/费用）；
- 缓存：key = image_sha256 + rubric_version + profile revision + intent_hash；同图重复校验直接返回；
- 失败缓存：短 TTL（如 5 分钟），避免瞬时故障反复重试；
- 视觉调用与文本报告分离，视觉失败不阻塞报告生成。

### 4.12 失败模式与降级

    情况                        结果
    图像 decode 失败            fail (structural)
    纯色/全透明/1x1             fail (structural)
    无图像 artifact             skipped
    无 main profile             inconclusive（提示配置）
    主模型无视觉 + 无 vision    inconclusive
    主模型视觉超时/报错        尝试下一个 vision，全部失败 -> inconclusive
    视觉返回非 JSON            一次修复重试，仍失败 -> inconclusive
    视觉返回 fail               fail (visual)，计划仍 completed
    视觉返回 inconclusive       inconclusive，记录原因
    多图混合                   任一 fail -> fail；否则任一 inconclusive -> inconclusive；全 pass -> pass
### 4.13 代码改造清单

后端：

- internal/modelprofile：存储、加密、CRUD、routing、probe；
- internal/modelresolver：按 owner/role 解析、缓存、热更新；
- internal/agent/visual_verifier.go：结构门禁 + 视觉调用 + verdict 解析 + 缓存；
- internal/agent/data.go：verify_result 接入 VisualVerifier；
- internal/agent/input_summary.go：继续省略文本 prompt 中的 base64（保留），图像改走 multi-content part；
- internal/api/model_profiles.go：API + 权限 + SSRF + 限流；
- internal/models：Plan/Node/Artifact verification 字段；
- internal/prompts：verify 报告 prompt 注入 verdict；
- metrics/audit：视觉调用、失败、成本、profile 变更；
- 配置：MODEL_PROFILE_ENCRYPTION_KEY、ADMIN_TOKEN、VISION_MAX_EDGE_PX、VISION_MAX_IMAGES、VISION_TIMEOUT_SECONDS、FEATURE_VISION_VERIFICATION。

前端：

- 设置页模型管理；
- 校验徽标/详情/artifact 角标；
- 事件处理；
- 成本/隐私提示。

## 5. 测试与验收

### 5.1 单元测试

- 加密/解密、掩码、密钥不可回显；
- routing 解析矩阵：user override / global default / no profile；
- probe：红/蓝双图正确/错误/超时/不支持 image_url；
- 结构检查：合法图、纯白、全黑、1x1、损坏、超大；
- verdict 解析：合法 JSON、非 JSON、未知字段、置信度范围；
- 汇总：多图 fail/inconclusive/pass；
- fail 仍 completed 的状态语义；
- 缓存命中/失效。

### 5.2 API/集成测试

- 用户 A/B profile 隔离；全局只读；管理员写；
- key 加密落盘、接口掩码、日志无 key；
- 保存后热更新（无需重启）；
- SSRF 拒绝内网 base_url；
- 主模型无视觉 + 备用视觉路由；
- 没有视觉 -> inconclusive；
- 视觉失败 -> 下一个候选 -> 全部失败 -> inconclusive；
- 真实 httptest vision server 返回 pass/fail/垃圾 JSON。

### 5.3 容器 E2E

- 生成多种图：折线、柱状、散点、热力图；
- 负例：空白图、损坏 PNG、与请求不符的图；
- 场景 A：主模型有视觉 -> 使用主模型；
- 场景 B：主模型无视觉 + 备用视觉 -> 只用备用判图；
- 场景 C：全部无视觉 -> inconclusive，UI 显示原因；
- 场景 D：视觉 fail -> 计划 completed + verification fail + UI 徽标/事件；
- 截图与事件断言；不包含任何 sin/cos 专用断言。

### 5.4 验收标准

- 前端可添加/测试/探测/删除 profile，key 不回显；
- 用户配置只影响自己的计划；全局默认可用；
- 主模型能力探测正确，路由符合规则；
- verify_result 对任意图像走通用结构 + 视觉判断，无图表类型硬编码；
- 没有视觉模型时返回 inconclusive 并在 UI 说明；
- 视觉 fail 时计划 completed、verification=fail、artifact 标记、事件/指标可见；
- 旧计划/旧前端兼容；
- 容器内测试与 E2E 通过，无密钥泄漏。

## 6. 分阶段落地

1. P0：modelprofile 存储/加密/CRUD + global bootstrap + 前端设置页骨架；
2. P1：ModelResolver + 热更新 + 现有 Agent/Planner 接入；
3. P2：结构门禁 + verification 字段 + verify_result 接入（无视觉时 inconclusive）；
4. P3：probe + VisualVerifier + 主/备用视觉路由 + 缓存/成本；
5. P4：前端校验展示 + 事件/指标 + E2E；
6. P5：可选 reverify / blocking 策略 / 成本预算。

## 7. 决策记录

- D1：配置范围 = per-user + 全局默认（已确认）。
- D2：无视觉 = inconclusive（已确认）。
- D3：视觉 fail = completed + verification fail（已确认）。
- D4：视觉模型只判图，报告仍由主文本模型生成。
- D5：不按图表类型写规则；结构检查保持通用。
- D6：视觉调用默认开启，但受 per-plan 上限与隐私提示约束；可用 FEATURE_VISION_VERIFICATION 关闭。
- D7：全局 profile 修改需要管理员；普通用户只读。

## 8. 开放问题

- 全局默认的管理入口：ADMIN_TOKEN vs 管理员账号体系（当前只有 owner 身份，建议先 ADMIN_TOKEN）。
- 多图校验策略：逐图调用 vs 拼一张联系表（逐图更准确但更贵）。
- 视觉 verdict 的置信度阈值是否影响 pass/fail（建议只记录，不自动降级）。
- 是否允许用户为单次计划覆盖路由（当前设计只按用户 routing）。
- verification 历史保留时长与清理策略。
