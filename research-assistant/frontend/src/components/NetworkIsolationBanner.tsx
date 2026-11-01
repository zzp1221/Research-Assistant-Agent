import { useEffect, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import type { SandboxCapabilities } from '../contracts/api';
import { getSandboxCapabilities } from '../services/api/researchAssistantApi';

interface NetworkIsolationBannerProps {
  /** When provided, the banner renders from this value and skips fetching. */
  capabilities?: SandboxCapabilities | null;
}

/** Persistent warning for native/lowil mode where the OS does not enforce network isolation. */
export function NetworkIsolationBanner({ capabilities }: NetworkIsolationBannerProps) {
  const [loaded, setLoaded] = useState<SandboxCapabilities | null>(null);
  const effective = capabilities === undefined ? loaded : capabilities;

  useEffect(() => {
    if (capabilities !== undefined) return;
    let cancelled = false;
    void getSandboxCapabilities()
      .then((value) => {
        if (!cancelled) setLoaded(value);
      })
      .catch(() => {
        // Capability probing is advisory; a transient failure must not break the workbench.
      });
    return () => {
      cancelled = true;
    };
  }, [capabilities]);

  if (!effective || effective.provider !== 'native' || effective.network_enforced !== 'none') {
    return null;
  }

  return (
    <div role="status" className="flex items-center gap-2 border-b border-warn/40 bg-warn-soft px-3 py-1.5 text-[12px] text-warn">
      <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
      <span>当前为本机兼容模式：网络隔离未由操作系统强制，请只运行可信代码。</span>
    </div>
  );
}

export default NetworkIsolationBanner;
