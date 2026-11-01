# Windows 真机回归

当前 Linux 容器只能验证纯逻辑与交叉编译。以下脚本需要在干净的 Windows 11 上执行：

## 1. AppContainer 兼容性探针

```powershell
powershell -ExecutionPolicy Bypass -File windows-runner-probe.ps1 -PythonExe "C:\path\to\python.exe"
```

输出 `ra-probe-report.json`，7 项与设计文档附录 D 一致：

1. `multiprocessing.Pool(2)`
2. torch `DataLoader(num_workers=2)`
3. `pip install --target ... six`
4. matplotlib 字体缓存
5. 写桌面文件必须被拒绝（安全项）
6. 访问 1.1.1.1:53 必须被拒绝（安全项）
7. 子进程回收

全部通过才应启用 `appcontainer`；任一失败按文档降级到 `lowil` 并记录原因。

## 2. 启动/退出残留检查

```powershell
powershell -ExecutionPolicy Bypass -File windows-launch.ps1 -InstallDir "$env:LOCALAPPDATA\Programs\ResearchAssistant"
```

验证：窗口启动后 `backend.exe`/`sandbox.exe` 存在；退出后没有残留进程。

## 3. 其他真机项

- DPAPI `secrets.dat` 加解密与掩码显示
- systray 菜单/双击显示主窗口
- NSIS 静默安装/卸载保留数据
- `signtool` 签名后的 SmartScreen 行为
