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

// The bare address out of a From header, lowercased. It keys the group, and it
// is what a sender rule matches (`From contains <address>` on the server).
export function senderAddress(from = '') {
  const angle = from.match(/<([^>]+)>/);
  return (angle ? angle[1] : from).trim().toLowerCase();
}

// The display name out of a From header, without the quotes RFC 5322 wraps it
// in, falling back to the address.
export function senderName(from = '') {
  const name = from.split('<')[0].replace(/"/g, '').trim();
  return name || senderAddress(from) || 'Expéditeur inconnu';
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
    const address = senderAddress(s.from || '');
    const key = groupKey(action, s.labelName, address || `email:${s.emailId}`);
    let g = byKey.get(key);
    if (!g) {
      g = {
        key,
        action,
        labelName: action === 'label' ? (s.labelName || '').trim() : '',
        address,
        name: senderName(s.from || ''),
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
// applies, `unsure` is what it leaves out and says so.
export function planTotals({ moves, keeps }) {
  const byAction = {};
  for (const g of moves) byAction[g.action] = (byAction[g.action] || 0) + g.items.length;
  const sureGroups = moves.filter((g) => g.sure);
  return {
    moves: count(moves),
    keeps: count(keeps),
    byAction,
    sureGroups,
    sure: count(sureGroups),
    unsure: count(moves) - count(sureGroups),
  };
}

// Verdict to sender-rule action. The rules engine says `trash` where the model
// says `delete`, and has no `keep`: a rule that does nothing is not a rule.
export const RULE_ACTION = { archive: 'archive', delete: 'trash', label: 'label' };
