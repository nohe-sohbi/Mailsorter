// What the landing may promise, in one place.
//
// GMAIL_ONLY lists the features still reachable only through the Gmail API:
// over IMAP they answer 501 (see gmailClientFor in backend/internal/api), so the
// landing must not promise them to someone who can only connect a mailbox.
// Porting a feature to IMAP is deleting its line here: the corridor badges, the
// key-board legend, the hall, the demo and the "Outlook" answer all follow.
// Each label carries its article, because it is used inside sentences.
export const GMAIL_ONLY = {
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

// The answer to "Ça marche avec Outlook ?".
export function outlookAnswer() {
  const missing = Object.values(GMAIL_ONLY);
  if (!missing.length) return 'Oui, avec toutes les fonctions.';
  const verb = missing.length > 1 ? "sont pour l'instant réservés" : "est pour l'instant réservé";
  return `Oui pour lire vos e-mails, les faire trier par l'IA et agir dessus. ${capitalize(joinFr(missing))} ${verb} à Gmail. Ça arrive.`;
}
