import React, { useEffect, useState } from 'react';
import { accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { History as HistoryIcon, Undo, Archive, Trash, Mail, Tag } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { cn } from '../ui/cn';

// Human labels for the canonical ledger vocabulary. Forward triage actions and
// their inverses (logged when the user undoes one) both show up here.
const ACTION_META = {
  archive: { label: 'Archivé', Icon: Archive, tone: 'bg-sky-50 text-sky-700' },
  delete: { label: 'Supprimé', Icon: Trash, tone: 'bg-rose-50 text-rose-700' },
  trash: { label: 'Supprimé', Icon: Trash, tone: 'bg-rose-50 text-rose-700' },
  label: { label: 'Étiqueté', Icon: Tag, tone: 'bg-violet-50 text-violet-700' },
  star: { label: 'Favori', Icon: Tag, tone: 'bg-amber-50 text-amber-700' },
  read: { label: 'Lu', Icon: Mail, tone: 'bg-ink-100 text-ink-600' },
  unarchive: { label: 'Désarchivé', Icon: Undo, tone: 'bg-emerald-50 text-emerald-700' },
  untrash: { label: 'Restauré', Icon: Undo, tone: 'bg-emerald-50 text-emerald-700' },
  unread: { label: 'Marqué non lu', Icon: Mail, tone: 'bg-ink-100 text-ink-600' },
};

// Where an action came from — the ledger's truthful attribution.
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

const SOURCE_FILTERS = [
  { value: '', label: 'Tout' },
  { value: 'rule', label: 'Règles' },
  { value: 'ai', label: 'IA' },
  { value: 'bulk', label: 'En masse' },
  { value: 'direct', label: 'Direct' },
  { value: 'snooze', label: 'Reports' },
  { value: 'unsubscribe', label: 'Désabos' },
];

function formatWhen(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const opts = sameDay
    ? { hour: '2-digit', minute: '2-digit' }
    : { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' };
  return d.toLocaleDateString('fr-FR', opts);
}

function History() {
  const toast = useToast();
  const [entries, setEntries] = useState([]);
  const [loading, setLoading] = useState(true);
  const [source, setSource] = useState('');
  const [undoing, setUndoing] = useState(null);

  const load = async (src) => {
    setLoading(true);
    try {
      const { data } = await accountService.getActionLog({ source: src, limit: 100 });
      setEntries(data.entries || []);
    } catch (err) {
      toast.error("Impossible de charger l'historique.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(source);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source]);

  const handleUndo = async (entry) => {
    setUndoing(entry.id);
    try {
      await accountService.undoAction(entry.id);
      // Reflect the reversal locally: this entry is now undone (no longer undoable).
      setEntries((prev) => prev.map((e) => (e.id === entry.id ? { ...e, undone: true, undoable: false } : e)));
      toast.success('Action annulée — email restauré.');
    } catch (err) {
      toast.error(err.response?.data?.error || 'Annulation impossible.');
    } finally {
      setUndoing(null);
    }
  };

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-8 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <HistoryIcon size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Historique</h1>
          <p className="text-sm text-ink-500">
            Tout ce que Mailsorter a fait à votre place — et un bouton pour l'annuler.
          </p>
        </div>
      </div>

      <div className="mb-5 flex flex-wrap gap-2">
        {SOURCE_FILTERS.map((f) => (
          <button
            key={f.value}
            onClick={() => setSource(f.value)}
            className={cn(
              'rounded-lg px-3 py-1.5 text-sm font-semibold transition-colors',
              source === f.value ? 'bg-brand-50 text-brand-700' : 'text-ink-500 hover:bg-ink-100 hover:text-ink-900'
            )}
          >
            {f.label}
          </button>
        ))}
      </div>

      {loading ? (
        <div className="flex min-h-[40vh] items-center justify-center">
          <Spinner size={28} className="text-brand-500" />
        </div>
      ) : entries.length === 0 ? (
        <div className="card flex flex-col items-center justify-center px-6 py-20 text-center">
          <span className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-brand-50 text-brand-500">
            <HistoryIcon size={32} />
          </span>
          <h3 className="text-lg font-bold text-ink-900">Aucune action pour l'instant</h3>
          <p className="mt-1 max-w-xs text-sm text-ink-500">
            Dès que vous (ou l'auto-pilote) triez un email, l'action apparaîtra ici — avec une option pour la défaire.
          </p>
        </div>
      ) : (
        <div className="space-y-2.5">
          {entries.map((e) => {
            const meta = ACTION_META[e.action] || { label: e.action, Icon: Mail, tone: 'bg-ink-100 text-ink-600' };
            const Icon = meta.Icon;
            return (
              <div key={e.id} className="card flex flex-wrap items-center gap-3 p-4 animate-fade-up">
                <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-lg', meta.tone)}>
                  <Icon size={17} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-ink-900">{meta.label}</span>
                    {e.undone && (
                      <span className="chip bg-emerald-50 text-emerald-700 text-xs">Annulé</span>
                    )}
                  </div>
                  <div className="truncate text-xs text-ink-400">
                    {SOURCE_LABELS[e.source] || e.source} · {formatWhen(e.createdAt)}
                  </div>
                </div>
                {e.undoable ? (
                  <button
                    onClick={() => handleUndo(e)}
                    disabled={undoing === e.id}
                    className="btn-secondary"
                    title="Annuler cette action"
                  >
                    {undoing === e.id ? <Spinner size={16} /> : <Undo size={16} />} Annuler
                  </button>
                ) : (
                  <span className="text-xs text-ink-300">{e.undone ? '' : 'Non réversible'}</span>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export default History;
