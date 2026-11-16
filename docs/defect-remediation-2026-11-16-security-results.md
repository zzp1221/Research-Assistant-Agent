# 安全边界与 PDF 依赖整改实施记录

- 日期：2026-11-16（America/Phoenix）
- 状态：**四项修复的代码与针对性回归已完成；完整发布验收待完成**。
- 审阅基线：`d4d2af1a5fda67fbd027f5d1e5cfe9116ace20fa`；以下测试记录来自提交前的修复工作树，实际提交与远程检查状态以关联 PR 为准。已构建本轮 MyGo exe，尚未正式发布。
- 对应规格：[安全边界与 PDF 依赖修复方案](defect-remediation-2026-11-15-security.md)。原方案保留基线风险和关闭标准，本文记录后续实施事实。

本轮已切断 native 执行环境的整份父环境继承，升级 PDF.js 并固定安全参数，替换隔离探针的启动和判定流程，收紧镜像授权。**这不代表 27 项完整验收全部通过，也不代表当前机器的 AppContainer 已满足全部科研兼容条件。** 无法证明的能力保留 `inconclusive/blocked`；未执行的交付与运维验收列在下文。

## 1. 四项整改的当前结论

| 项目 | 已落地内容 | 当前结论 |
|---|---|---|
| SEC-01 环境隔离 | 从空环境构造执行环境；raw、备用 exec、流式执行和 uv 构建入口共用策略；任务环境在翻译和启动时校验 | 合成秘密隔离及双 runner 真实 CPython、numpy/matplotlib 离线绘图通过；升级后重建旧进程与凭据轮换未操作 |
| SEC-02 PDF.js | 升级至精确版本 `6.5.438`，维护阅读器兼容适配；关闭 eval；版本化本地 worker 和资源；静态与浏览器门禁 | 本轮依赖已排除 CVE-2024-4367 受影响版本，浏览器回归和桌面嵌入资源检查通过；最终 exe/安装包人工验收待完成 |
| SEC-03 隔离判定 | 真实 AppContainer 探针、可信监督进程核验、严格聚合、环境指纹和执行前重测；独立运行时副本避免源 ACL 污染 | 真实文件边界、离线 TCP 拒绝和在线 TCP 允许有证据；标准库临时目录与进程池兼容仍不确定，完整强隔离请求保持阻断 |
| SEC-04 镜像授权 | 规范化精确引用、可信代理映射、副作用前校验、digest 验证、不可变本机 image ID、来源字段透传 | 单元/HTTP 回归及真实 Docker 的 digest、tag 漂移、可信 registry 拉取和 CPython 执行通过；远端内容固定及更多平台待验 |

## 2. 实现与行为变化

### 2.1 SEC-01：不向研究进程传递管理环境

`native/environment.go` 统一构造研究和安装环境。Windows 系统目录通过 OS 接口发现，解释器使用可信绝对路径；`HOME`、`USERPROFILE`、临时目录及常用缓存移至工作区的 `.ra-env`。实际进程创建代码不再调用 `os.Environ()` 继承服务环境。

任务参数采用科研变量白名单和 `RA_TASK_*` 命名范围，检查大小写重复键、非法名称、NUL、大小和数值边界。管理 token、任意 `PATH`、代理及启动注入变量不能回填。`PYTHONPATH` 仅从逻辑工作区路径生成，启动前再次校验；目录穿越、外部绝对路径和真实 Windows junction 逃逸有回归用例。

uv 安装使用单独环境、私有缓存和 `UV_LINK_MODE=copy`，关闭宿主配置继承。构建钩子通过真实子进程夹具验证；科研集成另通过受限安装环境从 PyPI 安装指定 wheel。raw 进程在加入 Job 后恢复线程；静默进程取消不再等待输出管道自然关闭。

双 runner 的环境隔离使用真实静态 Go 进程和 CPython 3.12 验证。此前 lowil 的 `0xC0000135` 被定位为共享夹具受到 AppContainer ACL 污染；现已为 AppContainer 使用独立副本，lowil 使用原运行时，两个 CPython 分支均严格通过，不再容忍加载失败。

真实科研集成在两个 runner 分别安装 `numpy==2.2.6`、`matplotlib==3.10.8`，通过 import、venv、私有临时文件、子进程、中文路径与工作区根目录的 Agg PNG 绘图，总计 106.605s。lowil 工作区根目录及 `.ra-env` 的已知目录采用绑定 handle 的非递归、非继承低完整性标签；真实硬链接/junction 反例确认外部对象与既有文件的标签、DACL 不变。此项不把 AppContainer 的所有标准库用法或全部科研包宣布为兼容。

### 2.2 SEC-02：升级引擎并保持阅读器行为

PDF.js 主库精确固定 `6.5.438`；`@react-pdf-viewer/core` 使用仓库内可审查的兼容版本，适配现代文本层、样式和取消行为。保留原高亮与布局插件，没有使用 `--force` 或 `--legacy-peer-deps` 掩盖依赖契约。

`PdfPanel` 在展开调用参数后写入 `isEvalSupported: false`，调用方不能重新开启。同步脚本复制版本化 `.mjs` worker、cMaps、standard fonts 和 wasm；清理旧 `.js` worker，生成资源清单。安全检查核对单一引擎、受影响版本范围、worker 内容 hash 和本地资源。

本轮真实 Chrome 的 10 条 PDF 用例全部通过，覆盖新引擎实际渲染文本与中文扫描件、模块 worker、MIME、翻译及窄窗口、选区连续对话和阅读状态。安全浏览器用例阻断并记录全部非部署源 HTTP 请求，两个 PDF 仍成功渲染且外部请求数为零，验证所测页面不依赖 CDN；这不是整台机器完全断网的实测。

桌面 staging 的完整 dist 共 282 个文件，使用真实 `go:embed` 核对各文件 SHA-256，并确认 `.mjs` 由嵌入服务返回 JavaScript MIME。本轮 `mygo build -skip-installer -skip-build-command .` 已成功生成 21,349,888 字节的 `desktop/build/mygo/windows-amd64/ResearchAssistant.exe`，没有启动 UI 或修改用户安装；安装包和最终产品运行验收仍待完成。

PR 的 Windows CI 暴露宿主 MIME 注册差异：同一 `.mjs` 在该 runner 被识别为 `text/plain`。桌面 proxy 现对 `.js/.mjs` 显式返回 JavaScript MIME、`.wasm` 返回 `application/wasm`；回归主动把宿主 MIME 映射改为纯文本，确认真实 handler 的 GET/HEAD、Range、类型及文件内容仍正确。该补充修复的远程结果以 PR 检查为准。

生产依赖审计已不再报告旧 PDF.js，但仍有 13 项其他历史依赖问题，其中 6 high、0 critical。没有据此宣称整个依赖树无漏洞；通用审计仍为 informational，本轮 PDF 专项检查为阻断门禁。

### 2.3 SEC-03：用真实证据决定隔离能力

报告版本为 `native.isolation-probe/v2`。八个必需用例分别声明预期：标准库、multiprocessing、工作区写入、实际进程身份、工作区外写入、离线联网、在线联网及 Job 子进程清理。探针使用显式 AppContainer 启动路径，由监督进程检查真实 token、哨兵文件及子进程 Job 成员关系和退出状态。

文件或联网操作成功时，拒绝项判为失败；只有可归因的 OS 拒绝才能支持拒绝项通过。超时、DNS、目标不可达、解释器故障和含糊输出不能证明安全。宿主先实际 TCP 连接运维配置的 `host:port`，将该连接的具体 IP:port 传给两个 profile，避免把离线 DNS 失败误判为 TCP 拒绝；IPv6 地址单独解析。

本机选用公共端点 `example.com:443`，仅进行 TCP 建连/关闭，无应用数据；真实离线 profile 得到 `Winerror 10013`，在线 profile 成功。该端点不属于项目自有服务，也不作为默认配置。受控 loopback 的在线对照仍超时并如实保留不确定；没有修改 loopback exemption 或宿主防火墙。

AppContainer 不再递归修改源运行时的 ACL。`prepareAppContainerRuntimeCopy` 只从 provider 管理的解释器、venv 与基础 Python 创建服务拥有的独立副本；使用稳定源 handle 校验最终路径与重解析点，复制为独立文件对象，再仅对副本逐对象授予非继承读执行 ACE。源文件即使为硬链接，也不会因授权而改变宿主对象 DACL。venv 的 `pyvenv.cfg` 改写到复制后的基础解释器，lowil 保留未受污染的原运行时。

真实并发源硬链接替换回归中，218 次替换尝试产生 8 份获准的独立副本，外部目标 DACL 始终不变；真实 AppContainer 对复制后的 Python 申请写入返回 `Winerror 5`。这覆盖了先前“预检后再递归授权”的竞态根因。副本按会话管理并纳入运行时缓存清理；不对用户任意 Python 安装开放权限。

生命周期回归还覆盖 Create 失败/lowil fallback 的副本清理与报告失效、实际 `VenvPath`、Delete/Reap 失败保留重试状态与释放锁、删除等待活动执行、活动会话拒绝清空运行时缓存。创建/执行/安装与删除/清理通过统一锁次序同步，避免仍在使用的解释器被并发移除。

环境指纹使用规范 JSON 编码的元数据、逐文件路径、长度与 SHA-256，回归实际复现了旧无分隔拼接的碰撞输入并证明新编码不同。指纹覆盖实际 Windows build、可加载字节码、venv 对应基础解释器和隔离配置。安全探针和 Job 子进程均以 `-I -S -B` 启动；真实对照确认普通 venv 启动会执行测试自有 `.pth/sitecustomize`，而 AppContainer 探针不会执行这些钩子。执行器策略版本为 `native-launch/v3`。探针前后比较指纹；Create/Exec 重新执行探针，Capabilities 快照超过 60 秒失效，缓存清理同步撤销报告和 runner 状态。

显式 AppContainer 不可验证、auto 观察到安全项明确失败，或离线任务最终 runner 没有 OS 级断网能力时，返回 `sandbox_capability`。显式 lowil 是兼容模式选择，不表示其通过 AppContainer 安全报告。服务以 HTTP 409 返回 `status=blocked`、`code`、兼容的 `error` 及 `missing_requirements`；后端仅识别这一完整契约为类型化能力错误，经过客户端、Agent 和 RoutedTaskExecutor 保持 blocked，不混同普通安装或网络故障。既有会话重新验证失败的 Python/命令及流式入口同样在执行前阻断。

后端 `prepareRuntime` 根据沙箱服务返回的实际 `Capabilities.Images` 决定镜像预检，不采信任务 metadata 中自报的 provider 能力。明确不消费镜像的 native/vm 可创建会话；Docker、remote、未知 provider 仍保留镜像校验，能力查询失败不能绕过授权。native/vm 无法满足显式或冻结的 OCI 镜像要求时阻断，并避免将原生会话 ID 错记为 `runtime_image` 或镜像 digest。受控 HTTP 集成使用真实客户端、Coder 和 RoutedTaskExecutor 覆盖 native 创建、执行和 Create/Exec 能力拒绝；该测试不等于真实 Windows 隔离进程验收。

ResearchCodingAgent 的 benchmark、Paper 调试、AutoResearch 搜索/验证和 experiment 搜索/holdout 循环均保留类型化能力错误，立即停止，不进入代码修复或把它当成候选质量失败。Paper 和 AutoResearch 恢复适用的原始或 best 工作区快照，已有并行 experiment 请求被取消并等待结束。完整性检查与能力拒绝同时发生时通过 `errors.Join` 保留能力类型；专项回归覆盖这些分支，也保留普通代码失败的修复和回归拒绝行为。

每次强隔离执行需扫描解释器与依赖树并运行八项探针，创建 AppContainer 会话还需复制运行时，存在时间与磁盘成本；本轮没有给出性能基准。当前真实 Windows 测试的 `TemporaryDirectory` 权限收紧和 multiprocessing 命名管道仍返回权限不足，完整 `AppContainerOK=true` 尚无验收证明；不通过修改标准库、放宽 ACL 或跳过必需项来制造通过。参见 [原生运行时迁移说明](security-native-runtime-migration-2026-11-16.md)。

### 2.4 SEC-04：精确授权并记录实际镜像

镜像引用使用成熟解析库，规范化 Docker Hub 短名、registry host 与 repository，tag 保持大小写语义。tag-only、digest-only、tag+digest 分别精确授权；相似 tag 和不同 registry 的同名镜像不再获得授权。未设置白名单时只有两个有限默认项，显式空值或旧 namespace 前缀是配置错误。纯 native 不消费未使用的容器配置。

代理仅执行显式可信的 Docker Hub 映射，先授权逻辑来源，再验证目的映射；伪代理和目的路径扩展被拒绝。EnsureImage、创建、复用及 remote/fallback 均在镜像副作用前校验来源，安全错误不得被 fallback 掩盖。

本机镜像准备后记录 immutable config ID，后续创建使用同一 ID 与 `--pull=never`。显式 digest 必须由匹配 repository 的 RepoDigests 证明；tag+digest 还要求独立 tag 指向同一 ID。固定镜像被删除时报 `image_pin_unavailable`，不静默重新解析 tag。缓存仅限服务进程生命周期，跨重启复现应固定 digest。

逻辑引用、实际引用、repo digest、config ID、digest 来源和平台已透传到后端、受信产物、环境 manifest、导出与前端可选字段；计划冻结 digest 不匹配时拒绝。远程实现仍负责其实际内容和平台，本机只保证其请求来源授权。配置示例与回退规则见 [镜像授权迁移说明](security-image-policy-migration-2026-11-16.md)。

真实 Docker Desktop daemon `29.2.1` 上三项集成共 11.093s 通过：使用实际 RepoDigest 创建并执行 CPython；把测试自有 tag 从 Python 改指 Alpine 后仍执行已固定 Python；向测试自有 loopback registry 推送、删除临时本地 tag，随后由产品实际代理拉取并按 digest 执行。容器 inspect 确认实际 `.Image` 与准备后的不可变 ID 一致，平台为 `linux/amd64`。日志见本地 `reports/security-remediation-2026-11-16/docker-real.log`；此结果不代替其他 CPU 架构、全部 registry 或远程执行器验收。

## 3. 27 项验收逐项对照

“自动化通过”仅指本行列出的执行范围；不外推到未运行的系统、交付形态和完整科研流程。`error/inconclusive` 不计安全通过，Go 测试函数 PASS 也不等于其内部报告全部为 pass。

| 用例 | 本轮证据 | 状态与待补证据 |
|---|---|---|
| ENV-01 父环境秘密隔离 | `TestResearchEnvironment*`、真实双 runner 合成秘密夹具 | 已验证；未使用真实 token |
| ENV-02 任务参数回填 | `TestTaskEnvironment*`、`TestNativeEnvironmentRejectedBeforeProcessStarts`、真实 junction 反例 | 自动化通过；启动前拒绝 |
| ENV-03 基础科研流程 | 双 runner 真实 CPython、numpy 2.2.6/matplotlib 3.10.8；临时文件、venv、子进程、中文路径及根目录 PNG | 所测科研流程通过；非全部第三方包兼容承诺 |
| ENV-04 执行入口一致 | raw/stream、备用 exec、`TestRuntimeBuildHook*`、安装环境断言和实际 wheel 安装 | 所测入口通过；构建钩子的宿主 OS 权限边界未扩展 |
| ENV-05 双 runner 验证 | `TestNativeEnvironmentRealLowILAndAppContainer` 及真实 Python/scientific 用例 | 两种实际 runner 严格通过，无容忍 DLL 加载失败分支 |
| ENV-06 控制服务回归 | sandbox 包与入口回归、Bearer 客户端及 gateway 测试 | 部分覆盖；完整桌面重启、审批及凭据轮换运维流程待验 |
| PDF-01 参数覆盖 | 组件捕获转换参数、`pdfEngineCompatibility.test.mjs` | 自动化通过；即使输入 true 仍为 false |
| PDF-02 真实引擎与资源 | 真实 worker 浏览器用例、版本/内容 hash、嵌入 MIME | 已验证所测构建；无旧 worker 或模块错误 |
| PDF-03 阅读回归 | 文本十页、中文扫描两页、缩放、翻页与阅读状态用例 | 部分覆盖；完整字体及更多用户文档视觉矩阵待验 |
| PDF-04 业务集成 | 翻译、选区连续对话、窄窗口浏览器回归及高亮适配测试 | 自动化回归通过；最终产品人工验收待补 |
| PDF-05 三种交付 | Web 构建在阻断外部 HTTP 时真实渲染；282 文件嵌入 hash/MIME；MyGo exe 构建 | 部分通过；整机断网、最终 exe UI 与安装包运行待验 |
| PDF-06 漏洞关闭 | PDF.js `6.5.438`、单一依赖树与专项安全检查、生产审计 | 本轮 CVE 对应旧引擎已排除；其他历史审计项保留 |
| ISO-01 聚合反例 | 空/缺项/重复/旧报告、解析失败和状态一致性测试 | 自动化通过；未知证据不得安全通过 |
| ISO-02 预期方向 | 允许成功、拒绝成功、明确拒绝及超时/网络故障反例 | 自动化通过；失败原因含糊记不确定 |
| ISO-03 真实隔离 | Windows 真进程 token、显式启动路径及监督进程核验 | 有真实身份与启动证据；完整兼容报告仍不确定 |
| ISO-04 文件与网络 | 真实工作区/外部哨兵；同一宿主已连接 IP 的离线/在线 TCP 对照 | 本机实测离线 10013、在线成功；loopback 超时不计通过，更多网络环境待验 |
| ISO-05 清理与恢复 | Job 成员与子进程终止、250ms 静默取消、缓存清理测试 | 已覆盖受控正常/取消路径；全异常恢复矩阵与长时运行待验 |
| ISO-06 决策一致 | ResolveIsolation、Create/Exec 409、六客户端及 Agent/Executor blocked 契约回归 | 自动化通过；未验证完整桌面 native 创建整链 |
| ISO-07 环境一致 | 探针共用实际进程环境策略，SEC-01 真进程环境断言 | 统一策略已验证；不含管理凭据 |
| ISO-08 缓存失效 | 字段边界/字节码/base Python hash、测试中变更、策略变更、60 秒快照和清理回归 | 自动化通过；Create/Exec 不借用永久通过缓存 |
| IMG-01 精确正例 | `TestImagePolicyExactNormalizedReferences` | 自动化通过；Docker Hub 等价名称规范化 |
| IMG-02 相似 tag | 相似后缀拒绝及 Docker 调用次数为零断言 | 自动化通过；拒绝发生在 inspect/pull 前 |
| IMG-03 非可信来源 | 任意 registry 同名镜像及伪代理反例 | 自动化通过 |
| IMG-04 代理映射 | 可信 host/port/prefix 与伪映射反例；测试自有 registry 真实代理拉取/执行 | 所测真实映射通过；不外推其他 registry |
| IMG-05 边界输入 | 未设置/显式空、非法引用、无 tag/digest、大小写和旧前缀 | 自动化通过；配置错误不放行全部镜像 |
| IMG-06 digest 约束 | RepoDigests/tag+digest/pin 删除反例；真实 digest-only 及 tag 漂移后 CPython 执行 | linux/amd64 实测通过；其他架构及 manifest/index 组合待验 |
| IMG-07 全入口一致 | Ensure/Create/reuse/remote/fallback、来源与冻结 digest 回归；真实 Docker 创建/执行 | 本机所测入口通过；远端实际内容固定、旧会话服务端来源反查未验收 |

## 4. 已执行验证与 CI

环境：Windows amd64、Go `1.27.3`、Node `24.14.0`、真实 Chrome、Docker Desktop daemon `29.2.1`；原生集成使用 CPython 3.12 夹具及指定科学包，Linux 回归使用 `golang:1.27.3-bookworm`。没有调用真实模型或付费 API，没有打开恶意 PDF 或读取真实密钥。实际联网限于工具/依赖获取、选定 TCP 对照和测试 registry；未发送应用凭据。

| 范围 / 入口 | 已观察结果 | 限制 |
|---|---|---|
| native 环境与路径专项测试（普通与 `-race`） | 科研双 runner 106.605s、环境/路径/取消 race 27.736s、CPython/硬链接/junction 回归 17.483s 通过 | 科研用例显式启用 `RA_TEST_SCIENTIFIC=1`；常规套件中的 skip 不替代此结果 |
| native ACL/真实隔离专项 | 独立副本、源硬链接并发替换、外部 ACL 不变、真实副本拒写、可信启动/IPv6 合并回归 32.988s 通过 | `TemporaryDirectory` 与进程池在内部报告中仍为 inconclusive，不计八项安全通过 |
| native 网络专项 | 配置公共端点后，同一具体 IP 离线拒绝/在线成功，10.991s 通过 | TCP 建连对照，无应用数据；端点非项目自有，不替代全部网络矩阵 |
| native 完整包：普通与 `-race` | 最终普通 66.961s、race 87.123s、vet 均通过；归档 `native-final.log`，含真实 TCP 对照 | 测试通过验证诚实报告与阻断，不代表本机八项能力全部可用；科研 opt-in 由独立运行证明 |
| sandbox Windows：`go test -v ./... -count=1` | 5 个有测试包全部通过；112 顶层测试通过、5 条件跳过、0失败；native 51.618s | 跳过为 3 Docker、科学绘图及配置外网 TCP 的 opt-in，分别有独立实测证据；跳过不计通过 |
| sandbox Linux：容器内 `go test -v ./... -count=1` 与 `go vet ./...` | 5 个有测试包通过；94 顶层测试通过、3 Docker opt-in 跳过、0失败；vet 通过 | Windows-only 测试不在 Linux 编译执行；真实 Docker 由独立运行证明 |
| sandbox：`go vet ./...`；`GOOS=linux GOARCH=amd64 go build ./...` | 均通过 | Linux 为构建检查，没有执行 Windows 隔离测试 |
| sandbox：engine/remote/main 的 `-race` 与 `go vet` | 3 包通过 | 授权反例使用可计数的 CLI 边界；真实 Docker 另有下行集成 |
| 真实 Docker 镜像专项 | digest 执行、tag 漂移内容固定、loopback 可信 registry 实际拉取/执行，3 条通过（11.093s） | 平台 linux/amd64；不包含实际远程 provider |
| backend：agent/sandbox/scheduler/api 的来源、能力契约与研究循环定点测试，普通及 `-race`；四包 `go vet` | 定点测试与 vet 通过；native 受控 HTTP 创建/执行、provider 镜像能力和研究循环 blocked 回归通过 | Windows 四包完整运行仍有既有 bash/路径、symlink 权限及 Git CRLF 夹具失败；API 完整包通过 |
| backend：Linux `go test -v ./... -count=1 -timeout=180s` | 45 个有测试包通过，947 顶层测试通过、18 条件跳过、0 失败；完整日志已归档 | 跳过项含真实模型、PDF 外部工具和特定外部夹具；backend/test/examples 保持相对布局，使用可写测试副本 |
| frontend：`npm test` | 23 个 Node 测试和 59 文件/513 个组件测试通过 | 此数为本轮最终前端套件记录 |
| frontend：build、lint、bundle、PDF 专项检查、追加契约后的 `tsc -b` | 通过 | 最终发布产物仍需绑定修复 commit |
| Chrome：`pdf-engine-security.spec.ts`、`pdf.spec.ts`、`workbench-pdf-chat.spec.ts` | 合计 10 条通过（最新 11.4s）；两个安全用例的外部 HTTP 请求数为零 | API 使用受控夹具；没有恶意 PDF/真实账户；不声称整机物理断网 |
| desktop：全模块 test/vet、embed 与 MyGo 构建 | 全模块通过；staging 全 dist、真实 go:embed、MIME 检查及真实 exe 构建通过 | exe UI 与安装包未运行；构建成功不等于产品运行验收 |
| `.github/workflows/ci.yml` | js-yaml 按 YAML 1.2 解析，检查 PDF 阻断步骤、浏览器安装及 test/vet 独立步骤 | 本地验证时尚未执行远程 Actions；提交后状态以 PR 检查为准 |

后端定点测试选择：`TestImagePreparePreserves*`、`TestRuntimeManifestPreserves*`、`TestRuntimeImageArtifacts*`、`TestImageApprovalIncludes*`、`TestBuildPlanBundlePreserves*`、`TestCoderCapability*`、`TestCoderExistingSessionCapability*`、`TestSandboxCapability*`、`TestSandboxClientPreservesStructured*`、`TestSandboxExecutionClients*`、`TestSandboxCreateErrors*`、`TestRoutedExecutorPreservesSandbox*`；另保留 `TestPrepareRuntimeRecordsImagePullFailure`，确认普通镜像故障没有被改成能力 blocked。

补充选择：`TestPrepareRuntimeUsesServiceImageCapabilities`、`TestPrepareRuntimeCapabilitiesFailureCannotBypassImagePolicy`、`TestNativeRuntimeCannotSatisfyFrozenImageRequirement`、`TestNativeRuntimeHTTPFlowThroughCoderAndRoutedExecutor` 和 `TestResearchCapability*`。后者覆盖基线/候选、补丁恢复、验证停止、并行取消及同时发生的完整性异常；现有 benchmark/Paper 修复、AutoResearch 保留改进/回退退化和 experiment 正常搜索验证作为对照。CI 在 Linux/Windows 均运行新增能力契约用例。

VM 两项 Windows 夹具问题已修正：能力失败测试注入明确不可用的 detection；fake WSL 改用跨平台可执行 Go 夹具，不在 Windows 直接执行 `.sh`。VM 包全量实测通过（0.798s）；其 `Images=false` 与实际 WSL 行为一致，不宣称能消费 OCI 镜像。

CI 将 sandbox test 与 vet 拆为独立步骤；native 安全回归覆盖 ACL/Python/缓存和 HTTP 阻断。新增 backend 能力契约、研究循环、Linux 真实 Docker digest/tag 漂移，以及真实 Chromium PDF 安全和阅读回归门禁；浏览器失败时上传产物。Docker 两条 CI 同命令已在本机实跑通过（5.265s），registry 集成仍是手动 opt-in。通用依赖审计的历史问题没有被静默忽略或标为已修复。

本轮证据来自工具输出与代码中的可复跑测试，已将部分最终全量及真实 Docker 日志归档至本地 `reports/security-remediation-2026-11-16/`，该目录按仓库约定不提交。早期未落盘的专项结果在证据说明中标注来源，不伪造原始日志；这不是全套独立审计包。发布时仍需将最终修复 commit 和产物按 [reports 约定](../reports/README.md) 绑定。[verification.md](verification.md) 已记录本轮状态。

当前 `frontend/package-lock.json` SHA-256 为 `9bfba7b59f0b1d6773dd8adf5670ec024fe5e30270948c6d534e397d1541066d`；本地模块 worker SHA-256 为 `1e64c8240e54735ec5913b37acd5134747799cb8c6bb3777c70adbc01232d2aa`。它们描述本轮工作树，不是签名发布证明。

四份安全文档及新增索引链接校验通过，27 项验收行完整且唯一，UTF-8 和代码围栏检查通过，`git diff --check` 通过。全量文档扫描另发现 README 中原有本地 reports 链接缺失；这不属于本轮新增链接，也未改写为存在的证据。

本地证据索引为 `reports/security-remediation-2026-11-16/README.md`；`sandbox-suite-summary.json` 与 `backend-linux-full-summary.json` 保留顶层分母、跳过名称及原始日志摘要，`artifact-hashes.json` 保留锁文件/worker/exe 的 SHA-256。上述顶层测试数量包含范围不同的测试函数，不与 27 项发布验收行直接相加或换算通过率。

### 专项复跑入口

通用构建/测试命令见原方案第 8 节；需要真实外部能力的用例必须显式开启：

| 入口 | 条件与含义 |
|---|---|
| `TestNativeScientificPlotWithPrivateEnvironment` | Windows、`RA_TEST_SCIENTIFIC=1`；真实包安装及两种 runner 科研绘图，详见原生迁移说明 |
| `TestProbeWindowsConfiguredNetworkPositiveAndNegativeControl` | Windows、`RA_TEST_PROBE_NETWORK_ADDRESS=host:port`；宿主、离线/在线 profile 对同一可达 IP 的 TCP 对照 |
| `TestImagePolicyRealDocker*` | `RA_TEST_DOCKER_INTEGRATION=1`、真实 daemon 和预装 Python/Alpine；digest 与 tag 漂移后实际执行 |
| `TestImagePolicyRealTrustedRegistryPullAndDigest` | 上述条件加 `RA_TEST_DOCKER_REGISTRY=127.0.0.1:port`；测试自有 registry 的实际代理拉取 |
| 三份 `test/e2e/*pdf*.spec.ts` | 构建后本地 preview 和真实 Chromium；API 使用夹具，安全用例禁止非部署源 HTTP |

科学/网络/Docker 用例默认跳过时不能记为实测通过。新增 CI 的本地验证只证明门禁定义已落地，远程执行结果以关联 PR 的检查为准。

## 5. 发布前仍需完成

1. 在目标 Windows 部署环境复测完整探针矩阵；临时目录权限收紧与 multiprocessing 仍未兼容，必需项不确定时保持 AppContainer 不可验证。公共 TCP 对照已经通过，不能由此忽略其余必需项。
2. 复核桌面到真实 native 进程的创建整链。后端已修正 provider 镜像能力分支，并通过受控 HTTP 的创建、执行和能力拒绝整合测试；这不能代替真实 Windows 进程、桌面 API 与交付产物的联动验收。
3. 完成更多字体/PDF 样本、整机完全断网、本轮桌面 exe UI 与安装包验收。双 runner 的所测科研包和离线绘图已经通过，不再列为未执行。
4. 补充其他 CPU 架构、manifest/index 组合和远程 provider 的内容固定验收；本机 Docker 与可信代理的真实拉取/执行已经通过。核对计划、运行镜像和导出 manifest 一致，进程内 tag 固定不能代替长期 digest 约束。
5. 发布时重建旧研究会话、轮换可能暴露的控制凭据，并执行健康、鉴权、正常审批与重启回归。凭据轮换与历史产物审查本轮没有操作。
6. 为最终修复 commit 和三种交付产物复跑必要门禁，保留失败/不确定的真实分母。任务级短期凭据、管理 API 协议细分及 CSP 仍为独立后续加固。

其他保留边界：不同任务仍共用固定 AppContainer profile SID，没有实现任务之间的工作区隔离；原生安装构建钩子的环境已净化，但本轮没有把其宿主权限改为独立 OS 沙箱。ResearchCodingAgent 主要执行/修复循环的能力拒绝分支已有定点验证，真实科研任务的全流程、更多 provider 与外部依赖组合仍需验收。后端复用既有会话时依赖后续 Exec 的服务端重验，尚无既有会话来源/冻结镜像的独立 inspect 反查契约。

回退时遵循原方案：补充经过审阅的最小环境变量、保留 PDF eval 关闭、保持未知能力阻断、修正精确镜像条目；不恢复整份父环境继承、旧探针假通过或字符串前后缀放行。
