import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { authService, configService, waitlistService, apiError } from '../services/api';
import { useInstance } from '../contexts/InstanceContext';
import { track } from '../lib/analytics';
import { hasJoinedWaitlist, rememberWaitlistJoin, waitlistEmail as getWaitlistEmail, forgetWaitlistJoin } from '../lib/waitlist';
import { isAuthed } from '../lib/session';
import PublicFooter, { SOURCE_URL } from '../components/PublicFooter';
import {
  Logo, Google, Sparkles, Archive, Tag, Users, Shield, Bolt, Check, BellOff,
  Lock, Server, Undo, Mail, ChevronDown,
} from '../ui/icons';
import Spinner from '../ui/Spinner';

const GMAIL_ONLY = 'labels';

const FEATURES = [
  {
    Icon: Sparkles,
    title: 'Tri par IA en un clic',
    text: "L'IA lit, comprend et classe vos emails comme un assistant humain : newsletters, factures, colis, spam.",
  },
  {
    Icon: BellOff,
    title: 'Désabonnement en 1 clic',
    text: 'Mailsorter traque les newsletters qui vous noient et vous désabonne instantanément, sans formulaire ni sortie de l\'app.',
  },
  {
    Icon: Users,
    title: 'Règles par expéditeur',
    text: 'Apprenez une fois, appliquez pour toujours. Mailsorter mémorise vos préférences pour chaque expéditeur.',
  },
  {
    Icon: Archive,
    title: 'Nettoyage en masse',
    text: 'Archivez ou supprimez des centaines d\'emails d\'un coup. Inbox Zero en minutes, pas en heures.',
  },
  {
    Icon: Tag,
    title: 'Libellés intelligents',
    text: 'Des étiquettes précises et cohérentes, créées et appliquées automatiquement dans votre Gmail.',
    soon: true,
    needs: GMAIL_ONLY,
  },
];

const STEPS = [
  {
    n: '01',
    title: 'Créez votre compte',
    text: 'Inscription en 10 secondes. Vos accès et vos préférences restent sous votre contrôle.',
  },
  {
    n: '02',
    title: 'Définissez la boîte à ranger',
    text: 'Connectez Gmail en un clic ou n\'importe quelle messagerie (Orange, Outlook, Yahoo...) avec un mot de passe d\'application.',
  },
  {
    n: '03',
    title: 'Laissez l\'IA faire le tri',
    text: 'L\'IA analyse et propose un tri sur mesure. Validez en un clic ou activez l\'autopilote.',
  },
];

const FACTS = [
  { big: '0', small: 'mot de passe stocké', title: 'La connexion passe par OAuth Google ou mot de passe d\'application scellé.' },
  { big: 'AES-256', small: 'chiffré au repos', title: 'Vos identifiants et jetons sont scellés en AES-256-GCM avant d\'atteindre la base.' },
  { big: '200', small: 'emails analysés / mois offerts', title: 'Quota du plan gratuit. Le cache et l\'auto-pilote ne le consomment pas.' },
  { big: '1 clic', small: 'pour annuler une action', title: 'Chaque action est journalisée avec son inverse, et annulable depuis l\'historique.' },
];

const TRUST = [
  {
    Icon: Lock,
    title: 'Ce que Mailsorter vous demandera',
    text: 'Lire et ranger votre boîte (archiver, étiqueter, corbeille), gérer vos libellés, et vous envoyer le récap quotidien si vous l\'activez. Rien d\'autre.',
  },
  {
    Icon: Sparkles,
    title: 'Ce qui part à l\'IA',
    text: 'L\'expéditeur, le sujet et un extrait de 200 caractères, uniquement quand vous lancez une analyse. Jamais le contenu complet, jamais les pièces jointes.',
  },
  {
    Icon: Undo,
    title: 'Rien d\'irréversible',
    text: 'Supprimer, c\'est envoyer à la corbeille, récupérable 30 jours. Chaque action est journalisée avec son origine et s\'annule d\'un clic.',
  },
  {
    Icon: Shield,
    title: 'Vos données, reprises quand vous voulez',
    text: 'Un export JSON complet et une suppression définitive, tous les deux en libre-service. Supprimer votre compte ne touche pas vos emails.',
  },
];

const FAQ = [
  {
    q: 'Est-ce que vous lisez mes emails ?',
    a: "Le serveur les lit pour les trier, et en garde une copie dans la base de l'instance pour afficher votre boîte sans rappeler votre fournisseur à chaque clic. Personne ne les consulte. Le détail de ce qui est conservé est dans la politique de confidentialité.",
  },
  {
    q: 'Mes emails partent-ils chez un fournisseur d\'IA ?',
    a: "Uniquement quand vous lancez une analyse, et seulement l'expéditeur, le sujet et un extrait de 200 caractères, chez Mistral. Les règles de tri, elles, sont purement déterministes : elles ne font appel à aucune IA et ne consomment aucun quota.",
  },
  {
    q: 'Que se passe-t-il si Mailsorter se trompe ?',
    a: "Rien d'irréversible. Une suppression part à la corbeille, récupérable 30 jours. Chaque action est inscrite dans un journal qui dit qui l'a décidée, et les actions réversibles s'annulent d'un clic. Vous pouvez aussi protéger des expéditeurs, qui ne seront jamais archivés ni supprimés automatiquement.",
  },
  {
    q: 'Comment fonctionne la connexion de ma boîte mail ?',
    a: "Dans un premier temps, vous créez votre compte Mailsorter avec un email et un mot de passe. Ensuite, vous choisissez la boîte mail à ranger : soit Gmail via la connexion Google sécurisée, soit toute autre messagerie avec un mot de passe d'application délivré par votre fournisseur.",
  },
  {
    q: 'Combien ça coûte ?',
    a: "200 emails analysés par mois, gratuitement et sans carte bancaire. Au-delà, le plan Pro lève la limite. Le cache et l'auto-pilote ne consomment pas votre quota, et les règles sont gratuites et illimitées.",
  },
  {
    q: 'Puis-je récupérer ou effacer mes données ?',
    a: "À tout moment, depuis l'application : un export JSON de tout ce qui est stocké à votre sujet, et une suppression définitive. Les deux sont pilotés par la même liste dans le code, donc rien ne peut être gardé sans être exportable.",
  },
];

function AuthBox({ isConfigured, onSignedIn }) {
  const [mode, setMode] = useState('register'); // 'register' | 'login'
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [googleBusy, setGoogleBusy] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (busy || googleBusy) return;
    setBusy(true);
    setError('');

    try {
      if (mode === 'register') {
        const { data } = await authService.register(email.trim(), password);
        localStorage.setItem('userEmail', data.userEmail);
        localStorage.setItem('accessToken', data.accessToken);
        localStorage.removeItem('hasMailbox');
        track('register_done');
        onSignedIn('/connect');
      } else {
        const { data } = await authService.login(email.trim(), password);
        localStorage.setItem('userEmail', data.userEmail);
        localStorage.setItem('accessToken', data.accessToken);
        localStorage.removeItem('hasMailbox');
        track('login_done');
        onSignedIn('/inbox');
      }
    } catch (err) {
      setError(apiError(err, 'Une erreur est survenue. Vérifiez vos identifiants.'));
      setBusy(false);
    }
  };

  const handleGoogleLogin = async () => {
    setGoogleBusy(true);
    setError('');
    try {
      const response = await authService.getAuthUrl();
      track('login_start', { provider: 'google' });
      window.location.href = response.data.authUrl;
    } catch (err) {
      setError('Impossible de démarrer la connexion Google. Réessayez.');
      setGoogleBusy(false);
    }
  };

  return (
    <div id="auth-box" className="mt-8 w-full max-w-md rounded-2xl border border-hairline bg-surface p-6 shadow-card animate-fade-up">
      {/* Switcher Inscription / Connexion */}
      <div className="flex rounded-xl bg-ink-100/70 p-1">
        <button
          type="button"
          onClick={() => { setMode('register'); setError(''); }}
          className={`flex-1 rounded-lg py-2 text-sm font-semibold transition-all ${
            mode === 'register' ? 'bg-surface text-ink-900 shadow-sm' : 'text-ink-600 hover:text-ink-900'
          }`}
        >
          Créer un compte
        </button>
        <button
          type="button"
          onClick={() => { setMode('login'); setError(''); }}
          className={`flex-1 rounded-lg py-2 text-sm font-semibold transition-all ${
            mode === 'login' ? 'bg-surface text-ink-900 shadow-sm' : 'text-ink-600 hover:text-ink-900'
          }`}
        >
          Se connecter
        </button>
      </div>

      <div className="mt-4 text-left">
        <h2 className="text-base font-bold text-ink-900">
          {mode === 'register' ? 'Créer mon compte Mailsorter' : 'Connexion à Mailsorter'}
        </h2>
        <p className="mt-1 text-xs leading-relaxed text-ink-500">
          {mode === 'register'
            ? 'Étape 1 sur 2 · Vous définirez la boîte mail à ranger juste après.'
            : 'Accédez à votre espace de tri.'}
        </p>
      </div>

      <form onSubmit={handleSubmit} className="mt-4 space-y-3 text-left">
        <div>
          <label htmlFor="auth-email" className="block text-xs font-bold text-ink-700">
            Adresse email
          </label>
          <input
            id="auth-email"
            type="email"
            required
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="vous@exemple.com"
            className="input mt-1 w-full text-sm"
          />
        </div>

        <div>
          <label htmlFor="auth-password" className="block text-xs font-bold text-ink-700">
            Mot de passe
          </label>
          <input
            id="auth-password"
            type="password"
            required
            minLength={mode === 'register' ? 8 : undefined}
            autoComplete={mode === 'register' ? 'new-password' : 'current-password'}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={mode === 'register' ? '8 caractères minimum' : 'Votre mot de passe'}
            className="input mt-1 w-full text-sm"
          />
        </div>

        <button type="submit" disabled={busy || googleBusy} className="btn-primary mt-2 w-full py-3 text-sm">
          {busy ? (
            <>
              <Spinner size={18} className="text-white" /> Chargement...
            </>
          ) : mode === 'register' ? (
            <>
              <Sparkles size={16} /> Créer mon compte gratuit
            </>
          ) : (
            'Se connecter'
          )}
        </button>

        {isConfigured && (
          <>
            <div className="my-3 flex items-center gap-3">
              <span className="h-px flex-1 bg-hairline" />
              <span className="text-[11px] font-semibold uppercase tracking-wider text-ink-400">ou</span>
              <span className="h-px flex-1 bg-hairline" />
            </div>

            <button
              type="button"
              onClick={handleGoogleLogin}
              disabled={busy || googleBusy}
              className="btn-secondary w-full py-2.5 text-sm"
            >
              {googleBusy ? (
                <>
                  <Spinner size={16} /> Redirection Google...
                </>
              ) : (
                <>
                  <Google size={18} /> Continuer avec Google
                </>
              )}
            </button>
          </>
        )}

        {mode === 'register' && (
          <p className="pt-2 text-center text-xs text-ink-500">
            <Check size={14} className="inline text-positive-600 mr-1" /> Gratuit · Sans carte bancaire
          </p>
        )}

        {error && (
          <div className="mt-3 rounded-xl border border-danger-100 bg-danger-50 px-3.5 py-2 text-xs text-danger-700">
            {error}
          </div>
        )}
      </form>
    </div>
  );
}

const DEMO_SUGGESTIONS = [
  { Icon: Archive, from: 'Medium Digest', act: 'Archiver', conf: 96 },
  { Icon: Tag, from: 'Amazon', act: 'Libellé · Achats', conf: 92 },
  { Icon: Sparkles, from: 'Promo Casino', act: 'Supprimer', conf: 88 },
];

// La démo de la landing était inerte : cliquer sur "Tout appliquer" ne réagissait
// pas, et le visiteur retenait ça plutôt que la promesse. Elle rejoue maintenant
// un cycle d'application complet, en pur client (aucun appel, aucune boîte), puis
// se réinitialise pour qu'on la rejoue.
function SuggestionsDemo() {
  const [applied, setApplied] = useState(0);
  const [running, setRunning] = useState(false);
  const timers = useRef([]);

  useEffect(() => () => timers.current.forEach(clearTimeout), []);

  const play = () => {
    if (running || applied === DEMO_SUGGESTIONS.length) return;
    setRunning(true);
    track('suggestions_demo_play', { count: DEMO_SUGGESTIONS.length });
    DEMO_SUGGESTIONS.forEach((_, i) => {
      timers.current.push(setTimeout(() => setApplied(i + 1), 500 * (i + 1)));
    });
    timers.current.push(
      setTimeout(() => {
        setRunning(false);
        timers.current.push(setTimeout(() => setApplied(0), 2600));
      }, 500 * DEMO_SUGGESTIONS.length + 150)
    );
  };

  const done = applied === DEMO_SUGGESTIONS.length;
  const remaining = DEMO_SUGGESTIONS.length - applied;
  const badge =
    running || applied > 0
      ? done
        ? 'Tout appliqué'
        : `${remaining} restante${remaining > 1 ? 's' : ''}`
      : `${DEMO_SUGGESTIONS.length} prêtes`;

  return (
    <div className="card p-3 shadow-card">
      <div className="rounded-2xl bg-ink-50 p-5">
        <div className="mb-4 flex items-center justify-between">
          <span className="text-sm font-semibold text-ink-700">Suggestions IA</span>
          <span className={done ? 'chip bg-positive-50 text-positive-700' : 'chip bg-brand-50 text-brand-700'}>
            {badge}
          </span>
        </div>
        <div className="space-y-2.5">
          {DEMO_SUGGESTIONS.map((r, i) => {
            const isDone = i < applied;
            return (
              <div key={r.from} className="flex items-center gap-3 rounded-xl border border-hairline bg-surface p-3">
                <span
                  className={
                    isDone
                      ? 'flex h-9 w-9 items-center justify-center rounded-lg bg-positive-50 text-positive-600'
                      : 'flex h-9 w-9 items-center justify-center rounded-lg bg-brand-50 text-brand-600'
                  }
                >
                  {isDone ? <Check size={18} /> : <r.Icon size={18} />}
                </span>
                <div className="min-w-0 flex-1">
                  <div className={isDone ? 'truncate text-sm font-semibold text-ink-400 line-through' : 'truncate text-sm font-semibold text-ink-900'}>
                    {r.from}
                  </div>
                  <div className={isDone ? 'text-xs text-positive-600' : 'text-xs text-ink-500'}>
                    {isDone ? 'Action appliquée' : r.act}
                  </div>
                </div>
                <span className="chip bg-positive-50 text-positive-700">{isDone ? 'Fait' : `${r.conf}%`}</span>
              </div>
            );
          })}
        </div>
        <button onClick={play} disabled={running || done} className="btn-primary mt-4 w-full py-3">
          {running ? (
            <>
              <Spinner size={16} className="text-white" /> Application...
            </>
          ) : done ? (
            <>
              <Check size={16} /> Tout appliqué
            </>
          ) : (
            <>
              <Bolt size={16} /> Tout appliquer
            </>
          )}
        </button>
      </div>
    </div>
  );
}

function Login() {
  const navigate = useNavigate();
  const { loading, isConfigured, selfHosted, billingOn } = useInstance();
  const [providers, setProviders] = useState([]);

  const features = useMemo(
    () => FEATURES.filter((f) => !f.needs || (f.needs === GMAIL_ONLY && isConfigured)),
    [isConfigured]
  );

  const [waitlistEmail, setWaitlistEmail] = useState(getWaitlistEmail);
  const [joining, setJoining] = useState(false);
  const [joined, setJoined] = useState(hasJoinedWaitlist);
  const [waitlistError, setWaitlistError] = useState('');

  useEffect(() => {
    if (isAuthed()) {
      if (localStorage.getItem('hasMailbox')) {
        navigate('/inbox');
      } else {
        navigate('/connect');
      }
    }
  }, [navigate]);

  useEffect(() => {
    let cancelled = false;
    configService
      .getProviders()
      .then(({ data }) => {
        if (!cancelled) setProviders(data.providers || []);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  const handleWaitlist = async (e) => {
    e.preventDefault();
    const email = waitlistEmail.trim();
    if (!email || joining) return;
    setJoining(true);
    setWaitlistError('');
    try {
      await waitlistService.join(email, 'landing');
      rememberWaitlistJoin(email);
      setJoined(true);
      track('waitlist_join', { source: 'landing' });
    } catch (err) {
      setWaitlistError(apiError(err, 'Inscription impossible pour le moment. Réessayez.'));
    } finally {
      setJoining(false);
    }
  };

  const handleResetWaitlist = () => {
    forgetWaitlistJoin();
    setJoined(false);
    setWaitlistError('');
  };

  const currentWaitlistEmail = waitlistEmail || getWaitlistEmail();

  const scrollToAuth = () => {
    const el = document.getElementById('auth-box');
    if (el) {
      el.scrollIntoView({ behavior: 'smooth' });
      const input = el.querySelector('input');
      if (input) input.focus();
    }
  };

  return (
    <div className="min-h-screen bg-ink-50 text-ink-900">
      <div className="mx-auto max-w-6xl px-6 pb-16 pt-8">
        {/* Nav */}
        <nav className="flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <Logo size={32} />
            <span className="font-display text-lg font-bold tracking-tight">Mailsorter</span>
          </div>
          <div className="flex items-center gap-3">
            {!selfHosted && (
              <button
                onClick={() => navigate('/pricing')}
                className="text-sm font-semibold text-ink-500 transition-colors hover:text-ink-900"
              >
                Tarifs
              </button>
            )}
            <button
              onClick={scrollToAuth}
              className="text-sm font-semibold text-brand-600 transition-colors hover:text-brand-700"
            >
              Connexion / Inscription
            </button>
          </div>
        </nav>

        {/* Hero */}
        <section className="grid items-center gap-12 pt-16 lg:grid-cols-2 lg:pt-24">
          <div className="animate-fade-up">
            <span className="chip mb-5 border border-brand-100 bg-brand-50 text-brand-700">
              <Sparkles size={14} /> Propulsé par l'IA Mistral
            </span>
            <h1 className="font-display text-4xl font-bold leading-[1.05] tracking-tight text-ink-900 sm:text-6xl">
              Votre boîte mail,
              <br />
              <span className="text-brand-600">triée pendant que vous dormez.</span>
            </h1>
            <p className="mt-6 max-w-xl text-lg leading-relaxed text-ink-600">
              Mailsorter lit, comprend et range vos emails à votre place.
              Stop au scroll infini : atteignez l'Inbox Zero en quelques clics, et gardez-la propre pour
              toujours.
            </p>

            {/* Formulaire d'inscription / connexion au compte MailSorter */}
            <AuthBox isConfigured={isConfigured} onSignedIn={(to) => navigate(to)} />

            <p className="mt-4 text-xs text-ink-500">
              Pas encore prêt à commencer ?{' '}
              <a href="#garder-contact" className="font-semibold text-brand-600 underline-offset-2 hover:underline">
                Laissez-nous votre adresse
              </a>
              .
            </p>
          </div>

          {/* Démo de l'écran de suggestions. Annoncée comme telle : ce n'est pas
              une capture du produit, et la faire passer pour une capture est
              exactement ce qu'on reproche aux pages de vente. Elle est jouable en
              revanche, en pur client : le bouton rejoue l'application, sans
              toucher à une boîte. */}
          <div className="animate-fade-up [animation-delay:120ms]">
            <SuggestionsDemo />
            <p className="mt-2 text-center text-xs text-ink-400">Démo de l'écran de suggestions</p>
          </div>
        </section>

        {/* Boîtes joignables */}
        {providers.length > 0 && (
          <section className="mt-14 rounded-2xl border border-hairline bg-surface/60 px-6 py-5">
            <p className="text-xs font-semibold uppercase tracking-wider text-ink-500">
              Messageries prises en charge
            </p>
            <ul className="mt-3 flex flex-wrap gap-1.5">
              {providers.map((p) => (
                <li key={p.key} className="chip bg-ink-100 text-ink-700">
                  {p.name}
                </li>
              ))}
            </ul>
            <p className="mt-3 text-xs text-ink-500">
              Vous créez votre compte Mailsorter, puis vous branchez la boîte de votre choix
              (Gmail, Orange, Outlook, Yahoo...) en toute sécurité.
            </p>
          </section>
        )}

        {/* Faits vérifiables */}
        <section className="mt-14 grid grid-cols-2 gap-4 sm:grid-cols-4">
          {FACTS.map(({ big, small, title }) => (
            <div key={small} className="card p-5 text-center" title={title}>
              <div className="font-display text-3xl font-bold text-ink-900">{big}</div>
              <div className="mt-1 text-xs text-ink-500">{small}</div>
            </div>
          ))}
        </section>

        {/* Features */}
        <section className="mt-24">
          <h2 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">
            Tout ce qu'une boîte mail aurait dû faire seule.
          </h2>
          <div className="mt-10 grid gap-4 sm:grid-cols-2">
            {features.map(({ Icon, title, text, soon }) => (
              <div key={title} className="card group p-6 transition-shadow hover:shadow-card">
                <span className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
                  <Icon size={22} />
                </span>
                <h3 className="flex flex-wrap items-center gap-2 text-lg font-bold text-ink-900">
                  {title}
                  {soon && <span className="chip bg-caution-50 text-caution-700">Bientôt</span>}
                </h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-600">{text}</p>
              </div>
            ))}
          </div>
        </section>

        {/* How it works */}
        <section className="mt-24">
          <h2 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">
            Trois étapes. Zéro effort.
          </h2>
          <div className="mt-10 grid gap-4 md:grid-cols-3">
            {STEPS.map(({ n, title, text }) => (
              <div key={n} className="card p-6">
                <div className="font-display text-4xl font-bold text-ink-200">{n}</div>
                <h3 className="mt-3 text-lg font-bold text-ink-900">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-600">{text}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Confiance */}
        <section className="mt-24">
          <span className="chip bg-positive-50 text-positive-700">
            <Lock size={14} /> Avant de cliquer
          </span>
          <h2 className="mt-4 font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">
            Ce que Mailsorter fait de vos emails.
          </h2>
          <p className="mt-3 max-w-2xl text-ink-600">
            Donner à une application l'accès à sa boîte mail n'est pas anodin. Voici ce qui est demandé,
            ce qui est conservé, et ce qui ne sort jamais.
          </p>
          <div className="mt-10 grid gap-4 sm:grid-cols-2">
            {TRUST.map(({ Icon, title, text }) => (
              <div key={title} className="card p-6">
                <span className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-xl bg-positive-50 text-positive-600">
                  <Icon size={22} />
                </span>
                <h3 className="text-lg font-bold text-ink-900">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-600">{text}</p>
              </div>
            ))}
          </div>
          <Link
            to="/confidentialite"
            className="mt-6 inline-flex items-center gap-2 text-sm font-semibold text-brand-600 underline-offset-2 hover:underline"
          >
            Lire la politique de confidentialité complète
          </Link>
        </section>

        {/* Auto-hébergement */}
        <section className="mt-16 flex flex-col gap-5 rounded-2xl border border-hairline bg-surface p-8 sm:flex-row sm:items-center">
          <span className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-ink-100 text-ink-700">
            <Server size={24} />
          </span>
          <div className="flex-1">
            <h3 className="text-lg font-bold text-ink-900">Ou hébergez-le vous-même.</h3>
            <p className="mt-1.5 text-sm leading-relaxed text-ink-600">
              Le code est public. Vous pouvez faire tourner Mailsorter sur votre serveur, avec votre
              propre projet Google : vos emails et vos jetons ne quittent alors jamais votre
              infrastructure, et il n'y a personne à qui faire confiance.
            </p>
          </div>
          <a
            href={SOURCE_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="btn-secondary shrink-0"
          >
            Voir le code
          </a>
        </section>

        {/* FAQ */}
        <section className="mt-24">
          <h2 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">
            Les questions qu'on nous pose.
          </h2>
          <div className="mt-8 space-y-2.5">
            {FAQ.map(({ q, a }) => (
              <details key={q} className="card group p-5 [&_summary::-webkit-details-marker]:hidden">
                <summary className="flex cursor-pointer items-center justify-between gap-4 text-base font-bold text-ink-900">
                  {q}
                  <ChevronDown
                    size={18}
                    className="shrink-0 text-ink-400 transition-transform group-open:rotate-180"
                  />
                </summary>
                <p className="mt-3 text-sm leading-relaxed text-ink-600">{a}</p>
              </details>
            ))}
          </div>
        </section>

        {/* Final CTA */}
        <section className="mt-24 overflow-hidden rounded-3xl bg-brand-fill p-10 text-center text-white sm:p-16">
          <h2 className="font-display text-3xl font-bold tracking-tight sm:text-4xl">
            Reprenez le contrôle de votre inbox.
          </h2>
          <p className="mx-auto mt-3 max-w-md text-white/85">
            Créez votre compte en 10 secondes et regardez le désordre disparaître.
            C'est gratuit, et sans carte bancaire.
          </p>
          <button
            onClick={scrollToAuth}
            className="mt-8 inline-flex items-center justify-center gap-3 rounded-xl bg-surface px-7 py-3.5 text-base font-bold text-brand-700 shadow-soft transition-colors hover:bg-brand-50"
          >
            <Sparkles size={20} />
            Commencer maintenant
          </button>
        </section>

        {/* Garder le contact sans donner sa boîte */}
        <section id="garder-contact" className="card mt-6 p-8 text-center">
          <span className="mx-auto mb-4 flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
            <Mail size={22} />
          </span>
          <h3 className="text-lg font-bold text-ink-900">Pas encore prêt ?</h3>
          <p className="mx-auto mt-1.5 max-w-md text-sm leading-relaxed text-ink-600">
            {!loading && !selfHosted && billingOn
              ? 'Le plan Pro est disponible dès maintenant.'
              : "Laissez votre adresse : vous serez prévenu des nouveautés et de l'ouverture du plan Pro. Pas d'accès à votre boîte, pas de compte à créer."}
          </p>

          {!loading && !selfHosted && billingOn ? (
            <div className="mt-5 flex justify-center">
              <button
                type="button"
                onClick={() => navigate('/pricing')}
                className="btn-secondary shrink-0"
              >
                Découvrir les tarifs Pro
              </button>
            </div>
          ) : joined ? (
            <div className="mt-5 flex flex-col items-center justify-center gap-2 sm:flex-row">
              <span className="inline-flex items-center gap-2 rounded-xl bg-positive-50 px-4 py-2.5 text-sm font-semibold text-positive-700">
                <Check size={16} />
                {currentWaitlistEmail ? `C'est noté, ${currentWaitlistEmail}. On vous écrit.` : "C'est noté. On vous écrit."}
              </span>
              <button
                type="button"
                onClick={handleResetWaitlist}
                className="text-xs font-semibold text-ink-500 underline-offset-2 hover:text-ink-800 hover:underline"
              >
                Changer d'adresse
              </button>
            </div>
          ) : (
            <form onSubmit={handleWaitlist} className="mx-auto mt-5 flex max-w-md flex-col gap-2 sm:flex-row">
              <label htmlFor="landing-email" className="sr-only">
                Votre adresse email
              </label>
              <input
                id="landing-email"
                type="email"
                required
                value={waitlistEmail}
                onChange={(e) => setWaitlistEmail(e.target.value)}
                placeholder="vous@exemple.com"
                className="input flex-1"
              />
              <button type="submit" disabled={joining} className="btn-primary shrink-0">
                {joining ? <Spinner size={18} /> : <Sparkles size={16} />} Me tenir au courant
              </button>
            </form>
          )}

          {!loading && !selfHosted && billingOn ? null : waitlistError && (
            <p role="alert" className="mt-3 text-sm text-danger-700">
              {waitlistError}
            </p>
          )}
        </section>

        <PublicFooter />
      </div>
    </div>
  );
}

export default Login;
