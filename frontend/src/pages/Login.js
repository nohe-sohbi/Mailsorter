import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { authService } from '../services/api';
import '../styles/Login.css';

function Login() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    // Redirect if already logged in
    const userEmail = localStorage.getItem('userEmail');
    if (userEmail) {
      navigate('/inbox');
      return;
    }

    const code = searchParams.get('code');
    if (code) {
      handleCallback(code);
    }
  }, [searchParams, navigate]);

  const handleCallback = async (code) => {
    setLoading(true);
    setError('');
    try {
      const response = await authService.handleCallback(code);
      localStorage.setItem('userEmail', response.data.userEmail);
      localStorage.setItem('accessToken', response.data.accessToken);
      navigate('/inbox');
    } catch (err) {
      setError('Erreur lors de l\'authentification: ' + err.message);
      setLoading(false);
    }
  };

  const handleLogin = async () => {
    setLoading(true);
    setError('');
    try {
      const response = await authService.getAuthUrl();
      window.location.href = response.data.authUrl;
    } catch (err) {
      setError('Erreur lors de la connexion: ' + err.message);
      setLoading(false);
    }
  };

  return (
    <div className="login-container">
      <div className="login-card">
        <h1>Mailsorter</h1>
        <p>Triez automatiquement vos emails Gmail avec l'IA</p>

        {error && <div className="error-message">{error}</div>}

        <button
          onClick={handleLogin}
          disabled={loading}
          className="login-button"
        >
          {loading ? 'Connexion en cours...' : 'Se connecter avec Gmail'}
        </button>
      </div>
    </div>
  );
}

export default Login;
