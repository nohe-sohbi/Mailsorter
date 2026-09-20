import React, { useEffect, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { authService, configService, waitlistService, apiError } from '../services/api';
import { useInstance } from '../contexts/InstanceContext';
import { track } from '../lib/analytics';
import { hasJoinedWaitlist, rememberWaitlistJoin } from '../lib/waitlist';
import PublicFooter, { SOURCE_URL } from '../components/PublicFooter';
import {
  Logo, Google, Sparkles, Archive, Tag, Users, Shield, Bolt, Check, BellOff,
  Lock, Server, Undo, Mail, ChevronDown,
} from '../ui/icons';
import Spinner from '../ui/Spinner';

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
    // Les routes /api/smart-labels existent et sont testées, mais aucun écran ne
    // les appelle encore. La carte reste, marquée : annoncer la fonctionnalité
    // sans dire qu'elle n'est pas livrée était la seule promesse fausse de cette
    // page.
    Icon: Tag,
    title: 'Libellés intelligents',
    text: 'Des étiquettes précises et cohérentes, créées et appliquées automatiquement dans votre Gmail.',
    soon: true,
  },
];

const STEPS = [
  { n: '01', title: 'Connectez Gmail', text: 'Authentification Google sécurisée. Aucun mot de passe stocké.' },
  { n: '02', title: 'Lancez l\'analyse', text: 'L\'IA passe votre boîte au crible et propose une action par email.' },
  { n: '03', title: 'Validez d\'un geste', text: 'Acceptez, ajustez, ou laissez l\'auto-pilote faire le ménage.' },
];

// Quatre chiffres, quatre faits vérifiables dans ce dépôt. Les précédents
// ("10x plus rapide", "moins de 2 min pour vider 500 emails") n'avaient aucune
// mesure derrière et fragilisaient les affirmations voisines, qui sont vraies.
const FACTS = [
  { big: '0', small: 'mot de passe stocké', title: 'La connexion passe par OAuth Google, aucun mot de passe ne transite.' },
  { big: 'AES-256', small: 'jetons chiffrés au repos', title: 'Votre jeton Google est scellé en AES-256-GCM avant d\'atteindre la base.' },
  { big: '200', small: 'emails analysés / mois offerts', title: 'Quota du plan gratuit. Le cache et l\'auto-pilote ne le consomment pas.' },
  { big: '1 clic', small: 'pour annuler une action', title: 'Chaque action est journalisée avec son inverse, et annulable depuis l\'historique.' },
];

// Ce que Mailsorter fait de vos emails, dit avant le clic plutôt qu'après.
// C'est l'objection numéro un d'un outil qui demande un accès Gmail complet, et
// la page n'y répondait que par une puce "OAuth Google sécurisé".
const TRUST = [
  {
    Icon: Lock,
    title: 'Ce que Google vous demandera',
    text: 'Lire et modifier votre boîte (archiver, étiqueter, corbeille), gérer vos libellés, et vous envoyer à vous-même le récap quotidien si vous l\'activez. Rien d\'autre.',
  },
  {
    Icon: Sparkles,
    title: 'Ce qui part à l\'IA',
    text: 'L\'expéditeur, le sujet et un extrait de 200 caractères, uniquement quand vous lancez une analyse. Jamais le contenu complet, jamais les pièces jointes.',
  },
  {
    Icon: Undo,
    title: 'Rien d\'irréversible',
    text: 'Supprimer, c\'est envoyer à la corbeille Gmail, récupérable 30 jours. Chaque action est journalisée avec son origine et s\'annule d\'un clic.',
  },
  {
    Icon: Shield,
    title: 'Vos données, reprises quand vous voulez',
    text: 'Un export JSON complet et une suppression définitive, tous les deux en libre-service. Supprimer votre compte ne touche pas votre boîte Gmail.',
  },
];

const FAQ = [
  {
    q: 'Est-ce que vous lisez mes emails ?',
    a: "Le serveur les lit pour les trier, et en garde une copie dans la base de l'instance pour afficher votre boîte sans rappeler Google à chaque clic. Personne ne les consulte. Le détail de ce qui est conservé est dans la politique de confidentialité.",
  },
  {
    q: 'Mes emails partent-ils chez un fournisseur d\'IA ?',
    a: "Uniquement quand vous lancez une analyse, et seulement l'expéditeur, le sujet et un extrait de 200 caractères, chez Mistral. Les règles de tri, elles, sont purement déterministes : elles ne font appel à aucune IA et ne consomment aucun quota.",
  },
  {
    q: 'Que se passe-t-il si Mailsorter se trompe ?',
    a: "Rien d'irréversible. Une suppression part à la corbeille Gmail, récupérable 30 jours. Chaque action est inscrite dans un journal qui dit qui l'a décidée, et les actions réversibles s'annulent d'un clic. Vous pouvez aussi protéger des expéditeurs, qui ne seront jamais archivés ni supprimés automatiquement.",
  },
  {
    q: 'Dois-je tout valider à la main ?',
    a: "Au début, oui : l'IA propose, vous disposez. Ensuite vous automatisez ce qui est évident, par des règles ou un auto-pilote par expéditeur. Un aperçu montre ce que vos règles feraient avant qu'elles ne touchent quoi que ce soit.",
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

function Login() {
  const navigate = useNavigate();
  // La page de tarifs n'existe pas sur une instance auto-hébergée, qui ne
  // facture personne : sans cette lecture, le bouton "Tarifs" menait à une
  // redirection vers la page qu'on venait de quitter. Le Header applique déjà
  // cette règle, la landing ne la connaissait pas.
  const { selfHosted, billingOn } = useInstance();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [providers, setProviders] = useState([]);

  const [waitlistEmail, setWaitlistEmail] = useState('');
  const [joining, setJoining] = useState(false);
  const [joined, setJoined] = useState(hasJoinedWaitlist);
  const [waitlistError, setWaitlistError] = useState('');

  useEffect(() => {
    if (localStorage.getItem('userEmail')) navigate('/inbox');
    // Le code OAuth était aussi traité ici, en doublon d'AuthCallback, alors que
    // GMAIL_REDIRECT_URL ne désigne qu'une seule adresse (/auth/callback). Ce
    // second chemin ne pouvait donc pas être emprunté, et avait déjà divergé :
    // il ne lisait pas le paramètre `error` renvoyé par un refus Google.
  }, [navigate]);

  // Ce que cette instance sait réellement joindre, lu dans le même catalogue que
  // celui avec lequel le serveur se connecte. Un échec coûte une bande, pas la
  // page.
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

  // Le seul appel à l'action était "Continuer avec Gmail", qui exige tout de
  // suite un accès complet à la boîte. Un visiteur intéressé mais pas prêt à
  // autoriser ça au premier contact n'avait aucun moyen de se manifester, alors
  // que la capture d'adresse existait déjà, une page plus loin.
  const handleWaitlist = async (e) => {
    e.preventDefault();
    const email = waitlistEmail.trim();
    if (!email || joining) return;
    setJoining(true);
    setWaitlistError('');
    try {
      await waitlistService.join(email, 'landing');
      rememberWaitlistJoin();
      setJoined(true);
      track('waitlist_join', { source: 'landing' });
    } catch (err) {
      setWaitlistError(apiError(err, 'Inscription impossible pour le moment. Réessayez.'));
    } finally {
      setJoining(false);
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
            <span className="hidden chip border border-hairline bg-surface text-ink-600 sm:inline-flex">
              <Shield size={14} className="text-brand-600" /> OAuth Google sécurisé
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
              Mailsorter lit, comprend et range vos emails Gmail à votre place. Stop au scroll infini :
              atteignez l'Inbox Zero en quelques clics, et gardez-la propre pour toujours.
            </p>

            <div className="mt-8 flex flex-col items-start gap-4 sm:flex-row sm:items-center">
              <button onClick={handleLogin} disabled={loading} className="btn-primary px-6 py-3.5 text-base">
                {loading ? (
                  <>
                    <Spinner size={20} className="text-white" /> Connexion...
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

            <p className="mt-4 text-sm text-ink-500">
              Pas encore prêt à donner accès à votre boîte ?{' '}
              <a href="#garder-contact" className="font-semibold text-brand-600 underline-offset-2 hover:underline">
                Laissez-nous votre adresse
              </a>
              .
            </p>

            {error && (
              <div className="mt-5 inline-flex items-center gap-2 rounded-xl border border-danger-100 bg-danger-50 px-4 py-2.5 text-sm text-danger-700">
                {error}
              </div>
            )}
          </div>

          {/* Illustration de l'écran de suggestions. Annoncée comme telle : ce
              n'est pas une capture du produit, et la faire passer pour une
              capture est exactement ce qu'on reproche aux pages de vente. */}
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
                <button className="btn-primary mt-4 w-full py-3" tabIndex={-1} aria-hidden>
                  <Bolt size={16} /> Tout appliquer
                </button>
              </div>
            </div>
            <p className="mt-2 text-center text-xs text-ink-400">Illustration de l'écran de suggestions</p>
          </div>
        </section>

        {/* Boîtes joignables : lu sur le serveur, jamais codé en dur. La page ne
            parlait que de Gmail alors que le catalogue en connaît bien plus, et
            un visiteur Outlook ou Proton repartait en croyant que le produit ne
            le concernait pas. */}
        {providers.length > 0 && (
          <section className="mt-14 rounded-2xl border border-hairline bg-surface/60 px-6 py-5">
            <p className="text-xs font-semibold uppercase tracking-wider text-ink-500">
              Boîtes joignables par cette instance
            </p>
            <ul className="mt-3 flex flex-wrap gap-1.5">
              {providers.map((p) => (
                <li key={p.key} className="chip bg-ink-100 text-ink-700">
                  {p.name}
                </li>
              ))}
            </ul>
            {/* Le compte Mailsorter s'ouvre avec Google, et la boite se branche
                ensuite depuis l'application, ecran Connexion d'une boite. Dire
                l'ordre evite la lecture inverse : que seul Gmail est servi. */}
            <p className="mt-3 text-xs text-ink-500">
              Vous ouvrez votre compte avec Google, puis vous branchez la boite de votre choix
              depuis l'application.
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
            {FEATURES.map(({ Icon, title, text, soon }) => (
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

        {/* FAQ. <details> plutôt qu'un accordéon maison : l'ouverture, le
            clavier et la recherche dans la page marchent sans une ligne de JS. */}
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
            Connectez Gmail et regardez le désordre disparaître. C'est gratuit, et ça prend 30 secondes.
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

        {/* Garder le contact sans donner sa boîte */}
        <section id="garder-contact" className="card mt-6 p-8 text-center">
          <span className="mx-auto mb-4 flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
            <Mail size={22} />
          </span>
          <h3 className="text-lg font-bold text-ink-900">Pas encore prêt ?</h3>
          <p className="mx-auto mt-1.5 max-w-md text-sm leading-relaxed text-ink-600">
            Laissez votre adresse : vous serez prévenu des nouveautés et de l'ouverture du plan Pro.
            Pas d'accès à votre boîte, pas de compte à créer.
          </p>

          {joined ? (
            <p className="mt-5 inline-flex items-center gap-2 rounded-xl bg-positive-50 px-4 py-2.5 text-sm font-semibold text-positive-700">
              <Check size={16} /> C'est noté. On vous écrit.
            </p>
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

          {waitlistError && (
            <p role="alert" className="mt-3 text-sm text-danger-700">
              {waitlistError}
            </p>
          )}

          {!selfHosted && billingOn && (
            <p className="mt-4 text-xs text-ink-500">
              Le plan Pro est déjà disponible :{' '}
              <button
                onClick={() => navigate('/pricing')}
                className="font-semibold text-brand-600 underline-offset-2 hover:underline"
              >
                voir les tarifs
              </button>
              .
            </p>
          )}
        </section>

        <PublicFooter />
      </div>
    </div>
  );
}

export default Login;
