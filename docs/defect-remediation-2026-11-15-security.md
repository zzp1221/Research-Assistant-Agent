# 安全边界与 PDF 依赖修复方案

- 日期：2026-11-15（Asia/Shanghai）
- 版本：v1.2（2026-11-16 更新实施、实测及迁移状态）
- 状态：**已落地代码与专项回归，完整发布验收尚未关闭**；逐项证据见 [实施与验证记录](defect-remediation-2026-11-16-security-results.md)
- 审阅基线：`d4d2af1`
- 范围：Windows 原生执行环境、PDF 阅读器、AppContainer 安全探测、Docker 镜像白名单。

本文保留 2026-11-15 的基线问题、修复规格和完成标准；代码位置与旧依赖版本描述对应该基线。2026-11-16 已实施代码与专项回归，实际实现、测试分母和未验收边界以独立 [实施与验证记录](defect-remediation-2026-11-16-security-results.md) 为准。未读取真实凭据，未验证恶意 PDF 对桌面桥接的进一步影响或完整宿主逃逸。本文建议名称不应直接作为当前实现证明。

升级操作另见 [Windows 原生运行时迁移](security-native-runtime-migration-2026-11-16.md) 和 [镜像精确授权迁移](security-image-policy-migration-2026-11-16.md)。双 runner 的所测科研绘图、在线/离线 TCP 对照和真实 Docker 集成已有实测；AppContainer 临时目录/进程池兼容与最终产品交付仍有未验收项，不能将专项回归通过表述为全部 27 项发布验收通过。

## 1. 问题清单与关闭条件

P1/P2 表示本项目的整改优先级；除引用官方漏洞公告外，不据此推定 CVSS 或已发生攻击。

| 编号 | 优先级 | 已确认事实 | 影响范围与条件 | 最小关闭条件 |
|---|---|---|---|---|
| SEC-01 | P1 | Windows 研究进程继承包含内部 token 的父环境；环境构造不做权限过滤 | 凭据进入研究进程的路径明确；进一步控制沙箱服务需要进程可达该 API | 所有研究执行入口及第三方构建入口使用安全环境；任务参数不能回填内部凭据；AppContainer/lowil 真机验证通过 |
| SEC-02 | P1 | PDF.js 3.11.174 命中 CVE-2024-4367；阅读器未显式禁用 eval | 特制 PDF 在受影响配置下可执行同源 JavaScript；本次未验证桌面桥接的进一步影响 | 立即关闭 eval 并验证真实加载参数；正式关闭还需升级到受维护、已修复版本，确认 worker 一致和阅读回归 |
| SEC-03 | P1 | 探针未通过真实 AppContainer 执行；越界写入/联网成功被记为通过 | 当前探测结论不能证明隔离有效；不等于已经证实所有隔离路径失效 | 在指定隔离环境中独立验证允许/拒绝规则；失败、超时、缺项均不能产生安全通过结论 |
| SEC-04 | P2 | 镜像白名单使用字符串前缀/后缀匹配 | 可接受相似 tag 或其他 registry 的同名镜像；实际执行仍受镜像可用性及审批等约束 | 拉取前、镜像改写后和创建前共用规范引用校验；反例拒绝、可信镜像及镜像代理正例通过 |

四项均可独立开发；SEC-03 的集成验收依赖 SEC-01 的安全进程环境。单人本机使用也会处理外部 PDF 和研究仓库，应按实际输入来源验收。

## 2. 信任边界与证据

### 2.1 应保持的边界

```text
桌面外壳 / 后端 / 沙箱控制服务（持有管理凭据）
                    |
         受控命令、最小环境、工作区、资源限额
                    v
研究代码 / 第三方构建脚本（不持有控制服务凭据）

外部 PDF -> PDF 解析与渲染 -> 工作台页面
            不允许文档触发任意同源 JavaScript

计划镜像 -> 规范化引用与可信来源校验 -> 拉取与创建
            不因名称相似或地址改写扩大信任范围
```

网络隔离、环境隔离、鉴权和人工审批分别承担不同职责。禁网不能阻止进程读取自身环境；前端审批也不能补偿执行进程获得全局控制 token 的问题。

### 2.2 基线源码位置

以下行号对应审阅基线，实施时以函数名和实际调用关系为准。

| 问题 | 证据位置 | 观察 |
|---|---|---|
| SEC-01 | [desktop/settings/settings.go](../desktop/settings/settings.go#L159) 的 `SandboxEnv` | 向 sandbox 服务注入 `SANDBOX_API_TOKEN`，服务监听本机地址 |
| SEC-01 | [rawprocess_windows.go](../research-assistant/docker-sandbox/internal/engine/native/rawprocess_windows.go#L90) 的 `startRawProcess`、`buildEnvironmentBlock` | 将 `os.Environ()` 合入研究进程；构造函数仅归一化、去重、排序 |
| SEC-01 | [runner_windows.go](../research-assistant/docker-sandbox/internal/engine/native/runner_windows.go#L72) 的 `runExecProcess`；[runtime.go](../research-assistant/docker-sandbox/internal/engine/native/runtime.go) 的运行时安装路径 | 备用执行与 uv 路径也需纳入环境审查，避免仅修一处 |
| SEC-01 | [main.go](../research-assistant/docker-sandbox/main.go#L1255) 的沙箱路由；[runtime_gate.go](../research-assistant/backend/internal/scheduler/runtime_gate.go#L152) | 沙箱管理 API 共用 token；后端 RuntimeAction 审批属于另一层控制 |
| SEC-02 | [package.json](../research-assistant/frontend/package.json#L34)、[package-lock.json](../research-assistant/frontend/package-lock.json) | PDF.js 实际锁定 3.11.174；阅读器声明 PDF.js 2.x/3.x peer 范围 |
| SEC-02 | [PdfPanel.tsx](../research-assistant/frontend/src/features/pdf-viewer/PdfPanel.tsx#L54)、[sync-pdfjs-assets.mjs](../research-assistant/frontend/scripts/sync-pdfjs-assets.mjs) | 加载参数未显式关闭 eval；本地同步脚本假定 worker 为 `.js` 文件 |
| SEC-03 | [probe_windows.go](../research-assistant/docker-sandbox/internal/engine/native/probe_windows.go#L29)、[probe.go](../research-assistant/docker-sandbox/internal/engine/native/probe.go#L17) | 普通进程运行探针；统一以“未抛异常”作为通过 |
| SEC-03 | [isolation.go](../research-assistant/docker-sandbox/internal/engine/native/isolation.go)、[engine.go](../research-assistant/docker-sandbox/internal/engine/native/engine.go) | probe 结果参与隔离选择；需要处理初始化依赖及显式设置行为 |
| SEC-04 | [docker_security.go](../research-assistant/docker-sandbox/internal/engine/docker_security.go#L102)、[docker_security_test.go](../research-assistant/docker-sandbox/internal/engine/docker_security_test.go) | 前缀/后缀匹配；旧测试明确允许 tag 前缀扩展 |
| SEC-04 | [docker.go](../research-assistant/docker-sandbox/internal/engine/docker.go)、[docker-compose.yml](../research-assistant/docker-compose.yml) | 镜像代理地址改写与默认白名单需要同步迁移 |
| 配套门禁 | [ci.yml](../.github/workflows/ci.yml) | Go 漏洞扫描和前端依赖审计当前均为非阻断步骤 |

补充界限：桌面配置将用户填写的 LLM Key 单独传给后端，不能由 SEC-01 推断其必然进入 sandbox 子进程；宿主父环境中原本存在的其他秘密仍需由白名单隔离。

## 3. SEC-01：研究进程环境隔离

### 3.1 根因与目标行为

当前 `SandboxEnv` 为可信控制服务提供凭据，研究进程启动时却继承整份服务环境。AppContainer 和 lowil 都走这一环境构造路径。即使本机 API 不可达，研究程序仍可能把继承到的值写入输出或产物。

目标是从空环境构造研究进程配置，只增加运行所需变量。不能仅移除 `SANDBOX_API_TOKEN`，也不能用 `TOKEN`、`KEY` 等名称黑名单代替最小授权。

### 3.2 实施步骤

1. 在 native 执行层增加统一环境构造与校验函数。输入区分可信运行时参数、受控宿主基础变量和任务申请变量；输出为新的环境快照，调用方不能修改内部共享 map。
2. Windows 基础变量按需要显式选择，例如 `SystemRoot`、`WINDIR`。解释器使用已解析的绝对路径，`PATH` 由可信系统目录与任务 venv 构造。临时目录、缓存和用户配置目录指向隔离工作区并授予所需 ACL，避免继承宿主用户目录和任意搜索路径。
3. 任务可设置的变量使用显式规则，例如随机种子、线程数、业务参数。`PATH`、`PYTHONPATH`、`PYTHONHOME`、启动注入变量、代理和内部配置不能通过任意 `command.Env` 覆盖；确有需求时由可信策略构造。
4. 内部控制变量始终禁止传入研究进程，包括 `SANDBOX_API_TOKEN`、`API_AUTH_TOKEN` 及身份签名秘密。Windows 环境名按不区分大小写处理；拒绝歧义重复键、非法名称、NUL 和越界长度，先校验再序列化。
5. `startRawProcess` 与 `runExecProcess` 共用策略。检查 Python、命令流式执行及探针出口；uv/pip 等可能运行第三方构建钩子的路径采用专门的安装环境策略，同样不携带控制凭据。可信控制服务继续保留其所需配置。
6. 在 `TranslateCommand` 到最终创建进程之间设置不可绕过的校验，防止任务环境重新填入被过滤的变量。任务确需外部业务凭据时，另行提供按任务授权机制，不能复用服务管理 token。
7. 对已运行的旧进程和会话执行升级重建；轮换可能暴露的内部 token。核查日志和产物的访问范围及留存策略，取证使用合成哨兵值，不把真实凭据写进报告。

### 3.3 后续纵深加固

最小修复必须切断所有执行路径的凭据继承。独立后续工作可将单一全局 token 拆成短期、按 sandbox/plan/操作授权的凭证，限制管理 API 的可达性，并在服务端消费必要的审批授权。该协议变更需单独设计版本兼容与授权测试，不能把未来能力写为本轮已有修复。

### 3.4 验收用例

| 用例 | 执行方式 | 必须满足 |
|---|---|---|
| ENV-01 父环境秘密隔离 | 在测试父环境设置合成控制 token 和一个未知名称的哨兵变量，经真实进程入口检查 | 研究进程不包含这些键和值；测试日志只记录布尔断言 |
| ENV-02 任务参数回填 | 经 `command.Env` 传入受保护名称、不同大小写及危险路径变量 | 启动前拒绝并返回明确原因；不执行命令 |
| ENV-03 基础科研流程 | Python import、venv、临时文件、子进程、中文路径、离线绘图 | 正常运行，缓存和临时产物位于授权目录 |
| ENV-04 执行入口一致 | 覆盖 raw、备用 exec、流式命令和 uv/pip 第三方 build hook | 没有从备用路径泄露环境；安装策略不扩大研究权限 |
| ENV-05 双 runner 验证 | Windows 分别强制 AppContainer 和 lowil | 两者均不继承控制 token；明确记录实际 runner |
| ENV-06 控制服务回归 | 后端调用 sandbox、健康检查、重启、正常审批，以及管理 API 无凭据/错误凭据请求 | 服务自身仍可鉴权；除公开健康检查外，管理 API 拒绝未授权请求；研究进程始终无管理凭据 |

## 4. SEC-02：PDF.js 漏洞缓解与版本迁移

### 4.1 公告与修复边界

[GHSA-wgrm-67xf-hhpq / CVE-2024-4367](https://github.com/advisories/GHSA-wgrm-67xf-hhpq) 指出：`pdfjs-dist <= 4.1.392` 在默认 `isEvalSupported=true` 时，加载特制 PDF 可执行同源 JavaScript；首次修复版本为 `4.2.67`，官方缓解措施为关闭 `isEvalSupported`。

当前锁定版本为 `3.11.174`。本项确认依赖风险和触发配置，未验证恶意文件执行、数据外传或桌面原生能力调用。首次修复版本只是漏洞修复下界，实施时应选择仍受维护且审计通过的版本。

### 4.2 A 批：立即缓解

1. 在 `transformGetDocumentParams` 中将 `isEvalSupported: false` 写在展开 `options` 之后，保证调用方无法覆盖。保留 cMap、字体和阅读状态参数。
2. 检查全仓库是否还有其他 PDF 加载入口，统一使用安全参数。
3. 组件测试捕获传给真实 Viewer 接口的转换函数；即使输入包含 `isEvalSupported: true`，输出也必须为 `false`。再用浏览器测试确认真实引擎接收到该配置。
4. 记录缓解后的阅读性能和兼容性；配置关闭 eval 不等于依赖升级完成，不将漏洞项标为已彻底修复。

### 4.3 B 批：升级与阅读器适配

1. 当前 `@react-pdf-viewer/core@3.12.0` 的 peer 范围为 PDF.js 2.x/3.x。先验证可支持已修复引擎的阅读器版本；若不可用，维护经过测试的适配层或替换阅读器。不要用 `--force` / `--legacy-peer-deps` 掩盖兼容问题。
2. 升级依赖并提交锁文件，确认依赖树没有仍被实际加载的旧引擎。同步评估高亮、翻译、页码控制与引用定位插件。
3. 同步更新本地 worker、cMaps、standard fonts 以及静态资源复制脚本。新版本可能使用 `.mjs` worker，需要同时调整加载 URL、模块类型与静态服务 MIME；不能只升级主库。
4. 清理部署中已废弃的旧 worker 资源，使用构建版本或 hash 避免浏览器缓存混用。Web、离线部署和桌面内嵌资源均从同一构建产物交付。
5. 升级后继续显式关闭 eval。CSP 可作为后续防护，但不能替代本项参数修复和版本升级。

### 4.4 验收用例

| 用例 | 必须满足 |
|---|---|
| PDF-01 参数覆盖 | 所有 PDF 加载入口最终 `isEvalSupported=false`，输入无法覆盖 |
| PDF-02 真实引擎与资源 | 主库和 worker 版本一致；无旧 worker、版本不匹配、模块/MIME 错误 |
| PDF-03 阅读回归 | 文本、中文、扫描件、多页、字体、缩放、翻页和阅读位置恢复正确 |
| PDF-04 业务集成 | 高亮、选区、引用定位、连续对话、翻译收放及窄窗口交互正常 |
| PDF-05 三种交付 | Web、离线和桌面包均加载新构建中的本地资源 |
| PDF-06 漏洞关闭 | 已加载引擎不在公告受影响范围；扫描不再报告该旧版本；保存依赖树和构建清单 |

如增加官方漏洞回归样本，应在无真实账户、无外网出口的一次性浏览器环境执行，只断言合成标记未执行，不纳入普通用户数据。正常 PDF 的成功渲染不能单独证明漏洞已关闭。

## 5. SEC-03：隔离探针与决策修复

### 5.1 根因与目标行为

当前探针通过普通 `exec.CommandContext` 启动 Python，安全项尝试执行越界写入、联网和打印 PID；脚本将无异常统一记为通过。普通进程上的成功结果不能证明 AppContainer 有效，打印 PID 也不能证明子进程被清理。

新报告必须区分功能兼容、安全约束和探针基础设施故障。只有在实际隔离环境中观察到预声明行为，才能记录通过。

### 5.2 实施步骤

1. 每个用例声明预期，例如 `allow`、`deny` 或 `cleanup`，记录实际操作结果及判定原因。不能用一个全局“异常即失败”规则覆盖所有测试。
2. 探针调用底层显式 AppContainer 启动路径，绕开 `auto` 的选择分支，但保留生产执行器的 ACL、环境净化、Job Object、限额和网络 profile。禁止重新调用 `Provider.Create` 造成运行时初始化递归。
3. 当前初始化先准备运行时，再获得 probe 并设置 runner。实施时分离“准备解释器”“启动指定隔离进程”“评估隔离能力”三个阶段，探针自身不能依赖其尚未生成的通过结果。
4. 功能兼容与安全用例分别在受控进程中执行。需要外部包的兼容用例明确前置依赖；缺包、安装失败、解释器启动失败、超时或输出不完整记为 `error/inconclusive`，不能计安全通过。
5. 越界写测试使用测试框架创建并可观测的宿主哨兵路径，先证明可信宿主进程可写，再验证隔离进程被拒绝。后置检查文件未被修改，并清理测试自有资源，避免依赖共享 Public 目录。
6. 网络测试使用受控测试端点和父进程可达性对照。端口关闭、DNS 故障、路由不通、超时均不足以证明 OS 阻断；无法区分环境故障与策略拒绝时记为不确定。离线与在线 profile 分开测试，不以访问公共互联网作为唯一证据。
7. Job 清理测试由可信监督进程观察父子进程生命周期：创建受控子进程，触发退出/超时/取消，确认整棵进程树终止。不能依赖被测脚本自报清理成功。
8. 聚合器检查必需用例名称、唯一性、版本和分母。缺项、重复、未知字段语义或空报告均不得产生 `AppContainerOK=true`。
9. 版本化报告并绑定 OS、runner、执行器构建、probe 版本、解释器/环境 hash、实际网络 profile 与相关隔离配置。旧报告失效，升级、切换 Python/venv 或配置变化后重测；Provider 级缓存不能让不同运行时借用兼容性通过结论。

### 5.3 隔离决策与产品行为

- 显式请求 AppContainer 时，基础设施故障、必需安全项失败或无法验证应返回结构化 `blocked`，不能以同名状态继续低隔离运行。
- `auto` 如退到 lowil，必须保留实际隔离级别和原因；需要 OS 级断网能力的任务应阻断，不能以逻辑网络开关替代操作系统能力。
- 兼容模式可继续用于明确接受其能力边界的任务，现有低隔离提示需要保留。若安全探针证实越界能力，不能把失败仅当成兼容性差异。
- 新增报告字段时同步后端/前端契约、运行时设置页、capabilities 和诊断包。失败报告保留诊断事实，不含环境秘密。

### 5.4 验收用例

| 用例 | 必须满足 |
|---|---|
| ISO-01 聚合反例 | 空、缺项、重复、旧版本、超时和解析失败均不能得到安全通过 |
| ISO-02 预期方向 | 允许操作成功为通过；拒绝操作成功为失败；失败原因不明确为不确定 |
| ISO-03 真实隔离 | Windows 真机记录实际 AppContainer 身份、profile 和进程启动路径，无宿主普通进程替代 |
| ISO-04 文件与网络 | 工作区正例、工作区外反例、离线网络反例、在线网络正例及宿主对照均符合预期 |
| ISO-05 清理与恢复 | 取消、超时、探针崩溃后无遗留子进程和测试文件；下次探测可重新执行 |
| ISO-06 决策一致 | 显式强隔离失败时 blocked；auto/lowil 能力与提示真实；初始化无循环依赖 |
| ISO-07 环境一致 | 探针与科研进程共用 SEC-01 的环境策略，不携带管理凭据 |
| ISO-08 缓存失效 | 切换 Python、venv、依赖环境、网络 profile 或隔离配置后重新探测，不复用另一运行时的通过结论 |

跨平台单元测试用于聚合和策略判断；Linux 的 AppContainer 用例应明确不适用。Windows CI 不具备必需能力时保留不确定状态，并安排具备能力的专用真机验收，不能用 skip 代替发布通过。

## 6. SEC-04：镜像引用校验与白名单迁移

### 6.1 当前问题

`HasPrefix` 会让 `python:3.11-bullseye-evil` 匹配 `python:3.11-bullseye`；`HasSuffix` 会允许任意 registry 下的同名引用。现有 `TestSandboxImageAllowlist` 还明确期望 `python:3.11` 接受 `python:3.11-slim`，因此收紧规则属于配置语义迁移。

### 6.2 实施步骤

1. 使用成熟的镜像引用解析器，规范化 registry、repository、tag 和 digest；明确 Docker Hub 短名称、`library/` 命名空间、registry 端口和无 tag 的规则。不对整个引用盲目转小写，tag 按其语义比较。
2. 最小方案将现有逗号列表升级为精确引用列表，例如 `python:3.11-bullseye` 规范化为 `docker.io/library/python:3.11-bullseye`。不同 tag 必须显式列举；不再隐式支持字符串前缀或任意路径后缀。
3. 既无显式 tag 也无 digest 的引用、空白规则、无效引用或无法识别的旧目录前缀规则在启动配置校验时给出明确错误。`repository@sha256:<digest>` 是合法的 digest-only 引用，不要求额外 tag。容器 provider 未配置有效白名单时采用显式默认可信集合或拒绝启动，不能隐式解释为全部允许；纯 native 模式不因此被阻断。
4. 拉取/检查镜像之前校验逻辑来源；`EnsureImage`、创建入口、重试和 fallback 共用同一规则。校验不通过时，不应先触发镜像拉取等网络副作用。
5. registry mirror 使用显式可信映射：先校验逻辑来源，再校验实际地址属于配置中的代理及允许的 repository 映射。记录逻辑引用与实际引用，不能对任意域名做后缀豁免。
6. 对高可信科研环境固定 `repository@sha256:<digest>`。批准与验收记录引用实际解析 digest，运行时使用同一 digest；tag 在批准后发生变化时需重新解析和校验，避免审批对象与执行内容不一致。
7. 明确 tag+digest 的共同约束及多架构镜像索引/平台 manifest 的记录方式；解析失败、规则冲突和未授权的地址改写均拒绝执行。
8. 同步 Compose 默认值、环境变量示例、运行时镜像配置及迁移说明；调整依赖旧前缀行为的测试。需要 namespace 范围授权时另建结构化规则，显式约束 registry 和路径边界，不复用模糊前缀。

### 6.3 验收用例

| 用例 | 输入或场景 | 必须满足 |
|---|---|---|
| IMG-01 精确正例 | `python:3.11-bullseye` 及其规范 Docker Hub 全名 | 规范化后按同一规则允许 |
| IMG-02 相似 tag | `python:3.11-bullseye-evil` | 拒绝；不触发拉取 |
| IMG-03 非可信来源 | `evil.example/python:3.11-bullseye` | 拒绝，即使 repository/tag 后缀相同 |
| IMG-04 代理映射 | 配置中的可信代理及另一个未配置域名 | 可信映射允许；伪代理拒绝 |
| IMG-05 边界输入 | 空列表、无效引用、既无 tag 也无 digest、registry 端口、大小写和旧前缀规则 | 结果符合明确配置契约，不能退回全部允许 |
| IMG-06 digest 约束 | 已授权的合法 digest-only 引用、错误 digest、tag+digest、批准后 tag 漂移 | digest-only 正例允许；实际运行内容与授权内容一致，不匹配时拒绝 |
| IMG-07 全入口一致 | `EnsureImage`、创建、重试、mirror、fallback | 每条路径均在副作用前完成对应授权 |

## 7. 实施批次与交付物

| 批次 | 内容 | 交付物 | 完成标准 |
|---|---|---|---|
| A：切断直接风险 | SEC-01 环境净化；SEC-02 关闭 eval | 执行环境策略、所有出口覆盖、PDF 参数缓解与专项测试 | ENV、PDF-01 及基本功能回归通过；依赖漏洞状态仍标记“已缓解、待升级” |
| B：恢复可信安全判定 | SEC-03 探针/决策；SEC-04 精确白名单 | 版本化 probe、真机证据、镜像规则与迁移说明 | ISO/IMG 用例通过，无错误的安全通过结论 |
| C：完成依赖迁移与发布门禁 | SEC-02 引擎/阅读器适配；配套 CI | 锁文件、同步资源、三种交付产物和漏洞复核报告 | PDF 全部验收通过，明确支持版本，安全回归阻断发布 |
| 后续独立加固 | 任务级授权凭证、管理 API 授权细化、CSP | 独立设计及兼容方案 | 单独评审验收，不借用 A–C 的结果宣称已完成 |

各批次可按独立 PR 交付。最终以修复后的 commit 和构建产物重新验收，不能直接复用基线测试数量作为修复证明。

## 8. 验证命令、CI 与证据

### 8.1 建议复跑范围

以下命令是完整验证入口，已执行范围和结果另见实施记录；命令本身不代表全部通过。命令均从所注明模块目录运行，不需要真实模型或付费 API。Windows 全量已有平台相关失败需单独记录，不能被后续 vet 的退出码覆盖。

```powershell
# research-assistant/docker-sandbox；Linux 和 Windows 分别执行
go test -count=1 ./...
go vet ./...

# desktop；验证启动配置与服务编排兼容
go test -count=1 ./...
go vet ./...

# research-assistant/backend；验证审批与调用边界
go test -count=1 ./internal/scheduler ./internal/sandbox

# research-assistant/frontend；使用实施后更新并提交的锁文件
npm ci
npm test
npm run lint
npm run build
npm run check:bundle
npm run check:pdf-security
npm run test:e2e -- test/e2e/pdf.spec.ts test/e2e/workbench-pdf-chat.spec.ts test/e2e/pdf-engine-security.spec.ts
npm audit --omit=dev --json
```

浏览器 E2E 需按 [frontend/README.md](../research-assistant/frontend/README.md) 和 Playwright 配置启动对应服务、安装浏览器。Windows 原生隔离另执行 ISO 真机矩阵，不能用 Go 交叉编译替代。

### 8.2 CI 门禁调整

1. 将环境泄漏、任务环境回填、探针方向和镜像误匹配加入可重复的阻断测试；Windows 必需用例使用真实可运行的 fixture，不依赖 POSIX shell。
2. 当前漏洞扫描是 informational。针对本轮 CVE 和新引入的高风险运行时依赖设置阻断规则；其余历史项先建立可审查基线，再逐项关闭，避免无差别新增门禁后长期红灯。
3. 如漏洞项暂以官方措施缓解，例外必须记录公告、实际加载路径、缓解证据、负责人及截止日期；不能全局忽略 high/critical，也不能把审计服务不可达解释为扫描通过。
4. 固定扫描工具版本并定期更新。前端依赖树、worker 版本、构建指纹和实际部署资源必须一致，测试源码配置通过不能替代产物检查。
5. 浏览器回归和 Windows 隔离证据不足时保持“待验收”。已有平台测试失败逐项记录根因，不得用本次新增通过项覆盖它们。

### 8.3 证据记录模板

原始记录按 [reports/README.md](../reports/README.md) 约定写入本地 `reports/security-remediation-<run-id>/`；脱敏结论另写实施报告并更新 [verification.md](verification.md)。

| 字段 | 要求 |
|---|---|
| 代码与产物 | 修复 commit、工作树状态、锁文件 hash、桌面/Web 构建版本 |
| 环境 | OS、Go/Node、实际 provider/runner、网络 profile、镜像逻辑/实际引用和 digest |
| 用例 | ENV/PDF/ISO/IMG 编号、执行入口、预期、观察、`pass/fail/error/inconclusive/skipped`；`error` 表示执行或基础设施错误，`inconclusive` 表示证据不足以判定 |
| 分母 | 计划总数、实际执行数、通过/失败/错误/不确定/跳过数；必需项零跳过，错误和不确定不计通过 |
| 安全证据 | 使用合成值的隔离断言、主库/worker 版本、可信监督进程观察和镜像拒绝记录 |
| 边界 | 未执行的系统、runner、浏览器和交付形态，不外推成功率或宿主安全结论 |

截图和日志只保留所需事实，不包含真实 token、用户 PDF、账户数据或完整宿主环境。

## 9. 升级、回退与完成定义

### 9.1 升级与兼容

- 环境净化会改变科研脚本对宿主变量的依赖。发布说明列出支持的变量和任务参数接口；缺失能力返回可操作错误，不恢复整份环境继承。
- SEC-01 部署后重建旧研究进程，并轮换可能暴露的控制 token。历史产物按既有数据策略保留，不因修复批量修改实验结果。
- SEC-03 升级使旧 probe 失效，重新探测并显示原因；显式强隔离需求不得悄然改为 lowil。
- SEC-04 在发布前提供配置校验和旧规则迁移清单。无效规则阻止相应容器执行，避免运行到一半才发现授权范围变化。
- PDF 升级须同时发布前端主包和静态 worker，桌面内嵌包同步重建。缓存更新与离线资源回归完成后再开放阅读。

### 9.2 回退原则

- 环境修复导致兼容问题时，暂停受影响任务或补充经过审阅的最小变量，不恢复内部凭据继承。
- PDF 新引擎不兼容时，优先回退到仍保留 `isEvalSupported=false` 的缓解版本；若缓解无法验证，暂停相关阅读入口，保留文件与用户阅读状态。
- 探针无法可靠运行时，返回能力不可确认；依据任务要求阻断或明确选择兼容模式，不恢复旧探针的错误通过结论。
- 镜像规则误拒绝时，修正精确条目或可信代理映射，不恢复任意 registry 后缀放行。

### 9.3 完成定义

下列保留完整发布关闭标准。代码修复专项测试已通过的部分不自动勾选仍含部署或科研兼容前提的整项；详见实施报告的 27 项矩阵。

- [ ] SEC-01：所有研究/第三方构建出口均不继承控制凭据；任务输入不可回填；Windows 双 runner 有实测证据。
- [ ] SEC-02：全入口关闭 eval；实际引擎已升级且 worker 一致；阅读与交付回归通过；公告命中关闭。
- [ ] SEC-03：探针真实运行于指定隔离环境，允许/拒绝方向正确，未知不计通过，报告版本与产品决策一致。
- [ ] SEC-04：规范化精确授权、可信代理、digest 与副作用前校验落实，配置及旧测试迁移完成。
- [ ] 修复版本、测试分母、失败与未验证边界进入独立实施报告；必要 CI 为阻断门禁。

只有完成对应验收后，才能将该项从“待实施/已缓解”改为“已修复”。任务级凭证等后续设计未实施时，继续保留其边界说明。
