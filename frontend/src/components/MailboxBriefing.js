import React from 'react';
import { Alert } from '../ui/icons';

// What a mailbox route means, in French, in ONE place.
//
// The login screen and the settings screen both ask for an address and an app
// password, and both have to warn about the same obstacles before the attempt.
// Two copies of BLOCKER_COPY would be two copies that drift, and a blocker with
// no sentence is silently dropped, which is worse than showing nothing: the
// blocker IS the reason the connection is about to fail.
//
// Nothing here knows a provider. It renders what GET /api/providers returned
// and matches the typed domain against that same catalog, so a provider the
// server offers and the screen hides, or the reverse, cannot happen.

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

export { BLOCKER_COPY, CAP_COPY, normalizeDomain, detectProvider, ProviderBriefing };
