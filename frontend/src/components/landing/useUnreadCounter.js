import { useEffect, useState } from 'react';
import { prefersReducedMotion } from './scroll';

export const UNREAD_START = 4212;

// The "4 212 non lus" figure. It ticks down by 1 to 3 every 3.2 s, in step with
// the elevator of the hall, and stands still for anyone who asked for less
// motion. Decorative: it is not announced to screen readers as it changes.
export function useUnreadCounter(start = UNREAD_START) {
  const [n, setN] = useState(start);

  useEffect(() => {
    if (prefersReducedMotion()) return undefined;
    const timer = setInterval(() => setN((v) => Math.max(0, v - 1 - Math.floor(Math.random() * 3))), 3200);
    return () => clearInterval(timer);
  }, []);

  return n;
}

// "4 212", with a no-break space so the figure never wraps in two.
export function formatCount(v) {
  return String(v).replace(/\B(?=(\d{3})+(?!\d))/g, ' ');
}
