import React from 'react';

// The heading of a floor: its number on a brass button, a kicker, the title
// (with an <em> for the italic accent) and an optional lede.
export default function FloorHeading({ n, kicker, id, lede, children }) {
  return (
    <div className="hl-fh">
      {kicker && (
        <span className="hl-fh__k">
          {n ? <b aria-hidden="true">{n}</b> : null}
          {kicker}
        </span>
      )}
      <h2 id={id} className="hl-fh__title">
        {children}
      </h2>
      {lede && <p className="hl-fh__lede">{lede}</p>}
    </div>
  );
}
