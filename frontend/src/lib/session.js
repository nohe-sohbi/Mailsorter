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

// Ends the session in this browser. One function for the three ways out (the
// header's button, a deleted account, a 401 from the API), because the three
// copies it replaces had already drifted: none of them cleared hasMailbox,
// which the next person signing in on that browser then inherited.
export function signOut() {
  try {
    localStorage.removeItem('accessToken');
    localStorage.removeItem('userEmail');
    localStorage.removeItem('hasMailbox');
  } catch {
    // Storage blocked: there is nothing stored to forget.
  }
}
