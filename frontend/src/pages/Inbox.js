import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useEmails } from '../contexts/EmailContext';
import { aiService, senderService } from '../services/api';
import EmailReader from '../components/EmailReader';
import '../styles/Inbox.css';

function Inbox() {
  const navigate = useNavigate();
  const {
    emails,
    senders,
    suggestions,
    stats,
    pagination,
    loading,
    loadingMore,
    error,
    setError,
    fetchData,
    loadMoreEmails,
    removeSuggestion,
  } = useEmails();

  const [view, setView] = useState('emails');
  const [selectedEmails, setSelectedEmails] = useState([]);
  const [selectedEmail, setSelectedEmail] = useState(null);
  const [syncing, setSyncing] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [successMessage, setSuccessMessage] = useState('');
  const [searchQuery, setSearchQuery] = useState('');
  const [localSenders, setLocalSenders] = useState([]);

  useEffect(() => {
    const userEmail = localStorage.getItem('userEmail');
    if (!userEmail) {
      navigate('/');
      return;
    }
    // Fetch data (will use cache if valid)
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate]); // Only run on mount, not when fetchData changes

  useEffect(() => {
    setLocalSenders(senders);
  }, [senders]);

  const handleSync = async () => {
    setSyncing(true);
    await fetchData({ forceRefresh: true });
    setSyncing(false);
    showSuccess('Emails synchronises');
  };

  const handleSearch = (e) => {
    e.preventDefault();
    fetchData({ forceRefresh: true, query: searchQuery || 'in:inbox' });
  };

  const handleLoadMore = () => {
    loadMoreEmails(searchQuery || 'in:inbox');
  };

  const formatNumber = (num) => {
    if (!num) return '0';
    if (num >= 1000) return `${(num / 1000).toFixed(1)}k`;
    return num.toString();
  };

  const showSuccess = (msg) => {
    setSuccessMessage(msg);
    setTimeout(() => setSuccessMessage(''), 3000);
  };

  const handleSelectEmail = (email) => {
    setSelectedEmails((prev) =>
      prev.includes(email.messageId)
        ? prev.filter((id) => id !== email.messageId)
        : [...prev, email.messageId]
    );
  };

  const handleSelectAll = () => {
    if (selectedEmails.length === emails.length) {
      setSelectedEmails([]);
    } else {
      setSelectedEmails(emails.map((e) => e.messageId));
    }
  };

  const handleOpenEmail = (email) => {
    setSelectedEmail(email);
  };

  const handleCloseEmail = () => {
    setSelectedEmail(null);
  };

  const handleAnalyze = async () => {
    if (selectedEmails.length === 0) {
      setError('Selectionnez des emails a analyser');
      return;
    }

    setAnalyzing(true);
    setError('');
    try {
      const response = await aiService.analyzeEmails(selectedEmails);
      await fetchData({ forceRefresh: true });
      showSuccess(`${response.data?.length || 0} suggestions generees`);
      setSelectedEmails([]);
    } catch (err) {
      setError('Erreur lors de l\'analyse: ' + (err.response?.data || err.message));
    } finally {
      setAnalyzing(false);
    }
  };

  const handleApplySuggestion = async (suggestion) => {
    const suggestionId = suggestion.id || suggestion._id;
    try {
      await aiService.applySuggestion(suggestionId);
      removeSuggestion(suggestionId);
      showSuccess('Action appliquee');
      fetchData({ forceRefresh: true });
    } catch (err) {
      setError('Erreur: ' + err.message);
    }
  };

  const handleRejectSuggestion = async (suggestion) => {
    const suggestionId = suggestion.id || suggestion._id;
    try {
      await aiService.rejectSuggestion(suggestionId);
      removeSuggestion(suggestionId);
    } catch (err) {
      setError('Erreur lors du rejet');
    }
  };

  const handleAnalyzeSender = async (sender) => {
    setAnalyzing(true);
    try {
      await aiService.analyzeSender(sender.senderEmail);
      const sendersRes = await senderService.getSenders();
      setLocalSenders(sendersRes.data || []);
      showSuccess('Analyse terminee');
    } catch (err) {
      setError('Erreur lors de l\'analyse');
    } finally {
      setAnalyzing(false);
    }
  };

  const handleApplyBulk = async (sender, action) => {
    try {
      const response = await aiService.applyBulk(sender.senderEmail, action, sender.preference?.defaultLabel || '');
      showSuccess(`${response.data.applied} emails traites`);
      fetchData({ forceRefresh: true });
    } catch (err) {
      setError('Erreur lors du traitement');
    }
  };

  const getActionLabel = (action) => {
    const labels = { archive: 'Archiver', delete: 'Supprimer', label: 'Categoriser', keep: 'Garder' };
    return labels[action] || action;
  };

  const getActionColor = (action) => {
    const colors = { archive: '#6b7280', delete: '#dc2626', label: '#4f46e5', keep: '#16a34a' };
    return colors[action] || '#6b7280';
  };

  const getActionIcon = (action) => {
    const icons = { archive: '📥', delete: '🗑️', label: '🏷️', keep: '📌' };
    return icons[action] || '📧';
  };

  const getActionDescription = (action, labelName) => {
    switch (action) {
      case 'archive':
        return 'Archiver cet email (retirer de la boite de reception)';
      case 'delete':
        return 'Deplacer vers la corbeille';
      case 'label':
        return `Ajouter le label "${labelName || 'Non defini'}"`;
      case 'keep':
        return 'Garder dans la boite de reception';
      default:
        return action;
    }
  };

  const formatDate = (dateStr) => {
    if (!dateStr) return '';
    const date = new Date(dateStr);
    const now = new Date();
    const isToday = date.toDateString() === now.toDateString();
    if (isToday) {
      return date.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' });
    }
    return date.toLocaleDateString('fr-FR', { day: 'numeric', month: 'short' });
  };

  if (loading && emails.length === 0) {
    return (
      <div className="inbox-container">
        <div className="loading-state">
          <div className="spinner"></div>
          <p>Chargement des emails...</p>
        </div>
      </div>
    );
  }

  return (
    <div className="inbox-container">
      {/* Toolbar */}
      <div className="inbox-toolbar">
        <div className="toolbar-left">
          <div className="view-toggle">
            <button
              className={`toggle-btn ${view === 'emails' ? 'active' : ''}`}
              onClick={() => setView('emails')}
            >
              Emails
            </button>
            <button
              className={`toggle-btn ${view === 'senders' ? 'active' : ''}`}
              onClick={() => setView('senders')}
            >
              Expediteurs ({localSenders.length})
            </button>
          </div>

          {view === 'emails' && (
            <>
              <button className="btn-icon" onClick={handleSelectAll} title="Tout selectionner">
                <span>{selectedEmails.length === emails.length ? '☑' : '☐'}</span>
              </button>
              <button
                className="btn-primary"
                onClick={handleAnalyze}
                disabled={selectedEmails.length === 0 || analyzing}
              >
                {analyzing ? 'Analyse...' : `Analyser IA (${selectedEmails.length})`}
              </button>
            </>
          )}
        </div>

        <div className="toolbar-right">
          <form onSubmit={handleSearch} className="search-form">
            <input
              type="text"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Rechercher..."
              className="search-input"
            />
          </form>
          <button className="btn-icon" onClick={handleSync} disabled={syncing} title="Synchroniser">
            <span className={syncing ? 'spinning' : ''}>↻</span>
          </button>
        </div>
      </div>

      {/* Mailbox Stats */}
      {stats && (
        <div className="stats-panel">
          <div className="stat-item">
            <span className="stat-value">{formatNumber(stats.totalMessages)}</span>
            <span className="stat-label">Total</span>
          </div>
          <div className="stat-item highlight">
            <span className="stat-value">{formatNumber(stats.inboxCount)}</span>
            <span className="stat-label">Boite de reception</span>
          </div>
          <div className="stat-item unread">
            <span className="stat-value">{formatNumber(stats.unreadCount)}</span>
            <span className="stat-label">Non lus</span>
          </div>
          <div className="stat-item">
            <span className="stat-value">{formatNumber(stats.sentCount)}</span>
            <span className="stat-label">Envoyes</span>
          </div>
          <div className="stat-item">
            <span className="stat-value">{formatNumber(stats.draftCount)}</span>
            <span className="stat-label">Brouillons</span>
          </div>
          <div className="stat-item warning">
            <span className="stat-value">{formatNumber(stats.spamCount)}</span>
            <span className="stat-label">Spam</span>
          </div>
          <div className="stat-item">
            <span className="stat-value">{formatNumber(stats.trashCount)}</span>
            <span className="stat-label">Corbeille</span>
          </div>
        </div>
      )}

      {/* Messages */}
      {error && <div className="message error">{error}</div>}
      {successMessage && <div className="message success">{successMessage}</div>}

      {/* Suggestions Panel */}
      {suggestions.length > 0 && view === 'emails' && (
        <div className="suggestions-panel">
          <div className="suggestions-header">
            <span className="suggestions-title">Suggestions IA ({suggestions.length})</span>
          </div>
          <div className="suggestions-list">
            {suggestions.map((suggestion) => {
              const email = emails.find((e) => e.messageId === suggestion.emailId);
              return (
                <div key={suggestion.id || suggestion._id} className="suggestion-card">
                  <div className="suggestion-email-info">
                    <span className="suggestion-from">{email?.from?.split('<')[0]?.trim() || 'Expediteur inconnu'}</span>
                    <span className="suggestion-subject">{email?.subject || 'Sans sujet'}</span>
                  </div>
                  <div className="suggestion-details">
                    <div className="suggestion-action-box" style={{ borderColor: getActionColor(suggestion.action) }}>
                      <span className="action-icon">{getActionIcon(suggestion.action)}</span>
                      <div className="action-info">
                        <span className="action-label" style={{ color: getActionColor(suggestion.action) }}>
                          {getActionDescription(suggestion.action, suggestion.labelName)}
                        </span>
                        <span className="action-reasoning">{suggestion.reasoning}</span>
                      </div>
                      <span className="confidence-badge" title="Niveau de confiance">
                        {Math.round((suggestion.confidence || 0) * 100)}%
                      </span>
                    </div>
                  </div>
                  <div className="suggestion-actions">
                    <button
                      className="btn-apply"
                      onClick={() => handleApplySuggestion(suggestion)}
                      title={getActionDescription(suggestion.action, suggestion.labelName)}
                    >
                      Appliquer
                    </button>
                    <button
                      className="btn-reject"
                      onClick={() => handleRejectSuggestion(suggestion)}
                      title="Ignorer cette suggestion"
                    >
                      Ignorer
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Main Content */}
      <div className={`inbox-content ${selectedEmail ? 'with-reader' : ''}`}>
        {view === 'emails' ? (
          <div className="email-list">
            <div className="email-list-header">
              <span>{emails.length} emails affiches</span>
              {pagination.resultSizeEstimate > 0 && (
                <span className="estimate">sur ~{formatNumber(pagination.resultSizeEstimate)} resultats</span>
              )}
            </div>
            {emails.map((email) => (
              <div
                key={email.messageId}
                className={`email-row ${selectedEmails.includes(email.messageId) ? 'selected' : ''} ${!email.isRead ? 'unread' : ''} ${selectedEmail?.messageId === email.messageId ? 'active' : ''}`}
              >
                <input
                  type="checkbox"
                  checked={selectedEmails.includes(email.messageId)}
                  onChange={() => handleSelectEmail(email)}
                  onClick={(e) => e.stopPropagation()}
                />
                <div className="email-main" onClick={() => handleOpenEmail(email)}>
                  <div className="email-from">{email.from?.split('<')[0]?.trim() || email.from}</div>
                  <div className="email-subject">{email.subject}</div>
                  <div className="email-snippet">{email.snippet}</div>
                </div>
                <div className="email-date">{formatDate(email.receivedDate)}</div>
              </div>
            ))}
            {pagination.nextPageToken && (
              <div className="load-more-container">
                <button
                  className="btn-load-more"
                  onClick={handleLoadMore}
                  disabled={loadingMore}
                >
                  {loadingMore ? 'Chargement...' : 'Charger plus d\'emails'}
                </button>
              </div>
            )}
          </div>
        ) : (
          <div className="sender-list">
            {localSenders.map((sender) => (
              <div key={sender.senderEmail} className="sender-row">
                <div className="sender-info">
                  <div className="sender-name">{sender.senderName || sender.senderEmail.split('@')[0]}</div>
                  <div className="sender-email">{sender.senderEmail}</div>
                  <div className="sender-count">{sender.emailCount} emails</div>
                </div>
                {sender.preference ? (
                  <div className="sender-preference">
                    <span
                      className="preference-badge"
                      style={{ background: getActionColor(sender.preference.defaultAction) }}
                    >
                      {getActionLabel(sender.preference.defaultAction)}
                    </span>
                  </div>
                ) : (
                  <button
                    className="btn-secondary"
                    onClick={() => handleAnalyzeSender(sender)}
                    disabled={analyzing}
                  >
                    Analyser
                  </button>
                )}
                <div className="sender-actions">
                  <button className="btn-action" onClick={() => handleApplyBulk(sender, 'archive')}>
                    Archiver tout
                  </button>
                  <button className="btn-action danger" onClick={() => handleApplyBulk(sender, 'delete')}>
                    Supprimer tout
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Email Reader Panel */}
        {selectedEmail && (
          <EmailReader email={selectedEmail} onClose={handleCloseEmail} />
        )}
      </div>
    </div>
  );
}

export default Inbox;
