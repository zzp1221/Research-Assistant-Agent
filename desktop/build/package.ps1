param(
  [string]$Version = "0.9.0",
  [string]$Configuration = "Release",
  [string]$WailsBin = "",
  [string]$Output = "dist"
)
$ErrorActionPreference = "Stop"
$desktopRoot = Split-Path -Parent $PSScriptRoot
$staging = Join-Path $desktopRoot "build\staging"
Remove-Item -Recurse -Force $staging -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $staging | Out-Null

$required = @("ResearchAssistant.exe", "backend.exe", "sandbox.exe")
foreach ($file in $required) {
  $source = Join-Path $WailsBin $file
  if (-not (Test-Path $source)) { throw "missing build artifact: $source" }
  Copy-Item $source (Join-Path $staging $file)
}
$uv = Join-Path $WailsBin "uv.exe"
if (Test-Path $uv) { Copy-Item $uv (Join-Path $staging "uv.exe") }
$webview = Join-Path $WailsBin "webview"
if (-not (Test-Path $webview)) { throw "missing webview assets: $webview" }
Copy-Item $webview (Join-Path $staging "webview") -Recurse

Push-Location $staging
try {
  & makensis "/DVERSION=$Version" (Join-Path $PSScriptRoot "installer.nsi")
  if ($LASTEXITCODE -ne 0) { throw "makensis failed" }
  $setup = Join-Path $staging "ResearchAssistant-Setup.exe"
  if (-not (Test-Path $setup)) { throw "installer was not produced" }
  New-Item -ItemType Directory -Force -Path $Output | Out-Null
  Copy-Item $setup (Join-Path $Output "ResearchAssistant-$Version-Setup.exe")
} finally {
  Pop-Location
}
Write-Host "package written to $Output"
