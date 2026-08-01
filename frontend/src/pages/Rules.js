import React, { useEffect, useId, useState } from 'react';
import { ruleService, accountService, labelService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { track } from '../lib/analytics';
import { cn } from '../ui/cn';
import Spinner from '../ui/Spinner';
import { actionMeta } from '../ui/actions';
import { Toggle, EmptyState, ErrorState } from '../ui/primitives';
import { Bolt, Trash, Tag, Check, X, Refresh, Search } from '../ui/icons';

const FIELDS = [
  { value: 'from', label: 'Expéditeur' },
  { value: 'subject', label: 'Sujet' },
  { value: 'snippet', label: 'Aperçu' },
  { value: 'to', label: 'Destinataire' },
  { value: 'body', label: 'Contenu' },
];

const OPERATORS = [
  { value: 'contains', label: 'contient' },
  { value: 'notContains', label: 'ne contient pas' },
  { value: 'equals', label: 'est égal à' },
  { value: 'notEquals', label: 'est différent de' },
  { value: 'startsWith', label: 'commence par' },
  { value: 'endsWith', label: 'finit par' },
  { value: 'regex', label: 'correspond à (regex)' },
  { value: 'olderThan', label: 'plus vieux que (jours)' },
  { value: 'newerThan', label: 'plus récent que (jours)' },
];

// Temporal operators compare the email's age, so their value is a number of days
// rather than free text.
const isTemporalOperator = (op) => op === 'olderThan' || op === 'newerThan';

// The action *values* the backend understands for a rule. They are not all the
// canonical keys of ui/actions (a rule deletes with `trash`, the ledger logs
// `delete`): the values below are what we send, actionMeta only decides how they
// are drawn.
const ACTION_TYPES = ['archive', 'trash', 'label', 'markRead', 'star'];

const emptyRule = () => ({
  name: '',
  enabled: true,
  matchAll: true,
  conditions: [{ field: 'from', operator: 'contains', value: '' }],
  actions: [{ type: 'archive', labelName: '' }],
  priority: 0,
});

const fieldLabel = (v) => FIELDS.find((f) => f.value === v)?.label || v;
const operatorLabel = (v) => OPERATORS.find((o) => o.value === v)?.label || v;

// A condition's value can arrive from the API as a number (day counts), so it is
// normalised before any string work.
const conditionValue = (v) => String(v ?? '');

// Concrete examples beat an abstract "Valeur…": most people write their first
// rule by imitating the placeholder.
const valuePlaceholder = (c) => {
  if (isTemporalOperator(c.operator)) return 'Ex. 30';
  if (c.operator === 'regex') return 'Ex. ^facture-\\d+';
  switch (c.field) {
    case 'from':
      return 'Ex. newsletter@acme.com ou @acme.com';
    case 'to':
      return 'Ex. moi+boulot@gmail.com';
    case 'subject':
      return 'Ex. Facture';
    case 'snippet':
      return 'Ex. se désabonner';
    case 'body':
      return 'Ex. numéro de commande';
    default:
      return 'Valeur…';
  }
};

// effectiveActions gives a uniform action list for a rule from either shape: the
// new multi-action `actions` array, or the legacy single `action`/`labelName`.
const effectiveActions = (rule) =>
  rule.actions && rule.actions.length
    ? rule.actions
    : [{ type: rule.action || 'archive', labelName: rule.labelName || '' }];

// normalizeForEdit ensures a rule loaded from the API always has an `actions`
// array the editor can mutate, regardless of how it was stored.
const normalizeForEdit = (rule) => ({ ...rule, actions: effectiveActions(rule) });

// LabelField: the user picks from their real Gmail labels, but stays free to
// name a new one. Typing blind was how a single typo ended up creating a second,
// near-identical label — so an unknown name is now announced *before* saving,
// instead of being discovered later in Gmail.
function LabelField({ value, onChange, labels, labelsKnown, listId, hintId }) {
  const typed = (value || '').trim();
  const isNew = labelsKnown && typed !== '' && !labels.includes(typed);
  return (
    <div className="min-w-[180px] flex-1">
      <input
        className="input"
        list={listId}
        placeholder={labels.length ? 'Choisissez ou créez un libellé…' : 'Ex. Factures'}
        value={value || ''}
        onChange={(e) => onChange(e.target.value)}
        aria-label="Libellé à appliquer"
        aria-describedby={isNew ? hintId : undefined}
        autoComplete="off"
      />
      {isNew && (
        <span id={hintId} className="mt-1.5 flex items-center gap-1 text-xs font-semibold text-caution-700">
          <Tag size={12} /> Nouveau libellé : il sera créé dans Gmail.
        </span>
      )}
    </div>
  );
}

function RuleEditor({ initial, onCancel, onSave, saving, labels, labelsKnown }) {
  const [rule, setRule] = useState(initial);
  const toast = useToast();
  const uid = useId();
  const listId = `${uid}-labels`;

  const set = (patch) => setRule((r) => ({ ...r, ...patch }));
  const setCondition = (i, patch) =>
    setRule((r) => ({ ...r, conditions: r.conditions.map((c, idx) => (idx === i ? { ...c, ...patch } : c)) }));
  const addCondition = () =>
    setRule((r) => ({ ...r, conditions: [...r.conditions, { field: 'subject', operator: 'contains', value: '' }] }));
  const removeCondition = (i) =>
    setRule((r) => ({ ...r, conditions: r.conditions.filter((_, idx) => idx !== i) }));

  const setAction = (i, patch) =>
    setRule((r) => ({ ...r, actions: r.actions.map((a, idx) => (idx === i ? { ...a, ...patch } : a)) }));
  // Switching an existing action to "label" has to give it a labelName: an
  // action created as `archive` has none, and the missing field used to crash
  // the validation below on `.trim()`.
  const setActionType = (i, type) =>
    setRule((r) => ({
      ...r,
      actions: r.actions.map((a, idx) =>
        idx === i ? { ...a, type, labelName: type === 'label' ? a.labelName || '' : a.labelName } : a
      ),
    }));
  const addAction = () =>
    setRule((r) => ({ ...r, actions: [...r.actions, { type: 'label', labelName: '' }] }));
  const removeAction = (i) =>
    setRule((r) => ({ ...r, actions: r.actions.filter((_, idx) => idx !== i) }));

  const submit = () => {
    if (!rule.name.trim()) return toast.error('Donnez un nom à votre règle.');
    if (!rule.actions.length) return toast.error('Ajoutez au moins une action.');
    if (rule.actions.some((a) => a.type === 'label' && !(a.labelName || '').trim()))
      return toast.error('Indiquez le libellé à appliquer.');
    if (rule.conditions.some((c) => !conditionValue(c.value).trim()))
      return toast.error('Chaque condition doit avoir une valeur.');
    onSave(rule);
  };

  // Actions already chosen can't be picked again (except "label", which can
  // repeat with different names), which keeps the combo meaningful.
  const usedTypes = new Set(rule.actions.map((a) => a.type));

  return (
    <div className="card space-y-5 p-6">
      <div className="grid gap-4 sm:grid-cols-2">
        <label className="block">
          <span className="mb-1 block text-sm font-semibold text-ink-700">Nom de la règle</span>
          <input
            className="input"
            placeholder="Ex. Archiver les newsletters Acme"
            value={rule.name}
            onChange={(e) => set({ name: e.target.value })}
          />
        </label>
        <label className="block">
          <span className="mb-1 block text-sm font-semibold text-ink-700">Priorité</span>
          <input
            type="number"
            className="input"
            placeholder="Ex. 0"
            value={rule.priority}
            onChange={(e) => set({ priority: parseInt(e.target.value, 10) || 0 })}
          />
          <span className="mt-1 block text-xs text-muted">
            Un email n’est traité que par <strong className="font-semibold text-ink-700">une seule règle</strong> : la
            première qui correspond gagne, les suivantes sont ignorées. Plus le nombre est petit, plus la règle passe tôt.
          </span>
        </label>
      </div>

      <div>
        <div className="mb-2 flex items-center justify-between">
          <span className="text-sm font-semibold text-ink-700">Conditions</span>
          <div className="flex items-center gap-1.5 text-xs">
            <button
              onClick={() => set({ matchAll: true })}
              aria-pressed={rule.matchAll}
              className={cn(
                'rounded-md px-2 py-1 font-semibold',
                rule.matchAll ? 'bg-brand-50 text-brand-700' : 'text-muted hover:bg-ink-100'
              )}
            >
              Toutes
            </button>
            <button
              onClick={() => set({ matchAll: false })}
              aria-pressed={!rule.matchAll}
              className={cn(
                'rounded-md px-2 py-1 font-semibold',
                !rule.matchAll ? 'bg-brand-50 text-brand-700' : 'text-muted hover:bg-ink-100'
              )}
            >
              Au moins une
            </button>
          </div>
        </div>
        <div className="space-y-2">
          {rule.conditions.map((c, i) => (
            <div key={i}>
              <div className="flex flex-wrap items-center gap-2">
                <select className="input w-auto flex-none" value={c.field} onChange={(e) => setCondition(i, { field: e.target.value })}>
                  {FIELDS.map((f) => (
                    <option key={f.value} value={f.value}>{f.label}</option>
                  ))}
                </select>
                <select className="input w-auto flex-none" value={c.operator} onChange={(e) => setCondition(i, { operator: e.target.value })}>
                  {OPERATORS.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </select>
                <input
                  className="input min-w-[140px] flex-1"
                  type={isTemporalOperator(c.operator) ? 'number' : 'text'}
                  min={isTemporalOperator(c.operator) ? 0 : undefined}
                  placeholder={isTemporalOperator(c.operator) ? 'Nombre de jours… (ex. 30)' : valuePlaceholder(c)}
                  value={c.value}
                  onChange={(e) => setCondition(i, { value: e.target.value })}
                />
                {rule.conditions.length > 1 && (
                  <button onClick={() => removeCondition(i)} className="btn-ghost btn-icon text-muted" aria-label="Retirer la condition">
                    <X size={16} />
                  </button>
                )}
              </div>
              {/* Une regex mal comprise ne se voit pas : elle ne correspond
                  simplement à rien. L'exemple sert de garde-fou. */}
              {c.operator === 'regex' && (
                <p className="mt-1.5 rounded-lg bg-info-50 px-3 py-2 text-xs text-info-700">
                  Motif avancé. Exemple : <code className="font-mono font-semibold">{'^facture-\\d+'}</code> reconnaît «
                  facture-2024 » mais pas « ma facture ». Les caractères{' '}
                  <code className="font-mono font-semibold">{'. * + ? ( ) [ ] \\'}</code> ont un sens spécial. Pour une
                  recherche simple, préférez <em>contient</em>.
                </p>
              )}
            </div>
          ))}
        </div>
        <button onClick={addCondition} className="mt-2 text-sm font-semibold text-brand-600 hover:text-brand-700">
          + Ajouter une condition
        </button>
      </div>

      <div>
        <div className="mb-2 flex items-center justify-between">
          <span className="text-sm font-semibold text-ink-700">Actions</span>
          <span className="text-xs text-muted">Exécutées dans l’ordre, ex. <em>Étiqueter</em> puis <em>Archiver</em></span>
        </div>
        <div className="space-y-2">
          {rule.actions.map((a, i) => (
            <div key={i} className="flex flex-wrap items-start gap-2">
              <select className="input w-auto flex-none" value={a.type} onChange={(e) => setActionType(i, e.target.value)}>
                {ACTION_TYPES.map((type) => (
                  <option key={type} value={type} disabled={type !== 'label' && type !== a.type && usedTypes.has(type)}>
                    {actionMeta(type).label}
                  </option>
                ))}
              </select>
              {a.type === 'label' && (
                <LabelField
                  value={a.labelName}
                  onChange={(labelName) => setAction(i, { labelName })}
                  labels={labels}
                  labelsKnown={labelsKnown}
                  listId={listId}
                  hintId={`${uid}-new-label-${i}`}
                />
              )}
              {rule.actions.length > 1 && (
                <button onClick={() => removeAction(i)} className="btn-ghost btn-icon text-muted" aria-label="Retirer l’action">
                  <X size={16} />
                </button>
              )}
            </div>
          ))}
        </div>
        {rule.actions.length < ACTION_TYPES.length && (
          <button onClick={addAction} className="mt-2 text-sm font-semibold text-brand-600 hover:text-brand-700">
            + Ajouter une action
          </button>
        )}
        {/* Une seule liste pour tous les champs de libellé de l'éditeur. */}
        <datalist id={listId}>
          {labels.map((name) => (
            <option key={name} value={name} />
          ))}
        </datalist>
      </div>

      <div className="flex items-center justify-end gap-2">
        <button onClick={onCancel} className="btn-secondary">Annuler</button>
        <button onClick={submit} disabled={saving} className="btn-primary">
          {saving ? <Spinner size={16} /> : <Check size={16} />} Enregistrer
        </button>
      </div>
    </div>
  );
}

function RuleCard({ rule, onToggle, onEdit, onDelete }) {
  const actions = effectiveActions(rule);
  const meta = actionMeta(actions[0].type);
  return (
    <div className="card flex items-start justify-between gap-4 p-5">
      <div className="min-w-0">
        <div className="flex items-center gap-2.5">
          <span className={cn('flex h-8 w-8 shrink-0 items-center justify-center rounded-lg', meta.chip)}>
            <meta.Icon size={16} />
          </span>
          <h3 className="truncate font-bold text-ink-900">{rule.name}</h3>
          {!rule.enabled && <span className="chip bg-ink-100 text-ink-500">En pause</span>}
        </div>
        <p className="mt-2 text-sm text-ink-500">
          <span className="font-medium text-ink-600">{rule.matchAll ? 'Si toutes' : 'Si au moins une'}</span> :{' '}
          {rule.conditions.map((c, i) => (
            <span key={i}>
              {i > 0 && <span className="text-subtle"> · </span>}
              {fieldLabel(c.field)} {operatorLabel(c.operator)} «{c.value}»
            </span>
          ))}
          <span className="text-subtle"> → </span>
          {actions.map((a, i) => (
            <span key={i} className="font-semibold text-ink-700">
              {i > 0 && <span className="font-normal text-subtle"> + </span>}
              {actionMeta(a.type).label}{a.type === 'label' ? ` « ${a.labelName} »` : ''}
            </span>
          ))}
        </p>
        {rule.appliedCount > 0 && (
          <p className="mt-1 text-xs text-muted">Appliquée {rule.appliedCount} fois</p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <Toggle
          id={`rule-${rule.id}-enabled`}
          checked={!!rule.enabled}
          onChange={onToggle}
          // Le nom du commutateur reste dans la carte : le libellé visible est le
          // titre de la règle, mais un lecteur d'écran l'entendrait hors contexte.
          label={<span className="sr-only">{`Activer la règle « ${rule.name} »`}</span>}
        />
        <button onClick={onEdit} className="btn-ghost btn-sm">Modifier</button>
        <button onClick={onDelete} className="btn-ghost btn-sm btn-icon text-muted hover:text-danger-600" aria-label="Supprimer la règle">
          <Trash size={16} />
        </button>
      </div>
    </div>
  );
}

function Rules() {
  const toast = useToast();
  const confirm = useConfirm();
  const [rules, setRules] = useState(null);
  const [error, setError] = useState(null);
  const [editing, setEditing] = useState(null); // rule object (with id) or 'new'
  const [saving, setSaving] = useState(false);
  const [applying, setApplying] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [preview, setPreview] = useState(null); // { scanned, willApply, byRule, samples }
  const [autoApply, setAutoApply] = useState(false);
  const [labels, setLabels] = useState([]);
  // Distinct from `labels.length`: tant que l'appel n'a pas abouti, on ne peut
  // pas affirmer qu'un libellé saisi n'existe pas.
  const [labelsKnown, setLabelsKnown] = useState(false);

  const load = () => {
    ruleService
      .getRules()
      .then((r) => {
        setRules(r.data.rules || []);
        setError(null);
      })
      .catch((err) =>
        setError(err.response?.data?.trim?.() || 'Vos règles n’ont pas pu être récupérées.')
      );
  };

  useEffect(() => {
    load();
    accountService
      .getSettings()
      .then((r) => setAutoApply(!!r.data.autoApplyRules))
      .catch(() => {});
    // Les libellés sont chargés une fois pour toute la page. Un échec est
    // silencieux : l'éditeur retombe alors sur la saisie libre.
    labelService
      .list()
      .then((r) => {
        const list = Array.isArray(r.data) ? r.data : [];
        setLabels(
          list
            .filter((l) => l && l.type === 'user' && l.name)
            .map((l) => l.name)
            .sort((a, b) => a.localeCompare(b, 'fr'))
        );
        setLabelsKnown(true);
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async (rule) => {
    setSaving(true);
    try {
      if (rule.id) await ruleService.updateRule(rule.id, rule);
      else {
        await ruleService.createRule(rule);
        track('rule_created', { source: 'editor' });
      }
      toast.success('Règle enregistrée.');
      setEditing(null);
      load();
    } catch (err) {
      toast.error(err.response?.data?.trim() || 'Échec de l’enregistrement.');
    } finally {
      setSaving(false);
    }
  };

  const toggle = async (rule) => {
    try {
      await ruleService.updateRule(rule.id, { ...rule, enabled: !rule.enabled });
      load();
    } catch {
      toast.error('Échec de la mise à jour.');
    }
  };

  const remove = async (rule) => {
    if (
      !(await confirm({
        title: 'Supprimer cette règle ?',
        message: `« ${rule.name} » sera définitivement supprimée. Les emails qu’elle a déjà triés ne changent pas.`,
        confirmLabel: 'Supprimer la règle',
        danger: true,
      }))
    )
      return;
    try {
      await ruleService.deleteRule(rule.id);
      toast.success('Règle supprimée.');
      load();
    } catch {
      toast.error('Échec de la suppression.');
    }
  };

  const applyNow = async () => {
    setApplying(true);
    try {
      const { data } = await ruleService.apply();
      track('rules_applied', { applied: data.applied, scanned: data.scanned });
      if (data.applied > 0) toast.success(`${data.applied} email(s) traité(s) par vos règles. 🎯`);
      else toast.info(`Aucun email à traiter (${data.scanned} analysés).`);
      setPreview(null);
      load();
    } catch {
      toast.error('Impossible d’appliquer les règles.');
    } finally {
      setApplying(false);
    }
  };

  const runPreview = async () => {
    setPreviewing(true);
    try {
      const { data } = await ruleService.preview();
      setPreview(data);
      if (data.willApply === 0) toast.info(`Aucun email concerné (${data.scanned} analysés).`);
    } catch {
      toast.error('Aperçu impossible.');
    } finally {
      setPreviewing(false);
    }
  };

  const toggleAutoApply = async (next) => {
    setAutoApply(next); // optimistic
    try {
      await accountService.updateSettings({ autoApplyRules: next });
      toast.success(next ? 'Autopilote activé : vos règles s’appliqueront à chaque synchro.' : 'Autopilote désactivé.');
    } catch {
      setAutoApply(!next); // revert
      toast.error('Mise à jour impossible.');
    }
  };

  const enabledCount = (rules || []).filter((r) => r.enabled).length;
  const hasRules = !error && rules && rules.length > 0;

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-6 flex items-end justify-between gap-4">
        <div>
          <span className="chip mb-2 bg-brand-50 text-brand-700"><Bolt size={14} /> Tri automatique, sans IA</span>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Règles de tri</h1>
          <p className="mt-1 max-w-lg text-sm text-ink-500">
            Encodez vos cas évidents une fois : les règles s’appliquent instantanément, gratuitement et sans consommer votre quota IA.
          </p>
        </div>
        {hasRules && (
          <div className="flex shrink-0 items-center gap-2">
            <button onClick={runPreview} disabled={previewing || enabledCount === 0} className="btn-secondary" title="Voir ce que feraient vos règles, sans rien modifier">
              {previewing ? <Spinner size={16} /> : <Search size={16} />} Aperçu
            </button>
            <button onClick={applyNow} disabled={applying || enabledCount === 0} className="btn-primary">
              {applying ? <Spinner size={16} /> : <Refresh size={16} />} Appliquer maintenant
            </button>
          </div>
        )}
      </div>

      {hasRules && (
        <div className="card mb-5 flex items-center gap-3 p-4">
          <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-lg', autoApply ? 'bg-brand-50 text-brand-600' : 'bg-ink-100 text-muted')}>
            <Bolt size={18} />
          </span>
          <div className="min-w-0 flex-1">
            <Toggle
              id="auto-apply-rules"
              checked={autoApply}
              onChange={toggleAutoApply}
              label="Autopilote au sync"
              description="Appliquer automatiquement vos règles à chaque synchronisation de la boîte, sans IA et sans quota."
            />
          </div>
        </div>
      )}

      {preview && (
        <div className="card mb-5 p-5">
          <div className="mb-3 flex items-center justify-between gap-3">
            <h3 className="flex items-center gap-2 font-bold text-ink-900">
              <Search size={16} className="text-brand-500" /> Aperçu : {preview.willApply} email{preview.willApply > 1 ? 's' : ''} sur {preview.scanned}
            </h3>
            <button onClick={() => setPreview(null)} className="btn-ghost btn-sm btn-icon text-muted" aria-label="Fermer l’aperçu">
              <X size={16} />
            </button>
          </div>
          {preview.willApply === 0 ? (
            <p className="text-sm text-ink-500">Aucun email de votre boîte ne correspond à vos règles actives.</p>
          ) : (
            <>
              <div className="flex flex-wrap gap-2">
                {(preview.byRule || []).map((h) => {
                  const meta = actionMeta(h.action);
                  return (
                    <span key={h.ruleName} className={cn('chip', meta.chip)}>
                      <meta.Icon size={13} /> {h.ruleName} · {h.matched}
                    </span>
                  );
                })}
              </div>
              <ul className="mt-3 space-y-1.5 border-t border-hairline pt-3">
                {(preview.samples || []).map((s, i) => (
                  <li key={i} className="flex items-center gap-2 text-xs text-ink-500">
                    {effectiveActions(s).map((a, j) => (
                      <span key={j} className={cn('chip shrink-0', actionMeta(a.type).chip)}>{actionMeta(a.type).label}</span>
                    ))}
                    <span className="truncate"><span className="font-medium text-ink-700">{s.subject || '(sans objet)'}</span> · {s.from}</span>
                  </li>
                ))}
              </ul>
              <p className="mt-3 text-xs text-muted">Aperçu en lecture seule : rien n’a été modifié dans Gmail.</p>
            </>
          )}
        </div>
      )}

      {editing === 'new' && (
        <div className="mb-5">
          <RuleEditor
            initial={emptyRule()}
            saving={saving}
            labels={labels}
            labelsKnown={labelsKnown}
            onCancel={() => setEditing(null)}
            onSave={save}
          />
        </div>
      )}

      {error ? (
        <div className="card">
          <ErrorState title="Impossible de charger vos règles" message={error} onRetry={load} />
        </div>
      ) : rules === null ? (
        <div className="flex justify-center py-16"><Spinner size={24} className="text-brand-500" /></div>
      ) : rules.length === 0 && editing !== 'new' ? (
        <div className="card">
          <EmptyState
            Icon={Bolt}
            title="Aucune règle pour l’instant"
            description="Créez votre première règle pour archiver, étiqueter ou supprimer automatiquement les emails récurrents."
            action={<button onClick={() => setEditing('new')} className="btn-primary">+ Créer une règle</button>}
          />
        </div>
      ) : (
        <div className="space-y-3">
          {/* La règle du premier match gagne change tout : sans elle, on croit
              que les règles se cumulent sur un même email. */}
          {rules.length > 1 && (
            <p className="text-xs text-muted">
              Un email n’est traité que par la première règle qui correspond (priorité la plus petite d’abord).
            </p>
          )}
          {rules.map((rule) =>
            editing && editing.id === rule.id ? (
              <RuleEditor
                key={rule.id}
                initial={normalizeForEdit(rule)}
                saving={saving}
                labels={labels}
                labelsKnown={labelsKnown}
                onCancel={() => setEditing(null)}
                onSave={save}
              />
            ) : (
              <RuleCard
                key={rule.id}
                rule={rule}
                onToggle={() => toggle(rule)}
                onEdit={() => setEditing(rule)}
                onDelete={() => remove(rule)}
              />
            )
          )}
        </div>
      )}

      {hasRules && editing !== 'new' && (
        <button onClick={() => setEditing('new')} className="btn-secondary mt-4 w-full">+ Nouvelle règle</button>
      )}
    </div>
  );
}

export default Rules;
