import React, { useCallback, useEffect, useRef, useState } from 'react';
import DOMPurify from 'dompurify';
import { emailService, labelService, apiError } from '../services/api';
import { X, Archive, Trash, Mail, BellOff, Shield, Paperclip, Star, Download } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { ErrorState } from '../ui/primitives';
import { useToast } from '../ui/Toast';
import SnoozeButton from '../ui/SnoozeMenu';
import { cn } from '../ui/cn';

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
  onFlag,
  unsubscribing,
  onRead,
}) {
  const toast = useToast();
  const [full, setFull] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [downloading, setDownloading] = useState(null);
  const [labelsById, setLabelsById] = useState(null);
  const loadSeqRef = useRef(0);
  // Held in a ref so loadMessage can stay a stable callback.
  const onReadRef = useRef(onRead);
  onReadRef.current = onRead;
  const closeRef = useRef(null);
  const messageId = email?.messageId;

  // The list endpoint deliberately ships no bodies, so the reader fetches the
  // message it is about to show. Before this existed the panel had nothing to
  // render and every email read "Contenu complet indisponible".
  //
  // One implementation, shared by the initial load and the retry button. The
  // retry used to be a second, inline copy with no cancellation guard, so a
  // slow response could paint the body of a message the panel had already
  // moved off, and it never told the list the mail had been read.
  const loadMessage = useCallback(
    (id) => {
      if (!id) return;
      const run = ++loadSeqRef.current;
      const stale = () => run !== loadSeqRef.current;
      setFull(null);
      setLoadError('');
      setLoading(true);
      emailService
        .getEmail(id, { markRead: true })
        .then(({ data }) => {
          if (stale()) return;
          setFull(data);
          // Opening an email is what marks it read; tell the list so the unread
          // dot disappears without a full refetch.
          if (data?.isRead) onReadRef.current?.(id);
        })
        .catch((err) => {
          if (stale()) return;
          setLoadError(apiError(err, "Impossible de charger cet email."));
        })
        .finally(() => {
          if (!stale()) setLoading(false);
        });
    },
    // onRead is read through a ref: depending on it would rebuild this callback
    // on every parent render, refetching the message and re-marking it read.
    []
  );

  useEffect(() => {
    loadMessage(messageId);
    // Any response still in flight belongs to a message we are leaving.
    return () => {
      loadSeqRef.current += 1;
    };
  }, [messageId, loadMessage]);

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

  if (!email) return null;

  const merged = { ...email, ...(full || {}) };
  const canUnsubscribe = Boolean(merged.unsubUrl || merged.unsubMailto);
  const attachments = full?.attachments || [];
  // The list keeps a local isStarred flag (patched optimistically), while the
  // freshly loaded message only carries Gmail's labels. Read both so the toggle
  // reflects whichever one is newer.
  const starred = merged.isStarred ?? (merged.labelIds || []).includes('STARRED');

  // The download goes through axios because the route is session-authenticated:
  // a plain <a href> would send no Authorization header and get a 401. So the
  // bytes arrive as a Blob and we hand the browser an object URL, revoked right
  // after the click so the blob does not stay pinned in memory for the session.
  const handleDownload = async (attachment) => {
    if (!attachment?.attachmentId || !messageId) return;
    setDownloading(attachment.attachmentId);
    try {
      const { data } = await emailService.downloadAttachment(messageId, attachment.attachmentId);
      const objectUrl = URL.createObjectURL(data);
      const link = document.createElement('a');
      link.href = objectUrl;
      link.download = attachment.filename || 'piece-jointe';
      document.body.appendChild(link);
      link.click();
      link.remove();
      // Revoking in the same tick cancels the download on some browsers, which
      // read the blob after the click returns. A delay keeps it alive long
      // enough without leaking the buffer for the rest of the session.
      setTimeout(() => URL.revokeObjectURL(objectUrl), 60000);
    } catch (err) {
      // The error body is a Blob here (responseType), so the shared reader would
      // find no message: say plainly that the download failed instead.
      toast.error(apiError(err, 'Téléchargement impossible.'));
    } finally {
      setDownloading(null);
    }
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
          {onSnooze && <SnoozeButton onSnooze={onSnooze} ariaLabel="Reporter cet email" />}
          {onFlag && (
            <>
              {/* Favori and "unread again" are the two things a reader is for
                  besides getting rid of mail: flagging what matters and putting
                  back what you opened by mistake. Both were reachable only from
                  the list's keyboard shortcuts, i.e. not while reading. */}
              <button
                onClick={() => onFlag(starred ? 'unstar' : 'star')}
                className={cn(
                  'btn-ghost btn-sm btn-icon',
                  starred ? 'text-caution-600 hover:bg-caution-50' : 'hover:bg-ink-100'
                )}
                aria-pressed={starred}
                aria-label={starred ? 'Retirer des favoris' : 'Mettre en favori'}
                title={starred ? 'Retirer des favoris' : 'Mettre en favori'}
              >
                <Star size={18} className={starred ? 'fill-current' : undefined} />
              </button>
              <button
                onClick={() => onFlag('unread')}
                className="btn-ghost btn-sm btn-icon"
                aria-label="Marquer comme non lu"
                title="Marquer comme non lu (le remettre dans la pile)"
              >
                <Mail size={18} />
              </button>
            </>
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
            {attachments.map((a, i) => {
              // A part with no attachment id carries its bytes inline in the
              // payload; there is nothing to fetch, so it stays a plain chip
              // rather than a button that could only fail.
              const downloadable = Boolean(a.attachmentId);
              const label = `${a.filename} · ${a.mimeType || 'fichier'}`;
              if (!downloadable) {
                return (
                  <span key={`${a.filename}-${i}`} className="chip bg-ink-100 text-ink-700" title={label}>
                    <Paperclip size={13} />
                    <span className="max-w-[180px] truncate">{a.filename}</span>
                    <span className="text-muted">{formatBytes(a.size)}</span>
                  </span>
                );
              }
              const busy = downloading === a.attachmentId;
              return (
                <button
                  key={`${a.filename}-${i}`}
                  type="button"
                  onClick={() => handleDownload(a)}
                  disabled={busy}
                  className="chip bg-ink-100 text-ink-700 transition-colors hover:bg-brand-50 hover:text-brand-700 disabled:opacity-60"
                  title={`Télécharger ${label}`}
                >
                  {busy ? <Spinner size={13} /> : <Download size={13} />}
                  <span className="max-w-[180px] truncate">{a.filename}</span>
                  <span className="text-muted">{formatBytes(a.size)}</span>
                </button>
              );
            })}
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
              onRetry={() => loadMessage(messageId)}
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

        {/* STARRED est une étiquette système, donc filtrée de visibleLabels :
            gardée sur la seule longueur de cette liste, la puce « Favori » ne
            s'affichait que sur un email portant par ailleurs une étiquette
            utilisateur. Mettre en favori depuis le lecteur ne montrait alors
            rien du tout. */}
        {(starred || visibleLabels.length > 0) && (
          <div className="mt-6 flex flex-wrap gap-2 border-t border-hairline pt-5">
            {starred && (
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
