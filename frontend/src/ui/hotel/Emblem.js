import React from 'react';

// The mark: an envelope under a pediment. Fixed colours, like every drawing of
// the theme: it reads the same by day and by night.
export default function Emblem({ size = 34 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 34 34" aria-hidden="true">
      <polygon points="3,14 17,4 31,14" fill="#7A2E3B" stroke="#2B1B1E" strokeWidth="1.6" strokeLinejoin="round" />
      <rect x="5" y="14" width="24" height="16" fill="#FBF1E4" stroke="#2B1B1E" strokeWidth="1.6" />
      <path d="M5.5 14.5 L17 23 L28.5 14.5" fill="none" stroke="#2B1B1E" strokeWidth="1.6" />
      <circle cx="17" cy="11" r="2" fill="#E3A93B" stroke="#2B1B1E" />
    </svg>
  );
}
