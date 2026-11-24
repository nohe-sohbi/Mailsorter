import React from 'react';
import { Alert } from '../ui/icons';
import { canConnectByPassword, imapLegend, imapRouteOf } from './landing/features';

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
  // An unknown domain falls back to the catalog's generic IMAP entry, exactly
  // as the server does. Not to "the first entry without domains": several
  // providers list none (their addresses are on the customer's own domain),
  // and that guess showed the Google Workspace card for any unknown address.
  return providers.find((p) => p.key === 'imap') || null;
}

// Why a provider cannot be connected with an address and a password here, in
// one sentence.
function notYetCopy(provider) {
  const route = imapRouteOf(provider);
  if (!route) {
    const viaMicrosoft = (provider.routes || []).some((r) => r.transport === 'graph');
    return viaMicrosoft
      ? "Cette messagerie passe par l'API de Microsoft, que Mailsorter n'utilise pas encore. Elle ne se branche pas encore ici."
      : "Cette messagerie ne se branche pas encore ici.";
  }
  return "Mailsorter ne connaît pas encore le serveur de cette messagerie, et ne sait pas encore le trouver seul : elle ne se branche pas encore ici.";
}

// The briefing for the address being typed in the address-and-password form,
// so it reads the provider's IMAP route, the one that form goes through. The
// catalog's notes are not shown: they are written for whoever maintains it.
function ProviderBriefing({ provider }) {
  if (!provider) return null;

  if (!canConnectByPassword(provider)) {
    return (
      <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
        <div className="flex flex-wrap items-center gap-2">
          <span className="chip bg-brand-50 text-brand-700">{provider.name}</span>
          <span className="chip bg-ink-100 text-ink-600">Bientôt</span>
        </div>
        <p className="mt-3 text-sm leading-relaxed text-ink-600">{notYetCopy(provider)}</p>
      </div>
    );
  }

  const route = imapRouteOf(provider);
  const blockers = (route.blockers || []).filter((b) => BLOCKER_COPY[b]);

  return (
    <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip bg-brand-50 text-brand-700">{provider.name}</span>
        <span className="font-mono text-xs text-muted">
          {route.imap.host}:{route.imap.port}
        </span>
      </div>

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

      {/* What a mailbox connected this way gets today, from the same list the
          landing reads (GMAIL_ONLY), not from the route's capabilities: those
          describe the provider, and Mailsorter does not use most of them yet. */}
      <div className="mt-4 border-t border-hairline/70 pt-4">
        <p className="text-xs font-semibold uppercase tracking-wider text-ink-500">Sur cette boîte</p>
        <p className="mt-2 text-xs leading-relaxed text-ink-600">{imapLegend()}</p>
      </div>
    </div>
  );
}

export { BLOCKER_COPY, normalizeDomain, detectProvider, ProviderBriefing };
