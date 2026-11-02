# Desktop build

## One command

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1 -Version 0.9.0
```

`build.ps1` runs the whole pipeline:

1. builds the React frontend (`npm --prefix research-assistant/frontend run build`)
   and copies its `dist` to `desktop/webui/dist`, which the Go binary embeds;
2. builds `backend.exe` (from `research-assistant/backend/cmd/api`) and
   `sandbox.exe` (from `research-assistant/docker-sandbox`);
3. runs `mygo build -skip-installer` in `desktop`, producing
   `desktop/build/mygo/windows-amd64/ResearchAssistant.exe`;
4. stages the artifacts and runs `package.ps1`, which writes
   `dist/ResearchAssistant-<version>-Setup.exe`.

Requires Go 1.27.3, Node.js 22 or later and `makensis` on `PATH`; pass
`-Go C:\path\to\go.exe` when Go is not on `PATH`. `-SkipFrontend`,
`-SkipRuntime`, `-SkipInstaller` and `-Output` narrow the run.
Set `UV_EXE` to copy a `uv.exe` into the installer.

## Package only

```powershell
powershell -ExecutionPolicy Bypass -File package.ps1 -Version 0.9.0 `
  -MyGoBin "desktop\build\mygo\windows-amd64" `
  -RuntimeBin "desktop\build\runtime"
```

`MyGoBin` must contain `ResearchAssistant.exe` from the MyGo build;
`RuntimeBin` must contain `backend.exe` and `sandbox.exe`, and may contain
`uv.exe`. Both default to `build/mygo/windows-amd64`.

The script stages the files in `build/staging`, calls `makensis` with
`installer.nsi`, and copies `ResearchAssistant-Setup.exe` to
`dist/ResearchAssistant-<version>-Setup.exe`. `Version` must be three
parts (for example `0.9.0`); it is written to VIProductVersion and the
uninstall entry.

The NSIS script can be smoke-tested in a Linux container:

```bash
docker run --rm -v "$PWD/desktop/build:/build" -w /build debian:bookworm-slim \
  sh -c "apt-get update && apt-get install -y nsis && bash installer-smoke.sh"
```

## Signing

```powershell
powershell -ExecutionPolicy Bypass -File sign.ps1 -CertPath "C:\certs\ra.pfx" -CertPassword "..."
```

`sign.ps1` signs `ResearchAssistant.exe`, `backend.exe`, `sandbox.exe`,
`uv.exe` and the installer when they are in the current directory (the
staging directory).

## Update manifest

`update-manifest.example.json` is the structure of `latest.json`: `sha256`
is the installer hash and `signature` is the Ed25519 signature of the
lower-case hex hash, base64 encoded.
