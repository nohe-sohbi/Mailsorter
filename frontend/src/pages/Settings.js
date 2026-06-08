import React, { useEffect, useState } from 'react';
import { configService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Settings as SettingsIcon, Shield, Check } from '../ui/icons';
import Spinner from '../ui/Spinner';

function Settings() {
  const toast = useToast();
  const [formData, setFormData] = useState({ clientId: '', clientSecret: '', redirectUrl: '' });
  const [originalData, setOriginalData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    fetchConfig();
  }, []);

  const fetchConfig = async () => {
    try {
      const response = await configService.getGmailConfig();
      const data = response.data;
      setFormData({
        clientId: data.clientId || '',
        clientSecret: '',
        redirectUrl: data.redirectUrl || `${window.location.origin}/auth/callback`,
      });
      setOriginalData(data);
    } catch (err) {
      setError('Impossible de charger la configuration.');
    } finally {
      setLoading(false);
    }
  };

  const handleChange = (e) => setFormData({ ...formData, [e.target.name]: e.target.value });

  const handleSubmit = async (e) => {
    e.preventDefault();
    setSaving(true);
    setError('');
    const payload = { clientId: formData.clientId, redirectUrl: formData.redirectUrl };
    if (formData.clientSecret) payload.clientSecret = formData.clientSecret;

    try {
      await configService.saveGmailConfig(payload);
      toast.success('Réglages mis à jour.');
      setFormData({ ...formData, clientSecret: '' });
      setOriginalData({ ...originalData, clientId: formData.clientId, redirectUrl: formData.redirectUrl, isConfigured: true });
    } catch (err) {
      setError(err.response?.data || 'Échec de l’enregistrement.');
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center">
        <Spinner size={28} className="text-brand-500" />
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-8 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <SettingsIcon size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Réglages</h1>
          <p className="text-sm text-ink-500">Gérez la connexion à l'API Gmail.</p>
        </div>
      </div>

      <div className="card animate-fade-up p-7">
        <div className="mb-5 flex items-center justify-between">
          <h2 className="text-lg font-bold text-ink-900">Identifiants Gmail API</h2>
          {originalData?.isConfigured && (
            <span className="chip bg-emerald-50 text-emerald-600">
              <Check size={14} /> Connecté
            </span>
          )}
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
              placeholder="Votre Client ID Gmail API"
            />
          </div>
          <div>
            <label htmlFor="clientSecret" className="mb-1.5 block text-sm font-semibold text-ink-700">
              Client Secret
              {originalData?.isConfigured && (
                <span className="ml-1.5 font-normal text-ink-400">— laissez vide pour conserver l'actuel</span>
              )}
            </label>
            <input
              id="clientSecret"
              name="clientSecret"
              type="password"
              value={formData.clientSecret}
              onChange={handleChange}
              className="input font-mono text-xs"
              placeholder={originalData?.clientSecret || 'Nouveau secret'}
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
          </div>

          <div className="flex items-center justify-between pt-1">
            <p className="flex items-center gap-2 text-xs text-ink-400">
              <Shield size={14} /> Secret chiffré au repos.
            </p>
            <button type="submit" disabled={saving} className="btn-primary">
              {saving ? (
                <>
                  <Spinner size={18} /> Enregistrement…
                </>
              ) : (
                'Mettre à jour'
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

export default Settings;
