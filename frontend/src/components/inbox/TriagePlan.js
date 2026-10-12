import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useEmails } from '../../contexts/EmailContext';
import { aiService, emailService, senderService, apiError } from '../../services/api';
import { useToast } from '../../ui/Toast';
import { useConfirm } from '../../ui/Confirm';
import { track } from '../../lib/analytics';
import { actionMeta, canonicalAction, pastParticiple, plural } from '../../ui/actions';
import { toneFor } from '../../ui/avatar';
import { cn } from '../../ui/cn';
import Spinner from '../../ui/Spinner';
import { Sparkles, Bolt, Check, ChevronDown, X, Tag, Pin, Alert } from '../../ui/icons';
import { groupSuggestions, keepOrder, planTotals, idOf, isSure, RULE_ACTION } from './planGroups';

// Mirrors maxBatchActionSize in backend/internal/api/batch.go (batch-undo) and
// maxRejectBatchSize in backend/internal/api/ai_handlers.go.
const BATCH_LIMIT = 200;

// How many groups each block shows before "Afficher N autres". The plan sits
// above the inbox, and on a phone a long one used to push the first email
// below the fold.
const VISIBLE_MOVES = 4;
const VISIBLE_KEEPS = 3;

// The order the header counts verbs in. Fixed rather than by size, so the same
// verb is always in the same place.
const VERB_ORDER = ['archive', 'label', 'delete'];

const COLLAPSED_KEY = 'mailsorter_plan_collapsed';

const readCollapsed = () => {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === '1';
  } catch {
    return false;
  }
};

const chunks = (list) => {
  const out = [];
  for (let i = 0; i < list.length; i += BATCH_LIMIT) out.push(list.slice(i, i + BATCH_LIMIT));
  return out;
};

const emailsCount = (n) => `${n} email${plural(n)}`;

// "6 emails archivés", "8 emails gardés dans la boîte", "2 emails étiquetés
// « Factures »", and "15 emails rangés" only when the verbs were mixed.
function appliedMessage(groups, applied) {
  const n = applied.length;
  const verbs = new Set(groups.map((g) => g.action));
  if (verbs.size > 1) return `${emailsCount(n)} rangé${plural(n)}`;
  const [verb] = verbs;
  if (verb === 'keep') return `${emailsCount(n)} gardé${plural(n)} dans la boîte`;
  const g = groups[0];
  const where = groups.length === 1 && verb === 'label' && g.labelName ? ` « ${g.labelName} »` : '';
  return `${emailsCount(n)} ${pastParticiple(verb, n).toLowerCase()}${where}`;
}

// useTriagePlan owns everything the plan DOES: grouping, applying, ignoring,
// undoing, promoting a sender to a rule. The Inbox holds the instance, because
// its keyboard handler needs `applyAll` and its own actions need `settle`.
export function useTriagePlan({ onTriaged, onMoved } = {}) {
  const navigate = useNavigate();
  const toast = useToast();
  const confirm = useConfirm();
  const { suggestions, removeSuggestions, restoreSuggestions, removeEmails, fetchData } = useEmails();
  const [applyingAll, setApplyingAll] = useState(false);
  const [ruling, setRuling] = useState(null);
  // Group keys a rule was created from in this visit, so the button says it is
  // done instead of offering to create the same rule twice.
  const [ruled, setRuled] = useState(() => new Set());

  // The order last rendered, so acting on a group never reshuffles the others.
  const shown = useRef({ moves: [], keeps: [] });
  const groups = useMemo(() => {
    const fresh = groupSuggestions(suggestions);
    return {
      moves: keepOrder(fresh.moves, shown.current.moves),
      keeps: keepOrder(fresh.keeps, shown.current.keeps),
    };
  }, [suggestions]);
  useEffect(() => {
    shown.current = { moves: groups.moves.map((g) => g.key), keeps: groups.keeps.map((g) => g.key) };
  }, [groups]);
  const totals = useMemo(() => planTotals(groups), [groups]);

  // `settle` is called from handlers the Inbox memoizes, so it reads the list
  // through a ref: a closure would settle against the list as it was when that
  // handler was created.
  const latest = useRef(suggestions);
  latest.current = suggestions;

  const refresh = () => fetchData({ forceRefresh: true, sync: false });

  // One batch-undo per verdict: undoing an archive and undoing a label are
  // different requests, and a label needs its name to be taken off again.
  const undo = async (applied) => {
    const byVerb = new Map();
    for (const s of applied) {
      const action = canonicalAction(s.action);
      if (action === 'keep') continue;
      const labelName = action === 'label' ? s.labelName || '' : '';
      const key = `${action}|${labelName}`;
      if (!byVerb.has(key)) byVerb.set(key, { action, labelName, ids: [] });
      byVerb.get(key).ids.push(s.emailId);
    }
    try {
      for (const { action, labelName, ids } of byVerb.values()) {
        for (const chunk of chunks(ids)) await emailService.batchUndo(chunk, action, labelName);
      }
      toast.success('Tri annulé');
    } catch (err) {
      toast.error(apiError(err, "Impossible d'annuler."));
    } finally {
      refresh();
    }
  };

  // Optimistic, like the single apply always was: the rows and the emails leave
  // at once. Whatever the server did not apply comes back from a refresh, which
  // knows better than a local guess.
  const apply = async (targets, { bulk = false } = {}) => {
    const items = targets.flatMap((g) => g.items);
    if (items.length === 0) return;
    const ids = items.map(idOf);
    const moving = items.filter((s) => canonicalAction(s.action) !== 'keep').map((s) => s.emailId);
    removeSuggestions(ids);
    removeEmails(moving);
    if (moving.length) onMoved?.(moving);

    try {
      const { data } = await aiService.applyBatch(ids);
      const done = new Set(data?.appliedIds || []);
      const applied = items.filter((s) => done.has(idOf(s)));
      if (applied.length < items.length) refresh();

      if (bulk) track('apply_all', { applied: applied.length });
      else track('suggestion_group_applied', { action: targets[0].action, count: applied.length });
      if (applied.length > 0) onTriaged?.(applied.length);

      const protectedSkipped = data?.protectedSkipped || 0;
      const failed = data?.failed || 0;
      if (applied.length === 0) {
        toast.error(
          protectedSkipped
            ? 'Rien appliqué : ces expéditeurs sont protégés.'
            : "Le tri n'a pas pu être appliqué. Réessayez."
        );
        return;
      }
      let message = appliedMessage(targets, applied);
      if (protectedSkipped) message += ` · ${protectedSkipped} protégé${plural(protectedSkipped)} laissé${plural(protectedSkipped)} en place`;
      const movedSomething = applied.some((s) => canonicalAction(s.action) !== 'keep');
      // Only offered where batch-undo can actually do it (the server says so):
      // an "Annuler" that fails once pressed is worse than none.
      if (movedSomething && data?.reversible) toast.action(message, 'Annuler', () => undo(applied));
      else toast.success(message);
      if (failed) toast.error(`${emailsCount(failed)} n'${failed > 1 ? 'ont' : 'a'} pas pu être traité${plural(failed)}.`);
    } catch (err) {
      refresh();
      toast.error(apiError(err, "Le tri n'a pas pu être appliqué. Réessayez."));
    }
  };

  const applyGroup = (group) => apply([group]);

  // The bulk button applies the SURE moves only, and says how many. A group
  // with one doubtful verdict is left for the user to look at, and the footer
  // says so rather than leaving them to wonder why it stayed.
  const applyAll = async () => {
    const targets = totals.sureGroups;
    if (targets.length === 0 || applyingAll) return;
    const n = targets.reduce((sum, g) => sum + g.items.length, 0);
    const deletions = targets.filter((g) => actionMeta(g.action).destructive);
    // Also bound to a single keystroke ("a"), so it must never trash mail
    // without asking, and it names who would be trashed.
    if (deletions.length > 0) {
      const d = deletions.reduce((sum, g) => sum + g.items.length, 0);
      const names = deletions.slice(0, 3).map((g) => g.name).join(', ');
      const more = deletions.length > 3 ? ` et ${deletions.length - 3} autre${plural(deletions.length - 3)}` : '';
      const ok = await confirm({
        title: `Ranger ${emailsCount(n)} ?`,
        message: `Dont ${d} suppression${plural(d)} : ${names}${more}.`,
        detail: 'Les suppressions partent à la corbeille et y restent récupérables 30 jours.',
        confirmLabel: `Ranger ${emailsCount(n)}`,
        danger: true,
      });
      if (!ok) return;
    }
    setApplyingAll(true);
    try {
      await apply(targets, { bulk: true });
    } finally {
      setApplyingAll(false);
    }
  };

  // Rejecting has no undo endpoint, and nothing in the mailbox moved, so a
  // failure simply puts the rows back.
  const ignore = async (items, { scope }) => {
    if (items.length === 0) return;
    const ids = items.map(idOf);
    removeSuggestions(ids);
    try {
      for (const chunk of chunks(ids)) await aiService.rejectBatch(chunk);
      track('suggestions_ignored', { scope, count: ids.length });
      if (scope !== 'email') {
        toast.info(`${ids.length} suggestion${plural(ids.length)} ignorée${plural(ids.length)}`, { duration: 2500 });
      }
    } catch (err) {
      restoreSuggestions(items);
      toast.error(apiError(err, 'Impossible de les ignorer. Réessayez.'));
    }
  };

  const ignoreGroup = (group) => ignore(group.items, { scope: 'group' });
  const ignoreEmail = (suggestion) => ignore([suggestion], { scope: 'email' });
  const ignoreMoves = () => ignore(groups.moves.flatMap((g) => g.items), { scope: 'all' });
  const keepAll = () => apply(groups.keeps);

  // Promote a sender to a deterministic rule: the next time, no model call and
  // no quota. Worded as what it is (a rule), because whether it runs at every
  // sync is the user's setting on /rules, not something this button can promise.
  const createRule = async (group) => {
    const ruleAction = RULE_ACTION[group.action];
    if (!ruleAction || !group.address || ruling) return;
    setRuling(group.key);
    try {
      await senderService.createRule(group.address, ruleAction, group.labelName);
      track('rule_created', { source: 'plan' });
      setRuled((prev) => new Set(prev).add(group.key));
      toast.action(`Règle créée pour ${group.name}.`, 'Voir les règles', () => navigate('/rules'));
    } catch (err) {
      toast.error(apiError(err, "La règle n'a pas pu être créée."));
    } finally {
      setRuling(null);
    }
  };

  // Drops the pending verdicts on mail the user just handled by hand (archived
  // from the reader, deleted from the list, snoozed), and rejects them on the
  // server so a refresh does not bring them back. Without it the plan went on
  // offering to archive a message that was already gone.
  const settle = useCallback(
    (emailIds) => {
      const wanted = new Set(Array.isArray(emailIds) ? emailIds : [emailIds]);
      const stale = latest.current.filter((s) => wanted.has(s.emailId));
      if (stale.length === 0) return;
      const ids = stale.map(idOf);
      removeSuggestions(ids);
      // Best effort: the user's own action already succeeded, and the only cost
      // of a failure here is the verdict reappearing on the next refresh.
      aiService.rejectBatch(ids).catch((err) => console.warn('Could not settle suggestions:', err));
    },
    [removeSuggestions]
  );

  return {
    groups,
    totals,
    applyingAll,
    ruling,
    ruled,
    applyGroup,
    applyAll,
    keepAll,
    ignoreGroup,
    ignoreEmail,
    ignoreMoves,
    createRule,
    settle,
  };
}

function Avatar({ group }) {
  return (
    <span
      className={cn(
        'flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white',
        toneFor(group.from)
      )}
      aria-hidden
    >
      {group.name[0]?.toUpperCase() || '?'}
    </span>
  );
}

function GroupRow({ group, plan, expanded, onToggle, onOpenEmail }) {
  const meta = actionMeta(group.action);
  const n = group.items.length;
  const single = n === 1;
  const isKeep = group.action === 'keep';
  const bodyId = `plan-group-${group.key.replace(/[^a-zA-Z0-9]/g, '-')}`;
  const verb = single ? meta.label : `${meta.label} les ${n}`;
  const where = group.action === 'label' && group.labelName ? ` dans « ${group.labelName} »` : '';
  const ruleAction = RULE_ACTION[group.action];
  const canRule = Boolean(ruleAction && group.address && (group.action !== 'label' || group.labelName));
  const ruled = plan.ruled.has(group.key);

  return (
    <li className="px-4 py-3 sm:px-5">
      {/* Header, list, buttons on a phone, so the buttons come AFTER the emails
          they act on; header and buttons on one line from sm up, the list
          wrapping beneath them. */}
      <div className="flex flex-col gap-2.5 sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-4 sm:gap-y-3">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          aria-controls={bodyId}
          className="order-1 flex min-w-0 flex-1 items-start gap-3 rounded-lg text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-2"
        >
          <Avatar group={group} />
          <span className="min-w-0 flex-1">
            <span className="flex items-center gap-2">
              <span className="truncate font-semibold text-ink-900">{group.name}</span>
              {!single && <span className="shrink-0 text-xs font-medium text-muted">{emailsCount(n)}</span>}
              <ChevronDown
                size={16}
                className={cn('ml-auto shrink-0 text-subtle transition-transform duration-200', expanded && 'rotate-180')}
                aria-hidden
              />
            </span>
            {single && (
              <span className="mt-0.5 block truncate text-sm text-ink-800">{group.items[0].subject || '(Sans sujet)'}</span>
            )}
            {group.reasoning && <span className="mt-0.5 block text-sm text-muted line-clamp-2">{group.reasoning}</span>}
            {(where || !group.sure) && (
              <span className="mt-1.5 flex flex-wrap gap-1.5">
                {where && (
                  <span className={cn('chip py-0.5', meta.chip)}>
                    <Tag size={12} /> {group.labelName}
                  </span>
                )}
                {!group.sure && (
                  <span className="chip bg-caution-50 py-0.5 text-caution-700" title="L'IA n'est pas sûre d'elle sur au moins un de ces emails">
                    <Alert size={12} /> À vérifier
                  </span>
                )}
              </span>
            )}
          </span>
        </button>

        {/* Fixed width from sm up, so every row's chevron lines up whatever the
            length of its verb. */}
        <div className="order-3 flex shrink-0 items-center justify-end gap-1.5 pl-12 sm:order-2 sm:w-[17rem] sm:pl-0">
          {!isKeep && (
            <button
              type="button"
              onClick={() => plan.ignoreGroup(group)}
              disabled={plan.applyingAll}
              className="btn-ghost h-10 rounded-lg px-3 sm:h-9"
              aria-label={`Ignorer la suggestion pour ${single ? 'cet email' : `ces ${n} emails`} de ${group.name}`}
            >
              Ignorer
            </button>
          )}
          <button
            type="button"
            onClick={() => plan.applyGroup(group)}
            disabled={plan.applyingAll}
            className={cn('btn h-10 rounded-lg px-3 sm:h-9', meta.quiet || 'bg-ink-100 text-ink-700 hover:bg-ink-200')}
            aria-label={`${meta.label} ${single ? "l'email" : `les ${n} emails`} de ${group.name}${where}`}
          >
            <meta.Icon size={15} />
            {verb}
          </button>
        </div>
        {expanded && (
          <div id={bodyId} className="order-2 pl-12 sm:order-3 sm:basis-full">
            <ul className="divide-y divide-[rgb(var(--hairline))] overflow-hidden rounded-xl border border-hairline">
              {group.items.map((s) => (
                <li key={idOf(s)} className="flex items-center bg-surface">
                  <button
                    type="button"
                    onClick={() => onOpenEmail(s)}
                    className="min-w-0 flex-1 px-3 py-2.5 text-left transition-colors hover:bg-surface-sunken focus:outline-none focus-visible:bg-surface-sunken"
                    title="Ouvrir cet email"
                  >
                    <span className="block truncate text-sm text-ink-800">{s.subject || '(Sans sujet)'}</span>
                    {!single && !isSure(s.confidence) && (
                      <span className="mt-0.5 flex items-center gap-1 text-xs font-semibold text-caution-700">
                        <Alert size={12} /> À vérifier
                      </span>
                    )}
                  </button>
                  {!single && (
                    <button
                      type="button"
                      onClick={() => plan.ignoreEmail(s)}
                      className="btn-ghost btn-sm btn-icon mr-1 shrink-0 text-muted"
                      aria-label={`Retirer « ${s.subject || 'sans sujet'} » de ce tri`}
                      title="Retirer de ce tri"
                    >
                      <X size={15} />
                    </button>
                  )}
                </li>
              ))}
            </ul>
            {canRule &&
              (ruled ? (
                <p className="mt-2 flex items-center gap-1.5 text-xs font-semibold text-positive-700">
                  <Check size={14} /> Règle créée
                </p>
              ) : (
                <button
                  type="button"
                  onClick={() => plan.createRule(group)}
                  disabled={plan.ruling === group.key}
                  className="mt-2 inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-xs font-semibold text-brand-700 transition-colors hover:bg-brand-50 disabled:opacity-50"
                  title="Les règles trient sans IA ni quota"
                >
                  {plan.ruling === group.key ? <Spinner size={14} /> : <Bolt size={14} />}
                  Toujours {meta.label.toLowerCase()} ses emails{where ? ` « ${group.labelName} »` : ''}
                </button>
              ))}
          </div>
        )}
      </div>
    </li>
  );
}

function ShowMore({ hidden, onClick }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center justify-center gap-1.5 border-t border-hairline px-4 py-2.5 text-sm font-semibold text-brand-700 transition-colors hover:bg-surface-sunken"
    >
      Afficher {hidden} autre{plural(hidden)} expéditeur{plural(hidden)} <ChevronDown size={15} />
    </button>
  );
}

// TriagePlan renders the plan: what the AI proposes, one row per sender and
// verdict, with the verb written out, the reason in full, and the emails one
// tap away. Moving verdicts first, with the bulk button; "keep" verdicts after,
// in their own block, because applying them changes nothing.
function TriagePlan({ plan, onOpenEmail }) {
  const { groups, totals } = plan;
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const [expanded, setExpanded] = useState(() => new Set());
  const [allMoves, setAllMoves] = useState(false);
  const [allKeeps, setAllKeeps] = useState(false);

  if (groups.moves.length === 0 && groups.keeps.length === 0) return null;

  const toggleCollapsed = () => {
    const next = !collapsed;
    setCollapsed(next);
    try {
      if (next) localStorage.setItem(COLLAPSED_KEY, '1');
      else localStorage.removeItem(COLLAPSED_KEY);
    } catch {
      // Remembering the fold is a convenience: without storage it just resets.
    }
  };

  const toggleGroup = (key) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const moves = allMoves ? groups.moves : groups.moves.slice(0, VISIBLE_MOVES);
  const keeps = allKeeps ? groups.keeps : groups.keeps.slice(0, VISIBLE_KEEPS);
  const reviewed = totals.moves + totals.keeps;

  const chips = [
    ...VERB_ORDER.filter((a) => totals.byAction[a]).map((a) => ({ action: a, n: totals.byAction[a] })),
    // A verb the table above does not list still gets counted, as itself.
    ...Object.keys(totals.byAction)
      .filter((a) => !VERB_ORDER.includes(a))
      .map((a) => ({ action: a, n: totals.byAction[a] })),
    ...(totals.keeps ? [{ action: 'keep', n: totals.keeps }] : []),
  ];

  const row = (g) => (
    <GroupRow
      key={g.key}
      group={g}
      plan={plan}
      expanded={expanded.has(g.key)}
      onToggle={() => toggleGroup(g.key)}
      onOpenEmail={onOpenEmail}
    />
  );

  return (
    <section aria-labelledby="triage-plan-title" className="card mb-4 overflow-hidden animate-fade-up">
      <div className="px-4 py-3.5 sm:px-5">
        <div className="flex items-start gap-3">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-brand-50 text-brand-600" aria-hidden>
            <Sparkles size={18} />
          </span>
          <div className="min-w-0 flex-1">
            <h2 id="triage-plan-title" className="font-bold leading-tight text-ink-900">
              Tri proposé
            </h2>
            <p className="mt-0.5 text-sm text-muted">
              {emailsCount(reviewed)} analysé{plural(reviewed)}, rien ne bouge sans votre accord.
            </p>
          </div>
          <button
            type="button"
            onClick={toggleCollapsed}
            aria-expanded={!collapsed}
            aria-controls="triage-plan-body"
            className="btn-ghost btn-sm btn-icon -mr-1.5 shrink-0"
            title={collapsed ? 'Déplier' : 'Replier'}
          >
            <span className="sr-only">{collapsed ? 'Déplier le tri proposé' : 'Replier le tri proposé'}</span>
            <ChevronDown size={18} className={cn('transition-transform duration-200', !collapsed && 'rotate-180')} />
          </button>
        </div>
        {/* Full width on a phone: inside the text column the chips got about
            250px and stacked one per line. */}
        <ul className="mt-3 flex flex-wrap gap-1.5 sm:pl-12" aria-label="Répartition du tri proposé">
          {chips.map(({ action, n }) => {
            const meta = actionMeta(action);
            return (
              <li key={action} className={cn('chip', meta.chip)}>
                <meta.Icon size={13} aria-hidden />
                {n} à {meta.label.toLowerCase()}
              </li>
            );
          })}
        </ul>
      </div>

      {!collapsed && (
        <div id="triage-plan-body">
          {groups.moves.length > 0 && (
            <div className="border-t border-hairline">
              {groups.keeps.length > 0 && (
                <h3 className="px-4 pt-3 text-xs font-bold uppercase tracking-wider text-muted sm:px-5">
                  À ranger · {totals.moves}
                </h3>
              )}
              <ul className="divide-y divide-[rgb(var(--hairline))]">{moves.map(row)}</ul>
              {moves.length < groups.moves.length && (
                <ShowMore hidden={groups.moves.length - moves.length} onClick={() => setAllMoves(true)} />
              )}
              <div className="flex flex-col gap-2 border-t border-hairline bg-surface-sunken/60 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
                <p className="text-xs text-muted">
                  {totals.unsure === 0
                    ? "Chaque rangement est journalisé dans l'historique."
                    : totals.sure === 0
                    ? "L'IA hésite sur ceux-ci : à vérifier un par un."
                    : `${emailsCount(totals.unsure)} à vérifier ${totals.unsure > 1 ? 'restent' : 'reste'} hors du tri groupé.`}
                </p>
                <div className="flex items-center justify-end gap-2">
                  <button
                    type="button"
                    onClick={plan.ignoreMoves}
                    disabled={plan.applyingAll}
                    className="btn-ghost h-10 rounded-lg px-3 sm:h-9"
                  >
                    Tout ignorer
                  </button>
                  {totals.sure > 0 && (
                    <button
                      type="button"
                      onClick={plan.applyAll}
                      disabled={plan.applyingAll}
                      className="btn-primary h-10 rounded-lg px-4 sm:h-9"
                      title="Ranger les suggestions sûres (a)"
                    >
                      {plan.applyingAll ? <Spinner size={16} /> : <Bolt size={16} />}
                      Ranger {emailsCount(totals.sure)}
                    </button>
                  )}
                </div>
              </div>
            </div>
          )}

          {groups.keeps.length > 0 && (
            <div className="border-t border-hairline">
              <div className="flex items-start justify-between gap-3 px-4 pt-3 sm:px-5">
                <div className="min-w-0">
                  <h3 className="flex items-center gap-1.5 text-sm font-bold text-ink-900">
                    <Pin size={15} className="text-positive-600" aria-hidden /> À garder sous les yeux
                  </h3>
                  <p className="mt-0.5 text-xs text-muted">L'IA conseille de ne pas y toucher.</p>
                </div>
                {groups.keeps.length > 1 && (
                  <button
                    type="button"
                    onClick={plan.keepAll}
                    disabled={plan.applyingAll}
                    className="btn-ghost btn-sm shrink-0"
                  >
                    Tout garder
                  </button>
                )}
              </div>
              <ul className="divide-y divide-[rgb(var(--hairline))]">{keeps.map(row)}</ul>
              {keeps.length < groups.keeps.length && (
                <ShowMore hidden={groups.keeps.length - keeps.length} onClick={() => setAllKeeps(true)} />
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}

export default TriagePlan;
