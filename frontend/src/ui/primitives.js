import React from 'react';
import { cn } from './cn';

// The three patterns every screen was copy-pasting, with the divergences that
// copy-pasting produces: six empty states in three different sizes, four
// progress bars none of which announced themselves, and three unrelated ways to
// render the same on/off switch.

// Toggle is a real `role="switch"`: the custom pills in Rules and Inbox were
// plain buttons whose on/off state was conveyed by colour alone, so assistive
// technology read them as unlabelled buttons with no state at all.
export function Toggle({ checked, onChange, label, description, disabled = false, id }) {
  const labelId = id ? `${id}-label` : undefined;
  return (
    <div className="flex items-center gap-3">
      {(label || description) && (
        <span className="min-w-0 flex-1">
          {label && (
            <span id={labelId} className="block text-sm font-bold text-ink-900">
              {label}
            </span>
          )}
          {description && <span className="mt-0.5 block text-xs text-muted">{description}</span>}
        </span>
      )}
      <button
        type="button"
        role="switch"
        id={id}
        aria-checked={checked}
        aria-labelledby={labelId}
        aria-label={labelId ? undefined : label}
        disabled={disabled}
        onClick={() => onChange?.(!checked)}
        className={cn(
          'relative h-6 w-11 shrink-0 rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-50',
          checked ? 'bg-brand-fill' : 'bg-ink-400'
        )}
      >
        <span
          className={cn(
            'absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all',
            checked ? 'left-[22px]' : 'left-0.5'
          )}
        />
      </button>
    </div>
  );
}

// Progress carries its value to assistive tech, and the rail is dark enough to
// be seen against the card it sits on: the old `bg-ink-100` rail came in at
// 1,23:1, which is to say invisible to the people who needed it most.
export function Progress({ value = 0, max = 100, label, tone = 'brand', className }) {
  const safeMax = max > 0 ? max : 100;
  const pct = Math.max(0, Math.min(100, Math.round((value / safeMax) * 100)));
  const tones = {
    brand: 'bg-brand-fill',
    positive: 'bg-positive-fill',
    caution: 'bg-caution-fill',
  };
  return (
    <div
      role="progressbar"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-label={label}
      className={cn('h-2.5 w-full overflow-hidden rounded-full bg-ink-300', className)}
    >
      <div
        className={cn('h-full rounded-full transition-all duration-500', tones[tone] || tones.brand)}
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}

// EmptyState: one shape, one scale, everywhere.
export function EmptyState({ Icon, title, description, action, tone = 'brand', compact = false }) {
  const tones = {
    brand: 'bg-brand-50 text-brand-600',
    positive: 'bg-positive-50 text-positive-600',
    caution: 'bg-caution-50 text-caution-700',
    neutral: 'bg-ink-100 text-ink-600',
  };
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center px-6 text-center',
        compact ? 'py-10' : 'py-16'
      )}
    >
      {Icon && (
        <span
          className={cn(
            'mb-4 flex items-center justify-center rounded-2xl',
            compact ? 'h-12 w-12' : 'h-14 w-14',
            tones[tone] || tones.brand
          )}
        >
          <Icon size={compact ? 22 : 26} />
        </span>
      )}
      <h3 className="text-base font-bold text-ink-900">{title}</h3>
      {description && <p className="mx-auto mt-1.5 max-w-sm text-sm text-muted">{description}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}

// ErrorState is what an empty list must show when the load actually failed.
// Rendering the celebratory "Inbox Zero atteint 🎉" on a Gmail outage told the
// user the exact opposite of the truth.
export function ErrorState({ title = 'Chargement impossible', message, onRetry, compact = false }) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center px-6 text-center',
        compact ? 'py-10' : 'py-16'
      )}
      role="alert"
    >
      <span className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-danger-50 text-danger-600">
        <svg viewBox="0 0 24 24" width={26} height={26} fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 9v4M12 17h.01" />
          <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
        </svg>
      </span>
      <h3 className="text-base font-bold text-ink-900">{title}</h3>
      {message && <p className="mx-auto mt-1.5 max-w-sm break-words text-sm text-muted">{message}</p>}
      {onRetry && (
        <button onClick={onRetry} className="btn-secondary mt-5">
          Réessayer
        </button>
      )}
    </div>
  );
}

// A visually hidden live region for announcements that have no visual anchor
// (e.g. "12 emails sélectionnés" after a select-all).
export function LiveAnnouncer({ message }) {
  return (
    <span aria-live="polite" aria-atomic="true" className="sr-only">
      {message}
    </span>
  );
}
