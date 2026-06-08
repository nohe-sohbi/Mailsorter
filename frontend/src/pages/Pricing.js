import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { cn } from '../ui/cn';
import { Logo, Check, Bolt, Sparkles, Shield, Google } from '../ui/icons';

const PLANS = [
  {
    name: 'Free',
    price: '0€',
    cadence: '/ mois',
    tagline: 'Pour reprendre le contrôle.',
    cta: 'Plan actuel',
    highlight: false,
    features: [
      '200 emails analysés / mois',
      'Tri IA + suggestions',
      'Actions en masse par expéditeur',
      'Auto-pilote par expéditeur',
      'Raccourcis clavier & Inbox Zero',
    ],
  },
  {
    name: 'Pro',
    price: '7€',
    cadence: '/ mois',
    tagline: 'Pour ne plus jamais y penser.',
    cta: "Rejoindre la liste d'attente",
    highlight: true,
    features: [
      'Emails analysés illimités',
      'Tri automatique à chaque synchro',
      'Digest quotidien par email',
      'Plusieurs comptes Gmail',
      'Support prioritaire',
    ],
  },
];

const ACTION_COLORS = {
  archive: 'bg-sky-500',
  delete: 'bg-rose-500',
  label: 'bg-violet-500',
  keep: 'bg-emerald-500',
};
const ACTION_LABELS = { archive: 'Archivés', delete: 'Supprimés', label: 'Étiquetés', keep: 'Gardés' };

function Pricing() {
  const navigate = useNavigate();
  const toast = useToast();
  const loggedIn = !!localStorage.getItem('userEmail');
  const [usage, setUsage] = useState(null);
  const [activity, setActivity] = useState(null);

  useEffect(() => {
    if (!loggedIn) return;
    accountService.getUsage().then((r) => setUsage(r.data)).catch(() => {});
    accountService.getActivity().then((r) => setActivity(r.data)).catch(() => {});
  }, [loggedIn]);

  const handleWaitlist = () => {
    localStorage.setItem('mailsorter_pro_waitlist', '1');
    toast.success('Vous y êtes ! On vous prévient dès l’ouverture de Pro. 🚀');
  };

  const usedPct = usage ? Math.min(100, Math.round((usage.used / usage.limit) * 100)) : 0;
  const maxDay = activity ? Math.max(1, ...activity.days.map((d) => d.count)) : 1;

  return (
    <div className="min-h-screen bg-ink-50 bg-mesh">
      <div className="mx-auto max-w-5xl px-4 py-12 sm:px-6">
        <button onClick={() => navigate(loggedIn ? '/inbox' : '/')} className="mb-8 flex items-center gap-2.5 transition-opacity hover:opacity-80">
          <Logo size={30} />
          <span className="font-display text-lg font-extrabold tracking-tight text-ink-900">Mailsorter</span>
        </button>

        <div className="mx-auto max-w-2xl text-center">
          <span className="chip mb-4 bg-brand-50 text-brand-700">
            <Sparkles size={14} /> Tarifs simples, sans surprise
          </span>
          <h1 className="font-display text-3xl font-extrabold tracking-tight text-ink-900 sm:text-4xl">
            Commencez gratuitement.
            <br />
            Passez à Pro quand vous serez accro.
          </h1>
          <p className="mx-auto mt-3 max-w-md text-ink-500">
            Pas de carte bancaire pour démarrer. Annulable à tout moment.
          </p>
        </div>

        {/* Logged-in dashboard: usage + weekly recap */}
        {loggedIn && (
          <div className="mt-10 grid gap-4 sm:grid-cols-2">
            <div className="card p-6">
              <div className="mb-3 flex items-center justify-between">
                <h3 className="font-bold text-ink-900">Usage du mois</h3>
                <span className="chip bg-ink-100 text-ink-600">Plan Free</span>
              </div>
              <div className="mb-2 flex items-baseline gap-1.5">
                <span className="font-display text-3xl font-extrabold text-ink-900">{usage?.used ?? '—'}</span>
                <span className="text-sm text-ink-500">/ {usage?.limit ?? 200} emails analysés</span>
              </div>
              <div className="h-2.5 w-full overflow-hidden rounded-full bg-ink-100">
                <div
                  className={cn('h-full rounded-full transition-all duration-500', usedPct >= 100 ? 'bg-rose-500' : 'bg-brand-gradient')}
                  style={{ width: `${usedPct}%` }}
                />
              </div>
              <p className="mt-3 text-xs text-ink-400">
                Le cache et l'auto-pilote ne consomment pas votre quota.
              </p>
            </div>

            <div className="card p-6">
              <h3 className="mb-3 font-bold text-ink-900">Cette semaine</h3>
              <div className="mb-3 flex items-baseline gap-1.5">
                <span className="font-display text-3xl font-extrabold text-ink-900">{activity?.total ?? 0}</span>
                <span className="text-sm text-ink-500">emails triés</span>
              </div>
              <div className="flex h-16 items-end gap-1.5">
                {(activity?.days || Array.from({ length: 7 })).map((d, i) => (
                  <div key={i} className="flex flex-1 flex-col items-center gap-1">
                    <div
                      className="w-full rounded-md bg-brand-gradient transition-all"
                      style={{ height: `${d ? Math.max(6, (d.count / maxDay) * 100) : 6}%`, opacity: d && d.count ? 1 : 0.25 }}
                      title={d ? `${d.count} le ${d.date}` : ''}
                    />
                  </div>
                ))}
              </div>
              {activity && (
                <div className="mt-4 flex flex-wrap gap-3">
                  {Object.entries(activity.byAction)
                    .filter(([, v]) => v > 0)
                    .map(([k, v]) => (
                      <span key={k} className="flex items-center gap-1.5 text-xs text-ink-500">
                        <span className={cn('h-2.5 w-2.5 rounded-full', ACTION_COLORS[k])} />
                        {ACTION_LABELS[k]} · {v}
                      </span>
                    ))}
                </div>
              )}
            </div>
          </div>
        )}

        {/* Plans */}
        <div className="mt-10 grid gap-5 md:grid-cols-2">
          {PLANS.map((plan) => (
            <div
              key={plan.name}
              className={cn(
                'card relative flex flex-col p-7',
                plan.highlight && 'ring-2 ring-brand-500 shadow-glow'
              )}
            >
              {plan.highlight && (
                <span className="absolute -top-3 left-7 chip bg-brand-gradient text-white shadow-soft">
                  <Bolt size={13} /> Le plus populaire
                </span>
              )}
              <h3 className="text-lg font-bold text-ink-900">{plan.name}</h3>
              <p className="text-sm text-ink-500">{plan.tagline}</p>
              <div className="mt-4 flex items-baseline gap-1">
                <span className="font-display text-4xl font-extrabold text-ink-900">{plan.price}</span>
                <span className="text-sm text-ink-400">{plan.cadence}</span>
              </div>
              <ul className="mt-6 space-y-3">
                {plan.features.map((f) => (
                  <li key={f} className="flex items-start gap-2.5 text-sm text-ink-700">
                    <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-emerald-600">
                      <Check size={12} />
                    </span>
                    {f}
                  </li>
                ))}
              </ul>
              <div className="mt-7">
                {plan.highlight ? (
                  <button onClick={handleWaitlist} className="btn-primary w-full">
                    {plan.cta}
                  </button>
                ) : loggedIn ? (
                  <button disabled className="btn-secondary w-full cursor-default opacity-70">
                    <Check size={16} /> {plan.cta}
                  </button>
                ) : (
                  <button onClick={() => navigate('/')} className="btn-secondary w-full">
                    <Google size={16} /> Commencer gratuitement
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>

        <p className="mt-10 flex items-center justify-center gap-2 text-center text-xs text-ink-400">
          <Shield size={14} /> Paiements sécurisés · Données chiffrées · Résiliation en un clic
        </p>
      </div>
    </div>
  );
}

export default Pricing;
