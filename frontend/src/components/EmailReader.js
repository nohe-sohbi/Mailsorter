import React, { useState } from 'react';
import DOMPurify from 'dompurify';
import { X, Archive, Trash, Mail, BellOff, Clock, Shield } from '../ui/icons';
import Spinner from '../ui/Spinner';

// Friendly snooze presets, resolved to concrete wake times server-side.
const SNOOZE_PRESETS = [
  ['laterToday', 'Plus tard'],
  ['thisEvening', 'Ce soir'],
  ['tomorrow', 'Demain matin'],
  ['weekend', 'Ce week-end'],
  ['nextWeek', 'Semaine prochaine'],
];

const AVATAR_GRADIENTS = [
  'from-brand-500 to-fuchsia-500',
  'from-sky-500 to-indigo-500',
  'from-emerald-500 to-teal-500',
  'from-amber-500 to-orange-500',
  'from-rose-500 to-pink-500',
];

function gradientFor(seed = '') {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_GRADIENTS[h % AVATAR_GRADIENTS.length];
}

function extractEmail(from) {
  const match = from?.match(/<(.+)>/);
  return match ? match[1] : from;
}
function extractName(from) {
  const match = from?.match(/^(.+?)\s*</);
  return match ? match[1].replace(/"/g, '').trim() : from;
}

function EmailReader({ email, onClose, onArchive, onDelete, onUnsubscribe, onSnooze, onProtect, unsubscribing }) {
  const [snoozeOpen, setSnoozeOpen] = useState(false);
  if (!email) return null;

  const canUnsubscribe = Boolean(email.unsubUrl || email.unsubMailto);

  const handleSnooze = (preset) => {
    setSnoozeOpen(false);
    onSnooze?.(preset);
  };

  const formatDate = (dateStr) => {
    if (!dateStr) return '';
    return new Date(dateStr).toLocaleDateString('fr-FR', {
      weekday: 'long',
      day: 'numeric',
      month: 'long',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  const name = extractName(email.from) || 'Expéditeur inconnu';
  const cleanBody = email.body
    ? DOMPurify.sanitize(email.body, { USE_PROFILES: { html: true } })
    : null;

  return (
    <aside className="flex h-full w-full animate-slide-in-right flex-col overflow-hidden bg-white">
      <div className="flex items-center justify-between border-b border-ink-200/70 px-5 py-3">
        <button onClick={onClose} className="btn-ghost px-2.5" aria-label="Fermer">
          <X size={18} />
        </button>
        <div className="flex items-center gap-1">
          {onSnooze && (
            <div className="relative">
              <button
                onClick={() => setSnoozeOpen((v) => !v)}
                className="btn-ghost px-2.5"
                title="Reporter (sortir de la boîte et revenir plus tard)"
              >
                <Clock size={18} />
              </button>
              {snoozeOpen && (
                <>
                  <div className="fixed inset-0 z-10" onClick={() => setSnoozeOpen(false)} />
                  <div className="absolute right-0 z-20 mt-1 w-48 overflow-hidden rounded-xl border border-ink-200 bg-white py-1 shadow-lg animate-fade-up">
                    <div className="px-3 py-1.5 text-xs font-semibold text-ink-400">Reporter jusqu'à…</div>
                    {SNOOZE_PRESETS.map(([value, label]) => (
                      <button
                        key={value}
                        onClick={() => handleSnooze(value)}
                        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-ink-700 hover:bg-ink-50"
                      >
                        <Clock size={15} className="text-ink-400" /> {label}
                      </button>
                    ))}
                  </div>
                </>
              )}
            </div>
          )}
          {onProtect && (
            <button
              onClick={onProtect}
              className="btn-ghost px-2.5 text-emerald-600 hover:bg-emerald-50"
              title="Protéger cet expéditeur (jamais archivé/supprimé automatiquement)"
            >
              <Shield size={18} />
            </button>
          )}
          <button onClick={onArchive} className="btn-ghost px-2.5" title="Archiver">
            <Archive size={18} />
          </button>
          <button onClick={onDelete} className="btn-ghost px-2.5 text-rose-500 hover:bg-rose-50" title="Supprimer">
            <Trash size={18} />
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-6 py-6">
        <h2 className="text-xl font-bold leading-snug text-ink-900">{email.subject || '(Sans sujet)'}</h2>

        <div className="mt-5 flex items-center gap-3">
          <span
            className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-gradient-to-br text-base font-bold text-white ${gradientFor(
              email.from
            )}`}
          >
            {name[0]?.toUpperCase() || '?'}
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate font-semibold text-ink-900">{name}</div>
            <div className="truncate text-sm text-ink-500">{extractEmail(email.from)}</div>
          </div>
          <div className="hidden text-right text-xs text-ink-400 sm:block">{formatDate(email.receivedDate)}</div>
        </div>

        {canUnsubscribe && onUnsubscribe && (
          <div className="mt-4 flex items-center gap-3 rounded-xl border border-amber-200/70 bg-amber-50 px-4 py-3">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-amber-100 text-amber-600">
              <BellOff size={18} />
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-bold text-ink-900">Vous recevez trop d'emails de cet expéditeur ?</div>
              <div className="text-xs text-ink-500">
                {email.unsubOneClick ? 'Désabonnement instantané, sans quitter Mailsorter.' : "On ouvre la page de désabonnement pour vous."}
              </div>
            </div>
            <button
              onClick={onUnsubscribe}
              disabled={unsubscribing}
              className="btn-secondary shrink-0 border-amber-300 text-amber-700 hover:bg-amber-100"
            >
              {unsubscribing ? <Spinner size={16} /> : <BellOff size={16} />} Se désabonner
            </button>
          </div>
        )}

        <div className="mt-6 border-t border-ink-100 pt-6">
          {cleanBody ? (
            <div
              className="prose-sm max-w-none text-sm leading-relaxed text-ink-700 [&_a]:text-brand-600 [&_img]:max-w-full"
              dangerouslySetInnerHTML={{ __html: cleanBody }}
            />
          ) : (
            <div className="space-y-4">
              <p className="text-sm leading-relaxed text-ink-700">{email.snippet}</p>
              <div className="flex items-center gap-2 rounded-xl bg-ink-50 px-4 py-3 text-xs text-ink-400">
                <Mail size={16} />
                Contenu complet indisponible. Synchronisez pour charger le corps de l'email.
              </div>
            </div>
          )}
        </div>

        {email.labelIds && email.labelIds.length > 0 && (
          <div className="mt-6 flex flex-wrap gap-2 border-t border-ink-100 pt-5">
            {email.labelIds.map((label) => (
              <span key={label} className="chip bg-ink-100 text-ink-600">
                {label.replace('Label_', '').replace('CATEGORY_', '').toLowerCase()}
              </span>
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}

export default EmailReader;
