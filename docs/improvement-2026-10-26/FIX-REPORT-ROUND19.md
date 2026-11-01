# 修复报告：崩溃转储注册 + 打包流水线 + 全量 E2E 回归（第十九轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成；容器内单测与大部分 E2E 通过，部分 E2E 依赖外部环境（LLM/vision/OCR/docker CLI），已记录限制。

## 1. 本轮完成

### 1.1 崩溃转储注册（13）

- 新增 desktop/crashdump：Targets（ResearchAssistant.exe/backend.exe/sandbox.exe）、RegistryPath、DumpDir。
- Windows Enable 写 HKCU\Software\Microsoft\Windows\Windows Error Reporting\LocalDumps\<exe>：DumpFolder=<DataDir>\crashes、DumpType=2、DumpCount=5；非 Windows no-op。
- main_wails 启动时调用 crashdump.Enable；诊断包已有 crashes.json + dump 内容收集。
- 纯逻辑单测 + Windows 交叉编译通过。

### 1.2 打包流水线（11）

- 新增 desktop/build/package.ps1：校验 ResearchAssistant.exe/backend.exe/sandbox.exe、可选 uv.exe、webview；暂存 build/staging；调用 makensis；输出 dist/ResearchAssistant-<version>-Setup.exe。
- 新增 desktop/build/README.md 说明打包/签名/更新 manifest。
- 与已有 installer.nsi/sign.ps1/update-manifest.example.json 衔接。

### 1.3 全量 E2E 回归（14）

- 运行 16 个既有 E2E 套件 + repro-bundle，结果见第 3 节。
- 环境限制：
  - model-profiles/verification 需要 ALLOW_PRIVATE_MODEL_ENDPOINTS=true、MOCK_VISION_URL 与 admin token；默认安全配置下 SSRF 会拒绝本地 mock。
  - document-intelligence 的 scanned PDF 断言要求 PDF_OCR_MAX_PAGES=20；容器默认 OCR 预算为 5 页。
  - tool-ecosystem 的 A5/A6 需要测试容器内 docker CLI 与 docker.sock；A7 需要真实 LLM 服务。
  - repro-bundle 需要测试容器安装 unzip。
- 未发现由本轮代码引入的回归。

## 2. 实际与文档不一致处

- 文档 13 的崩溃转储此前只有收集侧，没有 WER LocalDumps 注册；本轮补齐。
- 文档 11 的打包此前只有 installer.nsi/sign.ps1，没有 staging/makensis 脚本；本轮补齐。
- 文档 14 的测试策略此前只跑 5 个 E2E；本轮扩展到既有套件并记录环境限制。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| desktop 单测 | go test ./...（crashdump/updater/bridge 等） | 通过 |
| desktop Windows 交叉编译 | GOOS=windows GOARCH=amd64 go build ./... + Wails tag | 通过 |
| backend 全量 | go build/vet/test | 通过 |
| docker-sandbox 全量 | go build/test + Windows 交叉编译 | 通过 |
| 前端 | npm test、npm run build、npm run lint | 通过 |
| E2E 通过 | http-smoke、routing-and-prompts、literature、literature-plan、plan-library、cost-budget、budget-recovery、plan-revisions、review-hitl、sandbox-command、sandbox-network、desktop-smoke、repro-bundle（安装 unzip 后） | 通过 |
| E2E 环境限制 | model-profiles/verification（private endpoints/vision，开启后仍有 2/21 项依赖 mock 行为）、document-intelligence（OCR 默认 5 页 vs 测试要求 20）、tool-ecosystem（docker CLI + admin 后 40/44；A7 依赖真实 LLM 行为） | 记录限制，非代码回归 |

## 4. 限制与后续

- 真实 Windows WER dump、安装包、签名、更新服务器未验证。
- E2E 环境限制项需要测试镜像补齐 docker CLI、unzip、vision/LLM mock 配置；不属于产品代码缺陷。
- AppContainer 实际降权、systray、DPAPI、WebView2 未真机验证。

## 5. 清理

- 临时容器均为 --rm；不删除 compose 数据卷与项目镜像。
