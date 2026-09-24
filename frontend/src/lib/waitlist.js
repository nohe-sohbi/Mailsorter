// Whether THIS browser already joined the Pro waitlist.
//
// The server is the real record: joining again from another device is an
// idempotent upsert keyed by email, not a duplicate. This flag only decides
// which state the page shows, so it must never throw (private browsing, blocked
// site data) and it is shared by the landing and the pricing page: signing up
// on one is signing up on the other, and showing the form again after a
// successful join reads as a failure.

const KEY = 'mailsorter_pro_waitlist';

export function hasJoinedWaitlist() {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return false;
    if (raw === '1') return true;
    const parsed = JSON.parse(raw);
    return Boolean(parsed?.joined);
  } catch {
    return false;
  }
}

export function waitlistEmail() {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw || raw === '1') return '';
    const parsed = JSON.parse(raw);
    return typeof parsed?.email === 'string' ? parsed.email : '';
  } catch {
    return '';
  }
}

export function rememberWaitlistJoin(email = '') {
  try {
    localStorage.setItem(KEY, JSON.stringify({ joined: true, email: (email || '').trim() }));
  } catch {
    /* storage unavailable: the server still recorded the join */
  }
}

export function forgetWaitlistJoin() {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* storage unavailable */
  }
}
