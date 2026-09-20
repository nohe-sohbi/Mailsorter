import React, { useEffect, useMemo, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { apiError, authService, configService } from '../services/api';
import { useInstance } from '../contexts/InstanceContext';
import { track } from '../lib/analytics';
import { Logo, Google, Mail, Sparkles, Archive, Tag, Users, Shield, Bolt, Check, BellOff } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { detectProvider, ProviderBriefing } from '../components/MailboxBriefing';

// The landing page makes promises, so it has to know which ones this
// deployment can keep. Labels exist on the Gmail API and nowhere else: plain
// IMAP has no such thing (see internal/mailbox), so an instance whose only door
// is a mailbox must not advertise them.
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
    text: 'Mailsorter traque les newsletters qui vous noient et vous désabonne instantanément, sans formulaire ni sortie de l’app.',
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
    needs: GMAIL_ONLY,
  },
];

const stepsFor = (googleDoor) => [
  googleDoor
    ? { n: '01', title: 'Connectez Gmail', text: 'Authentification Google sécurisée. Aucun mot de passe stocké.' }
    : {
        n: '01',
        title: 'Branchez votre boîte',
        text: "Une adresse et un mot de passe d'application, révocable chez votre fournisseur.",
      },
  { n: '02', title: 'Lancez l’analyse', text: 'L’IA passe votre boîte au crible et propose une action par email.' },
  { n: '03', title: 'Validez d’un geste', text: 'Acceptez, ajustez, ou laissez l’auto-pilote faire le ménage.' },
];

// Signing in with a mailbox, which is also how an account is created.
//
// There is no separate sign-up form because there is nothing to sign up for:
// Mailsorter never invents a password, so it has none to set, confirm or reset.
// The mail server is the authority, and an app password it accepts is a
// stronger proof of identity than any confirmation link. The copy has to say
// that out loud, because "mot de passe" next to an email address reads as an
// account password and that is the one thing it must not be.
function MailboxSignIn({ providers, onSignedIn }) {
  const [address, setAddress] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const detected = useMemo(() => detectProvider(providers, address), [providers, address]);

  const submit = async (e) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      const { data } = await authService.signInWithMailbox(address.trim(), password);
      localStorage.setItem('userEmail', data.userEmail);
      localStorage.setItem('accessToken', data.accessToken);
      // No address, no provider: a count of successful sign-ins and nothing else.
      track('login_done', { method: 'mailbox' });
      onSignedIn();
    } catch (err) {
      setError(apiError(err, 'Connexion impossible. Réessayez.'));
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="mt-6 w-full max-w-xl space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block">
          <span className="sr-only">Adresse email</span>
          <input
            type="email"
            required
            autoComplete="username"
            value={address}
            onChange={(e) => setAddress(e.target.value)}
            placeholder="vous@orange.fr"
            className="input w-full"
          />
        </label>
        <label className="block">
          <span className="sr-only">Mot de passe d'application</span>
          <input
            type="password"
            required
            /* new-password, not current-password: what goes here is issued by the
               provider and pasted once, never the account password a manager
               would helpfully fill in. */
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Mot de passe d'application"
            className="input w-full"
          />
        </label>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <button type="submit" disabled={busy} className="btn-primary px-6 py-3 text-base">
          {busy ? (
            <>
              <Spinner size={18} className="text-white" /> Connexion en cours
            </>
          ) : (
            <>
              <Mail size={18} /> Brancher ma boîte
            </>
          )}
        </button>
        <span className="text-sm text-ink-500">
          Première fois ou retour : c'est le même formulaire.
        </span>
      </div>

      {detected && <ProviderBriefing provider={detected} />}

      {error && (
        <div className="inline-flex items-center gap-2 rounded-xl border border-danger-100 bg-danger-50 px-4 py-2.5 text-sm text-danger-700">
          {error}
        </div>
      )}
    </form>
  );
}

function Login() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [providers, setProviders] = useState([]);
  const { isConfigured } = useInstance();

  // Which doors this instance actually has. Google needs the instance to hold
  // OAuth credentials; the mailbox form needs at least one provider reachable
  // over IMAP in this edition. Both come from the server, so a hosted instance
  // (which can never reach the Gmail API) shows one door and a self-hosted one
  // with credentials shows two, with nothing in the SPA deciding that.
  const mailboxDoor = useMemo(
    () => providers.some((p) => (p.routes || []).some((r) => r.transport === 'imap')),
    [providers]
  );

  // A promise the deployment cannot keep is worse than one it does not make.
  const features = useMemo(
    () => FEATURES.filter((f) => !f.needs || (f.needs === GMAIL_ONLY && isConfigured)),
    [isConfigured]
  );

  useEffect(() => {
    configService
      .getProviders()
      .then((res) => setProviders(res.data.providers || []))
      // A catalog that will not load hides the mailbox form rather than
      // offering one that cannot work. The Google button is unaffected.
      .catch(() => setProviders([]));
  }, []);

  useEffect(() => {
    if (localStorage.getItem('userEmail')) {
      navigate('/inbox');
      return;
    }
    const code = searchParams.get('code');
    const state = searchParams.get('state');
    if (code) handleCallback(code, state);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams, navigate]);

  const handleCallback = async (code, state) => {
    setLoading(true);
    setError('');
    try {
      const response = await authService.handleCallback(code, state);
      localStorage.setItem('userEmail', response.data.userEmail);
      localStorage.setItem('accessToken', response.data.accessToken);
      track('login_done');
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
      track('login_start');
      window.location.href = response.data.authUrl;
    } catch (err) {
      setError('Impossible de démarrer la connexion. Réessayez.');
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-ink-50 text-ink-900">
      <div className="mx-auto max-w-6xl px-6 pb-24 pt-8">
        {/* Nav */}
        <nav className="flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <Logo size={32} />
            <span className="font-display text-lg font-bold tracking-tight">Mailsorter</span>
          </div>
          <div className="flex items-center gap-3">
            <button
              onClick={() => navigate('/pricing')}
              className="text-sm font-semibold text-ink-500 transition-colors hover:text-ink-900"
            >
              Tarifs
            </button>
            <span className="hidden chip border border-hairline bg-surface text-ink-600 sm:inline-flex">
              <Shield size={14} className="text-brand-600" />{' '}
              {isConfigured ? 'OAuth Google sécurisé' : "Mot de passe d'application, révocable"}
            </span>
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
              Mailsorter lit, comprend et range vos emails à votre place{isConfigured ? ', Gmail compris' : ''}.
              Stop au scroll infini : atteignez l'Inbox Zero en quelques clics, et gardez-la propre pour
              toujours.
            </p>

            {isConfigured && (
              <div className="mt-8 flex flex-col items-start gap-4 sm:flex-row sm:items-center">
                <button onClick={handleLogin} disabled={loading} className="btn-primary px-6 py-3.5 text-base">
                  {loading ? (
                    <>
                      <Spinner size={20} className="text-white" /> Connexion…
                    </>
                  ) : (
                    <>
                      <Google size={20} /> Continuer avec Gmail
                    </>
                  )}
                </button>
                <div className="flex items-center gap-2 text-sm text-ink-500">
                  <Check size={16} className="text-positive-600" /> Gratuit · Sans carte bancaire
                </div>
              </div>
            )}

            {mailboxDoor && (
              <>
                {isConfigured && (
                  <div className="mt-8 flex items-center gap-4">
                    <span className="h-px flex-1 bg-hairline" />
                    <span className="text-xs font-semibold uppercase tracking-wider text-ink-500">ou</span>
                    <span className="h-px flex-1 bg-hairline" />
                  </div>
                )}
                <p className={isConfigured ? 'mt-6 text-sm text-ink-600' : 'mt-8 text-sm text-ink-600'}>
                  Branchez n'importe quelle boîte avec un <strong>mot de passe d'application</strong>,
                  délivré par votre fournisseur et révocable quand vous voulez. Ce n'est jamais le mot
                  de passe de votre compte.
                </p>
                <MailboxSignIn providers={providers} onSignedIn={() => navigate('/inbox')} />
              </>
            )}

            {error && (
              <div className="mt-5 inline-flex items-center gap-2 rounded-xl border border-danger-100 bg-danger-50 px-4 py-2.5 text-sm text-danger-700">
                {error}
              </div>
            )}
          </div>

          {/* Visual mock */}
          <div className="animate-fade-up [animation-delay:120ms]">
            <div className="card p-3 shadow-card">
              <div className="rounded-2xl bg-ink-50 p-5">
                <div className="mb-4 flex items-center justify-between">
                  <span className="text-sm font-semibold text-ink-700">Suggestions IA</span>
                  <span className="chip bg-brand-50 text-brand-700">3 prêtes</span>
                </div>
                <div className="space-y-2.5">
                  {[
                    { Icon: Archive, from: 'Medium Digest', act: 'Archiver', conf: 96 },
                    { Icon: Tag, from: 'Amazon', act: 'Libellé · Achats', conf: 92 },
                    { Icon: Sparkles, from: 'Promo Casino', act: 'Supprimer', conf: 88 },
                  ].map((r, i) => (
                    <div key={i} className="flex items-center gap-3 rounded-xl border border-hairline bg-surface p-3">
                      <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-50 text-brand-600">
                        <r.Icon size={18} />
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-semibold text-ink-900">{r.from}</div>
                        <div className="text-xs text-ink-500">{r.act}</div>
                      </div>
                      <span className="chip bg-positive-50 text-positive-700">{r.conf}%</span>
                    </div>
                  ))}
                </div>
                <button className="btn-primary mt-4 w-full py-3">
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
            <div key={small} className="card p-5 text-center">
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
            {features.map(({ Icon, title, text }) => (
              <div
                key={title}
                className="card group p-6 transition-shadow hover:shadow-card"
              >
                <span className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
                  <Icon size={22} />
                </span>
                <h3 className="text-lg font-bold text-ink-900">{title}</h3>
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
            {stepsFor(isConfigured).map(({ n, title, text }) => (
              <div key={n} className="card p-6">
                <div className="font-display text-4xl font-bold text-ink-200">{n}</div>
                <h3 className="mt-3 text-lg font-bold text-ink-900">{title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink-600">{text}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Final CTA */}
        <section className="mt-24 overflow-hidden rounded-3xl bg-brand-fill p-10 text-center text-white sm:p-16">
          <h2 className="font-display text-3xl font-bold tracking-tight sm:text-4xl">
            Reprenez le contrôle de votre inbox.
          </h2>
          <p className="mx-auto mt-3 max-w-md text-white/85">
            {isConfigured ? 'Connectez Gmail' : 'Branchez votre boîte'} et regardez le désordre disparaître.
            C'est gratuit, et ça prend 30 secondes.
          </p>
          <button
            onClick={handleLogin}
            disabled={loading}
            className="mt-8 inline-flex items-center justify-center gap-3 rounded-xl bg-surface px-7 py-3.5 text-base font-bold text-brand-700 shadow-soft transition-colors hover:bg-brand-50 disabled:opacity-60"
          >
            {loading ? <Spinner size={20} className="text-brand-600" /> : <Google size={20} />}
            Commencer maintenant
          </button>
        </section>

        <footer className="mt-16 flex flex-col items-center justify-between gap-4 border-t border-hairline pt-8 text-sm text-muted sm:flex-row">
          <span>© {new Date().getFullYear()} Mailsorter</span>
          <span className="flex items-center gap-2">
            <Shield size={14} className="text-muted" /> Vos emails ne quittent jamais votre contrôle.
          </span>
        </footer>
      </div>
    </div>
  );
}

export default Login;
