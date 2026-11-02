param(
  [string]$Version = "0.9.0",
  [string]$Output = "dist",
  [string]$Go = "go",
  [switch]$SkipFrontend,
  [switch]$SkipRuntime,
  [switch]$SkipInstaller
)
$ErrorActionPreference = "Stop"

# Windows PowerShell turns a native command's stderr into a terminating error
# while $ErrorActionPreference is Stop (vite and go both write progress to
# stderr). Run native commands with Continue and check their exit code.
function Invoke-Native {
  param([scriptblock]$Command, [string]$Name)
  $previous = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try { & $Command } finally { $ErrorActionPreference = $previous }
  $code = $LASTEXITCODE
  if ($code -ne 0) { throw "$Name failed (exit $code)" }
}

$buildRoot = $PSScriptRoot
$desktopRoot = Split-Path -Parent $buildRoot
$repoRoot = Split-Path -Parent $desktopRoot
$frontendDir = Join-Path $repoRoot "research-assistant\frontend"
$backendDir = Join-Path $repoRoot "research-assistant\backend"
$sandboxDir = Join-Path $repoRoot "research-assistant\docker-sandbox"
$webuiDir = Join-Path $desktopRoot "webui\dist"
$runtimeDir = Join-Path $desktopRoot "build\runtime"
$mygoDir = Join-Path $desktopRoot "build\mygo\windows-amd64"

if (-not $SkipFrontend) {
  Write-Host "building frontend"
  Invoke-Native { & npm --prefix $frontendDir run build } "frontend build"
  Remove-Item -Recurse -Force $webuiDir -ErrorAction SilentlyContinue
  New-Item -ItemType Directory -Force -Path $webuiDir | Out-Null
  Copy-Item -Recurse -Force (Join-Path $frontendDir "dist\*") $webuiDir
}

if (-not $SkipRuntime) {
  Write-Host "building backend.exe and sandbox.exe"
  New-Item -ItemType Directory -Force -Path $runtimeDir | Out-Null
  Push-Location $backendDir
  try {
    Invoke-Native { & $Go build -trimpath -ldflags "-s -w" -o (Join-Path $runtimeDir "backend.exe") ./cmd/api } "backend build"
  } finally { Pop-Location }
  Push-Location $sandboxDir
  try {
    Invoke-Native { & $Go build -trimpath -ldflags "-s -w" -o (Join-Path $runtimeDir "sandbox.exe") . } "sandbox build"
  } finally { Pop-Location }
  if ($env:UV_EXE -and (Test-Path $env:UV_EXE)) {
    Copy-Item $env:UV_EXE (Join-Path $runtimeDir "uv.exe")
  }
}

Write-Host "building ResearchAssistant.exe with MyGo"
$env:RA_APP_VERSION = $Version
$mygoConfig = Join-Path $desktopRoot "mygo.json"
$originalConfig = [IO.File]::ReadAllText($mygoConfig)
Push-Location $desktopRoot
try {
  # mygo build links the configuration version into the executable; keep it
  # in step with the installer version for this build.
  $config = $originalConfig | ConvertFrom-Json
  $config.version = $Version
  $json = $config | ConvertTo-Json -Depth 10
  [IO.File]::WriteAllText($mygoConfig, $json + "`n", [Text.UTF8Encoding]::new($false))
  Invoke-Native { & $Go run github.com/egoist/mygo/cmd/mygo@v0.7.4 build -skip-installer -skip-build-command . } "mygo build"
} finally {
  [IO.File]::WriteAllText($mygoConfig, $originalConfig, [Text.UTF8Encoding]::new($false))
  Pop-Location
}

if (-not $SkipInstaller) {
  & (Join-Path $buildRoot "package.ps1") -Version $Version -MyGoBin $mygoDir -RuntimeBin $runtimeDir -Output $Output
  if ($LASTEXITCODE -ne 0) { throw "package failed" }
}
Write-Host "desktop build complete"
