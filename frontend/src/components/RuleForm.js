import React, { useState, useEffect } from 'react';

function RuleForm({ rule, onSave, onCancel }) {
  const [formData, setFormData] = useState({
    name: '',
    description: '',
    conditions: [{ field: 'from', operator: 'contains', value: '' }],
    actions: [{ type: 'addLabel', value: '' }],
    priority: 1,
    enabled: true,
  });

  useEffect(() => {
    if (rule) {
      setFormData(rule);
    }
  }, [rule]);

  const handleChange = (e) => {
    const { name, value, type, checked } = e.target;
    setFormData({
      ...formData,
      [name]: type === 'checkbox' ? checked : value,
    });
  };

  const handleConditionChange = (index, field, value) => {
    const newConditions = [...formData.conditions];
    newConditions[index][field] = value;
    setFormData({ ...formData, conditions: newConditions });
  };

  const addCondition = () => {
    setFormData({
      ...formData,
      conditions: [...formData.conditions, { field: 'from', operator: 'contains', value: '' }],
    });
  };

  const removeCondition = (index) => {
    const newConditions = formData.conditions.filter((_, i) => i !== index);
    setFormData({ ...formData, conditions: newConditions });
  };

  const handleActionChange = (index, field, value) => {
    const newActions = [...formData.actions];
    newActions[index][field] = value;
    setFormData({ ...formData, actions: newActions });
  };

  const addAction = () => {
    setFormData({
      ...formData,
      actions: [...formData.actions, { type: 'addLabel', value: '' }],
    });
  };

  const removeAction = (index) => {
    const newActions = formData.actions.filter((_, i) => i !== index);
    setFormData({ ...formData, actions: newActions });
  };

  const handleSubmit = (e) => {
    e.preventDefault();
    onSave(formData);
  };

  return (
    <div className="rule-form-container">
      <form onSubmit={handleSubmit} className="rule-form">
        <h2>{rule ? 'Modifier la règle' : 'Nouvelle règle'}</h2>

        <div className="form-group">
          <label>Nom de la règle</label>
          <input
            type="text"
            name="name"
            value={formData.name}
            onChange={handleChange}
            required
            placeholder="Ex: Trier emails professionnels"
          />
        </div>

        <div className="form-group">
          <label>Description</label>
          <textarea
            name="description"
            value={formData.description}
            onChange={handleChange}
            placeholder="Description optionnelle"
            rows="2"
          />
        </div>

        <div className="form-group">
          <label>Conditions</label>
          {formData.conditions.map((condition, index) => (
            <div key={index} className="condition-row">
              <select
                value={condition.field}
                onChange={(e) => handleConditionChange(index, 'field', e.target.value)}
              >
                <option value="from">De</option>
                <option value="to">À</option>
                <option value="subject">Objet</option>
                <option value="body">Corps</option>
              </select>

              <select
                value={condition.operator}
                onChange={(e) => handleConditionChange(index, 'operator', e.target.value)}
              >
                <option value="contains">Contient</option>
                <option value="equals">Égal à</option>
                <option value="startsWith">Commence par</option>
                <option value="endsWith">Finit par</option>
              </select>

              <input
                type="text"
                value={condition.value}
                onChange={(e) => handleConditionChange(index, 'value', e.target.value)}
                placeholder="Valeur"
                required
              />

              {formData.conditions.length > 1 && (
                <button type="button" onClick={() => removeCondition(index)} className="btn-remove">
                  ✕
                </button>
              )}
            </div>
          ))}
          <button type="button" onClick={addCondition} className="btn-add">
            + Ajouter une condition
          </button>
        </div>

        <div className="form-group">
          <label>Actions</label>
          {formData.actions.map((action, index) => (
            <div key={index} className="action-row">
              <select
                value={action.type}
                onChange={(e) => handleActionChange(index, 'type', e.target.value)}
              >
                <option value="addLabel">Ajouter un libellé</option>
                <option value="removeLabel">Retirer un libellé</option>
                <option value="markAsRead">Marquer comme lu</option>
                <option value="archive">Archiver</option>
              </select>

              {(action.type === 'addLabel' || action.type === 'removeLabel') && (
                <input
                  type="text"
                  value={action.value}
                  onChange={(e) => handleActionChange(index, 'value', e.target.value)}
                  placeholder="Nom du libellé"
                  required={action.type === 'addLabel' || action.type === 'removeLabel'}
                />
              )}

              {formData.actions.length > 1 && (
                <button type="button" onClick={() => removeAction(index)} className="btn-remove">
                  ✕
                </button>
              )}
            </div>
          ))}
          <button type="button" onClick={addAction} className="btn-add">
            + Ajouter une action
          </button>
        </div>

        <div className="form-group">
          <label>Priorité</label>
          <input
            type="number"
            name="priority"
            value={formData.priority}
            onChange={handleChange}
            min="1"
            max="100"
          />
        </div>

        <div className="form-group">
          <label className="checkbox-label">
            <input
              type="checkbox"
              name="enabled"
              checked={formData.enabled}
              onChange={handleChange}
            />
            Règle activée
          </label>
        </div>

        <div className="form-actions">
          <button type="button" onClick={onCancel} className="btn-secondary">
            Annuler
          </button>
          <button type="submit" className="btn-primary">
            {rule ? 'Mettre à jour' : 'Créer'}
          </button>
        </div>
      </form>
    </div>
  );
}

export default RuleForm;
