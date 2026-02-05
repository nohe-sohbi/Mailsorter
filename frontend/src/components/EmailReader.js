import React from 'react';
import '../styles/EmailReader.css';

function EmailReader({ email, onClose }) {
  if (!email) return null;

  const formatDate = (dateStr) => {
    if (!dateStr) return '';
    const date = new Date(dateStr);
    return date.toLocaleDateString('fr-FR', {
      weekday: 'long',
      day: 'numeric',
      month: 'long',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    });
  };

  const extractEmail = (from) => {
    const match = from?.match(/<(.+)>/);
    return match ? match[1] : from;
  };

  const extractName = (from) => {
    const match = from?.match(/^(.+?)\s*</);
    return match ? match[1].replace(/"/g, '').trim() : from;
  };

  return (
    <div className="email-reader">
      <div className="reader-header">
        <button className="btn-close" onClick={onClose}>
          ✕
        </button>
        <div className="reader-actions">
          <button className="btn-action" title="Archiver">📥</button>
          <button className="btn-action" title="Supprimer">🗑</button>
          <button className="btn-action" title="Marquer non lu">📧</button>
        </div>
      </div>

      <div className="reader-content">
        <h2 className="email-subject">{email.subject || '(Sans sujet)'}</h2>

        <div className="email-meta">
          <div className="sender-avatar">
            {extractName(email.from)?.[0]?.toUpperCase() || '?'}
          </div>
          <div className="sender-details">
            <div className="sender-name">{extractName(email.from)}</div>
            <div className="sender-email">{extractEmail(email.from)}</div>
          </div>
          <div className="email-date">{formatDate(email.receivedDate)}</div>
        </div>

        {email.to && (
          <div className="email-to">
            <span className="label">A:</span> {email.to}
          </div>
        )}

        <div className="email-body">
          {email.body ? (
            <div
              className="body-content"
              dangerouslySetInnerHTML={{ __html: sanitizeHtml(email.body) }}
            />
          ) : (
            <div className="body-snippet">
              <p>{email.snippet}</p>
              <p className="snippet-notice">
                Contenu complet non disponible. Synchronisez les emails pour charger le contenu.
              </p>
            </div>
          )}
        </div>

        {email.labelIds && email.labelIds.length > 0 && (
          <div className="email-labels">
            {email.labelIds.map((label) => (
              <span key={label} className="label-tag">
                {label.replace('Label_', '').replace('CATEGORY_', '')}
              </span>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// Basic HTML sanitization (in production, use a library like DOMPurify)
function sanitizeHtml(html) {
  if (!html) return '';

  // Remove script tags and event handlers
    return html
      .replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, '')
      .replace(/on\w+="[^"]*"/gi, '')
      .replace(/on\w+='[^']*'/gi, '');
}

export default EmailReader;
