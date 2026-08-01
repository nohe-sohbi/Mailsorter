import React, { useCallback, useEffect, useState } from 'react';
import { accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { actionMeta } from '../ui/actions';
import { EmptyState, ErrorState, LiveAnnouncer } from '../ui/primitives';
import { History as HistoryIcon, Undo, Search } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { cn } from '../ui/cn';

// Une page à la fois : le serveur plafonne à 200, mais 50 lignes remplissent
// déjà l'écran et rendent « Charger plus » utile plutôt que décoratif.
const PAGE_SIZE = 50;

// Où l'action a été décidée : l'attribution honnête du ledger.
const SOURCE_LABELS = {
  direct: 'Action directe',
  rule: 'Règle',
  ai: 'IA',
  'ai-auto': 'Auto-pilote IA',
  bulk: 'Action en masse',
  snooze: 'Report',
  unsubscribe: 'Désabonnement',
  undo: 'Annulation',
};

// Le filtre est envoyé tel quel au serveur, qui compare la source à l'identique.
// Il faut donc une entrée par source réelle : « ai » seul laissait l'auto-pilote
// (`ai-auto`) et les annulations (`undo`) totalement hors d'atteinte.
const SOURCE_FILTERS = [
  { value: '', label: 'Tout' },
  { value: 'rule', label: 'Règles' },
  { value: 'ai', label: 'IA' },
  { value: 'ai-auto', label: 'Auto-pilote' },
  { value: 'bulk', label: 'En masse' },
  { value: 'direct', label: 'Direct' },
  { value: 'snooze', label: 'Reports' },
  { value: 'unsubscribe', label: 'Désabos' },
  { value: 'undo', label: 'Annulations' },
];

const senderName = (from = '') => from.split('<')[0].replace(/"/g, '').trim() || from;

function formatWhen(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  if (Number.isNaN(d.getTime())) return '';
  const sameDay = d.toDateString() === new Date().toDateString();
  // toLocaleDateString ignore `hour`/`minute` : le jour même affichait donc la
  // date complète (« 01/08/2026 ») là où on ne voulait que l'heure.
  return sameDay
    ? d.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleString('fr-FR', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}

function fullWhen(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString('fr-FR', { dateStyle: 'full', timeStyle: 'short' });
}

// Le ledger n'a pas toujours porté l'identité du message, et le serveur ne peut
// la reconstituer que si l'email est encore en cache. Plutôt qu'une ligne muette
// (« Archivé · Règle · 14:32 », sans dire de quel email on parle), on nomme
// explicitement le trou.
const UNKNOWN_HINT =
  "Cette entrée est antérieure à l'enregistrement du sujet, ou l'email n'est plus en cache.";

function identityOf(entry) {
  const subject = (entry.subject || '').trim();
  const from = (entry.from || '').trim();
  return {
    known: Boolean(subject || from),
    title: subject || (from ? '(Sans sujet)' : 'Email non identifié'),
    sender: from ? senderName(from) : '',
    from,
  };
}

function History() {
  const toast = useToast();
  const confirm = useConfirm();
  const [entries, setEntries] = useState([]);
  const [nextBefore, setNextBefore] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState(null);
  const [source, setSource] = useState('');
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [undoing, setUndoing] = useState(null);

  // La recherche part au serveur : sans anti-rebond, taper « facture » ferait
  // sept requêtes dont six jetées, et l'ordre des réponses n'est pas garanti.
  useEffect(() => {
    const t = setTimeout(() => setQuery(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await accountService.getActionLog({ source, q: query, limit: PAGE_SIZE });
      setEntries(data.entries || []);
      setNextBefore(data.nextBefore || '');
    } catch (err) {
      // Une panne ne doit jamais se déguiser en « aucune action » : on retient
      // l'erreur pour rendre un état distinct, avec un vrai bouton Réessayer.
      setError(err.response?.data?.error || "L'historique n'a pas pu être chargé.");
      setEntries([]);
      setNextBefore('');
    } finally {
      setLoading(false);
    }
  }, [source, query]);

  useEffect(() => {
    load();
  }, [load]);

  const loadMore = async () => {
    if (!nextBefore || loadingMore) return;
    setLoadingMore(true);
    try {
      const { data } = await accountService.getActionLog({
        source,
        q: query,
        limit: PAGE_SIZE,
        before: nextBefore,
      });
      const page = data.entries || [];
      // Le curseur est temporel : une action enregistrée entre deux pages peut
      // décaler la fenêtre, on dédoublonne sur l'id plutôt que d'afficher deux
      // fois la même ligne.
      setEntries((prev) => {
        const seen = new Set(prev.map((e) => e.id));
        return prev.concat(page.filter((e) => !seen.has(e.id)));
      });
      setNextBefore(data.nextBefore || '');
    } catch (err) {
      toast.error("Impossible de charger la suite de l'historique.");
    } finally {
      setLoadingMore(false);
    }
  };

  const handleUndo = async (entry) => {
    const meta = actionMeta(entry.action);
    const identity = identityOf(entry);
    // Le ledger n'a pas d'inverse pour un inverse : une fois la ligne annulée,
    // le bouton disparaît définitivement. `danger` reste faux parce que l'effet
    // est restaurateur (l'email revient) — le peindre en rouge mentirait.
    if (
      !(await confirm({
        title: 'Annuler cette action ?',
        message: `« ${identity.title} » : l'action « ${meta.past} » sera défaite dans votre boîte Gmail.`,
        detail: "Cette annulation est définitive : elle ne pourra pas être refaite depuis l'historique.",
        confirmLabel: "Annuler l'action",
        cancelLabel: 'Revenir',
        danger: false,
      }))
    ) {
      return;
    }

    setUndoing(entry.id);
    try {
      await accountService.undoAction(entry.id);
      // Reflect the reversal locally: this entry is now undone (no longer undoable).
      setEntries((prev) => prev.map((e) => (e.id === entry.id ? { ...e, undone: true, undoable: false } : e)));
      toast.success('Action annulée, email restauré.');
    } catch (err) {
      // 409 = déjà annulée ailleurs (autre onglet, autre session) : la ligne
      // affichée est périmée, on la remet d'aplomb au lieu de laisser un bouton
      // qui échouera à chaque clic.
      if (err.response?.status === 409) {
        setEntries((prev) => prev.map((e) => (e.id === entry.id ? { ...e, undone: true, undoable: false } : e)));
      }
      toast.error(err.response?.data?.error || 'Annulation impossible.');
    } finally {
      setUndoing(null);
    }
  };

  const filtered = Boolean(query) || Boolean(source);
  const plural = entries.length > 1 ? 's' : '';
  const announcement = loading
    ? ''
    : error
    ? "Historique indisponible."
    : `${entries.length} action${plural} affichée${plural}.`;

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <LiveAnnouncer message={announcement} />

      <div className="mb-6 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <HistoryIcon size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Historique</h1>
          <p className="text-sm text-muted">
            Tout ce que Mailsorter a fait à votre place, et un bouton pour l'annuler.
          </p>
        </div>
      </div>

      <div className="mb-5 space-y-3">
        <div className="relative">
          <Search
            size={16}
            className="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-subtle"
          />
          <input
            type="search"
            className="input pl-10"
            placeholder="Rechercher un sujet ou un expéditeur…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Rechercher dans l'historique"
          />
        </div>

        <div role="group" aria-label="Filtrer par origine" className="flex flex-wrap gap-1.5">
          {SOURCE_FILTERS.map((f) => (
            <button
              key={f.value}
              type="button"
              onClick={() => setSource(f.value)}
              aria-pressed={source === f.value}
              // Même base pour les deux états : seules les couleurs changent, donc
              // la barre de filtres ne se réaligne pas quand on clique.
              className={cn(
                'btn-ghost btn-sm',
                source === f.value && 'bg-brand-600 text-white shadow-soft hover:bg-brand-700 hover:text-white'
              )}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <div className="flex min-h-[40vh] items-center justify-center">
          <Spinner size={28} className="text-brand-500" />
        </div>
      ) : error ? (
        <div className="card">
          <ErrorState
            title="Historique indisponible"
            message={error}
            onRetry={load}
          />
        </div>
      ) : entries.length === 0 ? (
        <div className="card">
          {filtered ? (
            <EmptyState
              Icon={Search}
              tone="neutral"
              title="Aucune action ne correspond"
              description="Essayez un autre mot-clé, ou revenez à l'historique complet."
              action={
                <button
                  className="btn-secondary"
                  onClick={() => {
                    setSearch('');
                    setSource('');
                  }}
                >
                  Tout afficher
                </button>
              }
            />
          ) : (
            <EmptyState
              Icon={HistoryIcon}
              title="Aucune action pour l'instant"
              description="Dès que vous (ou l'auto-pilote) triez un email, l'action apparaîtra ici, avec une option pour la défaire."
            />
          )}
        </div>
      ) : (
        <>
          <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <p className="text-sm text-muted">
              <span className="font-semibold text-ink-900">{entries.length}</span> action{plural} affichée{plural}
              {nextBefore ? ", l'historique continue plus bas" : ''}
            </p>
            <p className="text-xs text-muted">Les actions réversibles s'annulent d'un clic.</p>
          </div>

          <ul className="space-y-2.5">
            {entries.map((e) => {
              const meta = actionMeta(e.action);
              const Icon = meta.Icon;
              const identity = identityOf(e);
              return (
                <li key={e.id} className="card flex items-start gap-3 p-3.5 animate-fade-up sm:items-center">
                  <span
                    aria-hidden="true"
                    className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-lg', meta.chip)}
                  >
                    <Icon size={17} />
                  </span>

                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span
                        className={cn(
                          'truncate font-semibold',
                          identity.known ? 'text-ink-900' : 'italic text-muted'
                        )}
                        title={identity.known ? identity.title : UNKNOWN_HINT}
                      >
                        {identity.title}
                      </span>
                      {e.undone && <span className="chip shrink-0 bg-positive-50 text-positive-700">Annulé</span>}
                    </div>
                    <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 text-xs text-muted">
                      <span className="font-semibold text-ink-700">{meta.past}</span>
                      {identity.sender && (
                        <>
                          <span aria-hidden="true" className="text-subtle">·</span>
                          <span className="max-w-[16rem] truncate" title={identity.from}>
                            {identity.sender}
                          </span>
                        </>
                      )}
                      <span aria-hidden="true" className="text-subtle">·</span>
                      <span>{SOURCE_LABELS[e.source] || e.source}</span>
                      <span aria-hidden="true" className="text-subtle">·</span>
                      <time dateTime={e.createdAt} title={fullWhen(e.createdAt)}>
                        {formatWhen(e.createdAt)}
                      </time>
                    </div>
                  </div>

                  {e.undoable ? (
                    <button
                      onClick={() => handleUndo(e)}
                      disabled={undoing === e.id}
                      className="btn-secondary btn-sm shrink-0"
                      title="Annuler cette action"
                    >
                      {undoing === e.id ? <Spinner size={14} /> : <Undo size={14} />} Annuler
                    </button>
                  ) : (
                    !e.undone && (
                      <span
                        className="shrink-0 text-xs text-muted"
                        title="Cette action n'a pas d'inverse automatique."
                      >
                        Non réversible
                      </span>
                    )
                  )}
                </li>
              );
            })}
          </ul>

          {nextBefore && (
            <div className="mt-5 flex justify-center">
              <button onClick={loadMore} disabled={loadingMore} className="btn-secondary">
                {loadingMore && <Spinner size={16} />} Charger plus
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

export default History;
