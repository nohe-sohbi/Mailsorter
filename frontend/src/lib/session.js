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
