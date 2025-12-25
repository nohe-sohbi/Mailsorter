import React from 'react';

function EmailList({ emails, labels }) {
  const getLabelName = (labelId) => {
    const label = labels.find(l => l.id === labelId);
    return label ? label.name : labelId;
  };

  const formatDate = (dateString) => {
    const date = new Date(dateString);
    return date.toLocaleDateString('fr-FR', {
      day: '2-digit',
      month: 'short',
      hour: '2-digit',
      minute: '2-digit'
    });
  };

  if (!emails || emails.length === 0) {
    return <div className="no-emails">Aucun email trouvé</div>;
  }

  return (
    <div className="email-list">
      {emails.map((email) => (
        <div key={email.messageId} className={`email-item ${!email.isRead ? 'unread' : ''}`}>
          <div className="email-header">
            <span className="email-from">{email.from}</span>
            <span className="email-date">{formatDate(email.receivedDate)}</span>
          </div>
          <div className="email-subject">{email.subject}</div>
          <div className="email-snippet">{email.snippet}</div>
          {email.labelIds && email.labelIds.length > 0 && (
            <div className="email-labels">
              {email.labelIds
                .filter(id => !id.startsWith('CATEGORY_') && id !== 'UNREAD')
                .map(labelId => (
                  <span key={labelId} className="label-badge">
                    {getLabelName(labelId)}
                  </span>
                ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

export default EmailList;
