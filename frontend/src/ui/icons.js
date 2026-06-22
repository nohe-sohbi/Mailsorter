import React from 'react';

// Lightweight, dependency-free icon set (stroke-based, inherits currentColor).
const base = {
  width: '1em',
  height: '1em',
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 2,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
};

const make = (paths) => ({ size = 20, className = '', ...rest }) => (
  <svg {...base} style={{ width: size, height: size }} className={className} {...rest}>
    {paths}
  </svg>
);

export const Logo = ({ size = 28, className = '' }) => (
  <svg viewBox="0 0 32 32" width={size} height={size} className={className} aria-hidden>
    <defs>
      <linearGradient id="ms-logo" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0" stopColor="#6366f1" />
        <stop offset="1" stopColor="#d946ef" />
      </linearGradient>
    </defs>
    <rect width="32" height="32" rx="8" fill="url(#ms-logo)" />
    <rect x="7" y="9" width="18" height="14" rx="2.5" fill="none" stroke="white" strokeWidth="2.2" />
    <path d="M7 11l9 6 9-6" fill="none" stroke="white" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);

export const Sparkles = make(
  <>
    <path d="M12 3l1.6 4.4L18 9l-4.4 1.6L12 15l-1.6-4.4L6 9l4.4-1.6L12 3z" />
    <path d="M19 14l.8 2.2L22 17l-2.2.8L19 20l-.8-2.2L16 17l2.2-.8L19 14z" />
  </>
);
export const Archive = make(
  <>
    <rect x="3" y="4" width="18" height="4" rx="1" />
    <path d="M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8" />
    <path d="M10 12h4" />
  </>
);
export const Trash = make(
  <>
    <path d="M3 6h18" />
    <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" />
    <path d="M19 6l-1 14a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1L5 6" />
    <path d="M10 11v6M14 11v6" />
  </>
);
export const Tag = make(
  <>
    <path d="M20.6 13.4l-7.2 7.2a2 2 0 0 1-2.8 0L3 13V4a1 1 0 0 1 1-1h9l7.6 7.6a2 2 0 0 1 0 2.8z" />
    <circle cx="7.5" cy="7.5" r="1.5" />
  </>
);
export const Pin = make(
  <>
    <path d="M9 4h6l-1 6 3 3v2H7v-2l3-3-1-6z" />
    <path d="M12 15v5" />
  </>
);
export const Inbox = make(
  <>
    <path d="M22 12h-6l-2 3h-4l-2-3H2" />
    <path d="M5.5 5h13a2 2 0 0 1 1.8 1.1L22 12v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6l3.7-5.9A2 2 0 0 1 5.5 5z" />
  </>
);
export const Search = make(
  <>
    <circle cx="11" cy="11" r="7" />
    <path d="m21 21-4.3-4.3" />
  </>
);
export const Refresh = make(
  <>
    <path d="M21 12a9 9 0 1 1-2.6-6.4" />
    <path d="M21 3v6h-6" />
  </>
);
export const Check = make(<path d="M20 6 9 17l-5-5" />);
export const X = make(<path d="M18 6 6 18M6 6l12 12" />);
export const ChevronRight = make(<path d="m9 6 6 6-6 6" />);
export const Shield = make(
  <>
    <path d="M12 3l8 3v6c0 5-3.4 8.3-8 9-4.6-.7-8-4-8-9V6l8-3z" />
    <path d="m9 12 2 2 4-4" />
  </>
);
export const Bolt = make(<path d="M13 2 4.5 13.5H11l-1 8.5L19.5 10H13l0-8z" />);
export const Settings = make(
  <>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1A1.7 1.7 0 0 0 4.6 9a1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
  </>
);
export const LogOut = make(
  <>
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
    <path d="m16 17 5-5-5-5" />
    <path d="M21 12H9" />
  </>
);
export const Users = make(
  <>
    <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M22 21v-2a4 4 0 0 0-3-3.9" />
    <path d="M16 3.1A4 4 0 0 1 16 11" />
  </>
);
export const Mail = make(
  <>
    <rect x="2" y="4" width="20" height="16" rx="2" />
    <path d="m22 7-10 6L2 7" />
  </>
);
export const Alert = make(
  <>
    <path d="M12 9v4M12 17h.01" />
    <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
  </>
);
export const Flame = make(
  <path d="M12 22c4 0 7-2.7 7-6.5 0-3.6-2.6-5.6-3.8-8.5-.5 1.6-1.4 2.6-2.4 3.2.2-2.2-.6-4.6-2.8-7.2-.3 3-2 4.4-3.4 6C5.3 10.5 5 12 5 13.5 5 17.3 8 22 12 22z" />
);
export const Keyboard = make(
  <>
    <rect x="2" y="6" width="20" height="12" rx="2" />
    <path d="M7 10h.01M11 10h.01M15 10h.01M17 10h.01M7 14h10" />
  </>
);
export const Undo = make(
  <>
    <path d="M9 14 4 9l5-5" />
    <path d="M4 9h11a5 5 0 0 1 0 10h-1" />
  </>
);

export const Clock = make(
  <>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 7v5l3 2" />
  </>
);

export const History = make(
  <>
    <path d="M3 3v5h5" />
    <path d="M3.05 13A9 9 0 1 0 6 5.3L3 8" />
    <path d="M12 7v5l4 2" />
  </>
);

export const BellOff = make(
  <>
    <path d="M8.7 3.6A6 6 0 0 1 18 8c0 2 .4 3.6 1 5" />
    <path d="M6 8c0 4-2 5-2 5h12" />
    <path d="M10.3 21a2 2 0 0 0 3.4 0" />
    <path d="M2 2l20 20" />
  </>
);

export const Google = ({ size = 18, className = '' }) => (
  <svg width={size} height={size} viewBox="0 0 48 48" className={className} aria-hidden>
    <path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3c-1.6 4.7-6.1 8-11.3 8a12 12 0 1 1 7.9-21l5.7-5.7A20 20 0 1 0 44 24c0-1.2-.1-2.4-.4-3.5z" />
    <path fill="#FF3D00" d="M6.3 14.7l6.6 4.8A12 12 0 0 1 24 12c3 0 5.8 1.1 7.9 3l5.7-5.7A20 20 0 0 0 6.3 14.7z" />
    <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2A12 12 0 0 1 12.7 28l-6.5 5C9.5 39.6 16.2 44 24 44z" />
    <path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3a12 12 0 0 1-4.1 5.6l6.2 5.2C39.9 35.5 44 30.3 44 24c0-1.2-.1-2.4-.4-3.5z" />
  </svg>
);
