import React from 'react';
import { cn } from './cn';

export default function Spinner({ size = 20, className = '' }) {
  return (
    <svg
      className={cn('animate-spin', className)}
      style={{ width: size, height: size }}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden
    >
      <circle className="opacity-20" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" />
      <path
        className="opacity-90"
        d="M22 12a10 10 0 0 1-10 10"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
      />
    </svg>
  );
}
