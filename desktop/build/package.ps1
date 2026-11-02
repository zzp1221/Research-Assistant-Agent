param(
  [string]$Version = "0.9.0",
  [string]$Configuration = "Release",
  [string]$MyGoBin = "",
  [string]$RuntimeBin = "",
  [string]$Output = "dist"
)
$ErrorActionPreference = "Stop"
$desktopRoot = Split-Path -Parent $PSScriptRoot
if (-not $MyGoBin) { $MyGoBin = Join-Path $desktopRoot "build\mygo\windows-amd64" }
if (-not $RuntimeBin) {
  $RuntimeBin = Join-Path $desktopRoot "build\runtime"
  if (-not (Test-Path $RuntimeBin)) { $RuntimeBin = $MyGoBin }
}
if (-not [IO.Path]::IsPathRooted($Output)) { $Output = Join-Path $desktopRoot $Output }
$staging = Join-Path $desktopRoot "build\staging"
Remove-Item -Recurse -Force $staging -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $staging | Out-Null

# The MyGo build produces the window executable; backend.exe, sandbox.exe and
# uv.exe come from the runtime build and are shipped next to it by NSIS.
$fromMyGo = @("ResearchAssistant.exe")
foreach ($file in $fromMyGo) {
  $source = Join-Path $MyGoBin $file
  if (-not (Test-Path $source)) { throw "missing build artifact: $source" }
  Copy-Item $source (Join-Path $staging $file)
}
$fromRuntime = @("backend.exe", "sandbox.exe")
foreach ($file in $fromRuntime) {
  $source = Join-Path $RuntimeBin $file
  if (-not (Test-Path $source)) { throw "missing runtime artifact: $source" }
  Copy-Item $source (Join-Path $staging $file)
}
$uv = Join-Path $RuntimeBin "uv.exe"
if (Test-Path $uv) { Copy-Item $uv (Join-Path $staging "uv.exe") }

Copy-Item (Join-Path $PSScriptRoot "installer.nsi") (Join-Path $staging "installer.nsi")

Push-Location $staging
try {
  $previous = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try { & makensis "/DVERSION=$Version" "installer.nsi" } finally { $ErrorActionPreference = $previous }
  if ($LASTEXITCODE -ne 0) { throw "makensis failed" }
  $setup = Join-Path $staging "ResearchAssistant-Setup.exe"
  if (-not (Test-Path $setup)) { throw "installer was not produced" }
  New-Item -ItemType Directory -Force -Path $Output | Out-Null
  Copy-Item $setup (Join-Path $Output "ResearchAssistant-$Version-Setup.exe")
} finally {
  Pop-Location
}
Write-Host "package written to $Output"
