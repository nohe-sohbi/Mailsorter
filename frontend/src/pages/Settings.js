import React, { useEffect, useState } from 'react';
import { protectService, accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { Settings as SettingsIcon, Shield, X, Mail, Trash, Alert, Refresh } from '../ui/icons';
import Spinner from '../ui/Spinner';

// Opt in to hands-free background syncing: a scheduler periodically pulls the
// inbox and (when rule autopilot is on) applies the user's deterministic rules,
// with no manual click. Off by default so Mailsorter never touches Gmail
// unprompted.
function AutoSyncSettings() {
  const toast = useToast();
  const [enabled, setEnabled] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    (async () => {
      try {
        const { data } = await accountService.getSettings();
        setEnabled(!!data.autoSyncEnabled);
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
      const { data } = await accountService.updateSettings({ autoSyncEnabled: next });
      setEnabled(!!data.autoSyncEnabled);
      toast.success(next ? 'Synchronisation automatique activée.' : 'Synchronisation automatique désactivée.');
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
          <Refresh size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Synchronisation automatique</h2>
      </div>
      <p className="mb-5 text-sm text-ink-500">
        Laissez Mailsorter <span className="font-semibold text-ink-700">synchroniser votre boîte en arrière-plan</span>,
        sans aucun clic. Si l'<span className="font-semibold text-ink-700">application automatique des règles</span> est
        activée (page Règles), vos règles déterministes trient aussi vos nouveaux emails toutes seules — le chemin
        mains-libres vers l'Inbox Zero.
      </p>

      {loading ? (
        <div className="flex justify-center py-6">
          <Spinner size={22} className="text-brand-500" />
        </div>
      ) : (
        <label className="flex items-center gap-2 text-sm font-semibold text-ink-700">
          <input
            type="checkbox"
            className="h-4 w-4 accent-brand-600"
            checked={enabled}
            disabled={saving}
            onChange={(e) => persist(e.target.checked)}
          />
          Synchroniser ma boîte automatiquement
          {saving && <Spinner size={16} className="ml-1 text-brand-500" />}
        </label>
      )}
    </div>
  );
}

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

// Settings holds only per-user preferences. The Gmail API credentials used to
// live here too, but they are a single instance-wide OAuth app shared by every
// account: editing them from a user-facing screen let anyone reconfigure the
// login flow for everyone. They now come from the deployment environment and
// have no UI at all.
function Settings() {
  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-8 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <SettingsIcon size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Réglages</h1>
          <p className="text-sm text-ink-500">Synchronisation automatique, digest quotidien, expéditeurs protégés et vos données.</p>
        </div>
      </div>

      <AutoSyncSettings />
      <DigestSettings />
      <ProtectedSenders />
      <PrivacyData />
    </div>
  );
}

export default Settings;
