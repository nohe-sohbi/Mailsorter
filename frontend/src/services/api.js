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
  getEmails: (query = '') => apiClient.get(`/api/emails${query ? `?q=${query}` : ''}`),
  syncEmails: () => apiClient.post('/api/emails/sync'),
};

export const ruleService = {
  getRules: () => apiClient.get('/api/rules'),
  createRule: (rule) => apiClient.post('/api/rules', rule),
  updateRule: (id, rule) => apiClient.put(`/api/rules/${id}`, rule),
  deleteRule: (id) => apiClient.delete(`/api/rules/${id}`),
};

export const labelService = {
  getLabels: () => apiClient.get('/api/labels'),
};

export default apiClient;
