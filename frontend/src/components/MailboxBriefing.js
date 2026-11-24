import React from 'react';
import { Alert, Lock } from '../ui/icons';
import { canConnectByPassword, imapLegend, imapRouteOf } from './landing/features';

// What a mailbox route means, in French, in ONE place.
//
// The login screen and the settings screen both ask for an address and an app
// password, and both have to warn about the same obstacles before the attempt.
// Two copies of BLOCKER_COPY would be two copies that drift, and a blocker with
// no sentence is silently dropped, which is worse than showing nothing: the
// blocker IS the reason the connection is about to fail.
//
// Nothing here lists providers. It renders what GET /api/providers returned
// and matches the typed domain against that same catalog, so a provider the
// server offers and the screen hides, or the reverse, cannot happen. The one
// thing keyed by provider is PROVIDER_TIPS below, which only adds a sentence
// to a provider the server returned.

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

// What to know before creating the password, for the providers whose catalog
// entry says something the user has to act on.
//
// The catalog's own note (Route.Note, sent as `note` by GET /api/providers) is
// written for whoever maintains the catalog: compliance, bandwidth, a pitch,
// half of it without accents. It is never shown. Its useful part is said again
// here, for the user, keyed by the provider's key. A key the server does not
// return is simply never read: this table does not make a provider appear.
const PROVIDER_TIPS = {
  orange:
    "Chez Orange, ce mot de passe s'appelle une « clé d'accès ». Créez-la dans l'espace sécurité de votre compte Orange : ce n'est pas le mot de passe du compte, et c'est elle qu'il faut saisir ici.",
  icloud:
    "Apple n'affiche ce mot de passe qu'une seule fois, au moment où vous le créez : gardez la page ouverte et copiez-le tout de suite.",
  yahoo: 'Chez Yahoo, ce mot de passe se crée dans la section des connexions externes de votre compte.',
  proton:
    'Proton passe par Proton Mail Bridge, installé sur la machine où tourne Mailsorter. Saisissez le mot de passe que Bridge affiche pour cette adresse, pas celui de votre compte Proton.',
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
      ? "Cette messagerie passe par l'API de Microsoft, que Mailsorter n'utilise pas encore. Elle ne peut pas être branchée ici."
      : 'Cette messagerie ne se branche pas encore ici.';
  }
  return 'Mailsorter ne connaît pas le serveur de cette messagerie et ne sait pas encore le trouver tout seul. Elle ne peut donc pas être branchée ici.';
}

// The briefing for the address being typed in the address-and-password form,
// so it reads the provider's IMAP route, the one that form goes through, plus
// what to know before creating the password (PROVIDER_TIPS).
//
// `googleOn` is the instance holding Google credentials, so the "Connecter avec
// Google" button sits above the form. An address on a company's own domain may
// be a Google Workspace mailbox, which the server cannot tell from the domain:
// it lands on the generic entry, which cannot connect it, while that button
// can. The generic entry says so.
function ProviderBriefing({ provider, googleOn = false }) {
  if (!provider) return null;

  if (!canConnectByPassword(provider)) {
    return (
      <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
        <div className="flex flex-wrap items-center gap-2">
          <span className="chip bg-brand-50 text-brand-700">{provider.name}</span>
          <span className="chip bg-ink-100 text-ink-600">Bientôt</span>
        </div>
        <p className="mt-3 text-sm leading-relaxed text-ink-600">{notYetCopy(provider)}</p>
        {googleOn && provider.key === 'imap' && (
          <p className="mt-3 text-sm leading-relaxed text-ink-600">
            S'il s'agit d'une adresse Google Workspace (Gmail au nom de votre organisation), branchez-la avec le
            bouton « Connecter avec Google » en haut de la page.
          </p>
        )}
      </div>
    );
  }

  const route = imapRouteOf(provider);
  const blockers = (route.blockers || []).filter((b) => BLOCKER_COPY[b]);
  const tip = PROVIDER_TIPS[provider.key];

  return (
    <div className="animate-fade-up rounded-2xl border border-hairline/70 bg-surface/60 p-6">
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip bg-brand-50 text-brand-700">{provider.name}</span>
        <span className="font-mono text-xs text-muted">
          {route.imap.host}:{route.imap.port}
        </span>
      </div>

      {tip && (
        <p className="mt-4 flex gap-2.5">
          <Lock size={15} className="mt-0.5 shrink-0 text-brand-600" />
          <span className="text-xs leading-relaxed text-ink-700">{tip}</span>
        </p>
      )}

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

export { BLOCKER_COPY, PROVIDER_TIPS, normalizeDomain, detectProvider, ProviderBriefing };
