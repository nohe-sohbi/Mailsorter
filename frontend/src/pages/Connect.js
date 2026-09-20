import React, { useEffect, useMemo, useState } from 'react';
import { configService, mailboxService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { track } from '../lib/analytics';
import Spinner from '../ui/Spinner';
import { Mail, Shield, Check, Refresh, Trash } from '../ui/icons';
import { detectProvider, ProviderBriefing } from '../components/MailboxBriefing';

// Connecting a mailbox over IMAP.
//
// This screen knows no provider. It renders what GET /api/providers returns,
// and it guesses the provider by matching the typed domain against the domains
// of that same catalog. That is the rule the whole edition split protects: a
// provider offered here but unreachable by the backend, or the reverse, are two
// bugs that cannot happen while the list has one source.
//
// The password asked for is not an account password: it is an app password,
// revocable and limited to mail. The copy says so because it is the first
// question anyone asked for one has.
//
// The provider catalog copy (blockers, capabilities, the briefing panel) is
// shared with the login screen, which asks the same question to sign someone
// in: see components/MailboxBriefing.js.

function ConnectedMailbox({ mailbox, onDisconnect, disconnecting }) {
  return (
    <div className="card animate-fade-up p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-positive-50">
          <Check size={18} className="text-positive-600" />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Boîte branchée</h2>
      </div>
      <p className="mb-5 text-sm text-muted">
        Mailsorter lit cette boîte pour vous. Le mot de passe d'application est chiffré et ne ressort
        jamais, pas même dans votre export de données.
      </p>

      <dl className="space-y-3">
        <div className="rounded-xl border border-hairline/70 bg-ink-50/60 px-4 py-3">
          <dt className="text-xs font-bold uppercase tracking-wider text-ink-500">Adresse</dt>
          <dd className="mt-1 break-all font-mono text-sm text-ink-800">{mailbox.username}</dd>
        </div>
        <div className="rounded-xl border border-hairline/70 bg-ink-50/60 px-4 py-3">
          <dt className="text-xs font-bold uppercase tracking-wider text-ink-500">Serveur</dt>
          <dd className="mt-1 break-all font-mono text-sm text-ink-800">
            {mailbox.host}:{mailbox.port}
          </dd>
        </div>
      </dl>

      <button onClick={onDisconnect} disabled={disconnecting} className="btn-danger mt-6">
        {disconnecting ? <Spinner size={16} /> : <Trash size={16} />} Déconnecter cette boîte
      </button>
      <p className="mt-3 text-xs leading-relaxed text-muted">
        Déconnecter efface le mot de passe enregistré. C'est aussi la façon de retirer l'accès de
        Mailsorter sans passer par votre fournisseur. Votre boîte n'est pas touchée.
      </p>
    </div>
  );
}

function Connect() {
  const toast = useToast();
  const confirm = useConfirm();

  const [loading, setLoading] = useState(true);
  const [mailbox, setMailbox] = useState(null);
  const [providers, setProviders] = useState([]);
  const [address, setAddress] = useState('');
  const [password, setPassword] = useState('');
  const [connecting, setConnecting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);

  useEffect(() => {
    let cancelled = false;
    Promise.allSettled([mailboxService.get(), configService.getProviders()]).then(([box, cat]) => {
      if (cancelled) return;
      if (box.status === 'fulfilled') setMailbox(box.value.data.mailbox || null);
      if (cat.status === 'fulfilled') setProviders(cat.value.data.providers || []);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const detected = useMemo(() => detectProvider(providers, address), [providers, address]);

  const handleConnect = async (e) => {
    e.preventDefault();
    if (!address.trim() || !password) return;
    setConnecting(true);
    try {
      const { data } = await mailboxService.connect(address.trim(), password);
      setMailbox(data);
      // The password leaves the page's memory as soon as it has been used.
      setPassword('');
      setAddress('');
      // No personal data: the provider key is an enumeration value, not an
      // address.
      track('mailbox_connected', { provider: data.provider });
      toast.success('Boîte branchée. La prochaine synchro ira la chercher.');
    } catch (err) {
      const status = err.response?.status;
      track('mailbox_connect_failed', { status: status || 0 });
      if (status === 401) {
        toast.error("Identifiants refusés. Vérifiez qu'il s'agit d'un mot de passe d'application, pas de celui du compte.");
      } else if (status === 422) {
        toast.error("Cette boîte ne se branche pas en IMAP sur cette instance.");
      } else if (status === 502) {
        toast.error('Le serveur de mail ne répond pas. Réessayez dans un instant.');
      } else {
        toast.error("La connexion n'a pas pu être enregistrée.");
      }
    } finally {
      setConnecting(false);
    }
  };

  const handleDisconnect = async () => {
    const confirmed = await confirm({
      title: 'Déconnecter cette boîte ?',
      message:
        "Le mot de passe d'application enregistré sera effacé. Mailsorter ne lira plus cette boîte. Vos règles, protections et historique sont conservés.",
      confirmLabel: 'Déconnecter',
      danger: true,
    });
    if (!confirmed) return;

    setDisconnecting(true);
    try {
      await mailboxService.disconnect();
      setMailbox(null);
      track('mailbox_disconnected');
      toast.success('Boîte déconnectée.');
    } catch (err) {
      toast.error("La boîte n'a pas pu être déconnectée.");
    } finally {
      setDisconnecting(false);
    }
  };

  if (loading) {
    return (
      <div className="mx-auto flex max-w-3xl items-center gap-3 px-4 py-16 text-muted sm:px-6">
        <Spinner size={18} className="text-brand-600" />
        <span className="text-sm font-medium">Chargement de votre boîte</span>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-7 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50">
          <Mail size={22} className="text-brand-600" />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">
            Brancher une boîte
          </h1>
          <p className="text-sm text-muted">
            Mailsorter se connecte à votre boîte en IMAP, avec un mot de passe d'application.
          </p>
        </div>
      </div>

      {mailbox ? (
        <ConnectedMailbox
          mailbox={mailbox}
          onDisconnect={handleDisconnect}
          disconnecting={disconnecting}
        />
      ) : (
        <div className="grid gap-6 lg:grid-cols-[1.1fr_0.9fr]">
          <form onSubmit={handleConnect} className="card animate-fade-up p-7">
            <label htmlFor="mailbox-address" className="block text-sm font-bold text-ink-900">
              Adresse email
            </label>
            <input
              id="mailbox-address"
              type="email"
              autoComplete="email"
              className="input mt-2"
              placeholder="vous@exemple.fr"
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              disabled={connecting}
            />

            <label htmlFor="mailbox-password" className="mt-5 block text-sm font-bold text-ink-900">
              Mot de passe d'application
            </label>
            <input
              id="mailbox-password"
              type="password"
              // Not autoComplete="current-password": this is not the account
              // password, and offering that one is exactly the confusion that
              // makes the connection fail for a reason the user cannot see.
              autoComplete="new-password"
              className="input mt-2"
              placeholder="xxxx xxxx xxxx xxxx"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={connecting}
              aria-describedby="mailbox-password-help"
            />
            <p id="mailbox-password-help" className="mt-2 text-xs leading-relaxed text-muted">
              Ce n'est pas le mot de passe de votre compte. Un mot de passe d'application se crée
              dans les réglages de sécurité de votre fournisseur, ne donne accès qu'au courrier, et
              se révoque sans toucher au reste.
            </p>

            <button
              type="submit"
              disabled={connecting || !address.trim() || !password}
              className="btn-primary mt-6 w-full"
            >
              {connecting ? (
                <>
                  <Spinner size={18} /> Connexion
                </>
              ) : (
                <>
                  <Refresh size={18} /> Brancher cette boîte
                </>
              )}
            </button>

            <p className="mt-4 flex items-center justify-center gap-2 text-xs text-muted">
              <Shield size={14} /> La connexion est testée avant d'être enregistrée.
            </p>
          </form>

          {detected ? (
            <ProviderBriefing provider={detected} />
          ) : (
            <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
              <p className="text-sm leading-relaxed text-muted">
                Saisissez votre adresse : Mailsorter reconnaîtra votre fournisseur et vous dira ce
                qu'il faut savoir avant d'essayer.
              </p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export default Connect;
