import React, { useEffect, useState } from 'react';
import { useInstance } from '../contexts/InstanceContext';
import { configService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Logo, Shield, Refresh, Check } from '../ui/icons';
import Spinner from '../ui/Spinner';

// The steps for someone creating THEIR OWN Google Cloud project, which is what
// the self-hosted edition asks for. Step 3 is the one that looks optional and
// is not: an app left in Test status expires its authorization every seven
// days, so the instance simply stops working a week after it was set up, with
// no obvious cause. It is marked critical so it is not read as boilerplate.
const OWN_PROJECT_GUIDE = [
  { strong: 'Google Cloud Console', rest: ' : créez un projet, puis activez l\'API Gmail.', link: 'https://console.cloud.google.com/' },
  { strong: "Configurez l'écran de consentement", rest: ' avec votre propre adresse.' },
  {
    strong: 'Cliquez sur Publier l\'application',
    rest: ' : sans ce clic vous restez en mode Test et votre connexion expire au bout de sept jours.',
    critical: true,
  },
  { strong: 'Créez un ID OAuth 2.0', rest: ' de type « Application Web », pas Desktop.' },
  { strong: "Ajoutez l'URI de redirection", rest: ' ci-contre aux URI autorisés.' },
  { strong: 'Copiez Client ID et Secret', rest: ' dans les variables d\'environnement.' },
];

// Same destination, different owner: on the managed service the OAuth app
// belongs to whoever runs the deployment, so there is nothing here about
// publishing or about a personal-use exemption.
const OPERATOR_GUIDE = [
  { strong: 'Google Cloud Console', rest: ' : créez (ou sélectionnez) un projet.', link: 'https://console.cloud.google.com/' },
  { strong: 'Activez l\'API Gmail', rest: ' dans « APIs & Services ».' },
  { strong: 'Créez un ID OAuth 2.0', rest: ' de type « Application Web ».' },
  { strong: "Ajoutez l'URI de redirection", rest: ' ci-contre aux URI autorisés.' },
  { strong: 'Copiez Client ID et Secret', rest: ' dans les variables d\'environnement.' },
];

// Setup is a read-only briefing, not a form.
//
// The Gmail credentials are a single OAuth app shared by every account on the
// instance, so they belong to the deployment, not to a user-facing screen: when
// they were editable over HTTP, anyone could point the login flow somewhere
// else. They are now read from the environment at boot, and this page only
// explains how to supply them.
function Setup({ onComplete }) {
  const toast = useToast();
  const { selfHosted } = useInstance();
  const [checking, setChecking] = useState(false);
  const [providers, setProviders] = useState([]);
  const redirectUri = `${window.location.origin}/auth/callback`;
  const guide = selfHosted ? OWN_PROJECT_GUIDE : OPERATOR_GUIDE;

  // What this instance can actually reach, read from the same catalog the
  // backend connects with. Failing to load it costs a panel, not the page:
  // the setup instructions are what the visitor came for.
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

  const envVars = [
    { name: 'GMAIL_CLIENT_ID', value: '123456789-abc.apps.googleusercontent.com' },
    { name: 'GMAIL_CLIENT_SECRET', value: 'GOCSPX-••••••••••••••••' },
    { name: 'GMAIL_REDIRECT_URL', value: redirectUri },
  ];

  const handleRecheck = async () => {
    setChecking(true);
    try {
      const { data } = await configService.getStatus();
      if (data.isConfigured) {
        toast.success('Identifiants détectés. C’est parti !');
        if (onComplete) onComplete();
      } else {
        toast.info('Toujours rien côté serveur. Redémarrez le service après avoir renseigné les variables.');
      }
    } catch (err) {
      toast.error('Le serveur ne répond pas.');
    } finally {
      setChecking(false);
    }
  };

  return (
    <div className="min-h-screen bg-ink-50 px-4 py-12">
      <div className="mx-auto grid max-w-5xl gap-6 lg:grid-cols-[1.1fr_0.9fr]">
        {/* Instructions */}
        <div className="card animate-fade-up p-8">
          <div className="mb-6 flex items-center gap-3">
            <Logo size={36} />
            <div>
              <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">
                {selfHosted ? 'Branchez votre boîte' : 'Branchez votre Gmail'}
              </h1>
              <p className="text-sm text-ink-500">
                {selfHosted
                  ? "Votre instance, votre projet Google, vos identifiants. Rien ne sort d'ici."
                  : "Une configuration unique, valable pour toute l'instance."}
              </p>
            </div>
          </div>

          <p className="text-sm leading-relaxed text-ink-600">
            Mailsorter lit les identifiants OAuth dans son environnement. Renseignez ces trois
            variables là où tourne le serveur (fichier <code className="rounded bg-ink-100 px-1 py-0.5 font-mono text-xs">.env</code>,
            docker-compose ou panneau d'hébergement), puis redémarrez le service.
          </p>

          <dl className="mt-6 space-y-3">
            {envVars.map(({ name, value }) => (
              <div key={name} className="rounded-xl border border-hairline/70 bg-ink-50/60 px-4 py-3">
                <dt className="font-mono text-xs font-bold text-ink-800">{name}</dt>
                <dd className="mt-1 break-all font-mono text-xs text-ink-500">{value}</dd>
              </div>
            ))}
          </dl>

          <p className="mt-5 text-xs leading-relaxed text-ink-500">
            L'URI de redirection doit correspondre exactement à celui déclaré dans Google Cloud
            Console, sans quoi Google refusera la connexion.
          </p>

          <button onClick={handleRecheck} disabled={checking} className="btn-primary mt-6 w-full">
            {checking ? (
              <>
                <Spinner size={18} /> Vérification…
              </>
            ) : (
              <>
                <Refresh size={18} /> J'ai renseigné les variables
              </>
            )}
          </button>

          <p className="mt-4 flex items-center justify-center gap-2 text-xs text-muted">
            <Shield size={14} /> Le secret ne transite jamais par le navigateur.
          </p>
        </div>

        {/* Guide */}
        <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-8 [animation-delay:100ms]">
          <span className="chip bg-brand-50 text-brand-700">Guide express · 2 min</span>
          <h2 className="mt-4 text-lg font-bold text-ink-900">Obtenir vos identifiants Google</h2>
          <ol className="mt-5 space-y-4">
            {guide.map((step, i) => (
              <li key={i} className="flex gap-3">
                <span
                  className={
                    step.critical
                      ? 'flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-caution-fill text-xs font-bold text-white'
                      : 'flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-brand-fill text-xs font-bold text-white'
                  }
                >
                  {i + 1}
                </span>
                <p className="pt-0.5 text-sm leading-relaxed text-ink-600">
                  {step.link ? (
                    <a
                      href={step.link}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="font-semibold text-brand-600 underline-offset-2 hover:underline"
                    >
                      {step.strong}
                    </a>
                  ) : (
                    <span className={step.critical ? 'font-semibold text-caution-700' : 'font-semibold text-ink-800'}>
                      {step.strong}
                    </span>
                  )}
                  {step.rest}
                </p>
              </li>
            ))}
          </ol>

          <div className="mt-6 rounded-xl border border-hairline/70 bg-surface px-4 py-3">
            <p className="flex items-center gap-2 text-xs font-semibold text-ink-700">
              <Check size={14} className="text-positive-600" /> URI de redirection à déclarer
            </p>
            <p className="mt-1.5 break-all font-mono text-xs text-ink-500">{redirectUri}</p>
          </div>

          {providers.length > 0 && (
            <div className="mt-6 border-t border-hairline/70 pt-5">
              <p className="text-xs font-semibold uppercase tracking-wider text-ink-500">
                Joignables par cette instance
              </p>
              <ul className="mt-3 flex flex-wrap gap-1.5">
                {providers.map((p) => (
                  <li key={p.key} className="chip bg-ink-100 text-ink-700" title={p.routes[0]?.note || ''}>
                    {p.name}
                  </li>
                ))}
              </ul>
              <p className="mt-3 text-xs leading-relaxed text-ink-500">
                {selfHosted
                  ? "Cette liste vient de votre serveur. En auto-hébergé elle contient Gmail par l'API complète et Proton, que le service en ligne ne peut pas atteindre."
                  : 'Cette liste vient du serveur : elle ne contient que ce que cette édition sait réellement joindre.'}
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export default Setup;
