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
    return localStorage.getItem(KEY) === '1';
  } catch {
    return false;
  }
}

export function rememberWaitlistJoin() {
  try {
    localStorage.setItem(KEY, '1');
  } catch {
    /* storage unavailable: the server still recorded the join */
  }
}
