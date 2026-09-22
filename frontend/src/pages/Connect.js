import React, { useEffect, useMemo, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { mailboxService, configService, authService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { useInstance } from '../contexts/InstanceContext';
import { track } from '../lib/analytics';
import { detectProvider, ProviderBriefing } from '../components/MailboxBriefing';
import { Shield, Refresh, Trash, Check, Google, Sparkles, Inbox } from '../ui/icons';
import Spinner from '../ui/Spinner';

function ConnectedMailbox({ mailbox, onDisconnect, disconnecting }) {
  const isGmail = mailbox.transport === 'gmail' || mailbox.provider === 'google';

  return (
    <div className="card animate-fade-up p-8">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-positive-50 text-positive-600">
            <Check size={20} />
          </span>
          <div>
            <h2 className="text-lg font-bold text-ink-900">
              {isGmail ? 'Boîte Gmail connectée' : 'Boîte mail connectée (IMAP)'}
            </h2>
            <p className="text-xs text-muted">
              {isGmail ? 'Authentifiée via Google OAuth sécurisé' : 'Reconnue et joignable'}
            </p>
          </div>
        </div>
        <span className="chip bg-positive-50 text-positive-700">Active</span>
      </div>

      <dl className="mt-6 grid gap-3 sm:grid-cols-2">
        <div className="rounded-xl border border-hairline/70 bg-ink-50/60 px-4 py-3">
          <dt className="text-xs font-bold uppercase tracking-wider text-ink-500">Adresse de la boîte</dt>
          <dd className="mt-1 break-all font-mono text-sm text-ink-800">{mailbox.username}</dd>
        </div>
        <div className="rounded-xl border border-hairline/70 bg-ink-50/60 px-4 py-3">
          <dt className="text-xs font-bold uppercase tracking-wider text-ink-500">Protocole de tri</dt>
          <dd className="mt-1 break-all font-mono text-sm text-ink-800">
            {isGmail ? 'Google API (OAuth 2.0)' : `${mailbox.host}:${mailbox.port} (IMAP)`}
          </dd>
        </div>
      </dl>

      <div className="mt-6 flex flex-wrap items-center gap-3">
        <Link to="/inbox" className="btn-primary">
          <Inbox size={16} /> Accéder à ma boîte
        </Link>
        <button onClick={onDisconnect} disabled={disconnecting} className="btn-danger">
          {disconnecting ? <Spinner size={16} /> : <Trash size={16} />} Déconnecter cette boîte
        </button>
      </div>

      <p className="mt-3 text-xs leading-relaxed text-muted">
        Déconnecter efface l'accès de Mailsorter à cette boîte sans toucher à vos emails ni à votre compte Mailsorter.
      </p>
    </div>
  );
}

function Connect() {
  const toast = useToast();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const { isConfigured } = useInstance();

  const [loading, setLoading] = useState(true);
  const [mailbox, setMailbox] = useState(null);
  const [providers, setProviders] = useState([]);
  const [address, setAddress] = useState('');
  const [password, setPassword] = useState('');
  const [connecting, setConnecting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [googleBusy, setGoogleBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    Promise.allSettled([mailboxService.get(), configService.getProviders()]).then(([box, cat]) => {
      if (cancelled) return;
      if (box.status === 'fulfilled') {
        const mb = box.value.data.mailbox || null;
        setMailbox(mb);
        if (mb) {
          localStorage.setItem('hasMailbox', 'true');
        } else {
          localStorage.removeItem('hasMailbox');
        }
      }
      if (cat.status === 'fulfilled') setProviders(cat.value.data.providers || []);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const detected = useMemo(() => detectProvider(providers, address), [providers, address]);

  const handleConnectGmail = async () => {
    setGoogleBusy(true);
    try {
      const response = await authService.getAuthUrl();
      track('connect_gmail_start');
      window.location.href = response.data.authUrl;
    } catch (err) {
      toast.error("Impossible d'initialiser la connexion Google. Réessayez.");
      setGoogleBusy(false);
    }
  };

  const handleConnectIMAP = async (e) => {
    e.preventDefault();
    if (!address.trim() || !password) return;
    setConnecting(true);
    try {
      const { data } = await mailboxService.connect(address.trim(), password);
      setMailbox(data);
      localStorage.setItem('hasMailbox', 'true');
      setPassword('');
      setAddress('');
      track('mailbox_connected', { provider: data.provider });
      toast.success('Boîte branchée ! Redirection vers votre boîte...');
      setTimeout(() => navigate('/inbox'), 800);
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
        "L'accès de Mailsorter à cette boîte sera retiré. Vos règles, protections et historique Mailsorter restent conservés.",
      confirmLabel: 'Déconnecter',
      danger: true,
    });
    if (!confirmed) return;

    setDisconnecting(true);
    try {
      await mailboxService.disconnect();
      setMailbox(null);
      localStorage.removeItem('hasMailbox');
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
        <span className="text-sm font-medium">Chargement de votre configuration...</span>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-4xl px-4 py-10 sm:px-6">
      <div className="mb-8">
        <span className="chip mb-3 bg-brand-50 text-brand-700">
          <Sparkles size={14} /> Étape 2 sur 2
        </span>
        <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900 sm:text-3xl">
          Définir la boîte mail à ranger
        </h1>
        <p className="mt-2 max-w-2xl text-sm leading-relaxed text-muted">
          Choisissez l'adresse email sur laquelle Mailsorter doit opérer le tri. Vos emails restent hébergés chez votre fournisseur, Mailsorter ne fait que les lire et les ranger selon vos règles.
        </p>
      </div>

      {mailbox ? (
        <ConnectedMailbox
          mailbox={mailbox}
          onDisconnect={handleDisconnect}
          disconnecting={disconnecting}
        />
      ) : (
        <div className="space-y-6">
          {/* Option 1: Gmail (si configuré) */}
          {isConfigured && (
            <div className="card animate-fade-up p-7 transition-shadow hover:shadow-card">
              <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex items-start gap-3.5">
                  <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-ink-100 text-ink-700">
                    <Google size={24} />
                  </span>
                  <div>
                    <h2 className="text-base font-bold text-ink-900">
                      Connecter une boîte Gmail
                    </h2>
                    <p className="mt-1 text-xs leading-relaxed text-ink-600">
                      Connexion OAuth 2.0 sécurisée par Google. Aucun mot de passe stocké, autorisation révocable à tout moment.
                    </p>
                  </div>
                </div>

                <button
                  type="button"
                  onClick={handleConnectGmail}
                  disabled={googleBusy || connecting}
                  className="btn-primary shrink-0 px-5 py-3 text-sm"
                >
                  {googleBusy ? (
                    <>
                      <Spinner size={18} className="text-white" /> Connexion...
                    </>
                  ) : (
                    <>
                      <Google size={18} /> Connecter avec Google
                    </>
                  )}
                </button>
              </div>
            </div>
          )}

          {isConfigured && (
            <div className="flex items-center gap-4">
              <span className="h-px flex-1 bg-hairline" />
              <span className="text-xs font-semibold uppercase tracking-wider text-ink-400">
                ou toute autre messagerie
              </span>
              <span className="h-px flex-1 bg-hairline" />
            </div>
          )}

          {/* Option 2: Boîte IMAP avec mot de passe d'application */}
          <div className="grid gap-6 lg:grid-cols-[1.1fr_0.9fr]">
            <form onSubmit={handleConnectIMAP} className="card animate-fade-up p-7">
              <div className="mb-4">
                <h2 className="text-base font-bold text-ink-900">
                  Connecter une boîte IMAP
                </h2>
                <p className="mt-1 text-xs text-muted">
                  Orange, Outlook, Yahoo, iCloud, Free, messagerie professionnelle...
                </p>
              </div>

              <label htmlFor="mailbox-address" className="block text-xs font-bold text-ink-700">
                Adresse email de la boîte à ranger
              </label>
              <input
                id="mailbox-address"
                type="email"
                required
                autoComplete="email"
                className="input mt-1.5 w-full text-sm"
                placeholder="vous@orange.fr ou vous@outlook.com"
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                disabled={connecting}
              />

              <label htmlFor="mailbox-password" className="mt-4 block text-xs font-bold text-ink-700">
                Mot de passe d'application
              </label>
              <input
                id="mailbox-password"
                type="password"
                required
                autoComplete="new-password"
                className="input mt-1.5 w-full text-sm"
                placeholder="xxxx xxxx xxxx xxxx"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={connecting}
                aria-describedby="mailbox-password-help"
              />
              <p id="mailbox-password-help" className="mt-2 text-xs leading-relaxed text-muted">
                Ce n'est pas le mot de passe habituel de votre messagerie. C'est un code spécifique généré dans l'espace sécurité de votre fournisseur, révocable sans affecter votre boîte.
              </p>

              <button
                type="submit"
                disabled={connecting || !address.trim() || !password}
                className="btn-primary mt-6 w-full py-3 text-sm"
              >
                {connecting ? (
                  <>
                    <Spinner size={18} /> Test et connexion en cours...
                  </>
                ) : (
                  <>
                    <Refresh size={18} /> Brancher cette boîte (IMAP)
                  </>
                )}
              </button>

              <p className="mt-4 flex items-center justify-center gap-2 text-xs text-muted">
                <Shield size={14} /> La connexion est vérifiée en temps réel avant d'être scellée (AES-256).
              </p>
            </form>

            {detected ? (
              <ProviderBriefing provider={detected} />
            ) : (
              <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
                <span className="chip mb-3 bg-brand-50 text-brand-700">Aide fournisseur</span>
                <h3 className="text-sm font-bold text-ink-900">Assistance en direct</h3>
                <p className="mt-2 text-xs leading-relaxed text-muted">
                  Saisissez l'adresse de votre messagerie : Mailsorter identifiera automatiquement votre opérateur (Orange, Microsoft, Yahoo, etc.) et vous indiquera la marche à suivre pour obtenir votre mot de passe d'application en 1 minute.
                </p>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default Connect;
