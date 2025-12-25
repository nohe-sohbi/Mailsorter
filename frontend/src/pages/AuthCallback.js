import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { authService } from '../services/api';
import '../styles/Login.css';

function AuthCallback() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [error, setError] = useState('');

  useEffect(() => {
    const code = searchParams.get('code');
    const errorParam = searchParams.get('error');

    if (errorParam) {
      setError(`Erreur OAuth: ${errorParam}`);
      return;
    }

    if (code) {
      handleCallback(code);
    } else {
      setError('Code d\'autorisation manquant');
    }
  }, [searchParams]);

  const handleCallback = async (code) => {
    try {
      const response = await authService.handleCallback(code);
      localStorage.setItem('userEmail', response.data.userEmail);
      localStorage.setItem('accessToken', response.data.accessToken);
      navigate('/emails');
    } catch (err) {
      const errorMessage = err.response?.data?.error || err.message;
      setError(`Erreur lors de l'authentification: ${errorMessage}`);
    }
  };

  if (error) {
    return (
      <div className="login-container">
        <div className="login-card">
          <h1>Erreur d'authentification</h1>
          <div className="error-message">{error}</div>
          <button
            onClick={() => navigate('/')}
            className="login-button"
          >
            Retour à la connexion
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="login-container">
      <div className="login-card">
        <h1>Authentification en cours...</h1>
        <div className="spinner"></div>
        <p>Veuillez patienter</p>
      </div>
    </div>
  );
}

export default AuthCallback;
