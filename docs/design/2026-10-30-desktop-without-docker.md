# 桌面端打包与「去 Docker」方案

- 状态：设计稿（待评审）
- 日期：2026-10-30
- 范围：打包形态、沙箱执行层、Windows 可移植性
- 关联：[README](../../README.md)、[docker-sandbox](../../research-assistant/docker-sandbox/README.md)、[backend](../../research-assistant/backend/README.md)
- 实施：[Windows 桌面客户端实施方案](2026-10-30-windows-desktop-client.md)
- 一句话：Docker 在当前系统里只被封装在 `engine.SandboxEngine` 接口之后，后端只通过 HTTP 调用它；把它降级为「可选后端」、再补一个自带运行时，是可行且不需要重写架构的。

---

## 1. 结论

1. **能。** 后端与沙箱之间已经是 HTTP 契约，沙箱内部已经有引擎接口和两个实现（Docker / OpenSandbox）。所以「不强制 Docker」= 新增引擎实现 + 打包外壳，不是重构。
2. **但「不强制 Docker」不等于「直接跑在宿主机上」。** 这个项目执行的是 LLM 生成的代码，隔离层不能一起删掉，必须换一个替代品。
3. **推荐形态**：桌面外壳（Wails，Go）+ 本地后端进程 + 可插拔 Sandbox Provider（docker / vm / native / remote）+ 自动探测 + 诚实降级。
4. **有三个隐藏阻塞**必须先解决，否则桌面版在 Windows 上根本跑不起来：Linux-only syscall、POSIX 命令契约、单文件模式强依赖 `SANDBOX_URL`。

---

## 2. 现状：Docker 到底承担了什么

沙箱不是「为了用 Docker 而用 Docker」，它在替产品挡这些事：

| 能力 | 现在的实现 | 脱离 Docker 是否可行 |
|---|---|---|
| 文件系统隔离 | 容器 rootfs，只 bind mount 一个 `/workspace` | 可行。OS 沙箱 / 受限 token + ACL / 只读绑定系统盘 |
| 进程隔离与回收 | PID namespace，`--rm` | 可行。Job Object（Win）/ PID ns（Linux）/ 进程组 |
| 网络默认断网、审批后放行 | `--network none`（internal bridge）↔ `bridge` | 可行但最难。AppContainer 能力 / netns / sandbox-exec 各自实现 |
| 资源上限 | `--cpus --memory --pids-limit` | 可行。Job Object / cgroup v2 / rlimit |
| 可复现环境 | 任意镜像 + digest + pip/apt | **部分可行**。venv 覆盖 pip，覆盖不了 apt/系统库 |
| 镜像语义 | `EnsureImage` 返回 digest，写进 `EnvironmentManifest` | **只能 VM 或远程**；native 只能记 venv lock hash |
| GPU | `--gpus all` + nvidia runtime | 可行。宿主 CUDA 直接用，Windows 上反而比 Docker 更简单 |
| 清理 | 删容器 | 可行。删工作区 + 杀进程树 |

代码证据：

- 执行边界：`research-assistant/docker-sandbox/internal/engine/interface.go:26` 定义了 `SandboxEngine`（Create / Delete / ExecutePython / ExecuteCommand / GetType）。
- 引擎选择：`research-assistant/docker-sandbox/main.go:239`，先试 Native Docker，失败才在显式开关下 fallback 到 OpenSandbox。
- 后端只认 HTTP：`research-assistant/backend/internal/sandbox/opensandbox.go:19` 的 `SandboxClient`，方法只有 Create / Exec / EnsureImage / SetNetwork / Usage / Capabilities。
- 安全参数集中在 `docker_security.go:36`（cap-drop ALL、no-new-privileges、pids、内存、CPU、workdir、只读 rootfs）。
- 环境契约：`research-assistant/backend/internal/models/reproduction.go:131` 的 `EnvironmentManifest`。

**关键判断：真正「必须 Docker」的只有两项**——任意 OCI 镜像的复现环境、以及成熟的网络/资源/清理组合。其余能力都能在宿主层重建。

---

## 3. 目标架构

```text
Desktop Shell (Wails / Tauri)
  └─ Go backend (HTTP + SSE, 仅绑 127.0.0.1, 随机端口 + 启动 token)
       ├─ 内嵌前端 (backend/webui/dist, 已有)
       └─ Sandbox Provider (运行时探测 + 用户可切换)
            ├─ docker   检测到 docker 就用，行为与今天完全一致（最高保真）
            ├─ vm       自带引擎：WSL2 / Apple container / Lima + docker engine
            ├─ native   OS 原生沙箱 + uv 管理的 CPython + 每计划 venv
            └─ remote   OpenSandbox / E2B / Modal（已有客户端雏形）
```

四个后端共用同一个 `Capabilities` 协商结果，UI 显示当前档位与环境保真度；能力不足时返回 `blocked`，不允许静默降级。

---

## 4. 四种后端逐项分析

### 4.1 docker —— 复用现有（默认降级为「检测到就升级」）

- 触发：`docker version` 可用。
- 优点：零新增代码，镜像/GPU/网络/清理全部保留。
- 缺点：仍依赖 Docker Desktop（安装、磁盘、企业许可证）。
- 定位：开发者与已有 Docker 用户的最优档位；桌面版把它当「增强模式」。

### 4.2 vm —— 自带引擎，不要求用户安装 Docker

这个是「改动最小、保真度最高」的路线：保留完整 OCI 语义，只把引擎从用户机器搬进一个自己管的 VM。

| 平台 | 方案 | 说明 |
|---|---|---|
| Windows | WSL2 + 预置发行版（`wsl --import` tar）+ docker engine / containerd | 宿主只需要 `DOCKER_HOST` 或 CLI 包装；docker engine 是 Apache-2.0，不涉及 Desktop 许可证 |
| macOS | Apple `container` 1.0（macOS 26 / Apple silicon，per-container microVM，OCI）或 Lima + docker engine | 不需要 Docker Desktop，也不需要许可证 |
| Linux | 无需 VM，rootless podman / docker engine | 与原行为最接近 |

- 优点：后端与 Agent 的 POSIX 假设全部成立，命令契约不用改。
- 缺点：包体 500MB–1.5GB；Windows 首次启用 WSL/VMP 通常要管理员、可能要重启；macOS 需要签名与公证；VM 生命周期（启动、健康检查、升级、清理）要自己写。
- 坑：Windows 宿主路径（`D:\...`）必须翻译成 `/mnt/d/...` 才能 bind mount，沙箱引擎的 `mount_path` 处理要加一层映射。

### 4.3 native —— OS 原生沙箱，零安装

| 平台 | 机制 | 能做到 | 主要坑 |
|---|---|---|---|
| Windows | AppContainer + Job Object | 默认无网络、只能读写自己 profile 与显式授权的工作区、进程树随 Job 回收 | named object 隔离会破坏 `multiprocessing` 共享内存，torch DataLoader 可能直接挂；部分包依赖 HKCU/命名管道。备选：受限 token + Job + ACL（兼容更好、隔离更弱） |
| Linux | bubblewrap（静态二进制约 1MB）+ cgroup v2 / rlimit | `--ro-bind / /` + `--tmpfs /tmp` + `--bind workspace` + `--unshare-net` + `--die-with-parent`，接近容器 | 需要内核允许 user namespace；网络放行要 pasta/slirp4netns |
| macOS | `sandbox-exec` + SBPL profile | deny network、只允许写工作区 | Apple 已标记弃用，长期有风险；App Store 分发不可能 |

- 环境来源：不走镜像，用 uv 管理的可重定位 CPython + 每计划 venv；`EnvironmentManifest` 记录 Python 版本、uv.lock hash、pip freeze。
- 优点：0 安装、包体小（约 50MB + 运行时按需下载）、启动快、宿主 GPU 直接可用。
- 缺点：隔离强度比容器低一档；apt 类系统依赖无法覆盖；网络开关在 Windows 上要靠两套 AppContainer profile（离线/在线各一个，共享同一工作区 ACL）。

### 4.4 remote —— 已有雏形

`OpenSandboxEngine`（`main.go:61`）与后端客户端已经在仓库里；可以直接扩展 E2B / Modal / Daytona。

- 适合 GPU、大内存、长时间任务；把「本地跑不动」变成「换一个执行面」。
- 需要网络与账号，涉及用户凭据托管，要单独做权限与配额设计。

---

## 5. 桌面外壳选型

| 方案 | 体积 | 语言 | 系统集成 | 适配本项目 |
|---|---|---|---|---|
| Wails | 单 exe 约 20–35MB（Win 需 WebView2，Win11 自带） | Go | 托盘、原生对话框、文件关联、单实例 | **最顺**：后端本来就是 Go，可以直接 in-process 或 supervisor |
| Tauri 2 | 约 10–20MB | Rust + 外部二进制 | 安装包/更新生态最好 | 好，但 Go 后端要作为 sidecar，IPC 多一层 |
| Electron | 约 120MB+ | Node | 最成熟 | 能用，但要把后端当子进程，打包与内存都不划算 |

工程要点：

- **同源**：生产态前端已经用 `window.location.origin`（`frontend/src/config/env.ts:16`），由后端托管即可，前端几乎不用改。
- **端口与鉴权**：随机端口 + 启动握手 token，只绑 `127.0.0.1`，不再需要 nginx 注入 `API_AUTH_TOKEN`。
- **数据目录**：`%LOCALAPPDATA%\ResearchAssistant` / `~/Library/Application Support/...` / `~/.local/share/...`，替换 `resolveProjectRoot()` 这种按仓库布局猜路径的逻辑。
- **长任务**：托盘常驻、关闭窗口不杀进程、Job Object 保证退出时不留孤儿；SSE 在 WebView 里可用。
- **分发**：安装包（NSIS/MSIX、dmg）、自动更新、Windows 代码签名（SmartScreen）、macOS notarization。

---

## 6. 三个必须先拆的耦合

这三个是桌面化真正的成本所在，比「选哪个沙箱」更关键。

### 6.1 Linux-only syscall

| 位置 | 问题 | 处理 |
|---|---|---|
| `backend/internal/scheduler/repo_prepare.go:653,777` | `syscall.SysProcAttr{Setpgid:true}` + `syscall.Kill(-pid, SIGKILL)` | 抽 `procgroup` 包：Linux 用进程组，Windows 用 Job Object（`KILL_ON_JOB_CLOSE`） |
| `backend/internal/scheduler/reproduction_mode.go:174` | `syscall.Statfs` | build tag 拆 `diskfree_unix.go` / `diskfree_windows.go`（`GetDiskFreeSpaceEx`） |

编译级阻塞只有这 3 处；其余是路径分隔符、大小写、长路径等隐性问题，需要真机验证。

### 6.2 POSIX 命令契约

后端发给沙箱的命令是写死的 POSIX：

- 6 处 `bash -lc`（如 `agent/benchmark_harness.go:350`、`agent/coder_runtime.go:128`）
- 7 处 `cd /workspace`（如 `agent/benchmark_harness.go:422`）
- 18 处 exec 调用，命令里直接写 `python3`、`/workspace`

两条路：

1. **抽象成 `Command{Argv, Cwd, Env}`**，由引擎负责翻译与 shell 包装。正确、长期，但要改 6+7 处并补测试。
2. **VM 路线绕开**：命令始终在 Linux 里跑（WSL2 / Apple container），后端零改动。这是选 vm 后端的一个重要理由。

如果走 Windows 原生沙箱，必须选 1，或者退而求其次内置 busybox-w32 / git-bash 做兼容层（不推荐，坑多）。

### 6.3 单文件模式强依赖外部沙箱

- `backend/cmd/app/main.go:24`：`SANDBOX_URL` 为空直接 `log.Fatal`。
- 嵌入式 `internal/sandboxserver` 在 `c87d2e4`（安全整改：拆 gateway/executor）里被移除，`backend/README.md:114` 有明确记录。

桌面版**不建议**退回进程内：沙箱应当作为子进程由 supervisor 拉起，保持「后端不持有特权」的边界。只是把「用户手动起服务」换成「App 自动起、自动健康检查、退出时回收」。

---

## 7. 保真度与诚实降级

这个项目已经有 `PlanOutcome`（completed / partial / smoke / mock / blocked / needs_review）和 `MissingRequirement` 的严谨语义，桌面降级必须接上它们，不能破坏「不用 mock 冒充成功」的原则。

1. `EnvironmentManifest` 增加字段：`sandbox_provider`（docker/vm/native/remote）、`isolation`（container/vm/process）、`runtime_hash`。native 模式下 `Image` / `ImageDigest` 允许为空。
2. `MissingRequirement.Kind` 增加 `sandbox_capability`：任务需要镜像/GPU 而当前 provider 不提供时，返回 `blocked` 并给出 remediation（「安装内置运行时」/「改用远程沙箱」），而不是降级后算完成。
3. UI 增加环境保真度徽章：container / vm / venv / remote。用户在计划详情里能一眼看出这次复现是在哪跑的。
4. 导出包（`plan_export.go`）按 provider 生成不同的复现说明：容器模式给 Dockerfile，native 模式给 `uv.lock` + 运行命令。

---

## 8. 安全模型变化

从「多租户不可信代码」变成「单用户 + LLM 生成代码」，威胁模型变了，但底线不能丢：

- 只读绑定系统盘，只写工作区；
- 默认断网，联网走已有的 RuntimeAction 审批；
- 进程树必须可回收（Job Object / `--die-with-parent`）；
- native 模式下 `full_access` 应降级为 `confirm_important`，因为隔离不足；
- 工作区目录 ACL 只授给沙箱用户 SID，不授给普通用户会话；
- 安装包签名 + 运行时下载校验（hash/签名），避免成为供应链入口。

---

## 9. 分阶段实施

| 阶段 | 内容 | 产出 | 估算 |
|---|---|---|---|
| **P0** | Wails 外壳 + Windows 编译 + 打包脚本；仍要求 Docker | 双击可用的 `.exe`，能起后端、开前端、连已有 compose 沙箱 | 1–2 周 |
| **P1** | Provider 抽象 + docker 检测 + native 骨架（uv + venv + 进程） | Docker 变「可选」，无 Docker 也能跑纯 Python 任务 | 1 周 |
| **P2** | vm 后端（Windows 优先 WSL2；macOS Apple container / Lima） | 无 Docker Desktop 也能跑 OCI 镜像 | 1–2 周 |
| **P3** | native 强化（Linux bwrap / macOS sandbox-exec / Windows AppContainer）+ 命令契约改造 | 零安装默认路径 | 3–5 周 |
| **P4** | remote provider + GPU + 安装包/自动更新/签名 | 完整分发型产品 | 1–2 周 |

- **MVP（Windows + WSL2 路线，最快见到「不需要用户装 Docker」）≈ 4–6 周。**
- **纯原生全平台（零外部依赖）≈ 8–12 周**，主要成本在 Windows AppContainer 与命令契约。

---

## 10. 风险清单

| 风险 | 影响 | 对策 |
|---|---|---|
| 杀软误报（自解压、运行时下载、Job/Object API） | 安装即被拦 | 签名、白名单说明、避免释放可执行文件到临时目录 |
| AppContainer 破坏 multiprocessing / 共享内存 | ML 任务失败 | 先做 POC；失败则 Windows 默认走 WSL2，native 仅用于轻量任务 |
| macOS `sandbox-exec` 弃用 | 未来失效 | 抽象成 provider，失效时切 Apple container |
| WSL2 首次启用需管理员 | 「零权限」承诺不成立 | UI 明确提示；提供 remote 作为免权限兜底 |
| 包体与镜像下载 | 首启体验差 | 运行时按需下载 + 断点续传 + 国内镜像源 |
| Docker Desktop 许可证 | 企业分发受阻 | 自带 Docker Engine（Apache-2.0）或改用 podman/containerd |
| 路径翻译（`D:\` ↔ `/mnt/d/`） | bind mount 失败 | vm 引擎统一在工作区内做路径映射，并加集成测试 |

---

## 11. 验收标准

- **无 Docker 机器**：安装 → 新建计划 → 执行 Python → 产出 artifact；`/api/health` 显示 `sandbox.provider=native`（或 `vm`）。
- **有 Docker 机器**：自动选中 docker，行为与今天完全一致（回归现有 E2E）。
- **默认断网**：native 与 docker 都不能访问公网；审批后临时放行，结束立即恢复。
- **退出即清理**：关闭 App 后没有残留进程、容器、监听端口。
- **能力不足**：返回 `blocked` + `missing_requirements=[sandbox_capability]`，UI 给出替代方案。
- **可审计**：同一计划在两种 provider 下的 `EnvironmentManifest` 字段可区分，导出包不混淆。

---

## 12. 待决问题

1. 首发平台？如果是 Windows 优先，**WSL2（保真）还是 native（零安装）** 的选择决定后续 2–3 周的工作量。
2. 能否接受「首次安装需要管理员」（启用 WSL / 安装 VM）？如果不能，Windows 只能走 native 或 remote。
3. 是否需要离线安装包（内网/科研机构）？这会影响是否把运行时打进安装包。
4. 是否引入云沙箱与账号体系？涉及凭据托管、计费与合规。
5. 企业分发是否必须绕开 Docker Desktop 许可证？如果是，P1 就要优先做自带引擎，而不是 native。

---

## 13. 附录 A：论文复现在无 Docker 下的能力边界

结论先说：**这套产品的「论文复现」在无 Docker 时仍然成立**，因为它的智能链路（找仓库、读论文、冻结主张、生成 RuntimeSpec、装依赖、执行、debug、比主张、出证据图）完全不依赖容器。Docker 只在最后一步提供一个干净的 Python 环境。

### 13.1 代码事实：当前复现其实只用到镜像的三件事

| 事实 | 位置 | 含义 |
|---|---|---|
| 运行时规格里 `SystemPackages` 恒为空 | `agent/reproduction_spec.go:77` | 产品**本来就不装 apt 包** |
| 选环境只看 `pyproject.toml` 的 `requires-python` | `agent/coder_image.go:27-41` | 只是「在 3.9/3.10/3.11/3.12 里挑一个」 |
| 「升级 Python」= 换镜像 | `agent/coder_python.go:26-39` | 换成「切 uv 管理的 CPython 版本」更快 |
| `EnvironmentManifest` 由 `python --version` + `pip freeze` 生成 | `agent/coder_runtime.go:128-153` | 镜像只是清单里的一个字段，不是环境本身 |
| 默认镜像 `python:3.9/3.11-bullseye` | `agent/coder.go:22` | 本质就是「带 Python 和编译工具链的 Debian」 |
| 旗舰 smoke 用 torch CPU 跑注意力消融 | `catalog/templates/attention_ablation.py` | 依赖全是 pip wheel |

也就是说，Docker 在这里等价于「一个带 Python 和 gcc 的 Debian」。用 **uv 管理的 CPython + 每计划 venv** 可以替代它。

### 13.2 能覆盖的场景（估计占学术仓库的 70–85%）

- `requirements.txt` / `pyproject.toml` → `pip install`（绝大多数包有 wheel）
- 单入口脚本 + 仓库相对数据路径
- CPU smoke、消融、指标对比（本产品的主场景）
- Python 版本切换（3.9–3.13），比拉镜像快一个数量级
- GPU：直接用宿主 CUDA，Windows 上反而比 Docker 透传更简单
- 环境可审计：`uv.lock` + `pip freeze` + Python 版本 hash 代替 image digest；可复现性不一定更差，但**语义必须标注清楚**

### 13.3 覆盖不了、必须显式降级的场景

| 场景 | 为什么不行 | 处理 |
|---|---|---|
| 系统级依赖（`libgl1`、`ffmpeg`、`graphviz`、特定 glibc） | venv 不提供 OS 包 | 返回 `blocked` + `missing_requirements=[sandbox_capability]`，引导装 VM 运行时或走远程 |
| 只有 sdist 的包 | Windows venv 默认无 MSVC，Linux 无 build-essential | 优先预编译 wheel；失败则提示安装构建工具或切容器 |
| 镜像 digest 级精确复现 | 没有镜像 | 用 `runtime_hash`（Python 版本 + lock hash）代替，UI 标注「venv 保真度」 |
| CUDA / cuDNN 精确对齐 | venv 用宿主 CUDA，不是镜像自带 | 需要精确对齐时强制容器/VM |
| 非 Python 环境（R / Java / Node） | 现在也不支持 | 不算退化 |
| 全量复现（16 核/64GB/GPU） | 笔记本本来跑不动 | 保持 `smoke` / `blocked`，与今天行为一致 |

### 13.4 产品语义要求

1. `EnvironmentManifest` 在 native 模式：`Image`/`ImageDigest` 为空，新增 `sandbox_provider=native`、`isolation=process`、`runtime_hash`。
2. 任务需要镜像能力（仓库带 Dockerfile、依赖 apt、需要 CUDA 对齐）时，**不允许静默降级**，返回 `blocked` 并给出替代方案。
3. `PlanOutcome` 沿用现有语义：跑通且有证据 → `completed`；只覆盖最小路径 → `smoke`；环境降级但指标有效 → `completed` + UI 显示保真度徽章。
4. 导出包按 provider 生成不同复现说明：容器模式给 Dockerfile，native 模式给 `uv.lock` + 启动命令。

### 13.5 分档策略

```text
仓库扫描
  ├─ 有 Dockerfile / apt-get / CUDA 需求 → 提示「需要容器级环境」（vm / remote）
  ├─ 只有 requirements.txt / pyproject → native venv 直接跑
  └─ 跑失败且错误指向系统库/编译 → 升级到 vm 或 remote，而不是反复重试
```

一句话：**「读论文 + 跑 smoke + 做消融」这类复现，无 Docker 完全成立；「论文级完整复现 + 精确环境对齐」不能砍掉容器这条腿，只能把它换成自带 VM。**

