param(
  [string]$Version = "0.9.0",
  [string]$CertPath = "",
  [string]$CertPassword = "",
  [string]$TimestampUrl = "http://timestamp.digicert.com"
)
$ErrorActionPreference = "Stop"
if (-not (Test-Path $CertPath)) { throw "certificate not found: $CertPath" }
$files = @("ResearchAssistant.exe", "backend.exe", "sandbox.exe", "uv.exe")
foreach ($file in $files) {
  if (Test-Path $file) {
    Write-Host "signing $file"
    & signtool sign /fd SHA256 /tr $TimestampUrl /td SHA256 /f $CertPath /p $CertPassword $file
    if ($LASTEXITCODE -ne 0) { throw "signtool failed for $file" }
  }
}
if (Test-Path "ResearchAssistant-Setup.exe") {
  Write-Host "signing ResearchAssistant-Setup.exe"
  & signtool sign /fd SHA256 /tr $TimestampUrl /td SHA256 /f $CertPath /p $CertPassword "ResearchAssistant-Setup.exe"
  if ($LASTEXITCODE -ne 0) { throw "signtool failed for installer" }
}
Write-Host "signed ResearchAssistant $Version"
