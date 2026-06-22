import React, { useEffect, useState } from 'react';
import { configService, protectService, accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Settings as SettingsIcon, Shield, Check, X, Mail, Trash, Alert } from '../ui/icons';
import Spinner from '../ui/Spinner';

// Opt in to the daily email digest: a once-a-day recap of the last 7 days of
// triage, sent to the user's own inbox at a chosen UTC hour.
function DigestSettings() {
  const toast = useToast();
  const [enabled, setEnabled] = useState(false);
  const [hour, setHour] = useState(7);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const { data } = await accountService.getSettings();
        setEnabled(!!data.digestEnabled);
        setHour(typeof data.digestHourUTC === 'number' && data.digestHourUTC > 0 ? data.digestHourUTC : 7);
      } catch (err) {
        // Silent: the card still renders with defaults.
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  const persist = async (next) => {
    setSaving(true);
    try {
      const { data } = await accountService.updateSettings({
        digestEnabled: next.enabled,
        digestHourUTC: next.hour,
      });
      setEnabled(!!data.digestEnabled);
      setHour(data.digestHourUTC || 7);
      toast.success(next.enabled ? 'Digest quotidien activé.' : 'Réglages du digest enregistrés.');
    } catch (err) {
      toast.error('Enregistrement impossible.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="card animate-fade-up mt-6 p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-50 text-brand-600">
          <Mail size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Digest quotidien</h2>
      </div>
      <p className="mb-5 text-sm text-ink-500">
        Recevez chaque jour un <span className="font-semibold text-ink-700">récap de votre tri des 7 derniers jours</span>,
        directement dans votre boîte. Envoyé à l'heure choisie (UTC).
        <span className="mt-1 block text-xs text-ink-400">
          Astuce : si rien n'arrive, reconnectez Gmail pour autoriser l'envoi.
        </span>
      </p>

      {loading ? (
        <div className="flex justify-center py-6">
          <Spinner size={22} className="text-brand-500" />
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-4">
          <label className="flex items-center gap-2 text-sm font-semibold text-ink-700">
            <input
              type="checkbox"
              className="h-4 w-4 accent-brand-600"
              checked={enabled}
              disabled={saving}
              onChange={(e) => persist({ enabled: e.target.checked, hour })}
            />
            Activer l'envoi quotidien
          </label>

          <label className="flex items-center gap-2 text-sm text-ink-600">
            Heure (UTC)
            <select
              className="input w-auto"
              value={hour}
              disabled={saving || !enabled}
              onChange={(e) => persist({ enabled, hour: parseInt(e.target.value, 10) })}
            >
              {Array.from({ length: 24 }, (_, h) => (
                <option key={h} value={h}>{String(h).padStart(2, '0')}:00</option>
              ))}
            </select>
          </label>

          {saving && <Spinner size={18} className="text-brand-500" />}
        </div>
      )}
    </div>
  );
}

// Manage the protected-senders list: addresses or whole domains that no
// automated pass (AI, rules, auto-pilot, bulk) may ever archive, trash or
// delete. A safety net for your VIPs.
function ProtectedSenders() {
  const toast = useToast();
  const [items, setItems] = useState([]);
  const [value, setValue] = useState('');
  const [loading, setLoading] = useState(true);
  const [adding, setAdding] = useState(false);

  const load = async () => {
    try {
      const { data } = await protectService.list();
      setItems(data.protected || []);
    } catch (err) {
      // Silent: the card still renders with an empty list.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleAdd = async (e) => {
    e.preventDefault();
    const v = value.trim();
    if (!v) return;
    setAdding(true);
    try {
      const { data } = await protectService.add(v);
      setItems((prev) => [data, ...prev.filter((i) => i.value !== data.value)]);
      setValue('');
      toast.success(`${data.value} protégé`);
    } catch (err) {
      toast.error(err.response?.data?.trim() || 'Ajout impossible.');
    } finally {
      setAdding(false);
    }
  };

  const handleRemove = async (item) => {
    setItems((prev) => prev.filter((i) => i.id !== item.id));
    try {
      await protectService.remove(item.id);
    } catch (err) {
      toast.error('Suppression impossible.');
      load();
    }
  };

  return (
    <div className="card animate-fade-up mt-6 p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600">
          <Shield size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Expéditeurs protégés</h2>
      </div>
      <p className="mb-5 text-sm text-ink-500">
        Leurs emails ne seront <span className="font-semibold text-ink-700">jamais archivés ni supprimés automatiquement</span> —
        ni par l'IA, ni par les règles, ni en masse. Ajoutez une adresse (<span className="font-mono text-xs">boss@corp.com</span>)
        ou un domaine entier (<span className="font-mono text-xs">corp.com</span>).
      </p>

      <form onSubmit={handleAdd} className="mb-5 flex gap-2">
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="input flex-1"
          placeholder="adresse@exemple.com ou exemple.com"
        />
        <button type="submit" disabled={adding} className="btn-primary shrink-0">
          {adding ? <Spinner size={18} /> : <Shield size={16} />} Protéger
        </button>
      </form>

      {loading ? (
        <div className="flex justify-center py-6">
          <Spinner size={22} className="text-brand-500" />
        </div>
      ) : items.length === 0 ? (
        <p className="rounded-xl bg-ink-50 px-4 py-3 text-sm text-ink-400">
          Aucun expéditeur protégé pour l'instant.
        </p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {items.map((item) => (
            <span
              key={item.id || item.value}
              className="chip group bg-emerald-50 text-emerald-700"
              title={item.kind === 'domain' ? 'Domaine entier' : 'Adresse'}
            >
              <Shield size={13} />
              <span className="font-mono text-xs">{item.value}</span>
              <button
                onClick={() => handleRemove(item)}
                className="ml-0.5 rounded-full p-0.5 text-emerald-500 hover:bg-emerald-100 hover:text-emerald-700"
                aria-label="Retirer"
              >
                <X size={13} />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// RGPD controls: export everything Mailsorter stores about you (portability),
// and permanently erase your account and all derived data (right to erasure).
// Your Gmail mailbox is never touched — only the artifacts Mailsorter created.
function PrivacyData() {
  const toast = useToast();
  const [exporting, setExporting] = useState(false);
  const [confirm, setConfirm] = useState('');
  const [deleting, setDeleting] = useState(false);

  const exportData = async () => {
    setExporting(true);
    try {
      const { data } = await accountService.exportData();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `mailsorter-export-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      toast.success('Export téléchargé.');
    } catch (err) {
      toast.error('Export impossible.');
    } finally {
      setExporting(false);
    }
  };

  const deleteAccount = async () => {
    setDeleting(true);
    try {
      await accountService.deleteAccount();
      toast.success('Compte et données supprimés. À bientôt.');
      localStorage.removeItem('accessToken');
      localStorage.removeItem('userEmail');
      setTimeout(() => window.location.assign('/'), 800);
    } catch (err) {
      toast.error('Suppression impossible.');
      setDeleting(false);
    }
  };

  return (
    <div className="card animate-fade-up mt-6 p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-50 text-brand-600">
          <Shield size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Données &amp; confidentialité</h2>
      </div>
      <p className="mb-5 text-sm text-ink-500">
        Vos emails ne quittent jamais votre contrôle. Récupérez tout ce que Mailsorter stocke à votre sujet,
        ou effacez définitivement votre compte.
      </p>

      <div className="flex flex-wrap items-center gap-3">
        <button onClick={exportData} disabled={exporting} className="btn-secondary">
          {exporting ? <Spinner size={16} /> : <Shield size={16} />} Exporter mes données
        </button>
        <span className="text-xs text-ink-400">Un fichier JSON : règles, expéditeurs protégés, historique, réglages…</span>
      </div>

      <div className="mt-6 rounded-xl border border-rose-200 bg-rose-50/60 p-5">
        <div className="mb-1 flex items-center gap-2 text-rose-700">
          <Alert size={16} />
          <h3 className="text-sm font-bold">Zone de danger</h3>
        </div>
        <p className="mb-4 text-sm text-rose-600/90">
          La suppression efface définitivement votre compte et toutes vos données Mailsorter (règles, protections,
          reports, historique). Action <span className="font-semibold">irréversible</span>. Votre boîte Gmail n'est pas affectée.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <input
            className="input max-w-[220px] border-rose-200"
            placeholder="Tapez SUPPRIMER"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            aria-label="Confirmation de suppression"
          />
          <button
            onClick={deleteAccount}
            disabled={deleting || confirm.trim().toUpperCase() !== 'SUPPRIMER'}
            className="btn-danger"
          >
            {deleting ? <Spinner size={16} /> : <Trash size={16} />} Supprimer mon compte
          </button>
        </div>
      </div>
    </div>
  );
}

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
          <p className="text-sm text-ink-500">Connexion à l'API Gmail, digest quotidien et expéditeurs protégés.</p>
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

      <DigestSettings />
      <ProtectedSenders />
      <PrivacyData />
    </div>
  );
}

export default Settings;
