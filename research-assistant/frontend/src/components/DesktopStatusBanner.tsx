import { useEffect, useState } from 'react';
import { AlertTriangle, X } from 'lucide-react';

export interface DesktopState {
  state: string;
  restarts?: number;
  error?: string;
  last_restart_at?: string;
}

interface DesktopStatusBannerProps {
  /** When provided, the banner renders from this value and skips fetching. */
  state?: DesktopState | null;
}

/** Warns when the desktop services restarted or failed to start. */
export function DesktopStatusBanner({ state }: DesktopStatusBannerProps) {
  const [loaded, setLoaded] = useState<DesktopState | null>(null);
  const [dismissed, setDismissed] = useState(false);
  const effective = state === undefined ? loaded : state;

  useEffect(() => {
    if (state !== undefined) return;
    let cancelled = false;
    const load = async () => {
      try {
        const response = await fetch('/desktop/state', { headers: { Accept: 'application/json' } });
        if (!response.ok) return;
        const value = (await response.json()) as DesktopState;
        if (!cancelled) setLoaded(value);
      } catch {
        // The shell is optional in browser mode.
      }
    };
    void load();
    const timer = window.setInterval(load, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [state]);

  if (dismissed || !effective) return null;
  const failed = effective.state === 'failed';
  const restarted = (effective.restarts ?? 0) > 0;
  if (!failed && !restarted) return null;

  return (
    <div role="status" className="flex items-center gap-2 border-b border-warn/40 bg-warn-soft px-3 py-1.5 text-[12px] text-warn">
      <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
      <span>
        {failed
          ? `本地服务启动失败：${effective.error ?? '未知错误'}`
          : '本地服务已重启，进行中的计划已中断，请检查计划状态。'}
      </span>
      <button type="button" aria-label="关闭提示" onClick={() => setDismissed(true)} className="ml-auto text-warn/80 hover:text-warn">
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

export default DesktopStatusBanner;
