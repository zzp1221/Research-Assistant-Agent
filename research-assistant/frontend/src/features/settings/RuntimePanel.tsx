import { useCallback, useEffect, useState } from 'react';
import { AlertTriangle, CheckCircle2, Cpu, Download, FolderOpen, HardDrive, RefreshCw, Shield } from 'lucide-react';
import type { SandboxCapabilities } from '../../contracts/api';
import type { DesktopRuntimeStatus, DesktopUpdateStatus } from '../../contracts/desktop';
import { getSandboxCapabilities } from '../../services/api/researchAssistantApi';
import { getDesktopMetrics, type DesktopMetrics } from '../../services/desktopMetrics';
import { getDesktopRuntimeStatus } from '../../services/desktopRuntime';
import { getDesktopBridge } from '../../services/desktopBridge';

interface RuntimePanelProps {
  onClose?: () => void;
}

const providers = [
  { id: 'auto', label: '自动（有 Docker 用 Docker）' },
  { id: 'native', label: '本机运行（无需 Docker）' },
  { id: 'docker', label: 'Docker 容器' },
];

/** Read-only runtime capability panel plus a local provider preference. */
export function RuntimePanel({ onClose }: RuntimePanelProps) {
  const [capabilities, setCapabilities] = useState<SandboxCapabilities | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [metrics, setMetrics] = useState<DesktopMetrics | null>(null);
  const [runtimeStatus, setRuntimeStatus] = useState<DesktopRuntimeStatus | null>(null);
  const [runtimeInstalling, setRuntimeInstalling] = useState(false);
  const [runtimeCleaning, setRuntimeCleaning] = useState(false);
  const [runtimeError, setRuntimeError] = useState('');
  const [diagnosticsPath, setDiagnosticsPath] = useState('');
  const [diagnosticsError, setDiagnosticsError] = useState('');
  const [repoPath, setRepoPath] = useState('');
  const [updateStatus, setUpdateStatus] = useState<DesktopUpdateStatus | null>(null);
  const [updateError, setUpdateError] = useState('');
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [downloadingUpdate, setDownloadingUpdate] = useState(false);
  const [provider, setProvider] = useState(() => {
    if (typeof window === 'undefined') return 'auto';
    return window.localStorage.getItem('ra.sandbox.provider') ?? 'auto';
  });

  const reload = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      setCapabilities(await getSandboxCapabilities());
    } catch (err) {
      setError(err instanceof Error ? err.message : '无法读取运行时能力');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  useEffect(() => {
    if (!runtimeInstalling) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      void getDesktopRuntimeStatus()
        .then((value) => {
          if (!cancelled) setRuntimeStatus(value);
        })
        .catch(() => {});
    }, 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [runtimeInstalling]);

  useEffect(() => {
    let cancelled = false;
    void getDesktopMetrics()
      .then((value) => {
        if (!cancelled) setMetrics(value);
      })
      .catch(() => {
        // Metrics are optional outside the desktop shell.
      });
    void getDesktopRuntimeStatus()
      .then((value) => {
        if (!cancelled) setRuntimeStatus(value);
      })
      .catch(() => {
        // Runtime status is only available in the desktop shell.
      });
    const bridge = getDesktopBridge();
    if (bridge) {
      void Promise.resolve(bridge.getRuntimeSettings())
        .then((value) => {
          if (!cancelled && value?.provider) setProvider(value.provider);
        })
        .catch(() => {});
    }
    return () => {
      cancelled = true;
    };
  }, []);

  const chooseProvider = async (id: string) => {
    setProvider(id);
    if (typeof window !== 'undefined') {
      window.localStorage.setItem('ra.sandbox.provider', id);
    }
    const bridge = getDesktopBridge();
    if (bridge) {
      try {
        await bridge.updateProvider(id);
      } catch (error) {
        setError(error instanceof Error ? error.message : '保存运行模式失败');
      }
    }
  };

  const prepareRuntime = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) {
      setRuntimeError('仅桌面版支持准备 Python 运行时');
      return;
    }
    setRuntimeInstalling(true);
    setRuntimeError('');
    try {
      setRuntimeStatus(await bridge.installRuntime('3.11'));
    } catch (error) {
      setRuntimeError(error instanceof Error ? error.message : '准备 Python 运行时失败');
    } finally {
      setRuntimeInstalling(false);
    }
  };

  const cleanupRuntime = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) {
      setRuntimeError('仅桌面版支持清理运行时缓存');
      return;
    }
    if (typeof window !== 'undefined' && !window.confirm('清理后会删除已安装的 Python 运行时与 venv 缓存，确定继续？')) {
      return;
    }
    setRuntimeCleaning(true);
    setRuntimeError('');
    try {
      setRuntimeStatus(await bridge.cleanupRuntimes());
    } catch (error) {
      setRuntimeError(error instanceof Error ? error.message : '清理运行时缓存失败');
    } finally {
      setRuntimeCleaning(false);
    }
  };

  const exportDiagnostics = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) {
      setDiagnosticsError('仅桌面版支持导出诊断包');
      return;
    }
    try {
      const result = await bridge.exportDiagnostics();
      setDiagnosticsPath(result.path);
      setDiagnosticsError('');
    } catch (error) {
      setDiagnosticsError(error instanceof Error ? error.message : '导出诊断包失败');
    }
  };

  const openLogs = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) {
      setDiagnosticsError('仅桌面版支持打开日志目录');
      return;
    }
    try {
      await bridge.openLogs();
      setDiagnosticsError('');
    } catch (error) {
      setDiagnosticsError(error instanceof Error ? error.message : '打开日志目录失败');
    }
  };

  const chooseRepo = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) return;
    const selected = await bridge.chooseDirectory('选择仓库目录');
    if (selected) setRepoPath(selected);
  };

  const showRepo = async () => {
    if (!repoPath) return;
    await getDesktopBridge()?.showInFolder(repoPath);
  };

  const checkUpdate = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) {
      setUpdateError('仅桌面版支持更新检查');
      return;
    }
    setCheckingUpdate(true);
    setUpdateError('');
    try {
      setUpdateStatus(await bridge.checkForUpdate());
    } catch (error) {
      setUpdateError(error instanceof Error ? error.message : '检查更新失败');
    } finally {
      setCheckingUpdate(false);
    }
  };

  const downloadUpdate = async () => {
    const bridge = getDesktopBridge();
    if (!bridge) return;
    setDownloadingUpdate(true);
    setUpdateError('');
    try {
      setUpdateStatus(await bridge.downloadUpdate(updateStatus?.version ?? ''));
    } catch (error) {
      setUpdateError(error instanceof Error ? error.message : '下载更新失败');
    } finally {
      setDownloadingUpdate(false);
    }
  };

  const installUpdate = async () => {
    const bridge = getDesktopBridge();
    if (!bridge || !updateStatus?.path) return;
    setUpdateError('');
    try {
      await bridge.installUpdate(updateStatus.path);
    } catch (error) {
      setUpdateError(error instanceof Error ? error.message : '启动安装器失败');
    }
  };

  return (
    <div className="flex h-full flex-col overflow-auto bg-paper p-4">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h2 className="text-[14px] font-semibold text-ink">运行模式</h2>
          <p className="mt-1 text-[12px] text-ink-soft">选择沙箱执行档位。桌面版会在启动时把该偏好注入沙箱进程。</p>
        </div>
        {onClose && (
          <button type="button" onClick={onClose} className="border border-line px-2 py-1 text-[12px] text-ink-soft hover:border-line-strong">
            关闭
          </button>
        )}
      </div>

      {error && (
        <div className="mb-3 flex items-start gap-2 border border-proof/40 bg-proof-soft p-2 text-[12px] text-proof">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          {error}
        </div>
      )}

      <section className="mb-4 border border-line bg-surface p-3">
        <h3 className="mb-2 flex items-center gap-1 text-[12px] font-semibold text-ink"><Shield className="h-3.5 w-3.5" /> 当前能力</h3>
        {loading ? (
          <p className="text-[12px] text-ink-soft">正在读取…</p>
        ) : (
          <dl className="grid grid-cols-2 gap-2 text-[12px]">
            <div><dt className="text-ink-soft">Provider</dt><dd className="font-medium text-ink">{capabilities?.provider ?? 'unknown'}</dd></div>
            <div><dt className="text-ink-soft">隔离</dt><dd className="font-medium text-ink">{capabilities?.isolation ?? 'unknown'}</dd></div>
            <div><dt className="text-ink-soft">网络强制</dt><dd className="font-medium text-ink">{capabilities?.network_enforced ?? 'unknown'}</dd></div>
            <div><dt className="text-ink-soft">系统</dt><dd className="font-medium text-ink">{capabilities?.os ?? 'unknown'}</dd></div>
            <div className="col-span-2"><dt className="text-ink-soft">Python</dt><dd className="font-medium text-ink">{capabilities?.python_versions?.join(', ') || '由运行时按需下载'}</dd></div>
          </dl>
        )}
        <button type="button" onClick={() => void reload()} className="mt-3 inline-flex items-center gap-1 border border-line px-2 py-1 text-[12px] text-ink-soft hover:border-line-strong">
          <RefreshCw className="h-3 w-3" /> 重新检测
        </button>
      </section>

      {capabilities?.probe && (
        <section className="mb-4 border border-line bg-surface p-3">
          <h3 className="mb-2 flex items-center gap-1 text-[12px] font-semibold text-ink"><Shield className="h-3.5 w-3.5" /> AppContainer 兼容性探针</h3>
          <p className={capabilities.probe.appcontainer_ok ? 'text-[12px] text-ok' : 'text-[12px] text-warn'}>
            {capabilities.probe.appcontainer_ok ? '全部通过：可以使用 AppContainer' : '未通过：自动降级到 lowil（兼容模式）'}
          </p>
          {capabilities.probe.reason && <p className="mt-1 text-[11px] text-ink-soft">{capabilities.probe.reason}</p>}
          <ul className="mt-2 space-y-1 text-[11px] text-ink-soft">
            {(capabilities.probe.cases ?? []).map((item) => (
              <li key={item.name} className="flex items-center gap-1">
                <span className={item.passed ? 'text-ok' : 'text-proof'}>{item.passed ? '✓' : '✕'}</span>
                <span>{item.name}</span>
                {item.security ? <span className="text-ink-soft">（安全项）</span> : null}
                {item.detail && !item.passed ? <span className="truncate text-ink-soft">— {item.detail}</span> : null}
              </li>
            ))}
          </ul>
        </section>
      )}

      {capabilities?.provider === 'docker' && provider !== 'docker' && (
        <div className="mb-4 flex items-start gap-2 border border-proof/40 bg-proof-soft p-2 text-[12px] text-proof">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span className="flex-1">检测到 Docker，建议使用容器模式以获得更强的隔离。</span>
          <button type="button" onClick={() => void chooseProvider('docker')} className="border border-proof/40 px-2 py-0.5 hover:border-proof">切换到 Docker</button>
        </div>
      )}

      {capabilities?.network_enforced === 'none' && (
        <div className="mb-4 flex items-start gap-2 border border-warn/40 bg-warn-soft p-2 text-[12px] text-warn">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          当前为兼容模式：网络隔离未由操作系统强制，请只运行可信代码。
        </div>
      )}

      <section className="mb-4">
        <h3 className="mb-2 flex items-center gap-1 text-[12px] font-semibold text-ink"><Cpu className="h-3.5 w-3.5" /> 运行模式偏好</h3>
        <div className="space-y-1">
          {providers.map((entry) => (
            <button
              key={entry.id}
              type="button"
              onClick={() => void chooseProvider(entry.id)}
              aria-pressed={provider === entry.id}
              className={provider === entry.id
                ? 'flex w-full items-center gap-2 border border-ink bg-ink px-2 py-1.5 text-left text-[12px] font-medium text-paper'
                : 'flex w-full items-center gap-2 border border-line bg-surface px-2 py-1.5 text-left text-[12px] text-ink-soft hover:border-line-strong'}
            >
              {provider === entry.id ? <CheckCircle2 className="h-3.5 w-3.5" /> : <span className="h-3.5 w-3.5" />}
              {entry.label}
            </button>
          ))}
        </div>
      </section>

      <section className="mb-4 border border-line bg-surface p-3 text-[12px] text-ink-soft">
        <h3 className="mb-2 flex items-center gap-1 font-semibold text-ink"><FolderOpen className="h-3.5 w-3.5" /> 仓库目录</h3>
        <p className="mb-2 break-all">{repoPath || '未选择；导出或导入时可使用系统目录选择器。'}</p>
        <div className="flex gap-2">
          <button type="button" onClick={() => void chooseRepo()} className="border border-line px-2 py-1 hover:border-line-strong">选择仓库目录</button>
          <button type="button" onClick={() => void showRepo()} disabled={!repoPath} className="border border-line px-2 py-1 hover:border-line-strong disabled:opacity-40">在文件夹中显示</button>
        </div>
      </section>

      <section className="mb-4 border border-line bg-surface p-3 text-[12px] text-ink-soft">
        <h3 className="mb-2 flex items-center gap-1 font-semibold text-ink"><Download className="h-3.5 w-3.5" /> 更新</h3>
        <p className="mb-2">
          {updateStatus
            ? updateStatus.available
              ? `发现新版本 ${updateStatus.version ?? ''}`
              : '当前已是最新版本'
            : '检查是否有新的签名版本。'}
        </p>
        {updateStatus?.path && <p className="mb-2 break-all">已下载：{updateStatus.path}</p>}
        {updateError && <p className="mb-2 text-proof">{updateError}</p>}
        <div className="flex gap-2">
          <button type="button" onClick={() => void checkUpdate()} disabled={checkingUpdate} className="border border-line px-2 py-1 hover:border-line-strong disabled:opacity-40">
            {checkingUpdate ? '检查中…' : '检查更新'}
          </button>
          <button type="button" onClick={() => void downloadUpdate()} disabled={!updateStatus?.available || downloadingUpdate} className="border border-line px-2 py-1 hover:border-line-strong disabled:opacity-40">
            {downloadingUpdate ? '下载中…' : '下载并校验'}
          </button>
          <button type="button" onClick={() => void installUpdate()} disabled={!updateStatus?.path} className="border border-line px-2 py-1 hover:border-line-strong disabled:opacity-40">
            安装并重启
          </button>
        </div>
      </section>

      <section className="mb-4 border border-line bg-surface p-3 text-[12px] text-ink-soft">
        <h3 className="mb-2 flex items-center gap-1 font-semibold text-ink"><HardDrive className="h-3.5 w-3.5" /> 本地指标</h3>
        {metrics ? (
          <dl className="grid grid-cols-2 gap-2">
            <div><dt className="text-ink-soft">Goroutines</dt><dd className="font-medium text-ink">{metrics.goroutines}</dd></div>
            <div><dt className="text-ink-soft">常驻内存</dt><dd className="font-medium text-ink">{Math.round(metrics.resident_memory_bytes / 1024 / 1024)} MB</dd></div>
            <div><dt className="text-ink-soft">HTTP 请求</dt><dd className="font-medium text-ink">{metrics.request_count}</dd></div>
            <div><dt className="text-ink-soft">采集时间</dt><dd className="font-medium text-ink">{metrics.collected_at}</dd></div>
          </dl>
        ) : (
          <p>本地指标暂不可用（仅桌面外壳提供）。</p>
        )}
      </section>

      <section className="mb-4 border border-line bg-surface p-3 text-[12px] text-ink-soft">
        <h3 className="mb-2 flex items-center gap-1 font-semibold text-ink"><HardDrive className="h-3.5 w-3.5" /> 运行时状态</h3>
        {runtimeStatus?.supported ? (
          <dl className="grid grid-cols-2 gap-2">
            <div className="col-span-2"><dt className="text-ink-soft">Python</dt><dd className="font-medium text-ink">{runtimeStatus.python_versions?.join(', ') || '未安装'}</dd></div>
            <div><dt className="text-ink-soft">Venv 缓存</dt><dd className="font-medium text-ink">{runtimeStatus.venv_count ?? 0}</dd></div>
            <div><dt className="text-ink-soft">磁盘占用</dt><dd className="font-medium text-ink">{Math.round(runtimeStatus.disk_used_mb ?? 0)} MB</dd></div>
          </dl>
        ) : (
          <p>当前 provider 不提供本机运行时缓存（{runtimeStatus?.provider ?? provider}）。</p>
        )}
        {runtimeInstalling && <p className="mt-2 text-ink-soft">阶段：{runtimeStatus?.stage ?? 'starting'}</p>}
        {runtimeError && <p className="mt-2 text-proof">{runtimeError}</p>}
        <div className="mt-3 flex gap-2">
          <button type="button" onClick={() => void prepareRuntime()} disabled={runtimeInstalling || runtimeCleaning} className="border border-line px-2 py-1 hover:border-line-strong disabled:opacity-40">
            {runtimeInstalling ? '正在准备运行时…' : '准备 Python 运行时'}
          </button>
          <button type="button" onClick={() => void cleanupRuntime()} disabled={runtimeInstalling || runtimeCleaning} className="border border-line px-2 py-1 hover:border-line-strong disabled:opacity-40">
            {runtimeCleaning ? '正在清理…' : '清理运行时缓存'}
          </button>
        </div>
      </section>

      <section className="mb-4 border border-line bg-surface p-3 text-[12px] text-ink-soft">
        <h3 className="mb-2 flex items-center gap-1 font-semibold text-ink"><HardDrive className="h-3.5 w-3.5" /> 诊断</h3>
        {diagnosticsPath && <p className="mb-2 break-all">诊断包：{diagnosticsPath}</p>}
        {diagnosticsError && <p className="mb-2 text-proof">{diagnosticsError}</p>}
        <div className="flex gap-2">
          <button type="button" onClick={() => void exportDiagnostics()} className="border border-line px-2 py-1 hover:border-line-strong">导出诊断包</button>
          <button type="button" onClick={() => void openLogs()} className="border border-line px-2 py-1 hover:border-line-strong">打开日志目录</button>
        </div>
      </section>

    </div>
  );
}

export default RuntimePanel;
