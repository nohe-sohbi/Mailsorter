import React, { createContext, useCallback, useContext, useRef, useState } from 'react';
import { cn } from './cn';
import { Check, X, Alert, Sparkles } from './icons';

const ToastContext = createContext(null);

const VARIANTS = {
  success: { icon: Check, ring: 'bg-emerald-500' },
  error: { icon: Alert, ring: 'bg-rose-500' },
  info: { icon: Sparkles, ring: 'bg-brand-500' },
};

export function ToastProvider({ children }) {
  const [toasts, setToasts] = useState([]);
  const idRef = useRef(0);

  const dismiss = useCallback((id) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (message, { variant = 'info', duration = 3500, action = null } = {}) => {
      const id = ++idRef.current;
      setToasts((prev) => [...prev, { id, message, variant, action }]);
      if (duration > 0) setTimeout(() => dismiss(id), duration);
      return id;
    },
    [dismiss]
  );

  const toast = {
    success: (m, o) => push(m, { ...o, variant: 'success' }),
    error: (m, o) => push(m, { ...o, variant: 'error' }),
    info: (m, o) => push(m, { ...o, variant: 'info' }),
    // Action toast (e.g. "Annuler"). Stays a bit longer by default.
    action: (m, label, onClick, o = {}) =>
      push(m, { variant: 'info', duration: 5500, ...o, action: { label, onClick } }),
  };

  return (
    <ToastContext.Provider value={toast}>
      {children}
      <div className="pointer-events-none fixed inset-x-0 bottom-0 z-[100] flex flex-col items-center gap-2 p-4 sm:items-end sm:p-6">
        {toasts.map((t) => {
          const { icon: Icon, ring } = VARIANTS[t.variant] || VARIANTS.info;
          return (
            <div
              key={t.id}
              className="pointer-events-auto flex w-full max-w-sm animate-slide-in-right items-center gap-3 rounded-2xl border border-ink-200/70 bg-white/95 p-3 pr-4 shadow-card backdrop-blur"
              role="status"
            >
              <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-white', ring)}>
                <Icon size={18} />
              </span>
              <p className="flex-1 text-sm font-medium text-ink-800">{t.message}</p>
              {t.action && (
                <button
                  onClick={() => {
                    t.action.onClick();
                    dismiss(t.id);
                  }}
                  className="shrink-0 rounded-lg px-2.5 py-1 text-sm font-bold text-brand-600 transition-colors hover:bg-brand-50"
                >
                  {t.action.label}
                </button>
              )}
              <button
                onClick={() => dismiss(t.id)}
                className="rounded-lg p-1 text-ink-400 transition-colors hover:bg-ink-100 hover:text-ink-600"
                aria-label="Fermer"
              >
                <X size={16} />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used within a ToastProvider');
  return ctx;
}
