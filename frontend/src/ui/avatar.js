// One palette and one hash for sender avatars, so a sender wears the same colour
// in the list, the reader and the triage plan. Each of those used to carry its
// own copy, and a copy that drifted would recolour the same sender between two
// panes of one screen. Literal classes, so Tailwind sees them.
const AVATAR_TONES = ['bg-brand-fill', 'bg-info-fill', 'bg-positive-fill', 'bg-caution-fill', 'bg-danger-fill'];

export function toneFor(seed = '') {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_TONES[h % AVATAR_TONES.length];
}
