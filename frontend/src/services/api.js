import axios from 'axios';

const API_URL = process.env.REACT_APP_API_URL || 'http://localhost:8080';

const apiClient = axios.create({
  baseURL: API_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Authenticate requests with the signed session token issued at login.
// The server derives the user identity from this token, so we no longer send a
// (spoofable) X-User-Email header.
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('accessToken');
  if (token) {
    config.headers['Authorization'] = `Bearer ${token}`;
  }
  return config;
});

// On 401 the session is missing/expired: clear it and bounce to login.
//
// Requests flagged `optional` opt out. The backend answers 401 both for a dead
// Mailsorter session and for a revoked Gmail grant, so a purely cosmetic call
// (fetching label names) taking the second kind would log the user out of a page
// they were merely visiting. The failure is handled locally instead.
apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401 && !error.config?.optional) {
      localStorage.removeItem('accessToken');
      localStorage.removeItem('userEmail');
      if (window.location.pathname !== '/') {
        window.location.assign('/');
      }
    }
    return Promise.reject(error);
  }
);

// The API answers errors in two shapes: writeError produces {error, status},
// while the older handlers use http.Error and produce bare text. Reading only
// one of them silently discarded the real reason and left the UI showing a
// generic "Réessayez", and `data?.trim()` on a JSON object throws outright.
export function apiError(err, fallback = 'Une erreur est survenue.') {
  const data = err?.response?.data;
  if (typeof data === 'string' && data.trim()) return data.trim();
  if (data && typeof data.error === 'string' && data.error.trim()) return data.error.trim();
  if (err?.code === 'ERR_NETWORK') return 'Connexion au serveur impossible.';
  return fallback;
}

export const authService = {
  // reconnect forces Google's consent screen. Google only hands back a refresh
  // token on a first authorization or when consent is re-granted, so repairing a
  // revoked grant without it produces an access token that expires in an hour
  // and nothing to renew it with.
  getAuthUrl: ({ reconnect = false } = {}) =>
    apiClient.get(`/api/auth/url${reconnect ? '?reconnect=1' : ''}`),
  handleCallback: (code, state) => {
    const params = new URLSearchParams({ code });
    if (state) params.set('state', state);
    return apiClient.get(`/api/auth/callback?${params.toString()}`);
  },
  // Signing in with a mailbox, which is also how an account is created: there
  // is no separate sign-up call because there is no password of ours to set.
  // The mail server is what proves the identity, so the first request and every
  // one after it are the same request.
  signInWithMailbox: (address, password) =>
    apiClient.post('/api/auth/mailbox', { address, password }),
};

export const emailService = {
  getEmails: (query = '', options = {}) => {
    const params = new URLSearchParams();
    if (query) params.set('q', query);
    if (options.maxResults) params.set('maxResults', options.maxResults);
    if (options.pageToken) params.set('pageToken', options.pageToken);
    const queryString = params.toString();
    return apiClient.get(`/api/emails${queryString ? `?${queryString}` : ''}`);
  },
  // One message with its decoded body. The list endpoint omits bodies on
  // purpose, so this is what makes the reader able to show an email at all.
  // markRead mirrors what opening an email means everywhere else.
  getEmail: (messageId, { markRead = false } = {}) =>
    apiClient.get(`/api/emails/${encodeURIComponent(messageId)}${markRead ? '?markRead=1' : ''}`),
  // The attachment bytes, as a Blob. Gmail keeps them behind a second call, and
  // the route needs the session header, so a plain <a href> cannot fetch them:
  // the reader downloads through axios and hands the browser an object URL.
  downloadAttachment: (messageId, attachmentId) =>
    apiClient.get(
      `/api/emails/${encodeURIComponent(messageId)}/attachments/${encodeURIComponent(attachmentId)}`,
      { responseType: 'blob' }
    ),
  syncEmails: () => apiClient.post('/api/emails/sync'),
  action: (messageId, action) => apiClient.post('/api/emails/action', { messageId, action }),
  // One action over a whole selection, server-side: N Gmail mutations behind a
  // single request, with the protected-sender shield applied per message.
  batchAction: (messageIds, action, labelName = '') =>
    apiClient.post('/api/emails/batch-action', { messageIds, action, labelName }),
  // Undoing a labelling needs the label back: the server holds the id, but only
  // the caller knows which name it just applied.
  batchUndo: (messageIds, action, labelName = '') =>
    apiClient.post('/api/emails/batch-undo', { messageIds, action, labelName }),
  getStats: () => apiClient.get('/api/stats'),
  // Snooze: pull a message out of the inbox until a preset OR an explicit
  // instant. The two are exclusive server-side (a wakeAt short-circuits the
  // preset), so only the one the user actually chose is sent.
  snooze: (messageId, { preset = '', wakeAt = '' } = {}) =>
    apiClient.post('/api/emails/snooze', wakeAt ? { messageId, wakeAt } : { messageId, preset }),
  // The same, over a whole selection: one request, N messages, one wake time.
  batchSnooze: (messageIds, { preset = '', wakeAt = '' } = {}) =>
    apiClient.post('/api/emails/batch-snooze', wakeAt ? { messageIds, wakeAt } : { messageIds, preset }),
};

// The user's real Gmail labels. The rules editor used to ask people to type a
// label name blind (a typo silently created a second, near-identical label) and
// the reader rendered raw ids like "Label_1234567".
export const labelService = {
  // `optional`: label names are an enhancement everywhere they are used (the
  // rules picker, the reader's chips). Every caller degrades gracefully, so a
  // failure here must never cost the user their session.
  list: () => apiClient.get('/api/labels', { optional: true }),
};

// The user's own Gmail queries, kept as one-click filters. The six built-in
// quick filters cover the generic cases; these are the ones only this person
// needs, and retyping them was the reason the search box was used once.
export const searchService = {
  list: () => apiClient.get('/api/searches'),
  save: (name, query) => apiClient.post('/api/searches', { name, query }),
  remove: (id) => apiClient.delete(`/api/searches/${id}`),
  // Records a click so the bar orders itself by what actually gets used. Never
  // blocks the search it is counting.
  markUsed: (id) => apiClient.post(`/api/searches/${id}/use`),
};

export const snoozeService = {
  list: (status = 'scheduled') => apiClient.get(`/api/snoozes?status=${status}`),
  wake: (id) => apiClient.post(`/api/snoozes/${id}/wake`),
};

export const protectService = {
  list: () => apiClient.get('/api/protected'),
  add: (value, note = '') => apiClient.post('/api/protected', { value, note }),
  remove: (id) => apiClient.delete(`/api/protected/${id}`),
};

export const aiService = {
  analyzeEmails: (emailIds) => apiClient.post('/api/ai/analyze', { emailIds }),
  analyzeAsync: (emailIds) => apiClient.post('/api/ai/analyze-async', { emailIds }),
  getJob: (jobId) => apiClient.get(`/api/ai/jobs/${jobId}`),
  analyzeSender: (senderEmail) => apiClient.post('/api/ai/analyze-sender', { senderEmail }),
  applySuggestion: (suggestionId) => apiClient.post('/api/ai/apply', { suggestionId }),
  applyBatch: (suggestionIds) => apiClient.post('/api/ai/apply-batch', { suggestionIds }),
  applyBulk: (senderEmail, action, labelName) =>
    apiClient.post('/api/ai/apply-bulk', { senderEmail, action, labelName }),
  getSuggestions: (status = 'pending') => apiClient.get(`/api/ai/suggestions?status=${status}`),
  rejectSuggestion: (id) => apiClient.post(`/api/ai/suggestions/${id}/reject`),
};

export const senderService = {
  getSenders: () => apiClient.get('/api/senders'),
  updatePreference: (id, preference) => apiClient.put(`/api/senders/${id}/preferences`, preference),
  // Turn a sender into a permanent deterministic rule (learn once, apply forever).
  createRule: (senderEmail, action, labelName = '') =>
    apiClient.post('/api/senders/rule', { senderEmail, action, labelName }),
};

export const accountService = {
  // Redacted account record (no OAuth token, no Stripe id) for the profile page.
  getProfile: () => apiClient.get('/api/account/profile'),
  getUsage: () => apiClient.get('/api/usage'),
  getActivity: () => apiClient.get('/api/stats/activity'),
  getSettings: () => apiClient.get('/api/account/settings'),
  updateSettings: (settings) => apiClient.put('/api/account/settings', settings),
  // The exact recap the daily digest would carry (subject + text + HTML), and
  // an immediate send of it. Together they answer "what will I receive, and
  // does delivery actually work", which used to take a day to find out.
  getDigestPreview: () => apiClient.get('/api/stats/digest'),
  sendTestDigest: () => apiClient.post('/api/account/digest/test'),
  // Action history (audit trail) + one-click undo of an automated action.
  getActionLog: (params = {}) => {
    const qs = new URLSearchParams();
    if (params.source) qs.set('source', params.source);
    if (params.limit) qs.set('limit', params.limit);
    // Cursor from the previous page's `nextBefore`, and free-text search over
    // the subject/sender of the acted-on message.
    if (params.before) qs.set('before', params.before);
    if (params.q) qs.set('q', params.q);
    const s = qs.toString();
    return apiClient.get(`/api/activity/log${s ? `?${s}` : ''}`);
  },
  undoAction: (id) => apiClient.post('/api/activity/undo', { id }),
  // RGPD: export everything Mailsorter stores about the user, and erase it.
  exportData: () => apiClient.get('/api/account/export'),
  deleteAccount: () => apiClient.delete('/api/account'),
};

export const subscriptionService = {
  getSubscriptions: () => apiClient.get('/api/subscriptions'),
  unsubscribe: (messageId, alsoArchive = false) =>
    apiClient.post('/api/unsubscribe', { messageId, alsoArchive }),
};

export const billingService = {
  checkout: () => apiClient.post('/api/billing/checkout'),
  portal: () => apiClient.post('/api/billing/portal'),
};

export const ruleService = {
  getRules: () => apiClient.get('/api/rules'),
  createRule: (rule) => apiClient.post('/api/rules', rule),
  updateRule: (id, rule) => apiClient.put(`/api/rules/${id}`, rule),
  deleteRule: (id) => apiClient.delete(`/api/rules/${id}`),
  apply: () => apiClient.post('/api/rules/apply'),
  // Dry run: report what the rules WOULD do, without touching Gmail.
  preview: () => apiClient.post('/api/rules/preview'),
  // Order is the engine's semantics (first match wins), so it is edited as a
  // whole list rather than one number at a time.
  reorder: (ids) => apiClient.put('/api/rules/reorder', { ids }),
  duplicate: (id) => apiClient.post(`/api/rules/${id}/duplicate`),
  // Backup / move a ruleset. The export carries intent only (no ids, no owner,
  // no counters), which is what makes importing it elsewhere safe.
  exportRules: () => apiClient.get('/api/rules/export'),
  importRules: (doc) => apiClient.post('/api/rules/import', doc),
};

// The Gmail credentials are an instance-wide OAuth app set through environment
// variables, so there is nothing to read or write here: only a boot probe that
// tells the app whether the deployment is wired up and whether Pro can be
// bought yet ({ isConfigured, billingOn }).
export const configService = {
  getStatus: () => apiClient.get('/api/config/status'),
  // The mailbox catalog for the running edition. The connect screen renders
  // whatever this returns: no provider, hostname or help text is hardcoded in
  // the SPA, so it can never offer a provider the backend cannot reach.
  getProviders: () => apiClient.get('/api/providers'),
};

// The mailbox connected over IMAP, which is how the hosted edition reaches mail
// at all. connect() sends an address and a password and nothing else: the
// provider, host, port and TLS mode are resolved server-side from the address,
// so the screen never picks the host the server connects to. The response never
// carries the password back.
export const mailboxService = {
  get: () => apiClient.get('/api/mailbox'),
  connect: (address, password) => apiClient.post('/api/mailbox/connect', { address, password }),
  disconnect: () => apiClient.delete('/api/mailbox'),
};

// Pro waitlist. Public on purpose: the people worth measuring are the ones who
// do not have an account yet.
export const waitlistService = {
  join: (email, source = 'pricing') => apiClient.post('/api/waitlist', { email, source }),
};

export default apiClient;
