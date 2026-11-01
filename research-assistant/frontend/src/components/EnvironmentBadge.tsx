import type { EnvironmentManifestView } from '../contracts/api';

interface EnvironmentBadgeProps {
  manifest?: EnvironmentManifestView | null;
  className?: string;
}

const labelFor = (manifest: EnvironmentManifestView): string => {
  const provider = manifest.sandbox_provider ?? 'docker';
  if (provider === 'docker') return '容器';
  if (provider === 'native') {
    return manifest.isolation === 'appcontainer' ? '原生(AppContainer)' : '原生(兼容)';
  }
  if (provider === 'vm') return '虚拟机';
  if (provider === 'remote') return '远程沙箱';
  return provider;
};

const toneFor = (manifest: EnvironmentManifestView): string => {
  const provider = manifest.sandbox_provider ?? 'docker';
  if (provider === 'docker') return 'border-ok/40 bg-ok-soft text-ok';
  if (provider === 'native' && manifest.isolation === 'appcontainer') return 'border-proof/40 bg-proof-soft text-proof';
  return 'border-warn/40 bg-warn-soft text-warn';
};

/** Environment fidelity badge for plan details and lists. */
export function EnvironmentBadge({ manifest, className = '' }: EnvironmentBadgeProps) {
  if (!manifest || (!manifest.sandbox_provider && !manifest.isolation)) return null;
  const title = [
    manifest.sandbox_provider ? `provider: ${manifest.sandbox_provider}` : '',
    manifest.isolation ? `isolation: ${manifest.isolation}` : '',
    manifest.runtime_hash ? `runtime: ${manifest.runtime_hash.slice(0, 12)}` : '',
    manifest.network_enforced ? `network: ${manifest.network_enforced}` : '',
  ].filter(Boolean).join(' · ');
  return (
    <span
      className={`inline-flex items-center gap-1 border px-1.5 py-0.5 text-[11px] font-medium ${toneFor(manifest)} ${className}`}
      title={title}
    >
      {labelFor(manifest)}
    </span>
  );
}

export default EnvironmentBadge;
