import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { protectService, accountService, authService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { Toggle, EmptyState, ErrorState } from '../ui/primitives';
import { track } from '../lib/analytics';
import { Settings as SettingsIcon, Shield, X, Mail, Refresh, Google } from '../ui/icons';
import Spinner from '../ui/Spinner';

// The API answers with a JSON object on some routes and a bare string on others
// (the protected-senders validation errors are plain text). One reader for both,
// so a real server message is never swallowed in favour of a generic fallback.
function errText(err, fallback) {
  const data = err?.response?.data;
  if (typeof data === 'string' && data.trim()) return data.trim();
  return data?.error || fallback;
}

const DEFAULT_DIGEST_HOUR = 7;
const HOURS = Array.from({ length: 24 }, (_, h) => h);
const pad2 = (n) => String(n).padStart(2, '0');

// Midnight is a legitimate choice. The previous guard (`digestHourUTC > 0`)
// treated 0 as "unset" and silently rewrote it to 7, so picking midnight was
// impossible: the select snapped back on every reload.
function normalizeHour(value) {
  return Number.isInteger(value) && value >= 0 && value <= 23 ? value : DEFAULT_DIGEST_HOUR;
}

// The real instant matching `utcHour` today. Going through an actual Date is
// what makes the conversion correct for daylight saving and for the zones whose
// offset is not a whole number of hours; a hardcoded +1/+2 would not be.
function instantAt(utcHour) {
  const now = new Date();
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), utcHour, 0, 0));
}

function localTimeLabel(utcHour) {
  return instantAt(utcHour).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' });
}

// Far enough from Greenwich, an early UTC hour lands on the previous day
// locally. Saying only "23:00 chez vous" would let the user expect the digest
// on the wrong day.
function dayShift(utcHour) {
  const d = instantAt(utcHour);
  const diff =
    Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) -
    Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
  return Math.round(diff / 86400000);
}

function localZoneName() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  } catch (err) {
    // Older engines can throw here; the hour itself is still shown.
    return '';
  }
}

// Opt in to hands-free background syncing: a scheduler periodically pulls the
// inbox and (when rule autopilot is on) applies the user's deterministic rules,
// with no manual click. Off by default so Mailsorter never touches Gmail
// unprompted.
//
// The settings come from the parent: this card used to fetch them itself, which
// meant three identical GETs per visit.
function AutoSyncSettings({ settings, onSaved }) {
  const toast = useToast();
  const [saving, setSaving] = useState(false);
  const enabled = !!settings.autoSyncEnabled;

  const persist = async (next) => {
    setSaving(true);
    try {
      const { data } = await accountService.updateSettings({ autoSyncEnabled: next });
      onSaved(data);
      toast.success(next ? 'Synchronisation automatique activée.' : 'Synchronisation automatique désactivée.');
    } catch (err) {
      toast.error(errText(err, 'Enregistrement impossible.'));
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
      <p className="mb-5 text-sm text-muted">
        Laissez Mailsorter <span className="font-semibold text-ink-700">synchroniser votre boîte en arrière-plan</span>,
        sans aucun clic. Si l'<span className="font-semibold text-ink-700">application automatique des règles</span> est
        activée (page Règles), vos règles déterministes trient aussi vos nouveaux emails toutes seules, le chemin
        mains-libres vers l'Inbox Zero.
      </p>

      <div className="rounded-xl border border-hairline p-4">
        <Toggle
          id="auto-sync"
          checked={enabled}
          disabled={saving}
          onChange={persist}
          label="Synchroniser ma boîte automatiquement"
          description="Mailsorter relève vos nouveaux emails sans que vous ouvriez l'app."
        />
        {saving && (
          <p className="mt-3 flex items-center gap-2 text-xs text-muted">
            <Spinner size={14} className="text-brand-500" /> Enregistrement…
          </p>
        )}
      </div>
    </div>
  );
}

// Opt in to the daily email digest: a once-a-day recap of the last 7 days of
// triage, sent to the user's own inbox at a chosen hour.
//
// The hour travels to the server in UTC (unchanged contract) but is never shown
// alone: the audience is French, i.e. one or two hours ahead depending on the
// season, and "07:00" meant two different things to the user and to the server.
function DigestSettings({ settings, onSaved }) {
  const toast = useToast();
  const [saving, setSaving] = useState(false);
  const enabled = !!settings.digestEnabled;
  const hour = normalizeHour(settings.digestHourUTC);

  // The offset cannot change while the page is open, so the 24 labels are built
  // once instead of on every keystroke elsewhere in the tree.
  const options = useMemo(
    () => HOURS.map((h) => ({ h, label: `${pad2(h)}:00 UTC — ${localTimeLabel(h)} chez vous` })),
    []
  );
  const zone = useMemo(localZoneName, []);
  const shift = dayShift(hour);
  const shiftLabel = shift === 1 ? ' le lendemain' : shift === -1 ? ' la veille' : '';

  const persist = async (next) => {
    setSaving(true);
    try {
      const { data } = await accountService.updateSettings({
        digestEnabled: next.enabled,
        digestHourUTC: next.hour,
      });
      onSaved(data);
      toast.success(next.enabled ? 'Digest quotidien activé.' : 'Réglages du digest enregistrés.');
    } catch (err) {
      toast.error(errText(err, 'Enregistrement impossible.'));
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
      <p className="mb-5 text-sm text-muted">
        Recevez chaque jour un <span className="font-semibold text-ink-700">récap de votre tri des 7 derniers jours</span>,
        directement dans votre boîte.
        <span className="mt-1 block text-xs text-muted">
          Astuce : si rien n'arrive, reconnectez Gmail depuis la carte « Compte Gmail » ci-dessous pour réautoriser
          l'envoi.
        </span>
      </p>

      <div className="rounded-xl border border-hairline p-4">
        <Toggle
          id="digest-enabled"
          checked={enabled}
          disabled={saving}
          onChange={(next) => persist({ enabled: next, hour })}
          label="Activer l'envoi quotidien"
          description="Un seul email par jour, envoyé à vous-même."
        />

        <div className="mt-4 border-t border-hairline pt-4">
          <label htmlFor="digest-hour" className="block text-sm font-bold text-ink-900">
            Heure d'envoi
          </label>
          <select
            id="digest-hour"
            className="select mt-2 w-auto max-w-full"
            value={hour}
            disabled={saving || !enabled}
            onChange={(e) => persist({ enabled, hour: parseInt(e.target.value, 10) })}
          >
            {options.map((o) => (
              <option key={o.h} value={o.h}>
                {o.label}
              </option>
            ))}
          </select>
          <p className="mt-2 text-xs text-muted">
            Programmé à {pad2(hour)}:00 UTC, soit {localTimeLabel(hour)}
            {shiftLabel} chez vous{zone ? ` (${zone})` : ''}.
          </p>
        </div>

        {saving && (
          <p className="mt-3 flex items-center gap-2 text-xs text-muted">
            <Spinner size={14} className="text-brand-500" /> Enregistrement…
          </p>
        )}
      </div>
    </div>
  );
}

// Re-run the Google OAuth flow for the current account.
//
// Nothing in the app offered this, yet the digest card told people to "reconnect
// Gmail" when nothing arrived: the only advice given for an expired or
// insufficient authorisation pointed at a button that did not exist. Same call
// as the login screen (authService.getAuthUrl -> data.authUrl), because it is
// the same flow: the callback route re-issues the session.
function GmailAccount() {
  const toast = useToast();
  const confirm = useConfirm();
  const [redirecting, setRedirecting] = useState(false);
  const email = localStorage.getItem('userEmail') || '';

  const reconnect = async () => {
    // Google reuses whichever account is active in the browser. Signing in with
    // another address would quietly land the user in a different Mailsorter
    // account, so the address to pick is spelled out before leaving the app.
    if (
      !(await confirm({
        title: 'Reconnecter Gmail ?',
        message: email
          ? `Vous allez être redirigé vers Google pour réautoriser Mailsorter. Choisissez bien ${email} : un autre compte vous connecterait ailleurs.`
          : 'Vous allez être redirigé vers Google pour réautoriser Mailsorter.',
        confirmLabel: 'Continuer vers Google',
        cancelLabel: 'Rester ici',
      }))
    ) {
      return;
    }

    setRedirecting(true);
    try {
      const response = await authService.getAuthUrl();
      const authUrl = response.data?.authUrl;
      // A 200 without a URL would otherwise leave the button spinning forever.
      if (!authUrl) throw new Error('authUrl manquant');
      track('gmail_reconnect');
      window.location.href = authUrl;
    } catch (err) {
      toast.error(errText(err, 'Impossible de démarrer la reconnexion. Réessayez.'));
      setRedirecting(false);
    }
  };

  return (
    <div className="card animate-fade-up mt-6 p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-50">
          <Google size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Compte Gmail</h2>
      </div>
      <p className="mb-5 text-sm text-muted">
        Rejouez l'autorisation Google si le digest n'arrive plus, si le tri échoue soudainement, ou si vous avez
        révoqué l'accès depuis votre compte Google.{' '}
        <span className="font-semibold text-ink-700">Vos règles, protections et historique sont conservés.</span>
      </p>

      <div className="flex flex-wrap items-center gap-3">
        <button onClick={reconnect} disabled={redirecting} className="btn-secondary">
          {redirecting ? <Spinner size={16} /> : <Refresh size={16} />} Reconnecter Gmail
        </button>
        {email && (
          <span className="text-sm text-muted">
            Compte connecté : <span className="font-mono text-xs text-ink-700">{email}</span>
          </span>
        )}
      </div>
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
  const [error, setError] = useState(null);
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await protectService.list();
      setItems(data.protected || []);
    } catch (err) {
      // A failed load must never read as "aucun expéditeur protégé": on this
      // card that sentence claims the safety net is gone, which would push the
      // user to re-add VIPs that are in fact still protected.
      setError(errText(err, "La liste n'a pas pu être chargée."));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleAdd = async (e) => {
    e.preventDefault();
    const v = value.trim();
    if (!v) return;
    setAdding(true);
    try {
      const { data } = await protectService.add(v);
      setItems((prev) => [data, ...prev.filter((i) => i.value !== data.value)]);
      setValue('');
      // A successful add also proves the list is reachable again.
      setError(null);
      toast.success(`${data.value} protégé`);
    } catch (err) {
      toast.error(errText(err, 'Ajout impossible.'));
    } finally {
      setAdding(false);
    }
  };

  // Optimistic on purpose: removing a chip is cheap to undo (re-add the same
  // address) and the failure path puts the chip back with a toast.
  const handleRemove = async (item) => {
    setItems((prev) => prev.filter((i) => i.id !== item.id));
    try {
      await protectService.remove(item.id);
    } catch (err) {
      toast.error(errText(err, 'Suppression impossible.'));
      load();
    }
  };

  return (
    <div className="card animate-fade-up mt-6 p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-positive-50 text-positive-600">
          <Shield size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Expéditeurs protégés</h2>
      </div>
      <p className="mb-5 text-sm text-muted">
        Leurs emails ne seront <span className="font-semibold text-ink-700">jamais archivés ni supprimés automatiquement</span>,
        ni par l'IA, ni par les règles, ni en masse. Ajoutez une adresse (<span className="font-mono text-xs">boss@corp.com</span>)
        ou un domaine entier (<span className="font-mono text-xs">corp.com</span>).
      </p>

      <form onSubmit={handleAdd} className="mb-5 flex gap-2">
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="input flex-1"
          placeholder="adresse@exemple.com ou exemple.com"
          aria-label="Adresse ou domaine à protéger"
        />
        <button type="submit" disabled={adding} className="btn-primary shrink-0">
          {adding ? <Spinner size={18} /> : <Shield size={16} />} Protéger
        </button>
      </form>

      {loading ? (
        <div className="flex justify-center py-6">
          <Spinner size={22} className="text-brand-500" />
        </div>
      ) : error ? (
        <ErrorState compact title="Liste indisponible" message={error} onRetry={load} />
      ) : items.length === 0 ? (
        <EmptyState
          compact
          tone="positive"
          Icon={Shield}
          title="Aucun expéditeur protégé"
          description="Ajoutez vos VIP ci-dessus : leurs emails resteront hors de portée de tout tri automatique."
        />
      ) : (
        <div className="flex flex-wrap gap-2">
          {items.map((item) => (
            <span
              key={item.id || item.value}
              className="chip group bg-positive-50 text-positive-700"
              title={item.kind === 'domain' ? 'Domaine entier' : 'Adresse'}
            >
              <Shield size={13} />
              <span className="font-mono text-xs">{item.value}</span>
              <button
                onClick={() => handleRemove(item)}
                className="ml-0.5 rounded-full p-0.5 text-positive-600 transition-colors hover:bg-positive-100 hover:text-positive-700"
                aria-label={`Retirer ${item.value} des expéditeurs protégés`}
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

// Settings holds only per-user preferences.
//
// Two things used to live here and no longer do. The Gmail API credentials are
// a single instance-wide OAuth app shared by every account: editing them from a
// user-facing screen let anyone reconfigure the login flow for everyone, so
// they moved to the deployment environment. Data export and account deletion
// are account-level actions rather than preferences, so they moved to /account.
// Reconnecting Gmail stayed: it re-runs the OAuth flow for this user only and
// changes nothing for anyone else.
function Settings() {
  const [settings, setSettings] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // One GET for the whole screen. Each preference card used to call
  // getSettings() on mount, so opening this page fired the same request three
  // times — and each card failed (or not) on its own, showing defaults as if
  // they were the saved values.
  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await accountService.getSettings();
      setSettings(data || {});
    } catch (err) {
      setError(errText(err, "Vos réglages n'ont pas pu être chargés."));
      setSettings(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // The PUT answers with the full settings record: merging keeps the cards in
  // sync with the server without a second round-trip.
  const applySaved = useCallback((data) => {
    setSettings((prev) => ({ ...(prev || {}), ...(data || {}) }));
  }, []);

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-8 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <SettingsIcon size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Réglages</h1>
          <p className="text-sm text-muted">
            Synchronisation automatique, digest quotidien, compte Gmail et expéditeurs protégés.
          </p>
        </div>
      </div>

      {loading ? (
        <div className="card mt-6 flex justify-center p-10">
          <Spinner size={26} className="text-brand-500" />
        </div>
      ) : error ? (
        <div className="card mt-6">
          <ErrorState title="Réglages indisponibles" message={error} onRetry={load} />
        </div>
      ) : (
        settings && (
          <>
            <AutoSyncSettings settings={settings} onSaved={applySaved} />
            <DigestSettings settings={settings} onSaved={applySaved} />
          </>
        )
      )}

      <GmailAccount />
      <ProtectedSenders />
    </div>
  );
}

export default Settings;
