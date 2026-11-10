import { useEffect, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

interface WorkbenchDialogProps {
  label: string;
  onRequestClose: () => void;
  className?: string;
  children: ReactNode;
}

/** Native modal dialogs keep the rest of the workbench inert and restore focus. */
export function WorkbenchDialog({ label, onRequestClose, className = '', children }: WorkbenchDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const closeRef = useRef(onRequestClose);
  useEffect(() => { closeRef.current = onRequestClose; }, [onRequestClose]);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const closeOnBackdrop = (event: MouseEvent) => {
      if (event.target !== dialog) return;
      const bounds = dialog.getBoundingClientRect();
      if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) closeRef.current();
    };
    const containTabFocus = (event: KeyboardEvent) => {
      if (event.key !== 'Tab' || event.defaultPrevented) return;
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'))
        .filter((element) => element.getClientRects().length > 0 && !element.closest('[hidden], [inert]'));
      const first = focusable[0];
      const last = focusable.at(-1);
      if (!first) { event.preventDefault(); dialog.focus(); return; }
      if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog)) {
        event.preventDefault(); last?.focus();
      } else if (!event.shiftKey && (document.activeElement === last || document.activeElement === dialog)) {
        event.preventDefault(); first.focus();
      }
    };
    dialog.addEventListener('click', closeOnBackdrop);
    dialog.addEventListener('keydown', containTabFocus);
    dialog.showModal();
    return () => {
      dialog.removeEventListener('click', closeOnBackdrop);
      dialog.removeEventListener('keydown', containTabFocus);
      dialog.close();
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
    };
  }, []);

  return createPortal(
    <dialog
      ref={dialogRef}
      aria-label={label}
      tabIndex={-1}
      className={`workbench-dialog ${className}`}
      onCancel={(event) => { event.preventDefault(); closeRef.current(); }}
    >
      {children}
    </dialog>,
    document.body,
  );
}
