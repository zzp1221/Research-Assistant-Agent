# Windows real-machine regression

[简体中文](README.md) | English

Linux containers can only verify pure logic and cross-compilation. The scripts below must run on a clean Windows 11 machine.

## 1. AppContainer compatibility probe

```powershell
powershell -ExecutionPolicy Bypass -File windows-runner-probe.ps1 -PythonExe "C:\path\to\python.exe"
```

Outputs `ra-probe-report.json` with 7 cases matching design appendix D:

1. `multiprocessing.Pool(2)`
2. torch `DataLoader(num_workers=2)`
3. `pip install --target ... six`
4. matplotlib font cache
5. Writing to the desktop must be denied (security)
6. Accessing 1.1.1.1:53 must be denied (security)
7. Child-process reaping

Only enable `appcontainer` if all pass; on any failure, downgrade to `lowil` per the design and record the reason.

## 2. Launch/exit residue check

```powershell
powershell -ExecutionPolicy Bypass -File windows-launch.ps1 -InstallDir "$env:LOCALAPPDATA\Programs\ResearchAssistant"
```

Verify that `backend.exe` / `sandbox.exe` exist after the window starts, and that no processes remain after exit.

## 3. Other real-machine checks

- DPAPI `secrets.dat` encryption/decryption and masked display
- systray menu/double-click showing the main window
- NSIS silent install/uninstall retaining data
- SmartScreen behavior after `signtool` signing
