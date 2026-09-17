import { useEffect } from 'react';

// A single reference-counted scroll lock, shared by every overlay in the app.
//
// Two independent locks drift apart: with a dialog open on top of the
// full-screen mobile reader, closing the reader would restore scrolling while
// the dialog was still up, letting the page slide behind it. Counting instead of
// toggling means the page only starts scrolling again when the last overlay is
// gone, whatever order they close in.
let depth = 0;
let restoreOverflow = '';
let restorePadding = '';

export function lockScroll() {
  if (depth === 0) {
    restoreOverflow = document.body.style.overflow;
    restorePadding = document.body.style.paddingRight;
    // Hiding the overflow removes the scrollbar, and on any platform whose
    // scrollbars take up space (Windows, most Linux, macOS set to always show)
    // the page then widens by its thickness, and everything jumps sideways the
    // instant a dialog opens. Reserve the width we are about to reclaim.
    const gap = window.innerWidth - document.documentElement.clientWidth;
    if (gap > 0) {
      const current = parseFloat(window.getComputedStyle(document.body).paddingRight) || 0;
      document.body.style.paddingRight = `${current + gap}px`;
    }
    document.body.style.overflow = 'hidden';
  }
  depth += 1;
}

export function unlockScroll() {
  depth = Math.max(0, depth - 1);
  if (depth === 0) {
    document.body.style.overflow = restoreOverflow;
    document.body.style.paddingRight = restorePadding;
  }
}

export function useScrollLock(active) {
  useEffect(() => {
    if (!active) return undefined;
    lockScroll();
    return unlockScroll;
  }, [active]);
}
