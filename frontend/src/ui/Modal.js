import React, { useCallback, useEffect, useRef } from 'react';
import { cn } from './cn';
import { X } from './icons';
import { useScrollLock } from './scrollLock';

// Focusable descendants, in DOM order. Used to trap Tab inside the dialog.
const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';


// Modal is the one dialog implementation in the app.
//
// The screens used to hand-roll `fixed inset-0` overlays with no `role`, no
// `aria-modal`, no focus trap and no scroll lock; the onboarding one could not
// even be closed with Escape. Keyboard and screen-reader users could tab out of
// a dialog into the page behind it and never find their way back.
//
// Scroll locking is reference-counted so closing a confirmation stacked on top
// of another dialog does not hand scrolling back to the page too early.
function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  size = 'md',
  initialFocusRef,
  closeOnBackdrop = true,
  hideClose = false,
}) {
  const panelRef = useRef(null);
  const titleId = useRef(`modal-title-${Math.random().toString(36).slice(2)}`);
  const descId = useRef(`modal-desc-${Math.random().toString(36).slice(2)}`);
  const restoreRef = useRef(null);

  const close = useCallback(() => onClose?.(), [onClose]);

  useScrollLock(open);

  // Remember where focus came from, move it into the dialog, and put it back on
  // close: without this, dismissing a dialog drops focus onto <body> and the
  // next Tab restarts from the top of the page.
  useEffect(() => {
    if (!open) return undefined;
    restoreRef.current = document.activeElement;

    const target =
      initialFocusRef?.current || panelRef.current?.querySelector(FOCUSABLE) || panelRef.current;
    // Wait a frame so the element exists and the entry animation has started.
    const raf = window.requestAnimationFrame(() => target?.focus?.());

    return () => {
      window.cancelAnimationFrame(raf);
      const restore = restoreRef.current;
      if (restore && typeof restore.focus === 'function') restore.focus();
    };
  }, [open, initialFocusRef]);

  useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (e) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        close();
        return;
      }
      if (e.key !== 'Tab') return;
      const nodes = Array.from(panelRef.current?.querySelectorAll(FOCUSABLE) || []).filter(
        (el) => el.offsetParent !== null || el === document.activeElement
      );
      if (nodes.length === 0) {
        e.preventDefault();
        return;
      }
      const first = nodes[0];
      const last = nodes[nodes.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    // Capture phase: the dialog must win Escape over any page-level handler.
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [open, close]);

  if (!open) return null;

  const sizes = {
    sm: 'max-w-sm',
    md: 'max-w-md',
    lg: 'max-w-lg',
    xl: 'max-w-2xl',
  };

  return (
    <div
      className="fixed inset-0 z-[95] flex items-end justify-center bg-ink-950/60 p-0 backdrop-blur-sm sm:items-center sm:p-4"
      onMouseDown={(e) => {
        // mousedown, not click: a drag that starts inside the panel and ends on
        // the backdrop must not be read as "dismiss".
        if (closeOnBackdrop && e.target === e.currentTarget) close();
      }}
    >
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={title ? titleId.current : undefined}
        aria-describedby={description ? descId.current : undefined}
        className={cn(
          'card w-full animate-slide-up overflow-hidden rounded-b-none sm:animate-scale-in sm:rounded-2xl',
          sizes[size] || sizes.md
        )}
      >
        {(title || !hideClose) && (
          <div className="flex items-start justify-between gap-4 border-b border-hairline px-6 py-4">
            <div className="min-w-0">
              {title && (
                <h2 id={titleId.current} className="font-display text-lg font-extrabold text-ink-900">
                  {title}
                </h2>
              )}
              {description && (
                <p id={descId.current} className="mt-1 text-sm text-muted">
                  {description}
                </p>
              )}
            </div>
            {!hideClose && (
              <button onClick={close} className="btn-ghost btn-sm btn-icon shrink-0" aria-label="Fermer">
                <X size={18} />
              </button>
            )}
          </div>
        )}
        <div className="max-h-[70vh] overflow-y-auto px-6 py-5">{children}</div>
        {footer && (
          <div className="flex flex-col-reverse gap-2 border-t border-hairline px-6 py-4 sm:flex-row sm:justify-end">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}

export default Modal;
