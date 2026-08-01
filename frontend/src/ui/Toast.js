import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { cn } from './cn';
import { Check, X, Alert, Sparkles } from './icons';

const ToastContext = createContext(null);

const VARIANTS = {
  success: { icon: Check, ring: 'bg-positive-fill' },
  error: { icon: Alert, ring: 'bg-danger-fill' },
  info: { icon: Sparkles, ring: 'bg-brand-fill' },
};

function ToastItem({ toast, onDismiss }) {
  const { icon: Icon, ring } = VARIANTS[toast.variant] || VARIANTS.info;
  const [paused, setPaused] = useState(false);
  const remainingRef = useRef(toast.duration);
  const startedRef = useRef(Date.now());

  // The timer pauses on hover and on focus. A toast carrying the only "Annuler"
  // for an action the user just regretted must not expire while they are
  // reaching for it — and a keyboard user needs it to survive being tabbed to.
  useEffect(() => {
    if (toast.duration <= 0 || paused) return undefined;
    startedRef.current = Date.now();
    const id = window.setTimeout(() => onDismiss(toast.id), remainingRef.current);
    return () => {
      window.clearTimeout(id);
      remainingRef.current = Math.max(0, remainingRef.current - (Date.now() - startedRef.current));
    };
  }, [paused, toast.duration, toast.id, onDismiss]);

  return (
    <div
      className="pointer-events-auto flex w-full max-w-sm animate-slide-in-right items-center gap-3 rounded-2xl border border-hairline bg-surface-raised p-3 pr-4 shadow-card"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocusCapture={() => setPaused(true)}
      onBlurCapture={() => setPaused(false)}
    >
      <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-white', ring)}>
        <Icon size={18} />
      </span>
      <p className="flex-1 text-sm font-medium text-ink-800">{toast.message}</p>
      {toast.action && (
        <button
          onClick={() => {
            toast.action.onClick();
            onDismiss(toast.id);
          }}
          className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-bold text-brand-600 transition-colors hover:bg-brand-50"
        >
          {toast.action.label}
        </button>
      )}
      <button
        onClick={() => onDismiss(toast.id)}
        className="rounded-lg p-1 text-muted transition-colors hover:bg-ink-100 hover:text-ink-900"
        aria-label="Fermer la notification"
      >
        <X size={16} />
      </button>
    </div>
  );
}

export function ToastProvider({ children }) {
  const [toasts, setToasts] = useState([]);
  const idRef = useRef(0);

  const dismiss = useCallback((id) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback((message, { variant = 'info', duration = 3500, action = null } = {}) => {
    const id = ++idRef.current;
    // Cap the stack: a batch that fails per-item could otherwise bury the screen.
    setToasts((prev) => [...prev, { id, message, variant, action, duration }].slice(-4));
    return id;
  }, []);

  const toast = {
    success: (m, o) => push(m, { ...o, variant: 'success' }),
    error: (m, o) => push(m, { ...o, variant: 'error' }),
    info: (m, o) => push(m, { ...o, variant: 'info' }),
    // Action toast (e.g. "Annuler"). Stays a bit longer by default.
    action: (m, label, onClick, o = {}) =>
      push(m, { variant: 'info', duration: 8000, ...o, action: { label, onClick } }),
    dismiss,
  };

  // Two regions, declared up front and never remounted, so a screen reader picks
  // them up: errors interrupt (assertive), everything else waits its turn.
  // The old markup put role="status" on each toast as it appeared, which is
  // unreliable — a live region has to exist before the content lands in it.
  const errors = toasts.filter((t) => t.variant === 'error');
  const others = toasts.filter((t) => t.variant !== 'error');

  return (
    <ToastContext.Provider value={toast}>
      {children}
      <div className="sr-only" aria-live="assertive" aria-atomic="false">
        {errors.map((t) => (
          <p key={t.id}>{t.message}</p>
        ))}
      </div>
      <div className="sr-only" aria-live="polite" aria-atomic="false">
        {others.map((t) => (
          <p key={t.id}>{t.message}</p>
        ))}
      </div>
      <div className="pointer-events-none fixed inset-x-0 bottom-0 z-[100] flex flex-col items-center gap-2 p-4 sm:items-end sm:p-6">
        {toasts.map((t) => (
          <ToastItem key={t.id} toast={t} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used within a ToastProvider');
  return ctx;
}
