import React from 'react';

// The safe: what is kept is kept locked. The plate says how (AES-256-GCM at
// rest, see backend/internal/crypto).
const INK = '#2B1B1E';
const MUST = '#E3A93B';
const BOLTS = [[180, 63], [263, 97], [297, 180], [263, 263], [180, 297], [97, 263], [63, 180], [97, 97]];
const TICKS = [[180, 150, 180, 156], [210, 180, 204, 180], [180, 210, 180, 204], [150, 180, 156, 180], [201, 159, 197, 163], [201, 201, 197, 197], [159, 201, 163, 197], [159, 159, 163, 163]];

export default function Vault({ className }) {
  return (
    <svg className={className} viewBox="0 0 360 360" role="img" aria-label="Un coffre-fort, fermé">
      <rect x="14" y="14" width="332" height="332" rx="22" fill="#2F6F73" stroke={INK} strokeWidth="3" />
      <rect x="28" y="28" width="304" height="304" rx="14" fill="none" stroke="#1F4F4F" strokeWidth="2" />
      <g fill={MUST} stroke={INK}>
        {[[42, 42], [318, 42], [42, 318], [318, 318], [180, 38], [180, 322]].map(([cx, cy]) => (
          <circle key={`r-${cx}-${cy}`} cx={cx} cy={cy} r="4" />
        ))}
      </g>
      <g fill={MUST} stroke={INK} strokeWidth="1.5">
        <rect x="20" y="96" width="18" height="44" rx="3" />
        <rect x="20" y="220" width="18" height="44" rx="3" />
      </g>
      <circle cx="180" cy="180" r="124" fill="#3E8580" stroke={INK} strokeWidth="3" />
      <circle cx="180" cy="180" r="110" fill="none" stroke={MUST} strokeWidth="4" />
      <circle cx="180" cy="180" r="98" fill="#2F6F73" stroke={INK} strokeWidth="1.5" />
      <g fill={MUST} stroke={INK} strokeWidth="1.2">
        {BOLTS.map(([cx, cy]) => (
          <circle key={`b-${cx}-${cy}`} cx={cx} cy={cy} r="6" />
        ))}
      </g>
      <circle cx="180" cy="180" r="62" fill="none" stroke={MUST} strokeWidth="6" />
      <circle cx="180" cy="180" r="62" fill="none" stroke={INK} strokeWidth="1" />
      <g stroke={MUST} strokeWidth="6" strokeLinecap="round">
        <line x1="180" y1="180" x2="180" y2="112" />
        <line x1="180" y1="180" x2="239" y2="214" />
        <line x1="180" y1="180" x2="121" y2="214" />
      </g>
      <g fill={MUST} stroke={INK} strokeWidth="1.3">
        <circle cx="180" cy="108" r="9" />
        <circle cx="243" cy="216" r="9" />
        <circle cx="117" cy="216" r="9" />
      </g>
      <circle cx="180" cy="180" r="32" fill={MUST} stroke={INK} strokeWidth="2" />
      <g stroke={INK} strokeWidth="1.2">
        {TICKS.map(([x1, y1, x2, y2]) => (
          <line key={`t-${x1}-${y1}`} x1={x1} y1={y1} x2={x2} y2={y2} />
        ))}
      </g>
      <circle cx="180" cy="180" r="10" fill="#7A2E3B" stroke={INK} strokeWidth="1.5" />
      <polygon points="180,140 175,132 185,132" fill="#7A2E3B" stroke={INK} />
      <rect x="132" y="312" width="96" height="20" rx="2" fill={MUST} stroke={INK} strokeWidth="1.3" />
      <text x="180" y="326" className="hf-vault">
        AES-256
      </text>
    </svg>
  );
}
