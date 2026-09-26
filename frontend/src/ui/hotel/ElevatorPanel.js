import React from 'react';

// The elevator button panel: numbered brass buttons, the current floor lit.
// The landing's menu today, the dashboard's later.
export default function ElevatorPanel({ items, active, label = 'Sections', onGo }) {
  return (
    <nav className="ht-panel" aria-label={label}>
      {items.map((it) => (
        <a
          key={it.id}
          href={`#${it.id}`}
          className={it.id === active ? 'is-on' : undefined}
          aria-current={it.id === active ? 'location' : undefined}
          onClick={onGo ? (event) => onGo(event, it.id) : undefined}
        >
          <i aria-hidden="true">{it.n}</i>
          <span>{it.label}</span>
        </a>
      ))}
    </nav>
  );
}
