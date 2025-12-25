import React from 'react';

function RuleList({ rules, onEdit, onDelete }) {
  if (!rules || rules.length === 0) {
    return <div className="no-rules">Aucune règle créée. Créez votre première règle de tri !</div>;
  }

  return (
    <div className="rule-list">
      {rules.map((rule) => (
        <div key={rule.id} className={`rule-item ${!rule.enabled ? 'disabled' : ''}`}>
          <div className="rule-header">
            <h3>{rule.name}</h3>
            <div className="rule-actions">
              <button onClick={() => onEdit(rule)} className="btn-edit">
                Modifier
              </button>
              <button onClick={() => onDelete(rule.id)} className="btn-delete">
                Supprimer
              </button>
            </div>
          </div>
          
          {rule.description && (
            <p className="rule-description">{rule.description}</p>
          )}

          <div className="rule-details">
            <div className="rule-section">
              <h4>Conditions :</h4>
              <ul>
                {rule.conditions.map((condition, index) => (
                  <li key={index}>
                    {getConditionText(condition)}
                  </li>
                ))}
              </ul>
            </div>

            <div className="rule-section">
              <h4>Actions :</h4>
              <ul>
                {rule.actions.map((action, index) => (
                  <li key={index}>
                    {getActionText(action)}
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <div className="rule-footer">
            <span className="rule-priority">Priorité: {rule.priority}</span>
            <span className={`rule-status ${rule.enabled ? 'enabled' : 'disabled'}`}>
              {rule.enabled ? 'Activée' : 'Désactivée'}
            </span>
          </div>
        </div>
      ))}
    </div>
  );
}

function getConditionText(condition) {
  const fieldNames = {
    from: 'De',
    to: 'À',
    subject: 'Objet',
    body: 'Corps',
  };

  const operatorNames = {
    contains: 'contient',
    equals: 'égal à',
    startsWith: 'commence par',
    endsWith: 'finit par',
  };

  return `${fieldNames[condition.field]} ${operatorNames[condition.operator]} "${condition.value}"`;
}

function getActionText(action) {
  const actionNames = {
    addLabel: 'Ajouter le libellé',
    removeLabel: 'Retirer le libellé',
    markAsRead: 'Marquer comme lu',
    archive: 'Archiver',
  };

  if (action.type === 'addLabel' || action.type === 'removeLabel') {
    return `${actionNames[action.type]} "${action.value}"`;
  }
  return actionNames[action.type];
}

export default RuleList;
