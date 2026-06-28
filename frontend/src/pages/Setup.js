import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { configService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Logo, Shield, ChevronRight } from '../ui/icons';
import Spinner from '../ui/Spinner';

const GUIDE = [
  { strong: 'Google Cloud Console', rest: ' → créez (ou sélectionnez) un projet.' },
  { strong: 'Activez l’API Gmail', rest: ' dans « APIs & Services ».' },
  { strong: 'Créez un ID OAuth 2.0', rest: ' de type « Application Web ».' },
  { strong: "Ajoutez l'URI de redirection", rest: ' ci-dessous aux URI autorisés.' },
  { strong: 'Copiez Client ID & Secret', rest: ' puis collez-les ici.' },
];

function Setup({ onComplete }) {
  const navigate = useNavigate();
  const toast = useToast();
  const [formData, setFormData] = useState({
    clientId: '',
    clientSecret: '',
    redirectUrl: `${window.location.origin}/auth/callback`,
  });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleChange = (e) => setFormData({ ...formData, [e.target.name]: e.target.value });

  const handleSubmit = async (e) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      await configService.saveGmailConfig(formData);
      toast.success('Configuration enregistrée. C’est parti !');
      if (onComplete) onComplete();
      navigate('/');
    } catch (err) {
      setError(err.response?.data || 'Impossible d’enregistrer la configuration.');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-ink-50 px-4 py-12">
      <div className="mx-auto grid max-w-5xl gap-6 lg:grid-cols-[1.1fr_0.9fr]">
        {/* Form */}
        <div className="card animate-fade-up p-8">
          <div className="mb-6 flex items-center gap-3">
            <Logo size={36} />
            <div>
              <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">
                Branchez votre Gmail
              </h1>
              <p className="text-sm text-ink-500">Une configuration unique, valable pour toute l'app.</p>
            </div>
          </div>

          {error && (
            <div className="mb-5 rounded-xl border border-rose-200 bg-rose-50 px-4 py-3 text-sm font-medium text-rose-600">
              {error}
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-5">
            <div>
              <label htmlFor="clientId" className="mb-1.5 block text-sm font-semibold text-ink-700">
                Client ID
              </label>
              <input
                id="clientId"
                name="clientId"
                value={formData.clientId}
                onChange={handleChange}
                required
                className="input font-mono text-xs"
                placeholder="123456789-abc.apps.googleusercontent.com"
              />
            </div>
            <div>
              <label htmlFor="clientSecret" className="mb-1.5 block text-sm font-semibold text-ink-700">
                Client Secret
              </label>
              <input
                id="clientSecret"
                name="clientSecret"
                type="password"
                value={formData.clientSecret}
                onChange={handleChange}
                required
                className="input font-mono text-xs"
                placeholder="GOCSPX-••••••••••••••••"
              />
            </div>
            <div>
              <label htmlFor="redirectUrl" className="mb-1.5 block text-sm font-semibold text-ink-700">
                URI de redirection
              </label>
              <input
                id="redirectUrl"
                name="redirectUrl"
                value={formData.redirectUrl}
                onChange={handleChange}
                required
                className="input font-mono text-xs"
              />
              <p className="mt-1.5 text-xs text-ink-400">
                Doit correspondre exactement à l'URI déclaré dans Google Cloud Console.
              </p>
            </div>

            <button type="submit" disabled={loading} className="btn-primary w-full">
              {loading ? (
                <>
                  <Spinner size={18} /> Enregistrement…
                </>
              ) : (
                <>
                  Enregistrer et continuer <ChevronRight size={18} />
                </>
              )}
            </button>

            <p className="flex items-center justify-center gap-2 text-xs text-ink-400">
              <Shield size={14} /> Le secret est chiffré côté serveur, jamais exposé.
            </p>
          </form>
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
        </div>
      </div>
    </div>
  );
}

export default Setup;
