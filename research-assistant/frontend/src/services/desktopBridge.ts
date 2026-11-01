import type { DesktopBridge } from '../contracts/desktop';

interface WailsAPI {
  NeedsOnboarding(): boolean | Promise<boolean>;
  CompleteOnboarding(credentials: unknown): void | Promise<void>;
  TestConnection(credentials: unknown): unknown;
  Notify(title: string, body: string): void | Promise<void>;
  ExportDiagnostics(): unknown;
  OpenLogs(): void | Promise<void>;
  ChooseDirectory(title: string): string | Promise<string>;
  ShowInFolder(path: string): void | Promise<void>;
  CheckForUpdate(): unknown;
  DownloadUpdate(version: string): unknown;
  InstallUpdate(path: string): void | Promise<void>;
  GetRuntimeSettings(): unknown;
  UpdateProvider(provider: string): void | Promise<void>;
  InstallRuntime(version: string): unknown;
  CleanupRuntimes(): unknown;
  Retry(): void | Promise<void>;
}

function wailsBridge(): DesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined;
  const api = (window as unknown as { go?: { main?: { API?: WailsAPI } } }).go?.main?.API;
  if (!api) return undefined;
  return {
    needsOnboarding: () => api.NeedsOnboarding(),
    completeOnboarding: (credentials) => api.CompleteOnboarding(credentials),
    testConnection: (credentials) => api.TestConnection(credentials) as ReturnType<DesktopBridge['testConnection']>,
    notify: (title, body) => api.Notify(title, body),
    exportDiagnostics: () => api.ExportDiagnostics() as ReturnType<DesktopBridge['exportDiagnostics']>,
    openLogs: () => api.OpenLogs(),
    chooseDirectory: (title = '') => api.ChooseDirectory(title),
    showInFolder: (path) => api.ShowInFolder(path),
    checkForUpdate: () => api.CheckForUpdate() as ReturnType<DesktopBridge['checkForUpdate']>,
    downloadUpdate: (version = '') => api.DownloadUpdate(version) as ReturnType<DesktopBridge['downloadUpdate']>,
    installUpdate: (path) => api.InstallUpdate(path),
    getRuntimeSettings: () => api.GetRuntimeSettings() as ReturnType<DesktopBridge['getRuntimeSettings']>,
    updateProvider: (provider) => api.UpdateProvider(provider),
    installRuntime: (version = '') => api.InstallRuntime(version) as ReturnType<DesktopBridge['installRuntime']>,
    cleanupRuntimes: () => api.CleanupRuntimes() as ReturnType<DesktopBridge['cleanupRuntimes']>,
    retry: () => api.Retry(),
  };
}

/** Returns the desktop bridge when the app runs inside the Wails shell. */
export function getDesktopBridge(): DesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined;
  return window.__RA_DESKTOP__ ?? wailsBridge();
}

/** Test helper: install a fake bridge and return a restore function. */
export function installDesktopBridge(bridgeValue: DesktopBridge | undefined): () => void {
  if (typeof window === 'undefined') return () => undefined;
  const previous = window.__RA_DESKTOP__;
  window.__RA_DESKTOP__ = bridgeValue;
  return () => {
    window.__RA_DESKTOP__ = previous;
  };
}