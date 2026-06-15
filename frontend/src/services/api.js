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
apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('accessToken');
      localStorage.removeItem('userEmail');
      if (window.location.pathname !== '/') {
        window.location.assign('/');
      }
    }
    return Promise.reject(error);
  }
);

export const authService = {
  getAuthUrl: () => apiClient.get('/api/auth/url'),
  handleCallback: (code, state) => {
    const params = new URLSearchParams({ code });
    if (state) params.set('state', state);
    return apiClient.get(`/api/auth/callback?${params.toString()}`);
  },
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
  syncEmails: () => apiClient.post('/api/emails/sync'),
  action: (messageId, action) => apiClient.post('/api/emails/action', { messageId, action }),
  getStats: () => apiClient.get('/api/stats'),
};

export const labelService = {
  getLabels: () => apiClient.get('/api/labels'),
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
};

export const smartLabelService = {
  getLabels: () => apiClient.get('/api/smart-labels'),
  createLabel: (label) => apiClient.post('/api/smart-labels', label),
};

export const accountService = {
  getUsage: () => apiClient.get('/api/usage'),
  getActivity: () => apiClient.get('/api/stats/activity'),
};

export const subscriptionService = {
  getSubscriptions: () => apiClient.get('/api/subscriptions'),
  unsubscribe: (messageId, alsoArchive = false) =>
    apiClient.post('/api/unsubscribe', { messageId, alsoArchive }),
};

export const billingService = {
  checkout: () => apiClient.post('/api/billing/checkout'),
};

export const configService = {
  getStatus: () => apiClient.get('/api/config/status'),
  getGmailConfig: () => apiClient.get('/api/config/gmail'),
  saveGmailConfig: (config) => apiClient.post('/api/config/gmail', config),
};

export default apiClient;
