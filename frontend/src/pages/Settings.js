import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { configService } from '../services/api';
import '../styles/Settings.css';

function Settings() {
  const navigate = useNavigate();
  const [formData, setFormData] = useState({
    clientId: '',
    clientSecret: '',
    redirectUrl: '',
  });
  const [originalData, setOriginalData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    fetchConfig();
  }, []);

  const fetchConfig = async () => {
    try {
      const response = await configService.getGmailConfig();
      const data = response.data;
      setFormData({
        clientId: data.clientId || '',
        clientSecret: '',
        redirectUrl: data.redirectUrl || 'http://localhost:3000/auth/callback',
      });
      setOriginalData(data);
    } catch (err) {
      setError('Failed to load configuration');
    } finally {
      setLoading(false);
    }
  };

  const handleChange = (e) => {
    setFormData({ ...formData, [e.target.name]: e.target.value });
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setSaving(true);
    setError('');
    setSuccess('');

    const payload = {
      clientId: formData.clientId,
      redirectUrl: formData.redirectUrl,
    };

    if (formData.clientSecret) {
      payload.clientSecret = formData.clientSecret;
    }

    try {
      await configService.saveGmailConfig(payload);
      setSuccess('Configuration saved successfully');
      setFormData({ ...formData, clientSecret: '' });
      setOriginalData({ ...originalData, clientId: formData.clientId, redirectUrl: formData.redirectUrl, isConfigured: true });
      setTimeout(() => setSuccess(''), 3000);
    } catch (err) {
      setError(err.response?.data || 'Failed to save configuration');
    } finally {
      setSaving(false);
    }
  };

  const handleLogout = () => {
    localStorage.removeItem('userEmail');
    localStorage.removeItem('accessToken');
    navigate('/');
  };

  if (loading) {
    return (
      <div className="settings-container">
        <div className="loading">Loading configuration...</div>
      </div>
    );
  }

  return (
    <div className="settings-container">
      <header className="settings-header">
        <h1>Settings</h1>
        <div className="header-actions">
          <button onClick={() => navigate('/emails')} className="btn-secondary">
            Back to Emails
          </button>
          <button onClick={handleLogout} className="btn-logout">
            Logout
          </button>
        </div>
      </header>

      <div className="settings-content">
        <div className="settings-card">
          <h2>Gmail API Configuration</h2>

          {error && <div className="error-message">{error}</div>}
          {success && <div className="success-message">{success}</div>}

          <form onSubmit={handleSubmit}>
            <div className="form-group">
              <label htmlFor="clientId">Client ID</label>
              <input
                type="text"
                id="clientId"
                name="clientId"
                value={formData.clientId}
                onChange={handleChange}
                required
                placeholder="Your Gmail API Client ID"
              />
            </div>

            <div className="form-group">
              <label htmlFor="clientSecret">
                Client Secret
                {originalData?.isConfigured && (
                  <span className="secret-hint"> (leave empty to keep current)</span>
                )}
              </label>
              <input
                type="password"
                id="clientSecret"
                name="clientSecret"
                value={formData.clientSecret}
                onChange={handleChange}
                placeholder={originalData?.clientSecret || 'Enter new secret'}
              />
            </div>

            <div className="form-group">
              <label htmlFor="redirectUrl">Redirect URL</label>
              <input
                type="text"
                id="redirectUrl"
                name="redirectUrl"
                value={formData.redirectUrl}
                onChange={handleChange}
                required
              />
            </div>

            <div className="form-actions">
              <button type="submit" disabled={saving} className="btn-primary">
                {saving ? 'Saving...' : 'Update Configuration'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}

export default Settings;
