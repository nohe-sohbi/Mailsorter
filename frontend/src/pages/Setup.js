import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { configService } from '../services/api';
import '../styles/Setup.css';

function Setup({ onComplete }) {
  const navigate = useNavigate();
  const [formData, setFormData] = useState({
    clientId: '',
    clientSecret: '',
    redirectUrl: 'http://localhost:3000/auth/callback',
  });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleChange = (e) => {
    setFormData({ ...formData, [e.target.name]: e.target.value });
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setLoading(true);
    setError('');

    try {
      await configService.saveGmailConfig(formData);
      if (onComplete) {
        onComplete();
      }
      navigate('/');
    } catch (err) {
      setError(err.response?.data || 'Failed to save configuration');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="setup-container">
      <div className="setup-card">
        <h1>Mailsorter Setup</h1>
        <p>Configure your Gmail API credentials to get started</p>

        {error && <div className="error-message">{error}</div>}

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
            <label htmlFor="clientSecret">Client Secret</label>
            <input
              type="password"
              id="clientSecret"
              name="clientSecret"
              value={formData.clientSecret}
              onChange={handleChange}
              required
              placeholder="Your Gmail API Client Secret"
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
            <span className="field-hint">This must match the redirect URI in Google Cloud Console</span>
          </div>

          <div className="setup-help">
            <h3>How to get credentials:</h3>
            <ol>
              <li>Go to <a href="https://console.cloud.google.com/" target="_blank" rel="noopener noreferrer">Google Cloud Console</a></li>
              <li>Create a new project or select an existing one</li>
              <li>Enable the Gmail API in "APIs & Services"</li>
              <li>Go to "Credentials" and create OAuth 2.0 Client ID</li>
              <li>Add the redirect URL above to "Authorized redirect URIs"</li>
              <li>Copy the Client ID and Client Secret here</li>
            </ol>
          </div>

          <button type="submit" disabled={loading} className="setup-button">
            {loading ? 'Saving...' : 'Save and Continue'}
          </button>
        </form>
      </div>
    </div>
  );
}

export default Setup;
