import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { accountService, billingService, waitlistService } from '../services/api';
import { useInstance } from '../contexts/InstanceContext';
import { useToast } from '../ui/Toast';
import { cn } from '../ui/cn';
import { track } from '../lib/analytics';
import Spinner from '../ui/Spinner';
import { Logo, Check, Bolt, Sparkles, Shield, Google } from '../ui/icons';
import { actionMeta } from '../ui/actions';

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


// Local hint that this browser already signed up, so we show the confirmed
// state instead of the form. The server is the real record: signing up again
// from another device is an idempotent upsert, not a duplicate.
const WAITLIST_KEY = 'mailsorter_pro_waitlist';

function Pricing() {
  const navigate = useNavigate();
  const toast = useToast();
  const [searchParams, setSearchParams] = useSearchParams();
  const loggedIn = !!localStorage.getItem('userEmail');
  const [usage, setUsage] = useState(null);
  const [activity, setActivity] = useState(null);
  const [upgrading, setUpgrading] = useState(false);
  const [managing, setManaging] = useState(false);
  const [joined, setJoined] = useState(() => localStorage.getItem(WAITLIST_KEY) === '1');
  const [waitlistEmail, setWaitlistEmail] = useState('');
  const [joining, setJoining] = useState(false);

  const isPro = usage?.plan === 'pro';
  // Whether Pro can actually be bought comes from the PUBLIC instance probe,
  // not from /api/usage: logged-out visitors have no usage payload, and reading
  // billing state off a failed request would silently show the waitlist to
  // everyone. The probe is the shared one, so this page cannot disagree with
  // the header about the same instance. An unreachable API resolves to
  // billing off there, the state that cannot promise a checkout we may not be
  // able to honour. null means "not answered yet", which is why the CTA waits
  // rather than flashing the wrong button.
  const { loading: instanceUnknown, billingOn: canBuyPro } = useInstance();
  const billingOn = instanceUnknown ? null : canBuyPro;

  useEffect(() => {
    if (!loggedIn) return;
    accountService.getUsage().then((r) => setUsage(r.data)).catch(() => {});
    accountService.getActivity().then((r) => setActivity(r.data)).catch(() => {});
  }, [loggedIn]);

  // Handle the post-Checkout redirect (success / cancel), then clean the URL.
  useEffect(() => {
    const status = searchParams.get('checkout');
    if (!status) return;
    if (status === 'success') {
      toast.success('Bienvenue dans Pro ! Analyses illimitées débloquées. 🎉');
      accountService.getUsage().then((r) => setUsage(r.data)).catch(() => {});
    } else if (status === 'cancel') {
      toast.info('Paiement annulé. Vous pouvez réessayer quand vous voulez.');
    }
    searchParams.delete('checkout');
    setSearchParams(searchParams, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleUpgrade = async () => {
    if (!loggedIn) {
      navigate('/');
      return;
    }
    setUpgrading(true);
    try {
      const { data } = await billingService.checkout();
      track('upgrade_start');
      window.location.href = data.url;
    } catch (err) {
      const status = err.response?.status;
      if (status === 503) toast.error('Le paiement n’est pas encore activé. Réessayez bientôt.');
      else if (status === 409) toast.info('Vous êtes déjà abonné à Pro.');
      else toast.error('Impossible de démarrer le paiement. Réessayez.');
      setUpgrading(false);
    }
  };

  const handleManage = async () => {
    setManaging(true);
    try {
      const { data } = await billingService.portal();
      window.location.href = data.url;
    } catch (err) {
      const status = err.response?.status;
      if (status === 404) toast.info('Aucun abonnement à gérer pour le moment.');
      else toast.error('Impossible d’ouvrir le portail de facturation.');
      setManaging(false);
    }
  };

  // Logged-in visitors sign up with the address they already gave us; cold
  // traffic types one. Either way the address reaches the server, which is the
  // whole point: a waitlist nobody can read is not a waitlist.
  const handleWaitlist = async (e) => {
    if (e) e.preventDefault();
    const email = loggedIn ? localStorage.getItem('userEmail') : waitlistEmail.trim();
    if (!email) return;

    setJoining(true);
    try {
      await waitlistService.join(email);
      // Boolean only: the address itself must never reach the analytics.
      track('waitlist_join', { loggedIn });
      localStorage.setItem(WAITLIST_KEY, '1');
      setJoined(true);
      setWaitlistEmail('');
      toast.success('C’est noté. On vous écrit dès l’ouverture de Pro. 🚀');
    } catch (err) {
      if (err.response?.status === 400) toast.error('Cette adresse email n’est pas valide.');
      else toast.error('Inscription impossible pour le moment. Réessayez.');
    } finally {
      setJoining(false);
    }
  };

  const usedPct = usage && usage.limit > 0 ? Math.min(100, Math.round((usage.used / usage.limit) * 100)) : 0;
  const maxDay = activity ? Math.max(1, ...activity.days.map((d) => d.count)) : 1;

  return (
    <div className="min-h-screen bg-ink-50">
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
                <span className={cn('chip', isPro ? 'bg-brand-fill text-white' : 'bg-ink-100 text-ink-600')}>
                  {isPro ? <><Bolt size={13} /> Plan Pro</> : 'Plan Free'}
                </span>
              </div>
              <div className="mb-2 flex items-baseline gap-1.5">
                <span className="font-display text-3xl font-extrabold text-ink-900">{usage?.used ?? '-'}</span>
                <span className="text-sm text-ink-500">
                  {isPro ? 'emails analysés · illimité' : `/ ${usage?.limit ?? 200} emails analysés`}
                </span>
              </div>
              <div className="h-2.5 w-full overflow-hidden rounded-full bg-ink-100">
                <div
                  className={cn(
                    'h-full rounded-full transition-all duration-500',
                    isPro ? 'bg-positive-fill' : usedPct >= 100 ? 'bg-danger-fill' : 'bg-brand-fill'
                  )}
                  style={{ width: isPro ? '100%' : `${usedPct}%` }}
                />
              </div>
              <p className="mt-3 text-xs text-muted">
                {isPro
                  ? 'Analyses illimitées. Gérez votre abonnement à tout moment.'
                  : "Le cache et l'auto-pilote ne consomment pas votre quota."}
              </p>
              {isPro && billingOn && (
                <button onClick={handleManage} disabled={managing} className="btn-secondary mt-4 w-full">
                  {managing ? <Spinner size={16} /> : <Shield size={16} />}
                  {managing ? 'Redirection…' : 'Gérer mon abonnement'}
                </button>
              )}
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
                      className="w-full rounded-md bg-brand-fill transition-all"
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
                        <span className={cn('h-2.5 w-2.5 rounded-full', actionMeta(k).solid)} />
                        {actionMeta(k).past} · {v}
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
                plan.highlight && 'ring-2 ring-brand-500 shadow-card'
              )}
            >
              {plan.highlight && (
                <span className="absolute -top-3 left-7 chip bg-brand-fill text-white shadow-soft">
                  <Bolt size={13} /> Le plus populaire
                </span>
              )}
              <h3 className="text-lg font-bold text-ink-900">{plan.name}</h3>
              <p className="text-sm text-ink-500">{plan.tagline}</p>
              <div className="mt-4 flex items-baseline gap-1">
                <span className="font-display text-4xl font-extrabold text-ink-900">{plan.price}</span>
                <span className="text-sm text-muted">{plan.cadence}</span>
              </div>
              <ul className="mt-6 space-y-3">
                {plan.features.map((f) => (
                  <li key={f} className="flex items-start gap-2.5 text-sm text-ink-700">
                    <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-positive-100 text-positive-600">
                      <Check size={12} />
                    </span>
                    {f}
                  </li>
                ))}
              </ul>
              <div className="mt-7">
                {plan.highlight ? (
                  billingOn === null ? (
                    // Hold the slot until we know whether checkout is open, so
                    // the button never flips from waitlist to upgrade mid-read.
                    <button disabled className="btn-primary w-full cursor-default opacity-60">
                      <Spinner size={18} />
                    </button>
                  ) : isPro ? (
                    billingOn ? (
                      <button onClick={handleManage} disabled={managing} className="btn-primary w-full">
                        {managing ? <Spinner size={18} /> : <Shield size={16} />}
                        {managing ? 'Redirection…' : 'Gérer mon abonnement'}
                      </button>
                    ) : (
                      <button disabled className="btn-primary w-full cursor-default opacity-80">
                        <Check size={16} /> Votre plan
                      </button>
                    )
                  ) : billingOn ? (
                    loggedIn ? (
                      <button onClick={handleUpgrade} disabled={upgrading} className="btn-primary w-full">
                        {upgrading ? <Spinner size={18} /> : <Bolt size={16} />}
                        {upgrading ? 'Redirection…' : 'Passer à Pro'}
                      </button>
                    ) : (
                      <button onClick={() => navigate('/')} className="btn-primary w-full">
                        <Google size={16} /> Commencer gratuitement
                      </button>
                    )
                  ) : joined ? (
                    <button disabled className="btn-primary w-full cursor-default opacity-80">
                      <Check size={16} /> Vous êtes sur la liste
                    </button>
                  ) : loggedIn ? (
                    <button onClick={handleWaitlist} disabled={joining} className="btn-primary w-full">
                      {joining ? <Spinner size={18} /> : <Sparkles size={16} />} {plan.cta}
                    </button>
                  ) : (
                    <form onSubmit={handleWaitlist} className="space-y-2">
                      <label htmlFor="waitlist-email" className="sr-only">
                        Votre adresse email
                      </label>
                      <input
                        id="waitlist-email"
                        type="email"
                        required
                        value={waitlistEmail}
                        onChange={(e) => setWaitlistEmail(e.target.value)}
                        placeholder="vous@exemple.com"
                        className="input"
                      />
                      <button type="submit" disabled={joining} className="btn-primary w-full">
                        {joining ? <Spinner size={18} /> : <Sparkles size={16} />} {plan.cta}
                      </button>
                    </form>
                  )
                ) : loggedIn ? (
                  <button disabled className="btn-secondary w-full cursor-default opacity-70">
                    <Check size={16} /> {isPro ? 'Inclus' : 'Plan actuel'}
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

        <p className="mt-10 flex items-center justify-center gap-2 text-center text-xs text-muted">
          <Shield size={14} /> Paiements sécurisés · Données chiffrées · Résiliation en un clic
        </p>
      </div>
    </div>
  );
}

export default Pricing;
