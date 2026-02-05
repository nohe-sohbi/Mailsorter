import axios from 'axios';

const API_URL = process.env.REACT_APP_API_URL || 'http://localhost:8080';

const apiClient = axios.create({
  baseURL: API_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Add user email to requests if available
apiClient.interceptors.request.use((config) => {
  const userEmail = localStorage.getItem('userEmail');
  if (userEmail) {
    config.headers['X-User-Email'] = userEmail;
  }
  return config;
});

export const authService = {
  getAuthUrl: () => apiClient.get('/api/auth/url'),
  handleCallback: (code) => apiClient.get(`/api/auth/callback?code=${code}`),
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
  getStats: () => apiClient.get('/api/stats'),
};

export const labelService = {
  getLabels: () => apiClient.get('/api/labels'),
};

export const aiService = {
  analyzeEmails: (emailIds) => apiClient.post('/api/ai/analyze', { emailIds }),
  analyzeSender: (senderEmail) => apiClient.post('/api/ai/analyze-sender', { senderEmail }),
  applySuggestion: (suggestionId) => apiClient.post('/api/ai/apply', { suggestionId }),
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

export const configService = {
  getStatus: () => apiClient.get('/api/config/status'),
  getGmailConfig: () => apiClient.get('/api/config/gmail'),
  saveGmailConfig: (config) => apiClient.post('/api/config/gmail', config),
};

export default apiClient;
