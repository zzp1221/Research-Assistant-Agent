# Windows 原生运行时安全修复与迁移说明

日期：2026-11-16（America/Phoenix）。对应 [修复方案](defect-remediation-2026-11-15-security.md) 的 SEC-01、SEC-03；实际验收状态见 [实施与验证记录](defect-remediation-2026-11-16-security-results.md)。本文不代表已升级用户安装、重建已有任务或轮换真实凭据。

## 1. 升级后的行为

控制服务继续持有其鉴权配置，研究进程和 uv 第三方构建进程从空环境构造白名单。未列入策略的父变量不会传入，因此未知名称的秘密也不会因为未命中黑名单而泄漏。raw、备用 exec、流式执行和安装入口采用相同的凭据隔离规则。

任务不能覆盖服务 token、宿主 `PATH`、`PYTHONHOME`、代理及用户配置目录。Windows 环境名不区分大小写，歧义重复名、NUL 和超长输入在启动前拒绝；错误信息不回显变量值。

| 参数类型 | 现有支持 |
|---|---|
| 随机种子 | `SEED`、`RANDOM_SEED`、`NUMPY_SEED`、`PYTHONHASHSEED`，32 位无符号整数 |
| 线程数 | `OMP_NUM_THREADS`、`OPENBLAS_NUM_THREADS`、`MKL_NUM_THREADS`、`NUMEXPR_NUM_THREADS`、`VECLIB_MAXIMUM_THREADS`、`NUM_WORKERS`，1–4096 |
| 离线与记录 | `HF_HUB_OFFLINE`、`TRANSFORMERS_OFFLINE`、`TOKENIZERS_PARALLELISM`、`WANDB_DISABLED`、`WANDB_MODE`，仅策略列出的取值 |
| 业务参数 | `RA_TASK_` 开头的受限名称；这是普通任务输入，不是服务凭据授权接口 |
| Python 路径 | `PYTHONPATH` 仅接受 `/workspace` 及其子目录，转换后再次检查实际路径；拒绝遍历、外部盘符和越界 junction |
| Python 缓存 | `PYTHONDONTWRITEBYTECODE=0/1`；其余临时、缓存、用户配置路径由可信策略设置 |

研究进程的临时、缓存及配置目录位于工作区的 `.ra-env`。系统目录通过 Windows API 获取；解释器使用已解析绝对路径，不继承任意宿主搜索路径。Windows 对 AppContainer 临时路径的重定向也预先准备在此工作区中。

lowil 创建工作区时只为根目录与已知私有目录设置非递归、非继承的低完整性标签，允许新建任务产物；不会把已有 medium 文件、外部硬链接或 junction 目标一起降级。既有文件因完整性保护而不可写时应产生明确错误，不能通过递归重标宿主对象绕过限制。

uv 安装使用私有构建目录和缓存，禁用用户配置，并显式设置 `UV_LINK_MODE=copy`。不恢复父进程的代理或认证环境来解决安装兼容问题；确需访问私有源的授权机制需另行设计。

## 2. 隔离选择与阻断

`SANDBOX_WINDOWS_RUNNER` 支持 `auto`、`appcontainer`、`lowil`。旧文档中的 `raw` 不是独立的可选安全级别，不能用于绕过探针；未识别值按现有归一化规则走 `auto`。

| 请求 | 判定 |
|---|---|
| `appcontainer` | 只有指定运行时的完整、当前、版本化报告全部通过才执行；否则返回 `sandbox_capability` |
| `auto` | 完整报告通过时使用 AppContainer；兼容性不确定时可选择 lowil；安全项明确失败时阻断 |
| `lowil` | 显式兼容模式；不能声明有 AppContainer 的 OS 断网能力 |
| 任意请求需要 `network_mode=none` | 最终 runner 未验证为 AppContainer 时阻断，不能用逻辑网络开关冒充 OS 隔离 |

强隔离缺失通过 API 返回结构化 `blocked` 和缺失能力信息；后端保留阻断状态。普通进程错误仍按普通失败处理。运行时设置页只认可完整 v2 报告，不把旧报告的布尔值显示为已验证。

网络探针需由运维提供受控 TCP 端点：

```powershell
# 示例域名需替换为自己控制且可稳定连接的端点；配置到 sandbox 服务进程。
$env:SANDBOX_PROBE_NETWORK_ADDRESS = 'probe.example.com:443'
```

此配置默认为空，不会自动访问公共测试网站。宿主先连接该端点，再把已连接的具体 IP:port 传给离线和在线 profile，避免两个 profile 解析到不同地址，也避免把离线 DNS 故障当作 TCP 拒绝。未配置、宿主正向对照不可达、DNS 失败、拒绝连接或超时均不能证明 OS 拒绝网络，结果为 `inconclusive`。默认配置不能据此宣称 AppContainer 全部通过，需要 OS 断网的任务会保持阻断。

端点支持 DNS、IPv4 和 `[IPv6]:port`，必须同时能被宿主和在线 AppContainer profile 访问。在线 profile 只授予 `internetClient`（`S-1-15-3-1`）；loopback 或局域网端点可能仍被系统拒绝。不要修改 loopback exemption 或放宽防火墙来把一个不适用端点强行计为通过。

## 3. 探针信任与结果时效

探针通过真实 AppContainer、生产环境策略和 Job Object 执行，独立验证标准库、进程池、工作区写入、进程身份、越界文件拒绝、离线网络拒绝、在线网络允许和后代进程清理，共八项。

安全探针以 `python -I -S -B` 启动，避免 venv `.pth`、`sitecustomize` 或任务搜索路径改变观测；科学包兼容性需要另行测试。安全通过由可信 Go 监督进程汇总，不接受被测脚本自报安全。仅明确的 Windows 权限拒绝可作为拒绝项通过证据；操作成功、基础设施故障和无法归因的失败分别处理。

报告版本为 `native.isolation-probe/v2`，执行器策略版本为 `native-launch/v3`。环境指纹使用规范编码的元数据、文件路径、长度与内容摘要，覆盖实际 Windows build、架构、解释器、venv、基础 Python、依赖与现有 bytecode，以及 profile 和相关配置。

文件摘要不能涵盖所有宿主防火墙与 ACL 变化，因此创建强隔离会话和每次执行都重新运行探针，不把旧报告作为长期授权。探针前后指纹不同也阻断；运行时缓存清理立即清除报告和 runner 中的旧结论。capabilities 中上一次通过报告仅作为最长 60 秒的诊断快照，不能代替执行前重测。

这一策略会增加扫描运行时和执行八项探针的开销。未来性能优化应保留相同的安全失效语义，不能恢复无限期复用通过结果。

## 4. ACL 与旧缓存迁移

AppContainer 使用按会话生成的独立运行时副本，不再对共享源 CPython/venv 递归授权。源解释器、venv 和基础 Python 必须位于 provider 管理目录；复制过程绑定源 handle、检查最终路径及重解析点，并生成独立文件对象。只有副本获得 AppContainer 的逐对象、非继承读执行权限；服务用户与 SYSTEM 保有管理权限。venv 的基础解释器引用同步改写到副本。

这解决了两个具体问题：NTFS 硬链接共享 ACL，源目录先预检后递归授权仍可能改变外部对象权限；共享 Python 一旦被加入 AppContainer ACE，又可能使 lowil 无法读取 DLL。新实现不改变源对象 DACL，源硬链接也只读取复制，lowil 始终使用未受污染的原运行时。并发源硬链接替换、外部 DACL 不变及真实 AppContainer 拒写副本均有实测。

副本位于运行时缓存旁的 `appcontainer/<session-id>/`，创建失败或回退 lowil 时删除未使用副本；Delete/Reap 清理对应副本。缓存清理包含此目录，存在活动会话时明确拒绝，先结束会话再重试。创建、执行、安装和清理之间有生命周期同步；删除失败保留会话供重试。会话返回的 `VenvPath` 对应实际选定运行时。

不要手动给宿主共享缓存或任意 Python 安装递归开放权限。如果旧测试/旧版本曾修改共享运行时 ACL，先结束相关任务，通过既有运行时缓存清理和安装流程重建服务管理的运行时；不要重置任意宿主安装的 ACL。用户工作区和历史实验产物不随运行时缓存删除。

升级流程：

1. 保存任务与产物，结束旧研究进程，升级 sandbox、backend 和前端。源代码修改不会清除已运行进程中的旧环境。
2. 按既有运维流程轮换可能暴露的控制 token，并重启持有它们的服务；本轮验证只使用合成值，未执行真实凭据轮换。
3. 检查任务环境变量，把普通业务参数迁移到受支持变量；需要宿主任意环境的脚本会收到明确拒绝。
4. 必要时重建受旧 ACL 影响的运行时缓存，预留独立副本所需空间，配置受控网络端点，重新探测并检查实际 runner、各项状态和缺失能力。
5. 仅在符合任务能力要求时恢复执行。若 native 无法满足强隔离或 OS 断网需求，使用已经验证具备这些能力的容器/远端执行配置。

## 5. 当前实测边界

本轮真实 AppContainer 与 lowil Go/CPython 进程均验证了合成秘密不继承、私有临时目录、venv、子进程和中文路径。两种 runner 均实际安装 `numpy==2.2.6`、`matplotlib==3.10.8`，在禁用交互显示的 Agg 后端成功向工作区根目录保存 PNG；科学集成 106.605s 通过。此前 lowil DLL 加载问题已通过消除共享 ACL 污染解决，不再使用容忍该错误的测试分支。

配置公共 TCP 端点 `example.com:443` 的专项实测中，宿主可达，离线 profile 返回 `Winerror 10013`，在线 profile 成功；仅建连/关闭，无应用数据。该端点不是项目自有，也不写为默认配置。受控 loopback 端点的在线对照仍超时，保留不确定。

本机 AppContainer 的 `TemporaryDirectory` 权限收紧操作和 multiprocessing named pipe 仍返回权限不足。这些必需项保留 `inconclusive` 并阻断完整强隔离请求；未通过修改 CPython、放宽 ACL 或跳过必需项来制造通过。八项探针测试函数 PASS 可以表示“正确报告并阻断”，不能据此声称当前机器的八项能力全部可用。

后端已按服务实际 `Capabilities.Images` 处理 native/vm 的无镜像能力，并在研究/修复循环中保持结构化 blocked。受控 HTTP 创建执行测试通过；最终桌面产品到真实进程的全链与其他科学依赖组合仍需部署验收。

现有实现的不同任务共享固定 AppContainer profile SID，不能声称提供任务之间的工作区隔离。这是已有架构边界，本轮凭据隔离与探针修复没有实现每任务 profile；不适用于相互不信任的多租户工作负载。

回退兼容性时应暂停受影响任务、补充经过审阅的最小配置或改用满足能力的执行环境，继续保留凭据白名单和诚实阻断行为。

## 6. 专项复跑

在 `research-assistant/docker-sandbox` 下使用 Windows PowerShell；测试需要受支持的 CPython 3.12/uv，依赖包下载由受限安装环境完成。

```powershell
go test ./internal/engine/native -count=1 -timeout=240s
go test -race ./internal/engine/native -count=1 -timeout=240s
go vet ./internal/engine/native

$env:RA_TEST_SCIENTIFIC = '1'
go test ./internal/engine/native -run '^TestNativeScientificPlotWithPrivateEnvironment$' -count=1 -v -timeout=300s
Remove-Item Env:RA_TEST_SCIENTIFIC

# 替换为部署方选择的可达端点；只有TCP连接，不发送应用数据。
$env:RA_TEST_PROBE_NETWORK_ADDRESS = 'probe.example.com:443'
go test ./internal/engine/native -run '^TestProbeWindowsConfiguredNetworkPositiveAndNegativeControl$' -count=1 -v -timeout=120s
Remove-Item Env:RA_TEST_PROBE_NETWORK_ADDRESS
```

opt-in 条件未开启时的 skip 不是科研/网络验收通过。最新可用证据和未完成项目以实施记录为准。
