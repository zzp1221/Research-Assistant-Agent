# Windows 桌面客户端实施方案

- 状态：设计稿（方向已确认：Windows 首发；接受 native 作为默认沙箱档位）
- 日期：2026-10-30
- 范围：桌面外壳、进程模型、沙箱 Provider、后端 Windows 可移植性、打包发布
- 关联：[总方案](2026-10-30-desktop-without-docker.md)、[backend](../../research-assistant/backend/README.md)、[docker-sandbox](../../research-assistant/docker-sandbox/README.md)、[frontend](../../research-assistant/frontend/README.md)
- 一句话：用 Wails 把项目包成「本地运行时」——外壳只负责窗口、托盘和监督进程；后端与沙箱是可独立重启的子进程；沙箱新增 native Provider（uv 管理的 CPython + venv + Windows Job Object / AppContainer），Docker 降级为「检测到就用」的增强档位。

---

## 1. 目标与非目标

### 1.1 目标

| 编号 | 目标 | 验收方式 |
|---|---|---|
| G1 | 用户下载安装包，双击即用，**不要求预装 Docker / Python / Go** | 干净 Windows 11 虚拟机走通首次启动向导 |
| G2 | 没有 Docker 时，论文复现 / AutoResearch / Benchmark 的 Python 路径全部可用 | 无 Docker 机器跑通 annotated-transformer torch smoke |
| G3 | 有 Docker 时自动升级到容器档位，行为与现网一致 | 现有 E2E 套件在 docker provider 下全绿 |
| G4 | 关闭窗口不中断长任务；退出后无残留进程 | 进程树检查 + Job Object 回收测试 |
| G5 | 环境能力不足时**显式失败**，不用 mock 冒充成功 | `PlanOutcome` + `missing_requirements` 断言 |
| G6 | 可自动更新、可诊断、可卸载 | 升级测试 + 诊断包导出 |

### 1.2 非目标（本期不做）

- macOS / Linux 客户端（保留 Provider 接口的跨平台设计，但只在 Windows 上验收）
- 多用户 / 多租户桌面
- 通过 Microsoft Store 分发
- 容器内 GPU 直通（Windows 上 GPU 走宿主 CUDA，不走容器）
- 替换现有 Compose 部署（两者共存，见 4.4）

---

## 2. 已确认的产品决策

| 编号 | 决策 | 备选与否决理由 |
|---|---|---|
| D1 | 外壳用 **Wails**（Go） | Tauri 打包/更新更好但要引入 Rust；Electron 120MB 起、内存高 |
| D2 | 后端作为**独立子进程**复用 `cmd/app` | 进程内嵌会让后端 panic 直接打掉 UI；子进程可重启、可单独升级 |
| D3 | 沙箱作为**独立子进程** | 保持特权边界（`c87d2e4` 拆 gateway/executor 的方向是对的，不回退） |
| D4 | **native 默认**，Docker 检测到自动升级，VM 按需安装 | 用户要的是「零前置」，WSL2 仍然要管理员 + 1.5GB |
| D5 | 命令契约改为 **argv + cwd + env**，不再传 POSIX shell 字符串 | 否则 Windows 原生引擎无法执行；macOS/Linux 同样受益 |
| D6 | Python 运行时用 **uv** 管理（python-build-standalone） | 单二进制、按需下载、版本齐全、比打包解释器灵活 |
| D7 | 安装到 **per-user** 目录，不要求管理员 | 自动更新不需要 UAC；与「零前置」承诺一致 |

---

## 3. 总体架构

### 3.1 进程模型

```text
ResearchAssistant.exe   (Wails 外壳, Go)
├─ WebView2 窗口            ← React 构建产物（webui/dist）
├─ Supervisor
│   ├─ 单实例锁 / 端口分配 / 会话 token / 健康检查 / 重启
│   ├─ Tray（托盘菜单、通知、关闭到托盘）
│   ├─ Settings（settings.json + DPAPI 密钥）
│   └─ loopback 反向代理（固定 origin，后端端口不外露）
├─ backend.exe            (Go, 127.0.0.1:<backend-port>)
├─ sandbox.exe            (Go, 127.0.0.1:<sandbox-port>)
│   └─ Provider 路由
│       ├─ native  → uv / CPython / venv + Windows Runner
│       ├─ docker  → 现有 docker CLI 引擎
│       └─ vm/remote → 预留（P4）
└─ task processes         (用户代码，受 Job Object / AppContainer 约束)
```

为什么是三进程而不是一进程：

| 理由 | 说明 |
|---|---|
| 崩溃隔离 | 后端 panic 不能带走 UI；沙箱卡死可以单独杀掉重启 |
| 特权边界 | 沙箱进程可以按需以受限身份运行，后端保持普通用户 |
| 升级粒度 | 后端/沙箱可以独立热更新，外壳不动 |
| 资源回收 | 外壳给两个子进程各建一个 Job Object，外壳退出即可整体回收 |

### 3.2 启动时序

```text
1. 单实例锁（Named Mutex "Global\ResearchAssistant.Desktop"）
   └─ 已存在 → 唤起已有窗口（IPC / 窗口消息），退出
2. 加载 settings.json，DPAPI 解密 API Key（失败则进入首次启动向导）
3. 选择端口（优先 OS 分配，见 3.4）
4. 生成 session token（32 字节随机）
5. 启动 backend.exe，注入环境变量（见 5.3）
   └─ 轮询 GET /api/health，指数退避，超时 30s → 弹「后端启动失败」+ 日志路径
6. 启动 sandbox.exe，注入环境变量
   └─ 轮询 GET /api/v1/health，超时 20s
   └─ 根据健康结果确定 provider：docker 可用 → docker；否则 native
7. 创建窗口 → 加载 http://127.0.0.1:<shell-port>/
8. 后台任务：运行时探测、更新检查、孤儿沙箱回收
9. 首启且无运行时 → 显示「正在准备 Python 运行时」进度
```

关键点：**第 5、6 步失败不能白屏**。外壳必须有独立的错误页（显示日志路径、诊断包按钮、重试按钮）。

### 3.3 退出与异常回收

| 场景 | 行为 |
|---|---|
| 点关闭按钮 | 默认最小化到托盘（可在设置里改为退出）；正在跑计划时提示「任务在后台继续」 |
| 托盘「退出」 | 取消进行中计划 → 停 sandbox（等待任务回收，超时 10s 强杀）→ 停 backend → 释放锁 |
| 外壳崩溃 | Job Object `KILL_ON_JOB_CLOSE` 自动杀掉 backend / sandbox / 所有任务进程 |
| backend 崩溃 | Supervisor 指数退避重启（最多 3 次），UI 显示「后端已重启，进行中的计划已中断」 |
| sandbox 崩溃 | 同上；重启后对残留容器/工作区做一次清理 |
| 系统休眠/唤醒 | 监听电源事件，唤醒后重建健康检查，标记超时的任务 |

### 3.4 端口与鉴权

- **端口**：外壳先 `net.Listen("tcp", "127.0.0.1:0")` 探测一个空闲端口，立刻关闭并传给子进程，子进程用 `APP_ADDR` / `SANDBOX_LISTEN_ADDR` 绑定。竞态窗口极小，失败时重试 3 次。
- **绑定**：两个服务都只绑 `127.0.0.1`，禁止 `0.0.0.0`。
- **鉴权（三层）**：
  1. `SANDBOX_API_TOKEN` 与 `API_AUTH_TOKEN` 每次启动随机生成，只存在于进程环境变量；
  2. WebView 通过固定 origin 访问外壳的反向代理，代理注入 `Authorization`，浏览器永远拿不到 token；
  3. 后端额外增加 **loopback 守卫中间件**：校验 `Host` 必须是 `127.0.0.1`，对非 GET 请求校验 `Origin`，拒绝跨站请求（防本地 CSRF）。
- **SSE**：反向代理必须设置 `FlushInterval: -1`，并关闭响应缓冲；验收里要专门测 40 分钟长连接不断。

### 3.5 WebView 加载方式

两种实现，验收标准一致，按所用 Wails 版本的能力二选一：

| 方式 | 做法 | 优点 | 风险 |
|---|---|---|---|
| **A. 壳内反向代理（推荐）** | 外壳把 Wails 的 AssetServer 指向自己的 `ReverseProxy`，WebView 永远访问固定 origin | 无 CORS、端口不外露、前端零改动 | SSE 必须 `FlushInterval: -1`；32MB 上传与大文件 Range 要单独验 |
| **B. 直接加载 loopback** | 窗口 URL 指向 `http://127.0.0.1:<shell-port>/` | 实现最简单 | 依赖框架支持外部 URL；端口可被本机其他进程探测 |

两种方式都要求：外壳自己 serve 内嵌 `dist`，`/api/*` 转发到 backend，静态资源命中失败回退 `index.html`（与现有 `cmd/app` 的 SPA 回退逻辑一致，可直接复用）。

---

## 4. 目录布局

### 4.1 安装目录（per-user，无需管理员）

```text
%LOCALAPPDATA%\Programs\ResearchAssistant\
├─ ResearchAssistant.exe      外壳
├─ backend.exe                后端
├─ sandbox.exe                沙箱
├─ uv.exe                     Python 运行时管理器
└─ webview\                   前端静态资源（也可嵌入 backend）
```

### 4.2 数据目录

```text
%LOCALAPPDATA%\ResearchAssistant\
├─ settings.json
├─ secrets.dat                 DPAPI 加密的 API Key
├─ lock                       单实例锁
├─ data\
│   ├─ plans\                  PLAN_STORE_PATH
│   ├─ uploads\                UPLOAD_ROOT
│   ├─ blobs\                  BLOB_STORE_PATH
│   ├─ literature\             LITERATURE_LIBRARY_DIR
│   └─ logs\                   轮转日志（backend / sandbox / shell）
├─ runtimes\
│   ├─ python\3.9\ 3.11\ 3.12\   uv 管理的 CPython
│   ├─ cache\                   uv / pip wheel 缓存
│   └─ venvs\<env-hash>\         可复用的虚拟环境
└─ workspaces\<plan-id>\         每个计划的隔离工作区
```

设计要点：

- **venv 按环境哈希缓存**，哈希 = `sha256(python 版本 + 排序后的依赖声明 + 平台)`，跨计划复用，避免每次复现都重装 torch。
- **工作区按 plan-id 隔离**，计划删除时整目录回收；导出包不包含 venv。
- **日志轮转**：单文件 20MB，保留 5 份；现在用的是 `O_TRUNC`，桌面必须改。

### 4.3 代码目录（仓库内新增）

```text
desktop\                       新增，独立 Go module
├─ main.go                      Wails 入口
├─ supervisor\                 进程管理、健康检查、重启、Job Object
├─ tray\                       托盘与通知
├─ settings\                   settings.json + DPAPI
├─ proxy\                      loopback 反向代理（SSE 透传）
└─ build\                      NSIS 脚本、签名、图标

research-assistant\backend\
├─ internal\providers\         新增：Provider 接口与能力协商
├─ internal\procgroup\         新增：进程组/Job Object 抽象
├─ cmd\app\                    改为由外壳注入全部路径配置
└─ internal\sandbox\           client 增加 provider 字段

research-assistant\docker-sandbox\
├─ internal\engine\native\    新增：Windows 原生引擎
├─ internal\engine\native\runner\  exec / appcontainer / lowil
└─ internal\engine\command.go  新增：Command 契约与 /workspace 翻译
```

### 4.4 与现有 Compose 的关系

- **不删除** Compose：服务器部署、CI、开发态继续用。
- 桌面版是**第三个入口**（`cmd/api`、`cmd/app`、`desktop`），三者共用 `internal/api` 与 `internal/agents`。
- `docker-sandbox` 的 REST 契约保持不变，桌面只是换一个 provider 实现。

---

## 5. 配置与密钥

### 5.1 settings.json

```json
{
  "version": 1,
  "llm": {
    "base_url": "https://api.deepseek.com/v1",
    "model": "deepseek-chat",
    "api_key_ref": "dpapi:default",
    "timeout_seconds": 120
  },
  "sandbox": {
    "provider": "auto",
    "network_default": "off",
    "cpu_limit": 2,
    "memory_gb": 4,
    "disk_gb": 20,
    "windows_runner": "auto"
  },
  "ui": { "close_to_tray": true, "theme": "system" },
  "update": { "channel": "stable", "auto": true }
}
```

- `provider`: `auto` / `native` / `docker` / `vm` / `remote`。`auto` = 有 Docker 用 Docker，否则 native。
- `windows_runner`: `auto` / `appcontainer` / `lowil`（见 6.4）。
- 明文 API Key **绝不落盘**。

### 5.2 密钥存储

- Windows：`CryptProtectData`（DPAPI，CurrentUser scope），密文存 `secrets.dat`。
- 运行时由外壳解密，通过**子进程环境变量**注入 `OPENAI_API_KEY`，不写文件、不进命令行（避免被其他进程从命令行读到）。
- 设置界面只显示掩码（`sk-****abcd`），永不回显完整值。

### 5.3 环境变量注入表

外壳启动子进程时注入，**这是后端与桌面之间的唯一契约**：

| 变量 | 值 | 归属 | 说明 |
|---|---|---|---|
| `APP_ADDR` | `127.0.0.1:<port>` | backend | 替代 `PORT`，只绑 loopback |
| `SANDBOX_URL` | `http://127.0.0.1:<sport>` | backend | `cmd/app` 当前强依赖 |
| `SANDBOX_API_TOKEN` | 随机 32B | 双方 | 沙箱 fail-closed |
| `API_AUTH_TOKEN` | 随机 32B | backend | 由代理注入，浏览器不持有 |
| `REQUIRE_API_AUTH` | `true` | backend | 桌面强制鉴权 |
| `PLAN_STORE_PATH` | `data\plans` | backend | |
| `UPLOAD_ROOT` | `data\uploads` | 双方 | 路径白名单根 |
| `BLOB_STORE_PATH` | `data\blobs` | backend | |
| `LITERATURE_LIBRARY_DIR` | `data\literature` | backend | |
| `SANDBOX_WORKSPACE_ROOTS` | `workspaces` | 双方 | 必须一致 |
| `LOG_DIR` | `data\logs` | 双方 | **新增**，替代按仓库猜路径 |
| `OPENAI_API_KEY` | 解密值 | backend | 只走环境变量 |
| `OPENAI_BASE_URL` / `OPENAI_MODEL_NAME` | settings 值 | backend | |
| `SANDBOX_PROVIDER` | `auto`/`native`/`docker` | sandbox | **新增** |
| `SANDBOX_DEFAULT_IMAGE` | 空 或 `python:3.11-bullseye` | sandbox | native 模式可空 |
| `NoDefaultCurrentDirectoryInExePath` | `1` | 双方 | 防 PATH 劫持 |

### 5.4 首次启动向导

1. 欢迎页 + 隐私说明（本机执行、不上传代码）
2. 填 API Key / Base URL / 模型名 → 点「测试连接」（调用一次最小 completion）
3. 选择运行模式：
   - **推荐：在本机运行**（native，无需安装）
   - 检测到 Docker：显示「使用 Docker（更高隔离）」
   - 高级：远程沙箱（P4）
4. 下载 Python 运行时（显示大小与进度，可跳过）
5. 完成 → 进入工作台

---

## 6. 沙箱层设计

### 6.1 Provider 接口

```go
package providers

type Provider interface {
    Name() string                                  // docker | native | vm | remote
    Capabilities(ctx context.Context) (Capabilities, error)
    Create(ctx context.Context, spec CreateSpec) (Session, error)
    Delete(ctx context.Context, id string) error
    Exec(ctx context.Context, id string, cmd Command, emit func(Event) error) (*Result, error)
    InstallPackages(ctx context.Context, id string, packages []string) (*Result, error)
    SetNetwork(ctx context.Context, id string, on bool) error
    Usage(ctx context.Context, id string) (Usage, error)
    Reap(ctx context.Context, ttl time.Duration) (int, error)
}

type Capabilities struct {
    Provider        string   // docker / native / vm / remote
    Isolation       string   // container / vm / appcontainer / lowil / none
    Images          bool     // 是否支持 OCI 镜像
    NetworkControl  bool
    NetworkEnforced string   // os / firewall / none
    GPU             bool
    GPUEnforced     bool
    PythonVersions  []string
    OS              string
    Notes           []string
}

type CreateSpec struct {
    Image       string            // native 模式下为空
    MountPath   string            // 宿主机工作区
    Labels      map[string]string
    Limits      Limits
    NetworkMode string            // none | bridge
    PythonVer   string            // native 模式使用
    EnvHash     string            // venv 缓存键
}
```

### 6.2 能力协商

- `GET /api/v1/health` 返回当前选择、候选列表、每个候选的 `Capabilities`。
- 后端在创建计划前调用一次，把结果写进 `Plan.Metadata["sandbox_capabilities"]`。
- 规划器据此决定是否需要 `image_prepare` 节点：native 模式下该节点变为 `runtime_prepare`（准备 venv）。
- **能力不足必须前置暴露**，不能等执行到一半才失败。
- **执行中的计划禁止切换 provider**：设置页只在没有 running 计划时可改；否则提示「当前有 N 个计划在运行」。

### 6.3 native Provider 设计

#### 6.3.1 运行时管理

```text
EnsureRuntime(pythonVer, requirements) →
  1. 计算 envHash = sha256(pythonVer + sorted(requirements) + "win-x64")
  2. venv 已存在 → 直接复用
  3. 否则：
     uv python install <pythonVer>          (缓存到 runtimes\python)
     uv venv runtimes\venvs\<envHash> --python <pythonVer>
     uv pip install -r requirements.txt --python <venv>\Scripts\python.exe
     uv pip freeze > requirements.lock
  4. 写 EnvironmentManifest.runtime_hash = envHash
```

要点：

- `uv` 与 CPython 都是**按需下载**，安装包不背 torch 的 2.5GB。
- 依赖安装走「临时开网 → 装完关网」，与现有审批流程一致。
- `envHash` 相同则直接复用，第二次复现 0 安装时间。
- 失败时保留完整 pip 日志到 `data\logs\pip-<hash>.log`，并在错误里给出路径。

体积参考（安装包不包含任何 Python 运行时）：

| 组件 | 体积 | 下载时机 |
|---|---|---|
| `uv.exe` | ~15MB | 随安装包 |
| CPython 3.11 | ~30MB | 首次启动或首次执行 |
| numpy / pandas / scipy | ~80MB | 首次安装依赖 |
| torch（CPU wheel） | ~200MB | 首次安装依赖 |
| torch（CUDA wheel） | ~2.5GB | 用户显式选择 GPU 版本时 |

引导文案必须明示下载体积，并支持断点续传与镜像源配置。

#### 6.3.2 工作区

- 每个计划一个 `workspaces\<plan-id>`，创建时：
  1. 设置 ACL：移除继承，只授当前用户 + 沙箱 SID（AppContainer）读/写；
  2. 设置完整性标签（lowil 模式）；
  3. 记录原始路径的规范化形式（Windows 长路径加 `\\?\` 前缀）。
- 工作区外**只读**：AppContainer 天然做到；lowil 模式靠 NTFS 完整性级别（Medium 文件不可写）。

#### 6.3.3 Windows Runner（关键）

两档实现，共用接口：

| 档位 | 机制 | 隔离能力 | 兼容性 |
|---|---|---|---|
| `appcontainer` | `CreateAppContainerProfile` + `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES` + Job Object | 文件强隔离；**默认无网络**（不授予 `internetClient`）；进程树可回收 | 未知：命名对象/共享内存可能影响 multiprocessing |
| `lowil` | 复制自身进程令牌 → `SetTokenInformation(TokenIntegrityLevel, Low)` + Job Object | 不能写 Medium 及以上对象；**网络无 OS 级拦截** | 高：与普通 Python 进程一致 |

**默认策略**：

- 首次启动做一次**兼容性探针**（见附录 D），全过 → `appcontainer`；任一失败 → `lowil` 并提示「本机使用兼容模式，隔离强度较低」。
- 用户可在设置里强制切换。
- 需要 GPU 的任务强制 `lowil`（AppContainer 无法访问 CUDA 设备对象）。

**Job Object 配置**：

| 限制 | 值 |
|---|---|
| `JOBOBJECT_EXTENDED_LIMIT_INFORMATION.ProcessMemoryLimit` | 来自 `sandbox.memory_gb` |
| `JOBOBJECT_LIMIT_KILL_ON_JOB_CLOSE` | 必开（退出回收） |
| `JOBOBJECT_LIMIT_ACTIVE_PROCESS` | 64（防进程炸弹） |
| `JOBOBJECT_CPU_RATE_CONTROL_INFORMATION.CpuRate` | 来自 `sandbox.cpu_limit` 换算的百分比 |
| `JobObjectBasicUIRestrictions` | 禁止访问剪贴板、句柄继承 |

启动流程：`CREATE_SUSPENDED` → 建 Job → 绑定 → `AssignProcessToJobObject` → `ResumeThread`（避免子进程在绑定前跑掉）。

#### 6.3.4 网络控制

| 模式 | 实现 | 强度 |
|---|---|---|
| `appcontainer` 离线 | 不授予 `internetClient` / `privateNetworkClientServer` | 强（OS 强制） |
| `appcontainer` 联网 | 使用带能力的第二个 profile（`RA.Online`），进程需重启 | 强 |
| `lowil` 离线 | 只能靠「不给提示 + 审批门禁」，OS 不强制 | 弱，UI 必须标注 |
| 任意模式 | 安装时可选添加 Windows 防火墙出站规则（需一次管理员） | 强 |

**诚实原则**：`lowil` 模式下 `Capabilities.NetworkEnforced = "none"`，UI 显示「网络隔离：未强制」。这是产品必须承认的降级。

#### 6.3.5 资源与用量

- 内存/CPU/进程数：Job Object。
- 磁盘：没有免管理的配额；每执行完一次扫一次工作区大小（`filepath.WalkDir`），超限返回 507，与现在 Docker 引擎的行为一致。
- 网络字节：`GetExtendedTcpTable` 按进程统计，或直接返回 0 并在 UI 标注「native 模式不计流量」。
- 执行时长：`context.WithTimeout` + Job 强杀。

#### 6.3.6 输出与流式

- `cmd.StdoutPipe` / `StderrPipe` + 按行扫描 → 复用现有 NDJSON `ExecutionStreamEvent` 格式。
- 单行超长（>1MB）截断并标记 `truncated`。
- 退出码：`cmd.Wait()` 的 `ExitCode()`；被杀时映射为 `-1` 并附原因。

#### 6.3.7 清理

- 删 session → 终止 Job 内全部进程 → 删除 venv 引用（不删共享缓存）→ 按策略保留或删除工作区。
- reaper：每小时扫描超过 TTL（默认 120 分钟）的 session 与工作区。

### 6.4 docker Provider

- 直接包装现有 `NativeDockerEngine`，实现同一 `Provider` 接口。
- `Capabilities.Images = true`、`NetworkEnforced = "os"`（容器网络命名空间）。
- 只在检测到 `docker version` 成功且 `SANDBOX_DEFAULT_IMAGE` 可拉取时启用。
- **桌面不内置 Docker Desktop**：检测不到就静默用 native，绝不弹窗要求安装。

### 6.5 vm Provider（P4，预留）

- 接口先占位，`Capabilities.Images = true`。
- Windows 实现路径：`wsl --import` 预置发行版 + 内置 docker engine，宿主设置 `DOCKER_HOST`。
- 触发条件：仓库含 `Dockerfile`、依赖系统库、或用户显式选择「完整环境」。

### 6.6 与现有 REST 契约的映射

| 现有接口 | native 实现 |
|---|---|
| `POST /images/ensure` | 转成 `EnsureRuntime(pythonVer)`，返回 `{runtime_hash, size_bytes, pulled}` |
| `POST /sandboxes` | 建工作区 + venv 引用 |
| `POST /sandboxes/:id/commands` | 解释 `Command` 并启动受限进程 |
| `POST /sandboxes/:id/network` | 切换 runner（重启进程）或返回 `unsupported` |
| `GET /sandboxes/:id/usage` | 扫工作区 + 进程统计 |
| `DELETE /sandboxes/:id` | 杀 Job + 清理 |
| `GET /capabilities` | 返回 provider 能力矩阵 |

---

## 7. 命令契约改造

### 7.1 新的 Command 类型

```go
type Command struct {
    Argv        []string          // {"python", "-m", "pip", "install", "torch"}
    Cwd         string            // 逻辑路径，native/docker 各自翻译
    Env         map[string]string // 追加到基础环境
    Shell       bool              // 仅当必须用 shell（例如用户脚本中的管道）
    Timeout     time.Duration
    Stdin       string
    MaxOutputKB int
}
```

约定：

- `/workspace` 是**逻辑根**，Provider 负责映射到宿主真实路径或容器内挂载点。
- `python` / `python3` 是**逻辑命令**，native Provider 映射到 venv 的 `Scripts\python.exe`，docker Provider 保持原样。
- `Shell=true` 时：docker 用 `sh -lc`，native 用 `cmd /c`（并明确标注「Windows 下不保证 bash 语义」）。

### 7.2 改造清单

| 文件 | 现状 | 改法 |
|---|---|---|
| `agent/benchmark_harness.go:350,422` | `bash -lc "cd /workspace && ..."` | 改为 `Argv + Cwd` |
| `agent/coder_runtime.go:128` | `bash -lc "cd /workspace && python3 --version ..."` | 拆成两次 argv 调用 |
| `agent/coder_exec.go:19` | `bash -lc` 包 python 命令 | 改为 `Argv` + 环境变量 |
| `agent/paper_debug_harness.go:211` | `bash -lc command` | 同上 |
| `agent/autoresearch_eval.go:94` | `bash -lc shellCommand` | 同上 |
| `agent/coder_image.go:209-235` | 拼 `cd /workspace && find ...` | 改为 Provider 侧的工作区准备步骤 |
| 其余 `exec` 调用 | `{"python3", ...}` | 改成 `{"python", ...}`（逻辑名） |

改造原则：**后端不再知道宿主 OS**。所有 OS 差异收敛在 Provider 内部。

### 7.3 兼容层

为避免一次性全改，可先加一个 `legacyShell` 适配器：把旧字符串命令拆成 `{"sh","-lc",cmd}`，Windows native 下返回明确错误 `unsupported_posix_shell`，并把该节点标记为 `blocked`（而不是静默失败）。

---

## 8. 后端 Windows 可移植性

### 8.1 编译级阻塞（必须改）

| 位置 | 问题 | 改法 |
|---|---|---|
| `scheduler/repo_prepare.go:653,777` | `syscall.SysProcAttr{Setpgid:true}` + `syscall.Kill(-pid, SIGKILL)` | 抽 `internal/procgroup`：Linux 用进程组，Windows 用 Job Object `TerminateJobObject` |
| `scheduler/reproduction_mode.go:174` | `syscall.Statfs` | build tag 拆 `diskfree_unix.go` / `diskfree_windows.go`（`GetDiskFreeSpaceExW`） |
| `scheduler/reproduction_mode.go:155` | `/proc/meminfo` | 已按 `runtime.GOOS` 分支，补 `windows` 分支（`GlobalMemoryStatusEx`） |
| `docker-sandbox/internal/engine/capabilities_disk_windows.go` | 返回 0 | 改为真实实现（桌面需要磁盘探测） |

### 8.2 路径与临时目录

| 位置 | 问题 | 改法 |
|---|---|---|
| `cmd/api`、`cmd/app`、`docker-sandbox/main.go` | `resolveProjectRoot()` 向上找仓库结构 | 优先读 `LOG_DIR` / `APP_DATA_DIR`，找不到再回退（保留开发态行为） |
| `workspace.UploadRoots()` | 未配置时回退 `os.TempDir()` | 桌面注入 `UPLOAD_ROOT`，不依赖回退 |
| `uploads.Root()` | 回退 `TempDir` | 同上 |
| 长路径 | 默认 260 字符限制 | 关键路径统一加 `\\?\` 前缀；用 `os.Lstat` 前先规范化 |
| 大小写 | Windows 不区分大小写 | `workspace.validatePath` 的 `filepath.Rel` 比较要加 `strings.EqualFold` 兜底 |

### 8.3 配置与日志

- `appconfig.resolveConfigPath()` 会向上搜 8 层找 `config/config.toml`，安装后必失败。
  → 增加 `RA_CONFIG_PATH` 环境变量；桌面直接用环境变量注入，不写文件。
- 日志：`O_CREATE|O_WRONLY|O_TRUNC` 每次启动清空，桌面改为按大小轮转。

### 8.4 代理

`clearProxyEnv()` 会清掉 `HTTP_PROXY` / `HTTPS_PROXY`。桌面用户可能在公司代理后面，这会让 LLM 调用直接失败。

改法：桌面注入 `RA_KEEP_SYSTEM_PROXY=1` 时跳过清理，并按 Windows 系统代理设置（`ProxyEnable` / `ProxyServer` 注册表）填充环境变量；设置页提供「使用系统代理」开关。

---

## 9. 环境保真度与降级语义

### 9.1 EnvironmentManifest 扩展

```go
type EnvironmentManifest struct {
    // ... 现有字段保持不变
    SandboxProvider string `json:"sandbox_provider,omitempty"` // docker / native
    Isolation       string `json:"isolation,omitempty"`        // container / appcontainer / lowil
    RuntimeHash     string `json:"runtime_hash,omitempty"`     // native: envHash
    BaseOS          string `json:"base_os,omitempty"`          // windows 11 26200
    NetworkEnforced string `json:"network_enforced,omitempty"` // os / none
}
```

规则：

- docker：`Image` / `ImageDigest` 非空，`Isolation=container`。
- native：`Image` 为空、`RuntimeHash` 非空、`Isolationin{appcontainer,lowil}`。
- **两者不可混淆**：导出包、UI、审计都按 provider 分支。

### 9.2 缺失能力

`MissingRequirement.Kind` 新增：

| Kind | 触发 | Remediation |
|---|---|---|
| `sandbox_capability` | 任务需要镜像/系统库，当前 provider 不支持 | 「安装完整运行时」或「改用远程沙箱」 |
| `runtime_download_failed` | uv 下载 CPython 失败 | 检查网络 / 配置镜像源 |
| `python_version_unavailable` | 仓库要求 3.14 等未支持版本 | 升级应用 / 用容器 |

### 9.3 UI 呈现

- 计划详情页：环境徽章 `容器` / `原生(AppContainer)` / `原生(兼容)`，hover 展开字段。
- 计划列表：失败计划显示缺失项与「一键补救」。
- 全局提示条：`lowil` 模式下常驻「网络隔离未强制」的黄色提示，可点「了解详情」。

---

## 10. 前端改动清单

| 改动 | 文件 | 说明 |
|---|---|---|
| 运行模式设置页 | `features/settings/RuntimePanel.tsx`（新） | provider 选择、运行时安装状态、磁盘占用清理 |
| 首次启动向导 | `features/onboarding/Onboarding.tsx`（新） | API Key、测试连接、模式选择、下载进度 |
| 环境保真度徽章 | `components/EnvironmentBadge.tsx`（新） | 计划详情与列表 |
| 计划完成通知 | `app/ResearchAssistantApp.tsx` | 通过外壳桥接系统通知 |
| 原生对话框 | 导出/导入 | 「选择仓库目录」「在文件夹中显示」 |
| 错误页 | 外壳独立页面 | 后端起不来时显示日志路径与诊断按钮 |
| 移除 dev token 路径 | `config/env.ts` | 桌面版固定同源，不再注入 |

前端**不重写**：现有 `window.location.origin` 逻辑在桌面同源场景直接可用。

---

## 11. 打包、签名、更新

### 11.1 安装包

- NSIS，per-user 安装到 `%LOCALAPPDATA%\Programs\ResearchAssistant`，无需管理员。
- 体积目标：**≤ 60MB**（不含 Python 运行时与 torch）。
- 卸载默认保留用户数据，二次确认才删除。
- 关联 `.pdf` 与协议 `researchassistant://`（用于浏览器跳转回来）。

### 11.2 签名

| 平台 | 方式 |
|---|---|
| Windows | EV 证书或 Azure Trusted Signing；对 `ResearchAssistant.exe`、`backend.exe`、`sandbox.exe`、`uv.exe` 全部签名 |
| 安装包 | 同证书签名，减少 SmartScreen 拦截 |

签名同时是**自动更新的信任根**：更新包必须验证签名才允许执行。

### 11.3 自动更新

```text
GET https://update.example.com/latest.json
{
  "version": "0.9.2",
  "url": "https://.../ResearchAssistant-0.9.2.exe",
  "sha256": "...",
  "signature": "...",
  "release_notes_url": "..."
}
```

流程：后台检查 → 下载到 `%TEMP%\ra-update` → 校验 sha256 + 签名 → 退出时静默安装（`/S`）→ 重启。

失败降级：网络失败静默忽略；签名不匹配直接删除并记录安全事件。

---

## 12. 安全模型与威胁分析

### 12.1 威胁模型

单用户桌面，攻击面是 **LLM 生成/仓库自带的不可信代码**。

| 威胁 | Docker 现状 | native(AppContainer) | native(LowIL) | 缓解 |
|---|---|---|---|---|
| 删改用户文件 | 容器隔离 | 阻止（只授权工作区） | 阻止写入（完整性级别） | ACL + 完整性标签 |
| 读取隐私文件 | 容器内看不到 | **可读**（继承用户读权限） | **可读** | 文档明示；后续可加只读白名单 |
| 数据外传 | 默认断网 | 阻止（无网络能力） | **不强制** | 默认断网 + 审批；lowil 模式黄色警告 |
| 挖矿 / 吃满 CPU | cgroup | Job CPU 限制 | 同左 | 上限 + watchdog |
| 进程炸弹 | pids-limit | Job 进程数上限 | 同左 | 64 |
| 持久化驻留 | 删容器 | Job kill-on-close | 同左 | 退出回收 |
| 提权 | cap-drop + 非 root | 低完整性 | 低完整性 | 永不请求管理员 |
| 供应链（恶意 wheel） | 镜像 allowlist | 锁文件 + hash | 同左 | 记录并展示来源 |

**必须写进产品文案**：native 模式不是容器级隔离，它防的是「误删误改」和「默认外传」，不防针对性逃逸。

### 12.2 本地服务安全

- 只绑 `127.0.0.1`，随机端口。
- 随机 Bearer token，仅存在于进程环境变量与代理内存。
- loopback 守卫中间件：`Host` 白名单 + `Origin` 校验 + `Sec-Fetch-Site` 检查。
- SSE ticket 机制保留（60s HMAC），桌面下仍需测试。

---

## 13. 观测与诊断

| 能力 | 实现 |
|---|---|
| 日志 | `data\logs\{shell,backend,sandbox}.log`，20MB × 5 轮转 |
| 诊断包 | 一键打包：日志尾部 5MB、settings（脱敏）、provider 能力、系统信息、最近失败的 3 个计划元数据 |
| 崩溃转储 | Windows Error Reporting 本地 dump 到 `data\crashes`，诊断包可选包含 |
| 指标 | 复用现有 `/metrics`，桌面不上报，仅本地展示 |
| 遥测 | 默认关闭；开启后只上报匿名计数（启动次数、provider 分布、失败分类） |

---

## 14. 测试策略

### 14.1 单元

- Provider 契约测试：同一套用例跑 `docker` 与 `native`（跳过容器相关断言）。
- Command 翻译：`/workspace`、`python3` 映射，Windows 路径规范化。
- Job Object 封装：限额结构体、错误码映射。
- settings / DPAPI：加解密、篡改检测。

### 14.2 集成（Windows Runner）

必须覆盖（也是 AppContainer 的 go/no-go）：

1. `multiprocessing.Pool(2)` 正常
2. torch `DataLoader(num_workers=2)` 正常
3. `pip install` 到 venv 正常
4. matplotlib 字体缓存生成正常
5. 工作区外写入被拒绝
6. 网络连接被拒绝（AppContainer）/ 记录为未强制（lowil）
7. 关掉 Job 后进程树全部退出
8. 大输出（100MB）不 OOM，能截断

### 14.3 E2E

- 复用现有 `test/e2e/round7-*.mjs`，加 `RA_SANDBOX_PROVIDER=native` 变量跑一遍。
- 新增 `desktop-smoke.mjs`：直接对 `backend.exe + sandbox.exe` 发起 API 调用（绕过 GUI）。
- 新增 `desktop-launch.ps1`：启动 exe、等窗口、检查进程树、发退出信号、验证无残留。

### 14.4 回归基线

- docker provider 下必须与现网一致（现有 E2E 全绿），这是「不破坏已有能力」的门禁。
- native provider 下至少覆盖：General 计划、论文复现 smoke、Benchmark、AutoResearch 各 1 条。

---

## 15. 分阶段实施

### P0：外壳 + Windows 编译（第 1–2 周）

任务：

1. 新建 `desktop/` 模块，Wails 窗口 + 托盘 + 单实例 + 日志
2. Supervisor：启动/健康检查/重启/退出回收（Job Object）
3. loopback 反向代理（SSE 透传）
4. backend 与 sandbox 的 Windows 交叉编译打通（8.1）
5. 注入路径类环境变量（5.3 中除 provider 外的全部）
6. NSIS 打包脚本（先不签名）

验收：干净 Win11 上安装 → 启动 → 连已有 Docker 沙箱 → 创建并执行一个 General 计划 → 托盘退出后无残留进程。

### P1：Provider 抽象 + native 引擎 MVP（第 3–4 周）

任务：

1. `providers` 包与接口，docker 适配器包装现有引擎
2. `Command` 契约 + 7 处调用点改造（7.2）
3. native：uv 运行时管理、venv 缓存、lowil runner、Job Object
4. `EnvironmentManifest` 扩展 + 能力协商
5. 前端运行模式设置页

验收：**没有 Docker 的机器**上跑通 annotated-transformer torch smoke，`EnvironmentManifest.isolation=lowil`。

### P2：AppContainer 与网络隔离（第 5–6 周）

任务：

1. AppContainer profile 创建/回收、工作区 ACL、启动器
2. 兼容性探针（附录 D）+ 自动降级到 lowil
3. 双 profile 网络开关
4. 资源限额完善（CPU rate、进程数、磁盘扫描）
5. 保真度徽章 + 黄色警告条
6. 安全回归用例（14.2）

验收：AppContainer 模式下网络连接被 OS 拒绝；探针失败时自动降级且 UI 明示。

### P3：发布工程（第 7 周）

任务：

1. 代码签名、NSIS 静默安装、卸载保留数据
2. 自动更新（签名校验）
3. 诊断包导出
4. 首次启动向导 + 运行时下载进度
5. 崩溃恢复与「任务已中断」提示

验收：0.9.0 → 0.9.1 原地升级不丢数据；断网升级失败可回退。

### P4：增强档位（第 8 周起）

1. docker 自动升级提示（检测到 Docker 时建议切换）
2. vm provider（WSL2）POC
3. 远程 provider 接入（复用 OpenSandbox）

---

## 16. 风险登记

| # | 风险 | 概率 | 影响 | 触发信号 | 应对 |
|---|---|---|---|---|---|
| R1 | AppContainer 破坏 multiprocessing / 共享内存 | 中 | 高 | 探针第 1、2 项失败 | 自动降级 lowil；ML 任务默认 lowil |
| R2 | AppContainer 无法访问 GPU | 高 | 中 | CUDA 初始化失败 | GPU 任务强制 lowil，UI 提示 |
| R3 | lowil 无法强制断网 | 确定 | 中 | — | UI 明示 + 审批门禁 + 可选防火墙规则 |
| R4 | 杀软误报（进程注入类 API） | 中 | 高 | SmartScreen/Defender 拦截 | 签名 + 提交厂商白名单 + 避免可疑 API 组合 |
| R5 | 长路径 / 大小写导致的文件定位失败 | 中 | 中 | 工作区文件找不到 | `\\?\` 前缀 + 测试覆盖 |
| R6 | Python 运行时下载失败（网络/镜像） | 中 | 高 | uv 报错 | 镜像源配置 + 离线安装包 |
| R7 | 子进程孤儿（外壳被杀） | 低 | 高 | 任务管理器残留 | Job `KILL_ON_JOB_CLOSE` 兜底 |
| R8 | 反向代理破坏 SSE | 中 | 高 | 计划状态不更新 | `FlushInterval=-1` + 40 分钟长连接测试 |
| R9 | 改造命令契约引入回归 | 中 | 高 | docker 模式 E2E 失败 | legacyShell 兼容层 + 双 provider 回归 |
| R10 | Wails 版本不稳定/更新能力弱 | 低 | 中 | 构建失败/无更新 | 自研简易更新器；外壳与业务解耦，可换 Tauri |

---

## 17. 工作量估算

| 阶段 | 单人 | 双人并行 |
|---|---|---|
| P0 外壳 + 编译 | 2 周 | 1.5 周 |
| P1 Provider + native MVP | 2 周 | 1.5 周 |
| P2 AppContainer + 安全 | 2 周 | 1.5 周 |
| P3 发布工程 | 1 周 | 0.5 周 |
| P4 增强 | 1 周+ | 1 周 |
| **合计到可发布** | **约 7 周** | **约 5 周** |

双人拆分建议：A 负责 `desktop/` 与发布工程；B 负责 `providers` + native 引擎 + 命令契约改造。

---

## 附录 A：环境变量总表

见 5.3。新增变量共 5 个：`LOG_DIR`、`APP_DATA_DIR`、`SANDBOX_PROVIDER`、`RA_KEEP_SYSTEM_PROXY`、`RA_CONFIG_PATH`。

## 附录 B：关键接口

```go
// providers/provider.go
type Event struct {
    Type    string // chunk | final
    Stream  string // stdout | stderr
    Message string
    Result  *Result
}

type Result struct {
    Stdout     string
    Stderr     string
    ExitCode   int
    DurationMS int64
    Truncated  bool
}

type Session struct {
    ID          string
    Provider    string
    Isolation   string
    Workspace   string
    VenvPath    string // native only
    ContainerID string // docker only
    CreatedAt   time.Time
}
```

## 附录 C：Windows 启动伪代码

```go
func (s *Supervisor) Start(ctx context.Context) error {
    if _, err := s.acquireSingleInstance(); err != nil {
        s.activateExistingWindow()
        return ErrAlreadyRunning
    }
    cfg, err := settings.Load()
    if err != nil { return err }

    backendPort, sandboxPort, err := pickPorts()
    if err != nil { return err }

    backendJob, err := procgroup.NewJob(procgroup.Limit{
        MemoryBytes: cfg.Sandbox.MemoryGB << 30,
        KillOnClose: true,
        MaxProcesses: 64,
    })
    if err != nil { return err }

    s.backend = exec.Command(s.paths.BackendExe)
    s.backend.Env = envForBackend(cfg, backendPort, sandboxPort)
    if err := s.backend.Start(); err != nil { return err }
    if err := backendJob.Assign(s.backend.Process); err != nil { return err }

    if err := waitHealthy(ctx, backendURL+"/api/health", 30*time.Second); err != nil {
        return fmt.Errorf("backend failed to start: %w (logs: %s)", err, s.paths.LogsDir)
    }

    s.sandbox = exec.Command(s.paths.SandboxExe)
    s.sandbox.Env = envForSandbox(cfg, sandboxPort)
    // ... 同样启动 + 健康检查

    return s.openWindow(s.proxyURL())
}
```

## 附录 D：AppContainer 兼容性探针（go/no-go）

首次启动时在临时工作区执行，全部通过才启用 AppContainer：

| # | 用例 | 期望 | 失败后果 |
|---|---|---|---|
| 1 | `multiprocessing.Pool(2).map(len, ["a","bb"])` | `[1,2]` | 降级 lowil |
| 2 | `torch.utils.data.DataLoader(range(8), num_workers=2)` | 跑通 | 降级 lowil |
| 3 | `pip install --target ... six` | 成功 | 降级 lowil |
| 4 | `matplotlib.font_manager` 初始化 | 无异常 | 记录警告 |
| 5 | 写 `C:\Users\<me>\Desktop\x.txt` | `PermissionDenied` | 安全失败 |
| 6 | `socket.connect(("1.1.1.1", 53))` | 拒绝 | 安全失败 |
| 7 | 杀掉 Job | 子进程全退 | 安全失败 |

探针结果写入 settings（`sandbox.probe_report`），用户可在设置页重跑。

## 附录 E：待决问题

1. 安装包是否需要「离线完整版」（内置 Python 运行时）？影响体积从 60MB 到 200MB。
2. 是否要求「关闭窗口即退出」为默认（部分用户不适应托盘常驻）？
3. 是否内置浏览器内核之外的 PDF 打开能力（当前用 pdf.js，WebView 足够）？
4. 遥测是否默认开启（影响产品迭代与隐私口碑）？
5. 首个版本是否带上 docker provider 的「自动升级提示」，还是在设置页静默提供？
