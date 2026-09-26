import React from 'react';

// A key on its hook, the provider's name on the tag. Gold for a mailbox that
// gets every feature, pink otherwise. `tilt` (degrees) keeps a board of keys
// from looking like a spreadsheet.
export default function KeyTag({ name, note, gold = false, tilt = 0 }) {
  return (
    <div className="ht-key">
      <span className="ht-key__hook" aria-hidden="true" />
      <span className="ht-key__str" aria-hidden="true" />
      <span className={`ht-tag ht-key__tag${gold ? ' is-gold' : ''}`} style={{ '--tilt': `${tilt}deg` }}>
        {note && <small>{note}</small>}
        {name}
      </span>
    </div>
  );
}
