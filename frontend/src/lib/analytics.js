// Umami analytics, self-hosted on analytics.sohbi.dev.
//
// The tracker is injected once at boot rather than hardcoded in index.html: the
// website id is a build-time variable, and a `%REACT_APP_UMAMI_WEBSITE_ID%`
// placeholder left unsubstituted would ship a literal string as the id. Here,
// no id simply means no tracking, which is what we want in local development.

const WEBSITE_ID = process.env.REACT_APP_UMAMI_WEBSITE_ID;
const SRC = process.env.REACT_APP_UMAMI_SRC || 'https://analytics.sohbi.dev/script.js';
const DOMAINS = process.env.REACT_APP_UMAMI_DOMAINS || 'mailsorter.sohbi.dev';

let injected = false;

// initAnalytics loads the tracker exactly once. Call it from the app entry
// point, never from a route or a component that can remount.
export function initAnalytics() {
  if (!WEBSITE_ID || injected || typeof document === 'undefined') return;
  injected = true;

  const script = document.createElement('script');
  script.defer = true;
  script.src = SRC;
  script.dataset.websiteId = WEBSITE_ID;
  // Restricts collection to the production host, so `npm start` and any preview
  // build stay out of the stats without needing a separate site.
  script.dataset.domains = DOMAINS;
  // Auto-track hooks the History API, so react-router navigations are counted
  // on their own. Never add a manual pageview call: it would double-count.
  document.head.appendChild(script);
}

// track reports a user action. Properties must stay free of personal data: no
// email address, no sender, no subject line. Counts and enum-like labels only.
export function track(event, props) {
  if (!WEBSITE_ID || typeof window === 'undefined') return;
  // The optional chaining is load-bearing: an ad-blocker can drop the script
  // while WEBSITE_ID is set, so `window.umami` may never appear.
  window.umami?.track(event, props);
}
