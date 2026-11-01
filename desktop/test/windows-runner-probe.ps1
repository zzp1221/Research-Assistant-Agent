param(
  [string]$PythonExe = "python",
  [string]$Workspace = "$env:TEMP\ra-probe",
  [string]$Output = "$env:TEMP\ra-probe-report.json"
)
$ErrorActionPreference = "Continue"
New-Item -ItemType Directory -Force -Path $Workspace | Out-Null
$cases = @()

function Add-Case([string]$Name, [bool]$Security, [bool]$Passed, [string]$Detail = "") {
  $script:cases += [ordered]@{ name = $Name; security = $Security; passed = $Passed; detail = $Detail }
}

try {
  & $PythonExe -c "import multiprocessing as m; assert m.Pool(2).map(len, ['a','bb']) == [1,2]"
  Add-Case "multiprocessing" $false ($LASTEXITCODE -eq 0)
} catch { Add-Case "multiprocessing" $false $false $_.Exception.Message }

try {
  & $PythonExe -c "import torch; from torch.utils.data import DataLoader; list(DataLoader(range(8), num_workers=2))"
  Add-Case "torch_dataloader" $false ($LASTEXITCODE -eq 0)
} catch { Add-Case "torch_dataloader" $false $false $_.Exception.Message }

try {
  & $PythonExe -m pip install --target "$Workspace\pip-target" six
  Add-Case "pip_install" $false ($LASTEXITCODE -eq 0)
} catch { Add-Case "pip_install" $false $false $_.Exception.Message }

try {
  & $PythonExe -c "import matplotlib.font_manager; matplotlib.font_manager._load_fontmanager(try_read_cache=False)"
  Add-Case "matplotlib_font" $false ($LASTEXITCODE -eq 0)
} catch { Add-Case "matplotlib_font" $false $false $_.Exception.Message }

$desktopFile = Join-Path ([Environment]::GetFolderPath("Desktop")) "ra_probe_escape.txt"
try {
  Set-Content -Path $desktopFile -Value "probe" -ErrorAction Stop
  Remove-Item $desktopFile -Force -ErrorAction SilentlyContinue
  Add-Case "workspace_escape" $true $false "desktop write unexpectedly succeeded"
} catch {
  Add-Case "workspace_escape" $true $true "write denied"
}

try {
  & $PythonExe -c "import socket; socket.create_connection(('1.1.1.1',53),timeout=3)"
  Add-Case "network_connect" $true ($LASTEXITCODE -ne 0) "connection returned"
} catch {
  Add-Case "network_connect" $true $true "connection denied"
}

try {
  $child = Start-Process -FilePath $PythonExe -ArgumentList "-c","import time; time.sleep(30)" -PassThru -WindowStyle Hidden
  $started = $child.Id
  Stop-Process -Id $child.Id -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 300
  $alive = Get-Process -Id $started -ErrorAction SilentlyContinue
  Add-Case "job_cleanup" $true ($null -eq $alive) "process $started"
} catch { Add-Case "job_cleanup" $true $false $_.Exception.Message }

$report = [ordered]@{
  generated_at = (Get-Date).ToString("o")
  python = $PythonExe
  workspace = $Workspace
  cases = $cases
  appcontainer_ok = ($cases | Where-Object { -not $_.passed }).Count -eq 0
}
$report | ConvertTo-Json -Depth 6 | Set-Content -Path $Output -Encoding UTF8
Write-Host "probe report written to $Output"
