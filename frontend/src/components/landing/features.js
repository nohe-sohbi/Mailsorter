// What the landing may promise, in one place.
//
// GMAIL_ONLY lists the features still reachable only through the Gmail API:
// over IMAP they answer 501 (see gmailClientFor in backend/internal/api), or,
// for labels, have no equivalent at all (a message sits in one folder, see
// internal/mailbox/imap.go), so the landing must not promise them to someone
// who can only connect a mailbox. Porting a feature to IMAP is deleting its
// line here: the corridor badges, the key-board legend, the hall, the demo and
// the "Outlook" answer all follow.
// Each label carries its article, because it is used inside sentences.
export const GMAIL_ONLY = {
  label: 'le classement',
  undo: "l'annulation",
  rules: 'les règles',
  snooze: 'le report',
  unsubscribe: 'le désabonnement',
  digest: 'le récap',
};

export function isGmailOnly(feature) {
  return Object.prototype.hasOwnProperty.call(GMAIL_ONLY, feature);
}

// Without Google credentials nobody on this instance can reach the Gmail API,
// so a Gmail-only feature is a promise nobody here could keep.
export function canPromise(feature, isConfigured) {
  return Boolean(isConfigured) || !isGmailOnly(feature);
}

// The transports this server can actually connect a mailbox over. The catalog
// (GET /api/providers) also lists providers reached another way: Outlook.com
// and Microsoft 365 route through Microsoft Graph, which no code implements
// yet, so connecting one fails. Porting Graph is adding 'graph' here.
export const IMPLEMENTED_TRANSPORTS = ['imap', 'gmail-api'];

// Whether a provider of GET /api/providers ({ key, name, routes: [{ transport }] })
// can be connected on this instance: over IMAP always, over the Gmail API only
// when the instance holds Google credentials.
export function canConnect(provider, isConfigured) {
  return ((provider && provider.routes) || []).some(
    (r) => IMPLEMENTED_TRANSPORTS.includes(r.transport) && (r.transport !== 'gmail-api' || Boolean(isConfigured))
  );
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
