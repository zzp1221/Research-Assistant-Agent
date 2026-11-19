# 镜像精确授权迁移说明

日期：2026-11-16（America/Phoenix）。对应 SEC-04；本文件描述配置和行为变更，测试结果以本轮实施报告为准。

## 配置变化

`SANDBOX_IMAGE_ALLOWLIST` 现在是精确镜像引用列表，使用 OCI/Docker 引用解析器归一化 Docker Hub 短名称、registry、repository、tag 与 sha256 digest。tag 区分大小写，registry host 不区分大小写。每项必须包含显式 tag 或 digest。

```dotenv
SANDBOX_IMAGE_ALLOWLIST=docker.io/library/python:3.9-bullseye,docker.io/library/python:3.11-bullseye
SANDBOX_REGISTRY_MIRROR=
```

- 未设置白名单时使用上面两个内置精确引用。显式空字符串、空条目、无 tag 且无 digest、非法引用及旧目录前缀规则会报错，不会放行所有镜像。纯 native 执行不因未使用的容器配置报错。
- 旧规则 `python:3.11` 只允许该 tag，不再允许 `python:3.11-slim`；需要的变体必须逐项列举。
- `registry.example/research/` 不再代表 namespace 授权，应改成所需完整 repository 与 tag/digest 列表。
- 允许 `python:3.11-bullseye` 不会允许 `evil.example/python:3.11-bullseye`。`python`、隐式 `latest` 和任意 tag 后缀也不因此获得授权。
- tag-only、digest-only、tag+digest 是不同的完整规则形态，必须精确声明。例如允许 `repo@sha256:…` 不隐式允许 `repo:any-tag@sha256:…`；两者需分别配置。

Compose 的默认表达式保留显式空值，让执行服务拒绝错误配置。直接启动容器模式时配置错误会在启动校验中报错；确保修改执行服务的配置，不能只修改后端的环境文件。

## 可信镜像代理

`SANDBOX_REGISTRY_MIRROR` 是单个显式可信的 Docker Hub 地址映射，例如 `mirror.example:5443/cache`。值不包含 `http://`、`https://`、凭据、查询参数、片段或尾部斜杠。

逻辑来源 `docker.io/library/python:3.11-bullseye` 先通过精确白名单，再映射为 `mirror.example:5443/cache/library/python:3.11-bullseye`。白名单中写逻辑 Docker Hub 来源，不重复配置代理目的地址；两种含义冲突的配置会报错。直接提交已映射地址时，也必须反向对应一个已允许的 Docker Hub 来源；伪代理或不同路径不因名称相似而通过。其他显式 registry 不会被重写。

镜像检查、拉取、创建、复用以及 OpenSandbox fallback/remote 都会校验来源。未授权镜像在镜像检查、网络拉取和远程创建之前拒绝。该映射意味着运维方信任配置中的镜像代理；tag-only 规则本身不验证代理内容与官方仓库相同。需要内容级保证时使用明确的 sha256 digest 规则。

## 内容固定与运行记录

本机 Docker 首次准备镜像后，执行服务在内存中记录规范逻辑引用、实际传输引用、manifest digest（如可获取）、本机 image config ID、平台和大小。同一服务生命周期内，创建和复用都运行已检查的不可变 image ID，并设置 `--pull=never`；后续 tag 漂移不会改变已准备的执行内容。

`EnsureImage` 返回新增的 `resolved_image`、`repo_digest`、`image_id`、`digest_source` 和 `platform`。`digest` 优先报告对应 repository 的 manifest/index digest；无 RepoDigest 的本地镜像仍返回 config ID，`digest_source=image_config` 明确区分两种来源。平台字段来自本机镜像 metadata，不将 config ID 冒充 registry manifest digest。

显式 digest 请求必须由该实际 repository 的 `RepoDigests` 证明；不匹配时不运行，也不写入准备缓存。tag+digest 规则还要求独立本地 tag 指向同一 config ID；先准备匹配 tag，再使用组合引用，或采用 digest-only 规则。

准备缓存只在当前进程内保存。若已固定镜像被外部删除，后续操作报 `image_pin_unavailable`，不会静默重新解析同名 tag；恢复前应检查来源并重新准备。服务重启后会重新准备，跨重启或长期复现需在计划与白名单中固定 digest。远程服务收到的是已授权的规范逻辑来源，实际内容固定和执行记录由远端实现负责，本机未验证其 manifest 或平台。

## 升级与回退

升级前列出正在使用的镜像及所有变体，把旧前缀规则替换为精确项，并校验代理配置。相应容器执行服务启动成功后，重新准备镜像，核对上述来源、digest 与平台字段，再执行现有科研任务。

规则误拒绝时补充正确精确项或修正可信映射，不恢复任意前缀/后缀放行。需要重新选择 tag 内容时，应结束使用原准备结果的任务，核对新内容后重启执行服务重新准备；不要删除固定镜像后依赖隐式拉取。

## 实测与复跑

2026-11-16 已在 Docker Desktop daemon 29.2.1、linux/amd64 上完成真实 digest 执行、测试 tag 漂移和测试自有 registry 拉取/执行，三项共 11.093s 通过。单元反例仍用于证明未授权输入在 inspect/pull 前拒绝；两类测试互为补充。完整证据与未覆盖平台见 [实施记录](defect-remediation-2026-11-16-security-results.md)。

在 `research-assistant/docker-sandbox` 运行，要求真实 Linux 容器 daemon。测试只创建自己的临时 tag、容器和网络，不改用户源镜像 tag：

```powershell
docker pull --platform linux/amd64 python:3.11-bullseye
docker pull --platform linux/amd64 alpine:latest
$env:RA_TEST_DOCKER_INTEGRATION = '1'
go test ./internal/engine -run '^TestImagePolicyRealDocker' -count=1 -v -timeout=180s
Remove-Item Env:RA_TEST_DOCKER_INTEGRATION
```

可信 registry 用例还需要测试方创建只绑定 daemon loopback 的独立 registry；启动后将其地址写入 `RA_TEST_DOCKER_REGISTRY`，例如 `127.0.0.1:5000`，再执行 `TestImagePolicyRealTrustedRegistryPullAndDigest`。Docker Desktop 的 Windows published port 不一定能被 daemon 自身经 127.0.0.1 访问；本轮使用 daemon host networking，未修改全局 insecure-registry 配置。测试后删除自己的 registry 和临时资源。

未设置 opt-in 时的 skip 不算真实 Docker 验收。CI 的 Linux job 显式开启 digest 和 tag 漂移两条真实测试；registry 集成有本轮本机日志，尚未纳入该 CI job。远程 provider 的实际内容固定仍需由远端验证，不能借用本机结果。
