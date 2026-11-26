import { canonicalAction } from '../../ui/actions';

// Turns the flat list of pending AI verdicts into the plan the inbox shows:
// one decision per sender and verdict, not one per email.
//
// The model answers email by email, and the panel used to render exactly that.
// Eight notifications from one sender came out as eight identical rows, each
// asking the same question, and a hundred verdicts buried the inbox under a
// hundred rows. People triage by sender ("Medium, archive it"), so that is the
// unit of decision here. No React and no I/O, so the rules stay readable in one
// place.

// At and above this, a verdict is "sure". Below it, its group is flagged "À
// vérifier" and the bulk button leaves it out: the shortcut is for what the
// model is confident about, and the rest gets a look first. Same threshold as
// the "Haute confiance" filter this replaces.
export const SURE_CONFIDENCE = 0.8;

// Verdicts that move mail. `keep` is a verdict too, but applying it changes
// nothing in the mailbox, so it is not a task: it gets its own block, worded as
// such, instead of a row that asks to "apply" doing nothing.
const MOVES = ['delete', 'archive', 'label'];

export const idOf = (s) => s.id || s._id;

// The sender's address of a suggestion, lowercased: the one the server read
// out of From (`senderAddress`, protect.Address), never one this screen reads
// itself. The display name is the sender's to choose and may hold someone
// else's address ("<boss@corp.com>" <spam@evil.io>), and a reading of its own
// here diverged from the server's (`<vrai@x.fr> (<boss@corp.com>)`): it
// grouped a spam with the boss's mail and made a rule against the boss. It
// keys the group, and it is what a sender rule matches. "" when the server
// gave none (a From that does not read): the group is then the mail alone,
// and offers no rule.
export function senderAddress(s = {}) {
  return (s.senderAddress || '').trim().toLowerCase();
}

// What the plan shows for the sender: the server's reading (`senderLabel`,
// protect.Label), never the From as written nor a cut of it: its display name
// is the sender's to write, and may hold another address.
function senderName(s = {}) {
  return s.senderLabel || 'Expéditeur illisible';
}

export function isSure(confidence) {
  return (Number(confidence) || 0) >= SURE_CONFIDENCE;
}

const groupKey = (action, labelName, address) =>
  `${action}|${action === 'label' ? (labelName || '').trim().toLowerCase() : ''}|${address}`;

// groupSuggestions splits pending verdicts into the two blocks of the plan.
//
//   moves: archive / delete / label, one group per (verdict, label, sender)
//   keeps: keep, one group per sender
//
// Each group carries its items newest first (the order GetSuggestions returns),
// the reasoning of its most confident item, and `sure`, which is false as soon
// as ONE of its items is below the threshold: a group is applied as a whole, so
// it is only as sure as its weakest verdict.
export function groupSuggestions(suggestions = []) {
  const byKey = new Map();
  for (const s of suggestions) {
    const action = canonicalAction(s.action);
    const address = senderAddress(s);
    const key = groupKey(action, s.labelName, address || `email:${s.emailId}`);
    let g = byKey.get(key);
    if (!g) {
      g = {
        key,
        action,
        labelName: action === 'label' ? (s.labelName || '').trim() : '',
        address,
        name: senderName(s),
        // The name is the sender's address (the server says so): laid out
        // so its domain is never cut (AddressLine).
        nameIsAddress: Boolean(s.senderLabelIsAddress),
        from: s.from || '',
        items: [],
        reasoning: '',
        topConfidence: -1,
        minConfidence: 1,
      };
      byKey.set(key, g);
    }
    g.items.push(s);
    const c = Number(s.confidence) || 0;
    if (c < g.minConfidence) g.minConfidence = c;
    if (c > g.topConfidence && s.reasoning) {
      g.topConfidence = c;
      g.reasoning = s.reasoning;
    }
  }

  const groups = [...byKey.values()].map((g) => ({ ...g, sure: isSure(g.minConfidence) }));
  const byWeight = (a, b) =>
    b.sure - a.sure ||
    b.items.length - a.items.length ||
    MOVES.indexOf(a.action) - MOVES.indexOf(b.action) ||
    a.name.localeCompare(b.name, 'fr');

  return {
    moves: groups.filter((g) => g.action !== 'keep').sort(byWeight),
    keeps: groups.filter((g) => g.action === 'keep').sort(byWeight),
  };
}

// keepOrder holds groups where they are while the user works through them.
// Taking one email out of a group can change its rank (LinkedIn drops to three
// emails and falls behind Zalando), and a row that moves under the finger takes
// the next tap meant for its neighbour. So once an order is on screen it holds;
// only a group the screen has not shown yet (a new analysis, a refresh that
// brought one back) sorts the list by weight again.
export function keepOrder(groups, shownKeys = []) {
  const position = new Map(shownKeys.map((key, i) => [key, i]));
  if (groups.some((g) => !position.has(g.key))) return groups;
  return [...groups].sort((a, b) => position.get(a.key) - position.get(b.key));
}

const count = (groups) => groups.reduce((n, g) => n + g.items.length, 0);

// The numbers the header and the bulk button state. `sure` is what "Ranger"
// applies, `unsure` is what it leaves out and says so. `canApply` says whether
// a group's verdict can be applied now (a label waits until the mailbox is
// known, canApplyVerdict in landing/features.js): such a group is never in the
// bulk, nor counted as "à vérifier", nor in `byAction`, the "N à classer"
// chips. It is counted apart, as `held`.
export function planTotals({ moves, keeps }, canApply = () => true) {
  const applicable = moves.filter(canApply);
  const byAction = {};
  for (const g of applicable) byAction[g.action] = (byAction[g.action] || 0) + g.items.length;
  const sureGroups = applicable.filter((g) => g.sure);
  return {
    moves: count(moves),
    // What can be tidied now: the "à ranger" the header and the list
    // announce, so they never promise more than "Ranger N" applies.
    toTidy: count(applicable),
    held: count(moves) - count(applicable),
    keeps: count(keeps),
    byAction,
    sureGroups,
    sure: count(sureGroups),
    unsure: count(applicable) - count(sureGroups),
  };
}

// Verdict to sender-rule action. The rules engine says `trash` where the model
// says `delete`, and has no `keep`: a rule that does nothing is not a rule.
export const RULE_ACTION = { archive: 'archive', delete: 'trash', label: 'label' };
