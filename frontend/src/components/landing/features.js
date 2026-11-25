// What the landing may promise, in one place.
//
// GMAIL_ONLY lists the features still reachable only through the Gmail API:
// over IMAP they answer 501 (see gmailClientFor in backend/internal/api), so
// the landing must not promise them to someone who can only connect a
// mailbox. Porting a feature to IMAP is deleting its line here: the corridor
// badges, the key-board legend, the hall, the demo and the "Outlook" answer
// all follow. Classifying left it: over IMAP the AI's proposal, a selection
// classified by hand and a sender's auto-pilot file the mail in the folder of
// that name (internal/mailbox/imap.go). The rules' "Étiqueter" still goes
// through the Gmail API: that screen keeps it closed on `fullFeatures`, not
// on this list.
// Each label carries its article, because it is used inside sentences.
export const GMAIL_ONLY = {
  rules: 'les règles',
  snooze: 'le report',
};

export function isGmailOnly(feature) {
  return Object.prototype.hasOwnProperty.call(GMAIL_ONLY, feature);
}

// Without Google credentials nobody on this instance can reach the Gmail API,
// so a Gmail-only feature is a promise nobody here could keep.
export function canPromise(feature, isConfigured) {
  return Boolean(isConfigured) || !isGmailOnly(feature);
}

// Whether one route of GET /api/providers
// ({ transport, autodiscover, imap: { host, port } | null }) can be connected
// on this instance. The single rule every list of providers follows:
// - an IMAP route only when the catalog gives its server. A route left to
//   autodiscovery cannot connect: autodiscovery is not written yet, and the
//   server refuses the address (422);
// - a Gmail API route only when the instance holds Google credentials;
// - anything else (Microsoft Graph, for Outlook.com and Microsoft 365) is not
//   implemented, so connecting one fails.
export function canConnectRoute(route, isConfigured) {
  if (!route) return false;
  if (route.transport === 'imap') return Boolean(route.imap && route.imap.host);
  if (route.transport === 'gmail-api') return Boolean(isConfigured);
  return false;
}

// Whether a provider of GET /api/providers ({ key, name, routes }) can be
// connected on this instance by at least one of its routes.
export function canConnect(provider, isConfigured) {
  return ((provider && provider.routes) || []).some(
    (r) => canConnectRoute(r, isConfigured) && (r.transport !== 'imap' || isRecognizable(provider))
  );
}

// The server finds a mailbox's provider from the address's domain alone
// (provider.Detect). A provider with no domain of its own, Google Workspace
// on a company domain, is never recognized: its address falls back to the
// generic entry, which has no server yet. So its IMAP route cannot be reached
// with a password, whatever the catalog says about the route itself.
function isRecognizable(provider) {
  return Boolean(provider && provider.domains && provider.domains.length > 0);
}

// The provider's IMAP route, the one an address and an app password go
// through, or null.
export function imapRouteOf(provider) {
  return ((provider && provider.routes) || []).find((r) => r.transport === 'imap') || null;
}

// Whether the address-and-password form can connect this provider: its IMAP
// route, with its server known.
export function canConnectByPassword(provider) {
  return isRecognizable(provider) && canConnectRoute(imapRouteOf(provider), false);
}

// Whether a verdict of the AI can be applied now. A label files the mail in a
// folder over IMAP, out of the inbox, and adds a label over the Gmail API: the
// screen says which ("Classer" or "Étiqueter") and offers "Annuler" after it
// accordingly. Until the mailbox is known (`known` in useMailbox: still
// loading, or the request failed), a label verdict waits, "Indisponible pour
// le moment", rather than be worded and applied on a guess.
export function canApplyVerdict(action, mailboxKnown) {
  return action !== 'label' || Boolean(mailboxKnown);
}

// What the model reads and where it goes, in the privacy vault's words, for
// every screen that sums it up in a line. Checked against the code: the prompt
// carries the sender, the subject and a snippet cut at 200 characters
// (backend/internal/ai/prompts.go), sent to whichever provider the instance
// runs, which can be a model on the same machine.
export const AI_READS = "L'IA ne voit que l'expéditeur, l'objet et au plus 200 caractères du message.";
// Where that excerpt goes. An instance run at home may use a local model; the
// hosted service uses Mistral AI (its deployment forwards MISTRAL_* only), or
// the provider of a key the user plugged in.
export function aiEgress(selfHosted) {
  return selfHosted
    ? "Avec un modèle local, rien ne sort de l'instance. Avec une IA en ligne, cet extrait part chez son fournisseur."
    : "Cet extrait part chez Mistral AI, l'IA du service en ligne, ou chez le fournisseur dont vous avez branché votre propre clé.";
}

// "a", "a et b", "a, b et c".
export function joinFr(items) {
  if (items.length <= 1) return items.join('');
  return `${items.slice(0, -1).join(', ')} et ${items[items.length - 1]}`;
}

const capitalize = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

// The legend of the pink keys: what a mailbox connected over IMAP gets today.
export function imapLegend() {
  const missing = Object.values(GMAIL_ONLY);
  if (!missing.length) return 'Toutes les fonctions.';
  return `Lecture et tri par IA. ${capitalize(joinFr(missing))} ${missing.length > 1 ? 'arrivent' : 'arrive'}.`;
}

// The answer to "Ça marche avec Outlook ?" when Outlook cannot be connected
// here, which is also the answer while the catalog is loading or failed to:
// a "Oui" the connect screen then refuses is worse than a "pas encore".
export const OUTLOOK_NOT_YET =
  "Pas encore. Outlook et Microsoft 365 passent par l'API de Microsoft, que Mailsorter n'utilise pas encore. Ça arrive.";

// The answer to "Ça marche avec Outlook ?" once Outlook can be connected.
export function outlookAnswer() {
  const missing = Object.values(GMAIL_ONLY);
  if (!missing.length) return 'Oui, avec toutes les fonctions.';
  const verb = missing.length > 1 ? "sont pour l'instant réservés" : "est pour l'instant réservé";
  return `Oui pour lire vos e-mails, les faire trier par l'IA et agir dessus. ${capitalize(joinFr(missing))} ${verb} à Gmail. Ça arrive.`;
}
