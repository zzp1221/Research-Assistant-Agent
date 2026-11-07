# Desktop build

简体中文 | [English](README.en.md)

## 一键构建

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1 -Version 0.9.0
```

`build.ps1` 执行完整流水线：

1. 构建 React 前端（`npm --prefix research-assistant/frontend run build`），并把 `dist` 复制到 `desktop/webui/dist`，供 Go 二进制嵌入；
2. 构建 `backend.exe`（来自 `research-assistant/backend/cmd/api`）与 `sandbox.exe`（来自 `research-assistant/docker-sandbox`）；
3. 在 `desktop` 运行 `mygo build -skip-installer`，产出 `desktop/build/mygo/windows-amd64/ResearchAssistant.exe`；
4. 暂存产物并运行 `package.ps1`，写出 `dist/ResearchAssistant-<version>-Setup.exe`。

安装器是标准 MUI2 向导：欢迎、**安装位置**（带 Browse 按钮）、组件（含可选 PDF 关联）、安装进度、完成。它会记住上一次安装目录。

需要 Go 1.27.3、Node.js 22+ 与 `PATH` 中的 `makensis`；Go 不在 `PATH` 时传 `-Go C:\path\to\go.exe`。`-SkipFrontend`、`-SkipRuntime`、`-SkipInstaller` 与 `-Output` 可缩小构建范围。设置 `UV_EXE` 可把 `uv.exe` 复制进安装包。

## 运行安装包

从普通用户目录（如 Desktop/Downloads）运行 `ResearchAssistant-<version>-Setup.exe`。NSIS 会在安装包旁创建并删除临时文件；ACL 禁止 `DeleteSubdirectoriesAndFiles` 的目录（某些沙箱工作区）会报 `Error writing temporary file. Make sure your temp folder is valid.`。把安装包复制出该目录，或用 `-Output "$env:USERPROFILE\Downloads"` 构建。

## 仅打包

```powershell
powershell -ExecutionPolicy Bypass -File package.ps1 -Version 0.9.0 `
  -MyGoBin "desktop\build\mygo\windows-amd64" `
  -RuntimeBin "desktop\build\runtime"
```

`MyGoBin` 必须包含 MyGo 构建出的 `ResearchAssistant.exe`；`RuntimeBin` 必须包含 `backend.exe` 与 `sandbox.exe`，可选 `uv.exe`。两者默认都指向 `build/mygo/windows-amd64`。

脚本把文件暂存到 `build/staging`，用 `installer.nsi` 调用 `makensis`，并把 `ResearchAssistant-Setup.exe` 复制为 `dist/ResearchAssistant-<version>-Setup.exe`。`Version` 必须是三段（例如 `0.9.0`），会写入 VIProductVersion 与卸载项。

NSIS 脚本可在 Linux 容器内冒烟：

```bash
docker run --rm -v "$PWD/desktop/build:/build" -w /build debian:bookworm-slim \
  sh -c "apt-get update && apt-get install -y nsis && bash installer-smoke.sh"
```

## 签名

```powershell
powershell -ExecutionPolicy Bypass -File sign.ps1 -CertPath "C:\certs\ra.pfx" -CertPassword "..."
```

`sign.ps1` 会为当前目录（暂存目录）中的 `ResearchAssistant.exe`、`backend.exe`、`sandbox.exe`、`uv.exe` 与安装包签名。

## 更新清单

`update-manifest.example.json` 是 `latest.json` 的结构：`sha256` 是安装包哈希，`signature` 是小写十六进制哈希的 Ed25519 签名（base64）。
