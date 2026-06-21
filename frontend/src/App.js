import React, { useEffect, useState } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';
import Inbox from './pages/Inbox';
import Setup from './pages/Setup';
import Settings from './pages/Settings';
import Rules from './pages/Rules';
import Snoozed from './pages/Snoozed';
import Pricing from './pages/Pricing';
import AuthCallback from './pages/AuthCallback';
import Header from './components/Header';
import { EmailProvider } from './contexts/EmailContext';
import { ToastProvider } from './ui/Toast';
import { configService } from './services/api';
import { Logo, Alert } from './ui/icons';
import Spinner from './ui/Spinner';

function BootScreen({ children }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-6 bg-ink-50 bg-mesh px-6 text-center">
      {children}
    </div>
  );
}

function App() {
  const [isConfigured, setIsConfigured] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    checkConfiguration();
  }, []);

  const checkConfiguration = async () => {
    try {
      setError(null);
      const response = await configService.getStatus();
      setIsConfigured(response.data.isConfigured);
    } catch (err) {
      setError('Connexion au serveur impossible.');
      setIsConfigured(false);
    }
  };

  if (isConfigured === null) {
    return (
      <BootScreen>
        <div className="animate-float">
          <Logo size={56} />
        </div>
        <div className="flex items-center gap-3 text-ink-500">
          <Spinner size={18} className="text-brand-500" />
          <span className="text-sm font-medium">Démarrage de Mailsorter…</span>
        </div>
      </BootScreen>
    );
  }

  if (error && !isConfigured) {
    return (
      <BootScreen>
        <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-rose-50 text-rose-500">
          <Alert size={28} />
        </span>
        <div className="max-w-sm space-y-2">
          <h1 className="text-xl font-bold text-ink-900">Le moteur ne répond pas</h1>
          <p className="text-sm text-ink-500">
            {error} Vérifiez que le backend tourne, puis réessayez.
          </p>
        </div>
        <button onClick={checkConfiguration} className="btn-primary">
          Réessayer
        </button>
      </BootScreen>
    );
  }

  return (
    <Router>
      <ToastProvider>
        <EmailProvider>
          <div className="min-h-screen bg-ink-50">
            <Header />
            <Routes>
              <Route
                path="/setup"
                element={
                  isConfigured ? <Navigate to="/" replace /> : <Setup onComplete={() => setIsConfigured(true)} />
                }
              />
              <Route path="/" element={isConfigured ? <Login /> : <Navigate to="/setup" replace />} />
              <Route path="/inbox" element={isConfigured ? <Inbox /> : <Navigate to="/setup" replace />} />
              <Route path="/rules" element={isConfigured ? <Rules /> : <Navigate to="/setup" replace />} />
              <Route path="/snoozed" element={isConfigured ? <Snoozed /> : <Navigate to="/setup" replace />} />
              <Route path="/settings" element={isConfigured ? <Settings /> : <Navigate to="/setup" replace />} />
              <Route path="/pricing" element={<Pricing />} />
              <Route path="/auth/callback" element={<AuthCallback />} />
              {/* Redirects for legacy routes */}
              <Route path="/emails" element={<Navigate to="/inbox" replace />} />
              <Route path="/triage" element={<Navigate to="/inbox" replace />} />
            </Routes>
          </div>
        </EmailProvider>
      </ToastProvider>
    </Router>
  );
}

export default App;
