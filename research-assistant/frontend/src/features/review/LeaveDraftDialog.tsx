import { useEffect, useRef } from 'react';

interface LeaveDraftDialogProps {
  open: boolean;
  busy: boolean;
  error: string;
  revisionLabel: string;
  onSaveAndContinue: () => void;
  onDiscardAndContinue: () => void;
  onKeepEditing: () => void;
}

/**
 * Single dialog shared by every "leave with unsaved edits" path: the close
 * button, Escape, revision switching, refresh, and the guard the parent shell
 * uses for plan/session switches.
 *
 * It is a real nested `<dialog>` opened with `showModal()` — a plain
 * `role="dialog"` div would leave the rest of the workbench focusable behind
 * the prompt. `showModal` gives the focus trap, the inert background and focus
 * restoration to the trigger for free; the manual focus restore below is the
 * fallback for environments without dialog support (jsdom, older webviews).
 *
 * Escape is handled by `onCancel` with `preventDefault()` so the native cancel
 * does not also close the element while the draft must be kept, and by an
 * explicit keydown handler for environments that never dispatch `cancel`.
 */
export function LeaveDraftDialog({
  open,
  busy,
  error,
  revisionLabel,
  onSaveAndContinue,
  onDiscardAndContinue,
  onKeepEditing,
}: LeaveDraftDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    if (!open) return;
    const dialog = dialogRef.current;
    if (!dialog) return;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    let modal = false;
    if (typeof dialog.showModal === 'function') {
      try {
        dialog.showModal();
        modal = true;
      } catch {
        dialog.open = true;
      }
    } else {
      dialog.open = true;
    }
    return () => {
      if (modal) dialog.close();
      else dialog.open = false;
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
    };
  }, [open]);

  if (!open) return null;

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby="plan-review-leave-title"
      aria-describedby="plan-review-leave-body"
      style={{
        position: 'fixed',
        inset: 0,
        width: '100%',
        height: '100%',
        maxWidth: '100vw',
        maxHeight: '100dvh',
        margin: 0,
        padding: 0,
        border: 0,
        background: 'transparent',
        color: 'inherit',
        overflow: 'auto',
      }}
      onCancel={(event) => {
        // Never let the native cancel close the element on its own: keeping the
        // draft is the only safe default for Escape.
        event.preventDefault();
        if (!busy) onKeepEditing();
      }}
      onKeyDown={(event) => {
        if (event.key !== 'Escape') return;
        event.preventDefault();
        event.stopPropagation();
        if (!busy) onKeepEditing();
      }}
    >
      <div className="flex min-h-full w-full items-center justify-center bg-ink/30 p-4">
        <div className="w-full max-w-md rounded-2xl border border-line bg-paper p-4 shadow-float">
          <h2 id="plan-review-leave-title" className="text-[15px] font-semibold text-ink">
            {revisionLabel ? `r${revisionLabel} 有未保存的修改` : '有未保存的修改'}
          </h2>
          <p id="plan-review-leave-body" className="mt-1 text-[11px] text-muted">
            离开或切换版本会丢失这些修改。可以先保存为新版本，也可以放弃修改后继续。
          </p>
          {error && (
            <p role="alert" className="mt-2 rounded-lg border border-danger/40 bg-danger/10 px-2 py-1 text-[11px] text-danger">
              {error}
            </p>
          )}
          <div className="mt-3 flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              disabled={busy}
              onClick={onKeepEditing}
              className="inline-flex h-8 items-center rounded-lg border border-line px-2.5 text-[11px] text-ink-soft transition-colors hover:border-accent hover:text-accent disabled:opacity-40"
            >
              继续编辑
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={onDiscardAndContinue}
              className="inline-flex h-8 items-center rounded-lg border border-danger/60 px-2.5 text-[11px] text-danger transition-colors hover:border-danger disabled:opacity-40"
            >
              放弃修改
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={onSaveAndContinue}
              className="inline-flex h-8 items-center rounded-lg bg-accent px-2.5 text-[11px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:opacity-40"
            >
              {busy ? '保存中…' : '保存后继续'}
            </button>
          </div>
        </div>
      </div>
    </dialog>
  );
}

export default LeaveDraftDialog;
