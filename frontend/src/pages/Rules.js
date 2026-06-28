import React, { useEffect, useState } from 'react';
import { ruleService, accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { cn } from '../ui/cn';
import Spinner from '../ui/Spinner';
import { Bolt, Archive, Trash, Tag, Pin, Mail, Check, X, Refresh, Search } from '../ui/icons';

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

const ACTIONS = [
  { value: 'archive', label: 'Archiver', Icon: Archive, tone: 'text-sky-600 bg-sky-50' },
  { value: 'trash', label: 'Supprimer', Icon: Trash, tone: 'text-rose-600 bg-rose-50' },
  { value: 'label', label: 'Étiqueter', Icon: Tag, tone: 'text-amber-700 bg-amber-50' },
  { value: 'markRead', label: 'Marquer comme lu', Icon: Mail, tone: 'text-emerald-600 bg-emerald-50' },
  { value: 'star', label: 'Mettre en favori', Icon: Pin, tone: 'text-amber-600 bg-amber-50' },
];

const emptyRule = () => ({
  name: '',
  enabled: true,
  matchAll: true,
  conditions: [{ field: 'from', operator: 'contains', value: '' }],
  actions: [{ type: 'archive', labelName: '' }],
  priority: 0,
});

const actionMeta = (value) => ACTIONS.find((a) => a.value === value) || ACTIONS[0];
const fieldLabel = (v) => FIELDS.find((f) => f.value === v)?.label || v;
const operatorLabel = (v) => OPERATORS.find((o) => o.value === v)?.label || v;

// effectiveActions gives a uniform action list for a rule from either shape: the
// new multi-action `actions` array, or the legacy single `action`/`labelName`.
const effectiveActions = (rule) =>
  rule.actions && rule.actions.length
    ? rule.actions
    : [{ type: rule.action || 'archive', labelName: rule.labelName || '' }];

// normalizeForEdit ensures a rule loaded from the API always has an `actions`
// array the editor can mutate, regardless of how it was stored.
const normalizeForEdit = (rule) => ({ ...rule, actions: effectiveActions(rule) });

function RuleEditor({ initial, onCancel, onSave, saving }) {
  const [rule, setRule] = useState(initial);
  const toast = useToast();

  const set = (patch) => setRule((r) => ({ ...r, ...patch }));
  const setCondition = (i, patch) =>
    setRule((r) => ({ ...r, conditions: r.conditions.map((c, idx) => (idx === i ? { ...c, ...patch } : c)) }));
  const addCondition = () =>
    setRule((r) => ({ ...r, conditions: [...r.conditions, { field: 'subject', operator: 'contains', value: '' }] }));
  const removeCondition = (i) =>
    setRule((r) => ({ ...r, conditions: r.conditions.filter((_, idx) => idx !== i) }));

  const setAction = (i, patch) =>
    setRule((r) => ({ ...r, actions: r.actions.map((a, idx) => (idx === i ? { ...a, ...patch } : a)) }));
  const addAction = () =>
    setRule((r) => ({ ...r, actions: [...r.actions, { type: 'label', labelName: '' }] }));
  const removeAction = (i) =>
    setRule((r) => ({ ...r, actions: r.actions.filter((_, idx) => idx !== i) }));

  const submit = () => {
    if (!rule.name.trim()) return toast.error('Donnez un nom à votre règle.');
    if (!rule.actions.length) return toast.error('Ajoutez au moins une action.');
    if (rule.actions.some((a) => a.type === 'label' && !a.labelName.trim()))
      return toast.error('Indiquez le libellé à appliquer.');
    if (rule.conditions.some((c) => !c.value.trim())) return toast.error('Chaque condition doit avoir une valeur.');
    onSave(rule);
  };

  // Actions already chosen can't be picked again (except "label", which can
  // repeat with different names) — keeps the combo meaningful.
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
            value={rule.priority}
            onChange={(e) => set({ priority: parseInt(e.target.value, 10) || 0 })}
          />
          <span className="mt-1 block text-xs text-ink-400">Plus le nombre est petit, plus la règle est prioritaire.</span>
        </label>
      </div>

      <div>
        <div className="mb-2 flex items-center justify-between">
          <span className="text-sm font-semibold text-ink-700">Conditions</span>
          <div className="flex items-center gap-1.5 text-xs">
            <button
              onClick={() => set({ matchAll: true })}
              className={cn('rounded-md px-2 py-1 font-semibold', rule.matchAll ? 'bg-brand-50 text-brand-700' : 'text-ink-400 hover:bg-ink-100')}
            >
              Toutes
            </button>
            <button
              onClick={() => set({ matchAll: false })}
              className={cn('rounded-md px-2 py-1 font-semibold', !rule.matchAll ? 'bg-brand-50 text-brand-700' : 'text-ink-400 hover:bg-ink-100')}
            >
              Au moins une
            </button>
          </div>
        </div>
        <div className="space-y-2">
          {rule.conditions.map((c, i) => (
            <div key={i} className="flex flex-wrap items-center gap-2">
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
                placeholder={isTemporalOperator(c.operator) ? 'Nombre de jours…' : 'Valeur…'}
                value={c.value}
                onChange={(e) => setCondition(i, { value: e.target.value })}
              />
              {rule.conditions.length > 1 && (
                <button onClick={() => removeCondition(i)} className="btn-ghost px-2 text-ink-400" aria-label="Retirer la condition">
                  <X size={16} />
                </button>
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
          <span className="text-xs text-ink-400">Exécutées dans l’ordre — ex. <em>Étiqueter</em> puis <em>Archiver</em></span>
        </div>
        <div className="space-y-2">
          {rule.actions.map((a, i) => (
            <div key={i} className="flex flex-wrap items-center gap-2">
              <select className="input w-auto flex-none" value={a.type} onChange={(e) => setAction(i, { type: e.target.value })}>
                {ACTIONS.map((opt) => (
                  <option key={opt.value} value={opt.value} disabled={opt.value !== 'label' && opt.value !== a.type && usedTypes.has(opt.value)}>
                    {opt.label}
                  </option>
                ))}
              </select>
              {a.type === 'label' && (
                <input
                  className="input min-w-[140px] flex-1"
                  placeholder="Nom du libellé…"
                  value={a.labelName}
                  onChange={(e) => setAction(i, { labelName: e.target.value })}
                />
              )}
              {rule.actions.length > 1 && (
                <button onClick={() => removeAction(i)} className="btn-ghost px-2 text-ink-400" aria-label="Retirer l’action">
                  <X size={16} />
                </button>
              )}
            </div>
          ))}
        </div>
        {rule.actions.length < ACTIONS.length && (
          <button onClick={addAction} className="mt-2 text-sm font-semibold text-brand-600 hover:text-brand-700">
            + Ajouter une action
          </button>
        )}
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
          <span className={cn('flex h-8 w-8 shrink-0 items-center justify-center rounded-lg', meta.tone)}>
            <meta.Icon size={16} />
          </span>
          <h3 className="truncate font-bold text-ink-900">{rule.name}</h3>
          {!rule.enabled && <span className="chip bg-ink-100 text-ink-500">En pause</span>}
        </div>
        <p className="mt-2 text-sm text-ink-500">
          <span className="font-medium text-ink-600">{rule.matchAll ? 'Si toutes' : 'Si au moins une'}</span> :{' '}
          {rule.conditions.map((c, i) => (
            <span key={i}>
              {i > 0 && <span className="text-ink-300"> · </span>}
              {fieldLabel(c.field)} {operatorLabel(c.operator)} «{c.value}»
            </span>
          ))}
          <span className="text-ink-300"> → </span>
          {actions.map((a, i) => (
            <span key={i} className="font-semibold text-ink-700">
              {i > 0 && <span className="font-normal text-ink-300"> + </span>}
              {actionMeta(a.type).label}{a.type === 'label' ? ` « ${a.labelName} »` : ''}
            </span>
          ))}
        </p>
        {rule.appliedCount > 0 && (
          <p className="mt-1 text-xs text-ink-400">Appliquée {rule.appliedCount} fois</p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <button
          onClick={onToggle}
          className={cn('relative h-6 w-11 rounded-full transition-colors', rule.enabled ? 'bg-brand-500' : 'bg-ink-200')}
          aria-label={rule.enabled ? 'Désactiver' : 'Activer'}
          title={rule.enabled ? 'Désactiver' : 'Activer'}
        >
          <span className={cn('absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all', rule.enabled ? 'left-[22px]' : 'left-0.5')} />
        </button>
        <button onClick={onEdit} className="btn-ghost px-2.5 text-sm font-semibold">Modifier</button>
        <button onClick={onDelete} className="btn-ghost px-2 text-ink-400 hover:text-rose-600" aria-label="Supprimer la règle">
          <Trash size={16} />
        </button>
      </div>
    </div>
  );
}

function Rules() {
  const toast = useToast();
  const [rules, setRules] = useState(null);
  const [editing, setEditing] = useState(null); // rule object (with id) or 'new'
  const [saving, setSaving] = useState(false);
  const [applying, setApplying] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [preview, setPreview] = useState(null); // { scanned, willApply, byRule, samples }
  const [autoApply, setAutoApply] = useState(false);

  const load = () => {
    ruleService
      .getRules()
      .then((r) => setRules(r.data.rules || []))
      .catch(() => toast.error('Impossible de charger les règles.'));
  };

  useEffect(() => {
    load();
    accountService
      .getSettings()
      .then((r) => setAutoApply(!!r.data.autoApplyRules))
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async (rule) => {
    setSaving(true);
    try {
      if (rule.id) await ruleService.updateRule(rule.id, rule);
      else await ruleService.createRule(rule);
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

  const toggleAutoApply = async () => {
    const next = !autoApply;
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
        {rules && rules.length > 0 && (
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

      {rules && rules.length > 0 && (
        <div className="card mb-5 flex flex-wrap items-center justify-between gap-3 p-4">
          <div className="flex items-start gap-3">
            <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-lg', autoApply ? 'bg-brand-50 text-brand-600' : 'bg-ink-100 text-ink-400')}>
              <Bolt size={18} />
            </span>
            <div>
              <div className="text-sm font-bold text-ink-900">Autopilote au sync</div>
              <p className="mt-0.5 max-w-md text-xs text-ink-500">
                Appliquer automatiquement vos règles à chaque synchronisation de la boîte — sans IA, sans quota.
              </p>
            </div>
          </div>
          <button
            onClick={toggleAutoApply}
            className={cn('relative h-6 w-11 shrink-0 rounded-full transition-colors', autoApply ? 'bg-brand-500' : 'bg-ink-200')}
            aria-label={autoApply ? 'Désactiver l’autopilote' : 'Activer l’autopilote'}
            title={autoApply ? 'Désactiver l’autopilote' : 'Activer l’autopilote'}
          >
            <span className={cn('absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-all', autoApply ? 'left-[22px]' : 'left-0.5')} />
          </button>
        </div>
      )}

      {preview && (
        <div className="card mb-5 p-5">
          <div className="mb-3 flex items-center justify-between gap-3">
            <h3 className="flex items-center gap-2 font-bold text-ink-900">
              <Search size={16} className="text-brand-500" /> Aperçu — {preview.willApply} email{preview.willApply > 1 ? 's' : ''} sur {preview.scanned}
            </h3>
            <button onClick={() => setPreview(null)} className="btn-ghost px-2 text-ink-400" aria-label="Fermer l’aperçu">
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
                    <span key={h.ruleName} className={cn('chip', meta.tone)}>
                      <meta.Icon size={13} /> {h.ruleName} · {h.matched}
                    </span>
                  );
                })}
              </div>
              <ul className="mt-3 space-y-1.5 border-t border-ink-100 pt-3">
                {(preview.samples || []).map((s, i) => (
                  <li key={i} className="flex items-center gap-2 text-xs text-ink-500">
                    {effectiveActions(s).map((a, j) => (
                      <span key={j} className={cn('chip shrink-0', actionMeta(a.type).tone)}>{actionMeta(a.type).label}</span>
                    ))}
                    <span className="truncate"><span className="font-medium text-ink-700">{s.subject || '(sans objet)'}</span> — {s.from}</span>
                  </li>
                ))}
              </ul>
              <p className="mt-3 text-xs text-ink-400">Aperçu en lecture seule : rien n’a été modifié dans Gmail.</p>
            </>
          )}
        </div>
      )}

      {editing === 'new' && (
        <div className="mb-5">
          <RuleEditor initial={emptyRule()} saving={saving} onCancel={() => setEditing(null)} onSave={save} />
        </div>
      )}

      {rules === null ? (
        <div className="flex justify-center py-16"><Spinner size={24} className="text-brand-500" /></div>
      ) : rules.length === 0 && editing !== 'new' ? (
        <div className="card flex flex-col items-center gap-4 py-14 text-center">
          <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-brand-50 text-brand-600"><Bolt size={26} /></span>
          <div>
            <h3 className="font-bold text-ink-900">Aucune règle pour l’instant</h3>
            <p className="mx-auto mt-1 max-w-sm text-sm text-ink-500">Créez votre première règle pour archiver, étiqueter ou supprimer automatiquement les emails récurrents.</p>
          </div>
          <button onClick={() => setEditing('new')} className="btn-primary">+ Créer une règle</button>
        </div>
      ) : (
        <div className="space-y-3">
          {rules.map((rule) =>
            editing && editing.id === rule.id ? (
              <RuleEditor key={rule.id} initial={normalizeForEdit(rule)} saving={saving} onCancel={() => setEditing(null)} onSave={save} />
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

      {rules && rules.length > 0 && editing !== 'new' && (
        <button onClick={() => setEditing('new')} className="btn-secondary mt-4 w-full">+ Nouvelle règle</button>
      )}
    </div>
  );
}

export default Rules;
