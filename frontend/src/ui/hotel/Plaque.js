import React from 'react';

// A numbered brass plaque: one short line with its roman numeral.
export default function Plaque({ n, children }) {
  return (
    <div className="ht-plq">
      <b aria-hidden="true">{n}</b>
      <p>{children}</p>
    </div>
  );
}
