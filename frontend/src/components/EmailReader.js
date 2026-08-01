import React, { useEffect, useRef, useState } from 'react';
import DOMPurify from 'dompurify';
import { emailService, labelService } from '../services/api';
import { X, Archive, Trash, Mail, BellOff, Clock, Shield, Paperclip, Star } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { ErrorState } from '../ui/primitives';
import { cn } from '../ui/cn';

// Friendly snooze presets, resolved to concrete wake times server-side.
const SNOOZE_PRESETS = [
  ['laterToday', 'Plus tard'],
  ['thisEvening', 'Ce soir'],
  ['tomorrow', 'Demain matin'],
  ['weekend', 'Ce week-end'],
  ['nextWeek', 'Semaine prochaine'],
];

const AVATAR_TONES = ['bg-brand-fill', 'bg-info-fill', 'bg-positive-fill', 'bg-caution-fill', 'bg-danger-fill'];

function toneFor(seed = '') {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_TONES[h % AVATAR_TONES.length];
}

function extractEmail(from) {
  const match = from?.match(/<(.+)>/);
  return match ? match[1] : from;
}
function extractName(from) {
  const match = from?.match(/^(.+?)\s*</);
  return match ? match[1].replace(/"/g, '').trim() : from;
}

// Gmail's own labels are noise in a reader: the user knows the message is in
// their inbox and whether they have read it.
const SYSTEM_LABELS = new Set([
  'INBOX', 'UNREAD', 'STARRED', 'IMPORTANT', 'SENT', 'DRAFT', 'SPAM', 'TRASH', 'CHAT',
]);

const prettyLabel = (id, byId) => {
  const known = byId?.[id];
  if (known) return known;
  if (id.startsWith('CATEGORY_')) {
    const c = id.slice('CATEGORY_'.length).toLowerCase();
    return { personal: 'Personnel', social: 'Réseaux', promotions: 'Promotions', updates: 'Mises à jour', forums: 'Forums' }[c] || c;
  }
  return id.replace(/^Label_/, '');
};

const formatBytes = (n) => {
  if (!n || n < 1024) return `${n || 0} o`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} Ko`;
  return `${(n / (1024 * 1024)).toFixed(1)} Mo`;
};

// Sanitising is not optional here: the body is attacker-controlled HTML. On top
// of DOMPurify's default stripping, every surviving link is forced to open in a
// new tab with rel="noopener" so a marketing email can never navigate the app
// away from itself or reach back through window.opener.
function sanitize(html) {
  const clean = DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'iframe', 'object', 'embed'],
    FORBID_ATTR: ['srcset', 'formaction', 'ping'],
  });
  const doc = new DOMParser().parseFromString(`<div>${clean}</div>`, 'text/html');
  doc.querySelectorAll('a[href]').forEach((a) => {
    a.setAttribute('target', '_blank');
    a.setAttribute('rel', 'noopener noreferrer nofollow');
  });
  // Remote images are the classic read-receipt beacon. They still load (blocking
  // them silently breaks most newsletters) but never get to carry credentials.
  doc.querySelectorAll('img').forEach((img) => {
    img.setAttribute('loading', 'lazy');
    img.setAttribute('referrerpolicy', 'no-referrer');
  });
  return doc.body.firstChild?.innerHTML || '';
}

function EmailReader({
  email,
  onClose,
  onArchive,
  onDelete,
  onUnsubscribe,
  onSnooze,
  onProtect,
  unsubscribing,
  onRead,
}) {
  const [full, setFull] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [snoozeOpen, setSnoozeOpen] = useState(false);
  const [labelsById, setLabelsById] = useState(null);
  const snoozeRef = useRef(null);
  const closeRef = useRef(null);
  const messageId = email?.messageId;

  // The list endpoint deliberately ships no bodies, so the reader fetches the
  // message it is about to show. Before this existed the panel had nothing to
  // render and every email read "Contenu complet indisponible".
  useEffect(() => {
    if (!messageId) return undefined;
    let cancelled = false;
    setFull(null);
    setLoadError('');
    setLoading(true);
    emailService
      .getEmail(messageId, { markRead: true })
      .then(({ data }) => {
        if (cancelled) return;
        setFull(data);
        // Opening an email is what marks it read; tell the list so the unread
        // dot disappears without a full refetch.
        if (data?.isRead) onRead?.(messageId);
      })
      .catch((err) => {
        if (cancelled) return;
        setLoadError(err?.response?.data?.trim?.() || "Impossible de charger cet email.");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // onRead is stable enough in practice; re-running on it would refetch the
    // message (and re-mark it read) on every parent render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messageId]);

  // Label names are per-account and rarely change; fetch once for the session.
  useEffect(() => {
    let cancelled = false;
    labelService
      .list()
      .then(({ data }) => {
        if (cancelled) return;
        const map = {};
        (data || []).forEach((l) => {
          if (l?.id && l?.name) map[l.id] = l.name;
        });
        setLabelsById(map);
      })
      .catch(() => setLabelsById({}));
    return () => {
      cancelled = true;
    };
  }, []);

  // The snooze menu is a popover: Escape and outside clicks must close it, and
  // focus has to come back to the button that opened it.
  useEffect(() => {
    if (!snoozeOpen) return undefined;
    const onKey = (e) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        setSnoozeOpen(false);
        snoozeRef.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [snoozeOpen]);

  if (!email) return null;

  const merged = { ...email, ...(full || {}) };
  const canUnsubscribe = Boolean(merged.unsubUrl || merged.unsubMailto);
  const attachments = full?.attachments || [];

  const handleSnooze = (preset) => {
    setSnoozeOpen(false);
    onSnooze?.(preset);
  };

  const formatDate = (dateStr) => {
    if (!dateStr) return '';
    const d = new Date(dateStr);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleDateString('fr-FR', {
      weekday: 'long',
      day: 'numeric',
      month: 'long',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  const name = extractName(merged.from) || 'Expéditeur inconnu';
  const html = full?.bodyHtml ? sanitize(full.bodyHtml) : null;
  const text = full?.body || '';
  const visibleLabels = (merged.labelIds || []).filter((l) => !SYSTEM_LABELS.has(l));

  return (
    <aside
      className="flex h-full w-full flex-col overflow-hidden bg-surface"
      aria-label={`Email : ${merged.subject || 'sans sujet'}`}
    >
      <div className="flex items-center justify-between gap-2 border-b border-hairline px-3 py-2 sm:px-5 sm:py-3">
        <button ref={closeRef} onClick={onClose} className="btn-ghost btn-sm btn-icon" aria-label="Fermer le lecteur">
          <X size={18} />
        </button>
        <div className="flex items-center gap-0.5">
          {onSnooze && (
            <div className="relative">
              <button
                ref={snoozeRef}
                onClick={() => setSnoozeOpen((v) => !v)}
                className="btn-ghost btn-sm btn-icon"
                aria-haspopup="menu"
                aria-expanded={snoozeOpen}
                aria-label="Reporter cet email"
                title="Reporter (sortir de la boîte et revenir plus tard)"
              >
                <Clock size={18} />
              </button>
              {snoozeOpen && (
                <>
                  <div className="fixed inset-0 z-10" onClick={() => setSnoozeOpen(false)} />
                  <div
                    role="menu"
                    className="absolute right-0 z-20 mt-1 w-48 animate-fade-up overflow-hidden rounded-xl border border-hairline bg-surface-raised py-1 shadow-card"
                  >
                    <div className="px-3 py-1.5 text-xs font-semibold text-muted">Reporter jusqu'à…</div>
                    {SNOOZE_PRESETS.map(([value, label]) => (
                      <button
                        key={value}
                        role="menuitem"
                        onClick={() => handleSnooze(value)}
                        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-ink-700 hover:bg-ink-100"
                      >
                        <Clock size={15} className="text-subtle" /> {label}
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
              className="btn-ghost btn-sm btn-icon text-positive-600 hover:bg-positive-50"
              aria-label="Protéger cet expéditeur"
              title="Protéger cet expéditeur (jamais archivé/supprimé automatiquement)"
            >
              <Shield size={18} />
            </button>
          )}
          <button onClick={onArchive} className="btn-ghost btn-sm btn-icon" aria-label="Archiver" title="Archiver">
            <Archive size={18} />
          </button>
          <button
            onClick={onDelete}
            className="btn-ghost btn-sm btn-icon text-danger-600 hover:bg-danger-50"
            aria-label="Supprimer"
            title="Supprimer"
          >
            <Trash size={18} />
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-5 py-5 sm:px-6 sm:py-6">
        <h2 className="text-lg font-bold leading-snug text-ink-900 sm:text-xl">
          {merged.subject || '(Sans sujet)'}
        </h2>

        <div className="mt-4 flex items-center gap-3">
          <span
            className={cn(
              'flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-base font-bold text-white',
              toneFor(merged.from)
            )}
            aria-hidden
          >
            {name[0]?.toUpperCase() || '?'}
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate font-semibold text-ink-900">{name}</div>
            <div className="truncate text-sm text-muted">{extractEmail(merged.from)}</div>
          </div>
          {merged.receivedDate && (
            <time
              dateTime={merged.receivedDate}
              className="hidden shrink-0 text-right text-xs text-muted sm:block"
            >
              {formatDate(merged.receivedDate)}
            </time>
          )}
        </div>

        {canUnsubscribe && onUnsubscribe && (
          <div className="mt-4 flex flex-wrap items-center gap-3 rounded-xl border border-caution-100 bg-caution-50 px-4 py-3">
            <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-caution-100 text-caution-700">
              <BellOff size={18} />
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-bold text-ink-900">Vous recevez trop d'emails de cet expéditeur ?</div>
              <div className="text-xs text-muted">
                {merged.unsubOneClick
                  ? 'Désabonnement instantané, sans quitter Mailsorter.'
                  : "On ouvre la page de désabonnement pour vous."}
              </div>
            </div>
            <button
              onClick={onUnsubscribe}
              disabled={unsubscribing}
              className="btn-secondary btn-sm shrink-0 border-caution-100 text-caution-700 hover:bg-caution-100"
            >
              {unsubscribing ? <Spinner size={16} /> : <BellOff size={16} />} Se désabonner
            </button>
          </div>
        )}

        {attachments.length > 0 && (
          <div className="mt-4 flex flex-wrap gap-2">
            {attachments.map((a, i) => (
              <span
                key={`${a.filename}-${i}`}
                className="chip bg-ink-100 text-ink-700"
                title={`${a.filename} · ${a.mimeType || 'fichier'}`}
              >
                <Paperclip size={13} />
                <span className="max-w-[180px] truncate">{a.filename}</span>
                <span className="text-muted">{formatBytes(a.size)}</span>
              </span>
            ))}
          </div>
        )}

        <div className="mt-6 border-t border-hairline pt-6">
          {loading ? (
            <div className="space-y-3" aria-label="Chargement du message" role="status">
              <div className="skeleton h-3 w-11/12" />
              <div className="skeleton h-3 w-full" />
              <div className="skeleton h-3 w-9/12" />
              <div className="skeleton h-3 w-10/12" />
              <div className="skeleton h-32 w-full" />
            </div>
          ) : loadError ? (
            <ErrorState
              compact
              title="Message illisible"
              message={loadError}
              onRetry={() => {
                setLoadError('');
                setLoading(true);
                emailService
                  .getEmail(messageId, { markRead: true })
                  .then(({ data }) => setFull(data))
                  .catch((err) =>
                    setLoadError(err?.response?.data?.trim?.() || "Impossible de charger cet email.")
                  )
                  .finally(() => setLoading(false));
              }}
            />
          ) : html ? (
            <div className="email-body" dangerouslySetInnerHTML={{ __html: html }} />
          ) : text ? (
            <div className="email-body-plain">{text}</div>
          ) : (
            <div className="space-y-4">
              <p className="text-sm leading-relaxed text-ink-700">{merged.snippet}</p>
              <div className="flex items-center gap-2 rounded-xl bg-surface-sunken px-4 py-3 text-xs text-muted">
                <Mail size={16} />
                Cet email ne contient pas de texte affichable.
              </div>
            </div>
          )}
        </div>

        {visibleLabels.length > 0 && (
          <div className="mt-6 flex flex-wrap gap-2 border-t border-hairline pt-5">
            {(merged.labelIds || []).includes('STARRED') && (
              <span className="chip bg-caution-50 text-caution-700">
                <Star size={13} /> Favori
              </span>
            )}
            {visibleLabels.map((label) => (
              <span key={label} className="chip bg-ink-100 text-ink-700">
                <span className="max-w-[200px] truncate">{prettyLabel(label, labelsById)}</span>
              </span>
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}

export default EmailReader;
