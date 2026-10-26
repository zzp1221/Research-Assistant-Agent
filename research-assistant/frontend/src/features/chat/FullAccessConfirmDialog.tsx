import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { ShieldAlert, X } from 'lucide-react';

interface FullAccessConfirmDialogProps {
  open: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}

const FOCUSABLE_SELECTOR = [
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'a[href]',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

const risks = [
  { key: '镜像', value: '按白名单拉取固定 digest 的镜像' },
  { key: '依赖', value: '在沙箱内自动安装 Python / 系统依赖' },
  { key: '数据', value: '下载论文引用的公开数据集与模型权重' },
  { key: '算力', value: '按计划声明使用 GPU 与网络访问' },
];

export function FullAccessConfirmDialog({ open, onCancel, onConfirm }: FullAccessConfirmDialogProps) {
  const [acknowledged, setAcknowledged] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  const onCancelRef = useRef(onCancel);

  useEffect(() => {
    onCancelRef.current = onCancel;
  }, [onCancel]);

  useEffect(() => {
    if (!open || typeof document === 'undefined') return;
    previouslyFocusedRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;

    const body = document.body;
    const root = document.getElementById('root') ?? document.querySelector<HTMLElement>('.research-assistant-app-shell');
    const previousOverflow = body.style.overflow;
    const rootWasInert = root?.hasAttribute('inert') ?? false;
    const previousAriaHidden = root?.getAttribute('aria-hidden') ?? null;
    body.style.overflow = 'hidden';
    if (root) {
      root.setAttribute('inert', '');
      root.setAttribute('aria-hidden', 'true');
    }

    const focusTimer = window.setTimeout(() => {
      closeButtonRef.current?.focus();
    }, 0);

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        setAcknowledged(false);
        onCancelRef.current();
        return;
      }
      if (event.key !== 'Tab') return;
      const dialog = dialogRef.current;
      if (!dialog) return;
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR));
      if (focusable.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      const active = document.activeElement;
      if (!dialog.contains(active)) {
        event.preventDefault();
        first.focus();
        return;
      }
      if (event.shiftKey && active === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.clearTimeout(focusTimer);
      document.removeEventListener('keydown', handleKeyDown, true);
      body.style.overflow = previousOverflow;
      if (root) {
        if (!rootWasInert) root.removeAttribute('inert');
        if (previousAriaHidden === null) {
          root.removeAttribute('aria-hidden');
        } else {
          root.setAttribute('aria-hidden', previousAriaHidden);
        }
      }
      previouslyFocusedRef.current?.focus?.();
    };
  }, [open]);

  if (!open || typeof document === 'undefined') return null;

  return createPortal(
    <div
      className="fixed inset-0 flex items-center justify-center bg-ink/45 p-4"
      style={{ zIndex: 2000 }}
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          setAcknowledged(false);
          onCancel();
        }
      }}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="full-access-title"
        aria-describedby="full-access-description"
        tabIndex={-1}
        className="ra-fade-up w-full max-w-md border border-line-strong bg-surface outline-none shadow-float"
      >
        <div className="flex items-start justify-between gap-3 border-b border-line bg-surface-muted px-4 py-3">
          <div className="flex items-start gap-2.5">
            <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-flag" />
            <div>
              <h2 id="full-access-title" className="font-display text-[15px] font-semibold text-ink">启用完全权限</h2>
              <p id="full-access-description" className="mt-0.5 text-[12.5px] leading-5 text-muted">
                当前计划将不再逐项请求确认。以下操作会自动执行，仍受镜像白名单、磁盘与运行时长上限约束。
              </p>
            </div>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            onClick={() => { setAcknowledged(false); onCancel(); }}
            aria-label="关闭完全权限确认"
            className="flex h-6 w-6 items-center justify-center text-muted transition-colors hover:bg-surface-strong hover:text-ink"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>

        <dl className="grid grid-cols-[68px_minmax(0,1fr)] border-b border-line">
          {risks.map((risk) => (
            <div key={risk.key} className="contents">
              <dt className="border-b border-line/70 px-4 py-2 font-mono text-[11px] tracking-[0.08em] text-faint">{risk.key}</dt>
              <dd className="border-b border-line/70 px-3 py-2 text-[12.5px] leading-5 text-ink-soft">{risk.value}</dd>
            </div>
          ))}
        </dl>

        <div className="p-4">
          <label className="flex items-start gap-2 border border-flag/25 bg-flag-soft px-2.5 py-2 text-[12px] leading-5 text-ink-soft">
            <input
              type="checkbox"
              checked={acknowledged}
              onChange={(event) => setAcknowledged(event.target.checked)}
              className="mt-1 h-3.5 w-3.5 accent-[var(--color-accent)]"
            />
            <span>我理解并允许系统在当前计划中自动执行上述操作；仅在我信任该论文仓库时使用。</span>
          </label>
          <div className="mt-3 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => { setAcknowledged(false); onCancel(); }}
              className="h-7 border border-line-strong px-3 text-[12.5px] font-medium text-ink-soft transition-colors hover:bg-surface-muted focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
            >
              取消
            </button>
            <button
              type="button"
              disabled={!acknowledged}
              onClick={() => { setAcknowledged(false); onConfirm(); }}
              className="h-7 bg-accent px-3 text-[12.5px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:cursor-not-allowed disabled:bg-surface-strong disabled:text-faint focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
            >
              确认完全权限
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
