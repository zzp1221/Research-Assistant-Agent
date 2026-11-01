export interface DesktopCredentials {
  baseUrl: string;
  model: string;
  apiKey: string;
  provider?: string;
}

export interface DesktopConnectionResult {
  ok: boolean;
  error?: string;
}

export interface DesktopDiagnosticsResult {
  path: string;
}

export interface DesktopRuntimeSettings {
  provider: string;
  network_default?: string;
  windows_runner?: string;
}

export interface DesktopRuntimeStatus {
  supported: boolean;
  provider?: string;
  python_versions?: string[];
  venv_count?: number;
  disk_used_mb?: number;
  installing?: boolean;
  stage?: string;
  install_error?: string;
  error?: string;
}

export interface DesktopUpdateStatus {
  available: boolean;
  version?: string;
  release_notes_url?: string;
  path?: string;
}

/**
 * The bridge injected by the Wails shell. It is absent in a normal browser, so
 * every call site must treat it as optional.
 */
export interface DesktopBridge {
  needsOnboarding(): boolean | Promise<boolean>;
  completeOnboarding(credentials: DesktopCredentials): void | Promise<void>;
  testConnection(credentials: DesktopCredentials): DesktopConnectionResult | Promise<DesktopConnectionResult>;
  notify(title: string, body: string): void | Promise<void>;
  exportDiagnostics(): DesktopDiagnosticsResult | Promise<DesktopDiagnosticsResult>;
  openLogs(): void | Promise<void>;
  chooseDirectory(title?: string): string | Promise<string>;
  showInFolder(path: string): void | Promise<void>;
  checkForUpdate(): DesktopUpdateStatus | Promise<DesktopUpdateStatus>;
  downloadUpdate(version?: string): DesktopUpdateStatus | Promise<DesktopUpdateStatus>;
  installUpdate(path: string): void | Promise<void>;
  getRuntimeSettings(): DesktopRuntimeSettings | Promise<DesktopRuntimeSettings>;
  updateProvider(provider: string): void | Promise<void>;
  installRuntime(version?: string): DesktopRuntimeStatus | Promise<DesktopRuntimeStatus>;
  cleanupRuntimes(): DesktopRuntimeStatus | Promise<DesktopRuntimeStatus>;
  retry(): void | Promise<void>;
}

declare global {
  interface Window {
    __RA_DESKTOP__?: DesktopBridge;
  }
}