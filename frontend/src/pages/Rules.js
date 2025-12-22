import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ruleService } from '../services/api';
import RuleForm from '../components/RuleForm';
import RuleList from '../components/RuleList';
import '../styles/Rules.css';

function Rules() {
  const navigate = useNavigate();
  const [rules, setRules] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showForm, setShowForm] = useState(false);
  const [editingRule, setEditingRule] = useState(null);

  useEffect(() => {
    const userEmail = localStorage.getItem('userEmail');
    if (!userEmail) {
      navigate('/');
      return;
    }
    fetchRules();
  }, [navigate]);

  const fetchRules = async () => {
    setLoading(true);
    setError('');
    try {
      const response = await ruleService.getRules();
      setRules(response.data || []);
    } catch (err) {
      setError('Erreur lors de la récupération des règles: ' + err.message);
    } finally {
      setLoading(false);
    }
  };

  const handleCreateRule = async (rule) => {
    try {
      await ruleService.createRule(rule);
      setShowForm(false);
      fetchRules();
    } catch (err) {
      alert('Erreur lors de la création de la règle: ' + err.message);
    }
  };

  const handleUpdateRule = async (id, rule) => {
    try {
      await ruleService.updateRule(id, rule);
      setEditingRule(null);
      fetchRules();
    } catch (err) {
      alert('Erreur lors de la mise à jour de la règle: ' + err.message);
    }
  };

  const handleDeleteRule = async (id) => {
    if (!window.confirm('Êtes-vous sûr de vouloir supprimer cette règle ?')) {
      return;
    }
    try {
      await ruleService.deleteRule(id);
      fetchRules();
    } catch (err) {
      alert('Erreur lors de la suppression de la règle: ' + err.message);
    }
  };

  const handleEdit = (rule) => {
    setEditingRule(rule);
    setShowForm(true);
  };

  const handleCancel = () => {
    setShowForm(false);
    setEditingRule(null);
  };

  return (
    <div className="rules-container">
      <header className="rules-header">
        <h1>Règles de tri</h1>
        <div className="header-actions">
          <button onClick={() => navigate('/emails')} className="btn-secondary">
            Retour aux emails
          </button>
          <button onClick={() => setShowForm(true)} className="btn-primary">
            Nouvelle règle
          </button>
        </div>
      </header>

      <div className="rules-content">
        {error && <div className="error-message">{error}</div>}

        {showForm && (
          <RuleForm
            rule={editingRule}
            onSave={editingRule ? (rule) => handleUpdateRule(editingRule.id, rule) : handleCreateRule}
            onCancel={handleCancel}
          />
        )}

        {loading ? (
          <div className="loading">Chargement des règles...</div>
        ) : (
          <RuleList
            rules={rules}
            onEdit={handleEdit}
            onDelete={handleDeleteRule}
          />
        )}
      </div>
    </div>
  );
}

export default Rules;
