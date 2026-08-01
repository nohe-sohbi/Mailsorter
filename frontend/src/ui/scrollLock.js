import { useEffect } from 'react';

// A single reference-counted scroll lock, shared by every overlay in the app.
//
// Two independent locks drift apart: with a dialog open on top of the
// full-screen mobile reader, closing the reader would restore scrolling while
// the dialog was still up, letting the page slide behind it. Counting instead of
// toggling means the page only starts scrolling again when the last overlay is
// gone, whatever order they close in.
let depth = 0;
let restoreTo = '';

export function lockScroll() {
  if (depth === 0) {
    restoreTo = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  }
  depth += 1;
}

export function unlockScroll() {
  depth = Math.max(0, depth - 1);
  if (depth === 0) document.body.style.overflow = restoreTo;
}

export function useScrollLock(active) {
  useEffect(() => {
    if (!active) return undefined;
    lockScroll();
    return unlockScroll;
  }, [active]);
}
