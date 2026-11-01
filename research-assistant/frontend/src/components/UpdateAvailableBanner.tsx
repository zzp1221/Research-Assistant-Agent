import { Download, X } from 'lucide-react';
import { useUpdateAvailable, type UpdateAvailablePayload } from '../app/hooks/useUpdateAvailable';

interface UpdateAvailableBannerProps {
  /** When provided, renders from this value and skips the Wails event hook. */
  update?: UpdateAvailablePayload | null;
  onDismiss?: () => void;
}

/** Shows the signed update notification emitted by the desktop shell. */
export function UpdateAvailableBanner({ update, onDismiss }: UpdateAvailableBannerProps) {
  const live = useUpdateAvailable();
  const effective = update === undefined ? live.update : update;
  if (!effective) return null;
  const dismiss = onDismiss ?? live.dismiss;
  return (
    <div role="status" className="flex items-center gap-2 border-b border-proof/40 bg-proof-soft px-3 py-1.5 text-[12px] text-proof">
      <Download className="h-3.5 w-3.5 shrink-0" />
      <span>发现新版本 {effective.version ?? ''}，可在设置 → 运行模式中检查并下载。</span>
      {effective.release_notes_url && (
        <a href={effective.release_notes_url} target="_blank" rel="noreferrer" className="underline">
          Release Notes
        </a>
      )}
      <button type="button" aria-label="关闭更新提示" onClick={dismiss} className="ml-auto text-proof/80 hover:text-proof">
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

export default UpdateAvailableBanner;
