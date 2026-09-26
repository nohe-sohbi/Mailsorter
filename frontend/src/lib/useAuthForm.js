import { useCallback, useState } from 'react';
import { authService, apiError } from '../services/api';
import { track } from './analytics';

// Sign-up, sign-in and the Google hand-off, shared by both landings so a fix to
// one door is a fix to both. It owns the state and the side effects; each
// landing only decides how the form looks.
//
// onSignedIn receives where to go next: /connect after a sign-up (the account
// has no mailbox yet), /inbox after a sign-in (RequireMailbox sends it on to
// /connect if the account still has none).
export function useAuthForm(onSignedIn) {
  const [mode, setModeState] = useState('register');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [googleBusy, setGoogleBusy] = useState(false);
  const [error, setError] = useState('');

  const setMode = useCallback((next) => {
    setModeState(next);
    setError('');
  }, []);

  const submit = async (event) => {
    if (event) event.preventDefault();
    if (busy || googleBusy) return;
    setBusy(true);
    setError('');
    try {
      const call = mode === 'register' ? authService.register : authService.login;
      const { data } = await call(email.trim(), password);
      localStorage.setItem('userEmail', data.userEmail);
      localStorage.setItem('accessToken', data.accessToken);
      localStorage.removeItem('hasMailbox');
      track(mode === 'register' ? 'register_done' : 'login_done');
      onSignedIn(mode === 'register' ? '/connect' : '/inbox');
    } catch (err) {
      setError(apiError(err, 'Une erreur est survenue. Vérifiez vos identifiants.'));
      setBusy(false);
    }
  };

  const startGoogle = async () => {
    setGoogleBusy(true);
    setError('');
    try {
      const response = await authService.getAuthUrl();
      track('login_start', { provider: 'google' });
      window.location.href = response.data.authUrl;
    } catch {
      setError('Impossible de démarrer la connexion Google. Réessayez.');
      setGoogleBusy(false);
    }
  };

  return { mode, setMode, email, setEmail, password, setPassword, busy, googleBusy, error, submit, startGoogle };
}
