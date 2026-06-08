import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { authService } from '../services/api';
import { Logo, Google, Sparkles, Archive, Tag, Users, Shield, Bolt, Check } from '../ui/icons';
import Spinner from '../ui/Spinner';

const FEATURES = [
  {
    Icon: Sparkles,
    title: 'Tri par IA en un clic',
    text: "L'IA lit, comprend et classe vos emails comme un assistant humain — newsletters, factures, colis, spam.",
  },
  {
    Icon: Users,
    title: 'Règles par expéditeur',
    text: 'Apprenez une fois, appliquez pour toujours. Mailsorter mémorise vos préférences pour chaque expéditeur.',
  },
  {
    Icon: Archive,
    title: 'Nettoyage en masse',
    text: 'Archivez ou supprimez des centaines d’emails d’un coup. Inbox Zero en minutes, pas en heures.',
  },
  {
    Icon: Tag,
    title: 'Libellés intelligents',
    text: 'Des étiquettes précises et cohérentes, créées et appliquées automatiquement dans votre Gmail.',
  },
];

const STEPS = [
  { n: '01', title: 'Connectez Gmail', text: 'Authentification Google sécurisée. Aucun mot de passe stocké.' },
  { n: '02', title: 'Lancez l’analyse', text: 'L’IA passe votre boîte au crible et propose une action par email.' },
  { n: '03', title: 'Validez d’un geste', text: 'Acceptez, ajustez, ou laissez l’auto-pilote faire le ménage.' },
];

function Login() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (localStorage.getItem('userEmail')) {
      navigate('/inbox');
      return;
    }
    const code = searchParams.get('code');
    if (code) handleCallback(code);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams, navigate]);

  const handleCallback = async (code) => {
    setLoading(true);
    setError('');
    try {
      const response = await authService.handleCallback(code);
      localStorage.setItem('userEmail', response.data.userEmail);
      localStorage.setItem('accessToken', response.data.accessToken);
      navigate('/inbox');
    } catch (err) {
      setError("Échec de l'authentification. Réessayez.");
      setLoading(false);
    }
  };

  const handleLogin = async () => {
    setLoading(true);
    setError('');
    try {
      const response = await authService.getAuthUrl();
      window.location.href = response.data.authUrl;
    } catch (err) {
      setError('Impossible de démarrer la connexion. Réessayez.');
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen overflow-hidden bg-ink-950 text-white">
      {/* Ambient background */}
      <div className="pointer-events-none absolute inset-0 -z-0">
        <div className="absolute -top-40 left-1/2 h-[40rem] w-[40rem] -translate-x-1/2 rounded-full bg-brand-600/30 blur-[120px]" />
        <div className="absolute right-0 top-1/3 h-96 w-96 rounded-full bg-fuchsia-600/20 blur-[120px]" />
      </div>

      <div className="relative mx-auto max-w-6xl px-6 pb-20 pt-10">
        {/* Nav */}
        <nav className="flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <Logo size={32} />
            <span className="font-display text-lg font-extrabold tracking-tight">Mailsorter</span>
          </div>
          <span className="chip bg-white/10 text-white/80">
            <Shield size={14} /> OAuth Google sécurisé
          </span>
        </nav>

        {/* Hero */}
        <section className="grid items-center gap-12 pt-16 lg:grid-cols-2 lg:pt-24">
          <div className="animate-fade-up">
            <span className="chip mb-5 bg-white/10 text-brand-200">
              <Sparkles size={14} /> Propulsé par l'IA Mistral
            </span>
            <h1 className="font-display text-4xl font-extrabold leading-[1.05] tracking-tight sm:text-6xl">
              Votre boîte mail,
              <br />
              <span className="bg-gradient-to-r from-brand-300 via-fuchsia-300 to-sky-300 bg-clip-text text-transparent">
                triée pendant que vous dormez.
              </span>
            </h1>
            <p className="mt-6 max-w-xl text-lg leading-relaxed text-white/70">
              Mailsorter lit, comprend et range vos emails Gmail à votre place. Stop au scroll infini —
              atteignez l'Inbox Zero en quelques clics, et gardez-la propre pour toujours.
            </p>

            <div className="mt-8 flex flex-col items-start gap-4 sm:flex-row sm:items-center">
              <button
                onClick={handleLogin}
                disabled={loading}
                className="group inline-flex items-center justify-center gap-3 rounded-xl bg-white px-6 py-3.5 text-base font-bold text-ink-900 shadow-glow-lg transition-all hover:-translate-y-0.5 hover:shadow-glow-lg disabled:cursor-not-allowed disabled:opacity-60"
              >
                {loading ? (
                  <>
                    <Spinner size={20} className="text-brand-600" /> Connexion…
                  </>
                ) : (
                  <>
                    <Google size={20} /> Continuer avec Gmail
                  </>
                )}
              </button>
              <div className="flex items-center gap-2 text-sm text-white/60">
                <Check size={16} className="text-emerald-400" /> Gratuit · Sans carte bancaire
              </div>
            </div>

            {error && (
              <div className="mt-5 inline-flex items-center gap-2 rounded-xl border border-rose-400/30 bg-rose-500/10 px-4 py-2.5 text-sm text-rose-200">
                {error}
              </div>
            )}
          </div>

          {/* Visual mock */}
          <div className="animate-fade-up [animation-delay:120ms]">
            <div className="relative rounded-3xl border border-white/10 bg-white/[0.04] p-3 shadow-glow-lg backdrop-blur">
              <div className="rounded-2xl bg-ink-900/80 p-5">
                <div className="mb-4 flex items-center justify-between">
                  <span className="text-sm font-semibold text-white/80">Suggestions IA</span>
                  <span className="chip bg-brand-500/20 text-brand-200">3 prêtes</span>
                </div>
                <div className="space-y-2.5">
                  {[
                    { Icon: Archive, c: 'text-sky-300', from: 'Medium Digest', act: 'Archiver', conf: 96 },
                    { Icon: Tag, c: 'text-violet-300', from: 'Amazon', act: 'Libellé · Achats', conf: 92 },
                    { Icon: Sparkles, c: 'text-rose-300', from: 'Promo Casino', act: 'Supprimer', conf: 88 },
                  ].map((r, i) => (
                    <div
                      key={i}
                      className="flex items-center gap-3 rounded-xl border border-white/5 bg-white/[0.03] p-3"
                    >
                      <span className={`flex h-9 w-9 items-center justify-center rounded-lg bg-white/5 ${r.c}`}>
                        <r.Icon size={18} />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-semibold text-white/90">{r.from}</div>
                        <div className="text-xs text-white/50">{r.act}</div>
                      </div>
                      <span className="chip bg-emerald-500/15 text-emerald-300">{r.conf}%</span>
                    </div>
                  ))}
                </div>
                <button className="mt-4 flex w-full items-center justify-center gap-2 rounded-xl bg-brand-gradient py-3 text-sm font-bold text-white">
                  <Bolt size={16} /> Tout appliquer
                </button>
              </div>
            </div>
          </div>
        </section>

        {/* Social proof / stats */}
        <section className="mt-20 grid grid-cols-2 gap-4 sm:grid-cols-4">
          {[
            ['10×', 'plus rapide qu’à la main'],
            ['< 2 min', 'pour vider 500 emails'],
            ['0', 'mot de passe stocké'],
            ['100%', 'sous votre contrôle'],
          ].map(([big, small]) => (
            <div key={small} className="rounded-2xl border border-white/10 bg-white/[0.03] p-5 text-center">
              <div className="font-display text-3xl font-extrabold text-white">{big}</div>
              <div className="mt-1 text-xs text-white/55">{small}</div>
            </div>
          ))}
        </section>

        {/* Features */}
        <section className="mt-24">
          <h2 className="font-display text-3xl font-extrabold tracking-tight sm:text-4xl">
            Tout ce qu'une boîte mail aurait dû faire seule.
          </h2>
          <div className="mt-10 grid gap-4 sm:grid-cols-2">
            {FEATURES.map(({ Icon, title, text }) => (
              <div
                key={title}
                className="group rounded-2xl border border-white/10 bg-white/[0.03] p-6 transition-colors hover:border-brand-400/40 hover:bg-white/[0.05]"
              >
                <span className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-xl bg-brand-gradient text-white">
                  <Icon size={22} />
                </span>
                <h3 className="text-lg font-bold text-white">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-white/60">{text}</p>
              </div>
            ))}
          </div>
        </section>

        {/* How it works */}
        <section className="mt-24">
          <h2 className="font-display text-3xl font-extrabold tracking-tight sm:text-4xl">Trois étapes. Zéro effort.</h2>
          <div className="mt-10 grid gap-4 md:grid-cols-3">
            {STEPS.map(({ n, title, text }) => (
              <div key={n} className="rounded-2xl border border-white/10 bg-white/[0.03] p-6">
                <div className="font-display text-4xl font-extrabold text-brand-400/40">{n}</div>
                <h3 className="mt-3 text-lg font-bold text-white">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-white/60">{text}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Final CTA */}
        <section className="mt-24 overflow-hidden rounded-3xl border border-white/10 bg-brand-gradient p-10 text-center sm:p-16">
          <h2 className="font-display text-3xl font-extrabold tracking-tight sm:text-4xl">
            Reprenez le contrôle de votre inbox.
          </h2>
          <p className="mx-auto mt-3 max-w-md text-white/80">
            Connectez Gmail et regardez le désordre disparaître. C'est gratuit, et ça prend 30 secondes.
          </p>
          <button
            onClick={handleLogin}
            disabled={loading}
            className="mt-8 inline-flex items-center justify-center gap-3 rounded-xl bg-white px-7 py-3.5 text-base font-bold text-ink-900 shadow-xl transition-transform hover:-translate-y-0.5 disabled:opacity-60"
          >
            {loading ? <Spinner size={20} className="text-brand-600" /> : <Google size={20} />}
            Commencer maintenant
          </button>
        </section>

        <footer className="mt-16 flex flex-col items-center justify-between gap-4 border-t border-white/10 pt-8 text-sm text-white/40 sm:flex-row">
          <span>© {new Date().getFullYear()} Mailsorter</span>
          <span className="flex items-center gap-2">
            <Shield size={14} /> Vos emails ne quittent jamais votre contrôle.
          </span>
        </footer>
      </div>
    </div>
  );
}

export default Login;
