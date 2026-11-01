import React from 'react';
import { actionMeta } from '../../ui/actions';
import { cn } from '../../ui/cn';
import { Archive, Check, Search, Star, Trash, X } from '../../ui/icons';
import { hotelTone, initials } from './avatar';

// The inbox, drawn by the Grand Hotel. Inbox.js keeps every handler, every
// piece of state and the classic markup; these components only draw what it
// hands them, so the two themes cannot disagree on what a click does.

// What Mailsorter proposes, phrased as the question it is. `short` is the
// version that fits on a row: "Factures ?" rather than "Ranger dans Factures".
export function proposalText(suggestion, { short = false } = {}) {
  if (suggestion.action === 'label') {
    const name = suggestion.labelName || 'un libellé';
    return short ? name : `Ranger dans ${name}`;
  }
  return actionMeta(suggestion.action).label;
}

export function ProposalChip({ suggestion, short = false }) {
  const meta = actionMeta(suggestion.action);
  return (
    <span className={cn('hd-pchip', meta.destructive && 'is-danger')}>
      {proposalText(suggestion, { short })} ?
    </span>
  );
}

// The four counters as one board. Each cell is still the filter it names.
export function HotelStats({ stats, cards, activeQuery, onPick, formatNumber }) {
  return (
    <div className="hd-stats">
      {cards.map(({ key, label, query }) => (
        <button key={key} onClick={() => onPick(query)} aria-pressed={activeQuery === query}>
          <span className="hd-cap">{label}</span>
          <b>{formatNumber(stats[key])}</b>
        </button>
      ))}
    </div>
  );
}

// The quick filters and the saved searches, as luggage tags.
export function HotelFilters({ filters, saved, activeQuery, activeFilter, defaultQuery, isSaved, onFilter, onSaved, onRemoveSaved, onSave, onClear }) {
  return (
    <div className="hd-tags" role="group" aria-label="Filtres">
      {filters.map(({ id, label, query }) => (
        <button key={id} onClick={() => onFilter(query)} aria-pressed={activeQuery === query} className="ht-tag is-plain">
          {label}
        </button>
      ))}
      {saved.map((s) => (
        <span key={s.id} className={cn('ht-tag is-saved', activeQuery === s.query && 'is-on')}>
          <button onClick={() => onSaved(s)} aria-pressed={activeQuery === s.query} title={s.query}>
            <Search size={12} /> {s.name}
          </button>
          <button onClick={() => onRemoveSaved(s)} aria-label={`Supprimer la recherche « ${s.name} »`} className="hd-x">
            <X size={11} />
          </button>
        </span>
      ))}
      {!activeFilter && activeQuery !== defaultQuery && (
        <span className="ht-tag is-gold">
          <Search size={12} />
          <span className="ml-1">{activeQuery.replace(`${defaultQuery} `, '')}</span>
          {!isSaved(activeQuery) && (
            <button onClick={onSave} aria-label="Enregistrer cette recherche" title="Enregistrer cette recherche" className="hd-x">
              <Star size={11} />
            </button>
          )}
          <button onClick={onClear} aria-label="Effacer le filtre" className="hd-x">
            <X size={11} />
          </button>
        </span>
      )}
    </div>
  );
}

// One message on the list. The proposal for it, when there is one, rides on
// the row itself, next to the message it is about. The triage plan above
// stays the place to decide by sender; this is the same verdict, one email at
// a time, through the same handlers (the Inbox's single apply, the plan's
// ignore), so the two can never disagree.
export function HotelRow({
  email,
  name,
  rowRef,
  isActive,
  isChecked,
  isFocused,
  current,
  suggestion,
  when,
  onToggle,
  onOpen,
  onArchive,
  onDelete,
  onApply,
  onIgnore,
}) {
  const starred = email.isStarred || (email.labelIds || []).includes('STARRED');
  const subject = email.subject || '(Sans sujet)';
  return (
    <li
      ref={rowRef}
      className={cn('hd-row', !email.isRead && 'is-unread', isActive && 'is-open', isChecked && 'is-checked', isFocused && 'is-focused')}
    >
      <button onClick={onToggle} role="checkbox" aria-checked={isChecked} aria-label={`Sélectionner : ${subject}, de ${name}`} className="hd-check">
        <span className={cn('ht-check', isChecked && 'is-on')} aria-hidden />
      </button>
      <span className={hotelTone(email.from)} aria-hidden>
        {initials(name)}
      </span>
      <button onClick={onOpen} className="hd-row__open">
        <span className="hd-row__top">
          <span className="hd-row__who">
            {name}
            {!email.isRead && <span className="sr-only"> (non lu)</span>}
          </span>
          <span className="hd-row__when">
            {current && <span className="ht-tag is-gold" style={{ height: 20, fontSize: 10, paddingRight: 18 }}>En cours</span>}
            {starred && <Star size={13} className="fill-current text-caution-500" aria-label="Favori" />}
            {when}
          </span>
        </span>
        <span className="hd-row__what">
          <b>{subject}</b> {email.snippet}
        </span>
      </button>
      <span className="hd-row__acts">
        {suggestion ? (
          <>
            <ProposalChip suggestion={suggestion} short />
            <button onClick={() => onApply(suggestion)} className="hd-ibtn is-ok" aria-label={`Valider : ${proposalText(suggestion)}, ${subject}`} title="Valider">
              <Check size={14} />
            </button>
            <button onClick={() => onIgnore(suggestion)} className="hd-ibtn" aria-label={`Ignorer la proposition pour ${subject}`} title="Ignorer">
              <X size={14} />
            </button>
          </>
        ) : (
          onArchive && (
            <span className="hd-row__quick">
              <button onClick={onArchive} className="hd-ibtn" aria-label={`Archiver : ${subject}`} title="Archiver">
                <Archive size={14} />
              </button>
              <button onClick={onDelete} className="hd-ibtn is-danger" aria-label={`Supprimer : ${subject}`} title="Supprimer">
                <Trash size={14} />
              </button>
            </span>
          )
        )}
      </span>
    </li>
  );
}
