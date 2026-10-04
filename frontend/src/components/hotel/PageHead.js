import React from 'react';
import { useInstance } from '../../contexts/InstanceContext';
import { floorsFor } from './floors';

// The head of a floor: its number on a brass plaque, the title in the display
// face, one line of context, and the screen's own tools on the right.
//
// `floor` is the route, and the number is looked up from it (floors.js), so a
// page and its button in the lift panel always agree, Tarifs or not. A screen
// that is not a floor (the account, the connect screen) passes `badge`
// instead: the avatar, a key.
export default function PageHead({ floor, badge, title, sub, children }) {
  const { selfHosted } = useInstance();
  const n = floor ? floorsFor(selfHosted).find((f) => f.to === floor)?.n : null;
  return (
    <div className="hd-head">
      <div className="hd-head__t">
        {n ? (
          <span className="hd-floor" aria-hidden="true">
            {n}
          </span>
        ) : (
          badge
        )}
        <div className="min-w-0">
          <h1 className="hd-title">{title}</h1>
          {sub && <p className="hd-sub">{sub}</p>}
        </div>
      </div>
      {children && <div className="hd-head__x">{children}</div>}
    </div>
  );
}
