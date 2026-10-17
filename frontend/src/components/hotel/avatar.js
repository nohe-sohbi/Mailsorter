// The hotel's avatars: initials in ink on a coloured disc, picked from the
// address so a sender keeps its colour across screens. The classic theme
// picks one of its own fills (see the AVATAR_TONES of Inbox and EmailReader);
// this is the hotel's counterpart, used only when the theme is on.
const TONES = ['hd-av', 'hd-av hd-av--gold', 'hd-av hd-av--teal', 'hd-av hd-av--sunk', 'hd-av hd-av--plum'];

export function hotelTone(seed = '') {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return TONES[h % TONES.length];
}

// Up to two initials: "Le Monde" reads LM, "noreply@github.com" reads N.
export function initials(name = '') {
  const words = name
    .replace(/["<>]/g, ' ')
    .split(/[\s@._-]+/)
    .filter((w) => /^[\p{L}\p{N}]/u.test(w));
  if (words.length === 0) return '?';
  if (words.length === 1 || name.includes('@')) return words[0][0].toUpperCase();
  return (words[0][0] + words[1][0]).toUpperCase();
}
