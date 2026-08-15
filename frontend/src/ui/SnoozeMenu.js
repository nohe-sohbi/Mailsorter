import React, { useEffect, useRef, useState } from 'react';
import { Clock } from './icons';
import { cn } from './cn';

// Friendly presets, resolved to concrete wake times server-side (internal/snooze).
export const SNOOZE_PRESETS = [
  ['laterToday', 'Plus tard'],
  ['thisEvening', 'Ce soir'],
  ['tomorrow', 'Demain matin'],
  ['weekend', 'Ce week-end'],
  ['nextWeek', 'Semaine prochaine'],
];

// Mirrors snooze.MaxHorizon in the backend: a hand-picked date is free-form, and
// a mistyped year would otherwise hide an email for a decade.
const MAX_HORIZON_DAYS = 365;

// datetime-local speaks local wall-clock time in this exact shape, with no zone.
// Going through the parts (rather than toISOString) is what keeps the value in
// the user's own timezone instead of shifting it to UTC.
function toLocalInputValue(date) {
  const pad = (n) => String(n).padStart(2, '0');
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    `T${pad(date.getHours())}:${pad(date.getMinutes())}`
  );
}

/**
 * SnoozeButton owns the whole "reporter" affordance: the trigger, the preset
 * menu, and the custom date picker behind it.
 *
 * It exists as one component because the reader and the bulk bar both need it,
 * and the popover has real behaviour to get right (Escape closes, an outside
 * click closes, focus returns to the trigger). Two copies would have drifted.
 *
 * onSnooze receives ({ preset, wakeAt }): exactly one of the two is set, which
 * mirrors the API contract (POST /api/emails/snooze takes a preset OR a wakeAt).
 */
export default function SnoozeButton({
  onSnooze,
  disabled = false,
  label = '',
  title = 'Reporter (sortir de la boîte et revenir plus tard)',
  ariaLabel = 'Reporter',
  className = 'btn-ghost btn-sm btn-icon',
  align = 'right',
  Icon = Clock,
  iconSize = 18,
}) {
  const [open, setOpen] = useState(false);
  const [custom, setCustom] = useState(false);
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const triggerRef = useRef(null);
  const customRef = useRef(null);

  const close = () => {
    setOpen(false);
    setCustom(false);
    setError('');
  };

  useEffect(() => {
    if (!open) return undefined;
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      // Stop here: the reader and the bulk bar both close on Escape too, and
      // one keystroke must dismiss one layer.
      e.stopPropagation();
      close();
      triggerRef.current?.focus();
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [open]);

  useEffect(() => {
    if (custom) customRef.current?.focus();
  }, [custom]);

  const pickPreset = (preset) => {
    close();
    onSnooze?.({ preset });
  };

  const openCustom = () => {
    // Seed with a round hour ahead: a picker opening on "now" invites a time
    // that is already in the past by the time the user clicks.
    const seed = new Date(Date.now() + 60 * 60 * 1000);
    seed.setMinutes(0, 0, 0);
    setValue(toLocalInputValue(seed));
    setError('');
    setCustom(true);
  };

  const submitCustom = (e) => {
    e.preventDefault();
    const chosen = new Date(value);
    if (Number.isNaN(chosen.getTime())) {
      setError('Choisissez une date et une heure.');
      return;
    }
    // Validated here as well as server-side, so the mistake is caught before the
    // email leaves the inbox rather than after a round trip.
    if (chosen.getTime() <= Date.now()) {
      setError("L'échéance doit être dans le futur.");
      return;
    }
    if (chosen.getTime() - Date.now() > MAX_HORIZON_DAYS * 86400000) {
      setError('Un report ne peut pas dépasser un an.');
      return;
    }
    close();
    onSnooze?.({ wakeAt: chosen.toISOString() });
  };

  const min = toLocalInputValue(new Date(Date.now() + 60000));
  const max = toLocalInputValue(new Date(Date.now() + MAX_HORIZON_DAYS * 86400000));

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => (open ? close() : setOpen(true))}
        disabled={disabled}
        className={className}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={ariaLabel}
        title={title}
      >
        <Icon size={iconSize} />
        {label}
      </button>

      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={close} />
          <div
            role="menu"
            className={cn(
              'absolute z-20 mt-1 w-64 animate-fade-up overflow-hidden rounded-xl border border-hairline bg-surface-raised py-1 shadow-card',
              align === 'left' ? 'left-0' : 'right-0'
            )}
          >
            <div className="px-3 py-1.5 text-xs font-semibold text-muted">Reporter jusqu'à…</div>
            {SNOOZE_PRESETS.map(([preset, presetLabel]) => (
              <button
                key={preset}
                role="menuitem"
                type="button"
                onClick={() => pickPreset(preset)}
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-ink-700 hover:bg-ink-100"
              >
                <Clock size={15} className="text-subtle" /> {presetLabel}
              </button>
            ))}

            <div className="mt-1 border-t border-hairline pt-1">
              {custom ? (
                // noValidate: min/max borne le sélecteur natif, mais sa
                // validation refuse la soumission en silence (une bulle du
                // navigateur, non stylée, parfois hors écran). On garde les
                // bornes comme aide à la saisie et on rend nos propres messages.
                <form onSubmit={submitCustom} noValidate className="px-3 py-2">
                  <label htmlFor="snooze-custom" className="block text-xs font-semibold text-muted">
                    Date et heure de retour
                  </label>
                  <input
                    ref={customRef}
                    id="snooze-custom"
                    type="datetime-local"
                    className="input mt-1.5 text-sm"
                    value={value}
                    min={min}
                    max={max}
                    onChange={(e) => {
                      setValue(e.target.value);
                      setError('');
                    }}
                  />
                  {error && <p className="mt-1.5 text-xs font-semibold text-danger-600">{error}</p>}
                  <div className="mt-2 flex gap-2">
                    <button type="submit" className="btn-primary btn-sm flex-1">
                      Reporter
                    </button>
                    <button type="button" onClick={close} className="btn-ghost btn-sm">
                      Annuler
                    </button>
                  </div>
                </form>
              ) : (
                <button
                  role="menuitem"
                  type="button"
                  onClick={openCustom}
                  className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm font-semibold text-brand-700 hover:bg-ink-100"
                >
                  <Clock size={15} className="text-brand-600" /> Date personnalisée…
                </button>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
