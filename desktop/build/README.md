# Desktop build

## Package

```powershell
powershell -ExecutionPolicy Bypass -File package.ps1 -Version 0.9.0 -WailsBin "C:\build\bin"
```

`WailsBin` must contain:

- `ResearchAssistant.exe`
- `backend.exe`
- `sandbox.exe`
- `uv.exe`（可选，缺失时安装包不包含 uv）
- `webview\`（前端构建产物）

脚本会把文件暂存到 `build/staging`，调用 `makensis` 生成 `ResearchAssistant-Setup.exe`，并复制到 `dist/ResearchAssistant-<version>-Setup.exe`。

`Version` 必须是三段式（例如 `0.9.0`），会写入 VIProductVersion 与卸载项 DisplayVersion。

NSIS 脚本可在 Linux 容器里做编译烟测：

```bash
docker run --rm -v "$PWD/desktop/build:/build" -w /build debian:bookworm-slim \
  sh -c "apt-get update && apt-get install -y nsis && bash installer-smoke.sh"
```

## Signing

```powershell
powershell -ExecutionPolicy Bypass -File sign.ps1 -CertPath "C:\certs\ra.pfx" -CertPassword "..."
```

## Update manifest

`update-manifest.example.json` 是 latest.json 的结构示例；`sha256` 为安装包哈希，`signature` 为对 sha256 十六进制字符串的 Ed25519 签名（base64）。
