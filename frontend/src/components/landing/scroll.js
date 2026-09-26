export function prefersReducedMotion() {
  return typeof window !== 'undefined' && Boolean(window.matchMedia) && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// Smooth unless the visitor asked for less motion. No hash is pushed: the
// landing is one page and a #floor in the URL would outlive the visit.
export function scrollToId(id) {
  const el = document.getElementById(id);
  if (el) el.scrollIntoView({ behavior: prefersReducedMotion() ? 'auto' : 'smooth', block: 'start' });
}
