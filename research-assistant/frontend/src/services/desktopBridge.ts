import type { DesktopBridge } from '../contracts/desktop';
import { API } from './mygo';

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

/**
 * The bridge of the MyGo shell: the generated client calls the Go service
 * bound with mygo.Bind. The runtime object only exists inside a MyGo window,
 * so a browser tab never reaches this branch.
 */
function mygoBridge(): DesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined;
  if (!(window as unknown as { mygo?: unknown }).mygo) return undefined;
  return {
    needsOnboarding: () => API.needsOnboarding(),
    completeOnboarding: (credentials) => API.completeOnboarding(credentials),
    testConnection: (credentials) => API.testConnection(credentials),
    notify: (title, body) => API.notify(title, body),
    exportDiagnostics: async () => ({ path: await API.exportDiagnostics() }),
    openLogs: () => API.openLogs(),
    chooseDirectory: (title = '') => API.chooseDirectory(title),
    showInFolder: (path) => API.showInFolder(path),
    checkForUpdate: () => API.checkForUpdate(),
    downloadUpdate: (version = '') => API.downloadUpdate(version),
    installUpdate: (path) => API.installUpdate(path),
    getRuntimeSettings: () => API.getRuntimeSettings(),
    updateProvider: (provider) => API.updateProvider(provider),
    installRuntime: (version = '') => API.installRuntime(version),
    cleanupRuntimes: () => API.cleanupRuntimes(),
    retry: () => API.retry(),
  };
}

/** Returns the desktop bridge when the app runs inside the MyGo or Wails shell. */
export function getDesktopBridge(): DesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined;
  return window.__RA_DESKTOP__ ?? mygoBridge() ?? wailsBridge();
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