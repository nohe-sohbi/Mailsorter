// The dashboard's floors, in the order the lift serves them. One list for the
// panel in the header, the drawer on a phone and the plaque on each page, so
// a screen and its button can never disagree on a number.
//
// Tarifs is billing-only, like in the classic header: a self-hosted instance
// bills nobody. Floors are numbered after the filter, so the panel never
// skips a number.
const FLOORS = [
  { to: '/inbox', label: 'Boîte' },
  { to: '/rules', label: 'Règles' },
  { to: '/snoozed', label: 'Plus tard' },
  { to: '/history', label: 'Historique' },
  { to: '/pricing', label: 'Tarifs', billingOnly: true },
  { to: '/settings', label: 'Réglages' },
];

export function floorsFor(selfHosted) {
  return FLOORS.filter((f) => !f.billingOnly || !selfHosted).map((f, i) => ({ ...f, n: i + 1 }));
}

export function floorOf(pathname, selfHosted) {
  return floorsFor(selfHosted).find((f) => pathname === f.to || pathname.startsWith(`${f.to}/`)) || null;
}
