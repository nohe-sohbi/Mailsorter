// Single source of truth for whether the browser holds an active session.
//
// A session requires both the user email and the access token: checking only
// one leaves windows where a partially cleared state (e.g. 401 recovery or
// interrupted sign-out) redirects back and forth between login and authenticated
// screens.

export function isAuthed() {
  try {
    return Boolean(localStorage.getItem('userEmail') && localStorage.getItem('accessToken'));
  } catch {
    return false;
  }
}

// The address the browser is signed in as, or '' when nobody is. EmailProvider
// is keyed on it (App.js): the inbox it caches belongs to one person, and a key
// that changes is what throws that cache away when another person signs in.
export function sessionOwner() {
  try {
    return (localStorage.getItem('accessToken') && localStorage.getItem('userEmail')) || '';
  } catch {
    return '';
  }
}

// The mailbox the screen loaded (the `generation` GET /api/mailbox answers),
// which api.js sends with every request (X-Mailbox-Generation): the server
// refuses with a 409 a request from a screen loaded on another mailbox than
// the one connected now. Set by EmailContext only, which empties what it holds
// of the mailbox when it changes; kept in memory, so a page loaded again asks
// again.
let generation = '';

export function mailboxGeneration() {
  return generation;
}

export function setMailboxGeneration(value) {
  generation = value || '';
}

// Ends the session in this browser. One function for the three ways out (the
// header's button, a deleted account, a 401 from the API), because the three
// copies it replaces had already drifted: none of them cleared hasMailbox,
// which the next person signing in on that browser then inherited. The
// mailbox the screen loaded goes too: it was that person's.
export function signOut() {
  generation = '';
  try {
    localStorage.removeItem('accessToken');
    localStorage.removeItem('userEmail');
    localStorage.removeItem('hasMailbox');
  } catch {
    // Storage blocked: there is nothing stored to forget.
  }
}
