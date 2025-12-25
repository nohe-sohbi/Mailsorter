import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { emailService, labelService } from '../services/api';
import EmailList from '../components/EmailList';
import '../styles/Emails.css';

function Emails() {
  const navigate = useNavigate();
  const [emails, setEmails] = useState([]);
  const [labels, setLabels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [syncing, setSyncing] = useState(false);
  const [error, setError] = useState('');
  const [successMessage, setSuccessMessage] = useState('');
  const [searchQuery, setSearchQuery] = useState('in:inbox');

  useEffect(() => {
    const userEmail = localStorage.getItem('userEmail');
    if (!userEmail) {
      navigate('/');
      return;
    }
    fetchEmails();
    fetchLabels();
  }, [navigate]);

  const fetchEmails = async (query = searchQuery) => {
    setLoading(true);
    setError('');
    try {
      const response = await emailService.getEmails(query);
      setEmails(response.data);
    } catch (err) {
      setError('Erreur lors de la récupération des emails: ' + err.message);
    } finally {
      setLoading(false);
    }
  };

  const fetchLabels = async () => {
    try {
      const response = await labelService.getLabels();
      setLabels(response.data);
    } catch (err) {
      console.error('Erreur lors de la récupération des libellés:', err);
    }
  };

  const handleSync = async () => {
    setSyncing(true);
    setError('');
    setSuccessMessage('');
    try {
      const response = await emailService.syncEmails();
      setSuccessMessage(`${response.data.synced} emails synchronisés sur ${response.data.total}`);
      fetchEmails();
      setTimeout(() => setSuccessMessage(''), 5000);
    } catch (err) {
      setError('Erreur lors de la synchronisation: ' + err.message);
    } finally {
      setSyncing(false);
    }
  };

  const handleLogout = () => {
    localStorage.removeItem('userEmail');
    localStorage.removeItem('accessToken');
    navigate('/');
  };

  const handleSearch = (e) => {
    e.preventDefault();
    fetchEmails(searchQuery);
  };

  return (
    <div className="emails-container">
      <header className="emails-header">
        <h1>Mailsorter</h1>
        <div className="header-actions">
          <button onClick={() => navigate('/settings')} className="btn-settings">
            Settings
          </button>
          <button onClick={() => navigate('/rules')} className="btn-secondary">
            Sorting Rules
          </button>
          <button onClick={handleSync} disabled={syncing} className="btn-primary">
            {syncing ? 'Syncing...' : 'Sync'}
          </button>
          <button onClick={handleLogout} className="btn-logout">
            Logout
          </button>
        </div>
      </header>

      <div className="emails-content">
        <div className="search-bar">
          <form onSubmit={handleSearch}>
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Rechercher (ex: in:inbox, from:example@gmail.com)"
              className="search-input"
            />
            <button type="submit" className="btn-search">Rechercher</button>
          </form>
        </div>

        {error && <div className="error-message">{error}</div>}
        {successMessage && <div className="success-message">{successMessage}</div>}

        {loading ? (
          <div className="loading">Chargement des emails...</div>
        ) : (
          <EmailList emails={emails} labels={labels} />
        )}
      </div>
    </div>
  );
}

export default Emails;
