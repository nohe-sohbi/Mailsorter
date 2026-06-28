import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { authService } from '../services/api';
import { Logo, Alert } from '../ui/icons';
import Spinner from '../ui/Spinner';

function AuthCallback() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const [error, setError] = useState('');

  useEffect(() => {
    const code = searchParams.get('code');
    const state = searchParams.get('state');
    const errorParam = searchParams.get('error');

    if (errorParam) {
      setError(`Connexion Google refusée (${errorParam}).`);
      return;
    }
    if (code) {
      handleCallback(code, state);
    } else {
      setError("Code d'autorisation manquant.");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams]);

  const handleCallback = async (code, state) => {
    try {
      const response = await authService.handleCallback(code, state);
      localStorage.setItem('userEmail', response.data.userEmail);
      localStorage.setItem('accessToken', response.data.accessToken);
      navigate('/inbox');
    } catch (err) {
      const msg = err.response?.data?.error || err.message;
      setError(`Échec de l'authentification : ${msg}`);
    }
  };

  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-6 bg-ink-50 px-6 text-center">
      {error ? (
        <div className="card w-full max-w-md animate-scale-in p-8">
          <span className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-rose-50 text-rose-500">
            <Alert size={28} />
          </span>
          <h1 className="text-xl font-bold text-ink-900">Connexion interrompue</h1>
          <p className="mt-2 text-sm text-ink-500">{error}</p>
          <button onClick={() => navigate('/')} className="btn-primary mt-6 w-full">
            Retour à l'accueil
          </button>
        </div>
      ) : (
        <>
          <div className="animate-fade-up">
            <Logo size={52} />
          </div>
          <div className="flex items-center gap-3 text-ink-500">
            <Spinner size={18} className="text-brand-500" />
            <span className="text-sm font-medium">Finalisation de la connexion Google…</span>
          </div>
        </>
      )}
    </div>
  );
}

export default AuthCallback;
