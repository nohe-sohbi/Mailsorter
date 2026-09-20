import React, { useEffect, useMemo, useState } from 'react';
import { configService, mailboxService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { track } from '../lib/analytics';
import Spinner from '../ui/Spinner';
import { Mail, Shield, Check, Alert, Refresh, Trash } from '../ui/icons';

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

// BLOCKER_COPY translates the catalog's blocker vocabulary. These are
// enumeration values, not providers: writing them out here does not recreate
// the list the server owns.
//
// Showing them BEFORE the attempt is the whole point. Every one of these
// otherwise surfaces as "credentials refused", and the user goes off to
// regenerate a password that was working fine.
const BLOCKER_COPY = {
  'two-factor-required':
    "La validation en deux étapes doit être activée sur votre compte avant de pouvoir créer un mot de passe d'application.",
  'advanced-protection':
    "Un compte inscrit au programme Protection Avancée ne peut pas créer de mot de passe d'application. Une double authentification par clé de sécurité seule produit le même blocage.",
  'admin-policy':
    "L'administrateur de votre organisation peut avoir désactivé cet accès. Dans ce cas rien de ce que vous ferez ici ne débloquera la situation.",
  'admin-consent': "Un administrateur doit approuver l'application pour votre organisation avant votre première connexion.",
  'paid-plan-required': "Ce fournisseur réserve l'accès IMAP à ses offres payantes.",
  'local-only': "Ce fournisseur n'est joignable que depuis la machine où vous faites tourner Mailsorter.",
  'own-cloud-project': "Ce chemin demande votre propre projet Google Cloud.",
  'datacenter-ip':
    "Ce fournisseur note les connexions selon leur origine et refuse parfois celles qui viennent d'un hébergeur. Si la connexion échoue sans raison visible, c'est la première piste.",
};

// CAP_COPY names what a route can do, so the user learns what they give up
// before connecting rather than discovering it in use.
const CAP_COPY = {
  labels: 'Étiquettes',
  providerSearch: 'Recherche côté serveur',
  threads: 'Conversations',
  send: 'Envoi',
};

function normalizeDomain(address) {
  const at = address.lastIndexOf('@');
  if (at < 0 || at === address.length - 1) return '';
  return address.slice(at + 1).trim().toLowerCase();
}

// detectProvider does client-side what provider.Detect does server-side, over
// the same catalog. It is an aid while typing, not a decision: the server
// resolves again on connect and its answer is the one that counts.
function detectProvider(providers, address) {
  const domain = normalizeDomain(address);
  if (!domain) return null;
  const match = providers.find((p) => (p.domains || []).includes(domain));
  if (match) return match;
  // The catalog always ends with a generic IMAP entry, which is a valid answer
  // rather than a failure.
  return providers.find((p) => !(p.domains || []).length) || null;
}

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

function ProviderBriefing({ provider }) {
  const route = provider?.routes?.[0];
  if (!route) return null;

  const blockers = (route.blockers || []).filter((b) => BLOCKER_COPY[b]);
  const caps = Object.entries(CAP_COPY).filter(([key]) => route.capabilities?.[key]);
  const missing = Object.entries(CAP_COPY).filter(([key]) => !route.capabilities?.[key]);

  return (
    <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
      <div className="flex items-center gap-2">
        <span className="chip bg-brand-50 text-brand-700">{provider.name}</span>
        {route.autodiscover ? (
          <span className="text-xs text-muted">Réglages détectés à la connexion</span>
        ) : (
          route.imap && (
            <span className="font-mono text-xs text-muted">
              {route.imap.host}:{route.imap.port}
            </span>
          )
        )}
      </div>

      {route.note && <p className="mt-3 text-sm leading-relaxed text-ink-600">{route.note}</p>}

      {blockers.length > 0 && (
        <ul className="mt-4 space-y-2.5">
          {blockers.map((b) => (
            <li key={b} className="flex gap-2.5">
              <Alert size={15} className="mt-0.5 shrink-0 text-caution-600" />
              <span className="text-xs leading-relaxed text-ink-600">{BLOCKER_COPY[b]}</span>
            </li>
          ))}
        </ul>
      )}

      {caps.length > 0 && (
        <div className="mt-4 border-t border-hairline/70 pt-4">
          <p className="text-xs font-semibold uppercase tracking-wider text-ink-500">Disponible</p>
          <ul className="mt-2 flex flex-wrap gap-1.5">
            {caps.map(([key, label]) => (
              <li key={key} className="chip bg-positive-50 text-positive-700">
                {label}
              </li>
            ))}
          </ul>
          {missing.length > 0 && (
            <p className="mt-3 text-xs leading-relaxed text-muted">
              Pas sur cette boîte : {missing.map(([, label]) => label.toLowerCase()).join(', ')}.
            </p>
          )}
        </div>
      )}
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
