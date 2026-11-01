param(
  [string]$InstallDir = "$env:LOCALAPPDATA\Programs\ResearchAssistant",
  [int]$TimeoutSeconds = 60
)
$ErrorActionPreference = "Stop"
$exe = Join-Path $InstallDir "ResearchAssistant.exe"
if (-not (Test-Path $exe)) { throw "not found: $exe" }
$before = Get-Process -Name ResearchAssistant,backend,sandbox -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id
$process = Start-Process -FilePath $exe -PassThru
$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
while ((Get-Date) -lt $deadline) {
  Start-Sleep -Milliseconds 500
  $children = Get-Process -Name backend,sandbox -ErrorAction SilentlyContinue
  if ($children) { break }
}
$children = Get-Process -Name backend,sandbox -ErrorAction SilentlyContinue
if (-not $children) { throw "backend/sandbox did not start within $TimeoutSeconds seconds" }
Write-Host "started shell=$($process.Id) children=$($children.Id -join ',')"
Stop-Process -Id $process.Id -Force
Start-Sleep -Seconds 3
$residual = Get-Process -Name ResearchAssistant,backend,sandbox -ErrorAction SilentlyContinue | Where-Object { $before -notcontains $_.Id }
if ($residual) {
  Write-Error "residual processes: $($residual.Id -join ',')"
  exit 1
}
Write-Host "no residual processes"
