import { Archive, Trash, Tag, Pin, Mail, Undo, Clock, BellOff, Star } from './icons';

// The single source of truth for how a triage action looks and reads.
//
// This vocabulary used to be redefined in five files (Inbox, Rules, History,
// Pricing, Account) with drifting keys and colours: the same "Archivé" badge was
// sky-600 in one screen and sky-700 in another, and Pricing rendered blank chips
// for `read`/`star` because its table simply had no entry for them. One table
// with a real fallback removes both classes of bug.
//
// Keys are the canonical backend vocabulary. Synonyms the API also emits
// (`trash`, `markRead`) are folded onto their canonical key by `actionMeta`.
export const ACTIONS = {
  archive: {
    key: 'archive',
    label: 'Archiver',
    past: 'Archivé',
    pastPlural: 'Archivés',
    Icon: Archive,
    chip: 'bg-info-50 text-info-700',
    solid: 'bg-info-fill',
    ring: 'rgb(var(--info-500))',
    destructive: false,
  },
  delete: {
    key: 'delete',
    label: 'Supprimer',
    past: 'Supprimé',
    pastPlural: 'Supprimés',
    Icon: Trash,
    chip: 'bg-danger-50 text-danger-700',
    solid: 'bg-danger-fill',
    ring: 'rgb(var(--danger-500))',
    destructive: true,
  },
  label: {
    key: 'label',
    label: 'Étiqueter',
    past: 'Étiqueté',
    pastPlural: 'Étiquetés',
    Icon: Tag,
    chip: 'bg-caution-50 text-caution-700',
    solid: 'bg-caution-fill',
    ring: 'rgb(var(--caution-500))',
    destructive: false,
  },
  keep: {
    key: 'keep',
    label: 'Garder',
    past: 'Gardé',
    pastPlural: 'Gardés',
    Icon: Pin,
    chip: 'bg-positive-50 text-positive-700',
    solid: 'bg-positive-fill',
    ring: 'rgb(var(--positive-500))',
    destructive: false,
  },
  read: {
    key: 'read',
    label: 'Marquer comme lu',
    past: 'Lu',
    pastPlural: 'Lus',
    Icon: Mail,
    chip: 'bg-ink-100 text-ink-700',
    solid: 'bg-ink-500',
    ring: 'rgb(var(--ink-400))',
    destructive: false,
  },
  unread: {
    key: 'unread',
    label: 'Marquer comme non lu',
    past: 'Marqué non lu',
    pastPlural: 'Marqués non lus',
    Icon: Mail,
    chip: 'bg-ink-100 text-ink-700',
    solid: 'bg-ink-500',
    ring: 'rgb(var(--ink-400))',
    destructive: false,
  },
  star: {
    key: 'star',
    label: 'Mettre en favori',
    past: 'Favori',
    pastPlural: 'Favoris',
    Icon: Star,
    chip: 'bg-caution-50 text-caution-700',
    solid: 'bg-caution-fill',
    ring: 'rgb(var(--caution-500))',
    destructive: false,
  },
  unstar: {
    key: 'unstar',
    label: 'Retirer des favoris',
    past: 'Retiré des favoris',
    pastPlural: 'Retirés des favoris',
    Icon: Star,
    chip: 'bg-ink-100 text-ink-700',
    solid: 'bg-ink-500',
    ring: 'rgb(var(--ink-400))',
    destructive: false,
  },
  snooze: {
    key: 'snooze',
    label: 'Reporter',
    past: 'Reporté',
    pastPlural: 'Reportés',
    Icon: Clock,
    chip: 'bg-brand-50 text-brand-700',
    solid: 'bg-brand-fill',
    ring: 'rgb(var(--brand-500))',
    destructive: false,
  },
  unsubscribe: {
    key: 'unsubscribe',
    label: 'Se désabonner',
    past: 'Désabonné',
    pastPlural: 'Désabonnés',
    Icon: BellOff,
    chip: 'bg-caution-50 text-caution-700',
    solid: 'bg-caution-fill',
    ring: 'rgb(var(--caution-500))',
    destructive: false,
  },
  // Reversals, logged by the ledger when the user undoes something.
  unarchive: {
    key: 'unarchive',
    label: 'Désarchiver',
    past: 'Désarchivé',
    pastPlural: 'Désarchivés',
    Icon: Undo,
    chip: 'bg-positive-50 text-positive-700',
    solid: 'bg-positive-fill',
    ring: 'rgb(var(--positive-500))',
    destructive: false,
  },
  untrash: {
    key: 'untrash',
    label: 'Restaurer',
    past: 'Restauré',
    pastPlural: 'Restaurés',
    Icon: Undo,
    chip: 'bg-positive-50 text-positive-700',
    solid: 'bg-positive-fill',
    ring: 'rgb(var(--positive-500))',
    destructive: false,
  },
};

// Synonyms emitted by different parts of the API for the same act.
const ALIASES = {
  trash: 'delete',
  markRead: 'read',
  markUnread: 'unread',
};

// A neutral entry so an action key nobody anticipated renders as itself rather
// than as an uncoloured chip with an empty label.
const unknownAction = (raw) => ({
  key: raw || 'unknown',
  label: raw || 'Action',
  past: raw || 'Action',
  Icon: Mail,
  chip: 'bg-ink-100 text-ink-700',
  solid: 'bg-ink-500',
  ring: 'rgb(var(--ink-400))',
  destructive: false,
  unknown: true,
});

export const canonicalAction = (raw) => ALIASES[raw] || raw;

export function actionMeta(raw) {
  return ACTIONS[canonicalAction(raw)] || unknownAction(raw);
}

// Actions the user can trigger over a selection, in the order they appear in the
// bulk bar. `delete` is last and separated, so the destructive one is never the
// button next to the cursor's resting place.
export const BULK_ACTIONS = ['archive', 'read', 'star', 'label', 'delete'];

export const isDestructive = (raw) => actionMeta(raw).destructive === true;

// French agreement, which the toasts were getting wrong: "12 emails archivé".
// A past participle used as an attribute agrees in number, and 0 and 1 both take
// the singular.
export const plural = (n) => (n > 1 ? 's' : '');

export function pastParticiple(raw, count = 1) {
  const meta = actionMeta(raw);
  return count > 1 ? meta.pastPlural || meta.past : meta.past;
}
