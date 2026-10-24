import { useEffect, useState } from 'react';
import { ShieldAlert, X } from 'lucide-react';

interface FullAccessConfirmDialogProps {
  open: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

export function FullAccessConfirmDialog({ open, onCancel, onConfirm }: FullAccessConfirmDialogProps) {
  const [acknowledged, setAcknowledged] = useState(false);
  useEffect(() => {
    if (!open) setAcknowledged(false);
  }, [open]);
  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/35 p-4" role="presentation">
      <div role="dialog" aria-modal="true" aria-labelledby="full-access-title" className="w-full max-w-md rounded-[10px] border border-line bg-surface p-5 shadow-xl">
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-start gap-2">
            <ShieldAlert className="mt-0.5 h-5 w-5 shrink-0 text-flag" />
            <div>
              <h2 id="full-access-title" className="font-serif text-[17px] font-semibold text-ink">启用完全权限</h2>
              <p className="mt-1 text-[12px] leading-5 text-muted">
                系统会自动拉取镜像、安装依赖、下载公开数据/模型并使用 GPU（如需要）。仍受镜像白名单、磁盘与运行时长上限约束。
              </p>
            </div>
          </div>
          <button type="button" onClick={onCancel} aria-label="关闭完全权限确认" className="rounded-[4px] p-1 text-muted hover:bg-surface-muted hover:text-ink">
            <X className="h-4 w-4" />
          </button>
        </div>
        <label className="mt-4 flex items-start gap-2 rounded-[6px] border border-flag/25 bg-flag-soft px-3 py-2 text-[12px] leading-5 text-ink-soft">
          <input
            type="checkbox"
            checked={acknowledged}
            onChange={(event) => setAcknowledged(event.target.checked)}
            className="mt-1 h-3.5 w-3.5 accent-[var(--accent)]"
          />
          <span>我理解并允许系统在当前计划中自动执行上述操作；仅在我信任该论文仓库时使用。</span>
        </label>
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onCancel} className="h-8 rounded-[5px] border border-line px-3 text-[12.5px] font-medium text-ink-soft hover:bg-surface-muted">取消</button>
          <button
            type="button"
            disabled={!acknowledged}
            onClick={() => { setAcknowledged(false); onConfirm(); }}
            className="h-8 rounded-[5px] bg-accent px-3 text-[12.5px] font-semibold text-white hover:bg-accent-strong disabled:cursor-not-allowed disabled:bg-line disabled:text-faint"
          >
            确认完全权限
          </button>
        </div>
      </div>
    </div>
  );
}
