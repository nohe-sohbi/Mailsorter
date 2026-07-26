import React, { useState } from 'react';
import { configService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Logo, Shield, Refresh, Check } from '../ui/icons';
import Spinner from '../ui/Spinner';

const GUIDE = [
  { strong: 'Google Cloud Console', rest: ' : créez (ou sélectionnez) un projet.' },
  { strong: 'Activez l’API Gmail', rest: ' dans « APIs & Services ».' },
  { strong: 'Créez un ID OAuth 2.0', rest: ' de type « Application Web ».' },
  { strong: "Ajoutez l'URI de redirection", rest: ' ci-contre aux URI autorisés.' },
  { strong: 'Copiez Client ID & Secret', rest: ' dans les variables d’environnement.' },
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
  const [checking, setChecking] = useState(false);
  const redirectUri = `${window.location.origin}/auth/callback`;

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
                Branchez votre Gmail
              </h1>
              <p className="text-sm text-ink-500">
                Une configuration unique, valable pour toute l'instance.
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
              <div key={name} className="rounded-xl border border-ink-200/70 bg-ink-50/60 px-4 py-3">
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

          <p className="mt-4 flex items-center justify-center gap-2 text-xs text-ink-400">
            <Shield size={14} /> Le secret ne transite jamais par le navigateur.
          </p>
        </div>

        {/* Guide */}
        <div className="animate-fade-up rounded-2xl border border-ink-200/70 bg-white/60 p-8 [animation-delay:100ms]">
          <span className="chip bg-brand-50 text-brand-700">Guide express · 2 min</span>
          <h2 className="mt-4 text-lg font-bold text-ink-900">Obtenir vos identifiants Google</h2>
          <ol className="mt-5 space-y-4">
            {GUIDE.map((step, i) => (
              <li key={i} className="flex gap-3">
                <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-brand-600 text-xs font-bold text-white">
                  {i + 1}
                </span>
                <p className="pt-0.5 text-sm leading-relaxed text-ink-600">
                  {i === 0 ? (
                    <a
                      href="https://console.cloud.google.com/"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="font-semibold text-brand-600 underline-offset-2 hover:underline"
                    >
                      {step.strong}
                    </a>
                  ) : (
                    <span className="font-semibold text-ink-800">{step.strong}</span>
                  )}
                  {step.rest}
                </p>
              </li>
            ))}
          </ol>

          <div className="mt-6 rounded-xl border border-ink-200/70 bg-white px-4 py-3">
            <p className="flex items-center gap-2 text-xs font-semibold text-ink-700">
              <Check size={14} className="text-emerald-600" /> URI de redirection à déclarer
            </p>
            <p className="mt-1.5 break-all font-mono text-xs text-ink-500">{redirectUri}</p>
          </div>
        </div>
      </div>
    </div>
  );
}

export default Setup;
