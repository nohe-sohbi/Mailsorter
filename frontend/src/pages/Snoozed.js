import React, { useEffect, useState } from 'react';
import { snoozeService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Clock, Undo, Mail } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { cn } from '../ui/cn';

const AVATAR_GRADIENTS = [
  'bg-brand-500', 'bg-sky-500',
  'bg-emerald-500', 'bg-amber-500', 'bg-rose-500',
];
const gradientFor = (seed = '') => {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_GRADIENTS[h % AVATAR_GRADIENTS.length];
};
const senderName = (from = '') => from.split('<')[0].replace(/"/g, '').trim() || from;

function formatWake(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const opts = sameDay
    ? { hour: '2-digit', minute: '2-digit' }
    : { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' };
  return d.toLocaleDateString('fr-FR', opts);
}

function Snoozed() {
  const toast = useToast();
  const [snoozes, setSnoozes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [waking, setWaking] = useState(null);

  const load = async () => {
    try {
      const { data } = await snoozeService.list('scheduled');
      setSnoozes(data.snoozes || []);
    } catch (err) {
      toast.error('Impossible de charger les emails reportés.');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleWake = async (snooze) => {
    setWaking(snooze.id);
    try {
      await snoozeService.wake(snooze.id);
      setSnoozes((prev) => prev.filter((s) => s.id !== snooze.id));
      toast.success('Email réactivé — de retour dans votre boîte');
    } catch (err) {
      toast.error('Réactivation impossible. Réessayez.');
    } finally {
      setWaking(null);
    }
  };

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-8 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <Clock size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Reporté</h1>
          <p className="text-sm text-ink-500">Les emails sortis de votre boîte, qui reviendront au bon moment.</p>
        </div>
      </div>

      {loading ? (
        <div className="flex min-h-[40vh] items-center justify-center">
          <Spinner size={28} className="text-brand-500" />
        </div>
      ) : snoozes.length === 0 ? (
        <div className="card flex flex-col items-center justify-center px-6 py-20 text-center">
          <span className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-brand-50 text-brand-500">
            <Clock size={32} />
          </span>
          <h3 className="text-lg font-bold text-ink-900">Rien en attente</h3>
          <p className="mt-1 max-w-xs text-sm text-ink-500">
            Depuis le lecteur d'email, utilisez « Reporter » pour mettre un email de côté jusqu'au moment qui vous arrange.
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {snoozes.map((s) => (
            <div key={s.id} className="card flex flex-wrap items-center gap-4 p-4 animate-fade-up">
              <span className={cn('flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white', gradientFor(s.from))}>
                {senderName(s.from)[0]?.toUpperCase() || '?'}
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate font-semibold text-ink-900">{s.subject || '(Sans sujet)'}</div>
                <div className="flex items-center gap-1.5 truncate text-xs text-ink-400">
                  <Mail size={12} /> {senderName(s.from) || 'Expéditeur inconnu'}
                </div>
              </div>
              <span className="chip bg-brand-50 text-brand-700" title="Retour prévu">
                <Clock size={13} /> {formatWake(s.wakeAt)}
              </span>
              <button
                onClick={() => handleWake(s)}
                disabled={waking === s.id}
                className="btn-secondary"
                title="Ramener cet email dans la boîte maintenant"
              >
                {waking === s.id ? <Spinner size={16} /> : <Undo size={16} />} Réactiver
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default Snoozed;
