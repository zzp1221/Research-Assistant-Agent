# 修复报告：NSIS 版本参数化 + 安装包编译烟测 + 回归（第二十四轮）

- 日期：2026-10-31
- 分支：feature/desk
- 依据：docs/design/2026-10-30-windows-desktop-client.md
- 状态：实现完成，容器内 makensis 编译通过；Windows 真机安装未验证。

## 1. 本轮完成

### 1.1 NSIS 版本参数化（11）

- installer.nsi 增加 !ifndef VERSION 默认 0.9.0。
- VIProductVersion 使用 "${VERSION}.0"（三段式版本）。
- VIAddVersionKey FileVersion 与卸载项 DisplayVersion 使用 ${VERSION}。
- 补充 LegalCopyright，消除 makensis 版本信息警告。
- README 说明三段式版本约束。

### 1.2 安装包编译烟测（11）

- 新增 desktop/build/installer-smoke.sh：临时目录生成 dummy exe/uv/webview，复制 installer.nsi，执行 makensis，断言 Setup.exe 非空。
- Debian bookworm + nsis 容器内编译通过（输出 ResearchAssistant-Setup.exe）。
- POSIX makensis 使用 -DVERSION=（Windows 的 /DVERSION= 由 package.ps1 使用）。

## 2. 实际与文档不一致处

- package.ps1 一直传 /DVERSION，但 installer.nsi 完全忽略，版本固定 0.9.0；本轮修复。
- 此前没有安装包编译验证；本轮新增容器内烟测。

## 3. 测试证据

| 层级 | 命令 | 结果 |
|---|---|---|
| 安装包编译 | Debian + nsis + installer-smoke.sh | 通过，生成 Setup.exe |
| backend 全量 | go build/vet/test | 通过 |
| docker-sandbox 全量 | go test + Windows 交叉编译 | 通过 |
| desktop 全量 | go test + Windows 交叉编译（Wails tag） | 通过 |
| 前端 | npm test/build/lint | 36 文件 / 144 tests 通过 |
| E2E | http-smoke / desktop-smoke / sandbox-command / sandbox-network / repro-bundle | 全部通过 |

## 4. 限制与后续

- Windows 真机安装/卸载、签名、更新服务器未验证。
- makensis 只验证脚本可编译，不验证安装行为/快捷方式/注册表实际写入。
- Version 必须三段式；四段式会导致 VIProductVersion 非法。

## 5. 清理

- 烟测使用容器内 mktemp，容器 --rm 后自动清理；无 D:\tmp 残留。
